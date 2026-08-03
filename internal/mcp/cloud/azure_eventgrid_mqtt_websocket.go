package cloud

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
)

const (
	authSchemeAzureEventGridMQTTWS   = "eventgrid-mqtt-ws"
	azureEventGridMQTTMaxPacketBytes = 512 * 1024
	azureEventGridMQTTMaxTopics      = 16
	azureEventGridMQTTMaxPublishes   = 64
	azureEventGridMQTTMaxMessages    = 256
	azureEventGridMQTTMaxKeepAlive   = 1160
	azureEventGridMQTTMaxTimeout     = 300
	azureEventGridMQTTSubscribeID    = 1
	azureEventGridMQTTAuthMethod     = "OAUTH2-JWT"
	azureEventGridScope              = "https://eventgrid.azure.net/.default"
)

type azureEventGridMQTTSubscription struct {
	TopicFilter string `json:"topic_filter"`
	QoS         int    `json:"qos"`
}

type azureEventGridMQTTPublish struct {
	Topic                 string            `json:"topic"`
	QoS                   int               `json:"qos"`
	Retain                bool              `json:"retain,omitempty"`
	PayloadBase64         string            `json:"payload_base64"`
	PayloadFormat         int               `json:"payload_format,omitempty"`
	ContentType           string            `json:"content_type,omitempty"`
	MessageExpirySeconds  uint32            `json:"message_expiry_seconds,omitempty"`
	ResponseTopic         string            `json:"response_topic,omitempty"`
	CorrelationDataBase64 string            `json:"correlation_data_base64,omitempty"`
	UserProperties        map[string]string `json:"user_properties,omitempty"`
	TopicAlias            uint16            `json:"topic_alias,omitempty"`

	Payload         []byte `json:"-"`
	CorrelationData []byte `json:"-"`
}

type azureEventGridMQTTWill struct {
	azureEventGridMQTTPublish
	DelaySeconds uint32 `json:"delay_seconds,omitempty"`
}

func (will *azureEventGridMQTTWill) UnmarshalJSON(data []byte) error {
	type wire struct {
		Topic                 string            `json:"topic"`
		QoS                   int               `json:"qos"`
		Retain                bool              `json:"retain,omitempty"`
		PayloadBase64         string            `json:"payload_base64"`
		PayloadFormat         int               `json:"payload_format,omitempty"`
		ContentType           string            `json:"content_type,omitempty"`
		MessageExpirySeconds  uint32            `json:"message_expiry_seconds,omitempty"`
		ResponseTopic         string            `json:"response_topic,omitempty"`
		CorrelationDataBase64 string            `json:"correlation_data_base64,omitempty"`
		UserProperties        map[string]string `json:"user_properties,omitempty"`
		DelaySeconds          uint32            `json:"delay_seconds,omitempty"`
	}
	var value wire
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || ensureJSONDecoderEOF(decoder) != nil {
		return fmt.Errorf("invalid Event Grid MQTT Last Will")
	}
	will.azureEventGridMQTTPublish = azureEventGridMQTTPublish{
		Topic: value.Topic, QoS: value.QoS, Retain: value.Retain, PayloadBase64: value.PayloadBase64,
		PayloadFormat: value.PayloadFormat, ContentType: value.ContentType, MessageExpirySeconds: value.MessageExpirySeconds,
		ResponseTopic: value.ResponseTopic, CorrelationDataBase64: value.CorrelationDataBase64, UserProperties: value.UserProperties,
	}
	will.DelaySeconds = value.DelaySeconds
	return nil
}

type azureEventGridMQTTPlan struct {
	ClientID                   string                           `json:"client_id"`
	Username                   string                           `json:"username,omitempty"`
	CleanStart                 bool                             `json:"clean_start,omitempty"`
	SessionExpirySeconds       uint32                           `json:"session_expiry_seconds,omitempty"`
	SubscriptionIdentifier     uint32                           `json:"subscription_identifier,omitempty"`
	Subscriptions              []azureEventGridMQTTSubscription `json:"subscriptions,omitempty"`
	Publishes                  []azureEventGridMQTTPublish      `json:"publishes,omitempty"`
	Will                       *azureEventGridMQTTWill          `json:"will,omitempty"`
	ReceiveMaximum             uint16                           `json:"receive_maximum,omitempty"`
	MaximumPacketSize          uint32                           `json:"maximum_packet_size,omitempty"`
	TopicAliasMaximum          uint16                           `json:"topic_alias_maximum,omitempty"`
	KeepAliveSeconds           int                              `json:"keep_alive_seconds"`
	MaxMessages                int                              `json:"max_messages"`
	TimeoutSeconds             int                              `json:"timeout_seconds"`
	ReauthenticateAfterSeconds int                              `json:"reauthenticate_after_seconds,omitempty"`
}

type azureEventGridMQTTConnAck struct {
	azureWebPubSubMQTTConnAckCapabilities
	AssignedClientID                 string
	TopicAliasMaximum                uint16
	RetainAvailable                  bool
	WildcardSubscriptionsAvailable   bool
	SubscriptionIdentifiersAvailable bool
	SharedSubscriptionsAvailable     bool
}

type azureEventGridMQTTPacketReader struct {
	connection cloudWebSocketConnection
	buffer     []byte
	maximum    uint32
}

func validateAzureEventGridMQTTInvocation(invocation Invocation, allowedHosts []string) error {
	operation := strings.ToLower(strings.TrimSpace(invocation.Operation))
	mutating := operation == "clientmqtt"
	if !strings.EqualFold(invocation.Service, "eventgrid") || operation != "subscribemqtt" && !mutating || !strings.EqualFold(invocation.Method, http.MethodGet) {
		return fmt.Errorf("Azure Event Grid MQTT requires GET, service eventgrid, and operation SubscribeMQTT or ClientMQTT")
	}
	if mutating && invocation.Mode != ModeMutate || !mutating && invocation.Mode != ModeRead {
		return fmt.Errorf("Azure Event Grid MQTT ClientMQTT requires the mutate tool and SubscribeMQTT requires the read tool")
	}
	if _, err := parseAzureEventGridMQTTTarget(invocation.URL, allowedHosts); err != nil {
		return err
	}
	if len(invocation.Headers) != 0 || len(invocation.Parameters) != 0 || invocation.Body == nil || invocation.BodyFile != "" || invocation.ResponseFile == "" {
		return fmt.Errorf("Azure Event Grid MQTT requires only a finite credential-free protocol body and response_file")
	}
	if invocation.Region != "" || invocation.RegionSet != "" || invocation.Project != "" || invocation.Subscription != "" || invocation.Audience != "" || invocation.APIVersion != "" || invocation.AuthVersion != "" || invocation.PayloadMode != "" || invocation.ChecksumAlgorithm != "" || invocation.StreamChunkBytes != 0 || invocation.StreamIntervalMS != 0 || invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return fmt.Errorf("Azure Event Grid MQTT does not accept REST, cross-provider, or generic stream controls")
	}
	_, err := parseAzureEventGridMQTTPlan(invocation.Body, mutating)
	return err
}

func parseAzureEventGridMQTTTarget(rawURL string, allowedHosts []string) (*url.URL, error) {
	target, err := url.Parse(rawURL)
	if err != nil || target.Scheme != "wss" || target.User != nil || target.Fragment != "" || target.RawQuery != "" || target.Hostname() == "" || target.EscapedPath() != "/mqtt" || target.Port() != "" && target.Port() != "443" {
		return nil, fmt.Errorf("Azure Event Grid MQTT requires the exact credential-free wss://host/mqtt endpoint on port 443")
	}
	host := strings.ToLower(target.Hostname())
	official := false
	const suffix = ".eventgrid.azure.net"
	if prefix := strings.TrimSuffix(host, suffix); prefix != host {
		parts := strings.Split(prefix, ".")
		official = len(parts) == 2 && endpointLabelPattern.MatchString(parts[0]) && endpointLabelPattern.MatchString(parts[1])
	}
	if !official {
		for _, candidate := range allowedHosts {
			if validAdditionalEndpointHost(candidate) && host == strings.ToLower(candidate) {
				official = true
				break
			}
		}
	}
	if !official {
		return nil, fmt.Errorf("Azure Event Grid MQTT host must be namespace.region.eventgrid.azure.net or an operator-pinned custom domain")
	}
	return target, nil
}

func parseAzureEventGridMQTTPlan(body any, mutating bool) (azureEventGridMQTTPlan, error) {
	if body == nil || azureRealtimeContainsCredentialField(body) {
		return azureEventGridMQTTPlan{}, fmt.Errorf("Azure Event Grid MQTT requires a credential-free protocol body")
	}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) == 0 || len(encoded) > maxRequestPayloadBytes {
		return azureEventGridMQTTPlan{}, fmt.Errorf("Azure Event Grid MQTT body must be bounded JSON")
	}
	plan := azureEventGridMQTTPlan{CleanStart: true}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&plan) != nil || ensureJSONDecoderEOF(decoder) != nil {
		return azureEventGridMQTTPlan{}, fmt.Errorf("Azure Event Grid MQTT body does not match the finite MQTT v5 schema")
	}
	if plan.ClientID != "" && (!validAzureWebPubSubMQTTUTF8([]byte(plan.ClientID)) || len(plan.ClientID) > 128) {
		return azureEventGridMQTTPlan{}, fmt.Errorf("Azure Event Grid MQTT client_id must be empty for assignment or a bounded MQTT UTF-8 session name")
	}
	if plan.Username != "" && (!validAzureWebPubSubMQTTUTF8([]byte(plan.Username)) || len(plan.Username) > 128) {
		return azureEventGridMQTTPlan{}, fmt.Errorf("Azure Event Grid MQTT username must be a bounded MQTT UTF-8 authentication name")
	}
	if plan.ClientID == "" && (!plan.CleanStart || plan.SessionExpirySeconds != 0) {
		return azureEventGridMQTTPlan{}, fmt.Errorf("Azure Event Grid MQTT assigned client IDs require clean_start and a nonpersistent initial session")
	}
	if plan.SessionExpirySeconds > 8*60*60 {
		return azureEventGridMQTTPlan{}, fmt.Errorf("Azure Event Grid MQTT session_expiry_seconds must be at most eight hours")
	}
	if len(plan.Subscriptions) > azureEventGridMQTTMaxTopics || len(plan.Publishes) > azureEventGridMQTTMaxPublishes {
		return azureEventGridMQTTPlan{}, fmt.Errorf("Azure Event Grid MQTT topic or publish count exceeds the finite bound")
	}
	if !mutating && len(plan.Subscriptions) == 0 {
		return azureEventGridMQTTPlan{}, fmt.Errorf("Azure Event Grid MQTT read mode requires at least one subscription")
	}
	if !mutating && (len(plan.Publishes) != 0 || plan.Will != nil || !plan.CleanStart || plan.SessionExpirySeconds != 0) {
		return azureEventGridMQTTPlan{}, fmt.Errorf("Azure Event Grid MQTT read mode forbids publish, Last Will, and persistent-session behavior")
	}
	if mutating && len(plan.Subscriptions) == 0 && len(plan.Publishes) == 0 {
		return azureEventGridMQTTPlan{}, fmt.Errorf("Azure Event Grid MQTT ClientMQTT requires a subscription or publish")
	}
	seenFilters := make(map[string]bool)
	for _, subscription := range plan.Subscriptions {
		if !validAzureEventGridMQTTTopicFilter(subscription.TopicFilter) || seenFilters[subscription.TopicFilter] || subscription.QoS < 0 || subscription.QoS > 1 {
			return azureEventGridMQTTPlan{}, fmt.Errorf("Azure Event Grid MQTT subscription must use a unique bounded topic filter and QoS 0 or 1")
		}
		seenFilters[subscription.TopicFilter] = true
	}
	if plan.SubscriptionIdentifier > 268435455 || plan.SubscriptionIdentifier != 0 && len(plan.Subscriptions) == 0 {
		return azureEventGridMQTTPlan{}, fmt.Errorf("Azure Event Grid MQTT subscription_identifier is invalid")
	}
	aliases := make(map[uint16]bool)
	for index := range plan.Publishes {
		publish := &plan.Publishes[index]
		allowEmptyTopic := publish.TopicAlias != 0 && aliases[publish.TopicAlias]
		if err := validateAzureEventGridMQTTPublish(publish, allowEmptyTopic); err != nil {
			return azureEventGridMQTTPlan{}, fmt.Errorf("Azure Event Grid MQTT publish %d: %w", index, err)
		}
		if publish.TopicAlias > 0 && publish.Topic != "" {
			aliases[publish.TopicAlias] = true
		}
	}
	if plan.Will != nil {
		if err := validateAzureEventGridMQTTPublish(&plan.Will.azureEventGridMQTTPublish, false); err != nil || plan.Will.TopicAlias != 0 || plan.Will.DelaySeconds > 8*60*60 {
			return azureEventGridMQTTPlan{}, fmt.Errorf("Azure Event Grid MQTT Last Will is invalid")
		}
	}
	if plan.ReceiveMaximum == 0 {
		plan.ReceiveMaximum = 64
	}
	if plan.MaximumPacketSize == 0 {
		plan.MaximumPacketSize = azureEventGridMQTTMaxPacketBytes
	}
	if plan.MaximumPacketSize < 4 || plan.MaximumPacketSize > azureEventGridMQTTMaxPacketBytes || plan.TopicAliasMaximum > 10 {
		return azureEventGridMQTTPlan{}, fmt.Errorf("Azure Event Grid MQTT flow-control values exceed broker limits")
	}
	if plan.KeepAliveSeconds < 1 || plan.KeepAliveSeconds > azureEventGridMQTTMaxKeepAlive || plan.TimeoutSeconds < 1 || plan.TimeoutSeconds > azureEventGridMQTTMaxTimeout || plan.MaxMessages < 0 || plan.MaxMessages > azureEventGridMQTTMaxMessages || len(plan.Subscriptions) > 0 && plan.MaxMessages < 1 {
		return azureEventGridMQTTPlan{}, fmt.Errorf("Azure Event Grid MQTT message count, keep alive, or timeout is outside the finite bound")
	}
	if plan.ReauthenticateAfterSeconds != 0 && (plan.ReauthenticateAfterSeconds < 1 || plan.ReauthenticateAfterSeconds > plan.TimeoutSeconds) {
		return azureEventGridMQTTPlan{}, fmt.Errorf("Azure Event Grid MQTT reauthentication must occur within the finite session")
	}
	for index, publish := range plan.Publishes {
		packetID := uint16(0)
		if publish.QoS == 1 {
			packetID = uint16(index + 2)
		}
		if encoded, err := encodeAzureEventGridMQTTPublish(publish, packetID); err != nil || len(encoded) > azureEventGridMQTTMaxPacketBytes {
			return azureEventGridMQTTPlan{}, fmt.Errorf("Azure Event Grid MQTT publish %d exceeds the packet bound", index)
		}
	}
	return plan, nil
}

func validAzureEventGridMQTTTopicFilter(value string) bool {
	if !validAWSIoTMQTTString(value, 1024) {
		return false
	}
	filter := value
	if strings.HasPrefix(filter, "$share/") {
		rest := strings.TrimPrefix(filter, "$share/")
		group, nested, ok := strings.Cut(rest, "/")
		if !ok || group == "" || strings.ContainsAny(group, "#+") || strings.HasPrefix(nested, "$share/") {
			return false
		}
		filter = nested
	}
	parts := strings.Split(filter, "/")
	for index, part := range parts {
		if strings.Contains(part, "#") && (part != "#" || index != len(parts)-1) || strings.Contains(part, "+") && part != "+" {
			return false
		}
	}
	return true
}

func validateAzureEventGridMQTTPublish(publish *azureEventGridMQTTPublish, allowEmptyTopic bool) error {
	if publish.Topic == "" && !allowEmptyTopic || publish.Topic != "" && (!validAWSIoTMQTTString(publish.Topic, 1024) || strings.ContainsAny(publish.Topic, "#+")) {
		return fmt.Errorf("topic must be a bounded MQTT topic, or reuse an established alias")
	}
	if publish.QoS < 0 || publish.QoS > 1 || publish.PayloadFormat < 0 || publish.PayloadFormat > 1 || publish.TopicAlias > 10 {
		return fmt.Errorf("QoS, payload format, or topic alias exceeds Event Grid MQTT v5 limits")
	}
	payload, err := base64.StdEncoding.Strict().DecodeString(publish.PayloadBase64)
	if err != nil || len(payload) > azureEventGridMQTTMaxPacketBytes {
		return fmt.Errorf("payload_base64 is invalid or too large")
	}
	if publish.PayloadFormat == 1 && !utf8.Valid(payload) {
		return fmt.Errorf("UTF-8 payload format requires UTF-8 data")
	}
	publish.Payload = payload
	if publish.ContentType != "" && (!validAzureWebPubSubMQTTUTF8([]byte(publish.ContentType)) || len(publish.ContentType) > 256) || publish.ResponseTopic != "" && (!validAWSIoTMQTTString(publish.ResponseTopic, 1024) || strings.ContainsAny(publish.ResponseTopic, "#+")) {
		return fmt.Errorf("content_type or response_topic is invalid")
	}
	if publish.CorrelationDataBase64 != "" {
		publish.CorrelationData, err = base64.StdEncoding.Strict().DecodeString(publish.CorrelationDataBase64)
		if err != nil || len(publish.CorrelationData) > 4096 {
			return fmt.Errorf("correlation_data_base64 is invalid or too large")
		}
	}
	if len(publish.UserProperties) > 16 {
		return fmt.Errorf("user_properties exceeds the bounded count")
	}
	for key, value := range publish.UserProperties {
		if !validAzureWebPubSubMQTTUTF8([]byte(key)) || !validAzureWebPubSubMQTTUTF8([]byte(value)) || len(key) > 256 || len(value) > 1024 {
			return fmt.Errorf("user_properties contains invalid MQTT UTF-8")
		}
	}
	return nil
}

func encodeAzureEventGridMQTTConnect(plan azureEventGridMQTTPlan, token string) ([]byte, error) {
	if strings.TrimSpace(token) == "" || len(token) > 16384 {
		return nil, fmt.Errorf("Azure Event Grid identity returned an invalid access token")
	}
	flags := byte(0)
	if plan.CleanStart {
		flags |= 0x02
	}
	if plan.Will != nil {
		flags |= 0x04 | byte(plan.Will.QoS)<<3
		if plan.Will.Retain {
			flags |= 0x20
		}
	}
	if plan.Username != "" {
		flags |= 0x80
	}
	body := []byte{0x00, 0x04, 'M', 'Q', 'T', 'T', 0x05, flags, byte(plan.KeepAliveSeconds >> 8), byte(plan.KeepAliveSeconds)}
	properties := make([]byte, 0, 64+len(token))
	if plan.SessionExpirySeconds > 0 {
		properties = append(properties, 0x11)
		properties = appendMQTTUint32(properties, plan.SessionExpirySeconds)
	}
	properties = append(properties, 0x21, byte(plan.ReceiveMaximum>>8), byte(plan.ReceiveMaximum))
	properties = append(properties, 0x27)
	properties = appendMQTTUint32(properties, plan.MaximumPacketSize)
	properties = append(properties, 0x22, byte(plan.TopicAliasMaximum>>8), byte(plan.TopicAliasMaximum))
	properties = append(properties, 0x15)
	properties = appendMQTTUTF8(properties, azureEventGridMQTTAuthMethod)
	properties = append(properties, 0x16)
	properties = appendMQTTBinary(properties, []byte(token))
	body = appendMQTTVariableByteInteger(body, len(properties))
	body = append(body, properties...)
	body = appendMQTTUTF8(body, plan.ClientID)
	if plan.Will != nil {
		willProperties := encodeAzureEventGridMQTTApplicationProperties(plan.Will.azureEventGridMQTTPublish)
		if plan.Will.DelaySeconds > 0 {
			willProperties = append([]byte{0x18, byte(plan.Will.DelaySeconds >> 24), byte(plan.Will.DelaySeconds >> 16), byte(plan.Will.DelaySeconds >> 8), byte(plan.Will.DelaySeconds)}, willProperties...)
		}
		body = appendMQTTVariableByteInteger(body, len(willProperties))
		body = append(body, willProperties...)
		body = appendMQTTUTF8(body, plan.Will.Topic)
		body = appendMQTTBinary(body, plan.Will.Payload)
	}
	if plan.Username != "" {
		body = appendMQTTUTF8(body, plan.Username)
	}
	return encodeAzureEventGridMQTTPacket(0x10, body)
}

func encodeAzureEventGridMQTTReauthenticate(token string) ([]byte, error) {
	if strings.TrimSpace(token) == "" || len(token) > 16384 {
		return nil, fmt.Errorf("Azure Event Grid identity returned an invalid access token")
	}
	properties := []byte{0x15}
	properties = appendMQTTUTF8(properties, azureEventGridMQTTAuthMethod)
	properties = append(properties, 0x16)
	properties = appendMQTTBinary(properties, []byte(token))
	body := appendMQTTVariableByteInteger([]byte{0x19}, len(properties))
	body = append(body, properties...)
	return encodeAzureEventGridMQTTPacket(0xf0, body)
}

func encodeAzureEventGridMQTTSubscribe(plan azureEventGridMQTTPlan) ([]byte, error) {
	body := []byte{0, azureEventGridMQTTSubscribeID}
	properties := make([]byte, 0, 5)
	if plan.SubscriptionIdentifier > 0 {
		properties = append(properties, 0x0b)
		properties = appendMQTTVariableByteInteger(properties, int(plan.SubscriptionIdentifier))
	}
	body = appendMQTTVariableByteInteger(body, len(properties))
	body = append(body, properties...)
	for _, subscription := range plan.Subscriptions {
		body = appendMQTTUTF8(body, subscription.TopicFilter)
		body = append(body, byte(subscription.QoS))
	}
	return encodeAzureEventGridMQTTPacket(0x82, body)
}

func encodeAzureEventGridMQTTPublish(publish azureEventGridMQTTPublish, packetID uint16) ([]byte, error) {
	body := appendMQTTUTF8(nil, publish.Topic)
	if publish.QoS == 1 {
		if packetID == 0 {
			return nil, fmt.Errorf("Azure Event Grid MQTT QoS 1 publish requires packet ID")
		}
		body = append(body, byte(packetID>>8), byte(packetID))
	}
	properties := encodeAzureEventGridMQTTApplicationProperties(publish)
	body = appendMQTTVariableByteInteger(body, len(properties))
	body = append(body, properties...)
	body = append(body, publish.Payload...)
	header := byte(0x30 | byte(publish.QoS)<<1)
	if publish.Retain {
		header |= 0x01
	}
	return encodeAzureEventGridMQTTPacket(header, body)
}

func encodeAzureEventGridMQTTApplicationProperties(publish azureEventGridMQTTPublish) []byte {
	properties := encodeAzureWebPubSubMQTT5ApplicationProperties(publish.PayloadFormat, publish.ContentType, publish.MessageExpirySeconds)
	if publish.ResponseTopic != "" {
		properties = append(properties, 0x08)
		properties = appendMQTTUTF8(properties, publish.ResponseTopic)
	}
	if len(publish.CorrelationData) > 0 {
		properties = append(properties, 0x09)
		properties = appendMQTTBinary(properties, publish.CorrelationData)
	}
	keys := make([]string, 0, len(publish.UserProperties))
	for key := range publish.UserProperties {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := publish.UserProperties[key]
		properties = append(properties, 0x26)
		properties = appendMQTTUTF8(properties, key)
		properties = appendMQTTUTF8(properties, value)
	}
	if publish.TopicAlias > 0 {
		properties = append(properties, 0x23, byte(publish.TopicAlias>>8), byte(publish.TopicAlias))
	}
	return properties
}

func encodeAzureEventGridMQTTPacket(header byte, body []byte) ([]byte, error) {
	if len(body)+5 > azureEventGridMQTTMaxPacketBytes {
		return nil, fmt.Errorf("Azure Event Grid MQTT packet exceeds 512 KiB")
	}
	packet := appendMQTTVariableByteInteger([]byte{header}, len(body))
	packet = append(packet, body...)
	if len(packet) > azureEventGridMQTTMaxPacketBytes {
		return nil, fmt.Errorf("Azure Event Grid MQTT packet exceeds 512 KiB")
	}
	return packet, nil
}

func (reader *azureEventGridMQTTPacketReader) Read(ctx context.Context) (azureWebPubSubMQTTPacket, error) {
	for {
		packet, consumed, complete, err := decodeAzureEventGridMQTTPacket(reader.buffer)
		if err != nil {
			return azureWebPubSubMQTTPacket{}, err
		}
		if complete {
			if reader.maximum > 0 && uint32(consumed) > reader.maximum {
				return azureWebPubSubMQTTPacket{}, fmt.Errorf("Azure Event Grid MQTT broker exceeded the requested maximum packet size")
			}
			reader.buffer = append(reader.buffer[:0], reader.buffer[consumed:]...)
			return packet, nil
		}
		messageType, data, err := reader.connection.Read(ctx)
		if err != nil {
			return azureWebPubSubMQTTPacket{}, err
		}
		if messageType != cloudWebSocketMessageBinary || len(reader.buffer)+len(data) > azureEventGridMQTTMaxPacketBytes {
			return azureWebPubSubMQTTPacket{}, fmt.Errorf("Azure Event Grid MQTT requires bounded binary WebSocket frames")
		}
		reader.buffer = append(reader.buffer, data...)
	}
}

func decodeAzureEventGridMQTTPacket(data []byte) (azureWebPubSubMQTTPacket, int, bool, error) {
	if len(data) < 2 {
		return azureWebPubSubMQTTPacket{}, 0, false, nil
	}
	remaining, prefix, err := decodeMQTTVariableByteInteger(data[1:])
	if err != nil {
		if strings.Contains(err.Error(), "incomplete") {
			return azureWebPubSubMQTTPacket{}, 0, false, nil
		}
		return azureWebPubSubMQTTPacket{}, 0, false, err
	}
	if remaining > azureEventGridMQTTMaxPacketBytes || 1+prefix+remaining > azureEventGridMQTTMaxPacketBytes {
		return azureWebPubSubMQTTPacket{}, 0, false, fmt.Errorf("Azure Event Grid MQTT packet exceeds 512 KiB")
	}
	total := 1 + prefix + remaining
	if len(data) < total {
		return azureWebPubSubMQTTPacket{}, 0, false, nil
	}
	return azureWebPubSubMQTTPacket{Header: data[0], Body: append([]byte(nil), data[1+prefix:total]...)}, total, true, nil
}

func invokeAzureEventGridMQTT(ctx context.Context, adapter *AzureRESTAdapter, invocation Invocation) (InvocationResult, error) {
	if err := validateAzureEventGridMQTTInvocation(invocation, adapter.config.AllowedHosts); err != nil {
		return InvocationResult{}, err
	}
	mutating := strings.EqualFold(strings.TrimSpace(invocation.Operation), "ClientMQTT")
	plan, _ := parseAzureEventGridMQTTPlan(invocation.Body, mutating)
	token, err := adapter.config.Tokens.Token(ctx, azureEventGridScope)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("load Azure Event Grid MQTT identity token: %w", err)
	}
	connect, err := encodeAzureEventGridMQTTConnect(plan, token)
	if err != nil {
		return InvocationResult{}, err
	}
	handshakeCtx, cancelHandshake := context.WithTimeout(ctx, 30*time.Second)
	defer cancelHandshake()
	connection, err := adapter.config.EventGridMQTTWebSocketDial(handshakeCtx, invocation.URL, nil)
	if err != nil {
		return InvocationResult{}, err
	}
	defer connection.Close()
	sink, err := newWebSocketOutputSink(invocation, adapter.config.MaxBodyBytes, "Azure Event Grid MQTT WebSocket")
	if err != nil {
		return InvocationResult{}, err
	}
	defer sink.abort()
	if err := connection.Write(handshakeCtx, cloudWebSocketMessageBinary, connect); err != nil {
		return InvocationResult{}, fmt.Errorf("send Azure Event Grid MQTT CONNECT")
	}
	reader := &azureEventGridMQTTPacketReader{connection: connection, maximum: plan.MaximumPacketSize}
	packet, err := reader.Read(handshakeCtx)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("read Azure Event Grid MQTT CONNACK")
	}
	connAck, err := parseAzureEventGridMQTTConnAck(packet, plan)
	if err != nil {
		return InvocationResult{}, err
	}
	if plan.SubscriptionIdentifier != 0 && !connAck.SubscriptionIdentifiersAvailable {
		return InvocationResult{}, fmt.Errorf("Azure Event Grid MQTT broker disabled subscription identifiers")
	}
	for _, subscription := range plan.Subscriptions {
		if strings.ContainsAny(subscription.TopicFilter, "#+") && !connAck.WildcardSubscriptionsAvailable {
			return InvocationResult{}, fmt.Errorf("Azure Event Grid MQTT broker disabled wildcard subscriptions")
		}
		if strings.HasPrefix(subscription.TopicFilter, "$share/") && !connAck.SharedSubscriptionsAvailable {
			return InvocationResult{}, fmt.Errorf("Azure Event Grid MQTT broker disabled shared subscriptions")
		}
	}
	if plan.Will != nil && plan.Will.Retain && !connAck.RetainAvailable {
		return InvocationResult{}, fmt.Errorf("Azure Event Grid MQTT broker disabled retained messages")
	}
	clientID := plan.ClientID
	if clientID == "" {
		clientID = connAck.AssignedClientID
	}
	mqttConnected := true
	defer func() {
		if mqttConnected {
			disconnectCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cancel()
			_ = connection.Write(disconnectCtx, cloudWebSocketMessageBinary, []byte{0xe0, 0x00})
		}
	}()
	received := 0
	if len(plan.Subscriptions) > 0 {
		subscribe, err := encodeAzureEventGridMQTTSubscribe(plan)
		if err != nil || uint32(len(subscribe)) > connAck.MaximumPacketSize {
			return InvocationResult{}, fmt.Errorf("Azure Event Grid MQTT SUBSCRIBE exceeds broker limits")
		}
		if err := connection.Write(handshakeCtx, cloudWebSocketMessageBinary, subscribe); err != nil {
			return InvocationResult{}, fmt.Errorf("send Azure Event Grid MQTT SUBSCRIBE")
		}
		for {
			packet, err = reader.Read(handshakeCtx)
			if err != nil {
				return InvocationResult{}, fmt.Errorf("read Azure Event Grid MQTT SUBACK")
			}
			if packet.Header>>4 == 3 {
				if received >= plan.MaxMessages {
					return InvocationResult{}, fmt.Errorf("Azure Event Grid MQTT exceeded max_messages before SUBACK")
				}
				if err := acceptAzureEventGridMQTTPublish(handshakeCtx, connection, sink, packet); err != nil {
					return InvocationResult{}, err
				}
				received++
				continue
			}
			if err := validateAzureEventGridMQTTSubAck(packet, plan.Subscriptions); err != nil {
				return InvocationResult{}, err
			}
			break
		}
	}
	encodedPublishes := make([][]byte, len(plan.Publishes))
	for index, publish := range plan.Publishes {
		packetID := uint16(0)
		if publish.QoS == 1 {
			packetID = uint16(index + 2)
		}
		encodedPublishes[index], err = encodeAzureEventGridMQTTPublish(publish, packetID)
		if err != nil || publish.QoS > int(connAck.MaximumQoS) || uint32(len(encodedPublishes[index])) > connAck.MaximumPacketSize || publish.TopicAlias > connAck.TopicAliasMaximum || publish.Retain && !connAck.RetainAvailable {
			return InvocationResult{}, fmt.Errorf("Azure Event Grid MQTT publish exceeds broker flow-control limits")
		}
	}
	pending := make(map[uint16]bool)
	nextPublish, inflight := 0, 0
	sendAvailable := func(writeCtx context.Context) error {
		for nextPublish < len(plan.Publishes) {
			publish := plan.Publishes[nextPublish]
			if publish.QoS == 1 && inflight >= int(connAck.ReceiveMaximum) {
				return nil
			}
			if err := connection.Write(writeCtx, cloudWebSocketMessageBinary, encodedPublishes[nextPublish]); err != nil {
				return fmt.Errorf("send Azure Event Grid MQTT PUBLISH")
			}
			if publish.QoS == 1 {
				pending[uint16(nextPublish+2)] = true
				inflight++
			}
			nextPublish++
		}
		return nil
	}
	if err := sendAvailable(handshakeCtx); err != nil {
		return InvocationResult{}, err
	}
	cancelHandshake()
	collectionCtx, cancelCollection := context.WithTimeout(ctx, time.Duration(plan.TimeoutSeconds)*time.Second)
	defer cancelCollection()
	pingOutstanding, authOutstanding, reauthenticated := false, false, false
	started := time.Now()
	keepAlive := uint16(plan.KeepAliveSeconds)
	if connAck.ServerKeepAlive != nil {
		keepAlive = *connAck.ServerKeepAlive
	}
	remoteEnded := false
	for !remoteEnded && (received < plan.MaxMessages || len(pending) > 0 || nextPublish < len(plan.Publishes) || authOutstanding) {
		wait := time.Duration(keepAlive) * time.Second / 2
		if wait <= 0 {
			wait = time.Second
		}
		if plan.ReauthenticateAfterSeconds > 0 && !reauthenticated {
			remaining := time.Duration(plan.ReauthenticateAfterSeconds)*time.Second - time.Since(started)
			if remaining < wait {
				wait = remaining
			}
		}
		if wait <= 0 && !reauthenticated {
			fresh, err := adapter.config.Tokens.Token(collectionCtx, azureEventGridScope)
			if err != nil {
				return InvocationResult{}, fmt.Errorf("refresh Azure Event Grid MQTT identity token: %w", err)
			}
			authPacket, err := encodeAzureEventGridMQTTReauthenticate(fresh)
			if err != nil || connection.Write(collectionCtx, cloudWebSocketMessageBinary, authPacket) != nil {
				return InvocationResult{}, fmt.Errorf("send Azure Event Grid MQTT reauthentication")
			}
			reauthenticated, authOutstanding = true, true
			continue
		}
		readCtx, cancelRead := context.WithTimeout(collectionCtx, wait)
		packet, err = reader.Read(readCtx)
		cancelRead()
		if errors.Is(err, context.DeadlineExceeded) && collectionCtx.Err() == nil {
			if plan.ReauthenticateAfterSeconds > 0 && !reauthenticated && time.Since(started) >= time.Duration(plan.ReauthenticateAfterSeconds)*time.Second {
				continue
			}
			if pingOutstanding {
				return InvocationResult{}, fmt.Errorf("Azure Event Grid MQTT broker did not answer PINGREQ")
			}
			if err := connection.Write(collectionCtx, cloudWebSocketMessageBinary, []byte{0xc0, 0x00}); err != nil {
				return InvocationResult{}, fmt.Errorf("send Azure Event Grid MQTT PINGREQ")
			}
			pingOutstanding = true
			continue
		}
		if errors.Is(err, context.DeadlineExceeded) || websocket.CloseStatus(err) == websocket.StatusNormalClosure {
			if len(pending) > 0 || nextPublish < len(plan.Publishes) || authOutstanding {
				return InvocationResult{}, fmt.Errorf("Azure Event Grid MQTT session ended with incomplete acknowledgements")
			}
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				mqttConnected, remoteEnded = false, true
			}
			break
		}
		if err != nil {
			return InvocationResult{}, fmt.Errorf("read Azure Event Grid MQTT packet")
		}
		switch packet.Header >> 4 {
		case 3:
			if received >= plan.MaxMessages {
				return InvocationResult{}, fmt.Errorf("Azure Event Grid MQTT broker exceeded max_messages")
			}
			if err := acceptAzureEventGridMQTTPublish(collectionCtx, connection, sink, packet); err != nil {
				return InvocationResult{}, err
			}
			received++
		case 4:
			packetID, ackErr := parseAzureWebPubSubMQTTAcknowledgement(packet, 4, 5)
			if ackErr != nil || !pending[packetID] {
				return InvocationResult{}, fmt.Errorf("Azure Event Grid MQTT returned an invalid PUBACK")
			}
			delete(pending, packetID)
			inflight--
			if err := sendAvailable(collectionCtx); err != nil {
				return InvocationResult{}, err
			}
		case 13:
			if packet.Header != 0xd0 || len(packet.Body) != 0 {
				return InvocationResult{}, fmt.Errorf("Azure Event Grid MQTT returned malformed PINGRESP")
			}
			pingOutstanding = false
		case 14:
			if err := validateAzureWebPubSubMQTTDisconnect(packet, 5); err != nil || len(pending) > 0 || authOutstanding {
				return InvocationResult{}, fmt.Errorf("Azure Event Grid MQTT broker disconnected with incomplete work")
			}
			mqttConnected, remoteEnded = false, true
		case 15:
			if !authOutstanding || validateAzureEventGridMQTTAuth(packet) != nil {
				return InvocationResult{}, fmt.Errorf("Azure Event Grid MQTT returned invalid AUTH")
			}
			authOutstanding = false
		default:
			return InvocationResult{}, fmt.Errorf("Azure Event Grid MQTT broker returned an unexpected packet")
		}
	}
	if !remoteEnded {
		if err := connection.Write(ctx, cloudWebSocketMessageBinary, []byte{0xe0, 0x00}); err != nil {
			return InvocationResult{}, fmt.Errorf("send Azure Event Grid MQTT DISCONNECT")
		}
		mqttConnected = false
	}
	metadata, err := sink.finish(clientID)
	if err != nil {
		return InvocationResult{}, err
	}
	var summary map[string]any
	if json.Unmarshal(metadata, &summary) == nil {
		summary["messages"] = received
		if connAck.AssignedClientID != "" {
			summary["assigned_client_id"] = connAck.AssignedClientID
		}
		metadata, err = json.Marshal(summary)
		if err != nil {
			return InvocationResult{}, fmt.Errorf("encode Azure Event Grid MQTT output metadata")
		}
	}
	return InvocationResult{Output: metadata, RequestID: clientID}, nil
}

func parseAzureEventGridMQTTConnAck(packet azureWebPubSubMQTTPacket, plan azureEventGridMQTTPlan) (azureEventGridMQTTConnAck, error) {
	result := azureEventGridMQTTConnAck{
		azureWebPubSubMQTTConnAckCapabilities: azureWebPubSubMQTTConnAckCapabilities{ReceiveMaximum: 65535, MaximumPacketSize: azureEventGridMQTTMaxPacketBytes, MaximumQoS: 1},
		RetainAvailable:                       true, WildcardSubscriptionsAvailable: true, SubscriptionIdentifiersAvailable: true, SharedSubscriptionsAvailable: true,
	}
	if packet.Header != 0x20 || len(packet.Body) < 3 || packet.Body[0]&0xfe != 0 || packet.Body[1] != 0 {
		return result, fmt.Errorf("Azure Event Grid MQTT broker rejected or malformed CONNACK")
	}
	if plan.CleanStart && packet.Body[0]&0x01 != 0 {
		return result, fmt.Errorf("Azure Event Grid MQTT broker resumed a session despite clean_start")
	}
	propertyBytes, prefix, err := decodeMQTTVariableByteInteger(packet.Body[2:])
	if err != nil || 2+prefix+propertyBytes != len(packet.Body) {
		return result, fmt.Errorf("Azure Event Grid MQTT broker returned malformed CONNACK properties")
	}
	properties := packet.Body[2+prefix:]
	seen := make(map[byte]bool)
	for len(properties) > 0 {
		identifier := properties[0]
		properties = properties[1:]
		if seen[identifier] && identifier != 0x26 {
			return result, fmt.Errorf("Azure Event Grid MQTT broker duplicated a CONNACK property")
		}
		seen[identifier] = true
		switch identifier {
		case 0x12:
			value, consumed, err := readMQTTUTF8(properties)
			if err != nil || value == "" || len(value) > 128 {
				return result, fmt.Errorf("Azure Event Grid MQTT returned invalid assigned client ID")
			}
			result.AssignedClientID = value
			properties = properties[consumed:]
		case 0x13, 0x21, 0x22:
			if len(properties) < 2 {
				return result, fmt.Errorf("Azure Event Grid MQTT returned malformed CONNACK")
			}
			value := binary.BigEndian.Uint16(properties[:2])
			if identifier == 0x13 {
				if value > azureEventGridMQTTMaxKeepAlive {
					return result, fmt.Errorf("Azure Event Grid MQTT returned an excessive server keepalive")
				}
				result.ServerKeepAlive = &value
			} else if identifier == 0x21 {
				if value == 0 {
					return result, fmt.Errorf("Azure Event Grid MQTT returned zero receive maximum")
				}
				result.ReceiveMaximum = value
			} else {
				if value > 10 {
					return result, fmt.Errorf("Azure Event Grid MQTT returned an excessive topic alias maximum")
				}
				result.TopicAliasMaximum = value
			}
			properties = properties[2:]
		case 0x24:
			if len(properties) < 1 || properties[0] > 1 {
				return result, fmt.Errorf("Azure Event Grid MQTT returned invalid maximum QoS")
			}
			result.MaximumQoS = properties[0]
			properties = properties[1:]
		case 0x27:
			if len(properties) < 4 || binary.BigEndian.Uint32(properties[:4]) == 0 || binary.BigEndian.Uint32(properties[:4]) > azureEventGridMQTTMaxPacketBytes {
				return result, fmt.Errorf("Azure Event Grid MQTT returned invalid maximum packet size")
			}
			result.MaximumPacketSize = binary.BigEndian.Uint32(properties[:4])
			properties = properties[4:]
		case 0x11:
			if len(properties) < 4 {
				return result, fmt.Errorf("Azure Event Grid MQTT returned malformed session expiry")
			}
			properties = properties[4:]
		case 0x15, 0x1a, 0x1c, 0x1f:
			value, consumed, err := readMQTTUTF8(properties)
			if err != nil || identifier == 0x15 && value != azureEventGridMQTTAuthMethod {
				return result, fmt.Errorf("Azure Event Grid MQTT returned invalid CONNACK text property")
			}
			properties = properties[consumed:]
		case 0x16:
			_, consumed, err := readMQTTBinary(properties)
			if err != nil {
				return result, fmt.Errorf("Azure Event Grid MQTT returned invalid authentication data")
			}
			properties = properties[consumed:]
		case 0x25, 0x28, 0x29, 0x2a:
			if len(properties) < 1 || properties[0] > 1 {
				return result, fmt.Errorf("Azure Event Grid MQTT returned invalid CONNACK capability")
			}
			available := properties[0] == 1
			switch identifier {
			case 0x25:
				result.RetainAvailable = available
			case 0x28:
				result.WildcardSubscriptionsAvailable = available
			case 0x29:
				result.SubscriptionIdentifiersAvailable = available
			case 0x2a:
				result.SharedSubscriptionsAvailable = available
			}
			properties = properties[1:]
		case 0x26:
			_, first, err := readMQTTUTF8(properties)
			if err != nil {
				return result, err
			}
			_, second, err := readMQTTUTF8(properties[first:])
			if err != nil {
				return result, err
			}
			properties = properties[first+second:]
		default:
			return result, fmt.Errorf("Azure Event Grid MQTT returned unsupported CONNACK property")
		}
	}
	if plan.ClientID == "" && result.AssignedClientID == "" || plan.ClientID != "" && result.AssignedClientID != "" {
		return result, fmt.Errorf("Azure Event Grid MQTT assigned client ID response did not match CONNECT")
	}
	return result, nil
}

func validateAzureEventGridMQTTSubAck(packet azureWebPubSubMQTTPacket, subscriptions []azureEventGridMQTTSubscription) error {
	if packet.Header != 0x90 || len(packet.Body) < 4 || binary.BigEndian.Uint16(packet.Body[:2]) != azureEventGridMQTTSubscribeID {
		return fmt.Errorf("Azure Event Grid MQTT broker returned malformed SUBACK")
	}
	consumed, _, err := validateAzureWebPubSubMQTT5ControlProperties(packet.Body[2:], azureWebPubSubMQTT5ReasonProperties)
	if err != nil {
		return fmt.Errorf("Azure Event Grid MQTT broker returned malformed SUBACK properties")
	}
	reasons := packet.Body[2+consumed:]
	if len(reasons) != len(subscriptions) {
		return fmt.Errorf("Azure Event Grid MQTT SUBACK reason count does not match subscriptions")
	}
	for index, reason := range reasons {
		if reason > 1 || int(reason) > subscriptions[index].QoS {
			return fmt.Errorf("Azure Event Grid MQTT broker rejected or elevated a subscription")
		}
	}
	return nil
}

func acceptAzureEventGridMQTTPublish(ctx context.Context, connection cloudWebSocketConnection, sink *cloudWebSocketOutputSink, packet azureWebPubSubMQTTPacket) error {
	message, qos, packetID, err := decodeAzureEventGridMQTTPublish(packet)
	if err != nil {
		return err
	}
	if err := sink.writeMessage(message); err != nil {
		return err
	}
	if qos == 1 {
		if err := connection.Write(ctx, cloudWebSocketMessageBinary, encodeAzureWebPubSubMQTTAcknowledgement(4, packetID, 5)); err != nil {
			return fmt.Errorf("acknowledge Azure Event Grid MQTT PUBLISH")
		}
	}
	return nil
}

func decodeAzureEventGridMQTTPublish(packet azureWebPubSubMQTTPacket) ([]byte, byte, uint16, error) {
	if packet.Header>>4 != 3 {
		return nil, 0, 0, fmt.Errorf("Azure Event Grid MQTT expected PUBLISH")
	}
	qos := (packet.Header >> 1) & 0x03
	if qos > 1 || len(packet.Body) < 2 {
		return nil, 0, 0, fmt.Errorf("Azure Event Grid MQTT PUBLISH has invalid QoS or topic")
	}
	topicLength := int(binary.BigEndian.Uint16(packet.Body[:2]))
	offset := 2 + topicLength
	if topicLength == 0 || topicLength > 1024 || offset > len(packet.Body) || !validAzureWebPubSubMQTTUTF8(packet.Body[2:offset]) || strings.ContainsAny(string(packet.Body[2:offset]), "#+") {
		return nil, 0, 0, fmt.Errorf("Azure Event Grid MQTT PUBLISH has invalid topic")
	}
	packetID := uint16(0)
	if qos == 1 {
		if offset+2 > len(packet.Body) || binary.BigEndian.Uint16(packet.Body[offset:offset+2]) == 0 {
			return nil, 0, 0, fmt.Errorf("Azure Event Grid MQTT QoS 1 PUBLISH omitted packet ID")
		}
		packetID = binary.BigEndian.Uint16(packet.Body[offset : offset+2])
		offset += 2
	}
	consumed, metadata, err := parseAzureEventGridMQTTPublishProperties(packet.Body[offset:])
	if err != nil {
		return nil, 0, 0, err
	}
	offset += consumed
	payload := packet.Body[offset:]
	if format, ok := metadata["payload_format"].(int); ok && format == 1 && !utf8.Valid(payload) {
		return nil, 0, 0, fmt.Errorf("Azure Event Grid MQTT PUBLISH declared invalid UTF-8 payload")
	}
	metadata["topic"] = string(packet.Body[2 : 2+topicLength])
	metadata["qos"] = qos
	metadata["duplicate"] = packet.Header&0x08 != 0
	metadata["retained"] = packet.Header&0x01 != 0
	metadata["packet_id"] = packetID
	metadata["bytes"] = len(payload)
	metadata["payload_base64"] = base64.StdEncoding.EncodeToString(payload)
	message, err := json.Marshal(metadata)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("encode Azure Event Grid MQTT message")
	}
	return message, qos, packetID, nil
}

func parseAzureEventGridMQTTPublishProperties(data []byte) (int, map[string]any, error) {
	propertyBytes, prefix, err := decodeMQTTVariableByteInteger(data)
	if err != nil || prefix+propertyBytes > len(data) {
		return 0, nil, fmt.Errorf("Azure Event Grid MQTT returned malformed PUBLISH properties")
	}
	properties := data[prefix : prefix+propertyBytes]
	metadata := make(map[string]any)
	userProperties := make(map[string]string)
	var subscriptionIDs []int
	seen := make(map[byte]bool)
	for len(properties) > 0 {
		identifier := properties[0]
		properties = properties[1:]
		if identifier != 0x0b && identifier != 0x26 && seen[identifier] {
			return 0, nil, fmt.Errorf("Azure Event Grid MQTT returned duplicate PUBLISH property")
		}
		seen[identifier] = true
		switch identifier {
		case 0x01:
			if len(properties) < 1 || properties[0] > 1 {
				return 0, nil, fmt.Errorf("Azure Event Grid MQTT returned invalid payload format")
			}
			metadata["payload_format"] = int(properties[0])
			properties = properties[1:]
		case 0x02:
			if len(properties) < 4 {
				return 0, nil, fmt.Errorf("Azure Event Grid MQTT returned invalid message expiry")
			}
			metadata["message_expiry_seconds"] = binary.BigEndian.Uint32(properties[:4])
			properties = properties[4:]
		case 0x03, 0x08:
			value, consumed, err := readMQTTUTF8(properties)
			if err != nil {
				return 0, nil, fmt.Errorf("Azure Event Grid MQTT returned invalid PUBLISH text property")
			}
			if identifier == 0x03 {
				metadata["content_type"] = value
			} else {
				metadata["response_topic"] = value
			}
			properties = properties[consumed:]
		case 0x09:
			value, consumed, err := readMQTTBinary(properties)
			if err != nil {
				return 0, nil, fmt.Errorf("Azure Event Grid MQTT returned invalid correlation data")
			}
			metadata["correlation_data_base64"] = base64.StdEncoding.EncodeToString(value)
			properties = properties[consumed:]
		case 0x0b:
			value, consumed, err := decodeMQTTVariableByteInteger(properties)
			if err != nil || value == 0 {
				return 0, nil, fmt.Errorf("Azure Event Grid MQTT returned invalid subscription identifier")
			}
			subscriptionIDs = append(subscriptionIDs, value)
			properties = properties[consumed:]
		case 0x23:
			return 0, nil, fmt.Errorf("Azure Event Grid MQTT broker must not assign outgoing topic aliases")
		case 0x26:
			key, first, err := readMQTTUTF8(properties)
			if err != nil {
				return 0, nil, fmt.Errorf("Azure Event Grid MQTT returned invalid user property")
			}
			value, second, err := readMQTTUTF8(properties[first:])
			if err != nil {
				return 0, nil, fmt.Errorf("Azure Event Grid MQTT returned invalid user property")
			}
			if _, exists := userProperties[key]; exists {
				return 0, nil, fmt.Errorf("Azure Event Grid MQTT returned duplicate user-property key")
			}
			userProperties[key] = value
			properties = properties[first+second:]
		default:
			return 0, nil, fmt.Errorf("Azure Event Grid MQTT returned unsupported PUBLISH property")
		}
	}
	if len(userProperties) > 0 {
		metadata["user_properties"] = userProperties
	}
	if len(subscriptionIDs) > 0 {
		metadata["subscription_ids"] = subscriptionIDs
	}
	return prefix + propertyBytes, metadata, nil
}

func validateAzureEventGridMQTTAuth(packet azureWebPubSubMQTTPacket) error {
	if packet.Header != 0xf0 || len(packet.Body) == 0 || packet.Body[0] != 0x00 {
		return fmt.Errorf("Azure Event Grid MQTT AUTH did not succeed")
	}
	if len(packet.Body) == 1 {
		return nil
	}
	allowed := map[int]azureWebPubSubMQTT5PropertyKind{0x15: azureWebPubSubMQTT5PropertyUTF8, 0x16: azureWebPubSubMQTT5PropertyBinary, 0x1f: azureWebPubSubMQTT5PropertyUTF8}
	consumed, _, err := validateAzureWebPubSubMQTT5ControlProperties(packet.Body[1:], allowed)
	if err != nil || consumed != len(packet.Body)-1 {
		return fmt.Errorf("Azure Event Grid MQTT AUTH properties are malformed")
	}
	return nil
}

func defaultAzureEventGridMQTTWebSocketDial(ctx context.Context, target string, headers http.Header) (cloudWebSocketConnection, error) {
	client := &http.Client{Transport: http.DefaultTransport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	connection, response, err := websocket.Dial(ctx, target, &websocket.DialOptions{HTTPClient: client, HTTPHeader: headers.Clone(), Subprotocols: []string{"mqtt"}, CompressionMode: websocket.CompressionDisabled})
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("Azure Event Grid MQTT WebSocket handshake failed with HTTP %d", response.StatusCode)
		}
		return nil, fmt.Errorf("Azure Event Grid MQTT WebSocket handshake failed")
	}
	if connection.Subprotocol() != "mqtt" {
		connection.CloseNow()
		return nil, fmt.Errorf("Azure Event Grid MQTT WebSocket did not negotiate mqtt")
	}
	connection.SetReadLimit(azureEventGridMQTTMaxPacketBytes)
	return coderTencentWebSocketConnection{connection: connection}, nil
}

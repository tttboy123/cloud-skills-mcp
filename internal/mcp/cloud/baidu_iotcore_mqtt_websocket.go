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
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
)

const (
	authSchemeBaiduIoTCoreMQTTWS        = "iotcore-mqtt-ws"
	baiduIoTCoreMQTTSubscribeOperation  = "subscribemqtt"
	baiduIoTCoreMQTTClientOperation     = "clientmqtt"
	baiduIoTCoreMQTTMaxSubscriptions    = 100
	baiduIoTCoreMQTTSubscribeBatchSize  = 8
	baiduIoTCoreMQTTMaxPublishes        = 64
	baiduIoTCoreMQTTMaxMessages         = 256
	baiduIoTCoreMQTTMaxTopicBytes       = 255
	baiduIoTCoreMQTTMaxTopicLevels      = 16
	baiduIoTCoreMQTTMaxTopicLevelBytes  = 40
	baiduIoTCoreMQTTMaxPayloadBytes     = 128 * 1024
	baiduIoTCoreMQTTDefaultPayloadMax   = 32 * 1024
	baiduIoTCoreMQTTMaxTimeoutSeconds   = 300
	baiduIoTCoreMQTTDefaultKeepAlive    = 300
	baiduIoTCoreMQTTMinKeepAlive        = 30
	baiduIoTCoreMQTTMaxKeepAlive        = 1200
	baiduIoTCoreMQTTSubscribePacketID   = 1
	baiduIoTCoreMQTTQoS0PublishInterval = 34 * time.Millisecond
	baiduIoTCoreMQTTQoS1PublishInterval = 100 * time.Millisecond
	baiduIoTCoreMQTTQoS2PublishInterval = 100 * time.Millisecond
	baiduIoTCoreMQTTMaxUserProperties   = 32
	baiduIoTCoreMQTTMaxBinaryProperty   = 65535
	baiduIoTCoreMQTTMaxPacketBytes      = 320 * 1024
)

var baiduIoTCoreIDPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

type baiduIoTCoreMQTTSubscription struct {
	TopicFilter string `json:"topic_filter"`
	QoS         int    `json:"qos"`
}

type baiduIoTCoreMQTTPublish struct {
	Topic                 string                         `json:"topic"`
	PayloadBase64         string                         `json:"payload_base64"`
	QoS                   int                            `json:"qos"`
	Retain                bool                           `json:"retain,omitempty"`
	PayloadFormat         int                            `json:"payload_format,omitempty"`
	ContentType           string                         `json:"content_type,omitempty"`
	MessageExpirySeconds  uint32                         `json:"message_expiry_seconds,omitempty"`
	ResponseTopic         string                         `json:"response_topic,omitempty"`
	CorrelationDataBase64 string                         `json:"correlation_data_base64,omitempty"`
	UserProperties        []baiduIoTCoreMQTTUserProperty `json:"user_properties,omitempty"`
	payload               []byte
	correlationData       []byte
	userProperties        []baiduIoTCoreMQTTUserProperty
}

type baiduIoTCoreMQTTWill struct {
	Topic                 string                         `json:"topic"`
	PayloadBase64         string                         `json:"payload_base64"`
	QoS                   int                            `json:"qos"`
	Retain                bool                           `json:"retain,omitempty"`
	PayloadFormat         int                            `json:"payload_format,omitempty"`
	ContentType           string                         `json:"content_type,omitempty"`
	MessageExpirySeconds  uint32                         `json:"message_expiry_seconds,omitempty"`
	ResponseTopic         string                         `json:"response_topic,omitempty"`
	CorrelationDataBase64 string                         `json:"correlation_data_base64,omitempty"`
	UserProperties        []baiduIoTCoreMQTTUserProperty `json:"user_properties,omitempty"`
	WillDelaySeconds      uint32                         `json:"will_delay_seconds,omitempty"`
	payload               []byte
	correlationData       []byte
	userProperties        []baiduIoTCoreMQTTUserProperty
}

type baiduIoTCoreMQTTUserProperty struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type baiduIoTCoreMQTTPlan struct {
	ProtocolVersion  int                            `json:"protocol_version,omitempty"`
	IoTCoreID        string                         `json:"iot_core_id,omitempty"`
	ClientID         string                         `json:"client_id"`
	CleanSession     *bool                          `json:"clean_session,omitempty"`
	KeepAliveSeconds int                            `json:"keep_alive_seconds,omitempty"`
	MaxPayloadBytes  int                            `json:"max_payload_bytes,omitempty"`
	Subscriptions    []baiduIoTCoreMQTTSubscription `json:"subscriptions,omitempty"`
	Unsubscriptions  []string                       `json:"unsubscriptions,omitempty"`
	Publishes        []baiduIoTCoreMQTTPublish      `json:"publishes,omitempty"`
	Will             *baiduIoTCoreMQTTWill          `json:"will,omitempty"`
	MaxMessages      int                            `json:"max_messages,omitempty"`
	TimeoutSeconds   int                            `json:"timeout_seconds,omitempty"`
	cleanSession     bool
}

func validateBaiduIoTCoreMQTTInvocation(invocation Invocation) error {
	_, _, err := parseBaiduIoTCoreMQTTInvocation(invocation)
	return err
}

func parseBaiduIoTCoreMQTTInvocation(invocation Invocation) (*url.URL, baiduIoTCoreMQTTPlan, error) {
	if !strings.EqualFold(strings.TrimSpace(invocation.Service), "iotcore") {
		return nil, baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core MQTT requires service=iotcore")
	}
	operation := strings.ToLower(strings.TrimSpace(invocation.Operation))
	mutating := operation == baiduIoTCoreMQTTClientOperation
	if operation != baiduIoTCoreMQTTSubscribeOperation && !mutating {
		return nil, baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core MQTT operation must be SubscribeMQTT or ClientMQTT")
	}
	if (mutating && invocation.Mode != ModeMutate) || (!mutating && invocation.Mode != ModeRead) {
		return nil, baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core SubscribeMQTT is read-only and ClientMQTT is mutation-only")
	}
	if !strings.EqualFold(strings.TrimSpace(invocation.Method), http.MethodGet) {
		return nil, baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core MQTT over WSS requires method=GET")
	}
	target, err := url.Parse(invocation.URL)
	if err != nil || !strings.EqualFold(target.Scheme, "wss") || target.User != nil || target.Fragment != "" || target.RawQuery != "" || target.Port() != "" || target.EscapedPath() != "/mqtt" {
		return nil, baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core MQTT requires an exact wss://<iot-core-id>.iot.gz.baidubce.com/mqtt endpoint on port 443")
	}
	host := strings.ToLower(target.Hostname())
	suffix := ".iot.gz.baidubce.com"
	iotCoreID := strings.TrimSuffix(host, suffix)
	if iotCoreID == host || strings.Contains(iotCoreID, ".") || !baiduIoTCoreIDPattern.MatchString(iotCoreID) {
		return nil, baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core MQTT requires an exact single-instance official endpoint")
	}
	if len(invocation.Parameters) != 0 || len(invocation.Headers) != 0 || invocation.BodyFile != "" {
		return nil, baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core MQTT accepts only a credential-free protocol body and optional response_file")
	}
	if invocation.Region != "" || invocation.RegionSet != "" || invocation.Project != "" || invocation.Subscription != "" || invocation.Audience != "" || invocation.RegistryInstanceID != "" || invocation.RegistryUserID != "" || invocation.ACRScope != "" || invocation.ACRSourceScope != "" || invocation.AuthVersion != "" || invocation.APIVersion != "" || invocation.PayloadMode != "" || invocation.ChecksumAlgorithm != "" || invocation.StreamChunkBytes != 0 || invocation.StreamIntervalMS != 0 || invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return nil, baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core MQTT does not accept REST, cross-provider, Registry, or generic stream controls")
	}
	plan, err := parseBaiduIoTCoreMQTTPlan(invocation.Body, iotCoreID, mutating)
	if err != nil {
		return nil, baiduIoTCoreMQTTPlan{}, err
	}
	if len(plan.Subscriptions) > 0 && invocation.ResponseFile == "" {
		return nil, baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core MQTT subscriptions require response_file for atomic NDJSON")
	}
	if len(plan.Subscriptions) == 0 && invocation.ResponseFile != "" {
		return nil, baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core MQTT response_file is supported only when subscriptions are requested")
	}
	return target, plan, nil
}

func parseBaiduIoTCoreMQTTPlan(body any, endpointID string, mutating bool) (baiduIoTCoreMQTTPlan, error) {
	if body == nil || alibabaNLSContainsCredentialField(body) {
		return baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core MQTT requires a credential-free protocol body")
	}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) == 0 || len(encoded) > maxRequestPayloadBytes {
		return baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core MQTT body must be bounded JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	plan := baiduIoTCoreMQTTPlan{ProtocolVersion: 4, KeepAliveSeconds: baiduIoTCoreMQTTDefaultKeepAlive, MaxPayloadBytes: baiduIoTCoreMQTTDefaultPayloadMax}
	if err := decoder.Decode(&plan); err != nil || ensureJSONDecoderEOF(decoder) != nil {
		return baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core MQTT body does not match the protocol schema")
	}
	if plan.IoTCoreID == "" {
		plan.IoTCoreID = endpointID
	}
	if plan.IoTCoreID != endpointID || !baiduIoTCoreIDPattern.MatchString(plan.IoTCoreID) {
		return baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core MQTT iot_core_id must exactly match the endpoint")
	}
	if !validAWSIoTMQTTString(plan.ClientID, awsIoTMQTTMaxClientIDBytes) {
		return baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core MQTT client_id must be one bounded UTF-8 identifier")
	}
	if plan.ProtocolVersion != 4 && plan.ProtocolVersion != 5 {
		return baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core MQTT protocol_version must be 4 or 5")
	}
	plan.cleanSession = true
	if plan.CleanSession != nil {
		plan.cleanSession = *plan.CleanSession
	}
	if !mutating && !plan.cleanSession {
		return baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core read subscriptions require clean_session=true")
	}
	if plan.KeepAliveSeconds < baiduIoTCoreMQTTMinKeepAlive || plan.KeepAliveSeconds > baiduIoTCoreMQTTMaxKeepAlive {
		return baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core MQTT keep_alive_seconds must be between %d and %d", baiduIoTCoreMQTTMinKeepAlive, baiduIoTCoreMQTTMaxKeepAlive)
	}
	if plan.MaxPayloadBytes < baiduIoTCoreMQTTDefaultPayloadMax || plan.MaxPayloadBytes > baiduIoTCoreMQTTMaxPayloadBytes {
		return baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core MQTT max_payload_bytes must be between %d and %d", baiduIoTCoreMQTTDefaultPayloadMax, baiduIoTCoreMQTTMaxPayloadBytes)
	}
	if len(plan.Subscriptions) > baiduIoTCoreMQTTMaxSubscriptions {
		return baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core MQTT accepts at most %d subscriptions per connection plan", baiduIoTCoreMQTTMaxSubscriptions)
	}
	if !mutating && len(plan.Subscriptions) == 0 {
		return baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core SubscribeMQTT requires at least one subscription")
	}
	for _, subscription := range plan.Subscriptions {
		if err := validateBaiduIoTCoreMQTTTopic(subscription.TopicFilter, true); err != nil {
			return baiduIoTCoreMQTTPlan{}, err
		}
		if subscription.QoS < 0 || subscription.QoS > 2 {
			return baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core MQTT supports subscription QoS 0, 1, or 2")
		}
	}
	if len(plan.Unsubscriptions) > baiduIoTCoreMQTTMaxSubscriptions {
		return baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core MQTT accepts at most %d unsubscriptions per connection plan", baiduIoTCoreMQTTMaxSubscriptions)
	}
	if !mutating && len(plan.Unsubscriptions) != 0 {
		return baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core MQTT unsubscriptions require ClientMQTT through the mutation gate")
	}
	for _, topicFilter := range plan.Unsubscriptions {
		if err := validateBaiduIoTCoreMQTTTopic(topicFilter, true); err != nil {
			return baiduIoTCoreMQTTPlan{}, err
		}
	}
	if len(plan.Publishes) > baiduIoTCoreMQTTMaxPublishes {
		return baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core MQTT accepts at most %d publishes", baiduIoTCoreMQTTMaxPublishes)
	}
	if !mutating && (len(plan.Publishes) != 0 || plan.Will != nil) {
		return baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core publishes and Will messages require ClientMQTT through the mutation gate")
	}
	for index := range plan.Publishes {
		payload, err := validateBaiduIoTCoreMQTTPublish(plan.Publishes[index].Topic, plan.Publishes[index].PayloadBase64, plan.Publishes[index].QoS, plan.MaxPayloadBytes)
		if err != nil {
			return baiduIoTCoreMQTTPlan{}, err
		}
		plan.Publishes[index].payload = payload
		if err := validateBaiduIoTCoreMQTT5ApplicationProperties(plan.ProtocolVersion, &plan.Publishes[index]); err != nil {
			return baiduIoTCoreMQTTPlan{}, err
		}
	}
	if plan.Will != nil {
		payload, err := validateBaiduIoTCoreMQTTPublish(plan.Will.Topic, plan.Will.PayloadBase64, plan.Will.QoS, plan.MaxPayloadBytes)
		if err != nil {
			return baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core MQTT Will is invalid: %w", err)
		}
		plan.Will.payload = payload
		publishView := baiduIoTCoreMQTTPublish{PayloadFormat: plan.Will.PayloadFormat, ContentType: plan.Will.ContentType, MessageExpirySeconds: plan.Will.MessageExpirySeconds, ResponseTopic: plan.Will.ResponseTopic, CorrelationDataBase64: plan.Will.CorrelationDataBase64, UserProperties: plan.Will.UserProperties}
		if err := validateBaiduIoTCoreMQTT5ApplicationProperties(plan.ProtocolVersion, &publishView); err != nil {
			return baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core MQTT Will is invalid: %w", err)
		}
		if plan.ProtocolVersion != 5 && plan.Will.WillDelaySeconds != 0 {
			return baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core MQTT Will delay requires MQTT 5")
		}
		plan.Will.correlationData = publishView.correlationData
		plan.Will.userProperties = publishView.userProperties
	}
	if mutating && len(plan.Subscriptions) == 0 && len(plan.Unsubscriptions) == 0 && len(plan.Publishes) == 0 && plan.Will == nil {
		return baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core ClientMQTT requires a subscription, unsubscription, publish, or Will plan")
	}
	if len(plan.Subscriptions) > 0 {
		if plan.MaxMessages < 1 || plan.MaxMessages > baiduIoTCoreMQTTMaxMessages {
			return baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core MQTT max_messages must be between 1 and %d", baiduIoTCoreMQTTMaxMessages)
		}
		if plan.TimeoutSeconds < 1 || plan.TimeoutSeconds > baiduIoTCoreMQTTMaxTimeoutSeconds {
			return baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core MQTT timeout_seconds must be between 1 and %d", baiduIoTCoreMQTTMaxTimeoutSeconds)
		}
	} else if plan.MaxMessages != 0 || plan.TimeoutSeconds != 0 {
		return baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core MQTT collection bounds require at least one subscription")
	}
	return plan, nil
}

func validateBaiduIoTCoreMQTTPublish(topic, encoded string, qos, maxPayloadBytes int) ([]byte, error) {
	if err := validateBaiduIoTCoreMQTTTopic(topic, false); err != nil {
		return nil, err
	}
	if qos < 0 || qos > 2 {
		return nil, fmt.Errorf("Baidu IoT Core MQTT supports publish QoS 0, 1, or 2")
	}
	payload, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(payload) > maxPayloadBytes {
		return nil, fmt.Errorf("Baidu IoT Core MQTT payload_base64 must decode to at most %d bytes", maxPayloadBytes)
	}
	return payload, nil
}

func validateBaiduIoTCoreMQTT5ApplicationProperties(protocolVersion int, publish *baiduIoTCoreMQTTPublish) error {
	if publish == nil {
		return fmt.Errorf("Baidu IoT Core MQTT publish properties are required")
	}
	hasProperties := publish.PayloadFormat != 0 || publish.ContentType != "" || publish.MessageExpirySeconds != 0 || publish.ResponseTopic != "" || publish.CorrelationDataBase64 != "" || len(publish.UserProperties) != 0
	if protocolVersion != 5 && hasProperties {
		return fmt.Errorf("Baidu IoT Core MQTT application properties require MQTT 5")
	}
	if publish.PayloadFormat < 0 || publish.PayloadFormat > 1 {
		return fmt.Errorf("Baidu IoT Core MQTT payload_format must be 0 or 1")
	}
	if publish.ContentType != "" && !validAWSIoTMQTTString(publish.ContentType, 1024) {
		return fmt.Errorf("Baidu IoT Core MQTT content_type must be bounded UTF-8")
	}
	if publish.ResponseTopic != "" {
		if err := validateBaiduIoTCoreMQTTTopic(publish.ResponseTopic, false); err != nil {
			return fmt.Errorf("Baidu IoT Core MQTT response_topic is invalid: %w", err)
		}
	}
	if publish.CorrelationDataBase64 != "" {
		value, err := base64.StdEncoding.Strict().DecodeString(publish.CorrelationDataBase64)
		if err != nil || len(value) > baiduIoTCoreMQTTMaxBinaryProperty {
			return fmt.Errorf("Baidu IoT Core MQTT correlation_data_base64 is invalid")
		}
		publish.correlationData = value
	}
	if len(publish.UserProperties) > baiduIoTCoreMQTTMaxUserProperties {
		return fmt.Errorf("Baidu IoT Core MQTT accepts at most %d user properties", baiduIoTCoreMQTTMaxUserProperties)
	}
	for _, property := range publish.UserProperties {
		if !validAWSIoTMQTTString(property.Name, 1024) || !validAWSIoTMQTTString(property.Value, 1024) {
			return fmt.Errorf("Baidu IoT Core MQTT user properties must be bounded UTF-8")
		}
	}
	publish.userProperties = append([]baiduIoTCoreMQTTUserProperty(nil), publish.UserProperties...)
	return nil
}

func validateBaiduIoTCoreMQTTTopic(topic string, filter bool) error {
	if !validAWSIoTMQTTString(topic, baiduIoTCoreMQTTMaxTopicBytes) {
		return fmt.Errorf("Baidu IoT Core MQTT topic must be bounded UTF-8")
	}
	if filter && strings.HasPrefix(topic, "$share/") {
		parts := strings.SplitN(strings.TrimPrefix(topic, "$share/"), "/", 2)
		if len(parts) != 2 || len(parts[0]) < 1 || len(parts[0]) > baiduIoTCoreMQTTMaxTopicLevelBytes || strings.ContainsAny(parts[0], "/+#") || parts[1] == "" {
			return fmt.Errorf("Baidu IoT Core MQTT shared subscription group is invalid")
		}
	} else if filter && strings.HasPrefix(topic, "$share") {
		return fmt.Errorf("Baidu IoT Core MQTT shared subscription must use $share/<group>/<filter>")
	}
	levels := strings.Split(topic, "/")
	if len(levels) > baiduIoTCoreMQTTMaxTopicLevels {
		return fmt.Errorf("Baidu IoT Core MQTT topic exceeds %d levels", baiduIoTCoreMQTTMaxTopicLevels)
	}
	for index, level := range levels {
		if len(level) > baiduIoTCoreMQTTMaxTopicLevelBytes {
			return fmt.Errorf("Baidu IoT Core MQTT topic level exceeds %d bytes", baiduIoTCoreMQTTMaxTopicLevelBytes)
		}
		if !filter && strings.ContainsAny(level, "#+") {
			return fmt.Errorf("Baidu IoT Core MQTT publish topics cannot contain wildcards")
		}
		if filter && strings.Contains(level, "#") && (level != "#" || index != len(levels)-1) {
			return fmt.Errorf("Baidu IoT Core MQTT # wildcard must be the final complete level")
		}
		if filter && strings.Contains(level, "+") && level != "+" {
			return fmt.Errorf("Baidu IoT Core MQTT + wildcard must occupy a complete level")
		}
	}
	return nil
}

func deriveBaiduIoTCoreIAMMQTTCredential(iotCoreID string, credentials BCECredentials, timestamp time.Time) (string, string, error) {
	if !baiduIoTCoreIDPattern.MatchString(iotCoreID) || credentials.AccessKeyID == "" || credentials.SecretAccessKey == "" {
		return "", "", fmt.Errorf("Baidu IoT Core application permission requires a valid instance and complete BCE IAM AK/SK")
	}
	if credentials.SessionToken != "" {
		return "", "", fmt.Errorf("Baidu IoT Core application permission does not document a BCE session-token field")
	}
	now := timestamp.UTC()
	milliseconds := now.UnixMilli()
	normalTimestamp := now.Format("2006-01-02T15:04:05Z")
	prefix := "bce-auth-v1/" + credentials.AccessKeyID + "/" + normalTimestamp + "/60"
	signKey := hmacSHA256Hex(credentials.SecretAccessKey, prefix)
	canonicalRequest := "POST\n/connect\n\nhost:iot.gz.baidubce.com"
	password := hmacSHA256Hex(signKey, canonicalRequest)
	username := fmt.Sprintf("bceiam@%s|%s|%d|SHA256", iotCoreID, credentials.AccessKeyID, milliseconds)
	return username, password, nil
}

func encodeBaiduIoTCoreMQTTConnect(plan baiduIoTCoreMQTTPlan, username, password string) ([]byte, error) {
	flags := byte(0xc0)
	if plan.cleanSession {
		flags |= 0x02
	}
	if plan.Will != nil {
		flags |= 0x04 | byte(plan.Will.QoS<<3)
		if plan.Will.Retain {
			flags |= 0x20
		}
	}
	body := []byte{0x00, 0x04, 'M', 'Q', 'T', 'T', byte(plan.ProtocolVersion), flags, byte(plan.KeepAliveSeconds >> 8), byte(plan.KeepAliveSeconds)}
	if plan.ProtocolVersion == 5 {
		receiveMaximum := max(1, plan.MaxMessages)
		properties := []byte{0x21, byte(receiveMaximum >> 8), byte(receiveMaximum), 0x27}
		properties = appendMQTTUint32(properties, baiduIoTCoreMQTTMaxPacketBytes)
		properties = append(properties, 0x22, 0x00, 0x00)
		body = appendMQTTVariableByteInteger(body, len(properties))
		body = append(body, properties...)
	}
	body = appendMQTTUTF8(body, plan.ClientID)
	if plan.Will != nil {
		if plan.ProtocolVersion == 5 {
			properties := encodeBaiduIoTCoreMQTT5ApplicationProperties(plan.Will.PayloadFormat, plan.Will.ContentType, plan.Will.MessageExpirySeconds, plan.Will.ResponseTopic, plan.Will.correlationData, plan.Will.userProperties)
			if plan.Will.WillDelaySeconds > 0 {
				properties = append(append([]byte{0x18}, mqttUint32Bytes(plan.Will.WillDelaySeconds)...), properties...)
			}
			body = appendMQTTVariableByteInteger(body, len(properties))
			body = append(body, properties...)
		}
		body = appendMQTTUTF8(body, plan.Will.Topic)
		body = appendMQTTBinary(body, plan.Will.payload)
	}
	body = appendMQTTUTF8(body, username)
	body = appendMQTTUTF8(body, password)
	return encodeBaiduIoTCoreMQTTPacket(0x10, body)
}

func encodeBaiduIoTCoreMQTTSubscribe(subscriptions []baiduIoTCoreMQTTSubscription, packetID uint16, protocolVersion ...int) ([]byte, error) {
	body := []byte{byte(packetID >> 8), byte(packetID)}
	if len(protocolVersion) > 0 && protocolVersion[0] == 5 {
		body = append(body, 0x00)
	}
	for _, subscription := range subscriptions {
		body = appendMQTTUTF8(body, subscription.TopicFilter)
		body = append(body, byte(subscription.QoS))
	}
	return encodeBaiduIoTCoreMQTTPacket(0x82, body)
}

func encodeBaiduIoTCoreMQTTUnsubscribe(topicFilters []string, packetID uint16, protocolVersion int) ([]byte, error) {
	body := []byte{byte(packetID >> 8), byte(packetID)}
	if protocolVersion == 5 {
		body = append(body, 0x00)
	}
	for _, topicFilter := range topicFilters {
		body = appendMQTTUTF8(body, topicFilter)
	}
	return encodeBaiduIoTCoreMQTTPacket(0xa2, body)
}

func validateBaiduIoTCoreMQTTSubAck(packet awsMQTTPacket, packetID uint16, subscriptions []baiduIoTCoreMQTTSubscription, protocolVersion ...int) error {
	version := 4
	if len(protocolVersion) > 0 {
		version = protocolVersion[0]
	}
	if packet.Header != 0x90 || len(packet.Body) < 2 || binary.BigEndian.Uint16(packet.Body[:2]) != packetID {
		return fmt.Errorf("Baidu IoT Core MQTT returned a malformed SUBACK")
	}
	reasons := packet.Body[2:]
	if version == 5 {
		propertyBytes, _, err := validateAzureWebPubSubMQTT5ControlProperties(packet.Body[2:], azureWebPubSubMQTT5ReasonProperties)
		if err != nil {
			return fmt.Errorf("Baidu IoT Core MQTT returned malformed MQTT 5 SUBACK properties")
		}
		reasons = packet.Body[2+propertyBytes:]
	}
	if len(reasons) != len(subscriptions) {
		return fmt.Errorf("Baidu IoT Core MQTT returned a malformed SUBACK")
	}
	for index, grantedQoS := range reasons {
		if grantedQoS > 2 || int(grantedQoS) > subscriptions[index].QoS {
			return fmt.Errorf("Baidu IoT Core MQTT rejected or elevated a subscription")
		}
	}
	return nil
}

func validateBaiduIoTCoreMQTTUnsubAck(packet awsMQTTPacket, packetID uint16, topicFilters []string, protocolVersion int) error {
	if packet.Header != 0xb0 || len(packet.Body) < 2 || binary.BigEndian.Uint16(packet.Body[:2]) != packetID {
		return fmt.Errorf("Baidu IoT Core MQTT returned a malformed UNSUBACK")
	}
	if protocolVersion == 4 {
		if len(packet.Body) != 2 {
			return fmt.Errorf("Baidu IoT Core MQTT returned a malformed MQTT 3.1.1 UNSUBACK")
		}
		return nil
	}
	propertyBytes, _, err := validateAzureWebPubSubMQTT5ControlProperties(packet.Body[2:], azureWebPubSubMQTT5ReasonProperties)
	if err != nil {
		return fmt.Errorf("Baidu IoT Core MQTT returned malformed MQTT 5 UNSUBACK properties")
	}
	reasons := packet.Body[2+propertyBytes:]
	if len(reasons) != len(topicFilters) {
		return fmt.Errorf("Baidu IoT Core MQTT UNSUBACK reason count does not match unsubscriptions")
	}
	for _, reason := range reasons {
		if reason != 0x00 && reason != 0x11 {
			return fmt.Errorf("Baidu IoT Core MQTT broker rejected an unsubscription")
		}
	}
	return nil
}

func validateBaiduIoTCoreMQTTInboundPayload(packet awsMQTTPacket, maxPayloadBytes int, protocolVersion ...int) error {
	if packet.Header>>4 != 3 || len(packet.Body) < 2 {
		return fmt.Errorf("Baidu IoT Core MQTT returned a malformed PUBLISH")
	}
	qos := (packet.Header >> 1) & 0x03
	topicBytes := int(binary.BigEndian.Uint16(packet.Body[:2]))
	offset := 2 + topicBytes
	if qos > 2 || topicBytes < 1 || offset > len(packet.Body) {
		return fmt.Errorf("Baidu IoT Core MQTT returned a malformed PUBLISH")
	}
	if qos > 0 {
		offset += 2
		if offset > len(packet.Body) {
			return fmt.Errorf("Baidu IoT Core MQTT returned a malformed PUBLISH")
		}
	}
	if len(protocolVersion) > 0 && protocolVersion[0] == 5 {
		consumed, _, err := parseBaiduIoTCoreMQTT5PublishProperties(packet.Body[offset:])
		if err != nil {
			return err
		}
		offset += consumed
	}
	if len(packet.Body)-offset > maxPayloadBytes {
		return fmt.Errorf("Baidu IoT Core MQTT payload exceeds the declared instance limit")
	}
	return nil
}

func encodeBaiduIoTCoreMQTTPublish(publish baiduIoTCoreMQTTPublish, packetID uint16) ([]byte, error) {
	return encodeBaiduIoTCoreMQTTPublishForPlan(baiduIoTCoreMQTTPlan{ProtocolVersion: 4}, publish, packetID)
}

func encodeBaiduIoTCoreMQTTPublishForPlan(plan baiduIoTCoreMQTTPlan, publish baiduIoTCoreMQTTPublish, packetID uint16) ([]byte, error) {
	body := appendMQTTUTF8(nil, publish.Topic)
	if publish.QoS > 0 {
		body = append(body, byte(packetID>>8), byte(packetID))
	}
	if plan.ProtocolVersion == 5 {
		properties := encodeBaiduIoTCoreMQTT5ApplicationProperties(publish.PayloadFormat, publish.ContentType, publish.MessageExpirySeconds, publish.ResponseTopic, publish.correlationData, publish.userProperties)
		body = appendMQTTVariableByteInteger(body, len(properties))
		body = append(body, properties...)
	}
	body = append(body, publish.payload...)
	header := byte(0x30 | publish.QoS<<1)
	if publish.Retain {
		header |= 0x01
	}
	return encodeBaiduIoTCoreMQTTPacket(header, body)
}

func encodeBaiduIoTCoreMQTTPacket(header byte, body []byte) ([]byte, error) {
	if len(body) > baiduIoTCoreMQTTMaxPacketBytes {
		return nil, fmt.Errorf("Baidu IoT Core MQTT packet exceeds %d bytes", baiduIoTCoreMQTTMaxPacketBytes)
	}
	remaining := len(body)
	encoded := []byte{header}
	for {
		value := byte(remaining % 128)
		remaining /= 128
		if remaining > 0 {
			value |= 0x80
		}
		encoded = append(encoded, value)
		if remaining == 0 {
			break
		}
	}
	if len(encoded)+len(body) > baiduIoTCoreMQTTMaxPacketBytes {
		return nil, fmt.Errorf("Baidu IoT Core MQTT packet exceeds %d bytes", baiduIoTCoreMQTTMaxPacketBytes)
	}
	return append(encoded, body...), nil
}

func mqttUint32Bytes(value uint32) []byte {
	return []byte{byte(value >> 24), byte(value >> 16), byte(value >> 8), byte(value)}
}

func encodeBaiduIoTCoreMQTT5ApplicationProperties(payloadFormat int, contentType string, expiry uint32, responseTopic string, correlationData []byte, userProperties []baiduIoTCoreMQTTUserProperty) []byte {
	properties := make([]byte, 0, 32)
	if payloadFormat == 1 {
		properties = append(properties, 0x01, 0x01)
	}
	if expiry > 0 {
		properties = append(properties, 0x02)
		properties = appendMQTTUint32(properties, expiry)
	}
	if contentType != "" {
		properties = append(properties, 0x03)
		properties = appendMQTTUTF8(properties, contentType)
	}
	if responseTopic != "" {
		properties = append(properties, 0x08)
		properties = appendMQTTUTF8(properties, responseTopic)
	}
	if len(correlationData) > 0 {
		properties = append(properties, 0x09)
		properties = appendMQTTBinary(properties, correlationData)
	}
	for _, property := range userProperties {
		properties = append(properties, 0x26)
		properties = appendMQTTUTF8(properties, property.Name)
		properties = appendMQTTUTF8(properties, property.Value)
	}
	return properties
}

func validateBaiduIoTCoreMQTTConnAck(packet awsMQTTPacket, plan baiduIoTCoreMQTTPlan) error {
	if plan.ProtocolVersion == 4 {
		if packet.Header != 0x20 || len(packet.Body) != 2 || packet.Body[0]&0xfe != 0 || packet.Body[1] != 0 {
			return fmt.Errorf("Baidu IoT Core MQTT broker rejected or malformed CONNACK")
		}
	} else {
		if packet.Header != 0x20 || len(packet.Body) < 3 || packet.Body[0]&0xfe != 0 || packet.Body[1] != 0 {
			return fmt.Errorf("Baidu IoT Core MQTT broker rejected or malformed MQTT 5 CONNACK")
		}
		consumed, numeric, err := validateAzureWebPubSubMQTT5ControlProperties(packet.Body[2:], azureWebPubSubMQTT5ConnAckProperties)
		if err != nil || consumed != len(packet.Body)-2 {
			return fmt.Errorf("Baidu IoT Core MQTT broker returned malformed MQTT 5 CONNACK properties")
		}
		if values := numeric[0x24]; len(values) == 1 && values[0] > 2 {
			return fmt.Errorf("Baidu IoT Core MQTT broker returned invalid Maximum QoS")
		}
		if values := numeric[0x24]; len(values) == 1 {
			maximumQoS := int(values[0])
			for _, publish := range plan.Publishes {
				if publish.QoS > maximumQoS {
					return fmt.Errorf("Baidu IoT Core MQTT publish exceeds broker Maximum QoS")
				}
			}
			if plan.Will != nil && plan.Will.QoS > maximumQoS {
				return fmt.Errorf("Baidu IoT Core MQTT Will exceeds broker Maximum QoS")
			}
		}
		if values := numeric[0x21]; len(values) == 1 {
			inflight := 0
			for _, publish := range plan.Publishes {
				if publish.QoS > 0 {
					inflight++
				}
			}
			if uint64(inflight) > values[0] {
				return fmt.Errorf("Baidu IoT Core MQTT publish plan exceeds broker Receive Maximum")
			}
		}
		if values := numeric[0x27]; len(values) == 1 {
			for offset := 0; offset < len(plan.Subscriptions); offset += baiduIoTCoreMQTTSubscribeBatchSize {
				end := min(offset+baiduIoTCoreMQTTSubscribeBatchSize, len(plan.Subscriptions))
				encoded, err := encodeBaiduIoTCoreMQTTSubscribe(plan.Subscriptions[offset:end], uint16(offset/baiduIoTCoreMQTTSubscribeBatchSize+1), plan.ProtocolVersion)
				if err != nil || uint64(len(encoded)) > values[0] {
					return fmt.Errorf("Baidu IoT Core MQTT subscription exceeds broker Maximum Packet Size")
				}
			}
			for offset := 0; offset < len(plan.Unsubscriptions); offset += baiduIoTCoreMQTTSubscribeBatchSize {
				end := min(offset+baiduIoTCoreMQTTSubscribeBatchSize, len(plan.Unsubscriptions))
				encoded, err := encodeBaiduIoTCoreMQTTUnsubscribe(plan.Unsubscriptions[offset:end], uint16(offset/baiduIoTCoreMQTTSubscribeBatchSize+1), plan.ProtocolVersion)
				if err != nil || uint64(len(encoded)) > values[0] {
					return fmt.Errorf("Baidu IoT Core MQTT unsubscription exceeds broker Maximum Packet Size")
				}
			}
			for index, publish := range plan.Publishes {
				packetID := uint16(index + 1)
				if publish.QoS == 0 {
					packetID = 0
				}
				encoded, err := encodeBaiduIoTCoreMQTTPublishForPlan(plan, publish, packetID)
				if err != nil || uint64(len(encoded)) > values[0] {
					return fmt.Errorf("Baidu IoT Core MQTT publish exceeds broker Maximum Packet Size")
				}
			}
		}
	}
	if plan.cleanSession && packet.Body[0]&0x01 != 0 {
		return fmt.Errorf("Baidu IoT Core MQTT broker resumed a clean session")
	}
	return nil
}

func parseBaiduIoTCoreMQTT5PublishProperties(data []byte) (int, map[string]any, error) {
	propertyBytes, prefix, err := decodeMQTTVariableByteInteger(data)
	if err != nil || prefix+propertyBytes > len(data) {
		return 0, nil, fmt.Errorf("Baidu IoT Core MQTT returned malformed PUBLISH properties")
	}
	properties := data[prefix : prefix+propertyBytes]
	metadata := make(map[string]any)
	userProperties := make([]map[string]string, 0)
	seen := make(map[int]bool)
	for len(properties) > 0 {
		identifier, consumed, err := decodeMQTTVariableByteInteger(properties)
		if err != nil {
			return 0, nil, fmt.Errorf("Baidu IoT Core MQTT returned an invalid PUBLISH property identifier")
		}
		properties = properties[consumed:]
		if identifier != 0x26 && seen[identifier] {
			return 0, nil, fmt.Errorf("Baidu IoT Core MQTT returned a duplicate PUBLISH property")
		}
		seen[identifier] = true
		switch identifier {
		case 0x01:
			if len(properties) < 1 || properties[0] > 1 {
				return 0, nil, fmt.Errorf("Baidu IoT Core MQTT returned invalid payload format")
			}
			metadata["payload_format"] = int(properties[0])
			properties = properties[1:]
		case 0x02:
			if len(properties) < 4 {
				return 0, nil, fmt.Errorf("Baidu IoT Core MQTT returned invalid message expiry")
			}
			metadata["message_expiry_seconds"] = binary.BigEndian.Uint32(properties[:4])
			properties = properties[4:]
		case 0x03:
			value, consumed, err := readMQTTUTF8(properties)
			if err != nil {
				return 0, nil, fmt.Errorf("Baidu IoT Core MQTT returned invalid content type")
			}
			metadata["content_type"] = value
			properties = properties[consumed:]
		case 0x08:
			value, consumed, err := readMQTTUTF8(properties)
			if err != nil || validateBaiduIoTCoreMQTTTopic(value, false) != nil {
				return 0, nil, fmt.Errorf("Baidu IoT Core MQTT returned invalid response topic")
			}
			metadata["response_topic"] = value
			properties = properties[consumed:]
		case 0x09:
			value, consumed, err := readMQTTBinary(properties)
			if err != nil {
				return 0, nil, fmt.Errorf("Baidu IoT Core MQTT returned invalid correlation data")
			}
			metadata["correlation_data_base64"] = base64.StdEncoding.EncodeToString(value)
			properties = properties[consumed:]
		case 0x26:
			name, first, err := readMQTTUTF8(properties)
			if err != nil {
				return 0, nil, fmt.Errorf("Baidu IoT Core MQTT returned invalid user property")
			}
			value, second, err := readMQTTUTF8(properties[first:])
			if err != nil || len(userProperties) >= baiduIoTCoreMQTTMaxUserProperties {
				return 0, nil, fmt.Errorf("Baidu IoT Core MQTT returned invalid user property")
			}
			userProperties = append(userProperties, map[string]string{"name": name, "value": value})
			properties = properties[first+second:]
		default:
			return 0, nil, fmt.Errorf("Baidu IoT Core MQTT returned unsupported PUBLISH property")
		}
	}
	if len(userProperties) > 0 {
		metadata["user_properties"] = userProperties
	}
	return prefix + propertyBytes, metadata, nil
}

func decodeBaiduIoTCoreMQTTPublish(plan baiduIoTCoreMQTTPlan, packet awsMQTTPacket) ([]byte, byte, uint16, error) {
	if packet.Header>>4 != 3 || len(packet.Body) < 2 {
		return nil, 0, 0, fmt.Errorf("Baidu IoT Core MQTT expected a PUBLISH packet")
	}
	qos := (packet.Header >> 1) & 0x03
	topicLength := int(binary.BigEndian.Uint16(packet.Body[:2]))
	offset := 2 + topicLength
	if qos > 2 || topicLength == 0 || offset > len(packet.Body) || validateBaiduIoTCoreMQTTTopic(string(packet.Body[2:offset]), false) != nil {
		return nil, 0, 0, fmt.Errorf("Baidu IoT Core MQTT PUBLISH packet has invalid QoS or topic")
	}
	packetID := uint16(0)
	if qos > 0 {
		if offset+2 > len(packet.Body) {
			return nil, 0, 0, fmt.Errorf("Baidu IoT Core MQTT QoS PUBLISH omitted packet ID")
		}
		packetID = binary.BigEndian.Uint16(packet.Body[offset : offset+2])
		if packetID == 0 {
			return nil, 0, 0, fmt.Errorf("Baidu IoT Core MQTT QoS PUBLISH used packet ID zero")
		}
		offset += 2
	}
	metadata := make(map[string]any)
	if plan.ProtocolVersion == 5 {
		consumed, properties, err := parseBaiduIoTCoreMQTT5PublishProperties(packet.Body[offset:])
		if err != nil {
			return nil, 0, 0, err
		}
		offset += consumed
		metadata = properties
	}
	payload := packet.Body[offset:]
	if len(payload) > plan.MaxPayloadBytes {
		return nil, 0, 0, fmt.Errorf("Baidu IoT Core MQTT payload exceeds the declared instance limit")
	}
	if format, ok := metadata["payload_format"].(int); ok && format == 1 && !utf8.Valid(payload) {
		return nil, 0, 0, fmt.Errorf("Baidu IoT Core MQTT PUBLISH declared invalid UTF-8 payload")
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
		return nil, 0, 0, fmt.Errorf("encode Baidu IoT Core MQTT message")
	}
	return message, qos, packetID, nil
}

func acceptBaiduIoTCoreMQTTPublish(ctx context.Context, connection cloudWebSocketConnection, sink *cloudWebSocketOutputSink, plan baiduIoTCoreMQTTPlan, packet awsMQTTPacket, incomingQoS2 map[uint16][]byte, completedQoS2 map[uint16]bool, remaining int) (bool, error) {
	message, qos, packetID, err := decodeBaiduIoTCoreMQTTPublish(plan, packet)
	if err != nil {
		return false, err
	}
	if qos == 2 {
		_, pending := incomingQoS2[packetID]
		if !pending && !completedQoS2[packetID] {
			if remaining <= len(incomingQoS2) {
				return false, fmt.Errorf("Baidu IoT Core MQTT exceeded max_messages with pending QoS 2 messages")
			}
			incomingQoS2[packetID] = message
		}
		if err := connection.Write(ctx, cloudWebSocketMessageBinary, encodeAzureWebPubSubMQTTAcknowledgement(5, packetID, plan.ProtocolVersion)); err != nil {
			return false, fmt.Errorf("acknowledge Baidu IoT Core MQTT QoS 2 PUBLISH")
		}
		return false, nil
	}
	if remaining <= len(incomingQoS2) {
		return false, fmt.Errorf("Baidu IoT Core MQTT exceeded max_messages")
	}
	if err := sink.writeMessage(message); err != nil {
		return false, err
	}
	if qos == 1 {
		if err := connection.Write(ctx, cloudWebSocketMessageBinary, encodeAzureWebPubSubMQTTAcknowledgement(4, packetID, plan.ProtocolVersion)); err != nil {
			return false, fmt.Errorf("acknowledge Baidu IoT Core MQTT message")
		}
	}
	return true, nil
}

func completeBaiduIoTCoreMQTTQoS2(ctx context.Context, connection cloudWebSocketConnection, sink *cloudWebSocketOutputSink, plan baiduIoTCoreMQTTPlan, packet awsMQTTPacket, incomingQoS2 map[uint16][]byte, completedQoS2 map[uint16]bool) (bool, error) {
	packetID, err := parseBaiduIoTCoreMQTTAcknowledgement(packet, 6, plan.ProtocolVersion)
	if err != nil {
		return false, fmt.Errorf("Baidu IoT Core MQTT returned a malformed PUBREL")
	}
	message, pending := incomingQoS2[packetID]
	if !pending && !completedQoS2[packetID] {
		return false, fmt.Errorf("Baidu IoT Core MQTT returned an unknown PUBREL")
	}
	if pending {
		if err := sink.writeMessage(message); err != nil {
			return false, err
		}
		delete(incomingQoS2, packetID)
		completedQoS2[packetID] = true
	}
	if err := connection.Write(ctx, cloudWebSocketMessageBinary, encodeAzureWebPubSubMQTTAcknowledgement(7, packetID, plan.ProtocolVersion)); err != nil {
		return false, fmt.Errorf("complete Baidu IoT Core MQTT QoS 2 delivery")
	}
	return pending, nil
}

func parseBaiduIoTCoreMQTTAcknowledgement(packet awsMQTTPacket, packetType byte, protocolVersion int) (uint16, error) {
	return parseAzureWebPubSubMQTTAcknowledgement(azureWebPubSubMQTTPacket{Header: packet.Header, Body: packet.Body}, packetType, protocolVersion)
}

func validateBaiduIoTCoreMQTTDisconnect(packet awsMQTTPacket, protocolVersion int) error {
	return validateAzureWebPubSubMQTTDisconnect(azureWebPubSubMQTTPacket{Header: packet.Header, Body: packet.Body}, protocolVersion)
}

type baiduIoTCoreMQTTPacketReader struct {
	connection cloudWebSocketConnection
	buffer     []byte
}

func (reader *baiduIoTCoreMQTTPacketReader) Read(ctx context.Context) (awsMQTTPacket, error) {
	for {
		packet, consumed, complete, err := decodeBaiduIoTCoreMQTTPacket(reader.buffer)
		if err != nil {
			return awsMQTTPacket{}, err
		}
		if complete {
			reader.buffer = append(reader.buffer[:0], reader.buffer[consumed:]...)
			return packet, nil
		}
		messageType, data, err := reader.connection.Read(ctx)
		if err != nil {
			return awsMQTTPacket{}, err
		}
		if messageType != cloudWebSocketMessageBinary {
			return awsMQTTPacket{}, fmt.Errorf("Baidu IoT Core MQTT WebSocket returned a non-binary frame")
		}
		if len(reader.buffer)+len(data) > baiduIoTCoreMQTTMaxPacketBytes {
			return awsMQTTPacket{}, fmt.Errorf("Baidu IoT Core MQTT packet exceeds %d bytes", baiduIoTCoreMQTTMaxPacketBytes)
		}
		reader.buffer = append(reader.buffer, data...)
	}
}

func decodeBaiduIoTCoreMQTTPacket(data []byte) (awsMQTTPacket, int, bool, error) {
	if len(data) < 2 {
		return awsMQTTPacket{}, 0, false, nil
	}
	remaining, multiplier := 0, 1
	index := 1
	for count := 0; count < 4; count++ {
		if index >= len(data) {
			return awsMQTTPacket{}, 0, false, nil
		}
		value := data[index]
		index++
		remaining += int(value&0x7f) * multiplier
		if remaining > baiduIoTCoreMQTTMaxPacketBytes {
			return awsMQTTPacket{}, 0, false, fmt.Errorf("Baidu IoT Core MQTT packet exceeds %d bytes", baiduIoTCoreMQTTMaxPacketBytes)
		}
		if value&0x80 == 0 {
			total := index + remaining
			if total > baiduIoTCoreMQTTMaxPacketBytes {
				return awsMQTTPacket{}, 0, false, fmt.Errorf("Baidu IoT Core MQTT packet exceeds %d bytes", baiduIoTCoreMQTTMaxPacketBytes)
			}
			if len(data) < total {
				return awsMQTTPacket{}, 0, false, nil
			}
			return awsMQTTPacket{Header: data[0], Body: append([]byte(nil), data[index:total]...)}, total, true, nil
		}
		multiplier *= 128
	}
	return awsMQTTPacket{}, 0, false, fmt.Errorf("Baidu IoT Core MQTT remaining length is malformed")
}

func invokeBaiduIoTCoreMQTT(ctx context.Context, adapter *BaiduRESTAdapter, invocation Invocation) (result InvocationResult, returnErr error) {
	target, plan, err := parseBaiduIoTCoreMQTTInvocation(invocation)
	if err != nil {
		return InvocationResult{}, err
	}
	credentials, err := adapter.config.Credentials.Credentials(ctx)
	if err != nil {
		return InvocationResult{}, err
	}
	username, password, err := deriveBaiduIoTCoreIAMMQTTCredential(plan.IoTCoreID, credentials, adapter.config.Now().UTC())
	if err != nil {
		return InvocationResult{}, err
	}
	defer func() {
		returnErr = sanitizeBaiduCCRError(returnErr, credentials.AccessKeyID, credentials.SecretAccessKey, username, password)
	}()
	handshakeCtx, cancelHandshake := context.WithTimeout(ctx, 30*time.Second)
	defer cancelHandshake()
	connection, err := adapter.config.IoTWebSocketDial(handshakeCtx, target.String())
	if err != nil {
		return InvocationResult{}, fmt.Errorf("Baidu IoT Core MQTT WSS handshake failed")
	}
	defer connection.Close()
	sink, err := newWebSocketOutputSink(invocation, adapter.config.MaxBodyBytes, "Baidu IoT Core MQTT WSS")
	if err != nil {
		return InvocationResult{}, err
	}
	defer sink.abort()
	connectPacket, err := encodeBaiduIoTCoreMQTTConnect(plan, username, password)
	if err != nil {
		return InvocationResult{}, err
	}
	if err := connection.Write(handshakeCtx, cloudWebSocketMessageBinary, connectPacket); err != nil {
		return InvocationResult{}, fmt.Errorf("send Baidu IoT Core MQTT CONNECT")
	}
	reader := &baiduIoTCoreMQTTPacketReader{connection: connection}
	packet, err := reader.Read(handshakeCtx)
	if err != nil || validateBaiduIoTCoreMQTTConnAck(packet, plan) != nil {
		return InvocationResult{}, fmt.Errorf("Baidu IoT Core MQTT connection was not acknowledged")
	}
	pendingSubscribes := make(map[uint16][]baiduIoTCoreMQTTSubscription)
	nextPacketID := uint16(baiduIoTCoreMQTTSubscribePacketID)
	for offset := 0; offset < len(plan.Subscriptions); offset += baiduIoTCoreMQTTSubscribeBatchSize {
		end := min(offset+baiduIoTCoreMQTTSubscribeBatchSize, len(plan.Subscriptions))
		batch := plan.Subscriptions[offset:end]
		subscribePacket, err := encodeBaiduIoTCoreMQTTSubscribe(batch, nextPacketID, plan.ProtocolVersion)
		if err != nil {
			return InvocationResult{}, err
		}
		if err := connection.Write(handshakeCtx, cloudWebSocketMessageBinary, subscribePacket); err != nil {
			return InvocationResult{}, fmt.Errorf("send Baidu IoT Core MQTT SUBSCRIBE")
		}
		pendingSubscribes[nextPacketID] = batch
		nextPacketID++
	}
	pendingUnsubscribes := make(map[uint16][]string)
	for offset := 0; offset < len(plan.Unsubscriptions); offset += baiduIoTCoreMQTTSubscribeBatchSize {
		end := min(offset+baiduIoTCoreMQTTSubscribeBatchSize, len(plan.Unsubscriptions))
		batch := plan.Unsubscriptions[offset:end]
		unsubscribePacket, err := encodeBaiduIoTCoreMQTTUnsubscribe(batch, nextPacketID, plan.ProtocolVersion)
		if err != nil {
			return InvocationResult{}, err
		}
		if err := connection.Write(handshakeCtx, cloudWebSocketMessageBinary, unsubscribePacket); err != nil {
			return InvocationResult{}, fmt.Errorf("send Baidu IoT Core MQTT UNSUBSCRIBE")
		}
		pendingUnsubscribes[nextPacketID] = batch
		nextPacketID++
	}
	pendingPublishes := make(map[uint16]int)
	packetID := max(nextPacketID, uint16(2))
	for index, publish := range plan.Publishes {
		if index > 0 {
			interval := baiduIoTCoreMQTTQoS0PublishInterval
			if publish.QoS == 1 {
				interval = baiduIoTCoreMQTTQoS1PublishInterval
			} else if publish.QoS == 2 {
				interval = baiduIoTCoreMQTTQoS2PublishInterval
			}
			if err := adapter.config.StreamPause(handshakeCtx, interval); err != nil {
				return InvocationResult{}, fmt.Errorf("pace Baidu IoT Core MQTT PUBLISH")
			}
		}
		encoded, err := encodeBaiduIoTCoreMQTTPublishForPlan(plan, publish, packetID)
		if err != nil {
			return InvocationResult{}, err
		}
		if err := connection.Write(handshakeCtx, cloudWebSocketMessageBinary, encoded); err != nil {
			return InvocationResult{}, fmt.Errorf("send Baidu IoT Core MQTT PUBLISH")
		}
		if publish.QoS > 0 {
			pendingPublishes[packetID] = publish.QoS
			packetID++
		}
	}
	received := 0
	incomingQoS2 := make(map[uint16][]byte)
	completedQoS2 := make(map[uint16]bool)
	for len(pendingSubscribes) > 0 || len(pendingUnsubscribes) > 0 || len(pendingPublishes) > 0 {
		packet, err = reader.Read(handshakeCtx)
		if err != nil {
			return InvocationResult{}, fmt.Errorf("read Baidu IoT Core MQTT acknowledgement")
		}
		switch packet.Header >> 4 {
		case 3:
			if received >= plan.MaxMessages || len(plan.Subscriptions) == 0 {
				return InvocationResult{}, fmt.Errorf("Baidu IoT Core MQTT returned an unexpected message before acknowledgement")
			}
			if err := validateBaiduIoTCoreMQTTInboundPayload(packet, plan.MaxPayloadBytes, plan.ProtocolVersion); err != nil {
				return InvocationResult{}, err
			}
			delivered, err := acceptBaiduIoTCoreMQTTPublish(handshakeCtx, connection, sink, plan, packet, incomingQoS2, completedQoS2, plan.MaxMessages-received)
			if err != nil {
				return InvocationResult{}, fmt.Errorf("Baidu IoT Core MQTT returned an invalid publish")
			}
			if delivered {
				received++
			}
		case 9:
			if len(packet.Body) < 2 {
				return InvocationResult{}, fmt.Errorf("Baidu IoT Core MQTT returned a malformed SUBACK")
			}
			ackID := binary.BigEndian.Uint16(packet.Body[:2])
			subscriptions, ok := pendingSubscribes[ackID]
			if !ok || validateBaiduIoTCoreMQTTSubAck(packet, ackID, subscriptions, plan.ProtocolVersion) != nil {
				return InvocationResult{}, fmt.Errorf("Baidu IoT Core MQTT returned a malformed SUBACK")
			}
			delete(pendingSubscribes, ackID)
		case 11:
			if len(packet.Body) < 2 {
				return InvocationResult{}, fmt.Errorf("Baidu IoT Core MQTT returned a malformed UNSUBACK")
			}
			ackID := binary.BigEndian.Uint16(packet.Body[:2])
			topicFilters, ok := pendingUnsubscribes[ackID]
			if !ok || validateBaiduIoTCoreMQTTUnsubAck(packet, ackID, topicFilters, plan.ProtocolVersion) != nil {
				return InvocationResult{}, fmt.Errorf("Baidu IoT Core MQTT returned a malformed UNSUBACK")
			}
			delete(pendingUnsubscribes, ackID)
		case 4:
			ackID, ackErr := parseBaiduIoTCoreMQTTAcknowledgement(packet, 4, plan.ProtocolVersion)
			if ackErr != nil {
				return InvocationResult{}, fmt.Errorf("Baidu IoT Core MQTT returned a malformed PUBACK")
			}
			if qos, ok := pendingPublishes[ackID]; !ok || qos != 1 {
				return InvocationResult{}, fmt.Errorf("Baidu IoT Core MQTT returned an unknown PUBACK")
			}
			delete(pendingPublishes, ackID)
		case 5:
			ackID, ackErr := parseBaiduIoTCoreMQTTAcknowledgement(packet, 5, plan.ProtocolVersion)
			if ackErr != nil || pendingPublishes[ackID] != 2 {
				return InvocationResult{}, fmt.Errorf("Baidu IoT Core MQTT returned an invalid PUBREC")
			}
			if err := connection.Write(handshakeCtx, cloudWebSocketMessageBinary, encodeAzureWebPubSubMQTTAcknowledgement(6, ackID, plan.ProtocolVersion)); err != nil {
				return InvocationResult{}, fmt.Errorf("send Baidu IoT Core MQTT PUBREL")
			}
			pendingPublishes[ackID] = -2
		case 6:
			if delivered, err := completeBaiduIoTCoreMQTTQoS2(handshakeCtx, connection, sink, plan, packet, incomingQoS2, completedQoS2); err != nil {
				return InvocationResult{}, err
			} else if delivered {
				received++
			}
		case 7:
			ackID, ackErr := parseBaiduIoTCoreMQTTAcknowledgement(packet, 7, plan.ProtocolVersion)
			if ackErr != nil || pendingPublishes[ackID] != -2 {
				return InvocationResult{}, fmt.Errorf("Baidu IoT Core MQTT returned an invalid PUBCOMP")
			}
			delete(pendingPublishes, ackID)
		default:
			return InvocationResult{}, fmt.Errorf("Baidu IoT Core MQTT returned an unexpected acknowledgement packet")
		}
	}
	cancelHandshake()
	if len(plan.Subscriptions) > 0 && received < plan.MaxMessages {
		collectionCtx, cancelCollection := context.WithTimeout(ctx, time.Duration(plan.TimeoutSeconds)*time.Second)
		defer cancelCollection()
		pingOutstanding := false
		for received < plan.MaxMessages {
			readCtx, cancelRead := context.WithTimeout(collectionCtx, time.Duration(plan.KeepAliveSeconds)*time.Second/2)
			packet, err = reader.Read(readCtx)
			cancelRead()
			if errors.Is(err, context.DeadlineExceeded) && collectionCtx.Err() == nil {
				if pingOutstanding {
					return InvocationResult{}, fmt.Errorf("Baidu IoT Core MQTT broker did not answer PINGREQ")
				}
				if err := connection.Write(collectionCtx, cloudWebSocketMessageBinary, []byte{0xc0, 0x00}); err != nil {
					return InvocationResult{}, fmt.Errorf("send Baidu IoT Core MQTT PINGREQ")
				}
				pingOutstanding = true
				continue
			}
			if errors.Is(err, context.DeadlineExceeded) {
				break
			}
			if err != nil {
				return InvocationResult{}, fmt.Errorf("read Baidu IoT Core MQTT message")
			}
			switch packet.Header >> 4 {
			case 3:
				if err := validateBaiduIoTCoreMQTTInboundPayload(packet, plan.MaxPayloadBytes, plan.ProtocolVersion); err != nil {
					return InvocationResult{}, err
				}
				delivered, err := acceptBaiduIoTCoreMQTTPublish(collectionCtx, connection, sink, plan, packet, incomingQoS2, completedQoS2, plan.MaxMessages-received)
				if err != nil {
					return InvocationResult{}, fmt.Errorf("Baidu IoT Core MQTT returned an invalid publish")
				}
				if delivered {
					received++
				}
			case 6:
				delivered, err := completeBaiduIoTCoreMQTTQoS2(collectionCtx, connection, sink, plan, packet, incomingQoS2, completedQoS2)
				if err != nil {
					return InvocationResult{}, err
				}
				if delivered {
					received++
				}
			case 13:
				if packet.Header != 0xd0 || len(packet.Body) != 0 {
					return InvocationResult{}, fmt.Errorf("Baidu IoT Core MQTT returned a malformed PINGRESP")
				}
				pingOutstanding = false
			case 14:
				if validateBaiduIoTCoreMQTTDisconnect(packet, plan.ProtocolVersion) != nil {
					return InvocationResult{}, fmt.Errorf("Baidu IoT Core MQTT returned a malformed DISCONNECT")
				}
				goto collectionComplete
			default:
				return InvocationResult{}, fmt.Errorf("Baidu IoT Core MQTT returned an unexpected packet")
			}
		}
	}

collectionComplete:
	if err := connection.Write(ctx, cloudWebSocketMessageBinary, []byte{0xe0, 0x00}); err != nil {
		return InvocationResult{}, fmt.Errorf("send Baidu IoT Core MQTT DISCONNECT")
	}
	if len(plan.Subscriptions) > 0 {
		output, err := sink.finish(plan.ClientID)
		if err != nil {
			return InvocationResult{}, err
		}
		return InvocationResult{Output: output, RequestID: plan.ClientID}, nil
	}
	output, _ := json.Marshal(map[string]any{"published": len(plan.Publishes), "received": received, "unsubscribed": len(plan.Unsubscriptions)})
	return InvocationResult{Output: output, RequestID: plan.ClientID}, nil
}

func defaultBaiduIoTCoreMQTTWebSocketDial(ctx context.Context, target string) (cloudWebSocketConnection, error) {
	client := &http.Client{Transport: http.DefaultTransport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	connection, response, err := websocket.Dial(ctx, target, &websocket.DialOptions{
		HTTPClient: client, CompressionMode: websocket.CompressionDisabled, Subprotocols: []string{"mqtt"},
	})
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("Baidu IoT Core MQTT WSS handshake failed with HTTP %d", response.StatusCode)
		}
		return nil, fmt.Errorf("Baidu IoT Core MQTT WSS handshake failed")
	}
	if connection.Subprotocol() != "mqtt" {
		_ = connection.Close(websocket.StatusProtocolError, "mqtt subprotocol required")
		return nil, fmt.Errorf("Baidu IoT Core MQTT WSS did not negotiate the mqtt subprotocol")
	}
	connection.SetReadLimit(baiduIoTCoreMQTTMaxPacketBytes)
	return coderTencentWebSocketConnection{connection: connection}, nil
}

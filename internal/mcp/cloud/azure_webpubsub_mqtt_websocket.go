package cloud

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/coder/websocket"
)

const (
	authSchemeAzureWebPubSubMQTTWS   = "webpubsub-mqtt-ws"
	azureWebPubSubMQTTMaxTopics      = 8
	azureWebPubSubMQTTMaxMessages    = 256
	azureWebPubSubMQTTMaxTopicBytes  = 256
	azureWebPubSubMQTTMaxTimeout     = 300
	azureWebPubSubMQTTMaxKeepAlive   = 180
	azureWebPubSubMQTTSubscribeID    = 1
	azureWebPubSubMQTTMaxPacketBytes = 146 * 1024
)

var azureWebPubSubMQTTClientIDPattern = regexp.MustCompile(`^[A-Za-z0-9]{1,128}$`)

type azureWebPubSubMQTTSubscription struct {
	TopicFilter string `json:"topic_filter"`
	QoS         int    `json:"qos"`
}

type azureWebPubSubMQTTPublish struct {
	Topic                string `json:"topic"`
	QoS                  int    `json:"qos"`
	PayloadBase64        string `json:"payload_base64"`
	PayloadFormat        int    `json:"payload_format,omitempty"`
	ContentType          string `json:"content_type,omitempty"`
	MessageExpirySeconds uint32 `json:"message_expiry_seconds,omitempty"`

	Payload []byte `json:"-"`
}

type azureWebPubSubMQTTWill struct {
	Topic                string `json:"topic"`
	QoS                  int    `json:"qos"`
	PayloadBase64        string `json:"payload_base64"`
	PayloadFormat        int    `json:"payload_format,omitempty"`
	ContentType          string `json:"content_type,omitempty"`
	MessageExpirySeconds uint32 `json:"message_expiry_seconds,omitempty"`
	DelaySeconds         uint32 `json:"delay_seconds,omitempty"`

	Payload []byte `json:"-"`
}

type azureWebPubSubMQTTPlan struct {
	ProtocolVersion        int                              `json:"protocol_version,omitempty"`
	ClientID               string                           `json:"client_id"`
	CleanStart             bool                             `json:"clean_start,omitempty"`
	SessionExpirySeconds   uint32                           `json:"session_expiry_seconds,omitempty"`
	SubscriptionIdentifier uint32                           `json:"subscription_identifier,omitempty"`
	Subscriptions          []azureWebPubSubMQTTSubscription `json:"subscriptions,omitempty"`
	Publishes              []azureWebPubSubMQTTPublish      `json:"publishes,omitempty"`
	Will                   *azureWebPubSubMQTTWill          `json:"will,omitempty"`
	KeepAliveSeconds       int                              `json:"keep_alive_seconds"`
	MaxMessages            int                              `json:"max_messages"`
	TimeoutSeconds         int                              `json:"timeout_seconds"`
}

type azureWebPubSubMQTTConnAckCapabilities struct {
	ReceiveMaximum    uint16
	MaximumPacketSize uint32
	MaximumQoS        byte
	ServerKeepAlive   *uint16
}

type azureWebPubSubMQTTPacket struct {
	Header byte
	Body   []byte
}

type azureWebPubSubMQTTPacketReader struct {
	connection cloudWebSocketConnection
	buffer     []byte
}

type azureWebPubSubMQTT5PropertyKind byte

const (
	azureWebPubSubMQTT5PropertyByte azureWebPubSubMQTT5PropertyKind = iota
	azureWebPubSubMQTT5PropertyUint16
	azureWebPubSubMQTT5PropertyNonzeroUint16
	azureWebPubSubMQTT5PropertyUint32
	azureWebPubSubMQTT5PropertyNonzeroUint32
	azureWebPubSubMQTT5PropertyUTF8
	azureWebPubSubMQTT5PropertyBinary
	azureWebPubSubMQTT5PropertyUTF8Pair
)

var (
	azureWebPubSubMQTT5ConnAckProperties = map[int]azureWebPubSubMQTT5PropertyKind{
		0x11: azureWebPubSubMQTT5PropertyUint32, 0x12: azureWebPubSubMQTT5PropertyUTF8,
		0x13: azureWebPubSubMQTT5PropertyUint16, 0x15: azureWebPubSubMQTT5PropertyUTF8,
		0x16: azureWebPubSubMQTT5PropertyBinary, 0x1a: azureWebPubSubMQTT5PropertyUTF8,
		0x1c: azureWebPubSubMQTT5PropertyUTF8, 0x1f: azureWebPubSubMQTT5PropertyUTF8,
		0x21: azureWebPubSubMQTT5PropertyNonzeroUint16, 0x22: azureWebPubSubMQTT5PropertyUint16,
		0x24: azureWebPubSubMQTT5PropertyByte, 0x25: azureWebPubSubMQTT5PropertyByte,
		0x26: azureWebPubSubMQTT5PropertyUTF8Pair, 0x27: azureWebPubSubMQTT5PropertyNonzeroUint32,
		0x28: azureWebPubSubMQTT5PropertyByte, 0x29: azureWebPubSubMQTT5PropertyByte,
		0x2a: azureWebPubSubMQTT5PropertyByte,
	}
	azureWebPubSubMQTT5ReasonProperties = map[int]azureWebPubSubMQTT5PropertyKind{
		0x1f: azureWebPubSubMQTT5PropertyUTF8, 0x26: azureWebPubSubMQTT5PropertyUTF8Pair,
	}
	azureWebPubSubMQTT5DisconnectProperties = map[int]azureWebPubSubMQTT5PropertyKind{
		0x11: azureWebPubSubMQTT5PropertyUint32, 0x1c: azureWebPubSubMQTT5PropertyUTF8,
		0x1f: azureWebPubSubMQTT5PropertyUTF8, 0x26: azureWebPubSubMQTT5PropertyUTF8Pair,
	}
)

func validateAzureWebPubSubMQTTInvocation(invocation Invocation) error {
	operation := strings.ToLower(strings.TrimSpace(invocation.Operation))
	mutating := operation == "clientmqtt"
	if !strings.EqualFold(invocation.Service, "webpubsub") || operation != "subscribemqtt" && !mutating || !strings.EqualFold(invocation.Method, http.MethodGet) {
		return fmt.Errorf("Azure Web PubSub MQTT requires GET, service webpubsub, and operation SubscribeMQTT or ClientMQTT")
	}
	if mutating && invocation.Mode != ModeMutate || !mutating && invocation.Mode != ModeRead {
		return fmt.Errorf("Azure Web PubSub MQTT ClientMQTT requires the mutate tool and SubscribeMQTT requires the read tool")
	}
	if _, _, err := parseAzureWebPubSubMQTTTarget(invocation.URL); err != nil {
		return err
	}
	if len(invocation.Headers) != 0 || len(invocation.Parameters) != 0 || invocation.Body == nil || invocation.BodyFile != "" || invocation.ResponseFile == "" {
		return fmt.Errorf("Azure Web PubSub MQTT requires only a finite protocol body and response_file")
	}
	if invocation.Region != "" || invocation.RegionSet != "" || invocation.Project != "" || invocation.Subscription != "" || invocation.Audience != "" || invocation.APIVersion != "" || invocation.AuthVersion != "" || invocation.PayloadMode != "" || invocation.ChecksumAlgorithm != "" || invocation.StreamChunkBytes != 0 || invocation.StreamIntervalMS != 0 || invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return fmt.Errorf("Azure Web PubSub MQTT does not accept REST, cross-provider, or generic stream controls")
	}
	_, err := parseAzureWebPubSubMQTTPlan(invocation.Body, mutating)
	return err
}

func parseAzureWebPubSubMQTTTarget(rawURL string) (*url.URL, string, error) {
	target, err := url.Parse(rawURL)
	if err != nil || target.Scheme != "wss" || target.User != nil || target.Fragment != "" || target.RawQuery != "" || target.Port() != "" {
		return nil, "", fmt.Errorf("Azure Web PubSub MQTT requires an exact credential-free wss:// URL")
	}
	host := strings.ToLower(target.Hostname())
	const suffix = ".webpubsub.azure.com"
	resource := strings.TrimSuffix(host, suffix)
	if resource == host || !endpointLabelPattern.MatchString(resource) || len(resource) < 3 || len(resource) > 63 {
		return nil, "", fmt.Errorf("Azure Web PubSub MQTT requires a resource.webpubsub.azure.com host")
	}
	const prefix = "/clients/mqtt/hubs/"
	if !strings.HasPrefix(target.Path, prefix) {
		return nil, "", fmt.Errorf("Azure Web PubSub MQTT requires the official /clients/mqtt/hubs/{hub} path")
	}
	hub := strings.TrimPrefix(target.Path, prefix)
	if !azureWebPubSubHubPattern.MatchString(hub) {
		return nil, "", fmt.Errorf("Azure Web PubSub MQTT requires one valid hub path segment")
	}
	return target, hub, nil
}

func parseAzureWebPubSubMQTTPlan(body any, mutating ...bool) (azureWebPubSubMQTTPlan, error) {
	if body == nil || azureRealtimeContainsCredentialField(body) {
		return azureWebPubSubMQTTPlan{}, fmt.Errorf("Azure Web PubSub MQTT requires a credential-free protocol body")
	}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) == 0 || len(encoded) > maxRequestPayloadBytes {
		return azureWebPubSubMQTTPlan{}, fmt.Errorf("Azure Web PubSub MQTT body must be bounded JSON")
	}
	plan := azureWebPubSubMQTTPlan{ProtocolVersion: 4, CleanStart: true}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&plan) != nil || ensureJSONDecoderEOF(decoder) != nil {
		return azureWebPubSubMQTTPlan{}, fmt.Errorf("Azure Web PubSub MQTT body does not match the finite subscription schema")
	}
	if !azureWebPubSubMQTTClientIDPattern.MatchString(plan.ClientID) {
		return azureWebPubSubMQTTPlan{}, fmt.Errorf("Azure Web PubSub MQTT client_id must contain 1 to 128 ASCII letters or digits")
	}
	isMutating := len(mutating) > 0 && mutating[0]
	if plan.ProtocolVersion != 4 && plan.ProtocolVersion != 5 {
		return azureWebPubSubMQTTPlan{}, fmt.Errorf("Azure Web PubSub MQTT protocol_version must be 4 or 5")
	}
	if len(plan.Subscriptions) > azureWebPubSubMQTTMaxTopics {
		return azureWebPubSubMQTTPlan{}, fmt.Errorf("Azure Web PubSub MQTT supports at most %d subscriptions", azureWebPubSubMQTTMaxTopics)
	}
	if !isMutating && len(plan.Subscriptions) < 1 {
		return azureWebPubSubMQTTPlan{}, fmt.Errorf("Azure Web PubSub MQTT read mode requires at least one subscription")
	}
	seen := make(map[string]bool)
	for _, subscription := range plan.Subscriptions {
		if !validAWSIoTMQTTString(subscription.TopicFilter, azureWebPubSubMQTTMaxTopicBytes) || strings.ContainsAny(subscription.TopicFilter, "#+") || strings.HasPrefix(subscription.TopicFilter, "$share/") {
			return azureWebPubSubMQTTPlan{}, fmt.Errorf("Azure Web PubSub MQTT topic_filter must be one bounded exact topic without wildcards or shared-subscription syntax")
		}
		if seen[subscription.TopicFilter] {
			return azureWebPubSubMQTTPlan{}, fmt.Errorf("Azure Web PubSub MQTT topic_filter values must be unique")
		}
		seen[subscription.TopicFilter] = true
		if subscription.QoS < 0 || subscription.QoS > 2 {
			return azureWebPubSubMQTTPlan{}, fmt.Errorf("Azure Web PubSub MQTT subscription QoS must be 0, 1, or 2")
		}
	}
	if len(plan.Publishes) > azureWebPubSubMQTTMaxTopics {
		return azureWebPubSubMQTTPlan{}, fmt.Errorf("Azure Web PubSub MQTT supports at most %d initial publishes", azureWebPubSubMQTTMaxTopics)
	}
	for index := range plan.Publishes {
		if err := validateAzureWebPubSubMQTTPublish(&plan.Publishes[index], azureWebPubSubMQTTMaxPacketBytes); err != nil {
			return azureWebPubSubMQTTPlan{}, fmt.Errorf("Azure Web PubSub MQTT publish %d: %w", index, err)
		}
	}
	if plan.Will != nil {
		if err := validateAzureWebPubSubMQTTWill(plan.Will); err != nil {
			return azureWebPubSubMQTTPlan{}, err
		}
	}
	if !isMutating && (len(plan.Publishes) > 0 || plan.Will != nil || !plan.CleanStart || plan.SessionExpirySeconds != 0) {
		return azureWebPubSubMQTTPlan{}, fmt.Errorf("Azure Web PubSub MQTT read mode forbids publish, Last Will, and persistent-session behavior")
	}
	if isMutating && len(plan.Subscriptions) == 0 && len(plan.Publishes) == 0 {
		return azureWebPubSubMQTTPlan{}, fmt.Errorf("Azure Web PubSub MQTT ClientMQTT requires a subscription or publish")
	}
	if plan.ProtocolVersion == 4 && (plan.SessionExpirySeconds != 0 || plan.SubscriptionIdentifier != 0) {
		return azureWebPubSubMQTTPlan{}, fmt.Errorf("Azure Web PubSub MQTT session expiry and subscription identifiers require MQTT 5")
	}
	if plan.ProtocolVersion == 4 {
		for _, publish := range plan.Publishes {
			if publish.PayloadFormat != 0 || publish.ContentType != "" || publish.MessageExpirySeconds != 0 {
				return azureWebPubSubMQTTPlan{}, fmt.Errorf("Azure Web PubSub MQTT publish metadata requires MQTT 5")
			}
		}
		if plan.Will != nil && (plan.Will.PayloadFormat != 0 || plan.Will.ContentType != "" || plan.Will.MessageExpirySeconds != 0 || plan.Will.DelaySeconds != 0) {
			return azureWebPubSubMQTTPlan{}, fmt.Errorf("Azure Web PubSub MQTT Last Will metadata requires MQTT 5")
		}
	}
	if plan.SessionExpirySeconds > 30 {
		return azureWebPubSubMQTTPlan{}, fmt.Errorf("Azure Web PubSub MQTT session_expiry_seconds must be at most 30")
	}
	if plan.SubscriptionIdentifier > 268435455 || plan.SubscriptionIdentifier != 0 && len(plan.Subscriptions) == 0 {
		return azureWebPubSubMQTTPlan{}, fmt.Errorf("Azure Web PubSub MQTT subscription_identifier is invalid")
	}
	if plan.KeepAliveSeconds < 1 || plan.KeepAliveSeconds > azureWebPubSubMQTTMaxKeepAlive {
		return azureWebPubSubMQTTPlan{}, fmt.Errorf("Azure Web PubSub MQTT keep_alive_seconds must be between 1 and %d", azureWebPubSubMQTTMaxKeepAlive)
	}
	if plan.MaxMessages < 0 || plan.MaxMessages > azureWebPubSubMQTTMaxMessages || len(plan.Subscriptions) > 0 && plan.MaxMessages < 1 || plan.TimeoutSeconds < 1 || plan.TimeoutSeconds > azureWebPubSubMQTTMaxTimeout {
		return azureWebPubSubMQTTPlan{}, fmt.Errorf("Azure Web PubSub MQTT message count or timeout is outside the finite bound")
	}
	for index, publish := range plan.Publishes {
		packetID := uint16(0)
		if publish.QoS > 0 {
			packetID = uint16(index + 2)
		}
		if _, err := encodeAzureWebPubSubMQTTPublish(plan, publish, packetID); err != nil {
			return azureWebPubSubMQTTPlan{}, fmt.Errorf("Azure Web PubSub MQTT publish %d exceeds the packet bound", index)
		}
	}
	return plan, nil
}

func validateAzureWebPubSubMQTTPublish(publish *azureWebPubSubMQTTPublish, maxPayload int) error {
	if !validAWSIoTMQTTString(publish.Topic, 1024) || strings.ContainsAny(publish.Topic, "#+") || strings.HasPrefix(publish.Topic, "$share/") {
		return fmt.Errorf("topic must be one bounded exact MQTT topic")
	}
	if publish.QoS < 0 || publish.QoS > 2 || publish.PayloadFormat < 0 || publish.PayloadFormat > 1 || publish.ContentType != "" && (!azureWebPubSubBoundedValue(publish.ContentType, 256) || !validAzureWebPubSubMQTTUTF8([]byte(publish.ContentType))) {
		return fmt.Errorf("publish QoS or MQTT 5 metadata is invalid")
	}
	payload, err := base64.StdEncoding.Strict().DecodeString(publish.PayloadBase64)
	if err != nil || len(payload) > maxPayload {
		return fmt.Errorf("payload_base64 is invalid or too large")
	}
	if publish.PayloadFormat == 1 && !utf8.Valid(payload) {
		return fmt.Errorf("UTF-8 payload format requires UTF-8 data")
	}
	publish.Payload = payload
	return nil
}

func validateAzureWebPubSubMQTTWill(will *azureWebPubSubMQTTWill) error {
	publish := azureWebPubSubMQTTPublish{
		Topic: will.Topic, QoS: will.QoS, PayloadBase64: will.PayloadBase64, PayloadFormat: will.PayloadFormat,
		ContentType: will.ContentType, MessageExpirySeconds: will.MessageExpirySeconds,
	}
	if err := validateAzureWebPubSubMQTTPublish(&publish, 2000); err != nil {
		return fmt.Errorf("Azure Web PubSub MQTT Last Will: %w", err)
	}
	if will.DelaySeconds > 30 {
		return fmt.Errorf("Azure Web PubSub MQTT Last Will delay_seconds must be at most 30")
	}
	will.Payload = publish.Payload
	return nil
}

func invokeAzureWebPubSubMQTT(ctx context.Context, adapter *AzureRESTAdapter, invocation Invocation) (InvocationResult, error) {
	if err := validateAzureWebPubSubMQTTInvocation(invocation); err != nil {
		return InvocationResult{}, err
	}
	mutating := strings.EqualFold(strings.TrimSpace(invocation.Operation), "ClientMQTT")
	target, hub, _ := parseAzureWebPubSubMQTTTarget(invocation.URL)
	plan, _ := parseAzureWebPubSubMQTTPlan(invocation.Body, mutating)
	identityToken, err := adapter.config.Tokens.Token(ctx, "https://webpubsub.azure.com/.default")
	if err != nil {
		return InvocationResult{}, fmt.Errorf("load Azure Web PubSub MQTT identity token: %w", err)
	}
	if strings.TrimSpace(identityToken) == "" {
		return InvocationResult{}, fmt.Errorf("Azure identity returned an empty access token")
	}
	clientToken, err := mintAzureWebPubSubMQTTClientToken(ctx, adapter, target, hub, plan, identityToken)
	if err != nil {
		return InvocationResult{}, err
	}
	handshakeCtx, cancelHandshake := context.WithTimeout(ctx, 30*time.Second)
	defer cancelHandshake()
	connection, err := adapter.config.WebPubSubMQTTWebSocketDial(handshakeCtx, invocation.URL, http.Header{"Authorization": []string{"Bearer " + clientToken}})
	if err != nil {
		return InvocationResult{}, err
	}
	defer connection.Close()
	sink, err := newWebSocketOutputSink(invocation, adapter.config.MaxBodyBytes, "Azure Web PubSub MQTT WebSocket")
	if err != nil {
		return InvocationResult{}, err
	}
	defer sink.abort()
	connectPacket, err := encodeAzureWebPubSubMQTTConnect(plan)
	if err != nil {
		return InvocationResult{}, err
	}
	if err := connection.Write(handshakeCtx, cloudWebSocketMessageBinary, connectPacket); err != nil {
		return InvocationResult{}, fmt.Errorf("send Azure Web PubSub MQTT CONNECT")
	}
	reader := &azureWebPubSubMQTTPacketReader{connection: connection}
	packet, err := reader.Read(handshakeCtx)
	capabilities, connAckErr := parseAzureWebPubSubMQTTConnAck(packet, plan)
	if err != nil || connAckErr != nil {
		return InvocationResult{}, fmt.Errorf("Azure Web PubSub MQTT connection was not acknowledged")
	}
	mqttConnected := true
	defer func() {
		if mqttConnected {
			disconnectCtx, cancelDisconnect := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cancelDisconnect()
			_ = connection.Write(disconnectCtx, cloudWebSocketMessageBinary, []byte{0xe0, 0x00})
		}
	}()
	if capabilities.MaximumPacketSize < 2 {
		return InvocationResult{}, fmt.Errorf("Azure Web PubSub MQTT broker maximum packet size cannot carry client control packets")
	}
	acknowledgementsRequired := false
	for _, subscription := range plan.Subscriptions {
		acknowledgementsRequired = acknowledgementsRequired || subscription.QoS > 0
	}
	for _, publish := range plan.Publishes {
		acknowledgementsRequired = acknowledgementsRequired || publish.QoS > 0
	}
	if acknowledgementsRequired && capabilities.MaximumPacketSize < 4 {
		return InvocationResult{}, fmt.Errorf("Azure Web PubSub MQTT broker maximum packet size cannot carry QoS acknowledgements")
	}
	incomingQoS2 := make(map[uint16][]byte)
	completedIncomingQoS2 := make(map[uint16]bool)
	received := 0
	if len(plan.Subscriptions) > 0 {
		subscribePacket, err := encodeAzureWebPubSubMQTTSubscribe(plan)
		if err != nil {
			return InvocationResult{}, err
		}
		if uint32(len(subscribePacket)) > capabilities.MaximumPacketSize {
			return InvocationResult{}, fmt.Errorf("Azure Web PubSub MQTT SUBSCRIBE exceeds broker maximum packet size")
		}
		if err := connection.Write(handshakeCtx, cloudWebSocketMessageBinary, subscribePacket); err != nil {
			return InvocationResult{}, fmt.Errorf("send Azure Web PubSub MQTT SUBSCRIBE")
		}
		for {
			packet, err = reader.Read(handshakeCtx)
			if err != nil {
				return InvocationResult{}, fmt.Errorf("read Azure Web PubSub MQTT SUBACK")
			}
			if packet.Header>>4 == 3 {
				if received >= plan.MaxMessages {
					return InvocationResult{}, fmt.Errorf("Azure Web PubSub MQTT broker exceeded max_messages before SUBACK")
				}
				delivered, err := acceptAzureWebPubSubMQTTPublish(handshakeCtx, connection, sink, plan, packet, incomingQoS2, completedIncomingQoS2)
				if err != nil {
					return InvocationResult{}, err
				}
				if len(incomingQoS2) > plan.MaxMessages-received {
					return InvocationResult{}, fmt.Errorf("Azure Web PubSub MQTT broker exceeded pending QoS 2 message bound")
				}
				if delivered {
					received++
				}
				continue
			}
			if err := validateAzureWebPubSubMQTTSubAckForPlan(packet, plan); err != nil {
				return InvocationResult{}, err
			}
			break
		}
	}
	encodedPublishes := make([][]byte, len(plan.Publishes))
	for index, publish := range plan.Publishes {
		packetID := uint16(0)
		if publish.QoS > 0 {
			packetID = uint16(index + 2)
		}
		encoded, err := encodeAzureWebPubSubMQTTPublish(plan, publish, packetID)
		if err != nil {
			return InvocationResult{}, err
		}
		if publish.QoS > int(capabilities.MaximumQoS) || uint32(len(encoded)) > capabilities.MaximumPacketSize {
			return InvocationResult{}, fmt.Errorf("Azure Web PubSub MQTT publish exceeds broker flow-control limits")
		}
		encodedPublishes[index] = encoded
	}
	pendingOutbound := make(map[uint16]byte)
	nextOutbound := 0
	inflight := 0
	sendAvailablePublishes := func(writeCtx context.Context) error {
		for nextOutbound < len(plan.Publishes) {
			publish := plan.Publishes[nextOutbound]
			if publish.QoS > 0 && inflight >= int(capabilities.ReceiveMaximum) {
				return nil
			}
			if err := connection.Write(writeCtx, cloudWebSocketMessageBinary, encodedPublishes[nextOutbound]); err != nil {
				return fmt.Errorf("send Azure Web PubSub MQTT PUBLISH")
			}
			if publish.QoS > 0 {
				pendingOutbound[uint16(nextOutbound+2)] = byte(publish.QoS)
				inflight++
			}
			nextOutbound++
		}
		return nil
	}
	if err := sendAvailablePublishes(handshakeCtx); err != nil {
		return InvocationResult{}, err
	}
	cancelHandshake()
	collectionCtx, cancelCollection := context.WithTimeout(ctx, time.Duration(plan.TimeoutSeconds)*time.Second)
	defer cancelCollection()
	pingOutstanding := false
	negotiatedKeepAlive := uint16(plan.KeepAliveSeconds)
	if capabilities.ServerKeepAlive != nil {
		negotiatedKeepAlive = *capabilities.ServerKeepAlive
	}
	remoteEnded := false
	for !remoteEnded && (received < plan.MaxMessages || len(pendingOutbound) > 0 || len(incomingQoS2) > 0 || nextOutbound < len(plan.Publishes)) {
		readCtx := collectionCtx
		cancelRead := func() {}
		if negotiatedKeepAlive > 0 {
			readCtx, cancelRead = context.WithTimeout(collectionCtx, time.Duration(negotiatedKeepAlive)*time.Second/2)
		}
		packet, err = reader.Read(readCtx)
		cancelRead()
		if errors.Is(err, context.DeadlineExceeded) && collectionCtx.Err() == nil {
			if pingOutstanding {
				return InvocationResult{}, fmt.Errorf("Azure Web PubSub MQTT broker did not answer PINGREQ")
			}
			if err := connection.Write(collectionCtx, cloudWebSocketMessageBinary, []byte{0xc0, 0x00}); err != nil {
				return InvocationResult{}, fmt.Errorf("send Azure Web PubSub MQTT PINGREQ")
			}
			pingOutstanding = true
			continue
		}
		if errors.Is(err, context.DeadlineExceeded) || websocket.CloseStatus(err) == websocket.StatusNormalClosure {
			if len(pendingOutbound) > 0 || len(incomingQoS2) > 0 || nextOutbound < len(plan.Publishes) {
				return InvocationResult{}, fmt.Errorf("Azure Web PubSub MQTT session ended with incomplete QoS acknowledgements")
			}
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				mqttConnected = false
				remoteEnded = true
			}
			break
		}
		if err != nil {
			return InvocationResult{}, fmt.Errorf("read Azure Web PubSub MQTT message")
		}
		switch packet.Header >> 4 {
		case 3:
			if received >= plan.MaxMessages {
				return InvocationResult{}, fmt.Errorf("Azure Web PubSub MQTT broker exceeded max_messages")
			}
			delivered, err := acceptAzureWebPubSubMQTTPublish(collectionCtx, connection, sink, plan, packet, incomingQoS2, completedIncomingQoS2)
			if err != nil {
				return InvocationResult{}, err
			}
			if len(incomingQoS2) > plan.MaxMessages-received {
				return InvocationResult{}, fmt.Errorf("Azure Web PubSub MQTT broker exceeded pending QoS 2 message bound")
			}
			if delivered {
				received++
			}
		case 4:
			packetID, err := parseAzureWebPubSubMQTTAcknowledgement(packet, 4, plan.ProtocolVersion)
			if err != nil || pendingOutbound[packetID] != 1 {
				return InvocationResult{}, fmt.Errorf("Azure Web PubSub MQTT returned an invalid PUBACK")
			}
			delete(pendingOutbound, packetID)
			inflight--
			if err := sendAvailablePublishes(collectionCtx); err != nil {
				return InvocationResult{}, err
			}
		case 5:
			packetID, err := parseAzureWebPubSubMQTTAcknowledgement(packet, 5, plan.ProtocolVersion)
			if err != nil || pendingOutbound[packetID] != 2 {
				return InvocationResult{}, fmt.Errorf("Azure Web PubSub MQTT returned an invalid PUBREC")
			}
			pendingOutbound[packetID] = 3
			if err := connection.Write(collectionCtx, cloudWebSocketMessageBinary, encodeAzureWebPubSubMQTTAcknowledgement(6, packetID, plan.ProtocolVersion)); err != nil {
				return InvocationResult{}, fmt.Errorf("send Azure Web PubSub MQTT PUBREL")
			}
		case 6:
			packetID, err := parseAzureWebPubSubMQTTAcknowledgement(packet, 6, plan.ProtocolVersion)
			if err != nil {
				return InvocationResult{}, fmt.Errorf("Azure Web PubSub MQTT returned an invalid PUBREL")
			}
			message, exists := incomingQoS2[packetID]
			if exists {
				if received >= plan.MaxMessages {
					return InvocationResult{}, fmt.Errorf("Azure Web PubSub MQTT broker exceeded max_messages")
				}
				if err := sink.writeMessage(message); err != nil {
					return InvocationResult{}, err
				}
				delete(incomingQoS2, packetID)
				completedIncomingQoS2[packetID] = true
				received++
			} else if !completedIncomingQoS2[packetID] {
				return InvocationResult{}, fmt.Errorf("Azure Web PubSub MQTT PUBREL referenced an unknown packet")
			}
			if err := connection.Write(collectionCtx, cloudWebSocketMessageBinary, encodeAzureWebPubSubMQTTAcknowledgement(7, packetID, plan.ProtocolVersion)); err != nil {
				return InvocationResult{}, fmt.Errorf("send Azure Web PubSub MQTT PUBCOMP")
			}
		case 7:
			packetID, err := parseAzureWebPubSubMQTTAcknowledgement(packet, 7, plan.ProtocolVersion)
			if err != nil || pendingOutbound[packetID] != 3 {
				return InvocationResult{}, fmt.Errorf("Azure Web PubSub MQTT returned an invalid PUBCOMP")
			}
			delete(pendingOutbound, packetID)
			inflight--
			if err := sendAvailablePublishes(collectionCtx); err != nil {
				return InvocationResult{}, err
			}
		case 13:
			if packet.Header != 0xd0 || len(packet.Body) != 0 {
				return InvocationResult{}, fmt.Errorf("Azure Web PubSub MQTT broker returned a malformed PINGRESP")
			}
			pingOutstanding = false
		case 14:
			if err := validateAzureWebPubSubMQTTDisconnect(packet, plan.ProtocolVersion); err != nil || len(pendingOutbound) > 0 || len(incomingQoS2) > 0 || nextOutbound < len(plan.Publishes) {
				return InvocationResult{}, fmt.Errorf("Azure Web PubSub MQTT broker disconnected with incomplete work")
			}
			mqttConnected = false
			remoteEnded = true
		default:
			return InvocationResult{}, fmt.Errorf("Azure Web PubSub MQTT broker returned an unexpected packet")
		}
	}
	if !remoteEnded {
		if err := connection.Write(ctx, cloudWebSocketMessageBinary, []byte{0xe0, 0x00}); err != nil {
			return InvocationResult{}, fmt.Errorf("send Azure Web PubSub MQTT DISCONNECT")
		}
		mqttConnected = false
	}
	metadata, err := sink.finish(plan.ClientID)
	if err != nil {
		return InvocationResult{}, err
	}
	var summary map[string]any
	if json.Unmarshal(metadata, &summary) == nil {
		summary["messages"] = received
		metadata, err = json.Marshal(summary)
		if err != nil {
			return InvocationResult{}, fmt.Errorf("encode Azure Web PubSub MQTT output metadata")
		}
	}
	return InvocationResult{Output: metadata, RequestID: plan.ClientID}, nil
}

func mintAzureWebPubSubMQTTClientToken(ctx context.Context, adapter *AzureRESTAdapter, target *url.URL, hub string, plan azureWebPubSubMQTTPlan, identityToken string) (string, error) {
	tokenURL := &url.URL{Scheme: "https", Host: target.Host, Path: "/api/hubs/" + hub + "/:generateToken"}
	query := tokenURL.Query()
	query.Set("api-version", azureWebPubSubAPIVersion)
	query.Set("clientType", "MQTT")
	query.Set("minutesToExpire", "5")
	for _, subscription := range plan.Subscriptions {
		query.Add("role", "webpubsub.joinLeaveGroup."+subscription.TopicFilter)
	}
	for _, publish := range plan.Publishes {
		query.Add("role", "webpubsub.sendToGroup."+publish.Topic)
	}
	if plan.Will != nil {
		query.Add("role", "webpubsub.sendToGroup."+plan.Will.Topic)
	}
	tokenURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL.String(), nil)
	if err != nil {
		return "", fmt.Errorf("build Azure Web PubSub MQTT client-token request")
	}
	request.Header.Set("Authorization", "Bearer "+identityToken)
	response, err := adapter.config.HTTP.Do(request)
	if err != nil {
		return "", fmt.Errorf("Azure Web PubSub MQTT client-token request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Azure Web PubSub MQTT client-token request failed with HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxRequestPayloadBytes+1))
	if err != nil || len(data) > maxRequestPayloadBytes {
		return "", fmt.Errorf("read Azure Web PubSub MQTT client-token response")
	}
	var payload struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(data, &payload) != nil || !azureWebPubSubBoundedValue(payload.Token, 16384) {
		return "", fmt.Errorf("Azure Web PubSub returned an invalid MQTT client-token response")
	}
	return payload.Token, nil
}

func (reader *azureWebPubSubMQTTPacketReader) Read(ctx context.Context) (azureWebPubSubMQTTPacket, error) {
	for {
		packet, consumed, complete, err := decodeAzureWebPubSubMQTTPacket(reader.buffer)
		if err != nil {
			return azureWebPubSubMQTTPacket{}, err
		}
		if complete {
			reader.buffer = append(reader.buffer[:0], reader.buffer[consumed:]...)
			return packet, nil
		}
		messageType, data, err := reader.connection.Read(ctx)
		if err != nil {
			return azureWebPubSubMQTTPacket{}, err
		}
		if messageType != cloudWebSocketMessageBinary {
			return azureWebPubSubMQTTPacket{}, fmt.Errorf("Azure Web PubSub MQTT returned a non-binary WebSocket frame")
		}
		if len(reader.buffer)+len(data) > azureWebPubSubMQTTMaxPacketBytes {
			return azureWebPubSubMQTTPacket{}, fmt.Errorf("Azure Web PubSub MQTT packet exceeds %d bytes", azureWebPubSubMQTTMaxPacketBytes)
		}
		reader.buffer = append(reader.buffer, data...)
	}
}

func decodeAzureWebPubSubMQTTPacket(data []byte) (azureWebPubSubMQTTPacket, int, bool, error) {
	if len(data) < 2 {
		return azureWebPubSubMQTTPacket{}, 0, false, nil
	}
	remaining, multiplier, index := 0, 1, 1
	for count := 0; count < 4; count++ {
		if index >= len(data) {
			return azureWebPubSubMQTTPacket{}, 0, false, nil
		}
		value := data[index]
		index++
		remaining += int(value&0x7f) * multiplier
		if remaining > azureWebPubSubMQTTMaxPacketBytes {
			return azureWebPubSubMQTTPacket{}, 0, false, fmt.Errorf("Azure Web PubSub MQTT packet exceeds %d bytes", azureWebPubSubMQTTMaxPacketBytes)
		}
		if value&0x80 == 0 {
			if count > 0 && value == 0 {
				return azureWebPubSubMQTTPacket{}, 0, false, fmt.Errorf("Azure Web PubSub MQTT remaining length is not minimally encoded")
			}
			total := index + remaining
			if total > azureWebPubSubMQTTMaxPacketBytes {
				return azureWebPubSubMQTTPacket{}, 0, false, fmt.Errorf("Azure Web PubSub MQTT packet exceeds %d bytes", azureWebPubSubMQTTMaxPacketBytes)
			}
			if len(data) < total {
				return azureWebPubSubMQTTPacket{}, 0, false, nil
			}
			return azureWebPubSubMQTTPacket{Header: data[0], Body: append([]byte(nil), data[index:total]...)}, total, true, nil
		}
		multiplier *= 128
	}
	return azureWebPubSubMQTTPacket{}, 0, false, fmt.Errorf("Azure Web PubSub MQTT remaining length is malformed")
}

func encodeAzureWebPubSubMQTTPacket(header byte, body []byte) ([]byte, error) {
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
	if len(encoded)+len(body) > azureWebPubSubMQTTMaxPacketBytes {
		return nil, fmt.Errorf("Azure Web PubSub MQTT packet exceeds %d bytes", azureWebPubSubMQTTMaxPacketBytes)
	}
	return append(encoded, body...), nil
}

func encodeAzureWebPubSubMQTTConnect(plan azureWebPubSubMQTTPlan) ([]byte, error) {
	flags := byte(0)
	if plan.CleanStart {
		flags |= 0x02
	}
	if plan.Will != nil {
		flags |= 0x04 | byte(plan.Will.QoS)<<3
	}
	body := []byte{0x00, 0x04, 'M', 'Q', 'T', 'T', byte(plan.ProtocolVersion), flags, byte(plan.KeepAliveSeconds >> 8), byte(plan.KeepAliveSeconds)}
	if plan.ProtocolVersion == 5 {
		properties := make([]byte, 0, 5)
		if plan.SessionExpirySeconds > 0 {
			properties = append(properties, 0x11)
			properties = appendMQTTUint32(properties, plan.SessionExpirySeconds)
		}
		body = appendMQTTVariableByteInteger(body, len(properties))
		body = append(body, properties...)
	}
	body = appendMQTTUTF8(body, plan.ClientID)
	if plan.Will != nil {
		if plan.ProtocolVersion == 5 {
			properties := encodeAzureWebPubSubMQTT5ApplicationProperties(plan.Will.PayloadFormat, plan.Will.ContentType, plan.Will.MessageExpirySeconds)
			if plan.Will.DelaySeconds > 0 {
				properties = append([]byte{0x18, byte(plan.Will.DelaySeconds >> 24), byte(plan.Will.DelaySeconds >> 16), byte(plan.Will.DelaySeconds >> 8), byte(plan.Will.DelaySeconds)}, properties...)
			}
			body = appendMQTTVariableByteInteger(body, len(properties))
			body = append(body, properties...)
		}
		body = appendMQTTUTF8(body, plan.Will.Topic)
		body = appendMQTTBinary(body, plan.Will.Payload)
	}
	return encodeAzureWebPubSubMQTTPacket(0x10, body)
}

func encodeAzureWebPubSubMQTTSubscribe(plan azureWebPubSubMQTTPlan) ([]byte, error) {
	body := []byte{byte(azureWebPubSubMQTTSubscribeID >> 8), byte(azureWebPubSubMQTTSubscribeID)}
	if plan.ProtocolVersion == 5 {
		properties := make([]byte, 0, 5)
		if plan.SubscriptionIdentifier > 0 {
			properties = append(properties, 0x0b)
			properties = appendMQTTVariableByteInteger(properties, int(plan.SubscriptionIdentifier))
		}
		body = appendMQTTVariableByteInteger(body, len(properties))
		body = append(body, properties...)
	}
	for _, subscription := range plan.Subscriptions {
		body = appendMQTTUTF8(body, subscription.TopicFilter)
		body = append(body, byte(subscription.QoS))
	}
	return encodeAzureWebPubSubMQTTPacket(0x82, body)
}

func encodeAzureWebPubSubMQTTPublish(plan azureWebPubSubMQTTPlan, publish azureWebPubSubMQTTPublish, packetID uint16) ([]byte, error) {
	body := appendMQTTUTF8(nil, publish.Topic)
	if publish.QoS > 0 {
		if packetID == 0 {
			return nil, fmt.Errorf("Azure Web PubSub MQTT QoS publish requires packet ID")
		}
		body = append(body, byte(packetID>>8), byte(packetID))
	}
	if plan.ProtocolVersion == 5 {
		properties := encodeAzureWebPubSubMQTT5ApplicationProperties(publish.PayloadFormat, publish.ContentType, publish.MessageExpirySeconds)
		body = appendMQTTVariableByteInteger(body, len(properties))
		body = append(body, properties...)
	}
	body = append(body, publish.Payload...)
	return encodeAzureWebPubSubMQTTPacket(0x30|byte(publish.QoS)<<1, body)
}

func encodeAzureWebPubSubMQTT5ApplicationProperties(payloadFormat int, contentType string, expiry uint32) []byte {
	properties := make([]byte, 0, 16+len(contentType))
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
	return properties
}

func appendMQTTVariableByteInteger(target []byte, value int) []byte {
	for {
		encoded := byte(value % 128)
		value /= 128
		if value > 0 {
			encoded |= 0x80
		}
		target = append(target, encoded)
		if value == 0 {
			return target
		}
	}
}

func appendMQTTUint32(target []byte, value uint32) []byte {
	return append(target, byte(value>>24), byte(value>>16), byte(value>>8), byte(value))
}

func appendMQTTBinary(target, value []byte) []byte {
	return append(append(target, byte(len(value)>>8), byte(len(value))), value...)
}

func validateAzureWebPubSubMQTTConnAck(packet azureWebPubSubMQTTPacket) error {
	return validateAzureWebPubSubMQTTConnAckForPlan(packet, azureWebPubSubMQTTPlan{ProtocolVersion: 4, CleanStart: true})
}

func validateAzureWebPubSubMQTTConnAckForPlan(packet azureWebPubSubMQTTPacket, plan azureWebPubSubMQTTPlan) error {
	_, err := parseAzureWebPubSubMQTTConnAck(packet, plan)
	return err
}

func parseAzureWebPubSubMQTTConnAck(packet azureWebPubSubMQTTPacket, plan azureWebPubSubMQTTPlan) (azureWebPubSubMQTTConnAckCapabilities, error) {
	capabilities := azureWebPubSubMQTTConnAckCapabilities{ReceiveMaximum: 65535, MaximumPacketSize: azureWebPubSubMQTTMaxPacketBytes, MaximumQoS: 2}
	minimum := 2
	if plan.ProtocolVersion == 5 {
		minimum = 3
	}
	if packet.Header != 0x20 || len(packet.Body) < minimum || packet.Body[0]&0xfe != 0 || packet.Body[1] != 0 {
		return capabilities, fmt.Errorf("Azure Web PubSub MQTT broker rejected or malformed CONNACK")
	}
	if plan.CleanStart && packet.Body[0]&0x01 != 0 {
		return capabilities, fmt.Errorf("Azure Web PubSub MQTT broker resumed a session despite clean-session request")
	}
	if plan.ProtocolVersion == 4 && len(packet.Body) != 2 {
		return capabilities, fmt.Errorf("Azure Web PubSub MQTT broker returned malformed MQTT 3.1.1 CONNACK")
	}
	if plan.ProtocolVersion == 5 {
		consumed, numeric, err := validateAzureWebPubSubMQTT5ControlProperties(packet.Body[2:], azureWebPubSubMQTT5ConnAckProperties)
		if err != nil || consumed != len(packet.Body)-2 {
			return capabilities, fmt.Errorf("Azure Web PubSub MQTT broker returned malformed MQTT 5 CONNACK properties")
		}
		if values := numeric[0x21]; len(values) == 1 {
			capabilities.ReceiveMaximum = uint16(values[0])
		}
		if values := numeric[0x27]; len(values) == 1 {
			capabilities.MaximumPacketSize = uint32(values[0])
		}
		if values := numeric[0x24]; len(values) == 1 {
			capabilities.MaximumQoS = byte(values[0])
		}
		if values := numeric[0x13]; len(values) == 1 {
			value := uint16(values[0])
			capabilities.ServerKeepAlive = &value
		}
	}
	return capabilities, nil
}

func validateAzureWebPubSubMQTTSubAck(packet azureWebPubSubMQTTPacket, subscriptions []azureWebPubSubMQTTSubscription) error {
	if packet.Header != 0x90 || len(packet.Body) != len(subscriptions)+2 || binary.BigEndian.Uint16(packet.Body[:2]) != azureWebPubSubMQTTSubscribeID {
		return fmt.Errorf("Azure Web PubSub MQTT broker returned a malformed SUBACK")
	}
	for index, granted := range packet.Body[2:] {
		if granted == 0x80 || granted > 1 || int(granted) > subscriptions[index].QoS {
			return fmt.Errorf("Azure Web PubSub MQTT broker rejected or elevated a subscription")
		}
	}
	return nil
}

func validateAzureWebPubSubMQTTSubAckForPlan(packet azureWebPubSubMQTTPacket, plan azureWebPubSubMQTTPlan) error {
	if plan.ProtocolVersion == 4 {
		return validateAzureWebPubSubMQTTSubAck(packet, plan.Subscriptions)
	}
	if packet.Header != 0x90 || len(packet.Body) < 4 || binary.BigEndian.Uint16(packet.Body[:2]) != azureWebPubSubMQTTSubscribeID {
		return fmt.Errorf("Azure Web PubSub MQTT broker returned a malformed MQTT 5 SUBACK")
	}
	propertyBytes, _, err := validateAzureWebPubSubMQTT5ControlProperties(packet.Body[2:], azureWebPubSubMQTT5ReasonProperties)
	if err != nil {
		return fmt.Errorf("Azure Web PubSub MQTT broker returned malformed SUBACK properties")
	}
	reasons := packet.Body[2+propertyBytes:]
	if len(reasons) != len(plan.Subscriptions) {
		return fmt.Errorf("Azure Web PubSub MQTT SUBACK reason count does not match subscriptions")
	}
	for index, reason := range reasons {
		if reason > 2 || int(reason) > plan.Subscriptions[index].QoS {
			return fmt.Errorf("Azure Web PubSub MQTT broker rejected or elevated a subscription")
		}
	}
	return nil
}

func decodeMQTTVariableByteInteger(data []byte) (int, int, error) {
	value, multiplier := 0, 1
	for index := 0; index < 4; index++ {
		if index >= len(data) {
			return 0, 0, fmt.Errorf("incomplete MQTT variable byte integer")
		}
		current := data[index]
		value += int(current&0x7f) * multiplier
		if current&0x80 == 0 {
			if index > 0 && current == 0 {
				return 0, 0, fmt.Errorf("non-minimal MQTT variable byte integer")
			}
			return value, index + 1, nil
		}
		multiplier *= 128
	}
	return 0, 0, fmt.Errorf("malformed MQTT variable byte integer")
}

func validateAzureWebPubSubMQTT5ControlProperties(data []byte, allowed map[int]azureWebPubSubMQTT5PropertyKind) (int, map[int][]uint64, error) {
	propertyBytes, prefix, err := decodeMQTTVariableByteInteger(data)
	if err != nil || prefix+propertyBytes > len(data) {
		return 0, nil, fmt.Errorf("invalid MQTT 5 property block")
	}
	properties := data[prefix : prefix+propertyBytes]
	seen := make(map[int]bool)
	numeric := make(map[int][]uint64)
	for len(properties) > 0 {
		identifier, consumed, err := decodeMQTTVariableByteInteger(properties)
		if err != nil {
			return 0, nil, fmt.Errorf("invalid MQTT 5 property identifier")
		}
		properties = properties[consumed:]
		kind, ok := allowed[identifier]
		if !ok || kind != azureWebPubSubMQTT5PropertyUTF8Pair && seen[identifier] {
			return 0, nil, fmt.Errorf("unsupported or duplicate MQTT 5 property")
		}
		seen[identifier] = true
		switch kind {
		case azureWebPubSubMQTT5PropertyByte:
			if len(properties) < 1 || properties[0] > 1 {
				return 0, nil, fmt.Errorf("invalid MQTT 5 byte property")
			}
			numeric[identifier] = append(numeric[identifier], uint64(properties[0]))
			properties = properties[1:]
		case azureWebPubSubMQTT5PropertyUint16, azureWebPubSubMQTT5PropertyNonzeroUint16:
			if len(properties) < 2 || kind == azureWebPubSubMQTT5PropertyNonzeroUint16 && binary.BigEndian.Uint16(properties[:2]) == 0 {
				return 0, nil, fmt.Errorf("invalid MQTT 5 uint16 property")
			}
			numeric[identifier] = append(numeric[identifier], uint64(binary.BigEndian.Uint16(properties[:2])))
			properties = properties[2:]
		case azureWebPubSubMQTT5PropertyUint32, azureWebPubSubMQTT5PropertyNonzeroUint32:
			if len(properties) < 4 || kind == azureWebPubSubMQTT5PropertyNonzeroUint32 && binary.BigEndian.Uint32(properties[:4]) == 0 {
				return 0, nil, fmt.Errorf("invalid MQTT 5 uint32 property")
			}
			numeric[identifier] = append(numeric[identifier], uint64(binary.BigEndian.Uint32(properties[:4])))
			properties = properties[4:]
		case azureWebPubSubMQTT5PropertyUTF8:
			_, consumed, err := readMQTTUTF8(properties)
			if err != nil {
				return 0, nil, err
			}
			properties = properties[consumed:]
		case azureWebPubSubMQTT5PropertyBinary:
			_, consumed, err := readMQTTBinary(properties)
			if err != nil {
				return 0, nil, err
			}
			properties = properties[consumed:]
		case azureWebPubSubMQTT5PropertyUTF8Pair:
			_, first, err := readMQTTUTF8(properties)
			if err != nil {
				return 0, nil, err
			}
			_, second, err := readMQTTUTF8(properties[first:])
			if err != nil {
				return 0, nil, err
			}
			properties = properties[first+second:]
		default:
			return 0, nil, fmt.Errorf("unknown MQTT 5 property type")
		}
	}
	return prefix + propertyBytes, numeric, nil
}

func processAzureWebPubSubMQTTPublish(ctx context.Context, connection cloudWebSocketConnection, sink *cloudWebSocketOutputSink, packet azureWebPubSubMQTTPacket) error {
	delivered, err := acceptAzureWebPubSubMQTTPublish(ctx, connection, sink, azureWebPubSubMQTTPlan{ProtocolVersion: 4}, packet, make(map[uint16][]byte))
	if err != nil {
		return err
	}
	if !delivered {
		return fmt.Errorf("Azure Web PubSub MQTT 3.1.1 helper does not complete QoS 2 without PUBREL")
	}
	return nil
}

func acceptAzureWebPubSubMQTTPublish(ctx context.Context, connection cloudWebSocketConnection, sink *cloudWebSocketOutputSink, plan azureWebPubSubMQTTPlan, packet azureWebPubSubMQTTPacket, incomingQoS2 map[uint16][]byte, completed ...map[uint16]bool) (bool, error) {
	message, qos, packetID, err := decodeAzureWebPubSubMQTTPublish(plan, packet)
	if err != nil {
		return false, err
	}
	if qos == 2 {
		alreadyCompleted := len(completed) > 0 && completed[0][packetID]
		if _, exists := incomingQoS2[packetID]; !exists && !alreadyCompleted {
			incomingQoS2[packetID] = message
		}
		if err := connection.Write(ctx, cloudWebSocketMessageBinary, encodeAzureWebPubSubMQTTAcknowledgement(5, packetID, plan.ProtocolVersion)); err != nil {
			return false, fmt.Errorf("acknowledge Azure Web PubSub MQTT QoS 2 PUBLISH")
		}
		return false, nil
	}
	if err := sink.writeMessage(message); err != nil {
		return false, err
	}
	if qos == 1 {
		if err := connection.Write(ctx, cloudWebSocketMessageBinary, encodeAzureWebPubSubMQTTAcknowledgement(4, packetID, plan.ProtocolVersion)); err != nil {
			return false, fmt.Errorf("acknowledge Azure Web PubSub MQTT message")
		}
	}
	return true, nil
}

func decodeAzureWebPubSubMQTTPublish(plan azureWebPubSubMQTTPlan, packet azureWebPubSubMQTTPacket) ([]byte, byte, uint16, error) {
	if packet.Header>>4 != 3 || packet.Header&0x01 != 0 {
		return nil, 0, 0, fmt.Errorf("Azure Web PubSub MQTT expected a non-retained PUBLISH packet")
	}
	qos := (packet.Header >> 1) & 0x03
	if qos > 2 || len(packet.Body) < 2 {
		return nil, 0, 0, fmt.Errorf("Azure Web PubSub MQTT PUBLISH packet has invalid QoS or topic")
	}
	topicLength := int(binary.BigEndian.Uint16(packet.Body[:2]))
	offset := 2 + topicLength
	if topicLength == 0 || topicLength > 1024 || offset > len(packet.Body) || !validAzureWebPubSubMQTTUTF8(packet.Body[2:offset]) || strings.ContainsAny(string(packet.Body[2:offset]), "#+") {
		return nil, 0, 0, fmt.Errorf("Azure Web PubSub MQTT PUBLISH packet has an invalid topic")
	}
	packetID := uint16(0)
	if qos > 0 {
		if offset+2 > len(packet.Body) {
			return nil, 0, 0, fmt.Errorf("Azure Web PubSub MQTT QoS PUBLISH omitted packet ID")
		}
		packetID = binary.BigEndian.Uint16(packet.Body[offset : offset+2])
		if packetID == 0 {
			return nil, 0, 0, fmt.Errorf("Azure Web PubSub MQTT QoS PUBLISH used packet ID zero")
		}
		offset += 2
	}
	metadata := make(map[string]any)
	if plan.ProtocolVersion == 5 {
		consumed, properties, err := parseAzureWebPubSubMQTT5PublishProperties(packet.Body[offset:])
		if err != nil {
			return nil, 0, 0, err
		}
		offset += consumed
		metadata = properties
	}
	payload := packet.Body[offset:]
	if format, ok := metadata["payload_format"].(int); ok && format == 1 && !utf8.Valid(payload) {
		return nil, 0, 0, fmt.Errorf("Azure Web PubSub MQTT PUBLISH declared invalid UTF-8 payload")
	}
	metadata["topic"] = string(packet.Body[2 : 2+topicLength])
	metadata["qos"] = qos
	metadata["duplicate"] = packet.Header&0x08 != 0
	metadata["retained"] = false
	metadata["packet_id"] = packetID
	metadata["bytes"] = len(payload)
	metadata["payload_base64"] = base64.StdEncoding.EncodeToString(payload)
	message, err := json.Marshal(metadata)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("encode Azure Web PubSub MQTT message")
	}
	return message, qos, packetID, nil
}

func parseAzureWebPubSubMQTT5PublishProperties(data []byte) (int, map[string]any, error) {
	propertyBytes, prefix, err := decodeMQTTVariableByteInteger(data)
	if err != nil || prefix+propertyBytes > len(data) {
		return 0, nil, fmt.Errorf("Azure Web PubSub MQTT returned malformed PUBLISH properties")
	}
	properties := data[prefix : prefix+propertyBytes]
	metadata := make(map[string]any)
	var subscriptionIDs []int
	seen := make(map[byte]bool)
	for len(properties) > 0 {
		identifier := properties[0]
		properties = properties[1:]
		if identifier != 0x0b && identifier != 0x26 && seen[identifier] {
			return 0, nil, fmt.Errorf("Azure Web PubSub MQTT returned a duplicate PUBLISH property")
		}
		seen[identifier] = true
		switch identifier {
		case 0x01:
			if len(properties) < 1 || properties[0] > 1 {
				return 0, nil, fmt.Errorf("Azure Web PubSub MQTT returned invalid payload format")
			}
			metadata["payload_format"] = int(properties[0])
			properties = properties[1:]
		case 0x02:
			if len(properties) < 4 {
				return 0, nil, fmt.Errorf("Azure Web PubSub MQTT returned invalid message expiry")
			}
			metadata["message_expiry_seconds"] = binary.BigEndian.Uint32(properties[:4])
			properties = properties[4:]
		case 0x03:
			value, consumed, err := readMQTTUTF8(properties)
			if err != nil {
				return 0, nil, fmt.Errorf("Azure Web PubSub MQTT returned invalid content type")
			}
			metadata["content_type"] = value
			properties = properties[consumed:]
		case 0x08:
			_, consumed, err := readMQTTUTF8(properties)
			if err != nil {
				return 0, nil, fmt.Errorf("Azure Web PubSub MQTT returned invalid response topic")
			}
			properties = properties[consumed:]
		case 0x09:
			_, consumed, err := readMQTTBinary(properties)
			if err != nil {
				return 0, nil, fmt.Errorf("Azure Web PubSub MQTT returned invalid correlation data")
			}
			properties = properties[consumed:]
		case 0x0b:
			value, consumed, err := decodeMQTTVariableByteInteger(properties)
			if err != nil || value == 0 {
				return 0, nil, fmt.Errorf("Azure Web PubSub MQTT returned invalid subscription identifier")
			}
			subscriptionIDs = append(subscriptionIDs, value)
			properties = properties[consumed:]
		case 0x23:
			return 0, nil, fmt.Errorf("Azure Web PubSub MQTT returned unsupported topic alias")
		case 0x26:
			_, first, err := readMQTTUTF8(properties)
			if err != nil {
				return 0, nil, fmt.Errorf("Azure Web PubSub MQTT returned invalid user property")
			}
			_, second, err := readMQTTUTF8(properties[first:])
			if err != nil {
				return 0, nil, fmt.Errorf("Azure Web PubSub MQTT returned invalid user property")
			}
			properties = properties[first+second:]
		default:
			return 0, nil, fmt.Errorf("Azure Web PubSub MQTT returned unsupported PUBLISH property")
		}
	}
	if len(subscriptionIDs) > 0 {
		metadata["subscription_ids"] = subscriptionIDs
	}
	return prefix + propertyBytes, metadata, nil
}

func readMQTTUTF8(data []byte) (string, int, error) {
	value, consumed, err := readMQTTBinary(data)
	if err != nil || !validAzureWebPubSubMQTTUTF8(value) {
		return "", 0, fmt.Errorf("invalid MQTT UTF-8 string")
	}
	return string(value), consumed, nil
}

func validAzureWebPubSubMQTTUTF8(value []byte) bool {
	if !utf8.Valid(value) {
		return false
	}
	for _, character := range string(value) {
		if character == 0 || unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func readMQTTBinary(data []byte) ([]byte, int, error) {
	if len(data) < 2 {
		return nil, 0, fmt.Errorf("incomplete MQTT binary data")
	}
	length := int(binary.BigEndian.Uint16(data[:2]))
	if 2+length > len(data) {
		return nil, 0, fmt.Errorf("incomplete MQTT binary data")
	}
	return data[2 : 2+length], 2 + length, nil
}

func encodeAzureWebPubSubMQTTAcknowledgement(packetType byte, packetID uint16, protocolVersion int) []byte {
	header := packetType << 4
	if packetType == 6 {
		header = 0x62
	}
	_ = protocolVersion
	return []byte{header, 0x02, byte(packetID >> 8), byte(packetID)}
}

func parseAzureWebPubSubMQTTAcknowledgement(packet azureWebPubSubMQTTPacket, packetType byte, protocolVersion int) (uint16, error) {
	expectedHeader := packetType << 4
	if packetType == 6 {
		expectedHeader = 0x62
	}
	if packet.Header != expectedHeader || len(packet.Body) < 2 {
		return 0, fmt.Errorf("invalid MQTT acknowledgement")
	}
	packetID := binary.BigEndian.Uint16(packet.Body[:2])
	if packetID == 0 || protocolVersion == 4 && len(packet.Body) != 2 {
		return 0, fmt.Errorf("invalid MQTT acknowledgement packet ID or length")
	}
	if protocolVersion == 5 && len(packet.Body) > 2 {
		reason := packet.Body[2]
		if reason != 0 && !((packetType == 4 || packetType == 5) && reason == 0x10) {
			return 0, fmt.Errorf("MQTT acknowledgement rejected")
		}
		if len(packet.Body) > 3 {
			consumed, _, err := validateAzureWebPubSubMQTT5ControlProperties(packet.Body[3:], azureWebPubSubMQTT5ReasonProperties)
			if err != nil || consumed != len(packet.Body)-3 {
				return 0, fmt.Errorf("invalid MQTT acknowledgement properties")
			}
		}
	}
	return packetID, nil
}

func validateAzureWebPubSubMQTTDisconnect(packet azureWebPubSubMQTTPacket, protocolVersion int) error {
	if packet.Header != 0xe0 {
		return fmt.Errorf("invalid MQTT DISCONNECT")
	}
	if protocolVersion == 4 {
		if len(packet.Body) != 0 {
			return fmt.Errorf("invalid MQTT 3.1.1 DISCONNECT")
		}
		return nil
	}
	if len(packet.Body) == 0 {
		return nil
	}
	if packet.Body[0] != 0 {
		return fmt.Errorf("MQTT 5 server disconnect reason indicates failure")
	}
	if len(packet.Body) > 1 {
		consumed, _, err := validateAzureWebPubSubMQTT5ControlProperties(packet.Body[1:], azureWebPubSubMQTT5DisconnectProperties)
		if err != nil || consumed != len(packet.Body)-1 {
			return fmt.Errorf("invalid MQTT 5 DISCONNECT properties")
		}
	}
	return nil
}

func defaultAzureWebPubSubMQTTWebSocketDial(ctx context.Context, target string, headers http.Header) (cloudWebSocketConnection, error) {
	client := &http.Client{Transport: http.DefaultTransport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	connection, response, err := websocket.Dial(ctx, target, &websocket.DialOptions{HTTPClient: client, HTTPHeader: headers.Clone(), Subprotocols: []string{"mqtt"}, CompressionMode: websocket.CompressionDisabled})
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("Azure Web PubSub MQTT WebSocket handshake failed with HTTP %d", response.StatusCode)
		}
		return nil, fmt.Errorf("Azure Web PubSub MQTT WebSocket handshake failed")
	}
	if connection.Subprotocol() != "mqtt" {
		connection.Close(websocket.StatusProtocolError, "missing MQTT subprotocol")
		return nil, fmt.Errorf("Azure Web PubSub did not negotiate the MQTT subprotocol")
	}
	connection.SetReadLimit(azureWebPubSubMQTTMaxPacketBytes)
	return coderTencentWebSocketConnection{connection: connection}, nil
}

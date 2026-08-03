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
)

var baiduIoTCoreIDPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

type baiduIoTCoreMQTTSubscription struct {
	TopicFilter string `json:"topic_filter"`
	QoS         int    `json:"qos"`
}

type baiduIoTCoreMQTTPublish struct {
	Topic         string `json:"topic"`
	PayloadBase64 string `json:"payload_base64"`
	QoS           int    `json:"qos"`
	Retain        bool   `json:"retain,omitempty"`
	payload       []byte
}

type baiduIoTCoreMQTTWill struct {
	Topic         string `json:"topic"`
	PayloadBase64 string `json:"payload_base64"`
	QoS           int    `json:"qos"`
	Retain        bool   `json:"retain,omitempty"`
	payload       []byte
}

type baiduIoTCoreMQTTPlan struct {
	IoTCoreID        string                         `json:"iot_core_id,omitempty"`
	ClientID         string                         `json:"client_id"`
	CleanSession     *bool                          `json:"clean_session,omitempty"`
	KeepAliveSeconds int                            `json:"keep_alive_seconds,omitempty"`
	MaxPayloadBytes  int                            `json:"max_payload_bytes,omitempty"`
	Subscriptions    []baiduIoTCoreMQTTSubscription `json:"subscriptions,omitempty"`
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
	plan := baiduIoTCoreMQTTPlan{KeepAliveSeconds: baiduIoTCoreMQTTDefaultKeepAlive, MaxPayloadBytes: baiduIoTCoreMQTTDefaultPayloadMax}
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
		if subscription.QoS != 0 && subscription.QoS != 1 {
			return baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core MQTT supports subscription QoS 0 or 1 only")
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
	}
	if plan.Will != nil {
		payload, err := validateBaiduIoTCoreMQTTPublish(plan.Will.Topic, plan.Will.PayloadBase64, plan.Will.QoS, plan.MaxPayloadBytes)
		if err != nil {
			return baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core MQTT Will is invalid: %w", err)
		}
		plan.Will.payload = payload
	}
	if mutating && len(plan.Subscriptions) == 0 && len(plan.Publishes) == 0 && plan.Will == nil {
		return baiduIoTCoreMQTTPlan{}, fmt.Errorf("Baidu IoT Core ClientMQTT requires a subscription, publish, or Will plan")
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
	if qos != 0 && qos != 1 {
		return nil, fmt.Errorf("Baidu IoT Core MQTT supports publish QoS 0 or 1 only")
	}
	payload, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(payload) > maxPayloadBytes {
		return nil, fmt.Errorf("Baidu IoT Core MQTT payload_base64 must decode to at most %d bytes", maxPayloadBytes)
	}
	return payload, nil
}

func validateBaiduIoTCoreMQTTTopic(topic string, filter bool) error {
	if !validAWSIoTMQTTString(topic, baiduIoTCoreMQTTMaxTopicBytes) {
		return fmt.Errorf("Baidu IoT Core MQTT topic must be bounded UTF-8")
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
		return "", "", fmt.Errorf("Baidu IoT Core MQTT requires a valid instance and complete BCE IAM AK/SK")
	}
	if credentials.SessionToken != "" {
		return "", "", fmt.Errorf("Baidu IoT Core MQTT application permission does not document a BCE session-token field")
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
	body := []byte{0x00, 0x04, 'M', 'Q', 'T', 'T', 0x04, flags, byte(plan.KeepAliveSeconds >> 8), byte(plan.KeepAliveSeconds)}
	body = appendMQTTUTF8(body, plan.ClientID)
	if plan.Will != nil {
		body = appendMQTTUTF8(body, plan.Will.Topic)
		body = appendMQTTBinary(body, plan.Will.payload)
	}
	body = appendMQTTUTF8(body, username)
	body = appendMQTTUTF8(body, password)
	return encodeAWSMQTTPacket(0x10, body)
}

func encodeBaiduIoTCoreMQTTSubscribe(subscriptions []baiduIoTCoreMQTTSubscription, packetID uint16) ([]byte, error) {
	body := []byte{byte(packetID >> 8), byte(packetID)}
	for _, subscription := range subscriptions {
		body = appendMQTTUTF8(body, subscription.TopicFilter)
		body = append(body, byte(subscription.QoS))
	}
	return encodeAWSMQTTPacket(0x82, body)
}

func validateBaiduIoTCoreMQTTSubAck(packet awsMQTTPacket, packetID uint16, subscriptions []baiduIoTCoreMQTTSubscription) error {
	if packet.Header != 0x90 || len(packet.Body) != len(subscriptions)+2 || binary.BigEndian.Uint16(packet.Body[:2]) != packetID {
		return fmt.Errorf("Baidu IoT Core MQTT returned a malformed SUBACK")
	}
	for index, grantedQoS := range packet.Body[2:] {
		if grantedQoS > 1 || int(grantedQoS) > subscriptions[index].QoS {
			return fmt.Errorf("Baidu IoT Core MQTT rejected or elevated a subscription")
		}
	}
	return nil
}

func validateBaiduIoTCoreMQTTInboundPayload(packet awsMQTTPacket, maxPayloadBytes int) error {
	if packet.Header>>4 != 3 || len(packet.Body) < 2 {
		return fmt.Errorf("Baidu IoT Core MQTT returned a malformed PUBLISH")
	}
	qos := (packet.Header >> 1) & 0x03
	topicBytes := int(binary.BigEndian.Uint16(packet.Body[:2]))
	offset := 2 + topicBytes
	if qos > 1 || topicBytes < 1 || offset > len(packet.Body) {
		return fmt.Errorf("Baidu IoT Core MQTT returned a malformed PUBLISH")
	}
	if qos == 1 {
		offset += 2
		if offset > len(packet.Body) {
			return fmt.Errorf("Baidu IoT Core MQTT returned a malformed PUBLISH")
		}
	}
	if len(packet.Body)-offset > maxPayloadBytes {
		return fmt.Errorf("Baidu IoT Core MQTT payload exceeds the declared instance limit")
	}
	return nil
}

func encodeBaiduIoTCoreMQTTPublish(publish baiduIoTCoreMQTTPublish, packetID uint16) ([]byte, error) {
	body := appendMQTTUTF8(nil, publish.Topic)
	if publish.QoS == 1 {
		body = append(body, byte(packetID>>8), byte(packetID))
	}
	body = append(body, publish.payload...)
	header := byte(0x30 | publish.QoS<<1)
	if publish.Retain {
		header |= 0x01
	}
	return encodeAWSMQTTPacket(header, body)
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
	reader := &awsMQTTPacketReader{connection: connection}
	packet, err := reader.Read(handshakeCtx)
	if err != nil || validateAWSIoTMQTTConnAck(packet) != nil {
		return InvocationResult{}, fmt.Errorf("Baidu IoT Core MQTT connection was not acknowledged")
	}
	pendingSubscribes := make(map[uint16][]baiduIoTCoreMQTTSubscription)
	nextPacketID := uint16(baiduIoTCoreMQTTSubscribePacketID)
	for offset := 0; offset < len(plan.Subscriptions); offset += baiduIoTCoreMQTTSubscribeBatchSize {
		end := min(offset+baiduIoTCoreMQTTSubscribeBatchSize, len(plan.Subscriptions))
		batch := plan.Subscriptions[offset:end]
		subscribePacket, _ := encodeBaiduIoTCoreMQTTSubscribe(batch, nextPacketID)
		if err := connection.Write(handshakeCtx, cloudWebSocketMessageBinary, subscribePacket); err != nil {
			return InvocationResult{}, fmt.Errorf("send Baidu IoT Core MQTT SUBSCRIBE")
		}
		pendingSubscribes[nextPacketID] = batch
		nextPacketID++
	}
	pendingPublishes := make(map[uint16]struct{})
	packetID := max(nextPacketID, uint16(2))
	for index, publish := range plan.Publishes {
		if index > 0 {
			interval := baiduIoTCoreMQTTQoS0PublishInterval
			if publish.QoS == 1 {
				interval = baiduIoTCoreMQTTQoS1PublishInterval
			}
			if err := adapter.config.StreamPause(handshakeCtx, interval); err != nil {
				return InvocationResult{}, fmt.Errorf("pace Baidu IoT Core MQTT PUBLISH")
			}
		}
		encoded, _ := encodeBaiduIoTCoreMQTTPublish(publish, packetID)
		if err := connection.Write(handshakeCtx, cloudWebSocketMessageBinary, encoded); err != nil {
			return InvocationResult{}, fmt.Errorf("send Baidu IoT Core MQTT PUBLISH")
		}
		if publish.QoS == 1 {
			pendingPublishes[packetID] = struct{}{}
			packetID++
		}
	}
	decodeConfig := awsIoTMQTTSubscribeConfig{ProtocolVersion: 4, ClientID: plan.ClientID, CleanStart: plan.cleanSession, MaxMessages: plan.MaxMessages, TimeoutSeconds: plan.TimeoutSeconds}
	for _, subscription := range plan.Subscriptions {
		decodeConfig.Subscriptions = append(decodeConfig.Subscriptions, awsIoTMQTTSubscription{TopicFilter: subscription.TopicFilter, QoS: subscription.QoS})
	}
	received := 0
	for len(pendingSubscribes) > 0 || len(pendingPublishes) > 0 {
		packet, err = reader.Read(handshakeCtx)
		if err != nil {
			return InvocationResult{}, fmt.Errorf("read Baidu IoT Core MQTT acknowledgement")
		}
		switch packet.Header >> 4 {
		case 3:
			if received >= plan.MaxMessages || len(plan.Subscriptions) == 0 {
				return InvocationResult{}, fmt.Errorf("Baidu IoT Core MQTT returned an unexpected message before acknowledgement")
			}
			if err := validateBaiduIoTCoreMQTTInboundPayload(packet, plan.MaxPayloadBytes); err != nil {
				return InvocationResult{}, err
			}
			if err := processAWSIoTMQTTPublishForConfig(handshakeCtx, connection, sink, decodeConfig, packet); err != nil {
				return InvocationResult{}, fmt.Errorf("Baidu IoT Core MQTT returned an invalid publish")
			}
			received++
		case 9:
			if len(packet.Body) < 2 {
				return InvocationResult{}, fmt.Errorf("Baidu IoT Core MQTT returned a malformed SUBACK")
			}
			ackID := binary.BigEndian.Uint16(packet.Body[:2])
			subscriptions, ok := pendingSubscribes[ackID]
			if !ok || validateBaiduIoTCoreMQTTSubAck(packet, ackID, subscriptions) != nil {
				return InvocationResult{}, fmt.Errorf("Baidu IoT Core MQTT returned a malformed SUBACK")
			}
			delete(pendingSubscribes, ackID)
		case 4:
			if packet.Header != 0x40 || len(packet.Body) != 2 {
				return InvocationResult{}, fmt.Errorf("Baidu IoT Core MQTT returned a malformed PUBACK")
			}
			ackID := binary.BigEndian.Uint16(packet.Body)
			if _, ok := pendingPublishes[ackID]; !ok {
				return InvocationResult{}, fmt.Errorf("Baidu IoT Core MQTT returned an unknown PUBACK")
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
				if err := validateBaiduIoTCoreMQTTInboundPayload(packet, plan.MaxPayloadBytes); err != nil {
					return InvocationResult{}, err
				}
				if err := processAWSIoTMQTTPublishForConfig(collectionCtx, connection, sink, decodeConfig, packet); err != nil {
					return InvocationResult{}, fmt.Errorf("Baidu IoT Core MQTT returned an invalid publish")
				}
				received++
			case 13:
				if packet.Header != 0xd0 || len(packet.Body) != 0 {
					return InvocationResult{}, fmt.Errorf("Baidu IoT Core MQTT returned a malformed PINGRESP")
				}
				pingOutstanding = false
			case 14:
				if len(packet.Body) != 0 {
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
	output, _ := json.Marshal(map[string]any{"published": len(plan.Publishes), "received": received})
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
	connection.SetReadLimit(baiduIoTCoreMQTTMaxPayloadBytes + baiduIoTCoreMQTTMaxTopicBytes + 16)
	return coderTencentWebSocketConnection{connection: connection}, nil
}

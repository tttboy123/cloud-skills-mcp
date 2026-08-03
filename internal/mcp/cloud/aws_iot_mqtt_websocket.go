package cloud

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/coder/websocket"
)

const (
	authSchemeAWSIoTMQTTWS         = "iot-mqtt-ws"
	awsIoTMQTTService              = "iotdevicegateway"
	awsIoTMQTTSubscribeOperation   = "subscribemqtt"
	awsIoTMQTTExpires              = "300"
	awsIoTMQTTMaxSubscriptions     = 8
	awsIoTMQTTMaxMessages          = 256
	awsIoTMQTTMaxTopicBytes        = 256
	awsIoTMQTTMaxTopicSlashes      = 7
	awsIoTMQTTMaxClientIDBytes     = 128
	awsIoTMQTTMaxPacketBytes       = 128 * 1024
	awsIoTMQTTMaxTimeoutSeconds    = 300
	awsIoTMQTTSubscribePacketID    = 1
	awsIoTMQTTMaximumKeepAliveSecs = 1200
	awsIoTMQTTMaxSessionExpiry     = 7 * 24 * 60 * 60
)

type awsIoTMQTTSubscription struct {
	TopicFilter string `json:"topic_filter"`
	QoS         int    `json:"qos"`
}

type awsIoTMQTTSubscribeConfig struct {
	ProtocolVersion        int                      `json:"protocol_version,omitempty"`
	ClientID               string                   `json:"client_id"`
	CleanStart             bool                     `json:"clean_start,omitempty"`
	SessionExpirySeconds   uint32                   `json:"session_expiry_seconds,omitempty"`
	SubscriptionIdentifier uint32                   `json:"subscription_identifier,omitempty"`
	Subscriptions          []awsIoTMQTTSubscription `json:"subscriptions"`
	MaxMessages            int                      `json:"max_messages"`
	TimeoutSeconds         int                      `json:"timeout_seconds"`
}

type awsIoTMQTTConnAckCapabilities struct {
	ReceiveMaximum    uint16
	MaximumPacketSize uint32
	ServerKeepAlive   *uint16
}

var awsIoTMQTT5ConnAckProperties = map[int]azureWebPubSubMQTT5PropertyKind{
	0x11: azureWebPubSubMQTT5PropertyUint32,
	0x12: azureWebPubSubMQTT5PropertyUTF8,
	0x13: azureWebPubSubMQTT5PropertyUint16,
	0x1f: azureWebPubSubMQTT5PropertyUTF8,
	0x21: azureWebPubSubMQTT5PropertyNonzeroUint16,
	0x22: azureWebPubSubMQTT5PropertyUint16,
	0x24: azureWebPubSubMQTT5PropertyByte,
	0x25: azureWebPubSubMQTT5PropertyByte,
	0x26: azureWebPubSubMQTT5PropertyUTF8Pair,
	0x27: azureWebPubSubMQTT5PropertyNonzeroUint32,
	0x28: azureWebPubSubMQTT5PropertyByte,
	0x29: azureWebPubSubMQTT5PropertyByte,
	0x2a: azureWebPubSubMQTT5PropertyByte,
}

func validateAWSIoTMQTTWebSocketInvocation(invocation Invocation, allowedEndpointHosts []string) error {
	if !strings.EqualFold(invocation.Service, awsIoTMQTTService) || strings.ToLower(strings.TrimSpace(invocation.Operation)) != awsIoTMQTTSubscribeOperation {
		return fmt.Errorf("AWS IoT MQTT WebSocket requires service iotdevicegateway and operation SubscribeMQTT")
	}
	if !strings.EqualFold(invocation.Method, http.MethodGet) || !identifierPattern.MatchString(invocation.Region) {
		return fmt.Errorf("AWS IoT MQTT WebSocket requires GET and a valid region")
	}
	target, err := url.Parse(invocation.URL)
	if err != nil || !strings.EqualFold(target.Scheme, "wss") || target.Hostname() == "" || target.Port() != "" || target.User != nil || target.Fragment != "" || target.RawQuery != "" || target.EscapedPath() != "/mqtt" {
		return fmt.Errorf("AWS IoT MQTT WebSocket requires an exact wss:// endpoint with path /mqtt and no caller query")
	}
	if !isAWSIoTMQTTEndpoint(target.Hostname(), invocation.Region, allowedEndpointHosts) {
		return fmt.Errorf("AWS IoT MQTT WebSocket host does not match the requested region or endpoint allowlist")
	}
	if len(invocation.Parameters) != 0 || len(invocation.Headers) != 0 || invocation.BodyFile != "" || invocation.ResponseFile == "" {
		return fmt.Errorf("AWS IoT MQTT WebSocket requires only a protocol body and response_file; query, headers, and body_file are forbidden")
	}
	if invocation.RegionSet != "" || invocation.Project != "" || invocation.Subscription != "" || invocation.Audience != "" || invocation.AuthVersion != "" || invocation.APIVersion != "" || invocation.PayloadMode != "" || invocation.ChecksumAlgorithm != "" || invocation.StreamChunkBytes != 0 || invocation.StreamIntervalMS != 0 || invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return fmt.Errorf("AWS IoT MQTT WebSocket does not accept cross-provider, REST payload, or generic stream controls")
	}
	_, err = parseAWSIoTMQTTSubscribeConfig(invocation.Body)
	return err
}

func isAWSIoTMQTTEndpoint(host, region string, allowedEndpointHosts []string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	region = strings.ToLower(strings.TrimSpace(region))
	for _, suffix := range []string{
		".iot." + region + ".amazonaws.com",
		".iot-fips." + region + ".amazonaws.com",
		".iot." + region + ".amazonaws.com.cn",
		".iot." + region + ".api.aws",
	} {
		if prefix := strings.TrimSuffix(host, suffix); prefix != host && prefix != "" && validAdditionalEndpointHost(host) {
			return true
		}
	}
	for _, candidate := range allowedEndpointHosts {
		if validAdditionalEndpointHost(candidate) && host == strings.ToLower(strings.TrimSpace(candidate)) {
			return true
		}
	}
	return false
}

func parseAWSIoTMQTTSubscribeConfig(body any) (awsIoTMQTTSubscribeConfig, error) {
	if body == nil || alibabaNLSContainsCredentialField(body) {
		return awsIoTMQTTSubscribeConfig{}, fmt.Errorf("AWS IoT MQTT WebSocket requires a credential-free protocol body")
	}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) == 0 || len(encoded) > maxRequestPayloadBytes {
		return awsIoTMQTTSubscribeConfig{}, fmt.Errorf("AWS IoT MQTT WebSocket body must be bounded JSON")
	}
	config := awsIoTMQTTSubscribeConfig{ProtocolVersion: 4, CleanStart: true}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return awsIoTMQTTSubscribeConfig{}, fmt.Errorf("AWS IoT MQTT WebSocket body does not match the subscription schema")
	}
	if err := ensureJSONDecoderEOF(decoder); err != nil {
		return awsIoTMQTTSubscribeConfig{}, fmt.Errorf("AWS IoT MQTT WebSocket body must contain one JSON object")
	}
	if !validAWSIoTMQTTString(config.ClientID, awsIoTMQTTMaxClientIDBytes) {
		return awsIoTMQTTSubscribeConfig{}, fmt.Errorf("AWS IoT MQTT client_id must be one bounded UTF-8 identifier")
	}
	if config.ProtocolVersion != 4 && config.ProtocolVersion != 5 {
		return awsIoTMQTTSubscribeConfig{}, fmt.Errorf("AWS IoT MQTT protocol_version must be 4 or 5")
	}
	if config.SessionExpirySeconds > awsIoTMQTTMaxSessionExpiry {
		return awsIoTMQTTSubscribeConfig{}, fmt.Errorf("AWS IoT MQTT session_expiry_seconds must be at most %d", awsIoTMQTTMaxSessionExpiry)
	}
	if config.ProtocolVersion == 4 && config.SessionExpirySeconds != 0 {
		return awsIoTMQTTSubscribeConfig{}, fmt.Errorf("AWS IoT MQTT session expiry requires MQTT 5")
	}
	if !config.CleanStart || config.SessionExpirySeconds != 0 {
		return awsIoTMQTTSubscribeConfig{}, fmt.Errorf("AWS IoT MQTT read subscription requires clean_start and a zero session expiry")
	}
	if config.SubscriptionIdentifier != 0 {
		return awsIoTMQTTSubscribeConfig{}, fmt.Errorf("AWS IoT MQTT does not support subscription identifiers")
	}
	if len(config.Subscriptions) == 0 || len(config.Subscriptions) > awsIoTMQTTMaxSubscriptions {
		return awsIoTMQTTSubscribeConfig{}, fmt.Errorf("AWS IoT MQTT requires between 1 and %d subscriptions", awsIoTMQTTMaxSubscriptions)
	}
	for _, subscription := range config.Subscriptions {
		if err := validateAWSIoTMQTTTopicFilter(subscription.TopicFilter); err != nil {
			return awsIoTMQTTSubscribeConfig{}, err
		}
		if subscription.QoS != 0 && subscription.QoS != 1 {
			return awsIoTMQTTSubscribeConfig{}, fmt.Errorf("AWS IoT MQTT subscription QoS must be 0 or 1")
		}
	}
	if config.MaxMessages < 1 || config.MaxMessages > awsIoTMQTTMaxMessages {
		return awsIoTMQTTSubscribeConfig{}, fmt.Errorf("AWS IoT MQTT max_messages must be between 1 and %d", awsIoTMQTTMaxMessages)
	}
	if config.TimeoutSeconds < 1 || config.TimeoutSeconds > awsIoTMQTTMaxTimeoutSeconds {
		return awsIoTMQTTSubscribeConfig{}, fmt.Errorf("AWS IoT MQTT timeout_seconds must be between 1 and %d", awsIoTMQTTMaxTimeoutSeconds)
	}
	return config, nil
}

func ensureJSONDecoderEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return fmt.Errorf("trailing JSON value")
}

func validAWSIoTMQTTString(value string, maxBytes int) bool {
	if value == "" || len(value) > maxBytes || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character == 0 || unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validateAWSIoTMQTTTopicFilter(filter string) error {
	if !validAWSIoTMQTTString(filter, awsIoTMQTTMaxTopicBytes) || strings.Count(filter, "/") > awsIoTMQTTMaxTopicSlashes {
		return fmt.Errorf("AWS IoT MQTT topic_filter must be bounded UTF-8 with at most %d slashes", awsIoTMQTTMaxTopicSlashes)
	}
	segments := strings.Split(filter, "/")
	for index, segment := range segments {
		if strings.Contains(segment, "#") && (segment != "#" || index != len(segments)-1) {
			return fmt.Errorf("AWS IoT MQTT # wildcard must be the final complete topic level")
		}
		if strings.Contains(segment, "+") && segment != "+" {
			return fmt.Errorf("AWS IoT MQTT + wildcard must occupy a complete topic level")
		}
	}
	return nil
}

func signAWSIoTMQTTWebSocketURL(rawURL string, credentials AWSCredentials, region string, signingTime time.Time) (string, error) {
	if credentials.AccessKeyID == "" || credentials.SecretAccessKey == "" {
		return "", fmt.Errorf("AWS IoT MQTT WebSocket requires complete AKSK credentials")
	}
	target, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(target.Scheme, "wss") || target.Hostname() == "" || target.RawQuery != "" || target.EscapedPath() != "/mqtt" {
		return "", fmt.Errorf("parse AWS IoT MQTT WebSocket URL")
	}
	now := signingTime.UTC()
	amzDate := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	region = strings.ToLower(strings.TrimSpace(region))
	scope := date + "/" + region + "/" + awsIoTMQTTService + "/aws4_request"
	query := make(url.Values)
	query.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	query.Set("X-Amz-Credential", credentials.AccessKeyID+"/"+scope)
	query.Set("X-Amz-Date", amzDate)
	query.Set("X-Amz-Expires", awsIoTMQTTExpires)
	query.Set("X-Amz-SignedHeaders", "host")
	canonicalQuery := query.Encode()
	host := strings.ToLower(target.Host)
	canonicalRequest := strings.Join([]string{
		http.MethodGet,
		"/mqtt",
		canonicalQuery,
		"host:" + host + "\n",
		"host",
		sha256Hex(nil),
	}, "\n")
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")
	signingKey := deriveAWSSigV4SigningKey(credentials.SecretAccessKey, region, awsIoTMQTTService, now)
	query.Set("X-Amz-Signature", hex.EncodeToString(hmacBytes(sha256.New, signingKey, []byte(stringToSign))))
	// AWS IoT is intentionally different from ordinary SigV4 presigning: the
	// temporary session token is appended only after the canonical signature.
	if credentials.SessionToken != "" {
		query.Set("X-Amz-Security-Token", credentials.SessionToken)
	}
	target.RawQuery = query.Encode()
	return target.String(), nil
}

type awsMQTTPacket struct {
	Header byte
	Body   []byte
}

type awsMQTTPacketReader struct {
	connection cloudWebSocketConnection
	buffer     []byte
}

func (reader *awsMQTTPacketReader) Read(ctx context.Context) (awsMQTTPacket, error) {
	for {
		packet, consumed, complete, err := decodeAWSMQTTPacket(reader.buffer)
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
			return awsMQTTPacket{}, fmt.Errorf("AWS IoT MQTT WebSocket returned a non-binary frame")
		}
		if len(reader.buffer)+len(data) > awsIoTMQTTMaxPacketBytes {
			return awsMQTTPacket{}, fmt.Errorf("AWS IoT MQTT packet exceeds %d bytes", awsIoTMQTTMaxPacketBytes)
		}
		reader.buffer = append(reader.buffer, data...)
	}
}

func decodeAWSMQTTPacket(data []byte) (awsMQTTPacket, int, bool, error) {
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
		if remaining > awsIoTMQTTMaxPacketBytes {
			return awsMQTTPacket{}, 0, false, fmt.Errorf("AWS IoT MQTT packet exceeds %d bytes", awsIoTMQTTMaxPacketBytes)
		}
		if value&0x80 == 0 {
			total := index + remaining
			if total > awsIoTMQTTMaxPacketBytes {
				return awsMQTTPacket{}, 0, false, fmt.Errorf("AWS IoT MQTT packet exceeds %d bytes", awsIoTMQTTMaxPacketBytes)
			}
			if len(data) < total {
				return awsMQTTPacket{}, 0, false, nil
			}
			return awsMQTTPacket{Header: data[0], Body: append([]byte(nil), data[index:total]...)}, total, true, nil
		}
		multiplier *= 128
	}
	return awsMQTTPacket{}, 0, false, fmt.Errorf("AWS IoT MQTT remaining length is malformed")
}

func encodeAWSMQTTPacket(header byte, body []byte) ([]byte, error) {
	if len(body) > awsIoTMQTTMaxPacketBytes {
		return nil, fmt.Errorf("AWS IoT MQTT packet exceeds %d bytes", awsIoTMQTTMaxPacketBytes)
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
	if len(encoded)+len(body) > awsIoTMQTTMaxPacketBytes {
		return nil, fmt.Errorf("AWS IoT MQTT packet exceeds %d bytes", awsIoTMQTTMaxPacketBytes)
	}
	return append(encoded, body...), nil
}

func encodeAWSIoTMQTTConnect(config awsIoTMQTTSubscribeConfig) ([]byte, error) {
	keepAlive := config.TimeoutSeconds + 30
	if keepAlive > awsIoTMQTTMaximumKeepAliveSecs {
		keepAlive = awsIoTMQTTMaximumKeepAliveSecs
	}
	flags := byte(0)
	if config.CleanStart {
		flags = 0x02
	}
	body := []byte{0x00, 0x04, 'M', 'Q', 'T', 'T', byte(config.ProtocolVersion), flags, byte(keepAlive >> 8), byte(keepAlive)}
	if config.ProtocolVersion == 5 {
		properties := make([]byte, 0, 16)
		if config.SessionExpirySeconds > 0 {
			properties = append(properties, 0x11)
			properties = appendMQTTUint32(properties, config.SessionExpirySeconds)
		}
		properties = append(properties, 0x21, byte(config.MaxMessages>>8), byte(config.MaxMessages))
		properties = append(properties, 0x27)
		properties = appendMQTTUint32(properties, awsIoTMQTTMaxPacketBytes)
		properties = append(properties, 0x17, 0x00)
		body = appendMQTTVariableByteInteger(body, len(properties))
		body = append(body, properties...)
	}
	body = appendMQTTUTF8(body, config.ClientID)
	return encodeAWSMQTTPacket(0x10, body)
}

func encodeAWSIoTMQTTSubscribe(config awsIoTMQTTSubscribeConfig) ([]byte, error) {
	body := []byte{byte(awsIoTMQTTSubscribePacketID >> 8), byte(awsIoTMQTTSubscribePacketID)}
	if config.ProtocolVersion == 5 {
		body = append(body, 0x00)
	}
	for _, subscription := range config.Subscriptions {
		body = appendMQTTUTF8(body, subscription.TopicFilter)
		body = append(body, byte(subscription.QoS))
	}
	return encodeAWSMQTTPacket(0x82, body)
}

func appendMQTTUTF8(target []byte, value string) []byte {
	return append(append(target, byte(len(value)>>8), byte(len(value))), value...)
}

func validateAWSIoTMQTTConnAck(packet awsMQTTPacket) error {
	if packet.Header != 0x20 || len(packet.Body) != 2 || packet.Body[0]&0xfe != 0 || packet.Body[1] != 0 {
		return fmt.Errorf("AWS IoT MQTT broker rejected or malformed CONNACK")
	}
	if packet.Body[0]&0x01 != 0 {
		return fmt.Errorf("AWS IoT MQTT broker resumed a session despite clean-session request")
	}
	return nil
}

func parseAWSIoTMQTTConnAck(packet awsMQTTPacket, config awsIoTMQTTSubscribeConfig) (awsIoTMQTTConnAckCapabilities, error) {
	capabilities := awsIoTMQTTConnAckCapabilities{ReceiveMaximum: 65535, MaximumPacketSize: awsIoTMQTTMaxPacketBytes}
	if config.ProtocolVersion == 4 {
		return capabilities, validateAWSIoTMQTTConnAck(packet)
	}
	if packet.Header != 0x20 || len(packet.Body) < 3 || packet.Body[0]&0xfe != 0 || packet.Body[1] != 0 {
		return capabilities, fmt.Errorf("AWS IoT MQTT broker rejected or malformed MQTT 5 CONNACK")
	}
	if config.CleanStart && packet.Body[0]&0x01 != 0 {
		return capabilities, fmt.Errorf("AWS IoT MQTT broker resumed a session despite clean-start request")
	}
	consumed, numeric, err := validateAzureWebPubSubMQTT5ControlProperties(packet.Body[2:], awsIoTMQTT5ConnAckProperties)
	if err != nil || consumed != len(packet.Body)-2 {
		return capabilities, fmt.Errorf("AWS IoT MQTT broker returned malformed MQTT 5 CONNACK properties")
	}
	for _, identifier := range []int{0x24, 0x25, 0x28, 0x29, 0x2a} {
		if values := numeric[identifier]; len(values) == 1 && values[0] > 1 {
			return capabilities, fmt.Errorf("AWS IoT MQTT broker returned an invalid boolean CONNACK property")
		}
	}
	if values := numeric[0x24]; len(values) == 1 && values[0] > 1 {
		return capabilities, fmt.Errorf("AWS IoT MQTT broker advertised unsupported QoS")
	}
	if values := numeric[0x29]; len(values) == 1 && values[0] != 0 {
		return capabilities, fmt.Errorf("AWS IoT MQTT broker advertised unsupported subscription identifiers")
	}
	if values := numeric[0x11]; len(values) == 1 && values[0] > awsIoTMQTTMaxSessionExpiry {
		return capabilities, fmt.Errorf("AWS IoT MQTT broker returned excessive session expiry")
	}
	if values := numeric[0x21]; len(values) == 1 {
		capabilities.ReceiveMaximum = uint16(values[0])
	}
	if values := numeric[0x27]; len(values) == 1 {
		if values[0] > awsIoTMQTTMaxPacketBytes {
			return capabilities, fmt.Errorf("AWS IoT MQTT broker exceeded the AWS packet-size limit")
		}
		capabilities.MaximumPacketSize = uint32(values[0])
	}
	if values := numeric[0x13]; len(values) == 1 {
		value := uint16(values[0])
		capabilities.ServerKeepAlive = &value
	}
	return capabilities, nil
}

func validateAWSIoTMQTTSubAck(packet awsMQTTPacket, subscriptionCount int) error {
	if packet.Header != 0x90 || len(packet.Body) != subscriptionCount+2 || binary.BigEndian.Uint16(packet.Body[:2]) != awsIoTMQTTSubscribePacketID {
		return fmt.Errorf("AWS IoT MQTT broker returned a malformed SUBACK")
	}
	for _, code := range packet.Body[2:] {
		if code != 0 && code != 1 {
			return fmt.Errorf("AWS IoT MQTT broker rejected a subscription")
		}
	}
	return nil
}

func validateAWSIoTMQTTSubAckForSubscriptions(packet awsMQTTPacket, subscriptions []awsIoTMQTTSubscription) error {
	if err := validateAWSIoTMQTTSubAck(packet, len(subscriptions)); err != nil {
		return err
	}
	for index, grantedQoS := range packet.Body[2:] {
		if int(grantedQoS) > subscriptions[index].QoS {
			return fmt.Errorf("AWS IoT MQTT broker granted a higher QoS than requested")
		}
	}
	return nil
}

func validateAWSIoTMQTTSubAckForConfig(packet awsMQTTPacket, config awsIoTMQTTSubscribeConfig) error {
	if config.ProtocolVersion == 4 {
		return validateAWSIoTMQTTSubAckForSubscriptions(packet, config.Subscriptions)
	}
	if packet.Header != 0x90 || len(packet.Body) < 4 || binary.BigEndian.Uint16(packet.Body[:2]) != awsIoTMQTTSubscribePacketID {
		return fmt.Errorf("AWS IoT MQTT broker returned a malformed MQTT 5 SUBACK")
	}
	propertyBytes, _, err := validateAzureWebPubSubMQTT5ControlProperties(packet.Body[2:], azureWebPubSubMQTT5ReasonProperties)
	if err != nil {
		return fmt.Errorf("AWS IoT MQTT broker returned malformed MQTT 5 SUBACK properties")
	}
	reasons := packet.Body[2+propertyBytes:]
	if len(reasons) != len(config.Subscriptions) {
		return fmt.Errorf("AWS IoT MQTT SUBACK reason count does not match subscriptions")
	}
	for index, reason := range reasons {
		if reason > 1 || int(reason) > config.Subscriptions[index].QoS {
			return fmt.Errorf("AWS IoT MQTT broker rejected or elevated a subscription")
		}
	}
	return nil
}

func processAWSIoTMQTTPublish(ctx context.Context, connection cloudWebSocketConnection, sink *cloudWebSocketOutputSink, packet awsMQTTPacket) error {
	return processAWSIoTMQTTPublishForConfig(ctx, connection, sink, awsIoTMQTTSubscribeConfig{ProtocolVersion: 4}, packet)
}

func processAWSIoTMQTTPublishForConfig(ctx context.Context, connection cloudWebSocketConnection, sink *cloudWebSocketOutputSink, config awsIoTMQTTSubscribeConfig, packet awsMQTTPacket) error {
	message, qos, packetID, err := decodeAWSIoTMQTTPublish(config, packet)
	if err != nil {
		return err
	}
	if err := sink.writeMessage(message); err != nil {
		return err
	}
	if qos == 1 {
		ack := []byte{0x40, 0x02, byte(packetID >> 8), byte(packetID)}
		if err := connection.Write(ctx, cloudWebSocketMessageBinary, ack); err != nil {
			return fmt.Errorf("acknowledge AWS IoT MQTT message")
		}
	}
	return nil
}

func decodeAWSIoTMQTTPublish(config awsIoTMQTTSubscribeConfig, packet awsMQTTPacket) ([]byte, byte, uint16, error) {
	if packet.Header>>4 != 3 {
		return nil, 0, 0, fmt.Errorf("AWS IoT MQTT expected a PUBLISH packet")
	}
	qos := (packet.Header >> 1) & 0x03
	if qos > 1 || len(packet.Body) < 2 {
		return nil, 0, 0, fmt.Errorf("AWS IoT MQTT PUBLISH packet has invalid QoS or topic")
	}
	topicLength := int(binary.BigEndian.Uint16(packet.Body[:2]))
	offset := 2 + topicLength
	if topicLength == 0 || topicLength > awsIoTMQTTMaxTopicBytes || offset > len(packet.Body) || validateAWSIoTMQTTTopicName(string(packet.Body[2:offset])) != nil {
		return nil, 0, 0, fmt.Errorf("AWS IoT MQTT PUBLISH packet has an invalid topic")
	}
	packetID := uint16(0)
	if qos == 1 {
		if offset+2 > len(packet.Body) {
			return nil, 0, 0, fmt.Errorf("AWS IoT MQTT QoS 1 PUBLISH omitted packet ID")
		}
		packetID = binary.BigEndian.Uint16(packet.Body[offset : offset+2])
		if packetID == 0 {
			return nil, 0, 0, fmt.Errorf("AWS IoT MQTT QoS 1 PUBLISH used packet ID zero")
		}
		offset += 2
	}
	metadata := make(map[string]any)
	if config.ProtocolVersion == 5 {
		consumed, properties, err := parseAWSIoTMQTT5PublishProperties(packet.Body[offset:])
		if err != nil {
			return nil, 0, 0, err
		}
		offset += consumed
		metadata = properties
	}
	payload := packet.Body[offset:]
	if format, ok := metadata["payload_format"].(int); ok && format == 1 && !utf8.Valid(payload) {
		return nil, 0, 0, fmt.Errorf("AWS IoT MQTT PUBLISH declared invalid UTF-8 payload")
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
		return nil, 0, 0, fmt.Errorf("encode AWS IoT MQTT message: %w", err)
	}
	return message, qos, packetID, nil
}

func validateAWSIoTMQTTTopicName(topic string) error {
	if !validAWSIoTMQTTString(topic, awsIoTMQTTMaxTopicBytes) || strings.Count(topic, "/") > awsIoTMQTTMaxTopicSlashes || strings.ContainsAny(topic, "#+") {
		return fmt.Errorf("AWS IoT MQTT topic name is invalid")
	}
	return nil
}

func parseAWSIoTMQTT5PublishProperties(data []byte) (int, map[string]any, error) {
	propertyBytes, prefix, err := decodeMQTTVariableByteInteger(data)
	if err != nil || prefix+propertyBytes > len(data) {
		return 0, nil, fmt.Errorf("AWS IoT MQTT returned malformed PUBLISH properties")
	}
	properties := data[prefix : prefix+propertyBytes]
	metadata := make(map[string]any)
	userProperties := make([]map[string]string, 0)
	seen := make(map[int]bool)
	for len(properties) > 0 {
		identifier, consumed, err := decodeMQTTVariableByteInteger(properties)
		if err != nil {
			return 0, nil, fmt.Errorf("AWS IoT MQTT returned an invalid PUBLISH property identifier")
		}
		properties = properties[consumed:]
		if identifier != 0x26 && seen[identifier] {
			return 0, nil, fmt.Errorf("AWS IoT MQTT returned a duplicate PUBLISH property")
		}
		seen[identifier] = true
		switch identifier {
		case 0x01:
			if len(properties) < 1 || properties[0] > 1 {
				return 0, nil, fmt.Errorf("AWS IoT MQTT returned invalid payload format")
			}
			metadata["payload_format"] = int(properties[0])
			properties = properties[1:]
		case 0x02:
			if len(properties) < 4 {
				return 0, nil, fmt.Errorf("AWS IoT MQTT returned invalid message expiry")
			}
			metadata["message_expiry_seconds"] = binary.BigEndian.Uint32(properties[:4])
			properties = properties[4:]
		case 0x03:
			value, consumed, err := readMQTTUTF8(properties)
			if err != nil {
				return 0, nil, fmt.Errorf("AWS IoT MQTT returned invalid content type")
			}
			metadata["content_type"] = value
			properties = properties[consumed:]
		case 0x08:
			value, consumed, err := readMQTTUTF8(properties)
			if err != nil || validateAWSIoTMQTTTopicName(value) != nil {
				return 0, nil, fmt.Errorf("AWS IoT MQTT returned invalid response topic")
			}
			metadata["response_topic"] = value
			properties = properties[consumed:]
		case 0x09:
			value, consumed, err := readMQTTBinary(properties)
			if err != nil {
				return 0, nil, fmt.Errorf("AWS IoT MQTT returned invalid correlation data")
			}
			metadata["correlation_data_base64"] = base64.StdEncoding.EncodeToString(value)
			properties = properties[consumed:]
		case 0x26:
			name, first, err := readMQTTUTF8(properties)
			if err != nil {
				return 0, nil, fmt.Errorf("AWS IoT MQTT returned invalid user property")
			}
			value, second, err := readMQTTUTF8(properties[first:])
			if err != nil {
				return 0, nil, fmt.Errorf("AWS IoT MQTT returned invalid user property")
			}
			userProperties = append(userProperties, map[string]string{"name": name, "value": value})
			properties = properties[first+second:]
		case 0x0b:
			return 0, nil, fmt.Errorf("AWS IoT MQTT returned unsupported subscription identifier")
		case 0x23:
			return 0, nil, fmt.Errorf("AWS IoT MQTT returned a topic alias after the client advertised a zero maximum")
		default:
			return 0, nil, fmt.Errorf("AWS IoT MQTT returned unsupported PUBLISH property")
		}
	}
	if len(userProperties) > 0 {
		metadata["user_properties"] = userProperties
	}
	return prefix + propertyBytes, metadata, nil
}

func validateAWSIoTMQTTDisconnect(packet awsMQTTPacket, protocolVersion int) error {
	if packet.Header != 0xe0 {
		return fmt.Errorf("AWS IoT MQTT broker returned a malformed DISCONNECT")
	}
	if protocolVersion == 4 {
		if len(packet.Body) != 0 {
			return fmt.Errorf("AWS IoT MQTT broker returned a malformed MQTT 3.1.1 DISCONNECT")
		}
		return nil
	}
	if len(packet.Body) == 0 {
		return nil
	}
	if packet.Body[0] != 0 {
		return fmt.Errorf("AWS IoT MQTT broker disconnected with reason code 0x%02x", packet.Body[0])
	}
	if len(packet.Body) == 1 {
		return fmt.Errorf("AWS IoT MQTT broker omitted DISCONNECT property length")
	}
	consumed, _, err := validateAzureWebPubSubMQTT5ControlProperties(packet.Body[1:], azureWebPubSubMQTT5DisconnectProperties)
	if err != nil || consumed != len(packet.Body)-1 {
		return fmt.Errorf("AWS IoT MQTT broker returned malformed DISCONNECT properties")
	}
	return nil
}

func invokeAWSIoTMQTTWebSocket(ctx context.Context, adapter *AWSRESTAdapter, credentials AWSCredentials, invocation Invocation) (InvocationResult, error) {
	if err := validateAWSIoTMQTTWebSocketInvocation(invocation, adapter.config.AllowedHosts); err != nil {
		return InvocationResult{}, err
	}
	config, _ := parseAWSIoTMQTTSubscribeConfig(invocation.Body)
	signedURL, err := signAWSIoTMQTTWebSocketURL(invocation.URL, credentials, invocation.Region, adapter.config.Now().UTC())
	if err != nil {
		return InvocationResult{}, err
	}
	handshakeCtx, cancelHandshake := context.WithTimeout(ctx, 30*time.Second)
	defer cancelHandshake()
	connection, err := adapter.config.IoTWebSocketDial(handshakeCtx, signedURL)
	if err != nil {
		return InvocationResult{}, err
	}
	defer connection.Close()
	sink, err := newWebSocketOutputSink(invocation, adapter.config.MaxBodyBytes, "AWS IoT MQTT WebSocket")
	if err != nil {
		return InvocationResult{}, err
	}
	defer sink.abort()
	connectPacket, _ := encodeAWSIoTMQTTConnect(config)
	if err := connection.Write(handshakeCtx, cloudWebSocketMessageBinary, connectPacket); err != nil {
		return InvocationResult{}, fmt.Errorf("send AWS IoT MQTT CONNECT")
	}
	reader := &awsMQTTPacketReader{connection: connection}
	packet, err := reader.Read(handshakeCtx)
	capabilities, connAckErr := parseAWSIoTMQTTConnAck(packet, config)
	if err != nil || connAckErr != nil {
		return InvocationResult{}, fmt.Errorf("AWS IoT MQTT connection was not acknowledged")
	}
	if capabilities.MaximumPacketSize < 4 {
		return InvocationResult{}, fmt.Errorf("AWS IoT MQTT broker maximum packet size cannot carry QoS acknowledgements")
	}
	subscribePacket, _ := encodeAWSIoTMQTTSubscribe(config)
	if uint32(len(subscribePacket)) > capabilities.MaximumPacketSize {
		return InvocationResult{}, fmt.Errorf("AWS IoT MQTT SUBSCRIBE exceeds broker maximum packet size")
	}
	if err := connection.Write(handshakeCtx, cloudWebSocketMessageBinary, subscribePacket); err != nil {
		return InvocationResult{}, fmt.Errorf("send AWS IoT MQTT SUBSCRIBE")
	}
	received := 0
	for {
		packet, err = reader.Read(handshakeCtx)
		if err != nil {
			return InvocationResult{}, fmt.Errorf("read AWS IoT MQTT SUBACK")
		}
		if packet.Header>>4 == 3 {
			if received >= config.MaxMessages {
				return InvocationResult{}, fmt.Errorf("AWS IoT MQTT broker exceeded max_messages before SUBACK")
			}
			if err := processAWSIoTMQTTPublishForConfig(handshakeCtx, connection, sink, config, packet); err != nil {
				return InvocationResult{}, err
			}
			received++
			continue
		}
		if err := validateAWSIoTMQTTSubAckForConfig(packet, config); err != nil {
			return InvocationResult{}, err
		}
		break
	}
	cancelHandshake()
	collectionCtx, cancelCollection := context.WithTimeout(ctx, time.Duration(config.TimeoutSeconds)*time.Second)
	defer cancelCollection()
	remoteEnded := false
	pingOutstanding := false
	negotiatedKeepAlive := uint16(config.TimeoutSeconds + 30)
	if capabilities.ServerKeepAlive != nil {
		negotiatedKeepAlive = *capabilities.ServerKeepAlive
	}
	for received < config.MaxMessages {
		readCtx := collectionCtx
		cancelRead := func() {}
		if negotiatedKeepAlive > 0 {
			readCtx, cancelRead = context.WithTimeout(collectionCtx, time.Duration(negotiatedKeepAlive)*time.Second/2)
		}
		packet, err = reader.Read(readCtx)
		cancelRead()
		if errors.Is(err, context.DeadlineExceeded) && collectionCtx.Err() == nil {
			if pingOutstanding {
				return InvocationResult{}, fmt.Errorf("AWS IoT MQTT broker did not answer PINGREQ")
			}
			if err := connection.Write(collectionCtx, cloudWebSocketMessageBinary, []byte{0xc0, 0x00}); err != nil {
				return InvocationResult{}, fmt.Errorf("send AWS IoT MQTT PINGREQ")
			}
			pingOutstanding = true
			continue
		}
		if errors.Is(err, context.DeadlineExceeded) {
			break
		}
		if err != nil {
			return InvocationResult{}, fmt.Errorf("read AWS IoT MQTT message")
		}
		switch packet.Header >> 4 {
		case 3:
			if err := processAWSIoTMQTTPublishForConfig(collectionCtx, connection, sink, config, packet); err != nil {
				return InvocationResult{}, err
			}
			received++
		case 13:
			if packet.Header != 0xd0 || len(packet.Body) != 0 {
				return InvocationResult{}, fmt.Errorf("AWS IoT MQTT broker returned a malformed PINGRESP")
			}
			pingOutstanding = false
		case 14:
			if err := validateAWSIoTMQTTDisconnect(packet, config.ProtocolVersion); err != nil {
				return InvocationResult{}, fmt.Errorf("AWS IoT MQTT broker disconnected: %w", err)
			}
			remoteEnded = true
		default:
			return InvocationResult{}, fmt.Errorf("AWS IoT MQTT broker returned an unexpected packet")
		}
		if remoteEnded {
			break
		}
	}
	if !remoteEnded {
		if err := connection.Write(ctx, cloudWebSocketMessageBinary, []byte{0xe0, 0x00}); err != nil {
			return InvocationResult{}, fmt.Errorf("send AWS IoT MQTT DISCONNECT")
		}
	}
	output, err := sink.finish(config.ClientID)
	if err != nil {
		return InvocationResult{}, err
	}
	return InvocationResult{Output: output, RequestID: config.ClientID}, nil
}

func defaultAWSIoTMQTTWebSocketDial(ctx context.Context, signedURL string) (cloudWebSocketConnection, error) {
	client := &http.Client{Transport: http.DefaultTransport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	connection, response, err := websocket.Dial(ctx, signedURL, &websocket.DialOptions{
		HTTPClient: client, CompressionMode: websocket.CompressionDisabled, Subprotocols: []string{"mqtt"},
	})
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("AWS IoT MQTT WebSocket handshake failed with HTTP %d", response.StatusCode)
		}
		return nil, fmt.Errorf("AWS IoT MQTT WebSocket handshake failed")
	}
	connection.SetReadLimit(awsIoTMQTTMaxPacketBytes)
	return coderTencentWebSocketConnection{connection: connection}, nil
}

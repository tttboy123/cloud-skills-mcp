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
	awsIoTMQTTMaxPacketBytes       = 146 * 1024
	awsIoTMQTTMaxTimeoutSeconds    = 300
	awsIoTMQTTSubscribePacketID    = 1
	awsIoTMQTTMaximumKeepAliveSecs = 1200
)

type awsIoTMQTTSubscription struct {
	TopicFilter string `json:"topic_filter"`
	QoS         int    `json:"qos"`
}

type awsIoTMQTTSubscribeConfig struct {
	ClientID       string                   `json:"client_id"`
	Subscriptions  []awsIoTMQTTSubscription `json:"subscriptions"`
	MaxMessages    int                      `json:"max_messages"`
	TimeoutSeconds int                      `json:"timeout_seconds"`
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
	var config awsIoTMQTTSubscribeConfig
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
	return append(encoded, body...), nil
}

func encodeAWSIoTMQTTConnect(config awsIoTMQTTSubscribeConfig) ([]byte, error) {
	keepAlive := config.TimeoutSeconds + 30
	if keepAlive > awsIoTMQTTMaximumKeepAliveSecs {
		keepAlive = awsIoTMQTTMaximumKeepAliveSecs
	}
	body := []byte{0x00, 0x04, 'M', 'Q', 'T', 'T', 0x04, 0x02, byte(keepAlive >> 8), byte(keepAlive)}
	body = appendMQTTUTF8(body, config.ClientID)
	return encodeAWSMQTTPacket(0x10, body)
}

func encodeAWSIoTMQTTSubscribe(config awsIoTMQTTSubscribeConfig) ([]byte, error) {
	body := []byte{byte(awsIoTMQTTSubscribePacketID >> 8), byte(awsIoTMQTTSubscribePacketID)}
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

func processAWSIoTMQTTPublish(ctx context.Context, connection cloudWebSocketConnection, sink *cloudWebSocketOutputSink, packet awsMQTTPacket) error {
	if packet.Header>>4 != 3 {
		return fmt.Errorf("AWS IoT MQTT expected a PUBLISH packet")
	}
	qos := (packet.Header >> 1) & 0x03
	if qos > 1 || len(packet.Body) < 2 {
		return fmt.Errorf("AWS IoT MQTT PUBLISH packet has invalid QoS or topic")
	}
	topicLength := int(binary.BigEndian.Uint16(packet.Body[:2]))
	offset := 2 + topicLength
	if topicLength == 0 || topicLength > awsIoTMQTTMaxTopicBytes || offset > len(packet.Body) || !utf8.Valid(packet.Body[2:offset]) {
		return fmt.Errorf("AWS IoT MQTT PUBLISH packet has an invalid topic")
	}
	packetID := uint16(0)
	if qos == 1 {
		if offset+2 > len(packet.Body) {
			return fmt.Errorf("AWS IoT MQTT QoS 1 PUBLISH omitted packet ID")
		}
		packetID = binary.BigEndian.Uint16(packet.Body[offset : offset+2])
		if packetID == 0 {
			return fmt.Errorf("AWS IoT MQTT QoS 1 PUBLISH used packet ID zero")
		}
		offset += 2
	}
	payload := packet.Body[offset:]
	message, err := json.Marshal(map[string]any{
		"topic":          string(packet.Body[2 : 2+topicLength]),
		"qos":            qos,
		"duplicate":      packet.Header&0x08 != 0,
		"retained":       packet.Header&0x01 != 0,
		"packet_id":      packetID,
		"bytes":          len(payload),
		"payload_base64": base64.StdEncoding.EncodeToString(payload),
	})
	if err != nil {
		return fmt.Errorf("encode AWS IoT MQTT message: %w", err)
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
	if err != nil || validateAWSIoTMQTTConnAck(packet) != nil {
		return InvocationResult{}, fmt.Errorf("AWS IoT MQTT connection was not acknowledged")
	}
	subscribePacket, _ := encodeAWSIoTMQTTSubscribe(config)
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
			if err := processAWSIoTMQTTPublish(handshakeCtx, connection, sink, packet); err != nil {
				return InvocationResult{}, err
			}
			received++
			continue
		}
		if err := validateAWSIoTMQTTSubAckForSubscriptions(packet, config.Subscriptions); err != nil {
			return InvocationResult{}, err
		}
		break
	}
	cancelHandshake()
	collectionCtx, cancelCollection := context.WithTimeout(ctx, time.Duration(config.TimeoutSeconds)*time.Second)
	defer cancelCollection()
	for received < config.MaxMessages {
		packet, err = reader.Read(collectionCtx)
		if errors.Is(err, context.DeadlineExceeded) {
			break
		}
		if err != nil {
			return InvocationResult{}, fmt.Errorf("read AWS IoT MQTT message")
		}
		switch packet.Header >> 4 {
		case 3:
			if err := processAWSIoTMQTTPublish(collectionCtx, connection, sink, packet); err != nil {
				return InvocationResult{}, err
			}
			received++
		case 13:
			if packet.Header != 0xd0 || len(packet.Body) != 0 {
				return InvocationResult{}, fmt.Errorf("AWS IoT MQTT broker returned a malformed PINGRESP")
			}
		default:
			return InvocationResult{}, fmt.Errorf("AWS IoT MQTT broker returned an unexpected packet")
		}
	}
	if err := connection.Write(ctx, cloudWebSocketMessageBinary, []byte{0xe0, 0x00}); err != nil {
		return InvocationResult{}, fmt.Errorf("send AWS IoT MQTT DISCONNECT")
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

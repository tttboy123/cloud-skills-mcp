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

type azureWebPubSubMQTTPlan struct {
	ClientID         string                           `json:"client_id"`
	Subscriptions    []azureWebPubSubMQTTSubscription `json:"subscriptions"`
	KeepAliveSeconds int                              `json:"keep_alive_seconds"`
	MaxMessages      int                              `json:"max_messages"`
	TimeoutSeconds   int                              `json:"timeout_seconds"`
}

type azureWebPubSubMQTTPacket struct {
	Header byte
	Body   []byte
}

type azureWebPubSubMQTTPacketReader struct {
	connection cloudWebSocketConnection
	buffer     []byte
}

func validateAzureWebPubSubMQTTInvocation(invocation Invocation) error {
	if !strings.EqualFold(invocation.Service, "webpubsub") || !strings.EqualFold(invocation.Operation, "SubscribeMQTT") || !strings.EqualFold(invocation.Method, http.MethodGet) {
		return fmt.Errorf("Azure Web PubSub MQTT requires GET, service webpubsub, and operation SubscribeMQTT")
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
	_, err := parseAzureWebPubSubMQTTPlan(invocation.Body)
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

func parseAzureWebPubSubMQTTPlan(body any) (azureWebPubSubMQTTPlan, error) {
	if body == nil || azureRealtimeContainsCredentialField(body) {
		return azureWebPubSubMQTTPlan{}, fmt.Errorf("Azure Web PubSub MQTT requires a credential-free protocol body")
	}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) == 0 || len(encoded) > maxRequestPayloadBytes {
		return azureWebPubSubMQTTPlan{}, fmt.Errorf("Azure Web PubSub MQTT body must be bounded JSON")
	}
	var plan azureWebPubSubMQTTPlan
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&plan) != nil || ensureJSONDecoderEOF(decoder) != nil {
		return azureWebPubSubMQTTPlan{}, fmt.Errorf("Azure Web PubSub MQTT body does not match the finite subscription schema")
	}
	if !azureWebPubSubMQTTClientIDPattern.MatchString(plan.ClientID) {
		return azureWebPubSubMQTTPlan{}, fmt.Errorf("Azure Web PubSub MQTT client_id must contain 1 to 128 ASCII letters or digits")
	}
	if len(plan.Subscriptions) < 1 || len(plan.Subscriptions) > azureWebPubSubMQTTMaxTopics {
		return azureWebPubSubMQTTPlan{}, fmt.Errorf("Azure Web PubSub MQTT requires 1 to %d subscriptions", azureWebPubSubMQTTMaxTopics)
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
		if subscription.QoS != 0 && subscription.QoS != 1 {
			return azureWebPubSubMQTTPlan{}, fmt.Errorf("Azure Web PubSub MQTT 3.1.1 subscription QoS must be 0 or 1")
		}
	}
	if plan.KeepAliveSeconds < 1 || plan.KeepAliveSeconds > azureWebPubSubMQTTMaxKeepAlive {
		return azureWebPubSubMQTTPlan{}, fmt.Errorf("Azure Web PubSub MQTT 3.1.1 keep_alive_seconds must be between 1 and %d", azureWebPubSubMQTTMaxKeepAlive)
	}
	if plan.MaxMessages < 1 || plan.MaxMessages > azureWebPubSubMQTTMaxMessages || plan.TimeoutSeconds < 1 || plan.TimeoutSeconds > azureWebPubSubMQTTMaxTimeout {
		return azureWebPubSubMQTTPlan{}, fmt.Errorf("Azure Web PubSub MQTT message count or timeout is outside the finite bound")
	}
	return plan, nil
}

func invokeAzureWebPubSubMQTT(ctx context.Context, adapter *AzureRESTAdapter, invocation Invocation) (InvocationResult, error) {
	if err := validateAzureWebPubSubMQTTInvocation(invocation); err != nil {
		return InvocationResult{}, err
	}
	target, hub, _ := parseAzureWebPubSubMQTTTarget(invocation.URL)
	plan, _ := parseAzureWebPubSubMQTTPlan(invocation.Body)
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
	connectPacket, _ := encodeAzureWebPubSubMQTTConnect(plan)
	if err := connection.Write(handshakeCtx, cloudWebSocketMessageBinary, connectPacket); err != nil {
		return InvocationResult{}, fmt.Errorf("send Azure Web PubSub MQTT CONNECT")
	}
	reader := &azureWebPubSubMQTTPacketReader{connection: connection}
	packet, err := reader.Read(handshakeCtx)
	if err != nil || validateAzureWebPubSubMQTTConnAck(packet) != nil {
		return InvocationResult{}, fmt.Errorf("Azure Web PubSub MQTT connection was not acknowledged")
	}
	subscribePacket, _ := encodeAzureWebPubSubMQTTSubscribe(plan)
	if err := connection.Write(handshakeCtx, cloudWebSocketMessageBinary, subscribePacket); err != nil {
		return InvocationResult{}, fmt.Errorf("send Azure Web PubSub MQTT SUBSCRIBE")
	}
	received := 0
	for {
		packet, err = reader.Read(handshakeCtx)
		if err != nil {
			return InvocationResult{}, fmt.Errorf("read Azure Web PubSub MQTT SUBACK")
		}
		if packet.Header>>4 == 3 {
			if received >= plan.MaxMessages {
				return InvocationResult{}, fmt.Errorf("Azure Web PubSub MQTT broker exceeded max_messages before SUBACK")
			}
			if err := processAzureWebPubSubMQTTPublish(handshakeCtx, connection, sink, packet); err != nil {
				return InvocationResult{}, err
			}
			received++
			continue
		}
		if err := validateAzureWebPubSubMQTTSubAck(packet, plan.Subscriptions); err != nil {
			return InvocationResult{}, err
		}
		break
	}
	cancelHandshake()
	collectionCtx, cancelCollection := context.WithTimeout(ctx, time.Duration(plan.TimeoutSeconds)*time.Second)
	defer cancelCollection()
	pingOutstanding := false
	for received < plan.MaxMessages {
		readCtx, cancelRead := context.WithTimeout(collectionCtx, time.Duration(plan.KeepAliveSeconds)*time.Second/2)
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
			break
		}
		if err != nil {
			return InvocationResult{}, fmt.Errorf("read Azure Web PubSub MQTT message")
		}
		switch packet.Header >> 4 {
		case 3:
			if err := processAzureWebPubSubMQTTPublish(collectionCtx, connection, sink, packet); err != nil {
				return InvocationResult{}, err
			}
			received++
		case 13:
			if packet.Header != 0xd0 || len(packet.Body) != 0 {
				return InvocationResult{}, fmt.Errorf("Azure Web PubSub MQTT broker returned a malformed PINGRESP")
			}
			pingOutstanding = false
		default:
			return InvocationResult{}, fmt.Errorf("Azure Web PubSub MQTT broker returned an unexpected packet")
		}
	}
	if err := connection.Write(ctx, cloudWebSocketMessageBinary, []byte{0xe0, 0x00}); err != nil {
		return InvocationResult{}, fmt.Errorf("send Azure Web PubSub MQTT DISCONNECT")
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
	if len(body) > azureWebPubSubMQTTMaxPacketBytes {
		return nil, fmt.Errorf("Azure Web PubSub MQTT packet exceeds %d bytes", azureWebPubSubMQTTMaxPacketBytes)
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

func encodeAzureWebPubSubMQTTConnect(plan azureWebPubSubMQTTPlan) ([]byte, error) {
	body := []byte{0x00, 0x04, 'M', 'Q', 'T', 'T', 0x04, 0x02, byte(plan.KeepAliveSeconds >> 8), byte(plan.KeepAliveSeconds)}
	body = appendMQTTUTF8(body, plan.ClientID)
	return encodeAzureWebPubSubMQTTPacket(0x10, body)
}

func encodeAzureWebPubSubMQTTSubscribe(plan azureWebPubSubMQTTPlan) ([]byte, error) {
	body := []byte{byte(azureWebPubSubMQTTSubscribeID >> 8), byte(azureWebPubSubMQTTSubscribeID)}
	for _, subscription := range plan.Subscriptions {
		body = appendMQTTUTF8(body, subscription.TopicFilter)
		body = append(body, byte(subscription.QoS))
	}
	return encodeAzureWebPubSubMQTTPacket(0x82, body)
}

func validateAzureWebPubSubMQTTConnAck(packet azureWebPubSubMQTTPacket) error {
	if packet.Header != 0x20 || len(packet.Body) != 2 || packet.Body[0]&0xfe != 0 || packet.Body[1] != 0 {
		return fmt.Errorf("Azure Web PubSub MQTT broker rejected or malformed CONNACK")
	}
	if packet.Body[0]&0x01 != 0 {
		return fmt.Errorf("Azure Web PubSub MQTT broker resumed a session despite clean-session request")
	}
	return nil
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

func processAzureWebPubSubMQTTPublish(ctx context.Context, connection cloudWebSocketConnection, sink *cloudWebSocketOutputSink, packet azureWebPubSubMQTTPacket) error {
	if packet.Header>>4 != 3 || packet.Header&0x01 != 0 {
		return fmt.Errorf("Azure Web PubSub MQTT expected a non-retained PUBLISH packet")
	}
	qos := (packet.Header >> 1) & 0x03
	if qos > 1 || len(packet.Body) < 2 {
		return fmt.Errorf("Azure Web PubSub MQTT PUBLISH packet has invalid QoS or topic")
	}
	topicLength := int(binary.BigEndian.Uint16(packet.Body[:2]))
	offset := 2 + topicLength
	if topicLength == 0 || topicLength > azureWebPubSubMQTTMaxTopicBytes || offset > len(packet.Body) || !utf8.Valid(packet.Body[2:offset]) {
		return fmt.Errorf("Azure Web PubSub MQTT PUBLISH packet has an invalid topic")
	}
	packetID := uint16(0)
	if qos == 1 {
		if offset+2 > len(packet.Body) {
			return fmt.Errorf("Azure Web PubSub MQTT QoS 1 PUBLISH omitted packet ID")
		}
		packetID = binary.BigEndian.Uint16(packet.Body[offset : offset+2])
		if packetID == 0 {
			return fmt.Errorf("Azure Web PubSub MQTT QoS 1 PUBLISH used packet ID zero")
		}
		offset += 2
	}
	payload := packet.Body[offset:]
	message, err := json.Marshal(map[string]any{
		"topic": string(packet.Body[2 : 2+topicLength]), "qos": qos, "duplicate": packet.Header&0x08 != 0,
		"retained": false, "packet_id": packetID, "bytes": len(payload), "payload_base64": base64.StdEncoding.EncodeToString(payload),
	})
	if err != nil {
		return fmt.Errorf("encode Azure Web PubSub MQTT message")
	}
	if err := sink.writeMessage(message); err != nil {
		return err
	}
	if qos == 1 {
		ack := []byte{0x40, 0x02, byte(packetID >> 8), byte(packetID)}
		if err := connection.Write(ctx, cloudWebSocketMessageBinary, ack); err != nil {
			return fmt.Errorf("acknowledge Azure Web PubSub MQTT message")
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

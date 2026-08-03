package cloud

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestAzureWebPubSubMQTT5PlanSeparatesReadSubscriptionsFromMutatingClients(t *testing.T) {
	mutatingBody := map[string]any{
		"protocol_version": 5, "client_id": "Publisher123", "clean_start": false, "session_expiry_seconds": 30,
		"subscription_identifier": 7,
		"subscriptions":           []any{map[string]any{"topic_filter": "room/in", "qos": 2}},
		"publishes": []any{map[string]any{
			"topic": "room/out", "qos": 2, "payload_base64": base64.StdEncoding.EncodeToString([]byte("outbound")),
			"payload_format": 1, "content_type": "text/plain", "message_expiry_seconds": 30,
		}},
		"will": map[string]any{
			"topic": "room/status", "qos": 2, "payload_base64": base64.StdEncoding.EncodeToString([]byte("offline")),
			"payload_format": 1, "content_type": "text/plain", "message_expiry_seconds": 30, "delay_seconds": 1,
		},
		"keep_alive_seconds": 30, "max_messages": 1, "timeout_seconds": 30,
	}
	mutation := Invocation{
		Provider: ProviderAzure, Mode: ModeMutate, AuthScheme: "webpubsub-mqtt-ws", Service: "webpubsub", Operation: "ClientMQTT",
		Method: http.MethodGet, URL: "wss://demo.webpubsub.azure.com/clients/mqtt/hubs/chat", Body: mutatingBody,
		ResponseFile: filepath.Join(t.TempDir(), "mqtt.ndjson"), MaxResponseFileBytes: 4096,
	}
	if classifyRead(ProviderAzure, mutation) {
		t.Fatal("bidirectional MQTT client was classified read-only")
	}
	if err := validateAzureWebPubSubMQTTInvocation(mutation); err != nil {
		t.Fatal(err)
	}
	plan, err := parseAzureWebPubSubMQTTPlan(mutatingBody, true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.ProtocolVersion != 5 || plan.CleanStart || plan.SessionExpirySeconds != 30 || plan.SubscriptionIdentifier != 7 || len(plan.Publishes) != 1 || plan.Will == nil {
		t.Fatalf("plan=%+v", plan)
	}
	read := mutation
	read.Mode = ModeRead
	read.Operation = "SubscribeMQTT"
	if err := validateAzureWebPubSubMQTTInvocation(read); err == nil {
		t.Fatal("read-only MQTT subscription accepted publish or Last Will behavior")
	}
	for name, mutate := range map[string]func(map[string]any){
		"protocol":       func(body map[string]any) { body["protocol_version"] = 3 },
		"expiry":         func(body map[string]any) { body["session_expiry_seconds"] = 31 },
		"mqtt311 expiry": func(body map[string]any) { body["protocol_version"] = 4 },
		"mqtt311 metadata": func(body map[string]any) {
			body["protocol_version"] = 4
			body["session_expiry_seconds"] = 0
			body["subscription_identifier"] = 0
		},
		"will retained": func(body map[string]any) {
			body["will"].(map[string]any)["retain"] = true
		},
		"will oversized": func(body map[string]any) {
			body["will"].(map[string]any)["payload_base64"] = base64.StdEncoding.EncodeToString(make([]byte, 2001))
		},
		"publish wildcard":                func(body map[string]any) { body["publishes"].([]any)[0].(map[string]any)["topic"] = "room/+" },
		"subscription id":                 func(body map[string]any) { body["subscription_identifier"] = 268435456 },
		"subscription without collection": func(body map[string]any) { body["max_messages"] = 0 },
		"encoded packet oversized": func(body map[string]any) {
			body["publishes"].([]any)[0].(map[string]any)["payload_base64"] = base64.StdEncoding.EncodeToString(make([]byte, azureWebPubSubMQTTMaxPacketBytes))
		},
	} {
		t.Run(name, func(t *testing.T) {
			body := cloneJSONMap(t, mutatingBody)
			mutate(body)
			candidate := mutation
			candidate.Body = body
			if err := validateAzureWebPubSubMQTTInvocation(candidate); err == nil {
				t.Fatal("invalid MQTT 5 plan accepted")
			}
		})
	}
}

func TestAzureWebPubSubMQTT5EncodesConnectWillPropertiesAndPublish(t *testing.T) {
	plan := azureWebPubSubMQTTPlan{
		ProtocolVersion: 5, ClientID: "Publisher123", CleanStart: false, SessionExpirySeconds: 30,
		SubscriptionIdentifier: 7,
		Subscriptions:          []azureWebPubSubMQTTSubscription{{TopicFilter: "room/in", QoS: 2}},
		Publishes: []azureWebPubSubMQTTPublish{{
			Topic: "room/out", QoS: 2, Payload: []byte("outbound"), PayloadFormat: 1, ContentType: "text/plain", MessageExpirySeconds: 30,
		}},
		Will: &azureWebPubSubMQTTWill{
			Topic: "room/status", QoS: 2, Payload: []byte("offline"), PayloadFormat: 1, ContentType: "text/plain", MessageExpirySeconds: 30, DelaySeconds: 1,
		},
		KeepAliveSeconds: 30, MaxMessages: 1, TimeoutSeconds: 30,
	}
	connect, err := encodeAzureWebPubSubMQTTConnect(plan)
	if err != nil {
		t.Fatal(err)
	}
	packet, _, complete, err := decodeAzureWebPubSubMQTTPacket(connect)
	if err != nil || !complete || packet.Header != 0x10 || len(packet.Body) < 20 || packet.Body[6] != 5 || packet.Body[7] != 0x14 || !bytes.Contains(packet.Body, []byte("room/status")) || !bytes.Contains(packet.Body, []byte("offline")) || !bytes.Contains(packet.Body, []byte{0x11, 0x00, 0x00, 0x00, 0x1e}) {
		t.Fatalf("CONNECT=%x complete=%t err=%v", connect, complete, err)
	}
	subscribe, err := encodeAzureWebPubSubMQTTSubscribe(plan)
	if err != nil {
		t.Fatal(err)
	}
	subPacket, _, complete, err := decodeAzureWebPubSubMQTTPacket(subscribe)
	if err != nil || !complete || !bytes.HasPrefix(subPacket.Body, []byte{0x00, 0x01, 0x02, 0x0b, 0x07}) || !bytes.Contains(subPacket.Body, []byte("room/in")) || subPacket.Body[len(subPacket.Body)-1] != 2 {
		t.Fatalf("SUBSCRIBE=%x complete=%t err=%v", subscribe, complete, err)
	}
	publish, err := encodeAzureWebPubSubMQTTPublish(plan, plan.Publishes[0], 2)
	if err != nil {
		t.Fatal(err)
	}
	pubPacket, _, complete, err := decodeAzureWebPubSubMQTTPacket(publish)
	if err != nil || !complete || pubPacket.Header != 0x34 || !bytes.Contains(pubPacket.Body, []byte("room/out")) || !bytes.Contains(pubPacket.Body, []byte("text/plain")) || !bytes.HasSuffix(pubPacket.Body, []byte("outbound")) {
		t.Fatalf("PUBLISH=%x complete=%t err=%v", publish, complete, err)
	}
}

func TestAzureWebPubSubMQTT5CompletesBidirectionalQoS2ExactlyOnce(t *testing.T) {
	responseFile := filepath.Join(t.TempDir(), "mqtt5.ndjson")
	inbound := testAzureWebPubSubMQTT5Publish("room/in", 2, 7, []byte("inbound"), true)
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			{0x20, 0x03, 0x00, 0x00, 0x00},
			{0x90, 0x04, 0x00, 0x01, 0x00, 0x02},
			{0x50, 0x02, 0x00, 0x02},
			{0x70, 0x02, 0x00, 0x02},
			inbound,
			inbound,
			{0x62, 0x02, 0x00, 0x07},
		},
		readTypes: []tencentWebSocketMessageType{
			cloudWebSocketMessageBinary, cloudWebSocketMessageBinary, cloudWebSocketMessageBinary, cloudWebSocketMessageBinary,
			cloudWebSocketMessageBinary, cloudWebSocketMessageBinary, cloudWebSocketMessageBinary,
		},
	}
	var tokenRequest *http.Request
	adapter := NewAzureRESTAdapter(AzureRESTConfig{
		Tokens: &staticAzureTokenProvider{token: "private-entra-token"},
		HTTP: doerFunc(func(request *http.Request) (*http.Response, error) {
			tokenRequest = request.Clone(request.Context())
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"token":"private-client-token"}`))}, nil
		}),
		WebPubSubMQTTWebSocketDial: func(context.Context, string, http.Header) (cloudWebSocketConnection, error) {
			return connection, nil
		},
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, Mode: ModeMutate, AuthScheme: "webpubsub-mqtt-ws", Service: "webpubsub", Operation: "ClientMQTT",
		Method: http.MethodGet, URL: "wss://demo.webpubsub.azure.com/clients/mqtt/hubs/chat",
		Body: map[string]any{
			"protocol_version": 5, "client_id": "Publisher123", "clean_start": false, "session_expiry_seconds": 30,
			"subscription_identifier": 7,
			"subscriptions":           []any{map[string]any{"topic_filter": "room/in", "qos": 2}},
			"publishes": []any{map[string]any{
				"topic": "room/out", "qos": 2, "payload_base64": base64.StdEncoding.EncodeToString([]byte("outbound")),
				"payload_format": 1, "content_type": "text/plain", "message_expiry_seconds": 30,
			}},
			"will": map[string]any{
				"topic": "room/status", "qos": 2, "payload_base64": base64.StdEncoding.EncodeToString([]byte("offline")),
				"payload_format": 1, "content_type": "text/plain", "message_expiry_seconds": 30, "delay_seconds": 1,
			},
			"keep_alive_seconds": 30, "max_messages": 1, "timeout_seconds": 30,
		},
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	roles := tokenRequest.URL.Query()["role"]
	for _, expected := range []string{"webpubsub.joinLeaveGroup.room/in", "webpubsub.sendToGroup.room/out", "webpubsub.sendToGroup.room/status"} {
		if !containsString(roles, expected) {
			t.Fatalf("missing role %q in %v", expected, roles)
		}
	}
	var outboundPublish, outboundPUBREL, inboundPUBREC, inboundPUBCOMP bool
	for _, write := range connection.writes {
		switch write.data[0] {
		case 0x34:
			outboundPublish = bytes.Contains(write.data, []byte("outbound"))
		case 0x62:
			outboundPUBREL = bytes.Contains(write.data, []byte{0x00, 0x02})
		case 0x50:
			inboundPUBREC = bytes.Contains(write.data, []byte{0x00, 0x07})
		case 0x70:
			inboundPUBCOMP = bytes.Contains(write.data, []byte{0x00, 0x07})
		}
	}
	if !outboundPublish || !outboundPUBREL || !inboundPUBREC || !inboundPUBCOMP {
		t.Fatalf("writes=%#v", connection.writes)
	}
	written, readErr := os.ReadFile(responseFile)
	if readErr != nil || bytes.Count(bytes.TrimSpace(written), []byte{'\n'}) != 0 || !bytes.Contains(written, []byte(`"qos":2`)) || !bytes.Contains(written, []byte(`"payload_base64":"aW5ib3VuZA=="`)) || bytes.Contains(written, []byte("private-")) {
		t.Fatalf("output=%s err=%v", written, readErr)
	}
	if result.RequestID != "Publisher123" || !bytes.Contains(result.Output, []byte(`"messages":1`)) || bytes.Contains(result.Output, []byte("private-")) {
		t.Fatalf("result=%+v", result)
	}
}

func TestAzureWebPubSubMQTT311PublishesQoS1WithoutSubscription(t *testing.T) {
	responseFile := filepath.Join(t.TempDir(), "mqtt311.ndjson")
	connection := &fakeTencentWebSocketConnection{
		reads:     [][]byte{{0x20, 0x02, 0x00, 0x00}, {0x40, 0x02, 0x00, 0x02}},
		readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageBinary, cloudWebSocketMessageBinary},
	}
	var tokenRequest *http.Request
	adapter := NewAzureRESTAdapter(AzureRESTConfig{
		Tokens: &staticAzureTokenProvider{token: "private-entra-token"},
		HTTP: doerFunc(func(request *http.Request) (*http.Response, error) {
			tokenRequest = request.Clone(request.Context())
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"token":"private-client-token"}`))}, nil
		}),
		WebPubSubMQTTWebSocketDial: func(context.Context, string, http.Header) (cloudWebSocketConnection, error) {
			return connection, nil
		},
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, Mode: ModeMutate, AuthScheme: "webpubsub-mqtt-ws", Service: "webpubsub", Operation: "ClientMQTT",
		Method: http.MethodGet, URL: "wss://demo.webpubsub.azure.com/clients/mqtt/hubs/chat",
		Body: map[string]any{
			"client_id": "Publisher123", "publishes": []any{map[string]any{
				"topic": "room/out", "qos": 1, "payload_base64": base64.StdEncoding.EncodeToString([]byte("outbound")),
			}},
			"keep_alive_seconds": 30, "max_messages": 0, "timeout_seconds": 30,
		},
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if roles := tokenRequest.URL.Query()["role"]; len(roles) != 1 || roles[0] != "webpubsub.sendToGroup.room/out" {
		t.Fatalf("roles=%v", roles)
	}
	if len(connection.writes) != 3 || connection.writes[0].data[0] != 0x10 || connection.writes[1].data[0] != 0x32 || !bytes.Contains(connection.writes[1].data, []byte("outbound")) || !bytes.Equal(connection.writes[2].data, []byte{0xe0, 0x00}) {
		t.Fatalf("writes=%#v", connection.writes)
	}
	written, readErr := os.ReadFile(responseFile)
	if readErr != nil || len(written) != 0 || !bytes.Contains(result.Output, []byte(`"messages":0`)) || bytes.Contains(result.Output, []byte("private-")) {
		t.Fatalf("output=%s file=%q err=%v", result.Output, written, readErr)
	}
}

func TestAzureWebPubSubMQTT5RejectsMalformedControlAndPropertyPackets(t *testing.T) {
	plan := azureWebPubSubMQTTPlan{
		ProtocolVersion: 5, CleanStart: true,
		Subscriptions: []azureWebPubSubMQTTSubscription{{TopicFilter: "room/in", QoS: 2}},
	}
	capabilities, err := parseAzureWebPubSubMQTTConnAck(azureWebPubSubMQTTPacket{Header: 0x20, Body: []byte{0x00, 0x00, 0x03, 0x21, 0x00, 0x0a}}, plan)
	if err != nil || capabilities.ReceiveMaximum != 10 || capabilities.MaximumQoS != 2 || capabilities.MaximumPacketSize != azureWebPubSubMQTTMaxPacketBytes {
		t.Fatalf("capabilities=%+v err=%v", capabilities, err)
	}
	for _, packet := range []azureWebPubSubMQTTPacket{
		{Header: 0x20, Body: []byte{0x01, 0x00, 0x00}},
		{Header: 0x20, Body: []byte{0x00, 0x00, 0x01, 0x7f}},
		{Header: 0x20, Body: []byte{0x00, 0x00, 0x0a, 0x11, 0, 0, 0, 1, 0x11, 0, 0, 0, 1}},
	} {
		if err := validateAzureWebPubSubMQTTConnAckForPlan(packet, plan); err == nil {
			t.Fatalf("invalid CONNACK accepted: %x", packet.Body)
		}
	}
	if err := validateAzureWebPubSubMQTTSubAckForPlan(azureWebPubSubMQTTPacket{Header: 0x90, Body: []byte{0x00, 0x01, 0x01, 0x7f, 0x02}}, plan); err == nil {
		t.Fatal("SUBACK with unsupported property accepted")
	}
	if _, err := parseAzureWebPubSubMQTTAcknowledgement(azureWebPubSubMQTTPacket{Header: 0x40, Body: []byte{0x00, 0x02, 0x10}}, 4, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := parseAzureWebPubSubMQTTAcknowledgement(azureWebPubSubMQTTPacket{Header: 0x40, Body: []byte{0x00, 0x02, 0x01}}, 4, 5); err == nil {
		t.Fatal("PUBACK with invalid success reason accepted")
	}
	properties := []byte{0x01, 0x01, 0x01, 0x01}
	body := appendMQTTUTF8(nil, "room/in")
	body = append(body, 0x00, 0x07, byte(len(properties)))
	body = append(body, properties...)
	body = append(body, 'x')
	if _, _, _, err := decodeAzureWebPubSubMQTTPublish(plan, azureWebPubSubMQTTPacket{Header: 0x34, Body: body}); err == nil {
		t.Fatal("PUBLISH with duplicate singleton property accepted")
	}
	if err := validateAzureWebPubSubMQTTDisconnect(azureWebPubSubMQTTPacket{Header: 0xe0, Body: []byte{0x00, 0x01, 0x7f}}, 5); err == nil {
		t.Fatal("DISCONNECT with unsupported property accepted")
	}
}

func TestAzureWebPubSubMQTT5ServerDisconnectPreservesReceivedCount(t *testing.T) {
	responseFile := filepath.Join(t.TempDir(), "server-disconnect.ndjson")
	connection := &fakeTencentWebSocketConnection{
		reads:     [][]byte{{0x20, 0x03, 0x00, 0x00, 0x00}, {0x90, 0x04, 0x00, 0x01, 0x00, 0x00}, {0xe0, 0x00}},
		readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageBinary, cloudWebSocketMessageBinary, cloudWebSocketMessageBinary},
	}
	adapter := NewAzureRESTAdapter(AzureRESTConfig{
		Tokens: &staticAzureTokenProvider{token: "private-entra-token"},
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"token":"private-client-token"}`))}, nil
		}),
		WebPubSubMQTTWebSocketDial: func(context.Context, string, http.Header) (cloudWebSocketConnection, error) {
			return connection, nil
		},
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, Mode: ModeRead, AuthScheme: "webpubsub-mqtt-ws", Service: "webpubsub", Operation: "SubscribeMQTT",
		Method: http.MethodGet, URL: "wss://demo.webpubsub.azure.com/clients/mqtt/hubs/chat",
		Body: map[string]any{
			"protocol_version": 5, "client_id": "Observer123",
			"subscriptions":      []any{map[string]any{"topic_filter": "room/in", "qos": 0}},
			"keep_alive_seconds": 30, "max_messages": 2, "timeout_seconds": 30,
		},
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(result.Output, []byte(`"messages":0`)) {
		t.Fatalf("result=%+v", result)
	}
}

func TestAzureWebPubSubMQTT5HonorsBrokerReceiveMaximum(t *testing.T) {
	responseFile := filepath.Join(t.TempDir(), "flow-control.ndjson")
	connection := &azureWebPubSubMQTTFlowControlConnection{}
	adapter := NewAzureRESTAdapter(AzureRESTConfig{
		Tokens: &staticAzureTokenProvider{token: "private-entra-token"},
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"token":"private-client-token"}`))}, nil
		}),
		WebPubSubMQTTWebSocketDial: func(context.Context, string, http.Header) (cloudWebSocketConnection, error) {
			return connection, nil
		},
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, Mode: ModeMutate, AuthScheme: "webpubsub-mqtt-ws", Service: "webpubsub", Operation: "ClientMQTT",
		Method: http.MethodGet, URL: "wss://demo.webpubsub.azure.com/clients/mqtt/hubs/chat",
		Body: map[string]any{
			"protocol_version": 5, "client_id": "Publisher123",
			"publishes": []any{
				map[string]any{"topic": "room/one", "qos": 1, "payload_base64": base64.StdEncoding.EncodeToString([]byte("one"))},
				map[string]any{"topic": "room/two", "qos": 1, "payload_base64": base64.StdEncoding.EncodeToString([]byte("two"))},
			},
			"keep_alive_seconds": 30, "max_messages": 0, "timeout_seconds": 30,
		},
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if connection.publishWrites != 2 || !connection.firstAcknowledgementRead {
		t.Fatalf("publishWrites=%d firstAck=%t", connection.publishWrites, connection.firstAcknowledgementRead)
	}
}

func TestAzureWebPubSubMQTT5ParsesDocumentedControlAndPublishProperties(t *testing.T) {
	properties := []byte{0x11, 0, 0, 0, 30}
	properties = append(properties, 0x12)
	properties = appendMQTTUTF8(properties, "Assigned123")
	properties = append(properties, 0x13, 0, 20, 0x15)
	properties = appendMQTTUTF8(properties, "method")
	properties = append(properties, 0x16)
	properties = appendMQTTBinary(properties, []byte("auth"))
	properties = append(properties, 0x1a)
	properties = appendMQTTUTF8(properties, "response")
	properties = append(properties, 0x1c)
	properties = appendMQTTUTF8(properties, "server")
	properties = append(properties, 0x1f)
	properties = appendMQTTUTF8(properties, "ready")
	properties = append(properties, 0x21, 0, 2, 0x22, 0, 0, 0x24, 1, 0x25, 0, 0x26)
	properties = appendMQTTUTF8(properties, "key")
	properties = appendMQTTUTF8(properties, "value")
	properties = append(properties, 0x26)
	properties = appendMQTTUTF8(properties, "key2")
	properties = appendMQTTUTF8(properties, "value2")
	properties = append(properties, 0x27, 0, 0, 0x10, 0x00, 0x28, 0, 0x29, 1, 0x2a, 0)
	body := []byte{0x00, 0x00}
	body = appendMQTTVariableByteInteger(body, len(properties))
	body = append(body, properties...)
	capabilities, err := parseAzureWebPubSubMQTTConnAck(azureWebPubSubMQTTPacket{Header: 0x20, Body: body}, azureWebPubSubMQTTPlan{ProtocolVersion: 5})
	if err != nil || capabilities.ReceiveMaximum != 2 || capabilities.MaximumPacketSize != 4096 || capabilities.MaximumQoS != 1 || capabilities.ServerKeepAlive == nil || *capabilities.ServerKeepAlive != 20 {
		t.Fatalf("capabilities=%+v err=%v", capabilities, err)
	}

	publishProperties := []byte{0x01, 1, 0x02, 0, 0, 0, 30, 0x03}
	publishProperties = appendMQTTUTF8(publishProperties, "text/plain")
	publishProperties = append(publishProperties, 0x08)
	publishProperties = appendMQTTUTF8(publishProperties, "room/reply")
	publishProperties = append(publishProperties, 0x09)
	publishProperties = appendMQTTBinary(publishProperties, []byte("correlation"))
	publishProperties = append(publishProperties, 0x0b, 7, 0x26)
	publishProperties = appendMQTTUTF8(publishProperties, "trace")
	publishProperties = appendMQTTUTF8(publishProperties, "safe")
	encoded := appendMQTTVariableByteInteger(nil, len(publishProperties))
	encoded = append(encoded, publishProperties...)
	consumed, metadata, err := parseAzureWebPubSubMQTT5PublishProperties(encoded)
	if err != nil || consumed != len(encoded) || metadata["payload_format"] != 1 || metadata["content_type"] != "text/plain" || metadata["message_expiry_seconds"] != uint32(30) || !slices.Equal(metadata["subscription_ids"].([]int), []int{7}) {
		t.Fatalf("consumed=%d metadata=%#v err=%v", consumed, metadata, err)
	}
}

func TestAzureWebPubSubMQTT5DoesNotRequeueCompletedQoS2Duplicate(t *testing.T) {
	sink, err := newWebSocketOutputSink(Invocation{}, 4096, "Azure Web PubSub MQTT WebSocket")
	if err != nil {
		t.Fatal(err)
	}
	connection := &fakeTencentWebSocketConnection{}
	pending := make(map[uint16][]byte)
	completed := map[uint16]bool{7: true}
	packetBytes := testAzureWebPubSubMQTT5Publish("room/in", 2, 7, []byte("duplicate"), true)
	packet, _, complete, err := decodeAzureWebPubSubMQTTPacket(packetBytes)
	if err != nil || !complete {
		t.Fatalf("complete=%t err=%v", complete, err)
	}
	delivered, err := acceptAzureWebPubSubMQTTPublish(t.Context(), connection, sink, azureWebPubSubMQTTPlan{ProtocolVersion: 5}, packet, pending, completed)
	if err != nil || delivered || len(pending) != 0 || len(connection.writes) != 1 || !bytes.Equal(connection.writes[0].data, []byte{0x50, 0x02, 0x00, 0x07}) {
		t.Fatalf("delivered=%t pending=%v writes=%#v err=%v", delivered, pending, connection.writes, err)
	}
}

func testAzureWebPubSubMQTT5Publish(topic string, qos byte, packetID uint16, payload []byte, duplicate bool) []byte {
	body := appendMQTTUTF8(nil, topic)
	if qos > 0 {
		body = append(body, byte(packetID>>8), byte(packetID))
	}
	// Content Type "text/plain" and Subscription Identifier 7.
	properties := append([]byte{0x03}, appendMQTTUTF8(nil, "text/plain")...)
	properties = append(properties, 0x0b, 0x07)
	body = append(body, byte(len(properties)))
	body = append(body, properties...)
	body = append(body, payload...)
	header := byte(0x30) | qos<<1
	if duplicate {
		header |= 0x08
	}
	packet, _ := encodeAzureWebPubSubMQTTPacket(header, body)
	return packet
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

type azureWebPubSubMQTTFlowControlConnection struct {
	reads                    int
	publishWrites            int
	firstAcknowledgementRead bool
}

func (connection *azureWebPubSubMQTTFlowControlConnection) Read(context.Context) (cloudWebSocketMessageType, []byte, error) {
	connection.reads++
	switch connection.reads {
	case 1:
		// MQTT 5 CONNACK with Receive Maximum = 1.
		return cloudWebSocketMessageBinary, []byte{0x20, 0x06, 0x00, 0x00, 0x03, 0x21, 0x00, 0x01}, nil
	case 2:
		connection.firstAcknowledgementRead = true
		return cloudWebSocketMessageBinary, []byte{0x40, 0x02, 0x00, 0x02}, nil
	case 3:
		return cloudWebSocketMessageBinary, []byte{0x40, 0x02, 0x00, 0x03}, nil
	default:
		return 0, nil, io.EOF
	}
}

func (connection *azureWebPubSubMQTTFlowControlConnection) Write(_ context.Context, _ cloudWebSocketMessageType, data []byte) error {
	if len(data) > 0 && data[0]>>4 == 3 {
		connection.publishWrites++
		if connection.publishWrites > 1 && !connection.firstAcknowledgementRead {
			return io.ErrClosedPipe
		}
	}
	return nil
}

func (*azureWebPubSubMQTTFlowControlConnection) Close() error { return nil }

func cloneJSONMap(t *testing.T, source map[string]any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var clone map[string]any
	if err := json.Unmarshal(encoded, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

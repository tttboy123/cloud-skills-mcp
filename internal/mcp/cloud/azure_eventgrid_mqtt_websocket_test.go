package cloud

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAzureEventGridMQTTInvocationSeparatesReadAndMutation(t *testing.T) {
	body := map[string]any{
		"client_id": "worker-1", "username": "workload-app", "clean_start": true,
		"subscriptions":           []any{map[string]any{"topic_filter": "$share/processors/orders/+", "qos": 1}},
		"subscription_identifier": 7, "receive_maximum": 8, "maximum_packet_size": 65536,
		"topic_alias_maximum": 10, "keep_alive_seconds": 30, "max_messages": 2, "timeout_seconds": 30,
	}
	read := Invocation{
		Provider: ProviderAzure, Mode: ModeRead, AuthScheme: "eventgrid-mqtt-ws", Service: "eventgrid", Operation: "SubscribeMQTT",
		Method: http.MethodGet, URL: "wss://sample.westus2.eventgrid.azure.net/mqtt", Body: body,
		ResponseFile: filepath.Join(t.TempDir(), "events.ndjson"), MaxResponseFileBytes: 4096,
	}
	if !classifyRead(ProviderAzure, read) {
		t.Fatal("Event Grid subscription was not classified read-only")
	}
	if err := validateAzureEventGridMQTTInvocation(read, nil); err != nil {
		t.Fatal(err)
	}
	mutation := read
	mutation.Mode = ModeMutate
	mutation.Operation = "ClientMQTT"
	mutation.Body = map[string]any{
		"client_id": "worker-1", "username": "workload-app", "clean_start": false, "session_expiry_seconds": 3600,
		"publishes": []any{map[string]any{
			"topic": "orders/42", "qos": 1, "retain": true,
			"payload_base64": base64.StdEncoding.EncodeToString([]byte("created")), "payload_format": 1,
			"content_type": "text/plain", "message_expiry_seconds": 60, "response_topic": "orders/42/reply",
			"correlation_data_base64": base64.StdEncoding.EncodeToString([]byte("request-42")),
			"user_properties":         map[string]any{"kind": "created"}, "topic_alias": 1,
		}},
		"keep_alive_seconds": 30, "max_messages": 0, "timeout_seconds": 30,
	}
	if classifyRead(ProviderAzure, mutation) {
		t.Fatal("Event Grid publisher was classified read-only")
	}
	if err := validateAzureEventGridMQTTInvocation(mutation, nil); err != nil {
		t.Fatal(err)
	}

	for name, mutate := range map[string]func(*Invocation){
		"caller auth header": func(value *Invocation) { value.Headers = map[string]string{"Authorization": "Bearer caller"} },
		"inline query":       func(value *Invocation) { value.URL += "?access_token=caller" },
		"wrong path":         func(value *Invocation) { value.URL = "wss://sample.westus2.eventgrid.azure.net/other" },
		"wrong provider host": func(value *Invocation) {
			value.URL = "wss://sample.example.com/mqtt"
		},
		"qos 2": func(value *Invocation) {
			value.Body.(map[string]any)["publishes"].([]any)[0].(map[string]any)["qos"] = 2
		},
		"credential field": func(value *Invocation) { value.Body.(map[string]any)["access_token"] = "caller" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := mutation
			encoded, _ := json.Marshal(mutation.Body)
			var cloned map[string]any
			_ = json.Unmarshal(encoded, &cloned)
			candidate.Body = cloned
			mutate(&candidate)
			if err := validateAzureEventGridMQTTInvocation(candidate, nil); err == nil {
				t.Fatal("unsafe Event Grid MQTT invocation accepted")
			}
		})
	}
}

func TestAzureEventGridMQTTTargetAllowsOnlyOfficialOrOperatorPinnedHosts(t *testing.T) {
	for _, target := range []string{
		"wss://sample.westus2.eventgrid.azure.net/mqtt",
		"wss://sample.southeastasia.eventgrid.azure.net/mqtt",
	} {
		if _, err := parseAzureEventGridMQTTTarget(target, nil); err != nil {
			t.Fatalf("%s: %v", target, err)
		}
	}
	if _, err := parseAzureEventGridMQTTTarget("wss://mqtt.contoso.example/mqtt", []string{"mqtt.contoso.example"}); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{
		"ws://sample.westus2.eventgrid.azure.net/mqtt", "wss://eventgrid.azure.net/mqtt",
		"wss://sample.westus2.eventgrid.azure.net:444/mqtt", "wss://sample.westus2.eventgrid.azure.net/mqtt?sig=x",
	} {
		if _, err := parseAzureEventGridMQTTTarget(target, nil); err == nil {
			t.Fatalf("unsafe target accepted: %s", target)
		}
	}
}

func TestAzureEventGridMQTTEncodesEntraConnectAuthAndReauthentication(t *testing.T) {
	plan := azureEventGridMQTTPlan{
		ClientID: "worker-1", Username: "workload-app", CleanStart: false, SessionExpirySeconds: 3600,
		ReceiveMaximum: 8, MaximumPacketSize: 65536, TopicAliasMaximum: 10, KeepAliveSeconds: 30,
		Will: &azureEventGridMQTTWill{azureEventGridMQTTPublish: azureEventGridMQTTPublish{
			Topic: "workers/status", QoS: 1, Retain: true, Payload: []byte("offline"), PayloadFormat: 1,
		}},
	}
	connect, err := encodeAzureEventGridMQTTConnect(plan, "private-entra-jwt")
	if err != nil {
		t.Fatal(err)
	}
	packet, _, complete, err := decodeAzureWebPubSubMQTTPacket(connect)
	if err != nil || !complete || packet.Header != 0x10 || packet.Body[6] != 5 || packet.Body[7]&0x80 == 0 || packet.Body[7]&0x04 == 0 || packet.Body[7]&0x20 == 0 || !bytes.Contains(packet.Body, []byte("OAUTH2-JWT")) || !bytes.Contains(packet.Body, []byte("private-entra-jwt")) || !bytes.Contains(packet.Body, []byte("workload-app")) {
		t.Fatalf("CONNECT=%x complete=%t err=%v", connect, complete, err)
	}
	auth, err := encodeAzureEventGridMQTTReauthenticate("refreshed-private-jwt")
	if err != nil {
		t.Fatal(err)
	}
	authPacket, _, complete, err := decodeAzureWebPubSubMQTTPacket(auth)
	if err != nil || !complete || authPacket.Header != 0xf0 || len(authPacket.Body) < 2 || authPacket.Body[0] != 0x19 || !bytes.Contains(authPacket.Body, []byte("OAUTH2-JWT")) || !bytes.Contains(authPacket.Body, []byte("refreshed-private-jwt")) {
		t.Fatalf("AUTH=%x complete=%t err=%v", auth, complete, err)
	}
}

func TestAzureEventGridMQTTInvokesWithInternalEntraJWTAndAtomicOutput(t *testing.T) {
	responseFile := filepath.Join(t.TempDir(), "eventgrid.ndjson")
	inbound := testAzureEventGridMQTTPublish("orders/42", 1, 9, []byte("accepted"), map[string]any{
		"payload_format": 1, "content_type": "text/plain", "response_topic": "orders/42/reply",
		"correlation_data": []byte("corr"), "user_properties": map[string]string{"kind": "accepted"}, "subscription_id": 7,
	})
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			{0x20, 0x06, 0x00, 0x00, 0x03, 0x22, 0x00, 0x0a},
			{0x90, 0x04, 0x00, 0x01, 0x00, 0x01},
			{0x40, 0x02, 0x00, 0x02},
			inbound,
		},
		readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageBinary, cloudWebSocketMessageBinary, cloudWebSocketMessageBinary, cloudWebSocketMessageBinary},
	}
	var requestedScope, dialTarget string
	var dialHeaders http.Header
	adapter := NewAzureRESTAdapter(AzureRESTConfig{
		Tokens: azureTokenProviderFunc(func(_ context.Context, scope string) (string, error) {
			requestedScope = scope
			return "private-entra-jwt", nil
		}),
		EventGridMQTTWebSocketDial: func(_ context.Context, target string, headers http.Header) (cloudWebSocketConnection, error) {
			dialTarget, dialHeaders = target, headers.Clone()
			return connection, nil
		},
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, Mode: ModeMutate, AuthScheme: "eventgrid-mqtt-ws", Service: "eventgrid", Operation: "ClientMQTT",
		Method: http.MethodGet, URL: "wss://sample.westus2.eventgrid.azure.net/mqtt",
		Body: map[string]any{
			"client_id": "worker-1", "username": "workload-app", "clean_start": true,
			"subscriptions": []any{map[string]any{"topic_filter": "orders/+", "qos": 1}}, "subscription_identifier": 7,
			"publishes":          []any{map[string]any{"topic": "orders/42", "qos": 1, "payload_base64": base64.StdEncoding.EncodeToString([]byte("created"))}},
			"keep_alive_seconds": 30, "max_messages": 1, "timeout_seconds": 30,
		},
		ResponseFile: responseFile, MaxResponseFileBytes: 8192,
	})
	if err != nil {
		t.Fatal(err)
	}
	if requestedScope != "https://eventgrid.azure.net/.default" || dialTarget != "wss://sample.westus2.eventgrid.azure.net/mqtt" || len(dialHeaders) != 0 {
		t.Fatalf("scope=%q target=%q headers=%v", requestedScope, dialTarget, dialHeaders)
	}
	if len(connection.writes) < 4 || !bytes.Contains(connection.writes[0].data, []byte("private-entra-jwt")) || bytes.Contains(connection.writes[1].data, []byte("private-entra-jwt")) {
		t.Fatalf("unexpected wire frames: %x", connection.writes)
	}
	data, err := os.ReadFile(responseFile)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("private-entra-jwt")) || bytes.Contains(result.Output, []byte("private-entra-jwt")) || !bytes.Contains(data, []byte(`"response_topic":"orders/42/reply"`)) || !bytes.Contains(data, []byte(`"correlation_data_base64":"Y29ycg=="`)) || !bytes.Contains(data, []byte(`"user_properties":{"kind":"accepted"}`)) {
		t.Fatalf("unsafe or incomplete output: result=%s file=%s", result.Output, data)
	}
	if info, _ := os.Stat(responseFile); info == nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v", info)
	}
}

func TestAzureEventGridMQTTMalformedBrokerPacketLeavesNoOutput(t *testing.T) {
	responseFile := filepath.Join(t.TempDir(), "eventgrid.ndjson")
	connection := &fakeTencentWebSocketConnection{
		reads:     [][]byte{{0x20, 0x03, 0x00, 0x00, 0x00}, {0x90, 0x04, 0x00, 0x01, 0x00, 0x01}, {0x30, 0x03, 0x00, 0x05, 'x'}},
		readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageBinary, cloudWebSocketMessageBinary, cloudWebSocketMessageBinary},
	}
	adapter := NewAzureRESTAdapter(AzureRESTConfig{
		Tokens:                     &staticAzureTokenProvider{token: "private-entra-jwt"},
		EventGridMQTTWebSocketDial: func(context.Context, string, http.Header) (cloudWebSocketConnection, error) { return connection, nil },
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, Mode: ModeRead, AuthScheme: "eventgrid-mqtt-ws", Service: "eventgrid", Operation: "SubscribeMQTT",
		Method: http.MethodGet, URL: "wss://sample.westus2.eventgrid.azure.net/mqtt",
		Body:         map[string]any{"client_id": "worker-1", "username": "workload-app", "subscriptions": []any{map[string]any{"topic_filter": "orders/+", "qos": 1}}, "keep_alive_seconds": 30, "max_messages": 1, "timeout_seconds": 30},
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err == nil || !strings.Contains(err.Error(), "PUBLISH") {
		t.Fatalf("err=%v", err)
	}
	if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
		t.Fatalf("partial output exists: %v", statErr)
	}
}

func TestAzureEventGridMQTTParsesAssignedClientAndBrokerCapabilities(t *testing.T) {
	properties := []byte{0x12}
	properties = appendMQTTUTF8(properties, "assigned-42")
	properties = append(properties, 0x13, 0x00, 0x3c, 0x21, 0x00, 0x08, 0x22, 0x00, 0x0a, 0x24, 0x01, 0x27, 0x00, 0x01, 0x00, 0x00)
	properties = append(properties, 0x11, 0x00, 0x00, 0x0e, 0x10, 0x15)
	properties = appendMQTTUTF8(properties, azureEventGridMQTTAuthMethod)
	properties = append(properties, 0x16)
	properties = appendMQTTBinary(properties, []byte("challenge"))
	properties = append(properties, 0x25, 0x01, 0x26)
	properties = appendMQTTUTF8(properties, "server")
	properties = appendMQTTUTF8(properties, "eventgrid")
	body := []byte{0x00, 0x00}
	body = appendMQTTVariableByteInteger(body, len(properties))
	body = append(body, properties...)
	capabilities, err := parseAzureEventGridMQTTConnAck(azureWebPubSubMQTTPacket{Header: 0x20, Body: body}, azureEventGridMQTTPlan{CleanStart: true})
	if err != nil {
		t.Fatal(err)
	}
	if capabilities.AssignedClientID != "assigned-42" || capabilities.ReceiveMaximum != 8 || capabilities.MaximumPacketSize != 65536 || capabilities.MaximumQoS != 1 || capabilities.TopicAliasMaximum != 10 || capabilities.ServerKeepAlive == nil || *capabilities.ServerKeepAlive != 60 {
		t.Fatalf("capabilities=%+v", capabilities)
	}
	for name, packet := range map[string]azureWebPubSubMQTTPacket{
		"rejected":       {Header: 0x20, Body: []byte{0, 0x87, 0}},
		"resumed clean":  {Header: 0x20, Body: []byte{1, 0, 0}},
		"missing assign": {Header: 0x20, Body: []byte{0, 0, 0}},
		"unknown prop":   {Header: 0x20, Body: []byte{0, 0, 1, 0xff}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseAzureEventGridMQTTConnAck(packet, azureEventGridMQTTPlan{CleanStart: true}); err == nil {
				t.Fatal("malformed CONNACK accepted")
			}
		})
	}
}

func TestAzureEventGridMQTTValidatesAuthPacketAndReaderBounds(t *testing.T) {
	if err := validateAzureEventGridMQTTAuth(azureWebPubSubMQTTPacket{Header: 0xf0, Body: []byte{0, 0}}); err != nil {
		t.Fatal(err)
	}
	for _, packet := range []azureWebPubSubMQTTPacket{
		{Header: 0xe0, Body: []byte{0, 0}}, {Header: 0xf0, Body: []byte{0x18, 0}}, {Header: 0xf0, Body: []byte{0, 1, 0xff}},
	} {
		if err := validateAzureEventGridMQTTAuth(packet); err == nil {
			t.Fatal("invalid AUTH accepted")
		}
	}
	connection := &fakeTencentWebSocketConnection{
		reads:     [][]byte{{0x30}, {0x03, 0x00, 0x00, 0x00}},
		readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageBinary, cloudWebSocketMessageBinary},
	}
	reader := &azureEventGridMQTTPacketReader{connection: connection, maximum: 4}
	if _, err := reader.Read(t.Context()); err == nil || !strings.Contains(err.Error(), "maximum packet") {
		t.Fatalf("err=%v", err)
	}
	nonBinary := &azureEventGridMQTTPacketReader{connection: &fakeTencentWebSocketConnection{reads: [][]byte{{0x30, 0x00}}, readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageText}}}
	if _, err := nonBinary.Read(t.Context()); err == nil {
		t.Fatal("text WebSocket frame accepted")
	}
}

func TestAzureEventGridMQTTPlanCoversWillAliasesAndFailureBounds(t *testing.T) {
	base := map[string]any{
		"client_id": "worker-1", "username": "workload-app", "clean_start": false, "session_expiry_seconds": 3600,
		"publishes": []any{
			map[string]any{"topic": "orders/42", "qos": 1, "payload_base64": "YQ==", "topic_alias": 1},
			map[string]any{"topic": "", "qos": 0, "payload_base64": "Yg==", "topic_alias": 1},
		},
		"will":               map[string]any{"topic": "workers/status", "qos": 1, "retain": true, "payload_base64": "b2ZmbGluZQ==", "payload_format": 1, "delay_seconds": 10},
		"keep_alive_seconds": 30, "max_messages": 0, "timeout_seconds": 30,
	}
	plan, err := parseAzureEventGridMQTTPlan(base, true)
	if err != nil || plan.Will == nil || !plan.Will.Retain || len(plan.Will.Payload) == 0 || plan.Publishes[1].Topic != "" {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	for name, mutate := range map[string]func(map[string]any){
		"assigned persistent": func(body map[string]any) { body["client_id"] = "" },
		"bad username":        func(body map[string]any) { body["username"] = strings.Repeat("x", 129) },
		"expiry":              func(body map[string]any) { body["session_expiry_seconds"] = 28801 },
		"unknown will":        func(body map[string]any) { body["will"].(map[string]any)["password"] = "x" },
		"bad alias reuse":     func(body map[string]any) { body["publishes"].([]any)[1].(map[string]any)["topic_alias"] = 2 },
		"bad correlation": func(body map[string]any) {
			body["publishes"].([]any)[0].(map[string]any)["correlation_data_base64"] = "%%%"
		},
		"bad filter": func(body map[string]any) {
			body["subscriptions"] = []any{map[string]any{"topic_filter": "orders/#/bad", "qos": 1}}
		},
		"bad reauth": func(body map[string]any) { body["reauthenticate_after_seconds"] = 31 },
	} {
		t.Run(name, func(t *testing.T) {
			encoded, _ := json.Marshal(base)
			var candidate map[string]any
			_ = json.Unmarshal(encoded, &candidate)
			mutate(candidate)
			if _, err := parseAzureEventGridMQTTPlan(candidate, true); err == nil {
				t.Fatal("invalid plan accepted")
			}
		})
	}
	if _, err := encodeAzureEventGridMQTTPacket(0x30, make([]byte, azureEventGridMQTTMaxPacketBytes)); err == nil {
		t.Fatal("oversized packet accepted")
	}
	if _, err := encodeAzureEventGridMQTTReauthenticate(""); err == nil {
		t.Fatal("empty refresh token accepted")
	}
}

type azureTokenProviderFunc func(context.Context, string) (string, error)

func (fn azureTokenProviderFunc) Token(ctx context.Context, scope string) (string, error) {
	return fn(ctx, scope)
}

func testAzureEventGridMQTTPublish(topic string, qos byte, packetID uint16, payload []byte, metadata map[string]any) []byte {
	body := appendMQTTUTF8(nil, topic)
	if qos > 0 {
		body = append(body, byte(packetID>>8), byte(packetID))
	}
	properties := make([]byte, 0)
	if value, ok := metadata["payload_format"].(int); ok {
		properties = append(properties, 0x01, byte(value))
	}
	if value, ok := metadata["content_type"].(string); ok {
		properties = append(properties, 0x03)
		properties = appendMQTTUTF8(properties, value)
	}
	if value, ok := metadata["response_topic"].(string); ok {
		properties = append(properties, 0x08)
		properties = appendMQTTUTF8(properties, value)
	}
	if value, ok := metadata["correlation_data"].([]byte); ok {
		properties = append(properties, 0x09)
		properties = appendMQTTBinary(properties, value)
	}
	if values, ok := metadata["user_properties"].(map[string]string); ok {
		for key, value := range values {
			properties = append(properties, 0x26)
			properties = appendMQTTUTF8(properties, key)
			properties = appendMQTTUTF8(properties, value)
		}
	}
	if value, ok := metadata["subscription_id"].(int); ok {
		properties = append(properties, 0x0b)
		properties = appendMQTTVariableByteInteger(properties, value)
	}
	body = appendMQTTVariableByteInteger(body, len(properties))
	body = append(body, properties...)
	body = append(body, payload...)
	packet, _ := encodeAzureWebPubSubMQTTPacket(0x30|qos<<1, body)
	return packet
}

func FuzzAzureEventGridMQTTPacketAndPropertiesNeverPanic(f *testing.F) {
	f.Add([]byte{0x30, 0x00})
	f.Add([]byte{0x20, 0x03, 0x00, 0x00, 0x00})
	f.Add(testAzureEventGridMQTTPublish("events/one", 1, 1, []byte("payload"), map[string]any{"subscription_id": 1}))
	f.Fuzz(func(t *testing.T, data []byte) {
		packet, _, complete, _ := decodeAzureEventGridMQTTPacket(data)
		if !complete {
			return
		}
		_, _, _, _ = decodeAzureEventGridMQTTPublish(packet)
		_, _ = parseAzureEventGridMQTTConnAck(packet, azureEventGridMQTTPlan{ClientID: "client", CleanStart: true})
		_ = validateAzureEventGridMQTTAuth(packet)
	})
}

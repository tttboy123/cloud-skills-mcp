package cloud

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	coderwebsocket "github.com/coder/websocket"
)

func TestAzureWebPubSubMQTTBoundaryAllowsOnlyFiniteReadSubscriptions(t *testing.T) {
	valid := func() Invocation {
		return Invocation{
			Provider: ProviderAzure, Mode: ModeRead, AuthScheme: "webpubsub-mqtt-ws", Service: "webpubsub", Operation: "SubscribeMQTT",
			Method: http.MethodGet, URL: "wss://demo.webpubsub.azure.com/clients/mqtt/hubs/chat",
			Body: map[string]any{
				"client_id": "Observer123", "subscriptions": []any{map[string]any{"topic_filter": "room/temperature", "qos": 1}},
				"keep_alive_seconds": 30, "max_messages": 2, "timeout_seconds": 30,
			},
			ResponseFile: filepath.Join(t.TempDir(), "mqtt.ndjson"), MaxResponseFileBytes: 4096,
		}
	}
	base := valid()
	if !classifyRead(ProviderAzure, base) {
		t.Fatal("finite Azure MQTT subscription was not classified read-only")
	}
	if err := validateAzureWebPubSubMQTTInvocation(base); err != nil {
		t.Fatal(err)
	}
	if err := validateInvocationWithEndpointHosts(base, []string{filepath.Dir(base.ResponseFile)}, nil); err != nil {
		t.Fatalf("gateway rejected finite Azure MQTT subscription: %v", err)
	}
	for name, mutate := range map[string]func(*Invocation){
		"wrong endpoint": func(value *Invocation) { value.URL = "wss://demo.webpubsub.azure.com/client/hubs/chat" },
		"query":          func(value *Invocation) { value.URL += "?access_token=caller" },
		"header":         func(value *Invocation) { value.Headers = map[string]string{"Authorization": "Bearer caller"} },
		"client id":      func(value *Invocation) { value.Body.(map[string]any)["client_id"] = "observer-1" },
		"keepalive low":  func(value *Invocation) { value.Body.(map[string]any)["keep_alive_seconds"] = 0 },
		"keepalive high": func(value *Invocation) { value.Body.(map[string]any)["keep_alive_seconds"] = 181 },
		"wildcard": func(value *Invocation) {
			value.Body.(map[string]any)["subscriptions"] = []any{map[string]any{"topic_filter": "room/#", "qos": 1}}
		},
		"qos3": func(value *Invocation) {
			value.Body.(map[string]any)["subscriptions"] = []any{map[string]any{"topic_filter": "room/temperature", "qos": 3}}
		},
		"credential": func(value *Invocation) { value.Body.(map[string]any)["password"] = "caller" },
		"body file":  func(value *Invocation) { value.BodyFile = filepath.Join(t.TempDir(), "input") },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid()
			mutate(&candidate)
			if err := validateAzureWebPubSubMQTTInvocation(candidate); err == nil {
				t.Fatal("unsafe Azure Web PubSub MQTT invocation accepted")
			}
		})
	}
}

func TestAzureWebPubSubMQTTMintsContainedTokenAndCollectsSubscription(t *testing.T) {
	responseFile := filepath.Join(t.TempDir(), "mqtt.ndjson")
	publish := testMQTTPublishPacket("room/temperature", 1, 7, []byte(`{"value":21}`))
	connection := &fakeTencentWebSocketConnection{
		reads:     [][]byte{{0x20, 0x02, 0x00, 0x00}, {0x90, 0x03, 0x00, 0x01, 0x01}, publish},
		readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageBinary, cloudWebSocketMessageBinary, cloudWebSocketMessageBinary},
	}
	identity := &staticAzureTokenProvider{token: "private-entra-token"}
	var tokenRequest *http.Request
	var dialTarget string
	var dialHeaders http.Header
	adapter := NewAzureRESTAdapter(AzureRESTConfig{
		Tokens: identity,
		HTTP: doerFunc(func(request *http.Request) (*http.Response, error) {
			tokenRequest = request.Clone(request.Context())
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"token":"private-mqtt-token"}`))}, nil
		}),
		WebPubSubMQTTWebSocketDial: func(_ context.Context, target string, headers http.Header) (cloudWebSocketConnection, error) {
			dialTarget, dialHeaders = target, headers.Clone()
			return connection, nil
		},
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, Mode: ModeRead, AuthScheme: "webpubsub-mqtt-ws", Service: "webpubsub", Operation: "SubscribeMQTT",
		Method: http.MethodGet, URL: "wss://demo.webpubsub.azure.com/clients/mqtt/hubs/chat",
		Body: map[string]any{
			"client_id": "Observer123", "subscriptions": []any{map[string]any{"topic_filter": "room/temperature", "qos": 1}},
			"keep_alive_seconds": 30, "max_messages": 1, "timeout_seconds": 30,
		},
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if identity.calls != 1 || identity.scope != "https://webpubsub.azure.com/.default" {
		t.Fatalf("identity calls=%d scope=%q", identity.calls, identity.scope)
	}
	query := tokenRequest.URL.Query()
	if tokenRequest.Method != http.MethodPost || tokenRequest.URL.Path != "/api/hubs/chat/:generateToken" || query.Get("api-version") != "2024-01-01" || query.Get("minutesToExpire") != "5" || query.Get("clientType") != "MQTT" || query.Get("role") != "webpubsub.joinLeaveGroup.room/temperature" || tokenRequest.Header.Get("Authorization") != "Bearer private-entra-token" {
		t.Fatalf("token request=%s headers=%#v", tokenRequest.URL, tokenRequest.Header)
	}
	if dialTarget != "wss://demo.webpubsub.azure.com/clients/mqtt/hubs/chat" || dialHeaders.Get("Authorization") != "Bearer private-mqtt-token" || len(dialHeaders) != 1 {
		t.Fatalf("dial=%q headers=%#v", dialTarget, dialHeaders)
	}
	if len(connection.writes) != 4 || connection.writes[0].messageType != cloudWebSocketMessageBinary || !bytes.Contains(connection.writes[0].data, []byte{'M', 'Q', 'T', 'T'}) || !bytes.Contains(connection.writes[1].data, []byte("room/temperature")) || !bytes.Equal(connection.writes[2].data, []byte{0x40, 0x02, 0x00, 0x07}) || !bytes.Equal(connection.writes[3].data, []byte{0xe0, 0x00}) {
		t.Fatalf("MQTT writes=%#v", connection.writes)
	}
	written, readErr := os.ReadFile(responseFile)
	if readErr != nil || !bytes.Contains(written, []byte(`"topic":"room/temperature"`)) || !bytes.Contains(written, []byte(`"payload_base64":"eyJ2YWx1ZSI6MjF9"`)) || bytes.Contains(written, []byte("private-")) {
		t.Fatalf("output=%s err=%v", written, readErr)
	}
	if result.RequestID != "Observer123" || bytes.Contains(result.Output, []byte("private-")) || !bytes.Contains(result.Output, []byte(`"messages":1`)) {
		t.Fatalf("result=%+v", result)
	}
}

func TestDefaultAzureWebPubSubMQTTDialNegotiatesMQTTSubprotocol(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer client-token" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		connection, err := coderwebsocket.Accept(writer, request, &coderwebsocket.AcceptOptions{Subprotocols: []string{"mqtt"}})
		if err != nil {
			return
		}
		defer connection.Close(coderwebsocket.StatusNormalClosure, "done")
		_ = connection.Write(request.Context(), coderwebsocket.MessageBinary, []byte{0x20, 0x02, 0x00, 0x00})
	}))
	defer server.Close()
	connection, err := defaultAzureWebPubSubMQTTWebSocketDial(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http"), http.Header{"Authorization": []string{"Bearer client-token"}})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	messageType, message, err := connection.Read(t.Context())
	if err != nil || messageType != cloudWebSocketMessageBinary || !bytes.Equal(message, []byte{0x20, 0x02, 0x00, 0x00}) {
		t.Fatalf("type=%d message=%x err=%v", messageType, message, err)
	}
}

func TestAzureWebPubSubMQTTFailureDoesNotPublishPartialOutput(t *testing.T) {
	responseFile := filepath.Join(t.TempDir(), "mqtt.ndjson")
	adapter := NewAzureRESTAdapter(AzureRESTConfig{
		Tokens: &staticAzureTokenProvider{token: "entra-token"},
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"token":"client-token"}`))}, nil
		}),
		WebPubSubMQTTWebSocketDial: func(context.Context, string, http.Header) (cloudWebSocketConnection, error) {
			return &fakeTencentWebSocketConnection{reads: [][]byte{{0x20, 0x02, 0x00, 0x05}}, readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageBinary}}, nil
		},
		StreamPause: func(context.Context, time.Duration) error { return nil },
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, Mode: ModeRead, AuthScheme: "webpubsub-mqtt-ws", Service: "webpubsub", Operation: "SubscribeMQTT",
		Method: http.MethodGet, URL: "wss://demo.webpubsub.azure.com/clients/mqtt/hubs/chat",
		Body: map[string]any{
			"client_id": "Observer123", "subscriptions": []any{map[string]any{"topic_filter": "room", "qos": 0}},
			"keep_alive_seconds": 30, "max_messages": 1, "timeout_seconds": 30,
		},
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err == nil || strings.Contains(err.Error(), "client-token") {
		t.Fatalf("err=%v", err)
	}
	if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
		t.Fatalf("partial output published: %v", statErr)
	}
}

func TestAzureWebPubSubMQTTSendsBoundedKeepAlive(t *testing.T) {
	responseFile := filepath.Join(t.TempDir(), "mqtt.ndjson")
	connection := &azureWebPubSubMQTTKeepAliveConnection{}
	adapter := NewAzureRESTAdapter(AzureRESTConfig{
		Tokens: &staticAzureTokenProvider{token: "entra-token"},
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"token":"client-token"}`))}, nil
		}),
		WebPubSubMQTTWebSocketDial: func(context.Context, string, http.Header) (cloudWebSocketConnection, error) {
			return connection, nil
		},
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, Mode: ModeRead, AuthScheme: "webpubsub-mqtt-ws", Service: "webpubsub", Operation: "SubscribeMQTT",
		Method: http.MethodGet, URL: "wss://demo.webpubsub.azure.com/clients/mqtt/hubs/chat",
		Body: map[string]any{
			"client_id": "Observer123", "subscriptions": []any{map[string]any{"topic_filter": "room", "qos": 0}},
			"keep_alive_seconds": 1, "max_messages": 1, "timeout_seconds": 2,
		},
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	foundPing := false
	for _, write := range connection.writes {
		if bytes.Equal(write, []byte{0xc0, 0x00}) {
			foundPing = true
		}
	}
	if !foundPing {
		t.Fatalf("PINGREQ not sent: %#v", connection.writes)
	}
}

func TestAzureWebPubSubMQTTPacketValidationRejectsMalformedInput(t *testing.T) {
	if _, _, complete, err := decodeAzureWebPubSubMQTTPacket([]byte{0x30}); err != nil || complete {
		t.Fatalf("incomplete packet complete=%t err=%v", complete, err)
	}
	if _, _, _, err := decodeAzureWebPubSubMQTTPacket([]byte{0x30, 0x80, 0x80, 0x80, 0x80}); err == nil {
		t.Fatal("malformed remaining length accepted")
	}
	if _, err := encodeAzureWebPubSubMQTTPacket(0x30, make([]byte, azureWebPubSubMQTTMaxPacketBytes+1)); err == nil {
		t.Fatal("oversized packet encoded")
	}
	if _, err := encodeAzureWebPubSubMQTTPacket(0x30, make([]byte, azureWebPubSubMQTTMaxPacketBytes)); err == nil {
		t.Fatal("packet overhead escaped whole-packet limit")
	}
	if _, _, _, err := decodeAzureWebPubSubMQTTPacket([]byte{0x30, 0x80, 0x00}); err == nil {
		t.Fatal("non-minimal remaining length accepted")
	}
	for _, packet := range []azureWebPubSubMQTTPacket{
		{Header: 0x20, Body: []byte{0x00, 0x05}},
		{Header: 0x20, Body: []byte{0x01, 0x00}},
		{Header: 0x30, Body: nil},
	} {
		if err := validateAzureWebPubSubMQTTConnAck(packet); err == nil {
			t.Fatalf("invalid CONNACK accepted: %#v", packet)
		}
	}
	subscriptions := []azureWebPubSubMQTTSubscription{{TopicFilter: "room", QoS: 0}}
	for _, packet := range []azureWebPubSubMQTTPacket{
		{Header: 0x90, Body: []byte{0x00, 0x02, 0x00}},
		{Header: 0x90, Body: []byte{0x00, 0x01, 0x01}},
		{Header: 0x90, Body: []byte{0x00, 0x01, 0x80}},
	} {
		if err := validateAzureWebPubSubMQTTSubAck(packet, subscriptions); err == nil {
			t.Fatalf("invalid SUBACK accepted: %#v", packet)
		}
	}
	sink, err := newWebSocketOutputSink(Invocation{}, 4096, "Azure Web PubSub MQTT WebSocket")
	if err != nil {
		t.Fatal(err)
	}
	connection := &fakeTencentWebSocketConnection{}
	for _, packet := range []azureWebPubSubMQTTPacket{
		{Header: 0x31, Body: []byte{0x00, 0x01, 'a'}},
		{Header: 0x34, Body: []byte{0x00, 0x01, 'a'}},
		{Header: 0x30, Body: []byte{0x00, 0x02, 'a'}},
		{Header: 0x32, Body: []byte{0x00, 0x01, 'a'}},
		{Header: 0x32, Body: []byte{0x00, 0x01, 'a', 0x00, 0x00}},
	} {
		if err := processAzureWebPubSubMQTTPublish(t.Context(), connection, sink, packet); err == nil {
			t.Fatalf("invalid PUBLISH accepted: %#v", packet)
		}
	}
	reader := &azureWebPubSubMQTTPacketReader{connection: &fakeTencentWebSocketConnection{reads: [][]byte{[]byte("text")}}}
	if _, err := reader.Read(t.Context()); err == nil || !strings.Contains(err.Error(), "non-binary") {
		t.Fatalf("text MQTT frame err=%v", err)
	}
}

type azureWebPubSubMQTTKeepAliveConnection struct {
	reads  int
	writes [][]byte
}

func (connection *azureWebPubSubMQTTKeepAliveConnection) Read(ctx context.Context) (cloudWebSocketMessageType, []byte, error) {
	connection.reads++
	switch connection.reads {
	case 1:
		return cloudWebSocketMessageBinary, []byte{0x20, 0x02, 0x00, 0x00}, nil
	case 2:
		return cloudWebSocketMessageBinary, []byte{0x90, 0x03, 0x00, 0x01, 0x00}, nil
	case 3:
		<-ctx.Done()
		return 0, nil, ctx.Err()
	case 4:
		return cloudWebSocketMessageBinary, []byte{0xd0, 0x00}, nil
	default:
		return cloudWebSocketMessageBinary, testMQTTPublishPacket("room", 0, 0, []byte("ready")), nil
	}
}

func (connection *azureWebPubSubMQTTKeepAliveConnection) Write(_ context.Context, _ cloudWebSocketMessageType, data []byte) error {
	connection.writes = append(connection.writes, append([]byte(nil), data...))
	return nil
}

func (*azureWebPubSubMQTTKeepAliveConnection) Close() error { return nil }

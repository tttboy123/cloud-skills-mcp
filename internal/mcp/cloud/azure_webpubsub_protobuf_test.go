package cloud

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
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

func TestAzureWebPubSubProtobufOfficialUpstreamWireVectors(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		hex  string
		ack  uint64
	}{
		{"join", `{"type":"joinGroup","group":"room","ackId":2}`, "32080a04726f6f6d1002", 2},
		{"leave", `{"type":"leaveGroup","group":"room","ackId":3}`, "3a080a04726f6f6d1003", 3},
		{"send text", `{"type":"sendToGroup","group":"room","ackId":1,"noEcho":true,"dataType":"text","data":"hello"}`, "0a130a04726f6f6d10011a070a0568656c6c6f2001", 1},
		{"event binary", `{"type":"event","event":"notify","ackId":4,"dataType":"binary","data":"AQID"}`, "2a110a066e6f74696679120512030102031804", 4},
		{"ping", `{"type":"ping"}`, "4a00", 0},
		{"stream start", `{"type":"sendToGroup","group":"room","noEcho":true,"stream":{"streamId":"s","idleTimeoutMs":300000}}`, "0a110a04726f6f6d20013a070a017310e0a712", 0},
		{"stream data", `{"type":"streamData","streamId":"s","streamSequenceId":1,"dataType":"text","data":"x"}`, "6a0a0a017310011a030a0178", 0},
		{"stream end", `{"type":"streamEnd","streamId":"s","error":{"message":"done","userErrorCode":"E"}}`, "720e0a017312090a04646f6e65120145", 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded, ackID, err := encodeAzureWebPubSubProtobufClientMessage(json.RawMessage(test.raw))
			if err != nil {
				t.Fatal(err)
			}
			if got := hex.EncodeToString(encoded); got != test.hex || ackID != test.ack {
				t.Fatalf("wire=%s ack=%d, want %s/%d", got, ackID, test.hex, test.ack)
			}
		})
	}
}

func TestAzureWebPubSubProtobufOfficialDownstreamWireVectors(t *testing.T) {
	tests := []struct {
		name       string
		hex        string
		reliable   bool
		kind       string
		sequenceID uint64
		ackID      uint64
		terminal   bool
		contains   []string
	}{
		{"ack", "0a0408011001", false, "ack", 0, 1, false, []string{`"success":true`}},
		{"message", "12160a0567726f75701204726f6f6d1a050a036f6e652001", true, "message", 1, 0, false, []string{`"dataType":"text"`, `"data":"one"`, `"sequenceId":1`}},
		{"connected", "1a100a0e0a01631201751a06736563726574", true, "system", 0, 0, false, []string{`"connectionId":"c"`, `"userId":"u"`, `"reconnectionToken":"secret"`}},
		{"disconnected", "1a0712051203627965", false, "system", 0, 0, true, []string{`"event":"disconnected"`, `"reason":"bye"`}},
		{"pong", "2200", false, "pong", 0, 0, false, nil},
		{"stream ack", "32050a01731002", false, "streamAck", 0, 0, false, []string{`"streamId":"s"`, `"expectedSequenceId":2`}},
		{"stream closed", "42030a0173", false, "streamClosed", 0, 0, false, []string{`"streamId":"s"`}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wire, err := hex.DecodeString(test.hex)
			if err != nil {
				t.Fatal(err)
			}
			message, kind, sequenceID, ackID, terminal, err := parseAzureWebPubSubProtobufServerMessage(cloudWebSocketMessageBinary, wire, test.reliable)
			if err != nil {
				t.Fatal(err)
			}
			if kind != test.kind || sequenceID != test.sequenceID || ackID != test.ackID || terminal != test.terminal {
				t.Fatalf("kind=%q sequence=%d ack=%d terminal=%v", kind, sequenceID, ackID, terminal)
			}
			for _, expected := range test.contains {
				if !bytes.Contains(message, []byte(expected)) {
					t.Fatalf("message=%s missing %s", message, expected)
				}
			}
		})
	}
}

func TestAzureWebPubSubProtobufRejectsMalformedAndUnsafeMessages(t *testing.T) {
	for _, raw := range []string{
		`{"type":"sendToGroup","group":"room","dataType":"binary","data":"***"}`,
		`{"type":"event","event":"notify","dataType":"protobuf","data":{"typeUrl":"bad","value":"AQI="}}`,
		`{"type":"streamData","streamId":"s","streamSequenceId":0,"dataType":"text","data":"x"}`,
		`{"type":"streamEnd","streamId":"s","error":{"client_secret":"leak"}}`,
	} {
		if _, _, err := encodeAzureWebPubSubProtobufClientMessage(json.RawMessage(raw)); err == nil {
			t.Fatalf("invalid protobuf request accepted: %s", raw)
		}
	}
	for _, wire := range [][]byte{{}, {0x08, 0x01}, {0x0a, 0x02, 0x08}, {0x22, 0x00, 0x32, 0x00}} {
		if _, _, _, _, _, err := parseAzureWebPubSubProtobufServerMessage(cloudWebSocketMessageBinary, wire, false); err == nil {
			t.Fatalf("malformed protobuf response accepted: %x", wire)
		}
	}
	if _, _, _, _, _, err := parseAzureWebPubSubProtobufServerMessage(cloudWebSocketMessageText, []byte{0x22, 0x00}, false); err == nil {
		t.Fatal("text frame accepted for protobuf subprotocol")
	}
}

func TestAzureWebPubSubProtobufAnyBinaryAndStreamMetadata(t *testing.T) {
	anyRequest := json.RawMessage(`{"type":"sendToGroup","group":"room","ackId":18446744073709551615,"dataType":"protobuf","data":{"typeUrl":"type.googleapis.com/azure.webpubsub.TestMessage","value":"CAE="}}`)
	encoded, ackID, err := encodeAzureWebPubSubProtobufClientMessage(anyRequest)
	if err != nil || ackID != ^uint64(0) || !bytes.Contains(encoded, []byte("type.googleapis.com/azure.webpubsub.TestMessage")) {
		t.Fatalf("encoded=%x ack=%d err=%v", encoded, ackID, err)
	}
	if _, _, err := encodeAzureWebPubSubProtobufClientMessage(json.RawMessage(`{"type":"event","event":"notify","dataType":"protobuf","data":{"typeUrl":"https://schemas.example.test/example.Message","value":""}}`)); err != nil {
		t.Fatalf("custom Any type URL rejected: %v", err)
	}

	anyPayload := azureWebPubSubProtobufAppendString(nil, 1, "type.googleapis.com/azure.webpubsub.TestMessage")
	anyPayload = azureWebPubSubProtobufAppendBytes(anyPayload, 2, []byte{0x08, 0x01})
	messageData := azureWebPubSubProtobufAppendBytes(nil, 3, anyPayload)
	dataMessage := azureWebPubSubProtobufAppendString(nil, 1, "server")
	dataMessage = azureWebPubSubProtobufAppendBytes(dataMessage, 3, messageData)
	downstream := azureWebPubSubProtobufAppendBytes(nil, 2, dataMessage)
	canonical, kind, _, _, _, err := parseAzureWebPubSubProtobufServerMessage(cloudWebSocketMessageBinary, downstream, false)
	if err != nil || kind != "message" || !bytes.Contains(canonical, []byte(`"dataType":"protobuf"`)) || !bytes.Contains(canonical, []byte(`"typeUrl":"type.googleapis.com/azure.webpubsub.TestMessage"`)) || !bytes.Contains(canonical, []byte(`"value":"CAE="`)) {
		t.Fatalf("message=%s kind=%q err=%v", canonical, kind, err)
	}

	binaryData := azureWebPubSubProtobufAppendBytes(nil, 2, []byte{1, 2, 3})
	binaryMessage := azureWebPubSubProtobufAppendString(nil, 1, "server")
	binaryMessage = azureWebPubSubProtobufAppendBytes(binaryMessage, 3, binaryData)
	canonical, _, _, _, _, err = parseAzureWebPubSubProtobufServerMessage(cloudWebSocketMessageBinary, azureWebPubSubProtobufAppendBytes(nil, 2, binaryMessage), false)
	if err != nil || !bytes.Contains(canonical, []byte(`"dataType":"binary"`)) || !bytes.Contains(canonical, []byte(`"data":"AQID"`)) {
		t.Fatalf("binary=%s err=%v", canonical, err)
	}
	jsonData := azureWebPubSubProtobufAppendString(nil, 4, `{"ok":true}`)
	jsonMessage := azureWebPubSubProtobufAppendString(nil, 1, "server")
	jsonMessage = azureWebPubSubProtobufAppendBytes(jsonMessage, 3, jsonData)
	canonical, _, _, _, _, err = parseAzureWebPubSubProtobufServerMessage(cloudWebSocketMessageBinary, azureWebPubSubProtobufAppendBytes(nil, 2, jsonMessage), false)
	if err != nil || !bytes.Contains(canonical, []byte(`"dataType":"json"`)) || !bytes.Contains(canonical, []byte(`"data":{"ok":true}`)) {
		t.Fatalf("json=%s err=%v", canonical, err)
	}

	streamError := azureWebPubSubProtobufAppendString(nil, 1, "UserError")
	streamError = azureWebPubSubProtobufAppendString(streamError, 2, "bad")
	streamError = azureWebPubSubProtobufAppendString(streamError, 3, "E")
	stream := azureWebPubSubProtobufAppendString(nil, 1, "stream-1")
	stream = azureWebPubSubProtobufAppendVarint(stream, 2, 7)
	stream = azureWebPubSubProtobufAppendVarint(stream, 3, 1)
	stream = azureWebPubSubProtobufAppendBytes(stream, 4, streamError)
	streamMessage := azureWebPubSubProtobufAppendString(nil, 1, "group")
	streamMessage = azureWebPubSubProtobufAppendString(streamMessage, 2, "room")
	streamMessage = azureWebPubSubProtobufAppendBytes(streamMessage, 3, azureWebPubSubProtobufAppendString(nil, 1, "fragment"))
	streamMessage = azureWebPubSubProtobufAppendBytes(streamMessage, 6, stream)
	canonical, _, _, _, _, err = parseAzureWebPubSubProtobufServerMessage(cloudWebSocketMessageBinary, azureWebPubSubProtobufAppendBytes(nil, 2, streamMessage), false)
	for _, expected := range []string{`"streamId":"stream-1"`, `"streamSequenceId":7`, `"endOfStream":true`, `"userErrorCode":"E"`} {
		if err != nil || !bytes.Contains(canonical, []byte(expected)) {
			t.Fatalf("stream=%s missing=%s err=%v", canonical, expected, err)
		}
	}
}

func TestAzureWebPubSubProtobufAckAndStreamFailures(t *testing.T) {
	failure := azureWebPubSubProtobufAppendString(nil, 1, "Duplicate")
	failure = azureWebPubSubProtobufAppendString(failure, 2, "processed")
	ack := azureWebPubSubProtobufAppendVarint(nil, 1, 9)
	ack = azureWebPubSubProtobufAppendBytes(ack, 3, failure)
	wire := azureWebPubSubProtobufAppendBytes(nil, 1, ack)
	message, kind, _, ackID, _, err := parseAzureWebPubSubProtobufServerMessage(cloudWebSocketMessageBinary, wire, true)
	if err != nil || kind != "ack" || ackID != 9 || !bytes.Contains(message, []byte(`"name":"Duplicate"`)) {
		t.Fatalf("message=%s kind=%q ack=%d err=%v", message, kind, ackID, err)
	}
	if _, _, _, _, _, err := parseAzureWebPubSubProtobufServerMessage(cloudWebSocketMessageBinary, wire, false); err == nil {
		t.Fatal("failed standard acknowledgement accepted")
	}

	streamNack := azureWebPubSubProtobufAppendString(nil, 1, "s")
	streamNack = azureWebPubSubProtobufAppendString(streamNack, 2, "TransientError")
	streamNack = azureWebPubSubProtobufAppendString(streamNack, 3, "retry")
	streamNack = azureWebPubSubProtobufAppendVarint(streamNack, 4, 2)
	if _, _, _, _, _, err := parseAzureWebPubSubProtobufServerMessage(cloudWebSocketMessageBinary, azureWebPubSubProtobufAppendBytes(nil, 7, streamNack), false); err == nil {
		t.Fatal("stream nack accepted")
	}
	streamClosed := azureWebPubSubProtobufAppendString(nil, 1, "s")
	streamClosed = azureWebPubSubProtobufAppendBytes(streamClosed, 2, azureWebPubSubProtobufAppendString(nil, 1, "IdleTimeout"))
	if _, _, _, _, _, err := parseAzureWebPubSubProtobufServerMessage(cloudWebSocketMessageBinary, azureWebPubSubProtobufAppendBytes(nil, 8, streamClosed), false); err == nil {
		t.Fatal("failed stream close accepted")
	}
}

func TestAzureWebPubSubProtobufWireCompatibilityAndIntegerBounds(t *testing.T) {
	maxAck := azureWebPubSubProtobufAppendVarint(nil, 1, ^uint64(0))
	maxAck = azureWebPubSubProtobufAppendVarint(maxAck, 2, 1)
	maxAck = azureWebPubSubProtobufAppendBytes(nil, 1, maxAck)
	connection := &fakeTencentWebSocketConnection{reads: [][]byte{maxAck}, readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageBinary}}
	message, _, err := readAzureWebPubSubProtobufServerMessage(t.Context(), connection)
	if err != nil || !bytes.Contains(message, []byte(`"ackId":18446744073709551615`)) {
		t.Fatalf("uint64 response lost precision: %s err=%v", message, err)
	}
	pongWithUnknownFields := []byte{0x22, 0x00, 0x55, 1, 2, 3, 4, 0x59, 1, 2, 3, 4, 5, 6, 7, 8}
	if _, kind, _, _, _, err := parseAzureWebPubSubProtobufServerMessage(cloudWebSocketMessageBinary, pongWithUnknownFields, false); err != nil || kind != "pong" {
		t.Fatalf("forward-compatible protobuf rejected: kind=%q err=%v", kind, err)
	}
	for _, malformed := range [][]byte{{0x22, 0x00, 0x55, 1}, {0x22, 0x00, 0x59, 1}, {0x22, 0x00, 0x0b}} {
		if _, _, _, _, _, err := parseAzureWebPubSubProtobufServerMessage(cloudWebSocketMessageBinary, malformed, false); err == nil {
			t.Fatalf("malformed wire accepted: %x", malformed)
		}
	}
	tooManyFields := []byte{0x22, 0x00}
	tooManyFields = append(tooManyFields, bytes.Repeat([]byte{0x50, 0x01}, azureWebPubSubMaxProtobufFields)...)
	if _, _, _, _, _, err := parseAzureWebPubSubProtobufServerMessage(cloudWebSocketMessageBinary, tooManyFields, false); err == nil {
		t.Fatal("unbounded protobuf field count accepted")
	}
	for value, valid := range map[string]bool{
		"18446744073709551615": true,
		"18446744073709551616": false,
		"12.5":                 false,
		"":                     false,
	} {
		_, err := parseAzureWebPubSubUint64(value)
		if (err == nil) != valid {
			t.Fatalf("value=%q valid=%v err=%v", value, valid, err)
		}
	}
}

func TestAzureWebPubSubAcceptsBoundedProtobufProtocols(t *testing.T) {
	for _, protocol := range []string{"protobuf", "protobuf-reliable"} {
		body := map[string]any{
			"protocol": protocol, "messages": []any{map[string]any{"type": "ping"}},
			"max_messages": 2, "timeout_seconds": 30,
		}
		if _, err := parseAzureWebPubSubPlan(body); err != nil {
			t.Fatalf("protocol %q rejected: %v", protocol, err)
		}
	}
}

func TestAzureWebPubSubProtobufMintsContainedTokenAndUsesBinaryFrames(t *testing.T) {
	responseFile := filepath.Join(t.TempDir(), "protobuf.ndjson")
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			mustAzureWebPubSubHex(t, "1a090a070a016312027531"),
			mustAzureWebPubSubHex(t, "0a0408011001"),
		},
		readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageBinary, cloudWebSocketMessageBinary},
	}
	var dialHeader http.Header
	adapter := NewAzureRESTAdapter(AzureRESTConfig{
		Tokens: &staticAzureTokenProvider{token: "private-entra-token"},
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"token":"private-client-token"}`))}, nil
		}),
		ProtobufWebPubSubWebSocketDial: func(_ context.Context, _ string, header http.Header) (cloudWebSocketConnection, error) {
			dialHeader = header.Clone()
			return connection, nil
		},
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, Mode: ModeMutate, AuthScheme: "webpubsub-ws", Service: "webpubsub", Operation: "ClientConnect",
		Method: http.MethodGet, URL: "wss://demo.webpubsub.azure.com/client/hubs/chat",
		Body: map[string]any{
			"protocol":     "protobuf",
			"messages":     []any{map[string]any{"type": "joinGroup", "group": "room", "ackId": 1}},
			"max_messages": 2, "timeout_seconds": 30,
		},
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if dialHeader.Get("Authorization") != "Bearer private-client-token" || len(connection.writes) != 1 || connection.writes[0].messageType != cloudWebSocketMessageBinary || hex.EncodeToString(connection.writes[0].data) != "32080a04726f6f6d1001" {
		t.Fatalf("header=%#v writes=%#v", dialHeader, connection.writes)
	}
	written, readErr := os.ReadFile(responseFile)
	if readErr != nil || bytes.Contains(written, []byte("private-")) || !bytes.Contains(written, []byte(`"success":true`)) || !bytes.Contains(result.Output, []byte(`"messages":2`)) {
		t.Fatalf("output=%s result=%s err=%v", written, result.Output, readErr)
	}
}

func TestAzureReliableWebPubSubProtobufRecoversAndAcknowledgesSequences(t *testing.T) {
	responseFile := filepath.Join(t.TempDir(), "protobuf-reliable.ndjson")
	first := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			mustAzureWebPubSubHex(t, "1a0c0a0a0a01631201751a027431"),
			mustAzureWebPubSubHex(t, "0a0408011001"),
			mustAzureWebPubSubHex(t, "12160a0567726f75701204726f6f6d1a050a036f6e652001"),
		},
		readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageBinary, cloudWebSocketMessageBinary, cloudWebSocketMessageBinary},
	}
	second := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			mustAzureWebPubSubHex(t, "1a0c0a0a0a01631201751a027432"),
			mustAzureWebPubSubHex(t, "12160a0567726f75701204726f6f6d1a050a036f6e652001"),
			mustAzureWebPubSubHex(t, "12160a0567726f75701204726f6f6d1a050a0374776f2002"),
			mustAzureWebPubSubHex(t, "1a0712051203627965"),
		},
		readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageBinary, cloudWebSocketMessageBinary, cloudWebSocketMessageBinary, cloudWebSocketMessageBinary},
	}
	connections := []cloudWebSocketConnection{first, second}
	var targets []string
	var headers []http.Header
	adapter := NewAzureRESTAdapter(AzureRESTConfig{
		Tokens: &staticAzureTokenProvider{token: "entra-token"},
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"token":"client-token"}`))}, nil
		}),
		ReliableProtobufWebPubSubWebSocketDial: func(_ context.Context, target string, header http.Header) (cloudWebSocketConnection, error) {
			targets = append(targets, target)
			headers = append(headers, header.Clone())
			connection := connections[0]
			connections = connections[1:]
			return connection, nil
		},
		StreamPause: func(context.Context, time.Duration) error { return nil },
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, Mode: ModeMutate, AuthScheme: "webpubsub-ws", Service: "webpubsub", Operation: "ClientConnect",
		Method: http.MethodGet, URL: "wss://demo.webpubsub.azure.com/client/hubs/chat",
		Body: map[string]any{
			"protocol": "protobuf-reliable",
			"messages": []any{
				map[string]any{"type": "joinGroup", "group": "room", "ackId": 1},
				map[string]any{"type": "event", "event": "notify", "ackId": 2, "dataType": "text", "data": "hello"},
			},
			"max_messages": 6, "timeout_seconds": 30,
		},
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 || headers[0].Get("Authorization") != "Bearer client-token" || len(headers[1]) != 0 || !strings.Contains(targets[1], "awps_connection_id=c") || !strings.Contains(targets[1], "awps_reconnection_token=t1") {
		t.Fatalf("targets=%v headers=%#v", targets, headers)
	}
	written, readErr := os.ReadFile(responseFile)
	if readErr != nil || bytes.Contains(written, []byte("t1")) || bytes.Contains(written, []byte("t2")) || bytes.Count(written, []byte(`"sequenceId":1`)) != 1 || !bytes.Contains(written, []byte(`"sequenceId":2`)) || !bytes.Contains(result.Output, []byte(`"messages":6`)) {
		t.Fatalf("output=%s result=%s err=%v", written, result.Output, readErr)
	}
	for index, connection := range []*fakeTencentWebSocketConnection{first, second} {
		found := false
		for _, write := range connection.writes {
			if write.messageType == cloudWebSocketMessageBinary && bytes.HasPrefix(write.data, []byte{0x42}) {
				found = true
			}
		}
		if !found {
			t.Fatalf("connection %d omitted binary sequence ack: %#v", index, connection.writes)
		}
	}
	resentEvent := false
	for _, write := range second.writes {
		if write.messageType == cloudWebSocketMessageBinary && bytes.HasPrefix(write.data, []byte{0x2a}) {
			resentEvent = true
		}
		if bytes.HasPrefix(write.data, []byte{0x32}) {
			t.Fatal("acknowledged join was resent")
		}
	}
	if !resentEvent {
		t.Fatal("unacknowledged protobuf event was not resent")
	}
}

func TestAzureReliableWebPubSubProtobufNeverPublishesRecoveryToken(t *testing.T) {
	responseFile := filepath.Join(t.TempDir(), "must-not-exist.ndjson")
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			mustAzureWebPubSubHex(t, "1a120a100a01631201751a087072697661746531"),
			mustAzureWebPubSubHex(t, "1a120a100a01631201751a087072697661746532"),
		},
		readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageBinary, cloudWebSocketMessageBinary},
	}
	adapter := NewAzureRESTAdapter(AzureRESTConfig{
		Tokens: &staticAzureTokenProvider{token: "entra-token"},
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"token":"client-token"}`))}, nil
		}),
		ReliableProtobufWebPubSubWebSocketDial: func(context.Context, string, http.Header) (cloudWebSocketConnection, error) {
			return connection, nil
		},
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, Mode: ModeMutate, AuthScheme: "webpubsub-ws", Service: "webpubsub", Operation: "ClientConnect",
		Method: http.MethodGet, URL: "wss://demo.webpubsub.azure.com/client/hubs/chat",
		Body: map[string]any{
			"protocol": "protobuf-reliable", "messages": []any{map[string]any{"type": "ping"}},
			"max_messages": 2, "timeout_seconds": 30,
		},
		ResponseFile: responseFile,
	})
	if err == nil || strings.Contains(err.Error(), "private") {
		t.Fatalf("unsafe error=%v", err)
	}
	if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
		t.Fatalf("partial output published: %v", statErr)
	}
}

func TestDefaultAzureWebPubSubProtobufDialsNegotiateExactSubprotocols(t *testing.T) {
	for _, test := range []struct {
		name        string
		subprotocol string
		dial        azureWebPubSubWebSocketDial
	}{
		{"standard", "protobuf.webpubsub.azure.v1", defaultAzureProtobufWebPubSubWebSocketDial},
		{"reliable", "protobuf.reliable.webpubsub.azure.v1", defaultAzureReliableProtobufWebPubSubWebSocketDial},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				connection, err := coderwebsocket.Accept(writer, request, &coderwebsocket.AcceptOptions{Subprotocols: []string{test.subprotocol}})
				if err != nil {
					return
				}
				defer connection.Close(coderwebsocket.StatusNormalClosure, "done")
				_ = connection.Write(request.Context(), coderwebsocket.MessageBinary, []byte{0x22, 0x00})
			}))
			defer server.Close()
			connection, err := test.dial(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			messageType, message, err := connection.Read(t.Context())
			if err != nil || messageType != cloudWebSocketMessageBinary || !bytes.Equal(message, []byte{0x22, 0x00}) {
				t.Fatalf("type=%d message=%x err=%v", messageType, message, err)
			}
		})
	}
}

func mustAzureWebPubSubHex(t *testing.T, encoded string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func FuzzAzureWebPubSubProtobufServerMessageNeverPanics(f *testing.F) {
	for _, seed := range [][]byte{
		{0x22, 0x00},
		{0x0a, 0x04, 0x08, 0x01, 0x10, 0x01},
		{0x12, 0x0f, 0x0a, 0x06, 's', 'e', 'r', 'v', 'e', 'r', 0x1a, 0x05, 0x12, 0x03, 1, 2, 3},
		{0x1a, 0x07, 0x12, 0x05, 0x12, 0x03, 'b', 'y', 'e'},
	} {
		f.Add(seed, false)
		f.Add(seed, true)
	}
	f.Fuzz(func(t *testing.T, data []byte, reliable bool) {
		if len(data) > maxRequestPayloadBytes+1 {
			t.Skip()
		}
		message, _, _, _, _, err := parseAzureWebPubSubProtobufServerMessage(cloudWebSocketMessageBinary, data, reliable)
		if err == nil && !json.Valid(message) {
			t.Fatalf("successful parse returned invalid JSON: %x", message)
		}
	})
}

func FuzzAzureWebPubSubProtobufClientMessageNeverPanics(f *testing.F) {
	for _, seed := range []string{
		`{"type":"ping"}`,
		`{"type":"joinGroup","group":"room","ackId":1}`,
		`{"type":"streamData","streamId":"s"}`,
		`{"type":"sendToGroup","group":"room","dataType":"binary","data":"AQID"}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxRequestPayloadBytes+1 {
			t.Skip()
		}
		wire, _, err := encodeAzureWebPubSubProtobufClientMessage(data)
		if err == nil {
			if len(wire) == 0 {
				t.Fatal("successful encode returned an empty protobuf frame")
			}
			if _, parseErr := azureWebPubSubProtobufParseFields(wire); parseErr != nil {
				t.Fatalf("successful encode returned invalid protobuf: %v", parseErr)
			}
		}
	})
}

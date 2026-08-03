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

func TestAzureWebPubSubBoundaryRequiresExactEntraBackedProtocol(t *testing.T) {
	root := t.TempDir()
	valid := func() Invocation {
		return Invocation{
			Provider: ProviderAzure, Mode: ModeMutate, AuthScheme: "webpubsub-ws", Service: "webpubsub", Operation: "ClientConnect",
			Method: http.MethodGet, URL: "wss://demo.webpubsub.azure.com/client/hubs/chat_hub",
			Body: map[string]any{
				"user_id": "observer", "roles": []any{"webpubsub.joinLeaveGroup.room"}, "groups": []any{"room"},
				"messages":     []any{map[string]any{"type": "joinGroup", "group": "room", "ackId": 1}},
				"max_messages": 4, "timeout_seconds": 30,
			},
			ResponseFile: filepath.Join(root, "events.ndjson"), StreamIntervalMS: 10,
		}
	}
	if err := validateInvocation(valid(), []string{root}); err != nil {
		t.Fatal(err)
	}
	if classifyRead(ProviderAzure, valid()) {
		t.Fatal("Web PubSub session was classified read-only")
	}
	for name, mutate := range map[string]func(*Invocation){
		"post":            func(v *Invocation) { v.Method = http.MethodPost },
		"service":         func(v *Invocation) { v.Service = "signalr" },
		"operation":       func(v *Invocation) { v.Operation = "Send" },
		"host":            func(v *Invocation) { v.URL = "wss://demo.webpubsub.azure.com.attacker.test/client/hubs/chat_hub" },
		"path":            func(v *Invocation) { v.URL = "wss://demo.webpubsub.azure.com/client/hubs/chat_hub/extra" },
		"query":           func(v *Invocation) { v.URL += "?access_token=caller" },
		"header":          func(v *Invocation) { v.Headers = map[string]string{"Authorization": "Bearer caller"} },
		"missing body":    func(v *Invocation) { v.Body = nil },
		"body file":       func(v *Invocation) { v.BodyFile = filepath.Join(root, "body") },
		"missing output":  func(v *Invocation) { v.ResponseFile = "" },
		"unknown message": func(v *Invocation) { v.Body.(map[string]any)["messages"] = []any{map[string]any{"type": "connect"}} },
		"duplicate ack": func(v *Invocation) {
			v.Body.(map[string]any)["messages"] = []any{map[string]any{"type": "joinGroup", "ackId": 1}, map[string]any{"type": "ping", "ackId": 1}}
		},
		"credential":       func(v *Invocation) { v.Body.(map[string]any)["client_secret"] = "caller" },
		"role":             func(v *Invocation) { v.Body.(map[string]any)["roles"] = []any{"webpubsub.admin"} },
		"unbounded":        func(v *Invocation) { v.Body.(map[string]any)["timeout_seconds"] = 301 },
		"unknown protocol": func(v *Invocation) { v.Body.(map[string]any)["protocol"] = "xml" },
		"reliable missing ack": func(v *Invocation) {
			v.Body.(map[string]any)["protocol"] = "json-reliable"
			v.Body.(map[string]any)["messages"] = []any{map[string]any{"type": "joinGroup", "group": "room"}}
		},
		"reliable setup-only bound": func(v *Invocation) {
			v.Body.(map[string]any)["protocol"] = "json-reliable"
			v.Body.(map[string]any)["max_messages"] = 1
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid()
			mutate(&candidate)
			if err := validateInvocation(candidate, []string{root}); err == nil {
				t.Fatal("unsafe Web PubSub invocation accepted")
			}
		})
	}
}

func TestAzureWebPubSubMintsContainedClientTokenAndStreamsJSON(t *testing.T) {
	root := t.TempDir()
	responseFile := filepath.Join(root, "events.ndjson")
	identity := &staticAzureTokenProvider{token: "private-entra-token"}
	connection := &fakeTencentWebSocketConnection{reads: [][]byte{
		[]byte(`{"type":"system","event":"connected","connectionId":"connection-1"}`),
		[]byte(`{"type":"ack","ackId":1,"success":true}`),
	}}
	var tokenRequest *http.Request
	var dialTarget string
	var dialHeaders http.Header
	adapter := NewAzureRESTAdapter(AzureRESTConfig{
		Tokens: identity,
		HTTP: doerFunc(func(request *http.Request) (*http.Response, error) {
			tokenRequest = request.Clone(request.Context())
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"token":"private-client-token"}`))}, nil
		}),
		WebPubSubWebSocketDial: func(_ context.Context, target string, headers http.Header) (cloudWebSocketConnection, error) {
			dialTarget, dialHeaders = target, headers.Clone()
			return connection, nil
		},
		StreamPause: func(context.Context, time.Duration) error { return nil },
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, Mode: ModeMutate, AuthScheme: "webpubsub-ws", Service: "webpubsub", Operation: "ClientConnect",
		Method: http.MethodGet, URL: "wss://demo.webpubsub.azure.com/client/hubs/chat_hub",
		Body: map[string]any{
			"user_id": "observer", "roles": []any{"webpubsub.joinLeaveGroup.room"}, "groups": []any{"room"},
			"messages":     []any{map[string]any{"type": "joinGroup", "group": "room", "ackId": 1}},
			"max_messages": 2, "timeout_seconds": 30,
		},
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if identity.calls != 1 || identity.scope != "https://webpubsub.azure.com/.default" {
		t.Fatalf("identity calls=%d scope=%q", identity.calls, identity.scope)
	}
	if tokenRequest == nil || tokenRequest.Method != http.MethodPost || tokenRequest.URL.Path != "/api/hubs/chat_hub/:generateToken" || tokenRequest.URL.Query().Get("api-version") != "2024-01-01" || tokenRequest.URL.Query().Get("minutesToExpire") != "5" || tokenRequest.Header.Get("Authorization") != "Bearer private-entra-token" {
		t.Fatalf("token request=%#v", tokenRequest)
	}
	if dialTarget != "wss://demo.webpubsub.azure.com/client/hubs/chat_hub" || dialHeaders.Get("Authorization") != "Bearer private-client-token" || len(dialHeaders) != 1 {
		t.Fatalf("dial=%q headers=%#v", dialTarget, dialHeaders)
	}
	written, err := os.ReadFile(responseFile)
	if err != nil || bytes.Count(bytes.TrimSpace(written), []byte{'\n'}) != 1 || bytes.Contains(written, []byte("private-")) || !bytes.Contains(written, []byte(`"success":true`)) {
		t.Fatalf("output=%s err=%v", written, err)
	}
	if strings.Contains(string(result.Output), "private-") || !bytes.Contains(result.Output, []byte(`"messages":2`)) {
		t.Fatalf("result=%s", result.Output)
	}
}

func TestAzureWebPubSubFailuresNeverPublishTokenOrOutput(t *testing.T) {
	for name, failure := range map[string]struct {
		response *http.Response
		reads    [][]byte
	}{
		"token error": {response: &http.Response{StatusCode: http.StatusForbidden, Body: io.NopCloser(strings.NewReader(`{"token":"must-not-leak"}`))}},
		"failed ack":  {response: &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"token":"client-token"}`))}, reads: [][]byte{[]byte(`{"type":"ack","ackId":1,"success":false,"error":{"name":"Forbidden"}}`)}},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			responseFile := filepath.Join(root, "events.ndjson")
			adapter := NewAzureRESTAdapter(AzureRESTConfig{
				Tokens: &staticAzureTokenProvider{token: "entra-token"},
				HTTP:   doerFunc(func(*http.Request) (*http.Response, error) { return failure.response, nil }),
				WebPubSubWebSocketDial: func(context.Context, string, http.Header) (cloudWebSocketConnection, error) {
					return &fakeTencentWebSocketConnection{reads: failure.reads}, nil
				},
			})
			_, err := adapter.Invoke(t.Context(), Invocation{
				Provider: ProviderAzure, Mode: ModeMutate, AuthScheme: "webpubsub-ws", Service: "webpubsub", Operation: "ClientConnect",
				Method: http.MethodGet, URL: "wss://demo.webpubsub.azure.com/client/hubs/chat",
				Body:         map[string]any{"messages": []any{map[string]any{"type": "joinGroup", "group": "room", "ackId": 1}}, "max_messages": 1, "timeout_seconds": 1},
				ResponseFile: responseFile,
			})
			if err == nil || strings.Contains(err.Error(), "must-not-leak") || strings.Contains(err.Error(), "client-token\"") {
				t.Fatalf("unsafe error=%v", err)
			}
			if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
				t.Fatalf("failed session published output: %v", statErr)
			}
		})
	}
}

func TestAzureWebPubSubProtocolMessageValidation(t *testing.T) {
	for _, raw := range []string{
		`{"type":"leaveGroup","group":"room","ackId":2}`,
		`{"type":"sendToGroup","group":"room","dataType":"text","data":"hello"}`,
		`{"type":"event","event":"notify","dataType":"json","data":{}}`,
		`{"type":"ping"}`,
	} {
		if _, _, err := validateAzureWebPubSubClientMessage([]byte(raw)); err != nil {
			t.Fatalf("valid client message rejected: %s: %v", raw, err)
		}
	}
	if _, ackID, err := validateAzureWebPubSubClientMessage([]byte(`{"type":"joinGroup","group":"room","ackId":9007199254740993}`)); err != nil || ackID != 9007199254740993 {
		t.Fatalf("uint64 ackId=%d err=%v", ackID, err)
	}
	for _, raw := range []string{
		`{"type":"event"}`,
		`{"type":"joinGroup","group":"room","ackId":1.5}`,
		`{"type":"joinGroup","group":" room"}`,
	} {
		if _, _, err := validateAzureWebPubSubClientMessage([]byte(raw)); err == nil {
			t.Fatalf("invalid client message accepted: %s", raw)
		}
	}
	if azureWebPubSubBoundedValue(" bad", 10) || azureWebPubSubBoundedValue("bad\n", 10) || azureWebPubSubRole("webpubsub.sendToGroups.") || !azureWebPubSubRole("webpubsub.sendToGroups.room*") {
		t.Fatal("bounded claim validation mismatch")
	}

	for _, raw := range []string{
		`{"type":"message","from":"server","dataType":"text","data":"hello"}`,
		`{"type":"pong"}`,
		`{"type":"streamAck","streamId":"stream","expectedSequenceId":2}`,
		`{"type":"streamClosed","streamId":"stream"}`,
		`{"type":"system","event":"connected"}`,
	} {
		connection := &fakeTencentWebSocketConnection{reads: [][]byte{[]byte(raw)}}
		if _, _, err := readAzureWebPubSubServerMessage(t.Context(), connection); err != nil {
			t.Fatalf("valid server message rejected: %s: %v", raw, err)
		}
	}
	connection := &fakeTencentWebSocketConnection{reads: [][]byte{[]byte(`{"type":"system","event":"disconnected"}`)}}
	if _, terminal, err := readAzureWebPubSubServerMessage(t.Context(), connection); err != nil || !terminal {
		t.Fatalf("disconnect terminal=%v err=%v", terminal, err)
	}
	for _, raw := range []string{
		`{"type":"streamNack","streamId":"stream"}`,
		`{"type":"streamClosed","error":{"name":"Forbidden"}}`,
		`{"type":"system","event":"unknown"}`,
		`{"type":"unknown"}`,
		`{"data":"missing type"}`,
	} {
		connection := &fakeTencentWebSocketConnection{reads: [][]byte{[]byte(raw)}}
		if _, _, err := readAzureWebPubSubServerMessage(t.Context(), connection); err == nil {
			t.Fatalf("invalid server message accepted: %s", raw)
		}
	}
	if _, kind, sequenceID, _, _, err := parseAzureReliableServerMessage(cloudWebSocketMessageText, []byte(`{"type":"message","sequenceId":9007199254740993}`)); err != nil || kind != "message" || sequenceID != 9007199254740993 {
		t.Fatalf("uint64 sequenceId=%d kind=%q err=%v", sequenceID, kind, err)
	}
}

func TestDefaultAzureWebPubSubDialNegotiatesJSONSubprotocol(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer client-token" {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		connection, err := coderwebsocket.Accept(writer, request, &coderwebsocket.AcceptOptions{Subprotocols: []string{"json.webpubsub.azure.v1"}})
		if err != nil {
			return
		}
		defer connection.Close(coderwebsocket.StatusNormalClosure, "done")
		_ = connection.Write(request.Context(), coderwebsocket.MessageText, []byte(`{"type":"pong"}`))
	}))
	defer server.Close()
	connection, err := defaultAzureWebPubSubWebSocketDial(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http"), http.Header{"Authorization": []string{"Bearer client-token"}})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	messageType, message, err := connection.Read(t.Context())
	if err != nil || messageType != cloudWebSocketMessageText || string(message) != `{"type":"pong"}` {
		t.Fatalf("type=%d message=%s err=%v", messageType, message, err)
	}
}

func TestDefaultAzureReliableWebPubSubDialNegotiatesReliableSubprotocol(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := coderwebsocket.Accept(writer, request, &coderwebsocket.AcceptOptions{Subprotocols: []string{"json.reliable.webpubsub.azure.v1"}})
		if err != nil {
			return
		}
		defer connection.Close(coderwebsocket.StatusNormalClosure, "done")
		_ = connection.Write(request.Context(), coderwebsocket.MessageText, []byte(`{"type":"pong"}`))
	}))
	defer server.Close()
	connection, err := defaultAzureReliableWebPubSubWebSocketDial(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, message, err := connection.Read(t.Context()); err != nil || string(message) != `{"type":"pong"}` {
		t.Fatalf("message=%s err=%v", message, err)
	}
}

func TestAzureReliableWebPubSubRejectsInvalidRecoveryMessages(t *testing.T) {
	for _, raw := range []string{
		`{"type":"ack","ackId":1,"success":false,"error":{"name":"InternalServerError"}}`,
		`{"type":"message"}`,
		`{"type":"streamNack"}`,
		`{"type":"streamClosed","error":{"name":"Forbidden"}}`,
		`{"type":"system","event":"connected"}`,
		`{"type":"system","event":"unknown"}`,
		`{"type":"unknown"}`,
	} {
		if _, _, _, _, _, err := parseAzureReliableServerMessage(cloudWebSocketMessageText, []byte(raw)); err == nil {
			t.Fatalf("invalid reliable message accepted: %s", raw)
		}
	}
	for _, raw := range []string{
		`{"type":"ack","ackId":1,"success":false,"error":{"name":"Duplicate"}}`,
		`{"type":"pong"}`,
		`{"type":"streamAck","streamId":"stream"}`,
		`{"type":"streamClosed","streamId":"stream"}`,
		`{"type":"system","event":"disconnected"}`,
	} {
		if _, _, _, _, _, err := parseAzureReliableServerMessage(cloudWebSocketMessageText, []byte(raw)); err != nil {
			t.Fatalf("valid reliable message rejected: %s: %v", raw, err)
		}
	}
	connection := &fakeTencentWebSocketConnection{reads: [][]byte{[]byte(`{"type":"system","event":"connected","connectionId":"connection"}`)}}
	if _, err := readAzureReliableConnected(t.Context(), connection); err == nil {
		t.Fatal("connected without recovery token accepted")
	}
}

func TestAzureReliableWebPubSubRecoversAndAcknowledgesSequences(t *testing.T) {
	root := t.TempDir()
	responseFile := filepath.Join(root, "reliable.ndjson")
	firstConnection := &fakeTencentWebSocketConnection{reads: [][]byte{
		[]byte(`{"type":"system","event":"connected","connectionId":"connection-1","reconnectionToken":"private-reconnect-1"}`),
		[]byte(`{"type":"ack","ackId":1,"success":true}`),
		[]byte(`{"type":"message","from":"group","group":"room","sequenceId":1,"dataType":"text","data":"one"}`),
	}}
	secondConnection := &fakeTencentWebSocketConnection{reads: [][]byte{
		[]byte(`{"type":"system","event":"connected","connectionId":"connection-1","reconnectionToken":"private-reconnect-2"}`),
		[]byte(`{"type":"message","from":"group","group":"room","sequenceId":1,"dataType":"text","data":"duplicate"}`),
		[]byte(`{"type":"message","from":"group","group":"room","sequenceId":2,"dataType":"text","data":"two"}`),
		[]byte(`{"type":"system","event":"disconnected"}`),
	}}
	connections := []cloudWebSocketConnection{firstConnection, &azureWebPubSubReadErrorConnection{err: io.ErrUnexpectedEOF}, secondConnection}
	var targets []string
	var headers []http.Header
	dialCalls := 0
	adapter := NewAzureRESTAdapter(AzureRESTConfig{
		Tokens: &staticAzureTokenProvider{token: "entra-token"},
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"token":"client-token"}`))}, nil
		}),
		ReliableWebPubSubWebSocketDial: func(_ context.Context, target string, header http.Header) (cloudWebSocketConnection, error) {
			dialCalls++
			targets = append(targets, target)
			headers = append(headers, header.Clone())
			if dialCalls == 2 {
				return nil, io.ErrUnexpectedEOF
			}
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
			"protocol": "json-reliable",
			"messages": []any{
				map[string]any{"type": "joinGroup", "group": "room", "ackId": 1},
				map[string]any{"type": "event", "event": "notify", "ackId": 2},
			},
			"max_messages": 6, "timeout_seconds": 30,
		},
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 4 || headers[0].Get("Authorization") != "Bearer client-token" || len(headers[1]) != 0 || len(headers[2]) != 0 || len(headers[3]) != 0 || !strings.Contains(targets[3], "awps_connection_id=connection-1") || !strings.Contains(targets[3], "awps_reconnection_token=private-reconnect-1") {
		t.Fatalf("targets=%v headers=%#v", targets, headers)
	}
	if len(connections) != 0 {
		t.Fatalf("unused connections=%d", len(connections))
	}
	written, err := os.ReadFile(responseFile)
	if err != nil || bytes.Contains(written, []byte("private-reconnect")) || bytes.Contains(written, []byte("duplicate")) || !bytes.Contains(written, []byte(`"sequenceId":2`)) {
		t.Fatalf("output=%s err=%v", written, err)
	}
	if strings.Contains(string(result.Output), "private-reconnect") || !bytes.Contains(result.Output, []byte(`"messages":6`)) {
		t.Fatalf("result=%s", result.Output)
	}
	for index, connection := range []*fakeTencentWebSocketConnection{firstConnection, secondConnection} {
		found := false
		for _, write := range connection.writes {
			if bytes.Contains(write.data, []byte(`"type":"sequenceAck"`)) {
				found = true
			}
		}
		if !found {
			t.Fatalf("connection %d did not emit a sequenceAck: %#v", index, connection.writes)
		}
	}
	resentEvent := false
	for _, write := range secondConnection.writes {
		if bytes.Contains(write.data, []byte(`"event":"notify"`)) && bytes.Contains(write.data, []byte(`"ackId":2`)) {
			resentEvent = true
		}
		if bytes.Contains(write.data, []byte(`"joinGroup"`)) {
			t.Fatal("acknowledged publisher message was resent")
		}
	}
	if !resentEvent {
		t.Fatalf("unacknowledged publisher message was not resent: %#v", secondConnection.writes)
	}
}

func TestAzureReliableWebPubSubStopsOnExpiredRecoveryState(t *testing.T) {
	responseFile := filepath.Join(t.TempDir(), "reliable.ndjson")
	initial := &fakeTencentWebSocketConnection{reads: [][]byte{
		[]byte(`{"type":"system","event":"connected","connectionId":"connection-1","reconnectionToken":"private-reconnect"}`),
	}}
	dials := 0
	adapter := NewAzureRESTAdapter(AzureRESTConfig{
		Tokens: &staticAzureTokenProvider{token: "entra-token"},
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"token":"client-token"}`))}, nil
		}),
		ReliableWebPubSubWebSocketDial: func(context.Context, string, http.Header) (cloudWebSocketConnection, error) {
			dials++
			if dials == 1 {
				return initial, nil
			}
			return nil, coderwebsocket.CloseError{Code: coderwebsocket.StatusPolicyViolation, Reason: "private-reason"}
		},
		StreamPause: func(context.Context, time.Duration) error { return nil },
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, Mode: ModeMutate, AuthScheme: "webpubsub-ws", Service: "webpubsub", Operation: "ClientConnect",
		Method: http.MethodGet, URL: "wss://demo.webpubsub.azure.com/client/hubs/chat",
		Body: map[string]any{
			"protocol": "json-reliable", "messages": []any{map[string]any{"type": "joinGroup", "group": "room", "ackId": 1}},
			"max_messages": 2, "timeout_seconds": 30,
		},
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err == nil || err.Error() != "Azure reliable Web PubSub recovery state expired" || dials != 2 {
		t.Fatalf("err=%v dials=%d", err, dials)
	}
	if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
		t.Fatalf("partial output published: %v", statErr)
	}
}

type azureWebPubSubReadErrorConnection struct {
	err error
}

func (connection *azureWebPubSubReadErrorConnection) Read(context.Context) (cloudWebSocketMessageType, []byte, error) {
	return 0, nil, connection.err
}

func (*azureWebPubSubReadErrorConnection) Write(context.Context, cloudWebSocketMessageType, []byte) error {
	return nil
}

func (*azureWebPubSubReadErrorConnection) Close() error { return nil }

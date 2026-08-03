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
		"credential": func(v *Invocation) { v.Body.(map[string]any)["client_secret"] = "caller" },
		"role":       func(v *Invocation) { v.Body.(map[string]any)["roles"] = []any{"webpubsub.admin"} },
		"unbounded":  func(v *Invocation) { v.Body.(map[string]any)["timeout_seconds"] = 301 },
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

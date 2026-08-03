package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	coderwebsocket "github.com/coder/websocket"
)

func signalRFrame(payload string) []byte {
	return append([]byte(payload), azureSignalRRecordSeparator)
}

func TestAzureSignalRSubscribeBoundaryRequiresExactEntraBackedProtocol(t *testing.T) {
	root := t.TempDir()
	valid := func() Invocation {
		return Invocation{
			Provider: ProviderAzure, Mode: ModeRead, AuthScheme: "signalr-ws", Service: "signalr", Operation: "Subscribe",
			Method: http.MethodGet, URL: "wss://demo.service.signalr.net/client/?hub=chat_hub",
			Body: map[string]any{
				"user_id": "observer", "minutes_to_expire": 5,
				"max_messages": 4, "timeout_seconds": 30,
			},
			ResponseFile: filepath.Join(root, "events.ndjson"), StreamIntervalMS: 10,
		}
	}
	if err := validateInvocation(valid(), []string{root}); err != nil {
		t.Fatal(err)
	}
	if !classifyRead(ProviderAzure, valid()) {
		t.Fatal("SignalR Subscribe was not classified read-only")
	}
	for name, mutate := range map[string]func(*Invocation){
		"post":            func(v *Invocation) { v.Method = http.MethodPost },
		"service":         func(v *Invocation) { v.Service = "webpubsub" },
		"operation":       func(v *Invocation) { v.Operation = "Invoke" },
		"unknown op":      func(v *Invocation) { v.Operation = "Send" },
		"host":            func(v *Invocation) { v.URL = "wss://demo.service.signalr.net.attacker.test/client/?hub=chat_hub" },
		"private-link":    func(v *Invocation) { v.URL = "wss://privatelink.service.signalr.net/client/?hub=chat_hub" },
		"nested":          func(v *Invocation) { v.URL = "wss://nested.demo.service.signalr.net/client/?hub=chat_hub" },
		"numeric":         func(v *Invocation) { v.URL = "wss://123.service.signalr.net/client/?hub=chat_hub" },
		"path":            func(v *Invocation) { v.URL = "wss://demo.service.signalr.net/client/hubs/chat_hub" },
		"missing hub":     func(v *Invocation) { v.URL = "wss://demo.service.signalr.net/client/" },
		"token query":     func(v *Invocation) { v.URL = "wss://demo.service.signalr.net/client/?hub=chat_hub&access_token=caller" },
		"duplicate hub":   func(v *Invocation) { v.URL = "wss://demo.service.signalr.net/client/?hub=a&hub=b" },
		"header":          func(v *Invocation) { v.Headers = map[string]string{"Authorization": "Bearer caller"} },
		"parameter":       func(v *Invocation) { v.Parameters = map[string]any{"foo": "bar"} },
		"missing body":    func(v *Invocation) { v.Body = nil },
		"body file":       func(v *Invocation) { v.BodyFile = filepath.Join(root, "body") },
		"missing output":  func(v *Invocation) { v.ResponseFile = "" },
		"unknown field":   func(v *Invocation) { v.Body.(map[string]any)["roles"] = []any{"x"} },
		"credential":      func(v *Invocation) { v.Body.(map[string]any)["client_secret"] = "caller" },
		"bad user id":     func(v *Invocation) { v.Body.(map[string]any)["user_id"] = strings.Repeat("x", 129) },
		"expiry too long": func(v *Invocation) { v.Body.(map[string]any)["minutes_to_expire"] = 61 },
		"unbounded":       func(v *Invocation) { v.Body.(map[string]any)["timeout_seconds"] = 301 },
		"no messages":     func(v *Invocation) { v.Body.(map[string]any)["max_messages"] = 0 },
		"subscribe invoke": func(v *Invocation) {
			v.Body.(map[string]any)["invocations"] = []any{map[string]any{"id": "inv-1", "target": "Send", "arguments": []any{}}}
		},
		"region": func(v *Invocation) { v.Region = "eastus" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid()
			mutate(&candidate)
			if err := validateInvocation(candidate, []string{root}); err == nil {
				t.Fatal("unsafe SignalR invocation accepted")
			}
		})
	}
}

func TestAzureSignalRInvokeBoundaryRequiresForceAndBoundedInvocations(t *testing.T) {
	root := t.TempDir()
	valid := func() Invocation {
		return Invocation{
			Provider: ProviderAzure, Mode: ModeMutate, AuthScheme: "signalr-ws", Service: "signalr", Operation: "Invoke",
			Method: http.MethodGet, URL: "wss://demo.service.signalr.net/client/?hub=chat_hub",
			Body: map[string]any{
				"invocations": []any{
					map[string]any{"id": "inv-1", "target": "SendMessage", "arguments": []any{map[string]any{"text": "hello"}}},
					map[string]any{"id": "inv-2", "target": "JoinGroup", "arguments": []any{"room"}},
				},
				"max_messages": 4, "timeout_seconds": 30,
			},
			ResponseFile: filepath.Join(root, "invoke.ndjson"),
		}
	}
	if err := validateInvocation(valid(), []string{root}); err != nil {
		t.Fatal(err)
	}
	if classifyRead(ProviderAzure, valid()) {
		t.Fatal("SignalR Invoke was classified read-only")
	}
	for name, mutate := range map[string]func(*Invocation){
		"no invocations": func(v *Invocation) { v.Body.(map[string]any)["invocations"] = []any{} },
		"duplicate id": func(v *Invocation) {
			v.Body.(map[string]any)["invocations"] = []any{
				map[string]any{"id": "inv-1", "target": "A", "arguments": []any{}},
				map[string]any{"id": "inv-1", "target": "B", "arguments": []any{}},
			}
		},
		"bad id": func(v *Invocation) {
			v.Body.(map[string]any)["invocations"] = []any{map[string]any{"id": "bad\nid", "target": "A", "arguments": []any{}}}
		},
		"empty target": func(v *Invocation) {
			v.Body.(map[string]any)["invocations"] = []any{map[string]any{"id": "inv-1", "target": "", "arguments": []any{}}}
		},
		"credential argument": func(v *Invocation) {
			v.Body.(map[string]any)["invocations"] = []any{map[string]any{"id": "inv-1", "target": "A", "arguments": []any{map[string]any{"access_token": "caller"}}}}
		},
		"too many invocations": func(v *Invocation) {
			var invocations []any
			for index := 0; index < 65; index++ {
				invocations = append(invocations, map[string]any{"id": "inv-" + string(rune('a'+index)), "target": "A", "arguments": []any{}})
			}
			v.Body.(map[string]any)["invocations"] = invocations
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid()
			mutate(&candidate)
			if err := validateInvocation(candidate, []string{root}); err == nil {
				t.Fatal("unsafe SignalR Invoke invocation accepted")
			}
		})
	}
}

func TestAzureSignalRSubscribeStreamsSanitizedServerInvocations(t *testing.T) {
	root := t.TempDir()
	responseFile := filepath.Join(root, "events.ndjson")
	identity := &staticAzureTokenProvider{token: "private-entra-token"}
	handshakeAndFirst := append(signalRFrame(`{}`), signalRFrame(`{"type":1,"invocationId":"srv-1","target":"ReceiveMessage","arguments":[{"text":"hello"}]}`)...)
	pingAndSecondAndClose := append(signalRFrame(`{"type":6}`), signalRFrame(`{"type":1,"target":"Notify","arguments":[]}`)...)
	pingAndSecondAndClose = append(pingAndSecondAndClose, signalRFrame(`{"type":7}`)...)
	connection := &fakeTencentWebSocketConnection{reads: [][]byte{handshakeAndFirst, pingAndSecondAndClose}}
	var tokenRequest *http.Request
	var dialURL string
	adapter := NewAzureRESTAdapter(AzureRESTConfig{
		Tokens: identity,
		HTTP: doerFunc(func(request *http.Request) (*http.Response, error) {
			tokenRequest = request.Clone(request.Context())
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"token":"private-client-token"}`))}, nil
		}),
		SignalRWebSocketDial: func(_ context.Context, target string, _ http.Header) (cloudWebSocketConnection, error) {
			dialURL = target
			return connection, nil
		},
		StreamPause: func(context.Context, time.Duration) error { return nil },
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, Mode: ModeRead, AuthScheme: "signalr-ws", Service: "signalr", Operation: "Subscribe",
		Method: http.MethodGet, URL: "wss://demo.service.signalr.net/client/?hub=chat_hub",
		Body: map[string]any{
			"user_id": "observer", "minutes_to_expire": 5,
			"max_messages": 4, "timeout_seconds": 30,
		},
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if identity.calls != 1 || identity.scope != "https://signalr.azure.com/.default" {
		t.Fatalf("identity calls=%d scope=%q", identity.calls, identity.scope)
	}
	if tokenRequest == nil || tokenRequest.Method != http.MethodPost || tokenRequest.URL.Path != "/api/hubs/chat_hub/:generateToken" || tokenRequest.URL.Query().Get("api-version") != "2022-11-01" || tokenRequest.URL.Query().Get("minutesToExpire") != "5" || tokenRequest.URL.Query().Get("userId") != "observer" || tokenRequest.Header.Get("Authorization") != "Bearer private-entra-token" {
		t.Fatalf("token request=%#v", tokenRequest)
	}
	parsedURL, err := url.Parse(dialURL)
	if err != nil {
		t.Fatalf("dial URL %q is invalid: %v", dialURL, err)
	}
	if parsedURL.Query().Get("hub") != "chat_hub" || parsedURL.Query().Get("access_token") != "private-client-token" {
		t.Fatalf("dial URL %q is missing the internal access token", dialURL)
	}
	if len(connection.writes) != 2 {
		t.Fatalf("writes=%d, want handshake plus one completion", len(connection.writes))
	}
	if string(connection.writes[0].data) != string(signalRFrame(`{"protocol":"json","version":1}`)) {
		t.Fatalf("handshake frame=%q", connection.writes[0].data)
	}
	if !strings.Contains(string(connection.writes[1].data), `"type":3`) || !strings.Contains(string(connection.writes[1].data), `"invocationId":"srv-1"`) || connection.writes[1].data[len(connection.writes[1].data)-1] != azureSignalRRecordSeparator {
		t.Fatalf("completion frame=%q", connection.writes[1].data)
	}
	content, err := os.ReadFile(responseFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 3 {
		t.Fatalf("output lines=%d: %s", len(lines), content)
	}
	if !strings.Contains(string(content), `"target":"ReceiveMessage"`) || strings.Contains(string(content), "private-client-token") || strings.Contains(string(content), "private-entra-token") {
		t.Fatalf("sanitized output leaked or lost data: %s", content)
	}
	var metadata map[string]any
	if err := json.Unmarshal(result.Output, &metadata); err != nil || metadata["messages"] != float64(3) {
		t.Fatalf("metadata=%v err=%v", metadata, err)
	}
}

func TestAzureSignalRInvokeCorrelatesCompletions(t *testing.T) {
	root := t.TempDir()
	responseFile := filepath.Join(root, "invoke.ndjson")
	streamAndCompletion := append(signalRFrame(`{"type":2,"invocationId":"inv-1","item":{"seq":1}}`), signalRFrame(`{"type":3,"invocationId":"inv-1"}`)...)
	connection := &fakeTencentWebSocketConnection{reads: [][]byte{
		signalRFrame(`{}`),
		streamAndCompletion,
		signalRFrame(`{"type":3,"invocationId":"inv-2","result":"joined"}`),
	}}
	adapter := NewAzureRESTAdapter(AzureRESTConfig{
		Tokens: &staticAzureTokenProvider{token: "private-entra-token"},
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"token":"private-client-token"}`))}, nil
		}),
		SignalRWebSocketDial: func(_ context.Context, _ string, _ http.Header) (cloudWebSocketConnection, error) {
			return connection, nil
		},
		StreamPause: func(context.Context, time.Duration) error { return nil },
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, Mode: ModeMutate, AuthScheme: "signalr-ws", Service: "signalr", Operation: "Invoke",
		Method: http.MethodGet, URL: "wss://demo.service.signalr.net/client/?hub=chat_hub",
		Body: map[string]any{
			"invocations": []any{
				map[string]any{"id": "inv-1", "target": "SendMessage", "arguments": []any{map[string]any{"text": "hello"}}},
				map[string]any{"id": "inv-2", "target": "JoinGroup", "arguments": []any{"room"}},
			},
			"max_messages": 5, "timeout_seconds": 30,
		},
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(connection.writes) != 3 {
		t.Fatalf("writes=%d, want handshake plus two invocations", len(connection.writes))
	}
	if !strings.Contains(string(connection.writes[1].data), `"invocationId":"inv-1"`) || string(connection.writes[1].data)[len(connection.writes[1].data)-1] != azureSignalRRecordSeparator {
		t.Fatalf("invocation frame=%q", connection.writes[1].data)
	}
	if !strings.Contains(string(connection.writes[2].data), `"invocationId":"inv-2"`) {
		t.Fatalf("invocation frame=%q", connection.writes[2].data)
	}
	content, err := os.ReadFile(responseFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(content), "\n") != 3 {
		t.Fatalf("output=%s", content)
	}
	var metadata map[string]any
	if err := json.Unmarshal(result.Output, &metadata); err != nil || metadata["messages"] != float64(3) {
		t.Fatalf("metadata=%v err=%v", metadata, err)
	}
}

func TestAzureSignalRInvokeFailsAtomicallyOnProtocolErrors(t *testing.T) {
	for name, reads := range map[string][][]byte{
		"failed completion": {signalRFrame(`{}`), signalRFrame(`{"type":3,"invocationId":"inv-1","error":"hub rejected"}`)},
		"unexpected id":     {signalRFrame(`{}`), signalRFrame(`{"type":3,"invocationId":"other"}`)},
		"unsupported type":  {signalRFrame(`{}`), signalRFrame(`{"type":4,"invocationId":"inv-1","target":"X","arguments":[]}`)},
		"invalid frame":     {signalRFrame(`{}`), signalRFrame(`{"type":6}`), signalRFrame(`not-json`)},
		"handshake error":   {signalRFrame(`{"error":"unsupported protocol"}`)},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			responseFile := filepath.Join(root, "invoke.ndjson")
			connection := &fakeTencentWebSocketConnection{reads: reads}
			adapter := NewAzureRESTAdapter(AzureRESTConfig{
				Tokens: &staticAzureTokenProvider{token: "private-entra-token"},
				HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"token":"private-client-token"}`))}, nil
				}),
				SignalRWebSocketDial: func(_ context.Context, _ string, _ http.Header) (cloudWebSocketConnection, error) {
					return connection, nil
				},
			})
			_, err := adapter.Invoke(t.Context(), Invocation{
				Provider: ProviderAzure, Mode: ModeMutate, AuthScheme: "signalr-ws", Service: "signalr", Operation: "Invoke",
				Method: http.MethodGet, URL: "wss://demo.service.signalr.net/client/?hub=chat_hub",
				Body: map[string]any{
					"invocations":  []any{map[string]any{"id": "inv-1", "target": "SendMessage", "arguments": []any{}}},
					"max_messages": 5, "timeout_seconds": 5,
				},
				ResponseFile: responseFile, MaxResponseFileBytes: 4096,
			})
			if err == nil {
				t.Fatal("expected a protocol failure")
			}
			if _, statErr := os.Lstat(responseFile); statErr == nil {
				t.Fatal("failed session left a published response_file")
			} else if !os.IsNotExist(statErr) {
				t.Fatal(statErr)
			}
		})
	}
}

func TestAzureSignalRFrameReaderSplitsAndValidates(t *testing.T) {
	reader := &azureSignalRFrameReader{}
	connection := &fakeTencentWebSocketConnection{reads: [][]byte{
		append(signalRFrame(`{"type":6}`), signalRFrame(`{"type":7}`)...),
		[]byte(`{"type":1,`),
		[]byte(`"invocationId":"x",`),
		signalRFrame(`"target":"A","arguments":[]}`),
	}}
	first, err := reader.next(context.Background(), connection)
	if err != nil || string(first) != `{"type":6}` {
		t.Fatalf("first=%q err=%v", first, err)
	}
	second, err := reader.next(context.Background(), connection)
	if err != nil || string(second) != `{"type":7}` {
		t.Fatalf("second=%q err=%v", second, err)
	}
	third, err := reader.next(context.Background(), connection)
	if err != nil || string(third) != `{"type":1,"invocationId":"x","target":"A","arguments":[]}` {
		t.Fatalf("third=%q err=%v", third, err)
	}

	for name, candidate := range map[string]struct {
		reads     [][]byte
		readTypes []tencentWebSocketMessageType
	}{
		"empty frame":  {reads: [][]byte{[]byte("\x1e")}},
		"binary frame": {reads: [][]byte{signalRFrame(`{"type":6}`)}, readTypes: []tencentWebSocketMessageType{tencentWebSocketMessageBinary}},
		"invalid json": {reads: [][]byte{signalRFrame(`not-json`)}},
		"oversized":    {reads: [][]byte{[]byte(strings.Repeat("x", maxRequestPayloadBytes+2) + "\x1e")}},
		"no separator": {reads: [][]byte{[]byte(`{"type":6}`)}},
		"invalid utf8": {reads: [][]byte{[]byte{0xff, 0xfe, 0x1e}}},
	} {
		t.Run(name, func(t *testing.T) {
			single := &azureSignalRFrameReader{}
			_, err := single.next(context.Background(), &fakeTencentWebSocketConnection{reads: candidate.reads, readTypes: candidate.readTypes})
			if err == nil {
				t.Fatal("expected frame validation error")
			}
		})
	}
}

func TestAzureSignalRSanitizeRejectsUnsupportedFrames(t *testing.T) {
	if _, messageType, err := sanitizeAzureSignalRFrame([]byte(`{"type":6}`)); err != nil || messageType != 6 {
		t.Fatalf("ping type=%d err=%v", messageType, err)
	}
	if _, messageType, err := sanitizeAzureSignalRFrame([]byte(`{"type":7,"error":"gone"}`)); err != nil || messageType != 7 {
		t.Fatalf("close type=%d err=%v", messageType, err)
	}
	if _, messageType, err := sanitizeAzureSignalRFrame([]byte(`{"type":3,"invocationId":"a","result":42}`)); err != nil || messageType != 3 {
		t.Fatalf("completion type=%d err=%v", messageType, err)
	}
	if _, messageType, err := sanitizeAzureSignalRFrame([]byte(`{"type":2,"invocationId":"a","item":1}`)); err != nil || messageType != 2 {
		t.Fatalf("stream item type=%d err=%v", messageType, err)
	}
	for name, frame := range map[string]string{
		"missing type":     `{"target":"A"}`,
		"string type":      `{"type":"6"}`,
		"unknown type":     `{"type":9,"sequenceId":1}`,
		"stream invoke":    `{"type":4,"invocationId":"a","target":"A","arguments":[]}`,
		"cancel":           `{"type":5,"invocationId":"a"}`,
		"no target":        `{"type":1,"invocationId":"a","arguments":[]}`,
		"bad arguments":    `{"type":1,"invocationId":"a","target":"A","arguments":{}}`,
		"no id on item":    `{"type":2,"item":1}`,
		"result and error": `{"type":3,"invocationId":"a","result":1,"error":"x"}`,
		"bad close error":  `{"type":7,"error":5}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := sanitizeAzureSignalRFrame([]byte(frame)); err == nil {
				t.Fatal("unsafe SignalR frame accepted")
			}
		})
	}
}

func TestAzureSignalRTokenMintingFailsClosed(t *testing.T) {
	target, hub, err := parseAzureSignalRTarget("wss://demo.service.signalr.net/client/?hub=chat")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := parseAzureSignalRPlan(map[string]any{"user_id": "observer", "minutes_to_expire": 10, "max_messages": 1, "timeout_seconds": 5}, "subscribe")
	if err != nil {
		t.Fatal(err)
	}
	for name, candidate := range map[string]struct {
		status int
		body   string
	}{
		"http 403": {status: http.StatusForbidden, body: `{"error":"denied"}`},
		"invalid":  {status: http.StatusOK, body: `{"token":123}`},
		"empty":    {status: http.StatusOK, body: `{"token":""}`},
	} {
		t.Run(name, func(t *testing.T) {
			adapter := NewAzureRESTAdapter(AzureRESTConfig{
				HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: candidate.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(candidate.body))}, nil
				}),
			})
			if _, err := mintAzureSignalRClientToken(context.Background(), adapter, target, hub, plan, "private-entra-token"); err == nil {
				t.Fatal("expected token minting failure")
			}
		})
	}
	adapter := NewAzureRESTAdapter(AzureRESTConfig{HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network down")
	})})
	if _, err := mintAzureSignalRClientToken(context.Background(), adapter, target, hub, plan, "private-entra-token"); err == nil {
		t.Fatal("expected network failure")
	}
}

func TestAzureSignalRIdentityTokenMustBeNonEmpty(t *testing.T) {
	adapter := NewAzureRESTAdapter(AzureRESTConfig{
		Tokens: &staticAzureTokenProvider{token: "   "},
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("token minting must not run with an empty identity token")
			return nil, nil
		}),
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, Mode: ModeRead, AuthScheme: "signalr-ws", Service: "signalr", Operation: "Subscribe",
		Method: http.MethodGet, URL: "wss://demo.service.signalr.net/client/?hub=chat",
		Body:         map[string]any{"max_messages": 1, "timeout_seconds": 5},
		ResponseFile: filepath.Join(t.TempDir(), "events.ndjson"),
	})
	if err == nil || !strings.Contains(err.Error(), "empty access token") {
		t.Fatalf("err=%v", err)
	}
}

func TestAzureSignalRCloseWithErrorFailsSubscribe(t *testing.T) {
	root := t.TempDir()
	responseFile := filepath.Join(root, "events.ndjson")
	connection := &fakeTencentWebSocketConnection{reads: [][]byte{
		signalRFrame(`{}`),
		signalRFrame(`{"type":7,"error":"service disconnected"}`),
	}}
	adapter := NewAzureRESTAdapter(AzureRESTConfig{
		Tokens: &staticAzureTokenProvider{token: "private-entra-token"},
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"token":"private-client-token"}`))}, nil
		}),
		SignalRWebSocketDial: func(_ context.Context, _ string, _ http.Header) (cloudWebSocketConnection, error) {
			return connection, nil
		},
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, Mode: ModeRead, AuthScheme: "signalr-ws", Service: "signalr", Operation: "Subscribe",
		Method: http.MethodGet, URL: "wss://demo.service.signalr.net/client/?hub=chat",
		Body:         map[string]any{"max_messages": 4, "timeout_seconds": 5},
		ResponseFile: responseFile,
	})
	if err == nil || !strings.Contains(err.Error(), "service disconnected") {
		t.Fatalf("err=%v", err)
	}
	if _, statErr := os.Lstat(responseFile); statErr == nil {
		t.Fatal("failed session left a published response_file")
	}
}

func TestAzureSignalRMissingTerminalCompletionsFailInvoke(t *testing.T) {
	root := t.TempDir()
	responseFile := filepath.Join(root, "invoke.ndjson")
	connection := &fakeTencentWebSocketConnection{reads: [][]byte{
		signalRFrame(`{}`),
		signalRFrame(`{"type":3,"invocationId":"inv-1"}`),
		signalRFrame(`{"type":7}`),
	}}
	adapter := NewAzureRESTAdapter(AzureRESTConfig{
		Tokens: &staticAzureTokenProvider{token: "private-entra-token"},
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"token":"private-client-token"}`))}, nil
		}),
		SignalRWebSocketDial: func(_ context.Context, _ string, _ http.Header) (cloudWebSocketConnection, error) {
			return connection, nil
		},
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, Mode: ModeMutate, AuthScheme: "signalr-ws", Service: "signalr", Operation: "Invoke",
		Method: http.MethodGet, URL: "wss://demo.service.signalr.net/client/?hub=chat",
		Body: map[string]any{
			"invocations": []any{
				map[string]any{"id": "inv-1", "target": "A", "arguments": []any{}},
				map[string]any{"id": "inv-2", "target": "B", "arguments": []any{}},
			},
			"max_messages": 5, "timeout_seconds": 5,
		},
		ResponseFile: responseFile,
	})
	if err == nil || !strings.Contains(err.Error(), "before 1 invocation") {
		t.Fatalf("err=%v", err)
	}
}

func TestAzureSignalRWebSocketCloseStatusBreak(t *testing.T) {
	root := t.TempDir()
	responseFile := filepath.Join(root, "events.ndjson")
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			signalRFrame(`{}`),
			signalRFrame(`{"type":1,"target":"Notify","arguments":[]}`),
		},
		readErr: coderwebsocket.CloseError{Code: coderwebsocket.StatusNormalClosure},
	}
	adapter := NewAzureRESTAdapter(AzureRESTConfig{
		Tokens: &staticAzureTokenProvider{token: "private-entra-token"},
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"token":"private-client-token"}`))}, nil
		}),
		SignalRWebSocketDial: func(_ context.Context, _ string, _ http.Header) (cloudWebSocketConnection, error) {
			return connection, nil
		},
	})
	if _, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, Mode: ModeRead, AuthScheme: "signalr-ws", Service: "signalr", Operation: "Subscribe",
		Method: http.MethodGet, URL: "wss://demo.service.signalr.net/client/?hub=chat",
		Body:         map[string]any{"max_messages": 4, "timeout_seconds": 5},
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	}); err != nil {
		t.Fatal(err)
	}
}

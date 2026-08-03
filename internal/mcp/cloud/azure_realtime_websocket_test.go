package cloud

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAzureRealtimeWebSocketBoundarySupportsEntraAuthenticatedGAAndPreview(t *testing.T) {
	directory := t.TempDir()
	base := Invocation{
		Provider: ProviderAzure, Mode: ModeRead, AuthScheme: "realtime-ws", Service: "openai", Operation: "RealtimeResponse",
		Method: http.MethodGet, URL: "wss://demo.openai.azure.com/openai/v1/realtime",
		Parameters: map[string]any{"model": "gpt-realtime-deployment"},
		Body: []any{
			map[string]any{"type": "conversation.item.create", "item": map[string]any{"type": "message", "role": "user"}},
			map[string]any{"type": "response.create"},
		},
		ResponseFile: filepath.Join(directory, "events.ndjson"), StreamIntervalMS: 10,
	}
	if err := validateInvocation(base, []string{directory}); err != nil {
		t.Fatal(err)
	}
	if !classifyRead(ProviderAzure, base) {
		t.Fatal("Azure Realtime inference was not classified read-only")
	}
	preview := base
	preview.URL = "wss://demo.openai.azure.com/openai/realtime"
	preview.Parameters = map[string]any{"api-version": "2025-04-01-preview", "deployment": "gpt-realtime-preview"}
	preview.ResponseFile = filepath.Join(directory, "preview.ndjson")
	if err := validateInvocation(preview, []string{directory}); err != nil {
		t.Fatal(err)
	}

	for name, mutate := range map[string]func(*Invocation){
		"post method":        func(value *Invocation) { value.Method = http.MethodPost },
		"wrong host":         func(value *Invocation) { value.URL = "wss://example.com/openai/v1/realtime" },
		"nested public host": func(value *Invocation) { value.URL = "wss://nested.demo.openai.azure.com/openai/v1/realtime" },
		"private-link zone":  func(value *Invocation) { value.URL = "wss://privatelink.openai.azure.com/openai/v1/realtime" },
		"government host":    func(value *Invocation) { value.URL = "wss://demo.openai.azure.us/openai/v1/realtime" },
		"china host":         func(value *Invocation) { value.URL = "wss://demo.openai.azure.cn/openai/v1/realtime" },
		"wrong path":         func(value *Invocation) { value.URL = "wss://demo.openai.azure.com/openai/v1/chat" },
		"URL query":          func(value *Invocation) { value.URL += "?model=caller" },
		"missing body":       func(value *Invocation) { value.Body = nil },
		"missing response":   func(value *Invocation) { value.ResponseFile = "" },
		"handshake headers":  func(value *Invocation) { value.Headers = map[string]string{"OpenAI-Beta": "realtime=v1"} },
		"chunk control":      func(value *Invocation) { value.StreamChunkBytes = 10 },
		"bad GA query":       func(value *Invocation) { value.Parameters = map[string]any{"model": "x", "deployment": "y"} },
		"credential event":   func(value *Invocation) { value.Body = map[string]any{"type": "session.update", "api_key": "forbidden"} },
		"unknown scheme":     func(value *Invocation) { value.AuthScheme = "unknown" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := base
			candidate.Parameters = map[string]any{"model": "gpt-realtime-deployment"}
			candidate.Headers = nil
			mutate(&candidate)
			if err := validateInvocation(candidate, []string{directory}); err == nil {
				t.Fatal("invalid Azure Realtime invocation accepted")
			}
		})
	}
}

func TestAzureRealtimeWebSocketStreamsFiniteJSONEventsWithInternalEntraToken(t *testing.T) {
	directory := t.TempDir()
	responseFile := filepath.Join(directory, "events.ndjson")
	connection := &fakeTencentWebSocketConnection{reads: [][]byte{
		[]byte(`{"type":"session.created","event_id":"evt-session"}`),
		[]byte(`{"type":"response.created","response":{"id":"resp-1"}}`),
		[]byte(`{"type":"response.done","response":{"id":"resp-1","status":"completed"}}`),
	}}
	tokens := &staticAzureTokenProvider{token: "entra-private-token"}
	dialed := false
	adapter := NewAzureRESTAdapter(AzureRESTConfig{
		Tokens: tokens,
		WebSocketDial: func(_ context.Context, target string, headers http.Header) (cloudWebSocketConnection, error) {
			dialed = true
			parsed, err := url.Parse(target)
			if err != nil {
				return nil, err
			}
			if parsed.Scheme != "wss" || parsed.Query().Get("model") != "gpt-realtime-deployment" || strings.Contains(target, "entra-private-token") {
				return nil, fmt.Errorf("unexpected target %q", target)
			}
			if headers.Get("Authorization") != "Bearer entra-private-token" || len(headers) != 1 {
				return nil, fmt.Errorf("unexpected headers %#v", headers)
			}
			return connection, nil
		},
		StreamPause: func(context.Context, time.Duration) error { return nil },
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, Mode: ModeRead, AuthScheme: "realtime-ws", Service: "openai", Operation: "RealtimeResponse",
		Method: http.MethodGet, URL: "wss://demo.openai.azure.com/openai/v1/realtime",
		Parameters:   map[string]any{"model": "gpt-realtime-deployment"},
		Body:         []any{map[string]any{"type": "session.update", "session": map[string]any{"type": "realtime"}}, map[string]any{"type": "response.create"}},
		ResponseFile: responseFile, StreamIntervalMS: 1, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !dialed || tokens.calls != 1 || tokens.scope != "https://ai.azure.com/.default" {
		t.Fatalf("dialed=%v calls=%d scope=%q", dialed, tokens.calls, tokens.scope)
	}
	if len(connection.writes) != 2 || connection.writes[0].messageType != cloudWebSocketMessageText || !bytes.Contains(connection.writes[1].data, []byte(`"response.create"`)) {
		t.Fatalf("writes=%#v", connection.writes)
	}
	written, err := os.ReadFile(responseFile)
	if err != nil || !bytes.Contains(written, []byte(`"response.done"`)) {
		t.Fatalf("output=%s err=%v", written, err)
	}
	if strings.Contains(string(result.Output), "entra-private-token") || !bytes.Contains(result.Output, []byte(`"response_file"`)) {
		t.Fatalf("result=%s", result.Output)
	}
}

func TestAzureRealtimeWebSocketAcceptsNDJSONAndWaitsForEveryTranscriptionCommit(t *testing.T) {
	directory := t.TempDir()
	requestFile := filepath.Join(directory, "request.ndjson")
	responseFile := filepath.Join(directory, "response.ndjson")
	requestData := strings.Join([]string{
		`{"type":"session.update","session":{"type":"transcription"}}`,
		`{"type":"input_audio_buffer.append","audio":"AA=="}`,
		`{"type":"input_audio_buffer.commit"}`,
		`{"type":"input_audio_buffer.append","audio":"AQ=="}`,
		`{"type":"input_audio_buffer.commit"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(requestFile, []byte(requestData), 0o600); err != nil {
		t.Fatal(err)
	}
	connection := &fakeTencentWebSocketConnection{reads: [][]byte{
		[]byte(`{"type":"session.created"}`),
		[]byte(`{"type":"conversation.item.input_audio_transcription.completed","transcript":"one"}`),
		[]byte(`{"type":"conversation.item.input_audio_transcription.completed","transcript":"two"}`),
		[]byte(`{"type":"must.not.be.read"}`),
	}}
	adapter := NewAzureRESTAdapter(AzureRESTConfig{
		Tokens:        &staticAzureTokenProvider{token: "token"},
		WebSocketDial: func(context.Context, string, http.Header) (cloudWebSocketConnection, error) { return connection, nil },
		StreamPause:   func(context.Context, time.Duration) error { return nil },
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, AuthScheme: "realtime-ws", Service: "openai", Operation: "RealtimeTranscription",
		Method: http.MethodGet, URL: "wss://demo.openai.azure.com/openai/v1/realtime",
		Parameters: map[string]any{"intent": "transcription"}, BodyFile: requestFile,
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(responseFile)
	if err != nil || bytes.Count(written, []byte("input_audio_transcription.completed")) != 2 || bytes.Contains(written, []byte("must.not.be.read")) {
		t.Fatalf("output=%s err=%v", written, err)
	}
}

func TestAzureRealtimeWebSocketFailureNeverPublishesOutput(t *testing.T) {
	directory := t.TempDir()
	responseFile := filepath.Join(directory, "response.ndjson")
	connection := &fakeTencentWebSocketConnection{reads: [][]byte{[]byte(`{"type":"error","error":{"code":"invalid_request_error","message":"bad"}}`)}}
	adapter := NewAzureRESTAdapter(AzureRESTConfig{
		Tokens:        &staticAzureTokenProvider{token: "token"},
		WebSocketDial: func(context.Context, string, http.Header) (cloudWebSocketConnection, error) { return connection, nil },
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, AuthScheme: "realtime-ws", Service: "openai", Operation: "RealtimeResponse",
		Method: http.MethodGet, URL: "wss://demo.openai.azure.com/openai/v1/realtime", Parameters: map[string]any{"model": "deployment"},
		Body: map[string]any{"type": "response.create"}, ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err == nil || strings.Contains(err.Error(), "bad") {
		t.Fatalf("unsafe or missing error: %v", err)
	}
	if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
		t.Fatalf("failed response published: %v", statErr)
	}
}

func TestAzureRealtimeWebSocketRejectsMalformedResponseDone(t *testing.T) {
	directory := t.TempDir()
	responseFile := filepath.Join(directory, "response.ndjson")
	connection := &fakeTencentWebSocketConnection{reads: [][]byte{[]byte(`{"type":"response.done"}`)}}
	adapter := NewAzureRESTAdapter(AzureRESTConfig{
		Tokens:        &staticAzureTokenProvider{token: "token"},
		WebSocketDial: func(context.Context, string, http.Header) (cloudWebSocketConnection, error) { return connection, nil },
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, AuthScheme: "realtime-ws", Service: "openai", Operation: "RealtimeResponse",
		Method: http.MethodGet, URL: "wss://demo.openai.azure.com/openai/v1/realtime", Parameters: map[string]any{"model": "deployment"},
		Body: map[string]any{"type": "response.create"}, ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err == nil {
		t.Fatal("malformed response.done accepted")
	}
	if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
		t.Fatalf("malformed response published: %v", statErr)
	}
}

func TestAzureRealtimeMalformedNDJSONIsRejectedBeforeCredentialsOrDial(t *testing.T) {
	directory := t.TempDir()
	requestFile := filepath.Join(directory, "request.ndjson")
	if err := os.WriteFile(requestFile, []byte("not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tokens := &staticAzureTokenProvider{token: "must-not-be-used"}
	dialed := false
	adapter := NewAzureRESTAdapter(AzureRESTConfig{
		Tokens: tokens,
		WebSocketDial: func(context.Context, string, http.Header) (cloudWebSocketConnection, error) {
			dialed = true
			return nil, fmt.Errorf("must not dial")
		},
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, AuthScheme: "realtime-ws", Service: "openai", Operation: "RealtimeResponse",
		Method: http.MethodGet, URL: "wss://demo.openai.azure.com/openai/v1/realtime", Parameters: map[string]any{"model": "deployment"},
		BodyFile: requestFile, ResponseFile: filepath.Join(directory, "response.ndjson"),
	})
	if err == nil || tokens.calls != 0 || dialed {
		t.Fatalf("err=%v token calls=%d dialed=%v", err, tokens.calls, dialed)
	}
}

func FuzzAzureOpenAIRealtimeHostNeverEscapesPublicCloud(f *testing.F) {
	for _, seed := range []string{
		"demo.openai.azure.com",
		"nested.demo.openai.azure.com",
		"demo.openai.azure.us",
		"demo.openai.azure.cn",
		"demo.openai.azure.com.example.com",
		"privatelink.openai.azure.com",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, host string) {
		target := (&url.URL{Scheme: "wss", Host: host, Path: "/openai/v1/realtime"}).String()
		invocation := Invocation{
			Provider: ProviderAzure, Mode: ModeRead, AuthScheme: authSchemeAzureRealtimeWS,
			Service: "openai", Operation: "RealtimeResponse", Method: http.MethodGet,
			URL: target, Parameters: map[string]any{"model": "deployment"},
			Body: map[string]any{"type": "response.create"}, ResponseFile: "/approved/realtime.ndjson",
		}
		if validateAzureRealtimeWebSocketInvocation(invocation) != nil {
			return
		}
		parsed, err := url.Parse(target)
		if err != nil {
			t.Fatal(err)
		}
		normalized := strings.ToLower(parsed.Hostname())
		resource := strings.TrimSuffix(normalized, ".openai.azure.com")
		if resource == normalized || resource == "privatelink" || !endpointLabelPattern.MatchString(resource) {
			t.Fatalf("accepted non-public or non-single-label Azure OpenAI Realtime host %q", normalized)
		}
	})
}

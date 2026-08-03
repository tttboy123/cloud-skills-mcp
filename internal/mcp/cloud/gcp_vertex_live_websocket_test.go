package cloud

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGCPVertexLiveWebSocketBoundaryUsesADCAndExactOfficialProtocol(t *testing.T) {
	root := t.TempDir()
	valid := func() Invocation {
		return Invocation{
			Provider: ProviderGCP, Mode: ModeMutate, AuthScheme: "vertex-live-ws",
			Service: "aiplatform", Operation: "BidiGenerateContent", Project: "project-1", Region: "us-central1",
			Method: http.MethodGet,
			URL:    "wss://us-central1-aiplatform.googleapis.com/ws/google.cloud.aiplatform.v1beta1.LlmBidiService/BidiGenerateContent",
			Body: map[string]any{
				"messages": []any{
					map[string]any{"setup": map[string]any{"model": "projects/project-1/locations/us-central1/publishers/google/models/gemini-live-2.5-flash"}},
					map[string]any{"clientContent": map[string]any{"turns": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hello"}}}}, "turnComplete": true}},
				},
				"max_messages": 4, "timeout_seconds": 30,
			},
			ResponseFile: filepath.Join(root, "live.ndjson"), StreamIntervalMS: 10,
		}
	}
	base := valid()
	if err := validateInvocation(base, []string{root}); err != nil {
		t.Fatal(err)
	}
	if classifyRead(ProviderGCP, base) {
		t.Fatal("Vertex Live session was incorrectly classified read-only")
	}
	global := valid()
	global.Region = "global"
	global.URL = "wss://aiplatform.googleapis.com/ws/google.cloud.aiplatform.v1beta1.LlmBidiService/BidiGenerateContent"
	global.Body = gcpVertexLiveTestBody("project-1", "global")
	global.ResponseFile = filepath.Join(root, "global.ndjson")
	if err := validateInvocation(global, []string{root}); err != nil {
		t.Fatalf("global Vertex Live endpoint rejected: %v", err)
	}
	multiRegion := valid()
	multiRegion.Region = "us"
	multiRegion.URL = "wss://aiplatform.us.rep.googleapis.com/ws/google.cloud.aiplatform.v1beta1.LlmBidiService/BidiGenerateContent"
	multiRegion.Body = gcpVertexLiveTestBody("project-1", "us")
	multiRegion.ResponseFile = filepath.Join(root, "us.ndjson")
	if err := validateInvocation(multiRegion, []string{root}); err != nil {
		t.Fatalf("multi-region Vertex Live endpoint rejected: %v", err)
	}

	for name, mutate := range map[string]func(*Invocation){
		"post method":      func(value *Invocation) { value.Method = http.MethodPost },
		"wrong service":    func(value *Invocation) { value.Service = "generativelanguage" },
		"wrong operation":  func(value *Invocation) { value.Operation = "GenerateContent" },
		"missing project":  func(value *Invocation) { value.Project = "" },
		"invalid project":  func(value *Invocation) { value.Project = "project 1" },
		"uppercase region": func(value *Invocation) { value.Region = "US-CENTRAL1" },
		"wrong host": func(value *Invocation) {
			value.URL = "wss://aiplatform.googleapis.com/ws/google.cloud.aiplatform.v1beta1.LlmBidiService/BidiGenerateContent"
		},
		"wrong path": func(value *Invocation) {
			value.URL = "wss://us-central1-aiplatform.googleapis.com/v1/projects/project-1"
		},
		"query token":    func(value *Invocation) { value.URL += "?access_token=caller" },
		"parameters":     func(value *Invocation) { value.Parameters = map[string]any{"key": "caller"} },
		"auth header":    func(value *Invocation) { value.Headers = map[string]string{"Authorization": "Bearer caller"} },
		"missing body":   func(value *Invocation) { value.Body = nil },
		"body file":      func(value *Invocation) { value.BodyFile = filepath.Join(root, "messages.ndjson") },
		"missing output": func(value *Invocation) { value.ResponseFile = "" },
		"binary chunk":   func(value *Invocation) { value.StreamChunkBytes = 1024 },
		"cross provider": func(value *Invocation) { value.Subscription = "subscription" },
		"setup not first": func(value *Invocation) {
			value.Body = map[string]any{"messages": []any{map[string]any{"clientContent": map[string]any{}}, map[string]any{"setup": map[string]any{"model": "projects/project-1/locations/us-central1/publishers/google/models/model"}}}, "max_messages": 1, "timeout_seconds": 1}
		},
		"multiple fields": func(value *Invocation) {
			value.Body = map[string]any{"messages": []any{map[string]any{"setup": map[string]any{"model": "projects/project-1/locations/us-central1/publishers/google/models/model"}, "clientContent": map[string]any{}}}, "max_messages": 1, "timeout_seconds": 1}
		},
		"model project mismatch": func(value *Invocation) {
			value.Body = gcpVertexLiveTestBody("other-project", "us-central1")
		},
		"model location mismatch": func(value *Invocation) {
			value.Body = gcpVertexLiveTestBody("project-1", "global")
		},
		"invalid model id": func(value *Invocation) {
			value.Body = gcpVertexLiveTestBody("project-1", "us-central1")
			value.Body.(map[string]any)["messages"].([]any)[0].(map[string]any)["setup"].(map[string]any)["model"] = "projects/project-1/locations/us-central1/publishers/google/models/model name"
		},
		"credential body": func(value *Invocation) {
			value.Body = map[string]any{"messages": []any{map[string]any{"setup": map[string]any{"model": "projects/project-1/locations/us-central1/publishers/google/models/model", "accessToken": "caller"}}}, "max_messages": 1, "timeout_seconds": 1}
		},
		"unbounded responses": func(value *Invocation) {
			value.Body = gcpVertexLiveTestBody("project-1", "us-central1")
			value.Body.(map[string]any)["max_messages"] = 257
		},
		"setup-only response bound": func(value *Invocation) {
			value.Body = gcpVertexLiveTestBody("project-1", "us-central1")
			value.Body.(map[string]any)["max_messages"] = 1
		},
		"unbounded timeout": func(value *Invocation) {
			value.Body = gcpVertexLiveTestBody("project-1", "us-central1")
			value.Body.(map[string]any)["timeout_seconds"] = 301
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid()
			if name == "body file" {
				if err := os.WriteFile(filepath.Join(root, "messages.ndjson"), []byte("{}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			mutate(&candidate)
			if err := validateInvocation(candidate, []string{root}); err == nil {
				t.Fatal("unsafe Vertex Live WebSocket invocation accepted")
			}
		})
	}
}

func TestGCPVertexLiveWebSocketUsesInternalADCAndProtocolOrdering(t *testing.T) {
	root := t.TempDir()
	responseFile := filepath.Join(root, "live.ndjson")
	connection := &orderedGCPVertexLiveConnection{}
	tokens := &staticTokenProvider{token: "private-adc-token"}
	var pauses []time.Duration
	var target string
	var handshake http.Header
	adapter := NewGCPRESTAdapter(GCPRESTConfig{
		Tokens: tokens,
		VertexLiveWebSocketDial: func(_ context.Context, rawURL string, headers http.Header) (cloudWebSocketConnection, error) {
			target = rawURL
			handshake = headers.Clone()
			return connection, nil
		},
		StreamPause: func(_ context.Context, duration time.Duration) error {
			pauses = append(pauses, duration)
			return nil
		},
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderGCP, Mode: ModeMutate, AuthScheme: "vertex-live-ws",
		Service: "aiplatform", Operation: "BidiGenerateContent", Project: "project-1", Region: "us-central1",
		Method: http.MethodGet,
		URL:    "wss://us-central1-aiplatform.googleapis.com/ws/google.cloud.aiplatform.v1beta1.LlmBidiService/BidiGenerateContent",
		Body: map[string]any{
			"messages": []any{
				map[string]any{"setup": map[string]any{"model": "projects/project-1/locations/us-central1/publishers/google/models/gemini-live-2.5-flash"}},
				map[string]any{"clientContent": map[string]any{"turns": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hello"}}}}, "turnComplete": true}},
				map[string]any{"realtimeInput": map[string]any{"text": "more"}},
			},
			"max_messages": 2, "timeout_seconds": 30,
		},
		ResponseFile: responseFile, StreamIntervalMS: 25, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if target != "wss://us-central1-aiplatform.googleapis.com/ws/google.cloud.aiplatform.v1beta1.LlmBidiService/BidiGenerateContent" || strings.Contains(target, "private-adc-token") {
		t.Fatalf("target=%q", target)
	}
	if handshake.Get("Authorization") != "Bearer private-adc-token" || len(handshake) != 1 || tokens.calls != 1 {
		t.Fatalf("handshake=%#v token calls=%d", handshake, tokens.calls)
	}
	if connection.state != 5 || len(connection.writes) != 3 || len(pauses) != 1 || pauses[0] != 25*time.Millisecond {
		t.Fatalf("state=%d writes=%d pauses=%v", connection.state, len(connection.writes), pauses)
	}
	data, err := os.ReadFile(responseFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte{'\n'})
	if len(lines) != 2 || !bytes.Contains(lines[0], []byte(`"setupComplete"`)) || !bytes.Contains(lines[1], []byte(`"turnComplete":true`)) || bytes.Contains(data, []byte("private-adc-token")) {
		t.Fatalf("output=%s", data)
	}
	if strings.Contains(string(result.Output), "private-adc-token") || !bytes.Contains(result.Output, []byte(`"messages":2`)) {
		t.Fatalf("result=%s", result.Output)
	}
}

func TestGCPVertexLiveWebSocketFailuresNeverPublishOutput(t *testing.T) {
	for name, reads := range map[string][][]byte{
		"missing setup complete": {[]byte(`{"serverContent":{"turnComplete":true}}`)},
		"provider error":         {[]byte(`{"setupComplete":{}}`), []byte(`{"error":{"code":400,"message":"bad"}}`)},
		"invalid JSON":           {[]byte(`{"setupComplete":{}}`), []byte(`not-json`)},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			responseFile := filepath.Join(root, "live.ndjson")
			connection := &fakeTencentWebSocketConnection{reads: reads}
			adapter := NewGCPRESTAdapter(GCPRESTConfig{
				Tokens:                  &staticTokenProvider{token: "token"},
				VertexLiveWebSocketDial: func(context.Context, string, http.Header) (cloudWebSocketConnection, error) { return connection, nil },
			})
			_, err := adapter.Invoke(t.Context(), Invocation{
				Provider: ProviderGCP, Mode: ModeMutate, AuthScheme: "vertex-live-ws",
				Service: "aiplatform", Operation: "BidiGenerateContent", Project: "project-1", Region: "global", Method: http.MethodGet,
				URL:  "wss://aiplatform.googleapis.com/ws/google.cloud.aiplatform.v1beta1.LlmBidiService/BidiGenerateContent",
				Body: gcpVertexLiveTestBody("project-1", "global"), ResponseFile: responseFile,
			})
			if err == nil {
				t.Fatal("invalid Vertex Live session succeeded")
			}
			if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
				t.Fatalf("failed session published output: %v", statErr)
			}
		})
	}
}

func gcpVertexLiveTestBody(project, location string) map[string]any {
	return map[string]any{
		"messages": []any{
			map[string]any{"setup": map[string]any{"model": fmt.Sprintf("projects/%s/locations/%s/publishers/google/models/model", project, location)}},
			map[string]any{"clientContent": map[string]any{"turns": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hello"}}}}, "turnComplete": true}},
		},
		"max_messages": 2, "timeout_seconds": 30,
	}
}

type orderedGCPVertexLiveConnection struct {
	state  int
	writes [][]byte
}

func (connection *orderedGCPVertexLiveConnection) Write(_ context.Context, messageType cloudWebSocketMessageType, data []byte) error {
	if messageType != cloudWebSocketMessageText {
		return fmt.Errorf("message type=%d", messageType)
	}
	switch connection.state {
	case 0:
		if !bytes.Contains(data, []byte(`"setup"`)) {
			return fmt.Errorf("first message is not setup")
		}
		connection.state = 1
	case 2:
		if !bytes.Contains(data, []byte(`"clientContent"`)) {
			return fmt.Errorf("second message is not clientContent")
		}
		connection.state = 3
	case 3:
		if !bytes.Contains(data, []byte(`"realtimeInput"`)) {
			return fmt.Errorf("third message is not realtimeInput")
		}
		connection.state = 4
	default:
		return fmt.Errorf("write in state %d", connection.state)
	}
	connection.writes = append(connection.writes, append([]byte(nil), data...))
	return nil
}

func (connection *orderedGCPVertexLiveConnection) Read(context.Context) (cloudWebSocketMessageType, []byte, error) {
	switch connection.state {
	case 1:
		connection.state = 2
		return cloudWebSocketMessageText, []byte(`{"setupComplete":{"sessionId":"session-1"}}`), nil
	case 4:
		connection.state = 5
		return cloudWebSocketMessageText, []byte(`{"serverContent":{"turnComplete":true}}`), nil
	default:
		return 0, nil, io.EOF
	}
}

func (*orderedGCPVertexLiveConnection) Close() error { return nil }

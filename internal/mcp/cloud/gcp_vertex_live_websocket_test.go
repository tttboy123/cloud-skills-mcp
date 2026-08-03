package cloud

import (
	"bytes"
	"context"
	"encoding/json"
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
		"caller session handle": func(value *Invocation) {
			value.Body = gcpVertexLiveTestBody("project-1", "us-central1")
			value.Body.(map[string]any)["messages"].([]any)[0].(map[string]any)["setup"].(map[string]any)["sessionResumption"] = map[string]any{"handle": "caller-handle", "transparent": true}
		},
		"duplicate tool handler": func(value *Invocation) {
			value.Body = gcpVertexLiveTestBody("project-1", "us-central1")
			value.Body.(map[string]any)["tool_handlers"] = []any{
				map[string]any{"name": "lookup_weather", "response": map[string]any{"temperature": 21}, "max_calls": 1},
				map[string]any{"name": "lookup_weather", "response": map[string]any{"temperature": 22}, "max_calls": 1},
			}
		},
		"credential tool response": func(value *Invocation) {
			value.Body = gcpVertexLiveTestBody("project-1", "us-central1")
			value.Body.(map[string]any)["tool_handlers"] = []any{map[string]any{"name": "lookup_weather", "response": map[string]any{"accessToken": "caller"}, "max_calls": 1}}
		},
		"reconnects without resumption": func(value *Invocation) {
			value.Body = gcpVertexLiveTestBody("project-1", "us-central1")
			value.Body.(map[string]any)["max_reconnects"] = 1
		},
		"unbounded reconnects": func(value *Invocation) {
			value.Body = gcpVertexLiveTestBody("project-1", "us-central1")
			value.Body.(map[string]any)["resume_on_go_away"] = true
			value.Body.(map[string]any)["max_reconnects"] = 9
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

func TestGCPVertexLiveWebSocketDispatchesBoundedDynamicToolResponses(t *testing.T) {
	root := t.TempDir()
	responseFile := filepath.Join(root, "tool-live.ndjson")
	connection := &gcpVertexToolCallConnection{}
	adapter := NewGCPRESTAdapter(GCPRESTConfig{
		Tokens: &staticTokenProvider{token: "private-adc-token"},
		VertexLiveWebSocketDial: func(context.Context, string, http.Header) (cloudWebSocketConnection, error) {
			return connection, nil
		},
	})
	body := gcpVertexLiveTestBody("project-1", "global")
	body["max_messages"] = 3
	body["tool_handlers"] = []any{
		map[string]any{"name": "lookup_weather", "response": map[string]any{"temperature": 21, "unit": "celsius"}, "max_calls": 1},
	}
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderGCP, Mode: ModeMutate, AuthScheme: "vertex-live-ws",
		Service: "aiplatform", Operation: "BidiGenerateContent", Project: "project-1", Region: "global", Method: http.MethodGet,
		URL:  "wss://aiplatform.googleapis.com/ws/google.cloud.aiplatform.v1beta1.LlmBidiService/BidiGenerateContent",
		Body: body, ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if connection.state != 6 || len(connection.writes) != 3 {
		t.Fatalf("state=%d writes=%d", connection.state, len(connection.writes))
	}
	var response struct {
		ToolResponse struct {
			FunctionResponses []struct {
				ID       string         `json:"id"`
				Name     string         `json:"name"`
				Response map[string]any `json:"response"`
			} `json:"functionResponses"`
		} `json:"toolResponse"`
	}
	if err := json.Unmarshal(connection.writes[2], &response); err != nil {
		t.Fatal(err)
	}
	if len(response.ToolResponse.FunctionResponses) != 1 || response.ToolResponse.FunctionResponses[0].ID != "call-1" || response.ToolResponse.FunctionResponses[0].Name != "lookup_weather" || response.ToolResponse.FunctionResponses[0].Response["temperature"] != float64(21) {
		t.Fatalf("tool response=%s", connection.writes[2])
	}
	data, err := os.ReadFile(responseFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"toolCall"`)) || bytes.Contains(data, []byte(`"toolResponse"`)) || bytes.Contains(data, []byte("private-adc-token")) || !bytes.Contains(result.Output, []byte(`"tool_calls":1`)) {
		t.Fatalf("output=%s result=%s", data, result.Output)
	}
}

func TestGCPVertexLiveWebSocketResumesInternallyAndReplaysOnlyUnacknowledgedMessages(t *testing.T) {
	root := t.TempDir()
	responseFile := filepath.Join(root, "resumed.ndjson")
	first := &gcpVertexResumeFirstConnection{}
	second := &gcpVertexResumeSecondConnection{}
	connections := []cloudWebSocketConnection{first, second}
	tokens := &staticTokenProvider{token: "private-adc-token"}
	var handshakes []http.Header
	adapter := NewGCPRESTAdapter(GCPRESTConfig{
		Tokens: tokens,
		VertexLiveWebSocketDial: func(_ context.Context, _ string, headers http.Header) (cloudWebSocketConnection, error) {
			handshakes = append(handshakes, headers.Clone())
			if len(connections) == 0 {
				return nil, fmt.Errorf("unexpected third connection")
			}
			connection := connections[0]
			connections = connections[1:]
			return connection, nil
		},
	})
	body := gcpVertexLiveTestBody("project-1", "global")
	body["messages"] = append(body["messages"].([]any), map[string]any{"realtimeInput": map[string]any{"text": "unacknowledged"}})
	body["max_messages"] = 8
	body["resume_on_go_away"] = true
	body["max_reconnects"] = 1
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderGCP, Mode: ModeMutate, AuthScheme: "vertex-live-ws",
		Service: "aiplatform", Operation: "BidiGenerateContent", Project: "project-1", Region: "global", Method: http.MethodGet,
		URL:  "wss://aiplatform.googleapis.com/ws/google.cloud.aiplatform.v1beta1.LlmBidiService/BidiGenerateContent",
		Body: body, ResponseFile: responseFile, MaxResponseFileBytes: 8192,
	})
	if err != nil {
		t.Fatal(err)
	}
	if tokens.calls != 2 || len(handshakes) != 2 || handshakes[0].Get("Authorization") != "Bearer private-adc-token" || handshakes[1].Get("Authorization") != "Bearer private-adc-token" {
		t.Fatalf("token calls=%d handshakes=%#v", tokens.calls, handshakes)
	}
	if first.state != 5 || second.state != 4 || len(second.writes) != 2 || bytes.Contains(second.writes[1], []byte(`"clientContent"`)) || !bytes.Contains(second.writes[1], []byte(`"unacknowledged"`)) {
		t.Fatalf("first=%d second=%d writes=%q", first.state, second.state, second.writes)
	}
	if !bytes.Contains(second.writes[0], []byte(`"sessionResumption":{"handle":"private-session-handle","transparent":true}`)) {
		t.Fatalf("resume setup=%s", second.writes[0])
	}
	data, err := os.ReadFile(responseFile)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("private-session-handle")) || bytes.Contains(data, []byte("private-adc-token")) || bytes.Contains(result.Output, []byte("private-session-handle")) || !bytes.Contains(result.Output, []byte(`"reconnects":1`)) {
		t.Fatalf("output=%s result=%s", data, result.Output)
	}
	if !bytes.Contains(data, []byte(`"sessionResumptionUpdate":{"lastConsumedClientMessageIndex":"1","resumable":true}`)) {
		t.Fatalf("sanitized update missing: %s", data)
	}
}

func TestGCPVertexLiveToolResponseRejectsDuplicateAndOverLimitBatchesAtomically(t *testing.T) {
	for name, raw := range map[string]string{
		"duplicate id":                `{"functionCalls":[{"id":"call-1","name":"lookup"},{"id":"call-1","name":"lookup"}]}`,
		"batch exceeds handler limit": `{"functionCalls":[{"id":"call-1","name":"lookup"},{"id":"call-2","name":"lookup"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			handler := &gcpVertexLiveToolHandlerState{response: json.RawMessage(`{"ok":true}`), remaining: 1}
			seen := map[string]struct{}{}
			if _, _, err := buildGCPVertexLiveToolResponse(json.RawMessage(raw), map[string]*gcpVertexLiveToolHandlerState{"lookup": handler}, seen); err == nil {
				t.Fatal("unsafe tool-call batch accepted")
			}
			if handler.remaining != 1 || len(seen) != 0 {
				t.Fatalf("failed batch mutated state: remaining=%d seen=%v", handler.remaining, seen)
			}
		})
	}
}

func TestGCPVertexLiveToolResponsePreservesJSONIntegerPrecision(t *testing.T) {
	handler := &gcpVertexLiveToolHandlerState{response: json.RawMessage(`{"resourceId":9007199254740993}`), remaining: 1}
	response, _, err := buildGCPVertexLiveToolResponse(
		json.RawMessage(`{"functionCalls":[{"id":"call-1","name":"lookup"}]}`),
		map[string]*gcpVertexLiveToolHandlerState{"lookup": handler},
		map[string]struct{}{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(response, []byte(`"resourceId":9007199254740993`)) {
		t.Fatalf("integer precision changed: %s", response)
	}
}

func TestGCPVertexLiveWebSocketResumesAfterUnexpectedTransportClose(t *testing.T) {
	root := t.TempDir()
	first := &gcpVertexResumeEOFConnection{}
	second := &gcpVertexResumeSecondConnection{}
	connections := []cloudWebSocketConnection{first, second}
	adapter := NewGCPRESTAdapter(GCPRESTConfig{
		Tokens: &staticTokenProvider{token: "token"},
		VertexLiveWebSocketDial: func(context.Context, string, http.Header) (cloudWebSocketConnection, error) {
			connection := connections[0]
			connections = connections[1:]
			return connection, nil
		},
	})
	body := gcpVertexLiveTestBody("project-1", "global")
	body["messages"] = append(body["messages"].([]any), map[string]any{"realtimeInput": map[string]any{"text": "unacknowledged"}})
	body["max_messages"] = 8
	body["resume_on_go_away"] = true
	body["max_reconnects"] = 1
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderGCP, Mode: ModeMutate, AuthScheme: "vertex-live-ws",
		Service: "aiplatform", Operation: "BidiGenerateContent", Project: "project-1", Region: "global", Method: http.MethodGet,
		URL:  "wss://aiplatform.googleapis.com/ws/google.cloud.aiplatform.v1beta1.LlmBidiService/BidiGenerateContent",
		Body: body, ResponseFile: filepath.Join(root, "transport-resume.ndjson"), MaxResponseFileBytes: 8192,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.state != 4 || !bytes.Contains(result.Output, []byte(`"reconnects":1`)) {
		t.Fatalf("second state=%d result=%s", second.state, result.Output)
	}
}

func TestGCPVertexLiveWebSocketUnknownToolFailsWithoutPublishingOutput(t *testing.T) {
	root := t.TempDir()
	responseFile := filepath.Join(root, "unknown-tool.ndjson")
	adapter := NewGCPRESTAdapter(GCPRESTConfig{
		Tokens: &staticTokenProvider{token: "token"},
		VertexLiveWebSocketDial: func(context.Context, string, http.Header) (cloudWebSocketConnection, error) {
			return &gcpVertexToolCallConnection{}, nil
		},
	})
	body := gcpVertexLiveTestBody("project-1", "global")
	body["max_messages"] = 3
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderGCP, Mode: ModeMutate, AuthScheme: "vertex-live-ws",
		Service: "aiplatform", Operation: "BidiGenerateContent", Project: "project-1", Region: "global", Method: http.MethodGet,
		URL:  "wss://aiplatform.googleapis.com/ws/google.cloud.aiplatform.v1beta1.LlmBidiService/BidiGenerateContent",
		Body: body, ResponseFile: responseFile,
	})
	if err == nil || !strings.Contains(err.Error(), "unapproved tool") {
		t.Fatalf("error=%v", err)
	}
	if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
		t.Fatalf("failed tool session published output: %v", statErr)
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

type gcpVertexToolCallConnection struct {
	state  int
	writes [][]byte
}

func (connection *gcpVertexToolCallConnection) Write(_ context.Context, messageType cloudWebSocketMessageType, data []byte) error {
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
			return fmt.Errorf("second message is not client content")
		}
		connection.state = 3
	case 4:
		if !bytes.Contains(data, []byte(`"toolResponse"`)) {
			return fmt.Errorf("third message is not tool response")
		}
		connection.state = 5
	default:
		return fmt.Errorf("write in state %d", connection.state)
	}
	connection.writes = append(connection.writes, append([]byte(nil), data...))
	return nil
}

func (connection *gcpVertexToolCallConnection) Read(context.Context) (cloudWebSocketMessageType, []byte, error) {
	switch connection.state {
	case 1:
		connection.state = 2
		return cloudWebSocketMessageText, []byte(`{"setupComplete":{}}`), nil
	case 3:
		connection.state = 4
		return cloudWebSocketMessageText, []byte(`{"toolCall":{"functionCalls":[{"id":"call-1","name":"lookup_weather","args":{"city":"Singapore"}}]}}`), nil
	case 5:
		connection.state = 6
		return cloudWebSocketMessageText, []byte(`{"serverContent":{"turnComplete":true}}`), nil
	default:
		return 0, nil, io.EOF
	}
}

func (*gcpVertexToolCallConnection) Close() error { return nil }

type gcpVertexResumeFirstConnection struct {
	state int
}

func (connection *gcpVertexResumeFirstConnection) Write(_ context.Context, messageType cloudWebSocketMessageType, data []byte) error {
	if messageType != cloudWebSocketMessageText {
		return fmt.Errorf("message type=%d", messageType)
	}
	switch connection.state {
	case 0:
		if !bytes.Contains(data, []byte(`"sessionResumption":{"transparent":true}`)) || bytes.Contains(data, []byte(`"handle"`)) {
			return fmt.Errorf("initial setup does not enable transparent resumption: %s", data)
		}
		connection.state = 1
	case 2:
		if !bytes.Contains(data, []byte(`"clientContent"`)) {
			return fmt.Errorf("first client message missing")
		}
		connection.state = 3
	case 3:
		if !bytes.Contains(data, []byte(`"unacknowledged"`)) {
			return fmt.Errorf("second client message missing")
		}
		connection.state = 4
	default:
		return fmt.Errorf("write in state %d", connection.state)
	}
	return nil
}

func (connection *gcpVertexResumeFirstConnection) Read(context.Context) (cloudWebSocketMessageType, []byte, error) {
	switch connection.state {
	case 1:
		connection.state = 2
		return cloudWebSocketMessageText, []byte(`{"setupComplete":{}}`), nil
	case 4:
		connection.state = 5
		return cloudWebSocketMessageText, []byte(`{"sessionResumptionUpdate":{"newHandle":"private-session-handle","resumable":true,"lastConsumedClientMessageIndex":"1"}}`), nil
	case 5:
		return cloudWebSocketMessageText, []byte(`{"goAway":{"timeLeft":"60s"}}`), nil
	default:
		return 0, nil, io.EOF
	}
}

func (*gcpVertexResumeFirstConnection) Close() error { return nil }

type gcpVertexResumeSecondConnection struct {
	state  int
	writes [][]byte
}

func (connection *gcpVertexResumeSecondConnection) Write(_ context.Context, messageType cloudWebSocketMessageType, data []byte) error {
	if messageType != cloudWebSocketMessageText {
		return fmt.Errorf("message type=%d", messageType)
	}
	switch connection.state {
	case 0:
		if !bytes.Contains(data, []byte(`"private-session-handle"`)) {
			return fmt.Errorf("resume handle missing")
		}
		connection.state = 1
	case 2:
		if !bytes.Contains(data, []byte(`"unacknowledged"`)) {
			return fmt.Errorf("unacknowledged message not replayed")
		}
		connection.state = 3
	default:
		return fmt.Errorf("write in state %d", connection.state)
	}
	connection.writes = append(connection.writes, append([]byte(nil), data...))
	return nil
}

func (connection *gcpVertexResumeSecondConnection) Read(context.Context) (cloudWebSocketMessageType, []byte, error) {
	switch connection.state {
	case 1:
		connection.state = 2
		return cloudWebSocketMessageText, []byte(`{"setupComplete":{}}`), nil
	case 3:
		connection.state = 4
		return cloudWebSocketMessageText, []byte(`{"serverContent":{"turnComplete":true}}`), nil
	default:
		return 0, nil, io.EOF
	}
}

func (*gcpVertexResumeSecondConnection) Close() error { return nil }

type gcpVertexResumeEOFConnection struct {
	gcpVertexResumeFirstConnection
}

func (connection *gcpVertexResumeEOFConnection) Read(context.Context) (cloudWebSocketMessageType, []byte, error) {
	switch connection.state {
	case 1:
		connection.state = 2
		return cloudWebSocketMessageText, []byte(`{"setupComplete":{}}`), nil
	case 4:
		connection.state = 5
		return cloudWebSocketMessageText, []byte(`{"sessionResumptionUpdate":{"newHandle":"private-session-handle","resumable":true,"lastConsumedClientMessageIndex":"1"}}`), nil
	case 5:
		return 0, nil, io.ErrUnexpectedEOF
	default:
		return 0, nil, io.EOF
	}
}

func FuzzGCPVertexLiveSessionResumptionUpdateNeverPanics(f *testing.F) {
	f.Add([]byte(`{"newHandle":"handle-1","resumable":true,"lastConsumedClientMessageIndex":"1"}`))
	f.Add([]byte(`{"resumable":false}`))
	f.Add([]byte(`{"newHandle":"secret","resumable":false,"lastConsumedClientMessageIndex":"99"}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		state, err := parseGCPVertexLiveSessionResumptionUpdate(json.RawMessage(raw), 0, 32)
		if err != nil {
			return
		}
		if state.acknowledged < 0 || state.acknowledged > 32 {
			t.Fatalf("acknowledged=%d", state.acknowledged)
		}
		if state.resumable != validGCPVertexLiveSessionHandle(state.handle) {
			t.Fatalf("resumable=%v handle=%q", state.resumable, state.handle)
		}
	})
}

func FuzzGCPVertexLiveToolResponseNeverPanics(f *testing.F) {
	f.Add([]byte(`{"functionCalls":[{"id":"call-1","name":"lookup","args":{"resourceId":9007199254740993}}]}`))
	f.Add([]byte(`{"functionCalls":[]}`))
	f.Add([]byte(`not-json`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		handler := &gcpVertexLiveToolHandlerState{response: json.RawMessage(`{"resourceId":9007199254740993}`), remaining: 1}
		response, calls, err := buildGCPVertexLiveToolResponse(json.RawMessage(raw), map[string]*gcpVertexLiveToolHandlerState{"lookup": handler}, map[string]struct{}{})
		if err != nil {
			return
		}
		if calls != 1 || handler.remaining != 0 || len(response) > maxRequestPayloadBytes || !json.Valid(response) || !bytes.Contains(response, []byte(`9007199254740993`)) {
			t.Fatalf("calls=%d remaining=%d response=%s", calls, handler.remaining, response)
		}
	})
}

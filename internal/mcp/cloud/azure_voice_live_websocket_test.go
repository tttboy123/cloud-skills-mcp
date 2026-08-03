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

func TestAzureVoiceLiveBoundarySeparatesModelReadsFromAgentMutations(t *testing.T) {
	directory := t.TempDir()
	model := func() Invocation {
		return Invocation{
			Provider: ProviderAzure, Mode: ModeRead, AuthScheme: "voice-live-ws", Service: "voice-live", Operation: "VoiceLiveResponse",
			Method: http.MethodGet, URL: "wss://demo.services.ai.azure.com/voice-live/realtime",
			Parameters:   map[string]any{"api-version": "2026-04-10", "model": "gpt-realtime"},
			Body:         []any{map[string]any{"type": "session.update", "session": map[string]any{"modalities": []any{"text", "audio"}}}, map[string]any{"type": "response.create"}},
			ResponseFile: filepath.Join(directory, "model.ndjson"), StreamIntervalMS: 10,
		}
	}
	base := model()
	if err := validateInvocation(base, []string{directory}); err != nil {
		t.Fatal(err)
	}
	if !classifyRead(ProviderAzure, base) {
		t.Fatal("Voice Live model response was not classified read-only")
	}
	agent := model()
	agent.Mode = ModeMutate
	agent.Operation = "VoiceLiveAgentSession"
	agent.Parameters = map[string]any{"api-version": "2026-04-10", "agent_id": "agent-123", "project_id": "project-456"}
	agent.ResponseFile = filepath.Join(directory, "agent.ndjson")
	if err := validateInvocation(agent, []string{directory}); err != nil {
		t.Fatal(err)
	}
	if classifyRead(ProviderAzure, agent) {
		t.Fatal("Voice Live Agent session bypassed the mutation gate")
	}

	for name, mutate := range map[string]func(*Invocation){
		"wrong host":          func(value *Invocation) { value.URL = "wss://example.com/voice-live/realtime" },
		"nested Foundry host": func(value *Invocation) { value.URL = "wss://nested.demo.services.ai.azure.com/voice-live/realtime" },
		"nested legacy host": func(value *Invocation) {
			value.URL = "wss://nested.demo.cognitiveservices.azure.com/voice-live/realtime"
		},
		"private-link Foundry": func(value *Invocation) { value.URL = "wss://privatelink.services.ai.azure.com/voice-live/realtime" },
		"private-link legacy": func(value *Invocation) {
			value.URL = "wss://privatelink.cognitiveservices.azure.com/voice-live/realtime"
		},
		"government Foundry":  func(value *Invocation) { value.URL = "wss://demo.services.ai.azure.us/voice-live/realtime" },
		"government legacy":   func(value *Invocation) { value.URL = "wss://demo.cognitiveservices.azure.us/voice-live/realtime" },
		"China legacy":        func(value *Invocation) { value.URL = "wss://demo.cognitiveservices.azure.cn/voice-live/realtime" },
		"wrong path":          func(value *Invocation) { value.URL = "wss://demo.services.ai.azure.com/openai/v1/realtime" },
		"inline query":        func(value *Invocation) { value.URL += "?api-version=caller" },
		"missing api version": func(value *Invocation) { delete(value.Parameters, "api-version") },
		"mixed target":        func(value *Invocation) { value.Parameters["agent_id"] = "agent" },
		"agent read mode": func(value *Invocation) {
			value.Operation = "VoiceLiveAgentSession"
			value.Parameters = map[string]any{"api-version": "2026-04-10", "agent_id": "agent", "project_id": "project"}
		},
		"response without create": func(value *Invocation) { value.Body = map[string]any{"type": "session.update"} },
		"credential event": func(value *Invocation) {
			value.Body = []any{map[string]any{"type": "session.update", "session": map[string]any{"credential": "forbidden"}}, map[string]any{"type": "response.create"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := model()
			candidate.Parameters = map[string]any{"api-version": "2026-04-10", "model": "gpt-realtime"}
			mutate(&candidate)
			if err := validateInvocation(candidate, []string{directory}); err == nil {
				t.Fatal("invalid Voice Live invocation accepted")
			}
		})
	}
}

func TestAzureVoiceLiveUsesEndpointSpecificEntraScopeAndFiniteEvents(t *testing.T) {
	for _, test := range []struct {
		name  string
		host  string
		scope string
	}{
		{name: "Foundry", host: "demo.services.ai.azure.com", scope: "https://ai.azure.com/.default"},
		{name: "legacy Speech", host: "demo.cognitiveservices.azure.com", scope: "https://cognitiveservices.azure.com/.default"},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			responseFile := filepath.Join(directory, "response.ndjson")
			connection := &fakeTencentWebSocketConnection{reads: [][]byte{
				[]byte(`{"type":"session.created","event_id":"evt-session"}`),
				[]byte(`{"type":"response.done","response":{"id":"resp-voice","status":"completed"}}`),
			}}
			tokens := &staticAzureTokenProvider{token: "private-entra-token"}
			adapter := NewAzureRESTAdapter(AzureRESTConfig{
				Tokens: tokens,
				WebSocketDial: func(_ context.Context, target string, headers http.Header) (cloudWebSocketConnection, error) {
					parsed, err := url.Parse(target)
					if err != nil || parsed.Query().Get("api-version") != "2026-04-10" || parsed.Query().Get("model") != "gpt-realtime" || strings.Contains(target, "private-entra-token") {
						return nil, fmt.Errorf("unexpected target %q", target)
					}
					if headers.Get("Authorization") != "Bearer private-entra-token" || len(headers) != 1 {
						return nil, fmt.Errorf("unexpected headers %#v", headers)
					}
					return connection, nil
				},
				StreamPause: func(context.Context, time.Duration) error { return nil },
			})
			result, err := adapter.Invoke(t.Context(), Invocation{
				Provider: ProviderAzure, Mode: ModeRead, AuthScheme: "voice-live-ws", Service: "voice-live", Operation: "VoiceLiveResponse",
				Method: http.MethodGet, URL: "wss://" + test.host + "/voice-live/realtime",
				Parameters: map[string]any{"api-version": "2026-04-10", "model": "gpt-realtime"},
				Body: []any{
					map[string]any{"type": "session.update", "session": map[string]any{"voice": map[string]any{"name": "en-US-AvaNeural", "type": "azure-standard"}}},
					map[string]any{"type": "response.create"},
				},
				ResponseFile: responseFile, MaxResponseFileBytes: 4096,
			})
			if err != nil {
				t.Fatal(err)
			}
			if tokens.calls != 1 || tokens.scope != test.scope || strings.Contains(string(result.Output), "private-entra-token") {
				t.Fatalf("calls=%d scope=%q result=%s", tokens.calls, tokens.scope, result.Output)
			}
			output, err := os.ReadFile(responseFile)
			if err != nil || !bytes.Contains(output, []byte(`"response.done"`)) {
				t.Fatalf("output=%s err=%v", output, err)
			}
		})
	}
}

func TestAzureVoiceLiveRejectsReturnedAvatarCredentialWithoutPublishing(t *testing.T) {
	directory := t.TempDir()
	responseFile := filepath.Join(directory, "response.ndjson")
	connection := &fakeTencentWebSocketConnection{reads: [][]byte{[]byte(`{"type":"session.updated","session":{"avatar":{"ice_servers":[{"urls":["turn:example"],"username":"u","credential":"must-stay-private"}]}}}`)}}
	adapter := NewAzureRESTAdapter(AzureRESTConfig{
		Tokens:        &staticAzureTokenProvider{token: "private-token"},
		WebSocketDial: func(context.Context, string, http.Header) (cloudWebSocketConnection, error) { return connection, nil },
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, Mode: ModeRead, AuthScheme: "voice-live-ws", Service: "voice-live", Operation: "VoiceLiveSession",
		Method: http.MethodGet, URL: "wss://demo.services.ai.azure.com/voice-live/realtime",
		Parameters:   map[string]any{"api-version": "2026-04-10", "model": "gpt-realtime"},
		Body:         map[string]any{"type": "session.update", "session": map[string]any{"modalities": []any{"audio"}}},
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err == nil || strings.Contains(err.Error(), "must-stay-private") {
		t.Fatalf("unsafe or missing credential rejection: %v", err)
	}
	if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
		t.Fatalf("credential-bearing response published: %v", statErr)
	}
}

func FuzzAzureVoiceLiveClientEventNeverPanics(f *testing.F) {
	f.Add([]byte(`{"type":"session.update","session":{"voice":{"name":"en-US-AvaNeural","type":"azure-standard"}}}`))
	f.Add([]byte(`{"type":"response.create"}`))
	f.Add([]byte(`{"type":"session.update","credential":"forbidden"}`))
	f.Add([]byte{0xff, 0x00, 0x7b})
	f.Fuzz(func(t *testing.T, data []byte) {
		events := &azureRealtimeEvents{}
		_ = events.add(data)
	})
}

func FuzzAzureVoiceLiveHostNeverEscapesPublicCloud(f *testing.F) {
	for _, seed := range []string{
		"demo.services.ai.azure.com",
		"demo.cognitiveservices.azure.com",
		"nested.demo.services.ai.azure.com",
		"demo.services.ai.azure.us",
		"demo.cognitiveservices.azure.cn",
		"privatelink.services.ai.azure.com",
		"privatelink.cognitiveservices.azure.com",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, host string) {
		normalized := strings.ToLower(host)
		if !azureVoiceLiveHost(normalized) {
			return
		}
		valid := false
		for _, suffix := range []string{".services.ai.azure.com", ".cognitiveservices.azure.com"} {
			resource := strings.TrimSuffix(normalized, suffix)
			if resource != normalized && resource != "privatelink" && endpointLabelPattern.MatchString(resource) {
				valid = true
			}
		}
		if !valid {
			t.Fatalf("accepted non-public or non-single-label Azure Voice Live host %q", normalized)
		}
	})
}

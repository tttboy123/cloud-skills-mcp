package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func azureOpenAIChatStreamInvocation(responseFile string) Invocation {
	return Invocation{
		Provider: ProviderAzure, Mode: ModeRead, AuthScheme: "openai-chat-stream",
		Service: "openai", Operation: "StreamChatCompletions", Method: http.MethodPost,
		URL:        "https://demo.openai.azure.com/openai/deployments/gpt-4o-deployment/chat/completions",
		APIVersion: "2024-06-01",
		Body: map[string]any{
			"model": "gpt-4o-deployment",
			"messages": []any{
				map[string]any{"role": "system", "content": "You are a helpful assistant."},
				map[string]any{"role": "user", "content": "Hello"},
			},
			"max_events": 4, "timeout_seconds": 30,
		},
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	}
}

func TestAzureOpenAIChatStreamBoundarySupportsEntraAuthenticatedGAStream(t *testing.T) {
	directory := t.TempDir()
	base := azureOpenAIChatStreamInvocation(filepath.Join(directory, "chunks.ndjson"))
	if !classifyRead(ProviderAzure, base) {
		t.Fatal("Azure OpenAI Chat Completions streaming must be read-only")
	}
	if err := validateInvocation(base, []string{directory}); err != nil {
		t.Fatalf("valid Azure OpenAI Chat Completions invocation rejected: %v", err)
	}
	preview := base
	preview.APIVersion = "2025-05-01-preview"
	preview.ResponseFile = filepath.Join(directory, "preview.ndjson")
	if err := validateInvocation(preview, []string{directory}); err != nil {
		t.Fatalf("valid Azure OpenAI preview api_version rejected: %v", err)
	}

	tooManyMessages := make([]any, azureOpenAIChatStreamMaxMessages+1)
	for index := range tooManyMessages {
		tooManyMessages[index] = map[string]any{"role": "user", "content": "x"}
	}
	oversized := strings.Repeat("a", azureOpenAIChatStreamMaxMessageBytes+1)

	for name, mutate := range map[string]func(*Invocation){
		"mutate tool":         func(value *Invocation) { value.Mode = ModeMutate },
		"wrong method":        func(value *Invocation) { value.Method = http.MethodGet },
		"wrong service":       func(value *Invocation) { value.Service = "foundry" },
		"wrong operation":     func(value *Invocation) { value.Operation = "CreateCompletion" },
		"missing api version": func(value *Invocation) { value.APIVersion = "" },
		"bad api version":     func(value *Invocation) { value.APIVersion = "2024/06/01" },
		"nested public host": func(value *Invocation) {
			value.URL = "https://nested.demo.openai.azure.com/openai/deployments/gpt-4o-deployment/chat/completions"
		},
		"private-link zone": func(value *Invocation) {
			value.URL = "https://privatelink.openai.azure.com/openai/deployments/gpt-4o-deployment/chat/completions"
		},
		"government host": func(value *Invocation) {
			value.URL = "https://demo.openai.azure.us/openai/deployments/gpt-4o-deployment/chat/completions"
		},
		"china host": func(value *Invocation) {
			value.URL = "https://demo.openai.azure.cn/openai/deployments/gpt-4o-deployment/chat/completions"
		},
		"lookalike host": func(value *Invocation) {
			value.URL = "https://demo.openai.azure.com.attacker.example/openai/deployments/gpt-4o-deployment/chat/completions"
		},
		"wrong path": func(value *Invocation) {
			value.URL = "https://demo.openai.azure.com/openai/deployments/gpt-4o-deployment/chat/completions/extra"
		},
		"openai path":       func(value *Invocation) { value.URL = "https://demo.openai.azure.com/openai/v1/chat/completions" },
		"URL query":         func(value *Invocation) { value.URL += "?stream=true" },
		"caller parameters": func(value *Invocation) { value.Parameters = map[string]any{"stream": true} },
		"caller headers":    func(value *Invocation) { value.Headers = map[string]string{"OpenAI-Beta": "assistants=v2"} },
		"missing body":      func(value *Invocation) { value.Body = nil },
		"body file":         func(value *Invocation) { value.BodyFile = "/tmp/input.ndjson" },
		"missing response":  func(value *Invocation) { value.ResponseFile = "" },
		"model mismatch": func(value *Invocation) {
			value.Body = map[string]any{"model": "other", "messages": []any{map[string]any{"role": "user", "content": "x"}}, "max_events": 1, "timeout_seconds": 5}
		},
		"unknown plan field": func(value *Invocation) {
			value.Body = map[string]any{"model": "gpt-4o-deployment", "messages": []any{map[string]any{"role": "user", "content": "x"}}, "stream": true, "max_events": 1, "timeout_seconds": 5}
		},
		"credential plan": func(value *Invocation) {
			value.Body = map[string]any{"model": "gpt-4o-deployment", "messages": []any{map[string]any{"role": "user", "content": "x"}}, "api_key": "secret", "max_events": 1, "timeout_seconds": 5}
		},
		"bad role": func(value *Invocation) {
			value.Body = map[string]any{"model": "gpt-4o-deployment", "messages": []any{map[string]any{"role": "tool", "content": "x"}}, "max_events": 1, "timeout_seconds": 5}
		},
		"empty content": func(value *Invocation) {
			value.Body = map[string]any{"model": "gpt-4o-deployment", "messages": []any{map[string]any{"role": "user", "content": ""}}, "max_events": 1, "timeout_seconds": 5}
		},
		"oversized content": func(value *Invocation) {
			value.Body = map[string]any{"model": "gpt-4o-deployment", "messages": []any{map[string]any{"role": "user", "content": oversized}}, "max_events": 1, "timeout_seconds": 5}
		},
		"non-text part": func(value *Invocation) {
			value.Body = map[string]any{"model": "gpt-4o-deployment", "messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "image_url", "url": "https://evil.example/x"}}}}, "max_events": 1, "timeout_seconds": 5}
		},
		"too many messages": func(value *Invocation) {
			value.Body = map[string]any{"model": "gpt-4o-deployment", "messages": tooManyMessages, "max_events": 1, "timeout_seconds": 5}
		},
		"zero max tokens": func(value *Invocation) {
			value.Body = map[string]any{"model": "gpt-4o-deployment", "messages": []any{map[string]any{"role": "user", "content": "x"}}, "max_tokens": 0, "max_events": 1, "timeout_seconds": 5}
		},
		"bad temperature": func(value *Invocation) {
			value.Body = map[string]any{"model": "gpt-4o-deployment", "messages": []any{map[string]any{"role": "user", "content": "x"}}, "temperature": 3.5, "max_events": 1, "timeout_seconds": 5}
		},
		"zero events": func(value *Invocation) {
			value.Body = map[string]any{"model": "gpt-4o-deployment", "messages": []any{map[string]any{"role": "user", "content": "x"}}, "max_events": 0, "timeout_seconds": 5}
		},
		"excessive timeout": func(value *Invocation) {
			value.Body = map[string]any{"model": "gpt-4o-deployment", "messages": []any{map[string]any{"role": "user", "content": "x"}}, "max_events": 1, "timeout_seconds": 301}
		},
		"stream pacing":  func(value *Invocation) { value.StreamIntervalMS = 10 },
		"cross-provider": func(value *Invocation) { value.Region = "eastus" },
		"unknown scheme": func(value *Invocation) { value.AuthScheme = "unknown" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := base
			candidate.Body = base.Body
			candidate.Parameters = nil
			candidate.Headers = nil
			mutate(&candidate)
			if err := validateInvocation(candidate, []string{directory}); err == nil {
				t.Fatalf("invalid Azure OpenAI Chat Completions invocation accepted: %#v", candidate)
			}
		})
	}
}

func TestAzureOpenAIChatStreamStreamsChunksWithInternalEntraToken(t *testing.T) {
	directory := t.TempDir()
	responseFile := filepath.Join(directory, "chunks.ndjson")
	sse := strings.Join([]string{
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1720000000,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1720000000,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1720000000,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	tokens := &staticAzureTokenProvider{token: "entra-private-token"}
	adapter := NewAzureRESTAdapter(AzureRESTConfig{
		Tokens: tokens,
		HTTP: doerFunc(func(request *http.Request) (*http.Response, error) {
			if request.Method != http.MethodPost || request.URL.Host != "demo.openai.azure.com" ||
				request.URL.Path != "/openai/deployments/gpt-4o-deployment/chat/completions" ||
				request.URL.Query().Get("api-version") != "2024-06-01" || len(request.URL.Query()) != 1 {
				return nil, fmt.Errorf("unexpected target %s %s", request.Method, request.URL)
			}
			if request.Header.Get("Authorization") != "Bearer entra-private-token" ||
				request.Header.Get("Accept") != "text/event-stream" ||
				request.Header.Get("Content-Type") != "application/json" || len(request.Header) != 3 {
				return nil, fmt.Errorf("unexpected headers %#v", request.Header)
			}
			body, err := io.ReadAll(request.Body)
			if err != nil {
				return nil, err
			}
			var decoded map[string]any
			if err := json.Unmarshal(body, &decoded); err != nil {
				return nil, err
			}
			if decoded["stream"] != true || decoded["model"] != "gpt-4o-deployment" ||
				len(decoded) != 3 || strings.Contains(string(body), "entra-private-token") {
				return nil, fmt.Errorf("unexpected request body %s", body)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"text/event-stream; charset=utf-8"},
					"Request-Id":   []string{"azure-chat-request"},
				},
				Body: io.NopCloser(strings.NewReader(sse)),
			}, nil
		}),
	})
	result, err := adapter.Invoke(t.Context(), azureOpenAIChatStreamInvocation(responseFile))
	if err != nil {
		t.Fatal(err)
	}
	if tokens.calls != 1 || tokens.scope != "https://cognitiveservices.azure.com/.default" {
		t.Fatalf("token calls=%d scope=%q", tokens.calls, tokens.scope)
	}
	if result.RequestID != "azure-chat-request" {
		t.Fatalf("request id=%q", result.RequestID)
	}
	written, err := os.ReadFile(responseFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(written), []byte{'\n'})
	if len(lines) != 3 || !bytes.Contains(lines[0], []byte(`"content":"Hello"`)) ||
		!bytes.Contains(lines[1], []byte(`"content":" world"`)) || !bytes.Contains(lines[2], []byte(`"finish_reason":"stop"`)) {
		t.Fatalf("output=%s", written)
	}
	if strings.Contains(string(result.Output), "entra-private-token") || !bytes.Contains(result.Output, []byte(`"response_file"`)) {
		t.Fatalf("result=%s", result.Output)
	}
}

func TestAzureOpenAIChatStreamStopsAtCallerEventLimitBeforeSentinel(t *testing.T) {
	directory := t.TempDir()
	responseFile := filepath.Join(directory, "chunks.ndjson")
	sse := strings.Join([]string{
		`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"one"},"finish_reason":null}]}`,
		"",
		`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"two"},"finish_reason":null}]}`,
		"",
		`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"must-not-be-read"},"finish_reason":null}]}`,
		"",
	}, "\n")
	invocation := azureOpenAIChatStreamInvocation(responseFile)
	invocation.Body = map[string]any{
		"model": "gpt-4o-deployment", "messages": []any{map[string]any{"role": "user", "content": "x"}},
		"max_events": 2, "timeout_seconds": 30,
	}
	adapter := NewAzureRESTAdapter(AzureRESTConfig{
		Tokens: &staticAzureTokenProvider{token: "token"},
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(sse)),
			}, nil
		}),
	})
	if _, err := adapter.Invoke(t.Context(), invocation); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(responseFile)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(written, []byte("must-not-be-read")) || bytes.Count(written, []byte("\n")) != 2 {
		t.Fatalf("output=%s", written)
	}
}

func TestAzureOpenAIChatStreamFailureNeverPublishesOutput(t *testing.T) {
	for name, sse := range map[string]string{
		"unknown event": strings.Join([]string{
			`data: {"id":"c1","object":"chat.completion","created":1,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"x"}}]}`,
			"", "data: [DONE]", "",
		}, "\n"),
		"credential chunk": strings.Join([]string{
			`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"x","api_key":"leaked"}}]}`,
			"", "data: [DONE]", "",
		}, "\n"),
		"missing done": strings.Join([]string{
			`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"x"}}]}`,
			"",
		}, "\n"),
		"malformed chunk": strings.Join([]string{
			`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"x"},"finish_reason":42}]}`,
			"", "data: [DONE]", "",
		}, "\n"),
	} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			responseFile := filepath.Join(directory, "chunks.ndjson")
			adapter := NewAzureRESTAdapter(AzureRESTConfig{
				Tokens: &staticAzureTokenProvider{token: "token"},
				HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
						Body:       io.NopCloser(strings.NewReader(sse)),
					}, nil
				}),
			})
			if _, err := adapter.Invoke(t.Context(), azureOpenAIChatStreamInvocation(responseFile)); err == nil {
				t.Fatal("invalid stream accepted")
			}
			if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
				t.Fatalf("failed stream published output: %v", statErr)
			}
		})
	}
}

func TestAzureOpenAIChatStreamRejectsInvalidTransportAndEmptyStream(t *testing.T) {
	for name, mutate := range map[string]func(*http.Response){
		"wrong content type": func(response *http.Response) {
			response.Header.Set("Content-Type", "application/json")
		},
		"error status": func(response *http.Response) {
			response.StatusCode = http.StatusTooManyRequests
			response.Body = io.NopCloser(strings.NewReader(`{"error":{"message":"rate limited","token":"super-secret-token-value"}}`))
		},
		"empty stream": func(response *http.Response) {
			response.Body = io.NopCloser(strings.NewReader(""))
		},
	} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			responseFile := filepath.Join(directory, "chunks.ndjson")
			adapter := NewAzureRESTAdapter(AzureRESTConfig{
				Tokens: &staticAzureTokenProvider{token: "entra-token"},
				HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
					response := &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
						Body:       io.NopCloser(strings.NewReader(`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"x"}}]}` + "\n\n")),
					}
					mutate(response)
					return response, nil
				}),
			})
			_, err := adapter.Invoke(t.Context(), azureOpenAIChatStreamInvocation(responseFile))
			if err == nil {
				t.Fatal("invalid transport accepted")
			}
			if name == "error status" && strings.Contains(err.Error(), "super-secret-token-value") {
				t.Fatalf("provider error leaked secret material: %v", err)
			}
			if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
				t.Fatalf("failed transport published output: %v", statErr)
			}
		})
	}
}

func TestAzureOpenAIChatStreamTimeoutWithPartialOutputPublishes(t *testing.T) {
	directory := t.TempDir()
	responseFile := filepath.Join(directory, "chunks.ndjson")
	reader, writer := io.Pipe()
	go func() {
		_, _ = io.WriteString(writer, `data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"one"},"finish_reason":null}]}`+"\n\n")
		time.Sleep(2 * time.Second)
		_ = writer.Close()
	}()
	adapter := NewAzureRESTAdapter(AzureRESTConfig{
		Tokens: &staticAzureTokenProvider{token: "token"},
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       reader,
			}, nil
		}),
	})
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()
	if _, err := adapter.Invoke(ctx, azureOpenAIChatStreamInvocation(responseFile)); err != nil {
		t.Fatalf("partial timeout stream failed: %v", err)
	}
	written, err := os.ReadFile(responseFile)
	if err != nil || !bytes.Contains(written, []byte(`"content":"one"`)) {
		t.Fatalf("output=%s err=%v", written, err)
	}
}

func FuzzAzureOpenAIChatStreamHostNeverEscapesPublicCloud(f *testing.F) {
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
		target := (&url.URL{Scheme: "https", Host: host, Path: "/openai/deployments/deployment/chat/completions"}).String()
		invocation := Invocation{
			Provider: ProviderAzure, Mode: ModeRead, AuthScheme: authSchemeAzureOpenAIChatStream,
			Service: "openai", Operation: "StreamChatCompletions", Method: http.MethodPost,
			URL: target, APIVersion: "2024-06-01",
			Body: map[string]any{
				"model": "deployment", "messages": []any{map[string]any{"role": "user", "content": "x"}},
				"max_events": 1, "timeout_seconds": 5,
			},
			ResponseFile: "/approved/chunks.ndjson",
		}
		if validateAzureOpenAIChatStreamInvocation(invocation) != nil {
			return
		}
		parsed, err := url.Parse(target)
		if err != nil {
			t.Fatal(err)
		}
		normalized := strings.ToLower(parsed.Hostname())
		resource := strings.TrimSuffix(normalized, ".openai.azure.com")
		if resource == normalized || resource == "privatelink" || !endpointLabelPattern.MatchString(resource) {
			t.Fatalf("accepted non-public or non-single-label Azure OpenAI Chat host %q", normalized)
		}
	})
}

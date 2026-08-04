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

func azureOpenAIResponsesInvocation(responseFile string) Invocation {
	return Invocation{
		Provider: ProviderAzure, Mode: ModeRead, AuthScheme: authSchemeAzureOpenAIResponsesStream,
		Service: "openai", Operation: "StreamResponses", Method: http.MethodPost,
		URL: "https://demo.openai.azure.com/openai/v1/responses",
		Body: map[string]any{
			"model":      "gpt-5-deployment",
			"input":      "Summarize cloud skills in one sentence.",
			"max_events": 6, "timeout_seconds": 30,
		},
		ResponseFile: responseFile, MaxResponseFileBytes: 8192,
	}
}

func TestAzureOpenAIResponsesBoundarySupportsEntraAuthenticatedGAStream(t *testing.T) {
	directory := t.TempDir()
	base := azureOpenAIResponsesInvocation(filepath.Join(directory, "events.ndjson"))
	if !classifyRead(ProviderAzure, base) {
		t.Fatal("Azure OpenAI Responses streaming must be read-only")
	}
	for _, apiVersion := range []string{"", "v1", "preview", "2025-06-01", "2026-06-05-preview"} {
		candidate := base
		candidate.APIVersion = apiVersion
		candidate.ResponseFile = filepath.Join(directory, "events-"+strings.ReplaceAll(apiVersion, "/", "-")+".ndjson")
		if err := validateInvocation(candidate, []string{directory}); err != nil {
			t.Fatalf("valid Azure OpenAI Responses api_version %q rejected: %v", apiVersion, err)
		}
	}
	arrayInput := base
	arrayInput.ResponseFile = filepath.Join(directory, "array.ndjson")
	arrayInput.Body = map[string]any{
		"model": "gpt-5-deployment",
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": []any{
				map[string]any{"type": "input_text", "text": "Explain."},
			}},
			map[string]any{"type": "message", "role": "assistant", "content": "OK."},
		},
		"max_output_tokens": 128, "temperature": 0.5,
		"max_events": 2, "timeout_seconds": 10,
	}
	if err := validateInvocation(arrayInput, []string{directory}); err != nil {
		t.Fatalf("valid Azure OpenAI Responses array input rejected: %v", err)
	}

	oversized := strings.Repeat("a", azureOpenAIResponsesStreamMaxMessageBytes+1)
	tooManyItems := make([]any, azureOpenAIResponsesStreamMaxInputItems+1)
	for index := range tooManyItems {
		tooManyItems[index] = map[string]any{"type": "message", "role": "user", "content": "x"}
	}
	for name, mutate := range map[string]func(*Invocation){
		"mutate tool":        func(value *Invocation) { value.Mode = ModeMutate },
		"wrong method":       func(value *Invocation) { value.Method = http.MethodGet },
		"wrong service":      func(value *Invocation) { value.Service = "foundry" },
		"wrong operation":    func(value *Invocation) { value.Operation = "CreateResponse" },
		"bad api version":    func(value *Invocation) { value.APIVersion = "2024/06/01" },
		"api version v2":     func(value *Invocation) { value.APIVersion = "v2" },
		"nested public host": func(value *Invocation) { value.URL = "https://nested.demo.openai.azure.com/openai/v1/responses" },
		"private-link zone":  func(value *Invocation) { value.URL = "https://privatelink.openai.azure.com/openai/v1/responses" },
		"government host":    func(value *Invocation) { value.URL = "https://demo.openai.azure.us/openai/v1/responses" },
		"china host":         func(value *Invocation) { value.URL = "https://demo.openai.azure.cn/openai/v1/responses" },
		"lookalike host": func(value *Invocation) {
			value.URL = "https://demo.openai.azure.com.attacker.example/openai/v1/responses"
		},
		"deployment path": func(value *Invocation) {
			value.URL = "https://demo.openai.azure.com/openai/deployments/gpt-5-deployment/responses"
		},
		"wrong path": func(value *Invocation) { value.URL = "https://demo.openai.azure.com/openai/v1/responses/" },
		"input_items path": func(value *Invocation) {
			value.URL = "https://demo.openai.azure.com/openai/v1/responses/resp_1/input_items"
		},
		"URL query":         func(value *Invocation) { value.URL += "?stream=true" },
		"caller parameters": func(value *Invocation) { value.Parameters = map[string]any{"stream": true} },
		"caller headers":    func(value *Invocation) { value.Headers = map[string]string{"api-key": "secret"} },
		"missing body":      func(value *Invocation) { value.Body = nil },
		"body file":         func(value *Invocation) { value.BodyFile = "/tmp/input.ndjson" },
		"missing response":  func(value *Invocation) { value.ResponseFile = "" },
		"empty input": func(value *Invocation) {
			value.Body = map[string]any{"model": "gpt-5-deployment", "input": "", "max_events": 1, "timeout_seconds": 5}
		},
		"oversized input": func(value *Invocation) {
			value.Body = map[string]any{"model": "gpt-5-deployment", "input": oversized, "max_events": 1, "timeout_seconds": 5}
		},
		"bad input type": func(value *Invocation) {
			value.Body = map[string]any{"model": "gpt-5-deployment", "input": 42, "max_events": 1, "timeout_seconds": 5}
		},
		"unknown plan field": func(value *Invocation) {
			value.Body = map[string]any{"model": "gpt-5-deployment", "input": "x", "instructions": "be brief", "max_events": 1, "timeout_seconds": 5}
		},
		"caller stream": func(value *Invocation) {
			value.Body = map[string]any{"model": "gpt-5-deployment", "input": "x", "stream": true, "max_events": 1, "timeout_seconds": 5}
		},
		"caller store": func(value *Invocation) {
			value.Body = map[string]any{"model": "gpt-5-deployment", "input": "x", "store": true, "max_events": 1, "timeout_seconds": 5}
		},
		"credential plan": func(value *Invocation) {
			value.Body = map[string]any{"model": "gpt-5-deployment", "input": "x", "api_key": "secret", "max_events": 1, "timeout_seconds": 5}
		},
		"bad item type": func(value *Invocation) {
			value.Body = map[string]any{"model": "gpt-5-deployment", "input": []any{map[string]any{"type": "function_call_output", "output": "x"}}, "max_events": 1, "timeout_seconds": 5}
		},
		"bad item role": func(value *Invocation) {
			value.Body = map[string]any{"model": "gpt-5-deployment", "input": []any{map[string]any{"type": "message", "role": "tool", "content": "x"}}, "max_events": 1, "timeout_seconds": 5}
		},
		"non-text part": func(value *Invocation) {
			value.Body = map[string]any{"model": "gpt-5-deployment", "input": []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_image", "image_url": "https://evil.example/x"}}}}, "max_events": 1, "timeout_seconds": 5}
		},
		"too many items": func(value *Invocation) {
			value.Body = map[string]any{"model": "gpt-5-deployment", "input": tooManyItems, "max_events": 1, "timeout_seconds": 5}
		},
		"zero max tokens": func(value *Invocation) {
			value.Body = map[string]any{"model": "gpt-5-deployment", "input": "x", "max_output_tokens": 0, "max_events": 1, "timeout_seconds": 5}
		},
		"bad temperature": func(value *Invocation) {
			value.Body = map[string]any{"model": "gpt-5-deployment", "input": "x", "temperature": 3.5, "max_events": 1, "timeout_seconds": 5}
		},
		"zero events": func(value *Invocation) {
			value.Body = map[string]any{"model": "gpt-5-deployment", "input": "x", "max_events": 0, "timeout_seconds": 5}
		},
		"excessive timeout": func(value *Invocation) {
			value.Body = map[string]any{"model": "gpt-5-deployment", "input": "x", "max_events": 1, "timeout_seconds": 301}
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
				t.Fatalf("invalid Azure OpenAI Responses invocation accepted: %#v", candidate)
			}
		})
	}
}

func TestAzureOpenAIResponsesStreamsEventsWithInternalEntraToken(t *testing.T) {
	directory := t.TempDir()
	responseFile := filepath.Join(directory, "events.ndjson")
	sse := strings.Join([]string{
		`data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_1","object":"response","model":"gpt-5","status":"in_progress"}}`,
		"",
		`data: {"type":"response.output_text.delta","sequence_number":1,"output_index":0,"content_index":0,"item_id":"msg_1","delta":"Hello","logprobs":[]}`,
		"",
		`data: {"type":"response.output_text.delta","sequence_number":2,"output_index":0,"content_index":0,"item_id":"msg_1","delta":" world","logprobs":[]}`,
		"",
		`data: {"type":"response.completed","sequence_number":3,"response":{"id":"resp_1","object":"response","model":"gpt-5","status":"completed"}}`,
		"",
	}, "\n")
	tokens := &staticAzureTokenProvider{token: "entra-private-token"}
	adapter := NewAzureRESTAdapter(AzureRESTConfig{
		Tokens: tokens,
		HTTP: doerFunc(func(request *http.Request) (*http.Response, error) {
			if request.Method != http.MethodPost || request.URL.Host != "demo.openai.azure.com" ||
				request.URL.Path != "/openai/v1/responses" || request.URL.RawQuery != "" {
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
			if decoded["stream"] != true || decoded["store"] != false ||
				decoded["model"] != "gpt-5-deployment" || decoded["input"] != "Summarize cloud skills in one sentence." ||
				len(decoded) != 4 || strings.Contains(string(body), "entra-private-token") {
				return nil, fmt.Errorf("unexpected request body %s", body)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"text/event-stream; charset=utf-8"},
					"Request-Id":   []string{"azure-responses-request"},
				},
				Body: io.NopCloser(strings.NewReader(sse)),
			}, nil
		}),
	})
	result, err := adapter.Invoke(t.Context(), azureOpenAIResponsesInvocation(responseFile))
	if err != nil {
		t.Fatal(err)
	}
	if tokens.calls != 1 || tokens.scope != azureOpenAIResponsesStreamScope {
		t.Fatalf("token calls=%d scope=%q", tokens.calls, tokens.scope)
	}
	if result.RequestID != "azure-responses-request" {
		t.Fatalf("request id=%q", result.RequestID)
	}
	written, err := os.ReadFile(responseFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(written), []byte{'\n'})
	if len(lines) != 4 || !bytes.Contains(lines[1], []byte(`"delta":"Hello"`)) ||
		!bytes.Contains(lines[2], []byte(`"delta":" world"`)) || !bytes.Contains(lines[3], []byte(`"type":"response.completed"`)) {
		t.Fatalf("output=%s", written)
	}
	if strings.Contains(string(result.Output), "entra-private-token") || !bytes.Contains(result.Output, []byte(`"response_file"`)) {
		t.Fatalf("result=%s", result.Output)
	}
}

func TestAzureOpenAIResponsesAPIVersionQueryAndArrayInput(t *testing.T) {
	directory := t.TempDir()
	responseFile := filepath.Join(directory, "events.ndjson")
	invocation := azureOpenAIResponsesInvocation(responseFile)
	invocation.APIVersion = "v1"
	invocation.Body = map[string]any{
		"model": "gpt-5-deployment",
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": "Hello"},
		},
		"max_output_tokens": 64,
		"max_events":        1, "timeout_seconds": 10,
	}
	adapter := NewAzureRESTAdapter(AzureRESTConfig{
		Tokens: &staticAzureTokenProvider{token: "token"},
		HTTP: doerFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Query().Get("api-version") != "v1" || len(request.URL.Query()) != 1 {
				return nil, fmt.Errorf("unexpected api-version query %q", request.URL.RawQuery)
			}
			body, err := io.ReadAll(request.Body)
			if err != nil {
				return nil, err
			}
			var decoded map[string]any
			if err := json.Unmarshal(body, &decoded); err != nil {
				return nil, err
			}
			if decoded["max_output_tokens"] != float64(64) || decoded["stream"] != true || decoded["store"] != false {
				return nil, fmt.Errorf("unexpected body %s", body)
			}
			events := []string{
				`{"type":"response.output_text.delta","sequence_number":0,"output_index":0,"content_index":0,"item_id":"msg_1","delta":"ok"}`,
				`{"type":"response.completed","sequence_number":1,"response":{"id":"resp_1","object":"response","status":"completed"}}`,
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader("data: " + strings.Join(events, "\n\ndata: ") + "\n\n")),
			}, nil
		}),
	})
	if _, err := adapter.Invoke(t.Context(), invocation); err != nil {
		t.Fatal(err)
	}
}

func TestAzureOpenAIResponsesStopsAtCallerEventLimitBeforeTerminal(t *testing.T) {
	directory := t.TempDir()
	responseFile := filepath.Join(directory, "events.ndjson")
	sse := strings.Join([]string{
		`data: {"type":"response.output_text.delta","sequence_number":0,"output_index":0,"content_index":0,"item_id":"msg_1","delta":"one"}`,
		"",
		`data: {"type":"response.output_text.delta","sequence_number":1,"output_index":0,"content_index":0,"item_id":"msg_1","delta":"two"}`,
		"",
		`data: {"type":"response.output_text.delta","sequence_number":2,"output_index":0,"content_index":0,"item_id":"msg_1","delta":"must-not-be-read"}`,
		"",
	}, "\n")
	invocation := azureOpenAIResponsesInvocation(responseFile)
	invocation.Body = map[string]any{"model": "gpt-5-deployment", "input": "x", "max_events": 2, "timeout_seconds": 30}
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

func TestAzureOpenAIResponsesFailureNeverPublishesOutput(t *testing.T) {
	validDelta := `{"type":"response.output_text.delta","sequence_number":0,"output_index":0,"content_index":0,"item_id":"msg_1","delta":"x"}`
	for name, sse := range map[string]string{
		"unknown event type": strings.Join([]string{
			`data: {"type":"response.surprise","sequence_number":0}`,
			"",
		}, "\n"),
		"credential event": strings.Join([]string{
			`data: {"type":"response.created","sequence_number":0,"api_key":"leaked","response":{"id":"resp_1","object":"response"}}`,
			"",
		}, "\n"),
		"missing sequence": strings.Join([]string{
			`data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"msg_1","delta":"x"}`,
			"",
		}, "\n"),
		"negative sequence": strings.Join([]string{
			`data: {"type":"response.output_text.delta","sequence_number":-1,"output_index":0,"content_index":0,"item_id":"msg_1","delta":"x"}`,
			"",
		}, "\n"),
		"terminal without response": strings.Join([]string{
			`data: {"type":"response.completed","sequence_number":1,"response":{"object":"response"}}`,
			"",
		}, "\n"),
		"terminal wrong object": strings.Join([]string{
			`data: {"type":"response.incomplete","sequence_number":1,"response":{"id":"resp_1","object":"chat.completion"}}`,
			"",
		}, "\n"),
		"missing terminal": strings.Join([]string{
			"data: " + validDelta,
			"",
		}, "\n"),
		"malformed delta": strings.Join([]string{
			`data: {"type":"response.output_text.delta","sequence_number":0,"output_index":0,"content_index":0,"item_id":"msg_1","delta":42}`,
			"",
		}, "\n"),
		"oversized delta": strings.Join([]string{
			`data: {"type":"response.output_text.delta","sequence_number":0,"output_index":0,"content_index":0,"item_id":"msg_1","delta":"` + strings.Repeat("a", azureOpenAIResponsesStreamMaxMessageBytes+1) + `"}`,
			"",
		}, "\n"),
		"invalid SSE field": strings.Join([]string{
			"event: response.output_text.delta",
			"",
		}, "\n"),
	} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			responseFile := filepath.Join(directory, "events.ndjson")
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
			if _, err := adapter.Invoke(t.Context(), azureOpenAIResponsesInvocation(responseFile)); err == nil {
				t.Fatal("invalid stream accepted")
			}
			if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
				t.Fatalf("failed stream published output: %v", statErr)
			}
		})
	}
}

func TestAzureOpenAIResponsesErrorEventFailsClosed(t *testing.T) {
	for name, sse := range map[string]string{
		"nested azure error": strings.Join([]string{
			`data: {"type":"error","error":{"type":"too_many_requests","code":"no_capacity","message":"The system is currently experiencing high demand.","param":null}}`,
			"",
		}, "\n"),
		"sdk flat error": strings.Join([]string{
			`data: {"type":"error","sequence_number":5,"code":"no_capacity","message":"capacity exhausted","param":null}`,
			"",
		}, "\n"),
		"response.failed": strings.Join([]string{
			`data: {"type":"response.failed","sequence_number":2,"response":{"id":"resp_1","object":"response","status":"failed"}}`,
			"",
		}, "\n"),
	} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			responseFile := filepath.Join(directory, "events.ndjson")
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
			if _, err := adapter.Invoke(t.Context(), azureOpenAIResponsesInvocation(responseFile)); err == nil {
				t.Fatal("error-terminal stream accepted as success")
			}
			if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
				t.Fatalf("error-terminal stream published output: %v", statErr)
			}
		})
	}
}

func TestAzureOpenAIResponsesRejectsInvalidTransportAndEmptyStream(t *testing.T) {
	terminal := `data: {"type":"response.completed","sequence_number":0,"response":{"id":"resp_1","object":"response","status":"completed"}}` + "\n\n"
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
			responseFile := filepath.Join(directory, "events.ndjson")
			adapter := NewAzureRESTAdapter(AzureRESTConfig{
				Tokens: &staticAzureTokenProvider{token: "entra-token"},
				HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
					response := &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
						Body:       io.NopCloser(strings.NewReader(terminal)),
					}
					mutate(response)
					return response, nil
				}),
			})
			_, err := adapter.Invoke(t.Context(), azureOpenAIResponsesInvocation(responseFile))
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

func TestAzureOpenAIResponsesTimeoutWithPartialOutputPublishes(t *testing.T) {
	directory := t.TempDir()
	responseFile := filepath.Join(directory, "events.ndjson")
	reader, writer := io.Pipe()
	go func() {
		_, _ = io.WriteString(writer, `data: {"type":"response.output_text.delta","sequence_number":0,"output_index":0,"content_index":0,"item_id":"msg_1","delta":"one"}`+"\n\n")
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
	if _, err := adapter.Invoke(ctx, azureOpenAIResponsesInvocation(responseFile)); err != nil {
		t.Fatalf("partial timeout stream failed: %v", err)
	}
	written, err := os.ReadFile(responseFile)
	if err != nil || !bytes.Contains(written, []byte(`"delta":"one"`)) {
		t.Fatalf("output=%s err=%v", written, err)
	}
}

func FuzzAzureOpenAIResponsesHostNeverEscapesPublicCloud(f *testing.F) {
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
		target := (&url.URL{Scheme: "https", Host: host, Path: "/openai/v1/responses"}).String()
		invocation := Invocation{
			Provider: ProviderAzure, Mode: ModeRead, AuthScheme: authSchemeAzureOpenAIResponsesStream,
			Service: "openai", Operation: "StreamResponses", Method: http.MethodPost,
			URL: target,
			Body: map[string]any{
				"model": "deployment", "input": "x",
				"max_events": 1, "timeout_seconds": 5,
			},
			ResponseFile: "/approved/events.ndjson",
		}
		if validateAzureOpenAIResponsesStreamInvocation(invocation) != nil {
			return
		}
		parsed, err := url.Parse(target)
		if err != nil {
			t.Fatal(err)
		}
		normalized := strings.ToLower(parsed.Hostname())
		resource := strings.TrimSuffix(normalized, ".openai.azure.com")
		if resource == normalized || resource == "privatelink" || !endpointLabelPattern.MatchString(resource) {
			t.Fatalf("accepted non-public or non-single-label Azure OpenAI Responses host %q", normalized)
		}
	})
}

package cloud

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func firebaseSSEInvocation(responseFile string) Invocation {
	return Invocation{
		Provider: ProviderGCP, Mode: ModeRead, AuthScheme: "firebase-sse",
		Service: "firebase-database", Operation: "Listen", Method: http.MethodGet,
		URL:          "https://demo-project.europe-west1.firebasedatabase.app/messages.json",
		Parameters:   map[string]any{"orderBy": `"createdAt"`, "limitToLast": 10},
		Body:         map[string]any{"max_events": 2, "timeout_seconds": 30},
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	}
}

func TestFirebaseSSEInvocationBoundary(t *testing.T) {
	base := firebaseSSEInvocation(filepath.Join(t.TempDir(), "events.ndjson"))
	if !classifyRead(ProviderGCP, base) {
		t.Fatal("Firebase SSE Listen must be read-only")
	}
	if err := validateInvocation(base, []string{filepath.Dir(base.ResponseFile)}); err != nil {
		t.Fatalf("valid Firebase SSE invocation rejected: %v", err)
	}

	tests := map[string]func(*Invocation){
		"mutate tool":     func(value *Invocation) { value.Mode = ModeMutate },
		"wrong method":    func(value *Invocation) { value.Method = http.MethodPost },
		"wrong service":   func(value *Invocation) { value.Service = "firestore" },
		"wrong operation": func(value *Invocation) { value.Operation = "Get" },
		"untrusted host": func(value *Invocation) {
			value.URL = "https://demo-project.firebaseio.com.attacker.example/messages.json"
		},
		"unsupported region": func(value *Invocation) {
			value.URL = "https://demo-project.us-west1.firebasedatabase.app/messages.json"
		},
		"missing json suffix":   func(value *Invocation) { value.URL = "https://demo-project.firebaseio.com/messages" },
		"reserved settings":     func(value *Invocation) { value.URL = "https://demo-project.firebaseio.com/.settings/rules.json" },
		"inline query":          func(value *Invocation) { value.URL += "?orderBy=%22name%22" },
		"credential parameter":  func(value *Invocation) { value.Parameters = map[string]any{"auth": "secret"} },
		"unsupported parameter": func(value *Invocation) { value.Parameters = map[string]any{"callback": "steal"} },
		"unquoted orderBy":      func(value *Invocation) { value.Parameters = map[string]any{"orderBy": "createdAt"} },
		"caller header":         func(value *Invocation) { value.Headers = map[string]string{"Accept": "text/event-stream"} },
		"missing plan":          func(value *Invocation) { value.Body = nil },
		"credential plan": func(value *Invocation) {
			value.Body = map[string]any{"max_events": 1, "timeout_seconds": 5, "access_token": "secret"}
		},
		"unknown plan field": func(value *Invocation) {
			value.Body = map[string]any{"max_events": 1, "timeout_seconds": 5, "retry": true}
		},
		"zero events":            func(value *Invocation) { value.Body = map[string]any{"max_events": 0, "timeout_seconds": 5} },
		"excessive timeout":      func(value *Invocation) { value.Body = map[string]any{"max_events": 1, "timeout_seconds": 301} },
		"missing response file":  func(value *Invocation) { value.ResponseFile = "" },
		"body file":              func(value *Invocation) { value.BodyFile = "/tmp/input" },
		"stream transport knobs": func(value *Invocation) { value.StreamIntervalMS = 10 },
		"cross provider control": func(value *Invocation) { value.Region = "europe-west1" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			value := base
			value.Parameters = make(map[string]any, len(base.Parameters))
			for key, item := range base.Parameters {
				value.Parameters[key] = item
			}
			mutate(&value)
			if err := validateFirebaseSSEInvocation(value); err == nil {
				t.Fatalf("invalid invocation accepted: %#v", value)
			}
		})
	}
}

func TestFirebaseSSERejectsInvalidTargetBeforeLoadingADC(t *testing.T) {
	tokens := &staticTokenProvider{token: "must-not-be-used"}
	adapter := NewGCPRESTAdapter(GCPRESTConfig{
		FirebaseTokens: tokens,
		FirebaseHTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("HTTP transport must not run for an invalid Firebase endpoint")
			return nil, nil
		}),
	})
	invocation := firebaseSSEInvocation(filepath.Join(t.TempDir(), "events.ndjson"))
	invocation.URL = "https://demo-project.firebaseio.com.attacker.example/messages.json"
	if _, err := adapter.Invoke(t.Context(), invocation); err == nil {
		t.Fatal("invalid endpoint accepted")
	}
	if tokens.calls != 0 {
		t.Fatalf("ADC loaded %d times", tokens.calls)
	}
}

func TestFirebaseSSEStreamsCanonicalEventsWithScopedADC(t *testing.T) {
	directory := t.TempDir()
	responseFile := filepath.Join(directory, "events.ndjson")
	tokens := &staticTokenProvider{token: "firebase-private-token"}
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "Bearer firebase-private-token" || request.Header.Get("Accept") != "text/event-stream" {
			t.Fatalf("headers=%v", request.Header)
		}
		if request.URL.Query().Get("orderBy") != `"createdAt"` || request.URL.Query().Get("limitToLast") != "10" {
			t.Fatalf("query=%s", request.URL.RawQuery)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream; charset=utf-8"}, "X-Firebase-Request-Id": []string{"firebase-request"}},
			Body: io.NopCloser(strings.NewReader(
				"event: put\n" +
					"data: {\"path\":\"/\",\"data\":{\"message\":\"hello\"}}\n\n" +
					": heartbeat\n" +
					"event: keep-alive\ndata: null\n\n" +
					"event: patch\n" +
					"data: {\"path\":\"/a\",\"data\":{\"count\":2}}\n\n")),
		}, nil
	})
	adapter := NewGCPRESTAdapter(GCPRESTConfig{FirebaseTokens: tokens, FirebaseHTTP: doer})
	result, err := adapter.Invoke(t.Context(), firebaseSSEInvocation(responseFile))
	if err != nil {
		t.Fatal(err)
	}
	if tokens.calls != 1 || result.RequestID != "firebase-request" {
		t.Fatalf("token calls=%d request_id=%q", tokens.calls, result.RequestID)
	}
	if strings.Contains(string(result.Output), tokens.token) {
		t.Fatal("token exposed in MCP output")
	}
	data, err := os.ReadFile(responseFile)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"data\":{\"message\":\"hello\"},\"event\":\"put\",\"path\":\"/\"}\n" +
		"{\"data\":{\"count\":2},\"event\":\"patch\",\"path\":\"/a\"}\n"
	if string(data) != want {
		t.Fatalf("events=%s", data)
	}
	info, err := os.Stat(responseFile)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("info=%v err=%v", info, err)
	}
}

func TestFirebaseSSECanIncludeKeepAliveWithoutMisclassifyingBusinessData(t *testing.T) {
	responseFile := filepath.Join(t.TempDir(), "events.ndjson")
	stream := "event: put\ndata: {\"path\":\"/\",\"data\":{\"event\":\"keep-alive\"}}\n\n" +
		"event: keep-alive\ndata: null\n\n"
	adapter := NewGCPRESTAdapter(GCPRESTConfig{
		FirebaseTokens: &staticTokenProvider{token: "private"},
		FirebaseHTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream))}, nil
		}),
	})
	invocation := firebaseSSEInvocation(responseFile)
	invocation.Parameters = nil
	invocation.Body = map[string]any{"max_events": 2, "timeout_seconds": 30, "include_keep_alive": true}
	if _, err := adapter.Invoke(t.Context(), invocation); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(responseFile)
	if err != nil || !strings.Contains(string(data), `"data":{"event":"keep-alive"}`) || !strings.Contains(string(data), `{"data":null,"event":"keep-alive"}`) {
		t.Fatalf("data=%s err=%v", data, err)
	}
}

func TestFirebaseSSEFiniteContextPublishesObservedEvents(t *testing.T) {
	responseFile := filepath.Join(t.TempDir(), "events.ndjson")
	adapter := NewGCPRESTAdapter(GCPRESTConfig{
		FirebaseTokens: &staticTokenProvider{token: "private"},
		FirebaseHTTP: doerFunc(func(request *http.Request) (*http.Response, error) {
			reader, writer := io.Pipe()
			go func() {
				_, _ = writer.Write([]byte("event: put\ndata: {\"path\":\"/\",\"data\":1}\n\n"))
				<-request.Context().Done()
				_ = writer.CloseWithError(request.Context().Err())
			}()
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: reader}, nil
		}),
	})
	invocation := firebaseSSEInvocation(responseFile)
	invocation.Parameters = nil
	invocation.Body = map[string]any{"max_events": 2, "timeout_seconds": 1}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	if _, err := adapter.Invoke(ctx, invocation); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(responseFile); err != nil || string(data) != "{\"data\":1,\"event\":\"put\",\"path\":\"/\"}\n" {
		t.Fatalf("data=%s err=%v", data, err)
	}
}

func TestFirebaseSSEFollowsOnlyBoundedOfficialRedirect(t *testing.T) {
	responseFile := filepath.Join(t.TempDir(), "events.ndjson")
	tokens := &staticTokenProvider{token: "private-token"}
	calls := 0
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Header.Get("Authorization") != "Bearer private-token" {
			t.Fatal("redirect request omitted internal authorization")
		}
		if calls == 1 {
			return &http.Response{
				StatusCode: http.StatusTemporaryRedirect,
				Header:     http.Header{"Location": []string{"https://s-usc1c-nss-123.firebaseio.com/messages.json?limitToLast=10&orderBy=%22createdAt%22"}},
				Body:       io.NopCloser(strings.NewReader("redirect")),
			}, nil
		}
		if request.URL.Hostname() != "s-usc1c-nss-123.firebaseio.com" {
			t.Fatalf("redirect host=%q", request.URL.Hostname())
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("event: put\ndata: {\"path\":\"/\",\"data\":null}\n\n"))}, nil
	})
	adapter := NewGCPRESTAdapter(GCPRESTConfig{FirebaseTokens: tokens, FirebaseHTTP: doer})
	invocation := firebaseSSEInvocation(responseFile)
	invocation.URL = "https://demo-project.firebaseio.com/messages.json"
	invocation.Body = map[string]any{"max_events": 1, "timeout_seconds": 30}
	if _, err := adapter.Invoke(t.Context(), invocation); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || tokens.calls != 1 {
		t.Fatalf("http calls=%d token calls=%d", calls, tokens.calls)
	}
}

func TestFirebaseSSERejectsUnsafeRedirectBeforeCredentialReuse(t *testing.T) {
	for name, location := range map[string]string{
		"foreign host":     "https://attacker.example/messages.json?limitToLast=10&orderBy=%22createdAt%22",
		"changed path":     "https://s-usc1c-nss-123.firebaseio.com/other.json?limitToLast=10&orderBy=%22createdAt%22",
		"credential query": "https://s-usc1c-nss-123.firebaseio.com/messages.json?access_token=secret",
	} {
		t.Run(name, func(t *testing.T) {
			responseFile := filepath.Join(t.TempDir(), "events.ndjson")
			doer := doerFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusTemporaryRedirect, Header: http.Header{"Location": []string{location}}, Body: io.NopCloser(strings.NewReader("redirect"))}, nil
			})
			adapter := NewGCPRESTAdapter(GCPRESTConfig{FirebaseTokens: &staticTokenProvider{token: "private"}, FirebaseHTTP: doer})
			invocation := firebaseSSEInvocation(responseFile)
			invocation.URL = "https://demo-project.firebaseio.com/messages.json"
			if _, err := adapter.Invoke(t.Context(), invocation); err == nil {
				t.Fatal("unsafe redirect accepted")
			}
			if _, err := os.Stat(responseFile); !os.IsNotExist(err) {
				t.Fatalf("response_file published after redirect failure: %v", err)
			}
		})
	}
}

func TestFirebaseSSETerminalAndCredentialEventsAbortAtomicOutput(t *testing.T) {
	tests := map[string]string{
		"cancel":           "event: put\ndata: {\"path\":\"/\",\"data\":1}\n\nevent: cancel\ndata: null\n\n",
		"auth revoked":     "event: auth_revoked\ndata: \"expired private detail\"\n\n",
		"credential data":  "event: put\ndata: {\"path\":\"/\",\"data\":{\"access_token\":\"provider-secret\"}}\n\n",
		"plain token data": "event: put\ndata: {\"path\":\"/\",\"data\":{\"token\":\"provider-secret\"}}\n\n",
		"credential path":  "event: put\ndata: {\"path\":\"/password\",\"data\":\"provider-secret\"}\n\n",
		"unknown event":    "event: message\ndata: {\"path\":\"/\",\"data\":1}\n\n",
	}
	for name, stream := range tests {
		t.Run(name, func(t *testing.T) {
			responseFile := filepath.Join(t.TempDir(), "events.ndjson")
			doer := doerFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream))}, nil
			})
			adapter := NewGCPRESTAdapter(GCPRESTConfig{FirebaseTokens: &staticTokenProvider{token: "private"}, FirebaseHTTP: doer})
			_, err := adapter.Invoke(t.Context(), firebaseSSEInvocation(responseFile))
			if err == nil {
				t.Fatal("unsafe stream accepted")
			}
			if strings.Contains(err.Error(), "expired private detail") || strings.Contains(err.Error(), "provider-secret") {
				t.Fatalf("provider secret leaked in error: %v", err)
			}
			if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
				t.Fatalf("response_file published on error: %v", statErr)
			}
		})
	}
}

func TestFirebaseSSERejectsInvalidResponseEnvelope(t *testing.T) {
	tests := map[string]*http.Response{
		"non-200":            {StatusCode: http.StatusUnauthorized, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("private provider body"))},
		"wrong content type": {StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"secret":"value"}`))},
		"missing body":       {StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}},
	}
	for name, response := range tests {
		t.Run(name, func(t *testing.T) {
			responseFile := filepath.Join(t.TempDir(), "events.ndjson")
			adapter := NewGCPRESTAdapter(GCPRESTConfig{
				FirebaseTokens: &staticTokenProvider{token: "private"},
				FirebaseHTTP:   doerFunc(func(*http.Request) (*http.Response, error) { return response, nil }),
			})
			_, err := adapter.Invoke(t.Context(), firebaseSSEInvocation(responseFile))
			if err == nil || strings.Contains(err.Error(), "private provider body") || strings.Contains(err.Error(), `"secret"`) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestFirebaseADCUsesOfficialScopes(t *testing.T) {
	var scopes []string
	provider := &gcpADCTokenProvider{}
	provider.scopes = []string{gcpFirebaseDatabaseScope, gcpUserInfoEmailScope}
	provider.detectScopes = func(_ context.Context, values []string) (googleAuthTokenSource, error) {
		scopes = append([]string(nil), values...)
		return &fakeGoogleAuthSource{token: "firebase-token"}, nil
	}
	if token, err := provider.Token(t.Context()); err != nil || token != "firebase-token" {
		t.Fatalf("token=%q err=%v", token, err)
	}
	if strings.Join(scopes, " ") != gcpFirebaseDatabaseScope+" "+gcpUserInfoEmailScope {
		t.Fatalf("scopes=%v", scopes)
	}
}

func TestFirebaseDefaultAdapterConfiguresDedicatedScopedADC(t *testing.T) {
	adapter := NewGCPRESTAdapter(GCPRESTConfig{})
	provider, ok := adapter.config.FirebaseTokens.(*gcpADCTokenProvider)
	if !ok || strings.Join(provider.scopes, " ") != gcpFirebaseDatabaseScope+" "+gcpUserInfoEmailScope {
		t.Fatalf("firebase token provider=%#v", adapter.config.FirebaseTokens)
	}
}

func TestFirebaseDiscoveryReturnsOfficialReferencesWithoutHTTPOrADC(t *testing.T) {
	tokens := &staticTokenProvider{token: "must-not-be-used"}
	adapter := NewGCPRESTAdapter(GCPRESTConfig{
		Tokens: tokens,
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("Firebase documentation discovery must remain local")
			return nil, nil
		}),
	})
	output, err := adapter.Discover(t.Context(), DiscoveryRequest{Provider: ProviderGCP, Service: "firebase-database"})
	if err != nil || !strings.Contains(string(output), "firebase.google.com/docs/database/rest/auth") || tokens.calls != 0 {
		t.Fatalf("output=%s token_calls=%d err=%v", output, tokens.calls, err)
	}
}

func FuzzFirebaseSSEEventNeverPanics(f *testing.F) {
	f.Add("put", `{"path":"/","data":{"message":"hello"}}`)
	f.Add("patch", `{"path":"/a","data":{"count":2}}`)
	f.Add("auth_revoked", `"expired"`)
	f.Fuzz(func(t *testing.T, name, data string) {
		if len(name) > 128 || len(data) > gcpFirebaseMaxEventBytes {
			t.Skip()
		}
		_, _, _ = canonicalFirebaseSSEEvent(firebaseSSEEvent{
			name: name, seenEvent: true, dataLines: [][]byte{[]byte(data)},
		})
	})
}

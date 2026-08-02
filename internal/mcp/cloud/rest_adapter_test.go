package cloud

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestAzureRESTAdapterUsesAzRestWithoutExposingCredentials(t *testing.T) {
	runner := &fakeProcessRunner{stdout: []byte(`{"value":[]}`)}
	runner.check = func(call processCall) error {
		for _, argument := range call.args {
			if strings.HasPrefix(argument, "@") {
				data, err := io.ReadAll(mustOpen(t, strings.TrimPrefix(argument, "@")))
				if err != nil {
					return err
				}
				if string(data) != `{"name":"demo"}` {
					t.Fatalf("body=%s", data)
				}
			}
		}
		return nil
	}
	adapter := NewAzureRESTAdapter(AzureRESTConfig{Binary: "az-test", Runner: runner, TempDir: t.TempDir()})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, Method: "PUT",
		URL:          "https://management.azure.com/subscriptions/sub/resourceGroups/rg?api-version=2021-04-01",
		Subscription: "sub", Headers: map[string]string{"If-Match": "etag"}, Body: map[string]any{"name": "demo"},
	})
	if err != nil || string(result.Output) != `{"value":[]}` {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	want := []string{
		"rest", "--method", "put", "--url", "https://management.azure.com/subscriptions/sub/resourceGroups/rg?api-version=2021-04-01",
		"--subscription", "sub", "--headers", "If-Match=etag", "--body", "@", "--output", "json",
	}
	if len(runner.calls) != 1 {
		t.Fatalf("calls=%d", len(runner.calls))
	}
	got := append([]string(nil), runner.calls[0].args...)
	for index, argument := range got {
		if strings.HasPrefix(argument, "@") {
			got[index] = "@"
		}
		if strings.Contains(strings.ToLower(argument), "client-secret") {
			t.Fatal("credential leaked into argv")
		}
	}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args:\nwant %#v\n got %#v", want, got)
	}
}

func mustOpen(t *testing.T, path string) io.ReadCloser {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

type staticTokenProvider struct {
	token string
	err   error
	calls int
}

func (provider *staticTokenProvider) Token(context.Context) (string, error) {
	provider.calls++
	return provider.token, provider.err
}

type doerFunc func(*http.Request) (*http.Response, error)

func (function doerFunc) Do(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestGCPRESTAdapterUsesTokenInternallyAndReturnsOnlyProviderResponse(t *testing.T) {
	tokenProvider := &staticTokenProvider{token: "private-access-token"}
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		if got := request.Header.Get("Authorization"); got != "Bearer private-access-token" {
			t.Fatalf("authorization=%q", got)
		}
		if got := request.Header.Get("X-Goog-User-Project"); got != "billing-project" {
			t.Fatalf("quota project=%q", got)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != `{"name":"demo"}` {
			t.Fatalf("body=%s", body)
		}
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"X-Request-Id": []string{"gcp-request"}},
			Body:       io.NopCloser(strings.NewReader(`{"operation":"done"}`)),
		}, nil
	})
	adapter := NewGCPRESTAdapter(GCPRESTConfig{Tokens: tokenProvider, HTTP: doer})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderGCP, Method: "POST",
		URL:     "https://compute.googleapis.com/compute/v1/projects/p/zones/z/instances",
		Project: "billing-project", Body: map[string]any{"name": "demo"},
	})
	if err != nil || string(result.Output) != `{"operation":"done"}` || result.RequestID != "gcp-request" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if strings.Contains(string(result.Output), tokenProvider.token) || tokenProvider.calls != 1 {
		t.Fatalf("token exposed or not used exactly once: %#v", result)
	}
}

func TestGCPDiscoveryUsesPublicDiscoveryServiceWithoutCredential(t *testing.T) {
	tokenProvider := &staticTokenProvider{token: "must-not-be-used"}
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://www.googleapis.com/discovery/v1/apis/compute/v1/rest" {
			t.Fatalf("discovery URL=%s", request.URL)
		}
		if request.Header.Get("Authorization") != "" {
			t.Fatal("discovery sent credentials")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"name":"compute"}`))}, nil
	})
	adapter := NewGCPRESTAdapter(GCPRESTConfig{Tokens: tokenProvider, HTTP: doer})
	output, err := adapter.Discover(t.Context(), DiscoveryRequest{Provider: ProviderGCP, Service: "compute", Operation: "v1"})
	if err != nil || string(output) != `{"name":"compute"}` || tokenProvider.calls != 0 {
		t.Fatalf("output=%s token_calls=%d err=%v", output, tokenProvider.calls, err)
	}
}

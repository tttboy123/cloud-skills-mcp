package cloud

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

type staticAzureTokenProvider struct {
	token string
	err   error
	scope string
	calls int
}

func (provider *staticAzureTokenProvider) Token(_ context.Context, scope string) (string, error) {
	provider.calls++
	provider.scope = scope
	return provider.token, provider.err
}

func TestAzureRESTAdapterUsesDefaultCredentialTokenWithoutCLIExposure(t *testing.T) {
	tokens := &staticAzureTokenProvider{token: "azure-private-token"}
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "Bearer azure-private-token" {
			t.Fatalf("authorization=%q", request.Header.Get("Authorization"))
		}
		if request.URL.Host != "management.azure.com" {
			t.Fatalf("host=%s", request.URL.Host)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil || string(body) != `{"name":"demo"}` {
			t.Fatalf("body=%s err=%v", body, err)
		}
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"X-Ms-Request-Id": []string{"azure-request"}},
			Body:       io.NopCloser(strings.NewReader(`{"status":"ok"}`)),
		}, nil
	})
	adapter := NewAzureRESTAdapter(AzureRESTConfig{Tokens: tokens, HTTP: doer})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, Method: "PUT",
		URL:  "https://management.azure.com/subscriptions/sub/resourceGroups/rg?api-version=2021-04-01",
		Body: map[string]any{"name": "demo"},
	})
	if err != nil || string(result.Output) != `{"status":"ok"}` || result.RequestID != "azure-request" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if tokens.calls != 1 || tokens.scope != "https://management.azure.com/.default" {
		t.Fatalf("token calls=%d scope=%q", tokens.calls, tokens.scope)
	}
}

func TestAzureNonCLICredentialSourceDetection(t *testing.T) {
	servicePrincipal := []string{"AZURE_TENANT_ID=tenant", "AZURE_CLIENT_ID=client", "AZURE_CLIENT_SECRET=secret"}
	if !hasAzureServicePrincipalEnvironment(servicePrincipal) || hasAzureWorkloadIdentityEnvironment(servicePrincipal) {
		t.Fatalf("service principal environment not classified correctly")
	}
	workload := []string{"AZURE_TENANT_ID=tenant", "AZURE_CLIENT_ID=client", "AZURE_FEDERATED_TOKEN_FILE=/token"}
	if !hasAzureWorkloadIdentityEnvironment(workload) || hasAzureServicePrincipalEnvironment(workload) {
		t.Fatalf("workload identity environment not classified correctly")
	}
}

func TestAzureRESTAdapterMapsOfficialDataPlaneScopes(t *testing.T) {
	tests := map[string]string{
		"https://graph.microsoft.com/v1.0/users":                                    "https://graph.microsoft.com/.default",
		"https://account.blob.core.windows.net/container":                           "https://storage.azure.com/.default",
		"https://vault.vault.azure.net/secrets/name?api-version=7.4":                "https://vault.azure.net/.default",
		"https://server.database.windows.net/management/health":                     "https://database.windows.net/.default",
		"https://namespace.servicebus.windows.net/queue/messages/head":              "https://servicebus.azure.net/.default",
		"https://monitor.azure.com/subscriptions/sub/providers/metrics":             "https://monitor.azure.com/.default",
		"https://store.azconfig.io/kv?api-version=2026-04-01":                       "https://appconfig.azure.com/.default",
		"https://service.search.windows.net/indexes?api-version=2025-09-01":         "https://search.azure.com/.default",
		"https://workspace.azuredatabricks.net/api/2.0/clusters/list":               "2ff814a6-3304-4ab8-85cb-cd0e6f879c1d/.default",
		"https://management.chinacloudapi.cn/subscriptions?api-version=2020-01-01":  "https://management.chinacloudapi.cn/.default",
		"https://management.usgovcloudapi.net/subscriptions?api-version=2020-01-01": "https://management.usgovcloudapi.net/.default",
	}
	for rawURL, want := range tests {
		got, err := azureScopeForURL(rawURL)
		if err != nil || got != want {
			t.Errorf("url=%s scope=%q want=%q err=%v", rawURL, got, want, err)
		}
	}
}

func TestAzureRESTAdapterPassesExplicitAudienceOnlyAsInternalAuthConfiguration(t *testing.T) {
	tokens := &staticAzureTokenProvider{token: "azure-private-token"}
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"value":[]}`))}, nil
	})
	adapter := NewAzureRESTAdapter(AzureRESTConfig{Tokens: tokens, HTTP: doer})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure,
		Method:   "GET",
		URL:      "https://workspace.azuredatabricks.net/api/2.0/clusters/list",
		Audience: "2ff814a6-3304-4ab8-85cb-cd0e6f879c1d",
	})
	if err != nil {
		t.Fatal(err)
	}
	if tokens.scope != "2ff814a6-3304-4ab8-85cb-cd0e6f879c1d/.default" {
		t.Fatalf("scope=%q", tokens.scope)
	}
}

func TestAzureRESTAdapterUsesOperatorApprovedExactEndpointWithInternalToken(t *testing.T) {
	tokens := &staticAzureTokenProvider{token: "azure-private-token"}
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "new-api.example.microsoft" || request.Header.Get("Authorization") != "Bearer azure-private-token" {
			t.Fatalf("request=%s authorization=%q", request.URL, request.Header.Get("Authorization"))
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
	})
	adapter := NewAzureRESTAdapter(AzureRESTConfig{Tokens: tokens, HTTP: doer, AllowedHosts: []string{"new-api.example.microsoft"}})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, Method: "GET", URL: "https://new-api.example.microsoft/v1/resources", Audience: "https://management.azure.com",
	})
	if err != nil || string(result.Output) != `{"ok":true}` || tokens.scope != "https://management.azure.com/.default" {
		t.Fatalf("result=%#v scope=%q err=%v", result, tokens.scope, err)
	}
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
		if got := request.URL.Query().Get("pageToken"); got != "next" {
			t.Fatalf("query pageToken=%q", got)
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
		Project: "billing-project", Parameters: map[string]any{"pageToken": "next"}, Body: map[string]any{"name": "demo"},
	})
	if err != nil || string(result.Output) != `{"operation":"done"}` || result.RequestID != "gcp-request" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if strings.Contains(string(result.Output), tokenProvider.token) || tokenProvider.calls != 1 {
		t.Fatalf("token exposed or not used exactly once: %#v", result)
	}
}

func TestGCPRESTAdapterRejectsUntrustedEndpointBeforeLoadingToken(t *testing.T) {
	tokenProvider := &staticTokenProvider{token: "must-not-leak"}
	adapter := NewGCPRESTAdapter(GCPRESTConfig{
		Tokens: tokenProvider,
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("HTTP transport must not run for an untrusted endpoint")
			return nil, nil
		}),
	})
	_, err := adapter.Invoke(t.Context(), Invocation{Provider: ProviderGCP, Method: "GET", URL: "https://attacker.example/collect"})
	if err == nil || !strings.Contains(err.Error(), "endpoint allowlist") {
		t.Fatalf("error=%v", err)
	}
	if tokenProvider.calls != 0 {
		t.Fatalf("token provider called %d times", tokenProvider.calls)
	}
}

func TestGCPRESTAdapterRejectsInvalidQueryBeforeLoadingToken(t *testing.T) {
	tokenProvider := &staticTokenProvider{token: "must-not-be-used"}
	adapter := NewGCPRESTAdapter(GCPRESTConfig{Tokens: tokenProvider})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderGCP, Method: "GET", URL: "https://compute.googleapis.com/compute/v1/projects",
		Parameters: map[string]any{"filter": map[string]any{"nested": true}},
	})
	if err == nil || !strings.Contains(err.Error(), "query parameter") || tokenProvider.calls != 0 {
		t.Fatalf("error=%v token_calls=%d", err, tokenProvider.calls)
	}
}

func TestGCPRESTAdapterStreamsGuardedBodyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(path, []byte("binary-payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	tokens := &staticTokenProvider{token: "token"}
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil || string(body) != "binary-payload" || request.ContentLength != int64(len(body)) {
			t.Fatalf("body=%q length=%d err=%v", body, request.ContentLength, err)
		}
		if got := request.Header.Get("Content-Type"); got != "application/octet-stream" {
			t.Fatalf("content type=%q", got)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
	})
	adapter := NewGCPRESTAdapter(GCPRESTConfig{Tokens: tokens, HTTP: doer})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderGCP, Method: "POST", URL: "https://storage.googleapis.com/upload/storage/v1/b/b/o", BodyFile: path,
	})
	if err != nil || string(result.Output) != `{"ok":true}` {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestPrepareRESTBodyRechecksFileSizeAtOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversized.bin")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxRequestFileBytes + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	_, _, cleanup, err := prepareRESTBody(Invocation{BodyFile: path})
	cleanup()
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized body_file error=%v", err)
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

package cloud

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGCPArtifactRegistryUsesADCBearerForOCIRegistry(t *testing.T) {
	tokens := &staticTokenProvider{token: "private-google-access-token"}
	requests := 0
	adapter := NewGCPRESTAdapter(GCPRESTConfig{Tokens: tokens, HTTP: doerFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.Method != http.MethodGet || request.URL.String() != "https://us-west1-docker.pkg.dev/v2/my-project/my-repo/team/app/tags/list?n=20" {
			t.Fatalf("request=%s %s", request.Method, request.URL)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer private-google-access-token" {
			t.Fatalf("Authorization=%q", got)
		}
		if got := request.Header.Get("X-Goog-User-Project"); got != "billing-project" {
			t.Fatalf("X-Goog-User-Project=%q", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"X-Request-Id": {"artifact-request-1"}},
			Body:       io.NopCloser(strings.NewReader(`{"name":"my-project/my-repo/team/app","tags":["latest"]}`)),
		}, nil
	})})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderGCP, Mode: ModeRead, AuthScheme: authSchemeGCPArtifactRegistry,
		Service: "artifact-registry", Operation: "ListTags", Method: http.MethodGet,
		URL:     "https://us-west1-docker.pkg.dev/v2/my-project/my-repo/team/app/tags/list",
		Project: "billing-project", Parameters: map[string]any{"n": 20},
	})
	if err != nil {
		t.Fatalf("invoke Artifact Registry: %v", err)
	}
	if string(result.Output) != `{"name":"my-project/my-repo/team/app","tags":["latest"]}` || result.RequestID != "artifact-request-1" || requests != 1 || tokens.calls != 1 {
		t.Fatalf("result=%#v requests=%d token_calls=%d", result, requests, tokens.calls)
	}
	if strings.Contains(string(result.Output), tokens.token) {
		t.Fatal("ADC access token leaked into output")
	}
}

func TestGCPArtifactRegistrySupportsCurrentOfficialRegistryHosts(t *testing.T) {
	for _, test := range []struct {
		name   string
		method string
		rawURL string
	}{
		{name: "regional pkg dev", method: http.MethodGet, rawURL: "https://us-west1-docker.pkg.dev/v2/my-project/my-repo/app/tags/list"},
		{name: "multi region pkg dev", method: http.MethodHead, rawURL: "https://us-docker.pkg.dev/v2/my-project/my-repo/app/manifests/latest"},
		{name: "gcr", method: http.MethodGet, rawURL: "https://gcr.io/v2/my-project/app/manifests/latest"},
		{name: "us gcr", method: http.MethodHead, rawURL: "https://us.gcr.io/v2/my-project/app/blobs/sha256:abc"},
		{name: "eu gcr", method: http.MethodGet, rawURL: "https://eu.gcr.io/v2/my-project/app/referrers/sha256:abc"},
		{name: "asia gcr", method: http.MethodGet, rawURL: "https://asia.gcr.io/v2/my-project/app/tags/list"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := Invocation{Provider: ProviderGCP, Mode: ModeRead, AuthScheme: authSchemeGCPArtifactRegistry, Service: "artifact-registry", Operation: "RegistryRead", Method: test.method, URL: test.rawURL}
			if err := validateInvocation(request, nil); err != nil {
				t.Fatalf("official endpoint rejected: %v", err)
			}
		})
	}
}

func TestGCPArtifactRegistryRejectsUnsafeTargetBeforeADC(t *testing.T) {
	for _, test := range []struct {
		name   string
		method string
		rawURL string
	}{
		{name: "lookalike pkg dev", method: http.MethodGet, rawURL: "https://us-docker.pkg.dev.attacker.example/v2/project/repo/app/tags/list"},
		{name: "generic pkg dev", method: http.MethodGet, rawURL: "https://docker.pkg.dev/v2/project/repo/app/tags/list"},
		{name: "wrong package format", method: http.MethodGet, rawURL: "https://us-maven.pkg.dev/v2/project/repo/app/tags/list"},
		{name: "nested pkg host", method: http.MethodGet, rawURL: "https://nested.us-docker.pkg.dev/v2/project/repo/app/tags/list"},
		{name: "unofficial gcr region", method: http.MethodGet, rawURL: "https://west.gcr.io/v2/project/app/tags/list"},
		{name: "gcr lookalike", method: http.MethodGet, rawURL: "https://gcr.io.attacker.example/v2/project/app/tags/list"},
		{name: "token endpoint", method: http.MethodGet, rawURL: "https://gcr.io/v2/token"},
		{name: "catalog", method: http.MethodGet, rawURL: "https://us-docker.pkg.dev/v2/_catalog"},
		{name: "missing pkg repository", method: http.MethodGet, rawURL: "https://us-docker.pkg.dev/v2/project/app/tags/list"},
		{name: "missing gcr image", method: http.MethodGet, rawURL: "https://gcr.io/v2/project/tags/list"},
		{name: "escaped repository", method: http.MethodGet, rawURL: "https://gcr.io/v2/project%2Fapp/tags/list"},
		{name: "custom port", method: http.MethodGet, rawURL: "https://gcr.io:8443/v2/project/app/tags/list"},
		{name: "wrong tags method", method: http.MethodDelete, rawURL: "https://gcr.io/v2/project/app/tags/list"},
		{name: "chunked upload", method: http.MethodPatch, rawURL: "https://us-docker.pkg.dev/v2/project/repo/app/blobs/uploads/upload-id"},
	} {
		t.Run(test.name, func(t *testing.T) {
			tokens := &staticTokenProvider{token: "must-not-resolve"}
			requests := 0
			adapter := NewGCPRESTAdapter(GCPRESTConfig{Tokens: tokens, HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
				requests++
				return nil, context.Canceled
			})})
			invocation := Invocation{Provider: ProviderGCP, Mode: ModeMutate, AuthScheme: authSchemeGCPArtifactRegistry, Service: "artifact-registry", Operation: "RegistryOperation", Method: test.method, URL: test.rawURL}
			err := validateInvocation(invocation, nil)
			if err == nil {
				_, err = adapter.Invoke(t.Context(), invocation)
			}
			if err == nil {
				t.Fatal("unsafe Artifact Registry invocation accepted")
			}
			if tokens.calls != 0 || requests != 0 {
				t.Fatalf("unsafe target reached ADC/network: token_calls=%d requests=%d", tokens.calls, requests)
			}
		})
	}
}

func TestGCPArtifactRegistryAllowsOnlyMonolithicBlobUpload(t *testing.T) {
	payload := filepath.Join(t.TempDir(), "blob.bin")
	if err := os.WriteFile(payload, []byte("blob"), 0o600); err != nil {
		t.Fatal(err)
	}
	valid := Invocation{
		Provider: ProviderGCP, Mode: ModeMutate, AuthScheme: authSchemeGCPArtifactRegistry,
		Service: "artifact-registry", Operation: "UploadBlob", Method: http.MethodPost,
		URL:        "https://us-docker.pkg.dev/v2/project/repository/app/blobs/uploads/",
		Parameters: map[string]any{"digest": "sha256:abc"}, BodyFile: payload,
	}
	if err := validateInvocation(valid, []string{filepath.Dir(payload)}); err != nil {
		t.Fatalf("monolithic upload rejected: %v", err)
	}
	for _, mutate := range []func(*Invocation){
		func(request *Invocation) { request.Parameters = nil },
		func(request *Invocation) { request.BodyFile = "" },
		func(request *Invocation) { request.Method = http.MethodPatch; request.URL += "upload-id" },
	} {
		request := valid
		mutate(&request)
		if err := validateInvocation(request, []string{filepath.Dir(payload)}); err == nil {
			t.Fatalf("unsupported upload accepted: %#v", request)
		}
	}
}

func TestGCPArtifactRegistryCompletesPOSTThenPUTMonolithicUploadInternally(t *testing.T) {
	payload := filepath.Join(t.TempDir(), "blob.bin")
	if err := os.WriteFile(payload, []byte("blob-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	requests := 0
	adapter := NewGCPRESTAdapter(GCPRESTConfig{
		Tokens: &staticTokenProvider{token: "private-token"},
		HTTP: doerFunc(func(request *http.Request) (*http.Response, error) {
			requests++
			data, err := io.ReadAll(request.Body)
			closeErr := request.Body.Close()
			if err != nil || string(data) != "blob-data" || request.Header.Get("Authorization") != "Bearer private-token" {
				t.Fatalf("request=%d body=%q Authorization=%q err=%v close_err=%v", requests, data, request.Header.Get("Authorization"), err, closeErr)
			}
			switch requests {
			case 1:
				if request.Method != http.MethodPost || request.URL.Query().Get("digest") != "sha256:abc" {
					t.Fatalf("initial request=%s %s", request.Method, request.URL)
				}
				return &http.Response{StatusCode: http.StatusAccepted, Header: http.Header{
					"Location": {"/v2/project/repository/app/blobs/uploads/internal-id?_state=internal-capability"},
				}, Body: io.NopCloser(strings.NewReader("internal response"))}, nil
			case 2:
				if request.Method != http.MethodPut || request.URL.Host != "us-docker.pkg.dev" || request.URL.Path != "/v2/project/repository/app/blobs/uploads/internal-id" || request.URL.Query().Get("digest") != "sha256:abc" || request.URL.Query().Get("_state") != "internal-capability" {
					t.Fatalf("completion request=%s %s", request.Method, request.URL)
				}
				return &http.Response{StatusCode: http.StatusCreated, Header: http.Header{"X-Goog-Request-Id": {"upload-request"}}, Body: io.NopCloser(strings.NewReader(`{"uploaded":true}`))}, nil
			default:
				t.Fatalf("unexpected request %d", requests)
				return nil, nil
			}
		}),
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderGCP, Mode: ModeMutate, AuthScheme: authSchemeGCPArtifactRegistry,
		Service: "artifact-registry", Operation: "UploadBlob", Method: http.MethodPost,
		URL:        "https://us-docker.pkg.dev/v2/project/repository/app/blobs/uploads/",
		Parameters: map[string]any{"digest": "sha256:abc"}, BodyFile: payload,
	})
	if err != nil || requests != 2 || string(result.Output) != `{"uploaded":true}` || result.RequestID != "upload-request" {
		t.Fatalf("result=%#v requests=%d err=%v", result, requests, err)
	}
	if strings.Contains(string(result.Output), "internal-capability") || strings.Contains(string(result.Output), "private-token") {
		t.Fatal("internal upload capability leaked")
	}
}

func TestGCPArtifactRegistryRejectsUnsafeInternalUploadLocation(t *testing.T) {
	payload := filepath.Join(t.TempDir(), "blob.bin")
	if err := os.WriteFile(payload, []byte("blob-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	requests := 0
	adapter := NewGCPRESTAdapter(GCPRESTConfig{
		Tokens: &staticTokenProvider{token: "private-token"},
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return &http.Response{StatusCode: http.StatusAccepted, Header: http.Header{
				"Location": {"https://attacker.example/upload?access_token=internal-capability"},
			}, Body: io.NopCloser(strings.NewReader("internal response"))}, nil
		}),
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderGCP, Mode: ModeMutate, AuthScheme: authSchemeGCPArtifactRegistry,
		Service: "artifact-registry", Operation: "UploadBlob", Method: http.MethodPost,
		URL:        "https://us-docker.pkg.dev/v2/project/repository/app/blobs/uploads/",
		Parameters: map[string]any{"digest": "sha256:abc"}, BodyFile: payload,
	})
	if err == nil || strings.Contains(err.Error(), "attacker.example") || strings.Contains(err.Error(), "internal-capability") || requests != 1 {
		t.Fatalf("error=%v requests=%d", err, requests)
	}
}

func TestGCPArtifactRegistryWritesResponseFileAtomically(t *testing.T) {
	target := filepath.Join(t.TempDir(), "manifest.json")
	adapter := NewGCPRESTAdapter(GCPRESTConfig{
		Tokens: &staticTokenProvider{token: "private-token"},
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/vnd.oci.image.manifest.v1+json"}}, Body: io.NopCloser(strings.NewReader(`{"schemaVersion":2}`))}, nil
		}),
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderGCP, Mode: ModeRead, AuthScheme: authSchemeGCPArtifactRegistry,
		Service: "artifact-registry", Operation: "GetManifest", Method: http.MethodGet,
		URL: "https://gcr.io/v2/project/app/manifests/latest", ResponseFile: target, MaxResponseFileBytes: 1024,
	})
	data, readErr := os.ReadFile(target)
	if err != nil || readErr != nil || string(data) != `{"schemaVersion":2}` || strings.Contains(string(result.Output), "private-token") {
		t.Fatalf("result=%s data=%s err=%v read_err=%v", result.Output, data, err, readErr)
	}
}

func TestGCPArtifactRegistrySanitizesTokenFromTransportErrors(t *testing.T) {
	const token = "private-google-registry-token"
	adapter := NewGCPRESTAdapter(GCPRESTConfig{
		Tokens: &staticTokenProvider{token: token},
		HTTP: doerFunc(func(request *http.Request) (*http.Response, error) {
			return nil, errors.New("transport echoed " + request.Header.Get("Authorization"))
		}),
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderGCP, Mode: ModeRead, AuthScheme: authSchemeGCPArtifactRegistry,
		Service: "artifact-registry", Operation: "ListTags", Method: http.MethodGet,
		URL: "https://gcr.io/v2/project/app/tags/list",
	})
	if err == nil || strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "Bearer") {
		t.Fatalf("error=%v", err)
	}
}

func TestGCPArtifactRegistrySanitizesProviderErrorsAndRejectsRedirects(t *testing.T) {
	const token = "private-google-registry-token"
	for _, test := range []struct {
		name     string
		response *http.Response
	}{
		{
			name: "provider error",
			response: &http.Response{StatusCode: http.StatusUnauthorized, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{"error":"private-google-registry-token"}`))},
		},
		{
			name: "redirect",
			response: &http.Response{StatusCode: http.StatusTemporaryRedirect,
				Header: http.Header{"Location": {"https://attacker.example/blob?access_token=redirect-secret"}},
				Body:   io.NopCloser(strings.NewReader("private-google-registry-token redirect-secret"))},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			adapter := NewGCPRESTAdapter(GCPRESTConfig{
				Tokens: &staticTokenProvider{token: token},
				HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
					requests++
					return test.response, nil
				}),
			})
			_, err := adapter.Invoke(t.Context(), Invocation{
				Provider: ProviderGCP, Mode: ModeRead, AuthScheme: authSchemeGCPArtifactRegistry,
				Service: "artifact-registry", Operation: "GetBlob", Method: http.MethodGet,
				URL: "https://gcr.io/v2/project/app/blobs/sha256:abc",
			})
			if err == nil || strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "redirect-secret") || strings.Contains(err.Error(), "attacker.example") || requests != 1 {
				t.Fatalf("error=%v requests=%d", err, requests)
			}
		})
	}
}

func TestGCPArtifactRegistryDiscoveryIsPublicAndCredentialFree(t *testing.T) {
	tokens := &staticTokenProvider{token: "must-not-resolve"}
	requests := 0
	adapter := NewGCPRESTAdapter(GCPRESTConfig{Tokens: tokens, HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return nil, context.Canceled
	})})
	output, err := adapter.Discover(t.Context(), DiscoveryRequest{Provider: ProviderGCP, Service: "artifact-registry"})
	if err != nil || !strings.Contains(string(output), "artifact-registry/docs/reference/docker-api") || tokens.calls != 0 || requests != 0 {
		t.Fatalf("output=%s token_calls=%d requests=%d err=%v", output, tokens.calls, requests, err)
	}
}

func FuzzGCPArtifactRegistryInvocation(f *testing.F) {
	f.Add("artifact-registry", "ListTags", http.MethodGet, "https://us-docker.pkg.dev/v2/project/repository/app/tags/list")
	f.Add("artifact-registry", "GetManifest", http.MethodGet, "https://gcr.io/v2/project/app/manifests/latest")
	f.Add("artifact-registry", "RegistryOperation", http.MethodGet, "https://gcr.io.attacker.example/v2/project/app/tags/list")
	f.Fuzz(func(t *testing.T, service, operation, method, rawURL string) {
		_ = validateGCPArtifactRegistryInvocation(Invocation{Provider: ProviderGCP, AuthScheme: authSchemeGCPArtifactRegistry, Service: service, Operation: operation, Method: method, URL: rawURL})
	})
}

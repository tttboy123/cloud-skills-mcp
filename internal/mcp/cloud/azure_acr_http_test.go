package cloud

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAzureACRExchangesTokensInternallyAndUsesScopedAccessToken(t *testing.T) {
	tokens := &staticAzureTokenProvider{token: "entra-private-token"}
	var requests int
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		var body []byte
		if request.Body != nil {
			var err error
			body, err = io.ReadAll(request.Body)
			if err != nil {
				t.Fatalf("read request %d: %v", requests, err)
			}
		}
		switch requests {
		case 1:
			if request.Method != http.MethodPost || request.URL.String() != "https://registry123.azurecr.io/oauth2/exchange" {
				t.Fatalf("exchange request=%s %s", request.Method, request.URL)
			}
			assertACRForm(t, body, url.Values{
				"grant_type":   {"access_token"},
				"service":      {"registry123.azurecr.io"},
				"access_token": {"entra-private-token"},
			})
			if got := request.Header.Get("Authorization"); got != "" {
				t.Fatalf("exchange Authorization=%q", got)
			}
			return acrHTTPResponse(http.StatusOK, `{"refresh_token":"acr-private-refresh-token"}`), nil
		case 2:
			if request.Method != http.MethodPost || request.URL.String() != "https://registry123.azurecr.io/oauth2/token" {
				t.Fatalf("token request=%s %s", request.Method, request.URL)
			}
			assertACRForm(t, body, url.Values{
				"grant_type":    {"refresh_token"},
				"service":       {"registry123.azurecr.io"},
				"scope":         {"repository:team/app:pull"},
				"refresh_token": {"acr-private-refresh-token"},
			})
			return acrHTTPResponse(http.StatusOK, `{"access_token":"acr-private-access-token"}`), nil
		case 3:
			if request.Method != http.MethodGet || request.URL.String() != "https://registry123.azurecr.io/v2/team/app/tags/list?n=1" {
				t.Fatalf("data request=%s %s", request.Method, request.URL)
			}
			if got := request.Header.Get("Authorization"); got != "Bearer acr-private-access-token" {
				t.Fatalf("data Authorization=%q", got)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"X-Ms-Request-Id": {"acr-request-1"}},
				Body:       io.NopCloser(strings.NewReader(`{"name":"team/app","tags":["latest"]}`)),
			}, nil
		default:
			t.Fatalf("unexpected request %d: %s", requests, request.URL)
			return nil, nil
		}
	})
	adapter := NewAzureRESTAdapter(AzureRESTConfig{Tokens: tokens, HTTP: doer})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, Mode: ModeRead, AuthScheme: "acr", Service: "acr", Operation: "ListTags",
		Method: http.MethodGet, URL: "https://registry123.azurecr.io/v2/team/app/tags/list",
		Parameters: map[string]any{"n": 1}, ACRScope: "repository:team/app:pull",
	})
	if err != nil {
		t.Fatalf("invoke ACR: %v", err)
	}
	if string(result.Output) != `{"name":"team/app","tags":["latest"]}` || result.RequestID != "acr-request-1" {
		t.Fatalf("result=%#v", result)
	}
	if requests != 3 || tokens.calls != 1 || tokens.scope != "https://containerregistry.azure.net/.default" {
		t.Fatalf("requests=%d token_calls=%d token_scope=%q", requests, tokens.calls, tokens.scope)
	}
	for _, secret := range []string{"entra-private-token", "acr-private-refresh-token", "acr-private-access-token"} {
		if strings.Contains(string(result.Output), secret) {
			t.Fatalf("result leaked %q", secret)
		}
	}
}

func TestAzureACRRejectsUnsafeTargetsAndScopesBeforeIdentity(t *testing.T) {
	tests := []struct {
		name       string
		mode       InvocationMode
		method     string
		rawURL     string
		scope      string
		service    string
		headers    map[string]string
		wantAccept bool
	}{
		{name: "global repository", mode: ModeRead, method: http.MethodGet, rawURL: "https://registry123.azurecr.io/v2/team/app/manifests/latest", scope: "repository:team/app:pull", service: "acr", wantAccept: true},
		{name: "regional repository", mode: ModeRead, method: http.MethodHead, rawURL: "https://registry123.westus2.geo.azurecr.io/v2/team/app/blobs/sha256:abc", scope: "repository:team/app:pull", service: "acr", wantAccept: true},
		{name: "catalog", mode: ModeRead, method: http.MethodGet, rawURL: "https://registry123.azurecr.io/v2/_catalog", scope: "registry:catalog:*", service: "acr", wantAccept: true},
		{name: "docker v2 check", mode: ModeRead, method: http.MethodGet, rawURL: "https://registry123.azurecr.io/v2/", scope: "registry:catalog:*", service: "acr", wantAccept: true},
		{name: "repository properties", mode: ModeRead, method: http.MethodGet, rawURL: "https://registry123.azurecr.io/acr/v1/team/app", scope: "repository:team/app:metadata_read", service: "acr", wantAccept: true},
		{name: "repository contains protocol word", mode: ModeRead, method: http.MethodGet, rawURL: "https://registry123.azurecr.io/v2/team/manifests/app/manifests/latest", scope: "repository:team/manifests/app:pull", service: "acr", wantAccept: true},
		{name: "push", mode: ModeMutate, method: http.MethodPut, rawURL: "https://registry123.azurecr.io/v2/team/app/manifests/latest", scope: "repository:team/app:pull,push", service: "acr", wantAccept: true},
		{name: "lookalike", mode: ModeRead, method: http.MethodGet, rawURL: "https://registry123.azurecr.io.attacker.example/v2/team/app/tags/list", scope: "repository:team/app:pull", service: "acr"},
		{name: "nested global", mode: ModeRead, method: http.MethodGet, rawURL: "https://nested.registry123.azurecr.io/v2/team/app/tags/list", scope: "repository:team/app:pull", service: "acr"},
		{name: "data endpoint as login", mode: ModeRead, method: http.MethodGet, rawURL: "https://registry123.westus2.data.azurecr.io/v2/team/app/tags/list", scope: "repository:team/app:pull", service: "acr"},
		{name: "private link direct", mode: ModeRead, method: http.MethodGet, rawURL: "https://registry123.privatelink.azurecr.io/v2/team/app/tags/list", scope: "repository:team/app:pull", service: "acr"},
		{name: "token endpoint", mode: ModeRead, method: http.MethodPost, rawURL: "https://registry123.azurecr.io/oauth2/token", scope: "repository:team/app:pull", service: "acr"},
		{name: "missing scope", mode: ModeRead, method: http.MethodGet, rawURL: "https://registry123.azurecr.io/v2/team/app/tags/list", service: "acr"},
		{name: "mismatched repo", mode: ModeRead, method: http.MethodGet, rawURL: "https://registry123.azurecr.io/v2/team/app/tags/list", scope: "repository:other/app:pull", service: "acr"},
		{name: "read asks push", mode: ModeRead, method: http.MethodGet, rawURL: "https://registry123.azurecr.io/v2/team/app/tags/list", scope: "repository:team/app:pull,push", service: "acr"},
		{name: "mutate pull only", mode: ModeMutate, method: http.MethodPut, rawURL: "https://registry123.azurecr.io/v2/team/app/manifests/latest", scope: "repository:team/app:pull", service: "acr"},
		{name: "catalog on repo", mode: ModeRead, method: http.MethodGet, rawURL: "https://registry123.azurecr.io/v2/team/app/tags/list", scope: "registry:catalog:*", service: "acr"},
		{name: "wrong service", mode: ModeRead, method: http.MethodGet, rawURL: "https://registry123.azurecr.io/v2/team/app/tags/list", scope: "repository:team/app:pull", service: "registry"},
		{name: "caller authorization", mode: ModeRead, method: http.MethodGet, rawURL: "https://registry123.azurecr.io/v2/team/app/tags/list", scope: "repository:team/app:pull", service: "acr", headers: map[string]string{"Authorization": "Bearer caller"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tokens := &staticAzureTokenProvider{token: "must-not-resolve"}
			requests := 0
			adapter := NewAzureRESTAdapter(AzureRESTConfig{Tokens: tokens, HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
				requests++
				return nil, context.Canceled
			})})
			invocation := Invocation{
				Provider: ProviderAzure, Mode: test.mode, AuthScheme: "acr", Service: test.service, Operation: "Operation",
				Method: test.method, URL: test.rawURL, ACRScope: test.scope, Headers: test.headers,
			}
			err := validateInvocation(invocation, nil)
			if test.wantAccept {
				if err != nil {
					t.Fatalf("valid invocation rejected: %v", err)
				}
				return
			}
			if err == nil {
				_, err = adapter.Invoke(t.Context(), invocation)
			}
			if err == nil {
				t.Fatal("unsafe invocation accepted")
			}
			if tokens.calls != 0 || requests != 0 {
				t.Fatalf("unsafe invocation reached identity/network: token_calls=%d requests=%d", tokens.calls, requests)
			}
		})
	}
}

func TestAzureACRMatchesEveryCurrentDataPlaneScopeFamilyToPathAndMethod(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		rawURL     string
		scope      string
		wantAccept bool
	}{
		{name: "v2 catalog", method: http.MethodGet, rawURL: "https://registry123.azurecr.io/v2/_catalog", scope: "registry:catalog:*", wantAccept: true},
		{name: "acr catalog", method: http.MethodGet, rawURL: "https://registry123.azurecr.io/acr/v1/_catalog", scope: "registry:catalog:*", wantAccept: true},
		{name: "deleted catalog", method: http.MethodGet, rawURL: "https://registry123.azurecr.io/acr/v1/_deleted/_catalog", scope: "registry:deleted_catalog:*", wantAccept: true},
		{name: "pull manifest", method: http.MethodGet, rawURL: "https://registry123.azurecr.io/v2/team/app/manifests/latest", scope: "repository:team/app:pull", wantAccept: true},
		{name: "push manifest minimal", method: http.MethodPut, rawURL: "https://registry123.azurecr.io/v2/team/app/manifests/latest", scope: "repository:team/app:push", wantAccept: true},
		{name: "push manifest documented combined", method: http.MethodPut, rawURL: "https://registry123.azurecr.io/v2/team/app/manifests/latest", scope: "repository:team/app:pull,push", wantAccept: true},
		{name: "delete manifest", method: http.MethodDelete, rawURL: "https://registry123.azurecr.io/v2/team/app/manifests/sha256:abc", scope: "repository:team/app:delete", wantAccept: true},
		{name: "list tag metadata", method: http.MethodGet, rawURL: "https://registry123.azurecr.io/acr/v1/team/app/_tags", scope: "repository:team/app:metadata_read", wantAccept: true},
		{name: "update tag metadata", method: http.MethodPatch, rawURL: "https://registry123.azurecr.io/acr/v1/team/app/_tags/latest", scope: "repository:team/app:metadata_write,metadata_read", wantAccept: true},
		{name: "delete tag", method: http.MethodDelete, rawURL: "https://registry123.azurecr.io/acr/v1/team/app/_tags/latest", scope: "repository:team/app:delete", wantAccept: true},
		{name: "list deleted manifests", method: http.MethodGet, rawURL: "https://registry123.azurecr.io/acr/v1/_deleted/team/app/_manifests", scope: "repository:team/app:deleted_read", wantAccept: true},
		{name: "list deleted tags", method: http.MethodGet, rawURL: "https://registry123.azurecr.io/acr/v1/_deleted/team/app/_tags", scope: "repository:team/app:deleted_read", wantAccept: true},
		{name: "restore deleted manifest", method: http.MethodPost, rawURL: "https://registry123.azurecr.io/acr/v1/_deleted/team/app/_manifests/sha256:abc", scope: "repository:team/app:deleted_read,deleted_restore", wantAccept: true},
		{name: "catalog scope on deleted catalog", method: http.MethodGet, rawURL: "https://registry123.azurecr.io/acr/v1/_deleted/_catalog", scope: "registry:catalog:*"},
		{name: "deleted catalog scope on catalog", method: http.MethodGet, rawURL: "https://registry123.azurecr.io/v2/_catalog", scope: "registry:deleted_catalog:*"},
		{name: "pull scope on metadata", method: http.MethodGet, rawURL: "https://registry123.azurecr.io/acr/v1/team/app/_manifests", scope: "repository:team/app:pull"},
		{name: "metadata scope on content", method: http.MethodGet, rawURL: "https://registry123.azurecr.io/v2/team/app/manifests/latest", scope: "repository:team/app:metadata_read"},
		{name: "write scope on metadata read", method: http.MethodGet, rawURL: "https://registry123.azurecr.io/acr/v1/team/app/_tags", scope: "repository:team/app:metadata_write,metadata_read"},
		{name: "metadata read only on patch", method: http.MethodPatch, rawURL: "https://registry123.azurecr.io/acr/v1/team/app", scope: "repository:team/app:metadata_read"},
		{name: "delete scope on metadata patch", method: http.MethodPatch, rawURL: "https://registry123.azurecr.io/acr/v1/team/app", scope: "repository:team/app:delete"},
		{name: "deleted read on ordinary metadata", method: http.MethodGet, rawURL: "https://registry123.azurecr.io/acr/v1/team/app/_tags", scope: "repository:team/app:deleted_read"},
		{name: "metadata read on deleted list", method: http.MethodGet, rawURL: "https://registry123.azurecr.io/acr/v1/_deleted/team/app/_tags", scope: "repository:team/app:metadata_read"},
		{name: "restore scope on deleted list", method: http.MethodGet, rawURL: "https://registry123.azurecr.io/acr/v1/_deleted/team/app/_manifests", scope: "repository:team/app:deleted_read,deleted_restore"},
		{name: "deleted read only on restore", method: http.MethodPost, rawURL: "https://registry123.azurecr.io/acr/v1/_deleted/team/app/_manifests/sha256:abc", scope: "repository:team/app:deleted_read"},
		{name: "restore without digest", method: http.MethodPost, rawURL: "https://registry123.azurecr.io/acr/v1/_deleted/team/app/_manifests", scope: "repository:team/app:deleted_read,deleted_restore"},
		{name: "unsupported deleted tag item", method: http.MethodGet, rawURL: "https://registry123.azurecr.io/acr/v1/_deleted/team/app/_tags/latest", scope: "repository:team/app:deleted_read"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mode := ModeRead
			if test.method != http.MethodGet && test.method != http.MethodHead {
				mode = ModeMutate
			}
			err := validateInvocation(Invocation{
				Provider: ProviderAzure, Mode: mode, AuthScheme: "acr", Service: "acr", Operation: "Operation",
				Method: test.method, URL: test.rawURL, ACRScope: test.scope,
			}, nil)
			if test.wantAccept && err != nil {
				t.Fatalf("valid scope family rejected: %v", err)
			}
			if !test.wantAccept && err == nil {
				t.Fatal("path/scope mismatch accepted")
			}
		})
	}
}

func TestAzureACRCrossRepositoryMountUsesTwoExactScopes(t *testing.T) {
	tokens := &staticAzureTokenProvider{token: "entra-token"}
	requests := 0
	adapter := NewAzureRESTAdapter(AzureRESTConfig{Tokens: tokens, HTTP: doerFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		switch requests {
		case 1:
			return acrHTTPResponse(http.StatusOK, `{"refresh_token":"refresh-token"}`), nil
		case 2:
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			assertACRForm(t, body, url.Values{
				"grant_type":    {"refresh_token"},
				"service":       {"registry123.azurecr.io"},
				"scope":         {"repository:target/app:pull,push", "repository:source/base:pull"},
				"refresh_token": {"refresh-token"},
			})
			return acrHTTPResponse(http.StatusOK, `{"access_token":"access-token"}`), nil
		default:
			if request.URL.Query().Get("from") != "source/base" || request.URL.Query().Get("mount") != "sha256:abc" {
				t.Fatalf("mount query=%s", request.URL.RawQuery)
			}
			return &http.Response{StatusCode: http.StatusCreated, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
		}
	})})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, Mode: ModeMutate, AuthScheme: "acr", Service: "acr", Operation: "MountBlob",
		Method: http.MethodPost, URL: "https://registry123.azurecr.io/v2/target/app/blobs/uploads/",
		Parameters: map[string]any{"mount": "sha256:abc", "from": "source/base"},
		ACRScope:   "repository:target/app:pull,push", ACRSourceScope: "repository:source/base:pull",
	})
	if err != nil || requests != 3 {
		t.Fatalf("requests=%d err=%v", requests, err)
	}
}

func TestAzureACRCrossRepositoryMountRejectsScopeMismatchBeforeIdentity(t *testing.T) {
	tests := []Invocation{
		{
			Provider: ProviderAzure, Mode: ModeMutate, AuthScheme: "acr", Service: "acr", Operation: "MountBlob",
			Method: http.MethodPost, URL: "https://registry123.azurecr.io/v2/target/app/blobs/uploads/",
			Parameters: map[string]any{"mount": "sha256:abc", "from": "source/base"}, ACRScope: "repository:target/app:pull,push",
		},
		{
			Provider: ProviderAzure, Mode: ModeMutate, AuthScheme: "acr", Service: "acr", Operation: "MountBlob",
			Method: http.MethodPost, URL: "https://registry123.azurecr.io/v2/target/app/blobs/uploads/",
			Parameters: map[string]any{"mount": "sha256:abc", "from": "source/base"}, ACRScope: "repository:target/app:pull,push", ACRSourceScope: "repository:other/base:pull",
		},
		{
			Provider: ProviderAzure, Mode: ModeMutate, AuthScheme: "acr", Service: "acr", Operation: "StartUpload",
			Method: http.MethodPost, URL: "https://registry123.azurecr.io/v2/target/app/blobs/uploads/",
			ACRScope: "repository:target/app:pull,push", ACRSourceScope: "repository:source/base:pull",
		},
	}
	for index, invocation := range tests {
		t.Run(string(rune('a'+index)), func(t *testing.T) {
			tokens := &staticAzureTokenProvider{token: "must-not-resolve"}
			adapter := NewAzureRESTAdapter(AzureRESTConfig{Tokens: tokens})
			if _, err := adapter.Invoke(t.Context(), invocation); err == nil {
				t.Fatal("unsafe mount scope accepted")
			}
			if tokens.calls != 0 {
				t.Fatalf("unsafe mount reached identity: %d", tokens.calls)
			}
		})
	}
}

func TestAzureACRFollowsOnlyProviderOwnedReadRedirectWithoutAuthorization(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "layer.bin")
	tokens := &staticAzureTokenProvider{token: "entra-token"}
	var requests int
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		switch requests {
		case 1:
			return acrHTTPResponse(http.StatusOK, `{"refresh_token":"refresh-token"}`), nil
		case 2:
			return acrHTTPResponse(http.StatusOK, `{"access_token":"access-token"}`), nil
		case 3:
			if request.Header.Get("Authorization") != "Bearer access-token" {
				t.Fatalf("registry Authorization=%q", request.Header.Get("Authorization"))
			}
			return &http.Response{
				StatusCode: http.StatusTemporaryRedirect,
				Header:     http.Header{"Location": {"https://registry123.westus2.data.azurecr.io/layers/sha256:abc?sig=internal"}},
				Body:       io.NopCloser(strings.NewReader("redirect body must not escape")),
			}, nil
		case 4:
			if request.URL.Host != "registry123.westus2.data.azurecr.io" || request.Header.Get("Authorization") != "" {
				t.Fatalf("redirect host=%q Authorization=%q", request.URL.Host, request.Header.Get("Authorization"))
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"application/octet-stream"}, "X-Ms-Request-Id": {"data-request"}},
				Body:       io.NopCloser(strings.NewReader("layer-data")),
			}, nil
		default:
			t.Fatalf("unexpected request %d", requests)
			return nil, nil
		}
	})
	adapter := NewAzureRESTAdapter(AzureRESTConfig{Tokens: tokens, HTTP: doer})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, Mode: ModeRead, AuthScheme: "acr", Service: "acr", Operation: "GetBlob",
		Method: http.MethodGet, URL: "https://registry123.azurecr.io/v2/team/app/blobs/sha256:abc",
		ACRScope: "repository:team/app:pull", ResponseFile: target, MaxResponseFileBytes: 1024,
	})
	if err != nil {
		t.Fatalf("invoke redirected ACR read: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "layer-data" {
		t.Fatalf("response_file=%q err=%v", data, err)
	}
	if strings.Contains(string(result.Output), "sig=internal") || result.RequestID != "data-request" {
		t.Fatalf("result=%s request_id=%q", result.Output, result.RequestID)
	}
}

func TestAzureACRRejectsCrossProviderRedirectWithoutPublishingFile(t *testing.T) {
	target := filepath.Join(t.TempDir(), "layer.bin")
	tokens := &staticAzureTokenProvider{token: "entra-token"}
	requests := 0
	adapter := NewAzureRESTAdapter(AzureRESTConfig{Tokens: tokens, HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
		requests++
		switch requests {
		case 1:
			return acrHTTPResponse(http.StatusOK, `{"refresh_token":"refresh-token"}`), nil
		case 2:
			return acrHTTPResponse(http.StatusOK, `{"access_token":"access-token"}`), nil
		default:
			return &http.Response{
				StatusCode: http.StatusTemporaryRedirect,
				Header:     http.Header{"Location": {"https://registry123.westus2.data.azurecr.io.attacker.example/layer?sig=secret"}},
				Body:       io.NopCloser(strings.NewReader("do not expose")),
			}, nil
		}
	})})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, Mode: ModeRead, AuthScheme: "acr", Service: "acr", Operation: "GetBlob",
		Method: http.MethodGet, URL: "https://registry123.azurecr.io/v2/team/app/blobs/sha256:abc",
		ACRScope: "repository:team/app:pull", ResponseFile: target, MaxResponseFileBytes: 1024,
	})
	if err == nil || strings.Contains(err.Error(), "sig=secret") || strings.Contains(err.Error(), "do not expose") {
		t.Fatalf("error=%v", err)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("response_file published after unsafe redirect: %v", statErr)
	}
}

func TestAzureACRTokenFailuresNeverExposeTokenResponseBody(t *testing.T) {
	for _, response := range []*http.Response{
		acrHTTPResponse(http.StatusUnauthorized, `{"error":"private-provider-diagnostic"}`),
		acrHTTPResponse(http.StatusOK, `{"refresh_token":"`+strings.Repeat("x", 70*1024)+`"}`),
		acrHTTPResponse(http.StatusOK, `{"unexpected":"private-provider-diagnostic"}`),
	} {
		t.Run(http.StatusText(response.StatusCode), func(t *testing.T) {
			tokens := &staticAzureTokenProvider{token: "entra-token"}
			adapter := NewAzureRESTAdapter(AzureRESTConfig{Tokens: tokens, HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
				return response, nil
			})})
			_, err := adapter.Invoke(t.Context(), Invocation{
				Provider: ProviderAzure, Mode: ModeRead, AuthScheme: "acr", Service: "acr", Operation: "ListTags",
				Method: http.MethodGet, URL: "https://registry123.azurecr.io/v2/team/app/tags/list", ACRScope: "repository:team/app:pull",
			})
			if err == nil || strings.Contains(err.Error(), "private-provider-diagnostic") || strings.Contains(err.Error(), strings.Repeat("x", 128)) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestAzureACRProviderErrorsCannotEchoInternalAccessToken(t *testing.T) {
	tokens := &staticAzureTokenProvider{token: "entra-token"}
	requests := 0
	adapter := NewAzureRESTAdapter(AzureRESTConfig{Tokens: tokens, HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
		requests++
		switch requests {
		case 1:
			return acrHTTPResponse(http.StatusOK, `{"refresh_token":"refresh-token"}`), nil
		case 2:
			return acrHTTPResponse(http.StatusOK, `{"access_token":"access-token-raw-echo"}`), nil
		default:
			return acrHTTPResponse(http.StatusInternalServerError, "access-token-raw-echo"), nil
		}
	})})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, Mode: ModeRead, AuthScheme: "acr", Service: "acr", Operation: "ListTags",
		Method: http.MethodGet, URL: "https://registry123.azurecr.io/v2/team/app/tags/list", ACRScope: "repository:team/app:pull",
	})
	if err == nil || strings.Contains(err.Error(), "access-token-raw-echo") {
		t.Fatalf("error=%v", err)
	}
}

func TestAzureACRRedirectTransportErrorCannotExposeSignedLocation(t *testing.T) {
	tokens := &staticAzureTokenProvider{token: "entra-token"}
	requests := 0
	adapter := NewAzureRESTAdapter(AzureRESTConfig{Tokens: tokens, HTTP: doerFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		switch requests {
		case 1:
			return acrHTTPResponse(http.StatusOK, `{"refresh_token":"refresh-token"}`), nil
		case 2:
			return acrHTTPResponse(http.StatusOK, `{"access_token":"access-token"}`), nil
		case 3:
			return &http.Response{
				StatusCode: http.StatusTemporaryRedirect,
				Header:     http.Header{"Location": {"https://registry123.westus2.data.azurecr.io/layer?sig=signed-location-secret"}},
				Body:       io.NopCloser(strings.NewReader("")),
			}, nil
		default:
			return nil, &url.Error{Op: "Get", URL: request.URL.String(), Err: context.DeadlineExceeded}
		}
	})})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAzure, Mode: ModeRead, AuthScheme: "acr", Service: "acr", Operation: "GetBlob",
		Method: http.MethodGet, URL: "https://registry123.azurecr.io/v2/team/app/blobs/sha256:abc", ACRScope: "repository:team/app:pull",
	})
	if err == nil || strings.Contains(err.Error(), "signed-location-secret") {
		t.Fatalf("error=%v", err)
	}
}

func FuzzAzureACRInvocationValidation(f *testing.F) {
	for _, seed := range []struct{ method, rawURL, scope, sourceScope string }{
		{"GET", "https://registry123.azurecr.io/v2/team/app/tags/list", "repository:team/app:pull", ""},
		{"GET", "https://registry123.westus2.geo.azurecr.io/v2/_catalog", "registry:catalog:*", ""},
		{"POST", "https://registry123.azurecr.io/v2/target/app/blobs/uploads/?mount=sha256:abc&from=source/base", "repository:target/app:pull,push", "repository:source/base:pull"},
		{"GET", "https://registry123.azurecr.io.attacker.example/v2/team/app/tags/list", "repository:team/app:pull", ""},
	} {
		f.Add(seed.method, seed.rawURL, seed.scope, seed.sourceScope)
	}
	f.Fuzz(func(t *testing.T, method, rawURL, scope, sourceScope string) {
		if len(method)+len(rawURL)+len(scope)+len(sourceScope) > 4096 {
			t.Skip()
		}
		_ = validateAzureACRInvocation(Invocation{
			Provider: ProviderAzure, Mode: ModeMutate, AuthScheme: "acr", Service: "acr", Operation: "FuzzOperation",
			Method: method, URL: rawURL, ACRScope: scope, ACRSourceScope: sourceScope,
		})
	})
}

func assertACRForm(t *testing.T, body []byte, want url.Values) {
	t.Helper()
	got, err := url.ParseQuery(string(body))
	if err != nil {
		t.Fatalf("parse form: %v", err)
	}
	if got.Encode() != want.Encode() {
		t.Fatalf("form=%q want=%q", got.Encode(), want.Encode())
	}
}

func acrHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

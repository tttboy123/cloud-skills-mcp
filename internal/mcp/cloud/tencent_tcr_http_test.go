package cloud

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type countingTencentTCRCredentialProvider struct {
	calls int
}

func (provider *countingTencentTCRCredentialProvider) Credentials(context.Context) (TencentCredentials, error) {
	provider.calls++
	return TencentCredentials{SecretID: "id", SecretKey: "key", Token: "cam-session-token"}, nil
}

func TestTencentTCRUsesCAMTemporaryCredentialInternally(t *testing.T) {
	credentials := &countingTencentTCRCredentialProvider{}
	requests := 0
	adapter := NewTencentRESTAdapter(TencentRESTConfig{
		Credentials: credentials,
		Now:         func() time.Time { return time.Unix(1785736800, 0).UTC() },
		HTTP: doerFunc(func(request *http.Request) (*http.Response, error) {
			requests++
			switch requests {
			case 1:
				if request.Method != http.MethodPost || request.URL.String() != "https://tcr.tencentcloudapi.com/" {
					t.Fatalf("token request=%s %s", request.Method, request.URL)
				}
				if request.Header.Get("X-TC-Action") != "CreateInstanceToken" || request.Header.Get("X-TC-Version") != "2019-09-24" || request.Header.Get("X-TC-Region") != "ap-guangzhou" || !strings.HasPrefix(request.Header.Get("Authorization"), "TC3-HMAC-SHA256 Credential=id/") {
					t.Fatalf("token headers=%v", request.Header)
				}
				if request.Header.Get("X-TC-Token") != "cam-session-token" {
					t.Fatalf("internal CAM token=%q", request.Header.Get("X-TC-Token"))
				}
				body, err := io.ReadAll(request.Body)
				if err != nil || string(body) != `{"RegistryId":"tcr-example123","TokenType":"temp"}` {
					t.Fatalf("token body=%q err=%v", body, err)
				}
				return tencentTCRHTTPResponse(http.StatusOK, `{"Response":{"ExpTime":1785740400000,"RequestId":"token-request-1","Token":"private-tcr-token","TokenId":"","Username":"100036950234"}}`), nil
			case 2:
				wantBasic := "Basic " + base64.StdEncoding.EncodeToString([]byte("100036950234:private-tcr-token"))
				if request.URL.String() != "https://demo-tcr.tencentcloudcr.com/v2/team/app/tags/list?n=1" || request.Header.Get("Authorization") != wantBasic {
					t.Fatalf("registry request=%s headers=%v", request.URL, request.Header)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Request-Id": {"registry-request-1"}}, Body: io.NopCloser(strings.NewReader(`{"name":"team/app","tags":["latest"]}`))}, nil
			default:
				t.Fatalf("unexpected request %d", requests)
				return nil, context.Canceled
			}
		}),
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderTencent, Mode: ModeRead, AuthScheme: authSchemeTencentTCR,
		Service: "tcr", Operation: "ListTags", Region: "ap-guangzhou", RegistryInstanceID: "tcr-example123",
		Method: http.MethodGet, URL: "https://demo-tcr.tencentcloudcr.com/v2/team/app/tags/list", Parameters: map[string]any{"n": 1},
	})
	if err != nil {
		t.Fatalf("invoke Tencent TCR: %v", err)
	}
	if string(result.Output) != `{"name":"team/app","tags":["latest"]}` || result.RequestID != "registry-request-1" || requests != 2 || credentials.calls != 1 {
		t.Fatalf("result=%#v requests=%d credential_calls=%d", result, requests, credentials.calls)
	}
	for _, secret := range []string{"key", "cam-session-token", "private-tcr-token"} {
		if strings.Contains(string(result.Output), secret) {
			t.Fatalf("result leaked %q", secret)
		}
	}
}

func TestTencentTCRRegistryRedirectFailsClosed(t *testing.T) {
	requests := 0
	adapter := NewTencentRESTAdapter(TencentRESTConfig{
		Credentials: staticTencentCredentialsProvider{credentials: TencentCredentials{SecretID: "private-id", SecretKey: "private-key"}},
		Now:         func() time.Time { return time.Unix(1785736800, 0).UTC() },
		HTTP: doerFunc(func(request *http.Request) (*http.Response, error) {
			requests++
			if requests == 1 {
				return tencentTCRHTTPResponse(http.StatusOK, `{"Response":{"ExpTime":1785740400000,"Token":"temporary-password","TokenId":"","Username":"100036950234"}}`), nil
			}
			if requests == 2 {
				return &http.Response{
					StatusCode: http.StatusTemporaryRedirect,
					Header:     http.Header{"Location": {"https://attacker.example/private-layer"}},
					Body:       io.NopCloser(strings.NewReader(`{"message":"redirect"}`)),
				}, nil
			}
			t.Fatalf("redirect followed with request %d to %s", requests, request.URL)
			return nil, context.Canceled
		}),
	})
	_, err := adapter.Invoke(t.Context(), Invocation{Provider: ProviderTencent, Mode: ModeRead, AuthScheme: authSchemeTencentTCR, Service: "tcr", Operation: "GetBlob", Region: "ap-guangzhou", RegistryInstanceID: "tcr-example123", Method: http.MethodGet, URL: "https://demo-tcr.tencentcloudcr.com/v2/team/app/blobs/sha256:abcdef"})
	if err == nil || requests != 2 {
		t.Fatalf("redirect err=%v requests=%d", err, requests)
	}
	for _, secret := range []string{"private-id", "private-key", "temporary-password"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("redirect error leaked %q: %v", secret, err)
		}
	}
}

func TestTencentTCRAcceptsOnlyEnterpriseRegistryEndpoints(t *testing.T) {
	base := Invocation{Provider: ProviderTencent, Mode: ModeRead, AuthScheme: authSchemeTencentTCR, Service: "tcr", Operation: "ListTags", Region: "ap-guangzhou", RegistryInstanceID: "tcr-example123", Method: http.MethodGet}
	for _, rawURL := range []string{
		"https://demo-tcr.tencentcloudcr.com/v2/team/app/tags/list",
		"https://demo-tcr-vpc.tencentcloudcr.com/v2/team/app/tags/list",
	} {
		invocation := base
		invocation.URL = rawURL
		if err := validateInvocation(invocation, nil); err != nil {
			t.Fatalf("official endpoint %q rejected: %v", rawURL, err)
		}
	}
	custom := base
	custom.URL = "https://registry.example.com/v2/team/app/tags/list"
	if err := validateInvocationWithEndpointHosts(custom, nil, nil); err == nil {
		t.Fatal("caller-only custom TCR domain accepted")
	}
	if err := validateInvocationWithEndpointHosts(custom, nil, []string{"registry.example.com"}); err != nil {
		t.Fatalf("operator-pinned custom TCR domain rejected: %v", err)
	}
}

func TestTencentTCRRegistryResourcePathMatrix(t *testing.T) {
	base := Invocation{Provider: ProviderTencent, Mode: ModeRead, AuthScheme: authSchemeTencentTCR, Service: "tcr", Operation: "RegistryOperation", Region: "ap-guangzhou", RegistryInstanceID: "tcr-example123"}
	valid := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/v2/"},
		{http.MethodHead, "/v2/"},
		{http.MethodGet, "/v2/_catalog"},
		{http.MethodGet, "/v2/team/app/manifests/latest"},
		{http.MethodHead, "/v2/team/app/manifests/sha256:abcdef"},
		{http.MethodPut, "/v2/team/app/manifests/latest"},
		{http.MethodDelete, "/v2/team/app/manifests/sha256:abcdef"},
		{http.MethodGet, "/v2/team/app/blobs/sha256:abcdef"},
		{http.MethodHead, "/v2/team/app/blobs/sha256:abcdef"},
		{http.MethodDelete, "/v2/team/app/blobs/sha256:abcdef"},
		{http.MethodGet, "/v2/team/app/referrers/sha256:abcdef"},
	}
	for _, test := range valid {
		invocation := base
		invocation.Method = test.method
		invocation.URL = "https://demo-tcr.tencentcloudcr.com" + test.path
		if err := validateTencentTCRInvocation(invocation, nil); err != nil {
			t.Errorf("%s %s rejected: %v", test.method, test.path, err)
		}
	}
	invalid := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/v2/"},
		{http.MethodHead, "/v2/_catalog"},
		{http.MethodGet, "/v1/team/app/tags/list"},
		{http.MethodGet, "/v2/team/app"},
		{http.MethodGet, "/v2/app/manifests/latest"},
		{http.MethodPost, "/v2/team/app/manifests/latest"},
		{http.MethodGet, "/v2/team/app/manifests/a/b"},
		{http.MethodHead, "/v2/team/app/tags/list"},
		{http.MethodGet, "/v2/team/app/tags/other"},
		{http.MethodHead, "/v2/team/app/referrers/sha256:abcdef"},
		{http.MethodGet, "/v2/team/app/referrers/"},
		{http.MethodPost, "/v2/team/app/blobs/sha256:abcdef"},
		{http.MethodGet, "/v2/team/app/blobs/"},
	}
	for _, test := range invalid {
		invocation := base
		invocation.Method = test.method
		invocation.URL = "https://demo-tcr.tencentcloudcr.com" + test.path
		if err := validateTencentTCRInvocation(invocation, nil); err == nil {
			t.Errorf("%s %s accepted", test.method, test.path)
		}
	}
}

func TestTencentTCRRejectsMalformedInvocationShapes(t *testing.T) {
	base := Invocation{Provider: ProviderTencent, Mode: ModeRead, AuthScheme: authSchemeTencentTCR, Service: "tcr", Operation: "ListTags", Region: "ap-guangzhou", RegistryInstanceID: "tcr-example123", Method: http.MethodGet, URL: "https://demo-tcr.tencentcloudcr.com/v2/team/app/tags/list"}
	tests := map[string]func(*Invocation){
		"service":        func(value *Invocation) { value.Service = "cos" },
		"operation":      func(value *Invocation) { value.Operation = "bad operation" },
		"api version":    func(value *Invocation) { value.APIVersion = "2019-09-24" },
		"method":         func(value *Invocation) { value.Method = http.MethodConnect },
		"url user":       func(value *Invocation) { value.URL = "https://user@demo-tcr.tencentcloudcr.com/v2/team/app/tags/list" },
		"credential url": func(value *Invocation) { value.URL += "?access_token=caller" },
		"credential arg": func(value *Invocation) { value.Parameters = map[string]any{"signature": "caller"} },
		"escaped path": func(value *Invocation) {
			value.URL = "https://demo-tcr.tencentcloudcr.com/v2/team/app/manifests/%6catest"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			invocation := base
			mutate(&invocation)
			if err := validateTencentTCRInvocation(invocation, nil); err == nil {
				t.Fatal("malformed invocation accepted")
			}
		})
	}
}

func TestTencentTCRRejectsUnsafeTargetsBeforeCredentials(t *testing.T) {
	base := Invocation{Provider: ProviderTencent, Mode: ModeRead, AuthScheme: authSchemeTencentTCR, Service: "tcr", Operation: "ListTags", Region: "ap-guangzhou", RegistryInstanceID: "tcr-example123", Method: http.MethodGet, URL: "https://demo-tcr.tencentcloudcr.com/v2/team/app/tags/list"}
	tests := map[string]func(*Invocation){
		"personal edition": func(value *Invocation) { value.URL = "https://ccr.ccs.tencentyun.com/v2/team/app/tags/list" },
		"lookalike": func(value *Invocation) {
			value.URL = "https://demo-tcr.tencentcloudcr.com.attacker.example/v2/team/app/tags/list"
		},
		"nested": func(value *Invocation) {
			value.URL = "https://nested.demo-tcr.tencentcloudcr.com/v2/team/app/tags/list"
		},
		"missing region":  func(value *Invocation) { value.Region = "" },
		"bad instance id": func(value *Invocation) { value.RegistryInstanceID = "tcr-example/steal" },
		"caller auth":     func(value *Invocation) { value.Headers = map[string]string{"Authorization": "Basic caller"} },
		"token endpoint":  func(value *Invocation) { value.URL = "https://demo-tcr.tencentcloudcr.com/v2/token" },
		"custom port":     func(value *Invocation) { value.URL = "https://demo-tcr.tencentcloudcr.com:8443/v2/team/app/tags/list" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			invocation := base
			mutate(&invocation)
			credentials := &countingTencentCredentialProvider{}
			requests := 0
			adapter := NewTencentRESTAdapter(TencentRESTConfig{Credentials: credentials, HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
				requests++
				return nil, context.Canceled
			})})
			err := validateInvocation(invocation, nil)
			if err == nil {
				_, err = adapter.Invoke(t.Context(), invocation)
			}
			if err == nil || credentials.calls != 0 || requests != 0 {
				t.Fatalf("unsafe target err=%v credential_calls=%d requests=%d", err, credentials.calls, requests)
			}
		})
	}
}

func TestTencentTCRValidatesUploadAndCrossRepositoryMountPlans(t *testing.T) {
	base := Invocation{Provider: ProviderTencent, Mode: ModeMutate, AuthScheme: authSchemeTencentTCR, Service: "tcr", Operation: "UploadBlob", Region: "ap-guangzhou", RegistryInstanceID: "tcr-example123", URL: "https://demo-tcr.tencentcloudcr.com/v2/team/app/blobs/uploads/"}
	valid := []Invocation{
		func() Invocation { value := base; value.Method = http.MethodPost; return value }(),
		func() Invocation {
			value := base
			value.Method = http.MethodPost
			value.Parameters = map[string]any{"mount": "sha256:abcdef", "from": "team/source"}
			return value
		}(),
		func() Invocation {
			value := base
			value.Method = http.MethodPost
			value.Parameters = map[string]any{"digest": "sha256:abcdef"}
			value.Body = "layer"
			return value
		}(),
		func() Invocation {
			value := base
			value.Method = http.MethodPatch
			value.URL += "session-id"
			value.Body = "chunk"
			return value
		}(),
		func() Invocation {
			value := base
			value.Method = http.MethodPut
			value.URL += "session-id"
			value.Parameters = map[string]any{"digest": "sha256:abcdef"}
			return value
		}(),
	}
	for _, invocation := range valid {
		if err := validateTencentTCRInvocation(invocation, nil); err != nil {
			t.Fatalf("valid upload plan rejected: %#v: %v", invocation, err)
		}
	}
	invalid := []Invocation{
		func() Invocation {
			value := base
			value.Method = http.MethodPost
			value.Parameters = map[string]any{"mount": "sha256:abcdef"}
			return value
		}(),
		func() Invocation {
			value := base
			value.Method = http.MethodPost
			value.Parameters = map[string]any{"mount": "bad", "from": "team/source"}
			return value
		}(),
		func() Invocation {
			value := base
			value.Method = http.MethodPost
			value.Parameters = map[string]any{"digest": "sha256:abcdef"}
			return value
		}(),
		func() Invocation {
			value := base
			value.Method = http.MethodPatch
			value.URL += "session-id"
			return value
		}(),
		func() Invocation {
			value := base
			value.Method = http.MethodPut
			value.URL += "session-id"
			return value
		}(),
	}
	for _, invocation := range invalid {
		if err := validateTencentTCRInvocation(invocation, nil); err == nil {
			t.Fatalf("invalid upload plan accepted: %#v", invocation)
		}
	}
}

func TestTencentTCRRejectsLongTermOrMalformedInternalCredentials(t *testing.T) {
	tests := map[string]string{
		"long term":      `{"Response":{"ExpTime":1785740400000,"Token":"private-token","TokenId":"long-term-id","Username":"100036950234"}}`,
		"provider error": `{"Response":{"Error":{"Code":"UnauthorizedOperation","Message":"private-provider-message"},"RequestId":"request-id"}}`,
		"missing expiry": `{"Response":{"Token":"private-token","TokenId":"","Username":"100036950234"}}`,
		"invalid json":   `{`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			adapter := NewTencentRESTAdapter(TencentRESTConfig{HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
				return tencentTCRHTTPResponse(http.StatusOK, body), nil
			})})
			_, err := adapter.getTencentTCRTemporaryCredential(t.Context(), tencentTCRTarget{InstanceID: "tcr-example123", Region: "ap-guangzhou"}, TencentCredentials{SecretID: "private-id", SecretKey: "private-key", Token: "private-session"})
			if err == nil {
				t.Fatal("unsafe internal credential accepted")
			}
			for _, secret := range []string{"private-id", "private-key", "private-session", "private-token", "long-term-id", "private-provider-message"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("error leaked %q: %v", secret, err)
				}
			}
		})
	}
}

func TestTencentTCRTransportErrorsCannotEchoCredentials(t *testing.T) {
	requests := 0
	secrets := []string{"private-id", "private-key", "private-session", "temporary-password"}
	adapter := NewTencentRESTAdapter(TencentRESTConfig{
		Credentials: staticTencentCredentialsProvider{credentials: TencentCredentials{SecretID: secrets[0], SecretKey: secrets[1], Token: secrets[2]}},
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			requests++
			if requests == 1 {
				return tencentTCRHTTPResponse(http.StatusOK, `{"Response":{"ExpTime":1785740400000,"Token":"temporary-password","TokenId":"","Username":"100036950234"}}`), nil
			}
			return nil, errors.New(strings.Join(secrets, " "))
		}),
	})
	_, err := adapter.Invoke(t.Context(), Invocation{Provider: ProviderTencent, Mode: ModeRead, AuthScheme: authSchemeTencentTCR, Service: "tcr", Operation: "ListTags", Region: "ap-guangzhou", RegistryInstanceID: "tcr-example123", Method: http.MethodGet, URL: "https://demo-tcr.tencentcloudcr.com/v2/team/app/tags/list"})
	if err == nil {
		t.Fatal("transport error accepted")
	}
	for _, secret := range secrets {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaked %q: %v", secret, err)
		}
	}
}

func tencentTCRHTTPResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func FuzzTencentTCRInvocation(f *testing.F) {
	f.Add("https://demo-tcr.tencentcloudcr.com/v2/team/app/tags/list")
	f.Add("https://demo-tcr.tencentcloudcr.com.attacker.example/v2/team/app/tags/list")
	f.Fuzz(func(t *testing.T, rawURL string) {
		_, _ = parseTencentTCRInvocation(Invocation{Provider: ProviderTencent, Mode: ModeRead, AuthScheme: authSchemeTencentTCR, Service: "tcr", Operation: "ListTags", Region: "ap-guangzhou", RegistryInstanceID: "tcr-example123", Method: http.MethodGet, URL: rawURL}, nil)
	})
}

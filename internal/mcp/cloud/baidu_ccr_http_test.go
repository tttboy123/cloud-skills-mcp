package cloud

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type countingBCECredentialProvider struct {
	calls int
}

func (provider *countingBCECredentialProvider) Credentials(context.Context) (BCECredentials, error) {
	provider.calls++
	return BCECredentials{AccessKeyID: "bce-id", SecretAccessKey: "bce-key", SessionToken: "bce-session"}, nil
}

func TestBaiduCCREnterpriseUsesOnlyInternalTemporaryCredentials(t *testing.T) {
	credentials := &countingBCECredentialProvider{}
	requests := 0
	registryHost := "ccr-example12-pub.cnc.bd.bj.baidubce.com"
	adapter := NewBaiduRESTAdapter(BaiduRESTConfig{
		Credentials: credentials,
		Now:         func() time.Time { return time.Date(2026, 8, 3, 18, 0, 0, 0, time.UTC) },
		HTTP: doerFunc(func(request *http.Request) (*http.Response, error) {
			requests++
			switch requests {
			case 1:
				if request.Method != http.MethodGet || request.URL.String() != "https://ccr.bd.baidubce.com/v1/users/profile?userId=iam-user-123" {
					t.Fatalf("profile request=%s %s", request.Method, request.URL)
				}
				assertBaiduCCRControlAuthorization(t, request)
				return baiduCCRHTTPResponse(http.StatusOK, `{"name":"registry-user"}`), nil
			case 2:
				if request.Method != http.MethodPost || request.URL.String() != "https://ccr.bd.baidubce.com/v1/instances/ccr-example12/credential" {
					t.Fatalf("credential request=%s %s", request.Method, request.URL)
				}
				assertBaiduCCRControlAuthorization(t, request)
				body, _ := io.ReadAll(request.Body)
				if string(body) != `{"duration":1}` {
					t.Fatalf("credential body=%q", body)
				}
				return baiduCCRHTTPResponse(http.StatusOK, `{"password":"temporary-password"}`), nil
			case 3:
				if request.URL.String() != "https://"+registryHost+"/v2/" || request.Header.Get("Authorization") != "" {
					t.Fatalf("challenge request=%s auth=%q", request.URL, request.Header.Get("Authorization"))
				}
				response := baiduCCRHTTPResponse(http.StatusUnauthorized, `{}`)
				response.Header.Set("Www-Authenticate", `Bearer realm="https://`+registryHost+`/service/token",service="harbor-registry"`)
				return response, nil
			case 4:
				if request.URL.Scheme != "https" || request.URL.Host != registryHost || request.URL.Path != "/service/token" {
					t.Fatalf("Bearer request=%s", request.URL)
				}
				if request.URL.Query().Get("service") != "harbor-registry" || request.URL.Query()["scope"][0] != "repository:team/app:pull" {
					t.Fatalf("Bearer query=%v", request.URL.Query())
				}
				wantBasic := "Basic " + base64.StdEncoding.EncodeToString([]byte("registry-user:temporary-password"))
				if request.Header.Get("Authorization") != wantBasic {
					t.Fatalf("Bearer auth=%q", request.Header.Get("Authorization"))
				}
				return baiduCCRHTTPResponse(http.StatusOK, `{"token":"registry-bearer"}`), nil
			case 5:
				if request.URL.String() != "https://"+registryHost+"/v2/team/app/tags/list?n=1" || request.Header.Get("Authorization") != "Bearer registry-bearer" {
					t.Fatalf("Registry request=%s headers=%v", request.URL, request.Header)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Bce-Request-Id": {"registry-request"}}, Body: io.NopCloser(strings.NewReader(`{"name":"team/app","tags":["latest"]}`))}, nil
			default:
				t.Fatalf("unexpected request %d", requests)
				return nil, context.Canceled
			}
		}),
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderBaidu, Mode: ModeRead, AuthScheme: authSchemeBaiduCCR,
		Service: "ccr", Operation: "ListTags", Region: "bj", RegistryInstanceID: "ccr-example12", RegistryUserID: "iam-user-123",
		Method: http.MethodGet, URL: "https://" + registryHost + "/v2/team/app/tags/list", Parameters: map[string]any{"n": 1},
	})
	if err != nil {
		t.Fatalf("invoke Baidu CCR Enterprise: %v", err)
	}
	if string(result.Output) != `{"name":"team/app","tags":["latest"]}` || result.RequestID != "registry-request" || requests != 5 || credentials.calls != 1 {
		t.Fatalf("result=%#v requests=%d credential_calls=%d", result, requests, credentials.calls)
	}
	for _, secret := range []string{"bce-key", "bce-session", "temporary-password", "registry-bearer"} {
		if strings.Contains(string(result.Output), secret) {
			t.Fatalf("result leaked %q", secret)
		}
	}
}

func TestBaiduCCRPersonalUsesAKSKTemporaryToken(t *testing.T) {
	requests := 0
	adapter := NewBaiduRESTAdapter(BaiduRESTConfig{
		Credentials: staticBCECredentials{credentials: BCECredentials{AccessKeyID: "bce-id", SecretAccessKey: "bce-key"}},
		Now:         func() time.Time { return time.Date(2026, 8, 3, 18, 0, 0, 0, time.UTC) },
		HTTP: doerFunc(func(request *http.Request) (*http.Response, error) {
			requests++
			switch requests {
			case 1:
				if request.Method != http.MethodGet || request.URL.String() != "https://ccr.baidubce.com/v1/ccr/user" {
					t.Fatalf("user request=%s %s", request.Method, request.URL)
				}
				return baiduCCRHTTPResponse(http.StatusOK, `{"username":"personal-user"}`), nil
			case 2:
				if request.Method != http.MethodPost || request.URL.String() != "https://ccr.baidubce.com/v1/ccr/token" {
					t.Fatalf("token request=%s %s", request.Method, request.URL)
				}
				return baiduCCRHTTPResponse(http.StatusOK, `{"beginTime":"2026-08-03 18:00:00","expireTime":"2026-08-03 19:00:00","token":"personal-temporary"}`), nil
			case 3:
				response := baiduCCRHTTPResponse(http.StatusUnauthorized, `{}`)
				response.Header.Set("Www-Authenticate", `Bearer realm="https://registry.baidubce.com/service/token",service="harbor-registry"`)
				return response, nil
			case 4:
				return baiduCCRHTTPResponse(http.StatusOK, `{"access_token":"personal-bearer"}`), nil
			case 5:
				return baiduCCRHTTPResponse(http.StatusOK, `{"name":"team/app","tags":[]}`), nil
			default:
				t.Fatalf("unexpected request %d", requests)
				return nil, context.Canceled
			}
		}),
	})
	result, err := adapter.Invoke(t.Context(), Invocation{Provider: ProviderBaidu, Mode: ModeRead, AuthScheme: authSchemeBaiduCCR, Service: "ccr", Operation: "ListTags", Method: http.MethodGet, URL: "https://registry.baidubce.com/v2/team/app/tags/list"})
	if err != nil || requests != 5 || string(result.Output) != `{"name":"team/app","tags":[]}` {
		t.Fatalf("personal result=%#v err=%v requests=%d", result, err, requests)
	}
}

func TestBaiduCCRRejectsRegistryRedirectWithoutLeakingCredentials(t *testing.T) {
	requests := 0
	adapter := NewBaiduRESTAdapter(BaiduRESTConfig{
		Credentials: staticBCECredentials{credentials: BCECredentials{AccessKeyID: "bce-id", SecretAccessKey: "bce-secret", SessionToken: "bce-session"}},
		Now:         func() time.Time { return time.Date(2026, 8, 3, 18, 0, 0, 0, time.UTC) },
		HTTP: doerFunc(func(request *http.Request) (*http.Response, error) {
			requests++
			switch requests {
			case 1:
				return baiduCCRHTTPResponse(http.StatusOK, `{"username":"personal-user"}`), nil
			case 2:
				return baiduCCRHTTPResponse(http.StatusOK, `{"beginTime":"2026-08-03 18:00:00","expireTime":"2026-08-03 19:00:00","token":"personal-temporary"}`), nil
			case 3:
				response := baiduCCRHTTPResponse(http.StatusUnauthorized, `{}`)
				response.Header.Set("Www-Authenticate", `Bearer realm="https://registry.baidubce.com/service/token",service="harbor-registry"`)
				return response, nil
			case 4:
				return baiduCCRHTTPResponse(http.StatusOK, `{"token":"registry-bearer"}`), nil
			case 5:
				if request.Header.Get("Authorization") != "Bearer registry-bearer" {
					t.Fatalf("Registry Authorization=%q", request.Header.Get("Authorization"))
				}
				response := baiduCCRHTTPResponse(http.StatusTemporaryRedirect, `{}`)
				response.Header.Set("Location", "https://attacker.example/layer")
				return response, nil
			default:
				t.Fatalf("redirect was followed with request %d to %s", requests, request.URL)
				return nil, context.Canceled
			}
		}),
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderBaidu, Mode: ModeRead, AuthScheme: authSchemeBaiduCCR,
		Service: "ccr", Operation: "GetBlob", Method: http.MethodGet,
		URL: "https://registry.baidubce.com/v2/team/app/blobs/sha256:abcdef",
	})
	if err == nil || requests != 5 {
		t.Fatalf("redirect err=%v requests=%d", err, requests)
	}
	for _, secret := range []string{"bce-id", "bce-secret", "bce-session", "personal-user", "personal-temporary", "registry-bearer"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaked %q: %v", secret, err)
		}
	}
}

func TestBaiduCCRAcceptsOnlyDocumentedRegistryEndpoints(t *testing.T) {
	enterprise := Invocation{Provider: ProviderBaidu, Mode: ModeRead, AuthScheme: authSchemeBaiduCCR, Service: "ccr", Operation: "ListTags", Region: "bj", RegistryInstanceID: "ccr-example12", RegistryUserID: "iam-user-123", Method: http.MethodGet}
	for _, rawURL := range []string{
		"https://ccr-example12-pub.cnc.bj.baidubce.com/v2/team/app/tags/list",
		"https://ccr-example12-vpc.cnc.bj.baidubce.com/v2/team/app/tags/list",
		"https://ccr-example12-pub.cnc.bd.bj.baidubce.com/v2/team/app/tags/list",
		"https://ccr-example12-vpc.cnc.bd.bj.baidubce.com/v2/team/app/tags/list",
	} {
		invocation := enterprise
		invocation.URL = rawURL
		if err := validateBaiduCCRInvocation(invocation, nil); err != nil {
			t.Errorf("Enterprise endpoint %q rejected: %v", rawURL, err)
		}
	}
	personal := Invocation{Provider: ProviderBaidu, Mode: ModeRead, AuthScheme: authSchemeBaiduCCR, Service: "ccr", Operation: "ListTags", Method: http.MethodGet, URL: "https://registry.baidubce.com/v2/team/app/tags/list"}
	if err := validateBaiduCCRInvocation(personal, nil); err != nil {
		t.Fatalf("Personal endpoint rejected: %v", err)
	}
	custom := enterprise
	custom.URL = "https://registry.example.com/v2/team/app/tags/list"
	if err := validateBaiduCCRInvocation(custom, nil); err == nil {
		t.Fatal("caller-only custom domain accepted")
	}
	if err := validateBaiduCCRInvocation(custom, []string{"registry.example.com"}); err != nil {
		t.Fatalf("operator-pinned custom domain rejected: %v", err)
	}
}

func TestBaiduCCRRejectsUnsafeTargetsBeforeCredentials(t *testing.T) {
	base := Invocation{Provider: ProviderBaidu, Mode: ModeRead, AuthScheme: authSchemeBaiduCCR, Service: "ccr", Operation: "ListTags", Region: "bj", RegistryInstanceID: "ccr-example12", RegistryUserID: "iam-user-123", Method: http.MethodGet, URL: "https://ccr-example12-pub.cnc.bd.bj.baidubce.com/v2/team/app/tags/list"}
	tests := map[string]func(*Invocation){
		"lookalike": func(value *Invocation) {
			value.URL = "https://ccr-example12-pub.cnc.bd.bj.baidubce.com.attacker.example/v2/team/app/tags/list"
		},
		"nested": func(value *Invocation) {
			value.URL = "https://nested.ccr-example12-pub.cnc.bd.bj.baidubce.com/v2/team/app/tags/list"
		},
		"instance mismatch": func(value *Invocation) { value.RegistryInstanceID = "ccr-other1234" },
		"region mismatch":   func(value *Invocation) { value.Region = "gz" },
		"missing user":      func(value *Invocation) { value.RegistryUserID = "" },
		"caller auth":       func(value *Invocation) { value.Headers = map[string]string{"Authorization": "Bearer caller"} },
		"token path":        func(value *Invocation) { value.URL = "https://ccr-example12-pub.cnc.bd.bj.baidubce.com/service/token" },
		"custom port": func(value *Invocation) {
			value.URL = "https://ccr-example12-pub.cnc.bd.bj.baidubce.com:8443/v2/team/app/tags/list"
		},
		"personal ids": func(value *Invocation) {
			value.URL = "https://registry.baidubce.com/v2/team/app/tags/list"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			invocation := base
			mutate(&invocation)
			credentials := &countingBCECredentialProvider{}
			requests := 0
			adapter := NewBaiduRESTAdapter(BaiduRESTConfig{Credentials: credentials, HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
				requests++
				return nil, context.Canceled
			})})
			_, err := adapter.Invoke(t.Context(), invocation)
			if err == nil || credentials.calls != 0 || requests != 0 {
				t.Fatalf("unsafe target err=%v credential_calls=%d requests=%d", err, credentials.calls, requests)
			}
		})
	}
}

func TestBaiduCCRRejectsUnsafeBearerChallenges(t *testing.T) {
	target := baiduCCRTarget{RegistryHost: "ccr-example12-pub.cnc.bd.bj.baidubce.com"}
	challenges := []string{
		`Bearer realm="https://attacker.example/service/token",service="harbor-registry"`,
		`Bearer realm="https://ccr-example12-pub.cnc.bd.bj.baidubce.com/evil",service="harbor-registry"`,
		`Bearer realm="https://ccr-example12-pub.cnc.bd.bj.baidubce.com/service/token?next=evil",service="harbor-registry"`,
		`Bearer realm="https://ccr-example12-pub.cnc.bd.bj.baidubce.com/service/token",service="other"`,
		`Basic realm="registry"`,
	}
	for _, challenge := range challenges {
		adapter := NewBaiduRESTAdapter(BaiduRESTConfig{HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			response := baiduCCRHTTPResponse(http.StatusUnauthorized, `{}`)
			response.Header.Set("Www-Authenticate", challenge)
			return response, nil
		})})
		if _, err := adapter.getBaiduCCRBearerChallenge(t.Context(), target); err == nil {
			t.Errorf("unsafe challenge accepted: %s", challenge)
		}
	}
}

func TestBaiduCCRValidatesUploadAndMountPlans(t *testing.T) {
	base := Invocation{Provider: ProviderBaidu, Mode: ModeMutate, AuthScheme: authSchemeBaiduCCR, Service: "ccr", Operation: "UploadBlob", Region: "bj", RegistryInstanceID: "ccr-example12", RegistryUserID: "iam-user-123", URL: "https://ccr-example12-pub.cnc.bd.bj.baidubce.com/v2/team/app/blobs/uploads/"}
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
		if err := validateBaiduCCRInvocation(invocation, nil); err != nil {
			t.Errorf("valid upload rejected: %v", err)
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
		if err := validateBaiduCCRInvocation(invocation, nil); err == nil {
			t.Errorf("invalid upload accepted: %#v", invocation)
		}
	}
}

func TestBaiduCCRValidatesRegistryResourcePlans(t *testing.T) {
	base := Invocation{Provider: ProviderBaidu, Mode: ModeRead, AuthScheme: authSchemeBaiduCCR, Service: "ccr", Operation: "RegistryOperation", Method: http.MethodGet, URL: "https://registry.baidubce.com"}
	valid := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/v2/"},
		{http.MethodHead, "/v2/"},
		{http.MethodGet, "/v2/_catalog"},
		{http.MethodGet, "/v2/team/app/manifests/latest"},
		{http.MethodHead, "/v2/team/app/blobs/sha256:abcdef"},
		{http.MethodGet, "/v2/team/app/referrers/sha256:abcdef"},
		{http.MethodGet, "/v2/team/app/tags/list"},
		{http.MethodDelete, "/v2/team/app/manifests/sha256:abcdef"},
	}
	for _, test := range valid {
		invocation := base
		invocation.Method, invocation.URL = test.method, base.URL+test.path
		if err := validateBaiduCCRInvocation(invocation, nil); err != nil {
			t.Errorf("valid %s %s rejected: %v", test.method, test.path, err)
		}
	}
	invalid := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/v2/"},
		{http.MethodHead, "/v2/_catalog"},
		{http.MethodGet, "/v1/team/app/tags/list"},
		{http.MethodGet, "/v2/app/tags/list"},
		{http.MethodGet, "/v2/team/app/manifests/"},
		{http.MethodPost, "/v2/team/app/tags/list"},
		{http.MethodHead, "/v2/team/app/referrers/sha256:abcdef"},
		{http.MethodPost, "/v2/team/app/blobs/sha256:abcdef"},
	}
	for _, test := range invalid {
		invocation := base
		invocation.Method, invocation.URL = test.method, base.URL+test.path
		if err := validateBaiduCCRInvocation(invocation, nil); err == nil {
			t.Errorf("invalid %s %s accepted", test.method, test.path)
		}
	}
}

func TestBaiduCCRRejectsMalformedTemporaryCredentialsAndBearerTokens(t *testing.T) {
	for _, credential := range [][2]string{{"", "password"}, {"user:name", "password"}, {"user", "line\nbreak"}} {
		if _, err := validateBaiduCCRTemporaryCredential(credential[0], credential[1]); err == nil {
			t.Errorf("invalid temporary credential accepted: %#v", credential)
		}
	}
	adapter := NewBaiduRESTAdapter(BaiduRESTConfig{HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
		return baiduCCRHTTPResponse(http.StatusOK, `{"token":"line\nbreak"}`), nil
	})})
	_, err := adapter.getBaiduCCRBearerToken(t.Context(), baiduCCRTarget{}, baiduCCRTemporaryCredential{Username: "user", Password: "password"}, baiduCCRBearerChallenge{Realm: "https://registry.baidubce.com/service/token", Service: "harbor-registry"})
	if err == nil {
		t.Fatal("malformed Registry Bearer token accepted")
	}
}

func assertBaiduCCRControlAuthorization(t *testing.T, request *http.Request) {
	t.Helper()
	if !strings.HasPrefix(request.Header.Get("Authorization"), "bce-auth-v1/bce-id/2026-08-03T18:00:00Z/1800/") || request.Header.Get("x-bce-security-token") != "bce-session" {
		t.Fatalf("control headers=%v", request.Header)
	}
}

func baiduCCRHTTPResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func FuzzBaiduCCRInvocation(f *testing.F) {
	f.Add("https://ccr-example12-pub.cnc.bd.bj.baidubce.com/v2/team/app/tags/list")
	f.Add("https://registry.baidubce.com.attacker.example/v2/team/app/tags/list")
	f.Fuzz(func(t *testing.T, rawURL string) {
		_, _ = parseBaiduCCRInvocation(Invocation{Provider: ProviderBaidu, Mode: ModeRead, AuthScheme: authSchemeBaiduCCR, Service: "ccr", Operation: "ListTags", Region: "bj", RegistryInstanceID: "ccr-example12", RegistryUserID: "iam-user-123", Method: http.MethodGet, URL: rawURL}, nil)
	})
}

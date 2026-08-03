package cloud

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAlibabaACRUsesRAMTemporaryCredentialAndInternalBearerChallenge(t *testing.T) {
	credentials := &countingAlibabaCredentialsProvider{credentials: AlibabaCredentials{
		AccessKeyID: "aliyun-ak", AccessKeySecret: "aliyun-secret", SecurityToken: "ram-session",
	}}
	requests := 0
	adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{
		Credentials: credentials,
		Now:         func() time.Time { return time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC) },
		Nonce:       func() string { return "acr-rpc-nonce" },
		HTTP: doerFunc(func(request *http.Request) (*http.Response, error) {
			requests++
			switch requests {
			case 1:
				if request.Method != http.MethodGet || request.URL.Host != "cr.cn-hangzhou.aliyuncs.com" || request.URL.Path != "/" {
					t.Fatalf("token request=%s %s", request.Method, request.URL)
				}
				query := request.URL.Query()
				for name, want := range map[string]string{
					"Action": "GetAuthorizationToken", "Version": "2018-12-01", "InstanceId": "cri-example123",
					"AccessKeyId": "aliyun-ak", "SecurityToken": "ram-session", "SignatureNonce": "acr-rpc-nonce",
				} {
					if got := query.Get(name); got != want {
						t.Fatalf("token query %s=%q want=%q", name, got, want)
					}
				}
				if query.Get("Signature") == "" || strings.Contains(request.URL.RawQuery, "aliyun-secret") {
					t.Fatalf("unsafe or unsigned token request: %s", request.URL.RawQuery)
				}
				return alibabaACRHTTPResponse(http.StatusOK, `{"RequestId":"token-request-1","ExpireTime":1785736800000,"Code":"success","IsSuccess":true,"TempUsername":"temp_user_cr","AuthorizationToken":"private-registry-password"}`), nil
			case 2:
				if request.URL.String() != "https://demo-registry.cn-hangzhou.cr.aliyuncs.com/v2/" || request.Header.Get("Authorization") != "" {
					t.Fatalf("registry challenge request=%s auth=%q", request.URL, request.Header.Get("Authorization"))
				}
				response := alibabaACRHTTPResponse(http.StatusUnauthorized, `{"errors":[{"code":"UNAUTHORIZED"}]}`)
				response.Header.Set("Www-Authenticate", `Bearer realm="https://dockerauth.cn-hangzhou.aliyuncs.com/auth",service="registry.aliyuncs.com:cn-hangzhou:china:cri-example123:2a"`)
				return response, nil
			case 3:
				if request.URL.Host != "dockerauth.cn-hangzhou.aliyuncs.com" || request.URL.Path != "/auth" {
					t.Fatalf("auth request=%s", request.URL)
				}
				query := request.URL.Query()
				if query.Get("account") != "temp_user_cr" || query.Get("service") != "registry.aliyuncs.com:cn-hangzhou:china:cri-example123:2a" || query.Get("scope") != "repository:team/app:pull" {
					t.Fatalf("auth query=%s", request.URL.RawQuery)
				}
				wantBasic := "Basic " + base64.StdEncoding.EncodeToString([]byte("temp_user_cr:private-registry-password"))
				if request.Header.Get("Authorization") != wantBasic {
					t.Fatalf("auth header=%q want=%q", request.Header.Get("Authorization"), wantBasic)
				}
				return alibabaACRHTTPResponse(http.StatusOK, `{"token":"private-bearer-token","expires_in":3600}`), nil
			case 4:
				if request.URL.String() != "https://demo-registry.cn-hangzhou.cr.aliyuncs.com/v2/team/app/tags/list?n=1" || request.Header.Get("Authorization") != "Bearer private-bearer-token" {
					t.Fatalf("registry data request=%s auth=%q", request.URL, request.Header.Get("Authorization"))
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Request-Id": {"registry-request-1"}}, Body: io.NopCloser(strings.NewReader(`{"name":"team/app","tags":["latest"]}`))}, nil
			default:
				t.Fatalf("unexpected request %d: %s", requests, request.URL)
				return nil, context.Canceled
			}
		}),
	})

	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAlicloud, Mode: ModeRead, AuthScheme: authSchemeAlibabaACR,
		Service: "acr", Operation: "ListTags", Region: "cn-hangzhou", RegistryInstanceID: "cri-example123",
		Method: http.MethodGet, URL: "https://demo-registry.cn-hangzhou.cr.aliyuncs.com/v2/team/app/tags/list",
		Parameters: map[string]any{"n": 1},
	})
	if err != nil {
		t.Fatalf("invoke Alibaba ACR: %v", err)
	}
	if string(result.Output) != `{"name":"team/app","tags":["latest"]}` || result.RequestID != "registry-request-1" || requests != 4 || credentials.calls != 1 {
		t.Fatalf("result=%#v requests=%d credential_calls=%d", result, requests, credentials.calls)
	}
	for _, secret := range []string{"aliyun-secret", "ram-session", "private-registry-password", "private-bearer-token"} {
		if strings.Contains(string(result.Output), secret) {
			t.Fatalf("result leaked %q", secret)
		}
	}
}

func TestAlibabaACRAcceptsOnlyExactEnterpriseRegistryEndpoints(t *testing.T) {
	for _, rawURL := range []string{
		"https://demo-registry.cn-hangzhou.cr.aliyuncs.com/v2/team/app/manifests/latest",
		"https://demo-registry-vpc.cn-hangzhou.cr.aliyuncs.com/v2/team/app/blobs/sha256:abc",
	} {
		invocation := Invocation{Provider: ProviderAlicloud, Mode: ModeRead, AuthScheme: authSchemeAlibabaACR, Service: "acr", Operation: "RegistryRead", Region: "cn-hangzhou", RegistryInstanceID: "cri-example123", Method: http.MethodGet, URL: rawURL}
		if err := validateInvocation(invocation, nil); err != nil {
			t.Fatalf("official endpoint %q rejected: %v", rawURL, err)
		}
	}
}

func TestAlibabaACRAcceptsOnlyOperatorPinnedCustomRegistryDomain(t *testing.T) {
	invocation := Invocation{Provider: ProviderAlicloud, Mode: ModeRead, AuthScheme: authSchemeAlibabaACR, Service: "acr", Operation: "ListTags", Region: "cn-hangzhou", RegistryInstanceID: "cri-example123", Method: http.MethodGet, URL: "https://registry.example.com/v2/team/app/tags/list"}
	if err := validateInvocationWithEndpointHosts(invocation, nil, nil); err == nil {
		t.Fatal("caller-only custom ACR domain accepted")
	}
	if err := validateInvocationWithEndpointHosts(invocation, nil, []string{"registry.example.com"}); err != nil {
		t.Fatalf("operator-pinned custom ACR domain rejected: %v", err)
	}
}

func TestAlibabaACRRejectsUnsafeTargetsBeforeCredentialResolution(t *testing.T) {
	base := Invocation{Provider: ProviderAlicloud, Mode: ModeRead, AuthScheme: authSchemeAlibabaACR, Service: "acr", Operation: "RegistryRead", Region: "cn-hangzhou", RegistryInstanceID: "cri-example123", Method: http.MethodGet, URL: "https://demo-registry.cn-hangzhou.cr.aliyuncs.com/v2/team/app/tags/list"}
	tests := map[string]func(*Invocation){
		"personal edition": func(value *Invocation) {
			value.URL = "https://crpi-example.cn-hangzhou.personal.cr.aliyuncs.com/v2/team/app/tags/list"
		},
		"shared personal": func(value *Invocation) { value.URL = "https://registry.cn-hangzhou.aliyuncs.com/v2/team/app/tags/list" },
		"lookalike":       func(value *Invocation) { value.URL += ".attacker.example" },
		"nested": func(value *Invocation) {
			value.URL = "https://nested.demo-registry.cn-hangzhou.cr.aliyuncs.com/v2/team/app/tags/list"
		},
		"wrong region":         func(value *Invocation) { value.Region = "cn-shanghai" },
		"missing instance id":  func(value *Invocation) { value.RegistryInstanceID = "" },
		"bad instance id":      func(value *Invocation) { value.RegistryInstanceID = "cri-example123/steal" },
		"caller authorization": func(value *Invocation) { value.Headers = map[string]string{"Authorization": "Basic caller"} },
		"token endpoint":       func(value *Invocation) { value.URL = "https://demo-registry.cn-hangzhou.cr.aliyuncs.com/v2/token" },
		"escaped path": func(value *Invocation) {
			value.URL = "https://demo-registry.cn-hangzhou.cr.aliyuncs.com/v2/team%2Fapp/tags/list"
		},
		"custom port": func(value *Invocation) {
			value.URL = "https://demo-registry.cn-hangzhou.cr.aliyuncs.com:8443/v2/team/app/tags/list"
		},
		"wrong tags method": func(value *Invocation) { value.Method = http.MethodDelete; value.Mode = ModeMutate },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			invocation := base
			mutate(&invocation)
			credentials := &countingAlibabaCredentialsProvider{credentials: AlibabaCredentials{AccessKeyID: "must-not-resolve", AccessKeySecret: "must-not-resolve"}}
			requests := 0
			adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{Credentials: credentials, HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
				requests++
				return nil, context.Canceled
			})})
			err := validateInvocation(invocation, nil)
			if err == nil {
				_, err = adapter.Invoke(t.Context(), invocation)
			}
			if err == nil {
				t.Fatal("unsafe Alibaba ACR invocation accepted")
			}
			if credentials.calls != 0 || requests != 0 {
				t.Fatalf("unsafe target reached credential/network: calls=%d requests=%d", credentials.calls, requests)
			}
		})
	}
}

func TestAlibabaACRRejectsUnsafeBearerChallengeWithoutLeakingCapabilities(t *testing.T) {
	for name, challenge := range map[string]string{
		"lookalike realm": `Bearer realm="https://dockerauth.cn-hangzhou.aliyuncs.com.attacker.example/auth",service="registry.aliyuncs.com:cn-hangzhou:china:cri-example123:2a"`,
		"wrong region":    `Bearer realm="https://dockerauth.cn-shanghai.aliyuncs.com/auth",service="registry.aliyuncs.com:cn-hangzhou:china:cri-example123:2a"`,
		"wrong instance":  `Bearer realm="https://dockerauth.cn-hangzhou.aliyuncs.com/auth",service="registry.aliyuncs.com:cn-hangzhou:china:cri-other:2a"`,
		"caller scope":    `Bearer realm="https://dockerauth.cn-hangzhou.aliyuncs.com/auth?scope=repository:admin:pull,push",service="registry.aliyuncs.com:cn-hangzhou:china:cri-example123:2a"`,
	} {
		t.Run(name, func(t *testing.T) {
			requests := 0
			adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{
				Credentials: staticAlibabaCredentialsProvider{credentials: AlibabaCredentials{AccessKeyID: "ak", AccessKeySecret: "secret"}},
				HTTP: doerFunc(func(request *http.Request) (*http.Response, error) {
					requests++
					if requests == 1 {
						return alibabaACRHTTPResponse(http.StatusOK, `{"Code":"success","IsSuccess":true,"TempUsername":"private-user","AuthorizationToken":"private-password"}`), nil
					}
					response := alibabaACRHTTPResponse(http.StatusUnauthorized, `{"message":"private-provider-body"}`)
					response.Header.Set("Www-Authenticate", challenge)
					return response, nil
				}),
			})
			_, err := adapter.Invoke(t.Context(), Invocation{Provider: ProviderAlicloud, Mode: ModeRead, AuthScheme: authSchemeAlibabaACR, Service: "acr", Operation: "ListTags", Region: "cn-hangzhou", RegistryInstanceID: "cri-example123", Method: http.MethodGet, URL: "https://demo-registry.cn-hangzhou.cr.aliyuncs.com/v2/team/app/tags/list"})
			if err == nil {
				t.Fatal("unsafe challenge accepted")
			}
			for _, secret := range []string{"private-user", "private-password", "private-provider-body", "attacker.example", "repository:admin"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("error leaked %q: %v", secret, err)
				}
			}
			if requests != 2 {
				t.Fatalf("unsafe challenge made %d requests", requests)
			}
		})
	}
}

func TestAlibabaACRAcceptsCurrentEnterpriseEditionBearerRealmFamilies(t *testing.T) {
	tests := []struct {
		name     string
		registry string
		realm    string
		vpc      bool
	}{
		{
			name:     "public ee",
			registry: "demo-registry.cn-hangzhou.cr.aliyuncs.com",
			realm:    "https://dockerauth-ee.cn-hangzhou.aliyuncs.com/auth",
		},
		{
			name:     "zhangjiakou public special form",
			registry: "demo-registry.cn-zhangjiakou.cr.aliyuncs.com",
			realm:    "https://dockerauth-cn-zhangjiakou.aliyuncs.com/auth",
		},
		{
			name:     "vpc ee",
			registry: "demo-registry-vpc.cn-hangzhou.cr.aliyuncs.com",
			realm:    "https://dockerauth-ee-vpc.cn-hangzhou.aliyuncs.com/auth",
			vpc:      true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
				response := alibabaACRHTTPResponse(http.StatusUnauthorized, `{}`)
				region := "cn-hangzhou"
				if strings.Contains(test.registry, "cn-zhangjiakou") {
					region = "cn-zhangjiakou"
				}
				response.Header.Set("Www-Authenticate", `Bearer realm="`+test.realm+`",service="registry.aliyuncs.com:`+region+`:china:cri-example123:2a"`)
				return response, nil
			})})
			region := "cn-hangzhou"
			if strings.Contains(test.registry, "cn-zhangjiakou") {
				region = "cn-zhangjiakou"
			}
			challenge, err := adapter.getAlibabaACRBearerChallenge(t.Context(), alibabaACRTarget{
				RegistryHost: test.registry,
				Region:       region,
				InstanceID:   "cri-example123",
				VPC:          test.vpc,
			})
			if err != nil {
				t.Fatalf("current Enterprise Edition realm rejected: %v", err)
			}
			if challenge.Realm != test.realm {
				t.Fatalf("realm=%q want=%q", challenge.Realm, test.realm)
			}
		})
	}
}

func TestAlibabaACRCrossRepositoryMountAddsExactSourcePullScope(t *testing.T) {
	target, err := parseAlibabaACRInvocation(Invocation{
		Provider: ProviderAlicloud, Mode: ModeMutate, AuthScheme: authSchemeAlibabaACR,
		Service: "acr", Operation: "MountBlob", Region: "cn-hangzhou", RegistryInstanceID: "cri-example123",
		Method: http.MethodPost, URL: "https://demo-registry.cn-hangzhou.cr.aliyuncs.com/v2/team/target/blobs/uploads/",
		Parameters: map[string]any{"mount": "sha256:abcdef", "from": "team/source"},
	})
	if err != nil {
		t.Fatalf("parse mount: %v", err)
	}
	if strings.Join(target.Scopes, "|") != "repository:team/target:pull,push|repository:team/source:pull" {
		t.Fatalf("scopes=%v", target.Scopes)
	}
	for name, parameters := range map[string]map[string]any{
		"missing from": {"mount": "sha256:abcdef"},
		"bad digest":   {"mount": "not-a-digest", "from": "team/source"},
		"bad source":   {"mount": "sha256:abcdef", "from": "../source"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseAlibabaACRInvocation(Invocation{Provider: ProviderAlicloud, Mode: ModeMutate, AuthScheme: authSchemeAlibabaACR, Service: "acr", Operation: "MountBlob", Region: "cn-hangzhou", RegistryInstanceID: "cri-example123", Method: http.MethodPost, URL: "https://demo-registry.cn-hangzhou.cr.aliyuncs.com/v2/team/target/blobs/uploads/", Parameters: parameters})
			if err == nil {
				t.Fatal("unsafe mount accepted")
			}
		})
	}
}

func TestAlibabaACRDerivesCatalogAndUploadSessionScopes(t *testing.T) {
	tests := []struct {
		name   string
		method string
		url    string
		want   string
	}{
		{
			name:   "catalog",
			method: http.MethodGet,
			url:    "https://demo-registry.cn-hangzhou.cr.aliyuncs.com/v2/_catalog",
			want:   "registry:catalog:*",
		},
		{
			name:   "upload status",
			method: http.MethodGet,
			url:    "https://demo-registry.cn-hangzhou.cr.aliyuncs.com/v2/team/app/blobs/uploads/session-id",
			want:   "repository:team/app:pull,push",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target, err := parseAlibabaACRInvocation(Invocation{
				Provider: ProviderAlicloud, Mode: ModeRead, AuthScheme: authSchemeAlibabaACR,
				Service: "acr", Operation: "RegistryRead", Region: "cn-hangzhou", RegistryInstanceID: "cri-example123",
				Method: test.method, URL: test.url,
			})
			if err != nil {
				t.Fatalf("parse Registry request: %v", err)
			}
			if got := strings.Join(target.Scopes, "|"); got != test.want {
				t.Fatalf("scopes=%q want=%q", got, test.want)
			}
		})
	}
}

func TestAlibabaACRFollowsOnlySameRegionOSSBlobRedirectWithoutAuthorization(t *testing.T) {
	requests := 0
	targetFile := filepath.Join(t.TempDir(), "layer.bin")
	adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{
		Credentials: staticAlibabaCredentialsProvider{credentials: AlibabaCredentials{AccessKeyID: "ak", AccessKeySecret: "secret"}},
		HTTP: doerFunc(func(request *http.Request) (*http.Response, error) {
			requests++
			switch requests {
			case 1:
				return alibabaACRHTTPResponse(http.StatusOK, `{"Code":"success","IsSuccess":true,"TempUsername":"temp-user","AuthorizationToken":"temp-password"}`), nil
			case 2:
				response := alibabaACRHTTPResponse(http.StatusUnauthorized, `{}`)
				response.Header.Set("Www-Authenticate", `Bearer realm="https://dockerauth.cn-hangzhou.aliyuncs.com/auth",service="registry.aliyuncs.com:cn-hangzhou:china:cri-example123:2a"`)
				return response, nil
			case 3:
				return alibabaACRHTTPResponse(http.StatusOK, `{"token":"registry-bearer"}`), nil
			case 4:
				if request.Header.Get("Authorization") != "Bearer registry-bearer" || request.Header.Get("Range") != "bytes=0-3" {
					t.Fatalf("registry headers=%v", request.Header)
				}
				response := alibabaACRHTTPResponse(http.StatusTemporaryRedirect, "")
				response.Header.Set("Location", "https://acr-layer-bucket.oss-cn-hangzhou.aliyuncs.com/object?Expires=1&Signature=private-signed-capability")
				return response, nil
			case 5:
				if request.URL.Host != "acr-layer-bucket.oss-cn-hangzhou.aliyuncs.com" || request.Header.Get("Authorization") != "" || request.Header.Get("Range") != "bytes=0-3" {
					t.Fatalf("redirect request=%s headers=%v", request.URL, request.Header)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/octet-stream"}}, Body: io.NopCloser(strings.NewReader("layer"))}, nil
			default:
				t.Fatalf("unexpected request %d", requests)
				return nil, context.Canceled
			}
		}),
	})
	result, err := adapter.Invoke(t.Context(), Invocation{Provider: ProviderAlicloud, Mode: ModeRead, AuthScheme: authSchemeAlibabaACR, Service: "acr", Operation: "GetBlob", Region: "cn-hangzhou", RegistryInstanceID: "cri-example123", Method: http.MethodGet, URL: "https://demo-registry.cn-hangzhou.cr.aliyuncs.com/v2/team/app/blobs/sha256:abcdef", Headers: map[string]string{"Range": "bytes=0-3"}, ResponseFile: targetFile, MaxResponseFileBytes: 1024})
	if err != nil {
		t.Fatalf("invoke redirected blob: %v", err)
	}
	data, readErr := os.ReadFile(targetFile)
	if readErr != nil || string(data) != "layer" || !strings.Contains(string(result.Output), `"bytes":5`) {
		t.Fatalf("data=%q result=%s read_err=%v", data, result.Output, readErr)
	}
	if strings.Contains(string(result.Output), "private-signed-capability") {
		t.Fatal("signed redirect capability leaked")
	}
}

func TestAlibabaACRTransportErrorsCannotEchoInternalCredentials(t *testing.T) {
	requests := 0
	secrets := []string{"aliyun-secret", "ram-session", "temporary-password", "registry-bearer"}
	adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{
		Credentials: staticAlibabaCredentialsProvider{credentials: AlibabaCredentials{AccessKeyID: "aliyun-ak", AccessKeySecret: secrets[0], SecurityToken: secrets[1]}},
		HTTP: doerFunc(func(request *http.Request) (*http.Response, error) {
			requests++
			switch requests {
			case 1:
				return alibabaACRHTTPResponse(http.StatusOK, `{"Code":"success","IsSuccess":true,"TempUsername":"temp-user","AuthorizationToken":"temporary-password"}`), nil
			case 2:
				response := alibabaACRHTTPResponse(http.StatusUnauthorized, `{}`)
				response.Header.Set("Www-Authenticate", `Bearer realm="https://dockerauth.cn-hangzhou.aliyuncs.com/auth",service="registry.aliyuncs.com:cn-hangzhou:china:cri-example123:2a"`)
				return response, nil
			case 3:
				return alibabaACRHTTPResponse(http.StatusOK, `{"token":"registry-bearer"}`), nil
			default:
				return nil, errors.New(strings.Join(secrets, " "))
			}
		}),
	})
	_, err := adapter.Invoke(t.Context(), Invocation{Provider: ProviderAlicloud, Mode: ModeRead, AuthScheme: authSchemeAlibabaACR, Service: "acr", Operation: "ListTags", Region: "cn-hangzhou", RegistryInstanceID: "cri-example123", Method: http.MethodGet, URL: "https://demo-registry.cn-hangzhou.cr.aliyuncs.com/v2/team/app/tags/list"})
	if err == nil {
		t.Fatal("transport error accepted")
	}
	for _, secret := range secrets {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaked %q: %v", secret, err)
		}
	}
}

func alibabaACRHTTPResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: &http.Request{URL: &url.URL{Scheme: "https", Host: "provider.example"}}}
}

func FuzzAlibabaACRInvocationAndBearerChallenge(f *testing.F) {
	f.Add("https://demo-registry.cn-hangzhou.cr.aliyuncs.com/v2/team/app/tags/list", `Bearer realm="https://dockerauth.cn-hangzhou.aliyuncs.com/auth",service="registry.aliyuncs.com:cn-hangzhou:china:cri-example123:2a"`)
	f.Add("https://demo-registry.cn-hangzhou.cr.aliyuncs.com.attacker.example/v2/team/app/tags/list", `Bearer realm="https://evil.example/auth",service="steal"`)
	f.Fuzz(func(t *testing.T, rawURL, challenge string) {
		_, _ = parseAlibabaACRInvocation(Invocation{Provider: ProviderAlicloud, Mode: ModeRead, AuthScheme: authSchemeAlibabaACR, Service: "acr", Operation: "ListTags", Region: "cn-hangzhou", RegistryInstanceID: "cri-example123", Method: http.MethodGet, URL: rawURL})
		_, _ = parseDockerBearerChallenge(challenge)
	})
}

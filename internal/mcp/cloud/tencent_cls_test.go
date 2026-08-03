package cloud

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type countingTencentCredentialProvider struct {
	calls int
}

func (provider *countingTencentCredentialProvider) Credentials(context.Context) (TencentCredentials, error) {
	provider.calls++
	return TencentCredentials{SecretID: "id", SecretKey: "key"}, nil
}

func TestTencentCLSSignerMatchesFixedVectorAndContainsSTS(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "https://ap-shanghai.cls.tencentcs.com/logset?logset_id=xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if err := signTencentCLS(request, TencentCredentials{SecretID: "test-id", SecretKey: "test-secret", Token: "sts-token"}, time.Unix(1578976553, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	if got := request.Header.Get("X-Cls-Token"); got != "sts-token" {
		t.Fatalf("temporary token=%q", got)
	}
	want := "q-sign-algorithm=sha1&q-ak=test-id&q-sign-time=1578976553;1578977453&q-key-time=1578976553;1578977453&q-header-list=content-type;host;x-cls-token&q-url-param-list=logset_id&q-signature=bbcb088c692cf6ecc146e5b23604e4cfe55cf573"
	if got := request.Header.Get("Authorization"); got != want {
		t.Fatalf("authorization=%q, want %q", got, want)
	}
}

func TestTencentCLSInvocationUsesExactEndpointsAndInternalCredentialHeaders(t *testing.T) {
	var captured *http.Request
	adapter := NewTencentRESTAdapter(TencentRESTConfig{
		Credentials: staticTencentCredentialsProvider{credentials: TencentCredentials{SecretID: "id", SecretKey: "key", Token: "cam-token"}},
		Now:         func() time.Time { return time.Unix(1578976553, 0).UTC() },
		HTTP: doerFunc(func(request *http.Request) (*http.Response, error) {
			captured = request.Clone(request.Context())
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"X-Cls-Requestid": []string{"cls-request-id"}},
				Body:       io.NopCloser(strings.NewReader(`{"logsets":[]}`)),
			}, nil
		}),
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderTencent, AuthScheme: "cls", Service: "cls", Operation: "GetLogset",
		Method: http.MethodGet, URL: "https://ap-shanghai.cls.tencentcs.com/logset", Parameters: map[string]any{"logset_id": "id"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if captured == nil || captured.Header.Get("X-Cls-Token") != "cam-token" || !strings.HasPrefix(captured.Header.Get("Authorization"), "q-sign-algorithm=sha1&q-ak=id&") {
		t.Fatalf("captured request=%#v", captured)
	}
	if result.RequestID != "cls-request-id" {
		t.Fatalf("result=%#v", result)
	}

	valid := []string{
		"https://ap-beijing.cls.tencentcs.com/logset",
		"https://ap-beijing.cls.tencentyun.com/logset",
	}
	for _, target := range valid {
		request := Invocation{Provider: ProviderTencent, AuthScheme: "cls", Service: "cls", Operation: "GetLogset", Method: http.MethodGet, URL: target}
		if err := validateInvocation(request, nil); err != nil {
			t.Errorf("official CLS endpoint %q rejected: %v", target, err)
		}
	}
	invalid := []Invocation{
		{Provider: ProviderTencent, AuthScheme: "cls", Service: "cos", Operation: "GetLogset", Method: http.MethodGet, URL: valid[0]},
		{Provider: ProviderTencent, AuthScheme: "cls", Service: "cls", Operation: "GetLogset", Method: http.MethodGet, URL: "https://nested.ap-beijing.cls.tencentcs.com/logset"},
		{Provider: ProviderTencent, AuthScheme: "cls", Service: "cls", Operation: "GetLogset", Method: http.MethodGet, URL: "https://ap-beijing.cls.tencentcs.com.evil.example/logset"},
		{Provider: ProviderTencent, AuthScheme: "cls", Service: "cls", Operation: "GetLogset", Method: http.MethodGet, URL: valid[0], Headers: map[string]string{"X-Cls-Token": "caller-token"}},
	}
	for _, request := range invalid {
		if err := validateInvocation(request, nil); err == nil {
			t.Fatalf("unsafe CLS request accepted: %#v", request)
		}
	}
}

func TestTencentCLSRejectsTargetBeforeCredentials(t *testing.T) {
	credentials := &countingTencentCredentialProvider{}
	adapter := NewTencentRESTAdapter(TencentRESTConfig{Credentials: credentials})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderTencent, AuthScheme: "cls", Service: "cls", Operation: "GetLogset",
		Method: http.MethodGet, URL: "https://nested.ap-beijing.cls.tencentcs.com/logset",
	})
	if err == nil || credentials.calls != 0 {
		t.Fatalf("err=%v credential calls=%d", err, credentials.calls)
	}
}

func FuzzTencentCLSTargetValidation(f *testing.F) {
	for _, raw := range []string{
		"https://ap-beijing.cls.tencentcs.com/logset",
		"https://ap-beijing.cls.tencentyun.com/logset",
		"https://nested.ap-beijing.cls.tencentcs.com/logset",
		"https://ap-beijing.cls.tencentcs.com.evil.example/logset",
		"not-a-url",
	} {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		target, err := url.Parse(raw)
		if err != nil {
			return
		}
		if validateTencentCLSTarget("cls", target) != nil {
			return
		}
		host := strings.ToLower(target.Hostname())
		validSuffix := strings.HasSuffix(host, ".cls.tencentcs.com") || strings.HasSuffix(host, ".cls.tencentyun.com")
		prefix := strings.TrimSuffix(strings.TrimSuffix(host, ".cls.tencentcs.com"), ".cls.tencentyun.com")
		if target.Scheme != "https" || !validSuffix || prefix == "" || strings.Contains(prefix, ".") {
			t.Fatalf("validator accepted unsafe target %q", raw)
		}
	})
}

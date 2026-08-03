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

func TestAWSECRPrivateUsesInternalIAMTokenAndBasicRegistryAuthentication(t *testing.T) {
	credentials := &countingAWSCredentialsProvider{credentials: AWSCredentials{
		AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "private-secret", SessionToken: "private-session",
	}}
	registryToken := base64.StdEncoding.EncodeToString([]byte("AWS:private-registry-password"))
	requests := 0
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: credentials,
		Now:         func() time.Time { return time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC) },
		HTTP: doerFunc(func(request *http.Request) (*http.Response, error) {
			requests++
			switch requests {
			case 1:
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Fatal(err)
				}
				if request.Method != http.MethodPost || request.URL.String() != "https://api.ecr.us-west-2.amazonaws.com/" || request.Header.Get("X-Amz-Target") != "AmazonEC2ContainerRegistry_V20150921.GetAuthorizationToken" || string(body) != `{"registryIds":["123456789012"]}` {
					t.Fatalf("token request=%s %s target=%q body=%s", request.Method, request.URL, request.Header.Get("X-Amz-Target"), body)
				}
				if authorization := request.Header.Get("Authorization"); !strings.HasPrefix(authorization, "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/") || strings.Contains(authorization, "private-secret") {
					t.Fatalf("token Authorization=%q", authorization)
				}
				return ecrHTTPResponse(http.StatusOK, `{"authorizationData":[{"authorizationToken":"`+registryToken+`","expiresAt":1785736800,"proxyEndpoint":"https://123456789012.dkr.ecr.us-west-2.amazonaws.com"}]}`), nil
			case 2:
				if request.Method != http.MethodGet || request.URL.String() != "https://123456789012.dkr.ecr.us-west-2.amazonaws.com/v2/team/app/tags/list?n=1" {
					t.Fatalf("data request=%s %s", request.Method, request.URL)
				}
				if got := request.Header.Get("Authorization"); got != "Basic "+registryToken {
					t.Fatalf("data Authorization=%q", got)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Amzn-Requestid": {"ecr-request-1"}}, Body: io.NopCloser(strings.NewReader(`{"name":"team/app","tags":["latest"]}`))}, nil
			default:
				t.Fatalf("unexpected request %d", requests)
				return nil, nil
			}
		}),
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, Mode: ModeRead, AuthScheme: authSchemeAWSECR, Service: "ecr", Operation: "ListTags",
		Region: "us-west-2", Method: http.MethodGet, URL: "https://123456789012.dkr.ecr.us-west-2.amazonaws.com/v2/team/app/tags/list",
		Parameters: map[string]any{"n": 1},
	})
	if err != nil {
		t.Fatalf("invoke private ECR: %v", err)
	}
	if string(result.Output) != `{"name":"team/app","tags":["latest"]}` || result.RequestID != "ecr-request-1" || requests != 2 || credentials.calls != 1 {
		t.Fatalf("result=%#v requests=%d credential_calls=%d", result, requests, credentials.calls)
	}
	for _, secret := range []string{"private-secret", "private-session", "private-registry-password", registryToken} {
		if strings.Contains(string(result.Output), secret) {
			t.Fatalf("result leaked %q", secret)
		}
	}
}

func TestAWSECRPublicUsesInternalIAMTokenAndBearerRegistryAuthentication(t *testing.T) {
	credentials := &countingAWSCredentialsProvider{credentials: AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "private-secret"}}
	registryToken := base64.StdEncoding.EncodeToString([]byte("AWS:private-public-password"))
	requests := 0
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: credentials,
		Now:         func() time.Time { return time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC) },
		HTTP: doerFunc(func(request *http.Request) (*http.Response, error) {
			requests++
			switch requests {
			case 1:
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Fatal(err)
				}
				if request.URL.String() != "https://api.ecr-public.us-east-1.amazonaws.com/" || request.Header.Get("X-Amz-Target") != "SpencerFrontendService.GetAuthorizationToken" || string(body) != `{}` {
					t.Fatalf("token request=%s target=%q body=%s", request.URL, request.Header.Get("X-Amz-Target"), body)
				}
				return ecrHTTPResponse(http.StatusOK, `{"authorizationData":{"authorizationToken":"`+registryToken+`","expiresAt":1785736800}}`), nil
			case 2:
				if got := request.Header.Get("Authorization"); got != "Bearer "+registryToken {
					t.Fatalf("data Authorization=%q", got)
				}
				return ecrHTTPResponse(http.StatusOK, `{"schemaVersion":2}`), nil
			default:
				t.Fatalf("unexpected request %d", requests)
				return nil, nil
			}
		}),
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, Mode: ModeRead, AuthScheme: authSchemeAWSECR, Service: "ecr-public", Operation: "GetManifest",
		Region: "us-east-1", Method: http.MethodGet, URL: "https://public.ecr.aws/v2/alias123/repository/manifests/latest",
	})
	if err != nil || string(result.Output) != `{"schemaVersion":2}` || requests != 2 || credentials.calls != 1 {
		t.Fatalf("result=%#v requests=%d credential_calls=%d err=%v", result, requests, credentials.calls, err)
	}
}

func TestAWSECRDerivesOnlyOfficialTokenEndpoints(t *testing.T) {
	tests := []struct {
		host     string
		endpoint string
		target   string
		service  string
		region   string
	}{
		{host: "123456789012.dkr.ecr.us-west-2.amazonaws.com", endpoint: "https://api.ecr.us-west-2.amazonaws.com/", target: "AmazonEC2ContainerRegistry_V20150921.GetAuthorizationToken", service: "ecr", region: "us-west-2"},
		{host: "123456789012.dkr.ecr-fips.us-gov-west-1.amazonaws.com", endpoint: "https://api.ecr-fips.us-gov-west-1.amazonaws.com/", target: "AmazonEC2ContainerRegistry_V20150921.GetAuthorizationToken", service: "ecr", region: "us-gov-west-1"},
		{host: "123456789012.dkr.ecr.cn-north-1.amazonaws.com.cn", endpoint: "https://api.ecr.cn-north-1.amazonaws.com.cn/", target: "AmazonEC2ContainerRegistry_V20150921.GetAuthorizationToken", service: "ecr", region: "cn-north-1"},
		{host: "123456789012.dkr-ecr.eu-west-1.on.aws", endpoint: "https://ecr.eu-west-1.api.aws/", target: "AmazonEC2ContainerRegistry_V20150921.GetAuthorizationToken", service: "ecr", region: "eu-west-1"},
		{host: "123456789012.dkr-ecr-fips.us-east-1.on.aws", endpoint: "https://ecr-fips.us-east-1.api.aws/", target: "AmazonEC2ContainerRegistry_V20150921.GetAuthorizationToken", service: "ecr", region: "us-east-1"},
		{host: "public.ecr.aws", endpoint: "https://api.ecr-public.us-east-1.amazonaws.com/", target: "SpencerFrontendService.GetAuthorizationToken", service: "ecr-public", region: "us-east-1"},
		{host: "ecr-public.aws.com", endpoint: "https://ecr-public.us-east-1.api.aws/", target: "SpencerFrontendService.GetAuthorizationToken", service: "ecr-public", region: "us-east-1"},
	}
	for _, test := range tests {
		t.Run(test.host, func(t *testing.T) {
			got, ok := parseAWSPrivateECRHost(test.host)
			if !ok {
				got, ok = parseAWSPublicECRHost(test.host)
			}
			if !ok || got.TokenEndpoint != test.endpoint || got.TokenTarget != test.target || got.TokenService != test.service || got.Region != test.region {
				t.Fatalf("target=%#v ok=%t", got, ok)
			}
		})
	}
}

func TestAWSECRRejectsUnsafeTargetsBeforeIdentity(t *testing.T) {
	tests := []struct {
		name       string
		service    string
		region     string
		method     string
		rawURL     string
		headers    map[string]string
		wantAccept bool
	}{
		{name: "private classic", service: "ecr", region: "us-west-2", method: http.MethodGet, rawURL: "https://123456789012.dkr.ecr.us-west-2.amazonaws.com/v2/team/app/tags/list", wantAccept: true},
		{name: "private fips", service: "ecr", region: "us-gov-west-1", method: http.MethodHead, rawURL: "https://123456789012.dkr.ecr-fips.us-gov-west-1.amazonaws.com/v2/team/app/blobs/sha256:abc", wantAccept: true},
		{name: "private dualstack", service: "ecr", region: "eu-west-1", method: http.MethodGet, rawURL: "https://123456789012.dkr-ecr.eu-west-1.on.aws/v2/team/app/manifests/latest", wantAccept: true},
		{name: "private dualstack fips", service: "ecr", region: "us-east-1", method: http.MethodGet, rawURL: "https://123456789012.dkr-ecr-fips.us-east-1.on.aws/v2/", wantAccept: true},
		{name: "private china", service: "ecr", region: "cn-north-1", method: http.MethodGet, rawURL: "https://123456789012.dkr.ecr.cn-north-1.amazonaws.com.cn/v2/team/app/manifests/latest", wantAccept: true},
		{name: "public classic", service: "ecr-public", region: "us-east-1", method: http.MethodGet, rawURL: "https://public.ecr.aws/v2/alias123/team/app/manifests/latest", wantAccept: true},
		{name: "public dualstack", service: "ecr-public", region: "us-east-1", method: http.MethodHead, rawURL: "https://ecr-public.aws.com/v2/alias123/team/app/blobs/sha256:abc", wantAccept: true},
		{name: "lookalike", service: "ecr", region: "us-west-2", method: http.MethodGet, rawURL: "https://123456789012.dkr.ecr.us-west-2.amazonaws.com.attacker.example/v2/team/app/tags/list"},
		{name: "nested private", service: "ecr", region: "us-west-2", method: http.MethodGet, rawURL: "https://nested.123456789012.dkr.ecr.us-west-2.amazonaws.com/v2/team/app/tags/list"},
		{name: "wrong account length", service: "ecr", region: "us-west-2", method: http.MethodGet, rawURL: "https://123.dkr.ecr.us-west-2.amazonaws.com/v2/team/app/tags/list"},
		{name: "wrong region", service: "ecr", region: "us-east-1", method: http.MethodGet, rawURL: "https://123456789012.dkr.ecr.us-west-2.amazonaws.com/v2/team/app/tags/list"},
		{name: "wrong private service", service: "ecr-public", region: "us-west-2", method: http.MethodGet, rawURL: "https://123456789012.dkr.ecr.us-west-2.amazonaws.com/v2/team/app/tags/list"},
		{name: "wrong public region", service: "ecr-public", region: "eu-west-1", method: http.MethodGet, rawURL: "https://public.ecr.aws/v2/alias123/team/app/manifests/latest"},
		{name: "public tags api", service: "ecr-public", region: "us-east-1", method: http.MethodGet, rawURL: "https://public.ecr.aws/v2/alias123/team/app/tags/list"},
		{name: "public catalog", service: "ecr-public", region: "us-east-1", method: http.MethodGet, rawURL: "https://public.ecr.aws/v2/_catalog"},
		{name: "public missing alias", service: "ecr-public", region: "us-east-1", method: http.MethodGet, rawURL: "https://public.ecr.aws/v2/repository/manifests/latest"},
		{name: "token api exposed", service: "ecr", region: "us-west-2", method: http.MethodPost, rawURL: "https://api.ecr.us-west-2.amazonaws.com/"},
		{name: "empty manifest reference", service: "ecr", region: "us-west-2", method: http.MethodGet, rawURL: "https://123456789012.dkr.ecr.us-west-2.amazonaws.com/v2/team/app/manifests/"},
		{name: "extra tag suffix", service: "ecr", region: "us-west-2", method: http.MethodGet, rawURL: "https://123456789012.dkr.ecr.us-west-2.amazonaws.com/v2/team/app/tags/list/extra"},
		{name: "wrong tags method", service: "ecr", region: "us-west-2", method: http.MethodDelete, rawURL: "https://123456789012.dkr.ecr.us-west-2.amazonaws.com/v2/team/app/tags/list"},
		{name: "escaped path", service: "ecr", region: "us-west-2", method: http.MethodGet, rawURL: "https://123456789012.dkr.ecr.us-west-2.amazonaws.com/v2/team%2Fapp/tags/list"},
		{name: "custom port", service: "ecr", region: "us-west-2", method: http.MethodGet, rawURL: "https://123456789012.dkr.ecr.us-west-2.amazonaws.com:8443/v2/team/app/tags/list"},
		{name: "caller authorization", service: "ecr", region: "us-west-2", method: http.MethodGet, rawURL: "https://123456789012.dkr.ecr.us-west-2.amazonaws.com/v2/team/app/tags/list", headers: map[string]string{"Authorization": "Basic caller"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			credentials := &countingAWSCredentialsProvider{credentials: AWSCredentials{AccessKeyID: "must-not-resolve", SecretAccessKey: "must-not-resolve"}}
			requests := 0
			adapter := NewAWSRESTAdapter(AWSRESTConfig{Credentials: credentials, HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
				requests++
				return nil, context.Canceled
			})})
			invocation := Invocation{Provider: ProviderAWS, Mode: ModeMutate, AuthScheme: authSchemeAWSECR, Service: test.service, Operation: "RegistryOperation", Region: test.region, Method: test.method, URL: test.rawURL, Headers: test.headers}
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
			if credentials.calls != 0 || requests != 0 {
				t.Fatalf("unsafe invocation reached identity/network: credential_calls=%d requests=%d", credentials.calls, requests)
			}
		})
	}
}

func TestAWSECRFollowsOnlyStarportLayerReadRedirectWithoutAuthorization(t *testing.T) {
	targetFile := filepath.Join(t.TempDir(), "layer.bin")
	credentials := &countingAWSCredentialsProvider{credentials: AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret"}}
	registryToken := base64.StdEncoding.EncodeToString([]byte("AWS:registry-password"))
	requests := 0
	adapter := NewAWSRESTAdapter(AWSRESTConfig{Credentials: credentials, HTTP: doerFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		switch requests {
		case 1:
			return ecrHTTPResponse(http.StatusOK, `{"authorizationData":[{"authorizationToken":"`+registryToken+`","proxyEndpoint":"https://123456789012.dkr.ecr.us-west-2.amazonaws.com"}]}`), nil
		case 2:
			return &http.Response{StatusCode: http.StatusTemporaryRedirect, Header: http.Header{"Location": {"https://prod-us-west-2-starport-layer-bucket.s3.us-west-2.amazonaws.com/object?X-Amz-Signature=internal"}}, Body: io.NopCloser(strings.NewReader("private redirect body"))}, nil
		case 3:
			if request.Header.Get("Authorization") != "" || request.URL.Host != "prod-us-west-2-starport-layer-bucket.s3.us-west-2.amazonaws.com" {
				t.Fatalf("redirect request host=%q Authorization=%q", request.URL.Host, request.Header.Get("Authorization"))
			}
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/octet-stream"}, "X-Amz-Request-Id": {"s3-request"}}, Body: io.NopCloser(strings.NewReader("layer-data"))}, nil
		default:
			t.Fatalf("unexpected request %d", requests)
			return nil, nil
		}
	})})
	result, err := adapter.Invoke(t.Context(), Invocation{Provider: ProviderAWS, Mode: ModeRead, AuthScheme: authSchemeAWSECR, Service: "ecr", Operation: "GetBlob", Region: "us-west-2", Method: http.MethodGet, URL: "https://123456789012.dkr.ecr.us-west-2.amazonaws.com/v2/team/app/blobs/sha256:abc", ResponseFile: targetFile, MaxResponseFileBytes: 1024})
	if err != nil {
		t.Fatalf("invoke redirected ECR read: %v", err)
	}
	data, readErr := os.ReadFile(targetFile)
	if readErr != nil || string(data) != "layer-data" || result.RequestID != "s3-request" || strings.Contains(string(result.Output), "internal") {
		t.Fatalf("data=%q result=%s request_id=%q err=%v", data, result.Output, result.RequestID, readErr)
	}
}

func TestAWSECRRejectsUnsafeRedirectsWithoutPublishingFile(t *testing.T) {
	for _, test := range []struct {
		name     string
		method   string
		path     string
		location string
	}{
		{name: "lookalike", method: http.MethodGet, path: "/blobs/sha256:abc", location: "https://prod-us-west-2-starport-layer-bucket.s3.us-west-2.amazonaws.com.attacker.example/object?X-Amz-Signature=secret"},
		{name: "wrong bucket", method: http.MethodGet, path: "/blobs/sha256:abc", location: "https://attacker-bucket.s3.us-west-2.amazonaws.com/object?X-Amz-Signature=secret"},
		{name: "wrong region", method: http.MethodGet, path: "/blobs/sha256:abc", location: "https://prod-us-east-1-starport-layer-bucket.s3.us-east-1.amazonaws.com/object?X-Amz-Signature=secret"},
		{name: "mutation", method: http.MethodDelete, path: "/blobs/sha256:abc", location: "https://prod-us-west-2-starport-layer-bucket.s3.us-west-2.amazonaws.com/object?X-Amz-Signature=secret"},
		{name: "manifest", method: http.MethodGet, path: "/manifests/latest", location: "https://prod-us-west-2-starport-layer-bucket.s3.us-west-2.amazonaws.com/object?X-Amz-Signature=secret"},
	} {
		t.Run(test.name, func(t *testing.T) {
			targetFile := filepath.Join(t.TempDir(), "layer.bin")
			registryToken := base64.StdEncoding.EncodeToString([]byte("AWS:registry-password"))
			requests := 0
			adapter := NewAWSRESTAdapter(AWSRESTConfig{Credentials: &countingAWSCredentialsProvider{credentials: AWSCredentials{AccessKeyID: "AKID", SecretAccessKey: "secret"}}, HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
				requests++
				if requests == 1 {
					return ecrHTTPResponse(http.StatusOK, `{"authorizationData":[{"authorizationToken":"`+registryToken+`","proxyEndpoint":"https://123456789012.dkr.ecr.us-west-2.amazonaws.com"}]}`), nil
				}
				return &http.Response{StatusCode: http.StatusTemporaryRedirect, Header: http.Header{"Location": {test.location}}, Body: io.NopCloser(strings.NewReader("private redirect body"))}, nil
			})})
			mode := ModeRead
			if test.method != http.MethodGet {
				mode = ModeMutate
			}
			_, err := adapter.Invoke(t.Context(), Invocation{Provider: ProviderAWS, Mode: mode, AuthScheme: authSchemeAWSECR, Service: "ecr", Operation: "RegistryOperation", Region: "us-west-2", Method: test.method, URL: "https://123456789012.dkr.ecr.us-west-2.amazonaws.com/v2/team/app" + test.path, ResponseFile: targetFile})
			if err == nil || strings.Contains(err.Error(), "X-Amz-Signature") || strings.Contains(err.Error(), "private redirect body") {
				t.Fatalf("error=%v", err)
			}
			if requests != 2 {
				t.Fatalf("unsafe redirect was followed: requests=%d", requests)
			}
			if _, statErr := os.Stat(targetFile); !os.IsNotExist(statErr) {
				t.Fatalf("response_file published: %v", statErr)
			}
		})
	}
}

func TestAWSECRAuthorizationFailuresNeverExposeProviderBody(t *testing.T) {
	registryToken := base64.StdEncoding.EncodeToString([]byte("AWS:registry-password"))
	responses := []*http.Response{
		ecrHTTPResponse(http.StatusUnauthorized, `{"message":"private-provider-diagnostic"}`),
		ecrHTTPResponse(http.StatusOK, `{"authorizationData":[{"authorizationToken":"`+strings.Repeat("x", maxAWSECRAuthBytes+1)+`"}]}`),
		ecrHTTPResponse(http.StatusOK, `{"unexpected":"private-provider-diagnostic"}`),
		ecrHTTPResponse(http.StatusOK, `{"authorizationData":[{"authorizationToken":"`+registryToken+`","proxyEndpoint":"https://123456789012.dkr.ecr.us-west-2.amazonaws.com.cn"}]}`),
	}
	for index, response := range responses {
		t.Run(string(rune('a'+index)), func(t *testing.T) {
			adapter := NewAWSRESTAdapter(AWSRESTConfig{Credentials: &countingAWSCredentialsProvider{credentials: AWSCredentials{AccessKeyID: "AKID", SecretAccessKey: "secret"}}, HTTP: doerFunc(func(*http.Request) (*http.Response, error) { return response, nil })})
			_, err := adapter.Invoke(t.Context(), Invocation{Provider: ProviderAWS, Mode: ModeRead, AuthScheme: authSchemeAWSECR, Service: "ecr", Operation: "ListTags", Region: "us-west-2", Method: http.MethodGet, URL: "https://123456789012.dkr.ecr.us-west-2.amazonaws.com/v2/team/app/tags/list"})
			if err == nil || strings.Contains(err.Error(), "private-provider-diagnostic") || strings.Contains(err.Error(), strings.Repeat("x", 128)) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestAWSECRProviderAndRedirectErrorsCannotEchoSecrets(t *testing.T) {
	registryToken := base64.StdEncoding.EncodeToString([]byte("AWS:registry-password"))
	for _, redirect := range []bool{false, true} {
		t.Run(map[bool]string{false: "registry", true: "redirect"}[redirect], func(t *testing.T) {
			requests := 0
			adapter := NewAWSRESTAdapter(AWSRESTConfig{Credentials: &countingAWSCredentialsProvider{credentials: AWSCredentials{AccessKeyID: "AKID", SecretAccessKey: "secret", SessionToken: "session"}}, HTTP: doerFunc(func(request *http.Request) (*http.Response, error) {
				requests++
				if requests == 1 {
					return ecrHTTPResponse(http.StatusOK, `{"authorizationData":[{"authorizationToken":"`+registryToken+`","proxyEndpoint":"https://123456789012.dkr.ecr.us-west-2.amazonaws.com"}]}`), nil
				}
				if redirect && requests == 2 {
					return &http.Response{StatusCode: http.StatusTemporaryRedirect, Header: http.Header{"Location": {"https://prod-us-west-2-starport-layer-bucket.s3.us-west-2.amazonaws.com/object?X-Amz-Signature=signed-secret"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
				}
				return nil, &url.Error{Op: "Get", URL: request.URL.String(), Err: errors.New("secret session registry-password " + registryToken)}
			})})
			_, err := adapter.Invoke(t.Context(), Invocation{Provider: ProviderAWS, Mode: ModeRead, AuthScheme: authSchemeAWSECR, Service: "ecr", Operation: "GetBlob", Region: "us-west-2", Method: http.MethodGet, URL: "https://123456789012.dkr.ecr.us-west-2.amazonaws.com/v2/team/app/blobs/sha256:abc"})
			if err == nil {
				t.Fatal("expected provider error")
			}
			for _, secret := range []string{"secret", "session", "registry-password", registryToken, "signed-secret"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("error leaked %q: %v", secret, err)
				}
			}
		})
	}
}

func FuzzAWSECRInvocationValidation(f *testing.F) {
	for _, seed := range []struct{ method, rawURL, service, region string }{
		{"GET", "https://123456789012.dkr.ecr.us-west-2.amazonaws.com/v2/team/app/tags/list", "ecr", "us-west-2"},
		{"GET", "https://public.ecr.aws/v2/alias123/team/app/manifests/latest", "ecr-public", "us-east-1"},
		{"GET", "https://123456789012.dkr.ecr.us-west-2.amazonaws.com.attacker.example/v2/team/app/tags/list", "ecr", "us-west-2"},
	} {
		f.Add(seed.method, seed.rawURL, seed.service, seed.region)
	}
	f.Fuzz(func(t *testing.T, method, rawURL, service, region string) {
		if len(method)+len(rawURL)+len(service)+len(region) > 4096 {
			t.Skip()
		}
		_ = validateAWSECRInvocation(Invocation{Provider: ProviderAWS, Mode: ModeMutate, AuthScheme: authSchemeAWSECR, Service: service, Operation: "FuzzOperation", Region: region, Method: method, URL: rawURL})
	})
}

type countingAWSCredentialsProvider struct {
	credentials AWSCredentials
	calls       int
}

func (provider *countingAWSCredentialsProvider) Credentials(_ context.Context) (AWSCredentials, error) {
	provider.calls++
	return provider.credentials, nil
}

func ecrHTTPResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/x-amz-json-1.1"}}, Body: io.NopCloser(strings.NewReader(body))}
}

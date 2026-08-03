package cloud

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	googleauth "cloud.google.com/go/auth"
)

func TestAzureStatusDiscoveryAndFailures(t *testing.T) {
	adapter := NewAzureRESTAdapter(AzureRESTConfig{})
	status, err := adapter.Status(t.Context())
	if err != nil || !status.Available || status.CredentialStatus != CredentialStatusUnverified || !strings.Contains(status.Message, "no Azure CLI") {
		t.Fatalf("status=%#v err=%v", status, err)
	}
	output, err := adapter.Discover(t.Context(), DiscoveryRequest{})
	if err != nil || !strings.Contains(string(output), "rest_api_reference") || !strings.Contains(string(output), "eventgrid_mqtt_auth") || !strings.Contains(string(output), "messaging_amqp") || !strings.Contains(string(output), "eventhubs_auth") {
		t.Fatalf("discover=%s err=%v", output, err)
	}
}

func TestAzureDefaultCredentialAlwaysHasManagedIdentityFallback(t *testing.T) {
	t.Setenv("AZURE_TENANT_ID", "tenant")
	t.Setenv("AZURE_CLIENT_ID", "client")
	t.Setenv("AZURE_CLIENT_SECRET", "secret")
	adapter := NewAzureRESTAdapter(AzureRESTConfig{})
	if adapter.config.Tokens == nil {
		t.Fatal("non-CLI Azure credential chain was not configured")
	}
}

func TestGCPStatusUsesADCWithoutCLIFallback(t *testing.T) {
	adapter := NewGCPRESTAdapter(GCPRESTConfig{HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})})
	status, err := adapter.Status(t.Context())
	if err != nil || !status.Available || !strings.Contains(status.Adapter, "ADC") || status.CredentialStatus != "unverified" || !strings.Contains(status.Message, "no gcloud") {
		t.Fatalf("status=%#v err=%v", status, err)
	}
	if _, ok := adapter.config.Tokens.(*gcpADCTokenProvider); !ok {
		t.Fatalf("provider=%T", adapter.config.Tokens)
	}
}

type fakeGoogleAuthSource struct {
	token string
	err   error
	calls int
}

func (source *fakeGoogleAuthSource) Token(context.Context) (*googleauth.Token, error) {
	source.calls++
	return &googleauth.Token{Value: source.token}, source.err
}

func TestGCPADCCredentialProvider(t *testing.T) {
	source := &fakeGoogleAuthSource{token: "adc-token"}
	detectCalls := 0
	adc := &gcpADCTokenProvider{detect: func(context.Context) (googleAuthTokenSource, error) {
		detectCalls++
		return source, nil
	}}
	for range 2 {
		token, err := adc.Token(t.Context())
		if err != nil || token != "adc-token" {
			t.Fatalf("token=%q err=%v", token, err)
		}
	}
	if detectCalls != 1 || source.calls != 2 {
		t.Fatalf("detect=%d token_calls=%d", detectCalls, source.calls)
	}
}

func TestRESTResponseFailuresAreBoundedAndRedacted(t *testing.T) {
	if _, err := readRESTResponse(nil, 10); err == nil {
		t.Fatal("nil response accepted")
	}
	response := &http.Response{StatusCode: 401, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"SecretAccessKey":"hidden"}`))}
	if _, err := readRESTResponse(response, 100); err == nil || strings.Contains(err.Error(), "hidden") {
		t.Fatalf("credential response err=%v", err)
	}
	response = &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("123456"))}
	if _, err := readRESTResponse(response, 5); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversize err=%v", err)
	}
}

func TestBCEEnvironmentCredentialAndStatus(t *testing.T) {
	t.Setenv("BCE_ACCESS_KEY_ID", "ak")
	t.Setenv("BCE_SECRET_ACCESS_KEY", "sk")
	t.Setenv("BCE_SESSION_TOKEN", "sts")
	credentials, err := (EnvBCECredentialProvider{}).Credentials(t.Context())
	if err != nil || credentials.AccessKeyID != "ak" || credentials.SessionToken != "sts" {
		t.Fatalf("credentials=%#v err=%v", credentials, err)
	}
	adapter := NewBaiduRESTAdapter(BaiduRESTConfig{})
	status, err := adapter.Status(t.Context())
	if err != nil || !status.Available || status.Version != bceAuthVersionV1+"+"+bceAuthVersionV2+"+ccr-registry" || status.CredentialStatus != "local-material-present" {
		t.Fatalf("status=%#v err=%v", status, err)
	}
	t.Setenv("BCE_ACCESS_KEY_ID", "")
	status, err = adapter.Status(t.Context())
	if err != nil || status.Available || !strings.Contains(status.Message, "credential unavailable") || status.CredentialStatus != "missing-local-material" {
		t.Fatalf("missing status=%#v err=%v", status, err)
	}
}

func TestHTTPQueryParameterConversions(t *testing.T) {
	tests := []struct {
		value any
		want  []string
	}{
		{true, []string{"true"}}, {float64(1.5), []string{"1.5"}}, {nil, []string{""}},
		{[]any{"a", 2.0}, []string{"a", "2"}},
	}
	for _, test := range tests {
		got, err := stringValues(test.value)
		if err != nil || strings.Join(got, "|") != strings.Join(test.want, "|") {
			t.Errorf("value=%#v got=%q err=%v", test.value, got, err)
		}
	}
	if _, err := stringValues(map[string]any{"nested": true}); err == nil {
		t.Fatal("nested HTTP query value accepted")
	}
}

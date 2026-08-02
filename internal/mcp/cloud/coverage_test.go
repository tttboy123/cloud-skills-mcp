package cloud

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	googleauth "cloud.google.com/go/auth"
)

func TestAzureStatusDiscoveryAndFailures(t *testing.T) {
	runner := &fakeProcessRunner{stdout: []byte(`{"azure-cli":"test"}`)}
	adapter := NewAzureRESTAdapter(AzureRESTConfig{Binary: "az-test", Runner: runner, Env: []string{}})
	status, err := adapter.Status(t.Context())
	if err != nil || !status.Available || !strings.Contains(status.Version, "azure-cli") {
		t.Fatalf("status=%#v err=%v", status, err)
	}
	runner.stdout = nil
	runner.stderr = []byte("rest help")
	output, err := adapter.Discover(t.Context(), DiscoveryRequest{})
	if err != nil || string(output) != "rest help" {
		t.Fatalf("discover=%s err=%v", output, err)
	}
	runner.err = errors.New("missing")
	status, err = adapter.Status(t.Context())
	if err != nil || status.Available || !strings.Contains(status.Message, "az-test") {
		t.Fatalf("failed status=%#v err=%v", status, err)
	}
	if _, err := adapter.Discover(t.Context(), DiscoveryRequest{}); err == nil {
		t.Fatal("expected discovery failure")
	}
}

func TestAzureIdentityActivationRequiresOfficialCredentialHints(t *testing.T) {
	tests := []struct {
		environment []string
		want        bool
	}{
		{[]string{"AZURE_TENANT_ID=t", "AZURE_CLIENT_ID=c", "AZURE_CLIENT_SECRET=s"}, true},
		{[]string{"AZURE_TENANT_ID=t", "AZURE_CLIENT_ID=c", "AZURE_FEDERATED_TOKEN_FILE=/token"}, true},
		{[]string{"IDENTITY_ENDPOINT=http://localhost"}, true},
		{[]string{"CLOUD_SKILLS_AZURE_USE_DEFAULT_CREDENTIAL=1"}, true},
		{[]string{"CLOUD_SKILLS_AZURE_USE_DEFAULT_CREDENTIAL=0"}, false},
		{[]string{"AZURE_CLIENT_ID=c"}, false},
		{nil, false},
	}
	for _, test := range tests {
		if got := azureDefaultCredentialRequested(test.environment); got != test.want {
			t.Errorf("environment=%v got=%v want=%v", test.environment, got, test.want)
		}
	}
	t.Setenv("AZURE_TENANT_ID", "tenant")
	t.Setenv("AZURE_CLIENT_ID", "client")
	t.Setenv("AZURE_CLIENT_SECRET", "secret")
	adapter := NewAzureRESTAdapter(AzureRESTConfig{})
	if adapter.config.Tokens == nil {
		t.Fatal("service-principal environment did not activate DefaultAzureCredential")
	}
}

func TestGCPStatusAndTokenFallback(t *testing.T) {
	runner := &sequenceRunner{results: []runnerResult{
		{stderr: []byte("ADC unavailable"), err: errors.New("exit")},
		{stdout: []byte("access-token\n")},
	}}
	adapter := NewGCPRESTAdapter(GCPRESTConfig{Binary: "gcloud-test", Runner: runner, HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})})
	status, err := adapter.Status(t.Context())
	if err != nil || !status.Available || !strings.Contains(status.Adapter, "ADC") || runner.calls != 0 {
		t.Fatalf("status=%#v err=%v", status, err)
	}
	chain := adapter.config.Tokens.(*tokenProviderChain)
	provider := chain.providers[1].(*gcloudTokenProvider)
	token, err := provider.Token(t.Context())
	if err != nil || token != "access-token" {
		t.Fatalf("token=%q err=%v", token, err)
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

func TestGCPADCAndFallbackCredentialChain(t *testing.T) {
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
	fallback := &staticTokenProvider{token: "fallback-token"}
	chain := &tokenProviderChain{providers: []TokenProvider{
		&staticTokenProvider{err: errors.New("ADC missing")}, fallback,
	}}
	token, err := chain.Token(t.Context())
	if err != nil || token != "fallback-token" || fallback.calls != 1 {
		t.Fatalf("token=%q fallback_calls=%d err=%v", token, fallback.calls, err)
	}
}

type runnerResult struct {
	stdout []byte
	stderr []byte
	err    error
}

type sequenceRunner struct {
	results []runnerResult
	calls   int
}

func (runner *sequenceRunner) Run(context.Context, string, []string, []string) ([]byte, []byte, error) {
	if runner.calls >= len(runner.results) {
		return nil, nil, errors.New("unexpected call")
	}
	result := runner.results[runner.calls]
	runner.calls++
	return result.stdout, result.stderr, result.err
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
	if err != nil || !status.Available || status.Version != bceAuthVersion {
		t.Fatalf("status=%#v err=%v", status, err)
	}
	t.Setenv("BCE_ACCESS_KEY_ID", "")
	status, err = adapter.Status(t.Context())
	if err != nil || status.Available || !strings.Contains(status.Message, "credential unavailable") {
		t.Fatalf("missing status=%#v err=%v", status, err)
	}
}

func TestCLIParameterConversionsAndProviderBinaries(t *testing.T) {
	tests := []struct {
		value any
		want  string
	}{
		{true, "true"}, {int64(7), "7"}, {float32(1.5), "1.5"}, {nil, "null"},
		{map[string]any{"a": 1}, `{"a":1}`},
	}
	for _, test := range tests {
		got, err := cliParameterValue(test.value)
		if err != nil || got != test.want {
			t.Errorf("value=%#v got=%q err=%v", test.value, got, err)
		}
	}
	if _, err := alicloudParameterArgs(map[string]any{"bad.name": 1}); err == nil {
		t.Fatal("invalid Alibaba parameter accepted")
	}
	for _, provider := range AllProviders() {
		if providerBinary(provider) == "" {
			t.Errorf("empty binary for %s", provider)
		}
	}
}

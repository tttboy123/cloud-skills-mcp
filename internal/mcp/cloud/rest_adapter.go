package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	googleauth "cloud.google.com/go/auth"
	googlecredentials "cloud.google.com/go/auth/credentials"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azurepolicy "github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/tttboy123/cloud-skills-mcp/internal/mcp/sdk"
)

const defaultRESTBodyLimit = 2 * 1024 * 1024

type AzureRESTConfig struct {
	Binary       string
	Runner       ProcessRunner
	TempDir      string
	Timeout      time.Duration
	Env          []string
	Tokens       AzureTokenProvider
	HTTP         HTTPDoer
	MaxBodyBytes int64
}

type AzureRESTAdapter struct {
	config AzureRESTConfig
}

func NewAzureRESTAdapter(config AzureRESTConfig) *AzureRESTAdapter {
	if config.Binary == "" {
		config.Binary = "az"
	}
	if config.Runner == nil {
		config.Runner = execProcessRunner{}
	}
	if config.Timeout <= 0 {
		config.Timeout = 60 * time.Second
	}
	if config.Env == nil {
		config.Env = os.Environ()
	}
	if config.Tokens == nil && azureDefaultCredentialRequested(config.Env) {
		config.Tokens = &azureDefaultTokenProvider{factory: newDefaultAzureCredential}
	}
	if config.HTTP == nil {
		config.HTTP = &http.Client{
			Timeout:       config.Timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	if config.MaxBodyBytes <= 0 {
		config.MaxBodyBytes = defaultRESTBodyLimit
	}
	return &AzureRESTAdapter{config: config}
}

func (adapter *AzureRESTAdapter) Status(ctx context.Context) (ProviderStatus, error) {
	if adapter.config.Tokens != nil {
		return ProviderStatus{
			Provider: ProviderAzure, Available: true, Adapter: "Azure REST + DefaultAzureCredential",
			Version: "azidentity", CredentialSource: credentialSource(ProviderAzure),
			Message: "credentials are resolved lazily through the Azure Identity chain",
		}, nil
	}
	stdout, stderr, err := adapter.run(ctx, []string{"version", "--output", "json"})
	status := ProviderStatus{Provider: ProviderAzure, Adapter: adapter.config.Binary + " rest", CredentialSource: credentialSource(ProviderAzure)}
	if err != nil {
		status.Message = sdk.RedactSecret(err.Error())
		return status, nil
	}
	status.Available = true
	status.Version = strings.TrimSpace(string(stdout))
	if status.Version == "" {
		status.Version = strings.TrimSpace(string(stderr))
	}
	return status, nil
}

func (adapter *AzureRESTAdapter) Discover(ctx context.Context, _ DiscoveryRequest) ([]byte, error) {
	if adapter.config.Tokens != nil {
		return json.Marshal(map[string]string{
			"rest_api_reference": "https://learn.microsoft.com/en-us/rest/api/azure/",
			"authentication":     "https://learn.microsoft.com/en-us/azure/developer/go/sdk/authentication/authentication-overview",
		})
	}
	stdout, stderr, err := adapter.run(ctx, []string{"rest", "--help"})
	if err != nil {
		return nil, err
	}
	if len(stdout) == 0 {
		return stderr, nil
	}
	return stdout, nil
}

func (adapter *AzureRESTAdapter) Invoke(ctx context.Context, request Invocation) (InvocationResult, error) {
	if adapter.config.Tokens != nil {
		scope, err := azureScopeForURL(request.URL)
		if err != nil {
			return InvocationResult{}, err
		}
		if scope != "" && len(request.Arguments) == 0 {
			return adapter.invokeHTTP(ctx, request, scope)
		}
	}
	return adapter.invokeCLI(ctx, request)
}

func (adapter *AzureRESTAdapter) invokeCLI(ctx context.Context, request Invocation) (InvocationResult, error) {
	args := []string{"rest", "--method", strings.ToLower(request.Method), "--url", request.URL}
	if request.Subscription != "" {
		args = append(args, "--subscription", request.Subscription)
	}
	headerNames := make([]string, 0, len(request.Headers))
	for name := range request.Headers {
		headerNames = append(headerNames, name)
	}
	sort.Strings(headerNames)
	for _, name := range headerNames {
		args = append(args, "--headers", name+"="+request.Headers[name])
	}
	cleanup := func() {}
	if request.Body != nil {
		path, err := writeJSONPayload(adapter.config.TempDir, request.Body)
		if err != nil {
			return InvocationResult{}, err
		}
		cleanup = func() { _ = os.Remove(path) }
		args = append(args, "--body", "@"+path)
	}
	defer cleanup()
	args = append(args, request.Arguments...)
	args = append(args, "--output", "json")
	stdout, _, err := adapter.run(ctx, args)
	if err != nil {
		return InvocationResult{}, err
	}
	return InvocationResult{Output: stdout, RequestID: requestIDFromGenericJSON(stdout)}, nil
}

func (adapter *AzureRESTAdapter) invokeHTTP(ctx context.Context, invocation Invocation, scope string) (InvocationResult, error) {
	token, err := adapter.config.Tokens.Token(ctx, scope)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("load Azure identity token: %w", err)
	}
	if strings.TrimSpace(token) == "" {
		return InvocationResult{}, fmt.Errorf("Azure identity returned an empty access token")
	}
	var body io.Reader
	if invocation.Body != nil {
		data, err := json.Marshal(invocation.Body)
		if err != nil {
			return InvocationResult{}, fmt.Errorf("encode Azure request body: %w", err)
		}
		body = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, strings.ToUpper(invocation.Method), invocation.URL, body)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("build Azure request: %w", err)
	}
	for name, value := range invocation.Headers {
		request.Header.Set(name, value)
	}
	if invocation.Body != nil && request.Header.Get("Content-Type") == "" {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := adapter.config.HTTP.Do(request)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("Azure REST API request: %w", err)
	}
	output, err := readRESTResponse(response, adapter.config.MaxBodyBytes)
	if err != nil {
		return InvocationResult{}, err
	}
	return InvocationResult{Output: output, RequestID: responseRequestID(response.Header)}, nil
}

type AzureTokenProvider interface {
	Token(context.Context, string) (string, error)
}

type azureTokenCredential interface {
	GetToken(context.Context, azurepolicy.TokenRequestOptions) (azcore.AccessToken, error)
}

type azureDefaultTokenProvider struct {
	factory    func() (azureTokenCredential, error)
	once       sync.Once
	credential azureTokenCredential
	err        error
}

func newDefaultAzureCredential() (azureTokenCredential, error) {
	return azidentity.NewDefaultAzureCredential(nil)
}

func (provider *azureDefaultTokenProvider) Token(ctx context.Context, scope string) (string, error) {
	provider.once.Do(func() {
		provider.credential, provider.err = provider.factory()
	})
	if provider.err != nil {
		return "", fmt.Errorf("create Azure DefaultAzureCredential: %w", provider.err)
	}
	accessToken, err := provider.credential.GetToken(ctx, azurepolicy.TokenRequestOptions{Scopes: []string{scope}})
	if err != nil {
		return "", fmt.Errorf("Azure DefaultAzureCredential token: %w", err)
	}
	return accessToken.Token, nil
}

func azureDefaultCredentialRequested(environment []string) bool {
	values := make(map[string]string)
	for _, entry := range environment {
		name, value, found := strings.Cut(entry, "=")
		if found && strings.TrimSpace(value) != "" {
			values[name] = strings.TrimSpace(value)
		}
	}
	has := func(name string) bool { return values[name] != "" }
	servicePrincipal := has("AZURE_TENANT_ID") && has("AZURE_CLIENT_ID") &&
		(has("AZURE_CLIENT_SECRET") || has("AZURE_CLIENT_CERTIFICATE_PATH"))
	workloadIdentity := has("AZURE_TENANT_ID") && has("AZURE_CLIENT_ID") && has("AZURE_FEDERATED_TOKEN_FILE")
	managedIdentity := has("IDENTITY_ENDPOINT") || has("MSI_ENDPOINT") || has("IMDS_ENDPOINT")
	return servicePrincipal || workloadIdentity || managedIdentity || values["CLOUD_SKILLS_AZURE_USE_DEFAULT_CREDENTIAL"] == "1"
}

func azureScopeForURL(rawURL string) (string, error) {
	if err := validateRESTTarget(ProviderAzure, http.MethodGet, rawURL); err != nil {
		return "", err
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse Azure REST URL: %w", err)
	}
	host := strings.ToLower(parsed.Hostname())
	switch {
	case host == "management.azure.com":
		return "https://management.azure.com/.default", nil
	case host == "graph.microsoft.com":
		return "https://graph.microsoft.com/.default", nil
	case hasAnySuffix(host, ".blob.core.windows.net", ".dfs.core.windows.net", ".queue.core.windows.net", ".table.core.windows.net"):
		return "https://storage.azure.com/.default", nil
	case host == "vault.azure.net" || strings.HasSuffix(host, ".vault.azure.net"):
		return "https://vault.azure.net/.default", nil
	case host == "database.windows.net" || strings.HasSuffix(host, ".database.windows.net"):
		return "https://database.windows.net/.default", nil
	case host == "servicebus.windows.net" || strings.HasSuffix(host, ".servicebus.windows.net"):
		return "https://servicebus.azure.net/.default", nil
	case host == "monitor.azure.com" || strings.HasSuffix(host, ".monitor.azure.com"):
		return "https://monitor.azure.com/.default", nil
	case strings.HasSuffix(host, ".cognitiveservices.azure.com") || strings.HasSuffix(host, ".openai.azure.com"):
		return "https://cognitiveservices.azure.com/.default", nil
	default:
		return "", nil
	}
}

func (adapter *AzureRESTAdapter) run(ctx context.Context, args []string) ([]byte, []byte, error) {
	runContext, cancel := context.WithTimeout(ctx, adapter.config.Timeout)
	defer cancel()
	stdout, stderr, err := adapter.config.Runner.Run(runContext, adapter.config.Binary, args, adapter.config.Env)
	if err == nil {
		return stdout, stderr, nil
	}
	var cliError *sdk.CLIError
	if errors.As(err, &cliError) {
		return stdout, stderr, err
	}
	return stdout, stderr, &sdk.CLIError{CLI: adapter.config.Binary, Args: args, Stdout: string(stdout), Stderr: string(stderr), Code: -1}
}

type TokenProvider interface {
	Token(context.Context) (string, error)
}

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type GCPRESTConfig struct {
	Tokens       TokenProvider
	HTTP         HTTPDoer
	MaxBodyBytes int64
	Binary       string
	Runner       ProcessRunner
	Timeout      time.Duration
	Env          []string
}

type GCPRESTAdapter struct {
	config GCPRESTConfig
}

func NewGCPRESTAdapter(config GCPRESTConfig) *GCPRESTAdapter {
	if config.Binary == "" {
		config.Binary = "gcloud"
	}
	if config.Runner == nil {
		config.Runner = execProcessRunner{}
	}
	if config.Timeout <= 0 {
		config.Timeout = 60 * time.Second
	}
	if config.Env == nil {
		config.Env = os.Environ()
	}
	if config.Tokens == nil {
		config.Tokens = &tokenProviderChain{providers: []TokenProvider{
			&gcpADCTokenProvider{detect: detectDefaultGoogleCredentials},
			&gcloudTokenProvider{binary: config.Binary, runner: config.Runner, timeout: config.Timeout, env: config.Env},
		}}
	}
	if config.HTTP == nil {
		config.HTTP = &http.Client{
			Timeout:       config.Timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	if config.MaxBodyBytes <= 0 {
		config.MaxBodyBytes = defaultRESTBodyLimit
	}
	return &GCPRESTAdapter{config: config}
}

func (adapter *GCPRESTAdapter) Status(context.Context) (ProviderStatus, error) {
	return ProviderStatus{
		Provider: ProviderGCP, Available: true, Adapter: "googleapis REST + ADC",
		Version: "google-auth/v0.22", CredentialSource: credentialSource(ProviderGCP),
		Message: "credentials are resolved lazily through ADC, then authenticated gcloud identity",
	}, nil
}

func (adapter *GCPRESTAdapter) Discover(ctx context.Context, request DiscoveryRequest) ([]byte, error) {
	url := "https://www.googleapis.com/discovery/v1/apis"
	if request.Service != "" {
		version := request.Operation
		if version == "" {
			version = "v1"
		}
		url += "/" + request.Service + "/" + version + "/rest"
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build Google API discovery request: %w", err)
	}
	response, err := adapter.config.HTTP.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("Google API discovery request: %w", err)
	}
	return readRESTResponse(response, adapter.config.MaxBodyBytes)
}

func (adapter *GCPRESTAdapter) Invoke(ctx context.Context, request Invocation) (InvocationResult, error) {
	token, err := adapter.config.Tokens.Token(ctx)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("load Google Cloud application credential: %w", err)
	}
	if strings.TrimSpace(token) == "" {
		return InvocationResult{}, fmt.Errorf("Google Cloud credential returned an empty access token")
	}
	var body io.Reader
	if request.Body != nil {
		data, err := json.Marshal(request.Body)
		if err != nil {
			return InvocationResult{}, fmt.Errorf("encode Google Cloud request body: %w", err)
		}
		body = bytes.NewReader(data)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, strings.ToUpper(request.Method), request.URL, body)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("build Google Cloud request: %w", err)
	}
	for name, value := range request.Headers {
		httpRequest.Header.Set(name, value)
	}
	if request.Body != nil && httpRequest.Header.Get("Content-Type") == "" {
		httpRequest.Header.Set("Content-Type", "application/json")
	}
	httpRequest.Header.Set("Authorization", "Bearer "+token)
	if request.Project != "" {
		httpRequest.Header.Set("X-Goog-User-Project", request.Project)
	}
	response, err := adapter.config.HTTP.Do(httpRequest)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("Google Cloud API request: %w", err)
	}
	output, err := readRESTResponse(response, adapter.config.MaxBodyBytes)
	if err != nil {
		return InvocationResult{}, err
	}
	return InvocationResult{Output: output, RequestID: responseRequestID(response.Header)}, nil
}

type gcloudTokenProvider struct {
	binary  string
	runner  ProcessRunner
	timeout time.Duration
	env     []string
}

type googleAuthTokenSource interface {
	Token(context.Context) (*googleauth.Token, error)
}

type gcpADCTokenProvider struct {
	detect func(context.Context) (googleAuthTokenSource, error)
	once   sync.Once
	source googleAuthTokenSource
	err    error
}

func detectDefaultGoogleCredentials(context.Context) (googleAuthTokenSource, error) {
	return googlecredentials.DetectDefault(&googlecredentials.DetectOptions{
		Scopes: []string{"https://www.googleapis.com/auth/cloud-platform"},
	})
}

func (provider *gcpADCTokenProvider) Token(ctx context.Context) (string, error) {
	provider.once.Do(func() {
		provider.source, provider.err = provider.detect(ctx)
	})
	if provider.err != nil {
		return "", fmt.Errorf("detect Google Cloud Application Default Credentials: %w", provider.err)
	}
	if provider.source == nil {
		return "", fmt.Errorf("Google Cloud Application Default Credentials returned no token provider")
	}
	token, err := provider.source.Token(ctx)
	if err != nil {
		return "", fmt.Errorf("load Google Cloud ADC access token: %w", err)
	}
	if token == nil || strings.TrimSpace(token.Value) == "" {
		return "", fmt.Errorf("Google Cloud ADC returned an empty access token")
	}
	return strings.TrimSpace(token.Value), nil
}

type tokenProviderChain struct {
	providers []TokenProvider
}

func (chain *tokenProviderChain) Token(ctx context.Context) (string, error) {
	errorsSeen := make([]error, 0, len(chain.providers))
	for _, provider := range chain.providers {
		if provider == nil {
			continue
		}
		token, err := provider.Token(ctx)
		if err == nil && strings.TrimSpace(token) != "" {
			return strings.TrimSpace(token), nil
		}
		if err != nil {
			errorsSeen = append(errorsSeen, err)
		}
	}
	if len(errorsSeen) == 0 {
		return "", fmt.Errorf("no Google Cloud credential provider is configured")
	}
	return "", fmt.Errorf("Google Cloud credential chain failed: %w", errors.Join(errorsSeen...))
}

func (provider *gcloudTokenProvider) Token(ctx context.Context) (string, error) {
	commands := [][]string{
		{"auth", "application-default", "print-access-token"},
		{"auth", "print-access-token"},
	}
	var lastError error
	for _, args := range commands {
		runContext, cancel := context.WithTimeout(ctx, provider.timeout)
		stdout, stderr, err := provider.runner.Run(runContext, provider.binary, args, provider.env)
		cancel()
		if err == nil && strings.TrimSpace(string(stdout)) != "" {
			return strings.TrimSpace(string(stdout)), nil
		}
		if err != nil {
			lastError = fmt.Errorf("%s", sdk.RedactSecret(string(stderr)))
		}
	}
	if lastError == nil {
		lastError = fmt.Errorf("no access token returned")
	}
	return "", lastError
}

func readRESTResponse(response *http.Response, maxBytes int64) ([]byte, error) {
	if response == nil || response.Body == nil {
		return nil, fmt.Errorf("provider returned an empty HTTP response")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read provider response: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("provider response exceeds %d bytes", maxBytes)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("provider returned HTTP %d: %s", response.StatusCode, sdk.RedactSecret(string(data)))
	}
	return data, nil
}

func responseRequestID(headers http.Header) string {
	for _, name := range []string{"X-Request-Id", "X-Ms-Request-Id", "X-Goog-Request-Id", "X-Cloud-Trace-Context", "X-Bce-Request-Id", "Request-Id"} {
		if value := headers.Get(name); value != "" {
			return value
		}
	}
	return ""
}

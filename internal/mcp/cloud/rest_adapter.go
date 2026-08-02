package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/tttboy123/cloud-skills-mcp/internal/mcp/sdk"
)

const defaultRESTBodyLimit = 2 * 1024 * 1024

type AzureRESTConfig struct {
	Binary  string
	Runner  ProcessRunner
	TempDir string
	Timeout time.Duration
	Env     []string
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
	return &AzureRESTAdapter{config: config}
}

func (adapter *AzureRESTAdapter) Status(ctx context.Context) (ProviderStatus, error) {
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
		config.Tokens = &gcloudTokenProvider{binary: config.Binary, runner: config.Runner, timeout: config.Timeout, env: config.Env}
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

func (adapter *GCPRESTAdapter) Status(ctx context.Context) (ProviderStatus, error) {
	status := ProviderStatus{Provider: ProviderGCP, Adapter: "googleapis REST", CredentialSource: credentialSource(ProviderGCP)}
	runContext, cancel := context.WithTimeout(ctx, adapter.config.Timeout)
	defer cancel()
	stdout, stderr, err := adapter.config.Runner.Run(runContext, adapter.config.Binary, []string{"version", "--format=json"}, adapter.config.Env)
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
	for _, name := range []string{"X-Request-Id", "X-Goog-Request-Id", "X-Cloud-Trace-Context", "X-Bce-Request-Id", "Request-Id"} {
		if value := headers.Get(name); value != "" {
			return value
		}
	}
	return ""
}

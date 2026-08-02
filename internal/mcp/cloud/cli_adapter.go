package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tttboy123/cloud-skills-mcp/internal/mcp/sdk"
)

const maxCLIStreamBytes = 2 * 1024 * 1024

type ProcessRunner interface {
	Run(context.Context, string, []string, []string) ([]byte, []byte, error)
}

type CLIAdapterConfig struct {
	Provider Provider
	Binary   string
	Runner   ProcessRunner
	TempDir  string
	Timeout  time.Duration
	Env      []string
}

type CLIAdapter struct {
	config CLIAdapterConfig
}

func NewCLIAdapter(config CLIAdapterConfig) *CLIAdapter {
	if config.Binary == "" {
		config.Binary = providerBinary(config.Provider)
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
	return &CLIAdapter{config: config}
}

func (adapter *CLIAdapter) Status(ctx context.Context) (ProviderStatus, error) {
	stdout, stderr, err := adapter.run(ctx, []string{"--version"})
	status := ProviderStatus{
		Provider:         adapter.config.Provider,
		Adapter:          adapter.config.Binary,
		CredentialSource: credentialSource(adapter.config.Provider),
	}
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

func (adapter *CLIAdapter) Discover(ctx context.Context, request DiscoveryRequest) ([]byte, error) {
	args := make([]string, 0, 3)
	if request.Service != "" {
		args = append(args, request.Service)
	}
	if request.Operation != "" {
		args = append(args, request.Operation)
	}
	if adapter.config.Provider == ProviderAlicloud {
		args = append(args, "--help")
	} else {
		args = append(args, "help")
	}
	stdout, stderr, err := adapter.run(ctx, args)
	if err != nil {
		return nil, err
	}
	if len(stdout) == 0 {
		return stderr, nil
	}
	return stdout, nil
}

func (adapter *CLIAdapter) Invoke(ctx context.Context, request Invocation) (InvocationResult, error) {
	args, cleanup, err := adapter.buildInvokeArgs(request)
	if err != nil {
		return InvocationResult{}, err
	}
	defer cleanup()
	stdout, _, err := adapter.run(ctx, args)
	if err != nil {
		return InvocationResult{}, err
	}
	return InvocationResult{Output: stdout, RequestID: requestIDFromGenericJSON(stdout)}, nil
}

func (adapter *CLIAdapter) buildInvokeArgs(request Invocation) ([]string, func(), error) {
	args := []string{request.Service, request.Operation}
	cleanup := func() {}
	if request.Region != "" {
		args = append(args, "--region", request.Region)
	}
	switch adapter.config.Provider {
	case ProviderAWS, ProviderTencent:
		if request.Parameters != nil {
			path, err := writeJSONPayload(adapter.config.TempDir, request.Parameters)
			if err != nil {
				return nil, cleanup, err
			}
			cleanup = func() { _ = os.Remove(path) }
			args = append(args, "--cli-input-json", "file://"+path)
		}
		args = append(args, request.Arguments...)
		args = append(args, "--output", "json")
		if adapter.config.Provider == ProviderAWS {
			args = append(args, "--no-cli-pager")
		}
	case ProviderAlicloud:
		parameterArgs, err := alicloudParameterArgs(request.Parameters)
		if err != nil {
			return nil, cleanup, err
		}
		args = append(args, parameterArgs...)
		args = append(args, request.Arguments...)
	default:
		return nil, cleanup, fmt.Errorf("provider %q is not a universal CLI adapter", adapter.config.Provider)
	}
	return args, cleanup, nil
}

func (adapter *CLIAdapter) run(ctx context.Context, args []string) ([]byte, []byte, error) {
	runContext, cancel := context.WithTimeout(ctx, adapter.config.Timeout)
	defer cancel()
	stdout, stderr, err := adapter.config.Runner.Run(runContext, adapter.config.Binary, args, adapter.config.Env)
	if err == nil {
		return stdout, stderr, nil
	}
	var cliError *sdk.CLIError
	if !errors.As(err, &cliError) {
		cliError = &sdk.CLIError{CLI: adapter.config.Binary, Args: args, Stdout: string(stdout), Stderr: string(stderr), Code: -1}
		return stdout, stderr, cliError
	}
	return stdout, stderr, err
}

type execProcessRunner struct{}

func (execProcessRunner) Run(ctx context.Context, binary string, args, env []string) ([]byte, []byte, error) {
	command := exec.CommandContext(ctx, binary, args...)
	command.Env = env
	stdout := newCappedBuffer(maxCLIStreamBytes)
	stderr := newCappedBuffer(maxCLIStreamBytes)
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if err == nil && !stdout.truncated && !stderr.truncated {
		return stdout.Bytes(), stderr.Bytes(), nil
	}
	code := -1
	if exitError, ok := err.(*exec.ExitError); ok {
		code = exitError.ExitCode()
	}
	if stdout.truncated || stderr.truncated {
		return stdout.Bytes(), stderr.Bytes(), &sdk.CLIError{
			CLI: binary, Args: args, Stdout: stdout.String(),
			Stderr: fmt.Sprintf("provider CLI output exceeded %d bytes per stream", maxCLIStreamBytes), Code: code,
		}
	}
	return stdout.Bytes(), stderr.Bytes(), &sdk.CLIError{
		CLI: binary, Args: args, Stdout: stdout.String(), Stderr: stderr.String(), Code: code,
	}
}

type cappedBuffer struct {
	data      []byte
	limit     int
	truncated bool
}

func newCappedBuffer(limit int) cappedBuffer {
	return cappedBuffer{data: make([]byte, 0, limit), limit: limit}
}

func (buffer *cappedBuffer) Write(data []byte) (int, error) {
	remaining := buffer.limit - len(buffer.data)
	if remaining > 0 {
		if remaining > len(data) {
			remaining = len(data)
		}
		buffer.data = append(buffer.data, data[:remaining]...)
	}
	if remaining < len(data) {
		buffer.truncated = true
	}
	return len(data), nil
}

func (buffer *cappedBuffer) Bytes() []byte  { return buffer.data }
func (buffer *cappedBuffer) String() string { return string(buffer.data) }

func writeJSONPayload(tempDir string, value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode provider input: %w", err)
	}
	file, err := os.CreateTemp(tempDir, "cloud-api-input-*.json")
	if err != nil {
		return "", fmt.Errorf("create provider input: %w", err)
	}
	path := file.Name()
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("secure provider input: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("write provider input: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close provider input: %w", err)
	}
	return path, nil
}

func alicloudParameterArgs(parameters map[string]any) ([]string, error) {
	keys := make([]string, 0, len(parameters))
	for key := range parameters {
		if !identifierPattern.MatchString(key) {
			return nil, fmt.Errorf("invalid Alibaba Cloud parameter name %q", key)
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	args := make([]string, 0, len(keys)*2)
	for _, key := range keys {
		value, err := cliParameterValue(parameters[key])
		if err != nil {
			return nil, fmt.Errorf("encode Alibaba Cloud parameter %q: %w", key, err)
		}
		args = append(args, "--"+key, value)
	}
	return args, nil
}

func cliParameterValue(value any) (string, error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	case bool:
		return strconv.FormatBool(typed), nil
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), nil
	case float32:
		return strconv.FormatFloat(float64(typed), 'f', -1, 32), nil
	case int:
		return strconv.Itoa(typed), nil
	case int64:
		return strconv.FormatInt(typed, 10), nil
	case nil:
		return "null", nil
	default:
		data, err := json.Marshal(value)
		return string(data), err
	}
}

func requestIDFromGenericJSON(data []byte) string {
	var value any
	if json.Unmarshal(data, &value) != nil {
		return ""
	}
	return findRequestID(value)
}

func findRequestID(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := strings.ToLower(strings.ReplaceAll(key, "-", ""))
			if normalized == "requestid" || normalized == "xamznrequestid" || normalized == "request_id" {
				if text, ok := child.(string); ok {
					return text
				}
			}
			if result := findRequestID(child); result != "" {
				return result
			}
		}
	case []any:
		for _, child := range typed {
			if result := findRequestID(child); result != "" {
				return result
			}
		}
	}
	return ""
}

func providerBinary(provider Provider) string {
	switch provider {
	case ProviderAWS:
		return "aws"
	case ProviderAlicloud:
		return "aliyun"
	case ProviderTencent:
		return "tccli"
	case ProviderAzure:
		return "az"
	case ProviderGCP:
		return "gcloud"
	case ProviderBaidu:
		return "bcecmd"
	default:
		return string(provider)
	}
}

func credentialSource(provider Provider) string {
	groups := map[Provider][]struct {
		name string
		vars []string
	}{
		ProviderAWS: {
			{name: "web-identity", vars: []string{"AWS_WEB_IDENTITY_TOKEN_FILE", "AWS_ROLE_ARN"}},
			{name: "environment-aksk", vars: []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"}},
			{name: "profile-or-sso", vars: []string{"AWS_PROFILE"}},
		},
		ProviderAzure: {
			{name: "service-principal-environment", vars: []string{"AZURE_TENANT_ID", "AZURE_CLIENT_ID", "AZURE_CLIENT_SECRET"}},
		},
		ProviderGCP: {
			{name: "adc-file", vars: []string{"GOOGLE_APPLICATION_CREDENTIALS"}},
		},
		ProviderAlicloud: {
			{name: "environment-aksk", vars: []string{"ALIBABACLOUD_ACCESS_KEY_ID", "ALIBABACLOUD_ACCESS_KEY_SECRET"}},
			{name: "profile", vars: []string{"ALIBABACLOUD_PROFILE"}},
		},
		ProviderTencent: {
			{name: "environment-aksk", vars: []string{"TENCENTCLOUD_SECRET_ID", "TENCENTCLOUD_SECRET_KEY"}},
			{name: "profile", vars: []string{"TCCLI_PROFILE"}},
		},
		ProviderBaidu: {
			{name: "environment-aksk", vars: []string{"BCE_ACCESS_KEY_ID", "BCE_SECRET_ACCESS_KEY"}},
		},
	}
	for _, group := range groups[provider] {
		complete := true
		for _, name := range group.vars {
			if os.Getenv(name) == "" {
				complete = false
				break
			}
		}
		if complete {
			return group.name
		}
	}
	return "official-provider-chain"
}

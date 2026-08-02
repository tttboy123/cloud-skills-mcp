package tencent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/tttboy123/cloud-skills-mcp/internal/mcp/sdk"
)

type TencentAPIError struct {
	Service   string
	Action    string
	Code      string
	Message   string
	RequestID string
	CLIError  error
}

func (e *TencentAPIError) Error() string {
	message := strings.TrimSpace(e.Message)
	if message == "" && e.CLIError != nil {
		message = e.CLIError.Error()
	}
	return fmt.Sprintf("%s.%s %s: %s (RequestId=%s)", e.Service, e.Action, e.Code, message, e.RequestID)
}

func (e *TencentAPIError) Unwrap() error { return e.CLIError }

type tencentEnvelope struct {
	Response struct {
		Error *struct {
			Code    string `json:"Code"`
			Message string `json:"Message"`
		} `json:"Error"`
		RequestID string `json:"RequestId"`
	} `json:"Response"`
}

func parseTencentAPIError(service, action string, data []byte, cliErr error) *TencentAPIError {
	var envelope tencentEnvelope
	if json.Unmarshal(data, &envelope) != nil || envelope.Response.Error == nil {
		return nil
	}
	return &TencentAPIError{
		Service:   service,
		Action:    action,
		Code:      envelope.Response.Error.Code,
		Message:   envelope.Response.Error.Message,
		RequestID: envelope.Response.RequestID,
		CLIError:  cliErr,
	}
}

func requestIDFromResponse(data []byte) string {
	var envelope tencentEnvelope
	if json.Unmarshal(data, &envelope) != nil {
		return ""
	}
	return envelope.Response.RequestID
}

func isRetryableTencentCode(code string) bool {
	return code == "InternalError" || code == "ServiceUnavailable" ||
		strings.HasPrefix(code, "InternalError.") ||
		strings.HasPrefix(code, "RequestLimitExceeded")
}

func runTencentAPI(ctx context.Context, runtime Runtime, creds *sdk.Creds, service, action string, args []string, readOnly bool) ([]byte, int, error) {
	attempts := 1
	if readOnly {
		attempts = runtime.ReadMaxAttempts
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		out, stderr, err := runTccliProcess(ctx, runtime, creds, args)
		if apiErr := parseTencentAPIError(service, action, firstNonEmptyJSON(out, stderr), err); apiErr != nil {
			lastErr = apiErr
		} else if err != nil {
			lastErr = err
		} else if !json.Valid(out) {
			lastErr = fmt.Errorf("%s.%s returned invalid JSON: %s", service, action, sdk.RedactSecret(strings.TrimSpace(string(out))))
		} else {
			return out, attempt, nil
		}

		var apiErr *TencentAPIError
		if !readOnly || !errors.As(lastErr, &apiErr) || !isRetryableTencentCode(apiErr.Code) || attempt == attempts {
			return nil, attempt, lastErr
		}
		delay := runtime.RetryBaseDelay * time.Duration(1<<(attempt-1))
		if err := runtime.Sleep(ctx, delay); err != nil {
			return nil, attempt, err
		}
	}
	return nil, attempts, lastErr
}

func firstNonEmptyJSON(stdout, stderr []byte) []byte {
	if json.Valid(bytes.TrimSpace(stdout)) {
		return bytes.TrimSpace(stdout)
	}
	if json.Valid(bytes.TrimSpace(stderr)) {
		return bytes.TrimSpace(stderr)
	}
	return stdout
}

func runTccli(ctx context.Context, runtime Runtime, creds *sdk.Creds, args []string) ([]byte, error) {
	out, stderr, err := runTccliProcess(ctx, runtime, creds, args)
	if err != nil {
		return nil, err
	}
	if len(stderr) > 0 && len(out) == 0 {
		return stderr, nil
	}
	return out, nil
}

func runTccliProcess(ctx context.Context, runtime Runtime, creds *sdk.Creds, args []string) ([]byte, []byte, error) {
	env := replaceEnv(os.Environ(), map[string]string{
		"TENCENTCLOUD_SECRET_ID":  creds.AccessKeyID,
		"TENCENTCLOUD_SECRET_KEY": creds.AccessKeySecret,
		"TENCENTCLOUD_TOKEN":      creds.SecurityToken,
		"TENCENTCLOUD_REGION":     creds.Region,
	})
	cctx, cancel := context.WithTimeout(ctx, runtime.CLITimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, runtime.CLIPath, args...)
	cmd.Env = env
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.Bytes(), stderr.Bytes(), nil
	}
	code := -1
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	}
	return stdout.Bytes(), stderr.Bytes(), &sdk.CLIError{
		CLI:    runtime.CLIPath,
		Args:   args,
		Stdout: stdout.String(),
		Stderr: stderr.String(),
		Code:   code,
	}
}

func replaceEnv(base []string, replacements map[string]string) []string {
	result := make([]string, 0, len(base)+len(replacements))
	for _, item := range base {
		key, _, ok := strings.Cut(item, "=")
		if _, replaced := replacements[key]; ok && replaced {
			continue
		}
		result = append(result, item)
	}
	for key, value := range replacements {
		if value != "" {
			result = append(result, key+"="+value)
		}
	}
	return result
}

func tencentToolError(service, action string, err error, attempts int) *mcp.CallToolResult {
	type errorBody struct {
		Provider  string `json:"provider"`
		Service   string `json:"service"`
		Action    string `json:"action"`
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id,omitempty"`
		Retryable bool   `json:"retryable"`
		Attempts  int    `json:"attempts"`
	}
	body := errorBody{
		Provider: "tencent-cloud",
		Service:  service,
		Action:   action,
		Code:     "ClientError",
		Message:  sdk.RedactSecret(err.Error()),
		Attempts: attempts,
	}
	var apiErr *TencentAPIError
	if errors.As(err, &apiErr) {
		body.Code = apiErr.Code
		body.RequestID = apiErr.RequestID
		body.Retryable = isRetryableTencentCode(apiErr.Code)
		body.Message = sdk.RedactSecret(apiErr.Message)
	}
	data, marshalErr := json.Marshal(map[string]any{"error": body})
	if marshalErr != nil {
		return sdk.WrapError(service+"."+action, err)
	}
	return mcp.NewToolResultError(string(data))
}

func readAttemptsFromEnv(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 1 || value > 5 {
		return 3
	}
	return value
}

package tencent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/tttboy123/cloud-skills-mcp/internal/mcp/sdk"
)

func toolResultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if result == nil || len(result.Content) == 0 {
		t.Fatalf("tool result has no content: %#v", result)
	}
	text, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("first tool content is %T, not TextContent", result.Content[0])
	}
	return text.Text
}

func TestToolAnnotationsMatchBehavior(t *testing.T) {
	srv := NewServer(Runtime{
		LoadCreds: func() (*sdk.Creds, error) {
			return &sdk.Creds{Region: "ap-shanghai"}, nil
		},
	})

	c, err := client.NewInProcessClient(srv)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "test", Version: "test"}
	if _, err := c.Initialize(t.Context(), initReq); err != nil {
		t.Fatal(err)
	}

	listed, err := c.ListTools(context.Background(), mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		readOnly := tool.Name == "tencent_cvm_list_instances" || tool.Name == "tencent_cvm_describe_instance"
		if tool.Annotations.ReadOnlyHint == nil || *tool.Annotations.ReadOnlyHint != readOnly {
			t.Errorf("%s readOnlyHint mismatch: want %v, got %v", tool.Name, readOnly, tool.Annotations.ReadOnlyHint)
		}
		if tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint == readOnly {
			t.Errorf("%s destructiveHint mismatch: want %v, got %v", tool.Name, !readOnly, tool.Annotations.DestructiveHint)
		}
	}
}

func TestToolDiscoveryDoesNotLoadCredentials(t *testing.T) {
	loadCalls := 0
	srv := NewServer(Runtime{
		LoadCreds: func() (*sdk.Creds, error) {
			loadCalls++
			return nil, errors.New("credentials intentionally unavailable")
		},
	})

	c, err := client.NewInProcessClient(srv)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "test", Version: "test"}
	if _, err := c.Initialize(t.Context(), initReq); err != nil {
		t.Fatal(err)
	}
	listed, err := c.ListTools(t.Context(), mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 4 {
		t.Fatalf("expected four tools, got %d", len(listed.Tools))
	}
	if loadCalls != 0 {
		t.Fatalf("tool discovery loaded credentials %d times", loadCalls)
	}
}

func TestDisabledMutationDoesNotLoadCredentials(t *testing.T) {
	loadCalls := 0
	runtime := Runtime{
		AllowMutations: false,
		LoadCreds: func() (*sdk.Creds, error) {
			loadCalls++
			return &sdk.Creds{Region: "ap-shanghai"}, nil
		},
	}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"instance_id": "ins-12345678",
		"force":       true,
	}
	if _, err := makeStartHandler(runtime)(t.Context(), req); err == nil {
		t.Fatal("expected disabled mutation to fail")
	}
	if loadCalls != 0 {
		t.Fatalf("disabled mutation loaded credentials %d times", loadCalls)
	}
}

func TestDescribeRemovesPayloadFileAfterCall(t *testing.T) {
	tempDir := t.TempDir()
	fakeCLI := filepath.Join(t.TempDir(), "fake-tccli")
	script := `#!/bin/sh
for arg in "$@"; do
  case "$arg" in
    file://*) test -f "${arg#file://}" || exit 9 ;;
  esac
done
echo '{"ok":true}'
`
	if err := os.WriteFile(fakeCLI, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	runtime := Runtime{
		CLIPath: fakeCLI,
		TempDir: tempDir,
		LoadCreds: func() (*sdk.Creds, error) {
			return &sdk.Creds{Region: "ap-shanghai"}, nil
		},
	}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"instance_id": "ins-12345678"}
	result, err := makeDescribeHandler(runtime)(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("describe returned an MCP error: %#v", result)
	}
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("payload files leaked after handler returned: %v", entries)
	}
}

func TestRunTccliOverridesInheritedCredentialEnvironment(t *testing.T) {
	t.Setenv("TENCENTCLOUD_SECRET_ID", "stale-id")
	t.Setenv("TENCENTCLOUD_SECRET_KEY", "stale-secret")
	fakeCLI := filepath.Join(t.TempDir(), "fake-tccli")
	script := `#!/bin/sh
printf '%s|%s' "$TENCENTCLOUD_SECRET_ID" "$TENCENTCLOUD_SECRET_KEY"
`
	if err := os.WriteFile(fakeCLI, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	runtime := Runtime{CLIPath: fakeCLI}.normalized()
	out, err := runTccli(t.Context(), runtime, &sdk.Creds{
		AccessKeyID:     "fresh-id",
		AccessKeySecret: "fresh-secret",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "fresh-id|fresh-secret" {
		t.Fatalf("child received stale or duplicate credentials: %q", out)
	}
}

func TestRunTccliCapturesBothStreamsOnFailure(t *testing.T) {
	fakeCLI := filepath.Join(t.TempDir(), "fake-tccli")
	script := `#!/bin/sh
printf 'stdout detail\n'
printf 'stderr detail\n' >&2
exit 7
`
	if err := os.WriteFile(fakeCLI, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	runtime := Runtime{CLIPath: fakeCLI}.normalized()
	_, err := runTccli(t.Context(), runtime, &sdk.Creds{}, nil)
	var cliErr *sdk.CLIError
	if !errors.As(err, &cliErr) {
		t.Fatalf("expected CLIError, got %T: %v", err, err)
	}
	if cliErr.Code != 7 || !strings.Contains(cliErr.Stderr, "stdout detail") || !strings.Contains(cliErr.Stderr, "stderr detail") {
		t.Fatalf("combined output or exit code missing: %#v", cliErr)
	}
}

func TestReadAndMutationHandlersWithFakeCLI(t *testing.T) {
	fakeCLI := filepath.Join(t.TempDir(), "fake-tccli")
	script := `#!/bin/sh
for arg in "$@"; do
  case "$arg" in
    file://*) test -f "${arg#file://}" || exit 9 ;;
  esac
done
printf '{"action":"%s"}' "$2"
`
	if err := os.WriteFile(fakeCLI, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	runtime := Runtime{
		CLIPath:        fakeCLI,
		TempDir:        t.TempDir(),
		AllowMutations: true,
		LoadCreds: func() (*sdk.Creds, error) {
			return &sdk.Creds{AccessKeyID: "id", AccessKeySecret: "secret", Region: "ap-shanghai"}, nil
		},
	}
	tests := []struct {
		name    string
		handler server.ToolHandlerFunc
		args    map[string]any
		action  string
	}{
		{"list", makeListHandler(runtime), map[string]any{"limit": 10}, "DescribeInstances"},
		{"describe", makeDescribeHandler(runtime), map[string]any{"instance_id": "ins-12345678"}, "DescribeInstances"},
		{"start", makeStartHandler(runtime), map[string]any{"instance_id": "ins-12345678", "force": true}, "StartInstances"},
		{"stop", makeStopHandler(runtime), map[string]any{"instance_id": "ins-12345678", "force": true}, "StopInstances"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := mcp.CallToolRequest{}
			req.Params.Arguments = test.args
			result, err := test.handler(t.Context(), req)
			if err != nil || result.IsError {
				t.Fatalf("handler failed: result=%#v err=%v", result, err)
			}
			if text := toolResultText(t, result); !strings.Contains(text, test.action) {
				t.Fatalf("result %q does not contain action %q", text, test.action)
			}
		})
	}
}

func TestInvalidHandlerInputDoesNotLoadCredentials(t *testing.T) {
	loadCalls := 0
	runtime := Runtime{
		AllowMutations: true,
		LoadCreds: func() (*sdk.Creds, error) {
			loadCalls++
			return nil, errors.New("must not be called")
		},
	}
	tests := []struct {
		name    string
		handler server.ToolHandlerFunc
		args    map[string]any
	}{
		{"list-limit", makeListHandler(runtime), map[string]any{"limit": 101}},
		{"list-instance", makeListHandler(runtime), map[string]any{"instance_id": "bad", "limit": 20}},
		{"describe-region", makeDescribeHandler(runtime), map[string]any{"instance_id": "ins-12345678", "region": "../../bad"}},
		{"start-instance", makeStartHandler(runtime), map[string]any{"instance_id": "bad", "force": true}},
		{"stop-instance", makeStopHandler(runtime), map[string]any{"instance_id": "bad", "force": true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := mcp.CallToolRequest{}
			req.Params.Arguments = test.args
			result, err := test.handler(t.Context(), req)
			if err != nil || !result.IsError {
				t.Fatalf("expected a soft validation error, result=%#v err=%v", result, err)
			}
		})
	}
	if loadCalls != 0 {
		t.Fatalf("invalid input loaded credentials %d times", loadCalls)
	}
}

func TestHandlerRedactsCLIErrorButPreservesRequestID(t *testing.T) {
	fakeCLI := filepath.Join(t.TempDir(), "fake-tccli")
	script := `#!/bin/sh
echo 'SecretKey="super-secret-value" RequestId=36974a26-56f7-4d61-ab17-39107e442a5f' >&2
exit 7
`
	if err := os.WriteFile(fakeCLI, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	runtime := Runtime{
		CLIPath: fakeCLI,
		LoadCreds: func() (*sdk.Creds, error) {
			return &sdk.Creds{AccessKeyID: "id", AccessKeySecret: "secret", Region: "ap-shanghai"}, nil
		},
	}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"limit": 20}
	result, err := makeListHandler(runtime)(t.Context(), req)
	if err != nil || !result.IsError {
		t.Fatalf("expected soft CLI error, result=%#v err=%v", result, err)
	}
	text := toolResultText(t, result)
	if strings.Contains(text, "super-secret-value") || !strings.Contains(text, "***REDACTED***") {
		t.Fatalf("secret was not redacted: %s", text)
	}
	if !strings.Contains(text, "36974a26-56f7-4d61-ab17-39107e442a5f") {
		t.Fatalf("RequestId was lost: %s", text)
	}
}

func TestHelpAndRuntimeDefaults(t *testing.T) {
	t.Setenv("CLOUD_SKILLS_ALLOW_MUTATIONS", "1")
	runtime := DefaultRuntime()
	if !runtime.AllowMutations || runtime.CLIPath != "tccli" || runtime.CLITimeout <= 0 || runtime.LoadCreds == nil {
		t.Fatalf("unexpected default runtime: %#v", runtime)
	}
	help := HelpText()
	if !strings.Contains(help, "v0.2.0") || !strings.Contains(help, "CLOUD_SKILLS_ALLOW_MUTATIONS=1") {
		t.Fatalf("help text is stale: %s", help)
	}
}

func TestValidateInstanceID(t *testing.T) {
	for _, valid := range []string{"ins-12345678", "ins-AbCdEf0123456789"} {
		if err := validateInstanceID(valid); err != nil {
			t.Errorf("expected %q to be valid: %v", valid, err)
		}
	}
	for _, invalid := range []string{"", "lhins-12345678", "ins-short", "ins-1234;rm"} {
		if err := validateInstanceID(invalid); err == nil {
			t.Errorf("expected %q to be rejected", invalid)
		}
	}
}

func TestValidateRegionAndLimit(t *testing.T) {
	for _, region := range []string{"ap-shanghai", "ap-guangzhou-2"} {
		if err := validateRegion(region); err != nil {
			t.Errorf("expected region %q to be valid: %v", region, err)
		}
	}
	for _, region := range []string{"", "AP-SHANGHAI", "ap_shanghai", "../../tmp"} {
		if err := validateRegion(region); err == nil {
			t.Errorf("expected region %q to be rejected", region)
		}
	}
	for _, limit := range []int{1, 20, 100} {
		if err := validateLimit(limit); err != nil {
			t.Errorf("expected limit %d to be valid: %v", limit, err)
		}
	}
	for _, limit := range []int{0, -1, 101} {
		if err := validateLimit(limit); err == nil {
			t.Errorf("expected limit %d to be rejected", limit)
		}
	}
}

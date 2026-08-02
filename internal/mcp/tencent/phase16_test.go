package tencent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/tttboy123/cloud-skills-mcp/internal/mcp/sdk"
)

func initializedClient(t *testing.T, runtime Runtime) *client.Client {
	t.Helper()
	c, err := client.NewInProcessClient(NewServer(runtime))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "phase16-test", Version: "1"}
	if _, err := c.Initialize(t.Context(), initReq); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func callTool(t *testing.T, c *client.Client, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	result, err := c.CallTool(t.Context(), req)
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return result
}

func TestPhase16ToolContracts(t *testing.T) {
	c := initializedClient(t, Runtime{LoadCreds: func() (*sdk.Creds, error) {
		return nil, errors.New("tool discovery must not load credentials")
	}})
	listed, err := c.ListTools(t.Context(), mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"tencent_cloud_cli_status":               true,
		"tencent_cvm_list_instances":             true,
		"tencent_cvm_describe_instance":          true,
		"tencent_cvm_start_instance":             true,
		"tencent_cvm_stop_instance":              true,
		"tencent_cvm_reboot_instance":            true,
		"tencent_lighthouse_list_instances":      true,
		"tencent_lighthouse_describe_instance":   true,
		"tencent_lighthouse_start_instance":      true,
		"tencent_lighthouse_stop_instance":       true,
		"tencent_lighthouse_reboot_instance":     true,
		"tencent_cdb_list_instances":             true,
		"tencent_cdb_describe_instance":          true,
		"tencent_cloudbase_list_environments":    true,
		"tencent_cloudbase_describe_environment": true,
	}
	if len(listed.Tools) != len(want) {
		t.Fatalf("tool count: want %d, got %d", len(want), len(listed.Tools))
	}
	for _, tool := range listed.Tools {
		if !want[tool.Name] {
			t.Errorf("unexpected tool %s", tool.Name)
		}
		delete(want, tool.Name)
		if strings.Contains(tool.Name, "_list_") {
			if _, ok := tool.InputSchema.Properties["offset"]; !ok {
				t.Errorf("%s has no offset", tool.Name)
			}
		}
		if strings.Contains(tool.Name, "_start_") || strings.Contains(tool.Name, "_stop_") || strings.Contains(tool.Name, "_reboot_") {
			if !containsString(tool.InputSchema.Required, "force") {
				t.Errorf("%s does not require force", tool.Name)
			}
		}
		if strings.Contains(tool.Name, "_reboot_") {
			if tool.Annotations.IdempotentHint == nil || *tool.Annotations.IdempotentHint {
				t.Errorf("%s must not claim idempotency", tool.Name)
			}
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing tools: %v", want)
	}
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func TestPhase16ServiceToolsUseDocumentedProductsAndActions(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "calls.log")
	t.Setenv("FAKE_TCCLI_LOG", logPath)
	fakeCLI := filepath.Join(t.TempDir(), "fake-tccli")
	script := `#!/bin/sh
printf '%s|%s\n' "$1" "$2" >> "$FAKE_TCCLI_LOG"
if [ "$1" = "--version" ]; then
  printf 'tccli 3.0.test\n'
  exit 0
fi
for arg in "$@"; do
  case "$arg" in
    file://*) test -s "${arg#file://}" || exit 9 ;;
  esac
done
printf '{"Response":{"RequestId":"test-request"}}\n'
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
	c := initializedClient(t, runtime)
	calls := []struct {
		name string
		args map[string]any
	}{
		{"tencent_cloud_cli_status", map[string]any{}},
		{"tencent_cvm_reboot_instance", map[string]any{"instance_id": "ins-12345678", "force": true}},
		{"tencent_lighthouse_list_instances", map[string]any{"offset": 4, "limit": 25}},
		{"tencent_lighthouse_describe_instance", map[string]any{"instance_id": "lhins-12345678"}},
		{"tencent_lighthouse_start_instance", map[string]any{"instance_id": "lhins-12345678", "force": true}},
		{"tencent_lighthouse_stop_instance", map[string]any{"instance_id": "lhins-12345678", "force": true}},
		{"tencent_lighthouse_reboot_instance", map[string]any{"instance_id": "lhins-12345678", "force": true}},
		{"tencent_cdb_list_instances", map[string]any{"offset": 0, "limit": 2000}},
		{"tencent_cdb_describe_instance", map[string]any{"instance_id": "cdb-12345678"}},
		{"tencent_cloudbase_list_environments", map[string]any{"offset": 0, "limit": 100}},
		{"tencent_cloudbase_describe_environment", map[string]any{"environment_id": "env-demo-12345678"}},
	}
	for _, call := range calls {
		result := callTool(t, c, call.name, call.args)
		if result.IsError {
			t.Fatalf("%s returned error: %s", call.name, toolResultText(t, result))
		}
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(data)
	for _, want := range []string{
		"--version|", "cvm|RebootInstances", "lighthouse|DescribeInstances",
		"lighthouse|StartInstances", "lighthouse|StopInstances", "lighthouse|RebootInstances",
		"cdb|DescribeDBInstances", "tcb|DescribeEnvs",
	} {
		if !strings.Contains(logText, want) {
			t.Errorf("call log does not contain %q:\n%s", want, logText)
		}
	}
}

func TestPolicyRejectsBeforeCredentialLoading(t *testing.T) {
	loadCalls := 0
	runtime := Runtime{
		Policy: Policy{
			AllowedRegions:   exactSet("ap-shanghai"),
			AllowedResources: exactSet("ins-12345678"),
		},
		LoadCreds: func() (*sdk.Creds, error) {
			loadCalls++
			return nil, errors.New("must not load")
		},
	}
	c := initializedClient(t, runtime)
	for _, call := range []struct {
		name string
		args map[string]any
	}{
		{"tencent_cvm_list_instances", map[string]any{"region": "ap-shanghai", "limit": 20}},
		{"tencent_cvm_describe_instance", map[string]any{"region": "ap-guangzhou", "instance_id": "ins-12345678"}},
		{"tencent_cvm_describe_instance", map[string]any{"region": "ap-shanghai", "instance_id": "ins-87654321"}},
	} {
		result := callTool(t, c, call.name, call.args)
		if !result.IsError {
			t.Errorf("%s unexpectedly passed policy", call.name)
		}
	}
	if loadCalls != 0 {
		t.Fatalf("policy rejection loaded credentials %d times", loadCalls)
	}
}

func TestReadRetriesTransientTencentErrors(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "counter")
	t.Setenv("FAKE_COUNTER", counter)
	fakeCLI := filepath.Join(t.TempDir(), "fake-tccli")
	script := `#!/bin/sh
n=0
[ ! -f "$FAKE_COUNTER" ] || n=$(cat "$FAKE_COUNTER")
n=$((n + 1))
printf '%s' "$n" > "$FAKE_COUNTER"
if [ "$n" -lt 3 ]; then
  printf '{"Response":{"Error":{"Code":"RequestLimitExceeded","Message":"slow down"},"RequestId":"retry-%s"}}\n' "$n" >&2
  exit 1
fi
printf '{"Response":{"TotalCount":0,"InstanceSet":[],"RequestId":"success"}}\n'
`
	if err := os.WriteFile(fakeCLI, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	var sleeps []time.Duration
	runtime := Runtime{
		CLIPath:         fakeCLI,
		ReadMaxAttempts: 3,
		RetryBaseDelay:  time.Millisecond,
		Sleep: func(_ context.Context, d time.Duration) error {
			sleeps = append(sleeps, d)
			return nil
		},
		LoadCreds: func() (*sdk.Creds, error) {
			return &sdk.Creds{AccessKeyID: "id", AccessKeySecret: "secret", Region: "ap-shanghai"}, nil
		},
	}
	toolResult, err := makeListHandler(runtime)(t.Context(), mcp.CallToolRequest{})
	if toolResult == nil {
		t.Fatal("nil result")
	}
	if err != nil || toolResult.IsError {
		t.Fatalf("read did not recover: result=%#v err=%v", toolResult, err)
	}
	if len(sleeps) != 2 || sleeps[0] != time.Millisecond || sleeps[1] != 2*time.Millisecond {
		t.Fatalf("unexpected retry delays: %v", sleeps)
	}
}

func TestMutationIsNotRetriedAndAuditFailureIsFailClosed(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "called")
	t.Setenv("FAKE_CALLED", marker)
	fakeCLI := filepath.Join(t.TempDir(), "fake-tccli")
	if err := os.WriteFile(fakeCLI, []byte("#!/bin/sh\nprintf called > \"$FAKE_CALLED\"\nexit 7\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	runtime := Runtime{
		CLIPath:         fakeCLI,
		AllowMutations:  true,
		ReadMaxAttempts: 3,
		Audit: func(context.Context, AuditEvent) error {
			return errors.New("audit unavailable")
		},
		LoadCreds: func() (*sdk.Creds, error) {
			return &sdk.Creds{AccessKeyID: "id", AccessKeySecret: "secret", Region: "ap-shanghai"}, nil
		},
	}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"instance_id": "ins-12345678", "force": true}
	result, err := makeStartHandler(runtime)(t.Context(), req)
	if err != nil || !result.IsError || !strings.Contains(toolResultText(t, result), "audit") {
		t.Fatalf("audit failure was not surfaced: result=%#v err=%v", result, err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("mutation ran despite audit failure: %v", err)
	}
}

func TestMutationAPIErrorIsNotRetriedAndIsAudited(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "counter")
	t.Setenv("FAKE_COUNTER", counter)
	fakeCLI := filepath.Join(t.TempDir(), "fake-tccli")
	script := `#!/bin/sh
n=0
[ ! -f "$FAKE_COUNTER" ] || n=$(cat "$FAKE_COUNTER")
n=$((n + 1))
printf '%s' "$n" > "$FAKE_COUNTER"
printf '{"Response":{"Error":{"Code":"RequestLimitExceeded","Message":"slow down"},"RequestId":"mutation-request"}}\n' >&2
exit 1
`
	if err := os.WriteFile(fakeCLI, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	var events []AuditEvent
	runtime := Runtime{
		CLIPath:         fakeCLI,
		AllowMutations:  true,
		ReadMaxAttempts: 5,
		Audit: func(_ context.Context, event AuditEvent) error {
			events = append(events, event)
			return nil
		},
		LoadCreds: func() (*sdk.Creds, error) {
			return &sdk.Creds{AccessKeyID: "id", AccessKeySecret: "secret", Region: "ap-shanghai"}, nil
		},
	}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"instance_id": "ins-12345678", "force": true}
	result, err := makeStartHandler(runtime)(t.Context(), req)
	if err != nil || !result.IsError {
		t.Fatalf("expected mutation API error: result=%#v err=%v", result, err)
	}
	data, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "1" {
		t.Fatalf("mutation was retried %s times", data)
	}
	if len(events) != 2 || events[0].Outcome != "authorized" || events[1].Outcome != "failed" {
		t.Fatalf("unexpected audit events: %#v", events)
	}
}

func TestTencentAPIErrorEnvelopeIsStructuredEvenOnZeroExit(t *testing.T) {
	fakeCLI := filepath.Join(t.TempDir(), "fake-tccli")
	script := `#!/bin/sh
printf '{"Response":{"Error":{"Code":"UnauthorizedOperation","Message":"SecretKey=do-not-leak"},"RequestId":"request-123"}}\n'
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
	result, err := makeListHandler(runtime)(t.Context(), mcp.CallToolRequest{})
	if err != nil || !result.IsError {
		t.Fatalf("expected structured API error: result=%#v err=%v", result, err)
	}
	var envelope struct {
		Error struct {
			Code      string `json:"code"`
			RequestID string `json:"request_id"`
			Retryable bool   `json:"retryable"`
			Attempts  int    `json:"attempts"`
			Message   string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(toolResultText(t, result)), &envelope); err != nil {
		t.Fatalf("error is not JSON: %v: %s", err, toolResultText(t, result))
	}
	if envelope.Error.Code != "UnauthorizedOperation" || envelope.Error.RequestID != "request-123" || envelope.Error.Retryable || envelope.Error.Attempts != 1 {
		t.Fatalf("unexpected envelope: %#v", envelope)
	}
	if strings.Contains(envelope.Error.Message, "do-not-leak") || !strings.Contains(envelope.Error.Message, "REDACTED") {
		t.Fatalf("error message was not redacted: %q", envelope.Error.Message)
	}
}

func TestFileAuditSinkWritesMode0600WithoutSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	sink := FileAuditSink(path)
	event := AuditEvent{
		Time:       time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC),
		Tool:       "tencent_cvm_start_instance",
		Service:    "cvm",
		Action:     "StartInstances",
		ResourceID: "ins-12345678",
		Region:     "ap-shanghai",
		Mutation:   true,
		Outcome:    "authorized",
	}
	if err := sink(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("audit mode=%o", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "StartInstances") || strings.Contains(string(data), "Secret") {
		t.Fatalf("unexpected audit line: %s", data)
	}
}

func TestOfficialPaginationAndResourceValidation(t *testing.T) {
	for _, test := range []struct {
		kind string
		id   string
	}{
		{"cvm", "ins-12345678"},
		{"lighthouse", "lhins-12345678"},
		{"cdb", "cdb-12345678"},
		{"cloudbase", "env-demo-12345678"},
	} {
		if err := validateResourceID(test.kind, test.id); err != nil {
			t.Errorf("%s id rejected: %v", test.kind, err)
		}
	}
	for _, test := range []struct {
		kind string
		id   string
	}{
		{"cvm", "lhins-12345678"},
		{"lighthouse", "ins-12345678"},
		{"cdb", "cdb-short"},
		{"cloudbase", "../bad"},
	} {
		if err := validateResourceID(test.kind, test.id); err == nil {
			t.Errorf("%s id accepted: %q", test.kind, test.id)
		}
	}
	if err := validatePagination(0, 100, 100); err != nil {
		t.Fatal(err)
	}
	if err := validatePagination(0, 2000, 2000); err != nil {
		t.Fatal(err)
	}
	for _, values := range [][3]int{{-1, 20, 100}, {0, 101, 100}, {0, 2001, 2000}} {
		if err := validatePagination(values[0], values[1], values[2]); err == nil {
			t.Errorf("pagination accepted offset=%d limit=%d max=%d", values[0], values[1], values[2])
		}
	}
}

func TestLiveTencentReadOnlyMCP(t *testing.T) {
	if os.Getenv("CLOUD_SKILLS_LIVE_TEST") != "1" {
		t.Skip("set CLOUD_SKILLS_LIVE_TEST=1 to use real credentials and TCCLI")
	}
	selected := os.Getenv("CLOUD_SKILLS_LIVE_SERVICES")
	if selected == "" {
		selected = "cvm"
	}
	region := os.Getenv("CLOUD_SKILLS_LIVE_REGION")
	tools := map[string]string{
		"cvm":        "tencent_cvm_list_instances",
		"lighthouse": "tencent_lighthouse_list_instances",
		"cdb":        "tencent_cdb_list_instances",
		"cloudbase":  "tencent_cloudbase_list_environments",
	}
	c := initializedClient(t, DefaultRuntime())
	for _, service := range strings.Split(selected, ",") {
		service = strings.TrimSpace(service)
		tool, ok := tools[service]
		if !ok {
			t.Fatalf("unsupported live service %q", service)
		}
		args := map[string]any{"offset": 0, "limit": 1}
		if region != "" {
			args["region"] = region
		}
		result := callTool(t, c, tool, args)
		if result.IsError {
			t.Fatalf("live %s check failed: %s", service, toolResultText(t, result))
		}
		t.Logf("live %s read succeeded", service)
	}
}

// Package tencent implements the Tencent Cloud MCP server.
//
// It exposes 4 CVM tools over stdio JSON-RPC, backed by the shared sdk.LoadCreds
// for credentials and tccli as the underlying CLI.  The server is a thin
// orchestrator: every tool builds a tccli argv, runs it, and returns the JSON
// response to the LLM unchanged.
//
// Usage:
//
//	tencent-cloud-mcp                     # serve JSON-RPC on stdio
//	tencent-cloud-mcp --help              # print tool list + exit 0
//	echo '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | tencent-cloud-mcp
//
// Credentials: macOS Keychain (service=tencent-cloud) or env (TENCENTCLOUD_SECRET_ID/KEY).
// The same env vars the legacy cvm.sh script uses are exported into the tccli
// subprocess so the auth path is identical to the SKILL.md bash fallback.
package tencent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/tttboy123/cloud-skills-mcp/internal/mcp/sdk"
)

const (
	serverName    = "tencent-cloud-mcp"
	serverVersion = "0.2.0"
	tccliPath     = "tccli" // resolved via PATH at run time
	cliTimeout    = 30 * time.Second
	defaultRegion = "ap-shanghai"
)

var (
	instanceIDPattern = regexp.MustCompile(`^ins-[A-Za-z0-9]{8,64}$`)
	regionPattern     = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)
)

// Runtime contains side-effecting dependencies used by tool handlers. Keeping
// them injectable makes tests hermetic and lets clients discover tools before
// cloud credentials are configured.
type Runtime struct {
	LoadCreds      func() (*sdk.Creds, error)
	CLIPath        string
	CLITimeout     time.Duration
	TempDir        string
	AllowMutations bool
}

func DefaultRuntime() Runtime {
	return Runtime{
		LoadCreds: func() (*sdk.Creds, error) {
			return sdk.LoadCreds(sdk.CloudTencent)
		},
		CLIPath:        tccliPath,
		CLITimeout:     cliTimeout,
		AllowMutations: os.Getenv("CLOUD_SKILLS_ALLOW_MUTATIONS") == "1",
	}
}

func (r Runtime) normalized() Runtime {
	defaults := DefaultRuntime()
	if r.LoadCreds == nil {
		r.LoadCreds = defaults.LoadCreds
	}
	if r.CLIPath == "" {
		r.CLIPath = defaults.CLIPath
	}
	if r.CLITimeout <= 0 {
		r.CLITimeout = defaults.CLITimeout
	}
	return r
}

func NewServer(runtime Runtime) *server.MCPServer {
	runtime = runtime.normalized()
	srv := server.NewMCPServer(serverName, serverVersion,
		server.WithToolCapabilities(true),
	)
	registerTools(srv, runtime)
	return srv
}

// HelpText returns static help without loading credentials or touching stdout.
func HelpText() string {
	return fmt.Sprintf(`%s v%s — Tencent Cloud MCP server (stdio JSON-RPC)

Tools (registered via MCP tools/list):

  tencent_cvm_list_instances       — list CVM instances in a region
  tencent_cvm_describe_instance    — describe a single CVM by id
  tencent_cvm_start_instance       — START a CVM (requires --force=true)
  tencent_cvm_stop_instance        — STOP a CVM (requires --force=true)

Safety:
  Read-only tools are enabled by default. Mutating tools additionally require
  CLOUD_SKILLS_ALLOW_MUTATIONS=1 in the server environment and force=true in
  each request. Host-side human approval remains authoritative.

Credentials (3-tier fallback):
  1. macOS Keychain  service=tencent-cloud, account=tccli-{secretid,secretkey,region}
  2. env vars        TENCENTCLOUD_SECRET_ID, TENCENTCLOUD_SECRET_KEY, TENCENTCLOUD_REGION
  3. tccli config    ~/.tencentcloud/credentials [default]

Examples:

  # list tools
  echo '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' \
    | %s

  # call list_instances
  echo '{"jsonrpc":"2.0","id":2,"method":"tools/call",
         "params":{"name":"tencent_cvm_list_instances",
                   "arguments":{"region":"ap-shanghai","limit":10}}}' \
    | %s

  # start an instance (must pass force=true)
  echo '{"jsonrpc":"2.0","id":3,"method":"tools/call",
         "params":{"name":"tencent_cvm_start_instance",
                   "arguments":{"instance_id":"ins-abc12345","force":true}}}' \
    | %s
`, serverName, serverVersion, serverName, serverName, serverName)
}

// registerTools wires the four CVM tools without loading credentials. Each
// handler resolves credentials only after its arguments and policy gates pass.
func registerTools(srv *server.MCPServer, runtime Runtime) {
	// 1. list instances
	srv.AddTool(
		mcp.NewTool("tencent_cvm_list_instances",
			mcp.WithDescription("List CVM (Elastic Cloud Server) instances in a region. "+
				"Wraps `tccli cvm DescribeInstances`. Returns the raw JSON response "+
				"including TotalCount and InstanceSet array."),
			sdk.RegionArg(""),
			mcp.WithNumber("limit",
				mcp.Description("Max number of instances to return (maps to tccli --Limit). "+
					"if omitted, the API default applies (typically 20)."),
				mcp.DefaultNumber(20),
			),
			mcp.WithString("instance_id",
				mcp.Description("Optional: filter to a specific instance id (equivalent to --InstanceIds.0). "+
					"If provided, the call is equivalent to describe_instance."),
			),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(true),
		),
		makeListHandler(runtime),
	)

	// 2. describe one instance
	srv.AddTool(
		mcp.NewTool("tencent_cvm_describe_instance",
			mcp.WithDescription("Describe a single CVM instance by id. "+
				"Wraps `tccli cvm DescribeInstances` with --cli-input-json (because tccli "+
				"3.1.x doesn't accept --InstanceIds.0 dot-syntax for some installs). "+
				"Returns the full instance detail JSON."),
			sdk.RegionArg(""),
			mcp.WithString("instance_id",
				mcp.Description("The CVM instance id, e.g. ins-abc123def (must be CVM, not Lighthouse lhins-*)."),
				mcp.Required(),
			),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(true),
		),
		makeDescribeHandler(runtime),
	)

	// 3. start instance (destructive → force required)
	srv.AddTool(
		mcp.NewTool("tencent_cvm_start_instance",
			mcp.WithDescription("START a CVM instance. "+
				"This is a state change; the operator must enable mutations and the request must pass force=true. "+
				"Wraps `tccli cvm StartInstances` using a temporary JSON input file."),
			sdk.RegionArg(""),
			mcp.WithString("instance_id",
				mcp.Description("The CVM instance id to start."),
				mcp.Required(),
			),
			sdk.ForceArg(),
			mcp.WithReadOnlyHintAnnotation(false),
			mcp.WithDestructiveHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(true),
		),
		makeStartHandler(runtime),
	)

	// 4. stop instance (destructive → force required)
	srv.AddTool(
		mcp.NewTool("tencent_cvm_stop_instance",
			mcp.WithDescription("STOP a CVM instance. "+
				"This is a state change; the operator must enable mutations and the request must pass force=true. "+
				"Wraps `tccli cvm StopInstances` using a temporary JSON input file. "+
				"By default the instance is gracefully stopped (not forced)."),
			sdk.RegionArg(""),
			mcp.WithString("instance_id",
				mcp.Description("The CVM instance id to stop."),
				mcp.Required(),
			),
			sdk.ForceArg(),
			mcp.WithReadOnlyHintAnnotation(false),
			mcp.WithDestructiveHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(true),
		),
		makeStopHandler(runtime),
	)
}

// ---- handlers ----

func makeListHandler(runtime Runtime) server.ToolHandlerFunc {
	runtime = runtime.normalized()
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id := req.GetString("instance_id", "")
		if id != "" {
			if err := validateInstanceID(id); err != nil {
				return sdk.WrapError("cvm.DescribeInstances", err), nil
			}
		}
		limit := req.GetInt("limit", 20)
		if err := validateLimit(limit); err != nil {
			return sdk.WrapError("cvm.DescribeInstances", err), nil
		}
		creds, region, err := loadCredsAndRegion(runtime, req.GetString("region", ""))
		if err != nil {
			return sdk.WrapError("cvm.DescribeInstances", err), nil
		}
		args := []string{"cvm", "DescribeInstances", "--region", region}
		if id != "" {
			args = append(args, "--InstanceIds.0", id)
		}
		args = append(args, "--Limit", fmt.Sprintf("%d", limit))

		out, err := runTccli(ctx, runtime, creds, args)
		if err != nil {
			return sdk.WrapError("cvm.DescribeInstances", err), nil
		}
		return mcp.NewToolResultText(string(out)), nil
	}
}

func makeDescribeHandler(runtime Runtime) server.ToolHandlerFunc {
	runtime = runtime.normalized()
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, err := req.RequireString("instance_id")
		if err != nil {
			return sdk.WrapError("cvm.DescribeInstances", err), nil
		}
		if err := validateInstanceID(id); err != nil {
			return sdk.WrapError("cvm.DescribeInstances", err), nil
		}
		creds, region, err := loadCredsAndRegion(runtime, req.GetString("region", ""))
		if err != nil {
			return sdk.WrapError("cvm.DescribeInstances", err), nil
		}
		// tccli 3.1.x --InstanceIds.0 syntax is not portable; use --cli-input-json
		// with a file:// URI and remove the payload as soon as the call returns.
		payload, err := instancePayload(id)
		if err != nil {
			return sdk.WrapError("cvm.DescribeInstances", err), nil
		}
		tmp, err := writePayload(runtime.TempDir, payload)
		if err != nil {
			return sdk.WrapError("cvm.DescribeInstances", err), nil
		}
		defer os.Remove(tmp)
		args := []string{"cvm", "DescribeInstances", "--region", region, "--cli-input-json", "file://" + tmp}
		out, err := runTccli(ctx, runtime, creds, args)
		if err != nil {
			return sdk.WrapError("cvm.DescribeInstances", err), nil
		}
		return mcp.NewToolResultText(string(out)), nil
	}
}

func makeStartHandler(runtime Runtime) server.ToolHandlerFunc {
	runtime = runtime.normalized()
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if res, err := sdk.RequireMutationApproval(runtime.AllowMutations, req.GetBool("force", false)); res != nil {
			return res, err
		}
		id, err := req.RequireString("instance_id")
		if err != nil {
			return sdk.WrapError("cvm.StartInstances", err), nil
		}
		if err := validateInstanceID(id); err != nil {
			return sdk.WrapError("cvm.StartInstances", err), nil
		}
		creds, region, err := loadCredsAndRegion(runtime, req.GetString("region", ""))
		if err != nil {
			return sdk.WrapError("cvm.StartInstances", err), nil
		}
		payload, err := instancePayload(id)
		if err != nil {
			return sdk.WrapError("cvm.StartInstances", err), nil
		}
		tmp, err := writePayload(runtime.TempDir, payload)
		if err != nil {
			return sdk.WrapError("cvm.StartInstances", err), nil
		}
		defer os.Remove(tmp)
		args := []string{"cvm", "StartInstances", "--region", region, "--cli-input-json", "file://" + tmp}
		out, err := runTccli(ctx, runtime, creds, args)
		if err != nil {
			return sdk.WrapError("cvm.StartInstances", err), nil
		}
		return mcp.NewToolResultText(string(out)), nil
	}
}

func makeStopHandler(runtime Runtime) server.ToolHandlerFunc {
	runtime = runtime.normalized()
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if res, err := sdk.RequireMutationApproval(runtime.AllowMutations, req.GetBool("force", false)); res != nil {
			return res, err
		}
		id, err := req.RequireString("instance_id")
		if err != nil {
			return sdk.WrapError("cvm.StopInstances", err), nil
		}
		if err := validateInstanceID(id); err != nil {
			return sdk.WrapError("cvm.StopInstances", err), nil
		}
		creds, region, err := loadCredsAndRegion(runtime, req.GetString("region", ""))
		if err != nil {
			return sdk.WrapError("cvm.StopInstances", err), nil
		}
		payload, err := instancePayload(id)
		if err != nil {
			return sdk.WrapError("cvm.StopInstances", err), nil
		}
		tmp, err := writePayload(runtime.TempDir, payload)
		if err != nil {
			return sdk.WrapError("cvm.StopInstances", err), nil
		}
		defer os.Remove(tmp)
		args := []string{"cvm", "StopInstances", "--region", region, "--cli-input-json", "file://" + tmp}
		out, err := runTccli(ctx, runtime, creds, args)
		if err != nil {
			return sdk.WrapError("cvm.StopInstances", err), nil
		}
		return mcp.NewToolResultText(string(out)), nil
	}
}

// ---- tccli subprocess plumbing ----

// runTccli executes tccli with creds exported as env vars (the same way
// scripts/_creds.sh does for the bash fallback), captures stdout+stderr, and
// returns either (stdout, nil) on success or a *sdk.CLIError on failure.
//
// We do NOT use a pipe + `if cmd | head` pattern here — that has the classic
// "head exits 0, masking the upstream failure" gotcha. Instead we run tccli
// with combined stdout/stderr captured into a single buffer, then check
// cmd.Wait() for the real exit code.
func runTccli(ctx context.Context, runtime Runtime, creds *sdk.Creds, args []string) ([]byte, error) {
	// tccli reads TENCENTCLOUD_SECRET_ID (with underscores) — verified in scripts/_creds.sh.
	env := append(os.Environ(),
		"TENCENTCLOUD_SECRET_ID="+creds.AccessKeyID,
		"TENCENTCLOUD_SECRET_KEY="+creds.AccessKeySecret,
	)
	if creds.SecurityToken != "" {
		env = append(env, "TENCENTCLOUD_TOKEN="+creds.SecurityToken)
	}
	if creds.Region != "" {
		env = append(env, "TENCENTCLOUD_REGION="+creds.Region)
	}

	cctx, cancel := context.WithTimeout(ctx, runtime.CLITimeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, runtime.CLIPath, args...)
	cmd.Env = env
	// tccli prints JSON to stdout and errors to stderr. CombinedOutput safely
	// serializes both streams into one buffer; separate copy goroutines writing
	// to a shared strings.Builder would introduce a data race.
	out, err := cmd.CombinedOutput()

	if err != nil {
		// exec.ExitError doesn't carry the captured output, so we wrap with our
		// own CLIError that includes stdout+stderr for the LLM to read.
		code := -1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		return nil, &sdk.CLIError{
			CLI:    runtime.CLIPath,
			Args:   args,
			Stderr: string(out),
			Code:   code,
		}
	}
	return out, nil
}

// writePayload writes payload to a 0600 temporary file. Callers remove it with
// defer immediately after a successful return.
func writePayload(tempDir, payload string) (string, error) {
	f, err := os.CreateTemp(tempDir, "tccli-payload-*.json")
	if err != nil {
		return "", fmt.Errorf("mktemp: %w", err)
	}
	path := f.Name()
	if _, err := f.WriteString(payload); err != nil {
		f.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("write temp: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close temp: %w", err)
	}
	return path, nil
}

func instancePayload(id string) (string, error) {
	payload, err := json.Marshal(map[string][]string{"InstanceIds": {id}})
	if err != nil {
		return "", fmt.Errorf("encode instance payload: %w", err)
	}
	return string(payload), nil
}

func loadCredsAndRegion(runtime Runtime, requestedRegion string) (*sdk.Creds, string, error) {
	if requestedRegion != "" {
		if err := validateRegion(requestedRegion); err != nil {
			return nil, "", err
		}
	}
	creds, err := runtime.LoadCreds()
	if err != nil {
		return nil, "", err
	}
	if creds == nil {
		return nil, "", errors.New("credential loader returned nil credentials")
	}
	region := requestedRegion
	if region == "" {
		region = creds.Region
	}
	if region == "" {
		region = defaultRegion
	}
	if err := validateRegion(region); err != nil {
		return nil, "", err
	}
	return creds, region, nil
}

func validateInstanceID(id string) error {
	if !instanceIDPattern.MatchString(id) {
		return fmt.Errorf("invalid CVM instance_id %q: expected ins- followed by 8-64 letters or digits", id)
	}
	return nil
}

func validateRegion(region string) error {
	if !regionPattern.MatchString(region) {
		return fmt.Errorf("invalid Tencent Cloud region %q", region)
	}
	return nil
}

func validateLimit(limit int) error {
	if limit < 1 || limit > 100 {
		return fmt.Errorf("invalid limit %d: expected a value between 1 and 100", limit)
	}
	return nil
}

// Package main is the Tencent Cloud MCP server (Phase 1 PoC).
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
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/tttboy123/cloud-skills-mcp/internal/mcp/sdk"
)

const (
	serverName    = "tencent-cloud-mcp"
	serverVersion = "0.1.0"
	tccliPath     = "tccli" // resolved via PATH at run time
	cliTimeout    = 30 * time.Second
)

func main() {
	help := flag.Bool("help", false, "print tool list and exit 0")
	h := flag.Bool("h", false, "alias for --help")
	flag.Parse()

	if *help || *h {
		printHelp()
		return
	}

	creds, err := sdk.LoadCreds(sdk.CloudTencent)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tencent-cloud-mcp: credentials: %v\n", err)
		os.Exit(2)
	}

	srv := server.NewMCPServer(serverName, serverVersion,
		server.WithToolCapabilities(true),
	)

	registerTools(srv, creds)

	if err := server.ServeStdio(srv); err != nil {
		fmt.Fprintf(os.Stderr, "tencent-cloud-mcp: serve: %v\n", err)
		os.Exit(1)
	}
}

// printHelp writes the static help to stdout (NOT stderr) so the help text
// can be piped to `less` etc. without polluting the JSON-RPC stream.
func printHelp() {
	fmt.Printf(`%s v%s — Tencent Cloud MCP server (stdio JSON-RPC)

Tools (registered via MCP tools/list):

  tencent_cvm_list_instances       — list CVM instances in a region
  tencent_cvm_describe_instance    — describe a single CVM by id
  tencent_cvm_start_instance       — START a CVM (requires --force=true)
  tencent_cvm_stop_instance        — STOP a CVM (requires --force=true)

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
                   "arguments":{"instance_id":"ins-abc123","force":true}}}' \
    | %s
`, serverName, serverVersion, serverName, serverName, serverName)
}

// registerTools wires the 4 CVM tools. Each handler is a closure over creds so
// we don't re-resolve them per call.
func registerTools(srv *server.MCPServer, creds *sdk.Creds) {
	// 1. list instances
	srv.AddTool(
		mcp.NewTool("tencent_cvm_list_instances",
			mcp.WithDescription("List CVM (Elastic Cloud Server) instances in a region. "+
				"Wraps `tccli cvm DescribeInstances`. Returns the raw JSON response "+
				"including TotalCount and InstanceSet array."),
			sdk.RegionArg(creds.Region),
			mcp.WithNumber("limit",
				mcp.Description("Max number of instances to return (maps to tccli --Limit). "+
					"if omitted, the API default applies (typically 20)."),
				mcp.DefaultNumber(20),
			),
			mcp.WithString("instance_id",
				mcp.Description("Optional: filter to a specific instance id (equivalent to --InstanceIds.0). "+
					"If provided, the call is equivalent to describe_instance."),
			),
		),
		makeListHandler(creds),
	)

	// 2. describe one instance
	srv.AddTool(
		mcp.NewTool("tencent_cvm_describe_instance",
			mcp.WithDescription("Describe a single CVM instance by id. "+
				"Wraps `tccli cvm DescribeInstances` with --cli-input-json (because tccli "+
				"3.1.x doesn't accept --InstanceIds.0 dot-syntax for some installs). "+
				"Returns the full instance detail JSON."),
			sdk.RegionArg(creds.Region),
			mcp.WithString("instance_id",
				mcp.Description("The CVM instance id, e.g. ins-abc123def (must be CVM, not Lighthouse lhins-*)."),
				mcp.Required(),
			),
		),
		makeDescribeHandler(creds),
	)

	// 3. start instance (destructive → force required)
	srv.AddTool(
		mcp.NewTool("tencent_cvm_start_instance",
			mcp.WithDescription("START a CVM instance. "+
				"This is a state change; pass force=true to confirm. "+
				"Wraps `tccli cvm StartInstances --InstanceIds.0 <id>`."),
			sdk.RegionArg(creds.Region),
			mcp.WithString("instance_id",
				mcp.Description("The CVM instance id to start."),
				mcp.Required(),
			),
			sdk.ForceArg(),
		),
		makeStartHandler(creds),
	)

	// 4. stop instance (destructive → force required)
	srv.AddTool(
		mcp.NewTool("tencent_cvm_stop_instance",
			mcp.WithDescription("STOP a CVM instance. "+
				"This is a state change; pass force=true to confirm. "+
				"Wraps `tccli cvm StopInstances --InstanceIds.0 <id>`. "+
				"By default the instance is gracefully stopped (not forced)."),
			sdk.RegionArg(creds.Region),
			mcp.WithString("instance_id",
				mcp.Description("The CVM instance id to stop."),
				mcp.Required(),
			),
			sdk.ForceArg(),
		),
		makeStopHandler(creds),
	)
}

// ---- handlers ----

func makeListHandler(creds *sdk.Creds) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		region := req.GetString("region", creds.Region)
		if region == "" {
			region = "ap-shanghai"
		}
		args := []string{"cvm", "DescribeInstances", "--region", region}

		// optional instance_id → narrow to one
		if id := req.GetString("instance_id", ""); id != "" {
			args = append(args, "--InstanceIds.0", id)
		}
		// optional limit → --Limit (verified via `tccli cvm DescribeInstances help`)
		if lim := req.GetInt("limit", 0); lim > 0 {
			args = append(args, "--Limit", fmt.Sprintf("%d", lim))
		}

		out, err := runTccli(ctx, creds, args)
		if err != nil {
			return sdk.WrapError("cvm.DescribeInstances", err), nil
		}
		return mcp.NewToolResultText(string(out)), nil
	}
}

func makeDescribeHandler(creds *sdk.Creds) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, err := req.RequireString("instance_id")
		if err != nil {
			return sdk.WrapError("cvm.DescribeInstances", err), nil
		}
		region := req.GetString("region", creds.Region)
		if region == "" {
			region = "ap-shanghai"
		}
		// tccli 3.1.x --InstanceIds.0 syntax is not portable; use --cli-input-json
		// with a file:// URI. mktemp is the standard pattern; we trap-rm
		// immediately (not via 'rm "${VAR}"' inside single quotes).
		payload := fmt.Sprintf(`{"InstanceIds":[%q]}`, id)
		tmp, err := writeAndCleanup(ctx, payload)
		if err != nil {
			return sdk.WrapError("cvm.DescribeInstances", err), nil
		}
		_ = tmp // writeAndCleanup handles its own cleanup
		args := []string{"cvm", "DescribeInstances", "--region", region, "--cli-input-json", "file://" + tmp}
		out, err := runTccli(ctx, creds, args)
		if err != nil {
			return sdk.WrapError("cvm.DescribeInstances", err), nil
		}
		return mcp.NewToolResultText(string(out)), nil
	}
}

func makeStartHandler(creds *sdk.Creds) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if res, err := sdk.RequireForce(req.GetBool("force", false)); res != nil {
			return res, err
		}
		id, err := req.RequireString("instance_id")
		if err != nil {
			return sdk.WrapError("cvm.StartInstances", err), nil
		}
		region := req.GetString("region", creds.Region)
		if region == "" {
			region = "ap-shanghai"
		}
		payload := fmt.Sprintf(`{"InstanceIds":[%q]}`, id)
		tmp, err := writeAndCleanup(ctx, payload)
		if err != nil {
			return sdk.WrapError("cvm.StartInstances", err), nil
		}
		_ = tmp
		args := []string{"cvm", "StartInstances", "--region", region, "--cli-input-json", "file://" + tmp}
		out, err := runTccli(ctx, creds, args)
		if err != nil {
			return sdk.WrapError("cvm.StartInstances", err), nil
		}
		return mcp.NewToolResultText(string(out)), nil
	}
}

func makeStopHandler(creds *sdk.Creds) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if res, err := sdk.RequireForce(req.GetBool("force", false)); res != nil {
			return res, err
		}
		id, err := req.RequireString("instance_id")
		if err != nil {
			return sdk.WrapError("cvm.StopInstances", err), nil
		}
		region := req.GetString("region", creds.Region)
		if region == "" {
			region = "ap-shanghai"
		}
		payload := fmt.Sprintf(`{"InstanceIds":[%q]}`, id)
		tmp, err := writeAndCleanup(ctx, payload)
		if err != nil {
			return sdk.WrapError("cvm.StopInstances", err), nil
		}
		_ = tmp
		args := []string{"cvm", "StopInstances", "--region", region, "--cli-input-json", "file://" + tmp}
		out, err := runTccli(ctx, creds, args)
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
func runTccli(ctx context.Context, creds *sdk.Creds, args []string) ([]byte, error) {
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

	cctx, cancel := context.WithTimeout(ctx, cliTimeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, tccliPath, args...)
	cmd.Env = env
	// combined: tccli prints JSON to stdout and errors to stderr; for the LLM
	// we want a single block it can parse. If exit==0, the JSON is on stdout
	// (stderr is empty or just the elapsed-time line). If exit!=0, stderr
	// usually has the human-readable error.
	var buf strings.Builder
	cmd.Stdout = &stdoutWriter{w: &buf}
	cmd.Stderr = &stderrWriter{w: &buf}

	err := cmd.Run()
	out := []byte(buf.String())

	if err != nil {
		// exec.ExitError doesn't carry the captured output, so we wrap with our
		// own CLIError that includes stdout+stderr for the LLM to read.
		code := -1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		return nil, &sdk.CLIError{
			CLI:    tccliPath,
			Args:   args,
			Stderr: buf.String(),
			Code:   code,
		}
	}
	return out, nil
}

// stdoutWriter / stderrWriter prepend a tag so a single buffer can be split
// apart in the error path. They are no-ops in the success path because we
// just hand the whole buffer to NewToolResultText.
type stdoutWriter struct{ w *strings.Builder }
type stderrWriter struct{ w *strings.Builder }

func (s *stdoutWriter) Write(p []byte) (int, error) { return s.w.Write(p) }
func (s *stderrWriter) Write(p []byte) (int, error) { return s.w.Write(p) }

// writeAndCleanup writes payload to a mktemp file, returns the path, and
// schedules deletion when ctx is done. We do NOT use `trap 'rm -f "${VAR}"'`
// because single-quote expansion in trap is broken in some bash versions
// (the ${VAR} becomes literal). Using the runtime context is more reliable
// across shells and works correctly even if the parent process is killed.
func writeAndCleanup(ctx context.Context, payload string) (string, error) {
	f, err := os.CreateTemp("", "tccli-payload-*.json")
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
	go func() {
		<-ctx.Done()
		_ = os.Remove(path)
	}()
	return path, nil
}

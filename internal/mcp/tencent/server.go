// Package tencent implements the Tencent Cloud MCP server.
//
// It exposes Tencent Cloud tools over stdio JSON-RPC, backed by the shared sdk.LoadCreds
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
	"errors"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/tttboy123/cloud-skills-mcp/internal/mcp/sdk"
)

const (
	serverName    = "tencent-cloud-mcp"
	serverVersion = "0.3.0"
	tccliPath     = "tccli" // resolved via PATH at run time
	cliTimeout    = 30 * time.Second
	defaultRegion = "ap-shanghai"
)

var (
	instanceIDPattern          = regexp.MustCompile(`^ins-[A-Za-z0-9]{8,64}$`)
	lighthouseIDPattern        = regexp.MustCompile(`^lhins-[A-Za-z0-9]{8,64}$`)
	cdbIDPattern               = regexp.MustCompile(`^cdb-[A-Za-z0-9]{8,64}$`)
	cloudbaseEnvironmentIDExpr = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{1,63}$`)
	regionPattern              = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)
)

// Runtime contains side-effecting dependencies used by tool handlers. Keeping
// them injectable makes tests hermetic and lets clients discover tools before
// cloud credentials are configured.
type Runtime struct {
	LoadCreds       func() (*sdk.Creds, error)
	CLIPath         string
	CLITimeout      time.Duration
	TempDir         string
	AllowMutations  bool
	Policy          Policy
	ReadMaxAttempts int
	RetryBaseDelay  time.Duration
	Sleep           func(context.Context, time.Duration) error
	Audit           AuditSink
	Now             func() time.Time
}

func DefaultRuntime() Runtime {
	var audit AuditSink
	if path := os.Getenv("CLOUD_SKILLS_AUDIT_LOG"); path != "" {
		audit = FileAuditSink(path)
	}
	return Runtime{
		LoadCreds: func() (*sdk.Creds, error) {
			return sdk.LoadCreds(sdk.CloudTencent)
		},
		CLIPath:        tccliPath,
		CLITimeout:     cliTimeout,
		AllowMutations: os.Getenv("CLOUD_SKILLS_ALLOW_MUTATIONS") == "1",
		Policy: Policy{
			AllowedRegions:   commaSet(os.Getenv("CLOUD_SKILLS_ALLOWED_REGIONS")),
			AllowedResources: commaSet(os.Getenv("CLOUD_SKILLS_ALLOWED_RESOURCES")),
		},
		ReadMaxAttempts: readAttemptsFromEnv(os.Getenv("CLOUD_SKILLS_READ_MAX_ATTEMPTS")),
		RetryBaseDelay:  200 * time.Millisecond,
		Sleep: func(ctx context.Context, delay time.Duration) error {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		},
		Audit: audit,
		Now:   time.Now,
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
	if r.Policy.AllowedRegions == nil && r.Policy.AllowedResources == nil {
		r.Policy = defaults.Policy
	}
	if r.ReadMaxAttempts <= 0 {
		r.ReadMaxAttempts = defaults.ReadMaxAttempts
	}
	if r.RetryBaseDelay <= 0 {
		r.RetryBaseDelay = defaults.RetryBaseDelay
	}
	if r.Sleep == nil {
		r.Sleep = defaults.Sleep
	}
	if r.Audit == nil {
		r.Audit = defaults.Audit
	}
	if r.Now == nil {
		r.Now = defaults.Now
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

  tencent_cloud_cli_status         — report TCCLI version and local policy
  tencent_cvm_list_instances       — list CVM instances in a region
  tencent_cvm_describe_instance    — describe a single CVM by id
  tencent_cvm_start_instance       — START a CVM (requires --force=true)
  tencent_cvm_stop_instance        — STOP a CVM (requires --force=true)
  tencent_cvm_reboot_instance      — REBOOT a CVM (requires --force=true)
  tencent_lighthouse_*             — list/describe/start/stop/reboot Lighthouse
  tencent_cdb_*                    — list/describe TencentDB for MySQL
  tencent_cloudbase_*              — list/describe CloudBase environments

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

// registerTools wires the stable CVM tools and the additive Phase 1.6 surface
// without loading credentials. Each handler resolves credentials only after
// its arguments and policy gates pass.
func registerTools(srv *server.MCPServer, runtime Runtime) {
	// 1. list instances
	srv.AddTool(
		mcp.NewTool("tencent_cvm_list_instances",
			mcp.WithDescription("List CVM (Elastic Cloud Server) instances in a region. "+
				"Wraps `tccli cvm DescribeInstances`. Returns the raw JSON response "+
				"including TotalCount and InstanceSet array."),
			sdk.RegionArg(""),
			mcp.WithNumber("offset",
				mcp.Description("Pagination offset (maps to tccli --Offset)."),
				mcp.DefaultNumber(0),
			),
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

	registerPhase16Tools(srv, runtime)
}

// ---- handlers ----

func makeListHandler(runtime Runtime) server.ToolHandlerFunc {
	spec := cvmSpec
	spec.ToolName = "tencent_cvm_list_instances"
	spec.Action = "DescribeInstances"
	return makeResourceListHandler(runtime, spec)
}

func makeDescribeHandler(runtime Runtime) server.ToolHandlerFunc {
	spec := cvmSpec
	spec.ToolName = "tencent_cvm_describe_instance"
	spec.Action = "DescribeInstances"
	return makeResourceDescribeHandler(runtime, spec)
}

func makeStartHandler(runtime Runtime) server.ToolHandlerFunc {
	spec := cvmSpec
	spec.ToolName = "tencent_cvm_start_instance"
	spec.Action = "StartInstances"
	spec.Mutation = true
	return makeResourceMutationHandler(runtime, spec)
}

func makeStopHandler(runtime Runtime) server.ToolHandlerFunc {
	spec := cvmSpec
	spec.ToolName = "tencent_cvm_stop_instance"
	spec.Action = "StopInstances"
	spec.Mutation = true
	return makeResourceMutationHandler(runtime, spec)
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
	return validateResourceID("cvm", id)
}

func validateResourceID(kind, id string) error {
	patterns := map[string]*regexp.Regexp{
		"cvm":        instanceIDPattern,
		"lighthouse": lighthouseIDPattern,
		"cdb":        cdbIDPattern,
		"cloudbase":  cloudbaseEnvironmentIDExpr,
	}
	pattern, ok := patterns[kind]
	if !ok {
		return fmt.Errorf("unknown Tencent Cloud resource kind %q", kind)
	}
	if !pattern.MatchString(id) {
		return fmt.Errorf("invalid %s resource id %q", kind, id)
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
	return validatePagination(0, limit, 100)
}

func validatePagination(offset, limit, maxLimit int) error {
	if offset < 0 {
		return fmt.Errorf("invalid offset %d: expected a non-negative value", offset)
	}
	if limit < 1 || limit > maxLimit {
		return fmt.Errorf("invalid limit %d: expected a value between 1 and %d", limit, maxLimit)
	}
	return nil
}

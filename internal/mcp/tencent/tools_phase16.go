package tencent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/tttboy123/cloud-skills-mcp/internal/mcp/sdk"
)

type resourceToolSpec struct {
	ToolName    string
	Service     string
	Action      string
	IDArg       string
	IDKind      string
	Description string
	MaxLimit    int
	Mutation    bool
}

var (
	cvmSpec        = resourceToolSpec{Service: "cvm", IDArg: "instance_id", IDKind: "cvm", MaxLimit: 100}
	lighthouseSpec = resourceToolSpec{Service: "lighthouse", IDArg: "instance_id", IDKind: "lighthouse", MaxLimit: 100}
	cdbSpec        = resourceToolSpec{Service: "cdb", IDArg: "instance_id", IDKind: "cdb", MaxLimit: 2000}
	cloudbaseSpec  = resourceToolSpec{Service: "tcb", IDArg: "environment_id", IDKind: "cloudbase", MaxLimit: 100}
)

func registerPhase16Tools(srv *server.MCPServer, runtime Runtime) {
	srv.AddTool(mcp.NewTool("tencent_cloud_cli_status",
		mcp.WithDescription("Report the installed TCCLI version and active local MCP policy without loading cloud credentials."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(false),
	), makeCLIStatusHandler(runtime))

	addMutationTool(srv, runtime, resourceToolSpec{
		ToolName: "tencent_cvm_reboot_instance", Service: "cvm", Action: "RebootInstances",
		IDArg: "instance_id", IDKind: "cvm", Mutation: true,
		Description: "Reboot one CVM instance using the documented RebootInstances API. The API is asynchronous.",
	})

	addListTool(srv, runtime, resourceToolSpec{
		ToolName: "tencent_lighthouse_list_instances", Service: "lighthouse", Action: "DescribeInstances",
		IDArg: "instance_id", IDKind: "lighthouse", MaxLimit: 100,
		Description: "List Lighthouse instances with official Offset/Limit pagination.",
	})
	addDescribeTool(srv, runtime, resourceToolSpec{
		ToolName: "tencent_lighthouse_describe_instance", Service: "lighthouse", Action: "DescribeInstances",
		IDArg: "instance_id", IDKind: "lighthouse",
		Description: "Describe one Lighthouse instance by lhins-* ID.",
	})
	for _, spec := range []resourceToolSpec{
		{ToolName: "tencent_lighthouse_start_instance", Service: "lighthouse", Action: "StartInstances", IDArg: "instance_id", IDKind: "lighthouse", Mutation: true, Description: "Start one stopped Lighthouse instance. The API is asynchronous."},
		{ToolName: "tencent_lighthouse_stop_instance", Service: "lighthouse", Action: "StopInstances", IDArg: "instance_id", IDKind: "lighthouse", Mutation: true, Description: "Gracefully stop one running Lighthouse instance. The API is asynchronous."},
		{ToolName: "tencent_lighthouse_reboot_instance", Service: "lighthouse", Action: "RebootInstances", IDArg: "instance_id", IDKind: "lighthouse", Mutation: true, Description: "Reboot one running Lighthouse instance. The API is asynchronous."},
	} {
		addMutationTool(srv, runtime, spec)
	}

	addListTool(srv, runtime, resourceToolSpec{
		ToolName: "tencent_cdb_list_instances", Service: "cdb", Action: "DescribeDBInstances",
		IDArg: "instance_id", IDKind: "cdb", MaxLimit: 2000,
		Description: "List TencentDB for MySQL instances with official Offset/Limit pagination.",
	})
	addDescribeTool(srv, runtime, resourceToolSpec{
		ToolName: "tencent_cdb_describe_instance", Service: "cdb", Action: "DescribeDBInstances",
		IDArg: "instance_id", IDKind: "cdb",
		Description: "Describe one TencentDB for MySQL instance by cdb-* ID.",
	})

	addListTool(srv, runtime, resourceToolSpec{
		ToolName: "tencent_cloudbase_list_environments", Service: "tcb", Action: "DescribeEnvs",
		IDArg: "environment_id", IDKind: "cloudbase", MaxLimit: 100,
		Description: "List CloudBase environments with official Offset/Limit pagination.",
	})
	addDescribeTool(srv, runtime, resourceToolSpec{
		ToolName: "tencent_cloudbase_describe_environment", Service: "tcb", Action: "DescribeEnvs",
		IDArg: "environment_id", IDKind: "cloudbase",
		Description: "Describe one CloudBase environment by EnvId.",
	})
}

func addListTool(srv *server.MCPServer, runtime Runtime, spec resourceToolSpec) {
	srv.AddTool(mcp.NewTool(spec.ToolName,
		mcp.WithDescription(spec.Description),
		sdk.RegionArg(""),
		mcp.WithNumber("offset", mcp.Description("Pagination offset."), mcp.DefaultNumber(0)),
		mcp.WithNumber("limit", mcp.Description(fmt.Sprintf("Page size between 1 and %d.", spec.MaxLimit)), mcp.DefaultNumber(20)),
		mcp.WithString(spec.IDArg, mcp.Description("Optional exact resource ID filter.")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
	), makeResourceListHandler(runtime, spec))
}

func addDescribeTool(srv *server.MCPServer, runtime Runtime, spec resourceToolSpec) {
	srv.AddTool(mcp.NewTool(spec.ToolName,
		mcp.WithDescription(spec.Description),
		sdk.RegionArg(""),
		mcp.WithString(spec.IDArg, mcp.Description("Exact resource ID."), mcp.Required()),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
	), makeResourceDescribeHandler(runtime, spec))
}

func addMutationTool(srv *server.MCPServer, runtime Runtime, spec resourceToolSpec) {
	srv.AddTool(mcp.NewTool(spec.ToolName,
		mcp.WithDescription(spec.Description+" Requires operator mutation enablement, force=true and host-side human approval."),
		sdk.RegionArg(""),
		mcp.WithString(spec.IDArg, mcp.Description("Exact resource ID."), mcp.Required()),
		sdk.ForceArg(),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithIdempotentHintAnnotation(spec.Action != "RebootInstances"),
		mcp.WithOpenWorldHintAnnotation(true),
	), makeResourceMutationHandler(runtime, spec))
}

func makeCLIStatusHandler(runtime Runtime) server.ToolHandlerFunc {
	runtime = runtime.normalized()
	return func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		out, stderr, err := runTccliProcess(ctx, runtime, &sdk.Creds{}, []string{"--version"})
		if err != nil {
			return sdk.WrapError("tccli --version", err), nil
		}
		version := strings.TrimSpace(string(out))
		if version == "" {
			version = strings.TrimSpace(string(stderr))
		}
		data, err := json.Marshal(map[string]any{
			"cli_path":                     runtime.CLIPath,
			"version":                      version,
			"mutations_enabled":            runtime.AllowMutations,
			"allowed_regions":              sortedSetValues(runtime.Policy.AllowedRegions),
			"allowed_resources_configured": len(runtime.Policy.AllowedResources) > 0,
			"read_max_attempts":            runtime.ReadMaxAttempts,
		})
		if err != nil {
			return sdk.WrapError("tccli --version", err), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

func makeResourceListHandler(runtime Runtime, spec resourceToolSpec) server.ToolHandlerFunc {
	runtime = runtime.normalized()
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		resourceID := req.GetString(spec.IDArg, "")
		if resourceID != "" {
			if err := validateResourceID(spec.IDKind, resourceID); err != nil {
				return sdk.WrapError(spec.Service+"."+spec.Action, err), nil
			}
		}
		offset := req.GetInt("offset", 0)
		limit := req.GetInt("limit", 20)
		if err := validatePagination(offset, limit, spec.MaxLimit); err != nil {
			return sdk.WrapError(spec.Service+"."+spec.Action, err), nil
		}
		requestedRegion := req.GetString("region", "")
		if result := preflightPolicy(runtime, requestedRegion, resourceID, resourceID == ""); result != nil {
			return result, nil
		}
		creds, region, err := loadCredsAndRegion(runtime, requestedRegion)
		if err != nil {
			return sdk.WrapError(spec.Service+"."+spec.Action, err), nil
		}
		if err := runtime.Policy.authorize(region, resourceID, resourceID == ""); err != nil {
			return sdk.WrapError("policy", err), nil
		}
		payload := map[string]any{"Offset": offset, "Limit": limit}
		addResourceFilter(payload, spec, resourceID)
		return executeResourceCall(ctx, runtime, creds, region, spec, payload, true, resourceID)
	}
}

func makeResourceDescribeHandler(runtime Runtime, spec resourceToolSpec) server.ToolHandlerFunc {
	runtime = runtime.normalized()
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		resourceID, err := req.RequireString(spec.IDArg)
		if err != nil {
			return sdk.WrapError(spec.Service+"."+spec.Action, err), nil
		}
		if err := validateResourceID(spec.IDKind, resourceID); err != nil {
			return sdk.WrapError(spec.Service+"."+spec.Action, err), nil
		}
		requestedRegion := req.GetString("region", "")
		if result := preflightPolicy(runtime, requestedRegion, resourceID, false); result != nil {
			return result, nil
		}
		creds, region, err := loadCredsAndRegion(runtime, requestedRegion)
		if err != nil {
			return sdk.WrapError(spec.Service+"."+spec.Action, err), nil
		}
		if err := runtime.Policy.authorize(region, resourceID, false); err != nil {
			return sdk.WrapError("policy", err), nil
		}
		payload := make(map[string]any)
		addResourceFilter(payload, spec, resourceID)
		return executeResourceCall(ctx, runtime, creds, region, spec, payload, true, resourceID)
	}
}

func makeResourceMutationHandler(runtime Runtime, spec resourceToolSpec) server.ToolHandlerFunc {
	runtime = runtime.normalized()
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if result, err := sdk.RequireMutationApproval(runtime.AllowMutations, req.GetBool("force", false)); result != nil {
			return result, err
		}
		resourceID, err := req.RequireString(spec.IDArg)
		if err != nil {
			return sdk.WrapError(spec.Service+"."+spec.Action, err), nil
		}
		if err := validateResourceID(spec.IDKind, resourceID); err != nil {
			return sdk.WrapError(spec.Service+"."+spec.Action, err), nil
		}
		requestedRegion := req.GetString("region", "")
		if result := preflightPolicy(runtime, requestedRegion, resourceID, false); result != nil {
			return result, nil
		}
		creds, region, err := loadCredsAndRegion(runtime, requestedRegion)
		if err != nil {
			return sdk.WrapError(spec.Service+"."+spec.Action, err), nil
		}
		if err := runtime.Policy.authorize(region, resourceID, false); err != nil {
			return sdk.WrapError("policy", err), nil
		}
		if runtime.Audit != nil {
			event := AuditEvent{Time: runtime.Now().UTC(), Tool: spec.ToolName, Service: spec.Service, Action: spec.Action, ResourceID: resourceID, Region: region, Mutation: true, Outcome: "authorized"}
			if err := runtime.Audit(ctx, event); err != nil {
				return sdk.WrapError("audit", err), nil
			}
		}
		payload := map[string]any{"InstanceIds": []string{resourceID}}
		return executeResourceCall(ctx, runtime, creds, region, spec, payload, false, resourceID)
	}
}

func preflightPolicy(runtime Runtime, requestedRegion, resourceID string, unfilteredList bool) *mcp.CallToolResult {
	if requestedRegion != "" {
		if err := validateRegion(requestedRegion); err != nil {
			return sdk.WrapError("policy", err)
		}
	}
	if err := runtime.Policy.authorize(requestedRegion, resourceID, unfilteredList); err != nil {
		return sdk.WrapError("policy", err)
	}
	return nil
}

func addResourceFilter(payload map[string]any, spec resourceToolSpec, resourceID string) {
	if resourceID == "" {
		return
	}
	if spec.IDKind == "cloudbase" {
		payload["EnvId"] = resourceID
		return
	}
	payload["InstanceIds"] = []string{resourceID}
}

func executeResourceCall(ctx context.Context, runtime Runtime, creds *sdk.Creds, region string, spec resourceToolSpec, payload map[string]any, readOnly bool, resourceID string) (*mcp.CallToolResult, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return sdk.WrapError(spec.Service+"."+spec.Action, err), nil
	}
	tempPath, err := writePayload(runtime.TempDir, string(data))
	if err != nil {
		return sdk.WrapError(spec.Service+"."+spec.Action, err), nil
	}
	defer func() { _ = os.Remove(tempPath) }()
	args := []string{spec.Service, spec.Action, "--region", region, "--cli-input-json", "file://" + tempPath}
	out, attempts, err := runTencentAPI(ctx, runtime, creds, spec.Service, spec.Action, args, readOnly)
	if err != nil {
		auditOutcome(ctx, runtime, spec, resourceID, region, "failed", "")
		return tencentToolError(spec.Service, spec.Action, err, attempts), nil
	}
	requestID := requestIDFromResponse(out)
	auditOutcome(ctx, runtime, spec, resourceID, region, "succeeded", requestID)
	return mcp.NewToolResultText(string(out)), nil
}

func auditOutcome(ctx context.Context, runtime Runtime, spec resourceToolSpec, resourceID, region, outcome, requestID string) {
	if runtime.Audit == nil || !spec.Mutation {
		return
	}
	_ = runtime.Audit(ctx, AuditEvent{Time: runtime.Now().UTC(), Tool: spec.ToolName, Service: spec.Service, Action: spec.Action, ResourceID: resourceID, Region: region, Mutation: true, Outcome: outcome, RequestID: requestID})
}

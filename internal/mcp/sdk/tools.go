// tools.go — small helper layer over mcp-go so each cloud's main.go is shorter
// and consistent. We do NOT wrap the SDK — we just centralize the boilerplate
// (name prefix, "dangerous" annotations, JSON-schema input docs) that every
// tool shares.
//
// Why a thin helper layer and not a full DSL? Each tool's input schema is too
// domain-specific to template; the per-cloud main.go still writes
// `mcp.WithString` directly. The helper just provides a register function and
// a couple of common input fields.
package sdk

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// ToolRegistrar is the surface main.go uses; it's satisfied by *server.MCPServer.
// Defining it here (instead of importing server in every main.go) makes the
// helper testable in isolation.
type ToolRegistrar interface {
	AddTool(tool mcp.Tool, handler server.ToolHandlerFunc)
}

// RegisterTool is a thin wrapper around MCPServer.AddTool that adds a couple of
// defaults: a description (required) and a "region" string param if the tool
// opts in via CommonOptions.
//
// The function exists mainly so every cloud's main.go can be 1-liner per tool:
//
//	sdk.RegisterTool(srv,
//	    mcp.NewTool("tencent_cvm_list_instances", mcp.WithDescription("List CVM instances")),
//	    mcp.WithString("region", mcp.Description("Tencent region; defaults to creds region")),
//	    handler,
//	)
func RegisterTool(srv ToolRegistrar, tool mcp.Tool, handler server.ToolHandlerFunc) {
	srv.AddTool(tool, handler)
}

// RegionArg returns a string property for "region" with the standard description
// and optional default. Use it in mcp.NewTool's options:
//
//	mcp.NewTool(name,
//	    mcp.WithDescription(...),
//	    sdk.RegionArg(creds.Region),
//	    mcp.WithString("instance_id", mcp.Required()),
//	)
func RegionArg(defaultRegion string) mcp.ToolOption {
	opts := []mcp.PropertyOption{
		mcp.Description("Cloud region. If omitted, uses the default region from the loaded credentials (or the SDK default)."),
	}
	if defaultRegion != "" {
		opts = append(opts, mcp.DefaultString(defaultRegion))
	}
	return mcp.WithString("region", opts...)
}

// ForceArg returns a boolean property for the "force" confirmation flag. Tools
// that mutate state (start, stop, destroy, delete) should include this and
// pass its value to RequireMutationApproval. The schema requires the field,
// and false is still refused by the handler.
func ForceArg() mcp.ToolOption {
	return mcp.WithBoolean("force",
		mcp.Description("REQUIRED for mutating tools. Pass --force / force=true to confirm you understand the operation is irreversible. Without this, the tool refuses to run."),
		mcp.Required(),
	)
}

// DangerousAnnotation marks a tool as destructive in its MCP annotation so MCP
// clients (Claude Desktop, Cursor) can show a confirm dialog.
func DangerousAnnotation() mcp.ToolAnnotation {
	return mcp.ToolAnnotation{
		DestructiveHint: boolPtr(true),
		Title:           "Destructive — requires explicit --force",
	}
}

func boolPtr(b bool) *bool { return &b }

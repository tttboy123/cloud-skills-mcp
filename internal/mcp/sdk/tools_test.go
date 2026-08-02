package sdk

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type recordingRegistrar struct {
	tool mcp.Tool
}

func (r *recordingRegistrar) AddTool(tool mcp.Tool, _ server.ToolHandlerFunc) {
	r.tool = tool
}

func TestForceArgIsRequired(t *testing.T) {
	tool := mcp.NewTool("mutate", ForceArg())
	if !slices.Contains(tool.InputSchema.Required, "force") {
		t.Fatalf("force must be required in the JSON schema, got required=%v", tool.InputSchema.Required)
	}
}

func TestRegionArgOmitsEmptyDefault(t *testing.T) {
	tool := mcp.NewTool("read", RegionArg(""))
	data, err := json.Marshal(tool.InputSchema.Properties["region"])
	if err != nil {
		t.Fatal(err)
	}
	var property map[string]any
	if err := json.Unmarshal(data, &property); err != nil {
		t.Fatal(err)
	}
	if _, exists := property["default"]; exists {
		t.Fatalf("an unknown credential-derived region must not be advertised as an empty default: %s", data)
	}
}

func TestRegisterToolAndDangerousAnnotation(t *testing.T) {
	registrar := &recordingRegistrar{}
	tool := mcp.NewTool("test")
	RegisterTool(registrar, tool, func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	if registrar.tool.Name != "test" {
		t.Fatalf("tool was not registered: %#v", registrar.tool)
	}
	annotation := DangerousAnnotation()
	if annotation.DestructiveHint == nil || !*annotation.DestructiveHint || annotation.Title == "" {
		t.Fatalf("unexpected dangerous annotation: %#v", annotation)
	}
}

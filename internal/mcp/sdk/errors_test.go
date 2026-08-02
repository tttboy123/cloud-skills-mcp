package sdk

import (
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestWrapAndCLIError(t *testing.T) {
	if result := WrapError("test", nil); !result.IsError || !strings.Contains(result.Content[0].(mcp.TextContent).Text, "nil error") {
		t.Fatalf("unexpected nil error result: %#v", result)
	}
	err := &CLIError{
		CLI:    "tccli",
		Args:   []string{"cvm", "DescribeInstances"},
		Stderr: `SecretKey="secret-value" RequestId=req-123`,
		Code:   7,
	}
	message := err.Error()
	if strings.Contains(message, "secret-value") || !strings.Contains(message, "***REDACTED***") || !strings.Contains(message, "req-123") {
		t.Fatalf("unexpected CLI error: %s", message)
	}
	result := WrapError("call", err)
	if !result.IsError {
		t.Fatalf("expected soft MCP error: %#v", result)
	}
	empty := (&CLIError{CLI: "tccli", Code: 2}).Error()
	if !strings.Contains(empty, "no stderr") {
		t.Fatalf("unexpected empty CLI error: %s", empty)
	}
}

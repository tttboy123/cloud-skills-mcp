package sdk

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestWrapError(t *testing.T) {
	if result := WrapError("test", nil); !result.IsError || !strings.Contains(result.Content[0].(mcp.TextContent).Text, "nil error") {
		t.Fatalf("unexpected nil error result: %#v", result)
	}
	err := fmt.Errorf(`provider failure: SecretKey="secret-value" RequestId=req-123`)
	result := WrapError("call", err)
	message := result.Content[0].(mcp.TextContent).Text
	if !result.IsError || strings.Contains(message, "secret-value") || !strings.Contains(message, "***REDACTED***") || !strings.Contains(message, "req-123") {
		t.Fatalf("expected redacted soft MCP error: %#v", result)
	}
}

func TestMutationApprovalRequiresOperatorGateAndPerCallForce(t *testing.T) {
	result, err := RequireMutationApproval(false, true)
	if result == nil || !result.IsError || err == nil || !errors.Is(err, ErrMutationsDisabled) {
		t.Fatalf("disabled result=%#v err=%v", result, err)
	}
	result, err = RequireMutationApproval(true, false)
	if result == nil || !result.IsError || err == nil || !errors.Is(err, ErrForceRequired) {
		t.Fatalf("force result=%#v err=%v", result, err)
	}
	result, err = RequireMutationApproval(true, true)
	if result != nil || err != nil {
		t.Fatalf("approved result=%#v err=%v", result, err)
	}
}

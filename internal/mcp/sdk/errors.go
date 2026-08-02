// errors.go — translate cloud HTTP/SDK failures into MCP-friendly errors.
//
// MCP doesn't have a rich error code spec; tools return either
//   - mcp.NewToolResultError(text)  — soft error, client sees "isError: true"
//   - a non-nil error from the handler — hard error, JSON-RPC -32603
//
// We use the soft-error path (text) for everything that's the user's fault
// (bad args, missing creds, no permission) and the hard-error path only for
// programmer bugs (nil creds, schema mismatch).
package sdk

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/mark3labs/mcp-go/mcp"
)

// Common sentinel errors so callers can errors.Is against them.
var (
	ErrForceRequired      = errors.New("this operation requires --force confirmation")
	ErrMutationsDisabled  = errors.New("mutating cloud tools are disabled by the operator")
	ErrCredentialsMissing = errors.New("no credentials available for this cloud")
	ErrInvalidRegion      = errors.New("invalid region")
	ErrEmptyInstanceID    = errors.New("instance id is empty")
)

var (
	sensitiveQuotedValue = regexp.MustCompile(`(?i)(["']?(?:secret(?:id|key)?|access[_-]?key(?:id|secret)?|security[_-]?token|private[_-]?key|password|authorization|token|(?:q-|x-amz-|x-goog-)?signature|sig)["']?\s*[:=]\s*["'])([^"']*)(["'])`)
	sensitiveBareValue   = regexp.MustCompile(`(?i)(["']?(?:secret(?:id|key)?|access[_-]?key(?:id|secret)?|security[_-]?token|private[_-]?key|password|authorization|token|(?:q-|x-amz-|x-goog-)?signature|sig)["']?\s*[:=]\s*)([A-Za-z0-9%+/=_-]+)`)
	sensitiveSpaceValue  = regexp.MustCompile(`(?i)(--(?:secret(?:id|key)?|access[_-]?key(?:id|secret)?|security[_-]?token|private[_-]?key|password|authorization|token|(?:q-|x-amz-|x-goog-)?signature|sig)\s+)([^\s\]]+)`)
	bearerValue          = regexp.MustCompile(`(?i)(Bearer\s+)([A-Za-z0-9._~+/=-]+)`)
	rawTencentAKID       = regexp.MustCompile(`AKID[A-Za-z0-9]{8,}`)
	rawAWSAccessKey      = regexp.MustCompile(`(?:AKIA|ASIA)[A-Z0-9]{16}`)
)

// RedactSecret removes values assigned to security-sensitive field names and
// raw Tencent AKIDs. It intentionally preserves unrelated long identifiers
// such as RequestId and temporary paths because operators need them to debug.
func RedactSecret(s string) string {
	out := sensitiveQuotedValue.ReplaceAllString(s, `${1}***REDACTED***${3}`)
	out = sensitiveBareValue.ReplaceAllString(out, `${1}***REDACTED***`)
	out = sensitiveSpaceValue.ReplaceAllString(out, `${1}***REDACTED***`)
	out = bearerValue.ReplaceAllString(out, `${1}***REDACTED***`)
	out = rawTencentAKID.ReplaceAllString(out, "***REDACTED***")
	out = rawAWSAccessKey.ReplaceAllString(out, "***REDACTED***")
	return out
}

// RequireForce is the canonical guard for mutating tools. The tool handler
// calls it first; if force is false, it returns an mcp.NewToolResultError
// and a non-nil error so the caller knows to surface the error.
func RequireForce(force bool) (*mcp.CallToolResult, error) {
	if !force {
		return mcp.NewToolResultError(
				"refusing to run: this tool is destructive and requires --force=true " +
					"(or force: true in the arguments) to confirm."),
			fmt.Errorf("%w (force=false)", ErrForceRequired)
	}
	return nil, nil
}

// RequireMutationApproval enforces two independent gates: the operator must
// enable mutating tools when starting the server, and the individual request
// must still carry force=true. The second gate is not treated as human approval.
func RequireMutationApproval(enabled, force bool) (*mcp.CallToolResult, error) {
	if !enabled {
		return mcp.NewToolResultError(
				"refusing to run: mutating cloud tools are disabled. The operator must restart " +
					"the server with CLOUD_SKILLS_ALLOW_MUTATIONS=1 after explicit approval."),
			fmt.Errorf("%w (CLOUD_SKILLS_ALLOW_MUTATIONS!=1)", ErrMutationsDisabled)
	}
	return RequireForce(force)
}

// WrapError builds a soft MCP error from any error, redacting any embedded
// secret material in the message.
func WrapError(prefix string, err error) *mcp.CallToolResult {
	if err == nil {
		return mcp.NewToolResultError(prefix + ": <nil error>")
	}
	msg := err.Error()
	return mcp.NewToolResultError(RedactSecret(prefix + ": " + msg))
}

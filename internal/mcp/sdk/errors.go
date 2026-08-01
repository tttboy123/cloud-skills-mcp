// errors.go — translate cloud SDK / CLI failures into MCP-friendly errors.
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
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// Common sentinel errors so callers can errors.Is against them.
var (
	ErrForceRequired     = errors.New("this operation requires --force confirmation")
	ErrCredentialsMissing = errors.New("no credentials available for this cloud")
	ErrInvalidRegion     = errors.New("invalid region")
	ErrEmptyInstanceID   = errors.New("instance id is empty")
)

// RedactSecret returns s with anything that looks like an AKSK / API key
// redacted. The regex is intentionally conservative — better to over-redact
// than leak. Used to scrub error messages that originated from cloud SDKs
// (some SDKs echo the request body on auth errors).
//
// The match targets the common shapes:
//   - 32+ char base64-ish (AKID)
//   - 40+ char hex / base64 (SK)
//   - "SecretId":"..." / "secretKey":"..."
func RedactSecret(s string) string {
	out := s
	out = redactField(out, "secretId")
	out = redactField(out, "secretKey")
	out = redactField(out, "SecretId")
	out = redactField(out, "SecretKey")
	out = redactField(out, "AccessKeyId")
	out = redactField(out, "AccessKeySecret")
	out = redactLongToken(out)
	return out
}

func redactField(s, name string) string {
	// Walk the string left-to-right. At each occurrence of `name`, look ahead
	// for the JSON-style `"name":"value"` shape. If matched, replace value
	// with REDACTED. If not, advance past the occurrence and continue.
	for {
		idx := strings.Index(s, name)
		if idx < 0 {
			return s
		}
		// look for the pattern: optional whitespace/colon, then opening quote
		j := idx + len(name)
		// skip past the name itself; if next non-space is not ':' or '"' it's
		// not a JSON field, so just skip past it
		if j >= len(s) {
			return s
		}
		// skip whitespace between name and value
		k := j
		for k < len(s) && (s[k] == ' ' || s[k] == '\t') {
			k++
		}
		// accept "name": or "name"=
		if k < len(s) && s[k] == '=' {
			k++
		} else if k < len(s) && s[k] == ':' {
			k++
		} else {
			// not a key=value / key:value shape; skip past this occurrence
			s = s[:idx] + "X" + s[idx+1:]
			continue
		}
		// skip whitespace
		for k < len(s) && (s[k] == ' ' || s[k] == '\t') {
			k++
		}
		// expect opening quote
		if k >= len(s) || s[k] != '"' {
			s = s[:idx] + "X" + s[idx+1:]
			continue
		}
		start := k + 1
		quote2 := strings.Index(s[start:], `"`)
		if quote2 < 0 {
			return s
		}
		end := start + quote2
		s = s[:start] + "***REDACTED***" + s[end:]
	}
}

// redactLongToken replaces 32+ contiguous [A-Za-z0-9+/=_-] tokens (likely AKID/SK)
// that weren't already caught by redactField. False positives are fine.
func redactLongToken(s string) string {
	var b strings.Builder
	runes := []byte(s)
	i := 0
	for i < len(runes) {
		// start of a token?
		if isTokenByte(runes[i]) {
			j := i
			for j < len(runes) && isTokenByte(runes[j]) {
				j++
			}
			if j-i >= 32 {
				b.WriteString("***REDACTED***")
			} else {
				b.Write(runes[i:j])
			}
			i = j
		} else {
			b.WriteByte(runes[i])
			i++
		}
	}
	return b.String()
}

func isTokenByte(b byte) bool {
	switch {
	case b >= 'A' && b <= 'Z':
		return true
	case b >= 'a' && b <= 'z':
		return true
	case b >= '0' && b <= '9':
		return true
	case b == '+' || b == '/' || b == '=' || b == '_' || b == '-':
		return true
	}
	return false
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

// WrapError builds a soft MCP error from any error, redacting any embedded
// secret material in the message.
func WrapError(prefix string, err error) *mcp.CallToolResult {
	if err == nil {
		return mcp.NewToolResultError(prefix + ": <nil error>")
	}
	msg := err.Error()
	return mcp.NewToolResultError(RedactSecret(prefix + ": " + msg))
}

// CLIError is the structured form of a tccli / aws / gcloud subprocess failure.
// The handler that wraps subprocess.Run builds one of these, then calls WrapError.
type CLIError struct {
	CLI    string // e.g. "tccli"
	Args   []string
	Stderr string
	Stdout string
	Code   int
}

func (e *CLIError) Error() string {
	stderr := strings.TrimSpace(RedactSecret(e.Stderr))
	if stderr == "" {
		stderr = strings.TrimSpace(RedactSecret(e.Stdout))
	}
	if stderr == "" {
		return fmt.Sprintf("%s %v: exit %d (no stderr)", e.CLI, e.Args, e.Code)
	}
	return fmt.Sprintf("%s %v: exit %d: %s", e.CLI, e.Args, e.Code, stderr)
}

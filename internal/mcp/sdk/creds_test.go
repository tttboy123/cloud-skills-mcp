package sdk

import (
	"strings"
	"testing"
)

// TestLoadCreds_TencentKeychain verifies the Phase 1 happy path:
// the user's existing tencent-cloud Keychain entry is found and parsed.
//
// This test will fail on a machine without a `tencent-cloud` Keychain entry;
// that is the intended signal — it means setup-keychain.sh hasn't been run.
func TestLoadCreds_TencentKeychain(t *testing.T) {
	if !IsValidCloud(CloudTencent) {
		t.Fatal("CloudTencent is not registered")
	}
	creds, err := LoadCreds(CloudTencent)
	if err != nil {
		t.Fatalf("LoadCreds(tencent-cloud) failed: %v", err)
	}
	if creds.AccessKeyID == "" {
		t.Fatal("AccessKeyID is empty after LoadCreds")
	}
	// Redact: do not log the actual secret.
	t.Logf("got creds: source=%s region=%s akid-prefix=%s (redacted)",
		creds.Source, creds.Region, redactPrefix(creds.AccessKeyID))
	if creds.Region == "" {
		t.Error("Region is empty (no default applied)")
	}
}

// TestRedactSecret exercises the most common leak vectors.
// The fixture is a fake "tccli" error that contains a real-looking AKID/SK.
func TestRedactSecret(t *testing.T) {
	in := `tccli cvm DescribeInstances failed: {"secretId":"AKID1234567890ABCDEFGHIJKLMNOPQRSTUVWXYZ12","secretKey":"abcdef0123456789ABCDEF0123456789abcdef01"}`
	out := RedactSecret(in)
	for _, leak := range []string{"AKID1234567890", "abcdef0123456789"} {
		if strings.Contains(out, leak) {
			t.Errorf("RedactSecret leaked %q in: %s", leak, out)
		}
	}
	if !strings.Contains(out, "***REDACTED***") {
		t.Errorf("RedactSecret produced no redaction marker in: %s", out)
	}
}

// TestRequireForce verifies the guard.
func TestRequireForce(t *testing.T) {
	res, err := RequireForce(false)
	if res == nil {
		t.Error("expected non-nil CallToolResult when force=false")
	}
	if err == nil {
		t.Error("expected non-nil error when force=false")
	}
	res, err = RequireForce(true)
	if res != nil {
		t.Error("expected nil CallToolResult when force=true")
	}
	if err != nil {
		t.Errorf("expected nil error when force=true, got %v", err)
	}
}

func redactPrefix(s string) string {
	if len(s) <= 4 {
		return s
	}
	return s[:4] + "***"
}

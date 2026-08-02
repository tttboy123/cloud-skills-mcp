package sdk

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadCreds_TencentKeychain verifies the Phase 1 happy path:
// the user's existing tencent-cloud Keychain entry is found and parsed.
//
// This test will fail on a machine without a `tencent-cloud` Keychain entry;
// that is the intended signal — it means setup-keychain.sh hasn't been run.
func TestLoadCreds_TencentKeychain(t *testing.T) {
	if os.Getenv("CLOUD_SKILLS_LIVE_TEST") != "1" {
		t.Skip("set CLOUD_SKILLS_LIVE_TEST=1 to read the real macOS Keychain")
	}
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

func TestLoadFromKeychainRequiresCompleteCredentialPair(t *testing.T) {
	cfg := registry[CloudTencent]
	lookup := func(_, account string) (string, error) {
		if account == cfg.KeychainAccountAKID {
			return "AKID1234567890", nil
		}
		return "", os.ErrNotExist
	}
	if _, err := loadFromKeychainWithLookup(cfg, lookup); err == nil {
		t.Fatal("a keychain entry with an ID but no secret must be rejected")
	}
}

func TestLoadFromKeychainWithCompleteCredentialPair(t *testing.T) {
	cfg := registry[CloudTencent]
	values := map[string]string{
		cfg.KeychainAccountAKID:   "AKID1234567890",
		cfg.KeychainAccountSecret: "test-secret",
		cfg.KeychainAccountRegion: "ap-shanghai",
	}
	lookup := func(_, account string) (string, error) {
		value, ok := values[account]
		if !ok {
			return "", os.ErrNotExist
		}
		return value, nil
	}
	creds, err := loadFromKeychainWithLookup(cfg, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if creds.AccessKeyID == "" || creds.AccessKeySecret == "" || creds.Region != "ap-shanghai" {
		t.Fatalf("unexpected credentials: source=%s region=%s hasID=%v hasSecret=%v", creds.Source, creds.Region, creds.AccessKeyID != "", creds.AccessKeySecret != "")
	}
}

func TestJSONValueTraversesArrays(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"profiles":[{"name":"default","access_key_id":"test-id"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := jsonValue(path, "profiles", "0", "access_key_id")
	if err != nil {
		t.Fatal(err)
	}
	if value != "test-id" {
		t.Fatalf("expected array traversal to return test-id, got %q", value)
	}
}

func TestLoadFromCLIConfigRequiresSecretForKeyPairCloud(t *testing.T) {
	cfg := CloudConfig{
		EnvSecret:     []string{"TEST_SECRET"},
		DefaultRegion: "test-region",
		CLICredPath:   ".test/credentials",
		ParseCLIConfig: func(string) (string, string, string, error) {
			return "test-id", "", "", nil
		},
	}
	if _, err := loadFromCLIConfig(cfg, t.TempDir()); err == nil {
		t.Fatal("key-pair cloud CLI config with no secret must be rejected")
	}
}

// TestRedactSecret exercises the most common leak vectors.
// The fixture is a fake "tccli" error that contains a real-looking AKID/SK.
func TestRedactSecret(t *testing.T) {
	in := `tccli failed: {"secretId":"AKID1234567890ABCDEFGHIJKLMNOPQRSTUVWXYZ12","secretKey":"abcdef0123456789ABCDEF0123456789abcdef01"} --Password plain-secret Bearer oauth.token AKIA1234567890ABCDEF`
	out := RedactSecret(in)
	for _, leak := range []string{"AKID1234567890", "abcdef0123456789", "plain-secret", "oauth.token", "AKIA1234567890ABCDEF"} {
		if strings.Contains(out, leak) {
			t.Errorf("RedactSecret leaked %q in: %s", leak, out)
		}
	}
	if !strings.Contains(out, "***REDACTED***") {
		t.Errorf("RedactSecret produced no redaction marker in: %s", out)
	}
}

func TestRedactSecretPreservesOperationalIdentifiers(t *testing.T) {
	in := `requestId: 36974a26-56f7-4d61-ab17-39107e442a5f file:///tmp/tccli-payload-1234567890.json plus session token for IAM/STS`
	out := RedactSecret(in)
	if out != in {
		t.Fatalf("operational identifiers must remain available for debugging:\nwant: %s\n got: %s", in, out)
	}
}

func TestRequireMutationApproval(t *testing.T) {
	if res, err := RequireMutationApproval(false, true); res == nil || err == nil {
		t.Fatal("mutations must remain disabled even when force=true unless the operator opted in")
	}
	if res, err := RequireMutationApproval(true, false); res == nil || err == nil {
		t.Fatal("force=true must still be required after mutations are enabled")
	}
	if res, err := RequireMutationApproval(true, true); res != nil || err != nil {
		t.Fatalf("enabled mutation with force=true should pass, got result=%v err=%v", res, err)
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

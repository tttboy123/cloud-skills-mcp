package cloud

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultRuntimeLoadsOnlySafetyConfiguration(t *testing.T) {
	t.Setenv("CLOUD_SKILLS_ALLOW_MUTATIONS", "1")
	t.Setenv("CLOUD_SKILLS_ALLOW_SENSITIVE", "1")
	t.Setenv("CLOUD_SKILLS_MAX_OUTPUT_BYTES", "4096")
	t.Setenv("CLOUD_SKILLS_MAX_RESPONSE_FILE_BYTES", "8192")
	t.Setenv("CLOUD_SKILLS_ALLOWED_FILE_ROOTS", strings.Join([]string{"/tmp/one", "/tmp/two"}, string(os.PathListSeparator)))
	t.Setenv("CLOUD_SKILLS_AWS_ALLOWED_ENDPOINT_HOSTS", "new-api.example.aws")
	t.Setenv("CLOUD_SKILLS_AZURE_ALLOWED_ENDPOINT_HOSTS", "new-api.example.microsoft,*.evil.example,127.0.0.1,new-api.example.microsoft")
	t.Setenv("CLOUD_SKILLS_ALIBABA_ALLOWED_ENDPOINT_HOSTS", "new-api.example.alibaba")
	t.Setenv("CLOUD_SKILLS_TENCENT_ALLOWED_ENDPOINT_HOSTS", "new-api.example.tencent")
	t.Setenv("CLOUD_SKILLS_AUDIT_LOG", filepath.Join(t.TempDir(), "audit", "events.jsonl"))
	runtime := DefaultRuntime()
	if !runtime.AllowMutations || !runtime.AllowSensitive || runtime.MaxOutputBytes != 4096 || runtime.MaxResponseFileBytes != 8192 {
		t.Fatalf("runtime safety config=%#v", runtime)
	}
	if strings.Join(runtime.AllowedFileRoots, ",") != "/tmp/one,/tmp/two" || runtime.Audit == nil {
		t.Fatalf("roots/audit not configured: %#v", runtime.AllowedFileRoots)
	}
	if got := runtime.AllowedEndpointHosts[ProviderAzure]; len(got) != 1 || got[0] != "new-api.example.microsoft" {
		t.Fatalf("allowed Azure endpoint hosts=%v", got)
	}
	for provider, want := range map[Provider]string{
		ProviderAWS: "new-api.example.aws", ProviderAlicloud: "new-api.example.alibaba", ProviderTencent: "new-api.example.tencent",
	} {
		got := runtime.AllowedEndpointHosts[provider]
		if len(got) != 1 || got[0] != want {
			t.Fatalf("allowed %s endpoint hosts=%v", provider, got)
		}
	}
	if len(runtime.Adapters) != len(AllProviders()) {
		t.Fatalf("adapters=%d", len(runtime.Adapters))
	}
}

func TestCloudFileAuditSinkWritesMetadataMode0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "events.jsonl")
	sink := FileAuditSink(path)
	event := AuditEvent{
		Time: time.Unix(1, 0).UTC(), Provider: ProviderGCP, Mode: ModeMutate,
		Method: "POST", URL: "https://compute.googleapis.com/v1/projects/p/instances",
		Outcome: "authorized",
	}
	if err := sink(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"provider":"gcp"`) || strings.Contains(string(data), "secret") {
		t.Fatalf("audit=%s", data)
	}
}

func TestHelpTextDocumentsUniversalSurfaceAndCredentialBoundary(t *testing.T) {
	help := HelpText()
	for _, value := range []string{"19 tools", "AWS", "Azure", "Google Cloud", "Alibaba Cloud", "Tencent Cloud", "Baidu", "CLOUD_SKILLS_ALLOW_MUTATIONS", "credentials are never returned", "response_file"} {
		if !strings.Contains(help, value) {
			t.Fatalf("help missing %q", value)
		}
	}
}

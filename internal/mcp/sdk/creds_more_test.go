package sdk

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, root, relative, contents string) string {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRegistryHelpers(t *testing.T) {
	clouds := SupportedClouds()
	for _, cloud := range []Cloud{CloudAWS, CloudAzure, CloudGCP, CloudAlicloud, CloudTencent, CloudBaiducloud} {
		if !slices.Contains(clouds, cloud) || !IsValidCloud(cloud) {
			t.Fatalf("registered cloud missing: %s (all=%v)", cloud, clouds)
		}
	}
	if IsValidCloud("not-a-cloud") {
		t.Fatal("unknown cloud reported as valid")
	}
	if got := joinClouds([]Cloud{CloudTencent, CloudAWS}); got != "tencent-cloud, aws" {
		t.Fatalf("unexpected joined clouds: %q", got)
	}
}

func TestAlibabaCredentialEnvironmentUsesCurrentOfficialNamesBeforeLegacyAliases(t *testing.T) {
	cfg := registry[CloudAlicloud]
	if len(cfg.EnvAKID) < 2 || cfg.EnvAKID[0] != "ALIBABA_CLOUD_ACCESS_KEY_ID" ||
		len(cfg.EnvSecret) < 2 || cfg.EnvSecret[0] != "ALIBABA_CLOUD_ACCESS_KEY_SECRET" ||
		len(cfg.EnvToken) < 2 || cfg.EnvToken[0] != "ALIBABA_CLOUD_SECURITY_TOKEN" ||
		len(cfg.EnvRegion) < 2 || cfg.EnvRegion[0] != "ALIBABA_CLOUD_REGION_ID" {
		t.Fatalf("Alibaba credential environment is stale: %#v", cfg)
	}
	t.Setenv("ALIBABA_CLOUD_ACCESS_KEY_ID", "current-id")
	t.Setenv("ALIBABA_CLOUD_ACCESS_KEY_SECRET", "current-secret")
	t.Setenv("ALIBABA_CLOUD_SECURITY_TOKEN", "current-token")
	t.Setenv("ALIBABA_CLOUD_REGION_ID", "cn-shanghai")
	t.Setenv("ALIBABACLOUD_ACCESS_KEY_ID", "legacy-id")
	t.Setenv("ALIBABACLOUD_ACCESS_KEY_SECRET", "legacy-secret")
	creds, ok := loadFromEnv(cfg)
	if !ok || creds.AccessKeyID != "current-id" || creds.AccessKeySecret != "current-secret" ||
		creds.SecurityToken != "current-token" || creds.Region != "cn-shanghai" {
		t.Fatalf("current Alibaba environment not preferred: %#v ok=%v", creds, ok)
	}
}

func TestLoadCredsFromEnvironmentAndUnknownCloud(t *testing.T) {
	const testCloud Cloud = "test-cloud"
	registry[testCloud] = CloudConfig{
		KeychainService:       "cloud-skills-test-service-that-does-not-exist",
		KeychainAccountAKID:   "id",
		KeychainAccountSecret: "secret",
		EnvAKID:               []string{"CLOUD_SKILLS_TEST_ID"},
		EnvSecret:             []string{"CLOUD_SKILLS_TEST_SECRET"},
		EnvRegion:             []string{"CLOUD_SKILLS_TEST_REGION"},
		DefaultRegion:         "test-region",
		EnvPrefixHint:         "test env",
	}
	t.Cleanup(func() { delete(registry, testCloud) })
	t.Setenv("CLOUD_SKILLS_TEST_ID", "test-id")
	t.Setenv("CLOUD_SKILLS_TEST_SECRET", "test-secret")
	t.Setenv("CLOUD_SKILLS_TEST_REGION", "test-region-2")
	creds, err := LoadCreds(testCloud)
	if err != nil {
		t.Fatal(err)
	}
	if creds.Source != "env" || creds.AccessKeyID != "test-id" || creds.AccessKeySecret != "test-secret" || creds.Region != "test-region-2" {
		t.Fatalf("unexpected env credentials: %#v", creds)
	}
	if _, err := LoadCreds("not-a-cloud"); err == nil || !strings.Contains(err.Error(), "unknown cloud") {
		t.Fatalf("expected an unknown-cloud error, got %v", err)
	}
}

func TestEnvironmentHelpers(t *testing.T) {
	t.Setenv("CLOUD_SKILLS_SECOND", "second")
	if got := firstEnv([]string{"CLOUD_SKILLS_FIRST", "CLOUD_SKILLS_SECOND"}); got != "second" {
		t.Fatalf("unexpected first env: %q", got)
	}
	cfg := CloudConfig{
		EnvAKID:       []string{"ID"},
		EnvSecret:     []string{"SECRET"},
		EnvToken:      []string{"TOKEN"},
		EnvRegion:     []string{"REGION"},
		DefaultRegion: "default-region",
		CLIBinary:     "testcli",
	}
	if got := allEnvNames(cfg); !slices.Equal(got, []string{"ID", "SECRET", "TOKEN", "REGION"}) {
		t.Fatalf("unexpected env names: %v", got)
	}
	if got := cliHint(cfg); got != "`testcli` configure" {
		t.Fatalf("unexpected CLI hint: %q", got)
	}
	if got := cliHint(CloudConfig{}); got != "cloud-specific" {
		t.Fatalf("unexpected empty CLI hint: %q", got)
	}
	if creds, ok := loadFromEnv(cfg); ok || creds != nil {
		t.Fatal("incomplete environment credentials must be rejected")
	}
	t.Setenv("ID", "id")
	t.Setenv("SECRET", "secret")
	creds, ok := loadFromEnv(cfg)
	if !ok || creds.Region != "default-region" || creds.Source != "env" {
		t.Fatalf("unexpected loaded environment credentials: %#v ok=%v", creds, ok)
	}
}

func TestINIAndJSONHelpers(t *testing.T) {
	root := t.TempDir()
	ini := writeTestFile(t, root, "config.ini", "# comment\n[other]\nkey=no\n[default]\nkey = value\n")
	value, err := iniValue(ini, "default", "key")
	if err != nil || value != "value" {
		t.Fatalf("iniValue=%q err=%v", value, err)
	}
	missing, err := iniValue(ini, "default", "missing")
	if err != nil || missing != "" {
		t.Fatalf("missing ini value=%q err=%v", missing, err)
	}
	jsonPath := writeTestFile(t, root, "config.json", `{"nested":{"value":"ok"},"array":["zero"]}`)
	if value, err := jsonValue(jsonPath, "nested", "value"); err != nil || value != "ok" {
		t.Fatalf("nested json value=%q err=%v", value, err)
	}
	if value, err := jsonValue(jsonPath, "array", "0"); err != nil || value != "zero" {
		t.Fatalf("array json value=%q err=%v", value, err)
	}
	if value, err := jsonValue(jsonPath, "array", "bad"); err != nil || value != "" {
		t.Fatalf("invalid array json value=%q err=%v", value, err)
	}
}

func TestCloudCLIConfigParsers(t *testing.T) {
	home := t.TempDir()
	writeTestFile(t, home, ".aws/credentials", "[default]\naws_access_key_id=aws-id\naws_secret_access_key=aws-secret\n")
	writeTestFile(t, home, ".aws/config", "[default]\nregion=us-west-2\n")
	writeTestFile(t, home, ".azure/azureProfile.json", `{"subscriptions":[{"tenantId":"tenant-id"}]}`)
	writeTestFile(t, home, ".aliyun/config.json", `{"profiles":[{"access_key_id":"ali-id","access_key_secret":"ali-secret","region_id":"cn-shanghai"}]}`)
	writeTestFile(t, home, ".tencentcloud/credentials", "[default]\nsecretId=tc-id\nsecretKey=tc-secret\nregion=ap-beijing\n")
	writeTestFile(t, home, ".bce/config", "[default]\naccess_key_id=bce-id\nsecret_access_key=bce-secret\nregion=cn-bj\n")

	tests := []struct {
		name   string
		parse  func(string) (string, string, string, error)
		id     string
		secret string
		region string
	}{
		{"aws", parseAWSConfig, "aws-id", "aws-secret", "us-west-2"},
		{"azure", parseAzureConfig, "tenant-id", "", ""},
		{"alicloud", parseAlicloudConfig, "ali-id", "ali-secret", "cn-shanghai"},
		{"tencent", parseTencentConfig, "tc-id", "tc-secret", "ap-beijing"},
		{"baidu", parseBaiduConfig, "bce-id", "bce-secret", "cn-bj"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			id, secret, region, err := test.parse(home)
			if err != nil || id != test.id || secret != test.secret || region != test.region {
				t.Fatalf("got id=%q secret=%q region=%q err=%v", id, secret, region, err)
			}
		})
	}
}

func TestParseGCPConfig(t *testing.T) {
	home := t.TempDir()
	writeTestFile(t, home, ".config/gcloud/application_default_credentials.json", `{"client_email":"test@example.com"}`)
	binDir := t.TempDir()
	gcloud := writeTestFile(t, binDir, "gcloud", "#!/bin/sh\necho test-project\n")
	if err := os.Chmod(gcloud, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	id, secret, project, err := parseGCPConfig(home)
	if err != nil || id != "test@example.com" || secret != "" || project != "test-project" {
		t.Fatalf("got id=%q secret=%q project=%q err=%v", id, secret, project, err)
	}
}

func TestShellOut(t *testing.T) {
	out, err := shellOut("/bin/echo", " hello ")
	if err != nil || out != "hello" {
		t.Fatalf("shellOut=%q err=%v", out, err)
	}
	if _, err := shellOut(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing command must fail")
	}
}

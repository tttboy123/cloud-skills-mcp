package cloud

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func DefaultRuntime() Runtime {
	allowedEndpointHosts := map[Provider][]string{
		ProviderAzure: parseAllowedEndpointHosts(os.Getenv("CLOUD_SKILLS_AZURE_ALLOWED_ENDPOINT_HOSTS")),
		ProviderGCP:   parseAllowedEndpointHosts(os.Getenv("CLOUD_SKILLS_GCP_ALLOWED_ENDPOINT_HOSTS")),
		ProviderBaidu: parseAllowedEndpointHosts(os.Getenv("CLOUD_SKILLS_BAIDU_ALLOWED_ENDPOINT_HOSTS")),
	}
	runtime := Runtime{
		Adapters:             DefaultAdaptersWithEndpointHosts(allowedEndpointHosts),
		AllowMutations:       os.Getenv("CLOUD_SKILLS_ALLOW_MUTATIONS") == "1",
		AllowSensitive:       os.Getenv("CLOUD_SKILLS_ALLOW_SENSITIVE") == "1",
		AllowedFileRoots:     filepath.SplitList(os.Getenv("CLOUD_SKILLS_ALLOWED_FILE_ROOTS")),
		AllowedEndpointHosts: allowedEndpointHosts,
		MaxOutputBytes:       defaultOutputSize,
	}
	if value := strings.TrimSpace(os.Getenv("CLOUD_SKILLS_MAX_OUTPUT_BYTES")); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			runtime.MaxOutputBytes = parsed
		}
	}
	if path := strings.TrimSpace(os.Getenv("CLOUD_SKILLS_AUDIT_LOG")); path != "" {
		runtime.Audit = FileAuditSink(path)
	}
	return runtime
}

func parseAllowedEndpointHosts(raw string) []string {
	result := make([]string, 0)
	seen := make(map[string]bool)
	for _, item := range strings.Split(raw, ",") {
		host := strings.ToLower(strings.TrimSpace(item))
		if !validAdditionalEndpointHost(host) || seen[host] {
			continue
		}
		seen[host] = true
		result = append(result, host)
	}
	return result
}

func FileAuditSink(path string) AuditSink {
	return func(ctx context.Context, event AuditEvent) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if strings.TrimSpace(path) == "" {
			return fmt.Errorf("audit path is empty")
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return fmt.Errorf("create audit directory: %w", err)
		}
		file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("open audit log: %w", err)
		}
		defer file.Close()
		if err := file.Chmod(0o600); err != nil {
			return fmt.Errorf("secure audit log: %w", err)
		}
		line, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("encode audit event: %w", err)
		}
		writer := bufio.NewWriter(file)
		if _, err := writer.Write(append(line, '\n')); err != nil {
			return fmt.Errorf("write audit event: %w", err)
		}
		if err := writer.Flush(); err != nil {
			return fmt.Errorf("flush audit event: %w", err)
		}
		if err := file.Sync(); err != nil {
			return fmt.Errorf("sync audit event: %w", err)
		}
		return nil
	}
}

func HelpText() string {
	return `cloud-skills-mcp v0.4.0-dev — universal six-cloud MCP server (stdio JSON-RPC)

Surface: 19 tools
  cloud_provider_status
  <provider>_api_discover
  <provider>_api_read
  <provider>_api_mutate

Providers: AWS, Azure, Google Cloud, Alibaba Cloud, Tencent Cloud, Baidu AI Cloud.

The universal adapters address official provider API operations without adding
one Go tool per resource type. Read calls are enabled by default. Mutation calls
require CLOUD_SKILLS_ALLOW_MUTATIONS=1 and force=true. Credential or secret
operations also require CLOUD_SKILLS_ALLOW_SENSITIVE=1. Host-side human approval
remains authoritative.

Credentials are loaded only through each provider's official environment,
profile, IAM, ADC, service-principal or STS chain; credentials are never returned
in MCP results or written to audit logs.

AWS:       AWS profile/SSO/web identity/AKSK through AWS CLI
Azure:     DefaultAzureCredential (service principal/workload/managed identity) or az login
GCP:       Google Auth Application Default Credentials or gcloud identity fallback
Alibaba:   Alibaba Cloud CLI profile/RAM/ALIBABA_CLOUD_* AKSK or STS
Tencent:   TCCLI profile/CAM/AKSK
Baidu:     BCE_ACCESS_KEY_ID + BCE_SECRET_ACCESS_KEY, optional BCE_SESSION_TOKEN
`
}

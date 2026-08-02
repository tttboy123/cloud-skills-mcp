package cloud

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestLiveSixCloudReadOnly(t *testing.T) {
	if os.Getenv("CLOUD_SKILLS_LIVE_TEST") != "1" {
		t.Skip("set CLOUD_SKILLS_LIVE_TEST=1 to use real provider credentials")
	}
	selected := liveProviderSet(os.Getenv("CLOUD_SKILLS_LIVE_PROVIDERS"))
	runtime := DefaultRuntime()
	var auditLock sync.Mutex
	var auditEvents []AuditEvent
	runtime.Audit = func(_ context.Context, event AuditEvent) error {
		auditLock.Lock()
		defer auditLock.Unlock()
		auditEvents = append(auditEvents, event)
		return nil
	}
	c := newTestClient(t, runtime)
	tests := []struct {
		provider  Provider
		tool      string
		arguments func(*testing.T) map[string]any
	}{
		{ProviderAWS, "aws_api_read", func(*testing.T) map[string]any {
			return map[string]any{"service": "sts", "operation": "get-caller-identity", "region": envDefault("CLOUD_SKILLS_LIVE_AWS_REGION", "us-east-1")}
		}},
		{ProviderAzure, "azure_api_read", func(*testing.T) map[string]any {
			arguments := map[string]any{"method": "GET", "url": "https://management.azure.com/subscriptions?api-version=2020-01-01"}
			if subscription := os.Getenv("CLOUD_SKILLS_LIVE_AZURE_SUBSCRIPTION"); subscription != "" {
				arguments["subscription"] = subscription
			}
			return arguments
		}},
		{ProviderGCP, "gcp_api_read", func(t *testing.T) map[string]any {
			project := strings.TrimSpace(os.Getenv("CLOUD_SKILLS_LIVE_GCP_PROJECT"))
			if project == "" {
				t.Fatal("CLOUD_SKILLS_LIVE_GCP_PROJECT is required for GCP live validation")
			}
			return map[string]any{
				"method": "GET", "project": project,
				"url": "https://cloudresourcemanager.googleapis.com/v1/projects/" + project,
			}
		}},
		{ProviderAlicloud, "alicloud_api_read", func(*testing.T) map[string]any {
			return map[string]any{"service": "sts", "operation": "GetCallerIdentity"}
		}},
		{ProviderTencent, "tencent_api_read", func(*testing.T) map[string]any {
			return map[string]any{"service": "sts", "operation": "GetCallerIdentity", "region": envDefault("CLOUD_SKILLS_LIVE_TENCENT_REGION", "ap-guangzhou")}
		}},
		{ProviderBaidu, "baiducloud_api_read", func(*testing.T) map[string]any {
			return map[string]any{"method": "GET", "url": envDefault("CLOUD_SKILLS_LIVE_BAIDU_URL", "https://bcc.bj.baidubce.com/v2/instance")}
		}},
	}
	for _, test := range tests {
		if len(selected) > 0 && !selected[test.provider] {
			continue
		}
		t.Run(string(test.provider), func(t *testing.T) {
			auditLock.Lock()
			beforeAudit := len(auditEvents)
			auditLock.Unlock()
			status := callCloudTool(t, c, "cloud_provider_status", map[string]any{"provider": string(test.provider)})
			if status.IsError {
				t.Fatalf("provider status: %s", cloudToolText(t, status))
			}
			result := callCloudTool(t, c, test.tool, test.arguments(t))
			if result.IsError {
				t.Fatalf("live read failed: %s", cloudToolText(t, result))
			}
			if text := strings.TrimSpace(cloudToolText(t, result)); text == "" {
				t.Fatal("provider returned an empty response")
			} else {
				auditLock.Lock()
				providerEvents := append([]AuditEvent(nil), auditEvents[beforeAudit:]...)
				auditLock.Unlock()
				var observed *AuditEvent
				for index := range providerEvents {
					event := &providerEvents[index]
					if event.Provider == test.provider && event.Mode == ModeRead && event.Outcome == "succeeded" {
						observed = event
					}
				}
				if observed == nil {
					t.Fatalf("no succeeded read audit event for %s: %#v", test.provider, providerEvents)
				}
				t.Logf("provider=%s outcome=%s response_bytes=%d request_id=%q", test.provider, observed.Outcome, len(text), observed.RequestID)
			}
		})
	}
}

func liveProviderSet(raw string) map[Provider]bool {
	result := make(map[Provider]bool)
	for _, item := range strings.Split(raw, ",") {
		provider := Provider(strings.TrimSpace(item))
		if provider != "" {
			if !isProvider(provider) {
				panic(fmt.Sprintf("unknown CLOUD_SKILLS_LIVE_PROVIDERS value %q", provider))
			}
			result[provider] = true
		}
	}
	return result
}

func envDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

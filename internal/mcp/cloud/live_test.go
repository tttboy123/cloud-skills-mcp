package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
			region := envDefault("CLOUD_SKILLS_LIVE_AWS_REGION", "us-east-1")
			return map[string]any{
				"auth_scheme": "sigv4", "service": "sts", "operation": "get-caller-identity", "region": region,
				"method": "POST", "url": "https://sts." + region + ".amazonaws.com/",
				"headers": map[string]any{"Content-Type": "application/x-www-form-urlencoded"},
				"body":    "Action=GetCallerIdentity&Version=2011-06-15",
			}
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
			region := envDefault("CLOUD_SKILLS_LIVE_ALIBABA_REGION", "cn-hangzhou")
			return map[string]any{
				"auth_scheme": "acs3", "service": "sts", "operation": "GetCallerIdentity", "api_version": "2015-04-01", "region": region,
				"method": "POST", "url": "https://sts." + region + ".aliyuncs.com/",
			}
		}},
		{ProviderTencent, "tencent_api_read", func(*testing.T) map[string]any {
			return map[string]any{
				"auth_scheme": "tc3", "service": "sts", "operation": "GetCallerIdentity", "api_version": "2018-08-13",
				"region": envDefault("CLOUD_SKILLS_LIVE_TENCENT_REGION", "ap-guangzhou"), "method": "POST",
				"url": "https://sts.tencentcloudapi.com/", "body": map[string]any{},
			}
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
			providerStatus, err := parseLiveProviderStatus(cloudToolText(t, status))
			if err != nil {
				t.Fatalf("parse provider status: %v", err)
			}
			t.Logf("provider=%s adapter_available=%t credential_source=%q credential_status=%q", test.provider, providerStatus.Available, providerStatus.CredentialSource, providerStatus.CredentialStatus)
			if !providerStatus.Available {
				t.Fatalf("provider adapter or required local credential material is unavailable: %s", providerStatus.Message)
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

func TestLiveBaiduRTCAgentMutation(t *testing.T) {
	if os.Getenv("CLOUD_SKILLS_LIVE_BAIDU_RTC") != "1" {
		t.Skip("set CLOUD_SKILLS_LIVE_BAIDU_RTC=1 for an explicitly approved billed RTC AI Agent session")
	}
	required := func(name string) string {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			t.Fatalf("%s is required for Baidu RTC live validation", name)
		}
		return value
	}
	appID := required("CLOUD_SKILLS_LIVE_BAIDU_RTC_APP_ID")
	deviceID := required("CLOUD_SKILLS_LIVE_BAIDU_RTC_DEVICE_ID")
	userID := required("CLOUD_SKILLS_LIVE_BAIDU_RTC_USER_ID")
	textQuery := required("CLOUD_SKILLS_LIVE_BAIDU_RTC_TEXT")
	licenseKey := required("BCE_RTC_LICENSE_KEY")
	root := t.TempDir()
	responseFile := filepath.Join(root, "baidu-rtc.ndjson")
	runtime := DefaultRuntime()
	if !runtime.AllowMutations {
		t.Fatal("CLOUD_SKILLS_ALLOW_MUTATIONS=1 is required after explicit approval")
	}
	runtime.AllowedFileRoots = []string{root}
	var auditLock sync.Mutex
	var auditEvents []AuditEvent
	runtime.Audit = func(_ context.Context, event AuditEvent) error {
		auditLock.Lock()
		defer auditLock.Unlock()
		auditEvents = append(auditEvents, event)
		return nil
	}
	c := newTestClient(t, runtime)
	result := callCloudTool(t, c, "baiducloud_api_mutate", map[string]any{
		"force": true, "auth_scheme": "rtc-aiagent-ws", "service": "rtc-aiagent",
		"operation": "RealtimeInteraction", "api_version": "1", "method": "GET",
		"url": "wss://rtc-aiotgw.exp.bcelive.com/v1/realtime",
		"body": map[string]any{
			"app_id": appID, "instance_type": "VoiceChat", "config": map[string]any{},
			"device_id": deviceID, "user_id": userID, "messages": []any{"[T]:" + textQuery},
			"max_messages": 64, "timeout_seconds": 60, "terminal_event": "tts_end",
		},
		"response_file": responseFile,
	})
	if result.IsError {
		t.Fatalf("Baidu RTC live mutation failed: %s", cloudToolText(t, result))
	}
	data, err := os.ReadFile(responseFile)
	if err != nil || len(data) == 0 {
		t.Fatalf("read Baidu RTC live response: bytes=%d err=%v", len(data), err)
	}
	for _, secret := range []string{os.Getenv("BCE_ACCESS_KEY_ID"), os.Getenv("BCE_SECRET_ACCESS_KEY"), os.Getenv("BCE_SESSION_TOKEN"), os.Getenv("BCE_SECURITY_TOKEN"), licenseKey} {
		if secret != "" && (strings.Contains(string(data), secret) || strings.Contains(cloudToolText(t, result), secret)) {
			t.Fatal("Baidu RTC live result leaked operator credential material")
		}
	}
	auditLock.Lock()
	events := append([]AuditEvent(nil), auditEvents...)
	auditLock.Unlock()
	var succeeded *AuditEvent
	for index := range events {
		if events[index].Provider == ProviderBaidu && events[index].Mode == ModeMutate && events[index].Outcome == "succeeded" {
			succeeded = &events[index]
		}
	}
	if succeeded == nil {
		t.Fatalf("no succeeded Baidu RTC mutation audit event: %#v", events)
	}
	t.Logf("provider=baiducloud operation=RealtimeInteraction outcome=succeeded response_bytes=%d request_id=%q", len(data), succeeded.RequestID)
}

func TestLiveAlibabaMQMutation(t *testing.T) {
	if os.Getenv("CLOUD_SKILLS_LIVE_ALIBABA_MQ") != "1" {
		t.Skip("set CLOUD_SKILLS_LIVE_ALIBABA_MQ=1 for an explicitly approved RocketMQ 4.x HTTP consume probe")
	}
	required := func(name string) string {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			t.Fatalf("%s is required for Alibaba RocketMQ live validation", name)
		}
		return value
	}
	endpoint := strings.TrimRight(required("CLOUD_SKILLS_LIVE_ALIBABA_MQ_ENDPOINT"), "/")
	topic := required("CLOUD_SKILLS_LIVE_ALIBABA_MQ_TOPIC")
	consumer := required("CLOUD_SKILLS_LIVE_ALIBABA_MQ_CONSUMER")
	settlement := envDefault("CLOUD_SKILLS_LIVE_ALIBABA_MQ_SETTLEMENT", "release")
	if settlement != "release" && settlement != "acknowledge" {
		t.Fatal("CLOUD_SKILLS_LIVE_ALIBABA_MQ_SETTLEMENT must be release or acknowledge")
	}
	root := t.TempDir()
	responseFile := filepath.Join(root, "alibaba-mq.ndjson")
	runtime := DefaultRuntime()
	if !runtime.AllowMutations {
		t.Fatal("CLOUD_SKILLS_ALLOW_MUTATIONS=1 is required after explicit approval")
	}
	runtime.AllowedFileRoots = []string{root}
	var auditLock sync.Mutex
	var auditEvents []AuditEvent
	runtime.Audit = func(_ context.Context, event AuditEvent) error {
		auditLock.Lock()
		defer auditLock.Unlock()
		auditEvents = append(auditEvents, event)
		return nil
	}
	parameters := map[string]any{"consumer": consumer, "numOfMessages": 1, "waitseconds": 1}
	if instance := strings.TrimSpace(os.Getenv("CLOUD_SKILLS_LIVE_ALIBABA_MQ_INSTANCE")); instance != "" {
		parameters["ns"] = instance
	}
	if tag := strings.TrimSpace(os.Getenv("CLOUD_SKILLS_LIVE_ALIBABA_MQ_TAG")); tag != "" {
		parameters["tag"] = tag
	}
	c := newTestClient(t, runtime)
	result := callCloudTool(t, c, "alicloud_api_mutate", map[string]any{
		"force": true, "auth_scheme": "mq", "service": "rocketmq", "operation": "ConsumeMessages",
		"method": "GET", "url": endpoint + "/topics/" + topic + "/messages", "parameters": parameters,
		"body": map[string]any{"settlement": settlement}, "response_file": responseFile,
	})
	if result.IsError {
		t.Fatalf("Alibaba RocketMQ live mutation failed (stage one matching message before the probe): %s", cloudToolText(t, result))
	}
	data, err := os.ReadFile(responseFile)
	if err != nil || len(data) == 0 {
		t.Fatalf("read Alibaba RocketMQ live response: bytes=%d err=%v", len(data), err)
	}
	combined := string(data) + cloudToolText(t, result)
	if strings.Contains(strings.ToLower(combined), "receipthandle") || strings.Contains(strings.ToLower(combined), "receipt_handle") {
		t.Fatal("Alibaba RocketMQ live result leaked an internal receipt handle field")
	}
	for _, secret := range []string{os.Getenv("ALIBABA_CLOUD_ACCESS_KEY_ID"), os.Getenv("ALIBABA_CLOUD_ACCESS_KEY_SECRET"), os.Getenv("ALIBABA_CLOUD_SECURITY_TOKEN")} {
		if secret != "" && strings.Contains(combined, secret) {
			t.Fatal("Alibaba RocketMQ live result leaked operator credential material")
		}
	}
	auditLock.Lock()
	events := append([]AuditEvent(nil), auditEvents...)
	auditLock.Unlock()
	var succeeded *AuditEvent
	for index := range events {
		if events[index].Provider == ProviderAlicloud && events[index].Mode == ModeMutate && events[index].Operation == "ConsumeMessages" && events[index].Outcome == "succeeded" {
			succeeded = &events[index]
		}
	}
	if succeeded == nil {
		t.Fatalf("no succeeded Alibaba RocketMQ mutation audit event: %#v", events)
	}
	t.Logf("provider=alicloud operation=ConsumeMessages settlement=%s outcome=succeeded response_bytes=%d request_id=%q", settlement, len(data), succeeded.RequestID)
}

func TestLiveTencentCLSReadOnly(t *testing.T) {
	if os.Getenv("CLOUD_SKILLS_LIVE_TENCENT_CLS") != "1" {
		t.Skip("set CLOUD_SKILLS_LIVE_TENCENT_CLS=1 for a real CLS q-sign read probe")
	}
	required := func(name string) string {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			t.Fatalf("%s is required for Tencent CLS live validation", name)
		}
		return value
	}
	endpoint := strings.TrimRight(required("CLOUD_SKILLS_LIVE_TENCENT_CLS_ENDPOINT"), "/")
	logsetID := required("CLOUD_SKILLS_LIVE_TENCENT_CLS_LOGSET_ID")
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
	result := callCloudTool(t, c, "tencent_api_read", map[string]any{
		"auth_scheme": "cls", "service": "cls", "operation": "GetLogset",
		"method": "GET", "url": endpoint + "/logset", "parameters": map[string]any{"logset_id": logsetID},
	})
	if result.IsError {
		t.Fatalf("Tencent CLS live read failed: %s", cloudToolText(t, result))
	}
	text := strings.TrimSpace(cloudToolText(t, result))
	if text == "" {
		t.Fatal("Tencent CLS returned an empty response")
	}
	for _, secret := range []string{os.Getenv("TENCENTCLOUD_SECRET_ID"), os.Getenv("TENCENTCLOUD_SECRET_KEY"), os.Getenv("TENCENTCLOUD_SESSION_TOKEN"), os.Getenv("TENCENTCLOUD_TOKEN")} {
		if secret != "" && strings.Contains(text, secret) {
			t.Fatal("Tencent CLS live result leaked operator credential material")
		}
	}
	auditLock.Lock()
	events := append([]AuditEvent(nil), auditEvents...)
	auditLock.Unlock()
	var succeeded *AuditEvent
	for index := range events {
		if events[index].Provider == ProviderTencent && events[index].Mode == ModeRead && events[index].Operation == "GetLogset" && events[index].Outcome == "succeeded" {
			succeeded = &events[index]
		}
	}
	if succeeded == nil {
		t.Fatalf("no succeeded Tencent CLS read audit event: %#v", events)
	}
	t.Logf("provider=tencent operation=GetLogset outcome=succeeded response_bytes=%d request_id=%q", len(text), succeeded.RequestID)
}

func parseLiveProviderStatus(text string) (ProviderStatus, error) {
	var status ProviderStatus
	if err := json.Unmarshal([]byte(text), &status); err != nil {
		return ProviderStatus{}, fmt.Errorf("decode provider status: %w", err)
	}
	if !isProvider(status.Provider) {
		return ProviderStatus{}, fmt.Errorf("provider status returned unknown provider %q", status.Provider)
	}
	return status, nil
}

func TestParseLiveProviderStatus(t *testing.T) {
	status, err := parseLiveProviderStatus(`{"provider":"aws","available":true,"credential_source":"profile-or-sso","credential_status":"unverified"}`)
	if err != nil || status.Provider != ProviderAWS || !status.Available || status.CredentialStatus != CredentialStatusUnverified {
		t.Fatalf("status=%#v err=%v", status, err)
	}
	if _, err := parseLiveProviderStatus(`not-json`); err == nil {
		t.Fatal("invalid provider status JSON accepted")
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

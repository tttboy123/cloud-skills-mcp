package cloud

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLiveAWSIoTMQTTMutation(t *testing.T) {
	if os.Getenv("CLOUD_SKILLS_LIVE_AWS_IOT_MQTT") != "1" {
		t.Skip("set CLOUD_SKILLS_LIVE_AWS_IOT_MQTT=1 for an explicitly approved IAM MQTT publish/subscribe session")
	}
	required := func(name string) string {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			t.Fatalf("%s is required for AWS IoT MQTT live validation", name)
		}
		return value
	}
	endpoint := required("CLOUD_SKILLS_LIVE_AWS_IOT_MQTT_ENDPOINT")
	region := required("CLOUD_SKILLS_LIVE_AWS_IOT_MQTT_REGION")
	topic := required("CLOUD_SKILLS_LIVE_AWS_IOT_MQTT_TOPIC")
	clientID := required("CLOUD_SKILLS_LIVE_AWS_IOT_MQTT_CLIENT_ID")
	payload := []byte("cloud-skills-live-" + clientID)
	root := t.TempDir()
	responseFile := filepath.Join(root, "aws-iot-mqtt.ndjson")
	body := map[string]any{
		"protocol_version": 5, "client_id": clientID, "clean_start": true,
		"session_expiry_seconds": 60, "disconnect_session_expiry_seconds": 0,
		"subscriptions": []any{map[string]any{"topic_filter": topic, "qos": 1}},
		"publishes": []any{map[string]any{
			"topic": topic, "qos": 1, "payload_base64": base64.StdEncoding.EncodeToString(payload),
			"payload_format": 1, "content_type": "text/plain", "message_expiry_seconds": 60,
			"user_properties": []any{map[string]any{"name": "source", "value": "cloud-skills-live"}},
		}},
		"will": map[string]any{
			"topic": topic, "qos": 1, "payload_base64": base64.StdEncoding.EncodeToString([]byte("unexpected-disconnect")),
			"payload_format": 1, "content_type": "text/plain", "message_expiry_seconds": 60,
		},
		"max_messages": 1, "timeout_seconds": 60,
	}
	invocation := Invocation{
		Provider: ProviderAWS, Mode: ModeMutate, AuthScheme: authSchemeAWSIoTMQTTWS,
		Service: awsIoTMQTTService, Operation: "ClientMQTT", Region: region,
		Method: http.MethodGet, URL: endpoint, Body: body,
		ResponseFile: responseFile, MaxResponseFileBytes: 1024 * 1024,
	}
	if err := validateAWSIoTMQTTWebSocketInvocation(invocation, nil); err != nil {
		t.Fatalf("AWS IoT MQTT live endpoint or client plan is invalid: %v", err)
	}
	runtime := DefaultRuntime()
	runtime.AllowMutations = true
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
	result := callCloudTool(t, c, "aws_api_mutate", map[string]any{
		"force": true, "auth_scheme": authSchemeAWSIoTMQTTWS, "service": awsIoTMQTTService,
		"operation": "ClientMQTT", "region": region, "method": http.MethodGet,
		"url": endpoint, "body": body, "response_file": responseFile,
	})
	if result.IsError {
		t.Fatalf("AWS IoT MQTT live mutation failed: %s", cloudToolText(t, result))
	}
	data, err := os.ReadFile(responseFile)
	if err != nil || len(data) == 0 || !strings.Contains(string(data), base64.StdEncoding.EncodeToString(payload)) {
		t.Fatalf("read AWS IoT MQTT live response: bytes=%d matched=%t err=%v", len(data), strings.Contains(string(data), base64.StdEncoding.EncodeToString(payload)), err)
	}
	resultText := cloudToolText(t, result)
	for _, secret := range []string{os.Getenv("AWS_ACCESS_KEY_ID"), os.Getenv("AWS_SECRET_ACCESS_KEY"), os.Getenv("AWS_SESSION_TOKEN"), "X-Amz-Signature"} {
		if secret != "" && (strings.Contains(string(data), secret) || strings.Contains(resultText, secret)) {
			t.Fatal("AWS IoT MQTT live result leaked IAM credential or presign material")
		}
	}
	auditLock.Lock()
	events := append([]AuditEvent(nil), auditEvents...)
	auditLock.Unlock()
	var succeeded *AuditEvent
	for index := range events {
		if events[index].Provider == ProviderAWS && events[index].Mode == ModeMutate && events[index].Operation == "ClientMQTT" && events[index].AuthScheme == authSchemeAWSIoTMQTTWS && events[index].Outcome == "succeeded" {
			succeeded = &events[index]
		}
	}
	if succeeded == nil {
		t.Fatalf("no succeeded AWS IoT MQTT mutation audit event: %#v", events)
	}
	t.Logf("provider=aws service=iotdevicegateway operation=ClientMQTT outcome=succeeded response_bytes=%d request_id=%q", len(data), succeeded.RequestID)
}

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

func TestLiveBaiduCCRReadOnly(t *testing.T) {
	if os.Getenv("CLOUD_SKILLS_LIVE_BAIDU_CCR") != "1" {
		t.Skip("set CLOUD_SKILLS_LIVE_BAIDU_CCR=1 for a real BCE AKSK-backed CCR Registry ListTags probe")
	}
	required := func(name string) string {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			t.Fatalf("%s is required for Baidu CCR live validation", name)
		}
		return value
	}
	endpoint := strings.TrimRight(required("CLOUD_SKILLS_LIVE_BAIDU_CCR_ENDPOINT"), "/")
	repository := required("CLOUD_SKILLS_LIVE_BAIDU_CCR_REPOSITORY")
	arguments := map[string]any{
		"auth_scheme": "ccr-registry", "service": "ccr", "operation": "ListTags",
		"method": "GET", "url": endpoint + "/v2/" + repository + "/tags/list", "parameters": map[string]any{"n": 1},
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed == nil || parsed.Scheme != "https" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Port() != "" {
		t.Fatal("CLOUD_SKILLS_LIVE_BAIDU_CCR_ENDPOINT must be a valid HTTPS Registry origin")
	}
	region, instanceID, userID := "", "", ""
	if strings.EqualFold(parsed.Hostname(), baiduCCRPersonalHost) {
		// Personal CCR resolves its current username and one-hour token directly from BCE AKSK.
	} else {
		region = required("CLOUD_SKILLS_LIVE_BAIDU_CCR_REGION")
		instanceID = required("CLOUD_SKILLS_LIVE_BAIDU_CCR_INSTANCE_ID")
		userID = required("CLOUD_SKILLS_LIVE_BAIDU_CCR_USER_ID")
		arguments["region"] = region
		arguments["registry_instance_id"] = instanceID
		arguments["registry_user_id"] = userID
	}
	allowedHosts := parseAllowedEndpointHosts(os.Getenv("CLOUD_SKILLS_BAIDU_ALLOWED_ENDPOINT_HOSTS"))
	invocation := Invocation{Provider: ProviderBaidu, Mode: ModeRead, AuthScheme: authSchemeBaiduCCR, Service: "ccr", Operation: "ListTags", Region: region, RegistryInstanceID: instanceID, RegistryUserID: userID, Method: http.MethodGet, URL: endpoint + "/v2/" + repository + "/tags/list", Parameters: map[string]any{"n": 1}}
	if validateBaiduCCRInvocation(invocation, allowedHosts) != nil {
		t.Fatal("Baidu CCR live endpoint, identifiers, or namespaced repository are invalid")
	}
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
	result := callCloudTool(t, c, "baiducloud_api_read", arguments)
	if result.IsError {
		t.Fatalf("Baidu CCR live read failed: %s", cloudToolText(t, result))
	}
	text := strings.TrimSpace(cloudToolText(t, result))
	if text == "" {
		t.Fatal("Baidu CCR returned an empty response")
	}
	for _, secret := range []string{os.Getenv("BCE_ACCESS_KEY_ID"), os.Getenv("BCE_SECRET_ACCESS_KEY"), os.Getenv("BCE_SESSION_TOKEN"), os.Getenv("BCE_SECURITY_TOKEN")} {
		if secret != "" && strings.Contains(text, secret) {
			t.Fatal("Baidu CCR live result leaked operator credential material")
		}
	}
	auditLock.Lock()
	events := append([]AuditEvent(nil), auditEvents...)
	auditLock.Unlock()
	var succeeded *AuditEvent
	for index := range events {
		if events[index].Provider == ProviderBaidu && events[index].Mode == ModeRead && events[index].Operation == "ListTags" && events[index].AuthScheme == authSchemeBaiduCCR && events[index].Outcome == "succeeded" {
			succeeded = &events[index]
		}
	}
	if succeeded == nil {
		t.Fatalf("no succeeded Baidu CCR read audit event: %#v", events)
	}
	t.Logf("provider=baiducloud service=ccr operation=ListTags outcome=succeeded response_bytes=%d request_id=%q", len(text), succeeded.RequestID)
}

func TestLiveBaiduIoTCoreMQTTReadOnly(t *testing.T) {
	if os.Getenv("CLOUD_SKILLS_LIVE_BAIDU_IOTCORE_MQTT") != "1" {
		t.Skip("set CLOUD_SKILLS_LIVE_BAIDU_IOTCORE_MQTT=1 for a real IAM application-permission MQTT WSS subscription")
	}
	required := func(name string) string {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			t.Fatalf("%s is required for Baidu IoT Core MQTT live validation", name)
		}
		return value
	}
	if strings.TrimSpace(os.Getenv("BCE_SESSION_TOKEN")) != "" || strings.TrimSpace(os.Getenv("BCE_SECURITY_TOKEN")) != "" {
		t.Fatal("Baidu IoT Core application-permission MQTT documents IAM AK/SK only; clear BCE session-token variables for this live gate")
	}
	endpoint := required("CLOUD_SKILLS_LIVE_BAIDU_IOTCORE_MQTT_ENDPOINT")
	topic := required("CLOUD_SKILLS_LIVE_BAIDU_IOTCORE_MQTT_TOPIC")
	clientID := required("CLOUD_SKILLS_LIVE_BAIDU_IOTCORE_MQTT_CLIENT_ID")
	root := t.TempDir()
	responseFile := filepath.Join(root, "baidu-iotcore-mqtt.ndjson")
	invocation := Invocation{
		Provider: ProviderBaidu, Mode: ModeRead, AuthScheme: authSchemeBaiduIoTCoreMQTTWS,
		Service: "iotcore", Operation: "SubscribeMQTT", Method: http.MethodGet, URL: endpoint,
		Body: map[string]any{
			"protocol_version": 5, "client_id": clientID,
			"subscriptions": []any{map[string]any{"topic_filter": topic, "qos": 2}},
			"max_messages":  1, "timeout_seconds": 60,
		},
		ResponseFile: responseFile, MaxResponseFileBytes: 1024 * 1024,
	}
	if err := validateBaiduIoTCoreMQTTInvocation(invocation); err != nil {
		t.Fatalf("Baidu IoT Core live endpoint or subscription plan is invalid: %v", err)
	}
	runtime := DefaultRuntime()
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
	result := callCloudTool(t, c, "baiducloud_api_read", map[string]any{
		"auth_scheme": "iotcore-mqtt-ws", "service": "iotcore", "operation": "SubscribeMQTT",
		"method": "GET", "url": endpoint,
		"body": map[string]any{
			"protocol_version": 5, "client_id": clientID,
			"subscriptions": []any{map[string]any{"topic_filter": topic, "qos": 2}},
			"max_messages":  1, "timeout_seconds": 60,
		},
		"response_file": responseFile,
	})
	if result.IsError {
		t.Fatalf("Baidu IoT Core MQTT live read failed: %s", cloudToolText(t, result))
	}
	data, err := os.ReadFile(responseFile)
	if err != nil || len(data) == 0 {
		t.Fatalf("read Baidu IoT Core MQTT live response: bytes=%d err=%v", len(data), err)
	}
	resultText := cloudToolText(t, result)
	for _, secret := range []string{os.Getenv("BCE_ACCESS_KEY_ID"), os.Getenv("BCE_SECRET_ACCESS_KEY"), "bceiam@"} {
		if secret != "" && (strings.Contains(string(data), secret) || strings.Contains(resultText, secret)) {
			t.Fatal("Baidu IoT Core MQTT live result leaked IAM credential material")
		}
	}
	auditLock.Lock()
	events := append([]AuditEvent(nil), auditEvents...)
	auditLock.Unlock()
	var succeeded *AuditEvent
	for index := range events {
		if events[index].Provider == ProviderBaidu && events[index].Mode == ModeRead && events[index].Operation == "SubscribeMQTT" && events[index].AuthScheme == authSchemeBaiduIoTCoreMQTTWS && events[index].Outcome == "succeeded" {
			succeeded = &events[index]
		}
	}
	if succeeded == nil {
		t.Fatalf("no succeeded Baidu IoT Core MQTT read audit event: %#v", events)
	}
	t.Logf("provider=baiducloud service=iotcore operation=SubscribeMQTT outcome=succeeded response_bytes=%d request_id=%q", len(data), succeeded.RequestID)
}

func TestLiveBaiduIoTCoreHTTPPubMutation(t *testing.T) {
	if os.Getenv("CLOUD_SKILLS_LIVE_BAIDU_IOTCORE_HTTP_PUB") != "1" {
		t.Skip("set CLOUD_SKILLS_LIVE_BAIDU_IOTCORE_HTTP_PUB=1 for a real IAM application-permission HTTPS publish")
	}
	required := func(name string) string {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			t.Fatalf("%s is required for Baidu IoT Core HTTP publish live validation", name)
		}
		return value
	}
	if strings.TrimSpace(os.Getenv("BCE_SESSION_TOKEN")) != "" || strings.TrimSpace(os.Getenv("BCE_SECURITY_TOKEN")) != "" {
		t.Fatal("Baidu IoT Core application permission documents IAM AK/SK only; clear BCE session-token variables for this live gate")
	}
	endpoint := required("CLOUD_SKILLS_LIVE_BAIDU_IOTCORE_HTTP_PUB_ENDPOINT")
	topic := required("CLOUD_SKILLS_LIVE_BAIDU_IOTCORE_HTTP_PUB_TOPIC")
	payloadBase64 := required("CLOUD_SKILLS_LIVE_BAIDU_IOTCORE_HTTP_PUB_PAYLOAD_BASE64")
	invocation := Invocation{
		Provider: ProviderBaidu, Mode: ModeMutate, AuthScheme: authSchemeBaiduIoTCoreHTTPPub,
		Service: "iotcore", Operation: "PublishHTTP", Method: http.MethodPost, URL: endpoint,
		Body: map[string]any{"topic": topic, "qos": 1, "payload_base64": payloadBase64},
	}
	if err := validateBaiduIoTCoreHTTPPubInvocation(invocation); err != nil {
		t.Fatalf("Baidu IoT Core HTTP publish live endpoint or payload plan is invalid: %v", err)
	}
	runtime := DefaultRuntime()
	runtime.AllowMutations = true
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
		"auth_scheme": "iotcore-http-pub", "service": "iotcore", "operation": "PublishHTTP",
		"method": "POST", "url": endpoint,
		"body":  map[string]any{"topic": topic, "qos": 1, "payload_base64": payloadBase64},
		"force": true,
	})
	if result.IsError {
		t.Fatalf("Baidu IoT Core HTTP live publish failed: %s", cloudToolText(t, result))
	}
	resultText := cloudToolText(t, result)
	if !strings.Contains(resultText, `"message"`) || !strings.Contains(strings.ToLower(resultText), "ok") {
		t.Fatalf("Baidu IoT Core HTTP publish did not return the documented success response: %s", resultText)
	}
	for _, secret := range []string{os.Getenv("BCE_ACCESS_KEY_ID"), os.Getenv("BCE_SECRET_ACCESS_KEY"), "bceiam@"} {
		if secret != "" && strings.Contains(resultText, secret) {
			t.Fatal("Baidu IoT Core HTTP live result leaked IAM credential material")
		}
	}
	auditLock.Lock()
	events := append([]AuditEvent(nil), auditEvents...)
	auditLock.Unlock()
	var succeeded *AuditEvent
	for index := range events {
		if events[index].Provider == ProviderBaidu && events[index].Mode == ModeMutate && events[index].Operation == "PublishHTTP" && events[index].AuthScheme == authSchemeBaiduIoTCoreHTTPPub && events[index].Outcome == "succeeded" {
			succeeded = &events[index]
		}
	}
	if succeeded == nil {
		t.Fatalf("no succeeded Baidu IoT Core HTTP publish audit event: %#v", events)
	}
	t.Logf("provider=baiducloud service=iotcore operation=PublishHTTP outcome=succeeded response_bytes=%d request_id=%q", len(resultText), succeeded.RequestID)
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

func TestLiveAlibabaACRReadOnly(t *testing.T) {
	if os.Getenv("CLOUD_SKILLS_LIVE_ALIBABA_ACR") != "1" {
		t.Skip("set CLOUD_SKILLS_LIVE_ALIBABA_ACR=1 for a real RAM-backed Enterprise ACR Registry ListTags probe")
	}
	required := func(name string) string {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			t.Fatalf("%s is required for Alibaba Cloud ACR live validation", name)
		}
		return value
	}
	endpoint := strings.TrimRight(required("CLOUD_SKILLS_LIVE_ALIBABA_ACR_ENDPOINT"), "/")
	instanceID := required("CLOUD_SKILLS_LIVE_ALIBABA_ACR_INSTANCE_ID")
	repository := required("CLOUD_SKILLS_LIVE_ALIBABA_ACR_REPOSITORY")
	parsed, err := url.Parse(endpoint)
	if err != nil {
		t.Fatalf("parse Alibaba Cloud ACR endpoint: %v", err)
	}
	matches := alibabaACRRegistryHostPattern.FindStringSubmatch(strings.ToLower(parsed.Hostname()))
	if len(matches) != 4 || parsed.Scheme != "https" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Port() != "" {
		t.Fatal("CLOUD_SKILLS_LIVE_ALIBABA_ACR_ENDPOINT must be an exact Enterprise Edition public or VPC Registry origin")
	}
	if !alibabaACRInstanceIDPattern.MatchString(instanceID) || !azureACRRepositoryPattern.MatchString(repository) {
		t.Fatal("Alibaba Cloud ACR live instance ID or repository is invalid")
	}
	region := matches[3]
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
	result := callCloudTool(t, c, "alicloud_api_read", map[string]any{
		"auth_scheme": "acr-registry", "service": "acr", "operation": "ListTags", "region": region,
		"registry_instance_id": instanceID, "method": "GET", "url": endpoint + "/v2/" + repository + "/tags/list",
		"parameters": map[string]any{"n": 1},
	})
	if result.IsError {
		t.Fatalf("Alibaba Cloud ACR live read failed: %s", cloudToolText(t, result))
	}
	text := strings.TrimSpace(cloudToolText(t, result))
	if text == "" {
		t.Fatal("Alibaba Cloud ACR returned an empty response")
	}
	for _, secret := range []string{os.Getenv("ALIBABA_CLOUD_ACCESS_KEY_ID"), os.Getenv("ALIBABA_CLOUD_ACCESS_KEY_SECRET"), os.Getenv("ALIBABA_CLOUD_SECURITY_TOKEN")} {
		if secret != "" && strings.Contains(text, secret) {
			t.Fatal("Alibaba Cloud ACR live result leaked operator credential material")
		}
	}
	auditLock.Lock()
	events := append([]AuditEvent(nil), auditEvents...)
	auditLock.Unlock()
	var succeeded *AuditEvent
	for index := range events {
		if events[index].Provider == ProviderAlicloud && events[index].Mode == ModeRead && events[index].Operation == "ListTags" && events[index].AuthScheme == authSchemeAlibabaACR && events[index].Outcome == "succeeded" {
			succeeded = &events[index]
		}
	}
	if succeeded == nil || succeeded.RegistryInstanceID != instanceID {
		t.Fatalf("no succeeded Alibaba Cloud ACR read audit event: %#v", events)
	}
	t.Logf("provider=alicloud service=acr operation=ListTags region=%s outcome=succeeded response_bytes=%d request_id=%q", region, len(text), succeeded.RequestID)
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

func TestLiveTencentTCRReadOnly(t *testing.T) {
	if os.Getenv("CLOUD_SKILLS_LIVE_TENCENT_TCR") != "1" {
		t.Skip("set CLOUD_SKILLS_LIVE_TENCENT_TCR=1 for a real CAM-backed Enterprise TCR Registry ListTags probe")
	}
	required := func(name string) string {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			t.Fatalf("%s is required for Tencent Cloud TCR live validation", name)
		}
		return value
	}
	endpoint := strings.TrimRight(required("CLOUD_SKILLS_LIVE_TENCENT_TCR_ENDPOINT"), "/")
	instanceID := required("CLOUD_SKILLS_LIVE_TENCENT_TCR_INSTANCE_ID")
	region := required("CLOUD_SKILLS_LIVE_TENCENT_TCR_REGION")
	repository := required("CLOUD_SKILLS_LIVE_TENCENT_TCR_REPOSITORY")
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed == nil {
		t.Fatal("CLOUD_SKILLS_LIVE_TENCENT_TCR_ENDPOINT must be a valid HTTPS Registry origin")
	}
	host := strings.ToLower(parsed.Hostname())
	allowedHosts := parseAllowedEndpointHosts(os.Getenv("CLOUD_SKILLS_TENCENT_ALLOWED_ENDPOINT_HOSTS"))
	if parsed.Scheme != "https" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Port() != "" || (!validTencentTCRRegistryHost(host) && !isExplicitEndpointHost(host, allowedHosts)) {
		t.Fatal("CLOUD_SKILLS_LIVE_TENCENT_TCR_ENDPOINT must be an exact Enterprise Edition public, VPC, or operator-pinned custom Registry origin")
	}
	if !tencentTCRInstanceIDPattern.MatchString(instanceID) || !identifierPattern.MatchString(region) || !azureACRRepositoryPattern.MatchString(repository) || !strings.Contains(repository, "/") {
		t.Fatal("Tencent Cloud TCR live instance ID, region, or namespaced repository is invalid")
	}
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
		"auth_scheme": "tcr-registry", "service": "tcr", "operation": "ListTags", "region": region,
		"registry_instance_id": instanceID, "method": "GET", "url": endpoint + "/v2/" + repository + "/tags/list",
		"parameters": map[string]any{"n": 1},
	})
	if result.IsError {
		t.Fatalf("Tencent Cloud TCR live read failed: %s", cloudToolText(t, result))
	}
	text := strings.TrimSpace(cloudToolText(t, result))
	if text == "" {
		t.Fatal("Tencent Cloud TCR returned an empty response")
	}
	for _, secret := range []string{os.Getenv("TENCENTCLOUD_SECRET_ID"), os.Getenv("TENCENTCLOUD_SECRET_KEY"), os.Getenv("TENCENTCLOUD_SESSION_TOKEN"), os.Getenv("TENCENTCLOUD_TOKEN")} {
		if secret != "" && strings.Contains(text, secret) {
			t.Fatal("Tencent Cloud TCR live result leaked operator credential material")
		}
	}
	auditLock.Lock()
	events := append([]AuditEvent(nil), auditEvents...)
	auditLock.Unlock()
	var succeeded *AuditEvent
	for index := range events {
		if events[index].Provider == ProviderTencent && events[index].Mode == ModeRead && events[index].Operation == "ListTags" && events[index].AuthScheme == authSchemeTencentTCR && events[index].Outcome == "succeeded" {
			succeeded = &events[index]
		}
	}
	if succeeded == nil || succeeded.RegistryInstanceID != instanceID {
		t.Fatalf("no succeeded Tencent Cloud TCR read audit event: %#v", events)
	}
	t.Logf("provider=tencent service=tcr operation=ListTags region=%s outcome=succeeded response_bytes=%d request_id=%q", region, len(text), succeeded.RequestID)
}

func TestLiveAzureACRReadOnly(t *testing.T) {
	if os.Getenv("CLOUD_SKILLS_LIVE_AZURE_ACR") != "1" {
		t.Skip("set CLOUD_SKILLS_LIVE_AZURE_ACR=1 for a real ACR scoped-token read probe")
	}
	required := func(name string) string {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			t.Fatalf("%s is required for Azure ACR live validation", name)
		}
		return value
	}
	endpoint := strings.TrimRight(required("CLOUD_SKILLS_LIVE_AZURE_ACR_ENDPOINT"), "/")
	repository := required("CLOUD_SKILLS_LIVE_AZURE_ACR_REPOSITORY")
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
	result := callCloudTool(t, c, "azure_api_read", map[string]any{
		"auth_scheme": "acr", "service": "acr", "operation": "ListTags",
		"method": "GET", "url": endpoint + "/v2/" + repository + "/tags/list",
		"parameters": map[string]any{"n": 1}, "acr_scope": "repository:" + repository + ":pull",
	})
	if result.IsError {
		t.Fatalf("Azure ACR live read failed: %s", cloudToolText(t, result))
	}
	text := strings.TrimSpace(cloudToolText(t, result))
	if text == "" {
		t.Fatal("Azure ACR returned an empty response")
	}
	if secret := os.Getenv("AZURE_CLIENT_SECRET"); secret != "" && strings.Contains(text, secret) {
		t.Fatal("Azure ACR live result leaked operator credential material")
	}
	auditLock.Lock()
	events := append([]AuditEvent(nil), auditEvents...)
	auditLock.Unlock()
	var succeeded *AuditEvent
	for index := range events {
		if events[index].Provider == ProviderAzure && events[index].Mode == ModeRead && events[index].Operation == "ListTags" && events[index].Outcome == "succeeded" {
			succeeded = &events[index]
		}
	}
	if succeeded == nil || succeeded.ACRScope != "repository:"+repository+":pull" {
		t.Fatalf("no succeeded Azure ACR read audit event: %#v", events)
	}
	t.Logf("provider=azure operation=ListTags scope=%s outcome=succeeded response_bytes=%d request_id=%q", succeeded.ACRScope, len(text), succeeded.RequestID)
}

func TestLiveAzureSignalRSubscribeReadOnly(t *testing.T) {
	if os.Getenv("CLOUD_SKILLS_LIVE_AZURE_SIGNALR") != "1" {
		t.Skip("set CLOUD_SKILLS_LIVE_AZURE_SIGNALR=1 for a real Entra-backed SignalR Service WSS subscribe probe")
	}
	required := func(name string) string {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			t.Fatalf("%s is required for Azure SignalR live validation", name)
		}
		return value
	}
	endpoint := strings.TrimRight(required("CLOUD_SKILLS_LIVE_AZURE_SIGNALR_ENDPOINT"), "/")
	if _, _, err := parseAzureSignalRTarget(endpoint); err != nil {
		t.Fatalf("invalid Azure SignalR live endpoint: %v", err)
	}
	runtime := DefaultRuntime()
	root := t.TempDir()
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
	responseFile := filepath.Join(root, "signalr-subscribe.ndjson")
	result := callCloudTool(t, c, "azure_api_read", map[string]any{
		"auth_scheme": "signalr-ws", "service": "signalr", "operation": "Subscribe",
		"method": "GET", "url": endpoint, "response_file": responseFile,
		"body": map[string]any{"max_messages": 1, "timeout_seconds": 10},
	})
	if result.IsError {
		t.Fatalf("Azure SignalR live subscribe failed: %s", cloudToolText(t, result))
	}
	output, err := os.ReadFile(responseFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{os.Getenv("AZURE_CLIENT_SECRET"), os.Getenv("AZURE_ACCESS_TOKEN")} {
		if secret != "" && bytes.Contains(output, []byte(secret)) {
			t.Fatal("Azure SignalR live output leaked operator credential material")
		}
	}
	auditLock.Lock()
	events := append([]AuditEvent(nil), auditEvents...)
	auditLock.Unlock()
	var succeeded *AuditEvent
	for index := range events {
		if events[index].Provider == ProviderAzure && events[index].Mode == ModeRead && events[index].Operation == "Subscribe" && events[index].AuthScheme == authSchemeAzureSignalRWS && events[index].Outcome == "succeeded" {
			succeeded = &events[index]
		}
	}
	if succeeded == nil {
		t.Fatalf("no succeeded Azure SignalR read audit event: %#v", events)
	}
	t.Logf("provider=azure service=signalr operation=Subscribe outcome=succeeded response_bytes=%d request_id=%q", len(output), succeeded.RequestID)
}

func TestLiveAzureOpenAIStreamRead(t *testing.T) {
	if os.Getenv("CLOUD_SKILLS_LIVE_AZURE_OPENAI_STREAM") != "1" {
		t.Skip("set CLOUD_SKILLS_LIVE_AZURE_OPENAI_STREAM=1 for a real Entra-backed Azure OpenAI Chat Completions stream probe")
	}
	required := func(name string) string {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			t.Fatalf("%s is required for Azure OpenAI Chat Completions live validation", name)
		}
		return value
	}
	endpoint := strings.TrimRight(required("CLOUD_SKILLS_LIVE_AZURE_OPENAI_STREAM_ENDPOINT"), "/")
	apiVersion := strings.TrimSpace(os.Getenv("CLOUD_SKILLS_LIVE_AZURE_OPENAI_STREAM_API_VERSION"))
	if apiVersion == "" {
		apiVersion = "2024-06-01"
	}
	target, deployment, err := parseAzureOpenAIChatStreamTarget(endpoint)
	if err != nil {
		t.Fatalf("invalid Azure OpenAI Chat Completions live endpoint: %v", err)
	}
	_ = target
	runtime := DefaultRuntime()
	root := t.TempDir()
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
	responseFile := filepath.Join(root, "azure-openai-chat-stream.ndjson")
	result := callCloudTool(t, c, "azure_api_read", map[string]any{
		"auth_scheme": authSchemeAzureOpenAIChatStream, "service": "openai", "operation": "StreamChatCompletions",
		"method": "POST", "url": endpoint, "api_version": apiVersion, "response_file": responseFile,
		"body": map[string]any{
			"model": deployment,
			"messages": []any{
				map[string]any{"role": "system", "content": "Reply with the single word ok."},
				map[string]any{"role": "user", "content": "Hello"},
			},
			"max_events": 1, "timeout_seconds": 30,
		},
	})
	if result.IsError {
		t.Fatalf("Azure OpenAI Chat Completions live read failed: %s", cloudToolText(t, result))
	}
	output, err := os.ReadFile(responseFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{os.Getenv("AZURE_CLIENT_SECRET"), os.Getenv("AZURE_ACCESS_TOKEN")} {
		if secret != "" && bytes.Contains(output, []byte(secret)) {
			t.Fatal("Azure OpenAI Chat Completions live output leaked operator credential material")
		}
	}
	auditLock.Lock()
	events := append([]AuditEvent(nil), auditEvents...)
	auditLock.Unlock()
	var succeeded *AuditEvent
	for index := range events {
		if events[index].Provider == ProviderAzure && events[index].Mode == ModeRead &&
			events[index].Operation == "StreamChatCompletions" && events[index].AuthScheme == authSchemeAzureOpenAIChatStream &&
			events[index].Outcome == "succeeded" {
			succeeded = &events[index]
		}
	}
	if succeeded == nil {
		t.Fatalf("no succeeded Azure OpenAI Chat Completions read audit event: %#v", events)
	}
	t.Logf("provider=azure service=openai operation=StreamChatCompletions outcome=succeeded response_bytes=%d request_id=%q", len(output), succeeded.RequestID)
}

func TestLiveAWSECRReadOnly(t *testing.T) {
	if os.Getenv("CLOUD_SKILLS_LIVE_AWS_ECR") != "1" {
		t.Skip("set CLOUD_SKILLS_LIVE_AWS_ECR=1 for a real private ECR Registry token and ListTags probe")
	}
	runLiveAWSECRRegistryRead(t, false)
}

func TestLiveAWSECRPublicReadOnly(t *testing.T) {
	if os.Getenv("CLOUD_SKILLS_LIVE_AWS_ECR_PUBLIC") != "1" {
		t.Skip("set CLOUD_SKILLS_LIVE_AWS_ECR_PUBLIC=1 for a real ECR Public Registry token and manifest probe")
	}
	runLiveAWSECRRegistryRead(t, true)
}

func TestLiveAWSIVSChatMutation(t *testing.T) {
	if os.Getenv("CLOUD_SKILLS_LIVE_AWS_IVS_CHAT") != "1" {
		t.Skip("set CLOUD_SKILLS_LIVE_AWS_IVS_CHAT=1 for an explicitly approved IVS Chat self-message")
	}
	required := func(name string) string {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			t.Fatalf("%s is required for Amazon IVS Chat live validation", name)
		}
		return value
	}
	region := required("CLOUD_SKILLS_LIVE_AWS_IVS_CHAT_REGION")
	endpoint := strings.TrimRight(required("CLOUD_SKILLS_LIVE_AWS_IVS_CHAT_ENDPOINT"), "/")
	roomARN := required("CLOUD_SKILLS_LIVE_AWS_IVS_CHAT_ROOM_ARN")
	userID := required("CLOUD_SKILLS_LIVE_AWS_IVS_CHAT_USER_ID")
	if err := validateAWSIVSChatEndpoint(endpoint, region); err != nil {
		t.Fatalf("invalid Amazon IVS Chat live endpoint: %v", err)
	}
	runtime := DefaultRuntime()
	if !runtime.AllowMutations {
		t.Fatal("CLOUD_SKILLS_ALLOW_MUTATIONS=1 is required after explicit approval")
	}
	root := t.TempDir()
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
	responseFile := filepath.Join(root, "aws-ivs-chat.ndjson")
	content := fmt.Sprintf("cloud-skills-live-%d", time.Now().UTC().UnixNano())
	result := callCloudTool(t, c, "aws_api_mutate", map[string]any{
		"force": true, "auth_scheme": "ivs-chat-ws", "service": "ivschat", "operation": "ClientChat",
		"region": region, "method": "GET", "url": endpoint, "response_file": responseFile,
		"body": map[string]any{
			"room_identifier": roomARN, "user_id": userID, "session_duration_minutes": 1,
			"messages":     []any{map[string]any{"action": "SEND_MESSAGE", "content": content, "request_id": "cloud-skills-live"}},
			"max_messages": 1, "timeout_seconds": 30,
		},
	})
	if result.IsError {
		t.Fatalf("Amazon IVS Chat live mutation failed: %s", cloudToolText(t, result))
	}
	output, err := os.ReadFile(responseFile)
	if err != nil || !bytes.Contains(output, []byte(content)) || bytes.Contains(output, []byte("tokenExpirationTime")) {
		t.Fatalf("Amazon IVS Chat live output was missing the self-message or unsafe: bytes=%d err=%v", len(output), err)
	}
	for _, secret := range []string{os.Getenv("AWS_ACCESS_KEY_ID"), os.Getenv("AWS_SECRET_ACCESS_KEY"), os.Getenv("AWS_SESSION_TOKEN")} {
		if secret != "" && bytes.Contains(output, []byte(secret)) {
			t.Fatal("Amazon IVS Chat live output leaked operator credential material")
		}
	}
	auditLock.Lock()
	events := append([]AuditEvent(nil), auditEvents...)
	auditLock.Unlock()
	var succeeded *AuditEvent
	for index := range events {
		if events[index].Provider == ProviderAWS && events[index].Mode == ModeMutate && events[index].Operation == "ClientChat" && events[index].AuthScheme == authSchemeAWSIVSChatWS && events[index].Outcome == "succeeded" {
			succeeded = &events[index]
		}
	}
	if succeeded == nil {
		t.Fatalf("no succeeded Amazon IVS Chat mutation audit event: %#v", events)
	}
	t.Logf("provider=aws service=ivschat operation=ClientChat outcome=succeeded response_bytes=%d request_id=%q", len(output), succeeded.RequestID)
}

func TestLiveAWSChimeMessagingSubscribeReadOnly(t *testing.T) {
	if os.Getenv("CLOUD_SKILLS_LIVE_AWS_CHIME_MESSAGING") != "1" {
		t.Skip("set CLOUD_SKILLS_LIVE_AWS_CHIME_MESSAGING=1 for a real SigV4 Chime messaging WebSocket subscribe probe")
	}
	required := func(name string) string {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			t.Fatalf("%s is required for Amazon Chime messaging live validation", name)
		}
		return value
	}
	region := envDefault("CLOUD_SKILLS_LIVE_AWS_CHIME_MESSAGING_REGION", "us-east-1")
	userARN := required("CLOUD_SKILLS_LIVE_AWS_CHIME_MESSAGING_USER_ARN")
	sessionID := strings.TrimSpace(os.Getenv("CLOUD_SKILLS_LIVE_AWS_CHIME_MESSAGING_SESSION_ID"))
	if sessionID == "" {
		sessionID = fmt.Sprintf("cloud-skills-live-%d", time.Now().UTC().UnixNano())
	}
	runtime := DefaultRuntime()
	root := t.TempDir()
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
	responseFile := filepath.Join(root, "chime-messaging.ndjson")
	result := callCloudTool(t, c, "aws_api_read", map[string]any{
		"auth_scheme": "chime-messaging-ws", "service": "chime-messaging", "operation": "SubscribeMessages",
		"region": region, "method": "GET", "url": "wss://data-messaging.chime.aws/connect", "response_file": responseFile,
		"body": map[string]any{
			"user_arn": userARN, "session_id": sessionID,
			"connect_expires_seconds": 60, "max_messages": 1, "timeout_seconds": 10,
		},
	})
	if result.IsError {
		t.Fatalf("Amazon Chime messaging live subscribe failed: %s", cloudToolText(t, result))
	}
	output, err := os.ReadFile(responseFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{os.Getenv("AWS_ACCESS_KEY_ID"), os.Getenv("AWS_SECRET_ACCESS_KEY"), os.Getenv("AWS_SESSION_TOKEN")} {
		if secret != "" && bytes.Contains(output, []byte(secret)) {
			t.Fatal("Amazon Chime messaging live output leaked operator credential material")
		}
	}
	auditLock.Lock()
	events := append([]AuditEvent(nil), auditEvents...)
	auditLock.Unlock()
	var succeeded *AuditEvent
	for index := range events {
		if events[index].Provider == ProviderAWS && events[index].Mode == ModeRead && events[index].Operation == "SubscribeMessages" && events[index].AuthScheme == authSchemeAWSChimeMessagingWS && events[index].Outcome == "succeeded" {
			succeeded = &events[index]
		}
	}
	if succeeded == nil {
		t.Fatalf("no succeeded Amazon Chime messaging read audit event: %#v", events)
	}
	t.Logf("provider=aws service=chime-messaging operation=SubscribeMessages outcome=succeeded response_bytes=%d request_id=%q", len(output), succeeded.RequestID)
}

func runLiveAWSECRRegistryRead(t *testing.T, public bool) {
	t.Helper()
	required := func(name string) string {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			t.Fatalf("%s is required for Amazon ECR live validation", name)
		}
		return value
	}
	endpointName := "CLOUD_SKILLS_LIVE_AWS_ECR_ENDPOINT"
	repositoryName := "CLOUD_SKILLS_LIVE_AWS_ECR_REPOSITORY"
	service := "ecr"
	operation := "ListTags"
	pathSuffix := "/tags/list"
	if public {
		endpointName = "CLOUD_SKILLS_LIVE_AWS_ECR_PUBLIC_ENDPOINT"
		repositoryName = "CLOUD_SKILLS_LIVE_AWS_ECR_PUBLIC_REPOSITORY"
		service = "ecr-public"
		operation = "GetManifest"
		pathSuffix = "/manifests/latest"
	}
	endpoint := strings.TrimRight(required(endpointName), "/")
	repository := required(repositoryName)
	parsed, err := url.Parse(endpoint)
	if err != nil {
		t.Fatalf("parse Amazon ECR endpoint: %v", err)
	}
	target, ok := parseAWSPrivateECRHost(strings.ToLower(parsed.Hostname()))
	if public {
		target, ok = parseAWSPublicECRHost(strings.ToLower(parsed.Hostname()))
	}
	if !ok || parsed.Scheme != "https" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Port() != "" {
		t.Fatalf("%s must be an exact official Amazon ECR Registry origin", endpointName)
	}
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
	arguments := map[string]any{
		"auth_scheme": "ecr", "service": service, "operation": operation, "region": target.Region,
		"method": "GET", "url": endpoint + "/v2/" + repository + pathSuffix,
	}
	if !public {
		arguments["parameters"] = map[string]any{"n": 1}
	}
	result := callCloudTool(t, c, "aws_api_read", arguments)
	if result.IsError {
		t.Fatalf("Amazon ECR live read failed: %s", cloudToolText(t, result))
	}
	text := strings.TrimSpace(cloudToolText(t, result))
	if text == "" {
		t.Fatal("Amazon ECR returned an empty response")
	}
	for _, secret := range []string{os.Getenv("AWS_ACCESS_KEY_ID"), os.Getenv("AWS_SECRET_ACCESS_KEY"), os.Getenv("AWS_SESSION_TOKEN")} {
		if secret != "" && strings.Contains(text, secret) {
			t.Fatal("Amazon ECR live result leaked operator credential material")
		}
	}
	auditLock.Lock()
	events := append([]AuditEvent(nil), auditEvents...)
	auditLock.Unlock()
	var succeeded *AuditEvent
	for index := range events {
		if events[index].Provider == ProviderAWS && events[index].Mode == ModeRead && events[index].Operation == operation && events[index].AuthScheme == authSchemeAWSECR && events[index].Outcome == "succeeded" {
			succeeded = &events[index]
		}
	}
	if succeeded == nil {
		t.Fatalf("no succeeded Amazon ECR read audit event: %#v", events)
	}
	t.Logf("provider=aws service=%s operation=%s outcome=succeeded response_bytes=%d request_id=%q", service, operation, len(text), succeeded.RequestID)
}

func TestLiveGCPArtifactRegistryReadOnly(t *testing.T) {
	if os.Getenv("CLOUD_SKILLS_LIVE_GCP_ARTIFACT_REGISTRY") != "1" {
		t.Skip("set CLOUD_SKILLS_LIVE_GCP_ARTIFACT_REGISTRY=1 for a real ADC-backed Artifact Registry ListTags probe")
	}
	required := func(name string) string {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			t.Fatalf("%s is required for Google Artifact Registry live validation", name)
		}
		return value
	}
	endpoint := strings.TrimRight(required("CLOUD_SKILLS_LIVE_GCP_ARTIFACT_REGISTRY_ENDPOINT"), "/")
	repository := required("CLOUD_SKILLS_LIVE_GCP_ARTIFACT_REGISTRY_REPOSITORY")
	parsed, err := url.Parse(endpoint)
	if err != nil {
		t.Fatalf("parse Google Artifact Registry endpoint: %v", err)
	}
	if _, ok := parseGCPArtifactRegistryHost(strings.ToLower(parsed.Hostname())); !ok || parsed.Scheme != "https" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Port() != "" {
		t.Fatal("CLOUD_SKILLS_LIVE_GCP_ARTIFACT_REGISTRY_ENDPOINT must be an exact official docker.pkg.dev or supported gcr.io origin")
	}
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
	result := callCloudTool(t, c, "gcp_api_read", map[string]any{
		"auth_scheme": "artifact-registry", "service": "artifact-registry", "operation": "ListTags",
		"method": "GET", "url": endpoint + "/v2/" + repository + "/tags/list", "parameters": map[string]any{"n": 1},
	})
	if result.IsError {
		t.Fatalf("Google Artifact Registry live read failed: %s", cloudToolText(t, result))
	}
	text := strings.TrimSpace(cloudToolText(t, result))
	if text == "" {
		t.Fatal("Google Artifact Registry returned an empty response")
	}
	for _, secret := range []string{os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"), os.Getenv("GOOGLE_OAUTH_ACCESS_TOKEN")} {
		if secret != "" && strings.Contains(text, secret) {
			t.Fatal("Google Artifact Registry live result leaked operator credential material")
		}
	}
	auditLock.Lock()
	events := append([]AuditEvent(nil), auditEvents...)
	auditLock.Unlock()
	var succeeded *AuditEvent
	for index := range events {
		if events[index].Provider == ProviderGCP && events[index].Mode == ModeRead && events[index].Operation == "ListTags" && events[index].AuthScheme == authSchemeGCPArtifactRegistry && events[index].Outcome == "succeeded" {
			succeeded = &events[index]
		}
	}
	if succeeded == nil {
		t.Fatalf("no succeeded Google Artifact Registry read audit event: %#v", events)
	}
	t.Logf("provider=gcp service=artifact-registry operation=ListTags outcome=succeeded response_bytes=%d request_id=%q", len(text), succeeded.RequestID)
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

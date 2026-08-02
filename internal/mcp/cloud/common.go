package cloud

import (
	"encoding/json"
	"os"
	"strings"
)

func requestIDFromGenericJSON(data []byte) string {
	var value any
	if json.Unmarshal(data, &value) != nil {
		return ""
	}
	return findRequestID(value)
}

func findRequestID(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := strings.ToLower(strings.ReplaceAll(key, "-", ""))
			if normalized == "requestid" || normalized == "xamznrequestid" || normalized == "request_id" {
				if text, ok := child.(string); ok {
					return text
				}
			}
			if result := findRequestID(child); result != "" {
				return result
			}
		}
	case []any:
		for _, child := range typed {
			if result := findRequestID(child); result != "" {
				return result
			}
		}
	}
	return ""
}

func credentialSource(provider Provider) string {
	groups := map[Provider][]struct {
		name string
		vars []string
	}{
		ProviderAWS: {
			{name: "web-identity", vars: []string{"AWS_WEB_IDENTITY_TOKEN_FILE", "AWS_ROLE_ARN"}},
			{name: "environment-aksk", vars: []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"}},
			{name: "profile-or-sso", vars: []string{"AWS_PROFILE"}},
		},
		ProviderAzure: {
			{name: "workload-identity", vars: []string{"AZURE_TENANT_ID", "AZURE_CLIENT_ID", "AZURE_FEDERATED_TOKEN_FILE"}},
			{name: "service-principal-environment", vars: []string{"AZURE_TENANT_ID", "AZURE_CLIENT_ID", "AZURE_CLIENT_SECRET"}},
		},
		ProviderGCP: {
			{name: "adc-file", vars: []string{"GOOGLE_APPLICATION_CREDENTIALS"}},
		},
		ProviderAlicloud: {
			{name: "oidc-role", vars: []string{"ALIBABA_CLOUD_ROLE_ARN", "ALIBABA_CLOUD_OIDC_PROVIDER_ARN", "ALIBABA_CLOUD_OIDC_TOKEN_FILE"}},
			{name: "environment-aksk", vars: []string{"ALIBABA_CLOUD_ACCESS_KEY_ID", "ALIBABA_CLOUD_ACCESS_KEY_SECRET"}},
		},
		ProviderTencent: {
			{name: "environment-aksk-or-cam", vars: []string{"TENCENTCLOUD_SECRET_ID", "TENCENTCLOUD_SECRET_KEY"}},
		},
		ProviderBaidu: {
			{name: "environment-aksk", vars: []string{"BCE_ACCESS_KEY_ID", "BCE_SECRET_ACCESS_KEY"}},
		},
	}
	for _, group := range groups[provider] {
		complete := true
		for _, name := range group.vars {
			if os.Getenv(name) == "" {
				complete = false
				break
			}
		}
		if complete {
			return group.name
		}
	}
	return "official-provider-chain"
}

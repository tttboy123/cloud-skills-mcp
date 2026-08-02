package sdk

import (
	"fmt"
	"os"
	"strings"
)

// This file holds the per-cloud CloudConfig + tier-3 parsers.
// 6 clouds total: AWS, Azure, GCP, Alicloud, Tencent, Baidu BCE.
//
// Parsers are deliberately minimal — they read only the first profile / first
// account. Multi-profile clouds (AWS) get a $AWS_PROFILE env override.

func init() {
	registry[CloudAWS] = CloudConfig{
		KeychainService:       "aws",
		KeychainAccountAKID:   "aws-access-key-id",
		KeychainAccountSecret: "aws-secret-access-key",
		KeychainAccountToken:  "aws-session-token",
		KeychainAccountRegion: "aws-default-region",
		EnvAKID:               []string{"AWS_ACCESS_KEY_ID"},
		EnvSecret:             []string{"AWS_SECRET_ACCESS_KEY"},
		EnvToken:              []string{"AWS_SESSION_TOKEN"},
		EnvRegion:             []string{"AWS_REGION", "AWS_DEFAULT_REGION"},
		DefaultRegion:         "us-east-1",
		CLIBinary:             "aws",
		CLICredPath:           ".aws/credentials",
		ParseCLIConfig:        parseAWSConfig,
		EnvPrefixHint:         "AWS_ACCESS_KEY_ID + AWS_SECRET_ACCESS_KEY (with optional AWS_REGION)",
	}
	registry[CloudAzure] = CloudConfig{
		KeychainService:       "azure",
		KeychainAccountAKID:   "azure-tenant",
		KeychainAccountSecret: "azure-client-secret",
		KeychainAccountRegion: "azure-subscription",
		EnvAKID:               []string{"AZURE_TENANT_ID"},
		EnvSecret:             []string{"AZURE_CLIENT_SECRET"},
		EnvRegion:             []string{"AZURE_SUBSCRIPTION_ID"},
		DefaultRegion:         "eastus",
		CLIBinary:             "az",
		CLICredPath:           ".azure/azureProfile.json",
		ParseCLIConfig:        parseAzureConfig,
		EnvPrefixHint:         "AZURE_TENANT_ID + AZURE_CLIENT_SECRET (+ AZURE_SUBSCRIPTION_ID)",
	}
	registry[CloudGCP] = CloudConfig{
		KeychainService:       "gcp",
		KeychainAccountAKID:   "gcp-client-email",
		KeychainAccountSecret: "gcp-private-key",
		KeychainAccountRegion: "gcp-project",
		EnvAKID:               []string{"GOOGLE_APPLICATION_CREDENTIALS"}, // path to service-account JSON
		EnvRegion:             []string{"GOOGLE_CLOUD_PROJECT", "GCP_PROJECT"},
		DefaultRegion:         "us-central1",
		CLIBinary:             "gcloud",
		CLICredPath:           "", // GCP uses ADC + gcloud auth, no file parser needed
		ParseCLIConfig:        parseGCPConfig,
		EnvPrefixHint:         "GOOGLE_APPLICATION_CREDENTIALS (path to service-account JSON)",
	}
	registry[CloudAlicloud] = CloudConfig{
		KeychainService:       "alicloud",
		KeychainAccountAKID:   "alicloud-access-key-id",
		KeychainAccountSecret: "alicloud-access-key-secret",
		KeychainAccountToken:  "alicloud-sts-token",
		KeychainAccountRegion: "alicloud-default-region",
		EnvAKID:               []string{"ALIBABA_CLOUD_ACCESS_KEY_ID", "ALIBABACLOUD_ACCESS_KEY_ID"},
		EnvSecret:             []string{"ALIBABA_CLOUD_ACCESS_KEY_SECRET", "ALIBABACLOUD_ACCESS_KEY_SECRET"},
		EnvToken:              []string{"ALIBABA_CLOUD_SECURITY_TOKEN", "ALIBABACLOUD_SECURITY_TOKEN"},
		EnvRegion:             []string{"ALIBABA_CLOUD_REGION_ID", "ALIBABACLOUD_REGION"},
		DefaultRegion:         "cn-hangzhou",
		CLIBinary:             "aliyun",
		CLICredPath:           ".aliyun/config.json",
		ParseCLIConfig:        parseAlicloudConfig,
		EnvPrefixHint:         "ALIBABA_CLOUD_ACCESS_KEY_ID + ALIBABA_CLOUD_ACCESS_KEY_SECRET",
	}
	registry[CloudTencent] = CloudConfig{
		KeychainService:       "tencent-cloud",
		KeychainAccountAKID:   "tccli-secretid",
		KeychainAccountSecret: "tccli-secretkey",
		KeychainAccountToken:  "tccli-token",
		KeychainAccountRegion: "tccli-region",
		EnvAKID:               []string{"TENCENTCLOUD_SECRET_ID"}, // underscores (tccli convention)
		EnvSecret:             []string{"TENCENTCLOUD_SECRET_KEY"},
		EnvToken:              []string{"TENCENTCLOUD_TOKEN"},
		EnvRegion:             []string{"TENCENTCLOUD_REGION"},
		DefaultRegion:         "ap-shanghai",
		CLIBinary:             "tccli",
		CLICredPath:           ".tencentcloud/credentials",
		ParseCLIConfig:        parseTencentConfig,
		EnvPrefixHint:         "TENCENTCLOUD_SECRET_ID + TENCENTCLOUD_SECRET_KEY (with underscores)",
	}
	registry[CloudBaiducloud] = CloudConfig{
		KeychainService:       "baiducloud",
		KeychainAccountAKID:   "bce-access-key-id",
		KeychainAccountSecret: "bce-secret-access-key",
		KeychainAccountRegion: "bce-default-region",
		EnvAKID:               []string{"BCE_ACCESS_KEY_ID"},
		EnvSecret:             []string{"BCE_SECRET_ACCESS_KEY"},
		EnvRegion:             []string{"BCE_REGION"},
		DefaultRegion:         "cn-bj",
		CLIBinary:             "bcecmd",
		CLICredPath:           ".bce/config",
		ParseCLIConfig:        parseBaiduConfig,
		EnvPrefixHint:         "BCE_ACCESS_KEY_ID + BCE_SECRET_ACCESS_KEY",
	}
}

// parseAWSConfig reads ~/.aws/credentials [default] (or $AWS_PROFILE [profile]).
// The file is INI: [default]\naws_access_key_id=...\naws_secret_access_key=...
func parseAWSConfig(home string) (string, string, string, error) {
	path := home + "/.aws/credentials"
	section := "default"
	if p := os.Getenv("AWS_PROFILE"); p != "" {
		section = p
	}
	akid, err := iniValue(path, section, "aws_access_key_id")
	if err != nil {
		return "", "", "", err
	}
	secret, _ := iniValue(path, section, "aws_secret_access_key")
	region, _ := iniValue(home+"/.aws/config", section, "region")
	return akid, secret, region, nil
}

// parseAzureConfig reads ~/.azure/azureProfile.json. Azure's profile format is a
// JSON object with a "subscriptions" array. We extract the first subscription's
// tenantId; the client secret must come from Keychain or env (Azure does not
// store the secret in this file).
func parseAzureConfig(home string) (string, string, string, error) {
	path := home + "/.azure/azureProfile.json"
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", "", err
	}
	// crude tenantId extraction: find the first `"tenantId":"..."` occurrence
	idx := strings.Index(string(data), `"tenantId"`)
	if idx < 0 {
		return "", "", "", fmt.Errorf("no tenantId in azure profile")
	}
	rest := string(data)[idx:]
	colon := strings.Index(rest, ":")
	if colon < 0 {
		return "", "", "", fmt.Errorf("malformed azure profile")
	}
	quote1 := strings.Index(rest[colon:], `"`)
	if quote1 < 0 {
		return "", "", "", fmt.Errorf("malformed azure profile")
	}
	start := colon + quote1 + 1
	quote2 := strings.Index(rest[start:], `"`)
	if quote2 < 0 {
		return "", "", "", fmt.Errorf("malformed azure profile")
	}
	tenant := rest[start : start+quote2]
	return tenant, "", "", nil
}

// parseGCPConfig shells out to gcloud to read the active project + ADC email.
// GCP has no static credentials file in the AWS sense; auth is via
// `gcloud auth application-default login` which writes ADC to
// ~/.config/gcloud/application_default_credentials.json.
func parseGCPConfig(home string) (string, string, string, error) {
	project, err := shellOut("gcloud", "config", "get-value", "project")
	if err != nil {
		return "", "", "", err
	}
	adcPath := home + "/.config/gcloud/application_default_credentials.json"
	clientEmail, err := jsonValue(adcPath, "client_email")
	if err != nil {
		return "", "", "", err
	}
	// The "secret" slot would be the PEM private key; we deliberately don't
	// extract it here — env / Keychain tier is the safe path. Just return project
	// in the region slot.
	return clientEmail, "", project, nil
}

// parseAlicloudConfig reads ~/.aliyun/config.json (JSON, single profile).
// Format: {"current":"default","profiles":[{"name":"default","mode":"AK","access_key_id":"...","access_key_secret":"...","sts_token":"...","region_id":"..."}]}
func parseAlicloudConfig(home string) (string, string, string, error) {
	path := home + "/.aliyun/config.json"
	akid, err := jsonValue(path, "profiles", "0", "access_key_id")
	if err != nil {
		return "", "", "", err
	}
	secret, _ := jsonValue(path, "profiles", "0", "access_key_secret")
	region, _ := jsonValue(path, "profiles", "0", "region_id")
	return akid, secret, region, nil
}

// parseTencentConfig reads ~/.tencentcloud/credentials (INI).
// Format: [default]\nsecretId=...\nsecretKey=... (note: NOT TENCENTCLOUD_SECRET_ID).
func parseTencentConfig(home string) (string, string, string, error) {
	path := home + "/.tencentcloud/credentials"
	akid, err := iniValue(path, "default", "secretId")
	if err != nil {
		return "", "", "", err
	}
	secret, _ := iniValue(path, "default", "secretKey")
	region, _ := iniValue(path, "default", "region")
	return akid, secret, region, nil
}

// parseBaiduConfig reads ~/.bce/config (INI).
// Format: [default]\naccess_key_id=...\nsecret_access_key=...
func parseBaiduConfig(home string) (string, string, string, error) {
	path := home + "/.bce/config"
	akid, err := iniValue(path, "default", "access_key_id")
	if err != nil {
		return "", "", "", err
	}
	secret, _ := iniValue(path, "default", "secret_access_key")
	region, _ := iniValue(path, "default", "region")
	return akid, secret, region, nil
}

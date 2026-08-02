package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSignedHTTPAdaptersExposeHTTPOnlyStatusAndOfficialDiscovery(t *testing.T) {
	adapters := []struct {
		name    string
		adapter Adapter
		marker  string
	}{
		{name: "aws", adapter: NewAWSRESTAdapter(AWSRESTConfig{}), marker: "authentication"},
		{name: "alibaba", adapter: NewAlibabaRESTAdapter(AlibabaRESTConfig{}), marker: "acs3_signature"},
		{name: "tencent", adapter: NewTencentRESTAdapter(TencentRESTConfig{}), marker: "tc3_signature"},
	}
	for _, test := range adapters {
		t.Run(test.name, func(t *testing.T) {
			status, err := test.adapter.Status(t.Context())
			if err != nil || !status.Available || !strings.Contains(strings.ToLower(status.Adapter), "https") || !strings.Contains(strings.ToLower(status.Message), "no cloud cli") {
				t.Fatalf("status=%#v err=%v", status, err)
			}
			output, err := test.adapter.Discover(t.Context(), DiscoveryRequest{})
			if err != nil {
				t.Fatal(err)
			}
			var discovery map[string]string
			if err := json.Unmarshal(output, &discovery); err != nil || discovery[test.marker] == "" {
				t.Fatalf("discovery=%s err=%v", output, err)
			}
		})
	}
}

func TestDefaultSignedCredentialProvidersUseEnvironmentWithoutCloudCLI(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "aws-environment-ak")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "aws-environment-secret")
	t.Setenv("AWS_SESSION_TOKEN", "aws-environment-token")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	awsCredentials, err := (&awsDefaultCredentialProvider{}).Credentials(t.Context())
	if err != nil || awsCredentials.AccessKeyID != "aws-environment-ak" || awsCredentials.SessionToken != "aws-environment-token" {
		t.Fatalf("AWS credentials=%#v err=%v", awsCredentials, err)
	}

	t.Setenv("ALIBABA_CLOUD_ACCESS_KEY_ID", "alibaba-environment-ak")
	t.Setenv("ALIBABA_CLOUD_ACCESS_KEY_SECRET", "alibaba-environment-secret")
	t.Setenv("ALIBABA_CLOUD_SECURITY_TOKEN", "alibaba-environment-token")
	alibabaCredentials, err := (&alibabaDefaultCredentialProvider{}).Credentials(t.Context())
	if err != nil || alibabaCredentials.AccessKeyID != "alibaba-environment-ak" || alibabaCredentials.SecurityToken != "alibaba-environment-token" {
		t.Fatalf("Alibaba credentials=%#v err=%v", alibabaCredentials, err)
	}

	t.Setenv("TENCENTCLOUD_SECRET_ID", "tencent-environment-id")
	t.Setenv("TENCENTCLOUD_SECRET_KEY", "tencent-environment-key")
	t.Setenv("TENCENTCLOUD_SESSION_TOKEN", "tencent-environment-token")
	tencentCredentials, err := (EnvTencentCredentialProvider{}).Credentials(t.Context())
	if err != nil || tencentCredentials.SecretID != "tencent-environment-id" || tencentCredentials.Token != "tencent-environment-token" {
		t.Fatalf("Tencent credentials=%#v err=%v", tencentCredentials, err)
	}
}

func TestSignedHTTPHelpersCoverNoncePointersAndQueryTypes(t *testing.T) {
	nonce := secureNonce()
	if len(nonce) != 32 {
		t.Fatalf("nonce length=%d", len(nonce))
	}
	value := "value"
	if pointerString(nil) != "" || pointerString(&value) != value {
		t.Fatal("pointerString mismatch")
	}
	target, _ := url.Parse("https://service.example/path?existing=1")
	if err := addQueryParameters(target, map[string]any{
		"string": "text", "bool": true, "number": float64(2.5), "int": 3,
		"strings": []string{"a", "b"}, "values": []any{"x", false},
	}); err != nil {
		t.Fatal(err)
	}
	query := target.Query()
	if query.Get("existing") != "1" || query.Get("number") != "2.5" || len(query["strings"]) != 2 || len(query["values"]) != 2 {
		t.Fatalf("query=%v", query)
	}
	for _, parameters := range []map[string]any{{"": "bad"}, {"bad": map[string]any{"x": 1}}, {"bad": []any{[]any{"nested"}}}} {
		if err := addQueryParameters(target, parameters); err == nil {
			t.Fatalf("accepted invalid query=%#v", parameters)
		}
	}
}

type staticAWSCredentialsProvider struct {
	credentials AWSCredentials
}

func (provider staticAWSCredentialsProvider) Credentials(context.Context) (AWSCredentials, error) {
	return provider.credentials, nil
}

type staticAlibabaCredentialsProvider struct {
	credentials AlibabaCredentials
}

func (provider staticAlibabaCredentialsProvider) Credentials(context.Context) (AlibabaCredentials, error) {
	return provider.credentials, nil
}

type staticTencentCredentialsProvider struct {
	credentials TencentCredentials
}

func (provider staticTencentCredentialsProvider) Credentials(context.Context) (TencentCredentials, error) {
	return provider.credentials, nil
}

func TestDefaultAdaptersUseOnlyHTTPImplementations(t *testing.T) {
	adapters := DefaultAdapters()
	want := map[Provider]string{
		ProviderAWS:      "*cloud.AWSRESTAdapter",
		ProviderAzure:    "*cloud.AzureRESTAdapter",
		ProviderGCP:      "*cloud.GCPRESTAdapter",
		ProviderAlicloud: "*cloud.AlibabaRESTAdapter",
		ProviderTencent:  "*cloud.TencentRESTAdapter",
		ProviderBaidu:    "*cloud.BaiduRESTAdapter",
	}
	for provider, adapter := range adapters {
		if got := fmt.Sprintf("%T", adapter); got != want[provider] {
			t.Errorf("provider %s adapter=%s, want %s", provider, got, want[provider])
		}
	}
}

func TestAWSSigV4AdapterSignsAndSendsHTTP(t *testing.T) {
	now := time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC)
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(request.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/") {
			t.Fatalf("authorization=%q", request.Header.Get("Authorization"))
		}
		if request.Header.Get("X-Amz-Date") != "20150830T123600Z" {
			t.Fatalf("x-amz-date=%q", request.Header.Get("X-Amz-Date"))
		}
		if request.Header.Get("X-Amz-Security-Token") != "session-token" {
			t.Fatalf("session token missing")
		}
		return httpResponse(200, `{"Reservations":[]}`), nil
	})
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{
			AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret", SessionToken: "session-token",
		}},
		HTTP: doer,
		Now:  func() time.Time { return now },
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, AuthScheme: "sigv4", Method: "POST",
		URL: "https://ec2.us-east-1.amazonaws.com/", Service: "ec2", Region: "us-east-1",
		Headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
		Body:    "Action=DescribeInstances&Version=2016-11-15",
	})
	if err != nil || string(result.Output) != `{"Reservations":[]}` {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestAlibabaACS3AdapterSignsAndSendsHTTP(t *testing.T) {
	now := time.Date(2023, 10, 26, 9, 1, 1, 0, time.UTC)
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(request.Header.Get("Authorization"), "ACS3-HMAC-SHA256 Credential=aliyun-ak,") {
			t.Fatalf("authorization=%q", request.Header.Get("Authorization"))
		}
		if request.Header.Get("X-Acs-Action") != "DescribeInstances" || request.Header.Get("X-Acs-Version") != "2014-05-26" {
			t.Fatalf("action/version headers=%v", request.Header)
		}
		if request.Header.Get("X-Acs-Security-Token") != "ram-token" {
			t.Fatalf("security token missing")
		}
		return httpResponse(200, `{"Instances":[]}`), nil
	})
	adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{
		Credentials: staticAlibabaCredentialsProvider{AlibabaCredentials{
			AccessKeyID: "aliyun-ak", AccessKeySecret: "aliyun-secret", SecurityToken: "ram-token",
		}},
		HTTP:  doer,
		Now:   func() time.Time { return now },
		Nonce: func() string { return "d410180a5abf7fe235dd9b74aca91fc0" },
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAlicloud, AuthScheme: "acs3", Method: "POST",
		URL: "https://ecs.cn-shanghai.aliyuncs.com/", Service: "ecs", Operation: "DescribeInstances", APIVersion: "2014-05-26", Region: "cn-shanghai",
		Body: map[string]any{"RegionId": "cn-shanghai"},
	})
	if err != nil || string(result.Output) != `{"Instances":[]}` {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestAlibabaACS3MatchesOfficialFixedVector(t *testing.T) {
	request, err := http.NewRequest(http.MethodPost, "https://ecs.cn-shanghai.aliyuncs.com/?ImageId=win2019_1809_x64_dtc_zh-cn_40G_alibase_20230811.vhd&RegionId=cn-shanghai", nil)
	if err != nil {
		t.Fatal(err)
	}
	invocation := Invocation{Operation: "RunInstances", APIVersion: "2014-05-26"}
	now := time.Date(2023, 10, 26, 10, 22, 32, 0, time.UTC)
	if err := signAlibabaACS3(request, sha256Hex(nil), AlibabaCredentials{
		AccessKeyID: "YourAccessKeyId", AccessKeySecret: "YourAccessKeySecret",
	}, invocation, now, "3156853299f313e23d1673dc12e1703d"); err != nil {
		t.Fatal(err)
	}
	want := "ACS3-HMAC-SHA256 Credential=YourAccessKeyId,SignedHeaders=host;x-acs-action;x-acs-content-sha256;x-acs-date;x-acs-signature-nonce;x-acs-version,Signature=06563a9e1b43f5dfe96b81484da74bceab24a1d853912eee15083a6f0f3283c0"
	if got := request.Header.Get("Authorization"); got != want {
		t.Fatalf("authorization:\nwant %s\n got %s", want, got)
	}
}

func TestTencentTC3AdapterMatchesOfficialSignatureExample(t *testing.T) {
	now := time.Unix(1551113065, 0).UTC()
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		want := "TC3-HMAC-SHA256 Credential=AKIDEXAMPLE/2019-02-25/cvm/tc3_request, SignedHeaders=content-type;host, Signature="
		if !strings.HasPrefix(request.Header.Get("Authorization"), want) {
			t.Fatalf("authorization=%q", request.Header.Get("Authorization"))
		}
		if request.Header.Get("X-Tc-Action") != "DescribeInstances" || request.Header.Get("X-Tc-Version") != "2017-03-12" {
			t.Fatalf("action/version headers=%v", request.Header)
		}
		if request.Header.Get("X-Tc-Token") != "cam-token" {
			t.Fatalf("temporary token missing")
		}
		return httpResponse(200, `{"Response":{"RequestId":"request-id"}}`), nil
	})
	adapter := NewTencentRESTAdapter(TencentRESTConfig{
		Credentials: staticTencentCredentialsProvider{TencentCredentials{
			SecretID: "AKIDEXAMPLE", SecretKey: "secret", Token: "cam-token",
		}},
		HTTP: doer,
		Now:  func() time.Time { return now },
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderTencent, AuthScheme: "tc3", Method: "POST",
		URL: "https://cvm.tencentcloudapi.com/", Service: "cvm", Operation: "DescribeInstances", APIVersion: "2017-03-12", Region: "ap-guangzhou",
		Headers: map[string]string{"Content-Type": "application/json; charset=utf-8"}, Body: map[string]any{},
	})
	if err != nil || string(result.Output) != `{"Response":{"RequestId":"request-id"}}` || result.RequestID != "request-id" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestTencentTC3CanonicalRequestMatchesOfficialFixedVector(t *testing.T) {
	payload := `{"Limit": 1, "Filters": [{"Values": ["unnamed"], "Name": "instance-name"}]}`
	request, err := http.NewRequest(http.MethodPost, "https://cvm.tencentcloudapi.com/", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	canonicalHeaderValue, signedHeaders := canonicalHeaders(request, func(name string) bool {
		return name == "content-type" || name == "host"
	})
	canonicalRequest := strings.Join([]string{
		"POST", "/", "", canonicalHeaderValue, signedHeaders, sha256Hex([]byte(payload)),
	}, "\n")
	if got, want := sha256Hex([]byte(payload)), "99d58dfbc6745f6747f36bfca17dee5e6881dc0428a0a36f96199342bc5b4907"; got != want {
		t.Fatalf("payload hash=%s, want %s", got, want)
	}
	if got, want := sha256Hex([]byte(canonicalRequest)), "2815843035062fffda5fd6f2a44ea8a34818b0dc46f024b8b3786976a3adda7a"; got != want {
		t.Fatalf("canonical hash=%s, want %s\n%s", got, want, canonicalRequest)
	}
}

func TestAlibabaOSS4AndTencentCOSDataPlaneSchemes(t *testing.T) {
	ossRequest, err := http.NewRequest(http.MethodGet, "https://bucket.oss-cn-hangzhou.aliyuncs.com/object?acl", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := signAlibabaOSSV4(ossRequest, AlibabaCredentials{AccessKeyID: "ak", AccessKeySecret: "sk", SecurityToken: "sts"}, "cn-hangzhou", time.Date(2025, 4, 11, 6, 41, 24, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if got := ossRequest.Header.Get("Authorization"); !strings.HasPrefix(got, "OSS4-HMAC-SHA256 Credential=ak/20250411/cn-hangzhou/oss/aliyun_v4_request,Signature=") || strings.Contains(got, "AdditionalHeaders=") {
		t.Fatalf("OSS authorization=%q", got)
	}
	if got := canonicalOSSQuery(ossRequest.URL); got != "acl" {
		t.Fatalf("OSS canonical query=%q", got)
	}

	cosRequest, err := http.NewRequest(http.MethodGet, "https://bucket-1250000000.cos.ap-beijing.myqcloud.com/object?response-content-type=application%2Foctet-stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := signTencentCOS(cosRequest, TencentCredentials{SecretID: "id", SecretKey: "key", Token: "cam-token"}, time.Unix(1557989753, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	authorization := cosRequest.Header.Get("Authorization")
	if !strings.Contains(authorization, "q-header-list=date;host;x-cos-security-token") || !strings.Contains(authorization, "q-url-param-list=response-content-type") || !strings.Contains(authorization, "q-signature=") {
		t.Fatalf("COS authorization=%q", authorization)
	}
}

func TestAzureDefaultCredentialChainContainsNoCLIProvider(t *testing.T) {
	adapter := NewAzureRESTAdapter(AzureRESTConfig{})
	if _, ok := adapter.config.Tokens.(*azureNonCLITokenProvider); !ok {
		t.Fatalf("default token provider=%T, want non-CLI Azure Identity chain", adapter.config.Tokens)
	}
}

func TestGCPDefaultCredentialChainContainsNoCLIProvider(t *testing.T) {
	adapter := NewGCPRESTAdapter(GCPRESTConfig{})
	if _, ok := adapter.config.Tokens.(*gcpADCTokenProvider); !ok {
		t.Fatalf("default token provider=%T, want ADC only", adapter.config.Tokens)
	}
}

func httpResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

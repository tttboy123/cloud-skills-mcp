package cloud

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

type fakeAdapter struct {
	status      ProviderStatus
	statusErr   error
	discoverOut []byte
	discoverErr error
	invokeOut   []byte
	invokeErr   error
	discoveries []DiscoveryRequest
	invocations []Invocation
}

func (f *fakeAdapter) Status(context.Context) (ProviderStatus, error) {
	return f.status, f.statusErr
}

func (f *fakeAdapter) Discover(_ context.Context, request DiscoveryRequest) ([]byte, error) {
	f.discoveries = append(f.discoveries, request)
	return f.discoverOut, f.discoverErr
}

func (f *fakeAdapter) Invoke(_ context.Context, request Invocation) (InvocationResult, error) {
	f.invocations = append(f.invocations, request)
	return InvocationResult{Output: f.invokeOut, RequestID: "request-1"}, f.invokeErr
}

func newTestClient(t *testing.T, runtime Runtime) *client.Client {
	t.Helper()
	c, err := client.NewInProcessClient(NewServer(runtime))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	request := mcp.InitializeRequest{}
	request.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	request.Params.ClientInfo = mcp.Implementation{Name: "cloud-test", Version: "1"}
	if _, err := c.Initialize(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func callCloudTool(t *testing.T, c *client.Client, name string, arguments map[string]any) *mcp.CallToolResult {
	t.Helper()
	request := mcp.CallToolRequest{}
	request.Params.Name = name
	request.Params.Arguments = arguments
	result, err := c.CallTool(t.Context(), request)
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return result
}

func cloudToolText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if result == nil || len(result.Content) != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
	content, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("content is %T", result.Content[0])
	}
	return content.Text
}

func allFakeAdapters() map[Provider]Adapter {
	adapters := make(map[Provider]Adapter, len(AllProviders()))
	for _, provider := range AllProviders() {
		adapters[provider] = &fakeAdapter{
			status:      ProviderStatus{Provider: provider, Available: true, CredentialSource: "test"},
			discoverOut: []byte(`{"help":"ok"}`),
			invokeOut:   []byte(`{"result":"ok"}`),
		}
	}
	return adapters
}

func TestUnifiedToolContractCoversSixProviders(t *testing.T) {
	c := newTestClient(t, Runtime{Adapters: allFakeAdapters()})
	listed, err := c.ListTools(t.Context(), mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"cloud_provider_status": true}
	for _, prefix := range []string{"aws", "azure", "gcp", "alicloud", "tencent", "baiducloud"} {
		want[prefix+"_api_discover"] = true
		want[prefix+"_api_read"] = true
		want[prefix+"_api_mutate"] = true
	}
	if len(listed.Tools) != len(want) {
		t.Fatalf("tool count: want %d, got %d", len(want), len(listed.Tools))
	}
	for _, tool := range listed.Tools {
		if !want[tool.Name] {
			t.Errorf("unexpected tool %s", tool.Name)
		}
		delete(want, tool.Name)
		mutating := strings.HasSuffix(tool.Name, "_api_mutate")
		if strings.Contains(tool.Name, "_api_") && !strings.HasSuffix(tool.Name, "_api_discover") {
			for _, field := range []string{"method", "url", "auth_scheme", "region_set", "api_version", "payload_mode", "checksum_algorithm", "body_file", "response_file", "audience", "auth_version"} {
				if _, ok := tool.InputSchema.Properties[field]; !ok {
					t.Errorf("%s must expose HTTP field %s", tool.Name, field)
				}
			}
			if _, ok := tool.InputSchema.Properties["arguments"]; ok {
				t.Errorf("%s must not expose CLI arguments", tool.Name)
			}
		}
		if tool.Annotations.ReadOnlyHint == nil || *tool.Annotations.ReadOnlyHint == mutating {
			t.Errorf("%s readOnly annotation mismatch", tool.Name)
		}
		if mutating {
			if tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint {
				t.Errorf("%s must be destructive", tool.Name)
			}
			if !contains(tool.InputSchema.Required, "force") {
				t.Errorf("%s must require force", tool.Name)
			}
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing tools: %v", want)
	}
}

func TestStatusAndDiscoveryHandlersReturnProviderDataAndSoftErrors(t *testing.T) {
	fake := &fakeAdapter{
		status:      ProviderStatus{Provider: ProviderAWS, Available: true, Version: "test"},
		discoverOut: []byte("AWS HELP"),
	}
	adapters := allFakeAdapters()
	adapters[ProviderAWS] = fake
	c := newTestClient(t, Runtime{Adapters: adapters})
	status := callCloudTool(t, c, "cloud_provider_status", map[string]any{"provider": "aws"})
	if status.IsError || !strings.Contains(cloudToolText(t, status), `"version":"test"`) {
		t.Fatalf("status=%#v", status)
	}
	discovery := callCloudTool(t, c, "aws_api_discover", map[string]any{"service": "ec2", "operation": "describe-instances"})
	if discovery.IsError || cloudToolText(t, discovery) != "AWS HELP" || len(fake.discoveries) != 1 {
		t.Fatalf("discovery=%#v calls=%#v", discovery, fake.discoveries)
	}
	discovery = callCloudTool(t, c, "aws_api_discover", map[string]any{"service": "ec2;bad"})
	if !discovery.IsError || !strings.Contains(cloudToolText(t, discovery), "invalid service") {
		t.Fatalf("invalid discovery=%#v", discovery)
	}
	fake.statusErr = errors.New("status failed")
	status = callCloudTool(t, c, "cloud_provider_status", map[string]any{"provider": "aws"})
	if !status.IsError || !strings.Contains(cloudToolText(t, status), "status failed") {
		t.Fatalf("status error=%#v", status)
	}
	fake.discoverErr = errors.New("discover failed")
	discovery = callCloudTool(t, c, "aws_api_discover", map[string]any{"service": "ec2"})
	if !discovery.IsError || !strings.Contains(cloudToolText(t, discovery), "discover failed") {
		t.Fatalf("discover error=%#v", discovery)
	}
}

func TestInvocationParsingAndAdapterErrorsAreSoft(t *testing.T) {
	fake := &fakeAdapter{invokeErr: errors.New("provider unavailable")}
	adapters := allFakeAdapters()
	adapters[ProviderAWS] = fake
	c := newTestClient(t, Runtime{Adapters: adapters})
	for _, arguments := range []map[string]any{
		{"service": "ec2", "operation": "describe-instances", "region": "us-east-1", "method": "POST", "url": "https://ec2.us-east-1.amazonaws.com/", "parameters": "bad"},
		{"service": "ec2", "operation": "describe-instances", "region": "us-east-1", "method": "POST", "url": "https://ec2.us-east-1.amazonaws.com/", "headers": "bad"},
		{"service": "ec2", "operation": "describe-instances", "region": "us-east-1", "method": "POST", "url": "https://ec2.us-east-1.amazonaws.com/", "headers": map[string]any{"X-Test": 2}},
	} {
		result := callCloudTool(t, c, "aws_api_read", arguments)
		if !result.IsError {
			t.Fatalf("invalid arguments accepted: %#v", arguments)
		}
	}
	result := callCloudTool(t, c, "aws_api_read", awsHTTPArguments("describe-instances"))
	if !result.IsError || !strings.Contains(cloudToolText(t, result), "provider unavailable") {
		t.Fatalf("adapter error=%#v", result)
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestHTTPBoundaryHelperRejectsInvalidHeaderNamesAndTargets(t *testing.T) {
	for _, name := range []string{"", " leading", "line\nbreak", strings.Repeat("x", 129)} {
		if validHeaderName(name) {
			t.Errorf("accepted invalid header name %q", name)
		}
	}
	if !validHeaderName("X-Cloud-Request_1") {
		t.Fatal("rejected valid header name")
	}
	if err := validateRESTTarget(ProviderAWS, "GET", "https://sts.amazonaws.com/"); err != nil {
		t.Fatal(err)
	}
}

func TestReadClassifierCannotBeDowngradedByCaller(t *testing.T) {
	tests := []struct {
		provider  Provider
		request   Invocation
		readOnly  bool
		sensitive bool
	}{
		{ProviderAWS, Invocation{Service: "ec2", Operation: "describe-instances"}, true, false},
		{ProviderAWS, Invocation{Service: "ec2", Operation: "run-instances"}, false, false},
		{ProviderAWS, Invocation{Service: "secretsmanager", Operation: "get-secret-value"}, false, true},
		{ProviderAWS, Invocation{Service: "kms", Operation: "decrypt"}, false, true},
		{ProviderTencent, Invocation{Service: "cvm", Operation: "DescribeInstances"}, true, false},
		{ProviderTencent, Invocation{Service: "cvm", Operation: "StartInstances"}, false, false},
		{ProviderAzure, Invocation{Method: "GET", URL: "https://management.azure.com/subscriptions/sub/resources?api-version=2021-04-01"}, true, false},
		{ProviderAzure, Invocation{Method: "GET", URL: "https://registry.azurecr.io/v2/repositories"}, true, false},
		{ProviderAzure, Invocation{Method: "PUT", URL: "https://management.azure.com/subscriptions/sub/resourceGroups/rg?api-version=2021-04-01"}, false, false},
		{ProviderAzure, Invocation{Method: "POST", URL: "https://vault.vault.azure.net/keys/key/decrypt?api-version=7.4"}, false, true},
		{ProviderGCP, Invocation{Method: "GET", URL: "https://compute.googleapis.com/compute/v1/projects/p/zones"}, true, false},
		{ProviderGCP, Invocation{Method: "POST", URL: "https://cloudkms.googleapis.com/v1/projects/p/locations/l/keyRings/r/cryptoKeys/k:decrypt"}, false, true},
		{ProviderBaidu, Invocation{Method: "GET", URL: "https://bcc.bj.baidubce.com/v2/instance"}, true, false},
	}
	for _, test := range tests {
		if got := classifyRead(test.provider, test.request); got != test.readOnly {
			t.Errorf("%s %#v readOnly=%v, want %v", test.provider, test.request, got, test.readOnly)
		}
		if got := isSensitiveInvocation(test.request); got != test.sensitive {
			t.Errorf("%s %#v sensitive=%v, want %v", test.provider, test.request, got, test.sensitive)
		}
	}
}

func TestReadAndMutationGatesRunBeforeAdapter(t *testing.T) {
	fake := &fakeAdapter{invokeOut: []byte(`{"ok":true}`)}
	adapters := allFakeAdapters()
	adapters[ProviderAWS] = fake
	c := newTestClient(t, Runtime{Adapters: adapters})

	result := callCloudTool(t, c, "aws_api_read", map[string]any{
		"service": "ec2", "operation": "run-instances", "region": "us-east-1", "method": "POST", "url": "https://ec2.us-east-1.amazonaws.com/",
	})
	if !result.IsError || !strings.Contains(cloudToolText(t, result), "not classified as read-only") {
		t.Fatalf("write passed read tool: %#v", result)
	}

	result = callCloudTool(t, c, "aws_api_mutate", map[string]any{
		"service": "ec2", "operation": "run-instances", "region": "us-east-1", "method": "POST", "url": "https://ec2.us-east-1.amazonaws.com/", "force": true,
	})
	if !result.IsError || !strings.Contains(cloudToolText(t, result), "CLOUD_SKILLS_ALLOW_MUTATIONS") {
		t.Fatalf("mutation gate missing: %#v", result)
	}

	result = callCloudTool(t, c, "aws_api_mutate", map[string]any{
		"service": "secretsmanager", "operation": "get-secret-value", "region": "us-east-1", "method": "POST", "url": "https://secretsmanager.us-east-1.amazonaws.com/", "force": true,
	})
	if !result.IsError || !strings.Contains(cloudToolText(t, result), "CLOUD_SKILLS_ALLOW_SENSITIVE") {
		t.Fatalf("sensitive gate missing: %#v", result)
	}
	if len(fake.invocations) != 0 {
		t.Fatalf("adapter ran despite gates: %#v", fake.invocations)
	}
}

func TestAllowedInvocationReachesProviderAdapter(t *testing.T) {
	fake := &fakeAdapter{invokeOut: []byte(`{"instances":[]}`)}
	adapters := allFakeAdapters()
	adapters[ProviderAWS] = fake
	c := newTestClient(t, Runtime{Adapters: adapters, AllowMutations: true, AllowSensitive: true})
	result := callCloudTool(t, c, "aws_api_read", map[string]any{
		"service": "ec2", "operation": "describe-instances", "region": "us-east-1",
		"method": "POST", "url": "https://ec2.us-east-1.amazonaws.com/",
		"parameters": map[string]any{"MaxResults": 5},
	})
	if result.IsError || cloudToolText(t, result) != `{"instances":[]}` {
		t.Fatalf("read failed: %#v", result)
	}
	if len(fake.invocations) != 1 || fake.invocations[0].Provider != ProviderAWS || fake.invocations[0].Mode != ModeRead {
		t.Fatalf("unexpected invocation: %#v", fake.invocations)
	}
}

func TestInvocationBoundaryRejectsCredentialExfiltrationAndUnboundedInput(t *testing.T) {
	for _, request := range []Invocation{
		{Provider: ProviderAWS, Service: "ec2;curl", Operation: "describe-instances"},
		{Provider: ProviderAWS, Service: "ec2", Operation: "describe-instances", Region: "--endpoint-url"},
		{Provider: ProviderAzure, Method: "GET", URL: "https://management.azure.com/subscriptions", Subscription: "--resource"},
		{Provider: ProviderGCP, Method: "GET", URL: "https://compute.googleapis.com/v1/projects", Project: "project\nInjected: value"},
		{Provider: ProviderAzure, Method: "GET", URL: "http://management.azure.com/subscriptions/x"},
		{Provider: ProviderAzure, Method: "GET", URL: "https://management.azure.com/subscriptions/x?sig=credential"},
		{Provider: ProviderAzure, Method: "GET", URL: "https://untrusted.microsoft.com/subscriptions/x"},
		{Provider: ProviderGCP, Method: "GET", URL: "https://evil.example/v1/projects"},
		{Provider: ProviderGCP, Method: "GET", URL: "https://compute.googleapis.com/v1/projects?access_token=credential"},
		{Provider: ProviderGCP, Method: "GET", URL: "https://compute.googleapis.com/v1/projects", Parameters: map[string]any{"access_token": "credential"}},
		{Provider: ProviderAWS, Service: "s3", Operation: "get-object", Region: "us-east-1", Method: "GET", URL: "https://bucket.s3.amazonaws.com/object", Parameters: map[string]any{"X-Amz-Signature": "credential"}},
		{Provider: ProviderGCP, Method: "POST", URL: "https://compute.googleapis.com/v1/projects", Body: map[string]any{"data": strings.Repeat("x", maxRequestPayloadBytes)}},
		{Provider: ProviderBaidu, Method: "GET", URL: "https://bcc.bj.baidubce.com.evil.example/v2/instance"},
		{Provider: ProviderBaidu, Method: "GET", URL: "https://bcc.bj.baidubce.com/v2/instance", Headers: map[string]string{"X-API-Key": "credential"}},
		{Provider: ProviderTencent, Service: "cvm", Operation: "DescribeInstances", APIVersion: "2017-03-12", Method: "POST", URL: "https://cvm.tencentcloudapi.com/", Headers: map[string]string{"X-TC-Action": "RunInstances"}},
		{Provider: ProviderAWS, Service: "ec2", Operation: "describe-instances", Region: "us-east-1", Method: "POST", URL: "https://ec2.us-east-1.amazonaws.com/", Headers: map[string]string{"Host": "attacker.example"}},
		{Provider: ProviderAzure, Method: "GET", URL: "https://management.azure.com/subscriptions", Headers: map[string]string{"X-HTTP-Method-Override": "DELETE"}},
		{Provider: ProviderGCP, Method: "POST", URL: "https://storage.googleapis.com/upload/storage/v1/b/b/o", Body: map[string]any{"x": 1}, BodyFile: "/tmp/payload"},
		{Provider: ProviderAlicloud, Service: "oss", Operation: "PutObject", Parameters: map[string]any{"Body": "file:///etc/passwd"}},
		{Provider: ProviderAlicloud, AuthScheme: "sls", Service: "sls", Operation: "ListLogstores", Method: "GET", URL: "https://project.cn-hangzhou.log.aliyuncs.com/logstores", Headers: map[string]string{"X-Log-Date": "20260803T010203Z"}},
		{Provider: ProviderAlicloud, AuthScheme: "mns", Service: "mns", Operation: "ListQueues", Method: "GET", URL: "https://123456789.mns.cn-hangzhou.aliyuncs.com/queues", Headers: map[string]string{"Security-Token": "credential"}},
		{Provider: ProviderAlicloud, AuthScheme: "rpc", Service: "baas", Operation: "DescribeFabricOrganization", APIVersion: "2018-12-21", Method: "GET", URL: "https://baas.aliyuncs.com/?SignatureNonce=caller"},
		{Provider: ProviderAlicloud, AuthScheme: "rpc", Service: "baas", Operation: "DescribeFabricOrganization", APIVersion: "2018-12-21", Method: "GET", URL: "https://baas.aliyuncs.com/", Parameters: map[string]any{"Action": "CallerOverride"}},
		{Provider: ProviderGCP, Method: "GET", URL: "https://compute.googleapis.com/v1/projects", Audience: "https://management.azure.com"},
		{Provider: ProviderAzure, Method: "GET", URL: "https://management.azure.com/subscriptions", Audience: "https://attacker.example"},
		{Provider: ProviderAWS, Service: "sts", Operation: "get-caller-identity", AuthVersion: "v2"},
		{Provider: ProviderBaidu, Method: "GET", URL: "https://bts.bj.baidubce.com/v1/forms", AuthVersion: "v3"},
		{Provider: ProviderBaidu, Method: "GET", URL: "https://bts.bj.baidubce.com/v1/forms", AuthVersion: "v2", Service: "bts"},
	} {
		if err := validateInvocation(request, nil); err == nil {
			t.Errorf("accepted unsafe request: %#v", request)
		}
	}
}

func TestInvocationBoundaryNeverIssuesOrExportsCloudCredentials(t *testing.T) {
	blocked := []Invocation{
		{Provider: ProviderAWS, Service: "sts", Operation: "assume-role"},
		{Provider: ProviderAWS, Service: "ecr", Operation: "get-login-password"},
		{Provider: ProviderAWS, Service: "rds", Operation: "generate-db-auth-token"},
		{Provider: ProviderAWS, Service: "iam", Operation: "create-login-profile"},
		{Provider: ProviderAWS, Service: "sso-oidc", Operation: "create-token"},
		{Provider: ProviderAlicloud, Service: "sts", Operation: "AssumeRole"},
		{Provider: ProviderTencent, Service: "sts", Operation: "AssumeRole"},
		{Provider: ProviderAzure, Method: "POST", URL: "https://graph.microsoft.com/v1.0/applications/id/addPassword"},
		{Provider: ProviderAzure, Method: "POST", URL: "https://management.azure.com/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/account/listKeys?api-version=2025-06-01"},
		{Provider: ProviderGCP, Method: "POST", URL: "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/agent@example.iam.gserviceaccount.com:generateAccessToken"},
		{Provider: ProviderGCP, Method: "POST", URL: "https://iam.googleapis.com/v1/projects/project/serviceAccounts/agent@example.iam.gserviceaccount.com/keys"},
		{Provider: ProviderGCP, Method: "GET", URL: "https://apikeys.googleapis.com/v2/projects/project/locations/global/keys/key:getKeyString"},
		{Provider: ProviderGCP, Method: "POST", URL: "https://identitytoolkit.googleapis.com/v1/accounts:signInWithPassword"},
		{Provider: ProviderBaidu, Method: "POST", URL: "https://sts.bj.baidubce.com/v1/sessionToken"},
		{Provider: ProviderBaidu, Method: "POST", URL: "https://iam.bj.baidubce.com/v1/user/agent/accessKey"},
	}
	for _, request := range blocked {
		if err := validateInvocation(request, nil); err == nil || !strings.Contains(err.Error(), "credential issuance") {
			t.Errorf("credential issuance was not rejected: %#v: %v", request, err)
		}
	}
}

func TestInvocationBoundaryKeepsOrdinaryIAMResourceManagementAvailable(t *testing.T) {
	allowed := []Invocation{
		{Provider: ProviderAWS, Service: "iam", Operation: "list-roles", Region: "us-east-1", Method: "POST", URL: "https://iam.amazonaws.com/"},
		{Provider: ProviderAlicloud, Service: "ram", Operation: "ListRoles", APIVersion: "2015-05-01", Method: "POST", URL: "https://ram.aliyuncs.com/"},
		{Provider: ProviderTencent, Service: "cam", Operation: "ListRoles", APIVersion: "2019-01-16", Method: "POST", URL: "https://cam.tencentcloudapi.com/"},
		{Provider: ProviderAzure, Method: "GET", URL: "https://graph.microsoft.com/v1.0/applications"},
		{Provider: ProviderGCP, Method: "GET", URL: "https://iam.googleapis.com/v1/projects/project/serviceAccounts"},
		{Provider: ProviderBaidu, Method: "GET", URL: "https://iam.bj.baidubce.com/v1/user"},
	}
	for _, request := range allowed {
		if err := validateInvocation(request, nil); err != nil {
			t.Errorf("ordinary IAM resource operation was rejected: %#v: %v", request, err)
		}
	}
}

func TestInvocationBoundaryAllowsOfficialAzureAndBaiduDataPlaneEndpoints(t *testing.T) {
	requests := []Invocation{
		{Provider: ProviderAzure, Method: "GET", URL: "https://store.azconfig.io/kv?api-version=2026-04-01"},
		{Provider: ProviderAzure, Method: "GET", URL: "https://workspace.azuredatabricks.net/api/2.0/clusters/list", Audience: "2ff814a6-3304-4ab8-85cb-cd0e6f879c1d"},
		{Provider: ProviderAzure, Method: "GET", URL: "https://management.chinacloudapi.cn/subscriptions?api-version=2020-01-01"},
		{Provider: ProviderAzure, Method: "GET", URL: "https://management.usgovcloudapi.net/subscriptions?api-version=2020-01-01"},
		{Provider: ProviderBaidu, Method: "GET", URL: "https://bucket.bj.bcebos.com/object"},
		{Provider: ProviderBaidu, Method: "GET", URL: "https://bts.bj.baidubce.com/v1/forms", AuthVersion: "v2", Service: "bts", Region: "bj"},
	}
	for _, request := range requests {
		if err := validateInvocation(request, nil); err != nil {
			t.Errorf("official data-plane endpoint rejected: %#v: %v", request, err)
		}
	}
}

func TestInvocationBoundaryAllowsAlibabaProductSpecificHTTPSAuthSchemes(t *testing.T) {
	requests := []Invocation{
		{Provider: ProviderAlicloud, AuthScheme: "sls", Service: "sls", Operation: "ListLogstores", APIVersion: "0.6.0", Method: "GET", URL: "https://project.cn-hangzhou.log.aliyuncs.com/logstores"},
		{Provider: ProviderAlicloud, AuthScheme: "sls4", Service: "sls", Operation: "ListLogstores", Region: "cn-hangzhou", Method: "GET", URL: "https://project.cn-hangzhou.log.aliyuncs.com/logstores"},
		{Provider: ProviderAlicloud, AuthScheme: "mns", Service: "mns", Operation: "ListQueues", Method: "GET", URL: "https://123456789.mns.cn-hangzhou.aliyuncs.com/queues"},
		{Provider: ProviderAlicloud, AuthScheme: "ots", Service: "ots", Operation: "ListTable", Method: "POST", URL: "https://demo.cn-hangzhou.ots.aliyuncs.com/ListTable"},
		{Provider: ProviderAlicloud, AuthScheme: "ots4", Service: "ots", Operation: "ListTable", Region: "cn-hangzhou", Method: "POST", URL: "https://demo.cn-hangzhou.ots.aliyuncs.com/ListTable"},
		{Provider: ProviderAlicloud, AuthScheme: "rpc", Service: "baas", Operation: "DescribeFabricOrganization", APIVersion: "2018-12-21", Method: "GET", URL: "https://baas.aliyuncs.com/"},
	}
	for _, request := range requests {
		if err := validateInvocation(request, nil); err != nil {
			t.Errorf("auth_scheme=%s rejected: %v", request.AuthScheme, err)
		}
	}
	requests[1].Region = ""
	if err := validateInvocation(requests[1], nil); err == nil || !strings.Contains(err.Error(), "region") {
		t.Fatalf("SLS4 missing region error=%v", err)
	}
	requests[4].Region = ""
	if err := validateInvocation(requests[4], nil); err == nil || !strings.Contains(err.Error(), "region") {
		t.Fatalf("OTS4 missing region error=%v", err)
	}
	requests[3].Method = "GET"
	if err := validateInvocation(requests[3], nil); err == nil || !strings.Contains(err.Error(), "POST") {
		t.Fatalf("OTS non-POST error=%v", err)
	}
	rpc := requests[5]
	rpc.APIVersion = ""
	if err := validateInvocation(rpc, nil); err == nil || !strings.Contains(err.Error(), "api_version") {
		t.Fatalf("RPC missing api_version error=%v", err)
	}
	rpc = requests[5]
	rpc.Method = "PUT"
	if err := validateInvocation(rpc, nil); err == nil || !strings.Contains(err.Error(), "GET or POST") {
		t.Fatalf("RPC invalid method error=%v", err)
	}
}

func TestOperatorCanAllowOnlyAnExactAdditionalProviderEndpointHost(t *testing.T) {
	request := Invocation{Provider: ProviderAzure, Method: "GET", URL: "https://new-api.example.microsoft/v1/resources", Audience: "https://management.azure.com"}
	if err := validateInvocationWithEndpointHosts(request, nil, []string{"new-api.example.microsoft"}); err != nil {
		t.Fatalf("exact operator-approved endpoint rejected: %v", err)
	}
	for _, rawURL := range []string{
		"https://child.new-api.example.microsoft/v1/resources",
		"https://new-api.example.microsoft.evil.example/v1/resources",
	} {
		request.URL = rawURL
		if err := validateInvocationWithEndpointHosts(request, nil, []string{"new-api.example.microsoft"}); err == nil {
			t.Fatalf("non-exact endpoint accepted: %s", rawURL)
		}
	}
}

func TestOutputIsBoundedAndCredentialFieldsAreRedacted(t *testing.T) {
	fake := &fakeAdapter{invokeOut: []byte(`{"SecretAccessKey":"do-not-leak"}`)}
	adapters := allFakeAdapters()
	adapters[ProviderAWS] = fake
	c := newTestClient(t, Runtime{Adapters: adapters, MaxOutputBytes: 64})
	result := callCloudTool(t, c, "aws_api_read", map[string]any{
		"service": "ec2", "operation": "describe-instances", "region": "us-east-1", "method": "POST", "url": "https://ec2.us-east-1.amazonaws.com/",
	})
	text := cloudToolText(t, result)
	if strings.Contains(text, "do-not-leak") || !strings.Contains(text, "REDACTED") {
		t.Fatalf("secret output not redacted: %s", text)
	}

	fake.invokeOut = []byte(strings.Repeat("x", 65))
	result = callCloudTool(t, c, "aws_api_read", map[string]any{
		"service": "ec2", "operation": "describe-instances", "region": "us-east-1", "method": "POST", "url": "https://ec2.us-east-1.amazonaws.com/",
	})
	if !result.IsError || !strings.Contains(cloudToolText(t, result), "response exceeds") {
		t.Fatalf("large output was not rejected: %#v", result)
	}
}

func TestBodyFilePolicyAllowsOnlyBoundedRegularFilesUnderOperatorRoots(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "payload.bin")
	if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	request := Invocation{Provider: ProviderGCP, Method: "POST", URL: "https://storage.googleapis.com/upload/storage/v1/b/b/o", BodyFile: path}
	if err := validateInvocation(request, []string{root}); err != nil {
		t.Fatalf("guarded body_file rejected: %v", err)
	}
	aliRequest := Invocation{Provider: ProviderAlicloud, AuthScheme: "oss4", Service: "oss", Operation: "PutObject", Region: "cn-hangzhou", Method: "PUT", URL: "https://bucket.oss-cn-hangzhou.aliyuncs.com/object", BodyFile: path}
	if err := validateInvocation(aliRequest, []string{root}); err != nil {
		t.Fatalf("guarded Alibaba file reference rejected: %v", err)
	}
	request.BodyFile = root
	if err := validateInvocation(request, []string{root}); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("directory body_file error=%v", err)
	}
	large := filepath.Join(root, "large.bin")
	file, err := os.Create(large)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxRequestFileBytes + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	request.BodyFile = large
	if err := validateInvocation(request, []string{root}); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("large body_file error=%v", err)
	}
}

func TestMutationAuditIsFailClosed(t *testing.T) {
	fake := &fakeAdapter{invokeOut: []byte(`{"ok":true}`)}
	adapters := allFakeAdapters()
	adapters[ProviderAWS] = fake
	c := newTestClient(t, Runtime{
		Adapters:       adapters,
		AllowMutations: true,
		Audit: func(context.Context, AuditEvent) error {
			return errors.New("audit unavailable")
		},
	})
	result := callCloudTool(t, c, "aws_api_mutate", map[string]any{
		"service": "ec2", "operation": "run-instances", "region": "us-east-1", "method": "POST", "url": "https://ec2.us-east-1.amazonaws.com/", "force": true,
	})
	if !result.IsError || !strings.Contains(cloudToolText(t, result), "audit") {
		t.Fatalf("audit failure not surfaced: %#v", result)
	}
	if len(fake.invocations) != 0 {
		t.Fatal("mutation ran after audit failure")
	}
}

func awsHTTPArguments(operation string) map[string]any {
	return map[string]any{
		"service": "ec2", "operation": operation, "region": "us-east-1",
		"method": "POST", "url": "https://ec2.us-east-1.amazonaws.com/",
	}
}

func TestAuditURLNeverContainsQueryValues(t *testing.T) {
	fake := &fakeAdapter{invokeOut: []byte(`{"ok":true}`)}
	adapters := allFakeAdapters()
	adapters[ProviderAzure] = fake
	var events []AuditEvent
	c := newTestClient(t, Runtime{
		Adapters: adapters, AllowMutations: true,
		Audit: func(_ context.Context, event AuditEvent) error {
			events = append(events, event)
			return nil
		},
	})
	result := callCloudTool(t, c, "azure_api_mutate", map[string]any{
		"method": "POST", "url": "https://management.azure.com/subscriptions/sub/providers/test?api-version=2026-01-01&name=private", "force": true,
	})
	if result.IsError || len(events) != 2 {
		t.Fatalf("result=%#v events=%#v", result, events)
	}
	for _, event := range events {
		if strings.Contains(event.URL, "?") || strings.Contains(event.URL, "private") {
			t.Fatalf("audit URL leaked query: %q", event.URL)
		}
	}
}

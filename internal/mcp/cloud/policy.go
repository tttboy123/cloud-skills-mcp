package cloud

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
var azureApplicationIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}(?:/\.default)?$`)
var endpointLabelPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

const (
	maxRequestPayloadBytes         = 1024 * 1024
	maxRequestFileBytes            = 64 * 1024 * 1024
	defaultResponseFileLimit int64 = 1024 * 1024 * 1024
)

var sensitiveTerms = []string{
	"secret", "password", "credential", "accesskey", "access-key", "privatekey", "private-key",
	"sessiontoken", "session-token", "gettoken", "get-token", "print-access-token",
	"decrypt", "unseal", "unwrap",
}

func classifyRead(provider Provider, request Invocation) bool {
	if isSensitiveInvocation(request) {
		return false
	}
	switch provider {
	case ProviderAzure, ProviderGCP, ProviderBaidu:
		switch strings.ToUpper(strings.TrimSpace(request.Method)) {
		case "GET", "HEAD", "OPTIONS":
			return true
		default:
			return false
		}
	case ProviderAWS, ProviderAlicloud, ProviderTencent:
		action := strings.ToLower(strings.TrimSpace(request.Operation))
		for _, prefix := range []string{
			"describe", "list", "get", "head", "search", "query", "check", "show", "read", "inspect", "lookup", "count", "enumerate",
		} {
			if strings.HasPrefix(action, prefix) {
				return true
			}
		}
	}
	return false
}

func isSensitiveInvocation(request Invocation) bool {
	value := strings.ToLower(strings.Join([]string{request.Service, request.Operation, request.URL}, " "))
	for _, term := range sensitiveTerms {
		if strings.Contains(value, term) {
			return true
		}
	}
	return false
}

func validateInvocation(request Invocation, allowedFileRoots []string) error {
	return validateInvocationWithEndpointHosts(request, allowedFileRoots, nil)
}

func validateInvocationWithEndpointHosts(request Invocation, allowedFileRoots, allowedEndpointHosts []string) error {
	if !isProvider(request.Provider) {
		return fmt.Errorf("unsupported provider %q", request.Provider)
	}
	if isCredentialIssuanceInvocation(request) {
		return fmt.Errorf("credential issuance or export operations are not exposed through the MCP gateway")
	}
	if err := validateRESTTargetWithEndpointHosts(request.Provider, request.Method, request.URL, allowedEndpointHosts); err != nil {
		return err
	}
	switch request.Provider {
	case ProviderAWS:
		if !identifierPattern.MatchString(request.Service) {
			return fmt.Errorf("invalid service %q", request.Service)
		}
		if !identifierPattern.MatchString(request.Operation) {
			return fmt.Errorf("invalid operation %q", request.Operation)
		}
		if !identifierPattern.MatchString(request.Region) {
			return fmt.Errorf("AWS SigV4 requires a valid region")
		}
		if scheme := normalizedAuthScheme(request.AuthScheme, authSchemeAWSSigV4); scheme != authSchemeAWSSigV4 {
			return fmt.Errorf("AWS auth_scheme must be sigv4")
		}
	case ProviderAlicloud:
		if !identifierPattern.MatchString(request.Service) || !identifierPattern.MatchString(request.Operation) {
			return fmt.Errorf("Alibaba Cloud requires valid service and operation")
		}
		scheme := normalizedAuthScheme(request.AuthScheme, authSchemeAlibabaACS3)
		if scheme != authSchemeAlibabaACS3 && scheme != authSchemeAlibabaOSSV4 {
			return fmt.Errorf("Alibaba Cloud auth_scheme must be acs3 or oss4")
		}
		if scheme == authSchemeAlibabaACS3 && !identifierPattern.MatchString(request.APIVersion) {
			return fmt.Errorf("Alibaba Cloud ACS3 requires a valid api_version")
		}
		if scheme == authSchemeAlibabaOSSV4 && !identifierPattern.MatchString(request.Region) {
			return fmt.Errorf("Alibaba Cloud OSS4 requires a valid region")
		}
	case ProviderTencent:
		if !identifierPattern.MatchString(request.Service) || !identifierPattern.MatchString(request.Operation) {
			return fmt.Errorf("Tencent Cloud requires valid service and operation")
		}
		scheme := normalizedAuthScheme(request.AuthScheme, authSchemeTencentTC3)
		if scheme != authSchemeTencentTC3 && scheme != authSchemeTencentCOS {
			return fmt.Errorf("Tencent Cloud auth_scheme must be tc3 or cos")
		}
		if scheme == authSchemeTencentTC3 && !identifierPattern.MatchString(request.APIVersion) {
			return fmt.Errorf("Tencent Cloud TC3 requires a valid api_version")
		}
	}
	for name, value := range map[string]string{
		"region": request.Region, "project": request.Project, "subscription": request.Subscription,
	} {
		if err := validateContextValue(name, value); err != nil {
			return err
		}
	}
	if request.Audience != "" {
		if request.Provider != ProviderAzure {
			return fmt.Errorf("audience is supported only by Azure")
		}
		if _, err := normalizeAzureAudience(request.Audience); err != nil {
			return err
		}
	}
	if request.AuthVersion != "" {
		if request.Provider != ProviderBaidu {
			return fmt.Errorf("auth_version is supported only by Baidu AI Cloud")
		}
		if request.AuthVersion != "v1" && request.AuthVersion != "v2" {
			return fmt.Errorf("Baidu auth_version must be v1 or v2")
		}
	}
	if request.Provider != ProviderBaidu && request.AuthVersion != "" {
		return fmt.Errorf("auth_version is supported only by Baidu AI Cloud")
	}
	if request.Provider == ProviderBaidu && request.AuthVersion == "v2" {
		if !identifierPattern.MatchString(request.Service) {
			return fmt.Errorf("Baidu BCE v2 requires a valid service")
		}
		if !identifierPattern.MatchString(request.Region) {
			return fmt.Errorf("Baidu BCE v2 requires a valid region")
		}
	}
	for name := range request.Parameters {
		if isCredentialQueryParameter(name) {
			return fmt.Errorf("caller-supplied credential query parameter %q is forbidden", name)
		}
	}
	if request.Body != nil && request.BodyFile != "" {
		return fmt.Errorf("body and body_file are mutually exclusive")
	}
	if request.BodyFile != "" {
		if !pathAllowed(request.BodyFile, allowedFileRoots) {
			return fmt.Errorf("body_file is outside CLOUD_SKILLS_ALLOWED_FILE_ROOTS")
		}
		info, err := os.Stat(request.BodyFile)
		if err != nil {
			return fmt.Errorf("inspect body_file: %w", err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("body_file must be a regular file")
		}
		if info.Size() > maxRequestFileBytes {
			return fmt.Errorf("body_file exceeds %d bytes", maxRequestFileBytes)
		}
	}
	if request.ResponseFile != "" {
		if _, err := resolveResponseFileTarget(request.ResponseFile, allowedFileRoots); err != nil {
			return err
		}
	}
	if len(request.Headers) > 64 {
		return fmt.Errorf("too many HTTP headers: %d", len(request.Headers))
	}
	for name := range request.Headers {
		lower := strings.ToLower(strings.TrimSpace(name))
		if isProtectedHeader(lower) {
			return fmt.Errorf("caller-supplied protected header %q is forbidden", name)
		}
		if !validHeaderName(name) {
			return fmt.Errorf("invalid HTTP header name %q", name)
		}
		for _, character := range request.Headers[name] {
			if character == 0 || character == '\r' || character == '\n' {
				return fmt.Errorf("HTTP header %q contains a control character", name)
			}
		}
	}
	for name, value := range map[string]any{"parameters": request.Parameters, "body": request.Body} {
		if value == nil {
			continue
		}
		data, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("%s must be JSON-compatible: %w", name, err)
		}
		if len(data) > maxRequestPayloadBytes {
			return fmt.Errorf("%s exceeds %d bytes", name, maxRequestPayloadBytes)
		}
	}
	return nil
}

func isProtectedHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "authorization", "proxy-authorization", "host",
		"x-api-key", "api-key", "cookie", "set-cookie", "x-http-method-override", "x-method-override",
		"x-amz-date", "x-amz-security-token",
		"x-acs-action", "x-acs-version", "x-acs-date", "x-acs-signature-nonce", "x-acs-content-sha256", "x-acs-security-token",
		"x-oss-date", "x-oss-content-sha256", "x-oss-security-token",
		"x-tc-action", "x-tc-version", "x-tc-timestamp", "x-tc-region", "x-tc-token",
		"x-cos-security-token", "x-bce-date", "x-bce-security-token", "x-goog-user-project":
		return true
	default:
		return false
	}
}

func isCredentialIssuanceInvocation(request Invocation) bool {
	operation := normalizedOperation(request.Operation)
	for _, blocked := range []string{
		"assumerole", "assumerolewithsaml", "assumerolewithwebidentity",
		"getfederationtoken", "getsessiontoken", "getauthorizationtoken", "getloginpassword",
		"generatedbauthtoken", "createaccesskey", "createapikey", "createsecretid",
		"createcredential", "generatecredential", "createservicespecificcredential",
		"resetservicespecificcredential", "presign", "createpresignedurl", "generatepresignedurl",
		"createloginprofile", "updateloginprofile", "changepassword", "createvirtualmfadevice",
		"createtoken", "refreshtoken", "exchangetoken", "initiateauth", "admininitiateauth",
		"respondtoauthchallenge", "adminrespondtoauthchallenge",
	} {
		if operation == blocked || strings.HasPrefix(operation, blocked) {
			return true
		}
	}

	parsed, err := url.Parse(request.URL)
	if err != nil || parsed.Hostname() == "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	path := strings.ToLower(parsed.Path)
	actionPath := normalizedOperation(path)
	if request.Provider == ProviderGCP {
		switch host {
		case "iamcredentials.googleapis.com", "sts.googleapis.com", "securetoken.googleapis.com", "oauth2.googleapis.com":
			return true
		}
		if host == "iam.googleapis.com" && strings.EqualFold(request.Method, "POST") && strings.HasSuffix(strings.TrimRight(path, "/"), "/keys") {
			return true
		}
		if host == "apikeys.googleapis.com" && (strings.Contains(actionPath, "getkeystring") || strings.Contains(actionPath, "lookupkey")) {
			return true
		}
	}
	if request.Provider == ProviderAzure && host == "graph.microsoft.com" {
		for _, action := range []string{"addpassword", "addkey", "resetpassword"} {
			if strings.Contains(actionPath, action) {
				return true
			}
		}
	}
	if request.Provider == ProviderBaidu && (host == "sts.baidubce.com" || strings.HasPrefix(host, "sts.")) {
		return true
	}
	if strings.EqualFold(request.Method, "POST") && strings.Contains(actionPath, "accesskey") {
		return true
	}
	for _, action := range []string{"listkeys", "listcredentials", "regeneratekey", "regeneratekeys", "getkeystring", "generatetoken"} {
		if strings.Contains(actionPath, action) {
			return true
		}
	}
	for _, action := range []string{"signin", "signup", "refreshtoken", "exchangetoken"} {
		if strings.Contains(actionPath, action) {
			return true
		}
	}
	return false
}

func normalizedOperation(value string) string {
	value = strings.ToLower(value)
	return strings.NewReplacer("-", "", "_", "", "/", "", ":", "").Replace(value)
}

func validateContextValue(name, value string) error {
	if value == "" {
		return nil
	}
	if len(value) > 256 || strings.HasPrefix(value, "-") || strings.TrimSpace(value) != value {
		return fmt.Errorf("invalid %s", name)
	}
	for _, character := range value {
		if character == 0 || unicode.IsControl(character) {
			return fmt.Errorf("invalid %s", name)
		}
	}
	return nil
}

func pathAllowed(path string, roots []string) bool {
	if len(roots) == 0 || strings.TrimSpace(path) == "" {
		return false
	}
	resolved, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	resolved, err = filepath.EvalSymlinks(resolved)
	if err != nil {
		return false
	}
	for _, root := range roots {
		rootPath, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		rootPath, err = filepath.EvalSymlinks(rootPath)
		if err != nil {
			continue
		}
		relative, err := filepath.Rel(rootPath, resolved)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func resolveResponseFileTarget(path string, roots []string) (string, error) {
	if len(roots) == 0 || strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("response_file is outside CLOUD_SKILLS_ALLOWED_FILE_ROOTS")
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve response_file: %w", err)
	}
	if _, err := os.Lstat(target); err == nil {
		return "", fmt.Errorf("response_file already exists")
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect response_file: %w", err)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(target))
	if err != nil {
		return "", fmt.Errorf("resolve response_file parent: %w", err)
	}
	info, err := os.Stat(parent)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("response_file parent must be an existing directory")
	}
	target = filepath.Join(parent, filepath.Base(target))
	for _, root := range roots {
		rootPath, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		rootPath, err = filepath.EvalSymlinks(rootPath)
		if err != nil {
			continue
		}
		relative, err := filepath.Rel(rootPath, target)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return target, nil
		}
	}
	return "", fmt.Errorf("response_file is outside CLOUD_SKILLS_ALLOWED_FILE_ROOTS")
}

func validateRESTTarget(provider Provider, method, rawURL string) error {
	return validateRESTTargetWithEndpointHosts(provider, method, rawURL, nil)
}

func validateRESTTargetWithEndpointHosts(provider Provider, method, rawURL string, allowedEndpointHosts []string) error {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case "GET", "HEAD", "OPTIONS", "POST", "PUT", "PATCH", "DELETE":
	default:
		return fmt.Errorf("unsupported HTTP method %q", method)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("invalid HTTPS provider URL %q", rawURL)
	}
	if parsed.Port() != "" && parsed.Port() != "443" {
		return fmt.Errorf("provider URL port must be 443")
	}
	host := strings.ToLower(parsed.Hostname())
	for name := range parsed.Query() {
		if isCredentialQueryParameter(name) {
			return fmt.Errorf("caller-supplied credential query parameter %q is forbidden", name)
		}
	}
	allowed := false
	switch provider {
	case ProviderAWS:
		allowed = host == "amazonaws.com" || strings.HasSuffix(host, ".amazonaws.com") ||
			host == "amazonaws.com.cn" || strings.HasSuffix(host, ".amazonaws.com.cn") ||
			host == "api.aws" || strings.HasSuffix(host, ".api.aws")
	case ProviderAzure:
		allowed = host == "management.azure.com" || host == "graph.microsoft.com" || host == "api.loganalytics.io" ||
			hasAnySuffix(host, ".azure.com", ".azure.net", ".windows.net", ".azurecr.io", ".loganalytics.io", ".azureedge.net", ".trafficmanager.net",
				".azconfig.io", ".azuredatabricks.net", ".azure-api.net", ".azuresynapse.net", ".azureml.ms", ".service.signalr.net",
				".chinacloudapi.cn", ".azure.cn", ".windowsazure.cn", ".usgovcloudapi.net", ".microsoftazure.us", ".azure.us", ".microsoftazure.de")
	case ProviderGCP:
		allowed = host == "googleapis.com" || strings.HasSuffix(host, ".googleapis.com")
	case ProviderAlicloud:
		allowed = host == "aliyuncs.com" || strings.HasSuffix(host, ".aliyuncs.com") ||
			host == "aliyuncs.com.cn" || strings.HasSuffix(host, ".aliyuncs.com.cn") ||
			host == "alibabacloud.com" || strings.HasSuffix(host, ".alibabacloud.com")
	case ProviderTencent:
		allowed = host == "tencentcloudapi.com" || strings.HasSuffix(host, ".tencentcloudapi.com") ||
			host == "myqcloud.com" || strings.HasSuffix(host, ".myqcloud.com") ||
			host == "tencentcloud.com" || strings.HasSuffix(host, ".tencentcloud.com") ||
			host == "qcloud.com" || strings.HasSuffix(host, ".qcloud.com")
	case ProviderBaidu:
		allowed = host == "baidubce.com" || strings.HasSuffix(host, ".baidubce.com") ||
			host == "bcebos.com" || strings.HasSuffix(host, ".bcebos.com")
	}
	if !allowed {
		for _, candidate := range allowedEndpointHosts {
			if validAdditionalEndpointHost(candidate) && host == strings.ToLower(candidate) {
				allowed = true
				break
			}
		}
	}
	if !allowed {
		return fmt.Errorf("URL host %q is outside the provider endpoint allowlist", host)
	}
	return nil
}

func isCredentialQueryParameter(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "access_token", "oauth_token", "authorization", "sig", "signature",
		"x-amz-credential", "x-amz-signature", "x-amz-security-token",
		"x-goog-signature", "x-bce-security-token", "sharedaccesssignature",
		"q-signature", "q-ak", "x-cos-security-token", "x-acs-security-token", "x-oss-security-token":
		return true
	default:
		return false
	}
}

func validAdditionalEndpointHost(host string) bool {
	if len(host) > 253 || net.ParseIP(host) != nil || !strings.Contains(host, ".") || strings.Contains(host, "..") || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if !endpointLabelPattern.MatchString(label) {
			return false
		}
	}
	return true
}

func normalizeAzureAudience(raw string) (string, error) {
	audience := strings.TrimSpace(raw)
	if len(audience) == 0 || len(audience) > 2048 {
		return "", fmt.Errorf("Azure audience length must be between 1 and 2048 bytes")
	}
	if azureApplicationIDPattern.MatchString(audience) {
		return strings.TrimSuffix(audience, "/.default"), nil
	}
	parsed, err := url.Parse(audience)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid Azure audience %q", raw)
	}
	if parsed.Port() != "" && parsed.Port() != "443" {
		return "", fmt.Errorf("Azure audience port must be 443")
	}
	host := strings.ToLower(parsed.Hostname())
	allowed := host == "graph.microsoft.com" ||
		hasAnySuffix(host, ".azure.com", ".azure.net", ".windows.net", ".microsoft.com", ".loganalytics.io", ".azconfig.io", ".azureml.ms", ".azuredatabricks.net",
			".chinacloudapi.cn", ".azure.cn", ".windowsazure.cn", ".usgovcloudapi.net", ".microsoftazure.us", ".azure.us", ".microsoftazure.de")
	if !allowed {
		return "", fmt.Errorf("Azure audience host %q is outside the Microsoft identity allowlist", host)
	}
	path := strings.TrimSuffix(parsed.Path, "/.default")
	if strings.Contains(path, "..") {
		return "", fmt.Errorf("invalid Azure audience path")
	}
	parsed.RawPath = ""
	parsed.Path = strings.TrimSuffix(parsed.Path, "/.default")
	return strings.TrimRight(parsed.String(), "/"), nil
}

func validHeaderName(name string) bool {
	if strings.TrimSpace(name) != name || name == "" || len(name) > 128 {
		return false
	}
	for _, character := range name {
		if !(character >= 'A' && character <= 'Z') && !(character >= 'a' && character <= 'z') &&
			!(character >= '0' && character <= '9') && !strings.ContainsRune("!#$%&'*+-.^_`|~", character) {
			return false
		}
	}
	return true
}

func hasAnySuffix(value string, suffixes ...string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(value, suffix) {
			return true
		}
	}
	return false
}

func isProvider(provider Provider) bool {
	for _, candidate := range AllProviders() {
		if provider == candidate {
			return true
		}
	}
	return false
}

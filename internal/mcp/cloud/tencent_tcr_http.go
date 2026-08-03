package cloud

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const (
	authSchemeTencentTCR      = "tcr-registry"
	tencentTCRAPIVersion      = "2019-09-24"
	maxTencentTCRTokenBytes   = 1024 * 1024
	maxTencentTCRPasswordSize = 64 * 1024
)

var (
	tencentTCRInstanceIDPattern   = regexp.MustCompile(`^tcr-[a-z0-9]{4,64}$`)
	tencentTCRInstanceNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{3,48}[a-z0-9]$`)
)

type tencentTCRTarget struct {
	RegistryHost string
	Region       string
	InstanceID   string
	Custom       bool
}

type tencentTCRTemporaryCredential struct {
	Username string
	Password string
}

func validateTencentTCRInvocation(invocation Invocation, allowedHosts []string) error {
	_, err := parseTencentTCRInvocation(invocation, allowedHosts)
	return err
}

func parseTencentTCRInvocation(invocation Invocation, allowedHosts []string) (tencentTCRTarget, error) {
	if !strings.EqualFold(strings.TrimSpace(invocation.Service), "tcr") {
		return tencentTCRTarget{}, fmt.Errorf("Tencent Cloud TCR Registry requires service=tcr")
	}
	if !identifierPattern.MatchString(invocation.Operation) {
		return tencentTCRTarget{}, fmt.Errorf("Tencent Cloud TCR Registry requires a valid operation")
	}
	if invocation.APIVersion != "" {
		return tencentTCRTarget{}, fmt.Errorf("Tencent Cloud TCR Registry api_version is server-controlled and must be omitted")
	}
	region := strings.ToLower(strings.TrimSpace(invocation.Region))
	if !identifierPattern.MatchString(region) {
		return tencentTCRTarget{}, fmt.Errorf("Tencent Cloud TCR Registry requires a valid region")
	}
	instanceID := strings.TrimSpace(invocation.RegistryInstanceID)
	if !tencentTCRInstanceIDPattern.MatchString(instanceID) {
		return tencentTCRTarget{}, fmt.Errorf("Tencent Cloud TCR Registry requires a valid Enterprise Edition registry_instance_id")
	}
	method := strings.ToUpper(strings.TrimSpace(invocation.Method))
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return tencentTCRTarget{}, fmt.Errorf("Tencent Cloud TCR Registry requires GET, HEAD, POST, PUT, PATCH, or DELETE")
	}
	parsed, err := url.Parse(invocation.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return tencentTCRTarget{}, fmt.Errorf("Tencent Cloud TCR Registry requires a valid HTTPS URL")
	}
	if parsed.Port() != "" && parsed.Port() != "443" {
		return tencentTCRTarget{}, fmt.Errorf("Tencent Cloud TCR Registry URL port must be 443")
	}
	for name := range parsed.Query() {
		if isCredentialQueryParameter(name) {
			return tencentTCRTarget{}, fmt.Errorf("caller-supplied credential query parameter %q is forbidden", name)
		}
	}
	for name := range invocation.Parameters {
		if isCredentialQueryParameter(name) {
			return tencentTCRTarget{}, fmt.Errorf("caller-supplied credential query parameter %q is forbidden", name)
		}
	}
	for name := range invocation.Headers {
		if isProtectedHeader(name) {
			return tencentTCRTarget{}, fmt.Errorf("caller-supplied protected header %q is forbidden", name)
		}
	}
	host := strings.ToLower(parsed.Hostname())
	defaultEndpoint := validTencentTCRRegistryHost(host)
	custom := !defaultEndpoint && isExplicitEndpointHost(host, allowedHosts)
	if !defaultEndpoint && !custom {
		return tencentTCRTarget{}, fmt.Errorf("Tencent Cloud TCR Registry requires an exact Enterprise Edition public, VPC, or operator-pinned custom endpoint")
	}
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	if strings.Contains(path, "%") {
		return tencentTCRTarget{}, fmt.Errorf("Tencent Cloud TCR Registry paths must use their canonical unescaped form")
	}
	if path == "/v2/token" || strings.HasPrefix(path, "/v2/token/") {
		return tencentTCRTarget{}, fmt.Errorf("Tencent Cloud TCR Registry credential endpoints are internal and are not exposed through MCP")
	}
	if err := validateTencentTCRRegistryPath(path, method, invocation); err != nil {
		return tencentTCRTarget{}, err
	}
	return tencentTCRTarget{RegistryHost: host, Region: region, InstanceID: instanceID, Custom: custom}, nil
}

func validTencentTCRRegistryHost(host string) bool {
	const suffix = ".tencentcloudcr.com"
	if !strings.HasSuffix(host, suffix) {
		return false
	}
	label := strings.TrimSuffix(host, suffix)
	if strings.Contains(label, ".") {
		return false
	}
	if strings.HasSuffix(label, "-vpc") {
		label = strings.TrimSuffix(label, "-vpc")
	}
	return tencentTCRInstanceNamePattern.MatchString(label)
}

func validateTencentTCRRegistryPath(path, method string, invocation Invocation) error {
	if path == "/v2/" {
		if method != http.MethodGet && method != http.MethodHead {
			return fmt.Errorf("Tencent Cloud TCR Registry version checks require GET or HEAD")
		}
		return nil
	}
	if path == "/v2/_catalog" {
		if method != http.MethodGet {
			return fmt.Errorf("Tencent Cloud TCR Registry catalog requires GET")
		}
		return nil
	}
	if !strings.HasPrefix(path, "/v2/") {
		return fmt.Errorf("Tencent Cloud TCR supports only Docker or OCI Registry /v2 resource paths")
	}
	remainder := strings.TrimPrefix(path, "/v2/")
	end := -1
	marker := ""
	for _, candidate := range []string{"/manifests/", "/blobs/", "/tags/", "/referrers/"} {
		if index := strings.LastIndex(remainder, candidate); index > end {
			end = index
			marker = candidate
		}
	}
	if end <= 0 {
		return fmt.Errorf("Tencent Cloud TCR Registry path does not identify a repository operation")
	}
	repository := remainder[:end]
	if len(repository) > 1024 || !azureACRRepositoryPattern.MatchString(repository) || !strings.Contains(repository, "/") {
		return fmt.Errorf("Tencent Cloud TCR Registry path must include a valid namespace and repository")
	}
	tail := remainder[end+len(marker):]
	switch marker {
	case "/manifests/":
		if tail == "" || strings.Contains(tail, "/") || (method != http.MethodGet && method != http.MethodHead && method != http.MethodPut && method != http.MethodDelete) {
			return fmt.Errorf("Tencent Cloud TCR manifest paths require a reference and GET, HEAD, PUT, or DELETE")
		}
	case "/blobs/":
		return validateTencentTCRBlobPath(tail, method, invocation)
	case "/tags/":
		if tail != "list" || method != http.MethodGet {
			return fmt.Errorf("Tencent Cloud TCR tags paths require GET on /tags/list")
		}
	case "/referrers/":
		if tail == "" || strings.Contains(tail, "/") || method != http.MethodGet {
			return fmt.Errorf("Tencent Cloud TCR referrers paths require a digest and GET")
		}
	}
	return nil
}

func validateTencentTCRBlobPath(tail, method string, invocation Invocation) error {
	if tail == "uploads/" {
		if method != http.MethodPost {
			return fmt.Errorf("Tencent Cloud TCR blob upload creation requires POST")
		}
		digest, hasDigest, err := tencentTCRParameter(invocation, "digest")
		if err != nil {
			return err
		}
		mount, hasMount, err := tencentTCRParameter(invocation, "mount")
		if err != nil {
			return err
		}
		from, hasFrom, err := tencentTCRParameter(invocation, "from")
		if err != nil {
			return err
		}
		if hasMount || hasFrom {
			if !hasMount || !hasFrom || hasDigest || !validGCPArtifactDigest(mount) || !azureACRRepositoryPattern.MatchString(from) || !strings.Contains(from, "/") || invocation.Body != nil || invocation.BodyFile != "" {
				return fmt.Errorf("Tencent Cloud TCR cross-repository blob mounts require matching mount digest and namespaced from repository without an upload body")
			}
			return nil
		}
		if hasDigest {
			if !validGCPArtifactDigest(digest) || (invocation.Body == nil && invocation.BodyFile == "") {
				return fmt.Errorf("Tencent Cloud TCR monolithic blob uploads require a valid digest and upload body")
			}
			return nil
		}
		if invocation.Body != nil || invocation.BodyFile != "" {
			return fmt.Errorf("Tencent Cloud TCR upload creation without a digest must not include a body")
		}
		return nil
	}
	if strings.HasPrefix(tail, "uploads/") {
		uploadID := strings.TrimPrefix(tail, "uploads/")
		if uploadID == "" || strings.Contains(uploadID, "/") || (method != http.MethodGet && method != http.MethodPatch && method != http.MethodPut && method != http.MethodDelete) {
			return fmt.Errorf("Tencent Cloud TCR blob upload sessions require GET, PATCH, PUT, or DELETE")
		}
		if method == http.MethodPatch && invocation.Body == nil && invocation.BodyFile == "" {
			return fmt.Errorf("Tencent Cloud TCR chunk upload PATCH requires a body")
		}
		if method == http.MethodPut {
			digest, ok, err := tencentTCRParameter(invocation, "digest")
			if err != nil {
				return err
			}
			if !ok || !validGCPArtifactDigest(digest) {
				return fmt.Errorf("Tencent Cloud TCR upload completion requires a valid digest")
			}
		}
		return nil
	}
	if tail == "" || strings.Contains(tail, "/") || (method != http.MethodGet && method != http.MethodHead && method != http.MethodDelete) {
		return fmt.Errorf("Tencent Cloud TCR blob paths require a digest and GET, HEAD, or DELETE")
	}
	return nil
}

func tencentTCRParameter(invocation Invocation, name string) (string, bool, error) {
	parsed, err := url.Parse(invocation.URL)
	if err != nil {
		return "", false, fmt.Errorf("Tencent Cloud TCR requires a valid URL")
	}
	values, inURL := parsed.Query()[name]
	if inURL && len(values) != 1 {
		return "", false, fmt.Errorf("Tencent Cloud TCR query parameter %s must occur exactly once", name)
	}
	value := ""
	if inURL {
		value = values[0]
	}
	if parameter, inParameters := invocation.Parameters[name]; inParameters {
		if inURL {
			return "", false, fmt.Errorf("Tencent Cloud TCR query parameter %s must not be duplicated", name)
		}
		text, ok := parameter.(string)
		if !ok {
			return "", false, fmt.Errorf("Tencent Cloud TCR query parameter %s must be a string", name)
		}
		value, inURL = text, true
	}
	if inURL && strings.TrimSpace(value) == "" {
		return "", false, fmt.Errorf("Tencent Cloud TCR query parameter %s must not be empty", name)
	}
	return strings.TrimSpace(value), inURL, nil
}

func invokeTencentTCR(ctx context.Context, adapter *TencentRESTAdapter, invocation Invocation) (InvocationResult, error) {
	target, err := parseTencentTCRInvocation(invocation, adapter.config.AllowedHosts)
	if err != nil {
		return InvocationResult{}, err
	}
	credentials, err := adapter.config.Credentials.Credentials(ctx)
	if err != nil {
		return InvocationResult{}, err
	}
	if credentials.SecretID == "" || credentials.SecretKey == "" {
		return InvocationResult{}, fmt.Errorf("Tencent Cloud credential provider returned incomplete AKSK material")
	}
	temporary, err := adapter.getTencentTCRTemporaryCredential(ctx, target, credentials)
	if err != nil {
		return InvocationResult{}, sanitizeTencentTCRError(err, credentials.SecretID, credentials.SecretKey, credentials.Token)
	}
	body, contentLength, cleanup, err := prepareRESTBody(invocation)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("prepare Tencent Cloud TCR Registry request body: %w", err)
	}
	defer cleanup()
	request, err := http.NewRequestWithContext(ctx, strings.ToUpper(strings.TrimSpace(invocation.Method)), invocation.URL, body)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("build Tencent Cloud TCR Registry request")
	}
	if err := addQueryParameters(request.URL, invocation.Parameters); err != nil {
		return InvocationResult{}, fmt.Errorf("build Tencent Cloud TCR Registry query: %w", err)
	}
	for name, value := range invocation.Headers {
		request.Header.Set(name, value)
	}
	if contentLength >= 0 {
		request.ContentLength = contentLength
	}
	if (invocation.Body != nil || invocation.BodyFile != "") && request.Header.Get("Content-Type") == "" {
		request.Header.Set("Content-Type", invocationContentType(invocation))
	}
	request.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(temporary.Username+":"+temporary.Password)))
	result, err := invokeSignedHTTP(adapter.config.HTTP, adapter.config.MaxBodyBytes, request, invocation, "Tencent Cloud TCR Registry")
	if err != nil {
		return InvocationResult{}, sanitizeTencentTCRError(err, credentials.SecretID, credentials.SecretKey, credentials.Token, temporary.Username, temporary.Password)
	}
	return result, nil
}

func (adapter *TencentRESTAdapter) getTencentTCRTemporaryCredential(ctx context.Context, target tencentTCRTarget, credentials TencentCredentials) (tencentTCRTemporaryCredential, error) {
	payload, err := json.Marshal(struct {
		RegistryID string `json:"RegistryId"`
		TokenType  string `json:"TokenType"`
	}{RegistryID: target.InstanceID, TokenType: "temp"})
	if err != nil {
		return tencentTCRTemporaryCredential{}, fmt.Errorf("encode Tencent Cloud TCR internal temporary-credential request")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://tcr.tencentcloudapi.com/", bytes.NewReader(payload))
	if err != nil {
		return tencentTCRTemporaryCredential{}, fmt.Errorf("build Tencent Cloud TCR internal temporary-credential request")
	}
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	invocation := Invocation{Service: "tcr", Operation: "CreateInstanceToken", APIVersion: tencentTCRAPIVersion, Region: target.Region}
	if err := signTencentTC3(request, sha256Hex(payload), credentials, invocation, adapter.config.Now().UTC()); err != nil {
		return tencentTCRTemporaryCredential{}, fmt.Errorf("sign Tencent Cloud TCR internal temporary-credential request")
	}
	response, err := adapter.config.HTTP.Do(request)
	if err != nil {
		return tencentTCRTemporaryCredential{}, fmt.Errorf("Tencent Cloud TCR internal temporary-credential request failed")
	}
	if response == nil || response.Body == nil {
		return tencentTCRTemporaryCredential{}, fmt.Errorf("Tencent Cloud TCR internal temporary-credential request returned an empty response")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return tencentTCRTemporaryCredential{}, fmt.Errorf("Tencent Cloud TCR internal temporary-credential request returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxTencentTCRTokenBytes+1))
	if err != nil || len(data) > maxTencentTCRTokenBytes {
		return tencentTCRTemporaryCredential{}, fmt.Errorf("read bounded Tencent Cloud TCR internal temporary-credential response")
	}
	var output struct {
		Response struct {
			Username  string `json:"Username"`
			Token     string `json:"Token"`
			TokenID   string `json:"TokenId"`
			ExpTime   int64  `json:"ExpTime"`
			RequestID string `json:"RequestId"`
			Error     *struct {
				Code string `json:"Code"`
			} `json:"Error"`
		} `json:"Response"`
	}
	if json.Unmarshal(data, &output) != nil || output.Response.Error != nil {
		return tencentTCRTemporaryCredential{}, fmt.Errorf("Tencent Cloud TCR internal temporary-credential response is invalid")
	}
	username := strings.TrimSpace(output.Response.Username)
	password := strings.TrimSpace(output.Response.Token)
	if username == "" || password == "" || output.Response.ExpTime <= adapter.config.Now().UTC().UnixMilli() || output.Response.TokenID != "" || len(username) > 1024 || len(password) > maxTencentTCRPasswordSize || strings.ContainsAny(username, ":\r\n") || strings.ContainsAny(password, "\r\n") {
		return tencentTCRTemporaryCredential{}, fmt.Errorf("Tencent Cloud TCR internal temporary-credential response omitted a valid temporary credential")
	}
	return tencentTCRTemporaryCredential{Username: username, Password: password}, nil
}

func sanitizeTencentTCRError(err error, secrets ...string) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	for _, secret := range secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[REDACTED]")
		}
	}
	return fmt.Errorf("%s", message)
}

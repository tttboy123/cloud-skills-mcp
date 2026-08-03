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
	authSchemeBaiduCCR       = "ccr-registry"
	baiduCCRControlHost      = "ccr.bd.baidubce.com"
	baiduCCRPersonalHost     = "registry.baidubce.com"
	baiduCCRPersonalAPIHost  = "ccr.baidubce.com"
	maxBaiduCCRControlBytes  = 1024 * 1024
	maxBaiduCCRPasswordBytes = 64 * 1024
)

var (
	baiduCCRInstanceIDPattern = regexp.MustCompile(`^ccr-[a-z0-9]{8,64}$`)
	baiduCCRUserIDPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{2,127}$`)
	baiduCCREnterpriseHost    = regexp.MustCompile(`^(ccr-[a-z0-9]{8,64})-(pub|vpc)\.cnc(\.bd)?\.([a-z0-9-]+)\.baidubce\.com$`)
)

type baiduCCRTarget struct {
	RegistryHost string
	Region       string
	InstanceID   string
	UserID       string
	Personal     bool
	Repository   string
	Scopes       []string
}

type baiduCCRTemporaryCredential struct {
	Username string
	Password string
}

type baiduCCRBearerChallenge struct {
	Realm   string
	Service string
}

func validateBaiduCCRInvocation(invocation Invocation, allowedHosts []string) error {
	_, err := parseBaiduCCRInvocation(invocation, allowedHosts)
	return err
}

func parseBaiduCCRInvocation(invocation Invocation, allowedHosts []string) (baiduCCRTarget, error) {
	if !strings.EqualFold(strings.TrimSpace(invocation.Service), "ccr") {
		return baiduCCRTarget{}, fmt.Errorf("Baidu CCR Registry requires service=ccr")
	}
	if !identifierPattern.MatchString(invocation.Operation) {
		return baiduCCRTarget{}, fmt.Errorf("Baidu CCR Registry requires a valid operation")
	}
	if invocation.APIVersion != "" || invocation.AuthVersion != "" {
		return baiduCCRTarget{}, fmt.Errorf("Baidu CCR Registry API and auth versions are server-controlled and must be omitted")
	}
	method := strings.ToUpper(strings.TrimSpace(invocation.Method))
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return baiduCCRTarget{}, fmt.Errorf("Baidu CCR Registry requires GET, HEAD, POST, PUT, PATCH, or DELETE")
	}
	parsed, err := url.Parse(invocation.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return baiduCCRTarget{}, fmt.Errorf("Baidu CCR Registry requires a valid HTTPS URL")
	}
	if parsed.Port() != "" && parsed.Port() != "443" {
		return baiduCCRTarget{}, fmt.Errorf("Baidu CCR Registry URL port must be 443")
	}
	for name := range parsed.Query() {
		if isCredentialQueryParameter(name) {
			return baiduCCRTarget{}, fmt.Errorf("caller-supplied credential query parameter %q is forbidden", name)
		}
	}
	for name := range invocation.Parameters {
		if isCredentialQueryParameter(name) {
			return baiduCCRTarget{}, fmt.Errorf("caller-supplied credential query parameter %q is forbidden", name)
		}
	}
	for name := range invocation.Headers {
		if isProtectedHeader(name) {
			return baiduCCRTarget{}, fmt.Errorf("caller-supplied protected header %q is forbidden", name)
		}
	}
	host := strings.ToLower(parsed.Hostname())
	region := strings.ToLower(strings.TrimSpace(invocation.Region))
	instanceID := strings.TrimSpace(invocation.RegistryInstanceID)
	userID := strings.TrimSpace(invocation.RegistryUserID)
	target := baiduCCRTarget{RegistryHost: host, Region: region, InstanceID: instanceID, UserID: userID}
	if host == baiduCCRPersonalHost {
		if region != "" || instanceID != "" || userID != "" {
			return baiduCCRTarget{}, fmt.Errorf("Baidu CCR Personal Registry requires region, registry_instance_id, and registry_user_id to be omitted")
		}
		target.Personal = true
	} else {
		matches := baiduCCREnterpriseHost.FindStringSubmatch(host)
		custom := len(matches) != 5 && isExplicitEndpointHost(host, allowedHosts)
		if len(matches) != 5 && !custom {
			return baiduCCRTarget{}, fmt.Errorf("Baidu CCR Registry requires an exact Personal or Enterprise public, VPC, or operator-pinned custom endpoint")
		}
		if !identifierPattern.MatchString(region) || !baiduCCRInstanceIDPattern.MatchString(instanceID) || !baiduCCRUserIDPattern.MatchString(userID) {
			return baiduCCRTarget{}, fmt.Errorf("Baidu CCR Enterprise Registry requires a valid region, registry_instance_id, and registry_user_id")
		}
		if !custom && (matches[1] != instanceID || matches[4] != region) {
			return baiduCCRTarget{}, fmt.Errorf("Baidu CCR Enterprise instance and region must exactly match the Registry endpoint")
		}
	}
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	if strings.Contains(path, "%") {
		return baiduCCRTarget{}, fmt.Errorf("Baidu CCR Registry paths must use their canonical unescaped form")
	}
	if path == "/service/token" || strings.HasPrefix(path, "/service/token/") || path == "/v2/token" || strings.HasPrefix(path, "/v2/token/") {
		return baiduCCRTarget{}, fmt.Errorf("Baidu CCR Registry credential endpoints are internal and are not exposed through MCP")
	}
	repository, scopes, err := validateBaiduCCRRegistryPlan(path, method, invocation)
	if err != nil {
		return baiduCCRTarget{}, err
	}
	target.Repository, target.Scopes = repository, scopes
	return target, nil
}

func validateBaiduCCRRegistryPlan(path, method string, invocation Invocation) (string, []string, error) {
	if path == "/v2/" {
		if method != http.MethodGet && method != http.MethodHead {
			return "", nil, fmt.Errorf("Baidu CCR Registry version checks require GET or HEAD")
		}
		return "", nil, nil
	}
	if path == "/v2/_catalog" {
		if method != http.MethodGet {
			return "", nil, fmt.Errorf("Baidu CCR Registry catalog requires GET")
		}
		return "", []string{"registry:catalog:*"}, nil
	}
	if !strings.HasPrefix(path, "/v2/") {
		return "", nil, fmt.Errorf("Baidu CCR supports only Docker or OCI Registry /v2 resource paths")
	}
	remainder := strings.TrimPrefix(path, "/v2/")
	end, marker := -1, ""
	for _, candidate := range []string{"/manifests/", "/blobs/", "/tags/", "/referrers/"} {
		if index := strings.LastIndex(remainder, candidate); index > end {
			end, marker = index, candidate
		}
	}
	if end <= 0 {
		return "", nil, fmt.Errorf("Baidu CCR Registry path does not identify a repository operation")
	}
	repository := remainder[:end]
	if len(repository) > 1024 || !azureACRRepositoryPattern.MatchString(repository) || !strings.Contains(repository, "/") {
		return "", nil, fmt.Errorf("Baidu CCR Registry path must include a valid namespace and repository")
	}
	tail := remainder[end+len(marker):]
	action := "pull"
	switch marker {
	case "/manifests/":
		if tail == "" || strings.Contains(tail, "/") || (method != http.MethodGet && method != http.MethodHead && method != http.MethodPut && method != http.MethodDelete) {
			return "", nil, fmt.Errorf("Baidu CCR manifest paths require a reference and GET, HEAD, PUT, or DELETE")
		}
	case "/tags/":
		if tail != "list" || method != http.MethodGet {
			return "", nil, fmt.Errorf("Baidu CCR tags paths require GET on /tags/list")
		}
	case "/referrers/":
		if tail == "" || strings.Contains(tail, "/") || method != http.MethodGet {
			return "", nil, fmt.Errorf("Baidu CCR referrers paths require a digest and GET")
		}
	case "/blobs/":
		if err := validateBaiduCCRBlobPlan(tail, method, invocation); err != nil {
			return "", nil, err
		}
	}
	if method != http.MethodGet && method != http.MethodHead {
		action = "pull,push"
	}
	scopes := []string{"repository:" + repository + ":" + action}
	if strings.HasSuffix(path, "/blobs/uploads/") && method == http.MethodPost {
		mount, hasMount, err := baiduCCRParameter(invocation, "mount")
		if err != nil {
			return "", nil, err
		}
		from, hasFrom, err := baiduCCRParameter(invocation, "from")
		if err != nil {
			return "", nil, err
		}
		if hasMount || hasFrom {
			if !hasMount || !hasFrom || !validGCPArtifactDigest(mount) || !azureACRRepositoryPattern.MatchString(from) || !strings.Contains(from, "/") || invocation.Body != nil || invocation.BodyFile != "" {
				return "", nil, fmt.Errorf("Baidu CCR cross-repository blob mounts require matching mount digest and namespaced from repository without an upload body")
			}
			scopes = append(scopes, "repository:"+from+":pull")
		}
	}
	return repository, scopes, nil
}

func validateBaiduCCRBlobPlan(tail, method string, invocation Invocation) error {
	if tail == "uploads/" {
		if method != http.MethodPost {
			return fmt.Errorf("Baidu CCR blob upload creation requires POST")
		}
		digest, hasDigest, err := baiduCCRParameter(invocation, "digest")
		if err != nil {
			return err
		}
		_, hasMount, err := baiduCCRParameter(invocation, "mount")
		if err != nil {
			return err
		}
		_, hasFrom, err := baiduCCRParameter(invocation, "from")
		if err != nil {
			return err
		}
		if hasMount || hasFrom {
			if hasDigest {
				return fmt.Errorf("Baidu CCR cross-repository blob mounts must not include digest")
			}
			return nil
		}
		if hasDigest {
			if !validGCPArtifactDigest(digest) || (invocation.Body == nil && invocation.BodyFile == "") {
				return fmt.Errorf("Baidu CCR monolithic blob uploads require a valid digest and upload body")
			}
			return nil
		}
		if invocation.Body != nil || invocation.BodyFile != "" {
			return fmt.Errorf("Baidu CCR upload creation without a digest must not include a body")
		}
		return nil
	}
	if strings.HasPrefix(tail, "uploads/") {
		uploadID := strings.TrimPrefix(tail, "uploads/")
		if uploadID == "" || strings.Contains(uploadID, "/") || (method != http.MethodGet && method != http.MethodPatch && method != http.MethodPut && method != http.MethodDelete) {
			return fmt.Errorf("Baidu CCR blob upload sessions require GET, PATCH, PUT, or DELETE")
		}
		if method == http.MethodPatch && invocation.Body == nil && invocation.BodyFile == "" {
			return fmt.Errorf("Baidu CCR chunk upload PATCH requires a body")
		}
		if method == http.MethodPut {
			digest, ok, err := baiduCCRParameter(invocation, "digest")
			if err != nil {
				return err
			}
			if !ok || !validGCPArtifactDigest(digest) {
				return fmt.Errorf("Baidu CCR upload completion requires a valid digest")
			}
		}
		return nil
	}
	if tail == "" || strings.Contains(tail, "/") || (method != http.MethodGet && method != http.MethodHead && method != http.MethodDelete) {
		return fmt.Errorf("Baidu CCR blob paths require a digest and GET, HEAD, or DELETE")
	}
	return nil
}

func baiduCCRParameter(invocation Invocation, name string) (string, bool, error) {
	parsed, err := url.Parse(invocation.URL)
	if err != nil {
		return "", false, fmt.Errorf("Baidu CCR requires a valid URL")
	}
	values, inURL := parsed.Query()[name]
	if inURL && len(values) != 1 {
		return "", false, fmt.Errorf("Baidu CCR query parameter %s must occur exactly once", name)
	}
	value := ""
	if inURL {
		value = values[0]
	}
	if parameter, inParameters := invocation.Parameters[name]; inParameters {
		if inURL {
			return "", false, fmt.Errorf("Baidu CCR query parameter %s must not be duplicated", name)
		}
		text, ok := parameter.(string)
		if !ok {
			return "", false, fmt.Errorf("Baidu CCR query parameter %s must be a string", name)
		}
		value, inURL = text, true
	}
	if inURL && strings.TrimSpace(value) == "" {
		return "", false, fmt.Errorf("Baidu CCR query parameter %s must not be empty", name)
	}
	return strings.TrimSpace(value), inURL, nil
}

func invokeBaiduCCR(ctx context.Context, adapter *BaiduRESTAdapter, invocation Invocation) (InvocationResult, error) {
	target, err := parseBaiduCCRInvocation(invocation, adapter.config.AllowedHosts)
	if err != nil {
		return InvocationResult{}, err
	}
	credentials, err := adapter.config.Credentials.Credentials(ctx)
	if err != nil {
		return InvocationResult{}, err
	}
	if credentials.AccessKeyID == "" || credentials.SecretAccessKey == "" {
		return InvocationResult{}, fmt.Errorf("Baidu BCE credential provider returned incomplete AKSK material")
	}
	temporary, err := adapter.getBaiduCCRTemporaryCredential(ctx, target, credentials)
	if err != nil {
		return InvocationResult{}, sanitizeBaiduCCRError(err, credentials.AccessKeyID, credentials.SecretAccessKey, credentials.SessionToken)
	}
	challenge, err := adapter.getBaiduCCRBearerChallenge(ctx, target)
	if err != nil {
		return InvocationResult{}, sanitizeBaiduCCRError(err, credentials.AccessKeyID, credentials.SecretAccessKey, credentials.SessionToken, temporary.Username, temporary.Password)
	}
	bearer, err := adapter.getBaiduCCRBearerToken(ctx, target, temporary, challenge)
	if err != nil {
		return InvocationResult{}, sanitizeBaiduCCRError(err, credentials.AccessKeyID, credentials.SecretAccessKey, credentials.SessionToken, temporary.Username, temporary.Password)
	}
	body, contentLength, cleanup, err := prepareRESTBody(invocation)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("prepare Baidu CCR Registry request body: %w", err)
	}
	defer cleanup()
	request, err := http.NewRequestWithContext(ctx, strings.ToUpper(strings.TrimSpace(invocation.Method)), invocation.URL, body)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("build Baidu CCR Registry request")
	}
	if err := addQueryParameters(request.URL, invocation.Parameters); err != nil {
		return InvocationResult{}, fmt.Errorf("build Baidu CCR Registry query: %w", err)
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
	request.Header.Set("Authorization", "Bearer "+bearer)
	result, err := invokeSignedHTTP(adapter.config.HTTP, adapter.config.MaxBodyBytes, request, invocation, "Baidu CCR Registry")
	if err != nil {
		return InvocationResult{}, sanitizeBaiduCCRError(err, credentials.AccessKeyID, credentials.SecretAccessKey, credentials.SessionToken, temporary.Username, temporary.Password, bearer)
	}
	return result, nil
}

func (adapter *BaiduRESTAdapter) getBaiduCCRTemporaryCredential(ctx context.Context, target baiduCCRTarget, credentials BCECredentials) (baiduCCRTemporaryCredential, error) {
	if target.Personal {
		userData, err := adapter.doBaiduCCRSignedJSON(ctx, http.MethodGet, "https://"+baiduCCRPersonalAPIHost+"/v1/ccr/user", nil, credentials)
		if err != nil {
			return baiduCCRTemporaryCredential{}, fmt.Errorf("Baidu CCR Personal internal user lookup failed: %w", err)
		}
		var user struct {
			Username string `json:"username"`
		}
		if json.Unmarshal(userData, &user) != nil {
			return baiduCCRTemporaryCredential{}, fmt.Errorf("Baidu CCR Personal internal user response is invalid")
		}
		tokenData, err := adapter.doBaiduCCRSignedJSON(ctx, http.MethodPost, "https://"+baiduCCRPersonalAPIHost+"/v1/ccr/token", []byte(`{"duration":1}`), credentials)
		if err != nil {
			return baiduCCRTemporaryCredential{}, fmt.Errorf("Baidu CCR Personal internal temporary-key request failed: %w", err)
		}
		var token struct {
			BeginTime  string `json:"beginTime"`
			ExpireTime string `json:"expireTime"`
			Token      string `json:"token"`
		}
		if json.Unmarshal(tokenData, &token) != nil || strings.TrimSpace(token.BeginTime) == "" || strings.TrimSpace(token.ExpireTime) == "" {
			return baiduCCRTemporaryCredential{}, fmt.Errorf("Baidu CCR Personal internal temporary-key response is invalid")
		}
		return validateBaiduCCRTemporaryCredential(user.Username, token.Token)
	}
	profileURL := "https://" + baiduCCRControlHost + "/v1/users/profile?userId=" + url.QueryEscape(target.UserID)
	profileData, err := adapter.doBaiduCCRSignedJSON(ctx, http.MethodGet, profileURL, nil, credentials)
	if err != nil {
		return baiduCCRTemporaryCredential{}, fmt.Errorf("Baidu CCR Enterprise internal user lookup failed: %w", err)
	}
	var profile struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(profileData, &profile) != nil {
		return baiduCCRTemporaryCredential{}, fmt.Errorf("Baidu CCR Enterprise internal user response is invalid")
	}
	credentialURL := "https://" + baiduCCRControlHost + "/v1/instances/" + url.PathEscape(target.InstanceID) + "/credential"
	credentialData, err := adapter.doBaiduCCRSignedJSON(ctx, http.MethodPost, credentialURL, []byte(`{"duration":1}`), credentials)
	if err != nil {
		return baiduCCRTemporaryCredential{}, fmt.Errorf("Baidu CCR Enterprise internal temporary-password request failed: %w", err)
	}
	var output struct {
		Password string `json:"password"`
	}
	if json.Unmarshal(credentialData, &output) != nil {
		return baiduCCRTemporaryCredential{}, fmt.Errorf("Baidu CCR Enterprise internal temporary-password response is invalid")
	}
	return validateBaiduCCRTemporaryCredential(profile.Name, output.Password)
}

func validateBaiduCCRTemporaryCredential(username, password string) (baiduCCRTemporaryCredential, error) {
	username, password = strings.TrimSpace(username), strings.TrimSpace(password)
	if username == "" || password == "" || len(username) > 1024 || len(password) > maxBaiduCCRPasswordBytes || strings.ContainsAny(username, ":\r\n") || strings.ContainsAny(password, "\r\n") {
		return baiduCCRTemporaryCredential{}, fmt.Errorf("Baidu CCR internal response omitted a valid temporary credential")
	}
	return baiduCCRTemporaryCredential{Username: username, Password: password}, nil
}

func (adapter *BaiduRESTAdapter) doBaiduCCRSignedJSON(ctx context.Context, method, rawURL string, payload []byte, credentials BCECredentials) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, fmt.Errorf("build internal signed request")
	}
	timestamp := adapter.config.Now().UTC()
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Set("x-bce-date", timestamp.Format("2006-01-02T15:04:05Z"))
	if credentials.SessionToken != "" {
		request.Header.Set("x-bce-security-token", credentials.SessionToken)
	}
	authorization, err := signBCERequest(request, credentials, timestamp, bceExpiry)
	if err != nil {
		return nil, fmt.Errorf("sign internal request")
	}
	request.Header.Set("Authorization", authorization)
	response, err := adapter.config.HTTP.Do(request)
	if err != nil {
		return nil, fmt.Errorf("internal signed request failed")
	}
	if response == nil || response.Body == nil {
		return nil, fmt.Errorf("internal signed request returned an empty response")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("internal signed request returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBaiduCCRControlBytes+1))
	if err != nil || len(data) > maxBaiduCCRControlBytes {
		return nil, fmt.Errorf("read bounded internal signed response")
	}
	return data, nil
}

func (adapter *BaiduRESTAdapter) getBaiduCCRBearerChallenge(ctx context.Context, target baiduCCRTarget) (baiduCCRBearerChallenge, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+target.RegistryHost+"/v2/", nil)
	if err != nil {
		return baiduCCRBearerChallenge{}, fmt.Errorf("build Baidu CCR Registry challenge request")
	}
	response, err := adapter.config.HTTP.Do(request)
	if err != nil {
		return baiduCCRBearerChallenge{}, fmt.Errorf("Baidu CCR Registry challenge request failed")
	}
	if response == nil || response.Body == nil {
		return baiduCCRBearerChallenge{}, fmt.Errorf("Baidu CCR Registry challenge returned an empty response")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		return baiduCCRBearerChallenge{}, fmt.Errorf("Baidu CCR Registry challenge returned HTTP %d instead of 401", response.StatusCode)
	}
	values, err := parseDockerBearerChallenge(response.Header.Get("Www-Authenticate"))
	if err != nil {
		return baiduCCRBearerChallenge{}, fmt.Errorf("Baidu CCR Registry returned an unsafe Bearer challenge")
	}
	parsed, err := url.Parse(values["realm"])
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Fragment != "" || parsed.RawQuery != "" || parsed.EscapedPath() != "/service/token" || (parsed.Port() != "" && parsed.Port() != "443") || !strings.EqualFold(parsed.Hostname(), target.RegistryHost) || values["service"] != "harbor-registry" {
		return baiduCCRBearerChallenge{}, fmt.Errorf("Baidu CCR Registry returned an unsafe Bearer challenge")
	}
	return baiduCCRBearerChallenge{Realm: parsed.String(), Service: values["service"]}, nil
}

func (adapter *BaiduRESTAdapter) getBaiduCCRBearerToken(ctx context.Context, target baiduCCRTarget, temporary baiduCCRTemporaryCredential, challenge baiduCCRBearerChallenge) (string, error) {
	parsed, _ := url.Parse(challenge.Realm)
	query := parsed.Query()
	query.Set("service", challenge.Service)
	query.Del("scope")
	for _, scope := range target.Scopes {
		query.Add("scope", scope)
	}
	parsed.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", fmt.Errorf("build Baidu CCR internal Bearer-token request")
	}
	request.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(temporary.Username+":"+temporary.Password)))
	response, err := adapter.config.HTTP.Do(request)
	if err != nil {
		return "", fmt.Errorf("Baidu CCR internal Bearer-token request failed")
	}
	if response == nil || response.Body == nil {
		return "", fmt.Errorf("Baidu CCR internal Bearer-token request returned an empty response")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("Baidu CCR internal Bearer-token request returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBaiduCCRControlBytes+1))
	if err != nil || len(data) > maxBaiduCCRControlBytes {
		return "", fmt.Errorf("read bounded Baidu CCR internal Bearer-token response")
	}
	var output struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if json.Unmarshal(data, &output) != nil {
		return "", fmt.Errorf("Baidu CCR internal Bearer-token response is invalid")
	}
	token := strings.TrimSpace(output.Token)
	if token == "" {
		token = strings.TrimSpace(output.AccessToken)
	}
	if token == "" || len(token) > maxBaiduCCRPasswordBytes || strings.ContainsAny(token, "\r\n") {
		return "", fmt.Errorf("Baidu CCR internal Bearer-token response omitted a valid token")
	}
	return token, nil
}

func sanitizeBaiduCCRError(err error, secrets ...string) error {
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

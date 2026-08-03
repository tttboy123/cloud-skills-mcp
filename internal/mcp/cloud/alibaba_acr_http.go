package cloud

import (
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
	alibabaACRAPIVersion       = "2018-12-01"
	maxAlibabaACRAuthBytes     = 1024 * 1024
	maxAlibabaACRBearerBytes   = 64 * 1024
	maxAlibabaACRBlobRedirects = 3
)

var (
	alibabaACRRegistryHostPattern = regexp.MustCompile(`^([a-z](?:[a-z0-9-]{0,61}[a-z0-9])?)-registry(-vpc)?\.([a-z0-9-]+)\.cr\.aliyuncs\.com$`)
	alibabaACRInstanceIDPattern   = regexp.MustCompile(`^cri-[A-Za-z0-9]{4,64}$`)
	alibabaACROSSBucketPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)
)

type alibabaACRTarget struct {
	RegistryHost string
	Region       string
	InstanceID   string
	VPC          bool
	Custom       bool
	Repository   string
	Scopes       []string
}

type alibabaACRTemporaryCredential struct {
	Username string
	Password string
}

func validateAlibabaACRInvocation(invocation Invocation) error {
	_, err := parseAlibabaACRInvocationWithEndpointHosts(invocation, nil)
	return err
}

func parseAlibabaACRInvocation(invocation Invocation) (alibabaACRTarget, error) {
	return parseAlibabaACRInvocationWithEndpointHosts(invocation, nil)
}

func validateAlibabaACRInvocationWithEndpointHosts(invocation Invocation, allowedHosts []string) error {
	_, err := parseAlibabaACRInvocationWithEndpointHosts(invocation, allowedHosts)
	return err
}

func parseAlibabaACRInvocationWithEndpointHosts(invocation Invocation, allowedHosts []string) (alibabaACRTarget, error) {
	if !identifierPattern.MatchString(invocation.Operation) {
		return alibabaACRTarget{}, fmt.Errorf("Alibaba Cloud ACR Registry requires a valid operation")
	}
	if !strings.EqualFold(strings.TrimSpace(invocation.Service), "acr") {
		return alibabaACRTarget{}, fmt.Errorf("Alibaba Cloud ACR Registry requires service=acr")
	}
	if invocation.APIVersion != "" {
		return alibabaACRTarget{}, fmt.Errorf("Alibaba Cloud ACR Registry api_version is server-controlled and must be omitted")
	}
	region := strings.ToLower(strings.TrimSpace(invocation.Region))
	if !identifierPattern.MatchString(region) {
		return alibabaACRTarget{}, fmt.Errorf("Alibaba Cloud ACR Registry requires a valid region")
	}
	instanceID := strings.TrimSpace(invocation.RegistryInstanceID)
	if !alibabaACRInstanceIDPattern.MatchString(instanceID) {
		return alibabaACRTarget{}, fmt.Errorf("Alibaba Cloud ACR Registry requires a valid Enterprise Edition registry_instance_id")
	}
	method := strings.ToUpper(strings.TrimSpace(invocation.Method))
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return alibabaACRTarget{}, fmt.Errorf("Alibaba Cloud ACR Registry requires GET, HEAD, POST, PUT, PATCH, or DELETE")
	}
	parsed, err := url.Parse(invocation.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return alibabaACRTarget{}, fmt.Errorf("Alibaba Cloud ACR Registry requires a valid HTTPS URL")
	}
	if parsed.Port() != "" && parsed.Port() != "443" {
		return alibabaACRTarget{}, fmt.Errorf("Alibaba Cloud ACR Registry URL port must be 443")
	}
	for name := range parsed.Query() {
		if isCredentialQueryParameter(name) {
			return alibabaACRTarget{}, fmt.Errorf("caller-supplied credential query parameter %q is forbidden", name)
		}
	}
	for name := range invocation.Parameters {
		if isCredentialQueryParameter(name) {
			return alibabaACRTarget{}, fmt.Errorf("caller-supplied credential query parameter %q is forbidden", name)
		}
	}
	for name := range invocation.Headers {
		if isProtectedHeader(name) {
			return alibabaACRTarget{}, fmt.Errorf("caller-supplied protected header %q is forbidden", name)
		}
	}
	host := strings.ToLower(parsed.Hostname())
	matches := alibabaACRRegistryHostPattern.FindStringSubmatch(host)
	custom := len(matches) != 4 && isExplicitEndpointHost(host, allowedHosts)
	if len(matches) != 4 && !custom {
		return alibabaACRTarget{}, fmt.Errorf("Alibaba Cloud ACR Registry requires an exact Enterprise Edition public or VPC endpoint")
	}
	if !custom && matches[3] != region {
		return alibabaACRTarget{}, fmt.Errorf("Alibaba Cloud ACR region must exactly match the Registry endpoint")
	}
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	if strings.Contains(path, "%") {
		return alibabaACRTarget{}, fmt.Errorf("Alibaba Cloud ACR Registry paths must use their canonical unescaped form")
	}
	repository, scope, err := validateAlibabaACRRegistryPath(path, method)
	if err != nil {
		return alibabaACRTarget{}, err
	}
	scopes := make([]string, 0, 2)
	if scope != "" {
		scopes = append(scopes, scope)
	}
	if strings.HasSuffix(path, "/blobs/uploads/") && method == http.MethodPost {
		mount, hasMount, err := alibabaACRParameter(invocation, "mount")
		if err != nil {
			return alibabaACRTarget{}, err
		}
		from, hasFrom, err := alibabaACRParameter(invocation, "from")
		if err != nil {
			return alibabaACRTarget{}, err
		}
		if hasMount || hasFrom {
			if !hasMount || !hasFrom || !validGCPArtifactDigest(mount) || !azureACRRepositoryPattern.MatchString(from) || invocation.Body != nil || invocation.BodyFile != "" {
				return alibabaACRTarget{}, fmt.Errorf("Alibaba Cloud ACR cross-repository blob mounts require matching mount digest and from repository without an upload body")
			}
			scopes = append(scopes, "repository:"+from+":pull")
		}
	}
	vpc := !custom && matches[2] != ""
	return alibabaACRTarget{RegistryHost: host, Region: region, InstanceID: instanceID, VPC: vpc, Custom: custom, Repository: repository, Scopes: scopes}, nil
}

func alibabaACRParameter(invocation Invocation, name string) (string, bool, error) {
	parsed, err := url.Parse(invocation.URL)
	if err != nil {
		return "", false, fmt.Errorf("Alibaba Cloud ACR requires a valid URL")
	}
	values, inURL := parsed.Query()[name]
	if inURL && len(values) != 1 {
		return "", false, fmt.Errorf("Alibaba Cloud ACR query parameter %s must occur exactly once", name)
	}
	value := ""
	if inURL {
		value = values[0]
	}
	if parameter, inParameters := invocation.Parameters[name]; inParameters {
		if inURL {
			return "", false, fmt.Errorf("Alibaba Cloud ACR query parameter %s must not be duplicated", name)
		}
		text, ok := parameter.(string)
		if !ok {
			return "", false, fmt.Errorf("Alibaba Cloud ACR query parameter %s must be a string", name)
		}
		value, inURL = text, true
	}
	if inURL && strings.TrimSpace(value) == "" {
		return "", false, fmt.Errorf("Alibaba Cloud ACR query parameter %s must not be empty", name)
	}
	return strings.TrimSpace(value), inURL, nil
}

func validateAlibabaACRRegistryPath(path, method string) (string, string, error) {
	if path == "/v2/" {
		if method != http.MethodGet && method != http.MethodHead {
			return "", "", fmt.Errorf("Alibaba Cloud ACR Registry version checks require GET or HEAD")
		}
		return "", "", nil
	}
	if path == "/v2/_catalog" {
		if method != http.MethodGet {
			return "", "", fmt.Errorf("Alibaba Cloud ACR Registry catalog requires GET")
		}
		return "", "registry:catalog:*", nil
	}
	if !strings.HasPrefix(path, "/v2/") || path == "/v2/token" {
		return "", "", fmt.Errorf("Alibaba Cloud ACR supports only Docker or OCI Registry /v2 resource paths")
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
		return "", "", fmt.Errorf("Alibaba Cloud ACR Registry path does not identify a repository operation")
	}
	repository := remainder[:end]
	if len(repository) > 256 || !azureACRRepositoryPattern.MatchString(repository) {
		return "", "", fmt.Errorf("Alibaba Cloud ACR Registry path contains an invalid repository name")
	}
	tail := remainder[end+len(marker):]
	scopeAction := "pull"
	switch marker {
	case "/manifests/":
		if tail == "" || strings.Contains(tail, "/") || (method != http.MethodGet && method != http.MethodHead && method != http.MethodPut && method != http.MethodDelete) {
			return "", "", fmt.Errorf("Alibaba Cloud ACR manifest paths require a reference and GET, HEAD, PUT, or DELETE")
		}
	case "/blobs/":
		if tail == "uploads/" {
			if method != http.MethodPost {
				return "", "", fmt.Errorf("Alibaba Cloud ACR blob upload creation requires POST")
			}
		} else if strings.HasPrefix(tail, "uploads/") {
			uploadID := strings.TrimPrefix(tail, "uploads/")
			if uploadID == "" || strings.Contains(uploadID, "/") || (method != http.MethodGet && method != http.MethodPatch && method != http.MethodPut && method != http.MethodDelete) {
				return "", "", fmt.Errorf("Alibaba Cloud ACR blob upload sessions require GET, PATCH, PUT, or DELETE")
			}
			scopeAction = "pull,push"
		} else if tail == "" || strings.Contains(tail, "/") || (method != http.MethodGet && method != http.MethodHead && method != http.MethodDelete) {
			return "", "", fmt.Errorf("Alibaba Cloud ACR blob paths require a digest and GET, HEAD, or DELETE")
		}
	case "/tags/":
		if tail != "list" || method != http.MethodGet {
			return "", "", fmt.Errorf("Alibaba Cloud ACR Registry tags paths require GET on /tags/list")
		}
	case "/referrers/":
		if tail == "" || strings.Contains(tail, "/") || method != http.MethodGet {
			return "", "", fmt.Errorf("Alibaba Cloud ACR referrers paths require a digest and GET")
		}
	}
	if method != http.MethodGet && method != http.MethodHead {
		scopeAction = "pull,push"
	}
	return repository, "repository:" + repository + ":" + scopeAction, nil
}

func invokeAlibabaACR(ctx context.Context, adapter *AlibabaRESTAdapter, invocation Invocation) (InvocationResult, error) {
	target, err := parseAlibabaACRInvocationWithEndpointHosts(invocation, adapter.config.AllowedHosts)
	if err != nil {
		return InvocationResult{}, err
	}
	credentials, err := adapter.config.Credentials.Credentials(ctx)
	if err != nil {
		return InvocationResult{}, err
	}
	if credentials.AccessKeyID == "" || credentials.AccessKeySecret == "" {
		return InvocationResult{}, fmt.Errorf("Alibaba Cloud credential provider returned incomplete AKSK material")
	}
	temporary, err := adapter.getAlibabaACRTemporaryCredential(ctx, target, credentials)
	if err != nil {
		return InvocationResult{}, sanitizeAlibabaACRError(err, credentials.AccessKeyID, credentials.AccessKeySecret, credentials.SecurityToken)
	}
	challenge, err := adapter.getAlibabaACRBearerChallenge(ctx, target)
	if err != nil {
		return InvocationResult{}, sanitizeAlibabaACRError(err, credentials.AccessKeyID, credentials.AccessKeySecret, credentials.SecurityToken, temporary.Username, temporary.Password)
	}
	bearer, err := adapter.getAlibabaACRBearerToken(ctx, target, temporary, challenge)
	if err != nil {
		return InvocationResult{}, sanitizeAlibabaACRError(err, credentials.AccessKeyID, credentials.AccessKeySecret, credentials.SecurityToken, temporary.Username, temporary.Password)
	}
	body, contentLength, cleanup, err := prepareRESTBody(invocation)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("prepare Alibaba Cloud ACR Registry request body: %w", err)
	}
	defer cleanup()
	request, err := http.NewRequestWithContext(ctx, strings.ToUpper(strings.TrimSpace(invocation.Method)), invocation.URL, body)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("build Alibaba Cloud ACR Registry request")
	}
	if err := addQueryParameters(request.URL, invocation.Parameters); err != nil {
		return InvocationResult{}, fmt.Errorf("build Alibaba Cloud ACR Registry query: %w", err)
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
	response, err := adapter.config.HTTP.Do(request)
	if err != nil {
		return InvocationResult{}, sanitizeAlibabaACRError(fmt.Errorf("Alibaba Cloud ACR Registry request failed: %w", err), credentials.AccessKeyID, credentials.AccessKeySecret, credentials.SecurityToken, temporary.Username, temporary.Password, bearer)
	}
	response, err = adapter.followAlibabaACRBlobRedirects(ctx, invocation, target, request, response)
	if err != nil {
		return InvocationResult{}, sanitizeAlibabaACRError(err, credentials.AccessKeyID, credentials.AccessKeySecret, credentials.SecurityToken, temporary.Username, temporary.Password, bearer)
	}
	output, err := readRESTResponseWithFile(response, adapter.config.MaxBodyBytes, invocation.ResponseFile, invocation.MaxResponseFileBytes)
	if err != nil {
		return InvocationResult{}, sanitizeAlibabaACRError(err, credentials.AccessKeyID, credentials.AccessKeySecret, credentials.SecurityToken, temporary.Username, temporary.Password, bearer)
	}
	return InvocationResult{Output: output, RequestID: responseRequestID(response.Header)}, nil
}

func (adapter *AlibabaRESTAdapter) getAlibabaACRTemporaryCredential(ctx context.Context, target alibabaACRTarget, credentials AlibabaCredentials) (alibabaACRTemporaryCredential, error) {
	endpointPrefix := "cr"
	if target.VPC {
		endpointPrefix = "cr-vpc"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+endpointPrefix+"."+target.Region+".aliyuncs.com/?InstanceId="+url.QueryEscape(target.InstanceID), nil)
	if err != nil {
		return alibabaACRTemporaryCredential{}, fmt.Errorf("build Alibaba Cloud ACR internal authorization request")
	}
	invocation := Invocation{Operation: "GetAuthorizationToken", APIVersion: alibabaACRAPIVersion}
	if err := signAlibabaRPCV2(request, credentials, invocation, adapter.config.Now().UTC(), adapter.config.Nonce()); err != nil {
		return alibabaACRTemporaryCredential{}, fmt.Errorf("sign Alibaba Cloud ACR internal authorization request")
	}
	response, err := adapter.config.HTTP.Do(request)
	if err != nil {
		return alibabaACRTemporaryCredential{}, fmt.Errorf("Alibaba Cloud ACR internal authorization request failed")
	}
	if response == nil || response.Body == nil {
		return alibabaACRTemporaryCredential{}, fmt.Errorf("Alibaba Cloud ACR internal authorization returned an empty response")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return alibabaACRTemporaryCredential{}, fmt.Errorf("Alibaba Cloud ACR internal authorization returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxAlibabaACRAuthBytes+1))
	if err != nil || len(data) > maxAlibabaACRAuthBytes {
		return alibabaACRTemporaryCredential{}, fmt.Errorf("read bounded Alibaba Cloud ACR internal authorization response")
	}
	var output struct {
		Code               string `json:"Code"`
		IsSuccess          bool   `json:"IsSuccess"`
		TempUsername       string `json:"TempUsername"`
		AuthorizationToken string `json:"AuthorizationToken"`
	}
	if json.Unmarshal(data, &output) != nil || !output.IsSuccess || !strings.EqualFold(strings.TrimSpace(output.Code), "success") {
		return alibabaACRTemporaryCredential{}, fmt.Errorf("Alibaba Cloud ACR internal authorization returned an invalid response")
	}
	output.TempUsername = strings.TrimSpace(output.TempUsername)
	output.AuthorizationToken = strings.TrimSpace(output.AuthorizationToken)
	if output.TempUsername == "" || output.AuthorizationToken == "" || len(output.TempUsername) > 1024 || len(output.AuthorizationToken) > maxAlibabaACRBearerBytes || strings.ContainsAny(output.TempUsername, "\r\n:") || strings.ContainsAny(output.AuthorizationToken, "\r\n") {
		return alibabaACRTemporaryCredential{}, fmt.Errorf("Alibaba Cloud ACR internal authorization omitted a valid temporary credential")
	}
	return alibabaACRTemporaryCredential{Username: output.TempUsername, Password: output.AuthorizationToken}, nil
}

type alibabaACRBearerChallenge struct {
	Realm   string
	Service string
}

func (adapter *AlibabaRESTAdapter) getAlibabaACRBearerChallenge(ctx context.Context, target alibabaACRTarget) (alibabaACRBearerChallenge, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+target.RegistryHost+"/v2/", nil)
	if err != nil {
		return alibabaACRBearerChallenge{}, fmt.Errorf("build Alibaba Cloud ACR Registry challenge request")
	}
	response, err := adapter.config.HTTP.Do(request)
	if err != nil {
		return alibabaACRBearerChallenge{}, fmt.Errorf("Alibaba Cloud ACR Registry challenge request failed")
	}
	if response == nil || response.Body == nil {
		return alibabaACRBearerChallenge{}, fmt.Errorf("Alibaba Cloud ACR Registry challenge returned an empty response")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		return alibabaACRBearerChallenge{}, fmt.Errorf("Alibaba Cloud ACR Registry challenge returned HTTP %d instead of 401", response.StatusCode)
	}
	values, err := parseDockerBearerChallenge(response.Header.Get("Www-Authenticate"))
	if err != nil {
		return alibabaACRBearerChallenge{}, fmt.Errorf("Alibaba Cloud ACR Registry returned an unsafe Bearer challenge")
	}
	realm, service := values["realm"], values["service"]
	parsed, err := url.Parse(realm)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Fragment != "" || parsed.RawQuery != "" || parsed.EscapedPath() != "/auth" || (parsed.Port() != "" && parsed.Port() != "443") {
		return alibabaACRBearerChallenge{}, fmt.Errorf("Alibaba Cloud ACR Registry returned an unsafe Bearer challenge")
	}
	authHost := strings.ToLower(parsed.Hostname())
	allowedAuthHost := isAllowedAlibabaACRAuthHost(authHost, target)
	if !allowedAuthHost || !validAlibabaACRService(service, target) {
		return alibabaACRBearerChallenge{}, fmt.Errorf("Alibaba Cloud ACR Registry returned an unsafe Bearer challenge")
	}
	return alibabaACRBearerChallenge{Realm: parsed.String(), Service: service}, nil
}

func isAllowedAlibabaACRAuthHost(authHost string, target alibabaACRTarget) bool {
	if authHost == target.RegistryHost {
		return true
	}
	if !target.VPC || target.Custom {
		if authHost == "dockerauth."+target.Region+".aliyuncs.com" || authHost == "dockerauth-ee."+target.Region+".aliyuncs.com" {
			return true
		}
		if target.Region == "cn-zhangjiakou" && authHost == "dockerauth-cn-zhangjiakou.aliyuncs.com" {
			return true
		}
	}
	if target.VPC || target.Custom {
		return authHost == "dockerauth-vpc."+target.Region+".aliyuncs.com" || authHost == "dockerauth-ee-vpc."+target.Region+".aliyuncs.com"
	}
	return false
}

func validAlibabaACRService(service string, target alibabaACRTarget) bool {
	if len(service) == 0 || len(service) > 512 || strings.ContainsAny(service, "\r\n&=?#") {
		return false
	}
	parts := strings.Split(service, ":")
	return len(parts) >= 5 && parts[0] == "registry.aliyuncs.com" && parts[1] == target.Region && parts[3] == target.InstanceID
}

func parseDockerBearerChallenge(header string) (map[string]string, error) {
	header = strings.TrimSpace(header)
	if len(header) < 7 || !strings.EqualFold(header[:6], "Bearer") || header[6] != ' ' {
		return nil, fmt.Errorf("Bearer challenge is missing")
	}
	remainder := strings.TrimSpace(header[7:])
	values := make(map[string]string)
	for remainder != "" {
		equals := strings.IndexByte(remainder, '=')
		if equals <= 0 {
			return nil, fmt.Errorf("Bearer challenge parameter is invalid")
		}
		name := strings.ToLower(strings.TrimSpace(remainder[:equals]))
		if name == "" || !identifierPattern.MatchString(name) || values[name] != "" {
			return nil, fmt.Errorf("Bearer challenge parameter is invalid")
		}
		remainder = strings.TrimSpace(remainder[equals+1:])
		if len(remainder) < 2 || remainder[0] != '"' {
			return nil, fmt.Errorf("Bearer challenge value is invalid")
		}
		end := strings.IndexByte(remainder[1:], '"')
		if end < 0 {
			return nil, fmt.Errorf("Bearer challenge value is invalid")
		}
		end++
		value := remainder[1:end]
		if value == "" || strings.ContainsAny(value, "\\\r\n") {
			return nil, fmt.Errorf("Bearer challenge value is invalid")
		}
		values[name] = value
		remainder = strings.TrimSpace(remainder[end+1:])
		if remainder == "" {
			break
		}
		if remainder[0] != ',' {
			return nil, fmt.Errorf("Bearer challenge separator is invalid")
		}
		remainder = strings.TrimSpace(remainder[1:])
	}
	if values["realm"] == "" || values["service"] == "" {
		return nil, fmt.Errorf("Bearer challenge is incomplete")
	}
	return values, nil
}

func (adapter *AlibabaRESTAdapter) getAlibabaACRBearerToken(ctx context.Context, target alibabaACRTarget, temporary alibabaACRTemporaryCredential, challenge alibabaACRBearerChallenge) (string, error) {
	parsed, _ := url.Parse(challenge.Realm)
	query := parsed.Query()
	query.Set("account", temporary.Username)
	query.Set("service", challenge.Service)
	query.Del("scope")
	for _, scope := range target.Scopes {
		query.Add("scope", scope)
	}
	parsed.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", fmt.Errorf("build Alibaba Cloud ACR internal Bearer-token request")
	}
	request.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(temporary.Username+":"+temporary.Password)))
	response, err := adapter.config.HTTP.Do(request)
	if err != nil {
		return "", fmt.Errorf("Alibaba Cloud ACR internal Bearer-token request failed")
	}
	if response == nil || response.Body == nil {
		return "", fmt.Errorf("Alibaba Cloud ACR internal Bearer-token request returned an empty response")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("Alibaba Cloud ACR internal Bearer-token request returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxAlibabaACRAuthBytes+1))
	if err != nil || len(data) > maxAlibabaACRAuthBytes {
		return "", fmt.Errorf("read bounded Alibaba Cloud ACR internal Bearer-token response")
	}
	var output struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if json.Unmarshal(data, &output) != nil {
		return "", fmt.Errorf("Alibaba Cloud ACR internal Bearer-token response is invalid")
	}
	token := strings.TrimSpace(output.Token)
	if token == "" {
		token = strings.TrimSpace(output.AccessToken)
	}
	if token == "" || len(token) > maxAlibabaACRBearerBytes || strings.ContainsAny(token, "\r\n") {
		return "", fmt.Errorf("Alibaba Cloud ACR internal Bearer-token response omitted a valid token")
	}
	return token, nil
}

func (adapter *AlibabaRESTAdapter) followAlibabaACRBlobRedirects(ctx context.Context, invocation Invocation, target alibabaACRTarget, previous *http.Request, response *http.Response) (*http.Response, error) {
	for redirects := 0; response != nil && response.StatusCode == http.StatusTemporaryRedirect; redirects++ {
		if redirects >= maxAlibabaACRBlobRedirects || !isAlibabaACRBlobRead(invocation) {
			if response.Body != nil {
				response.Body.Close()
			}
			return nil, fmt.Errorf("Alibaba Cloud ACR returned an unsafe blob redirect")
		}
		location := response.Header.Get("Location")
		if response.Body != nil {
			response.Body.Close()
		}
		redirectURL, err := url.Parse(location)
		if err != nil || !validAlibabaACRBlobRedirectTarget(redirectURL, target.Region) {
			return nil, fmt.Errorf("Alibaba Cloud ACR returned an unsafe blob redirect")
		}
		redirect, err := http.NewRequestWithContext(ctx, strings.ToUpper(strings.TrimSpace(invocation.Method)), redirectURL.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("build Alibaba Cloud ACR blob redirect")
		}
		for _, name := range []string{"Accept", "Range", "If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since"} {
			for _, value := range previous.Header.Values(name) {
				redirect.Header.Add(name, value)
			}
		}
		response, err = adapter.config.HTTP.Do(redirect)
		if err != nil {
			return nil, fmt.Errorf("Alibaba Cloud ACR blob redirect failed")
		}
		previous = redirect
	}
	if response != nil && response.StatusCode >= 300 && response.StatusCode < 400 {
		if response.Body != nil {
			response.Body.Close()
		}
		return nil, fmt.Errorf("Alibaba Cloud ACR returned an unsafe redirect")
	}
	return response, nil
}

func isAlibabaACRBlobRead(invocation Invocation) bool {
	method := strings.ToUpper(strings.TrimSpace(invocation.Method))
	if method != http.MethodGet && method != http.MethodHead {
		return false
	}
	parsed, err := url.Parse(invocation.URL)
	if err != nil {
		return false
	}
	path := parsed.EscapedPath()
	return strings.Contains(path, "/blobs/") && !strings.Contains(path, "/blobs/uploads/")
}

func validAlibabaACRBlobRedirectTarget(target *url.URL, region string) bool {
	if target == nil || target.Scheme != "https" || target.User != nil || target.Fragment != "" || (target.Port() != "" && target.Port() != "443") {
		return false
	}
	host := strings.ToLower(target.Hostname())
	suffixes := []string{".oss-" + region + ".aliyuncs.com", ".oss-" + region + "-internal.aliyuncs.com"}
	for _, suffix := range suffixes {
		if strings.HasSuffix(host, suffix) {
			bucket := strings.TrimSuffix(host, suffix)
			return alibabaACROSSBucketPattern.MatchString(bucket)
		}
	}
	return false
}

func sanitizeAlibabaACRError(err error, secrets ...string) error {
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

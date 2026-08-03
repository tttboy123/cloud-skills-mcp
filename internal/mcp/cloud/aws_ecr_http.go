package cloud

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	aws "github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

const (
	authSchemeAWSECR   = "ecr"
	maxAWSECRAuthBytes = 1024 * 1024
	maxAWSECRRedirects = 3
)

var (
	awsECRPrivateRegistryPattern = regexp.MustCompile(`^([0-9]{12})\.dkr\.ecr(-fips)?\.([a-z0-9-]+)\.(amazonaws\.com(?:\.cn)?)$`)
	awsECRPrivateDualPattern     = regexp.MustCompile(`^([0-9]{12})\.dkr-ecr(-fips)?\.([a-z0-9-]+)\.on\.aws$`)
)

type awsECRTarget struct {
	RegistryHost  string
	RegistryID    string
	Region        string
	Public        bool
	TokenEndpoint string
	TokenTarget   string
	TokenService  string
}

func validateAWSECRInvocation(invocation Invocation) error {
	_, err := parseAWSECRInvocation(invocation)
	return err
}

func parseAWSECRInvocation(invocation Invocation) (awsECRTarget, error) {
	if !identifierPattern.MatchString(invocation.Operation) {
		return awsECRTarget{}, fmt.Errorf("Amazon ECR requires a valid operation")
	}
	method := strings.ToUpper(strings.TrimSpace(invocation.Method))
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return awsECRTarget{}, fmt.Errorf("Amazon ECR Registry requires GET, HEAD, POST, PUT, PATCH, or DELETE")
	}
	parsed, err := url.Parse(invocation.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return awsECRTarget{}, fmt.Errorf("Amazon ECR Registry requires a valid HTTPS URL")
	}
	if parsed.Port() != "" && parsed.Port() != "443" {
		return awsECRTarget{}, fmt.Errorf("Amazon ECR Registry URL port must be 443")
	}
	for name := range parsed.Query() {
		if isCredentialQueryParameter(name) {
			return awsECRTarget{}, fmt.Errorf("caller-supplied credential query parameter %q is forbidden", name)
		}
	}
	for name := range invocation.Headers {
		if isProtectedHeader(name) {
			return awsECRTarget{}, fmt.Errorf("caller-supplied protected header %q is forbidden", name)
		}
	}
	host := strings.ToLower(parsed.Hostname())
	region := strings.ToLower(strings.TrimSpace(invocation.Region))
	service := strings.ToLower(strings.TrimSpace(invocation.Service))
	target, ok := parseAWSPrivateECRHost(host)
	if ok {
		if service != "ecr" {
			return awsECRTarget{}, fmt.Errorf("private Amazon ECR Registry requires service=ecr")
		}
		if region != target.Region {
			return awsECRTarget{}, fmt.Errorf("Amazon ECR region must exactly match the registry endpoint")
		}
	} else {
		target, ok = parseAWSPublicECRHost(host)
		if !ok {
			return awsECRTarget{}, fmt.Errorf("Amazon ECR requires an exact private or public Registry endpoint")
		}
		if service != "ecr-public" || region != "us-east-1" {
			return awsECRTarget{}, fmt.Errorf("Amazon ECR Public requires service=ecr-public and region=us-east-1")
		}
	}
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	if strings.Contains(path, "%") {
		return awsECRTarget{}, fmt.Errorf("Amazon ECR Registry paths must use their canonical unescaped form")
	}
	if err := validateAWSECRRegistryPath(path, method, target.Public); err != nil {
		return awsECRTarget{}, err
	}
	return target, nil
}

func parseAWSPrivateECRHost(host string) (awsECRTarget, bool) {
	if matches := awsECRPrivateRegistryPattern.FindStringSubmatch(host); len(matches) == 5 {
		fips := matches[2] != ""
		endpointPrefix := "api.ecr"
		if fips {
			endpointPrefix = "api.ecr-fips"
		}
		return awsECRTarget{
			RegistryHost: host, RegistryID: matches[1], Region: matches[3],
			TokenEndpoint: "https://" + endpointPrefix + "." + matches[3] + "." + matches[4] + "/",
			TokenTarget:   "AmazonEC2ContainerRegistry_V20150921.GetAuthorizationToken", TokenService: "ecr",
		}, true
	}
	if matches := awsECRPrivateDualPattern.FindStringSubmatch(host); len(matches) == 4 {
		endpointPrefix := "ecr"
		if matches[2] != "" {
			endpointPrefix = "ecr-fips"
		}
		return awsECRTarget{
			RegistryHost: host, RegistryID: matches[1], Region: matches[3],
			TokenEndpoint: "https://" + endpointPrefix + "." + matches[3] + ".api.aws/",
			TokenTarget:   "AmazonEC2ContainerRegistry_V20150921.GetAuthorizationToken", TokenService: "ecr",
		}, true
	}
	return awsECRTarget{}, false
}

func parseAWSPublicECRHost(host string) (awsECRTarget, bool) {
	switch host {
	case "public.ecr.aws":
		return awsECRTarget{
			RegistryHost: host, Region: "us-east-1", Public: true,
			TokenEndpoint: "https://api.ecr-public.us-east-1.amazonaws.com/",
			TokenTarget:   "SpencerFrontendService.GetAuthorizationToken", TokenService: "ecr-public",
		}, true
	case "ecr-public.aws.com":
		return awsECRTarget{
			RegistryHost: host, Region: "us-east-1", Public: true,
			TokenEndpoint: "https://ecr-public.us-east-1.api.aws/",
			TokenTarget:   "SpencerFrontendService.GetAuthorizationToken", TokenService: "ecr-public",
		}, true
	default:
		return awsECRTarget{}, false
	}
}

func validateAWSECRRegistryPath(path, method string, public bool) error {
	if path == "/v2/" {
		if method != http.MethodGet && method != http.MethodHead {
			return fmt.Errorf("Amazon ECR Registry version checks require GET or HEAD")
		}
		return nil
	}
	if path == "/v2/_catalog" {
		if public {
			return fmt.Errorf("Amazon ECR Public does not expose a Registry catalog")
		}
		if method != http.MethodGet {
			return fmt.Errorf("Amazon ECR Registry catalog requires GET")
		}
		return nil
	}
	if !strings.HasPrefix(path, "/v2/") {
		return fmt.Errorf("Amazon ECR supports only Docker or OCI Registry /v2 paths")
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
		return fmt.Errorf("Amazon ECR Registry path does not identify a repository operation")
	}
	repository := remainder[:end]
	if len(repository) > 256 || !azureACRRepositoryPattern.MatchString(repository) {
		return fmt.Errorf("Amazon ECR Registry path contains an invalid repository name")
	}
	tail := remainder[end+len(marker):]
	switch marker {
	case "/manifests/":
		if tail == "" || strings.Contains(tail, "/") || (method != http.MethodGet && method != http.MethodHead && method != http.MethodPut && method != http.MethodDelete) {
			return fmt.Errorf("Amazon ECR manifest paths require a reference and GET, HEAD, PUT, or DELETE")
		}
	case "/blobs/":
		if tail == "uploads/" {
			if method != http.MethodPost {
				return fmt.Errorf("Amazon ECR blob upload creation requires POST")
			}
		} else if strings.HasPrefix(tail, "uploads/") {
			uploadID := strings.TrimPrefix(tail, "uploads/")
			if uploadID == "" || strings.Contains(uploadID, "/") || (method != http.MethodGet && method != http.MethodPatch && method != http.MethodPut && method != http.MethodDelete) {
				return fmt.Errorf("Amazon ECR blob upload sessions require GET, PATCH, PUT, or DELETE")
			}
		} else if tail == "" || strings.Contains(tail, "/") || (method != http.MethodGet && method != http.MethodHead && method != http.MethodDelete) {
			return fmt.Errorf("Amazon ECR blob paths require a digest and GET, HEAD, or DELETE")
		}
	case "/tags/":
		if public {
			return fmt.Errorf("Amazon ECR Public does not support the Docker Registry tags API")
		}
		if tail != "list" || method != http.MethodGet {
			return fmt.Errorf("Amazon ECR Registry tags paths require GET on /tags/list")
		}
	case "/referrers/":
		if tail == "" || strings.Contains(tail, "/") || method != http.MethodGet {
			return fmt.Errorf("Amazon ECR referrers paths require a digest and GET")
		}
	}
	if public && strings.Count(repository, "/") < 1 {
		return fmt.Errorf("Amazon ECR Public paths require registry alias and repository name")
	}
	return nil
}

func invokeAWSECR(ctx context.Context, adapter *AWSRESTAdapter, invocation Invocation) (InvocationResult, error) {
	target, err := parseAWSECRInvocation(invocation)
	if err != nil {
		return InvocationResult{}, err
	}
	body, contentLength, cleanup, err := prepareRESTBody(invocation)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("prepare Amazon ECR Registry request body: %w", err)
	}
	defer cleanup()
	dataRequest, err := http.NewRequestWithContext(ctx, strings.ToUpper(strings.TrimSpace(invocation.Method)), invocation.URL, body)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("build Amazon ECR Registry request")
	}
	if err := addQueryParameters(dataRequest.URL, invocation.Parameters); err != nil {
		return InvocationResult{}, fmt.Errorf("build Amazon ECR Registry query: %w", err)
	}
	for name, value := range invocation.Headers {
		dataRequest.Header.Set(name, value)
	}
	if contentLength >= 0 {
		dataRequest.ContentLength = contentLength
	}
	if (invocation.Body != nil || invocation.BodyFile != "") && dataRequest.Header.Get("Content-Type") == "" {
		dataRequest.Header.Set("Content-Type", invocationContentType(invocation))
	}
	credentials, err := adapter.config.Credentials.Credentials(ctx)
	if err != nil {
		return InvocationResult{}, err
	}
	if credentials.AccessKeyID == "" || credentials.SecretAccessKey == "" {
		return InvocationResult{}, fmt.Errorf("AWS credential provider returned incomplete AKSK material")
	}
	registryToken, registryPassword, err := adapter.getAWSECRAuthorization(ctx, target, credentials)
	if err != nil {
		return InvocationResult{}, err
	}
	if target.Public {
		dataRequest.Header.Set("Authorization", "Bearer "+registryToken)
	} else {
		dataRequest.Header.Set("Authorization", "Basic "+registryToken)
	}
	response, err := adapter.config.HTTP.Do(dataRequest)
	if err != nil {
		return InvocationResult{}, sanitizeAWSECRError(fmt.Errorf("Amazon ECR Registry data request: %w", err), credentials.AccessKeyID, credentials.SecretAccessKey, credentials.SessionToken, registryToken, registryPassword)
	}
	response, err = adapter.followAWSECRRedirects(ctx, invocation, target, dataRequest, response)
	if err != nil {
		return InvocationResult{}, sanitizeAWSECRError(err, credentials.AccessKeyID, credentials.SecretAccessKey, credentials.SessionToken, registryToken, registryPassword)
	}
	output, err := readRESTResponseWithFile(response, adapter.config.MaxBodyBytes, invocation.ResponseFile, invocation.MaxResponseFileBytes)
	if err != nil {
		return InvocationResult{}, sanitizeAWSECRError(err, credentials.AccessKeyID, credentials.SecretAccessKey, credentials.SessionToken, registryToken, registryPassword)
	}
	return InvocationResult{Output: output, RequestID: responseRequestID(response.Header)}, nil
}

func (adapter *AWSRESTAdapter) followAWSECRRedirects(ctx context.Context, invocation Invocation, target awsECRTarget, previous *http.Request, response *http.Response) (*http.Response, error) {
	if response != nil && response.StatusCode == http.StatusTemporaryRedirect && (target.Public || !isAWSECRLayerRead(invocation)) {
		if response.Body != nil {
			response.Body.Close()
		}
		return nil, fmt.Errorf("Amazon ECR returned an unsafe layer redirect")
	}
	for redirects := 0; response != nil && response.StatusCode == http.StatusTemporaryRedirect; redirects++ {
		if redirects >= maxAWSECRRedirects {
			if response.Body != nil {
				response.Body.Close()
			}
			return nil, fmt.Errorf("Amazon ECR exceeded %d layer redirects", maxAWSECRRedirects)
		}
		method := strings.ToUpper(strings.TrimSpace(invocation.Method))
		if method != http.MethodGet && method != http.MethodHead {
			if response.Body != nil {
				response.Body.Close()
			}
			return nil, fmt.Errorf("Amazon ECR layer redirects are supported only for GET or HEAD")
		}
		location := response.Header.Get("Location")
		if response.Body != nil {
			response.Body.Close()
		}
		redirectURL, err := url.Parse(location)
		if err != nil || !validAWSECRLayerRedirectTarget(redirectURL, target.Region) {
			return nil, fmt.Errorf("Amazon ECR returned an unsafe layer redirect")
		}
		redirect, err := http.NewRequestWithContext(ctx, method, redirectURL.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("build Amazon ECR layer redirect")
		}
		for _, name := range []string{"Accept", "Range", "If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since"} {
			for _, value := range previous.Header.Values(name) {
				redirect.Header.Add(name, value)
			}
		}
		response, err = adapter.config.HTTP.Do(redirect)
		if err != nil {
			return nil, fmt.Errorf("Amazon ECR redirected layer request failed")
		}
		previous = redirect
	}
	if response == nil {
		return nil, fmt.Errorf("Amazon ECR returned an empty Registry response")
	}
	return response, nil
}

func isAWSECRLayerRead(invocation Invocation) bool {
	method := strings.ToUpper(strings.TrimSpace(invocation.Method))
	if method != http.MethodGet && method != http.MethodHead {
		return false
	}
	parsed, err := url.Parse(invocation.URL)
	if err != nil {
		return false
	}
	remainder := strings.TrimPrefix(parsed.EscapedPath(), "/v2/")
	end := -1
	marker := ""
	for _, candidate := range []string{"/manifests/", "/blobs/", "/tags/", "/referrers/"} {
		if index := strings.LastIndex(remainder, candidate); index > end {
			end = index
			marker = candidate
		}
	}
	if marker != "/blobs/" {
		return false
	}
	tail := remainder[end+len(marker):]
	return tail != "" && !strings.HasPrefix(tail, "uploads/")
}

func validAWSECRLayerRedirectTarget(target *url.URL, region string) bool {
	if target == nil || target.Scheme != "https" || target.Hostname() == "" || target.User != nil || target.Fragment != "" {
		return false
	}
	if target.Port() != "" && target.Port() != "443" {
		return false
	}
	bucket := "prod-" + region + "-starport-layer-bucket"
	host := strings.ToLower(target.Hostname())
	for _, suffix := range []string{
		".s3." + region + ".amazonaws.com",
		".s3.dualstack." + region + ".amazonaws.com",
		".s3." + region + ".amazonaws.com.cn",
		".s3.dualstack." + region + ".amazonaws.com.cn",
	} {
		if host == bucket+suffix {
			return true
		}
	}
	return false
}

func (adapter *AWSRESTAdapter) getAWSECRAuthorization(ctx context.Context, target awsECRTarget, credentials AWSCredentials) (string, string, error) {
	payload := []byte(`{}`)
	if !target.Public {
		payload, _ = json.Marshal(map[string][]string{"registryIds": {target.RegistryID}})
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.TokenEndpoint, bytes.NewReader(payload))
	if err != nil {
		return "", "", fmt.Errorf("build Amazon ECR internal authorization request")
	}
	request.Header.Set("Content-Type", "application/x-amz-json-1.1")
	request.Header.Set("X-Amz-Target", target.TokenTarget)
	digest := sha256.Sum256(payload)
	awsCredentials := aws.Credentials{
		AccessKeyID: credentials.AccessKeyID, SecretAccessKey: credentials.SecretAccessKey, SessionToken: credentials.SessionToken,
	}
	if err := awsv4.NewSigner().SignHTTP(ctx, awsCredentials, request, hex.EncodeToString(digest[:]), target.TokenService, target.Region, adapter.config.Now().UTC()); err != nil {
		return "", "", fmt.Errorf("sign Amazon ECR internal authorization request")
	}
	response, err := adapter.config.HTTP.Do(request)
	if err != nil {
		return "", "", fmt.Errorf("Amazon ECR internal authorization request failed")
	}
	if response == nil || response.Body == nil {
		return "", "", fmt.Errorf("Amazon ECR internal authorization returned an empty response")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", "", fmt.Errorf("Amazon ECR internal authorization returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxAWSECRAuthBytes+1))
	if err != nil {
		return "", "", fmt.Errorf("read Amazon ECR internal authorization response")
	}
	if len(data) > maxAWSECRAuthBytes {
		return "", "", fmt.Errorf("Amazon ECR internal authorization response exceeds %d bytes", maxAWSECRAuthBytes)
	}
	token, err := parseAWSECRAuthorizationResponse(data, target)
	if err != nil {
		return "", "", err
	}
	decoded, err := base64.StdEncoding.DecodeString(token)
	if err != nil || !strings.HasPrefix(string(decoded), "AWS:") || len(decoded) <= len("AWS:") {
		return "", "", fmt.Errorf("Amazon ECR internal authorization returned an invalid Registry token")
	}
	return token, strings.TrimPrefix(string(decoded), "AWS:"), nil
}

func parseAWSECRAuthorizationResponse(data []byte, target awsECRTarget) (string, error) {
	if target.Public {
		var response struct {
			AuthorizationData struct {
				AuthorizationToken string `json:"authorizationToken"`
			} `json:"authorizationData"`
		}
		if json.Unmarshal(data, &response) != nil || strings.TrimSpace(response.AuthorizationData.AuthorizationToken) == "" {
			return "", fmt.Errorf("Amazon ECR Public internal authorization omitted its Registry token")
		}
		return strings.TrimSpace(response.AuthorizationData.AuthorizationToken), nil
	}
	var response struct {
		AuthorizationData []struct {
			AuthorizationToken string `json:"authorizationToken"`
			ProxyEndpoint      string `json:"proxyEndpoint"`
		} `json:"authorizationData"`
	}
	if json.Unmarshal(data, &response) != nil {
		return "", fmt.Errorf("Amazon ECR internal authorization returned invalid JSON")
	}
	for _, authorization := range response.AuthorizationData {
		parsed, err := url.Parse(authorization.ProxyEndpoint)
		if err != nil || parsed.Scheme != "https" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Port() != "" {
			continue
		}
		proxy, ok := parseAWSPrivateECRHost(strings.ToLower(parsed.Hostname()))
		if ok && proxy.RegistryID == target.RegistryID && proxy.Region == target.Region && sameAWSECRPartition(target.RegistryHost, proxy.RegistryHost) && strings.TrimSpace(authorization.AuthorizationToken) != "" {
			return strings.TrimSpace(authorization.AuthorizationToken), nil
		}
	}
	return "", fmt.Errorf("Amazon ECR internal authorization omitted the requested Registry token")
}

func sameAWSECRPartition(left, right string) bool {
	return strings.HasSuffix(left, ".amazonaws.com.cn") == strings.HasSuffix(right, ".amazonaws.com.cn")
}

func sanitizeAWSECRError(err error, secrets ...string) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	for _, secret := range secrets {
		if secret = strings.TrimSpace(secret); secret != "" {
			message = strings.ReplaceAll(message, secret, "***REDACTED***")
		}
	}
	return fmt.Errorf("%s", message)
}

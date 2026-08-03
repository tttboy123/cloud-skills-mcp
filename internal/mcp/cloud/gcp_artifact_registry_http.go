package cloud

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const authSchemeGCPArtifactRegistry = "artifact-registry"

var gcpArtifactRegistryHostPattern = regexp.MustCompile(`^[a-z](?:[a-z0-9-]{0,61}[a-z0-9])?-docker\.pkg\.dev$`)

type gcpArtifactRegistryTarget struct {
	Host   string
	PkgDev bool
}

func validateGCPArtifactRegistryInvocation(invocation Invocation) error {
	_, err := parseGCPArtifactRegistryInvocation(invocation)
	return err
}

func parseGCPArtifactRegistryInvocation(invocation Invocation) (gcpArtifactRegistryTarget, error) {
	if !strings.EqualFold(strings.TrimSpace(invocation.Service), "artifact-registry") {
		return gcpArtifactRegistryTarget{}, fmt.Errorf("Google Artifact Registry requires service=artifact-registry")
	}
	if !identifierPattern.MatchString(invocation.Operation) {
		return gcpArtifactRegistryTarget{}, fmt.Errorf("Google Artifact Registry requires a valid operation")
	}
	method := strings.ToUpper(strings.TrimSpace(invocation.Method))
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodDelete:
	default:
		return gcpArtifactRegistryTarget{}, fmt.Errorf("Google Artifact Registry requires GET, HEAD, POST, PUT, or DELETE; Docker chunked PATCH uploads are unsupported")
	}
	parsed, err := url.Parse(invocation.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return gcpArtifactRegistryTarget{}, fmt.Errorf("Google Artifact Registry requires a valid HTTPS Registry URL")
	}
	if parsed.Port() != "" && parsed.Port() != "443" {
		return gcpArtifactRegistryTarget{}, fmt.Errorf("Google Artifact Registry URL port must be 443")
	}
	for name := range parsed.Query() {
		if isCredentialQueryParameter(name) {
			return gcpArtifactRegistryTarget{}, fmt.Errorf("caller-supplied credential query parameter %q is forbidden", name)
		}
	}
	for name := range invocation.Headers {
		if isProtectedHeader(name) {
			return gcpArtifactRegistryTarget{}, fmt.Errorf("caller-supplied protected header %q is forbidden", name)
		}
	}
	target, ok := parseGCPArtifactRegistryHost(strings.ToLower(parsed.Hostname()))
	if !ok {
		return gcpArtifactRegistryTarget{}, fmt.Errorf("Google Artifact Registry requires an exact docker.pkg.dev or supported gcr.io endpoint")
	}
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	if strings.Contains(path, "%") {
		return gcpArtifactRegistryTarget{}, fmt.Errorf("Google Artifact Registry paths must use their canonical unescaped form")
	}
	if path == "/v2/token" || strings.HasPrefix(path, "/v2/token/") {
		return gcpArtifactRegistryTarget{}, fmt.Errorf("Google Artifact Registry token endpoints are internal and are not exposed through MCP")
	}
	if err := validateGCPArtifactRegistryPath(path, method, target, invocation); err != nil {
		return gcpArtifactRegistryTarget{}, err
	}
	return target, nil
}

func parseGCPArtifactRegistryHost(host string) (gcpArtifactRegistryTarget, bool) {
	if gcpArtifactRegistryHostPattern.MatchString(host) {
		return gcpArtifactRegistryTarget{Host: host, PkgDev: true}, true
	}
	switch host {
	case "gcr.io", "us.gcr.io", "eu.gcr.io", "asia.gcr.io":
		return gcpArtifactRegistryTarget{Host: host}, true
	default:
		return gcpArtifactRegistryTarget{}, false
	}
}

func validateGCPArtifactRegistryPath(path, method string, target gcpArtifactRegistryTarget, invocation Invocation) error {
	if path == "/v2/" {
		if method != http.MethodGet && method != http.MethodHead {
			return fmt.Errorf("Google Artifact Registry version checks require GET or HEAD")
		}
		return nil
	}
	if path == "/v2/_catalog" {
		return fmt.Errorf("Google Artifact Registry exposes scoped OCI repositories, not an unscoped Registry catalog")
	}
	if !strings.HasPrefix(path, "/v2/") {
		return fmt.Errorf("Google Artifact Registry supports only OCI Distribution /v2 paths")
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
		return fmt.Errorf("Google Artifact Registry path does not identify a scoped repository operation")
	}
	repository := remainder[:end]
	if len(repository) > 1024 || !azureACRRepositoryPattern.MatchString(repository) {
		return fmt.Errorf("Google Artifact Registry path contains an invalid repository name")
	}
	minimumSegments := 2
	if target.PkgDev {
		minimumSegments = 3
	}
	if len(strings.Split(repository, "/")) < minimumSegments {
		return fmt.Errorf("Google Artifact Registry path omits its project, repository, or image namespace")
	}
	tail := remainder[end+len(marker):]
	switch marker {
	case "/manifests/":
		if tail == "" || strings.Contains(tail, "/") || (method != http.MethodGet && method != http.MethodHead && method != http.MethodPut && method != http.MethodDelete) {
			return fmt.Errorf("Google Artifact Registry manifest paths require a reference and GET, HEAD, PUT, or DELETE")
		}
	case "/blobs/":
		return validateGCPArtifactRegistryBlobPath(tail, method, invocation)
	case "/tags/":
		if tail != "list" || method != http.MethodGet {
			return fmt.Errorf("Google Artifact Registry tags paths require GET on /tags/list")
		}
	case "/referrers/":
		if tail == "" || strings.Contains(tail, "/") || method != http.MethodGet {
			return fmt.Errorf("Google Artifact Registry referrers paths require a digest and GET")
		}
	}
	return nil
}

func validateGCPArtifactRegistryBlobPath(tail, method string, invocation Invocation) error {
	if tail == "uploads/" {
		if method != http.MethodPost {
			return fmt.Errorf("Google Artifact Registry blob upload creation requires POST")
		}
		digest, hasDigest, err := gcpArtifactRegistryParameter(invocation, "digest")
		if err != nil {
			return err
		}
		mount, hasMount, err := gcpArtifactRegistryParameter(invocation, "mount")
		if err != nil {
			return err
		}
		from, hasFrom, err := gcpArtifactRegistryParameter(invocation, "from")
		if err != nil {
			return err
		}
		if hasMount || hasFrom {
			if !hasMount || !hasFrom || hasDigest || invocation.Body != nil || invocation.BodyFile != "" || !validGCPArtifactDigest(mount) || !azureACRRepositoryPattern.MatchString(from) {
				return fmt.Errorf("Google Artifact Registry cross-repository blob mounts require matching mount digest and from repository without an upload body")
			}
			return nil
		}
		if !hasDigest || !validGCPArtifactDigest(digest) || (invocation.Body == nil && invocation.BodyFile == "") {
			return fmt.Errorf("Google Artifact Registry requires a digest and body for a monolithic blob upload")
		}
		return nil
	}
	if strings.HasPrefix(tail, "uploads/") {
		uploadID := strings.TrimPrefix(tail, "uploads/")
		if uploadID == "" || strings.Contains(uploadID, "/") {
			return fmt.Errorf("Google Artifact Registry blob upload session is invalid")
		}
		switch method {
		case http.MethodGet, http.MethodDelete:
			return nil
		case http.MethodPut:
			digest, ok, err := gcpArtifactRegistryParameter(invocation, "digest")
			if err != nil {
				return err
			}
			if !ok || !validGCPArtifactDigest(digest) || (invocation.Body == nil && invocation.BodyFile == "") {
				return fmt.Errorf("Google Artifact Registry upload completion requires a digest and monolithic body")
			}
			return nil
		default:
			return fmt.Errorf("Google Artifact Registry does not support Docker chunked upload sessions")
		}
	}
	if tail == "" || strings.Contains(tail, "/") || (method != http.MethodGet && method != http.MethodHead && method != http.MethodDelete) {
		return fmt.Errorf("Google Artifact Registry blob paths require a digest and GET, HEAD, or DELETE")
	}
	return nil
}

func gcpArtifactRegistryParameter(invocation Invocation, name string) (string, bool, error) {
	parsed, err := url.Parse(invocation.URL)
	if err != nil {
		return "", false, fmt.Errorf("Google Artifact Registry requires a valid URL")
	}
	values, inURL := parsed.Query()[name]
	if inURL && len(values) != 1 {
		return "", false, fmt.Errorf("Google Artifact Registry query parameter %s must occur exactly once", name)
	}
	value := ""
	if inURL {
		value = values[0]
	}
	if parameter, inParameters := invocation.Parameters[name]; inParameters {
		if inURL {
			return "", false, fmt.Errorf("Google Artifact Registry query parameter %s must not be duplicated", name)
		}
		text, ok := parameter.(string)
		if !ok {
			return "", false, fmt.Errorf("Google Artifact Registry query parameter %s must be a string", name)
		}
		value, inURL = text, true
	}
	if inURL && strings.TrimSpace(value) == "" {
		return "", false, fmt.Errorf("Google Artifact Registry query parameter %s must not be empty", name)
	}
	return strings.TrimSpace(value), inURL, nil
}

func validGCPArtifactDigest(value string) bool {
	algorithm, encoded, ok := strings.Cut(value, ":")
	if !ok || algorithm == "" || len(encoded) < 3 || len(encoded) > 256 {
		return false
	}
	for _, character := range algorithm {
		if !((character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '_' || character == '+' || character == '.' || character == '-') {
			return false
		}
	}
	for _, character := range encoded {
		if !((character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F') || (character >= '0' && character <= '9')) {
			return false
		}
	}
	return true
}

func invokeGCPArtifactRegistry(ctx context.Context, adapter *GCPRESTAdapter, invocation Invocation) (InvocationResult, error) {
	target, err := parseGCPArtifactRegistryInvocation(invocation)
	if err != nil {
		return InvocationResult{}, err
	}
	body, contentLength, cleanup, err := prepareRESTBody(invocation)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("prepare Google Artifact Registry request body: %w", err)
	}
	defer cleanup()
	requestBody := body
	if body != nil {
		requestBody = io.NopCloser(body)
	}
	request, err := http.NewRequestWithContext(ctx, strings.ToUpper(strings.TrimSpace(invocation.Method)), invocation.URL, requestBody)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("build Google Artifact Registry request")
	}
	if err := addQueryParameters(request.URL, invocation.Parameters); err != nil {
		return InvocationResult{}, fmt.Errorf("build Google Artifact Registry query: %w", err)
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
	token, err := adapter.config.Tokens.Token(ctx)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("load Google Cloud ADC token for Artifact Registry: %w", err)
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return InvocationResult{}, fmt.Errorf("Google Cloud ADC returned an empty Artifact Registry access token")
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if invocation.Project != "" {
		request.Header.Set("X-Goog-User-Project", invocation.Project)
	}
	response, err := adapter.config.HTTP.Do(request)
	if err != nil {
		return InvocationResult{}, sanitizeGCPArtifactRegistryError(fmt.Errorf("Google Artifact Registry request failed: %w", err), token)
	}
	response, err = adapter.completeGCPArtifactRegistryMonolithicUpload(ctx, invocation, target, request, body, contentLength, token, response)
	if err != nil {
		return InvocationResult{}, sanitizeGCPArtifactRegistryError(err, token)
	}
	if response != nil && response.StatusCode >= 300 && response.StatusCode < 400 {
		if response.Body != nil {
			response.Body.Close()
		}
		return InvocationResult{}, fmt.Errorf("Google Artifact Registry redirects are not followed")
	}
	output, err := readRESTResponseWithFile(response, adapter.config.MaxBodyBytes, invocation.ResponseFile, invocation.MaxResponseFileBytes)
	if err != nil {
		return InvocationResult{}, sanitizeGCPArtifactRegistryError(err, token)
	}
	return InvocationResult{Output: output, RequestID: responseRequestID(response.Header)}, nil
}

func (adapter *GCPRESTAdapter) completeGCPArtifactRegistryMonolithicUpload(ctx context.Context, invocation Invocation, target gcpArtifactRegistryTarget, initial *http.Request, body io.Reader, contentLength int64, token string, response *http.Response) (*http.Response, error) {
	if response == nil || response.StatusCode != http.StatusAccepted || !isGCPArtifactRegistryUploadStart(initial.URL) {
		return response, nil
	}
	if response.Body != nil {
		response.Body.Close()
	}
	digest, hasDigest, err := gcpArtifactRegistryParameter(invocation, "digest")
	if err != nil || !hasDigest || body == nil {
		return nil, fmt.Errorf("Google Artifact Registry did not complete the requested blob mount or upload")
	}
	location, err := resolveGCPArtifactRegistryUploadLocation(initial.URL, response.Header.Get("Location"), target)
	if err != nil {
		return nil, err
	}
	query := location.Query()
	query.Set("digest", digest)
	location.RawQuery = query.Encode()
	seeker, ok := body.(io.Seeker)
	if !ok {
		return nil, fmt.Errorf("Google Artifact Registry cannot replay the monolithic upload body")
	}
	if _, err := seeker.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewind Google Artifact Registry monolithic upload body")
	}
	completion, err := http.NewRequestWithContext(ctx, http.MethodPut, location.String(), io.NopCloser(body))
	if err != nil {
		return nil, fmt.Errorf("build Google Artifact Registry monolithic upload completion")
	}
	for name, values := range initial.Header {
		for _, value := range values {
			completion.Header.Add(name, value)
		}
	}
	completion.Header.Set("Authorization", "Bearer "+token)
	completion.ContentLength = contentLength
	response, err = adapter.config.HTTP.Do(completion)
	if err != nil {
		return nil, fmt.Errorf("Google Artifact Registry monolithic upload completion failed: %w", err)
	}
	if response != nil && response.StatusCode == http.StatusAccepted {
		if response.Body != nil {
			response.Body.Close()
		}
		return nil, fmt.Errorf("Google Artifact Registry did not complete the monolithic blob upload")
	}
	return response, nil
}

func isGCPArtifactRegistryUploadStart(target *url.URL) bool {
	return target != nil && strings.HasPrefix(target.EscapedPath(), "/v2/") && strings.HasSuffix(target.EscapedPath(), "/blobs/uploads/")
}

func resolveGCPArtifactRegistryUploadLocation(initial *url.URL, rawLocation string, target gcpArtifactRegistryTarget) (*url.URL, error) {
	if initial == nil || strings.TrimSpace(rawLocation) == "" {
		return nil, fmt.Errorf("Google Artifact Registry omitted its internal upload location")
	}
	reference, err := url.Parse(strings.TrimSpace(rawLocation))
	if err != nil {
		return nil, fmt.Errorf("Google Artifact Registry returned an unsafe internal upload location")
	}
	resolved := initial.ResolveReference(reference)
	if resolved.Scheme != "https" || strings.ToLower(resolved.Hostname()) != target.Host || resolved.User != nil || resolved.Fragment != "" || (resolved.Port() != "" && resolved.Port() != "443") {
		return nil, fmt.Errorf("Google Artifact Registry returned an unsafe internal upload location")
	}
	base := strings.TrimSuffix(initial.EscapedPath(), "/") + "/"
	path := resolved.EscapedPath()
	uploadID := strings.TrimPrefix(path, base)
	if path == base || uploadID == path || uploadID == "" || strings.Contains(uploadID, "/") || strings.Contains(path, "%") {
		return nil, fmt.Errorf("Google Artifact Registry returned an unsafe internal upload location")
	}
	return resolved, nil
}

func sanitizeGCPArtifactRegistryError(err error, secrets ...string) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	for _, secret := range secrets {
		if secret = strings.TrimSpace(secret); secret != "" {
			message = strings.ReplaceAll(message, "Bearer "+secret, "***REDACTED***")
			message = strings.ReplaceAll(message, secret, "***REDACTED***")
		}
	}
	return fmt.Errorf("%s", message)
}

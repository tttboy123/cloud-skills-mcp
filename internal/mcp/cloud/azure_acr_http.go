package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const (
	authSchemeAzureACR    = "acr"
	azureACREntraScope    = "https://containerregistry.azure.net/.default"
	maxAzureACRTokenBytes = 64 * 1024
	maxAzureACRRedirects  = 3
)

var (
	azureACRRegistryPattern   = regexp.MustCompile(`^[a-z0-9]{5,50}$`)
	azureACRRepositoryPattern = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*(?:/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)*$`)
)

type azureACRTarget struct {
	LoginHost    string
	RegistryName string
	Repository   string
	ScopeClass   azureACRScopeClass
}

type azureACRScopeClass uint8

const (
	azureACRScopeRegistryCatalog azureACRScopeClass = iota + 1
	azureACRScopeRegistryDeletedCatalog
	azureACRScopeRepositoryContent
	azureACRScopeRepositoryMetadata
	azureACRScopeRepositoryDeletedRead
	azureACRScopeRepositoryDeletedRestore
)

func validateAzureACRInvocation(invocation Invocation) error {
	_, err := parseAzureACRInvocation(invocation)
	return err
}

func parseAzureACRInvocation(invocation Invocation) (azureACRTarget, error) {
	if !strings.EqualFold(strings.TrimSpace(invocation.Service), "acr") {
		return azureACRTarget{}, fmt.Errorf("Azure Container Registry requires service=acr")
	}
	if !identifierPattern.MatchString(invocation.Operation) {
		return azureACRTarget{}, fmt.Errorf("Azure Container Registry requires a valid operation")
	}
	method := strings.ToUpper(strings.TrimSpace(invocation.Method))
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return azureACRTarget{}, fmt.Errorf("Azure Container Registry requires GET, HEAD, POST, PUT, PATCH, or DELETE")
	}
	parsed, err := url.Parse(invocation.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return azureACRTarget{}, fmt.Errorf("Azure Container Registry requires a valid HTTPS registry URL")
	}
	if parsed.Port() != "" && parsed.Port() != "443" {
		return azureACRTarget{}, fmt.Errorf("Azure Container Registry URL port must be 443")
	}
	for name := range parsed.Query() {
		if isCredentialQueryParameter(name) {
			return azureACRTarget{}, fmt.Errorf("caller-supplied credential query parameter %q is forbidden", name)
		}
	}
	host := strings.ToLower(parsed.Hostname())
	registry, ok := parseAzureACRLoginHost(host)
	if !ok {
		return azureACRTarget{}, fmt.Errorf("Azure Container Registry requires an exact public login endpoint")
	}
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	if strings.Contains(path, "%") {
		return azureACRTarget{}, fmt.Errorf("Azure Container Registry paths must use their canonical unescaped form")
	}
	if path == "/oauth2/exchange" || path == "/oauth2/token" || strings.HasPrefix(path, "/oauth2/") {
		return azureACRTarget{}, fmt.Errorf("Azure Container Registry token endpoints are internal and are not exposed through MCP")
	}
	target := azureACRTarget{LoginHost: host, RegistryName: registry}
	target.Repository, target.ScopeClass, err = azureACRResourceForPath(path)
	if err != nil {
		return azureACRTarget{}, err
	}
	if err := validateAzureACRScope(invocation.ACRScope, target, method); err != nil {
		return azureACRTarget{}, err
	}
	if err := validateAzureACRMountScopes(invocation, target, path); err != nil {
		return azureACRTarget{}, err
	}
	for name := range invocation.Headers {
		if isProtectedHeader(name) {
			return azureACRTarget{}, fmt.Errorf("caller-supplied protected header %q is forbidden", name)
		}
	}
	return target, nil
}

func parseAzureACRLoginHost(host string) (string, bool) {
	const suffix = ".azurecr.io"
	if !strings.HasSuffix(host, suffix) {
		return "", false
	}
	prefix := strings.TrimSuffix(host, suffix)
	labels := strings.Split(prefix, ".")
	if len(labels) == 1 && azureACRRegistryPattern.MatchString(labels[0]) {
		return labels[0], true
	}
	if len(labels) == 3 && labels[2] == "geo" && azureACRRegistryPattern.MatchString(labels[0]) && endpointLabelPattern.MatchString(labels[1]) {
		return labels[0], true
	}
	return "", false
}

func azureACRResourceForPath(path string) (string, azureACRScopeClass, error) {
	if path == "/v2/" || path == "/v2/_catalog" || path == "/acr/v1/_catalog" {
		return "", azureACRScopeRegistryCatalog, nil
	}
	if path == "/acr/v1/_deleted/_catalog" {
		return "", azureACRScopeRegistryDeletedCatalog, nil
	}
	if strings.HasPrefix(path, "/acr/v1/_deleted/") {
		return azureACRDeletedResourceForPath(strings.TrimPrefix(path, "/acr/v1/_deleted/"))
	}
	var remainder string
	var markers []string
	var scopeClass azureACRScopeClass
	switch {
	case strings.HasPrefix(path, "/v2/"):
		remainder = strings.TrimPrefix(path, "/v2/")
		markers = []string{"/manifests/", "/blobs/", "/tags/list", "/referrers/"}
		scopeClass = azureACRScopeRepositoryContent
	case strings.HasPrefix(path, "/acr/v1/"):
		remainder = strings.TrimPrefix(path, "/acr/v1/")
		markers = []string{"/_tags", "/_manifests"}
		scopeClass = azureACRScopeRepositoryMetadata
	default:
		return "", 0, fmt.Errorf("Azure Container Registry supports only official /v2 or /acr/v1 data-plane paths")
	}
	end := -1
	for _, marker := range markers {
		if index := strings.LastIndex(remainder, marker); index > end {
			end = index
		}
	}
	if end <= 0 {
		if strings.HasPrefix(path, "/acr/v1/") && azureACRRepositoryPattern.MatchString(remainder) && len(remainder) <= 255 {
			return remainder, azureACRScopeRepositoryMetadata, nil
		}
		return "", 0, fmt.Errorf("Azure Container Registry path does not identify a scoped repository operation")
	}
	repository := remainder[:end]
	if len(repository) > 255 || !azureACRRepositoryPattern.MatchString(repository) {
		return "", 0, fmt.Errorf("Azure Container Registry path contains an invalid repository name")
	}
	return repository, scopeClass, nil
}

func azureACRDeletedResourceForPath(remainder string) (string, azureACRScopeClass, error) {
	for _, marker := range []string{"/_manifests", "/_tags"} {
		index := strings.LastIndex(remainder, marker)
		if index <= 0 {
			continue
		}
		repository := remainder[:index]
		if len(repository) > 255 || !azureACRRepositoryPattern.MatchString(repository) {
			return "", 0, fmt.Errorf("Azure Container Registry deleted path contains an invalid repository name")
		}
		suffix := remainder[index+len(marker):]
		if suffix == "" {
			return repository, azureACRScopeRepositoryDeletedRead, nil
		}
		if marker == "/_manifests" && strings.HasPrefix(suffix, "/") && len(suffix) > 1 && !strings.Contains(strings.TrimPrefix(suffix, "/"), "/") {
			return repository, azureACRScopeRepositoryDeletedRestore, nil
		}
		return "", 0, fmt.Errorf("Azure Container Registry deleted path is not a supported list or manifest restore operation")
	}
	return "", 0, fmt.Errorf("Azure Container Registry deleted path does not identify a repository operation")
}

func validateAzureACRMountScopes(invocation Invocation, target azureACRTarget, path string) error {
	from, hasFrom, err := azureACRParameterString(invocation, "from")
	if err != nil {
		return err
	}
	_, hasMount, err := azureACRParameterString(invocation, "mount")
	if err != nil {
		return err
	}
	if !hasFrom && !hasMount {
		if invocation.ACRSourceScope != "" {
			return fmt.Errorf("acr_source_scope is supported only by a cross-repository blob mount")
		}
		return nil
	}
	if !hasFrom || !hasMount || strings.ToUpper(strings.TrimSpace(invocation.Method)) != http.MethodPost || !strings.HasPrefix(path, "/v2/") || !strings.HasSuffix(path, "/blobs/uploads/") {
		return fmt.Errorf("Azure Container Registry from and mount parameters are accepted only together on a blob-upload POST")
	}
	if from == target.Repository || len(from) > 255 || !azureACRRepositoryPattern.MatchString(from) {
		return fmt.Errorf("Azure Container Registry blob mount requires a distinct valid source repository")
	}
	want := "repository:" + from + ":pull"
	if strings.TrimSpace(invocation.ACRSourceScope) != want {
		return fmt.Errorf("acr_source_scope must exactly match the blob mount source repository with pull access")
	}
	return nil
}

func azureACRParameterString(invocation Invocation, name string) (string, bool, error) {
	parsed, err := url.Parse(invocation.URL)
	if err != nil {
		return "", false, fmt.Errorf("Azure Container Registry requires a valid URL")
	}
	values, inURL := parsed.Query()[name]
	if inURL && len(values) != 1 {
		return "", false, fmt.Errorf("Azure Container Registry query parameter %s must occur exactly once", name)
	}
	value := ""
	if inURL {
		value = values[0]
	}
	if parameter, inParameters := invocation.Parameters[name]; inParameters {
		if inURL {
			return "", false, fmt.Errorf("Azure Container Registry query parameter %s must not be duplicated", name)
		}
		text, ok := parameter.(string)
		if !ok {
			return "", false, fmt.Errorf("Azure Container Registry query parameter %s must be a string", name)
		}
		value, inURL = text, true
	}
	if inURL && strings.TrimSpace(value) == "" {
		return "", false, fmt.Errorf("Azure Container Registry query parameter %s must not be empty", name)
	}
	return strings.TrimSpace(value), inURL, nil
}

func validateAzureACRScope(scope string, target azureACRTarget, method string) error {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return fmt.Errorf("Azure Container Registry requires acr_scope")
	}
	if len(scope) > 512 {
		return fmt.Errorf("Azure Container Registry acr_scope exceeds 512 bytes")
	}
	read := method == http.MethodGet || method == http.MethodHead
	switch target.ScopeClass {
	case azureACRScopeRegistryCatalog:
		if !read || scope != "registry:catalog:*" {
			return fmt.Errorf("Azure Container Registry catalog reads require acr_scope=registry:catalog:*")
		}
		return nil
	case azureACRScopeRegistryDeletedCatalog:
		if !read || scope != "registry:deleted_catalog:*" {
			return fmt.Errorf("Azure Container Registry deleted catalog reads require acr_scope=registry:deleted_catalog:*")
		}
		return nil
	}
	const prefix = "repository:"
	if !strings.HasPrefix(scope, prefix) {
		return fmt.Errorf("Azure Container Registry repository operations require a repository acr_scope")
	}
	remainder := strings.TrimPrefix(scope, prefix)
	delimiter := strings.LastIndexByte(remainder, ':')
	if delimiter <= 0 {
		return fmt.Errorf("Azure Container Registry repository acr_scope is invalid")
	}
	repository, actions := remainder[:delimiter], remainder[delimiter+1:]
	if repository != target.Repository {
		return fmt.Errorf("Azure Container Registry acr_scope repository must exactly match the request path")
	}
	switch target.ScopeClass {
	case azureACRScopeRepositoryContent:
		switch method {
		case http.MethodGet, http.MethodHead:
			if actions != "pull" {
				return fmt.Errorf("Azure Container Registry content reads require pull scope")
			}
		case http.MethodPost, http.MethodPut, http.MethodPatch:
			if actions != "push" && actions != "pull,push" {
				return fmt.Errorf("Azure Container Registry content writes require push or pull,push scope")
			}
		case http.MethodDelete:
			if actions != "delete" {
				return fmt.Errorf("Azure Container Registry content deletes require delete scope")
			}
		}
	case azureACRScopeRepositoryMetadata:
		switch method {
		case http.MethodGet, http.MethodHead:
			if actions != "metadata_read" {
				return fmt.Errorf("Azure Container Registry metadata reads require metadata_read scope")
			}
		case http.MethodPatch:
			if actions != "metadata_write,metadata_read" {
				return fmt.Errorf("Azure Container Registry metadata updates require metadata_write,metadata_read scope")
			}
		case http.MethodDelete:
			if actions != "delete" {
				return fmt.Errorf("Azure Container Registry metadata deletes require delete scope")
			}
		default:
			return fmt.Errorf("Azure Container Registry metadata paths support only GET, HEAD, PATCH, or DELETE")
		}
	case azureACRScopeRepositoryDeletedRead:
		if !read || actions != "deleted_read" {
			return fmt.Errorf("Azure Container Registry deleted repository lists require deleted_read scope")
		}
	case azureACRScopeRepositoryDeletedRestore:
		if method != http.MethodPost || actions != "deleted_read,deleted_restore" {
			return fmt.Errorf("Azure Container Registry manifest restore requires POST and deleted_read,deleted_restore scope")
		}
	default:
		return fmt.Errorf("Azure Container Registry path has no supported authorization scope")
	}
	return nil
}

func invokeAzureACR(ctx context.Context, adapter *AzureRESTAdapter, invocation Invocation) (InvocationResult, error) {
	target, err := parseAzureACRInvocation(invocation)
	if err != nil {
		return InvocationResult{}, err
	}
	body, contentLength, cleanup, err := prepareRESTBody(invocation)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("prepare Azure Container Registry request body: %w", err)
	}
	defer cleanup()
	dataRequest, err := http.NewRequestWithContext(ctx, strings.ToUpper(strings.TrimSpace(invocation.Method)), invocation.URL, body)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("build Azure Container Registry request")
	}
	if err := addQueryParameters(dataRequest.URL, invocation.Parameters); err != nil {
		return InvocationResult{}, fmt.Errorf("build Azure Container Registry query: %w", err)
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
	entraToken, err := adapter.config.Tokens.Token(ctx, azureACREntraScope)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("load Azure identity token for Container Registry: %w", err)
	}
	if strings.TrimSpace(entraToken) == "" {
		return InvocationResult{}, fmt.Errorf("Azure identity returned an empty access token")
	}
	refreshToken, err := adapter.exchangeAzureACRToken(ctx, target.LoginHost, url.Values{
		"grant_type":   {"access_token"},
		"service":      {target.LoginHost},
		"access_token": {strings.TrimSpace(entraToken)},
	}, "refresh_token", "exchange")
	if err != nil {
		return InvocationResult{}, err
	}
	accessScopes := []string{strings.TrimSpace(invocation.ACRScope)}
	if invocation.ACRSourceScope != "" {
		accessScopes = append(accessScopes, strings.TrimSpace(invocation.ACRSourceScope))
	}
	accessToken, err := adapter.exchangeAzureACRToken(ctx, target.LoginHost, url.Values{
		"grant_type":    {"refresh_token"},
		"service":       {target.LoginHost},
		"scope":         accessScopes,
		"refresh_token": {refreshToken},
	}, "access_token", "token")
	if err != nil {
		return InvocationResult{}, err
	}
	dataRequest.Header.Set("Authorization", "Bearer "+accessToken)
	response, err := adapter.config.HTTP.Do(dataRequest)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("Azure Container Registry data request: %w", err)
	}
	response, err = adapter.followAzureACRRedirects(ctx, invocation, target, dataRequest, response)
	if err != nil {
		return InvocationResult{}, err
	}
	output, err := readRESTResponseWithFile(response, adapter.config.MaxBodyBytes, invocation.ResponseFile, invocation.MaxResponseFileBytes)
	if err != nil {
		return InvocationResult{}, sanitizeAzureACRError(err, entraToken, refreshToken, accessToken)
	}
	return InvocationResult{Output: output, RequestID: responseRequestID(response.Header)}, nil
}

func (adapter *AzureRESTAdapter) exchangeAzureACRToken(ctx context.Context, host string, form url.Values, field, endpoint string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+host+"/oauth2/"+endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build Azure Container Registry token request")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := adapter.config.HTTP.Do(request)
	if err != nil {
		return "", fmt.Errorf("Azure Container Registry token exchange request: %w", err)
	}
	if response == nil || response.Body == nil {
		return "", fmt.Errorf("Azure Container Registry token exchange returned an empty response")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("Azure Container Registry token exchange returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxAzureACRTokenBytes+1))
	if err != nil {
		return "", fmt.Errorf("read Azure Container Registry token exchange response")
	}
	if len(data) > maxAzureACRTokenBytes {
		return "", fmt.Errorf("Azure Container Registry token exchange response exceeds %d bytes", maxAzureACRTokenBytes)
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(data, &payload) != nil {
		return "", fmt.Errorf("Azure Container Registry token exchange returned invalid JSON")
	}
	var token string
	if raw, ok := payload[field]; !ok || json.Unmarshal(raw, &token) != nil || strings.TrimSpace(token) == "" {
		return "", fmt.Errorf("Azure Container Registry token exchange omitted the required internal token")
	}
	return strings.TrimSpace(token), nil
}

func (adapter *AzureRESTAdapter) followAzureACRRedirects(ctx context.Context, invocation Invocation, target azureACRTarget, previous *http.Request, response *http.Response) (*http.Response, error) {
	for redirects := 0; response != nil && isAzureACRRedirect(response.StatusCode); redirects++ {
		if redirects >= maxAzureACRRedirects {
			if response.Body != nil {
				response.Body.Close()
			}
			return nil, fmt.Errorf("Azure Container Registry exceeded %d data redirects", maxAzureACRRedirects)
		}
		method := strings.ToUpper(strings.TrimSpace(invocation.Method))
		if method != http.MethodGet && method != http.MethodHead {
			if response.Body != nil {
				response.Body.Close()
			}
			return nil, fmt.Errorf("Azure Container Registry redirects are supported only for GET or HEAD")
		}
		location := response.Header.Get("Location")
		if response.Body != nil {
			response.Body.Close()
		}
		redirectURL, err := url.Parse(location)
		if err != nil || !validAzureACRRedirectTarget(redirectURL, target.RegistryName) {
			return nil, fmt.Errorf("Azure Container Registry returned an unsafe data redirect")
		}
		redirect, err := http.NewRequestWithContext(ctx, method, redirectURL.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("build Azure Container Registry data redirect")
		}
		for name, values := range previous.Header {
			if strings.EqualFold(name, "Authorization") {
				continue
			}
			for _, value := range values {
				redirect.Header.Add(name, value)
			}
		}
		response, err = adapter.config.HTTP.Do(redirect)
		if err != nil {
			return nil, fmt.Errorf("Azure Container Registry redirected data request failed")
		}
		previous = redirect
	}
	if response == nil {
		return nil, fmt.Errorf("Azure Container Registry returned an empty data response")
	}
	return response, nil
}

func sanitizeAzureACRError(err error, secrets ...string) error {
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

func isAzureACRRedirect(status int) bool {
	return status == http.StatusTemporaryRedirect
}

func validAzureACRRedirectTarget(target *url.URL, registry string) bool {
	if target == nil || target.Scheme != "https" || target.Hostname() == "" || target.User != nil || target.Fragment != "" {
		return false
	}
	if target.Port() != "" && target.Port() != "443" {
		return false
	}
	host := strings.ToLower(target.Hostname())
	labels := strings.Split(host, ".")
	if len(labels) == 5 && labels[0] == registry && labels[2] == "data" && labels[3] == "azurecr" && labels[4] == "io" && endpointLabelPattern.MatchString(labels[1]) {
		return true
	}
	return strings.HasSuffix(host, ".blob.core.windows.net") && host != ".blob.core.windows.net" && len(strings.TrimSuffix(host, ".blob.core.windows.net")) > 0 && !strings.Contains(strings.TrimSuffix(host, ".blob.core.windows.net"), ".")
}

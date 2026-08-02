package cloud

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

const (
	maxRequestPayloadBytes = 1024 * 1024
	maxRequestFileBytes    = 64 * 1024 * 1024
)

var sensitiveTerms = []string{
	"secret", "password", "credential", "accesskey", "access-key", "privatekey", "private-key",
	"sessiontoken", "session-token", "gettoken", "get-token", "print-access-token",
}

var forbiddenFlags = map[string]struct{}{
	"--endpoint": {}, "--endpoint-url": {}, "--https-proxy": {}, "--proxy": {},
	"--no-verify-ssl": {}, "--ca-bundle": {}, "--secretid": {}, "--secretkey": {},
	"--secret-id": {}, "--secret-key": {}, "--access-key-id": {}, "--access-key-secret": {},
	"--aws-access-key-id": {}, "--aws-secret-access-key": {}, "--token": {},
	"--security-token": {}, "--authorization": {}, "--password": {}, "--client-secret": {},
	"--url": {}, "--method": {}, "--body": {}, "--headers": {}, "--subscription": {},
	"--resource": {}, "--resource-type": {}, "--skip-authorization-header": {}, "--cli-input-json": {},
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
	if !isProvider(request.Provider) {
		return fmt.Errorf("unsupported provider %q", request.Provider)
	}
	switch request.Provider {
	case ProviderAWS, ProviderAlicloud, ProviderTencent:
		if !identifierPattern.MatchString(request.Service) {
			return fmt.Errorf("invalid service %q", request.Service)
		}
		if !identifierPattern.MatchString(request.Operation) {
			return fmt.Errorf("invalid operation %q", request.Operation)
		}
	case ProviderAzure, ProviderGCP, ProviderBaidu:
		if err := validateRESTTarget(request.Provider, request.Method, request.URL); err != nil {
			return err
		}
	}
	if len(request.Arguments) > 128 {
		return fmt.Errorf("too many CLI arguments: %d", len(request.Arguments))
	}
	for _, argument := range request.Arguments {
		if err := validateArgument(argument, allowedFileRoots); err != nil {
			return err
		}
	}
	if request.Provider == ProviderAlicloud {
		if err := validateEmbeddedFileReferences(request.Parameters, allowedFileRoots); err != nil {
			return err
		}
	}
	if request.Body != nil && request.BodyFile != "" {
		return fmt.Errorf("body and body_file are mutually exclusive")
	}
	if request.BodyFile != "" {
		if request.Provider != ProviderAzure && request.Provider != ProviderGCP && request.Provider != ProviderBaidu {
			return fmt.Errorf("body_file is supported only by REST providers")
		}
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
	if len(request.Headers) > 64 {
		return fmt.Errorf("too many HTTP headers: %d", len(request.Headers))
	}
	for name := range request.Headers {
		lower := strings.ToLower(strings.TrimSpace(name))
		if lower == "authorization" || lower == "proxy-authorization" || lower == "x-bce-security-token" ||
			lower == "x-api-key" || lower == "api-key" || lower == "cookie" || lower == "set-cookie" {
			return fmt.Errorf("caller-supplied credential header %q is forbidden", name)
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

func validateEmbeddedFileReferences(value any, allowedFileRoots []string) error {
	switch typed := value.(type) {
	case map[string]any:
		for _, child := range typed {
			if err := validateEmbeddedFileReferences(child, allowedFileRoots); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := validateEmbeddedFileReferences(child, allowedFileRoots); err != nil {
				return err
			}
		}
	case string:
		for _, prefix := range []string{"file://", "fileb://", "@"} {
			if strings.HasPrefix(typed, prefix) && !pathAllowed(strings.TrimPrefix(typed, prefix), allowedFileRoots) {
				return fmt.Errorf("embedded local file reference is outside CLOUD_SKILLS_ALLOWED_FILE_ROOTS")
			}
		}
	}
	return nil
}

func validateArgument(argument string, allowedFileRoots []string) error {
	if len(argument) == 0 || len(argument) > 8192 {
		return fmt.Errorf("CLI argument length must be between 1 and 8192 bytes")
	}
	for _, char := range argument {
		if char == 0 || unicode.IsControl(char) {
			return fmt.Errorf("CLI argument contains a control character")
		}
	}
	flag := strings.ToLower(argument)
	if index := strings.IndexByte(flag, '='); index >= 0 {
		flag = flag[:index]
	}
	if _, forbidden := forbiddenFlags[flag]; forbidden {
		return fmt.Errorf("CLI flag %q is forbidden", flag)
	}
	for _, prefix := range []string{"file://", "fileb://", "@"} {
		if strings.HasPrefix(argument, prefix) {
			path := strings.TrimPrefix(argument, prefix)
			if !pathAllowed(path, allowedFileRoots) {
				return fmt.Errorf("local file reference is outside CLOUD_SKILLS_ALLOWED_FILE_ROOTS")
			}
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

func validateRESTTarget(provider Provider, method, rawURL string) error {
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
		switch strings.ToLower(name) {
		case "access_token", "oauth_token", "authorization", "sig", "signature",
			"x-amz-credential", "x-amz-signature", "x-amz-security-token",
			"x-goog-signature", "x-bce-security-token", "sharedaccesssignature":
			return fmt.Errorf("caller-supplied credential query parameter %q is forbidden", name)
		}
	}
	allowed := false
	switch provider {
	case ProviderAzure:
		allowed = host == "management.azure.com" || host == "graph.microsoft.com" || host == "api.loganalytics.io" ||
			hasAnySuffix(host, ".azure.com", ".azure.net", ".windows.net", ".azurecr.io", ".loganalytics.io", ".azureedge.net", ".trafficmanager.net")
	case ProviderGCP:
		allowed = host == "googleapis.com" || strings.HasSuffix(host, ".googleapis.com")
	case ProviderBaidu:
		allowed = host == "baidubce.com" || strings.HasSuffix(host, ".baidubce.com")
	}
	if !allowed {
		return fmt.Errorf("URL host %q is outside the provider endpoint allowlist", host)
	}
	return nil
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

package cloud

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
var apiVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
var azureApplicationIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}(?:/\.default)?$`)
var endpointLabelPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var awsRegionSetEntryPattern = regexp.MustCompile(`^(?:\*|[a-z0-9][a-z0-9*-]{0,62})$`)

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
		if provider == ProviderTencent && normalizedAuthScheme(request.AuthScheme, authSchemeTencentTC3) == authSchemeTencentVoiceWS && strings.HasPrefix(action, "convert") {
			return true
		}
		if provider == ProviderTencent && normalizedAuthScheme(request.AuthScheme, authSchemeTencentTC3) == authSchemeTencentSOEWS && strings.HasPrefix(action, "evaluate") {
			return true
		}
		for _, prefix := range []string{
			"describe", "list", "get", "head", "search", "query", "check", "show", "read", "inspect", "lookup", "count", "enumerate", "recognize", "transcribe", "translate", "synthesize",
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
	tencentScheme := normalizedAuthScheme(request.AuthScheme, authSchemeTencentTC3)
	webSocketScheme := request.Provider == ProviderTencent && (tencentScheme == authSchemeTencentASRWS || tencentScheme == authSchemeTencentVirtualWS || tencentScheme == authSchemeTencentSOEWS || tencentScheme == authSchemeTencentTranslateWS || tencentScheme == authSchemeTencentVoiceWS || tencentScheme == authSchemeTencentMPSWS || tencentScheme == authSchemeTencentMPSTTSWS)
	if webSocketScheme {
		switch tencentScheme {
		case authSchemeTencentASRWS:
			if err := validateTencentASRWebSocketInvocation(request); err != nil {
				return err
			}
		case authSchemeTencentVirtualWS:
			if err := validateTencentVirtualNumberWebSocketInvocation(request); err != nil {
				return err
			}
		case authSchemeTencentSOEWS:
			if err := validateTencentSOEWebSocketInvocation(request); err != nil {
				return err
			}
		case authSchemeTencentTranslateWS:
			if err := validateTencentSpeechTranslateWebSocketInvocation(request); err != nil {
				return err
			}
		case authSchemeTencentVoiceWS:
			if err := validateTencentVoiceConversionWebSocketInvocation(request); err != nil {
				return err
			}
		case authSchemeTencentMPSWS:
			if err := validateTencentMPSWebSocketInvocation(request); err != nil {
				return err
			}
		case authSchemeTencentMPSTTSWS:
			if err := validateTencentMPSTTSWebSocketInvocation(request); err != nil {
				return err
			}
		}
	} else {
		if err := validateRESTTargetWithEndpointHosts(request.Provider, request.Method, request.URL, allowedEndpointHosts); err != nil {
			return err
		}
	}
	switch request.Provider {
	case ProviderAWS:
		if !identifierPattern.MatchString(request.Service) {
			return fmt.Errorf("invalid service %q", request.Service)
		}
		if !identifierPattern.MatchString(request.Operation) {
			return fmt.Errorf("invalid operation %q", request.Operation)
		}
		scheme := normalizedAuthScheme(request.AuthScheme, authSchemeAWSSigV4)
		if scheme != authSchemeAWSSigV4 && scheme != authSchemeAWSSigV4a {
			return fmt.Errorf("AWS auth_scheme must be sigv4 or sigv4a")
		}
		if scheme == authSchemeAWSSigV4 && !identifierPattern.MatchString(request.Region) {
			return fmt.Errorf("AWS SigV4 requires a valid region")
		}
		if scheme == authSchemeAWSSigV4a {
			if _, err := parseAWSRegionSet(request.RegionSet); err != nil {
				return err
			}
		} else if request.RegionSet != "" {
			return fmt.Errorf("region_set is supported only by AWS SigV4a")
		}
	case ProviderAlicloud:
		if !identifierPattern.MatchString(request.Service) || !identifierPattern.MatchString(request.Operation) {
			return fmt.Errorf("Alibaba Cloud requires valid service and operation")
		}
		scheme := normalizedAuthScheme(request.AuthScheme, authSchemeAlibabaACS3)
		if scheme != authSchemeAlibabaACS3 && scheme != authSchemeAlibabaRPCV2 && scheme != authSchemeAlibabaROAV2 && scheme != authSchemeAlibabaDataHub && scheme != authSchemeAlibabaOpenSearch && scheme != authSchemeAlibabaODPS && scheme != authSchemeAlibabaODPSV4 && scheme != authSchemeAlibabaFC && scheme != authSchemeAlibabaFC3 && scheme != authSchemeAlibabaFCCustom && scheme != authSchemeAlibabaOSS && scheme != authSchemeAlibabaOSSV4 && scheme != authSchemeAlibabaSLS && scheme != authSchemeAlibabaSLSV4 && scheme != authSchemeAlibabaMNS && scheme != authSchemeAlibabaOTS && scheme != authSchemeAlibabaOTSV4 {
			return fmt.Errorf("Alibaba Cloud auth_scheme must be acs3, rpc, roa, datahub, opensearch, odps, odps4, fc, fc3, fc-custom, oss, oss4, sls, sls4, mns, ots, or ots4")
		}
		if (scheme == authSchemeAlibabaACS3 || scheme == authSchemeAlibabaRPCV2 || scheme == authSchemeAlibabaROAV2) && !apiVersionPattern.MatchString(request.APIVersion) {
			return fmt.Errorf("Alibaba Cloud %s requires a valid api_version", strings.ToUpper(scheme))
		}
		if scheme == authSchemeAlibabaRPCV2 {
			if !strings.EqualFold(request.Method, http.MethodGet) && !strings.EqualFold(request.Method, http.MethodPost) {
				return fmt.Errorf("Alibaba Cloud RPC V2 requires method GET or POST")
			}
			parsed, err := url.Parse(request.URL)
			if err != nil || (parsed.EscapedPath() != "" && parsed.EscapedPath() != "/") {
				return fmt.Errorf("Alibaba Cloud RPC V2 requires the root request path")
			}
			for name := range parsed.Query() {
				if isAlibabaRPCV2ControlledParameter(name) {
					return fmt.Errorf("caller-supplied Alibaba Cloud RPC V2 signing parameter %q is forbidden", name)
				}
			}
			for name := range request.Parameters {
				if isAlibabaRPCV2ControlledParameter(name) {
					return fmt.Errorf("caller-supplied Alibaba Cloud RPC V2 signing parameter %q is forbidden", name)
				}
			}
		}
		if scheme == authSchemeAlibabaROAV2 {
			switch strings.ToUpper(strings.TrimSpace(request.Method)) {
			case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete:
			default:
				return fmt.Errorf("Alibaba Cloud ROA V2 requires method GET, POST, PUT, or DELETE")
			}
			for name := range request.Headers {
				if isAlibabaROAV2ControlledHeader(name) {
					return fmt.Errorf("caller-supplied protected Alibaba Cloud ROA V2 header %q is forbidden", name)
				}
			}
		}
		if scheme == authSchemeAlibabaDataHub {
			switch strings.ToUpper(strings.TrimSpace(request.Method)) {
			case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete:
			default:
				return fmt.Errorf("Alibaba Cloud DataHub requires method GET, POST, PUT, or DELETE")
			}
			for name := range request.Headers {
				if isAlibabaDataHubControlledHeader(name) {
					return fmt.Errorf("caller-supplied protected Alibaba Cloud DataHub header %q is forbidden", name)
				}
			}
		}
		if scheme == authSchemeAlibabaOpenSearch {
			switch strings.ToUpper(strings.TrimSpace(request.Method)) {
			case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodHead, http.MethodDelete:
			default:
				return fmt.Errorf("Alibaba Cloud OpenSearch requires method GET, POST, PUT, HEAD, or DELETE")
			}
			for name := range request.Headers {
				if isAlibabaOpenSearchControlledHeader(name) {
					return fmt.Errorf("caller-supplied protected Alibaba Cloud OpenSearch header %q is forbidden", name)
				}
			}
		}
		if scheme == authSchemeAlibabaODPS || scheme == authSchemeAlibabaODPSV4 {
			for name := range request.Headers {
				if isAlibabaODPSControlledHeader(name) {
					return fmt.Errorf("caller-supplied protected Alibaba Cloud ODPS header %q is forbidden", name)
				}
			}
		}
		if scheme == authSchemeAlibabaODPSV4 && !identifierPattern.MatchString(request.Region) {
			return fmt.Errorf("Alibaba Cloud ODPS4 requires a valid region")
		}
		if scheme == authSchemeAlibabaFC {
			for name := range request.Headers {
				if isAlibabaFCControlledHeader(name) {
					return fmt.Errorf("caller-supplied protected Alibaba Cloud Function Compute header %q is forbidden", name)
				}
			}
		}
		if scheme == authSchemeAlibabaFC3 || scheme == authSchemeAlibabaFCCustom {
			for name := range request.Headers {
				if isAlibabaFCTriggerControlledHeader(name) {
					return fmt.Errorf("caller-supplied protected Alibaba Cloud Function Compute trigger header %q is forbidden", name)
				}
			}
		}
		if scheme == authSchemeAlibabaFC3 {
			parsed, _ := url.Parse(request.URL)
			host := strings.ToLower(parsed.Hostname())
			if host != "fcapp.run" && !strings.HasSuffix(host, ".fcapp.run") {
				return fmt.Errorf("Alibaba Cloud FC3 requires an official fcapp.run HTTP trigger endpoint")
			}
		}
		if scheme == authSchemeAlibabaOSS {
			for name := range request.Headers {
				if isAlibabaOSSV1ControlledHeader(name) {
					return fmt.Errorf("caller-supplied protected Alibaba Cloud OSS V1 header %q is forbidden", name)
				}
			}
			if err := validateAlibabaOSSV1Target(request.URL); err != nil {
				return err
			}
		}
		if scheme == authSchemeAlibabaOSSV4 && !identifierPattern.MatchString(request.Region) {
			return fmt.Errorf("Alibaba Cloud OSS4 requires a valid region")
		}
		if scheme == authSchemeAlibabaSLSV4 && !identifierPattern.MatchString(request.Region) {
			return fmt.Errorf("Alibaba Cloud SLS4 requires a valid region")
		}
		if scheme == authSchemeAlibabaOTSV4 && !identifierPattern.MatchString(request.Region) {
			return fmt.Errorf("Alibaba Cloud OTS4 requires a valid region")
		}
		if (scheme == authSchemeAlibabaOTS || scheme == authSchemeAlibabaOTSV4) && !strings.EqualFold(request.Method, http.MethodPost) {
			return fmt.Errorf("Alibaba Cloud OTS requires method POST")
		}
		if scheme != authSchemeAlibabaACS3 && scheme != authSchemeAlibabaRPCV2 && scheme != authSchemeAlibabaROAV2 && request.APIVersion != "" && !apiVersionPattern.MatchString(request.APIVersion) {
			return fmt.Errorf("Alibaba Cloud requires a valid api_version when provided")
		}
	case ProviderTencent:
		if !identifierPattern.MatchString(request.Service) || !identifierPattern.MatchString(request.Operation) {
			return fmt.Errorf("Tencent Cloud requires valid service and operation")
		}
		scheme := normalizedAuthScheme(request.AuthScheme, authSchemeTencentTC3)
		if scheme != authSchemeTencentTC3 && scheme != authSchemeTencentV1 && scheme != authSchemeTencentV1SHA256 && scheme != authSchemeTencentQCloud && scheme != authSchemeTencentQCloud256 && scheme != authSchemeTencentASRWS && scheme != authSchemeTencentVirtualWS && scheme != authSchemeTencentSOEWS && scheme != authSchemeTencentTranslateWS && scheme != authSchemeTencentVoiceWS && scheme != authSchemeTencentMPSWS && scheme != authSchemeTencentMPSTTSWS && scheme != authSchemeTencentCOS {
			return fmt.Errorf("Tencent Cloud auth_scheme must be tc3, tc1, tc1-sha256, qcloud, qcloud-sha256, asr-ws, virtual-number-ws, soe-ws, speech-translate-ws, voice-convert-ws, mps-ws, mps-tts-ws, or cos")
		}
		if (scheme == authSchemeTencentTC3 || scheme == authSchemeTencentV1 || scheme == authSchemeTencentV1SHA256) && !identifierPattern.MatchString(request.APIVersion) {
			return fmt.Errorf("Tencent Cloud API signing requires a valid api_version")
		}
		if scheme == authSchemeTencentV1 || scheme == authSchemeTencentV1SHA256 || scheme == authSchemeTencentQCloud || scheme == authSchemeTencentQCloud256 {
			if !strings.EqualFold(request.Method, http.MethodGet) && !strings.EqualFold(request.Method, http.MethodPost) {
				return fmt.Errorf("Tencent Cloud query signing requires method GET or POST")
			}
			parsed, err := url.Parse(request.URL)
			if err != nil {
				return fmt.Errorf("Tencent Cloud query signing requires a valid URL")
			}
			path := parsed.EscapedPath()
			if path == "" {
				path = "/"
			}
			if scheme == authSchemeTencentQCloud || scheme == authSchemeTencentQCloud256 {
				host := strings.ToLower(parsed.Hostname())
				if path != "/v2/index.php" || !strings.HasSuffix(host, ".api.qcloud.com") || host == ".api.qcloud.com" {
					return fmt.Errorf("Tencent Cloud legacy API requires a product .api.qcloud.com endpoint with path /v2/index.php")
				}
			} else if path != "/" {
				return fmt.Errorf("Tencent Cloud API v1 requires the root request path")
			}
			for name := range parsed.Query() {
				if isTencentV1ControlledParameter(name) {
					return fmt.Errorf("caller-supplied Tencent Cloud API v1 signing parameter %q is forbidden", name)
				}
			}
			for name := range request.Parameters {
				if isTencentV1ControlledParameter(name) {
					return fmt.Errorf("caller-supplied Tencent Cloud API v1 signing parameter %q is forbidden", name)
				}
			}
		}
		if scheme == authSchemeTencentASRWS {
			for name := range request.Parameters {
				if isTencentASRControlledParameter(name) {
					return fmt.Errorf("caller-supplied Tencent Cloud ASR WebSocket signing parameter %q is forbidden", name)
				}
			}
		}
		if scheme == authSchemeTencentTranslateWS {
			for name := range request.Parameters {
				if isTencentASRControlledParameter(name) {
					return fmt.Errorf("caller-supplied Tencent Cloud speech translation WebSocket signing parameter %q is forbidden", name)
				}
			}
		}
		if scheme == authSchemeTencentMPSWS {
			for name := range request.Parameters {
				if isTencentMPSControlledParameter(name) {
					return fmt.Errorf("caller-supplied Tencent Cloud MPS WebSocket signing parameter %q is forbidden", name)
				}
			}
		}
		if scheme == authSchemeTencentMPSTTSWS {
			for name := range request.Parameters {
				if isTencentMPSControlledParameter(name) {
					return fmt.Errorf("caller-supplied Tencent Cloud MPS TTS WebSocket signing parameter %q is forbidden", name)
				}
			}
		}
	}
	if request.Provider != ProviderAWS && request.RegionSet != "" {
		return fmt.Errorf("region_set is supported only by AWS SigV4a")
	}
	if request.StreamChunkBytes < 0 || request.StreamChunkBytes > maxRequestFileBytes {
		return fmt.Errorf("stream_chunk_bytes must be between 1 and %d when provided", maxRequestFileBytes)
	}
	if request.StreamIntervalMS < 0 || request.StreamIntervalMS > 5000 {
		return fmt.Errorf("stream_interval_ms must be between 1 and 5000 when provided")
	}
	streamControlScheme := request.Provider == ProviderTencent && (tencentScheme == authSchemeTencentASRWS || tencentScheme == authSchemeTencentVirtualWS || tencentScheme == authSchemeTencentSOEWS || tencentScheme == authSchemeTencentTranslateWS || tencentScheme == authSchemeTencentVoiceWS || tencentScheme == authSchemeTencentMPSWS)
	if (request.StreamChunkBytes != 0 || request.StreamIntervalMS != 0) && !streamControlScheme {
		return fmt.Errorf("stream transport controls require a supported WebSocket auth_scheme")
	}
	if (request.StreamUserID != "" || request.StreamFormat != 0) && !(request.Provider == ProviderTencent && tencentScheme == authSchemeTencentMPSWS) {
		return fmt.Errorf("stream_user_id and stream_format are supported only by Tencent MPS WebSocket")
	}
	payloadMode := strings.ToLower(strings.TrimSpace(request.PayloadMode))
	if payloadMode != "" {
		if request.Provider != ProviderAWS || (payloadMode != awsPayloadModeChunked && payloadMode != awsPayloadModeChunkedTrailer && payloadMode != awsPayloadModeEventStream) {
			return fmt.Errorf("unsupported payload_mode %q", request.PayloadMode)
		}
		payloadScheme := normalizedAuthScheme(request.AuthScheme, authSchemeAWSSigV4)
		if payloadMode == awsPayloadModeEventStream && payloadScheme != authSchemeAWSSigV4 {
			return fmt.Errorf("aws-eventstream requires AWS SigV4")
		}
		if payloadMode != awsPayloadModeEventStream && payloadScheme != authSchemeAWSSigV4 && payloadScheme != authSchemeAWSSigV4a {
			return fmt.Errorf("%s requires AWS SigV4 or SigV4a", payloadMode)
		}
		if request.Body == nil && request.BodyFile == "" {
			return fmt.Errorf("%s requires a request body", payloadMode)
		}
		if (payloadMode == awsPayloadModeChunked || payloadMode == awsPayloadModeChunkedTrailer) && (!strings.EqualFold(request.Service, "s3") || !strings.EqualFold(request.Method, http.MethodPut)) {
			return fmt.Errorf("%s requires service s3 and method PUT", payloadMode)
		}
		if payloadMode == awsPayloadModeEventStream && !strings.EqualFold(request.Method, http.MethodPost) {
			return fmt.Errorf("aws-eventstream requires method POST")
		}
	}
	checksumAlgorithm := strings.ToLower(strings.TrimSpace(request.ChecksumAlgorithm))
	if payloadMode == awsPayloadModeChunkedTrailer {
		if !isSupportedAWSTrailerChecksum(checksumAlgorithm) {
			return fmt.Errorf("aws-chunked-trailer requires checksum_algorithm crc32, crc32c, crc64nvme, sha1, or sha256")
		}
	} else if checksumAlgorithm != "" {
		return fmt.Errorf("checksum_algorithm is supported only by aws-chunked-trailer")
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
	case "authorization", "proxy-authorization", "host", "content-length", "transfer-encoding",
		"x-api-key", "api-key", "cookie", "set-cookie", "x-http-method-override", "x-method-override",
		"x-amz-date", "x-amz-security-token", "x-amz-region-set", "x-amz-content-sha256", "x-amz-decoded-content-length", "x-amz-trailer", "x-amz-trailer-signature",
		"x-amz-checksum-crc32", "x-amz-checksum-crc32c", "x-amz-checksum-crc64nvme", "x-amz-checksum-sha1", "x-amz-checksum-sha256",
		"x-acs-action", "x-acs-version", "x-acs-date", "x-acs-signature-nonce", "x-acs-content-sha256", "x-acs-security-token",
		"x-oss-date", "x-oss-content-sha256", "x-oss-security-token",
		"x-log-apiversion", "x-log-signaturemethod", "x-log-date", "x-log-content-sha256",
		"x-mns-version", "x-mns-date", "security-token",
		"x-ots-date", "x-ots-apiversion", "x-ots-accesskeyid", "x-ots-contentmd5", "x-ots-instancename",
		"x-ots-ststoken", "x-ots-signature", "x-ots-signaturev4", "x-ots-signregion", "x-ots-signdate",
		"x-tc-action", "x-tc-version", "x-tc-timestamp", "x-tc-region", "x-tc-token",
		"x-cos-security-token", "x-bce-date", "x-bce-security-token", "x-goog-user-project":
		return true
	default:
		return false
	}
}

func parseAWSRegionSet(value string) ([]string, error) {
	parts := strings.Split(value, ",")
	if strings.TrimSpace(value) == "" || len(parts) > 16 {
		return nil, fmt.Errorf("AWS SigV4a requires 1 to 16 region_set entries")
	}
	regions := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, part := range parts {
		region := strings.ToLower(strings.TrimSpace(part))
		if !awsRegionSetEntryPattern.MatchString(region) || strings.Contains(region, "**") {
			return nil, fmt.Errorf("invalid AWS SigV4a region_set entry %q", part)
		}
		if !seen[region] {
			seen[region] = true
			regions = append(regions, region)
		}
	}
	return regions, nil
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
			host == "atlas.microsoft.com" || host == "azurehealthcareapis.com" ||
			hasAnySuffix(host, ".azure.com", ".azure.net", ".windows.net", ".azurecr.io", ".loganalytics.io", ".azureedge.net", ".trafficmanager.net",
				".azconfig.io", ".azuredatabricks.net", ".azure-api.net", ".azuresynapse.net", ".azureml.ms", ".service.signalr.net",
				".atlas.microsoft.com", ".azurehealthcareapis.com",
				".chinacloudapi.cn", ".azure.cn", ".windowsazure.cn", ".usgovcloudapi.net", ".microsoftazure.us", ".azure.us", ".microsoftazure.de")
	case ProviderGCP:
		allowed = host == "googleapis.com" || strings.HasSuffix(host, ".googleapis.com")
	case ProviderAlicloud:
		allowed = host == "aliyuncs.com" || strings.HasSuffix(host, ".aliyuncs.com") ||
			host == "aliyuncs.com.cn" || strings.HasSuffix(host, ".aliyuncs.com.cn") ||
			host == "alibabacloud.com" || strings.HasSuffix(host, ".alibabacloud.com") ||
			host == "aliyunpds.com" || strings.HasSuffix(host, ".aliyunpds.com") ||
			host == "fcapp.run" || strings.HasSuffix(host, ".fcapp.run") ||
			host == "maxcompute.aliyun.com" || strings.HasSuffix(host, ".maxcompute.aliyun.com") ||
			host == "maxcompute.aliyun-inc.com" || strings.HasSuffix(host, ".maxcompute.aliyun-inc.com")
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
	case "access_token", "oauth_token", "authorization", "sig", "signature", "accesskeyid", "securitytoken",
		"x-amz-credential", "x-amz-signature", "x-amz-security-token",
		"x-goog-signature", "x-bce-security-token", "sharedaccesssignature",
		"q-signature", "q-ak", "x-cos-security-token", "x-acs-security-token", "x-oss-security-token", "security-token", "x-ots-ststoken",
		"authorization-sts-token", "x-odps-bearer-token", "x-fc-access-key-id", "x-fc-access-key-secret", "x-fc-security-token", "x-fc-signature", "x-fc-expires":
		return true
	default:
		return false
	}
}

func isAlibabaRPCV2ControlledParameter(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "accesskeyid", "action", "signature", "signaturemethod", "signaturenonce", "signatureversion", "securitytoken", "timestamp", "version":
		return true
	default:
		return false
	}
}

func isTencentV1ControlledParameter(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "action", "nonce", "secretid", "signature", "signaturemethod", "timestamp", "token", "version":
		return true
	default:
		return false
	}
}

func isAlibabaROAV2ControlledHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "authorization", "content-md5", "date", "x-acs-security-token", "x-acs-signature-method", "x-acs-signature-nonce", "x-acs-signature-version", "x-acs-version":
		return true
	default:
		return false
	}
}

func isAlibabaDataHubControlledHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "authorization", "date", "x-datahub-client-version", "x-datahub-security-token":
		return true
	default:
		return false
	}
}

func isAlibabaOpenSearchControlledHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "authorization", "content-md5", "date", "x-opensearch-nonce", "x-opensearch-security-token":
		return true
	default:
		return false
	}
}

func isAlibabaODPSControlledHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "authorization", "authorization-sts-token", "application-authentication", "date", "x-odps-bearer-token", "x-pyodps-token-timestamp":
		return true
	default:
		return false
	}
}

func isAlibabaFCControlledHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "authorization", "content-md5", "date", "x-fc-access-key-id", "x-fc-access-key-secret", "x-fc-security-token", "x-fc-signature", "x-fc-expires":
		return true
	default:
		return false
	}
}

func isAlibabaFCTriggerControlledHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "authorization", "date", "x-acs-date", "x-acs-security-token":
		return true
	default:
		return false
	}
}

func isAlibabaOSSV1ControlledHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "authorization", "date", "x-oss-date", "x-oss-security-token":
		return true
	default:
		return false
	}
}

func validateAlibabaOSSV1Target(rawURL string) error {
	target, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid Alibaba Cloud OSS V1 URL")
	}
	host := strings.ToLower(target.Hostname())
	isOSSEndpoint := false
	for _, label := range strings.Split(host, ".") {
		if label == "oss" || strings.HasPrefix(label, "oss-") {
			isOSSEndpoint = true
			break
		}
	}
	if !isOSSEndpoint || !(strings.HasSuffix(host, ".aliyuncs.com") || strings.HasSuffix(host, ".aliyuncs.com.cn") || strings.HasSuffix(host, ".alibabacloud.com")) {
		return fmt.Errorf("Alibaba Cloud OSS V1 requires an official OSS endpoint whose bucket is encoded in the host or path")
	}
	for name, entries := range target.Query() {
		if isAlibabaOSSV1Subresource(name) && len(entries) > 1 {
			return fmt.Errorf("Alibaba Cloud OSS V1 does not allow repeated signed query parameter %q", name)
		}
	}
	return nil
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
	allowed := host == "graph.microsoft.com" || host == "atlas.microsoft.com" || host == "azurehealthcareapis.com" ||
		hasAnySuffix(host, ".azure.com", ".azure.net", ".windows.net", ".microsoft.com", ".loganalytics.io", ".azconfig.io", ".azureml.ms", ".azuredatabricks.net",
			".azurehealthcareapis.com", ".healthcareapis.azure.com",
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

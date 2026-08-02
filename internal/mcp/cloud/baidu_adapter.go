package cloud

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	bceAuthVersionV1 = "bce-auth-v1"
	bceAuthVersionV2 = "bce-auth-v2"
	bceExpiry        = 1800
)

type BCECredentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

type BCECredentialProvider interface {
	Credentials(context.Context) (BCECredentials, error)
}

type EnvBCECredentialProvider struct{}

func (EnvBCECredentialProvider) Credentials(context.Context) (BCECredentials, error) {
	credentials := BCECredentials{
		AccessKeyID:     strings.TrimSpace(os.Getenv("BCE_ACCESS_KEY_ID")),
		SecretAccessKey: strings.TrimSpace(os.Getenv("BCE_SECRET_ACCESS_KEY")),
		SessionToken:    strings.TrimSpace(os.Getenv("BCE_SESSION_TOKEN")),
	}
	if credentials.SessionToken == "" {
		credentials.SessionToken = strings.TrimSpace(os.Getenv("BCE_SECURITY_TOKEN"))
	}
	if credentials.AccessKeyID == "" || credentials.SecretAccessKey == "" {
		return BCECredentials{}, fmt.Errorf("Baidu BCE credential unavailable: set BCE_ACCESS_KEY_ID and BCE_SECRET_ACCESS_KEY, plus BCE_SESSION_TOKEN for IAM/STS")
	}
	return credentials, nil
}

type BaiduRESTConfig struct {
	Credentials  BCECredentialProvider
	HTTP         HTTPDoer
	MaxBodyBytes int64
	Timeout      time.Duration
	Now          func() time.Time
	AllowedHosts []string
}

type BaiduRESTAdapter struct {
	config BaiduRESTConfig
}

func NewBaiduRESTAdapter(config BaiduRESTConfig) *BaiduRESTAdapter {
	if config.Credentials == nil {
		config.Credentials = EnvBCECredentialProvider{}
	}
	if config.Timeout <= 0 {
		config.Timeout = 60 * time.Second
	}
	if config.HTTP == nil {
		config.HTTP = &http.Client{
			Timeout:       config.Timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	if config.MaxBodyBytes <= 0 {
		config.MaxBodyBytes = defaultRESTBodyLimit
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &BaiduRESTAdapter{config: config}
}

func (adapter *BaiduRESTAdapter) Status(ctx context.Context) (ProviderStatus, error) {
	status := ProviderStatus{Provider: ProviderBaidu, Adapter: "BCE signed HTTPS", CredentialSource: credentialSource(ProviderBaidu)}
	if _, err := adapter.config.Credentials.Credentials(ctx); err != nil {
		status.CredentialStatus = CredentialStatusMissingLocalMaterial
		status.Message = err.Error()
		return status, nil
	}
	status.Available = true
	status.Version = bceAuthVersionV1 + "+" + bceAuthVersionV2
	status.CredentialStatus = CredentialStatusLocalMaterialPresent
	status.Message = "local BCE credential material is present; validity remains unverified until a provider API call succeeds"
	return status, nil
}

func (adapter *BaiduRESTAdapter) Discover(_ context.Context, request DiscoveryRequest) ([]byte, error) {
	service := strings.ToUpper(strings.TrimSpace(request.Service))
	result := map[string]string{
		"api_center":     "https://cloud.baidu.com/doc/API/index.html",
		"authentication": "https://cloud.baidu.com/doc/Reference/s/njwvz1yfu",
	}
	if service != "" {
		result["service"] = strings.ToLower(service)
		result["documentation"] = "https://cloud.baidu.com/doc/" + url.PathEscape(service) + "/index.html"
	}
	return json.Marshal(result)
}

func (adapter *BaiduRESTAdapter) Invoke(ctx context.Context, invocation Invocation) (InvocationResult, error) {
	if err := validateRESTTargetWithEndpointHosts(ProviderBaidu, invocation.Method, invocation.URL, adapter.config.AllowedHosts); err != nil {
		return InvocationResult{}, err
	}
	body, contentLength, cleanup, err := prepareRESTBody(invocation)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("prepare Baidu BCE request body: %w", err)
	}
	defer cleanup()
	request, err := http.NewRequestWithContext(ctx, strings.ToUpper(invocation.Method), invocation.URL, body)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("build Baidu BCE request: %w", err)
	}
	if err := addQueryParameters(request.URL, invocation.Parameters); err != nil {
		return InvocationResult{}, fmt.Errorf("build Baidu BCE query: %w", err)
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
	credentials, err := adapter.config.Credentials.Credentials(ctx)
	if err != nil {
		return InvocationResult{}, err
	}
	if credentials.SessionToken != "" {
		request.Header.Set("x-bce-security-token", credentials.SessionToken)
	}
	timestamp := adapter.config.Now().UTC()
	var authorization string
	switch invocation.AuthVersion {
	case "", "v1":
		authorization, err = signBCERequest(request, credentials, timestamp, bceExpiry)
	case "v2":
		request.Header.Set("x-bce-date", timestamp.Format("2006-01-02T15:04:05Z"))
		authorization, err = signBCEV2Request(request, credentials, timestamp, invocation.Region, invocation.Service)
	default:
		err = fmt.Errorf("unsupported Baidu BCE auth version %q", invocation.AuthVersion)
	}
	if err != nil {
		return InvocationResult{}, err
	}
	request.Header.Set("Authorization", authorization)
	response, err := adapter.config.HTTP.Do(request)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("Baidu BCE API request: %w", err)
	}
	output, err := readRESTResponseWithFile(response, adapter.config.MaxBodyBytes, invocation.ResponseFile, invocation.MaxResponseFileBytes)
	if err != nil {
		return InvocationResult{}, err
	}
	return InvocationResult{Output: output, RequestID: responseRequestID(response.Header)}, nil
}

func signBCERequest(request *http.Request, credentials BCECredentials, timestamp time.Time, expiry int) (string, error) {
	if request == nil || request.URL == nil {
		return "", fmt.Errorf("Baidu BCE request is required")
	}
	if credentials.AccessKeyID == "" || credentials.SecretAccessKey == "" {
		return "", fmt.Errorf("Baidu BCE AK/SK is required")
	}
	if expiry <= 0 {
		return "", fmt.Errorf("Baidu BCE signature expiry must be positive")
	}
	signDate := timestamp.UTC().Format("2006-01-02T15:04:05Z")
	prefix := fmt.Sprintf("%s/%s/%s/%d", bceAuthVersionV1, credentials.AccessKeyID, signDate, expiry)
	signingKey := hmacSHA256Hex(credentials.SecretAccessKey, prefix)
	canonicalHeaders, signedHeaders := canonicalBCEHeaders(request)
	canonicalRequest := strings.Join([]string{
		strings.ToUpper(request.Method),
		canonicalBCEPath(request.URL.Path),
		canonicalBCEQuery(request.URL.Query()),
		canonicalHeaders,
	}, "\n")
	signature := hmacSHA256Hex(signingKey, canonicalRequest)
	return prefix + "/" + strings.Join(signedHeaders, ";") + "/" + signature, nil
}

func signBCEV2Request(request *http.Request, credentials BCECredentials, timestamp time.Time, region, service string) (string, error) {
	if request == nil || request.URL == nil {
		return "", fmt.Errorf("Baidu BCE request is required")
	}
	if credentials.AccessKeyID == "" || credentials.SecretAccessKey == "" {
		return "", fmt.Errorf("Baidu BCE AK/SK is required")
	}
	region = strings.ToLower(strings.TrimSpace(region))
	service = strings.ToLower(strings.TrimSpace(service))
	if !identifierPattern.MatchString(region) || !identifierPattern.MatchString(service) {
		return "", fmt.Errorf("Baidu BCE v2 requires valid region and service")
	}
	if request.Header.Get("x-bce-date") == "" {
		request.Header.Set("x-bce-date", timestamp.UTC().Format("2006-01-02T15:04:05Z"))
	}
	prefix := fmt.Sprintf("%s/%s/%s/%s/%s", bceAuthVersionV2, credentials.AccessKeyID, timestamp.UTC().Format("20060102"), region, service)
	signingKey := hmacSHA256Hex(credentials.SecretAccessKey, prefix)
	canonicalHeaders, signedHeaders := canonicalBCEHeaders(request)
	canonicalRequest := strings.Join([]string{
		strings.ToUpper(request.Method),
		canonicalBCEPath(request.URL.Path),
		canonicalBCEQuery(request.URL.Query()),
		canonicalHeaders,
	}, "\n")
	signature := hmacSHA256Hex(signingKey, canonicalRequest)
	return prefix + "/" + strings.Join(signedHeaders, ";") + "/" + signature, nil
}

func canonicalBCEPath(path string) string {
	if path == "" {
		return "/"
	}
	return "/" + bceURIEncode(strings.TrimPrefix(path, "/"), false)
}

func canonicalBCEQuery(values url.Values) string {
	items := make([]string, 0, len(values))
	for name, entries := range values {
		if strings.EqualFold(name, "authorization") {
			continue
		}
		if len(entries) == 0 {
			entries = []string{""}
		}
		for _, value := range entries {
			items = append(items, bceURIEncode(name, true)+"="+bceURIEncode(value, true))
		}
	}
	sort.Strings(items)
	return strings.Join(items, "&")
}

func canonicalBCEHeaders(request *http.Request) (string, []string) {
	headers := make(map[string]string, len(request.Header)+2)
	for name, values := range request.Header {
		headers[strings.ToLower(name)] = strings.Join(values, ",")
	}
	host := request.Host
	if host == "" {
		host = request.URL.Host
	}
	headers["host"] = host
	if request.ContentLength >= 0 && request.Body != nil && headers["content-length"] == "" {
		headers["content-length"] = fmt.Sprintf("%d", request.ContentLength)
	}
	defaultHeaders := map[string]struct{}{
		"host": {}, "content-length": {}, "content-type": {}, "content-md5": {},
	}
	canonical := make([]string, 0, len(headers))
	signed := make([]string, 0, len(headers))
	for name, value := range headers {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "authorization" || name == "x-bce-request-id" {
			continue
		}
		_, isDefault := defaultHeaders[name]
		if !isDefault && !strings.HasPrefix(name, "x-bce-") {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		canonical = append(canonical, bceURIEncode(name, true)+":"+bceURIEncode(value, true))
		signed = append(signed, name)
	}
	sort.Strings(canonical)
	sort.Strings(signed)
	return strings.Join(canonical, "\n"), signed
}

func bceURIEncode(value string, encodeSlash bool) string {
	var builder strings.Builder
	for _, character := range []byte(value) {
		if (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("-_.~", rune(character)) ||
			(character == '/' && !encodeSlash) {
			builder.WriteByte(character)
			continue
		}
		fmt.Fprintf(&builder, "%%%02X", character)
	}
	return builder.String()
}

func hmacSHA256Hex(key, value string) string {
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

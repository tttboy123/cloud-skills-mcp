package cloud

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azurepolicy "github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/coder/websocket"
)

const (
	authSchemeAzureServiceBusAMQPWS = "servicebus-amqp-ws"
	authSchemeAzureEventHubsAMQPWS  = "eventhubs-amqp-ws"
	azureServiceBusTokenScope       = "https://servicebus.azure.net//.default"
	azureEventHubsTokenScope        = "https://eventhubs.azure.net//.default"
	azureAMQPWebSocketPath          = "/$servicebus/websocket"
	azureAMQPMaxTimeoutSeconds      = 300
	azureAMQPMaxPropertyBytes       = 64 * 1024
)

var azureAMQPActiveNamespaceSuffixes = [...]string{
	".servicebus.windows.net",
	".servicebus.usgovcloudapi.net",
	".servicebus.chinacloudapi.cn",
}

type azureAMQPWebSocketDial func(context.Context, string) (net.Conn, error)

type azureServiceBusAMQPExecutor interface {
	Execute(context.Context, string, azureServiceBusAMQPPlan) ([]map[string]any, string, error)
}

type azureEventHubsAMQPExecutor interface {
	Execute(context.Context, string, azureEventHubsAMQPPlan) ([]map[string]any, string, error)
}

type azureAMQPTokenCredential struct {
	provider AzureTokenProvider
	now      func() time.Time
}

func (credential azureAMQPTokenCredential) GetToken(ctx context.Context, options azurepolicy.TokenRequestOptions) (azcore.AccessToken, error) {
	if credential.provider == nil || len(options.Scopes) != 1 || options.Scopes[0] != azureServiceBusTokenScope && options.Scopes[0] != azureEventHubsTokenScope {
		return azcore.AccessToken{}, fmt.Errorf("Azure AMQP token request must use the official Service Bus or Event Hubs scope")
	}
	token, err := credential.provider.Token(ctx, options.Scopes[0])
	if err != nil {
		return azcore.AccessToken{}, fmt.Errorf("load Azure messaging identity token: %w", err)
	}
	if strings.TrimSpace(token) == "" {
		return azcore.AccessToken{}, fmt.Errorf("Azure messaging identity returned an empty access token")
	}
	now := time.Now
	if credential.now != nil {
		now = credential.now
	}
	// The narrow provider abstraction intentionally doesn't export token metadata.
	// A short synthetic lifetime makes the Azure SDK refresh through the provider
	// well before a normal Entra access token expires.
	return azcore.AccessToken{Token: token, ExpiresOn: now().Add(20 * time.Minute)}, nil
}

func parseAzureAMQPWebSocketTarget(rawURL string) (string, error) {
	target, err := url.Parse(rawURL)
	if err != nil || target.Scheme != "wss" || target.User != nil || target.Fragment != "" || target.RawQuery != "" || target.Hostname() == "" || target.EscapedPath() != azureAMQPWebSocketPath || target.Port() != "" && target.Port() != "443" {
		return "", fmt.Errorf("Azure messaging AMQP requires an exact credential-free active-cloud Service Bus namespace WSS endpoint on port 443")
	}
	host := strings.ToLower(target.Hostname())
	for _, suffix := range azureAMQPActiveNamespaceSuffixes {
		name := strings.TrimSuffix(host, suffix)
		if name != host && !strings.Contains(name, ".") && endpointLabelPattern.MatchString(name) {
			return host, nil
		}
	}
	return "", fmt.Errorf("Azure messaging AMQP host must use exact namespace.servicebus.windows.net, namespace.servicebus.usgovcloudapi.net, or namespace.servicebus.chinacloudapi.cn endpoint form")
}

func defaultAzureAMQPWebSocketDial(ctx context.Context, target string) (net.Conn, error) {
	client := &http.Client{
		Timeout:       60 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return dialAzureAMQPWebSocket(ctx, target, client)
}

func dialAzureAMQPWebSocket(ctx context.Context, target string, client *http.Client) (net.Conn, error) {
	if client == nil {
		return nil, fmt.Errorf("Azure messaging AMQP WebSocket requires an HTTP client")
	}
	connection, response, err := websocket.Dial(ctx, target, &websocket.DialOptions{
		HTTPClient: client, Subprotocols: []string{"amqp"}, CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return nil, fmt.Errorf("Azure messaging AMQP WebSocket handshake failed")
	}
	if connection.Subprotocol() != "amqp" {
		connection.Close(websocket.StatusProtocolError, "AMQP subprotocol required")
		return nil, fmt.Errorf("Azure messaging endpoint did not negotiate the amqp WebSocket subprotocol")
	}
	return websocket.NetConn(context.Background(), connection, websocket.MessageBinary), nil
}

func validateAzureAMQPEnvelope(invocation Invocation) (string, error) {
	if !strings.EqualFold(invocation.Method, http.MethodGet) {
		return "", fmt.Errorf("Azure messaging AMQP over WebSocket requires GET")
	}
	fqdn, err := parseAzureAMQPWebSocketTarget(invocation.URL)
	if err != nil {
		return "", err
	}
	if len(invocation.Headers) != 0 || len(invocation.Parameters) != 0 || invocation.Body == nil || invocation.BodyFile != "" || invocation.ImageFile != "" || invocation.ProtobufDescriptorFile != "" || invocation.ResponseFile == "" {
		return "", fmt.Errorf("Azure messaging AMQP requires only a finite credential-free JSON plan and response_file")
	}
	if invocation.Region != "" || invocation.RegionSet != "" || invocation.Project != "" || invocation.Subscription != "" || invocation.Audience != "" || invocation.APIVersion != "" || invocation.AuthVersion != "" || invocation.PayloadMode != "" || invocation.ChecksumAlgorithm != "" || invocation.StreamChunkBytes != 0 || invocation.StreamIntervalMS != 0 || invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return "", fmt.Errorf("Azure messaging AMQP does not accept REST, cross-provider, or generic stream controls")
	}
	if azureRealtimeContainsCredentialField(invocation.Body) {
		return "", fmt.Errorf("Azure messaging AMQP plan must not contain credentials, tokens, or connection strings")
	}
	return fqdn, nil
}

func decodeAzureAMQPPlan(body any, destination any) error {
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) == 0 || len(encoded) > maxRequestPayloadBytes {
		return fmt.Errorf("Azure messaging AMQP plan must be bounded JSON")
	}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil || ensureJSONDecoderEOF(decoder) != nil {
		return fmt.Errorf("Azure messaging AMQP plan does not match the operation schema")
	}
	return nil
}

func validAzureMessagingEntity(value string, maximum int, allowDollar bool) bool {
	if value == "" || len(value) > maximum || !utf8.ValidString(value) || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") || strings.Contains(value, "//") || strings.ContainsAny(value, "?#%\\") {
		return false
	}
	for _, r := range value {
		if r < 0x21 || r == 0x7f || r > 0x7e || r == '$' && !allowDollar {
			return false
		}
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func validateAzureAMQPProperties(properties map[string]any) error {
	if len(properties) > 128 {
		return fmt.Errorf("AMQP property count exceeds 128")
	}
	encoded, err := json.Marshal(properties)
	if err != nil || len(encoded) > azureAMQPMaxPropertyBytes {
		return fmt.Errorf("AMQP properties exceed the 64 KiB gateway bound")
	}
	for key, value := range properties {
		if !validAzureMessagingEntity(key, 128, false) || !azureAMQPJSONScalar(value) {
			return fmt.Errorf("AMQP application properties require bounded names and scalar JSON values")
		}
	}
	return nil
}

func azureAMQPJSONScalar(value any) bool {
	switch typed := value.(type) {
	case nil, bool, string:
		return true
	case float64:
		return !math.IsNaN(typed) && !math.IsInf(typed, 0)
	case float32:
		return !math.IsNaN(float64(typed)) && !math.IsInf(float64(typed), 0)
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, json.Number:
		return true
	default:
		return false
	}
}

func azureAMQPSanitizeProperties(properties map[string]any) map[string]any {
	if len(properties) == 0 {
		return nil
	}
	keys := make([]string, 0, len(properties))
	for key := range properties {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make(map[string]any, len(keys))
	for _, key := range keys {
		result[key] = azureAMQPSanitizeValue(properties[key], 0)
	}
	return result
}

func azureAMQPSanitizeValue(value any, depth int) any {
	if value == nil || depth > 4 {
		return nil
	}
	switch typed := value.(type) {
	case string, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, json.Number:
		return typed
	case time.Time:
		return typed.UTC().Format(time.RFC3339Nano)
	case []byte:
		return map[string]any{"binary_base64": base64.StdEncoding.EncodeToString(typed)}
	case map[string]any:
		return azureAMQPSanitizeProperties(typed)
	}
	reflected := reflect.ValueOf(value)
	if reflected.IsValid() && (reflected.Kind() == reflect.Array || reflected.Kind() == reflect.Slice) {
		maximum := reflected.Len()
		if maximum > 128 {
			maximum = 128
		}
		items := make([]any, 0, maximum)
		for index := 0; index < maximum; index++ {
			items = append(items, azureAMQPSanitizeValue(reflected.Index(index).Interface(), depth+1))
		}
		return items
	}
	return fmt.Sprint(value)
}

func publishAzureAMQPNDJSON(invocation Invocation, protocol string, records []map[string]any, requestID string) (InvocationResult, error) {
	sink, err := newWebSocketOutputSink(invocation, defaultRESTBodyLimit, protocol)
	if err != nil {
		return InvocationResult{}, err
	}
	defer sink.abort()
	for _, record := range records {
		encoded, err := json.Marshal(record)
		if err != nil {
			return InvocationResult{}, fmt.Errorf("encode %s result", protocol)
		}
		if err := sink.writeMessage(encoded); err != nil {
			return InvocationResult{}, err
		}
	}
	output, err := sink.finish(requestID)
	if err != nil {
		return InvocationResult{}, err
	}
	return InvocationResult{Output: output, RequestID: requestID}, nil
}

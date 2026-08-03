package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/coder/websocket"
)

const (
	authSchemeAWSAppSyncGraphQLWS       = "appsync-graphql-ws"
	awsAppSyncGraphQLSubscribeOperation = "graphqlsubscribe"
	awsAppSyncGraphQLMaxMessages        = 256
	awsAppSyncGraphQLMaxTimeoutSeconds  = 300
)

type awsAppSyncGraphQLSubscribeConfig struct {
	Query          string         `json:"query"`
	Variables      map[string]any `json:"variables"`
	MaxMessages    int            `json:"max_messages"`
	TimeoutSeconds int            `json:"timeout_seconds"`
}

type awsAppSyncGraphQLEnvelope struct {
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

func validateAWSAppSyncGraphQLWebSocketInvocation(invocation Invocation, allowedEndpointHosts []string) error {
	if !strings.EqualFold(invocation.Service, awsAppSyncEventService) || normalizedOperation(invocation.Operation) != awsAppSyncGraphQLSubscribeOperation {
		return fmt.Errorf("AWS AppSync GraphQL WebSocket requires service appsync and operation GraphQLSubscribe")
	}
	if !strings.EqualFold(invocation.Method, http.MethodGet) || !identifierPattern.MatchString(invocation.Region) {
		return fmt.Errorf("AWS AppSync GraphQL WebSocket requires GET and a valid region")
	}
	if _, _, err := awsAppSyncGraphQLHTTPURLs(invocation.URL, invocation.Region, allowedEndpointHosts); err != nil {
		return err
	}
	if len(invocation.Parameters) != 0 || len(invocation.Headers) != 0 || invocation.BodyFile != "" || invocation.ResponseFile == "" {
		return fmt.Errorf("AWS AppSync GraphQL WebSocket requires only a protocol body and response_file; query, headers, and body_file are forbidden")
	}
	if invocation.RegionSet != "" || invocation.Project != "" || invocation.Subscription != "" || invocation.Audience != "" || invocation.AuthVersion != "" || invocation.APIVersion != "" || invocation.PayloadMode != "" || invocation.ChecksumAlgorithm != "" || invocation.StreamChunkBytes != 0 || invocation.StreamIntervalMS != 0 || invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return fmt.Errorf("AWS AppSync GraphQL WebSocket does not accept cross-provider, REST payload, or generic stream controls")
	}
	_, err := parseAWSAppSyncGraphQLSubscribeConfig(invocation.Body)
	return err
}

func awsAppSyncGraphQLHTTPURLs(rawURL, region string, allowedEndpointHosts []string) (string, string, error) {
	target, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(target.Scheme, "wss") || target.Hostname() == "" || target.Port() != "" || target.User != nil || target.Fragment != "" || target.RawQuery != "" {
		return "", "", fmt.Errorf("AWS AppSync GraphQL WebSocket requires an exact wss:// endpoint with no caller query")
	}
	host := strings.ToLower(target.Hostname())
	region = strings.ToLower(strings.TrimSpace(region))
	for _, domains := range [][2]string{
		{".appsync-realtime-api." + region + ".amazonaws.com", ".appsync-api." + region + ".amazonaws.com"},
		{".appsync-realtime-api." + region + ".amazonaws.com.cn", ".appsync-api." + region + ".amazonaws.com.cn"},
		{".appsync-realtime-api." + region + ".api.aws", ".appsync-api." + region + ".api.aws"},
	} {
		prefix := strings.TrimSuffix(host, domains[0])
		if prefix != host && prefix != "" && target.EscapedPath() == "/graphql" && validAdditionalEndpointHost(host) {
			base := "https://" + prefix + domains[1] + "/graphql"
			return base, base + "/connect", nil
		}
	}
	for _, candidate := range allowedEndpointHosts {
		if validAdditionalEndpointHost(candidate) && host == strings.ToLower(strings.TrimSpace(candidate)) && target.EscapedPath() == "/graphql/realtime" {
			base := "https://" + host + "/graphql"
			return base, base + "/connect", nil
		}
	}
	return "", "", fmt.Errorf("AWS AppSync GraphQL WebSocket host or path does not match the requested region or endpoint allowlist")
}

func parseAWSAppSyncGraphQLSubscribeConfig(body any) (awsAppSyncGraphQLSubscribeConfig, error) {
	if body == nil || alibabaNLSContainsCredentialField(body) {
		return awsAppSyncGraphQLSubscribeConfig{}, fmt.Errorf("AWS AppSync GraphQL WebSocket requires a credential-free protocol body")
	}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) == 0 || len(encoded) > maxRequestPayloadBytes {
		return awsAppSyncGraphQLSubscribeConfig{}, fmt.Errorf("AWS AppSync GraphQL WebSocket body must be bounded JSON")
	}
	var config awsAppSyncGraphQLSubscribeConfig
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil || ensureJSONDecoderEOF(decoder) != nil {
		return awsAppSyncGraphQLSubscribeConfig{}, fmt.Errorf("AWS AppSync GraphQL WebSocket body does not match the subscription schema")
	}
	if config.Variables == nil || alibabaNLSContainsCredentialField(config.Variables) {
		return awsAppSyncGraphQLSubscribeConfig{}, fmt.Errorf("AWS AppSync GraphQL variables must be a credential-free JSON object")
	}
	if err := validateAWSAppSyncGraphQLSubscription(config.Query); err != nil {
		return awsAppSyncGraphQLSubscribeConfig{}, err
	}
	if config.MaxMessages < 1 || config.MaxMessages > awsAppSyncGraphQLMaxMessages {
		return awsAppSyncGraphQLSubscribeConfig{}, fmt.Errorf("AWS AppSync GraphQL max_messages must be between 1 and %d", awsAppSyncGraphQLMaxMessages)
	}
	if config.TimeoutSeconds < 1 || config.TimeoutSeconds > awsAppSyncGraphQLMaxTimeoutSeconds {
		return awsAppSyncGraphQLSubscribeConfig{}, fmt.Errorf("AWS AppSync GraphQL timeout_seconds must be between 1 and %d", awsAppSyncGraphQLMaxTimeoutSeconds)
	}
	return config, nil
}

func validateAWSAppSyncGraphQLSubscription(query string) error {
	if query == "" || len(query) > maxRequestPayloadBytes || !utf8.ValidString(query) || strings.TrimSpace(query) != query {
		return fmt.Errorf("AWS AppSync GraphQL query must be bounded non-empty UTF-8")
	}
	for _, character := range query {
		if character == 0 || (unicode.IsControl(character) && character != '\n' && character != '\r' && character != '\t') {
			return fmt.Errorf("AWS AppSync GraphQL query contains a forbidden control character")
		}
	}
	subscriptions, invalidOperation, balanced := scanAWSAppSyncGraphQLDefinitions(query)
	if !balanced || invalidOperation || subscriptions != 1 {
		return fmt.Errorf("AWS AppSync GraphQL requires exactly one subscription operation and no query or mutation operation")
	}
	return nil
}

func scanAWSAppSyncGraphQLDefinitions(document string) (subscriptions int, invalidOperation, balanced bool) {
	depth := 0
	definition := ""
	expectingDefinition := true
	for index := 0; index < len(document); {
		character := document[index]
		if character == '#' {
			for index < len(document) && document[index] != '\n' {
				index++
			}
			continue
		}
		if character == '"' {
			var ok bool
			index, ok = skipAWSAppSyncGraphQLString(document, index)
			if !ok {
				return subscriptions, invalidOperation, false
			}
			continue
		}
		if character == '{' {
			if depth == 0 {
				if expectingDefinition || definition == "" {
					return subscriptions, true, false
				}
			}
			depth++
			index++
			continue
		}
		if character == '}' {
			if depth == 0 {
				return subscriptions, invalidOperation, false
			}
			depth--
			index++
			if depth == 0 {
				expectingDefinition = true
				definition = ""
			}
			continue
		}
		if depth == 0 && isAWSAppSyncGraphQLNameStart(character) {
			start := index
			for index < len(document) && isAWSAppSyncGraphQLNameContinue(document[index]) {
				index++
			}
			if expectingDefinition {
				definition = document[start:index]
				switch definition {
				case "subscription":
					subscriptions++
				case "query", "mutation":
					invalidOperation = true
				case "fragment":
				default:
					return subscriptions, true, false
				}
				expectingDefinition = false
			}
			continue
		}
		index++
	}
	return subscriptions, invalidOperation, depth == 0 && expectingDefinition
}

func skipAWSAppSyncGraphQLString(document string, start int) (int, bool) {
	if strings.HasPrefix(document[start:], `"""`) {
		if end := strings.Index(document[start+3:], `"""`); end >= 0 {
			return start + 3 + end + 3, true
		}
		return len(document), false
	}
	for index := start + 1; index < len(document); index++ {
		switch document[index] {
		case '\\':
			index++
		case '"':
			return index + 1, true
		case '\n', '\r':
			return len(document), false
		}
	}
	return len(document), false
}

func isAWSAppSyncGraphQLNameStart(character byte) bool {
	return character == '_' || character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z'
}

func isAWSAppSyncGraphQLNameContinue(character byte) bool {
	return isAWSAppSyncGraphQLNameStart(character) || character >= '0' && character <= '9'
}

func signAWSAppSyncGraphQLAuthorization(ctx context.Context, rawURL string, body []byte, credentials AWSCredentials, region string, signingTime time.Time) (map[string]string, error) {
	target, err := url.Parse(rawURL)
	if err != nil || (target.EscapedPath() != "/graphql" && target.EscapedPath() != "/graphql/connect") {
		return nil, fmt.Errorf("construct AWS AppSync GraphQL signing request")
	}
	return signAWSAppSyncAuthorization(ctx, rawURL, target.EscapedPath(), body, credentials, region, signingTime)
}

func readAWSAppSyncGraphQLEnvelope(ctx context.Context, connection cloudWebSocketConnection) (awsAppSyncGraphQLEnvelope, error) {
	messageType, data, err := connection.Read(ctx)
	if err != nil {
		return awsAppSyncGraphQLEnvelope{}, err
	}
	if messageType != cloudWebSocketMessageText || len(data) == 0 || len(data) > maxRequestPayloadBytes {
		return awsAppSyncGraphQLEnvelope{}, fmt.Errorf("AWS AppSync GraphQL WebSocket returned an invalid text message")
	}
	var envelope awsAppSyncGraphQLEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil || strings.TrimSpace(envelope.Type) == "" {
		return awsAppSyncGraphQLEnvelope{}, fmt.Errorf("AWS AppSync GraphQL WebSocket returned invalid JSON")
	}
	return envelope, nil
}

func waitAWSAppSyncGraphQLType(ctx context.Context, connection cloudWebSocketConnection, expectedType, expectedID string) error {
	for {
		envelope, err := readAWSAppSyncGraphQLEnvelope(ctx, connection)
		if err != nil {
			return err
		}
		if envelope.Type == "ka" && envelope.ID == "" {
			continue
		}
		if envelope.Type != expectedType || envelope.ID != expectedID {
			return fmt.Errorf("AWS AppSync GraphQL WebSocket returned an unexpected protocol message")
		}
		return nil
	}
}

func invokeAWSAppSyncGraphQLWebSocket(ctx context.Context, adapter *AWSRESTAdapter, credentials AWSCredentials, invocation Invocation) (InvocationResult, error) {
	if err := validateAWSAppSyncGraphQLWebSocketInvocation(invocation, adapter.config.AllowedHosts); err != nil {
		return InvocationResult{}, err
	}
	config, _ := parseAWSAppSyncGraphQLSubscribeConfig(invocation.Body)
	graphqlURL, connectURL, _ := awsAppSyncGraphQLHTTPURLs(invocation.URL, invocation.Region, adapter.config.AllowedHosts)
	connectAuthorization, err := signAWSAppSyncGraphQLAuthorization(ctx, connectURL, []byte("{}"), credentials, invocation.Region, adapter.config.Now().UTC())
	if err != nil {
		return InvocationResult{}, err
	}
	authProtocol, err := encodeAWSAppSyncAuthProtocol(connectAuthorization)
	if err != nil {
		return InvocationResult{}, err
	}
	subscriptionID, err := adapter.config.AppSyncGraphQLID()
	if err != nil || !awsAppSyncEventIDPattern.MatchString(subscriptionID) {
		return InvocationResult{}, fmt.Errorf("generate valid AWS AppSync GraphQL subscription ID")
	}
	handshakeCtx, cancelHandshake := context.WithTimeout(ctx, 30*time.Second)
	defer cancelHandshake()
	connection, err := adapter.config.AppSyncGraphQLWebSocketDial(handshakeCtx, invocation.URL, []string{"graphql-ws", authProtocol})
	if err != nil {
		return InvocationResult{}, err
	}
	defer connection.Close()
	sink, err := newWebSocketOutputSink(invocation, adapter.config.MaxBodyBytes, "AWS AppSync GraphQL WebSocket")
	if err != nil {
		return InvocationResult{}, err
	}
	defer sink.abort()
	if err := writeAWSAppSyncJSON(handshakeCtx, connection, map[string]string{"type": "connection_init"}, "GraphQL connection_init"); err != nil {
		return InvocationResult{}, err
	}
	for {
		envelope, readErr := readAWSAppSyncGraphQLEnvelope(handshakeCtx, connection)
		if readErr != nil {
			return InvocationResult{}, fmt.Errorf("read AWS AppSync GraphQL connection acknowledgement")
		}
		if envelope.Type == "ka" && envelope.ID == "" {
			continue
		}
		var payload struct {
			ConnectionTimeoutMS int `json:"connectionTimeoutMs"`
		}
		if envelope.Type != "connection_ack" || envelope.ID != "" || json.Unmarshal(envelope.Payload, &payload) != nil || payload.ConnectionTimeoutMS < 1 || payload.ConnectionTimeoutMS > awsAppSyncEventConnectionTimeout {
			return InvocationResult{}, fmt.Errorf("AWS AppSync GraphQL connection was not acknowledged")
		}
		break
	}
	data, err := json.Marshal(struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}{Query: config.Query, Variables: config.Variables})
	if err != nil {
		return InvocationResult{}, fmt.Errorf("encode AWS AppSync GraphQL subscription")
	}
	subscribeAuthorization, err := signAWSAppSyncGraphQLAuthorization(ctx, graphqlURL, data, credentials, invocation.Region, adapter.config.Now().UTC())
	if err != nil {
		return InvocationResult{}, err
	}
	start := map[string]any{
		"type": "start", "id": subscriptionID,
		"payload": map[string]any{
			"data": string(data), "extensions": map[string]any{"authorization": subscribeAuthorization},
		},
	}
	if err := writeAWSAppSyncJSON(handshakeCtx, connection, start, "GraphQL start"); err != nil {
		return InvocationResult{}, err
	}
	if err := waitAWSAppSyncGraphQLType(handshakeCtx, connection, "start_ack", subscriptionID); err != nil {
		return InvocationResult{}, fmt.Errorf("AWS AppSync GraphQL subscription was not acknowledged")
	}
	cancelHandshake()
	collectionCtx, cancelCollection := context.WithTimeout(ctx, time.Duration(config.TimeoutSeconds)*time.Second)
	received := 0
	for received < config.MaxMessages {
		envelope, readErr := readAWSAppSyncGraphQLEnvelope(collectionCtx, connection)
		if errors.Is(readErr, context.DeadlineExceeded) {
			break
		}
		if readErr != nil {
			cancelCollection()
			return InvocationResult{}, fmt.Errorf("read AWS AppSync GraphQL data")
		}
		if envelope.Type == "ka" && envelope.ID == "" {
			continue
		}
		payload := bytes.TrimSpace(envelope.Payload)
		if envelope.Type != "data" || envelope.ID != subscriptionID || len(payload) < 2 || payload[0] != '{' || payload[len(payload)-1] != '}' || !json.Valid(payload) {
			cancelCollection()
			return InvocationResult{}, fmt.Errorf("AWS AppSync GraphQL WebSocket returned an unexpected data message")
		}
		sanitized, marshalErr := json.Marshal(awsAppSyncGraphQLEnvelope{Type: "data", ID: subscriptionID, Payload: append(json.RawMessage(nil), payload...)})
		if marshalErr != nil {
			cancelCollection()
			return InvocationResult{}, fmt.Errorf("encode AWS AppSync GraphQL data")
		}
		if err := sink.writeMessage(sanitized); err != nil {
			cancelCollection()
			return InvocationResult{}, err
		}
		received++
	}
	cancelCollection()
	cleanupCtx, cancelCleanup := context.WithTimeout(ctx, 10*time.Second)
	defer cancelCleanup()
	if err := writeAWSAppSyncJSON(cleanupCtx, connection, map[string]string{"type": "stop", "id": subscriptionID}, "GraphQL stop"); err != nil {
		return InvocationResult{}, err
	}
	if err := waitAWSAppSyncGraphQLType(cleanupCtx, connection, "complete", subscriptionID); err != nil {
		return InvocationResult{}, fmt.Errorf("AWS AppSync GraphQL stop was not acknowledged")
	}
	output, err := sink.finish(subscriptionID)
	if err != nil {
		return InvocationResult{}, err
	}
	return InvocationResult{Output: output, RequestID: subscriptionID}, nil
}

func defaultAWSAppSyncGraphQLWebSocketDial(ctx context.Context, rawURL string, subprotocols []string) (cloudWebSocketConnection, error) {
	client := &http.Client{Transport: http.DefaultTransport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	connection, response, err := websocket.Dial(ctx, rawURL, &websocket.DialOptions{HTTPClient: client, CompressionMode: websocket.CompressionDisabled, Subprotocols: subprotocols})
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("AWS AppSync GraphQL WebSocket handshake failed with HTTP %d", response.StatusCode)
		}
		return nil, fmt.Errorf("AWS AppSync GraphQL WebSocket handshake failed")
	}
	connection.SetReadLimit(maxRequestPayloadBytes)
	return coderTencentWebSocketConnection{connection: connection}, nil
}

package cloud

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	aws "github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/coder/websocket"
)

const (
	authSchemeAWSAppSyncEventWS       = "appsync-event-ws"
	awsAppSyncEventService            = "appsync"
	awsAppSyncEventSubscribeOperation = "eventsubscribe"
	awsAppSyncEventMaxMessages        = 256
	awsAppSyncEventMaxTimeoutSeconds  = 300
	awsAppSyncEventConnectionTimeout  = 300000
	awsAppSyncEventMaxEventsPerFrame  = 5
)

var (
	awsAppSyncEventChannelPattern = regexp.MustCompile(`^/?[A-Za-z0-9](?:[A-Za-z0-9-]{0,48}[A-Za-z0-9])?(?:/[A-Za-z0-9](?:[A-Za-z0-9-]{0,48}[A-Za-z0-9])?){0,4}/?$`)
	awsAppSyncEventIDPattern      = regexp.MustCompile(`^[A-Za-z0-9_+-]{1,128}$`)
)

type awsAppSyncEventSubscribeConfig struct {
	Channel        string `json:"channel"`
	MaxMessages    int    `json:"max_messages"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

type awsAppSyncEventEnvelope struct {
	Type                string            `json:"type"`
	ID                  string            `json:"id,omitempty"`
	ConnectionTimeoutMS int               `json:"connectionTimeoutMs,omitempty"`
	Event               []json.RawMessage `json:"event,omitempty"`
}

func validateAWSAppSyncEventWebSocketInvocation(invocation Invocation, allowedEndpointHosts []string) error {
	if !strings.EqualFold(invocation.Service, awsAppSyncEventService) || normalizedOperation(invocation.Operation) != awsAppSyncEventSubscribeOperation {
		return fmt.Errorf("AWS AppSync Events WebSocket requires service appsync and operation EventSubscribe")
	}
	if !strings.EqualFold(invocation.Method, http.MethodGet) || !identifierPattern.MatchString(invocation.Region) {
		return fmt.Errorf("AWS AppSync Events WebSocket requires GET and a valid region")
	}
	if _, err := awsAppSyncEventHTTPURL(invocation.URL, invocation.Region, allowedEndpointHosts); err != nil {
		return err
	}
	if len(invocation.Parameters) != 0 || len(invocation.Headers) != 0 || invocation.BodyFile != "" || invocation.ResponseFile == "" {
		return fmt.Errorf("AWS AppSync Events WebSocket requires only a protocol body and response_file; query, headers, and body_file are forbidden")
	}
	if invocation.RegionSet != "" || invocation.Project != "" || invocation.Subscription != "" || invocation.Audience != "" || invocation.AuthVersion != "" || invocation.APIVersion != "" || invocation.PayloadMode != "" || invocation.ChecksumAlgorithm != "" || invocation.StreamChunkBytes != 0 || invocation.StreamIntervalMS != 0 || invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return fmt.Errorf("AWS AppSync Events WebSocket does not accept cross-provider, REST payload, or generic stream controls")
	}
	_, err := parseAWSAppSyncEventSubscribeConfig(invocation.Body)
	return err
}

func awsAppSyncEventHTTPURL(rawURL, region string, allowedEndpointHosts []string) (string, error) {
	target, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(target.Scheme, "wss") || target.Hostname() == "" || target.Port() != "" || target.User != nil || target.Fragment != "" || target.RawQuery != "" || target.EscapedPath() != "/event/realtime" {
		return "", fmt.Errorf("AWS AppSync Events WebSocket requires an exact wss:// endpoint with path /event/realtime and no caller query")
	}
	host := strings.ToLower(target.Hostname())
	region = strings.ToLower(strings.TrimSpace(region))
	for _, domains := range [][2]string{
		{".appsync-realtime-api." + region + ".amazonaws.com", ".appsync-api." + region + ".amazonaws.com"},
		{".appsync-realtime-api." + region + ".amazonaws.com.cn", ".appsync-api." + region + ".amazonaws.com.cn"},
		{".appsync-realtime-api." + region + ".api.aws", ".appsync-api." + region + ".api.aws"},
	} {
		prefix := strings.TrimSuffix(host, domains[0])
		if prefix != host && prefix != "" && validAdditionalEndpointHost(host) {
			return "https://" + prefix + domains[1] + "/event", nil
		}
	}
	for _, candidate := range allowedEndpointHosts {
		if validAdditionalEndpointHost(candidate) && host == strings.ToLower(strings.TrimSpace(candidate)) {
			return "https://" + host + "/event", nil
		}
	}
	return "", fmt.Errorf("AWS AppSync Events WebSocket host does not match the requested region or endpoint allowlist")
}

func parseAWSAppSyncEventSubscribeConfig(body any) (awsAppSyncEventSubscribeConfig, error) {
	if body == nil || alibabaNLSContainsCredentialField(body) {
		return awsAppSyncEventSubscribeConfig{}, fmt.Errorf("AWS AppSync Events WebSocket requires a credential-free protocol body")
	}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) == 0 || len(encoded) > maxRequestPayloadBytes {
		return awsAppSyncEventSubscribeConfig{}, fmt.Errorf("AWS AppSync Events WebSocket body must be bounded JSON")
	}
	var config awsAppSyncEventSubscribeConfig
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil || ensureJSONDecoderEOF(decoder) != nil {
		return awsAppSyncEventSubscribeConfig{}, fmt.Errorf("AWS AppSync Events WebSocket body does not match the subscription schema")
	}
	if !awsAppSyncEventChannelPattern.MatchString(config.Channel) {
		return awsAppSyncEventSubscribeConfig{}, fmt.Errorf("AWS AppSync Events channel does not match the documented one-to-five-segment format")
	}
	if config.MaxMessages < 1 || config.MaxMessages > awsAppSyncEventMaxMessages {
		return awsAppSyncEventSubscribeConfig{}, fmt.Errorf("AWS AppSync Events max_messages must be between 1 and %d", awsAppSyncEventMaxMessages)
	}
	if config.TimeoutSeconds < 1 || config.TimeoutSeconds > awsAppSyncEventMaxTimeoutSeconds {
		return awsAppSyncEventSubscribeConfig{}, fmt.Errorf("AWS AppSync Events timeout_seconds must be between 1 and %d", awsAppSyncEventMaxTimeoutSeconds)
	}
	return config, nil
}

func signAWSAppSyncEventAuthorization(ctx context.Context, rawURL string, body []byte, credentials AWSCredentials, region string, signingTime time.Time) (map[string]string, error) {
	return signAWSAppSyncAuthorization(ctx, rawURL, "/event", body, credentials, region, signingTime)
}

func signAWSAppSyncAuthorization(ctx context.Context, rawURL, expectedPath string, body []byte, credentials AWSCredentials, region string, signingTime time.Time) (map[string]string, error) {
	if credentials.AccessKeyID == "" || credentials.SecretAccessKey == "" {
		return nil, fmt.Errorf("AWS AppSync WebSocket requires complete AKSK credentials")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(body))
	if err != nil || request.URL.Scheme != "https" || request.URL.Hostname() == "" || request.URL.Port() != "" || request.URL.EscapedPath() != expectedPath || request.URL.RawQuery != "" || request.URL.User != nil || request.URL.Fragment != "" {
		return nil, fmt.Errorf("construct AWS AppSync signing request")
	}
	request.Header.Set("Accept", "application/json, text/javascript")
	request.Header.Set("Content-Encoding", "amz-1.0")
	request.Header.Set("Content-Type", "application/json; charset=UTF-8")
	awsCredentials := aws.Credentials{AccessKeyID: credentials.AccessKeyID, SecretAccessKey: credentials.SecretAccessKey, SessionToken: credentials.SessionToken}
	if err := awsv4.NewSigner().SignHTTP(ctx, awsCredentials, request, sha256Hex(body), awsAppSyncEventService, strings.ToLower(strings.TrimSpace(region)), signingTime.UTC()); err != nil {
		return nil, fmt.Errorf("sign AWS AppSync request")
	}
	authorization := map[string]string{
		"accept":           request.Header.Get("Accept"),
		"content-encoding": request.Header.Get("Content-Encoding"),
		"content-type":     request.Header.Get("Content-Type"),
		"host":             strings.ToLower(request.URL.Host),
		"x-amz-date":       request.Header.Get("X-Amz-Date"),
		"Authorization":    request.Header.Get("Authorization"),
	}
	if token := request.Header.Get("X-Amz-Security-Token"); token != "" {
		authorization["X-Amz-Security-Token"] = token
	}
	return authorization, nil
}

func encodeAWSAppSyncAuthProtocol(authorization map[string]string) (string, error) {
	encoded, err := json.Marshal(authorization)
	if err != nil {
		return "", fmt.Errorf("encode AWS AppSync authorization")
	}
	return "header-" + base64.RawURLEncoding.EncodeToString(encoded), nil
}

func newAWSAppSyncEventID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate AWS AppSync Events subscription ID")
	}
	return hex.EncodeToString(value), nil
}

func readAWSAppSyncEventEnvelope(ctx context.Context, connection cloudWebSocketConnection) (awsAppSyncEventEnvelope, []byte, error) {
	messageType, data, err := connection.Read(ctx)
	if err != nil {
		return awsAppSyncEventEnvelope{}, nil, err
	}
	if messageType != cloudWebSocketMessageText || len(data) == 0 || len(data) > maxRequestPayloadBytes {
		return awsAppSyncEventEnvelope{}, nil, fmt.Errorf("AWS AppSync Events WebSocket returned an invalid text message")
	}
	var envelope awsAppSyncEventEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil || strings.TrimSpace(envelope.Type) == "" {
		return awsAppSyncEventEnvelope{}, nil, fmt.Errorf("AWS AppSync Events WebSocket returned invalid JSON")
	}
	return envelope, data, nil
}

func writeAWSAppSyncJSON(ctx context.Context, connection cloudWebSocketConnection, value any, label string) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode AWS AppSync %s", label)
	}
	if err := connection.Write(ctx, cloudWebSocketMessageText, encoded); err != nil {
		return fmt.Errorf("send AWS AppSync %s", label)
	}
	return nil
}

func validateAWSAppSyncEventValues(values []json.RawMessage) error {
	if len(values) == 0 || len(values) > awsAppSyncEventMaxEventsPerFrame {
		return fmt.Errorf("AWS AppSync Events data must contain between 1 and %d events", awsAppSyncEventMaxEventsPerFrame)
	}
	for _, rawValue := range values {
		var value string
		if len(rawValue) == 0 || len(rawValue) > maxRequestPayloadBytes || json.Unmarshal(rawValue, &value) != nil || !json.Valid([]byte(value)) {
			return fmt.Errorf("AWS AppSync Events data contained a non-stringified JSON event")
		}
	}
	return nil
}

func waitAWSAppSyncEventType(ctx context.Context, connection cloudWebSocketConnection, expectedType, expectedID string) error {
	for {
		envelope, _, err := readAWSAppSyncEventEnvelope(ctx, connection)
		if err != nil {
			return err
		}
		if envelope.Type == "ka" && envelope.ID == "" {
			continue
		}
		if envelope.Type != expectedType || envelope.ID != expectedID {
			return fmt.Errorf("AWS AppSync Events WebSocket returned an unexpected protocol message")
		}
		return nil
	}
}

func invokeAWSAppSyncEventWebSocket(ctx context.Context, adapter *AWSRESTAdapter, credentials AWSCredentials, invocation Invocation) (InvocationResult, error) {
	if err := validateAWSAppSyncEventWebSocketInvocation(invocation, adapter.config.AllowedHosts); err != nil {
		return InvocationResult{}, err
	}
	config, _ := parseAWSAppSyncEventSubscribeConfig(invocation.Body)
	httpURL, _ := awsAppSyncEventHTTPURL(invocation.URL, invocation.Region, adapter.config.AllowedHosts)
	connectAuthorization, err := signAWSAppSyncEventAuthorization(ctx, httpURL, []byte("{}"), credentials, invocation.Region, adapter.config.Now().UTC())
	if err != nil {
		return InvocationResult{}, err
	}
	authProtocol, err := encodeAWSAppSyncAuthProtocol(connectAuthorization)
	if err != nil {
		return InvocationResult{}, err
	}
	subscriptionID, err := adapter.config.AppSyncEventID()
	if err != nil || !awsAppSyncEventIDPattern.MatchString(subscriptionID) {
		return InvocationResult{}, fmt.Errorf("generate valid AWS AppSync Events subscription ID")
	}
	handshakeCtx, cancelHandshake := context.WithTimeout(ctx, 30*time.Second)
	defer cancelHandshake()
	connection, err := adapter.config.AppSyncEventWebSocketDial(handshakeCtx, invocation.URL, []string{"aws-appsync-event-ws", authProtocol})
	if err != nil {
		return InvocationResult{}, err
	}
	defer connection.Close()
	sink, err := newWebSocketOutputSink(invocation, adapter.config.MaxBodyBytes, "AWS AppSync Events WebSocket")
	if err != nil {
		return InvocationResult{}, err
	}
	defer sink.abort()
	if err := writeAWSAppSyncJSON(handshakeCtx, connection, map[string]string{"type": "connection_init"}, "Events connection_init"); err != nil {
		return InvocationResult{}, err
	}
	for {
		envelope, _, readErr := readAWSAppSyncEventEnvelope(handshakeCtx, connection)
		if readErr != nil {
			return InvocationResult{}, fmt.Errorf("read AWS AppSync Events connection acknowledgement")
		}
		if envelope.Type == "ka" && envelope.ID == "" {
			continue
		}
		if envelope.Type != "connection_ack" || envelope.ID != "" || envelope.ConnectionTimeoutMS < 1 || envelope.ConnectionTimeoutMS > awsAppSyncEventConnectionTimeout {
			return InvocationResult{}, fmt.Errorf("AWS AppSync Events connection was not acknowledged")
		}
		break
	}
	channelBody, _ := json.Marshal(struct {
		Channel string `json:"channel"`
	}{Channel: config.Channel})
	subscribeAuthorization, err := signAWSAppSyncEventAuthorization(ctx, httpURL, channelBody, credentials, invocation.Region, adapter.config.Now().UTC())
	if err != nil {
		return InvocationResult{}, err
	}
	if err := writeAWSAppSyncJSON(handshakeCtx, connection, map[string]any{"type": "subscribe", "id": subscriptionID, "channel": config.Channel, "authorization": subscribeAuthorization}, "Events subscribe"); err != nil {
		return InvocationResult{}, err
	}
	if err := waitAWSAppSyncEventType(handshakeCtx, connection, "subscribe_success", subscriptionID); err != nil {
		return InvocationResult{}, fmt.Errorf("AWS AppSync Events subscription was not acknowledged")
	}
	cancelHandshake()
	collectionCtx, cancelCollection := context.WithTimeout(ctx, time.Duration(config.TimeoutSeconds)*time.Second)
	received := 0
	for received < config.MaxMessages {
		envelope, _, readErr := readAWSAppSyncEventEnvelope(collectionCtx, connection)
		if errors.Is(readErr, context.DeadlineExceeded) {
			break
		}
		if readErr != nil {
			cancelCollection()
			return InvocationResult{}, fmt.Errorf("read AWS AppSync Events data")
		}
		if envelope.Type == "ka" && envelope.ID == "" {
			continue
		}
		if envelope.Type != "data" || envelope.ID != subscriptionID || validateAWSAppSyncEventValues(envelope.Event) != nil {
			cancelCollection()
			return InvocationResult{}, fmt.Errorf("AWS AppSync Events WebSocket returned an unexpected data message")
		}
		sanitized, marshalErr := json.Marshal(awsAppSyncEventEnvelope{Type: "data", ID: subscriptionID, Event: envelope.Event})
		if marshalErr != nil {
			cancelCollection()
			return InvocationResult{}, fmt.Errorf("encode AWS AppSync Events data")
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
	if err := writeAWSAppSyncJSON(cleanupCtx, connection, map[string]string{"type": "unsubscribe", "id": subscriptionID}, "Events unsubscribe"); err != nil {
		return InvocationResult{}, err
	}
	if err := waitAWSAppSyncEventType(cleanupCtx, connection, "unsubscribe_success", subscriptionID); err != nil {
		return InvocationResult{}, fmt.Errorf("AWS AppSync Events unsubscribe was not acknowledged")
	}
	output, err := sink.finish(subscriptionID)
	if err != nil {
		return InvocationResult{}, err
	}
	return InvocationResult{Output: output, RequestID: subscriptionID}, nil
}

func defaultAWSAppSyncEventWebSocketDial(ctx context.Context, rawURL string, subprotocols []string) (cloudWebSocketConnection, error) {
	client := &http.Client{Transport: http.DefaultTransport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	connection, response, err := websocket.Dial(ctx, rawURL, &websocket.DialOptions{HTTPClient: client, CompressionMode: websocket.CompressionDisabled, Subprotocols: subprotocols})
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("AWS AppSync Events WebSocket handshake failed with HTTP %d", response.StatusCode)
		}
		return nil, fmt.Errorf("AWS AppSync Events WebSocket handshake failed")
	}
	connection.SetReadLimit(maxRequestPayloadBytes)
	return coderTencentWebSocketConnection{connection: connection}, nil
}

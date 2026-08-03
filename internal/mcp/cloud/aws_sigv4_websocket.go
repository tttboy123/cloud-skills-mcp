package cloud

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	aws "github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/coder/websocket"
)

const (
	authSchemeAWSSigV4WS      = "sigv4-ws"
	awsSigV4WSMaxMessages     = 256
	awsSigV4WSMaxTimeout      = 300
	awsSigV4WSMaxClientFrames = 256
	awsAgentCoreMaxFrameBytes = 32 * 1024
)

type awsSigV4WebSocketMessage struct {
	Type       string          `json:"type"`
	Data       json.RawMessage `json:"data,omitempty"`
	DataBase64 string          `json:"data_base64,omitempty"`

	messageType cloudWebSocketMessageType
	payload     []byte
}

type awsSigV4WebSocketPlan struct {
	Messages       []awsSigV4WebSocketMessage `json:"messages"`
	MaxMessages    int                        `json:"max_messages"`
	TimeoutSeconds int                        `json:"timeout_seconds"`
}

func validateAWSSigV4WebSocketInvocation(invocation Invocation, allowedEndpointHosts []string) error {
	if !strings.EqualFold(invocation.Method, http.MethodGet) || !identifierPattern.MatchString(invocation.Service) || !identifierPattern.MatchString(invocation.Operation) || !identifierPattern.MatchString(invocation.Region) {
		return fmt.Errorf("AWS SigV4 WebSocket requires GET and valid service, operation, and region")
	}
	if err := validateAWSSigV4WebSocketTarget(invocation.URL, allowedEndpointHosts); err != nil {
		return err
	}
	for name := range invocation.Headers {
		lower := strings.ToLower(strings.TrimSpace(name))
		if lower == "connection" || lower == "upgrade" || strings.HasPrefix(lower, "sec-websocket-") {
			return fmt.Errorf("caller-supplied WebSocket handshake control header %q is forbidden", name)
		}
		if isAWSSigV4WebSocketCredentialName(lower) {
			return fmt.Errorf("caller-supplied WebSocket credential header %q is forbidden", name)
		}
	}
	for name := range invocation.Parameters {
		if isAWSSigV4WebSocketCredentialName(name) {
			return fmt.Errorf("caller-supplied WebSocket credential query parameter %q is forbidden", name)
		}
	}
	if invocation.Body == nil || invocation.BodyFile != "" || invocation.ResponseFile == "" {
		return fmt.Errorf("AWS SigV4 WebSocket requires a finite protocol body and response_file; body_file is forbidden")
	}
	if invocation.RegionSet != "" || invocation.Project != "" || invocation.Subscription != "" || invocation.Audience != "" || invocation.AuthVersion != "" || invocation.APIVersion != "" || invocation.PayloadMode != "" || invocation.ChecksumAlgorithm != "" || invocation.StreamChunkBytes != 0 || invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return fmt.Errorf("AWS SigV4 WebSocket does not accept cross-provider, REST payload, checksum, SigV4a, or binary stream controls")
	}
	plan, err := parseAWSSigV4WebSocketPlan(invocation.Body)
	if err != nil {
		return err
	}
	if isAWSAgentCoreWebSocketTarget(invocation.URL) {
		for _, message := range plan.Messages {
			if len(message.payload) > awsAgentCoreMaxFrameBytes {
				return fmt.Errorf("Amazon Bedrock AgentCore WebSocket frames must not exceed %d bytes", awsAgentCoreMaxFrameBytes)
			}
		}
	}
	return nil
}

func isAWSAgentCoreWebSocketTarget(rawURL string) bool {
	target, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(target.Hostname())
	return strings.HasPrefix(host, "bedrock-agentcore.") && (strings.HasSuffix(host, ".amazonaws.com") || strings.HasSuffix(host, ".amazonaws.com.cn") || strings.HasSuffix(host, ".api.aws"))
}

func validateAWSSigV4WebSocketTarget(rawURL string, allowedEndpointHosts []string) error {
	target, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(target.Scheme, "wss") || target.Hostname() == "" || target.User != nil || target.Fragment != "" {
		return fmt.Errorf("AWS SigV4 WebSocket requires an exact wss:// provider URL")
	}
	if target.Port() != "" && target.Port() != "443" {
		return fmt.Errorf("AWS SigV4 WebSocket endpoint port must be 443")
	}
	for name := range target.Query() {
		if isAWSSigV4WebSocketCredentialName(name) {
			return fmt.Errorf("caller-supplied WebSocket credential query parameter %q is forbidden", name)
		}
	}
	httpsTarget := *target
	httpsTarget.Scheme = "https"
	if err := validateRESTTargetWithEndpointHosts(ProviderAWS, http.MethodGet, httpsTarget.String(), allowedEndpointHosts); err != nil {
		return fmt.Errorf("AWS SigV4 WebSocket target: %w", err)
	}
	return nil
}

func isAWSSigV4WebSocketCredentialName(name string) bool {
	normalized := strings.NewReplacer("-", "", "_", "").Replace(strings.ToLower(strings.TrimSpace(name)))
	for _, term := range []string{"authorization", "credential", "signature", "secret", "token", "apikey", "accesskey"} {
		if strings.Contains(normalized, term) {
			return true
		}
	}
	return false
}

func parseAWSSigV4WebSocketPlan(body any) (awsSigV4WebSocketPlan, error) {
	if body == nil || alibabaNLSContainsCredentialField(body) {
		return awsSigV4WebSocketPlan{}, fmt.Errorf("AWS SigV4 WebSocket requires a credential-free protocol body")
	}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) == 0 || len(encoded) > maxRequestPayloadBytes {
		return awsSigV4WebSocketPlan{}, fmt.Errorf("AWS SigV4 WebSocket body must be bounded JSON")
	}
	var plan awsSigV4WebSocketPlan
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil || ensureJSONDecoderEOF(decoder) != nil {
		return awsSigV4WebSocketPlan{}, fmt.Errorf("AWS SigV4 WebSocket body does not match the finite message-plan schema")
	}
	if len(plan.Messages) > awsSigV4WSMaxClientFrames {
		return awsSigV4WebSocketPlan{}, fmt.Errorf("AWS SigV4 WebSocket messages must contain at most %d frames", awsSigV4WSMaxClientFrames)
	}
	if plan.MaxMessages < 1 || plan.MaxMessages > awsSigV4WSMaxMessages {
		return awsSigV4WebSocketPlan{}, fmt.Errorf("AWS SigV4 WebSocket max_messages must be between 1 and %d", awsSigV4WSMaxMessages)
	}
	if plan.TimeoutSeconds < 1 || plan.TimeoutSeconds > awsSigV4WSMaxTimeout {
		return awsSigV4WebSocketPlan{}, fmt.Errorf("AWS SigV4 WebSocket timeout_seconds must be between 1 and %d", awsSigV4WSMaxTimeout)
	}
	for index := range plan.Messages {
		if err := prepareAWSSigV4WebSocketMessage(&plan.Messages[index]); err != nil {
			return awsSigV4WebSocketPlan{}, fmt.Errorf("AWS SigV4 WebSocket message %d: %w", index, err)
		}
	}
	return plan, nil
}

func prepareAWSSigV4WebSocketMessage(message *awsSigV4WebSocketMessage) error {
	switch strings.ToLower(strings.TrimSpace(message.Type)) {
	case "json":
		if len(message.Data) == 0 || len(message.Data) > maxRequestPayloadBytes || !json.Valid(message.Data) || message.DataBase64 != "" {
			return fmt.Errorf("json frame requires only bounded valid data")
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, message.Data); err != nil {
			return fmt.Errorf("compact json frame")
		}
		message.messageType = cloudWebSocketMessageText
		message.payload = compact.Bytes()
	case "text":
		var value string
		if len(message.Data) == 0 || message.DataBase64 != "" || json.Unmarshal(message.Data, &value) != nil || len(value) > maxRequestPayloadBytes || !utf8.ValidString(value) {
			return fmt.Errorf("text frame requires only one bounded UTF-8 string in data")
		}
		message.messageType = cloudWebSocketMessageText
		message.payload = []byte(value)
	case "binary":
		if len(message.Data) != 0 || message.DataBase64 == "" {
			return fmt.Errorf("binary frame requires only data_base64")
		}
		decoded, err := base64.StdEncoding.Strict().DecodeString(message.DataBase64)
		if err != nil || len(decoded) > maxRequestPayloadBytes {
			return fmt.Errorf("binary frame data_base64 is invalid or too large")
		}
		message.messageType = cloudWebSocketMessageBinary
		message.payload = decoded
	default:
		return fmt.Errorf("type must be json, text, or binary")
	}
	return nil
}

func signAWSSigV4WebSocketHandshake(ctx context.Context, rawURL string, parameters map[string]any, headers map[string]string, credentials AWSCredentials, service, region string, signingTime time.Time) (string, http.Header, error) {
	if credentials.AccessKeyID == "" || credentials.SecretAccessKey == "" || !identifierPattern.MatchString(service) || !identifierPattern.MatchString(region) {
		return "", nil, fmt.Errorf("AWS SigV4 WebSocket requires complete credentials, service, and region")
	}
	target, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(target.Scheme, "wss") {
		return "", nil, fmt.Errorf("parse AWS SigV4 WebSocket target")
	}
	if err := addQueryParameters(target, parameters); err != nil {
		return "", nil, fmt.Errorf("build AWS SigV4 WebSocket query: %w", err)
	}
	signedTarget := *target
	signedTarget.Scheme = "https"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, signedTarget.String(), nil)
	if err != nil {
		return "", nil, fmt.Errorf("build AWS SigV4 WebSocket handshake")
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	awsCredentials := aws.Credentials{AccessKeyID: credentials.AccessKeyID, SecretAccessKey: credentials.SecretAccessKey, SessionToken: credentials.SessionToken}
	if err := awsv4.NewSigner().SignHTTP(ctx, awsCredentials, request, sha256Hex(nil), strings.ToLower(service), strings.ToLower(region), signingTime.UTC()); err != nil {
		return "", nil, fmt.Errorf("sign AWS SigV4 WebSocket handshake")
	}
	target.RawQuery = request.URL.RawQuery
	return target.String(), request.Header.Clone(), nil
}

func invokeAWSSigV4WebSocket(ctx context.Context, adapter *AWSRESTAdapter, credentials AWSCredentials, invocation Invocation) (InvocationResult, error) {
	if err := validateAWSSigV4WebSocketInvocation(invocation, adapter.config.AllowedHosts); err != nil {
		return InvocationResult{}, err
	}
	plan, _ := parseAWSSigV4WebSocketPlan(invocation.Body)
	target, handshake, err := signAWSSigV4WebSocketHandshake(ctx, invocation.URL, invocation.Parameters, invocation.Headers, credentials, invocation.Service, invocation.Region, adapter.config.Now().UTC())
	if err != nil {
		return InvocationResult{}, err
	}
	dialCtx, cancelDial := context.WithTimeout(ctx, adapter.config.Timeout)
	connection, err := adapter.config.SigV4WebSocketDial(dialCtx, target, handshake)
	cancelDial()
	if err != nil {
		return InvocationResult{}, err
	}
	defer connection.Close()
	sink, err := newWebSocketOutputSink(invocation, adapter.config.MaxBodyBytes, "AWS SigV4 WebSocket")
	if err != nil {
		return InvocationResult{}, err
	}
	defer sink.abort()

	sessionCtx, cancelSession := context.WithTimeout(ctx, time.Duration(plan.TimeoutSeconds)*time.Second)
	defer cancelSession()
	interval := time.Duration(invocation.StreamIntervalMS) * time.Millisecond
	for index, message := range plan.Messages {
		if err := connection.Write(sessionCtx, message.messageType, message.payload); err != nil {
			return InvocationResult{}, fmt.Errorf("send AWS SigV4 WebSocket client message")
		}
		if interval > 0 && index+1 < len(plan.Messages) {
			if err := adapter.config.StreamPause(sessionCtx, interval); err != nil {
				return InvocationResult{}, fmt.Errorf("pace AWS SigV4 WebSocket client messages")
			}
		}
	}

	received := 0
	for received < plan.MaxMessages {
		messageType, data, readErr := connection.Read(sessionCtx)
		if errors.Is(readErr, context.DeadlineExceeded) {
			break
		}
		if readErr != nil {
			if readErr == io.EOF || websocket.CloseStatus(readErr) == websocket.StatusNormalClosure {
				break
			}
			return InvocationResult{}, fmt.Errorf("read AWS SigV4 WebSocket response")
		}
		if len(data) > maxRequestPayloadBytes {
			return InvocationResult{}, fmt.Errorf("AWS SigV4 WebSocket returned an oversized message")
		}
		var output any
		switch messageType {
		case cloudWebSocketMessageText:
			if !utf8.Valid(data) {
				return InvocationResult{}, fmt.Errorf("AWS SigV4 WebSocket returned invalid UTF-8 text")
			}
			output = map[string]string{"type": "text", "data": string(data)}
		case cloudWebSocketMessageBinary:
			output = map[string]string{"type": "binary", "data_base64": base64.StdEncoding.EncodeToString(data)}
		default:
			return InvocationResult{}, fmt.Errorf("AWS SigV4 WebSocket returned an unsupported message type")
		}
		encoded, err := json.Marshal(output)
		if err != nil {
			return InvocationResult{}, fmt.Errorf("encode AWS SigV4 WebSocket response")
		}
		if err := sink.writeMessage(encoded); err != nil {
			return InvocationResult{}, err
		}
		received++
	}
	metadata, err := sink.finish("")
	if err != nil {
		return InvocationResult{}, err
	}
	var summary map[string]any
	if err := json.Unmarshal(metadata, &summary); err != nil {
		return InvocationResult{}, fmt.Errorf("decode AWS SigV4 WebSocket output metadata")
	}
	summary["messages"] = received
	metadata, err = json.Marshal(summary)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("encode AWS SigV4 WebSocket output metadata")
	}
	return InvocationResult{Output: metadata}, nil
}

func defaultAWSSigV4WebSocketDial(ctx context.Context, target string, headers http.Header) (cloudWebSocketConnection, error) {
	client := &http.Client{Transport: http.DefaultTransport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	connection, response, err := websocket.Dial(ctx, target, &websocket.DialOptions{HTTPClient: client, HTTPHeader: headers.Clone(), CompressionMode: websocket.CompressionDisabled})
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("AWS SigV4 WebSocket handshake failed with HTTP %d", response.StatusCode)
		}
		return nil, fmt.Errorf("AWS SigV4 WebSocket handshake failed")
	}
	connection.SetReadLimit(maxRequestPayloadBytes)
	return coderTencentWebSocketConnection{connection: connection}, nil
}

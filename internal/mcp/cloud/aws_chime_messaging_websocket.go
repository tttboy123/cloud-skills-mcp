package cloud

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	aws "github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/coder/websocket"
)

const (
	authSchemeAWSChimeMessagingWS       = "chime-messaging-ws"
	awsChimeMessagingService            = "chime"
	awsChimeMessagingSubscribeOperation = "subscribemessages"
	awsChimeMessagingMaxMessages        = 256
	awsChimeMessagingMaxTimeoutSeconds  = 300
	awsChimeMessagingMaxARN             = 512
	awsChimeMessagingEndpointHost       = "data-messaging.chime.aws"
	awsChimeMessagingEndpointPath       = "/connect"
	awsChimeMessagingTokenResponseLimit = 32 * 1024
	awsChimeMessagingMinExpires         = 10
	awsChimeMessagingMaxExpires         = 3600
	awsChimeMessagingDefaultExpires     = 60
)

var (
	awsChimeMessagingUserARNPattern   = regexp.MustCompile(`^arn:aws:chime:[a-z0-9-]+:[0-9]{12}:app-instance/[A-Za-z0-9][A-Za-z0-9-]{0,63}/user/[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	awsChimeMessagingSessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	awsChimeMessagingEventTypes       = map[string]struct{}{
		"SESSION_ESTABLISHED":             {},
		"CHANNEL_DETAILS":                 {},
		"CREATE_CHANNEL_MESSAGE":          {},
		"REDACT_CHANNEL_MESSAGE":          {},
		"UPDATE_CHANNEL_MESSAGE":          {},
		"DELETE_CHANNEL_MESSAGE":          {},
		"PENDING_CREATE_CHANNEL_MESSAGE":  {},
		"PENDING_UPDATE_CHANNEL_MESSAGE":  {},
		"FAILED_CREATE_CHANNEL_MESSAGE":   {},
		"FAILED_UPDATE_CHANNEL_MESSAGE":   {},
		"DENIED_CREATE_CHANNEL_MESSAGE":   {},
		"DENIED_UPDATE_CHANNEL_MESSAGE":   {},
		"UPDATE_CHANNEL":                  {},
		"DELETE_CHANNEL":                  {},
		"BATCH_CREATE_CHANNEL_MEMBERSHIP": {},
		"CREATE_CHANNEL_MEMBERSHIP":       {},
		"DELETE_CHANNEL_MEMBERSHIP":       {},
		"UPDATE_CHANNEL_MEMBERSHIP":       {},
	}
	awsChimeMessagingMessageTypes = map[string]struct{}{
		"STANDARD": {}, "CONTROL": {}, "SYSTEM": {},
	}
)

type awsChimeMessagingPlan struct {
	UserARN               string `json:"user_arn"`
	SessionID             string `json:"session_id"`
	PrefetchOnConnect     bool   `json:"prefetch_on_connect,omitempty"`
	ConnectExpiresSeconds int    `json:"connect_expires_seconds,omitempty"`
	MaxMessages           int    `json:"max_messages"`
	TimeoutSeconds        int    `json:"timeout_seconds"`
}

func validateAWSChimeMessagingInvocation(invocation Invocation) error {
	if !strings.EqualFold(invocation.Method, http.MethodGet) || !strings.EqualFold(invocation.Service, "chime-messaging") || normalizedOperation(invocation.Operation) != awsChimeMessagingSubscribeOperation {
		return fmt.Errorf("AWS Chime messaging WebSocket requires GET, service chime-messaging, and operation SubscribeMessages")
	}
	if !identifierPattern.MatchString(invocation.Region) {
		return fmt.Errorf("AWS Chime messaging requires a valid region")
	}
	if err := validateAWSChimeMessagingTarget(invocation.URL); err != nil {
		return err
	}
	if len(invocation.Parameters) != 0 || len(invocation.Headers) != 0 || invocation.BodyFile != "" || invocation.ResponseFile == "" {
		return fmt.Errorf("AWS Chime messaging WebSocket requires only a protocol body and response_file; query, headers, and body_file are forbidden")
	}
	if invocation.RegionSet != "" || invocation.Project != "" || invocation.Subscription != "" || invocation.Audience != "" || invocation.AuthVersion != "" || invocation.APIVersion != "" || invocation.PayloadMode != "" || invocation.ChecksumAlgorithm != "" || invocation.StreamChunkBytes != 0 || invocation.StreamIntervalMS != 0 || invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return fmt.Errorf("AWS Chime messaging WebSocket does not accept cross-provider, REST payload, checksum, SigV4a, or generic stream controls")
	}
	_, err := parseAWSChimeMessagingPlan(invocation.Body)
	return err
}

func validateAWSChimeMessagingTarget(rawURL string) error {
	target, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(target.Scheme, "wss") || target.User != nil || target.Fragment != "" || target.Port() != "" || target.RawQuery != "" {
		return fmt.Errorf("AWS Chime messaging requires an exact query-free wss:// connect URL")
	}
	if strings.ToLower(target.Hostname()) != awsChimeMessagingEndpointHost {
		return fmt.Errorf("AWS Chime messaging requires the exact official %s host", awsChimeMessagingEndpointHost)
	}
	if target.EscapedPath() != awsChimeMessagingEndpointPath {
		return fmt.Errorf("AWS Chime messaging requires the official /connect path")
	}
	return nil
}

func parseAWSChimeMessagingPlan(body any) (awsChimeMessagingPlan, error) {
	if body == nil || alibabaNLSContainsCredentialField(body) {
		return awsChimeMessagingPlan{}, fmt.Errorf("AWS Chime messaging requires a credential-free protocol body")
	}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) == 0 || len(encoded) > maxRequestPayloadBytes {
		return awsChimeMessagingPlan{}, fmt.Errorf("AWS Chime messaging body must be bounded JSON")
	}
	var plan awsChimeMessagingPlan
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&plan) != nil || ensureJSONDecoderEOF(decoder) != nil {
		return awsChimeMessagingPlan{}, fmt.Errorf("AWS Chime messaging body does not match the finite session schema")
	}
	if !awsChimeMessagingUserARNPattern.MatchString(plan.UserARN) || len(plan.UserARN) > awsChimeMessagingMaxARN {
		return awsChimeMessagingPlan{}, fmt.Errorf("AWS Chime messaging user_arn must be one official AppInstanceUser ARN")
	}
	if !awsChimeMessagingSessionIDPattern.MatchString(plan.SessionID) {
		return awsChimeMessagingPlan{}, fmt.Errorf("AWS Chime messaging session_id must be one bounded unique identifier")
	}
	if plan.ConnectExpiresSeconds == 0 {
		plan.ConnectExpiresSeconds = awsChimeMessagingDefaultExpires
	}
	if plan.ConnectExpiresSeconds < awsChimeMessagingMinExpires || plan.ConnectExpiresSeconds > awsChimeMessagingMaxExpires {
		return awsChimeMessagingPlan{}, fmt.Errorf("AWS Chime messaging connect_expires_seconds must be between %d and %d", awsChimeMessagingMinExpires, awsChimeMessagingMaxExpires)
	}
	if plan.MaxMessages < 1 || plan.MaxMessages > awsChimeMessagingMaxMessages || plan.TimeoutSeconds < 1 || plan.TimeoutSeconds > awsChimeMessagingMaxTimeoutSeconds {
		return awsChimeMessagingPlan{}, fmt.Errorf("AWS Chime messaging response count or timeout is outside the finite bound")
	}
	return plan, nil
}

func invokeAWSChimeMessagingWebSocket(ctx context.Context, adapter *AWSRESTAdapter, credentials AWSCredentials, invocation Invocation) (InvocationResult, error) {
	if err := validateAWSChimeMessagingInvocation(invocation); err != nil {
		return InvocationResult{}, err
	}
	plan, _ := parseAWSChimeMessagingPlan(invocation.Body)
	region := strings.ToLower(strings.TrimSpace(invocation.Region))
	endpoint, err := getAWSChimeMessagingEndpoint(ctx, adapter, credentials, region)
	if err != nil {
		return InvocationResult{}, err
	}
	callerURL, _ := url.Parse(invocation.URL)
	if !strings.EqualFold(endpoint, callerURL.Hostname()) {
		return InvocationResult{}, fmt.Errorf("AWS Chime messaging session endpoint does not match the official caller URL")
	}
	signedURL, err := signAWSChimeMessagingConnectURL(invocation.URL, plan, credentials, region, adapter.config.Now().UTC())
	if err != nil {
		return InvocationResult{}, err
	}
	sessionCtx, cancel := context.WithTimeout(ctx, time.Duration(plan.TimeoutSeconds)*time.Second)
	defer cancel()
	connection, err := adapter.config.ChimeMessagingWebSocketDial(sessionCtx, signedURL)
	if err != nil {
		return InvocationResult{}, err
	}
	defer connection.Close()
	sink, err := newWebSocketOutputSink(invocation, adapter.config.MaxBodyBytes, "AWS Chime messaging WebSocket")
	if err != nil {
		return InvocationResult{}, err
	}
	defer sink.abort()
	received := 0
	for received < plan.MaxMessages {
		messageType, data, err := connection.Read(sessionCtx)
		if errors.Is(err, context.DeadlineExceeded) || err == io.EOF || websocket.CloseStatus(err) == websocket.StatusNormalClosure {
			break
		}
		if err != nil {
			return InvocationResult{}, err
		}
		event, err := sanitizeAWSChimeMessagingEvent(data, messageType)
		if err != nil {
			return InvocationResult{}, err
		}
		if err := sink.writeMessage(event); err != nil {
			return InvocationResult{}, err
		}
		received++
	}
	metadata, err := sink.finish("")
	if err != nil {
		return InvocationResult{}, err
	}
	var summary map[string]any
	if json.Unmarshal(metadata, &summary) != nil {
		return InvocationResult{}, fmt.Errorf("decode AWS Chime messaging output metadata")
	}
	summary["messages"] = received
	metadata, err = json.Marshal(summary)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("encode AWS Chime messaging output metadata")
	}
	return InvocationResult{Output: metadata}, nil
}

func getAWSChimeMessagingEndpoint(ctx context.Context, adapter *AWSRESTAdapter, credentials AWSCredentials, region string) (string, error) {
	if credentials.AccessKeyID == "" || credentials.SecretAccessKey == "" {
		return "", fmt.Errorf("AWS Chime messaging requires complete AKSK credentials")
	}
	endpoint := "https://messaging-chime." + strings.ToLower(strings.TrimSpace(region)) + ".amazonaws.com/endpoints/messaging-session"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("build AWS Chime messaging session-endpoint request")
	}
	request.Header.Set("Accept", "application/json")
	awsCredentials := aws.Credentials{AccessKeyID: credentials.AccessKeyID, SecretAccessKey: credentials.SecretAccessKey, SessionToken: credentials.SessionToken}
	if err := awsv4.NewSigner().SignHTTP(ctx, awsCredentials, request, sha256Hex(nil), awsChimeMessagingService, strings.ToLower(strings.TrimSpace(region)), adapter.config.Now().UTC()); err != nil {
		return "", fmt.Errorf("sign AWS Chime messaging session-endpoint request")
	}
	response, err := adapter.config.HTTP.Do(request)
	if err != nil {
		return "", fmt.Errorf("AWS Chime messaging session-endpoint request failed")
	}
	if response == nil || response.Body == nil {
		return "", fmt.Errorf("AWS Chime messaging session-endpoint request returned no response")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("AWS Chime messaging session-endpoint request failed with HTTP %d", response.StatusCode)
	}
	encoded, err := io.ReadAll(io.LimitReader(response.Body, awsChimeMessagingTokenResponseLimit+1))
	if err != nil || len(encoded) == 0 || len(encoded) > awsChimeMessagingTokenResponseLimit {
		return "", fmt.Errorf("AWS Chime messaging session-endpoint response was invalid")
	}
	var payload struct {
		Endpoint struct {
			URL string `json:"Url"`
		} `json:"Endpoint"`
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || ensureJSONDecoderEOF(decoder) != nil || payload.Endpoint.URL == "" {
		return "", fmt.Errorf("AWS Chime messaging session-endpoint response was invalid")
	}
	endpointURL, err := url.Parse(payload.Endpoint.URL)
	if err != nil || !strings.EqualFold(endpointURL.Scheme, "wss") || strings.ToLower(endpointURL.Hostname()) != awsChimeMessagingEndpointHost || endpointURL.Port() != "" || endpointURL.User != nil || endpointURL.Fragment != "" || endpointURL.RawQuery != "" || (endpointURL.Path != "" && endpointURL.Path != "/") {
		return "", fmt.Errorf("AWS Chime messaging returned an unexpected session endpoint")
	}
	return strings.ToLower(endpointURL.Hostname()), nil
}

func signAWSChimeMessagingConnectURL(rawURL string, plan awsChimeMessagingPlan, credentials AWSCredentials, region string, signingTime time.Time) (string, error) {
	if credentials.AccessKeyID == "" || credentials.SecretAccessKey == "" {
		return "", fmt.Errorf("AWS Chime messaging requires complete AKSK credentials")
	}
	target, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(target.Scheme, "wss") || target.RawQuery != "" || target.EscapedPath() != awsChimeMessagingEndpointPath {
		return "", fmt.Errorf("parse AWS Chime messaging connect URL")
	}
	now := signingTime.UTC()
	amzDate := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	region = strings.ToLower(strings.TrimSpace(region))
	scope := date + "/" + region + "/" + awsChimeMessagingService + "/aws4_request"
	query := make(url.Values)
	query.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	query.Set("X-Amz-Credential", credentials.AccessKeyID+"/"+scope)
	query.Set("X-Amz-Date", amzDate)
	query.Set("X-Amz-Expires", strconv.Itoa(plan.ConnectExpiresSeconds))
	query.Set("X-Amz-SignedHeaders", "host")
	if credentials.SessionToken != "" {
		query.Set("X-Amz-Security-Token", credentials.SessionToken)
	}
	query.Set("userArn", plan.UserARN)
	query.Set("sessionId", plan.SessionID)
	if plan.PrefetchOnConnect {
		query.Set("prefetch-on", "connect")
	}
	canonicalQuery := query.Encode()
	host := strings.ToLower(target.Host)
	canonicalRequest := strings.Join([]string{
		http.MethodGet,
		awsChimeMessagingEndpointPath,
		canonicalQuery,
		"host:" + host + "\n",
		"host",
		sha256Hex(nil),
	}, "\n")
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")
	signingKey := deriveAWSSigV4SigningKey(credentials.SecretAccessKey, region, awsChimeMessagingService, now)
	query.Set("X-Amz-Signature", hex.EncodeToString(hmacBytes(sha256.New, signingKey, []byte(stringToSign))))
	target.RawQuery = query.Encode()
	return target.String(), nil
}

func sanitizeAWSChimeMessagingEvent(data []byte, messageType cloudWebSocketMessageType) ([]byte, error) {
	if messageType != cloudWebSocketMessageText || len(data) == 0 || len(data) > maxRequestPayloadBytes || !utf8.Valid(data) {
		return nil, fmt.Errorf("AWS Chime messaging returned an invalid text event")
	}
	var envelope struct {
		Headers map[string]string `json:"Headers"`
		Payload *json.RawMessage  `json:"Payload"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&envelope) != nil || ensureJSONDecoderEOF(decoder) != nil || envelope.Headers == nil {
		return nil, fmt.Errorf("AWS Chime messaging returned a malformed event")
	}
	if len(envelope.Headers) > 32 {
		return nil, fmt.Errorf("AWS Chime messaging event headers exceed the bounded count")
	}
	for name, value := range envelope.Headers {
		if !awsChimeMessagingBoundedHeader(name, value) {
			return nil, fmt.Errorf("AWS Chime messaging event header %q is invalid", name)
		}
	}
	eventType := strings.TrimSpace(envelope.Headers["x-amz-chime-event-type"])
	if _, ok := awsChimeMessagingEventTypes[eventType]; !ok {
		return nil, fmt.Errorf("AWS Chime messaging returned an unsupported event type")
	}
	if rawMessageType, exists := envelope.Headers["x-amz-chime-message-type"]; exists {
		if _, ok := awsChimeMessagingMessageTypes[rawMessageType]; !ok {
			return nil, fmt.Errorf("AWS Chime messaging returned an unsupported message type")
		}
	}
	output := map[string]any{"Headers": envelope.Headers}
	if envelope.Payload == nil {
		output["Payload"] = nil
		return json.Marshal(output)
	}
	var rawPayload string
	if json.Unmarshal(*envelope.Payload, &rawPayload) != nil || len(rawPayload) > maxRequestPayloadBytes {
		return nil, fmt.Errorf("AWS Chime messaging returned an invalid event payload")
	}
	if strings.TrimSpace(rawPayload) == "" {
		output["Payload"] = nil
	} else {
		var payload any
		payloadDecoder := json.NewDecoder(strings.NewReader(rawPayload))
		payloadDecoder.UseNumber()
		if payloadDecoder.Decode(&payload) != nil || ensureJSONDecoderEOF(payloadDecoder) != nil {
			return nil, fmt.Errorf("AWS Chime messaging returned an invalid event payload")
		}
		output["Payload"] = payload
	}
	canonical, err := json.Marshal(output)
	if err != nil {
		return nil, fmt.Errorf("encode AWS Chime messaging event")
	}
	return canonical, nil
}

func awsChimeMessagingBoundedHeader(name, value string) bool {
	if name == "" || len(name) > 128 || strings.TrimSpace(name) != name || strings.ContainsAny(name, "\x00\r\n") {
		return false
	}
	if len(value) > 8192 || strings.ContainsAny(value, "\x00\r\n") {
		return false
	}
	return true
}

func defaultAWSChimeMessagingWebSocketDial(ctx context.Context, signedURL string) (cloudWebSocketConnection, error) {
	client := &http.Client{
		Transport:     http.DefaultTransport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	connection, response, err := websocket.Dial(ctx, signedURL, &websocket.DialOptions{
		HTTPClient: client, CompressionMode: websocket.CompressionDisabled,
	})
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("AWS Chime messaging WebSocket handshake failed with HTTP %d", response.StatusCode)
		}
		return nil, fmt.Errorf("AWS Chime messaging WebSocket handshake failed")
	}
	connection.SetReadLimit(maxRequestPayloadBytes)
	return coderTencentWebSocketConnection{connection: connection}, nil
}

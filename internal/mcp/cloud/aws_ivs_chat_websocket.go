package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	aws "github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/coder/websocket"
)

const (
	authSchemeAWSIVSChatWS       = "ivs-chat-ws"
	awsIVSChatService            = "ivschat"
	awsIVSChatSubscribeOperation = "subscribechat"
	awsIVSChatClientOperation    = "clientchat"
	awsIVSChatModerateOperation  = "moderatechat"
	awsIVSChatMaxMessages        = 256
	awsIVSChatMaxActions         = 64
	awsIVSChatMaxTimeoutSeconds  = 300
	awsIVSChatTokenResponseLimit = 32 * 1024
	awsIVSChatAttributeLimit     = 1024
)

var awsIVSChatRegions = map[string]struct{}{
	"us-east-1": {}, "us-west-2": {}, "ap-south-1": {}, "ap-northeast-2": {},
	"ap-northeast-1": {}, "eu-central-1": {}, "eu-west-1": {},
}

type awsIVSChatAction struct {
	Action     string            `json:"action"`
	Attributes map[string]string `json:"attributes,omitempty"`
	Content    string            `json:"content,omitempty"`
	RequestID  string            `json:"request_id,omitempty"`
	ID         string            `json:"id,omitempty"`
	Reason     string            `json:"reason,omitempty"`
	UserID     string            `json:"user_id,omitempty"`
}

type awsIVSChatRawPlan struct {
	RoomIdentifier           string             `json:"room_identifier"`
	UserID                   string             `json:"user_id"`
	Attributes               map[string]string  `json:"attributes,omitempty"`
	SessionDurationInMinutes int                `json:"session_duration_minutes,omitempty"`
	Messages                 []awsIVSChatAction `json:"messages,omitempty"`
	MaxMessages              int                `json:"max_messages"`
	TimeoutSeconds           int                `json:"timeout_seconds"`
}

type awsIVSChatPlan struct {
	RoomIdentifier           string
	UserID                   string
	Attributes               map[string]string
	SessionDurationInMinutes int
	Actions                  []map[string]any
	Capabilities             []string
	MaxMessages              int
	Timeout                  time.Duration
}

type awsIVSChatTokenResponse struct {
	Token                 string `json:"token"`
	TokenExpirationTime   string `json:"tokenExpirationTime"`
	SessionExpirationTime string `json:"sessionExpirationTime"`
}

type awsIVSChatTokenRequest struct {
	Attributes               map[string]string `json:"attributes,omitempty"`
	Capabilities             []string          `json:"capabilities,omitempty"`
	RoomIdentifier           string            `json:"roomIdentifier"`
	SessionDurationInMinutes int               `json:"sessionDurationInMinutes"`
	UserID                   string            `json:"userId"`
}

func validateAWSIVSChatInvocation(invocation Invocation) error {
	operation := normalizedOperation(invocation.Operation)
	if !strings.EqualFold(invocation.Service, awsIVSChatService) || operation != awsIVSChatSubscribeOperation && operation != awsIVSChatClientOperation && operation != awsIVSChatModerateOperation {
		return fmt.Errorf("AWS IVS Chat WebSocket requires service ivschat and operation SubscribeChat, ClientChat, or ModerateChat")
	}
	if !strings.EqualFold(invocation.Method, http.MethodGet) {
		return fmt.Errorf("AWS IVS Chat WebSocket requires GET")
	}
	if err := validateAWSIVSChatEndpoint(invocation.URL, invocation.Region); err != nil {
		return err
	}
	if len(invocation.Parameters) != 0 || len(invocation.Headers) != 0 || invocation.BodyFile != "" || invocation.ResponseFile == "" {
		return fmt.Errorf("AWS IVS Chat WebSocket requires only a protocol body and response_file; query, headers, and body_file are forbidden")
	}
	if invocation.RegionSet != "" || invocation.Project != "" || invocation.Subscription != "" || invocation.Audience != "" || invocation.AuthVersion != "" || invocation.APIVersion != "" || invocation.PayloadMode != "" || invocation.ChecksumAlgorithm != "" || invocation.StreamChunkBytes != 0 || invocation.StreamIntervalMS != 0 || invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return fmt.Errorf("AWS IVS Chat WebSocket does not accept cross-provider, REST payload, or generic stream controls")
	}
	_, err := parseAWSIVSChatPlan(invocation.Operation, invocation.Region, invocation.Body)
	return err
}

func validateAWSIVSChatEndpoint(rawURL, region string) error {
	region = strings.ToLower(strings.TrimSpace(region))
	if _, ok := awsIVSChatRegions[region]; !ok {
		return fmt.Errorf("AWS IVS Chat WebSocket region is not in the current official endpoint table")
	}
	target, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(target.Scheme, "wss") || target.Hostname() == "" || target.Port() != "" || target.User != nil || target.Fragment != "" || target.RawQuery != "" || target.EscapedPath() != "" && target.EscapedPath() != "/" {
		return fmt.Errorf("AWS IVS Chat WebSocket requires an exact root wss:// endpoint with no caller query")
	}
	if strings.ToLower(target.Hostname()) != "edge.ivschat."+region+".amazonaws.com" {
		return fmt.Errorf("AWS IVS Chat WebSocket host does not match the requested official region endpoint")
	}
	return nil
}

func parseAWSIVSChatPlan(operation, region string, body any) (awsIVSChatPlan, error) {
	if body == nil || alibabaNLSContainsCredentialField(body) {
		return awsIVSChatPlan{}, fmt.Errorf("AWS IVS Chat requires a credential-free protocol body")
	}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) == 0 || len(encoded) > maxRequestPayloadBytes {
		return awsIVSChatPlan{}, fmt.Errorf("AWS IVS Chat body must be bounded JSON")
	}
	var raw awsIVSChatRawPlan
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&raw) != nil || ensureJSONDecoderEOF(decoder) != nil {
		return awsIVSChatPlan{}, fmt.Errorf("AWS IVS Chat body does not match the protocol schema")
	}
	region = strings.ToLower(strings.TrimSpace(region))
	wantPrefix := "arn:aws:ivschat:" + region + ":"
	if len(raw.RoomIdentifier) < 1 || len(raw.RoomIdentifier) > 128 || !strings.HasPrefix(raw.RoomIdentifier, wantPrefix) {
		return awsIVSChatPlan{}, fmt.Errorf("AWS IVS Chat room_identifier must be a room ARN in the requested region")
	}
	parts := strings.Split(raw.RoomIdentifier, ":")
	if len(parts) != 6 || parts[4] == "" || !allASCIIDigits(parts[4]) || !strings.HasPrefix(parts[5], "room/") || !validAWSIVSChatRoomID(strings.TrimPrefix(parts[5], "room/")) {
		return awsIVSChatPlan{}, fmt.Errorf("AWS IVS Chat room_identifier does not match the documented ARN format")
	}
	if err := validateAWSIVSChatText("user_id", raw.UserID, 128, true); err != nil {
		return awsIVSChatPlan{}, err
	}
	if err := validateAWSIVSChatAttributes(raw.Attributes); err != nil {
		return awsIVSChatPlan{}, err
	}
	if raw.SessionDurationInMinutes == 0 {
		raw.SessionDurationInMinutes = 60
	}
	if raw.SessionDurationInMinutes < 1 || raw.SessionDurationInMinutes > 180 {
		return awsIVSChatPlan{}, fmt.Errorf("AWS IVS Chat session_duration_minutes must be between 1 and 180")
	}
	if raw.MaxMessages < 1 || raw.MaxMessages > awsIVSChatMaxMessages || raw.TimeoutSeconds < 1 || raw.TimeoutSeconds > awsIVSChatMaxTimeoutSeconds {
		return awsIVSChatPlan{}, fmt.Errorf("AWS IVS Chat requires max_messages 1..%d and timeout_seconds 1..%d", awsIVSChatMaxMessages, awsIVSChatMaxTimeoutSeconds)
	}
	normalized := normalizedOperation(operation)
	if normalized == awsIVSChatSubscribeOperation && len(raw.Messages) != 0 {
		return awsIVSChatPlan{}, fmt.Errorf("AWS IVS Chat SubscribeChat does not accept publish actions")
	}
	if normalized != awsIVSChatSubscribeOperation && (len(raw.Messages) < 1 || len(raw.Messages) > awsIVSChatMaxActions) {
		return awsIVSChatPlan{}, fmt.Errorf("AWS IVS Chat mutation operations require 1..%d actions", awsIVSChatMaxActions)
	}
	capabilitySet := map[string]struct{}{}
	actions := make([]map[string]any, 0, len(raw.Messages))
	for _, action := range raw.Messages {
		providerAction, capability, err := normalizeAWSIVSChatAction(normalized, action)
		if err != nil {
			return awsIVSChatPlan{}, err
		}
		actions = append(actions, providerAction)
		capabilitySet[capability] = struct{}{}
	}
	capabilities := make([]string, 0, len(capabilitySet))
	for capability := range capabilitySet {
		capabilities = append(capabilities, capability)
	}
	sort.Strings(capabilities)
	return awsIVSChatPlan{
		RoomIdentifier: raw.RoomIdentifier, UserID: raw.UserID, Attributes: raw.Attributes,
		SessionDurationInMinutes: raw.SessionDurationInMinutes, Actions: actions, Capabilities: capabilities,
		MaxMessages: raw.MaxMessages, Timeout: time.Duration(raw.TimeoutSeconds) * time.Second,
	}, nil
}

func normalizeAWSIVSChatAction(operation string, raw awsIVSChatAction) (map[string]any, string, error) {
	action := strings.ToUpper(strings.TrimSpace(raw.Action))
	if err := validateAWSIVSChatText("request_id", raw.RequestID, 128, false); err != nil {
		return nil, "", err
	}
	base := map[string]any{"Action": action}
	if raw.RequestID != "" {
		base["RequestId"] = raw.RequestID
	}
	switch action {
	case "SEND_MESSAGE":
		if operation != awsIVSChatClientOperation || raw.ID != "" || raw.Reason != "" || raw.UserID != "" {
			return nil, "", fmt.Errorf("AWS IVS Chat SEND_MESSAGE is allowed only by ClientChat with send-message fields")
		}
		if err := validateAWSIVSChatText("content", raw.Content, 500, true); err != nil {
			return nil, "", err
		}
		if err := validateAWSIVSChatAttributes(raw.Attributes); err != nil {
			return nil, "", err
		}
		base["Content"] = raw.Content
		if len(raw.Attributes) != 0 {
			base["Attributes"] = raw.Attributes
		}
		return base, action, nil
	case "DELETE_MESSAGE":
		if operation != awsIVSChatModerateOperation || raw.Content != "" || raw.UserID != "" || len(raw.Attributes) != 0 {
			return nil, "", fmt.Errorf("AWS IVS Chat DELETE_MESSAGE is allowed only by ModerateChat with moderation fields")
		}
		if err := validateAWSIVSChatText("id", raw.ID, 128, true); err != nil {
			return nil, "", err
		}
		if err := validateAWSIVSChatText("reason", raw.Reason, 500, false); err != nil {
			return nil, "", err
		}
		base["Id"] = raw.ID
		if raw.Reason != "" {
			base["Reason"] = raw.Reason
		}
		return base, action, nil
	case "DISCONNECT_USER":
		if operation != awsIVSChatModerateOperation || raw.Content != "" || raw.ID != "" || len(raw.Attributes) != 0 {
			return nil, "", fmt.Errorf("AWS IVS Chat DISCONNECT_USER is allowed only by ModerateChat with moderation fields")
		}
		if err := validateAWSIVSChatText("user_id", raw.UserID, 128, true); err != nil {
			return nil, "", err
		}
		if err := validateAWSIVSChatText("reason", raw.Reason, 500, false); err != nil {
			return nil, "", err
		}
		base["UserId"] = raw.UserID
		if raw.Reason != "" {
			base["Reason"] = raw.Reason
		}
		return base, action, nil
	default:
		return nil, "", fmt.Errorf("AWS IVS Chat action must be SEND_MESSAGE, DELETE_MESSAGE, or DISCONNECT_USER")
	}
}

func allASCIIDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func validAWSIVSChatRoomID(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func validateAWSIVSChatText(name, value string, maxRunes int, required bool) error {
	if required && value == "" {
		return fmt.Errorf("AWS IVS Chat %s is required", name)
	}
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxRunes {
		return fmt.Errorf("AWS IVS Chat %s exceeds its documented bound", name)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("AWS IVS Chat %s contains control characters", name)
		}
	}
	return nil
}

func validateAWSIVSChatAttributes(attributes map[string]string) error {
	if len(attributes) == 0 {
		return nil
	}
	if alibabaNLSContainsCredentialField(attributes) {
		return fmt.Errorf("AWS IVS Chat attributes contain a credential-like field")
	}
	encoded, err := json.Marshal(attributes)
	if err != nil || len(encoded) > awsIVSChatAttributeLimit {
		return fmt.Errorf("AWS IVS Chat attributes exceed the documented 1 KB bound")
	}
	for key, value := range attributes {
		if err := validateAWSIVSChatText("attribute key", key, 128, true); err != nil {
			return err
		}
		if err := validateAWSIVSChatText("attribute value", value, 500, false); err != nil {
			return err
		}
	}
	return nil
}

func createAWSIVSChatToken(ctx context.Context, adapter *AWSRESTAdapter, credentials AWSCredentials, region string, plan awsIVSChatPlan) (string, string, error) {
	body, err := json.Marshal(awsIVSChatTokenRequest{
		Attributes: plan.Attributes, Capabilities: plan.Capabilities, RoomIdentifier: plan.RoomIdentifier,
		SessionDurationInMinutes: plan.SessionDurationInMinutes, UserID: plan.UserID,
	})
	if err != nil {
		return "", "", fmt.Errorf("encode AWS IVS Chat token request")
	}
	endpoint := "https://ivschat." + strings.ToLower(strings.TrimSpace(region)) + ".amazonaws.com/CreateChatToken"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", "", fmt.Errorf("build AWS IVS Chat token request")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	awsCredentials := aws.Credentials{AccessKeyID: credentials.AccessKeyID, SecretAccessKey: credentials.SecretAccessKey, SessionToken: credentials.SessionToken}
	if err := awsv4.NewSigner().SignHTTP(ctx, awsCredentials, request, sha256Hex(body), awsIVSChatService, strings.ToLower(strings.TrimSpace(region)), adapter.config.Now().UTC()); err != nil {
		return "", "", fmt.Errorf("sign AWS IVS Chat token request")
	}
	response, err := adapter.config.HTTP.Do(request)
	if err != nil {
		return "", "", fmt.Errorf("AWS IVS Chat token request failed")
	}
	if response == nil || response.Body == nil {
		return "", "", fmt.Errorf("AWS IVS Chat token request returned no response")
	}
	defer response.Body.Close()
	requestID := strings.TrimSpace(response.Header.Get("X-Amzn-Requestid"))
	if response.StatusCode != http.StatusOK {
		return "", requestID, fmt.Errorf("AWS IVS Chat token request failed with HTTP %d", response.StatusCode)
	}
	encoded, err := io.ReadAll(io.LimitReader(response.Body, awsIVSChatTokenResponseLimit+1))
	if err != nil || len(encoded) == 0 || len(encoded) > awsIVSChatTokenResponseLimit {
		return "", requestID, fmt.Errorf("AWS IVS Chat token response was invalid")
	}
	var tokenResponse awsIVSChatTokenResponse
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&tokenResponse) != nil || ensureJSONDecoderEOF(decoder) != nil || tokenResponse.Token == "" || len(tokenResponse.Token) > 16*1024 {
		return "", requestID, fmt.Errorf("AWS IVS Chat token response was invalid")
	}
	now := adapter.config.Now().UTC()
	tokenExpiry, tokenErr := time.Parse(time.RFC3339, tokenResponse.TokenExpirationTime)
	sessionExpiry, sessionErr := time.Parse(time.RFC3339, tokenResponse.SessionExpirationTime)
	if tokenErr != nil || sessionErr != nil || !tokenExpiry.After(now) || !sessionExpiry.After(now) {
		return "", requestID, fmt.Errorf("AWS IVS Chat token response contained invalid expiration times")
	}
	return tokenResponse.Token, requestID, nil
}

func invokeAWSIVSChatWebSocket(ctx context.Context, adapter *AWSRESTAdapter, credentials AWSCredentials, invocation Invocation) (InvocationResult, error) {
	if err := validateAWSIVSChatInvocation(invocation); err != nil {
		return InvocationResult{}, err
	}
	plan, _ := parseAWSIVSChatPlan(invocation.Operation, invocation.Region, invocation.Body)
	handshakeCtx, cancelHandshake := context.WithTimeout(ctx, 30*time.Second)
	token, tokenRequestID, err := createAWSIVSChatToken(handshakeCtx, adapter, credentials, invocation.Region, plan)
	if err != nil {
		cancelHandshake()
		return InvocationResult{}, err
	}
	connection, err := adapter.config.IVSChatWebSocketDial(handshakeCtx, invocation.URL, []string{token})
	cancelHandshake()
	if err != nil {
		return InvocationResult{}, fmt.Errorf("AWS IVS Chat WebSocket handshake failed")
	}
	defer connection.Close()
	sink, err := newWebSocketOutputSink(invocation, adapter.config.MaxBodyBytes, "AWS IVS Chat WebSocket")
	if err != nil {
		return InvocationResult{}, err
	}
	defer sink.abort()
	for index, action := range plan.Actions {
		encoded, marshalErr := json.Marshal(action)
		if marshalErr != nil {
			return InvocationResult{}, fmt.Errorf("encode AWS IVS Chat action")
		}
		writeCtx, cancelWrite := context.WithTimeout(ctx, 10*time.Second)
		writeErr := connection.Write(writeCtx, cloudWebSocketMessageText, encoded)
		cancelWrite()
		if writeErr != nil {
			return InvocationResult{}, fmt.Errorf("send AWS IVS Chat action")
		}
		if index+1 < len(plan.Actions) {
			if err := adapter.config.StreamPause(ctx, 100*time.Millisecond); err != nil {
				return InvocationResult{}, fmt.Errorf("pace AWS IVS Chat actions")
			}
		}
	}
	collectionCtx, cancelCollection := context.WithTimeout(ctx, plan.Timeout)
	defer cancelCollection()
	received := 0
	lastRequestID := tokenRequestID
	for received < plan.MaxMessages {
		messageType, data, readErr := connection.Read(collectionCtx)
		if errors.Is(readErr, context.DeadlineExceeded) {
			break
		}
		if readErr != nil {
			return InvocationResult{}, fmt.Errorf("read AWS IVS Chat message")
		}
		if messageType != cloudWebSocketMessageText || len(data) == 0 || int64(len(data)) > adapter.config.MaxBodyBytes {
			return InvocationResult{}, fmt.Errorf("AWS IVS Chat returned an invalid text frame")
		}
		sanitized, requestID, parseErr := sanitizeAWSIVSChatFrame(data)
		if parseErr != nil {
			return InvocationResult{}, parseErr
		}
		if requestID != "" {
			lastRequestID = requestID
		}
		if err := sink.writeMessage(sanitized); err != nil {
			return InvocationResult{}, err
		}
		received++
	}
	output, err := sink.finish(lastRequestID)
	if err != nil {
		return InvocationResult{}, err
	}
	return InvocationResult{Output: output, RequestID: lastRequestID}, nil
}

func sanitizeAWSIVSChatFrame(data []byte) ([]byte, string, error) {
	var object map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if decoder.Decode(&object) != nil || ensureJSONDecoderEOF(decoder) != nil || alibabaNLSContainsCredentialField(object) {
		return nil, "", fmt.Errorf("AWS IVS Chat returned invalid or credential-bearing JSON")
	}
	typeValue, _ := object["Type"].(string)
	requestID, _ := object["RequestId"].(string)
	if err := validateAWSIVSChatText("provider request ID", requestID, 128, false); err != nil {
		return nil, "", fmt.Errorf("AWS IVS Chat returned an invalid request ID")
	}
	if typeValue == "ERROR" {
		code, ok := object["ErrorCode"].(json.Number)
		if !ok {
			return nil, requestID, fmt.Errorf("AWS IVS Chat returned an invalid error frame")
		}
		return nil, requestID, fmt.Errorf("AWS IVS Chat operation failed with code %s", code.String())
	}
	if typeValue != "MESSAGE" && typeValue != "EVENT" {
		return nil, requestID, fmt.Errorf("AWS IVS Chat returned an unexpected message type")
	}
	identifier, _ := object["Id"].(string)
	sendTime, _ := object["SendTime"].(string)
	if validateAWSIVSChatText("provider message ID", identifier, 128, true) != nil {
		return nil, requestID, fmt.Errorf("AWS IVS Chat returned an invalid message ID")
	}
	if _, err := time.Parse(time.RFC3339, sendTime); err != nil {
		return nil, requestID, fmt.Errorf("AWS IVS Chat returned an invalid send time")
	}
	sanitized, err := json.Marshal(object)
	if err != nil {
		return nil, requestID, fmt.Errorf("encode AWS IVS Chat message")
	}
	return sanitized, requestID, nil
}

func defaultAWSIVSChatWebSocketDial(ctx context.Context, rawURL string, subprotocols []string) (cloudWebSocketConnection, error) {
	if len(subprotocols) != 1 || subprotocols[0] == "" {
		return nil, fmt.Errorf("AWS IVS Chat WebSocket requires one internal token subprotocol")
	}
	client := &http.Client{Transport: http.DefaultTransport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	connection, response, err := websocket.Dial(ctx, rawURL, &websocket.DialOptions{HTTPClient: client, CompressionMode: websocket.CompressionDisabled, Subprotocols: subprotocols})
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("AWS IVS Chat WebSocket handshake failed with HTTP %d", response.StatusCode)
		}
		return nil, fmt.Errorf("AWS IVS Chat WebSocket handshake failed")
	}
	if connection.Subprotocol() != subprotocols[0] {
		connection.Close(websocket.StatusPolicyViolation, "")
		return nil, fmt.Errorf("AWS IVS Chat WebSocket did not negotiate its token subprotocol")
	}
	connection.SetReadLimit(maxRequestPayloadBytes)
	return coderTencentWebSocketConnection{connection: connection}, nil
}

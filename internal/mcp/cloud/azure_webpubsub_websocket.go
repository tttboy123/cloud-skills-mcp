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
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
)

const (
	authSchemeAzureWebPubSubWS      = "webpubsub-ws"
	azureWebPubSubAPIVersion        = "2024-01-01"
	azureWebPubSubMaxMessages       = 256
	azureWebPubSubMaxTimeoutSeconds = 300
)

var azureWebPubSubHubPattern = regexp.MustCompile("^[A-Za-z][A-Za-z0-9_`,.\\[\\]]{0,127}$")

type azureWebPubSubWebSocketDial func(context.Context, string, http.Header) (cloudWebSocketConnection, error)

type azureWebPubSubPlan struct {
	UserID         string            `json:"user_id,omitempty"`
	Roles          []string          `json:"roles,omitempty"`
	Groups         []string          `json:"groups,omitempty"`
	Messages       []json.RawMessage `json:"messages"`
	MaxMessages    int               `json:"max_messages"`
	TimeoutSeconds int               `json:"timeout_seconds"`

	encodedMessages [][]byte
}

func validateAzureWebPubSubInvocation(invocation Invocation) error {
	if !strings.EqualFold(invocation.Method, http.MethodGet) || !strings.EqualFold(invocation.Service, "webpubsub") || !strings.EqualFold(invocation.Operation, "ClientConnect") {
		return fmt.Errorf("Azure Web PubSub requires GET, service webpubsub, and operation ClientConnect")
	}
	if _, _, err := parseAzureWebPubSubTarget(invocation.URL); err != nil {
		return err
	}
	if len(invocation.Headers) != 0 || len(invocation.Parameters) != 0 {
		return fmt.Errorf("Azure Web PubSub does not accept caller headers or query parameters")
	}
	if invocation.Body == nil || invocation.BodyFile != "" || invocation.ResponseFile == "" {
		return fmt.Errorf("Azure Web PubSub requires a finite protocol body and response_file; body_file is forbidden")
	}
	if invocation.Region != "" || invocation.Project != "" || invocation.Subscription != "" || invocation.Audience != "" || invocation.APIVersion != "" || invocation.AuthVersion != "" || invocation.PayloadMode != "" || invocation.ChecksumAlgorithm != "" || invocation.StreamChunkBytes != 0 || invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return fmt.Errorf("Azure Web PubSub does not accept REST, cross-provider, checksum, or binary stream controls")
	}
	_, err := parseAzureWebPubSubPlan(invocation.Body)
	return err
}

func parseAzureWebPubSubTarget(rawURL string) (*url.URL, string, error) {
	target, err := url.Parse(rawURL)
	if err != nil || target.Scheme != "wss" || target.User != nil || target.Fragment != "" || target.RawQuery != "" || target.Port() != "" {
		return nil, "", fmt.Errorf("Azure Web PubSub requires an exact credential-free wss:// URL")
	}
	host := strings.ToLower(target.Hostname())
	const suffix = ".webpubsub.azure.com"
	resource := strings.TrimSuffix(host, suffix)
	if resource == host || !endpointLabelPattern.MatchString(resource) || len(resource) < 3 || len(resource) > 63 {
		return nil, "", fmt.Errorf("Azure Web PubSub requires a resource.webpubsub.azure.com host")
	}
	const prefix = "/client/hubs/"
	if !strings.HasPrefix(target.Path, prefix) {
		return nil, "", fmt.Errorf("Azure Web PubSub requires the official /client/hubs/{hub} path")
	}
	hub := strings.TrimPrefix(target.Path, prefix)
	if !azureWebPubSubHubPattern.MatchString(hub) {
		return nil, "", fmt.Errorf("Azure Web PubSub requires one valid hub path segment")
	}
	return target, hub, nil
}

func parseAzureWebPubSubPlan(body any) (azureWebPubSubPlan, error) {
	if body == nil || azureRealtimeContainsCredentialField(body) {
		return azureWebPubSubPlan{}, fmt.Errorf("Azure Web PubSub requires a credential-free protocol body")
	}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) == 0 || len(encoded) > maxRequestPayloadBytes {
		return azureWebPubSubPlan{}, fmt.Errorf("Azure Web PubSub body must be bounded JSON")
	}
	var plan azureWebPubSubPlan
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&plan) != nil || ensureJSONDecoderEOF(decoder) != nil {
		return azureWebPubSubPlan{}, fmt.Errorf("Azure Web PubSub body does not match the finite session schema")
	}
	if plan.UserID != "" && !azureWebPubSubBoundedValue(plan.UserID, 128) {
		return azureWebPubSubPlan{}, fmt.Errorf("Azure Web PubSub user_id is invalid")
	}
	if len(plan.Roles) > 32 || len(plan.Groups) > 32 {
		return azureWebPubSubPlan{}, fmt.Errorf("Azure Web PubSub token claims exceed the bounded count")
	}
	for _, role := range plan.Roles {
		if !azureWebPubSubRole(role) {
			return azureWebPubSubPlan{}, fmt.Errorf("Azure Web PubSub role is not a documented client permission")
		}
	}
	for _, group := range plan.Groups {
		if !azureWebPubSubBoundedValue(group, 256) {
			return azureWebPubSubPlan{}, fmt.Errorf("Azure Web PubSub group claim is invalid")
		}
	}
	if len(plan.Messages) < 1 || len(plan.Messages) > azureWebPubSubMaxMessages {
		return azureWebPubSubPlan{}, fmt.Errorf("Azure Web PubSub messages must contain 1 to %d frames", azureWebPubSubMaxMessages)
	}
	if plan.MaxMessages < 1 || plan.MaxMessages > azureWebPubSubMaxMessages || plan.TimeoutSeconds < 1 || plan.TimeoutSeconds > azureWebPubSubMaxTimeoutSeconds {
		return azureWebPubSubPlan{}, fmt.Errorf("Azure Web PubSub response count or timeout is outside the finite bound")
	}
	plan.encodedMessages = make([][]byte, len(plan.Messages))
	ackIDs := make(map[int64]bool)
	for index, raw := range plan.Messages {
		canonical, ackID, err := validateAzureWebPubSubClientMessage(raw)
		if err != nil {
			return azureWebPubSubPlan{}, fmt.Errorf("Azure Web PubSub message %d: %w", index, err)
		}
		if ackID != 0 {
			if ackIDs[ackID] {
				return azureWebPubSubPlan{}, fmt.Errorf("Azure Web PubSub ackId values must be unique")
			}
			ackIDs[ackID] = true
		}
		plan.encodedMessages[index] = canonical
	}
	return plan, nil
}

func validateAzureWebPubSubClientMessage(raw json.RawMessage) ([]byte, int64, error) {
	if len(raw) == 0 || len(raw) > maxRequestPayloadBytes || !json.Valid(raw) {
		return nil, 0, fmt.Errorf("client message must be bounded JSON")
	}
	var message map[string]any
	if json.Unmarshal(raw, &message) != nil || azureRealtimeContainsCredentialField(message) {
		return nil, 0, fmt.Errorf("client message is invalid or contains credentials")
	}
	kind, ok := message["type"].(string)
	if !ok {
		return nil, 0, fmt.Errorf("client message requires type")
	}
	switch kind {
	case "joinGroup", "leaveGroup", "sendToGroup":
		group, ok := message["group"].(string)
		if !ok || !azureWebPubSubBoundedValue(group, 256) {
			return nil, 0, fmt.Errorf("group message requires a bounded group")
		}
	case "event":
		event, ok := message["event"].(string)
		if !ok || !azureWebPubSubBoundedValue(event, 128) {
			return nil, 0, fmt.Errorf("event message requires a bounded event")
		}
	case "ping":
	default:
		return nil, 0, fmt.Errorf("unsupported client message type")
	}
	var ackID int64
	if rawAck, exists := message["ackId"]; exists {
		value, ok := rawAck.(float64)
		if !ok || value < 1 || value > 9007199254740991 || value != float64(int64(value)) {
			return nil, 0, fmt.Errorf("ackId must be a positive unique integer")
		}
		ackID = int64(value)
	}
	canonical, err := json.Marshal(message)
	if err != nil {
		return nil, 0, fmt.Errorf("encode client message")
	}
	return canonical, ackID, nil
}

func azureWebPubSubBoundedValue(value string, maximum int) bool {
	if value == "" || len(value) > maximum || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character == 0 || character == '\r' || character == '\n' {
			return false
		}
	}
	return true
}

func azureWebPubSubRole(role string) bool {
	if !azureWebPubSubBoundedValue(role, 512) {
		return false
	}
	for _, base := range []string{"webpubsub.joinLeaveGroup", "webpubsub.sendToGroup"} {
		if role == base || strings.HasPrefix(role, base+".") && azureWebPubSubBoundedValue(strings.TrimPrefix(role, base+"."), 256) {
			return true
		}
	}
	for _, base := range []string{"webpubsub.joinLeaveGroups.", "webpubsub.sendToGroups."} {
		if strings.HasPrefix(role, base) && azureWebPubSubBoundedValue(strings.TrimPrefix(role, base), 256) {
			return true
		}
	}
	return false
}

func invokeAzureWebPubSub(ctx context.Context, adapter *AzureRESTAdapter, invocation Invocation) (InvocationResult, error) {
	if err := validateAzureWebPubSubInvocation(invocation); err != nil {
		return InvocationResult{}, err
	}
	target, hub, _ := parseAzureWebPubSubTarget(invocation.URL)
	plan, _ := parseAzureWebPubSubPlan(invocation.Body)
	identityToken, err := adapter.config.Tokens.Token(ctx, "https://webpubsub.azure.com/.default")
	if err != nil {
		return InvocationResult{}, fmt.Errorf("load Azure Web PubSub identity token: %w", err)
	}
	if strings.TrimSpace(identityToken) == "" {
		return InvocationResult{}, fmt.Errorf("Azure identity returned an empty access token")
	}
	clientToken, err := mintAzureWebPubSubClientToken(ctx, adapter, target, hub, plan, identityToken)
	if err != nil {
		return InvocationResult{}, err
	}
	sessionCtx, cancel := context.WithTimeout(ctx, time.Duration(plan.TimeoutSeconds)*time.Second)
	defer cancel()
	connection, err := adapter.config.WebPubSubWebSocketDial(sessionCtx, invocation.URL, http.Header{"Authorization": []string{"Bearer " + clientToken}})
	if err != nil {
		return InvocationResult{}, err
	}
	defer connection.Close()
	sink, err := newWebSocketOutputSink(invocation, adapter.config.MaxBodyBytes, "Azure Web PubSub WebSocket")
	if err != nil {
		return InvocationResult{}, err
	}
	defer sink.abort()
	for index, message := range plan.encodedMessages {
		if index > 0 && invocation.StreamIntervalMS > 0 {
			if err := adapter.config.StreamPause(sessionCtx, time.Duration(invocation.StreamIntervalMS)*time.Millisecond); err != nil {
				return InvocationResult{}, fmt.Errorf("pace Azure Web PubSub client messages")
			}
		}
		if err := connection.Write(sessionCtx, cloudWebSocketMessageText, message); err != nil {
			return InvocationResult{}, fmt.Errorf("send Azure Web PubSub client message")
		}
	}
	received := 0
	for received < plan.MaxMessages {
		message, terminal, err := readAzureWebPubSubServerMessage(sessionCtx, connection)
		if errors.Is(err, context.DeadlineExceeded) || err == io.EOF || websocket.CloseStatus(err) == websocket.StatusNormalClosure {
			break
		}
		if err != nil {
			return InvocationResult{}, err
		}
		if err := sink.writeMessage(message); err != nil {
			return InvocationResult{}, err
		}
		received++
		if terminal {
			break
		}
	}
	metadata, err := sink.finish("")
	if err != nil {
		return InvocationResult{}, err
	}
	var summary map[string]any
	if json.Unmarshal(metadata, &summary) != nil {
		return InvocationResult{}, fmt.Errorf("decode Azure Web PubSub output metadata")
	}
	summary["messages"] = received
	metadata, err = json.Marshal(summary)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("encode Azure Web PubSub output metadata")
	}
	return InvocationResult{Output: metadata}, nil
}

func mintAzureWebPubSubClientToken(ctx context.Context, adapter *AzureRESTAdapter, target *url.URL, hub string, plan azureWebPubSubPlan, identityToken string) (string, error) {
	tokenURL := &url.URL{Scheme: "https", Host: target.Host, Path: "/api/hubs/" + hub + "/:generateToken"}
	query := tokenURL.Query()
	query.Set("api-version", azureWebPubSubAPIVersion)
	query.Set("minutesToExpire", "5")
	if plan.UserID != "" {
		query.Set("userId", plan.UserID)
	}
	for _, role := range plan.Roles {
		query.Add("role", role)
	}
	for _, group := range plan.Groups {
		query.Add("group", group)
	}
	tokenURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL.String(), nil)
	if err != nil {
		return "", fmt.Errorf("build Azure Web PubSub client-token request")
	}
	request.Header.Set("Authorization", "Bearer "+identityToken)
	response, err := adapter.config.HTTP.Do(request)
	if err != nil {
		return "", fmt.Errorf("Azure Web PubSub client-token request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Azure Web PubSub client-token request failed with HTTP %d", response.StatusCode)
	}
	limited := io.LimitReader(response.Body, maxRequestPayloadBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil || len(data) > maxRequestPayloadBytes {
		return "", fmt.Errorf("read Azure Web PubSub client-token response")
	}
	var payload struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(data, &payload) != nil || !azureWebPubSubBoundedValue(payload.Token, 16384) {
		return "", fmt.Errorf("Azure Web PubSub returned an invalid client-token response")
	}
	return payload.Token, nil
}

func readAzureWebPubSubServerMessage(ctx context.Context, connection cloudWebSocketConnection) ([]byte, bool, error) {
	messageType, data, err := connection.Read(ctx)
	if err != nil {
		return nil, false, err
	}
	if messageType != cloudWebSocketMessageText || len(data) == 0 || len(data) > maxRequestPayloadBytes || !utf8.Valid(data) || !json.Valid(data) {
		return nil, false, fmt.Errorf("Azure Web PubSub returned an invalid JSON text message")
	}
	var message map[string]any
	if json.Unmarshal(data, &message) != nil {
		return nil, false, fmt.Errorf("Azure Web PubSub returned invalid JSON")
	}
	kind, ok := message["type"].(string)
	if !ok {
		return nil, false, fmt.Errorf("Azure Web PubSub response has no type")
	}
	switch kind {
	case "ack":
		if success, ok := message["success"].(bool); !ok || !success {
			return nil, false, fmt.Errorf("Azure Web PubSub returned a failed acknowledgement")
		}
	case "streamNack":
		return nil, false, fmt.Errorf("Azure Web PubSub returned a stream rejection")
	case "streamClosed":
		if _, failed := message["error"]; failed {
			return nil, false, fmt.Errorf("Azure Web PubSub stream closed with an error")
		}
	case "message", "pong", "streamAck":
	case "system":
		event, _ := message["event"].(string)
		if event != "connected" && event != "disconnected" {
			return nil, false, fmt.Errorf("Azure Web PubSub returned an unsupported system event")
		}
	default:
		return nil, false, fmt.Errorf("Azure Web PubSub returned an unsupported protocol message")
	}
	canonical, err := json.Marshal(message)
	if err != nil {
		return nil, false, fmt.Errorf("encode Azure Web PubSub response")
	}
	return canonical, kind == "system" && message["event"] == "disconnected", nil
}

func defaultAzureWebPubSubWebSocketDial(ctx context.Context, target string, headers http.Header) (cloudWebSocketConnection, error) {
	client := &http.Client{Transport: http.DefaultTransport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	connection, response, err := websocket.Dial(ctx, target, &websocket.DialOptions{HTTPClient: client, HTTPHeader: headers.Clone(), Subprotocols: []string{"json.webpubsub.azure.v1"}, CompressionMode: websocket.CompressionDisabled})
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("Azure Web PubSub WebSocket handshake failed with HTTP %d", response.StatusCode)
		}
		return nil, fmt.Errorf("Azure Web PubSub WebSocket handshake failed")
	}
	if connection.Subprotocol() != "json.webpubsub.azure.v1" {
		connection.Close(websocket.StatusProtocolError, "missing required subprotocol")
		return nil, fmt.Errorf("Azure Web PubSub did not negotiate the required JSON subprotocol")
	}
	connection.SetReadLimit(maxRequestPayloadBytes)
	return coderTencentWebSocketConnection{connection: connection}, nil
}

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
	"strings"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
)

const (
	authSchemeGCPVertexLiveWS      = "vertex-live-ws"
	gcpVertexLivePath              = "/ws/google.cloud.aiplatform.v1beta1.LlmBidiService/BidiGenerateContent"
	gcpVertexLiveMaxClientMessages = 256
	gcpVertexLiveMaxServerMessages = 256
	gcpVertexLiveMaxTimeoutSeconds = 300
)

type gcpVertexLiveWebSocketDial func(context.Context, string, http.Header) (cloudWebSocketConnection, error)

type gcpVertexLivePlan struct {
	Messages       []json.RawMessage `json:"messages"`
	MaxMessages    int               `json:"max_messages"`
	TimeoutSeconds int               `json:"timeout_seconds"`

	encodedMessages [][]byte
}

func validateGCPVertexLiveWebSocketInvocation(invocation Invocation) error {
	if !strings.EqualFold(invocation.Method, http.MethodGet) || !strings.EqualFold(invocation.Service, "aiplatform") || !strings.EqualFold(invocation.Operation, "BidiGenerateContent") {
		return fmt.Errorf("Google Cloud Vertex Live WebSocket requires GET, service aiplatform, and operation BidiGenerateContent")
	}
	if !apiVersionPattern.MatchString(invocation.Project) || !identifierPattern.MatchString(invocation.Region) || strings.ToLower(invocation.Region) != invocation.Region {
		return fmt.Errorf("Google Cloud Vertex Live WebSocket requires valid project and region")
	}
	if err := validateGCPVertexLiveWebSocketTarget(invocation.URL, invocation.Region); err != nil {
		return err
	}
	if len(invocation.Headers) != 0 || len(invocation.Parameters) != 0 {
		return fmt.Errorf("Google Cloud Vertex Live WebSocket does not accept caller headers or query parameters")
	}
	if invocation.Body == nil || invocation.BodyFile != "" || invocation.ResponseFile == "" {
		return fmt.Errorf("Google Cloud Vertex Live WebSocket requires a finite protocol body and response_file; body_file is forbidden")
	}
	if invocation.RegionSet != "" || invocation.Subscription != "" || invocation.Audience != "" || invocation.APIVersion != "" || invocation.AuthVersion != "" || invocation.PayloadMode != "" || invocation.ChecksumAlgorithm != "" || invocation.StreamChunkBytes != 0 || invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return fmt.Errorf("Google Cloud Vertex Live WebSocket does not accept cross-provider, REST payload, checksum, or binary stream controls")
	}
	_, err := parseGCPVertexLivePlan(invocation.Body, invocation.Project, invocation.Region)
	return err
}

func validateGCPVertexLiveWebSocketTarget(rawURL, region string) error {
	target, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(target.Scheme, "wss") || target.Hostname() == "" || target.User != nil || target.Fragment != "" || target.RawQuery != "" {
		return fmt.Errorf("Google Cloud Vertex Live WebSocket requires an exact credential-free wss:// URL")
	}
	if target.Port() != "" && target.Port() != "443" {
		return fmt.Errorf("Google Cloud Vertex Live WebSocket endpoint port must be 443")
	}
	if target.EscapedPath() != gcpVertexLivePath {
		return fmt.Errorf("Google Cloud Vertex Live WebSocket requires the official BidiGenerateContent path")
	}
	expectedHost := strings.ToLower(region) + "-aiplatform.googleapis.com"
	switch strings.ToLower(region) {
	case "global":
		expectedHost = "aiplatform.googleapis.com"
	case "us", "eu":
		expectedHost = "aiplatform." + strings.ToLower(region) + ".rep.googleapis.com"
	}
	if !strings.EqualFold(target.Hostname(), expectedHost) {
		return fmt.Errorf("Google Cloud Vertex Live WebSocket host does not match the requested region")
	}
	return nil
}

func parseGCPVertexLivePlan(body any, project, region string) (gcpVertexLivePlan, error) {
	if body == nil || azureRealtimeContainsCredentialField(body) {
		return gcpVertexLivePlan{}, fmt.Errorf("Google Cloud Vertex Live WebSocket requires a credential-free protocol body")
	}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) == 0 || len(encoded) > maxRequestPayloadBytes {
		return gcpVertexLivePlan{}, fmt.Errorf("Google Cloud Vertex Live WebSocket body must be bounded JSON")
	}
	var plan gcpVertexLivePlan
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil || ensureJSONDecoderEOF(decoder) != nil {
		return gcpVertexLivePlan{}, fmt.Errorf("Google Cloud Vertex Live WebSocket body does not match the finite message-plan schema")
	}
	if len(plan.Messages) < 2 || len(plan.Messages) > gcpVertexLiveMaxClientMessages {
		return gcpVertexLivePlan{}, fmt.Errorf("Google Cloud Vertex Live WebSocket messages must contain 2 to %d frames", gcpVertexLiveMaxClientMessages)
	}
	if plan.MaxMessages < 2 || plan.MaxMessages > gcpVertexLiveMaxServerMessages {
		return gcpVertexLivePlan{}, fmt.Errorf("Google Cloud Vertex Live WebSocket max_messages must be between 2 and %d", gcpVertexLiveMaxServerMessages)
	}
	if plan.TimeoutSeconds < 1 || plan.TimeoutSeconds > gcpVertexLiveMaxTimeoutSeconds {
		return gcpVertexLivePlan{}, fmt.Errorf("Google Cloud Vertex Live WebSocket timeout_seconds must be between 1 and %d", gcpVertexLiveMaxTimeoutSeconds)
	}
	plan.encodedMessages = make([][]byte, len(plan.Messages))
	for index, rawMessage := range plan.Messages {
		canonical, kind, value, err := validateGCPVertexLiveClientMessage(rawMessage)
		if err != nil {
			return gcpVertexLivePlan{}, fmt.Errorf("Google Cloud Vertex Live WebSocket message %d: %w", index, err)
		}
		if index == 0 {
			if kind != "setup" {
				return gcpVertexLivePlan{}, fmt.Errorf("Google Cloud Vertex Live WebSocket first message must be setup")
			}
			if err := validateGCPVertexLiveSetupModel(value, project, region); err != nil {
				return gcpVertexLivePlan{}, err
			}
		} else if kind == "setup" {
			return gcpVertexLivePlan{}, fmt.Errorf("Google Cloud Vertex Live WebSocket setup is allowed only as the first message")
		}
		plan.encodedMessages[index] = canonical
	}
	return plan, nil
}

func validateGCPVertexLiveClientMessage(rawMessage json.RawMessage) ([]byte, string, json.RawMessage, error) {
	if len(rawMessage) == 0 || len(rawMessage) > maxRequestPayloadBytes || !json.Valid(rawMessage) {
		return nil, "", nil, fmt.Errorf("client message must be bounded JSON")
	}
	var message map[string]json.RawMessage
	if err := json.Unmarshal(rawMessage, &message); err != nil || len(message) != 1 {
		return nil, "", nil, fmt.Errorf("client message must contain exactly one protocol field")
	}
	var kind string
	var value json.RawMessage
	for name, raw := range message {
		kind, value = name, raw
	}
	switch kind {
	case "setup", "clientContent", "realtimeInput", "toolResponse":
	default:
		return nil, "", nil, fmt.Errorf("client message field must be setup, clientContent, realtimeInput, or toolResponse")
	}
	var decoded any
	if json.Unmarshal(rawMessage, &decoded) != nil || azureRealtimeContainsCredentialField(decoded) {
		return nil, "", nil, fmt.Errorf("client message contains invalid JSON or a credential field")
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		return nil, "", nil, fmt.Errorf("encode client message")
	}
	return canonical, kind, value, nil
}

func validateGCPVertexLiveSetupModel(rawSetup json.RawMessage, project, region string) error {
	var setup map[string]any
	if json.Unmarshal(rawSetup, &setup) != nil {
		return fmt.Errorf("Google Cloud Vertex Live WebSocket setup must be an object")
	}
	model, ok := setup["model"].(string)
	if !ok || len(model) > 512 || strings.TrimSpace(model) != model {
		return fmt.Errorf("Google Cloud Vertex Live WebSocket setup requires a bounded model name")
	}
	prefix := fmt.Sprintf("projects/%s/locations/%s/publishers/google/models/", project, region)
	modelID := strings.TrimPrefix(model, prefix)
	if modelID == model || !apiVersionPattern.MatchString(modelID) || strings.Contains(modelID, "..") {
		return fmt.Errorf("Google Cloud Vertex Live WebSocket model must match the requested project and region")
	}
	return nil
}

func invokeGCPVertexLiveWebSocket(ctx context.Context, adapter *GCPRESTAdapter, invocation Invocation) (InvocationResult, error) {
	if err := validateGCPVertexLiveWebSocketInvocation(invocation); err != nil {
		return InvocationResult{}, err
	}
	plan, _ := parseGCPVertexLivePlan(invocation.Body, invocation.Project, invocation.Region)
	token, err := adapter.config.Tokens.Token(ctx)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("load Google Cloud Vertex Live ADC token: %w", err)
	}
	if strings.TrimSpace(token) == "" {
		return InvocationResult{}, fmt.Errorf("Google Cloud credential returned an empty access token")
	}
	sessionCtx, cancelSession := context.WithTimeout(ctx, time.Duration(plan.TimeoutSeconds)*time.Second)
	defer cancelSession()
	connection, err := adapter.config.VertexLiveWebSocketDial(sessionCtx, invocation.URL, http.Header{"Authorization": []string{"Bearer " + token}})
	if err != nil {
		return InvocationResult{}, err
	}
	defer connection.Close()
	sink, err := newWebSocketOutputSink(invocation, adapter.config.MaxBodyBytes, "Google Cloud Vertex Live WebSocket")
	if err != nil {
		return InvocationResult{}, err
	}
	defer sink.abort()

	if err := connection.Write(sessionCtx, cloudWebSocketMessageText, plan.encodedMessages[0]); err != nil {
		return InvocationResult{}, fmt.Errorf("send Google Cloud Vertex Live setup")
	}
	setupMessage, setupKind, _, err := readGCPVertexLiveServerMessage(sessionCtx, connection)
	if err != nil {
		return InvocationResult{}, err
	}
	if setupKind != "setupComplete" {
		return InvocationResult{}, fmt.Errorf("Google Cloud Vertex Live WebSocket did not acknowledge setup")
	}
	if err := sink.writeMessage(setupMessage); err != nil {
		return InvocationResult{}, err
	}
	received := 1
	interval := time.Duration(invocation.StreamIntervalMS) * time.Millisecond
	for index := 1; index < len(plan.encodedMessages); index++ {
		if index > 1 && interval > 0 {
			if err := adapter.config.StreamPause(sessionCtx, interval); err != nil {
				return InvocationResult{}, fmt.Errorf("pace Google Cloud Vertex Live client messages")
			}
		}
		if err := connection.Write(sessionCtx, cloudWebSocketMessageText, plan.encodedMessages[index]); err != nil {
			return InvocationResult{}, fmt.Errorf("send Google Cloud Vertex Live client message")
		}
	}

	for received < plan.MaxMessages {
		message, kind, value, readErr := readGCPVertexLiveServerMessage(sessionCtx, connection)
		if errors.Is(readErr, context.DeadlineExceeded) {
			break
		}
		if readErr != nil {
			if readErr == io.EOF || websocket.CloseStatus(readErr) == websocket.StatusNormalClosure {
				break
			}
			return InvocationResult{}, readErr
		}
		if kind == "setupComplete" {
			return InvocationResult{}, fmt.Errorf("Google Cloud Vertex Live WebSocket returned duplicate setupComplete")
		}
		if err := sink.writeMessage(message); err != nil {
			return InvocationResult{}, err
		}
		received++
		if kind == "goAway" || gcpVertexLiveTurnComplete(kind, value) {
			break
		}
	}
	metadata, err := sink.finish("")
	if err != nil {
		return InvocationResult{}, err
	}
	var summary map[string]any
	if json.Unmarshal(metadata, &summary) != nil {
		return InvocationResult{}, fmt.Errorf("decode Google Cloud Vertex Live output metadata")
	}
	summary["messages"] = received
	metadata, err = json.Marshal(summary)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("encode Google Cloud Vertex Live output metadata")
	}
	return InvocationResult{Output: metadata}, nil
}

func readGCPVertexLiveServerMessage(ctx context.Context, connection cloudWebSocketConnection) ([]byte, string, json.RawMessage, error) {
	messageType, data, err := connection.Read(ctx)
	if err != nil {
		return nil, "", nil, err
	}
	if messageType != cloudWebSocketMessageText || len(data) == 0 || len(data) > maxRequestPayloadBytes || !utf8.Valid(data) || !json.Valid(data) {
		return nil, "", nil, fmt.Errorf("Google Cloud Vertex Live WebSocket returned an invalid JSON text message")
	}
	var message map[string]json.RawMessage
	if json.Unmarshal(data, &message) != nil || len(message) != 1 {
		return nil, "", nil, fmt.Errorf("Google Cloud Vertex Live WebSocket returned an invalid protocol message")
	}
	var kind string
	var value json.RawMessage
	for name, raw := range message {
		kind, value = name, raw
	}
	if kind == "error" {
		return nil, "", nil, fmt.Errorf("Google Cloud Vertex Live WebSocket returned a provider error")
	}
	switch kind {
	case "setupComplete", "serverContent", "toolCall", "toolCallCancellation", "usageMetadata", "goAway", "sessionResumptionUpdate", "inputTranscription", "outputTranscription":
	default:
		return nil, "", nil, fmt.Errorf("Google Cloud Vertex Live WebSocket returned an unsupported protocol message")
	}
	var decoded any
	if json.Unmarshal(data, &decoded) != nil {
		return nil, "", nil, fmt.Errorf("decode Google Cloud Vertex Live WebSocket response")
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		return nil, "", nil, fmt.Errorf("encode Google Cloud Vertex Live WebSocket response")
	}
	return canonical, kind, value, nil
}

func gcpVertexLiveTurnComplete(kind string, value json.RawMessage) bool {
	if kind != "serverContent" {
		return false
	}
	var content struct {
		TurnComplete bool `json:"turnComplete"`
	}
	return json.Unmarshal(value, &content) == nil && content.TurnComplete
}

func defaultGCPVertexLiveWebSocketDial(ctx context.Context, target string, headers http.Header) (cloudWebSocketConnection, error) {
	client := &http.Client{Transport: http.DefaultTransport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	connection, response, err := websocket.Dial(ctx, target, &websocket.DialOptions{HTTPClient: client, HTTPHeader: headers.Clone(), CompressionMode: websocket.CompressionDisabled})
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("Google Cloud Vertex Live WebSocket handshake failed with HTTP %d", response.StatusCode)
		}
		return nil, fmt.Errorf("Google Cloud Vertex Live WebSocket handshake failed")
	}
	connection.SetReadLimit(maxRequestPayloadBytes)
	return coderTencentWebSocketConnection{connection: connection}, nil
}

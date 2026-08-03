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
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/coder/websocket"
)

const (
	authSchemeGCPVertexLiveWS      = "vertex-live-ws"
	gcpVertexLivePath              = "/ws/google.cloud.aiplatform.v1beta1.LlmBidiService/BidiGenerateContent"
	gcpVertexLiveMaxClientMessages = 256
	gcpVertexLiveMaxServerMessages = 256
	gcpVertexLiveMaxTimeoutSeconds = 300
	gcpVertexLiveMaxToolHandlers   = 64
	gcpVertexLiveMaxToolCalls      = 32
	gcpVertexLiveMaxReconnects     = 8
	gcpVertexLiveMaxSessionHandle  = 16384
)

type gcpVertexLiveWebSocketDial func(context.Context, string, http.Header) (cloudWebSocketConnection, error)

type gcpVertexLivePlan struct {
	Messages       []json.RawMessage          `json:"messages"`
	ToolHandlers   []gcpVertexLiveToolHandler `json:"tool_handlers,omitempty"`
	ResumeOnGoAway bool                       `json:"resume_on_go_away,omitempty"`
	MaxReconnects  int                        `json:"max_reconnects,omitempty"`
	MaxMessages    int                        `json:"max_messages"`
	TimeoutSeconds int                        `json:"timeout_seconds"`

	encodedMessages [][]byte
}

type gcpVertexLiveToolHandler struct {
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
	MaxCalls int             `json:"max_calls"`

	encodedResponse []byte
}

type gcpVertexLiveToolHandlerState struct {
	response  json.RawMessage
	remaining int
}

type gcpVertexLiveFunctionResponse struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
}

type gcpVertexLiveTransportError struct {
	err error
}

func (err *gcpVertexLiveTransportError) Error() string { return err.err.Error() }
func (err *gcpVertexLiveTransportError) Unwrap() error { return err.err }

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
	if plan.ResumeOnGoAway {
		if plan.MaxReconnects < 1 || plan.MaxReconnects > gcpVertexLiveMaxReconnects {
			return gcpVertexLivePlan{}, fmt.Errorf("Google Cloud Vertex Live WebSocket max_reconnects must be between 1 and %d when resumption is enabled", gcpVertexLiveMaxReconnects)
		}
	} else if plan.MaxReconnects != 0 {
		return gcpVertexLivePlan{}, fmt.Errorf("Google Cloud Vertex Live WebSocket max_reconnects requires resume_on_go_away")
	}
	if len(plan.ToolHandlers) > gcpVertexLiveMaxToolHandlers {
		return gcpVertexLivePlan{}, fmt.Errorf("Google Cloud Vertex Live WebSocket supports at most %d tool handlers", gcpVertexLiveMaxToolHandlers)
	}
	handlerNames := make(map[string]struct{}, len(plan.ToolHandlers))
	for index := range plan.ToolHandlers {
		handler := &plan.ToolHandlers[index]
		if !apiVersionPattern.MatchString(handler.Name) {
			return gcpVertexLivePlan{}, fmt.Errorf("Google Cloud Vertex Live WebSocket tool handler %d has an invalid name", index)
		}
		if _, exists := handlerNames[handler.Name]; exists {
			return gcpVertexLivePlan{}, fmt.Errorf("Google Cloud Vertex Live WebSocket tool handler names must be unique")
		}
		handlerNames[handler.Name] = struct{}{}
		if handler.MaxCalls < 1 || handler.MaxCalls > gcpVertexLiveMaxToolCalls {
			return gcpVertexLivePlan{}, fmt.Errorf("Google Cloud Vertex Live WebSocket tool handler max_calls must be between 1 and %d", gcpVertexLiveMaxToolCalls)
		}
		var response map[string]any
		if len(handler.Response) == 0 || json.Unmarshal(handler.Response, &response) != nil || response == nil || azureRealtimeContainsCredentialField(response) {
			return gcpVertexLivePlan{}, fmt.Errorf("Google Cloud Vertex Live WebSocket tool handler response must be a credential-free JSON object")
		}
		var compact bytes.Buffer
		if json.Compact(&compact, handler.Response) != nil {
			return gcpVertexLivePlan{}, fmt.Errorf("encode Google Cloud Vertex Live tool handler response")
		}
		handler.encodedResponse = append([]byte(nil), compact.Bytes()...)
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
			if err := validateGCPVertexLiveInitialSessionResumption(value); err != nil {
				return gcpVertexLivePlan{}, err
			}
			if plan.ResumeOnGoAway {
				canonical, err = buildGCPVertexLiveSetup(canonical, "")
				if err != nil {
					return gcpVertexLivePlan{}, err
				}
			}
		} else if kind == "setup" {
			return gcpVertexLivePlan{}, fmt.Errorf("Google Cloud Vertex Live WebSocket setup is allowed only as the first message")
		}
		plan.encodedMessages[index] = canonical
	}
	return plan, nil
}

func validateGCPVertexLiveInitialSessionResumption(rawSetup json.RawMessage) error {
	var setup map[string]json.RawMessage
	if json.Unmarshal(rawSetup, &setup) != nil {
		return fmt.Errorf("Google Cloud Vertex Live WebSocket setup must be an object")
	}
	rawResumption, exists := setup["sessionResumption"]
	if !exists {
		return nil
	}
	var resumption map[string]json.RawMessage
	if json.Unmarshal(rawResumption, &resumption) != nil {
		return fmt.Errorf("Google Cloud Vertex Live WebSocket sessionResumption must be an object")
	}
	if _, hasHandle := resumption["handle"]; hasHandle {
		return fmt.Errorf("Google Cloud Vertex Live WebSocket session handles are internal and cannot be caller supplied")
	}
	return nil
}

func buildGCPVertexLiveSetup(encodedSetup []byte, handle string) ([]byte, error) {
	var envelope map[string]any
	if json.Unmarshal(encodedSetup, &envelope) != nil {
		return nil, fmt.Errorf("decode Google Cloud Vertex Live setup")
	}
	setup, ok := envelope["setup"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Google Cloud Vertex Live setup must be an object")
	}
	resumption := map[string]any{"transparent": true}
	if handle != "" {
		resumption["handle"] = handle
	}
	setup["sessionResumption"] = resumption
	canonical, err := json.Marshal(envelope)
	if err != nil || len(canonical) > maxRequestPayloadBytes {
		return nil, fmt.Errorf("encode Google Cloud Vertex Live resumable setup")
	}
	return canonical, nil
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
	sessionCtx, cancelSession := context.WithTimeout(ctx, time.Duration(plan.TimeoutSeconds)*time.Second)
	defer cancelSession()
	sink, err := newWebSocketOutputSink(invocation, adapter.config.MaxBodyBytes, "Google Cloud Vertex Live WebSocket")
	if err != nil {
		return InvocationResult{}, err
	}
	defer sink.abort()

	pending := make([][]byte, len(plan.encodedMessages)-1)
	for index := range pending {
		pending[index] = append([]byte(nil), plan.encodedMessages[index+1]...)
	}
	handlers := make(map[string]*gcpVertexLiveToolHandlerState, len(plan.ToolHandlers))
	for _, handler := range plan.ToolHandlers {
		handlers[handler.Name] = &gcpVertexLiveToolHandlerState{response: append(json.RawMessage(nil), handler.encodedResponse...), remaining: handler.MaxCalls}
	}
	seenToolCalls := make(map[string]struct{})
	received, reconnects, toolCalls := 0, 0, 0
	interval := time.Duration(invocation.StreamIntervalMS) * time.Millisecond
	var connection cloudWebSocketConnection
	defer func() {
		if connection != nil {
			_ = connection.Close()
		}
	}()
	resumeHandle := ""
	resumable := false
	sentIndex, acknowledgedIndex := 0, int64(0)
	connect := func(handle string) error {
		token, tokenErr := adapter.config.Tokens.Token(sessionCtx)
		if tokenErr != nil {
			return fmt.Errorf("load Google Cloud Vertex Live ADC token: %w", tokenErr)
		}
		if strings.TrimSpace(token) == "" {
			return fmt.Errorf("Google Cloud credential returned an empty access token")
		}
		setup := plan.encodedMessages[0]
		if handle != "" {
			setup, tokenErr = buildGCPVertexLiveSetup(plan.encodedMessages[0], handle)
			if tokenErr != nil {
				return tokenErr
			}
		}
		connection, tokenErr = adapter.config.VertexLiveWebSocketDial(sessionCtx, invocation.URL, http.Header{"Authorization": []string{"Bearer " + token}})
		if tokenErr != nil {
			return tokenErr
		}
		if tokenErr = connection.Write(sessionCtx, cloudWebSocketMessageText, setup); tokenErr != nil {
			return fmt.Errorf("send Google Cloud Vertex Live setup")
		}
		setupMessage, setupKind, _, tokenErr := readGCPVertexLiveServerMessage(sessionCtx, connection)
		if tokenErr != nil {
			return tokenErr
		}
		if setupKind != "setupComplete" {
			return fmt.Errorf("Google Cloud Vertex Live WebSocket did not acknowledge setup")
		}
		if received >= plan.MaxMessages {
			return fmt.Errorf("Google Cloud Vertex Live WebSocket response bound is too small for session setup")
		}
		if tokenErr = sink.writeMessage(setupMessage); tokenErr != nil {
			return tokenErr
		}
		received++
		for index, message := range pending {
			if index > 0 && interval > 0 {
				if tokenErr = adapter.config.StreamPause(sessionCtx, interval); tokenErr != nil {
					return fmt.Errorf("pace Google Cloud Vertex Live client messages")
				}
			}
			if tokenErr = connection.Write(sessionCtx, cloudWebSocketMessageText, message); tokenErr != nil {
				return fmt.Errorf("send Google Cloud Vertex Live client message")
			}
		}
		sentIndex = len(pending)
		acknowledgedIndex = 0
		return nil
	}
	if err := connect(""); err != nil {
		return InvocationResult{}, err
	}

	reconnect := func() error {
		if !plan.ResumeOnGoAway || !resumable || resumeHandle == "" {
			return fmt.Errorf("Google Cloud Vertex Live WebSocket cannot resume without a current internal session handle")
		}
		if reconnects >= plan.MaxReconnects {
			return fmt.Errorf("Google Cloud Vertex Live WebSocket exhausted max_reconnects")
		}
		_ = connection.Close()
		connection = nil
		reconnects++
		handle := resumeHandle
		resumeHandle = ""
		resumable = false
		return connect(handle)
	}

	for received < plan.MaxMessages {
		message, kind, value, readErr := readGCPVertexLiveServerMessage(sessionCtx, connection)
		if errors.Is(readErr, context.DeadlineExceeded) {
			break
		}
		if readErr != nil {
			var transportErr *gcpVertexLiveTransportError
			if errors.As(readErr, &transportErr) && plan.ResumeOnGoAway && resumable && resumeHandle != "" {
				if err := reconnect(); err != nil {
					return InvocationResult{}, err
				}
				continue
			}
			if errors.Is(readErr, io.EOF) || websocket.CloseStatus(readErr) == websocket.StatusNormalClosure {
				break
			}
			return InvocationResult{}, readErr
		}
		if kind == "setupComplete" {
			return InvocationResult{}, fmt.Errorf("Google Cloud Vertex Live WebSocket returned duplicate setupComplete")
		}
		if kind == "sessionResumptionUpdate" {
			update, updateErr := parseGCPVertexLiveSessionResumptionUpdate(value, acknowledgedIndex, int64(sentIndex))
			if updateErr != nil {
				return InvocationResult{}, updateErr
			}
			if update.acknowledged > acknowledgedIndex {
				prune := int(update.acknowledged - acknowledgedIndex)
				pending = pending[prune:]
				acknowledgedIndex = update.acknowledged
			}
			resumable, resumeHandle = update.resumable, update.handle
		}
		if err := sink.writeMessage(message); err != nil {
			return InvocationResult{}, err
		}
		received++
		if kind == "toolCall" {
			response, calls, responseErr := buildGCPVertexLiveToolResponse(value, handlers, seenToolCalls)
			if responseErr != nil {
				return InvocationResult{}, responseErr
			}
			if err := connection.Write(sessionCtx, cloudWebSocketMessageText, response); err != nil {
				return InvocationResult{}, fmt.Errorf("send Google Cloud Vertex Live tool response")
			}
			pending = append(pending, response)
			sentIndex++
			toolCalls += calls
		}
		if kind == "goAway" {
			if !plan.ResumeOnGoAway {
				break
			}
			if err := reconnect(); err != nil {
				return InvocationResult{}, err
			}
			continue
		}
		if gcpVertexLiveTurnComplete(kind, value) {
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
	summary["tool_calls"] = toolCalls
	summary["reconnects"] = reconnects
	metadata, err = json.Marshal(summary)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("encode Google Cloud Vertex Live output metadata")
	}
	return InvocationResult{Output: metadata}, nil
}

func readGCPVertexLiveServerMessage(ctx context.Context, connection cloudWebSocketConnection) ([]byte, string, json.RawMessage, error) {
	messageType, data, err := connection.Read(ctx)
	if err != nil {
		return nil, "", nil, &gcpVertexLiveTransportError{err: err}
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
	if kind == "sessionResumptionUpdate" {
		var update map[string]json.RawMessage
		if json.Unmarshal(value, &update) != nil {
			return nil, "", nil, fmt.Errorf("Google Cloud Vertex Live WebSocket returned an invalid session resumption update")
		}
		sanitized := make(map[string]any, 2)
		for _, field := range []string{"resumable", "lastConsumedClientMessageIndex"} {
			if rawField, exists := update[field]; exists {
				var fieldValue any
				if json.Unmarshal(rawField, &fieldValue) != nil {
					return nil, "", nil, fmt.Errorf("Google Cloud Vertex Live WebSocket returned an invalid session resumption update")
				}
				sanitized[field] = fieldValue
			}
		}
		decoded = map[string]any{"sessionResumptionUpdate": sanitized}
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		return nil, "", nil, fmt.Errorf("encode Google Cloud Vertex Live WebSocket response")
	}
	return canonical, kind, value, nil
}

type gcpVertexLiveSessionResumptionState struct {
	handle       string
	resumable    bool
	acknowledged int64
}

func parseGCPVertexLiveSessionResumptionUpdate(raw json.RawMessage, previous, sent int64) (gcpVertexLiveSessionResumptionState, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return gcpVertexLiveSessionResumptionState{}, fmt.Errorf("Google Cloud Vertex Live WebSocket returned an invalid session resumption update")
	}
	state := gcpVertexLiveSessionResumptionState{acknowledged: previous}
	if rawResumable, exists := fields["resumable"]; exists && json.Unmarshal(rawResumable, &state.resumable) != nil {
		return gcpVertexLiveSessionResumptionState{}, fmt.Errorf("Google Cloud Vertex Live WebSocket returned an invalid resumable flag")
	}
	if rawHandle, exists := fields["newHandle"]; exists && json.Unmarshal(rawHandle, &state.handle) != nil {
		return gcpVertexLiveSessionResumptionState{}, fmt.Errorf("Google Cloud Vertex Live WebSocket returned an invalid session handle")
	}
	if state.resumable {
		if !validGCPVertexLiveSessionHandle(state.handle) {
			return gcpVertexLiveSessionResumptionState{}, fmt.Errorf("Google Cloud Vertex Live WebSocket returned an invalid resumable session handle")
		}
	} else if state.handle != "" {
		return gcpVertexLiveSessionResumptionState{}, fmt.Errorf("Google Cloud Vertex Live WebSocket returned a handle for a non-resumable session")
	}
	if rawIndex, exists := fields["lastConsumedClientMessageIndex"]; exists {
		var encodedIndex string
		if json.Unmarshal(rawIndex, &encodedIndex) != nil {
			return gcpVertexLiveSessionResumptionState{}, fmt.Errorf("Google Cloud Vertex Live WebSocket returned a non-string consumed message index")
		}
		index, err := strconv.ParseInt(encodedIndex, 10, 64)
		if err != nil || index < previous || index > sent {
			return gcpVertexLiveSessionResumptionState{}, fmt.Errorf("Google Cloud Vertex Live WebSocket returned an out-of-range consumed message index")
		}
		state.acknowledged = index
	}
	return state, nil
}

func validGCPVertexLiveSessionHandle(handle string) bool {
	return handle != "" && len(handle) <= gcpVertexLiveMaxSessionHandle && utf8.ValidString(handle) && strings.TrimSpace(handle) == handle && strings.IndexFunc(handle, unicode.IsControl) < 0
}

func buildGCPVertexLiveToolResponse(raw json.RawMessage, handlers map[string]*gcpVertexLiveToolHandlerState, seen map[string]struct{}) ([]byte, int, error) {
	var toolCall struct {
		FunctionCalls []struct {
			ID   string          `json:"id"`
			Name string          `json:"name"`
			Args json.RawMessage `json:"args"`
		} `json:"functionCalls"`
	}
	if json.Unmarshal(raw, &toolCall) != nil || len(toolCall.FunctionCalls) == 0 || len(toolCall.FunctionCalls) > gcpVertexLiveMaxToolCalls {
		return nil, 0, fmt.Errorf("Google Cloud Vertex Live WebSocket returned an invalid tool call")
	}
	responses := make([]gcpVertexLiveFunctionResponse, 0, len(toolCall.FunctionCalls))
	batchIDs := make(map[string]struct{}, len(toolCall.FunctionCalls))
	batchCounts := make(map[string]int, len(toolCall.FunctionCalls))
	for _, call := range toolCall.FunctionCalls {
		if !validGCPVertexLiveToolCallID(call.ID) || !apiVersionPattern.MatchString(call.Name) {
			return nil, 0, fmt.Errorf("Google Cloud Vertex Live WebSocket returned an invalid tool call identity")
		}
		if _, exists := seen[call.ID]; exists {
			return nil, 0, fmt.Errorf("Google Cloud Vertex Live WebSocket returned a duplicate tool call id")
		}
		if _, exists := batchIDs[call.ID]; exists {
			return nil, 0, fmt.Errorf("Google Cloud Vertex Live WebSocket returned a duplicate tool call id")
		}
		batchIDs[call.ID] = struct{}{}
		handler, exists := handlers[call.Name]
		if !exists {
			return nil, 0, fmt.Errorf("Google Cloud Vertex Live WebSocket requested an unapproved tool %q", call.Name)
		}
		batchCounts[call.Name]++
		if batchCounts[call.Name] > handler.remaining {
			return nil, 0, fmt.Errorf("Google Cloud Vertex Live WebSocket tool %q exceeded max_calls", call.Name)
		}
		if !json.Valid(handler.response) {
			return nil, 0, fmt.Errorf("decode Google Cloud Vertex Live tool handler response")
		}
		responses = append(responses, gcpVertexLiveFunctionResponse{ID: call.ID, Name: call.Name, Response: append(json.RawMessage(nil), handler.response...)})
	}
	encoded, err := json.Marshal(map[string]any{"toolResponse": map[string]any{"functionResponses": responses}})
	if err != nil || len(encoded) > maxRequestPayloadBytes {
		return nil, 0, fmt.Errorf("encode Google Cloud Vertex Live tool response")
	}
	for _, call := range toolCall.FunctionCalls {
		seen[call.ID] = struct{}{}
	}
	for name, count := range batchCounts {
		handlers[name].remaining -= count
	}
	return encoded, len(responses), nil
}

func validGCPVertexLiveToolCallID(id string) bool {
	return id != "" && len(id) <= 512 && utf8.ValidString(id) && strings.TrimSpace(id) == id && strings.IndexFunc(id, unicode.IsControl) < 0
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

package cloud

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"
)

const (
	authSchemeAzureRealtimeWS  = "realtime-ws"
	authSchemeAzureVoiceLiveWS = "voice-live-ws"
)

const maxAzureRealtimeEvents = 65536

type azureRealtimeWebSocketDial func(context.Context, string, http.Header) (cloudWebSocketConnection, error)

type azureRealtimeEvents struct {
	messages             [][]byte
	responseCreates      int
	transcriptionCommits int
	sessionUpdates       int
}

func validateAzureRealtimeWebSocketInvocation(invocation Invocation) error {
	if normalizedAuthScheme(invocation.AuthScheme, "") == authSchemeAzureVoiceLiveWS {
		return validateAzureVoiceLiveWebSocketInvocation(invocation)
	}
	if !strings.EqualFold(invocation.Method, http.MethodGet) {
		return fmt.Errorf("Azure OpenAI Realtime WebSocket requires method GET for the HTTP upgrade")
	}
	if !strings.EqualFold(invocation.Service, "openai") {
		return fmt.Errorf("Azure OpenAI Realtime WebSocket requires service openai")
	}
	switch strings.ToLower(invocation.Operation) {
	case "realtimeresponse", "realtimetranscription", "realtimesession":
	default:
		return fmt.Errorf("Azure OpenAI Realtime WebSocket operation must be RealtimeResponse, RealtimeTranscription, or RealtimeSession")
	}
	target, err := url.Parse(invocation.URL)
	if err != nil || !strings.EqualFold(target.Scheme, "wss") || target.User != nil || target.Fragment != "" || target.RawQuery != "" || target.Port() != "" {
		return fmt.Errorf("Azure OpenAI Realtime WebSocket requires an official WSS URL without inline query parameters")
	}
	host := strings.ToLower(target.Hostname())
	if host == ".openai.azure.com" || !strings.HasSuffix(host, ".openai.azure.com") {
		return fmt.Errorf("Azure OpenAI Realtime WebSocket requires a resource.openai.azure.com host")
	}
	switch target.EscapedPath() {
	case "/openai/v1/realtime":
		if err := validateAzureRealtimeGAParameters(invocation.Parameters); err != nil {
			return err
		}
	case "/openai/realtime":
		if err := validateAzureRealtimePreviewParameters(invocation.Parameters); err != nil {
			return err
		}
	default:
		return fmt.Errorf("Azure OpenAI Realtime WebSocket requires the GA /openai/v1/realtime or preview /openai/realtime path")
	}
	if (invocation.Body == nil) == (invocation.BodyFile == "") {
		return fmt.Errorf("Azure OpenAI Realtime WebSocket requires exactly one of body or NDJSON body_file")
	}
	if invocation.Body != nil && azureRealtimeContainsCredentialField(invocation.Body) {
		return fmt.Errorf("Azure OpenAI Realtime events cannot contain credential fields")
	}
	if invocation.ResponseFile == "" {
		return fmt.Errorf("Azure OpenAI Realtime WebSocket requires response_file")
	}
	if len(invocation.Headers) != 0 {
		return fmt.Errorf("Azure OpenAI Realtime WebSocket does not accept caller-supplied handshake headers")
	}
	if invocation.Region != "" || invocation.Project != "" || invocation.Subscription != "" || invocation.Audience != "" || invocation.APIVersion != "" || invocation.AuthVersion != "" || invocation.PayloadMode != "" || invocation.ChecksumAlgorithm != "" || invocation.StreamChunkBytes != 0 || invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return fmt.Errorf("Azure OpenAI Realtime WebSocket does not accept REST, cross-provider, chunk-size, or provider-specific controls")
	}
	return nil
}

func validateAzureVoiceLiveWebSocketInvocation(invocation Invocation) error {
	if !strings.EqualFold(invocation.Method, http.MethodGet) || !strings.EqualFold(invocation.Service, "voice-live") {
		return fmt.Errorf("Azure Voice Live WebSocket requires method GET and service voice-live")
	}
	operation := strings.ToLower(strings.TrimSpace(invocation.Operation))
	switch operation {
	case "voiceliveresponse", "voicelivetranscription", "voicelivesession":
		if invocation.Mode != ModeRead {
			return fmt.Errorf("Azure Voice Live model sessions require the read tool")
		}
	case "voiceliveagentsession":
		if invocation.Mode != ModeMutate {
			return fmt.Errorf("Azure Voice Live Agent sessions require the mutate tool")
		}
	default:
		return fmt.Errorf("Azure Voice Live operation must be VoiceLiveResponse, VoiceLiveTranscription, VoiceLiveSession, or VoiceLiveAgentSession")
	}
	target, err := url.Parse(invocation.URL)
	if err != nil || !strings.EqualFold(target.Scheme, "wss") || target.User != nil || target.Fragment != "" || target.RawQuery != "" || target.Port() != "" || target.EscapedPath() != "/voice-live/realtime" {
		return fmt.Errorf("Azure Voice Live requires the exact official query-free WSS endpoint")
	}
	host := strings.ToLower(target.Hostname())
	if !azureVoiceLiveHost(host) {
		return fmt.Errorf("Azure Voice Live requires an official Foundry or Cognitive Services host")
	}
	values, err := azureRealtimeStringParameters(invocation.Parameters, map[string]bool{"api-version": true, "model": true, "agent_id": true, "project_id": true})
	if err != nil || values["api-version"] == "" || !apiVersionPattern.MatchString(values["api-version"]) {
		return fmt.Errorf("Azure Voice Live requires one valid api-version query parameter")
	}
	modelMode := values["model"] != "" && values["agent_id"] == "" && values["project_id"] == ""
	agentMode := values["model"] == "" && values["agent_id"] != "" && values["project_id"] != ""
	if !modelMode && !agentMode {
		return fmt.Errorf("Azure Voice Live requires exactly model or the agent_id/project_id pair")
	}
	if (operation == "voiceliveagentsession") != agentMode {
		return fmt.Errorf("Azure Voice Live Agent operation and query parameters must agree")
	}
	if (invocation.Body == nil) == (invocation.BodyFile == "") {
		return fmt.Errorf("Azure Voice Live requires exactly one of body or NDJSON body_file")
	}
	if invocation.Body != nil && azureRealtimeContainsCredentialField(invocation.Body) {
		return fmt.Errorf("Azure Voice Live events cannot contain credential fields")
	}
	if invocation.ResponseFile == "" || len(invocation.Headers) != 0 {
		return fmt.Errorf("Azure Voice Live requires response_file and forbids caller handshake headers")
	}
	if invocation.Region != "" || invocation.Project != "" || invocation.Subscription != "" || invocation.Audience != "" || invocation.APIVersion != "" || invocation.AuthVersion != "" || invocation.PayloadMode != "" || invocation.ChecksumAlgorithm != "" || invocation.StreamChunkBytes != 0 || invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return fmt.Errorf("Azure Voice Live does not accept REST, cross-provider, chunk-size, or provider-specific controls")
	}
	if invocation.Body != nil {
		events, err := loadAzureRealtimeEvents(invocation)
		if err != nil {
			return err
		}
		if err := validateAzureRealtimeEventIntent(invocation, events); err != nil {
			return err
		}
	}
	return nil
}

func azureVoiceLiveHost(host string) bool {
	for _, suffix := range []string{".services.ai.azure.com", ".cognitiveservices.azure.com"} {
		prefix := strings.TrimSuffix(host, suffix)
		if prefix != host && endpointLabelPattern.MatchString(prefix) {
			return true
		}
	}
	return false
}

func validateAzureRealtimeGAParameters(parameters map[string]any) error {
	values, err := azureRealtimeStringParameters(parameters, map[string]bool{"model": true, "intent": true})
	if err != nil {
		return err
	}
	model, intent := values["model"], values["intent"]
	if (model == "") == (intent == "") || intent != "" && intent != "transcription" {
		return fmt.Errorf("Azure OpenAI Realtime GA requires exactly model=<deployment> or intent=transcription")
	}
	return nil
}

func validateAzureRealtimePreviewParameters(parameters map[string]any) error {
	values, err := azureRealtimeStringParameters(parameters, map[string]bool{"api-version": true, "deployment": true})
	if err != nil {
		return err
	}
	if values["api-version"] == "" || values["deployment"] == "" {
		return fmt.Errorf("Azure OpenAI Realtime preview requires api-version and deployment")
	}
	return nil
}

func azureRealtimeStringParameters(parameters map[string]any, allowed map[string]bool) (map[string]string, error) {
	output := make(map[string]string, len(parameters))
	for name, raw := range parameters {
		if !allowed[name] {
			return nil, fmt.Errorf("unsupported Azure OpenAI Realtime query parameter %q", name)
		}
		values, err := stringValues(raw)
		if err != nil || len(values) != 1 || strings.TrimSpace(values[0]) == "" || strings.TrimSpace(values[0]) != values[0] || len(values[0]) > 256 {
			return nil, fmt.Errorf("Azure OpenAI Realtime query parameter %q must be one bounded scalar", name)
		}
		for _, character := range values[0] {
			if character == 0 || character == '\r' || character == '\n' {
				return nil, fmt.Errorf("Azure OpenAI Realtime query parameter %q contains a control character", name)
			}
		}
		output[name] = values[0]
	}
	return output, nil
}

func loadAzureRealtimeEvents(invocation Invocation) (azureRealtimeEvents, error) {
	if invocation.BodyFile != "" {
		file, err := os.Open(invocation.BodyFile)
		if err != nil {
			return azureRealtimeEvents{}, fmt.Errorf("open Azure OpenAI Realtime body_file: %w", err)
		}
		defer file.Close()
		return scanAzureRealtimeEvents(file)
	}
	values, ok := invocation.Body.([]any)
	if !ok {
		values = []any{invocation.Body}
	}
	events := azureRealtimeEvents{}
	for _, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			return azureRealtimeEvents{}, fmt.Errorf("encode Azure OpenAI Realtime event: %w", err)
		}
		if err := events.add(encoded); err != nil {
			return azureRealtimeEvents{}, err
		}
	}
	return events.finish()
}

func scanAzureRealtimeEvents(source io.Reader) (azureRealtimeEvents, error) {
	scanner := bufio.NewScanner(source)
	scanner.Buffer(make([]byte, 64*1024), maxRequestPayloadBytes+1)
	events := azureRealtimeEvents{}
	totalBytes := 0
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		totalBytes += len(line)
		if totalBytes > maxRequestFileBytes {
			return azureRealtimeEvents{}, fmt.Errorf("Azure OpenAI Realtime NDJSON events exceed %d bytes", maxRequestFileBytes)
		}
		if err := events.add(append([]byte(nil), line...)); err != nil {
			return azureRealtimeEvents{}, err
		}
	}
	if err := scanner.Err(); err != nil {
		return azureRealtimeEvents{}, fmt.Errorf("read Azure OpenAI Realtime NDJSON events: %w", err)
	}
	return events.finish()
}

func (events *azureRealtimeEvents) add(encoded []byte) error {
	if len(encoded) == 0 || len(encoded) > maxRequestPayloadBytes {
		return fmt.Errorf("Azure OpenAI Realtime event must be between 1 and %d bytes", maxRequestPayloadBytes)
	}
	if len(events.messages) >= maxAzureRealtimeEvents {
		return fmt.Errorf("Azure OpenAI Realtime event count exceeds %d", maxAzureRealtimeEvents)
	}
	var event map[string]any
	if err := json.Unmarshal(encoded, &event); err != nil {
		return fmt.Errorf("Azure OpenAI Realtime body must contain JSON objects")
	}
	eventType, ok := event["type"].(string)
	if !ok || strings.TrimSpace(eventType) == "" || len(eventType) > 128 {
		return fmt.Errorf("Azure OpenAI Realtime event requires a bounded string type")
	}
	if azureRealtimeContainsCredentialField(event) {
		return fmt.Errorf("Azure OpenAI Realtime events cannot contain credential fields")
	}
	canonical, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode Azure OpenAI Realtime event: %w", err)
	}
	switch eventType {
	case "response.create":
		events.responseCreates++
	case "input_audio_buffer.commit":
		events.transcriptionCommits++
	case "session.update":
		events.sessionUpdates++
	}
	events.messages = append(events.messages, canonical)
	return nil
}

func (events azureRealtimeEvents) finish() (azureRealtimeEvents, error) {
	if len(events.messages) == 0 {
		return azureRealtimeEvents{}, fmt.Errorf("Azure OpenAI Realtime requires at least one event")
	}
	if events.responseCreates == 0 && events.transcriptionCommits == 0 && events.sessionUpdates == 0 {
		return azureRealtimeEvents{}, fmt.Errorf("Azure OpenAI Realtime event stream has no bounded terminal event")
	}
	return events, nil
}

func azureRealtimeContainsCredentialField(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for name, child := range typed {
			normalized := normalizedOperation(name)
			if normalized == "authorization" || normalized == "apikey" || normalized == "accesstoken" || normalized == "bearertoken" || normalized == "clientsecret" || normalized == "credential" || normalized == "sessiontoken" {
				return true
			}
			if azureRealtimeContainsCredentialField(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if azureRealtimeContainsCredentialField(child) {
				return true
			}
		}
	}
	return false
}

func invokeAzureRealtimeWebSocket(ctx context.Context, adapter *AzureRESTAdapter, invocation Invocation) (InvocationResult, error) {
	if err := validateAzureRealtimeWebSocketInvocation(invocation); err != nil {
		return InvocationResult{}, err
	}
	events, err := loadAzureRealtimeEvents(invocation)
	if err != nil {
		return InvocationResult{}, err
	}
	if err := validateAzureRealtimeEventIntent(invocation, events); err != nil {
		return InvocationResult{}, err
	}
	token, err := adapter.config.Tokens.Token(ctx, azureRealtimeTokenScope(invocation))
	if err != nil {
		return InvocationResult{}, fmt.Errorf("load Azure OpenAI Realtime identity token: %w", err)
	}
	if strings.TrimSpace(token) == "" {
		return InvocationResult{}, fmt.Errorf("Azure identity returned an empty access token")
	}
	target, _ := url.Parse(invocation.URL)
	if err := addQueryParameters(target, invocation.Parameters); err != nil {
		return InvocationResult{}, fmt.Errorf("build Azure OpenAI Realtime query: %w", err)
	}
	streamContext, cancel := azureRealtimeStreamContext(ctx, len(events.messages), invocation.StreamIntervalMS)
	defer cancel()
	connection, err := adapter.config.WebSocketDial(streamContext, target.String(), http.Header{"Authorization": []string{"Bearer " + token}})
	if err != nil {
		return InvocationResult{}, err
	}
	defer connection.Close()
	sink, err := newWebSocketOutputSink(invocation, adapter.config.MaxBodyBytes, "Azure OpenAI Realtime WebSocket")
	if err != nil {
		return InvocationResult{}, err
	}
	defer sink.abort()
	for index, message := range events.messages {
		if index > 0 && invocation.StreamIntervalMS > 0 {
			if err := adapter.config.StreamPause(streamContext, time.Duration(invocation.StreamIntervalMS)*time.Millisecond); err != nil {
				return InvocationResult{}, fmt.Errorf("pace Azure OpenAI Realtime event stream: %w", err)
			}
		}
		if err := connection.Write(streamContext, cloudWebSocketMessageText, message); err != nil {
			return InvocationResult{}, fmt.Errorf("write Azure OpenAI Realtime event")
		}
	}
	requestID, err := readAzureRealtimeEvents(streamContext, connection, sink, events)
	if err != nil {
		return InvocationResult{}, err
	}
	output, err := sink.finish(requestID)
	if err != nil {
		return InvocationResult{}, err
	}
	return InvocationResult{Output: output, RequestID: requestID}, nil
}

func azureRealtimeTokenScope(invocation Invocation) string {
	if normalizedAuthScheme(invocation.AuthScheme, "") == authSchemeAzureVoiceLiveWS {
		target, _ := url.Parse(invocation.URL)
		if strings.HasSuffix(strings.ToLower(target.Hostname()), ".cognitiveservices.azure.com") {
			return "https://cognitiveservices.azure.com/.default"
		}
	}
	return "https://ai.azure.com/.default"
}

func validateAzureRealtimeEventIntent(invocation Invocation, events azureRealtimeEvents) error {
	switch strings.ToLower(strings.TrimSpace(invocation.Operation)) {
	case "realtimeresponse", "voiceliveresponse", "voiceliveagentsession":
		if events.responseCreates == 0 {
			return fmt.Errorf("Azure realtime response session requires response.create")
		}
	case "realtimetranscription", "voicelivetranscription":
		if events.transcriptionCommits == 0 {
			return fmt.Errorf("Azure realtime transcription session requires input_audio_buffer.commit")
		}
	case "realtimesession", "voicelivesession":
		if events.sessionUpdates == 0 {
			return fmt.Errorf("Azure realtime session requires session.update")
		}
	}
	return nil
}

func readAzureRealtimeEvents(ctx context.Context, connection cloudWebSocketConnection, sink *cloudWebSocketOutputSink, expected azureRealtimeEvents) (string, error) {
	responsesDone, transcriptionsDone, sessionsUpdated := 0, 0, 0
	requestID := ""
	for {
		messageType, data, err := connection.Read(ctx)
		if err != nil {
			return requestID, fmt.Errorf("read Azure OpenAI Realtime response")
		}
		if messageType != cloudWebSocketMessageText || len(data) == 0 || len(data) > maxRequestPayloadBytes {
			return requestID, fmt.Errorf("Azure OpenAI Realtime returned an invalid event frame")
		}
		var event map[string]any
		if err := json.Unmarshal(data, &event); err != nil {
			return requestID, fmt.Errorf("Azure OpenAI Realtime returned invalid JSON")
		}
		eventType, ok := event["type"].(string)
		if !ok || eventType == "" {
			return requestID, fmt.Errorf("Azure OpenAI Realtime returned an event without type")
		}
		if eventType == "error" || eventType == "conversation.item.input_audio_transcription.failed" {
			return requestID, fmt.Errorf("Azure OpenAI Realtime returned a failed event")
		}
		if azureRealtimeContainsCredentialField(event) {
			return requestID, fmt.Errorf("Azure realtime service returned a credential-bearing event")
		}
		if eventID, ok := event["event_id"].(string); ok && eventID != "" {
			requestID = eventID
		}
		response, hasResponse := event["response"].(map[string]any)
		if hasResponse {
			if responseID, ok := response["id"].(string); ok && responseID != "" {
				requestID = responseID
			}
			if status, ok := response["status"].(string); ok && (status == "failed" || status == "cancelled") {
				return requestID, fmt.Errorf("Azure OpenAI Realtime response did not complete successfully")
			}
		}
		if eventType == "response.done" {
			responseID, idOK := response["id"].(string)
			status, statusOK := response["status"].(string)
			if !hasResponse || !idOK || strings.TrimSpace(responseID) == "" || len(responseID) > 256 || !statusOK || status != "completed" && status != "incomplete" {
				return requestID, fmt.Errorf("Azure OpenAI Realtime returned an invalid response.done event")
			}
		}
		if err := sink.writeMessage(data); err != nil {
			return requestID, err
		}
		switch eventType {
		case "response.done":
			responsesDone++
		case "conversation.item.input_audio_transcription.completed":
			transcriptionsDone++
		case "session.updated":
			sessionsUpdated++
		}
		responseComplete := expected.responseCreates == 0 || responsesDone >= expected.responseCreates
		transcriptionComplete := expected.transcriptionCommits == 0 || transcriptionsDone >= expected.transcriptionCommits
		sessionComplete := expected.responseCreates != 0 || expected.transcriptionCommits != 0 || sessionsUpdated >= expected.sessionUpdates
		if responseComplete && transcriptionComplete && sessionComplete {
			return requestID, nil
		}
	}
}

func azureRealtimeStreamContext(ctx context.Context, messageCount, intervalMS int) (context.Context, context.CancelFunc) {
	duration := 10*time.Minute + time.Duration(messageCount*intervalMS)*time.Millisecond
	if duration > time.Hour {
		duration = time.Hour
	}
	return context.WithTimeout(ctx, duration)
}

func defaultAzureRealtimeWebSocketDial(ctx context.Context, target string, headers http.Header) (cloudWebSocketConnection, error) {
	client := &http.Client{Transport: http.DefaultTransport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	connection, response, err := websocket.Dial(ctx, target, &websocket.DialOptions{
		HTTPClient: client, HTTPHeader: headers.Clone(), CompressionMode: websocket.CompressionDisabled,
	})
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("Azure OpenAI Realtime WebSocket handshake failed with HTTP %d", response.StatusCode)
		}
		return nil, fmt.Errorf("Azure OpenAI Realtime WebSocket handshake failed")
	}
	connection.SetReadLimit(maxRequestPayloadBytes)
	return coderTencentWebSocketConnection{connection: connection}, nil
}

package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/coder/websocket"
)

const (
	alibabaNLSTokenEndpoint        = "https://nlsmeta.ap-southeast-1.aliyuncs.com/"
	alibabaNLSTokenAPIVersion      = "2019-07-17"
	alibabaNLSSuccessStatus        = int64(20000000)
	defaultAlibabaNLSChunkPause    = 100 * time.Millisecond
	maxAlibabaNLSAudioChunkBytes   = 256 * 1024
	maxAlibabaNLSTextSegments      = 65536
	alibabaNLSTokenRefreshSkew     = 5 * time.Minute
	alibabaNLSTokenMinimumLifetime = 30 * time.Second
)

var alibabaNLSIDPattern = regexp.MustCompile(`^[A-Fa-f0-9]{32}$`)

type alibabaNLSProtocolKind uint8

const (
	alibabaNLSAudioInput alibabaNLSProtocolKind = iota + 1
	alibabaNLSFlowingTTS
	alibabaNLSSingleTTS
)

type alibabaNLSProtocol struct {
	namespace string
	start     string
	started   string
	stop      string
	completed string
	kind      alibabaNLSProtocolKind
}

type alibabaNLSInput struct {
	start map[string]any
	texts []string
}

type alibabaNLSEventHeader struct {
	MessageID string `json:"message_id"`
	TaskID    string `json:"task_id"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Status    int64  `json:"status"`
}

type alibabaNLSEvent struct {
	Header alibabaNLSEventHeader `json:"header"`
}

type alibabaNLSReadResult struct {
	requestID string
	err       error
}

func alibabaNLSProtocolForOperation(operation string) (alibabaNLSProtocol, bool) {
	switch strings.ToLower(strings.TrimSpace(operation)) {
	case "speechtranscriber":
		return alibabaNLSProtocol{namespace: "SpeechTranscriber", start: "StartTranscription", started: "TranscriptionStarted", stop: "StopTranscription", completed: "TranscriptionCompleted", kind: alibabaNLSAudioInput}, true
	case "speechrecognizer":
		return alibabaNLSProtocol{namespace: "SpeechRecognizer", start: "StartRecognition", started: "RecognitionStarted", stop: "StopRecognition", completed: "RecognitionCompleted", kind: alibabaNLSAudioInput}, true
	case "flowingspeechsynthesizer":
		return alibabaNLSProtocol{namespace: "FlowingSpeechSynthesizer", start: "StartSynthesis", started: "SynthesisStarted", stop: "StopSynthesis", completed: "SynthesisCompleted", kind: alibabaNLSFlowingTTS}, true
	case "speechsynthesizer":
		return alibabaNLSProtocol{namespace: "SpeechSynthesizer", start: "StartSynthesis", completed: "SynthesisCompleted", kind: alibabaNLSSingleTTS}, true
	case "speechlongsynthesizer":
		return alibabaNLSProtocol{namespace: "SpeechLongSynthesizer", start: "StartSynthesis", completed: "SynthesisCompleted", kind: alibabaNLSSingleTTS}, true
	default:
		return alibabaNLSProtocol{}, false
	}
}

func validateAlibabaNLSWebSocketInvocation(invocation Invocation) error {
	if !strings.EqualFold(invocation.Method, http.MethodGet) {
		return fmt.Errorf("Alibaba Cloud NLS WebSocket requires method GET for the HTTP upgrade")
	}
	if !strings.EqualFold(invocation.Service, "nls") {
		return fmt.Errorf("Alibaba Cloud NLS WebSocket requires service nls")
	}
	protocol, ok := alibabaNLSProtocolForOperation(invocation.Operation)
	if !ok {
		return fmt.Errorf("Alibaba Cloud NLS WebSocket operation must be SpeechTranscriber, SpeechRecognizer, FlowingSpeechSynthesizer, SpeechSynthesizer, or SpeechLongSynthesizer")
	}
	target, err := url.Parse(invocation.URL)
	if err != nil || !strings.EqualFold(target.Scheme, "wss") || target.User != nil || target.Fragment != "" || target.RawQuery != "" || target.Port() != "" || target.EscapedPath() != "/ws/v1" {
		return fmt.Errorf("Alibaba Cloud NLS WebSocket requires an official public WSS /ws/v1 URL without caller query parameters")
	}
	if !isAlibabaNLSPublicGatewayHost(target.Hostname()) {
		return fmt.Errorf("Alibaba Cloud NLS WebSocket requires an official public nls-gateway host")
	}
	if _, err := alibabaNLSAppKey(invocation.Parameters); err != nil {
		return err
	}
	if len(invocation.Headers) != 0 {
		return fmt.Errorf("Alibaba Cloud NLS WebSocket does not accept caller-supplied handshake headers")
	}
	if invocation.ResponseFile == "" {
		return fmt.Errorf("Alibaba Cloud NLS WebSocket requires response_file")
	}
	if invocation.Region != "" || invocation.RegionSet != "" || invocation.Project != "" || invocation.Subscription != "" || invocation.Audience != "" || invocation.APIVersion != "" || invocation.AuthVersion != "" || invocation.PayloadMode != "" || invocation.ChecksumAlgorithm != "" || invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return fmt.Errorf("Alibaba Cloud NLS WebSocket does not accept REST, cross-provider, or provider-specific controls")
	}
	if invocation.StreamChunkBytes > maxAlibabaNLSAudioChunkBytes {
		return fmt.Errorf("Alibaba Cloud NLS WebSocket audio chunks must not exceed %d bytes", maxAlibabaNLSAudioChunkBytes)
	}
	if protocol.kind != alibabaNLSAudioInput && invocation.StreamChunkBytes != 0 {
		return fmt.Errorf("stream_chunk_bytes is supported only by Alibaba Cloud NLS recognition")
	}
	if protocol.kind == alibabaNLSAudioInput {
		if invocation.BodyFile == "" {
			return fmt.Errorf("Alibaba Cloud NLS recognition requires body_file for the finite audio stream")
		}
	} else if invocation.BodyFile != "" {
		return fmt.Errorf("Alibaba Cloud NLS synthesis does not accept body_file")
	}
	_, err = parseAlibabaNLSInput(invocation, protocol)
	return err
}

func isAlibabaNLSPublicGatewayHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "nls-gateway.aliyuncs.com" {
		return true
	}
	if !strings.HasPrefix(host, "nls-gateway-") || !strings.HasSuffix(host, ".aliyuncs.com") {
		return false
	}
	region := strings.TrimSuffix(strings.TrimPrefix(host, "nls-gateway-"), ".aliyuncs.com")
	return identifierPattern.MatchString(region) && !strings.Contains(region, "internal")
}

func alibabaNLSAppKey(parameters map[string]any) (string, error) {
	if len(parameters) != 1 {
		return "", fmt.Errorf("Alibaba Cloud NLS WebSocket requires only the appkey parameter")
	}
	raw, ok := parameters["appkey"]
	if !ok {
		return "", fmt.Errorf("Alibaba Cloud NLS WebSocket requires appkey")
	}
	values, err := stringValues(raw)
	if err != nil || len(values) != 1 {
		return "", fmt.Errorf("Alibaba Cloud NLS appkey must be one string")
	}
	value := values[0]
	if value == "" || len(value) > 256 || strings.TrimSpace(value) != value {
		return "", fmt.Errorf("Alibaba Cloud NLS appkey must be a bounded non-empty string")
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return "", fmt.Errorf("Alibaba Cloud NLS appkey contains a control character")
		}
	}
	return value, nil
}

func parseAlibabaNLSInput(invocation Invocation, protocol alibabaNLSProtocol) (alibabaNLSInput, error) {
	if alibabaNLSContainsCredentialField(invocation.Body) {
		return alibabaNLSInput{}, fmt.Errorf("Alibaba Cloud NLS payload cannot contain credential fields")
	}
	switch protocol.kind {
	case alibabaNLSAudioInput:
		if invocation.Body == nil {
			return alibabaNLSInput{start: map[string]any{}}, nil
		}
		payload, ok := invocation.Body.(map[string]any)
		if !ok {
			return alibabaNLSInput{}, fmt.Errorf("Alibaba Cloud NLS recognition body must be a JSON object")
		}
		if err := validateAlibabaNLSPayloadSize(payload); err != nil {
			return alibabaNLSInput{}, err
		}
		return alibabaNLSInput{start: payload}, nil
	case alibabaNLSFlowingTTS:
		body, ok := invocation.Body.(map[string]any)
		if !ok {
			return alibabaNLSInput{}, fmt.Errorf("Alibaba Cloud flowing synthesis body must contain start and texts")
		}
		for name := range body {
			if name != "start" && name != "texts" {
				return alibabaNLSInput{}, fmt.Errorf("unsupported Alibaba Cloud flowing synthesis body field %q", name)
			}
		}
		start := map[string]any{}
		if raw, present := body["start"]; present {
			var valid bool
			start, valid = raw.(map[string]any)
			if !valid {
				return alibabaNLSInput{}, fmt.Errorf("Alibaba Cloud flowing synthesis start must be a JSON object")
			}
		}
		texts, err := alibabaNLSTexts(body["texts"])
		if err != nil {
			return alibabaNLSInput{}, err
		}
		if err := validateAlibabaNLSPayloadSize(body); err != nil {
			return alibabaNLSInput{}, err
		}
		return alibabaNLSInput{start: start, texts: texts}, nil
	case alibabaNLSSingleTTS:
		payload, ok := invocation.Body.(map[string]any)
		if !ok {
			return alibabaNLSInput{}, fmt.Errorf("Alibaba Cloud NLS synthesis body must be a JSON object")
		}
		text, ok := payload["text"].(string)
		if !ok || strings.TrimSpace(text) == "" {
			return alibabaNLSInput{}, fmt.Errorf("Alibaba Cloud NLS synthesis body requires non-empty text")
		}
		if err := validateAlibabaNLSPayloadSize(payload); err != nil {
			return alibabaNLSInput{}, err
		}
		return alibabaNLSInput{start: payload}, nil
	default:
		return alibabaNLSInput{}, fmt.Errorf("unsupported Alibaba Cloud NLS protocol")
	}
}

func alibabaNLSTexts(raw any) ([]string, error) {
	var values []any
	switch typed := raw.(type) {
	case []any:
		values = typed
	case []string:
		values = make([]any, len(typed))
		for index := range typed {
			values[index] = typed[index]
		}
	default:
		return nil, fmt.Errorf("Alibaba Cloud flowing synthesis texts must be an array")
	}
	if len(values) == 0 || len(values) > maxAlibabaNLSTextSegments {
		return nil, fmt.Errorf("Alibaba Cloud flowing synthesis requires between 1 and %d text segments", maxAlibabaNLSTextSegments)
	}
	texts := make([]string, len(values))
	total := 0
	for index, rawValue := range values {
		value, ok := rawValue.(string)
		if !ok || strings.TrimSpace(value) == "" || len(value) > maxRequestPayloadBytes {
			return nil, fmt.Errorf("Alibaba Cloud flowing synthesis text segment must be a bounded non-empty string")
		}
		total += len(value)
		if total > maxRequestFileBytes {
			return nil, fmt.Errorf("Alibaba Cloud flowing synthesis text exceeds %d bytes", maxRequestFileBytes)
		}
		texts[index] = value
	}
	return texts, nil
}

func validateAlibabaNLSPayloadSize(value any) error {
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > maxRequestPayloadBytes {
		return fmt.Errorf("Alibaba Cloud NLS payload must be bounded valid JSON")
	}
	return nil
}

func alibabaNLSContainsCredentialField(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for name, child := range typed {
			normalized := normalizedOperation(name)
			for _, blocked := range []string{"authorization", "accesstoken", "securitytoken", "token", "accesskeyid", "accesskeysecret", "apikey", "secret"} {
				if normalized == blocked {
					return true
				}
			}
			if alibabaNLSContainsCredentialField(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if alibabaNLSContainsCredentialField(child) {
				return true
			}
		}
	}
	return false
}

func invokeAlibabaNLSWebSocket(ctx context.Context, adapter *AlibabaRESTAdapter, invocation Invocation) (InvocationResult, error) {
	if err := validateAlibabaNLSWebSocketInvocation(invocation); err != nil {
		return InvocationResult{}, err
	}
	protocol, _ := alibabaNLSProtocolForOperation(invocation.Operation)
	input, err := parseAlibabaNLSInput(invocation, protocol)
	if err != nil {
		return InvocationResult{}, err
	}
	appkey, _ := alibabaNLSAppKey(invocation.Parameters)
	token, err := adapter.loadAlibabaNLSToken(ctx)
	if err != nil {
		return InvocationResult{}, err
	}
	target, _ := url.Parse(invocation.URL)
	query := target.Query()
	query.Set("token", token)
	target.RawQuery = query.Encode()
	taskID, err := adapter.newAlibabaNLSID()
	if err != nil {
		return InvocationResult{}, err
	}

	streamContext, cancel := alibabaNLSStreamContext(ctx, invocation, input)
	connection, err := adapter.config.NLSWebSocketDial(streamContext, target.String())
	if err != nil {
		cancel()
		return InvocationResult{}, fmt.Errorf("Alibaba Cloud NLS WebSocket handshake failed")
	}
	eventInvocation := invocation
	if protocol.kind != alibabaNLSAudioInput {
		eventInvocation.ResponseFile = ""
	}
	eventSink, err := newWebSocketOutputSink(eventInvocation, adapter.config.MaxBodyBytes, "Alibaba Cloud NLS WebSocket")
	if err != nil {
		cancel()
		connection.Close()
		return InvocationResult{}, err
	}
	var audioSink *cloudWebSocketOutputSink
	if protocol.kind != alibabaNLSAudioInput {
		audioSink, err = newWebSocketOutputSink(invocation, adapter.config.MaxBodyBytes, "Alibaba Cloud NLS WebSocket audio")
		if err != nil {
			cancel()
			connection.Close()
			eventSink.abort()
			return InvocationResult{}, err
		}
	}
	ready := make(chan error, 1)
	readResult := make(chan alibabaNLSReadResult, 1)
	readerExited := make(chan struct{})
	go func() {
		defer close(readerExited)
		readAlibabaNLSWebSocket(streamContext, connection, protocol, taskID, eventSink, audioSink, ready, readResult)
	}()
	defer func() {
		cancel()
		_ = connection.Close()
		<-readerExited
		eventSink.abort()
		if audioSink != nil {
			audioSink.abort()
		}
	}()

	start, err := adapter.alibabaNLSCommand(protocol.namespace, protocol.start, taskID, appkey, input.start)
	if err != nil || connection.Write(streamContext, cloudWebSocketMessageText, start) != nil {
		return InvocationResult{}, fmt.Errorf("write Alibaba Cloud NLS start command")
	}
	if err := <-ready; err != nil {
		return InvocationResult{}, err
	}
	switch protocol.kind {
	case alibabaNLSAudioInput:
		if err := streamAlibabaNLSAudio(streamContext, connection, invocation, input.start, adapter.config.StreamPause); err != nil {
			return InvocationResult{}, err
		}
	case alibabaNLSFlowingTTS:
		if err := adapter.streamAlibabaNLSText(streamContext, connection, protocol, taskID, appkey, input.texts, invocation.StreamIntervalMS); err != nil {
			return InvocationResult{}, err
		}
	}
	if protocol.stop != "" {
		stop, commandErr := adapter.alibabaNLSCommand(protocol.namespace, protocol.stop, taskID, appkey, nil)
		if commandErr != nil || connection.Write(streamContext, cloudWebSocketMessageText, stop) != nil {
			return InvocationResult{}, fmt.Errorf("write Alibaba Cloud NLS stop command")
		}
	}
	completed := <-readResult
	if completed.err != nil {
		return InvocationResult{}, completed.err
	}
	if protocol.kind == alibabaNLSAudioInput {
		output, err := eventSink.finish(completed.requestID)
		return InvocationResult{Output: output, RequestID: completed.requestID}, err
	}
	return finishAlibabaNLSSynthesis(eventSink, audioSink, input.start, completed.requestID)
}

func (adapter *AlibabaRESTAdapter) newAlibabaNLSID() (string, error) {
	value := strings.TrimSpace(adapter.config.NLSID())
	if !alibabaNLSIDPattern.MatchString(value) {
		return "", fmt.Errorf("Alibaba Cloud NLS ID generator returned an invalid 32-character ID")
	}
	return strings.ToLower(value), nil
}

func (adapter *AlibabaRESTAdapter) alibabaNLSCommand(namespace, name, taskID, appkey string, payload map[string]any) ([]byte, error) {
	messageID, err := adapter.newAlibabaNLSID()
	if err != nil {
		return nil, err
	}
	command := map[string]any{"header": map[string]any{"message_id": messageID, "task_id": taskID, "namespace": namespace, "name": name, "appkey": appkey}}
	if payload != nil {
		command["payload"] = payload
	}
	encoded, err := json.Marshal(command)
	if err != nil || len(encoded) > maxRequestPayloadBytes {
		return nil, fmt.Errorf("encode Alibaba Cloud NLS command")
	}
	return encoded, nil
}

func (adapter *AlibabaRESTAdapter) streamAlibabaNLSText(ctx context.Context, connection cloudWebSocketConnection, protocol alibabaNLSProtocol, taskID, appkey string, texts []string, intervalMS int) error {
	for index, text := range texts {
		if index > 0 && intervalMS > 0 {
			if err := adapter.config.StreamPause(ctx, time.Duration(intervalMS)*time.Millisecond); err != nil {
				return fmt.Errorf("pace Alibaba Cloud NLS text stream: %w", err)
			}
		}
		command, err := adapter.alibabaNLSCommand(protocol.namespace, "RunSynthesis", taskID, appkey, map[string]any{"text": text})
		if err != nil || connection.Write(ctx, cloudWebSocketMessageText, command) != nil {
			return fmt.Errorf("write Alibaba Cloud NLS text segment")
		}
	}
	return nil
}

func streamAlibabaNLSAudio(ctx context.Context, connection cloudWebSocketConnection, invocation Invocation, payload map[string]any, pause func(context.Context, time.Duration) error) error {
	audio, err := os.Open(invocation.BodyFile)
	if err != nil {
		return fmt.Errorf("open Alibaba Cloud NLS audio body_file: %w", err)
	}
	defer audio.Close()
	chunkBytes := invocation.StreamChunkBytes
	if chunkBytes == 0 {
		chunkBytes = alibabaNLSDefaultChunkBytes(payload)
	}
	interval := time.Duration(invocation.StreamIntervalMS) * time.Millisecond
	if interval == 0 {
		interval = defaultAlibabaNLSChunkPause
	}
	buffer := make([]byte, chunkBytes)
	for {
		count, readErr := audio.Read(buffer)
		if count > 0 {
			if err := connection.Write(ctx, cloudWebSocketMessageBinary, buffer[:count]); err != nil {
				return fmt.Errorf("write Alibaba Cloud NLS audio frame")
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return fmt.Errorf("read Alibaba Cloud NLS audio body_file: %w", readErr)
		}
		if count > 0 {
			if err := pause(ctx, interval); err != nil {
				return fmt.Errorf("pace Alibaba Cloud NLS audio stream: %w", err)
			}
		}
	}
}

func alibabaNLSDefaultChunkBytes(payload map[string]any) int {
	sampleRate := alibabaNLSInteger(payload["sample_rate"], 16000)
	if sampleRate == 8000 {
		return 1600
	}
	return 3200
}

func readAlibabaNLSWebSocket(ctx context.Context, connection cloudWebSocketConnection, protocol alibabaNLSProtocol, taskID string, eventSink, audioSink *cloudWebSocketOutputSink, ready chan<- error, result chan<- alibabaNLSReadResult) {
	readySent := protocol.started == ""
	if readySent {
		ready <- nil
	}
	fail := func(err error) {
		if !readySent {
			readySent = true
			ready <- err
			return
		}
		result <- alibabaNLSReadResult{requestID: taskID, err: err}
	}
	for {
		messageType, data, err := connection.Read(ctx)
		if err != nil {
			fail(fmt.Errorf("read Alibaba Cloud NLS WebSocket response"))
			return
		}
		if messageType == cloudWebSocketMessageBinary {
			if !readySent || audioSink == nil || len(data) == 0 || len(data) > maxRequestPayloadBytes {
				fail(fmt.Errorf("Alibaba Cloud NLS WebSocket returned an invalid binary frame"))
				return
			}
			if err := audioSink.writeBinary(data); err != nil {
				fail(err)
				return
			}
			continue
		}
		if messageType != cloudWebSocketMessageText || len(data) == 0 || len(data) > maxRequestPayloadBytes {
			fail(fmt.Errorf("Alibaba Cloud NLS WebSocket returned an invalid event frame"))
			return
		}
		var event alibabaNLSEvent
		if err := json.Unmarshal(data, &event); err != nil || event.Header.MessageID == "" || event.Header.TaskID != taskID || event.Header.Name == "" {
			fail(fmt.Errorf("Alibaba Cloud NLS WebSocket returned an invalid event"))
			return
		}
		if event.Header.Status != alibabaNLSSuccessStatus || event.Header.Name == "TaskFailed" {
			fail(fmt.Errorf("Alibaba Cloud NLS WebSocket task failed with status %d", event.Header.Status))
			return
		}
		if event.Header.Namespace != protocol.namespace {
			fail(fmt.Errorf("Alibaba Cloud NLS WebSocket returned an event for another namespace"))
			return
		}
		if err := eventSink.writeMessage(data); err != nil {
			fail(err)
			return
		}
		if event.Header.Name == protocol.started && !readySent {
			readySent = true
			ready <- nil
		}
		if event.Header.Name == protocol.completed {
			if !readySent {
				fail(fmt.Errorf("Alibaba Cloud NLS WebSocket completed before the start acknowledgement"))
				return
			}
			result <- alibabaNLSReadResult{requestID: taskID}
			return
		}
	}
}

func finishAlibabaNLSSynthesis(eventSink, audioSink *cloudWebSocketOutputSink, start map[string]any, requestID string) (InvocationResult, error) {
	eventsNDJSON, err := eventSink.finish(requestID)
	if err != nil {
		return InvocationResult{}, err
	}
	format := strings.ToLower(strings.TrimSpace(alibabaNLSString(start["format"], "pcm")))
	sampleRate := uint32(alibabaNLSInteger(start["sample_rate"], 16000))
	metadataJSON, err := audioSink.finishAudio(requestID, format, sampleRate)
	if err != nil {
		return InvocationResult{}, err
	}
	var metadata map[string]any
	if err := json.Unmarshal(metadataJSON, &metadata); err != nil {
		return InvocationResult{}, fmt.Errorf("decode Alibaba Cloud NLS audio metadata")
	}
	events := make([]json.RawMessage, 0)
	for _, line := range bytes.Split(eventsNDJSON, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) != 0 {
			events = append(events, append(json.RawMessage(nil), line...))
		}
	}
	metadata["events"] = events
	output, err := json.Marshal(metadata)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("encode Alibaba Cloud NLS synthesis result")
	}
	return InvocationResult{Output: output, RequestID: requestID}, nil
}

func alibabaNLSString(value any, fallback string) string {
	if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
		return text
	}
	return fallback
}

func alibabaNLSInteger(value any, fallback int) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, err := strconv.Atoi(string(typed))
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func alibabaNLSStreamContext(ctx context.Context, invocation Invocation, input alibabaNLSInput) (context.Context, context.CancelFunc) {
	duration := 2 * time.Minute
	if invocation.BodyFile != "" {
		chunkBytes := invocation.StreamChunkBytes
		if chunkBytes == 0 {
			chunkBytes = alibabaNLSDefaultChunkBytes(input.start)
		}
		interval := invocation.StreamIntervalMS
		if interval == 0 {
			interval = int(defaultAlibabaNLSChunkPause / time.Millisecond)
		}
		if info, err := os.Stat(invocation.BodyFile); err == nil && info.Size() > 0 {
			chunks := (info.Size() + int64(chunkBytes) - 1) / int64(chunkBytes)
			duration += time.Duration(chunks) * time.Duration(interval) * time.Millisecond
		}
	} else if invocation.StreamIntervalMS > 0 {
		duration += time.Duration(len(input.texts)*invocation.StreamIntervalMS) * time.Millisecond
	}
	if duration > time.Hour {
		duration = time.Hour
	}
	return context.WithTimeout(ctx, duration)
}

func (adapter *AlibabaRESTAdapter) loadAlibabaNLSToken(ctx context.Context) (string, error) {
	adapter.nlsTokenMu.Lock()
	defer adapter.nlsTokenMu.Unlock()
	now := adapter.config.Now().UTC()
	if adapter.nlsToken != "" && adapter.nlsTokenExpiry.After(now.Add(alibabaNLSTokenRefreshSkew)) {
		return adapter.nlsToken, nil
	}
	credentials, err := adapter.config.Credentials.Credentials(ctx)
	if err != nil {
		return "", err
	}
	if credentials.AccessKeyID == "" || credentials.AccessKeySecret == "" {
		return "", fmt.Errorf("Alibaba Cloud credential provider returned incomplete AKSK material")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, alibabaNLSTokenEndpoint+"?Format=JSON&RegionId=ap-southeast-1", nil)
	if err != nil {
		return "", fmt.Errorf("build Alibaba Cloud NLS token request")
	}
	invocation := Invocation{Operation: "CreateToken", APIVersion: alibabaNLSTokenAPIVersion}
	if err := signAlibabaRPCV2(request, credentials, invocation, now, adapter.config.Nonce()); err != nil {
		return "", fmt.Errorf("sign Alibaba Cloud NLS token request: %w", err)
	}
	response, err := adapter.config.HTTP.Do(request)
	if err != nil {
		return "", fmt.Errorf("request Alibaba Cloud NLS token")
	}
	if response == nil || response.Body == nil {
		return "", fmt.Errorf("Alibaba Cloud NLS token endpoint returned an empty response")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, adapter.config.MaxBodyBytes+1))
	if err != nil || int64(len(data)) > adapter.config.MaxBodyBytes {
		return "", fmt.Errorf("read Alibaba Cloud NLS token response")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("Alibaba Cloud NLS token endpoint returned HTTP %d", response.StatusCode)
	}
	var payload struct {
		Token struct {
			ID         string `json:"Id"`
			ExpireTime int64  `json:"ExpireTime"`
		} `json:"Token"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", fmt.Errorf("Alibaba Cloud NLS token endpoint returned invalid JSON")
	}
	token := strings.TrimSpace(payload.Token.ID)
	expiry := time.Unix(payload.Token.ExpireTime, 0).UTC()
	if token == "" || len(token) > 8192 || strings.ContainsAny(token, "\r\n\x00") || !expiry.After(now.Add(alibabaNLSTokenMinimumLifetime)) {
		return "", fmt.Errorf("Alibaba Cloud NLS token endpoint returned an invalid token")
	}
	adapter.nlsToken = token
	adapter.nlsTokenExpiry = expiry
	return token, nil
}

func defaultAlibabaNLSWebSocketDial(ctx context.Context, target string) (cloudWebSocketConnection, error) {
	client := &http.Client{Transport: http.DefaultTransport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	connection, response, err := websocket.Dial(ctx, target, &websocket.DialOptions{HTTPClient: client, CompressionMode: websocket.CompressionDisabled})
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("Alibaba Cloud NLS WebSocket handshake failed with HTTP %d", response.StatusCode)
		}
		return nil, fmt.Errorf("Alibaba Cloud NLS WebSocket handshake failed")
	}
	connection.SetReadLimit(maxRequestPayloadBytes)
	return coderTencentWebSocketConnection{connection: connection}, nil
}

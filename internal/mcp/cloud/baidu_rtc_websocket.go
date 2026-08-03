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
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/coder/websocket"
)

const (
	authSchemeBaiduRTCAgentWS = "rtc-aiagent-ws"
	baiduRTCControlEndpoint   = "https://rtc-aiagent.baidubce.com"
	baiduRTCWebSocketTarget   = "wss://rtc-aiotgw.exp.bcelive.com/v1/realtime"
	baiduRTCAudioChunkBytes   = 640
	baiduRTCAudioInterval     = 20 * time.Millisecond
)

var baiduRTCInstanceIDPattern = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)

type baiduRTCAgentPlan struct {
	AppID         string
	InstanceType  string
	Config        map[string]any
	DeviceID      string
	UserID        string
	Messages      []string
	MaxMessages   int
	Timeout       time.Duration
	TerminalEvent string
}

type baiduRTCCreateResponse struct {
	InstanceID   json.Number `json:"ai_agent_instance_id"`
	InstanceType string      `json:"instance_type"`
	Context      struct {
		CID   json.Number `json:"cid"`
		Token string      `json:"token"`
	} `json:"context"`
}

func validateBaiduRTCAgentWebSocketInvocation(invocation Invocation) error {
	if !strings.EqualFold(strings.TrimSpace(invocation.Method), http.MethodGet) {
		return fmt.Errorf("Baidu RTC AI Agent WebSocket requires method GET for the HTTP upgrade")
	}
	target, err := url.Parse(invocation.URL)
	if err != nil || target.String() != baiduRTCWebSocketTarget || target.User != nil || target.Fragment != "" || target.RawQuery != "" {
		return fmt.Errorf("Baidu RTC AI Agent requires the exact credential-free %s target", baiduRTCWebSocketTarget)
	}
	if invocation.Service != "rtc-aiagent" || invocation.Operation != "RealtimeInteraction" {
		return fmt.Errorf("Baidu RTC AI Agent requires service rtc-aiagent and operation RealtimeInteraction")
	}
	if invocation.APIVersion != "1" {
		return fmt.Errorf("Baidu RTC AI Agent requires api_version 1")
	}
	if invocation.AuthVersion != "" && invocation.AuthVersion != "v1" {
		return fmt.Errorf("Baidu RTC AI Agent control plane requires BCE auth_version v1")
	}
	if len(invocation.Parameters) != 0 || len(invocation.Headers) != 0 {
		return fmt.Errorf("Baidu RTC AI Agent does not accept caller query parameters or handshake headers")
	}
	if invocation.ResponseFile == "" {
		return fmt.Errorf("Baidu RTC AI Agent requires response_file for atomic NDJSON output")
	}
	if invocation.Body == nil {
		return fmt.Errorf("Baidu RTC AI Agent requires an inline protocol plan")
	}
	plan, err := parseBaiduRTCAgentPlan(invocation.Body)
	if err != nil {
		return err
	}
	if len(plan.Messages) == 0 && invocation.BodyFile == "" {
		return fmt.Errorf("Baidu RTC AI Agent requires at least one text message or body_file audio stream")
	}
	if invocation.BodyFile != "" {
		info, statErr := os.Stat(invocation.BodyFile)
		if statErr != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxRequestFileBytes || info.Size()%baiduRTCAudioChunkBytes != 0 {
			return fmt.Errorf("Baidu RTC raw16k body_file must be a non-empty regular file of complete 640-byte frames below %d bytes", maxRequestFileBytes)
		}
		audioDuration := time.Duration(info.Size()/baiduRTCAudioChunkBytes) * baiduRTCAudioInterval
		if audioDuration >= plan.Timeout {
			return fmt.Errorf("Baidu RTC raw16k audio duration must be shorter than timeout_seconds")
		}
		if invocation.StreamChunkBytes != 0 && invocation.StreamChunkBytes != baiduRTCAudioChunkBytes {
			return fmt.Errorf("Baidu RTC raw16k audio requires stream_chunk_bytes 640 when provided")
		}
		if invocation.StreamIntervalMS != 0 && invocation.StreamIntervalMS != int(baiduRTCAudioInterval/time.Millisecond) {
			return fmt.Errorf("Baidu RTC raw16k audio requires stream_interval_ms 20 when provided")
		}
	} else if invocation.StreamChunkBytes != 0 || invocation.StreamIntervalMS != 0 {
		return fmt.Errorf("Baidu RTC stream controls require body_file audio")
	}
	if invocation.Region != "" || invocation.Project != "" || invocation.Subscription != "" || invocation.Audience != "" || invocation.RegionSet != "" || invocation.PayloadMode != "" || invocation.ChecksumAlgorithm != "" || invocation.ProtobufDescriptorFile != "" || invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return fmt.Errorf("Baidu RTC AI Agent does not accept unrelated provider or transport controls")
	}
	return nil
}

func parseBaiduRTCAgentPlan(body any) (baiduRTCAgentPlan, error) {
	if baiduRTCAgentContainsCredentialField(body) {
		return baiduRTCAgentPlan{}, fmt.Errorf("Baidu RTC AI Agent requires a credential-free protocol plan")
	}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) > maxRequestPayloadBytes {
		return baiduRTCAgentPlan{}, fmt.Errorf("Baidu RTC AI Agent plan must be bounded JSON")
	}
	var raw struct {
		AppID         string         `json:"app_id"`
		InstanceType  string         `json:"instance_type"`
		Config        map[string]any `json:"config"`
		DeviceID      string         `json:"device_id"`
		UserID        string         `json:"user_id"`
		Messages      []string       `json:"messages"`
		MaxMessages   int            `json:"max_messages"`
		Timeout       int            `json:"timeout_seconds"`
		TerminalEvent string         `json:"terminal_event"`
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return baiduRTCAgentPlan{}, fmt.Errorf("Baidu RTC AI Agent plan is invalid: %w", err)
	}
	if !identifierPattern.MatchString(raw.AppID) {
		return baiduRTCAgentPlan{}, fmt.Errorf("Baidu RTC AI Agent app_id must be a safe identifier")
	}
	if raw.InstanceType == "" {
		raw.InstanceType = "VoiceChat"
	}
	switch raw.InstanceType {
	case "VoiceChat", "DigitalHuman", "RealtimeTranslation", "ClawChat":
	default:
		return baiduRTCAgentPlan{}, fmt.Errorf("Baidu RTC AI Agent instance_type must be VoiceChat, DigitalHuman, RealtimeTranslation, or ClawChat")
	}
	if !validBaiduRTCIdentity(raw.DeviceID) || !validBaiduRTCIdentity(raw.UserID) {
		return baiduRTCAgentPlan{}, fmt.Errorf("Baidu RTC AI Agent requires bounded device_id and user_id values for license activation")
	}
	if baiduRTCConfigContainsCredentialMaterial(raw.Config) {
		return baiduRTCAgentPlan{}, fmt.Errorf("Baidu RTC AI Agent config contains embedded credential material")
	}
	if codec, present := raw.Config["rtc_ac"]; present {
		codecString, ok := codec.(string)
		if !ok || codecString != "raw16k" {
			return baiduRTCAgentPlan{}, fmt.Errorf("Baidu RTC AI Agent config.rtc_ac must be raw16k")
		}
	}
	if len(raw.Messages) > 64 {
		return baiduRTCAgentPlan{}, fmt.Errorf("Baidu RTC AI Agent accepts at most 64 text messages")
	}
	for _, message := range raw.Messages {
		if !strings.HasPrefix(message, "[T]:") || len(message) <= len("[T]:") || len(message) > 16*1024 || !validBaiduRTCText(message) {
			return baiduRTCAgentPlan{}, fmt.Errorf("Baidu RTC AI Agent client messages must be bounded UTF-8 [T]: queries")
		}
	}
	if raw.MaxMessages < 1 || raw.MaxMessages > 256 {
		return baiduRTCAgentPlan{}, fmt.Errorf("Baidu RTC AI Agent max_messages must be between 1 and 256")
	}
	if raw.Timeout < 1 || raw.Timeout > 300 {
		return baiduRTCAgentPlan{}, fmt.Errorf("Baidu RTC AI Agent timeout_seconds must be between 1 and 300")
	}
	if raw.TerminalEvent != "tts_end" && raw.TerminalEvent != "answer" && raw.TerminalEvent != "message_limit" {
		return baiduRTCAgentPlan{}, fmt.Errorf("Baidu RTC AI Agent terminal_event must be tts_end, answer, or message_limit")
	}
	return baiduRTCAgentPlan{
		AppID: raw.AppID, InstanceType: raw.InstanceType, Config: raw.Config,
		DeviceID: raw.DeviceID, UserID: raw.UserID, Messages: raw.Messages,
		MaxMessages: raw.MaxMessages, Timeout: time.Duration(raw.Timeout) * time.Second,
		TerminalEvent: raw.TerminalEvent,
	}, nil
}

func baiduRTCAgentContainsCredentialField(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for name, child := range typed {
			normalized := normalizedOperation(name)
			switch normalized {
			case "authorization", "apikey", "accesstoken", "bearertoken", "clientsecret", "secretaccesskey", "accesskeyid", "sessiontoken", "securitytoken", "llmtoken", "token", "ak", "sk", "lickey", "licensekey", "password", "credential":
				return true
			}
			if baiduRTCAgentContainsCredentialField(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if baiduRTCAgentContainsCredentialField(child) {
				return true
			}
		}
	}
	return false
}

func baiduRTCConfigContainsCredentialMaterial(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for _, child := range typed {
			if baiduRTCConfigContainsCredentialMaterial(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if baiduRTCConfigContainsCredentialMaterial(child) {
				return true
			}
		}
	case string:
		lower := strings.ToLower(typed)
		for _, marker := range []string{"authorization", "bearer ", "api_key", "apikey", "access_token", "llm_token", "client_secret", "secret_access_key", "security_token", "session_token", "lickey", "license_key"} {
			if strings.Contains(lower, marker) {
				return true
			}
		}
	}
	return false
}

func validBaiduRTCText(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) && character != '\t' {
			return false
		}
	}
	return true
}

func validBaiduRTCIdentity(value string) bool {
	return len(value) >= 1 && len(value) <= 256 && validBaiduRTCText(value)
}

func defaultBaiduRTCWebSocketDial(ctx context.Context, target string) (cloudWebSocketConnection, error) {
	client := &http.Client{
		Transport:     http.DefaultTransport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	connection, response, err := websocket.Dial(ctx, target, &websocket.DialOptions{
		HTTPClient: client, CompressionMode: websocket.CompressionDisabled,
	})
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("Baidu RTC AI Agent WebSocket handshake failed with HTTP %d", response.StatusCode)
		}
		return nil, fmt.Errorf("Baidu RTC AI Agent WebSocket handshake failed")
	}
	connection.SetReadLimit(maxRequestPayloadBytes)
	return coderTencentWebSocketConnection{connection: connection}, nil
}

func invokeBaiduRTCAgentWebSocket(ctx context.Context, adapter *BaiduRESTAdapter, credentials BCECredentials, invocation Invocation) (result InvocationResult, err error) {
	if err = validateBaiduRTCAgentWebSocketInvocation(invocation); err != nil {
		return InvocationResult{}, err
	}
	plan, _ := parseBaiduRTCAgentPlan(invocation.Body)
	sink, sinkErr := newWebSocketOutputSink(invocation, adapter.config.MaxBodyBytes, "Baidu RTC AI Agent WebSocket")
	if sinkErr != nil {
		return InvocationResult{}, sinkErr
	}
	defer sink.abort()
	createRequest := map[string]any{"app_id": plan.AppID, "instance_type": plan.InstanceType}
	config := make(map[string]any, len(plan.Config)+1)
	for name, value := range plan.Config {
		config[name] = value
	}
	config["rtc_ac"] = "raw16k"
	configJSON, marshalErr := json.Marshal(config)
	if marshalErr != nil {
		return InvocationResult{}, fmt.Errorf("encode Baidu RTC AI Agent config")
	}
	createRequest["config"] = string(configJSON)
	createBytes, createRequestID, controlErr := adapter.baiduRTCControlRequest(ctx, credentials, "/api/v1/aiagent/generateAIAgentCall", createRequest)
	if controlErr != nil {
		return InvocationResult{}, controlErr
	}
	var created baiduRTCCreateResponse
	decoder := json.NewDecoder(bytes.NewReader(createBytes))
	decoder.UseNumber()
	decodeErr := decoder.Decode(&created)
	var trailing any
	trailingErr := decoder.Decode(&trailing)
	if decodeErr != nil || trailingErr != io.EOF || !baiduRTCInstanceIDPattern.MatchString(string(created.InstanceID)) {
		return InvocationResult{}, fmt.Errorf("Baidu RTC AI Agent create response is missing a valid instance ID")
	}
	stopBody := map[string]any{"app_id": plan.AppID, "ai_agent_instance_id": created.InstanceID}
	stopAttempted := false
	stopped := false
	defer func() {
		if stopped || stopAttempted {
			return
		}
		stopAttempted = true
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_, _, stopErr := adapter.baiduRTCControlRequest(cleanupCtx, credentials, "/api/v1/aiagent/stopAIAgentInstance", stopBody)
		if stopErr != nil {
			if err == nil {
				err = fmt.Errorf("stop Baidu RTC AI Agent instance: %w", stopErr)
			} else {
				err = fmt.Errorf("%w; cleanup stop failed: %v", err, stopErr)
			}
		}
	}()
	if !validBaiduRTCSecret(created.Context.Token) {
		return InvocationResult{}, fmt.Errorf("Baidu RTC AI Agent create response is missing a valid internal token")
	}

	webSocketURL, buildErr := baiduRTCInternalWebSocketURL(plan.AppID, string(created.InstanceID), created.Context.Token)
	if buildErr != nil {
		return InvocationResult{}, buildErr
	}
	dialCtx, cancelDial := context.WithTimeout(ctx, adapter.config.Timeout)
	connection, dialErr := adapter.config.RTCWebSocketDial(dialCtx, webSocketURL)
	cancelDial()
	if dialErr != nil {
		return InvocationResult{}, dialErr
	}
	defer connection.Close()
	sessionCtx, cancelSession := context.WithTimeout(ctx, plan.Timeout)
	defer cancelSession()
	if err = activateBaiduRTCLicense(sessionCtx, connection, adapter.config.RTCLicenseKey, plan.DeviceID, plan.UserID); err != nil {
		return InvocationResult{}, err
	}
	secretValues := []string{credentials.AccessKeyID, credentials.SecretAccessKey, credentials.SessionToken, created.Context.Token, adapter.config.RTCLicenseKey}
	sendResult := make(chan error, 1)
	readResult := make(chan error, 1)
	go func() {
		sendResult <- sendBaiduRTCInputs(sessionCtx, adapter, connection, plan.Messages, invocation.BodyFile)
	}()
	go func() {
		readResult <- readBaiduRTCResponses(sessionCtx, connection, sink, plan, secretValues)
	}()
	var sendErr, readErr error
	var primaryErr error
	for sendResult != nil || readResult != nil {
		select {
		case sendErr = <-sendResult:
			sendResult = nil
			if sendErr != nil {
				if primaryErr == nil {
					primaryErr = sendErr
				}
				cancelSession()
			}
		case readErr = <-readResult:
			readResult = nil
			if readErr != nil {
				if primaryErr == nil {
					primaryErr = readErr
				}
				cancelSession()
			}
		}
	}
	if primaryErr != nil {
		return InvocationResult{}, primaryErr
	}
	stopAttempted = true
	if _, _, err = adapter.baiduRTCControlRequest(ctx, credentials, "/api/v1/aiagent/stopAIAgentInstance", stopBody); err != nil {
		return InvocationResult{}, fmt.Errorf("stop Baidu RTC AI Agent instance: %w", err)
	}
	stopped = true
	output, finishErr := sink.finish(createRequestID)
	if finishErr != nil {
		return InvocationResult{}, finishErr
	}
	return InvocationResult{Output: output, RequestID: createRequestID}, nil
}

func sendBaiduRTCInputs(ctx context.Context, adapter *BaiduRESTAdapter, connection cloudWebSocketConnection, messages []string, bodyFile string) error {
	for _, message := range messages {
		if err := connection.Write(ctx, cloudWebSocketMessageText, []byte(message)); err != nil {
			return fmt.Errorf("write Baidu RTC AI Agent text query")
		}
	}
	if bodyFile != "" {
		return streamBaiduRTCAudio(ctx, adapter, connection, bodyFile)
	}
	return nil
}

func readBaiduRTCResponses(ctx context.Context, connection cloudWebSocketConnection, sink *cloudWebSocketOutputSink, plan baiduRTCAgentPlan, secrets []string) error {
	type outputFrame struct {
		Type       string `json:"type"`
		Data       string `json:"data,omitempty"`
		DataBase64 string `json:"data_base64,omitempty"`
	}
	for count := 0; count < plan.MaxMessages; count++ {
		messageType, data, err := connection.Read(ctx)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("Baidu RTC AI Agent session timed out before %s", plan.TerminalEvent)
			}
			return fmt.Errorf("read Baidu RTC AI Agent response")
		}
		if len(data) == 0 || len(data) > maxRequestPayloadBytes || baiduRTCLeaksSecret(data, secrets) {
			return fmt.Errorf("Baidu RTC AI Agent returned an unsafe or oversized frame")
		}
		var output []byte
		terminal := false
		if messageType == cloudWebSocketMessageText {
			if !validBaiduRTCText(string(data)) || strings.HasPrefix(string(data), "[E]:[LIC]:") {
				return fmt.Errorf("Baidu RTC AI Agent returned an invalid protocol event")
			}
			output, _ = json.Marshal(outputFrame{Type: "text", Data: string(data)})
			terminal = baiduRTCTerminal(plan.TerminalEvent, string(data), count+1, plan.MaxMessages)
		} else if messageType == cloudWebSocketMessageBinary {
			output, _ = json.Marshal(outputFrame{Type: "binary", DataBase64: base64.StdEncoding.EncodeToString(data)})
			terminal = plan.TerminalEvent == "message_limit" && count+1 == plan.MaxMessages
		} else {
			return fmt.Errorf("Baidu RTC AI Agent returned an unsupported frame type")
		}
		if err := sink.writeMessage(output); err != nil {
			return err
		}
		if terminal {
			return nil
		}
	}
	return fmt.Errorf("Baidu RTC AI Agent response ended without the requested terminal event")
}

func (adapter *BaiduRESTAdapter) baiduRTCControlRequest(ctx context.Context, credentials BCECredentials, path string, body any) ([]byte, string, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, "", fmt.Errorf("encode Baidu RTC AI Agent control request")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baiduRTCControlEndpoint+path, bytes.NewReader(encoded))
	if err != nil {
		return nil, "", fmt.Errorf("build Baidu RTC AI Agent control request")
	}
	request.Header.Set("Content-Type", "application/json")
	if credentials.SessionToken != "" {
		request.Header.Set("x-bce-security-token", credentials.SessionToken)
	}
	authorization, err := signBCERequest(request, credentials, adapter.config.Now().UTC(), bceExpiry)
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("Authorization", authorization)
	response, err := adapter.config.HTTP.Do(request)
	if err != nil {
		return nil, "", fmt.Errorf("Baidu RTC AI Agent control request: %w", err)
	}
	defer response.Body.Close()
	limit := adapter.config.MaxBodyBytes
	if limit <= 0 || limit > maxRequestPayloadBytes {
		limit = maxRequestPayloadBytes
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, "", fmt.Errorf("read Baidu RTC AI Agent control response")
	}
	if int64(len(data)) > limit {
		return nil, "", fmt.Errorf("Baidu RTC AI Agent control response exceeds %d bytes", limit)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", fmt.Errorf("Baidu RTC AI Agent control request returned HTTP %d", response.StatusCode)
	}
	return data, responseRequestID(response.Header), nil
}

func baiduRTCInternalWebSocketURL(appID, instanceID, token string) (string, error) {
	if !identifierPattern.MatchString(appID) || !baiduRTCInstanceIDPattern.MatchString(instanceID) || !validBaiduRTCSecret(token) {
		return "", fmt.Errorf("Baidu RTC AI Agent returned invalid internal connection material")
	}
	target, _ := url.Parse(baiduRTCWebSocketTarget)
	query := url.Values{}
	query.Set("a", appID)
	query.Set("id", instanceID)
	query.Set("t", token)
	query.Set("ac", "raw16k")
	target.RawQuery = query.Encode()
	return target.String(), nil
}

func validBaiduRTCSecret(value string) bool {
	return len(value) >= 8 && len(value) <= 4096 && validBaiduRTCText(value)
}

func activateBaiduRTCLicense(ctx context.Context, connection cloudWebSocketConnection, licenseKey, deviceID, userID string) error {
	messageType, data, err := connection.Read(ctx)
	if err != nil || messageType != cloudWebSocketMessageText || !strings.HasPrefix(string(data), "[E]:[LIC]:[MUST]:") {
		return fmt.Errorf("Baidu RTC AI Agent did not request the required license activation")
	}
	if !validBaiduRTCSecret(licenseKey) {
		return fmt.Errorf("Baidu RTC AI Agent license unavailable: set BCE_RTC_LICENSE_KEY on the MCP server")
	}
	payload, _ := json.Marshal(map[string]string{"devId": deviceID, "uId": userID, "licKey": licenseKey})
	activation := append([]byte("[E]:[LIC]:[ACTIVE]:"), payload...)
	if err := connection.Write(ctx, cloudWebSocketMessageText, activation); err != nil {
		return fmt.Errorf("write Baidu RTC AI Agent license activation")
	}
	messageType, data, err = connection.Read(ctx)
	if err != nil || messageType != cloudWebSocketMessageText || !strings.HasPrefix(string(data), "[E]:[LIC]:[RES]:[PASS]:") {
		return fmt.Errorf("Baidu RTC AI Agent license activation failed")
	}
	return nil
}

func streamBaiduRTCAudio(ctx context.Context, adapter *BaiduRESTAdapter, connection cloudWebSocketConnection, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open Baidu RTC raw16k audio: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxRequestFileBytes || info.Size()%baiduRTCAudioChunkBytes != 0 {
		return fmt.Errorf("Baidu RTC raw16k body_file must be a non-empty regular file of complete 640-byte frames below %d bytes", maxRequestFileBytes)
	}
	buffer := make([]byte, baiduRTCAudioChunkBytes)
	for {
		read, readErr := io.ReadFull(file, buffer)
		if readErr == io.EOF {
			return nil
		}
		if readErr == io.ErrUnexpectedEOF {
			return fmt.Errorf("Baidu RTC raw16k audio must contain complete 640-byte/20-ms frames")
		}
		if readErr != nil {
			return fmt.Errorf("read Baidu RTC raw16k audio: %w", readErr)
		}
		if writeErr := connection.Write(ctx, cloudWebSocketMessageBinary, buffer[:read]); writeErr != nil {
			return fmt.Errorf("write Baidu RTC raw16k audio")
		}
		if pauseErr := adapter.config.StreamPause(ctx, baiduRTCAudioInterval); pauseErr != nil {
			return fmt.Errorf("pace Baidu RTC raw16k audio: %w", pauseErr)
		}
	}
}

func baiduRTCTerminal(terminalEvent, event string, count, maxMessages int) bool {
	switch terminalEvent {
	case "tts_end":
		return event == "[E]:[TTS_END_SPEAKING]"
	case "answer":
		return strings.HasPrefix(event, "[A]:") && len(event) > len("[A]:") && !strings.HasPrefix(event, "[A]:[")
	case "message_limit":
		return count == maxMessages
	default:
		return false
	}
}

func baiduRTCLeaksSecret(data []byte, secrets []string) bool {
	for _, secret := range secrets {
		if len(secret) >= 8 && bytes.Contains(data, []byte(secret)) {
			return true
		}
	}
	return false
}

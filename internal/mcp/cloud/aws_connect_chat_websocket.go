package cloud

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
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

	aws "github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/coder/websocket"
)

const (
	authSchemeAWSConnectChatWS = "connect-chat-ws"

	awsConnectChatService            = "connect"
	awsConnectChatParticipantService = "connectparticipant"
	awsConnectChatObserveOperation   = "observechat"
	awsConnectChatMaxEvents          = 256
	awsConnectChatMaxTimeoutSeconds  = 300
	awsConnectChatMaxResponseBytes   = 64 * 1024
	awsConnectChatMaxMessageBytes    = 64 * 1024
	awsConnectChatMaxAttributes      = 16
	awsConnectChatMaxAttributeKey    = 64
	awsConnectChatMaxAttributeValue  = 4096
	awsConnectChatMaxMessages        = 16
	awsConnectChatMaxDisplayName     = 256
	awsConnectChatMaxInitialMessage  = 64 * 1024
	awsConnectChatMaxWebSocketURL    = 8 * 1024
	awsConnectChatEndpointPath       = "/participant/connect"
	awsConnectChatSubscribeTopic     = "aws/subscribe"
	awsConnectChatHeartbeatTopic     = "aws/heartbeat"
	awsConnectChatPingTopic          = "aws/ping"
	awsConnectChatChatTopic          = "aws/chat"
)

var (
	awsConnectChatInstancePattern  = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	awsConnectChatContactFlowARN   = regexp.MustCompile(`^arn:aws:connect:[a-z0-9-]+:[0-9]{12}:contact-flow/[A-Za-z0-9][A-Za-z0-9-]{0,63}$`)
	awsConnectChatContactFlowID    = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	awsConnectChatBoundedName      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._'-]{0,255}$`)
	awsConnectChatParticipantRoles = map[string]bool{
		"AGENT": true, "CUSTOMER": true, "CUSTOM_BOT": true, "SUPERVISOR": true, "SYSTEM": true,
	}
	awsConnectChatEventTypes = map[string]bool{
		"ATTACHMENT": true, "CHAT_ENDED": true, "CONNECTION_ACK": true, "EVENT": true,
		"MESSAGE": true, "MESSAGE_DELIVERED": true, "MESSAGE_READ": true,
		"PARTICIPANT_JOINED": true, "PARTICIPANT_LEFT": true,
		"TRANSFER_FAILED": true, "TRANSFER_SUCCEEDED": true, "TYPING": true,
	}
)

type awsConnectChatPlan struct {
	InstanceID     string                  `json:"instance_id"`
	ContactFlowID  string                  `json:"contact_flow_id"`
	DisplayName    string                  `json:"display_name"`
	InitialMessage string                  `json:"initial_message,omitempty"`
	Attributes     map[string]string       `json:"attributes,omitempty"`
	Messages       []awsConnectChatMessage `json:"messages,omitempty"`
	MaxEvents      int                     `json:"max_events"`
	TimeoutSeconds int                     `json:"timeout_seconds"`
}

type awsConnectChatMessage struct {
	Content     string `json:"content"`
	ContentType string `json:"content_type,omitempty"`
}

func validateAWSConnectChatInvocation(invocation Invocation) error {
	if !strings.EqualFold(invocation.Method, http.MethodPost) || !strings.EqualFold(invocation.Service, "connect") || normalizedOperation(invocation.Operation) != awsConnectChatObserveOperation {
		return fmt.Errorf("AWS Connect chat WebSocket requires POST, service connect, and operation ObserveChat")
	}
	if !identifierPattern.MatchString(invocation.Region) {
		return fmt.Errorf("AWS Connect chat requires a valid region")
	}
	if err := validateAWSConnectChatTarget(invocation.URL, invocation.Region); err != nil {
		return err
	}
	if len(invocation.Parameters) != 0 || len(invocation.Headers) != 0 || invocation.BodyFile != "" || invocation.ResponseFile == "" {
		return fmt.Errorf("AWS Connect chat WebSocket requires only a protocol body and response_file; query, headers, and body_file are forbidden")
	}
	if invocation.RegionSet != "" || invocation.Project != "" || invocation.Subscription != "" || invocation.Audience != "" || invocation.AuthVersion != "" || invocation.APIVersion != "" || invocation.PayloadMode != "" || invocation.ChecksumAlgorithm != "" || invocation.StreamChunkBytes != 0 || invocation.StreamIntervalMS != 0 || invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return fmt.Errorf("AWS Connect chat WebSocket does not accept cross-provider, REST payload, checksum, SigV4a, or generic stream controls")
	}
	_, err := parseAWSConnectChatPlan(invocation.Body)
	return err
}

func validateAWSConnectChatTarget(rawURL, region string) error {
	target, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(target.Scheme, "wss") || target.User != nil || target.Fragment != "" || target.Port() != "" || target.RawQuery != "" {
		return fmt.Errorf("AWS Connect chat requires an exact query-free wss:// participant connect URL")
	}
	expectedHost := "participant.connect." + strings.ToLower(strings.TrimSpace(region)) + ".amazonaws.com"
	if !strings.EqualFold(target.Hostname(), expectedHost) {
		return fmt.Errorf("AWS Connect chat requires the exact official participant.connect.<region>.amazonaws.com host")
	}
	if target.EscapedPath() != awsConnectChatEndpointPath {
		return fmt.Errorf("AWS Connect chat requires the official /participant/connect path")
	}
	return nil
}

func parseAWSConnectChatPlan(body any) (awsConnectChatPlan, error) {
	if body == nil || alibabaNLSContainsCredentialField(body) {
		return awsConnectChatPlan{}, fmt.Errorf("AWS Connect chat requires a credential-free protocol body")
	}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) == 0 || len(encoded) > maxRequestPayloadBytes {
		return awsConnectChatPlan{}, fmt.Errorf("AWS Connect chat body must be bounded JSON")
	}
	var plan awsConnectChatPlan
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&plan) != nil || ensureJSONDecoderEOF(decoder) != nil {
		return awsConnectChatPlan{}, fmt.Errorf("AWS Connect chat body does not match the finite session schema")
	}
	if !awsConnectChatInstancePattern.MatchString(plan.InstanceID) {
		return awsConnectChatPlan{}, fmt.Errorf("AWS Connect chat instance_id must be one official instance UUID")
	}
	if !awsConnectChatContactFlowARN.MatchString(plan.ContactFlowID) && !awsConnectChatContactFlowID.MatchString(plan.ContactFlowID) {
		return awsConnectChatPlan{}, fmt.Errorf("AWS Connect chat contact_flow_id must be an official contact flow ARN or UUID")
	}
	if !awsConnectChatBoundedName.MatchString(plan.DisplayName) || len(plan.DisplayName) > awsConnectChatMaxDisplayName {
		return awsConnectChatPlan{}, fmt.Errorf("AWS Connect chat display_name is invalid")
	}
	if plan.InitialMessage != "" && (len(plan.InitialMessage) > awsConnectChatMaxInitialMessage || strings.ContainsAny(plan.InitialMessage, "\x00\r\n")) {
		return awsConnectChatPlan{}, fmt.Errorf("AWS Connect chat initial_message is outside the bounded text bound")
	}
	if len(plan.Attributes) > awsConnectChatMaxAttributes {
		return awsConnectChatPlan{}, fmt.Errorf("AWS Connect chat attributes exceed the bounded count")
	}
	for key, value := range plan.Attributes {
		if key == "" || len(key) > awsConnectChatMaxAttributeKey || strings.ContainsAny(key, "\x00\r\n") ||
			len(value) > awsConnectChatMaxAttributeValue || strings.ContainsAny(value, "\x00\r\n") {
			return awsConnectChatPlan{}, fmt.Errorf("AWS Connect chat attribute %q is invalid", key)
		}
	}
	if len(plan.Messages) > awsConnectChatMaxMessages {
		return awsConnectChatPlan{}, fmt.Errorf("AWS Connect chat messages exceed the bounded count")
	}
	for index := range plan.Messages {
		message := &plan.Messages[index]
		if len(message.Content) == 0 || len(message.Content) > awsConnectChatMaxMessageBytes || strings.ContainsAny(message.Content, "\x00\r\n") {
			return awsConnectChatPlan{}, fmt.Errorf("AWS Connect chat message %d content is outside the bounded text bound", index)
		}
		switch message.ContentType {
		case "", "text/plain", "text/markdown":
		default:
			return awsConnectChatPlan{}, fmt.Errorf("AWS Connect chat message %d content_type must be text/plain or text/markdown", index)
		}
	}
	if plan.MaxEvents < 1 || plan.MaxEvents > awsConnectChatMaxEvents || plan.TimeoutSeconds < 1 || plan.TimeoutSeconds > awsConnectChatMaxTimeoutSeconds {
		return awsConnectChatPlan{}, fmt.Errorf("AWS Connect chat response count or timeout is outside the finite bound")
	}
	return plan, nil
}

func invokeAWSConnectChatWebSocket(ctx context.Context, adapter *AWSRESTAdapter, credentials AWSCredentials, invocation Invocation) (InvocationResult, error) {
	if err := validateAWSConnectChatInvocation(invocation); err != nil {
		return InvocationResult{}, err
	}
	if credentials.AccessKeyID == "" || credentials.SecretAccessKey == "" {
		return InvocationResult{}, fmt.Errorf("AWS Connect chat requires complete AKSK credentials")
	}
	plan, _ := parseAWSConnectChatPlan(invocation.Body)
	region := strings.ToLower(strings.TrimSpace(invocation.Region))
	participantToken, contactID, err := awsConnectChatStartContact(ctx, adapter, credentials, region, plan)
	if err != nil {
		return InvocationResult{}, err
	}
	websocketURL, connectionToken, err := awsConnectChatCreateParticipantConnection(ctx, adapter, region, participantToken)
	if err != nil {
		return InvocationResult{}, err
	}
	if err := validateAWSConnectChatWebSocketURL(websocketURL, region); err != nil {
		return InvocationResult{}, err
	}
	sessionCtx, cancel := context.WithTimeout(ctx, time.Duration(plan.TimeoutSeconds)*time.Second)
	defer cancel()
	connection, err := adapter.config.ConnectChatWebSocketDial(sessionCtx, websocketURL)
	if err != nil {
		return InvocationResult{}, err
	}
	defer connection.Close()
	subscribe, err := json.Marshal(map[string]any{
		"topic":   awsConnectChatSubscribeTopic,
		"content": map[string]any{"topics": []string{awsConnectChatChatTopic}},
	})
	if err != nil {
		return InvocationResult{}, fmt.Errorf("encode AWS Connect chat subscribe frame")
	}
	if err := connection.Write(sessionCtx, cloudWebSocketMessageText, subscribe); err != nil {
		return InvocationResult{}, fmt.Errorf("AWS Connect chat subscribe frame failed")
	}
	sent := 0
	if len(plan.Messages) > 0 {
		for index := range plan.Messages {
			if err := awsConnectChatSendMessage(ctx, adapter, region, connectionToken, plan.Messages[index], index); err != nil {
				return InvocationResult{}, err
			}
			sent++
		}
	}
	sink, err := newWebSocketOutputSink(invocation, adapter.config.MaxBodyBytes, "AWS Connect chat WebSocket")
	if err != nil {
		return InvocationResult{}, err
	}
	defer sink.abort()
	subscribed := false
	received := 0
	for received < plan.MaxEvents {
		messageType, data, err := connection.Read(sessionCtx)
		if errors.Is(err, context.DeadlineExceeded) || err == io.EOF || websocket.CloseStatus(err) == websocket.StatusNormalClosure {
			break
		}
		if err != nil {
			return InvocationResult{}, err
		}
		frame, err := parseAWSConnectChatFrame(data, messageType)
		if err != nil {
			return InvocationResult{}, err
		}
		switch frame.kind {
		case awsConnectChatFrameSubscribeSuccess:
			subscribed = true
		case awsConnectChatFrameIgnored:
			// heartbeat and deep-ping frames are transport-level only.
		case awsConnectChatFrameEvent:
			if err := sink.writeMessage(frame.event); err != nil {
				return InvocationResult{}, err
			}
			received++
		default:
			return InvocationResult{}, fmt.Errorf("AWS Connect chat subscription failed")
		}
	}
	if !subscribed {
		return InvocationResult{}, fmt.Errorf("AWS Connect chat stream ended before the aws/chat subscription succeeded")
	}
	metadata, err := sink.finish("")
	if err != nil {
		return InvocationResult{}, err
	}
	var summary map[string]any
	if json.Unmarshal(metadata, &summary) != nil {
		return InvocationResult{}, fmt.Errorf("decode AWS Connect chat output metadata")
	}
	summary["messages"] = received
	summary["sent_messages"] = sent
	if contactID != "" {
		summary["contact_id"] = contactID
	}
	metadata, err = json.Marshal(summary)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("encode AWS Connect chat output metadata")
	}
	return InvocationResult{Output: metadata}, nil
}

func awsConnectChatStartContact(ctx context.Context, adapter *AWSRESTAdapter, credentials AWSCredentials, region string, plan awsConnectChatPlan) (participantToken, contactID string, err error) {
	body := map[string]any{
		"InstanceId":    plan.InstanceID,
		"ContactFlowId": plan.ContactFlowID,
		"ParticipantDetails": map[string]any{
			"DisplayName": plan.DisplayName,
		},
		"SupportedMessagingContentTypes": []string{"text/plain", "text/markdown"},
	}
	if plan.InitialMessage != "" {
		body["InitialMessage"] = map[string]any{"ContentType": "text/plain", "Content": plan.InitialMessage}
	}
	if len(plan.Attributes) > 0 {
		attributes := make(map[string]any, len(plan.Attributes))
		for key, value := range plan.Attributes {
			attributes[key] = value
		}
		body["Attributes"] = attributes
	}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) == 0 || len(encoded) > maxRequestPayloadBytes {
		return "", "", fmt.Errorf("encode AWS Connect StartChatContact request")
	}
	endpoint := "https://connect." + region + ".amazonaws.com/contact/chat"
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return "", "", fmt.Errorf("build AWS Connect StartChatContact request")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	awsCredentials := aws.Credentials{AccessKeyID: credentials.AccessKeyID, SecretAccessKey: credentials.SecretAccessKey, SessionToken: credentials.SessionToken}
	if err := awsv4.NewSigner().SignHTTP(ctx, awsCredentials, request, sha256Hex(encoded), awsConnectChatService, region, adapter.config.Now().UTC()); err != nil {
		return "", "", fmt.Errorf("sign AWS Connect StartChatContact request")
	}
	response, err := adapter.config.HTTP.Do(request)
	if err != nil {
		return "", "", fmt.Errorf("AWS Connect StartChatContact request failed")
	}
	if response == nil || response.Body == nil {
		return "", "", fmt.Errorf("AWS Connect StartChatContact returned no response")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("AWS Connect StartChatContact failed with HTTP %d", response.StatusCode)
	}
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, awsConnectChatMaxResponseBytes+1))
	if err != nil || len(responseBody) == 0 || len(responseBody) > awsConnectChatMaxResponseBytes {
		return "", "", fmt.Errorf("AWS Connect StartChatContact response was invalid")
	}
	var payload struct {
		ContactID        string `json:"ContactId"`
		ParticipantID    string `json:"ParticipantId"`
		ParticipantToken string `json:"ParticipantToken"`
	}
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || ensureJSONDecoderEOF(decoder) != nil {
		return "", "", fmt.Errorf("AWS Connect StartChatContact response was invalid")
	}
	if payload.ParticipantToken == "" || len(payload.ParticipantToken) > awsConnectChatMaxResponseBytes {
		return "", "", fmt.Errorf("AWS Connect StartChatContact returned no participant token")
	}
	if payload.ContactID != "" && len(payload.ContactID) > 256 {
		return "", "", fmt.Errorf("AWS Connect StartChatContact returned an invalid contact id")
	}
	return payload.ParticipantToken, payload.ContactID, nil
}

func awsConnectChatCreateParticipantConnection(ctx context.Context, adapter *AWSRESTAdapter, region, participantToken string) (websocketURL, connectionToken string, err error) {
	if participantToken == "" {
		return "", "", fmt.Errorf("AWS Connect participant token is missing")
	}
	body, err := json.Marshal(map[string]any{
		"Type":               []string{"WEBSOCKET", "CONNECTION_CREDENTIALS"},
		"ConnectParticipant": true,
	})
	if err != nil {
		return "", "", fmt.Errorf("encode AWS Connect CreateParticipantConnection request")
	}
	endpoint := "https://participant.connect." + region + ".amazonaws.com/participant/connection"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", "", fmt.Errorf("build AWS Connect CreateParticipantConnection request")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Amz-Bearer", participantToken)
	response, err := adapter.config.HTTP.Do(request)
	if err != nil {
		return "", "", fmt.Errorf("AWS Connect CreateParticipantConnection request failed")
	}
	if response == nil || response.Body == nil {
		return "", "", fmt.Errorf("AWS Connect CreateParticipantConnection returned no response")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("AWS Connect CreateParticipantConnection failed with HTTP %d", response.StatusCode)
	}
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, awsConnectChatMaxResponseBytes+1))
	if err != nil || len(responseBody) == 0 || len(responseBody) > awsConnectChatMaxResponseBytes {
		return "", "", fmt.Errorf("AWS Connect CreateParticipantConnection response was invalid")
	}
	var payload struct {
		Websocket struct {
			URL              string `json:"Url"`
			ConnectionExpiry string `json:"ConnectionExpiry"`
		} `json:"Websocket"`
		ConnectionCredentials struct {
			ConnectionToken string `json:"ConnectionToken"`
			Expiry          string `json:"Expiry"`
		} `json:"ConnectionCredentials"`
	}
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || ensureJSONDecoderEOF(decoder) != nil {
		return "", "", fmt.Errorf("AWS Connect CreateParticipantConnection response was invalid")
	}
	if payload.Websocket.URL == "" || len(payload.Websocket.URL) > awsConnectChatMaxWebSocketURL ||
		payload.ConnectionCredentials.ConnectionToken == "" || len(payload.ConnectionCredentials.ConnectionToken) > awsConnectChatMaxResponseBytes {
		return "", "", fmt.Errorf("AWS Connect CreateParticipantConnection returned invalid connection material")
	}
	return payload.Websocket.URL, payload.ConnectionCredentials.ConnectionToken, nil
}

func awsConnectChatSendMessage(ctx context.Context, adapter *AWSRESTAdapter, region, connectionToken string, message awsConnectChatMessage, index int) error {
	if connectionToken == "" {
		return fmt.Errorf("AWS Connect chat connection token is missing")
	}
	clientToken, err := adapter.config.ConnectChatClientToken()
	if err != nil {
		return fmt.Errorf("generate AWS Connect chat client token: %w", err)
	}
	contentType := message.ContentType
	if contentType == "" {
		contentType = "text/plain"
	}
	body, err := json.Marshal(map[string]any{
		"Content":     message.Content,
		"ContentType": contentType,
		"ClientToken": clientToken,
	})
	if err != nil {
		return fmt.Errorf("encode AWS Connect SendMessage request")
	}
	endpoint := "https://participant.connect." + region + ".amazonaws.com/participant/message"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build AWS Connect SendMessage request")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Amz-Bearer", connectionToken)
	response, err := adapter.config.HTTP.Do(request)
	if err != nil {
		return fmt.Errorf("AWS Connect SendMessage request failed")
	}
	if response == nil || response.Body == nil {
		return fmt.Errorf("AWS Connect SendMessage returned no response")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("AWS Connect SendMessage message %d failed with HTTP %d", index, response.StatusCode)
	}
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, awsConnectChatMaxResponseBytes+1))
	if err != nil || len(responseBody) == 0 || len(responseBody) > awsConnectChatMaxResponseBytes {
		return fmt.Errorf("AWS Connect SendMessage message %d response was invalid", index)
	}
	var payload struct {
		ID           string `json:"Id"`
		AbsoluteTime string `json:"AbsoluteTime"`
	}
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || ensureJSONDecoderEOF(decoder) != nil || payload.ID == "" || len(payload.ID) > 256 {
		return fmt.Errorf("AWS Connect SendMessage message %d response was invalid", index)
	}
	return nil
}

func newAWSConnectChatClientToken() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate AWS Connect chat client token")
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}

func validateAWSConnectChatWebSocketURL(rawURL, region string) error {
	target, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(target.Scheme, "wss") || target.User != nil || target.Fragment != "" || target.Port() != "" {
		return fmt.Errorf("AWS Connect chat returned an unexpected WebSocket URL")
	}
	expectedHost := "participant.connect." + region + ".amazonaws.com"
	if !strings.EqualFold(target.Hostname(), expectedHost) || target.EscapedPath() != awsConnectChatEndpointPath {
		return fmt.Errorf("AWS Connect chat returned an unexpected WebSocket endpoint")
	}
	if target.RawQuery == "" || len(target.RawQuery) > awsConnectChatMaxWebSocketURL {
		return fmt.Errorf("AWS Connect chat returned an invalid WebSocket credential query")
	}
	return nil
}

type awsConnectChatFrameKind uint8

const (
	awsConnectChatFrameSubscribeSuccess awsConnectChatFrameKind = iota
	awsConnectChatFrameSubscribeFailure
	awsConnectChatFrameIgnored
	awsConnectChatFrameEvent
)

type awsConnectChatFrame struct {
	kind  awsConnectChatFrameKind
	event []byte
}

func parseAWSConnectChatFrame(data []byte, messageType cloudWebSocketMessageType) (awsConnectChatFrame, error) {
	if messageType != cloudWebSocketMessageText || len(data) == 0 || len(data) > maxRequestPayloadBytes || !utf8.Valid(data) {
		return awsConnectChatFrame{}, fmt.Errorf("AWS Connect chat returned an invalid text frame")
	}
	var envelope struct {
		Topic         string         `json:"topic"`
		Content       map[string]any `json:"content"`
		Message       string         `json:"message"`
		Status        int            `json:"statusCode"`
		StatusContent string         `json:"statusContent"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&envelope) != nil || ensureJSONDecoderEOF(decoder) != nil {
		return awsConnectChatFrame{}, fmt.Errorf("AWS Connect chat returned a malformed frame")
	}
	switch envelope.Topic {
	case awsConnectChatSubscribeTopic:
		status, _ := envelope.Content["status"].(string)
		if status != "success" {
			return awsConnectChatFrame{kind: awsConnectChatFrameSubscribeFailure}, nil
		}
		topics, _ := envelope.Content["topics"].([]any)
		for _, raw := range topics {
			if topic, ok := raw.(string); ok && topic == awsConnectChatChatTopic {
				return awsConnectChatFrame{kind: awsConnectChatFrameSubscribeSuccess}, nil
			}
		}
		return awsConnectChatFrame{kind: awsConnectChatFrameSubscribeFailure}, nil
	case awsConnectChatHeartbeatTopic, awsConnectChatPingTopic:
		return awsConnectChatFrame{kind: awsConnectChatFrameIgnored}, nil
	case awsConnectChatChatTopic:
		event, err := sanitizeAWSConnectChatContent(envelope.Content)
		if err != nil {
			return awsConnectChatFrame{}, err
		}
		return awsConnectChatFrame{kind: awsConnectChatFrameEvent, event: event}, nil
	default:
		return awsConnectChatFrame{}, fmt.Errorf("AWS Connect chat returned an unsupported frame topic")
	}
}

func sanitizeAWSConnectChatContent(content map[string]any) ([]byte, error) {
	if content == nil || alibabaNLSContainsCredentialField(content) {
		return nil, fmt.Errorf("AWS Connect chat returned invalid or credential-bearing content")
	}
	eventType, _ := content["Type"].(string)
	if !awsConnectChatEventTypes[eventType] {
		return nil, fmt.Errorf("AWS Connect chat returned an unsupported event type")
	}
	role, _ := content["ParticipantRole"].(string)
	if role != "" && !awsConnectChatParticipantRoles[role] {
		return nil, fmt.Errorf("AWS Connect chat returned an unsupported participant role")
	}
	output := make(map[string]any, 8)
	for _, field := range []string{"Id", "AbsoluteTime", "ParticipantId", "DisplayName"} {
		if value, ok := content[field]; ok {
			text, isString := value.(string)
			if !isString || len(text) > 256 {
				return nil, fmt.Errorf("AWS Connect chat returned an invalid %s", field)
			}
			output[field] = text
		}
	}
	for _, field := range []string{"Content", "ContentType"} {
		if value, ok := content[field]; ok {
			text, isString := value.(string)
			if !isString || len(text) > awsConnectChatMaxMessageBytes || strings.ContainsAny(text, "\x00\r\n") {
				return nil, fmt.Errorf("AWS Connect chat returned an invalid %s", field)
			}
			output[field] = text
		}
	}
	output["Type"] = eventType
	if role != "" {
		output["ParticipantRole"] = role
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return nil, fmt.Errorf("encode AWS Connect chat event")
	}
	return encoded, nil
}

func defaultAWSConnectChatWebSocketDial(ctx context.Context, dialURL string) (cloudWebSocketConnection, error) {
	client := &http.Client{
		Transport: http.DefaultTransport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	connection, response, err := websocket.Dial(ctx, dialURL, &websocket.DialOptions{
		HTTPClient: client, CompressionMode: websocket.CompressionDisabled,
	})
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("AWS Connect chat WebSocket handshake failed with HTTP %d", response.StatusCode)
		}
		return nil, fmt.Errorf("AWS Connect chat WebSocket handshake failed")
	}
	connection.SetReadLimit(maxRequestPayloadBytes)
	return coderTencentWebSocketConnection{connection: connection}, nil
}

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
	"unicode"
	"unicode/utf8"

	aws "github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/smithy-go/eventstream"
)

const (
	authSchemeAWSLexV2Conversation = "lex-v2-conversation"
	awsLexV2Service                = "lex"
	awsLexV2RuntimeHostPrefix      = "runtime-v2-lex"
	awsLexV2StartOperation         = "startconversation"
	awsLexV2MaxTextEvents          = 64
	awsLexV2MaxResponseEvents      = 256
	awsLexV2MaxTimeoutSeconds      = 300
	awsLexV2MaxEventID             = 100
	awsLexV2MaxTextRunes           = 1024
	awsLexV2MaxTranscriptBytes     = 64 * 1024
	awsLexV2MaxAttributes          = 16
	awsLexV2MaxAttributeKeyRunes   = 64
	awsLexV2MaxAttributeValueRunes = 1024
	awsLexV2MaxMessages            = 10
	awsLexV2MaxInterpretations     = 5
	awsLexV2MaxAudioChunkBytes     = 256 * 1024
	awsLexV2MaxDTMFChars           = 32
	awsLexV2AudioContentType       = "audio/lpcm; sample-rate=8000; sample-size-bits=16; channel-count=1; is-big-endian=false"
	awsLexV2ConversationModeText   = "TEXT"
	awsLexV2ConversationModeAudio  = "AUDIO"
)

var awsLexV2ResponseContentTypes = map[string]struct{}{
	"CustomPayload": {}, "ImageResponseCard": {}, "PlainText": {}, "SSML": {},
}

var awsLexV2InputModes = map[string]struct{}{
	"Text": {}, "Speech": {}, "DTMF": {},
}

type awsLexV2RawTextEvent struct {
	Text              string `json:"text"`
	EventID           string `json:"event_id,omitempty"`
	ClientTimestampMS int64  `json:"client_timestamp_ms,omitempty"`
}

type awsLexV2RawPlan struct {
	BotID             string                 `json:"bot_id"`
	BotAliasID        string                 `json:"bot_alias_id"`
	LocaleID          string                 `json:"locale_id"`
	SessionID         string                 `json:"session_id"`
	ConversationMode  string                 `json:"conversation_mode,omitempty"`
	RequestAttributes map[string]string      `json:"request_attributes,omitempty"`
	SessionAttributes map[string]string      `json:"session_attributes,omitempty"`
	AudioBase64       string                 `json:"audio_base64,omitempty"`
	DTMF              string                 `json:"dtmf,omitempty"`
	Texts             []awsLexV2RawTextEvent `json:"texts"`
	MaxEvents         int                    `json:"max_events,omitempty"`
	TimeoutSeconds    int                    `json:"timeout_seconds,omitempty"`
}

type awsLexV2TextPlan struct {
	Text              string
	EventID           string
	ClientTimestampMS int64
}

type awsLexV2Plan struct {
	BotID             string
	BotAliasID        string
	LocaleID          string
	SessionID         string
	ConversationMode  string
	RequestAttributes map[string]string
	SessionAttributes map[string]string
	AudioChunk        []byte
	DTMF              []string
	Texts             []awsLexV2TextPlan
	MaxEvents         int
	Timeout           time.Duration
}

func validateAWSLexV2Invocation(invocation Invocation) error {
	if !strings.EqualFold(invocation.Service, awsLexV2Service) || normalizedOperation(invocation.Operation) != awsLexV2StartOperation {
		return fmt.Errorf("Amazon Lex V2 conversation requires service lex and operation StartConversation")
	}
	if !strings.EqualFold(invocation.Method, http.MethodPost) {
		return fmt.Errorf("Amazon Lex V2 conversation requires POST")
	}
	if err := validateAWSLexV2Endpoint(invocation.URL, invocation.Region); err != nil {
		return err
	}
	if len(invocation.Parameters) != 0 || len(invocation.Headers) != 0 || invocation.BodyFile != "" || invocation.ResponseFile == "" {
		return fmt.Errorf("Amazon Lex V2 conversation requires only a protocol body and response_file; query, headers, and body_file are forbidden")
	}
	if invocation.RegionSet != "" || invocation.Project != "" || invocation.Subscription != "" || invocation.Audience != "" || invocation.AuthVersion != "" || invocation.APIVersion != "" || invocation.PayloadMode != "" || invocation.ChecksumAlgorithm != "" || invocation.StreamChunkBytes != 0 || invocation.StreamIntervalMS != 0 || invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return fmt.Errorf("Amazon Lex V2 conversation does not accept cross-provider, REST payload, or generic stream controls")
	}
	_, err := parseAWSLexV2Plan(invocation.Body, invocation.Region)
	return err
}

func validateAWSLexV2Endpoint(rawURL, region string) error {
	region = strings.ToLower(strings.TrimSpace(region))
	if !identifierPattern.MatchString(region) {
		return fmt.Errorf("Amazon Lex V2 requires a valid AWS region")
	}
	target, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(target.Scheme, "https") || target.Hostname() == "" || target.Port() != "" || target.User != nil || target.Fragment != "" || target.RawQuery != "" || target.EscapedPath() != "" && target.EscapedPath() != "/" {
		return fmt.Errorf("Amazon Lex V2 requires the exact official https://runtime-v2-lex.<region>.amazonaws.com endpoint")
	}
	if strings.ToLower(target.Hostname()) != awsLexV2RuntimeHostPrefix+"."+region+".amazonaws.com" {
		return fmt.Errorf("Amazon Lex V2 endpoint host does not match the requested official region")
	}
	return nil
}

func parseAWSLexV2Plan(body any, region string) (awsLexV2Plan, error) {
	if body == nil || alibabaNLSContainsCredentialField(body) {
		return awsLexV2Plan{}, fmt.Errorf("Amazon Lex V2 requires a credential-free protocol body")
	}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) == 0 || len(encoded) > maxRequestPayloadBytes {
		return awsLexV2Plan{}, fmt.Errorf("Amazon Lex V2 body must be bounded JSON")
	}
	var raw awsLexV2RawPlan
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&raw) != nil || ensureJSONDecoderEOF(decoder) != nil {
		return awsLexV2Plan{}, fmt.Errorf("Amazon Lex V2 body does not match the protocol schema")
	}
	if err := validateAWSLexV2Identifier("bot_id", raw.BotID, 10, 10, true, false); err != nil {
		return awsLexV2Plan{}, err
	}
	if err := validateAWSLexV2Identifier("bot_alias_id", raw.BotAliasID, 1, 64, true, true); err != nil {
		return awsLexV2Plan{}, err
	}
	if err := validateAWSLexV2Identifier("locale_id", raw.LocaleID, 1, 10, true, false); err != nil {
		return awsLexV2Plan{}, err
	}
	if len(raw.SessionID) < 2 || len(raw.SessionID) > 100 || !awsLexV2SessionIDPattern(raw.SessionID) {
		return awsLexV2Plan{}, fmt.Errorf("Amazon Lex V2 session_id must be 2..100 characters of [0-9a-zA-Z._:-]")
	}
	conversationMode := strings.ToUpper(strings.TrimSpace(raw.ConversationMode))
	if conversationMode == "" {
		conversationMode = awsLexV2ConversationModeText
	}
	if conversationMode != awsLexV2ConversationModeText && conversationMode != awsLexV2ConversationModeAudio {
		return awsLexV2Plan{}, fmt.Errorf("Amazon Lex V2 conversation_mode must be TEXT or AUDIO")
	}
	if err := validateAWSLexV2Attributes("request_attributes", raw.RequestAttributes); err != nil {
		return awsLexV2Plan{}, err
	}
	if err := validateAWSLexV2Attributes("session_attributes", raw.SessionAttributes); err != nil {
		return awsLexV2Plan{}, err
	}
	var audioChunk []byte
	if strings.TrimSpace(raw.AudioBase64) != "" {
		decoded, err := base64.StdEncoding.DecodeString(raw.AudioBase64)
		if err != nil || len(decoded) == 0 || len(decoded) > awsLexV2MaxAudioChunkBytes {
			return awsLexV2Plan{}, fmt.Errorf("Amazon Lex V2 audio_base64 must decode to 1..%d bytes of audio/lpcm", awsLexV2MaxAudioChunkBytes)
		}
		audioChunk = decoded
	}
	var dtmf []string
	if strings.TrimSpace(raw.DTMF) != "" {
		if len(raw.DTMF) > awsLexV2MaxDTMFChars {
			return awsLexV2Plan{}, fmt.Errorf("Amazon Lex V2 dtmf exceeds its documented %d-character bound", awsLexV2MaxDTMFChars)
		}
		for _, character := range raw.DTMF {
			if !strings.ContainsRune("ABCD0123456789#*", character) {
				return awsLexV2Plan{}, fmt.Errorf("Amazon Lex V2 dtmf contains an invalid character")
			}
			dtmf = append(dtmf, string(character))
		}
	}
	if conversationMode == awsLexV2ConversationModeText && (audioChunk != nil || len(dtmf) != 0) {
		return awsLexV2Plan{}, fmt.Errorf("Amazon Lex V2 TEXT mode accepts only text events")
	}
	if len(raw.Texts) > awsLexV2MaxTextEvents || (conversationMode == awsLexV2ConversationModeText && len(raw.Texts) < 1) {
		return awsLexV2Plan{}, fmt.Errorf("Amazon Lex V2 requires 1..%d text events", awsLexV2MaxTextEvents)
	}
	if conversationMode == awsLexV2ConversationModeAudio && len(raw.Texts) == 0 && audioChunk == nil && len(dtmf) == 0 {
		return awsLexV2Plan{}, fmt.Errorf("Amazon Lex V2 AUDIO mode requires audio, DTMF, or text input")
	}
	texts := make([]awsLexV2TextPlan, 0, len(raw.Texts))
	for index, rawText := range raw.Texts {
		if err := validateAWSLexV2Text("text", rawText.Text, awsLexV2MaxTextRunes, true); err != nil {
			return awsLexV2Plan{}, err
		}
		eventID := strings.TrimSpace(rawText.EventID)
		if eventID == "" {
			eventID = fmt.Sprintf("lex-evt-%d", index+1)
		}
		if len(eventID) < 2 || len(eventID) > awsLexV2MaxEventID || !awsLexV2SessionIDPattern(eventID) {
			return awsLexV2Plan{}, fmt.Errorf("Amazon Lex V2 text event_id must be 2..100 characters of [0-9a-zA-Z._:-]")
		}
		if rawText.ClientTimestampMS < 0 {
			return awsLexV2Plan{}, fmt.Errorf("Amazon Lex V2 client_timestamp_ms must not be negative")
		}
		texts = append(texts, awsLexV2TextPlan{
			Text: rawText.Text, EventID: eventID, ClientTimestampMS: rawText.ClientTimestampMS,
		})
	}
	maxEvents := raw.MaxEvents
	if maxEvents == 0 {
		maxEvents = 64
	}
	timeoutSeconds := raw.TimeoutSeconds
	if timeoutSeconds == 0 {
		timeoutSeconds = 60
	}
	if maxEvents < 1 || maxEvents > awsLexV2MaxResponseEvents || timeoutSeconds < 1 || timeoutSeconds > awsLexV2MaxTimeoutSeconds {
		return awsLexV2Plan{}, fmt.Errorf("Amazon Lex V2 requires max_events 1..%d and timeout_seconds 1..%d", awsLexV2MaxResponseEvents, awsLexV2MaxTimeoutSeconds)
	}
	return awsLexV2Plan{
		BotID: raw.BotID, BotAliasID: raw.BotAliasID, LocaleID: raw.LocaleID, SessionID: raw.SessionID,
		ConversationMode: conversationMode, RequestAttributes: raw.RequestAttributes, SessionAttributes: raw.SessionAttributes,
		AudioChunk: audioChunk, DTMF: dtmf,
		Texts: texts, MaxEvents: maxEvents, Timeout: time.Duration(timeoutSeconds) * time.Second,
	}, nil
}

func validateAWSLexV2Identifier(name, value string, minLength, maxLength int, required, sessionStyle bool) error {
	if required && value == "" {
		return fmt.Errorf("Amazon Lex V2 %s is required", name)
	}
	if len(value) < minLength || len(value) > maxLength || !awsLexV2IdentifierPattern(value, sessionStyle) {
		return fmt.Errorf("Amazon Lex V2 %s does not match its documented identifier pattern", name)
	}
	return nil
}

func awsLexV2IdentifierPattern(value string, sessionStyle bool) bool {
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' {
			continue
		}
		if sessionStyle && (character == '.' || character == ':' || character == '-') {
			continue
		}
		return false
	}
	return true
}

func awsLexV2SessionIDPattern(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character >= '0' && character <= '9' || character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character == '.' || character == '_' || character == ':' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func validateAWSLexV2Text(name, value string, maxRunes int, required bool) error {
	if required && value == "" {
		return fmt.Errorf("Amazon Lex V2 %s is required", name)
	}
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxRunes {
		return fmt.Errorf("Amazon Lex V2 %s exceeds its documented bound", name)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("Amazon Lex V2 %s contains control characters", name)
		}
	}
	return nil
}

func validateAWSLexV2Attributes(name string, attributes map[string]string) error {
	if len(attributes) == 0 {
		return nil
	}
	credentialCheck := make(map[string]any, len(attributes))
	for key, value := range attributes {
		credentialCheck[key] = value
	}
	if len(attributes) > awsLexV2MaxAttributes || alibabaNLSContainsCredentialField(credentialCheck) {
		return fmt.Errorf("Amazon Lex V2 %s exceeds its documented bound or contains a credential-like field", name)
	}
	for key, value := range attributes {
		if err := validateAWSLexV2Text(name+" key", key, awsLexV2MaxAttributeKeyRunes, true); err != nil {
			return err
		}
		if err := validateAWSLexV2Text(name+" value", value, awsLexV2MaxAttributeValueRunes, false); err != nil {
			return err
		}
	}
	return nil
}

func encodeAWSLexV2RequestEvents(plan awsLexV2Plan, now time.Time) ([]byte, error) {
	responseContentType := "text/plain; charset=utf-8"
	if plan.ConversationMode == awsLexV2ConversationModeAudio {
		responseContentType = awsLexV2AudioContentType
	}
	configuration := map[string]any{
		"responseContentType":   responseContentType,
		"eventId":               "lex-cfg-1",
		"clientTimestampMillis": now.UnixMilli(),
	}
	if len(plan.RequestAttributes) != 0 {
		configuration["requestAttributes"] = plan.RequestAttributes
	}
	if len(plan.SessionAttributes) != 0 {
		configuration["sessionState"] = map[string]any{"sessionAttributes": plan.SessionAttributes}
	}
	events := []struct {
		eventType string
		payload   []byte
	}{
		{eventType: "ConfigurationEvent", payload: nil},
	}
	configurationPayload, err := json.Marshal(configuration)
	if err != nil {
		return nil, fmt.Errorf("encode Amazon Lex V2 configuration event")
	}
	events[0].payload = configurationPayload
	if plan.AudioChunk != nil {
		event := map[string]any{
			"audioChunk":            base64.StdEncoding.EncodeToString(plan.AudioChunk),
			"contentType":           awsLexV2AudioContentType,
			"eventId":               "lex-audio-1",
			"clientTimestampMillis": now.UnixMilli(),
		}
		payload, err := json.Marshal(event)
		if err != nil {
			return nil, fmt.Errorf("encode Amazon Lex V2 audio event")
		}
		events = append(events, struct {
			eventType string
			payload   []byte
		}{eventType: "AudioInputEvent", payload: payload})
	}
	for index, character := range plan.DTMF {
		event := map[string]any{
			"inputCharacter":        character,
			"eventId":               fmt.Sprintf("lex-dtmf-%d", index+1),
			"clientTimestampMillis": now.UnixMilli(),
		}
		payload, err := json.Marshal(event)
		if err != nil {
			return nil, fmt.Errorf("encode Amazon Lex V2 DTMF event")
		}
		events = append(events, struct {
			eventType string
			payload   []byte
		}{eventType: "DTMFInputEvent", payload: payload})
	}
	for _, text := range plan.Texts {
		event := map[string]any{"text": text.Text, "eventId": text.EventID}
		if text.ClientTimestampMS != 0 {
			event["clientTimestampMillis"] = text.ClientTimestampMS
		}
		payload, err := json.Marshal(event)
		if err != nil {
			return nil, fmt.Errorf("encode Amazon Lex V2 text event")
		}
		events = append(events, struct {
			eventType string
			payload   []byte
		}{eventType: "TextInputEvent", payload: payload})
	}
	var encoded bytes.Buffer
	for _, event := range events {
		message := eventstream.Message{Payload: event.payload}
		message.Headers.Set(eventstream.MessageTypeHeader, eventstream.StringValue(eventstream.EventMessageType))
		message.Headers.Set(eventstream.EventTypeHeader, eventstream.StringValue(event.eventType))
		message.Headers.Set(eventstream.ContentTypeHeader, eventstream.StringValue("application/json"))
		if err := eventstream.NewEncoder().Encode(&encoded, message); err != nil {
			return nil, fmt.Errorf("encode Amazon Lex V2 conversation event")
		}
	}
	return encoded.Bytes(), nil
}

func invokeAWSLexV2Conversation(ctx context.Context, adapter *AWSRESTAdapter, credentials AWSCredentials, invocation Invocation) (InvocationResult, error) {
	if err := validateAWSLexV2Invocation(invocation); err != nil {
		return InvocationResult{}, err
	}
	plan, err := parseAWSLexV2Plan(invocation.Body, invocation.Region)
	if err != nil {
		return InvocationResult{}, err
	}
	signingTime := adapter.config.Now().UTC()
	encoded, err := encodeAWSLexV2RequestEvents(plan, signingTime)
	if err != nil {
		return InvocationResult{}, err
	}
	region := strings.ToLower(strings.TrimSpace(invocation.Region))
	endpoint := "https://" + awsLexV2RuntimeHostPrefix + "." + region + ".amazonaws.com"
	path := "/bots/" + plan.BotID + "/botAliases/" + plan.BotAliasID + "/botLocales/" + plan.LocaleID + "/sessions/" + plan.SessionID + "/conversation"
	conversationCtx, cancel := context.WithTimeout(ctx, plan.Timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(conversationCtx, http.MethodPost, endpoint+path, bytes.NewReader(encoded))
	if err != nil {
		return InvocationResult{}, fmt.Errorf("build Amazon Lex V2 conversation request")
	}
	request.Header.Set("x-amz-lex-conversation-mode", plan.ConversationMode)
	if err := configureAWSEventStreamRequest(request); err != nil {
		return InvocationResult{}, err
	}
	awsCredentials := aws.Credentials{
		AccessKeyID: credentials.AccessKeyID, SecretAccessKey: credentials.SecretAccessKey, SessionToken: credentials.SessionToken,
	}
	if err := awsv4.NewSigner().SignHTTP(ctx, awsCredentials, request, awsSigV4StreamingEventsPayload, awsLexV2Service, region, signingTime); err != nil {
		return InvocationResult{}, fmt.Errorf("sign Amazon Lex V2 conversation request")
	}
	seed, err := awsSeedSignature(request.Header.Get("Authorization"))
	if err != nil {
		return InvocationResult{}, err
	}
	streamReader := newAWSSigV4EventStreamReader(conversationCtx, request.Body, awsCredentials, awsLexV2Service, region, adapter.config.Now, seed)
	request.Body = streamReader
	response, err := adapter.config.HTTP.Do(request)
	if err != nil {
		if conversationCtx.Err() != nil {
			return InvocationResult{}, fmt.Errorf("Amazon Lex V2 conversation timed out")
		}
		return InvocationResult{}, fmt.Errorf("Amazon Lex V2 conversation request: %w", err)
	}
	if response == nil || response.Body == nil {
		return InvocationResult{}, fmt.Errorf("Amazon Lex V2 conversation returned no response")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return InvocationResult{}, fmt.Errorf("Amazon Lex V2 conversation failed with HTTP %d", response.StatusCode)
	}
	sink, err := newWebSocketOutputSink(invocation, adapter.config.MaxBodyBytes, "Amazon Lex V2 conversation")
	if err != nil {
		return InvocationResult{}, err
	}
	defer sink.abort()
	decoder := eventstream.NewDecoder()
	validated := newAWSValidatingEventStreamReader(response.Body)
	defer validated.Close()
	received := 0
	for received < plan.MaxEvents {
		message, err := decoder.Decode(validated, nil)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if conversationCtx.Err() != nil {
				break
			}
			return InvocationResult{}, fmt.Errorf("read Amazon Lex V2 conversation event: %w", err)
		}
		messageType := message.Headers.Get(eventstream.MessageTypeHeader)
		eventTypeHeader := message.Headers.Get(eventstream.EventTypeHeader)
		eventType := ""
		if eventTypeHeader != nil {
			eventType = eventTypeHeader.String()
		}
		if messageType != nil && messageType.String() == eventstream.ExceptionMessageType || isAWSLexV2Exception(eventType) {
			return InvocationResult{}, fmt.Errorf("Amazon Lex V2 conversation failed with %s", eventType)
		}
		sanitized, err := sanitizeAWSLexV2ResponseEvent(plan.ConversationMode, eventType, message.Payload)
		if err != nil {
			return InvocationResult{}, err
		}
		if err := sink.writeMessage(sanitized); err != nil {
			return InvocationResult{}, err
		}
		received++
	}
	output, err := sink.finish(responseRequestID(response.Header))
	if err != nil {
		return InvocationResult{}, err
	}
	return InvocationResult{Output: output, RequestID: responseRequestID(response.Header)}, nil
}

func isAWSLexV2Exception(eventType string) bool {
	switch eventType {
	case "AccessDeniedException", "ResourceNotFoundException", "ValidationException", "ThrottlingException",
		"InternalServerException", "ConflictException", "DependencyFailedException", "BadGatewayException":
		return true
	}
	return false
}

func sanitizeAWSLexV2ResponseEvent(conversationMode, eventType string, payload []byte) ([]byte, error) {
	var object map[string]any
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if decoder.Decode(&object) != nil || ensureJSONDecoderEOF(decoder) != nil || alibabaNLSContainsCredentialField(object) {
		return nil, fmt.Errorf("Amazon Lex V2 returned invalid or credential-bearing JSON")
	}
	eventID, _ := object["eventId"].(string)
	if err := validateAWSLexV2Text("provider event ID", eventID, awsLexV2MaxEventID, false); err != nil {
		return nil, err
	}
	var sanitized map[string]any
	switch eventType {
	case "TextResponseEvent":
		messages, err := sanitizeAWSLexV2Messages(object["messages"])
		if err != nil {
			return nil, err
		}
		sanitized = map[string]any{"eventType": eventType, "eventId": eventID, "messages": messages}
	case "TranscriptEvent":
		transcript, _ := object["transcript"].(string)
		if err := validateAWSLexV2Text("provider transcript", transcript, awsLexV2MaxTranscriptBytes, false); err != nil {
			return nil, err
		}
		sanitized = map[string]any{"eventType": eventType, "eventId": eventID, "transcript": transcript}
	case "HeartbeatEvent":
		sanitized = map[string]any{"eventType": eventType, "eventId": eventID}
	case "IntentResultEvent":
		interpretations, err := sanitizeAWSLexV2Interpretations(object["interpretations"])
		if err != nil {
			return nil, err
		}
		sessionID, _ := object["sessionId"].(string)
		if sessionID != "" && (len(sessionID) < 2 || len(sessionID) > 100 || !awsLexV2SessionIDPattern(sessionID)) {
			return nil, fmt.Errorf("Amazon Lex V2 returned an invalid session ID")
		}
		inputMode, _ := object["inputMode"].(string)
		if inputMode != "" {
			if _, ok := awsLexV2InputModes[inputMode]; !ok {
				return nil, fmt.Errorf("Amazon Lex V2 returned an invalid input mode")
			}
		}
		sanitized = map[string]any{"eventType": eventType, "eventId": eventID, "sessionId": sessionID, "inputMode": inputMode, "interpretations": interpretations}
	case "AudioResponseEvent", "PlaybackInterruptionEvent":
		if conversationMode != awsLexV2ConversationModeAudio {
			return nil, fmt.Errorf("Amazon Lex V2 returned an unexpected audio event in TEXT mode")
		}
		if eventType == "AudioResponseEvent" {
			audioChunk, _ := object["audioChunk"].(string)
			contentType, _ := object["contentType"].(string)
			decoded, err := base64.StdEncoding.DecodeString(audioChunk)
			if err != nil || len(decoded) == 0 || len(decoded) > awsLexV2MaxAudioChunkBytes || !strings.HasPrefix(strings.ToLower(contentType), "audio/lpcm") {
				return nil, fmt.Errorf("Amazon Lex V2 returned an invalid audio response")
			}
			sanitized = map[string]any{"eventType": eventType, "eventId": eventID, "contentType": contentType, "audio_base64": audioChunk}
		} else {
			eventReason, _ := object["eventReason"].(string)
			if _, ok := map[string]struct{}{"DTMF_START_DETECTED": {}, "TEXT_DETECTED": {}, "VOICE_START_DETECTED": {}}[eventReason]; !ok {
				return nil, fmt.Errorf("Amazon Lex V2 returned an invalid playback interruption reason")
			}
			causedByEventID, _ := object["causedByEventId"].(string)
			if causedByEventID != "" && (len(causedByEventID) < 2 || len(causedByEventID) > awsLexV2MaxEventID || !awsLexV2SessionIDPattern(causedByEventID)) {
				return nil, fmt.Errorf("Amazon Lex V2 returned an invalid playback interruption event ID")
			}
			sanitized = map[string]any{"eventType": eventType, "eventId": eventID, "eventReason": eventReason}
			if causedByEventID != "" {
				sanitized["causedByEventId"] = causedByEventID
			}
		}
	default:
		return nil, fmt.Errorf("Amazon Lex V2 returned unexpected event type %q", eventType)
	}
	encoded, err := json.Marshal(sanitized)
	if err != nil {
		return nil, fmt.Errorf("encode Amazon Lex V2 conversation message")
	}
	return encoded, nil
}

func sanitizeAWSLexV2Messages(value any) ([]map[string]any, error) {
	raw, ok := value.([]any)
	if !ok || len(raw) > awsLexV2MaxMessages {
		return nil, fmt.Errorf("Amazon Lex V2 returned invalid messages")
	}
	messages := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		message, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("Amazon Lex V2 returned an invalid message")
		}
		content, _ := message["content"].(string)
		contentType, _ := message["contentType"].(string)
		if err := validateAWSLexV2Text("provider message content", content, awsLexV2MaxTextRunes, true); err != nil {
			return nil, err
		}
		if _, ok := awsLexV2ResponseContentTypes[contentType]; !ok {
			return nil, fmt.Errorf("Amazon Lex V2 returned an invalid message content type")
		}
		messages = append(messages, map[string]any{"content": content, "contentType": contentType})
	}
	return messages, nil
}

func sanitizeAWSLexV2Interpretations(value any) ([]map[string]any, error) {
	raw, ok := value.([]any)
	if !ok || len(raw) > awsLexV2MaxInterpretations {
		return nil, fmt.Errorf("Amazon Lex V2 returned invalid interpretations")
	}
	interpretations := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("Amazon Lex V2 returned an invalid interpretation")
		}
		intent, _ := entry["intent"].(map[string]any)
		name, _ := intent["name"].(string)
		state, _ := intent["state"].(string)
		confirmationState, _ := intent["confirmationState"].(string)
		if err := validateAWSLexV2Text("provider intent name", name, 100, true); err != nil {
			return nil, err
		}
		if err := validateAWSLexV2Text("provider intent state", state, 64, false); err != nil {
			return nil, err
		}
		if err := validateAWSLexV2Text("provider confirmation state", confirmationState, 64, false); err != nil {
			return nil, err
		}
		interpretationSource, _ := entry["interpretationSource"].(string)
		if err := validateAWSLexV2Text("provider interpretation source", interpretationSource, 64, false); err != nil {
			return nil, err
		}
		sanitized := map[string]any{
			"intent": map[string]any{"name": name, "state": state, "confirmationState": confirmationState},
		}
		if interpretationSource != "" {
			sanitized["interpretationSource"] = interpretationSource
		}
		if confidence, ok := entry["nluConfidence"].(map[string]any); ok {
			if score, ok := confidence["score"].(json.Number); ok {
				if parsed, err := score.Float64(); err == nil && parsed >= 0 && parsed <= 1 {
					sanitized["nluConfidence"] = map[string]any{"score": parsed}
				}
			}
		}
		interpretations = append(interpretations, sanitized)
	}
	return interpretations, nil
}

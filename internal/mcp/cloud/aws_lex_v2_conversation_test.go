package cloud

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/smithy-go/eventstream"
)

func validAWSLexV2Invocation(directory string) Invocation {
	return Invocation{
		Provider: ProviderAWS, Mode: ModeMutate, AuthScheme: "lex-v2-conversation", Service: "lex", Operation: "StartConversation",
		Region: "us-west-2", Method: http.MethodPost, URL: "https://runtime-v2-lex.us-west-2.amazonaws.com",
		Body: map[string]any{
			"bot_id": "ABCDEFGHIJ", "bot_alias_id": "alias-1", "locale_id": "en_US", "session_id": "session-1",
			"texts":      []any{map[string]any{"text": "hello", "event_id": "lex-evt-1"}},
			"max_events": 10, "timeout_seconds": 30,
		},
		ResponseFile: filepath.Join(directory, "lex.ndjson"),
	}
}

func TestAWSLexV2ConversationBoundaryIsMutationOnly(t *testing.T) {
	directory := t.TempDir()
	valid := validAWSLexV2Invocation(directory)
	if err := validateInvocation(valid, []string{directory}); err != nil {
		t.Fatalf("valid Lex V2 invocation rejected: %v", err)
	}
	if classifyRead(ProviderAWS, valid) || isSensitiveInvocation(valid) {
		t.Fatal("Lex V2 conversation must be mutation-only and not sensitive")
	}
	for name, mutate := range map[string]func(*Invocation){
		"lookalike host":    func(value *Invocation) { value.URL = "https://runtime-v2-lex.us-west-2.amazonaws.com.example.com" },
		"wrong region host": func(value *Invocation) { value.URL = "https://runtime-v2-lex.us-east-1.amazonaws.com" },
		"http scheme":       func(value *Invocation) { value.URL = "http://runtime-v2-lex.us-west-2.amazonaws.com" },
		"port":              func(value *Invocation) { value.URL = "https://runtime-v2-lex.us-west-2.amazonaws.com:8443" },
		"query":             func(value *Invocation) { value.URL = "https://runtime-v2-lex.us-west-2.amazonaws.com?x=1" },
		"non-root path":     func(value *Invocation) { value.URL = "https://runtime-v2-lex.us-west-2.amazonaws.com/chat" },
		"bad region": func(value *Invocation) {
			value.Region, value.URL = "us west 2", "https://runtime-v2-lex.us-west-2.amazonaws.com"
		},
		"wrong service":   func(value *Invocation) { value.Service = "lexruntimev2" },
		"wrong operation": func(value *Invocation) { value.Operation = "GetSession" },
		"wrong method":    func(value *Invocation) { value.Method = http.MethodGet },
		"caller header":   func(value *Invocation) { value.Headers = map[string]string{"Authorization": "caller"} },
		"caller query":    func(value *Invocation) { value.Parameters = map[string]any{"x": "1"} },
		"missing file":    func(value *Invocation) { value.ResponseFile = "" },
		"body_file":       func(value *Invocation) { value.BodyFile = "/tmp/lex.events" },
		"credential body": func(value *Invocation) {
			value.Body = map[string]any{"bot_id": "ABCDEFGHIJ", "bot_alias_id": "alias-1", "locale_id": "en_US", "session_id": "session-1", "token": "caller", "texts": []any{map[string]any{"text": "hello"}}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := validAWSLexV2Invocation(directory)
			candidate.ResponseFile = filepath.Join(directory, strings.ReplaceAll(name, " ", "-")+".ndjson")
			mutate(&candidate)
			if err := validateInvocation(candidate, []string{directory}); err == nil {
				t.Fatal("invalid Lex V2 invocation accepted")
			}
		})
	}
}

func TestAWSLexV2PlanBounds(t *testing.T) {
	baseBody := func() map[string]any {
		return map[string]any{
			"bot_id": "ABCDEFGHIJ", "bot_alias_id": "alias-1", "locale_id": "en_US", "session_id": "session-1",
			"texts":      []any{map[string]any{"text": "hello", "event_id": "lex-evt-1"}},
			"max_events": 10, "timeout_seconds": 30,
		}
	}
	good := func(body map[string]any) {
		t.Helper()
		if _, err := parseAWSLexV2Plan(body, "us-west-2"); err != nil {
			t.Fatalf("valid plan rejected: %v", err)
		}
	}
	bad := func(body map[string]any, fragment string) {
		t.Helper()
		if _, err := parseAWSLexV2Plan(body, "us-west-2"); err == nil || !strings.Contains(err.Error(), fragment) {
			t.Fatalf("expected error containing %q, got %v", fragment, err)
		}
	}
	good(baseBody())
	good(func() map[string]any {
		body := baseBody()
		body["request_attributes"] = map[string]string{"locale": "en-US"}
		body["session_attributes"] = map[string]string{"flow": "support"}
		body["texts"] = []any{
			map[string]any{"text": strings.Repeat("你", 1024), "event_id": "lex-evt-1"},
			map[string]any{"text": "second", "client_timestamp_ms": 123},
		}
		return body
	}())
	bad(func() map[string]any { body := baseBody(); body["bot_id"] = "SHORT"; return body }(), "bot_id")
	bad(func() map[string]any { body := baseBody(); body["bot_id"] = "ABCDEFGHIJ_"; return body }(), "bot_id")
	bad(func() map[string]any { body := baseBody(); body["bot_alias_id"] = ""; return body }(), "bot_alias_id")
	bad(func() map[string]any { body := baseBody(); body["bot_alias_id"] = "bad alias!"; return body }(), "bot_alias_id")
	bad(func() map[string]any { body := baseBody(); body["locale_id"] = ""; return body }(), "locale_id")
	bad(func() map[string]any { body := baseBody(); body["session_id"] = "s"; return body }(), "session_id")
	bad(func() map[string]any { body := baseBody(); body["session_id"] = "bad session!"; return body }(), "session_id")
	bad(func() map[string]any { body := baseBody(); body["texts"] = []any{}; return body }(), "1..64 text events")
	bad(func() map[string]any { body := baseBody(); body["texts"] = nil; return body }(), "1..64 text events")
	bad(func() map[string]any {
		body := baseBody()
		texts := make([]any, 65)
		for index := range texts {
			texts[index] = map[string]any{"text": "hello", "event_id": "lex-evt-1"}
		}
		body["texts"] = texts
		return body
	}(), "1..64 text events")
	bad(func() map[string]any {
		body := baseBody()
		body["texts"] = []any{map[string]any{"text": ""}}
		return body
	}(), "text is required")
	bad(func() map[string]any {
		body := baseBody()
		body["texts"] = []any{map[string]any{"text": strings.Repeat("x", 1025)}}
		return body
	}(), "text exceeds")
	bad(func() map[string]any {
		body := baseBody()
		body["texts"] = []any{map[string]any{"text": "bad\x00text"}}
		return body
	}(), "control characters")
	bad(func() map[string]any {
		body := baseBody()
		body["texts"] = []any{map[string]any{"text": "hello", "event_id": "x"}}
		return body
	}(), "event_id")
	bad(func() map[string]any {
		body := baseBody()
		body["request_attributes"] = map[string]string{strings.Repeat("k", 65): "v"}
		return body
	}(), "request_attributes key")
	bad(func() map[string]any {
		body := baseBody()
		body["session_attributes"] = map[string]string{"k": strings.Repeat("v", 1025)}
		return body
	}(), "session_attributes value")
	bad(func() map[string]any {
		body := baseBody()
		body["request_attributes"] = map[string]string{"access_token": "private"}
		return body
	}(), "credential-like")
	bad(func() map[string]any { body := baseBody(); body["max_events"] = 257; return body }(), "max_events 1..256")
	bad(func() map[string]any { body := baseBody(); body["timeout_seconds"] = 301; return body }(), "max_events 1..256")
	bad(func() map[string]any { body := baseBody(); body["bogus"] = true; return body }(), "protocol schema")
	bad(map[string]any{"bot_id": "ABCDEFGHIJ", "bot_alias_id": "alias-1", "locale_id": "en_US", "session_id": "session-1", "texts": []any{map[string]any{"text": "hello"}}, "token": "x"}, "credential-free")
}

func TestAWSLexV2AudioDTMFPlanBounds(t *testing.T) {
	baseBody := func() map[string]any {
		return map[string]any{
			"bot_id": "ABCDEFGHIJ", "bot_alias_id": "alias-1", "locale_id": "en_US", "session_id": "session-1",
			"conversation_mode": "AUDIO",
			"audio_base64":      base64.StdEncoding.EncodeToString([]byte("audio-bytes")),
			"dtmf":              "12#",
			"texts":             []any{map[string]any{"text": "hello", "event_id": "lex-evt-1"}},
			"max_events":        10, "timeout_seconds": 30,
		}
	}
	good := func(body map[string]any) {
		t.Helper()
		if _, err := parseAWSLexV2Plan(body, "us-west-2"); err != nil {
			t.Fatalf("valid AUDIO plan rejected: %v", err)
		}
	}
	bad := func(body map[string]any, fragment string) {
		t.Helper()
		if _, err := parseAWSLexV2Plan(body, "us-west-2"); err == nil || !strings.Contains(err.Error(), fragment) {
			t.Fatalf("expected error containing %q, got %v", fragment, err)
		}
	}
	good(baseBody())
	audioPlan, err := parseAWSLexV2Plan(baseBody(), "us-west-2")
	if err != nil {
		t.Fatal(err)
	}
	if audioPlan.ConversationMode != "AUDIO" || string(audioPlan.AudioChunk) != "audio-bytes" || strings.Join(audioPlan.DTMF, "") != "12#" {
		t.Fatalf("plan=%#v", audioPlan)
	}
	good(func() map[string]any {
		body := baseBody()
		body["conversation_mode"] = "text"
		body["audio_base64"] = ""
		body["dtmf"] = ""
		return body
	}())
	bad(func() map[string]any { body := baseBody(); body["conversation_mode"] = "VIDEO"; return body }(), "conversation_mode must be TEXT or AUDIO")
	bad(func() map[string]any {
		body := baseBody()
		body["audio_base64"] = "not-base64!!"
		return body
	}(), "audio_base64")
	bad(func() map[string]any {
		body := baseBody()
		body["audio_base64"] = base64.StdEncoding.EncodeToString(make([]byte, 256*1024+1))
		return body
	}(), "audio_base64")
	bad(func() map[string]any { body := baseBody(); body["dtmf"] = "12!#"; return body }(), "invalid character")
	bad(func() map[string]any { body := baseBody(); body["dtmf"] = strings.Repeat("1", 33); return body }(), "32-character")
	bad(func() map[string]any {
		body := baseBody()
		body["conversation_mode"] = "TEXT"
		body["audio_base64"] = base64.StdEncoding.EncodeToString([]byte("x"))
		return body
	}(), "TEXT mode accepts only text events")
	bad(func() map[string]any {
		body := baseBody()
		body["conversation_mode"] = "TEXT"
		body["dtmf"] = "1"
		return body
	}(), "TEXT mode accepts only text events")
	bad(func() map[string]any {
		body := baseBody()
		body["audio_base64"] = ""
		body["dtmf"] = ""
		body["texts"] = []any{}
		return body
	}(), "AUDIO mode requires audio, DTMF, or text input")
}

func TestAWSLexV2AudioConversationSignsAudioDTMFAndPublishesSanitizedOutput(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	var conversationModeHeader string
	var eventTypes []string
	var eventPayloads [][]byte
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		conversationModeHeader = request.Header.Get("x-amz-lex-conversation-mode")
		encoded, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		decoder := eventstream.NewDecoder()
		encodedReader := bytes.NewReader(encoded)
		for encodedReader.Len() > 0 {
			message, err := decoder.Decode(encodedReader, nil)
			if err != nil {
				return nil, err
			}
			if message.Headers.Get(eventstream.ChunkSignatureHeader) == nil {
				return nil, errors.New("missing chunk signature")
			}
			if len(message.Payload) == 0 {
				continue
			}
			inner, err := eventstream.NewDecoder().Decode(bytes.NewReader(message.Payload), nil)
			if err != nil {
				return nil, err
			}
			eventTypes = append(eventTypes, inner.Headers.Get(eventstream.EventTypeHeader).String())
			eventPayloads = append(eventPayloads, append([]byte(nil), inner.Payload...))
		}
		reader, writer := io.Pipe()
		go func() {
			defer writer.Close()
			for _, frame := range [][]byte{
				encodeAWSHTTP2TestEvent(t, "AudioResponseEvent", "application/json", []byte(`{"eventId":"res-audio","audioChunk":"aGVsbG8tYXVkaW8=","contentType":"audio/lpcm; sample-rate=8000; sample-size-bits=16; channel-count=1; is-big-endian=false"}`)),
				encodeAWSHTTP2TestEvent(t, "PlaybackInterruptionEvent", "application/json", []byte(`{"eventId":"res-interrupt","causedByEventId":"lex-dtmf-1","eventReason":"TEXT_DETECTED"}`)),
				encodeAWSHTTP2TestEvent(t, "TextResponseEvent", "application/json", []byte(`{"eventId":"res-text","messages":[{"content":"audio received","contentType":"PlainText"}]}`)),
			} {
				if _, err := writer.Write(frame); err != nil {
					return
				}
			}
		}()
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}, "X-Amzn-Requestid": []string{"lex-audio-req"}},
			Body: reader,
		}, nil
	})
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "private-secret", SessionToken: "private-session"}},
		HTTP:        doer, Now: func() time.Time { return now },
	})
	directory := t.TempDir()
	invocation := validAWSLexV2Invocation(directory)
	invocation.Body = map[string]any{
		"bot_id": "ABCDEFGHIJ", "bot_alias_id": "alias-1", "locale_id": "en_US", "session_id": "session-1",
		"conversation_mode": "AUDIO",
		"audio_base64":      base64.StdEncoding.EncodeToString([]byte("audio-bytes")),
		"dtmf":              "12#",
		"texts":             []any{map[string]any{"text": "hello", "event_id": "lex-evt-1"}},
		"max_events":        10, "timeout_seconds": 30,
	}
	result, err := adapter.Invoke(t.Context(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if conversationModeHeader != "AUDIO" {
		t.Fatalf("conversation mode header=%q", conversationModeHeader)
	}
	wantTypes := []string{"ConfigurationEvent", "AudioInputEvent", "DTMFInputEvent", "DTMFInputEvent", "DTMFInputEvent", "TextInputEvent"}
	if strings.Join(eventTypes, ",") != strings.Join(wantTypes, ",") {
		t.Fatalf("event types=%#v", eventTypes)
	}
	var audioEvent map[string]any
	if json.Unmarshal(eventPayloads[1], &audioEvent) != nil || audioEvent["contentType"] != awsLexV2AudioContentType || audioEvent["audioChunk"] != base64.StdEncoding.EncodeToString([]byte("audio-bytes")) {
		t.Fatalf("audio event=%s", eventPayloads[1])
	}
	var dtmfEvent map[string]any
	if json.Unmarshal(eventPayloads[2], &dtmfEvent) != nil || dtmfEvent["inputCharacter"] != "1" || dtmfEvent["eventId"] != "lex-dtmf-1" {
		t.Fatalf("dtmf event=%s", eventPayloads[2])
	}
	output, err := os.ReadFile(invocation.ResponseFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"eventType":"AudioResponseEvent"`, `"audio_base64":"aGVsbG8tYXVkaW8="`,
		`"eventType":"PlaybackInterruptionEvent"`, `"TEXT_DETECTED"`, `"causedByEventId":"lex-dtmf-1"`,
		`"eventType":"TextResponseEvent"`, `"audio received"`,
	} {
		if !bytes.Contains(output, []byte(want)) {
			t.Fatalf("output missing %s: %s", want, output)
		}
	}
	for _, secret := range []string{"AKIDEXAMPLE", "private-secret", "private-session"} {
		if bytes.Contains(output, []byte(secret)) || bytes.Contains(result.Output, []byte(secret)) {
			t.Fatalf("output leaked %q", secret)
		}
	}
	if result.RequestID != "lex-audio-req" {
		t.Fatalf("request id=%q", result.RequestID)
	}
}

func TestAWSLexV2ConversationSignsEventsAndPublishesSanitizedTranscript(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	var signedRequest *http.Request
	var eventTypes []string
	var eventPayloads [][]byte
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		signedRequest = request
		encoded, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		decoder := eventstream.NewDecoder()
		encodedReader := bytes.NewReader(encoded)
		for encodedReader.Len() > 0 {
			message, err := decoder.Decode(encodedReader, nil)
			if err != nil {
				return nil, err
			}
			if message.Headers.Get(eventstream.ChunkSignatureHeader) == nil {
				return nil, errors.New("missing chunk signature")
			}
			if len(message.Payload) == 0 {
				continue
			}
			inner, err := eventstream.NewDecoder().Decode(bytes.NewReader(message.Payload), nil)
			if err != nil {
				return nil, err
			}
			contentTypeHeader := inner.Headers.Get(eventstream.ContentTypeHeader)
			if contentTypeHeader == nil || contentTypeHeader.String() != "application/json" {
				return nil, errors.New("missing JSON content type")
			}
			eventTypeHeader := inner.Headers.Get(eventstream.EventTypeHeader)
			if eventTypeHeader == nil {
				return nil, errors.New("missing event type")
			}
			eventTypes = append(eventTypes, eventTypeHeader.String())
			eventPayloads = append(eventPayloads, append([]byte(nil), inner.Payload...))
		}
		reader, writer := io.Pipe()
		go func() {
			defer writer.Close()
			for _, frame := range [][]byte{
				encodeAWSHTTP2TestEvent(t, "TextResponseEvent", "application/json", []byte(`{"eventId":"res-1","messages":[{"content":"hello back","contentType":"PlainText"},{"content":"card","contentType":"CustomPayload"}]}`)),
				encodeAWSHTTP2TestEvent(t, "TranscriptEvent", "application/json", []byte(`{"eventId":"res-2","transcript":"hello back transcript"}`)),
				encodeAWSHTTP2TestEvent(t, "HeartbeatEvent", "application/json", []byte(`{"eventId":"res-3"}`)),
				encodeAWSHTTP2TestEvent(t, "IntentResultEvent", "application/json", []byte(`{"eventId":"res-4","sessionId":"session-1","inputMode":"Text","interpretations":[{"intent":{"name":"GreetingIntent","state":"Fulfilled","confirmationState":"None"},"nluConfidence":{"score":0.99},"interpretationSource":"Lex"}]}`)),
			} {
				if _, err := writer.Write(frame); err != nil {
					return
				}
			}
		}()
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}, "X-Amzn-Requestid": []string{"lex-req-1"}},
			Body: reader,
		}, nil
	})
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "private-secret", SessionToken: "private-session"}},
		HTTP:        doer, Now: func() time.Time { return now },
	})
	directory := t.TempDir()
	invocation := validAWSLexV2Invocation(directory)
	invocation.Body = map[string]any{
		"bot_id": "ABCDEFGHIJ", "bot_alias_id": "alias-1", "locale_id": "en_US", "session_id": "session-1",
		"request_attributes": map[string]string{"locale": "en-US"},
		"session_attributes": map[string]string{"flow": "support"},
		"texts": []any{
			map[string]any{"text": "first", "event_id": "lex-evt-1"},
			map[string]any{"text": "second", "event_id": "lex-evt-2"},
		},
		"max_events": 10, "timeout_seconds": 30,
	}
	result, err := adapter.Invoke(t.Context(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if signedRequest == nil {
		t.Fatal("no request was sent")
	}
	if signedRequest.URL.Path != "/bots/ABCDEFGHIJ/botAliases/alias-1/botLocales/en_US/sessions/session-1/conversation" {
		t.Fatalf("path=%s", signedRequest.URL.Path)
	}
	if signedRequest.Header.Get("x-amz-lex-conversation-mode") != "TEXT" || signedRequest.Header.Get("X-Amz-Content-Sha256") != "STREAMING-AWS4-HMAC-SHA256-EVENTS" || signedRequest.Header.Get("Content-Type") != "application/vnd.amazon.eventstream" {
		t.Fatalf("headers=%v", signedRequest.Header)
	}
	if !strings.Contains(signedRequest.Header.Get("Authorization"), "/us-west-2/lex/aws4_request") || signedRequest.Header.Get("X-Amz-Security-Token") != "private-session" {
		t.Fatalf("authorization=%q", signedRequest.Header.Get("Authorization"))
	}
	if len(eventTypes) != 3 || eventTypes[0] != "ConfigurationEvent" || eventTypes[1] != "TextInputEvent" || eventTypes[2] != "TextInputEvent" {
		t.Fatalf("event types=%#v", eventTypes)
	}
	var configuration map[string]any
	if json.Unmarshal(eventPayloads[0], &configuration) != nil || configuration["responseContentType"] != "text/plain; charset=utf-8" {
		t.Fatalf("configuration=%s", eventPayloads[0])
	}
	if _, ok := configuration["requestAttributes"].(map[string]any); !ok {
		t.Fatalf("configuration missing request attributes: %s", eventPayloads[0])
	}
	sessionState, ok := configuration["sessionState"].(map[string]any)
	if !ok || sessionState["sessionAttributes"] == nil {
		t.Fatalf("configuration missing session state: %s", eventPayloads[0])
	}
	var firstText map[string]any
	if json.Unmarshal(eventPayloads[1], &firstText) != nil || firstText["text"] != "first" || firstText["eventId"] != "lex-evt-1" {
		t.Fatalf("first text event=%s", eventPayloads[1])
	}
	output, err := os.ReadFile(invocation.ResponseFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"eventType":"TextResponseEvent"`, `"hello back"`, `"PlainText"`, `"CustomPayload"`,
		`"eventType":"TranscriptEvent"`, `"hello back transcript"`, `"eventType":"HeartbeatEvent"`,
		`"eventType":"IntentResultEvent"`, `"GreetingIntent"`, `"Fulfilled"`, `0.99`,
	} {
		if !bytes.Contains(output, []byte(want)) {
			t.Fatalf("output missing %s: %s", want, output)
		}
	}
	for _, secret := range []string{"AKIDEXAMPLE", "private-secret", "private-session", "audioChunk"} {
		if bytes.Contains(output, []byte(secret)) || bytes.Contains(result.Output, []byte(secret)) {
			t.Fatalf("output leaked %q", secret)
		}
	}
	if result.RequestID != "lex-req-1" {
		t.Fatalf("request id=%q", result.RequestID)
	}
}

func TestAWSLexV2ConversationFailuresPublishNothing(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	responseWith := func(frames [][]byte) *http.Response {
		reader, writer := io.Pipe()
		go func() {
			defer writer.Close()
			for _, frame := range frames {
				if _, err := writer.Write(frame); err != nil {
					return
				}
			}
		}()
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}}, Body: reader}
	}
	run := func(name string, adapter *AWSRESTAdapter) {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			invocation := validAWSLexV2Invocation(directory)
			if _, err := adapter.Invoke(t.Context(), invocation); err == nil {
				t.Fatal("expected failure")
			}
			if _, err := os.Stat(invocation.ResponseFile); !os.IsNotExist(err) {
				t.Fatalf("failure published output: %v", err)
			}
		})
	}
	run("HTTP 403", NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKID", SecretAccessKey: "secret"}},
		Now:         func() time.Time { return now },
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader(`{"message":"denied"}`))}, nil
		}),
	}))
	run("transport error", NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKID", SecretAccessKey: "secret"}},
		Now:         func() time.Time { return now },
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("transport blocked")
		}),
	}))
	exceptionFrame := func() []byte {
		message := eventstream.Message{Payload: []byte(`{"message":"denied"}`)}
		message.Headers.Set(eventstream.MessageTypeHeader, eventstream.StringValue(eventstream.ExceptionMessageType))
		message.Headers.Set(eventstream.EventTypeHeader, eventstream.StringValue("AccessDeniedException"))
		message.Headers.Set(eventstream.ContentTypeHeader, eventstream.StringValue("application/json"))
		var encoded bytes.Buffer
		if err := eventstream.NewEncoder().Encode(&encoded, message); err != nil {
			t.Fatal(err)
		}
		return encoded.Bytes()
	}()
	run("exception frame", NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKID", SecretAccessKey: "secret"}},
		Now:         func() time.Time { return now },
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return responseWith([][]byte{exceptionFrame}), nil
		}),
	}))
	run("unknown event type", NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKID", SecretAccessKey: "secret"}},
		Now:         func() time.Time { return now },
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return responseWith([][]byte{encodeAWSHTTP2TestEvent(t, "MysteryEvent", "application/json", []byte(`{"eventId":"e1"}`))}), nil
		}),
	}))
	run("audio event in TEXT mode", NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKID", SecretAccessKey: "secret"}},
		Now:         func() time.Time { return now },
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return responseWith([][]byte{encodeAWSHTTP2TestEvent(t, "AudioResponseEvent", "application/json", []byte(`{"eventId":"a1","audioChunk":"AA==","contentType":"audio/lpcm"}`))}), nil
		}),
	}))
	run("credential-bearing payload", NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKID", SecretAccessKey: "secret"}},
		Now:         func() time.Time { return now },
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return responseWith([][]byte{encodeAWSHTTP2TestEvent(t, "TextResponseEvent", "application/json", []byte(`{"eventId":"e1","messages":[{"content":"x","contentType":"PlainText","access_token":"private"}]}`))}), nil
		}),
	}))
	run("invalid JSON payload", NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKID", SecretAccessKey: "secret"}},
		Now:         func() time.Time { return now },
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return responseWith([][]byte{encodeAWSHTTP2TestEvent(t, "TextResponseEvent", "application/json", []byte(`not-json`))}), nil
		}),
	}))
}

func TestAWSLexV2ConversationGracefulEndAndTimeout(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	t.Run("EOF publishes partial stream", func(t *testing.T) {
		adapter := NewAWSRESTAdapter(AWSRESTConfig{
			Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKID", SecretAccessKey: "secret"}},
			Now:         func() time.Time { return now },
			HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}}, Body: io.NopCloser(bytes.NewReader(encodeAWSHTTP2TestEvent(t, "TextResponseEvent", "application/json", []byte(`{"eventId":"e1","messages":[{"content":"done","contentType":"PlainText"}]}`))))}, nil
			}),
		})
		directory := t.TempDir()
		invocation := validAWSLexV2Invocation(directory)
		invocation.Body = map[string]any{
			"bot_id": "ABCDEFGHIJ", "bot_alias_id": "alias-1", "locale_id": "en_US", "session_id": "session-1",
			"texts":      []any{map[string]any{"text": "hello", "event_id": "lex-evt-1"}},
			"max_events": 5, "timeout_seconds": 30,
		}
		if _, err := adapter.Invoke(t.Context(), invocation); err != nil {
			t.Fatal(err)
		}
		output, err := os.ReadFile(invocation.ResponseFile)
		if err != nil || !bytes.Contains(output, []byte("done")) {
			t.Fatalf("output=%q err=%v", output, err)
		}
	})
	t.Run("timeout publishes what arrived", func(t *testing.T) {
		adapter := NewAWSRESTAdapter(AWSRESTConfig{
			Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKID", SecretAccessKey: "secret"}},
			Now:         func() time.Time { return now },
			HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}}, Body: readerFunc(func([]byte) (int, error) {
					time.Sleep(1200 * time.Millisecond)
					return 0, context.DeadlineExceeded
				})}, nil
			}),
		})
		directory := t.TempDir()
		invocation := validAWSLexV2Invocation(directory)
		invocation.Body = map[string]any{
			"bot_id": "ABCDEFGHIJ", "bot_alias_id": "alias-1", "locale_id": "en_US", "session_id": "session-1",
			"texts":      []any{map[string]any{"text": "hello", "event_id": "lex-evt-1"}},
			"max_events": 5, "timeout_seconds": 1,
		}
		if _, err := adapter.Invoke(t.Context(), invocation); err != nil {
			t.Fatalf("graceful timeout failed: %v", err)
		}
		if _, err := os.Stat(invocation.ResponseFile); err != nil {
			t.Fatalf("timeout did not publish atomic file: %v", err)
		}
	})
}

type readerFunc func([]byte) (int, error)

func (reader readerFunc) Read(target []byte) (int, error) { return reader(target) }

func (readerFunc) Close() error { return nil }

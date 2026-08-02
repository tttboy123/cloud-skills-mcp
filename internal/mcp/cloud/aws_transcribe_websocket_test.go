package cloud

import (
	"bytes"
	"context"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/smithy-go/eventstream"
)

func TestAWSTranscribeWebSocketPresignUsesOfficialSigV4Shape(t *testing.T) {
	credentials := AWSCredentials{
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		SessionToken:    "session-token",
	}
	signedURL, seed, err := signAWSTranscribeWebSocketURL(
		t.Context(),
		"wss://transcribestreaming.us-west-2.amazonaws.com:8443/stream-transcription-websocket",
		credentials,
		"us-west-2",
		map[string]any{"language-code": "en-US", "media-encoding": "pcm", "sample-rate": 16000},
		time.Date(2022, 2, 8, 23, 59, 59, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(signedURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if parsed.Scheme != "wss" || query.Get("X-Amz-Algorithm") != "AWS4-HMAC-SHA256" || query.Get("X-Amz-Expires") != "300" || query.Get("X-Amz-SignedHeaders") != "host" || query.Get("X-Amz-Security-Token") != "session-token" {
		t.Fatalf("signed query=%q", parsed.RawQuery)
	}
	if got, want := query.Get("X-Amz-Credential"), "AKIAIOSFODNN7EXAMPLE/20220208/us-west-2/transcribe/aws4_request"; got != want {
		t.Fatalf("credential=%q want=%q", got, want)
	}
	if got, want := hex.EncodeToString(seed), query.Get("X-Amz-Signature"); got != want || len(seed) != 32 {
		t.Fatalf("seed=%q signature=%q", got, want)
	}
	if got, want := query.Get("X-Amz-Signature"), "a8f333b8c836d574c5be8761fa0ab91ef90f3eefed7507ad3c80c341649da4e4"; got != want {
		t.Fatalf("signature=%q want=%q", got, want)
	}
	if strings.Contains(signedURL, credentials.SecretAccessKey) {
		t.Fatal("secret access key leaked into presigned URL")
	}
}

func TestAWSTranscribeWebSocketValidationCoversAllOfficialPaths(t *testing.T) {
	audio := filepath.Join(t.TempDir(), "audio.pcm")
	if err := os.WriteFile(audio, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		path      string
		operation string
		body      any
	}{
		{"standard", "/stream-transcription-websocket", "StartStreamTranscriptionWebSocket", nil},
		{"medical", "/medical-stream-transcription-websocket", "StartMedicalStreamTranscriptionWebSocket", nil},
		{"call analytics", "/call-analytics-stream-transcription-websocket", "StartCallAnalyticsStreamTranscriptionWebSocket", map[string]any{"ChannelDefinitions": []any{}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			parameters := map[string]any{"language-code": "en-US", "media-encoding": "pcm", "sample-rate": 16000}
			if test.path == "/medical-stream-transcription-websocket" {
				parameters["specialty"] = "PRIMARYCARE"
				parameters["type"] = "CONVERSATION"
			}
			invocation := Invocation{
				Provider: ProviderAWS, Mode: ModeRead, AuthScheme: "transcribe-ws", Service: "transcribe", Operation: test.operation,
				Region: "us-west-2", Method: "GET", URL: "wss://transcribestreaming.us-west-2.amazonaws.com:8443" + test.path,
				Parameters: parameters,
				Body:       test.body, BodyFile: audio, StreamChunkBytes: 4, StreamIntervalMS: 1,
			}
			if err := validateInvocation(invocation, []string{filepath.Dir(audio)}); err != nil {
				t.Fatal(err)
			}
			if !classifyRead(ProviderAWS, invocation) {
				t.Fatal("transcription stream was not classified as a bounded read capability")
			}
		})
	}

	base := Invocation{
		Provider: ProviderAWS, AuthScheme: "transcribe-ws", Service: "transcribe", Operation: "StartStreamTranscriptionWebSocket",
		Region: "us-west-2", Method: "GET", URL: "wss://transcribestreaming.us-west-2.amazonaws.com:8443/stream-transcription-websocket",
		Parameters: map[string]any{"language-code": "en-US", "media-encoding": "pcm", "sample-rate": 16000}, BodyFile: audio,
	}
	for name, mutate := range map[string]func(*Invocation){
		"wrong host":          func(value *Invocation) { value.URL = "wss://example.com:8443/stream-transcription-websocket" },
		"wrong region host":   func(value *Invocation) { value.Region = "eu-west-1" },
		"caller signature":    func(value *Invocation) { value.Parameters["X-Amz-Signature"] = "caller" },
		"caller headers":      func(value *Invocation) { value.Headers = map[string]string{"Origin": "https://example.com"} },
		"missing audio":       func(value *Invocation) { value.BodyFile = "" },
		"unsupported service": func(value *Invocation) { value.Service = "execute-api" },
		"over one second PCM": func(value *Invocation) { value.StreamChunkBytes = 32001 },
	} {
		t.Run(name, func(t *testing.T) {
			value := base
			value.Parameters = map[string]any{"language-code": "en-US", "media-encoding": "pcm", "sample-rate": 16000}
			mutate(&value)
			if err := validateInvocation(value, []string{filepath.Dir(audio)}); err == nil {
				t.Fatal("invalid Transcribe WebSocket invocation accepted")
			}
		})
	}
}

func TestAWSTranscribeWebSocketFrameSignerProducesChainedEventStream(t *testing.T) {
	credentials := aws.Credentials{AccessKeyID: "id", SecretAccessKey: "key"}
	signer := newAWSTranscribeWebSocketFrameSigner(credentials, "us-west-2", bytes.Repeat([]byte{0x11}, 32), func() time.Time {
		return time.Unix(1640995200, 0).UTC()
	})
	first, err := signer.Encode("AudioEvent", "application/octet-stream", []byte("audio"))
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := signer.Encode("AudioEvent", "application/octet-stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, frame := range [][]byte{first, terminal} {
		outer := decodeEventStreamMessage(t, frame)
		if outer.Headers.Get(eventstream.DateHeader) == nil || outer.Headers.Get(eventstream.ChunkSignatureHeader) == nil {
			t.Fatalf("outer headers=%#v", outer.Headers)
		}
		inner := decodeEventStreamMessage(t, outer.Payload)
		if got := eventStreamStringHeader(t, inner, ":event-type"); got != "AudioEvent" {
			t.Fatalf("event type=%q", got)
		}
	}
	if bytes.Equal(first, terminal) {
		t.Fatal("terminal frame reused the prior chained signature")
	}
}

func TestAWSAdapterStreamsTranscribeWebSocketWithoutExposingPresignedURL(t *testing.T) {
	directory := t.TempDir()
	audio := filepath.Join(directory, "audio.pcm")
	if err := os.WriteFile(audio, []byte("abcdefgh"), 0o600); err != nil {
		t.Fatal(err)
	}
	providerResponse := encodeAWSTranscribeTestResponse(t, "TranscriptEvent", "event", []byte(`{"Transcript":{"Results":[]}}`))
	connection := &fakeTencentWebSocketConnection{
		reads:     [][]byte{providerResponse},
		readTypes: []tencentWebSocketMessageType{tencentWebSocketMessageBinary},
	}
	var dialed string
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "id", SecretAccessKey: "key", SessionToken: "sts"}},
		Now:         func() time.Time { return time.Unix(1640995200, 0).UTC() },
		WebSocketDial: func(_ context.Context, target string) (tencentWebSocketConnection, error) {
			dialed = target
			return connection, nil
		},
		StreamPause: func(context.Context, time.Duration) error { return nil },
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, Mode: ModeRead, AuthScheme: "transcribe-ws", Service: "transcribe", Operation: "StartCallAnalyticsStreamTranscriptionWebSocket",
		Region: "us-west-2", Method: "GET", URL: "wss://transcribestreaming.us-west-2.amazonaws.com:8443/call-analytics-stream-transcription-websocket",
		Parameters: map[string]any{"language-code": "en-US", "media-encoding": "pcm", "sample-rate": 16000, "session-id": "session-1"},
		Body:       map[string]any{"ChannelDefinitions": []any{}}, BodyFile: audio, StreamChunkBytes: 4, StreamIntervalMS: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(result.Output), "{\"Transcript\":{\"Results\":[]}}\n"; got != want {
		t.Fatalf("output=%q want=%q", got, want)
	}
	if result.RequestID != "" {
		t.Fatalf("request id=%q", result.RequestID)
	}
	if !strings.Contains(dialed, "X-Amz-Signature=") || strings.Contains(string(result.Output), "X-Amz-") || strings.Contains(string(result.Output), "key") {
		t.Fatalf("dialed=%q output=%q", dialed, result.Output)
	}
	if len(connection.writes) != 4 {
		t.Fatalf("writes=%d want=4", len(connection.writes))
	}
	for index, write := range connection.writes {
		if write.messageType != tencentWebSocketMessageBinary {
			t.Fatalf("write %d type=%d", index, write.messageType)
		}
		outer := decodeEventStreamMessage(t, write.data)
		inner := decodeEventStreamMessage(t, outer.Payload)
		wantEventType := "AudioEvent"
		if index == 0 {
			wantEventType = "ConfigurationEvent"
		}
		if got := eventStreamStringHeader(t, inner, ":event-type"); got != wantEventType {
			t.Fatalf("write %d event type=%q want=%q", index, got, wantEventType)
		}
		if index == 3 && len(inner.Payload) != 0 {
			t.Fatalf("terminal payload=%q", inner.Payload)
		}
	}
}

func TestAWSTranscribeWebSocketRejectsExceptionAndMalformedFrames(t *testing.T) {
	for name, frame := range map[string][]byte{
		"text frame": nil,
		"bad crc":    []byte("not-eventstream"),
		"exception":  encodeAWSTranscribeTestResponse(t, "BadRequestException", "exception", []byte(`{"Message":"bad audio"}`)),
	} {
		t.Run(name, func(t *testing.T) {
			messageType := tencentWebSocketMessageBinary
			if name == "text frame" {
				messageType = tencentWebSocketMessageText
				frame = []byte(`{"not":"eventstream"}`)
			}
			if _, _, err := acceptAWSTranscribeWebSocketMessage(messageType, frame); err == nil {
				t.Fatal("malformed provider response accepted")
			}
		})
	}
}

func encodeAWSTranscribeTestResponse(t *testing.T, eventType, messageType string, payload []byte) []byte {
	t.Helper()
	inner := eventstream.Message{Payload: payload}
	inner.Headers.Set(":content-type", eventstream.StringValue("application/json"))
	inner.Headers.Set(":event-type", eventstream.StringValue(eventType))
	inner.Headers.Set(":message-type", eventstream.StringValue(messageType))
	var innerBytes bytes.Buffer
	if err := eventstream.NewEncoder().Encode(&innerBytes, inner); err != nil {
		t.Fatal(err)
	}
	outer := eventstream.Message{Payload: innerBytes.Bytes()}
	outer.Headers.Set(eventstream.DateHeader, eventstream.TimestampValue(time.Unix(1640995200, 0)))
	outer.Headers.Set(eventstream.ChunkSignatureHeader, eventstream.BytesValue(bytes.Repeat([]byte{1}, 32)))
	var output bytes.Buffer
	if err := eventstream.NewEncoder().Encode(&output, outer); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func decodeEventStreamMessage(t *testing.T, data []byte) eventstream.Message {
	t.Helper()
	reader := bytes.NewReader(data)
	message, err := eventstream.NewDecoder().Decode(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reader.Len() != 0 {
		t.Fatalf("event stream frame has %d trailing bytes", reader.Len())
	}
	return message
}

func eventStreamStringHeader(t *testing.T, message eventstream.Message, name string) string {
	t.Helper()
	value, ok := message.Headers.Get(name).(eventstream.StringValue)
	if !ok {
		t.Fatalf("header %s=%T", name, message.Headers.Get(name))
	}
	return string(value)
}

package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/smithy-go/eventstream"
	"github.com/coder/websocket"
)

type fakeAWSConnectHealthWebSocketConnection struct {
	*fakeTencentWebSocketConnection
}

func (connection *fakeAWSConnectHealthWebSocketConnection) Read(ctx context.Context) (cloudWebSocketMessageType, []byte, error) {
	if len(connection.reads) == 0 {
		return 0, nil, websocket.CloseError{Code: websocket.StatusNormalClosure, Reason: "complete"}
	}
	return connection.fakeTencentWebSocketConnection.Read(ctx)
}

func TestAWSConnectHealthWebSocketBoundaryRequiresGuardedWriteSession(t *testing.T) {
	directory := t.TempDir()
	audioFile := filepath.Join(directory, "audio.pcm")
	if err := os.WriteFile(audioFile, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	valid := func() Invocation {
		return Invocation{
			Provider: ProviderAWS, Mode: ModeMutate, AuthScheme: "connect-health-ws", Service: "health-agent", Operation: "StartMedicalScribeListeningSession",
			Region: "us-west-2", Method: http.MethodGet,
			URL: "wss://streaming.health-agent.us-west-2.api.aws/medical-scribe-stream-websocket",
			Parameters: map[string]any{
				"session-id": "12345678-1234-1234-1234-123456789abc", "domain-id": "dom-abcdefghijklmnop",
				"subscription-id": "sub-abcdefghijklmnopqrstu", "language-code": "en-US", "sample-rate": 16000, "media-encoding": "pcm",
			},
			Body: map[string]any{
				"postStreamActionSettings": map[string]any{
					"outputS3Uri":                    "s3://medical-output/session/",
					"clinicalNoteGenerationSettings": map[string]any{"noteTemplateSettings": map[string]any{"managedTemplate": map[string]any{"templateType": "PHYSICAL_SOAP"}}},
				},
			},
			BodyFile: audioFile, ResponseFile: filepath.Join(directory, "transcript.ndjson"), StreamChunkBytes: 3200, StreamIntervalMS: 100,
		}
	}
	base := valid()
	if err := validateInvocation(base, []string{directory}); err != nil {
		t.Fatal(err)
	}
	if classifyRead(ProviderAWS, base) {
		t.Fatal("Connect Health listening session was classified read-only")
	}

	for name, mutate := range map[string]func(*Invocation){
		"wrong method":       func(value *Invocation) { value.Method = http.MethodPost },
		"wrong service":      func(value *Invocation) { value.Service = "connecthealth" },
		"wrong operation":    func(value *Invocation) { value.Operation = "GetMedicalScribeListeningSession" },
		"unsupported region": func(value *Invocation) { value.Region = "eu-west-1" },
		"wrong host": func(value *Invocation) {
			value.URL = "wss://streaming.health-agent.us-west-2.amazonaws.com/medical-scribe-stream-websocket"
		},
		"wrong path":   func(value *Invocation) { value.URL = "wss://streaming.health-agent.us-west-2.api.aws/other" },
		"caller query": func(value *Invocation) { value.URL += "?X-Amz-Signature=caller" },
		"wrong port": func(value *Invocation) {
			value.URL = "wss://streaming.health-agent.us-west-2.api.aws:8443/medical-scribe-stream-websocket"
		},
		"handshake header":     func(value *Invocation) { value.Headers = map[string]string{"Authorization": "caller"} },
		"missing audio":        func(value *Invocation) { value.BodyFile = "" },
		"missing config":       func(value *Invocation) { value.Body = nil },
		"missing output":       func(value *Invocation) { value.ResponseFile = "" },
		"unknown parameter":    func(value *Invocation) { value.Parameters["extra"] = "value" },
		"invalid session":      func(value *Invocation) { value.Parameters["session-id"] = "session" },
		"invalid domain":       func(value *Invocation) { value.Parameters["domain-id"] = "domain" },
		"invalid subscription": func(value *Invocation) { value.Parameters["subscription-id"] = "subscription" },
		"invalid language":     func(value *Invocation) { value.Parameters["language-code"] = "fr-FR" },
		"invalid rate":         func(value *Invocation) { value.Parameters["sample-rate"] = 7999 },
		"invalid encoding":     func(value *Invocation) { value.Parameters["media-encoding"] = "mp3" },
		"missing post action":  func(value *Invocation) { value.Body = map[string]any{} },
		"missing note template": func(value *Invocation) {
			value.Body = map[string]any{"postStreamActionSettings": map[string]any{"outputS3Uri": "s3://bucket", "clinicalNoteGenerationSettings": map[string]any{"noteTemplateSettings": map[string]any{}}}}
		},
		"one channel": func(value *Invocation) {
			value.Body.(map[string]any)["channelDefinitions"] = []any{map[string]any{"channelId": 0, "participantRole": "CLINICIAN"}}
		},
		"oversized encounter context": func(value *Invocation) {
			value.Body.(map[string]any)["encounterContext"] = map[string]any{"unstructuredContext": strings.Repeat("x", maxAWSConnectHealthEncounterContextBytes+1)}
		},
		"invalid S3 output": func(value *Invocation) {
			value.Body = map[string]any{"postStreamActionSettings": map[string]any{"outputS3Uri": "https://example.com/output", "clinicalNoteGenerationSettings": map[string]any{}}}
		},
		"credential config": func(value *Invocation) {
			value.Body = map[string]any{"postStreamActionSettings": map[string]any{"outputS3Uri": "s3://bucket/output", "clinicalNoteGenerationSettings": map[string]any{}}, "access_token": "caller"}
		},
		"oversized PCM chunk":  func(value *Invocation) { value.StreamChunkBytes = 32001 },
		"REST payload mode":    func(value *Invocation) { value.PayloadMode = "aws-eventstream" },
		"cross-provider field": func(value *Invocation) { value.Project = "project" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid()
			mutate(&candidate)
			if err := validateInvocation(candidate, []string{directory}); err == nil {
				t.Fatal("unsafe Connect Health WebSocket invocation accepted")
			}
		})
	}
}

func TestAWSConnectHealthWebSocketPresignUsesOfficialShape(t *testing.T) {
	now := time.Date(2026, 8, 3, 2, 3, 4, 0, time.UTC)
	credentials := AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "private-secret", SessionToken: "private-session-token"}
	parameters := map[string]any{
		"session-id": "12345678-1234-1234-1234-123456789abc", "domain-id": "dom-abcdefghijklmnop",
		"subscription-id": "sub-abcdefghijklmnopqrstu", "language-code": "en-US", "sample-rate": 16000, "media-encoding": "pcm",
	}
	signedURL, seed, err := signAWSConnectHealthWebSocketURL(t.Context(), "wss://streaming.health-agent.us-west-2.api.aws/medical-scribe-stream-websocket", credentials, "us-west-2", parameters, now)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(signedURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if parsed.Scheme != "wss" || query.Get("X-Amz-Expires") != "60" || query.Get("X-Amz-Security-Token") != credentials.SessionToken || query.Get("session-id") == "" || query.Get("domain-id") == "" || query.Get("subscription-id") == "" {
		t.Fatalf("signed URL=%q", signedURL)
	}
	if !strings.Contains(query.Get("X-Amz-Credential"), "/us-west-2/health-agent/aws4_request") || len(seed) != 32 {
		t.Fatalf("credential=%q seed=%x", query.Get("X-Amz-Credential"), seed)
	}
	if strings.Contains(signedURL, credentials.SecretAccessKey) {
		t.Fatal("secret access key leaked into presigned URL")
	}
}

func TestAWSConnectHealthWebSocketFrameSignerChainsOfficialEvents(t *testing.T) {
	credentials := aws.Credentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "private-secret"}
	now := func() time.Time { return time.Date(2026, 8, 3, 2, 3, 4, 0, time.UTC) }
	seed := bytes.Repeat([]byte{0x11}, 32)
	signer := newAWSConnectHealthWebSocketFrameSigner(credentials, "us-west-2", seed, now)
	configuration, err := signer.Encode("configurationEvent", "application/json", []byte(`{"postStreamActionSettings":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	audio, err := signer.Encode("binaryAudioEvent", "application/octet-stream", []byte{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := signer.Encode("sessionControlEvent", "application/json", []byte(`{"type":"END_OF_SESSION"}`))
	if err != nil {
		t.Fatal(err)
	}
	var signatures [][]byte
	for _, test := range []struct {
		frame, payload []byte
		event, content string
	}{
		{configuration, []byte(`{"postStreamActionSettings":{}}`), "configurationEvent", "application/json"},
		{audio, []byte{1, 2, 3}, "binaryAudioEvent", "application/octet-stream"},
		{terminal, []byte(`{"type":"END_OF_SESSION"}`), "sessionControlEvent", "application/json"},
	} {
		outer := decodeEventStreamMessage(t, test.frame)
		if outer.Headers.Get(eventstream.DateHeader) == nil {
			t.Fatal("signed outer frame omitted :date")
		}
		signature, ok := outer.Headers.Get(eventstream.ChunkSignatureHeader).(eventstream.BytesValue)
		if !ok || len(signature) != 32 {
			t.Fatal("signed outer frame omitted :chunk-signature")
		}
		signatures = append(signatures, append([]byte(nil), signature...))
		inner := decodeEventStreamMessage(t, outer.Payload)
		if eventStreamStringHeader(t, inner, ":event-type") != test.event || eventStreamStringHeader(t, inner, ":content-type") != test.content || !bytes.Equal(inner.Payload, test.payload) {
			t.Fatalf("inner frame=%#v payload=%q", inner.Headers, inner.Payload)
		}
	}
	if bytes.Equal(signatures[0], signatures[1]) || bytes.Equal(signatures[1], signatures[2]) {
		t.Fatal("frame signatures were not chained")
	}
}

func TestAWSConnectHealthWebSocketHelpersCoverOfficialVariants(t *testing.T) {
	for _, test := range []struct {
		value any
		want  int
		ok    bool
	}{
		{1, 1, true}, {float64(1), 1, true}, {float64(1.5), 1, false}, {json.Number("1"), 1, true}, {json.Number("bad"), 0, false}, {"1", 0, false},
	} {
		got, ok := jsonInteger(test.value)
		if got != test.want || ok != test.ok {
			t.Fatalf("jsonInteger(%#v)=(%d,%t), want (%d,%t)", test.value, got, ok, test.want, test.ok)
		}
	}
	configuration := map[string]any{
		"postStreamActionSettings": map[string]any{
			"outputS3Uri":                    "s3://medical-output",
			"clinicalNoteGenerationSettings": map[string]any{"noteTemplateSettings": map[string]any{"customTemplate": map[string]any{"templateType": "DAP", "templateInstructions": []any{}}}},
		},
		"channelDefinitions": []any{
			map[string]any{"channelId": float64(0), "participantRole": "CLINICIAN"},
			map[string]any{"channelId": json.Number("1"), "participantRole": "PATIENT"},
		},
		"encounterContext": map[string]any{"unstructuredContext": "fictional context"},
	}
	if encoded, channels, err := validateAWSConnectHealthConfiguration(configuration); err != nil || channels != 2 || !json.Valid(encoded) {
		t.Fatalf("valid configuration rejected: channels=%d err=%v", channels, err)
	}
	pcm := map[string]any{
		"session-id": "12345678-1234-1234-1234-123456789abc", "domain-id": "dom-abcdefghijklmnop",
		"subscription-id": "sub-abcdefghijklmnopqrstu", "language-code": "en-US", "sample-rate": 16000, "media-encoding": "pcm",
	}
	if got := defaultAWSConnectHealthChunkBytes(pcm, 2); got != 6400 {
		t.Fatalf("default PCM chunk=%d, want 6400", got)
	}
	flac := make(map[string]any, len(pcm))
	for key, value := range pcm {
		flac[key] = value
	}
	flac["media-encoding"] = "flac"
	if got := defaultAWSConnectHealthChunkBytes(flac, 1); got != 4096 {
		t.Fatalf("default FLAC chunk=%d, want 4096", got)
	}
	if !validAWSConnectHealthS3URI("s3://medical-output") || validAWSConnectHealthS3URI("s3://Bad_Bucket") {
		t.Fatal("S3 URI validation did not follow the official shape")
	}

	plain := eventstream.Message{Payload: []byte(`{"transcriptSegment":{"content":"plain"}}`)}
	plain.Headers.Set(eventstream.ContentTypeHeader, eventstream.StringValue("application/json"))
	plain.Headers.Set(eventstream.EventTypeHeader, eventstream.StringValue("transcriptEvent"))
	plain.Headers.Set(eventstream.MessageTypeHeader, eventstream.StringValue(eventstream.EventMessageType))
	var encodedPlain bytes.Buffer
	if err := eventstream.NewEncoder().Encode(&encodedPlain, plain); err != nil {
		t.Fatal(err)
	}
	payload, eventType, err := acceptAWSConnectHealthWebSocketMessage(cloudWebSocketMessageBinary, encodedPlain.Bytes())
	if err != nil || eventType != "transcriptEvent" || !bytes.Contains(payload, []byte("plain")) {
		t.Fatalf("plain response rejected: event=%q err=%v payload=%q", eventType, err, payload)
	}
	if _, _, err := acceptAWSConnectHealthWebSocketMessage(cloudWebSocketMessageText, encodedPlain.Bytes()); err == nil {
		t.Fatal("text response accepted")
	}
	exception := encodeAWSTranscribeTestResponse(t, "validationException", "exception", []byte(`{"message":"invalid"}`))
	if _, eventType, err := acceptAWSConnectHealthWebSocketMessage(cloudWebSocketMessageBinary, exception); err == nil || eventType != "validationException" {
		t.Fatalf("exception accepted: event=%q err=%v", eventType, err)
	}
	incomplete := eventstream.Message{Payload: encodedPlain.Bytes()}
	incomplete.Headers.Set(eventstream.DateHeader, eventstream.TimestampValue(time.Now()))
	var encodedIncomplete bytes.Buffer
	if err := eventstream.NewEncoder().Encode(&encodedIncomplete, incomplete); err != nil {
		t.Fatal(err)
	}
	if _, _, err := acceptAWSConnectHealthWebSocketMessage(cloudWebSocketMessageBinary, encodedIncomplete.Bytes()); err == nil {
		t.Fatal("incomplete signed response accepted")
	}

	credentials := AWSCredentials{AccessKeyID: "ak", SecretAccessKey: "sk"}
	if _, _, err := signAWSConnectHealthWebSocketURL(t.Context(), "wss://streaming.health-agent.us-west-2.api.aws/other", credentials, "us-west-2", pcm, time.Now()); err == nil {
		t.Fatal("wrong presign path accepted")
	}
	if _, _, err := signAWSConnectHealthWebSocketURL(t.Context(), "wss://streaming.health-agent.us-west-2.api.aws/medical-scribe-stream-websocket", AWSCredentials{}, "us-west-2", pcm, time.Now()); err == nil {
		t.Fatal("incomplete credentials accepted")
	}
}

func TestAWSAdapterStreamsConnectHealthWebSocketAndWaitsForCompletion(t *testing.T) {
	directory := t.TempDir()
	audioFile := filepath.Join(directory, "audio.pcm")
	responseFile := filepath.Join(directory, "transcript.ndjson")
	if err := os.WriteFile(audioFile, []byte("abcdefgh"), 0o600); err != nil {
		t.Fatal(err)
	}
	providerResponse := encodeAWSTranscribeTestResponse(t, "transcriptEvent", "event", []byte(`{"transcriptSegment":{"content":"hello","isPartial":false}}`))
	connection := &fakeAWSConnectHealthWebSocketConnection{fakeTencentWebSocketConnection: &fakeTencentWebSocketConnection{reads: [][]byte{providerResponse}, readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageBinary}}}
	var signedURL string
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "private-secret", SessionToken: "private-session-token"}},
		Now:         func() time.Time { return time.Date(2026, 8, 3, 2, 3, 4, 0, time.UTC) },
		ConnectHealthWebSocketDial: func(_ context.Context, target string) (cloudWebSocketConnection, error) {
			signedURL = target
			return connection, nil
		},
		StreamPause: func(context.Context, time.Duration) error { return nil },
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, Mode: ModeMutate, AuthScheme: "connect-health-ws", Service: "health-agent", Operation: "StartMedicalScribeListeningSession",
		Region: "us-west-2", Method: http.MethodGet, URL: "wss://streaming.health-agent.us-west-2.api.aws/medical-scribe-stream-websocket",
		Parameters: map[string]any{
			"session-id": "12345678-1234-1234-1234-123456789abc", "domain-id": "dom-abcdefghijklmnop",
			"subscription-id": "sub-abcdefghijklmnopqrstu", "language-code": "en-US", "sample-rate": 16000, "media-encoding": "pcm",
		},
		Body: map[string]any{"postStreamActionSettings": map[string]any{
			"outputS3Uri":                    "s3://medical-output/session/",
			"clinicalNoteGenerationSettings": map[string]any{"noteTemplateSettings": map[string]any{"managedTemplate": map[string]any{"templateType": "PHYSICAL_SOAP"}}},
		}},
		BodyFile: audioFile, ResponseFile: responseFile, StreamChunkBytes: 4, StreamIntervalMS: 1, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(signedURL, "X-Amz-Signature=") || strings.Contains(string(result.Output), "X-Amz-") || strings.Contains(string(result.Output), "private-session-token") {
		t.Fatalf("signedURL=%q result=%s", signedURL, result.Output)
	}
	if len(connection.writes) != 4 {
		t.Fatalf("writes=%d, want configuration, two audio, terminal", len(connection.writes))
	}
	for index, write := range connection.writes {
		if write.messageType != cloudWebSocketMessageBinary {
			t.Fatalf("write %d was not binary", index)
		}
	}
	innerEvents := make([]string, 0, len(connection.writes))
	for _, write := range connection.writes {
		outer := decodeEventStreamMessage(t, write.data)
		inner := decodeEventStreamMessage(t, outer.Payload)
		innerEvents = append(innerEvents, eventStreamStringHeader(t, inner, ":event-type"))
	}
	if got := strings.Join(innerEvents, ","); got != "configurationEvent,binaryAudioEvent,binaryAudioEvent,sessionControlEvent" {
		t.Fatalf("event order=%q", got)
	}
	data, err := os.ReadFile(responseFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"content":"hello"`)) || bytes.Contains(data, []byte("private-session-token")) {
		t.Fatalf("transcript=%q", data)
	}
	var metadata map[string]any
	if json.Unmarshal(result.Output, &metadata) != nil || metadata["response_file"] == "" {
		t.Fatalf("result=%s", result.Output)
	}
}

func TestAWSConnectHealthWebSocketProtocolFailureLeavesNoOutput(t *testing.T) {
	directory := t.TempDir()
	responseFile := filepath.Join(directory, "transcript.ndjson")
	connection := &fakeTencentWebSocketConnection{reads: [][]byte{[]byte("not-eventstream")}, readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageBinary}}
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials:                staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "private-secret"}},
		ConnectHealthWebSocketDial: func(context.Context, string) (cloudWebSocketConnection, error) { return connection, nil },
		StreamPause:                func(context.Context, time.Duration) error { return nil },
	})
	audioFile := filepath.Join(directory, "audio.pcm")
	if err := os.WriteFile(audioFile, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, Mode: ModeMutate, AuthScheme: "connect-health-ws", Service: "health-agent", Operation: "StartMedicalScribeListeningSession",
		Region: "us-east-1", Method: http.MethodGet, URL: "wss://streaming.health-agent.us-east-1.api.aws/medical-scribe-stream-websocket",
		Parameters: map[string]any{
			"session-id": "12345678-1234-1234-1234-123456789abc", "domain-id": "dom-abcdefghijklmnop",
			"subscription-id": "sub-abcdefghijklmnopqrstu", "language-code": "en-US", "sample-rate": 16000, "media-encoding": "pcm",
		},
		Body: map[string]any{"postStreamActionSettings": map[string]any{
			"outputS3Uri":                    "s3://medical-output/session/",
			"clinicalNoteGenerationSettings": map[string]any{"noteTemplateSettings": map[string]any{"managedTemplate": map[string]any{"templateType": "PHYSICAL_SOAP"}}},
		}},
		BodyFile: audioFile, ResponseFile: responseFile, StreamChunkBytes: 4, StreamIntervalMS: 1,
	})
	if err == nil {
		t.Fatal("malformed provider frame succeeded")
	}
	if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
		t.Fatalf("failed session published output: %v", statErr)
	}
}

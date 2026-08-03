package cloud

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAWSKinesisVideoSignalingBoundaryIsMutationOnlyAndCredentialFree(t *testing.T) {
	directory := t.TempDir()
	valid := func() Invocation {
		return Invocation{
			Provider: ProviderAWS, Mode: ModeMutate, AuthScheme: "kinesisvideo-signaling-ws", Service: "kinesisvideo", Operation: "ConnectAsViewer",
			Region: "us-west-2", Method: http.MethodGet, URL: "wss://m-01234567.kinesisvideo.us-west-2.amazonaws.com/",
			Body: map[string]any{
				"role": "VIEWER", "channel_arn": "arn:aws:kinesisvideo:us-west-2:123456789012:channel/demo/1700000000000", "client_id": "viewer-1",
				"messages":     []any{map[string]any{"action": "SDP_OFFER", "payload": map[string]any{"type": "offer", "sdp": "v=0"}, "correlation_id": "offer-1"}},
				"max_messages": 2, "timeout_seconds": 30,
			},
			ResponseFile: filepath.Join(directory, "signaling.ndjson"),
		}
	}
	base := valid()
	if err := validateInvocation(base, []string{directory}); err != nil {
		t.Fatal(err)
	}
	if classifyRead(ProviderAWS, base) {
		t.Fatal("Kinesis Video signaling session was classified read-only")
	}

	for name, mutate := range map[string]func(*Invocation){
		"read mode":         func(value *Invocation) { value.Mode = ModeRead },
		"wrong service":     func(value *Invocation) { value.Service = "kinesis-video-signaling" },
		"wrong operation":   func(value *Invocation) { value.Operation = "SendSdpOffer" },
		"wrong method":      func(value *Invocation) { value.Method = http.MethodPost },
		"wrong region":      func(value *Invocation) { value.Region = "eu-west-1" },
		"wrong endpoint":    func(value *Invocation) { value.URL = "wss://example.com/" },
		"endpoint query":    func(value *Invocation) { value.URL += "?X-Amz-ChannelARN=caller" },
		"caller parameters": func(value *Invocation) { value.Parameters = map[string]any{"X-Amz-ClientId": "caller"} },
		"caller headers":    func(value *Invocation) { value.Headers = map[string]string{"Authorization": "caller"} },
		"missing output":    func(value *Invocation) { value.ResponseFile = "" },
		"body file":         func(value *Invocation) { value.BodyFile = filepath.Join(directory, "body") },
		"credential body": func(value *Invocation) {
			value.Body = map[string]any{"role": "VIEWER", "channel_arn": "arn:aws:kinesisvideo:us-west-2:123456789012:channel/demo/1700000000000", "client_id": "viewer-1", "access_token": "forbidden", "max_messages": 1, "timeout_seconds": 1}
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid()
			mutate(&candidate)
			if err := validateInvocation(candidate, []string{directory}); err == nil {
				t.Fatal("invalid Kinesis Video signaling invocation accepted")
			}
		})
	}
}

func TestAWSKinesisVideoSignalingPlanRejectsRoleAndMessageConfusion(t *testing.T) {
	base := map[string]any{
		"role": "VIEWER", "channel_arn": "arn:aws:kinesisvideo:us-west-2:123456789012:channel/demo/1700000000000", "client_id": "viewer-1",
		"messages":     []any{map[string]any{"action": "SDP_OFFER", "payload": map[string]any{"type": "offer", "sdp": "v=0"}, "correlation_id": "offer-1"}},
		"max_messages": 1, "timeout_seconds": 30,
	}
	for name, mutate := range map[string]func(map[string]any){
		"viewer recipient": func(value map[string]any) {
			value["messages"] = []any{map[string]any{"action": "SDP_OFFER", "payload": map[string]any{"type": "offer"}, "recipient_client_id": "master", "correlation_id": "one"}}
		},
		"master missing recipient": func(value map[string]any) {
			value["role"] = "MASTER"
			delete(value, "client_id")
		},
		"master client id": func(value map[string]any) {
			value["role"] = "MASTER"
			value["messages"] = []any{map[string]any{"action": "SDP_ANSWER", "payload": map[string]any{"type": "answer"}, "recipient_client_id": "viewer-1", "correlation_id": "one"}}
		},
		"unknown action": func(value map[string]any) {
			value["messages"] = []any{map[string]any{"action": "DELETE_CHANNEL", "payload": map[string]any{}, "correlation_id": "one"}}
		},
		"missing correlation": func(value map[string]any) {
			value["messages"] = []any{map[string]any{"action": "ICE_CANDIDATE", "payload": map[string]any{"candidate": "candidate"}}}
		},
		"duplicate correlation": func(value map[string]any) {
			value["messages"] = []any{
				map[string]any{"action": "SDP_OFFER", "payload": map[string]any{"type": "offer"}, "correlation_id": "same"},
				map[string]any{"action": "ICE_CANDIDATE", "payload": map[string]any{"candidate": "candidate"}, "correlation_id": "same"},
			}
		},
		"credential payload": func(value map[string]any) {
			value["messages"] = []any{map[string]any{"action": "SDP_OFFER", "payload": map[string]any{"access_token": "forbidden"}, "correlation_id": "one"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := make(map[string]any, len(base))
			for key, value := range base {
				candidate[key] = value
			}
			mutate(candidate)
			if _, err := parseAWSKinesisVideoSignalingPlan(candidate, "us-west-2", "ConnectAsViewer"); err == nil {
				t.Fatal("invalid signaling plan accepted")
			}
		})
	}
	master := map[string]any{
		"role": "MASTER", "channel_arn": "arn:aws:kinesisvideo:us-west-2:123456789012:channel/demo/1700000000000",
		"messages":     []any{map[string]any{"action": "SDP_ANSWER", "payload": map[string]any{"type": "answer", "sdp": "v=0"}, "recipient_client_id": "viewer-1", "correlation_id": "answer-1"}},
		"max_messages": 1, "timeout_seconds": 30,
	}
	if _, err := parseAWSKinesisVideoSignalingPlan(master, "us-west-2", "ConnectAsMaster"); err != nil {
		t.Fatalf("valid master signaling plan rejected: %v", err)
	}
}

func TestAWSKinesisVideoSignalingPresignIncludesSessionTokenInCanonicalQuery(t *testing.T) {
	now := time.Date(2026, 8, 3, 9, 10, 11, 0, time.UTC)
	credentials := AWSCredentials{AccessKeyID: "AKIAIOSFODNN7EXAMPLE", SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", SessionToken: "session-one"}
	first, err := signAWSKinesisVideoSignalingURL(
		"wss://m-01234567.kinesisvideo.us-west-2.amazonaws.com/",
		"arn:aws:kinesisvideo:us-west-2:123456789012:channel/demo/1700000000000", "viewer-1", credentials, "us-west-2", now,
	)
	if err != nil {
		t.Fatal(err)
	}
	credentials.SessionToken = "session-two"
	second, err := signAWSKinesisVideoSignalingURL(
		"wss://m-01234567.kinesisvideo.us-west-2.amazonaws.com/",
		"arn:aws:kinesisvideo:us-west-2:123456789012:channel/demo/1700000000000", "viewer-1", credentials, "us-west-2", now,
	)
	if err != nil {
		t.Fatal(err)
	}
	firstURL, _ := url.Parse(first)
	secondURL, _ := url.Parse(second)
	if firstURL.Query().Get("X-Amz-Expires") != "299" || firstURL.Query().Get("X-Amz-ChannelARN") == "" || firstURL.Query().Get("X-Amz-ClientId") != "viewer-1" || firstURL.Query().Get("X-Amz-Security-Token") != "session-one" {
		t.Fatalf("signed URL query=%q", firstURL.RawQuery)
	}
	if firstURL.Query().Get("X-Amz-Signature") == secondURL.Query().Get("X-Amz-Signature") {
		t.Fatal("Kinesis Video session token was omitted from the canonical query")
	}
	// Generated independently with the official WebRTC JavaScript SDK's
	// documented SigV4 query algorithm and the same fixed inputs.
	if firstURL.Query().Get("X-Amz-Signature") != "5d271b907c4ccf0278812e9817fc51494e6edd28509464d81fdd10dc9636833a" {
		t.Fatalf("signature does not match official SDK vector: %q", firstURL.Query().Get("X-Amz-Signature"))
	}
	if strings.Contains(first, credentials.SecretAccessKey) {
		t.Fatal("secret access key leaked into signed URL")
	}
}

func TestAWSAdapterRunsFiniteKinesisVideoViewerSignalingSession(t *testing.T) {
	directory := t.TempDir()
	responseFile := filepath.Join(directory, "signaling.ndjson")
	answerPayload := base64.StdEncoding.EncodeToString([]byte(`{"type":"answer","sdp":"v=0"}`))
	icePayload := base64.StdEncoding.EncodeToString([]byte(`{"candidate":"candidate:1"}`))
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			[]byte(`{"messageType":"SDP_ANSWER","messagePayload":"` + answerPayload + `"}`),
			[]byte(`{"senderClientId":"MASTER","messageType":"ICE_CANDIDATE","messagePayload":"` + icePayload + `"}`),
		},
	}
	var signedTarget string
	var handshake http.Header
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret", SessionToken: "private-session-token"}},
		Now:         func() time.Time { return time.Date(2026, 8, 3, 9, 10, 11, 0, time.UTC) },
		SigV4WebSocketDial: func(_ context.Context, target string, headers http.Header) (cloudWebSocketConnection, error) {
			signedTarget, handshake = target, headers
			return connection, nil
		},
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, Mode: ModeMutate, AuthScheme: "kinesisvideo-signaling-ws", Service: "kinesisvideo", Operation: "ConnectAsViewer",
		Region: "us-west-2", Method: http.MethodGet, URL: "wss://m-01234567.kinesisvideo.us-west-2.amazonaws.com/",
		Body: map[string]any{
			"role": "VIEWER", "channel_arn": "arn:aws:kinesisvideo:us-west-2:123456789012:channel/demo/1700000000000", "client_id": "viewer-1",
			"messages":     []any{map[string]any{"action": "SDP_OFFER", "payload": map[string]any{"type": "offer", "sdp": "v=0"}, "correlation_id": "offer-1"}},
			"max_messages": 2, "timeout_seconds": 30,
		},
		ResponseFile: responseFile, MaxResponseFileBytes: 8192,
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(signedTarget)
	if len(handshake) != 0 || parsed.Query().Get("X-Amz-Signature") == "" || parsed.Query().Get("X-Amz-Security-Token") != "private-session-token" || result.RequestID != "viewer-1" || strings.Contains(string(result.Output), "private-session-token") {
		t.Fatalf("unsafe result=%#v target=%q headers=%v", result, signedTarget, handshake)
	}
	if len(connection.writes) != 1 || connection.writes[0].messageType != cloudWebSocketMessageText {
		t.Fatalf("writes=%#v", connection.writes)
	}
	var outbound map[string]any
	if json.Unmarshal(connection.writes[0].data, &outbound) != nil || outbound["action"] != "SDP_OFFER" || outbound["correlationId"] != "offer-1" {
		t.Fatalf("outbound=%s", connection.writes[0].data)
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(outbound["messagePayload"].(string))
	if err != nil || !bytes.Equal(decoded, []byte(`{"sdp":"v=0","type":"offer"}`)) {
		t.Fatalf("payload=%q err=%v", decoded, err)
	}
	data, err := os.ReadFile(responseFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte{'\n'})
	if len(lines) != 2 || bytes.Contains(data, []byte(answerPayload)) {
		t.Fatalf("NDJSON=%q", data)
	}
	var answer map[string]any
	if json.Unmarshal(lines[0], &answer) != nil || answer["message_type"] != "SDP_ANSWER" || answer["message_payload"].(map[string]any)["type"] != "answer" {
		t.Fatalf("answer=%s", lines[0])
	}
}

func TestAWSKinesisVideoStatusErrorLeavesNoOutput(t *testing.T) {
	directory := t.TempDir()
	responseFile := filepath.Join(directory, "signaling.ndjson")
	connection := &fakeTencentWebSocketConnection{reads: [][]byte{[]byte(`{"messageType":"STATUS_RESPONSE","statusResponse":{"correlationId":"offer-1","errorType":"InvalidArgumentException","statusCode":"400","description":"invalid offer"}}`)}}
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials:        staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret"}},
		Now:                func() time.Time { return time.Date(2026, 8, 3, 9, 10, 11, 0, time.UTC) },
		SigV4WebSocketDial: func(context.Context, string, http.Header) (cloudWebSocketConnection, error) { return connection, nil },
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, Mode: ModeMutate, AuthScheme: "kinesisvideo-signaling-ws", Service: "kinesisvideo", Operation: "ConnectAsViewer",
		Region: "us-west-2", Method: http.MethodGet, URL: "wss://m-01234567.kinesisvideo.us-west-2.amazonaws.com/",
		Body: map[string]any{
			"role": "VIEWER", "channel_arn": "arn:aws:kinesisvideo:us-west-2:123456789012:channel/demo/1700000000000", "client_id": "viewer-1",
			"messages":     []any{map[string]any{"action": "SDP_OFFER", "payload": map[string]any{"type": "offer", "sdp": "v=0"}, "correlation_id": "offer-1"}},
			"max_messages": 1, "timeout_seconds": 30,
		},
		ResponseFile: responseFile,
	})
	if err == nil || !strings.Contains(err.Error(), "STATUS_RESPONSE") {
		t.Fatalf("expected signaling status error, got %v", err)
	}
	if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
		t.Fatalf("failed signaling session published output: %v", statErr)
	}
}

func TestAWSKinesisVideoSignalingStatusAndTerminalEvents(t *testing.T) {
	plan := awsKinesisVideoSignalingPlan{Role: "VIEWER"}
	correlations := map[string]bool{"offer-1": true}
	event, terminal, err := decodeAWSKinesisVideoSignalingEvent(
		[]byte(`{"messageType":"STATUS_RESPONSE","statusResponse":{"correlationId":"offer-1","success":true,"statusCode":"200"}}`),
		plan,
		correlations,
	)
	if err != nil || terminal || !bytes.Contains(event, []byte(`"success":true`)) {
		t.Fatalf("success status event=%s terminal=%v err=%v", event, terminal, err)
	}
	event, terminal, err = decodeAWSKinesisVideoSignalingEvent([]byte(`{"messageType":"GO_AWAY"}`), plan, correlations)
	if err != nil || !terminal || !bytes.Contains(event, []byte(`"message_type":"GO_AWAY"`)) {
		t.Fatalf("terminal event=%s terminal=%v err=%v", event, terminal, err)
	}
}

func FuzzAWSKinesisVideoSignalingEventNeverPanics(f *testing.F) {
	f.Add([]byte(`{"messageType":"SDP_ANSWER","messagePayload":"eyJ0eXBlIjoiYW5zd2VyIn0="}`))
	f.Add([]byte(`{"messageType":"STATUS_RESPONSE","statusResponse":{"correlationId":"offer-1","success":false,"statusCode":"400"}}`))
	f.Add([]byte{0xff, 0x00, 0x7b})
	plan := awsKinesisVideoSignalingPlan{Role: "VIEWER"}
	correlations := map[string]bool{"offer-1": true}
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _, _ = decodeAWSKinesisVideoSignalingEvent(data, plan, correlations)
	})
}

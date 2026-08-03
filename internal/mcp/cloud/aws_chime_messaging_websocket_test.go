package cloud

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	aws "github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

const chimeTestARN = "arn:aws:chime:us-east-1:123456789012:app-instance/694d2099-cb1e-463e-9d64-697ff5b8950e/user/johndoe"

func TestAWSChimeMessagingSubscribeBoundaryRequiresExactOfficialProtocol(t *testing.T) {
	root := t.TempDir()
	valid := func() Invocation {
		return Invocation{
			Provider: ProviderAWS, Mode: ModeRead, AuthScheme: "chime-messaging-ws", Service: "chime-messaging", Operation: "SubscribeMessages",
			Region: "us-east-1", Method: http.MethodGet, URL: "wss://data-messaging.chime.aws/connect",
			Body: map[string]any{
				"user_arn": chimeTestARN, "session_id": "session-1", "prefetch_on_connect": true,
				"connect_expires_seconds": 60, "max_messages": 4, "timeout_seconds": 30,
			},
			ResponseFile: filepath.Join(root, "events.ndjson"),
		}
	}
	if err := validateInvocation(valid(), []string{root}); err != nil {
		t.Fatal(err)
	}
	if !classifyRead(ProviderAWS, valid()) {
		t.Fatal("Chime messaging SubscribeMessages was not classified read-only")
	}
	for name, mutate := range map[string]func(*Invocation){
		"post":           func(v *Invocation) { v.Method = http.MethodPost },
		"service":        func(v *Invocation) { v.Service = "chime" },
		"operation":      func(v *Invocation) { v.Operation = "Connect" },
		"missing region": func(v *Invocation) { v.Region = "" },
		"host":           func(v *Invocation) { v.URL = "wss://data-messaging.chime.aws.attacker.test/connect" },
		"wrong host":     func(v *Invocation) { v.URL = "wss://messaging-chime.us-east-1.amazonaws.com/connect" },
		"path":           func(v *Invocation) { v.URL = "wss://data-messaging.chime.aws/" },
		"query":          func(v *Invocation) { v.URL = "wss://data-messaging.chime.aws/connect?sessionId=caller" },
		"port":           func(v *Invocation) { v.URL = "wss://data-messaging.chime.aws:8443/connect" },
		"header":         func(v *Invocation) { v.Headers = map[string]string{"Authorization": "caller"} },
		"parameter":      func(v *Invocation) { v.Parameters = map[string]any{"userArn": "caller"} },
		"missing body":   func(v *Invocation) { v.Body = nil },
		"body file":      func(v *Invocation) { v.BodyFile = filepath.Join(root, "body") },
		"missing output": func(v *Invocation) { v.ResponseFile = "" },
		"region set":     func(v *Invocation) { v.RegionSet = "us-east-1" },
		"unknown field": func(v *Invocation) {
			v.Body.(map[string]any)["channel_arn"] = "arn:aws:chime:us-east-1:123456789012:channel/x"
		},
		"credential": func(v *Invocation) { v.Body.(map[string]any)["access_token"] = "caller" },
		"bad arn": func(v *Invocation) {
			v.Body.(map[string]any)["user_arn"] = "arn:aws:chime:us-east-1:123456789012:user/johndoe"
		},
		"non chime arn": func(v *Invocation) { v.Body.(map[string]any)["user_arn"] = "arn:aws:iam::123456789012:user/johndoe" },
		"long arn": func(v *Invocation) {
			v.Body.(map[string]any)["user_arn"] = "arn:aws:chime:us-east-1:123456789012:app-instance/" + strings.Repeat("a", 65) + "/user/johndoe"
		},
		"bad session": func(v *Invocation) { v.Body.(map[string]any)["session_id"] = "bad session id" },
		"expires low": func(v *Invocation) { v.Body.(map[string]any)["connect_expires_seconds"] = 9 },
		"expires high": func(v *Invocation) {
			v.Body.(map[string]any)["connect_expires_seconds"] = 3601
		},
		"unbounded":   func(v *Invocation) { v.Body.(map[string]any)["timeout_seconds"] = 301 },
		"no messages": func(v *Invocation) { v.Body.(map[string]any)["max_messages"] = 0 },
		"stream pace": func(v *Invocation) { v.StreamIntervalMS = 100 },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid()
			mutate(&candidate)
			if err := validateInvocation(candidate, []string{root}); err == nil {
				t.Fatal("unsafe Chime messaging invocation accepted")
			}
		})
	}
}

func TestAWSChimeMessagingSubscribeStreamsSanitizedEvents(t *testing.T) {
	root := t.TempDir()
	responseFile := filepath.Join(root, "events.ndjson")
	now := time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC)
	connection := &fakeTencentWebSocketConnection{reads: [][]byte{
		[]byte(`{"Headers":{"x-amz-chime-event-type":"SESSION_ESTABLISHED"},"Payload":""}`),
		[]byte(`{"Headers":{"x-amz-chime-event-type":"CREATE_CHANNEL_MESSAGE","x-amz-chime-message-type":"STANDARD"},"Payload":"{\"ChannelMessage\":{\"MessageId\":\"msg-1\",\"Content\":\"hello\"}}"}`),
		[]byte(`{"Headers":{"x-amz-chime-event-type":"CHANNEL_DETAILS","x-amz-chime-message-type":"SYSTEM"},"Payload":"{\"Channel\":{\"ChannelArn\":\"arn:aws:chime:us-east-1:123456789012:channel/room\"}}"}`),
	}}
	var signedURL string
	var endpointRequest *http.Request
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret", SessionToken: "private-session"}},
		Now:         func() time.Time { return now },
		HTTP: doerFunc(func(request *http.Request) (*http.Response, error) {
			endpointRequest = request.Clone(request.Context())
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"Endpoint":{"Url":"wss://data-messaging.chime.aws"}}`))}, nil
		}),
		ChimeMessagingWebSocketDial: func(_ context.Context, target string) (cloudWebSocketConnection, error) {
			signedURL = target
			return connection, nil
		},
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, Mode: ModeRead, AuthScheme: "chime-messaging-ws", Service: "chime-messaging", Operation: "SubscribeMessages",
		Region: "us-east-1", Method: http.MethodGet, URL: "wss://data-messaging.chime.aws/connect",
		Body: map[string]any{
			"user_arn": chimeTestARN, "session_id": "session-1", "prefetch_on_connect": true,
			"connect_expires_seconds": 60, "max_messages": 4, "timeout_seconds": 30,
		},
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if endpointRequest == nil || endpointRequest.Method != http.MethodGet || endpointRequest.URL.String() != "https://messaging-chime.us-east-1.amazonaws.com/endpoints/messaging-session" {
		t.Fatalf("endpoint request=%#v", endpointRequest)
	}
	authorization := endpointRequest.Header.Get("Authorization")
	if !strings.Contains(authorization, "AWS4-HMAC-SHA256") || !strings.Contains(authorization, "Credential=AKIDEXAMPLE/20240501/us-east-1/chime/aws4_request") {
		t.Fatalf("endpoint authorization=%q", authorization)
	}
	if signedURL == "" || !strings.HasPrefix(signedURL, "wss://data-messaging.chime.aws/connect?") {
		t.Fatalf("signed URL=%q", signedURL)
	}
	signed, err := url.Parse(signedURL)
	if err != nil {
		t.Fatal(err)
	}
	query := signed.Query()
	if query.Get("userArn") != chimeTestARN || query.Get("sessionId") != "session-1" || query.Get("prefetch-on") != "connect" || query.Get("X-Amz-Expires") != "60" || query.Get("X-Amz-Algorithm") != "AWS4-HMAC-SHA256" || query.Get("X-Amz-SignedHeaders") != "host" || query.Get("X-Amz-Security-Token") != "private-session" || len(query.Get("X-Amz-Signature")) != 64 {
		t.Fatalf("signed query=%#v", query)
	}
	content, err := os.ReadFile(responseFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 3 {
		t.Fatalf("output lines=%d: %s", len(lines), content)
	}
	if !strings.Contains(string(content), `"MessageId":"msg-1"`) || strings.Contains(string(content), "private-session") || strings.Contains(string(content), "AKIDEXAMPLE") || strings.Contains(string(content), "X-Amz-Signature") {
		t.Fatalf("sanitized output leaked or lost data: %s", content)
	}
	var messageEvent map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &messageEvent); err != nil {
		t.Fatal(err)
	}
	payload, ok := messageEvent["Payload"].(map[string]any)
	if !ok {
		t.Fatalf("payload was not decoded: %#v", messageEvent["Payload"])
	}
	if _, ok := payload["ChannelMessage"]; !ok {
		t.Fatalf("payload=%#v", payload)
	}
	var metadata map[string]any
	if err := json.Unmarshal(result.Output, &metadata); err != nil || metadata["messages"] != float64(3) {
		t.Fatalf("metadata=%v err=%v", metadata, err)
	}
}

func TestAWSChimeMessagingConnectSignatureMatchesSDKPresigner(t *testing.T) {
	now := time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC)
	credentials := AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret", SessionToken: "private-session"}
	plan, err := parseAWSChimeMessagingPlan(map[string]any{
		"user_arn": chimeTestARN, "session_id": "session-1", "prefetch_on_connect": true,
		"connect_expires_seconds": 60, "max_messages": 1, "timeout_seconds": 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signAWSChimeMessagingConnectURL("wss://data-messaging.chime.aws/connect", plan, credentials, "us-east-1", now)
	if err != nil {
		t.Fatal(err)
	}
	referenceURL, _ := url.Parse("wss://data-messaging.chime.aws/connect")
	reference := *referenceURL
	query := reference.Query()
	query.Set("userArn", chimeTestARN)
	query.Set("sessionId", "session-1")
	query.Set("prefetch-on", "connect")
	query.Set("X-Amz-Expires", "60")
	reference.RawQuery = query.Encode()
	request, err := http.NewRequest(http.MethodGet, reference.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	presignedURI, _, err := awsv4.NewSigner().PresignHTTP(context.Background(), aws.Credentials{
		AccessKeyID: credentials.AccessKeyID, SecretAccessKey: credentials.SecretAccessKey, SessionToken: credentials.SessionToken,
	}, request, sha256Hex(nil), awsChimeMessagingService, "us-east-1", now)
	if err != nil {
		t.Fatal(err)
	}
	got := signedQuery(t, signed)
	want := signedQuery(t, presignedURI)
	for _, key := range []string{"X-Amz-Algorithm", "X-Amz-Credential", "X-Amz-Date", "X-Amz-Expires", "X-Amz-SignedHeaders", "X-Amz-Security-Token", "userArn", "sessionId", "prefetch-on", "X-Amz-Signature"} {
		if got[key] != want[key] {
			t.Fatalf("signature param %s: got %q want %q", key, got[key], want[key])
		}
	}
}

func signedQuery(t *testing.T, raw string) map[string]string {
	t.Helper()
	target, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	result := make(map[string]string)
	for key, values := range target.Query() {
		result[key] = values[0]
	}
	return result
}

func TestAWSChimeMessagingSanitizeRejectsInvalidEvents(t *testing.T) {
	if _, err := sanitizeAWSChimeMessagingEvent([]byte(`{"Headers":{"x-amz-chime-event-type":"DELETE_CHANNEL_MEMBERSHIP","x-amz-chime-event-reason":"subchannel_DELETED"},"Payload":"{\"ChannelMembership\":{}}"}`), cloudWebSocketMessageText); err != nil {
		t.Fatal(err)
	}
	for name, candidate := range map[string]struct {
		data []byte
		kind cloudWebSocketMessageType
	}{
		"binary":           {data: []byte(`{"Headers":{}}`), kind: cloudWebSocketMessageBinary},
		"not json":         {data: []byte(`not-json`), kind: cloudWebSocketMessageText},
		"unknown envelope": {data: []byte(`{"Headers":{},"Payload":"{}","Extra":1}`), kind: cloudWebSocketMessageText},
		"missing headers":  {data: []byte(`{"Payload":"{}"}`), kind: cloudWebSocketMessageText},
		"unknown event":    {data: []byte(`{"Headers":{"x-amz-chime-event-type":"TYPING"},"Payload":"{}"}`), kind: cloudWebSocketMessageText},
		"bad message type": {data: []byte(`{"Headers":{"x-amz-chime-event-type":"CREATE_CHANNEL_MESSAGE","x-amz-chime-message-type":"BROADCAST"},"Payload":"{}"}`), kind: cloudWebSocketMessageText},
		"bad reason":       {data: []byte("{\"Headers\":{\"x-amz-chime-event-type\":\"DELETE_CHANNEL_MEMBERSHIP\",\"x-amz-chime-event-reason\":\"bad\\nreason\"},\"Payload\":\"{}\"}"), kind: cloudWebSocketMessageText},
		"payload not str":  {data: []byte(`{"Headers":{"x-amz-chime-event-type":"CHANNEL_DETAILS"},"Payload":{"Channel":{}}}`), kind: cloudWebSocketMessageText},
		"payload bad json": {data: []byte(`{"Headers":{"x-amz-chime-event-type":"CHANNEL_DETAILS"},"Payload":"not-json"}`), kind: cloudWebSocketMessageText},
		"too many headers": {data: []byte(`{"Headers":{"a1":"1","a2":"2","a3":"3","a4":"4","a5":"5","a6":"6","a7":"7","a8":"8","a9":"9","a10":"10","a11":"11","a12":"12","a13":"13","a14":"14","a15":"15","a16":"16","a17":"17","a18":"18","a19":"19","a20":"20","a21":"21","a22":"22","a23":"23","a24":"24","a25":"25","a26":"26","a27":"27","a28":"28","a29":"29","a30":"30","a31":"31","a32":"32","a33":"33"},"Payload":""}`), kind: cloudWebSocketMessageText},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := sanitizeAWSChimeMessagingEvent(candidate.data, candidate.kind); err == nil {
				t.Fatal("unsafe Chime messaging event accepted")
			}
		})
	}
}

func TestAWSChimeMessagingEndpointMismatchFailsClosed(t *testing.T) {
	connection := &fakeTencentWebSocketConnection{reads: [][]byte{}}
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret"}},
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"Endpoint":{"Url":"wss://evil.example"}}`))}, nil
		}),
		ChimeMessagingWebSocketDial: func(_ context.Context, _ string) (cloudWebSocketConnection, error) {
			return connection, nil
		},
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, Mode: ModeRead, AuthScheme: "chime-messaging-ws", Service: "chime-messaging", Operation: "SubscribeMessages",
		Region: "us-east-1", Method: http.MethodGet, URL: "wss://data-messaging.chime.aws/connect",
		Body: map[string]any{
			"user_arn": chimeTestARN, "session_id": "session-1",
			"max_messages": 1, "timeout_seconds": 5,
		},
		ResponseFile: filepath.Join(t.TempDir(), "events.ndjson"),
	})
	if err == nil || !strings.Contains(err.Error(), "unexpected session endpoint") {
		t.Fatalf("err=%v", err)
	}
}

func TestAWSChimeMessagingAtomicFailureLeavesNoOutput(t *testing.T) {
	root := t.TempDir()
	responseFile := filepath.Join(root, "events.ndjson")
	connection := &fakeTencentWebSocketConnection{reads: [][]byte{
		[]byte(`{"Headers":{"x-amz-chime-event-type":"SESSION_ESTABLISHED"},"Payload":""}`),
		[]byte(`{"Headers":{"x-amz-chime-event-type":"TYPING"},"Payload":""}`),
	}}
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret"}},
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"Endpoint":{"Url":"wss://data-messaging.chime.aws"}}`))}, nil
		}),
		ChimeMessagingWebSocketDial: func(_ context.Context, _ string) (cloudWebSocketConnection, error) {
			return connection, nil
		},
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, Mode: ModeRead, AuthScheme: "chime-messaging-ws", Service: "chime-messaging", Operation: "SubscribeMessages",
		Region: "us-east-1", Method: http.MethodGet, URL: "wss://data-messaging.chime.aws/connect",
		Body: map[string]any{
			"user_arn": chimeTestARN, "session_id": "session-1",
			"max_messages": 4, "timeout_seconds": 5,
		},
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err == nil {
		t.Fatal("expected an unsupported event failure")
	}
	if _, statErr := os.Lstat(responseFile); statErr == nil {
		t.Fatal("failed session left a published response_file")
	} else if !os.IsNotExist(statErr) {
		t.Fatal(statErr)
	}
}

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
)

const connectChatTestInstance = "12345678-1234-1234-1234-123456789012"
const connectChatTestFlow = "arn:aws:connect:us-east-1:123456789012:contact-flow/87654321-4321-4321-4321-210987654321"

func awsConnectChatInvocation(responseFile string) Invocation {
	return Invocation{
		Provider: ProviderAWS, Mode: ModeMutate, AuthScheme: authSchemeAWSConnectChatWS,
		Service: "connect", Operation: "ObserveChat", Region: "us-east-1", Method: http.MethodPost,
		URL: "wss://participant.connect.us-east-1.amazonaws.com/participant/connect",
		Body: map[string]any{
			"instance_id": connectChatTestInstance, "contact_flow_id": connectChatTestFlow,
			"display_name": "Observer", "max_events": 4, "timeout_seconds": 30,
		},
		ResponseFile: responseFile, MaxResponseFileBytes: 8192,
	}
}

func TestAWSConnectChatBoundaryRequiresExactOfficialProtocol(t *testing.T) {
	root := t.TempDir()
	base := awsConnectChatInvocation(filepath.Join(root, "events.ndjson"))
	if err := validateInvocation(base, []string{root}); err != nil {
		t.Fatalf("valid AWS Connect chat invocation rejected: %v", err)
	}
	if classifyRead(ProviderAWS, base) {
		t.Fatal("AWS Connect chat ObserveChat must be mutation-only")
	}
	withOptions := base
	withOptions.Body = map[string]any{
		"instance_id": connectChatTestInstance, "contact_flow_id": connectChatTestFlow,
		"display_name": "Observer", "initial_message": "Hello", "attributes": map[string]any{"team": "cloud"},
		"max_events": 2, "timeout_seconds": 10,
	}
	if err := validateInvocation(withOptions, []string{root}); err != nil {
		t.Fatalf("valid AWS Connect chat options rejected: %v", err)
	}
	for name, mutate := range map[string]func(*Invocation){
		"get method":      func(v *Invocation) { v.Method = http.MethodGet },
		"wrong service":   func(v *Invocation) { v.Service = "connectparticipant" },
		"wrong operation": func(v *Invocation) { v.Operation = "SendMessage" },
		"missing region":  func(v *Invocation) { v.Region = "" },
		"region mismatch": func(v *Invocation) { v.Region = "us-west-2" },
		"lookalike host": func(v *Invocation) {
			v.URL = "wss://participant.connect.us-east-1.amazonaws.com.attacker.example/participant/connect"
		},
		"control host": func(v *Invocation) { v.URL = "wss://connect.us-east-1.amazonaws.com/participant/connect" },
		"wrong path":   func(v *Invocation) { v.URL = "wss://participant.connect.us-east-1.amazonaws.com/participant/message" },
		"query":        func(v *Invocation) { v.URL += "?X-Amz-Signature=caller" },
		"port": func(v *Invocation) {
			v.URL = "wss://participant.connect.us-east-1.amazonaws.com:8443/participant/connect"
		},
		"caller header":    func(v *Invocation) { v.Headers = map[string]string{"X-Amz-Bearer": "caller"} },
		"caller parameter": func(v *Invocation) { v.Parameters = map[string]any{"instanceId": "caller"} },
		"missing body":     func(v *Invocation) { v.Body = nil },
		"body file":        func(v *Invocation) { v.BodyFile = filepath.Join(root, "body") },
		"missing output":   func(v *Invocation) { v.ResponseFile = "" },
		"region set":       func(v *Invocation) { v.RegionSet = "us-east-1" },
		"unknown field":    func(v *Invocation) { v.Body.(map[string]any)["chat_duration_minutes"] = 10 },
		"credential":       func(v *Invocation) { v.Body.(map[string]any)["connection_token"] = "caller" },
		"bad instance":     func(v *Invocation) { v.Body.(map[string]any)["instance_id"] = "not-a-uuid" },
		"bad flow":         func(v *Invocation) { v.Body.(map[string]any)["contact_flow_id"] = "flow" },
		"empty display":    func(v *Invocation) { v.Body.(map[string]any)["display_name"] = "" },
		"control display":  func(v *Invocation) { v.Body.(map[string]any)["display_name"] = "Bad\nName" },
		"newline message":  func(v *Invocation) { v.Body.(map[string]any)["initial_message"] = "bad\nmessage" },
		"too many attrs": func(v *Invocation) {
			attrs := map[string]any{}
			for i := 0; i <= awsConnectChatMaxAttributes; i++ {
				attrs[strings.Repeat("k", 8)+string(rune('a'+i%26))] = "v"
			}
			v.Body.(map[string]any)["attributes"] = attrs
		},
		"oversized attr value": func(v *Invocation) {
			v.Body.(map[string]any)["attributes"] = map[string]any{"k": strings.Repeat("a", awsConnectChatMaxAttributeValue+1)}
		},
		"too many messages": func(v *Invocation) {
			messages := make([]any, awsConnectChatMaxMessages+1)
			for i := range messages {
				messages[i] = map[string]any{"content": "x"}
			}
			v.Body.(map[string]any)["messages"] = messages
		},
		"empty message": func(v *Invocation) { v.Body.(map[string]any)["messages"] = []any{map[string]any{"content": ""}} },
		"oversized message": func(v *Invocation) {
			v.Body.(map[string]any)["messages"] = []any{map[string]any{"content": strings.Repeat("a", awsConnectChatMaxMessageBytes+1)}}
		},
		"newline message content": func(v *Invocation) {
			v.Body.(map[string]any)["messages"] = []any{map[string]any{"content": "bad\nmessage"}}
		},
		"bad content type": func(v *Invocation) {
			v.Body.(map[string]any)["messages"] = []any{map[string]any{"content": "x", "content_type": "text/html"}}
		},
		"message credential": func(v *Invocation) {
			v.Body.(map[string]any)["messages"] = []any{map[string]any{"content": "x", "connection_token": "caller"}}
		},
		"zero events":    func(v *Invocation) { v.Body.(map[string]any)["max_events"] = 0 },
		"excess timeout": func(v *Invocation) { v.Body.(map[string]any)["timeout_seconds"] = 301 },
		"stream pace":    func(v *Invocation) { v.StreamIntervalMS = 100 },
		"cross provider": func(v *Invocation) { v.Audience = "https://evil.example" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := base
			candidate.Body = base.Body
			candidate.Parameters = nil
			candidate.Headers = nil
			mutate(&candidate)
			if err := validateInvocation(candidate, []string{root}); err == nil {
				t.Fatalf("unsafe AWS Connect chat invocation accepted: %#v", candidate)
			}
		})
	}
}

func TestAWSConnectChatObservesSanitizedChatEvents(t *testing.T) {
	root := t.TempDir()
	responseFile := filepath.Join(root, "events.ndjson")
	now := time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC)
	connection := &fakeTencentWebSocketConnection{reads: [][]byte{
		[]byte(`{"topic":"aws/subscribe","content":{"status":"success","topics":["aws/chat"]}}`),
		[]byte(`{"topic":"aws/heartbeat"}`),
		[]byte(`{"topic":"aws/chat","content":{"Type":"MESSAGE","Id":"msg-1","AbsoluteTime":"2024-05-01T12:00:01Z","Content":"hello world","ContentType":"text/plain","ParticipantRole":"CUSTOMER","ParticipantId":"participant-1","DisplayName":"Observer"}}`),
		[]byte(`{"topic":"aws/chat","content":{"Type":"MESSAGE","Id":"msg-2","AbsoluteTime":"2024-05-01T12:00:03Z","Content":"sent first","ContentType":"text/plain","ParticipantRole":"CUSTOMER","ParticipantId":"participant-1","DisplayName":"Observer"}}`),
		[]byte(`{"topic":"aws/chat","content":{"Type":"PARTICIPANT_JOINED","Id":"evt-1","AbsoluteTime":"2024-05-01T12:00:02Z","ContentType":"application/vnd.amazonaws.connect.event.participant.joined","ParticipantRole":"AGENT","DisplayName":"Agent Smith"}}`),
	}}
	var dialURL string
	var startRequest *http.Request
	var participantRequest *http.Request
	var participantBearer string
	var sendRequests []*http.Request
	var sendBearer string
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "private-secret", SessionToken: "private-session"}},
		Now:         func() time.Time { return now },
		HTTP: doerFunc(func(request *http.Request) (*http.Response, error) {
			switch {
			case request.URL.Host == "connect.us-east-1.amazonaws.com":
				startRequest = request.Clone(request.Context())
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(
					`{"ContactId":"contact-1","ParticipantId":"participant-1","ParticipantToken":"participant-token-secret"}`))}, nil
			case request.URL.Host == "participant.connect.us-east-1.amazonaws.com":
				if request.URL.Path == "/participant/message" {
					sendRequests = append(sendRequests, request.Clone(request.Context()))
					sendBearer = request.Header.Get("X-Amz-Bearer")
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(
						`{"Id":"msg-2","AbsoluteTime":"2024-05-01T12:00:03Z"}`))}, nil
				}
				participantRequest = request.Clone(request.Context())
				participantBearer = request.Header.Get("X-Amz-Bearer")
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(
					`{"Websocket":{"Url":"wss://participant.connect.us-east-1.amazonaws.com/participant/connect?X-Amz-Credential=opaque-signature","ConnectionExpiry":"2024-05-01T12:01:40Z"},"ConnectionCredentials":{"ConnectionToken":"connection-token-secret","Expiry":"2024-05-02T12:00:00Z"}}`))}, nil
			}
			return nil, io.EOF
		}),
		ConnectChatWebSocketDial: func(_ context.Context, target string) (cloudWebSocketConnection, error) {
			dialURL = target
			return connection, nil
		},
	})
	invocation := awsConnectChatInvocation(responseFile)
	invocation.Body = map[string]any{
		"instance_id": connectChatTestInstance, "contact_flow_id": connectChatTestFlow,
		"display_name": "Observer",
		"messages": []any{
			map[string]any{"content": "sent first"},
			map[string]any{"content": "sent second", "content_type": "text/markdown"},
		},
		"max_events": 4, "timeout_seconds": 30,
	}
	result, err := adapter.Invoke(t.Context(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if startRequest == nil || startRequest.Method != http.MethodPut || startRequest.URL.String() != "https://connect.us-east-1.amazonaws.com/contact/chat" {
		t.Fatalf("start request=%#v", startRequest)
	}
	authorization := startRequest.Header.Get("Authorization")
	if !strings.Contains(authorization, "AWS4-HMAC-SHA256") || !strings.Contains(authorization, "Credential=AKIDEXAMPLE/20240501/us-east-1/connect/aws4_request") {
		t.Fatalf("start authorization=%q", authorization)
	}
	startBody, err := io.ReadAll(startRequest.Body)
	if err != nil {
		t.Fatal(err)
	}
	var decodedStart map[string]any
	if err := json.Unmarshal(startBody, &decodedStart); err != nil {
		t.Fatal(err)
	}
	if decodedStart["InstanceId"] != connectChatTestInstance || decodedStart["ContactFlowId"] != connectChatTestFlow || strings.Contains(string(startBody), "participant-token-secret") {
		t.Fatalf("start body=%s", startBody)
	}
	if participantRequest == nil || participantRequest.Method != http.MethodPost || participantRequest.URL.String() != "https://participant.connect.us-east-1.amazonaws.com/participant/connection" || participantBearer != "participant-token-secret" {
		t.Fatalf("participant request=%#v bearer=%q", participantRequest, participantBearer)
	}
	if len(sendRequests) != 2 || sendRequests[0].Method != http.MethodPost || sendRequests[0].URL.String() != "https://participant.connect.us-east-1.amazonaws.com/participant/message" || sendBearer != "connection-token-secret" {
		t.Fatalf("send requests=%d bearer=%q", len(sendRequests), sendBearer)
	}
	sendBody, err := io.ReadAll(sendRequests[0].Body)
	if err != nil {
		t.Fatal(err)
	}
	var decodedSend map[string]any
	if err := json.Unmarshal(sendBody, &decodedSend); err != nil {
		t.Fatal(err)
	}
	if decodedSend["Content"] != "sent first" || decodedSend["ContentType"] != "text/plain" {
		t.Fatalf("send body=%s", sendBody)
	}
	secondSendBody, err := io.ReadAll(sendRequests[1].Body)
	if err != nil {
		t.Fatal(err)
	}
	var decodedSecondSend map[string]any
	if err := json.Unmarshal(secondSendBody, &decodedSecondSend); err != nil {
		t.Fatal(err)
	}
	if decodedSecondSend["Content"] != "sent second" || decodedSecondSend["ContentType"] != "text/markdown" {
		t.Fatalf("second send body=%s", secondSendBody)
	}
	clientToken, _ := decodedSend["ClientToken"].(string)
	if len(clientToken) != 36 || strings.Count(clientToken, "-") != 4 {
		t.Fatalf("send client token=%q", clientToken)
	}
	if strings.Contains(string(sendBody), "connection-token-secret") {
		t.Fatalf("send body leaked the connection token")
	}
	if dialURL != "wss://participant.connect.us-east-1.amazonaws.com/participant/connect?X-Amz-Credential=opaque-signature" {
		t.Fatalf("dial URL=%q", dialURL)
	}
	if len(connection.writes) != 1 || !strings.Contains(string(connection.writes[0].data), `"topic":"aws/subscribe"`) || !strings.Contains(string(connection.writes[0].data), `"aws/chat"`) {
		t.Fatalf("subscribe frame=%q", connection.writes[0].data)
	}
	content, err := os.ReadFile(responseFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 3 || !strings.Contains(string(content), `"Content":"hello world"`) || !strings.Contains(string(content), `"Content":"sent first"`) || !strings.Contains(string(content), `"Type":"PARTICIPANT_JOINED"`) {
		t.Fatalf("output=%s", content)
	}
	for _, secret := range []string{"participant-token-secret", "connection-token-secret", "private-secret", "private-session", "AKIDEXAMPLE", "opaque-signature"} {
		if strings.Contains(string(content), secret) || strings.Contains(string(result.Output), secret) {
			t.Fatalf("output leaked %q", secret)
		}
	}
	var metadata map[string]any
	if err := json.Unmarshal(result.Output, &metadata); err != nil || metadata["messages"] != float64(3) || metadata["sent_messages"] != float64(2) || metadata["contact_id"] != "contact-1" {
		t.Fatalf("metadata=%v err=%v", metadata, err)
	}
}

func TestAWSConnectChatFailureNeverPublishesOutput(t *testing.T) {
	okConnection := func(reads ...[]byte) *fakeTencentWebSocketConnection {
		return &fakeTencentWebSocketConnection{reads: reads}
	}
	subscribeSuccess := []byte(`{"topic":"aws/subscribe","content":{"status":"success","topics":["aws/chat"]}}`)
	for name, test := range map[string]struct {
		connection *fakeTencentWebSocketConnection
		websocket  string
		startHTTP  int
		startBody  string
		partHTTP   int
		partBody   string
		sendHTTP   int
		sendBody   string
		send       bool
		dialErr    error
		writeErr   error
		emptyCreds bool
	}{
		"subscribe failure":        {connection: okConnection([]byte(`{"topic":"aws/subscribe","content":{"status":"failure","topics":["aws/chat"]}}`))},
		"missing chat topic":       {connection: okConnection([]byte(`{"topic":"aws/subscribe","content":{"status":"success","topics":["aws/other"]}}`))},
		"unknown topic":            {connection: okConnection(subscribeSuccess, []byte(`{"topic":"aws/typing","content":{}}`))},
		"malformed frame":          {connection: okConnection(subscribeSuccess, []byte(`{"topic":42}`))},
		"credential content":       {connection: okConnection(subscribeSuccess, []byte(`{"topic":"aws/chat","content":{"Type":"MESSAGE","Id":"m","Content":"x","api_key":"leaked"}}`))},
		"unsupported event":        {connection: okConnection(subscribeSuccess, []byte(`{"topic":"aws/chat","content":{"Type":"UNKNOWN","Id":"m"}}`))},
		"unsupported role":         {connection: okConnection(subscribeSuccess, []byte(`{"topic":"aws/chat","content":{"Type":"MESSAGE","Id":"m","ParticipantRole":"BOT"}}`))},
		"empty before subscribe":   {connection: okConnection()},
		"websocket host mismatch":  {websocket: "wss://evil.example/participant/connect?X-Amz-Credential=opaque"},
		"start http error":         {startHTTP: http.StatusForbidden, startBody: `{"Message":"forbidden","token":"super-secret-token-value"}`},
		"start invalid response":   {startBody: `{"ParticipantToken":123}`},
		"start missing token":      {startBody: `{}`},
		"participant http error":   {partHTTP: http.StatusForbidden, partBody: `{"Message":"forbidden"}`},
		"participant bad url":      {partBody: `{"Websocket":{"Url":"wss://evil.example/x"}}`},
		"participant empty url":    {partBody: `{"Websocket":{"Url":""},"ConnectionCredentials":{"ConnectionToken":"t"}}`},
		"participant invalid json": {partBody: `{"Websocket":{`},
		"send http error":          {send: true, sendHTTP: http.StatusForbidden, sendBody: `{"Message":"forbidden","token":"super-secret-token-value"}`},
		"send invalid response":    {send: true, sendBody: `{"Id":123}`},
		"send missing id":          {send: true, sendBody: `{}`},
		"dial error":               {dialErr: io.ErrUnexpectedEOF},
		"write error":              {writeErr: io.ErrClosedPipe},
		"empty credentials":        {emptyCreds: true},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			responseFile := filepath.Join(root, "events.ndjson")
			websocketURL := "wss://participant.connect.us-east-1.amazonaws.com/participant/connect?X-Amz-Credential=opaque"
			if test.websocket != "" {
				websocketURL = test.websocket
			}
			connection := test.connection
			if connection == nil {
				connection = okConnection(subscribeSuccess)
			}
			provider := staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "private-secret"}}
			if test.emptyCreds {
				provider = staticAWSCredentialsProvider{AWSCredentials{}}
			}
			adapter := NewAWSRESTAdapter(AWSRESTConfig{
				Credentials: provider,
				HTTP: doerFunc(func(request *http.Request) (*http.Response, error) {
					if request.URL.Host == "connect.us-east-1.amazonaws.com" {
						status := http.StatusOK
						if test.startHTTP != 0 {
							status = test.startHTTP
						}
						body := test.startBody
						if body == "" {
							body = `{"ParticipantToken":"participant-token-secret"}`
						}
						return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
					}
					if request.URL.Path == "/participant/message" {
						status := http.StatusOK
						if test.sendHTTP != 0 {
							status = test.sendHTTP
						}
						body := test.sendBody
						if body == "" {
							body = `{"Id":"msg-sent","AbsoluteTime":"2024-05-01T12:00:03Z"}`
						}
						return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
					}
					status := http.StatusOK
					if test.partHTTP != 0 {
						status = test.partHTTP
					}
					body := test.partBody
					if body == "" {
						body = `{"Websocket":{"Url":"` + websocketURL + `"},"ConnectionCredentials":{"ConnectionToken":"connection-token-secret"}}`
					}
					return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
				}),
				ConnectChatWebSocketDial: func(_ context.Context, _ string) (cloudWebSocketConnection, error) {
					if test.dialErr != nil {
						return nil, test.dialErr
					}
					if test.writeErr != nil {
						return &awsConnectChatFailingConnection{inner: connection, writeErr: test.writeErr}, nil
					}
					return connection, nil
				},
			})
			invocation := awsConnectChatInvocation(responseFile)
			if test.send {
				invocation.Body = map[string]any{
					"instance_id": connectChatTestInstance, "contact_flow_id": connectChatTestFlow,
					"display_name": "Observer",
					"messages":     []any{map[string]any{"content": "sent first"}},
					"max_events":   4, "timeout_seconds": 30,
				}
			}
			_, err := adapter.Invoke(t.Context(), invocation)
			if err == nil {
				t.Fatal("unsafe AWS Connect chat stream accepted")
			}
			if (name == "start http error" || name == "send http error") && strings.Contains(err.Error(), "super-secret-token-value") {
				t.Fatalf("provider error leaked secret material: %v", err)
			}
			if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
				t.Fatalf("failed AWS Connect chat stream published output: %v", statErr)
			}
		})
	}
}

type awsConnectChatFailingConnection struct {
	inner    cloudWebSocketConnection
	writeErr error
}

func (connection *awsConnectChatFailingConnection) Read(ctx context.Context) (cloudWebSocketMessageType, []byte, error) {
	return connection.inner.Read(ctx)
}

func (connection *awsConnectChatFailingConnection) Write(_ context.Context, _ cloudWebSocketMessageType, _ []byte) error {
	return connection.writeErr
}

func (connection *awsConnectChatFailingConnection) Close() error {
	return connection.inner.Close()
}

func TestAWSConnectChatWebSocketURLStrictValidation(t *testing.T) {
	valid := "wss://participant.connect.us-east-1.amazonaws.com/participant/connect?X-Amz-Credential=opaque"
	for name, candidate := range map[string]string{
		"valid":        valid,
		"https scheme": "https://participant.connect.us-east-1.amazonaws.com/participant/connect?X-Amz-Credential=opaque",
		"wrong host":   "wss://participant.connect.us-west-2.amazonaws.com/participant/connect?X-Amz-Credential=opaque",
		"evil host":    "wss://evil.example/participant/connect?X-Amz-Credential=opaque",
		"wrong path":   "wss://participant.connect.us-east-1.amazonaws.com/participant/message?X-Amz-Credential=opaque",
		"port":         "wss://participant.connect.us-east-1.amazonaws.com:443/participant/connect?X-Amz-Credential=opaque",
		"empty query":  "wss://participant.connect.us-east-1.amazonaws.com/participant/connect",
	} {
		t.Run(name, func(t *testing.T) {
			err := validateAWSConnectChatWebSocketURL(candidate, "us-east-1")
			if name == "valid" && err != nil {
				t.Fatalf("valid URL rejected: %v", err)
			}
			if name != "valid" && err == nil {
				t.Fatal("invalid WebSocket URL accepted")
			}
		})
	}
}

func FuzzAWSConnectChatHostNeverLeavesOfficialEndpoint(f *testing.F) {
	for _, seed := range []string{
		"us-east-1",
		"us-west-2",
		"participant.connect.us-east-1.amazonaws.com",
		"participant.connect.us-east-1.amazonaws.com.evil.example",
		"evil.example",
		"",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, host string) {
		target := (&url.URL{Scheme: "wss", Host: host, Path: "/participant/connect"}).String()
		invocation := Invocation{
			Provider: ProviderAWS, Mode: ModeMutate, AuthScheme: authSchemeAWSConnectChatWS,
			Service: "connect", Operation: "ObserveChat", Region: "us-east-1", Method: http.MethodPost,
			URL: target,
			Body: map[string]any{
				"instance_id": connectChatTestInstance, "contact_flow_id": connectChatTestFlow,
				"display_name": "Observer", "max_events": 1, "timeout_seconds": 5,
			},
			ResponseFile: "/approved/events.ndjson",
		}
		if validateAWSConnectChatInvocation(invocation) != nil {
			return
		}
		parsed, err := url.Parse(target)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.EqualFold(parsed.Hostname(), "participant.connect.us-east-1.amazonaws.com") || parsed.EscapedPath() != awsConnectChatEndpointPath {
			t.Fatalf("accepted non-official AWS Connect chat endpoint %q", target)
		}
	})
}

package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	coderwebsocket "github.com/coder/websocket"
)

func validAWSIVSChatInvocation(directory string) Invocation {
	return Invocation{
		Provider: ProviderAWS, Mode: ModeRead, AuthScheme: "ivs-chat-ws", Service: "ivschat", Operation: "SubscribeChat",
		Region: "us-west-2", Method: http.MethodGet, URL: "wss://edge.ivschat.us-west-2.amazonaws.com",
		Body: map[string]any{
			"room_identifier": "arn:aws:ivschat:us-west-2:123456789012:room/test-room",
			"user_id":         "observer", "session_duration_minutes": 1, "max_messages": 1, "timeout_seconds": 10,
		},
		ResponseFile: filepath.Join(directory, "chat.ndjson"),
	}
}

func TestAWSIVSChatBoundarySeparatesReadSendAndSensitiveModeration(t *testing.T) {
	directory := t.TempDir()
	read := validAWSIVSChatInvocation(directory)
	if err := validateInvocation(read, []string{directory}); err != nil || !classifyRead(ProviderAWS, read) || isSensitiveInvocation(read) {
		t.Fatalf("read boundary err=%v read=%t sensitive=%t", err, classifyRead(ProviderAWS, read), isSensitiveInvocation(read))
	}
	send := validAWSIVSChatInvocation(directory)
	send.Mode, send.Operation = ModeMutate, "ClientChat"
	send.Body = map[string]any{
		"room_identifier": "arn:aws:ivschat:us-west-2:123456789012:room/test-room", "user_id": "sender",
		"messages":     []any{map[string]any{"action": "SEND_MESSAGE", "content": "hello", "request_id": "request-1"}},
		"max_messages": 1, "timeout_seconds": 10,
	}
	if err := validateInvocation(send, []string{directory}); err != nil || classifyRead(ProviderAWS, send) || isSensitiveInvocation(send) {
		t.Fatalf("send boundary err=%v read=%t sensitive=%t", err, classifyRead(ProviderAWS, send), isSensitiveInvocation(send))
	}
	moderate := validAWSIVSChatInvocation(directory)
	moderate.Mode, moderate.Operation = ModeMutate, "ModerateChat"
	moderate.Body = map[string]any{
		"room_identifier": "arn:aws:ivschat:us-west-2:123456789012:room/test-room", "user_id": "moderator",
		"messages":     []any{map[string]any{"action": "DELETE_MESSAGE", "id": "message-1", "reason": "policy"}},
		"max_messages": 1, "timeout_seconds": 10,
	}
	if err := validateInvocation(moderate, []string{directory}); err != nil || !isSensitiveInvocation(moderate) {
		t.Fatalf("moderation boundary err=%v sensitive=%t", err, isSensitiveInvocation(moderate))
	}

	for name, mutate := range map[string]func(*Invocation){
		"lookalike host": func(value *Invocation) { value.URL = "wss://edge.ivschat.us-west-2.amazonaws.com.example.com" },
		"unsupported region": func(value *Invocation) {
			value.Region, value.URL = "ca-central-1", "wss://edge.ivschat.ca-central-1.amazonaws.com"
		},
		"caller query":  func(value *Invocation) { value.URL += "?token=caller" },
		"caller header": func(value *Invocation) { value.Headers = map[string]string{"Authorization": "caller"} },
		"missing file":  func(value *Invocation) { value.ResponseFile = "" },
		"credential body": func(value *Invocation) {
			value.Body = map[string]any{"room_identifier": "arn:aws:ivschat:us-west-2:123456789012:room/test-room", "user_id": "observer", "token": "caller", "max_messages": 1, "timeout_seconds": 1}
		},
		"wrong room region": func(value *Invocation) {
			value.Body = map[string]any{"room_identifier": "arn:aws:ivschat:us-east-1:123456789012:room/test-room", "user_id": "observer", "max_messages": 1, "timeout_seconds": 1}
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := validAWSIVSChatInvocation(directory)
			candidate.ResponseFile = filepath.Join(directory, strings.ReplaceAll(name, " ", "-")+".ndjson")
			mutate(&candidate)
			if err := validateInvocation(candidate, []string{directory}); err == nil {
				t.Fatal("invalid IVS Chat invocation accepted")
			}
		})
	}
}

func TestAWSIVSChatTokenIsSigV4SignedAndNeverExposed(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	var signed *http.Request
	httpDoer := doerFunc(func(request *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(request.Body)
		request.Body = io.NopCloser(bytes.NewReader(body))
		signed = request
		return &http.Response{StatusCode: 200, Header: http.Header{"X-Amzn-Requestid": []string{"token-request"}}, Body: io.NopCloser(strings.NewReader(`{"token":"private-one-time-token","tokenExpirationTime":"2026-08-03T12:01:00Z","sessionExpirationTime":"2026-08-03T12:30:00Z"}`))}, nil
	})
	connection := &fakeTencentWebSocketConnection{reads: [][]byte{
		[]byte(`{"Type":"MESSAGE","Id":"message-1","RequestId":"request-1","Content":"hello","Sender":{"UserId":"sender","Attributes":{"display":"safe"}},"SendTime":"2026-08-03T12:00:01Z","Attributes":{}}`),
	}, readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageText}}
	var protocols []string
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret", SessionToken: "private-session"}},
		HTTP:        httpDoer,
		Now:         func() time.Time { return now },
		IVSChatWebSocketDial: func(_ context.Context, rawURL string, values []string) (cloudWebSocketConnection, error) {
			if rawURL != "wss://edge.ivschat.us-west-2.amazonaws.com" {
				t.Fatalf("URL=%s", rawURL)
			}
			protocols = append([]string(nil), values...)
			return connection, nil
		},
	})
	directory := t.TempDir()
	invocation := validAWSIVSChatInvocation(directory)
	result, err := adapter.Invoke(t.Context(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if signed == nil || signed.URL.String() != "https://ivschat.us-west-2.amazonaws.com/CreateChatToken" || signed.Method != http.MethodPost {
		t.Fatalf("token request=%v", signed)
	}
	if !strings.Contains(signed.Header.Get("Authorization"), "/us-west-2/ivschat/aws4_request") || signed.Header.Get("X-Amz-Security-Token") != "private-session" {
		t.Fatalf("token signature headers=%v", signed.Header)
	}
	var tokenBody map[string]any
	encoded, _ := io.ReadAll(signed.Body)
	if json.Unmarshal(encoded, &tokenBody) != nil || tokenBody["roomIdentifier"] == "" || tokenBody["userId"] != "observer" {
		t.Fatalf("token body=%s", encoded)
	}
	if len(protocols) != 1 || protocols[0] != "private-one-time-token" {
		t.Fatalf("subprotocols=%#v", protocols)
	}
	output, err := os.ReadFile(invocation.ResponseFile)
	if err != nil || !bytes.Contains(output, []byte(`"Type":"MESSAGE"`)) || bytes.Contains(output, []byte("private-one-time-token")) || bytes.Contains(result.Output, []byte("private")) {
		t.Fatalf("output=%q result=%q err=%v", output, result.Output, err)
	}
}

func TestAWSIVSChatActionsAreCapabilityBoundAndPaced(t *testing.T) {
	clientBody := map[string]any{
		"room_identifier": "arn:aws:ivschat:us-west-2:123456789012:room/test-room", "user_id": "sender",
		"attributes": map[string]string{"displayName": "Agent"},
		"messages": []any{
			map[string]any{"action": "SEND_MESSAGE", "content": "first", "request_id": "one"},
			map[string]any{"action": "SEND_MESSAGE", "content": "second", "attributes": map[string]string{"kind": "test"}, "request_id": "two"},
		}, "max_messages": 1, "timeout_seconds": 10,
	}
	plan, err := parseAWSIVSChatPlan("ClientChat", "us-west-2", clientBody)
	if err != nil || len(plan.Capabilities) != 1 || plan.Capabilities[0] != "SEND_MESSAGE" || len(plan.Actions) != 2 {
		t.Fatalf("plan=%#v err=%v", plan, err)
	}
	moderation, err := parseAWSIVSChatPlan("ModerateChat", "us-west-2", map[string]any{
		"room_identifier": "arn:aws:ivschat:us-west-2:123456789012:room/test-room", "user_id": "moderator",
		"messages": []any{
			map[string]any{"action": "DELETE_MESSAGE", "id": "message-1"},
			map[string]any{"action": "DISCONNECT_USER", "user_id": "bad-user", "reason": "policy"},
		}, "max_messages": 1, "timeout_seconds": 10,
	})
	if err != nil || strings.Join(moderation.Capabilities, ",") != "DELETE_MESSAGE,DISCONNECT_USER" {
		t.Fatalf("moderation=%#v err=%v", moderation, err)
	}
	for index, body := range []any{
		map[string]any{"room_identifier": "arn:aws:ivschat:us-west-2:123456789012:room/test-room", "user_id": "sender", "messages": []any{map[string]any{"action": "DELETE_MESSAGE", "id": "x"}}, "max_messages": 1, "timeout_seconds": 1},
		map[string]any{"room_identifier": "arn:aws:ivschat:us-west-2:123456789012:room/test-room", "user_id": "moderator", "messages": []any{map[string]any{"action": "SEND_MESSAGE", "content": "x"}}, "max_messages": 1, "timeout_seconds": 1},
		map[string]any{"room_identifier": "arn:aws:ivschat:us-west-2:123456789012:room/test-room", "user_id": "sender", "messages": []any{map[string]any{"action": "SEND_MESSAGE", "content": strings.Repeat("x", 501)}}, "max_messages": 1, "timeout_seconds": 1},
	} {
		operation := "ClientChat"
		if index == 1 {
			operation = "ModerateChat"
		}
		if _, err := parseAWSIVSChatPlan(operation, "us-west-2", body); err == nil {
			t.Fatalf("invalid plan %d accepted", index)
		}
	}
}

func TestAWSIVSChatProviderErrorsAndCredentialDataLeaveNoOutput(t *testing.T) {
	for name, frame := range map[string]string{
		"provider error":  `{"Type":"ERROR","ErrorCode":403,"ErrorMessage":"private echoed input","Id":"message-1","RequestId":"request-1"}`,
		"credential data": `{"Type":"MESSAGE","Id":"message-1","Content":"hello","Sender":{"UserId":"sender","Attributes":{"access_token":"private"}},"SendTime":"2026-08-03T12:00:01Z"}`,
	} {
		t.Run(name, func(t *testing.T) {
			now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
			connection := &fakeTencentWebSocketConnection{reads: [][]byte{[]byte(frame)}, readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageText}}
			adapter := NewAWSRESTAdapter(AWSRESTConfig{
				Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKID", SecretAccessKey: "secret"}}, Now: func() time.Time { return now },
				HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"token":"private-token","tokenExpirationTime":"2026-08-03T12:01:00Z","sessionExpirationTime":"2026-08-03T12:30:00Z"}`))}, nil
				}),
				IVSChatWebSocketDial: func(context.Context, string, []string) (cloudWebSocketConnection, error) { return connection, nil },
			})
			directory := t.TempDir()
			invocation := validAWSIVSChatInvocation(directory)
			if _, err := adapter.Invoke(t.Context(), invocation); err == nil || strings.Contains(err.Error(), "private") {
				t.Fatalf("unsafe error=%v", err)
			}
			if _, err := os.Stat(invocation.ResponseFile); !os.IsNotExist(err) {
				t.Fatalf("failed call published output: %v", err)
			}
		})
	}
}

func TestAWSIVSChatClientChatPacesWritesAndCollectsMessages(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	var tokenCapabilities []string
	httpDoer := doerFunc(func(request *http.Request) (*http.Response, error) {
		encoded, _ := io.ReadAll(request.Body)
		request.Body = io.NopCloser(bytes.NewReader(encoded))
		var tokenBody map[string]any
		if json.Unmarshal(encoded, &tokenBody) != nil {
			t.Fatal("token request is not JSON")
		}
		for _, item := range tokenBody["capabilities"].([]any) {
			capability, _ := item.(string)
			tokenCapabilities = append(tokenCapabilities, capability)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"X-Amzn-Requestid": []string{"token-req-1"}}, Body: io.NopCloser(strings.NewReader(`{"token":"private-one-time-token","tokenExpirationTime":"2026-08-03T12:10:00Z","sessionExpirationTime":"2026-08-03T12:30:00Z"}`))}, nil
	})
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			[]byte(`{"Type":"MESSAGE","Id":"message-1","RequestId":"request-one","Content":"first","Sender":{"UserId":"sender"},"SendTime":"2026-08-03T12:00:01Z","Attributes":{}}`),
			[]byte(`{"Type":"EVENT","Id":"event-1","RequestId":"request-two","EventName":"MessageSent","SendTime":"2026-08-03T12:00:02Z","Attributes":{"kind":"test"}}`),
		},
		readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageText, cloudWebSocketMessageText},
	}
	var protocols []string
	paces := 0
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret", SessionToken: "private-session"}},
		HTTP:        httpDoer,
		Now:         func() time.Time { return now },
		StreamPause: func(context.Context, time.Duration) error { paces++; return nil },
		IVSChatWebSocketDial: func(_ context.Context, rawURL string, values []string) (cloudWebSocketConnection, error) {
			if rawURL != "wss://edge.ivschat.us-west-2.amazonaws.com" {
				t.Fatalf("URL=%s", rawURL)
			}
			protocols = append([]string(nil), values...)
			return connection, nil
		},
	})
	directory := t.TempDir()
	invocation := validAWSIVSChatInvocation(directory)
	invocation.Mode, invocation.Operation = ModeMutate, "ClientChat"
	invocation.Body = map[string]any{
		"room_identifier": "arn:aws:ivschat:us-west-2:123456789012:room/test-room", "user_id": "sender",
		"attributes": map[string]string{"displayName": "Agent"},
		"messages": []any{
			map[string]any{"action": "SEND_MESSAGE", "content": "first", "request_id": "one"},
			map[string]any{"action": "SEND_MESSAGE", "content": "second", "attributes": map[string]string{"kind": "test"}, "request_id": "two"},
		},
		"max_messages": 2, "timeout_seconds": 10,
	}
	result, err := adapter.Invoke(t.Context(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(tokenCapabilities, ",") != "SEND_MESSAGE" {
		t.Fatalf("token capabilities=%#v", tokenCapabilities)
	}
	if len(protocols) != 1 || protocols[0] != "private-one-time-token" {
		t.Fatalf("subprotocols=%#v", protocols)
	}
	if len(connection.writes) != 2 || paces != 1 {
		t.Fatalf("writes=%d paces=%d", len(connection.writes), paces)
	}
	for _, want := range []string{`"Action":"SEND_MESSAGE"`, `"RequestId":"one"`, `"Content":"first"`} {
		if !bytes.Contains(connection.writes[0].data, []byte(want)) {
			t.Fatalf("write[0] missing %s: %s", want, connection.writes[0].data)
		}
	}
	if !bytes.Contains(connection.writes[1].data, []byte(`"RequestId":"two"`)) || !bytes.Contains(connection.writes[1].data, []byte(`"Attributes":{"kind":"test"}`)) {
		t.Fatalf("write[1]=%s", connection.writes[1].data)
	}
	output, err := os.ReadFile(invocation.ResponseFile)
	if err != nil || !bytes.Contains(output, []byte("message-1")) || !bytes.Contains(output, []byte("event-1")) || bytes.Contains(output, []byte("private")) {
		t.Fatalf("output=%q err=%v", output, err)
	}
	if result.RequestID != "request-two" {
		t.Fatalf("request id=%q", result.RequestID)
	}
}

func TestAWSIVSChatModerateChatWritesCapabilityBoundActions(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	var tokenCapabilities []string
	httpDoer := doerFunc(func(request *http.Request) (*http.Response, error) {
		encoded, _ := io.ReadAll(request.Body)
		request.Body = io.NopCloser(bytes.NewReader(encoded))
		var tokenBody map[string]any
		_ = json.Unmarshal(encoded, &tokenBody)
		for _, item := range tokenBody["capabilities"].([]any) {
			capability, _ := item.(string)
			tokenCapabilities = append(tokenCapabilities, capability)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"X-Amzn-Requestid": []string{"token-req-m"}}, Body: io.NopCloser(strings.NewReader(`{"token":"private-token","tokenExpirationTime":"2026-08-03T12:10:00Z","sessionExpirationTime":"2026-08-03T12:30:00Z"}`))}, nil
	})
	connection := &fakeTencentWebSocketConnection{
		reads:     [][]byte{[]byte(`{"Type":"MESSAGE","Id":"mod-echo","RequestId":"mod-1","Content":"ok","Sender":{"UserId":"sender"},"SendTime":"2026-08-03T12:00:01Z"}`)},
		readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageText},
	}
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKID", SecretAccessKey: "secret"}},
		HTTP:        httpDoer,
		Now:         func() time.Time { return now },
		IVSChatWebSocketDial: func(context.Context, string, []string) (cloudWebSocketConnection, error) {
			return connection, nil
		},
	})
	directory := t.TempDir()
	invocation := validAWSIVSChatInvocation(directory)
	invocation.Mode, invocation.Operation = ModeMutate, "ModerateChat"
	invocation.Body = map[string]any{
		"room_identifier": "arn:aws:ivschat:us-west-2:123456789012:room/test-room", "user_id": "moderator",
		"messages": []any{
			map[string]any{"action": "DELETE_MESSAGE", "id": "message-1", "reason": "policy"},
			map[string]any{"action": "DISCONNECT_USER", "user_id": "bad-user", "reason": "abuse"},
		}, "max_messages": 1, "timeout_seconds": 10,
	}
	if _, err := adapter.Invoke(t.Context(), invocation); err != nil {
		t.Fatal(err)
	}
	if strings.Join(tokenCapabilities, ",") != "DELETE_MESSAGE,DISCONNECT_USER" {
		t.Fatalf("token capabilities=%#v", tokenCapabilities)
	}
	if len(connection.writes) != 2 {
		t.Fatalf("writes=%d", len(connection.writes))
	}
	for _, want := range []string{`"Action":"DELETE_MESSAGE"`, `"Id":"message-1"`, `"Reason":"policy"`} {
		if !bytes.Contains(connection.writes[0].data, []byte(want)) {
			t.Fatalf("write[0] missing %s: %s", want, connection.writes[0].data)
		}
	}
	for _, want := range []string{`"Action":"DISCONNECT_USER"`, `"UserId":"bad-user"`, `"Reason":"abuse"`} {
		if !bytes.Contains(connection.writes[1].data, []byte(want)) {
			t.Fatalf("write[1] missing %s: %s", want, connection.writes[1].data)
		}
	}
	output, err := os.ReadFile(invocation.ResponseFile)
	if err != nil || !bytes.Contains(output, []byte("mod-echo")) || bytes.Contains(output, []byte("private")) {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

type failingAWSIVSChatWriteConnection struct {
	fakeTencentWebSocketConnection
}

func (*failingAWSIVSChatWriteConnection) Write(context.Context, tencentWebSocketMessageType, []byte) error {
	return errors.New("write blocked")
}

func TestAWSIVSChatInvokeFailuresPublishNothing(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	validToken := `{"token":"private-token","tokenExpirationTime":"2026-08-03T12:10:00Z","sessionExpirationTime":"2026-08-03T12:30:00Z"}`
	run := func(name string, adapter *AWSRESTAdapter) {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			invocation := validAWSIVSChatInvocation(directory)
			if _, err := adapter.Invoke(t.Context(), invocation); err == nil {
				t.Fatal("expected failure")
			}
			if _, err := os.Stat(invocation.ResponseFile); !os.IsNotExist(err) {
				t.Fatalf("failure published output: %v", err)
			}
		})
	}
	run("token HTTP 403", NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKID", SecretAccessKey: "secret"}},
		Now:         func() time.Time { return now },
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 403, Header: http.Header{"X-Amzn-Requestid": []string{"req"}}, Body: io.NopCloser(strings.NewReader(`{"message":"denied"}`))}, nil
		}),
		IVSChatWebSocketDial: func(context.Context, string, []string) (cloudWebSocketConnection, error) {
			return &fakeTencentWebSocketConnection{}, nil
		},
	}))
	run("token transport error", NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKID", SecretAccessKey: "secret"}},
		Now:         func() time.Time { return now },
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("transport blocked")
		}),
		IVSChatWebSocketDial: func(context.Context, string, []string) (cloudWebSocketConnection, error) {
			return &fakeTencentWebSocketConnection{}, nil
		},
	}))
	run("token invalid JSON", NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKID", SecretAccessKey: "secret"}},
		Now:         func() time.Time { return now },
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`not-json`))}, nil
		}),
		IVSChatWebSocketDial: func(context.Context, string, []string) (cloudWebSocketConnection, error) {
			return &fakeTencentWebSocketConnection{}, nil
		},
	}))
	run("token missing expiry", NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKID", SecretAccessKey: "secret"}},
		Now:         func() time.Time { return now },
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"token":"t"}`))}, nil
		}),
		IVSChatWebSocketDial: func(context.Context, string, []string) (cloudWebSocketConnection, error) {
			return &fakeTencentWebSocketConnection{}, nil
		},
	}))
	oversizedToken := strings.Repeat("x", 16*1024+10)
	run("token oversized", NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKID", SecretAccessKey: "secret"}},
		Now:         func() time.Time { return now },
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			body := `{"token":"` + oversizedToken + `","tokenExpirationTime":"2026-08-03T12:10:00Z","sessionExpirationTime":"2026-08-03T12:30:00Z"}`
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		}),
		IVSChatWebSocketDial: func(context.Context, string, []string) (cloudWebSocketConnection, error) {
			return &fakeTencentWebSocketConnection{}, nil
		},
	}))
	run("dial failure", NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKID", SecretAccessKey: "secret"}},
		Now:         func() time.Time { return now },
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(validToken))}, nil
		}),
		IVSChatWebSocketDial: func(context.Context, string, []string) (cloudWebSocketConnection, error) {
			return nil, errors.New("handshake blocked")
		},
	}))
	run("binary frame", NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKID", SecretAccessKey: "secret"}},
		Now:         func() time.Time { return now },
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(validToken))}, nil
		}),
		IVSChatWebSocketDial: func(context.Context, string, []string) (cloudWebSocketConnection, error) {
			return &fakeTencentWebSocketConnection{reads: [][]byte{[]byte("binary")}, readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageBinary}}, nil
		},
	}))
	run("write failure", NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKID", SecretAccessKey: "secret"}},
		Now:         func() time.Time { return now },
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(validToken))}, nil
		}),
		IVSChatWebSocketDial: func(context.Context, string, []string) (cloudWebSocketConnection, error) {
			return &failingAWSIVSChatWriteConnection{}, nil
		},
	}))

	t.Run("read interruption leaves no output", func(t *testing.T) {
		directory := t.TempDir()
		adapter := NewAWSRESTAdapter(AWSRESTConfig{
			Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKID", SecretAccessKey: "secret"}},
			Now:         func() time.Time { return now },
			HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(validToken))}, nil
			}),
			IVSChatWebSocketDial: func(context.Context, string, []string) (cloudWebSocketConnection, error) {
				return &fakeTencentWebSocketConnection{reads: [][]byte{[]byte(`{"Type":"MESSAGE","Id":"message-1","SendTime":"2026-08-03T12:00:01Z"}`)}, readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageText}}, nil
			},
		})
		invocation := validAWSIVSChatInvocation(directory)
		invocation.Mode, invocation.Operation = ModeMutate, "ClientChat"
		invocation.Body = map[string]any{
			"room_identifier": "arn:aws:ivschat:us-west-2:123456789012:room/test-room", "user_id": "sender",
			"messages":     []any{map[string]any{"action": "SEND_MESSAGE", "content": "hello", "request_id": "one"}},
			"max_messages": 2, "timeout_seconds": 10,
		}
		if _, err := adapter.Invoke(t.Context(), invocation); err == nil {
			t.Fatal("expected failure on interrupted collection")
		}
		if _, err := os.Stat(invocation.ResponseFile); !os.IsNotExist(err) {
			t.Fatalf("interrupted collection published output: %v", err)
		}
	})
}

func TestAWSIVSChatValidationBoundedFields(t *testing.T) {
	baseBody := func() map[string]any {
		return map[string]any{
			"room_identifier": "arn:aws:ivschat:us-west-2:123456789012:room/test-room",
			"user_id":         "observer", "max_messages": 1, "timeout_seconds": 10,
		}
	}
	good := func(operation string, body map[string]any) {
		t.Helper()
		if _, err := parseAWSIVSChatPlan(operation, "us-west-2", body); err != nil {
			t.Fatalf("valid plan rejected: %v", err)
		}
	}
	bad := func(operation string, body map[string]any, fragment string) {
		t.Helper()
		if _, err := parseAWSIVSChatPlan(operation, "us-west-2", body); err == nil || !strings.Contains(err.Error(), fragment) {
			t.Fatalf("expected error containing %q, got %v", fragment, err)
		}
	}
	good("SubscribeChat", baseBody())
	sendBody := func() map[string]any {
		body := baseBody()
		body["messages"] = []any{map[string]any{"action": "SEND_MESSAGE", "content": "hello", "request_id": "one"}}
		return body
	}
	good("ClientChat", sendBody())
	good("ClientChat", map[string]any{
		"room_identifier": "arn:aws:ivschat:us-west-2:123456789012:room/test-room", "user_id": "sender",
		"messages":     []any{map[string]any{"action": "SEND_MESSAGE", "content": strings.Repeat("你", 500), "request_id": "one"}},
		"max_messages": 1, "timeout_seconds": 10,
	})

	withAttributes := func(attributes map[string]string) map[string]any {
		body := sendBody()
		body["attributes"] = attributes
		return body
	}
	bad("ClientChat", withAttributes(map[string]string{"k": strings.Repeat("v", 1200)}), "1 KB")
	bad("ClientChat", withAttributes(map[string]string{strings.Repeat("k", 129): "v"}), "attribute key")
	bad("ClientChat", withAttributes(map[string]string{"k": strings.Repeat("v", 501)}), "attribute value")
	bad("ClientChat", withAttributes(map[string]string{"k": "line\nbreak"}), "control characters")

	bad("SubscribeChat", func() map[string]any {
		body := baseBody()
		body["user_id"] = strings.Repeat("u", 129)
		return body
	}(), "user_id")
	bad("SubscribeChat", func() map[string]any {
		body := baseBody()
		body["user_id"] = "user\x00id"
		return body
	}(), "control characters")
	bad("ClientChat", func() map[string]any {
		body := sendBody()
		body["messages"] = []any{map[string]any{"action": "SEND_MESSAGE", "content": strings.Repeat("x", 501), "request_id": "one"}}
		return body
	}(), "content")
	bad("ClientChat", func() map[string]any {
		body := sendBody()
		body["messages"] = []any{map[string]any{"action": "SEND_MESSAGE", "content": "hello", "request_id": "bad\x01id"}}
		return body
	}(), "request_id")
	bad("SubscribeChat", func() map[string]any {
		body := baseBody()
		body["session_duration_minutes"] = 181
		return body
	}(), "session_duration_minutes")
	bad("SubscribeChat", func() map[string]any {
		body := baseBody()
		body["max_messages"] = 0
		return body
	}(), "max_messages 1..256")
	bad("SubscribeChat", func() map[string]any {
		body := baseBody()
		body["max_messages"] = 257
		return body
	}(), "max_messages 1..256")
	bad("SubscribeChat", func() map[string]any {
		body := baseBody()
		body["timeout_seconds"] = 301
		return body
	}(), "max_messages 1..256")

	for name, mutate := range map[string]func(map[string]any){
		"wrong region ARN": func(body map[string]any) {
			body["room_identifier"] = "arn:aws:ivschat:us-east-1:123456789012:room/test-room"
		},
		"non-digit account": func(body map[string]any) {
			body["room_identifier"] = "arn:aws:ivschat:us-west-2:12345678901a:room/test-room"
		},
		"invalid room id": func(body map[string]any) {
			body["room_identifier"] = "arn:aws:ivschat:us-west-2:123456789012:room/test_room"
		},
		"oversized ARN": func(body map[string]any) {
			body["room_identifier"] = "arn:aws:ivschat:us-west-2:123456789012:room/" + strings.Repeat("a", 100)
		},
		"short ARN": func(body map[string]any) { body["room_identifier"] = "arn:aws:ivschat:us-west-2:123456789012" },
	} {
		t.Run(name, func(t *testing.T) {
			body := baseBody()
			mutate(body)
			if _, err := parseAWSIVSChatPlan("SubscribeChat", "us-west-2", body); err == nil {
				t.Fatal("invalid room ARN accepted")
			}
		})
	}

	bad("SubscribeChat", func() map[string]any {
		body := baseBody()
		body["messages"] = []any{map[string]any{"action": "SEND_MESSAGE", "content": "hello"}}
		return body
	}(), "does not accept publish actions")
	bad("ClientChat", func() map[string]any {
		body := sendBody()
		body["messages"] = []any{map[string]any{"action": "PURGE", "content": "hello"}}
		return body
	}(), "action must be")
	bad("ClientChat", func() map[string]any {
		body := sendBody()
		body["messages"] = []any{map[string]any{"action": "SEND_MESSAGE", "content": "hello", "id": "message-1"}}
		return body
	}(), "SEND_MESSAGE is allowed only by ClientChat")
	bad("ModerateChat", func() map[string]any {
		body := baseBody()
		body["messages"] = []any{map[string]any{"action": "DELETE_MESSAGE", "id": "message-1", "content": "x"}}
		return body
	}(), "DELETE_MESSAGE is allowed only by ModerateChat")
	bad("ModerateChat", func() map[string]any {
		body := baseBody()
		body["messages"] = []any{map[string]any{"action": "DISCONNECT_USER", "user_id": "bad", "id": "message-1"}}
		return body
	}(), "DISCONNECT_USER is allowed only by ModerateChat")
	bad("SubscribeChat", func() map[string]any {
		body := baseBody()
		body["token"] = "caller-token"
		return body
	}(), "credential-free")
	bad("SubscribeChat", func() map[string]any {
		body := baseBody()
		body["bogus"] = true
		return body
	}(), "protocol schema")

	for name, mutate := range map[string]func(*Invocation){
		"port":          func(value *Invocation) { value.URL = "wss://edge.ivschat.us-west-2.amazonaws.com:8443" },
		"http scheme":   func(value *Invocation) { value.URL = "http://edge.ivschat.us-west-2.amazonaws.com" },
		"query":         func(value *Invocation) { value.URL = "wss://edge.ivschat.us-west-2.amazonaws.com?x=1" },
		"non-root path": func(value *Invocation) { value.URL = "wss://edge.ivschat.us-west-2.amazonaws.com/chat" },
		"host mismatch": func(value *Invocation) { value.URL = "wss://edge.ivschat.us-east-1.amazonaws.com" },
		"bad region": func(value *Invocation) {
			value.Region, value.URL = "ca-central-1", "wss://edge.ivschat.ca-central-1.amazonaws.com"
		},
	} {
		t.Run("endpoint "+name, func(t *testing.T) {
			directory := t.TempDir()
			invocation := validAWSIVSChatInvocation(directory)
			mutate(&invocation)
			if err := validateAWSIVSChatInvocation(invocation); err == nil {
				t.Fatal("invalid endpoint accepted")
			}
		})
	}
}

func TestDefaultAWSIVSChatDialNegotiatesTokenSubprotocol(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := coderwebsocket.Accept(writer, request, &coderwebsocket.AcceptOptions{Subprotocols: []string{"private-one-time-token"}})
		if err != nil {
			return
		}
		defer connection.Close(coderwebsocket.StatusNormalClosure, "done")
		_ = connection.Write(request.Context(), coderwebsocket.MessageText, []byte(`{"Type":"MESSAGE","Id":"m1","SendTime":"2026-08-03T12:00:01Z"}`))
	}))
	defer server.Close()
	connection, err := defaultAWSIVSChatWebSocketDial(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http"), []string{"private-one-time-token"})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if messageType, data, err := connection.Read(t.Context()); err != nil || messageType != cloudWebSocketMessageText || !bytes.Contains(data, []byte("m1")) {
		t.Fatalf("type=%d data=%s err=%v", messageType, data, err)
	}
}

func TestDefaultAWSIVSChatDialRejectsBadSubprotocolAndNonUpgrade(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"wrong subprotocol": func(writer http.ResponseWriter, request *http.Request) {
			connection, err := coderwebsocket.Accept(writer, request, &coderwebsocket.AcceptOptions{Subprotocols: []string{"other-subprotocol"}})
			if err != nil {
				return
			}
			connection.Close(coderwebsocket.StatusNormalClosure, "done")
		},
		"non-upgrade response": func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write([]byte("not a websocket"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(handler)
			defer server.Close()
			if _, err := defaultAWSIVSChatWebSocketDial(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http"), []string{"private-one-time-token"}); err == nil {
				t.Fatal("expected dial failure")
			}
		})
	}
}

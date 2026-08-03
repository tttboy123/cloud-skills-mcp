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
)

func TestAWSSigV4WebSocketBoundaryRequiresGuardedMutationSession(t *testing.T) {
	directory := t.TempDir()
	valid := func() Invocation {
		return Invocation{
			Provider: ProviderAWS, Mode: ModeMutate, AuthScheme: "sigv4-ws", Service: "bedrock-agentcore", Operation: "InvokeAgentRuntimeWithWebSocketStream",
			Region: "us-west-2", Method: http.MethodGet,
			URL:     "wss://bedrock-agentcore.us-west-2.amazonaws.com/runtimes/arn%3Aaws%3Abedrock-agentcore%3Aus-west-2%3A123456789012%3Aruntime%2Fagent-abc/ws?qualifier=prod",
			Headers: map[string]string{"X-Amzn-Bedrock-AgentCore-Runtime-Session-Id": "session-123456789012345678901234567890"},
			Body: map[string]any{
				"messages": []any{
					map[string]any{"type": "json", "data": map[string]any{"inputText": "hello"}},
				},
				"max_messages": 2, "timeout_seconds": 30,
			},
			ResponseFile: filepath.Join(directory, "agentcore.ndjson"),
		}
	}
	base := valid()
	if err := validateInvocation(base, []string{directory}); err != nil {
		t.Fatal(err)
	}
	if classifyRead(ProviderAWS, base) {
		t.Fatal("generic signed WebSocket was incorrectly classified read-only")
	}
	custom := valid()
	custom.URL = "wss://runtime.example.com/ws"
	if err := validateInvocationWithEndpointHosts(custom, []string{directory}, []string{"runtime.example.com"}); err != nil {
		t.Fatalf("approved custom WebSocket endpoint rejected: %v", err)
	}

	for name, mutate := range map[string]func(*Invocation){
		"wrong method":      func(value *Invocation) { value.Method = http.MethodPost },
		"missing service":   func(value *Invocation) { value.Service = "" },
		"missing operation": func(value *Invocation) { value.Operation = "" },
		"invalid region":    func(value *Invocation) { value.Region = "bad region" },
		"https URL":         func(value *Invocation) { value.URL = strings.Replace(value.URL, "wss://", "https://", 1) },
		"wrong host":        func(value *Invocation) { value.URL = "wss://example.com/ws" },
		"wrong port":        func(value *Invocation) { value.URL = "wss://bedrock-agentcore.us-west-2.amazonaws.com:8443/ws" },
		"userinfo":          func(value *Invocation) { value.URL = "wss://user@bedrock-agentcore.us-west-2.amazonaws.com/ws" },
		"fragment":          func(value *Invocation) { value.URL += "#fragment" },
		"signed query":      func(value *Invocation) { value.URL += "&X-Amz-Signature=caller" },
		"bearer query":      func(value *Invocation) { value.Parameters = map[string]any{"access_token": "caller"} },
		"upgrade header":    func(value *Invocation) { value.Headers = map[string]string{"Upgrade": "websocket"} },
		"websocket header":  func(value *Invocation) { value.Headers = map[string]string{"Sec-WebSocket-Protocol": "caller"} },
		"credential header": func(value *Invocation) { value.Headers = map[string]string{"X-Custom-Api-Key": "caller"} },
		"body file":         func(value *Invocation) { value.BodyFile = filepath.Join(directory, "input") },
		"missing output":    func(value *Invocation) { value.ResponseFile = "" },
		"missing body":      func(value *Invocation) { value.Body = nil },
		"credential body": func(value *Invocation) {
			value.Body = map[string]any{"messages": []any{map[string]any{"type": "json", "data": map[string]any{"access_token": "forbidden"}}}, "max_messages": 1, "timeout_seconds": 1}
		},
		"oversized AgentCore frame": func(value *Invocation) {
			value.Body = map[string]any{"messages": []any{map[string]any{"type": "text", "data": strings.Repeat("x", awsAgentCoreMaxFrameBytes+1)}}, "max_messages": 1, "timeout_seconds": 1}
		},
		"unbounded messages": func(value *Invocation) {
			value.Body = map[string]any{"messages": []any{}, "max_messages": 257, "timeout_seconds": 1}
		},
		"unbounded timeout": func(value *Invocation) {
			value.Body = map[string]any{"messages": []any{}, "max_messages": 1, "timeout_seconds": 301}
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid()
			if name == "body file" {
				if err := os.WriteFile(filepath.Join(directory, "input"), []byte("data"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			mutate(&candidate)
			if err := validateInvocation(candidate, []string{directory}); err == nil {
				t.Fatal("unsafe AWS SigV4 WebSocket invocation accepted")
			}
		})
	}
}

func TestAWSSigV4WebSocketSignsHandshakeHeadersWithoutExposingCredentials(t *testing.T) {
	now := time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC)
	credentials := AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "private-secret", SessionToken: "private-session-token"}
	target, headers, err := signAWSSigV4WebSocketHandshake(t.Context(),
		"wss://bedrock-agentcore.us-west-2.amazonaws.com/runtimes/runtime-id/ws",
		map[string]any{"qualifier": "prod"},
		map[string]string{"X-Amzn-Bedrock-AgentCore-Runtime-Session-Id": "session-123456789012345678901234567890"},
		credentials, "bedrock-agentcore", "us-west-2", now)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	authorization := headers.Get("Authorization")
	if parsed.Scheme != "wss" || parsed.Query().Get("qualifier") != "prod" || parsed.Query().Get("X-Amz-Signature") != "" {
		t.Fatalf("signed target=%q", target)
	}
	if !strings.Contains(authorization, "/us-west-2/bedrock-agentcore/aws4_request") || !strings.Contains(strings.ToLower(authorization), "x-amzn-bedrock-agentcore-runtime-session-id") {
		t.Fatalf("authorization=%q", authorization)
	}
	if headers.Get("X-Amz-Security-Token") != "private-session-token" || headers.Get("X-Amzn-Bedrock-AgentCore-Runtime-Session-Id") == "" {
		t.Fatalf("headers=%#v", headers)
	}
	serializedHeaders, err := json.Marshal(headers)
	if err != nil {
		t.Fatal(err)
	}
	serialized := target + string(serializedHeaders)
	if strings.Contains(serialized, credentials.SecretAccessKey) {
		t.Fatal("secret access key leaked into signed handshake")
	}
}

func TestAWSSigV4WebSocketMessagePlanRejectsMalformedFrames(t *testing.T) {
	valid := []awsSigV4WebSocketMessage{
		{Type: "json", Data: json.RawMessage(`{"value":1}`)},
		{Type: "text", Data: json.RawMessage(`"hello"`)},
		{Type: "binary", DataBase64: "AQID"},
	}
	for index := range valid {
		message := valid[index]
		if err := prepareAWSSigV4WebSocketMessage(&message); err != nil || len(message.payload) == 0 {
			t.Fatalf("valid frame %d rejected: %v", index, err)
		}
	}
	for name, message := range map[string]awsSigV4WebSocketMessage{
		"unknown type":     {Type: "control", Data: json.RawMessage(`{}`)},
		"missing json":     {Type: "json"},
		"json with binary": {Type: "json", Data: json.RawMessage(`{}`), DataBase64: "AQ=="},
		"non-string text":  {Type: "text", Data: json.RawMessage(`1`)},
		"text with binary": {Type: "text", Data: json.RawMessage(`"x"`), DataBase64: "AQ=="},
		"binary with data": {Type: "binary", Data: json.RawMessage(`"x"`), DataBase64: "AQ=="},
		"invalid base64":   {Type: "binary", DataBase64: "***"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := prepareAWSSigV4WebSocketMessage(&message); err == nil {
				t.Fatal("malformed WebSocket frame accepted")
			}
		})
	}
	if _, _, err := signAWSSigV4WebSocketHandshake(t.Context(), "https://example.com/ws", nil, nil, AWSCredentials{AccessKeyID: "ak", SecretAccessKey: "sk"}, "service", "us-east-1", time.Now()); err == nil {
		t.Fatal("non-WSS signing target accepted")
	}
	if _, _, err := signAWSSigV4WebSocketHandshake(t.Context(), "wss://service.us-east-1.amazonaws.com/ws", map[string]any{"bad": map[string]any{}}, nil, AWSCredentials{AccessKeyID: "ak", SecretAccessKey: "sk"}, "service", "us-east-1", time.Now()); err == nil {
		t.Fatal("non-scalar signing parameter accepted")
	}
}

func TestAWSAdapterRunsFiniteSigV4WebSocketPlanWithAtomicOutput(t *testing.T) {
	directory := t.TempDir()
	responseFile := filepath.Join(directory, "websocket.ndjson")
	connection := &fakeTencentWebSocketConnection{
		reads:     [][]byte{[]byte(`{"ok":true}`), {0x01, 0x02, 0x03}},
		readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageText, cloudWebSocketMessageBinary},
	}
	var target string
	var handshake http.Header
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "private-secret", SessionToken: "private-session-token"}},
		Now:         func() time.Time { return time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC) },
		SigV4WebSocketDial: func(_ context.Context, rawURL string, headers http.Header) (cloudWebSocketConnection, error) {
			target = rawURL
			handshake = headers.Clone()
			return connection, nil
		},
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, Mode: ModeMutate, AuthScheme: "sigv4-ws", Service: "bedrock-agentcore", Operation: "InvokeAgentRuntimeWithWebSocketStream",
		Region: "us-west-2", Method: http.MethodGet, URL: "wss://bedrock-agentcore.us-west-2.amazonaws.com/runtimes/runtime-id/ws",
		Headers: map[string]string{"X-Amzn-Bedrock-AgentCore-Runtime-Session-Id": "session-123456789012345678901234567890"},
		Body: map[string]any{
			"messages": []any{
				map[string]any{"type": "json", "data": map[string]any{"inputText": "hello"}},
				map[string]any{"type": "text", "data": "next"},
				map[string]any{"type": "binary", "data_base64": "BAUG"},
			},
			"max_messages": 2, "timeout_seconds": 30,
		},
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(target, "wss://bedrock-agentcore.us-west-2.amazonaws.com/") || handshake.Get("Authorization") == "" || handshake.Get("X-Amz-Security-Token") != "private-session-token" {
		t.Fatalf("target=%q handshake=%#v", target, handshake)
	}
	if strings.Contains(string(result.Output), "Authorization") || strings.Contains(string(result.Output), "private-session-token") || strings.Contains(string(result.Output), "X-Amz-") {
		t.Fatalf("unsafe result=%s", result.Output)
	}
	if len(connection.writes) != 3 || connection.writes[0].messageType != cloudWebSocketMessageText || string(connection.writes[0].data) != `{"inputText":"hello"}` || string(connection.writes[1].data) != "next" || !bytes.Equal(connection.writes[2].data, []byte{4, 5, 6}) {
		t.Fatalf("writes=%#v", connection.writes)
	}
	data, err := os.ReadFile(responseFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte{'\n'})
	if len(lines) != 2 || !bytes.Contains(lines[0], []byte(`"type":"text"`)) || !bytes.Contains(lines[0], []byte(`"data":"{\"ok\":true}"`)) || !bytes.Contains(lines[1], []byte(`"data_base64":"AQID"`)) || bytes.Contains(data, []byte("private-session-token")) {
		t.Fatalf("NDJSON=%q", data)
	}
	var summary map[string]any
	if json.Unmarshal(result.Output, &summary) != nil || summary["messages"] != float64(2) {
		t.Fatalf("summary=%s", result.Output)
	}
}

func TestAWSSigV4WebSocketProtocolFailureLeavesNoOutput(t *testing.T) {
	directory := t.TempDir()
	responseFile := filepath.Join(directory, "websocket.ndjson")
	connection := &fakeTencentWebSocketConnection{reads: [][]byte{[]byte("invalid")}, readTypes: []tencentWebSocketMessageType{99}}
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "private-secret"}},
		SigV4WebSocketDial: func(context.Context, string, http.Header) (cloudWebSocketConnection, error) {
			return connection, nil
		},
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, Mode: ModeMutate, AuthScheme: "sigv4-ws", Service: "managedblockchain", Operation: "SubscribeEthereum",
		Region: "us-east-1", Method: http.MethodGet, URL: "wss://node.wss.ethereum.managedblockchain.us-east-1.amazonaws.com/",
		Body: map[string]any{"messages": []any{}, "max_messages": 1, "timeout_seconds": 1}, ResponseFile: responseFile,
	})
	if err == nil {
		t.Fatal("invalid WebSocket message type succeeded")
	}
	if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
		t.Fatalf("failed session published output: %v", statErr)
	}
}

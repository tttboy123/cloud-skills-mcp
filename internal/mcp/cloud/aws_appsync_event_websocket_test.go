package cloud

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAWSAppSyncEventWebSocketBoundaryAllowsOnlyFiniteIAMSubscriptions(t *testing.T) {
	directory := t.TempDir()
	valid := func() Invocation {
		return Invocation{
			Provider: ProviderAWS, Mode: ModeRead, AuthScheme: "appsync-event-ws", Service: "appsync", Operation: "EventSubscribe",
			Region: "us-east-1", Method: http.MethodGet, URL: "wss://api-id.appsync-realtime-api.us-east-1.amazonaws.com/event/realtime",
			Body:         map[string]any{"channel": "/news/latest", "max_messages": 2, "timeout_seconds": 30},
			ResponseFile: filepath.Join(directory, "events.ndjson"),
		}
	}
	base := valid()
	if err := validateInvocation(base, []string{directory}); err != nil {
		t.Fatal(err)
	}
	if !classifyRead(ProviderAWS, base) {
		t.Fatal("finite AppSync Event subscription was not classified read-only")
	}
	custom := valid()
	custom.URL = "wss://events.example.com/event/realtime"
	if err := validateInvocationWithEndpointHosts(custom, []string{directory}, []string{"events.example.com"}); err != nil {
		t.Fatalf("approved custom AppSync endpoint rejected: %v", err)
	}

	for name, mutate := range map[string]func(*Invocation){
		"wrong service":   func(value *Invocation) { value.Service = "execute-api" },
		"wrong operation": func(value *Invocation) { value.Operation = "EventPublish" },
		"wrong method":    func(value *Invocation) { value.Method = http.MethodPost },
		"wrong host":      func(value *Invocation) { value.URL = "wss://example.com/event/realtime" },
		"wrong region":    func(value *Invocation) { value.Region = "eu-west-1" },
		"wrong path": func(value *Invocation) {
			value.URL = "wss://api-id.appsync-realtime-api.us-east-1.amazonaws.com/graphql"
		},
		"caller query":   func(value *Invocation) { value.URL += "?header=caller" },
		"caller headers": func(value *Invocation) { value.Headers = map[string]string{"Authorization": "caller"} },
		"missing output": func(value *Invocation) { value.ResponseFile = "" },
		"credential body": func(value *Invocation) {
			value.Body = map[string]any{"channel": "/news/latest", "token": "forbidden", "max_messages": 1, "timeout_seconds": 1}
		},
		"bad channel": func(value *Invocation) {
			value.Body = map[string]any{"channel": "/news/*", "max_messages": 1, "timeout_seconds": 1}
		},
		"unbounded": func(value *Invocation) {
			value.Body = map[string]any{"channel": "/news/latest", "max_messages": 257, "timeout_seconds": 1}
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid()
			mutate(&candidate)
			if err := validateInvocation(candidate, []string{directory}); err == nil {
				t.Fatal("invalid AppSync Event invocation accepted")
			}
		})
	}
}

func TestAWSAppSyncEventIAMAuthorizationSignsConnectionAndChannelBodies(t *testing.T) {
	now := time.Date(2026, 8, 3, 3, 4, 5, 0, time.UTC)
	credentials := AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret", SessionToken: "private-session-token"}
	connect, err := signAWSAppSyncEventAuthorization(t.Context(), "https://api-id.appsync-api.us-east-1.amazonaws.com/event", []byte("{}"), credentials, "us-east-1", now)
	if err != nil {
		t.Fatal(err)
	}
	subscribe, err := signAWSAppSyncEventAuthorization(t.Context(), "https://api-id.appsync-api.us-east-1.amazonaws.com/event", []byte(`{"channel":"/news/latest"}`), credentials, "us-east-1", now)
	if err != nil {
		t.Fatal(err)
	}
	if connect["host"] != "api-id.appsync-api.us-east-1.amazonaws.com" || connect["x-amz-date"] != "20260803T030405Z" || connect["X-Amz-Security-Token"] != "private-session-token" {
		t.Fatalf("connect authorization=%#v", connect)
	}
	if !strings.Contains(connect["Authorization"], "/us-east-1/appsync/aws4_request") || connect["Authorization"] == subscribe["Authorization"] {
		t.Fatalf("body-specific AppSync signatures connect=%q subscribe=%q", connect["Authorization"], subscribe["Authorization"])
	}
	encoded, err := encodeAWSAppSyncEventAuthProtocol(connect)
	if err != nil || !strings.HasPrefix(encoded, "header-") {
		t.Fatalf("auth subprotocol=%q err=%v", encoded, err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(encoded, "header-"))
	if err != nil || !bytes.Contains(decoded, []byte("private-session-token")) || bytes.Contains(decoded, []byte(credentials.SecretAccessKey)) {
		t.Fatalf("decoded auth protocol=%q err=%v", decoded, err)
	}
}

func TestAWSAdapterCollectsFiniteAppSyncEventsWithoutExposingAuthorization(t *testing.T) {
	directory := t.TempDir()
	responseFile := filepath.Join(directory, "events.ndjson")
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			[]byte(`{"type":"connection_ack","connectionTimeoutMs":300000}`),
			[]byte(`{"type":"subscribe_success","id":"subscription-1"}`),
			[]byte(`{"type":"ka"}`),
			[]byte(`{"type":"data","id":"subscription-1","event":["{\"headline\":\"one\"}"]}`),
			[]byte(`{"type":"data","id":"subscription-1","event":["{\"headline\":\"two\"}"]}`),
			[]byte(`{"type":"unsubscribe_success","id":"subscription-1"}`),
		},
		readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageText, cloudWebSocketMessageText, cloudWebSocketMessageText, cloudWebSocketMessageText, cloudWebSocketMessageText, cloudWebSocketMessageText},
	}
	var target string
	var subprotocols []string
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret", SessionToken: "private-session-token"}},
		Now:         func() time.Time { return time.Date(2026, 8, 3, 3, 4, 5, 0, time.UTC) },
		AppSyncEventID: func() (string, error) {
			return "subscription-1", nil
		},
		AppSyncEventWebSocketDial: func(_ context.Context, rawURL string, protocols []string) (cloudWebSocketConnection, error) {
			target = rawURL
			subprotocols = append([]string(nil), protocols...)
			return connection, nil
		},
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, AuthScheme: "appsync-event-ws", Service: "appsync", Operation: "EventSubscribe",
		Region: "us-east-1", Method: http.MethodGet, URL: "wss://api-id.appsync-realtime-api.us-east-1.amazonaws.com/event/realtime",
		Body:         map[string]any{"channel": "/news/latest", "max_messages": 2, "timeout_seconds": 30},
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if target != "wss://api-id.appsync-realtime-api.us-east-1.amazonaws.com/event/realtime" || len(subprotocols) != 2 || subprotocols[0] != "aws-appsync-event-ws" || !strings.HasPrefix(subprotocols[1], "header-") {
		t.Fatalf("target=%q subprotocols=%#v", target, subprotocols)
	}
	if strings.Contains(string(result.Output), "private-session-token") || strings.Contains(string(result.Output), "Authorization") || result.RequestID != "subscription-1" {
		t.Fatalf("unsafe result=%#v", result)
	}
	data, err := os.ReadFile(responseFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte{'\n'})
	if len(lines) != 2 || !bytes.Contains(lines[0], []byte("headline")) || bytes.Contains(data, []byte("private-session-token")) {
		t.Fatalf("NDJSON=%q", data)
	}
	if len(connection.writes) != 3 {
		t.Fatalf("writes=%#v", connection.writes)
	}
	var subscribe map[string]any
	if json.Unmarshal(connection.writes[1].data, &subscribe) != nil || subscribe["type"] != "subscribe" || subscribe["id"] != "subscription-1" {
		t.Fatalf("subscribe=%q", connection.writes[1].data)
	}
	if !bytes.Contains(connection.writes[2].data, []byte(`"type":"unsubscribe"`)) {
		t.Fatalf("unsubscribe=%q", connection.writes[2].data)
	}
}

func TestAWSAppSyncEventProtocolFailuresLeaveNoOutput(t *testing.T) {
	for name, reads := range map[string][][]byte{
		"subscription rejected": {
			[]byte(`{"type":"connection_ack","connectionTimeoutMs":300000}`),
			[]byte(`{"type":"subscribe_error","id":"subscription-1","errors":[{"message":"denied"}]}`),
		},
		"event is not a stringified JSON value": {
			[]byte(`{"type":"connection_ack","connectionTimeoutMs":300000}`),
			[]byte(`{"type":"subscribe_success","id":"subscription-1"}`),
			[]byte(`{"type":"data","id":"subscription-1","event":[{"unsafe":true}]}`),
			[]byte(`{"type":"unsubscribe_success","id":"subscription-1"}`),
		},
		"wrong subscription id": {
			[]byte(`{"type":"connection_ack","connectionTimeoutMs":300000}`),
			[]byte(`{"type":"subscribe_success","id":"subscription-1"}`),
			[]byte(`{"type":"data","id":"another-id","event":["{}"]}`),
		},
	} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			responseFile := filepath.Join(directory, "events.ndjson")
			connection := &fakeTencentWebSocketConnection{reads: reads, readTypes: make([]tencentWebSocketMessageType, len(reads))}
			for index := range connection.readTypes {
				connection.readTypes[index] = cloudWebSocketMessageText
			}
			adapter := NewAWSRESTAdapter(AWSRESTConfig{
				Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret"}},
				Now:         func() time.Time { return time.Date(2026, 8, 3, 3, 4, 5, 0, time.UTC) },
				AppSyncEventID: func() (string, error) {
					return "subscription-1", nil
				},
				AppSyncEventWebSocketDial: func(context.Context, string, []string) (cloudWebSocketConnection, error) {
					return connection, nil
				},
			})
			_, err := adapter.Invoke(t.Context(), Invocation{
				Provider: ProviderAWS, AuthScheme: "appsync-event-ws", Service: "appsync", Operation: "EventSubscribe",
				Region: "us-east-1", Method: http.MethodGet, URL: "wss://api-id.appsync-realtime-api.us-east-1.amazonaws.com/event/realtime",
				Body: map[string]any{"channel": "/news/latest", "max_messages": 1, "timeout_seconds": 30}, ResponseFile: responseFile,
			})
			if err == nil {
				t.Fatal("invalid AppSync protocol sequence succeeded")
			}
			if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
				t.Fatalf("failed session published output: %v", statErr)
			}
		})
	}
}

func TestAWSAppSyncEventSchemaAndGeneratedIDAreBounded(t *testing.T) {
	for index, body := range []any{
		nil,
		map[string]any{"channel": "/news/latest", "unknown": true, "max_messages": 1, "timeout_seconds": 1},
		map[string]any{"channel": "/news/latest/segment/segment/segment/extra", "max_messages": 1, "timeout_seconds": 1},
		map[string]any{"channel": "/news/latest", "max_messages": 0, "timeout_seconds": 1},
		map[string]any{"channel": "/news/latest", "max_messages": 1, "timeout_seconds": 301},
	} {
		if _, err := parseAWSAppSyncEventSubscribeConfig(body); err == nil {
			t.Fatalf("invalid body %d accepted", index)
		}
	}
	id, err := newAWSAppSyncEventID()
	if err != nil || !awsAppSyncEventIDPattern.MatchString(id) || len(id) != 32 {
		t.Fatalf("generated id=%q err=%v", id, err)
	}
}

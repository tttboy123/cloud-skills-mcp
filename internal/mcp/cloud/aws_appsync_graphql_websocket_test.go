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

func TestAWSAppSyncGraphQLWebSocketBoundaryAllowsOnlyFiniteIAMSubscriptions(t *testing.T) {
	directory := t.TempDir()
	valid := func() Invocation {
		return Invocation{
			Provider: ProviderAWS, Mode: ModeRead, AuthScheme: "appsync-graphql-ws", Service: "appsync", Operation: "GraphQLSubscribe",
			Region: "us-east-1", Method: http.MethodGet, URL: "wss://api-id.appsync-realtime-api.us-east-1.amazonaws.com/graphql",
			Body: map[string]any{
				"query":     "subscription OnMessage($room: ID!) { onMessage(room: $room) { id text } }",
				"variables": map[string]any{"room": "room-1"}, "max_messages": 2, "timeout_seconds": 30,
			},
			ResponseFile: filepath.Join(directory, "graphql.ndjson"),
		}
	}
	base := valid()
	if err := validateInvocation(base, []string{directory}); err != nil {
		t.Fatal(err)
	}
	if !classifyRead(ProviderAWS, base) {
		t.Fatal("finite AppSync GraphQL subscription was not classified read-only")
	}
	custom := valid()
	custom.URL = "wss://graphql.example.com/graphql/realtime"
	if err := validateInvocationWithEndpointHosts(custom, []string{directory}, []string{"graphql.example.com"}); err != nil {
		t.Fatalf("approved custom GraphQL endpoint rejected: %v", err)
	}

	for name, mutate := range map[string]func(*Invocation){
		"wrong service":   func(value *Invocation) { value.Service = "execute-api" },
		"wrong operation": func(value *Invocation) { value.Operation = "GraphQLQuery" },
		"wrong method":    func(value *Invocation) { value.Method = http.MethodPost },
		"wrong host":      func(value *Invocation) { value.URL = "wss://example.com/graphql" },
		"wrong region":    func(value *Invocation) { value.Region = "eu-west-1" },
		"wrong path": func(value *Invocation) {
			value.URL = "wss://api-id.appsync-realtime-api.us-east-1.amazonaws.com/graphql/realtime"
		},
		"caller query":   func(value *Invocation) { value.URL += "?header=caller" },
		"caller headers": func(value *Invocation) { value.Headers = map[string]string{"Authorization": "caller"} },
		"missing output": func(value *Invocation) { value.ResponseFile = "" },
		"mutation": func(value *Invocation) {
			value.Body = map[string]any{"query": "mutation Update { updateMessage { id } }", "variables": map[string]any{}, "max_messages": 1, "timeout_seconds": 1}
		},
		"multiple operations": func(value *Invocation) {
			value.Body = map[string]any{"query": "subscription A { a } query B { b }", "variables": map[string]any{}, "max_messages": 1, "timeout_seconds": 1}
		},
		"credential variables": func(value *Invocation) {
			value.Body = map[string]any{"query": "subscription A { a }", "variables": map[string]any{"access_token": "forbidden"}, "max_messages": 1, "timeout_seconds": 1}
		},
		"unbounded": func(value *Invocation) {
			value.Body = map[string]any{"query": "subscription A { a }", "variables": map[string]any{}, "max_messages": 257, "timeout_seconds": 1}
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid()
			mutate(&candidate)
			if err := validateInvocation(candidate, []string{directory}); err == nil {
				t.Fatal("invalid AppSync GraphQL invocation accepted")
			}
		})
	}
}

func TestAWSAppSyncGraphQLIAMAuthorizationSignsConnectAndSubscriptionRequests(t *testing.T) {
	now := time.Date(2026, 8, 3, 4, 5, 6, 0, time.UTC)
	credentials := AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret", SessionToken: "private-session-token"}
	connect, err := signAWSAppSyncGraphQLAuthorization(t.Context(), "https://api-id.appsync-api.us-east-1.amazonaws.com/graphql/connect", []byte("{}"), credentials, "us-east-1", now)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"query":"subscription A { a }","variables":{}}`)
	subscribe, err := signAWSAppSyncGraphQLAuthorization(t.Context(), "https://api-id.appsync-api.us-east-1.amazonaws.com/graphql", data, credentials, "us-east-1", now)
	if err != nil {
		t.Fatal(err)
	}
	if connect["host"] != "api-id.appsync-api.us-east-1.amazonaws.com" || connect["x-amz-date"] != "20260803T040506Z" || connect["X-Amz-Security-Token"] != "private-session-token" {
		t.Fatalf("connect authorization=%#v", connect)
	}
	if !strings.Contains(connect["Authorization"], "/us-east-1/appsync/aws4_request") || connect["Authorization"] == subscribe["Authorization"] {
		t.Fatalf("request-specific signatures connect=%q subscribe=%q", connect["Authorization"], subscribe["Authorization"])
	}
	encoded, err := encodeAWSAppSyncAuthProtocol(connect)
	if err != nil || !strings.HasPrefix(encoded, "header-") {
		t.Fatalf("auth subprotocol=%q err=%v", encoded, err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(encoded, "header-"))
	if err != nil || !bytes.Contains(decoded, []byte("private-session-token")) || bytes.Contains(decoded, []byte(credentials.SecretAccessKey)) {
		t.Fatalf("decoded auth protocol=%q err=%v", decoded, err)
	}
}

func TestAWSAppSyncGraphQLDocumentValidationHandlesStringsCommentsAndFragments(t *testing.T) {
	for name, query := range map[string]string{
		"quoted operation words": `subscription S { message(filter: "query mutation subscription") }`,
		"escaped quote":          `subscription S { message(filter: "a\"b") }`,
		"block string":           `subscription S { message(filter: """query { ignored }""") }`,
		"comment and fragment":   "# query Ignored { no }\nsubscription S { ...Fields }\nfragment Fields on Subscription { message }",
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateAWSAppSyncGraphQLSubscription(query); err != nil {
				t.Fatal(err)
			}
		})
	}

	for name, query := range map[string]string{
		"unterminated string":       `subscription S { message(filter: "unterminated) }`,
		"unterminated block string": `subscription S { message(filter: """unterminated) }`,
		"newline in string":         "subscription S { message(filter: \"bad\nvalue\") }",
		"stray close":               `subscription S { message } }`,
		"shorthand query":           `{ message }`,
		"unknown definition":        `schema { subscription: Subscription }`,
		"fragment only":             `fragment Fields on Subscription { message }`,
		"duplicate subscription":    `subscription A { a } subscription B { b }`,
		"leading whitespace":        ` subscription S { message }`,
		"control character":         "subscription S { message }\x00",
		"invalid utf8":              string([]byte{'s', 'u', 'b', 0xff}),
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateAWSAppSyncGraphQLSubscription(query); err == nil {
				t.Fatal("unsafe or malformed GraphQL document accepted")
			}
		})
	}
}

func TestAWSAdapterCollectsFiniteAppSyncGraphQLDataWithoutExposingAuthorization(t *testing.T) {
	directory := t.TempDir()
	responseFile := filepath.Join(directory, "graphql.ndjson")
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			[]byte(`{"type":"connection_ack","payload":{"connectionTimeoutMs":300000}}`),
			[]byte(`{"type":"start_ack","id":"subscription-1"}`),
			[]byte(`{"type":"ka"}`),
			[]byte(`{"type":"data","id":"subscription-1","payload":{"data":{"onMessage":{"id":"1","text":"one"}}}}`),
			[]byte(`{"type":"data","id":"subscription-1","payload":{"data":{"onMessage":{"id":"2","text":"two"}}}}`),
			[]byte(`{"type":"complete","id":"subscription-1"}`),
		},
		readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageText, cloudWebSocketMessageText, cloudWebSocketMessageText, cloudWebSocketMessageText, cloudWebSocketMessageText, cloudWebSocketMessageText},
	}
	var target string
	var subprotocols []string
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret", SessionToken: "private-session-token"}},
		Now:         func() time.Time { return time.Date(2026, 8, 3, 4, 5, 6, 0, time.UTC) },
		AppSyncGraphQLID: func() (string, error) {
			return "subscription-1", nil
		},
		AppSyncGraphQLWebSocketDial: func(_ context.Context, rawURL string, protocols []string) (cloudWebSocketConnection, error) {
			target = rawURL
			subprotocols = append([]string(nil), protocols...)
			return connection, nil
		},
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, AuthScheme: "appsync-graphql-ws", Service: "appsync", Operation: "GraphQLSubscribe",
		Region: "us-east-1", Method: http.MethodGet, URL: "wss://api-id.appsync-realtime-api.us-east-1.amazonaws.com/graphql",
		Body: map[string]any{
			"query":     "subscription OnMessage($room: ID!) { onMessage(room: $room) { id text } }",
			"variables": map[string]any{"room": "room-1"}, "max_messages": 2, "timeout_seconds": 30,
		},
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if target != "wss://api-id.appsync-realtime-api.us-east-1.amazonaws.com/graphql" || len(subprotocols) != 2 || subprotocols[0] != "graphql-ws" || !strings.HasPrefix(subprotocols[1], "header-") {
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
	if len(lines) != 2 || !bytes.Contains(lines[0], []byte(`"text":"one"`)) || bytes.Contains(data, []byte("private-session-token")) {
		t.Fatalf("NDJSON=%q", data)
	}
	if len(connection.writes) != 3 {
		t.Fatalf("writes=%#v", connection.writes)
	}
	var start struct {
		Type    string `json:"type"`
		ID      string `json:"id"`
		Payload struct {
			Data       string `json:"data"`
			Extensions struct {
				Authorization map[string]string `json:"authorization"`
			} `json:"extensions"`
		} `json:"payload"`
	}
	if json.Unmarshal(connection.writes[1].data, &start) != nil || start.Type != "start" || start.ID != "subscription-1" || !strings.Contains(start.Payload.Data, "subscription OnMessage") || start.Payload.Extensions.Authorization["X-Amz-Security-Token"] != "private-session-token" {
		t.Fatalf("start=%q", connection.writes[1].data)
	}
	if !bytes.Contains(connection.writes[2].data, []byte(`"type":"stop"`)) {
		t.Fatalf("stop=%q", connection.writes[2].data)
	}
}

func TestAWSAppSyncGraphQLProtocolFailuresLeaveNoOutput(t *testing.T) {
	for name, reads := range map[string][][]byte{
		"subscription rejected": {
			[]byte(`{"type":"connection_ack","payload":{"connectionTimeoutMs":300000}}`),
			[]byte(`{"type":"error","id":"subscription-1","payload":{"errors":[{"message":"denied"}]}}`),
		},
		"wrong subscription id": {
			[]byte(`{"type":"connection_ack","payload":{"connectionTimeoutMs":300000}}`),
			[]byte(`{"type":"start_ack","id":"subscription-1"}`),
			[]byte(`{"type":"data","id":"another-id","payload":{"data":{"a":1}}}`),
		},
		"non-object payload": {
			[]byte(`{"type":"connection_ack","payload":{"connectionTimeoutMs":300000}}`),
			[]byte(`{"type":"start_ack","id":"subscription-1"}`),
			[]byte(`{"type":"data","id":"subscription-1","payload":"unsafe"}`),
			[]byte(`{"type":"complete","id":"subscription-1"}`),
		},
	} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			responseFile := filepath.Join(directory, "graphql.ndjson")
			connection := &fakeTencentWebSocketConnection{reads: reads, readTypes: make([]tencentWebSocketMessageType, len(reads))}
			for index := range connection.readTypes {
				connection.readTypes[index] = cloudWebSocketMessageText
			}
			adapter := NewAWSRESTAdapter(AWSRESTConfig{
				Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret"}},
				Now:         func() time.Time { return time.Date(2026, 8, 3, 4, 5, 6, 0, time.UTC) },
				AppSyncGraphQLID: func() (string, error) {
					return "subscription-1", nil
				},
				AppSyncGraphQLWebSocketDial: func(context.Context, string, []string) (cloudWebSocketConnection, error) {
					return connection, nil
				},
			})
			_, err := adapter.Invoke(t.Context(), Invocation{
				Provider: ProviderAWS, AuthScheme: "appsync-graphql-ws", Service: "appsync", Operation: "GraphQLSubscribe",
				Region: "us-east-1", Method: http.MethodGet, URL: "wss://api-id.appsync-realtime-api.us-east-1.amazonaws.com/graphql",
				Body: map[string]any{"query": "subscription A { a }", "variables": map[string]any{}, "max_messages": 1, "timeout_seconds": 30}, ResponseFile: responseFile,
			})
			if err == nil {
				t.Fatal("invalid AppSync GraphQL protocol sequence succeeded")
			}
			if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
				t.Fatalf("failed session published output: %v", statErr)
			}
		})
	}
}

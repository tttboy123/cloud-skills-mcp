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

func TestAWSIoTMQTTWebSocketBoundaryAllowsOnlyFiniteIAMSubscriptions(t *testing.T) {
	directory := t.TempDir()
	valid := func() Invocation {
		return Invocation{
			Provider: ProviderAWS, Mode: ModeRead, AuthScheme: "iot-mqtt-ws", Service: "iotdevicegateway", Operation: "SubscribeMQTT",
			Region: "us-west-2", Method: http.MethodGet, URL: "wss://account-prefix-ats.iot.us-west-2.amazonaws.com/mqtt",
			Body: map[string]any{
				"client_id": "observer-1", "max_messages": 2, "timeout_seconds": 30,
				"subscriptions": []any{
					map[string]any{"topic_filter": "sensors/+/temperature", "qos": 0},
					map[string]any{"topic_filter": "$aws/things/device/shadow/update/delta", "qos": 1},
				},
			},
			ResponseFile: filepath.Join(directory, "messages.ndjson"),
		}
	}
	base := valid()
	if err := validateInvocation(base, []string{directory}); err != nil {
		t.Fatal(err)
	}
	if !classifyRead(ProviderAWS, base) {
		t.Fatal("finite MQTT subscription was not classified read-only")
	}
	custom := valid()
	custom.URL = "wss://mqtt.example.com/mqtt"
	if err := validateInvocationWithEndpointHosts(custom, []string{directory}, []string{"mqtt.example.com"}); err != nil {
		t.Fatalf("approved custom IoT endpoint rejected: %v", err)
	}

	for name, mutate := range map[string]func(*Invocation){
		"wrong service":    func(value *Invocation) { value.Service = "iot" },
		"wrong operation":  func(value *Invocation) { value.Operation = "PublishMQTT" },
		"wrong method":     func(value *Invocation) { value.Method = http.MethodPost },
		"wrong host":       func(value *Invocation) { value.URL = "wss://example.com/mqtt" },
		"wrong region":     func(value *Invocation) { value.Region = "eu-west-1" },
		"wrong path":       func(value *Invocation) { value.URL = "wss://account-prefix-ats.iot.us-west-2.amazonaws.com/other" },
		"caller signature": func(value *Invocation) { value.URL += "?X-Amz-Signature=caller" },
		"caller headers":   func(value *Invocation) { value.Headers = map[string]string{"Sec-WebSocket-Protocol": "mqtt"} },
		"query parameters": func(value *Invocation) { value.Parameters = map[string]any{"token": "caller"} },
		"missing output":   func(value *Invocation) { value.ResponseFile = "" },
		"body file":        func(value *Invocation) { value.BodyFile = filepath.Join(directory, "input") },
		"stream control":   func(value *Invocation) { value.StreamIntervalMS = 1 },
		"credential body": func(value *Invocation) {
			value.Body = map[string]any{"client_id": "observer-1", "access_token": "forbidden", "subscriptions": []any{map[string]any{"topic_filter": "a", "qos": 0}}, "max_messages": 1, "timeout_seconds": 30}
		},
		"bad wildcard": func(value *Invocation) {
			value.Body = map[string]any{"client_id": "observer-1", "subscriptions": []any{map[string]any{"topic_filter": "a/#/b", "qos": 0}}, "max_messages": 1, "timeout_seconds": 30}
		},
		"unbounded messages": func(value *Invocation) {
			value.Body = map[string]any{"client_id": "observer-1", "subscriptions": []any{map[string]any{"topic_filter": "a", "qos": 0}}, "max_messages": 257, "timeout_seconds": 30}
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid()
			mutate(&candidate)
			if err := validateInvocation(candidate, []string{directory}); err == nil {
				t.Fatal("invalid AWS IoT MQTT WebSocket invocation accepted")
			}
		})
	}
}

func TestAWSIoTMQTTWebSocketPresignOmitsSessionTokenFromCanonicalQuery(t *testing.T) {
	now := time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC)
	base := AWSCredentials{AccessKeyID: "AKIAIOSFODNN7EXAMPLE", SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", SessionToken: "session-one"}
	first, err := signAWSIoTMQTTWebSocketURL("wss://account-prefix-ats.iot.us-west-2.amazonaws.com/mqtt", base, "us-west-2", now)
	if err != nil {
		t.Fatal(err)
	}
	base.SessionToken = "session-two"
	second, err := signAWSIoTMQTTWebSocketURL("wss://account-prefix-ats.iot.us-west-2.amazonaws.com/mqtt", base, "us-west-2", now)
	if err != nil {
		t.Fatal(err)
	}
	firstURL, _ := url.Parse(first)
	secondURL, _ := url.Parse(second)
	firstQuery, secondQuery := firstURL.Query(), secondURL.Query()
	if firstURL.Scheme != "wss" || firstQuery.Get("X-Amz-Algorithm") != "AWS4-HMAC-SHA256" || firstQuery.Get("X-Amz-Expires") != "300" || firstQuery.Get("X-Amz-SignedHeaders") != "host" {
		t.Fatalf("signed query=%q", firstURL.RawQuery)
	}
	if firstQuery.Get("X-Amz-Security-Token") != "session-one" || secondQuery.Get("X-Amz-Security-Token") != "session-two" {
		t.Fatal("temporary session token was not appended to the internal URL")
	}
	if firstQuery.Get("X-Amz-Signature") == "" || firstQuery.Get("X-Amz-Signature") != secondQuery.Get("X-Amz-Signature") {
		t.Fatalf("session token affected canonical signature: first=%q second=%q", firstQuery.Get("X-Amz-Signature"), secondQuery.Get("X-Amz-Signature"))
	}
	// Generated independently with the official AWS CRT query signer using
	// omit_session_token=true and the same fixed credentials and timestamp.
	if firstQuery.Get("X-Amz-Signature") != "eb2d37419795d0501c7549c3bbf51ab1d6e4cc1884fe3faeac658d12a6a3b644" {
		t.Fatalf("signature does not match the AWS CRT query-signing vector: %q", firstQuery.Get("X-Amz-Signature"))
	}
	if strings.Contains(first, base.SecretAccessKey) {
		t.Fatal("secret access key leaked into signed URL")
	}
}

func TestAWSAdapterCollectsFiniteIoTMQTTSubscriptionWithoutExposingSignedURL(t *testing.T) {
	directory := t.TempDir()
	responseFile := filepath.Join(directory, "messages.ndjson")
	firstPublish := testMQTTPublishPacket("sensors/room1/temperature", 1, 7, []byte{0x00, 0x01, 0x02})
	secondPublish := testMQTTPublishPacket("sensors/room2/temperature", 0, 0, []byte(`{"value":21}`))
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			append([]byte{0x20, 0x02, 0x00, 0x00}, []byte{0x90, 0x04, 0x00, 0x01, 0x00, 0x01}...),
			firstPublish[:5], firstPublish[5:], secondPublish,
		},
		readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageBinary, cloudWebSocketMessageBinary, cloudWebSocketMessageBinary, cloudWebSocketMessageBinary},
	}
	var signedTarget string
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret", SessionToken: "private-session-token"}},
		Now:         func() time.Time { return time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC) },
		IoTWebSocketDial: func(_ context.Context, target string) (cloudWebSocketConnection, error) {
			signedTarget = target
			return connection, nil
		},
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, Mode: ModeRead, AuthScheme: "iot-mqtt-ws", Service: "iotdevicegateway", Operation: "SubscribeMQTT",
		Region: "us-west-2", Method: http.MethodGet, URL: "wss://account-prefix-ats.iot.us-west-2.amazonaws.com/mqtt",
		Body: map[string]any{
			"client_id": "observer-1", "max_messages": 2, "timeout_seconds": 30,
			"subscriptions": []any{
				map[string]any{"topic_filter": "sensors/+/temperature", "qos": 0},
				map[string]any{"topic_filter": "$aws/things/device/shadow/update/delta", "qos": 1},
			},
		},
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(result.Output), "private-session-token") || strings.Contains(string(result.Output), "X-Amz-Signature") || result.RequestID != "observer-1" {
		t.Fatalf("unsafe result=%#v", result)
	}
	parsed, err := url.Parse(signedTarget)
	if err != nil || parsed.Query().Get("X-Amz-Signature") == "" || parsed.Query().Get("X-Amz-Security-Token") != "private-session-token" {
		t.Fatalf("internal target=%q err=%v", signedTarget, err)
	}
	data, err := os.ReadFile(responseFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte{'\n'})
	if len(lines) != 2 {
		t.Fatalf("NDJSON=%q", data)
	}
	var first map[string]any
	if json.Unmarshal(lines[0], &first) != nil || first["topic"] != "sensors/room1/temperature" || first["payload_base64"] != base64.StdEncoding.EncodeToString([]byte{0x00, 0x01, 0x02}) || first["qos"] != float64(1) {
		t.Fatalf("first message=%s", lines[0])
	}
	if len(connection.writes) != 4 || connection.writes[0].data[0] != 0x10 || connection.writes[1].data[0] != 0x82 || !bytes.Equal(connection.writes[2].data, []byte{0x40, 0x02, 0x00, 0x07}) || !bytes.Equal(connection.writes[3].data, []byte{0xe0, 0x00}) {
		t.Fatalf("MQTT writes=%#v", connection.writes)
	}
}

func TestAWSIoTMQTTPacketAndSchemaValidationRejectMalformedInput(t *testing.T) {
	if _, _, complete, err := decodeAWSMQTTPacket([]byte{0x30}); err != nil || complete {
		t.Fatalf("partial packet complete=%v err=%v", complete, err)
	}
	if _, _, _, err := decodeAWSMQTTPacket([]byte{0x30, 0x80, 0x80, 0x80, 0x80}); err == nil {
		t.Fatal("malformed remaining length accepted")
	}
	if _, err := encodeAWSMQTTPacket(0x30, make([]byte, awsIoTMQTTMaxPacketBytes+1)); err == nil {
		t.Fatal("oversized MQTT packet encoded")
	}
	for _, packet := range []awsMQTTPacket{
		{Header: 0x20, Body: []byte{0, 5}},
		{Header: 0x21, Body: []byte{0, 0}},
		{Header: 0x20, Body: []byte{1, 0}},
	} {
		if err := validateAWSIoTMQTTConnAck(packet); err == nil {
			t.Fatalf("invalid CONNACK accepted: %#v", packet)
		}
	}
	for _, packet := range []awsMQTTPacket{
		{Header: 0x90, Body: []byte{0, 2, 0}},
		{Header: 0x90, Body: []byte{0, 1, 0x80}},
	} {
		if err := validateAWSIoTMQTTSubAck(packet, 1); err == nil {
			t.Fatalf("invalid SUBACK accepted: %#v", packet)
		}
	}
	reader := &awsMQTTPacketReader{connection: &fakeTencentWebSocketConnection{reads: [][]byte{[]byte("text")}}}
	if _, err := reader.Read(t.Context()); err == nil || !strings.Contains(err.Error(), "non-binary") {
		t.Fatalf("text MQTT frame error=%v", err)
	}
	reader = &awsMQTTPacketReader{connection: &fakeTencentWebSocketConnection{
		reads:     [][]byte{make([]byte, awsIoTMQTTMaxPacketBytes+1)},
		readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageBinary},
	}}
	if _, err := reader.Read(t.Context()); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized MQTT frame error=%v", err)
	}

	invalidBodies := []any{
		nil,
		map[string]any{"client_id": "observer", "unknown": true, "subscriptions": []any{map[string]any{"topic_filter": "a", "qos": 0}}, "max_messages": 1, "timeout_seconds": 1},
		map[string]any{"client_id": "bad\nclient", "subscriptions": []any{map[string]any{"topic_filter": "a", "qos": 0}}, "max_messages": 1, "timeout_seconds": 1},
		map[string]any{"client_id": strings.Repeat("a", 129), "subscriptions": []any{map[string]any{"topic_filter": "a", "qos": 0}}, "max_messages": 1, "timeout_seconds": 1},
		map[string]any{"client_id": "observer", "subscriptions": []any{}, "max_messages": 1, "timeout_seconds": 1},
		map[string]any{"client_id": "observer", "subscriptions": []any{map[string]any{"topic_filter": "a+bad", "qos": 0}}, "max_messages": 1, "timeout_seconds": 1},
		map[string]any{"client_id": "observer", "subscriptions": []any{map[string]any{"topic_filter": "a", "qos": 2}}, "max_messages": 1, "timeout_seconds": 1},
		map[string]any{"client_id": "observer", "subscriptions": []any{map[string]any{"topic_filter": "a", "qos": 0}}, "max_messages": 0, "timeout_seconds": 1},
		map[string]any{"client_id": "observer", "subscriptions": []any{map[string]any{"topic_filter": "a", "qos": 0}}, "max_messages": 1, "timeout_seconds": 301},
	}
	for index, body := range invalidBodies {
		if _, err := parseAWSIoTMQTTSubscribeConfig(body); err == nil {
			t.Fatalf("invalid body %d accepted", index)
		}
	}
}

func TestAWSIoTMQTTSubAckCannotGrantHigherQoSThanRequested(t *testing.T) {
	packet := awsMQTTPacket{Header: 0x90, Body: []byte{0x00, 0x01, 0x01}}
	requested := []awsIoTMQTTSubscription{{TopicFilter: "sensors/#", QoS: 0}}
	if err := validateAWSIoTMQTTSubAckForSubscriptions(packet, requested); err == nil {
		t.Fatal("SUBACK granted QoS 1 for a QoS 0 subscription")
	}
}

func TestAWSIoTMQTTRejectedSubscriptionLeavesNoOutput(t *testing.T) {
	directory := t.TempDir()
	responseFile := filepath.Join(directory, "messages.ndjson")
	connection := &fakeTencentWebSocketConnection{
		reads:     [][]byte{{0x20, 0x02, 0x00, 0x00}, {0x90, 0x03, 0x00, 0x01, 0x80}},
		readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageBinary, cloudWebSocketMessageBinary},
	}
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret"}},
		Now:         func() time.Time { return time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC) },
		IoTWebSocketDial: func(context.Context, string) (cloudWebSocketConnection, error) {
			return connection, nil
		},
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, Mode: ModeRead, AuthScheme: "iot-mqtt-ws", Service: "iotdevicegateway", Operation: "SubscribeMQTT",
		Region: "us-west-2", Method: http.MethodGet, URL: "wss://account-prefix-ats.iot.us-west-2.amazonaws.com/mqtt",
		Body: map[string]any{
			"client_id": "observer-1", "subscriptions": []any{map[string]any{"topic_filter": "sensors/#", "qos": 1}}, "max_messages": 1, "timeout_seconds": 30,
		},
		ResponseFile: responseFile,
	})
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("expected rejected subscription, got %v", err)
	}
	if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
		t.Fatalf("failed subscription published output: %v", statErr)
	}
}

func TestAWSIoTMQTTDoesNotExceedMessageLimitBeforeSubAck(t *testing.T) {
	directory := t.TempDir()
	responseFile := filepath.Join(directory, "messages.ndjson")
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			{0x20, 0x02, 0x00, 0x00},
			testMQTTPublishPacket("sensors/one", 0, 0, []byte("one")),
			testMQTTPublishPacket("sensors/two", 0, 0, []byte("two")),
			{0x90, 0x03, 0x00, 0x01, 0x00},
		},
		readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageBinary, cloudWebSocketMessageBinary, cloudWebSocketMessageBinary, cloudWebSocketMessageBinary},
	}
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret"}},
		Now:         func() time.Time { return time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC) },
		IoTWebSocketDial: func(context.Context, string) (cloudWebSocketConnection, error) {
			return connection, nil
		},
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, Mode: ModeRead, AuthScheme: "iot-mqtt-ws", Service: "iotdevicegateway", Operation: "SubscribeMQTT",
		Region: "us-west-2", Method: http.MethodGet, URL: "wss://account-prefix-ats.iot.us-west-2.amazonaws.com/mqtt",
		Body: map[string]any{
			"client_id": "observer-1", "subscriptions": []any{map[string]any{"topic_filter": "sensors/#", "qos": 0}}, "max_messages": 1, "timeout_seconds": 30,
		},
		ResponseFile: responseFile,
	})
	if err == nil || !strings.Contains(err.Error(), "max_messages") {
		t.Fatalf("expected pre-SUBACK message-limit failure, got %v", err)
	}
	if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
		t.Fatalf("over-limit subscription published output: %v", statErr)
	}
}

func testMQTTPublishPacket(topic string, qos byte, packetID uint16, payload []byte) []byte {
	variable := []byte{byte(len(topic) >> 8), byte(len(topic))}
	variable = append(variable, topic...)
	if qos == 1 {
		variable = append(variable, byte(packetID>>8), byte(packetID))
	}
	variable = append(variable, payload...)
	header := byte(0x30 | qos<<1)
	return append([]byte{header, byte(len(variable))}, variable...)
}

package cloud

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAWSIoTMQTTClientBoundarySeparatesReadAndMutationPlans(t *testing.T) {
	directory := t.TempDir()
	mutation := Invocation{
		Provider: ProviderAWS, Mode: ModeMutate, AuthScheme: authSchemeAWSIoTMQTTWS,
		Service: awsIoTMQTTService, Operation: "ClientMQTT", Region: "us-west-2",
		Method: http.MethodGet, URL: "wss://account-prefix-ats.iot.us-west-2.amazonaws.com/mqtt",
		Body: map[string]any{
			"protocol_version": 5, "client_id": "client-5", "clean_start": false,
			"session_expiry_seconds": 3600, "disconnect_session_expiry_seconds": 0,
			"subscriptions":   []any{map[string]any{"topic_filter": "sensors/#", "qos": 1}},
			"unsubscriptions": []any{"legacy/topic"},
			"publishes": []any{map[string]any{
				"topic": "sensors/status", "qos": 1, "retain": true,
				"payload_base64": base64.StdEncoding.EncodeToString([]byte("online")),
				"payload_format": 1, "content_type": "text/plain", "message_expiry_seconds": 60,
				"response_topic": "sensors/replies", "correlation_data_base64": "cmVxLTE=",
				"user_properties": []any{map[string]any{"name": "site", "value": "sfo"}},
			}},
			"will": map[string]any{
				"topic": "sensors/status", "qos": 1, "retain": true,
				"payload_base64": base64.StdEncoding.EncodeToString([]byte("offline")),
				"payload_format": 1, "content_type": "text/plain",
			},
			"max_messages": 1, "timeout_seconds": 30,
		},
		ResponseFile: filepath.Join(directory, "messages.ndjson"),
	}
	if err := validateInvocation(mutation, []string{directory}); err != nil {
		t.Fatal(err)
	}
	if classifyRead(ProviderAWS, mutation) {
		t.Fatal("ClientMQTT was classified read-only")
	}

	wrongTool := mutation
	wrongTool.Mode = ModeRead
	if err := validateInvocation(wrongTool, []string{directory}); err == nil {
		t.Fatal("ClientMQTT accepted through the read tool")
	}
	wrongOperation := mutation
	wrongOperation.Operation = "SubscribeMQTT"
	if err := validateInvocation(wrongOperation, []string{directory}); err == nil {
		t.Fatal("mutating plan accepted as SubscribeMQTT")
	}
	noSubscription := mutation
	noSubscription.Body = map[string]any{
		"protocol_version": 4, "client_id": "publisher-3", "clean_start": true,
		"publishes": []any{map[string]any{"topic": "sensors/status", "qos": 0, "retain": true, "payload_base64": ""}},
	}
	noSubscription.ResponseFile = ""
	if err := validateInvocation(noSubscription, []string{directory}); err != nil {
		t.Fatalf("MQTT 3 retained publish rejected: %v", err)
	}
	noSubscription.ResponseFile = filepath.Join(directory, "unused.ndjson")
	if err := validateInvocation(noSubscription, []string{directory}); err == nil {
		t.Fatal("publish-only plan accepted an unused response_file")
	}
	persistentWithoutBounds := noSubscription
	persistentWithoutBounds.ResponseFile = ""
	persistentWithoutBounds.Body.(map[string]any)["clean_start"] = false
	if err := validateInvocation(persistentWithoutBounds, []string{directory}); err == nil {
		t.Fatal("persistent resume accepted without bounded message collection")
	}
}

func TestAWSIoTMQTTClientEncodesPersistentWillPublishAndDisconnect(t *testing.T) {
	zero := uint32(0)
	plan := awsIoTMQTTSubscribeConfig{
		ProtocolVersion: 5, ClientID: "client-5", CleanStart: false, SessionExpirySeconds: 3600,
		DisconnectSessionExpirySeconds: &zero, MaxMessages: 8, TimeoutSeconds: 30,
		Will: &awsIoTMQTTWill{
			Topic: "status/client-5", QoS: 1, Retain: true, PayloadFormat: awsIoTMQTTTestPointer(1),
			ContentType: "text/plain", payload: []byte("offline"),
		},
	}
	connect, err := encodeAWSIoTMQTTConnect(plan)
	if err != nil {
		t.Fatal(err)
	}
	packet, _, complete, err := decodeAWSMQTTPacket(connect)
	if err != nil || !complete || packet.Header != 0x10 {
		t.Fatalf("CONNECT=%x complete=%v err=%v", connect, complete, err)
	}
	if packet.Body[7] != 0x2c { // Will present, QoS 1, retained; Clean Start is false.
		t.Fatalf("CONNECT flags=%08b", packet.Body[7])
	}
	if !bytes.Contains(packet.Body, []byte{0x11, 0x00, 0x00, 0x0e, 0x10}) {
		t.Fatalf("CONNECT omitted Session Expiry: %x", packet.Body)
	}

	publish := awsIoTMQTTPublish{
		Topic: "events/client-5", QoS: 1, Retain: true, PayloadFormat: awsIoTMQTTTestPointer(1),
		ContentType: "application/json", MessageExpirySeconds: awsIoTMQTTTestPointer(uint32(60)),
		ResponseTopic: "events/replies", correlationData: []byte("req-1"),
		userProperties: []awsIoTMQTTUserProperty{{Name: "site", Value: "sfo"}},
		payload:        []byte(`{"ok":true}`),
	}
	encoded, err := encodeAWSIoTMQTTPublish(plan, publish, 3)
	if err != nil {
		t.Fatal(err)
	}
	packet, _, complete, err = decodeAWSMQTTPacket(encoded)
	if err != nil || !complete || packet.Header != 0x33 || binary.BigEndian.Uint16(packet.Body[2+len(publish.Topic):]) != 3 {
		t.Fatalf("PUBLISH=%x complete=%v err=%v", encoded, complete, err)
	}
	disconnect, err := encodeAWSIoTMQTTDisconnect(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(disconnect, []byte{0xe0, 0x07, 0x00, 0x05, 0x11, 0x00, 0x00, 0x00, 0x00}) {
		t.Fatalf("DISCONNECT=%x", disconnect)
	}
	zeroFormat, zeroExpiry := 0, uint32(0)
	if properties := encodeAWSIoTMQTT5ApplicationProperties(&zeroFormat, "", &zeroExpiry, "", nil, nil); !bytes.Equal(properties, []byte{0x01, 0x00, 0x02, 0x00, 0x00, 0x00, 0x00}) {
		t.Fatalf("explicit zero MQTT 5 properties=%x", properties)
	}
}

func TestAWSIoTMQTTClientRejectsUnsupportedOrUnsafeMutationFeatures(t *testing.T) {
	base := func() map[string]any {
		return map[string]any{
			"protocol_version": 5, "client_id": "client-5",
			"publishes": []any{map[string]any{"topic": "sensors/out", "qos": 1, "payload_base64": "b2s="}},
		}
	}
	for name, mutate := range map[string]func(map[string]any){
		"QoS 2": func(value map[string]any) {
			value["publishes"].([]any)[0].(map[string]any)["qos"] = 2
		},
		"retained reserved topic": func(value map[string]any) {
			publish := value["publishes"].([]any)[0].(map[string]any)
			publish["topic"], publish["retain"] = "$aws/things/x/shadow/update", true
		},
		"MQTT 3 properties": func(value map[string]any) {
			value["protocol_version"] = 4
			value["publishes"].([]any)[0].(map[string]any)["content_type"] = "text/plain"
		},
		"unsupported Will Delay": func(value map[string]any) {
			value["will"] = map[string]any{"topic": "status", "qos": 1, "payload_base64": "b2Zm", "will_delay_seconds": 1}
		},
		"increase expiry on disconnect": func(value map[string]any) {
			value["disconnect_session_expiry_seconds"] = 1
		},
		"unbounded collection": func(value map[string]any) {
			value["clean_start"], value["max_messages"], value["timeout_seconds"] = false, 257, 30
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseAWSIoTMQTTConfig(baseWithAWSIoTMQTTMutation(base(), mutate), true); err == nil {
				t.Fatal("unsupported mutation plan accepted")
			}
		})
	}
}

func TestAWSIoTMQTTClientValidatesBrokerCapabilitiesAndControlReasons(t *testing.T) {
	config := awsIoTMQTTSubscribeConfig{
		ProtocolVersion: 5, ClientID: "client-5", CleanStart: false,
		Unsubscriptions: []string{"legacy/topic"},
	}
	capabilities, err := parseAWSIoTMQTTConnAck(awsMQTTPacket{
		Header: 0x20,
		Body: []byte{
			0x01, 0x00, 0x0c,
			0x24, 0x00, // Maximum QoS 0.
			0x25, 0x00, // Retain unavailable.
			0x21, 0x00, 0x01,
			0x27, 0x00, 0x00, 0x02, 0x00,
		},
	}, config)
	if err != nil || !capabilities.SessionPresent || capabilities.MaximumQoS != 0 || capabilities.RetainAvailable || capabilities.ReceiveMaximum != 1 || capabilities.MaximumPacketSize != 512 {
		t.Fatalf("capabilities=%#v err=%v", capabilities, err)
	}
	if err := validateAWSIoTMQTTUnsubAck(awsMQTTPacket{Header: 0xb0, Body: []byte{0x00, 0x02, 0x00, 0x11}}, config, 2); err != nil {
		t.Fatal(err)
	}
	for _, packet := range []awsMQTTPacket{
		{Header: 0xb0, Body: []byte{0x00, 0x02, 0x00, 0x87}},
		{Header: 0xb0, Body: []byte{0x00, 0x03, 0x00, 0x00}},
	} {
		if err := validateAWSIoTMQTTUnsubAck(packet, config, 2); err == nil {
			t.Fatalf("invalid UNSUBACK accepted: %#v", packet)
		}
	}
	if err := validateAWSIoTMQTTDisconnect(awsMQTTPacket{Header: 0xe0, Body: []byte{0x00, 0x05, 0x11, 0x00, 0x00, 0x00, 0x00}}, 5); err != nil {
		t.Fatal(err)
	}
	if err := validateAWSIoTMQTTDisconnect(awsMQTTPacket{Header: 0xe0, Body: []byte{0x87, 0x00}}, 5); err == nil {
		t.Fatal("broker error DISCONNECT accepted")
	}
}

func baseWithAWSIoTMQTTMutation(value map[string]any, mutate func(map[string]any)) map[string]any {
	mutate(value)
	return value
}

func TestAWSAdapterRunsBoundedMQTT5BidirectionalClient(t *testing.T) {
	directory := t.TempDir()
	responseFile := filepath.Join(directory, "messages.ndjson")
	inbound := testAWSIoTMQTT5PublishPacket("sensors/in", 1, 41, []byte("ack"))
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			{0x20, 0x06, 0x01, 0x00, 0x03, 0x21, 0x00, 0x01}, // resumed; Receive Maximum 1
			{0x90, 0x04, 0x00, 0x01, 0x00, 0x01},             // SUBACK QoS 1
			{0xb0, 0x04, 0x00, 0x02, 0x00, 0x00},             // UNSUBACK success
			inbound,
			{0x40, 0x04, 0x00, 0x03, 0x00, 0x00}, // PUBACK success
		},
		readTypes: []tencentWebSocketMessageType{
			cloudWebSocketMessageBinary, cloudWebSocketMessageBinary, cloudWebSocketMessageBinary,
			cloudWebSocketMessageBinary, cloudWebSocketMessageBinary,
		},
	}
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret"}},
		Now:         func() time.Time { return time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC) },
		StreamPause: func(context.Context, time.Duration) error { return nil },
		IoTWebSocketDial: func(context.Context, string) (cloudWebSocketConnection, error) {
			return connection, nil
		},
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, Mode: ModeMutate, AuthScheme: authSchemeAWSIoTMQTTWS,
		Service: awsIoTMQTTService, Operation: "ClientMQTT", Region: "us-west-2", Method: http.MethodGet,
		URL: "wss://account-prefix-ats.iot.us-west-2.amazonaws.com/mqtt",
		Body: map[string]any{
			"protocol_version": 5, "client_id": "client-5", "clean_start": false, "session_expiry_seconds": 3600,
			"subscriptions":   []any{map[string]any{"topic_filter": "sensors/in", "qos": 1}},
			"unsubscriptions": []any{"legacy/topic"},
			"publishes": []any{map[string]any{
				"topic": "sensors/out", "qos": 1, "retain": true,
				"payload_base64": base64.StdEncoding.EncodeToString([]byte("online")),
			}},
			"max_messages": 1, "timeout_seconds": 30,
		},
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RequestID != "client-5" {
		t.Fatalf("result=%#v", result)
	}
	data, err := os.ReadFile(responseFile)
	if err != nil {
		t.Fatal(err)
	}
	var message map[string]any
	if json.Unmarshal(bytes.TrimSpace(data), &message) != nil || message["topic"] != "sensors/in" || message["payload_base64"] != "YWNr" {
		t.Fatalf("NDJSON=%q", data)
	}
	if len(connection.writes) != 6 {
		t.Fatalf("writes=%#v", connection.writes)
	}
	wantTypes := []byte{1, 8, 10, 3, 4, 14}
	for index, packetType := range wantTypes {
		if connection.writes[index].data[0]>>4 != packetType {
			t.Fatalf("write %d=%x want packet type %d", index, connection.writes[index].data, packetType)
		}
	}
}

func TestAWSAdapterRunsMQTT3PersistentRetainedPublishWithBoundedQueueDrain(t *testing.T) {
	directory := t.TempDir()
	responseFile := filepath.Join(directory, "mqtt3-persistent.ndjson")
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			{0x20, 0x02, 0x01, 0x00},
			testMQTTPublishPacket("sensors/queued", 0, 0, []byte("queued")),
		},
		readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageBinary, cloudWebSocketMessageBinary},
	}
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret"}},
		Now:         func() time.Time { return time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC) },
		IoTWebSocketDial: func(context.Context, string) (cloudWebSocketConnection, error) {
			return connection, nil
		},
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, Mode: ModeMutate, AuthScheme: authSchemeAWSIoTMQTTWS,
		Service: awsIoTMQTTService, Operation: "ClientMQTT", Region: "us-west-2", Method: http.MethodGet,
		URL: "wss://account-prefix-ats.iot.us-west-2.amazonaws.com/mqtt",
		Body: map[string]any{
			"protocol_version": 4, "client_id": "publisher-3", "clean_start": false,
			"keep_alive_seconds": 0,
			"publishes": []any{map[string]any{
				"topic": "sensors/retained", "qos": 0, "retain": true, "payload_base64": "",
			}},
			"max_messages": 1, "timeout_seconds": 30,
		},
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(responseFile)
	if err != nil || !bytes.Contains(data, []byte(`"topic":"sensors/queued"`)) || result.RequestID != "publisher-3" {
		t.Fatalf("response=%q result=%#v err=%v", data, result, err)
	}
	if len(connection.writes) != 3 || connection.writes[0].data[10] != 0 || connection.writes[0].data[11] != 0 || connection.writes[1].data[0] != 0x31 || !bytes.Equal(connection.writes[2].data, []byte{0xe0, 0x00}) {
		t.Fatalf("writes=%#v", connection.writes)
	}
}

func TestAWSIoTMQTTClientSuppressesWillOnPostConnAckValidationFailure(t *testing.T) {
	connection := &fakeTencentWebSocketConnection{
		reads:     [][]byte{{0x20, 0x05, 0x00, 0x00, 0x02, 0x24, 0x00}},
		readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageBinary},
	}
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret"}},
		Now:         func() time.Time { return time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC) },
		IoTWebSocketDial: func(context.Context, string) (cloudWebSocketConnection, error) {
			return connection, nil
		},
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, Mode: ModeMutate, AuthScheme: authSchemeAWSIoTMQTTWS,
		Service: awsIoTMQTTService, Operation: "ClientMQTT", Region: "us-west-2", Method: http.MethodGet,
		URL: "wss://account-prefix-ats.iot.us-west-2.amazonaws.com/mqtt",
		Body: map[string]any{
			"protocol_version": 5, "client_id": "will-client",
			"will": map[string]any{"topic": "status/will-client", "qos": 1, "payload_base64": "b2ZmbGluZQ=="},
		},
	})
	if err == nil {
		t.Fatal("broker Maximum QoS mismatch accepted")
	}
	if len(connection.writes) != 2 || connection.writes[0].data[0]>>4 != 1 || !bytes.Equal(connection.writes[1].data, []byte{0xe0, 0x00}) {
		t.Fatalf("post-CONNACK failure did not attempt graceful Will suppression: %#v", connection.writes)
	}
}

func TestAWSIoTMQTTClientPacesRepeatedRetainedPublishesToOfficialTopicQuota(t *testing.T) {
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			{0x20, 0x02, 0x00, 0x00},
			{0x40, 0x02, 0x00, 0x01},
			{0x40, 0x02, 0x00, 0x02},
		},
		readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageBinary, cloudWebSocketMessageBinary, cloudWebSocketMessageBinary},
	}
	var pauses []time.Duration
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret"}},
		Now:         func() time.Time { return time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC) },
		StreamPause: func(_ context.Context, duration time.Duration) error {
			pauses = append(pauses, duration)
			return nil
		},
		IoTWebSocketDial: func(context.Context, string) (cloudWebSocketConnection, error) {
			return connection, nil
		},
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, Mode: ModeMutate, AuthScheme: authSchemeAWSIoTMQTTWS,
		Service: awsIoTMQTTService, Operation: "ClientMQTT", Region: "us-west-2", Method: http.MethodGet,
		URL: "wss://account-prefix-ats.iot.us-west-2.amazonaws.com/mqtt",
		Body: map[string]any{
			"protocol_version": 4, "client_id": "retained-publisher",
			"publishes": []any{
				map[string]any{"topic": "status/device", "qos": 1, "retain": true, "payload_base64": "b25saW5l"},
				map[string]any{"topic": "status/device", "qos": 1, "retain": true, "payload_base64": ""},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pauses) != 1 || pauses[0] != awsIoTMQTTRetainedTopicDelay {
		t.Fatalf("retained publish pauses=%v", pauses)
	}
	config, err := parseAWSIoTMQTTConfig(map[string]any{
		"client_id": "retained-publisher",
		"publishes": []any{
			map[string]any{"topic": "status/device", "qos": 1, "retain": true, "payload_base64": "b25saW5l"},
			map[string]any{"topic": "status/device", "qos": 1, "retain": true, "payload_base64": ""},
		},
	}, true)
	if err != nil || awsIoTMQTTOperationTimeout(config) != 31*time.Second {
		t.Fatalf("operation timeout=%v err=%v", awsIoTMQTTOperationTimeout(config), err)
	}
}

func testAWSIoTMQTT5PublishPacket(topic string, qos byte, packetID uint16, payload []byte) []byte {
	body := appendMQTTUTF8(nil, topic)
	if qos == 1 {
		body = append(body, byte(packetID>>8), byte(packetID))
	}
	body = append(body, 0x00) // MQTT 5 property length.
	body = append(body, payload...)
	packet, _ := encodeAWSMQTTPacket(byte(0x30|qos<<1), body)
	return packet
}

func awsIoTMQTTTestPointer[T any](value T) *T {
	return &value
}

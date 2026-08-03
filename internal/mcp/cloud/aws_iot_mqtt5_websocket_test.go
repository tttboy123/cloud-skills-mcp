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

func TestAWSIoTMQTT5EncodesOfficialFiniteSubscribeFrames(t *testing.T) {
	config, err := parseAWSIoTMQTTSubscribeConfig(map[string]any{
		"protocol_version": 5,
		"client_id":        "observer-5",
		"subscriptions":    []any{map[string]any{"topic_filter": "sensors/+/temperature", "qos": 1}},
		"max_messages":     8,
		"timeout_seconds":  30,
	})
	if err != nil {
		t.Fatal(err)
	}
	connect, err := encodeAWSIoTMQTTConnect(config)
	if err != nil {
		t.Fatal(err)
	}
	properties := []byte{
		0x21, 0x00, 0x08, // Receive Maximum = max_messages.
		0x27, 0x00, 0x02, 0x00, 0x00, // Maximum Packet Size = 128 KiB.
		0x17, 0x00, // Request Problem Information = false.
	}
	body := []byte{0x00, 0x04, 'M', 'Q', 'T', 'T', 0x05, 0x02, 0x00, 0x3c, byte(len(properties))}
	body = append(body, properties...)
	body = appendMQTTUTF8(body, "observer-5")
	wantConnect, _ := encodeAWSMQTTPacket(0x10, body)
	if !bytes.Equal(connect, wantConnect) {
		t.Fatalf("CONNECT=%x want=%x", connect, wantConnect)
	}
	subscribe, err := encodeAWSIoTMQTTSubscribe(config)
	if err != nil {
		t.Fatal(err)
	}
	// The subscription options byte follows the topic filter.
	wantBody := []byte{0x00, 0x01, 0x00}
	wantBody = appendMQTTUTF8(wantBody, "sensors/+/temperature")
	wantBody = append(wantBody, 0x01)
	wantSubscribe, _ := encodeAWSMQTTPacket(0x82, wantBody)
	if !bytes.Equal(subscribe, wantSubscribe) {
		t.Fatalf("SUBSCRIBE=%x want=%x", subscribe, wantSubscribe)
	}
}

func TestAWSAdapterCollectsFiniteIoTMQTT5Subscription(t *testing.T) {
	directory := t.TempDir()
	responseFile := filepath.Join(directory, "messages.ndjson")
	properties := []byte{
		0x01, 0x01,
		0x02, 0x00, 0x00, 0x00, 0x1e,
		0x03, 0x00, 0x10, 'a', 'p', 'p', 'l', 'i', 'c', 'a', 't', 'i', 'o', 'n', '/', 'j', 's', 'o', 'n',
		0x08, 0x00, 0x10, 'r', 'e', 'p', 'l', 'i', 'e', 's', '/', 'o', 'b', 's', 'e', 'r', 'v', 'e', 'r',
		0x09, 0x00, 0x03, 0x01, 0x02, 0x03,
		0x26, 0x00, 0x04, 's', 'i', 't', 'e', 0x00, 0x03, 's', 'f', 'o',
	}
	publishBody := appendMQTTUTF8(nil, "sensors/room1/temperature")
	publishBody = append(publishBody, 0x00, 0x07)
	publishBody = appendMQTTVariableByteInteger(publishBody, len(properties))
	publishBody = append(publishBody, properties...)
	publishBody = append(publishBody, []byte(`{"value":21}`)...)
	publish, _ := encodeAWSMQTTPacket(0x32, publishBody)
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			{0x20, 0x0b, 0x00, 0x00, 0x08, 0x21, 0x00, 0x08, 0x27, 0x00, 0x02, 0x00, 0x00},
			{0x90, 0x04, 0x00, 0x01, 0x00, 0x01},
			publish,
		},
		readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageBinary, cloudWebSocketMessageBinary, cloudWebSocketMessageBinary},
	}
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret"}},
		Now:         func() time.Time { return time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC) },
		IoTWebSocketDial: func(context.Context, string) (cloudWebSocketConnection, error) {
			return connection, nil
		},
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, Mode: ModeRead, AuthScheme: "iot-mqtt-ws", Service: "iotdevicegateway", Operation: "SubscribeMQTT",
		Region: "us-west-2", Method: http.MethodGet, URL: "wss://account-prefix-ats.iot.us-west-2.amazonaws.com/mqtt",
		Body: map[string]any{
			"protocol_version": 5, "client_id": "observer-5", "max_messages": 1, "timeout_seconds": 30,
			"subscriptions": []any{map[string]any{"topic_filter": "sensors/+/temperature", "qos": 1}},
		},
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RequestID != "observer-5" {
		t.Fatalf("result=%#v", result)
	}
	data, err := os.ReadFile(responseFile)
	if err != nil {
		t.Fatal(err)
	}
	var message map[string]any
	if json.Unmarshal(bytes.TrimSpace(data), &message) != nil {
		t.Fatalf("NDJSON=%q", data)
	}
	if message["payload_format"] != float64(1) || message["content_type"] != "application/json" || message["message_expiry_seconds"] != float64(30) || message["response_topic"] != "replies/observer" || message["correlation_data_base64"] != base64.StdEncoding.EncodeToString([]byte{1, 2, 3}) {
		t.Fatalf("message=%#v", message)
	}
	userProperties, ok := message["user_properties"].([]any)
	if !ok || len(userProperties) != 1 || userProperties[0].(map[string]any)["name"] != "site" || userProperties[0].(map[string]any)["value"] != "sfo" {
		t.Fatalf("user_properties=%#v", message["user_properties"])
	}
	if len(connection.writes) != 4 || connection.writes[0].data[8] != 0x05 || connection.writes[1].data[4] != 0x00 || !bytes.Equal(connection.writes[2].data, []byte{0x40, 0x02, 0x00, 0x07}) || !bytes.Equal(connection.writes[3].data, []byte{0xe0, 0x00}) {
		t.Fatalf("MQTT 5 writes=%#v", connection.writes)
	}
}

func TestAWSIoTMQTT5RejectsUnsupportedOrMalformedFeatures(t *testing.T) {
	base := map[string]any{
		"protocol_version": 5, "client_id": "observer-5", "max_messages": 1, "timeout_seconds": 30,
		"subscriptions": []any{map[string]any{"topic_filter": "sensors/#", "qos": 1}},
	}
	for name, mutate := range map[string]func(map[string]any){
		"unsupported version":      func(value map[string]any) { value["protocol_version"] = 6 },
		"persistent read session":  func(value map[string]any) { value["clean_start"] = false; value["session_expiry_seconds"] = 60 },
		"subscription identifier":  func(value map[string]any) { value["subscription_identifier"] = 1 },
		"oversized session expiry": func(value map[string]any) { value["session_expiry_seconds"] = 604801 },
		"MQTT3 session expiry":     func(value map[string]any) { value["protocol_version"] = 4; value["session_expiry_seconds"] = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := make(map[string]any, len(base))
			for key, value := range base {
				candidate[key] = value
			}
			mutate(candidate)
			if _, err := parseAWSIoTMQTTSubscribeConfig(candidate); err == nil {
				t.Fatal("unsupported MQTT 5 plan accepted")
			}
		})
	}

	config, err := parseAWSIoTMQTTSubscribeConfig(base)
	if err != nil {
		t.Fatal(err)
	}
	badPackets := []awsMQTTPacket{
		{Header: 0x20, Body: []byte{0x00, 0x00, 0x02, 0x29, 0x01}}, // AWS advertises unsupported subscription identifiers.
		{Header: 0x20, Body: []byte{0x00, 0x87, 0x00}},             // Not authorized.
		{Header: 0x20, Body: []byte{0x00, 0x00, 0x02, 0x24, 0x02}}, // Invalid Maximum QoS.
	}
	for _, packet := range badPackets {
		if _, err := parseAWSIoTMQTTConnAck(packet, config); err == nil {
			t.Fatalf("invalid MQTT 5 CONNACK accepted: %#v", packet)
		}
	}
	badSubAck := awsMQTTPacket{Header: 0x90, Body: []byte{0x00, 0x01, 0x00, 0x87}}
	if err := validateAWSIoTMQTTSubAckForConfig(badSubAck, config); err == nil {
		t.Fatal("failed MQTT 5 SUBACK accepted")
	}
	for _, properties := range [][]byte{
		{0x03, 0x23, 0x00, 0x01},       // Topic Alias after advertising a zero maximum.
		{0x02, 0x0b, 0x01},             // AWS-unsupported Subscription Identifier.
		{0x04, 0x01, 0x00, 0x01, 0x00}, // Duplicate Payload Format Indicator.
		{0x80, 0x00},                   // Non-minimal property length.
	} {
		if _, _, err := parseAWSIoTMQTT5PublishProperties(properties); err == nil {
			t.Fatalf("invalid MQTT 5 PUBLISH properties accepted: %x", properties)
		}
	}
}

func TestAWSIoTMQTT5BrokerErrorDisconnectLeavesNoOutput(t *testing.T) {
	directory := t.TempDir()
	responseFile := filepath.Join(directory, "messages.ndjson")
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			{0x20, 0x03, 0x00, 0x00, 0x00},
			{0x90, 0x04, 0x00, 0x01, 0x00, 0x00},
			{0xe0, 0x02, 0x87, 0x00},
		},
		readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageBinary, cloudWebSocketMessageBinary, cloudWebSocketMessageBinary},
	}
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials:      staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret"}},
		Now:              func() time.Time { return time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC) },
		IoTWebSocketDial: func(context.Context, string) (cloudWebSocketConnection, error) { return connection, nil },
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, Mode: ModeRead, AuthScheme: "iot-mqtt-ws", Service: "iotdevicegateway", Operation: "SubscribeMQTT",
		Region: "us-west-2", Method: http.MethodGet, URL: "wss://account-prefix-ats.iot.us-west-2.amazonaws.com/mqtt",
		Body: map[string]any{
			"protocol_version": 5, "client_id": "observer-5", "max_messages": 1, "timeout_seconds": 30,
			"subscriptions": []any{map[string]any{"topic_filter": "sensors/#", "qos": 0}},
		},
		ResponseFile: responseFile,
	})
	if err == nil || !strings.Contains(err.Error(), "disconnected") {
		t.Fatalf("expected broker disconnect error, got %v", err)
	}
	if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
		t.Fatalf("failed MQTT 5 session published output: %v", statErr)
	}
}

func FuzzAWSIoTMQTT5PacketAndPropertiesNeverPanic(f *testing.F) {
	f.Add([]byte{0x20, 0x03, 0x00, 0x00, 0x00})
	f.Add([]byte{0x03, 0x23, 0x00, 0x01})
	f.Add([]byte{0x2c, 0x01, 0x01, 0x03, 0x00, 0x10, 'a'})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _, _, _ = decodeAWSMQTTPacket(data)
		_, _, _ = parseAWSIoTMQTT5PublishProperties(data)
	})
}

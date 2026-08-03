package cloud

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBaiduIoTCoreMQTT5PlanCoversOfficialFeatures(t *testing.T) {
	plan, err := parseBaiduIoTCoreMQTTPlan(map[string]any{
		"protocol_version": 5,
		"client_id":        "observer-5",
		"subscriptions": []any{
			map[string]any{"topic_filter": "$share/group1/sensors/+/temperature", "qos": 2},
		},
		"unsubscriptions": []any{"$share/old-group/sensors/+/temperature"},
		"publishes": []any{
			map[string]any{
				"topic": "commands/device-1", "qos": 2,
				"payload_base64": base64.StdEncoding.EncodeToString([]byte("turn-on")),
				"payload_format": 1, "content_type": "text/plain", "message_expiry_seconds": 30,
				"response_topic": "responses/device-1", "correlation_data_base64": "AQI=",
				"user_properties": []any{map[string]any{"name": "trace", "value": "one"}},
			},
		},
		"will": map[string]any{
			"topic": "status/device-1", "qos": 2, "payload_base64": "b2ZmbGluZQ==",
			"will_delay_seconds": 5,
		},
		"max_messages": 1, "timeout_seconds": 30,
	}, "aop098js", true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.ProtocolVersion != 5 || len(plan.Unsubscriptions) != 1 || plan.Publishes[0].QoS != 2 || len(plan.Publishes[0].userProperties) != 1 || plan.Will.WillDelaySeconds != 5 {
		t.Fatalf("plan=%#v", plan)
	}
	connect, err := encodeBaiduIoTCoreMQTTConnect(plan, "user", "password")
	if err != nil || !bytes.Contains(connect, []byte{0x00, 0x04, 'M', 'Q', 'T', 'T', 0x05}) || !bytes.Contains(connect, []byte{0x21, 0x00, 0x01}) {
		t.Fatalf("connect=%x err=%v", connect, err)
	}
	publish, err := encodeBaiduIoTCoreMQTTPublishForPlan(plan, plan.Publishes[0], 7)
	if err != nil || publish[0]&0x06 != 0x04 || !bytes.Contains(publish, []byte{0x08, 0x00, 0x12, 'r', 'e', 's', 'p', 'o', 'n', 's', 'e', 's', '/', 'd', 'e', 'v', 'i', 'c', 'e', '-', '1'}) {
		t.Fatalf("publish=%x err=%v", publish, err)
	}
}

func TestBaiduIoTCoreMQTT5RejectsVersionSpecificAndSharedSubscriptionErrors(t *testing.T) {
	base := map[string]any{
		"protocol_version": 5, "client_id": "observer-5",
		"subscriptions": []any{map[string]any{"topic_filter": "$share/group1/sensors/#", "qos": 1}},
		"max_messages":  1, "timeout_seconds": 30,
	}
	tests := map[string]func(map[string]any){
		"version": func(v map[string]any) { v["protocol_version"] = 6 },
		"shared group": func(v map[string]any) {
			v["subscriptions"] = []any{map[string]any{"topic_filter": "$share/gr+oup/sensors/#", "qos": 1}}
		},
		"mqtt3 props": func(v map[string]any) {
			v["protocol_version"] = 4
			v["publishes"] = []any{map[string]any{"topic": "commands/one", "qos": 1, "payload_base64": "eA==", "content_type": "text/plain"}}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			copy := cloneJSONMap(t, base)
			mutate(copy)
			if _, err := parseBaiduIoTCoreMQTTPlan(copy, "aop098js", true); err == nil {
				t.Fatal("invalid MQTT plan accepted")
			}
		})
	}
}

func TestBaiduIoTCoreMQTT5ValidatesBrokerCapabilitiesAndProperties(t *testing.T) {
	plan := baiduIoTCoreMQTTPlan{
		ProtocolVersion: 5, cleanSession: true,
		Publishes: []baiduIoTCoreMQTTPublish{{Topic: "commands/a", QoS: 2, payload: []byte("x")}},
	}
	if err := validateBaiduIoTCoreMQTTConnAck(awsMQTTPacket{Header: 0x20, Body: []byte{0x00, 0x00, 0x02, 0x24, 0x01}}, plan); err == nil {
		t.Fatal("broker Maximum QoS below the publish plan was accepted")
	}
	if err := validateBaiduIoTCoreMQTTConnAck(awsMQTTPacket{Header: 0x20, Body: []byte{0x00, 0x00, 0x03, 0x21, 0x00, 0x01}}, plan); err != nil {
		t.Fatalf("valid Receive Maximum rejected: %v", err)
	}
	twoPublishes := plan
	twoPublishes.Publishes = append(twoPublishes.Publishes, plan.Publishes[0])
	if err := validateBaiduIoTCoreMQTTConnAck(awsMQTTPacket{Header: 0x20, Body: []byte{0x00, 0x00, 0x03, 0x21, 0x00, 0x01}}, twoPublishes); err == nil {
		t.Fatal("publish plan above broker Receive Maximum accepted")
	}
	if err := validateBaiduIoTCoreMQTTConnAck(awsMQTTPacket{Header: 0x20, Body: []byte{0x00, 0x00, 0x05, 0x27, 0x00, 0x00, 0x00, 0x04}}, plan); err == nil {
		t.Fatal("publish above broker Maximum Packet Size accepted")
	}
	if err := validateBaiduIoTCoreMQTTConnAck(awsMQTTPacket{Header: 0x20, Body: []byte{0x01, 0x00, 0x00}}, plan); err == nil {
		t.Fatal("resumed clean session accepted")
	}
	v4Plan := baiduIoTCoreMQTTPlan{ProtocolVersion: 4, cleanSession: true}
	if err := validateBaiduIoTCoreMQTTConnAck(awsMQTTPacket{Header: 0x20, Body: []byte{0x00, 0x00}}, v4Plan); err != nil {
		t.Fatalf("valid MQTT 3.1.1 CONNACK rejected: %v", err)
	}
	encoded := encodeBaiduIoTCoreMQTT5ApplicationProperties(1, "text/plain", 30, "responses/a", []byte{1, 2}, []baiduIoTCoreMQTTUserProperty{{Name: "trace", Value: "one"}})
	propertyBlock := appendMQTTVariableByteInteger(nil, len(encoded))
	propertyBlock = append(propertyBlock, encoded...)
	consumed, metadata, err := parseBaiduIoTCoreMQTT5PublishProperties(propertyBlock)
	if err != nil || consumed != len(propertyBlock) || metadata["payload_format"] != 1 || metadata["content_type"] != "text/plain" || metadata["message_expiry_seconds"] != uint32(30) || metadata["response_topic"] != "responses/a" || metadata["correlation_data_base64"] != "AQI=" {
		t.Fatalf("consumed=%d metadata=%#v err=%v", consumed, metadata, err)
	}
	if err := validateBaiduIoTCoreMQTTDisconnect(awsMQTTPacket{Header: 0xe0, Body: []byte{0x00, 0x00}}, 5); err != nil {
		t.Fatalf("valid MQTT 5 DISCONNECT rejected: %v", err)
	}
	if err := validateBaiduIoTCoreMQTTDisconnect(awsMQTTPacket{Header: 0xe0, Body: []byte{0x80}}, 5); err == nil {
		t.Fatal("failed MQTT 5 DISCONNECT accepted")
	}
	if err := validateBaiduIoTCoreMQTTUnsubAck(awsMQTTPacket{Header: 0xb0, Body: []byte{0x00, 0x01}}, 1, []string{"sensors/#"}, 4); err != nil {
		t.Fatalf("valid MQTT 3.1.1 UNSUBACK rejected: %v", err)
	}
	if err := validateBaiduIoTCoreMQTTUnsubAck(awsMQTTPacket{Header: 0xb0, Body: []byte{0x00, 0x01, 0x00, 0x80}}, 1, []string{"sensors/#"}, 5); err == nil {
		t.Fatal("failed MQTT 5 UNSUBACK accepted")
	}
	for name, properties := range map[string][]byte{
		"topic alias":       {0x02, 0x23, 0x01},
		"duplicate content": {0x04, 0x01, 0x01, 0x01, 0x01},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := parseBaiduIoTCoreMQTT5PublishProperties(properties); err == nil {
				t.Fatal("unsupported or duplicate MQTT 5 property accepted")
			}
		})
	}
}

func TestBaiduAdapterPublishesIoTCoreMQTT5QoS2(t *testing.T) {
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			{0x20, 0x03, 0x00, 0x00, 0x00},
			{0x50, 0x02, 0x00, 0x02},
			{0x70, 0x02, 0x00, 0x02},
		},
		readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageBinary, cloudWebSocketMessageBinary, cloudWebSocketMessageBinary},
	}
	adapter := NewBaiduRESTAdapter(BaiduRESTConfig{
		Credentials: staticBCECredentials{credentials: BCECredentials{AccessKeyID: "iam-ak", SecretAccessKey: "iam-secret"}},
		Now:         func() time.Time { return time.Date(2026, 8, 3, 18, 0, 0, 0, time.UTC) },
		IoTWebSocketDial: func(context.Context, string) (cloudWebSocketConnection, error) {
			return connection, nil
		},
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderBaidu, Mode: ModeMutate, AuthScheme: authSchemeBaiduIoTCoreMQTTWS,
		Service: "iotcore", Operation: "ClientMQTT", Method: http.MethodGet,
		URL: "wss://aop098js.iot.gz.baidubce.com/mqtt",
		Body: map[string]any{
			"protocol_version": 5, "client_id": "publisher-qos2",
			"publishes": []any{map[string]any{
				"topic": "commands/device-1", "qos": 2, "payload_base64": "b24=",
				"payload_format": 1, "content_type": "text/plain", "message_expiry_seconds": 30,
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(connection.writes) != 4 || connection.writes[0].data[9] != 5 || connection.writes[1].data[0]&0x06 != 0x04 || !bytes.Equal(connection.writes[2].data, []byte{0x62, 0x02, 0x00, 0x02}) || !bytes.Equal(connection.writes[3].data, []byte{0xe0, 0x00}) {
		t.Fatalf("writes=%#v", connection.writes)
	}
	var summary map[string]any
	if json.Unmarshal(result.Output, &summary) != nil || summary["published"] != float64(1) {
		t.Fatalf("output=%q", result.Output)
	}
}

func TestBaiduAdapterUnsubscribesIoTCoreMQTT5PersistentState(t *testing.T) {
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			{0x20, 0x03, 0x00, 0x00, 0x00},
			{0xb0, 0x04, 0x00, 0x01, 0x00, 0x00},
		},
		readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageBinary, cloudWebSocketMessageBinary},
	}
	adapter := NewBaiduRESTAdapter(BaiduRESTConfig{
		Credentials: staticBCECredentials{credentials: BCECredentials{AccessKeyID: "iam-ak", SecretAccessKey: "iam-secret"}},
		IoTWebSocketDial: func(context.Context, string) (cloudWebSocketConnection, error) {
			return connection, nil
		},
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderBaidu, Mode: ModeMutate, AuthScheme: authSchemeBaiduIoTCoreMQTTWS,
		Service: "iotcore", Operation: "ClientMQTT", Method: http.MethodGet,
		URL: "wss://aop098js.iot.gz.baidubce.com/mqtt",
		Body: map[string]any{
			"protocol_version": 5, "client_id": "unsubscribe-client", "clean_session": false,
			"unsubscriptions": []any{"$share/old-group/sensors/+/temperature"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(connection.writes) != 3 || connection.writes[1].data[0] != 0xa2 || !bytes.Equal(connection.writes[2].data, []byte{0xe0, 0x00}) {
		t.Fatalf("writes=%#v", connection.writes)
	}
	var summary map[string]any
	if json.Unmarshal(result.Output, &summary) != nil || summary["unsubscribed"] != float64(1) {
		t.Fatalf("output=%q", result.Output)
	}
}

func TestBaiduAdapterReceivesIoTCoreMQTT5QoS2ExactlyOnce(t *testing.T) {
	responseFile := filepath.Join(t.TempDir(), "messages.ndjson")
	properties := []byte{0x01, 0x01, 0x03}
	properties = appendMQTTUTF8(properties, "text/plain")
	body := appendMQTTUTF8(nil, "sensors/device-1/status")
	body = append(body, 0x00, 0x07)
	body = appendMQTTVariableByteInteger(body, len(properties))
	body = append(body, properties...)
	body = append(body, []byte("online")...)
	publish, err := encodeAWSMQTTPacket(0x34, body)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := encodeAWSMQTTPacket(0x3c, body)
	if err != nil {
		t.Fatal(err)
	}
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			{0x20, 0x03, 0x00, 0x00, 0x00},
			{0x90, 0x04, 0x00, 0x01, 0x00, 0x02},
			publish,
			duplicate,
			{0x62, 0x02, 0x00, 0x07},
		},
		readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageBinary, cloudWebSocketMessageBinary, cloudWebSocketMessageBinary, cloudWebSocketMessageBinary, cloudWebSocketMessageBinary},
	}
	adapter := NewBaiduRESTAdapter(BaiduRESTConfig{
		Credentials: staticBCECredentials{credentials: BCECredentials{AccessKeyID: "iam-ak", SecretAccessKey: "iam-secret"}},
		IoTWebSocketDial: func(context.Context, string) (cloudWebSocketConnection, error) {
			return connection, nil
		},
	})
	_, err = adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderBaidu, Mode: ModeRead, AuthScheme: authSchemeBaiduIoTCoreMQTTWS,
		Service: "iotcore", Operation: "SubscribeMQTT", Method: http.MethodGet,
		URL: "wss://aop098js.iot.gz.baidubce.com/mqtt",
		Body: map[string]any{
			"protocol_version": 5, "client_id": "observer-qos2",
			"subscriptions": []any{map[string]any{"topic_filter": "$share/processors/sensors/+/status", "qos": 2}},
			"max_messages":  1, "timeout_seconds": 30,
		},
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(responseFile)
	if err != nil {
		t.Fatal(err)
	}
	var message map[string]any
	if json.Unmarshal(bytes.TrimSpace(data), &message) != nil || message["topic"] != "sensors/device-1/status" || message["qos"] != float64(2) || message["content_type"] != "text/plain" || message["payload_base64"] != base64.StdEncoding.EncodeToString([]byte("online")) {
		t.Fatalf("message=%q", data)
	}
	if len(connection.writes) != 6 || !bytes.Equal(connection.writes[2].data, []byte{0x50, 0x02, 0x00, 0x07}) || !bytes.Equal(connection.writes[3].data, []byte{0x50, 0x02, 0x00, 0x07}) || !bytes.Equal(connection.writes[4].data, []byte{0x70, 0x02, 0x00, 0x07}) {
		t.Fatalf("writes=%#v", connection.writes)
	}
}

func FuzzBaiduIoTCoreMQTT5PacketAndProperties(f *testing.F) {
	f.Add([]byte{0x34, 0x07, 0x00, 0x01, 't', 0x00, 0x01, 0x00, 'x'})
	f.Add([]byte{0x30, 0x04, 0x00, 0x01, 't', 0x00})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 4096 {
			return
		}
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("MQTT 5 parser panicked: %v", recovered)
			}
		}()
		packet, _, complete, _ := decodeBaiduIoTCoreMQTTPacket(data)
		if complete {
			_ = validateBaiduIoTCoreMQTTInboundPayload(packet, baiduIoTCoreMQTTDefaultPayloadMax, 5)
		}
		_, _, _ = parseBaiduIoTCoreMQTT5PublishProperties(data)
	})
}

package cloud

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBaiduIoTCoreIAMMQTTSignatureMatchesOfficialVector(t *testing.T) {
	username, password, err := deriveBaiduIoTCoreIAMMQTTCredential(
		"aop098js",
		BCECredentials{AccessKeyID: "7761E24FC8b9bee8703a5efb266d9c0", SecretAccessKey: "ABCxxxx1234567"},
		time.UnixMilli(1600834787219).UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if username != "bceiam@aop098js|7761E24FC8b9bee8703a5efb266d9c0|1600834787219|SHA256" {
		t.Fatalf("username=%q", username)
	}
	if password != "1b937b1268d8943860038f2a4bec637e5370ded2e848289bee1594e30c600d39" {
		t.Fatalf("password=%q", password)
	}
}

func TestBaiduIoTCoreIAMMQTTRejectsUndocumentedSessionToken(t *testing.T) {
	_, _, err := deriveBaiduIoTCoreIAMMQTTCredential(
		"aop098js",
		BCECredentials{AccessKeyID: "iam-ak", SecretAccessKey: "iam-secret", SessionToken: "sts-token"},
		time.UnixMilli(1600834787219).UTC(),
	)
	if err == nil || !strings.Contains(err.Error(), "does not document") || strings.Contains(err.Error(), "sts-token") {
		t.Fatalf("err=%v", err)
	}
}

func TestBaiduIoTCoreMQTTTargetAndPlanFailClosed(t *testing.T) {
	valid := Invocation{
		Provider: ProviderBaidu, Mode: ModeRead, AuthScheme: authSchemeBaiduIoTCoreMQTTWS,
		Service: "iotcore", Operation: "SubscribeMQTT", Method: http.MethodGet,
		URL: "wss://aop098js.iot.gz.baidubce.com/mqtt",
		Body: map[string]any{
			"client_id": "observer-1", "max_messages": 1, "timeout_seconds": 30,
			"subscriptions": []any{map[string]any{"topic_filter": "sensors/+/temperature", "qos": 1}},
		},
		ResponseFile: "/approved/messages.ndjson",
	}
	if err := validateBaiduIoTCoreMQTTInvocation(valid); err != nil {
		t.Fatalf("valid plan rejected: %v", err)
	}
	hyphenated := valid
	hyphenated.URL = "wss://iot-core-1.iot.gz.baidubce.com/mqtt"
	if err := validateBaiduIoTCoreMQTTInvocation(hyphenated); err != nil {
		t.Fatalf("valid DNS-label instance ID rejected: %v", err)
	}
	if !classifyRead(ProviderBaidu, valid) {
		t.Fatal("SubscribeMQTT was not classified as read-only")
	}
	mutating := valid
	mutating.Mode = ModeMutate
	mutating.Operation = "ClientMQTT"
	if classifyRead(ProviderBaidu, mutating) {
		t.Fatal("ClientMQTT was classified as read-only")
	}
	tests := map[string]func(*Invocation){
		"lookalike": func(value *Invocation) { value.URL = "wss://aop098js.iot.gz.baidubce.com.attacker.example/mqtt" },
		"nested":    func(value *Invocation) { value.URL = "wss://nested.aop098js.iot.gz.baidubce.com/mqtt" },
		"wrong path": func(value *Invocation) {
			value.URL = "wss://aop098js.iot.gz.baidubce.com/"
		},
		"host id mismatch": func(value *Invocation) {
			value.Body.(map[string]any)["iot_core_id"] = "other123"
		},
		"credential field": func(value *Invocation) { value.Body.(map[string]any)["password"] = "caller-secret" },
		"publish on read": func(value *Invocation) {
			value.Body.(map[string]any)["publishes"] = []any{map[string]any{"topic": "sensors/a", "payload_base64": "eA=="}}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			invocation := valid
			body, _ := json.Marshal(valid.Body)
			_ = json.Unmarshal(body, &invocation.Body)
			mutate(&invocation)
			if err := validateBaiduIoTCoreMQTTInvocation(invocation); err == nil {
				t.Fatal("unsafe invocation accepted")
			}
		})
	}
}

func TestBaiduIoTCoreMQTTProtocolBoundsFailClosed(t *testing.T) {
	base := Invocation{
		Provider: ProviderBaidu, Mode: ModeRead, AuthScheme: authSchemeBaiduIoTCoreMQTTWS,
		Service: "iotcore", Operation: "SubscribeMQTT", Method: http.MethodGet,
		URL: "wss://aop098js.iot.gz.baidubce.com/mqtt",
		Body: map[string]any{
			"client_id": "observer-1", "max_messages": 1, "timeout_seconds": 30,
			"subscriptions": []any{map[string]any{"topic_filter": "sensors/#", "qos": 1}},
		},
		ResponseFile: "/approved/messages.ndjson",
	}
	clone := func() Invocation {
		value := base
		encoded, _ := json.Marshal(base.Body)
		_ = json.Unmarshal(encoded, &value.Body)
		return value
	}
	tests := map[string]func() error{
		"service": func() error {
			value := clone()
			value.Service = "bcc"
			return validateBaiduIoTCoreMQTTInvocation(value)
		},
		"operation": func() error {
			value := clone()
			value.Operation = "Listen"
			return validateBaiduIoTCoreMQTTInvocation(value)
		},
		"mode": func() error {
			value := clone()
			value.Mode = ModeMutate
			return validateBaiduIoTCoreMQTTInvocation(value)
		},
		"method": func() error {
			value := clone()
			value.Method = http.MethodPost
			return validateBaiduIoTCoreMQTTInvocation(value)
		},
		"query": func() error {
			value := clone()
			value.URL += "?password=x"
			return validateBaiduIoTCoreMQTTInvocation(value)
		},
		"parameters": func() error {
			value := clone()
			value.Parameters = map[string]any{"x": 1}
			return validateBaiduIoTCoreMQTTInvocation(value)
		},
		"rest controls": func() error { value := clone(); value.Region = "bj"; return validateBaiduIoTCoreMQTTInvocation(value) },
		"nil body":      func() error { value := clone(); value.Body = nil; return validateBaiduIoTCoreMQTTInvocation(value) },
		"unknown body field": func() error {
			value := clone()
			value.Body.(map[string]any)["raw_frame"] = "x"
			return validateBaiduIoTCoreMQTTInvocation(value)
		},
		"client id": func() error {
			value := clone()
			value.Body.(map[string]any)["client_id"] = ""
			return validateBaiduIoTCoreMQTTInvocation(value)
		},
		"persistent read": func() error {
			value := clone()
			value.Body.(map[string]any)["clean_session"] = false
			return validateBaiduIoTCoreMQTTInvocation(value)
		},
		"keepalive": func() error {
			value := clone()
			value.Body.(map[string]any)["keep_alive_seconds"] = 29
			return validateBaiduIoTCoreMQTTInvocation(value)
		},
		"subscription qos2": func() error {
			value := clone()
			value.Body.(map[string]any)["subscriptions"].([]any)[0].(map[string]any)["qos"] = 2
			return validateBaiduIoTCoreMQTTInvocation(value)
		},
		"no subscription": func() error {
			value := clone()
			value.Body.(map[string]any)["subscriptions"] = []any{}
			return validateBaiduIoTCoreMQTTInvocation(value)
		},
		"message bound": func() error {
			value := clone()
			value.Body.(map[string]any)["max_messages"] = 257
			return validateBaiduIoTCoreMQTTInvocation(value)
		},
		"timeout bound": func() error {
			value := clone()
			value.Body.(map[string]any)["timeout_seconds"] = 301
			return validateBaiduIoTCoreMQTTInvocation(value)
		},
		"missing output": func() error {
			value := clone()
			value.ResponseFile = ""
			return validateBaiduIoTCoreMQTTInvocation(value)
		},
		"unexpected output": func() error {
			value := clone()
			value.Mode = ModeMutate
			value.Operation = "ClientMQTT"
			value.Body = map[string]any{"client_id": "publisher", "publishes": []any{map[string]any{"topic": "commands/a", "payload_base64": "eA==", "qos": 0}}}
			return validateBaiduIoTCoreMQTTInvocation(value)
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if err := test(); err == nil {
				t.Fatal("unsafe protocol plan accepted")
			}
		})
	}

	for name, test := range map[string]func() error{
		"too many levels":  func() error { return validateBaiduIoTCoreMQTTTopic(strings.Repeat("a/", 16)+"a", true) },
		"long level":       func() error { return validateBaiduIoTCoreMQTTTopic(strings.Repeat("a", 41), true) },
		"publish wildcard": func() error { return validateBaiduIoTCoreMQTTTopic("commands/+", false) },
		"bad hash":         func() error { return validateBaiduIoTCoreMQTTTopic("sensors/#/x", true) },
		"bad plus":         func() error { return validateBaiduIoTCoreMQTTTopic("sensors/a+", true) },
		"qos2 publish": func() error {
			_, err := validateBaiduIoTCoreMQTTPublish("commands/a", "eA==", 2, baiduIoTCoreMQTTDefaultPayloadMax)
			return err
		},
		"bad base64": func() error {
			_, err := validateBaiduIoTCoreMQTTPublish("commands/a", "***", 0, baiduIoTCoreMQTTDefaultPayloadMax)
			return err
		},
		"large payload": func() error {
			_, err := validateBaiduIoTCoreMQTTPublish("commands/a", base64.StdEncoding.EncodeToString(make([]byte, baiduIoTCoreMQTTDefaultPayloadMax+1)), 0, baiduIoTCoreMQTTDefaultPayloadMax)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := test(); err == nil {
				t.Fatal("unsafe topic or publish accepted")
			}
		})
	}
}

func TestBaiduIoTCoreMQTTEncodesWillAndRetainedPublish(t *testing.T) {
	plan := baiduIoTCoreMQTTPlan{
		ClientID: "client-1", KeepAliveSeconds: 60,
		Will: &baiduIoTCoreMQTTWill{Topic: "status/client-1", QoS: 1, Retain: true, payload: []byte("offline")},
	}
	packet, err := encodeBaiduIoTCoreMQTTConnect(plan, "user", "password")
	if err != nil || len(packet) < 10 || packet[9] != 0xec {
		t.Fatalf("packet=%x err=%v", packet, err)
	}
	publish, err := encodeBaiduIoTCoreMQTTPublish(baiduIoTCoreMQTTPublish{Topic: "status/client-1", Retain: true, payload: []byte("online")}, 0)
	if err != nil || publish[0] != 0x31 {
		t.Fatalf("publish=%x err=%v", publish, err)
	}
}

func TestBaiduIoTCoreMQTTSupportsApprovedRaisedMessageLimit(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString(make([]byte, baiduIoTCoreMQTTDefaultPayloadMax+1))
	invocation := Invocation{
		Provider: ProviderBaidu, Mode: ModeMutate, AuthScheme: authSchemeBaiduIoTCoreMQTTWS,
		Service: "iotcore", Operation: "ClientMQTT", Method: http.MethodGet,
		URL: "wss://aop098js.iot.gz.baidubce.com/mqtt",
		Body: map[string]any{
			"client_id": "publisher-raised-limit", "max_payload_bytes": baiduIoTCoreMQTTMaxPayloadBytes,
			"publishes": []any{map[string]any{"topic": "commands/a", "payload_base64": payload, "qos": 0}},
		},
	}
	if err := validateBaiduIoTCoreMQTTInvocation(invocation); err != nil {
		t.Fatalf("approved raised message limit rejected: %v", err)
	}
	invocation.Body.(map[string]any)["max_payload_bytes"] = baiduIoTCoreMQTTMaxPayloadBytes + 1
	if err := validateBaiduIoTCoreMQTTInvocation(invocation); err == nil {
		t.Fatal("message limit above provider maximum accepted")
	}
}

func TestBaiduIoTCoreMQTTEnforcesInboundMessageLimit(t *testing.T) {
	packet := awsMQTTPacket{Header: 0x30, Body: append([]byte{0x00, 0x01, 't'}, make([]byte, baiduIoTCoreMQTTDefaultPayloadMax+1)...)}
	if err := validateBaiduIoTCoreMQTTInboundPayload(packet, baiduIoTCoreMQTTDefaultPayloadMax); err == nil {
		t.Fatal("inbound payload above the declared instance limit accepted")
	}
	if err := validateBaiduIoTCoreMQTTInboundPayload(packet, baiduIoTCoreMQTTMaxPayloadBytes); err != nil {
		t.Fatalf("inbound payload within the approved raised limit rejected: %v", err)
	}
}

func TestBaiduIoTCoreMQTTRejectsMalformedBrokerPackets(t *testing.T) {
	for name, packet := range map[string]awsMQTTPacket{
		"not publish": {},
		"qos2":        {Header: 0x34, Body: []byte{0x00, 0x01, 't'}},
		"missing id":  {Header: 0x32, Body: []byte{0x00, 0x01, 't'}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateBaiduIoTCoreMQTTInboundPayload(packet, baiduIoTCoreMQTTDefaultPayloadMax); err == nil {
				t.Fatal("malformed broker PUBLISH accepted")
			}
		})
	}
	subscriptions := []baiduIoTCoreMQTTSubscription{{TopicFilter: "sensors/#", QoS: 0}}
	if err := validateBaiduIoTCoreMQTTSubAck(awsMQTTPacket{Header: 0x90, Body: []byte{0x00, 0x01, 0x01}}, 1, subscriptions); err == nil {
		t.Fatal("elevated SUBACK accepted")
	}
	if err := validateBaiduIoTCoreMQTTSubAck(awsMQTTPacket{Header: 0x91, Body: []byte{0x00, 0x01, 0x00}}, 1, subscriptions); err == nil {
		t.Fatal("malformed SUBACK header accepted")
	}
}

func TestBaiduAdapterCollectsFiniteIoTCoreMQTTSubscription(t *testing.T) {
	responseFile := filepath.Join(t.TempDir(), "messages.ndjson")
	publish := testMQTTPublishPacket("sensors/room1/temperature", 1, 7, []byte{0x00, 0x01, 0x02})
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			append([]byte{0x20, 0x02, 0x00, 0x00}, []byte{0x90, 0x03, 0x00, 0x01, 0x01}...),
			publish,
		},
		readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageBinary, cloudWebSocketMessageBinary},
	}
	var dialTarget string
	adapter := NewBaiduRESTAdapter(BaiduRESTConfig{
		Credentials: staticBCECredentials{credentials: BCECredentials{AccessKeyID: "iam-ak", SecretAccessKey: "iam-secret"}},
		Now:         func() time.Time { return time.Date(2026, 8, 3, 18, 0, 0, 0, time.UTC) },
		IoTWebSocketDial: func(_ context.Context, target string) (cloudWebSocketConnection, error) {
			dialTarget = target
			return connection, nil
		},
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderBaidu, Mode: ModeRead, AuthScheme: authSchemeBaiduIoTCoreMQTTWS,
		Service: "iotcore", Operation: "SubscribeMQTT", Method: http.MethodGet,
		URL: "wss://aop098js.iot.gz.baidubce.com/mqtt",
		Body: map[string]any{
			"client_id": "observer-1", "max_messages": 1, "timeout_seconds": 30,
			"subscriptions": []any{map[string]any{"topic_filter": "sensors/+/temperature", "qos": 1}},
		},
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if dialTarget != "wss://aop098js.iot.gz.baidubce.com/mqtt" || result.RequestID != "observer-1" {
		t.Fatalf("target=%q result=%#v", dialTarget, result)
	}
	data, err := os.ReadFile(responseFile)
	if err != nil {
		t.Fatal(err)
	}
	var message map[string]any
	if json.Unmarshal(bytes.TrimSpace(data), &message) != nil || message["topic"] != "sensors/room1/temperature" || message["payload_base64"] != base64.StdEncoding.EncodeToString([]byte{0x00, 0x01, 0x02}) {
		t.Fatalf("message=%q", data)
	}
	if len(connection.writes) != 4 || connection.writes[0].data[0] != 0x10 || connection.writes[1].data[0] != 0x82 || !bytes.Equal(connection.writes[2].data, []byte{0x40, 0x02, 0x00, 0x07}) || !bytes.Equal(connection.writes[3].data, []byte{0xe0, 0x00}) {
		t.Fatalf("writes=%#v", connection.writes)
	}
	for _, secret := range []string{"iam-ak", "iam-secret", "bceiam@", "SHA256"} {
		if strings.Contains(string(result.Output), secret) || strings.Contains(string(data), secret) {
			t.Fatalf("output leaked %q", secret)
		}
	}
}

func TestBaiduAdapterBatchesIoTCoreSubscriptionRequestLimit(t *testing.T) {
	responseFile := filepath.Join(t.TempDir(), "messages.ndjson")
	firstSubAck := append([]byte{0x90, 0x0a, 0x00, 0x01}, make([]byte, 8)...)
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			{0x20, 0x02, 0x00, 0x00}, firstSubAck, {0x90, 0x03, 0x00, 0x02, 0x00},
			testMQTTPublishPacket("sensors/0", 0, 0, []byte("ok")),
		},
		readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageBinary, cloudWebSocketMessageBinary, cloudWebSocketMessageBinary, cloudWebSocketMessageBinary},
	}
	adapter := NewBaiduRESTAdapter(BaiduRESTConfig{
		Credentials: staticBCECredentials{credentials: BCECredentials{AccessKeyID: "iam-ak", SecretAccessKey: "iam-secret"}},
		IoTWebSocketDial: func(context.Context, string) (cloudWebSocketConnection, error) {
			return connection, nil
		},
	})
	subscriptions := make([]any, 9)
	for index := range subscriptions {
		subscriptions[index] = map[string]any{"topic_filter": "sensors/" + string(rune('0'+index)), "qos": 0}
	}
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderBaidu, Mode: ModeRead, AuthScheme: authSchemeBaiduIoTCoreMQTTWS,
		Service: "iotcore", Operation: "SubscribeMQTT", Method: http.MethodGet,
		URL: "wss://aop098js.iot.gz.baidubce.com/mqtt",
		Body: map[string]any{
			"client_id": "observer-batch", "subscriptions": subscriptions,
			"max_messages": 1, "timeout_seconds": 5,
		},
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(connection.writes) != 4 || connection.writes[1].data[0] != 0x82 || connection.writes[2].data[0] != 0x82 || binary.BigEndian.Uint16(connection.writes[1].data[2:4]) != 1 || binary.BigEndian.Uint16(connection.writes[2].data[2:4]) != 2 {
		t.Fatalf("writes=%#v", connection.writes)
	}
}

func TestBaiduAdapterPublishesIoTCoreMQTTWithMutationGatePlan(t *testing.T) {
	connection := &fakeTencentWebSocketConnection{
		reads:     [][]byte{{0x20, 0x02, 0x00, 0x00}, {0x40, 0x02, 0x00, 0x02}},
		readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageBinary, cloudWebSocketMessageBinary},
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
			"client_id": "publisher-1",
			"publishes": []any{map[string]any{"topic": "sensors/room1/command", "payload_base64": "b24=", "qos": 1}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(connection.writes) != 3 || connection.writes[0].data[0] != 0x10 || connection.writes[1].data[0]>>4 != 3 || !bytes.Equal(connection.writes[2].data, []byte{0xe0, 0x00}) {
		t.Fatalf("writes=%#v", connection.writes)
	}
	var summary map[string]any
	if json.Unmarshal(result.Output, &summary) != nil || summary["published"] != float64(1) || summary["received"] != float64(0) {
		t.Fatalf("output=%q", result.Output)
	}
	parsed, _ := url.Parse("wss://aop098js.iot.gz.baidubce.com/mqtt")
	if parsed.RawQuery != "" {
		t.Fatal("credentials unexpectedly moved into URL")
	}
}

func TestBaiduAdapterPacesIoTCoreMQTTPublishRate(t *testing.T) {
	connection := &fakeTencentWebSocketConnection{
		reads:     [][]byte{{0x20, 0x02, 0x00, 0x00}, {0x40, 0x02, 0x00, 0x02}, {0x40, 0x02, 0x00, 0x03}},
		readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageBinary, cloudWebSocketMessageBinary, cloudWebSocketMessageBinary},
	}
	var pauses []time.Duration
	adapter := NewBaiduRESTAdapter(BaiduRESTConfig{
		Credentials: staticBCECredentials{credentials: BCECredentials{AccessKeyID: "iam-ak", SecretAccessKey: "iam-secret"}},
		IoTWebSocketDial: func(context.Context, string) (cloudWebSocketConnection, error) {
			return connection, nil
		},
		StreamPause: func(_ context.Context, duration time.Duration) error {
			pauses = append(pauses, duration)
			return nil
		},
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderBaidu, Mode: ModeMutate, AuthScheme: authSchemeBaiduIoTCoreMQTTWS,
		Service: "iotcore", Operation: "ClientMQTT", Method: http.MethodGet,
		URL: "wss://aop098js.iot.gz.baidubce.com/mqtt",
		Body: map[string]any{
			"client_id": "publisher-paced",
			"publishes": []any{
				map[string]any{"topic": "commands/a", "payload_base64": "MQ==", "qos": 1},
				map[string]any{"topic": "commands/a", "payload_base64": "Mg==", "qos": 1},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pauses) != 1 || pauses[0] != 100*time.Millisecond {
		t.Fatalf("pauses=%v", pauses)
	}
}

func TestBaiduAdapterIoTCoreMQTTFailuresStayAtomic(t *testing.T) {
	tests := map[string]struct {
		connection *fakeTencentWebSocketConnection
		dialErr    error
	}{
		"handshake": {dialErr: errors.New("iam-secret must not escape")},
		"connack": {
			connection: &fakeTencentWebSocketConnection{reads: [][]byte{{0x20, 0x02, 0x00, 0x05}}, readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageBinary}},
		},
		"suback": {
			connection: &fakeTencentWebSocketConnection{reads: [][]byte{{0x20, 0x02, 0x00, 0x00}, {0x90, 0x03, 0x00, 0x02, 0x01}}, readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageBinary, cloudWebSocketMessageBinary}},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			responseFile := filepath.Join(t.TempDir(), "messages.ndjson")
			adapter := NewBaiduRESTAdapter(BaiduRESTConfig{
				Credentials: staticBCECredentials{credentials: BCECredentials{AccessKeyID: "iam-ak", SecretAccessKey: "iam-secret"}},
				IoTWebSocketDial: func(context.Context, string) (cloudWebSocketConnection, error) {
					return test.connection, test.dialErr
				},
			})
			_, err := adapter.Invoke(t.Context(), Invocation{
				Provider: ProviderBaidu, Mode: ModeRead, AuthScheme: authSchemeBaiduIoTCoreMQTTWS,
				Service: "iotcore", Operation: "SubscribeMQTT", Method: http.MethodGet,
				URL: "wss://aop098js.iot.gz.baidubce.com/mqtt",
				Body: map[string]any{
					"client_id": "observer-1", "max_messages": 1, "timeout_seconds": 5,
					"subscriptions": []any{map[string]any{"topic_filter": "sensors/#", "qos": 1}},
				},
				ResponseFile: responseFile, MaxResponseFileBytes: 4096,
			})
			if err == nil || strings.Contains(err.Error(), "iam-secret") {
				t.Fatalf("err=%v", err)
			}
			if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
				t.Fatalf("failed exchange published response_file: %v", statErr)
			}
		})
	}
}

func FuzzBaiduIoTCoreMQTTTargetAndPlan(f *testing.F) {
	f.Add("wss://aop098js.iot.gz.baidubce.com/mqtt", `{"client_id":"observer-1","subscriptions":[{"topic_filter":"sensors/#","qos":1}],"max_messages":1,"timeout_seconds":5}`)
	f.Add("wss://aop098js.iot.gz.baidubce.com.attacker.example/mqtt", `{"client_id":"observer-1","subscriptions":[{"topic_filter":"#","qos":0}],"max_messages":1,"timeout_seconds":5}`)
	f.Fuzz(func(t *testing.T, rawURL, rawBody string) {
		if len(rawURL) > 2048 || len(rawBody) > maxRequestPayloadBytes {
			return
		}
		var body any
		if json.Unmarshal([]byte(rawBody), &body) != nil {
			return
		}
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("parser panicked: %v", recovered)
			}
		}()
		_ = validateBaiduIoTCoreMQTTInvocation(Invocation{
			Provider: ProviderBaidu, Mode: ModeRead, AuthScheme: authSchemeBaiduIoTCoreMQTTWS,
			Service: "iotcore", Operation: "SubscribeMQTT", Method: http.MethodGet,
			URL: rawURL, Body: body, ResponseFile: "/approved/messages.ndjson",
		})
	})
}

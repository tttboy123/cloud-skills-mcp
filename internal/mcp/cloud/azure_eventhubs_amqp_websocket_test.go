package cloud

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azeventhubs/v2"
)

func eventHubsTestInvocation(root string) Invocation {
	return Invocation{
		Provider: ProviderAzure, Mode: ModeRead, AuthScheme: "eventhubs-amqp-ws",
		Service: "eventhubs", Operation: "ReceiveEvents", Method: http.MethodGet,
		URL: "wss://telemetry.servicebus.windows.net/$servicebus/websocket",
		Body: map[string]any{
			"event_hub": "measurements", "consumer_group": "$Default", "partition_id": "0",
			"start_position": map[string]any{"earliest": true, "inclusive": false},
			"max_events":     10, "timeout_seconds": 30, "prefetch": 10,
		},
		ResponseFile: filepath.Join(root, "events.ndjson"), MaxResponseFileBytes: 8192,
	}
}

func TestAzureEventHubsAMQPValidationAndPolicy(t *testing.T) {
	base := eventHubsTestInvocation(t.TempDir())
	if err := validateInvocation(base, []string{filepath.Dir(base.ResponseFile)}); err != nil {
		t.Fatalf("valid Event Hubs receive rejected: %v", err)
	}
	if !classifyRead(ProviderAzure, base) {
		t.Fatal("ReceiveEvents was not read-only")
	}
	properties := base
	properties.Operation = "GetProperties"
	properties.Body = map[string]any{"event_hub": "measurements", "partition_id": "0", "timeout_seconds": 30}
	if err := validateInvocation(properties, []string{filepath.Dir(properties.ResponseFile)}); err != nil || !classifyRead(ProviderAzure, properties) {
		t.Fatalf("GetProperties validation=%v read=%t", err, classifyRead(ProviderAzure, properties))
	}
	send := base
	send.Mode, send.Operation = ModeMutate, "SendEvents"
	send.Body = map[string]any{"event_hub": "measurements", "partition_key": "device-1", "events": []any{map[string]any{"body_base64": "eyJ2YWx1ZSI6MX0=", "content_type": "application/json"}}}
	send.ResponseFile = filepath.Join(t.TempDir(), "send.ndjson")
	if err := validateInvocation(send, []string{filepath.Dir(send.ResponseFile)}); err != nil || classifyRead(ProviderAzure, send) {
		t.Fatalf("SendEvents validation=%v read=%t", err, classifyRead(ProviderAzure, send))
	}
}

func TestAzureEventHubsAMQPRejectsUnsafeOrAmbiguousPlans(t *testing.T) {
	base := eventHubsTestInvocation(t.TempDir())
	tests := map[string]func(*Invocation){
		"wrong host":        func(v *Invocation) { v.URL = "wss://example.com/$servicebus/websocket" },
		"port":              func(v *Invocation) { v.URL = "wss://telemetry.servicebus.windows.net:444/$servicebus/websocket" },
		"query":             func(v *Invocation) { v.URL += "?token=x" },
		"headers":           func(v *Invocation) { v.Headers = map[string]string{"Sec-WebSocket-Protocol": "caller"} },
		"missing partition": func(v *Invocation) { delete(v.Body.(map[string]any), "partition_id") },
		"too many events":   func(v *Invocation) { v.Body.(map[string]any)["max_events"] = 257 },
		"too long":          func(v *Invocation) { v.Body.(map[string]any)["timeout_seconds"] = 301 },
		"ambiguous start": func(v *Invocation) {
			v.Body.(map[string]any)["start_position"] = map[string]any{"earliest": true, "offset": "4"}
		},
		"owner capability": func(v *Invocation) { v.Body.(map[string]any)["owner_level"] = 1 },
		"credential":       func(v *Invocation) { v.Body.(map[string]any)["sas_token"] = "caller" },
		"wrong mode":       func(v *Invocation) { v.Mode = ModeMutate },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := base
			candidate.Body = map[string]any{"event_hub": "measurements", "consumer_group": "$Default", "partition_id": "0", "start_position": map[string]any{"earliest": true}, "max_events": 10, "timeout_seconds": 30, "prefetch": 10}
			mutate(&candidate)
			if err := validateAzureEventHubsAMQPInvocation(candidate); err == nil {
				t.Fatal("unsafe Event Hubs invocation accepted")
			}
		})
	}
}

func TestAzureEventHubsStartPositionsAndSendPlan(t *testing.T) {
	positions := []map[string]any{
		{"earliest": true}, {"latest": true}, {"offset": "42", "inclusive": true},
		{"sequence_number": 7}, {"enqueued_time": "2030-01-02T03:04:05Z"},
	}
	for _, position := range positions {
		plan, err := parseAzureEventHubsAMQPPlan("ReceiveEvents", map[string]any{
			"event_hub": "measurements", "consumer_group": "$Default", "partition_id": "0",
			"start_position": position, "max_events": 1, "timeout_seconds": 30,
		})
		if err != nil || plan.PartitionID != "0" {
			t.Errorf("position=%v plan=%#v err=%v", position, plan, err)
		}
	}
	plan, err := parseAzureEventHubsAMQPPlan("SendEvents", map[string]any{
		"event_hub": "measurements", "partition_id": "0", "events": []any{map[string]any{
			"body_base64": "AAE=", "message_id": "m1", "correlation_id": "c1", "content_type": "application/octet-stream",
			"properties": map[string]any{"device": "d1", "attempt": 1, "ok": true},
		}},
	})
	if err != nil || len(plan.Events) != 1 || !bytes.Equal(plan.Events[0].Body, []byte{0, 1}) {
		t.Fatalf("plan=%#v err=%v", plan, err)
	}
	if _, err := parseAzureEventHubsAMQPPlan("SendEvents", map[string]any{"event_hub": "measurements", "partition_id": "0", "partition_key": "device", "events": []any{map[string]any{"body_base64": "aA=="}}}); err == nil {
		t.Fatal("partition_id and partition_key accepted together")
	}
}

type fakeAzureEventHubsAMQPExecutor struct {
	called  bool
	fqdn    string
	plan    azureEventHubsAMQPPlan
	records []map[string]any
	err     error
}

func (f *fakeAzureEventHubsAMQPExecutor) Execute(_ context.Context, fqdn string, plan azureEventHubsAMQPPlan) ([]map[string]any, string, error) {
	f.called, f.fqdn, f.plan = true, fqdn, plan
	return f.records, "tracking-2", f.err
}

func TestAzureEventHubsAMQPInvokePublishesAtomicNDJSON(t *testing.T) {
	root := t.TempDir()
	invocation := eventHubsTestInvocation(root)
	fake := &fakeAzureEventHubsAMQPExecutor{records: []map[string]any{{
		"type": "event", "body_base64": "eyJ2YWx1ZSI6MX0=", "partition_id": "0", "sequence_number": int64(9), "offset": "42",
	}}}
	adapter := NewAzureRESTAdapter(AzureRESTConfig{})
	adapter.config.EventHubsAMQP = fake
	result, err := adapter.Invoke(t.Context(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := os.ReadFile(invocation.ResponseFile)
	if readErr != nil || !bytes.Contains(data, []byte(`"sequence_number":9`)) || bytes.Contains(data, []byte("private")) {
		t.Fatalf("output=%s read_err=%v", data, readErr)
	}
	if !fake.called || fake.fqdn != "telemetry.servicebus.windows.net" || result.RequestID != "tracking-2" {
		t.Fatalf("fake=%#v result=%#v", fake, result)
	}
}

func TestAzureEventHubsAMQPFailureNeverPublishesPartialFile(t *testing.T) {
	invocation := eventHubsTestInvocation(t.TempDir())
	adapter := NewAzureRESTAdapter(AzureRESTConfig{})
	adapter.config.EventHubsAMQP = &fakeAzureEventHubsAMQPExecutor{err: errors.New("receive failed")}
	if _, err := adapter.Invoke(t.Context(), invocation); err == nil {
		t.Fatal("executor failure accepted")
	}
	if _, err := os.Stat(invocation.ResponseFile); !os.IsNotExist(err) {
		t.Fatalf("partial file published: %v", err)
	}
}

func TestAzureEventHubsSDKOptionsAllowOnlyTheExactNamespaceWebSocketTarget(t *testing.T) {
	for _, fqdn := range []string{
		"telemetry.servicebus.windows.net",
		"telemetry.servicebus.usgovcloudapi.net",
		"telemetry.servicebus.chinacloudapi.cn",
	} {
		t.Run(fqdn, func(t *testing.T) {
			var targets []string
			executor := &azureSDKEventHubsAMQPExecutor{dial: func(_ context.Context, target string) (net.Conn, error) {
				targets = append(targets, target)
				return nil, errors.New("intentional dial stop")
			}}
			expected := "wss://" + fqdn + azureAMQPWebSocketPath
			for _, dial := range []func(context.Context, azeventhubs.WebSocketConnParams) (net.Conn, error){
				executor.producerOptions(fqdn).NewWebSocketConn,
				executor.consumerOptions(fqdn).NewWebSocketConn,
			} {
				if _, err := dial(t.Context(), azeventhubs.WebSocketConnParams{Host: expected}); err == nil {
					t.Fatal("intentional exact-target dial failure was accepted")
				}
				if _, err := dial(t.Context(), azeventhubs.WebSocketConnParams{Host: "wss://evil.example/$servicebus/websocket"}); err == nil {
					t.Fatal("unexpected SDK target was accepted")
				}
			}
			if len(targets) != 2 || targets[0] != expected || targets[1] != expected {
				t.Fatalf("dial targets=%q", targets)
			}
		})
	}
}

func TestAzureEventHubsSDKExecuteRoutesSendAndReadThroughWSS(t *testing.T) {
	tests := []struct {
		operation string
		body      map[string]any
	}{
		{"SendEvents", map[string]any{"event_hub": "measurements", "events": []any{map[string]any{"body_base64": "aGk="}}, "timeout_seconds": 1}},
		{"GetProperties", map[string]any{"event_hub": "measurements", "partition_id": "0", "timeout_seconds": 1}},
	}
	for _, test := range tests {
		t.Run(test.operation, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			var target string
			executor := &azureSDKEventHubsAMQPExecutor{
				credential: azureAMQPTokenCredential{provider: azureTokenProviderFunc(func(context.Context, string) (string, error) { return "unused", nil })},
				dial: func(_ context.Context, requested string) (net.Conn, error) {
					target = requested
					cancel()
					return nil, errors.New("intentional dial stop")
				},
			}
			plan, err := parseAzureEventHubsAMQPPlan(test.operation, test.body)
			if err != nil {
				t.Fatal(err)
			}
			_, _, _ = executor.Execute(ctx, "telemetry.servicebus.windows.net", plan)
			if target != "wss://telemetry.servicebus.windows.net/$servicebus/websocket" {
				t.Fatalf("target=%q", target)
			}
		})
	}
}

func TestAzureEventHubsRecordsAndStartPositionsAreCanonical(t *testing.T) {
	now := time.Date(2030, 1, 2, 3, 4, 5, 6, time.UTC)
	contentType, messageID, partitionKey := "application/json", "event-1", "device-1"
	event := &azeventhubs.ReceivedEventData{
		EventData: azeventhubs.EventData{
			Body: []byte(`{"value":1}`), ContentType: &contentType, MessageID: &messageID,
			CorrelationID: []byte("correlation"), Properties: map[string]any{"ok": true, "when": now},
		},
		EnqueuedTime: &now, PartitionKey: &partitionKey, Offset: "42", SequenceNumber: 9,
		SystemProperties: map[string]any{"binary": []byte{0, 1}},
	}
	record := azureEventHubsEventRecord("0", event)
	if record["body_base64"] != "eyJ2YWx1ZSI6MX0=" || record["partition_key"] != partitionKey || record["message_id"] != messageID {
		t.Fatalf("event record=%#v", record)
	}
	if strings.Contains(fmt.Sprint(record), "RawAMQPMessage") {
		t.Fatalf("raw AMQP capability leaked: %#v", record)
	}
	positions := []azureEventHubsStartPosition{
		{Earliest: true}, {Latest: true}, {Offset: "42"}, {SequenceNumber: int64Pointer(7)}, {EnqueuedTime: now.Format(time.RFC3339), EnqueuedValue: now},
	}
	for _, input := range positions {
		position := toAzureEventHubsStartPosition(input)
		selected := 0
		for _, present := range []bool{position.Earliest != nil, position.Latest != nil, position.Offset != nil, position.SequenceNumber != nil, position.EnqueuedTime != nil} {
			if present {
				selected++
			}
		}
		if selected != 1 {
			t.Fatalf("input=%#v output=%#v", input, position)
		}
	}
	partition := azureEventHubsPartitionRecord(azeventhubs.PartitionProperties{
		EventHubName: "measurements", PartitionID: "0", BeginningSequenceNumber: 1,
		LastEnqueuedSequenceNumber: 9, LastEnqueuedOffset: "42", LastEnqueuedOn: now,
	})
	if partition["event_hub"] != "measurements" || partition["last_enqueued_sequence_number"] != int64(9) {
		t.Fatalf("partition record=%#v", partition)
	}
}

func TestAzureAMQPSanitizeValuePreservesOnlyBoundedJSONShapes(t *testing.T) {
	now := time.Date(2030, 1, 2, 3, 4, 5, 6, time.UTC)
	properties := azureAMQPSanitizeProperties(map[string]any{
		"bytes": []byte{0, 1}, "time": now, "items": []any{"x", 1, true}, "nested": map[string]any{"n": int64(2)},
	})
	if properties["time"] != now.Format(time.RFC3339Nano) || properties["bytes"].(map[string]any)["binary_base64"] != "AAE=" {
		t.Fatalf("sanitized=%#v", properties)
	}
	if got := azureAMQPSanitizeValue(struct{ Secret string }{Secret: "opaque"}, 0); got != "{opaque}" {
		t.Fatalf("fallback=%#v", got)
	}
	if got := azureAMQPSanitizeValue("too-deep", 5); got != nil {
		t.Fatalf("deep value=%#v", got)
	}
}

func TestAzureAMQPPropertyAndEntityBounds(t *testing.T) {
	tooMany := make(map[string]any, 129)
	for index := 0; index < 129; index++ {
		tooMany[fmt.Sprintf("k%d", index)] = index
	}
	for name, properties := range map[string]map[string]any{
		"too many":   tooMany,
		"too large":  {"value": strings.Repeat("x", azureAMQPMaxPropertyBytes)},
		"bad name":   {"../secret": true},
		"not scalar": {"nested": map[string]any{"x": 1}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateAzureAMQPProperties(properties); err == nil {
				t.Fatal("invalid properties accepted")
			}
		})
	}
	for _, value := range []any{nil, true, "text", float64(1), float32(1), int64(1)} {
		if !azureAMQPJSONScalar(value) {
			t.Fatalf("valid scalar rejected: %#v", value)
		}
	}
	if azureAMQPJSONScalar(math.NaN()) || azureAMQPJSONScalar(complex(1, 2)) {
		t.Fatal("invalid scalar accepted")
	}
	for _, entity := range []string{"", "../secret", "a//b", "a?b", strings.Repeat("x", 51)} {
		if validAzureMessagingEntity(entity, 50, false) {
			t.Fatalf("invalid entity accepted: %q", entity)
		}
	}
}

func TestAzureEventHubsStartPositionRejectsEveryAmbiguousOrUnboundedForm(t *testing.T) {
	sequence := int64(-2)
	for name, position := range map[string]*azureEventHubsStartPosition{
		"none":          {},
		"negative seq":  {SequenceNumber: &sequence},
		"long offset":   {Offset: strings.Repeat("x", 129)},
		"bad timestamp": {EnqueuedTime: "tomorrow"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateAzureEventHubsStartPosition(position); err == nil {
				t.Fatal("invalid start position accepted")
			}
		})
	}
}

func int64Pointer(value int64) *int64 { return &value }

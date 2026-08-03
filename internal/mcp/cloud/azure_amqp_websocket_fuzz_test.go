package cloud

import (
	"encoding/json"
	"strings"
	"testing"
)

func FuzzAzureMessagingAMQPWebSocketTargetNeverEscapesActiveClouds(f *testing.F) {
	for _, rawURL := range []string{
		"wss://orders.servicebus.windows.net/$servicebus/websocket",
		"wss://orders.servicebus.usgovcloudapi.net/$servicebus/websocket",
		"wss://orders.servicebus.chinacloudapi.cn/$servicebus/websocket",
		"wss://orders.servicebus.cloudapi.de/$servicebus/websocket",
		"wss://orders.privatelink.servicebus.usgovcloudapi.net/$servicebus/websocket",
	} {
		f.Add(rawURL)
	}
	f.Fuzz(func(t *testing.T, rawURL string) {
		if len(rawURL) > 2048 {
			t.Skip()
		}
		fqdn, err := parseAzureAMQPWebSocketTarget(rawURL)
		if err != nil {
			return
		}
		for _, suffix := range azureAMQPActiveNamespaceSuffixes {
			name := strings.TrimSuffix(fqdn, suffix)
			if name != fqdn && name != "" && !strings.Contains(name, ".") && endpointLabelPattern.MatchString(name) {
				return
			}
		}
		t.Fatalf("accepted endpoint escaped the active-cloud namespace contract: %q", fqdn)
	})
}

func FuzzAzureMessagingAMQPPlansNeverPanic(f *testing.F) {
	f.Add([]byte(`{"queue":"orders","max_messages":1,"timeout_seconds":1}`))
	f.Add([]byte(`{"event_hub":"telemetry","partition_id":"0","start_position":{"earliest":true},"max_events":1,"timeout_seconds":1}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxRequestPayloadBytes {
			t.Skip()
		}
		var body any
		if json.Unmarshal(data, &body) != nil {
			return
		}
		for _, operation := range []string{"PeekMessages", "SendMessages", "ScheduleMessages", "CancelScheduledMessages", "ReceiveMessages", "ReceiveDeferredMessages", "GetSessionState", "SetSessionState"} {
			_, _ = parseAzureServiceBusAMQPPlan(operation, body)
		}
		for _, operation := range []string{"GetProperties", "ReceiveEvents", "SendEvents"} {
			_, _ = parseAzureEventHubsAMQPPlan(operation, body)
		}
	})
}

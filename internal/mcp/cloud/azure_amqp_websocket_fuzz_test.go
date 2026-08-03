package cloud

import (
	"encoding/json"
	"testing"
)

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

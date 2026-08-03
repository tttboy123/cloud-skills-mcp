package cloud

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azeventhubs/v2"
)

const (
	azureEventHubsMaxEvents = 256
	azureEventHubsMaxSend   = 100
)

type azureEventHubsAMQPEvent struct {
	BodyBase64    string         `json:"body_base64"`
	ContentType   string         `json:"content_type,omitempty"`
	CorrelationID string         `json:"correlation_id,omitempty"`
	MessageID     string         `json:"message_id,omitempty"`
	Properties    map[string]any `json:"properties,omitempty"`
	Body          []byte         `json:"-"`
}

type azureEventHubsStartPosition struct {
	Earliest       bool      `json:"earliest,omitempty"`
	Latest         bool      `json:"latest,omitempty"`
	Offset         string    `json:"offset,omitempty"`
	SequenceNumber *int64    `json:"sequence_number,omitempty"`
	EnqueuedTime   string    `json:"enqueued_time,omitempty"`
	Inclusive      bool      `json:"inclusive,omitempty"`
	EnqueuedValue  time.Time `json:"-"`
}

type azureEventHubsAMQPPlan struct {
	EventHub       string                       `json:"event_hub"`
	ConsumerGroup  string                       `json:"consumer_group,omitempty"`
	PartitionID    string                       `json:"partition_id,omitempty"`
	PartitionKey   string                       `json:"partition_key,omitempty"`
	Events         []azureEventHubsAMQPEvent    `json:"events,omitempty"`
	StartPosition  *azureEventHubsStartPosition `json:"start_position,omitempty"`
	MaxEvents      int                          `json:"max_events,omitempty"`
	TimeoutSeconds int                          `json:"timeout_seconds,omitempty"`
	Prefetch       int32                        `json:"prefetch,omitempty"`
	Operation      string                       `json:"-"`
}

func validateAzureEventHubsAMQPInvocation(invocation Invocation) error {
	if !strings.EqualFold(invocation.Service, "eventhubs") {
		return fmt.Errorf("Azure Event Hubs AMQP requires service eventhubs")
	}
	if _, err := validateAzureAMQPEnvelope(invocation); err != nil {
		return err
	}
	operation := strings.ToLower(strings.TrimSpace(invocation.Operation))
	readOnly := operation == "receiveevents" || operation == "getproperties"
	if !readOnly && operation != "sendevents" {
		return fmt.Errorf("Azure Event Hubs AMQP operation must be GetProperties, ReceiveEvents, or SendEvents")
	}
	if readOnly && invocation.Mode != ModeRead || !readOnly && invocation.Mode != ModeMutate {
		return fmt.Errorf("Azure Event Hubs GetProperties and ReceiveEvents require the read tool; SendEvents requires the mutate tool")
	}
	_, err := parseAzureEventHubsAMQPPlan(invocation.Operation, invocation.Body)
	return err
}

func parseAzureEventHubsAMQPPlan(operation string, body any) (azureEventHubsAMQPPlan, error) {
	var plan azureEventHubsAMQPPlan
	if body == nil || azureRealtimeContainsCredentialField(body) {
		return plan, fmt.Errorf("Azure Event Hubs AMQP requires a credential-free plan")
	}
	if err := decodeAzureAMQPPlan(body, &plan); err != nil {
		return plan, err
	}
	plan.Operation = strings.ToLower(strings.TrimSpace(operation))
	if !validAzureMessagingEntity(plan.EventHub, 256, false) {
		return plan, fmt.Errorf("Azure Event Hubs event_hub is invalid or exceeds 256 characters")
	}
	if plan.ConsumerGroup == "" {
		plan.ConsumerGroup = azeventhubs.DefaultConsumerGroup
	}
	if strings.HasPrefix(plan.ConsumerGroup, "$") && plan.ConsumerGroup != azeventhubs.DefaultConsumerGroup {
		return plan, fmt.Errorf("Azure Event Hubs reserved consumer groups other than $Default are not exposed")
	}
	if !validAzureMessagingEntity(plan.ConsumerGroup, 50, true) || plan.PartitionID != "" && !validAzureMessagingEntity(plan.PartitionID, 128, false) || plan.PartitionKey != "" && (len(plan.PartitionKey) > 128 || !utf8.ValidString(plan.PartitionKey)) {
		return plan, fmt.Errorf("Azure Event Hubs consumer group, partition ID, or partition key is invalid")
	}
	for index := range plan.Events {
		if err := validateAzureEventHubsEvent(&plan.Events[index]); err != nil {
			return plan, fmt.Errorf("Azure Event Hubs event %d: %w", index, err)
		}
	}
	if plan.TimeoutSeconds == 0 && plan.Operation != "receiveevents" {
		plan.TimeoutSeconds = 60
	}
	if plan.TimeoutSeconds < 1 || plan.TimeoutSeconds > azureAMQPMaxTimeoutSeconds {
		return plan, fmt.Errorf("Azure Event Hubs timeout_seconds must be between 1 and %d", azureAMQPMaxTimeoutSeconds)
	}
	if err := validateAzureEventHubsOperationPlan(&plan); err != nil {
		return plan, err
	}
	return plan, nil
}

func validateAzureEventHubsEvent(event *azureEventHubsAMQPEvent) error {
	body, err := base64.StdEncoding.Strict().DecodeString(event.BodyBase64)
	if err != nil || len(body) > maxRequestPayloadBytes {
		return fmt.Errorf("body_base64 must be bounded canonical base64")
	}
	event.Body = body
	if len(event.ContentType) > 4096 || len(event.CorrelationID) > 4096 || len(event.MessageID) > 128 || !utf8.ValidString(event.ContentType+event.CorrelationID+event.MessageID) {
		return fmt.Errorf("event metadata exceeds the broker bound")
	}
	return validateAzureAMQPProperties(event.Properties)
}

func validateAzureEventHubsOperationPlan(plan *azureEventHubsAMQPPlan) error {
	switch plan.Operation {
	case "sendevents":
		if len(plan.Events) < 1 || len(plan.Events) > azureEventHubsMaxSend || plan.PartitionID != "" && plan.PartitionKey != "" || plan.StartPosition != nil || plan.MaxEvents != 0 || plan.Prefetch != 0 || plan.ConsumerGroup != azeventhubs.DefaultConsumerGroup {
			return fmt.Errorf("Azure Event Hubs SendEvents requires 1..100 events, optional partition_id or partition_key, and only sender fields")
		}
	case "getproperties":
		if len(plan.Events) != 0 || plan.PartitionKey != "" || plan.StartPosition != nil || plan.MaxEvents != 0 || plan.Prefetch != 0 || plan.ConsumerGroup != azeventhubs.DefaultConsumerGroup {
			return fmt.Errorf("Azure Event Hubs GetProperties accepts only event_hub, optional partition_id, and timeout_seconds")
		}
	case "receiveevents":
		if plan.PartitionID == "" || len(plan.Events) != 0 || plan.PartitionKey != "" || plan.MaxEvents < 1 || plan.MaxEvents > azureEventHubsMaxEvents || plan.StartPosition == nil {
			return fmt.Errorf("Azure Event Hubs ReceiveEvents requires partition_id, one start_position, and max_events 1..256")
		}
		if plan.Prefetch < -1 || plan.Prefetch > 5000 {
			return fmt.Errorf("Azure Event Hubs prefetch must be -1..5000")
		}
		if plan.Prefetch == 0 {
			plan.Prefetch = int32(plan.MaxEvents)
		}
		if err := validateAzureEventHubsStartPosition(plan.StartPosition); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported Azure Event Hubs AMQP operation")
	}
	return nil
}

func validateAzureEventHubsStartPosition(position *azureEventHubsStartPosition) error {
	selected := 0
	if position.Earliest {
		selected++
	}
	if position.Latest {
		selected++
	}
	if position.Offset != "" {
		selected++
		if len(position.Offset) > 128 || !utf8.ValidString(position.Offset) {
			return fmt.Errorf("Azure Event Hubs offset exceeds the bound")
		}
	}
	if position.SequenceNumber != nil {
		selected++
		if *position.SequenceNumber < -1 {
			return fmt.Errorf("Azure Event Hubs sequence_number must be at least -1")
		}
	}
	if position.EnqueuedTime != "" {
		selected++
		parsed, err := time.Parse(time.RFC3339, position.EnqueuedTime)
		if err != nil {
			return fmt.Errorf("Azure Event Hubs enqueued_time must be RFC3339")
		}
		position.EnqueuedValue = parsed
	}
	if selected != 1 {
		return fmt.Errorf("Azure Event Hubs start_position requires exactly one of earliest, latest, offset, sequence_number, or enqueued_time")
	}
	return nil
}

func invokeAzureEventHubsAMQP(ctx context.Context, adapter *AzureRESTAdapter, invocation Invocation) (InvocationResult, error) {
	if err := validateAzureEventHubsAMQPInvocation(invocation); err != nil {
		return InvocationResult{}, err
	}
	fqdn, _ := parseAzureAMQPWebSocketTarget(invocation.URL)
	plan, _ := parseAzureEventHubsAMQPPlan(invocation.Operation, invocation.Body)
	records, requestID, err := adapter.config.EventHubsAMQP.Execute(ctx, fqdn, plan)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("Azure Event Hubs AMQP operation failed: %w", err)
	}
	return publishAzureAMQPNDJSON(invocation, "Azure Event Hubs AMQP", records, requestID)
}

type azureSDKEventHubsAMQPExecutor struct {
	credential azureAMQPTokenCredential
	dial       azureAMQPWebSocketDial
}

func (executor *azureSDKEventHubsAMQPExecutor) Execute(ctx context.Context, fqdn string, plan azureEventHubsAMQPPlan) ([]map[string]any, string, error) {
	operationContext, cancel := context.WithTimeout(ctx, time.Duration(plan.TimeoutSeconds)*time.Second)
	defer cancel()
	if plan.Operation == "sendevents" {
		return executor.send(operationContext, fqdn, plan)
	}
	return executor.read(operationContext, fqdn, plan)
}

func (executor *azureSDKEventHubsAMQPExecutor) producerOptions(fqdn string) *azeventhubs.ProducerClientOptions {
	expectedTarget := "wss://" + fqdn + azureAMQPWebSocketPath
	return &azeventhubs.ProducerClientOptions{
		ApplicationID: "cloud-skills-mcp",
		NewWebSocketConn: func(ctx context.Context, args azeventhubs.WebSocketConnParams) (net.Conn, error) {
			if args.Host != expectedTarget {
				return nil, fmt.Errorf("Azure Event Hubs SDK requested an unexpected WebSocket target")
			}
			return executor.dial(ctx, args.Host)
		},
	}
}

func (executor *azureSDKEventHubsAMQPExecutor) consumerOptions(fqdn string) *azeventhubs.ConsumerClientOptions {
	expectedTarget := "wss://" + fqdn + azureAMQPWebSocketPath
	return &azeventhubs.ConsumerClientOptions{
		ApplicationID: "cloud-skills-mcp",
		NewWebSocketConn: func(ctx context.Context, args azeventhubs.WebSocketConnParams) (net.Conn, error) {
			if args.Host != expectedTarget {
				return nil, fmt.Errorf("Azure Event Hubs SDK requested an unexpected WebSocket target")
			}
			return executor.dial(ctx, args.Host)
		},
	}
}

func (executor *azureSDKEventHubsAMQPExecutor) send(ctx context.Context, fqdn string, plan azureEventHubsAMQPPlan) ([]map[string]any, string, error) {
	client, err := azeventhubs.NewProducerClient(fqdn, plan.EventHub, executor.credential, executor.producerOptions(fqdn))
	if err != nil {
		return nil, "", err
	}
	defer closeAzureEventHubsProducer(client)
	options := &azeventhubs.EventDataBatchOptions{}
	if plan.PartitionID != "" {
		options.PartitionID = &plan.PartitionID
	}
	if plan.PartitionKey != "" {
		options.PartitionKey = &plan.PartitionKey
	}
	batch, err := client.NewEventDataBatch(ctx, options)
	if err != nil {
		return nil, "", err
	}
	for _, input := range plan.Events {
		event := &azeventhubs.EventData{Body: input.Body, Properties: input.Properties}
		if input.ContentType != "" {
			event.ContentType = &input.ContentType
		}
		if input.CorrelationID != "" {
			event.CorrelationID = input.CorrelationID
		}
		if input.MessageID != "" {
			event.MessageID = &input.MessageID
		}
		if err := batch.AddEventData(event, nil); err != nil {
			return nil, "", err
		}
	}
	if err := client.SendEventDataBatch(ctx, batch, nil); err != nil {
		return nil, "", err
	}
	return []map[string]any{{"type": "send_result", "sent": len(plan.Events), "bytes": batch.NumBytes()}}, "", nil
}

func (executor *azureSDKEventHubsAMQPExecutor) read(ctx context.Context, fqdn string, plan azureEventHubsAMQPPlan) ([]map[string]any, string, error) {
	client, err := azeventhubs.NewConsumerClient(fqdn, plan.EventHub, plan.ConsumerGroup, executor.credential, executor.consumerOptions(fqdn))
	if err != nil {
		return nil, "", err
	}
	defer closeAzureEventHubsConsumer(client)
	if plan.Operation == "getproperties" {
		properties, err := client.GetEventHubProperties(ctx, nil)
		if err != nil {
			return nil, "", err
		}
		records := []map[string]any{{
			"type": "event_hub_properties", "name": properties.Name, "created_on": properties.CreatedOn.UTC().Format(time.RFC3339Nano),
			"partition_ids": properties.PartitionIDs, "geo_replication_enabled": properties.GeoReplicationEnabled,
		}}
		if plan.PartitionID != "" {
			partition, err := client.GetPartitionProperties(ctx, plan.PartitionID, nil)
			if err != nil {
				return nil, "", err
			}
			records = append(records, azureEventHubsPartitionRecord(partition))
		}
		return records, "", nil
	}
	partitionClient, err := client.NewPartitionClient(plan.PartitionID, &azeventhubs.PartitionClientOptions{
		StartPosition: toAzureEventHubsStartPosition(*plan.StartPosition), Prefetch: plan.Prefetch,
	})
	if err != nil {
		return nil, "", err
	}
	defer closeAzureEventHubsPartition(partitionClient)
	events, err := partitionClient.ReceiveEvents(ctx, plan.MaxEvents, nil)
	if err != nil && len(events) == 0 {
		if ctx.Err() != nil {
			return []map[string]any{}, "", nil
		}
		return nil, "", err
	}
	records := make([]map[string]any, 0, len(events))
	for _, event := range events {
		records = append(records, azureEventHubsEventRecord(plan.PartitionID, event))
	}
	return records, "", nil
}

func toAzureEventHubsStartPosition(input azureEventHubsStartPosition) azeventhubs.StartPosition {
	position := azeventhubs.StartPosition{Inclusive: input.Inclusive}
	if input.Earliest {
		position.Earliest = &input.Earliest
	}
	if input.Latest {
		position.Latest = &input.Latest
	}
	if input.Offset != "" {
		position.Offset = &input.Offset
	}
	position.SequenceNumber = input.SequenceNumber
	if input.EnqueuedTime != "" {
		position.EnqueuedTime = &input.EnqueuedValue
	}
	return position
}

func azureEventHubsEventRecord(partitionID string, event *azeventhubs.ReceivedEventData) map[string]any {
	record := map[string]any{
		"type": "event", "partition_id": partitionID, "body_base64": base64.StdEncoding.EncodeToString(event.Body),
		"offset": event.Offset, "sequence_number": event.SequenceNumber,
	}
	if event.EnqueuedTime != nil {
		record["enqueued_time"] = event.EnqueuedTime.UTC().Format(time.RFC3339Nano)
	}
	if event.PartitionKey != nil {
		record["partition_key"] = *event.PartitionKey
	}
	if event.ContentType != nil {
		record["content_type"] = *event.ContentType
	}
	if event.MessageID != nil {
		record["message_id"] = *event.MessageID
	}
	if event.CorrelationID != nil {
		record["correlation_id"] = azureAMQPSanitizeValue(event.CorrelationID, 0)
	}
	if properties := azureAMQPSanitizeProperties(event.Properties); len(properties) != 0 {
		record["properties"] = properties
	}
	if properties := azureAMQPSanitizeProperties(event.SystemProperties); len(properties) != 0 {
		record["system_properties"] = properties
	}
	return record
}

func azureEventHubsPartitionRecord(properties azeventhubs.PartitionProperties) map[string]any {
	return map[string]any{
		"type": "partition_properties", "event_hub": properties.EventHubName, "partition_id": properties.PartitionID,
		"beginning_sequence_number": properties.BeginningSequenceNumber, "last_enqueued_sequence_number": properties.LastEnqueuedSequenceNumber,
		"last_enqueued_offset": properties.LastEnqueuedOffset, "last_enqueued_on": properties.LastEnqueuedOn.UTC().Format(time.RFC3339Nano), "is_empty": properties.IsEmpty,
	}
}

func closeAzureEventHubsProducer(client *azeventhubs.ProducerClient) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = client.Close(ctx)
}

func closeAzureEventHubsConsumer(client *azeventhubs.ConsumerClient) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = client.Close(ctx)
}

func closeAzureEventHubsPartition(client *azeventhubs.PartitionClient) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = client.Close(ctx)
}

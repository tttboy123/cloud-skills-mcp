package cloud

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
)

const (
	azureServiceBusMaxSendMessages    = 100
	azureServiceBusMaxPeekMessages    = 250
	azureServiceBusMaxReceiveMessages = 100
)

type azureServiceBusAMQPMessage struct {
	BodyBase64            string         `json:"body_base64"`
	ContentType           string         `json:"content_type,omitempty"`
	CorrelationID         string         `json:"correlation_id,omitempty"`
	MessageID             string         `json:"message_id,omitempty"`
	PartitionKey          string         `json:"partition_key,omitempty"`
	ReplyTo               string         `json:"reply_to,omitempty"`
	ReplyToSessionID      string         `json:"reply_to_session_id,omitempty"`
	SessionID             *string        `json:"session_id,omitempty"`
	Subject               string         `json:"subject,omitempty"`
	TimeToLiveSeconds     int            `json:"time_to_live_seconds,omitempty"`
	To                    string         `json:"to,omitempty"`
	ApplicationProperties map[string]any `json:"application_properties,omitempty"`
	Body                  []byte         `json:"-"`
}

type azureServiceBusAMQPPlan struct {
	Queue                     string                       `json:"queue,omitempty"`
	Topic                     string                       `json:"topic,omitempty"`
	Subscription              string                       `json:"subscription,omitempty"`
	SubQueue                  string                       `json:"sub_queue,omitempty"`
	Messages                  []azureServiceBusAMQPMessage `json:"messages,omitempty"`
	MaxMessages               int                          `json:"max_messages,omitempty"`
	TimeoutSeconds            int                          `json:"timeout_seconds,omitempty"`
	FromSequenceNumber        *int64                       `json:"from_sequence_number,omitempty"`
	SequenceNumbers           []int64                      `json:"sequence_numbers,omitempty"`
	ScheduledEnqueueTime      string                       `json:"scheduled_enqueue_time,omitempty"`
	Settlement                string                       `json:"settlement,omitempty"`
	SettlementProperties      map[string]any               `json:"settlement_properties,omitempty"`
	DeadLetterReason          string                       `json:"dead_letter_reason,omitempty"`
	DeadLetterDescription     string                       `json:"dead_letter_description,omitempty"`
	SessionID                 *string                      `json:"session_id,omitempty"`
	AcceptNextSession         bool                         `json:"accept_next_session,omitempty"`
	SessionStateBase64        *string                      `json:"session_state_base64,omitempty"`
	SessionState              []byte                       `json:"-"`
	Operation                 string                       `json:"-"`
	ScheduledEnqueueTimeValue time.Time                    `json:"-"`
}

func validateAzureServiceBusAMQPInvocation(invocation Invocation) error {
	if !strings.EqualFold(invocation.Service, "servicebus") {
		return fmt.Errorf("Azure Service Bus AMQP requires service servicebus")
	}
	if _, err := validateAzureAMQPEnvelope(invocation); err != nil {
		return err
	}
	operation := strings.ToLower(strings.TrimSpace(invocation.Operation))
	readOnly := operation == "peekmessages"
	supported := readOnly || operation == "sendmessages" || operation == "schedulemessages" || operation == "cancelscheduledmessages" || operation == "receivemessages" || operation == "receivedeferredmessages" || operation == "getsessionstate" || operation == "setsessionstate"
	if !supported {
		return fmt.Errorf("Azure Service Bus AMQP operation must be PeekMessages, SendMessages, ScheduleMessages, CancelScheduledMessages, ReceiveMessages, ReceiveDeferredMessages, GetSessionState, or SetSessionState")
	}
	if readOnly && invocation.Mode != ModeRead || !readOnly && invocation.Mode != ModeMutate {
		return fmt.Errorf("Azure Service Bus PeekMessages requires the read tool; receive, settlement, send, schedule, cancel, and session operations require the mutate tool")
	}
	_, err := parseAzureServiceBusAMQPPlan(invocation.Operation, invocation.Body)
	return err
}

func parseAzureServiceBusAMQPPlan(operation string, body any) (azureServiceBusAMQPPlan, error) {
	var plan azureServiceBusAMQPPlan
	if body == nil || azureRealtimeContainsCredentialField(body) {
		return plan, fmt.Errorf("Azure Service Bus AMQP requires a credential-free plan")
	}
	if err := decodeAzureAMQPPlan(body, &plan); err != nil {
		return plan, err
	}
	plan.Operation = strings.ToLower(strings.TrimSpace(operation))
	plan.SubQueue = strings.ToLower(strings.TrimSpace(plan.SubQueue))
	plan.Settlement = strings.ToLower(strings.TrimSpace(plan.Settlement))
	if plan.TimeoutSeconds == 0 && (plan.Operation == "sendmessages" || plan.Operation == "schedulemessages" || plan.Operation == "cancelscheduledmessages") {
		plan.TimeoutSeconds = 60
	}
	if err := validateAzureServiceBusEntity(plan); err != nil {
		return plan, err
	}
	if err := validateAzureServiceBusSubQueue(plan.SubQueue); err != nil {
		return plan, err
	}
	if plan.SessionID != nil && plan.AcceptNextSession {
		return plan, fmt.Errorf("Azure Service Bus session_id and accept_next_session are mutually exclusive")
	}
	if (plan.SessionID != nil || plan.AcceptNextSession) && plan.SubQueue != "" {
		return plan, fmt.Errorf("Azure Service Bus sessions do not accept sub_queue")
	}
	if plan.SessionID != nil && (len(*plan.SessionID) > 128 || !utf8.ValidString(*plan.SessionID)) {
		return plan, fmt.Errorf("Azure Service Bus session_id exceeds the broker bound")
	}
	if len(plan.DeadLetterReason) > 4096 || len(plan.DeadLetterDescription) > 4096 {
		return plan, fmt.Errorf("Azure Service Bus dead-letter metadata is too large")
	}
	if err := validateAzureAMQPProperties(plan.SettlementProperties); err != nil {
		return plan, fmt.Errorf("Azure Service Bus settlement properties: %w", err)
	}
	for index := range plan.Messages {
		if err := validateAzureServiceBusMessage(&plan.Messages[index]); err != nil {
			return plan, fmt.Errorf("Azure Service Bus message %d: %w", index, err)
		}
	}
	for _, sequence := range plan.SequenceNumbers {
		if sequence <= 0 {
			return plan, fmt.Errorf("Azure Service Bus sequence numbers must be positive")
		}
	}
	if plan.SessionStateBase64 != nil {
		decoded, err := base64.StdEncoding.Strict().DecodeString(*plan.SessionStateBase64)
		if err != nil || len(decoded) > maxRequestPayloadBytes {
			return plan, fmt.Errorf("Azure Service Bus session_state_base64 must be bounded canonical base64")
		}
		plan.SessionState = decoded
	}
	if err := validateAzureServiceBusOperationPlan(&plan); err != nil {
		return plan, err
	}
	return plan, nil
}

func validateAzureServiceBusEntity(plan azureServiceBusAMQPPlan) error {
	queue := plan.Queue != ""
	topic := plan.Topic != ""
	if queue == topic || queue && plan.Subscription != "" || topic && plan.Operation != "sendmessages" && plan.Operation != "schedulemessages" && plan.Operation != "cancelscheduledmessages" && plan.Subscription == "" {
		return fmt.Errorf("Azure Service Bus plan requires either queue, or topic with subscription for receiver operations")
	}
	if !validAzureMessagingEntity(plan.Queue, 260, false) && queue || !validAzureMessagingEntity(plan.Topic, 260, false) && topic || plan.Subscription != "" && !validAzureMessagingEntity(plan.Subscription, 50, false) {
		return fmt.Errorf("Azure Service Bus entity name is invalid or exceeds the official length bound")
	}
	return nil
}

func validateAzureServiceBusSubQueue(value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "dead_letter", "transfer_dead_letter":
		return nil
	default:
		return fmt.Errorf("Azure Service Bus sub_queue must be dead_letter or transfer_dead_letter")
	}
}

func validateAzureServiceBusMessage(message *azureServiceBusAMQPMessage) error {
	body, err := base64.StdEncoding.Strict().DecodeString(message.BodyBase64)
	if err != nil || len(body) > maxRequestPayloadBytes {
		return fmt.Errorf("body_base64 must be bounded canonical base64")
	}
	message.Body = body
	for name, value := range map[string]string{
		"content_type": message.ContentType, "correlation_id": message.CorrelationID, "message_id": message.MessageID,
		"partition_key": message.PartitionKey, "reply_to": message.ReplyTo, "reply_to_session_id": message.ReplyToSessionID,
		"subject": message.Subject, "to": message.To,
	} {
		maximum := 4096
		if name == "message_id" || name == "partition_key" || name == "reply_to_session_id" {
			maximum = 128
		}
		if len(value) > maximum || !utf8.ValidString(value) {
			return fmt.Errorf("%s exceeds its broker bound", name)
		}
	}
	if message.SessionID != nil && (len(*message.SessionID) > 128 || !utf8.ValidString(*message.SessionID)) {
		return fmt.Errorf("session_id exceeds the broker bound")
	}
	if message.SessionID != nil && message.PartitionKey != "" && *message.SessionID != message.PartitionKey {
		return fmt.Errorf("session_id and partition_key must match when both are set")
	}
	if message.TimeToLiveSeconds < 0 || message.TimeToLiveSeconds > 365*24*60*60 {
		return fmt.Errorf("time_to_live_seconds is outside the bounded range")
	}
	return validateAzureAMQPProperties(message.ApplicationProperties)
}

func validateAzureServiceBusOperationPlan(plan *azureServiceBusAMQPPlan) error {
	requireTimeout := func() error {
		if plan.TimeoutSeconds < 1 || plan.TimeoutSeconds > azureAMQPMaxTimeoutSeconds {
			return fmt.Errorf("Azure Service Bus timeout_seconds must be between 1 and %d", azureAMQPMaxTimeoutSeconds)
		}
		return nil
	}
	sessionSelector := plan.SessionID != nil || plan.AcceptNextSession
	hasSettlementMetadata := len(plan.SettlementProperties) != 0 || plan.DeadLetterReason != "" || plan.DeadLetterDescription != ""
	switch plan.Operation {
	case "peekmessages":
		if plan.MaxMessages < 1 || plan.MaxMessages > azureServiceBusMaxPeekMessages || len(plan.Messages) != 0 || len(plan.SequenceNumbers) != 0 || plan.Settlement != "" || hasSettlementMetadata || plan.ScheduledEnqueueTime != "" || sessionSelector || plan.SessionStateBase64 != nil {
			return fmt.Errorf("Azure Service Bus PeekMessages requires max_messages 1..250 and only peek fields")
		}
		return requireTimeout()
	case "sendmessages":
		if len(plan.Messages) < 1 || len(plan.Messages) > azureServiceBusMaxSendMessages || plan.Subscription != "" || plan.SubQueue != "" || plan.MaxMessages != 0 || plan.FromSequenceNumber != nil || len(plan.SequenceNumbers) != 0 || plan.ScheduledEnqueueTime != "" || plan.Settlement != "" || hasSettlementMetadata || sessionSelector || plan.SessionStateBase64 != nil {
			return fmt.Errorf("Azure Service Bus SendMessages requires 1..100 messages and only sender fields")
		}
		return requireTimeout()
	case "schedulemessages":
		if len(plan.Messages) < 1 || len(plan.Messages) > azureServiceBusMaxSendMessages || plan.ScheduledEnqueueTime == "" || plan.Subscription != "" || plan.SubQueue != "" || plan.MaxMessages != 0 || plan.FromSequenceNumber != nil || len(plan.SequenceNumbers) != 0 || plan.Settlement != "" || hasSettlementMetadata || sessionSelector || plan.SessionStateBase64 != nil {
			return fmt.Errorf("Azure Service Bus ScheduleMessages requires 1..100 messages and scheduled_enqueue_time")
		}
		parsed, err := time.Parse(time.RFC3339, plan.ScheduledEnqueueTime)
		if err != nil {
			return fmt.Errorf("Azure Service Bus scheduled_enqueue_time must be RFC3339")
		}
		plan.ScheduledEnqueueTimeValue = parsed
		return requireTimeout()
	case "cancelscheduledmessages":
		if len(plan.SequenceNumbers) < 1 || len(plan.SequenceNumbers) > azureServiceBusMaxSendMessages || len(plan.Messages) != 0 || plan.Subscription != "" || plan.SubQueue != "" || plan.MaxMessages != 0 || plan.FromSequenceNumber != nil || plan.ScheduledEnqueueTime != "" || plan.Settlement != "" || hasSettlementMetadata || sessionSelector || plan.SessionStateBase64 != nil {
			return fmt.Errorf("Azure Service Bus CancelScheduledMessages requires 1..100 sequence_numbers and only sender fields")
		}
		return requireTimeout()
	case "receivemessages", "receivedeferredmessages":
		if err := requireTimeout(); err != nil {
			return err
		}
		if plan.Operation == "receivemessages" && (plan.MaxMessages < 1 || plan.MaxMessages > azureServiceBusMaxReceiveMessages || len(plan.SequenceNumbers) != 0) {
			return fmt.Errorf("Azure Service Bus ReceiveMessages requires max_messages 1..100")
		}
		if plan.Operation == "receivedeferredmessages" && (len(plan.SequenceNumbers) < 1 || len(plan.SequenceNumbers) > azureServiceBusMaxReceiveMessages || plan.MaxMessages != 0) {
			return fmt.Errorf("Azure Service Bus ReceiveDeferredMessages requires 1..100 sequence_numbers")
		}
		switch strings.ToLower(plan.Settlement) {
		case "complete", "abandon", "defer", "dead_letter", "receive_and_delete":
		default:
			return fmt.Errorf("Azure Service Bus receive requires an explicit settlement: complete, abandon, defer, dead_letter, or receive_and_delete")
		}
		if plan.Settlement != "dead_letter" && (plan.DeadLetterReason != "" || plan.DeadLetterDescription != "") || plan.Settlement == "receive_and_delete" && len(plan.SettlementProperties) != 0 {
			return fmt.Errorf("Azure Service Bus settlement metadata does not match the selected settlement")
		}
		if len(plan.Messages) != 0 || plan.FromSequenceNumber != nil || plan.ScheduledEnqueueTime != "" || plan.SessionStateBase64 != nil {
			return fmt.Errorf("Azure Service Bus receive plan contains unrelated fields")
		}
	case "getsessionstate", "setsessionstate":
		if err := requireTimeout(); err != nil {
			return err
		}
		if !sessionSelector || plan.SubQueue != "" || len(plan.Messages) != 0 || len(plan.SequenceNumbers) != 0 || plan.MaxMessages != 0 || plan.FromSequenceNumber != nil || plan.ScheduledEnqueueTime != "" || plan.Settlement != "" || hasSettlementMetadata || plan.Operation == "getsessionstate" && plan.SessionStateBase64 != nil || plan.Operation == "setsessionstate" && plan.SessionStateBase64 == nil {
			return fmt.Errorf("Azure Service Bus session state operation requires one session selector and the matching state fields")
		}
	}
	return nil
}

func invokeAzureServiceBusAMQP(ctx context.Context, adapter *AzureRESTAdapter, invocation Invocation) (InvocationResult, error) {
	if err := validateAzureServiceBusAMQPInvocation(invocation); err != nil {
		return InvocationResult{}, err
	}
	fqdn, _ := parseAzureAMQPWebSocketTarget(invocation.URL)
	plan, _ := parseAzureServiceBusAMQPPlan(invocation.Operation, invocation.Body)
	records, requestID, err := adapter.config.ServiceBusAMQP.Execute(ctx, fqdn, plan)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("Azure Service Bus AMQP operation failed: %w", err)
	}
	return publishAzureAMQPNDJSON(invocation, "Azure Service Bus AMQP", records, requestID)
}

type azureSDKServiceBusAMQPExecutor struct {
	credential azureAMQPTokenCredential
	dial       azureAMQPWebSocketDial
}

type azureServiceBusReceiver interface {
	ReceiveMessages(context.Context, int, *azservicebus.ReceiveMessagesOptions) ([]*azservicebus.ReceivedMessage, error)
	ReceiveDeferredMessages(context.Context, []int64, *azservicebus.ReceiveDeferredMessagesOptions) ([]*azservicebus.ReceivedMessage, error)
	PeekMessages(context.Context, int, *azservicebus.PeekMessagesOptions) ([]*azservicebus.ReceivedMessage, error)
	CompleteMessage(context.Context, *azservicebus.ReceivedMessage, *azservicebus.CompleteMessageOptions) error
	AbandonMessage(context.Context, *azservicebus.ReceivedMessage, *azservicebus.AbandonMessageOptions) error
	DeferMessage(context.Context, *azservicebus.ReceivedMessage, *azservicebus.DeferMessageOptions) error
	DeadLetterMessage(context.Context, *azservicebus.ReceivedMessage, *azservicebus.DeadLetterOptions) error
	Close(context.Context) error
}

type azureServiceBusSessionLock interface {
	LockedUntil() time.Time
	RenewSessionLock(context.Context, *azservicebus.RenewSessionLockOptions) error
}

type azureServiceBusMessageLockRenewer interface {
	RenewMessageLock(context.Context, *azservicebus.ReceivedMessage, *azservicebus.RenewMessageLockOptions) error
}

func (executor *azureSDKServiceBusAMQPExecutor) Execute(ctx context.Context, fqdn string, plan azureServiceBusAMQPPlan) ([]map[string]any, string, error) {
	expectedTarget := "wss://" + fqdn + azureAMQPWebSocketPath
	client, err := azservicebus.NewClient(fqdn, executor.credential, &azservicebus.ClientOptions{
		ApplicationID: "cloud-skills-mcp",
		NewWebSocketConn: func(dialContext context.Context, args azservicebus.NewWebSocketConnArgs) (net.Conn, error) {
			if args.Host != expectedTarget {
				return nil, fmt.Errorf("Azure Service Bus SDK requested an unexpected WebSocket target")
			}
			return executor.dial(dialContext, args.Host)
		},
	})
	if err != nil {
		return nil, "", err
	}
	defer closeAzureServiceBusClient(client)
	operationContext := ctx
	if plan.TimeoutSeconds > 0 {
		var cancel context.CancelFunc
		operationContext, cancel = context.WithTimeout(ctx, time.Duration(plan.TimeoutSeconds)*time.Second)
		defer cancel()
	}
	switch plan.Operation {
	case "sendmessages", "schedulemessages", "cancelscheduledmessages":
		return executeAzureServiceBusSend(operationContext, client, plan)
	case "peekmessages", "receivemessages", "receivedeferredmessages":
		return executeAzureServiceBusReceive(operationContext, client, plan)
	case "getsessionstate", "setsessionstate":
		return executeAzureServiceBusSessionState(operationContext, client, plan)
	default:
		return nil, "", fmt.Errorf("unsupported Service Bus operation")
	}
}

func executeAzureServiceBusSend(ctx context.Context, client *azservicebus.Client, plan azureServiceBusAMQPPlan) ([]map[string]any, string, error) {
	entity := plan.Queue
	if entity == "" {
		entity = plan.Topic
	}
	sender, err := client.NewSender(entity, nil)
	if err != nil {
		return nil, "", err
	}
	defer closeAzureServiceBusSender(sender)
	messages := make([]*azservicebus.Message, len(plan.Messages))
	for index := range plan.Messages {
		messages[index] = toAzureServiceBusMessage(plan.Messages[index])
	}
	switch plan.Operation {
	case "sendmessages":
		for _, message := range messages {
			if err := sender.SendMessage(ctx, message, nil); err != nil {
				return nil, "", err
			}
		}
		return []map[string]any{{"type": "send_result", "sent": len(messages)}}, "", nil
	case "schedulemessages":
		sequenceNumbers, err := sender.ScheduleMessages(ctx, messages, plan.ScheduledEnqueueTimeValue, nil)
		if err != nil {
			return nil, "", err
		}
		return []map[string]any{{"type": "schedule_result", "scheduled": len(sequenceNumbers), "sequence_numbers": sequenceNumbers}}, "", nil
	case "cancelscheduledmessages":
		if err := sender.CancelScheduledMessages(ctx, plan.SequenceNumbers, nil); err != nil {
			return nil, "", err
		}
		return []map[string]any{{"type": "cancel_result", "cancelled": len(plan.SequenceNumbers)}}, "", nil
	}
	return nil, "", fmt.Errorf("unsupported sender operation")
}

func executeAzureServiceBusReceive(ctx context.Context, client *azservicebus.Client, plan azureServiceBusAMQPPlan) ([]map[string]any, string, error) {
	receiver, err := newAzureServiceBusReceiver(ctx, client, plan)
	if err != nil {
		return nil, "", err
	}
	defer closeAzureServiceBusReceiver(receiver)
	return executeAzureServiceBusWithSessionLock(ctx, receiver, func(operationContext context.Context) ([]map[string]any, string, error) {
		return receiveAndSettleAzureServiceBusMessages(operationContext, receiver, plan)
	})
}

func receiveAndSettleAzureServiceBusMessages(ctx context.Context, receiver azureServiceBusReceiver, plan azureServiceBusAMQPPlan) ([]map[string]any, string, error) {
	var messages []*azservicebus.ReceivedMessage
	var err error
	switch plan.Operation {
	case "peekmessages":
		messages, err = receiver.PeekMessages(ctx, plan.MaxMessages, &azservicebus.PeekMessagesOptions{FromSequenceNumber: plan.FromSequenceNumber})
	case "receivemessages":
		messages, err = receiver.ReceiveMessages(ctx, plan.MaxMessages, &azservicebus.ReceiveMessagesOptions{TimeAfterFirstMessage: 50 * time.Millisecond})
	case "receivedeferredmessages":
		messages, err = receiver.ReceiveDeferredMessages(ctx, plan.SequenceNumbers, nil)
	}
	if err != nil && len(messages) == 0 {
		if ctx.Err() != nil {
			return []map[string]any{}, "", nil
		}
		return nil, "", err
	}
	records := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		if plan.Operation != "peekmessages" && strings.ToLower(plan.Settlement) != "receive_and_delete" {
			if err := renewAzureServiceBusMessageLockIfNeeded(ctx, receiver, message, time.Now()); err != nil {
				return nil, "", err
			}
			if err := settleAzureServiceBusMessage(ctx, receiver, message, plan); err != nil {
				return nil, "", err
			}
		}
		record := azureServiceBusMessageRecord(message)
		if plan.Operation != "peekmessages" {
			record["settlement"] = strings.ToLower(plan.Settlement)
		}
		records = append(records, record)
	}
	return records, "", nil
}

func newAzureServiceBusReceiver(ctx context.Context, client *azservicebus.Client, plan azureServiceBusAMQPPlan) (azureServiceBusReceiver, error) {
	mode := azservicebus.ReceiveModePeekLock
	if strings.EqualFold(plan.Settlement, "receive_and_delete") {
		mode = azservicebus.ReceiveModeReceiveAndDelete
	}
	subQueue := azureServiceBusSubQueue(plan.SubQueue)
	if plan.SessionID != nil || plan.AcceptNextSession {
		options := &azservicebus.SessionReceiverOptions{ReceiveMode: mode}
		if plan.Queue != "" {
			if plan.AcceptNextSession {
				return client.AcceptNextSessionForQueue(ctx, plan.Queue, options)
			}
			return client.AcceptSessionForQueue(ctx, plan.Queue, *plan.SessionID, options)
		}
		if plan.AcceptNextSession {
			return client.AcceptNextSessionForSubscription(ctx, plan.Topic, plan.Subscription, options)
		}
		return client.AcceptSessionForSubscription(ctx, plan.Topic, plan.Subscription, *plan.SessionID, options)
	}
	options := &azservicebus.ReceiverOptions{ReceiveMode: mode, SubQueue: subQueue}
	if plan.Queue != "" {
		return client.NewReceiverForQueue(plan.Queue, options)
	}
	return client.NewReceiverForSubscription(plan.Topic, plan.Subscription, options)
}

func executeAzureServiceBusSessionState(ctx context.Context, client *azservicebus.Client, plan azureServiceBusAMQPPlan) ([]map[string]any, string, error) {
	receiver, err := newAzureServiceBusReceiver(ctx, client, plan)
	if err != nil {
		return nil, "", err
	}
	session, ok := receiver.(*azservicebus.SessionReceiver)
	if !ok {
		return nil, "", fmt.Errorf("Azure Service Bus session receiver unavailable")
	}
	defer closeAzureServiceBusReceiver(session)
	return executeAzureServiceBusWithSessionLock(ctx, session, func(operationContext context.Context) ([]map[string]any, string, error) {
		if plan.Operation == "getsessionstate" {
			state, err := session.GetSessionState(operationContext, nil)
			if err != nil {
				return nil, "", err
			}
			return []map[string]any{{"type": "session_state", "session_id": session.SessionID(), "state_base64": base64.StdEncoding.EncodeToString(state)}}, "", nil
		}
		if err := session.SetSessionState(operationContext, plan.SessionState, nil); err != nil {
			return nil, "", err
		}
		return []map[string]any{{"type": "set_session_state_result", "session_id": session.SessionID(), "bytes": len(plan.SessionState)}}, "", nil
	})
}

func executeAzureServiceBusWithSessionLock(ctx context.Context, receiver any, operation func(context.Context) ([]map[string]any, string, error)) ([]map[string]any, string, error) {
	session, ok := receiver.(azureServiceBusSessionLock)
	if !ok {
		return operation(ctx)
	}
	operationContext, cancelOperation := context.WithCancel(ctx)
	renewalContext, stopRenewal := context.WithCancel(ctx)
	renewalDone := make(chan error, 1)
	go func() {
		for {
			delay := azureServiceBusSessionRenewalDelay(session.LockedUntil(), time.Now())
			timer := time.NewTimer(delay)
			select {
			case <-renewalContext.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				renewalDone <- nil
				return
			case <-timer.C:
			}
			if err := session.RenewSessionLock(renewalContext, nil); err != nil {
				if renewalContext.Err() != nil {
					renewalDone <- nil
					return
				}
				cancelOperation()
				renewalDone <- fmt.Errorf("renew Azure Service Bus session lock: %w", err)
				return
			}
		}
	}()
	records, requestID, operationErr := operation(operationContext)
	stopRenewal()
	renewalErr := <-renewalDone
	cancelOperation()
	if renewalErr != nil {
		return nil, "", renewalErr
	}
	return records, requestID, operationErr
}

func renewAzureServiceBusMessageLockIfNeeded(ctx context.Context, receiver any, message *azservicebus.ReceivedMessage, now time.Time) error {
	renewer, ok := receiver.(azureServiceBusMessageLockRenewer)
	if !ok || message.LockedUntil == nil || message.LockedUntil.Sub(now) > 15*time.Second {
		return nil
	}
	if err := renewer.RenewMessageLock(ctx, message, nil); err != nil {
		return fmt.Errorf("renew Azure Service Bus message lock before settlement: %w", err)
	}
	return nil
}

func azureServiceBusSessionRenewalDelay(lockedUntil, now time.Time) time.Duration {
	remaining := lockedUntil.Sub(now)
	if remaining <= 0 {
		return 0
	}
	delay := remaining / 2
	if delay > 30*time.Second {
		return 30 * time.Second
	}
	return delay
}

func settleAzureServiceBusMessage(ctx context.Context, receiver azureServiceBusReceiver, message *azservicebus.ReceivedMessage, plan azureServiceBusAMQPPlan) error {
	switch strings.ToLower(plan.Settlement) {
	case "complete":
		return receiver.CompleteMessage(ctx, message, nil)
	case "abandon":
		return receiver.AbandonMessage(ctx, message, &azservicebus.AbandonMessageOptions{PropertiesToModify: plan.SettlementProperties})
	case "defer":
		return receiver.DeferMessage(ctx, message, &azservicebus.DeferMessageOptions{PropertiesToModify: plan.SettlementProperties})
	case "dead_letter":
		options := &azservicebus.DeadLetterOptions{PropertiesToModify: plan.SettlementProperties}
		if plan.DeadLetterReason != "" {
			options.Reason = &plan.DeadLetterReason
		}
		if plan.DeadLetterDescription != "" {
			options.ErrorDescription = &plan.DeadLetterDescription
		}
		return receiver.DeadLetterMessage(ctx, message, options)
	default:
		return fmt.Errorf("unsupported Service Bus settlement")
	}
}

func toAzureServiceBusMessage(input azureServiceBusAMQPMessage) *azservicebus.Message {
	message := &azservicebus.Message{Body: input.Body, ApplicationProperties: input.ApplicationProperties, SessionID: input.SessionID}
	setAzureString := func(value string, destination **string) {
		if value != "" {
			copy := value
			*destination = &copy
		}
	}
	setAzureString(input.ContentType, &message.ContentType)
	setAzureString(input.CorrelationID, &message.CorrelationID)
	setAzureString(input.MessageID, &message.MessageID)
	setAzureString(input.PartitionKey, &message.PartitionKey)
	setAzureString(input.ReplyTo, &message.ReplyTo)
	setAzureString(input.ReplyToSessionID, &message.ReplyToSessionID)
	setAzureString(input.Subject, &message.Subject)
	setAzureString(input.To, &message.To)
	if input.TimeToLiveSeconds > 0 {
		duration := time.Duration(input.TimeToLiveSeconds) * time.Second
		message.TimeToLive = &duration
	}
	return message
}

func azureServiceBusSubQueue(value string) azservicebus.SubQueue {
	switch strings.ToLower(value) {
	case "dead_letter":
		return azservicebus.SubQueueDeadLetter
	case "transfer_dead_letter":
		return azservicebus.SubQueueTransfer
	default:
		return azservicebus.SubQueue(0)
	}
}

func azureServiceBusMessageRecord(message *azservicebus.ReceivedMessage) map[string]any {
	record := map[string]any{
		"type": "message", "body_base64": base64.StdEncoding.EncodeToString(message.Body),
		"message_id": message.MessageID, "delivery_count": message.DeliveryCount,
		"state": azureServiceBusMessageState(message.State),
	}
	set := func(name string, value *string) {
		if value != nil {
			record[name] = *value
		}
	}
	set("content_type", message.ContentType)
	set("correlation_id", message.CorrelationID)
	set("partition_key", message.PartitionKey)
	set("reply_to", message.ReplyTo)
	set("reply_to_session_id", message.ReplyToSessionID)
	set("session_id", message.SessionID)
	set("subject", message.Subject)
	set("to", message.To)
	set("dead_letter_reason", message.DeadLetterReason)
	set("dead_letter_description", message.DeadLetterErrorDescription)
	set("dead_letter_source", message.DeadLetterSource)
	if message.SequenceNumber != nil {
		record["sequence_number"] = *message.SequenceNumber
	}
	if message.EnqueuedSequenceNumber != nil {
		record["enqueued_sequence_number"] = *message.EnqueuedSequenceNumber
	}
	if message.EnqueuedTime != nil {
		record["enqueued_time"] = message.EnqueuedTime.UTC().Format(time.RFC3339Nano)
	}
	if message.ExpiresAt != nil {
		record["expires_at"] = message.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	if message.ScheduledEnqueueTime != nil {
		record["scheduled_enqueue_time"] = message.ScheduledEnqueueTime.UTC().Format(time.RFC3339Nano)
	}
	if message.TimeToLive != nil {
		record["time_to_live_seconds"] = int64(*message.TimeToLive / time.Second)
	}
	if properties := azureAMQPSanitizeProperties(message.ApplicationProperties); len(properties) != 0 {
		record["application_properties"] = properties
	}
	return record
}

func azureServiceBusMessageState(state azservicebus.MessageState) string {
	switch state {
	case azservicebus.MessageStateDeferred:
		return "deferred"
	case azservicebus.MessageStateScheduled:
		return "scheduled"
	default:
		return "active"
	}
}

func closeAzureServiceBusClient(client *azservicebus.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = client.Close(ctx)
}

func closeAzureServiceBusSender(sender *azservicebus.Sender) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = sender.Close(ctx)
}

func closeAzureServiceBusReceiver(receiver azureServiceBusReceiver) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = receiver.Close(ctx)
}

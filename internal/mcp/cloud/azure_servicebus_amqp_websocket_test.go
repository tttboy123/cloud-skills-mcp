package cloud

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	azurepolicy "github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
	"github.com/coder/websocket"
)

func serviceBusTestInvocation(root string) Invocation {
	return Invocation{
		Provider: ProviderAzure, Mode: ModeRead, AuthScheme: "servicebus-amqp-ws",
		Service: "servicebus", Operation: "PeekMessages", Method: http.MethodGet,
		URL: "wss://orders.servicebus.windows.net/$servicebus/websocket",
		Body: map[string]any{
			"queue": "orders", "max_messages": 2, "timeout_seconds": 10,
		},
		ResponseFile: filepath.Join(root, "servicebus.ndjson"), MaxResponseFileBytes: 8192,
	}
}

func TestAzureServiceBusAMQPValidationAndPolicy(t *testing.T) {
	base := serviceBusTestInvocation(t.TempDir())
	if err := validateInvocation(base, []string{filepath.Dir(base.ResponseFile)}); err != nil {
		t.Fatalf("valid Service Bus AMQP invocation rejected: %v", err)
	}
	mutations := []Invocation{
		{Provider: ProviderAzure, Mode: ModeMutate, AuthScheme: authSchemeAzureServiceBusAMQPWS, Service: "servicebus", Operation: "SendMessages", Method: http.MethodGet, URL: base.URL, Body: map[string]any{"topic": "events", "messages": []any{map[string]any{"body_base64": "aGVsbG8=", "message_id": "m1"}}}, ResponseFile: filepath.Join(t.TempDir(), "send.ndjson")},
		{Provider: ProviderAzure, Mode: ModeMutate, AuthScheme: authSchemeAzureServiceBusAMQPWS, Service: "servicebus", Operation: "ReceiveMessages", Method: http.MethodGet, URL: base.URL, Body: map[string]any{"queue": "orders", "max_messages": 1, "timeout_seconds": 10, "settlement": "complete"}, ResponseFile: filepath.Join(t.TempDir(), "receive.ndjson")},
		{Provider: ProviderAzure, Mode: ModeMutate, AuthScheme: authSchemeAzureServiceBusAMQPWS, Service: "servicebus", Operation: "SetSessionState", Method: http.MethodGet, URL: base.URL, Body: map[string]any{"queue": "orders", "session_id": "customer-1", "session_state_base64": "c3RhdGU=", "timeout_seconds": 10}, ResponseFile: filepath.Join(t.TempDir(), "state.ndjson")},
	}
	for _, invocation := range mutations {
		if err := validateInvocation(invocation, []string{filepath.Dir(invocation.ResponseFile)}); err != nil {
			t.Errorf("valid %s rejected: %v", invocation.Operation, err)
		}
		if classifyRead(ProviderAzure, invocation) {
			t.Errorf("%s was classified as read-only", invocation.Operation)
		}
	}
	if !classifyRead(ProviderAzure, base) {
		t.Fatal("PeekMessages was not classified as read-only")
	}
}

func TestAzureServiceBusAMQPRejectsUnsafeOrUnboundedInputs(t *testing.T) {
	base := serviceBusTestInvocation(t.TempDir())
	tests := map[string]func(*Invocation){
		"wrong host":        func(v *Invocation) { v.URL = "wss://example.com/$servicebus/websocket" },
		"wrong path":        func(v *Invocation) { v.URL = "wss://orders.servicebus.windows.net/" },
		"query":             func(v *Invocation) { v.URL += "?sig=caller" },
		"caller headers":    func(v *Invocation) { v.Headers = map[string]string{"Authorization": "Bearer caller"} },
		"caller parameters": func(v *Invocation) { v.Parameters = map[string]any{"token": "caller"} },
		"body file":         func(v *Invocation) { v.BodyFile = "messages.json" },
		"missing output":    func(v *Invocation) { v.ResponseFile = "" },
		"unbounded count":   func(v *Invocation) { v.Body.(map[string]any)["max_messages"] = 251 },
		"unbounded timeout": func(v *Invocation) { v.Body.(map[string]any)["timeout_seconds"] = 301 },
		"credential field":  func(v *Invocation) { v.Body.(map[string]any)["access_token"] = "caller" },
		"wrong mode":        func(v *Invocation) { v.Mode = ModeMutate },
		"generic stream":    func(v *Invocation) { v.StreamIntervalMS = 1 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := base
			candidate.Headers = nil
			candidate.Parameters = nil
			candidate.Body = map[string]any{"queue": "orders", "max_messages": 2, "timeout_seconds": 10}
			mutate(&candidate)
			if err := validateAzureServiceBusAMQPInvocation(candidate); err == nil {
				t.Fatal("unsafe invocation accepted")
			}
		})
	}
}

func TestAzureServiceBusAMQPPlanCoversBrokerOperations(t *testing.T) {
	tests := []struct {
		operation string
		body      map[string]any
	}{
		{"PeekMessages", map[string]any{"topic": "events", "subscription": "analytics", "sub_queue": "dead_letter", "max_messages": 250, "from_sequence_number": 1, "timeout_seconds": 300}},
		{"SendMessages", map[string]any{"queue": "orders", "messages": []any{map[string]any{"body_base64": "AAE=", "content_type": "application/octet-stream", "message_id": "m1", "correlation_id": "c1", "subject": "created", "session_id": "customer-1", "partition_key": "customer-1", "time_to_live_seconds": 60, "application_properties": map[string]any{"attempt": 1, "ready": true}}}}},
		{"ScheduleMessages", map[string]any{"queue": "orders", "scheduled_enqueue_time": "2030-01-02T03:04:05Z", "messages": []any{map[string]any{"body_base64": "aGk="}}}},
		{"CancelScheduledMessages", map[string]any{"queue": "orders", "sequence_numbers": []any{1, 2}}},
		{"ReceiveMessages", map[string]any{"queue": "orders", "max_messages": 100, "timeout_seconds": 30, "settlement": "dead_letter", "dead_letter_reason": "invalid", "dead_letter_description": "schema mismatch"}},
		{"ReceiveDeferredMessages", map[string]any{"queue": "orders", "sequence_numbers": []any{42}, "timeout_seconds": 30, "settlement": "complete"}},
		{"GetSessionState", map[string]any{"queue": "orders", "session_id": "customer-1", "timeout_seconds": 30}},
		{"SetSessionState", map[string]any{"queue": "orders", "accept_next_session": true, "session_state_base64": "", "timeout_seconds": 30}},
	}
	for _, test := range tests {
		plan, err := parseAzureServiceBusAMQPPlan(test.operation, test.body)
		if err != nil {
			t.Errorf("%s rejected: %v", test.operation, err)
			continue
		}
		if plan.Queue == "" && plan.Topic == "" {
			t.Errorf("%s lost entity", test.operation)
		}
	}
}

type fakeAzureServiceBusAMQPExecutor struct {
	called  bool
	fqdn    string
	plan    azureServiceBusAMQPPlan
	records []map[string]any
	err     error
}

func (f *fakeAzureServiceBusAMQPExecutor) Execute(_ context.Context, fqdn string, plan azureServiceBusAMQPPlan) ([]map[string]any, string, error) {
	f.called, f.fqdn, f.plan = true, fqdn, plan
	return f.records, "tracking-1", f.err
}

func TestAzureServiceBusAMQPInvokePublishesAtomicSanitizedNDJSON(t *testing.T) {
	root := t.TempDir()
	invocation := serviceBusTestInvocation(root)
	fake := &fakeAzureServiceBusAMQPExecutor{records: []map[string]any{{
		"type": "message", "body_base64": base64.StdEncoding.EncodeToString([]byte("hello")),
		"message_id": "m1", "sequence_number": int64(7),
	}}}
	adapter := NewAzureRESTAdapter(AzureRESTConfig{})
	adapter.config.ServiceBusAMQP = fake
	result, err := adapter.Invoke(t.Context(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := os.ReadFile(invocation.ResponseFile)
	if readErr != nil || !bytes.Contains(data, []byte(`"body_base64":"aGVsbG8="`)) || bytes.Contains(data, []byte("token")) {
		t.Fatalf("output=%s read_err=%v", data, readErr)
	}
	if !fake.called || fake.fqdn != "orders.servicebus.windows.net" || result.RequestID != "tracking-1" || !bytes.Contains(result.Output, []byte(`"response_file"`)) {
		t.Fatalf("called=%t fqdn=%q result=%s", fake.called, fake.fqdn, result.Output)
	}
	info, err := os.Stat(invocation.ResponseFile)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v err=%v", info.Mode(), err)
	}
}

func TestAzureServiceBusAMQPFailureNeverPublishesPartialFile(t *testing.T) {
	root := t.TempDir()
	invocation := serviceBusTestInvocation(root)
	adapter := NewAzureRESTAdapter(AzureRESTConfig{})
	adapter.config.ServiceBusAMQP = &fakeAzureServiceBusAMQPExecutor{err: errors.New("broker failed")}
	if _, err := adapter.Invoke(t.Context(), invocation); err == nil {
		t.Fatal("executor failure accepted")
	}
	if _, err := os.Stat(invocation.ResponseFile); !os.IsNotExist(err) {
		t.Fatalf("partial output published: %v", err)
	}
}

func TestAzureAMQPTokenCredentialAllowsOnlyMessagingScopes(t *testing.T) {
	var scope string
	credential := azureAMQPTokenCredential{
		provider: azureTokenProviderFunc(func(_ context.Context, requested string) (string, error) {
			scope = requested
			return "private-entra-token", nil
		}),
		now: func() time.Time { return time.Unix(1_700_000_000, 0) },
	}
	token, err := credential.GetToken(t.Context(), azurepolicy.TokenRequestOptions{Scopes: []string{azureServiceBusTokenScope}})
	if err != nil || token.Token != "private-entra-token" || scope != azureServiceBusTokenScope || !token.ExpiresOn.Equal(time.Unix(1_700_000_000, 0).Add(20*time.Minute)) {
		t.Fatalf("token=%#v scope=%q err=%v", token, scope, err)
	}
	for _, scopes := range [][]string{nil, {"https://management.azure.com/.default"}, {azureServiceBusTokenScope, azureEventHubsTokenScope}} {
		if _, err := credential.GetToken(t.Context(), azurepolicy.TokenRequestOptions{Scopes: scopes}); err == nil {
			t.Errorf("scopes %q accepted", scopes)
		}
	}
	if _, err := credential.GetToken(t.Context(), azurepolicy.TokenRequestOptions{Scopes: []string{azureEventHubsTokenScope}}); err != nil {
		t.Fatalf("Event Hubs scope rejected: %v", err)
	}
	for name, provider := range map[string]AzureTokenProvider{
		"provider error": azureTokenProviderFunc(func(context.Context, string) (string, error) { return "", errors.New("identity unavailable") }),
		"empty token":    azureTokenProviderFunc(func(context.Context, string) (string, error) { return "  ", nil }),
	} {
		t.Run(name, func(t *testing.T) {
			candidate := azureAMQPTokenCredential{provider: provider}
			if _, err := candidate.GetToken(t.Context(), azurepolicy.TokenRequestOptions{Scopes: []string{azureServiceBusTokenScope}}); err == nil {
				t.Fatal("invalid identity response accepted")
			}
		})
	}
}

func TestAzureAMQPWebSocketDialUsesBinaryAMQPSubprotocol(t *testing.T) {
	negotiated := make(chan string, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(writer, request, &websocket.AcceptOptions{Subprotocols: []string{"amqp"}})
		if err != nil {
			return
		}
		negotiated <- connection.Subprotocol()
		_ = connection.Close(websocket.StatusNormalClosure, "done")
	}))
	defer server.Close()
	target := "wss" + strings.TrimPrefix(server.URL, "https")
	connection, err := dialAzureAMQPWebSocket(t.Context(), target, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_ = connection.Close()
	if got := <-negotiated; got != "amqp" {
		t.Fatalf("subprotocol=%q", got)
	}
}

func TestAzureAMQPWebSocketDialRejectsMissingProtocolAndHandshakeFailures(t *testing.T) {
	if _, err := dialAzureAMQPWebSocket(t.Context(), "wss://unused.example", nil); err == nil {
		t.Fatal("nil HTTP client accepted")
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(writer, request, nil)
		if err == nil {
			connection.CloseNow()
		}
	}))
	defer server.Close()
	target := "wss" + strings.TrimPrefix(server.URL, "https")
	if _, err := dialAzureAMQPWebSocket(t.Context(), target, server.Client()); err == nil || !strings.Contains(err.Error(), "did not negotiate") {
		t.Fatalf("missing subprotocol error=%v", err)
	}
	rejected := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "forbidden", http.StatusForbidden)
	}))
	defer rejected.Close()
	if _, err := dialAzureAMQPWebSocket(t.Context(), "wss"+strings.TrimPrefix(rejected.URL, "https"), rejected.Client()); err == nil || !strings.Contains(err.Error(), "handshake failed") {
		t.Fatalf("handshake error=%v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := defaultAzureAMQPWebSocketDial(ctx, "wss://orders.servicebus.windows.net/$servicebus/websocket"); err == nil {
		t.Fatal("cancelled default dial accepted")
	}
}

func TestAzureServiceBusSDKCanDialOnlyTheExactNamespaceWebSocketTarget(t *testing.T) {
	var target string
	ctx, cancel := context.WithCancel(t.Context())
	executor := &azureSDKServiceBusAMQPExecutor{
		credential: azureAMQPTokenCredential{provider: azureTokenProviderFunc(func(context.Context, string) (string, error) {
			return "unused", nil
		})},
		dial: func(_ context.Context, requested string) (net.Conn, error) {
			target = requested
			cancel()
			return nil, errors.New("intentional dial stop")
		},
	}
	plan, err := parseAzureServiceBusAMQPPlan("PeekMessages", map[string]any{"queue": "orders", "max_messages": 1, "timeout_seconds": 1})
	if err != nil {
		t.Fatal(err)
	}
	_, _, _ = executor.Execute(ctx, "orders.servicebus.windows.net", plan)
	if target != "wss://orders.servicebus.windows.net/$servicebus/websocket" {
		t.Fatalf("SDK dial target=%q", target)
	}
}

type fakeAzureServiceBusSessionLock struct {
	lockedUntil time.Time
	renewErr    error
	renewed     chan struct{}
}

type fakeAzureServiceBusMessageLockRenewer struct {
	called bool
	err    error
}

type fakeAzureServiceBusReceiver struct {
	settlement string
	messages   []*azservicebus.ReceivedMessage
	receiveErr error
	receivedBy string
}

func (receiver *fakeAzureServiceBusReceiver) ReceiveMessages(context.Context, int, *azservicebus.ReceiveMessagesOptions) ([]*azservicebus.ReceivedMessage, error) {
	receiver.receivedBy = "receive"
	return receiver.messages, receiver.receiveErr
}

func (receiver *fakeAzureServiceBusReceiver) ReceiveDeferredMessages(context.Context, []int64, *azservicebus.ReceiveDeferredMessagesOptions) ([]*azservicebus.ReceivedMessage, error) {
	receiver.receivedBy = "deferred"
	return receiver.messages, receiver.receiveErr
}

func (receiver *fakeAzureServiceBusReceiver) PeekMessages(context.Context, int, *azservicebus.PeekMessagesOptions) ([]*azservicebus.ReceivedMessage, error) {
	receiver.receivedBy = "peek"
	return receiver.messages, receiver.receiveErr
}

func (receiver *fakeAzureServiceBusReceiver) CompleteMessage(context.Context, *azservicebus.ReceivedMessage, *azservicebus.CompleteMessageOptions) error {
	receiver.settlement = "complete"
	return nil
}

func (receiver *fakeAzureServiceBusReceiver) AbandonMessage(context.Context, *azservicebus.ReceivedMessage, *azservicebus.AbandonMessageOptions) error {
	receiver.settlement = "abandon"
	return nil
}

func (receiver *fakeAzureServiceBusReceiver) DeferMessage(context.Context, *azservicebus.ReceivedMessage, *azservicebus.DeferMessageOptions) error {
	receiver.settlement = "defer"
	return nil
}

func (receiver *fakeAzureServiceBusReceiver) DeadLetterMessage(context.Context, *azservicebus.ReceivedMessage, *azservicebus.DeadLetterOptions) error {
	receiver.settlement = "dead_letter"
	return nil
}

func (*fakeAzureServiceBusReceiver) Close(context.Context) error { return nil }

func (renewer *fakeAzureServiceBusMessageLockRenewer) RenewMessageLock(context.Context, *azservicebus.ReceivedMessage, *azservicebus.RenewMessageLockOptions) error {
	renewer.called = true
	return renewer.err
}

func (session *fakeAzureServiceBusSessionLock) LockedUntil() time.Time { return session.lockedUntil }

func (session *fakeAzureServiceBusSessionLock) RenewSessionLock(context.Context, *azservicebus.RenewSessionLockOptions) error {
	close(session.renewed)
	return session.renewErr
}

func TestAzureServiceBusSessionLockRenewalIsBoundedAndFailsClosed(t *testing.T) {
	now := time.Now()
	if got := azureServiceBusSessionRenewalDelay(now.Add(2*time.Minute), now); got != 30*time.Second {
		t.Fatalf("long lock renewal delay=%s", got)
	}
	if got := azureServiceBusSessionRenewalDelay(now.Add(20*time.Second), now); got != 10*time.Second {
		t.Fatalf("short lock renewal delay=%s", got)
	}
	session := &fakeAzureServiceBusSessionLock{lockedUntil: now.Add(-time.Second), renewErr: errors.New("lock lost"), renewed: make(chan struct{})}
	_, _, err := executeAzureServiceBusWithSessionLock(t.Context(), session, func(ctx context.Context) ([]map[string]any, string, error) {
		<-ctx.Done()
		return nil, "", ctx.Err()
	})
	if err == nil || !strings.Contains(err.Error(), "renew Azure Service Bus session lock") {
		t.Fatalf("renewal failure=%v", err)
	}
	select {
	case <-session.renewed:
	default:
		t.Fatal("session lock was not renewed")
	}
}

func TestAzureServiceBusRenewsExpiringMessageLockBeforeSettlement(t *testing.T) {
	lockedUntil := time.Now().Add(10 * time.Second)
	message := &azservicebus.ReceivedMessage{LockedUntil: &lockedUntil}
	renewer := &fakeAzureServiceBusMessageLockRenewer{}
	if err := renewAzureServiceBusMessageLockIfNeeded(t.Context(), renewer, message, time.Now()); err != nil || !renewer.called {
		t.Fatalf("called=%t err=%v", renewer.called, err)
	}
	renewer = &fakeAzureServiceBusMessageLockRenewer{err: errors.New("lock lost")}
	if err := renewAzureServiceBusMessageLockIfNeeded(t.Context(), renewer, message, time.Now()); err == nil || !strings.Contains(err.Error(), "before settlement") {
		t.Fatalf("lock renewal failure=%v", err)
	}
}

func TestAzureServiceBusMessageConversionRecordAndSettlements(t *testing.T) {
	sessionID := "customer-1"
	input := azureServiceBusAMQPMessage{
		Body: []byte("hello"), ContentType: "text/plain", CorrelationID: "c1", MessageID: "m1",
		PartitionKey: sessionID, ReplyTo: "reply", ReplyToSessionID: "reply-session", SessionID: &sessionID,
		Subject: "created", TimeToLiveSeconds: 60, To: "processor", ApplicationProperties: map[string]any{"attempt": 1},
	}
	sent := toAzureServiceBusMessage(input)
	if string(sent.Body) != "hello" || sent.MessageID == nil || *sent.MessageID != "m1" || sent.TimeToLive == nil || *sent.TimeToLive != time.Minute {
		t.Fatalf("sent=%#v", sent)
	}
	now, later := time.Now().UTC(), time.Now().UTC().Add(time.Minute)
	sequence, enqueuedSequence, ttl := int64(7), int64(6), time.Minute
	state := azservicebus.MessageStateDeferred
	received := &azservicebus.ReceivedMessage{
		ApplicationProperties: map[string]any{"attempt": int64(1)}, Body: []byte("hello"),
		ContentType: sent.ContentType, CorrelationID: sent.CorrelationID, MessageID: "m1", PartitionKey: sent.PartitionKey,
		ReplyTo: sent.ReplyTo, ReplyToSessionID: sent.ReplyToSessionID, SessionID: sent.SessionID, Subject: sent.Subject, To: sent.To,
		DeadLetterReason: stringPointer("invalid"), DeadLetterErrorDescription: stringPointer("schema"), DeadLetterSource: stringPointer("orders"),
		DeliveryCount: 2, SequenceNumber: &sequence, EnqueuedSequenceNumber: &enqueuedSequence, EnqueuedTime: &now,
		ExpiresAt: &later, ScheduledEnqueueTime: &now, TimeToLive: &ttl, State: state,
	}
	record := azureServiceBusMessageRecord(received)
	if record["body_base64"] != "aGVsbG8=" || record["state"] != "deferred" || record["sequence_number"] != int64(7) {
		t.Fatalf("record=%#v", record)
	}
	if _, leaked := record["lock_token"]; leaked {
		t.Fatalf("lock token leaked: %#v", record)
	}
	for _, settlement := range []string{"complete", "abandon", "defer", "dead_letter"} {
		receiver := &fakeAzureServiceBusReceiver{}
		plan := azureServiceBusAMQPPlan{Settlement: settlement, SettlementProperties: map[string]any{"attempt": 2}, DeadLetterReason: "invalid", DeadLetterDescription: "schema"}
		if err := settleAzureServiceBusMessage(t.Context(), receiver, received, plan); err != nil || receiver.settlement != settlement {
			t.Fatalf("settlement=%s called=%s err=%v", settlement, receiver.settlement, err)
		}
	}
	if err := settleAzureServiceBusMessage(t.Context(), &fakeAzureServiceBusReceiver{}, received, azureServiceBusAMQPPlan{Settlement: "unknown"}); err == nil {
		t.Fatal("unknown settlement accepted")
	}
	if azureServiceBusMessageState(azservicebus.MessageStateScheduled) != "scheduled" || azureServiceBusMessageState(azservicebus.MessageStateActive) != "active" {
		t.Fatal("message states were not canonical")
	}
	if azureServiceBusSubQueue("dead_letter") != azservicebus.SubQueueDeadLetter || azureServiceBusSubQueue("transfer_dead_letter") != azservicebus.SubQueueTransfer {
		t.Fatal("subqueue mapping failed")
	}
}

func TestAzureServiceBusReceivePathsSettleInsideTheInvocation(t *testing.T) {
	for _, test := range []struct {
		operation, receivedBy, settlement string
		maxMessages                       int
		sequenceNumbers                   []int64
	}{
		{"peekmessages", "peek", "", 1, nil},
		{"receivemessages", "receive", "complete", 1, nil},
		{"receivedeferredmessages", "deferred", "defer", 0, []int64{7}},
	} {
		t.Run(test.operation, func(t *testing.T) {
			receiver := &fakeAzureServiceBusReceiver{messages: []*azservicebus.ReceivedMessage{{Body: []byte("hello"), MessageID: "m1"}}}
			plan := azureServiceBusAMQPPlan{Operation: test.operation, MaxMessages: test.maxMessages, SequenceNumbers: test.sequenceNumbers, Settlement: test.settlement}
			records, _, err := receiveAndSettleAzureServiceBusMessages(t.Context(), receiver, plan)
			if err != nil || len(records) != 1 || receiver.receivedBy != test.receivedBy || test.settlement != "" && receiver.settlement != test.settlement {
				t.Fatalf("records=%#v receiver=%#v err=%v", records, receiver, err)
			}
		})
	}
	receiver := &fakeAzureServiceBusReceiver{receiveErr: errors.New("broker failed")}
	if _, _, err := receiveAndSettleAzureServiceBusMessages(t.Context(), receiver, azureServiceBusAMQPPlan{Operation: "receivemessages", MaxMessages: 1}); err == nil {
		t.Fatal("broker receive failure accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	records, _, err := receiveAndSettleAzureServiceBusMessages(ctx, receiver, azureServiceBusAMQPPlan{Operation: "receivemessages", MaxMessages: 1})
	if err != nil || len(records) != 0 {
		t.Fatalf("cancelled finite receive records=%#v err=%v", records, err)
	}
}

func stringPointer(value string) *string { return &value }

func TestAzureServiceBusMessageValidationRejectsCapabilityAndOversizeFields(t *testing.T) {
	base := map[string]any{"queue": "orders", "messages": []any{map[string]any{"body_base64": "aGk="}}}
	for name, mutate := range map[string]func(map[string]any){
		"lock token": func(v map[string]any) { v["lock_token"] = "capability" },
		"too many": func(v map[string]any) {
			messages := make([]any, 101)
			for i := range messages {
				messages[i] = map[string]any{"body_base64": "aA=="}
			}
			v["messages"] = messages
		},
		"bad base64": func(v map[string]any) { v["messages"] = []any{map[string]any{"body_base64": "%%%"}} },
		"non scalar property": func(v map[string]any) {
			v["messages"] = []any{map[string]any{"body_base64": "aA==", "application_properties": map[string]any{"nested": map[string]any{"x": 1}}}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := map[string]any{}
			for k, v := range base {
				candidate[k] = v
			}
			mutate(candidate)
			if _, err := parseAzureServiceBusAMQPPlan("SendMessages", candidate); err == nil || strings.Contains(err.Error(), "capability") && name != "lock token" {
				t.Fatalf("invalid message accepted or wrong error: %v", err)
			}
		})
	}
}

func TestAzureServiceBusPlanRejectsInvalidEntitiesAndBrokerCombinations(t *testing.T) {
	tests := []struct {
		operation string
		body      map[string]any
	}{
		{"SendMessages", map[string]any{"queue": "orders", "topic": "events", "messages": []any{map[string]any{"body_base64": "aA=="}}}},
		{"ReceiveMessages", map[string]any{"topic": "events", "max_messages": 1, "timeout_seconds": 1, "settlement": "complete"}},
		{"PeekMessages", map[string]any{"queue": "orders", "sub_queue": "unknown", "max_messages": 1, "timeout_seconds": 1}},
		{"CancelScheduledMessages", map[string]any{"queue": "orders", "sequence_numbers": []any{0}}},
		{"ScheduleMessages", map[string]any{"queue": "orders", "scheduled_enqueue_time": "tomorrow", "messages": []any{map[string]any{"body_base64": "aA=="}}}},
		{"ReceiveMessages", map[string]any{"queue": "orders", "session_id": "a", "accept_next_session": true, "max_messages": 1, "timeout_seconds": 1, "settlement": "complete"}},
		{"ReceiveMessages", map[string]any{"queue": "orders", "session_id": "a", "sub_queue": "dead_letter", "max_messages": 1, "timeout_seconds": 1, "settlement": "complete"}},
	}
	for _, test := range tests {
		if _, err := parseAzureServiceBusAMQPPlan(test.operation, test.body); err == nil {
			t.Errorf("%s invalid plan accepted: %#v", test.operation, test.body)
		}
	}
	partition, session := "partition", "session"
	for name, message := range map[string]azureServiceBusAMQPMessage{
		"negative ttl":     {BodyBase64: "aA==", TimeToLiveSeconds: -1},
		"session mismatch": {BodyBase64: "aA==", PartitionKey: partition, SessionID: &session},
		"long metadata":    {BodyBase64: "aA==", MessageID: strings.Repeat("x", 129)},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateAzureServiceBusMessage(&message); err == nil {
				t.Fatal("invalid message accepted")
			}
		})
	}
}

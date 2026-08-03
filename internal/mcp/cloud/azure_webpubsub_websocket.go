package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
)

const (
	authSchemeAzureWebPubSubWS      = "webpubsub-ws"
	azureWebPubSubAPIVersion        = "2024-01-01"
	azureWebPubSubMaxMessages       = 256
	azureWebPubSubMaxTimeoutSeconds = 300
)

var azureWebPubSubHubPattern = regexp.MustCompile("^[A-Za-z][A-Za-z0-9_`,.\\[\\]]{0,127}$")

type azureWebPubSubWebSocketDial func(context.Context, string, http.Header) (cloudWebSocketConnection, error)

type azureWebPubSubPlan struct {
	Protocol       string            `json:"protocol,omitempty"`
	UserID         string            `json:"user_id,omitempty"`
	Roles          []string          `json:"roles,omitempty"`
	Groups         []string          `json:"groups,omitempty"`
	Messages       []json.RawMessage `json:"messages"`
	MaxMessages    int               `json:"max_messages"`
	TimeoutSeconds int               `json:"timeout_seconds"`

	encodedMessages [][]byte
	ackIDs          []uint64
}

func validateAzureWebPubSubInvocation(invocation Invocation) error {
	if !strings.EqualFold(invocation.Method, http.MethodGet) || !strings.EqualFold(invocation.Service, "webpubsub") || !strings.EqualFold(invocation.Operation, "ClientConnect") {
		return fmt.Errorf("Azure Web PubSub requires GET, service webpubsub, and operation ClientConnect")
	}
	if _, _, err := parseAzureWebPubSubTarget(invocation.URL); err != nil {
		return err
	}
	if len(invocation.Headers) != 0 || len(invocation.Parameters) != 0 {
		return fmt.Errorf("Azure Web PubSub does not accept caller headers or query parameters")
	}
	if invocation.Body == nil || invocation.BodyFile != "" || invocation.ResponseFile == "" {
		return fmt.Errorf("Azure Web PubSub requires a finite protocol body and response_file; body_file is forbidden")
	}
	if invocation.Region != "" || invocation.Project != "" || invocation.Subscription != "" || invocation.Audience != "" || invocation.APIVersion != "" || invocation.AuthVersion != "" || invocation.PayloadMode != "" || invocation.ChecksumAlgorithm != "" || invocation.StreamChunkBytes != 0 || invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return fmt.Errorf("Azure Web PubSub does not accept REST, cross-provider, checksum, or binary stream controls")
	}
	_, err := parseAzureWebPubSubPlan(invocation.Body)
	return err
}

func parseAzureWebPubSubTarget(rawURL string) (*url.URL, string, error) {
	target, err := url.Parse(rawURL)
	if err != nil || target.Scheme != "wss" || target.User != nil || target.Fragment != "" || target.RawQuery != "" || target.Port() != "" {
		return nil, "", fmt.Errorf("Azure Web PubSub requires an exact credential-free wss:// URL")
	}
	host := strings.ToLower(target.Hostname())
	const suffix = ".webpubsub.azure.com"
	resource := strings.TrimSuffix(host, suffix)
	if resource == host || !endpointLabelPattern.MatchString(resource) || len(resource) < 3 || len(resource) > 63 {
		return nil, "", fmt.Errorf("Azure Web PubSub requires a resource.webpubsub.azure.com host")
	}
	const prefix = "/client/hubs/"
	if !strings.HasPrefix(target.Path, prefix) {
		return nil, "", fmt.Errorf("Azure Web PubSub requires the official /client/hubs/{hub} path")
	}
	hub := strings.TrimPrefix(target.Path, prefix)
	if !azureWebPubSubHubPattern.MatchString(hub) {
		return nil, "", fmt.Errorf("Azure Web PubSub requires one valid hub path segment")
	}
	return target, hub, nil
}

func parseAzureWebPubSubPlan(body any) (azureWebPubSubPlan, error) {
	if body == nil || azureRealtimeContainsCredentialField(body) {
		return azureWebPubSubPlan{}, fmt.Errorf("Azure Web PubSub requires a credential-free protocol body")
	}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) == 0 || len(encoded) > maxRequestPayloadBytes {
		return azureWebPubSubPlan{}, fmt.Errorf("Azure Web PubSub body must be bounded JSON")
	}
	var plan azureWebPubSubPlan
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&plan) != nil || ensureJSONDecoderEOF(decoder) != nil {
		return azureWebPubSubPlan{}, fmt.Errorf("Azure Web PubSub body does not match the finite session schema")
	}
	if plan.UserID != "" && !azureWebPubSubBoundedValue(plan.UserID, 128) {
		return azureWebPubSubPlan{}, fmt.Errorf("Azure Web PubSub user_id is invalid")
	}
	if plan.Protocol == "" {
		plan.Protocol = "json"
	}
	if plan.Protocol != "json" && plan.Protocol != "json-reliable" && plan.Protocol != "protobuf" && plan.Protocol != "protobuf-reliable" {
		return azureWebPubSubPlan{}, fmt.Errorf("Azure Web PubSub protocol must be json, json-reliable, protobuf, or protobuf-reliable")
	}
	if len(plan.Roles) > 32 || len(plan.Groups) > 32 {
		return azureWebPubSubPlan{}, fmt.Errorf("Azure Web PubSub token claims exceed the bounded count")
	}
	for _, role := range plan.Roles {
		if !azureWebPubSubRole(role) {
			return azureWebPubSubPlan{}, fmt.Errorf("Azure Web PubSub role is not a documented client permission")
		}
	}
	for _, group := range plan.Groups {
		if !azureWebPubSubBoundedValue(group, 256) {
			return azureWebPubSubPlan{}, fmt.Errorf("Azure Web PubSub group claim is invalid")
		}
	}
	if len(plan.Messages) < 1 || len(plan.Messages) > azureWebPubSubMaxMessages {
		return azureWebPubSubPlan{}, fmt.Errorf("Azure Web PubSub messages must contain 1 to %d frames", azureWebPubSubMaxMessages)
	}
	if plan.MaxMessages < 1 || plan.MaxMessages > azureWebPubSubMaxMessages || plan.TimeoutSeconds < 1 || plan.TimeoutSeconds > azureWebPubSubMaxTimeoutSeconds {
		return azureWebPubSubPlan{}, fmt.Errorf("Azure Web PubSub response count or timeout is outside the finite bound")
	}
	if azureWebPubSubIsReliable(plan.Protocol) && plan.MaxMessages < 2 {
		return azureWebPubSubPlan{}, fmt.Errorf("Azure reliable Web PubSub max_messages must allow connected plus protocol output")
	}
	plan.encodedMessages = make([][]byte, len(plan.Messages))
	plan.ackIDs = make([]uint64, len(plan.Messages))
	ackIDs := make(map[uint64]bool)
	for index, raw := range plan.Messages {
		var canonical []byte
		var ackID uint64
		var err error
		if azureWebPubSubIsProtobuf(plan.Protocol) {
			canonical, ackID, err = encodeAzureWebPubSubProtobufClientMessage(raw)
		} else {
			canonical, ackID, err = validateAzureWebPubSubClientMessage(raw)
		}
		if err != nil {
			return azureWebPubSubPlan{}, fmt.Errorf("Azure Web PubSub message %d: %w", index, err)
		}
		if ackID != 0 {
			if ackIDs[ackID] {
				return azureWebPubSubPlan{}, fmt.Errorf("Azure Web PubSub ackId values must be unique")
			}
			ackIDs[ackID] = true
		}
		if azureWebPubSubIsReliable(plan.Protocol) && ackID == 0 {
			var message map[string]any
			_ = json.Unmarshal(raw, &message)
			if azureWebPubSubReliableMessageRequiresAck(message) {
				return azureWebPubSubPlan{}, fmt.Errorf("Azure reliable Web PubSub publisher messages require ackId")
			}
		}
		plan.encodedMessages[index] = canonical
		plan.ackIDs[index] = ackID
	}
	return plan, nil
}

func azureWebPubSubIsProtobuf(protocol string) bool {
	return protocol == "protobuf" || protocol == "protobuf-reliable"
}

func azureWebPubSubIsReliable(protocol string) bool {
	return protocol == "json-reliable" || protocol == "protobuf-reliable"
}

func azureWebPubSubReliableMessageRequiresAck(message map[string]any) bool {
	kind, _ := message["type"].(string)
	if kind == "ping" || kind == "streamData" || kind == "streamEnd" {
		return false
	}
	if kind == "sendToGroup" {
		_, stream := message["stream"]
		return !stream
	}
	return true
}

func validateAzureWebPubSubClientMessage(raw json.RawMessage) ([]byte, uint64, error) {
	if len(raw) == 0 || len(raw) > maxRequestPayloadBytes || !json.Valid(raw) {
		return nil, 0, fmt.Errorf("client message must be bounded JSON")
	}
	var message map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&message) != nil || ensureJSONDecoderEOF(decoder) != nil || azureRealtimeContainsCredentialField(message) {
		return nil, 0, fmt.Errorf("client message is invalid or contains credentials")
	}
	kind, ok := message["type"].(string)
	if !ok {
		return nil, 0, fmt.Errorf("client message requires type")
	}
	switch kind {
	case "joinGroup", "leaveGroup", "sendToGroup":
		group, ok := message["group"].(string)
		if !ok || !azureWebPubSubBoundedValue(group, 256) {
			return nil, 0, fmt.Errorf("group message requires a bounded group")
		}
	case "event":
		event, ok := message["event"].(string)
		if !ok || !azureWebPubSubBoundedValue(event, 128) {
			return nil, 0, fmt.Errorf("event message requires a bounded event")
		}
	case "ping":
	default:
		return nil, 0, fmt.Errorf("unsupported client message type")
	}
	var ackID uint64
	if rawAck, exists := message["ackId"]; exists {
		value, ok := rawAck.(json.Number)
		parsed, err := strconv.ParseUint(string(value), 10, 64)
		if !ok || err != nil || parsed == 0 {
			return nil, 0, fmt.Errorf("ackId must be a positive unique integer")
		}
		ackID = parsed
	}
	canonical, err := json.Marshal(message)
	if err != nil {
		return nil, 0, fmt.Errorf("encode client message")
	}
	return canonical, ackID, nil
}

func azureWebPubSubBoundedValue(value string, maximum int) bool {
	if value == "" || len(value) > maximum || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character == 0 || character == '\r' || character == '\n' {
			return false
		}
	}
	return true
}

func azureWebPubSubRole(role string) bool {
	if !azureWebPubSubBoundedValue(role, 512) {
		return false
	}
	for _, base := range []string{"webpubsub.joinLeaveGroup", "webpubsub.sendToGroup"} {
		if role == base || strings.HasPrefix(role, base+".") && azureWebPubSubBoundedValue(strings.TrimPrefix(role, base+"."), 256) {
			return true
		}
	}
	for _, base := range []string{"webpubsub.joinLeaveGroups.", "webpubsub.sendToGroups."} {
		if strings.HasPrefix(role, base) && azureWebPubSubBoundedValue(strings.TrimPrefix(role, base), 256) {
			return true
		}
	}
	return false
}

func invokeAzureWebPubSub(ctx context.Context, adapter *AzureRESTAdapter, invocation Invocation) (InvocationResult, error) {
	if err := validateAzureWebPubSubInvocation(invocation); err != nil {
		return InvocationResult{}, err
	}
	target, hub, _ := parseAzureWebPubSubTarget(invocation.URL)
	plan, _ := parseAzureWebPubSubPlan(invocation.Body)
	identityToken, err := adapter.config.Tokens.Token(ctx, "https://webpubsub.azure.com/.default")
	if err != nil {
		return InvocationResult{}, fmt.Errorf("load Azure Web PubSub identity token: %w", err)
	}
	if strings.TrimSpace(identityToken) == "" {
		return InvocationResult{}, fmt.Errorf("Azure identity returned an empty access token")
	}
	clientToken, err := mintAzureWebPubSubClientToken(ctx, adapter, target, hub, plan, identityToken)
	if err != nil {
		return InvocationResult{}, err
	}
	if azureWebPubSubIsReliable(plan.Protocol) {
		return invokeAzureReliableWebPubSub(ctx, adapter, invocation, plan, clientToken)
	}
	sessionCtx, cancel := context.WithTimeout(ctx, time.Duration(plan.TimeoutSeconds)*time.Second)
	defer cancel()
	dial := adapter.config.WebPubSubWebSocketDial
	frameType := cloudWebSocketMessageText
	if azureWebPubSubIsProtobuf(plan.Protocol) {
		dial = adapter.config.ProtobufWebPubSubWebSocketDial
		frameType = cloudWebSocketMessageBinary
	}
	connection, err := dial(sessionCtx, invocation.URL, http.Header{"Authorization": []string{"Bearer " + clientToken}})
	if err != nil {
		return InvocationResult{}, err
	}
	defer connection.Close()
	sink, err := newWebSocketOutputSink(invocation, adapter.config.MaxBodyBytes, "Azure Web PubSub WebSocket")
	if err != nil {
		return InvocationResult{}, err
	}
	defer sink.abort()
	for index, message := range plan.encodedMessages {
		if index > 0 && invocation.StreamIntervalMS > 0 {
			if err := adapter.config.StreamPause(sessionCtx, time.Duration(invocation.StreamIntervalMS)*time.Millisecond); err != nil {
				return InvocationResult{}, fmt.Errorf("pace Azure Web PubSub client messages")
			}
		}
		if err := connection.Write(sessionCtx, frameType, message); err != nil {
			return InvocationResult{}, fmt.Errorf("send Azure Web PubSub client message")
		}
	}
	received := 0
	for received < plan.MaxMessages {
		var message []byte
		var terminal bool
		if azureWebPubSubIsProtobuf(plan.Protocol) {
			message, terminal, err = readAzureWebPubSubProtobufServerMessage(sessionCtx, connection)
		} else {
			message, terminal, err = readAzureWebPubSubServerMessage(sessionCtx, connection)
		}
		if errors.Is(err, context.DeadlineExceeded) || err == io.EOF || websocket.CloseStatus(err) == websocket.StatusNormalClosure {
			break
		}
		if err != nil {
			return InvocationResult{}, err
		}
		if err := sink.writeMessage(message); err != nil {
			return InvocationResult{}, err
		}
		received++
		if terminal {
			break
		}
	}
	metadata, err := sink.finish("")
	if err != nil {
		return InvocationResult{}, err
	}
	var summary map[string]any
	if json.Unmarshal(metadata, &summary) != nil {
		return InvocationResult{}, fmt.Errorf("decode Azure Web PubSub output metadata")
	}
	summary["messages"] = received
	metadata, err = json.Marshal(summary)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("encode Azure Web PubSub output metadata")
	}
	return InvocationResult{Output: metadata}, nil
}

func readAzureWebPubSubProtobufServerMessage(ctx context.Context, connection cloudWebSocketConnection) ([]byte, bool, error) {
	messageType, data, err := connection.Read(ctx)
	if err != nil {
		return nil, false, err
	}
	message, _, _, _, terminal, err := parseAzureWebPubSubProtobufServerMessage(messageType, data, false)
	if err != nil {
		return nil, false, err
	}
	var sanitized map[string]any
	decoder := json.NewDecoder(bytes.NewReader(message))
	decoder.UseNumber()
	if decoder.Decode(&sanitized) != nil || ensureJSONDecoderEOF(decoder) != nil {
		return nil, false, fmt.Errorf("sanitize Azure Web PubSub protobuf response")
	}
	delete(sanitized, "reconnectionToken")
	message, err = json.Marshal(sanitized)
	if err != nil {
		return nil, false, fmt.Errorf("sanitize Azure Web PubSub protobuf response")
	}
	return message, terminal, nil
}

func mintAzureWebPubSubClientToken(ctx context.Context, adapter *AzureRESTAdapter, target *url.URL, hub string, plan azureWebPubSubPlan, identityToken string) (string, error) {
	tokenURL := &url.URL{Scheme: "https", Host: target.Host, Path: "/api/hubs/" + hub + "/:generateToken"}
	query := tokenURL.Query()
	query.Set("api-version", azureWebPubSubAPIVersion)
	query.Set("minutesToExpire", "5")
	if plan.UserID != "" {
		query.Set("userId", plan.UserID)
	}
	for _, role := range plan.Roles {
		query.Add("role", role)
	}
	for _, group := range plan.Groups {
		query.Add("group", group)
	}
	tokenURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL.String(), nil)
	if err != nil {
		return "", fmt.Errorf("build Azure Web PubSub client-token request")
	}
	request.Header.Set("Authorization", "Bearer "+identityToken)
	response, err := adapter.config.HTTP.Do(request)
	if err != nil {
		return "", fmt.Errorf("Azure Web PubSub client-token request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Azure Web PubSub client-token request failed with HTTP %d", response.StatusCode)
	}
	limited := io.LimitReader(response.Body, maxRequestPayloadBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil || len(data) > maxRequestPayloadBytes {
		return "", fmt.Errorf("read Azure Web PubSub client-token response")
	}
	var payload struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(data, &payload) != nil || !azureWebPubSubBoundedValue(payload.Token, 16384) {
		return "", fmt.Errorf("Azure Web PubSub returned an invalid client-token response")
	}
	return payload.Token, nil
}

func readAzureWebPubSubServerMessage(ctx context.Context, connection cloudWebSocketConnection) ([]byte, bool, error) {
	messageType, data, err := connection.Read(ctx)
	if err != nil {
		return nil, false, err
	}
	if messageType != cloudWebSocketMessageText || len(data) == 0 || len(data) > maxRequestPayloadBytes || !utf8.Valid(data) || !json.Valid(data) {
		return nil, false, fmt.Errorf("Azure Web PubSub returned an invalid JSON text message")
	}
	var message map[string]any
	if json.Unmarshal(data, &message) != nil {
		return nil, false, fmt.Errorf("Azure Web PubSub returned invalid JSON")
	}
	kind, ok := message["type"].(string)
	if !ok {
		return nil, false, fmt.Errorf("Azure Web PubSub response has no type")
	}
	switch kind {
	case "ack":
		if success, ok := message["success"].(bool); !ok || !success {
			return nil, false, fmt.Errorf("Azure Web PubSub returned a failed acknowledgement")
		}
	case "streamNack":
		return nil, false, fmt.Errorf("Azure Web PubSub returned a stream rejection")
	case "streamClosed":
		if _, failed := message["error"]; failed {
			return nil, false, fmt.Errorf("Azure Web PubSub stream closed with an error")
		}
	case "message", "pong", "streamAck":
	case "system":
		event, _ := message["event"].(string)
		if event != "connected" && event != "disconnected" {
			return nil, false, fmt.Errorf("Azure Web PubSub returned an unsupported system event")
		}
	default:
		return nil, false, fmt.Errorf("Azure Web PubSub returned an unsupported protocol message")
	}
	canonical, err := json.Marshal(message)
	if err != nil {
		return nil, false, fmt.Errorf("encode Azure Web PubSub response")
	}
	return canonical, kind == "system" && message["event"] == "disconnected", nil
}

func invokeAzureReliableWebPubSub(ctx context.Context, adapter *AzureRESTAdapter, invocation Invocation, plan azureWebPubSubPlan, clientToken string) (InvocationResult, error) {
	sessionCtx, cancel := context.WithTimeout(ctx, time.Duration(plan.TimeoutSeconds)*time.Second)
	defer cancel()
	protobufProtocol := azureWebPubSubIsProtobuf(plan.Protocol)
	dial := adapter.config.ReliableWebPubSubWebSocketDial
	frameType := cloudWebSocketMessageText
	if protobufProtocol {
		dial = adapter.config.ReliableProtobufWebPubSubWebSocketDial
		frameType = cloudWebSocketMessageBinary
	}
	sink, err := newWebSocketOutputSink(invocation, adapter.config.MaxBodyBytes, "Azure reliable Web PubSub WebSocket")
	if err != nil {
		return InvocationResult{}, err
	}
	defer sink.abort()
	pending := make(map[uint64][]byte)
	for index, encoded := range plan.encodedMessages {
		ackID := plan.ackIDs[index]
		if ackID != 0 {
			pending[ackID] = encoded
		}
	}
	baseTarget, _ := url.Parse(invocation.URL)
	target := invocation.URL
	headers := http.Header{"Authorization": []string{"Bearer " + clientToken}}
	firstConnection := true
	received := 0
	var connectionID, reconnectionToken string
	var lastSequence uint64
	var recoveryDeadline time.Time

	for received < plan.MaxMessages {
		connection, err := dial(sessionCtx, target, headers)
		if err != nil {
			if websocket.CloseStatus(err) == websocket.StatusPolicyViolation {
				return InvocationResult{}, fmt.Errorf("Azure reliable Web PubSub recovery state expired")
			}
			if recoveryDeadline.IsZero() || time.Now().After(recoveryDeadline) {
				return InvocationResult{}, fmt.Errorf("recover Azure reliable Web PubSub connection")
			}
			if err := adapter.config.StreamPause(sessionCtx, 500*time.Millisecond); err != nil {
				return InvocationResult{}, fmt.Errorf("recover Azure reliable Web PubSub connection")
			}
			continue
		}
		var connected azureReliableConnected
		if protobufProtocol {
			connected, err = readAzureReliableProtobufConnected(sessionCtx, connection)
		} else {
			connected, err = readAzureReliableConnected(sessionCtx, connection)
		}
		if err != nil {
			connection.Close()
			var readErr *azureReliableConnectedReadError
			if !recoveryDeadline.IsZero() && errors.As(err, &readErr) {
				if websocket.CloseStatus(readErr) == websocket.StatusPolicyViolation {
					return InvocationResult{}, fmt.Errorf("Azure reliable Web PubSub recovery state expired")
				}
				if time.Now().Before(recoveryDeadline) {
					if pauseErr := adapter.config.StreamPause(sessionCtx, 500*time.Millisecond); pauseErr == nil {
						continue
					}
				}
				return InvocationResult{}, fmt.Errorf("recover Azure reliable Web PubSub connection")
			}
			return InvocationResult{}, err
		}
		if connectionID != "" && connected.ConnectionID != connectionID {
			connection.Close()
			return InvocationResult{}, fmt.Errorf("Azure reliable Web PubSub recovered a different connection")
		}
		connectionID, reconnectionToken = connected.ConnectionID, connected.ReconnectionToken
		recoveryDeadline = time.Time{}
		if err := sink.writeMessage(connected.Sanitized); err != nil {
			connection.Close()
			return InvocationResult{}, err
		}
		received++
		if received >= plan.MaxMessages {
			connection.Close()
			break
		}
		if firstConnection {
			for index, message := range plan.encodedMessages {
				if index > 0 && invocation.StreamIntervalMS > 0 {
					if err := adapter.config.StreamPause(sessionCtx, time.Duration(invocation.StreamIntervalMS)*time.Millisecond); err != nil {
						connection.Close()
						return InvocationResult{}, fmt.Errorf("pace Azure reliable Web PubSub client messages")
					}
				}
				if err := connection.Write(sessionCtx, frameType, message); err != nil {
					connection.Close()
					return InvocationResult{}, fmt.Errorf("send Azure reliable Web PubSub client message")
				}
			}
			firstConnection = false
		} else {
			for index, message := range plan.encodedMessages {
				ackID := plan.ackIDs[index]
				if _, stillPending := pending[ackID]; !stillPending {
					continue
				}
				if err := connection.Write(sessionCtx, frameType, message); err != nil {
					connection.Close()
					return InvocationResult{}, fmt.Errorf("resend Azure reliable Web PubSub pending message")
				}
			}
		}

		recoverConnection := false
		for received < plan.MaxMessages {
			messageType, data, readErr := connection.Read(sessionCtx)
			if errors.Is(readErr, context.DeadlineExceeded) {
				connection.Close()
				return finishAzureReliableWebPubSub(sink, received)
			}
			if readErr != nil {
				if websocket.CloseStatus(readErr) == websocket.StatusNormalClosure {
					connection.Close()
					return finishAzureReliableWebPubSub(sink, received)
				}
				if websocket.CloseStatus(readErr) == websocket.StatusPolicyViolation {
					connection.Close()
					return InvocationResult{}, fmt.Errorf("Azure reliable Web PubSub recovery state expired")
				}
				if connectionID == "" || reconnectionToken == "" {
					connection.Close()
					return InvocationResult{}, fmt.Errorf("read Azure reliable Web PubSub response")
				}
				recoverConnection = true
				recoveryDeadline = time.Now().Add(time.Minute)
				break
			}
			var message []byte
			var kind string
			var sequenceID, ackID uint64
			var terminal bool
			if protobufProtocol {
				message, kind, sequenceID, ackID, terminal, err = parseAzureWebPubSubProtobufServerMessage(messageType, data, true)
			} else {
				message, kind, sequenceID, ackID, terminal, err = parseAzureReliableServerMessage(messageType, data)
			}
			if err != nil {
				connection.Close()
				return InvocationResult{}, err
			}
			if protobufProtocol && kind == "system" {
				var system map[string]any
				if json.Unmarshal(message, &system) != nil || system["event"] != "disconnected" {
					connection.Close()
					return InvocationResult{}, fmt.Errorf("Azure reliable Web PubSub returned an unsupported system event")
				}
			}
			if kind == "ack" {
				delete(pending, ackID)
			}
			if kind == "message" {
				if sequenceID <= lastSequence {
					if err := writeAzureReliableSequenceAckForProtocol(sessionCtx, connection, lastSequence, protobufProtocol); err != nil {
						connection.Close()
						return InvocationResult{}, err
					}
					continue
				}
				lastSequence = sequenceID
			}
			if err := sink.writeMessage(message); err != nil {
				connection.Close()
				return InvocationResult{}, err
			}
			received++
			if kind == "message" {
				if err := writeAzureReliableSequenceAckForProtocol(sessionCtx, connection, lastSequence, protobufProtocol); err != nil {
					connection.Close()
					return InvocationResult{}, err
				}
			}
			if terminal {
				connection.Close()
				return finishAzureReliableWebPubSub(sink, received)
			}
		}
		connection.Close()
		if !recoverConnection || received >= plan.MaxMessages {
			break
		}
		recovery := *baseTarget
		query := recovery.Query()
		query.Set("awps_connection_id", connectionID)
		query.Set("awps_reconnection_token", reconnectionToken)
		recovery.RawQuery = query.Encode()
		target = recovery.String()
		headers = make(http.Header)
	}
	return finishAzureReliableWebPubSub(sink, received)
}

type azureReliableConnected struct {
	ConnectionID      string
	ReconnectionToken string
	Sanitized         []byte
}

type azureReliableConnectedReadError struct {
	cause error
}

func (*azureReliableConnectedReadError) Error() string {
	return "Azure reliable Web PubSub did not return connected"
}

func (failure *azureReliableConnectedReadError) Unwrap() error {
	return failure.cause
}

func readAzureReliableConnected(ctx context.Context, connection cloudWebSocketConnection) (azureReliableConnected, error) {
	messageType, data, err := connection.Read(ctx)
	if err != nil {
		return azureReliableConnected{}, &azureReliableConnectedReadError{cause: err}
	}
	if messageType != cloudWebSocketMessageText || !json.Valid(data) {
		return azureReliableConnected{}, fmt.Errorf("Azure reliable Web PubSub did not return connected")
	}
	var message map[string]any
	if json.Unmarshal(data, &message) != nil || message["type"] != "system" || message["event"] != "connected" {
		return azureReliableConnected{}, fmt.Errorf("Azure reliable Web PubSub did not return connected")
	}
	connectionID, idOK := message["connectionId"].(string)
	reconnectionToken, tokenOK := message["reconnectionToken"].(string)
	if !idOK || !tokenOK || !azureWebPubSubBoundedValue(connectionID, 512) || !azureWebPubSubBoundedValue(reconnectionToken, 16384) {
		return azureReliableConnected{}, fmt.Errorf("Azure reliable Web PubSub returned invalid recovery state")
	}
	delete(message, "reconnectionToken")
	sanitized, err := json.Marshal(message)
	if err != nil {
		return azureReliableConnected{}, fmt.Errorf("sanitize Azure reliable Web PubSub connected event")
	}
	return azureReliableConnected{ConnectionID: connectionID, ReconnectionToken: reconnectionToken, Sanitized: sanitized}, nil
}

func readAzureReliableProtobufConnected(ctx context.Context, connection cloudWebSocketConnection) (azureReliableConnected, error) {
	messageType, data, err := connection.Read(ctx)
	if err != nil {
		return azureReliableConnected{}, &azureReliableConnectedReadError{cause: err}
	}
	canonical, kind, _, _, _, err := parseAzureWebPubSubProtobufServerMessage(messageType, data, true)
	if err != nil || kind != "system" {
		return azureReliableConnected{}, fmt.Errorf("Azure reliable Web PubSub did not return connected")
	}
	var message map[string]any
	if json.Unmarshal(canonical, &message) != nil || message["event"] != "connected" {
		return azureReliableConnected{}, fmt.Errorf("Azure reliable Web PubSub did not return connected")
	}
	connectionID, idOK := message["connectionId"].(string)
	reconnectionToken, tokenOK := message["reconnectionToken"].(string)
	if !idOK || !tokenOK || !azureWebPubSubBoundedValue(connectionID, 512) || !azureWebPubSubBoundedValue(reconnectionToken, 16384) {
		return azureReliableConnected{}, fmt.Errorf("Azure reliable Web PubSub returned invalid recovery state")
	}
	delete(message, "reconnectionToken")
	sanitized, err := json.Marshal(message)
	if err != nil {
		return azureReliableConnected{}, fmt.Errorf("sanitize Azure reliable Web PubSub connected event")
	}
	return azureReliableConnected{ConnectionID: connectionID, ReconnectionToken: reconnectionToken, Sanitized: sanitized}, nil
}

func parseAzureReliableServerMessage(messageType cloudWebSocketMessageType, data []byte) ([]byte, string, uint64, uint64, bool, error) {
	if messageType != cloudWebSocketMessageText || len(data) == 0 || len(data) > maxRequestPayloadBytes || !utf8.Valid(data) || !json.Valid(data) {
		return nil, "", 0, 0, false, fmt.Errorf("Azure reliable Web PubSub returned invalid JSON")
	}
	var message map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if decoder.Decode(&message) != nil || ensureJSONDecoderEOF(decoder) != nil {
		return nil, "", 0, 0, false, fmt.Errorf("Azure reliable Web PubSub returned invalid JSON")
	}
	kind, ok := message["type"].(string)
	if !ok || kind == "system" && message["event"] == "connected" {
		return nil, "", 0, 0, false, fmt.Errorf("Azure reliable Web PubSub returned an invalid protocol message")
	}
	var sequenceID uint64
	var ackID uint64
	terminal := false
	switch kind {
	case "ack":
		value, valueOK := message["ackId"].(json.Number)
		parsed, parseErr := strconv.ParseUint(string(value), 10, 64)
		success, successOK := message["success"].(bool)
		duplicate := false
		if failure, ok := message["error"].(map[string]any); ok {
			duplicate = failure["name"] == "Duplicate"
		}
		if !valueOK || parseErr != nil || parsed == 0 || !successOK || !success && !duplicate {
			return nil, "", 0, 0, false, fmt.Errorf("Azure reliable Web PubSub returned a failed acknowledgement")
		}
		ackID = parsed
	case "message":
		value, valueOK := message["sequenceId"].(json.Number)
		parsed, parseErr := strconv.ParseUint(string(value), 10, 64)
		if !valueOK || parseErr != nil || parsed == 0 {
			return nil, "", 0, 0, false, fmt.Errorf("Azure reliable Web PubSub message omitted sequenceId")
		}
		sequenceID = parsed
	case "pong", "streamAck":
	case "streamNack":
		return nil, "", 0, 0, false, fmt.Errorf("Azure reliable Web PubSub returned a stream rejection")
	case "streamClosed":
		if _, failed := message["error"]; failed {
			return nil, "", 0, 0, false, fmt.Errorf("Azure reliable Web PubSub stream closed with an error")
		}
	case "system":
		if message["event"] != "disconnected" {
			return nil, "", 0, 0, false, fmt.Errorf("Azure reliable Web PubSub returned an unsupported system event")
		}
		terminal = true
	default:
		return nil, "", 0, 0, false, fmt.Errorf("Azure reliable Web PubSub returned an unsupported message")
	}
	canonical, err := json.Marshal(message)
	if err != nil {
		return nil, "", 0, 0, false, fmt.Errorf("encode Azure reliable Web PubSub response")
	}
	return canonical, kind, sequenceID, ackID, terminal, nil
}

func writeAzureReliableSequenceAck(ctx context.Context, connection cloudWebSocketConnection, sequenceID uint64) error {
	message, _ := json.Marshal(map[string]any{"type": "sequenceAck", "sequenceId": sequenceID})
	if err := connection.Write(ctx, cloudWebSocketMessageText, message); err != nil {
		return fmt.Errorf("acknowledge Azure reliable Web PubSub sequence")
	}
	return nil
}

func writeAzureReliableSequenceAckForProtocol(ctx context.Context, connection cloudWebSocketConnection, sequenceID uint64, protobufProtocol bool) error {
	if !protobufProtocol {
		return writeAzureReliableSequenceAck(ctx, connection, sequenceID)
	}
	payload := azureWebPubSubProtobufAppendVarint(nil, 1, sequenceID)
	message := azureWebPubSubProtobufAppendBytes(nil, 8, payload)
	if err := connection.Write(ctx, cloudWebSocketMessageBinary, message); err != nil {
		return fmt.Errorf("acknowledge Azure reliable Web PubSub sequence")
	}
	return nil
}

func finishAzureReliableWebPubSub(sink *cloudWebSocketOutputSink, received int) (InvocationResult, error) {
	metadata, err := sink.finish("")
	if err != nil {
		return InvocationResult{}, err
	}
	var summary map[string]any
	if json.Unmarshal(metadata, &summary) != nil {
		return InvocationResult{}, fmt.Errorf("decode Azure reliable Web PubSub output metadata")
	}
	summary["messages"] = received
	metadata, err = json.Marshal(summary)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("encode Azure reliable Web PubSub output metadata")
	}
	return InvocationResult{Output: metadata}, nil
}

func defaultAzureWebPubSubWebSocketDial(ctx context.Context, target string, headers http.Header) (cloudWebSocketConnection, error) {
	return defaultAzureWebPubSubProtocolDial(ctx, target, headers, "json.webpubsub.azure.v1")
}

func defaultAzureReliableWebPubSubWebSocketDial(ctx context.Context, target string, headers http.Header) (cloudWebSocketConnection, error) {
	return defaultAzureWebPubSubProtocolDial(ctx, target, headers, "json.reliable.webpubsub.azure.v1")
}

func defaultAzureProtobufWebPubSubWebSocketDial(ctx context.Context, target string, headers http.Header) (cloudWebSocketConnection, error) {
	return defaultAzureWebPubSubProtocolDial(ctx, target, headers, "protobuf.webpubsub.azure.v1")
}

func defaultAzureReliableProtobufWebPubSubWebSocketDial(ctx context.Context, target string, headers http.Header) (cloudWebSocketConnection, error) {
	return defaultAzureWebPubSubProtocolDial(ctx, target, headers, "protobuf.reliable.webpubsub.azure.v1")
}

func defaultAzureWebPubSubProtocolDial(ctx context.Context, target string, headers http.Header, subprotocol string) (cloudWebSocketConnection, error) {
	client := &http.Client{Transport: http.DefaultTransport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	connection, response, err := websocket.Dial(ctx, target, &websocket.DialOptions{HTTPClient: client, HTTPHeader: headers.Clone(), Subprotocols: []string{subprotocol}, CompressionMode: websocket.CompressionDisabled})
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("Azure Web PubSub WebSocket handshake failed with HTTP %d", response.StatusCode)
		}
		return nil, fmt.Errorf("Azure Web PubSub WebSocket handshake failed")
	}
	if connection.Subprotocol() != subprotocol {
		connection.Close(websocket.StatusProtocolError, "missing required subprotocol")
		return nil, fmt.Errorf("Azure Web PubSub did not negotiate the required subprotocol")
	}
	connection.SetReadLimit(maxRequestPayloadBytes)
	return coderTencentWebSocketConnection{connection: connection}, nil
}

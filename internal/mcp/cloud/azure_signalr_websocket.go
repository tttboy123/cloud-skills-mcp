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
	authSchemeAzureSignalRWS      = "signalr-ws"
	azureSignalRAPIVersion        = "2022-11-01"
	azureSignalRMaxMessages       = 256
	azureSignalRMaxTimeoutSeconds = 300
	azureSignalRMaxInvocations    = 64
	azureSignalRRecordSeparator   = 0x1E
)

var (
	azureSignalRResourcePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,61}[a-z0-9]$`)
	azureSignalRHubPattern      = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,127}$`)
)

type azureSignalRInvocation struct {
	ID        string            `json:"id"`
	Target    string            `json:"target"`
	Arguments []json.RawMessage `json:"arguments"`
}

type azureSignalRPlan struct {
	UserID          string                   `json:"user_id,omitempty"`
	MinutesToExpire int                      `json:"minutes_to_expire,omitempty"`
	Invocations     []azureSignalRInvocation `json:"invocations,omitempty"`
	MaxMessages     int                      `json:"max_messages"`
	TimeoutSeconds  int                      `json:"timeout_seconds"`

	encodedInvocations [][]byte
}

type azureSignalRFrameReader struct {
	buffer []byte
}

var azureSignalRHandshake = []byte(`{"protocol":"json","version":1}` + "\x1e")

func validateAzureSignalRInvocation(invocation Invocation) error {
	operation := strings.ToLower(strings.TrimSpace(invocation.Operation))
	if !strings.EqualFold(invocation.Method, http.MethodGet) || !strings.EqualFold(invocation.Service, "signalr") || (operation != "subscribe" && operation != "invoke") {
		return fmt.Errorf("Azure SignalR requires GET, service signalr, and operation Subscribe or Invoke")
	}
	if _, _, err := parseAzureSignalRTarget(invocation.URL); err != nil {
		return err
	}
	if len(invocation.Headers) != 0 || len(invocation.Parameters) != 0 {
		return fmt.Errorf("Azure SignalR does not accept caller headers or query parameters")
	}
	if invocation.Body == nil || invocation.BodyFile != "" || invocation.ResponseFile == "" {
		return fmt.Errorf("Azure SignalR requires a finite protocol body and response_file; body_file is forbidden")
	}
	if invocation.Region != "" || invocation.Project != "" || invocation.Subscription != "" || invocation.Audience != "" || invocation.APIVersion != "" || invocation.AuthVersion != "" || invocation.PayloadMode != "" || invocation.ChecksumAlgorithm != "" || invocation.StreamChunkBytes != 0 || invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return fmt.Errorf("Azure SignalR does not accept REST, cross-provider, checksum, or binary stream controls")
	}
	_, err := parseAzureSignalRPlan(invocation.Body, operation)
	return err
}

func parseAzureSignalRTarget(rawURL string) (*url.URL, string, error) {
	target, err := url.Parse(rawURL)
	if err != nil || target.Scheme != "wss" || target.User != nil || target.Fragment != "" || target.Port() != "" {
		return nil, "", fmt.Errorf("Azure SignalR requires an exact credential-free wss:// URL")
	}
	host := strings.ToLower(target.Hostname())
	_, validResource := azureSignalRResourceName(host)
	if !validResource {
		return nil, "", fmt.Errorf("Azure SignalR requires a resource.service.signalr.net host")
	}
	if target.Path != "/client/" {
		return nil, "", fmt.Errorf("Azure SignalR requires the official /client/ path")
	}
	values := target.Query()
	hubs := values["hub"]
	if len(values) != 1 || len(hubs) != 1 || hubs[0] == "" {
		return nil, "", fmt.Errorf("Azure SignalR requires exactly the official hub query parameter")
	}
	hub := hubs[0]
	if !azureSignalRHubPattern.MatchString(hub) {
		return nil, "", fmt.Errorf("Azure SignalR requires one valid hub name")
	}
	return target, hub, nil
}

func azureSignalRResourceName(host string) (string, bool) {
	const suffix = ".service.signalr.net"
	resource := strings.TrimSuffix(host, suffix)
	return resource, resource != host && resource != "privatelink" && azureSignalRResourcePattern.MatchString(resource)
}

func parseAzureSignalRPlan(body any, operation string) (azureSignalRPlan, error) {
	if body == nil || azureRealtimeContainsCredentialField(body) {
		return azureSignalRPlan{}, fmt.Errorf("Azure SignalR requires a credential-free protocol body")
	}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) == 0 || len(encoded) > maxRequestPayloadBytes {
		return azureSignalRPlan{}, fmt.Errorf("Azure SignalR body must be bounded JSON")
	}
	var plan azureSignalRPlan
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&plan) != nil || ensureJSONDecoderEOF(decoder) != nil {
		return azureSignalRPlan{}, fmt.Errorf("Azure SignalR body does not match the finite session schema")
	}
	if plan.UserID != "" && !azureWebPubSubBoundedValue(plan.UserID, 128) {
		return azureSignalRPlan{}, fmt.Errorf("Azure SignalR user_id is invalid")
	}
	if plan.MinutesToExpire == 0 {
		plan.MinutesToExpire = 5
	}
	if plan.MinutesToExpire < 1 || plan.MinutesToExpire > 60 {
		return azureSignalRPlan{}, fmt.Errorf("Azure SignalR minutes_to_expire must be between 1 and 60")
	}
	if plan.MaxMessages < 1 || plan.MaxMessages > azureSignalRMaxMessages || plan.TimeoutSeconds < 1 || plan.TimeoutSeconds > azureSignalRMaxTimeoutSeconds {
		return azureSignalRPlan{}, fmt.Errorf("Azure SignalR response count or timeout is outside the finite bound")
	}
	if operation == "subscribe" {
		if len(plan.Invocations) != 0 {
			return azureSignalRPlan{}, fmt.Errorf("Azure SignalR Subscribe does not accept invocations")
		}
		return plan, nil
	}
	if len(plan.Invocations) < 1 || len(plan.Invocations) > azureSignalRMaxInvocations {
		return azureSignalRPlan{}, fmt.Errorf("Azure SignalR Invoke requires 1 to %d bounded invocations", azureSignalRMaxInvocations)
	}
	ids := make(map[string]bool, len(plan.Invocations))
	for index := range plan.Invocations {
		invocation := &plan.Invocations[index]
		if !azureWebPubSubBoundedValue(invocation.ID, 64) {
			return azureSignalRPlan{}, fmt.Errorf("Azure SignalR invocation %d id is invalid", index)
		}
		if ids[invocation.ID] {
			return azureSignalRPlan{}, fmt.Errorf("Azure SignalR invocation ids must be unique")
		}
		ids[invocation.ID] = true
		if !azureWebPubSubBoundedValue(invocation.Target, 128) {
			return azureSignalRPlan{}, fmt.Errorf("Azure SignalR invocation %d target is invalid", index)
		}
		if len(invocation.Arguments) > 32 {
			return azureSignalRPlan{}, fmt.Errorf("Azure SignalR invocation %d arguments exceed the bounded count", index)
		}
		for argumentIndex, raw := range invocation.Arguments {
			if len(raw) == 0 || len(raw) > maxRequestPayloadBytes || !json.Valid(raw) {
				return azureSignalRPlan{}, fmt.Errorf("Azure SignalR invocation %d argument %d is invalid", index, argumentIndex)
			}
			var decoded any
			argumentDecoder := json.NewDecoder(bytes.NewReader(raw))
			argumentDecoder.UseNumber()
			if argumentDecoder.Decode(&decoded) != nil || ensureJSONDecoderEOF(argumentDecoder) != nil || azureRealtimeContainsCredentialField(decoded) {
				return azureSignalRPlan{}, fmt.Errorf("Azure SignalR invocation %d argument %d is invalid or contains credentials", index, argumentIndex)
			}
		}
		frame := map[string]any{
			"type":         1,
			"invocationId": invocation.ID,
			"target":       invocation.Target,
			"arguments":    invocation.Arguments,
		}
		canonical, err := json.Marshal(frame)
		if err != nil || len(canonical) > maxRequestPayloadBytes {
			return azureSignalRPlan{}, fmt.Errorf("encode Azure SignalR invocation %d", index)
		}
		plan.encodedInvocations = append(plan.encodedInvocations, canonical)
	}
	return plan, nil
}

func invokeAzureSignalR(ctx context.Context, adapter *AzureRESTAdapter, invocation Invocation) (InvocationResult, error) {
	if err := validateAzureSignalRInvocation(invocation); err != nil {
		return InvocationResult{}, err
	}
	operation := strings.ToLower(strings.TrimSpace(invocation.Operation))
	target, hub, _ := parseAzureSignalRTarget(invocation.URL)
	plan, _ := parseAzureSignalRPlan(invocation.Body, operation)
	identityToken, err := adapter.config.Tokens.Token(ctx, "https://signalr.azure.com/.default")
	if err != nil {
		return InvocationResult{}, fmt.Errorf("load Azure SignalR identity token: %w", err)
	}
	if strings.TrimSpace(identityToken) == "" {
		return InvocationResult{}, fmt.Errorf("Azure identity returned an empty access token")
	}
	clientToken, err := mintAzureSignalRClientToken(ctx, adapter, target, hub, plan, identityToken)
	if err != nil {
		return InvocationResult{}, err
	}
	dialURL := azureSignalRDialURL(target, clientToken)
	if operation == "subscribe" {
		return invokeAzureSignalRSubscribe(ctx, adapter, invocation, plan, dialURL)
	}
	return invokeAzureSignalRInvoke(ctx, adapter, invocation, plan, dialURL)
}

func mintAzureSignalRClientToken(ctx context.Context, adapter *AzureRESTAdapter, target *url.URL, hub string, plan azureSignalRPlan, identityToken string) (string, error) {
	tokenURL := &url.URL{Scheme: "https", Host: target.Host, Path: "/api/hubs/" + hub + "/:generateToken"}
	query := tokenURL.Query()
	query.Set("api-version", azureSignalRAPIVersion)
	query.Set("minutesToExpire", strconv.Itoa(plan.MinutesToExpire))
	if plan.UserID != "" {
		query.Set("userId", plan.UserID)
	}
	tokenURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL.String(), nil)
	if err != nil {
		return "", fmt.Errorf("build Azure SignalR client-token request")
	}
	request.Header.Set("Authorization", "Bearer "+identityToken)
	response, err := adapter.config.HTTP.Do(request)
	if err != nil {
		return "", fmt.Errorf("Azure SignalR client-token request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Azure SignalR client-token request failed with HTTP %d", response.StatusCode)
	}
	limited := io.LimitReader(response.Body, maxRequestPayloadBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil || len(data) > maxRequestPayloadBytes {
		return "", fmt.Errorf("read Azure SignalR client-token response")
	}
	var payload struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(data, &payload) != nil || !azureWebPubSubBoundedValue(payload.Token, 16384) {
		return "", fmt.Errorf("Azure SignalR returned an invalid client-token response")
	}
	return payload.Token, nil
}

func azureSignalRDialURL(target *url.URL, clientToken string) string {
	query := target.Query()
	query.Set("access_token", clientToken)
	copy := *target
	copy.RawQuery = query.Encode()
	return copy.String()
}

func defaultAzureSignalRWebSocketDial(ctx context.Context, target string, _ http.Header) (cloudWebSocketConnection, error) {
	client := &http.Client{Transport: http.DefaultTransport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	connection, response, err := websocket.Dial(ctx, target, &websocket.DialOptions{HTTPClient: client, CompressionMode: websocket.CompressionDisabled})
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("Azure SignalR WebSocket handshake failed with HTTP %d", response.StatusCode)
		}
		return nil, fmt.Errorf("Azure SignalR WebSocket handshake failed")
	}
	connection.SetReadLimit(maxRequestPayloadBytes)
	return coderTencentWebSocketConnection{connection: connection}, nil
}

func azureSignalRReadHandshake(ctx context.Context, reader *azureSignalRFrameReader, connection cloudWebSocketConnection) error {
	frame, err := reader.next(ctx, connection)
	if err != nil {
		return fmt.Errorf("read Azure SignalR handshake response: %w", err)
	}
	var response struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(frame, &response) != nil {
		return fmt.Errorf("Azure SignalR returned an invalid handshake response")
	}
	if response.Error != "" {
		return fmt.Errorf("Azure SignalR handshake failed: %s", response.Error)
	}
	return nil
}

func (reader *azureSignalRFrameReader) next(ctx context.Context, connection cloudWebSocketConnection) ([]byte, error) {
	for {
		if index := bytes.IndexByte(reader.buffer, azureSignalRRecordSeparator); index >= 0 {
			frame := reader.buffer[:index]
			reader.buffer = reader.buffer[index+1:]
			if len(frame) == 0 {
				return nil, fmt.Errorf("Azure SignalR returned an empty protocol frame")
			}
			if len(frame) > maxRequestPayloadBytes || !utf8.Valid(frame) || !json.Valid(frame) {
				return nil, fmt.Errorf("Azure SignalR returned an invalid JSON frame")
			}
			return frame, nil
		}
		if len(reader.buffer) > maxRequestPayloadBytes {
			return nil, fmt.Errorf("Azure SignalR frame exceeds the bounded size")
		}
		messageType, data, err := connection.Read(ctx)
		if err != nil {
			return nil, err
		}
		if messageType != cloudWebSocketMessageText || len(data) == 0 || !utf8.Valid(data) {
			return nil, fmt.Errorf("Azure SignalR returned a non-JSON protocol frame")
		}
		if len(reader.buffer)+len(data) > maxRequestPayloadBytes {
			return nil, fmt.Errorf("Azure SignalR frame exceeds the bounded size")
		}
		reader.buffer = append(reader.buffer, data...)
	}
}

func sanitizeAzureSignalRFrame(frame []byte) (map[string]any, int, error) {
	var message map[string]any
	decoder := json.NewDecoder(bytes.NewReader(frame))
	decoder.UseNumber()
	if decoder.Decode(&message) != nil || ensureJSONDecoderEOF(decoder) != nil {
		return nil, 0, fmt.Errorf("Azure SignalR returned invalid JSON")
	}
	rawType, ok := message["type"].(json.Number)
	if !ok {
		return nil, 0, fmt.Errorf("Azure SignalR protocol frame has no type")
	}
	messageType, err := strconv.Atoi(string(rawType))
	if err != nil {
		return nil, 0, fmt.Errorf("Azure SignalR protocol frame has an invalid type")
	}
	switch messageType {
	case 1:
		target, ok := message["target"].(string)
		if !ok || !azureWebPubSubBoundedValue(target, 128) {
			return nil, 0, fmt.Errorf("Azure SignalR invocation has an invalid target")
		}
		arguments, ok := message["arguments"].([]any)
		if !ok || len(arguments) > 32 {
			return nil, 0, fmt.Errorf("Azure SignalR invocation has invalid arguments")
		}
		if rawID, exists := message["invocationId"]; exists {
			id, ok := rawID.(string)
			if !ok || !azureWebPubSubBoundedValue(id, 1024) {
				return nil, 0, fmt.Errorf("Azure SignalR invocation has an invalid invocationId")
			}
		}
	case 2, 3:
		id, ok := message["invocationId"].(string)
		if !ok || !azureWebPubSubBoundedValue(id, 1024) {
			return nil, 0, fmt.Errorf("Azure SignalR response has an invalid invocationId")
		}
		if messageType == 3 {
			if rawError, exists := message["error"]; exists {
				errorText, ok := rawError.(string)
				if !ok || len(errorText) > maxRequestPayloadBytes {
					return nil, 0, fmt.Errorf("Azure SignalR completion has an invalid error")
				}
			}
			_, hasResult := message["result"]
			_, hasError := message["error"]
			if hasResult && hasError {
				return nil, 0, fmt.Errorf("Azure SignalR completion has both result and error")
			}
		}
	case 6:
	case 7:
		if rawError, exists := message["error"]; exists {
			errorText, ok := rawError.(string)
			if !ok || len(errorText) > maxRequestPayloadBytes {
				return nil, 0, fmt.Errorf("Azure SignalR close has an invalid error")
			}
		}
	default:
		return nil, 0, fmt.Errorf("Azure SignalR returned an unsupported protocol message")
	}
	return message, messageType, nil
}

func invokeAzureSignalRSubscribe(ctx context.Context, adapter *AzureRESTAdapter, invocation Invocation, plan azureSignalRPlan, dialURL string) (InvocationResult, error) {
	sessionCtx, cancel := context.WithTimeout(ctx, time.Duration(plan.TimeoutSeconds)*time.Second)
	defer cancel()
	connection, err := adapter.config.SignalRWebSocketDial(sessionCtx, dialURL, http.Header{})
	if err != nil {
		return InvocationResult{}, err
	}
	defer connection.Close()
	if err := connection.Write(sessionCtx, cloudWebSocketMessageText, azureSignalRHandshake); err != nil {
		return InvocationResult{}, fmt.Errorf("send Azure SignalR handshake")
	}
	reader := &azureSignalRFrameReader{}
	if err := azureSignalRReadHandshake(sessionCtx, reader, connection); err != nil {
		return InvocationResult{}, err
	}
	sink, err := newWebSocketOutputSink(invocation, adapter.config.MaxBodyBytes, "Azure SignalR WebSocket")
	if err != nil {
		return InvocationResult{}, err
	}
	defer sink.abort()
	received := 0
	terminal := false
	for received < plan.MaxMessages && !terminal {
		frame, err := reader.next(sessionCtx, connection)
		if errors.Is(err, context.DeadlineExceeded) || err == io.EOF || websocket.CloseStatus(err) == websocket.StatusNormalClosure {
			break
		}
		if err != nil {
			return InvocationResult{}, err
		}
		message, messageType, err := sanitizeAzureSignalRFrame(frame)
		if err != nil {
			return InvocationResult{}, err
		}
		switch messageType {
		case 6:
			continue
		case 7:
			if rawError, exists := message["error"]; exists {
				errorText, _ := rawError.(string)
				return InvocationResult{}, fmt.Errorf("Azure SignalR closed the connection: %s", errorText)
			}
			terminal = true
		case 1:
			if rawID, exists := message["invocationId"]; exists {
				id := rawID.(string)
				completion, err := json.Marshal(map[string]any{"type": 3, "invocationId": id})
				if err != nil {
					return InvocationResult{}, fmt.Errorf("acknowledge Azure SignalR invocation")
				}
				completion = append(completion, azureSignalRRecordSeparator)
				if err := connection.Write(sessionCtx, cloudWebSocketMessageText, completion); err != nil {
					return InvocationResult{}, fmt.Errorf("acknowledge Azure SignalR invocation")
				}
			}
		default:
			return InvocationResult{}, fmt.Errorf("Azure SignalR returned an unsupported protocol message")
		}
		canonical, err := json.Marshal(message)
		if err != nil {
			return InvocationResult{}, fmt.Errorf("encode Azure SignalR response")
		}
		if err := sink.writeMessage(canonical); err != nil {
			return InvocationResult{}, err
		}
		received++
	}
	metadata, err := sink.finish("")
	if err != nil {
		return InvocationResult{}, err
	}
	var summary map[string]any
	if json.Unmarshal(metadata, &summary) != nil {
		return InvocationResult{}, fmt.Errorf("decode Azure SignalR output metadata")
	}
	summary["messages"] = received
	metadata, err = json.Marshal(summary)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("encode Azure SignalR output metadata")
	}
	return InvocationResult{Output: metadata}, nil
}

func invokeAzureSignalRInvoke(ctx context.Context, adapter *AzureRESTAdapter, invocation Invocation, plan azureSignalRPlan, dialURL string) (InvocationResult, error) {
	sessionCtx, cancel := context.WithTimeout(ctx, time.Duration(plan.TimeoutSeconds)*time.Second)
	defer cancel()
	connection, err := adapter.config.SignalRWebSocketDial(sessionCtx, dialURL, http.Header{})
	if err != nil {
		return InvocationResult{}, err
	}
	defer connection.Close()
	if err := connection.Write(sessionCtx, cloudWebSocketMessageText, azureSignalRHandshake); err != nil {
		return InvocationResult{}, fmt.Errorf("send Azure SignalR handshake")
	}
	reader := &azureSignalRFrameReader{}
	if err := azureSignalRReadHandshake(sessionCtx, reader, connection); err != nil {
		return InvocationResult{}, err
	}
	sink, err := newWebSocketOutputSink(invocation, adapter.config.MaxBodyBytes, "Azure SignalR WebSocket")
	if err != nil {
		return InvocationResult{}, err
	}
	defer sink.abort()
	pending := make(map[string]bool, len(plan.Invocations))
	for index, canonical := range plan.encodedInvocations {
		pending[plan.Invocations[index].ID] = true
		frame := append(append([]byte(nil), canonical...), azureSignalRRecordSeparator)
		if index > 0 && invocation.StreamIntervalMS > 0 {
			if err := adapter.config.StreamPause(sessionCtx, time.Duration(invocation.StreamIntervalMS)*time.Millisecond); err != nil {
				return InvocationResult{}, fmt.Errorf("pace Azure SignalR invocations")
			}
		}
		if err := connection.Write(sessionCtx, cloudWebSocketMessageText, frame); err != nil {
			return InvocationResult{}, fmt.Errorf("send Azure SignalR invocation")
		}
	}
	received := 0
	terminal := false
	for received < plan.MaxMessages && !terminal {
		frame, err := reader.next(sessionCtx, connection)
		if errors.Is(err, context.DeadlineExceeded) || err == io.EOF || websocket.CloseStatus(err) == websocket.StatusNormalClosure {
			break
		}
		if err != nil {
			return InvocationResult{}, err
		}
		message, messageType, err := sanitizeAzureSignalRFrame(frame)
		if err != nil {
			return InvocationResult{}, err
		}
		switch messageType {
		case 6:
			continue
		case 7:
			if rawError, exists := message["error"]; exists {
				errorText, _ := rawError.(string)
				return InvocationResult{}, fmt.Errorf("Azure SignalR closed the connection: %s", errorText)
			}
			terminal = true
		case 1:
			if rawID, exists := message["invocationId"]; exists {
				id := rawID.(string)
				completion, err := json.Marshal(map[string]any{"type": 3, "invocationId": id})
				if err != nil {
					return InvocationResult{}, fmt.Errorf("acknowledge Azure SignalR invocation")
				}
				completion = append(completion, azureSignalRRecordSeparator)
				if err := connection.Write(sessionCtx, cloudWebSocketMessageText, completion); err != nil {
					return InvocationResult{}, fmt.Errorf("acknowledge Azure SignalR invocation")
				}
			}
		case 2, 3:
			id := message["invocationId"].(string)
			if !pending[id] {
				return InvocationResult{}, fmt.Errorf("Azure SignalR returned an unexpected invocationId")
			}
			if messageType == 3 {
				if rawError, exists := message["error"]; exists {
					errorText, _ := rawError.(string)
					return InvocationResult{}, fmt.Errorf("Azure SignalR invocation %s failed: %s", id, errorText)
				}
				delete(pending, id)
			}
		default:
			return InvocationResult{}, fmt.Errorf("Azure SignalR returned an unsupported protocol message")
		}
		canonical, err := json.Marshal(message)
		if err != nil {
			return InvocationResult{}, fmt.Errorf("encode Azure SignalR response")
		}
		if err := sink.writeMessage(canonical); err != nil {
			return InvocationResult{}, err
		}
		received++
	}
	if len(pending) != 0 {
		return InvocationResult{}, fmt.Errorf("Azure SignalR session ended before %d invocation(s) completed", len(pending))
	}
	metadata, err := sink.finish("")
	if err != nil {
		return InvocationResult{}, err
	}
	var summary map[string]any
	if json.Unmarshal(metadata, &summary) != nil {
		return InvocationResult{}, fmt.Errorf("decode Azure SignalR output metadata")
	}
	summary["messages"] = received
	metadata, err = json.Marshal(summary)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("encode Azure SignalR output metadata")
	}
	return InvocationResult{Output: metadata}, nil
}

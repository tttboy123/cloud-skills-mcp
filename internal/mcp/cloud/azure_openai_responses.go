package cloud

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/tttboy123/cloud-skills-mcp/internal/mcp/sdk"
)

const (
	authSchemeAzureOpenAIResponsesStream = "openai-responses-stream"

	azureOpenAIResponsesStreamScope = "https://cognitiveservices.azure.com/.default"

	azureOpenAIResponsesStreamMaxEvents         = 256
	azureOpenAIResponsesStreamMaxTimeoutSeconds = 300
	azureOpenAIResponsesStreamMaxInputItems     = 64
	azureOpenAIResponsesStreamMaxInputParts     = 16
	azureOpenAIResponsesStreamMaxMessageBytes   = 64 * 1024
	azureOpenAIResponsesStreamMaxChunkBytes     = 1024 * 1024
	azureOpenAIResponsesStreamMaxTokens         = 1_000_000
)

var (
	azureOpenAIResponsesStreamAPIVersionPattern = regexp.MustCompile(`^(?:v1|preview|[0-9]{4}-[0-9]{2}-[0-9]{2}(?:-[a-z0-9]+)?)$`)
	azureOpenAIResponsesDeploymentPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	azureOpenAIResponsesItemIDPattern           = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,255}$`)
)

// azureOpenAIResponsesStreamEventTypes is the documented ResponseStreamEvent
// union from the official Responses API stream contract (OpenAI SDK types used
// verbatim by Azure OpenAI). Unknown event types fail closed.
var azureOpenAIResponsesStreamEventTypes = map[string]bool{
	"error":                                        true,
	"response.audio.delta":                         true,
	"response.audio.done":                          true,
	"response.audio.transcript.delta":              true,
	"response.audio.transcript.done":               true,
	"response.code_interpreter_call.completed":     true,
	"response.code_interpreter_call.in_progress":   true,
	"response.code_interpreter_call.interpreting":  true,
	"response.code_interpreter_call_code.delta":    true,
	"response.code_interpreter_call_code.done":     true,
	"response.completed":                           true,
	"response.content_part.added":                  true,
	"response.content_part.done":                   true,
	"response.created":                             true,
	"response.custom_tool_call_input.delta":        true,
	"response.custom_tool_call_input.done":         true,
	"response.failed":                              true,
	"response.file_search_call.completed":          true,
	"response.file_search_call.in_progress":        true,
	"response.file_search_call.searching":          true,
	"response.function_call_arguments.delta":       true,
	"response.function_call_arguments.done":        true,
	"response.image_generation_call.completed":     true,
	"response.image_generation_call.generating":    true,
	"response.image_generation_call.in_progress":   true,
	"response.image_generation_call.partial_image": true,
	"response.in_progress":                         true,
	"response.incomplete":                          true,
	"response.mcp_call.completed":                  true,
	"response.mcp_call.failed":                     true,
	"response.mcp_call.in_progress":                true,
	"response.mcp_call_arguments.delta":            true,
	"response.mcp_call_arguments.done":             true,
	"response.mcp_list_tools.completed":            true,
	"response.mcp_list_tools.failed":               true,
	"response.mcp_list_tools.in_progress":          true,
	"response.output_item.added":                   true,
	"response.output_item.done":                    true,
	"response.output_text.annotation.added":        true,
	"response.output_text.delta":                   true,
	"response.output_text.done":                    true,
	"response.queued":                              true,
	"response.reasoning_summary_part.added":        true,
	"response.reasoning_summary_part.done":         true,
	"response.reasoning_summary_text.delta":        true,
	"response.reasoning_summary_text.done":         true,
	"response.reasoning_text.delta":                true,
	"response.reasoning_text.done":                 true,
	"response.refusal.delta":                       true,
	"response.refusal.done":                        true,
	"response.web_search_call.completed":           true,
	"response.web_search_call.in_progress":         true,
	"response.web_search_call.searching":           true,
}

type azureOpenAIResponsesStreamPlan struct {
	Model           string   `json:"model"`
	Input           any      `json:"input"`
	MaxOutputTokens *int     `json:"max_output_tokens,omitempty"`
	Temperature     *float64 `json:"temperature,omitempty"`
	MaxEvents       int      `json:"max_events"`
	TimeoutSeconds  int      `json:"timeout_seconds"`
}

type azureOpenAIResponsesInputItem struct {
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content any    `json:"content"`
}

func validateAzureOpenAIResponsesStreamInvocation(invocation Invocation) error {
	if invocation.Mode != ModeRead || !strings.EqualFold(invocation.Method, http.MethodPost) ||
		!strings.EqualFold(invocation.Service, "openai") || !strings.EqualFold(invocation.Operation, "StreamResponses") {
		return fmt.Errorf("Azure OpenAI Responses streaming requires the read tool, POST, service openai, and operation StreamResponses")
	}
	if _, err := parseAzureOpenAIResponsesStreamTarget(invocation.URL); err != nil {
		return err
	}
	if invocation.APIVersion != "" && !azureOpenAIResponsesStreamAPIVersionPattern.MatchString(invocation.APIVersion) {
		return fmt.Errorf("Azure OpenAI Responses streaming api_version must be v1, preview, a date-structured version, or omitted for the v1 GA default")
	}
	if len(invocation.Parameters) != 0 {
		return fmt.Errorf("Azure OpenAI Responses streaming does not accept caller query parameters")
	}
	if len(invocation.Headers) != 0 {
		return fmt.Errorf("Azure OpenAI Responses streaming does not accept caller headers")
	}
	if invocation.Body == nil || invocation.BodyFile != "" || invocation.ImageFile != "" || invocation.ProtobufDescriptorFile != "" || invocation.ResponseFile == "" {
		return fmt.Errorf("Azure OpenAI Responses streaming requires a finite body plan and response_file; input files are forbidden")
	}
	if invocation.Region != "" || invocation.RegionSet != "" || invocation.Project != "" || invocation.Subscription != "" || invocation.Audience != "" ||
		invocation.AuthVersion != "" || invocation.PayloadMode != "" || invocation.ChecksumAlgorithm != "" ||
		invocation.StreamChunkBytes != 0 || invocation.StreamIntervalMS != 0 || invocation.StreamMaxMessages != 0 || invocation.StreamTimeoutSeconds != 0 ||
		invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return fmt.Errorf("Azure OpenAI Responses streaming does not accept REST, cross-provider, or stream transport controls")
	}
	plan, err := parseAzureOpenAIResponsesStreamPlan(invocation.Body)
	if err != nil {
		return err
	}
	return validateAzureOpenAIResponsesDeployment(plan.Model)
}

func parseAzureOpenAIResponsesStreamTarget(rawURL string) (*url.URL, error) {
	target, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(target.Scheme, "https") || target.Hostname() == "" || target.User != nil ||
		target.Fragment != "" || target.RawQuery != "" {
		return nil, fmt.Errorf("Azure OpenAI Responses streaming requires an exact credential-free query-free https:// URL")
	}
	if target.Port() != "" && target.Port() != "443" {
		return nil, fmt.Errorf("Azure OpenAI Responses streaming endpoint port must be 443")
	}
	const publicSuffix = ".openai.azure.com"
	host := strings.ToLower(target.Hostname())
	resource := strings.TrimSuffix(host, publicSuffix)
	if resource == host || resource == "privatelink" || !endpointLabelPattern.MatchString(resource) {
		return nil, fmt.Errorf("Azure OpenAI Responses streaming requires an exact public-cloud resource.openai.azure.com host")
	}
	if target.EscapedPath() != "/openai/v1/responses" {
		return nil, fmt.Errorf("Azure OpenAI Responses streaming requires the official /openai/v1/responses path")
	}
	return target, nil
}

func parseAzureOpenAIResponsesStreamPlan(body any) (azureOpenAIResponsesStreamPlan, error) {
	if body == nil || azureRealtimeContainsCredentialField(body) {
		return azureOpenAIResponsesStreamPlan{}, fmt.Errorf("Azure OpenAI Responses streaming requires a credential-free finite plan")
	}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) == 0 || len(encoded) > maxRequestPayloadBytes {
		return azureOpenAIResponsesStreamPlan{}, fmt.Errorf("Azure OpenAI Responses streaming plan must be bounded JSON")
	}
	var plan azureOpenAIResponsesStreamPlan
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil || ensureJSONDecoderEOF(decoder) != nil {
		return azureOpenAIResponsesStreamPlan{}, fmt.Errorf("Azure OpenAI Responses streaming body does not match the finite plan schema")
	}
	if err := validateAzureOpenAIResponsesDeployment(plan.Model); err != nil {
		return azureOpenAIResponsesStreamPlan{}, err
	}
	if err := validateAzureOpenAIResponsesInput(plan.Input); err != nil {
		return azureOpenAIResponsesStreamPlan{}, err
	}
	if plan.MaxOutputTokens != nil && (*plan.MaxOutputTokens < 1 || *plan.MaxOutputTokens > azureOpenAIResponsesStreamMaxTokens) {
		return azureOpenAIResponsesStreamPlan{}, fmt.Errorf("Azure OpenAI Responses streaming max_output_tokens must be between 1 and %d", azureOpenAIResponsesStreamMaxTokens)
	}
	if plan.Temperature != nil && (*plan.Temperature < 0 || *plan.Temperature > 2) {
		return azureOpenAIResponsesStreamPlan{}, fmt.Errorf("Azure OpenAI Responses streaming temperature must be between 0 and 2")
	}
	if plan.MaxEvents < 1 || plan.MaxEvents > azureOpenAIResponsesStreamMaxEvents {
		return azureOpenAIResponsesStreamPlan{}, fmt.Errorf("Azure OpenAI Responses streaming max_events must be between 1 and %d", azureOpenAIResponsesStreamMaxEvents)
	}
	if plan.TimeoutSeconds < 1 || plan.TimeoutSeconds > azureOpenAIResponsesStreamMaxTimeoutSeconds {
		return azureOpenAIResponsesStreamPlan{}, fmt.Errorf("Azure OpenAI Responses streaming timeout_seconds must be between 1 and %d", azureOpenAIResponsesStreamMaxTimeoutSeconds)
	}
	return plan, nil
}

func validateAzureOpenAIResponsesDeployment(deployment string) error {
	if !azureOpenAIResponsesDeploymentPattern.MatchString(deployment) {
		return fmt.Errorf("Azure OpenAI Responses streaming model must be one bounded deployment name")
	}
	return nil
}

func validateAzureOpenAIResponsesInput(input any) error {
	switch typed := input.(type) {
	case string:
		if len(typed) == 0 || len(typed) > azureOpenAIResponsesStreamMaxMessageBytes {
			return fmt.Errorf("Azure OpenAI Responses streaming input must be between 1 and %d bytes", azureOpenAIResponsesStreamMaxMessageBytes)
		}
		return nil
	case []any:
		if len(typed) < 1 || len(typed) > azureOpenAIResponsesStreamMaxInputItems {
			return fmt.Errorf("Azure OpenAI Responses streaming input items must be between 1 and %d", azureOpenAIResponsesStreamMaxInputItems)
		}
		for index, raw := range typed {
			if err := validateAzureOpenAIResponsesInputItem(raw); err != nil {
				return fmt.Errorf("input item %d: %w", index, err)
			}
		}
		return nil
	default:
		return fmt.Errorf("Azure OpenAI Responses streaming input must be a bounded string or an array of message items")
	}
}

func validateAzureOpenAIResponsesInputItem(raw any) error {
	encoded, err := json.Marshal(raw)
	if err != nil || len(encoded) == 0 || len(encoded) > maxRequestPayloadBytes {
		return fmt.Errorf("item must be bounded JSON")
	}
	var item azureOpenAIResponsesInputItem
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&item); err != nil || ensureJSONDecoderEOF(decoder) != nil {
		return fmt.Errorf("item does not match the documented message item shape")
	}
	if item.Type != "message" {
		return fmt.Errorf("item type must be message")
	}
	switch item.Role {
	case "user", "system", "developer", "assistant":
	default:
		return fmt.Errorf("item role must be user, system, developer, or assistant")
	}
	return validateAzureOpenAIResponsesInputContent(item.Content)
}

func validateAzureOpenAIResponsesInputContent(content any) error {
	switch typed := content.(type) {
	case string:
		if len(typed) == 0 || len(typed) > azureOpenAIResponsesStreamMaxMessageBytes {
			return fmt.Errorf("content must be between 1 and %d bytes", azureOpenAIResponsesStreamMaxMessageBytes)
		}
		return nil
	case []any:
		if len(typed) < 1 || len(typed) > azureOpenAIResponsesStreamMaxInputParts {
			return fmt.Errorf("content parts must be between 1 and %d", azureOpenAIResponsesStreamMaxInputParts)
		}
		for partIndex, raw := range typed {
			part, ok := raw.(map[string]any)
			if !ok || len(part) != 2 {
				return fmt.Errorf("content part %d must be an input_text object", partIndex)
			}
			partType, typeOK := part["type"].(string)
			text, textOK := part["text"].(string)
			if !typeOK || !textOK || partType != "input_text" || len(text) == 0 || len(text) > azureOpenAIResponsesStreamMaxMessageBytes {
				return fmt.Errorf("content part %d must be one bounded input_text part", partIndex)
			}
		}
		return nil
	default:
		return fmt.Errorf("content must be a bounded string or an array of input_text parts")
	}
}

func buildAzureOpenAIResponsesStreamRequest(plan azureOpenAIResponsesStreamPlan) ([]byte, error) {
	body := map[string]any{
		"model":  plan.Model,
		"input":  plan.Input,
		"stream": true,
		"store":  false,
	}
	if plan.MaxOutputTokens != nil {
		body["max_output_tokens"] = *plan.MaxOutputTokens
	}
	if plan.Temperature != nil {
		body["temperature"] = *plan.Temperature
	}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) == 0 || len(encoded) > maxRequestPayloadBytes {
		return nil, fmt.Errorf("encode Azure OpenAI Responses stream request")
	}
	return encoded, nil
}

func invokeAzureOpenAIResponsesStream(ctx context.Context, adapter *AzureRESTAdapter, invocation Invocation) (InvocationResult, error) {
	if err := validateAzureOpenAIResponsesStreamInvocation(invocation); err != nil {
		return InvocationResult{}, err
	}
	target, _ := parseAzureOpenAIResponsesStreamTarget(invocation.URL)
	plan, _ := parseAzureOpenAIResponsesStreamPlan(invocation.Body)
	sink, err := newWebSocketOutputSink(invocation, adapter.config.MaxBodyBytes, "Azure OpenAI Responses streaming")
	if err != nil {
		return InvocationResult{}, err
	}
	defer sink.abort()
	token, err := adapter.config.Tokens.Token(ctx, azureOpenAIResponsesStreamScope)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("load Azure OpenAI Responses identity token: %w", err)
	}
	if strings.TrimSpace(token) == "" {
		return InvocationResult{}, fmt.Errorf("Azure identity returned an empty access token")
	}
	requestBody, err := buildAzureOpenAIResponsesStreamRequest(plan)
	if err != nil {
		return InvocationResult{}, err
	}
	if invocation.APIVersion != "" {
		query := target.Query()
		query.Set("api-version", invocation.APIVersion)
		target.RawQuery = query.Encode()
	}
	streamContext, cancel := context.WithTimeout(ctx, time.Duration(plan.TimeoutSeconds)*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(streamContext, http.MethodPost, target.String(), bytes.NewReader(requestBody))
	if err != nil {
		return InvocationResult{}, fmt.Errorf("build Azure OpenAI Responses request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Authorization", "Bearer "+token)
	request.ContentLength = int64(len(requestBody))
	response, err := adapter.config.HTTP.Do(request)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("Azure OpenAI Responses stream request: %w", err)
	}
	if response == nil || response.Body == nil {
		return InvocationResult{}, fmt.Errorf("Azure OpenAI Responses stream returned an empty response")
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		limited := io.LimitReader(response.Body, 64*1024)
		errorBody, _ := io.ReadAll(limited)
		return InvocationResult{}, fmt.Errorf("Azure OpenAI Responses stream returned HTTP %d: %s", response.StatusCode, sdk.RedactSecret(string(errorBody)))
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "text/event-stream") {
		return InvocationResult{}, fmt.Errorf("Azure OpenAI Responses stream returned an invalid content type")
	}
	requestID := responseRequestID(response.Header)
	written, err := readAzureOpenAIResponsesStream(streamContext, response.Body, plan, sink)
	if err != nil {
		if streamContext.Err() == context.DeadlineExceeded && written > 0 {
			// The caller-selected finite observation window is a normal terminal condition.
		} else {
			return InvocationResult{}, err
		}
	}
	if written == 0 {
		return InvocationResult{}, fmt.Errorf("Azure OpenAI Responses stream produced no events before termination")
	}
	output, err := sink.finish(requestID)
	if err != nil {
		return InvocationResult{}, err
	}
	return InvocationResult{Output: output, RequestID: requestID}, nil
}

func readAzureOpenAIResponsesStream(ctx context.Context, source io.Reader, plan azureOpenAIResponsesStreamPlan, sink *cloudWebSocketOutputSink) (int, error) {
	scanner := bufio.NewScanner(source)
	scanner.Buffer(make([]byte, 64*1024), azureOpenAIResponsesStreamMaxChunkBytes+1)
	var dataLines [][]byte
	dataBytes := 0
	written := 0
	done := false
	dispatch := func() error {
		if len(dataLines) == 0 {
			return nil
		}
		joined := bytes.Join(dataLines, []byte{'\n'})
		dataLines = nil
		dataBytes = 0
		trimmed := bytes.TrimSpace(joined)
		if len(trimmed) == 0 {
			return nil
		}
		eventType, terminal, err := canonicalAzureOpenAIResponsesEvent(trimmed)
		if err != nil {
			return err
		}
		if terminal == "failed" {
			return fmt.Errorf("Azure OpenAI Responses stream terminated with a provider error event")
		}
		if err := sink.writeMessage(eventType); err != nil {
			return err
		}
		written++
		if terminal != "" {
			done = true
		}
		return nil
	}
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			if err := dispatch(); err != nil {
				return written, err
			}
			if done || written >= plan.MaxEvents {
				return written, nil
			}
			continue
		}
		if line[0] == ':' {
			continue
		}
		field, value, found := bytes.Cut(line, []byte{':'})
		if !found {
			return written, fmt.Errorf("Azure OpenAI Responses returned an invalid SSE field")
		}
		if len(value) > 0 && value[0] == ' ' {
			value = value[1:]
		}
		if string(field) != "data" {
			return written, fmt.Errorf("Azure OpenAI Responses returned an unsupported SSE field")
		}
		dataLines = append(dataLines, append([]byte(nil), value...))
		dataBytes += len(value)
		if dataBytes > azureOpenAIResponsesStreamMaxChunkBytes {
			return written, fmt.Errorf("Azure OpenAI Responses event exceeds %d bytes", azureOpenAIResponsesStreamMaxChunkBytes)
		}
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return written, ctx.Err()
		}
		return written, fmt.Errorf("read Azure OpenAI Responses event stream")
	}
	if err := dispatch(); err != nil {
		return written, err
	}
	if done || written >= plan.MaxEvents {
		return written, nil
	}
	return written, fmt.Errorf("Azure OpenAI Responses stream ended before a terminal response event")
}

// canonicalAzureOpenAIResponsesEvent validates one SSE data payload against the
// documented ResponseStreamEvent envelope and returns the canonical re-marshaled
// event plus the terminal kind ("", "completed", "incomplete", or "failed").
// Credential-bearing, unknown, oversized, or malformed events fail closed.
func canonicalAzureOpenAIResponsesEvent(data []byte) ([]byte, string, error) {
	if len(data) == 0 || len(data) > azureOpenAIResponsesStreamMaxChunkBytes || !json.Valid(data) {
		return nil, "", fmt.Errorf("Azure OpenAI Responses returned an invalid event payload")
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, "", fmt.Errorf("Azure OpenAI Responses returned invalid event JSON")
	}
	if azureRealtimeContainsCredentialField(raw) {
		return nil, "", fmt.Errorf("Azure OpenAI Responses returned a credential-bearing event")
	}
	eventType, ok := raw["type"].(string)
	if !ok || eventType == "" || !azureOpenAIResponsesStreamEventTypes[eventType] {
		return nil, "", fmt.Errorf("Azure OpenAI Responses returned an unsupported stream event")
	}
	// The official Azure OpenAI error event sample omits sequence_number, so
	// validate it only when the provider includes it; every response.* event
	// requires the SDK-contract sequence number.
	if err := validateAzureOpenAIResponsesSequenceNumber(raw, eventType != "error"); err != nil {
		return nil, "", err
	}
	terminal := ""
	switch eventType {
	case "error":
		if err := validateAzureOpenAIResponsesErrorEvent(raw); err != nil {
			return nil, "", err
		}
		terminal = "failed"
	case "response.output_text.delta":
		if err := validateAzureOpenAIResponsesTextDelta(raw); err != nil {
			return nil, "", err
		}
	case "response.output_text.done":
		text, _ := raw["text"].(string)
		if len(text) == 0 || len(text) > azureOpenAIResponsesStreamMaxMessageBytes {
			return nil, "", fmt.Errorf("Azure OpenAI Responses returned an invalid output_text.done text")
		}
	case "response.completed", "response.incomplete", "response.failed":
		if err := validateAzureOpenAIResponsesTerminalResponse(raw); err != nil {
			return nil, "", err
		}
		switch eventType {
		case "response.completed":
			terminal = "completed"
		case "response.incomplete":
			terminal = "incomplete"
		case "response.failed":
			terminal = "failed"
		}
	default:
		if err := validateAzureOpenAIResponsesGenericEvent(raw); err != nil {
			return nil, "", err
		}
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, "", fmt.Errorf("encode Azure OpenAI Responses event")
	}
	return encoded, terminal, nil
}

func validateAzureOpenAIResponsesSequenceNumber(raw map[string]any, required bool) error {
	sequence, ok := raw["sequence_number"].(float64)
	if !ok {
		if required {
			return fmt.Errorf("Azure OpenAI Responses returned an event without sequence_number")
		}
		return nil
	}
	if sequence < 0 || sequence != float64(int64(sequence)) || int64(sequence) > 1_000_000_000 {
		return fmt.Errorf("Azure OpenAI Responses returned an invalid sequence_number")
	}
	return nil
}

func validateAzureOpenAIResponsesTextDelta(raw map[string]any) error {
	delta, ok := raw["delta"].(string)
	if !ok || len(delta) == 0 || len(delta) > azureOpenAIResponsesStreamMaxMessageBytes {
		return fmt.Errorf("Azure OpenAI Responses returned an invalid output_text.delta")
	}
	for _, field := range []string{"output_index", "content_index"} {
		value, exists := raw[field].(float64)
		if !exists || value < 0 || value != float64(int64(value)) || int64(value) > 1_000_000 {
			return fmt.Errorf("Azure OpenAI Responses returned an invalid %s", field)
		}
	}
	itemID, _ := raw["item_id"].(string)
	if !azureOpenAIResponsesItemIDPattern.MatchString(itemID) {
		return fmt.Errorf("Azure OpenAI Responses returned an invalid output_text.delta item_id")
	}
	return nil
}

func validateAzureOpenAIResponsesErrorEvent(raw map[string]any) error {
	// Official Azure OpenAI streams surface mid-stream errors as either the
	// OpenAI SDK flat shape (code/message/param) or the documented nested
	// {"type":"error","error":{...}} shape.
	message := ""
	if nested, ok := raw["error"].(map[string]any); ok {
		if nestedMessage, ok := nested["message"].(string); ok {
			message = nestedMessage
		}
		if nestedType, ok := nested["type"].(string); ok && len(nestedType) > 128 {
			return fmt.Errorf("Azure OpenAI Responses returned an oversized error type")
		}
		if nestedCode, ok := nested["code"].(string); ok && len(nestedCode) > 256 {
			return fmt.Errorf("Azure OpenAI Responses returned an oversized error code")
		}
	} else if flat, ok := raw["message"].(string); ok {
		message = flat
	}
	if len(message) == 0 || len(message) > 16*1024 {
		return fmt.Errorf("Azure OpenAI Responses returned an invalid error message")
	}
	return nil
}

func validateAzureOpenAIResponsesTerminalResponse(raw map[string]any) error {
	response, ok := raw["response"].(map[string]any)
	if !ok {
		return fmt.Errorf("Azure OpenAI Responses returned a terminal event without a response object")
	}
	responseID, _ := response["id"].(string)
	if len(responseID) == 0 || len(responseID) > 256 {
		return fmt.Errorf("Azure OpenAI Responses returned a terminal event with an invalid response id")
	}
	if object, _ := response["object"].(string); object != "response" {
		return fmt.Errorf("Azure OpenAI Responses returned a terminal event with an invalid response object")
	}
	return nil
}

func validateAzureOpenAIResponsesGenericEvent(raw map[string]any) error {
	for _, field := range []string{"item_id", "output_id"} {
		if value, exists := raw[field]; exists {
			itemID, ok := value.(string)
			if !ok || !azureOpenAIResponsesItemIDPattern.MatchString(itemID) {
				return fmt.Errorf("Azure OpenAI Responses returned an invalid %s", field)
			}
		}
	}
	for _, field := range []string{"delta", "text", "summary_text", "refusal", "reasoning_text", "input"} {
		if value, exists := raw[field]; exists {
			text, ok := value.(string)
			if !ok || len(text) > azureOpenAIResponsesStreamMaxMessageBytes {
				return fmt.Errorf("Azure OpenAI Responses returned an invalid %s", field)
			}
		}
	}
	return nil
}

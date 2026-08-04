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
	authSchemeAzureOpenAIChatStream = "openai-chat-stream"

	azureOpenAIChatStreamScope = "https://cognitiveservices.azure.com/.default"

	azureOpenAIChatStreamMaxEvents         = 256
	azureOpenAIChatStreamMaxTimeoutSeconds = 300
	azureOpenAIChatStreamMaxMessages       = 128
	azureOpenAIChatStreamMaxMessageBytes   = 64 * 1024
	azureOpenAIChatStreamMaxChunkBytes     = 1024 * 1024
	azureOpenAIChatStreamMaxTokens         = 1_000_000
)

var (
	azureOpenAIChatStreamAPIVersionPattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}(?:-[a-z0-9]+)?$`)
	azureOpenAIChatDeploymentPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	azureOpenAIChatNamePattern             = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

type azureOpenAIChatStreamPlan struct {
	Model          string                         `json:"model"`
	Messages       []azureOpenAIChatStreamMessage `json:"messages"`
	MaxTokens      *int                           `json:"max_tokens,omitempty"`
	Temperature    *float64                       `json:"temperature,omitempty"`
	MaxEvents      int                            `json:"max_events"`
	TimeoutSeconds int                            `json:"timeout_seconds"`
}

type azureOpenAIChatStreamMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
	Name    string `json:"name,omitempty"`
}

type azureOpenAIChatChunk struct {
	ID      string                       `json:"id"`
	Object  string                       `json:"object"`
	Created int64                        `json:"created"`
	Model   string                       `json:"model"`
	Choices []azureOpenAIChatChunkChoice `json:"choices"`
}

type azureOpenAIChatChunkChoice struct {
	Index        int                       `json:"index"`
	Delta        azureOpenAIChatChunkDelta `json:"delta"`
	FinishReason any                       `json:"finish_reason"`
}

type azureOpenAIChatChunkDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

func validateAzureOpenAIChatStreamInvocation(invocation Invocation) error {
	if invocation.Mode != ModeRead || !strings.EqualFold(invocation.Method, http.MethodPost) ||
		!strings.EqualFold(invocation.Service, "openai") || !strings.EqualFold(invocation.Operation, "StreamChatCompletions") {
		return fmt.Errorf("Azure OpenAI Chat Completions streaming requires the read tool, POST, service openai, and operation StreamChatCompletions")
	}
	_, deployment, err := parseAzureOpenAIChatStreamTarget(invocation.URL)
	if err != nil {
		return err
	}
	if !azureOpenAIChatStreamAPIVersionPattern.MatchString(invocation.APIVersion) {
		return fmt.Errorf("Azure OpenAI Chat Completions streaming requires a date-structured api_version such as 2024-06-01")
	}
	if len(invocation.Parameters) != 0 {
		return fmt.Errorf("Azure OpenAI Chat Completions streaming does not accept caller query parameters")
	}
	if len(invocation.Headers) != 0 {
		return fmt.Errorf("Azure OpenAI Chat Completions streaming does not accept caller headers")
	}
	if invocation.Body == nil || invocation.BodyFile != "" || invocation.ImageFile != "" || invocation.ProtobufDescriptorFile != "" || invocation.ResponseFile == "" {
		return fmt.Errorf("Azure OpenAI Chat Completions streaming requires a finite body plan and response_file; input files are forbidden")
	}
	if invocation.Region != "" || invocation.RegionSet != "" || invocation.Project != "" || invocation.Subscription != "" || invocation.Audience != "" ||
		invocation.AuthVersion != "" || invocation.PayloadMode != "" || invocation.ChecksumAlgorithm != "" ||
		invocation.StreamChunkBytes != 0 || invocation.StreamIntervalMS != 0 || invocation.StreamMaxMessages != 0 || invocation.StreamTimeoutSeconds != 0 ||
		invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return fmt.Errorf("Azure OpenAI Chat Completions streaming does not accept REST, cross-provider, or stream transport controls")
	}
	plan, err := parseAzureOpenAIChatStreamPlan(invocation.Body)
	if err != nil {
		return err
	}
	if plan.Model != deployment {
		return fmt.Errorf("Azure OpenAI Chat Completions body model must equal the deployment in the URL path")
	}
	return nil
}

func parseAzureOpenAIChatStreamTarget(rawURL string) (*url.URL, string, error) {
	target, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(target.Scheme, "https") || target.Hostname() == "" || target.User != nil ||
		target.Fragment != "" || target.RawQuery != "" {
		return nil, "", fmt.Errorf("Azure OpenAI Chat Completions streaming requires an exact credential-free query-free https:// URL")
	}
	if target.Port() != "" && target.Port() != "443" {
		return nil, "", fmt.Errorf("Azure OpenAI Chat Completions streaming endpoint port must be 443")
	}
	const publicSuffix = ".openai.azure.com"
	host := strings.ToLower(target.Hostname())
	resource := strings.TrimSuffix(host, publicSuffix)
	if resource == host || resource == "privatelink" || !endpointLabelPattern.MatchString(resource) {
		return nil, "", fmt.Errorf("Azure OpenAI Chat Completions streaming requires an exact public-cloud resource.openai.azure.com host")
	}
	const prefix = "/openai/deployments/"
	path := target.EscapedPath()
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, "/chat/completions") {
		return nil, "", fmt.Errorf("Azure OpenAI Chat Completions streaming requires the official /openai/deployments/<deployment>/chat/completions path")
	}
	deployment := strings.TrimSuffix(strings.TrimPrefix(path, prefix), "/chat/completions")
	if !azureOpenAIChatDeploymentPattern.MatchString(deployment) {
		return nil, "", fmt.Errorf("Azure OpenAI Chat Completions deployment name is invalid")
	}
	return target, deployment, nil
}

func parseAzureOpenAIChatStreamPlan(body any) (azureOpenAIChatStreamPlan, error) {
	if body == nil || azureRealtimeContainsCredentialField(body) {
		return azureOpenAIChatStreamPlan{}, fmt.Errorf("Azure OpenAI Chat Completions streaming requires a credential-free finite plan")
	}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) == 0 || len(encoded) > maxRequestPayloadBytes {
		return azureOpenAIChatStreamPlan{}, fmt.Errorf("Azure OpenAI Chat Completions plan must be bounded JSON")
	}
	var plan azureOpenAIChatStreamPlan
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil || ensureJSONDecoderEOF(decoder) != nil {
		return azureOpenAIChatStreamPlan{}, fmt.Errorf("Azure OpenAI Chat Completions body does not match the finite chat plan schema")
	}
	if !azureOpenAIChatDeploymentPattern.MatchString(plan.Model) {
		return azureOpenAIChatStreamPlan{}, fmt.Errorf("Azure OpenAI Chat Completions model must be one bounded deployment name")
	}
	if len(plan.Messages) < 1 || len(plan.Messages) > azureOpenAIChatStreamMaxMessages {
		return azureOpenAIChatStreamPlan{}, fmt.Errorf("Azure OpenAI Chat Completions messages must be between 1 and %d", azureOpenAIChatStreamMaxMessages)
	}
	for index := range plan.Messages {
		if err := validateAzureOpenAIChatStreamMessage(plan.Messages[index]); err != nil {
			return azureOpenAIChatStreamPlan{}, fmt.Errorf("message %d: %w", index, err)
		}
	}
	if plan.MaxTokens != nil && (*plan.MaxTokens < 1 || *plan.MaxTokens > azureOpenAIChatStreamMaxTokens) {
		return azureOpenAIChatStreamPlan{}, fmt.Errorf("Azure OpenAI Chat Completions max_tokens must be between 1 and %d", azureOpenAIChatStreamMaxTokens)
	}
	if plan.Temperature != nil && (*plan.Temperature < 0 || *plan.Temperature > 2) {
		return azureOpenAIChatStreamPlan{}, fmt.Errorf("Azure OpenAI Chat Completions temperature must be between 0 and 2")
	}
	if plan.MaxEvents < 1 || plan.MaxEvents > azureOpenAIChatStreamMaxEvents {
		return azureOpenAIChatStreamPlan{}, fmt.Errorf("Azure OpenAI Chat Completions max_events must be between 1 and %d", azureOpenAIChatStreamMaxEvents)
	}
	if plan.TimeoutSeconds < 1 || plan.TimeoutSeconds > azureOpenAIChatStreamMaxTimeoutSeconds {
		return azureOpenAIChatStreamPlan{}, fmt.Errorf("Azure OpenAI Chat Completions timeout_seconds must be between 1 and %d", azureOpenAIChatStreamMaxTimeoutSeconds)
	}
	return plan, nil
}

func validateAzureOpenAIChatStreamMessage(message azureOpenAIChatStreamMessage) error {
	switch message.Role {
	case "system", "user", "assistant", "developer":
	default:
		return fmt.Errorf("role must be system, user, assistant, or developer")
	}
	if message.Name != "" && !azureOpenAIChatNamePattern.MatchString(message.Name) {
		return fmt.Errorf("name is invalid")
	}
	return validateAzureOpenAIChatStreamContent(message.Content)
}

func validateAzureOpenAIChatStreamContent(content any) error {
	switch typed := content.(type) {
	case string:
		if len(typed) == 0 || len(typed) > azureOpenAIChatStreamMaxMessageBytes {
			return fmt.Errorf("content must be between 1 and %d bytes", azureOpenAIChatStreamMaxMessageBytes)
		}
		return nil
	case []any:
		if len(typed) < 1 || len(typed) > 16 {
			return fmt.Errorf("content parts must be between 1 and 16")
		}
		for partIndex, raw := range typed {
			part, ok := raw.(map[string]any)
			if !ok || len(part) != 2 {
				return fmt.Errorf("content part %d must be a text object", partIndex)
			}
			partType, typeOK := part["type"].(string)
			text, textOK := part["text"].(string)
			if !typeOK || !textOK || partType != "text" || len(text) == 0 || len(text) > azureOpenAIChatStreamMaxMessageBytes {
				return fmt.Errorf("content part %d must be one bounded text part", partIndex)
			}
		}
		return nil
	default:
		return fmt.Errorf("content must be a bounded string or an array of text parts")
	}
}

func buildAzureOpenAIChatStreamRequest(plan azureOpenAIChatStreamPlan) ([]byte, error) {
	body := map[string]any{
		"model":    plan.Model,
		"messages": plan.Messages,
		"stream":   true,
	}
	if plan.MaxTokens != nil {
		body["max_tokens"] = *plan.MaxTokens
	}
	if plan.Temperature != nil {
		body["temperature"] = *plan.Temperature
	}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) == 0 || len(encoded) > maxRequestPayloadBytes {
		return nil, fmt.Errorf("encode Azure OpenAI Chat Completions stream request")
	}
	return encoded, nil
}

func invokeAzureOpenAIChatStream(ctx context.Context, adapter *AzureRESTAdapter, invocation Invocation) (InvocationResult, error) {
	if err := validateAzureOpenAIChatStreamInvocation(invocation); err != nil {
		return InvocationResult{}, err
	}
	target, _, _ := parseAzureOpenAIChatStreamTarget(invocation.URL)
	plan, _ := parseAzureOpenAIChatStreamPlan(invocation.Body)
	sink, err := newWebSocketOutputSink(invocation, adapter.config.MaxBodyBytes, "Azure OpenAI Chat Completions streaming")
	if err != nil {
		return InvocationResult{}, err
	}
	defer sink.abort()
	token, err := adapter.config.Tokens.Token(ctx, azureOpenAIChatStreamScope)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("load Azure OpenAI Chat Completions identity token: %w", err)
	}
	if strings.TrimSpace(token) == "" {
		return InvocationResult{}, fmt.Errorf("Azure identity returned an empty access token")
	}
	requestBody, err := buildAzureOpenAIChatStreamRequest(plan)
	if err != nil {
		return InvocationResult{}, err
	}
	query := target.Query()
	query.Set("api-version", invocation.APIVersion)
	target.RawQuery = query.Encode()
	streamContext, cancel := context.WithTimeout(ctx, time.Duration(plan.TimeoutSeconds)*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(streamContext, http.MethodPost, target.String(), bytes.NewReader(requestBody))
	if err != nil {
		return InvocationResult{}, fmt.Errorf("build Azure OpenAI Chat Completions request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Authorization", "Bearer "+token)
	request.ContentLength = int64(len(requestBody))
	response, err := adapter.config.HTTP.Do(request)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("Azure OpenAI Chat Completions stream request: %w", err)
	}
	if response == nil || response.Body == nil {
		return InvocationResult{}, fmt.Errorf("Azure OpenAI Chat Completions stream returned an empty response")
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		limited := io.LimitReader(response.Body, 64*1024)
		errorBody, _ := io.ReadAll(limited)
		return InvocationResult{}, fmt.Errorf("Azure OpenAI Chat Completions stream returned HTTP %d: %s", response.StatusCode, sdk.RedactSecret(string(errorBody)))
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "text/event-stream") {
		return InvocationResult{}, fmt.Errorf("Azure OpenAI Chat Completions stream returned an invalid content type")
	}
	requestID := responseRequestID(response.Header)
	written, err := readAzureOpenAIChatStream(streamContext, response.Body, plan, sink)
	if err != nil {
		if streamContext.Err() == context.DeadlineExceeded && written > 0 {
			// The caller-selected finite observation window is a normal terminal condition.
		} else {
			return InvocationResult{}, err
		}
	}
	if written == 0 {
		return InvocationResult{}, fmt.Errorf("Azure OpenAI Chat Completions stream produced no chunk events before termination")
	}
	output, err := sink.finish(requestID)
	if err != nil {
		return InvocationResult{}, err
	}
	return InvocationResult{Output: output, RequestID: requestID}, nil
}

func readAzureOpenAIChatStream(ctx context.Context, source io.Reader, plan azureOpenAIChatStreamPlan, sink *cloudWebSocketOutputSink) (int, error) {
	scanner := bufio.NewScanner(source)
	scanner.Buffer(make([]byte, 64*1024), azureOpenAIChatStreamMaxChunkBytes+1)
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
		if string(trimmed) == "[DONE]" {
			done = true
			return nil
		}
		encoded, err := canonicalAzureOpenAIChatChunk(trimmed)
		if err != nil {
			return err
		}
		if err := sink.writeMessage(encoded); err != nil {
			return err
		}
		written++
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
			return written, fmt.Errorf("Azure OpenAI Chat Completions returned an invalid SSE field")
		}
		if len(value) > 0 && value[0] == ' ' {
			value = value[1:]
		}
		if string(field) != "data" {
			return written, fmt.Errorf("Azure OpenAI Chat Completions returned an unsupported SSE field")
		}
		dataLines = append(dataLines, append([]byte(nil), value...))
		dataBytes += len(value)
		if dataBytes > azureOpenAIChatStreamMaxChunkBytes {
			return written, fmt.Errorf("Azure OpenAI Chat Completions chunk exceeds %d bytes", azureOpenAIChatStreamMaxChunkBytes)
		}
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return written, ctx.Err()
		}
		return written, fmt.Errorf("read Azure OpenAI Chat Completions event stream")
	}
	if err := dispatch(); err != nil {
		return written, err
	}
	if done || written >= plan.MaxEvents {
		return written, nil
	}
	return written, fmt.Errorf("Azure OpenAI Chat Completions stream ended before the [DONE] sentinel")
}

func canonicalAzureOpenAIChatChunk(data []byte) ([]byte, error) {
	if len(data) == 0 || len(data) > azureOpenAIChatStreamMaxChunkBytes || !json.Valid(data) {
		return nil, fmt.Errorf("Azure OpenAI Chat Completions returned an invalid chunk payload")
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("Azure OpenAI Chat Completions returned invalid chunk JSON")
	}
	if object, _ := raw["object"].(string); object != "chat.completion.chunk" {
		return nil, fmt.Errorf("Azure OpenAI Chat Completions returned an unsupported stream event")
	}
	if azureRealtimeContainsCredentialField(raw) {
		return nil, fmt.Errorf("Azure OpenAI Chat Completions returned a credential-bearing chunk")
	}
	var chunk azureOpenAIChatChunk
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&chunk); err != nil || ensureJSONDecoderEOF(decoder) != nil {
		return nil, fmt.Errorf("Azure OpenAI Chat Completions returned a chunk outside the documented shape")
	}
	if chunk.ID == "" || len(chunk.ID) > 256 || chunk.Object != "chat.completion.chunk" ||
		chunk.Created <= 0 || chunk.Model == "" || len(chunk.Model) > 256 || len(chunk.Choices) == 0 {
		return nil, fmt.Errorf("Azure OpenAI Chat Completions returned a malformed chunk header")
	}
	for index := range chunk.Choices {
		choice := &chunk.Choices[index]
		if choice.Index < 0 || choice.Index > 1_000_000 {
			return nil, fmt.Errorf("Azure OpenAI Chat Completions returned an invalid chunk choice index")
		}
		if choice.Delta.Role != "" && choice.Delta.Role != "assistant" {
			return nil, fmt.Errorf("Azure OpenAI Chat Completions returned an invalid chunk role")
		}
		if len(choice.Delta.Content) > azureOpenAIChatStreamMaxMessageBytes {
			return nil, fmt.Errorf("Azure OpenAI Chat Completions chunk content exceeds %d bytes", azureOpenAIChatStreamMaxMessageBytes)
		}
		if choice.FinishReason != nil {
			finish, ok := choice.FinishReason.(string)
			if !ok || finish == "" || len(finish) > 64 {
				return nil, fmt.Errorf("Azure OpenAI Chat Completions returned an invalid finish_reason")
			}
		}
	}
	encoded, err := json.Marshal(chunk)
	if err != nil {
		return nil, fmt.Errorf("encode Azure OpenAI Chat Completions chunk")
	}
	return encoded, nil
}

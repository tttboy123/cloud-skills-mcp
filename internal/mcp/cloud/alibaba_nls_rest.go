package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

const (
	alibabaNLSRESTASROperation = "shortsentencerecognition"
	alibabaNLSRESTTTSOperation = "speechsynthesisrest"
	alibabaNLSRESTMaxTTSChars  = 300
)

func validateAlibabaNLSRESTInvocation(invocation Invocation) error {
	if !strings.EqualFold(invocation.Service, "nls") {
		return fmt.Errorf("Alibaba Cloud NLS REST requires service nls")
	}
	operation := strings.ToLower(strings.TrimSpace(invocation.Operation))
	if operation != alibabaNLSRESTASROperation && operation != alibabaNLSRESTTTSOperation {
		return fmt.Errorf("Alibaba Cloud NLS REST operation must be ShortSentenceRecognition or SpeechSynthesisREST")
	}
	target, err := url.Parse(invocation.URL)
	if err != nil || !strings.EqualFold(target.Scheme, "https") || target.User != nil || target.Fragment != "" || target.RawQuery != "" || target.Port() != "" || !isAlibabaNLSPublicGatewayHost(target.Hostname()) {
		return fmt.Errorf("Alibaba Cloud NLS REST requires an official public HTTPS nls-gateway URL without inline query parameters")
	}
	if len(invocation.Headers) != 0 {
		return fmt.Errorf("Alibaba Cloud NLS REST does not accept caller-supplied headers")
	}
	if alibabaNLSContainsCredentialField(invocation.Parameters) {
		return fmt.Errorf("Alibaba Cloud NLS REST parameters cannot contain credential fields")
	}
	if invocation.Region != "" || invocation.RegionSet != "" || invocation.Project != "" || invocation.Subscription != "" || invocation.Audience != "" || invocation.APIVersion != "" || invocation.AuthVersion != "" || invocation.PayloadMode != "" || invocation.ChecksumAlgorithm != "" || invocation.StreamChunkBytes != 0 || invocation.StreamIntervalMS != 0 || invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return fmt.Errorf("Alibaba Cloud NLS REST does not accept cross-provider, streaming, or provider-specific controls")
	}
	if operation == alibabaNLSRESTASROperation {
		if target.EscapedPath() != "/stream/v1/asr" || !strings.EqualFold(invocation.Method, http.MethodPost) {
			return fmt.Errorf("Alibaba Cloud NLS short recognition requires POST /stream/v1/asr")
		}
		if invocation.BodyFile == "" || invocation.Body != nil || invocation.ResponseFile != "" {
			return fmt.Errorf("Alibaba Cloud NLS short recognition requires only body_file audio and an inline JSON response")
		}
		if _, err := alibabaNLSRESTRequiredString(invocation.Parameters, "appkey"); err != nil {
			return err
		}
		return nil
	}
	if target.EscapedPath() != "/stream/v1/tts" || invocation.ResponseFile == "" || invocation.BodyFile != "" {
		return fmt.Errorf("Alibaba Cloud NLS speech synthesis requires /stream/v1/tts and response_file")
	}
	switch strings.ToUpper(strings.TrimSpace(invocation.Method)) {
	case http.MethodGet:
		if invocation.Body != nil {
			return fmt.Errorf("Alibaba Cloud NLS TTS GET accepts parameters only")
		}
		if _, err := alibabaNLSRESTRequiredString(invocation.Parameters, "appkey"); err != nil {
			return err
		}
		text, err := alibabaNLSRESTRequiredString(invocation.Parameters, "text")
		if err != nil {
			return err
		}
		if err := validateAlibabaNLSRESTTTSText(text); err != nil {
			return err
		}
	case http.MethodPost:
		if len(invocation.Parameters) != 0 {
			return fmt.Errorf("Alibaba Cloud NLS TTS POST accepts its fields only in the JSON body")
		}
		body, ok := invocation.Body.(map[string]any)
		if !ok || alibabaNLSContainsCredentialField(body) {
			return fmt.Errorf("Alibaba Cloud NLS TTS POST requires a credential-free JSON object")
		}
		if _, err := alibabaNLSRESTRequiredString(body, "appkey"); err != nil {
			return err
		}
		text, err := alibabaNLSRESTRequiredString(body, "text")
		if err != nil {
			return err
		}
		if err := validateAlibabaNLSRESTTTSText(text); err != nil {
			return err
		}
		if err := validateAlibabaNLSPayloadSize(body); err != nil {
			return err
		}
	default:
		return fmt.Errorf("Alibaba Cloud NLS TTS requires method GET or POST")
	}
	return nil
}

func validateAlibabaNLSRESTTTSText(text string) error {
	if !utf8.ValidString(text) || utf8.RuneCountInString(text) > alibabaNLSRESTMaxTTSChars {
		return fmt.Errorf("Alibaba Cloud NLS REST TTS text must be valid UTF-8 and at most %d characters", alibabaNLSRESTMaxTTSChars)
	}
	return nil
}

func alibabaNLSRESTRequiredString(values map[string]any, name string) (string, error) {
	raw, ok := values[name]
	if !ok {
		return "", fmt.Errorf("Alibaba Cloud NLS REST requires %s", name)
	}
	items, err := stringValues(raw)
	if err != nil || len(items) != 1 || strings.TrimSpace(items[0]) == "" || strings.TrimSpace(items[0]) != items[0] || len(items[0]) > maxRequestPayloadBytes {
		return "", fmt.Errorf("Alibaba Cloud NLS REST %s must be one bounded non-empty string", name)
	}
	return items[0], nil
}

func invokeAlibabaNLSREST(ctx context.Context, adapter *AlibabaRESTAdapter, invocation Invocation) (InvocationResult, error) {
	if err := validateAlibabaNLSRESTInvocation(invocation); err != nil {
		return InvocationResult{}, err
	}
	operation := strings.ToLower(strings.TrimSpace(invocation.Operation))
	if invocation.Headers == nil {
		invocation.Headers = make(map[string]string)
	}
	if operation == alibabaNLSRESTASROperation {
		invocation.Headers["Content-Type"] = "application/octet-stream"
	} else if strings.EqualFold(invocation.Method, http.MethodPost) {
		invocation.Headers["Content-Type"] = "application/json"
	}
	request, _, cleanup, err := buildSignedHTTPRequest(ctx, invocation)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("build Alibaba Cloud NLS REST request: %w", err)
	}
	defer cleanup()
	token, err := adapter.loadAlibabaNLSToken(ctx)
	if err != nil {
		return InvocationResult{}, err
	}
	request.Header.Set("X-NLS-Token", token)
	response, err := adapter.config.HTTP.Do(request)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("Alibaba Cloud NLS REST request failed")
	}
	if operation == alibabaNLSRESTTTSOperation && response != nil && !strings.HasPrefix(strings.ToLower(strings.TrimSpace(response.Header.Get("Content-Type"))), "audio/") {
		if response.Body != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, adapter.config.MaxBodyBytes+1))
			_ = response.Body.Close()
		}
		return InvocationResult{}, fmt.Errorf("Alibaba Cloud NLS speech synthesis returned an error response")
	}
	output, err := readRESTResponseWithFile(response, adapter.config.MaxBodyBytes, invocation.ResponseFile, invocation.MaxResponseFileBytes)
	if err != nil {
		return InvocationResult{}, err
	}
	if operation == alibabaNLSRESTASROperation {
		var result struct {
			TaskID string `json:"task_id"`
			Status int64  `json:"status"`
		}
		if json.Unmarshal(output, &result) != nil || result.Status != alibabaNLSSuccessStatus || strings.TrimSpace(result.TaskID) == "" {
			return InvocationResult{}, fmt.Errorf("Alibaba Cloud NLS short recognition returned an unsuccessful result")
		}
		return InvocationResult{Output: output, RequestID: result.TaskID}, nil
	}
	return InvocationResult{Output: output, RequestID: responseRequestID(response.Header)}, nil
}

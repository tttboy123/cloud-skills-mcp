package cloud

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	authSchemeBaiduIoTCoreHTTPPub = "iotcore-http-pub"
	baiduIoTCoreHTTPPubOperation  = "publishhttp"
	baiduIoTCoreHTTPTokenSeconds  = 60
	baiduIoTCoreHTTPMaxAuthBytes  = 8 * 1024
	baiduIoTCoreHTTPMaxTokenBytes = 4096
	baiduIoTCoreHTTPPubInterval   = 20 * time.Millisecond
)

type baiduIoTCoreHTTPPubPlan struct {
	IoTCoreID       string  `json:"iot_core_id,omitempty"`
	Topic           string  `json:"topic"`
	QoS             int     `json:"qos"`
	PayloadBase64   *string `json:"payload_base64,omitempty"`
	MaxPayloadBytes int     `json:"max_payload_bytes,omitempty"`
	payload         []byte
}

func validateBaiduIoTCoreHTTPPubInvocation(invocation Invocation) error {
	_, _, err := parseBaiduIoTCoreHTTPPubInvocation(invocation)
	return err
}

func parseBaiduIoTCoreHTTPPubInvocation(invocation Invocation) (*url.URL, baiduIoTCoreHTTPPubPlan, error) {
	if !strings.EqualFold(strings.TrimSpace(invocation.Service), "iotcore") || strings.ToLower(strings.TrimSpace(invocation.Operation)) != baiduIoTCoreHTTPPubOperation {
		return nil, baiduIoTCoreHTTPPubPlan{}, fmt.Errorf("Baidu IoT Core HTTP publish requires service=iotcore and operation=PublishHTTP")
	}
	if invocation.Mode != ModeMutate {
		return nil, baiduIoTCoreHTTPPubPlan{}, fmt.Errorf("Baidu IoT Core HTTP publish is mutation-only")
	}
	if !strings.EqualFold(strings.TrimSpace(invocation.Method), http.MethodPost) {
		return nil, baiduIoTCoreHTTPPubPlan{}, fmt.Errorf("Baidu IoT Core HTTP publish requires method=POST")
	}
	target, iotCoreID, err := parseBaiduIoTCoreHTTPDataEndpoint(invocation.URL, "/pub")
	if err != nil {
		return nil, baiduIoTCoreHTTPPubPlan{}, err
	}
	if len(invocation.Parameters) != 0 || len(invocation.Headers) != 0 || invocation.ResponseFile != "" {
		return nil, baiduIoTCoreHTTPPubPlan{}, fmt.Errorf("Baidu IoT Core HTTP publish accepts only a credential-free protocol body and optional body_file")
	}
	if invocation.Region != "" || invocation.RegionSet != "" || invocation.Project != "" || invocation.Subscription != "" || invocation.Audience != "" || invocation.RegistryInstanceID != "" || invocation.RegistryUserID != "" || invocation.ACRScope != "" || invocation.ACRSourceScope != "" || invocation.AuthVersion != "" || invocation.APIVersion != "" || invocation.PayloadMode != "" || invocation.ChecksumAlgorithm != "" || invocation.StreamChunkBytes != 0 || invocation.StreamIntervalMS != 0 || invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return nil, baiduIoTCoreHTTPPubPlan{}, fmt.Errorf("Baidu IoT Core HTTP publish does not accept REST, cross-provider, Registry, or stream controls")
	}
	plan, err := parseBaiduIoTCoreHTTPPubPlan(invocation.Body, invocation.BodyFile != "", iotCoreID)
	if err != nil {
		return nil, baiduIoTCoreHTTPPubPlan{}, err
	}
	return target, plan, nil
}

func parseBaiduIoTCoreHTTPDataEndpoint(rawURL, path string) (*url.URL, string, error) {
	target, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(target.Scheme, "https") || target.User != nil || target.Fragment != "" || target.RawQuery != "" || target.Port() != "" || target.EscapedPath() != path {
		return nil, "", fmt.Errorf("Baidu IoT Core HTTP publish requires an exact https://<iot-core-id>.iot.gz.baidubce.com/pub endpoint on port 443")
	}
	host := strings.ToLower(target.Hostname())
	suffix := ".iot.gz.baidubce.com"
	iotCoreID := strings.TrimSuffix(host, suffix)
	if iotCoreID == host || strings.Contains(iotCoreID, ".") || !baiduIoTCoreIDPattern.MatchString(iotCoreID) {
		return nil, "", fmt.Errorf("Baidu IoT Core HTTP publish requires an exact single-instance official endpoint")
	}
	return target, iotCoreID, nil
}

func parseBaiduIoTCoreHTTPPubPlan(body any, bodyFile bool, endpointID string) (baiduIoTCoreHTTPPubPlan, error) {
	if body == nil || alibabaNLSContainsCredentialField(body) {
		return baiduIoTCoreHTTPPubPlan{}, fmt.Errorf("Baidu IoT Core HTTP publish requires a credential-free protocol body")
	}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) == 0 || len(encoded) > maxRequestPayloadBytes {
		return baiduIoTCoreHTTPPubPlan{}, fmt.Errorf("Baidu IoT Core HTTP publish body must be bounded JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	plan := baiduIoTCoreHTTPPubPlan{MaxPayloadBytes: baiduIoTCoreMQTTDefaultPayloadMax}
	if decoder.Decode(&plan) != nil || ensureJSONDecoderEOF(decoder) != nil {
		return baiduIoTCoreHTTPPubPlan{}, fmt.Errorf("Baidu IoT Core HTTP publish body does not match the protocol schema")
	}
	if plan.IoTCoreID == "" {
		plan.IoTCoreID = endpointID
	}
	if plan.IoTCoreID != endpointID || !baiduIoTCoreIDPattern.MatchString(plan.IoTCoreID) {
		return baiduIoTCoreHTTPPubPlan{}, fmt.Errorf("Baidu IoT Core HTTP publish iot_core_id must exactly match the endpoint")
	}
	if err := validateBaiduIoTCoreMQTTTopic(plan.Topic, false); err != nil {
		return baiduIoTCoreHTTPPubPlan{}, err
	}
	if plan.QoS != 0 && plan.QoS != 1 {
		return baiduIoTCoreHTTPPubPlan{}, fmt.Errorf("Baidu IoT Core HTTP publish supports QoS 0 or 1 only")
	}
	if plan.MaxPayloadBytes < 1 || plan.MaxPayloadBytes > baiduIoTCoreMQTTMaxPayloadBytes {
		return baiduIoTCoreHTTPPubPlan{}, fmt.Errorf("Baidu IoT Core HTTP publish max_payload_bytes must be between 1 and %d", baiduIoTCoreMQTTMaxPayloadBytes)
	}
	if bodyFile == (plan.PayloadBase64 != nil) {
		return baiduIoTCoreHTTPPubPlan{}, fmt.Errorf("Baidu IoT Core HTTP publish requires exactly one of payload_base64 or body_file")
	}
	if plan.PayloadBase64 != nil {
		payload, err := base64.StdEncoding.Strict().DecodeString(*plan.PayloadBase64)
		if err != nil || len(payload) > plan.MaxPayloadBytes {
			return baiduIoTCoreHTTPPubPlan{}, fmt.Errorf("Baidu IoT Core HTTP publish payload_base64 must decode within max_payload_bytes")
		}
		plan.payload = payload
	}
	return plan, nil
}

func loadBaiduIoTCoreHTTPPubPayload(invocation Invocation, plan baiduIoTCoreHTTPPubPlan) ([]byte, error) {
	if invocation.BodyFile == "" {
		return plan.payload, nil
	}
	file, err := os.Open(invocation.BodyFile)
	if err != nil {
		return nil, fmt.Errorf("open Baidu IoT Core HTTP publish body_file")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("Baidu IoT Core HTTP publish body_file must be a regular file")
	}
	if info.Size() > int64(plan.MaxPayloadBytes) {
		return nil, fmt.Errorf("Baidu IoT Core HTTP publish body_file exceeds max_payload_bytes")
	}
	payload, err := io.ReadAll(io.LimitReader(file, int64(plan.MaxPayloadBytes)+1))
	if err != nil || len(payload) > plan.MaxPayloadBytes {
		return nil, fmt.Errorf("read Baidu IoT Core HTTP publish body_file")
	}
	return payload, nil
}

func invokeBaiduIoTCoreHTTPPub(ctx context.Context, adapter *BaiduRESTAdapter, invocation Invocation) (result InvocationResult, returnErr error) {
	target, plan, err := parseBaiduIoTCoreHTTPPubInvocation(invocation)
	if err != nil {
		return InvocationResult{}, err
	}
	payload, err := loadBaiduIoTCoreHTTPPubPayload(invocation, plan)
	if err != nil {
		return InvocationResult{}, err
	}
	credentials, err := adapter.config.Credentials.Credentials(ctx)
	if err != nil {
		return InvocationResult{}, err
	}
	username, password, err := deriveBaiduIoTCoreIAMMQTTCredential(plan.IoTCoreID, credentials, adapter.config.Now().UTC())
	if err != nil {
		return InvocationResult{}, err
	}
	internalToken := ""
	defer func() {
		returnErr = sanitizeBaiduCCRError(returnErr, credentials.AccessKeyID, credentials.SecretAccessKey, username, password, internalToken)
	}()
	authPayload, _ := json.Marshal(map[string]any{
		"username": username, "password": password, "tokenLifeSpanInSeconds": baiduIoTCoreHTTPTokenSeconds,
	})
	authURL := target.Scheme + "://" + target.Host + "/auth"
	authRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, authURL, bytes.NewReader(authPayload))
	if err != nil {
		return InvocationResult{}, fmt.Errorf("build Baidu IoT Core internal auth request")
	}
	authRequest.Header.Set("Accept", "application/json")
	authRequest.Header.Set("Content-Type", "application/json")
	authResponse, err := adapter.config.HTTP.Do(authRequest)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("Baidu IoT Core internal auth request failed")
	}
	authData, err := readRESTResponse(authResponse, baiduIoTCoreHTTPMaxAuthBytes)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("Baidu IoT Core internal auth failed: %w", err)
	}
	var authResult struct {
		Token string `json:"token"`
	}
	authDecoder := json.NewDecoder(bytes.NewReader(authData))
	authDecoder.DisallowUnknownFields()
	if authDecoder.Decode(&authResult) != nil || ensureJSONDecoderEOF(authDecoder) != nil {
		return InvocationResult{}, fmt.Errorf("Baidu IoT Core internal auth returned an invalid response")
	}
	internalToken = strings.TrimSpace(authResult.Token)
	if internalToken == "" || len(internalToken) > baiduIoTCoreHTTPMaxTokenBytes || strings.ContainsAny(internalToken, "\r\n\t ") {
		return InvocationResult{}, fmt.Errorf("Baidu IoT Core internal auth omitted a valid token")
	}
	if err := adapter.reserveBaiduIoTCoreHTTPPub(ctx); err != nil {
		return InvocationResult{}, fmt.Errorf("Baidu IoT Core HTTP publish rate limit wait: %w", err)
	}
	query := url.Values{"topic": []string{plan.Topic}, "qos": []string{strconv.Itoa(plan.QoS)}}
	pubURL := target.Scheme + "://" + target.Host + "/pub?" + query.Encode()
	pubRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, pubURL, bytes.NewReader(payload))
	if err != nil {
		return InvocationResult{}, fmt.Errorf("build Baidu IoT Core HTTP publish request")
	}
	pubRequest.Header.Set("Accept", "application/json")
	pubRequest.Header.Set("Content-Type", "application/octet-stream")
	pubRequest.Header.Set("token", internalToken)
	pubResponse, err := adapter.config.HTTP.Do(pubRequest)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("Baidu IoT Core HTTP publish request failed")
	}
	output, err := readRESTResponse(pubResponse, adapter.config.MaxBodyBytes)
	if err != nil {
		return InvocationResult{}, err
	}
	var publishResult struct {
		Message string `json:"message"`
	}
	publishDecoder := json.NewDecoder(bytes.NewReader(output))
	if publishDecoder.Decode(&publishResult) != nil || ensureJSONDecoderEOF(publishDecoder) != nil || strings.TrimSpace(publishResult.Message) != "ok" {
		return InvocationResult{}, fmt.Errorf("Baidu IoT Core HTTP publish returned an invalid success response")
	}
	for _, secret := range []string{credentials.AccessKeyID, credentials.SecretAccessKey, username, password, internalToken} {
		if secret != "" && bytes.Contains(output, []byte(secret)) {
			return InvocationResult{}, fmt.Errorf("Baidu IoT Core HTTP publish response contained internal credential material")
		}
	}
	return InvocationResult{Output: output, RequestID: responseRequestID(pubResponse.Header)}, nil
}

func (adapter *BaiduRESTAdapter) reserveBaiduIoTCoreHTTPPub(ctx context.Context) error {
	now := adapter.config.Now().UTC()
	adapter.iotHTTPPubMu.Lock()
	slot := now
	if adapter.iotHTTPPubNextSlot.After(slot) {
		slot = adapter.iotHTTPPubNextSlot
	}
	adapter.iotHTTPPubNextSlot = slot.Add(baiduIoTCoreHTTPPubInterval)
	adapter.iotHTTPPubMu.Unlock()
	if wait := slot.Sub(now); wait > 0 {
		return adapter.config.StreamPause(ctx, wait)
	}
	return nil
}

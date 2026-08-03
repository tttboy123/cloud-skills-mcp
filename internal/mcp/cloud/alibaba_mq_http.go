package cloud

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	alibabaMQAPIVersion       = "2015-06-06"
	maxAlibabaMQMessages      = 16
	maxAlibabaMQWaitSeconds   = 30
	maxAlibabaMQQueryBytes    = 1024
	maxAlibabaMQProperties    = 32
	maxAlibabaMQPropertyBytes = 4096
)

type alibabaMQPublishPlan struct {
	MessageBody        string
	MessageTag         string
	Properties         map[string]string
	ProducerGroup      string
	TransactionOutcome string
}

type alibabaMQConsumePlan struct {
	Settlement         string
	TransactionOutcome string
}

type alibabaMQPublishXML struct {
	XMLName     xml.Name `xml:"Message"`
	MessageBody string   `xml:"MessageBody"`
	MessageTag  string   `xml:"MessageTag,omitempty"`
	Properties  string   `xml:"Properties,omitempty"`
}

type alibabaMQReceiptHandlesXML struct {
	XMLName xml.Name `xml:"ReceiptHandles"`
	Handles []string `xml:"ReceiptHandle"`
}

type alibabaMQProviderMessage struct {
	RequestID        string `xml:"RequestId"`
	MessageID        string `xml:"MessageId"`
	ReceiptHandle    string `xml:"ReceiptHandle"`
	MessageBodyMD5   string `xml:"MessageBodyMD5"`
	MessageBody      string `xml:"MessageBody"`
	PublishTime      int64  `xml:"PublishTime"`
	NextConsumeTime  int64  `xml:"NextConsumeTime"`
	FirstConsumeTime int64  `xml:"FirstConsumeTime"`
	ConsumedTimes    int64  `xml:"ConsumedTimes"`
	MessageTag       string `xml:"MessageTag"`
	Properties       string `xml:"Properties"`
}

type alibabaMQMessagesXML struct {
	XMLName  xml.Name                   `xml:"Messages"`
	Messages []alibabaMQProviderMessage `xml:"Message"`
}

type alibabaMQErrorXML struct {
	Code      string `xml:"Code"`
	RequestID string `xml:"RequestId"`
	Errors    []struct {
		Code string `xml:"ErrorCode"`
	} `xml:"Error"`
}

type alibabaMQSanitizedMessage struct {
	MessageID                string            `json:"message_id"`
	MessageBodyMD5           string            `json:"message_body_md5,omitempty"`
	MessageBody              string            `json:"message_body"`
	PublishTime              int64             `json:"publish_time,omitempty"`
	NextConsumeTime          int64             `json:"next_consume_time,omitempty"`
	FirstConsumeTime         int64             `json:"first_consume_time,omitempty"`
	ConsumedTimes            int64             `json:"consumed_times,omitempty"`
	MessageTag               string            `json:"message_tag,omitempty"`
	Properties               map[string]string `json:"properties,omitempty"`
	MessageKey               string            `json:"message_key,omitempty"`
	StartDeliverTime         int64             `json:"start_deliver_time,omitempty"`
	ShardingKey              string            `json:"sharding_key,omitempty"`
	TransactionCheckImmunity int               `json:"transaction_check_immunity_seconds,omitempty"`
}

type alibabaMQPublishOutput struct {
	MessageID      string `json:"message_id"`
	MessageBodyMD5 string `json:"message_body_md5,omitempty"`
	Transaction    string `json:"transaction_outcome,omitempty"`
}

func invokeAlibabaMQ(ctx context.Context, adapter *AlibabaRESTAdapter, invocation Invocation) (InvocationResult, error) {
	target, operation, err := validateAlibabaMQInvocation(invocation, adapter.config.AllowedHosts)
	if err != nil {
		return InvocationResult{}, err
	}
	// The operation plan is validated before any credential-chain or network access.
	if operation == "PublishMessage" {
		if _, err := parseAlibabaMQPublishPlan(invocation.Body); err != nil {
			return InvocationResult{}, err
		}
	} else if _, err := parseAlibabaMQConsumePlan(invocation.Body, operation); err != nil {
		return InvocationResult{}, err
	}
	sink, err := newWebSocketOutputSink(invocation, adapter.config.MaxBodyBytes, "Alibaba Cloud RocketMQ HTTP")
	if err != nil {
		return InvocationResult{}, err
	}
	defer sink.abort()
	credentials, err := adapter.config.Credentials.Credentials(ctx)
	if err != nil {
		return InvocationResult{}, err
	}
	if credentials.AccessKeyID == "" || credentials.AccessKeySecret == "" {
		return InvocationResult{}, fmt.Errorf("Alibaba Cloud MQ credential provider returned incomplete AKSK material")
	}

	switch operation {
	case "PublishMessage":
		return invokeAlibabaMQPublish(ctx, adapter, invocation, target, credentials, sink)
	case "ConsumeMessages", "ConsumeOrderly", "ConsumeHalfMessages":
		return invokeAlibabaMQConsume(ctx, adapter, invocation, target, credentials, sink, operation)
	default:
		return InvocationResult{}, fmt.Errorf("unsupported Alibaba Cloud RocketMQ HTTP operation %q", operation)
	}
}

func validateAlibabaMQInvocation(invocation Invocation, allowedHosts []string) (*url.URL, string, error) {
	if invocation.Mode != ModeMutate {
		return nil, "", fmt.Errorf("Alibaba Cloud RocketMQ HTTP operations require the mutation approval path")
	}
	if !strings.EqualFold(strings.TrimSpace(invocation.Service), "rocketmq") {
		return nil, "", fmt.Errorf("Alibaba Cloud MQ requires service rocketmq")
	}
	operation := strings.TrimSpace(invocation.Operation)
	switch operation {
	case "PublishMessage", "ConsumeMessages", "ConsumeOrderly", "ConsumeHalfMessages":
	default:
		return nil, "", fmt.Errorf("Alibaba Cloud MQ exposes only PublishMessage, ConsumeMessages, ConsumeOrderly, and ConsumeHalfMessages; acknowledgement and transaction handles remain internal")
	}
	if invocation.ResponseFile == "" {
		return nil, "", fmt.Errorf("Alibaba Cloud MQ requires response_file for atomic sanitized output")
	}
	if invocation.BodyFile != "" {
		return nil, "", fmt.Errorf("Alibaba Cloud MQ accepts only a bounded JSON body plan; body_file is forbidden")
	}
	if len(invocation.Headers) != 0 {
		return nil, "", fmt.Errorf("Alibaba Cloud MQ headers are server-controlled")
	}
	if invocation.APIVersion != "" && invocation.APIVersion != alibabaMQAPIVersion {
		return nil, "", fmt.Errorf("Alibaba Cloud MQ api_version must be %s when provided", alibabaMQAPIVersion)
	}
	if err := validateRESTTargetWithEndpointHosts(ProviderAlicloud, invocation.Method, invocation.URL, allowedHosts); err != nil {
		return nil, "", err
	}
	target, err := url.Parse(invocation.URL)
	if err != nil {
		return nil, "", fmt.Errorf("parse Alibaba Cloud MQ URL: %w", err)
	}
	if target.RawQuery != "" {
		return nil, "", fmt.Errorf("Alibaba Cloud MQ URL must not contain a raw query; use parameters")
	}
	host := strings.ToLower(target.Hostname())
	if !isAlibabaMQOfficialHost(host) && !isExplicitEndpointHost(host, allowedHosts) {
		return nil, "", fmt.Errorf("Alibaba Cloud MQ requires an official mqrest endpoint or an operator-pinned endpoint host")
	}
	parts := strings.Split(target.EscapedPath(), "/")
	if len(parts) != 4 || parts[0] != "" || parts[1] != "topics" || parts[3] != "messages" || !validAlibabaMQName(parts[2], 255) {
		return nil, "", fmt.Errorf("Alibaba Cloud MQ requires exact path /topics/<topic>/messages")
	}
	wantMethod := http.MethodGet
	if operation == "PublishMessage" {
		wantMethod = http.MethodPost
	}
	if !strings.EqualFold(strings.TrimSpace(invocation.Method), wantMethod) {
		return nil, "", fmt.Errorf("Alibaba Cloud MQ %s requires method %s", operation, wantMethod)
	}
	query, err := alibabaMQOperationQuery(operation, invocation.Parameters)
	if err != nil {
		return nil, "", err
	}
	if len(query.Encode()) > maxAlibabaMQQueryBytes {
		return nil, "", fmt.Errorf("Alibaba Cloud MQ query exceeds %d bytes", maxAlibabaMQQueryBytes)
	}
	target.RawQuery = query.Encode()
	return target, operation, nil
}

func isAlibabaMQOfficialHost(host string) bool {
	parts := strings.Split(host, ".")
	if len(parts) < 5 || parts[1] != "mqrest" || !endpointLabelPattern.MatchString(parts[0]) || !endpointLabelPattern.MatchString(parts[2]) {
		return false
	}
	suffix := strings.Join(parts[3:], ".")
	return suffix == "aliyuncs.com" || suffix == "aliyuncs.com.cn"
}

func isExplicitEndpointHost(host string, allowedHosts []string) bool {
	for _, candidate := range allowedHosts {
		if validAdditionalEndpointHost(candidate) && strings.EqualFold(host, candidate) {
			return true
		}
	}
	return false
}

func validAlibabaMQName(value string, max int) bool {
	if value == "" || len(value) > max {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func alibabaMQOperationQuery(operation string, parameters map[string]any) (url.Values, error) {
	allowed := map[string]bool{"ns": true}
	if operation != "PublishMessage" {
		allowed["consumer"] = true
		allowed["numOfMessages"] = true
		allowed["waitseconds"] = true
		if operation != "ConsumeHalfMessages" {
			allowed["tag"] = true
		}
		if operation == "ConsumeOrderly" || operation == "ConsumeHalfMessages" {
			allowed["trans"] = true
		}
	}
	query := make(url.Values)
	for name, raw := range parameters {
		if !allowed[name] {
			return nil, fmt.Errorf("Alibaba Cloud MQ %s does not allow query parameter %q", operation, name)
		}
		values, err := stringValues(raw)
		if err != nil || len(values) != 1 {
			return nil, fmt.Errorf("Alibaba Cloud MQ query parameter %q must be one scalar", name)
		}
		query.Set(name, values[0])
	}
	if ns := query.Get("ns"); ns != "" && !validAlibabaMQControlValue(ns, 255) {
		return nil, fmt.Errorf("Alibaba Cloud MQ ns is invalid")
	}
	if operation == "PublishMessage" {
		return query, nil
	}
	if !validAlibabaMQControlValue(query.Get("consumer"), 255) {
		return nil, fmt.Errorf("Alibaba Cloud MQ consumer is required and must be valid")
	}
	count, err := strconv.Atoi(query.Get("numOfMessages"))
	if err != nil || count < 1 || count > maxAlibabaMQMessages {
		return nil, fmt.Errorf("Alibaba Cloud MQ numOfMessages must be from 1 to %d", maxAlibabaMQMessages)
	}
	if wait := query.Get("waitseconds"); wait != "" {
		seconds, err := strconv.Atoi(wait)
		if err != nil || seconds < 0 || seconds > maxAlibabaMQWaitSeconds {
			return nil, fmt.Errorf("Alibaba Cloud MQ waitseconds must be from 0 to %d", maxAlibabaMQWaitSeconds)
		}
	}
	if tag := query.Get("tag"); tag != "" && !validAlibabaMQControlValue(tag, 255) {
		return nil, fmt.Errorf("Alibaba Cloud MQ tag is invalid")
	}
	wantTrans := ""
	if operation == "ConsumeOrderly" {
		wantTrans = "order"
	} else if operation == "ConsumeHalfMessages" {
		wantTrans = "pop"
	}
	if wantTrans != "" {
		if supplied := query.Get("trans"); supplied != "" && supplied != wantTrans {
			return nil, fmt.Errorf("Alibaba Cloud MQ %s requires trans=%s", operation, wantTrans)
		}
		query.Set("trans", wantTrans)
	}
	return query, nil
}

func validAlibabaMQControlValue(value string, max int) bool {
	if value == "" || len(value) > max || strings.TrimSpace(value) != value || !validAlibabaMQXMLString(value) {
		return false
	}
	for _, character := range value {
		if character == 0 || character == '\r' || character == '\n' || character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func invokeAlibabaMQPublish(ctx context.Context, adapter *AlibabaRESTAdapter, invocation Invocation, target *url.URL, credentials AlibabaCredentials, sink *tencentWebSocketOutputSink) (InvocationResult, error) {
	plan, err := parseAlibabaMQPublishPlan(invocation.Body)
	if err != nil {
		return InvocationResult{}, err
	}
	properties := serializeAlibabaMQProperties(plan.Properties)
	body, err := xml.Marshal(alibabaMQPublishXML{MessageBody: plan.MessageBody, MessageTag: plan.MessageTag, Properties: properties})
	if err != nil {
		return InvocationResult{}, fmt.Errorf("encode Alibaba Cloud MQ publish request: %w", err)
	}
	responseBody, requestID, err := doAlibabaMQRequest(ctx, adapter, http.MethodPost, target, body, credentials)
	if err != nil {
		return InvocationResult{}, err
	}
	var response alibabaMQProviderMessage
	if err := xml.Unmarshal(responseBody, &response); err != nil || response.MessageID == "" {
		return InvocationResult{}, fmt.Errorf("decode Alibaba Cloud MQ publish response")
	}
	if requestID == "" {
		requestID = response.RequestID
	}
	encoded, err := json.Marshal(alibabaMQPublishOutput{MessageID: response.MessageID, MessageBodyMD5: response.MessageBodyMD5, Transaction: plan.TransactionOutcome})
	if err != nil {
		return InvocationResult{}, err
	}
	if err := sink.writeMessage(encoded); err != nil {
		return InvocationResult{}, err
	}
	if response.ReceiptHandle != "" {
		if plan.TransactionOutcome == "" || plan.ProducerGroup == "" {
			return InvocationResult{}, fmt.Errorf("Alibaba Cloud MQ transaction response requires a preapproved producer_group and transaction_outcome")
		}
		if _, _, err := settleAlibabaMQ(ctx, adapter, target, credentials, plan.ProducerGroup, plan.TransactionOutcome, []string{response.ReceiptHandle}); err != nil {
			return InvocationResult{}, err
		}
	} else if plan.TransactionOutcome != "" {
		return InvocationResult{}, fmt.Errorf("Alibaba Cloud MQ transactional publish returned no internal transaction handle")
	}
	output, err := sink.finish(requestID)
	return InvocationResult{Output: output, RequestID: requestID}, err
}

func invokeAlibabaMQConsume(ctx context.Context, adapter *AlibabaRESTAdapter, invocation Invocation, target *url.URL, credentials AlibabaCredentials, sink *tencentWebSocketOutputSink, operation string) (InvocationResult, error) {
	plan, err := parseAlibabaMQConsumePlan(invocation.Body, operation)
	if err != nil {
		return InvocationResult{}, err
	}
	responseBody, requestID, err := doAlibabaMQRequest(ctx, adapter, http.MethodGet, target, nil, credentials)
	if err != nil {
		return InvocationResult{}, err
	}
	var response alibabaMQMessagesXML
	if err := xml.Unmarshal(responseBody, &response); err != nil {
		return InvocationResult{}, fmt.Errorf("decode Alibaba Cloud MQ consume response")
	}
	expectedMessages, _ := strconv.Atoi(target.Query().Get("numOfMessages"))
	if len(response.Messages) > expectedMessages || len(response.Messages) > maxAlibabaMQMessages {
		return InvocationResult{}, fmt.Errorf("Alibaba Cloud MQ consume response exceeds the requested message bound")
	}
	handles := make([]string, 0, len(response.Messages))
	seenHandles := make(map[string]struct{}, len(response.Messages))
	for _, message := range response.Messages {
		if message.MessageID == "" || message.ReceiptHandle == "" || len(message.ReceiptHandle) > maxRequestPayloadBytes {
			return InvocationResult{}, fmt.Errorf("Alibaba Cloud MQ consume response omitted required internal message metadata")
		}
		if _, duplicate := seenHandles[message.ReceiptHandle]; duplicate {
			return InvocationResult{}, fmt.Errorf("Alibaba Cloud MQ consume response repeated an internal receipt handle")
		}
		seenHandles[message.ReceiptHandle] = struct{}{}
		handles = append(handles, message.ReceiptHandle)
	}
	for _, message := range response.Messages {
		encoded, err := json.Marshal(sanitizeAlibabaMQMessage(message))
		if err != nil {
			return InvocationResult{}, err
		}
		if err := sink.writeMessage(encoded); err != nil {
			return InvocationResult{}, err
		}
	}
	if len(handles) > 0 {
		consumer := target.Query().Get("consumer")
		if operation == "ConsumeHalfMessages" {
			_, _, err = settleAlibabaMQ(ctx, adapter, target, credentials, consumer, plan.TransactionOutcome, handles)
		} else if plan.Settlement == "acknowledge" {
			_, _, err = settleAlibabaMQ(ctx, adapter, target, credentials, consumer, "", handles)
		}
		if err != nil {
			return InvocationResult{}, err
		}
	}
	output, err := sink.finish(requestID)
	return InvocationResult{Output: output, RequestID: requestID}, err
}

func parseAlibabaMQPublishPlan(value any) (alibabaMQPublishPlan, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return alibabaMQPublishPlan{}, fmt.Errorf("Alibaba Cloud MQ PublishMessage body must be an object")
	}
	allowed := map[string]bool{"message_body": true, "message_tag": true, "properties": true, "producer_group": true, "transaction_outcome": true}
	for name := range object {
		if !allowed[name] {
			return alibabaMQPublishPlan{}, fmt.Errorf("Alibaba Cloud MQ PublishMessage body field %q is unsupported", name)
		}
	}
	plan := alibabaMQPublishPlan{}
	var okString bool
	plan.MessageBody, okString = object["message_body"].(string)
	if !okString || plan.MessageBody == "" || len(plan.MessageBody) > maxRequestPayloadBytes || !validAlibabaMQXMLString(plan.MessageBody) {
		return alibabaMQPublishPlan{}, fmt.Errorf("Alibaba Cloud MQ message_body must be a non-empty string no larger than %d bytes", maxRequestPayloadBytes)
	}
	if raw, exists := object["message_tag"]; exists {
		plan.MessageTag, okString = raw.(string)
		if !okString || (plan.MessageTag != "" && !validAlibabaMQControlValue(plan.MessageTag, 255)) {
			return alibabaMQPublishPlan{}, fmt.Errorf("Alibaba Cloud MQ message_tag is invalid")
		}
	}
	if raw, exists := object["producer_group"]; exists {
		plan.ProducerGroup, okString = raw.(string)
		if !okString || !validAlibabaMQControlValue(plan.ProducerGroup, 255) {
			return alibabaMQPublishPlan{}, fmt.Errorf("Alibaba Cloud MQ producer_group is invalid")
		}
	}
	if raw, exists := object["transaction_outcome"]; exists {
		plan.TransactionOutcome, okString = raw.(string)
		if !okString || (plan.TransactionOutcome != "commit" && plan.TransactionOutcome != "rollback") {
			return alibabaMQPublishPlan{}, fmt.Errorf("Alibaba Cloud MQ transaction_outcome must be commit or rollback")
		}
	}
	properties, err := parseAlibabaMQProperties(object["properties"])
	if err != nil {
		return alibabaMQPublishPlan{}, err
	}
	plan.Properties = properties
	_, transactional := properties["__TransCheckT"]
	if (transactional && (plan.TransactionOutcome == "" || plan.ProducerGroup == "")) || (!transactional && (plan.TransactionOutcome != "" || plan.ProducerGroup != "")) {
		return alibabaMQPublishPlan{}, fmt.Errorf("Alibaba Cloud MQ transactional properties require both producer_group and transaction_outcome, and non-transactional messages forbid them")
	}
	if transactional {
		seconds, err := strconv.Atoi(properties["__TransCheckT"])
		if err != nil || seconds < 10 || seconds > 300 {
			return alibabaMQPublishPlan{}, fmt.Errorf("Alibaba Cloud MQ __TransCheckT must be from 10 to 300 seconds")
		}
	}
	return plan, nil
}

func parseAlibabaMQProperties(value any) (map[string]string, error) {
	if value == nil {
		return nil, nil
	}
	object, ok := value.(map[string]any)
	if !ok || len(object) > maxAlibabaMQProperties {
		return nil, fmt.Errorf("Alibaba Cloud MQ properties must be an object with at most %d entries", maxAlibabaMQProperties)
	}
	properties := make(map[string]string, len(object))
	total := 0
	for name, raw := range object {
		value, ok := raw.(string)
		if !ok || name == "" || value == "" || len(name) > 128 || len(value) > 1024 || !validAlibabaMQXMLString(name) || !validAlibabaMQXMLString(value) || containsAlibabaMQPropertySpecial(name) || containsAlibabaMQPropertySpecial(value) || isAlibabaMQReservedProperty(name) {
			return nil, fmt.Errorf("Alibaba Cloud MQ property %q is invalid", name)
		}
		total += len(name) + len(value) + 2
		properties[name] = value
	}
	if total > maxAlibabaMQPropertyBytes {
		return nil, fmt.Errorf("Alibaba Cloud MQ serialized properties exceed %d bytes", maxAlibabaMQPropertyBytes)
	}
	return properties, nil
}

func containsAlibabaMQPropertySpecial(value string) bool {
	return strings.ContainsAny(value, "'\"<>&:|\r\n")
}

func validAlibabaMQXMLString(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if character == 0x9 || character == 0xA || character == 0xD || character >= 0x20 && character <= 0xD7FF || character >= 0xE000 && character <= 0xFFFD || character >= 0x10000 && character <= 0x10FFFF {
			continue
		}
		return false
	}
	return true
}

func isAlibabaMQReservedProperty(name string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(name, "-", ""), "_", ""))
	for _, forbidden := range []string{"receipthandle", "authorization", "securitytoken", "accesskeyid", "accesskeysecret"} {
		if strings.Contains(normalized, forbidden) {
			return true
		}
	}
	return false
}

func serializeAlibabaMQProperties(properties map[string]string) string {
	if len(properties) == 0 {
		return ""
	}
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	var output strings.Builder
	for _, name := range names {
		output.WriteString(name)
		output.WriteByte(':')
		output.WriteString(properties[name])
		output.WriteByte('|')
	}
	return output.String()
}

func parseAlibabaMQConsumePlan(value any, operation string) (alibabaMQConsumePlan, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return alibabaMQConsumePlan{}, fmt.Errorf("Alibaba Cloud MQ %s body must be a control object", operation)
	}
	plan := alibabaMQConsumePlan{}
	if operation == "ConsumeHalfMessages" {
		if len(object) != 1 {
			return plan, fmt.Errorf("Alibaba Cloud MQ ConsumeHalfMessages body accepts only transaction_outcome")
		}
		outcome, ok := object["transaction_outcome"].(string)
		if !ok || (outcome != "commit" && outcome != "rollback") {
			return plan, fmt.Errorf("Alibaba Cloud MQ transaction_outcome must be commit or rollback")
		}
		plan.TransactionOutcome = outcome
		return plan, nil
	}
	if len(object) != 1 {
		return plan, fmt.Errorf("Alibaba Cloud MQ consume body accepts only settlement")
	}
	settlement, ok := object["settlement"].(string)
	if !ok || (settlement != "acknowledge" && settlement != "release") {
		return plan, fmt.Errorf("Alibaba Cloud MQ settlement must be acknowledge or release")
	}
	plan.Settlement = settlement
	return plan, nil
}

func settleAlibabaMQ(ctx context.Context, adapter *AlibabaRESTAdapter, base *url.URL, credentials AlibabaCredentials, consumer, outcome string, handles []string) ([]byte, string, error) {
	query := make(url.Values)
	query.Set("consumer", consumer)
	if ns := base.Query().Get("ns"); ns != "" {
		query.Set("ns", ns)
	}
	if outcome != "" {
		query.Set("trans", outcome)
	}
	target := *base
	target.RawQuery = query.Encode()
	body, err := xml.Marshal(alibabaMQReceiptHandlesXML{Handles: handles})
	if err != nil {
		return nil, "", fmt.Errorf("encode Alibaba Cloud MQ internal settlement: %w", err)
	}
	return doAlibabaMQRequest(ctx, adapter, http.MethodDelete, &target, body, credentials)
}

func doAlibabaMQRequest(ctx context.Context, adapter *AlibabaRESTAdapter, method string, target *url.URL, body []byte, credentials AlibabaCredentials) ([]byte, string, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, target.String(), reader)
	if err != nil {
		return nil, "", fmt.Errorf("build Alibaba Cloud MQ request: %w", err)
	}
	if body != nil {
		request.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	}
	request.Header.Set("Content-Type", "text/xml;charset=utf-8")
	if err := signAlibabaMQ(request, credentials, adapter.config.Now().UTC()); err != nil {
		return nil, "", err
	}
	response, err := adapter.config.HTTP.Do(request)
	if err != nil {
		return nil, "", fmt.Errorf("Alibaba Cloud MQ HTTP request: %w", err)
	}
	if response == nil || response.Body == nil {
		return nil, "", fmt.Errorf("Alibaba Cloud MQ HTTP response is empty")
	}
	defer response.Body.Close()
	limit := adapter.config.MaxBodyBytes
	if limit <= 0 {
		limit = defaultRESTBodyLimit
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, "", fmt.Errorf("read Alibaba Cloud MQ HTTP response: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, "", fmt.Errorf("Alibaba Cloud MQ response exceeds %d bytes", limit)
	}
	requestID := alibabaMQRequestID(response.Header)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, requestID, alibabaMQResponseError(response.StatusCode, data, requestID)
	}
	return data, requestID, nil
}

func signAlibabaMQ(request *http.Request, credentials AlibabaCredentials, now time.Time) error {
	if credentials.AccessKeyID == "" || credentials.AccessKeySecret == "" {
		return fmt.Errorf("Alibaba Cloud MQ requires complete AKSK material")
	}
	request.Header.Set("X-Mq-Version", alibabaMQAPIVersion)
	request.Header.Set("Date", now.UTC().Format(http.TimeFormat))
	request.Header.Set("Content-Type", "text/xml;charset=utf-8")
	contentMD5, hasBody, err := requestBodyMD5(request, true)
	if err != nil {
		return fmt.Errorf("hash Alibaba Cloud MQ request body: %w", err)
	}
	if !hasBody {
		digest := md5.Sum(nil)
		contentMD5 = base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(digest[:])))
	}
	request.Header.Set("Content-MD5", contentMD5)
	if credentials.SecurityToken != "" {
		request.Header.Set("Security-Token", credentials.SecurityToken)
	}
	resource := canonicalURI(request.URL)
	if request.URL.RawQuery != "" {
		resource += "?" + request.URL.RawQuery
	}
	canonicalHeaders := canonicalPrefixedHeaders(request.Header, "x-mq-")
	stringToSign := request.Method + "\n" + contentMD5 + "\n" + request.Header.Get("Content-Type") + "\n" + request.Header.Get("Date") + "\n" + canonicalHeaders + "\n" + resource
	signature := base64.StdEncoding.EncodeToString(hmacBytes(sha1.New, []byte(credentials.AccessKeySecret), []byte(stringToSign)))
	request.Header.Set("Authorization", "MQ "+credentials.AccessKeyID+":"+signature)
	return nil
}

func alibabaMQResponseError(status int, body []byte, requestID string) error {
	var providerError alibabaMQErrorXML
	_ = xml.Unmarshal(body, &providerError)
	code := strings.TrimSpace(providerError.Code)
	if len(providerError.Errors) > 0 {
		code = strings.TrimSpace(providerError.Errors[0].Code)
	}
	if requestID == "" {
		requestID = strings.TrimSpace(providerError.RequestID)
	}
	if code == "" {
		code = "HTTP " + strconv.Itoa(status)
	} else if !validAlibabaMQErrorCode(code) {
		code = "HTTP " + strconv.Itoa(status)
	}
	if requestID != "" {
		return fmt.Errorf("Alibaba Cloud MQ request failed: %s (request_id=%s)", code, requestID)
	}
	return fmt.Errorf("Alibaba Cloud MQ request failed: %s", code)
}

func validAlibabaMQErrorCode(code string) bool {
	if len(code) == 0 || len(code) > 128 || !((code[0] >= 'A' && code[0] <= 'Z') || (code[0] >= 'a' && code[0] <= 'z')) {
		return false
	}
	for _, character := range code[1:] {
		if (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func alibabaMQRequestID(headers http.Header) string {
	for _, name := range []string{"X-Mq-Request-Id", "X-Mq-RequestId", "X-Mqs-Request-Id", "X-Mqs-RequestId", "X-Request-Id", "Request-Id"} {
		if value := headers.Get(name); value != "" {
			return value
		}
	}
	return ""
}

func sanitizeAlibabaMQMessage(message alibabaMQProviderMessage) alibabaMQSanitizedMessage {
	properties := parseAlibabaMQResponseProperties(message.Properties)
	result := alibabaMQSanitizedMessage{
		MessageID: message.MessageID, MessageBodyMD5: message.MessageBodyMD5, MessageBody: message.MessageBody,
		PublishTime: message.PublishTime, NextConsumeTime: message.NextConsumeTime, FirstConsumeTime: message.FirstConsumeTime,
		ConsumedTimes: message.ConsumedTimes, MessageTag: message.MessageTag,
	}
	result.MessageKey = properties["KEYS"]
	result.StartDeliverTime, _ = strconv.ParseInt(properties["__STARTDELIVERTIME"], 10, 64)
	result.ShardingKey = properties["__SHARDINGKEY"]
	result.TransactionCheckImmunity, _ = strconv.Atoi(properties["__TransCheckT"])
	delete(properties, "KEYS")
	delete(properties, "__STARTDELIVERTIME")
	delete(properties, "__SHARDINGKEY")
	delete(properties, "__TransCheckT")
	if len(properties) > 0 {
		result.Properties = properties
	}
	return result
}

func parseAlibabaMQResponseProperties(encoded string) map[string]string {
	properties := make(map[string]string)
	for _, pair := range strings.Split(encoded, "|") {
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, ":", 2)
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" && !isAlibabaMQReservedProperty(parts[0]) {
			properties[parts[0]] = parts[1]
		}
	}
	return properties
}

package cloud

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	authSchemeAWSKinesisVideoSignalingWS = "kinesisvideo-signaling-ws"
	awsKinesisVideoService               = "kinesisvideo"
	awsKinesisVideoSignalingExpires      = "299"
	awsKinesisVideoMaxFrames             = 256
	awsKinesisVideoMaxMessages           = 256
	awsKinesisVideoMaxTimeoutSeconds     = 300
	awsKinesisVideoMaxPayloadBase64Bytes = 10_000
	awsKinesisVideoMessageInterval       = 200 * time.Millisecond
)

var (
	awsKinesisVideoIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,256}$`)
	awsKinesisVideoChannelARNPattern = regexp.MustCompile(`^arn:([a-z0-9-]+):kinesisvideo:([a-z0-9-]+):([0-9]{12}):channel/([A-Za-z0-9_.-]{1,256})/([0-9]+)$`)
)

type awsKinesisVideoSignalingMessage struct {
	Action            string          `json:"action"`
	Payload           json.RawMessage `json:"payload"`
	RecipientClientID string          `json:"recipient_client_id,omitempty"`
	CorrelationID     string          `json:"correlation_id"`

	encoded []byte
}

type awsKinesisVideoSignalingPlan struct {
	Role           string                            `json:"role"`
	ChannelARN     string                            `json:"channel_arn"`
	ClientID       string                            `json:"client_id,omitempty"`
	Messages       []awsKinesisVideoSignalingMessage `json:"messages,omitempty"`
	MaxMessages    int                               `json:"max_messages"`
	TimeoutSeconds int                               `json:"timeout_seconds"`
}

type awsKinesisVideoSignalingStatus struct {
	CorrelationID string `json:"correlationId"`
	Success       *bool  `json:"success,omitempty"`
	ErrorType     string `json:"errorType,omitempty"`
	StatusCode    string `json:"statusCode,omitempty"`
	Description   string `json:"description,omitempty"`
}

type awsKinesisVideoSignalingEvent struct {
	SenderClientID string                          `json:"senderClientId,omitempty"`
	MessageType    string                          `json:"messageType"`
	MessagePayload string                          `json:"messagePayload,omitempty"`
	StatusResponse *awsKinesisVideoSignalingStatus `json:"statusResponse,omitempty"`
}

func validateAWSKinesisVideoSignalingInvocation(invocation Invocation) error {
	operation := strings.TrimSpace(invocation.Operation)
	if !strings.EqualFold(invocation.Service, awsKinesisVideoService) || operation != "ConnectAsMaster" && operation != "ConnectAsViewer" || !strings.EqualFold(invocation.Method, http.MethodGet) {
		return fmt.Errorf("AWS Kinesis Video signaling requires GET, service kinesisvideo, and operation ConnectAsMaster or ConnectAsViewer")
	}
	if invocation.Mode != ModeMutate {
		return fmt.Errorf("AWS Kinesis Video signaling requires the mutate tool")
	}
	if !identifierPattern.MatchString(invocation.Region) {
		return fmt.Errorf("AWS Kinesis Video signaling requires a valid region")
	}
	if err := validateAWSKinesisVideoSignalingTarget(invocation.URL, invocation.Region); err != nil {
		return err
	}
	if len(invocation.Parameters) != 0 || len(invocation.Headers) != 0 || invocation.Body == nil || invocation.BodyFile != "" || invocation.ResponseFile == "" {
		return fmt.Errorf("AWS Kinesis Video signaling requires only a finite protocol body and response_file")
	}
	if invocation.RegionSet != "" || invocation.Project != "" || invocation.Subscription != "" || invocation.Audience != "" || invocation.AuthVersion != "" || invocation.APIVersion != "" || invocation.PayloadMode != "" || invocation.ChecksumAlgorithm != "" || invocation.StreamChunkBytes != 0 || invocation.StreamIntervalMS != 0 || invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return fmt.Errorf("AWS Kinesis Video signaling does not accept REST, cross-provider, or generic stream controls")
	}
	_, err := parseAWSKinesisVideoSignalingPlan(invocation.Body, invocation.Region, operation)
	return err
}

func validateAWSKinesisVideoSignalingTarget(rawURL, region string) error {
	target, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(target.Scheme, "wss") || target.Hostname() == "" || target.Port() != "" || target.User != nil || target.Fragment != "" || target.RawQuery != "" || target.EscapedPath() != "/" {
		return fmt.Errorf("AWS Kinesis Video signaling requires an exact query-free wss:// endpoint with path /")
	}
	host := strings.ToLower(target.Hostname())
	region = strings.ToLower(strings.TrimSpace(region))
	for _, suffix := range []string{
		".kinesisvideo." + region + ".amazonaws.com",
		".kinesisvideo." + region + ".amazonaws.com.cn",
		".kinesisvideo." + region + ".api.aws",
	} {
		prefix := strings.TrimSuffix(host, suffix)
		if prefix != host && endpointLabelPattern.MatchString(prefix) {
			return nil
		}
	}
	return fmt.Errorf("AWS Kinesis Video signaling endpoint does not match the requested region")
}

func parseAWSKinesisVideoSignalingPlan(body any, region, operation string) (awsKinesisVideoSignalingPlan, error) {
	if body == nil || alibabaNLSContainsCredentialField(body) {
		return awsKinesisVideoSignalingPlan{}, fmt.Errorf("AWS Kinesis Video signaling requires a credential-free protocol body")
	}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) == 0 || len(encoded) > maxRequestPayloadBytes {
		return awsKinesisVideoSignalingPlan{}, fmt.Errorf("AWS Kinesis Video signaling body must be bounded JSON")
	}
	var plan awsKinesisVideoSignalingPlan
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&plan) != nil || ensureJSONDecoderEOF(decoder) != nil {
		return awsKinesisVideoSignalingPlan{}, fmt.Errorf("AWS Kinesis Video signaling body does not match the finite plan schema")
	}
	plan.Role = strings.ToUpper(strings.TrimSpace(plan.Role))
	wantRole := "VIEWER"
	if operation == "ConnectAsMaster" {
		wantRole = "MASTER"
	}
	if plan.Role != wantRole {
		return awsKinesisVideoSignalingPlan{}, fmt.Errorf("AWS Kinesis Video signaling role must match the connect operation")
	}
	arnMatch := awsKinesisVideoChannelARNPattern.FindStringSubmatch(plan.ChannelARN)
	if arnMatch == nil || len(plan.ChannelARN) > 1024 || !strings.EqualFold(arnMatch[2], region) || !validAWSKinesisVideoPartition(arnMatch[1], region) {
		return awsKinesisVideoSignalingPlan{}, fmt.Errorf("AWS Kinesis Video signaling channel_arn must be one channel in the requested region")
	}
	if plan.Role == "VIEWER" {
		if !awsKinesisVideoIdentifierPattern.MatchString(plan.ClientID) {
			return awsKinesisVideoSignalingPlan{}, fmt.Errorf("AWS Kinesis Video viewer requires a valid client_id")
		}
	} else if plan.ClientID != "" {
		return awsKinesisVideoSignalingPlan{}, fmt.Errorf("AWS Kinesis Video master must not provide client_id")
	}
	if len(plan.Messages) > awsKinesisVideoMaxFrames {
		return awsKinesisVideoSignalingPlan{}, fmt.Errorf("AWS Kinesis Video signaling supports at most %d client messages", awsKinesisVideoMaxFrames)
	}
	if plan.MaxMessages < 1 || plan.MaxMessages > awsKinesisVideoMaxMessages || plan.TimeoutSeconds < 1 || plan.TimeoutSeconds > awsKinesisVideoMaxTimeoutSeconds {
		return awsKinesisVideoSignalingPlan{}, fmt.Errorf("AWS Kinesis Video signaling message count or timeout is outside the finite bound")
	}
	correlations := make(map[string]bool)
	for index := range plan.Messages {
		message := &plan.Messages[index]
		message.Action = strings.ToUpper(strings.TrimSpace(message.Action))
		switch message.Action {
		case "SDP_OFFER", "SDP_ANSWER", "ICE_CANDIDATE":
		default:
			return awsKinesisVideoSignalingPlan{}, fmt.Errorf("AWS Kinesis Video signaling message %d has an unsupported action", index)
		}
		if !awsKinesisVideoIdentifierPattern.MatchString(message.CorrelationID) || correlations[message.CorrelationID] {
			return awsKinesisVideoSignalingPlan{}, fmt.Errorf("AWS Kinesis Video signaling correlation_id values must be valid and unique")
		}
		correlations[message.CorrelationID] = true
		if plan.Role == "MASTER" {
			if !awsKinesisVideoIdentifierPattern.MatchString(message.RecipientClientID) {
				return awsKinesisVideoSignalingPlan{}, fmt.Errorf("AWS Kinesis Video master messages require recipient_client_id")
			}
		} else if message.RecipientClientID != "" {
			return awsKinesisVideoSignalingPlan{}, fmt.Errorf("AWS Kinesis Video viewer messages must not provide recipient_client_id")
		}
		if len(message.Payload) == 0 {
			return awsKinesisVideoSignalingPlan{}, fmt.Errorf("AWS Kinesis Video signaling message %d requires a JSON object payload", index)
		}
		var payload map[string]any
		payloadDecoder := json.NewDecoder(bytes.NewReader(message.Payload))
		payloadDecoder.UseNumber()
		if payloadDecoder.Decode(&payload) != nil || ensureJSONDecoderEOF(payloadDecoder) != nil || payload == nil || alibabaNLSContainsCredentialField(payload) {
			return awsKinesisVideoSignalingPlan{}, fmt.Errorf("AWS Kinesis Video signaling message %d payload must be one credential-free JSON object", index)
		}
		compact, err := json.Marshal(payload)
		if err != nil || len(base64.StdEncoding.EncodeToString(compact)) > awsKinesisVideoMaxPayloadBase64Bytes {
			return awsKinesisVideoSignalingPlan{}, fmt.Errorf("AWS Kinesis Video signaling message %d payload exceeds the provider bound", index)
		}
		wire := map[string]any{
			"action":         message.Action,
			"messagePayload": base64.StdEncoding.EncodeToString(compact),
			"correlationId":  message.CorrelationID,
		}
		if message.RecipientClientID != "" {
			wire["recipientClientId"] = message.RecipientClientID
		}
		message.encoded, err = json.Marshal(wire)
		if err != nil {
			return awsKinesisVideoSignalingPlan{}, fmt.Errorf("encode AWS Kinesis Video signaling message")
		}
	}
	return plan, nil
}

func validAWSKinesisVideoPartition(partition, region string) bool {
	switch {
	case strings.HasPrefix(region, "cn-"):
		return partition == "aws-cn"
	case strings.HasPrefix(region, "us-gov-"):
		return partition == "aws-us-gov"
	default:
		return partition == "aws"
	}
}

func signAWSKinesisVideoSignalingURL(rawURL, channelARN, clientID string, credentials AWSCredentials, region string, signingTime time.Time) (string, error) {
	if credentials.AccessKeyID == "" || credentials.SecretAccessKey == "" {
		return "", fmt.Errorf("AWS Kinesis Video signaling requires complete AKSK credentials")
	}
	target, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(target.Scheme, "wss") || target.RawQuery != "" || target.EscapedPath() != "/" {
		return "", fmt.Errorf("parse AWS Kinesis Video signaling URL")
	}
	now := signingTime.UTC()
	amzDate := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	region = strings.ToLower(strings.TrimSpace(region))
	scope := date + "/" + region + "/" + awsKinesisVideoService + "/aws4_request"
	query := make(url.Values)
	query.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	query.Set("X-Amz-ChannelARN", channelARN)
	if clientID != "" {
		query.Set("X-Amz-ClientId", clientID)
	}
	query.Set("X-Amz-Credential", credentials.AccessKeyID+"/"+scope)
	query.Set("X-Amz-Date", amzDate)
	query.Set("X-Amz-Expires", awsKinesisVideoSignalingExpires)
	query.Set("X-Amz-SignedHeaders", "host")
	// Unlike AWS IoT MQTT, Kinesis Video signaling requires the STS token in
	// the canonical query before the signature is calculated.
	if credentials.SessionToken != "" {
		query.Set("X-Amz-Security-Token", credentials.SessionToken)
	}
	canonicalQuery := query.Encode()
	host := strings.ToLower(target.Host)
	canonicalRequest := strings.Join([]string{
		http.MethodGet,
		"/",
		canonicalQuery,
		"host:" + host + "\n",
		"host",
		sha256Hex(nil),
	}, "\n")
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")
	signingKey := deriveAWSSigV4SigningKey(credentials.SecretAccessKey, region, awsKinesisVideoService, now)
	query.Set("X-Amz-Signature", hex.EncodeToString(hmacBytes(sha256.New, signingKey, []byte(stringToSign))))
	target.RawQuery = query.Encode()
	return target.String(), nil
}

func decodeAWSKinesisVideoSignalingEvent(data []byte, plan awsKinesisVideoSignalingPlan, correlations map[string]bool) ([]byte, bool, error) {
	if len(data) == 0 || len(data) > maxRequestPayloadBytes || !utf8.Valid(data) {
		return nil, false, fmt.Errorf("AWS Kinesis Video signaling returned an invalid text event")
	}
	var event awsKinesisVideoSignalingEvent
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&event) != nil || ensureJSONDecoderEOF(decoder) != nil {
		return nil, false, fmt.Errorf("AWS Kinesis Video signaling returned a malformed event")
	}
	if event.SenderClientID != "" && !awsKinesisVideoIdentifierPattern.MatchString(event.SenderClientID) {
		return nil, false, fmt.Errorf("AWS Kinesis Video signaling returned an invalid sender client ID")
	}
	event.MessageType = strings.ToUpper(strings.TrimSpace(event.MessageType))
	output := make(map[string]any)
	output["message_type"] = event.MessageType
	if event.SenderClientID != "" {
		output["sender_client_id"] = event.SenderClientID
	}
	terminal := false
	switch event.MessageType {
	case "SDP_OFFER", "SDP_ANSWER", "ICE_CANDIDATE":
		if event.StatusResponse != nil || event.MessagePayload == "" || len(event.MessagePayload) > awsKinesisVideoMaxPayloadBase64Bytes {
			return nil, false, fmt.Errorf("AWS Kinesis Video signaling returned an invalid payload event")
		}
		if plan.Role == "MASTER" && event.SenderClientID == "" {
			return nil, false, fmt.Errorf("AWS Kinesis Video master event omitted sender client ID")
		}
		payload, err := base64.StdEncoding.Strict().DecodeString(event.MessagePayload)
		if err != nil || len(payload) == 0 || !json.Valid(payload) {
			return nil, false, fmt.Errorf("AWS Kinesis Video signaling returned invalid Base64 JSON payload")
		}
		var object map[string]any
		payloadDecoder := json.NewDecoder(bytes.NewReader(payload))
		payloadDecoder.UseNumber()
		if payloadDecoder.Decode(&object) != nil || ensureJSONDecoderEOF(payloadDecoder) != nil || object == nil || alibabaNLSContainsCredentialField(object) {
			return nil, false, fmt.Errorf("AWS Kinesis Video signaling returned an unsafe payload object")
		}
		output["message_payload"] = object
	case "STATUS_RESPONSE":
		status := event.StatusResponse
		if status == nil || event.MessagePayload != "" || !awsKinesisVideoIdentifierPattern.MatchString(status.CorrelationID) || !correlations[status.CorrelationID] {
			return nil, false, fmt.Errorf("AWS Kinesis Video signaling returned an uncorrelated STATUS_RESPONSE")
		}
		if (status.ErrorType != "" && !awsKinesisVideoIdentifierPattern.MatchString(status.ErrorType)) || !validAWSKinesisVideoStatusField(status.Description, 1024) || (status.StatusCode != "" && !validAWSKinesisVideoStatusCode(status.StatusCode)) {
			return nil, false, fmt.Errorf("AWS Kinesis Video signaling returned a malformed STATUS_RESPONSE")
		}
		success := status.Success != nil && *status.Success && status.ErrorType == "" && (status.StatusCode == "" || strings.HasPrefix(status.StatusCode, "2"))
		if !success {
			return nil, false, fmt.Errorf("AWS Kinesis Video signaling STATUS_RESPONSE rejected correlation %s", status.CorrelationID)
		}
		output["correlation_id"] = status.CorrelationID
		output["success"] = true
	case "GO_AWAY", "RECONNECT_ICE_SERVER":
		if event.MessagePayload != "" || event.StatusResponse != nil {
			return nil, false, fmt.Errorf("AWS Kinesis Video signaling returned a malformed terminal event")
		}
		terminal = true
	default:
		return nil, false, fmt.Errorf("AWS Kinesis Video signaling returned an unsupported event type")
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return nil, false, fmt.Errorf("encode AWS Kinesis Video signaling event")
	}
	return encoded, terminal, nil
}

func validAWSKinesisVideoStatusField(value string, maximum int) bool {
	if value == "" {
		return true
	}
	return len(value) <= maximum && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

func validAWSKinesisVideoStatusCode(value string) bool {
	if len(value) != 3 {
		return false
	}
	number, err := strconv.Atoi(value)
	return err == nil && number >= 100 && number <= 599
}

func invokeAWSKinesisVideoSignalingWebSocket(ctx context.Context, adapter *AWSRESTAdapter, credentials AWSCredentials, invocation Invocation) (InvocationResult, error) {
	if err := validateAWSKinesisVideoSignalingInvocation(invocation); err != nil {
		return InvocationResult{}, err
	}
	plan, _ := parseAWSKinesisVideoSignalingPlan(invocation.Body, invocation.Region, invocation.Operation)
	signedURL, err := signAWSKinesisVideoSignalingURL(invocation.URL, plan.ChannelARN, plan.ClientID, credentials, invocation.Region, adapter.config.Now().UTC())
	if err != nil {
		return InvocationResult{}, err
	}
	dialCtx, cancelDial := context.WithTimeout(ctx, adapter.config.Timeout)
	connection, err := adapter.config.SigV4WebSocketDial(dialCtx, signedURL, make(http.Header))
	cancelDial()
	if err != nil {
		return InvocationResult{}, err
	}
	defer connection.Close()
	sink, err := newWebSocketOutputSink(invocation, adapter.config.MaxBodyBytes, "AWS Kinesis Video signaling WebSocket")
	if err != nil {
		return InvocationResult{}, err
	}
	defer sink.abort()
	sessionCtx, cancelSession := context.WithTimeout(ctx, time.Duration(plan.TimeoutSeconds)*time.Second)
	defer cancelSession()
	for index, message := range plan.Messages {
		if err := connection.Write(sessionCtx, cloudWebSocketMessageText, message.encoded); err != nil {
			return InvocationResult{}, fmt.Errorf("send AWS Kinesis Video signaling message")
		}
		if index+1 < len(plan.Messages) {
			if err := adapter.config.StreamPause(sessionCtx, awsKinesisVideoMessageInterval); err != nil {
				return InvocationResult{}, fmt.Errorf("pace AWS Kinesis Video signaling messages")
			}
		}
	}
	correlations := make(map[string]bool, len(plan.Messages))
	for _, message := range plan.Messages {
		correlations[message.CorrelationID] = true
	}
	received := 0
	terminal := false
	for received < plan.MaxMessages && !terminal {
		messageType, data, readErr := connection.Read(sessionCtx)
		if errors.Is(readErr, context.DeadlineExceeded) {
			break
		}
		if websocket.CloseStatus(readErr) == websocket.StatusNormalClosure {
			break
		}
		if readErr != nil {
			return InvocationResult{}, fmt.Errorf("read AWS Kinesis Video signaling event")
		}
		if messageType != cloudWebSocketMessageText {
			return InvocationResult{}, fmt.Errorf("AWS Kinesis Video signaling returned a non-text event")
		}
		encoded, ended, err := decodeAWSKinesisVideoSignalingEvent(data, plan, correlations)
		if err != nil {
			return InvocationResult{}, err
		}
		if err := sink.writeMessage(encoded); err != nil {
			return InvocationResult{}, err
		}
		received++
		terminal = ended
	}
	requestID := plan.ClientID
	if requestID == "" {
		requestID = "MASTER"
	}
	metadata, err := sink.finish(requestID)
	if err != nil {
		return InvocationResult{}, err
	}
	var summary map[string]any
	if json.Unmarshal(metadata, &summary) == nil {
		summary["messages"] = received
		metadata, err = json.Marshal(summary)
		if err != nil {
			return InvocationResult{}, fmt.Errorf("encode AWS Kinesis Video signaling output metadata")
		}
	}
	return InvocationResult{Output: metadata, RequestID: requestID}, nil
}

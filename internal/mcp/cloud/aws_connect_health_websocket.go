package cloud

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/smithy-go/eventstream"
	"github.com/coder/websocket"
)

const (
	authSchemeAWSConnectHealthWS             = "connect-health-ws"
	awsConnectHealthWebSocketService         = "health-agent"
	awsConnectHealthWebSocketOperation       = "startmedicalscribelisteningsession"
	awsConnectHealthWebSocketPath            = "/medical-scribe-stream-websocket"
	awsConnectHealthWebSocketExpires         = "60"
	defaultAWSConnectHealthChunkInterval     = 100 * time.Millisecond
	maxAWSConnectHealthAudioChunkBytes       = 256 * 1024
	maxAWSConnectHealthEncounterContextBytes = 10 * 1024
)

var awsConnectHealthSessionIDPattern = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)
var awsConnectHealthDomainIDPattern = regexp.MustCompile(`^(?:hai-|dom-)[a-z0-9]{16,21}$`)
var awsConnectHealthSubscriptionIDPattern = regexp.MustCompile(`^sub-[A-Za-z0-9]{21}$`)
var awsConnectHealthS3BucketPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{1,61}[a-z0-9])?$`)

var awsConnectHealthParameterNames = map[string]struct{}{
	"session-id": {}, "domain-id": {}, "subscription-id": {}, "language-code": {}, "sample-rate": {}, "media-encoding": {},
}

func validateAWSConnectHealthWebSocketInvocation(invocation Invocation) error {
	if !strings.EqualFold(invocation.Method, http.MethodGet) || !strings.EqualFold(invocation.Service, awsConnectHealthWebSocketService) || !strings.EqualFold(invocation.Operation, awsConnectHealthWebSocketOperation) {
		return fmt.Errorf("AWS Connect Health WebSocket requires GET, service health-agent, and operation StartMedicalScribeListeningSession")
	}
	if invocation.Region != "us-east-1" && invocation.Region != "us-west-2" {
		return fmt.Errorf("AWS Connect Health WebSocket is available only in us-east-1 and us-west-2")
	}
	if err := validateAWSConnectHealthWebSocketTarget(invocation.URL, invocation.Region); err != nil {
		return err
	}
	if len(invocation.Headers) != 0 {
		return fmt.Errorf("AWS Connect Health WebSocket does not accept caller-supplied handshake headers")
	}
	if invocation.Body == nil || invocation.BodyFile == "" || invocation.ResponseFile == "" {
		return fmt.Errorf("AWS Connect Health WebSocket requires configuration body, finite audio body_file, and response_file")
	}
	if invocation.RegionSet != "" || invocation.Project != "" || invocation.Subscription != "" || invocation.Audience != "" || invocation.AuthVersion != "" || invocation.APIVersion != "" || invocation.PayloadMode != "" || invocation.ChecksumAlgorithm != "" || invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return fmt.Errorf("AWS Connect Health WebSocket does not accept cross-provider, REST payload, checksum, SigV4a, or Tencent stream controls")
	}
	parameters, err := validateAWSConnectHealthParameters(invocation.Parameters)
	if err != nil {
		return err
	}
	configuration, channels, err := validateAWSConnectHealthConfiguration(invocation.Body)
	if err != nil {
		return err
	}
	_ = configuration
	if invocation.StreamChunkBytes < 0 || invocation.StreamChunkBytes > maxAWSConnectHealthAudioChunkBytes {
		return fmt.Errorf("AWS Connect Health WebSocket audio chunks must be between 1 and %d bytes when provided", maxAWSConnectHealthAudioChunkBytes)
	}
	if invocation.StreamIntervalMS < 0 || invocation.StreamIntervalMS > 5000 {
		return fmt.Errorf("AWS Connect Health WebSocket stream_interval_ms must not exceed 5000")
	}
	if invocation.StreamChunkBytes > 0 && parameters["media-encoding"] == "pcm" {
		sampleRate, _ := strconv.Atoi(parameters["sample-rate"])
		if invocation.StreamChunkBytes > sampleRate*channels*2 {
			return fmt.Errorf("AWS Connect Health WebSocket PCM audio chunks must not exceed one second")
		}
	}
	return nil
}

func validateAWSConnectHealthWebSocketTarget(rawURL, region string) error {
	target, err := url.Parse(rawURL)
	expectedHost := "streaming.health-agent." + strings.ToLower(region) + ".api.aws"
	if err != nil || !strings.EqualFold(target.Scheme, "wss") || strings.ToLower(target.Hostname()) != expectedHost || target.Port() != "" || target.EscapedPath() != awsConnectHealthWebSocketPath || target.RawQuery != "" || target.User != nil || target.Fragment != "" {
		return fmt.Errorf("AWS Connect Health WebSocket requires the exact regional wss://streaming.health-agent.<region>.api.aws/medical-scribe-stream-websocket endpoint")
	}
	return nil
}

func validateAWSConnectHealthParameters(parameters map[string]any) (map[string]string, error) {
	if len(parameters) != len(awsConnectHealthParameterNames) {
		return nil, fmt.Errorf("AWS Connect Health WebSocket requires exactly the six documented session parameters")
	}
	values := make(map[string]string, len(parameters))
	for name, value := range parameters {
		if _, supported := awsConnectHealthParameterNames[name]; !supported || isAWSSigV4WebSocketCredentialName(name) {
			return nil, fmt.Errorf("AWS Connect Health WebSocket query parameter %q is forbidden or unsupported", name)
		}
		entries, err := stringValues(value)
		if err != nil || len(entries) != 1 || entries[0] == "" {
			return nil, fmt.Errorf("AWS Connect Health WebSocket query parameter %q must be one non-empty scalar", name)
		}
		values[name] = entries[0]
	}
	if !awsConnectHealthSessionIDPattern.MatchString(values["session-id"]) {
		return nil, fmt.Errorf("AWS Connect Health WebSocket session-id must be a UUID")
	}
	if !awsConnectHealthDomainIDPattern.MatchString(values["domain-id"]) {
		return nil, fmt.Errorf("AWS Connect Health WebSocket domain-id is malformed")
	}
	if !awsConnectHealthSubscriptionIDPattern.MatchString(values["subscription-id"]) {
		return nil, fmt.Errorf("AWS Connect Health WebSocket subscription-id is malformed")
	}
	if values["language-code"] != "en-US" {
		return nil, fmt.Errorf("AWS Connect Health WebSocket language-code must be en-US")
	}
	if values["media-encoding"] != "pcm" && values["media-encoding"] != "flac" {
		return nil, fmt.Errorf("AWS Connect Health WebSocket media-encoding must be pcm or flac")
	}
	sampleRate, err := strconv.Atoi(values["sample-rate"])
	if err != nil || sampleRate < 8000 || sampleRate > 48000 {
		return nil, fmt.Errorf("AWS Connect Health WebSocket sample-rate must be between 8000 and 48000")
	}
	return values, nil
}

func validateAWSConnectHealthConfiguration(body any) ([]byte, int, error) {
	configuration, ok := body.(map[string]any)
	if !ok || alibabaNLSContainsCredentialField(configuration) {
		return nil, 0, fmt.Errorf("AWS Connect Health WebSocket configuration must be a credential-free JSON object")
	}
	encoded, err := json.Marshal(configuration)
	if err != nil || len(encoded) == 0 || len(encoded) > maxRequestPayloadBytes || !json.Valid(encoded) {
		return nil, 0, fmt.Errorf("AWS Connect Health WebSocket configuration must be bounded valid JSON")
	}
	postActions, ok := configuration["postStreamActionSettings"].(map[string]any)
	if !ok {
		return nil, 0, fmt.Errorf("AWS Connect Health WebSocket configuration requires postStreamActionSettings")
	}
	outputS3URI, ok := postActions["outputS3Uri"].(string)
	if !ok || !validAWSConnectHealthS3URI(outputS3URI) {
		return nil, 0, fmt.Errorf("AWS Connect Health WebSocket configuration requires a valid outputS3Uri")
	}
	noteSettings, ok := postActions["clinicalNoteGenerationSettings"].(map[string]any)
	if !ok {
		return nil, 0, fmt.Errorf("AWS Connect Health WebSocket configuration requires clinicalNoteGenerationSettings")
	}
	templateSettings, ok := noteSettings["noteTemplateSettings"].(map[string]any)
	if !ok {
		return nil, 0, fmt.Errorf("AWS Connect Health WebSocket configuration requires noteTemplateSettings")
	}
	_, managed := templateSettings["managedTemplate"].(map[string]any)
	_, custom := templateSettings["customTemplate"].(map[string]any)
	if managed == custom {
		return nil, 0, fmt.Errorf("AWS Connect Health WebSocket noteTemplateSettings requires exactly one managedTemplate or customTemplate")
	}
	channels := 1
	if definitions, present := configuration["channelDefinitions"]; present {
		entries, ok := definitions.([]any)
		if !ok || len(entries) != 2 {
			return nil, 0, fmt.Errorf("AWS Connect Health WebSocket channelDefinitions must contain exactly two channels when provided")
		}
		seen := make(map[int]struct{}, len(entries))
		for _, entry := range entries {
			definition, ok := entry.(map[string]any)
			if !ok {
				return nil, 0, fmt.Errorf("AWS Connect Health WebSocket channelDefinitions entry is malformed")
			}
			channelID, ok := jsonInteger(definition["channelId"])
			role, roleOK := definition["participantRole"].(string)
			if !ok || channelID < 0 || channelID > 1 || !roleOK || (role != "CLINICIAN" && role != "PATIENT") {
				return nil, 0, fmt.Errorf("AWS Connect Health WebSocket channel definition is invalid")
			}
			if _, duplicate := seen[channelID]; duplicate {
				return nil, 0, fmt.Errorf("AWS Connect Health WebSocket channel IDs must be unique")
			}
			seen[channelID] = struct{}{}
		}
		channels = len(entries)
	}
	if encounter, present := configuration["encounterContext"]; present {
		contextObject, ok := encounter.(map[string]any)
		value, valueOK := contextObject["unstructuredContext"].(string)
		if !ok || !valueOK || len(value) > maxAWSConnectHealthEncounterContextBytes {
			return nil, 0, fmt.Errorf("AWS Connect Health WebSocket encounterContext must contain at most 10 KiB of unstructuredContext")
		}
	}
	return encoded, channels, nil
}

func jsonInteger(value any) (int, bool) {
	switch number := value.(type) {
	case int:
		return number, true
	case float64:
		integer := int(number)
		return integer, number == float64(integer)
	case json.Number:
		integer, err := strconv.Atoi(string(number))
		return integer, err == nil
	default:
		return 0, false
	}
}

func validAWSConnectHealthS3URI(rawURI string) bool {
	target, err := url.Parse(rawURI)
	if err != nil || len(rawURI) > 1024 || target.Scheme != "s3" || !awsConnectHealthS3BucketPattern.MatchString(target.Host) || target.User != nil || target.RawQuery != "" || target.Fragment != "" {
		return false
	}
	return true
}

func signAWSConnectHealthWebSocketURL(ctx context.Context, rawURL string, credentials AWSCredentials, region string, parameters map[string]any, signingTime time.Time) (string, []byte, error) {
	if credentials.AccessKeyID == "" || credentials.SecretAccessKey == "" {
		return "", nil, fmt.Errorf("AWS Connect Health WebSocket requires complete AKSK credentials")
	}
	if err := validateAWSConnectHealthWebSocketTarget(rawURL, region); err != nil {
		return "", nil, err
	}
	values, err := validateAWSConnectHealthParameters(parameters)
	if err != nil {
		return "", nil, err
	}
	target, _ := url.Parse(rawURL)
	query := target.Query()
	for name, value := range values {
		query.Set(name, value)
	}
	query.Set("X-Amz-Expires", awsConnectHealthWebSocketExpires)
	target.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return "", nil, fmt.Errorf("build AWS Connect Health WebSocket upgrade request")
	}
	awsCredentials := aws.Credentials{AccessKeyID: credentials.AccessKeyID, SecretAccessKey: credentials.SecretAccessKey, SessionToken: credentials.SessionToken}
	signedURL, _, err := awsv4.NewSigner().PresignHTTP(ctx, awsCredentials, request, sha256Hex(nil), awsConnectHealthWebSocketService, region, signingTime.UTC())
	if err != nil {
		return "", nil, fmt.Errorf("presign AWS Connect Health WebSocket upgrade")
	}
	parsed, err := url.Parse(signedURL)
	if err != nil {
		return "", nil, fmt.Errorf("parse signed AWS Connect Health WebSocket URL")
	}
	seed, err := hex.DecodeString(parsed.Query().Get("X-Amz-Signature"))
	if err != nil || len(seed) != sha256.Size {
		return "", nil, fmt.Errorf("AWS Connect Health WebSocket presign returned an invalid seed signature")
	}
	return signedURL, seed, nil
}

type awsConnectHealthWebSocketFrameSigner struct {
	signer  *awsv4.StreamSigner
	encoder *eventstream.Encoder
	now     func() time.Time
}

func newAWSConnectHealthWebSocketFrameSigner(credentials aws.Credentials, region string, seed []byte, now func() time.Time) *awsConnectHealthWebSocketFrameSigner {
	return &awsConnectHealthWebSocketFrameSigner{
		signer: awsv4.NewStreamSigner(credentials, awsConnectHealthWebSocketService, region, seed), encoder: eventstream.NewEncoder(), now: now,
	}
}

func (signer *awsConnectHealthWebSocketFrameSigner) Encode(eventType, contentType string, payload []byte) ([]byte, error) {
	inner := eventstream.Message{Payload: payload}
	inner.Headers.Set(eventstream.ContentTypeHeader, eventstream.StringValue(contentType))
	inner.Headers.Set(eventstream.EventTypeHeader, eventstream.StringValue(eventType))
	inner.Headers.Set(eventstream.MessageTypeHeader, eventstream.StringValue(eventstream.EventMessageType))
	var innerBytes bytes.Buffer
	if err := signer.encoder.Encode(&innerBytes, inner); err != nil {
		return nil, fmt.Errorf("encode AWS Connect Health WebSocket event")
	}
	now := signer.now().UTC()
	outer := eventstream.Message{Payload: innerBytes.Bytes()}
	outer.Headers.Set(eventstream.DateHeader, eventstream.TimestampValue(now))
	var signingHeaders bytes.Buffer
	if err := eventstream.EncodeHeaders(&signingHeaders, outer.Headers); err != nil {
		return nil, fmt.Errorf("encode AWS Connect Health WebSocket signing headers")
	}
	signature, err := signer.signer.GetSignature(context.Background(), signingHeaders.Bytes(), outer.Payload, now)
	if err != nil {
		return nil, fmt.Errorf("sign AWS Connect Health WebSocket event")
	}
	outer.Headers.Set(eventstream.ChunkSignatureHeader, eventstream.BytesValue(signature))
	var output bytes.Buffer
	if err := signer.encoder.Encode(&output, outer); err != nil {
		return nil, fmt.Errorf("encode signed AWS Connect Health WebSocket event")
	}
	return output.Bytes(), nil
}

func acceptAWSConnectHealthWebSocketMessage(messageType cloudWebSocketMessageType, data []byte) ([]byte, string, error) {
	if messageType != cloudWebSocketMessageBinary {
		return nil, "", fmt.Errorf("AWS Connect Health WebSocket returned a non-binary response")
	}
	message, err := decodeSingleAWSEventStreamMessage(data)
	if err != nil {
		return nil, "", fmt.Errorf("AWS Connect Health WebSocket returned an invalid event-stream frame")
	}
	if message.Headers.Get(eventstream.DateHeader) != nil || message.Headers.Get(eventstream.ChunkSignatureHeader) != nil {
		if message.Headers.Get(eventstream.DateHeader) == nil || message.Headers.Get(eventstream.ChunkSignatureHeader) == nil {
			return nil, "", fmt.Errorf("AWS Connect Health WebSocket returned an incomplete signed event-stream frame")
		}
		message, err = decodeSingleAWSEventStreamMessage(message.Payload)
		if err != nil {
			return nil, "", fmt.Errorf("AWS Connect Health WebSocket returned an invalid event payload")
		}
	}
	eventType, ok := awsEventStreamStringHeader(message, eventstream.EventTypeHeader)
	if !ok || eventType == "" {
		return nil, "", fmt.Errorf("AWS Connect Health WebSocket response omitted event type")
	}
	messageKind, ok := awsEventStreamStringHeader(message, eventstream.MessageTypeHeader)
	if !ok || (messageKind != eventstream.EventMessageType && messageKind != eventstream.ExceptionMessageType) {
		return nil, "", fmt.Errorf("AWS Connect Health WebSocket response has an invalid message type")
	}
	contentType, ok := awsEventStreamStringHeader(message, eventstream.ContentTypeHeader)
	if !ok || contentType != "application/json" || !json.Valid(message.Payload) {
		return nil, "", fmt.Errorf("AWS Connect Health WebSocket response payload is not valid JSON")
	}
	if messageKind == eventstream.ExceptionMessageType || eventType != "transcriptEvent" {
		return nil, eventType, fmt.Errorf("AWS Connect Health WebSocket returned %s", eventType)
	}
	return message.Payload, eventType, nil
}

func invokeAWSConnectHealthWebSocket(ctx context.Context, adapter *AWSRESTAdapter, credentials AWSCredentials, invocation Invocation) (InvocationResult, error) {
	if err := validateAWSConnectHealthWebSocketInvocation(invocation); err != nil {
		return InvocationResult{}, err
	}
	configuration, channels, _ := validateAWSConnectHealthConfiguration(invocation.Body)
	signedURL, seed, err := signAWSConnectHealthWebSocketURL(ctx, invocation.URL, credentials, invocation.Region, invocation.Parameters, adapter.config.Now().UTC())
	if err != nil {
		return InvocationResult{}, err
	}
	dialCtx, cancelDial := context.WithTimeout(ctx, adapter.config.Timeout)
	connection, err := adapter.config.ConnectHealthWebSocketDial(dialCtx, signedURL)
	cancelDial()
	if err != nil {
		return InvocationResult{}, err
	}
	defer connection.Close()
	sink, err := newWebSocketOutputSink(invocation, adapter.config.MaxBodyBytes, "AWS Connect Health WebSocket")
	if err != nil {
		return InvocationResult{}, err
	}
	defer sink.abort()

	streamCtx, cancelStream := awsConnectHealthStreamContext(ctx, invocation, channels)
	defer cancelStream()
	readResult := make(chan error, 1)
	go readAWSConnectHealthWebSocket(streamCtx, connection, sink, readResult)
	awsCredentials := aws.Credentials{AccessKeyID: credentials.AccessKeyID, SecretAccessKey: credentials.SecretAccessKey, SessionToken: credentials.SessionToken}
	frameSigner := newAWSConnectHealthWebSocketFrameSigner(awsCredentials, invocation.Region, seed, adapter.config.Now)
	configurationFrame, err := frameSigner.Encode("configurationEvent", "application/json", configuration)
	if err != nil || connection.Write(streamCtx, cloudWebSocketMessageBinary, configurationFrame) != nil {
		cancelStream()
		<-readResult
		return InvocationResult{}, fmt.Errorf("write AWS Connect Health WebSocket configuration event")
	}
	if err := streamAWSConnectHealthAudio(streamCtx, connection, invocation, channels, frameSigner, adapter.config.StreamPause); err != nil {
		cancelStream()
		readerErr := <-readResult
		if readerErr != nil {
			return InvocationResult{}, readerErr
		}
		return InvocationResult{}, err
	}
	terminal, err := frameSigner.Encode("sessionControlEvent", "application/json", []byte(`{"type":"END_OF_SESSION"}`))
	if err != nil || connection.Write(streamCtx, cloudWebSocketMessageBinary, terminal) != nil {
		cancelStream()
		<-readResult
		return InvocationResult{}, fmt.Errorf("finish AWS Connect Health WebSocket session")
	}
	if err := <-readResult; err != nil {
		return InvocationResult{}, err
	}
	output, err := sink.finish("")
	if err != nil {
		return InvocationResult{}, err
	}
	return InvocationResult{Output: output}, nil
}

func readAWSConnectHealthWebSocket(ctx context.Context, connection cloudWebSocketConnection, sink *cloudWebSocketOutputSink, result chan<- error) {
	for {
		messageType, data, err := connection.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				result <- nil
				return
			}
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				result <- fmt.Errorf("AWS Connect Health WebSocket ended before a normal provider close")
				return
			}
			result <- fmt.Errorf("read AWS Connect Health WebSocket response")
			return
		}
		payload, _, err := acceptAWSConnectHealthWebSocketMessage(messageType, data)
		if err != nil {
			result <- err
			return
		}
		if err := sink.writeMessage(payload); err != nil {
			result <- err
			return
		}
	}
}

func streamAWSConnectHealthAudio(ctx context.Context, connection cloudWebSocketConnection, invocation Invocation, channels int, signer *awsConnectHealthWebSocketFrameSigner, pause func(context.Context, time.Duration) error) error {
	audio, err := os.Open(invocation.BodyFile)
	if err != nil {
		return fmt.Errorf("open AWS Connect Health audio body_file: %w", err)
	}
	defer audio.Close()
	chunkBytes := invocation.StreamChunkBytes
	if chunkBytes == 0 {
		chunkBytes = defaultAWSConnectHealthChunkBytes(invocation.Parameters, channels)
	}
	interval := time.Duration(invocation.StreamIntervalMS) * time.Millisecond
	if interval == 0 {
		interval = defaultAWSConnectHealthChunkInterval
	}
	buffer := make([]byte, chunkBytes)
	for {
		count, readErr := audio.Read(buffer)
		if count > 0 {
			frame, err := signer.Encode("binaryAudioEvent", "application/octet-stream", buffer[:count])
			if err != nil {
				return err
			}
			if err := connection.Write(ctx, cloudWebSocketMessageBinary, frame); err != nil {
				return fmt.Errorf("write AWS Connect Health audio event")
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read AWS Connect Health audio body_file: %w", readErr)
		}
		if count > 0 {
			if err := pause(ctx, interval); err != nil {
				return fmt.Errorf("pace AWS Connect Health audio stream: %w", err)
			}
		}
	}
	return nil
}

func defaultAWSConnectHealthChunkBytes(parameters map[string]any, channels int) int {
	values, err := validateAWSConnectHealthParameters(parameters)
	if err == nil && values["media-encoding"] == "pcm" {
		sampleRate, _ := strconv.Atoi(values["sample-rate"])
		if size := sampleRate * channels * 2 / 10; size > 0 && size <= maxAWSConnectHealthAudioChunkBytes {
			return size
		}
	}
	return 4096
}

func awsConnectHealthStreamContext(ctx context.Context, invocation Invocation, channels int) (context.Context, context.CancelFunc) {
	chunkBytes := invocation.StreamChunkBytes
	if chunkBytes == 0 {
		chunkBytes = defaultAWSConnectHealthChunkBytes(invocation.Parameters, channels)
	}
	interval := invocation.StreamIntervalMS
	if interval == 0 {
		interval = int(defaultAWSConnectHealthChunkInterval / time.Millisecond)
	}
	duration := 2 * time.Minute
	if info, err := os.Stat(invocation.BodyFile); err == nil && info.Size() > 0 {
		chunks := (info.Size() + int64(chunkBytes) - 1) / int64(chunkBytes)
		duration += time.Duration(chunks) * time.Duration(interval) * time.Millisecond
	}
	if duration > time.Hour {
		duration = time.Hour
	}
	return context.WithTimeout(ctx, duration)
}

func defaultAWSConnectHealthWebSocketDial(ctx context.Context, signedURL string) (cloudWebSocketConnection, error) {
	client := &http.Client{Transport: http.DefaultTransport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	connection, response, err := websocket.Dial(ctx, signedURL, &websocket.DialOptions{HTTPClient: client, CompressionMode: websocket.CompressionDisabled})
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("AWS Connect Health WebSocket handshake failed with HTTP %d", response.StatusCode)
		}
		return nil, fmt.Errorf("AWS Connect Health WebSocket handshake failed")
	}
	connection.SetReadLimit(maxAWSEventStreamFrameBytes)
	return coderTencentWebSocketConnection{connection: connection}, nil
}

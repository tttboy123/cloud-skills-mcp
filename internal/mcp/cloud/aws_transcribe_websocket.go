package cloud

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	authSchemeAWSTranscribeWS         = "transcribe-ws"
	awsTranscribeWebSocketService     = "transcribe"
	awsTranscribeWebSocketPort        = "8443"
	awsTranscribeWebSocketExpires     = "300"
	defaultAWSTranscribeChunkInterval = 100 * time.Millisecond
	maxAWSTranscribeAudioChunkBytes   = 256 * 1024
)

var awsTranscribeQueryNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,127}$`)
var awsTranscribeSessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,200}$`)

var awsTranscribeWebSocketOperations = map[string]string{
	"/stream-transcription-websocket":                "startstreamtranscriptionwebsocket",
	"/medical-stream-transcription-websocket":        "startmedicalstreamtranscriptionwebsocket",
	"/call-analytics-stream-transcription-websocket": "startcallanalyticsstreamtranscriptionwebsocket",
}

func validateAWSTranscribeWebSocketInvocation(invocation Invocation) error {
	if !strings.EqualFold(invocation.Method, http.MethodGet) {
		return fmt.Errorf("AWS Transcribe WebSocket requires method GET for the HTTP upgrade")
	}
	if !strings.EqualFold(invocation.Service, awsTranscribeWebSocketService) {
		return fmt.Errorf("AWS Transcribe WebSocket requires service transcribe")
	}
	if !identifierPattern.MatchString(invocation.Region) {
		return fmt.Errorf("AWS Transcribe WebSocket requires a valid region")
	}
	target, err := url.Parse(invocation.URL)
	if err != nil || !strings.EqualFold(target.Scheme, "wss") || target.Port() != awsTranscribeWebSocketPort || target.RawQuery != "" || target.User != nil || target.Fragment != "" {
		return fmt.Errorf("AWS Transcribe WebSocket requires an official wss://transcribestreaming.<region> endpoint on port 8443 without caller query parameters")
	}
	expectedOperation, pathSupported := awsTranscribeWebSocketOperations[target.EscapedPath()]
	if !pathSupported || !strings.EqualFold(invocation.Operation, expectedOperation) {
		return fmt.Errorf("AWS Transcribe WebSocket operation does not match its official streaming path")
	}
	if !isAWSTranscribeStreamingHost(target.Hostname(), invocation.Region) {
		return fmt.Errorf("AWS Transcribe WebSocket host does not match the requested region")
	}
	if invocation.BodyFile == "" {
		return fmt.Errorf("AWS Transcribe WebSocket requires body_file for the finite audio stream")
	}
	if len(invocation.Headers) != 0 {
		return fmt.Errorf("AWS Transcribe WebSocket does not accept caller-supplied handshake headers")
	}
	if invocation.PayloadMode != "" || invocation.ChecksumAlgorithm != "" || invocation.RegionSet != "" {
		return fmt.Errorf("AWS Transcribe WebSocket does not accept REST payload, checksum, or SigV4a controls")
	}
	if invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return fmt.Errorf("AWS Transcribe WebSocket does not accept Tencent MPS frame controls")
	}
	if invocation.StreamChunkBytes > maxAWSTranscribeAudioChunkBytes {
		return fmt.Errorf("AWS Transcribe WebSocket audio chunks must not exceed %d bytes", maxAWSTranscribeAudioChunkBytes)
	}
	if err := validateAWSTranscribeQueryParameters(invocation.Parameters, target.EscapedPath()); err != nil {
		return err
	}
	if invocation.StreamChunkBytes > 0 && strings.EqualFold(awsTranscribeParameter(invocation.Parameters, "media-encoding"), "pcm") && invocation.StreamChunkBytes > maxAWSTranscribePCMChunkBytes(invocation.Parameters) {
		return fmt.Errorf("AWS Transcribe WebSocket PCM audio chunks must not exceed one second")
	}
	if invocation.Body != nil {
		if _, ok := invocation.Body.(map[string]any); !ok {
			return fmt.Errorf("AWS Transcribe WebSocket configuration body must be a JSON object")
		}
		configuration, err := json.Marshal(invocation.Body)
		if err != nil || len(configuration) == 0 || len(configuration) > maxRequestPayloadBytes || !json.Valid(configuration) {
			return fmt.Errorf("AWS Transcribe WebSocket configuration body must be bounded valid JSON")
		}
	}
	return nil
}

func isAWSTranscribeStreamingHost(host, region string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	region = strings.ToLower(strings.TrimSpace(region))
	for _, prefix := range []string{"transcribestreaming.", "transcribestreaming-fips."} {
		for _, suffix := range []string{".amazonaws.com", ".api.aws", ".amazonaws.com.cn"} {
			if host == prefix+region+suffix {
				return true
			}
		}
	}
	return false
}

func validateAWSTranscribeQueryParameters(parameters map[string]any, path string) error {
	values := make(map[string]string, len(parameters))
	for name, value := range parameters {
		lower := strings.ToLower(strings.TrimSpace(name))
		if strings.HasPrefix(lower, "x-amz-") || !awsTranscribeQueryNamePattern.MatchString(lower) || lower != name {
			return fmt.Errorf("caller-supplied AWS Transcribe WebSocket query parameter %q is forbidden or malformed", name)
		}
		entries, err := stringValues(value)
		if err != nil || len(entries) != 1 || entries[0] == "" {
			return fmt.Errorf("AWS Transcribe WebSocket query parameter %q must be one non-empty scalar", name)
		}
		values[lower] = entries[0]
	}
	encoding := strings.ToLower(values["media-encoding"])
	if encoding != "pcm" && encoding != "ogg-opus" && encoding != "flac" {
		return fmt.Errorf("AWS Transcribe WebSocket media-encoding must be pcm, ogg-opus, or flac")
	}
	sampleRate, err := strconv.Atoi(values["sample-rate"])
	if err != nil || sampleRate < 8000 || sampleRate > 48000 {
		return fmt.Errorf("AWS Transcribe WebSocket sample-rate must be between 8000 and 48000")
	}
	if sessionID := values["session-id"]; sessionID != "" && !awsTranscribeSessionIDPattern.MatchString(sessionID) {
		return fmt.Errorf("AWS Transcribe WebSocket session-id is malformed")
	}
	if channels := values["number-of-channels"]; channels != "" && channels != "2" {
		return fmt.Errorf("AWS Transcribe WebSocket number-of-channels must be 2 when provided")
	}
	if path == "/medical-stream-transcription-websocket" {
		if values["language-code"] == "" || values["specialty"] == "" || values["type"] == "" {
			return fmt.Errorf("AWS Transcribe Medical WebSocket requires language-code, specialty, and type")
		}
	} else if values["language-code"] == "" && values["identify-language"] == "" && values["identify-multiple-languages"] == "" {
		return fmt.Errorf("AWS Transcribe WebSocket requires a documented language selection parameter")
	}
	return nil
}

func signAWSTranscribeWebSocketURL(ctx context.Context, rawURL string, credentials AWSCredentials, region string, parameters map[string]any, signingTime time.Time) (string, []byte, error) {
	if credentials.AccessKeyID == "" || credentials.SecretAccessKey == "" {
		return "", nil, fmt.Errorf("AWS Transcribe WebSocket requires complete AKSK credentials")
	}
	target, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(target.Scheme, "wss") || target.RawQuery != "" {
		return "", nil, fmt.Errorf("parse AWS Transcribe WebSocket URL")
	}
	if !isAWSTranscribeStreamingHost(target.Hostname(), region) || target.Port() != awsTranscribeWebSocketPort {
		return "", nil, fmt.Errorf("AWS Transcribe WebSocket host does not match the requested region")
	}
	if _, supported := awsTranscribeWebSocketOperations[target.EscapedPath()]; !supported {
		return "", nil, fmt.Errorf("AWS Transcribe WebSocket path is unsupported")
	}
	if err := validateAWSTranscribeQueryParameters(parameters, target.EscapedPath()); err != nil {
		return "", nil, err
	}
	query := target.Query()
	for name, value := range parameters {
		entries, _ := stringValues(value)
		query.Set(name, entries[0])
	}
	query.Set("X-Amz-Expires", awsTranscribeWebSocketExpires)
	target.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return "", nil, fmt.Errorf("build AWS Transcribe WebSocket upgrade request: %w", err)
	}
	awsCredentials := aws.Credentials{
		AccessKeyID: credentials.AccessKeyID, SecretAccessKey: credentials.SecretAccessKey, SessionToken: credentials.SessionToken,
	}
	signedURL, _, err := awsv4.NewSigner().PresignHTTP(ctx, awsCredentials, request, sha256Hex(nil), awsTranscribeWebSocketService, strings.ToLower(region), signingTime.UTC())
	if err != nil {
		return "", nil, fmt.Errorf("presign AWS Transcribe WebSocket upgrade: %w", err)
	}
	parsed, err := url.Parse(signedURL)
	if err != nil {
		return "", nil, fmt.Errorf("parse signed AWS Transcribe WebSocket URL")
	}
	seed, err := hex.DecodeString(parsed.Query().Get("X-Amz-Signature"))
	if err != nil || len(seed) != sha256.Size {
		return "", nil, fmt.Errorf("AWS Transcribe WebSocket presign returned an invalid seed signature")
	}
	return signedURL, seed, nil
}

type awsTranscribeWebSocketFrameSigner struct {
	signer  *awsv4.StreamSigner
	encoder *eventstream.Encoder
	now     func() time.Time
}

func newAWSTranscribeWebSocketFrameSigner(credentials aws.Credentials, region string, seed []byte, now func() time.Time) *awsTranscribeWebSocketFrameSigner {
	return &awsTranscribeWebSocketFrameSigner{
		signer:  awsv4.NewStreamSigner(credentials, awsTranscribeWebSocketService, strings.ToLower(region), seed),
		encoder: eventstream.NewEncoder(), now: now,
	}
}

func (signer *awsTranscribeWebSocketFrameSigner) Encode(eventType, contentType string, payload []byte) ([]byte, error) {
	inner := eventstream.Message{Payload: payload}
	inner.Headers.Set(":content-type", eventstream.StringValue(contentType))
	inner.Headers.Set(":event-type", eventstream.StringValue(eventType))
	inner.Headers.Set(":message-type", eventstream.StringValue("event"))
	var innerBytes bytes.Buffer
	if err := signer.encoder.Encode(&innerBytes, inner); err != nil {
		return nil, fmt.Errorf("encode AWS Transcribe WebSocket event: %w", err)
	}

	now := signer.now().UTC()
	outer := eventstream.Message{Payload: innerBytes.Bytes()}
	outer.Headers.Set(eventstream.DateHeader, eventstream.TimestampValue(now))
	var signingHeaders bytes.Buffer
	if err := eventstream.EncodeHeaders(&signingHeaders, outer.Headers); err != nil {
		return nil, fmt.Errorf("encode AWS Transcribe WebSocket signing headers: %w", err)
	}
	signature, err := signer.signer.GetSignature(context.Background(), signingHeaders.Bytes(), outer.Payload, now)
	if err != nil {
		return nil, fmt.Errorf("sign AWS Transcribe WebSocket event: %w", err)
	}
	outer.Headers.Set(eventstream.ChunkSignatureHeader, eventstream.BytesValue(signature))
	var output bytes.Buffer
	if err := signer.encoder.Encode(&output, outer); err != nil {
		return nil, fmt.Errorf("encode signed AWS Transcribe WebSocket event: %w", err)
	}
	return output.Bytes(), nil
}

func acceptAWSTranscribeWebSocketMessage(messageType cloudWebSocketMessageType, data []byte) ([]byte, string, error) {
	if messageType != cloudWebSocketMessageBinary {
		return nil, "", fmt.Errorf("AWS Transcribe WebSocket returned a non-binary response")
	}
	outer, err := decodeSingleAWSEventStreamMessage(data)
	if err != nil || outer.Headers.Get(eventstream.DateHeader) == nil || outer.Headers.Get(eventstream.ChunkSignatureHeader) == nil {
		return nil, "", fmt.Errorf("AWS Transcribe WebSocket returned an invalid signed event-stream frame")
	}
	inner, err := decodeSingleAWSEventStreamMessage(outer.Payload)
	if err != nil {
		return nil, "", fmt.Errorf("AWS Transcribe WebSocket returned an invalid event payload")
	}
	eventType, ok := awsEventStreamStringHeader(inner, ":event-type")
	if !ok || eventType == "" {
		return nil, "", fmt.Errorf("AWS Transcribe WebSocket response omitted event type")
	}
	messageKind, ok := awsEventStreamStringHeader(inner, ":message-type")
	if !ok || (messageKind != "event" && messageKind != "exception") {
		return nil, "", fmt.Errorf("AWS Transcribe WebSocket response has an invalid message type")
	}
	contentType, ok := awsEventStreamStringHeader(inner, ":content-type")
	if !ok || contentType != "application/json" || !json.Valid(inner.Payload) {
		return nil, "", fmt.Errorf("AWS Transcribe WebSocket response payload is not valid JSON")
	}
	if messageKind == "exception" {
		return nil, eventType, fmt.Errorf("AWS Transcribe WebSocket returned %s", eventType)
	}
	return inner.Payload, eventType, nil
}

func decodeSingleAWSEventStreamMessage(data []byte) (eventstream.Message, error) {
	reader := bytes.NewReader(data)
	message, err := eventstream.NewDecoder().Decode(reader, nil)
	if err != nil {
		return eventstream.Message{}, err
	}
	if reader.Len() != 0 {
		return eventstream.Message{}, fmt.Errorf("event-stream frame has trailing data")
	}
	return message, nil
}

func awsEventStreamStringHeader(message eventstream.Message, name string) (string, bool) {
	value, ok := message.Headers.Get(name).(eventstream.StringValue)
	return string(value), ok
}

func invokeAWSTranscribeWebSocket(ctx context.Context, adapter *AWSRESTAdapter, credentials AWSCredentials, invocation Invocation) (InvocationResult, error) {
	signedURL, seed, err := signAWSTranscribeWebSocketURL(ctx, invocation.URL, credentials, invocation.Region, invocation.Parameters, adapter.config.Now().UTC())
	if err != nil {
		return InvocationResult{}, err
	}
	dialContext, cancelDial := context.WithTimeout(ctx, adapter.config.Timeout)
	connection, err := adapter.config.WebSocketDial(dialContext, signedURL)
	cancelDial()
	if err != nil {
		return InvocationResult{}, err
	}
	defer connection.Close()

	sink, err := newWebSocketOutputSink(invocation, adapter.config.MaxBodyBytes, "AWS Transcribe WebSocket")
	if err != nil {
		return InvocationResult{}, err
	}
	defer sink.abort()

	awsCredentials := aws.Credentials{
		AccessKeyID: credentials.AccessKeyID, SecretAccessKey: credentials.SecretAccessKey, SessionToken: credentials.SessionToken,
	}
	frameSigner := newAWSTranscribeWebSocketFrameSigner(awsCredentials, invocation.Region, seed, adapter.config.Now)
	streamContext, cancelStream := awsTranscribeStreamContext(ctx, invocation)
	defer cancelStream()
	readResult := make(chan error, 1)
	go readAWSTranscribeWebSocket(streamContext, connection, sink, readResult)

	if invocation.Body != nil {
		configuration, err := json.Marshal(invocation.Body)
		if err != nil {
			cancelStream()
			<-readResult
			return InvocationResult{}, fmt.Errorf("encode AWS Transcribe WebSocket configuration: %w", err)
		}
		frame, err := frameSigner.Encode("ConfigurationEvent", "application/json", configuration)
		if err != nil || connection.Write(streamContext, cloudWebSocketMessageBinary, frame) != nil {
			cancelStream()
			<-readResult
			return InvocationResult{}, fmt.Errorf("write AWS Transcribe WebSocket configuration event")
		}
	}
	if err := streamAWSTranscribeAudio(streamContext, connection, invocation, frameSigner, adapter.config.StreamPause); err != nil {
		cancelStream()
		readerErr := <-readResult
		if readerErr != nil {
			return InvocationResult{}, readerErr
		}
		return InvocationResult{}, err
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

func readAWSTranscribeWebSocket(ctx context.Context, connection cloudWebSocketConnection, sink *cloudWebSocketOutputSink, result chan<- error) {
	for {
		messageType, data, err := connection.Read(ctx)
		if err != nil {
			if err == io.EOF || websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				result <- nil
				return
			}
			result <- fmt.Errorf("read AWS Transcribe WebSocket response")
			return
		}
		payload, _, err := acceptAWSTranscribeWebSocketMessage(messageType, data)
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

func streamAWSTranscribeAudio(ctx context.Context, connection cloudWebSocketConnection, invocation Invocation, signer *awsTranscribeWebSocketFrameSigner, pause func(context.Context, time.Duration) error) error {
	audio, err := os.Open(invocation.BodyFile)
	if err != nil {
		return fmt.Errorf("open AWS Transcribe audio body_file: %w", err)
	}
	defer audio.Close()
	chunkBytes := invocation.StreamChunkBytes
	if chunkBytes == 0 {
		chunkBytes = defaultAWSTranscribeChunkBytes(invocation.Parameters)
	}
	interval := time.Duration(invocation.StreamIntervalMS) * time.Millisecond
	if interval == 0 {
		interval = defaultAWSTranscribeChunkInterval
	}
	buffer := make([]byte, chunkBytes)
	for {
		count, readErr := audio.Read(buffer)
		if count > 0 {
			frame, err := signer.Encode("AudioEvent", "application/octet-stream", buffer[:count])
			if err != nil {
				return err
			}
			if err := connection.Write(ctx, cloudWebSocketMessageBinary, frame); err != nil {
				return fmt.Errorf("write AWS Transcribe audio event")
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read AWS Transcribe audio body_file: %w", readErr)
		}
		if count > 0 {
			if err := pause(ctx, interval); err != nil {
				return fmt.Errorf("pace AWS Transcribe audio stream: %w", err)
			}
		}
	}
	terminal, err := signer.Encode("AudioEvent", "application/octet-stream", nil)
	if err != nil {
		return err
	}
	if err := connection.Write(ctx, cloudWebSocketMessageBinary, terminal); err != nil {
		return fmt.Errorf("finish AWS Transcribe audio stream")
	}
	return nil
}

func defaultAWSTranscribeChunkBytes(parameters map[string]any) int {
	if strings.EqualFold(awsTranscribeParameter(parameters, "media-encoding"), "pcm") {
		if size := maxAWSTranscribePCMChunkBytes(parameters) / 10; size > 0 && size <= maxAWSTranscribeAudioChunkBytes {
			return size
		}
	}
	return 4096
}

func maxAWSTranscribePCMChunkBytes(parameters map[string]any) int {
	sampleRate, _ := strconv.Atoi(awsTranscribeParameter(parameters, "sample-rate"))
	channels := 1
	if awsTranscribeParameter(parameters, "number-of-channels") == "2" {
		channels = 2
	}
	return sampleRate * channels * 2
}

func awsTranscribeParameter(parameters map[string]any, name string) string {
	values, err := stringValues(parameters[name])
	if err != nil || len(values) != 1 {
		return ""
	}
	return values[0]
}

func awsTranscribeStreamContext(ctx context.Context, invocation Invocation) (context.Context, context.CancelFunc) {
	chunkBytes := invocation.StreamChunkBytes
	if chunkBytes == 0 {
		chunkBytes = defaultAWSTranscribeChunkBytes(invocation.Parameters)
	}
	interval := invocation.StreamIntervalMS
	if interval == 0 {
		interval = int(defaultAWSTranscribeChunkInterval / time.Millisecond)
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

func defaultAWSTranscribeWebSocketDial(ctx context.Context, signedURL string) (cloudWebSocketConnection, error) {
	client := &http.Client{
		Transport:     http.DefaultTransport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	connection, response, err := websocket.Dial(ctx, signedURL, &websocket.DialOptions{
		HTTPClient: client, CompressionMode: websocket.CompressionDisabled,
		HTTPHeader: http.Header{"Origin": []string{"https://cloud-skills-mcp.local"}},
	})
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("AWS Transcribe WebSocket handshake failed with HTTP %d", response.StatusCode)
		}
		return nil, fmt.Errorf("AWS Transcribe WebSocket handshake failed")
	}
	connection.SetReadLimit(maxAWSEventStreamFrameBytes)
	return coderTencentWebSocketConnection{connection: connection}, nil
}

package cloud

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
)

var tencentASRWebSocketPath = regexp.MustCompile(`^/asr/v2/[0-9]{5,20}$`)
var tencentASRParameterName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
var tencentVoiceIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func validateTencentASRWebSocketInvocation(invocation Invocation) error {
	if !strings.EqualFold(invocation.Method, http.MethodGet) {
		return fmt.Errorf("Tencent Cloud ASR WebSocket requires method GET for the HTTP upgrade")
	}
	target, err := url.Parse(invocation.URL)
	if err != nil || !strings.EqualFold(target.Scheme, "wss") || !strings.EqualFold(target.Hostname(), "asr.cloud.tencent.com") || target.Port() != "" || !tencentASRWebSocketPath.MatchString(target.EscapedPath()) || target.RawQuery != "" || target.User != nil || target.Fragment != "" {
		return fmt.Errorf("Tencent Cloud ASR WebSocket requires wss://asr.cloud.tencent.com/asr/v2/<appid> without caller query parameters")
	}
	if invocation.BodyFile == "" || invocation.Body != nil {
		return fmt.Errorf("Tencent Cloud ASR WebSocket requires body_file and does not accept inline body")
	}
	if len(invocation.Headers) != 0 {
		return fmt.Errorf("Tencent Cloud ASR WebSocket does not accept caller-supplied handshake headers")
	}
	return nil
}

type tencentWebSocketMessageType uint8

const (
	tencentWebSocketMessageText   tencentWebSocketMessageType = 1
	tencentWebSocketMessageBinary tencentWebSocketMessageType = 2
)

type tencentWebSocketConnection interface {
	Read(context.Context) (tencentWebSocketMessageType, []byte, error)
	Write(context.Context, tencentWebSocketMessageType, []byte) error
	Close() error
}

type coderTencentWebSocketConnection struct {
	connection *websocket.Conn
}

func (connection coderTencentWebSocketConnection) Read(ctx context.Context) (tencentWebSocketMessageType, []byte, error) {
	messageType, data, err := connection.connection.Read(ctx)
	return tencentWebSocketMessageType(messageType), data, err
}

func (connection coderTencentWebSocketConnection) Write(ctx context.Context, messageType tencentWebSocketMessageType, data []byte) error {
	return connection.connection.Write(ctx, websocket.MessageType(messageType), data)
}

func (connection coderTencentWebSocketConnection) Close() error {
	return connection.connection.Close(websocket.StatusNormalClosure, "")
}

func defaultTencentWebSocketDial(ctx context.Context, signedURL string) (tencentWebSocketConnection, error) {
	client := &http.Client{
		Transport: http.DefaultTransport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	connection, response, err := websocket.Dial(ctx, signedURL, &websocket.DialOptions{
		HTTPClient:      client,
		CompressionMode: websocket.CompressionDisabled,
	})
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("Tencent Cloud WebSocket handshake failed with HTTP %d", response.StatusCode)
		}
		return nil, fmt.Errorf("Tencent Cloud WebSocket handshake failed")
	}
	connection.SetReadLimit(maxRequestPayloadBytes)
	return coderTencentWebSocketConnection{connection: connection}, nil
}

func pauseTencentStream(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func signTencentASRWebSocketURL(rawURL string, credentials TencentCredentials, input map[string]any, now time.Time, nonce, voiceID string) (string, error) {
	target, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse Tencent Cloud ASR WebSocket URL: %w", err)
	}
	if !strings.EqualFold(target.Scheme, "wss") || !strings.EqualFold(target.Hostname(), "asr.cloud.tencent.com") || target.Port() != "" || !tencentASRWebSocketPath.MatchString(target.EscapedPath()) || target.RawQuery != "" || target.User != nil || target.Fragment != "" {
		return "", fmt.Errorf("Tencent Cloud ASR WebSocket requires wss://asr.cloud.tencent.com/asr/v2/<appid> without caller query parameters")
	}
	if credentials.SecretID == "" || credentials.SecretKey == "" {
		return "", fmt.Errorf("Tencent Cloud ASR WebSocket requires complete SecretId/SecretKey credentials")
	}
	if credentials.Token != "" {
		return "", fmt.Errorf("Tencent Cloud ASR WebSocket does not document CAM temporary-token authentication")
	}
	if !validTencentASRNonce(nonce) {
		return "", fmt.Errorf("Tencent Cloud ASR WebSocket requires a positive nonce of at most 10 digits")
	}
	if !tencentVoiceIDPattern.MatchString(voiceID) {
		return "", fmt.Errorf("Tencent Cloud ASR WebSocket requires a generated voice_id of at most 128 characters")
	}

	parameters := make(map[string]string, len(input)+6)
	for name, value := range input {
		if isTencentASRControlledParameter(name) {
			return "", fmt.Errorf("caller-supplied Tencent Cloud ASR WebSocket signing parameter %q is forbidden", name)
		}
		if !tencentASRParameterName.MatchString(name) {
			return "", fmt.Errorf("Tencent Cloud ASR WebSocket query parameter name %q is invalid", name)
		}
		values, err := stringValues(value)
		if err != nil || len(values) != 1 {
			return "", fmt.Errorf("Tencent Cloud ASR WebSocket query parameter %q must be scalar", name)
		}
		parameters[name] = values[0]
	}
	if strings.TrimSpace(parameters["engine_model_type"]) == "" {
		return "", fmt.Errorf("Tencent Cloud ASR WebSocket requires engine_model_type")
	}
	timestamp := now.UTC().Unix()
	parameters["secretid"] = credentials.SecretID
	parameters["timestamp"] = strconv.FormatInt(timestamp, 10)
	parameters["expired"] = strconv.FormatInt(timestamp+24*60*60, 10)
	parameters["nonce"] = nonce
	parameters["voice_id"] = voiceID
	canonical := canonicalTencentV1Parameters(parameters)
	target.Scheme = "wss"
	target.Host = "asr.cloud.tencent.com"
	source := target.Host + target.EscapedPath() + "?" + canonical
	parameters["signature"] = base64.StdEncoding.EncodeToString(hmacBytes(sha1.New, []byte(credentials.SecretKey), []byte(source)))
	target.RawQuery = encodeTencentV1Parameters(parameters)
	return target.String(), nil
}

func isTencentASRControlledParameter(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "expired", "nonce", "secretid", "signature", "timestamp", "voice_id":
		return true
	default:
		return false
	}
}

func validTencentASRNonce(nonce string) bool {
	if len(nonce) == 0 || len(nonce) > 10 {
		return false
	}
	parsed, err := strconv.ParseUint(nonce, 10, 32)
	return err == nil && parsed > 0
}

func secureTencentASRNonce() string {
	var value [4]byte
	if _, err := rand.Read(value[:]); err == nil {
		parsed, _ := strconv.ParseUint(hex.EncodeToString(value[:]), 16, 32)
		return strconv.FormatUint(parsed%4_000_000_000+1, 10)
	}
	return strconv.FormatUint(uint64(time.Now().UTC().UnixNano())%4_000_000_000+1, 10)
}

func secureTencentVoiceID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

type tencentASRWebSocketMessage struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	VoiceID string `json:"voice_id"`
	Final   int    `json:"final"`
}

func invokeTencentASRWebSocket(ctx context.Context, adapter *TencentRESTAdapter, credentials TencentCredentials, invocation Invocation) (InvocationResult, error) {
	voiceID := adapter.config.VoiceID()
	signedURL, err := signTencentASRWebSocketURL(invocation.URL, credentials, invocation.Parameters, adapter.config.Now().UTC(), adapter.config.Nonce(), voiceID)
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

	sink, err := newTencentWebSocketOutputSink(invocation, adapter.config.MaxBodyBytes)
	if err != nil {
		return InvocationResult{}, err
	}
	defer sink.abort()

	handshakeContext, cancelHandshake := context.WithTimeout(ctx, adapter.config.Timeout)
	messageType, handshake, err := connection.Read(handshakeContext)
	cancelHandshake()
	if err != nil {
		return InvocationResult{}, fmt.Errorf("read Tencent Cloud ASR WebSocket handshake")
	}
	requestID, final, err := acceptTencentASRWebSocketMessage(messageType, handshake, sink)
	if err != nil {
		return InvocationResult{}, err
	}
	if requestID == "" {
		return InvocationResult{}, fmt.Errorf("Tencent Cloud ASR WebSocket handshake omitted voice_id")
	}
	if final {
		output, err := sink.finish(requestID)
		return InvocationResult{Output: output, RequestID: requestID}, err
	}

	streamContext, cancelStream := tencentASRStreamContext(ctx, invocation)
	defer cancelStream()
	readResult := make(chan tencentASRReadResult, 1)
	go readTencentASRWebSocket(streamContext, cancelStream, connection, sink, requestID, readResult)
	if err := streamTencentASRAudio(streamContext, connection, invocation, adapter.config.StreamPause); err != nil {
		cancelStream()
		reader := <-readResult
		if reader.err != nil {
			return InvocationResult{}, reader.err
		}
		return InvocationResult{}, err
	}
	result := <-readResult
	if result.err != nil {
		return InvocationResult{}, result.err
	}
	output, err := sink.finish(result.requestID)
	if err != nil {
		return InvocationResult{}, err
	}
	return InvocationResult{Output: output, RequestID: result.requestID}, nil
}

func tencentASRStreamContext(ctx context.Context, invocation Invocation) (context.Context, context.CancelFunc) {
	chunkBytes := invocation.StreamChunkBytes
	if chunkBytes == 0 {
		chunkBytes = defaultTencentASRChunkBytes(invocation.Parameters)
	}
	interval := invocation.StreamIntervalMS
	if interval == 0 {
		interval = 200
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

type tencentASRReadResult struct {
	requestID string
	err       error
}

func readTencentASRWebSocket(ctx context.Context, cancel context.CancelFunc, connection tencentWebSocketConnection, sink *tencentWebSocketOutputSink, requestID string, result chan<- tencentASRReadResult) {
	for {
		messageType, data, err := connection.Read(ctx)
		if err != nil {
			cancel()
			result <- tencentASRReadResult{requestID: requestID, err: fmt.Errorf("read Tencent Cloud ASR WebSocket response")}
			return
		}
		currentID, final, err := acceptTencentASRWebSocketMessage(messageType, data, sink)
		if currentID != "" {
			requestID = currentID
		}
		if err != nil || final {
			if err != nil {
				cancel()
			}
			result <- tencentASRReadResult{requestID: requestID, err: err}
			return
		}
	}
}

func acceptTencentASRWebSocketMessage(messageType tencentWebSocketMessageType, data []byte, sink *tencentWebSocketOutputSink) (string, bool, error) {
	if messageType != tencentWebSocketMessageText {
		return "", false, fmt.Errorf("Tencent Cloud ASR WebSocket returned a non-text response")
	}
	var message tencentASRWebSocketMessage
	if err := json.Unmarshal(data, &message); err != nil {
		return "", false, fmt.Errorf("Tencent Cloud ASR WebSocket returned invalid JSON")
	}
	if message.Code != 0 {
		return message.VoiceID, false, fmt.Errorf("Tencent Cloud ASR WebSocket returned code %d", message.Code)
	}
	if err := sink.writeMessage(data); err != nil {
		return message.VoiceID, false, err
	}
	return message.VoiceID, message.Final == 1, nil
}

func streamTencentASRAudio(ctx context.Context, connection tencentWebSocketConnection, invocation Invocation, pause func(context.Context, time.Duration) error) error {
	audio, err := os.Open(invocation.BodyFile)
	if err != nil {
		return fmt.Errorf("open Tencent Cloud ASR audio body_file: %w", err)
	}
	defer audio.Close()
	chunkBytes := invocation.StreamChunkBytes
	if chunkBytes == 0 {
		chunkBytes = defaultTencentASRChunkBytes(invocation.Parameters)
	}
	interval := invocation.StreamIntervalMS
	if interval == 0 {
		interval = 200
	}
	buffer := make([]byte, chunkBytes)
	for {
		count, readErr := audio.Read(buffer)
		if count > 0 {
			if err := connection.Write(ctx, tencentWebSocketMessageBinary, buffer[:count]); err != nil {
				return fmt.Errorf("write Tencent Cloud ASR audio frame")
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read Tencent Cloud ASR audio body_file: %w", readErr)
		}
		if count > 0 {
			if err := pause(ctx, time.Duration(interval)*time.Millisecond); err != nil {
				return fmt.Errorf("pace Tencent Cloud ASR audio stream: %w", err)
			}
		}
	}
	if err := connection.Write(ctx, tencentWebSocketMessageText, []byte(`{"type":"end"}`)); err != nil {
		return fmt.Errorf("finish Tencent Cloud ASR audio stream")
	}
	return nil
}

func defaultTencentASRChunkBytes(parameters map[string]any) int {
	voiceFormat := ""
	if values, err := stringValues(parameters["voice_format"]); err == nil && len(values) == 1 {
		voiceFormat = values[0]
	}
	if voiceFormat != "" && voiceFormat != "1" {
		return 4096
	}
	if values, err := stringValues(parameters["input_sample_rate"]); err == nil && len(values) == 1 && values[0] == "8000" {
		return 3200
	}
	if values, err := stringValues(parameters["engine_model_type"]); err == nil && len(values) == 1 && strings.HasPrefix(strings.ToLower(values[0]), "8k_") {
		return 3200
	}
	return 6400
}

type tencentWebSocketOutputSink struct {
	buffer        bytes.Buffer
	temporary     *os.File
	temporaryPath string
	target        string
	maxBytes      int64
	written       int64
	finished      bool
}

func newTencentWebSocketOutputSink(invocation Invocation, maxBodyBytes int64) (*tencentWebSocketOutputSink, error) {
	sink := &tencentWebSocketOutputSink{maxBytes: maxBodyBytes}
	if invocation.ResponseFile == "" {
		return sink, nil
	}
	if invocation.MaxResponseFileBytes > 0 {
		sink.maxBytes = invocation.MaxResponseFileBytes
	} else {
		sink.maxBytes = defaultResponseFileLimit
	}
	target, err := filepath.Abs(invocation.ResponseFile)
	if err != nil {
		return nil, fmt.Errorf("resolve response_file: %w", err)
	}
	if _, err := os.Lstat(target); err == nil {
		return nil, fmt.Errorf("response_file already exists")
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect response_file: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".cloud-skills-websocket-*")
	if err != nil {
		return nil, fmt.Errorf("create response_file temporary file: %w", err)
	}
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		os.Remove(temporary.Name())
		return nil, fmt.Errorf("secure response_file temporary file: %w", err)
	}
	sink.temporary = temporary
	sink.temporaryPath = temporary.Name()
	sink.target = target
	return sink, nil
}

func (sink *tencentWebSocketOutputSink) writeMessage(data []byte) error {
	messageBytes := int64(len(data) + 1)
	if sink.written+messageBytes > sink.maxBytes {
		return fmt.Errorf("Tencent Cloud WebSocket response exceeds %d bytes", sink.maxBytes)
	}
	var writer io.Writer = &sink.buffer
	if sink.temporary != nil {
		writer = sink.temporary
	}
	if _, err := writer.Write(data); err != nil {
		return fmt.Errorf("write Tencent Cloud WebSocket response: %w", err)
	}
	if _, err := writer.Write([]byte{'\n'}); err != nil {
		return fmt.Errorf("write Tencent Cloud WebSocket response: %w", err)
	}
	sink.written += messageBytes
	return nil
}

func (sink *tencentWebSocketOutputSink) finish(requestID string) ([]byte, error) {
	if sink.finished {
		return nil, fmt.Errorf("Tencent Cloud WebSocket response sink already finalized")
	}
	sink.finished = true
	if sink.temporary == nil {
		return sink.buffer.Bytes(), nil
	}
	if err := sink.temporary.Sync(); err != nil {
		return nil, fmt.Errorf("sync response_file: %w", err)
	}
	if err := sink.temporary.Close(); err != nil {
		return nil, fmt.Errorf("close response_file: %w", err)
	}
	sink.temporary = nil
	if err := os.Link(sink.temporaryPath, sink.target); err != nil {
		if _, statErr := os.Lstat(sink.target); statErr == nil {
			return nil, fmt.Errorf("response_file already exists")
		}
		return nil, fmt.Errorf("publish response_file: %w", err)
	}
	metadata, err := json.Marshal(map[string]any{
		"response_file": sink.target,
		"bytes":         sink.written,
		"content_type":  "application/x-ndjson",
		"request_id":    requestID,
	})
	if err != nil {
		os.Remove(sink.target)
		return nil, fmt.Errorf("encode response_file metadata: %w", err)
	}
	return metadata, nil
}

func (sink *tencentWebSocketOutputSink) abort() {
	if sink.finished {
		os.Remove(sink.temporaryPath)
		return
	}
	if sink.temporary != nil {
		sink.temporary.Close()
	}
	if sink.temporaryPath != "" {
		os.Remove(sink.temporaryPath)
	}
}

package cloud

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/coder/websocket"
)

var tencentASRWebSocketPath = regexp.MustCompile(`^/asr/v2/[0-9]{5,20}$`)
var tencentVirtualNumberWebSocketPath = regexp.MustCompile(`^/asr/virtual_number/v1/[0-9]{5,20}$`)
var tencentSOEWebSocketPath = regexp.MustCompile(`^/soe/api/[0-9]{5,20}$`)
var tencentSpeechTranslateWebSocketPath = regexp.MustCompile(`^/asr/speech_translate/[0-9]{5,20}$`)
var tencentMPSWebSocketPath = regexp.MustCompile(`^/wss/v1/[0-9]{5,20}$`)
var tencentMPSTTSWebSocketPath = regexp.MustCompile(`^/tts/v1/[0-9]{5,20}$`)
var tencentVoiceConversionWebSocketPath = regexp.MustCompile(`^/vc_stream/[0-9]{5,20}$`)
var tencentASRParameterName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
var tencentVoiceIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var tencentMPSNoncePattern = regexp.MustCompile(`^[1-9][0-9]{9}$`)
var tencentTTSAppIDPattern = regexp.MustCompile(`^[1-9][0-9]{4,18}$`)

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

func validateTencentVirtualNumberWebSocketInvocation(invocation Invocation) error {
	if !strings.EqualFold(invocation.Method, http.MethodGet) {
		return fmt.Errorf("Tencent Cloud virtual-number WebSocket requires method GET for the HTTP upgrade")
	}
	target, err := url.Parse(invocation.URL)
	if err != nil || !strings.EqualFold(target.Scheme, "wss") || !strings.EqualFold(target.Hostname(), "asr.cloud.tencent.com") || target.Port() != "" || !tencentVirtualNumberWebSocketPath.MatchString(target.EscapedPath()) || target.RawQuery != "" || target.User != nil || target.Fragment != "" {
		return fmt.Errorf("Tencent Cloud virtual-number WebSocket requires wss://asr.cloud.tencent.com/asr/virtual_number/v1/<appid> without caller query parameters")
	}
	if invocation.BodyFile == "" || invocation.Body != nil {
		return fmt.Errorf("Tencent Cloud virtual-number WebSocket requires body_file and does not accept inline body")
	}
	if len(invocation.Headers) != 0 {
		return fmt.Errorf("Tencent Cloud virtual-number WebSocket does not accept caller-supplied handshake headers")
	}
	if invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return fmt.Errorf("Tencent Cloud virtual-number WebSocket does not accept MPS frame controls")
	}
	return validateTencentVirtualNumberParameters(invocation.Parameters)
}

func validateTencentSOEWebSocketInvocation(invocation Invocation) error {
	if !strings.EqualFold(invocation.Method, http.MethodGet) {
		return fmt.Errorf("Tencent Cloud SOE WebSocket requires method GET for the HTTP upgrade")
	}
	target, err := url.Parse(invocation.URL)
	if err != nil || !strings.EqualFold(target.Scheme, "wss") || !strings.EqualFold(target.Hostname(), "soe.cloud.tencent.com") || target.Port() != "" || !tencentSOEWebSocketPath.MatchString(target.EscapedPath()) || target.RawQuery != "" || target.User != nil || target.Fragment != "" {
		return fmt.Errorf("Tencent Cloud SOE WebSocket requires wss://soe.cloud.tencent.com/soe/api/<appid> without caller query parameters")
	}
	if invocation.BodyFile == "" || invocation.Body != nil {
		return fmt.Errorf("Tencent Cloud SOE WebSocket requires body_file and does not accept inline body")
	}
	if len(invocation.Headers) != 0 {
		return fmt.Errorf("Tencent Cloud SOE WebSocket does not accept caller-supplied handshake headers")
	}
	if invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return fmt.Errorf("Tencent Cloud SOE WebSocket does not accept MPS frame controls")
	}
	if err := validateTencentSOEParameters(invocation.Parameters); err != nil {
		return err
	}
	if tencentScalarParameterEquals(invocation.Parameters, "rec_mode", "1") && (invocation.StreamChunkBytes != 0 || invocation.StreamIntervalMS != 0) {
		return fmt.Errorf("Tencent Cloud SOE recording mode requires one server-sized audio frame and does not accept stream controls")
	}
	return nil
}

func validateTencentSpeechTranslateWebSocketInvocation(invocation Invocation) error {
	if !strings.EqualFold(invocation.Method, http.MethodGet) {
		return fmt.Errorf("Tencent Cloud speech translation WebSocket requires method GET for the HTTP upgrade")
	}
	target, err := url.Parse(invocation.URL)
	if err != nil || !strings.EqualFold(target.Scheme, "wss") || !strings.EqualFold(target.Hostname(), "asr.cloud.tencent.com") || target.Port() != "" || !tencentSpeechTranslateWebSocketPath.MatchString(target.EscapedPath()) || target.RawQuery != "" || target.User != nil || target.Fragment != "" {
		return fmt.Errorf("Tencent Cloud speech translation WebSocket requires wss://asr.cloud.tencent.com/asr/speech_translate/<appid> without caller query parameters")
	}
	if invocation.BodyFile == "" || invocation.Body != nil {
		return fmt.Errorf("Tencent Cloud speech translation WebSocket requires body_file and does not accept inline body")
	}
	if len(invocation.Headers) != 0 {
		return fmt.Errorf("Tencent Cloud speech translation WebSocket does not accept caller-supplied handshake headers")
	}
	if invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return fmt.Errorf("Tencent Cloud speech translation WebSocket does not accept MPS frame controls")
	}
	if err := validateTencentSpeechTranslateParameters(invocation.Parameters); err != nil {
		return err
	}
	if tencentScalarParameterEquals(invocation.Parameters, "enable_tts", "1") && invocation.ResponseFile == "" {
		return fmt.Errorf("Tencent Cloud speech translation WebSocket requires response_file when enable_tts=1")
	}
	return nil
}

func validateTencentVoiceConversionWebSocketInvocation(invocation Invocation) error {
	if !strings.EqualFold(invocation.Method, http.MethodGet) {
		return fmt.Errorf("Tencent Cloud voice conversion WebSocket requires method GET for the HTTP upgrade")
	}
	target, err := url.Parse(invocation.URL)
	if err != nil || !strings.EqualFold(target.Scheme, "wss") || !strings.EqualFold(target.Hostname(), "tts.cloud.tencent.com") || target.Port() != "" || !tencentVoiceConversionWebSocketPath.MatchString(target.EscapedPath()) || target.RawQuery != "" || target.User != nil || target.Fragment != "" {
		return fmt.Errorf("Tencent Cloud voice conversion WebSocket requires wss://tts.cloud.tencent.com/vc_stream/<appid> without caller query parameters")
	}
	if invocation.BodyFile == "" || invocation.Body != nil {
		return fmt.Errorf("Tencent Cloud voice conversion WebSocket requires body_file and does not accept inline body")
	}
	if invocation.ResponseFile == "" {
		return fmt.Errorf("Tencent Cloud voice conversion WebSocket requires response_file for converted PCM audio")
	}
	if len(invocation.Headers) != 0 {
		return fmt.Errorf("Tencent Cloud voice conversion WebSocket does not accept caller-supplied handshake headers")
	}
	if invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return fmt.Errorf("Tencent Cloud voice conversion WebSocket does not accept MPS frame controls")
	}
	_, err = validateTencentVoiceConversionParameters(invocation.Parameters)
	return err
}

func validateTencentMPSWebSocketInvocation(invocation Invocation) error {
	if !strings.EqualFold(invocation.Method, http.MethodGet) {
		return fmt.Errorf("Tencent Cloud MPS WebSocket requires method GET for the HTTP upgrade")
	}
	target, err := url.Parse(invocation.URL)
	if err != nil || !strings.EqualFold(target.Scheme, "wss") || !strings.EqualFold(target.Hostname(), "mps.cloud.tencent.com") || target.Port() != "" || !tencentMPSWebSocketPath.MatchString(target.EscapedPath()) || target.RawQuery != "" || target.User != nil || target.Fragment != "" {
		return fmt.Errorf("Tencent Cloud MPS WebSocket requires wss://mps.cloud.tencent.com/wss/v1/<appid> without caller query parameters")
	}
	if invocation.BodyFile == "" || invocation.Body != nil {
		return fmt.Errorf("Tencent Cloud MPS WebSocket requires body_file and does not accept inline body")
	}
	if len(invocation.Headers) != 0 {
		return fmt.Errorf("Tencent Cloud MPS WebSocket does not accept caller-supplied handshake headers")
	}
	if err := validateTencentMPSUserID(invocation.StreamUserID); err != nil {
		return err
	}
	if invocation.StreamFormat != 1 && invocation.StreamFormat != 2 {
		return fmt.Errorf("Tencent Cloud MPS WebSocket stream_format must be 1 (PCM 16 kHz) or 2 (PCM 8 kHz)")
	}
	return validateTencentMPSParameters(invocation.Parameters)
}

func validateTencentMPSTTSWebSocketInvocation(invocation Invocation) error {
	if !strings.EqualFold(invocation.Method, http.MethodGet) {
		return fmt.Errorf("Tencent Cloud MPS TTS WebSocket requires method GET for the HTTP upgrade")
	}
	target, err := url.Parse(invocation.URL)
	if err != nil || !strings.EqualFold(target.Scheme, "wss") || !strings.EqualFold(target.Hostname(), "mps.cloud.tencent.com") || target.Port() != "" || !tencentMPSTTSWebSocketPath.MatchString(target.EscapedPath()) || target.RawQuery != "" || target.User != nil || target.Fragment != "" {
		return fmt.Errorf("Tencent Cloud MPS TTS WebSocket requires wss://mps.cloud.tencent.com/tts/v1/<appid> without caller query parameters")
	}
	if invocation.Body == nil || invocation.BodyFile != "" {
		return fmt.Errorf("Tencent Cloud MPS TTS WebSocket requires inline text body and does not accept body_file")
	}
	if invocation.ResponseFile == "" {
		return fmt.Errorf("Tencent Cloud MPS TTS WebSocket requires response_file for binary audio")
	}
	if len(invocation.Headers) != 0 {
		return fmt.Errorf("Tencent Cloud MPS TTS WebSocket does not accept caller-supplied handshake headers")
	}
	if invocation.StreamChunkBytes != 0 || invocation.StreamIntervalMS != 0 || invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return fmt.Errorf("Tencent Cloud MPS TTS WebSocket does not accept audio-upload stream controls")
	}
	if _, err := tencentTTSTextSegments(invocation.Body); err != nil {
		return err
	}
	return validateTencentMPSTTSParameters(invocation.Parameters)
}

func validateTencentTTSWebSocketInvocation(invocation Invocation) error {
	if !strings.EqualFold(invocation.Method, http.MethodGet) {
		return fmt.Errorf("Tencent Cloud TTS WebSocket requires method GET for the HTTP upgrade")
	}
	target, err := url.Parse(invocation.URL)
	if err != nil || !strings.EqualFold(target.Scheme, "wss") || !strings.EqualFold(target.Hostname(), "tts.cloud.tencent.com") || target.Port() != "" || target.EscapedPath() != "/stream_ws" || target.RawQuery != "" || target.User != nil || target.Fragment != "" {
		return fmt.Errorf("Tencent Cloud TTS WebSocket requires wss://tts.cloud.tencent.com/stream_ws without caller query parameters")
	}
	if _, err := tencentTTSRequestText(invocation.Body); err != nil || invocation.BodyFile != "" {
		if err != nil {
			return err
		}
		return fmt.Errorf("Tencent Cloud TTS WebSocket requires inline text body and does not accept body_file")
	}
	if invocation.ResponseFile == "" {
		return fmt.Errorf("Tencent Cloud TTS WebSocket requires response_file for binary audio")
	}
	if len(invocation.Headers) != 0 {
		return fmt.Errorf("Tencent Cloud TTS WebSocket does not accept caller-supplied handshake headers")
	}
	if invocation.StreamChunkBytes != 0 || invocation.StreamIntervalMS != 0 || invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return fmt.Errorf("Tencent Cloud TTS WebSocket does not accept audio-upload stream controls")
	}
	_, err = validateTencentTTSParameters(invocation.Parameters)
	return err
}

func validateTencentStreamingTTSWebSocketInvocation(invocation Invocation) error {
	if !strings.EqualFold(invocation.Method, http.MethodGet) {
		return fmt.Errorf("Tencent Cloud streaming TTS WebSocket requires method GET for the HTTP upgrade")
	}
	target, err := url.Parse(invocation.URL)
	if err != nil || !strings.EqualFold(target.Scheme, "wss") || !strings.EqualFold(target.Hostname(), "tts.cloud.tencent.com") || target.Port() != "" || target.EscapedPath() != "/stream_wsv2" || target.RawQuery != "" || target.User != nil || target.Fragment != "" {
		return fmt.Errorf("Tencent Cloud streaming TTS WebSocket requires wss://tts.cloud.tencent.com/stream_wsv2 without caller query parameters")
	}
	if _, err := tencentStreamingTTSTextSegments(invocation.Body); err != nil || invocation.BodyFile != "" {
		if err != nil {
			return err
		}
		return fmt.Errorf("Tencent Cloud streaming TTS WebSocket requires inline text body and does not accept body_file")
	}
	if invocation.ResponseFile == "" {
		return fmt.Errorf("Tencent Cloud streaming TTS WebSocket requires response_file for binary audio")
	}
	if len(invocation.Headers) != 0 {
		return fmt.Errorf("Tencent Cloud streaming TTS WebSocket does not accept caller-supplied handshake headers")
	}
	if invocation.StreamChunkBytes != 0 || invocation.StreamIntervalMS != 0 || invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return fmt.Errorf("Tencent Cloud streaming TTS WebSocket does not accept audio-upload stream controls")
	}
	_, err = validateTencentTTSParameters(invocation.Parameters)
	return err
}

func validateTencentMPSUserID(userID string) error {
	if len(userID) == 0 || len(userID) > 65535 || !utf8.ValidString(userID) {
		return fmt.Errorf("Tencent Cloud MPS WebSocket stream_user_id must be valid UTF-8 between 1 and 65535 bytes")
	}
	for _, character := range userID {
		if unicode.IsControl(character) {
			return fmt.Errorf("Tencent Cloud MPS WebSocket stream_user_id must not contain control characters")
		}
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
	if credentials.Token != "" {
		parameters["token"] = credentials.Token
	}
	canonical := canonicalTencentV1Parameters(parameters)
	target.Scheme = "wss"
	target.Host = "asr.cloud.tencent.com"
	source := "asr.cloud.tencent.com" + target.EscapedPath() + "?" + canonical
	parameters["signature"] = base64.StdEncoding.EncodeToString(hmacBytes(sha1.New, []byte(credentials.SecretKey), []byte(source)))
	target.RawQuery = encodeTencentV1Parameters(parameters)
	return target.String(), nil
}

func signTencentVirtualNumberWebSocketURL(rawURL string, credentials TencentCredentials, input map[string]any, now time.Time, nonce, voiceID string) (string, error) {
	target, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse Tencent Cloud virtual-number WebSocket URL: %w", err)
	}
	if !strings.EqualFold(target.Scheme, "wss") || !strings.EqualFold(target.Hostname(), "asr.cloud.tencent.com") || target.Port() != "" || !tencentVirtualNumberWebSocketPath.MatchString(target.EscapedPath()) || target.RawQuery != "" || target.User != nil || target.Fragment != "" {
		return "", fmt.Errorf("Tencent Cloud virtual-number WebSocket requires wss://asr.cloud.tencent.com/asr/virtual_number/v1/<appid> without caller query parameters")
	}
	if credentials.SecretID == "" || credentials.SecretKey == "" {
		return "", fmt.Errorf("Tencent Cloud virtual-number WebSocket requires complete SecretId/SecretKey credentials")
	}
	if credentials.Token != "" {
		return "", fmt.Errorf("Tencent Cloud virtual-number WebSocket does not document CAM temporary-token authentication")
	}
	if !validTencentASRNonce(nonce) {
		return "", fmt.Errorf("Tencent Cloud virtual-number WebSocket requires a positive nonce of at most 10 digits")
	}
	if !tencentVoiceIDPattern.MatchString(voiceID) {
		return "", fmt.Errorf("Tencent Cloud virtual-number WebSocket requires a generated voice_id of at most 128 characters")
	}
	if err := validateTencentVirtualNumberParameters(input); err != nil {
		return "", err
	}
	parameters := make(map[string]string, len(input)+6)
	for name, value := range input {
		parameter, _ := tencentScalarStringValue(value)
		parameters[name] = parameter
	}
	timestamp := now.UTC().Unix()
	parameters["secretid"] = credentials.SecretID
	parameters["timestamp"] = strconv.FormatInt(timestamp, 10)
	parameters["expired"] = strconv.FormatInt(timestamp+24*60*60, 10)
	parameters["nonce"] = nonce
	parameters["voice_id"] = voiceID
	canonical := canonicalTencentV1Parameters(parameters)
	source := "asr.cloud.tencent.com" + target.EscapedPath() + "?" + canonical
	parameters["signature"] = base64.StdEncoding.EncodeToString(hmacBytes(sha1.New, []byte(credentials.SecretKey), []byte(source)))
	target.Scheme = "wss"
	target.Host = "asr.cloud.tencent.com"
	target.RawQuery = encodeTencentV1Parameters(parameters)
	return target.String(), nil
}

func signTencentSOEWebSocketURL(rawURL string, credentials TencentCredentials, input map[string]any, now time.Time, nonce, voiceID string) (string, error) {
	target, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse Tencent Cloud SOE WebSocket URL: %w", err)
	}
	if !strings.EqualFold(target.Scheme, "wss") || !strings.EqualFold(target.Hostname(), "soe.cloud.tencent.com") || target.Port() != "" || !tencentSOEWebSocketPath.MatchString(target.EscapedPath()) || target.RawQuery != "" || target.User != nil || target.Fragment != "" {
		return "", fmt.Errorf("Tencent Cloud SOE WebSocket requires wss://soe.cloud.tencent.com/soe/api/<appid> without caller query parameters")
	}
	if credentials.SecretID == "" || credentials.SecretKey == "" {
		return "", fmt.Errorf("Tencent Cloud SOE WebSocket requires complete SecretId/SecretKey credentials")
	}
	if credentials.Token != "" {
		return "", fmt.Errorf("Tencent Cloud SOE WebSocket does not document CAM temporary-token authentication")
	}
	if !validTencentASRNonce(nonce) {
		return "", fmt.Errorf("Tencent Cloud SOE WebSocket requires a positive nonce of at most 10 digits")
	}
	if !tencentVoiceIDPattern.MatchString(voiceID) {
		return "", fmt.Errorf("Tencent Cloud SOE WebSocket requires a generated voice_id of at most 128 characters")
	}
	if err := validateTencentSOEParameters(input); err != nil {
		return "", err
	}
	parameters := make(map[string]string, len(input)+6)
	for name, value := range input {
		parameter, _ := tencentScalarStringValue(value)
		parameters[name] = parameter
	}
	timestamp := now.UTC().Unix()
	parameters["secretid"] = credentials.SecretID
	parameters["timestamp"] = strconv.FormatInt(timestamp, 10)
	parameters["expired"] = strconv.FormatInt(timestamp+24*60*60, 10)
	parameters["nonce"] = nonce
	parameters["voice_id"] = voiceID
	canonical := canonicalTencentV1Parameters(parameters)
	source := "soe.cloud.tencent.com" + target.EscapedPath() + "?" + canonical
	parameters["signature"] = base64.StdEncoding.EncodeToString(hmacBytes(sha1.New, []byte(credentials.SecretKey), []byte(source)))
	target.Scheme = "wss"
	target.Host = "soe.cloud.tencent.com"
	target.RawQuery = encodeTencentV1Parameters(parameters)
	return target.String(), nil
}

func validateTencentSOEParameters(input map[string]any) error {
	parameters := make(map[string]string, len(input))
	for name, value := range input {
		if isTencentASRControlledParameter(name) {
			return fmt.Errorf("caller-supplied Tencent Cloud SOE WebSocket signing parameter %q is forbidden", name)
		}
		switch name {
		case "server_engine_type", "voice_format", "text_mode", "ref_text", "keyword", "eval_mode", "score_coeff", "sentence_info_enabled", "rec_mode":
		default:
			return fmt.Errorf("Tencent Cloud SOE WebSocket query parameter %q is not documented", name)
		}
		parameter, err := tencentScalarStringValue(value)
		if err != nil {
			return fmt.Errorf("Tencent Cloud SOE WebSocket query parameter %q must be scalar", name)
		}
		parameters[name] = parameter
	}
	if engine := parameters["server_engine_type"]; engine != "16k_zh" && engine != "16k_en" {
		return fmt.Errorf("Tencent Cloud SOE WebSocket server_engine_type must be 16k_zh or 16k_en")
	}
	evalMode, err := strconv.Atoi(parameters["eval_mode"])
	if err != nil || evalMode < 0 || evalMode > 8 {
		return fmt.Errorf("Tencent Cloud SOE WebSocket eval_mode must be between 0 and 8")
	}
	score, err := strconv.ParseFloat(parameters["score_coeff"], 64)
	if err != nil || math.IsNaN(score) || math.IsInf(score, 0) || score < 1 || score > 4 {
		return fmt.Errorf("Tencent Cloud SOE WebSocket score_coeff must be between 1.0 and 4.0")
	}
	if format, present := parameters["voice_format"]; present && format != "0" && format != "1" && format != "2" && format != "4" {
		return fmt.Errorf("Tencent Cloud SOE WebSocket voice_format is unsupported")
	}
	for _, name := range []string{"text_mode", "sentence_info_enabled", "rec_mode"} {
		if value, present := parameters[name]; present && value != "0" && value != "1" {
			return fmt.Errorf("Tencent Cloud SOE WebSocket %s must be 0 or 1", name)
		}
	}
	return nil
}

func validateTencentVirtualNumberParameters(input map[string]any) error {
	parameters := make(map[string]string, len(input))
	for name, value := range input {
		if isTencentASRControlledParameter(name) {
			return fmt.Errorf("caller-supplied Tencent Cloud virtual-number WebSocket signing parameter %q is forbidden", name)
		}
		switch name {
		case "voice_format", "wait_time":
		default:
			return fmt.Errorf("Tencent Cloud virtual-number WebSocket query parameter %q is not documented", name)
		}
		parameter, err := tencentScalarStringValue(value)
		if err != nil {
			return fmt.Errorf("Tencent Cloud virtual-number WebSocket query parameter %q must be scalar", name)
		}
		parameters[name] = parameter
	}
	if format, present := parameters["voice_format"]; present {
		switch format {
		case "1", "4", "6", "8", "10", "12", "14", "16":
		default:
			return fmt.Errorf("Tencent Cloud virtual-number WebSocket voice_format is unsupported")
		}
	}
	if wait, present := parameters["wait_time"]; present {
		parsed, err := strconv.Atoi(wait)
		if err != nil || parsed < 1 || parsed > 60 {
			return fmt.Errorf("Tencent Cloud virtual-number WebSocket wait_time must be between 1 and 60")
		}
	}
	return nil
}

func signTencentSpeechTranslateWebSocketURL(rawURL string, credentials TencentCredentials, input map[string]any, now time.Time, nonce, voiceID string) (string, error) {
	target, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse Tencent Cloud speech translation WebSocket URL: %w", err)
	}
	if !strings.EqualFold(target.Scheme, "wss") || !strings.EqualFold(target.Hostname(), "asr.cloud.tencent.com") || target.Port() != "" || !tencentSpeechTranslateWebSocketPath.MatchString(target.EscapedPath()) || target.RawQuery != "" || target.User != nil || target.Fragment != "" {
		return "", fmt.Errorf("Tencent Cloud speech translation WebSocket requires wss://asr.cloud.tencent.com/asr/speech_translate/<appid> without caller query parameters")
	}
	if credentials.SecretID == "" || credentials.SecretKey == "" {
		return "", fmt.Errorf("Tencent Cloud speech translation WebSocket requires complete SecretId/SecretKey credentials")
	}
	if credentials.Token != "" {
		return "", fmt.Errorf("Tencent Cloud speech translation WebSocket does not document CAM temporary-token authentication")
	}
	if !validTencentASRNonce(nonce) {
		return "", fmt.Errorf("Tencent Cloud speech translation WebSocket requires a positive nonce of at most 10 digits")
	}
	if !tencentVoiceIDPattern.MatchString(voiceID) {
		return "", fmt.Errorf("Tencent Cloud speech translation WebSocket requires a generated voice_id of at most 128 characters")
	}
	if err := validateTencentSpeechTranslateParameters(input); err != nil {
		return "", err
	}

	parameters := make(map[string]string, len(input)+6)
	for name, value := range input {
		values, _ := stringValues(value)
		parameters[name] = values[0]
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
	source := "asr.cloud.tencent.com" + target.EscapedPath() + "?" + canonical
	parameters["signature"] = base64.StdEncoding.EncodeToString(hmacBytes(sha1.New, []byte(credentials.SecretKey), []byte(source)))
	target.RawQuery = encodeTencentV1Parameters(parameters)
	return target.String(), nil
}

func signTencentVoiceConversionWebSocketURL(rawURL string, credentials TencentCredentials, input map[string]any, now time.Time, voiceID string) (string, error) {
	target, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse Tencent Cloud voice conversion WebSocket URL: %w", err)
	}
	if !strings.EqualFold(target.Scheme, "wss") || !strings.EqualFold(target.Hostname(), "tts.cloud.tencent.com") || target.Port() != "" || !tencentVoiceConversionWebSocketPath.MatchString(target.EscapedPath()) || target.RawQuery != "" || target.User != nil || target.Fragment != "" {
		return "", fmt.Errorf("Tencent Cloud voice conversion WebSocket requires wss://tts.cloud.tencent.com/vc_stream/<appid> without caller query parameters")
	}
	if credentials.SecretID == "" || credentials.SecretKey == "" {
		return "", fmt.Errorf("Tencent Cloud voice conversion WebSocket requires complete SecretId/SecretKey credentials")
	}
	if credentials.Token != "" {
		return "", fmt.Errorf("Tencent Cloud voice conversion WebSocket does not document CAM temporary-token authentication")
	}
	if !tencentVoiceIDPattern.MatchString(voiceID) {
		return "", fmt.Errorf("Tencent Cloud voice conversion WebSocket requires a generated VoiceId of at most 128 characters")
	}
	parameters, err := validateTencentVoiceConversionParameters(input)
	if err != nil {
		return "", err
	}
	timestamp := now.UTC().Unix()
	parameters["SecretId"] = credentials.SecretID
	parameters["Timestamp"] = strconv.FormatInt(timestamp, 10)
	parameters["Expired"] = strconv.FormatInt(timestamp+24*60*60, 10)
	parameters["VoiceId"] = voiceID
	parameters["End"] = "0"
	if _, present := parameters["Volume"]; !present {
		parameters["Volume"] = "0"
	}
	canonical := canonicalTencentV1Parameters(parameters)
	source := "tts.cloud.tencent.com" + target.EscapedPath() + "?" + canonical
	parameters["Signature"] = base64.StdEncoding.EncodeToString(hmacBytes(sha1.New, []byte(credentials.SecretKey), []byte(source)))
	target.Scheme = "wss"
	target.Host = "tts.cloud.tencent.com"
	target.RawQuery = encodeTencentV1Parameters(parameters)
	return target.String(), nil
}

func validateTencentVoiceConversionParameters(input map[string]any) (map[string]string, error) {
	parameters := make(map[string]string, len(input))
	for name, value := range input {
		switch name {
		case "VoiceType", "SampleRate", "Codec", "Volume":
		default:
			return nil, fmt.Errorf("Tencent Cloud voice conversion WebSocket query parameter %q is not documented", name)
		}
		parameter, err := tencentScalarStringValue(value)
		if err != nil {
			return nil, fmt.Errorf("Tencent Cloud voice conversion WebSocket query parameter %q must be scalar", name)
		}
		parameters[name] = parameter
	}
	for _, name := range []string{"VoiceType", "SampleRate", "Codec"} {
		if strings.TrimSpace(parameters[name]) == "" {
			return nil, fmt.Errorf("Tencent Cloud voice conversion WebSocket requires %s", name)
		}
	}
	voiceType, err := strconv.ParseUint(parameters["VoiceType"], 10, 32)
	if err != nil || voiceType == 0 {
		return nil, fmt.Errorf("Tencent Cloud voice conversion WebSocket VoiceType must be a positive uint32")
	}
	if parameters["SampleRate"] != "16000" {
		return nil, fmt.Errorf("Tencent Cloud voice conversion WebSocket SampleRate must be 16000")
	}
	if parameters["Codec"] != "pcm" {
		return nil, fmt.Errorf("Tencent Cloud voice conversion WebSocket Codec must be pcm")
	}
	if volume, present := parameters["Volume"]; present {
		parsed, err := strconv.ParseFloat(volume, 64)
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed < -10 || parsed > 10 {
			return nil, fmt.Errorf("Tencent Cloud voice conversion WebSocket Volume must be between -10 and 10")
		}
	}
	return parameters, nil
}

type tencentVoiceConversionMessage struct {
	VoiceID   string `json:"VoiceId"`
	MessageID string `json:"MessageId"`
	Message   string `json:"Message"`
	Final     int    `json:"Final"`
	Code      int    `json:"Code"`
}

func encodeTencentVoiceConversionFrame(isEnd bool, audio []byte) ([]byte, error) {
	body := struct {
		End int `json:"End"`
	}{}
	if isEnd {
		body.End = 1
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode Tencent Cloud voice conversion frame: %w", err)
	}
	frame := make([]byte, 4+len(encoded)+len(audio))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(encoded)))
	copy(frame[4:], encoded)
	copy(frame[4+len(encoded):], audio)
	return frame, nil
}

func decodeTencentVoiceConversionFrame(frame []byte) (tencentVoiceConversionMessage, []byte, error) {
	if len(frame) < 4 {
		return tencentVoiceConversionMessage{}, nil, fmt.Errorf("Tencent Cloud voice conversion WebSocket returned a truncated frame")
	}
	jsonBytes := int(binary.BigEndian.Uint32(frame[:4]))
	if jsonBytes == 0 || jsonBytes > len(frame)-4 {
		return tencentVoiceConversionMessage{}, nil, fmt.Errorf("Tencent Cloud voice conversion WebSocket returned an invalid JSON length")
	}
	var message tencentVoiceConversionMessage
	if err := json.Unmarshal(frame[4:4+jsonBytes], &message); err != nil {
		return tencentVoiceConversionMessage{}, nil, fmt.Errorf("Tencent Cloud voice conversion WebSocket returned invalid JSON")
	}
	return message, frame[4+jsonBytes:], nil
}

func validateTencentSpeechTranslateParameters(input map[string]any) error {
	parameters := make(map[string]string, len(input))
	for name, value := range input {
		if isTencentASRControlledParameter(name) {
			return fmt.Errorf("caller-supplied Tencent Cloud speech translation WebSocket signing parameter %q is forbidden", name)
		}
		switch name {
		case "voice_format", "source", "target", "trans_model", "enable_tts", "voice_type", "fast_voice_type", "sample_rate", "speed", "volume", "codec", "hotword_list", "filter_dirty", "filter_modal", "filter_punc", "convert_num_mode", "noise_threshold", "domain", "vad_silence_time", "max_speak_time":
		default:
			return fmt.Errorf("Tencent Cloud speech translation WebSocket query parameter %q is not documented", name)
		}
		parameter, err := tencentScalarStringValue(value)
		if err != nil {
			return fmt.Errorf("Tencent Cloud speech translation WebSocket query parameter %q must be scalar", name)
		}
		parameters[name] = parameter
	}
	for _, name := range []string{"source", "target", "trans_model", "voice_format"} {
		if strings.TrimSpace(parameters[name]) == "" {
			return fmt.Errorf("Tencent Cloud speech translation WebSocket requires %s", name)
		}
	}
	if parameters["voice_format"] != "1" && parameters["voice_format"] != "12" {
		return fmt.Errorf("Tencent Cloud speech translation WebSocket voice_format must be 1 or 12")
	}
	if parameters["trans_model"] != "hunyuan-translation-lite" && parameters["trans_model"] != "hunyuan-translation" {
		return fmt.Errorf("Tencent Cloud speech translation WebSocket trans_model is unsupported")
	}
	if value, present := parameters["enable_tts"]; present && value != "0" && value != "1" {
		return fmt.Errorf("Tencent Cloud speech translation WebSocket enable_tts must be 0 or 1")
	}
	if value, present := parameters["codec"]; present && value != "pcm" && value != "mp3" {
		return fmt.Errorf("Tencent Cloud speech translation WebSocket codec must be pcm or mp3")
	}
	return nil
}

func tencentScalarStringValue(value any) (string, error) {
	switch value.(type) {
	case []string, []any:
		return "", fmt.Errorf("array is not scalar")
	}
	values, err := stringValues(value)
	if err != nil || len(values) != 1 {
		return "", fmt.Errorf("value is not scalar")
	}
	return values[0], nil
}

func tencentScalarParameterEquals(parameters map[string]any, name, expected string) bool {
	values, err := stringValues(parameters[name])
	return err == nil && len(values) == 1 && values[0] == expected
}

func isTencentASRControlledParameter(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "expired", "nonce", "secretid", "signature", "timestamp", "token", "voice_id":
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

func secureTencentMPSNonce() string {
	var value [8]byte
	if _, err := rand.Read(value[:]); err == nil {
		return strconv.FormatUint(binary.BigEndian.Uint64(value[:])%9_000_000_000+1_000_000_000, 10)
	}
	return strconv.FormatUint(uint64(time.Now().UTC().UnixNano())%9_000_000_000+1_000_000_000, 10)
}

func signTencentMPSWebSocketURL(rawURL string, credentials TencentCredentials, input map[string]any, now time.Time, nonce string) (string, error) {
	target, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse Tencent Cloud MPS WebSocket URL: %w", err)
	}
	if !strings.EqualFold(target.Scheme, "wss") || !strings.EqualFold(target.Hostname(), "mps.cloud.tencent.com") || target.Port() != "" || !tencentMPSWebSocketPath.MatchString(target.EscapedPath()) || target.RawQuery != "" || target.User != nil || target.Fragment != "" {
		return "", fmt.Errorf("Tencent Cloud MPS WebSocket requires wss://mps.cloud.tencent.com/wss/v1/<appid> without caller query parameters")
	}
	if credentials.SecretID == "" || credentials.SecretKey == "" {
		return "", fmt.Errorf("Tencent Cloud MPS WebSocket requires complete SecretId/SecretKey credentials")
	}
	if credentials.Token != "" {
		return "", fmt.Errorf("Tencent Cloud MPS WebSocket does not document CAM temporary-token authentication")
	}
	if !tencentMPSNoncePattern.MatchString(nonce) {
		return "", fmt.Errorf("Tencent Cloud MPS WebSocket requires a generated 10-digit nonce")
	}
	if err := validateTencentMPSParameters(input); err != nil {
		return "", err
	}

	parameters := make(map[string]string, len(input)+5)
	for name, value := range input {
		values, _ := stringValues(value)
		parameters[name] = values[0]
	}
	timestamp := now.UTC().Unix()
	parameters["timeStamp"] = strconv.FormatInt(timestamp, 10)
	parameters["expired"] = strconv.FormatInt(timestamp+60*60, 10)
	parameters["secretId"] = credentials.SecretID
	parameters["nonce"] = nonce
	canonicalQuery := encodeTencentMPSParameters(parameters)
	canonicalRequest := "post\n" + target.EscapedPath() + "\n" + canonicalQuery + "\n" +
		"content-type:application/json; charset=utf-8\n" +
		"host:mps.cloud.tencent.com\n\n" +
		"content-type;host\n"
	date := now.UTC().Format("2006-01-02")
	scope := date + "/mps/tc3_request"
	stringToSign := "TC3-HMAC-SHA256\n" + strconv.FormatInt(timestamp, 10) + "\n" + scope + "\n" + sha256Hex([]byte(canonicalRequest))
	secretDate := hmacBytes(sha256.New, []byte("TC3"+credentials.SecretKey), []byte(date))
	secretService := hmacBytes(sha256.New, secretDate, []byte("mps"))
	secretSigning := hmacBytes(sha256.New, secretService, []byte("tc3_request"))
	parameters["signature"] = hmacHex(sha256.New, secretSigning, []byte(stringToSign))
	target.Scheme = "wss"
	target.Host = "mps.cloud.tencent.com"
	target.RawQuery = encodeTencentMPSParameters(parameters)
	return target.String(), nil
}

func signTencentMPSTTSWebSocketURL(rawURL string, credentials TencentCredentials, input map[string]any, now time.Time, nonce string) (string, error) {
	target, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse Tencent Cloud MPS TTS WebSocket URL: %w", err)
	}
	if !strings.EqualFold(target.Scheme, "wss") || !strings.EqualFold(target.Hostname(), "mps.cloud.tencent.com") || target.Port() != "" || !tencentMPSTTSWebSocketPath.MatchString(target.EscapedPath()) || target.RawQuery != "" || target.User != nil || target.Fragment != "" {
		return "", fmt.Errorf("Tencent Cloud MPS TTS WebSocket requires wss://mps.cloud.tencent.com/tts/v1/<appid> without caller query parameters")
	}
	if credentials.SecretID == "" || credentials.SecretKey == "" {
		return "", fmt.Errorf("Tencent Cloud MPS TTS WebSocket requires complete SecretId/SecretKey credentials")
	}
	if credentials.Token != "" {
		return "", fmt.Errorf("Tencent Cloud MPS TTS WebSocket does not document CAM temporary-token authentication")
	}
	if !tencentMPSNoncePattern.MatchString(nonce) {
		return "", fmt.Errorf("Tencent Cloud MPS TTS WebSocket requires a generated 10-digit nonce")
	}
	if err := validateTencentMPSTTSParameters(input); err != nil {
		return "", err
	}

	parameters := make(map[string]string, len(input)+5)
	for name, value := range input {
		values, _ := stringValues(value)
		parameters[name] = values[0]
	}
	timestamp := now.UTC().Unix()
	parameters["timeStamp"] = strconv.FormatInt(timestamp, 10)
	parameters["expired"] = strconv.FormatInt(timestamp+60*60, 10)
	parameters["secretId"] = credentials.SecretID
	parameters["nonce"] = nonce
	canonicalQuery := encodeTencentMPSParameters(parameters)
	canonicalRequest := "post\n" + target.EscapedPath() + "\n" + canonicalQuery + "\n" +
		"content-type:application/json; charset=utf-8\n" +
		"host:mps.cloud.tencent.com\n\n" +
		"content-type;host\n" + sha256Hex(nil)
	date := now.UTC().Format("2006-01-02")
	scope := date + "/mps/tc3_request"
	stringToSign := "TC3-HMAC-SHA256\n" + strconv.FormatInt(timestamp, 10) + "\n" + scope + "\n" + sha256Hex([]byte(canonicalRequest))
	secretDate := hmacBytes(sha256.New, []byte("TC3"+credentials.SecretKey), []byte(date))
	secretService := hmacBytes(sha256.New, secretDate, []byte("mps"))
	secretSigning := hmacBytes(sha256.New, secretService, []byte("tc3_request"))
	parameters["signature"] = hmacHex(sha256.New, secretSigning, []byte(stringToSign))
	target.Scheme = "wss"
	target.Host = "mps.cloud.tencent.com"
	target.RawQuery = encodeTencentMPSParameters(parameters)
	return target.String(), nil
}

func signTencentTTSWebSocketURL(rawURL string, credentials TencentCredentials, input map[string]any, text string, now time.Time, sessionID string) (string, error) {
	if _, err := tencentTTSRequestText(text); err != nil {
		return "", err
	}
	return signTencentTTSWebSocketHandshake(rawURL, "/stream_ws", "TextToStreamAudioWS", "Tencent Cloud TTS WebSocket", credentials, input, map[string]string{"Text": text}, now, sessionID)
}

func signTencentStreamingTTSWebSocketURL(rawURL string, credentials TencentCredentials, input map[string]any, now time.Time, sessionID string) (string, error) {
	return signTencentTTSWebSocketHandshake(rawURL, "/stream_wsv2", "TextToStreamAudioWSv2", "Tencent Cloud streaming TTS WebSocket", credentials, input, nil, now, sessionID)
}

func signTencentTTSWebSocketHandshake(rawURL, expectedPath, action, protocol string, credentials TencentCredentials, input map[string]any, extra map[string]string, now time.Time, sessionID string) (string, error) {
	target, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse %s URL: %w", protocol, err)
	}
	if !strings.EqualFold(target.Scheme, "wss") || !strings.EqualFold(target.Hostname(), "tts.cloud.tencent.com") || target.Port() != "" || target.EscapedPath() != expectedPath || target.RawQuery != "" || target.User != nil || target.Fragment != "" {
		return "", fmt.Errorf("%s requires wss://tts.cloud.tencent.com%s without caller query parameters", protocol, expectedPath)
	}
	if credentials.SecretID == "" || credentials.SecretKey == "" {
		return "", fmt.Errorf("%s requires complete SecretId/SecretKey credentials", protocol)
	}
	if credentials.Token != "" {
		return "", fmt.Errorf("%s does not document CAM temporary-token authentication", protocol)
	}
	if !tencentVoiceIDPattern.MatchString(sessionID) {
		return "", fmt.Errorf("%s requires a generated SessionId of at most 128 characters", protocol)
	}
	parameters, err := validateTencentTTSParameters(input)
	if err != nil {
		return "", err
	}
	timestamp := now.UTC().Unix()
	parameters["Action"] = action
	parameters["SecretId"] = credentials.SecretID
	parameters["Timestamp"] = strconv.FormatInt(timestamp, 10)
	parameters["Expired"] = strconv.FormatInt(timestamp+24*60*60, 10)
	parameters["SessionId"] = sessionID
	for name, value := range extra {
		parameters[name] = value
	}
	canonical := canonicalTencentV1Parameters(parameters)
	source := "GETtts.cloud.tencent.com" + expectedPath + "?" + canonical
	parameters["Signature"] = base64.StdEncoding.EncodeToString(hmacBytes(sha1.New, []byte(credentials.SecretKey), []byte(source)))
	target.Scheme = "wss"
	target.Host = "tts.cloud.tencent.com"
	target.RawQuery = encodeTencentV1Parameters(parameters)
	return target.String(), nil
}

func validateTencentTTSParameters(input map[string]any) (map[string]string, error) {
	parameters := make(map[string]string, len(input))
	for name, value := range input {
		if isTencentTTSControlledParameter(name) {
			return nil, fmt.Errorf("caller-supplied Tencent Cloud TTS WebSocket signing parameter %q is forbidden", name)
		}
		switch name {
		case "AppId", "VoiceType", "FastVoiceType", "Volume", "Speed", "SampleRate", "Codec", "EnableSubtitle", "EmotionCategory", "EmotionIntensity", "SegmentRate":
		default:
			return nil, fmt.Errorf("Tencent Cloud TTS WebSocket query parameter %q is not documented", name)
		}
		values, err := stringValues(value)
		if err != nil || len(values) != 1 {
			return nil, fmt.Errorf("Tencent Cloud TTS WebSocket query parameter %q must be scalar", name)
		}
		parameters[name] = values[0]
	}
	if parsed, err := strconv.ParseUint(parameters["AppId"], 10, 63); !tencentTTSAppIDPattern.MatchString(parameters["AppId"]) || err != nil || parsed == 0 {
		return nil, fmt.Errorf("Tencent Cloud TTS WebSocket AppId must be a positive 5 to 19 digit integer")
	}
	codec := strings.ToLower(strings.TrimSpace(parameters["Codec"]))
	if codec != "pcm" && codec != "mp3" {
		return nil, fmt.Errorf("Tencent Cloud TTS WebSocket Codec must be pcm or mp3")
	}
	parameters["Codec"] = codec
	if value, present := parameters["VoiceType"]; present {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed < 0 {
			return nil, fmt.Errorf("Tencent Cloud TTS WebSocket VoiceType must be a non-negative integer")
		}
	}
	if value, present := parameters["FastVoiceType"]; present {
		if strings.TrimSpace(value) == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > 128 {
			return nil, fmt.Errorf("Tencent Cloud TTS WebSocket FastVoiceType must be valid UTF-8 with 1 to 128 characters")
		}
		for _, character := range value {
			if unicode.IsControl(character) {
				return nil, fmt.Errorf("Tencent Cloud TTS WebSocket FastVoiceType must not contain control characters")
			}
		}
	}
	if value, present := parameters["Volume"]; present {
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed < -10 || parsed > 10 {
			return nil, fmt.Errorf("Tencent Cloud TTS WebSocket Volume must be between -10 and 10")
		}
	}
	if value, present := parameters["Speed"]; present {
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed < -2 || parsed > 6 {
			return nil, fmt.Errorf("Tencent Cloud TTS WebSocket Speed must be between -2 and 6")
		}
	}
	if value, present := parameters["SampleRate"]; present && value != "8000" && value != "16000" && value != "24000" {
		return nil, fmt.Errorf("Tencent Cloud TTS WebSocket SampleRate must be 8000, 16000, or 24000")
	}
	if value, present := parameters["EnableSubtitle"]; present {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("Tencent Cloud TTS WebSocket EnableSubtitle must be boolean")
		}
		parameters["EnableSubtitle"] = strconv.FormatBool(parsed)
	}
	if value, present := parameters["EmotionCategory"]; present {
		value = strings.ToLower(strings.TrimSpace(value))
		switch value {
		case "neutral", "sad", "happy", "angry", "fear", "news", "story", "radio", "poetry", "call", "sajiao", "disgusted", "amaze", "peaceful", "exciting", "aojiao", "jieshuo":
		default:
			return nil, fmt.Errorf("Tencent Cloud TTS WebSocket EmotionCategory is unsupported")
		}
		parameters["EmotionCategory"] = value
	}
	if value, present := parameters["EmotionIntensity"]; present {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 50 || parsed > 200 {
			return nil, fmt.Errorf("Tencent Cloud TTS WebSocket EmotionIntensity must be between 50 and 200")
		}
	}
	if value, present := parameters["SegmentRate"]; present {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 || parsed > 2 {
			return nil, fmt.Errorf("Tencent Cloud TTS WebSocket SegmentRate must be 0, 1, or 2")
		}
	}
	return parameters, nil
}

func tencentTTSRequestText(body any) (string, error) {
	text, ok := body.(string)
	if !ok || text == "" || strings.TrimSpace(text) == "" || !utf8.ValidString(text) {
		return "", fmt.Errorf("Tencent Cloud TTS WebSocket body must be a non-empty UTF-8 string")
	}
	limit := 1800
	for _, character := range text {
		if character > unicode.MaxASCII {
			limit = 600
			break
		}
	}
	if utf8.RuneCountInString(text) > limit {
		return "", fmt.Errorf("Tencent Cloud TTS WebSocket body exceeds the documented %d-character limit", limit)
	}
	return text, nil
}

func tencentStreamingTTSTextSegments(body any) ([]string, error) {
	var segments []string
	switch typed := body.(type) {
	case string:
		segments = []string{typed}
	case []string:
		segments = append([]string(nil), typed...)
	case []any:
		segments = make([]string, len(typed))
		for index, value := range typed {
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("Tencent Cloud streaming TTS WebSocket body segments must be strings")
			}
			segments[index] = text
		}
	default:
		return nil, fmt.Errorf("Tencent Cloud streaming TTS WebSocket body must be a string or string array")
	}
	if len(segments) == 0 || len(segments) > 1024 {
		return nil, fmt.Errorf("Tencent Cloud streaming TTS WebSocket requires 1 to 1024 text segments")
	}
	totalCharacters := 0
	for _, text := range segments {
		if text == "" || !utf8.ValidString(text) {
			return nil, fmt.Errorf("Tencent Cloud streaming TTS WebSocket text segments must be non-empty UTF-8 strings")
		}
		totalCharacters += utf8.RuneCountInString(text)
		if totalCharacters > 10000 {
			return nil, fmt.Errorf("Tencent Cloud streaming TTS WebSocket body exceeds the documented 10000-character session limit")
		}
	}
	return segments, nil
}

func isTencentTTSControlledParameter(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "action", "secretid", "timestamp", "expired", "sessionid", "text", "signature":
		return true
	default:
		return false
	}
}

func validateTencentMPSParameters(input map[string]any) error {
	parameters := make(map[string]string, len(input))
	for name, value := range input {
		if isTencentMPSControlledParameter(name) {
			return fmt.Errorf("caller-supplied Tencent Cloud MPS WebSocket signing parameter %q is forbidden", name)
		}
		switch name {
		case "asrDst", "transSrc", "transDst", "fragmentNotify", "resultType", "timeoutSec", "resId":
		default:
			return fmt.Errorf("Tencent Cloud MPS WebSocket query parameter %q is not documented", name)
		}
		values, err := stringValues(value)
		if err != nil || len(values) != 1 {
			return fmt.Errorf("Tencent Cloud MPS WebSocket query parameter %q must be scalar", name)
		}
		parameters[name] = values[0]
	}
	if strings.TrimSpace(parameters["asrDst"]) == "" && (strings.TrimSpace(parameters["transSrc"]) == "" || strings.TrimSpace(parameters["transDst"]) == "") {
		return fmt.Errorf("Tencent Cloud MPS WebSocket requires asrDst or both transSrc and transDst")
	}
	for _, name := range []string{"fragmentNotify", "resultType"} {
		if value, present := parameters[name]; present && value != "0" && value != "1" {
			return fmt.Errorf("Tencent Cloud MPS WebSocket %s must be 0 or 1", name)
		}
	}
	if value, present := parameters["timeoutSec"]; present {
		timeout, err := strconv.Atoi(value)
		if err != nil || timeout < 1 || timeout > 300 {
			return fmt.Errorf("Tencent Cloud MPS WebSocket timeoutSec must be between 1 and 300")
		}
	}
	return nil
}

func validateTencentMPSTTSParameters(input map[string]any) error {
	parameters := make(map[string]string, len(input))
	for name, value := range input {
		if isTencentMPSControlledParameter(name) {
			return fmt.Errorf("caller-supplied Tencent Cloud MPS TTS WebSocket signing parameter %q is forbidden", name)
		}
		switch name {
		case "voiceId", "format", "sampleRate", "language", "timeoutSec", "speed", "vol", "resId":
		default:
			return fmt.Errorf("Tencent Cloud MPS TTS WebSocket query parameter %q is not documented", name)
		}
		values, err := stringValues(value)
		if err != nil || len(values) != 1 {
			return fmt.Errorf("Tencent Cloud MPS TTS WebSocket query parameter %q must be scalar", name)
		}
		parameters[name] = values[0]
	}
	if strings.TrimSpace(parameters["voiceId"]) == "" {
		return fmt.Errorf("Tencent Cloud MPS TTS WebSocket requires voiceId")
	}
	if value, present := parameters["format"]; present {
		switch strings.ToLower(value) {
		case "pcm", "mp3", "wav", "flac", "opus", "ulaw", "alaw":
		default:
			return fmt.Errorf("Tencent Cloud MPS TTS WebSocket format is unsupported")
		}
	}
	if value, present := parameters["sampleRate"]; present {
		parsed, err := strconv.ParseUint(value, 10, 32)
		if err != nil || parsed == 0 {
			return fmt.Errorf("Tencent Cloud MPS TTS WebSocket sampleRate must be a positive uint32")
		}
	}
	if value, present := parameters["timeoutSec"]; present {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed < 1 || parsed > 120 {
			return fmt.Errorf("Tencent Cloud MPS TTS WebSocket timeoutSec must be between 1 and 120")
		}
	}
	for _, name := range []string{"speed", "vol"} {
		if value, present := parameters[name]; present {
			parsed, err := strconv.ParseFloat(value, 64)
			if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
				return fmt.Errorf("Tencent Cloud MPS TTS WebSocket %s must be a finite number", name)
			}
		}
	}
	return nil
}

func tencentTTSTextSegments(body any) ([]string, error) {
	var segments []string
	switch typed := body.(type) {
	case string:
		segments = []string{typed}
	case []string:
		segments = append([]string(nil), typed...)
	case []any:
		segments = make([]string, len(typed))
		for index, value := range typed {
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("Tencent Cloud MPS TTS WebSocket body segments must be strings")
			}
			segments[index] = text
		}
	default:
		return nil, fmt.Errorf("Tencent Cloud MPS TTS WebSocket body must be a string or string array")
	}
	if len(segments) == 0 || len(segments) > 256 {
		return nil, fmt.Errorf("Tencent Cloud MPS TTS WebSocket requires 1 to 256 text segments")
	}
	for _, text := range segments {
		if text == "" || !utf8.ValidString(text) || utf8.RuneCountInString(text) > 5000 {
			return nil, fmt.Errorf("Tencent Cloud MPS TTS WebSocket text segments must be valid UTF-8 with 1 to 5000 characters")
		}
	}
	return segments, nil
}

func isTencentMPSControlledParameter(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "timestamp", "expired", "secretid", "nonce", "signature":
		return true
	default:
		return false
	}
}

func encodeTencentMPSParameters(parameters map[string]string) string {
	names := make([]string, 0, len(parameters))
	for name := range parameters {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, uriEncode(name, true)+"="+uriEncode(parameters[name], true))
	}
	return strings.Join(parts, "&")
}

func encodeTencentMPSAudioFrame(format int, isEnd bool, timestamp uint64, userID string, audio []byte) ([]byte, error) {
	if format != 1 && format != 2 {
		return nil, fmt.Errorf("Tencent Cloud MPS WebSocket stream_format must be 1 or 2")
	}
	if err := validateTencentMPSUserID(userID); err != nil {
		return nil, err
	}
	frame := make([]byte, 14+len(userID)+len(audio))
	frame[0] = byte(format)
	if isEnd {
		frame[1] = 1
	}
	binary.BigEndian.PutUint64(frame[2:10], timestamp)
	binary.BigEndian.PutUint16(frame[10:12], uint16(len(userID)))
	copy(frame[12:], userID)
	extensionOffset := 12 + len(userID)
	binary.BigEndian.PutUint16(frame[extensionOffset:extensionOffset+2], 0)
	copy(frame[extensionOffset+2:], audio)
	return frame, nil
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

func invokeTencentVirtualNumberWebSocket(ctx context.Context, adapter *TencentRESTAdapter, credentials TencentCredentials, invocation Invocation) (InvocationResult, error) {
	voiceID := adapter.config.VoiceID()
	signedURL, err := signTencentVirtualNumberWebSocketURL(invocation.URL, credentials, invocation.Parameters, adapter.config.Now().UTC(), adapter.config.Nonce(), voiceID)
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
		return InvocationResult{}, fmt.Errorf("read Tencent Cloud virtual-number WebSocket handshake")
	}
	requestID, _, err := acceptTencentVirtualNumberText(messageType, handshake, sink, voiceID)
	if err != nil {
		return InvocationResult{}, err
	}
	streamInvocation := invocation
	if streamInvocation.StreamChunkBytes == 0 {
		streamInvocation.StreamChunkBytes = defaultTencentVirtualNumberChunkBytes(invocation.Parameters)
	}
	if streamInvocation.StreamIntervalMS == 0 {
		streamInvocation.StreamIntervalMS = 40
	}
	streamContext, cancelStream := tencentASRStreamContext(ctx, streamInvocation)
	defer cancelStream()
	readResult := make(chan tencentASRReadResult, 1)
	go readTencentVirtualNumberWebSocket(streamContext, cancelStream, connection, sink, requestID, readResult)
	if err := streamTencentASRAudio(streamContext, connection, streamInvocation, adapter.config.StreamPause); err != nil {
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

func acceptTencentVirtualNumberText(messageType tencentWebSocketMessageType, data []byte, sink *tencentWebSocketOutputSink, expectedID string) (string, bool, error) {
	if messageType != tencentWebSocketMessageText {
		return expectedID, false, fmt.Errorf("Tencent Cloud virtual-number WebSocket returned a non-text response")
	}
	var message tencentASRWebSocketMessage
	if err := json.Unmarshal(data, &message); err != nil {
		return expectedID, false, fmt.Errorf("Tencent Cloud virtual-number WebSocket returned invalid JSON")
	}
	if message.Code != 0 {
		return message.VoiceID, false, fmt.Errorf("Tencent Cloud virtual-number WebSocket returned code %d", message.Code)
	}
	if strings.TrimSpace(message.VoiceID) == "" || (expectedID != "" && message.VoiceID != expectedID) {
		return message.VoiceID, false, fmt.Errorf("Tencent Cloud virtual-number WebSocket returned an invalid voice_id")
	}
	if err := sink.writeMessage(data); err != nil {
		return message.VoiceID, false, err
	}
	return message.VoiceID, message.Final == 1, nil
}

func readTencentVirtualNumberWebSocket(ctx context.Context, cancel context.CancelFunc, connection tencentWebSocketConnection, sink *tencentWebSocketOutputSink, requestID string, result chan<- tencentASRReadResult) {
	for {
		messageType, data, err := connection.Read(ctx)
		if err != nil {
			cancel()
			result <- tencentASRReadResult{requestID: requestID, err: fmt.Errorf("read Tencent Cloud virtual-number WebSocket response")}
			return
		}
		currentID, final, err := acceptTencentVirtualNumberText(messageType, data, sink, requestID)
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

func defaultTencentVirtualNumberChunkBytes(parameters map[string]any) int {
	if values, err := stringValues(parameters["voice_format"]); err == nil && len(values) == 1 && values[0] == "1" {
		return 640
	}
	return 4096
}

func invokeTencentSOEWebSocket(ctx context.Context, adapter *TencentRESTAdapter, credentials TencentCredentials, invocation Invocation) (InvocationResult, error) {
	streamInvocation := invocation
	if tencentScalarParameterEquals(invocation.Parameters, "rec_mode", "1") {
		info, err := os.Stat(invocation.BodyFile)
		if err != nil {
			return InvocationResult{}, fmt.Errorf("inspect Tencent Cloud SOE recording body_file: %w", err)
		}
		if info.Size() <= 0 {
			return InvocationResult{}, fmt.Errorf("Tencent Cloud SOE recording body_file is empty")
		}
		streamInvocation.StreamChunkBytes = int(info.Size())
		streamInvocation.StreamIntervalMS = 40
	}
	voiceID := adapter.config.VoiceID()
	signedURL, err := signTencentSOEWebSocketURL(invocation.URL, credentials, invocation.Parameters, adapter.config.Now().UTC(), adapter.config.Nonce(), voiceID)
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
		return InvocationResult{}, fmt.Errorf("read Tencent Cloud SOE WebSocket handshake")
	}
	requestID, _, err := acceptTencentSOEText(messageType, handshake, sink, voiceID)
	if err != nil {
		return InvocationResult{}, err
	}
	if streamInvocation.StreamChunkBytes == 0 {
		if tencentScalarParameterEquals(invocation.Parameters, "voice_format", "0") || invocation.Parameters["voice_format"] == nil {
			streamInvocation.StreamChunkBytes = 1280
		} else {
			streamInvocation.StreamChunkBytes = 4096
		}
	}
	if streamInvocation.StreamIntervalMS == 0 {
		streamInvocation.StreamIntervalMS = 40
	}
	streamContext, cancelStream := tencentASRStreamContext(ctx, streamInvocation)
	defer cancelStream()
	readResult := make(chan tencentASRReadResult, 1)
	go readTencentSOEWebSocket(streamContext, cancelStream, connection, sink, requestID, readResult)
	if err := streamTencentASRAudio(streamContext, connection, streamInvocation, adapter.config.StreamPause); err != nil {
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

func acceptTencentSOEText(messageType tencentWebSocketMessageType, data []byte, sink *tencentWebSocketOutputSink, expectedID string) (string, bool, error) {
	if messageType != tencentWebSocketMessageText {
		return expectedID, false, fmt.Errorf("Tencent Cloud SOE WebSocket returned a non-text response")
	}
	var message tencentASRWebSocketMessage
	if err := json.Unmarshal(data, &message); err != nil {
		return expectedID, false, fmt.Errorf("Tencent Cloud SOE WebSocket returned invalid JSON")
	}
	if message.Code != 0 {
		return message.VoiceID, false, fmt.Errorf("Tencent Cloud SOE WebSocket returned code %d", message.Code)
	}
	if strings.TrimSpace(message.VoiceID) == "" || (expectedID != "" && message.VoiceID != expectedID) {
		return message.VoiceID, false, fmt.Errorf("Tencent Cloud SOE WebSocket returned an invalid voice_id")
	}
	if err := sink.writeMessage(data); err != nil {
		return message.VoiceID, false, err
	}
	return message.VoiceID, message.Final == 1, nil
}

func readTencentSOEWebSocket(ctx context.Context, cancel context.CancelFunc, connection tencentWebSocketConnection, sink *tencentWebSocketOutputSink, requestID string, result chan<- tencentASRReadResult) {
	for {
		messageType, data, err := connection.Read(ctx)
		if err != nil {
			cancel()
			result <- tencentASRReadResult{requestID: requestID, err: fmt.Errorf("read Tencent Cloud SOE WebSocket response")}
			return
		}
		currentID, final, err := acceptTencentSOEText(messageType, data, sink, requestID)
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

type tencentSpeechTranslateReadResult struct {
	requestID string
	err       error
}

func invokeTencentSpeechTranslateWebSocket(ctx context.Context, adapter *TencentRESTAdapter, credentials TencentCredentials, invocation Invocation) (InvocationResult, error) {
	voiceID := adapter.config.VoiceID()
	signedURL, err := signTencentSpeechTranslateWebSocketURL(invocation.URL, credentials, invocation.Parameters, adapter.config.Now().UTC(), adapter.config.Nonce(), voiceID)
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

	ttsEnabled := tencentScalarParameterEquals(invocation.Parameters, "enable_tts", "1")
	messageInvocation := invocation
	if ttsEnabled {
		messageInvocation.ResponseFile = ""
	}
	messageSink, err := newTencentWebSocketOutputSink(messageInvocation, adapter.config.MaxBodyBytes)
	if err != nil {
		return InvocationResult{}, err
	}
	defer messageSink.abort()
	var audioSink *tencentWebSocketOutputSink
	if ttsEnabled {
		audioSink, err = newTencentWebSocketOutputSink(invocation, adapter.config.MaxBodyBytes)
		if err != nil {
			return InvocationResult{}, err
		}
		defer audioSink.abort()
	}

	handshakeContext, cancelHandshake := context.WithTimeout(ctx, adapter.config.Timeout)
	messageType, handshake, err := connection.Read(handshakeContext)
	cancelHandshake()
	if err != nil {
		return InvocationResult{}, fmt.Errorf("read Tencent Cloud speech translation WebSocket handshake")
	}
	requestID, _, err := acceptTencentSpeechTranslateText(messageType, handshake, messageSink, "", 0)
	if err != nil {
		return InvocationResult{}, err
	}
	if requestID == "" {
		return InvocationResult{}, fmt.Errorf("Tencent Cloud speech translation WebSocket handshake omitted voice_id")
	}

	streamContext, cancelStream := tencentASRStreamContext(ctx, invocation)
	defer cancelStream()
	readResult := make(chan tencentSpeechTranslateReadResult, 1)
	finalValue := 1
	if ttsEnabled {
		finalValue = 2
	}
	go readTencentSpeechTranslateWebSocket(streamContext, cancelStream, connection, messageSink, audioSink, requestID, finalValue, readResult)
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
	if !ttsEnabled {
		output, err := messageSink.finish(result.requestID)
		return InvocationResult{Output: output, RequestID: result.requestID}, err
	}
	messages, err := messageSink.finish(result.requestID)
	if err != nil {
		return InvocationResult{}, err
	}
	format, sampleRate := tencentSpeechTranslateAudioProperties(invocation.Parameters)
	audioMetadata, err := audioSink.finishAudio(result.requestID, format, sampleRate)
	if err != nil {
		return InvocationResult{}, err
	}
	output, err := combineTencentSpeechTranslateOutput(messages, audioMetadata)
	if err != nil {
		return InvocationResult{}, err
	}
	return InvocationResult{Output: output, RequestID: result.requestID}, nil
}

func acceptTencentSpeechTranslateText(messageType tencentWebSocketMessageType, data []byte, sink *tencentWebSocketOutputSink, expectedID string, finalValue int) (string, bool, error) {
	if messageType != tencentWebSocketMessageText {
		return expectedID, false, fmt.Errorf("Tencent Cloud speech translation WebSocket returned a non-text response")
	}
	var message tencentASRWebSocketMessage
	if err := json.Unmarshal(data, &message); err != nil {
		return expectedID, false, fmt.Errorf("Tencent Cloud speech translation WebSocket returned invalid JSON")
	}
	if message.Code != 0 {
		return message.VoiceID, false, fmt.Errorf("Tencent Cloud speech translation WebSocket returned code %d", message.Code)
	}
	if strings.TrimSpace(message.VoiceID) == "" || (expectedID != "" && message.VoiceID != expectedID) {
		return message.VoiceID, false, fmt.Errorf("Tencent Cloud speech translation WebSocket returned an invalid voice_id")
	}
	if err := sink.writeMessage(data); err != nil {
		return message.VoiceID, false, err
	}
	return message.VoiceID, finalValue != 0 && message.Final == finalValue, nil
}

func readTencentSpeechTranslateWebSocket(ctx context.Context, cancel context.CancelFunc, connection tencentWebSocketConnection, messageSink, audioSink *tencentWebSocketOutputSink, requestID string, finalValue int, result chan<- tencentSpeechTranslateReadResult) {
	for {
		messageType, data, err := connection.Read(ctx)
		if err != nil {
			cancel()
			result <- tencentSpeechTranslateReadResult{requestID: requestID, err: fmt.Errorf("read Tencent Cloud speech translation WebSocket response")}
			return
		}
		if messageType == tencentWebSocketMessageBinary {
			if audioSink == nil {
				cancel()
				result <- tencentSpeechTranslateReadResult{requestID: requestID, err: fmt.Errorf("Tencent Cloud speech translation WebSocket returned unexpected synthesized audio")}
				return
			}
			if err := audioSink.writeBinary(data); err != nil {
				cancel()
				result <- tencentSpeechTranslateReadResult{requestID: requestID, err: err}
				return
			}
			continue
		}
		currentID, final, err := acceptTencentSpeechTranslateText(messageType, data, messageSink, requestID, finalValue)
		if currentID != "" {
			requestID = currentID
		}
		if err != nil || final {
			if err != nil {
				cancel()
			}
			result <- tencentSpeechTranslateReadResult{requestID: requestID, err: err}
			return
		}
	}
}

func tencentSpeechTranslateAudioProperties(parameters map[string]any) (string, uint32) {
	format := "pcm"
	if values, err := stringValues(parameters["codec"]); err == nil && len(values) == 1 && values[0] != "" {
		format = values[0]
	}
	sampleRate := uint32(16000)
	if values, err := stringValues(parameters["sample_rate"]); err == nil && len(values) == 1 {
		if parsed, parseErr := strconv.ParseUint(values[0], 10, 32); parseErr == nil && parsed > 0 {
			sampleRate = uint32(parsed)
		}
	}
	return format, sampleRate
}

func combineTencentSpeechTranslateOutput(messages, audioMetadata []byte) ([]byte, error) {
	lines := bytes.Split(bytes.TrimSpace(messages), []byte{'\n'})
	encodedMessages := make([]json.RawMessage, 0, len(lines))
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		if !json.Valid(line) {
			return nil, fmt.Errorf("encode Tencent Cloud speech translation messages")
		}
		encodedMessages = append(encodedMessages, append(json.RawMessage(nil), line...))
	}
	if !json.Valid(audioMetadata) {
		return nil, fmt.Errorf("encode Tencent Cloud speech translation audio metadata")
	}
	return json.Marshal(map[string]any{
		"messages": encodedMessages,
		"audio":    json.RawMessage(audioMetadata),
	})
}

func invokeTencentVoiceConversionWebSocket(ctx context.Context, adapter *TencentRESTAdapter, credentials TencentCredentials, invocation Invocation) (InvocationResult, error) {
	voiceID := adapter.config.VoiceID()
	signedURL, err := signTencentVoiceConversionWebSocketURL(invocation.URL, credentials, invocation.Parameters, adapter.config.Now().UTC(), voiceID)
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
		return InvocationResult{}, fmt.Errorf("read Tencent Cloud voice conversion WebSocket handshake")
	}
	requestID, final, err := acceptTencentVoiceConversionFrame(messageType, handshake, sink, voiceID)
	if err != nil {
		return InvocationResult{}, err
	}
	if final {
		output, err := sink.finishAudio(requestID, "pcm", 16000)
		return InvocationResult{Output: output, RequestID: requestID}, err
	}

	streamContext, cancelStream := tencentVoiceConversionStreamContext(ctx, invocation, adapter.config.Timeout)
	defer cancelStream()
	readResult := make(chan tencentASRReadResult, 1)
	go readTencentVoiceConversionWebSocket(streamContext, cancelStream, connection, sink, requestID, readResult)
	if err := streamTencentVoiceConversionAudio(streamContext, connection, invocation, adapter.config.StreamPause); err != nil {
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
	output, err := sink.finishAudio(result.requestID, "pcm", 16000)
	if err != nil {
		return InvocationResult{}, err
	}
	return InvocationResult{Output: output, RequestID: result.requestID}, nil
}

func acceptTencentVoiceConversionFrame(messageType tencentWebSocketMessageType, frame []byte, sink *tencentWebSocketOutputSink, expectedID string) (string, bool, error) {
	if messageType != tencentWebSocketMessageBinary {
		return expectedID, false, fmt.Errorf("Tencent Cloud voice conversion WebSocket returned a non-binary response")
	}
	message, audio, err := decodeTencentVoiceConversionFrame(frame)
	if err != nil {
		return expectedID, false, err
	}
	if message.Code != 0 {
		return message.VoiceID, false, fmt.Errorf("Tencent Cloud voice conversion WebSocket returned code %d", message.Code)
	}
	if strings.TrimSpace(message.VoiceID) == "" || (expectedID != "" && message.VoiceID != expectedID) {
		return message.VoiceID, false, fmt.Errorf("Tencent Cloud voice conversion WebSocket returned an invalid VoiceId")
	}
	if len(audio) > 0 {
		if err := sink.writeBinary(audio); err != nil {
			return message.VoiceID, false, err
		}
	}
	return message.VoiceID, message.Final == 1, nil
}

func readTencentVoiceConversionWebSocket(ctx context.Context, cancel context.CancelFunc, connection tencentWebSocketConnection, sink *tencentWebSocketOutputSink, requestID string, result chan<- tencentASRReadResult) {
	for {
		messageType, data, err := connection.Read(ctx)
		if err != nil {
			cancel()
			result <- tencentASRReadResult{requestID: requestID, err: fmt.Errorf("read Tencent Cloud voice conversion WebSocket response")}
			return
		}
		currentID, final, err := acceptTencentVoiceConversionFrame(messageType, data, sink, requestID)
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

func streamTencentVoiceConversionAudio(ctx context.Context, connection tencentWebSocketConnection, invocation Invocation, pause func(context.Context, time.Duration) error) error {
	audio, err := os.Open(invocation.BodyFile)
	if err != nil {
		return fmt.Errorf("open Tencent Cloud voice conversion audio body_file: %w", err)
	}
	defer audio.Close()
	chunkBytes := invocation.StreamChunkBytes
	if chunkBytes == 0 {
		chunkBytes = 3200
	}
	interval := invocation.StreamIntervalMS
	if interval == 0 {
		interval = 100
	}
	reader := bufio.NewReaderSize(audio, chunkBytes+1)
	buffer := make([]byte, chunkBytes)
	for {
		count, readErr := io.ReadFull(reader, buffer)
		if readErr == io.EOF && count == 0 {
			return fmt.Errorf("Tencent Cloud voice conversion audio body_file is empty")
		}
		if readErr != nil && readErr != io.ErrUnexpectedEOF {
			return fmt.Errorf("read Tencent Cloud voice conversion audio body_file: %w", readErr)
		}
		isEnd := readErr == io.ErrUnexpectedEOF
		if readErr == nil {
			if _, peekErr := reader.Peek(1); peekErr == io.EOF {
				isEnd = true
			} else if peekErr != nil {
				return fmt.Errorf("read Tencent Cloud voice conversion audio body_file: %w", peekErr)
			}
		}
		frame, err := encodeTencentVoiceConversionFrame(isEnd, buffer[:count])
		if err != nil {
			return err
		}
		if err := connection.Write(ctx, tencentWebSocketMessageBinary, frame); err != nil {
			return fmt.Errorf("write Tencent Cloud voice conversion audio frame")
		}
		if isEnd {
			return nil
		}
		if err := pause(ctx, time.Duration(interval)*time.Millisecond); err != nil {
			return fmt.Errorf("pace Tencent Cloud voice conversion audio stream: %w", err)
		}
	}
}

func tencentVoiceConversionStreamContext(ctx context.Context, invocation Invocation, adapterTimeout time.Duration) (context.Context, context.CancelFunc) {
	chunkBytes := invocation.StreamChunkBytes
	if chunkBytes == 0 {
		chunkBytes = 3200
	}
	interval := invocation.StreamIntervalMS
	if interval == 0 {
		interval = 100
	}
	duration := adapterTimeout + 2*time.Minute
	if info, err := os.Stat(invocation.BodyFile); err == nil && info.Size() > 0 {
		chunks := (info.Size() + int64(chunkBytes) - 1) / int64(chunkBytes)
		duration += time.Duration(chunks) * time.Duration(interval) * time.Millisecond
	}
	if duration > time.Hour {
		duration = time.Hour
	}
	return context.WithTimeout(ctx, duration)
}

type tencentMPSHandshake struct {
	Code    int    `json:"Code"`
	Message string `json:"Message"`
	TaskID  string `json:"TaskId"`
}

type tencentMPSNotification struct {
	Response struct {
		NotificationType string `json:"NotificationType"`
		TaskID           string `json:"TaskId"`
		ProcessEofInfo   struct {
			ErrCode int    `json:"ErrCode"`
			Message string `json:"Message"`
		} `json:"ProcessEofInfo"`
	} `json:"Response"`
}

func invokeTencentMPSWebSocket(ctx context.Context, adapter *TencentRESTAdapter, credentials TencentCredentials, invocation Invocation) (InvocationResult, error) {
	signedURL, err := signTencentMPSWebSocketURL(invocation.URL, credentials, invocation.Parameters, adapter.config.Now().UTC(), adapter.config.MPSNonce())
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
		return InvocationResult{}, fmt.Errorf("read Tencent Cloud MPS WebSocket handshake")
	}
	taskID, err := acceptTencentMPSHandshake(messageType, handshake, sink)
	if err != nil {
		return InvocationResult{}, err
	}

	streamContext, cancelStream := tencentMPSStreamContext(ctx, invocation, adapter.config.Timeout)
	defer cancelStream()
	readResult := make(chan tencentASRReadResult, 1)
	go readTencentMPSWebSocket(streamContext, cancelStream, connection, sink, taskID, readResult)
	if err := streamTencentMPSAudio(streamContext, connection, invocation, adapter.config.StreamPause); err != nil {
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

func acceptTencentMPSHandshake(messageType tencentWebSocketMessageType, data []byte, sink *tencentWebSocketOutputSink) (string, error) {
	if messageType != tencentWebSocketMessageText {
		return "", fmt.Errorf("Tencent Cloud MPS WebSocket returned a non-text handshake")
	}
	var message tencentMPSHandshake
	if err := json.Unmarshal(data, &message); err != nil {
		return "", fmt.Errorf("Tencent Cloud MPS WebSocket returned an invalid handshake")
	}
	if message.Code != 0 {
		return message.TaskID, fmt.Errorf("Tencent Cloud MPS WebSocket returned handshake code %d", message.Code)
	}
	if strings.TrimSpace(message.TaskID) == "" {
		return "", fmt.Errorf("Tencent Cloud MPS WebSocket handshake omitted TaskId")
	}
	if err := sink.writeMessage(data); err != nil {
		return message.TaskID, err
	}
	return message.TaskID, nil
}

func readTencentMPSWebSocket(ctx context.Context, cancel context.CancelFunc, connection tencentWebSocketConnection, sink *tencentWebSocketOutputSink, taskID string, result chan<- tencentASRReadResult) {
	for {
		messageType, data, err := connection.Read(ctx)
		if err != nil {
			cancel()
			result <- tencentASRReadResult{requestID: taskID, err: fmt.Errorf("read Tencent Cloud MPS WebSocket response")}
			return
		}
		currentID, final, err := acceptTencentMPSNotification(messageType, data, sink, taskID)
		if currentID != "" {
			taskID = currentID
		}
		if err != nil || final {
			if err != nil {
				cancel()
			}
			result <- tencentASRReadResult{requestID: taskID, err: err}
			return
		}
	}
}

func acceptTencentMPSNotification(messageType tencentWebSocketMessageType, data []byte, sink *tencentWebSocketOutputSink, expectedTaskID string) (string, bool, error) {
	if messageType != tencentWebSocketMessageText {
		return "", false, fmt.Errorf("Tencent Cloud MPS WebSocket returned a non-text response")
	}
	var message tencentMPSNotification
	if err := json.Unmarshal(data, &message); err != nil {
		return "", false, fmt.Errorf("Tencent Cloud MPS WebSocket returned invalid JSON")
	}
	taskID := strings.TrimSpace(message.Response.TaskID)
	if taskID == "" || (expectedTaskID != "" && taskID != expectedTaskID) {
		return taskID, false, fmt.Errorf("Tencent Cloud MPS WebSocket returned an invalid TaskId")
	}
	switch message.Response.NotificationType {
	case "AiRecognitionResult":
		if err := sink.writeMessage(data); err != nil {
			return taskID, false, err
		}
		return taskID, false, nil
	case "ProcessEof":
		if err := sink.writeMessage(data); err != nil {
			return taskID, false, err
		}
		if code := message.Response.ProcessEofInfo.ErrCode; code != 0 && code != 4002 {
			return taskID, true, fmt.Errorf("Tencent Cloud MPS WebSocket ended with code %d", code)
		}
		return taskID, true, nil
	default:
		return taskID, false, fmt.Errorf("Tencent Cloud MPS WebSocket returned an unsupported notification type")
	}
}

func streamTencentMPSAudio(ctx context.Context, connection tencentWebSocketConnection, invocation Invocation, pause func(context.Context, time.Duration) error) error {
	audio, err := os.Open(invocation.BodyFile)
	if err != nil {
		return fmt.Errorf("open Tencent Cloud MPS audio body_file: %w", err)
	}
	defer audio.Close()
	chunkBytes := invocation.StreamChunkBytes
	if chunkBytes == 0 {
		chunkBytes = defaultTencentMPSChunkBytes(invocation.StreamFormat)
	}
	interval := invocation.StreamIntervalMS
	if interval == 0 {
		interval = 40
	}
	reader := bufio.NewReaderSize(audio, chunkBytes+1)
	buffer := make([]byte, chunkBytes)
	var sentBytes uint64
	bytesPerMillisecond := uint64(32)
	if invocation.StreamFormat == 2 {
		bytesPerMillisecond = 16
	}
	for {
		count, readErr := io.ReadFull(reader, buffer)
		if readErr == io.EOF && count == 0 {
			return fmt.Errorf("Tencent Cloud MPS audio body_file is empty")
		}
		if readErr != nil && readErr != io.ErrUnexpectedEOF {
			return fmt.Errorf("read Tencent Cloud MPS audio body_file: %w", readErr)
		}
		isEnd := readErr == io.ErrUnexpectedEOF
		if readErr == nil {
			if _, peekErr := reader.Peek(1); peekErr == io.EOF {
				isEnd = true
			} else if peekErr != nil {
				return fmt.Errorf("read Tencent Cloud MPS audio body_file: %w", peekErr)
			}
		}
		frame, err := encodeTencentMPSAudioFrame(invocation.StreamFormat, isEnd, sentBytes/bytesPerMillisecond, invocation.StreamUserID, buffer[:count])
		if err != nil {
			return err
		}
		if err := connection.Write(ctx, tencentWebSocketMessageBinary, frame); err != nil {
			return fmt.Errorf("write Tencent Cloud MPS audio frame")
		}
		sentBytes += uint64(count)
		if isEnd {
			return nil
		}
		if err := pause(ctx, time.Duration(interval)*time.Millisecond); err != nil {
			return fmt.Errorf("pace Tencent Cloud MPS audio stream: %w", err)
		}
	}
}

func defaultTencentMPSChunkBytes(format int) int {
	if format == 2 {
		return 640
	}
	return 1280
}

func tencentMPSStreamContext(ctx context.Context, invocation Invocation, adapterTimeout time.Duration) (context.Context, context.CancelFunc) {
	chunkBytes := invocation.StreamChunkBytes
	if chunkBytes == 0 {
		chunkBytes = defaultTencentMPSChunkBytes(invocation.StreamFormat)
	}
	interval := invocation.StreamIntervalMS
	if interval == 0 {
		interval = 40
	}
	timeoutSeconds := 120
	if values, err := stringValues(invocation.Parameters["timeoutSec"]); err == nil && len(values) == 1 {
		if parsed, parseErr := strconv.Atoi(values[0]); parseErr == nil && parsed >= 1 && parsed <= 300 {
			timeoutSeconds = parsed
		}
	}
	duration := adapterTimeout + time.Duration(timeoutSeconds)*time.Second
	if info, err := os.Stat(invocation.BodyFile); err == nil && info.Size() > 0 {
		chunks := (info.Size() + int64(chunkBytes) - 1) / int64(chunkBytes)
		duration += time.Duration(chunks) * time.Duration(interval) * time.Millisecond
	}
	if duration > time.Hour {
		duration = time.Hour
	}
	return context.WithTimeout(ctx, duration)
}

type tencentMPSTTSMessage struct {
	NotificationType string `json:"NotificationType"`
	TaskID           string `json:"TaskId"`
	HandshakeResult  struct {
		Code       int    `json:"Code"`
		Message    string `json:"Message"`
		Format     string `json:"Format"`
		SampleRate uint32 `json:"SampleRate"`
	} `json:"HandshakeResult"`
	ProcessEofInfo struct {
		Code    int    `json:"Code"`
		Message string `json:"Message"`
	} `json:"ProcessEofInfo"`
}

type tencentMPSTTSReadResult struct {
	taskID string
	err    error
}

func invokeTencentMPSTTSWebSocket(ctx context.Context, adapter *TencentRESTAdapter, credentials TencentCredentials, invocation Invocation) (InvocationResult, error) {
	segments, err := tencentTTSTextSegments(invocation.Body)
	if err != nil {
		return InvocationResult{}, err
	}
	signedURL, err := signTencentMPSTTSWebSocketURL(invocation.URL, credentials, invocation.Parameters, adapter.config.Now().UTC(), adapter.config.MPSNonce())
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
	messageType, handshakeData, err := connection.Read(handshakeContext)
	cancelHandshake()
	if err != nil {
		return InvocationResult{}, fmt.Errorf("read Tencent Cloud MPS TTS WebSocket handshake")
	}
	handshake, err := acceptTencentMPSTTSHandshake(messageType, handshakeData)
	if err != nil {
		return InvocationResult{}, err
	}

	streamContext, cancelStream := tencentMPSTTSStreamContext(ctx, invocation, adapter.config.Timeout)
	defer cancelStream()
	readResult := make(chan tencentMPSTTSReadResult, 1)
	go readTencentMPSTTSWebSocket(streamContext, cancelStream, connection, sink, handshake.TaskID, readResult)
	if err := sendTencentMPSTTSText(streamContext, connection, segments); err != nil {
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
	output, err := sink.finishAudio(result.taskID, handshake.HandshakeResult.Format, handshake.HandshakeResult.SampleRate)
	if err != nil {
		return InvocationResult{}, err
	}
	return InvocationResult{Output: output, RequestID: result.taskID}, nil
}

func acceptTencentMPSTTSHandshake(messageType tencentWebSocketMessageType, data []byte) (tencentMPSTTSMessage, error) {
	if messageType != tencentWebSocketMessageText {
		return tencentMPSTTSMessage{}, fmt.Errorf("Tencent Cloud MPS TTS WebSocket returned a non-text handshake")
	}
	var message tencentMPSTTSMessage
	if err := json.Unmarshal(data, &message); err != nil || message.NotificationType != "Handshake" {
		return tencentMPSTTSMessage{}, fmt.Errorf("Tencent Cloud MPS TTS WebSocket returned an invalid handshake")
	}
	if message.HandshakeResult.Code != 0 {
		return tencentMPSTTSMessage{}, fmt.Errorf("Tencent Cloud MPS TTS WebSocket returned handshake code %d", message.HandshakeResult.Code)
	}
	if strings.TrimSpace(message.TaskID) == "" || strings.TrimSpace(message.HandshakeResult.Format) == "" || message.HandshakeResult.SampleRate == 0 {
		return tencentMPSTTSMessage{}, fmt.Errorf("Tencent Cloud MPS TTS WebSocket handshake omitted negotiated session fields")
	}
	return message, nil
}

func sendTencentMPSTTSText(ctx context.Context, connection tencentWebSocketConnection, segments []string) error {
	type request struct {
		Text  string `json:"Text"`
		Final bool   `json:"Final"`
	}
	for _, segment := range segments {
		data, _ := json.Marshal(request{Text: segment, Final: false})
		if err := connection.Write(ctx, tencentWebSocketMessageText, data); err != nil {
			return fmt.Errorf("write Tencent Cloud MPS TTS text segment")
		}
	}
	final, _ := json.Marshal(request{Text: "", Final: true})
	if err := connection.Write(ctx, tencentWebSocketMessageText, final); err != nil {
		return fmt.Errorf("finish Tencent Cloud MPS TTS text stream")
	}
	return nil
}

func readTencentMPSTTSWebSocket(ctx context.Context, cancel context.CancelFunc, connection tencentWebSocketConnection, sink *tencentWebSocketOutputSink, taskID string, result chan<- tencentMPSTTSReadResult) {
	for {
		messageType, data, err := connection.Read(ctx)
		if err != nil {
			cancel()
			result <- tencentMPSTTSReadResult{taskID: taskID, err: fmt.Errorf("read Tencent Cloud MPS TTS WebSocket response")}
			return
		}
		if messageType == tencentWebSocketMessageBinary {
			if err := sink.writeBinary(data); err != nil {
				cancel()
				result <- tencentMPSTTSReadResult{taskID: taskID, err: err}
				return
			}
			continue
		}
		if messageType != tencentWebSocketMessageText {
			cancel()
			result <- tencentMPSTTSReadResult{taskID: taskID, err: fmt.Errorf("Tencent Cloud MPS TTS WebSocket returned an unsupported message type")}
			return
		}
		var message tencentMPSTTSMessage
		if err := json.Unmarshal(data, &message); err != nil || message.NotificationType != "ProcessEof" {
			cancel()
			result <- tencentMPSTTSReadResult{taskID: taskID, err: fmt.Errorf("Tencent Cloud MPS TTS WebSocket returned an invalid notification")}
			return
		}
		if strings.TrimSpace(message.TaskID) == "" || message.TaskID != taskID {
			cancel()
			result <- tencentMPSTTSReadResult{taskID: taskID, err: fmt.Errorf("Tencent Cloud MPS TTS WebSocket returned an invalid TaskId")}
			return
		}
		if message.ProcessEofInfo.Code != 0 {
			cancel()
			result <- tencentMPSTTSReadResult{taskID: taskID, err: fmt.Errorf("Tencent Cloud MPS TTS WebSocket ended with code %d", message.ProcessEofInfo.Code)}
			return
		}
		result <- tencentMPSTTSReadResult{taskID: taskID}
		return
	}
}

func tencentMPSTTSStreamContext(ctx context.Context, invocation Invocation, adapterTimeout time.Duration) (context.Context, context.CancelFunc) {
	timeoutSeconds := 30
	if values, err := stringValues(invocation.Parameters["timeoutSec"]); err == nil && len(values) == 1 {
		if parsed, parseErr := strconv.Atoi(values[0]); parseErr == nil && parsed >= 1 && parsed <= 120 {
			timeoutSeconds = parsed
		}
	}
	return context.WithTimeout(ctx, adapterTimeout+time.Duration(timeoutSeconds)*time.Second)
}

type tencentTTSMessage struct {
	Code      int    `json:"code"`
	Message   string `json:"message"`
	SessionID string `json:"session_id"`
	RequestID string `json:"request_id"`
	MessageID string `json:"message_id"`
	Final     int    `json:"final"`
	Ready     int    `json:"ready"`
	Heartbeat int    `json:"heartbeat"`
}

func invokeTencentTTSWebSocket(ctx context.Context, adapter *TencentRESTAdapter, credentials TencentCredentials, invocation Invocation) (InvocationResult, error) {
	text, err := tencentTTSRequestText(invocation.Body)
	if err != nil {
		return InvocationResult{}, err
	}
	sessionID := adapter.config.VoiceID()
	signedURL, err := signTencentTTSWebSocketURL(invocation.URL, credentials, invocation.Parameters, text, adapter.config.Now().UTC(), sessionID)
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

	messageInvocation := invocation
	messageInvocation.ResponseFile = ""
	messageSink, err := newTencentWebSocketOutputSink(messageInvocation, adapter.config.MaxBodyBytes)
	if err != nil {
		return InvocationResult{}, err
	}
	defer messageSink.abort()
	audioSink, err := newTencentWebSocketOutputSink(invocation, adapter.config.MaxBodyBytes)
	if err != nil {
		return InvocationResult{}, err
	}
	defer audioSink.abort()

	streamContext, cancelStream := context.WithTimeout(ctx, adapter.config.Timeout+5*time.Minute)
	defer cancelStream()
	messageType, data, err := connection.Read(streamContext)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("read Tencent Cloud TTS WebSocket handshake")
	}
	requestID, final, err := acceptTencentTTSText(messageType, data, messageSink, sessionID, "")
	if err != nil {
		return InvocationResult{}, err
	}
	for !final {
		messageType, data, err = connection.Read(streamContext)
		if err != nil {
			return InvocationResult{}, fmt.Errorf("read Tencent Cloud TTS WebSocket response")
		}
		if messageType == tencentWebSocketMessageBinary {
			if err := audioSink.writeBinary(data); err != nil {
				return InvocationResult{}, err
			}
			continue
		}
		requestID, final, err = acceptTencentTTSText(messageType, data, messageSink, sessionID, requestID)
		if err != nil {
			return InvocationResult{}, err
		}
	}
	if audioSink.written == 0 {
		return InvocationResult{}, fmt.Errorf("Tencent Cloud TTS WebSocket completed without audio")
	}
	messages, err := messageSink.finish(requestID)
	if err != nil {
		return InvocationResult{}, err
	}
	format, sampleRate := tencentTTSAudioProperties(invocation.Parameters)
	audioMetadata, err := audioSink.finishAudio(requestID, format, sampleRate)
	if err != nil {
		return InvocationResult{}, err
	}
	output, err := combineTencentSpeechTranslateOutput(messages, audioMetadata)
	if err != nil {
		return InvocationResult{}, err
	}
	return InvocationResult{Output: output, RequestID: requestID}, nil
}

func acceptTencentTTSText(messageType tencentWebSocketMessageType, data []byte, sink *tencentWebSocketOutputSink, expectedSessionID, expectedRequestID string) (string, bool, error) {
	if messageType != tencentWebSocketMessageText {
		return expectedRequestID, false, fmt.Errorf("Tencent Cloud TTS WebSocket returned a non-text status response")
	}
	var message tencentTTSMessage
	if err := json.Unmarshal(data, &message); err != nil {
		return expectedRequestID, false, fmt.Errorf("Tencent Cloud TTS WebSocket returned invalid JSON")
	}
	if message.Code != 0 {
		return message.RequestID, false, fmt.Errorf("Tencent Cloud TTS WebSocket returned code %d", message.Code)
	}
	if message.SessionID == "" || message.SessionID != expectedSessionID {
		return message.RequestID, false, fmt.Errorf("Tencent Cloud TTS WebSocket returned an invalid session_id")
	}
	if strings.TrimSpace(message.RequestID) == "" || (expectedRequestID != "" && message.RequestID != expectedRequestID) {
		return message.RequestID, false, fmt.Errorf("Tencent Cloud TTS WebSocket returned an invalid request_id")
	}
	if message.Final != 0 && message.Final != 1 {
		return message.RequestID, false, fmt.Errorf("Tencent Cloud TTS WebSocket returned an invalid final value")
	}
	if err := sink.writeMessage(data); err != nil {
		return message.RequestID, false, err
	}
	return message.RequestID, message.Final == 1, nil
}

func tencentTTSAudioProperties(parameters map[string]any) (string, uint32) {
	format := "pcm"
	if values, err := stringValues(parameters["Codec"]); err == nil && len(values) == 1 && values[0] != "" {
		format = strings.ToLower(values[0])
	}
	sampleRate := uint32(16000)
	if values, err := stringValues(parameters["SampleRate"]); err == nil && len(values) == 1 {
		if parsed, parseErr := strconv.ParseUint(values[0], 10, 32); parseErr == nil && parsed > 0 {
			sampleRate = uint32(parsed)
		}
	}
	return format, sampleRate
}

func invokeTencentStreamingTTSWebSocket(ctx context.Context, adapter *TencentRESTAdapter, credentials TencentCredentials, invocation Invocation) (InvocationResult, error) {
	segments, err := tencentStreamingTTSTextSegments(invocation.Body)
	if err != nil {
		return InvocationResult{}, err
	}
	sessionID := adapter.config.VoiceID()
	signedURL, err := signTencentStreamingTTSWebSocketURL(invocation.URL, credentials, invocation.Parameters, adapter.config.Now().UTC(), sessionID)
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

	messageInvocation := invocation
	messageInvocation.ResponseFile = ""
	messageSink, err := newTencentWebSocketOutputSink(messageInvocation, adapter.config.MaxBodyBytes)
	if err != nil {
		return InvocationResult{}, err
	}
	defer messageSink.abort()
	audioSink, err := newTencentWebSocketOutputSink(invocation, adapter.config.MaxBodyBytes)
	if err != nil {
		return InvocationResult{}, err
	}
	defer audioSink.abort()

	streamContext, cancelStream := context.WithTimeout(ctx, adapter.config.Timeout+10*time.Minute)
	defer cancelStream()
	requestID := ""
	ready := false
	for !ready {
		messageType, data, err := connection.Read(streamContext)
		if err != nil {
			return InvocationResult{}, fmt.Errorf("read Tencent Cloud streaming TTS WebSocket handshake")
		}
		if messageType != tencentWebSocketMessageText {
			return InvocationResult{}, fmt.Errorf("Tencent Cloud streaming TTS WebSocket returned binary audio before READY")
		}
		var final bool
		requestID, ready, final, err = acceptTencentStreamingTTSText(messageType, data, messageSink, sessionID, requestID)
		if err != nil {
			return InvocationResult{}, err
		}
		if final {
			return InvocationResult{}, fmt.Errorf("Tencent Cloud streaming TTS WebSocket ended before READY")
		}
	}

	readResult := make(chan tencentSpeechTranslateReadResult, 1)
	go readTencentStreamingTTSWebSocket(streamContext, cancelStream, connection, messageSink, audioSink, sessionID, requestID, readResult)
	if err := sendTencentStreamingTTSText(streamContext, connection, sessionID, segments); err != nil {
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
	if audioSink.written == 0 {
		return InvocationResult{}, fmt.Errorf("Tencent Cloud streaming TTS WebSocket completed without audio")
	}
	messages, err := messageSink.finish(result.requestID)
	if err != nil {
		return InvocationResult{}, err
	}
	format, sampleRate := tencentTTSAudioProperties(invocation.Parameters)
	audioMetadata, err := audioSink.finishAudio(result.requestID, format, sampleRate)
	if err != nil {
		return InvocationResult{}, err
	}
	output, err := combineTencentSpeechTranslateOutput(messages, audioMetadata)
	if err != nil {
		return InvocationResult{}, err
	}
	return InvocationResult{Output: output, RequestID: result.requestID}, nil
}

func sendTencentStreamingTTSText(ctx context.Context, connection tencentWebSocketConnection, sessionID string, segments []string) error {
	type command struct {
		SessionID string `json:"session_id"`
		MessageID string `json:"message_id"`
		Action    string `json:"action"`
		Data      string `json:"data"`
	}
	for _, segment := range segments {
		data, _ := json.Marshal(command{SessionID: sessionID, MessageID: secureTencentVoiceID(), Action: "ACTION_SYNTHESIS", Data: segment})
		if err := connection.Write(ctx, tencentWebSocketMessageText, data); err != nil {
			return fmt.Errorf("write Tencent Cloud streaming TTS text segment")
		}
	}
	data, _ := json.Marshal(command{SessionID: sessionID, MessageID: secureTencentVoiceID(), Action: "ACTION_COMPLETE", Data: ""})
	if err := connection.Write(ctx, tencentWebSocketMessageText, data); err != nil {
		return fmt.Errorf("finish Tencent Cloud streaming TTS text stream")
	}
	return nil
}

func readTencentStreamingTTSWebSocket(ctx context.Context, cancel context.CancelFunc, connection tencentWebSocketConnection, messageSink, audioSink *tencentWebSocketOutputSink, sessionID, requestID string, result chan<- tencentSpeechTranslateReadResult) {
	for {
		messageType, data, err := connection.Read(ctx)
		if err != nil {
			cancel()
			result <- tencentSpeechTranslateReadResult{requestID: requestID, err: fmt.Errorf("read Tencent Cloud streaming TTS WebSocket response")}
			return
		}
		if messageType == tencentWebSocketMessageBinary {
			if err := audioSink.writeBinary(data); err != nil {
				cancel()
				result <- tencentSpeechTranslateReadResult{requestID: requestID, err: err}
				return
			}
			continue
		}
		currentID, _, final, err := acceptTencentStreamingTTSText(messageType, data, messageSink, sessionID, requestID)
		if currentID != "" {
			requestID = currentID
		}
		if err != nil || final {
			if err != nil {
				cancel()
			}
			result <- tencentSpeechTranslateReadResult{requestID: requestID, err: err}
			return
		}
	}
}

func acceptTencentStreamingTTSText(messageType tencentWebSocketMessageType, data []byte, sink *tencentWebSocketOutputSink, expectedSessionID, expectedRequestID string) (string, bool, bool, error) {
	if messageType != tencentWebSocketMessageText {
		return expectedRequestID, false, false, fmt.Errorf("Tencent Cloud streaming TTS WebSocket returned a non-text status response")
	}
	var message tencentTTSMessage
	if err := json.Unmarshal(data, &message); err != nil {
		return expectedRequestID, false, false, fmt.Errorf("Tencent Cloud streaming TTS WebSocket returned invalid JSON")
	}
	if message.Code != 0 && message.Code != 10009 {
		return message.RequestID, false, false, fmt.Errorf("Tencent Cloud streaming TTS WebSocket returned code %d", message.Code)
	}
	if message.SessionID == "" || message.SessionID != expectedSessionID {
		return message.RequestID, false, false, fmt.Errorf("Tencent Cloud streaming TTS WebSocket returned an invalid session_id")
	}
	if strings.TrimSpace(message.RequestID) == "" || (expectedRequestID != "" && message.RequestID != expectedRequestID) {
		return message.RequestID, false, false, fmt.Errorf("Tencent Cloud streaming TTS WebSocket returned an invalid request_id")
	}
	if (message.Final != 0 && message.Final != 1) || (message.Ready != 0 && message.Ready != 1) || (message.Heartbeat != 0 && message.Heartbeat != 1) {
		return message.RequestID, false, false, fmt.Errorf("Tencent Cloud streaming TTS WebSocket returned an invalid event value")
	}
	if err := sink.writeMessage(data); err != nil {
		return message.RequestID, false, false, err
	}
	return message.RequestID, message.Ready == 1, message.Final == 1, nil
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
	return sink.write(data, true)
}

func (sink *tencentWebSocketOutputSink) writeBinary(data []byte) error {
	return sink.write(data, false)
}

func (sink *tencentWebSocketOutputSink) write(data []byte, newline bool) error {
	messageBytes := int64(len(data))
	if newline {
		messageBytes++
	}
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
	if newline {
		if _, err := writer.Write([]byte{'\n'}); err != nil {
			return fmt.Errorf("write Tencent Cloud WebSocket response: %w", err)
		}
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
	return sink.finishFile(map[string]any{
		"response_file": sink.target,
		"bytes":         sink.written,
		"content_type":  "application/x-ndjson",
		"request_id":    requestID,
	})
}

func (sink *tencentWebSocketOutputSink) finishAudio(requestID, format string, sampleRate uint32) ([]byte, error) {
	if sink.finished {
		return nil, fmt.Errorf("Tencent Cloud WebSocket response sink already finalized")
	}
	if sink.temporary == nil {
		return nil, fmt.Errorf("Tencent Cloud TTS WebSocket requires response_file")
	}
	sink.finished = true
	return sink.finishFile(map[string]any{
		"response_file": sink.target,
		"bytes":         sink.written,
		"content_type":  tencentTTSAudioContentType(format),
		"request_id":    requestID,
		"format":        format,
		"sample_rate":   sampleRate,
	})
}

func (sink *tencentWebSocketOutputSink) finishFile(metadata map[string]any) ([]byte, error) {
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
	encoded, err := json.Marshal(metadata)
	if err != nil {
		os.Remove(sink.target)
		return nil, fmt.Errorf("encode response_file metadata: %w", err)
	}
	return encoded, nil
}

func tencentTTSAudioContentType(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "mp3":
		return "audio/mpeg"
	case "wav":
		return "audio/wav"
	case "flac":
		return "audio/flac"
	default:
		return "application/octet-stream"
	}
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

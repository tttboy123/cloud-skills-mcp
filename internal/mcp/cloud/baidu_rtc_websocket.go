package cloud

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/coder/websocket"
)

const (
	authSchemeBaiduRTCAgentWS = "rtc-aiagent-ws"
	baiduRTCControlEndpoint   = "https://rtc-aiagent.baidubce.com"
	baiduRTCWebSocketTarget   = "wss://rtc-aiotgw.exp.bcelive.com/v1/realtime"
	baiduRTCDefaultPacketMS   = 20
	baiduRTCMaxPacketMS       = 200
	baiduRTCMaxPacketCount    = 4096
	baiduRTCImageChunkBytes   = 16 * 1024
)

var (
	baiduRTCInstanceIDPattern = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)
	baiduRTCTrackIDPattern    = regexp.MustCompile(`^[0-9]{1,64}$`)
	baiduRTCImageNamePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,255}$`)
)

type baiduRTCAgentPlan struct {
	AppID              string
	InstanceType       string
	Config             map[string]any
	AudioCodec         string
	OpusPacketTimeMS   int
	OpusPacketLengths  []int
	OpusPacketMaxBytes int
	DeviceID           string
	UserID             string
	Messages           []string
	FinalMessages      []string
	ImageMode          string
	MaxMessages        int
	Timeout            time.Duration
	TerminalEvent      string
}

type baiduRTCAudioPlan struct {
	Codec          string
	ChunkBytes     int
	Interval       time.Duration
	PacketLengths  []int
	MaxPacketBytes int
}

type baiduRTCCreateResponse struct {
	InstanceID   json.Number `json:"ai_agent_instance_id"`
	InstanceType string      `json:"instance_type"`
	Context      struct {
		CID   json.Number `json:"cid"`
		Token string      `json:"token"`
	} `json:"context"`
}

func validateBaiduRTCAgentWebSocketInvocation(invocation Invocation) error {
	if !strings.EqualFold(strings.TrimSpace(invocation.Method), http.MethodGet) {
		return fmt.Errorf("Baidu RTC AI Agent WebSocket requires method GET for the HTTP upgrade")
	}
	target, err := url.Parse(invocation.URL)
	if err != nil || target.String() != baiduRTCWebSocketTarget || target.User != nil || target.Fragment != "" || target.RawQuery != "" {
		return fmt.Errorf("Baidu RTC AI Agent requires the exact credential-free %s target", baiduRTCWebSocketTarget)
	}
	if invocation.Service != "rtc-aiagent" || invocation.Operation != "RealtimeInteraction" {
		return fmt.Errorf("Baidu RTC AI Agent requires service rtc-aiagent and operation RealtimeInteraction")
	}
	if invocation.APIVersion != "1" {
		return fmt.Errorf("Baidu RTC AI Agent requires api_version 1")
	}
	if invocation.AuthVersion != "" && invocation.AuthVersion != "v1" {
		return fmt.Errorf("Baidu RTC AI Agent control plane requires BCE auth_version v1")
	}
	if len(invocation.Parameters) != 0 || len(invocation.Headers) != 0 {
		return fmt.Errorf("Baidu RTC AI Agent does not accept caller query parameters or handshake headers")
	}
	if invocation.ResponseFile == "" {
		return fmt.Errorf("Baidu RTC AI Agent requires response_file for atomic NDJSON output")
	}
	if invocation.Body == nil {
		return fmt.Errorf("Baidu RTC AI Agent requires an inline protocol plan")
	}
	plan, err := parseBaiduRTCAgentPlan(invocation.Body)
	if err != nil {
		return err
	}
	if len(plan.Messages) == 0 && len(plan.FinalMessages) == 0 && invocation.BodyFile == "" && invocation.ImageFile == "" {
		return fmt.Errorf("Baidu RTC AI Agent requires at least one client command, body_file audio stream, or event-correlated image_file")
	}
	if invocation.ImageFile != "" {
		if err := validateBaiduRTCImageFile(invocation.ImageFile); err != nil {
			return err
		}
	} else if plan.ImageMode != "" {
		return fmt.Errorf("Baidu RTC AI Agent image_mode requires image_file")
	}
	if _, err := buildBaiduRTCAudioPlan(plan, invocation); err != nil {
		return err
	}
	if invocation.Region != "" || invocation.Project != "" || invocation.Subscription != "" || invocation.Audience != "" || invocation.RegionSet != "" || invocation.PayloadMode != "" || invocation.ChecksumAlgorithm != "" || invocation.ProtobufDescriptorFile != "" || invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return fmt.Errorf("Baidu RTC AI Agent does not accept unrelated provider or transport controls")
	}
	return nil
}

func buildBaiduRTCAudioPlan(plan baiduRTCAgentPlan, invocation Invocation) (baiduRTCAudioPlan, error) {
	audio := baiduRTCAudioPlan{Codec: plan.AudioCodec}
	if invocation.BodyFile == "" {
		if invocation.StreamChunkBytes != 0 || invocation.StreamIntervalMS != 0 || len(plan.OpusPacketLengths) != 0 || plan.OpusPacketMaxBytes != 0 {
			return baiduRTCAudioPlan{}, fmt.Errorf("Baidu RTC stream controls require body_file audio")
		}
		return audio, nil
	}
	info, err := os.Stat(invocation.BodyFile)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxRequestFileBytes {
		return baiduRTCAudioPlan{}, fmt.Errorf("Baidu RTC %s body_file must be a non-empty regular file below %d bytes", plan.AudioCodec, maxRequestFileBytes)
	}
	if plan.AudioCodec == "opus" {
		if len(plan.OpusPacketLengths) == 0 {
			return baiduRTCAudioPlan{}, fmt.Errorf("Baidu RTC Opus body_file requires opus_packet_lengths")
		}
		if invocation.StreamChunkBytes != 0 {
			return baiduRTCAudioPlan{}, fmt.Errorf("Baidu RTC variable Opus packets use opus_packet_lengths instead of stream_chunk_bytes")
		}
		if invocation.StreamIntervalMS != 0 && invocation.StreamIntervalMS != plan.OpusPacketTimeMS {
			return baiduRTCAudioPlan{}, fmt.Errorf("Baidu RTC Opus stream_interval_ms must match opus_packet_time_ms")
		}
		total := int64(0)
		maximum := 0
		for _, length := range plan.OpusPacketLengths {
			total += int64(length)
			if length > maximum {
				maximum = length
			}
		}
		if total != info.Size() {
			return baiduRTCAudioPlan{}, fmt.Errorf("Baidu RTC opus_packet_lengths must exactly cover body_file")
		}
		if plan.OpusPacketMaxBytes > 0 {
			maximum = plan.OpusPacketMaxBytes
		}
		audio.Interval = time.Duration(plan.OpusPacketTimeMS) * time.Millisecond
		audio.PacketLengths = append([]int(nil), plan.OpusPacketLengths...)
		audio.MaxPacketBytes = maximum
	} else {
		intervalMS := invocation.StreamIntervalMS
		if intervalMS == 0 {
			intervalMS = baiduRTCDefaultPacketMS
		}
		if intervalMS < baiduRTCDefaultPacketMS || intervalMS > baiduRTCMaxPacketMS {
			return baiduRTCAudioPlan{}, fmt.Errorf("Baidu RTC audio packets must represent 20-200 ms")
		}
		bytesPerMS := map[string]int{"raw": 16, "raw16k": 32, "pcma": 8, "pcmu": 8, "g722": 8}[plan.AudioCodec]
		if bytesPerMS == 0 {
			return baiduRTCAudioPlan{}, fmt.Errorf("Baidu RTC fixed-rate audio codec is invalid")
		}
		chunkBytes := bytesPerMS * intervalMS
		if invocation.StreamChunkBytes != 0 && invocation.StreamChunkBytes != chunkBytes {
			return baiduRTCAudioPlan{}, fmt.Errorf("Baidu RTC %s audio requires stream_chunk_bytes %d for %d-ms packets", plan.AudioCodec, chunkBytes, intervalMS)
		}
		if info.Size()%int64(chunkBytes) != 0 {
			return baiduRTCAudioPlan{}, fmt.Errorf("Baidu RTC %s body_file must contain complete %d-byte/%d-ms packets", plan.AudioCodec, chunkBytes, intervalMS)
		}
		audio.ChunkBytes = chunkBytes
		audio.Interval = time.Duration(intervalMS) * time.Millisecond
		audio.MaxPacketBytes = chunkBytes
	}
	packetCount := info.Size() / int64(audio.MaxPacketBytes)
	if len(audio.PacketLengths) != 0 {
		packetCount = int64(len(audio.PacketLengths))
	}
	if time.Duration(packetCount)*audio.Interval >= plan.Timeout {
		return baiduRTCAudioPlan{}, fmt.Errorf("Baidu RTC %s audio duration must be shorter than timeout_seconds", plan.AudioCodec)
	}
	return audio, nil
}

func parseBaiduRTCAgentPlan(body any) (baiduRTCAgentPlan, error) {
	if baiduRTCAgentContainsCredentialField(body) {
		return baiduRTCAgentPlan{}, fmt.Errorf("Baidu RTC AI Agent requires a credential-free protocol plan")
	}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) > maxRequestPayloadBytes {
		return baiduRTCAgentPlan{}, fmt.Errorf("Baidu RTC AI Agent plan must be bounded JSON")
	}
	var raw struct {
		AppID              string         `json:"app_id"`
		InstanceType       string         `json:"instance_type"`
		Config             map[string]any `json:"config"`
		AudioCodec         string         `json:"audio_codec"`
		OpusPacketTimeMS   int            `json:"opus_packet_time_ms"`
		OpusPacketLengths  []int          `json:"opus_packet_lengths"`
		OpusPacketMaxBytes int            `json:"opus_packet_max_bytes"`
		DeviceID           string         `json:"device_id"`
		UserID             string         `json:"user_id"`
		Messages           []string       `json:"messages"`
		FinalMessages      []string       `json:"final_messages"`
		ImageMode          string         `json:"image_mode"`
		MaxMessages        int            `json:"max_messages"`
		Timeout            int            `json:"timeout_seconds"`
		TerminalEvent      string         `json:"terminal_event"`
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return baiduRTCAgentPlan{}, fmt.Errorf("Baidu RTC AI Agent plan is invalid: %w", err)
	}
	if !identifierPattern.MatchString(raw.AppID) {
		return baiduRTCAgentPlan{}, fmt.Errorf("Baidu RTC AI Agent app_id must be a safe identifier")
	}
	if raw.InstanceType == "" {
		raw.InstanceType = "VoiceChat"
	}
	switch raw.InstanceType {
	case "VoiceChat", "DigitalHuman", "RealtimeTranslation", "ClawChat":
	default:
		return baiduRTCAgentPlan{}, fmt.Errorf("Baidu RTC AI Agent instance_type must be VoiceChat, DigitalHuman, RealtimeTranslation, or ClawChat")
	}
	if !validBaiduRTCIdentity(raw.DeviceID) || !validBaiduRTCIdentity(raw.UserID) {
		return baiduRTCAgentPlan{}, fmt.Errorf("Baidu RTC AI Agent requires bounded device_id and user_id values for license activation")
	}
	if baiduRTCConfigContainsCredentialMaterial(raw.Config) {
		return baiduRTCAgentPlan{}, fmt.Errorf("Baidu RTC AI Agent config contains embedded credential material")
	}
	if raw.AudioCodec == "" {
		raw.AudioCodec = "raw16k"
	}
	switch raw.AudioCodec {
	case "raw", "raw16k", "pcma", "pcmu", "g722", "opus":
	default:
		return baiduRTCAgentPlan{}, fmt.Errorf("Baidu RTC AI Agent audio_codec must be raw, raw16k, pcma, pcmu, g722, or opus")
	}
	if codec, present := raw.Config["audiocodec"]; present {
		codecString, ok := codec.(string)
		if !ok || codecString != raw.AudioCodec {
			return baiduRTCAgentPlan{}, fmt.Errorf("Baidu RTC AI Agent config.audiocodec must match audio_codec")
		}
	}
	if raw.AudioCodec == "opus" {
		if raw.OpusPacketTimeMS == 0 {
			raw.OpusPacketTimeMS = baiduRTCDefaultPacketMS
		}
		if raw.OpusPacketTimeMS != 20 && raw.OpusPacketTimeMS != 40 && raw.OpusPacketTimeMS != 60 {
			return baiduRTCAgentPlan{}, fmt.Errorf("Baidu RTC AI Agent opus_packet_time_ms must be 20, 40, or 60")
		}
		if len(raw.OpusPacketLengths) > baiduRTCMaxPacketCount {
			return baiduRTCAgentPlan{}, fmt.Errorf("Baidu RTC AI Agent accepts at most %d Opus packets", baiduRTCMaxPacketCount)
		}
		if raw.OpusPacketMaxBytes < 0 || raw.OpusPacketMaxBytes > maxRequestPayloadBytes {
			return baiduRTCAgentPlan{}, fmt.Errorf("Baidu RTC AI Agent opus_packet_max_bytes is out of range")
		}
		for _, length := range raw.OpusPacketLengths {
			if length < 1 || length > maxRequestPayloadBytes || (raw.OpusPacketMaxBytes > 0 && length > raw.OpusPacketMaxBytes) {
				return baiduRTCAgentPlan{}, fmt.Errorf("Baidu RTC AI Agent Opus packet lengths must be positive and no larger than opus_packet_max_bytes")
			}
		}
	} else if raw.OpusPacketTimeMS != 0 || len(raw.OpusPacketLengths) != 0 || raw.OpusPacketMaxBytes != 0 {
		return baiduRTCAgentPlan{}, fmt.Errorf("Baidu RTC AI Agent Opus packet controls require audio_codec opus")
	}
	if len(raw.Messages)+len(raw.FinalMessages) > 64 {
		return baiduRTCAgentPlan{}, fmt.Errorf("Baidu RTC AI Agent accepts at most 64 client commands")
	}
	if raw.ImageMode != "" && raw.ImageMode != "image_generate" {
		return baiduRTCAgentPlan{}, fmt.Errorf("Baidu RTC AI Agent image_mode must be image_generate or omitted")
	}
	for _, message := range append(append([]string(nil), raw.Messages...), raw.FinalMessages...) {
		if err := validateBaiduRTCClientMessage(message); err != nil {
			return baiduRTCAgentPlan{}, err
		}
	}
	if raw.MaxMessages < 1 || raw.MaxMessages > 256 {
		return baiduRTCAgentPlan{}, fmt.Errorf("Baidu RTC AI Agent max_messages must be between 1 and 256")
	}
	if raw.Timeout < 1 || raw.Timeout > 300 {
		return baiduRTCAgentPlan{}, fmt.Errorf("Baidu RTC AI Agent timeout_seconds must be between 1 and 300")
	}
	if raw.TerminalEvent != "tts_end" && raw.TerminalEvent != "answer" && raw.TerminalEvent != "message_limit" {
		return baiduRTCAgentPlan{}, fmt.Errorf("Baidu RTC AI Agent terminal_event must be tts_end, answer, or message_limit")
	}
	return baiduRTCAgentPlan{
		AppID: raw.AppID, InstanceType: raw.InstanceType, Config: raw.Config,
		AudioCodec: raw.AudioCodec, OpusPacketTimeMS: raw.OpusPacketTimeMS,
		OpusPacketLengths: raw.OpusPacketLengths, OpusPacketMaxBytes: raw.OpusPacketMaxBytes,
		DeviceID: raw.DeviceID, UserID: raw.UserID, Messages: raw.Messages, FinalMessages: raw.FinalMessages, ImageMode: raw.ImageMode,
		MaxMessages: raw.MaxMessages, Timeout: time.Duration(raw.Timeout) * time.Second,
		TerminalEvent: raw.TerminalEvent,
	}, nil
}

func baiduRTCAgentContainsCredentialField(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for name, child := range typed {
			normalized := normalizedOperation(name)
			switch normalized {
			case "authorization", "apikey", "accesstoken", "bearertoken", "clientsecret", "secretaccesskey", "accesskeyid", "sessiontoken", "securitytoken", "llmtoken", "token", "ak", "sk", "lickey", "licensekey", "password", "credential":
				return true
			}
			if baiduRTCAgentContainsCredentialField(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if baiduRTCAgentContainsCredentialField(child) {
				return true
			}
		}
	}
	return false
}

func baiduRTCConfigContainsCredentialMaterial(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for _, child := range typed {
			if baiduRTCConfigContainsCredentialMaterial(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if baiduRTCConfigContainsCredentialMaterial(child) {
				return true
			}
		}
	case string:
		lower := strings.ToLower(typed)
		for _, marker := range []string{"authorization", "bearer ", "api_key", "apikey", "access_token", "llm_token", "client_secret", "secret_access_key", "security_token", "session_token", "lickey", "license_key"} {
			if strings.Contains(lower, marker) {
				return true
			}
		}
	}
	return false
}

func validBaiduRTCText(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) && character != '\t' {
			return false
		}
	}
	return true
}

func validateBaiduRTCClientMessage(message string) error {
	if len(message) < 3 || len(message) > 16*1024 || !validBaiduRTCText(message) {
		return fmt.Errorf("Baidu RTC AI Agent client messages must be bounded UTF-8 protocol commands")
	}
	if message == "[B]" || message == "[B]:[END]" || baiduRTCExactClientCommand(message) {
		return nil
	}
	if strings.HasPrefix(message, "[B]:[BEGIN]:") {
		delay, err := strconv.Atoi(strings.TrimPrefix(message, "[B]:[BEGIN]:"))
		if err == nil && delay >= 1 && delay <= 300000 {
			return nil
		}
		return fmt.Errorf("Baidu RTC AI Agent break delay must be 1-300000 milliseconds")
	}
	for _, prefix := range []string{"[T]:", "[TTS]:"} {
		if strings.HasPrefix(message, prefix) {
			if len(message) > len(prefix) {
				return nil
			}
			return fmt.Errorf("Baidu RTC AI Agent text commands require content")
		}
	}
	if strings.HasPrefix(message, "[SET]:[DEVICE_INFO]:") {
		object, err := decodeBaiduRTCCommandObject(strings.TrimPrefix(message, "[SET]:[DEVICE_INFO]:"))
		if err != nil || !baiduRTCObjectHasOnly(object, "os", "soc", "model", "user_id", "asr_text_ext") || len(object) == 0 {
			return fmt.Errorf("Baidu RTC AI Agent DEVICE_INFO command is invalid")
		}
		for _, name := range []string{"os", "soc", "model", "user_id"} {
			if value, present := object[name]; present && !boundedBaiduRTCJSONString(value, 256, true) {
				return fmt.Errorf("Baidu RTC AI Agent DEVICE_INFO command is invalid")
			}
		}
		if value, present := object["asr_text_ext"]; present {
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("Baidu RTC AI Agent DEVICE_INFO command is invalid")
			}
		}
		return nil
	}
	if strings.HasPrefix(message, "[SET]:[GIS]") {
		coordinates := strings.Split(strings.TrimPrefix(message, "[SET]:[GIS]"), ",")
		if len(coordinates) == 2 {
			latitude, latitudeErr := strconv.ParseFloat(coordinates[0], 64)
			longitude, longitudeErr := strconv.ParseFloat(coordinates[1], 64)
			if latitudeErr == nil && longitudeErr == nil && latitude >= -90 && latitude <= 90 && longitude >= -180 && longitude <= 180 {
				return nil
			}
		}
		return fmt.Errorf("Baidu RTC AI Agent GIS command requires valid latitude,longitude")
	}
	if strings.HasPrefix(message, "[SET]:[UPDATE_SYSTEM_PROMPT]:") {
		object, err := decodeBaiduRTCCommandObject(strings.TrimPrefix(message, "[SET]:[UPDATE_SYSTEM_PROMPT]:"))
		if err != nil || !baiduRTCObjectHasOnly(object, "model_type", "prompt") {
			return fmt.Errorf("Baidu RTC AI Agent system-prompt command is invalid")
		}
		modelType, modelOK := object["model_type"].(string)
		prompt, promptOK := object["prompt"].(string)
		if !modelOK || (modelType != "2" && modelType != "3") || !promptOK || len(prompt) > 16*1024 || !validBaiduRTCText(prompt) {
			return fmt.Errorf("Baidu RTC AI Agent system-prompt command is invalid")
		}
		return nil
	}
	for _, prefix := range []string{"[SET]:[VARIABLES]:", "[SET]:[TP_EXTRA_DATA]:"} {
		if strings.HasPrefix(message, prefix) {
			object, err := decodeBaiduRTCCommandObject(strings.TrimPrefix(message, prefix))
			if err != nil || len(object) == 0 || len(object) > 64 {
				return fmt.Errorf("Baidu RTC AI Agent dynamic-data command is invalid")
			}
			return nil
		}
	}
	if strings.HasPrefix(message, "[OP]:[switchSceneRole]:") {
		return validateBaiduRTCSwitchRole(strings.TrimPrefix(message, "[OP]:[switchSceneRole]:"))
	}
	if strings.HasPrefix(message, "[SET]:[ENHANCE_QUERY]:") {
		return validateBaiduRTCEnhanceQuery(strings.TrimPrefix(message, "[SET]:[ENHANCE_QUERY]:"))
	}
	if strings.HasPrefix(message, "[E]:[DC]:") {
		return validateBaiduRTCDirectControl(strings.TrimPrefix(message, "[E]:[DC]:"))
	}
	if strings.HasPrefix(message, "[E]:[CMD]:[MEETING_SUMMARY_CREATE]:") {
		return validateBaiduRTCMeetingSummary(strings.TrimPrefix(message, "[E]:[CMD]:[MEETING_SUMMARY_CREATE]:"))
	}
	return fmt.Errorf("Baidu RTC AI Agent client message is not a documented client command")
}

func baiduRTCExactClientCommand(message string) bool {
	switch message {
	case "[SET]:[AUTO_INT]:[FALSE]", "[SET]:[AUTO_INT]:[TRUE]",
		"[E]:[CMD]:[REMOTE_PLAYER]:[STOP]", "[E]:[CMD]:[REMOTE_PLAYER]:[PAUSE]", "[E]:[CMD]:[REMOTE_PLAYER]:[RESUME]",
		"[E]:[CMD]:[ASR_ENABLE_REALTIME]", "[E]:[CMD]:[ASR_DISABLE_REALTIME]", "[E]:[CMD]:[ASR_START_LONGTEXT_REC]", "[E]:[CMD]:[ASR_STOP_LONGTEXT_REC]",
		"[E]:[CMD]:[MCP_TOOLS_CHANGED]", "[E]:[CMD]:[MEETING_SUMMARY_FINISH]":
		return true
	default:
		return false
	}
}

func decodeBaiduRTCCommandObject(raw string) (map[string]any, error) {
	if raw == "" || len(raw) > 16*1024 || !validBaiduRTCText(raw) {
		return nil, fmt.Errorf("invalid command JSON")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, fmt.Errorf("invalid command JSON")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("invalid command JSON")
	}
	if baiduRTCAgentContainsCredentialField(object) || baiduRTCConfigContainsCredentialMaterial(object) {
		return nil, fmt.Errorf("command JSON contains credential material")
	}
	return object, nil
}

func baiduRTCObjectHasOnly(object map[string]any, allowed ...string) bool {
	allowedNames := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		allowedNames[name] = struct{}{}
	}
	for name := range object {
		if _, ok := allowedNames[name]; !ok {
			return false
		}
	}
	return true
}

func boundedBaiduRTCJSONString(value any, maximum int, allowEmpty bool) bool {
	text, ok := value.(string)
	return ok && len(text) <= maximum && (allowEmpty || text != "") && validBaiduRTCText(text)
}

func validateBaiduRTCSwitchRole(raw string) error {
	object, err := decodeBaiduRTCCommandObject(raw)
	if err != nil || len(object) == 0 || !baiduRTCObjectHasOnly(object, "tts", "scene_role", "scene_role_cfg") {
		return fmt.Errorf("Baidu RTC AI Agent switchSceneRole command is invalid")
	}
	for _, name := range []string{"tts", "scene_role"} {
		if value, present := object[name]; present && !boundedBaiduRTCJSONString(value, 16*1024, false) {
			return fmt.Errorf("Baidu RTC AI Agent switchSceneRole command is invalid")
		}
	}
	if value, present := object["scene_role_cfg"]; present {
		config, ok := value.(map[string]any)
		if !ok || !baiduRTCObjectHasOnly(config, "name", "prompt") || !boundedBaiduRTCJSONString(config["name"], 256, false) || !boundedBaiduRTCJSONString(config["prompt"], 16*1024, false) {
			return fmt.Errorf("Baidu RTC AI Agent switchSceneRole command is invalid")
		}
	}
	return nil
}

func validateBaiduRTCEnhanceQuery(raw string) error {
	object, err := decodeBaiduRTCCommandObject(raw)
	if err != nil || !baiduRTCObjectHasOnly(object, "enhance_type", "pre_query", "post_query") {
		return fmt.Errorf("Baidu RTC AI Agent ENHANCE_QUERY command is invalid")
	}
	enhanceType, ok := object["enhance_type"].(string)
	if !ok || (enhanceType != "0" && enhanceType != "1" && enhanceType != "2" && enhanceType != "3") {
		return fmt.Errorf("Baidu RTC AI Agent ENHANCE_QUERY command is invalid")
	}
	pre, _ := object["pre_query"].(string)
	post, _ := object["post_query"].(string)
	if value, present := object["pre_query"]; present {
		if _, ok := value.(string); !ok {
			return fmt.Errorf("Baidu RTC AI Agent ENHANCE_QUERY command is invalid")
		}
	}
	if value, present := object["post_query"]; present {
		if _, ok := value.(string); !ok {
			return fmt.Errorf("Baidu RTC AI Agent ENHANCE_QUERY command is invalid")
		}
	}
	if len(pre)+len(post) > 1024 || !validBaiduRTCText(pre) || !validBaiduRTCText(post) || (enhanceType == "1" && pre == "") || (enhanceType == "2" && post == "") || (enhanceType == "3" && (pre == "" || post == "")) {
		return fmt.Errorf("Baidu RTC AI Agent ENHANCE_QUERY command is invalid")
	}
	return nil
}

func validateBaiduRTCDirectControl(raw string) error {
	object, err := decodeBaiduRTCCommandObject(raw)
	if err != nil || !baiduRTCObjectHasOnly(object, "intent", "parameter_list") || object["intent"] != "music" {
		return fmt.Errorf("Baidu RTC AI Agent direct-control command is invalid")
	}
	parameters, ok := object["parameter_list"].([]any)
	if !ok || len(parameters) < 1 || len(parameters) > 8 {
		return fmt.Errorf("Baidu RTC AI Agent direct-control command is invalid")
	}
	hasSource := false
	for _, parameter := range parameters {
		entry, ok := parameter.(map[string]any)
		if !ok || len(entry) != 1 || !baiduRTCObjectHasOnly(entry, "url", "track_id", "text") {
			return fmt.Errorf("Baidu RTC AI Agent direct-control command is invalid")
		}
		for name, value := range entry {
			if !boundedBaiduRTCJSONString(value, 4096, false) {
				return fmt.Errorf("Baidu RTC AI Agent direct-control command is invalid")
			}
			if name == "url" {
				if !validBaiduRTCMediaURL(value.(string)) {
					return fmt.Errorf("Baidu RTC AI Agent direct-control media URL is invalid")
				}
				hasSource = true
			}
			if name == "track_id" {
				if !baiduRTCTrackIDPattern.MatchString(value.(string)) {
					return fmt.Errorf("Baidu RTC AI Agent direct-control track_id is invalid")
				}
				hasSource = true
			}
		}
	}
	if !hasSource {
		return fmt.Errorf("Baidu RTC AI Agent direct-control command requires a URL or track_id")
	}
	return nil
}

func validBaiduRTCMediaURL(value string) bool {
	target, err := url.Parse(value)
	if err != nil || target.Scheme != "https" || target.User != nil || target.Fragment != "" || target.Hostname() == "" || (target.Port() != "" && target.Port() != "443") {
		return false
	}
	host := strings.ToLower(target.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return false
	}
	if address := net.ParseIP(host); address != nil && (address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast() || address.IsUnspecified()) {
		return false
	}
	return true
}

func validateBaiduRTCMeetingSummary(raw string) error {
	if raw == "" {
		return nil
	}
	fields := strings.Split(raw, "|")
	if len(fields) != 4 || len(raw) > 1024 {
		return fmt.Errorf("Baidu RTC AI Agent meeting-summary command is invalid")
	}
	if fields[0] != "" && !identifierPattern.MatchString(fields[0]) {
		return fmt.Errorf("Baidu RTC AI Agent meeting-summary taskKey is invalid")
	}
	if fields[1] != "" && !identifierPattern.MatchString(fields[1]) {
		return fmt.Errorf("Baidu RTC AI Agent meeting-summary promptMode is invalid")
	}
	if fields[2] != "" && fields[2] != "true" && fields[2] != "false" {
		return fmt.Errorf("Baidu RTC AI Agent meeting-summary aiSummaryEnable is invalid")
	}
	allowedModules := map[string]struct{}{"basicInfo": {}, "fullSummary": {}, "todoList": {}}
	if fields[3] != "" {
		seen := make(map[string]struct{})
		for _, module := range strings.Split(fields[3], ",") {
			if _, ok := allowedModules[module]; !ok {
				return fmt.Errorf("Baidu RTC AI Agent meeting-summary module is invalid")
			}
			if _, duplicate := seen[module]; duplicate {
				return fmt.Errorf("Baidu RTC AI Agent meeting-summary modules must be unique")
			}
			seen[module] = struct{}{}
		}
	}
	return nil
}

func validBaiduRTCIdentity(value string) bool {
	return len(value) >= 1 && len(value) <= 256 && validBaiduRTCText(value)
}

func validateBaiduRTCImageFile(path string) error {
	name := filepath.Base(path)
	if name == "." || name == ".." || !baiduRTCImageNamePattern.MatchString(name) {
		return fmt.Errorf("Baidu RTC image_file basename must contain only letters, digits, dot, underscore, or hyphen")
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect Baidu RTC image_file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxRequestFileBytes {
		return fmt.Errorf("Baidu RTC image_file must be a non-empty regular file below %d bytes", maxRequestFileBytes)
	}
	return nil
}

type baiduRTCSerializedConnection struct {
	cloudWebSocketConnection
	writeMu sync.Mutex
}

func (connection *baiduRTCSerializedConnection) Write(ctx context.Context, messageType cloudWebSocketMessageType, data []byte) error {
	connection.writeMu.Lock()
	defer connection.writeMu.Unlock()
	return connection.cloudWebSocketConnection.Write(ctx, messageType, data)
}

func (connection *baiduRTCSerializedConnection) exclusiveWrite(run func(cloudWebSocketConnection) error) error {
	connection.writeMu.Lock()
	defer connection.writeMu.Unlock()
	return run(connection.cloudWebSocketConnection)
}

type baiduRTCExclusiveWriter interface {
	exclusiveWrite(func(cloudWebSocketConnection) error) error
}

func defaultBaiduRTCWebSocketDial(ctx context.Context, target string) (cloudWebSocketConnection, error) {
	client := &http.Client{
		Transport:     http.DefaultTransport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	connection, response, err := websocket.Dial(ctx, target, &websocket.DialOptions{
		HTTPClient: client, CompressionMode: websocket.CompressionDisabled,
	})
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("Baidu RTC AI Agent WebSocket handshake failed with HTTP %d", response.StatusCode)
		}
		return nil, fmt.Errorf("Baidu RTC AI Agent WebSocket handshake failed")
	}
	connection.SetReadLimit(maxRequestPayloadBytes)
	return coderTencentWebSocketConnection{connection: connection}, nil
}

func invokeBaiduRTCAgentWebSocket(ctx context.Context, adapter *BaiduRESTAdapter, credentials BCECredentials, invocation Invocation) (result InvocationResult, err error) {
	if err = validateBaiduRTCAgentWebSocketInvocation(invocation); err != nil {
		return InvocationResult{}, err
	}
	plan, _ := parseBaiduRTCAgentPlan(invocation.Body)
	audioPlan, _ := buildBaiduRTCAudioPlan(plan, invocation)
	sink, sinkErr := newWebSocketOutputSink(invocation, adapter.config.MaxBodyBytes, "Baidu RTC AI Agent WebSocket")
	if sinkErr != nil {
		return InvocationResult{}, sinkErr
	}
	defer sink.abort()
	createRequest := map[string]any{"app_id": plan.AppID, "instance_type": plan.InstanceType}
	config := make(map[string]any, len(plan.Config)+1)
	for name, value := range plan.Config {
		config[name] = value
	}
	config["audiocodec"] = plan.AudioCodec
	configJSON, marshalErr := json.Marshal(config)
	if marshalErr != nil {
		return InvocationResult{}, fmt.Errorf("encode Baidu RTC AI Agent config")
	}
	createRequest["config"] = string(configJSON)
	createBytes, createRequestID, controlErr := adapter.baiduRTCControlRequest(ctx, credentials, "/api/v1/aiagent/generateAIAgentCall", createRequest)
	if controlErr != nil {
		return InvocationResult{}, controlErr
	}
	var created baiduRTCCreateResponse
	decoder := json.NewDecoder(bytes.NewReader(createBytes))
	decoder.UseNumber()
	decodeErr := decoder.Decode(&created)
	var trailing any
	trailingErr := decoder.Decode(&trailing)
	if decodeErr != nil || trailingErr != io.EOF || !baiduRTCInstanceIDPattern.MatchString(string(created.InstanceID)) {
		return InvocationResult{}, fmt.Errorf("Baidu RTC AI Agent create response is missing a valid instance ID")
	}
	stopBody := map[string]any{"app_id": plan.AppID, "ai_agent_instance_id": created.InstanceID}
	stopAttempted := false
	stopped := false
	defer func() {
		if stopped || stopAttempted {
			return
		}
		stopAttempted = true
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_, _, stopErr := adapter.baiduRTCControlRequest(cleanupCtx, credentials, "/api/v1/aiagent/stopAIAgentInstance", stopBody)
		if stopErr != nil {
			if err == nil {
				err = fmt.Errorf("stop Baidu RTC AI Agent instance: %w", stopErr)
			} else {
				err = fmt.Errorf("%w; cleanup stop failed: %v", err, stopErr)
			}
		}
	}()
	if !validBaiduRTCSecret(created.Context.Token) {
		return InvocationResult{}, fmt.Errorf("Baidu RTC AI Agent create response is missing a valid internal token")
	}

	webSocketURL, buildErr := baiduRTCInternalWebSocketURL(plan.AppID, string(created.InstanceID), created.Context.Token, audioPlan)
	if buildErr != nil {
		return InvocationResult{}, buildErr
	}
	dialCtx, cancelDial := context.WithTimeout(ctx, adapter.config.Timeout)
	rawConnection, dialErr := adapter.config.RTCWebSocketDial(dialCtx, webSocketURL)
	cancelDial()
	if dialErr != nil {
		return InvocationResult{}, dialErr
	}
	connection := &baiduRTCSerializedConnection{cloudWebSocketConnection: rawConnection}
	defer connection.Close()
	sessionCtx, cancelSession := context.WithTimeout(ctx, plan.Timeout)
	defer cancelSession()
	if err = activateBaiduRTCLicense(sessionCtx, connection, adapter.config.RTCLicenseKey, plan.DeviceID, plan.UserID); err != nil {
		return InvocationResult{}, err
	}
	secretValues := []string{credentials.AccessKeyID, credentials.SecretAccessKey, credentials.SessionToken, created.Context.Token, adapter.config.RTCLicenseKey}
	sendResult := make(chan error, 1)
	readResult := make(chan error, 1)
	go func() {
		sendResult <- sendBaiduRTCInputs(sessionCtx, adapter, connection, plan.Messages, plan.FinalMessages, invocation.BodyFile, audioPlan)
	}()
	go func() {
		readResult <- readBaiduRTCResponses(sessionCtx, connection, sink, plan, invocation.ImageFile, secretValues)
	}()
	var sendErr, readErr error
	var primaryErr error
	for sendResult != nil || readResult != nil {
		select {
		case sendErr = <-sendResult:
			sendResult = nil
			if sendErr != nil {
				if primaryErr == nil {
					primaryErr = sendErr
				}
				cancelSession()
			}
		case readErr = <-readResult:
			readResult = nil
			if readErr != nil {
				if primaryErr == nil {
					primaryErr = readErr
				}
				cancelSession()
			}
		}
	}
	if primaryErr != nil {
		return InvocationResult{}, primaryErr
	}
	stopAttempted = true
	if _, _, err = adapter.baiduRTCControlRequest(ctx, credentials, "/api/v1/aiagent/stopAIAgentInstance", stopBody); err != nil {
		return InvocationResult{}, fmt.Errorf("stop Baidu RTC AI Agent instance: %w", err)
	}
	stopped = true
	output, finishErr := sink.finish(createRequestID)
	if finishErr != nil {
		return InvocationResult{}, finishErr
	}
	return InvocationResult{Output: output, RequestID: createRequestID}, nil
}

func sendBaiduRTCInputs(ctx context.Context, adapter *BaiduRESTAdapter, connection cloudWebSocketConnection, messages, finalMessages []string, bodyFile string, audioPlan baiduRTCAudioPlan) error {
	writeMessages := func(values []string) error {
		for _, message := range values {
			if err := connection.Write(ctx, cloudWebSocketMessageText, []byte(message)); err != nil {
				return fmt.Errorf("write Baidu RTC AI Agent client command")
			}
		}
		return nil
	}
	if err := writeMessages(messages); err != nil {
		return err
	}
	if bodyFile != "" {
		if err := streamBaiduRTCAudio(ctx, adapter, connection, bodyFile, audioPlan); err != nil {
			return err
		}
	}
	if err := writeMessages(finalMessages); err != nil {
		return err
	}
	return nil
}

func readBaiduRTCResponses(ctx context.Context, connection cloudWebSocketConnection, sink *cloudWebSocketOutputSink, plan baiduRTCAgentPlan, imageFile string, secrets []string) error {
	type outputFrame struct {
		Type       string `json:"type"`
		Data       string `json:"data,omitempty"`
		DataBase64 string `json:"data_base64,omitempty"`
	}
	imageUploaded := false
	for count := 0; count < plan.MaxMessages; count++ {
		messageType, data, err := connection.Read(ctx)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("Baidu RTC AI Agent session timed out before %s", plan.TerminalEvent)
			}
			return fmt.Errorf("read Baidu RTC AI Agent response")
		}
		if len(data) == 0 || len(data) > maxRequestPayloadBytes || baiduRTCLeaksSecret(data, secrets) {
			return fmt.Errorf("Baidu RTC AI Agent returned an unsafe or oversized frame")
		}
		var output []byte
		terminal := false
		if messageType == cloudWebSocketMessageText {
			event := string(data)
			if !validBaiduRTCText(event) || strings.HasPrefix(event, "[E]:[LIC]:") {
				return fmt.Errorf("Baidu RTC AI Agent returned an invalid protocol event")
			}
			if event == "[E]:[UPLOAD_IMAGE]" {
				if imageFile == "" {
					return fmt.Errorf("Baidu RTC AI Agent requested an image without image_file")
				}
				if imageUploaded {
					return fmt.Errorf("Baidu RTC AI Agent requested image upload more than once")
				}
				if err := uploadBaiduRTCImage(ctx, connection, imageFile, plan.ImageMode); err != nil {
					return err
				}
				imageUploaded = true
			}
			output, _ = json.Marshal(outputFrame{Type: "text", Data: event})
			terminal = baiduRTCTerminal(plan.TerminalEvent, event, count+1, plan.MaxMessages)
		} else if messageType == cloudWebSocketMessageBinary {
			output, _ = json.Marshal(outputFrame{Type: "binary", DataBase64: base64.StdEncoding.EncodeToString(data)})
			terminal = plan.TerminalEvent == "message_limit" && count+1 == plan.MaxMessages
		} else {
			return fmt.Errorf("Baidu RTC AI Agent returned an unsupported frame type")
		}
		if err := sink.writeMessage(output); err != nil {
			return err
		}
		if terminal {
			if imageFile != "" && !imageUploaded {
				return fmt.Errorf("Baidu RTC AI Agent reached terminal event before image upload")
			}
			return nil
		}
	}
	return fmt.Errorf("Baidu RTC AI Agent response ended without the requested terminal event")
}

func (adapter *BaiduRESTAdapter) baiduRTCControlRequest(ctx context.Context, credentials BCECredentials, path string, body any) ([]byte, string, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, "", fmt.Errorf("encode Baidu RTC AI Agent control request")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baiduRTCControlEndpoint+path, bytes.NewReader(encoded))
	if err != nil {
		return nil, "", fmt.Errorf("build Baidu RTC AI Agent control request")
	}
	request.Header.Set("Content-Type", "application/json")
	if credentials.SessionToken != "" {
		request.Header.Set("x-bce-security-token", credentials.SessionToken)
	}
	authorization, err := signBCERequest(request, credentials, adapter.config.Now().UTC(), bceExpiry)
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("Authorization", authorization)
	response, err := adapter.config.HTTP.Do(request)
	if err != nil {
		return nil, "", fmt.Errorf("Baidu RTC AI Agent control request: %w", err)
	}
	defer response.Body.Close()
	limit := adapter.config.MaxBodyBytes
	if limit <= 0 || limit > maxRequestPayloadBytes {
		limit = maxRequestPayloadBytes
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, "", fmt.Errorf("read Baidu RTC AI Agent control response")
	}
	if int64(len(data)) > limit {
		return nil, "", fmt.Errorf("Baidu RTC AI Agent control response exceeds %d bytes", limit)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", fmt.Errorf("Baidu RTC AI Agent control request returned HTTP %d", response.StatusCode)
	}
	return data, responseRequestID(response.Header), nil
}

func baiduRTCInternalWebSocketURL(appID, instanceID, token string, audioPlan baiduRTCAudioPlan) (string, error) {
	if !identifierPattern.MatchString(appID) || !baiduRTCInstanceIDPattern.MatchString(instanceID) || !validBaiduRTCSecret(token) {
		return "", fmt.Errorf("Baidu RTC AI Agent returned invalid internal connection material")
	}
	switch audioPlan.Codec {
	case "raw", "raw16k", "pcma", "pcmu", "g722", "opus":
	default:
		return "", fmt.Errorf("Baidu RTC AI Agent returned invalid audio settings")
	}
	target, _ := url.Parse(baiduRTCWebSocketTarget)
	query := url.Values{}
	query.Set("a", appID)
	query.Set("id", instanceID)
	query.Set("t", token)
	query.Set("ac", audioPlan.Codec)
	if audioPlan.Codec == "opus" && audioPlan.MaxPacketBytes > 0 {
		query.Set("ptime", fmt.Sprintf("%d", audioPlan.Interval/time.Millisecond))
		query.Set("plen", fmt.Sprintf("%d", audioPlan.MaxPacketBytes))
	}
	target.RawQuery = query.Encode()
	return target.String(), nil
}

func validBaiduRTCSecret(value string) bool {
	return len(value) >= 8 && len(value) <= 4096 && validBaiduRTCText(value)
}

func activateBaiduRTCLicense(ctx context.Context, connection cloudWebSocketConnection, licenseKey, deviceID, userID string) error {
	messageType, data, err := connection.Read(ctx)
	if err != nil || messageType != cloudWebSocketMessageText || !strings.HasPrefix(string(data), "[E]:[LIC]:[MUST]:") {
		return fmt.Errorf("Baidu RTC AI Agent did not request the required license activation")
	}
	if !validBaiduRTCSecret(licenseKey) {
		return fmt.Errorf("Baidu RTC AI Agent license unavailable: set BCE_RTC_LICENSE_KEY on the MCP server")
	}
	payload, _ := json.Marshal(map[string]string{"devId": deviceID, "uId": userID, "licKey": licenseKey})
	activation := append([]byte("[E]:[LIC]:[ACTIVE]:"), payload...)
	if err := connection.Write(ctx, cloudWebSocketMessageText, activation); err != nil {
		return fmt.Errorf("write Baidu RTC AI Agent license activation")
	}
	messageType, data, err = connection.Read(ctx)
	if err != nil || messageType != cloudWebSocketMessageText || !strings.HasPrefix(string(data), "[E]:[LIC]:[RES]:[PASS]:") {
		return fmt.Errorf("Baidu RTC AI Agent license activation failed")
	}
	return nil
}

func uploadBaiduRTCImage(ctx context.Context, connection cloudWebSocketConnection, path, mode string) error {
	if mode != "" && mode != "image_generate" {
		return fmt.Errorf("Baidu RTC AI Agent image mode is invalid")
	}
	if err := validateBaiduRTCImageFile(path); err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open Baidu RTC image_file: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxRequestFileBytes {
		return fmt.Errorf("Baidu RTC image_file changed after validation")
	}
	writeFrames := func(writer cloudWebSocketConnection) error {
		buffer := make([]byte, baiduRTCImageChunkBytes)
		remaining := info.Size()
		first := true
		for remaining > 0 {
			chunkSize := baiduRTCImageChunkBytes
			if int64(chunkSize) > remaining {
				chunkSize = int(remaining)
			}
			read, readErr := io.ReadFull(file, buffer[:chunkSize])
			if readErr != nil || read != chunkSize {
				return fmt.Errorf("read Baidu RTC image_file chunk")
			}
			var header []byte
			if first {
				suffix := ""
				if mode != "" {
					suffix = ";[FT]=" + mode
				}
				header = []byte("\x18[T]=binary;[N]=" + filepath.Base(path) + suffix + "\n")
				first = false
			} else {
				header = []byte{0x10}
			}
			payload := make([]byte, len(header)+read)
			copy(payload, header)
			copy(payload[len(header):], buffer[:read])
			message := "[E]:[IMG]:" + base64.StdEncoding.EncodeToString(payload)
			if err := writer.Write(ctx, cloudWebSocketMessageText, []byte(message)); err != nil {
				return fmt.Errorf("write Baidu RTC image_file chunk")
			}
			remaining -= int64(read)
		}
		var trailing [1]byte
		if read, readErr := file.Read(trailing[:]); read != 0 || readErr != io.EOF {
			return fmt.Errorf("Baidu RTC image_file changed during upload")
		}
		end := "[E]:[IMG]:" + base64.StdEncoding.EncodeToString([]byte{0x14})
		if err := writer.Write(ctx, cloudWebSocketMessageText, []byte(end)); err != nil {
			return fmt.Errorf("write Baidu RTC image_file end marker")
		}
		return nil
	}
	if serialized, ok := connection.(baiduRTCExclusiveWriter); ok {
		return serialized.exclusiveWrite(writeFrames)
	}
	return writeFrames(connection)
}

func streamBaiduRTCAudio(ctx context.Context, adapter *BaiduRESTAdapter, connection cloudWebSocketConnection, path string, audioPlan baiduRTCAudioPlan) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open Baidu RTC %s audio: %w", audioPlan.Codec, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxRequestFileBytes || audioPlan.MaxPacketBytes < 1 {
		return fmt.Errorf("Baidu RTC %s body_file is invalid", audioPlan.Codec)
	}
	expectedBytes := int64(0)
	for _, packetLength := range audioPlan.PacketLengths {
		if packetLength < 1 || packetLength > audioPlan.MaxPacketBytes {
			return fmt.Errorf("Baidu RTC %s audio packet plan is invalid", audioPlan.Codec)
		}
		expectedBytes += int64(packetLength)
	}
	if len(audioPlan.PacketLengths) == 0 {
		if audioPlan.ChunkBytes < 1 || info.Size()%int64(audioPlan.ChunkBytes) != 0 {
			return fmt.Errorf("Baidu RTC %s body_file changed after validation", audioPlan.Codec)
		}
		expectedBytes = info.Size()
	}
	if expectedBytes != info.Size() {
		return fmt.Errorf("Baidu RTC %s body_file changed after validation", audioPlan.Codec)
	}
	buffer := make([]byte, audioPlan.MaxPacketBytes)
	writePacket := func(packetLength int) error {
		read, readErr := io.ReadFull(file, buffer[:packetLength])
		if readErr != nil || read != packetLength {
			return fmt.Errorf("read Baidu RTC %s audio packet", audioPlan.Codec)
		}
		if writeErr := connection.Write(ctx, cloudWebSocketMessageBinary, buffer[:read]); writeErr != nil {
			return fmt.Errorf("write Baidu RTC %s audio", audioPlan.Codec)
		}
		if pauseErr := adapter.config.StreamPause(ctx, audioPlan.Interval); pauseErr != nil {
			return fmt.Errorf("pace Baidu RTC %s audio: %w", audioPlan.Codec, pauseErr)
		}
		return nil
	}
	if len(audioPlan.PacketLengths) != 0 {
		for _, packetLength := range audioPlan.PacketLengths {
			if err := writePacket(packetLength); err != nil {
				return err
			}
		}
	} else {
		for remaining := info.Size(); remaining > 0; remaining -= int64(audioPlan.ChunkBytes) {
			if err := writePacket(audioPlan.ChunkBytes); err != nil {
				return err
			}
		}
	}
	var trailing [1]byte
	if read, trailingErr := file.Read(trailing[:]); read != 0 || trailingErr != io.EOF {
		return fmt.Errorf("Baidu RTC %s body_file changed during streaming", audioPlan.Codec)
	}
	return nil
}

func baiduRTCTerminal(terminalEvent, event string, count, maxMessages int) bool {
	switch terminalEvent {
	case "tts_end":
		return event == "[E]:[TTS_END_SPEAKING]"
	case "answer":
		return strings.HasPrefix(event, "[A]:") && len(event) > len("[A]:") && !strings.HasPrefix(event, "[A]:[")
	case "message_limit":
		return count == maxMessages
	default:
		return false
	}
}

func baiduRTCLeaksSecret(data []byte, secrets []string) bool {
	for _, secret := range secrets {
		if len(secret) >= 8 && bytes.Contains(data, []byte(secret)) {
			return true
		}
	}
	return false
}

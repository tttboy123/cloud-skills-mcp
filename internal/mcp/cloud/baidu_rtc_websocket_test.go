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
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type bceCredentialProviderFunc func(context.Context) (BCECredentials, error)

func (provider bceCredentialProviderFunc) Credentials(ctx context.Context) (BCECredentials, error) {
	return provider(ctx)
}

func TestBaiduRTCAgentWebSocketBoundaryKeepsCredentialsAndInstanceTokenInternal(t *testing.T) {
	root := t.TempDir()
	audioFile := filepath.Join(root, "audio.pcm")
	if err := os.WriteFile(audioFile, bytes.Repeat([]byte{1}, 1280), 0o600); err != nil {
		t.Fatal(err)
	}
	imageFile := filepath.Join(root, "camera.jpg")
	if err := os.WriteFile(imageFile, []byte("safe-image"), 0o600); err != nil {
		t.Fatal(err)
	}
	valid := func() Invocation {
		return Invocation{
			Provider: ProviderBaidu, Mode: ModeMutate, AuthScheme: "rtc-aiagent-ws",
			Service: "rtc-aiagent", Operation: "RealtimeInteraction", APIVersion: "1",
			Method: http.MethodGet, URL: "wss://rtc-aiotgw.exp.bcelive.com/v1/realtime",
			Body: map[string]any{
				"app_id": "rtc-app-1", "instance_type": "VoiceChat",
				"config":    map[string]any{"welcome": "hello"},
				"device_id": "device-1", "user_id": "user-1",
				"messages": []any{"[T]:hello"}, "max_messages": 8,
				"timeout_seconds": 30, "terminal_event": "tts_end",
			},
			BodyFile: audioFile, ImageFile: imageFile, ResponseFile: filepath.Join(root, "rtc.ndjson"),
			StreamChunkBytes: 640, StreamIntervalMS: 20,
		}
	}
	base := valid()
	if err := validateInvocation(base, []string{root}); err != nil {
		t.Fatal(err)
	}
	raw16kSixtyMS := valid()
	sixtyMSFile := filepath.Join(root, "raw16k-60ms.pcm")
	if err := os.WriteFile(sixtyMSFile, bytes.Repeat([]byte{2}, 1920*2), 0o600); err != nil {
		t.Fatal(err)
	}
	raw16kSixtyMS.BodyFile = sixtyMSFile
	raw16kSixtyMS.StreamChunkBytes = 1920
	raw16kSixtyMS.StreamIntervalMS = 60
	if err := validateInvocation(raw16kSixtyMS, []string{root}); err != nil {
		t.Fatalf("official 60-ms raw16k frames rejected: %v", err)
	}
	if classifyRead(ProviderBaidu, base) {
		t.Fatal("RTC AI Agent creates a billed instance and must never be read-only")
	}
	macIdentity := valid()
	macIdentity.Body.(map[string]any)["device_id"] = "00:11:22:33:44:55"
	if err := validateInvocation(macIdentity, []string{root}); err != nil {
		t.Fatalf("official MAC device identity rejected: %v", err)
	}

	for name, mutate := range map[string]func(*Invocation){
		"wrong method":      func(value *Invocation) { value.Method = http.MethodPost },
		"wrong service":     func(value *Invocation) { value.Service = "rtc" },
		"wrong operation":   func(value *Invocation) { value.Operation = "GenerateAIAgentCall" },
		"wrong api version": func(value *Invocation) { value.APIVersion = "2" },
		"wrong instance type": func(value *Invocation) {
			value.Body.(map[string]any)["instance_type"] = "Unknown"
		},
		"bce v2":         func(value *Invocation) { value.AuthVersion = "v2" },
		"wrong host":     func(value *Invocation) { value.URL = "wss://example.com/v1/realtime" },
		"wrong path":     func(value *Invocation) { value.URL = "wss://rtc-aiotgw.exp.bcelive.com/v2/realtime" },
		"caller token":   func(value *Invocation) { value.URL += "?t=caller-token" },
		"parameters":     func(value *Invocation) { value.Parameters = map[string]any{"ak": "caller"} },
		"headers":        func(value *Invocation) { value.Headers = map[string]string{"X-Test": "caller"} },
		"missing body":   func(value *Invocation) { value.Body = nil },
		"missing output": func(value *Invocation) { value.ResponseFile = "" },
		"missing input":  func(value *Invocation) { value.BodyFile = ""; value.Body.(map[string]any)["messages"] = []any{} },
		"wrong chunk":    func(value *Invocation) { value.StreamChunkBytes = 320 },
		"wrong interval": func(value *Invocation) { value.StreamIntervalMS = 10 },
		"partial raw16k frame": func(value *Invocation) {
			partial := filepath.Join(root, "partial.pcm")
			if err := os.WriteFile(partial, bytes.Repeat([]byte{1}, 639), 0o600); err != nil {
				t.Fatal(err)
			}
			value.BodyFile = partial
		},
		"audio exceeds timeout": func(value *Invocation) {
			tooLong := filepath.Join(root, "too-long.pcm")
			if err := os.WriteFile(tooLong, bytes.Repeat([]byte{1}, 640*50), 0o600); err != nil {
				t.Fatal(err)
			}
			value.BodyFile = tooLong
			value.Body.(map[string]any)["timeout_seconds"] = 1
		},
		"credential config": func(value *Invocation) { value.Body.(map[string]any)["config"] = map[string]any{"llm_token": "caller"} },
		"embedded credential": func(value *Invocation) {
			value.Body.(map[string]any)["config"] = map[string]any{"tts_url": `DEFAULT{"apikey":"caller-secret"}`}
		},
		"codec mismatch": func(value *Invocation) {
			value.Body.(map[string]any)["audio_codec"] = "raw16k"
			value.Body.(map[string]any)["config"] = map[string]any{"audiocodec": "opus"}
		},
		"unsupported codec": func(value *Invocation) { value.Body.(map[string]any)["audio_codec"] = "mp3" },
		"opus without packet lengths": func(value *Invocation) {
			value.Body.(map[string]any)["audio_codec"] = "opus"
		},
		"opus invalid packet time": func(value *Invocation) {
			value.Body.(map[string]any)["audio_codec"] = "opus"
			value.Body.(map[string]any)["opus_packet_time_ms"] = 30
			value.Body.(map[string]any)["opus_packet_lengths"] = []any{640, 640}
		},
		"opus lengths do not cover file": func(value *Invocation) {
			value.Body.(map[string]any)["audio_codec"] = "opus"
			value.Body.(map[string]any)["opus_packet_time_ms"] = 20
			value.Body.(map[string]any)["opus_packet_lengths"] = []any{1279}
			value.StreamChunkBytes = 0
		},
		"opus packet exceeds plen": func(value *Invocation) {
			value.Body.(map[string]any)["audio_codec"] = "opus"
			value.Body.(map[string]any)["opus_packet_time_ms"] = 20
			value.Body.(map[string]any)["opus_packet_lengths"] = []any{640, 640}
			value.Body.(map[string]any)["opus_packet_max_bytes"] = 639
			value.StreamChunkBytes = 0
		},
		"non opus packet lengths": func(value *Invocation) {
			value.Body.(map[string]any)["opus_packet_lengths"] = []any{640, 640}
		},
		"caller license":     func(value *Invocation) { value.Body.(map[string]any)["lic_key"] = "caller" },
		"caller access key":  func(value *Invocation) { value.Body.(map[string]any)["access_key_id"] = "caller" },
		"invalid message":    func(value *Invocation) { value.Body.(map[string]any)["messages"] = []any{"[E]:[LIC]:[ACTIVE]:caller"} },
		"unbounded messages": func(value *Invocation) { value.Body.(map[string]any)["max_messages"] = 257 },
		"too many client commands": func(value *Invocation) {
			commands := make([]any, 64)
			for index := range commands {
				commands[index] = "[B]"
			}
			value.Body.(map[string]any)["messages"] = commands
			value.Body.(map[string]any)["final_messages"] = []any{"[B]:[END]"}
		},
		"unbounded timeout": func(value *Invocation) { value.Body.(map[string]any)["timeout_seconds"] = 301 },
		"bad terminal":      func(value *Invocation) { value.Body.(map[string]any)["terminal_event"] = "forever" },
		"image outside root": func(value *Invocation) {
			outside := t.TempDir()
			value.ImageFile = filepath.Join(outside, "camera.jpg")
			if err := os.WriteFile(value.ImageFile, []byte("image"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"empty image": func(value *Invocation) {
			value.ImageFile = filepath.Join(root, "empty.jpg")
			if err := os.WriteFile(value.ImageFile, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"invalid image mode": func(value *Invocation) { value.Body.(map[string]any)["image_mode"] = "caller-defined" },
		"invalid function result": func(value *Invocation) {
			value.Body.(map[string]any)["function_results"] = map[string]any{"adjust_volume": map[string]any{"result": "maybe"}}
			value.Body.(map[string]any)["max_function_calls"] = 1
		},
		"caller function session": func(value *Invocation) {
			value.Body.(map[string]any)["function_results"] = map[string]any{"adjust_volume": map[string]any{"session_id": "caller", "result": "ok"}}
			value.Body.(map[string]any)["max_function_calls"] = 1
		},
		"duplicate post function type": func(value *Invocation) {
			value.Body.(map[string]any)["function_results"] = map[string]any{"birthday": map[string]any{
				"result": "ok", "post_function": []any{
					map[string]any{"type": "text", "content": "first"},
					map[string]any{"type": "text", "content": "second"},
				},
			}}
			value.Body.(map[string]any)["max_function_calls"] = 1
		},
		"text and prompt post functions": func(value *Invocation) {
			value.Body.(map[string]any)["function_results"] = map[string]any{"birthday": map[string]any{
				"result": "ok", "post_function": []any{
					map[string]any{"type": "text", "content": "fixed"},
					map[string]any{"type": "prompt", "query": "generate"},
				},
			}}
			value.Body.(map[string]any)["max_function_calls"] = 1
		},
		"function result credential material": func(value *Invocation) {
			value.Body.(map[string]any)["function_results"] = map[string]any{"fetch": map[string]any{"result": "ok", "message": "Authorization: Bearer caller-secret"}}
			value.Body.(map[string]any)["max_function_calls"] = 1
		},
		"function calls without results": func(value *Invocation) { value.Body.(map[string]any)["max_function_calls"] = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid()
			mutate(&candidate)
			if err := validateInvocation(candidate, []string{root}); err == nil {
				t.Fatal("unsafe Baidu RTC AI Agent invocation accepted")
			}
		})
	}
}

func TestBaiduRTCFunctionCallResponseCorrelatesProviderSession(t *testing.T) {
	plan, err := parseBaiduRTCAgentPlan(map[string]any{
		"app_id": "rtc-app-1", "device_id": "device-1", "user_id": "user-1", "messages": []any{"[T]:volume up"},
		"function_results": map[string]any{
			"adjust_volume": map[string]any{
				"result": "ok", "post_function": []any{
					map[string]any{"type": "text", "content": "volume adjusted"},
					map[string]any{"type": "play_music", "query": "play a confirmation sound", "enableMusicPadTts": false},
				},
			},
		},
		"max_function_calls": 2, "max_messages": 4, "timeout_seconds": 30, "terminal_event": "tts_end",
	})
	if err != nil {
		t.Fatal(err)
	}
	connection := &fakeTencentWebSocketConnection{}
	state := newBaiduRTCFunctionCallState(plan)
	event := `[F]:{"session_id":"1754555833139","content":"{\"function_name\":\"adjust_volume\",\"parameter_list\":[{\"mode\":\"up\"}]}"}`
	if err := respondBaiduRTCFunctionCall(t.Context(), connection, event, plan, state); err != nil {
		t.Fatal(err)
	}
	if len(connection.writes) != 1 || connection.writes[0].messageType != cloudWebSocketMessageText || !strings.HasPrefix(string(connection.writes[0].data), "[F]:") {
		t.Fatalf("writes=%#v", connection.writes)
	}
	var response struct {
		SessionID    string                       `json:"session_id"`
		Result       string                       `json:"result"`
		PostFunction []baiduRTCPostFunctionResult `json:"post_function"`
	}
	if json.Unmarshal(bytes.TrimPrefix(connection.writes[0].data, []byte("[F]:")), &response) != nil || response.SessionID != "1754555833139" || response.Result != "ok" || len(response.PostFunction) != 2 {
		t.Fatalf("response=%s", connection.writes[0].data)
	}
	if response.PostFunction[1].EnableMusicPadTTS == nil || *response.PostFunction[1].EnableMusicPadTTS {
		t.Fatalf("explicit false enableMusicPadTts was not preserved: %s", connection.writes[0].data)
	}
	if bytes.Contains(connection.writes[0].data, []byte("function_name")) || bytes.Contains(connection.writes[0].data, []byte("parameter_list")) {
		t.Fatalf("provider call payload reflected into response: %s", connection.writes[0].data)
	}
}

func TestBaiduRTCFunctionCallResponseRejectsUnsafeOrUncorrelatedEvents(t *testing.T) {
	plan, err := parseBaiduRTCAgentPlan(map[string]any{
		"app_id": "rtc-app-1", "device_id": "device-1", "user_id": "user-1", "messages": []any{"[T]:run"},
		"function_results":   map[string]any{"safe_function": map[string]any{"result": "error", "message": "not available"}},
		"max_function_calls": 1, "max_messages": 4, "timeout_seconds": 30, "terminal_event": "tts_end",
	})
	if err != nil {
		t.Fatal(err)
	}
	valid := `[F]:{"session_id":"session-1","content":"{\"function_name\":\"safe_function\",\"parameter_list\":[]}"}`
	for _, test := range []struct {
		name   string
		events []string
		want   string
	}{
		{name: "old prefix", events: []string{`[F]:[C]:{"fname":"safe_function"}`}, want: "invalid Function Call"},
		{name: "caller response", events: []string{`[F]:{"session_id":"session-1","result":"ok"}`}, want: "invalid Function Call"},
		{name: "unknown function", events: []string{`[F]:{"session_id":"session-1","content":"{\"function_name\":\"unknown\",\"parameter_list\":[]}"}`}, want: "no declared result"},
		{name: "credential parameter", events: []string{`[F]:{"session_id":"session-1","content":"{\"function_name\":\"safe_function\",\"parameter_list\":[{\"access_token\":\"caller\"}]}"}`}, want: "credential"},
		{name: "duplicate session", events: []string{valid, valid}, want: "duplicate session_id"},
		{name: "call limit", events: []string{valid, `[F]:{"session_id":"session-2","content":"{\"function_name\":\"safe_function\",\"parameter_list\":[]}"}`}, want: "max_function_calls"},
	} {
		t.Run(test.name, func(t *testing.T) {
			connection := &fakeTencentWebSocketConnection{}
			state := newBaiduRTCFunctionCallState(plan)
			var got error
			for _, event := range test.events {
				got = respondBaiduRTCFunctionCall(t.Context(), connection, event, plan, state)
				if got != nil {
					break
				}
			}
			if got == nil || !strings.Contains(got.Error(), test.want) {
				t.Fatalf("err=%v writes=%#v", got, connection.writes)
			}
		})
	}
}

func TestBaiduRTCFunctionCallRunsInsideSignedLifecycle(t *testing.T) {
	root := t.TempDir()
	responseFile := filepath.Join(root, "rtc.ndjson")
	controlCalls := 0
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		controlCalls++
		if strings.Contains(request.URL.Path, "generateAIAgentCall") {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Bce-Request-Id": []string{"create-request"}}, Body: io.NopCloser(strings.NewReader(`{"ai_agent_instance_id":555,"context":{"token":"private-function-token"}}`))}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})
	functionEvent := `[F]:{"session_id":"call-1","content":"{\"function_name\":\"adjust_volume\",\"parameter_list\":[{\"mode\":\"up\"}]}"}`
	connection := &fakeTencentWebSocketConnection{reads: [][]byte{
		[]byte("[E]:[LIC]:[MUST]:activate"), []byte("[E]:[LIC]:[RES]:[PASS]:ok"),
		[]byte(functionEvent), []byte("[E]:[TTS_END_SPEAKING]"),
	}}
	adapter := NewBaiduRESTAdapter(BaiduRESTConfig{
		Credentials: staticBCECredentials{credentials: BCECredentials{AccessKeyID: "ak", SecretAccessKey: "secret-key"}},
		HTTP:        doer, RTCLicenseKey: "license-key",
		RTCWebSocketDial: func(context.Context, string) (cloudWebSocketConnection, error) { return connection, nil },
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderBaidu, Mode: ModeMutate, AuthScheme: "rtc-aiagent-ws",
		Service: "rtc-aiagent", Operation: "RealtimeInteraction", APIVersion: "1",
		Method: http.MethodGet, URL: baiduRTCWebSocketTarget,
		Body: map[string]any{
			"app_id": "rtc-app-1", "device_id": "device-1", "user_id": "user-1", "messages": []any{},
			"function_results":   map[string]any{"adjust_volume": map[string]any{"result": "ok", "message": "done"}},
			"max_function_calls": 1, "max_messages": 2, "timeout_seconds": 2, "terminal_event": "tts_end",
		},
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err != nil || controlCalls != 2 || result.RequestID != "create-request" || len(connection.writes) != 2 {
		t.Fatalf("err=%v controls=%d result=%#v writes=%#v", err, controlCalls, result, connection.writes)
	}
	if got := string(connection.writes[1].data); got != `[F]:{"session_id":"call-1","result":"ok","message":"done"}` {
		t.Fatalf("correlated response=%s", got)
	}
	written, err := os.ReadFile(responseFile)
	if err != nil || !bytes.Contains(written, []byte(`\"session_id\":\"call-1\"`)) || !bytes.Contains(written, []byte("[E]:[TTS_END_SPEAKING]")) {
		t.Fatalf("err=%v output=%s", err, written)
	}
}

func TestBaiduRTCImageUploadUsesOfficialEventCorrelatedFrames(t *testing.T) {
	root := t.TempDir()
	imageFile := filepath.Join(root, "camera.jpg")
	image := bytes.Repeat([]byte{0x5a}, baiduRTCImageChunkBytes+7)
	if err := os.WriteFile(imageFile, image, 0o600); err != nil {
		t.Fatal(err)
	}
	connection := &fakeTencentWebSocketConnection{}
	if err := uploadBaiduRTCImage(t.Context(), connection, imageFile, "image_generate"); err != nil {
		t.Fatal(err)
	}
	if len(connection.writes) != 3 {
		t.Fatalf("writes=%#v", connection.writes)
	}
	decode := func(index int) []byte {
		t.Helper()
		message := string(connection.writes[index].data)
		if connection.writes[index].messageType != cloudWebSocketMessageText || !strings.HasPrefix(message, "[E]:[IMG]:") {
			t.Fatalf("frame %d=%#v", index, connection.writes[index])
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(message, "[E]:[IMG]:"))
		if err != nil {
			t.Fatal(err)
		}
		return decoded
	}
	first := decode(0)
	header := []byte("\x18[T]=binary;[N]=camera.jpg;[FT]=image_generate\n")
	if !bytes.Equal(first[:len(header)], header) || !bytes.Equal(first[len(header):], image[:baiduRTCImageChunkBytes]) {
		t.Fatalf("first frame does not match official protocol")
	}
	second := decode(1)
	if len(second) != 8 || second[0] != 0x10 || !bytes.Equal(second[1:], image[baiduRTCImageChunkBytes:]) {
		t.Fatalf("second frame=%x", second)
	}
	if end := decode(2); !bytes.Equal(end, []byte{0x14}) {
		t.Fatalf("end frame=%x", end)
	}
}

func TestBaiduRTCImageUploadRequiresAndConsumesOneProviderEvent(t *testing.T) {
	for _, test := range []struct {
		name      string
		imageFile bool
		reads     [][]byte
		wantError string
	}{
		{name: "uploads once", imageFile: true, reads: [][]byte{[]byte("[E]:[UPLOAD_IMAGE]"), []byte("[E]:[TTS_END_SPEAKING]")}},
		{name: "provider asks without file", reads: [][]byte{[]byte("[E]:[UPLOAD_IMAGE]")}, wantError: "without image_file"},
		{name: "configured file never requested", imageFile: true, reads: [][]byte{[]byte("[E]:[TTS_END_SPEAKING]")}, wantError: "before image upload"},
		{name: "provider asks twice", imageFile: true, reads: [][]byte{[]byte("[E]:[UPLOAD_IMAGE]"), []byte("[E]:[UPLOAD_IMAGE]")}, wantError: "more than once"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			responseFile := filepath.Join(root, "rtc.ndjson")
			imageFile := ""
			if test.imageFile {
				imageFile = filepath.Join(root, "camera.jpg")
				if err := os.WriteFile(imageFile, []byte("image"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			controlCalls := 0
			doer := doerFunc(func(request *http.Request) (*http.Response, error) {
				controlCalls++
				if strings.Contains(request.URL.Path, "generateAIAgentCall") {
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ai_agent_instance_id":444,"context":{"token":"private-image-token"}}`))}, nil
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
			})
			connection := &fakeTencentWebSocketConnection{reads: append([][]byte{[]byte("[E]:[LIC]:[MUST]:activate"), []byte("[E]:[LIC]:[RES]:[PASS]:ok")}, test.reads...)}
			messages := []any{}
			imageMode := "image_generate"
			if !test.imageFile {
				messages = []any{"[B]"}
				imageMode = ""
			}
			adapter := NewBaiduRESTAdapter(BaiduRESTConfig{
				Credentials: staticBCECredentials{credentials: BCECredentials{AccessKeyID: "ak", SecretAccessKey: "secret-key"}},
				HTTP:        doer, RTCLicenseKey: "license-key",
				RTCWebSocketDial: func(context.Context, string) (cloudWebSocketConnection, error) { return connection, nil },
			})
			_, err := adapter.Invoke(t.Context(), Invocation{
				Provider: ProviderBaidu, Mode: ModeMutate, AuthScheme: "rtc-aiagent-ws",
				Service: "rtc-aiagent", Operation: "RealtimeInteraction", APIVersion: "1",
				Method: http.MethodGet, URL: baiduRTCWebSocketTarget,
				Body: map[string]any{
					"app_id": "rtc-app-1", "device_id": "device-1", "user_id": "user-1",
					"messages": messages, "image_mode": imageMode, "max_messages": 4,
					"timeout_seconds": 2, "terminal_event": "tts_end",
				},
				ImageFile: imageFile, ResponseFile: responseFile, MaxResponseFileBytes: 4096,
			})
			if test.wantError == "" {
				if err != nil || controlCalls != 2 || len(connection.writes) != 3 {
					t.Fatalf("err=%v controls=%d writes=%#v", err, controlCalls, connection.writes)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) || controlCalls != 2 {
				t.Fatalf("err=%v controls=%d", err, controlCalls)
			}
			if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
				t.Fatalf("partial output published: %v", statErr)
			}
		})
	}
}

func TestBaiduRTCAgentRejectsInvalidPlanBeforeResolvingCredentials(t *testing.T) {
	credentialCalls := 0
	adapter := NewBaiduRESTAdapter(BaiduRESTConfig{
		Credentials: bceCredentialProviderFunc(func(context.Context) (BCECredentials, error) {
			credentialCalls++
			return BCECredentials{AccessKeyID: "must-not", SecretAccessKey: "be-used"}, nil
		}),
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderBaidu, Mode: ModeMutate, AuthScheme: "rtc-aiagent-ws",
		Service: "rtc-aiagent", Operation: "RealtimeInteraction", APIVersion: "1",
		Method: http.MethodGet, URL: "wss://rtc-aiotgw.exp.bcelive.com/v1/realtime?t=caller-token",
	})
	if err == nil || credentialCalls != 0 {
		t.Fatalf("err=%v credential calls=%d", err, credentialCalls)
	}
	_, err = adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderBaidu, Mode: ModeMutate, AuthScheme: "rtc-aiagent-ws",
		Service: "rtc-aiagent", Operation: "RealtimeInteraction", APIVersion: "1",
		Method: http.MethodGet, URL: baiduRTCWebSocketTarget, ResponseFile: filepath.Join(t.TempDir(), "rtc.ndjson"),
		Body: map[string]any{
			"app_id": "rtc-app-1", "device_id": "device-1", "user_id": "user-1", "messages": []any{"[T]:run"},
			"function_results":   map[string]any{"unsafe": map[string]any{"result": "ok", "message": "Authorization: Bearer caller-secret"}},
			"max_function_calls": 1, "max_messages": 2, "timeout_seconds": 2, "terminal_event": "tts_end",
		},
	})
	if err == nil || credentialCalls != 0 {
		t.Fatalf("unsafe Function Call plan reached credentials: err=%v calls=%d", err, credentialCalls)
	}
}

func TestBaiduRTCAgentAcceptsEveryOfficialFixedRateCodec(t *testing.T) {
	for codec, packetBytes := range map[string]int{
		"raw": 320, "raw16k": 640, "pcma": 160, "pcmu": 160, "g722": 160,
	} {
		t.Run(codec, func(t *testing.T) {
			root := t.TempDir()
			audioFile := filepath.Join(root, "audio."+codec)
			if err := os.WriteFile(audioFile, bytes.Repeat([]byte{1}, packetBytes*2), 0o600); err != nil {
				t.Fatal(err)
			}
			invocation := Invocation{
				Provider: ProviderBaidu, Mode: ModeMutate, AuthScheme: "rtc-aiagent-ws",
				Service: "rtc-aiagent", Operation: "RealtimeInteraction", APIVersion: "1",
				Method: http.MethodGet, URL: "wss://rtc-aiotgw.exp.bcelive.com/v1/realtime",
				Body: map[string]any{
					"app_id": "rtc-app-1", "audio_codec": codec,
					"device_id": "device-1", "user_id": "user-1", "messages": []any{},
					"max_messages": 2, "timeout_seconds": 2, "terminal_event": "tts_end",
				},
				BodyFile: audioFile, ResponseFile: filepath.Join(root, "rtc.ndjson"),
				StreamChunkBytes: packetBytes, StreamIntervalMS: 20,
			}
			if err := validateInvocation(invocation, []string{root}); err != nil {
				t.Fatalf("official %s codec rejected: %v", codec, err)
			}
		})
	}
}

func TestBaiduRTCAgentAcceptsOfficialStaticClientCommands(t *testing.T) {
	validMessages := []string{
		`[B]`, `[B]:[BEGIN]:3000`, `[B]:[END]`, `[T]:你是谁？`, `[TTS]:欢迎光临。`,
		`[SET]:[AUTO_INT]:[FALSE]`, `[SET]:[AUTO_INT]:[TRUE]`,
		`[SET]:[DEVICE_INFO]:{"os":"rtos","soc":"arm","model":"A","user_id":"1234567","asr_text_ext":true}`,
		`[SET]:[GIS]31.051335108535,121.51484487905`,
		`[E]:[CMD]:[REMOTE_PLAYER]:[STOP]`, `[E]:[CMD]:[REMOTE_PLAYER]:[PAUSE]`, `[E]:[CMD]:[REMOTE_PLAYER]:[RESUME]`,
		`[E]:[CMD]:[ASR_ENABLE_REALTIME]`, `[E]:[CMD]:[ASR_DISABLE_REALTIME]`, `[E]:[CMD]:[ASR_START_LONGTEXT_REC]`, `[E]:[CMD]:[ASR_STOP_LONGTEXT_REC]`,
		`[SET]:[UPDATE_SYSTEM_PROMPT]:{"model_type":"3","prompt":"严格使用黑白线条。"}`,
		`[SET]:[VARIABLES]:{"room":"卧室","device":"空调"}`,
		`[SET]:[TP_EXTRA_DATA]:{"tp_extra_data":{"functionCall":{"headerMap":{"h1":"v1"},"bodyMap":{"b1":"v3"}}}}`,
		`[OP]:[switchSceneRole]:{"tts":"DEFAULT{\"vcn\":\"1000000\"}"}`,
		`[OP]:[switchSceneRole]:{"scene_role":"英语口语老师"}`,
		`[OP]:[switchSceneRole]:{"scene_role_cfg":{"name":"小度熊","prompt":"你是简洁的聊天助手。"}}`,
		`[SET]:[ENHANCE_QUERY]:{"enhance_type":"3","pre_query":"我现在在故宫","post_query":"用5个字回答问题"}`,
		`[E]:[CMD]:[MCP_TOOLS_CHANGED]`,
		`[E]:[DC]:{"intent":"music","parameter_list":[{"url":"https://media.example.com/test.mp3"},{"text":"播放音乐"}]}`,
		`[E]:[DC]:{"intent":"music","parameter_list":[{"track_id":"69104227419"},{"text":"播放音乐"}]}`,
		`[E]:[CMD]:[MEETING_SUMMARY_CREATE]:`,
		`[E]:[CMD]:[MEETING_SUMMARY_CREATE]:meeting_001|single|true|basicInfo,fullSummary,todoList`,
		`[E]:[CMD]:[MEETING_SUMMARY_FINISH]`,
	}
	for _, message := range validMessages {
		t.Run(message, func(t *testing.T) {
			root := t.TempDir()
			invocation := Invocation{
				Provider: ProviderBaidu, Mode: ModeMutate, AuthScheme: "rtc-aiagent-ws",
				Service: "rtc-aiagent", Operation: "RealtimeInteraction", APIVersion: "1",
				Method: http.MethodGet, URL: "wss://rtc-aiotgw.exp.bcelive.com/v1/realtime",
				Body: map[string]any{
					"app_id": "rtc-app-1", "device_id": "device-1", "user_id": "user-1",
					"messages": []any{message}, "max_messages": 2, "timeout_seconds": 30, "terminal_event": "answer",
				},
				ResponseFile: filepath.Join(root, "rtc.ndjson"),
			}
			if err := validateInvocation(invocation, []string{root}); err != nil {
				t.Fatalf("official client command rejected: %v", err)
			}
		})
	}
}

func TestBaiduRTCAgentRejectsUnsafeOrMalformedClientCommands(t *testing.T) {
	for _, message := range []string{
		`[E]:[LIC]:[ACTIVE]:{"devId":"caller","licKey":"caller-license"}`,
		`[E]:[UPLOAD_IMAGE]`, `[E]:[REMOTE_PLAYER_BEGIN]`, `[UNKNOWN]:value`,
		`[F]:{"session_id":"1","result":"maybe"}`,
		`[F]:{"session_id":"1","result":"ok","post_function":[{"type":"text","content":"a"},{"type":"text","content":"b"}]}`,
		`[B]:[BEGIN]:0`, `[B]:[BEGIN]:300001`, `[B]:[END]:extra`,
		`[SET]:[DEVICE_INFO]:{"os":"rtos","api_key":"secret"}`,
		`[SET]:[GIS]91,181`,
		`[SET]:[UPDATE_SYSTEM_PROMPT]:{"model_type":"1","prompt":"bad"}`,
		`[SET]:[VARIABLES]:{"access_token":"secret"}`,
		`[OP]:[switchSceneRole]:{"tts":"DEFAULT{\"apikey\":\"secret\"}"}`,
		`[SET]:[ENHANCE_QUERY]:{"enhance_type":"9","pre_query":"bad"}`,
		`[E]:[DC]:{"intent":"music","parameter_list":[{"url":"http://127.0.0.1/a.mp3"}]}`,
		`[E]:[DC]:{"intent":"music","parameter_list":[{"url":"https://127.0.0.1/a.mp3"}]}`,
		`[E]:[DC]:{"intent":"music","parameter_list":[{"url":"https://user:pass@media.example.com/a.mp3"}]}`,
		`[E]:[DC]:{"intent":"music","parameter_list":[{"url":"https://media.example.com/a.mp3?access_token=secret"}]}`,
		`[E]:[CMD]:[MEETING_SUMMARY_CREATE]:bad|too|many|fields|here`,
	} {
		t.Run(message, func(t *testing.T) {
			_, err := parseBaiduRTCAgentPlan(map[string]any{
				"app_id": "rtc-app-1", "device_id": "device-1", "user_id": "user-1",
				"messages": []any{message}, "max_messages": 2, "timeout_seconds": 30, "terminal_event": "answer",
			})
			if err == nil {
				t.Fatal("unsafe client command accepted")
			}
		})
	}
}

func TestBaiduRTCAgentWebSocketSignsLifecycleAndPublishesSanitizedNDJSON(t *testing.T) {
	root := t.TempDir()
	audioFile := filepath.Join(root, "audio.pcmu")
	responseFile := filepath.Join(root, "rtc.ndjson")
	if err := os.WriteFile(audioFile, bytes.Repeat([]byte{7}, 320), 0o600); err != nil {
		t.Fatal(err)
	}
	credentials := BCECredentials{AccessKeyID: "bce-ak", SecretAccessKey: "bce-secret", SessionToken: "bce-session"}
	instanceToken := "private-instance-token"
	licenseKey := "private-license-key"
	controlCalls := 0
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		controlCalls++
		if request.URL.Scheme != "https" || request.URL.Host != "rtc-aiagent.baidubce.com" || request.Method != http.MethodPost {
			return nil, fmt.Errorf("unexpected control request %s %s", request.Method, request.URL)
		}
		if !strings.HasPrefix(request.Header.Get("Authorization"), "bce-auth-v1/bce-ak/") || request.Header.Get("x-bce-security-token") != credentials.SessionToken {
			return nil, fmt.Errorf("unsigned control request")
		}
		data, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		switch request.URL.Path {
		case "/api/v1/aiagent/generateAIAgentCall":
			var body map[string]any
			if err := json.Unmarshal(data, &body); err != nil || body["app_id"] != "rtc-app-1" || body["instance_type"] != "VoiceChat" {
				return nil, fmt.Errorf("unexpected create body %s", data)
			}
			config, ok := body["config"].(string)
			if !ok || !strings.Contains(config, `"welcome":"hello"`) || !strings.Contains(config, `"audiocodec":"pcmu"`) || strings.Contains(config, `"rtc_ac"`) {
				return nil, fmt.Errorf("config was not serialized as a JSON string: %s", data)
			}
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Bce-Request-Id": []string{"create-request"}}, Body: io.NopCloser(strings.NewReader(`{"ai_agent_instance_id":222,"instance_type":"VoiceChat","context":{"cid":1,"token":"` + instanceToken + `"}}`))}, nil
		case "/api/v1/aiagent/stopAIAgentInstance":
			if string(data) != `{"ai_agent_instance_id":222,"app_id":"rtc-app-1"}` && string(data) != `{"app_id":"rtc-app-1","ai_agent_instance_id":222}` {
				return nil, fmt.Errorf("unexpected stop body %s", data)
			}
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Bce-Request-Id": []string{"stop-request"}}, Body: io.NopCloser(strings.NewReader(`{"success":true}`))}, nil
		default:
			return nil, fmt.Errorf("unexpected control path %s", request.URL.Path)
		}
	})
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			[]byte(`[E]:[LIC]:[MUST]:activate`),
			[]byte(`[E]:[LIC]:[RES]:[PASS]:ok`),
			[]byte(`[A]:hello`),
			[]byte("provider-audio"),
			[]byte(`[E]:[TTS_END_SPEAKING]`),
		},
		readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageText, cloudWebSocketMessageText, cloudWebSocketMessageText, cloudWebSocketMessageBinary, cloudWebSocketMessageText},
	}
	adapter := NewBaiduRESTAdapter(BaiduRESTConfig{
		Credentials: staticBCECredentials{credentials: credentials}, HTTP: doer,
		Now:           func() time.Time { return time.Unix(1735689600, 0).UTC() },
		RTCLicenseKey: licenseKey,
		RTCWebSocketDial: func(_ context.Context, target string) (cloudWebSocketConnection, error) {
			parsed, err := url.Parse(target)
			if err != nil {
				return nil, err
			}
			query := parsed.Query()
			if parsed.Scheme != "wss" || parsed.Host != "rtc-aiotgw.exp.bcelive.com" || parsed.Path != "/v1/realtime" || query.Get("a") != "rtc-app-1" || query.Get("id") != "222" || query.Get("t") != instanceToken || query.Get("ac") != "pcmu" || query.Get("ptime") != "" || query.Get("plen") != "" || query.Get("ak") != "" || query.Get("sk") != "" {
				return nil, fmt.Errorf("unsafe RTC target %s", target)
			}
			return connection, nil
		},
		StreamPause: func(context.Context, time.Duration) error { return nil },
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderBaidu, Mode: ModeMutate, AuthScheme: "rtc-aiagent-ws",
		Service: "rtc-aiagent", Operation: "RealtimeInteraction", APIVersion: "1",
		Method: http.MethodGet, URL: "wss://rtc-aiotgw.exp.bcelive.com/v1/realtime",
		Body: map[string]any{
			"app_id": "rtc-app-1", "instance_type": "VoiceChat", "config": map[string]any{"welcome": "hello"},
			"audio_codec": "pcmu",
			"device_id":   "device-1", "user_id": "user-1",
			"messages":       []any{"[SET]:[AUTO_INT]:[FALSE]", "[T]:hello"},
			"final_messages": []any{"[E]:[CMD]:[REMOTE_PLAYER]:[STOP]"},
			"max_messages":   8, "timeout_seconds": 30, "terminal_event": "tts_end",
		},
		BodyFile: audioFile, ResponseFile: responseFile, StreamChunkBytes: 160, StreamIntervalMS: 20, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if controlCalls != 2 || result.RequestID != "create-request" {
		t.Fatalf("control calls=%d result=%#v", controlCalls, result)
	}
	if len(connection.writes) != 6 {
		t.Fatalf("writes=%#v", connection.writes)
	}
	if connection.writes[0].messageType != cloudWebSocketMessageText || !bytes.Contains(connection.writes[0].data, []byte(`"licKey":"`+licenseKey+`"`)) || !bytes.Contains(connection.writes[0].data, []byte(`"devId":"device-1"`)) {
		t.Fatalf("license activation=%s", connection.writes[0].data)
	}
	if connection.writes[1].messageType != cloudWebSocketMessageText || string(connection.writes[1].data) != "[SET]:[AUTO_INT]:[FALSE]" || string(connection.writes[2].data) != "[T]:hello" || len(connection.writes[3].data) != 160 || len(connection.writes[4].data) != 160 || string(connection.writes[5].data) != "[E]:[CMD]:[REMOTE_PLAYER]:[STOP]" {
		t.Fatalf("protocol writes=%#v", connection.writes)
	}
	written, err := os.ReadFile(responseFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{credentials.AccessKeyID, credentials.SecretAccessKey, credentials.SessionToken, instanceToken, licenseKey} {
		if bytes.Contains(written, []byte(secret)) || bytes.Contains(result.Output, []byte(secret)) {
			t.Fatalf("secret %q leaked: file=%s result=%s", secret, written, result.Output)
		}
	}
	if !bytes.Contains(written, []byte(`{"type":"text","data":"[A]:hello"}`)) || !bytes.Contains(written, []byte(`"data_base64":"`+base64.StdEncoding.EncodeToString([]byte("provider-audio"))+`"`)) || !bytes.Contains(written, []byte(`[E]:[TTS_END_SPEAKING]`)) {
		t.Fatalf("unexpected NDJSON %s", written)
	}
}

func TestBaiduRTCAgentOpusUsesOfficialQueryAndVariablePacketBoundaries(t *testing.T) {
	root := t.TempDir()
	audioFile := filepath.Join(root, "audio.opus-packets")
	responseFile := filepath.Join(root, "rtc.ndjson")
	packetLengths := []int{17, 29, 23}
	if err := os.WriteFile(audioFile, bytes.Repeat([]byte{9}, 69), 0o600); err != nil {
		t.Fatal(err)
	}
	controlCalls := 0
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		controlCalls++
		data, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		if strings.Contains(request.URL.Path, "generateAIAgentCall") {
			var body map[string]any
			if json.Unmarshal(data, &body) != nil {
				return nil, fmt.Errorf("invalid create body")
			}
			config, _ := body["config"].(string)
			if !strings.Contains(config, `"audiocodec":"opus"`) {
				return nil, fmt.Errorf("missing official opus config: %s", data)
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ai_agent_instance_id":333,"context":{"token":"private-opus-token"}}`))}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			[]byte(`[E]:[LIC]:[MUST]:activate`),
			[]byte(`[E]:[LIC]:[RES]:[PASS]:ok`),
			[]byte(`[E]:[TTS_END_SPEAKING]`),
		},
	}
	var pauses []time.Duration
	adapter := NewBaiduRESTAdapter(BaiduRESTConfig{
		Credentials: staticBCECredentials{credentials: BCECredentials{AccessKeyID: "ak", SecretAccessKey: "secret-key"}},
		HTTP:        doer, RTCLicenseKey: "license-key",
		RTCWebSocketDial: func(_ context.Context, target string) (cloudWebSocketConnection, error) {
			parsed, err := url.Parse(target)
			if err != nil {
				return nil, err
			}
			query := parsed.Query()
			if query.Get("ac") != "opus" || query.Get("ptime") != "40" || query.Get("plen") != "29" || query.Get("t") != "private-opus-token" {
				return nil, fmt.Errorf("unexpected opus target %s", target)
			}
			return connection, nil
		},
		StreamPause: func(_ context.Context, duration time.Duration) error {
			pauses = append(pauses, duration)
			return nil
		},
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderBaidu, Mode: ModeMutate, AuthScheme: "rtc-aiagent-ws",
		Service: "rtc-aiagent", Operation: "RealtimeInteraction", APIVersion: "1",
		Method: http.MethodGet, URL: "wss://rtc-aiotgw.exp.bcelive.com/v1/realtime",
		Body: map[string]any{
			"app_id": "rtc-app-1", "audio_codec": "opus", "opus_packet_time_ms": 40,
			"opus_packet_lengths": []any{17, 29, 23}, "device_id": "device-1", "user_id": "user-1",
			"messages": []any{}, "max_messages": 2, "timeout_seconds": 2, "terminal_event": "tts_end",
		},
		BodyFile: audioFile, ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if controlCalls != 2 || len(connection.writes) != 1+len(packetLengths) {
		t.Fatalf("control calls=%d writes=%#v", controlCalls, connection.writes)
	}
	for index, length := range packetLengths {
		if got := len(connection.writes[index+1].data); got != length {
			t.Fatalf("packet %d length=%d want=%d", index, got, length)
		}
	}
	if len(pauses) != len(packetLengths) {
		t.Fatalf("pauses=%v", pauses)
	}
	for _, duration := range pauses {
		if duration != 40*time.Millisecond {
			t.Fatalf("unexpected opus pacing %v", pauses)
		}
	}
}

func TestBaiduRTCAgentWebSocketStopsAndNeverPublishesPartialOutputOnFailure(t *testing.T) {
	for _, failure := range []string{"license missing", "license failed", "stop failed"} {
		t.Run(failure, func(t *testing.T) {
			root := t.TempDir()
			responseFile := filepath.Join(root, "rtc.ndjson")
			stops := 0
			doer := doerFunc(func(request *http.Request) (*http.Response, error) {
				if strings.Contains(request.URL.Path, "stopAIAgentInstance") {
					stops++
					if failure == "stop failed" {
						return &http.Response{StatusCode: http.StatusInternalServerError, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":"stop"}`))}, nil
					}
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Bce-Request-Id": []string{"create-request"}}, Body: io.NopCloser(strings.NewReader(`{"ai_agent_instance_id":222,"context":{"token":"instance-token"}}`))}, nil
			})
			reads := [][]byte{[]byte(`[E]:[LIC]:[MUST]:activate`)}
			if failure == "license failed" || failure == "stop failed" {
				reads = append(reads, []byte(`[E]:[LIC]:[RES]:[FAILED]:denied`))
			}
			if failure == "stop failed" {
				reads[1] = []byte(`[E]:[LIC]:[RES]:[PASS]:ok`)
				reads = append(reads, []byte(`[E]:[TTS_END_SPEAKING]`))
			}
			license := "license"
			if failure == "license missing" {
				license = ""
			}
			adapter := NewBaiduRESTAdapter(BaiduRESTConfig{
				Credentials: staticBCECredentials{credentials: BCECredentials{AccessKeyID: "ak", SecretAccessKey: "sk"}},
				HTTP:        doer, RTCLicenseKey: license,
				RTCWebSocketDial: func(context.Context, string) (cloudWebSocketConnection, error) {
					return &fakeTencentWebSocketConnection{reads: reads}, nil
				},
			})
			_, err := adapter.Invoke(t.Context(), Invocation{
				Provider: ProviderBaidu, Mode: ModeMutate, AuthScheme: "rtc-aiagent-ws",
				Service: "rtc-aiagent", Operation: "RealtimeInteraction", APIVersion: "1",
				Method: http.MethodGet, URL: "wss://rtc-aiotgw.exp.bcelive.com/v1/realtime",
				Body:         map[string]any{"app_id": "rtc-app-1", "device_id": "device-1", "user_id": "user-1", "messages": []any{"[T]:hello"}, "max_messages": 2, "timeout_seconds": 2, "terminal_event": "tts_end"},
				ResponseFile: responseFile, MaxResponseFileBytes: 4096,
			})
			if err == nil || stops != 1 {
				t.Fatalf("err=%v stops=%d", err, stops)
			}
			if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
				t.Fatalf("partial response was published: %v", statErr)
			}
		})
	}
}

func TestBaiduRTCAgentWebSocketMissingInternalTokenStopsBeforeDial(t *testing.T) {
	root := t.TempDir()
	responseFile := filepath.Join(root, "rtc.ndjson")
	stops := 0
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "stopAIAgentInstance") {
			stops++
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ai_agent_instance_id":222,"context":{}}`))}, nil
	})
	adapter := NewBaiduRESTAdapter(BaiduRESTConfig{
		Credentials: staticBCECredentials{credentials: BCECredentials{AccessKeyID: "ak", SecretAccessKey: "sk"}}, HTTP: doer,
		RTCLicenseKey: "license-key",
		RTCWebSocketDial: func(context.Context, string) (cloudWebSocketConnection, error) {
			t.Fatal("WSS dial must not occur without the internal instance token")
			return nil, nil
		},
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderBaidu, Mode: ModeMutate, AuthScheme: "rtc-aiagent-ws",
		Service: "rtc-aiagent", Operation: "RealtimeInteraction", APIVersion: "1",
		Method: http.MethodGet, URL: "wss://rtc-aiotgw.exp.bcelive.com/v1/realtime",
		Body:         map[string]any{"app_id": "rtc-app-1", "device_id": "device-1", "user_id": "user-1", "messages": []any{"[T]:hello"}, "max_messages": 2, "timeout_seconds": 2, "terminal_event": "answer"},
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err == nil || stops != 1 {
		t.Fatalf("err=%v stops=%d", err, stops)
	}
	if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
		t.Fatalf("partial response was published: %v", statErr)
	}
}

func TestBaiduRTCAgentTerminalModesRejectIntermediateAnswerEvents(t *testing.T) {
	for _, test := range []struct {
		mode, event string
		count, max  int
		want        bool
	}{
		{"tts_end", "[E]:[TTS_END_SPEAKING]", 1, 8, true},
		{"tts_end", "[E]:[TTS_END_SPEAKING]:extra", 1, 8, false},
		{"answer", "[A]:final answer", 1, 8, true},
		{"answer", "[A]:[M]:partial", 1, 8, false},
		{"answer", "[A]:[H]:hint", 1, 8, false},
		{"message_limit", "binary", 8, 8, true},
		{"message_limit", "binary", 7, 8, false},
	} {
		if got := baiduRTCTerminal(test.mode, test.event, test.count, test.max); got != test.want {
			t.Fatalf("mode=%s event=%q got=%t want=%t", test.mode, test.event, got, test.want)
		}
	}
}

func FuzzBaiduRTCAgentPlanRejectsCredentialFields(f *testing.F) {
	f.Add(`{"app_id":"rtc-app-1","messages":["[T]:hello"],"max_messages":2,"timeout_seconds":2,"terminal_event":"answer"}`)
	f.Add(`{"app_id":"rtc-app-1","audio_codec":"opus","opus_packet_time_ms":40,"opus_packet_lengths":[17,29],"device_id":"dev","user_id":"user","messages":[],"max_messages":2,"timeout_seconds":2,"terminal_event":"answer"}`)
	f.Add(`{"app_id":"rtc-app-1","config":{"api_key":"secret"},"messages":["[T]:hello"],"max_messages":2,"timeout_seconds":2}`)
	f.Add(`{"app_id":"rtc-app-1","config":{"tts_url":"{\"apikey\":\"secret\"}"},"device_id":"dev","user_id":"user","messages":["[T]:hello"],"max_messages":2,"timeout_seconds":2,"terminal_event":"answer"}`)
	f.Fuzz(func(t *testing.T, raw string) {
		var body any
		if json.Unmarshal([]byte(raw), &body) != nil {
			return
		}
		plan, err := parseBaiduRTCAgentPlan(body)
		if err == nil {
			validCodec := plan.AudioCodec == "raw" || plan.AudioCodec == "raw16k" || plan.AudioCodec == "pcma" || plan.AudioCodec == "pcmu" || plan.AudioCodec == "g722" || plan.AudioCodec == "opus"
			validOpus := plan.AudioCodec != "opus" && plan.OpusPacketTimeMS == 0 && len(plan.OpusPacketLengths) == 0 && plan.OpusPacketMaxBytes == 0
			if plan.AudioCodec == "opus" {
				validOpus = plan.OpusPacketTimeMS == 20 || plan.OpusPacketTimeMS == 40 || plan.OpusPacketTimeMS == 60
			}
			if plan.AppID == "" || !validCodec || !validOpus || len(plan.Messages)+len(plan.FinalMessages) > 64 || plan.MaxMessages < 1 || plan.MaxMessages > 256 || plan.Timeout < time.Second || plan.Timeout > 300*time.Second || baiduRTCAgentContainsCredentialField(body) || baiduRTCConfigContainsCredentialMaterial(plan.Config) {
				t.Fatal("unsafe plan accepted")
			}
		}
	})
}

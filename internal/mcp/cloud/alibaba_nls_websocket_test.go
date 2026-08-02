package cloud

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAlibabaNLSWebSocketBoundaryKeepsTokenAndProtocolControlsInternal(t *testing.T) {
	directory := t.TempDir()
	audioFile := filepath.Join(directory, "audio.pcm")
	if err := os.WriteFile(audioFile, []byte("abcdefgh"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := Invocation{
		Provider: ProviderAlicloud, Mode: ModeRead, AuthScheme: "nls-ws", Service: "nls", Operation: "SpeechTranscriber",
		Method: http.MethodGet, URL: "wss://nls-gateway-ap-southeast-1.aliyuncs.com/ws/v1",
		Parameters: map[string]any{"appkey": "project-appkey"},
		Body:       map[string]any{"format": "pcm", "sample_rate": 16000}, BodyFile: audioFile,
		ResponseFile: filepath.Join(directory, "transcript.ndjson"), StreamChunkBytes: 4, StreamIntervalMS: 10,
	}
	if err := validateInvocation(base, []string{directory}); err != nil {
		t.Fatal(err)
	}
	if !classifyRead(ProviderAlicloud, base) {
		t.Fatal("Alibaba NLS speech transcription was not classified read-only")
	}
	tts := base
	tts.Operation = "FlowingSpeechSynthesizer"
	tts.BodyFile = ""
	tts.Body = map[string]any{
		"start": map[string]any{"voice": "xiaoyun", "format": "mp3", "sample_rate": 16000},
		"texts": []any{"hello", " world"},
	}
	tts.ResponseFile = filepath.Join(directory, "speech.mp3")
	tts.StreamChunkBytes = 0
	if err := validateInvocation(tts, []string{directory}); err != nil {
		t.Fatal(err)
	}
	if !classifyRead(ProviderAlicloud, tts) {
		t.Fatal("Alibaba NLS speech synthesis was not classified read-only")
	}

	for name, mutate := range map[string]func(*Invocation){
		"wrong method":    func(value *Invocation) { value.Method = http.MethodPost },
		"wrong service":   func(value *Invocation) { value.Service = "isi" },
		"wrong operation": func(value *Invocation) { value.Operation = "CreateToken" },
		"wrong host":      func(value *Invocation) { value.URL = "wss://example.com/ws/v1" },
		"wrong path":      func(value *Invocation) { value.URL = "wss://nls-gateway-ap-southeast-1.aliyuncs.com/other" },
		"caller token":    func(value *Invocation) { value.URL += "?token=forbidden" },
		"auth header":     func(value *Invocation) { value.Headers = map[string]string{"X-NLS-Token": "forbidden"} },
		"missing appkey":  func(value *Invocation) { value.Parameters = nil },
		"extra parameter": func(value *Invocation) {
			value.Parameters = map[string]any{"appkey": "project-appkey", "token": "forbidden"}
		},
		"credential payload": func(value *Invocation) { value.Body = map[string]any{"access_token": "forbidden"} },
		"missing audio":      func(value *Invocation) { value.BodyFile = "" },
		"missing response":   func(value *Invocation) { value.ResponseFile = "" },
		"cross-provider data": func(value *Invocation) {
			value.Region = "ap-southeast-1"
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := base
			candidate.Parameters = map[string]any{"appkey": "project-appkey"}
			candidate.Headers = nil
			candidate.Body = map[string]any{"format": "pcm", "sample_rate": 16000}
			mutate(&candidate)
			if err := validateInvocation(candidate, []string{directory}); err == nil {
				t.Fatal("invalid Alibaba NLS invocation accepted")
			}
		})
	}
}

func TestAlibabaNLSWebSocketMintsCachesAndContainsTokenWhileStreamingASR(t *testing.T) {
	directory := t.TempDir()
	audioFile := filepath.Join(directory, "audio.pcm")
	responseFile := filepath.Join(directory, "transcript.ndjson")
	if err := os.WriteFile(audioFile, []byte("abcdefgh"), 0o600); err != nil {
		t.Fatal(err)
	}
	connection := &fakeTencentWebSocketConnection{reads: [][]byte{
		[]byte(`{"header":{"message_id":"provider-1","task_id":"11111111111111111111111111111111","namespace":"SpeechTranscriber","name":"TranscriptionStarted","status":20000000}}`),
		[]byte(`{"header":{"message_id":"provider-2","task_id":"11111111111111111111111111111111","namespace":"SpeechTranscriber","name":"SentenceEnd","status":20000000},"payload":{"result":"hello"}}`),
		[]byte(`{"header":{"message_id":"provider-3","task_id":"11111111111111111111111111111111","namespace":"SpeechTranscriber","name":"TranscriptionCompleted","status":20000000}}`),
	}}
	tokenCalls := 0
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		tokenCalls++
		query := request.URL.Query()
		if request.Method != http.MethodPost || request.URL.Scheme != "https" || request.URL.Host != "nlsmeta.ap-southeast-1.aliyuncs.com" || request.URL.Path != "/" {
			return nil, fmt.Errorf("unexpected token request %s %s", request.Method, request.URL)
		}
		if query.Get("Action") != "CreateToken" || query.Get("Version") != "2019-07-17" || query.Get("AccessKeyId") != "AKIDEXAMPLE" || query.Get("SecurityToken") != "ram-session" || query.Get("Signature") == "" {
			return nil, fmt.Errorf("unsigned token query %#v", query)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"RequestId":"token-request","Token":{"Id":"nls-private-token","ExpireTime":4102444800}}`))}, nil
	})
	ids := []string{"11111111111111111111111111111111", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{
		Credentials: staticAlibabaCredentialsProvider{credentials: AlibabaCredentials{AccessKeyID: "AKIDEXAMPLE", AccessKeySecret: "testsecret", SecurityToken: "ram-session"}},
		HTTP:        doer, Now: func() time.Time { return time.Unix(1735689600, 0).UTC() }, Nonce: func() string { return "rpc-signature-nonce" },
		NLSID: func() string {
			value := ids[0]
			ids = ids[1:]
			return value
		},
		NLSWebSocketDial: func(_ context.Context, target string) (cloudWebSocketConnection, error) {
			parsed, err := url.Parse(target)
			if err != nil {
				return nil, err
			}
			if parsed.Query().Get("token") != "nls-private-token" || parsed.Query().Get("appkey") != "" {
				return nil, fmt.Errorf("unexpected NLS handshake query %#v", parsed.Query())
			}
			return connection, nil
		},
		StreamPause: func(context.Context, time.Duration) error { return nil },
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAlicloud, Mode: ModeRead, AuthScheme: "nls-ws", Service: "nls", Operation: "SpeechTranscriber",
		Method: http.MethodGet, URL: "wss://nls-gateway-ap-southeast-1.aliyuncs.com/ws/v1",
		Parameters: map[string]any{"appkey": "project-appkey"}, Body: map[string]any{"format": "pcm", "sample_rate": 16000},
		BodyFile: audioFile, ResponseFile: responseFile, StreamChunkBytes: 4, StreamIntervalMS: 1, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if tokenCalls != 1 || result.RequestID != "11111111111111111111111111111111" {
		t.Fatalf("token calls=%d result=%#v", tokenCalls, result)
	}
	if cached, err := adapter.loadAlibabaNLSToken(t.Context()); err != nil || cached != "nls-private-token" || tokenCalls != 1 {
		t.Fatalf("cached token=%q calls=%d err=%v", cached, tokenCalls, err)
	}
	if len(connection.writes) != 4 || connection.writes[0].messageType != cloudWebSocketMessageText || connection.writes[1].messageType != cloudWebSocketMessageBinary || connection.writes[2].messageType != cloudWebSocketMessageBinary || connection.writes[3].messageType != cloudWebSocketMessageText {
		t.Fatalf("writes=%#v", connection.writes)
	}
	if !bytes.Contains(connection.writes[0].data, []byte(`"name":"StartTranscription"`)) || !bytes.Contains(connection.writes[3].data, []byte(`"name":"StopTranscription"`)) || bytes.Contains(connection.writes[0].data, []byte("nls-private-token")) {
		t.Fatalf("unsafe commands start=%s stop=%s", connection.writes[0].data, connection.writes[3].data)
	}
	written, err := os.ReadFile(responseFile)
	if err != nil || !bytes.Contains(written, []byte(`"name":"SentenceEnd"`)) || strings.Contains(string(written), "nls-private-token") || strings.Contains(string(result.Output), "nls-private-token") {
		t.Fatalf("output=%s result=%s err=%v", written, result.Output, err)
	}
}

func TestAlibabaNLSWebSocketCoversRecognizerAndSingleSynthesisNamespaces(t *testing.T) {
	directory := t.TempDir()
	audioFile := filepath.Join(directory, "short.pcm")
	if err := os.WriteFile(audioFile, []byte("abcd"), 0o600); err != nil {
		t.Fatal(err)
	}
	tokenHTTP := doerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"Token":{"Id":"nls-private-token","ExpireTime":4102444800}}`))}, nil
	})
	t.Run("SpeechRecognizer", func(t *testing.T) {
		connection := &fakeTencentWebSocketConnection{reads: [][]byte{
			[]byte(`{"header":{"message_id":"provider-1","task_id":"44444444444444444444444444444444","namespace":"SpeechRecognizer","name":"RecognitionStarted","status":20000000}}`),
			[]byte(`{"header":{"message_id":"provider-2","task_id":"44444444444444444444444444444444","namespace":"SpeechRecognizer","name":"RecognitionCompleted","status":20000000},"payload":{"result":"hello"}}`),
		}}
		ids := []string{"44444444444444444444444444444444", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
		adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{
			Credentials: staticAlibabaCredentialsProvider{credentials: AlibabaCredentials{AccessKeyID: "ak", AccessKeySecret: "secret"}}, HTTP: tokenHTTP,
			Now: func() time.Time { return time.Unix(1735689600, 0).UTC() }, Nonce: func() string { return "rpc-nonce" },
			NLSID:            func() string { value := ids[0]; ids = ids[1:]; return value },
			NLSWebSocketDial: func(context.Context, string) (cloudWebSocketConnection, error) { return connection, nil },
			StreamPause:      func(context.Context, time.Duration) error { return nil },
		})
		responseFile := filepath.Join(directory, "short.ndjson")
		_, err := adapter.Invoke(t.Context(), Invocation{
			Provider: ProviderAlicloud, Mode: ModeRead, AuthScheme: "nls-ws", Service: "nls", Operation: "SpeechRecognizer",
			Method: http.MethodGet, URL: "wss://nls-gateway.aliyuncs.com/ws/v1", Parameters: map[string]any{"appkey": "project-appkey"},
			Body: map[string]any{"format": "pcm", "sample_rate": 16000}, BodyFile: audioFile, ResponseFile: responseFile, MaxResponseFileBytes: 4096,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(connection.writes) != 3 || !bytes.Contains(connection.writes[0].data, []byte(`"name":"StartRecognition"`)) || connection.writes[1].messageType != cloudWebSocketMessageBinary || !bytes.Contains(connection.writes[2].data, []byte(`"name":"StopRecognition"`)) {
			t.Fatalf("writes=%#v", connection.writes)
		}
	})

	for index, operation := range []string{"SpeechSynthesizer", "SpeechLongSynthesizer"} {
		t.Run(operation, func(t *testing.T) {
			taskID := strings.Repeat(strconv.Itoa(index+5), 32)
			connection := &fakeTencentWebSocketConnection{
				reads: [][]byte{
					[]byte("audio"),
					[]byte(fmt.Sprintf(`{"header":{"message_id":"provider","task_id":%q,"namespace":%q,"name":"SynthesisCompleted","status":20000000}}`, taskID, operation)),
				},
				readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageBinary, cloudWebSocketMessageText},
			}
			ids := []string{taskID, "cccccccccccccccccccccccccccccccc"}
			adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{
				Credentials: staticAlibabaCredentialsProvider{credentials: AlibabaCredentials{AccessKeyID: "ak", AccessKeySecret: "secret"}}, HTTP: tokenHTTP,
				Now: func() time.Time { return time.Unix(1735689600, 0).UTC() }, Nonce: func() string { return "rpc-nonce" },
				NLSID:            func() string { value := ids[0]; ids = ids[1:]; return value },
				NLSWebSocketDial: func(context.Context, string) (cloudWebSocketConnection, error) { return connection, nil },
			})
			responseFile := filepath.Join(directory, strings.ToLower(operation)+".mp3")
			result, err := adapter.Invoke(t.Context(), Invocation{
				Provider: ProviderAlicloud, Mode: ModeRead, AuthScheme: "nls-ws", Service: "nls", Operation: operation,
				Method: http.MethodGet, URL: "wss://nls-gateway-cn-beijing.aliyuncs.com/ws/v1", Parameters: map[string]any{"appkey": "project-appkey"},
				Body: map[string]any{"text": "hello", "format": "mp3", "sample_rate": 16000}, ResponseFile: responseFile, MaxResponseFileBytes: 4096,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(connection.writes) != 1 || !bytes.Contains(connection.writes[0].data, []byte(`"name":"StartSynthesis"`)) || !bytes.Contains(connection.writes[0].data, []byte(`"text":"hello"`)) {
				t.Fatalf("writes=%#v", connection.writes)
			}
			if audio, err := os.ReadFile(responseFile); err != nil || string(audio) != "audio" || !bytes.Contains(result.Output, []byte(`"SynthesisCompleted"`)) {
				t.Fatalf("audio=%q result=%s err=%v", audio, result.Output, err)
			}
		})
	}
}

func TestAlibabaNLSWebSocketStreamsTTSAndAtomicallyPublishesAudio(t *testing.T) {
	directory := t.TempDir()
	responseFile := filepath.Join(directory, "speech.mp3")
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			[]byte(`{"header":{"message_id":"provider-1","task_id":"22222222222222222222222222222222","namespace":"FlowingSpeechSynthesizer","name":"SynthesisStarted","status":20000000}}`),
			[]byte("audio-one"),
			[]byte(`{"header":{"message_id":"provider-2","task_id":"22222222222222222222222222222222","namespace":"FlowingSpeechSynthesizer","name":"SentenceEnd","status":20000000},"payload":{"index":1}}`),
			[]byte("audio-two"),
			[]byte(`{"header":{"message_id":"provider-3","task_id":"22222222222222222222222222222222","namespace":"FlowingSpeechSynthesizer","name":"SynthesisCompleted","status":20000000}}`),
		},
		readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageText, cloudWebSocketMessageBinary, cloudWebSocketMessageText, cloudWebSocketMessageBinary, cloudWebSocketMessageText},
	}
	ids := []string{"22222222222222222222222222222222", "cccccccccccccccccccccccccccccccc", "dddddddddddddddddddddddddddddddd", "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", "ffffffffffffffffffffffffffffffff"}
	adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{
		Credentials: staticAlibabaCredentialsProvider{credentials: AlibabaCredentials{AccessKeyID: "ak", AccessKeySecret: "secret"}},
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"Token":{"Id":"nls-private-token","ExpireTime":4102444800}}`))}, nil
		}),
		Now: func() time.Time { return time.Unix(1735689600, 0).UTC() }, Nonce: func() string { return "rpc-nonce" },
		NLSID: func() string {
			value := ids[0]
			ids = ids[1:]
			return value
		},
		NLSWebSocketDial: func(context.Context, string) (cloudWebSocketConnection, error) { return connection, nil },
		StreamPause:      func(context.Context, time.Duration) error { return nil },
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAlicloud, Mode: ModeRead, AuthScheme: "nls-ws", Service: "nls", Operation: "FlowingSpeechSynthesizer",
		Method: http.MethodGet, URL: "wss://nls-gateway-cn-beijing.aliyuncs.com/ws/v1", Parameters: map[string]any{"appkey": "project-appkey"},
		Body:         map[string]any{"start": map[string]any{"voice": "xiaoyun", "format": "mp3", "sample_rate": 16000}, "texts": []any{"hello", "world"}},
		ResponseFile: responseFile, StreamIntervalMS: 1, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(connection.writes) != 4 || !bytes.Contains(connection.writes[0].data, []byte(`"name":"StartSynthesis"`)) || !bytes.Contains(connection.writes[1].data, []byte(`"text":"hello"`)) || !bytes.Contains(connection.writes[2].data, []byte(`"text":"world"`)) || !bytes.Contains(connection.writes[3].data, []byte(`"name":"StopSynthesis"`)) {
		t.Fatalf("writes=%#v", connection.writes)
	}
	audio, err := os.ReadFile(responseFile)
	if err != nil || string(audio) != "audio-oneaudio-two" {
		t.Fatalf("audio=%q err=%v", audio, err)
	}
	if !bytes.Contains(result.Output, []byte(`"content_type":"audio/mpeg"`)) || !bytes.Contains(result.Output, []byte(`"events"`)) || !bytes.Contains(result.Output, []byte(`"SynthesisCompleted"`)) || strings.Contains(string(result.Output), "nls-private-token") {
		t.Fatalf("result=%s", result.Output)
	}
}

func TestAlibabaNLSWebSocketFailureNeverPublishesPartialAudio(t *testing.T) {
	directory := t.TempDir()
	responseFile := filepath.Join(directory, "speech.mp3")
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			[]byte(`{"header":{"message_id":"provider-1","task_id":"33333333333333333333333333333333","namespace":"FlowingSpeechSynthesizer","name":"SynthesisStarted","status":20000000}}`),
			[]byte("partial-audio"),
			[]byte(`{"header":{"message_id":"provider-2","task_id":"33333333333333333333333333333333","namespace":"FlowingSpeechSynthesizer","name":"TaskFailed","status":40000004,"status_message":"secret provider detail"}}`),
		},
		readTypes: []tencentWebSocketMessageType{cloudWebSocketMessageText, cloudWebSocketMessageBinary, cloudWebSocketMessageText},
	}
	ids := []string{"33333333333333333333333333333333", "0123456789abcdef0123456789abcdef", "abcdef0123456789abcdef0123456789", "1234567890abcdef1234567890abcdef"}
	adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{
		Credentials: staticAlibabaCredentialsProvider{credentials: AlibabaCredentials{AccessKeyID: "ak", AccessKeySecret: "secret"}},
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"Token":{"Id":"nls-private-token","ExpireTime":4102444800}}`))}, nil
		}),
		Now: func() time.Time { return time.Unix(1735689600, 0).UTC() }, Nonce: func() string { return "rpc-nonce" },
		NLSID: func() string {
			value := ids[0]
			ids = ids[1:]
			return value
		},
		NLSWebSocketDial: func(context.Context, string) (cloudWebSocketConnection, error) { return connection, nil },
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAlicloud, Mode: ModeRead, AuthScheme: "nls-ws", Service: "nls", Operation: "FlowingSpeechSynthesizer",
		Method: http.MethodGet, URL: "wss://nls-gateway-cn-beijing.aliyuncs.com/ws/v1", Parameters: map[string]any{"appkey": "project-appkey"},
		Body: map[string]any{"start": map[string]any{"format": "mp3"}, "texts": []any{"hello"}}, ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err == nil || strings.Contains(err.Error(), "secret provider detail") {
		t.Fatalf("unsafe or missing error: %v", err)
	}
	if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
		t.Fatalf("partial audio was published: %v", statErr)
	}
}

func TestAlibabaNLSWebSocketHandshakeErrorCannotExposeInternalToken(t *testing.T) {
	directory := t.TempDir()
	ids := []string{"77777777777777777777777777777777"}
	adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{
		Credentials: staticAlibabaCredentialsProvider{credentials: AlibabaCredentials{AccessKeyID: "ak", AccessKeySecret: "secret"}},
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"Token":{"Id":"nls-private-token","ExpireTime":4102444800}}`))}, nil
		}),
		Now: func() time.Time { return time.Unix(1735689600, 0).UTC() }, Nonce: func() string { return "rpc-nonce" },
		NLSID: func() string { value := ids[0]; ids = ids[1:]; return value },
		NLSWebSocketDial: func(_ context.Context, target string) (cloudWebSocketConnection, error) {
			return nil, fmt.Errorf("dial failed for %s", target)
		},
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAlicloud, Mode: ModeRead, AuthScheme: "nls-ws", Service: "nls", Operation: "SpeechSynthesizer",
		Method: http.MethodGet, URL: "wss://nls-gateway-cn-beijing.aliyuncs.com/ws/v1", Parameters: map[string]any{"appkey": "project-appkey"},
		Body: map[string]any{"text": "hello", "format": "mp3"}, ResponseFile: filepath.Join(directory, "speech.mp3"), MaxResponseFileBytes: 4096,
	})
	if err == nil || strings.Contains(err.Error(), "nls-private-token") || strings.Contains(err.Error(), "token=") {
		t.Fatalf("unsafe or missing error: %v", err)
	}
}

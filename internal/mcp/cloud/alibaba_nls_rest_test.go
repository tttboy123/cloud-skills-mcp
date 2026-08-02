package cloud

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAlibabaNLSRESTBoundarySupportsOnlyOfficialASRAndTTSShapes(t *testing.T) {
	directory := t.TempDir()
	audio := filepath.Join(directory, "audio.pcm")
	if err := os.WriteFile(audio, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	asr := Invocation{
		Provider: ProviderAlicloud, Mode: ModeRead, AuthScheme: "nls-rest", Service: "nls", Operation: "ShortSentenceRecognition",
		Method: http.MethodPost, URL: "https://nls-gateway-ap-southeast-1.aliyuncs.com/stream/v1/asr",
		Parameters: map[string]any{"appkey": "project-appkey", "format": "pcm", "sample_rate": 16000}, BodyFile: audio,
	}
	if err := validateInvocation(asr, []string{directory}); err != nil || !classifyRead(ProviderAlicloud, asr) {
		t.Fatalf("ASR boundary err=%v read=%v", err, classifyRead(ProviderAlicloud, asr))
	}
	ttsGET := Invocation{
		Provider: ProviderAlicloud, Mode: ModeRead, AuthScheme: "nls-rest", Service: "nls", Operation: "SpeechSynthesisREST",
		Method: http.MethodGet, URL: "https://nls-gateway-ap-southeast-1.aliyuncs.com/stream/v1/tts",
		Parameters: map[string]any{"appkey": "project-appkey", "text": "hello", "format": "mp3"}, ResponseFile: filepath.Join(directory, "get.mp3"),
	}
	if err := validateInvocation(ttsGET, []string{directory}); err != nil || !classifyRead(ProviderAlicloud, ttsGET) {
		t.Fatalf("TTS GET boundary err=%v read=%v", err, classifyRead(ProviderAlicloud, ttsGET))
	}
	ttsPOST := ttsGET
	ttsPOST.Method = http.MethodPost
	ttsPOST.Parameters = nil
	ttsPOST.Body = map[string]any{"appkey": "project-appkey", "text": "hello", "format": "mp3"}
	ttsPOST.ResponseFile = filepath.Join(directory, "post.mp3")
	if err := validateInvocation(ttsPOST, []string{directory}); err != nil {
		t.Fatal(err)
	}
	tooLongTTS := ttsGET
	tooLongTTS.Parameters = map[string]any{"appkey": "project-appkey", "text": strings.Repeat("界", 301)}
	if err := validateInvocation(tooLongTTS, []string{directory}); err == nil {
		t.Fatal("TTS text beyond the documented 300-character limit was accepted")
	}

	for name, mutate := range map[string]func(*Invocation){
		"HTTP":            func(value *Invocation) { value.URL = strings.Replace(value.URL, "https://", "http://", 1) },
		"wrong host":      func(value *Invocation) { value.URL = "https://example.com/stream/v1/asr" },
		"wrong path":      func(value *Invocation) { value.URL = "https://nls-gateway-ap-southeast-1.aliyuncs.com/other" },
		"caller token":    func(value *Invocation) { value.Parameters["token"] = "forbidden" },
		"auth header":     func(value *Invocation) { value.Headers = map[string]string{"X-NLS-Token": "forbidden"} },
		"missing appkey":  func(value *Invocation) { delete(value.Parameters, "appkey") },
		"missing audio":   func(value *Invocation) { value.BodyFile = "" },
		"inline ASR body": func(value *Invocation) { value.BodyFile = ""; value.Body = "audio" },
		"provider field":  func(value *Invocation) { value.Region = "ap-southeast-1" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := asr
			candidate.Parameters = map[string]any{"appkey": "project-appkey", "format": "pcm", "sample_rate": 16000}
			mutate(&candidate)
			if err := validateInvocation(candidate, []string{directory}); err == nil {
				t.Fatal("invalid NLS REST invocation accepted")
			}
		})
	}
}

func TestAlibabaNLSRESTUsesInternalHeaderTokenForASRAndTTS(t *testing.T) {
	directory := t.TempDir()
	audio := filepath.Join(directory, "audio.pcm")
	if err := os.WriteFile(audio, []byte("audio-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	apiCalls := 0
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "nlsmeta.ap-southeast-1.aliyuncs.com" {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"Token":{"Id":"nls-private-token","ExpireTime":4102444800}}`))}, nil
		}
		apiCalls++
		if request.Header.Get("X-NLS-Token") != "nls-private-token" || strings.Contains(request.URL.RawQuery, "token") || strings.Contains(request.URL.String(), "nls-private-token") {
			return nil, fmt.Errorf("unsafe NLS auth target=%s headers=%v", request.URL, request.Header)
		}
		switch request.URL.Path {
		case "/stream/v1/asr":
			body, err := io.ReadAll(request.Body)
			if err != nil || string(body) != "audio-bytes" || request.Method != http.MethodPost || request.Header.Get("Content-Type") != "application/octet-stream" || request.URL.Query().Get("appkey") != "project-appkey" {
				return nil, fmt.Errorf("bad ASR request body=%q err=%v", body, err)
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"task_id":"asr-task","result":"hello","status":20000000,"message":"SUCCESS"}`))}, nil
		case "/stream/v1/tts":
			var body []byte
			var err error
			if request.Body != nil {
				body, err = io.ReadAll(request.Body)
				if err != nil {
					return nil, fmt.Errorf("read TTS request: %w", err)
				}
			}
			switch request.Method {
			case http.MethodPost:
				if !bytes.Contains(body, []byte(`"appkey":"project-appkey"`)) || request.Header.Get("Content-Type") != "application/json" {
					return nil, fmt.Errorf("bad TTS POST request body=%q", body)
				}
			case http.MethodGet:
				if len(body) != 0 || request.Header.Get("Content-Type") != "" || request.URL.Query().Get("appkey") != "project-appkey" || request.URL.Query().Get("text") != "hello from GET" {
					return nil, fmt.Errorf("bad TTS GET request body=%q query=%v headers=%v", body, request.URL.Query(), request.Header)
				}
			default:
				return nil, fmt.Errorf("unexpected TTS method %s", request.Method)
			}
			headers := make(http.Header)
			headers.Set("Content-Type", "audio/mpeg")
			headers.Set("X-NLS-RequestId", "tts-task")
			return &http.Response{StatusCode: http.StatusOK, Header: headers, Body: io.NopCloser(strings.NewReader("audio-result"))}, nil
		default:
			return nil, fmt.Errorf("unexpected path %s", request.URL.Path)
		}
	})
	adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{
		Credentials: staticAlibabaCredentialsProvider{credentials: AlibabaCredentials{AccessKeyID: "ak", AccessKeySecret: "secret"}}, HTTP: doer,
		Now: func() time.Time { return time.Unix(1735689600, 0).UTC() }, Nonce: func() string { return "rpc-nonce" },
	})
	asrResult, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAlicloud, AuthScheme: "nls-rest", Service: "nls", Operation: "ShortSentenceRecognition",
		Method: http.MethodPost, URL: "https://nls-gateway-ap-southeast-1.aliyuncs.com/stream/v1/asr",
		Parameters: map[string]any{"appkey": "project-appkey", "format": "pcm", "sample_rate": 16000}, BodyFile: audio,
	})
	if err != nil || !bytes.Contains(asrResult.Output, []byte(`"status":20000000`)) || asrResult.RequestID != "asr-task" {
		t.Fatalf("ASR result=%#v err=%v", asrResult, err)
	}
	responseFile := filepath.Join(directory, "speech.mp3")
	ttsResult, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAlicloud, AuthScheme: "nls-rest", Service: "nls", Operation: "SpeechSynthesisREST",
		Method: http.MethodPost, URL: "https://nls-gateway-ap-southeast-1.aliyuncs.com/stream/v1/tts",
		Body: map[string]any{"appkey": "project-appkey", "text": "hello", "format": "mp3"}, ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err != nil || !bytes.Contains(ttsResult.Output, []byte(`"response_file"`)) || strings.Contains(string(ttsResult.Output), "nls-private-token") {
		t.Fatalf("TTS result=%#v err=%v", ttsResult, err)
	}
	if data, err := os.ReadFile(responseFile); err != nil || string(data) != "audio-result" || apiCalls != 2 {
		t.Fatalf("audio=%q calls=%d err=%v", data, apiCalls, err)
	}
	getResponseFile := filepath.Join(directory, "speech-get.mp3")
	getResult, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAlicloud, AuthScheme: "nls-rest", Service: "nls", Operation: "SpeechSynthesisREST",
		Method: http.MethodGet, URL: "https://nls-gateway-ap-southeast-1.aliyuncs.com/stream/v1/tts",
		Parameters: map[string]any{"appkey": "project-appkey", "text": "hello from GET", "format": "mp3"}, ResponseFile: getResponseFile, MaxResponseFileBytes: 4096,
	})
	if err != nil || getResult.RequestID != "tts-task" || strings.Contains(string(getResult.Output), "nls-private-token") {
		t.Fatalf("TTS GET result=%#v err=%v", getResult, err)
	}
	if data, err := os.ReadFile(getResponseFile); err != nil || string(data) != "audio-result" || apiCalls != 3 {
		t.Fatalf("GET audio=%q calls=%d err=%v", data, apiCalls, err)
	}
}

func TestAlibabaNLSRESTDoesNotPublishTTSErrorBody(t *testing.T) {
	directory := t.TempDir()
	responseFile := filepath.Join(directory, "speech.mp3")
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "nlsmeta.ap-southeast-1.aliyuncs.com" {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"Token":{"Id":"nls-private-token","ExpireTime":4102444800}}`))}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json;charset=utf-8"}},
			Body:       io.NopCloser(strings.NewReader(`{"status":40000000,"message":"invalid text"}`)),
		}, nil
	})
	adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{
		Credentials: staticAlibabaCredentialsProvider{credentials: AlibabaCredentials{AccessKeyID: "ak", AccessKeySecret: "secret"}}, HTTP: doer,
		Now: func() time.Time { return time.Unix(1735689600, 0).UTC() }, Nonce: func() string { return "rpc-nonce" },
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAlicloud, AuthScheme: "nls-rest", Service: "nls", Operation: "SpeechSynthesisREST",
		Method: http.MethodPost, URL: "https://nls-gateway-ap-southeast-1.aliyuncs.com/stream/v1/tts",
		Body: map[string]any{"appkey": "project-appkey", "text": "hello"}, ResponseFile: responseFile,
	})
	if err == nil || !strings.Contains(err.Error(), "error response") {
		t.Fatalf("expected bounded provider error, got %v", err)
	}
	if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
		t.Fatalf("failed response was published: %v", statErr)
	}
}

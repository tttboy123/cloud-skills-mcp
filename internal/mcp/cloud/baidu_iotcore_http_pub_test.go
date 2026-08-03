package cloud

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func validBaiduIoTCoreHTTPPubInvocation() Invocation {
	return Invocation{
		Provider: ProviderBaidu, Mode: ModeMutate, AuthScheme: authSchemeBaiduIoTCoreHTTPPub,
		Service: "iotcore", Operation: "PublishHTTP", Method: http.MethodPost,
		URL: "https://aop098js.iot.gz.baidubce.com/pub",
		Body: map[string]any{
			"topic": "commands/device-1", "qos": 1,
			"payload_base64": base64.StdEncoding.EncodeToString([]byte("turn-on")),
		},
	}
}

func TestBaiduIoTCoreHTTPPubTargetAndPlanFailClosed(t *testing.T) {
	valid := validBaiduIoTCoreHTTPPubInvocation()
	if err := validateBaiduIoTCoreHTTPPubInvocation(valid); err != nil {
		t.Fatalf("valid invocation rejected: %v", err)
	}
	if err := validateInvocation(valid, nil); err != nil {
		t.Fatalf("public policy rejected valid HTTP publish: %v", err)
	}
	if classifyRead(ProviderBaidu, valid) {
		t.Fatal("HTTP publish was classified as read-only")
	}
	restricted := validBaiduIoTCoreHTTPPubInvocation()
	restricted.Body.(map[string]any)["max_payload_bytes"] = 1024
	if err := validateBaiduIoTCoreHTTPPubInvocation(restricted); err != nil {
		t.Fatalf("caller-selected stricter payload bound rejected: %v", err)
	}
	tests := map[string]func(*Invocation){
		"lookalike":        func(value *Invocation) { value.URL = "https://aop098js.iot.gz.baidubce.com.attacker.example/pub" },
		"wrong path":       func(value *Invocation) { value.URL = "https://aop098js.iot.gz.baidubce.com/auth" },
		"query":            func(value *Invocation) { value.URL += "?token=caller" },
		"read mode":        func(value *Invocation) { value.Mode = ModeRead },
		"credential field": func(value *Invocation) { value.Body.(map[string]any)["token"] = "caller-token" },
		"topic wildcard":   func(value *Invocation) { value.Body.(map[string]any)["topic"] = "commands/#" },
		"qos2":             func(value *Invocation) { value.Body.(map[string]any)["qos"] = 2 },
		"oversize": func(value *Invocation) {
			value.Body.(map[string]any)["payload_base64"] = base64.StdEncoding.EncodeToString(make([]byte, baiduIoTCoreMQTTDefaultPayloadMax+1))
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			value := valid
			encoded, _ := json.Marshal(valid.Body)
			_ = json.Unmarshal(encoded, &value.Body)
			mutate(&value)
			if err := validateBaiduIoTCoreHTTPPubInvocation(value); err == nil {
				t.Fatal("unsafe HTTP publish accepted")
			}
		})
	}
}

func TestBaiduIoTCoreHTTPPubBodyFilePassesPublicPolicy(t *testing.T) {
	root := t.TempDir()
	payloadFile := filepath.Join(root, "payload.bin")
	if err := os.WriteFile(payloadFile, []byte("binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	invocation := validBaiduIoTCoreHTTPPubInvocation()
	invocation.Body = map[string]any{"topic": "commands/device-1", "qos": 0}
	invocation.BodyFile = payloadFile
	if err := validateInvocation(invocation, []string{root}); err != nil {
		t.Fatalf("body_file protocol plan rejected by public policy: %v", err)
	}
}

func TestBaiduAdapterPublishesIoTCoreHTTPWithInternalToken(t *testing.T) {
	credentials := BCECredentials{AccessKeyID: "7761E24FC8b9bee8703a5efb266d9c0", SecretAccessKey: "ABCxxxx1234567"}
	calls := 0
	adapter := NewBaiduRESTAdapter(BaiduRESTConfig{
		Credentials: staticBCECredentials{credentials: credentials},
		Now:         func() time.Time { return time.UnixMilli(1600834787219).UTC() },
		HTTP: doerFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			switch calls {
			case 1:
				if request.URL.String() != "https://aop098js.iot.gz.baidubce.com/auth" || request.Method != http.MethodPost || request.Header.Get("token") != "" || request.Header.Get("Content-Type") != "application/json" {
					t.Fatalf("auth request=%s headers=%v", request.URL, request.Header)
				}
				var auth map[string]any
				if json.Unmarshal(body, &auth) != nil || auth["username"] != "bceiam@aop098js|7761E24FC8b9bee8703a5efb266d9c0|1600834787219|SHA256" || auth["password"] != "1b937b1268d8943860038f2a4bec637e5370ded2e848289bee1594e30c600d39" || auth["tokenLifeSpanInSeconds"] != float64(60) {
					t.Fatalf("auth body=%s", body)
				}
				return baiduCCRHTTPResponse(http.StatusOK, `{"token":"header.payload.signature"}`), nil
			case 2:
				if request.URL.Path != "/pub" || request.URL.Query().Get("topic") != "commands/device-1" || request.URL.Query().Get("qos") != "1" || request.Header.Get("token") != "header.payload.signature" || request.Header.Get("Content-Type") != "application/octet-stream" || string(body) != "turn-on" {
					t.Fatalf("pub request=%s headers=%v body=%q", request.URL, request.Header, body)
				}
				return baiduCCRHTTPResponse(http.StatusOK, `{"message":"ok"}`), nil
			default:
				t.Fatal("unexpected HTTP request")
				return nil, nil
			}
		}),
	})
	result, err := adapter.Invoke(t.Context(), validBaiduIoTCoreHTTPPubInvocation())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || !strings.Contains(string(result.Output), `"message":"ok"`) {
		t.Fatalf("calls=%d result=%s", calls, result.Output)
	}
	for _, secret := range []string{credentials.AccessKeyID, credentials.SecretAccessKey, "header.payload.signature", "bceiam@"} {
		if strings.Contains(string(result.Output), secret) {
			t.Fatalf("result leaked %q", secret)
		}
	}
}

func TestBaiduAdapterPublishesIoTCoreHTTPBodyFile(t *testing.T) {
	payloadFile := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(payloadFile, []byte{0x00, 0x01, 0x02}, 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	adapter := NewBaiduRESTAdapter(BaiduRESTConfig{
		Credentials: staticBCECredentials{credentials: BCECredentials{AccessKeyID: "iam-ak", SecretAccessKey: "iam-secret"}},
		HTTP: doerFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				return baiduCCRHTTPResponse(http.StatusOK, `{"token":"header.payload.signature"}`), nil
			}
			body, _ := io.ReadAll(request.Body)
			if string(body) != string([]byte{0x00, 0x01, 0x02}) {
				t.Fatalf("payload=%x", body)
			}
			return baiduCCRHTTPResponse(http.StatusOK, `{"message":"ok"}`), nil
		}),
	})
	invocation := validBaiduIoTCoreHTTPPubInvocation()
	invocation.Body = map[string]any{"topic": "commands/device-1", "qos": 0}
	invocation.BodyFile = payloadFile
	if _, err := adapter.Invoke(t.Context(), invocation); err != nil {
		t.Fatal(err)
	}
}

func TestBaiduAdapterIoTCoreHTTPAuthFailureDoesNotPublishOrLeak(t *testing.T) {
	calls := 0
	adapter := NewBaiduRESTAdapter(BaiduRESTConfig{
		Credentials: staticBCECredentials{credentials: BCECredentials{AccessKeyID: "iam-ak", SecretAccessKey: "iam-secret"}},
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return baiduCCRHTTPResponse(http.StatusUnauthorized, `{"message":"iam-secret rejected"}`), nil
		}),
	})
	_, err := adapter.Invoke(context.Background(), validBaiduIoTCoreHTTPPubInvocation())
	if err == nil || calls != 1 || strings.Contains(err.Error(), "iam-secret") {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestBaiduAdapterIoTCoreHTTPRejectsInvalidSuccessResponse(t *testing.T) {
	calls := 0
	adapter := NewBaiduRESTAdapter(BaiduRESTConfig{
		Credentials: staticBCECredentials{credentials: BCECredentials{AccessKeyID: "iam-ak", SecretAccessKey: "iam-secret"}},
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				return baiduCCRHTTPResponse(http.StatusOK, `{"token":"header.payload.signature"}`), nil
			}
			return baiduCCRHTTPResponse(http.StatusOK, `{"message":"queued"}`), nil
		}),
	})
	if _, err := adapter.Invoke(t.Context(), validBaiduIoTCoreHTTPPubInvocation()); err == nil || calls != 2 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestBaiduIoTCoreHTTPPubEnforcesProviderQPS(t *testing.T) {
	var waits []time.Duration
	adapter := NewBaiduRESTAdapter(BaiduRESTConfig{
		Now: func() time.Time { return time.Unix(1, 0).UTC() },
		StreamPause: func(_ context.Context, duration time.Duration) error {
			waits = append(waits, duration)
			return nil
		},
	})
	for range 3 {
		if err := adapter.reserveBaiduIoTCoreHTTPPub(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if len(waits) != 2 || waits[0] != 20*time.Millisecond || waits[1] != 40*time.Millisecond {
		t.Fatalf("waits=%v", waits)
	}
}

func TestBaiduIoTCoreHTTPPubRejectsSessionCredential(t *testing.T) {
	adapter := NewBaiduRESTAdapter(BaiduRESTConfig{
		Credentials: staticBCECredentials{credentials: BCECredentials{AccessKeyID: "iam-ak", SecretAccessKey: "iam-secret", SessionToken: "temporary"}},
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("session credential reached HTTP transport")
			return nil, nil
		}),
	})
	if _, err := adapter.Invoke(t.Context(), validBaiduIoTCoreHTTPPubInvocation()); err == nil || strings.Contains(err.Error(), "temporary") {
		t.Fatalf("err=%v", err)
	}
}

func FuzzBaiduIoTCoreHTTPPubTargetAndPlan(f *testing.F) {
	f.Add("https://aop098js.iot.gz.baidubce.com/pub", `{"topic":"commands/device-1","qos":1,"payload_base64":"dHVybi1vbg=="}`)
	f.Add("https://aop098js.iot.gz.baidubce.com.attacker.example/pub", `{"token":"caller"}`)
	f.Fuzz(func(t *testing.T, rawURL, rawBody string) {
		var body any
		if json.Unmarshal([]byte(rawBody), &body) != nil {
			body = rawBody
		}
		invocation := validBaiduIoTCoreHTTPPubInvocation()
		invocation.URL = rawURL
		invocation.Body = body
		_ = validateBaiduIoTCoreHTTPPubInvocation(invocation)
	})
}

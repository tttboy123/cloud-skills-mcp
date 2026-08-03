package cloud

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type staticBCECredentials struct {
	credentials BCECredentials
	err         error
}

func (provider staticBCECredentials) Credentials(context.Context) (BCECredentials, error) {
	return provider.credentials, provider.err
}

func TestBCESignerMatchesOfficialReferenceVector(t *testing.T) {
	request, err := http.NewRequest(http.MethodPut, "https://bce.baidu.com/v1/bucket/object1?A&b=&C=d", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("abc", "123")
	request.Header.Set("x-bce-meta-key1", "ABC")
	timestamp := time.Unix(1402639056, 0).UTC()
	authorization, err := signBCERequest(request, BCECredentials{AccessKeyID: "my_ak", SecretAccessKey: "my_sk"}, timestamp, 1800)
	if err != nil {
		t.Fatal(err)
	}
	want := "bce-auth-v1/my_ak/2014-06-13T05:57:36Z/1800/host;x-bce-meta-key1/80c9672aca2ea9af4bb40b9a8ff458d72df94e97d550840727f3a929af271d25"
	if authorization != want {
		t.Fatalf("authorization:\nwant %s\n got %s", want, authorization)
	}
}

func TestBCEV2SignerIncludesDateRegionServiceAndRequiredHeaders(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "https://bts.bj.baidubce.com/v1/forms?limit=10", nil)
	if err != nil {
		t.Fatal(err)
	}
	timestamp := time.Date(2015, 4, 27, 8, 23, 49, 0, time.UTC)
	authorization, err := signBCEV2Request(request, BCECredentials{AccessKeyID: "my_ak", SecretAccessKey: "my_sk"}, timestamp, "bj", "bts")
	if err != nil {
		t.Fatal(err)
	}
	want := "bce-auth-v2/my_ak/20150427/bj/bts/host;x-bce-date/c63d91fe40c53a7ae539f8fcb2baeecd75b9f26fb20631afdf851c4a13b9601d"
	if authorization != want || request.Header.Get("x-bce-date") != "2015-04-27T08:23:49Z" {
		t.Fatalf("authorization=%q date=%q", authorization, request.Header.Get("x-bce-date"))
	}
}

func TestBaiduRESTAdapterSignsWithAKSKAndSTSTokenInternally(t *testing.T) {
	credentials := BCECredentials{AccessKeyID: "bce-ak", SecretAccessKey: "bce-secret", SessionToken: "temporary-token"}
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		authorization := request.Header.Get("Authorization")
		if !strings.HasPrefix(authorization, "bce-auth-v1/bce-ak/") || strings.Contains(authorization, credentials.SecretAccessKey) {
			t.Fatalf("unexpected authorization header %q", authorization)
		}
		if request.Header.Get("x-bce-security-token") != credentials.SessionToken {
			t.Fatal("temporary IAM token was not attached")
		}
		body, err := io.ReadAll(request.Body)
		if err != nil || string(body) != `{"name":"demo"}` {
			t.Fatalf("body=%s err=%v", body, err)
		}
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"X-Bce-Request-Id": []string{"bce-request"}},
			Body:       io.NopCloser(strings.NewReader(`{"result":"ok"}`)),
		}, nil
	})
	adapter := NewBaiduRESTAdapter(BaiduRESTConfig{
		Credentials: staticBCECredentials{credentials: credentials}, HTTP: doer,
		Now: func() time.Time { return time.Unix(1402639056, 0).UTC() },
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderBaidu, Method: http.MethodPost,
		URL: "https://bcc.bj.baidubce.com/v2/instance", Body: map[string]any{"name": "demo"},
	})
	if err != nil || string(result.Output) != `{"result":"ok"}` || result.RequestID != "bce-request" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	for _, secret := range []string{credentials.AccessKeyID, credentials.SecretAccessKey, credentials.SessionToken} {
		if strings.Contains(string(result.Output), secret) {
			t.Fatalf("credential leaked in provider output")
		}
	}
}

func TestBaiduDiscoveryReturnsOfficialDocumentationMapWithoutCredentials(t *testing.T) {
	adapter := NewBaiduRESTAdapter(BaiduRESTConfig{
		Credentials: staticBCECredentials{credentials: BCECredentials{AccessKeyID: "must-not", SecretAccessKey: "be-used"}},
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("discovery must not send credentials or call arbitrary endpoints")
			return nil, nil
		}),
	})
	output, err := adapter.Discover(t.Context(), DiscoveryRequest{Provider: ProviderBaidu, Service: "bcc"})
	if err != nil || !strings.Contains(string(output), "cloud.baidu.com/doc/BCC") {
		t.Fatalf("output=%s err=%v", output, err)
	}
}

func TestBaiduDiscoveryMapsRTCAgentLifecycleDocuments(t *testing.T) {
	adapter := NewBaiduRESTAdapter(BaiduRESTConfig{
		Credentials: staticBCECredentials{credentials: BCECredentials{AccessKeyID: "unused", SecretAccessKey: "unused"}},
	})
	output, err := adapter.Discover(t.Context(), DiscoveryRequest{Provider: ProviderBaidu, Service: "rtc-aiagent"})
	if err != nil || !strings.Contains(string(output), "RTC/s/hm8zjic1q") || !strings.Contains(string(output), "RTC/s/Jmakuvimy") {
		t.Fatalf("output=%s err=%v", output, err)
	}
}

func TestBaiduDiscoveryMapsIoTCoreHTTPAndMQTTDocuments(t *testing.T) {
	adapter := NewBaiduRESTAdapter(BaiduRESTConfig{
		Credentials: staticBCECredentials{credentials: BCECredentials{AccessKeyID: "unused", SecretAccessKey: "unused"}},
	})
	output, err := adapter.Discover(t.Context(), DiscoveryRequest{Provider: ProviderBaidu, Service: "iotcore"})
	if err != nil || !strings.Contains(string(output), "IoTCore/s/Gkfeuwrpr") || !strings.Contains(string(output), "IoTCore/s/okck4e7k4") || !strings.Contains(string(output), "IoTCore/s/hk7omsfcl") || !strings.Contains(string(output), "IoTCore/s/ikahmphms") {
		t.Fatalf("output=%s err=%v", output, err)
	}
}

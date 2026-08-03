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

type countingAlibabaCredentialsProvider struct {
	credentials AlibabaCredentials
	calls       int
}

func TestAlibabaMQIsAdvertisedByStatusAndDiscovery(t *testing.T) {
	adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{})
	status, err := adapter.Status(t.Context())
	if err != nil || !strings.Contains(status.Version, "+mq+") || !strings.Contains(status.Message, "no cloud CLI") {
		t.Fatalf("status=%#v err=%v", status, err)
	}
	encoded, err := adapter.Discover(t.Context(), DiscoveryRequest{Service: "rocketmq"})
	if err != nil {
		t.Fatal(err)
	}
	var discovery map[string]string
	if err := json.Unmarshal(encoded, &discovery); err != nil || discovery["mq_signature"] == "" || discovery["mq_http"] == "" {
		t.Fatalf("discovery=%s err=%v", encoded, err)
	}
}

func (provider *countingAlibabaCredentialsProvider) Credentials(context.Context) (AlibabaCredentials, error) {
	provider.calls++
	return provider.credentials, nil
}

func TestSignAlibabaMQUsesOfficialAuthorizationAndHeaders(t *testing.T) {
	request, err := http.NewRequest(http.MethodPost, "https://123456.mqrest.cn-hangzhou.aliyuncs.com/topics/orders/messages?ns=MQ_INST_1", strings.NewReader("<Message><MessageBody>aGVsbG8=</MessageBody></Message>"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "text/xml;charset=utf-8")
	request.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("<Message><MessageBody>aGVsbG8=</MessageBody></Message>")), nil
	}
	credentials := AlibabaCredentials{AccessKeyID: "testid", AccessKeySecret: "testsecret", SecurityToken: "sts-token"}
	if err := signAlibabaMQ(request, credentials, time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if request.Header.Get("X-Mq-Version") != "2015-06-06" || request.Header.Get("Date") != "Mon, 03 Aug 2026 01:02:03 GMT" {
		t.Fatalf("headers=%v", request.Header)
	}
	if request.Header.Get("Security-Token") != "sts-token" {
		t.Fatalf("security token=%q", request.Header.Get("Security-Token"))
	}
	if request.Header.Get("Content-MD5") != "MmYzZDUxN2ZhNDg5NjRiNDMyN2E3YWQ0ZTQxMzEyMGE=" {
		t.Fatalf("content md5=%q", request.Header.Get("Content-MD5"))
	}
	if got := request.Header.Get("Authorization"); got != "MQ testid:zzlyDBsSb7sBWZgOLWjdFWc9dkg=" {
		t.Fatalf("authorization=%q", got)
	}
	empty, _ := http.NewRequest(http.MethodGet, "https://123456.mqrest.cn-hangzhou.aliyuncs.com/topics/orders/messages?consumer=g&numOfMessages=1", nil)
	if err := signAlibabaMQ(empty, credentials, time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if empty.Header.Get("Content-MD5") != "ZDQxZDhjZDk4ZjAwYjIwNGU5ODAwOTk4ZWNmODQyN2U=" {
		t.Fatalf("empty content md5=%q", empty.Header.Get("Content-MD5"))
	}
}

func TestAlibabaMQConsumeAcknowledgesInternallyAndStripsHandles(t *testing.T) {
	credentials := &countingAlibabaCredentialsProvider{credentials: AlibabaCredentials{AccessKeyID: "ak", AccessKeySecret: "secret", SecurityToken: "sts"}}
	var requests []*http.Request
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		requests = append(requests, request)
		if request.Header.Get("Authorization") == "" || request.Header.Get("Security-Token") != "sts" {
			t.Fatalf("unsigned request headers=%v", request.Header)
		}
		if len(requests) == 1 {
			if request.Method != http.MethodGet || request.URL.RawQuery != "consumer=group-a&ns=MQ_INST_1&numOfMessages=2&tag=blue&waitseconds=10" {
				t.Fatalf("consume request=%s %s", request.Method, request.URL.String())
			}
			response := httpResponse(200, `<Messages><Message><MessageId>m-1</MessageId><ReceiptHandle>secret-handle-1</ReceiptHandle><MessageBodyMD5>md5-1</MessageBodyMD5><MessageBody>aGVsbG8=</MessageBody><PublishTime>1</PublishTime><NextConsumeTime>2</NextConsumeTime><FirstConsumeTime>3</FirstConsumeTime><ConsumedTimes>1</ConsumedTimes><MessageTag>blue</MessageTag><Properties>KEYS:key-1|custom:value|</Properties></Message><Message><MessageId>m-2</MessageId><ReceiptHandle>secret-handle-2</ReceiptHandle><MessageBodyMD5>md5-2</MessageBodyMD5><MessageBody>d29ybGQ=</MessageBody><PublishTime>4</PublishTime><NextConsumeTime>5</NextConsumeTime><FirstConsumeTime>6</FirstConsumeTime><ConsumedTimes>2</ConsumedTimes></Message></Messages>`)
			response.Header.Set("X-Mq-Request-Id", "request-1")
			return response, nil
		}
		if request.Method != http.MethodDelete || request.URL.RawQuery != "consumer=group-a&ns=MQ_INST_1" {
			t.Fatalf("ack request=%s %s", request.Method, request.URL.String())
		}
		body, _ := io.ReadAll(request.Body)
		if !strings.Contains(string(body), "secret-handle-1") || !strings.Contains(string(body), "secret-handle-2") {
			t.Fatalf("ack body=%s", body)
		}
		return httpResponse(204, ""), nil
	})
	target := filepath.Join(t.TempDir(), "messages.ndjson")
	adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{Credentials: credentials, HTTP: doer, Now: func() time.Time { return time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC) }})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAlicloud, Mode: ModeMutate, AuthScheme: authSchemeAlibabaMQ,
		Service: "rocketmq", Operation: "ConsumeMessages", Method: http.MethodGet,
		URL:        "https://123456.mqrest.cn-hangzhou.aliyuncs.com/topics/orders/messages",
		Parameters: map[string]any{"consumer": "group-a", "ns": "MQ_INST_1", "numOfMessages": 2, "waitseconds": 10, "tag": "blue"},
		Body:       map[string]any{"settlement": "acknowledge"}, ResponseFile: target,
	})
	if err != nil {
		t.Fatal(err)
	}
	if credentials.calls != 1 || len(requests) != 2 || result.RequestID != "request-1" {
		t.Fatalf("credentials=%d requests=%d result=%#v", credentials.calls, len(requests), result)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(strings.ToLower(text), "receipt") || strings.Contains(text, "secret-handle") || !strings.Contains(text, `"message_id":"m-1"`) || !strings.Contains(text, `"message_key":"key-1"`) {
		t.Fatalf("unsafe output=%s", text)
	}
	if !strings.Contains(string(result.Output), `"content_type":"application/x-ndjson"`) {
		t.Fatalf("metadata=%s", result.Output)
	}
}

func TestAlibabaMQPublishTransactionSettlesBeforePublishingOutput(t *testing.T) {
	credentials := &countingAlibabaCredentialsProvider{credentials: AlibabaCredentials{AccessKeyID: "ak", AccessKeySecret: "secret"}}
	var calls int
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		body, _ := io.ReadAll(request.Body)
		if calls == 1 {
			if request.Method != http.MethodPost || !strings.Contains(string(body), `<Properties>KEYS:order-1|__TransCheckT:30|custom:value|</Properties>`) {
				t.Fatalf("publish=%s %s", request.Method, body)
			}
			return httpResponse(201, `<Message><MessageId>m-1</MessageId><MessageBodyMD5>abc</MessageBodyMD5><ReceiptHandle>transaction-secret</ReceiptHandle></Message>`), nil
		}
		if request.Method != http.MethodDelete || request.URL.Query().Get("trans") != "commit" || request.URL.Query().Get("consumer") != "producer-group-a" || !strings.Contains(string(body), "transaction-secret") {
			t.Fatalf("settlement=%s %s body=%s", request.Method, request.URL.String(), body)
		}
		return httpResponse(204, ""), nil
	})
	target := filepath.Join(t.TempDir(), "published.ndjson")
	adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{Credentials: credentials, HTTP: doer})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAlicloud, Mode: ModeMutate, AuthScheme: authSchemeAlibabaMQ,
		Service: "rocketmq", Operation: "PublishMessage", Method: http.MethodPost,
		URL:        "https://123456.mqrest.cn-hangzhou.aliyuncs.com/topics/orders/messages",
		Parameters: map[string]any{"ns": "MQ_INST_1"}, ResponseFile: target,
		Body: map[string]any{
			"message_body":        base64.StdEncoding.EncodeToString([]byte("hello")),
			"properties":          map[string]any{"KEYS": "order-1", "__TransCheckT": "30", "custom": "value"},
			"producer_group":      "producer-group-a",
			"transaction_outcome": "commit",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(target)
	if calls != 2 || credentials.calls != 1 || strings.Contains(string(data), "transaction-secret") || !strings.Contains(string(data), `"message_id":"m-1"`) {
		t.Fatalf("calls=%d credentials=%d output=%s", calls, credentials.calls, data)
	}
}

func TestAlibabaMQFollowUpFailureDoesNotPublishOutput(t *testing.T) {
	var calls int
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return httpResponse(200, `<Messages><Message><MessageId>m-1</MessageId><ReceiptHandle>secret-handle</ReceiptHandle><MessageBody>eA==</MessageBody></Message></Messages>`), nil
		}
		return httpResponse(500, `<Error><Code>InternalError</Code><Message>failed</Message><RequestId>r-2</RequestId></Error>`), nil
	})
	target := filepath.Join(t.TempDir(), "must-not-exist.ndjson")
	adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{Credentials: staticAlibabaCredentialsProvider{credentials: AlibabaCredentials{AccessKeyID: "ak", AccessKeySecret: "secret"}}, HTTP: doer})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAlicloud, Mode: ModeMutate, AuthScheme: authSchemeAlibabaMQ,
		Service: "rocketmq", Operation: "ConsumeMessages", Method: http.MethodGet,
		URL:        "https://123456.mqrest.cn-hangzhou.aliyuncs.com/topics/orders/messages",
		Parameters: map[string]any{"consumer": "group-a", "numOfMessages": 1},
		Body:       map[string]any{"settlement": "acknowledge"}, ResponseFile: target,
	})
	if err == nil || !strings.Contains(err.Error(), "InternalError") {
		t.Fatalf("err=%v", err)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("response file leaked: %v", statErr)
	}
}

func TestAlibabaMQReleaseNeverExportsHandleOrSendsDelete(t *testing.T) {
	var calls int
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		return httpResponse(200, `<Messages><Message><MessageId>m-1</MessageId><ReceiptHandle>secret-handle</ReceiptHandle><MessageBody>eA==</MessageBody></Message></Messages>`), nil
	})
	target := filepath.Join(t.TempDir(), "released.ndjson")
	adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{Credentials: staticAlibabaCredentialsProvider{credentials: AlibabaCredentials{AccessKeyID: "ak", AccessKeySecret: "secret"}}, HTTP: doer})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAlicloud, Mode: ModeMutate, AuthScheme: authSchemeAlibabaMQ,
		Service: "rocketmq", Operation: "ConsumeOrderly", Method: http.MethodGet,
		URL:        "https://123456.mqrest.cn-hangzhou.aliyuncs.com/topics/orders/messages",
		Parameters: map[string]any{"consumer": "group-a", "numOfMessages": 1, "trans": "order"},
		Body:       map[string]any{"settlement": "release"}, ResponseFile: target,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(target)
	if calls != 1 || strings.Contains(string(data), "secret-handle") || strings.Contains(strings.ToLower(string(data)), "receipt") {
		t.Fatalf("calls=%d output=%s", calls, data)
	}
}

func TestAlibabaMQValidationHappensBeforeCredentials(t *testing.T) {
	credentials := &countingAlibabaCredentialsProvider{credentials: AlibabaCredentials{AccessKeyID: "ak", AccessKeySecret: "secret"}}
	adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{Credentials: credentials, HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("network reached")
		return nil, nil
	})})
	tests := []Invocation{
		{Provider: ProviderAlicloud, Mode: ModeMutate, AuthScheme: authSchemeAlibabaMQ, Service: "ecs", Operation: "ConsumeMessages", Method: http.MethodGet, URL: "https://1.mqrest.cn-hangzhou.aliyuncs.com/topics/t/messages", Body: map[string]any{"settlement": "release"}, ResponseFile: "x"},
		{Provider: ProviderAlicloud, Mode: ModeMutate, AuthScheme: authSchemeAlibabaMQ, Service: "rocketmq", Operation: "AcknowledgeMessage", Method: http.MethodDelete, URL: "https://1.mqrest.cn-hangzhou.aliyuncs.com/topics/t/messages", Body: map[string]any{"receipt_handle": "secret"}, ResponseFile: "x"},
		{Provider: ProviderAlicloud, Mode: ModeMutate, AuthScheme: authSchemeAlibabaMQ, Service: "rocketmq", Operation: "ConsumeMessages", Method: http.MethodGet, URL: "http://1.mqrest.cn-hangzhou.aliyuncs.com/topics/t/messages", Parameters: map[string]any{"consumer": "g", "numOfMessages": 1}, Body: map[string]any{"settlement": "release"}, ResponseFile: "x"},
		{Provider: ProviderAlicloud, Mode: ModeMutate, AuthScheme: authSchemeAlibabaMQ, Service: "rocketmq", Operation: "ConsumeMessages", Method: http.MethodGet, URL: "https://1.mqrest.cn-hangzhou.aliyuncs.com/topics/t/messages", Parameters: map[string]any{"consumer": "g", "numOfMessages": 17}, Body: map[string]any{"settlement": "release"}, ResponseFile: "x"},
		{Provider: ProviderAlicloud, Mode: ModeMutate, AuthScheme: authSchemeAlibabaMQ, Service: "rocketmq", Operation: "PublishMessage", Method: http.MethodPost, URL: "https://1.mqrest.cn-hangzhou.aliyuncs.com/topics/t/messages", BodyFile: "payload", ResponseFile: "x"},
		{Provider: ProviderAlicloud, Mode: ModeMutate, AuthScheme: authSchemeAlibabaMQ, Service: "rocketmq", Operation: "PublishMessage", Method: http.MethodPost, URL: "https://1.mqrest.cn-hangzhou.aliyuncs.com/topics/t/messages", Headers: map[string]string{"Authorization": "caller"}, Body: map[string]any{"message_body": "eA=="}, ResponseFile: "x"},
	}
	for index, invocation := range tests {
		if _, err := adapter.Invoke(t.Context(), invocation); err == nil {
			t.Fatalf("case %d accepted", index)
		}
	}
	if credentials.calls != 0 {
		t.Fatalf("credentials resolved %d times", credentials.calls)
	}
	existing := filepath.Join(t.TempDir(), "existing.ndjson")
	if err := os.WriteFile(existing, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAlicloud, Mode: ModeMutate, AuthScheme: authSchemeAlibabaMQ,
		Service: "rocketmq", Operation: "ConsumeMessages", Method: http.MethodGet,
		URL:        "https://1.mqrest.cn-hangzhou.aliyuncs.com/topics/t/messages",
		Parameters: map[string]any{"consumer": "g", "numOfMessages": 1},
		Body:       map[string]any{"settlement": "release"}, ResponseFile: existing,
	})
	if err == nil || credentials.calls != 0 {
		t.Fatalf("existing output err=%v credentials=%d", err, credentials.calls)
	}
}

func TestAlibabaMQIsAlwaysMutationClassified(t *testing.T) {
	for _, operation := range []string{"PublishMessage", "ConsumeMessages", "ConsumeOrderly", "ConsumeHalfMessages"} {
		if classifyRead(ProviderAlicloud, Invocation{AuthScheme: authSchemeAlibabaMQ, Service: "rocketmq", Operation: operation, Method: http.MethodGet}) {
			t.Fatalf("%s classified read", operation)
		}
	}
}

func TestAlibabaMQHalfMessageRollsBackInternally(t *testing.T) {
	var calls int
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			if request.URL.Query().Get("trans") != "pop" {
				t.Fatalf("consume query=%s", request.URL.RawQuery)
			}
			return httpResponse(200, `<Messages><Message><MessageId>half-1</MessageId><ReceiptHandle>half-secret</ReceiptHandle><MessageBody>eA==</MessageBody></Message></Messages>`), nil
		}
		body, _ := io.ReadAll(request.Body)
		if request.Method != http.MethodDelete || request.URL.Query().Get("trans") != "rollback" || request.URL.Query().Get("consumer") != "producer-group-a" || !strings.Contains(string(body), "half-secret") {
			t.Fatalf("rollback=%s %s body=%s", request.Method, request.URL.String(), body)
		}
		return httpResponse(204, ""), nil
	})
	target := filepath.Join(t.TempDir(), "half.ndjson")
	adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{Credentials: staticAlibabaCredentialsProvider{credentials: AlibabaCredentials{AccessKeyID: "ak", AccessKeySecret: "secret"}}, HTTP: doer})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAlicloud, Mode: ModeMutate, AuthScheme: authSchemeAlibabaMQ,
		Service: "rocketmq", Operation: "ConsumeHalfMessages", Method: http.MethodGet,
		URL:        "https://123456.mqrest.cn-hangzhou.aliyuncs.com/topics/orders/messages",
		Parameters: map[string]any{"consumer": "producer-group-a", "ns": "MQ_INST_1", "numOfMessages": 1},
		Body:       map[string]any{"transaction_outcome": "rollback"}, ResponseFile: target,
	})
	if err != nil || calls != 2 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	data, _ := os.ReadFile(target)
	if strings.Contains(string(data), "half-secret") || !strings.Contains(string(data), `"message_id":"half-1"`) {
		t.Fatalf("output=%s", data)
	}
}

func TestAlibabaMQRejectsInvalidPlansAndAcceptsPinnedEndpoint(t *testing.T) {
	invalidPlans := []any{
		map[string]any{"message_body": "eA==", "properties": map[string]any{"bad:key": "value"}},
		map[string]any{"message_body": "eA==", "properties": map[string]any{"ReceiptHandle": "value"}},
		map[string]any{"message_body": "eA==", "properties": map[string]any{"__TransCheckT": "30"}, "transaction_outcome": "commit"},
		map[string]any{"message_body": "eA==", "properties": map[string]any{"__TransCheckT": "9"}, "transaction_outcome": "commit", "producer_group": "g"},
		map[string]any{"message_body": string([]byte{0xff, 0xfe})},
	}
	for index, plan := range invalidPlans {
		if _, err := parseAlibabaMQPublishPlan(plan); err == nil {
			t.Fatalf("invalid plan %d accepted", index)
		}
	}
	invocation := Invocation{
		Provider: ProviderAlicloud, Mode: ModeMutate, AuthScheme: authSchemeAlibabaMQ,
		Service: "rocketmq", Operation: "ConsumeMessages", Method: http.MethodGet,
		URL:        "https://mq.private.example.com/topics/orders/messages",
		Parameters: map[string]any{"consumer": "group-a", "numOfMessages": 1},
		Body:       map[string]any{"settlement": "release"}, ResponseFile: "output",
	}
	if _, _, err := validateAlibabaMQInvocation(invocation, []string{"mq.private.example.com"}); err != nil {
		t.Fatal(err)
	}
	invocation.URL = "https://mq.private.example.com/topics/a%2Fb/messages"
	if _, _, err := validateAlibabaMQInvocation(invocation, []string{"mq.private.example.com"}); err == nil {
		t.Fatal("accepted an escaped topic path separator")
	}
	invocation.URL = "https://mq.private.example.com/topics/orders/messages/"
	if _, _, err := validateAlibabaMQInvocation(invocation, []string{"mq.private.example.com"}); err == nil {
		t.Fatal("accepted a non-exact topic path")
	}
}

func TestAlibabaMQPolicyValidatesPlanBeforeApproval(t *testing.T) {
	target := filepath.Join(t.TempDir(), "messages.ndjson")
	invocation := Invocation{
		Provider: ProviderAlicloud, Mode: ModeMutate, AuthScheme: authSchemeAlibabaMQ,
		Service: "rocketmq", Operation: "ConsumeMessages", Method: http.MethodGet,
		URL:        "https://123456.mqrest.cn-hangzhou.aliyuncs.com/topics/orders/messages",
		Parameters: map[string]any{"consumer": "group-a", "numOfMessages": 1},
		Body:       map[string]any{"settlement": "caller-handle"}, ResponseFile: target,
	}
	if err := validateInvocationWithEndpointHosts(invocation, []string{filepath.Dir(target)}, nil); err == nil || !strings.Contains(err.Error(), "settlement") {
		t.Fatalf("err=%v", err)
	}
}

func TestAlibabaMQErrorNeverEchoesReceiptHandle(t *testing.T) {
	err := alibabaMQResponseError(400, []byte(`<Errors><Error><ErrorCode>AckFail</ErrorCode><ErrorMessage>bad secret-handle</ErrorMessage><ReceiptHandle>secret-handle</ReceiptHandle></Error></Errors>`), "request-1")
	if !strings.Contains(err.Error(), "AckFail") || strings.Contains(err.Error(), "secret-handle") {
		t.Fatalf("unsafe error=%v", err)
	}
}

func TestAlibabaMQCapturesMQAndMQSRequestIDHeaders(t *testing.T) {
	for name, value := range map[string]string{"x-mq-request-id": "mq-request", "x-mqs-request-id": "mqs-request"} {
		headers := make(http.Header)
		headers.Set(name, value)
		if got := alibabaMQRequestID(headers); got != value {
			t.Fatalf("header=%s got=%q", name, got)
		}
	}
}

func TestAlibabaMQValidatesOutputBeforeAcknowledging(t *testing.T) {
	var calls int
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		return httpResponse(200, `<Messages><Message><MessageId>m-1</MessageId><ReceiptHandle>secret-handle</ReceiptHandle><MessageBody>`+strings.Repeat("x", 128)+`</MessageBody></Message></Messages>`), nil
	})
	target := filepath.Join(t.TempDir(), "too-small.ndjson")
	adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{Credentials: staticAlibabaCredentialsProvider{credentials: AlibabaCredentials{AccessKeyID: "ak", AccessKeySecret: "secret"}}, HTTP: doer})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAlicloud, Mode: ModeMutate, AuthScheme: authSchemeAlibabaMQ,
		Service: "rocketmq", Operation: "ConsumeMessages", Method: http.MethodGet,
		URL:        "https://123456.mqrest.cn-hangzhou.aliyuncs.com/topics/orders/messages",
		Parameters: map[string]any{"consumer": "group-a", "numOfMessages": 1},
		Body:       map[string]any{"settlement": "acknowledge"}, ResponseFile: target, MaxResponseFileBytes: 32,
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds") || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("response file leaked: %v", statErr)
	}
}

func TestAlibabaMQRejectsProviderMessageOverflowAndDuplicates(t *testing.T) {
	responses := []string{
		`<Messages><Message><MessageId>m-1</MessageId><ReceiptHandle>h-1</ReceiptHandle><MessageBody>eA==</MessageBody></Message><Message><MessageId>m-2</MessageId><ReceiptHandle>h-2</ReceiptHandle><MessageBody>eA==</MessageBody></Message></Messages>`,
		`<Messages><Message><MessageId>m-1</MessageId><ReceiptHandle>h-1</ReceiptHandle><MessageBody>eA==</MessageBody></Message><Message><MessageId>m-2</MessageId><ReceiptHandle>h-1</ReceiptHandle><MessageBody>eA==</MessageBody></Message></Messages>`,
	}
	for index, responseBody := range responses {
		var calls int
		adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{
			Credentials: staticAlibabaCredentialsProvider{credentials: AlibabaCredentials{AccessKeyID: "ak", AccessKeySecret: "secret"}},
			HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return httpResponse(200, responseBody), nil
			}),
		})
		count := 1
		if index == 1 {
			count = 2
		}
		target := filepath.Join(t.TempDir(), "invalid.ndjson")
		_, err := adapter.Invoke(t.Context(), Invocation{
			Provider: ProviderAlicloud, Mode: ModeMutate, AuthScheme: authSchemeAlibabaMQ,
			Service: "rocketmq", Operation: "ConsumeMessages", Method: http.MethodGet,
			URL:        "https://123456.mqrest.cn-hangzhou.aliyuncs.com/topics/orders/messages",
			Parameters: map[string]any{"consumer": "group-a", "numOfMessages": count},
			Body:       map[string]any{"settlement": "acknowledge"}, ResponseFile: target,
		})
		if err == nil || calls != 1 {
			t.Fatalf("case=%d calls=%d err=%v", index, calls, err)
		}
		if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
			t.Fatalf("case=%d response file leaked: %v", index, statErr)
		}
	}
}

func FuzzAlibabaMQResponsePropertySanitization(f *testing.F) {
	f.Add("KEYS:key-1|custom:value|")
	f.Add("ReceiptHandle:secret|security-token:token|safe:value|")
	f.Fuzz(func(t *testing.T, properties string) {
		message := sanitizeAlibabaMQMessage(alibabaMQProviderMessage{MessageID: "m-1", MessageBody: "eA==", Properties: properties})
		encoded, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		for name := range message.Properties {
			if isAlibabaMQReservedProperty(name) {
				t.Fatalf("unsafe output=%s", encoded)
			}
		}
	})
}

package cloud

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/smithy-go/eventstream"
)

type ecdsaSignaturePair struct {
	R *big.Int
	S *big.Int
}

func TestSignedHTTPAdaptersExposeHTTPOnlyStatusAndOfficialDiscovery(t *testing.T) {
	adapters := []struct {
		name    string
		adapter Adapter
		marker  string
	}{
		{name: "aws", adapter: NewAWSRESTAdapter(AWSRESTConfig{}), marker: "authentication"},
		{name: "alibaba", adapter: NewAlibabaRESTAdapter(AlibabaRESTConfig{}), marker: "acs3_signature"},
		{name: "tencent", adapter: NewTencentRESTAdapter(TencentRESTConfig{}), marker: "tc3_signature"},
	}
	for _, test := range adapters {
		t.Run(test.name, func(t *testing.T) {
			status, err := test.adapter.Status(t.Context())
			if err != nil || !status.Available || !strings.Contains(strings.ToLower(status.Adapter), "https") || !strings.Contains(strings.ToLower(status.Message), "no cloud cli") {
				t.Fatalf("status=%#v err=%v", status, err)
			}
			output, err := test.adapter.Discover(t.Context(), DiscoveryRequest{})
			if err != nil {
				t.Fatal(err)
			}
			var discovery map[string]string
			if err := json.Unmarshal(output, &discovery); err != nil || discovery[test.marker] == "" {
				t.Fatalf("discovery=%s err=%v", output, err)
			}
		})
	}
}

func TestDefaultSignedCredentialProvidersUseEnvironmentWithoutCloudCLI(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "aws-environment-ak")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "aws-environment-secret")
	t.Setenv("AWS_SESSION_TOKEN", "aws-environment-token")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	awsCredentials, err := (&awsDefaultCredentialProvider{}).Credentials(t.Context())
	if err != nil || awsCredentials.AccessKeyID != "aws-environment-ak" || awsCredentials.SessionToken != "aws-environment-token" {
		t.Fatalf("AWS credentials=%#v err=%v", awsCredentials, err)
	}

	t.Setenv("ALIBABA_CLOUD_ACCESS_KEY_ID", "alibaba-environment-ak")
	t.Setenv("ALIBABA_CLOUD_ACCESS_KEY_SECRET", "alibaba-environment-secret")
	t.Setenv("ALIBABA_CLOUD_SECURITY_TOKEN", "alibaba-environment-token")
	alibabaCredentials, err := (&alibabaDefaultCredentialProvider{}).Credentials(t.Context())
	if err != nil || alibabaCredentials.AccessKeyID != "alibaba-environment-ak" || alibabaCredentials.SecurityToken != "alibaba-environment-token" {
		t.Fatalf("Alibaba credentials=%#v err=%v", alibabaCredentials, err)
	}

	t.Setenv("TENCENTCLOUD_SECRET_ID", "tencent-environment-id")
	t.Setenv("TENCENTCLOUD_SECRET_KEY", "tencent-environment-key")
	t.Setenv("TENCENTCLOUD_SESSION_TOKEN", "tencent-environment-token")
	tencentCredentials, err := (EnvTencentCredentialProvider{}).Credentials(t.Context())
	if err != nil || tencentCredentials.SecretID != "tencent-environment-id" || tencentCredentials.Token != "tencent-environment-token" {
		t.Fatalf("Tencent credentials=%#v err=%v", tencentCredentials, err)
	}
}

func TestSignedHTTPHelpersCoverNoncePointersAndQueryTypes(t *testing.T) {
	nonce := secureNonce()
	if len(nonce) != 32 {
		t.Fatalf("nonce length=%d", len(nonce))
	}
	value := "value"
	if pointerString(nil) != "" || pointerString(&value) != value {
		t.Fatal("pointerString mismatch")
	}
	target, _ := url.Parse("https://service.example/path?existing=1")
	if err := addQueryParameters(target, map[string]any{
		"string": "text", "bool": true, "number": float64(2.5), "int": 3,
		"strings": []string{"a", "b"}, "values": []any{"x", false},
	}); err != nil {
		t.Fatal(err)
	}
	query := target.Query()
	if query.Get("existing") != "1" || query.Get("number") != "2.5" || len(query["strings"]) != 2 || len(query["values"]) != 2 {
		t.Fatalf("query=%v", query)
	}
	for _, parameters := range []map[string]any{{"": "bad"}, {"bad": map[string]any{"x": 1}}, {"bad": []any{[]any{"nested"}}}} {
		if err := addQueryParameters(target, parameters); err == nil {
			t.Fatalf("accepted invalid query=%#v", parameters)
		}
	}
}

type staticAWSCredentialsProvider struct {
	credentials AWSCredentials
}

func (provider staticAWSCredentialsProvider) Credentials(context.Context) (AWSCredentials, error) {
	return provider.credentials, nil
}

type staticAlibabaCredentialsProvider struct {
	credentials AlibabaCredentials
}

func (provider staticAlibabaCredentialsProvider) Credentials(context.Context) (AlibabaCredentials, error) {
	return provider.credentials, nil
}

type staticTencentCredentialsProvider struct {
	credentials TencentCredentials
}

func (provider staticTencentCredentialsProvider) Credentials(context.Context) (TencentCredentials, error) {
	return provider.credentials, nil
}

func TestDefaultAdaptersUseOnlyHTTPImplementations(t *testing.T) {
	adapters := DefaultAdapters()
	want := map[Provider]string{
		ProviderAWS:      "*cloud.AWSRESTAdapter",
		ProviderAzure:    "*cloud.AzureRESTAdapter",
		ProviderGCP:      "*cloud.GCPRESTAdapter",
		ProviderAlicloud: "*cloud.AlibabaRESTAdapter",
		ProviderTencent:  "*cloud.TencentRESTAdapter",
		ProviderBaidu:    "*cloud.BaiduRESTAdapter",
	}
	for provider, adapter := range adapters {
		if got := fmt.Sprintf("%T", adapter); got != want[provider] {
			t.Errorf("provider %s adapter=%s, want %s", provider, got, want[provider])
		}
	}
}

func TestAWSSigV4AdapterSignsAndSendsHTTP(t *testing.T) {
	now := time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC)
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(request.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/") {
			t.Fatalf("authorization=%q", request.Header.Get("Authorization"))
		}
		if request.Header.Get("X-Amz-Date") != "20150830T123600Z" {
			t.Fatalf("x-amz-date=%q", request.Header.Get("X-Amz-Date"))
		}
		if request.Header.Get("X-Amz-Security-Token") != "session-token" {
			t.Fatalf("session token missing")
		}
		return httpResponse(200, `{"Reservations":[]}`), nil
	})
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{
			AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret", SessionToken: "session-token",
		}},
		HTTP: doer,
		Now:  func() time.Time { return now },
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, AuthScheme: "sigv4", Method: "POST",
		URL: "https://ec2.us-east-1.amazonaws.com/", Service: "ec2", Region: "us-east-1",
		Headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
		Body:    "Action=DescribeInstances&Version=2016-11-15",
	})
	if err != nil || string(result.Output) != `{"Reservations":[]}` {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestAWSSigV4aDerivesOfficialAWSKeyVector(t *testing.T) {
	privateKey, err := deriveAWSSigV4aKey("AKISORANDOMAASORANDOM", "q+jcrXGc+0zWN6uzclKVhvMmUsIfRPa4rlRandom")
	if err != nil {
		t.Fatal(err)
	}
	wantX, _ := new(big.Int).SetString("15D242CEEBF8D8169FD6A8B5A746C41140414C3B07579038DA06AF89190FFFCB", 16)
	wantY, _ := new(big.Int).SetString("515242CEDD82E94799482E4C0514B505AFCCF2C0C98D6A553BF539F424C5EC0", 16)
	if privateKey.X.Cmp(wantX) != 0 || privateKey.Y.Cmp(wantY) != 0 {
		t.Fatalf("public key=(%X,%X)", privateKey.X, privateKey.Y)
	}
}

func TestAWSSigV4aMatchesOfficialAWSSDKSigningVector(t *testing.T) {
	request, err := http.NewRequest(http.MethodPost, "https://dynamodb.us-east-1.amazonaws.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.URL.Opaque = "//example.org/bucket/key-._~,!@%23$%25^&*()"
	request.Header.Set("X-Amz-Target", "prefix.Operation")
	request.Header.Set("Content-Type", "application/x-amz-json-1.0")
	request.Header.Set("Content-Length", "1024")
	request.Header.Set("X-Amz-Meta-Other-Header", "some-value=!@#$%^&* (+)")
	request.Header.Add("X-Amz-Meta-Other-Header_With_Underscore", "some-value=!@#$%^&* (+)")
	request.Header.Add("X-amz-Meta-Other-Header_With_Underscore", "some-value=!@#$%^&* (+)")
	request.Header.Set("User-Agent", "ignored")
	request.Header.Set("X-Amzn-Trace-Id", "ignored")
	request.Header.Set("Transfer-Encoding", "ignored")

	stringToSign, err := signAWSSigV4a(request, sha256Hex(nil), AWSCredentials{
		AccessKeyID: "AKISORANDOMAASORANDOM", SecretAccessKey: "q+jcrXGc+0zWN6uzclKVhvMmUsIfRPa4rlRandom", SessionToken: "TOKEN",
	}, "dynamodb", []string{"us-east-1"}, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	// Published by the AWS SDK for Go v2 SigV4a signer test suite.
	if got, want := sha256Hex([]byte(stringToSign)), "4ba7d0482cf4d5450cefdc067a00de1a4a715e444856fa3e1d85c35fb34d9730"; got != want {
		t.Fatalf("string-to-sign hash=%s want=%s\n%s", got, want, stringToSign)
	}
}

func TestAWSSigV4aSignerProducesVerifiableMultiRegionHeader(t *testing.T) {
	now := time.Date(2022, 8, 30, 12, 36, 0, 0, time.UTC)
	credentials := AWSCredentials{AccessKeyID: "AKISORANDOMAASORANDOM", SecretAccessKey: "q+jcrXGc+0zWN6uzclKVhvMmUsIfRPa4rlRandom", SessionToken: "session-token"}
	request, err := http.NewRequest(http.MethodGet, "https://mrap.accesspoint.s3-global.amazonaws.com/object", nil)
	if err != nil {
		t.Fatal(err)
	}
	stringToSign, err := signAWSSigV4a(request, sha256Hex(nil), credentials, "s3", []string{"us-east-1", "us-west-*"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if request.Header.Get("X-Amz-Region-Set") != "us-east-1,us-west-*" || request.Header.Get("X-Amz-Date") != "20220830T123600Z" {
		t.Fatalf("sigv4a headers=%v", request.Header)
	}
	authorization := request.Header.Get("Authorization")
	prefix := "AWS4-ECDSA-P256-SHA256 Credential=AKISORANDOMAASORANDOM/20220830/s3/aws4_request, SignedHeaders="
	if !strings.HasPrefix(authorization, prefix) || !strings.Contains(authorization, "x-amz-region-set") {
		t.Fatalf("authorization=%q", authorization)
	}
	parts := strings.Split(authorization, "Signature=")
	if len(parts) != 2 {
		t.Fatalf("authorization signature=%q", authorization)
	}
	signatureBytes, err := hex.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var signature ecdsaSignaturePair
	if _, err := asn1.Unmarshal(signatureBytes, &signature); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(stringToSign))
	privateKey, err := deriveAWSSigV4aKey(credentials.AccessKeyID, credentials.SecretAccessKey)
	if err != nil || !ecdsa.Verify(&privateKey.PublicKey, digest[:], signature.R, signature.S) {
		t.Fatalf("signature verification failed: %v", err)
	}
}

func TestAWSSigV4aChunkSigningMatchesOfficialAWSSDKVectorShape(t *testing.T) {
	privateKey, err := deriveAWSSigV4aKey("AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY")
	if err != nil {
		t.Fatal(err)
	}
	wantX, _ := new(big.Int).SetString("18b7d04643359f6ec270dcbab8dce6d169d66ddc9778c75cfb08dfdb701637ab", 16)
	wantY, _ := new(big.Int).SetString("fa36b35e4fe67e3112261d2e17a956ef85b06e44712d2850bcd3c2161e9993f2", 16)
	if privateKey.X.Cmp(wantX) != 0 || privateKey.Y.Cmp(wantY) != 0 {
		t.Fatalf("official streaming public key=(%X,%X)", privateKey.X, privateKey.Y)
	}
	previous := "30440220010203040506070809000102030405060708090001020304050607080900010202200102030405060708090001020304050607080900010203040506070809000102"
	amzDate := "20130524T000000Z"
	scope := "20130524/s3/aws4_request"
	chunk := bytes.Repeat([]byte("a"), 64*1024)
	signature, err := awsSigV4aChunkSignature(privateKey, previous, amzDate, scope, chunk)
	if err != nil {
		t.Fatal(err)
	}
	if len(signature) != awsSigV4aStreamingSignatureLength {
		t.Fatalf("padded signature length=%d", len(signature))
	}
	encodedSignature := strings.TrimRight(signature, "*")
	der, err := hex.DecodeString(encodedSignature)
	if err != nil {
		t.Fatal(err)
	}
	stringToSign := strings.Join([]string{
		awsSigV4aChunkAlgorithm, amzDate, scope, previous, sha256Hex(nil), sha256Hex(chunk),
	}, "\n")
	digest := sha256.Sum256([]byte(stringToSign))
	if !ecdsa.VerifyASN1(&privateKey.PublicKey, digest[:], der) {
		t.Fatal("SigV4a chunk signature does not verify")
	}
	checksumLine := "x-amz-checksum-crc32c:sOO8/Q==\n"
	trailerSignature, err := awsSigV4aTrailerSignature(privateKey, signature, amzDate, scope, checksumLine)
	if err != nil {
		t.Fatal(err)
	}
	trailerDER, err := hex.DecodeString(strings.TrimRight(trailerSignature, "*"))
	if err != nil {
		t.Fatal(err)
	}
	trailerStringToSign := strings.Join([]string{
		awsSigV4aTrailerAlgorithm, amzDate, scope, strings.TrimRight(signature, "*"), sha256Hex([]byte(checksumLine)),
	}, "\n")
	trailerDigest := sha256.Sum256([]byte(trailerStringToSign))
	if len(trailerSignature) != awsSigV4aStreamingSignatureLength || !ecdsa.VerifyASN1(&privateKey.PublicKey, trailerDigest[:], trailerDER) {
		t.Fatal("SigV4a trailer signature does not verify")
	}
}

func TestAWSSigV4aChunkedConfigurationUsesFixedSignatureLength(t *testing.T) {
	request, err := http.NewRequest(http.MethodPut, "https://s3.amazonaws.com/examplebucket/chunkObject.txt", strings.NewReader(strings.Repeat("a", 65*1024)))
	if err != nil {
		t.Fatal(err)
	}
	if err := configureAWSSigV4aChunkedRequest(request, defaultAWSChunkSize); err != nil {
		t.Fatal(err)
	}
	if request.ContentLength != 67064 || request.Header.Get("Content-Length") != "67064" {
		t.Fatalf("SigV4a encoded length=%d header=%q want=67064", request.ContentLength, request.Header.Get("Content-Length"))
	}
	if request.Header.Get("X-Amz-Content-Sha256") != awsSigV4aStreamingPayload {
		t.Fatalf("payload marker=%q", request.Header.Get("X-Amz-Content-Sha256"))
	}
}

func TestAWSSigV4aChunkedTrailerAdapterSignsStreamingChecksum(t *testing.T) {
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if request.Header.Get("X-Amz-Content-Sha256") != awsSigV4aStreamingPayloadTrailer || request.Header.Get("X-Amz-Trailer") != "x-amz-checksum-crc64nvme" {
			t.Fatalf("SigV4a trailer headers=%v", request.Header)
		}
		if !bytes.Contains(body, []byte("x-amz-checksum-crc64nvme:")) {
			t.Fatalf("SigV4a trailer checksum missing: %q", body)
		}
		if int64(len(body)) != request.ContentLength {
			t.Fatalf("SigV4a trailer wire length=%d header=%d", len(body), request.ContentLength)
		}
		for _, line := range bytes.Split(body, []byte("\r\n")) {
			if bytes.Contains(line, []byte("signature=")) || bytes.HasPrefix(line, []byte("x-amz-trailer-signature:")) {
				value := line[strings.LastIndex(string(line), ":")+1:]
				if bytes.Contains(line, []byte("chunk-signature=")) {
					value = line[strings.LastIndex(string(line), "=")+1:]
				}
				if len(value) != awsSigV4aStreamingSignatureLength {
					t.Fatalf("SigV4a wire signature length=%d line=%q", len(value), line)
				}
			}
		}
		return httpResponse(200, `{"ok":true}`), nil
	})
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIAIOSFODNN7EXAMPLE", SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}}, HTTP: doer,
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, AuthScheme: "sigv4a", RegionSet: "us-east-1", PayloadMode: "aws-chunked-trailer", ChecksumAlgorithm: "crc64nvme", Method: "PUT",
		URL: "https://bucket.s3.us-east-1.amazonaws.com/object", Service: "s3", Operation: "put-object", Body: "payload",
	})
	if err != nil || string(result.Output) != `{"ok":true}` {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestAWSSigV4aAdapterSignsAndSendsHTTP(t *testing.T) {
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(request.Header.Get("Authorization"), "AWS4-ECDSA-P256-SHA256 Credential=AKIDEXAMPLE/") {
			t.Fatalf("authorization=%q", request.Header.Get("Authorization"))
		}
		return httpResponse(200, `{"ok":true}`), nil
	})
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret"}}, HTTP: doer,
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, AuthScheme: "sigv4a", Method: "GET", URL: "https://mrap.accesspoint.s3-global.amazonaws.com/object",
		Service: "s3", Operation: "get-object", RegionSet: "us-east-1,us-west-*",
	})
	if err != nil || string(result.Output) != `{"ok":true}` {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestAWSSigV4aPolicyValidatesSchemeRegionSetAndProtectedHeaders(t *testing.T) {
	valid := Invocation{
		Provider: ProviderAWS, AuthScheme: "sigv4a", Service: "s3", Operation: "get-object",
		Method: "GET", URL: "https://mrap.accesspoint.s3-global.amazonaws.com/object", RegionSet: "us-east-1, us-west-*",
	}
	if err := validateInvocation(valid, nil); err != nil {
		t.Fatalf("valid SigV4a request rejected: %v", err)
	}
	for _, mutate := range []func(*Invocation){
		func(request *Invocation) { request.RegionSet = "" },
		func(request *Invocation) { request.RegionSet = "us-east-1,../../bad" },
		func(request *Invocation) { request.Headers = map[string]string{"X-Amz-Region-Set": "*"} },
		func(request *Invocation) { request.Headers = map[string]string{"X-Amz-Content-Sha256": sha256Hex(nil)} },
	} {
		request := valid
		mutate(&request)
		if err := validateInvocation(request, nil); err == nil {
			t.Fatalf("unsafe SigV4a request accepted: %#v", request)
		}
	}
	valid.AuthScheme = "sigv4"
	valid.Region = "us-east-1"
	if err := validateInvocation(valid, nil); err == nil || !strings.Contains(err.Error(), "region_set") {
		t.Fatalf("SigV4 region_set error=%v", err)
	}
	valid.Provider = ProviderAzure
	valid.AuthScheme = ""
	valid.RegionSet = ""
	valid.Region = ""
	valid.Service = "management"
	valid.Operation = "get-resource"
	valid.URL = "https://management.azure.com/subscriptions/example?api-version=2024-01-01"
	if err := validateInvocation(valid, nil); err != nil {
		t.Fatalf("Azure baseline rejected: %v", err)
	}
	valid.RegionSet = "*"
	if err := validateInvocation(valid, nil); err == nil || !strings.Contains(err.Error(), "region_set") {
		t.Fatalf("non-AWS region_set error=%v", err)
	}
}

func TestAWSSigV4ChunkedMatchesOfficialS3Vector(t *testing.T) {
	now := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)
	request, err := http.NewRequest(http.MethodPut, "https://s3.amazonaws.com/examplebucket/chunkObject.txt", strings.NewReader(strings.Repeat("a", 65*1024)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Amz-Storage-Class", "REDUCED_REDUNDANCY")
	if err := configureAWSSigV4ChunkedRequest(request, defaultAWSChunkSize); err != nil {
		t.Fatal(err)
	}
	credentials := aws.Credentials{AccessKeyID: "AKIAIOSFODNN7EXAMPLE", SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}
	if err := awsv4.NewSigner().SignHTTP(t.Context(), credentials, request, awsSigV4StreamingPayload, "s3", "us-east-1", now); err != nil {
		t.Fatal(err)
	}
	seed, err := awsSeedSignature(request.Header.Get("Authorization"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := hex.EncodeToString(seed), "4f232c4386841ef735655705268965c44a0e4690baa4adea153f7db9fa80a0a9"; got != want {
		t.Fatalf("seed signature=%s want=%s", got, want)
	}
	request.Body = newAWSSigV4ChunkedReader(request.Body, 65*1024, credentials, "s3", "us-east-1", now, seed, defaultAWSChunkSize)
	encoded, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, signature := range []string{
		"ad80c730a21e5b8d04586a2213dd63b9a0e99e0e2307b0ade35a65485a288648",
		"0055627c9e194cb4542bae2aa5492e3c1575bbb81b612b7d234b86a503ef5497",
		"b6c6ea8a5354eaf15b3cb7646744f4275b71ea724fed81ceb9323e279d449df9",
	} {
		if !bytes.Contains(encoded, []byte("chunk-signature="+signature)) {
			t.Fatalf("official chunk signature %s missing", signature)
		}
	}
	if got, want := int64(len(encoded)), request.ContentLength; got != want || got != 66824 {
		t.Fatalf("encoded length=%d request length=%d want=66824", got, request.ContentLength)
	}
}

func TestAWSSigV4ChunkedAdapterSignsAndStreamsBody(t *testing.T) {
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if request.Header.Get("X-Amz-Content-Sha256") != awsSigV4StreamingPayload || request.Header.Get("Content-Encoding") != "aws-chunked" {
			t.Fatalf("streaming headers=%v", request.Header)
		}
		if !bytes.Contains(body, []byte("chunk-signature=")) || !bytes.HasSuffix(body, []byte("\r\n")) {
			t.Fatalf("encoded body=%q", body)
		}
		return httpResponse(200, `{"ok":true}`), nil
	})
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret"}}, HTTP: doer,
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, AuthScheme: "sigv4", PayloadMode: "aws-chunked", Method: "PUT",
		URL: "https://bucket.s3.us-east-1.amazonaws.com/object", Service: "s3", Operation: "put-object", Region: "us-east-1", Body: "payload",
	})
	if err != nil || string(result.Output) != `{"ok":true}` {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestAWSSigV4ChunkedPolicyRejectsUnsafeCombinationsAndHeaders(t *testing.T) {
	valid := Invocation{
		Provider: ProviderAWS, AuthScheme: "sigv4", PayloadMode: "aws-chunked", Service: "s3", Operation: "put-object",
		Method: "PUT", URL: "https://bucket.s3.us-east-1.amazonaws.com/object", Region: "us-east-1", Body: "payload",
	}
	if err := validateInvocation(valid, nil); err != nil {
		t.Fatalf("valid aws-chunked request rejected: %v", err)
	}
	for _, mutate := range []func(*Invocation){
		func(request *Invocation) { request.AuthScheme = "sigv4a" },
		func(request *Invocation) { request.Service = "ec2" },
		func(request *Invocation) { request.Method = "POST" },
		func(request *Invocation) { request.Body = nil },
		func(request *Invocation) { request.Headers = map[string]string{"Content-Length": "1"} },
		func(request *Invocation) { request.Headers = map[string]string{"X-Amz-Decoded-Content-Length": "1"} },
		func(request *Invocation) { request.Headers = map[string]string{"Transfer-Encoding": "chunked"} },
	} {
		request := valid
		mutate(&request)
		if err := validateInvocation(request, nil); err == nil {
			t.Fatalf("unsafe aws-chunked request accepted: %#v", request)
		}
	}
	valid.Provider = ProviderGCP
	valid.AuthScheme = ""
	valid.Service = ""
	valid.Region = ""
	valid.URL = "https://storage.googleapis.com/upload/storage/v1/b/b/o"
	if err := validateInvocation(valid, nil); err == nil || !strings.Contains(err.Error(), "payload_mode") {
		t.Fatalf("non-AWS payload_mode error=%v", err)
	}
}

func TestAWSSigV4ChunkedHelpersRejectMalformedInputAndPreserveEncoding(t *testing.T) {
	request, err := http.NewRequest(http.MethodPut, "https://s3.amazonaws.com/bucket/key", strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Encoding", "gzip")
	if err := configureAWSSigV4ChunkedRequest(request, defaultAWSChunkSize); err != nil {
		t.Fatal(err)
	}
	if got := request.Header.Get("Content-Encoding"); got != "aws-chunked,gzip" {
		t.Fatalf("content encoding=%q", got)
	}
	if _, err := awsSeedSignature("AWS4-HMAC-SHA256 Signature=not-hex"); err == nil {
		t.Fatal("accepted malformed seed signature")
	}
	if _, err := awsChunkedEncodedLength(1, 1024); err == nil {
		t.Fatal("accepted undersized chunks")
	}
	empty, err := awsChunkedEncodedLength(0, defaultAWSChunkSize)
	if err != nil || empty != 86 {
		t.Fatalf("empty encoded length=%d err=%v", empty, err)
	}
}

func TestAWSSigV4ChunkedTrailerMatchesOfficialS3Vector(t *testing.T) {
	now := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)
	request, err := http.NewRequest(http.MethodPut, "https://s3.amazonaws.com/examplebucket/chunkObject.txt", strings.NewReader(strings.Repeat("a", 65*1024)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Amz-Storage-Class", "REDUCED_REDUNDANCY")
	encodedLength, checksum, err := configureAWSSigV4ChunkedTrailerRequest(request, defaultAWSChunkSize, "crc32c")
	if err != nil {
		t.Fatal(err)
	}
	credentials := aws.Credentials{AccessKeyID: "AKIAIOSFODNN7EXAMPLE", SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}
	if err := awsv4.NewSigner().SignHTTP(t.Context(), credentials, request, awsSigV4StreamingPayloadTrailer, "s3", "us-east-1", now); err != nil {
		t.Fatal(err)
	}
	seed, err := awsSeedSignature(request.Header.Get("Authorization"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := hex.EncodeToString(seed), "106e2a8a18243abcf37539882f36619c00e2dfc72633413f02d3b74544bfeb8e"; got != want {
		t.Fatalf("seed signature=%s want=%s authorization=%s", got, want, request.Header.Get("Authorization"))
	}
	request.ContentLength = encodedLength
	request.Header.Set("Content-Length", fmt.Sprintf("%d", encodedLength))
	request.Body = newAWSSigV4ChunkedTrailerReader(request.Body, 65*1024, credentials, "s3", "us-east-1", now, seed, defaultAWSChunkSize, checksum)
	encoded, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, signature := range []string{
		"b474d8862b1487a5145d686f57f013e54db672cee1c953b3010fb58501ef5aa2",
		"1c1344b170168f8e65b41376b44b20fe354e373826ccbbe2c1d40a8cae51e5c7",
		"2ca2aba2005185cf7159c6277faf83795951dd77a3a99e6e65d5c9f85863f992",
		"d81f82fc3505edab99d459891051a732e8730629a2e4a59689829ca17fe2e435",
	} {
		if !bytes.Contains(encoded, []byte(signature)) {
			t.Fatalf("official trailer vector signature %s missing", signature)
		}
	}
	if !bytes.Contains(encoded, []byte("x-amz-checksum-crc32c:sOO8/Q==\r\n")) {
		t.Fatalf("official CRC32C trailer missing: %q", encoded[len(encoded)-160:])
	}
	if got := int64(len(encoded)); got != encodedLength || got != 66946 {
		t.Fatalf("encoded length=%d configured=%d want=66946", got, encodedLength)
	}
}

func TestAWSSigV4ChunkedTrailerAdapterComputesChecksum(t *testing.T) {
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if request.Header.Get("X-Amz-Content-Sha256") != awsSigV4StreamingPayloadTrailer || request.Header.Get("X-Amz-Trailer") != "x-amz-checksum-sha256" {
			t.Fatalf("trailer headers=%v", request.Header)
		}
		if !bytes.Contains(body, []byte("x-amz-checksum-sha256:")) || !bytes.Contains(body, []byte("x-amz-trailer-signature:")) {
			t.Fatalf("trailer body=%q", body)
		}
		return httpResponse(200, `{"ok":true}`), nil
	})
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret"}}, HTTP: doer,
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, AuthScheme: "sigv4", PayloadMode: "aws-chunked-trailer", ChecksumAlgorithm: "sha256", Method: "PUT",
		URL: "https://bucket.s3.us-east-1.amazonaws.com/object", Service: "s3", Operation: "put-object", Region: "us-east-1", Body: "payload",
	})
	if err != nil || string(result.Output) != `{"ok":true}` {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestAWSTrailerChecksumMatchesOfficialCRC64NVMEVector(t *testing.T) {
	checksum, err := newAWSTrailerChecksum("crc64nvme")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checksum.hash.Write([]byte("123456789")); err != nil {
		t.Fatal(err)
	}
	if got, want := base64.StdEncoding.EncodeToString(checksum.hash.Sum(nil)), "rosUhgp5mIg="; got != want {
		t.Fatalf("CRC64NVME checksum=%s want=%s", got, want)
	}
	if checksum.header != "x-amz-checksum-crc64nvme" {
		t.Fatalf("CRC64NVME header=%s", checksum.header)
	}
}

func TestAWSSigV4ChunkedTrailerPolicyRequiresSupportedChecksum(t *testing.T) {
	valid := Invocation{
		Provider: ProviderAWS, AuthScheme: "sigv4", PayloadMode: "aws-chunked-trailer", ChecksumAlgorithm: "crc64nvme",
		Service: "s3", Operation: "put-object", Method: "PUT", URL: "https://bucket.s3.us-east-1.amazonaws.com/object", Region: "us-east-1", Body: "payload",
	}
	if err := validateInvocation(valid, nil); err != nil {
		t.Fatalf("valid aws-chunked-trailer request rejected: %v", err)
	}
	for _, mutate := range []func(*Invocation){
		func(request *Invocation) { request.ChecksumAlgorithm = "" },
		func(request *Invocation) { request.ChecksumAlgorithm = "md5" },
		func(request *Invocation) {
			request.Headers = map[string]string{"X-Amz-Checksum-CRC64NVME": "caller-value"}
		},
		func(request *Invocation) {
			request.Headers = map[string]string{"X-Amz-Trailer-Signature": strings.Repeat("0", 64)}
		},
		func(request *Invocation) { request.PayloadMode = "aws-chunked" },
		func(request *Invocation) { request.AuthScheme = "sigv4a" },
	} {
		request := valid
		mutate(&request)
		if err := validateInvocation(request, nil); err == nil {
			t.Fatalf("unsafe trailer request accepted: %#v", request)
		}
	}
}

func TestAWSSigV4EventStreamAdapterSignsFramesAndTerminalMessage(t *testing.T) {
	innerMessage := eventstream.Message{
		Headers: eventstream.Headers{
			{Name: eventstream.MessageTypeHeader, Value: eventstream.StringValue(eventstream.EventMessageType)},
			{Name: eventstream.EventTypeHeader, Value: eventstream.StringValue("AudioEvent")},
			{Name: eventstream.ContentTypeHeader, Value: eventstream.StringValue("application/octet-stream")},
		},
		Payload: []byte("audio-frame"),
	}
	var unsigned bytes.Buffer
	if err := eventstream.NewEncoder().Encode(&unsigned, innerMessage); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	bodyFile := filepath.Join(root, "events.bin")
	if err := os.WriteFile(bodyFile, unsigned.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("X-Amz-Content-Sha256") != awsSigV4StreamingEventsPayload || request.Header.Get("Content-Type") != "application/vnd.amazon.eventstream" {
			t.Fatalf("event-stream headers=%v", request.Header)
		}
		if request.ContentLength != -1 || request.GetBody != nil {
			t.Fatalf("event-stream content length=%d getBody=%v", request.ContentLength, request.GetBody != nil)
		}
		decoder := eventstream.NewDecoder()
		first, err := decoder.Decode(request.Body, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first.Payload, unsigned.Bytes()) || first.Headers.Get(eventstream.DateHeader) == nil || first.Headers.Get(eventstream.ChunkSignatureHeader) == nil {
			t.Fatalf("signed event=%#v", first)
		}
		terminal, err := decoder.Decode(request.Body, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(terminal.Payload) != 0 || terminal.Headers.Get(eventstream.ChunkSignatureHeader) == nil {
			t.Fatalf("terminal event=%#v", terminal)
		}
		if _, err := decoder.Decode(request.Body, nil); err != io.EOF {
			t.Fatalf("event stream end error=%v", err)
		}
		return httpResponse(200, `{"ok":true}`), nil
	})
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret"}}, HTTP: doer, Now: func() time.Time { return now },
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, AuthScheme: "sigv4", PayloadMode: "aws-eventstream", Method: "POST",
		URL: "https://transcribestreaming.us-east-1.amazonaws.com/stream-transcription", Service: "transcribestreaming", Operation: "start-stream-transcription", Region: "us-east-1", BodyFile: bodyFile,
	})
	if err != nil || string(result.Output) != `{"ok":true}` {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestAWSSigV4EventStreamPolicyRejectsUnsafeCombinations(t *testing.T) {
	valid := Invocation{
		Provider: ProviderAWS, AuthScheme: "sigv4", PayloadMode: "aws-eventstream", Service: "transcribestreaming", Operation: "start-stream-transcription",
		Method: "POST", URL: "https://transcribestreaming.us-east-1.amazonaws.com/stream-transcription", Region: "us-east-1", Body: "encoded-event",
	}
	if err := validateInvocation(valid, nil); err != nil {
		t.Fatalf("valid aws-eventstream request rejected: %v", err)
	}
	for _, mutate := range []func(*Invocation){
		func(request *Invocation) { request.AuthScheme = "sigv4a"; request.RegionSet = "*" },
		func(request *Invocation) { request.Method = "GET" },
		func(request *Invocation) { request.Body = nil },
	} {
		request := valid
		mutate(&request)
		if err := validateInvocation(request, nil); err == nil {
			t.Fatalf("unsafe aws-eventstream request accepted: %#v", request)
		}
	}
}

func TestAWSSigV4EventStreamRejectsMalformedAndOversizedFrames(t *testing.T) {
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	credentials := aws.Credentials{AccessKeyID: "AKID", SecretAccessKey: "SECRET"}
	seed := bytes.Repeat([]byte{1}, sha256.Size)
	oversizedPrelude := make([]byte, 8)
	binary.BigEndian.PutUint32(oversizedPrelude[:4], maxAWSEventStreamFrameBytes+1)
	for name, input := range map[string][]byte{
		"truncated-prelude": {0},
		"oversized-frame":   oversizedPrelude,
		"invalid-crc":       make([]byte, awsEventStreamMinimumFrameBytes),
	} {
		t.Run(name, func(t *testing.T) {
			if name == "invalid-crc" {
				binary.BigEndian.PutUint32(input[:4], awsEventStreamMinimumFrameBytes)
			}
			reader := newAWSSigV4EventStreamReader(t.Context(), io.NopCloser(bytes.NewReader(input)), credentials, "transcribestreaming", "us-east-1", func() time.Time { return now }, seed)
			if _, err := io.ReadAll(reader); err == nil {
				t.Fatalf("accepted malformed event stream frame %q", name)
			}
		})
	}
	request, err := http.NewRequest(http.MethodPost, "https://transcribestreaming.us-east-1.amazonaws.com/stream-transcription", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := configureAWSEventStreamRequest(request); err == nil {
		t.Fatal("accepted event stream request without body")
	}
}

func TestParseAWSRegionSetNormalizesDeduplicatesAndBounds(t *testing.T) {
	regions, err := parseAWSRegionSet("US-EAST-1, us-west-*,US-EAST-1")
	if err != nil || strings.Join(regions, ",") != "us-east-1,us-west-*" {
		t.Fatalf("regions=%v err=%v", regions, err)
	}
	if _, err := parseAWSRegionSet(strings.Repeat("us-east-1,", 16) + "us-west-2"); err == nil {
		t.Fatal("accepted more than 16 region_set entries")
	}
}

func TestAlibabaACS3AdapterSignsAndSendsHTTP(t *testing.T) {
	now := time.Date(2023, 10, 26, 9, 1, 1, 0, time.UTC)
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(request.Header.Get("Authorization"), "ACS3-HMAC-SHA256 Credential=aliyun-ak,") {
			t.Fatalf("authorization=%q", request.Header.Get("Authorization"))
		}
		if request.Header.Get("X-Acs-Action") != "DescribeInstances" || request.Header.Get("X-Acs-Version") != "2014-05-26" {
			t.Fatalf("action/version headers=%v", request.Header)
		}
		if request.Header.Get("X-Acs-Security-Token") != "ram-token" {
			t.Fatalf("security token missing")
		}
		return httpResponse(200, `{"Instances":[]}`), nil
	})
	adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{
		Credentials: staticAlibabaCredentialsProvider{AlibabaCredentials{
			AccessKeyID: "aliyun-ak", AccessKeySecret: "aliyun-secret", SecurityToken: "ram-token",
		}},
		HTTP:  doer,
		Now:   func() time.Time { return now },
		Nonce: func() string { return "d410180a5abf7fe235dd9b74aca91fc0" },
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAlicloud, AuthScheme: "acs3", Method: "POST",
		URL: "https://ecs.cn-shanghai.aliyuncs.com/", Service: "ecs", Operation: "DescribeInstances", APIVersion: "2014-05-26", Region: "cn-shanghai",
		Body: map[string]any{"RegionId": "cn-shanghai"},
	})
	if err != nil || string(result.Output) != `{"Instances":[]}` {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestAlibabaACS3MatchesOfficialFixedVector(t *testing.T) {
	request, err := http.NewRequest(http.MethodPost, "https://ecs.cn-shanghai.aliyuncs.com/?ImageId=win2019_1809_x64_dtc_zh-cn_40G_alibase_20230811.vhd&RegionId=cn-shanghai", nil)
	if err != nil {
		t.Fatal(err)
	}
	invocation := Invocation{Operation: "RunInstances", APIVersion: "2014-05-26"}
	now := time.Date(2023, 10, 26, 10, 22, 32, 0, time.UTC)
	if err := signAlibabaACS3(request, sha256Hex(nil), AlibabaCredentials{
		AccessKeyID: "YourAccessKeyId", AccessKeySecret: "YourAccessKeySecret",
	}, invocation, now, "3156853299f313e23d1673dc12e1703d"); err != nil {
		t.Fatal(err)
	}
	want := "ACS3-HMAC-SHA256 Credential=YourAccessKeyId,SignedHeaders=host;x-acs-action;x-acs-content-sha256;x-acs-date;x-acs-signature-nonce;x-acs-version,Signature=06563a9e1b43f5dfe96b81484da74bceab24a1d853912eee15083a6f0f3283c0"
	if got := request.Header.Get("Authorization"); got != want {
		t.Fatalf("authorization:\nwant %s\n got %s", want, got)
	}
}

func TestAlibabaRPCV2MatchesOfficialFixedVector(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "https://ecs.cn-beijing.aliyuncs.com/?Format=JSON&RegionId=cn-beijing", nil)
	if err != nil {
		t.Fatal(err)
	}
	invocation := Invocation{Operation: "DescribeDedicatedHosts", APIVersion: "2014-05-26"}
	now := time.Date(2023, 3, 13, 8, 34, 30, 0, time.UTC)
	if err := signAlibabaRPCV2(request, AlibabaCredentials{
		AccessKeyID: "testid", AccessKeySecret: "testsecret",
	}, invocation, now, "edb2b34af0af9a6d14deaf7c1a5315eb"); err != nil {
		t.Fatal(err)
	}
	query := request.URL.Query()
	if got, want := query.Get("Signature"), "9NaGiOspFP5UPcwX8Iwt2YJXXuk="; got != want {
		t.Fatalf("RPC V2 signature=%q want=%q", got, want)
	}
	for name, want := range map[string]string{
		"AccessKeyId": "testid", "Action": "DescribeDedicatedHosts", "Format": "JSON",
		"SignatureMethod": "HMAC-SHA1", "SignatureNonce": "edb2b34af0af9a6d14deaf7c1a5315eb",
		"SignatureVersion": "1.0", "Timestamp": "2023-03-13T08:34:30Z", "Version": "2014-05-26",
	} {
		if got := query.Get(name); got != want {
			t.Fatalf("%s=%q want=%q", name, got, want)
		}
	}
}

func TestAlibabaRPCV2AdapterSignsFormBodyAndInjectsSTS(t *testing.T) {
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		query := request.URL.Query()
		for name, want := range map[string]string{
			"AccessKeyId": "aliyun-ak", "Action": "TranslateGeneral", "Version": "2018-10-12",
			"SecurityToken": "ram-token", "SignatureMethod": "HMAC-SHA1", "SignatureVersion": "1.0",
		} {
			if got := query.Get(name); got != want {
				t.Fatalf("%s=%q want=%q", name, got, want)
			}
		}
		if got, want := query.Get("Signature"), "4XSGq7W8KVsFh1OtbHyY+g9rJj4="; got != want {
			t.Fatalf("RPC V2 form signature=%q want=%q", got, want)
		}
		if query.Has("Scene") || query.Has("SourceText") {
			t.Fatalf("form parameters were duplicated into query: %s", request.URL.RawQuery)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil || string(body) != "Scene=general&SourceText=Hello%20world" {
			t.Fatalf("body=%q err=%v", body, err)
		}
		return httpResponse(200, `{"RequestId":"rpc-request-id"}`), nil
	})
	adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{
		Credentials: staticAlibabaCredentialsProvider{AlibabaCredentials{
			AccessKeyID: "aliyun-ak", AccessKeySecret: "aliyun-secret", SecurityToken: "ram-token",
		}},
		HTTP:  doer,
		Now:   func() time.Time { return time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC) },
		Nonce: func() string { return "rpc-nonce" },
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAlicloud, AuthScheme: "rpc", Service: "alimt", Operation: "TranslateGeneral",
		APIVersion: "2018-10-12", Method: http.MethodPost, URL: "https://mt.aliyuncs.com/",
		Headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
		Body:    "Scene=general&SourceText=Hello%20world",
	})
	if err != nil || result.RequestID != "rpc-request-id" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestAlibabaRPCV2RejectsDuplicateAndControlledParameters(t *testing.T) {
	for _, rawURL := range []string{
		"https://ecs.cn-beijing.aliyuncs.com/?RegionId=a&RegionId=b",
		"https://ecs.cn-beijing.aliyuncs.com/?SignatureNonce=caller",
	} {
		request, err := http.NewRequest(http.MethodGet, rawURL, nil)
		if err != nil {
			t.Fatal(err)
		}
		err = signAlibabaRPCV2(request, AlibabaCredentials{
			AccessKeyID: "ak", AccessKeySecret: "secret",
		}, Invocation{Operation: "DescribeInstances", APIVersion: "2014-05-26"}, time.Now(), "nonce")
		if err == nil {
			t.Fatalf("accepted unsafe RPC V2 URL %q", rawURL)
		}
	}
}

func TestAlibabaROAV2MatchesOfficialCanonicalFormula(t *testing.T) {
	body := `{"CategoryName":"test","CategoryType":"UNSTRUCTURED"}`
	request, err := http.NewRequest(http.MethodPost, "https://bailian.cn-beijing.aliyuncs.com/llm-p2e4XXXXXXXXsvtn/datacenter/category", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	if err := signAlibabaROAV2(request, AlibabaCredentials{
		AccessKeyID: "testid", AccessKeySecret: "testsecret",
	}, "2023-12-29", time.Date(2025, 4, 16, 3, 44, 46, 0, time.UTC), "ef34aae7-7bd2-413d-a541-680cd2c48538"); err != nil {
		t.Fatal(err)
	}
	if got, want := request.Header.Get("Content-MD5"), "q2qaEcR4P47+Z7CUzHRTBw=="; got != want {
		t.Fatalf("Content-MD5=%q want=%q", got, want)
	}
	if got, want := request.Header.Get("Authorization"), "acs testid:AYFXm52Ok0J/NswY03XdQFe/mgc="; got != want {
		t.Fatalf("authorization=%q want=%q", got, want)
	}
	for name, want := range map[string]string{
		"Date": "Wed, 16 Apr 2025 03:44:46 GMT", "X-Acs-Signature-Method": "HMAC-SHA1",
		"X-Acs-Signature-Nonce": "ef34aae7-7bd2-413d-a541-680cd2c48538", "X-Acs-Signature-Version": "1.0", "X-Acs-Version": "2023-12-29",
	} {
		if got := request.Header.Get(name); got != want {
			t.Fatalf("%s=%q want=%q", name, got, want)
		}
	}
}

func TestAlibabaROAV2CanonicalResourceSortsQueryNames(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "https://example.aliyuncs.com/resources?z=last&empty=&a=first", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := canonicalAlibabaROAResource(request.URL), "/resources?a=first&empty&z=last"; got != want {
		t.Fatalf("canonical resource=%q want=%q", got, want)
	}
}

func TestAlibabaROAV2AdapterInjectsSTSAndCallsPDS(t *testing.T) {
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "123.api.aliyunpds.com" || request.Header.Get("X-Acs-Security-Token") != "ram-token" {
			t.Fatalf("request=%s headers=%v", request.URL, request.Header)
		}
		if !strings.HasPrefix(request.Header.Get("Authorization"), "acs aliyun-ak:") {
			t.Fatalf("authorization=%q", request.Header.Get("Authorization"))
		}
		return httpResponse(200, `{"items":[]}`), nil
	})
	adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{
		Credentials: staticAlibabaCredentialsProvider{AlibabaCredentials{
			AccessKeyID: "aliyun-ak", AccessKeySecret: "aliyun-secret", SecurityToken: "ram-token",
		}},
		HTTP:  doer,
		Now:   func() time.Time { return time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC) },
		Nonce: func() string { return "roa-nonce" },
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAlicloud, AuthScheme: "roa", Service: "pds", Operation: "ListDrives",
		APIVersion: "v2", Method: http.MethodPost, URL: "https://123.api.aliyunpds.com/v2/drive/list",
		Body: map[string]any{"limit": 20},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAlibabaDataHubMatchesOfficialCanonicalFormula(t *testing.T) {
	request, err := http.NewRequest(http.MethodPost, "https://dh-cn-hangzhou.aliyuncs.com/projects/test_project/topics/test_topic", strings.NewReader(`{"ShardCount":1}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if err := signAlibabaDataHub(request, AlibabaCredentials{
		AccessKeyID: "testid", AccessKeySecret: "testsecret",
	}, "1.1", time.Date(2019, 1, 10, 7, 28, 29, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if got, want := request.Header.Get("Authorization"), "DATAHUB testid:DzXPmORqWCxnaQfRgh+06bvaJeU="; got != want {
		t.Fatalf("authorization=%q want=%q", got, want)
	}
	if request.Header.Get("X-Datahub-Client-Version") != "1.1" || request.Header.Get("Date") != "Thu, 10 Jan 2019 07:28:29 GMT" {
		t.Fatalf("headers=%v", request.Header)
	}
}

func TestAlibabaDataHubAdapterInjectsSTS(t *testing.T) {
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("X-Datahub-Security-Token") != "ram-token" || !strings.HasPrefix(request.Header.Get("Authorization"), "DATAHUB ak:") {
			t.Fatalf("headers=%v", request.Header)
		}
		return httpResponse(200, `{"ProjectNames":[]}`), nil
	})
	adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{
		Credentials: staticAlibabaCredentialsProvider{AlibabaCredentials{AccessKeyID: "ak", AccessKeySecret: "secret", SecurityToken: "ram-token"}},
		HTTP:        doer, Now: func() time.Time { return time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC) },
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAlicloud, AuthScheme: "datahub", Service: "datahub", Operation: "ListProjects",
		Method: http.MethodGet, URL: "https://dh-cn-hangzhou.aliyuncs.com/projects",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAlibabaOpenSearchMatchesOfficialSearchVector(t *testing.T) {
	rawURL := "https://opensearch-cn-hangzhou.aliyuncs.com/v3/openapi/apps/app_schema_demo/search?query=query%3Dname%3A%27%E6%96%87%E6%A1%A3%27%26%26sort%3Did%26%26config%3Dformat%3Afulljson&fetch_fields=name&empty="
	request, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if err := signAlibabaOpenSearch(request, AlibabaCredentials{
		AccessKeyID: "LTAIEXAMPLE", AccessKeySecret: "yourAccessKeySecret",
	}, time.Date(2019, 2, 25, 10, 9, 57, 0, time.UTC), "1551089397451704"); err != nil {
		t.Fatal(err)
	}
	if got, want := request.Header.Get("Authorization"), "OPENSEARCH LTAIEXAMPLE:Mv5FyQxr6myxxnwMPqJ6f6F9+9Y="; got != want {
		t.Fatalf("authorization=%q want=%q", got, want)
	}
	if request.Header.Get("Date") != "2019-02-25T10:09:57Z" || request.Header.Get("X-Opensearch-Nonce") != "1551089397451704" {
		t.Fatalf("headers=%v", request.Header)
	}
}

func TestAlibabaOpenSearchAdapterSignsPushBodyAndInjectsSTS(t *testing.T) {
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Content-MD5") != "f2b1f6a843d9e59265e0ba88011b4b5d" || request.Header.Get("X-Opensearch-Security-Token") != "ram-token" {
			t.Fatalf("headers=%v", request.Header)
		}
		if !strings.HasPrefix(request.Header.Get("Authorization"), "OPENSEARCH ak:") {
			t.Fatalf("authorization=%q", request.Header.Get("Authorization"))
		}
		return httpResponse(200, `{"status":"OK"}`), nil
	})
	adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{
		Credentials: staticAlibabaCredentialsProvider{AlibabaCredentials{AccessKeyID: "ak", AccessKeySecret: "secret", SecurityToken: "ram-token"}},
		HTTP:        doer, Now: func() time.Time { return time.Unix(1551089397, 0).UTC() }, Nonce: func() string { return "seed" },
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAlicloud, AuthScheme: "opensearch", Service: "opensearch", Operation: "PushDocuments",
		Method: http.MethodPost, URL: "https://opensearch-cn-hangzhou.aliyuncs.com/v3/openapi/apps/demo/tab/actions/bulk", Body: `[{"cmd":"ADD"}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAlibabaODPSV4MatchesOfficialPyODPSVector(t *testing.T) {
	request, err := http.NewRequest(
		http.MethodPost,
		"https://service.cn-hangzhou.maxcompute.aliyun.com/api/projects/demo/instances?x-odps-option=enabled&curr_project=demo&empty=",
		strings.NewReader("test"),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Content-MD5", "098f6bcd4621d373cade4e832627b4f6")
	request.Header.Set("x-odps-test", "value")
	if err := signAlibabaODPSV4(request, AlibabaCredentials{
		AccessKeyID: "testid", AccessKeySecret: "testsecret",
	}, "cn-hangzhou", time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if got, want := request.Header.Get("Authorization"), "ODPS testid/20260803/cn-hangzhou/odps/aliyun_v4_request:Ask3eewTpb1WwlfFqN/C8vOGJFs="; got != want {
		t.Fatalf("authorization=%q want=%q", got, want)
	}
	if got, want := request.Header.Get("Date"), "Mon, 03 Aug 2026 01:02:03 GMT"; got != want {
		t.Fatalf("Date=%q want=%q", got, want)
	}
}

func TestAlibabaODPSV2MatchesOfficialPyODPSVector(t *testing.T) {
	request, err := http.NewRequest(
		http.MethodPost,
		"https://service.cn-hangzhou.maxcompute.aliyun.com/api/projects/demo/instances?x-odps-option=enabled&curr_project=demo&empty=",
		strings.NewReader("test"),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Content-MD5", "098f6bcd4621d373cade4e832627b4f6")
	request.Header.Set("x-odps-test", "value")
	if err := signAlibabaODPSV2(request, AlibabaCredentials{
		AccessKeyID: "testid", AccessKeySecret: "testsecret",
	}, time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if got, want := request.Header.Get("Authorization"), "ODPS testid:u9CWBy6JSAunsHVNgGlJuHRsRSg="; got != want {
		t.Fatalf("authorization=%q want=%q", got, want)
	}
}

func TestAlibabaODPSCanonicalRequestMatchesPyODPSQueryDecoding(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "https://service.cn-hangzhou.maxcompute.aliyun.com/api/projects?literal=a%252Fb", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Date", "Mon, 03 Aug 2026 01:02:03 GMT")
	canonical, err := canonicalAlibabaODPSRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := canonical, "GET\n\n\nMon, 03 Aug 2026 01:02:03 GMT\n/projects?literal=a/b"; got != want {
		t.Fatalf("canonical=%q want=%q", got, want)
	}
}

func TestAlibabaODPSSigningRejectsInvalidInputs(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "https://dt.cn-hangzhou.maxcompute.aliyun.com/projects/demo?tag=a&tag=b", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := signAlibabaODPSV2(request, AlibabaCredentials{AccessKeyID: "ak", AccessKeySecret: "secret"}, time.Now()); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("repeated query error=%v", err)
	}
	request, err = http.NewRequest(http.MethodGet, "https://dt.cn-hangzhou.maxcompute.aliyun.com/projects/demo", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := signAlibabaODPSV2(request, AlibabaCredentials{}, time.Now()); err == nil || !strings.Contains(err.Error(), "AKSK") {
		t.Fatalf("missing AKSK error=%v", err)
	}
}

func TestAlibabaODPSV4AdapterSignsTunnelRequestAndInjectsSTS(t *testing.T) {
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		if got, want := request.Header.Get("Authorization-Sts-Token"), "ram-token"; got != want {
			t.Fatalf("Authorization-Sts-Token=%q want=%q", got, want)
		}
		if !strings.HasPrefix(request.Header.Get("Authorization"), "ODPS ak/20260803/cn-hangzhou/odps/aliyun_v4_request:") {
			t.Fatalf("authorization=%q", request.Header.Get("Authorization"))
		}
		return httpResponse(200, `{}`), nil
	})
	adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{
		Credentials: staticAlibabaCredentialsProvider{AlibabaCredentials{AccessKeyID: "ak", AccessKeySecret: "secret", SecurityToken: "ram-token"}},
		HTTP:        doer,
		Now:         func() time.Time { return time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC) },
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAlicloud, AuthScheme: "odps4", Service: "maxcompute", Operation: "DownloadTable",
		Region: "cn-hangzhou", Method: http.MethodGet,
		URL: "https://dt.cn-hangzhou.maxcompute.aliyun.com/projects/demo/tables/table?downloadid=session",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAlibabaFCMatchesOfficialSDKSignatureVector(t *testing.T) {
	headers := http.Header{
		"x-fc-trace-id": {"trace-id"},
		"content-type":  {"text/json"},
		"content-md5":   {"ef5b43a0fe1d5b401b62c3ba33a7a3d6"},
	}
	resource := "/2016-08-15/proxy/service.LATEST/func/abc 123\n" +
		"a=123 45\n" +
		"x-fc-access-key-id=akID\n" +
		"x-fc-expires=1583221068\n" +
		"x-fc-security-token=stsToken\n" +
		"x=456\n" +
		"x=xyz"
	got := alibabaFCSignature("S9MWOBG0AOWHSO9HPASP216QOPHT5YR4NLH3A", http.MethodGet, headers, "1583221068", resource)
	if want := "QFdNhz0Dl+onUM/MvpX940WFH3H8OHayZHbby1c01aI="; got != want {
		t.Fatalf("signature=%q want=%q", got, want)
	}
}

func TestAlibabaFCCanonicalResourceDistinguishesAPIAndHTTPTrigger(t *testing.T) {
	common, err := http.NewRequest(http.MethodGet, "https://123.cn-hangzhou.fc.aliyuncs.com/2016-08-15/services?limit=10&nextToken=opaque", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := canonicalAlibabaFCResource(common.URL), "/2016-08-15/services"; got != want {
		t.Fatalf("common resource=%q want=%q", got, want)
	}
	trigger, err := http.NewRequest(http.MethodGet, "https://123.cn-hangzhou.fc.aliyuncs.com/2016-08-15/proxy/service.LATEST/function/path-with-%20-space?x=1&a=2&x=3&with%20space=foo%20bar", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "/2016-08-15/proxy/service.LATEST/function/path-with- -space\na=2\nwith space=foo bar\nx=1\nx=3"
	if got := canonicalAlibabaFCResource(trigger.URL); got != want {
		t.Fatalf("trigger resource=%q want=%q", got, want)
	}
}

func TestAlibabaFCAdapterSignsBodyAndSTS(t *testing.T) {
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		for name, want := range map[string]string{
			"Content-MD5":         "28804cae9c94c693a03da301e61b7646",
			"Date":                "Mon, 03 Aug 2026 01:02:03 GMT",
			"X-Fc-Security-Token": "ram-token",
			"Authorization":       "FC testid:JdketQUDIQWDp+2KBm5mm/I32W+YpkII3Vv1UnQwY24=",
		} {
			if got := request.Header.Get(name); got != want {
				t.Fatalf("%s=%q want=%q headers=%v", name, got, want, request.Header)
			}
		}
		return httpResponse(200, `{}`), nil
	})
	adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{
		Credentials: staticAlibabaCredentialsProvider{AlibabaCredentials{AccessKeyID: "testid", AccessKeySecret: "testsecret", SecurityToken: "ram-token"}},
		HTTP:        doer,
		Now:         func() time.Time { return time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC) },
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAlicloud, AuthScheme: "fc", Service: "fc", Operation: "CreateService",
		Method: http.MethodPost, URL: "https://123.cn-hangzhou.fc.aliyuncs.com/2016-08-15/services",
		Headers: map[string]string{"X-Fc-Trace-Id": "trace-id"}, Body: map[string]any{"serviceName": "demo"},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAlibabaFC3AdapterMatchesOfficialOpenAPIUtilVector(t *testing.T) {
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		want := "ACS3-HMAC-SHA256 Credential=testid,SignedHeaders=content-type;x-acs-date;x-acs-security-token,Signature=e85a61d8ac3b1e0fd94f9c7e747668ad95794edffb52137947195c40b484c855"
		if got := request.Header.Get("Authorization"); got != want {
			t.Fatalf("authorization=%q want=%q", got, want)
		}
		if request.Header.Get("X-Acs-Date") != "2026-08-03T01:02:03Z" || request.Header.Get("X-Acs-Security-Token") != "ram-token" {
			t.Fatalf("headers=%v", request.Header)
		}
		return httpResponse(200, `ok`), nil
	})
	adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{
		Credentials: staticAlibabaCredentialsProvider{AlibabaCredentials{AccessKeyID: "testid", AccessKeySecret: "testsecret", SecurityToken: "ram-token"}},
		HTTP:        doer,
		Now:         func() time.Time { return time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC) },
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAlicloud, AuthScheme: "fc3", Service: "fc", Operation: "InvokeHTTPTrigger",
		Method: http.MethodPost, URL: "https://xx.cn-shanghai.fcapp.run/hello?foo=bar", Body: "hello world",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAlibabaFCCustomDomainAdapterMatchesOfficialHMACFormula(t *testing.T) {
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		if got, want := request.Header.Get("Authorization"), "acs testid:wHPkxQA9V4QHS7RHi9EguBgpgoY="; got != want {
			t.Fatalf("authorization=%q want=%q", got, want)
		}
		return httpResponse(200, `ok`), nil
	})
	adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{
		Credentials:  staticAlibabaCredentialsProvider{AlibabaCredentials{AccessKeyID: "testid", AccessKeySecret: "testsecret", SecurityToken: "ram-token"}},
		HTTP:         doer,
		AllowedHosts: []string{"functions.example.com"},
		Now:          func() time.Time { return time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC) },
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAlicloud, AuthScheme: "fc-custom", Service: "fc", Operation: "InvokeCustomDomain",
		Method: http.MethodPost, URL: "https://functions.example.com/hello?foo=bar&empty=", Body: "hello world",
		Headers: map[string]string{"Accept": "application/json"},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAlibabaFCCustomDomainWithoutCanonicalHeaders(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "https://functions.example.com/hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := signAlibabaFCCustomDomain(request, AlibabaCredentials{AccessKeyID: "testid", AccessKeySecret: "testsecret"}, time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if got, want := request.Header.Get("Authorization"), "acs testid:qoM5xk9WFeQBqm1LGDEJ0As1nwE="; got != want {
		t.Fatalf("authorization=%q want=%q", got, want)
	}
}

func TestAlibabaFCTriggerSignersRejectRepeatedQueryNames(t *testing.T) {
	credentials := AlibabaCredentials{AccessKeyID: "testid", AccessKeySecret: "testsecret"}
	fc3, err := http.NewRequest(http.MethodGet, "https://xx.cn-shanghai.fcapp.run/hello?tag=a&tag=b", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := signAlibabaFC3(fc3, credentials, time.Now()); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("FC3 repeated query error=%v", err)
	}
	custom, err := http.NewRequest(http.MethodGet, "https://functions.example.com/hello?tag=a&tag=b", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := signAlibabaFCCustomDomain(custom, credentials, time.Now()); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("FC custom repeated query error=%v", err)
	}
}

func TestAlibabaSLSV1MatchesOfficialSDKVector(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "/logstores", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Date", "Mon, 3 Jan 2010 08:33:47 GMT")
	request.Header.Set("x-log-bodyrawsize", "0")
	if err := signAlibabaSLSV1(request, AlibabaCredentials{
		AccessKeyID: "mockAccessKeyID", AccessKeySecret: "mockAccessKeySecret",
	}, "0.6.0", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if got, want := request.Header.Get("Authorization"), "LOG mockAccessKeyID:Rwm6cTKzoti4HWoe+GKcb6Kv07E="; got != want {
		t.Fatalf("authorization=%q want=%q", got, want)
	}
}

func TestAlibabaSLSV4MatchesOfficialSDKVector(t *testing.T) {
	body := "adasd= -asd zcas"
	request, err := http.NewRequest(http.MethodPost, "/logstores", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("x-log-date", "20220808T032330Z")
	if err := signAlibabaSLSV4(request, sha256Hex([]byte(body)), AlibabaCredentials{
		AccessKeyID: "acsddda21dsd", AccessKeySecret: "zxasdasdasw2",
	}, "cn-shanghai", "", time.Time{}); err != nil {
		t.Fatal(err)
	}
	want := "SLS4-HMAC-SHA256 Credential=acsddda21dsd/20220808/cn-shanghai/sls/aliyun_v4_request,Signature=8a10a5e723cb2e75964816de660b2c16a58af8bc0261f7f0722d832468c76ce8"
	if got := request.Header.Get("Authorization"); got != want {
		t.Fatalf("authorization=%q want=%q", got, want)
	}
}

func TestAlibabaMNSMatchesOfficialCanonicalizationFormula(t *testing.T) {
	request, err := http.NewRequest(http.MethodPut, "/queues/examplequeue?metaOverride=true", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "text/xml")
	request.Header.Set("Date", "Wed, 08 Mar 2012 12:00:00 GMT")
	if err := signAlibabaMNS(request, AlibabaCredentials{
		AccessKeyID: "test-ak", AccessKeySecret: "test-secret",
	}, "2015-06-06", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if got, want := request.Header.Get("Authorization"), "MNS test-ak:Y6SD1lHAB+0Yco7zX0WcrNFXMJk="; got != want {
		t.Fatalf("authorization=%q want=%q", got, want)
	}
}

func TestAlibabaProductSpecificSignersInjectSTSAndSendHTTPS(t *testing.T) {
	tests := []struct {
		scheme string
		region string
		url    string
		prefix string
		token  string
	}{
		{scheme: "sls", url: "https://project.cn-hangzhou.log.aliyuncs.com/logstores", prefix: "LOG aliyun-ak:", token: "X-Acs-Security-Token"},
		{scheme: "sls4", region: "cn-hangzhou", url: "https://project.cn-hangzhou.log.aliyuncs.com/logstores", prefix: "SLS4-HMAC-SHA256 Credential=aliyun-ak/", token: "X-Acs-Security-Token"},
		{scheme: "oss", url: "https://bucket.oss-cn-hangzhou.aliyuncs.com/object?acl", prefix: "OSS aliyun-ak:", token: "X-Oss-Security-Token"},
		{scheme: "mns", url: "https://123456789.mns.cn-hangzhou.aliyuncs.com/queues", prefix: "MNS aliyun-ak:", token: "Security-Token"},
	}
	for _, test := range tests {
		t.Run(test.scheme, func(t *testing.T) {
			doer := doerFunc(func(request *http.Request) (*http.Response, error) {
				if got := request.Header.Get("Authorization"); !strings.HasPrefix(got, test.prefix) {
					t.Fatalf("authorization=%q", got)
				}
				if got := request.Header.Get(test.token); got != "ram-token" {
					t.Fatalf("%s=%q", test.token, got)
				}
				return httpResponse(200, `{}`), nil
			})
			adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{
				Credentials: staticAlibabaCredentialsProvider{AlibabaCredentials{
					AccessKeyID: "aliyun-ak", AccessKeySecret: "aliyun-secret", SecurityToken: "ram-token",
				}},
				HTTP: doer,
				Now:  func() time.Time { return time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC) },
			})
			_, err := adapter.Invoke(t.Context(), Invocation{
				Provider: ProviderAlicloud, AuthScheme: test.scheme, Service: test.scheme, Operation: "ListResources",
				APIVersion: "0.6.0", Region: test.region, Method: http.MethodGet, URL: test.url,
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAlibabaProductSpecificBodiesAndQueriesFollowOfficialCanonicalization(t *testing.T) {
	slsRequest, err := http.NewRequest(http.MethodPost, "/logstores?size=10&offset=1&tag=b&tag=a", strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	slsRequest.Header.Set("Content-Type", "application/json")
	slsRequest.Header.Set("Date", "Mon, 09 Nov 2015 06:03:03 GMT")
	if err := signAlibabaSLSV1(slsRequest, AlibabaCredentials{
		AccessKeyID: "ak", AccessKeySecret: "secret",
	}, "", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if got, want := slsRequest.Header.Get("Content-MD5"), "5D41402ABC4B2A76B9719D911017C592"; got != want {
		t.Fatalf("SLS Content-MD5=%q want=%q", got, want)
	}
	if got, want := canonicalAlibabaSLSResource(slsRequest.URL), "/logstores?offset=1&size=10&tag=btag=a"; got != want {
		t.Fatalf("SLS canonical resource=%q want=%q", got, want)
	}
	queryURL, err := url.Parse("/logstores?a=&x=a+b")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := canonicalAlibabaSLSV4Query(queryURL), "a&x=a%20b"; got != want {
		t.Fatalf("SLS4 canonical query=%q want=%q", got, want)
	}

	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		if got := request.Header.Get("Content-Type"); got != "application/xml" {
			t.Fatalf("MNS Content-Type=%q", got)
		}
		if got, want := request.Header.Get("Content-MD5"), "YzA5ZjA5MTUzNzY5NDgyMTA0ZjEyNWVlYTc5YjExYzE="; got != want {
			t.Fatalf("MNS Content-MD5=%q want=%q", got, want)
		}
		return httpResponse(200, `<Queues/>`), nil
	})
	adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{
		Credentials: staticAlibabaCredentialsProvider{AlibabaCredentials{AccessKeyID: "ak", AccessKeySecret: "secret"}},
		HTTP:        doer,
		Now:         func() time.Time { return time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC) },
	})
	if _, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAlicloud, AuthScheme: "mns", Service: "mns", Operation: "CreateQueue",
		Method: http.MethodPut, URL: "https://123456789.mns.cn-hangzhou.aliyuncs.com/queues/demo", Body: "<Queue/>",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAlibabaProductSpecificSignerRejectsUnreplayableBodyAndInvalidSLS4Date(t *testing.T) {
	request, err := http.NewRequest(http.MethodPost, "/logstores", io.NopCloser(strings.NewReader("body")))
	if err != nil {
		t.Fatal(err)
	}
	request.GetBody = nil
	if _, _, err := requestBodyMD5(request, false); err == nil {
		t.Fatal("accepted unreplayable body")
	}
	request, err = http.NewRequest(http.MethodGet, "/logstores", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Log-Date", "short")
	if err := signAlibabaSLSV4(request, sha256Hex(nil), AlibabaCredentials{AccessKeyID: "ak", AccessKeySecret: "secret"}, "cn-hangzhou", "", time.Time{}); err == nil {
		t.Fatal("accepted invalid SLS4 date")
	}
}

func TestAlibabaOTSV2MatchesOfficialSDKVector(t *testing.T) {
	request, err := http.NewRequest(http.MethodPost, "/ListTable", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("x-ots-date", "2024-08-08T03:22:47.8123Z")
	request.Header.Set("x-ots-instancename", "credentials_test")
	request.Header.Set("x-ots-contentmd5", "1B2M2Y8AsgTpgAmY7PhCfg==")
	if err := signAlibabaOTSV2(request, AlibabaCredentials{
		AccessKeyID: "credentials_test_id", AccessKeySecret: "credentials_test_secret",
	}, "2015-12-31", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if got, want := request.Header.Get("x-ots-signature"), "A940TpusldxW4mUEOFbhrtwctoU="; got != want {
		t.Fatalf("OTS V2 signature=%q want=%q", got, want)
	}
}

func TestAlibabaOTSV4MatchesOfficialSDKVector(t *testing.T) {
	request, err := http.NewRequest(http.MethodPost, "/ListTable", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("x-ots-date", "2024-08-08T03:22:47.8123Z")
	request.Header.Set("x-ots-instancename", "credentials_test")
	request.Header.Set("x-ots-contentmd5", "1B2M2Y8AsgTpgAmY7PhCfg==")
	if err := signAlibabaOTSV4(request, AlibabaCredentials{
		AccessKeyID: "credentials_test_id", AccessKeySecret: "credentials_test_secret",
	}, "cn-hangzhou", "2015-12-31", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if got, want := request.Header.Get("x-ots-signaturev4"), "mUSbIuIfN/JuO4/mCOaQZ72JHDc8z6gjPdBpPyAd/ac="; got != want {
		t.Fatalf("OTS V4 signature=%q want=%q", got, want)
	}
}

func TestAlibabaOTSAdapterSignsProtobufHTTPWithInternalSTS(t *testing.T) {
	for _, scheme := range []string{"ots", "ots4"} {
		t.Run(scheme, func(t *testing.T) {
			doer := doerFunc(func(request *http.Request) (*http.Response, error) {
				if request.Method != http.MethodPost || request.Header.Get("x-ots-instancename") != "demo" {
					t.Fatalf("request=%s %s headers=%v", request.Method, request.URL, request.Header)
				}
				if request.Header.Get("x-ots-ststoken") != "ram-token" {
					t.Fatalf("STS token missing")
				}
				signatureHeader := "x-ots-signature"
				if scheme == "ots4" {
					signatureHeader = "x-ots-signaturev4"
				}
				if request.Header.Get(signatureHeader) == "" {
					t.Fatalf("%s missing", signatureHeader)
				}
				return httpResponse(200, "protobuf-response"), nil
			})
			adapter := NewAlibabaRESTAdapter(AlibabaRESTConfig{
				Credentials: staticAlibabaCredentialsProvider{AlibabaCredentials{
					AccessKeyID: "ak", AccessKeySecret: "secret", SecurityToken: "ram-token",
				}},
				HTTP: doer,
				Now:  func() time.Time { return time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC) },
			})
			_, err := adapter.Invoke(t.Context(), Invocation{
				Provider: ProviderAlicloud, AuthScheme: scheme, Service: "ots", Operation: "ListTable",
				Region: "cn-hangzhou", Method: http.MethodPost, URL: "https://demo.cn-hangzhou.ots.aliyuncs.com/ListTable", Body: "protobuf-request",
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTencentTC3AdapterMatchesOfficialSignatureExample(t *testing.T) {
	now := time.Unix(1551113065, 0).UTC()
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		want := "TC3-HMAC-SHA256 Credential=AKIDEXAMPLE/2019-02-25/cvm/tc3_request, SignedHeaders=content-type;host, Signature="
		if !strings.HasPrefix(request.Header.Get("Authorization"), want) {
			t.Fatalf("authorization=%q", request.Header.Get("Authorization"))
		}
		if request.Header.Get("X-Tc-Action") != "DescribeInstances" || request.Header.Get("X-Tc-Version") != "2017-03-12" {
			t.Fatalf("action/version headers=%v", request.Header)
		}
		if request.Header.Get("X-Tc-Token") != "cam-token" {
			t.Fatalf("temporary token missing")
		}
		return httpResponse(200, `{"Response":{"RequestId":"request-id"}}`), nil
	})
	adapter := NewTencentRESTAdapter(TencentRESTConfig{
		Credentials: staticTencentCredentialsProvider{TencentCredentials{
			SecretID: "AKIDEXAMPLE", SecretKey: "secret", Token: "cam-token",
		}},
		HTTP: doer,
		Now:  func() time.Time { return now },
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderTencent, AuthScheme: "tc3", Method: "POST",
		URL: "https://cvm.tencentcloudapi.com/", Service: "cvm", Operation: "DescribeInstances", APIVersion: "2017-03-12", Region: "ap-guangzhou",
		Headers: map[string]string{"Content-Type": "application/json; charset=utf-8"}, Body: map[string]any{},
	})
	if err != nil || string(result.Output) != `{"Response":{"RequestId":"request-id"}}` || result.RequestID != "request-id" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestTencentTC3CanonicalRequestMatchesOfficialFixedVector(t *testing.T) {
	payload := `{"Limit": 1, "Filters": [{"Values": ["unnamed"], "Name": "instance-name"}]}`
	request, err := http.NewRequest(http.MethodPost, "https://cvm.tencentcloudapi.com/", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	canonicalHeaderValue, signedHeaders := canonicalHeaders(request, func(name string) bool {
		return name == "content-type" || name == "host"
	})
	canonicalRequest := strings.Join([]string{
		"POST", "/", "", canonicalHeaderValue, signedHeaders, sha256Hex([]byte(payload)),
	}, "\n")
	if got, want := sha256Hex([]byte(payload)), "99d58dfbc6745f6747f36bfca17dee5e6881dc0428a0a36f96199342bc5b4907"; got != want {
		t.Fatalf("payload hash=%s, want %s", got, want)
	}
	if got, want := sha256Hex([]byte(canonicalRequest)), "2815843035062fffda5fd6f2a44ea8a34818b0dc46f024b8b3786976a3adda7a"; got != want {
		t.Fatalf("canonical hash=%s, want %s\n%s", got, want, canonicalRequest)
	}
}

func TestTencentV1MatchesOfficialCanonicalExample(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "https://cvm.tencentcloudapi.com/?InstanceIds.0=ins-09dx96dg&Offset=0&Limit=20", nil)
	if err != nil {
		t.Fatal(err)
	}
	invocation := Invocation{Service: "cvm", Operation: "DescribeInstances", APIVersion: "2017-03-12", Region: "ap-guangzhou"}
	if err := signTencentV1(request, TencentCredentials{SecretID: "AKIDEXAMPLE", SecretKey: "testsecret"}, invocation, time.Unix(1465185768, 0).UTC(), "11886", false); err != nil {
		t.Fatal(err)
	}
	query := request.URL.Query()
	if got, want := query.Get("Signature"), "E/mORCKgkwA4wtfri8eb+yh6Kk4="; got != want {
		t.Fatalf("signature=%q, want %q", got, want)
	}
	if query.Get("Action") != "DescribeInstances" || query.Get("SecretId") != "AKIDEXAMPLE" || query.Get("Nonce") != "11886" {
		t.Fatalf("common parameters=%v", query)
	}
}

func TestTencentV1SignsFormPOSTWithSHA256AndCAMToken(t *testing.T) {
	request, err := http.NewRequest(http.MethodPost, "https://cvm.tencentcloudapi.com/", strings.NewReader("Offset=0&Limit=20"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	invocation := Invocation{Service: "cvm", Operation: "DescribeInstances", APIVersion: "2017-03-12", Region: "ap-guangzhou"}
	if err := signTencentV1(request, TencentCredentials{SecretID: "AKIDEXAMPLE", SecretKey: "testsecret", Token: "cam-token"}, invocation, time.Unix(1465185768, 0).UTC(), "11886", true); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	form, err := url.ParseQuery(string(body))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := form.Get("Signature"), "3f/YqrWx8PZ/O/GxvUu+MPX0jp8kyMU9lbAJ40/lDDI="; got != want {
		t.Fatalf("signature=%q, want %q", got, want)
	}
	if form.Get("Token") != "cam-token" || form.Get("SignatureMethod") != "HmacSHA256" {
		t.Fatalf("form=%v", form)
	}
}

func TestTencentV1AdapterInjectsCredentialsAndCallsHTTPS(t *testing.T) {
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		query := request.URL.Query()
		if query.Get("SecretId") != "AKIDEXAMPLE" || query.Get("Token") != "cam-token" || query.Get("Signature") == "" {
			t.Fatalf("query=%v", query)
		}
		return httpResponse(200, `{"Response":{"RequestId":"request-id"}}`), nil
	})
	adapter := NewTencentRESTAdapter(TencentRESTConfig{
		Credentials: staticTencentCredentialsProvider{TencentCredentials{SecretID: "AKIDEXAMPLE", SecretKey: "testsecret", Token: "cam-token"}},
		HTTP:        doer, Now: func() time.Time { return time.Unix(1465185768, 0).UTC() }, Nonce: func() string { return "11886" },
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderTencent, AuthScheme: "tc1", Service: "cvm", Operation: "DescribeInstances", APIVersion: "2017-03-12", Region: "ap-guangzhou",
		Method: http.MethodGet, URL: "https://cvm.tencentcloudapi.com/", Parameters: map[string]any{"Limit": 20},
	})
	if err != nil || result.RequestID != "request-id" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestTencentV1SHA256SchemeWired(t *testing.T) {
	root := t.TempDir()
	valid := Invocation{
		Provider: ProviderTencent, AuthScheme: authSchemeTencentV1SHA256,
		Service: "cvm", Operation: "DescribeInstances", APIVersion: "2017-03-12",
		Region: "ap-guangzhou", Method: http.MethodGet, URL: "https://cvm.tencentcloudapi.com/",
	}
	if err := validateInvocation(valid, []string{root}); err != nil {
		t.Fatalf("valid tc1-sha256 invocation rejected: %v", err)
	}
	lookalike := valid
	lookalike.URL = "https://cvm.tencentcloudapi.com.attacker.example/"
	if err := validateInvocation(lookalike, []string{root}); err == nil {
		t.Fatal("lookalike Tencent tc1-sha256 host accepted")
	}
}

func TestTencentQCloudLegacyAdapterSignsDocumentedPath(t *testing.T) {
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		if got, want := request.URL.EscapedPath(), "/v2/index.php"; got != want {
			t.Fatalf("path=%q, want %q", got, want)
		}
		query := request.URL.Query()
		if got, want := query.Get("Signature"), "t8vBZr4+Yu2PUKC1b50qtw8I+6E="; got != want {
			t.Fatalf("signature=%q, want %q; query=%v", got, want, query)
		}
		if query.Get("Version") != "" || query.Get("SignatureMethod") != "HmacSHA1" || query.Get("Token") != "cam-token" {
			t.Fatalf("legacy common parameters=%v", query)
		}
		return httpResponse(200, `{"code":0,"message":""}`), nil
	})
	adapter := NewTencentRESTAdapter(TencentRESTConfig{
		Credentials: staticTencentCredentialsProvider{TencentCredentials{SecretID: "AKIDEXAMPLE", SecretKey: "testsecret", Token: "cam-token"}},
		HTTP:        doer, Now: func() time.Time { return time.Unix(1465185768, 0).UTC() }, Nonce: func() string { return "11886" },
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderTencent, AuthScheme: "qcloud", Service: "dc", Operation: "CreateDirectConnectTunnel", Region: "ap-guangzhou",
		Method: http.MethodGet, URL: "https://dc.api.qcloud.com/v2/index.php", Parameters: map[string]any{"directConnectId": "dc-kd7d06of"},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTencentQCloudLegacySignsFormPOSTWithSHA256(t *testing.T) {
	request, err := http.NewRequest(http.MethodPost, "https://dc.api.qcloud.com/v2/index.php", strings.NewReader("limit=20"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	invocation := Invocation{Service: "dc", Operation: "DescribeDirectConnects", Region: "ap-guangzhou"}
	if err := signTencentQCloud(request, TencentCredentials{SecretID: "AKIDEXAMPLE", SecretKey: "testsecret"}, invocation, time.Unix(1465185768, 0).UTC(), "11886", true); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	form, err := url.ParseQuery(string(body))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := form.Get("Signature"), "u7bWTdzEn2MDdrqmoTS2gKkiB9Hzueqnz2u3eP4PCdw="; got != want {
		t.Fatalf("signature=%q, want %q", got, want)
	}
	if form.Get("SignatureMethod") != "HmacSHA256" || form.Get("Version") != "" {
		t.Fatalf("form=%v", form)
	}
}

func TestTencentQCloudLegacyRejectsNonProductEndpoint(t *testing.T) {
	for _, rawURL := range []string{
		"https://api.qcloud.com/v2/index.php",
		"https://dc.api.qcloud.com/",
		"https://dc.tencentcloudapi.com/v2/index.php",
	} {
		request, err := http.NewRequest(http.MethodGet, rawURL, nil)
		if err != nil {
			t.Fatal(err)
		}
		err = signTencentQCloud(request, TencentCredentials{SecretID: "id", SecretKey: "key"}, Invocation{Operation: "DescribeDirectConnects"}, time.Unix(1465185768, 0).UTC(), "11886", false)
		if err == nil {
			t.Fatalf("URL %q was accepted", rawURL)
		}
	}
}

func TestTencentASRWebSocketSignatureMatchesOfficialCanonicalAlgorithm(t *testing.T) {
	signedURL, err := signTencentASRWebSocketURL(
		"wss://asr.cloud.tencent.com/asr/v2/1259220000",
		TencentCredentials{SecretID: "AKIDEXAMPLE", SecretKey: "testsecret"},
		map[string]any{"engine_model_type": "16k_zh", "needvad": 1, "voice_format": 1},
		time.Unix(1673408372, 0).UTC(),
		"1673408372",
		"c64385ee-3e5c-4fc5-bbfd-7c71addb35b0",
	)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(signedURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if got, want := query.Get("signature"), "TWnOXzSRGSW/kExVPvxQSP6/4Uk="; got != want {
		t.Fatalf("signature=%q, want %q", got, want)
	}
	if query.Get("secretid") != "AKIDEXAMPLE" || query.Get("expired") != "1673494772" || query.Get("voice_id") == "" {
		t.Fatalf("query=%v", query)
	}
}

func TestTencentASRWebSocketSignsOfficialTemporaryToken(t *testing.T) {
	signedURL, err := signTencentASRWebSocketURL(
		"wss://asr.cloud.tencent.com/asr/v2/1259220000",
		TencentCredentials{SecretID: "AKIDEXAMPLE", SecretKey: "testsecret", Token: "session-token"},
		map[string]any{"engine_model_type": "16k_zh", "needvad": 1, "voice_format": 1},
		time.Unix(1673408372, 0).UTC(),
		"1673408372",
		"c64385ee-3e5c-4fc5-bbfd-7c71addb35b0",
	)
	if err != nil {
		t.Fatal(err)
	}
	query, err := url.ParseQuery(strings.SplitN(signedURL, "?", 2)[1])
	if err != nil {
		t.Fatal(err)
	}
	if query.Get("token") != "session-token" {
		t.Fatalf("query=%v", query)
	}
	if got, want := query.Get("signature"), "ktYY613rhDshviEkWWnR9/gVvqo="; got != want {
		t.Fatalf("signature=%q, want %q", got, want)
	}
}

func TestTencentSpeechTranslateWebSocketSignatureMatchesOfficialAlgorithm(t *testing.T) {
	signedURL, err := signTencentSpeechTranslateWebSocketURL(
		"wss://asr.cloud.tencent.com/asr/speech_translate/1259220000",
		TencentCredentials{SecretID: "AKIDEXAMPLE", SecretKey: "testsecret"},
		map[string]any{"source": "zh", "target": "en", "trans_model": "hunyuan-translation-lite", "voice_format": 1},
		time.Unix(1673408372, 0).UTC(),
		"1673408372",
		"c64385ee-3e5c-4fc5-bbfd-7c71addb35b0",
	)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(signedURL)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := parsed.Query().Get("signature"), "kkqPvXSnhA6H1FRAEmuau0oyy9w="; got != want {
		t.Fatalf("signature=%q, want %q", got, want)
	}
}

func TestTencentVirtualNumberWebSocketSignatureMatchesOfficialAlgorithm(t *testing.T) {
	signedURL, err := signTencentVirtualNumberWebSocketURL(
		"wss://asr.cloud.tencent.com/asr/virtual_number/v1/1259220000",
		TencentCredentials{SecretID: "AKIDEXAMPLE", SecretKey: "testsecret"},
		map[string]any{"voice_format": 1, "wait_time": 30},
		time.Unix(1673408372, 0).UTC(),
		"1673408372",
		"c64385ee-3e5c-4fc5-bbfd-7c71addb35b0",
	)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(signedURL)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := parsed.Query().Get("signature"), "uRX2ieVJSbmPWgLxQZBvySNo2Do="; got != want {
		t.Fatalf("signature=%q, want %q", got, want)
	}
}

func TestTencentVirtualNumberWebSocketRejectsTemporaryTokenAndInvalidParameters(t *testing.T) {
	baseURL := "wss://asr.cloud.tencent.com/asr/virtual_number/v1/1259220000"
	if _, err := signTencentVirtualNumberWebSocketURL(baseURL, TencentCredentials{SecretID: "id", SecretKey: "key", Token: "session"}, nil, time.Unix(1673408372, 0), "1673408372", "voice"); err == nil || !strings.Contains(err.Error(), "temporary-token") {
		t.Fatalf("temporary token error=%v", err)
	}
	invalid := []map[string]any{
		{"voice_format": 2},
		{"wait_time": 0},
		{"wait_time": 61},
		{"signature": "caller"},
		{"unknown": "value"},
		{"voice_format": []string{"1"}},
	}
	for _, parameters := range invalid {
		if err := validateTencentVirtualNumberParameters(parameters); err == nil {
			t.Fatalf("invalid parameters accepted: %#v", parameters)
		}
	}
	if got := defaultTencentVirtualNumberChunkBytes(map[string]any{"voice_format": 1}); got != 640 {
		t.Fatalf("PCM chunk=%d", got)
	}
	if got := defaultTencentVirtualNumberChunkBytes(map[string]any{"voice_format": 10}); got != 4096 {
		t.Fatalf("compressed chunk=%d", got)
	}
}

func TestTencentSOEWebSocketSignatureMatchesOfficialAlgorithm(t *testing.T) {
	signedURL, err := signTencentSOEWebSocketURL(
		"wss://soe.cloud.tencent.com/soe/api/1306000000",
		TencentCredentials{SecretID: "AKIDEXAMPLE", SecretKey: "testsecret"},
		map[string]any{"eval_mode": 1, "ref_text": "hello", "score_coeff": 1.5, "sentence_info_enabled": 1, "server_engine_type": "16k_zh", "text_mode": 0, "voice_format": 0},
		time.Unix(1722321759, 0).UTC(),
		"42261112",
		"4943511b-192c-40f8-b6c9-c3df2a827b75",
	)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(signedURL)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := parsed.Query().Get("signature"), "rMeQTtXY/Gn3smDgzaGi463ynMM="; got != want {
		t.Fatalf("signature=%q, want %q", got, want)
	}
}

func TestTencentSOEWebSocketRejectsTemporaryTokenAndInvalidParameters(t *testing.T) {
	baseURL := "wss://soe.cloud.tencent.com/soe/api/1306000000"
	valid := map[string]any{"server_engine_type": "16k_en", "eval_mode": 1, "score_coeff": 1.5}
	if _, err := signTencentSOEWebSocketURL(baseURL, TencentCredentials{SecretID: "id", SecretKey: "key", Token: "session"}, valid, time.Unix(1722321759, 0), "42261112", "voice"); err == nil || !strings.Contains(err.Error(), "temporary-token") {
		t.Fatalf("temporary token error=%v", err)
	}
	invalid := []map[string]any{
		{},
		{"server_engine_type": "8k_en", "eval_mode": 1, "score_coeff": 1.5},
		{"server_engine_type": "16k_en", "eval_mode": 9, "score_coeff": 1.5},
		{"server_engine_type": "16k_en", "eval_mode": 1, "score_coeff": 0.5},
		{"server_engine_type": "16k_en", "eval_mode": 1, "score_coeff": 1.5, "voice_format": 3},
		{"server_engine_type": "16k_en", "eval_mode": 1, "score_coeff": 1.5, "text_mode": 2},
		{"server_engine_type": "16k_en", "eval_mode": 1, "score_coeff": 1.5, "signature": "caller"},
		{"server_engine_type": []string{"16k_en"}, "eval_mode": 1, "score_coeff": 1.5},
	}
	for _, parameters := range invalid {
		if err := validateTencentSOEParameters(parameters); err == nil {
			t.Fatalf("invalid parameters accepted: %#v", parameters)
		}
	}
	credentials := TencentCredentials{SecretID: "id", SecretKey: "key"}
	if _, err := signTencentSOEWebSocketURL(baseURL, credentials, valid, time.Unix(1722321759, 0), "invalid", "voice"); err == nil {
		t.Fatal("invalid nonce was accepted")
	}
	if _, err := signTencentSOEWebSocketURL(baseURL, credentials, valid, time.Unix(1722321759, 0), "42261112", "voice id"); err == nil {
		t.Fatal("invalid voice_id was accepted")
	}
}

func TestTencentSOEWebSocketRejectsMalformedProviderResponses(t *testing.T) {
	sink := &tencentWebSocketOutputSink{maxBytes: 1024}
	tests := []struct {
		messageType tencentWebSocketMessageType
		data        []byte
	}{
		{tencentWebSocketMessageBinary, []byte("binary")},
		{tencentWebSocketMessageText, []byte("not-json")},
		{tencentWebSocketMessageText, []byte(`{"code":4001,"voice_id":"voice"}`)},
		{tencentWebSocketMessageText, []byte(`{"code":0,"voice_id":"other"}`)},
	}
	for _, test := range tests {
		if _, _, err := acceptTencentSOEText(test.messageType, test.data, sink, "voice"); err == nil {
			t.Fatalf("malformed response accepted: type=%d data=%q", test.messageType, test.data)
		}
	}
}

func TestTencentSpeechTranslateWebSocketRejectsTemporaryTokenAndInvalidInputs(t *testing.T) {
	baseURL := "wss://asr.cloud.tencent.com/asr/speech_translate/1259220000"
	credentials := TencentCredentials{SecretID: "id", SecretKey: "key"}
	parameters := map[string]any{"source": "zh", "target": "en", "trans_model": "hunyuan-translation-lite", "voice_format": 1}
	now := time.Unix(1673408372, 0).UTC()
	if _, err := signTencentSpeechTranslateWebSocketURL(baseURL, TencentCredentials{SecretID: "id", SecretKey: "key", Token: "session"}, parameters, now, "1673408372", "voice"); err == nil || !strings.Contains(err.Error(), "temporary-token") {
		t.Fatalf("temporary token error=%v", err)
	}
	if _, err := signTencentSpeechTranslateWebSocketURL(baseURL+"?signature=caller", credentials, parameters, now, "1673408372", "voice"); err == nil {
		t.Fatal("caller query was accepted")
	}
	if _, err := signTencentSpeechTranslateWebSocketURL(baseURL, credentials, parameters, now, "invalid", "voice"); err == nil {
		t.Fatal("invalid nonce was accepted")
	}
	if _, err := signTencentSpeechTranslateWebSocketURL(baseURL, credentials, parameters, now, "1673408372", "voice id"); err == nil {
		t.Fatal("invalid voice_id was accepted")
	}
	invalidParameters := []map[string]any{
		{},
		{"source": "zh", "target": "en", "trans_model": "unsupported", "voice_format": 1},
		{"source": "zh", "target": "en", "trans_model": "hunyuan-translation", "voice_format": 2},
		{"source": "zh", "target": "en", "trans_model": "hunyuan-translation", "voice_format": 1, "enable_tts": 2},
		{"source": "zh", "target": "en", "trans_model": "hunyuan-translation", "voice_format": 1, "codec": "wav"},
		{"source": "zh", "target": "en", "trans_model": "hunyuan-translation", "voice_format": 1, "signature": "caller"},
		{"source": "zh", "target": "en", "trans_model": "hunyuan-translation", "voice_format": 1, "unknown": "value"},
		{"source": []string{"zh"}, "target": "en", "trans_model": "hunyuan-translation", "voice_format": 1},
	}
	for _, candidate := range invalidParameters {
		if err := validateTencentSpeechTranslateParameters(candidate); err == nil {
			t.Fatalf("invalid parameters accepted: %#v", candidate)
		}
	}
}

func TestTencentVoiceConversionWebSocketSignatureMatchesOfficialAlgorithm(t *testing.T) {
	signedURL, err := signTencentVoiceConversionWebSocketURL(
		"wss://tts.cloud.tencent.com/vc_stream/1259220000",
		TencentCredentials{SecretID: "AKIDEXAMPLE", SecretKey: "testsecret"},
		map[string]any{"VoiceType": 301005, "SampleRate": 16000, "Codec": "pcm"},
		time.Unix(1670304040, 0).UTC(),
		"voice-test",
	)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(signedURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if got, want := query.Get("Signature"), "vuCesnwv4Oj9bLNbC7A9Cpikb3g="; got != want {
		t.Fatalf("signature=%q, want %q", got, want)
	}
	if query.Get("SecretId") != "AKIDEXAMPLE" || query.Get("VoiceType") != "301005" || query.Get("Volume") != "0" || query.Get("End") != "0" {
		t.Fatalf("query=%v", query)
	}
	uppercaseURL, err := signTencentVoiceConversionWebSocketURL(
		"wss://TTS.CLOUD.TENCENT.COM/vc_stream/1259220000",
		TencentCredentials{SecretID: "AKIDEXAMPLE", SecretKey: "testsecret"},
		map[string]any{"VoiceType": 301005, "SampleRate": 16000, "Codec": "pcm"},
		time.Unix(1670304040, 0).UTC(),
		"voice-test",
	)
	if err != nil {
		t.Fatal(err)
	}
	uppercaseParsed, err := url.Parse(uppercaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := uppercaseParsed.Query().Get("Signature"), query.Get("Signature"); got != want {
		t.Fatalf("case-normalized signature=%q, want %q", got, want)
	}
}

func TestTencentVoiceConversionFramesUseDocumentedNetworkByteOrder(t *testing.T) {
	frame, err := encodeTencentVoiceConversionFrame(true, []byte{0xde, 0xad})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := hex.EncodeToString(frame), "000000097b22456e64223a317ddead"; got != want {
		t.Fatalf("frame=%q, want %q", got, want)
	}
	responseJSON := []byte(`{"VoiceId":"voice-test","MessageId":"message","Message":"success","Final":1,"Code":0}`)
	responseFrame := make([]byte, 4+len(responseJSON)+2)
	binary.BigEndian.PutUint32(responseFrame[:4], uint32(len(responseJSON)))
	copy(responseFrame[4:], responseJSON)
	copy(responseFrame[4+len(responseJSON):], []byte{0xca, 0xfe})
	message, audio, err := decodeTencentVoiceConversionFrame(responseFrame)
	if err != nil {
		t.Fatal(err)
	}
	if message.VoiceID != "voice-test" || message.Final != 1 || hex.EncodeToString(audio) != "cafe" {
		t.Fatalf("message=%#v audio=%x", message, audio)
	}
}

func TestTencentVoiceConversionWebSocketRejectsTemporaryTokenAndInvalidInputs(t *testing.T) {
	baseURL := "wss://tts.cloud.tencent.com/vc_stream/1259220000"
	parameters := map[string]any{"VoiceType": 301005, "SampleRate": 16000, "Codec": "pcm"}
	if _, err := signTencentVoiceConversionWebSocketURL(baseURL, TencentCredentials{SecretID: "id", SecretKey: "key", Token: "session"}, parameters, time.Unix(1670304040, 0), "voice"); err == nil || !strings.Contains(err.Error(), "temporary-token") {
		t.Fatalf("temporary token error=%v", err)
	}
	if _, err := signTencentVoiceConversionWebSocketURL(baseURL+"?Signature=caller", TencentCredentials{SecretID: "id", SecretKey: "key"}, parameters, time.Unix(1670304040, 0), "voice"); err == nil {
		t.Fatal("caller query was accepted")
	}
	invalidParameters := []map[string]any{
		{},
		{"VoiceType": 0, "SampleRate": 16000, "Codec": "pcm"},
		{"VoiceType": 301005, "SampleRate": 8000, "Codec": "pcm"},
		{"VoiceType": 301005, "SampleRate": 16000, "Codec": "wav"},
		{"VoiceType": 301005, "SampleRate": 16000, "Codec": "pcm", "Volume": 11},
		{"VoiceType": 301005, "SampleRate": 16000, "Codec": "pcm", "End": 1},
		{"VoiceType": []string{"301005"}, "SampleRate": 16000, "Codec": "pcm"},
	}
	for _, candidate := range invalidParameters {
		if _, err := validateTencentVoiceConversionParameters(candidate); err == nil {
			t.Fatalf("invalid parameters accepted: %#v", candidate)
		}
	}
	for _, frame := range [][]byte{{0, 0, 0}, {0, 0, 0, 0}, {0, 0, 0, 10, '{'}} {
		if _, _, err := decodeTencentVoiceConversionFrame(frame); err == nil {
			t.Fatalf("invalid frame accepted: %x", frame)
		}
	}
}

func TestTencentASRGeneratedNonceAndVoiceIDStayWithinProtocolBounds(t *testing.T) {
	for range 100 {
		nonce := secureTencentASRNonce()
		if !validTencentASRNonce(nonce) {
			t.Fatalf("invalid generated nonce %q", nonce)
		}
		voiceID := secureTencentVoiceID()
		if !tencentVoiceIDPattern.MatchString(voiceID) {
			t.Fatalf("invalid generated voice ID %q", voiceID)
		}
	}
}

func TestTencentMPSWebSocketSignatureMatchesOfficialCanonicalAlgorithm(t *testing.T) {
	signedURL, err := signTencentMPSWebSocketURL(
		"wss://mps.cloud.tencent.com/wss/v1/1258344699",
		TencentCredentials{SecretID: "AKIDEXAMPLE", SecretKey: "testsecret"},
		map[string]any{"asrDst": "zh", "fragmentNotify": 0},
		time.Unix(1750217009, 0).UTC(),
		"7549145852",
	)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(signedURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if got, want := query.Get("signature"), "d5259168af7cab8ed9cefb1d50628f6b88faabfc9b5092171f90d7b119f0db0e"; got != want {
		t.Fatalf("signature=%q, want %q", got, want)
	}
	if query.Get("secretId") != "AKIDEXAMPLE" || query.Get("timeStamp") != "1750217009" || query.Get("expired") != "1750220609" || query.Get("nonce") != "7549145852" || query.Has("timeoutSec") {
		t.Fatalf("query=%v", query)
	}
}

func TestTencentMPSWebSocketRejectsTemporaryTokenAndControlledQuery(t *testing.T) {
	base := func(credentials TencentCredentials, parameters map[string]any) error {
		_, err := signTencentMPSWebSocketURL(
			"wss://mps.cloud.tencent.com/wss/v1/1258344699",
			credentials,
			parameters,
			time.Unix(1750217009, 0).UTC(),
			"7549145852",
		)
		return err
	}
	if err := base(TencentCredentials{SecretID: "id", SecretKey: "key", Token: "session"}, map[string]any{"asrDst": "zh"}); err == nil || !strings.Contains(err.Error(), "temporary-token") {
		t.Fatalf("temporary token error=%v", err)
	}
	if err := base(TencentCredentials{SecretID: "id", SecretKey: "key"}, map[string]any{"asrDst": "zh", "signature": "caller"}); err == nil || !strings.Contains(err.Error(), "caller-supplied") {
		t.Fatalf("controlled query error=%v", err)
	}
	if err := base(TencentCredentials{SecretID: "id", SecretKey: "key"}, map[string]any{"transSrc": "zh"}); err == nil || !strings.Contains(err.Error(), "transDst") {
		t.Fatalf("translation pair error=%v", err)
	}
}

func TestTencentMPSGeneratedNonceIsExactlyTenDigits(t *testing.T) {
	for range 100 {
		if nonce := secureTencentMPSNonce(); !tencentMPSNoncePattern.MatchString(nonce) {
			t.Fatalf("invalid generated MPS nonce %q", nonce)
		}
	}
}

func TestTencentMPSTTSWebSocketSignatureIncludesEmptyPayloadHash(t *testing.T) {
	signedURL, err := signTencentMPSTTSWebSocketURL(
		"wss://mps.cloud.tencent.com/tts/v1/1258344699",
		TencentCredentials{SecretID: "AKIDEXAMPLE", SecretKey: "testsecret"},
		map[string]any{"voiceId": "voice/test+id", "format": "pcm", "sampleRate": 16000, "language": "zh", "timeoutSec": 30},
		time.Unix(1782289476, 0).UTC(),
		"6751517593",
	)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(signedURL)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := parsed.Query().Get("signature"), "4aa9c1cbf3562116a664f0a823552386786625b7bc40d97d12bb34b74ac2f9a9"; got != want {
		t.Fatalf("signature=%q, want %q", got, want)
	}
	if parsed.Query().Get("voiceId") != "voice/test+id" || parsed.Query().Get("expired") != "1782293076" {
		t.Fatalf("query=%v", parsed.Query())
	}
}

func TestTencentMPSTTSWebSocketValidatesDocumentedInputs(t *testing.T) {
	credentials := TencentCredentials{SecretID: "id", SecretKey: "key"}
	if _, err := signTencentMPSTTSWebSocketURL("wss://mps.cloud.tencent.com/tts/v1/1258344699", TencentCredentials{SecretID: "id", SecretKey: "key", Token: "session"}, map[string]any{"voiceId": "voice"}, time.Unix(1782289476, 0), "6751517593"); err == nil {
		t.Fatal("temporary token was accepted")
	}
	if _, err := signTencentMPSTTSWebSocketURL("wss://mps.cloud.tencent.com/tts/v1/1258344699", credentials, map[string]any{"voiceId": "voice"}, time.Unix(1782289476, 0), "short"); err == nil {
		t.Fatal("invalid nonce was accepted")
	}
	invalidParameters := []map[string]any{
		{},
		{"voiceId": "voice", "format": "raw"},
		{"voiceId": "voice", "sampleRate": 0},
		{"voiceId": "voice", "timeoutSec": 121},
		{"voiceId": "voice", "speed": "fast"},
		{"voiceId": "voice", "signature": "caller"},
		{"voiceId": "voice", "unknown": "value"},
	}
	for _, parameters := range invalidParameters {
		if err := validateTencentMPSTTSParameters(parameters); err == nil {
			t.Fatalf("invalid parameters accepted: %#v", parameters)
		}
	}
	if segments, err := tencentTTSTextSegments([]string{"one", "two"}); err != nil || len(segments) != 2 {
		t.Fatalf("segments=%v err=%v", segments, err)
	}
	for _, body := range []any{[]any{}, []any{"ok", 1}, strings.Repeat("x", 5001), map[string]any{"Text": "no"}} {
		if _, err := tencentTTSTextSegments(body); err == nil {
			t.Fatalf("invalid TTS body accepted: %#v", body)
		}
	}
	if defaultTencentMPSChunkBytes(1) != 1280 || defaultTencentMPSChunkBytes(2) != 640 {
		t.Fatal("unexpected MPS PCM defaults")
	}
	for format, want := range map[string]string{"mp3": "audio/mpeg", "wav": "audio/wav", "flac": "audio/flac", "pcm": "application/octet-stream"} {
		if got := tencentTTSAudioContentType(format); got != want {
			t.Fatalf("content type for %s=%q, want %q", format, got, want)
		}
	}
}

func TestTencentTTSWebSocketSignatureMatchesOfficialCanonicalAlgorithm(t *testing.T) {
	signedURL, err := signTencentTTSWebSocketURL(
		"wss://tts.cloud.tencent.com/stream_ws",
		TencentCredentials{SecretID: "AKIDEXAMPLE", SecretKey: "testsecret"},
		map[string]any{"AppId": 1300460000, "Codec": "pcm", "EnableSubtitle": true, "SampleRate": 16000, "Speed": 0, "VoiceType": 101001, "Volume": 0},
		"hello",
		time.Unix(1688610905, 0).UTC(),
		"b78ae3ba-1ba5-11ee-a106-768645a5c72a",
	)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(signedURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if got, want := query.Get("Signature"), "E1QvAtEl7SevcQLgk3TCXRieJ5A="; got != want {
		t.Fatalf("signature=%q, want %q", got, want)
	}
	if query.Get("Action") != "TextToStreamAudioWS" || query.Get("Expired") != "1688697305" || query.Get("SessionId") == "" || query.Get("Text") != "hello" || query.Get("EnableSubtitle") != "true" {
		t.Fatalf("query=%v", query)
	}
}

func TestTencentTTSWebSocketValidatesDocumentedInputs(t *testing.T) {
	credentials := TencentCredentials{SecretID: "id", SecretKey: "key"}
	valid := map[string]any{"AppId": 1300460000, "Codec": "pcm"}
	baseURL := "wss://tts.cloud.tencent.com/stream_ws"
	if _, err := signTencentTTSWebSocketURL(baseURL, TencentCredentials{SecretID: "id", SecretKey: "key", Token: "session"}, valid, "hello", time.Unix(1688610905, 0), "session"); err == nil || !strings.Contains(err.Error(), "temporary-token") {
		t.Fatalf("temporary token error=%v", err)
	}
	if _, err := signTencentTTSWebSocketURL(baseURL+"?Signature=caller", credentials, valid, "hello", time.Unix(1688610905, 0), "session"); err == nil {
		t.Fatal("caller query was accepted")
	}
	invalidParameters := []map[string]any{
		{},
		{"AppId": 1300460000},
		{"AppId": "not-an-int", "Codec": "pcm"},
		{"AppId": "99999999999999999999", "Codec": "pcm"},
		{"AppId": 1300460000, "Codec": "wav"},
		{"AppId": 1300460000, "Codec": "pcm", "SampleRate": 22050},
		{"AppId": 1300460000, "Codec": "pcm", "Volume": 11},
		{"AppId": 1300460000, "Codec": "pcm", "Speed": 6.01},
		{"AppId": 1300460000, "Codec": "pcm", "EmotionCategory": "surprised"},
		{"AppId": 1300460000, "Codec": "pcm", "EmotionIntensity": 49},
		{"AppId": 1300460000, "Codec": "pcm", "SegmentRate": 3},
		{"AppId": 1300460000, "Codec": "pcm", "Signature": "caller"},
		{"AppId": 1300460000, "Codec": "pcm", "unknown": "value"},
	}
	for _, parameters := range invalidParameters {
		if _, err := validateTencentTTSParameters(parameters); err == nil {
			t.Fatalf("invalid parameters accepted: %#v", parameters)
		}
	}
	for _, text := range []any{"", strings.Repeat("x", 1801), strings.Repeat("中", 601), []string{"not", "one"}} {
		if _, err := tencentTTSRequestText(text); err == nil {
			t.Fatalf("invalid TTS body accepted: %#v", text)
		}
	}
}

func TestTencentStreamingTTSWebSocketSignatureMatchesOfficialCanonicalAlgorithm(t *testing.T) {
	signedURL, err := signTencentStreamingTTSWebSocketURL(
		"wss://tts.cloud.tencent.com/stream_wsv2",
		TencentCredentials{SecretID: "AKIDEXAMPLE", SecretKey: "testsecret"},
		map[string]any{"AppId": 1300460000, "Codec": "pcm", "EnableSubtitle": true, "SampleRate": 16000, "Speed": 0, "VoiceType": 101001, "Volume": 0},
		time.Unix(1688610905, 0).UTC(),
		"b78ae3ba-1ba5-11ee-a106-768645a5c72a",
	)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(signedURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if got, want := query.Get("Signature"), "fnubVPDcyKIUpjCDe71CHJDPv18="; got != want {
		t.Fatalf("signature=%q, want %q", got, want)
	}
	if query.Get("Action") != "TextToStreamAudioWSv2" || query.Get("Expired") != "1688697305" || query.Get("SessionId") == "" || query.Has("Text") {
		t.Fatalf("query=%v", query)
	}
}

func TestTencentStreamingTTSWebSocketValidatesTextSegments(t *testing.T) {
	if segments, err := tencentStreamingTTSTextSegments([]any{"hello, ", "world!"}); err != nil || len(segments) != 2 {
		t.Fatalf("segments=%v err=%v", segments, err)
	}
	for _, body := range []any{nil, "", []any{}, []any{"ok", 1}, []any{strings.Repeat("x", 10000), "x"}, map[string]any{"data": "no"}} {
		if _, err := tencentStreamingTTSTextSegments(body); err == nil {
			t.Fatalf("invalid streaming TTS body accepted: %#v", body)
		}
	}
	if _, err := signTencentStreamingTTSWebSocketURL("wss://tts.cloud.tencent.com/stream_wsv2", TencentCredentials{SecretID: "id", SecretKey: "key", Token: "session"}, map[string]any{"AppId": 1300460000, "Codec": "pcm"}, time.Unix(1688610905, 0), "session"); err == nil || !strings.Contains(err.Error(), "temporary-token") {
		t.Fatalf("temporary token error=%v", err)
	}
}

func TestTencentPodcastWebSocketSignatureMatchesOfficialVector(t *testing.T) {
	signedURL, err := signTencentPodcastWebSocketURL(
		"wss://tts.cloud.tencent.com/stream_ws_podcast",
		TencentCredentials{SecretID: "AKIDPseudo" + "SecretId1234567890abcdefgH", SecretKey: "PseudoSecretKey1234567890abcdefG"},
		map[string]any{"AppId": 1300466766, "Codec": "pcm", "SampleRate": 24000},
		time.Unix(1761816664, 0).UTC(),
		"27d0a902-b573-11f0-b377-52540037edd7",
	)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(signedURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if got, want := query.Get("Signature"), "3Ivu6TM5GFMNhUhdvMVkMMcdiX4="; got != want {
		t.Fatalf("signature=%q, want %q", got, want)
	}
	if query.Get("Action") != "TextToPodcastStreamAudioWS" || query.Get("Expired") != "1761903064" || query.Get("SessionId") == "" {
		t.Fatalf("query=%v", query)
	}
}

func TestTencentPodcastWebSocketValidatesInputObjects(t *testing.T) {
	textObjects, err := tencentPodcastInputObjects([]any{
		map[string]any{"ObjectType": "TYPE_TEXT", "Text": "first"},
		map[string]any{"ObjectType": "TYPE_TEXT", "Text": "second"},
	})
	if err != nil || len(textObjects) != 2 {
		t.Fatalf("objects=%v err=%v", textObjects, err)
	}
	urlObjects, err := tencentPodcastInputObjects(map[string]any{"ObjectType": "TYPE_URL", "Url": "https://example.com/article"})
	if err != nil || len(urlObjects) != 1 {
		t.Fatalf("url objects=%v err=%v", urlObjects, err)
	}
	fileObjects, err := tencentPodcastInputObjects(map[string]any{"ObjectType": "TYPE_FILE", "Url": "https://example.com/report.pdf", "FileFormat": "pdf"})
	if err != nil || len(fileObjects) != 1 {
		t.Fatalf("file objects=%v err=%v", fileObjects, err)
	}
	invalid := []any{
		nil,
		[]any{},
		append(make([]any, 10), map[string]any{"ObjectType": "TYPE_TEXT", "Text": "too many"}),
		[]any{map[string]any{"ObjectType": "TYPE_TEXT", "Text": "text"}, map[string]any{"ObjectType": "TYPE_URL", "Url": "https://example.com"}},
		map[string]any{"ObjectType": "TYPE_TEXT", "Text": ""},
		map[string]any{"ObjectType": "TYPE_TEXT", "Text": "ok", "Url": "https://example.com"},
		map[string]any{"ObjectType": "TYPE_URL", "Url": "http://example.com"},
		map[string]any{"ObjectType": "TYPE_URL", "Url": "https://127.0.0.1/private"},
		map[string]any{"ObjectType": "TYPE_FILE", "Url": "https://example.com/report.exe", "FileFormat": "exe"},
		map[string]any{"ObjectType": "TYPE_FILE", "Url": "https://example.com/report.pdf"},
		map[string]any{"ObjectType": "TYPE_TEXT", "Text": "ok", "Extra": true},
	}
	for _, body := range invalid {
		if _, err := tencentPodcastInputObjects(body); err == nil {
			t.Fatalf("invalid podcast body accepted: %#v", body)
		}
	}
	if _, err := signTencentPodcastWebSocketURL("wss://tts.cloud.tencent.com/stream_ws_podcast", TencentCredentials{SecretID: "id", SecretKey: "key", Token: "session"}, map[string]any{"AppId": 1300466766, "Codec": "pcm", "SampleRate": 24000}, time.Unix(1761816664, 0), "session"); err == nil || !strings.Contains(err.Error(), "temporary-token") {
		t.Fatalf("temporary token error=%v", err)
	}
}

func TestTencentPodcastWebSocketValidatesDocumentedParameters(t *testing.T) {
	parameters, err := validateTencentPodcastParameters(map[string]any{
		"AppId": 1300466766, "SampleRate": 24000, "SpeakerNumber": 2, "Speaker1Voice": "zixin", "Speaker2Voice": "acan", "EnableWebSearch": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if parameters["Codec"] != "pcm" || parameters["EnableWebSearch"] != "false" {
		t.Fatalf("parameters=%v", parameters)
	}
	invalid := []map[string]any{
		{"AppId": 1300466766, "Codec": "pcm"},
		{"AppId": 1300466766, "SampleRate": 16000},
		{"AppId": 1300466766, "SampleRate": 24000, "Codec": "mp3"},
		{"AppId": 1300466766, "SampleRate": 24000, "SpeakerNumber": 1},
		{"AppId": 1300466766, "SampleRate": 24000, "SpeakerNumber": 1, "Speaker1Voice": "one", "Speaker2Voice": "two"},
		{"AppId": 1300466766, "SampleRate": 24000, "SpeakerNumber": 2, "Speaker1Voice": "one"},
		{"AppId": 1300466766, "SampleRate": 24000, "EnableWebSearch": "sometimes"},
		{"AppId": 1300466766, "SampleRate": 24000, "Unknown": true},
	}
	for _, candidate := range invalid {
		if _, err := validateTencentPodcastParameters(candidate); err == nil {
			t.Fatalf("invalid parameters accepted: %v", candidate)
		}
	}
}

func TestTencentMPSAudioFrameUsesDocumentedNetworkByteOrder(t *testing.T) {
	frame, err := encodeTencentMPSAudioFrame(1, true, 0x0102030405060708, "user", []byte{0xde, 0xad})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := hex.EncodeToString(frame), "010101020304050607080004757365720000dead"; got != want {
		t.Fatalf("frame=%q, want %q", got, want)
	}
}

type fakeTencentWebSocketConnection struct {
	reads     [][]byte
	readTypes []tencentWebSocketMessageType
	readErr   error
	writes    []struct {
		messageType tencentWebSocketMessageType
		data        []byte
	}
}

func (connection *fakeTencentWebSocketConnection) Read(context.Context) (tencentWebSocketMessageType, []byte, error) {
	if len(connection.reads) == 0 {
		if connection.readErr != nil {
			return 0, nil, connection.readErr
		}
		return 0, nil, io.EOF
	}
	data := connection.reads[0]
	connection.reads = connection.reads[1:]
	messageType := tencentWebSocketMessageText
	if len(connection.readTypes) > 0 {
		messageType = connection.readTypes[0]
		connection.readTypes = connection.readTypes[1:]
	}
	return messageType, data, nil
}

func (connection *fakeTencentWebSocketConnection) Write(_ context.Context, messageType tencentWebSocketMessageType, data []byte) error {
	connection.writes = append(connection.writes, struct {
		messageType tencentWebSocketMessageType
		data        []byte
	}{messageType: messageType, data: append([]byte(nil), data...)})
	return nil
}

func (*fakeTencentWebSocketConnection) Close() error { return nil }

func TestTencentASRWebSocketAdapterStreamsGuardedAudioInternally(t *testing.T) {
	audioFile := filepath.Join(t.TempDir(), "audio.pcm")
	if err := os.WriteFile(audioFile, []byte("abcdefgh"), 0o600); err != nil {
		t.Fatal(err)
	}
	connection := &fakeTencentWebSocketConnection{reads: [][]byte{
		[]byte(`{"code":0,"message":"success","voice_id":"voice"}`),
		[]byte(`{"code":0,"message":"success","voice_id":"voice","result":{"slice_type":2,"voice_text_str":"hello"}}`),
		[]byte(`{"code":0,"message":"success","voice_id":"voice","final":1}`),
	}}
	adapter := NewTencentRESTAdapter(TencentRESTConfig{
		Credentials: staticTencentCredentialsProvider{TencentCredentials{SecretID: "AKIDEXAMPLE", SecretKey: "testsecret"}},
		Now:         func() time.Time { return time.Unix(1673408372, 0).UTC() },
		Nonce:       func() string { return "1673408372" },
		VoiceID:     func() string { return "c64385ee-3e5c-4fc5-bbfd-7c71addb35b0" },
		WebSocketDial: func(_ context.Context, signedURL string) (tencentWebSocketConnection, error) {
			parsed, err := url.Parse(signedURL)
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Query().Get("signature") == "" || parsed.Query().Get("secretid") != "AKIDEXAMPLE" {
				t.Fatalf("signed query=%v", parsed.Query())
			}
			return connection, nil
		},
		StreamPause: func(context.Context, time.Duration) error { return nil },
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderTencent, AuthScheme: "asr-ws", Service: "asr", Operation: "RecognizeStream", Method: http.MethodGet,
		URL: "wss://asr.cloud.tencent.com/asr/v2/1259220000", Parameters: map[string]any{"engine_model_type": "16k_zh", "voice_format": 1}, BodyFile: audioFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(result.Output, []byte(`"voice_text_str":"hello"`)) || result.RequestID != "voice" {
		t.Fatalf("result=%#v", result)
	}
	if len(connection.writes) != 2 || connection.writes[0].messageType != tencentWebSocketMessageBinary || string(connection.writes[0].data) != "abcdefgh" || connection.writes[1].messageType != tencentWebSocketMessageText || string(connection.writes[1].data) != `{"type":"end"}` {
		t.Fatalf("writes=%#v", connection.writes)
	}
}

func TestTencentVirtualNumberWebSocketStreamsEightKilohertzAudio(t *testing.T) {
	audioFile := filepath.Join(t.TempDir(), "audio.pcm")
	if err := os.WriteFile(audioFile, bytes.Repeat([]byte{0x44}, 1000), 0o600); err != nil {
		t.Fatal(err)
	}
	connection := &fakeTencentWebSocketConnection{reads: [][]byte{
		[]byte(`{"code":0,"message":"success","voice_id":"voice"}`),
		[]byte(`{"code":0,"message":"success","voice_id":"voice","message_id":"message","result":1,"final":1}`),
	}}
	adapter := NewTencentRESTAdapter(TencentRESTConfig{
		Credentials: staticTencentCredentialsProvider{TencentCredentials{SecretID: "id", SecretKey: "key"}},
		Now:         func() time.Time { return time.Unix(1673408372, 0).UTC() },
		Nonce:       func() string { return "1673408372" },
		VoiceID:     func() string { return "voice" },
		WebSocketDial: func(context.Context, string) (tencentWebSocketConnection, error) {
			return connection, nil
		},
		StreamPause: func(context.Context, time.Duration) error { return nil },
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderTencent, AuthScheme: "virtual-number-ws", Service: "asr", Operation: "RecognizeHumanStream", Method: http.MethodGet,
		URL: "wss://asr.cloud.tencent.com/asr/virtual_number/v1/1259220000", Parameters: map[string]any{"voice_format": 1, "wait_time": 30}, BodyFile: audioFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RequestID != "voice" || !bytes.Contains(result.Output, []byte(`"result":1`)) {
		t.Fatalf("result=%#v", result)
	}
	if len(connection.writes) != 3 || len(connection.writes[0].data) != 640 || len(connection.writes[1].data) != 360 || string(connection.writes[2].data) != `{"type":"end"}` {
		t.Fatalf("writes=%#v", connection.writes)
	}
}

func TestTencentSOEWebSocketStreamsEvaluationAudio(t *testing.T) {
	audioFile := filepath.Join(t.TempDir(), "audio.pcm")
	if err := os.WriteFile(audioFile, bytes.Repeat([]byte{0x44}, 1500), 0o600); err != nil {
		t.Fatal(err)
	}
	connection := &fakeTencentWebSocketConnection{reads: [][]byte{
		[]byte(`{"code":0,"message":"success","voice_id":"voice"}`),
		[]byte(`{"code":0,"message":"success","voice_id":"voice","message_id":"message","result":{"SuggestedScore":88.5},"final":1}`),
	}}
	adapter := NewTencentRESTAdapter(TencentRESTConfig{
		Credentials: staticTencentCredentialsProvider{TencentCredentials{SecretID: "id", SecretKey: "key"}},
		Now:         func() time.Time { return time.Unix(1722321759, 0).UTC() },
		Nonce:       func() string { return "42261112" },
		VoiceID:     func() string { return "voice" },
		WebSocketDial: func(context.Context, string) (tencentWebSocketConnection, error) {
			return connection, nil
		},
		StreamPause: func(context.Context, time.Duration) error { return nil },
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderTencent, AuthScheme: "soe-ws", Service: "soe", Operation: "EvaluateSpeechStream", Method: http.MethodGet,
		URL: "wss://soe.cloud.tencent.com/soe/api/1306000000", Parameters: map[string]any{"server_engine_type": "16k_en", "eval_mode": 1, "score_coeff": 1.5, "ref_text": "hello", "voice_format": 0}, BodyFile: audioFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RequestID != "voice" || !bytes.Contains(result.Output, []byte(`"SuggestedScore":88.5`)) {
		t.Fatalf("result=%#v", result)
	}
	if len(connection.writes) != 3 || len(connection.writes[0].data) != 1280 || len(connection.writes[1].data) != 220 || string(connection.writes[2].data) != `{"type":"end"}` {
		t.Fatalf("writes=%#v", connection.writes)
	}
}

func TestTencentSOERecordingModeSendsOneAudioFrame(t *testing.T) {
	audioFile := filepath.Join(t.TempDir(), "recording.pcm")
	if err := os.WriteFile(audioFile, bytes.Repeat([]byte{0x55}, 1500), 0o600); err != nil {
		t.Fatal(err)
	}
	connection := &fakeTencentWebSocketConnection{reads: [][]byte{
		[]byte(`{"code":0,"message":"success","voice_id":"voice"}`),
		[]byte(`{"code":0,"message":"success","voice_id":"voice","final":1}`),
	}}
	adapter := NewTencentRESTAdapter(TencentRESTConfig{
		Credentials: staticTencentCredentialsProvider{TencentCredentials{SecretID: "id", SecretKey: "key"}},
		Now:         func() time.Time { return time.Unix(1722321759, 0).UTC() },
		Nonce:       func() string { return "42261112" },
		VoiceID:     func() string { return "voice" },
		WebSocketDial: func(context.Context, string) (tencentWebSocketConnection, error) {
			return connection, nil
		},
		StreamPause: func(context.Context, time.Duration) error { return nil },
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderTencent, AuthScheme: "soe-ws", Service: "soe", Operation: "EvaluateSpeechRecording", Method: http.MethodGet,
		URL: "wss://soe.cloud.tencent.com/soe/api/1306000000", Parameters: map[string]any{"server_engine_type": "16k_en", "eval_mode": 1, "score_coeff": 1.5, "rec_mode": 1}, BodyFile: audioFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(connection.writes) != 2 || len(connection.writes[0].data) != 1500 || string(connection.writes[1].data) != `{"type":"end"}` {
		t.Fatalf("writes=%#v", connection.writes)
	}
}

func TestTencentSpeechTranslateWebSocketStreamsJSONAndSynthesizedAudio(t *testing.T) {
	audioFile := filepath.Join(t.TempDir(), "source.pcm")
	if err := os.WriteFile(audioFile, []byte("abcdefgh"), 0o600); err != nil {
		t.Fatal(err)
	}
	outputFile := filepath.Join(t.TempDir(), "translated.mp3")
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			[]byte(`{"code":0,"message":"success","voice_id":"voice"}`),
			[]byte(`{"code":0,"message":"success","voice_id":"voice","result":{"source_text":"你好","target_text":"hello"}}`),
			[]byte("audio-1"),
			[]byte(`{"code":0,"message":"success","voice_id":"voice","final":1}`),
			[]byte("audio-2"),
			[]byte(`{"code":0,"message":"success","voice_id":"voice","final":2}`),
		},
		readTypes: []tencentWebSocketMessageType{tencentWebSocketMessageText, tencentWebSocketMessageText, tencentWebSocketMessageBinary, tencentWebSocketMessageText, tencentWebSocketMessageBinary, tencentWebSocketMessageText},
	}
	adapter := NewTencentRESTAdapter(TencentRESTConfig{
		Credentials: staticTencentCredentialsProvider{TencentCredentials{SecretID: "AKIDEXAMPLE", SecretKey: "testsecret"}},
		Now:         func() time.Time { return time.Unix(1673408372, 0).UTC() },
		Nonce:       func() string { return "1673408372" },
		VoiceID:     func() string { return "c64385ee-3e5c-4fc5-bbfd-7c71addb35b0" },
		WebSocketDial: func(context.Context, string) (tencentWebSocketConnection, error) {
			return connection, nil
		},
		StreamPause: func(context.Context, time.Duration) error { return nil },
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderTencent, AuthScheme: "speech-translate-ws", Service: "asr", Operation: "TranslateStream", Method: http.MethodGet,
		URL:        "wss://asr.cloud.tencent.com/asr/speech_translate/1259220000",
		Parameters: map[string]any{"source": "zh", "target": "en", "trans_model": "hunyuan-translation-lite", "voice_format": 1, "enable_tts": 1, "codec": "mp3", "sample_rate": 16000},
		BodyFile:   audioFile, ResponseFile: outputFile, StreamChunkBytes: 8, StreamIntervalMS: 200, MaxResponseFileBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	audio, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(audio) != "audio-1audio-2" || result.RequestID != "voice" || !bytes.Contains(result.Output, []byte(`"target_text":"hello"`)) || !bytes.Contains(result.Output, []byte(`"format":"mp3"`)) {
		t.Fatalf("audio=%q result=%#v", audio, result)
	}
	if len(connection.writes) != 2 || connection.writes[0].messageType != tencentWebSocketMessageBinary || string(connection.writes[0].data) != "abcdefgh" || string(connection.writes[1].data) != `{"type":"end"}` {
		t.Fatalf("writes=%#v", connection.writes)
	}
}

func TestTencentSpeechTranslateWebSocketReturnsTranslationWithoutTTS(t *testing.T) {
	audioFile := filepath.Join(t.TempDir(), "source.pcm")
	if err := os.WriteFile(audioFile, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	connection := &fakeTencentWebSocketConnection{reads: [][]byte{
		[]byte(`{"code":0,"message":"success","voice_id":"voice"}`),
		[]byte(`{"code":0,"message":"success","voice_id":"voice","result":{"target_text":"hello"}}`),
		[]byte(`{"code":0,"message":"success","voice_id":"voice","final":1}`),
	}}
	adapter := NewTencentRESTAdapter(TencentRESTConfig{
		Credentials: staticTencentCredentialsProvider{TencentCredentials{SecretID: "id", SecretKey: "key"}},
		Now:         func() time.Time { return time.Unix(1673408372, 0).UTC() },
		Nonce:       func() string { return "1673408372" },
		VoiceID:     func() string { return "voice" },
		WebSocketDial: func(context.Context, string) (tencentWebSocketConnection, error) {
			return connection, nil
		},
		StreamPause: func(context.Context, time.Duration) error { return nil },
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderTencent, AuthScheme: "speech-translate-ws", Service: "asr", Operation: "TranslateStream", Method: http.MethodGet,
		URL: baseTencentSpeechTranslateTestURL(), Parameters: map[string]any{"source": "zh", "target": "en", "trans_model": "hunyuan-translation", "voice_format": 1}, BodyFile: audioFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RequestID != "voice" || !bytes.Contains(result.Output, []byte(`"target_text":"hello"`)) {
		t.Fatalf("result=%#v", result)
	}
}

func TestTencentSpeechTranslateWebSocketDoesNotPublishPartialTTSFailure(t *testing.T) {
	directory := t.TempDir()
	audioFile := filepath.Join(directory, "source.pcm")
	outputFile := filepath.Join(directory, "translated.mp3")
	if err := os.WriteFile(audioFile, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			[]byte(`{"code":0,"message":"success","voice_id":"voice"}`),
			[]byte("partial-audio"),
			[]byte(`{"code":4001,"message":"provider failure","voice_id":"voice"}`),
		},
		readTypes: []tencentWebSocketMessageType{tencentWebSocketMessageText, tencentWebSocketMessageBinary, tencentWebSocketMessageText},
	}
	adapter := NewTencentRESTAdapter(TencentRESTConfig{
		Credentials: staticTencentCredentialsProvider{TencentCredentials{SecretID: "id", SecretKey: "key"}},
		Now:         func() time.Time { return time.Unix(1673408372, 0).UTC() },
		Nonce:       func() string { return "1673408372" },
		VoiceID:     func() string { return "voice" },
		WebSocketDial: func(context.Context, string) (tencentWebSocketConnection, error) {
			return connection, nil
		},
		StreamPause: func(context.Context, time.Duration) error { return nil },
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderTencent, AuthScheme: "speech-translate-ws", Service: "asr", Operation: "TranslateStream", Method: http.MethodGet,
		URL: baseTencentSpeechTranslateTestURL(), Parameters: map[string]any{"source": "zh", "target": "en", "trans_model": "hunyuan-translation", "voice_format": 1, "enable_tts": 1, "codec": "mp3"},
		BodyFile: audioFile, ResponseFile: outputFile,
	})
	if err == nil || !strings.Contains(err.Error(), "code 4001") {
		t.Fatalf("error=%v", err)
	}
	if _, statErr := os.Stat(outputFile); !os.IsNotExist(statErr) {
		t.Fatalf("partial output was published: %v", statErr)
	}
}

func TestTencentVoiceConversionWebSocketStreamsFramedPCMAndPublishesOutput(t *testing.T) {
	directory := t.TempDir()
	inputFile := filepath.Join(directory, "source.pcm")
	outputFile := filepath.Join(directory, "converted.pcm")
	if err := os.WriteFile(inputFile, bytes.Repeat([]byte{0x44}, 4000), 0o600); err != nil {
		t.Fatal(err)
	}
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			tencentVoiceConversionTestFrame(t, "voice", 0, 0, nil),
			tencentVoiceConversionTestFrame(t, "voice", 0, 0, []byte("audio-1")),
			tencentVoiceConversionTestFrame(t, "voice", 0, 1, []byte("audio-2")),
		},
		readTypes: []tencentWebSocketMessageType{tencentWebSocketMessageBinary, tencentWebSocketMessageBinary, tencentWebSocketMessageBinary},
	}
	adapter := NewTencentRESTAdapter(TencentRESTConfig{
		Credentials: staticTencentCredentialsProvider{TencentCredentials{SecretID: "id", SecretKey: "key"}},
		Now:         func() time.Time { return time.Unix(1670304040, 0).UTC() },
		VoiceID:     func() string { return "voice" },
		WebSocketDial: func(_ context.Context, signedURL string) (tencentWebSocketConnection, error) {
			parsed, err := url.Parse(signedURL)
			if err != nil || parsed.Query().Get("Signature") == "" {
				t.Fatalf("signed URL=%q err=%v", signedURL, err)
			}
			return connection, nil
		},
		StreamPause: func(context.Context, time.Duration) error { return nil },
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderTencent, AuthScheme: "voice-convert-ws", Service: "vc", Operation: "ConvertVoice", Method: http.MethodGet,
		URL: "wss://tts.cloud.tencent.com/vc_stream/1259220000", Parameters: map[string]any{"VoiceType": 301005, "SampleRate": 16000, "Codec": "pcm"},
		BodyFile: inputFile, ResponseFile: outputFile, MaxResponseFileBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	converted, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(converted) != "audio-1audio-2" || result.RequestID != "voice" || !bytes.Contains(result.Output, []byte(`"format":"pcm"`)) {
		t.Fatalf("converted=%q result=%#v", converted, result)
	}
	if len(connection.writes) != 2 {
		t.Fatalf("writes=%d", len(connection.writes))
	}
	for index, want := range []struct {
		end   float64
		bytes int
	}{{end: 0, bytes: 3200}, {end: 1, bytes: 800}} {
		write := connection.writes[index]
		message, audio := decodeTencentVoiceConversionTestFrame(t, write.data)
		if write.messageType != tencentWebSocketMessageBinary || message["End"] != want.end || len(audio) != want.bytes {
			t.Fatalf("write[%d] type=%d message=%v audio=%d", index, write.messageType, message, len(audio))
		}
	}
}

func TestTencentVoiceConversionWebSocketDoesNotPublishPartialProviderFailure(t *testing.T) {
	directory := t.TempDir()
	inputFile := filepath.Join(directory, "source.pcm")
	outputFile := filepath.Join(directory, "converted.pcm")
	if err := os.WriteFile(inputFile, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			tencentVoiceConversionTestFrame(t, "voice", 0, 0, nil),
			tencentVoiceConversionTestFrame(t, "voice", 0, 0, []byte("partial")),
			tencentVoiceConversionTestFrame(t, "voice", 5000, 0, nil),
		},
		readTypes: []tencentWebSocketMessageType{tencentWebSocketMessageBinary, tencentWebSocketMessageBinary, tencentWebSocketMessageBinary},
	}
	adapter := NewTencentRESTAdapter(TencentRESTConfig{
		Credentials: staticTencentCredentialsProvider{TencentCredentials{SecretID: "id", SecretKey: "key"}},
		Now:         func() time.Time { return time.Unix(1670304040, 0).UTC() },
		VoiceID:     func() string { return "voice" },
		WebSocketDial: func(context.Context, string) (tencentWebSocketConnection, error) {
			return connection, nil
		},
		StreamPause: func(context.Context, time.Duration) error { return nil },
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderTencent, AuthScheme: "voice-convert-ws", Service: "vc", Operation: "ConvertVoice", Method: http.MethodGet,
		URL: "wss://tts.cloud.tencent.com/vc_stream/1259220000", Parameters: map[string]any{"VoiceType": 301005, "SampleRate": 16000, "Codec": "pcm"},
		BodyFile: inputFile, ResponseFile: outputFile,
	})
	if err == nil || !strings.Contains(err.Error(), "code 5000") {
		t.Fatalf("error=%v", err)
	}
	if _, statErr := os.Stat(outputFile); !os.IsNotExist(statErr) {
		t.Fatalf("partial output was published: %v", statErr)
	}
}

func tencentVoiceConversionTestFrame(t *testing.T, voiceID string, code, final int, audio []byte) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{"VoiceId": voiceID, "MessageId": "message", "Message": "success", "Final": final, "Code": code})
	if err != nil {
		t.Fatal(err)
	}
	frame := make([]byte, 4+len(body)+len(audio))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(body)))
	copy(frame[4:], body)
	copy(frame[4+len(body):], audio)
	return frame
}

func decodeTencentVoiceConversionTestFrame(t *testing.T, frame []byte) (map[string]any, []byte) {
	t.Helper()
	if len(frame) < 4 {
		t.Fatal("truncated voice conversion frame")
	}
	length := int(binary.BigEndian.Uint32(frame[:4]))
	if length <= 0 || length > len(frame)-4 {
		t.Fatal("invalid voice conversion frame length")
	}
	var message map[string]any
	if err := json.Unmarshal(frame[4:4+length], &message); err != nil {
		t.Fatal(err)
	}
	return message, frame[4+length:]
}

func baseTencentSpeechTranslateTestURL() string {
	return "wss://asr.cloud.tencent.com/asr/speech_translate/1259220000"
}

func TestTencentMPSWebSocketAdapterStreamsFramedAudioInternally(t *testing.T) {
	audioFile := filepath.Join(t.TempDir(), "audio.pcm")
	if err := os.WriteFile(audioFile, []byte("abcdefgh"), 0o600); err != nil {
		t.Fatal(err)
	}
	connection := &fakeTencentWebSocketConnection{reads: [][]byte{
		[]byte(`{"Code":0,"Message":"success","TaskId":"task-1"}`),
		[]byte(`{"Response":{"NotificationType":"AiRecognitionResult","TaskId":"task-1"}}`),
		[]byte(`{"Response":{"NotificationType":"ProcessEof","TaskId":"task-1","ProcessEofInfo":{"ErrCode":4002,"Message":"data timeout"}}}`),
	}}
	adapter := NewTencentRESTAdapter(TencentRESTConfig{
		Credentials: staticTencentCredentialsProvider{TencentCredentials{SecretID: "AKIDEXAMPLE", SecretKey: "testsecret"}},
		Now:         func() time.Time { return time.Unix(1750217009, 0).UTC() },
		MPSNonce:    func() string { return "7549145852" },
		WebSocketDial: func(_ context.Context, signedURL string) (tencentWebSocketConnection, error) {
			parsed, err := url.Parse(signedURL)
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Query().Get("signature") == "" || parsed.Query().Get("secretId") != "AKIDEXAMPLE" {
				t.Fatalf("signed query=%v", parsed.Query())
			}
			return connection, nil
		},
		StreamPause: func(context.Context, time.Duration) error { return nil },
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderTencent, AuthScheme: "mps-ws", Service: "mps", Operation: "RecognizeStream", Method: http.MethodGet,
		URL: "wss://mps.cloud.tencent.com/wss/v1/1258344699", Parameters: map[string]any{"asrDst": "zh"}, BodyFile: audioFile,
		StreamChunkBytes: 4, StreamUserID: "user-1", StreamFormat: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(result.Output, []byte(`"NotificationType":"AiRecognitionResult"`)) || result.RequestID != "task-1" {
		t.Fatalf("result=%#v", result)
	}
	if len(connection.writes) != 2 {
		t.Fatalf("writes=%#v", connection.writes)
	}
	for index, write := range connection.writes {
		if write.messageType != tencentWebSocketMessageBinary || len(write.data) < 16 || write.data[0] != 1 {
			t.Fatalf("write[%d]=%#v", index, write)
		}
	}
	if connection.writes[0].data[1] != 0 || connection.writes[1].data[1] != 1 {
		t.Fatalf("MPS IsEnd flags=%d,%d", connection.writes[0].data[1], connection.writes[1].data[1])
	}
}

func TestTencentMPSTTSWebSocketAdapterPublishesAudioInternally(t *testing.T) {
	outputFile := filepath.Join(t.TempDir(), "speech.pcm")
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			[]byte(`{"NotificationType":"Handshake","TaskId":"task-tts","HandshakeResult":{"Code":0,"Message":"success","Format":"pcm","SampleRate":16000}}`),
			[]byte("audio-1"),
			[]byte("audio-2"),
			[]byte(`{"NotificationType":"ProcessEof","TaskId":"task-tts","ProcessEofInfo":{"Code":0,"Message":"finished normally"}}`),
		},
		readTypes: []tencentWebSocketMessageType{tencentWebSocketMessageText, tencentWebSocketMessageBinary, tencentWebSocketMessageBinary, tencentWebSocketMessageText},
	}
	adapter := NewTencentRESTAdapter(TencentRESTConfig{
		Credentials: staticTencentCredentialsProvider{TencentCredentials{SecretID: "AKIDEXAMPLE", SecretKey: "testsecret"}},
		Now:         func() time.Time { return time.Unix(1782289476, 0).UTC() },
		MPSNonce:    func() string { return "6751517593" },
		WebSocketDial: func(_ context.Context, signedURL string) (tencentWebSocketConnection, error) {
			parsed, err := url.Parse(signedURL)
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Query().Get("signature") == "" || parsed.Query().Get("voiceId") != "voice/test+id" {
				t.Fatalf("signed query=%v", parsed.Query())
			}
			return connection, nil
		},
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderTencent, AuthScheme: "mps-tts-ws", Service: "mps", Operation: "SynthesizeSpeech", Method: http.MethodGet,
		URL: "wss://mps.cloud.tencent.com/tts/v1/1258344699", Parameters: map[string]any{"voiceId": "voice/test+id", "format": "pcm", "sampleRate": 16000},
		Body: []any{"hello", "world"}, ResponseFile: outputFile, MaxResponseFileBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	audio, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(audio) != "audio-1audio-2" || result.RequestID != "task-tts" || !bytes.Contains(result.Output, []byte(`"format":"pcm"`)) || !bytes.Contains(result.Output, []byte(`"sample_rate":16000`)) {
		t.Fatalf("audio=%q result=%#v", audio, result)
	}
	if len(connection.writes) != 3 || string(connection.writes[0].data) != `{"Text":"hello","Final":false}` || string(connection.writes[1].data) != `{"Text":"world","Final":false}` || string(connection.writes[2].data) != `{"Text":"","Final":true}` {
		t.Fatalf("writes=%#v", connection.writes)
	}
}

func TestTencentMPSTTSWebSocketDoesNotPublishProviderFailure(t *testing.T) {
	outputFile := filepath.Join(t.TempDir(), "failed.mp3")
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			[]byte(`{"NotificationType":"Handshake","TaskId":"task-tts","HandshakeResult":{"Code":0,"Message":"success","Format":"mp3","SampleRate":22050}}`),
			[]byte("partial-audio"),
			[]byte(`{"NotificationType":"ProcessEof","TaskId":"task-tts","ProcessEofInfo":{"Code":5000,"Message":"failed"}}`),
		},
		readTypes: []tencentWebSocketMessageType{tencentWebSocketMessageText, tencentWebSocketMessageBinary, tencentWebSocketMessageText},
	}
	adapter := NewTencentRESTAdapter(TencentRESTConfig{
		Credentials: staticTencentCredentialsProvider{TencentCredentials{SecretID: "id", SecretKey: "key"}},
		Now:         func() time.Time { return time.Unix(1782289476, 0).UTC() },
		MPSNonce:    func() string { return "6751517593" },
		WebSocketDial: func(context.Context, string) (tencentWebSocketConnection, error) {
			return connection, nil
		},
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderTencent, AuthScheme: "mps-tts-ws", Service: "mps", Operation: "SynthesizeSpeech", Method: http.MethodGet,
		URL: "wss://mps.cloud.tencent.com/tts/v1/1258344699", Parameters: map[string]any{"voiceId": "voice"}, Body: "hello", ResponseFile: outputFile,
	})
	if err == nil || !strings.Contains(err.Error(), "code 5000") {
		t.Fatalf("provider failure error=%v", err)
	}
	if _, statErr := os.Stat(outputFile); !os.IsNotExist(statErr) {
		t.Fatalf("failed output was published: %v", statErr)
	}
}

func TestTencentTTSWebSocketAdapterPublishesAudioAndMessages(t *testing.T) {
	outputFile := filepath.Join(t.TempDir(), "speech.pcm")
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			[]byte(`{"code":0,"message":"success","session_id":"session-tts","request_id":"request-tts","message_id":"message-1","final":0,"result":{"subtitles":null}}`),
			[]byte("audio-1"),
			[]byte(`{"code":0,"message":"success","session_id":"session-tts","request_id":"request-tts","message_id":"message-2","final":0,"result":{"subtitles":[{"Text":"hello","BeginTime":0,"EndTime":100}]}}`),
			[]byte("audio-2"),
			[]byte(`{"code":0,"message":"success","session_id":"session-tts","request_id":"request-tts","message_id":"message-3","final":1,"result":{"subtitles":null}}`),
		},
		readTypes: []tencentWebSocketMessageType{tencentWebSocketMessageText, tencentWebSocketMessageBinary, tencentWebSocketMessageText, tencentWebSocketMessageBinary, tencentWebSocketMessageText},
	}
	adapter := NewTencentRESTAdapter(TencentRESTConfig{
		Credentials: staticTencentCredentialsProvider{TencentCredentials{SecretID: "AKIDEXAMPLE", SecretKey: "testsecret"}},
		Now:         func() time.Time { return time.Unix(1688610905, 0).UTC() },
		VoiceID:     func() string { return "session-tts" },
		WebSocketDial: func(_ context.Context, signedURL string) (tencentWebSocketConnection, error) {
			parsed, err := url.Parse(signedURL)
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Query().Get("Signature") == "" || parsed.Query().Get("Text") != "hello" || parsed.Query().Get("SessionId") != "session-tts" {
				t.Fatalf("signed query=%v", parsed.Query())
			}
			return connection, nil
		},
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderTencent, AuthScheme: "tts-ws", Service: "tts", Operation: "SynthesizeSpeech", Method: http.MethodGet,
		URL: "wss://tts.cloud.tencent.com/stream_ws", Parameters: map[string]any{"AppId": 1300460000, "Codec": "pcm", "SampleRate": 16000, "EnableSubtitle": true},
		Body: "hello", ResponseFile: outputFile, MaxResponseFileBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	audio, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(audio) != "audio-1audio-2" || result.RequestID != "request-tts" || !bytes.Contains(result.Output, []byte(`"messages"`)) || !bytes.Contains(result.Output, []byte(`"format":"pcm"`)) || !bytes.Contains(result.Output, []byte(`"sample_rate":16000`)) {
		t.Fatalf("audio=%q result=%#v", audio, result)
	}
	if len(connection.writes) != 0 {
		t.Fatalf("unexpected writes=%#v", connection.writes)
	}
}

func TestTencentTTSWebSocketDoesNotPublishProviderFailure(t *testing.T) {
	outputFile := filepath.Join(t.TempDir(), "failed.mp3")
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			[]byte(`{"code":0,"message":"success","session_id":"session-tts","request_id":"request-tts","message_id":"message-1","final":0}`),
			[]byte("partial-audio"),
			[]byte(`{"code":10001,"message":"invalid VoiceType","session_id":"session-tts","request_id":"request-tts","message_id":"message-2","final":0}`),
		},
		readTypes: []tencentWebSocketMessageType{tencentWebSocketMessageText, tencentWebSocketMessageBinary, tencentWebSocketMessageText},
	}
	adapter := NewTencentRESTAdapter(TencentRESTConfig{
		Credentials: staticTencentCredentialsProvider{TencentCredentials{SecretID: "id", SecretKey: "key"}},
		Now:         func() time.Time { return time.Unix(1688610905, 0).UTC() },
		VoiceID:     func() string { return "session-tts" },
		WebSocketDial: func(context.Context, string) (tencentWebSocketConnection, error) {
			return connection, nil
		},
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderTencent, AuthScheme: "tts-ws", Service: "tts", Operation: "SynthesizeSpeech", Method: http.MethodGet,
		URL: "wss://tts.cloud.tencent.com/stream_ws", Parameters: map[string]any{"AppId": 1300460000, "Codec": "mp3"}, Body: "hello", ResponseFile: outputFile,
	})
	if err == nil || !strings.Contains(err.Error(), "code 10001") {
		t.Fatalf("provider failure error=%v", err)
	}
	if _, statErr := os.Stat(outputFile); !os.IsNotExist(statErr) {
		t.Fatalf("failed output was published: %v", statErr)
	}
}

func TestTencentStreamingTTSWebSocketAdapterSendsTextAndPublishesAudio(t *testing.T) {
	outputFile := filepath.Join(t.TempDir(), "speech.mp3")
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			[]byte(`{"code":0,"message":"success","session_id":"session-tts","request_id":"request-tts","message_id":"event-1","final":0}`),
			[]byte(`{"code":0,"message":"success","session_id":"session-tts","request_id":"request-tts","message_id":"event-2","ready":1,"final":0}`),
			[]byte("audio-1"),
			[]byte(`{"code":0,"message":"success","session_id":"session-tts","request_id":"request-tts","message_id":"event-3","final":0,"result":{"subtitles":[]}}`),
			[]byte("audio-2"),
			[]byte(`{"code":0,"message":"success","session_id":"session-tts","request_id":"request-tts","message_id":"event-4","final":1}`),
		},
		readTypes: []tencentWebSocketMessageType{tencentWebSocketMessageText, tencentWebSocketMessageText, tencentWebSocketMessageBinary, tencentWebSocketMessageText, tencentWebSocketMessageBinary, tencentWebSocketMessageText},
	}
	adapter := NewTencentRESTAdapter(TencentRESTConfig{
		Credentials: staticTencentCredentialsProvider{TencentCredentials{SecretID: "id", SecretKey: "key"}},
		Now:         func() time.Time { return time.Unix(1688610905, 0).UTC() },
		VoiceID:     func() string { return "session-tts" },
		WebSocketDial: func(_ context.Context, signedURL string) (tencentWebSocketConnection, error) {
			parsed, err := url.Parse(signedURL)
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Query().Get("Signature") == "" || parsed.Query().Get("Action") != "TextToStreamAudioWSv2" {
				t.Fatalf("signed query=%v", parsed.Query())
			}
			return connection, nil
		},
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderTencent, AuthScheme: "tts-stream-ws", Service: "tts", Operation: "SynthesizeSpeechStream", Method: http.MethodGet,
		URL: "wss://tts.cloud.tencent.com/stream_wsv2", Parameters: map[string]any{"AppId": 1300460000, "Codec": "mp3", "SampleRate": 16000},
		Body: []any{"hello, ", "world!"}, ResponseFile: outputFile, MaxResponseFileBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	audio, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(audio) != "audio-1audio-2" || result.RequestID != "request-tts" || !bytes.Contains(result.Output, []byte(`"format":"mp3"`)) {
		t.Fatalf("audio=%q result=%#v", audio, result)
	}
	if len(connection.writes) != 3 {
		t.Fatalf("writes=%#v", connection.writes)
	}
	seenIDs := map[string]bool{}
	for index, write := range connection.writes {
		var command struct {
			SessionID string `json:"session_id"`
			MessageID string `json:"message_id"`
			Action    string `json:"action"`
			Data      string `json:"data"`
		}
		if write.messageType != tencentWebSocketMessageText || json.Unmarshal(write.data, &command) != nil || command.SessionID != "session-tts" || command.MessageID == "" || seenIDs[command.MessageID] {
			t.Fatalf("write[%d]=%s", index, write.data)
		}
		seenIDs[command.MessageID] = true
		if index < 2 && (command.Action != "ACTION_SYNTHESIS" || command.Data != []string{"hello, ", "world!"}[index]) {
			t.Fatalf("write[%d]=%s", index, write.data)
		}
		if index == 2 && (command.Action != "ACTION_COMPLETE" || command.Data != "") {
			t.Fatalf("complete=%s", write.data)
		}
	}
}

func TestTencentStreamingTTSWebSocketDoesNotPublishProviderFailure(t *testing.T) {
	outputFile := filepath.Join(t.TempDir(), "failed.pcm")
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			[]byte(`{"code":0,"session_id":"session-tts","request_id":"request-tts","message_id":"event-1","final":0}`),
			[]byte(`{"code":0,"session_id":"session-tts","request_id":"request-tts","message_id":"event-2","ready":1,"final":0}`),
			[]byte("partial-audio"),
			[]byte(`{"code":10001,"message":"invalid VoiceType","session_id":"session-tts","request_id":"request-tts","message_id":"event-3","final":0}`),
		},
		readTypes: []tencentWebSocketMessageType{tencentWebSocketMessageText, tencentWebSocketMessageText, tencentWebSocketMessageBinary, tencentWebSocketMessageText},
	}
	adapter := NewTencentRESTAdapter(TencentRESTConfig{
		Credentials: staticTencentCredentialsProvider{TencentCredentials{SecretID: "id", SecretKey: "key"}},
		Now:         func() time.Time { return time.Unix(1688610905, 0).UTC() }, VoiceID: func() string { return "session-tts" },
		WebSocketDial: func(context.Context, string) (tencentWebSocketConnection, error) { return connection, nil },
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderTencent, AuthScheme: "tts-stream-ws", Service: "tts", Operation: "SynthesizeSpeechStream", Method: http.MethodGet,
		URL: "wss://tts.cloud.tencent.com/stream_wsv2", Parameters: map[string]any{"AppId": 1300460000, "Codec": "pcm"}, Body: "hello!", ResponseFile: outputFile,
	})
	if err == nil || !strings.Contains(err.Error(), "code 10001") {
		t.Fatalf("provider failure error=%v", err)
	}
	if _, statErr := os.Stat(outputFile); !os.IsNotExist(statErr) {
		t.Fatalf("failed output was published: %v", statErr)
	}
}

func TestTencentPodcastWebSocketAdapterSendsObjectsAndPublishesAudio(t *testing.T) {
	outputFile := filepath.Join(t.TempDir(), "podcast.pcm")
	connection := &fakeTencentWebSocketConnection{
		reads: [][]byte{
			[]byte(`{"code":0,"message":"success","session_id":"session-podcast","request_id":"request-podcast","message_id":"event-1","ready":0,"final":0}`),
			[]byte(`{"code":0,"message":"success","session_id":"session-podcast","request_id":"request-podcast","message_id":"event-2","ready":1,"final":0}`),
			[]byte("audio-1"),
			[]byte(`{"code":0,"message":"success","session_id":"session-podcast","request_id":"request-podcast","message_id":"event-3","final":0,"result":{"scripts":[{"Text":"script","Speaker":"host","BeginTime":0,"EndTime":1000,"Index":0}]}}`),
			[]byte(`{"code":10009,"message":"input timeout notification","session_id":"session-podcast","request_id":"request-podcast","message_id":"event-timeout","final":0}`),
			[]byte("audio-2"),
			[]byte(`{"code":0,"message":"success","session_id":"session-podcast","request_id":"request-podcast","message_id":"event-4","final":1}`),
		},
		readTypes: []tencentWebSocketMessageType{tencentWebSocketMessageText, tencentWebSocketMessageText, tencentWebSocketMessageBinary, tencentWebSocketMessageText, tencentWebSocketMessageText, tencentWebSocketMessageBinary, tencentWebSocketMessageText},
	}
	adapter := NewTencentRESTAdapter(TencentRESTConfig{
		Credentials: staticTencentCredentialsProvider{TencentCredentials{SecretID: "id", SecretKey: "key"}},
		Now:         func() time.Time { return time.Unix(1761816664, 0).UTC() }, VoiceID: func() string { return "session-podcast" },
		WebSocketDial: func(_ context.Context, signedURL string) (tencentWebSocketConnection, error) {
			parsed, err := url.Parse(signedURL)
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Query().Get("Signature") == "" || parsed.Query().Get("Action") != "TextToPodcastStreamAudioWS" {
				t.Fatalf("signed query=%v", parsed.Query())
			}
			return connection, nil
		},
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderTencent, AuthScheme: "podcast-ws", Service: "tts", Operation: "TextToPodcastStreamAudioWS", Method: http.MethodGet,
		URL: "wss://tts.cloud.tencent.com/stream_ws_podcast", Parameters: map[string]any{"AppId": 1300466766, "Codec": "pcm", "SampleRate": 24000},
		Body: []any{map[string]any{"ObjectType": "TYPE_TEXT", "Text": "first"}, map[string]any{"ObjectType": "TYPE_TEXT", "Text": "second"}}, ResponseFile: outputFile, MaxResponseFileBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	audio, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(audio) != "audio-1audio-2" || result.RequestID != "request-podcast" || !bytes.Contains(result.Output, []byte(`"sample_rate":24000`)) {
		t.Fatalf("audio=%q result=%#v", audio, result)
	}
	if len(connection.writes) != 3 {
		t.Fatalf("writes=%#v", connection.writes)
	}
	for index, write := range connection.writes {
		var command struct {
			SessionID string `json:"session_id"`
			MessageID string `json:"message_id"`
			Action    string `json:"action"`
			Data      string `json:"data"`
		}
		if write.messageType != tencentWebSocketMessageText || json.Unmarshal(write.data, &command) != nil || command.SessionID != "session-podcast" || command.MessageID == "" {
			t.Fatalf("write[%d]=%s", index, write.data)
		}
		if index < 2 {
			var object map[string]any
			if command.Action != "ACTION_SYNTHESIS" || json.Unmarshal([]byte(command.Data), &object) != nil || object["ObjectType"] != "TYPE_TEXT" || object["Text"] != []string{"first", "second"}[index] {
				t.Fatalf("write[%d]=%s", index, write.data)
			}
		} else if command.Action != "ACTION_COMPLETE" || command.Data != "" {
			t.Fatalf("complete=%s", write.data)
		}
	}
}

func TestTencentWebSocketOutputSinkAtomicallyPublishesNDJSON(t *testing.T) {
	target := filepath.Join(t.TempDir(), "result.ndjson")
	sink, err := newTencentWebSocketOutputSink(Invocation{ResponseFile: target, MaxResponseFileBytes: 1024}, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer sink.abort()
	if err := sink.writeMessage([]byte(`{"code":0}`)); err != nil {
		t.Fatal(err)
	}
	metadata, err := sink.finish("voice-id")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{\"code\":0}\n" || !bytes.Contains(metadata, []byte(`"content_type":"application/x-ndjson"`)) || !bytes.Contains(metadata, []byte(`"request_id":"voice-id"`)) {
		t.Fatalf("data=%q metadata=%s", data, metadata)
	}
	if info, err := os.Stat(target); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("response file info=%v err=%v", info, err)
	}
}

func TestAlibabaOSS4AndTencentCOSDataPlaneSchemes(t *testing.T) {
	ossRequest, err := http.NewRequest(http.MethodGet, "https://bucket.oss-cn-hangzhou.aliyuncs.com/object?acl", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := signAlibabaOSSV4(ossRequest, AlibabaCredentials{AccessKeyID: "ak", AccessKeySecret: "sk", SecurityToken: "sts"}, "cn-hangzhou", time.Date(2025, 4, 11, 6, 41, 24, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if got := ossRequest.Header.Get("Authorization"); !strings.HasPrefix(got, "OSS4-HMAC-SHA256 Credential=ak/20250411/cn-hangzhou/oss/aliyun_v4_request,Signature=") || strings.Contains(got, "AdditionalHeaders=") {
		t.Fatalf("OSS authorization=%q", got)
	}
	if got := canonicalOSSQuery(ossRequest.URL); got != "acl" {
		t.Fatalf("OSS canonical query=%q", got)
	}

	cosRequest, err := http.NewRequest(http.MethodGet, "https://bucket-1250000000.cos.ap-beijing.myqcloud.com/object?response-content-type=application%2Foctet-stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := signTencentCOS(cosRequest, TencentCredentials{SecretID: "id", SecretKey: "key", Token: "cam-token"}, time.Unix(1557989753, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	authorization := cosRequest.Header.Get("Authorization")
	if !strings.Contains(authorization, "q-header-list=date;host;x-cos-security-token") || !strings.Contains(authorization, "q-url-param-list=response-content-type") || !strings.Contains(authorization, "q-signature=") {
		t.Fatalf("COS authorization=%q", authorization)
	}
}

func TestAlibabaOSSV1MatchesOfficialGoSDKFixedVector(t *testing.T) {
	request, err := http.NewRequest(http.MethodPut, "https://examplebucket.oss-cn-hangzhou.aliyuncs.com/nelson?acl&prefix=ignored", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-MD5", "eB5eJF1ptWaXm4bijSPyxw==")
	request.Header.Set("Content-Type", "text/html")
	request.Header.Set("X-Oss-Date", "Wed, 28 Dec 2022 10:27:41 GMT")
	request.Header.Set("X-Oss-Meta-Author", "alice")
	request.Header.Set("X-Oss-Meta-Magic", "abracadabra")

	if err := signAlibabaOSSV1(request, AlibabaCredentials{AccessKeyID: "ak", AccessKeySecret: "sk"}, time.Date(2022, 12, 28, 10, 27, 41, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if got, want := request.Header.Get("Authorization"), "OSS ak:/afkugFbmWDQ967j1vr6zygBLQk="; got != want {
		t.Fatalf("authorization=%q, want %q", got, want)
	}
	if got, want := canonicalAlibabaOSSV1Resource(request.URL), "/examplebucket/nelson?acl"; got != want {
		t.Fatalf("canonical resource=%q, want %q", got, want)
	}
}

func TestAlibabaOSSV1SignsSTSAndCurrentSDKSubresources(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "https://examplebucket.oss-cn-hangzhou.aliyuncs.com/?resourceGroup&non-resource=ignored", nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2022, 12, 28, 10, 27, 41, 0, time.UTC)
	if err := signAlibabaOSSV1(request, AlibabaCredentials{AccessKeyID: "ak", AccessKeySecret: "sk", SecurityToken: "sts-token"}, now); err != nil {
		t.Fatal(err)
	}
	if got := request.Header.Get("X-Oss-Security-Token"); got != "sts-token" {
		t.Fatalf("security token=%q", got)
	}
	if got := canonicalAlibabaOSSV1Resource(request.URL); got != "/examplebucket/?resourceGroup" {
		t.Fatalf("canonical resource=%q", got)
	}
	if got := request.Header.Get("Authorization"); !strings.HasPrefix(got, "OSS ak:") {
		t.Fatalf("authorization=%q", got)
	}
}

func TestAzureDefaultCredentialChainContainsNoCLIProvider(t *testing.T) {
	adapter := NewAzureRESTAdapter(AzureRESTConfig{})
	if _, ok := adapter.config.Tokens.(*azureNonCLITokenProvider); !ok {
		t.Fatalf("default token provider=%T, want non-CLI Azure Identity chain", adapter.config.Tokens)
	}
}

func TestGCPDefaultCredentialChainContainsNoCLIProvider(t *testing.T) {
	adapter := NewGCPRESTAdapter(GCPRESTConfig{})
	if _, ok := adapter.config.Tokens.(*gcpADCTokenProvider); !ok {
		t.Fatalf("default token provider=%T, want ADC only", adapter.config.Tokens)
	}
}

func httpResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

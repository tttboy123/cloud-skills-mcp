package cloud

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGCPGRPCInvocationBoundarySupportsGenericHTTP2Methods(t *testing.T) {
	directory := t.TempDir()
	requestFile := filepath.Join(directory, "request.grpc")
	responseFile := filepath.Join(directory, "response.grpc")
	if err := os.WriteFile(requestFile, appendGRPCFrame(nil, []byte("config")), 0o600); err != nil {
		t.Fatal(err)
	}
	request := Invocation{
		Provider: ProviderGCP, Mode: ModeRead, AuthScheme: "grpc", Service: "speech", Operation: "StreamingRecognize",
		Method: http.MethodPost, URL: "https://speech.googleapis.com/google.cloud.speech.v2.Speech/StreamingRecognize",
		Project: "project-1", Headers: map[string]string{"x-goog-request-params": "recognizer=projects/project-1/locations/global/recognizers/_"},
		BodyFile: requestFile, ResponseFile: responseFile, StreamIntervalMS: 20,
	}
	if err := validateInvocation(request, []string{directory}); err != nil {
		t.Fatal(err)
	}
	if !classifyRead(ProviderGCP, request) {
		t.Fatal("GCP gRPC StreamingRecognize was not classified as read-only")
	}

	for name, mutate := range map[string]func(*Invocation){
		"get method":          func(value *Invocation) { value.Method = http.MethodGet },
		"query parameters":    func(value *Invocation) { value.Parameters = map[string]any{"key": "value"} },
		"query in URL":        func(value *Invocation) { value.URL += "?alt=proto" },
		"wrong path":          func(value *Invocation) { value.URL = "https://speech.googleapis.com/v2/speech:recognize" },
		"missing request":     func(value *Invocation) { value.BodyFile = "" },
		"inline body":         func(value *Invocation) { value.Body = map[string]any{"audio": "value"} },
		"missing response":    func(value *Invocation) { value.ResponseFile = "" },
		"protocol header":     func(value *Invocation) { value.Headers = map[string]string{"grpc-encoding": "gzip"} },
		"REST payload mode":   func(value *Invocation) { value.PayloadMode = "aws-eventstream" },
		"unknown auth scheme": func(value *Invocation) { value.AuthScheme = "unknown" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := request
			candidate.Headers = map[string]string{"x-goog-request-params": "recognizer=projects/project-1/locations/global/recognizers/_"}
			mutate(&candidate)
			if err := validateInvocation(candidate, []string{directory}); err == nil {
				t.Fatal("invalid GCP gRPC invocation accepted")
			}
		})
	}
}

func TestGCPGRPCFrameReaderValidatesAndPacesMessages(t *testing.T) {
	data := appendGRPCFrame(nil, []byte("one"))
	data = appendGRPCFrame(data, []byte("two"))
	pauses := 0
	reader := newGCPGRPCFrameReader(io.NopCloser(bytes.NewReader(data)), func(context.Context, time.Duration) error {
		pauses++
		return nil
	}, time.Millisecond)
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output, data) || pauses != 1 {
		t.Fatalf("output=%x pauses=%d", output, pauses)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}

	for name, data := range map[string][]byte{
		"compressed": {1, 0, 0, 0, 0},
		"truncated":  {0, 0, 0, 0, 3, 1},
		"oversized":  {0, 0xff, 0xff, 0xff, 0xff},
	} {
		t.Run(name, func(t *testing.T) {
			reader := newGCPGRPCFrameReader(io.NopCloser(bytes.NewReader(data)), func(context.Context, time.Duration) error { return nil }, 0)
			if _, err := io.ReadAll(reader); err == nil {
				t.Fatal("invalid gRPC frame stream accepted")
			}
		})
	}
}

func TestGCPAdapterStreamsRawGRPCOverADCAuthenticatedHTTP2(t *testing.T) {
	directory := t.TempDir()
	requestData := appendGRPCFrame(nil, []byte("configuration"))
	requestData = appendGRPCFrame(requestData, []byte("audio"))
	requestFile := filepath.Join(directory, "request.grpc")
	responseFile := filepath.Join(directory, "response.grpc")
	if err := os.WriteFile(requestFile, requestData, 0o600); err != nil {
		t.Fatal(err)
	}
	responseData := appendGRPCFrame(nil, []byte("transcript"))
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.Path != "/google.cloud.speech.v2.Speech/StreamingRecognize" {
			return nil, fmt.Errorf("unexpected request %s %s", request.Method, request.URL)
		}
		if request.Header.Get("Authorization") != "Bearer adc-token" || request.Header.Get("Content-Type") != "application/grpc+proto" || !strings.EqualFold(request.Header.Get("TE"), "trailers") || request.Header.Get("X-Goog-User-Project") != "project-1" {
			return nil, fmt.Errorf("unexpected headers %#v", request.Header)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(body, requestData) {
			return nil, fmt.Errorf("request body=%x want=%x", body, requestData)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			ProtoMajor: 2,
			Header: http.Header{
				"Content-Type":      []string{"application/grpc+proto"},
				"X-Goog-Request-Id": []string{"request-1"},
			},
			Body:    io.NopCloser(bytes.NewReader(responseData)),
			Trailer: http.Header{"Grpc-Status": []string{"0"}},
		}, nil
	})
	adapter := NewGCPRESTAdapter(GCPRESTConfig{
		Tokens: &staticTokenProvider{token: "adc-token"}, GRPCHTTP: doer,
		StreamPause: func(context.Context, time.Duration) error { return nil },
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderGCP, Mode: ModeRead, AuthScheme: "grpc", Service: "speech", Operation: "StreamingRecognize",
		Method: http.MethodPost, URL: "https://speech.googleapis.com/google.cloud.speech.v2.Speech/StreamingRecognize",
		Project: "project-1", BodyFile: requestFile, ResponseFile: responseFile, MaxResponseFileBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RequestID != "request-1" {
		t.Fatalf("request id=%q", result.RequestID)
	}
	written, err := os.ReadFile(responseFile)
	if err != nil || !bytes.Equal(written, responseData) {
		t.Fatalf("written=%x err=%v", written, err)
	}
	var metadata map[string]any
	if err := json.Unmarshal(result.Output, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["content_type"] != "application/grpc+proto" || metadata["response_file"] != responseFile || strings.Contains(string(result.Output), "adc-token") {
		t.Fatalf("metadata=%s", result.Output)
	}
}

func TestGCPGRPCFailuresNeverPublishResponseFile(t *testing.T) {
	for name, response := range map[string]*http.Response{
		"http1 response": {
			StatusCode: http.StatusOK, ProtoMajor: 1, Header: http.Header{"Content-Type": []string{"application/grpc+proto"}},
			Body: io.NopCloser(bytes.NewReader(appendGRPCFrame(nil, []byte("response")))), Trailer: http.Header{"Grpc-Status": []string{"0"}},
		},
		"grpc status": {
			StatusCode: http.StatusOK, ProtoMajor: 2, Header: http.Header{"Content-Type": []string{"application/grpc+proto"}},
			Body: io.NopCloser(bytes.NewReader(appendGRPCFrame(nil, []byte("partial")))), Trailer: http.Header{"Grpc-Status": []string{"3"}},
		},
		"malformed frame": {
			StatusCode: http.StatusOK, ProtoMajor: 2, Header: http.Header{"Content-Type": []string{"application/grpc+proto"}},
			Body: io.NopCloser(bytes.NewReader([]byte{0, 0, 0, 0, 3, 1})), Trailer: http.Header{"Grpc-Status": []string{"0"}},
		},
		"missing trailer": {
			StatusCode: http.StatusOK, ProtoMajor: 2, Header: http.Header{"Content-Type": []string{"application/grpc+proto"}},
			Body: io.NopCloser(bytes.NewReader(nil)), Trailer: make(http.Header),
		},
	} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			requestFile := filepath.Join(directory, "request.grpc")
			responseFile := filepath.Join(directory, "response.grpc")
			if err := os.WriteFile(requestFile, appendGRPCFrame(nil, []byte("request")), 0o600); err != nil {
				t.Fatal(err)
			}
			adapter := NewGCPRESTAdapter(GCPRESTConfig{
				Tokens:      &staticTokenProvider{token: "token"},
				GRPCHTTP:    doerFunc(func(*http.Request) (*http.Response, error) { return response, nil }),
				StreamPause: func(context.Context, time.Duration) error { return nil },
			})
			_, err := adapter.Invoke(t.Context(), Invocation{
				Provider: ProviderGCP, AuthScheme: "grpc", Service: "speech", Operation: "StreamingRecognize",
				Method: http.MethodPost, URL: "https://speech.googleapis.com/google.cloud.speech.v2.Speech/StreamingRecognize",
				BodyFile: requestFile, ResponseFile: responseFile, MaxResponseFileBytes: 1024,
			})
			if err == nil {
				t.Fatal("failed gRPC response accepted")
			}
			if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
				t.Fatalf("partial response was published: %v", statErr)
			}
		})
	}
}

func TestGCPGRPCRejectsMalformedRequestBeforeCredentialsOrNetwork(t *testing.T) {
	directory := t.TempDir()
	requestFile := filepath.Join(directory, "request.grpc")
	responseFile := filepath.Join(directory, "response.grpc")
	if err := os.WriteFile(requestFile, []byte{0, 0, 0, 0, 4, 1}, 0o600); err != nil {
		t.Fatal(err)
	}
	tokens := &staticTokenProvider{token: "must-not-be-used"}
	doerCalled := false
	adapter := NewGCPRESTAdapter(GCPRESTConfig{
		Tokens: tokens,
		GRPCHTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			doerCalled = true
			return nil, fmt.Errorf("must not be called")
		}),
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderGCP, AuthScheme: "grpc", Service: "speech", Operation: "StreamingRecognize",
		Method: http.MethodPost, URL: "https://speech.googleapis.com/google.cloud.speech.v2.Speech/StreamingRecognize",
		BodyFile: requestFile, ResponseFile: responseFile,
	})
	if err == nil || tokens.calls != 0 || doerCalled {
		t.Fatalf("err=%v token calls=%d doer=%v", err, tokens.calls, doerCalled)
	}
}

func TestGCPGRPCDefaultHTTPClientHasNoWholeStreamTimeout(t *testing.T) {
	adapter := NewGCPRESTAdapter(GCPRESTConfig{Timeout: time.Second})
	restClient, ok := adapter.config.HTTP.(*http.Client)
	if !ok || restClient.Timeout != time.Second {
		t.Fatalf("REST client=%#v", adapter.config.HTTP)
	}
	grpcClient, ok := adapter.config.GRPCHTTP.(*http.Client)
	if !ok || grpcClient.Timeout != 0 || grpcClient == restClient {
		t.Fatalf("gRPC client=%#v", adapter.config.GRPCHTTP)
	}
}

func appendGRPCFrame(target, payload []byte) []byte {
	header := make([]byte, 5)
	binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
	target = append(target, header...)
	return append(target, payload...)
}

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

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/anypb"
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

func TestGCPGRPCInvocationBoundarySupportsSchemaDrivenProtoJSON(t *testing.T) {
	directory := t.TempDir()
	descriptorFile := writeGCPGRPCTestDescriptorSet(t, directory)
	responseFile := filepath.Join(directory, "response.ndjson")
	request := Invocation{
		Provider: ProviderGCP, Mode: ModeRead, AuthScheme: "grpc", Service: "test", Operation: "Echo",
		Method: http.MethodPost, URL: "https://example.googleapis.com/test.v1.TestService/Echo",
		PayloadMode: "protobuf-json", ProtobufDescriptorFile: descriptorFile,
		Body: map[string]any{"name": "Lune", "resourceId": "9007199254740993"}, ResponseFile: responseFile,
	}
	if err := validateInvocation(request, []string{directory}); err != nil {
		t.Fatal(err)
	}

	for name, mutate := range map[string]func(*Invocation){
		"missing descriptor": func(value *Invocation) { value.ProtobufDescriptorFile = "" },
		"descriptor outside roots": func(value *Invocation) {
			outside := t.TempDir()
			value.ProtobufDescriptorFile = writeGCPGRPCTestDescriptorSet(t, outside)
		},
		"descriptor directory": func(value *Invocation) { value.ProtobufDescriptorFile = directory },
		"missing body":         func(value *Invocation) { value.Body = nil },
		"raw body file":        func(value *Invocation) { value.BodyFile = descriptorFile },
		"credential body":      func(value *Invocation) { value.Body = map[string]any{"accessToken": "caller"} },
		"descriptor in raw mode": func(value *Invocation) {
			value.PayloadMode = ""
			value.Body = nil
			value.BodyFile = descriptorFile
		},
		"message bound only": func(value *Invocation) { value.StreamMaxMessages = 1 },
		"timeout bound only": func(value *Invocation) { value.StreamTimeoutSeconds = 1 },
		"message bound high": func(value *Invocation) {
			value.StreamMaxMessages = 257
			value.StreamTimeoutSeconds = 1
		},
		"timeout bound high": func(value *Invocation) {
			value.StreamMaxMessages = 1
			value.StreamTimeoutSeconds = 301
		},
		"other provider": func(value *Invocation) {
			value.Provider = ProviderAWS
			value.AuthScheme = "sigv4"
			value.Region = "us-east-1"
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := request
			mutate(&candidate)
			if err := validateInvocation(candidate, []string{directory}); err == nil {
				t.Fatal("unsafe schema-driven gRPC invocation accepted")
			}
		})
	}
}

func TestGCPGRPCStreamBoundsAreNeverIgnoredByOtherTransports(t *testing.T) {
	request := Invocation{
		Provider: ProviderAWS, Mode: ModeMutate, AuthScheme: "sigv4", Service: "execute-api", Operation: "Invoke",
		Region: "us-east-1", Method: http.MethodPost, URL: "https://example.execute-api.us-east-1.amazonaws.com/resource",
		StreamMaxMessages: 8, StreamTimeoutSeconds: 30,
	}
	if err := validateInvocation(request, nil); err == nil {
		t.Fatal("non-gRPC transport silently ignored Google Cloud stream bounds")
	}
}

func TestGCPGRPCClassifiesOnlyKnownLongLivedObservationMethodsAsReadOnly(t *testing.T) {
	for name, test := range map[string]struct {
		service   string
		operation string
		rawURL    string
		readOnly  bool
	}{
		"Firestore Listen": {
			service: "firestore", operation: "Listen",
			rawURL: "https://firestore.googleapis.com/google.firestore.v1.Firestore/Listen", readOnly: true,
		},
		"Logging TailLogEntries": {
			service: "logging", operation: "TailLogEntries",
			rawURL: "https://logging.googleapis.com/google.logging.v2.LoggingServiceV2/TailLogEntries", readOnly: true,
		},
		"PubSub StreamingPull can acknowledge": {
			service: "pubsub", operation: "StreamingPull",
			rawURL: "https://pubsub.googleapis.com/google.pubsub.v1.Subscriber/StreamingPull", readOnly: false,
		},
		"lookalike Listen": {
			service: "example", operation: "Listen",
			rawURL: "https://example.googleapis.com/example.v1.MutableService/Listen", readOnly: false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			request := Invocation{Provider: ProviderGCP, AuthScheme: "grpc", Service: test.service, Operation: test.operation, Method: http.MethodPost, URL: test.rawURL}
			if got := classifyRead(ProviderGCP, request); got != test.readOnly {
				t.Fatalf("classifyRead=%v want %v", got, test.readOnly)
			}
		})
	}
}

func TestGCPAdapterEncodesAndDecodesSchemaDrivenProtoJSONOverHTTP2(t *testing.T) {
	directory := t.TempDir()
	descriptorFile := writeGCPGRPCTestDescriptorSet(t, directory)
	responseFile := filepath.Join(directory, "response.ndjson")
	wantRequest := protowire.AppendTag(nil, 1, protowire.BytesType)
	wantRequest = protowire.AppendString(wantRequest, "Lune")
	wantRequest = protowire.AppendTag(wantRequest, 2, protowire.VarintType)
	wantRequest = protowire.AppendVarint(wantRequest, 9007199254740993)
	responsePayload := protowire.AppendTag(nil, 1, protowire.BytesType)
	responsePayload = protowire.AppendString(responsePayload, "hello")
	responsePayload = protowire.AppendTag(responsePayload, 2, protowire.VarintType)
	responsePayload = protowire.AppendVarint(responsePayload, 9007199254740993)
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		data, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(data, appendGRPCFrame(nil, wantRequest)) {
			return nil, fmt.Errorf("request body=%x want=%x", data, appendGRPCFrame(nil, wantRequest))
		}
		if request.Header.Get("Authorization") != "Bearer adc-token" || request.Header.Get("Content-Type") != "application/grpc+proto" {
			return nil, fmt.Errorf("request headers=%#v", request.Header)
		}
		return &http.Response{
			StatusCode: http.StatusOK, ProtoMajor: 2,
			Header: http.Header{"Content-Type": []string{"application/grpc+proto"}, "X-Goog-Request-Id": []string{"request-json-1"}},
			Body:   io.NopCloser(bytes.NewReader(appendGRPCFrame(nil, responsePayload))), Trailer: http.Header{"Grpc-Status": []string{"0"}},
		}, nil
	})
	adapter := NewGCPRESTAdapter(GCPRESTConfig{Tokens: &staticTokenProvider{token: "adc-token"}, GRPCHTTP: doer})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderGCP, Mode: ModeRead, AuthScheme: "grpc", Service: "test", Operation: "Echo",
		Method: http.MethodPost, URL: "https://example.googleapis.com/test.v1.TestService/Echo",
		PayloadMode: "protobuf-json", ProtobufDescriptorFile: descriptorFile,
		Body:         map[string]any{"name": "Lune", "resourceId": "9007199254740993"},
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(responseFile)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if json.Unmarshal(bytes.TrimSpace(data), &decoded) != nil || decoded["message"] != "hello" || decoded["resourceId"] != "9007199254740993" {
		t.Fatalf("response=%s decoded=%v", data, decoded)
	}
	if result.RequestID != "request-json-1" || !bytes.Contains(result.Output, []byte(`"content_type":"application/x-ndjson"`)) || strings.Contains(string(result.Output), "adc-token") || strings.Contains(string(result.Output), descriptorFile) || !bytes.Contains(result.Output, []byte(responseFile)) {
		t.Fatalf("result=%s request_id=%q", result.Output, result.RequestID)
	}
}

func TestGCPAdapterSchemaDrivenProtoJSONSupportsFiniteBidirectionalStreams(t *testing.T) {
	directory := t.TempDir()
	descriptorFile := writeGCPGRPCTestDescriptorSet(t, directory)
	responseFile := filepath.Join(directory, "stream.ndjson")
	responseOne := appendGRPCTestResponsePayload(nil, "one", 1)
	responseTwo := appendGRPCTestResponsePayload(nil, "two", 2)
	pauses := 0
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		data, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		requestOne := protowire.AppendTag(nil, 1, protowire.BytesType)
		requestOne = protowire.AppendString(requestOne, "one")
		requestOne = protowire.AppendTag(requestOne, 2, protowire.VarintType)
		requestOne = protowire.AppendVarint(requestOne, 1)
		requestTwo := protowire.AppendTag(nil, 1, protowire.BytesType)
		requestTwo = protowire.AppendString(requestTwo, "two")
		requestTwo = protowire.AppendTag(requestTwo, 2, protowire.VarintType)
		requestTwo = protowire.AppendVarint(requestTwo, 2)
		want := appendGRPCFrame(nil, requestOne)
		want = appendGRPCFrame(want, requestTwo)
		if !bytes.Equal(data, want) {
			return nil, fmt.Errorf("request body=%x want=%x", data, want)
		}
		responseData := appendGRPCFrame(nil, responseOne)
		responseData = appendGRPCFrame(responseData, responseTwo)
		return &http.Response{StatusCode: 200, ProtoMajor: 2, Header: http.Header{"Content-Type": []string{"application/grpc+proto"}}, Body: io.NopCloser(bytes.NewReader(responseData)), Trailer: http.Header{"Grpc-Status": []string{"0"}}}, nil
	})
	adapter := NewGCPRESTAdapter(GCPRESTConfig{
		Tokens: &staticTokenProvider{token: "token"}, GRPCHTTP: doer,
		StreamPause: func(context.Context, time.Duration) error { pauses++; return nil },
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderGCP, Mode: ModeRead, AuthScheme: "grpc", Service: "test", Operation: "Chat",
		Method: http.MethodPost, URL: "https://example.googleapis.com/test.v1.TestService/Chat",
		PayloadMode: "protobuf-json", ProtobufDescriptorFile: descriptorFile,
		Body:         []any{map[string]any{"name": "one", "resourceId": "1"}, map[string]any{"name": "two", "resourceId": "2"}},
		ResponseFile: responseFile, StreamIntervalMS: 1, MaxResponseFileBytes: 4096,
		StreamMaxMessages: 2, StreamTimeoutSeconds: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(responseFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte{'\n'})
	if pauses != 1 || len(lines) != 2 || !bytes.Contains(lines[0], []byte(`"message":"one"`)) || !bytes.Contains(lines[0], []byte(`"resourceId":"1"`)) || !bytes.Contains(lines[1], []byte(`"message":"two"`)) || !bytes.Contains(lines[1], []byte(`"resourceId":"2"`)) {
		t.Fatalf("pauses=%d response=%s", pauses, data)
	}
}

func TestGCPGRPCProtoJSONDerivesAllFourRPCShapesFromMethodDescriptor(t *testing.T) {
	descriptors := gcpGRPCTestDescriptorSetBytes(t)
	validObject := map[string]any{"name": "one"}
	validArray := []any{map[string]any{"name": "one"}, map[string]any{"name": "two"}}
	for name, test := range map[string]struct {
		method          string
		clientStreaming bool
		serverStreaming bool
		validBody       any
		invalidBody     any
		wantFrames      int
	}{
		"unary":         {method: "Echo", validBody: validObject, invalidBody: validArray, wantFrames: 1},
		"client stream": {method: "Upload", clientStreaming: true, validBody: validArray, invalidBody: validObject, wantFrames: 2},
		"server stream": {method: "Watch", serverStreaming: true, validBody: validObject, invalidBody: validArray, wantFrames: 1},
		"bidirectional": {method: "Chat", clientStreaming: true, serverStreaming: true, validBody: validArray, invalidBody: validObject, wantFrames: 2},
	} {
		t.Run(name, func(t *testing.T) {
			schema, err := parseGCPGRPCProtoJSONSchema(descriptors, "https://example.googleapis.com/test.v1.TestService/"+test.method)
			if err != nil {
				t.Fatal(err)
			}
			if schema.method.IsStreamingClient() != test.clientStreaming || schema.method.IsStreamingServer() != test.serverStreaming {
				t.Fatalf("method shape client=%v server=%v", schema.method.IsStreamingClient(), schema.method.IsStreamingServer())
			}
			framed, err := encodeGCPGRPCProtoJSONRequest(test.validBody, schema)
			if err != nil {
				t.Fatal(err)
			}
			if countGCPGRPCTestFrames(t, framed) != test.wantFrames {
				t.Fatalf("encoded frame count does not match method shape: %x", framed)
			}
			if _, err := encodeGCPGRPCProtoJSONRequest(test.invalidBody, schema); err == nil {
				t.Fatal("request body with the wrong method shape was accepted")
			}
		})
	}
}

func TestGCPGRPCProtoJSONRejectsInvalidSchemaOrRequestBeforeCredentialsAndNetwork(t *testing.T) {
	for name, configure := range map[string]func(*testing.T, string, *Invocation){
		"invalid descriptor": func(t *testing.T, directory string, invocation *Invocation) {
			path := filepath.Join(directory, "invalid.protoset")
			if err := os.WriteFile(path, []byte("not-a-descriptor"), 0o600); err != nil {
				t.Fatal(err)
			}
			invocation.ProtobufDescriptorFile = path
		},
		"method missing from descriptor": func(_ *testing.T, _ string, invocation *Invocation) {
			invocation.Operation = "Missing"
			invocation.URL = "https://example.googleapis.com/test.v1.TestService/Missing"
		},
		"unknown request field": func(_ *testing.T, _ string, invocation *Invocation) {
			invocation.Body = map[string]any{"unknown": true}
		},
		"array for unary": func(_ *testing.T, _ string, invocation *Invocation) {
			invocation.Body = []any{map[string]any{"name": "one"}}
		},
		"object for client stream": func(_ *testing.T, _ string, invocation *Invocation) {
			invocation.Operation = "Chat"
			invocation.URL = "https://example.googleapis.com/test.v1.TestService/Chat"
		},
		"server stream without bounds": func(_ *testing.T, _ string, invocation *Invocation) {
			invocation.Operation = "Watch"
			invocation.URL = "https://example.googleapis.com/test.v1.TestService/Watch"
		},
		"stream bounds on unary": func(_ *testing.T, _ string, invocation *Invocation) {
			invocation.StreamMaxMessages = 1
			invocation.StreamTimeoutSeconds = 30
		},
	} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			tokens := &staticTokenProvider{token: "must-not-be-used"}
			doerCalled := false
			adapter := NewGCPRESTAdapter(GCPRESTConfig{
				Tokens: tokens,
				GRPCHTTP: doerFunc(func(*http.Request) (*http.Response, error) {
					doerCalled = true
					return nil, fmt.Errorf("must not be called")
				}),
			})
			invocation := Invocation{
				Provider: ProviderGCP, Mode: ModeRead, AuthScheme: "grpc", Service: "test", Operation: "Echo",
				Method: http.MethodPost, URL: "https://example.googleapis.com/test.v1.TestService/Echo",
				PayloadMode: "protobuf-json", ProtobufDescriptorFile: writeGCPGRPCTestDescriptorSet(t, directory),
				Body: map[string]any{"name": "Lune"}, ResponseFile: filepath.Join(directory, "response.ndjson"),
			}
			configure(t, directory, &invocation)
			_, err := adapter.Invoke(t.Context(), invocation)
			if err == nil || tokens.calls != 0 || doerCalled {
				t.Fatalf("err=%v token calls=%d doer=%v", err, tokens.calls, doerCalled)
			}
		})
	}
}

func TestGCPGRPCProtoJSONBoundsLongLivedServerStreamByMessageCount(t *testing.T) {
	directory := t.TempDir()
	responseFile := filepath.Join(directory, "watch.ndjson")
	responseData := appendGRPCFrame(nil, appendGRPCTestResponsePayload(nil, "one", 1))
	responseData = appendGRPCFrame(responseData, appendGRPCTestResponsePayload(nil, "two", 2))
	adapter := NewGCPRESTAdapter(GCPRESTConfig{
		Tokens: &staticTokenProvider{token: "token"},
		GRPCHTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200, ProtoMajor: 2,
				Header: http.Header{"Content-Type": []string{"application/grpc+proto"}},
				Body:   io.NopCloser(bytes.NewReader(responseData)),
			}, nil
		}),
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderGCP, Mode: ModeMutate, AuthScheme: "grpc", Service: "test", Operation: "Watch",
		Method: http.MethodPost, URL: "https://example.googleapis.com/test.v1.TestService/Watch",
		PayloadMode: "protobuf-json", ProtobufDescriptorFile: writeGCPGRPCTestDescriptorSet(t, directory),
		Body: map[string]any{"name": "Lune"}, ResponseFile: responseFile, MaxResponseFileBytes: 4096,
		StreamMaxMessages: 1, StreamTimeoutSeconds: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(responseFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte{'\n'})
	if len(lines) != 1 || !bytes.Contains(lines[0], []byte(`"message":"one"`)) || bytes.Contains(data, []byte(`"message":"two"`)) {
		t.Fatalf("bounded response=%s", data)
	}
}

func TestGCPGRPCProtoJSONBoundedStreamRejectsKnownProviderError(t *testing.T) {
	directory := t.TempDir()
	responseFile := filepath.Join(directory, "failed-watch.ndjson")
	responseData := appendGRPCFrame(nil, appendGRPCTestResponsePayload(nil, "partial", 1))
	adapter := NewGCPRESTAdapter(GCPRESTConfig{
		Tokens: &staticTokenProvider{token: "token"},
		GRPCHTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200, ProtoMajor: 2,
				Header:  http.Header{"Content-Type": []string{"application/grpc+proto"}},
				Body:    io.NopCloser(bytes.NewReader(responseData)),
				Trailer: http.Header{"Grpc-Status": []string{"13"}},
			}, nil
		}),
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderGCP, Mode: ModeMutate, AuthScheme: "grpc", Service: "test", Operation: "Watch",
		Method: http.MethodPost, URL: "https://example.googleapis.com/test.v1.TestService/Watch",
		PayloadMode: "protobuf-json", ProtobufDescriptorFile: writeGCPGRPCTestDescriptorSet(t, directory),
		Body: map[string]any{"name": "Lune"}, ResponseFile: responseFile, MaxResponseFileBytes: 4096,
		StreamMaxMessages: 1, StreamTimeoutSeconds: 30,
	})
	if err == nil {
		t.Fatal("bounded stream accepted a known nonzero grpc-status")
	}
	if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
		t.Fatalf("failed bounded stream published output: %v", statErr)
	}
}

func TestGCPGRPCProtoJSONBoundsIdleServerStreamByTimeout(t *testing.T) {
	directory := t.TempDir()
	responseFile := filepath.Join(directory, "idle-watch.ndjson")
	adapter := NewGCPRESTAdapter(GCPRESTConfig{
		Tokens: &staticTokenProvider{token: "token"},
		GRPCHTTP: doerFunc(func(request *http.Request) (*http.Response, error) {
			reader, writer := io.Pipe()
			go func() {
				<-request.Context().Done()
				_ = writer.CloseWithError(request.Context().Err())
			}()
			return &http.Response{
				StatusCode: 200, ProtoMajor: 2,
				Header: http.Header{"Content-Type": []string{"application/grpc+proto"}},
				Body:   reader,
			}, nil
		}),
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderGCP, Mode: ModeMutate, AuthScheme: "grpc", Service: "test", Operation: "Watch",
		Method: http.MethodPost, URL: "https://example.googleapis.com/test.v1.TestService/Watch",
		PayloadMode: "protobuf-json", ProtobufDescriptorFile: writeGCPGRPCTestDescriptorSet(t, directory),
		Body: map[string]any{"name": "Lune"}, ResponseFile: responseFile, MaxResponseFileBytes: 4096,
		StreamMaxMessages: 8, StreamTimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(responseFile)
	if err != nil || len(data) != 0 {
		t.Fatalf("idle bounded stream data=%q err=%v", data, err)
	}
}

func TestGCPGRPCProtoJSONFailuresNeverPublishResponseFile(t *testing.T) {
	validPayload := appendGRPCTestResponsePayload(nil, "hello", 1)
	unknownPayload := append(append([]byte(nil), validPayload...), protowire.AppendTag(nil, 99, protowire.VarintType)...)
	unknownPayload = protowire.AppendVarint(unknownPayload, 1)
	nestedUnknown := protowire.AppendTag(nil, 99, protowire.VarintType)
	nestedUnknown = protowire.AppendVarint(nestedUnknown, 1)
	nestedUnknownPayload := append(append([]byte(nil), validPayload...), protowire.AppendTag(nil, 3, protowire.BytesType)...)
	nestedUnknownPayload = protowire.AppendBytes(nestedUnknownPayload, nestedUnknown)
	anyPayload := protowire.AppendTag(nil, 1, protowire.BytesType)
	anyPayload = protowire.AppendString(anyPayload, "type.googleapis.com/test.v1.Child")
	anyPayload = protowire.AppendTag(anyPayload, 2, protowire.BytesType)
	anyPayload = protowire.AppendBytes(anyPayload, nestedUnknown)
	anyUnknownPayload := append(append([]byte(nil), validPayload...), protowire.AppendTag(nil, 4, protowire.BytesType)...)
	anyUnknownPayload = protowire.AppendBytes(anyUnknownPayload, anyPayload)
	for name, responseData := range map[string][]byte{
		"malformed protobuf":     appendGRPCFrame(nil, []byte{0xff}),
		"unknown wire field":     appendGRPCFrame(nil, unknownPayload),
		"nested unknown field":   appendGRPCFrame(nil, nestedUnknownPayload),
		"Any unknown field":      appendGRPCFrame(nil, anyUnknownPayload),
		"missing unary response": nil,
		"multiple unary responses": func() []byte {
			data := appendGRPCFrame(nil, validPayload)
			return appendGRPCFrame(data, validPayload)
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			responseFile := filepath.Join(directory, "failed.ndjson")
			adapter := NewGCPRESTAdapter(GCPRESTConfig{
				Tokens: &staticTokenProvider{token: "token"},
				GRPCHTTP: doerFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 200, ProtoMajor: 2, Header: http.Header{"Content-Type": []string{"application/grpc+proto"}}, Body: io.NopCloser(bytes.NewReader(responseData)), Trailer: http.Header{"Grpc-Status": []string{"0"}}}, nil
				}),
			})
			_, err := adapter.Invoke(t.Context(), Invocation{
				Provider: ProviderGCP, Mode: ModeRead, AuthScheme: "grpc", Service: "test", Operation: "Echo",
				Method: http.MethodPost, URL: "https://example.googleapis.com/test.v1.TestService/Echo",
				PayloadMode: "protobuf-json", ProtobufDescriptorFile: writeGCPGRPCTestDescriptorSet(t, directory),
				Body: map[string]any{"name": "Lune"}, ResponseFile: responseFile, MaxResponseFileBytes: 4096,
			})
			if err == nil {
				t.Fatal("invalid schema-driven response accepted")
			}
			if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
				t.Fatalf("failed response published output: %v", statErr)
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
		"grpc-web content type": {
			StatusCode: http.StatusOK, ProtoMajor: 2, Header: http.Header{"Content-Type": []string{"application/grpc-web+proto"}},
			Body: io.NopCloser(bytes.NewReader(appendGRPCFrame(nil, []byte("response")))), Trailer: http.Header{"Grpc-Status": []string{"0"}},
		},
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

func appendGRPCTestResponsePayload(target []byte, message string, resourceID uint64) []byte {
	target = protowire.AppendTag(target, 1, protowire.BytesType)
	target = protowire.AppendString(target, message)
	target = protowire.AppendTag(target, 2, protowire.VarintType)
	return protowire.AppendVarint(target, resourceID)
}

func countGCPGRPCTestFrames(t testing.TB, framed []byte) int {
	t.Helper()
	count := 0
	for len(framed) != 0 {
		if len(framed) < 5 || framed[0] != 0 {
			t.Fatalf("invalid test gRPC frames: %x", framed)
		}
		length := int(binary.BigEndian.Uint32(framed[1:5]))
		if length > len(framed)-5 {
			t.Fatalf("truncated test gRPC frame: %x", framed)
		}
		framed = framed[5+length:]
		count++
	}
	return count
}

func writeGCPGRPCTestDescriptorSet(t *testing.T, directory string) string {
	t.Helper()
	data := gcpGRPCTestDescriptorSetBytes(t)
	path := filepath.Join(directory, "service.protoset")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func gcpGRPCTestDescriptorSetBytes(t testing.TB) []byte {
	t.Helper()
	optional := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL
	stringType := descriptorpb.FieldDescriptorProto_TYPE_STRING
	int64Type := descriptorpb.FieldDescriptorProto_TYPE_INT64
	messageType := descriptorpb.FieldDescriptorProto_TYPE_MESSAGE
	request := &descriptorpb.DescriptorProto{
		Name: proto.String("Request"),
		Field: []*descriptorpb.FieldDescriptorProto{
			{Name: proto.String("name"), JsonName: proto.String("name"), Number: proto.Int32(1), Label: &optional, Type: &stringType},
			{Name: proto.String("resource_id"), JsonName: proto.String("resourceId"), Number: proto.Int32(2), Label: &optional, Type: &int64Type},
		},
	}
	response := &descriptorpb.DescriptorProto{
		Name: proto.String("Response"),
		Field: []*descriptorpb.FieldDescriptorProto{
			{Name: proto.String("message"), JsonName: proto.String("message"), Number: proto.Int32(1), Label: &optional, Type: &stringType},
			{Name: proto.String("resource_id"), JsonName: proto.String("resourceId"), Number: proto.Int32(2), Label: &optional, Type: &int64Type},
			{Name: proto.String("child"), JsonName: proto.String("child"), Number: proto.Int32(3), Label: &optional, Type: &messageType, TypeName: proto.String(".test.v1.Child")},
			{Name: proto.String("metadata"), JsonName: proto.String("metadata"), Number: proto.Int32(4), Label: &optional, Type: &messageType, TypeName: proto.String(".google.protobuf.Any")},
		},
	}
	child := &descriptorpb.DescriptorProto{
		Name: proto.String("Child"),
		Field: []*descriptorpb.FieldDescriptorProto{
			{Name: proto.String("value"), JsonName: proto.String("value"), Number: proto.Int32(1), Label: &optional, Type: &stringType},
		},
	}
	set := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{protodesc.ToFileDescriptorProto(anypb.File_google_protobuf_any_proto), {
		Name: proto.String("test/v1/service.proto"), Package: proto.String("test.v1"), Syntax: proto.String("proto3"),
		Dependency:  []string{"google/protobuf/any.proto"},
		MessageType: []*descriptorpb.DescriptorProto{request, response, child},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name: proto.String("TestService"),
			Method: []*descriptorpb.MethodDescriptorProto{
				{Name: proto.String("Echo"), InputType: proto.String(".test.v1.Request"), OutputType: proto.String(".test.v1.Response")},
				{Name: proto.String("Upload"), InputType: proto.String(".test.v1.Request"), OutputType: proto.String(".test.v1.Response"), ClientStreaming: proto.Bool(true)},
				{Name: proto.String("Watch"), InputType: proto.String(".test.v1.Request"), OutputType: proto.String(".test.v1.Response"), ServerStreaming: proto.Bool(true)},
				{Name: proto.String("Chat"), InputType: proto.String(".test.v1.Request"), OutputType: proto.String(".test.v1.Response"), ClientStreaming: proto.Bool(true), ServerStreaming: proto.Bool(true)},
			},
		}},
	}}}
	data, err := proto.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func FuzzGCPGRPCProtoJSONDescriptorSetNeverPanics(f *testing.F) {
	f.Add(gcpGRPCTestDescriptorSetBytes(f), "https://example.googleapis.com/test.v1.TestService/Echo")
	f.Add([]byte("not-a-descriptor"), "https://example.googleapis.com/test.v1.TestService/Echo")
	f.Add([]byte{}, "not-a-url")
	f.Fuzz(func(t *testing.T, data []byte, rawURL string) {
		if len(data) > maxGCPGRPCDescriptorBytes {
			return
		}
		schema, err := parseGCPGRPCProtoJSONSchema(data, rawURL)
		if err == nil && (schema.method == nil || schema.types == nil) {
			t.Fatal("successful descriptor parse returned an incomplete schema")
		}
	})
}

func FuzzGCPGRPCProtoJSONStreamBounds(f *testing.F) {
	f.Add(1, 1, true)
	f.Add(256, 300, true)
	f.Add(0, 0, false)
	f.Add(257, 301, true)
	f.Fuzz(func(t *testing.T, maxMessages, timeoutSeconds int, serverStreaming bool) {
		method := "Echo"
		if serverStreaming {
			method = "Watch"
		}
		schema, err := parseGCPGRPCProtoJSONSchema(gcpGRPCTestDescriptorSetBytes(t), "https://example.googleapis.com/test.v1.TestService/"+method)
		if err != nil {
			t.Fatal(err)
		}
		invocation := Invocation{StreamMaxMessages: maxMessages, StreamTimeoutSeconds: timeoutSeconds}
		err = validateGCPGRPCProtoJSONStreamBounds(schema, invocation)
		valid := maxMessages == 0 && timeoutSeconds == 0
		if serverStreaming {
			valid = maxMessages >= 1 && maxMessages <= maxGCPGRPCJSONMessages && timeoutSeconds >= 1 && timeoutSeconds <= maxGCPGRPCStreamTimeoutSeconds
		}
		if (err == nil) != valid {
			t.Fatalf("max=%d timeout=%d server=%v err=%v", maxMessages, timeoutSeconds, serverStreaming, err)
		}
	})
}

func FuzzGCPGRPCProtoJSONRequestNeverPanics(f *testing.F) {
	schema, err := parseGCPGRPCProtoJSONSchema(gcpGRPCTestDescriptorSetBytes(f), "https://example.googleapis.com/test.v1.TestService/Echo")
	if err != nil {
		f.Fatal(err)
	}
	f.Add([]byte(`{"name":"Lune","resourceId":"9007199254740993"}`))
	f.Add([]byte(`{"unknown":true}`))
	f.Add([]byte(`not-json`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxRequestPayloadBytes {
			return
		}
		var body any
		if json.Unmarshal(raw, &body) != nil {
			body = json.RawMessage(raw)
		}
		framed, err := encodeGCPGRPCProtoJSONRequest(body, schema)
		if err != nil {
			return
		}
		if len(framed) == 0 || validateGCPGRPCRequest(bytes.NewReader(framed)) != nil {
			t.Fatalf("successful request encoding returned invalid frames: %x", framed)
		}
	})
}

package cloud

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aws/smithy-go/eventstream"
)

func TestAWSSigV4EventStreamRunsPacedBidirectionalHTTP2AndValidatesResponse(t *testing.T) {
	root := t.TempDir()
	requestFile := filepath.Join(root, "request.events")
	responseFile := filepath.Join(root, "response.events")
	requestFrames := append(
		encodeAWSHTTP2TestEvent(t, "configurationEvent", "application/json", []byte(`{"configuration":true}`)),
		encodeAWSHTTP2TestEvent(t, "binaryAudioEvent", "application/octet-stream", []byte("audio"))...,
	)
	if err := os.WriteFile(requestFile, requestFrames, 0o600); err != nil {
		t.Fatal(err)
	}
	responseFrame := encodeAWSHTTP2TestEvent(t, "transcriptEvent", "application/json", []byte(`{"transcript":"hello"}`))

	var pauseMu sync.Mutex
	var pauses []time.Duration
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		responseReader, responseWriter := io.Pipe()
		go func() {
			defer responseWriter.Close()
			defer request.Body.Close()
			decoder := eventstream.NewDecoder()
			first, err := decoder.Decode(request.Body, nil)
			if err != nil {
				responseWriter.CloseWithError(err)
				return
			}
			if first.Headers.Get(eventstream.ChunkSignatureHeader) == nil {
				responseWriter.CloseWithError(io.ErrUnexpectedEOF)
				return
			}
			if _, err := responseWriter.Write(responseFrame); err != nil {
				return
			}
			second, err := decoder.Decode(request.Body, nil)
			if err != nil || second.Headers.Get(eventstream.ChunkSignatureHeader) == nil {
				responseWriter.CloseWithError(io.ErrUnexpectedEOF)
				return
			}
			terminal, err := decoder.Decode(request.Body, nil)
			if err != nil || len(terminal.Payload) != 0 || terminal.Headers.Get(eventstream.ChunkSignatureHeader) == nil {
				responseWriter.CloseWithError(io.ErrUnexpectedEOF)
			}
		}()
		return &http.Response{
			StatusCode: http.StatusOK, Status: "200 OK", Proto: "HTTP/2.0", ProtoMajor: 2, ProtoMinor: 0,
			Header: http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}}, Body: responseReader,
		}, nil
	})
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "private-secret"}},
		HTTP:        doer, Now: func() time.Time { return time.Date(2026, 8, 3, 3, 4, 5, 0, time.UTC) },
		StreamPause: func(_ context.Context, duration time.Duration) error {
			pauseMu.Lock()
			defer pauseMu.Unlock()
			pauses = append(pauses, duration)
			return nil
		},
	})
	result, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, Mode: ModeMutate, AuthScheme: "sigv4", PayloadMode: "aws-eventstream",
		Service: "health-agent", Operation: "StartMedicalScribeListeningSession", Region: "us-west-2", Method: http.MethodPost,
		URL: "https://streaming.health-agent.us-west-2.api.aws/medical-scribe-stream/", BodyFile: requestFile,
		ResponseFile: responseFile, StreamIntervalMS: 25, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	pauseMu.Lock()
	defer pauseMu.Unlock()
	if len(pauses) != 1 || pauses[0] != 25*time.Millisecond {
		t.Fatalf("pauses=%v, want one 25ms inter-frame pause", pauses)
	}
	data, err := os.ReadFile(responseFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, responseFrame) || !bytes.Contains(result.Output, []byte(`"response_file"`)) {
		t.Fatalf("response=%x result=%s", data, result.Output)
	}
}

func TestAWSSigV4EventStreamUsesRealHTTP2TransportBidirectionally(t *testing.T) {
	root := t.TempDir()
	requestFile := filepath.Join(root, "request.events")
	responseFile := filepath.Join(root, "response.events")
	requestFrames := append(
		encodeAWSHTTP2TestEvent(t, "configurationEvent", "application/json", []byte(`{"configuration":true}`)),
		encodeAWSHTTP2TestEvent(t, "binaryAudioEvent", "application/octet-stream", []byte("audio"))...,
	)
	if err := os.WriteFile(requestFile, requestFrames, 0o600); err != nil {
		t.Fatal(err)
	}
	responseFrame := encodeAWSHTTP2TestEvent(t, "transcriptEvent", "application/json", []byte(`{"transcript":"h2"}`))
	handlerDone := make(chan error, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		fail := func(err error) { handlerDone <- err }
		if request.ProtoMajor != 2 {
			fail(fmt.Errorf("request protocol=%s, want HTTP/2", request.Proto))
			return
		}
		decoder := eventstream.NewDecoder()
		first, err := decoder.Decode(request.Body, nil)
		if err != nil || first.Headers.Get(eventstream.ChunkSignatureHeader) == nil {
			fail(fmt.Errorf("decode first signed frame: %w", err))
			return
		}
		response.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		response.WriteHeader(http.StatusOK)
		if _, err := response.Write(responseFrame); err != nil {
			fail(fmt.Errorf("write early response frame: %w", err))
			return
		}
		response.(http.Flusher).Flush()
		second, err := decoder.Decode(request.Body, nil)
		if err != nil || second.Headers.Get(eventstream.ChunkSignatureHeader) == nil {
			fail(fmt.Errorf("decode second signed frame after response started: %w", err))
			return
		}
		terminal, err := decoder.Decode(request.Body, nil)
		if err != nil || len(terminal.Payload) != 0 || terminal.Headers.Get(eventstream.ChunkSignatureHeader) == nil {
			fail(fmt.Errorf("decode signed terminal frame: %w", err))
			return
		}
		handlerDone <- nil
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	testTransport := server.Client().Transport.(*http.Transport).Clone()
	testTransport.TLSClientConfig = testTransport.TLSClientConfig.Clone()
	testTransport.TLSClientConfig.ServerName = "example.com"
	testTransport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	testClient := &http.Client{Transport: testTransport, Timeout: 5 * time.Second}
	defer testTransport.CloseIdleConnections()

	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "private-secret"}},
		HTTP:        testClient, Now: func() time.Time { return time.Date(2026, 8, 3, 3, 4, 5, 0, time.UTC) },
		StreamPause: func(context.Context, time.Duration) error { return nil },
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, Mode: ModeMutate, AuthScheme: "sigv4", PayloadMode: "aws-eventstream",
		Service: "health-agent", Operation: "StartMedicalScribeListeningSession", Region: "us-west-2", Method: http.MethodPost,
		URL: "https://streaming.health-agent.us-west-2.api.aws/medical-scribe-stream/", BodyFile: requestFile,
		ResponseFile: responseFile, StreamIntervalMS: 25, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-handlerDone; err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(responseFile)
	if err != nil || !bytes.Equal(data, responseFrame) {
		t.Fatalf("HTTP/2 response=%x err=%v", data, err)
	}
}

func TestAWSSigV4EventStreamMalformedHTTP2ResponseLeavesNoOutput(t *testing.T) {
	root := t.TempDir()
	requestFile := filepath.Join(root, "request.events")
	responseFile := filepath.Join(root, "response.events")
	if err := os.WriteFile(requestFile, encodeAWSHTTP2TestEvent(t, "audioEvent", "application/octet-stream", []byte("audio")), 0o600); err != nil {
		t.Fatal(err)
	}
	malformed := encodeAWSHTTP2TestEvent(t, "transcriptEvent", "application/json", []byte(`{"ok":true}`))
	malformed[len(malformed)-1] ^= 0xff
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		if _, err := io.Copy(io.Discard, request.Body); err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}}, Body: io.NopCloser(bytes.NewReader(malformed))}, nil
	})
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "private-secret"}}, HTTP: doer,
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, Mode: ModeMutate, AuthScheme: "sigv4", PayloadMode: "aws-eventstream",
		Service: "health-agent", Operation: "StartMedicalScribeListeningSession", Region: "us-west-2", Method: http.MethodPost,
		URL: "https://streaming.health-agent.us-west-2.api.aws/medical-scribe-stream/", BodyFile: requestFile,
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err == nil {
		t.Fatal("malformed EventStream response succeeded")
	}
	if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
		t.Fatalf("malformed response published output: %v", statErr)
	}
}

func TestAWSSigV4EventStreamInputOnlyKeepsOrdinaryResponseFile(t *testing.T) {
	root := t.TempDir()
	requestFile := filepath.Join(root, "request.events")
	responseFile := filepath.Join(root, "response.json")
	if err := os.WriteFile(requestFile, encodeAWSHTTP2TestEvent(t, "inputEvent", "application/json", []byte(`{"input":true}`)), 0o600); err != nil {
		t.Fatal(err)
	}
	ordinaryResponse := []byte(`{"accepted":true}`)
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		if _, err := io.Copy(io.Discard, request.Body); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(ordinaryResponse)),
		}, nil
	})
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "private-secret"}}, HTTP: doer,
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, Mode: ModeMutate, AuthScheme: "sigv4", PayloadMode: "aws-eventstream",
		Service: "example", Operation: "StreamInput", Region: "us-west-2", Method: http.MethodPost,
		URL: "https://example.us-west-2.amazonaws.com/input", BodyFile: requestFile,
		ResponseFile: responseFile, MaxResponseFileBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(responseFile)
	if err != nil || !bytes.Equal(data, ordinaryResponse) {
		t.Fatalf("ordinary response=%q err=%v", data, err)
	}
}

func TestAWSSigV4EventStreamPacingFailureAbortsRequest(t *testing.T) {
	root := t.TempDir()
	requestFile := filepath.Join(root, "request.events")
	responseFile := filepath.Join(root, "response.events")
	frames := append(
		encodeAWSHTTP2TestEvent(t, "firstEvent", "application/json", []byte(`{"first":true}`)),
		encodeAWSHTTP2TestEvent(t, "secondEvent", "application/json", []byte(`{"second":true}`))...,
	)
	if err := os.WriteFile(requestFile, frames, 0o600); err != nil {
		t.Fatal(err)
	}
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		defer request.Body.Close()
		_, err := io.Copy(io.Discard, request.Body)
		return nil, err
	})
	adapter := NewAWSRESTAdapter(AWSRESTConfig{
		Credentials: staticAWSCredentialsProvider{AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "private-secret"}},
		HTTP:        doer, StreamPause: func(context.Context, time.Duration) error { return context.Canceled },
	})
	_, err := adapter.Invoke(t.Context(), Invocation{
		Provider: ProviderAWS, Mode: ModeMutate, AuthScheme: "sigv4", PayloadMode: "aws-eventstream",
		Service: "example", Operation: "StreamInput", Region: "us-west-2", Method: http.MethodPost,
		URL: "https://example.us-west-2.amazonaws.com/input", BodyFile: requestFile,
		ResponseFile: responseFile, StreamIntervalMS: 10,
	})
	if err == nil {
		t.Fatal("pacing failure did not abort the request")
	}
	if _, statErr := os.Stat(responseFile); !os.IsNotExist(statErr) {
		t.Fatalf("pacing failure published output: %v", statErr)
	}
}

func TestAWSSigV4EventStreamPolicyAllowsOnlyPacingControl(t *testing.T) {
	root := t.TempDir()
	requestFile := filepath.Join(root, "request.events")
	if err := os.WriteFile(requestFile, encodeAWSHTTP2TestEvent(t, "audioEvent", "application/octet-stream", []byte("audio")), 0o600); err != nil {
		t.Fatal(err)
	}
	valid := Invocation{
		Provider: ProviderAWS, Mode: ModeMutate, AuthScheme: "sigv4", PayloadMode: "aws-eventstream",
		Service: "health-agent", Operation: "StartMedicalScribeListeningSession", Region: "us-west-2", Method: http.MethodPost,
		URL: "https://streaming.health-agent.us-west-2.api.aws/medical-scribe-stream/", BodyFile: requestFile,
		ResponseFile: filepath.Join(root, "response.events"), StreamIntervalMS: 100,
	}
	if err := validateInvocation(valid, []string{root}); err != nil {
		t.Fatalf("paced EventStream rejected: %v", err)
	}
	invalid := valid
	invalid.StreamChunkBytes = 1024
	if err := validateInvocation(invalid, []string{root}); err == nil {
		t.Fatal("EventStream accepted stream_chunk_bytes even though frames are pre-encoded")
	}
}

func encodeAWSHTTP2TestEvent(t *testing.T, eventType, contentType string, payload []byte) []byte {
	t.Helper()
	message := eventstream.Message{Payload: payload}
	message.Headers.Set(eventstream.MessageTypeHeader, eventstream.StringValue(eventstream.EventMessageType))
	message.Headers.Set(eventstream.EventTypeHeader, eventstream.StringValue(eventType))
	message.Headers.Set(eventstream.ContentTypeHeader, eventstream.StringValue(contentType))
	var encoded bytes.Buffer
	if err := eventstream.NewEncoder().Encode(&encoded, message); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

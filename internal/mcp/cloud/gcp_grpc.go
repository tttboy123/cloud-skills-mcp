package cloud

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	authSchemeGCPGRPC           = "grpc"
	gcpGRPCPayloadModeProtoJSON = "protobuf-json"
	maxGCPGRPCMessageBytes      = 16 * 1024 * 1024
	maxGCPGRPCDescriptorBytes   = 16 * 1024 * 1024
	maxGCPGRPCJSONMessages      = 256
)

var gcpGRPCMethodPathPattern = regexp.MustCompile(`^/[A-Za-z][A-Za-z0-9_.]{0,254}/[A-Za-z][A-Za-z0-9_]{0,127}$`)

func validateGCPGRPCInvocation(invocation Invocation, allowedHosts []string) error {
	if !strings.EqualFold(invocation.Method, http.MethodPost) {
		return fmt.Errorf("Google Cloud gRPC requires method POST over HTTP/2")
	}
	if !identifierPattern.MatchString(invocation.Service) || !identifierPattern.MatchString(invocation.Operation) {
		return fmt.Errorf("Google Cloud gRPC requires valid service and operation names")
	}
	if err := validateRESTTargetWithEndpointHosts(ProviderGCP, invocation.Method, invocation.URL, allowedHosts); err != nil {
		return err
	}
	target, err := url.Parse(invocation.URL)
	if err != nil || target.RawQuery != "" || target.Fragment != "" || !gcpGRPCMethodPathPattern.MatchString(target.EscapedPath()) {
		return fmt.Errorf("Google Cloud gRPC requires https://<googleapis-host>/<fully-qualified-service>/<method> without query parameters")
	}
	parts := strings.Split(strings.TrimPrefix(target.EscapedPath(), "/"), "/")
	if len(parts) != 2 || parts[1] != invocation.Operation {
		return fmt.Errorf("Google Cloud gRPC operation must match the case-sensitive method path")
	}
	if len(invocation.Parameters) != 0 {
		return fmt.Errorf("Google Cloud gRPC system and routing parameters must use non-credential HTTP metadata headers")
	}
	payloadMode := strings.ToLower(strings.TrimSpace(invocation.PayloadMode))
	switch payloadMode {
	case "":
		if invocation.Body != nil || invocation.BodyFile == "" {
			return fmt.Errorf("Google Cloud gRPC raw mode requires a framed protobuf body_file and does not accept inline body")
		}
		if invocation.ProtobufDescriptorFile != "" {
			return fmt.Errorf("Google Cloud gRPC raw mode does not accept protobuf_descriptor_file")
		}
	case gcpGRPCPayloadModeProtoJSON:
		if invocation.Body == nil || invocation.BodyFile != "" || invocation.ProtobufDescriptorFile == "" {
			return fmt.Errorf("Google Cloud gRPC protobuf-json requires inline body and protobuf_descriptor_file; body_file is forbidden")
		}
		if azureRealtimeContainsCredentialField(invocation.Body) {
			return fmt.Errorf("Google Cloud gRPC protobuf-json requires a credential-free request body")
		}
	default:
		return fmt.Errorf("Google Cloud gRPC payload_mode must be protobuf-json or omitted for raw framed protobuf")
	}
	if invocation.ResponseFile == "" {
		return fmt.Errorf("Google Cloud gRPC requires response_file for framed protobuf responses")
	}
	if invocation.ChecksumAlgorithm != "" || invocation.StreamChunkBytes != 0 || invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return fmt.Errorf("Google Cloud gRPC does not accept checksum, chunk-size, or provider-specific stream controls")
	}
	for name := range invocation.Headers {
		lower := strings.ToLower(strings.TrimSpace(name))
		if lower == "content-type" || lower == "te" || strings.HasPrefix(lower, "grpc-") {
			return fmt.Errorf("caller-supplied Google Cloud gRPC protocol header %q is forbidden", name)
		}
	}
	return nil
}

type gcpGRPCFrameReader struct {
	ctx      context.Context
	source   *bufio.Reader
	closer   io.Closer
	pause    func(context.Context, time.Duration) error
	interval time.Duration
	pending  []byte
	started  bool
	done     bool
}

func newGCPGRPCFrameReader(source io.ReadCloser, pause func(context.Context, time.Duration) error, interval time.Duration) *gcpGRPCFrameReader {
	if pause == nil {
		pause = pauseTencentStream
	}
	return &gcpGRPCFrameReader{
		ctx: context.Background(), source: bufio.NewReader(source), closer: source,
		pause: pause, interval: interval,
	}
}

func (reader *gcpGRPCFrameReader) Read(target []byte) (int, error) {
	if len(target) == 0 {
		return 0, nil
	}
	if len(reader.pending) == 0 {
		if reader.done {
			return 0, io.EOF
		}
		if err := reader.loadNextFrame(); err != nil {
			return 0, err
		}
	}
	written := copy(target, reader.pending)
	reader.pending = reader.pending[written:]
	return written, nil
}

func (reader *gcpGRPCFrameReader) Close() error {
	reader.done = true
	reader.pending = nil
	return reader.closer.Close()
}

func (reader *gcpGRPCFrameReader) loadNextFrame() error {
	header := make([]byte, 5)
	count, err := io.ReadFull(reader.source, header)
	if err != nil {
		if err == io.EOF && count == 0 {
			reader.done = true
			return io.EOF
		}
		return fmt.Errorf("read Google Cloud gRPC frame header: %w", err)
	}
	if header[0] != 0 {
		return fmt.Errorf("Google Cloud gRPC request frames must be uncompressed")
	}
	messageBytes := binary.BigEndian.Uint32(header[1:])
	if messageBytes > maxGCPGRPCMessageBytes {
		return fmt.Errorf("Google Cloud gRPC frame exceeds %d bytes", maxGCPGRPCMessageBytes)
	}
	if reader.started && reader.interval > 0 {
		if err := reader.pause(reader.ctx, reader.interval); err != nil {
			return fmt.Errorf("pace Google Cloud gRPC message stream: %w", err)
		}
	}
	payload := make([]byte, int(messageBytes))
	if _, err := io.ReadFull(reader.source, payload); err != nil {
		return fmt.Errorf("read Google Cloud gRPC message: %w", err)
	}
	reader.pending = append(header, payload...)
	reader.started = true
	return nil
}

func validateGCPGRPCRequest(source io.Reader) error {
	reader := newGCPGRPCFrameReader(io.NopCloser(source), func(context.Context, time.Duration) error { return nil }, 0)
	defer reader.Close()
	if _, err := io.Copy(io.Discard, reader); err != nil {
		return err
	}
	if !reader.started {
		return fmt.Errorf("Google Cloud gRPC body_file must contain at least one framed protobuf message")
	}
	return nil
}

func invokeGCPGRPC(ctx context.Context, adapter *GCPRESTAdapter, invocation Invocation) (InvocationResult, error) {
	if err := validateGCPGRPCInvocation(invocation, adapter.config.AllowedHosts); err != nil {
		return InvocationResult{}, err
	}
	if strings.EqualFold(strings.TrimSpace(invocation.PayloadMode), gcpGRPCPayloadModeProtoJSON) {
		return invokeGCPGRPCProtoJSON(ctx, adapter, invocation)
	}
	file, err := os.Open(invocation.BodyFile)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("open Google Cloud gRPC body_file: %w", err)
	}
	defer file.Close()
	if err := validateGCPGRPCRequest(file); err != nil {
		return InvocationResult{}, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return InvocationResult{}, fmt.Errorf("rewind Google Cloud gRPC body_file: %w", err)
	}
	token, err := adapter.config.Tokens.Token(ctx)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("load Google Cloud application credential: %w", err)
	}
	if strings.TrimSpace(token) == "" {
		return InvocationResult{}, fmt.Errorf("Google Cloud credential returned an empty access token")
	}
	interval := time.Duration(invocation.StreamIntervalMS) * time.Millisecond
	body := newGCPGRPCFrameReader(file, adapter.config.StreamPause, interval)
	body.ctx = ctx
	defer body.Close()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, invocation.URL, body)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("build Google Cloud gRPC request: %w", err)
	}
	request.ContentLength = -1
	request.Header.Set("Content-Type", "application/grpc+proto")
	request.Header.Set("TE", "trailers")
	request.Header.Set("Authorization", "Bearer "+token)
	if invocation.Project != "" {
		request.Header.Set("X-Goog-User-Project", invocation.Project)
	}
	for name, value := range invocation.Headers {
		request.Header.Set(name, value)
	}
	response, err := adapter.config.GRPCHTTP.Do(request)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("Google Cloud gRPC request: %w", err)
	}
	output, requestID, err := readGCPGRPCResponse(response, invocation, adapter.config.MaxBodyBytes)
	if err != nil {
		return InvocationResult{}, err
	}
	return InvocationResult{Output: output, RequestID: requestID}, nil
}

func readGCPGRPCResponse(response *http.Response, invocation Invocation, maxBodyBytes int64) ([]byte, string, error) {
	if response == nil || response.Body == nil {
		return nil, "", fmt.Errorf("Google Cloud gRPC returned no response")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("Google Cloud gRPC returned HTTP %d", response.StatusCode)
	}
	if response.ProtoMajor != 2 {
		return nil, "", fmt.Errorf("Google Cloud gRPC requires an HTTP/2 response")
	}
	if !validGCPGRPCContentType(response.Header.Get("Content-Type")) {
		return nil, "", fmt.Errorf("Google Cloud gRPC returned an invalid content type")
	}
	sink, err := newWebSocketOutputSink(invocation, maxBodyBytes, "Google Cloud gRPC")
	if err != nil {
		return nil, "", err
	}
	defer sink.abort()
	reader := newGCPGRPCFrameReader(response.Body, func(context.Context, time.Duration) error { return nil }, 0)
	reader.ctx = context.Background()
	buffer := make([]byte, 32*1024)
	for {
		count, readErr := reader.Read(buffer)
		if count > 0 {
			if err := sink.writeBinary(buffer[:count]); err != nil {
				return nil, "", err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, "", fmt.Errorf("decode Google Cloud gRPC response: %w", readErr)
		}
	}
	status := response.Trailer.Get("Grpc-Status")
	if status == "" {
		status = response.Header.Get("Grpc-Status")
	}
	if status == "" {
		return nil, "", fmt.Errorf("Google Cloud gRPC response omitted grpc-status")
	}
	if status != "0" {
		if _, err := strconv.Atoi(status); err != nil {
			return nil, "", fmt.Errorf("Google Cloud gRPC returned an invalid grpc-status")
		}
		return nil, "", fmt.Errorf("Google Cloud gRPC returned grpc-status %s", status)
	}
	requestID := responseRequestID(response.Header)
	sink.finished = true
	metadata, err := sink.finishFile(map[string]any{
		"response_file": invocation.ResponseFile,
		"bytes":         sink.written,
		"content_type":  "application/grpc+proto",
		"request_id":    requestID,
	})
	if err != nil {
		return nil, "", err
	}
	return metadata, requestID, nil
}

func validGCPGRPCContentType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return false
	}
	mediaType = strings.ToLower(mediaType)
	return mediaType == "application/grpc" || strings.HasPrefix(mediaType, "application/grpc+")
}

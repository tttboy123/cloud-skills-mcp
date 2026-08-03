package cloud

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

type gcpGRPCProtoJSONSchema struct {
	method protoreflect.MethodDescriptor
	types  *dynamicpb.Types
}

func loadGCPGRPCProtoJSONSchema(path, rawURL string) (gcpGRPCProtoJSONSchema, error) {
	file, err := os.Open(path)
	if err != nil {
		return gcpGRPCProtoJSONSchema{}, fmt.Errorf("open Google Cloud gRPC protobuf_descriptor_file: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxGCPGRPCDescriptorBytes+1))
	if err != nil {
		return gcpGRPCProtoJSONSchema{}, fmt.Errorf("read Google Cloud gRPC protobuf_descriptor_file: %w", err)
	}
	if len(data) == 0 || len(data) > maxGCPGRPCDescriptorBytes {
		return gcpGRPCProtoJSONSchema{}, fmt.Errorf("Google Cloud gRPC protobuf_descriptor_file must be between 1 and %d bytes", maxGCPGRPCDescriptorBytes)
	}
	return parseGCPGRPCProtoJSONSchema(data, rawURL)
}

func parseGCPGRPCProtoJSONSchema(data []byte, rawURL string) (gcpGRPCProtoJSONSchema, error) {
	var descriptorSet descriptorpb.FileDescriptorSet
	if proto.Unmarshal(data, &descriptorSet) != nil || len(descriptorSet.File) == 0 {
		return gcpGRPCProtoJSONSchema{}, fmt.Errorf("Google Cloud gRPC protobuf_descriptor_file is not a valid FileDescriptorSet")
	}
	files, err := protodesc.NewFiles(&descriptorSet)
	if err != nil {
		return gcpGRPCProtoJSONSchema{}, fmt.Errorf("resolve Google Cloud gRPC FileDescriptorSet: %w", err)
	}
	target, err := url.Parse(rawURL)
	if err != nil {
		return gcpGRPCProtoJSONSchema{}, fmt.Errorf("parse Google Cloud gRPC method URL")
	}
	parts := strings.Split(strings.TrimPrefix(target.EscapedPath(), "/"), "/")
	if len(parts) != 2 {
		return gcpGRPCProtoJSONSchema{}, fmt.Errorf("Google Cloud gRPC URL does not identify a service method")
	}
	descriptor, err := files.FindDescriptorByName(protoreflect.FullName(parts[0]))
	if err != nil {
		return gcpGRPCProtoJSONSchema{}, fmt.Errorf("Google Cloud gRPC descriptor set does not contain service %q", parts[0])
	}
	service, ok := descriptor.(protoreflect.ServiceDescriptor)
	if !ok {
		return gcpGRPCProtoJSONSchema{}, fmt.Errorf("Google Cloud gRPC URL service name resolves to a non-service descriptor")
	}
	method := service.Methods().ByName(protoreflect.Name(parts[1]))
	if method == nil {
		return gcpGRPCProtoJSONSchema{}, fmt.Errorf("Google Cloud gRPC descriptor set does not contain method %q", parts[1])
	}
	return gcpGRPCProtoJSONSchema{method: method, types: dynamicpb.NewTypes(files)}, nil
}

func encodeGCPGRPCProtoJSONRequest(body any, schema gcpGRPCProtoJSONSchema) ([]byte, error) {
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) == 0 || len(encoded) > maxRequestPayloadBytes {
		return nil, fmt.Errorf("Google Cloud gRPC protobuf-json body must be bounded JSON")
	}
	trimmed := bytes.TrimSpace(encoded)
	var messages []json.RawMessage
	if schema.method.IsStreamingClient() {
		if len(trimmed) == 0 || trimmed[0] != '[' || json.Unmarshal(trimmed, &messages) != nil || len(messages) == 0 || len(messages) > maxGCPGRPCJSONMessages {
			return nil, fmt.Errorf("Google Cloud client-streaming gRPC protobuf-json body must contain 1 to %d message objects", maxGCPGRPCJSONMessages)
		}
	} else {
		if len(trimmed) == 0 || trimmed[0] != '{' {
			return nil, fmt.Errorf("Google Cloud unary or server-streaming gRPC protobuf-json body must be one message object")
		}
		messages = []json.RawMessage{append([]byte(nil), trimmed...)}
	}
	framed := make([]byte, 0, len(encoded))
	unmarshal := protojson.UnmarshalOptions{DiscardUnknown: false, Resolver: schema.types}
	for index, raw := range messages {
		if len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' {
			return nil, fmt.Errorf("Google Cloud gRPC protobuf-json request message %d must be an object", index)
		}
		message := dynamicpb.NewMessage(schema.method.Input())
		if err := unmarshal.Unmarshal(raw, message); err != nil {
			return nil, fmt.Errorf("decode Google Cloud gRPC protobuf-json request message %d: %w", index, err)
		}
		payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
		if err != nil || len(payload) > maxGCPGRPCMessageBytes {
			return nil, fmt.Errorf("encode Google Cloud gRPC protobuf request message %d", index)
		}
		framed = appendGCPGRPCFrame(framed, payload)
	}
	return framed, nil
}

func appendGCPGRPCFrame(target, payload []byte) []byte {
	header := make([]byte, 5)
	binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
	target = append(target, header...)
	return append(target, payload...)
}

func invokeGCPGRPCProtoJSON(ctx context.Context, adapter *GCPRESTAdapter, invocation Invocation) (InvocationResult, error) {
	schema, err := loadGCPGRPCProtoJSONSchema(invocation.ProtobufDescriptorFile, invocation.URL)
	if err != nil {
		return InvocationResult{}, err
	}
	if err := validateGCPGRPCProtoJSONStreamBounds(schema, invocation); err != nil {
		return InvocationResult{}, err
	}
	framed, err := encodeGCPGRPCProtoJSONRequest(invocation.Body, schema)
	if err != nil {
		return InvocationResult{}, err
	}
	token, err := adapter.config.Tokens.Token(ctx)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("load Google Cloud application credential: %w", err)
	}
	if strings.TrimSpace(token) == "" {
		return InvocationResult{}, fmt.Errorf("Google Cloud credential returned an empty access token")
	}
	interval := time.Duration(invocation.StreamIntervalMS) * time.Millisecond
	requestContext := ctx
	cancel := func() {}
	if schema.method.IsStreamingServer() {
		requestContext, cancel = context.WithTimeout(ctx, time.Duration(invocation.StreamTimeoutSeconds)*time.Second)
	}
	defer cancel()
	body := newGCPGRPCFrameReader(io.NopCloser(bytes.NewReader(framed)), adapter.config.StreamPause, interval)
	body.ctx = requestContext
	defer body.Close()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, invocation.URL, body)
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
	output, requestID, err := readGCPGRPCProtoJSONResponse(requestContext, response, invocation, adapter.config.MaxBodyBytes, schema)
	if err != nil {
		return InvocationResult{}, err
	}
	return InvocationResult{Output: output, RequestID: requestID}, nil
}

func validateGCPGRPCProtoJSONStreamBounds(schema gcpGRPCProtoJSONSchema, invocation Invocation) error {
	if schema.method.IsStreamingServer() {
		if invocation.StreamMaxMessages < 1 || invocation.StreamMaxMessages > maxGCPGRPCJSONMessages || invocation.StreamTimeoutSeconds < 1 || invocation.StreamTimeoutSeconds > maxGCPGRPCStreamTimeoutSeconds {
			return fmt.Errorf("Google Cloud server-streaming gRPC protobuf-json requires stream_max_messages and stream_timeout_seconds")
		}
		return nil
	}
	if invocation.StreamMaxMessages != 0 || invocation.StreamTimeoutSeconds != 0 {
		return fmt.Errorf("Google Cloud unary or client-streaming gRPC does not accept server-stream bounds")
	}
	return nil
}

func readGCPGRPCProtoJSONResponse(ctx context.Context, response *http.Response, invocation Invocation, maxBodyBytes int64, schema gcpGRPCProtoJSONSchema) ([]byte, string, error) {
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
	sink, err := newWebSocketOutputSink(invocation, maxBodyBytes, "Google Cloud gRPC protobuf-json")
	if err != nil {
		return nil, "", err
	}
	defer sink.abort()
	reader := bufio.NewReader(response.Body)
	marshal := protojson.MarshalOptions{Resolver: schema.types}
	messageCount := 0
	boundedTermination := false
	for {
		header := make([]byte, 5)
		count, readErr := io.ReadFull(reader, header)
		if readErr == io.EOF && count == 0 {
			break
		}
		if readErr != nil {
			if schema.method.IsStreamingServer() && errors.Is(ctx.Err(), context.DeadlineExceeded) {
				boundedTermination = true
				break
			}
			return nil, "", fmt.Errorf("decode Google Cloud gRPC response frame header: %w", readErr)
		}
		if header[0] != 0 {
			return nil, "", fmt.Errorf("Google Cloud gRPC protobuf-json responses must be uncompressed")
		}
		messageBytes := binary.BigEndian.Uint32(header[1:])
		if messageBytes > maxGCPGRPCMessageBytes {
			return nil, "", fmt.Errorf("Google Cloud gRPC response frame exceeds %d bytes", maxGCPGRPCMessageBytes)
		}
		payload := make([]byte, int(messageBytes))
		if _, err := io.ReadFull(reader, payload); err != nil {
			return nil, "", fmt.Errorf("decode Google Cloud gRPC response message: %w", err)
		}
		messageCount++
		if messageCount > maxGCPGRPCJSONMessages || (!schema.method.IsStreamingServer() && messageCount > 1) {
			return nil, "", fmt.Errorf("Google Cloud gRPC response message count exceeds the method bound")
		}
		message := dynamicpb.NewMessage(schema.method.Output())
		if err := (proto.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(payload, message); err != nil {
			return nil, "", fmt.Errorf("decode Google Cloud gRPC protobuf response message %d: %w", messageCount-1, err)
		}
		if gcpGRPCMessageContainsUnknown(message.ProtoReflect(), schema.types) {
			return nil, "", fmt.Errorf("Google Cloud gRPC descriptor set does not describe every response field")
		}
		encoded, err := marshal.Marshal(message)
		if err != nil {
			return nil, "", fmt.Errorf("encode Google Cloud gRPC ProtoJSON response message %d: %w", messageCount-1, err)
		}
		if err := sink.writeMessage(encoded); err != nil {
			return nil, "", err
		}
		if schema.method.IsStreamingServer() && messageCount == invocation.StreamMaxMessages {
			boundedTermination = true
			break
		}
	}
	if !schema.method.IsStreamingServer() && messageCount != 1 {
		return nil, "", fmt.Errorf("Google Cloud unary gRPC response must contain exactly one message")
	}
	status := response.Trailer.Get("Grpc-Status")
	if status == "" {
		status = response.Header.Get("Grpc-Status")
	}
	if status == "" && !boundedTermination {
		return nil, "", fmt.Errorf("Google Cloud gRPC response omitted grpc-status")
	}
	if status != "" && status != "0" {
		if _, err := strconv.Atoi(status); err != nil {
			return nil, "", fmt.Errorf("Google Cloud gRPC returned an invalid grpc-status")
		}
		return nil, "", fmt.Errorf("Google Cloud gRPC returned grpc-status %s", status)
	}
	requestID := responseRequestID(response.Header)
	metadata, err := sink.finish(requestID)
	if err != nil {
		return nil, "", err
	}
	return metadata, requestID, nil
}

func gcpGRPCMessageContainsUnknown(message protoreflect.Message, types *dynamicpb.Types) bool {
	if len(message.GetUnknown()) != 0 {
		return true
	}
	if message.Descriptor().FullName() == "google.protobuf.Any" {
		fields := message.Descriptor().Fields()
		typeURLField := fields.ByNumber(1)
		valueField := fields.ByNumber(2)
		if typeURLField == nil || valueField == nil {
			return true
		}
		typeURL := message.Get(typeURLField).String()
		payload := message.Get(valueField).Bytes()
		if typeURL == "" {
			return len(payload) != 0
		}
		if types == nil {
			return true
		}
		messageType, err := types.FindMessageByURL(typeURL)
		if err != nil {
			return true
		}
		embedded := messageType.New()
		if err := (proto.UnmarshalOptions{DiscardUnknown: false, Resolver: types}).Unmarshal(payload, embedded.Interface()); err != nil {
			return true
		}
		return gcpGRPCMessageContainsUnknown(embedded, types)
	}
	found := false
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if field.IsMap() {
			if field.MapValue().Message() == nil {
				return true
			}
			value.Map().Range(func(_ protoreflect.MapKey, item protoreflect.Value) bool {
				found = gcpGRPCMessageContainsUnknown(item.Message(), types)
				return !found
			})
			return !found
		}
		if field.IsList() {
			if field.Message() == nil {
				return true
			}
			list := value.List()
			for index := 0; index < list.Len() && !found; index++ {
				found = gcpGRPCMessageContainsUnknown(list.Get(index).Message(), types)
			}
			return !found
		}
		if field.Message() != nil {
			found = gcpGRPCMessageContainsUnknown(value.Message(), types)
		}
		return !found
	})
	return found
}

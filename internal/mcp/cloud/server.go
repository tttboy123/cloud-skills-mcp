package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/tttboy123/cloud-skills-mcp/internal/mcp/sdk"
)

const (
	serverName    = "cloud-skills-mcp"
	serverVersion = "0.4.0-dev"
)

func NewServer(runtime Runtime) *server.MCPServer {
	runtime = runtime.normalized()
	srv := server.NewMCPServer(serverName, serverVersion, server.WithToolCapabilities(true))
	registerTools(srv, runtime)
	return srv
}

func registerTools(srv *server.MCPServer, runtime Runtime) {
	providers := make([]string, 0, len(AllProviders()))
	for _, provider := range AllProviders() {
		providers = append(providers, string(provider))
	}
	srv.AddTool(mcp.NewTool("cloud_provider_status",
		mcp.WithDescription("Report one provider HTTP adapter and non-secret credential-source readiness."),
		mcp.WithString("provider", mcp.Description("Cloud provider."), mcp.Enum(providers...), mcp.Required()),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(false),
	), makeStatusHandler(runtime))

	for _, provider := range AllProviders() {
		prefix := string(provider)
		srv.AddTool(mcp.NewTool(prefix+"_api_discover",
			mcp.WithDescription("Discover official HTTP API and authentication documentation without invoking a cloud resource operation."),
			mcp.WithString("service", mcp.Description("Optional provider service or product code.")),
			mcp.WithString("operation", mcp.Description("Optional operation/action name.")),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(false),
		), makeDiscoverHandler(runtime, provider))
		srv.AddTool(newInvokeTool(prefix+"_api_read", false), makeInvokeHandler(runtime, provider, ModeRead))
		srv.AddTool(newInvokeTool(prefix+"_api_mutate", true), makeInvokeHandler(runtime, provider, ModeMutate))
	}
}

func newInvokeTool(name string, mutating bool) mcp.Tool {
	description := "Invoke any provider API operation through the official universal adapter."
	options := []mcp.ToolOption{
		mcp.WithDescription(description),
		mcp.WithString("service", mcp.Description("Provider service/product code used by request signing.")),
		mcp.WithString("operation", mcp.Description("Provider API operation/action used for signing and safety classification.")),
		mcp.WithString("region", mcp.Description("Optional provider region/location.")),
		mcp.WithString("region_set", mcp.Description("AWS SigV4a comma-separated signing region set, such as us-east-1,us-west-*; not a credential.")),
		mcp.WithString("project", mcp.Description("Optional Google Cloud project.")),
		mcp.WithString("subscription", mcp.Description("Optional Azure subscription.")),
		mcp.WithString("audience", mcp.Description("Optional Azure Entra resource audience for an uncommon official data-plane endpoint. This is an application/resource identifier, never a token.")),
		mcp.WithString("registry_instance_id", mcp.Description("Alibaba ACR, Tencent TCR, or Baidu CCR Enterprise Edition instance ID used only for an internal official temporary-credential request; never a Registry credential.")),
		mcp.WithString("registry_user_id", mcp.Description("Baidu CCR Enterprise IAM user ID used only for the internal signed username lookup; it is a resource identifier, never a Registry credential.")),
		mcp.WithString("acr_scope", mcp.Description("Azure Container Registry data-plane authorization metadata, never a token. It must exactly match the request path and method: registry catalog/deleted_catalog, or repository pull, push, delete, metadata_read, metadata_write, deleted_read, and deleted_restore permission families.")),
		mcp.WithString("acr_source_scope", mcp.Description("Azure Container Registry cross-repository blob mount only: repository:<exact-from-repository>:pull. It is sent as a second internal OAuth scope and must match the from query parameter.")),
		mcp.WithString("auth_scheme", mcp.Description("Optional provider HTTP authentication scheme: sigv4, sigv4a, ecr (private or public Docker/OCI Registry with internal IAM token retrieval), sigv4-ws, connect-health-ws, transcribe-ws, iot-mqtt-ws, kinesisvideo-signaling-ws, appsync-event-ws, appsync-graphql-ws, acr, realtime-ws, voice-live-ws, webpubsub-ws, webpubsub-mqtt-ws, eventgrid-mqtt-ws, servicebus-amqp-ws, eventhubs-amqp-ws, artifact-registry (Google OCI Registry with internal ADC Bearer), firebase-sse, grpc, vertex-live-ws, acs3, rpc, roa, datahub, opensearch, odps, odps4, fc, fc3, fc-custom, oss, oss4, sls, sls4, mns, mq, acr-registry (Alibaba Enterprise Registry with internal RAM token exchange), ots, ots4, nls-rest, nls-ws, tc3, tc1, tc1-sha256, qcloud, qcloud-sha256, asr-ws, virtual-number-ws, soe-ws, speech-translate-ws, voice-convert-ws, mps-ws, mps-tts-ws, tts-ws, tts-stream-ws, podcast-ws, cos, cls, tcr-registry (Tencent Enterprise Registry with internal CAM temporary credential), ccr-registry (Baidu Enterprise or Personal Registry with internal BCE temporary credential), rtc-aiagent-ws, or the provider default.")),
		mcp.WithString("auth_version", mcp.Description("Optional Baidu BCE signing version: v1 (default) or v2. BCE v2 also requires service and region.")),
		mcp.WithString("api_version", mcp.Description("Provider API version used by Alibaba ACS3/RPC/ROA/DataHub/SLS/MNS/MQ/OTS and Tencent TC3 common parameters or headers.")),
		mcp.WithString("payload_mode", mcp.Description("Optional payload protocol: AWS aws-chunked, aws-chunked-trailer, or aws-eventstream; Google Cloud grpc also accepts protobuf-json for schema-driven JSON/protobuf conversion.")),
		mcp.WithString("checksum_algorithm", mcp.Description("Required for payload_mode=aws-chunked-trailer: crc32, crc32c, crc64nvme, sha1, or sha256. The server computes the checksum; callers never provide its value.")),
		mcp.WithString("method", mcp.Description("HTTP method for the official provider API request or GET for a WebSocket upgrade.")),
		mcp.WithString("url", mcp.Description("Exact official HTTPS or supported WSS provider API URL.")),
		mcp.WithObject("parameters", mcp.Description("Optional scalar HTTP query parameters."), mcp.AdditionalProperties(true)),
		mcp.WithObject("headers", mcp.Description("Non-credential HTTP headers."), mcp.AdditionalProperties(map[string]any{"type": "string"})),
		mcp.WithAny("body", mcp.Description("Optional JSON-compatible REST request body. Google Cloud firebase-sse uses a finite max_events/timeout_seconds observation plan; grpc protobuf-json accepts one ProtoJSON object for unary methods or a finite object array for client streams; AWS sigv4-ws accepts a finite text/JSON/binary message plan; Google Cloud vertex-live-ws accepts a bounded setup-first Live API message plan; connect-health-ws requires the Medical Scribe configuration event before body_file audio; Azure realtime-ws and voice-live-ws accept one event object or an event array, webpubsub-ws accepts a bounded client-token/message plan, webpubsub-mqtt-ws or eventgrid-mqtt-ws accepts a finite MQTT read or bidirectional client plan, and servicebus-amqp-ws/eventhubs-amqp-ws accepts bounded broker operations over AMQP 1.0 WSS with internal Entra/CBS; AWS transcribe-ws accepts an optional ConfigurationEvent before body_file audio; AWS iot-mqtt-ws accepts finite MQTT 3.1.1/5.0 subscription plans, kinesisvideo-signaling-ws accepts a bounded WebRTC signaling plan, and appsync-event-ws/appsync-graphql-ws accept finite subscription plans; Alibaba mq accepts a bounded RocketMQ 4.x publish or consume-and-internal-settlement plan whose receipt handles never leave the server; Alibaba nls-ws accepts guarded start payloads or text segments; Alibaba nls-rest TTS POST accepts documented JSON fields; Baidu rtc-aiagent-ws accepts a credential-free instance/config/termination/audio-codec plan, validated pre/post-audio commands, optional image mode, and bounded Function Call result templates whose session IDs are correlated internally, while all tokens and license material remain internal.")),
		mcp.WithString("body_file", mcp.Description("Optional local HTTP body or WebSocket stream file. AWS connect-health-ws and transcribe-ws stream finite audio; Azure realtime-ws and voice-live-ws accept one JSON event per line; Google Cloud grpc requires standard 5-byte-framed, uncompressed protobuf messages; Alibaba NLS recognition reads finite audio; Alibaba mq deliberately forbids body_file and builds bounded XML internally; Baidu RTC accepts raw, raw16k, PCMA, PCMU, G.722, or Opus packet streams, with variable Opus packet boundaries declared in the body plan. The resolved regular file must be under CLOUD_SKILLS_ALLOWED_FILE_ROOTS. Only explicit AWS Connect Health, AWS Transcribe, Alibaba nls-ws recognition, and Baidu rtc-aiagent-ws can combine it with body; Alibaba nls-rest ASR uses the file alone.")),
		mcp.WithString("image_file", mcp.Description("Baidu RTC AI Agent only: one non-empty local image uploaded exactly once after the provider emits [E]:[UPLOAD_IMAGE]. The regular file must be below 64 MiB and under CLOUD_SKILLS_ALLOWED_FILE_ROOTS; callers cannot inject raw image protocol frames.")),
		mcp.WithString("protobuf_descriptor_file", mcp.Description("Google Cloud grpc protobuf-json only: a binary google.protobuf.FileDescriptorSet under CLOUD_SKILLS_ALLOWED_FILE_ROOTS. It must include the exact service/method from the URL and all imports; the file stays local and is never sent to the provider.")),
		mcp.WithString("response_file", mcp.Description("Optional new local file for a successful HTTP body, validated AWS EventStream response, Firebase SSE or gRPC NDJSON, WebSocket/AMQP NDJSON events, or synthesized audio; required by paced AWS aws-eventstream and by AWS sigv4-ws, connect-health-ws, iot-mqtt-ws, kinesisvideo-signaling-ws, appsync-event-ws, and appsync-graphql-ws, Azure realtime-ws, voice-live-ws, webpubsub-ws, webpubsub-mqtt-ws, eventgrid-mqtt-ws, servicebus-amqp-ws, and eventhubs-amqp-ws, Google Cloud firebase-sse, grpc, and vertex-live-ws, Alibaba mq, Alibaba nls-ws synthesis, Alibaba nls-rest TTS, and Baidu rtc-aiagent-ws. The target must be under CLOUD_SKILLS_ALLOWED_FILE_ROOTS and is never overwritten.")),
		mcp.WithNumber("stream_chunk_bytes", mcp.Description("Optional WebSocket binary message size. AWS Connect Health, AWS Transcribe, Alibaba NLS recognition, and Tencent audio streams derive protocol-specific defaults when omitted. Baidu RTC fixed-rate codecs require the exact byte count implied by codec and stream_interval_ms; Opus uses body.opus_packet_lengths instead."), mcp.Min(1), mcp.Max(maxRequestFileBytes)),
		mcp.WithNumber("stream_interval_ms", mcp.Description("Optional streaming message pacing interval in milliseconds; AWS aws-eventstream paces consecutive pre-encoded frames and requires response_file, Azure realtime-ws/voice-live-ws/webpubsub-ws and Google Cloud vertex-live-ws pace JSON events, Google Cloud grpc paces framed protobuf messages, AWS Connect Health, AWS Transcribe, and Alibaba NLS recognition default to 100 ms, Tencent ASR to 200 ms, Tencent MPS PCM to 40 ms, and Baidu RTC accepts official 20-200 ms fixed-rate packets or Opus 20/40/60 ms packets."), mcp.Min(1), mcp.Max(5000)),
		mcp.WithNumber("stream_max_messages", mcp.Description("Google Cloud grpc protobuf-json server-streaming methods only: required finite response-message bound from 1 to 256."), mcp.Min(1), mcp.Max(maxGCPGRPCJSONMessages)),
		mcp.WithNumber("stream_timeout_seconds", mcp.Description("Google Cloud grpc protobuf-json server-streaming methods only: required finite observation timeout from 1 to 300 seconds."), mcp.Min(1), mcp.Max(maxGCPGRPCStreamTimeoutSeconds)),
		mcp.WithString("stream_user_id", mcp.Description("Required MPS WebSocket audio-source ID. It is placed only in the internal binary frame, not the signed URL.")),
		mcp.WithNumber("stream_format", mcp.Description("Required MPS WebSocket PCM format: 1 for 16 kHz s16 mono or 2 for 8 kHz s16 mono."), mcp.Min(1), mcp.Max(2)),
		mcp.WithReadOnlyHintAnnotation(!mutating),
		mcp.WithDestructiveHintAnnotation(mutating),
		mcp.WithIdempotentHintAnnotation(!mutating),
		mcp.WithOpenWorldHintAnnotation(true),
	}
	if mutating {
		options = append(options, sdk.ForceArg())
	}
	return mcp.NewTool(name, options...)
}

func makeStatusHandler(runtime Runtime) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		provider := Provider(request.GetString("provider", ""))
		adapter, err := adapterFor(runtime, provider)
		if err != nil {
			return sdk.WrapError("provider status", err), nil
		}
		status, err := adapter.Status(ctx)
		if err != nil {
			return sdk.WrapError(string(provider)+" status", err), nil
		}
		data, err := json.Marshal(status)
		if err != nil {
			return sdk.WrapError(string(provider)+" status", err), nil
		}
		return boundedResult(runtime, data), nil
	}
}

func makeDiscoverHandler(runtime Runtime, provider Provider) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		adapter, err := adapterFor(runtime, provider)
		if err != nil {
			return sdk.WrapError("discover", err), nil
		}
		discovery := DiscoveryRequest{
			Provider:  provider,
			Service:   request.GetString("service", ""),
			Operation: request.GetString("operation", ""),
		}
		if discovery.Service != "" && !identifierPattern.MatchString(discovery.Service) {
			return sdk.WrapError("discover", fmt.Errorf("invalid service %q", discovery.Service)), nil
		}
		if discovery.Operation != "" && !identifierPattern.MatchString(discovery.Operation) {
			return sdk.WrapError("discover", fmt.Errorf("invalid operation %q", discovery.Operation)), nil
		}
		output, err := adapter.Discover(ctx, discovery)
		if err != nil {
			return sdk.WrapError(string(provider)+" discover", err), nil
		}
		return boundedResult(runtime, output), nil
	}
}

func makeInvokeHandler(runtime Runtime, provider Provider, mode InvocationMode) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		invocation, err := invocationFromRequest(provider, mode, request)
		if err != nil {
			return sdk.WrapError(string(provider)+" invoke", err), nil
		}
		if err := validateInvocationWithEndpointHosts(invocation, runtime.AllowedFileRoots, runtime.AllowedEndpointHosts[provider]); err != nil {
			return sdk.WrapError(string(provider)+" invoke", err), nil
		}
		if invocation.ResponseFile != "" {
			invocation.ResponseFile, err = resolveResponseFileTarget(invocation.ResponseFile, runtime.AllowedFileRoots)
			if err != nil {
				return sdk.WrapError(string(provider)+" invoke", err), nil
			}
			invocation.MaxResponseFileBytes = runtime.MaxResponseFileBytes
		}
		sensitive := isSensitiveInvocation(invocation)
		if mode == ModeRead && !classifyRead(provider, invocation) {
			return sdk.WrapError(string(provider)+" invoke", fmt.Errorf("operation is not classified as read-only; use %s_api_mutate with explicit approval", provider)), nil
		}
		if mode == ModeMutate {
			if sensitive && !runtime.AllowSensitive {
				return sdk.WrapError(string(provider)+" invoke", fmt.Errorf("sensitive cloud operations are disabled; set CLOUD_SKILLS_ALLOW_SENSITIVE=1 after explicit approval")), nil
			}
			if result, _ := sdk.RequireMutationApproval(runtime.AllowMutations, request.GetBool("force", false)); result != nil {
				return result, nil
			}
			if runtime.Audit != nil {
				if err := runtime.Audit(ctx, auditEvent(runtime, invocation, sensitive, "authorized", "")); err != nil {
					return sdk.WrapError("audit", err), nil
				}
			}
		}
		adapter, err := adapterFor(runtime, provider)
		if err != nil {
			return sdk.WrapError(string(provider)+" invoke", err), nil
		}
		result, err := adapter.Invoke(ctx, invocation)
		if err != nil {
			auditBestEffort(ctx, runtime, invocation, sensitive, "failed", "")
			return sdk.WrapError(string(provider)+" invoke", err), nil
		}
		auditBestEffort(ctx, runtime, invocation, sensitive, "succeeded", result.RequestID)
		return boundedResult(runtime, result.Output), nil
	}
}

func invocationFromRequest(provider Provider, mode InvocationMode, request mcp.CallToolRequest) (Invocation, error) {
	arguments := request.GetArguments()
	invocation := Invocation{
		Provider: provider, Mode: mode,
		Service: request.GetString("service", ""), Operation: request.GetString("operation", ""),
		Region: request.GetString("region", ""), RegionSet: request.GetString("region_set", ""), Project: request.GetString("project", ""),
		Subscription: request.GetString("subscription", ""), Audience: request.GetString("audience", ""), RegistryInstanceID: request.GetString("registry_instance_id", ""), RegistryUserID: request.GetString("registry_user_id", ""), ACRScope: request.GetString("acr_scope", ""), ACRSourceScope: request.GetString("acr_source_scope", ""), AuthScheme: request.GetString("auth_scheme", ""), AuthVersion: request.GetString("auth_version", ""), APIVersion: request.GetString("api_version", ""), PayloadMode: request.GetString("payload_mode", ""), ChecksumAlgorithm: request.GetString("checksum_algorithm", ""), Method: request.GetString("method", ""),
		URL:                    request.GetString("url", ""),
		BodyFile:               request.GetString("body_file", ""),
		ImageFile:              request.GetString("image_file", ""),
		ProtobufDescriptorFile: request.GetString("protobuf_descriptor_file", ""),
		ResponseFile:           request.GetString("response_file", ""),
		StreamChunkBytes:       request.GetInt("stream_chunk_bytes", 0),
		StreamIntervalMS:       request.GetInt("stream_interval_ms", 0),
		StreamMaxMessages:      request.GetInt("stream_max_messages", 0),
		StreamTimeoutSeconds:   request.GetInt("stream_timeout_seconds", 0),
		StreamUserID:           request.GetString("stream_user_id", ""),
		StreamFormat:           request.GetInt("stream_format", 0),
	}
	if value, ok := arguments["parameters"]; ok {
		parameters, ok := value.(map[string]any)
		if !ok {
			return Invocation{}, fmt.Errorf("parameters must be an object")
		}
		invocation.Parameters = parameters
	}
	if value, ok := arguments["headers"]; ok {
		rawHeaders, ok := value.(map[string]any)
		if !ok {
			return Invocation{}, fmt.Errorf("headers must be an object")
		}
		invocation.Headers = make(map[string]string, len(rawHeaders))
		for name, value := range rawHeaders {
			text, ok := value.(string)
			if !ok {
				return Invocation{}, fmt.Errorf("header %q must be a string", name)
			}
			invocation.Headers[name] = text
		}
	}
	if body, ok := arguments["body"]; ok {
		invocation.Body = body
	}
	return invocation, nil
}

func boundedResult(runtime Runtime, output []byte) *mcp.CallToolResult {
	if len(output) > runtime.MaxOutputBytes {
		return mcp.NewToolResultError(fmt.Sprintf("response exceeds CLOUD_SKILLS_MAX_OUTPUT_BYTES (%d > %d)", len(output), runtime.MaxOutputBytes))
	}
	return mcp.NewToolResultText(sdk.RedactSecret(string(output)))
}

func adapterFor(runtime Runtime, provider Provider) (Adapter, error) {
	if !isProvider(provider) {
		return nil, fmt.Errorf("unsupported provider %q", provider)
	}
	adapter := runtime.Adapters[provider]
	if adapter == nil {
		return nil, fmt.Errorf("provider %q adapter is not configured", provider)
	}
	return adapter, nil
}

func auditEvent(runtime Runtime, invocation Invocation, sensitive bool, outcome, requestID string) AuditEvent {
	return AuditEvent{
		Time: runtime.Now().UTC(), Provider: invocation.Provider, Mode: invocation.Mode,
		Service: invocation.Service, Operation: invocation.Operation, Method: strings.ToUpper(invocation.Method),
		URL: auditURL(invocation.URL), Region: invocation.Region, RegionSet: invocation.RegionSet, Project: invocation.Project,
		Subscription: invocation.Subscription, RegistryInstanceID: invocation.RegistryInstanceID, RegistryUserID: invocation.RegistryUserID, ACRScope: invocation.ACRScope, ACRSourceScope: invocation.ACRSourceScope, AuthScheme: invocation.AuthScheme, AuthVersion: invocation.AuthVersion, APIVersion: invocation.APIVersion, PayloadMode: invocation.PayloadMode, ChecksumAlgorithm: invocation.ChecksumAlgorithm, Sensitive: sensitive, Outcome: outcome, RequestID: requestID,
	}
}

func auditURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	return parsed.String()
}

func auditBestEffort(ctx context.Context, runtime Runtime, invocation Invocation, sensitive bool, outcome, requestID string) {
	if runtime.Audit == nil {
		return
	}
	_ = runtime.Audit(ctx, auditEvent(runtime, invocation, sensitive, outcome, requestID))
}

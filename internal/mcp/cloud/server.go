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
		mcp.WithString("auth_scheme", mcp.Description("Optional provider HTTP authentication scheme: sigv4, sigv4a, acs3, rpc, roa, datahub, opensearch, odps, odps4, fc, fc3, fc-custom, oss, oss4, sls, sls4, mns, ots, ots4, tc3, tc1, tc1-sha256, qcloud, qcloud-sha256, asr-ws, virtual-number-ws, soe-ws, speech-translate-ws, voice-convert-ws, mps-ws, mps-tts-ws, tts-ws, tts-stream-ws, podcast-ws, cos, or the provider default.")),
		mcp.WithString("auth_version", mcp.Description("Optional Baidu BCE signing version: v1 (default) or v2. BCE v2 also requires service and region.")),
		mcp.WithString("api_version", mcp.Description("Provider API version used by Alibaba ACS3/RPC/ROA/DataHub/SLS/MNS/OTS and Tencent TC3 common parameters or headers.")),
		mcp.WithString("payload_mode", mcp.Description("Optional AWS payload protocol: aws-chunked for SigV4/SigV4a S3 streaming PutObject/UploadPart, aws-chunked-trailer for a server-computed signed checksum trailer, or aws-eventstream for a bounded SigV4 CRC-valid encoded event-stream body.")),
		mcp.WithString("checksum_algorithm", mcp.Description("Required for payload_mode=aws-chunked-trailer: crc32, crc32c, crc64nvme, sha1, or sha256. The server computes the checksum; callers never provide its value.")),
		mcp.WithString("method", mcp.Description("HTTP method for the official provider API request or GET for a WebSocket upgrade.")),
		mcp.WithString("url", mcp.Description("Exact official HTTPS or supported WSS provider API URL.")),
		mcp.WithObject("parameters", mcp.Description("Optional scalar HTTP query parameters."), mcp.AdditionalProperties(true)),
		mcp.WithObject("headers", mcp.Description("Non-credential HTTP headers."), mcp.AdditionalProperties(map[string]any{"type": "string"})),
		mcp.WithAny("body", mcp.Description("Optional JSON-compatible REST request body.")),
		mcp.WithString("body_file", mcp.Description("Optional local HTTP body or WebSocket binary-stream file. The resolved regular file must be under CLOUD_SKILLS_ALLOWED_FILE_ROOTS and cannot be combined with body.")),
		mcp.WithString("response_file", mcp.Description("Optional new local file for a successful HTTP body or WebSocket NDJSON messages. The target must be under CLOUD_SKILLS_ALLOWED_FILE_ROOTS and is never overwritten.")),
		mcp.WithNumber("stream_chunk_bytes", mcp.Description("Optional WebSocket binary message size. Tencent ASR and MPS derive protocol-specific defaults when omitted."), mcp.Min(1), mcp.Max(maxRequestFileBytes)),
		mcp.WithNumber("stream_interval_ms", mcp.Description("Optional Tencent WebSocket pacing interval in milliseconds; ASR defaults to 200 ms and MPS PCM defaults to 40 ms."), mcp.Min(1), mcp.Max(5000)),
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
		Subscription: request.GetString("subscription", ""), Audience: request.GetString("audience", ""), AuthScheme: request.GetString("auth_scheme", ""), AuthVersion: request.GetString("auth_version", ""), APIVersion: request.GetString("api_version", ""), PayloadMode: request.GetString("payload_mode", ""), ChecksumAlgorithm: request.GetString("checksum_algorithm", ""), Method: request.GetString("method", ""),
		URL:              request.GetString("url", ""),
		BodyFile:         request.GetString("body_file", ""),
		ResponseFile:     request.GetString("response_file", ""),
		StreamChunkBytes: request.GetInt("stream_chunk_bytes", 0),
		StreamIntervalMS: request.GetInt("stream_interval_ms", 0),
		StreamUserID:     request.GetString("stream_user_id", ""),
		StreamFormat:     request.GetInt("stream_format", 0),
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
		Subscription: invocation.Subscription, AuthScheme: invocation.AuthScheme, AuthVersion: invocation.AuthVersion, APIVersion: invocation.APIVersion, PayloadMode: invocation.PayloadMode, ChecksumAlgorithm: invocation.ChecksumAlgorithm, Sensitive: sensitive, Outcome: outcome, RequestID: requestID,
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

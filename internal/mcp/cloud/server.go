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
		mcp.WithDescription("Report one provider adapter, CLI and non-secret credential-source readiness."),
		mcp.WithString("provider", mcp.Description("Cloud provider."), mcp.Enum(providers...), mcp.Required()),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(false),
	), makeStatusHandler(runtime))

	for _, provider := range AllProviders() {
		prefix := string(provider)
		srv.AddTool(mcp.NewTool(prefix+"_api_discover",
			mcp.WithDescription("Discover official CLI/API help without invoking a cloud resource operation."),
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
		mcp.WithString("service", mcp.Description("CLI service/product code for AWS, Alibaba Cloud or Tencent Cloud.")),
		mcp.WithString("operation", mcp.Description("CLI API operation/action for AWS, Alibaba Cloud or Tencent Cloud.")),
		mcp.WithString("region", mcp.Description("Optional provider region/location.")),
		mcp.WithString("project", mcp.Description("Optional Google Cloud project.")),
		mcp.WithString("subscription", mcp.Description("Optional Azure subscription.")),
		mcp.WithString("method", mcp.Description("HTTP method for Azure, Google Cloud or Baidu AI Cloud REST calls.")),
		mcp.WithString("url", mcp.Description("Official HTTPS API URL for Azure, Google Cloud or Baidu AI Cloud.")),
		mcp.WithObject("parameters", mcp.Description("Structured provider API parameters."), mcp.AdditionalProperties(true)),
		mcp.WithArray("arguments", mcp.Description("Additional fixed-executable CLI arguments."), mcp.WithStringItems(), mcp.MaxItems(128)),
		mcp.WithObject("headers", mcp.Description("Non-credential HTTP headers."), mcp.AdditionalProperties(map[string]any{"type": "string"})),
		mcp.WithAny("body", mcp.Description("Optional JSON-compatible REST request body.")),
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
		if err := validateInvocation(invocation, runtime.AllowedFileRoots); err != nil {
			return sdk.WrapError(string(provider)+" invoke", err), nil
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
		Region: request.GetString("region", ""), Project: request.GetString("project", ""),
		Subscription: request.GetString("subscription", ""), Method: request.GetString("method", ""),
		URL: request.GetString("url", ""), Arguments: request.GetStringSlice("arguments", nil),
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
		URL: auditURL(invocation.URL), Region: invocation.Region, Project: invocation.Project,
		Subscription: invocation.Subscription, Sensitive: sensitive, Outcome: outcome, RequestID: requestID,
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

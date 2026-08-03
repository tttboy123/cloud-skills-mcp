package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	googleauth "cloud.google.com/go/auth"
	googlecredentials "cloud.google.com/go/auth/credentials"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azurepolicy "github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/tttboy123/cloud-skills-mcp/internal/mcp/sdk"
)

const defaultRESTBodyLimit = 2 * 1024 * 1024

type AzureRESTConfig struct {
	Timeout                                time.Duration
	Tokens                                 AzureTokenProvider
	HTTP                                   HTTPDoer
	WebSocketDial                          azureRealtimeWebSocketDial
	WebPubSubWebSocketDial                 azureWebPubSubWebSocketDial
	ReliableWebPubSubWebSocketDial         azureWebPubSubWebSocketDial
	ProtobufWebPubSubWebSocketDial         azureWebPubSubWebSocketDial
	ReliableProtobufWebPubSubWebSocketDial azureWebPubSubWebSocketDial
	WebPubSubMQTTWebSocketDial             azureWebPubSubWebSocketDial
	EventGridMQTTWebSocketDial             azureWebPubSubWebSocketDial
	AMQPWebSocketDial                      azureAMQPWebSocketDial
	ServiceBusAMQP                         azureServiceBusAMQPExecutor
	EventHubsAMQP                          azureEventHubsAMQPExecutor
	StreamPause                            func(context.Context, time.Duration) error
	MaxBodyBytes                           int64
	AllowedHosts                           []string
}

type AzureRESTAdapter struct {
	config AzureRESTConfig
}

func NewAzureRESTAdapter(config AzureRESTConfig) *AzureRESTAdapter {
	if config.Timeout <= 0 {
		config.Timeout = 60 * time.Second
	}
	if config.Tokens == nil {
		config.Tokens = &azureNonCLITokenProvider{factory: newNonCLIAzureCredential}
	}
	if config.HTTP == nil {
		config.HTTP = &http.Client{
			Timeout:       config.Timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	if config.WebSocketDial == nil {
		config.WebSocketDial = defaultAzureRealtimeWebSocketDial
	}
	if config.WebPubSubWebSocketDial == nil {
		config.WebPubSubWebSocketDial = defaultAzureWebPubSubWebSocketDial
	}
	if config.ReliableWebPubSubWebSocketDial == nil {
		config.ReliableWebPubSubWebSocketDial = defaultAzureReliableWebPubSubWebSocketDial
	}
	if config.ProtobufWebPubSubWebSocketDial == nil {
		config.ProtobufWebPubSubWebSocketDial = defaultAzureProtobufWebPubSubWebSocketDial
	}
	if config.ReliableProtobufWebPubSubWebSocketDial == nil {
		config.ReliableProtobufWebPubSubWebSocketDial = defaultAzureReliableProtobufWebPubSubWebSocketDial
	}
	if config.WebPubSubMQTTWebSocketDial == nil {
		config.WebPubSubMQTTWebSocketDial = defaultAzureWebPubSubMQTTWebSocketDial
	}
	if config.EventGridMQTTWebSocketDial == nil {
		config.EventGridMQTTWebSocketDial = defaultAzureEventGridMQTTWebSocketDial
	}
	if config.AMQPWebSocketDial == nil {
		config.AMQPWebSocketDial = defaultAzureAMQPWebSocketDial
	}
	credential := azureAMQPTokenCredential{provider: config.Tokens}
	if config.ServiceBusAMQP == nil {
		config.ServiceBusAMQP = &azureSDKServiceBusAMQPExecutor{credential: credential, dial: config.AMQPWebSocketDial}
	}
	if config.EventHubsAMQP == nil {
		config.EventHubsAMQP = &azureSDKEventHubsAMQPExecutor{credential: credential, dial: config.AMQPWebSocketDial}
	}
	if config.StreamPause == nil {
		config.StreamPause = pauseTencentStream
	}
	if config.MaxBodyBytes <= 0 {
		config.MaxBodyBytes = defaultRESTBodyLimit
	}
	return &AzureRESTAdapter{config: config}
}

func (adapter *AzureRESTAdapter) Status(ctx context.Context) (ProviderStatus, error) {
	_ = ctx
	return ProviderStatus{
		Provider: ProviderAzure, Available: true, Adapter: "Azure HTTPS/WSS + non-CLI Azure Identity", Version: "azidentity+acr-oauth2+realtime-ws+voice-live-ws+webpubsub-json-protobuf-reliable+mqtt5+eventgrid-mqtt5+servicebus-amqp1+eventhubs-amqp1",
		CredentialSource: credentialSource(ProviderAzure), CredentialStatus: CredentialStatusUnverified,
		Message: "credentials are resolved lazily through Environment, Workload Identity, or Managed Identity; no Azure CLI credential is included",
	}, nil
}

func (adapter *AzureRESTAdapter) Discover(ctx context.Context, _ DiscoveryRequest) ([]byte, error) {
	_ = ctx
	return json.Marshal(map[string]string{
		"rest_api_reference":  "https://learn.microsoft.com/en-us/rest/api/azure/",
		"authentication":      "https://learn.microsoft.com/en-us/azure/developer/go/sdk/authentication/credential-chains",
		"acr_authentication":  "https://learn.microsoft.com/en-us/rest/api/registry-dataplane/authentication/exchange-aad-access-token-for-acr-refresh-token",
		"acr_scoped_token":    "https://learn.microsoft.com/en-us/rest/api/registry-dataplane/authentication/exchange-acr-refresh-token-for-acr-access-token",
		"acr_data_plane":      "https://learn.microsoft.com/en-us/rest/api/registry-dataplane/container-registry",
		"openai_realtime":     "https://learn.microsoft.com/en-us/azure/foundry/openai/how-to/realtime-audio-websockets",
		"voice_live":          "https://learn.microsoft.com/en-us/azure/ai-services/speech-service/voice-live-how-to",
		"webpubsub_protocol":  "https://learn.microsoft.com/en-us/azure/azure-web-pubsub/reference-json-webpubsub-subprotocol",
		"webpubsub_reliable":  "https://learn.microsoft.com/en-us/azure/azure-web-pubsub/reference-json-reliable-webpubsub-subprotocol",
		"webpubsub_mqtt":      "https://learn.microsoft.com/en-us/azure/azure-web-pubsub/howto-connect-mqtt-websocket-client",
		"eventgrid_mqtt":      "https://learn.microsoft.com/en-us/azure/event-grid/mqtt-support",
		"eventgrid_mqtt_auth": "https://learn.microsoft.com/en-us/azure/event-grid/mqtt-client-microsoft-entra-token-and-rbac",
		"messaging_amqp":      "https://learn.microsoft.com/en-us/azure/service-bus-messaging/service-bus-amqp-protocol-guide",
		"servicebus_auth":     "https://learn.microsoft.com/en-us/azure/service-bus-messaging/service-bus-authentication-and-authorization",
		"eventhubs_auth":      "https://learn.microsoft.com/en-us/azure/event-hubs/authenticate-application",
	})
}

func (adapter *AzureRESTAdapter) Invoke(ctx context.Context, request Invocation) (InvocationResult, error) {
	scheme := normalizedAuthScheme(request.AuthScheme, "")
	if scheme == authSchemeAzureACR {
		return invokeAzureACR(ctx, adapter, request)
	}
	if scheme == authSchemeAzureRealtimeWS {
		return invokeAzureRealtimeWebSocket(ctx, adapter, request)
	}
	if scheme == authSchemeAzureVoiceLiveWS {
		return invokeAzureRealtimeWebSocket(ctx, adapter, request)
	}
	if scheme == authSchemeAzureWebPubSubWS {
		return invokeAzureWebPubSub(ctx, adapter, request)
	}
	if scheme == authSchemeAzureWebPubSubMQTTWS {
		return invokeAzureWebPubSubMQTT(ctx, adapter, request)
	}
	if scheme == authSchemeAzureEventGridMQTTWS {
		return invokeAzureEventGridMQTT(ctx, adapter, request)
	}
	if scheme == authSchemeAzureServiceBusAMQPWS {
		return invokeAzureServiceBusAMQP(ctx, adapter, request)
	}
	if scheme == authSchemeAzureEventHubsAMQPWS {
		return invokeAzureEventHubsAMQP(ctx, adapter, request)
	}
	if scheme != "" {
		return InvocationResult{}, fmt.Errorf("Azure auth_scheme must be acr, realtime-ws, voice-live-ws, webpubsub-ws, webpubsub-mqtt-ws, eventgrid-mqtt-ws, servicebus-amqp-ws, eventhubs-amqp-ws, or omitted for REST")
	}
	scope, err := azureScopeForInvocationWithEndpointHosts(request.URL, request.Audience, adapter.config.AllowedHosts)
	if err != nil {
		return InvocationResult{}, err
	}
	if scope == "" {
		return InvocationResult{}, fmt.Errorf("Azure HTTP audience cannot be inferred for this endpoint; provide the public audience field")
	}
	return adapter.invokeHTTP(ctx, request, scope)
}

func (adapter *AzureRESTAdapter) invokeHTTP(ctx context.Context, invocation Invocation, scope string) (InvocationResult, error) {
	body, contentLength, cleanup, err := prepareRESTBody(invocation)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("prepare Azure request body: %w", err)
	}
	defer cleanup()
	request, err := http.NewRequestWithContext(ctx, strings.ToUpper(invocation.Method), invocation.URL, body)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("build Azure request: %w", err)
	}
	if err := addQueryParameters(request.URL, invocation.Parameters); err != nil {
		return InvocationResult{}, fmt.Errorf("build Azure query: %w", err)
	}
	for name, value := range invocation.Headers {
		request.Header.Set(name, value)
	}
	if contentLength >= 0 {
		request.ContentLength = contentLength
	}
	if (invocation.Body != nil || invocation.BodyFile != "") && request.Header.Get("Content-Type") == "" {
		request.Header.Set("Content-Type", invocationContentType(invocation))
	}
	token, err := adapter.config.Tokens.Token(ctx, scope)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("load Azure identity token: %w", err)
	}
	if strings.TrimSpace(token) == "" {
		return InvocationResult{}, fmt.Errorf("Azure identity returned an empty access token")
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := adapter.config.HTTP.Do(request)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("Azure REST API request: %w", err)
	}
	output, err := readRESTResponseWithFile(response, adapter.config.MaxBodyBytes, invocation.ResponseFile, invocation.MaxResponseFileBytes)
	if err != nil {
		return InvocationResult{}, err
	}
	return InvocationResult{Output: output, RequestID: responseRequestID(response.Header)}, nil
}

type AzureTokenProvider interface {
	Token(context.Context, string) (string, error)
}

type azureTokenCredential interface {
	GetToken(context.Context, azurepolicy.TokenRequestOptions) (azcore.AccessToken, error)
}

type azureNonCLITokenProvider struct {
	factory    func() (azureTokenCredential, error)
	once       sync.Once
	credential azureTokenCredential
	err        error
}

func newNonCLIAzureCredential() (azureTokenCredential, error) {
	sources := make([]azcore.TokenCredential, 0, 3)
	if hasAzureServicePrincipalEnvironment(os.Environ()) {
		credential, err := azidentity.NewEnvironmentCredential(nil)
		if err != nil {
			return nil, err
		}
		sources = append(sources, credential)
	}
	if hasAzureWorkloadIdentityEnvironment(os.Environ()) {
		credential, err := azidentity.NewWorkloadIdentityCredential(nil)
		if err != nil {
			return nil, err
		}
		sources = append(sources, credential)
	}
	credential, err := azidentity.NewManagedIdentityCredential(nil)
	if err != nil {
		return nil, err
	}
	sources = append(sources, credential)
	if len(sources) == 1 {
		return sources[0], nil
	}
	return azidentity.NewChainedTokenCredential(sources, nil)
}

func (provider *azureNonCLITokenProvider) Token(ctx context.Context, scope string) (string, error) {
	provider.once.Do(func() {
		provider.credential, provider.err = provider.factory()
	})
	if provider.err != nil {
		return "", fmt.Errorf("create non-CLI Azure Identity credential chain: %w", provider.err)
	}
	accessToken, err := provider.credential.GetToken(ctx, azurepolicy.TokenRequestOptions{Scopes: []string{scope}})
	if err != nil {
		return "", fmt.Errorf("Azure Identity credential token: %w", err)
	}
	return accessToken.Token, nil
}

func hasAzureServicePrincipalEnvironment(environment []string) bool {
	return azureEnvironmentMatches(environment, hasAzureServicePrincipalValues)
}

func hasAzureWorkloadIdentityEnvironment(environment []string) bool {
	return azureEnvironmentMatches(environment, hasAzureWorkloadIdentityValues)
}

func azureEnvironmentMatches(environment []string, match func(func(string) bool) bool) bool {
	values := make(map[string]bool)
	for _, entry := range environment {
		name, value, found := strings.Cut(entry, "=")
		values[name] = found && strings.TrimSpace(value) != ""
	}
	return match(func(name string) bool { return values[name] })
}

func hasAzureServicePrincipalValues(has func(string) bool) bool {
	return has("AZURE_TENANT_ID") && has("AZURE_CLIENT_ID") && (has("AZURE_CLIENT_SECRET") || has("AZURE_CLIENT_CERTIFICATE_PATH"))
}

func hasAzureWorkloadIdentityValues(has func(string) bool) bool {
	return has("AZURE_TENANT_ID") && has("AZURE_CLIENT_ID") && has("AZURE_FEDERATED_TOKEN_FILE")
}

func azureScopeForURL(rawURL string) (string, error) {
	return azureScopeForInvocation(rawURL, "")
}

func azureScopeForInvocation(rawURL, explicitAudience string) (string, error) {
	return azureScopeForInvocationWithEndpointHosts(rawURL, explicitAudience, nil)
}

func azureScopeForInvocationWithEndpointHosts(rawURL, explicitAudience string, allowedEndpointHosts []string) (string, error) {
	if err := validateRESTTargetWithEndpointHosts(ProviderAzure, http.MethodGet, rawURL, allowedEndpointHosts); err != nil {
		return "", err
	}
	if explicitAudience != "" {
		audience, err := normalizeAzureAudience(explicitAudience)
		if err != nil {
			return "", err
		}
		return audience + "/.default", nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse Azure REST URL: %w", err)
	}
	host := strings.ToLower(parsed.Hostname())
	switch {
	case host == "management.azure.com":
		return "https://management.azure.com/.default", nil
	case host == "management.chinacloudapi.cn":
		return "https://management.chinacloudapi.cn/.default", nil
	case host == "management.usgovcloudapi.net":
		return "https://management.usgovcloudapi.net/.default", nil
	case host == "management.microsoftazure.de":
		return "https://management.microsoftazure.de/.default", nil
	case host == "graph.microsoft.com":
		return "https://graph.microsoft.com/.default", nil
	case host == "atlas.microsoft.com" || strings.HasSuffix(host, ".atlas.microsoft.com"):
		return "https://atlas.microsoft.com/.default", nil
	case strings.HasSuffix(host, ".dicom.azurehealthcareapis.com"):
		return "https://dicom.healthcareapis.azure.com/.default", nil
	case host == "azurehealthcareapis.com" || strings.HasSuffix(host, ".azurehealthcareapis.com"):
		return "https://" + host + "/.default", nil
	case strings.HasSuffix(host, ".azconfig.io"):
		return "https://appconfig.azure.com/.default", nil
	case strings.HasSuffix(host, ".search.windows.net"):
		return "https://search.azure.com/.default", nil
	case strings.HasSuffix(host, ".azuredatabricks.net"):
		return "2ff814a6-3304-4ab8-85cb-cd0e6f879c1d/.default", nil
	case strings.HasSuffix(host, ".grafana.azure.com"):
		return "https://dashboard.azure.com/.default", nil
	case strings.HasSuffix(host, ".webpubsub.azure.com"):
		return "https://webpubsub.azure.com/.default", nil
	case strings.HasSuffix(host, ".eventgrid.azure.net"):
		return azureEventGridScope, nil
	case strings.HasSuffix(host, ".service.signalr.net"):
		return "https://signalr.azure.com/.default", nil
	case strings.HasSuffix(host, ".digitaltwins.azure.net"):
		return "https://digitaltwins.azure.net/.default", nil
	case strings.HasSuffix(host, ".dev.azuresynapse.net"):
		return "https://dev.azuresynapse.net/.default", nil
	case host == "api.loganalytics.io" || strings.HasSuffix(host, ".api.loganalytics.io"):
		return "https://api.loganalytics.io/.default", nil
	case strings.HasSuffix(host, ".azurecr.io"):
		return "https://containerregistry.azure.net/.default", nil
	case hasAnySuffix(host, ".blob.core.windows.net", ".dfs.core.windows.net", ".queue.core.windows.net", ".table.core.windows.net"):
		return "https://storage.azure.com/.default", nil
	case host == "vault.azure.net" || strings.HasSuffix(host, ".vault.azure.net"):
		return "https://vault.azure.net/.default", nil
	case host == "database.windows.net" || strings.HasSuffix(host, ".database.windows.net"):
		return "https://database.windows.net/.default", nil
	case host == "servicebus.windows.net" || strings.HasSuffix(host, ".servicebus.windows.net"):
		return "https://servicebus.azure.net/.default", nil
	case host == "monitor.azure.com" || strings.HasSuffix(host, ".monitor.azure.com"):
		return "https://monitor.azure.com/.default", nil
	case strings.HasSuffix(host, ".cognitiveservices.azure.com") || strings.HasSuffix(host, ".openai.azure.com"):
		return "https://cognitiveservices.azure.com/.default", nil
	default:
		return "", nil
	}
}

type TokenProvider interface {
	Token(context.Context) (string, error)
}

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type GCPRESTConfig struct {
	Tokens                  TokenProvider
	FirebaseTokens          TokenProvider
	HTTP                    HTTPDoer
	FirebaseHTTP            HTTPDoer
	GRPCHTTP                HTTPDoer
	VertexLiveWebSocketDial gcpVertexLiveWebSocketDial
	MaxBodyBytes            int64
	Timeout                 time.Duration
	AllowedHosts            []string
	StreamPause             func(context.Context, time.Duration) error
}

type GCPRESTAdapter struct {
	config GCPRESTConfig
}

func NewGCPRESTAdapter(config GCPRESTConfig) *GCPRESTAdapter {
	customHTTP := config.HTTP != nil
	if config.Timeout <= 0 {
		config.Timeout = 60 * time.Second
	}
	customTokens := config.Tokens != nil
	if config.Tokens == nil {
		config.Tokens = &gcpADCTokenProvider{detect: detectDefaultGoogleCredentials}
	}
	if config.FirebaseTokens == nil {
		if customTokens {
			config.FirebaseTokens = config.Tokens
		} else {
			config.FirebaseTokens = &gcpADCTokenProvider{
				detectScopes: detectGoogleCredentialsWithScopes,
				scopes:       []string{gcpFirebaseDatabaseScope, gcpUserInfoEmailScope},
			}
		}
	}
	if config.HTTP == nil {
		config.HTTP = &http.Client{
			Timeout:       config.Timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	if config.FirebaseHTTP == nil {
		if customHTTP {
			config.FirebaseHTTP = config.HTTP
		} else {
			config.FirebaseHTTP = &http.Client{
				CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
			}
		}
	}
	if config.GRPCHTTP == nil {
		if customHTTP {
			config.GRPCHTTP = config.HTTP
		} else {
			config.GRPCHTTP = &http.Client{
				CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
			}
		}
	}
	if config.VertexLiveWebSocketDial == nil {
		config.VertexLiveWebSocketDial = defaultGCPVertexLiveWebSocketDial
	}
	if config.StreamPause == nil {
		config.StreamPause = pauseTencentStream
	}
	if config.MaxBodyBytes <= 0 {
		config.MaxBodyBytes = defaultRESTBodyLimit
	}
	return &GCPRESTAdapter{config: config}
}

func (adapter *GCPRESTAdapter) Status(context.Context) (ProviderStatus, error) {
	return ProviderStatus{
		Provider: ProviderGCP, Available: true, Adapter: "googleapis REST/gRPC/WSS/SSE + ADC",
		Version: "google-auth/v0.22+grpc-http2+grpc-protojson+vertex-live-ws+firebase-sse", CredentialSource: credentialSource(ProviderGCP), CredentialStatus: CredentialStatusUnverified,
		Message: "credentials are resolved lazily through ADC; no gcloud subprocess fallback exists",
	}, nil
}

func (adapter *GCPRESTAdapter) Discover(ctx context.Context, request DiscoveryRequest) ([]byte, error) {
	if strings.EqualFold(request.Service, "firebase-database") {
		return json.Marshal(map[string]string{
			"rest_streaming": "https://firebase.google.com/docs/database/rest/retrieve-data#section-rest-streaming",
			"authentication": "https://firebase.google.com/docs/database/rest/auth",
			"locations":      "https://firebase.google.com/docs/database/locations",
			"limits":         "https://firebase.google.com/docs/database/usage/limits",
		})
	}
	url := "https://www.googleapis.com/discovery/v1/apis"
	if request.Service != "" {
		version := request.Operation
		if version == "" {
			version = "v1"
		}
		url += "/" + request.Service + "/" + version + "/rest"
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build Google API discovery request: %w", err)
	}
	response, err := adapter.config.HTTP.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("Google API discovery request: %w", err)
	}
	return readRESTResponse(response, adapter.config.MaxBodyBytes)
}

func (adapter *GCPRESTAdapter) Invoke(ctx context.Context, request Invocation) (InvocationResult, error) {
	scheme := normalizedAuthScheme(request.AuthScheme, "")
	if scheme == authSchemeGCPFirebaseSSE {
		return invokeGCPFirebaseSSE(ctx, adapter, request)
	}
	if scheme == authSchemeGCPVertexLiveWS {
		return invokeGCPVertexLiveWebSocket(ctx, adapter, request)
	}
	if scheme == authSchemeGCPGRPC {
		return invokeGCPGRPC(ctx, adapter, request)
	}
	if scheme != "" {
		return InvocationResult{}, fmt.Errorf("Google Cloud auth_scheme must be firebase-sse, grpc, vertex-live-ws, or omitted for REST")
	}
	if err := validateRESTTargetWithEndpointHosts(ProviderGCP, request.Method, request.URL, adapter.config.AllowedHosts); err != nil {
		return InvocationResult{}, err
	}
	body, contentLength, cleanup, err := prepareRESTBody(request)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("prepare Google Cloud request body: %w", err)
	}
	defer cleanup()
	httpRequest, err := http.NewRequestWithContext(ctx, strings.ToUpper(request.Method), request.URL, body)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("build Google Cloud request: %w", err)
	}
	if err := addQueryParameters(httpRequest.URL, request.Parameters); err != nil {
		return InvocationResult{}, fmt.Errorf("build Google Cloud query: %w", err)
	}
	for name, value := range request.Headers {
		httpRequest.Header.Set(name, value)
	}
	if contentLength >= 0 {
		httpRequest.ContentLength = contentLength
	}
	if (request.Body != nil || request.BodyFile != "") && httpRequest.Header.Get("Content-Type") == "" {
		httpRequest.Header.Set("Content-Type", invocationContentType(request))
	}
	token, err := adapter.config.Tokens.Token(ctx)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("load Google Cloud application credential: %w", err)
	}
	if strings.TrimSpace(token) == "" {
		return InvocationResult{}, fmt.Errorf("Google Cloud credential returned an empty access token")
	}
	httpRequest.Header.Set("Authorization", "Bearer "+token)
	if request.Project != "" {
		httpRequest.Header.Set("X-Goog-User-Project", request.Project)
	}
	response, err := adapter.config.HTTP.Do(httpRequest)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("Google Cloud API request: %w", err)
	}
	output, err := readRESTResponseWithFile(response, adapter.config.MaxBodyBytes, request.ResponseFile, request.MaxResponseFileBytes)
	if err != nil {
		return InvocationResult{}, err
	}
	return InvocationResult{Output: output, RequestID: responseRequestID(response.Header)}, nil
}

type googleAuthTokenSource interface {
	Token(context.Context) (*googleauth.Token, error)
}

type gcpADCTokenProvider struct {
	detect       func(context.Context) (googleAuthTokenSource, error)
	detectScopes func(context.Context, []string) (googleAuthTokenSource, error)
	scopes       []string
	once         sync.Once
	source       googleAuthTokenSource
	err          error
}

func detectDefaultGoogleCredentials(context.Context) (googleAuthTokenSource, error) {
	return googlecredentials.DetectDefault(&googlecredentials.DetectOptions{
		Scopes: []string{"https://www.googleapis.com/auth/cloud-platform"},
	})
}

func detectGoogleCredentialsWithScopes(_ context.Context, scopes []string) (googleAuthTokenSource, error) {
	return googlecredentials.DetectDefault(&googlecredentials.DetectOptions{Scopes: append([]string(nil), scopes...)})
}

func (provider *gcpADCTokenProvider) Token(ctx context.Context) (string, error) {
	provider.once.Do(func() {
		if len(provider.scopes) > 0 && provider.detectScopes != nil {
			provider.source, provider.err = provider.detectScopes(ctx, append([]string(nil), provider.scopes...))
		} else if provider.detect != nil {
			provider.source, provider.err = provider.detect(ctx)
		} else {
			provider.err = fmt.Errorf("Google Cloud ADC detector is not configured")
		}
	})
	if provider.err != nil {
		return "", fmt.Errorf("detect Google Cloud Application Default Credentials: %w", provider.err)
	}
	if provider.source == nil {
		return "", fmt.Errorf("Google Cloud Application Default Credentials returned no token provider")
	}
	token, err := provider.source.Token(ctx)
	if err != nil {
		return "", fmt.Errorf("load Google Cloud ADC access token: %w", err)
	}
	if token == nil || strings.TrimSpace(token.Value) == "" {
		return "", fmt.Errorf("Google Cloud ADC returned an empty access token")
	}
	return strings.TrimSpace(token.Value), nil
}

func readRESTResponse(response *http.Response, maxBytes int64) ([]byte, error) {
	return readRESTResponseWithFile(response, maxBytes, "", 0)
}

func readRESTResponseWithFile(response *http.Response, maxBytes int64, responseFile string, maxResponseFileBytes int64) ([]byte, error) {
	if response == nil || response.Body == nil {
		return nil, fmt.Errorf("provider returned an empty HTTP response")
	}
	defer response.Body.Close()
	if response.StatusCode >= 200 && response.StatusCode < 300 && responseFile != "" {
		return streamRESTResponseToFile(response, responseFile, maxResponseFileBytes)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read provider response: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("provider response exceeds %d bytes", maxBytes)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("provider returned HTTP %d: %s", response.StatusCode, sdk.RedactSecret(string(data)))
	}
	return data, nil
}

func streamRESTResponseToFile(response *http.Response, responseFile string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = defaultResponseFileLimit
	}
	target, err := filepath.Abs(responseFile)
	if err != nil {
		return nil, fmt.Errorf("resolve response_file: %w", err)
	}
	if _, err := os.Lstat(target); err == nil {
		return nil, fmt.Errorf("response_file already exists")
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect response_file: %w", err)
	}
	if response.ContentLength > maxBytes {
		return nil, fmt.Errorf("provider response download exceeds %d bytes", maxBytes)
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".cloud-skills-download-*")
	if err != nil {
		return nil, fmt.Errorf("create response_file temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return nil, fmt.Errorf("secure response_file temporary file: %w", err)
	}
	written, copyErr := io.Copy(temporary, io.LimitReader(response.Body, maxBytes+1))
	if copyErr != nil {
		temporary.Close()
		return nil, fmt.Errorf("write response_file: %w", copyErr)
	}
	if written > maxBytes {
		temporary.Close()
		return nil, fmt.Errorf("provider response download exceeds %d bytes", maxBytes)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return nil, fmt.Errorf("sync response_file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return nil, fmt.Errorf("close response_file: %w", err)
	}
	// A same-directory hard link publishes the complete file atomically and
	// fails if another process created the target after policy validation.
	if err := os.Link(temporaryPath, target); err != nil {
		if _, statErr := os.Lstat(target); statErr == nil {
			return nil, fmt.Errorf("response_file already exists")
		}
		return nil, fmt.Errorf("publish response_file: %w", err)
	}
	metadata, err := json.Marshal(map[string]any{
		"response_file": target,
		"bytes":         written,
		"content_type":  response.Header.Get("Content-Type"),
		"etag":          response.Header.Get("ETag"),
		"request_id":    responseRequestID(response.Header),
	})
	if err != nil {
		_ = os.Remove(target)
		return nil, fmt.Errorf("encode response_file metadata: %w", err)
	}
	return metadata, nil
}

func responseRequestID(headers http.Header) string {
	for _, name := range []string{"X-Request-Id", "X-Ms-Request-Id", "X-Goog-Request-Id", "X-Firebase-Request-Id", "X-Cloud-Trace-Context", "X-Bce-Request-Id", "X-Cls-Requestid", "X-NLS-RequestId", "Request-Id"} {
		if value := headers.Get(name); value != "" {
			return value
		}
	}
	return ""
}

func prepareRESTBody(invocation Invocation) (io.Reader, int64, func(), error) {
	if invocation.Body != nil && invocation.BodyFile != "" {
		return nil, -1, func() {}, fmt.Errorf("body and body_file are mutually exclusive")
	}
	if invocation.BodyFile != "" {
		file, err := os.Open(invocation.BodyFile)
		if err != nil {
			return nil, -1, func() {}, fmt.Errorf("open body_file: %w", err)
		}
		info, err := file.Stat()
		if err != nil {
			file.Close()
			return nil, -1, func() {}, fmt.Errorf("inspect body_file: %w", err)
		}
		if !info.Mode().IsRegular() {
			file.Close()
			return nil, -1, func() {}, fmt.Errorf("body_file must be a regular file")
		}
		if info.Size() > maxRequestFileBytes {
			file.Close()
			return nil, -1, func() {}, fmt.Errorf("body_file exceeds %d bytes", maxRequestFileBytes)
		}
		return file, info.Size(), func() { _ = file.Close() }, nil
	}
	if invocation.Body != nil {
		var data []byte
		var err error
		switch typed := invocation.Body.(type) {
		case string:
			data = []byte(typed)
		case []byte:
			data = typed
		default:
			data, err = json.Marshal(invocation.Body)
		}
		if err != nil {
			return nil, -1, func() {}, fmt.Errorf("encode JSON body: %w", err)
		}
		return bytes.NewReader(data), int64(len(data)), func() {}, nil
	}
	return nil, -1, func() {}, nil
}

func invocationContentType(invocation Invocation) string {
	if invocation.BodyFile != "" {
		return "application/octet-stream"
	}
	return "application/json"
}

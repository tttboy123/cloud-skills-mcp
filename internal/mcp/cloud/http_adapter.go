package cloud

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	alibabacredentials "github.com/aliyun/credentials-go/credentials"
	aws "github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
)

const (
	authSchemeAWSSigV4           = "sigv4"
	authSchemeAWSSigV4a          = "sigv4a"
	authSchemeAlibabaACS3        = "acs3"
	authSchemeAlibabaRPCV2       = "rpc"
	authSchemeAlibabaROAV2       = "roa"
	authSchemeAlibabaDataHub     = "datahub"
	authSchemeAlibabaOpenSearch  = "opensearch"
	authSchemeAlibabaODPS        = "odps"
	authSchemeAlibabaODPSV4      = "odps4"
	authSchemeAlibabaFC          = "fc"
	authSchemeAlibabaFC3         = "fc3"
	authSchemeAlibabaFCCustom    = "fc-custom"
	authSchemeAlibabaOSS         = "oss"
	authSchemeAlibabaOSSV4       = "oss4"
	authSchemeAlibabaSLS         = "sls"
	authSchemeAlibabaSLSV4       = "sls4"
	authSchemeAlibabaMNS         = "mns"
	authSchemeAlibabaOTS         = "ots"
	authSchemeAlibabaOTSV4       = "ots4"
	authSchemeAlibabaNLSWS       = "nls-ws"
	authSchemeAlibabaNLSREST     = "nls-rest"
	authSchemeTencentTC3         = "tc3"
	authSchemeTencentV1          = "tc1"
	authSchemeTencentV1SHA256    = "tc1-sha256"
	authSchemeTencentQCloud      = "qcloud"
	authSchemeTencentQCloud256   = "qcloud-sha256"
	authSchemeTencentASRWS       = "asr-ws"
	authSchemeTencentVirtualWS   = "virtual-number-ws"
	authSchemeTencentSOEWS       = "soe-ws"
	authSchemeTencentTranslateWS = "speech-translate-ws"
	authSchemeTencentVoiceWS     = "voice-convert-ws"
	authSchemeTencentMPSWS       = "mps-ws"
	authSchemeTencentMPSTTSWS    = "mps-tts-ws"
	authSchemeTencentTTSWS       = "tts-ws"
	authSchemeTencentTTSStreamWS = "tts-stream-ws"
	authSchemeTencentPodcastWS   = "podcast-ws"
	authSchemeTencentCOS         = "cos"
	defaultHTTPClientTimeout     = 60 * time.Second
)

type AWSCredentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

type AWSCredentialProvider interface {
	Credentials(context.Context) (AWSCredentials, error)
}

type awsDefaultCredentialProvider struct {
	once     sync.Once
	provider aws.CredentialsProvider
	err      error
}

func (provider *awsDefaultCredentialProvider) Credentials(ctx context.Context) (AWSCredentials, error) {
	provider.once.Do(func() {
		configuration, err := awsconfig.LoadDefaultConfig(ctx)
		if err != nil {
			provider.err = err
			return
		}
		provider.provider = configuration.Credentials
	})
	if provider.err != nil {
		return AWSCredentials{}, fmt.Errorf("load AWS credential chain: %w", provider.err)
	}
	if provider.provider == nil {
		return AWSCredentials{}, fmt.Errorf("AWS credential chain returned no provider")
	}
	credentials, err := provider.provider.Retrieve(ctx)
	if err != nil {
		return AWSCredentials{}, fmt.Errorf("retrieve AWS credentials: %w", err)
	}
	return AWSCredentials{
		AccessKeyID: credentials.AccessKeyID, SecretAccessKey: credentials.SecretAccessKey, SessionToken: credentials.SessionToken,
	}, nil
}

type AWSRESTConfig struct {
	Credentials                 AWSCredentialProvider
	HTTP                        HTTPDoer
	MaxBodyBytes                int64
	Timeout                     time.Duration
	Now                         func() time.Time
	WebSocketDial               func(context.Context, string) (cloudWebSocketConnection, error)
	IoTWebSocketDial            func(context.Context, string) (cloudWebSocketConnection, error)
	AppSyncEventWebSocketDial   func(context.Context, string, []string) (cloudWebSocketConnection, error)
	AppSyncEventID              func() (string, error)
	AppSyncGraphQLWebSocketDial func(context.Context, string, []string) (cloudWebSocketConnection, error)
	AppSyncGraphQLID            func() (string, error)
	SigV4WebSocketDial          func(context.Context, string, http.Header) (cloudWebSocketConnection, error)
	ConnectHealthWebSocketDial  func(context.Context, string) (cloudWebSocketConnection, error)
	StreamPause                 func(context.Context, time.Duration) error
	AllowedHosts                []string
}

type AWSRESTAdapter struct {
	config AWSRESTConfig
}

func NewAWSRESTAdapter(config AWSRESTConfig) *AWSRESTAdapter {
	if config.Credentials == nil {
		config.Credentials = &awsDefaultCredentialProvider{}
	}
	normalizeSignedHTTPConfig(&config.HTTP, &config.Timeout, &config.MaxBodyBytes, &config.Now)
	if config.WebSocketDial == nil {
		config.WebSocketDial = defaultAWSTranscribeWebSocketDial
	}
	if config.IoTWebSocketDial == nil {
		config.IoTWebSocketDial = defaultAWSIoTMQTTWebSocketDial
	}
	if config.AppSyncEventWebSocketDial == nil {
		config.AppSyncEventWebSocketDial = defaultAWSAppSyncEventWebSocketDial
	}
	if config.AppSyncEventID == nil {
		config.AppSyncEventID = newAWSAppSyncEventID
	}
	if config.AppSyncGraphQLWebSocketDial == nil {
		config.AppSyncGraphQLWebSocketDial = defaultAWSAppSyncGraphQLWebSocketDial
	}
	if config.AppSyncGraphQLID == nil {
		config.AppSyncGraphQLID = newAWSAppSyncEventID
	}
	if config.SigV4WebSocketDial == nil {
		config.SigV4WebSocketDial = defaultAWSSigV4WebSocketDial
	}
	if config.ConnectHealthWebSocketDial == nil {
		config.ConnectHealthWebSocketDial = defaultAWSConnectHealthWebSocketDial
	}
	if config.StreamPause == nil {
		config.StreamPause = pauseTencentStream
	}
	return &AWSRESTAdapter{config: config}
}

func (adapter *AWSRESTAdapter) Status(context.Context) (ProviderStatus, error) {
	return ProviderStatus{
		Provider: ProviderAWS, Available: true, Adapter: "AWS SigV4/SigV4a HTTPS and guarded WSS", Version: "sigv4+sigv4a+sigv4-ws+connect-health-ws+transcribe-ws+iot-mqtt-ws+kinesisvideo-signaling-ws+appsync-event-ws+appsync-graphql-ws",
		CredentialSource: credentialSource(ProviderAWS), CredentialStatus: CredentialStatusUnverified,
		Message: "credentials are resolved lazily through the AWS SDK credential chain; no cloud CLI is executed",
	}, nil
}

func (adapter *AWSRESTAdapter) Discover(context.Context, DiscoveryRequest) ([]byte, error) {
	return json.Marshal(map[string]string{
		"api_reference":                    "https://docs.aws.amazon.com/index.html#lang/en_us",
		"authentication":                   "https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv.html",
		"sigv4a":                           "https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv-create-signed-request.html",
		"transcribe_websocket":             "https://docs.aws.amazon.com/transcribe/latest/dg/streaming-setting-up.html",
		"iot_mqtt_websocket":               "https://docs.aws.amazon.com/iot/latest/developerguide/protocols.html",
		"kinesisvideo_signaling_websocket": "https://docs.aws.amazon.com/kinesisvideostreams-webrtc-dg/latest/devguide/kvswebrtc-websocket-apis.html",
		"appsync_event_ws":                 "https://docs.aws.amazon.com/appsync/latest/eventapi/event-api-websocket-protocol.html",
		"appsync_graphql_ws":               "https://docs.aws.amazon.com/appsync/latest/devguide/real-time-websocket-client.html",
		"sigv4_websocket":                  "https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv.html",
		"agentcore_websocket":              "https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/runtime-get-started-websocket.html",
		"blockchain_websocket":             "https://docs.aws.amazon.com/managed-blockchain/latest/ethereum-dev/json-rpc-api-examples.html",
		"connect_health_ws":                "https://docs.aws.amazon.com/connecthealth/latest/userguide/ambient-documentation.html",
	})
}

func (adapter *AWSRESTAdapter) Invoke(ctx context.Context, invocation Invocation) (InvocationResult, error) {
	scheme := normalizedAuthScheme(invocation.AuthScheme, authSchemeAWSSigV4)
	if scheme != authSchemeAWSSigV4 && scheme != authSchemeAWSSigV4a && scheme != authSchemeAWSSigV4WS && scheme != authSchemeAWSConnectHealthWS && scheme != authSchemeAWSTranscribeWS && scheme != authSchemeAWSIoTMQTTWS && scheme != authSchemeAWSKinesisVideoSignalingWS && scheme != authSchemeAWSAppSyncEventWS && scheme != authSchemeAWSAppSyncGraphQLWS {
		return InvocationResult{}, fmt.Errorf("AWS auth_scheme must be sigv4, sigv4a, sigv4-ws, connect-health-ws, transcribe-ws, iot-mqtt-ws, kinesisvideo-signaling-ws, appsync-event-ws, or appsync-graphql-ws")
	}
	if scheme == authSchemeAWSSigV4WS || scheme == authSchemeAWSConnectHealthWS || scheme == authSchemeAWSTranscribeWS || scheme == authSchemeAWSIoTMQTTWS || scheme == authSchemeAWSKinesisVideoSignalingWS || scheme == authSchemeAWSAppSyncEventWS || scheme == authSchemeAWSAppSyncGraphQLWS {
		var validationErr error
		switch scheme {
		case authSchemeAWSSigV4WS:
			validationErr = validateAWSSigV4WebSocketInvocation(invocation, adapter.config.AllowedHosts)
		case authSchemeAWSConnectHealthWS:
			validationErr = validateAWSConnectHealthWebSocketInvocation(invocation)
		case authSchemeAWSTranscribeWS:
			validationErr = validateAWSTranscribeWebSocketInvocation(invocation)
		case authSchemeAWSIoTMQTTWS:
			validationErr = validateAWSIoTMQTTWebSocketInvocation(invocation, adapter.config.AllowedHosts)
		case authSchemeAWSKinesisVideoSignalingWS:
			validationErr = validateAWSKinesisVideoSignalingInvocation(invocation)
		case authSchemeAWSAppSyncEventWS:
			validationErr = validateAWSAppSyncEventWebSocketInvocation(invocation, adapter.config.AllowedHosts)
		case authSchemeAWSAppSyncGraphQLWS:
			validationErr = validateAWSAppSyncGraphQLWebSocketInvocation(invocation, adapter.config.AllowedHosts)
		}
		if validationErr != nil {
			return InvocationResult{}, validationErr
		}
		credentials, err := adapter.config.Credentials.Credentials(ctx)
		if err != nil {
			return InvocationResult{}, err
		}
		if credentials.AccessKeyID == "" || credentials.SecretAccessKey == "" {
			return InvocationResult{}, fmt.Errorf("AWS credential provider returned incomplete AKSK material")
		}
		if scheme == authSchemeAWSSigV4WS {
			return invokeAWSSigV4WebSocket(ctx, adapter, credentials, invocation)
		}
		if scheme == authSchemeAWSConnectHealthWS {
			return invokeAWSConnectHealthWebSocket(ctx, adapter, credentials, invocation)
		}
		if scheme == authSchemeAWSTranscribeWS {
			return invokeAWSTranscribeWebSocket(ctx, adapter, credentials, invocation)
		}
		if scheme == authSchemeAWSIoTMQTTWS {
			return invokeAWSIoTMQTTWebSocket(ctx, adapter, credentials, invocation)
		}
		if scheme == authSchemeAWSKinesisVideoSignalingWS {
			return invokeAWSKinesisVideoSignalingWebSocket(ctx, adapter, credentials, invocation)
		}
		if scheme == authSchemeAWSAppSyncEventWS {
			return invokeAWSAppSyncEventWebSocket(ctx, adapter, credentials, invocation)
		}
		return invokeAWSAppSyncGraphQLWebSocket(ctx, adapter, credentials, invocation)
	}
	if !identifierPattern.MatchString(invocation.Service) {
		return InvocationResult{}, fmt.Errorf("AWS signing requires a valid service")
	}
	var regionSet []string
	if scheme == authSchemeAWSSigV4 {
		if !identifierPattern.MatchString(invocation.Region) {
			return InvocationResult{}, fmt.Errorf("AWS SigV4 requires a valid region")
		}
	} else {
		var err error
		regionSet, err = parseAWSRegionSet(invocation.RegionSet)
		if err != nil {
			return InvocationResult{}, err
		}
	}
	request, payloadHash, cleanup, err := buildSignedHTTPRequest(ctx, invocation)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("build AWS request: %w", err)
	}
	defer cleanup()
	if err := validateRESTTargetWithEndpointHosts(ProviderAWS, request.Method, request.URL.String(), adapter.config.AllowedHosts); err != nil {
		return InvocationResult{}, err
	}
	credentials, err := adapter.config.Credentials.Credentials(ctx)
	if err != nil {
		return InvocationResult{}, err
	}
	if credentials.AccessKeyID == "" || credentials.SecretAccessKey == "" {
		return InvocationResult{}, fmt.Errorf("AWS credential provider returned incomplete AKSK material")
	}
	payloadMode := strings.ToLower(strings.TrimSpace(invocation.PayloadMode))
	decodedContentLength := request.ContentLength
	var trailerEncodedLength int64
	var trailerChecksum *awsTrailerChecksum
	if payloadMode == awsPayloadModeChunked {
		if !strings.EqualFold(invocation.Service, "s3") || !strings.EqualFold(invocation.Method, http.MethodPut) {
			return InvocationResult{}, fmt.Errorf("aws-chunked requires service s3 and method PUT")
		}
		if scheme == authSchemeAWSSigV4 {
			err = configureAWSSigV4ChunkedRequest(request, defaultAWSChunkSize)
			payloadHash = awsSigV4StreamingPayload
		} else {
			err = configureAWSSigV4aChunkedRequest(request, defaultAWSChunkSize)
			payloadHash = awsSigV4aStreamingPayload
		}
		if err != nil {
			return InvocationResult{}, err
		}
	} else if payloadMode == awsPayloadModeChunkedTrailer {
		if !strings.EqualFold(invocation.Service, "s3") || !strings.EqualFold(invocation.Method, http.MethodPut) {
			return InvocationResult{}, fmt.Errorf("aws-chunked-trailer requires service s3 and method PUT")
		}
		if scheme == authSchemeAWSSigV4 {
			trailerEncodedLength, trailerChecksum, err = configureAWSSigV4ChunkedTrailerRequest(request, defaultAWSChunkSize, invocation.ChecksumAlgorithm)
			payloadHash = awsSigV4StreamingPayloadTrailer
		} else {
			trailerEncodedLength, trailerChecksum, err = configureAWSSigV4aChunkedTrailerRequest(request, defaultAWSChunkSize, invocation.ChecksumAlgorithm)
			payloadHash = awsSigV4aStreamingPayloadTrailer
		}
		if err != nil {
			return InvocationResult{}, err
		}
	} else if payloadMode == awsPayloadModeEventStream {
		if scheme != authSchemeAWSSigV4 || !strings.EqualFold(invocation.Method, http.MethodPost) {
			return InvocationResult{}, fmt.Errorf("aws-eventstream requires AWS SigV4 and method POST")
		}
		if err := configureAWSEventStreamRequest(request); err != nil {
			return InvocationResult{}, err
		}
		payloadHash = awsSigV4StreamingEventsPayload
	} else if payloadMode != "" {
		return InvocationResult{}, fmt.Errorf("unsupported AWS payload_mode %q", invocation.PayloadMode)
	}
	if strings.EqualFold(invocation.Service, "s3") {
		request.Header.Set("X-Amz-Content-Sha256", payloadHash)
	}
	signingTime := adapter.config.Now().UTC()
	if scheme == authSchemeAWSSigV4 {
		awsCredentials := aws.Credentials{
			AccessKeyID: credentials.AccessKeyID, SecretAccessKey: credentials.SecretAccessKey, SessionToken: credentials.SessionToken,
		}
		if err := awsv4.NewSigner().SignHTTP(ctx, awsCredentials, request, payloadHash, strings.ToLower(invocation.Service), strings.ToLower(invocation.Region), signingTime); err != nil {
			return InvocationResult{}, fmt.Errorf("sign AWS SigV4 request: %w", err)
		}
		if payloadMode == awsPayloadModeChunked {
			seed, err := awsSeedSignature(request.Header.Get("Authorization"))
			if err != nil {
				return InvocationResult{}, err
			}
			request.Body = newAWSSigV4ChunkedReader(request.Body, decodedContentLength, awsCredentials, "s3", strings.ToLower(invocation.Region), signingTime, seed, defaultAWSChunkSize)
			request.GetBody = nil
		} else if payloadMode == awsPayloadModeChunkedTrailer {
			seed, err := awsSeedSignature(request.Header.Get("Authorization"))
			if err != nil {
				return InvocationResult{}, err
			}
			request.ContentLength = trailerEncodedLength
			request.Header.Set("Content-Length", strconv.FormatInt(trailerEncodedLength, 10))
			request.Body = newAWSSigV4ChunkedTrailerReader(request.Body, decodedContentLength, awsCredentials, "s3", strings.ToLower(invocation.Region), signingTime, seed, defaultAWSChunkSize, trailerChecksum)
			request.GetBody = nil
		} else if payloadMode == awsPayloadModeEventStream {
			seed, err := awsSeedSignature(request.Header.Get("Authorization"))
			if err != nil {
				return InvocationResult{}, err
			}
			streamReader := newAWSSigV4EventStreamReader(ctx, request.Body, awsCredentials, strings.ToLower(invocation.Service), strings.ToLower(invocation.Region), adapter.config.Now, seed)
			streamReader.pause = adapter.config.StreamPause
			streamReader.interval = time.Duration(invocation.StreamIntervalMS) * time.Millisecond
			request.Body = streamReader
		}
	} else {
		if _, err := signAWSSigV4a(request, payloadHash, credentials, strings.ToLower(invocation.Service), regionSet, signingTime); err != nil {
			return InvocationResult{}, fmt.Errorf("sign AWS SigV4a request: %w", err)
		}
		if payloadMode == awsPayloadModeChunked || payloadMode == awsPayloadModeChunkedTrailer {
			seed, err := awsSigV4aSeedSignature(request.Header.Get("Authorization"))
			if err != nil {
				return InvocationResult{}, err
			}
			request.Body, err = newAWSSigV4aChunkedReader(request.Body, decodedContentLength, credentials, "s3", signingTime, seed, defaultAWSChunkSize, trailerChecksum)
			if err != nil {
				return InvocationResult{}, err
			}
			request.GetBody = nil
		}
	}
	return invokeSignedHTTP(adapter.config.HTTP, adapter.config.MaxBodyBytes, request, invocation, "AWS API")
}

type AlibabaCredentials struct {
	AccessKeyID     string
	AccessKeySecret string
	SecurityToken   string
}

type AlibabaCredentialProvider interface {
	Credentials(context.Context) (AlibabaCredentials, error)
}

type alibabaDefaultCredentialProvider struct {
	once       sync.Once
	credential alibabacredentials.Credential
	err        error
}

func (provider *alibabaDefaultCredentialProvider) Credentials(context.Context) (AlibabaCredentials, error) {
	provider.once.Do(func() {
		provider.credential, provider.err = alibabacredentials.NewCredential(nil)
	})
	if provider.err != nil {
		return AlibabaCredentials{}, fmt.Errorf("load Alibaba Cloud credential chain: %w", provider.err)
	}
	model, err := provider.credential.GetCredential()
	if err != nil {
		return AlibabaCredentials{}, fmt.Errorf("retrieve Alibaba Cloud credentials: %w", err)
	}
	if model == nil {
		return AlibabaCredentials{}, fmt.Errorf("Alibaba Cloud credential chain returned no credentials")
	}
	return AlibabaCredentials{
		AccessKeyID: pointerString(model.AccessKeyId), AccessKeySecret: pointerString(model.AccessKeySecret), SecurityToken: pointerString(model.SecurityToken),
	}, nil
}

type AlibabaRESTConfig struct {
	Credentials      AlibabaCredentialProvider
	HTTP             HTTPDoer
	MaxBodyBytes     int64
	Timeout          time.Duration
	Now              func() time.Time
	Nonce            func() string
	NLSID            func() string
	NLSWebSocketDial func(context.Context, string) (cloudWebSocketConnection, error)
	StreamPause      func(context.Context, time.Duration) error
	AllowedHosts     []string
}

type AlibabaRESTAdapter struct {
	config         AlibabaRESTConfig
	nlsTokenMu     sync.Mutex
	nlsToken       string
	nlsTokenExpiry time.Time
}

func NewAlibabaRESTAdapter(config AlibabaRESTConfig) *AlibabaRESTAdapter {
	if config.Credentials == nil {
		config.Credentials = &alibabaDefaultCredentialProvider{}
	}
	normalizeSignedHTTPConfig(&config.HTTP, &config.Timeout, &config.MaxBodyBytes, &config.Now)
	if config.Nonce == nil {
		config.Nonce = secureNonce
	}
	if config.NLSID == nil {
		config.NLSID = secureNonce
	}
	if config.NLSWebSocketDial == nil {
		config.NLSWebSocketDial = defaultAlibabaNLSWebSocketDial
	}
	if config.StreamPause == nil {
		config.StreamPause = pauseTencentStream
	}
	return &AlibabaRESTAdapter{config: config}
}

func (adapter *AlibabaRESTAdapter) Status(context.Context) (ProviderStatus, error) {
	return ProviderStatus{
		Provider: ProviderAlicloud, Available: true, Adapter: "Alibaba Cloud signed HTTPS and NLS HTTPS/WSS", Version: "acs3+rpc+roa+datahub+opensearch+odps+odps4+fc+fc3+fc-custom+oss+oss4+sls+sls4+mns+ots+ots4+nls-rest+nls-ws",
		CredentialSource: credentialSource(ProviderAlicloud), CredentialStatus: CredentialStatusUnverified,
		Message: "credentials are resolved lazily through the Alibaba Cloud credential chain; no cloud CLI is executed",
	}, nil
}

func (adapter *AlibabaRESTAdapter) Discover(context.Context, DiscoveryRequest) ([]byte, error) {
	return json.Marshal(map[string]string{
		"api_reference":  "https://api.aliyun.com/",
		"acs3_signature": "https://help.aliyun.com/zh/sdk/product-overview/v3-request-structure-and-signature",
		"rpc_signature":  "https://www.alibabacloud.com/help/en/sdk/product-overview/rpc-mechanism",
		"roa_signature":  "https://www.alibabacloud.com/help/en/sdk/product-overview/roa-mechanism",
		"datahub_api":    "https://www.alibabacloud.com/help/en/datahub/developer-reference/nerbcz",
		"opensearch_api": "https://www.alibabacloud.com/help/en/open-search/high-performance-searchedition/signature-method-of-opensearch-api-v3",
		"odps_signature": "https://github.com/aliyun/aliyun-odps-python-sdk/blob/558462b8d61b43c73016837f32c68b2ddfad2cdf/odps/accounts.py",
		"odps_endpoints": "https://www.alibabacloud.com/help/en/maxcompute/user-guide/endpoints",
		"fc_signature":   "https://www.alibabacloud.com/help/en/functioncompute/signature-authentication",
		"fc_endpoints":   "https://www.alibabacloud.com/help/en/functioncompute/endpoints",
		"fc3_trigger":    "https://www.alibabacloud.com/help/en/functioncompute/fc/configure-signature-authentication-for-http-triggers",
		"fc_custom":      "https://www.alibabacloud.com/help/en/functioncompute/configure-signature-authentication-for-custom-domain-names",
		"oss_signature":  "https://www.alibabacloud.com/help/en/oss/developer-reference/include-signatures-in-the-authorization-header",
		"oss4_signature": "https://help.aliyun.com/en/oss/developer-reference/recommend-to-use-signature-version-4",
		"sls_signature":  "https://www.alibabacloud.com/help/en/sls/developer-reference/request-signatures",
		"mns_signature":  "https://www.alibabacloud.com/help/en/mns/developer-reference/request-protocol-description",
		"ots_signature":  "https://github.com/aliyun/aliyun-tablestore-go-sdk/blob/master/tablestore/ots_header.go",
		"nls_websocket":  "https://www.alibabacloud.com/help/en/isi/developer-reference/websocket",
		"nls_token":      "https://www.alibabacloud.com/help/en/isi/getting-started/obtain-an-access-token",
	})
}

func (adapter *AlibabaRESTAdapter) Invoke(ctx context.Context, invocation Invocation) (InvocationResult, error) {
	scheme := normalizedAuthScheme(invocation.AuthScheme, authSchemeAlibabaACS3)
	if scheme != authSchemeAlibabaACS3 && scheme != authSchemeAlibabaRPCV2 && scheme != authSchemeAlibabaROAV2 && scheme != authSchemeAlibabaDataHub && scheme != authSchemeAlibabaOpenSearch && scheme != authSchemeAlibabaODPS && scheme != authSchemeAlibabaODPSV4 && scheme != authSchemeAlibabaFC && scheme != authSchemeAlibabaFC3 && scheme != authSchemeAlibabaFCCustom && scheme != authSchemeAlibabaOSS && scheme != authSchemeAlibabaOSSV4 && scheme != authSchemeAlibabaSLS && scheme != authSchemeAlibabaSLSV4 && scheme != authSchemeAlibabaMNS && scheme != authSchemeAlibabaOTS && scheme != authSchemeAlibabaOTSV4 && scheme != authSchemeAlibabaNLSWS && scheme != authSchemeAlibabaNLSREST {
		return InvocationResult{}, fmt.Errorf("Alibaba Cloud auth_scheme must be acs3, rpc, roa, datahub, opensearch, odps, odps4, fc, fc3, fc-custom, oss, oss4, sls, sls4, mns, ots, ots4, nls-rest, or nls-ws")
	}
	if scheme == authSchemeAlibabaNLSWS {
		return invokeAlibabaNLSWebSocket(ctx, adapter, invocation)
	}
	if scheme == authSchemeAlibabaNLSREST {
		return invokeAlibabaNLSREST(ctx, adapter, invocation)
	}
	if scheme == authSchemeAlibabaMNS && (invocation.Body != nil || invocation.BodyFile != "") && !hasHeader(invocation.Headers, "content-type") {
		invocation.Headers = cloneStringMap(invocation.Headers)
		invocation.Headers["Content-Type"] = "application/xml"
	}
	request, payloadHash, cleanup, err := buildSignedHTTPRequest(ctx, invocation)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("build Alibaba Cloud request: %w", err)
	}
	defer cleanup()
	if err := validateRESTTargetWithEndpointHosts(ProviderAlicloud, request.Method, request.URL.String(), adapter.config.AllowedHosts); err != nil {
		return InvocationResult{}, err
	}
	if scheme == authSchemeAlibabaRPCV2 {
		if err := validateAlibabaRPCV2Request(request); err != nil {
			return InvocationResult{}, err
		}
	}
	credentials, err := adapter.config.Credentials.Credentials(ctx)
	if err != nil {
		return InvocationResult{}, err
	}
	if credentials.AccessKeyID == "" || credentials.AccessKeySecret == "" {
		return InvocationResult{}, fmt.Errorf("Alibaba Cloud credential provider returned incomplete AKSK material")
	}
	switch scheme {
	case authSchemeAlibabaACS3:
		if err := signAlibabaACS3(request, payloadHash, credentials, invocation, adapter.config.Now().UTC(), adapter.config.Nonce()); err != nil {
			return InvocationResult{}, err
		}
	case authSchemeAlibabaRPCV2:
		if err := signAlibabaRPCV2(request, credentials, invocation, adapter.config.Now().UTC(), adapter.config.Nonce()); err != nil {
			return InvocationResult{}, err
		}
	case authSchemeAlibabaROAV2:
		if err := signAlibabaROAV2(request, credentials, invocation.APIVersion, adapter.config.Now().UTC(), adapter.config.Nonce()); err != nil {
			return InvocationResult{}, err
		}
	case authSchemeAlibabaDataHub:
		if err := signAlibabaDataHub(request, credentials, invocation.APIVersion, adapter.config.Now().UTC()); err != nil {
			return InvocationResult{}, err
		}
	case authSchemeAlibabaOpenSearch:
		now := adapter.config.Now().UTC()
		if err := signAlibabaOpenSearch(request, credentials, now, alibabaOpenSearchNonce(now, adapter.config.Nonce())); err != nil {
			return InvocationResult{}, err
		}
	case authSchemeAlibabaODPS:
		if err := signAlibabaODPSV2(request, credentials, adapter.config.Now().UTC()); err != nil {
			return InvocationResult{}, err
		}
	case authSchemeAlibabaODPSV4:
		if err := signAlibabaODPSV4(request, credentials, invocation.Region, adapter.config.Now().UTC()); err != nil {
			return InvocationResult{}, err
		}
	case authSchemeAlibabaFC:
		if err := signAlibabaFC(request, credentials, adapter.config.Now().UTC()); err != nil {
			return InvocationResult{}, err
		}
	case authSchemeAlibabaFC3:
		if err := signAlibabaFC3(request, credentials, adapter.config.Now().UTC()); err != nil {
			return InvocationResult{}, err
		}
	case authSchemeAlibabaFCCustom:
		if err := signAlibabaFCCustomDomain(request, credentials, adapter.config.Now().UTC()); err != nil {
			return InvocationResult{}, err
		}
	case authSchemeAlibabaOSS:
		if err := signAlibabaOSSV1(request, credentials, adapter.config.Now().UTC()); err != nil {
			return InvocationResult{}, err
		}
	case authSchemeAlibabaOSSV4:
		if err := signAlibabaOSSV4(request, credentials, invocation.Region, adapter.config.Now().UTC()); err != nil {
			return InvocationResult{}, err
		}
	case authSchemeAlibabaSLS:
		if err := signAlibabaSLSV1(request, credentials, invocation.APIVersion, adapter.config.Now().UTC()); err != nil {
			return InvocationResult{}, err
		}
	case authSchemeAlibabaSLSV4:
		if err := signAlibabaSLSV4(request, payloadHash, credentials, invocation.Region, invocation.APIVersion, adapter.config.Now().UTC()); err != nil {
			return InvocationResult{}, err
		}
	case authSchemeAlibabaMNS:
		if err := signAlibabaMNS(request, credentials, invocation.APIVersion, adapter.config.Now().UTC()); err != nil {
			return InvocationResult{}, err
		}
	case authSchemeAlibabaOTS:
		if err := signAlibabaOTSV2(request, credentials, invocation.APIVersion, adapter.config.Now().UTC()); err != nil {
			return InvocationResult{}, err
		}
	case authSchemeAlibabaOTSV4:
		if err := signAlibabaOTSV4(request, credentials, invocation.Region, invocation.APIVersion, adapter.config.Now().UTC()); err != nil {
			return InvocationResult{}, err
		}
	}
	return invokeSignedHTTP(adapter.config.HTTP, adapter.config.MaxBodyBytes, request, invocation, "Alibaba Cloud API")
}

type TencentCredentials struct {
	SecretID  string
	SecretKey string
	Token     string
}

type TencentCredentialProvider interface {
	Credentials(context.Context) (TencentCredentials, error)
}

type EnvTencentCredentialProvider struct{}

func (EnvTencentCredentialProvider) Credentials(context.Context) (TencentCredentials, error) {
	credentials := TencentCredentials{
		SecretID: strings.TrimSpace(os.Getenv("TENCENTCLOUD_SECRET_ID")), SecretKey: strings.TrimSpace(os.Getenv("TENCENTCLOUD_SECRET_KEY")),
		Token: strings.TrimSpace(os.Getenv("TENCENTCLOUD_SESSION_TOKEN")),
	}
	if credentials.Token == "" {
		credentials.Token = strings.TrimSpace(os.Getenv("TENCENTCLOUD_TOKEN"))
	}
	if credentials.SecretID == "" || credentials.SecretKey == "" {
		return TencentCredentials{}, fmt.Errorf("Tencent Cloud credential unavailable: set TENCENTCLOUD_SECRET_ID and TENCENTCLOUD_SECRET_KEY, plus TENCENTCLOUD_SESSION_TOKEN for CAM temporary credentials")
	}
	return credentials, nil
}

type TencentRESTConfig struct {
	Credentials   TencentCredentialProvider
	HTTP          HTTPDoer
	MaxBodyBytes  int64
	Timeout       time.Duration
	Now           func() time.Time
	Nonce         func() string
	MPSNonce      func() string
	VoiceID       func() string
	WebSocketDial func(context.Context, string) (tencentWebSocketConnection, error)
	StreamPause   func(context.Context, time.Duration) error
	AllowedHosts  []string
}

type TencentRESTAdapter struct {
	config TencentRESTConfig
}

func NewTencentRESTAdapter(config TencentRESTConfig) *TencentRESTAdapter {
	if config.Credentials == nil {
		config.Credentials = EnvTencentCredentialProvider{}
	}
	normalizeSignedHTTPConfig(&config.HTTP, &config.Timeout, &config.MaxBodyBytes, &config.Now)
	if config.Nonce == nil {
		config.Nonce = secureTencentNonce
	}
	if config.MPSNonce == nil {
		config.MPSNonce = secureTencentMPSNonce
	}
	if config.VoiceID == nil {
		config.VoiceID = secureTencentVoiceID
	}
	if config.WebSocketDial == nil {
		config.WebSocketDial = defaultTencentWebSocketDial
	}
	if config.StreamPause == nil {
		config.StreamPause = pauseTencentStream
	}
	return &TencentRESTAdapter{config: config}
}

func (adapter *TencentRESTAdapter) Status(context.Context) (ProviderStatus, error) {
	return ProviderStatus{
		Provider: ProviderTencent, Available: true, Adapter: "Tencent Cloud signed HTTPS/WSS", Version: "tc3+tc1+tc1-sha256+qcloud+qcloud-sha256+asr-ws+virtual-number-ws+soe-ws+speech-translate-ws+voice-convert-ws+mps-ws+mps-tts-ws+tts-ws+tts-stream-ws+podcast-ws+cos",
		CredentialSource: credentialSource(ProviderTencent), CredentialStatus: CredentialStatusUnverified,
		Message: "AKSK or CAM temporary credentials are resolved lazily from the server environment; no cloud CLI is executed",
	}, nil
}

func (adapter *TencentRESTAdapter) Discover(context.Context, DiscoveryRequest) ([]byte, error) {
	return json.Marshal(map[string]string{
		"api_reference":              "https://cloud.tencent.com/document/api",
		"tc3_signature":              "https://intl.cloud.tencent.com/document/product/627/64494",
		"tc1_signature":              "https://cloud.tencent.com/document/api/583/17239",
		"qcloud_signature":           "https://cloud.tencent.com/document/product/216/1714",
		"asr_websocket":              "https://cloud.tencent.com/document/product/1093/48982",
		"virtual_number_websocket":   "https://cloud.tencent.com/document/product/1093/94490",
		"soe_websocket":              "https://cloud.tencent.com/document/product/1774/107497",
		"speech_translate_websocket": "https://cloud.tencent.com/document/api/1093/127565",
		"voice_conversion_websocket": "https://cloud.tencent.com/document/product/1664/85973",
		"mps_websocket":              "https://cloud.tencent.com/document/product/862/121186",
		"mps_tts_websocket":          "https://cloud.tencent.com/document/product/862/133241",
		"tts_websocket":              "https://cloud.tencent.com/document/product/1073/94308",
		"streaming_tts_websocket":    "https://cloud.tencent.com/document/product/1073/108595",
		"podcast_websocket":          "https://cloud.tencent.com/document/api/1073/124700",
		"cos_signature":              "https://intl.cloud.tencent.com/document/product/436/7778",
	})
}

func (adapter *TencentRESTAdapter) Invoke(ctx context.Context, invocation Invocation) (InvocationResult, error) {
	scheme := normalizedAuthScheme(invocation.AuthScheme, authSchemeTencentTC3)
	if scheme != authSchemeTencentTC3 && scheme != authSchemeTencentV1 && scheme != authSchemeTencentV1SHA256 && scheme != authSchemeTencentQCloud && scheme != authSchemeTencentQCloud256 && scheme != authSchemeTencentASRWS && scheme != authSchemeTencentVirtualWS && scheme != authSchemeTencentSOEWS && scheme != authSchemeTencentTranslateWS && scheme != authSchemeTencentVoiceWS && scheme != authSchemeTencentMPSWS && scheme != authSchemeTencentMPSTTSWS && scheme != authSchemeTencentTTSWS && scheme != authSchemeTencentTTSStreamWS && scheme != authSchemeTencentPodcastWS && scheme != authSchemeTencentCOS {
		return InvocationResult{}, fmt.Errorf("Tencent Cloud auth_scheme must be tc3, tc1, tc1-sha256, qcloud, qcloud-sha256, asr-ws, virtual-number-ws, soe-ws, speech-translate-ws, voice-convert-ws, mps-ws, mps-tts-ws, tts-ws, tts-stream-ws, podcast-ws, or cos")
	}
	if scheme == authSchemeTencentASRWS || scheme == authSchemeTencentVirtualWS || scheme == authSchemeTencentSOEWS || scheme == authSchemeTencentTranslateWS || scheme == authSchemeTencentVoiceWS || scheme == authSchemeTencentMPSWS || scheme == authSchemeTencentMPSTTSWS || scheme == authSchemeTencentTTSWS || scheme == authSchemeTencentTTSStreamWS || scheme == authSchemeTencentPodcastWS {
		if scheme == authSchemeTencentASRWS {
			if err := validateTencentASRWebSocketInvocation(invocation); err != nil {
				return InvocationResult{}, err
			}
		} else if scheme == authSchemeTencentVirtualWS {
			if err := validateTencentVirtualNumberWebSocketInvocation(invocation); err != nil {
				return InvocationResult{}, err
			}
		} else if scheme == authSchemeTencentSOEWS {
			if err := validateTencentSOEWebSocketInvocation(invocation); err != nil {
				return InvocationResult{}, err
			}
		} else if scheme == authSchemeTencentTranslateWS {
			if err := validateTencentSpeechTranslateWebSocketInvocation(invocation); err != nil {
				return InvocationResult{}, err
			}
		} else if scheme == authSchemeTencentVoiceWS {
			if err := validateTencentVoiceConversionWebSocketInvocation(invocation); err != nil {
				return InvocationResult{}, err
			}
		} else if scheme == authSchemeTencentMPSWS {
			if err := validateTencentMPSWebSocketInvocation(invocation); err != nil {
				return InvocationResult{}, err
			}
		} else if scheme == authSchemeTencentMPSTTSWS {
			if err := validateTencentMPSTTSWebSocketInvocation(invocation); err != nil {
				return InvocationResult{}, err
			}
		} else if scheme == authSchemeTencentTTSWS {
			if err := validateTencentTTSWebSocketInvocation(invocation); err != nil {
				return InvocationResult{}, err
			}
		} else if scheme == authSchemeTencentTTSStreamWS {
			if err := validateTencentStreamingTTSWebSocketInvocation(invocation); err != nil {
				return InvocationResult{}, err
			}
		} else {
			if err := validateTencentPodcastWebSocketInvocation(invocation); err != nil {
				return InvocationResult{}, err
			}
		}
		credentials, err := adapter.config.Credentials.Credentials(ctx)
		if err != nil {
			return InvocationResult{}, err
		}
		if credentials.SecretID == "" || credentials.SecretKey == "" {
			return InvocationResult{}, fmt.Errorf("Tencent Cloud credential provider returned incomplete AKSK material")
		}
		if scheme == authSchemeTencentASRWS {
			return invokeTencentASRWebSocket(ctx, adapter, credentials, invocation)
		}
		if scheme == authSchemeTencentVirtualWS {
			return invokeTencentVirtualNumberWebSocket(ctx, adapter, credentials, invocation)
		}
		if scheme == authSchemeTencentSOEWS {
			return invokeTencentSOEWebSocket(ctx, adapter, credentials, invocation)
		}
		if scheme == authSchemeTencentTranslateWS {
			return invokeTencentSpeechTranslateWebSocket(ctx, adapter, credentials, invocation)
		}
		if scheme == authSchemeTencentVoiceWS {
			return invokeTencentVoiceConversionWebSocket(ctx, adapter, credentials, invocation)
		}
		if scheme == authSchemeTencentMPSWS {
			return invokeTencentMPSWebSocket(ctx, adapter, credentials, invocation)
		}
		if scheme == authSchemeTencentMPSTTSWS {
			return invokeTencentMPSTTSWebSocket(ctx, adapter, credentials, invocation)
		}
		if scheme == authSchemeTencentTTSWS {
			return invokeTencentTTSWebSocket(ctx, adapter, credentials, invocation)
		}
		if scheme == authSchemeTencentTTSStreamWS {
			return invokeTencentStreamingTTSWebSocket(ctx, adapter, credentials, invocation)
		}
		return invokeTencentPodcastWebSocket(ctx, adapter, credentials, invocation)
	}
	request, payloadHash, cleanup, err := buildSignedHTTPRequest(ctx, invocation)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("build Tencent Cloud request: %w", err)
	}
	defer cleanup()
	if err := validateRESTTargetWithEndpointHosts(ProviderTencent, request.Method, request.URL.String(), adapter.config.AllowedHosts); err != nil {
		return InvocationResult{}, err
	}
	credentials, err := adapter.config.Credentials.Credentials(ctx)
	if err != nil {
		return InvocationResult{}, err
	}
	if credentials.SecretID == "" || credentials.SecretKey == "" {
		return InvocationResult{}, fmt.Errorf("Tencent Cloud credential provider returned incomplete AKSK material")
	}
	switch scheme {
	case authSchemeTencentTC3:
		if err := signTencentTC3(request, payloadHash, credentials, invocation, adapter.config.Now().UTC()); err != nil {
			return InvocationResult{}, err
		}
	case authSchemeTencentV1, authSchemeTencentV1SHA256:
		if err := signTencentV1(request, credentials, invocation, adapter.config.Now().UTC(), adapter.config.Nonce(), scheme == authSchemeTencentV1SHA256); err != nil {
			return InvocationResult{}, err
		}
	case authSchemeTencentQCloud, authSchemeTencentQCloud256:
		if err := signTencentQCloud(request, credentials, invocation, adapter.config.Now().UTC(), adapter.config.Nonce(), scheme == authSchemeTencentQCloud256); err != nil {
			return InvocationResult{}, err
		}
	case authSchemeTencentCOS:
		if err := signTencentCOS(request, credentials, adapter.config.Now().UTC()); err != nil {
			return InvocationResult{}, err
		}
	}
	return invokeSignedHTTP(adapter.config.HTTP, adapter.config.MaxBodyBytes, request, invocation, "Tencent Cloud API")
}

func normalizeSignedHTTPConfig(client *HTTPDoer, timeout *time.Duration, maxBodyBytes *int64, now *func() time.Time) {
	if *timeout <= 0 {
		*timeout = defaultHTTPClientTimeout
	}
	if *client == nil {
		*client = &http.Client{
			Timeout: *timeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	if *maxBodyBytes <= 0 {
		*maxBodyBytes = defaultRESTBodyLimit
	}
	if *now == nil {
		*now = time.Now
	}
}

func buildSignedHTTPRequest(ctx context.Context, invocation Invocation) (*http.Request, string, func(), error) {
	body, contentLength, cleanup, err := prepareRESTBody(invocation)
	if err != nil {
		return nil, "", func() {}, err
	}
	method := strings.ToUpper(strings.TrimSpace(invocation.Method))
	request, err := http.NewRequestWithContext(ctx, method, invocation.URL, body)
	if err != nil {
		cleanup()
		return nil, "", func() {}, err
	}
	for name, value := range invocation.Headers {
		request.Header.Set(name, value)
	}
	if contentLength >= 0 {
		request.ContentLength = contentLength
	}
	if request.GetBody == nil && invocation.BodyFile != "" {
		path := invocation.BodyFile
		request.GetBody = func() (io.ReadCloser, error) { return os.Open(path) }
	}
	if (invocation.Body != nil || invocation.BodyFile != "") && request.Header.Get("Content-Type") == "" {
		request.Header.Set("Content-Type", invocationContentType(invocation))
	}
	if err := addQueryParameters(request.URL, invocation.Parameters); err != nil {
		cleanup()
		return nil, "", func() {}, err
	}
	payloadHash, err := hashRequestBody(request)
	if err != nil {
		cleanup()
		return nil, "", func() {}, err
	}
	return request, payloadHash, cleanup, nil
}

func hashRequestBody(request *http.Request) (string, error) {
	hash := sha256.New()
	if request.Body != nil {
		if _, err := io.Copy(hash, request.Body); err != nil {
			return "", fmt.Errorf("hash request body: %w", err)
		}
		if request.GetBody == nil {
			return "", fmt.Errorf("signed HTTP body is not replayable")
		}
		body, err := request.GetBody()
		if err != nil {
			return "", fmt.Errorf("rewind request body: %w", err)
		}
		request.Body = body
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func addQueryParameters(target *url.URL, parameters map[string]any) error {
	if len(parameters) == 0 {
		return nil
	}
	query := target.Query()
	for name, value := range parameters {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("query parameter name is empty")
		}
		values, err := stringValues(value)
		if err != nil {
			return fmt.Errorf("query parameter %q: %w", name, err)
		}
		for _, item := range values {
			query.Add(name, item)
		}
	}
	target.RawQuery = query.Encode()
	return nil
}

func stringValues(value any) ([]string, error) {
	switch typed := value.(type) {
	case string:
		return []string{typed}, nil
	case bool:
		return []string{strconv.FormatBool(typed)}, nil
	case float64:
		return []string{strconv.FormatFloat(typed, 'f', -1, 64)}, nil
	case int:
		return []string{strconv.Itoa(typed)}, nil
	case []string:
		return typed, nil
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			switch item.(type) {
			case []any, []string:
				return nil, fmt.Errorf("array values must be scalar")
			}
			items, err := stringValues(item)
			if err != nil || len(items) != 1 {
				return nil, fmt.Errorf("array values must be scalar")
			}
			values = append(values, items[0])
		}
		return values, nil
	case nil:
		return []string{""}, nil
	default:
		return nil, fmt.Errorf("must be a string, number, boolean, null, or scalar array")
	}
}

func invokeSignedHTTP(client HTTPDoer, maxBodyBytes int64, request *http.Request, invocation Invocation, providerName string) (InvocationResult, error) {
	response, err := client.Do(request)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("%s HTTP request: %w", providerName, err)
	}
	if response != nil && response.Body != nil && response.StatusCode >= 200 && response.StatusCode < 300 && invocation.Provider == ProviderAWS && strings.EqualFold(strings.TrimSpace(invocation.PayloadMode), awsPayloadModeEventStream) && (invocation.StreamIntervalMS != 0 || strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "application/vnd.amazon.eventstream")) {
		response.Body = newAWSValidatingEventStreamReader(response.Body)
		response.ContentLength = -1
	}
	output, err := readRESTResponseWithFile(response, maxBodyBytes, invocation.ResponseFile, invocation.MaxResponseFileBytes)
	if err != nil {
		return InvocationResult{}, err
	}
	requestID := responseRequestID(response.Header)
	if requestID == "" {
		requestID = requestIDFromGenericJSON(output)
	}
	return InvocationResult{Output: output, RequestID: requestID}, nil
}

func signAlibabaACS3(request *http.Request, payloadHash string, credentials AlibabaCredentials, invocation Invocation, now time.Time, nonce string) error {
	if !identifierPattern.MatchString(invocation.Operation) || !identifierPattern.MatchString(invocation.APIVersion) {
		return fmt.Errorf("Alibaba Cloud ACS3 requires valid operation and api_version")
	}
	request.Header.Set("X-Acs-Action", invocation.Operation)
	request.Header.Set("X-Acs-Version", invocation.APIVersion)
	request.Header.Set("X-Acs-Date", now.UTC().Format("2006-01-02T15:04:05Z"))
	request.Header.Set("X-Acs-Signature-Nonce", nonce)
	request.Header.Set("X-Acs-Content-Sha256", payloadHash)
	if credentials.SecurityToken != "" {
		request.Header.Set("X-Acs-Security-Token", credentials.SecurityToken)
	}
	canonicalHeaders, signedHeaders := canonicalHeaders(request, func(name string) bool {
		return name == "host" || strings.HasPrefix(name, "x-acs-")
	})
	canonicalRequest := strings.Join([]string{
		request.Method, canonicalURI(request.URL), canonicalQuery(request.URL.Query()), canonicalHeaders, signedHeaders, payloadHash,
	}, "\n")
	hashedCanonical := sha256Hex([]byte(canonicalRequest))
	signature := hmacHex(sha256.New, []byte(credentials.AccessKeySecret), []byte("ACS3-HMAC-SHA256\n"+hashedCanonical))
	request.Header.Set("Authorization", "ACS3-HMAC-SHA256 Credential="+credentials.AccessKeyID+",SignedHeaders="+signedHeaders+",Signature="+signature)
	return nil
}

func signAlibabaRPCV2(request *http.Request, credentials AlibabaCredentials, invocation Invocation, now time.Time, nonce string) error {
	if !identifierPattern.MatchString(invocation.Operation) || !apiVersionPattern.MatchString(invocation.APIVersion) {
		return fmt.Errorf("Alibaba Cloud RPC V2 requires valid operation and api_version")
	}
	if credentials.AccessKeyID == "" || credentials.AccessKeySecret == "" {
		return fmt.Errorf("Alibaba Cloud RPC V2 requires complete AKSK material")
	}
	parameters, err := alibabaRPCV2SigningParameters(request)
	if err != nil {
		return err
	}
	for _, name := range []string{"AccessKeyId", "Action", "SignatureMethod", "SignatureNonce", "SignatureVersion", "Timestamp", "Version"} {
		if hasCaseInsensitiveMapKey(parameters, name) {
			return fmt.Errorf("caller-supplied Alibaba Cloud RPC V2 signing parameter %q is forbidden", name)
		}
	}
	if hasCaseInsensitiveMapKey(parameters, "Signature") || hasCaseInsensitiveMapKey(parameters, "SecurityToken") {
		return fmt.Errorf("caller-supplied Alibaba Cloud RPC V2 credential or signature parameter is forbidden")
	}
	parameters["AccessKeyId"] = credentials.AccessKeyID
	parameters["Action"] = invocation.Operation
	parameters["SignatureMethod"] = "HMAC-SHA1"
	parameters["SignatureNonce"] = nonce
	parameters["SignatureVersion"] = "1.0"
	parameters["Timestamp"] = now.UTC().Format("2006-01-02T15:04:05Z")
	parameters["Version"] = invocation.APIVersion
	if credentials.SecurityToken != "" {
		parameters["SecurityToken"] = credentials.SecurityToken
	}
	canonical := canonicalAlibabaRPCV2Parameters(parameters)
	stringToSign := request.Method + "&%2F&" + uriEncode(canonical, true)
	signature := base64.StdEncoding.EncodeToString(hmacBytes(sha1.New, []byte(credentials.AccessKeySecret+"&"), []byte(stringToSign)))

	queryParameters := request.URL.Query()
	queryParameters.Set("AccessKeyId", credentials.AccessKeyID)
	queryParameters.Set("Action", invocation.Operation)
	queryParameters.Set("SignatureMethod", "HMAC-SHA1")
	queryParameters.Set("SignatureNonce", nonce)
	queryParameters.Set("SignatureVersion", "1.0")
	queryParameters.Set("Timestamp", now.UTC().Format("2006-01-02T15:04:05Z"))
	queryParameters.Set("Version", invocation.APIVersion)
	if credentials.SecurityToken != "" {
		queryParameters.Set("SecurityToken", credentials.SecurityToken)
	}
	queryParameters.Set("Signature", signature)
	request.URL.RawQuery = canonicalAlibabaRPCV2Values(queryParameters)
	return nil
}

func validateAlibabaRPCV2Request(request *http.Request) error {
	if request == nil || request.URL == nil {
		return fmt.Errorf("Alibaba Cloud RPC V2 request is missing")
	}
	if request.Method != http.MethodGet && request.Method != http.MethodPost {
		return fmt.Errorf("Alibaba Cloud RPC V2 requires method GET or POST")
	}
	if path := request.URL.EscapedPath(); path != "" && path != "/" {
		return fmt.Errorf("Alibaba Cloud RPC V2 requires the root request path")
	}
	parameters, err := alibabaRPCV2SigningParameters(request)
	if err != nil {
		return err
	}
	for name := range parameters {
		if isAlibabaRPCV2ControlledParameter(name) {
			return fmt.Errorf("caller-supplied Alibaba Cloud RPC V2 signing parameter %q is forbidden", name)
		}
	}
	return nil
}

func alibabaRPCV2SigningParameters(request *http.Request) (map[string]string, error) {
	parameters := make(map[string]string)
	for name, values := range request.URL.Query() {
		if len(values) != 1 {
			return nil, fmt.Errorf("Alibaba Cloud RPC V2 parameter %q must have exactly one value", name)
		}
		parameters[name] = values[0]
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]))
	if contentType != "application/x-www-form-urlencoded" || request.Body == nil {
		return parameters, nil
	}
	if request.GetBody == nil {
		return nil, fmt.Errorf("Alibaba Cloud RPC V2 form body is not replayable")
	}
	body, err := request.GetBody()
	if err != nil {
		return nil, fmt.Errorf("read Alibaba Cloud RPC V2 form body: %w", err)
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, maxRequestPayloadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read Alibaba Cloud RPC V2 form body: %w", err)
	}
	if len(data) > maxRequestPayloadBytes {
		return nil, fmt.Errorf("Alibaba Cloud RPC V2 form body exceeds %d bytes", maxRequestPayloadBytes)
	}
	form, err := url.ParseQuery(string(data))
	if err != nil {
		return nil, fmt.Errorf("parse Alibaba Cloud RPC V2 form body: %w", err)
	}
	for name, values := range form {
		if len(values) != 1 {
			return nil, fmt.Errorf("Alibaba Cloud RPC V2 form parameter %q must have exactly one value", name)
		}
		if _, present := parameters[name]; present {
			return nil, fmt.Errorf("Alibaba Cloud RPC V2 parameter %q appears in both query and form body", name)
		}
		parameters[name] = values[0]
	}
	return parameters, nil
}

func canonicalAlibabaRPCV2Parameters(parameters map[string]string) string {
	names := make([]string, 0, len(parameters))
	for name := range parameters {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, uriEncode(name, true)+"="+uriEncode(parameters[name], true))
	}
	return strings.Join(parts, "&")
}

func canonicalAlibabaRPCV2Values(values url.Values) string {
	parameters := make(map[string]string, len(values))
	for name, entries := range values {
		if len(entries) > 0 {
			parameters[name] = entries[0]
		}
	}
	return canonicalAlibabaRPCV2Parameters(parameters)
}

func hasCaseInsensitiveMapKey(values map[string]string, name string) bool {
	for candidate := range values {
		if strings.EqualFold(candidate, name) {
			return true
		}
	}
	return false
}

func signAlibabaROAV2(request *http.Request, credentials AlibabaCredentials, apiVersion string, now time.Time, nonce string) error {
	if !apiVersionPattern.MatchString(apiVersion) {
		return fmt.Errorf("Alibaba Cloud ROA V2 requires a valid api_version")
	}
	if credentials.AccessKeyID == "" || credentials.AccessKeySecret == "" {
		return fmt.Errorf("Alibaba Cloud ROA V2 requires complete AKSK material")
	}
	for name, values := range request.URL.Query() {
		if len(values) != 1 {
			return fmt.Errorf("Alibaba Cloud ROA V2 query parameter %q must have exactly one value", name)
		}
	}
	if request.Header.Get("Accept") == "" {
		request.Header.Set("Accept", "application/json")
	}
	request.Header.Set("Date", now.UTC().Format(http.TimeFormat))
	request.Header.Set("X-Acs-Signature-Method", "HMAC-SHA1")
	request.Header.Set("X-Acs-Signature-Nonce", nonce)
	request.Header.Set("X-Acs-Signature-Version", "1.0")
	request.Header.Set("X-Acs-Version", apiVersion)
	if credentials.SecurityToken != "" {
		request.Header.Set("X-Acs-Security-Token", credentials.SecurityToken)
	}
	if request.Body != nil && request.Body != http.NoBody {
		bodyMD5, err := rawRequestBodyMD5(request)
		if err != nil {
			return fmt.Errorf("hash Alibaba Cloud ROA V2 request body: %w", err)
		}
		request.Header.Set("Content-MD5", bodyMD5)
	} else {
		request.Header.Del("Content-MD5")
	}
	canonicalHeaders := canonicalPrefixedHeaders(request.Header, "x-acs-")
	stringToSign := strings.Join([]string{
		request.Method,
		request.Header.Get("Accept"),
		request.Header.Get("Content-MD5"),
		request.Header.Get("Content-Type"),
		request.Header.Get("Date"),
	}, "\n") + "\n" + canonicalHeaders + "\n" + canonicalAlibabaROAResource(request.URL)
	signature := base64.StdEncoding.EncodeToString(hmacBytes(sha1.New, []byte(credentials.AccessKeySecret), []byte(stringToSign)))
	request.Header.Set("Authorization", "acs "+credentials.AccessKeyID+":"+signature)
	return nil
}

func canonicalAlibabaROAResource(target *url.URL) string {
	resource := canonicalURI(target)
	query := target.Query()
	if len(query) == 0 {
		return resource
	}
	names := make([]string, 0, len(query))
	for name := range query {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		value := ""
		if len(query[name]) > 0 {
			value = query[name][0]
		}
		if value == "" {
			parts = append(parts, name)
		} else {
			parts = append(parts, name+"="+value)
		}
	}
	return resource + "?" + strings.Join(parts, "&")
}

func signAlibabaDataHub(request *http.Request, credentials AlibabaCredentials, apiVersion string, now time.Time) error {
	if credentials.AccessKeyID == "" || credentials.AccessKeySecret == "" {
		return fmt.Errorf("Alibaba Cloud DataHub requires complete AKSK material")
	}
	if apiVersion == "" {
		apiVersion = "1.1"
	}
	if !apiVersionPattern.MatchString(apiVersion) {
		return fmt.Errorf("Alibaba Cloud DataHub requires a valid api_version when provided")
	}
	request.Header.Set("Date", now.UTC().Format(http.TimeFormat))
	request.Header.Set("X-Datahub-Client-Version", apiVersion)
	if credentials.SecurityToken != "" {
		request.Header.Set("X-Datahub-Security-Token", credentials.SecurityToken)
	}
	canonicalHeaders := canonicalPrefixedHeaders(request.Header, "x-datahub-")
	stringToSign := request.Method + "\n" + request.Header.Get("Content-Type") + "\n" + request.Header.Get("Date") + "\n" + canonicalHeaders + "\n" + canonicalAlibabaROAResource(request.URL)
	signature := base64.StdEncoding.EncodeToString(hmacBytes(sha1.New, []byte(credentials.AccessKeySecret), []byte(stringToSign)))
	request.Header.Set("Authorization", "DATAHUB "+credentials.AccessKeyID+":"+signature)
	return nil
}

func signAlibabaOpenSearch(request *http.Request, credentials AlibabaCredentials, now time.Time, nonce string) error {
	if credentials.AccessKeyID == "" || credentials.AccessKeySecret == "" {
		return fmt.Errorf("Alibaba Cloud OpenSearch requires complete AKSK material")
	}
	if len(nonce) != 16 {
		return fmt.Errorf("Alibaba Cloud OpenSearch requires a 16-digit nonce")
	}
	for _, character := range nonce {
		if character < '0' || character > '9' {
			return fmt.Errorf("Alibaba Cloud OpenSearch requires a 16-digit nonce")
		}
	}
	request.Header.Set("Date", now.UTC().Format("2006-01-02T15:04:05Z"))
	request.Header.Set("X-Opensearch-Nonce", nonce)
	if credentials.SecurityToken != "" {
		request.Header.Set("X-Opensearch-Security-Token", credentials.SecurityToken)
	}
	if request.Body != nil && request.Body != http.NoBody {
		bodyMD5, _, err := requestBodyMD5(request, false)
		if err != nil {
			return fmt.Errorf("hash Alibaba Cloud OpenSearch request body: %w", err)
		}
		request.Header.Set("Content-MD5", strings.ToLower(bodyMD5))
	} else {
		request.Header.Del("Content-MD5")
	}
	canonicalHeaders := canonicalPrefixedHeaders(request.Header, "x-opensearch-")
	resource := canonicalURI(request.URL)
	if request.Method == http.MethodGet {
		if query := canonicalAlibabaOpenSearchQuery(request.URL.Query()); query != "" {
			resource += "?" + query
		}
	}
	stringToSign := request.Method + "\n" + request.Header.Get("Content-MD5") + "\n" + request.Header.Get("Content-Type") + "\n" + request.Header.Get("Date") + "\n" + canonicalHeaders + "\n" + resource
	signature := base64.StdEncoding.EncodeToString(hmacBytes(sha1.New, []byte(credentials.AccessKeySecret), []byte(stringToSign)))
	request.Header.Set("Authorization", "OPENSEARCH "+credentials.AccessKeyID+":"+signature)
	return nil
}

func canonicalAlibabaOpenSearchQuery(values url.Values) string {
	items := make([]string, 0)
	for name, entries := range values {
		for _, value := range entries {
			if value != "" {
				items = append(items, uriEncode(name, true)+"="+uriEncode(value, true))
			}
		}
	}
	sort.Strings(items)
	return strings.Join(items, "&")
}

func alibabaOpenSearchNonce(now time.Time, seed string) string {
	digest := sha256.Sum256([]byte(seed))
	random := (int(digest[0])<<16|int(digest[1])<<8|int(digest[2]))%900000 + 100000
	return fmt.Sprintf("%010d%06d", now.UTC().Unix(), random)
}

func signAlibabaODPSV2(request *http.Request, credentials AlibabaCredentials, now time.Time) error {
	if credentials.AccessKeyID == "" || credentials.AccessKeySecret == "" {
		return fmt.Errorf("Alibaba Cloud ODPS requires complete AKSK material")
	}
	canonical, err := prepareAlibabaODPSRequest(request, now)
	if err != nil {
		return err
	}
	signature := base64.StdEncoding.EncodeToString(hmacBytes(sha1.New, []byte(credentials.AccessKeySecret), []byte(canonical)))
	request.Header.Set("Authorization", "ODPS "+credentials.AccessKeyID+":"+signature)
	setAlibabaODPSSTSToken(request, credentials.SecurityToken)
	return nil
}

func signAlibabaODPSV4(request *http.Request, credentials AlibabaCredentials, region string, now time.Time) error {
	if credentials.AccessKeyID == "" || credentials.AccessKeySecret == "" {
		return fmt.Errorf("Alibaba Cloud ODPS4 requires complete AKSK material")
	}
	if !identifierPattern.MatchString(region) {
		return fmt.Errorf("Alibaba Cloud ODPS4 requires a valid region")
	}
	canonical, err := prepareAlibabaODPSRequest(request, now)
	if err != nil {
		return err
	}
	date := now.UTC().Format("20060102")
	region = strings.ToLower(region)
	dateKey := hmacBytes(sha256.New, []byte("aliyun_v4"+credentials.AccessKeySecret), []byte(date))
	regionKey := hmacBytes(sha256.New, dateKey, []byte(region))
	serviceKey := hmacBytes(sha256.New, regionKey, []byte("odps"))
	signingKey := hmacBytes(sha256.New, serviceKey, []byte("aliyun_v4_request"))
	signature := base64.StdEncoding.EncodeToString(hmacBytes(sha1.New, signingKey, []byte(canonical)))
	credential := credentials.AccessKeyID + "/" + date + "/" + region + "/odps/aliyun_v4_request"
	request.Header.Set("Authorization", "ODPS "+credential+":"+signature)
	setAlibabaODPSSTSToken(request, credentials.SecurityToken)
	return nil
}

func prepareAlibabaODPSRequest(request *http.Request, now time.Time) (string, error) {
	if request == nil || request.URL == nil {
		return "", fmt.Errorf("Alibaba Cloud ODPS request is missing")
	}
	if request.Header == nil {
		request.Header = make(http.Header)
	}
	if request.Header.Get("Date") == "" {
		request.Header.Set("Date", now.UTC().Format(http.TimeFormat))
	}
	return canonicalAlibabaODPSRequest(request)
}

func canonicalAlibabaODPSRequest(request *http.Request) (string, error) {
	decodedQuery, err := url.PathUnescape(request.URL.RawQuery)
	if err != nil {
		return "", fmt.Errorf("decode Alibaba Cloud ODPS query: %w", err)
	}
	values, err := url.ParseQuery(decodedQuery)
	if err != nil {
		return "", fmt.Errorf("parse Alibaba Cloud ODPS query: %w", err)
	}
	queryNames := make([]string, 0, len(values))
	for name, entries := range values {
		if len(entries) != 1 {
			return "", fmt.Errorf("Alibaba Cloud ODPS query parameter %q must have exactly one value", name)
		}
		queryNames = append(queryNames, name)
	}
	sort.Strings(queryNames)
	queryParts := make([]string, 0, len(queryNames))
	for _, name := range queryNames {
		value := values[name][0]
		if value == "" {
			queryParts = append(queryParts, name)
		} else {
			queryParts = append(queryParts, name+"="+value)
		}
	}

	resource := request.URL.Path
	if isAlibabaODPSServiceEndpoint(request.URL.Hostname()) {
		if resource == "/api" {
			resource = ""
		} else if strings.HasPrefix(resource, "/api/") {
			resource = strings.TrimPrefix(resource, "/api")
		}
	}
	if len(queryParts) > 0 {
		resource += "?" + strings.Join(queryParts, "&")
	}

	headersToSign := map[string]string{
		"content-md5":  request.Header.Get("Content-MD5"),
		"content-type": request.Header.Get("Content-Type"),
		"date":         request.Header.Get("Date"),
	}
	for name, entries := range request.Header {
		lowerName := strings.ToLower(name)
		if strings.HasPrefix(lowerName, "x-odps") && len(entries) > 0 {
			headersToSign[lowerName] = entries[0]
		}
	}
	for _, name := range queryNames {
		if strings.HasPrefix(name, "x-odps-") {
			headersToSign[name] = values[name][0]
		}
	}
	headerNames := make([]string, 0, len(headersToSign))
	for name := range headersToSign {
		headerNames = append(headerNames, name)
	}
	sort.Strings(headerNames)
	lines := []string{request.Method}
	for _, name := range headerNames {
		if strings.HasPrefix(name, "x-odps-") {
			lines = append(lines, name+":"+headersToSign[name])
		} else {
			lines = append(lines, headersToSign[name])
		}
	}
	lines = append(lines, resource)
	return strings.Join(lines, "\n"), nil
}

func isAlibabaODPSServiceEndpoint(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return strings.HasPrefix(host, "service.") &&
		(strings.HasSuffix(host, ".maxcompute.aliyun.com") || strings.HasSuffix(host, ".maxcompute.aliyun-inc.com"))
}

func setAlibabaODPSSTSToken(request *http.Request, token string) {
	if token != "" {
		request.Header.Set("Authorization-Sts-Token", token)
	}
}

func signAlibabaFC(request *http.Request, credentials AlibabaCredentials, now time.Time) error {
	if credentials.AccessKeyID == "" || credentials.AccessKeySecret == "" {
		return fmt.Errorf("Alibaba Cloud Function Compute requires complete AKSK material")
	}
	request.Header.Set("Date", now.UTC().Format(http.TimeFormat))
	if credentials.SecurityToken != "" {
		request.Header.Set("X-Fc-Security-Token", credentials.SecurityToken)
	}
	contentMD5, hasBody, err := requestBodyMD5(request, false)
	if err != nil {
		return fmt.Errorf("hash Alibaba Cloud Function Compute request body: %w", err)
	}
	if hasBody {
		request.Header.Set("Content-MD5", strings.ToLower(contentMD5))
	} else {
		request.Header.Del("Content-MD5")
	}
	resource := canonicalAlibabaFCResource(request.URL)
	signature := alibabaFCSignature(credentials.AccessKeySecret, request.Method, request.Header, request.Header.Get("Date"), resource)
	request.Header.Set("Authorization", "FC "+credentials.AccessKeyID+":"+signature)
	return nil
}

func alibabaFCSignature(secret, method string, headers http.Header, date, resource string) string {
	names := make([]string, 0)
	values := make(map[string]string)
	for name, entries := range headers {
		lowerName := strings.ToLower(strings.TrimSpace(name))
		if len(entries) > 0 {
			values[lowerName] = entries[0]
		}
		if strings.HasPrefix(lowerName, "x-fc-") && len(entries) > 0 {
			names = append(names, lowerName)
		}
	}
	sort.Strings(names)
	var canonicalHeaders strings.Builder
	for _, name := range names {
		canonicalHeaders.WriteString(name)
		canonicalHeaders.WriteByte(':')
		canonicalHeaders.WriteString(values[name])
		canonicalHeaders.WriteByte('\n')
	}
	stringToSign := strings.ToUpper(method) + "\n" + values["content-md5"] + "\n" + values["content-type"] + "\n" + date + "\n" + canonicalHeaders.String() + resource
	return base64.StdEncoding.EncodeToString(hmacBytes(sha256.New, []byte(secret), []byte(stringToSign)))
}

func canonicalAlibabaFCResource(target *url.URL) string {
	path := target.Path
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(segments) < 2 || segments[1] != "proxy" {
		return path
	}
	query := target.Query()
	parts := make([]string, 0)
	for name, entries := range query {
		if len(entries) == 0 {
			parts = append(parts, name)
			continue
		}
		for _, value := range entries {
			parts = append(parts, name+"="+value)
		}
	}
	sort.Strings(parts)
	return path + "\n" + strings.Join(parts, "\n")
}

func signAlibabaFC3(request *http.Request, credentials AlibabaCredentials, now time.Time) error {
	if credentials.AccessKeyID == "" || credentials.AccessKeySecret == "" {
		return fmt.Errorf("Alibaba Cloud Function Compute ACS3 trigger requires complete AKSK material")
	}
	request.Header.Set("X-Acs-Date", now.UTC().Format("2006-01-02T15:04:05Z"))
	request.Header.Set("X-Acs-Security-Token", credentials.SecurityToken)
	authorization, err := alibabaFC3Authorization(request, credentials)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", authorization)
	return nil
}

func alibabaFC3Authorization(request *http.Request, credentials AlibabaCredentials) (string, error) {
	canonicalQuery, err := canonicalAlibabaSingleValueQuery(request.URL.Query(), true)
	if err != nil {
		return "", fmt.Errorf("canonicalize Alibaba Cloud Function Compute ACS3 trigger query: %w", err)
	}
	canonicalURI := request.URL.EscapedPath()
	if canonicalURI == "" {
		canonicalURI = "/"
	}
	canonicalURI = strings.NewReplacer("$", "%24", "+", "%20", "*", "%2A", "%7E", "~").Replace(canonicalURI)
	canonicalHeaders, signedHeaders := canonicalAlibabaFC3Headers(request.Header)
	canonicalRequest := strings.ToUpper(request.Method) + "\n" + canonicalURI + "\n" + canonicalQuery + "\n" + canonicalHeaders + "\n" + signedHeaders + "\n"
	stringToSign := "ACS3-HMAC-SHA256\n" + sha256Hex([]byte(canonicalRequest))
	signature := hmacHex(sha256.New, []byte(credentials.AccessKeySecret), []byte(stringToSign))
	return "ACS3-HMAC-SHA256 Credential=" + credentials.AccessKeyID + ",SignedHeaders=" + signedHeaders + ",Signature=" + signature, nil
}

func canonicalAlibabaFC3Headers(headers http.Header) (string, string) {
	values := make(map[string]string)
	for name, entries := range headers {
		lowerName := strings.ToLower(name)
		if (strings.HasPrefix(lowerName, "x-acs-") || lowerName == "host" || lowerName == "content-type") && len(entries) > 0 {
			values[lowerName] = strings.TrimSpace(entries[0])
		}
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	var canonical strings.Builder
	for _, name := range names {
		canonical.WriteString(name)
		canonical.WriteByte(':')
		canonical.WriteString(values[name])
		canonical.WriteByte('\n')
	}
	return canonical.String(), strings.Join(names, ";")
}

func signAlibabaFCCustomDomain(request *http.Request, credentials AlibabaCredentials, now time.Time) error {
	if credentials.AccessKeyID == "" || credentials.AccessKeySecret == "" {
		return fmt.Errorf("Alibaba Cloud Function Compute custom-domain signing requires complete AKSK material")
	}
	request.Header.Set("Date", now.UTC().Format(http.TimeFormat))
	if credentials.SecurityToken != "" {
		request.Header.Set("X-Acs-Security-Token", credentials.SecurityToken)
	}
	resource, err := canonicalAlibabaFCCustomResource(request.URL)
	if err != nil {
		return err
	}
	canonicalHeaders := canonicalPrefixedHeaders(request.Header, "x-acs-")
	stringToSign := strings.ToUpper(request.Method) + "\n" + request.Header.Get("Accept") + "\n" + request.Header.Get("Content-MD5") + "\n" + request.Header.Get("Content-Type") + "\n" + request.Header.Get("Date") + "\n"
	if canonicalHeaders != "" {
		stringToSign += canonicalHeaders + "\n"
	}
	stringToSign += resource
	signature := base64.StdEncoding.EncodeToString(hmacBytes(sha1.New, []byte(credentials.AccessKeySecret), []byte(stringToSign)))
	request.Header.Set("Authorization", "acs "+credentials.AccessKeyID+":"+signature)
	return nil
}

func canonicalAlibabaFCCustomResource(target *url.URL) (string, error) {
	query, err := canonicalAlibabaSingleValueQuery(target.Query(), false)
	if err != nil {
		return "", fmt.Errorf("canonicalize Alibaba Cloud Function Compute custom-domain query: %w", err)
	}
	resource := target.EscapedPath()
	if resource == "" {
		resource = "/"
	}
	if query != "" {
		resource += "?" + query
	}
	return resource, nil
}

func canonicalAlibabaSingleValueQuery(values url.Values, encodeValue bool) (string, error) {
	names := make([]string, 0, len(values))
	for name, entries := range values {
		if len(entries) != 1 {
			return "", fmt.Errorf("query parameter %q must have exactly one value", name)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		value := values[name][0]
		if encodeValue {
			parts = append(parts, name+"="+uriEncode(value, true))
		} else if value == "" {
			parts = append(parts, name)
		} else {
			parts = append(parts, name+"="+value)
		}
	}
	return strings.Join(parts, "&"), nil
}

func signTencentTC3(request *http.Request, payloadHash string, credentials TencentCredentials, invocation Invocation, now time.Time) error {
	if !identifierPattern.MatchString(invocation.Service) || !identifierPattern.MatchString(invocation.Operation) || !identifierPattern.MatchString(invocation.APIVersion) {
		return fmt.Errorf("Tencent Cloud TC3 requires valid service, operation, and api_version")
	}
	if request.Header.Get("Content-Type") == "" {
		request.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	timestamp := strconv.FormatInt(now.Unix(), 10)
	request.Header.Set("X-TC-Action", invocation.Operation)
	request.Header.Set("X-TC-Version", invocation.APIVersion)
	request.Header.Set("X-TC-Timestamp", timestamp)
	if invocation.Region != "" {
		request.Header.Set("X-TC-Region", invocation.Region)
	}
	if credentials.Token != "" {
		request.Header.Set("X-TC-Token", credentials.Token)
	}
	canonicalHeaders, signedHeaders := canonicalHeaders(request, func(name string) bool {
		return name == "content-type" || name == "host"
	})
	canonicalHeaders = strings.ToLower(canonicalHeaders)
	canonicalRequest := strings.Join([]string{
		request.Method, canonicalURI(request.URL), canonicalQuery(request.URL.Query()), canonicalHeaders, signedHeaders, payloadHash,
	}, "\n")
	date := now.UTC().Format("2006-01-02")
	service := strings.ToLower(invocation.Service)
	credentialScope := date + "/" + service + "/tc3_request"
	stringToSign := "TC3-HMAC-SHA256\n" + timestamp + "\n" + credentialScope + "\n" + sha256Hex([]byte(canonicalRequest))
	secretDate := hmacBytes(sha256.New, []byte("TC3"+credentials.SecretKey), []byte(date))
	secretService := hmacBytes(sha256.New, secretDate, []byte(service))
	secretSigning := hmacBytes(sha256.New, secretService, []byte("tc3_request"))
	signature := hmacHex(sha256.New, secretSigning, []byte(stringToSign))
	request.Header.Set("Authorization", "TC3-HMAC-SHA256 Credential="+credentials.SecretID+"/"+credentialScope+", SignedHeaders="+signedHeaders+", Signature="+signature)
	return nil
}

func signTencentV1(request *http.Request, credentials TencentCredentials, invocation Invocation, now time.Time, nonce string, useSHA256 bool) error {
	return signTencentQuery(request, credentials, invocation, now, nonce, tencentQuerySigningOptions{
		protocol: "Tencent Cloud API v1", requiredPath: "/", includeVersion: true, useSHA256: useSHA256,
	})
}

func signTencentQCloud(request *http.Request, credentials TencentCredentials, invocation Invocation, now time.Time, nonce string, useSHA256 bool) error {
	host := strings.ToLower(request.URL.Hostname())
	if !strings.HasSuffix(host, ".api.qcloud.com") || host == ".api.qcloud.com" {
		return fmt.Errorf("Tencent Cloud legacy API requires a product .api.qcloud.com endpoint")
	}
	return signTencentQuery(request, credentials, invocation, now, nonce, tencentQuerySigningOptions{
		protocol: "Tencent Cloud legacy API", requiredPath: "/v2/index.php", emitSHA1Method: true, useSHA256: useSHA256,
	})
}

type tencentQuerySigningOptions struct {
	protocol       string
	requiredPath   string
	includeVersion bool
	emitSHA1Method bool
	useSHA256      bool
}

func signTencentQuery(request *http.Request, credentials TencentCredentials, invocation Invocation, now time.Time, nonce string, options tencentQuerySigningOptions) error {
	if parsedNonce, err := strconv.ParseUint(nonce, 10, 63); err != nil || parsedNonce == 0 {
		return fmt.Errorf("%s requires a positive numeric nonce", options.protocol)
	}
	if request.Method != http.MethodGet && request.Method != http.MethodPost {
		return fmt.Errorf("%s requires method GET or POST", options.protocol)
	}
	path := request.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	if path != options.requiredPath {
		return fmt.Errorf("%s requires request path %s", options.protocol, options.requiredPath)
	}
	values := request.URL.Query()
	if request.Method == http.MethodPost {
		if !strings.HasPrefix(strings.ToLower(request.Header.Get("Content-Type")), "application/x-www-form-urlencoded") {
			return fmt.Errorf("%s POST requires application/x-www-form-urlencoded", options.protocol)
		}
		if request.URL.RawQuery != "" {
			return fmt.Errorf("%s POST does not allow query parameters outside the form body", options.protocol)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			return fmt.Errorf("read %s form body: %w", options.protocol, err)
		}
		values, err = url.ParseQuery(string(body))
		if err != nil {
			return fmt.Errorf("parse %s form body: %w", options.protocol, err)
		}
	}
	parameters := make(map[string]string)
	for name, entries := range values {
		if isTencentV1ControlledParameter(name) {
			return fmt.Errorf("caller-supplied %s signing parameter %q is forbidden", options.protocol, name)
		}
		if len(entries) != 1 {
			return fmt.Errorf("%s parameter %q must have exactly one value", options.protocol, name)
		}
		parameters[name] = entries[0]
	}
	parameters["Action"] = invocation.Operation
	parameters["Nonce"] = nonce
	parameters["SecretId"] = credentials.SecretID
	parameters["Timestamp"] = strconv.FormatInt(now.UTC().Unix(), 10)
	if options.includeVersion {
		parameters["Version"] = invocation.APIVersion
	}
	if invocation.Region != "" {
		parameters["Region"] = invocation.Region
	}
	if credentials.Token != "" {
		parameters["Token"] = credentials.Token
	}
	var algorithm func() hash.Hash = sha1.New
	if options.useSHA256 {
		parameters["SignatureMethod"] = "HmacSHA256"
		algorithm = sha256.New
	} else if options.emitSHA1Method {
		parameters["SignatureMethod"] = "HmacSHA1"
	}
	canonical := canonicalTencentV1Parameters(parameters)
	stringToSign := request.Method + request.URL.Host + path + "?" + canonical
	parameters["Signature"] = base64.StdEncoding.EncodeToString(hmacBytes(algorithm, []byte(credentials.SecretKey), []byte(stringToSign)))
	encoded := encodeTencentV1Parameters(parameters)
	if request.Method == http.MethodGet {
		request.URL.RawQuery = encoded
	} else {
		request.Body = io.NopCloser(strings.NewReader(encoded))
		request.ContentLength = int64(len(encoded))
		request.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(encoded)), nil }
	}
	return nil
}

func canonicalTencentV1Parameters(parameters map[string]string) string {
	names := make([]string, 0, len(parameters))
	for name := range parameters {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+"="+parameters[name])
	}
	return strings.Join(parts, "&")
}

func encodeTencentV1Parameters(parameters map[string]string) string {
	names := make([]string, 0, len(parameters))
	for name := range parameters {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+"="+uriEncode(parameters[name], true))
	}
	return strings.Join(parts, "&")
}

func signAlibabaOSSV4(request *http.Request, credentials AlibabaCredentials, region string, now time.Time) error {
	if !identifierPattern.MatchString(region) {
		return fmt.Errorf("Alibaba Cloud OSS4 requires a valid region")
	}
	request.Header.Set("X-Oss-Date", now.UTC().Format("20060102T150405Z"))
	request.Header.Set("X-Oss-Content-Sha256", "UNSIGNED-PAYLOAD")
	if credentials.SecurityToken != "" {
		request.Header.Set("X-Oss-Security-Token", credentials.SecurityToken)
	}
	canonicalHeadersValue, _ := canonicalHeaders(request, func(name string) bool {
		return name == "content-type" || name == "content-md5" || strings.HasPrefix(name, "x-oss-")
	})
	additionalHeaders := ""
	canonicalRequest := strings.Join([]string{
		request.Method, canonicalURI(request.URL), canonicalOSSQuery(request.URL), canonicalHeadersValue, additionalHeaders, "UNSIGNED-PAYLOAD",
	}, "\n")
	date := now.UTC().Format("20060102")
	scope := date + "/" + strings.ToLower(region) + "/oss/aliyun_v4_request"
	stringToSign := "OSS4-HMAC-SHA256\n" + now.UTC().Format("20060102T150405Z") + "\n" + scope + "\n" + sha256Hex([]byte(canonicalRequest))
	dateKey := hmacBytes(sha256.New, []byte("aliyun_v4"+credentials.AccessKeySecret), []byte(date))
	regionKey := hmacBytes(sha256.New, dateKey, []byte(strings.ToLower(region)))
	serviceKey := hmacBytes(sha256.New, regionKey, []byte("oss"))
	signingKey := hmacBytes(sha256.New, serviceKey, []byte("aliyun_v4_request"))
	signature := hmacHex(sha256.New, signingKey, []byte(stringToSign))
	authorization := "OSS4-HMAC-SHA256 Credential=" + credentials.AccessKeyID + "/" + scope
	if additionalHeaders != "" {
		authorization += ",AdditionalHeaders=" + additionalHeaders
	}
	authorization += ",Signature=" + signature
	request.Header.Set("Authorization", authorization)
	return nil
}

func signAlibabaOSSV1(request *http.Request, credentials AlibabaCredentials, now time.Time) error {
	request.Header.Set("Date", now.UTC().Format(http.TimeFormat))
	if credentials.SecurityToken != "" {
		request.Header.Set("X-Oss-Security-Token", credentials.SecurityToken)
	}
	date := request.Header.Get("Date")
	if ossDate := request.Header.Get("X-Oss-Date"); ossDate != "" {
		date = ossDate
	}
	canonicalHeaders := canonicalPrefixedHeaders(request.Header, "x-oss-")
	stringToSign := strings.Join([]string{
		request.Method,
		request.Header.Get("Content-MD5"),
		request.Header.Get("Content-Type"),
		date,
	}, "\n") + "\n"
	if canonicalHeaders != "" {
		stringToSign += canonicalHeaders + "\n"
	}
	stringToSign += canonicalAlibabaOSSV1Resource(request.URL)
	signature := base64.StdEncoding.EncodeToString(hmacBytes(sha1.New, []byte(credentials.AccessKeySecret), []byte(stringToSign)))
	request.Header.Set("Authorization", "OSS "+credentials.AccessKeyID+":"+signature)
	return nil
}

func canonicalAlibabaOSSV1Resource(target *url.URL) string {
	if target == nil {
		return "/"
	}
	host := strings.ToLower(target.Hostname())
	bucket := ""
	for _, marker := range []string{".oss-", ".oss."} {
		if index := strings.Index(host, marker); index > 0 {
			bucket = host[:index]
			break
		}
	}
	path := target.Path
	if path == "" {
		path = "/"
	}
	if bucket == "" && (strings.HasPrefix(host, "oss-") || strings.HasPrefix(host, "oss.")) {
		trimmed := strings.TrimPrefix(path, "/")
		if index := strings.IndexByte(trimmed, '/'); index >= 0 {
			bucket = trimmed[:index]
			path = "/" + trimmed[index+1:]
		} else if trimmed != "" {
			bucket = trimmed
			path = "/"
		}
	}
	resource := ""
	if bucket != "" {
		resource = "/" + bucket
		if !strings.HasPrefix(path, "/") {
			resource += "/"
		}
	}
	resource += path
	if resource == "" || resource == "//" {
		resource = "/"
	}
	query := target.Query()
	parts := make([]string, 0)
	for name, entries := range query {
		if !isAlibabaOSSV1Subresource(name) {
			continue
		}
		value := ""
		if len(entries) > 0 {
			value = entries[0]
		}
		if value == "" {
			parts = append(parts, name)
		} else {
			parts = append(parts, name+"="+value)
		}
	}
	sort.Strings(parts)
	if len(parts) > 0 {
		resource += "?" + strings.Join(parts, "&")
	}
	return resource
}

func isAlibabaOSSV1Subresource(name string) bool {
	if strings.HasPrefix(name, "x-oss-") {
		return true
	}
	_, ok := alibabaOSSV1Subresources[name]
	return ok
}

var alibabaOSSV1Subresources = map[string]struct{}{
	"DeleteVectorIndex": {}, "GetVectorIndex": {}, "ListVectorIndexes": {}, "PutVectorIndex": {},
	"accessPoint": {}, "accessPointConfigForObjectProcess": {}, "accessPointForObjectProcess": {}, "accessPointPolicy": {}, "accessPointPolicyForObjectProcess": {},
	"acl": {}, "append": {}, "bucketArchiveDirectRead": {}, "bucketInfo": {}, "callback": {}, "callback-var": {}, "cleanRestoredObject": {}, "cloudboxes": {}, "cname": {}, "comp": {}, "continuation-token": {}, "cors": {},
	"delete": {}, "encryption": {}, "httpsConfig": {}, "img": {}, "inventory": {}, "inventoryId": {}, "legalHold": {}, "lifecycle": {}, "live": {}, "location": {}, "logging": {}, "metaQuery": {},
	"objectMeta": {}, "objectWorm": {}, "overwriteConfig": {}, "partNumber": {}, "policy": {}, "policyStatus": {}, "position": {}, "publicAccessBlock": {}, "qos": {}, "redundancyTransition": {}, "referer": {},
	"regionList": {}, "regions": {}, "replication": {}, "replicationLocation": {}, "replicationProgress": {}, "requestPayment": {}, "resourceGroup": {}, "restore": {}, "retention": {}, "rtc": {}, "seal": {}, "security-token": {}, "sequential": {},
	"startTime": {}, "stat": {}, "status": {}, "style": {}, "styleName": {}, "symlink": {}, "tagging": {}, "transferAcceleration": {}, "uploadId": {}, "uploads": {}, "userDefinedLogFieldsConfig": {}, "versionId": {}, "versioning": {}, "versions": {}, "vod": {}, "website": {},
	"worm": {}, "wormExtend": {}, "wormId": {}, "x-oss-process": {}, "x-oss-write-get-object-response": {},
	"response-cache-control": {}, "response-content-disposition": {}, "response-content-encoding": {}, "response-content-language": {}, "response-content-type": {}, "response-expires": {},
}

func signAlibabaSLSV1(request *http.Request, credentials AlibabaCredentials, apiVersion string, now time.Time) error {
	if apiVersion == "" {
		apiVersion = "0.6.0"
	}
	request.Header.Set("X-Log-Apiversion", apiVersion)
	request.Header.Set("X-Log-Signaturemethod", "hmac-sha1")
	if request.Header.Get("Date") == "" {
		request.Header.Set("Date", now.UTC().Format(http.TimeFormat))
	}
	if credentials.SecurityToken != "" {
		request.Header.Set("X-Acs-Security-Token", credentials.SecurityToken)
	}
	contentMD5, hasBody, err := requestBodyMD5(request, false)
	if err != nil {
		return fmt.Errorf("hash Alibaba SLS request body: %w", err)
	}
	if hasBody {
		request.Header.Set("Content-MD5", contentMD5)
	}
	canonicalHeaders := canonicalPrefixedHeaders(request.Header, "x-log-", "x-acs-")
	canonicalResource := canonicalAlibabaSLSResource(request.URL)
	stringToSign := strings.Join([]string{
		request.Method,
		request.Header.Get("Content-MD5"),
		request.Header.Get("Content-Type"),
		request.Header.Get("Date"),
	}, "\n") + "\n" + canonicalHeaders + "\n" + canonicalResource
	signature := base64.StdEncoding.EncodeToString(hmacBytes(sha1.New, []byte(credentials.AccessKeySecret), []byte(stringToSign)))
	request.Header.Set("Authorization", "LOG "+credentials.AccessKeyID+":"+signature)
	return nil
}

func signAlibabaSLSV4(request *http.Request, payloadHash string, credentials AlibabaCredentials, region, apiVersion string, now time.Time) error {
	if !identifierPattern.MatchString(region) {
		return fmt.Errorf("Alibaba Cloud SLS4 requires a valid region")
	}
	if request.Header.Get("X-Log-Date") == "" {
		request.Header.Set("X-Log-Date", now.UTC().Format("20060102T150405Z"))
	}
	dateTime := request.Header.Get("X-Log-Date")
	if len(dateTime) < 8 {
		return fmt.Errorf("Alibaba Cloud SLS4 requires a valid x-log-date")
	}
	if apiVersion != "" {
		request.Header.Set("X-Log-Apiversion", apiVersion)
	}
	request.Header.Set("X-Log-Content-Sha256", payloadHash)
	if credentials.SecurityToken != "" {
		request.Header.Set("X-Acs-Security-Token", credentials.SecurityToken)
	}
	canonicalHeaders, signedHeaders := canonicalAlibabaSLSV4Headers(request)
	canonicalRequest := strings.Join([]string{
		request.Method,
		canonicalURI(request.URL),
		canonicalAlibabaSLSV4Query(request.URL),
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")
	date := dateTime[:8]
	scope := date + "/" + strings.ToLower(region) + "/sls/aliyun_v4_request"
	stringToSign := "SLS4-HMAC-SHA256\n" + dateTime + "\n" + scope + "\n" + sha256Hex([]byte(canonicalRequest))
	dateKey := hmacBytes(sha256.New, []byte("aliyun_v4"+credentials.AccessKeySecret), []byte(date))
	regionKey := hmacBytes(sha256.New, dateKey, []byte(strings.ToLower(region)))
	serviceKey := hmacBytes(sha256.New, regionKey, []byte("sls"))
	signingKey := hmacBytes(sha256.New, serviceKey, []byte("aliyun_v4_request"))
	signature := hmacHex(sha256.New, signingKey, []byte(stringToSign))
	request.Header.Set("Authorization", "SLS4-HMAC-SHA256 Credential="+credentials.AccessKeyID+"/"+scope+",Signature="+signature)
	return nil
}

func signAlibabaMNS(request *http.Request, credentials AlibabaCredentials, apiVersion string, now time.Time) error {
	if apiVersion == "" {
		apiVersion = "2015-06-06"
	}
	request.Header.Set("X-Mns-Version", apiVersion)
	if request.Header.Get("Date") == "" {
		request.Header.Set("Date", now.UTC().Format(http.TimeFormat))
	}
	if credentials.SecurityToken != "" {
		request.Header.Set("Security-Token", credentials.SecurityToken)
	}
	contentMD5, hasBody, err := requestBodyMD5(request, true)
	if err != nil {
		return fmt.Errorf("hash Alibaba MNS request body: %w", err)
	}
	if hasBody {
		request.Header.Set("Content-MD5", contentMD5)
	}
	date := request.Header.Get("Date")
	if request.Header.Get("X-Mns-Date") != "" {
		date = request.Header.Get("X-Mns-Date")
	}
	canonicalHeaders := canonicalPrefixedHeaders(request.Header, "x-mns-")
	resource := canonicalURI(request.URL)
	if request.URL.RawQuery != "" {
		resource += "?" + request.URL.RawQuery
	}
	stringToSign := strings.Join([]string{
		request.Method,
		request.Header.Get("Content-MD5"),
		request.Header.Get("Content-Type"),
		date,
	}, "\n") + "\n" + canonicalHeaders + "\n" + resource
	signature := base64.StdEncoding.EncodeToString(hmacBytes(sha1.New, []byte(credentials.AccessKeySecret), []byte(stringToSign)))
	request.Header.Set("Authorization", "MNS "+credentials.AccessKeyID+":"+signature)
	return nil
}

func signAlibabaOTSV2(request *http.Request, credentials AlibabaCredentials, apiVersion string, now time.Time) error {
	if err := prepareAlibabaOTSHeaders(request, credentials, apiVersion, now); err != nil {
		return err
	}
	stringToSign, err := alibabaOTSStringToSign(request, false)
	if err != nil {
		return err
	}
	signature := base64.StdEncoding.EncodeToString(hmacBytes(sha1.New, []byte(credentials.AccessKeySecret), []byte(stringToSign)))
	request.Header.Set("X-Ots-Signature", signature)
	return nil
}

func signAlibabaOTSV4(request *http.Request, credentials AlibabaCredentials, region, apiVersion string, now time.Time) error {
	if !identifierPattern.MatchString(region) {
		return fmt.Errorf("Alibaba Cloud OTS4 requires a valid region")
	}
	if err := prepareAlibabaOTSHeaders(request, credentials, apiVersion, now); err != nil {
		return err
	}
	signingDate := request.Header.Get("X-Ots-Signdate")
	if signingDate == "" {
		requestDate := request.Header.Get("X-Ots-Date")
		if len(requestDate) >= 10 {
			signingDate = strings.ReplaceAll(requestDate[:10], "-", "")
		} else {
			signingDate = now.UTC().Format("20060102")
		}
		request.Header.Set("X-Ots-Signdate", signingDate)
	}
	request.Header.Set("X-Ots-Signregion", strings.ToLower(region))
	stringToSign, err := alibabaOTSStringToSign(request, true)
	if err != nil {
		return err
	}
	dateKey := hmacBytes(sha256.New, []byte("aliyun_v4"+credentials.AccessKeySecret), []byte(signingDate))
	regionKey := hmacBytes(sha256.New, dateKey, []byte(strings.ToLower(region)))
	serviceKey := hmacBytes(sha256.New, regionKey, []byte("ots"))
	finalKey := hmacBytes(sha256.New, serviceKey, []byte("aliyun_v4_request"))
	signingAccessKey := base64.StdEncoding.EncodeToString(finalKey)
	signature := base64.StdEncoding.EncodeToString(hmacBytes(sha256.New, []byte(signingAccessKey), []byte(stringToSign+"ots")))
	request.Header.Set("X-Ots-Signaturev4", signature)
	return nil
}

func prepareAlibabaOTSHeaders(request *http.Request, credentials AlibabaCredentials, apiVersion string, now time.Time) error {
	if apiVersion == "" {
		apiVersion = "2015-12-31"
	}
	if request.Header.Get("X-Ots-Date") == "" {
		request.Header.Set("X-Ots-Date", now.UTC().Format("2006-01-02T15:04:05.123Z"))
	}
	request.Header.Set("X-Ots-Apiversion", apiVersion)
	request.Header.Set("X-Ots-Accesskeyid", credentials.AccessKeyID)
	host := strings.ToLower(request.URL.Hostname())
	if request.Header.Get("X-Ots-Instancename") == "" {
		instance, _, ok := strings.Cut(host, ".")
		if !ok || !endpointLabelPattern.MatchString(instance) {
			return fmt.Errorf("Alibaba Cloud OTS endpoint must identify an instance in the first DNS label")
		}
		request.Header.Set("X-Ots-Instancename", instance)
	}
	bodyMD5, err := rawRequestBodyMD5(request)
	if err != nil {
		return fmt.Errorf("hash Alibaba OTS request body: %w", err)
	}
	request.Header.Set("X-Ots-Contentmd5", bodyMD5)
	if credentials.SecurityToken != "" {
		request.Header.Set("X-Ots-Ststoken", credentials.SecurityToken)
	}
	return nil
}

var alibabaOTSSignedHeaders = []string{
	"x-ots-accesskeyid", "x-ots-admin-target-userid", "x-ots-admin-task-type", "x-ots-apiversion",
	"x-ots-charge-for-admin", "x-ots-contentmd5", "x-ots-date", "x-ots-instancename", "x-ots-issecuretransport",
	"x-ots-playeraccountid", "x-ots-request-compress-size", "x-ots-request-compress-type", "x-ots-request-priority",
	"x-ots-request-search-tag", "x-ots-request-tag", "x-ots-response-compress-type", "x-ots-sdk-traceid",
	"x-ots-signdate", "x-ots-signregion", "x-ots-sourceip", "x-ots-ststoken", "x-ots-tunnel-type",
}

func alibabaOTSStringToSign(request *http.Request, v4 bool) (string, error) {
	required := []string{"x-ots-date", "x-ots-apiversion", "x-ots-accesskeyid", "x-ots-contentmd5", "x-ots-instancename"}
	if v4 {
		required = append(required, "x-ots-signregion", "x-ots-signdate")
	}
	for _, name := range required {
		if request.Header.Get(name) == "" {
			return "", fmt.Errorf("Alibaba Cloud OTS signing requires %s", name)
		}
	}
	var builder strings.Builder
	builder.WriteString(canonicalURI(request.URL))
	builder.WriteByte('\n')
	builder.WriteString(request.Method)
	builder.WriteString("\n\n")
	for _, name := range alibabaOTSSignedHeaders {
		if value := request.Header.Get(name); value != "" {
			builder.WriteString(name)
			builder.WriteByte(':')
			builder.WriteString(strings.TrimSpace(value))
			builder.WriteByte('\n')
		}
	}
	return builder.String(), nil
}

func rawRequestBodyMD5(request *http.Request) (string, error) {
	digest := md5.New()
	if request.Body != nil && request.Body != http.NoBody {
		if request.GetBody == nil {
			return "", fmt.Errorf("signed HTTP body is not replayable")
		}
		body, err := request.GetBody()
		if err != nil {
			return "", err
		}
		defer body.Close()
		if _, err := io.Copy(digest, body); err != nil {
			return "", err
		}
	}
	return base64.StdEncoding.EncodeToString(digest.Sum(nil)), nil
}

func requestBodyMD5(request *http.Request, mnsEncoding bool) (string, bool, error) {
	if request.Body == nil || request.Body == http.NoBody {
		return "", false, nil
	}
	if request.GetBody == nil {
		return "", false, fmt.Errorf("signed HTTP body is not replayable")
	}
	body, err := request.GetBody()
	if err != nil {
		return "", false, err
	}
	defer body.Close()
	digest := md5.New()
	if _, err := io.Copy(digest, body); err != nil {
		return "", false, err
	}
	if mnsEncoding {
		hexDigest := hex.EncodeToString(digest.Sum(nil))
		return base64.StdEncoding.EncodeToString([]byte(hexDigest)), true, nil
	}
	return strings.ToUpper(hex.EncodeToString(digest.Sum(nil))), true, nil
}

func canonicalPrefixedHeaders(headers http.Header, prefixes ...string) string {
	values := make(map[string]string)
	for name, entries := range headers {
		lower := strings.ToLower(strings.TrimSpace(name))
		for _, prefix := range prefixes {
			if strings.HasPrefix(lower, prefix) {
				values[lower] = strings.TrimSpace(strings.Join(entries, ","))
				break
			}
		}
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	lines := make([]string, 0, len(names))
	for _, name := range names {
		lines = append(lines, name+":"+values[name])
	}
	return strings.Join(lines, "\n")
}

func canonicalAlibabaSLSResource(target *url.URL) string {
	resource := canonicalURI(target)
	if target.RawQuery == "" {
		return resource
	}
	values := target.Query()
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	var query strings.Builder
	for index, name := range names {
		if index > 0 {
			query.WriteByte('&')
		}
		for _, value := range values[name] {
			query.WriteString(name)
			query.WriteByte('=')
			query.WriteString(value)
		}
	}
	return resource + "?" + query.String()
}

func canonicalAlibabaSLSV4Headers(request *http.Request) (string, string) {
	values := make(map[string]string)
	for name, entries := range request.Header {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "x-log-meta-") {
			continue
		}
		if strings.HasPrefix(lower, "x-log-") || strings.HasPrefix(lower, "x-acs-") || lower == "content-type" {
			values[lower] = strings.Join(entries, ",")
		}
	}
	if request.URL.Host != "" || request.Host != "" {
		host := request.URL.Host
		if request.Host != "" {
			host = request.Host
		}
		values["host"] = host
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	var canonical strings.Builder
	for _, name := range names {
		canonical.WriteString(name)
		canonical.WriteByte(':')
		canonical.WriteString(values[name])
		canonical.WriteByte('\n')
	}
	return canonical.String(), strings.Join(names, ";")
}

func canonicalAlibabaSLSV4Query(target *url.URL) string {
	values := target.Query()
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		value := ""
		if len(values[name]) > 0 {
			value = strings.ReplaceAll(url.QueryEscape(values[name][0]), "+", "%20")
		}
		if value == "" {
			parts = append(parts, name)
		} else {
			parts = append(parts, name+"="+value)
		}
	}
	return strings.Join(parts, "&")
}

func hasHeader(headers map[string]string, name string) bool {
	for candidate := range headers {
		if strings.EqualFold(candidate, name) {
			return true
		}
	}
	return false
}

func cloneStringMap(values map[string]string) map[string]string {
	cloned := make(map[string]string, len(values)+1)
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func signTencentCOS(request *http.Request, credentials TencentCredentials, now time.Time) error {
	start := now.UTC().Unix()
	end := start + 900
	keyTime := fmt.Sprintf("%d;%d", start, end)
	request.Header.Set("Date", now.UTC().Format(http.TimeFormat))
	if credentials.Token != "" {
		request.Header.Set("X-Cos-Security-Token", credentials.Token)
	}
	canonicalHeaderValue, headerList := canonicalCOSHeaders(request)
	queryValue, queryList := canonicalCOSQuery(request.URL.Query())
	path := request.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	httpString := strings.ToLower(request.Method) + "\n" + path + "\n" + queryValue + "\n" + canonicalHeaderValue + "\n"
	stringToSign := "sha1\n" + keyTime + "\n" + sha1Hex([]byte(httpString)) + "\n"
	signKey := hmacHex(sha1.New, []byte(credentials.SecretKey), []byte(keyTime))
	signature := hmacHex(sha1.New, []byte(signKey), []byte(stringToSign))
	request.Header.Set("Authorization", "q-sign-algorithm=sha1&q-ak="+uriEncode(credentials.SecretID, true)+"&q-sign-time="+keyTime+"&q-key-time="+keyTime+"&q-header-list="+headerList+"&q-url-param-list="+queryList+"&q-signature="+signature)
	return nil
}

func canonicalHeaders(request *http.Request, include func(string) bool) (string, string) {
	values := make(map[string]string)
	for name, entries := range request.Header {
		lower := strings.ToLower(strings.TrimSpace(name))
		if include(lower) {
			values[lower] = strings.Join(entries, ",")
		}
	}
	if include("host") {
		host := request.URL.Host
		if request.Host != "" {
			host = request.Host
		}
		values["host"] = host
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	var canonical strings.Builder
	for _, name := range names {
		canonical.WriteString(name)
		canonical.WriteByte(':')
		canonical.WriteString(strings.Join(strings.Fields(values[name]), " "))
		canonical.WriteByte('\n')
	}
	return canonical.String(), strings.Join(names, ";")
}

func canonicalURI(target *url.URL) string {
	path := ""
	if target.Opaque != "" {
		opaque := target.Opaque
		if index := strings.IndexByte(opaque, '?'); index >= 0 {
			opaque = opaque[:index]
		}
		opaque = strings.TrimPrefix(opaque, "//")
		if index := strings.IndexByte(opaque, '/'); index >= 0 {
			path = opaque[index:]
		}
	} else {
		path = target.EscapedPath()
	}
	if path == "" {
		return "/"
	}
	return path
}

func canonicalQuery(values url.Values) string {
	items := make([]string, 0)
	for name, entries := range values {
		if len(entries) == 0 {
			entries = []string{""}
		}
		for _, value := range entries {
			items = append(items, uriEncode(name, true)+"="+uriEncode(value, true))
		}
	}
	sort.Strings(items)
	return strings.Join(items, "&")
}

func canonicalOSSQuery(target *url.URL) string {
	if target == nil || target.RawQuery == "" {
		return ""
	}
	items := make([]string, 0)
	for _, pair := range strings.Split(target.RawQuery, "&") {
		name, value, hasValue := strings.Cut(pair, "=")
		decodedName, err := url.QueryUnescape(name)
		if err != nil {
			decodedName = name
		}
		encodedName := uriEncode(decodedName, true)
		if !hasValue {
			items = append(items, encodedName)
			continue
		}
		decodedValue, err := url.QueryUnescape(value)
		if err != nil {
			decodedValue = value
		}
		items = append(items, encodedName+"="+uriEncode(decodedValue, true))
	}
	sort.Strings(items)
	return strings.Join(items, "&")
}

func canonicalCOSHeaders(request *http.Request) (string, string) {
	values := make(map[string]string)
	for name, entries := range request.Header {
		lower := strings.ToLower(name)
		if lower == "authorization" {
			continue
		}
		values[lower] = strings.Join(entries, ",")
	}
	values["host"] = request.URL.Host
	return canonicalCOSValues(values)
}

func canonicalCOSQuery(query url.Values) (string, string) {
	values := make(map[string]string)
	for name, entries := range query {
		values[strings.ToLower(name)] = strings.Join(entries, ",")
	}
	return canonicalCOSValues(values)
}

func canonicalCOSValues(values map[string]string) (string, string) {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, strings.ToLower(name))
	}
	sort.Strings(names)
	items := make([]string, 0, len(names))
	for _, name := range names {
		items = append(items, uriEncode(name, true)+"="+uriEncode(strings.TrimSpace(values[name]), true))
	}
	return strings.Join(items, "&"), strings.Join(names, ";")
}

func uriEncode(value string, encodeSlash bool) string {
	var builder strings.Builder
	for _, character := range []byte(value) {
		if (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("-_.~", rune(character)) || (character == '/' && !encodeSlash) {
			builder.WriteByte(character)
			continue
		}
		fmt.Fprintf(&builder, "%%%02X", character)
	}
	return builder.String()
}

func hmacBytes(hash func() hash.Hash, key, value []byte) []byte {
	mac := hmac.New(hash, key)
	_, _ = mac.Write(value)
	return mac.Sum(nil)
}

func hmacHex(hash func() hash.Hash, key, value []byte) string {
	return hex.EncodeToString(hmacBytes(hash, key, value))
}

func sha256Hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func sha1Hex(value []byte) string {
	digest := sha1.Sum(value)
	return hex.EncodeToString(digest[:])
}

func normalizedAuthScheme(scheme, fallback string) string {
	if strings.TrimSpace(scheme) == "" {
		return fallback
	}
	return strings.ToLower(strings.TrimSpace(scheme))
}

func secureNonce() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(value)
}

func secureTencentNonce() string {
	return secureTencentASRNonce()
}

func pointerString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

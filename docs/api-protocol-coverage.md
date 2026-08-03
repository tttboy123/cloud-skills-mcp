# Six-cloud API protocol coverage

Date: 2026-08-03
Status: living implementation inventory

This inventory separates resource addressability from protocol-family support.
An API is usable only when its official endpoint, authentication scheme,
request transport, response transport, and safety classification are all
covered. A generic REST gateway alone is not evidence that every provider API
is complete.

## Implemented common transport

| Capability | Current implementation | Bound |
|---|---|---|
| JSON, XML, form, query, and arbitrary HTTPS methods | Exact provider URL plus scalar query, non-credential headers, JSON/string body | 1 MiB inline request; 2 MiB inline provider response |
| Binary/media upload | `body_file` streams a regular file below an approved root | 64 MiB per request; provider multipart/resumable operations compose larger uploads |
| Binary/media download and export | `response_file` streams a successful response to a new mode-0600 file and atomically publishes it | 1 GiB default per request; operator configurable; provider `Range` operations compose larger downloads |
| Error response | Bounded in-memory read with credential-field redaction | No output file is created |
| Redirect | Disabled | Prevents credentials or signatures crossing hosts |

The six object-storage implementations all expose an official HTTP download
body and byte ranges: [AWS S3 GetObject](https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObject.html),
[Azure Get Blob](https://learn.microsoft.com/en-us/rest/api/storageservices/get-blob),
[Google Cloud Storage objects.get](https://docs.cloud.google.com/storage/docs/json_api/v1/objects/get),
[Alibaba OSS GetObject](https://www.alibabacloud.com/help/en/oss/developer-reference/getobject),
[Tencent COS GET Object](https://intl.cloud.tencent.com/document/product/436/7753),
and [Baidu BOS GetObject](https://cloud.baidu.com/doc/BOS/s/xkc5pcmcj).

## Authentication and protocol families

| Provider | Implemented | Still requiring implementation or proof |
|---|---|---|
| AWS | SigV4 and SigV4a header-signed HTTPS; SigV4/SigV4a S3 `aws-chunked` streaming payload signing with optional signed CRC32, CRC32C, CRC64NVME, SHA-1, or SHA-256 trailer; finite and bidirectional HTTP/2 SigV4 EventStream request signing with whole-frame pacing, concurrent length/CRC-validated response streaming, and atomic response-file publication; finite raw-frame SigV4 WSS for official IAM endpoints such as Bedrock AgentCore Runtime and Managed Blockchain JSON-RPC, with mutation-only policy, signed-header containment, bounded text/JSON/Base64 frame plans, and atomic NDJSON; Amazon Connect Health Medical Scribe WSS with exact regional endpoints, six validated session parameters, internal 60-second presigning, chained configuration/raw-audio/`END_OF_SESSION` EventStream frames, normal-close enforcement, mutation-only policy, and atomic transcript NDJSON; standard, Medical, and Call Analytics Transcribe WSS with internal five-minute presigning, optional ConfigurationEvent, chained double-EventStream audio, and bounded JSON event output; AWS IoT MQTT 3.1.1 WSS finite clean-session subscriptions with its special STS presign rule, QoS 0/1, and atomic Base64 NDJSON; AWS AppSync Events IAM WSS with separately signed connect/channel authorization, finite sanitized event collection, and explicit unsubscribe; legacy AWS AppSync GraphQL IAM WSS with separately signed `/graphql/connect` and subscription requests, dynamic auth subprotocol containment, finite sanitized data collection, and explicit stop; AWS SDK default IAM/AKSK/STS chain | WebSocket sessions that require additional product-specific per-frame signing or state machines outside the implemented raw SigV4, Connect Health, Transcribe, IoT MQTT, AppSync Events, and AppSync GraphQL families; service-by-service live vectors |
| Azure | Entra bearer REST through non-CLI service principal, workload identity, or managed identity; public and sovereign endpoint/audience routing; Azure Maps public/geographic endpoint scope; Azure Health Data Services and legacy API for FHIR service audiences plus the shared DICOM audience; Azure OpenAI Realtime GA/preview Entra-authenticated WSS with bounded JSON/NDJSON client events, optional pacing, counted response/transcription terminal events, and atomic NDJSON output; Azure Web PubSub public-cloud standard and reliable JSON WSS with internal Entra-backed client-token minting, exact subprotocol negotiation, uint64 sequence acknowledgement, duplicate suppression, pending publisher resend, bounded recovery, mutation-only policy, and atomic sanitized NDJSON; Web PubSub MQTT 3.1.1/5.0 finite read and bidirectional clients with `clientType=MQTT`, exact-topic publish/subscribe QoS 0/1/2 state machines, duplicate-safe inbound QoS 2, persistent sessions, Last Will, MQTT 5 properties/subscription identifiers/flow control/server disconnect, bounded keepalive, and atomic Base64 NDJSON | Other official long-lived streaming/WebSocket protocols; data planes with no Entra authorization path; sovereign Web PubSub and Azure OpenAI Realtime endpoint proof; Web PubSub standard/reliable protobuf subprotocols; service-by-service live vectors |
| Google Cloud | ADC OAuth bearer REST on `googleapis.com`; public Discovery documents; generic unary/client-streaming/server-streaming/bidirectional gRPC over HTTP/2 using caller-prepared standard framed protobuf messages, validated response frames/trailers, optional per-message pacing, and atomic raw-protobuf output; Speech-to-Text v2 `StreamingRecognize` hermetic vector; Vertex/Gemini Live ADC-authenticated WSS on exact global/regional/multi-region aiplatform endpoints with setup/setupComplete sequencing, bounded official JSON messages, mutation-only policy, and atomic NDJSON | Product-specific WebSocket transports outside Vertex Live; dynamic Vertex Live tool-call and session-resumption workflows; schema-driven protobuf construction/decoding usability beyond the raw generic gRPC transport; service-by-service live vectors |
| Alibaba Cloud | ACS3 OpenAPI; legacy RPC/ROA V2; DataHub `DATAHUB` and OpenSearch V3 `OPENSEARCH` HMAC-SHA1; MaxCompute project/data/Tunnel ODPS V2/V4; classic Function Compute `FC`, current `fcapp.run` Trigger ACS3, and custom-domain Trigger POP HMAC-SHA1; OSS V1/V4 Header authentication; SLS v1/v4; MNS/SMQ; Tablestore OTS v2/v4; NLS short ASR and TTS REST with internal header token and atomic audio output; and NLS `SpeechTranscriber`, `SpeechRecognizer`, `FlowingSpeechSynthesizer`, `SpeechSynthesizer`, and `SpeechLongSynthesizer` WSS with an internally minted/cached RPC V2 token, server-generated task/message IDs, protocol start/stop/terminal enforcement, paced audio/text, and atomic event/audio output; credentials-go RAM/OIDC/ECS/AKSK/STS chain | Product-specific signatures or non-HTTP transports outside implemented families; service-by-service live vectors |
| Tencent Cloud | TC3 API 3.0, API 3.0 v1 HmacSHA1/HmacSHA256 query/form, still-active legacy `*.api.qcloud.com/v2/index.php` HmacSHA1/HmacSHA256 query/form, COS REST signatures, realtime ASR with documented CAM temporary-token signing, virtual-number human detection, SOE evaluation, speech-translation, standard realtime TTS, streaming-text TTS v2, and large-model podcast HMAC-SHA1 WSS, voice-conversion HMAC-SHA1 WSS with framed bidirectional PCM, MPS private-audio TC3 WSS recognition/translation with network-order framing, and MPS TC3 WSS streaming TTS with controlled text segments and atomic binary-audio output; AKSK/CAM credentials within each protocol's documented fields | Product-specific signatures outside implemented families; other remaining long-lived streaming/WebSocket protocols; service-by-service live vectors |
| Baidu AI Cloud | BCE auth v1 and v2 signed HTTPS; AKSK/IAM-STS session token | Product-specific legacy signatures or non-HTTP transports outside BCE v1/v2; service-by-service live vectors |

Credential minting/export and caller-supplied signed URLs, SAS, bearer tokens,
API keys, or Authorization headers remain intentionally outside the public MCP
surface. The only credential entrypoints are operator-controlled AKSK,
temporary AKSK/session tokens, or official IAM identity chains.

Alibaba NLS is a contained protocol dependency, not a public credential-minting
exception: the adapter calls the provider-fixed HTTPS RPC V2 `CreateToken`
operation from the operator's credentials-go chain, validates and caches the
bounded response until near expiry, uses the token only in the validated HTTPS/WSS
transport, and never returns it or permits a caller to invoke `CreateToken`.

OSS POST policy signatures and presigned URL signatures are also intentionally
excluded: both create transferable, time-bounded authorization artifacts for a
different client. All OSS resource operations that do not require delegated
authorization remain callable with the implemented V1/V4 Header signers,
including PutObject and multipart upload operations behind the write gate.

## Explicit unavailable or credential-bound mappings

- Alibaba Cloud Batch Compute is not an active API surface: Alibaba Cloud's
  discontinuation notice states that the service, console, support, and ticket
  service were completely discontinued on May 15, 2026. Its historical
  `batchcompute.*.aliyuncs.com` signer is therefore not exposed as a callable
  MCP scheme; the replacement E-HPC resource OpenAPI remains addressable
  through the general Alibaba Cloud adapter.
- DataWorks DataService published business APIs are application endpoints, not
  DataWorks resource-management APIs. The current official documentation says
  their AppKey/AppSecret or AppCode cannot be replaced by RAM AKSK. They remain
  outside the user-required AKSK/IAM-only credential surface; DataWorks
  platform resources exposed through Alibaba Cloud OpenAPI remain addressable.
- Tencent TRTC's newer realtime-ASR WebSocket is not CAM AKSK-authenticated.
  Its official handshake requires a TRTC `SdkAppId` and `UserSig` derived from
  the TRTC application's SDK secret key. It is credential-bound under the
  AKSK/IAM-only entrypoint contract; the CAM-authenticated ASR WebSocket remains
  implemented separately.
- Baidu RTC large-model interaction control-plane resources are BCE v1 HTTPS
  and remain addressable through the generic adapter. Its interactive
  WebSocket is credential-bound: both direct AK/SK and recommended private
  instance-token modes still require a separately purchased and activated
  `licKey`. The MCP server neither accepts that product credential nor exports
  the 24-hour instance token.

## Completion rule

Do not mark a provider or the overall goal complete from this inventory until:

1. every official public resource API is mapped to an implemented protocol
   family or a documented non-resource/security exclusion;
2. each protocol family has hermetic request/signature/transport vectors;
3. representative control-plane, object/data-plane, upload, download, and
   mutation-gate calls pass against real credentials; and
4. a newly published API can be invoked without adding a resource-specific Go
   handler.

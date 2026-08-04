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
| Redirect | Disabled by default; Firebase SSE has a path/query-preserving same-database 307 rule, ACR GET/HEAD has a provider-owned data-endpoint/Blob 307 rule, and private ECR layer GET/HEAD has an exact regional Starport S3 307 rule; Authorization is stripped before data-plane hops | Prevents credentials or signatures crossing unvalidated hosts |

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
| AWS | SigV4 and SigV4a header-signed HTTPS; SigV4/SigV4a S3 `aws-chunked` streaming payload signing with optional signed CRC32, CRC32C, CRC64NVME, SHA-1, or SHA-256 trailer; finite and bidirectional HTTP/2 SigV4 EventStream request signing with whole-frame pacing, concurrent length/CRC-validated response streaming, and atomic response-file publication; finite raw-frame SigV4 WSS for official IAM endpoints such as Bedrock AgentCore Runtime and Managed Blockchain JSON-RPC, with mutation-only policy, signed-header containment, bounded text/JSON/Base64 frame plans, and atomic NDJSON; Amazon Connect Health Medical Scribe WSS with exact regional endpoints, six validated session parameters, internal 60-second presigning, chained configuration/raw-audio/`END_OF_SESSION` EventStream frames, normal-close enforcement, mutation-only policy, and atomic transcript NDJSON; standard, Medical, and Call Analytics Transcribe WSS with internal five-minute presigning, optional ConfigurationEvent, chained double-EventStream audio, and bounded JSON event output; AWS IoT MQTT 3.1.1/5.0 WSS read-only subscriptions and mutation-only bidirectional clients with its special STS presign rule, QoS 0/1 subscribe/unsubscribe/publish/PUBACK, persistent sessions, retained/Last Will, supported v5 application/session properties, strict broker capabilities/reason codes, 128 KiB packet and receive bounds, server keepalive, and atomic Base64 NDJSON; Kinesis Video Streams WebRTC Signaling Master/Viewer WSS with official 299-second SigV4 query signing, STS canonical-query inclusion, bounded role-aware SDP/ICE messaging, quota pacing, status correlation, and atomic decoded NDJSON; AWS AppSync Events IAM WSS with separately signed connect/channel authorization, finite sanitized event collection, and explicit unsubscribe; legacy AWS AppSync GraphQL IAM WSS with separately signed `/graphql/connect` and subscription requests, dynamic auth subprotocol containment, finite sanitized data collection, and explicit stop; Amazon IVS Chat messaging WSS with internal SigV4 `CreateChatToken`, least-capability single-use token containment, read/send/sensitive-moderation separation, quota pacing, and atomic sanitized Message/Event output; Amazon Lex V2 TEXT/AUDIO/DTMF `StartConversation` bidirectional HTTP/2 SigV4 EventStream with exact official URI/bot/alias/locale/session validation, internal per-frame stream signing, configuration-then-audio/DTMF/text event state machine, bounded sanitized TextResponse/Transcript/Heartbeat/IntentResult/AudioResponse/PlaybackInterruption output, and graceful EOF/timeout publication; Amazon Chime SDK Messaging WebSocket event streaming with internal SigV4 `GetMessagingSessionEndpoint`, exact official `data-messaging.chime.aws` host validation, SigV4-presigned `/connect` with bounded expiry and official AppInstanceUser ARN/session ID query claims, read-only subscription, strict documented event-type/message-type validation, decoded bounded `Payload` JSON, and atomic sanitized NDJSON; AWS SDK default IAM/AKSK/STS chain; Amazon Connect chat participant WebSocket with internal SigV4 `StartChatContact` and bearer-only `CreateParticipantConnection`, exact `participant.connect.<region>.amazonaws.com` host and `/participant/connect` path validation, official `aws/subscribe`/`aws/chat` topic framing, mutation-only policy, bounded participant `SendMessage` REST sends with internal ConnectionToken and generated idempotent `ClientToken` containment, bounded sanitized event output, and no exposed participant/connection token or dial URL | WebSocket or streaming sessions that require additional product-specific per-frame signing or state machines outside the implemented raw SigV4, Connect Health, Transcribe, IoT MQTT, Kinesis Video Signaling, AppSync Events, AppSync GraphQL, IVS Chat, Lex V2 conversation, and Chime SDK Messaging families; MSK/ElastiCache/MemoryDB Kafka or RESP client protocols and MediaConnect media inputs are documented non-resource client/media-plane exclusions; Kinesis Video Streams RTMP/HLS media ingest is a documented non-resource media plane; Amazon MQ ActiveMQ/RabbitMQ broker wire protocols are documented non-resource client line protocols; MediaLive RTMP/RTP channel inputs are a documented non-resource media plane; IVS real-time Stage WebRTC/RTMPS media is a documented non-resource media plane; service-by-service live vectors |
| Azure | Entra bearer REST through non-CLI service principal, workload identity, or managed identity; public and sovereign endpoint/audience routing; Azure Maps public/geographic endpoint scope; Azure Health Data Services and legacy API for FHIR service audiences plus the shared DICOM audience; public-cloud Azure OpenAI Realtime GA/preview Entra-authenticated WSS on the exact single-label `.openai.azure.com` resource host with bounded JSON/NDJSON client events, optional pacing, counted response/transcription terminal events, and atomic NDJSON output; public-cloud Azure OpenAI Chat Completions streaming `text/event-stream` on the exact single-label `.openai.azure.com` resource host with an internal `https://cognitiveservices.azure.com/.default` Entra token, internally injected `stream:true`, date-structured `api-version`, bounded model/messages/max_tokens/temperature plans, strict `chat.completion.chunk` shape validation with `[DONE]` termination and event/time bounds, and atomic sanitized NDJSON; public-cloud Azure OpenAI Responses streaming `text/event-stream` on the exact single-label `.openai.azure.com` resource host and official `/openai/v1/responses` path with the REST-reference `https://cognitiveservices.azure.com/.default` Entra token, optional documented `api-version` (`v1`, `preview`, date-structured, or omitted for the v1 GA default), internally injected `stream:true` and `store:false`, bounded model/input/max_output_tokens/temperature plans, strict documented `ResponseStreamEvent` type/sequence/per-type validation with `response.completed`/`response.incomplete` termination, fail-closed `error`/`response.failed`, and atomic sanitized NDJSON; public-cloud Azure Voice Live Foundry/current and legacy Speech WSS on exact single-label resource hosts with endpoint-selected Entra scope, model read sessions, mutation-gated Agent sessions, finite response/transcription/session intents, credential-free event validation, and atomic output; Azure Web PubSub public-cloud standard and reliable JSON/Protobuf WSS with internal Entra-backed client-token minting, exact four-subprotocol negotiation, official proto3 binary framing, JSON-shaped schema usability, text/binary/Any and streaming messages, uint64 sequence acknowledgement, duplicate suppression, pending publisher resend, bounded recovery, mutation-only policy, and atomic sanitized NDJSON; Web PubSub MQTT 3.1.1/5.0 finite read and bidirectional clients with `clientType=MQTT`, exact-topic publish/subscribe QoS 0/1/2 state machines, duplicate-safe inbound QoS 2, persistent sessions, Last Will, MQTT 5 properties/subscription identifiers/flow control/server disconnect, bounded keepalive, and atomic Base64 NDJSON; Event Grid Namespace MQTT v5 WSS with direct Entra `OAUTH2-JWT` CONNECT/AUTH, QoS 0/1, retained/Last Will, user/request-response properties, expiry/topic alias/flow control, assigned IDs, wildcard/shared subscriptions, subscription IDs, bounded keepalive, and atomic Base64 NDJSON; Azure SignalR Service public-cloud serverless JSON-hub WSS with internal Entra `:generateToken` (official `api-version=2022-11-01`), exact single-label `<resource>.service.signalr.net` host and swagger-validated hub names, in-band JSON Hub Protocol handshake/`0x1E` framing, read-only Subscribe listener with automatic void Completion replies, mutation-only Invoke with correlated StreamItem/Completion terminal state, bounded `minutes_to_expire`/message/timeout, and atomic sanitized NDJSON; Service Bus AMQP 1.0 over exact public, US Government, or China WSS with official SDK SASL/CBS, internal Entra refresh, queue/topic/subscription send, peek, receive-and-in-call-settlement, deferred, schedule/cancel, subqueues and session state without exported locks; Event Hubs AMQP 1.0 over the same three active-cloud endpoint forms with internal Entra/CBS, hub/partition properties, finite partition reads from five start-position forms, and partition-ID/key batch sends | Other official long-lived streaming/WebSocket protocols outside the implemented families; data planes with no Entra authorization path; sovereign Web PubSub/Event Grid MQTT and sovereign SignalR; sovereign Azure OpenAI Chat Completions streaming; sovereign Azure OpenAI Responses streaming; Voice Live avatar WebRTC media plane (credential-bearing, non-resource transport); Event Hubs Kafka and IoT Hub device planes are documented credential-bound mappings, and Speech TTS WebSocket V2 raw WSS is a documented withdrawn SDK-only mapping; Azure Media Services is retired (2024-06-30) and the deprecated Assistants API (retired 2026-08-26) is a documented unavailable mapping replaced by the implemented Responses surface; Azure Cache for Redis RESP and Azure Relay Hybrid Connection SAS-token WebSocket are documented non-resource/credential-bound client transports; Azure Database for PostgreSQL/MySQL wire protocols are documented non-resource client line protocols; Communication Services real-time calling RTP/WebRTC media is a documented non-resource media plane; service-by-service live vectors |
| Google Cloud | ADC OAuth bearer REST on `googleapis.com`; Artifact Registry and Artifact Registry-backed `gcr.io` OCI Distribution 1.1 HTTP with exact host/path validation, direct internal ADC Bearer, manifests/blobs/tags/referrers/mounts, monolithic uploads and chunked-upload rejection; public Discovery documents; generic unary/client-streaming/server-streaming/bidirectional gRPC over HTTP/2 in both caller-prepared raw framed-protobuf mode and `FileDescriptorSet`-driven ProtoJSON mode; exact URL method/type resolution, strict request JSON, deterministic protobuf frames, bounded message pacing, recursive unknown-wire rejection including `google.protobuf.Any`, official 64-bit-safe ProtoJSON mapping, validated response frames/trailers, and atomic raw-protobuf or NDJSON output; descriptor-proven server streams require finite message/time bounds, including exact read-only Firestore Listen and Logging TailLogEntries mappings, while Pub/Sub StreamingPull stays mutation-gated for acknowledgements; a Speech-to-Text v2 `StreamingRecognize` raw hermetic vector plus generic schema-driven four-shape vectors; Vertex/Gemini Live ADC-authenticated WSS on exact global/regional/multi-region aiplatform endpoints with setup/setupComplete sequencing, bounded official JSON messages, deterministic bounded tool-call dispatch with exact provider ID matching, internal-only transparent session handles, acknowledged-message pruning, bounded GoAway/transport reconnect, mutation-only policy, sanitized transcripts, and atomic NDJSON; Firebase Realtime Database SSE on exact official database URL forms with its documented two ADC OAuth scopes, bounded read-only filter/query plans, manual path/query-preserving 307 handling, strict `put`/`patch`/optional keep-alive parsing, credential-data rejection, and atomic NDJSON | Product-specific WebSocket or streaming transports outside Vertex Live and Firebase SSE; Google Cloud IoT Core is retired (2023-08-16) with no active Device Manager API; Memorystore for Redis RESP and Managed Service for Apache Kafka are documented non-resource client line protocols; Cloud SQL MySQL/PostgreSQL/SQL Server wire protocols are documented non-resource client line protocols; service-by-service live vectors |
| Alibaba Cloud | ACS3 OpenAPI; legacy RPC/ROA V2; DataHub `DATAHUB` and OpenSearch V3 `OPENSEARCH` HMAC-SHA1; MaxCompute project/data/Tunnel ODPS V2/V4; classic Function Compute `FC`, current `fcapp.run` Trigger ACS3, and custom-domain Trigger POP HMAC-SHA1; OSS V1/V4 Header authentication; SLS v1/v4; MNS/SMQ; RocketMQ 4.x `MQ` HMAC-SHA1/XML HTTPS publish, normal/order/transaction-half consume, and in-call acknowledge/commit/rollback with internal-only receipt handles and atomic sanitized NDJSON; ACR Enterprise Docker/OCI Registry V2 with internal RAM `GetAuthorizationToken`, exact Bearer challenge/scope binding, same-region OSS blob redirect containment, and Personal Edition credential-bound exclusion; Tablestore OTS v2/v4; NLS short ASR and TTS REST with internal header token and atomic audio output; and NLS `SpeechTranscriber`, `SpeechRecognizer`, `FlowingSpeechSynthesizer`, `SpeechSynthesizer`, and `SpeechLongSynthesizer` WSS with an internally minted/cached RPC V2 token, server-generated task/message IDs, protocol start/stop/terminal enforcement, paced audio/text, and atomic event/audio output; credentials-go RAM/OIDC/ECS/AKSK/STS chain. RocketMQ 5.x control plane remains ACS3-addressable; its public data plane is an explicit credential-bound exclusion because it requires instance ACL username/password rather than the Goal's RAM AKSK/IAM entrypoint | Product-specific signatures or non-HTTP transports outside implemented families; Alibaba Cloud IoT Platform device MQTT is a documented device-credential-bound exclusion; ApsaraMQ for Kafka is a documented non-resource Kafka client line protocol and ApsaraVideo Live ingest is a documented non-resource media plane; ApsaraMQ for MQTT is a documented device-credential-bound MQTT client transport; ApsaraDB for Redis/Tair RESP and ApsaraMQ for RabbitMQ AMQP are documented non-resource client line protocols; service-by-service live vectors |
| Tencent Cloud | TC3 API 3.0, API 3.0 v1 HmacSHA1/HmacSHA256 query/form, still-active legacy `*.api.qcloud.com/v2/index.php` HmacSHA1/HmacSHA256 query/form, COS REST signatures, CLS legacy `q-sign-algorithm=sha1` HTTPS with exact public/internal regional endpoints and internal CAM `x-cls-token`, TCR Enterprise Docker/OCI Registry V2 with fixed internal TC3 `CreateInstanceToken(TokenType=temp)`, exact public/VPC/operator-custom endpoints, temporary Basic containment, manifests/blobs/tags/referrers/uploads/mounts, redirect rejection, and Personal Edition credential-bound exclusion, realtime ASR with documented CAM temporary-token signing, virtual-number human detection, SOE evaluation, speech-translation, standard realtime TTS, streaming-text TTS v2, and large-model podcast HMAC-SHA1 WSS, voice-conversion HMAC-SHA1 WSS with framed bidirectional PCM, MPS private-audio TC3 WSS recognition/translation with network-order framing, and MPS TC3 WSS streaming TTS with controlled text segments and atomic binary-audio output; AKSK/CAM credentials within each protocol's documented fields | Product-specific signatures outside implemented families; other remaining long-lived streaming/WebSocket protocols; Tencent Cloud IoT Explorer device MQTT is a documented device-credential-bound exclusion; CKafka is a documented non-resource Kafka client line protocol, CSS ingest/playback is a documented non-resource media plane, and IM is a documented UserSig credential-bound surface; TDMQ for Apache Pulsar (Pulsar binary) and TDMQ for RabbitMQ (AMQP 0-9-1) are documented non-resource client line protocols; TencentDB for Redis RESP is a documented non-resource client line protocol; service-by-service live vectors |
| Baidu AI Cloud | BCE auth v1 and v2 signed HTTPS; AKSK/IAM-STS session token; CCR Enterprise and Personal Docker/OCI Registry V2 with fixed internal BCE v1 user/one-hour credential exchange, exact public/VPC/operator-custom endpoint validation, same-origin Bearer challenge binding, manifests/blobs/tags/referrers/uploads/mounts, redirect rejection, and internal temporary credential containment; IoT Core HTTP Publish through fixed internal `/auth` plus `/pub`, and MQTT 3.1.1/5.0 over exact WSS with internal IAM application-permission HMAC, 100 wildcard/shared subscriptions in eight-entry batches, bounded/rate-paced QoS 0/1/2 publish, duplicate-safe bidirectional QoS 2, MQTT 5 application properties/Will delay/Receive Maximum/server disconnect, Will/keepalive, 32–128 KiB instance payload bounds, atomic output, and no exported derived credential; RTC AI Agent BCE v1 create/private-instance-token WSS/stop lifecycle with exact official endpoints, operator-only license activation, all six documented upload codecs (`raw`, `raw16k`, `pcma`, `pcmu`, `g722`, `opus`), control/WSS codec agreement, 20–200 ms fixed-rate framing, variable-length Opus packet plans with official `ptime`/`plen`, strict credential-free parsing of the documented static break/text/TTS/device/GIS/player/ASR/prompt/variable/role/query/MCP/direct-control/meeting commands before or after audio, provider-event-correlated single-image upload with approved-root file validation and official 16 KiB/Base64 frame sequencing, current-format Function Call parsing with provider-session correlation to bounded credential-free `ok|error`/`post_function` templates, duplicate/unknown/call-limit rejection, serialized concurrent writes, bounded text/binary streaming, internal token containment, mutation-only policy, and atomic sanitized NDJSON | Product-specific legacy signatures or other long-lived transports outside BCE v1/v2, CCR, IoT Core HTTP/MQTT, and RTC AI Agent; Message Service for Kafka is a documented non-resource Kafka client line protocol; Message Service for RabbitMQ AMQP is a documented non-resource client line protocol; Cloud Database SCS (Redis) RESP is a documented non-resource client line protocol; service-by-service live vectors |

Amazon ECR private and public Docker/OCI Registry HTTP is a first-class
exception to generic AWS SigV4 request signing. `auth_scheme=ecr` derives
GetAuthorizationToken through the AWS SDK identity chain and a provider-fixed
SigV4 JSON request, keeps the base64 `AWS:password` material internal, and
applies the documented private Basic or public Bearer Registry header. The
contract binds private classic/FIPS/dual-stack and China endpoints to exact
account/region/service values, binds Public classic/dual-stack to
`ecr-public`/`us-east-1`, rejects the Public tags API, and maps Registry
version, catalog, manifest, blob, upload, tags, and referrers paths to their
valid methods. Private layer GET/HEAD follows at most three 307 responses only
to the exact same-region Starport S3 bucket with Authorization removed and
signed Location values contained. Private and Public live probes remain
pending operator IAM credentials and existing repositories.

Amazon Chime SDK Messaging event streaming is a first-class exception to the
generic AWS SigV4 WSS family. `auth_scheme=chime-messaging-ws` signs the fixed
`GET https://messaging-chime.<region>.amazonaws.com/endpoints/messaging-session`
request with the `chime` signing name, rejects any returned endpoint other than
the official `wss://data-messaging.chime.aws` host, and then SigV4-presigns
`GET /connect` with a bounded `X-Amz-Expires`, the official AppInstanceUser ARN
as `userArn`, a unique `sessionId`, optional `prefetch-on=connect`, and the STS
token only inside the canonical query. The signed URL and STS material never
leave the adapter. Read-only `SubscribeMessages` validates every event against
the documented `x-amz-chime-event-type` table and `STANDARD|CONTROL|SYSTEM`
message types, decodes bounded `Payload` JSON strings, and atomically publishes
sanitized NDJSON. The signature is verified against the AWS SDK Go v2
presigner with the same request vector; a dedicated read-only live gate remains
pending an operator IAM identity with `chime:GetMessagingSessionEndpoint` and
`chime:Connect` on an existing AppInstanceUser.

Google Artifact Registry Docker/OCI HTTP is a first-class exception to the
generic `googleapis.com` REST allowlist. `auth_scheme=artifact-registry` uses
the official ADC OAuth token directly as a Registry Bearer while accepting
only an exact `<location>-docker.pkg.dev` host or the current `gcr.io`,
`us.gcr.io`, `eu.gcr.io`, and `asia.gcr.io` compatibility hosts. It binds
project/repository/image namespaces to OCI version, manifest, blob, tags,
referrers, cross-repository mount, and monolithic upload methods before ADC is
resolved. A provider `202` fallback is completed with a full-body PUT to an
exact same-origin internal Location that never leaves the server; the internal
`/v2/token` endpoint, unscoped catalog, caller
credentials, lookalike hosts, redirects, and Docker chunked `PATCH` uploads
fail closed. A dedicated ADC-backed live ListTags probe remains pending an
operator-provided existing repository.

Azure Container Registry is a first-class exception to generic Entra bearer
REST. `auth_scheme=acr` obtains the official Container Registry Entra audience,
exchanges it internally for an ACR refresh token and then a path-matched scoped
access token, and uses that token for Docker `/v2` and ACR `/acr/v1` data-plane
operations. Registry catalog and deleted-catalog scopes, Docker/OCI content
pull/push/delete scopes, ACR metadata read/write/delete scopes, and soft-delete
read/restore scopes are each bound to their documented path and HTTP method;
cross-repository blob mount adds an exact source pull scope as the second OAuth
scope. Exact public/regional login hosts, public-name Private
Endpoint DNS, internal-only token responses, and bounded GET/HEAD 307 handling
to the same registry's dedicated data endpoint or Azure Blob are covered.

Alibaba Cloud ACR Enterprise Edition is a first-class exception to generic
ACS3/RPC resource calls. `auth_scheme=acr-registry` signs the fixed
`cr/2018-12-01 GetAuthorizationToken` RPC from credentials-go RAM AKSK/STS,
keeps the temporary username/password internal, validates the Registry's exact
public, VPC, operator-pinned custom, or same-Registry takeover Bearer realm and instance-bound service, then asks
only for path/method-derived target and optional source repository scopes.
Registry version/catalog, manifest, blob, tags, referrers, uploads and mounts
are covered. Blob GET/HEAD follows at most three 307 responses only to an exact
same-region OSS bucket with Authorization removed and the signed Location
contained. A dedicated read-only ListTags live gate remains pending operator
RAM credentials and an existing Enterprise repository. Personal Edition is an
explicit credential-bound mapping because its current official contract does
not support `GetAuthorizationToken` and requires a fixed Registry password.

Tencent Cloud TCR Enterprise Edition is a first-class exception to generic
TC3 resource calls. `auth_scheme=tcr-registry` resolves CAM AKSK/STS only after
validating an exact public, VPC, or operator-pinned custom Registry endpoint,
then sends a fixed internal TC3 `CreateInstanceToken` request with
`TokenType=temp`. The returned temporary username/JWT is used only as Registry
Basic authorization and is never exposed; a non-empty `TokenId`, an expired or
malformed credential, and every redirect fail closed. Registry version,
catalog, manifest, blob, tags, referrers, monolithic/chunked uploads and
cross-repository mounts are path/method validated before credentials are
resolved. A dedicated read-only ListTags live gate remains pending operator
CAM credentials, an Enterprise instance ID, and an existing repository.
Personal Edition is credential-bound because its official login contract uses
a separately configured Registry username/password rather than the Goal's CAM
AKSK/IAM entrypoint.

Baidu CCR Enterprise and Personal Edition are first-class exceptions to
generic BCE resource signing. `auth_scheme=ccr-registry` validates an exact
official Registry origin, namespaced repository, path and method before the
BCE AKSK/IAM-STS chain is resolved. Enterprise uses fixed signed user-profile
and one-hour instance-credential calls; Personal uses fixed signed current-user
and one-hour token calls. The resulting login is used only for an exact
same-origin HTTPS `/service/token` challenge whose service is
`harbor-registry`, then exchanged for path-derived catalog or repository
scopes. Registry version, catalog, manifests, blobs, tags, referrers,
monolithic/chunked uploads and cross-repository mounts are covered. Temporary
usernames/passwords, Basic and Bearer values remain internal, and redirects
fail closed until an official safe blob-redirect contract is available. A
dedicated read-only ListTags live gate remains pending operator BCE identity
and an existing Enterprise or Personal repository.

Baidu IoT Core MQTT 3.1.1/5.0 over WSS is a first-class data-plane protocol.
`auth_scheme=iotcore-mqtt-ws` accepts only the exact single-instance
`wss://<iot-core-id>.iot.gz.baidubce.com/mqtt` endpoint, subprotocol `mqtt`,
and a credential-free bounded client plan. The server derives the documented
application-permission CONNECT username/password from BCE IAM AK/SK, with the
official example vector enforced by a hermetic test; it never exposes those
derived values. SubscribeMQTT supports up to the documented 100 QoS 0/1/2
wildcard or `$share/<group>/<filter>` subscriptions per connection, internally
batching eight per SUBSCRIBE request, plus mutation-gated bounded UNSUBSCRIBE
batches with MQTT-version-aware UNSUBACK reason validation, bounded observation and atomic
NDJSON output. ClientMQTT mutation-gates publish, persistent-session and Will
behavior. Both protocol versions implement QoS 2 PUBREC/PUBREL/PUBCOMP in both
directions with duplicate-safe delivery. MQTT 5 additionally covers reason
codes including SUBACK/UNSUBACK, server DISCONNECT, application properties, message expiry, Will delay,
and Receive Maximum. Unsupported properties/topic aliases, oversized
topic/message plans, arbitrary frames, credential fields, redirects, query
parameters, and lookalike hosts fail closed. The
default 32 KiB payload bound can be explicitly raised only up to the documented
128 KiB instance maximum, is enforced on both published and received data, and
outgoing messages are paced to the documented QoS-specific publish rates.
The official application-permission contract has no STS session-token field, so
this scheme rejects one rather than dropping it. A dedicated live read gate
remains pending operator IAM application binding and a staged topic message.

Baidu IoT Core HTTP Publish is a separate mutation-only
`auth_scheme=iotcore-http-pub` direct-HTTPS protocol. It accepts only exact
single-instance `POST /pub` targets and a credential-free topic/QoS/payload
plan. The server derives the same IAM application username/password, exchanges
it at fixed same-origin `/auth` for a 60-second internal token, and publishes
one raw payload through `/pub?topic=...&qos=0|1`. The default 32 KiB and
explicit 128 KiB approved-instance bounds, topic rules, documented 50 QPS/IP
limit, success response, mutation gate, and credential containment are
enforced. A dedicated opt-in mutation live gate remains pending operator IAM
application binding and a disposable topic.

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

Azure Web PubSub standard/reliable JSON/Protobuf and MQTT clients follow the
official Private Endpoint contract: callers always use the unchanged
`<resource>.webpubsub.azure.com` URL and VNet DNS resolves it to the private
address. Direct `privatelink.webpubsub.azure.com` or nested `privatelink`
subdomain URLs are rejected before identity or client-token resolution.

Azure SignalR Service serverless JSON-hub WSS follows the official client
negotiation contract: the gateway requests `https://signalr.azure.com/.default`,
calls `POST /api/hubs/{hub}/:generateToken?api-version=2022-11-01` with bounded
`minutesToExpire` and optional `userId`, and dials the exact public
`<resource>.service.signalr.net/client/?hub=<hub>` URL with the client token
only in the internal `access_token` query parameter. The hub name follows the
data-plane swagger contract (alphabetic start, alphanumeric or underscore
only); `privatelink` labels, nested subdomains, numeric-first resources,
lookalike hosts, sovereign guesses, caller headers, and caller query parameters
fail before token resolution. The in-band JSON Hub Protocol handshake, `0x1E`
record-separator framing, Ping/Close handling, and Completion error rules come
from the official Hub Protocol specification; unsupported message types and
empty/invalid/oversized frames fail closed. Read-only `Subscribe` auto-replies
void Completion frames for server-to-client Invocations; mutation-only `Invoke`
correlates StreamItem/Completion frames to unique bounded invocation IDs and
fails atomically on error or missing terminal Completions. The Entra token,
client token, and dial URL never enter MCP output.

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
- Baidu realtime ASR at `wss://vop.baidu.com/realtime_asr` requires the AI
  application's `appid` plus `appkey`, not BCE AKSK/IAM. Baidu's end-to-end
  realtime speech and voice-clone streaming TTS likewise document only API Key
  or OAuth `access_token`. These application-key/token transports remain
  outside the operator-required AKSK/IAM-only credential entrypoint; their BCE
  resource-management APIs remain addressable through signed HTTPS.
- Microsoft Cloud Germany shut down in 2021. Its historical
  `*.servicebus.cloudapi.de` endpoint is not an active Azure messaging surface
  and is rejected by the AMQP WSS adapter.
- Azure Government currently marks Web PubSub as not GA/pending review, and the
  current Azure China documentation does not publish a Web PubSub data-plane
  endpoint contract. Event Grid's current MQTT namespace region table lists
  public Azure regions but does not document a sovereign MQTT endpoint. These
  transports remain public-cloud only pending explicit product documentation;
  generic Event Grid or Azure service availability is not treated as proof.
- Azure SignalR Service remains public-cloud only in this adapter. The current
  English client-negotiation and data-plane documentation publishes the
  `https://signalr.azure.com/.default` scope and the public
  `<resource>.service.signalr.net/client/?hub=<hub>` client endpoint; sovereign
  host guesses, `privatelink` labels, nested subdomains, numeric-first
  resources, and lookalikes fail before identity or client-token resolution.
  Sovereign SignalR data-plane endpoint contracts are not implemented and are
  recorded as an unavailable mapping rather than a guessed endpoint.
- Azure OpenAI Realtime's current official contract documents the public
  `<resource>.openai.azure.com` WSS endpoint and Global deployments in public
  Azure regions. The Azure Government model page states that its list includes
  all Azure OpenAI models offered there and lists no Realtime model; Microsoft
  publishes no Azure China Foundry Realtime endpoint contract. The adapter
  therefore accepts one public resource label only and rejects guessed
  `.azure.us`, `.azure.cn`, nested, direct private-DNS-zone, or lookalike hosts.
  This is a documented unavailable mapping, not an outstanding implementation
  placeholder; it can be revisited when Microsoft publishes a sovereign
  product contract.
- Azure OpenAI Chat Completions streaming follows the same public-cloud rule:
  the current REST reference publishes `https://{resource}.openai.azure.com`
  and date-structured `api-version` values, and no sovereign data-plane
  Chat Completions endpoint contract is implemented. The adapter accepts the
  exact public single-label host and rejects `.azure.us`, `.azure.cn`,
  nested, direct private-DNS-zone, and lookalike hosts before token
  resolution, keeping the documented public-cloud exclusion explicit.
- AWS Managed Streaming for Apache Kafka (MSK) exposes a Kafka wire data
  plane: clients authenticate with IAM, SASL/SCRAM, or mTLS and speak the
  Kafka binary protocol, which is a custom client line protocol rather than an
  AWS resource API. The MSK control plane (`CreateCluster`,
  `DescribeCluster`, `ListClusters`, and related operations) remains
  addressable through the generic SigV4 adapter; the Kafka client protocol is
  recorded as a non-resource client line protocol outside the MCP
  resource-API surface.
- AWS ElastiCache and MemoryDB expose a ValKey/Redis RESP data plane: clients
  authenticate with a RESP AUTH token or IAM auth through a Redis-compatible
  client library. RESP is a custom client protocol, not an AWS resource API;
  both control planes remain SigV4-addressable through the generic adapter.
- AWS Elemental MediaConnect flow inputs are the media transmission plane:
  the official source-ports contract publishes Zixi push (port 2088, VPC
  range 2090–2099), SRT, RTP, and RIST over UDP, which are inbound media
  streams rather than resource-management APIs. MediaConnect resource APIs
  (`CreateFlow`, `DescribeFlow`, and related operations) remain
  SigV4-addressable; the gateway does not implement media ingest.
- Azure Event Hubs Kafka endpoint: Kafka clients must use SASL/PLAIN with
  username `$ConnectionString` and the Event Hubs connection string (SAS key
  material) as the password, or Entra OAuth inside the Kafka client. The
  Kafka binary protocol is a custom client line protocol; Event Hubs remains
  addressable through the implemented AMQP 1.0 WSS family and Entra REST.
- Azure IoT Hub device plane: devices authenticate with per-device SAS tokens
  derived from the device symmetric key or with X.509 client certificates,
  not Entra bearer/operator IAM. IoT Hub resource management and the
  implemented messaging surfaces remain addressable; the device-plane
  MQTT/AMQP/HTTPS surface is a credential-bound device identity transport
  outside the operator-IAM-only boundary.
- Azure Speech TTS WebSocket V2 raw client streaming is withdrawn: Microsoft
  states that TTS WebSocket V2 input-text streaming cannot be used through a
  raw WebSocket and is available only through the Speech SDK, which owns the
  connection and message framing internally. The raw WSS transport is an
  unavailable mapping rather than an implementation placeholder; Speech REST
  and the implemented Voice Live WSS remain addressable.
- Google Cloud IoT Core is retired: the service was discontinued on
  2023-08-16 and the Device Manager APIs are no longer available, so there is
  no active resource API to implement. The mapping is recorded as retired
  rather than pending.
- Alibaba Cloud IoT Platform device plane: devices connect over MQTT-TLS with
  One-Device-One-Secret pre-registration (ProductKey, DeviceName,
  DeviceSecret) or device certificates; dynamic registration derives the
  DeviceSecret from the device itself. This is device-identity credential
  material, not RAM AKSK. The platform control plane remains ACS3/RPC
  addressable and the device MQTT data plane is a documented credential-bound
  device surface.
- AWS Kinesis Video Streams media ingest and playback are the media plane:
  the official contract ingests live video over RTMP (or the WebRTC media
  path for which this project already implements the IAM-signaled control
  plane) and serves HLS playback URLs. The media frames themselves are
  streaming media, not resource-management APIs; Kinesis Video Streams
  control-plane APIs remain SigV4-addressable and WebRTC signaling is
  implemented, while raw media transport is recorded as a non-resource media
  plane.
- AWS Amazon MQ brokers speak the ActiveMQ and RabbitMQ wire protocols
  (AMQP, OpenWire, STOMP, MQTT, WebSocket, and AMQP 0-9-1) authenticated with
  broker user or LDAP credentials rather than the AWS identity chain. The
  broker data plane is a non-resource client line protocol; the Amazon MQ
  REST control plane (`CreateBroker`, `DescribeBroker`, and related
  operations) remains SigV4-addressable.
- AWS Elemental MediaLive channel inputs are the media plane: the official
  input-class contract documents RTMP push/pull, RTP, and transport-stream
  sources. Live media transport is a non-resource media plane; the MediaLive
  control plane remains SigV4-addressable.
- Amazon IVS real-time Stage publish/subscribe is the WebRTC/RTMPS media
  plane, while the IVS Chat messaging WebSocket already implemented here stays
  separate; Stage media frames are a non-resource media transport and IVS
  control-plane APIs remain SigV4-addressable.
- Amazon Connect chat realtime transport is now implemented by the
  `connect-chat-ws` scheme instead of an exclusion: the gateway signs
  `StartChatContact` internally with SigV4, calls
  `CreateParticipantConnection` with the participant token only in the
  internal `X-Amz-Bearer` header, validates the returned participant WSS
  endpoint, subscribes to the official `aws/chat` topic, and keeps the
  participant token, connection token, and dial URL internal. The Connect and
  Connect Participant control planes remain SigV4-addressable.
- Azure Media Services is retired: the service was announced in June 2023
  and retired on 2024-06-30, so its v3 resource APIs and media pipelines no
  longer exist as an active public surface. The mapping is recorded as
  retired rather than pending.
- Azure OpenAI Assistants API is deprecated and will be retired on
  2026-08-26; Microsoft directs existing workloads to the GA Microsoft Foundry
  Agents service and the Responses API. The deprecated Assistants streaming
  route is therefore an unavailable mapping, and the implemented
  `openai-responses-stream` family covers the replacement surface.
- Azure Cache for Redis speaks the ValKey/Redis RESP data plane: clients
  authenticate with cache access keys or an in-client Microsoft Entra token.
  RESP is a custom client protocol, not an Azure resource API; the cache
  control plane remains addressable through Entra REST.
- Azure Relay Hybrid Connections are client WebSocket/SBMP tunnels
  authenticated with SAS tokens from a relay policy; the relay data plane is
  a credential-bound client transport, while Relay control-plane resources
  remain addressable through Entra REST.
- Azure Database for PostgreSQL and MySQL flexible servers speak the
  PostgreSQL/MySQL wire protocols with password or in-client Microsoft Entra
  token authentication. Those are client line protocols, not Azure resource
  APIs; the server control plane remains addressable through Entra REST.
- Azure Communication Services real-time calling is the media plane: the
  official call-flow contract transmits audio, video, and screen sharing over
  RTP/WebRTC after SDK signaling. ACS REST surfaces (Chat, Rooms, Job Router,
  Email, SMS, and others) remain addressable through generic Entra REST.
- Google Cloud Memorystore for Redis exposes a ValKey/Redis RESP data plane:
  clients authenticate with OSS Redis AUTH (enabled per instance) or IAM
  authentication through a Redis-compatible client library. RESP is a custom
  client protocol, not a Google Cloud resource API; the Cloud Redis/Memorystore
  control-plane APIs remain addressable through generic ADC REST.
- Google Cloud Cloud SQL instances speak the MySQL, PostgreSQL, and SQL
  Server wire protocols with Cloud SQL connectors or SSL/TLS client
  certificates. Those are client line protocols, not Google Cloud resource
  APIs; the Cloud SQL Admin control-plane API remains addressable through
  generic ADC REST.
- Google Cloud Managed Service for Apache Kafka exposes the Kafka wire data
  plane: clients authenticate with SASL/OAUTHBEARER over ADC or SASL/PLAIN
  and speak the Kafka binary protocol. The Kafka client protocol is a
  non-resource client line protocol; the `managedkafka` control-plane API
  remains addressable through generic ADC REST.
- Alibaba Cloud ApsaraMQ for Apache Kafka exposes the Kafka wire data plane:
  the official endpoint comparison documents default, SASL (PLAIN or
  SCRAM-SHA-256), and SSL endpoints with instance ACL username/password. The
  Kafka client protocol is a non-resource client line protocol; the Kafka
  instance control plane remains ACS3/RPC addressable.
- Alibaba Cloud ApsaraVideo Live ingest is the media plane: the official
  stream-ingest contract documents RTMP, ApsaraVideo Real-time Communication
  (ARTC), and SRT ingest with ingest/streaming URLs. Live media transport is a
  non-resource media plane; ApsaraVideo Live control-plane APIs remain
  ACS3/RPC addressable.
- Alibaba Cloud ApsaraMQ for MQTT authenticates every MQTT CONNECT with a
  per-device access credential (ClientId, DeviceAccessKeyId,
  DeviceAccessKeySecret) or an application token minted by the broker OpenAPI;
  the MQTT client protocol is a device-credential-bound transport, while the
  broker control-plane OpenAPI remains ACS3/RPC addressable.
- Alibaba Cloud ApsaraDB for Redis/Tair and ApsaraMQ for RabbitMQ speak the
  RESP client protocol (instance password `AUTH`) and AMQP 0-9-1 (compatible
  with open-source RabbitMQ clients). Both are non-resource client line
  protocols; the corresponding control planes remain ACS3/RPC addressable.
- Tencent Cloud IoT Explorer device plane: devices authenticate with the
  device triple (ProductID + DeviceName + DeviceSecret) or TLS certificates
  over MQTT; the device surface is device-identity-bound, not CAM AKSK. IoT
  Explorer control-plane APIs remain TC3 addressable through the generic
  adapter.
- Tencent Cloud CKafka exposes the Kafka wire data plane: the official
  network-connection contract documents SASL PLAIN and SCRAM-SHA-256/512
  mechanisms with instance username/password. The Kafka client protocol is a
  non-resource client line protocol; the CKafka control-plane API remains TC3
  addressable.
- Tencent Cloud Cloud Streaming Services (CSS) ingest/playback is the media
  plane: the official live-streaming contract documents RTMP, HTTP-FLV, HLS,
  and WebRTC push/playback. Live media transport is a non-resource media
  plane; CSS control-plane APIs remain TC3 addressable.
- Tencent Cloud IM is credential-bound under the AKSK/IAM-only entrypoint
  contract: every client and server endpoint requires an app `SDKAppID` and a
  `UserSig` derived from the IM application's SDK secret key, with no CAM
  AKSK alternative documented. IM control-plane resources with a TC3 surface
  remain addressable through the generic adapter.
- Tencent Cloud TDMQ for Apache Pulsar and TDMQ for RabbitMQ expose the
  open-source Pulsar binary (`pulsar://`, `pulsar+ssl://`) and AMQP 0-9-1
  client protocols with cluster access username/password. Those are
  non-resource client line protocols; the TDMQ control-plane APIs remain TC3
  addressable.
- Tencent Cloud TencentDB for Redis speaks the RESP client protocol (RESP2
  `AUTH` username/password; RESP3 and ACL are unsupported per the official
  version table). RESP is a non-resource client line protocol; the TencentDB
  control plane remains TC3 addressable.
- Baidu AI Cloud Message Service for Kafka exposes the Kafka wire data plane:
  the official access-protocol contract documents PLAINTEXT, SASL_PLAINTEXT,
  SASL_SSL (SASL/PLAIN or SASL/SCRAM username/password), and SSL endpoints.
  The Kafka client protocol is a non-resource client line protocol; the Kafka
  cluster control plane remains BCE-signed addressable.
- Baidu AI Cloud Message Service for RabbitMQ is fully compatible with the
  AMQP protocol from open-source RabbitMQ; the AMQP client protocol is a
  non-resource client line protocol, while the RabbitMQ cluster control plane
  remains BCE-signed addressable.
- Baidu AI Cloud Cloud Database SCS (Redis) runs the open-source Redis RESP
  data plane while its control-plane REST API uses BCE signing. RESP is a
  non-resource client line protocol; the SCS control plane remains
  BCE-signed addressable.
- Azure Speech's current sovereign-cloud feature tables explicitly mark Voice
  Live unsupported in both Azure Government and Azure operated by 21Vianet.
  The adapter therefore accepts only exact single-label public
  `<resource>.services.ai.azure.com` and legacy
  `<resource>.cognitiveservices.azure.com` WSS hosts. Government/China,
  nested, direct private-DNS-zone, custom, and lookalike hosts fail before token
  resolution. Normal public resource names remain usable when Azure Private
  Link DNS resolves them privately inside the operator VNet. This is an
  explicit unavailable mapping, not a guessed endpoint implementation.

Baidu RTC AI Agent is not part of that exclusion. It has a documented BCE v1
server control plane, so `rtc-aiagent-ws` signs create/stop from BCE AKSK/IAM,
uses the returned 24-hour instance token only inside the exact WSS handshake,
and reads the separately purchased product entitlement only from
`BCE_RTC_LICENSE_KEY`. The public MCP schema still exposes no credential,
license, or token field and never supports the direct AK/SK query mode.

## Completion rule

Do not mark a provider or the overall goal complete from this inventory until:

1. every official public resource API is mapped to an implemented protocol
   family or a documented non-resource/security exclusion;
2. each protocol family has hermetic request/signature/transport vectors;
3. representative control-plane, object/data-plane, upload, download, and
   mutation-gate calls pass against real credentials; and
4. a newly published API can be invoked without adding a resource-specific Go
   handler.

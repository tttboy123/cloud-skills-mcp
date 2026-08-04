# Six-cloud Goal completion matrix

Date: 2026-08-03
Overall status: **IN PROGRESS — protocol-family coverage landed; live acceptance scoped to AWS + Tencent per operator decision 2026-08-04 (both passed), remaining four providers accepted on hermetic evidence**

This matrix maps the active product Goal to authoritative repository and
runtime evidence. A green hermetic test proves contract behavior without cloud
credentials; it does not prove that an operator's real IAM principal can reach
a provider. The Goal stays open until every official public resource API is
mapped to an implemented protocol family (or a documented security/non-resource
exclusion), all remaining families in `api-protocol-coverage.md` are closed,
and every provider has successful live acceptance with sanitized audit evidence.

| Goal requirement | Implementation evidence | Verification evidence | Gate status |
|---|---|---|---|
| One MCP server for AWS, Azure, Google Cloud, Alibaba Cloud, Tencent Cloud and Baidu AI Cloud | `internal/mcp/cloud/types.go`, `adapters.go`, `server.go` | Tool-contract and protocol-smoke tests enumerate all six provider prefixes | Implemented; live pending |
| All documented resource APIs addressable without per-resource Go handlers | Generic transport coverage exists for AWS SigV4/SigV4a including finite/bidirectional HTTP/2 signed EventStream, finite mutation-only raw-frame SigV4 WSS, Connect Health Medical Scribe signed-EventStream WSS, standard/Medical/Call Analytics Transcribe WSS, finite IoT MQTT WSS, Kinesis Video WebRTC Signaling Master/Viewer WSS, finite AppSync Events IAM WSS, finite legacy AWS AppSync GraphQL IAM WSS subscriptions, Chime SDK Messaging WebSocket event streaming and Amazon Connect chat participant WebSocket event streaming, Azure Entra REST plus OpenAI Realtime, Chat Completions streaming SSE, Responses streaming SSE, Voice Live model/Agent WSS, Entra-backed Web PubSub standard/reliable JSON/Protobuf WSS and finite MQTT 3.1.1/5.0 clients, Entra-backed SignalR Service serverless JSON-hub WSS, Event Grid Namespace Entra MQTT v5 WSS, and Service Bus/Event Hubs AMQP 1.0 over exact public, US Government, or China WSS with internal Entra/CBS, bounded broker operations and no exported message/session locks, GCP ADC REST plus Artifact Registry/`gcr.io` OCI Distribution 1.1 HTTP, both raw and operator-approved `FileDescriptorSet`-driven ProtoJSON gRPC over HTTP/2 (including Speech v2 bidirectional-streaming vectors), bounded Vertex/Gemini Live ADC WSS with bounded tool-call dispatch/internal transparent resumption, and Firebase SSE, Alibaba ACS3/RPC V2/ROA V2/DataHub/OpenSearch/MaxCompute ODPS V2/V4/Function Compute classic and current Trigger families/OSS V1/V4/SLS/MNS/RocketMQ 4.x MQ HTTP/ACR Enterprise Registry HTTP/OTS plus NLS REST short ASR/TTS and five NLS recognition/synthesis WSS namespaces with internal token, Registry credential, and receipt-handle containment, Tencent TC3/API 3.0 v1/legacy qcloud API 2017/COS/CLS q-sign/realtime ASR, virtual-number detection, SOE evaluation, speech-translation, voice-conversion, standard realtime TTS, streaming-text TTS and large-model podcast WSS/MPS private-audio recognition WSS/MPS streaming TTS WSS, and Baidu BCE v1/v2, IAM application-permission IoT Core HTTP Publish and MQTT 3.1.1/5.0 WSS, plus RTC AI Agent BCE-create/private-token-WSS/stop; remaining families and explicit retired/credential-bound mappings are enumerated in `api-protocol-coverage.md` | Official and SDK signature vectors plus transport, policy, and data-plane tests | **Protocol mapping landed — remaining families are enumerated as implemented or documented exclusions; AWS read-only live vectors passed 2026-08-04 (sts:GetCallerIdentity request `ed7f62aa-6aad-4e40-a711-95e9ab809cef`, 399 bytes; ECR Public GetManifest on `aws-containers/hello-app-runner`, 5832 bytes, audit succeeded), and Tencent sts:GetCallerIdentity passed (request `e9e17daa-f20b-42ea-8965-c39c8011b551`, 194 bytes, audit succeeded); per operator decision 2026-08-04 the remaining four providers are accepted on hermetic evidence with live gates documented as pending operator credentials** |
| Official AKSK/IAM identity entrypoints | AWS SDK chain, non-CLI Azure Identity, Google ADC, Alibaba credentials-go RAM/OIDC/ECS role, Tencent CAM temporary tuple, Baidu BCE AKSK/STS | Credential-chain and status tests; credentials absent from MCP schemas; status distinguishes adapter availability from unverified authentication | Implemented; AWS and Tencent live identity resolution passed 2026-08-04; Azure/GCP/Alibaba/Baidu live resolution pending operator credentials (accepted per operator decision 2026-08-04) |
| Credentials cannot be supplied, minted or exported through MCP | Credential headers/query parameters rejected; STS/token/key/password issuance and export families hard-rejected; output redaction remains defense in depth | `TestInvocationBoundaryRejectsCredentialExfiltrationAndUnboundedInput` and `TestInvocationBoundaryNeverIssuesOrExportsCloudCredentials` | Hermetic gate implemented |
| Large/binary API responses remain usable without entering model context | All six adapters support bounded `response_file` streaming to a new approved-root target; no overwrite, mode 0600, atomic publish, provider errors and over-limit transfers leave no target; official Range requests cover larger objects | `TestReadRESTResponseStreamsSuccessfulBodyToNewFile`, `TestReadRESTResponseFileNeverOverwritesOrLeavesPartialFiles`, policy/schema/protocol tests | Hermetic gate implemented; live object download pending |
| Read/write and sensitive-operation boundary | Conservative read classifier; `CLOUD_SKILLS_ALLOW_MUTATIONS=1` + `force=true`; separate sensitive gate; host approval remains mandatory | Server contract, mutation-gate and protocol-smoke tests | Hermetic gate implemented |
| Sanitized, fail-closed audit | Mode-0600 JSONL; no headers/body/response/query values; mutation pre-audit fail closed; request ID captured | Audit sink, URL sanitization, failure and live audit tests | Hermetic implemented; live audit pending |
| Skills service and official documentation mapping | Six direct-network `SKILL.md`, six `agents/openai.yaml`, provider `references/official-docs.md` files | Skill validator and live official-link review | Implemented |
| Protocol, installer, security and cross-platform build gates | `scripts/ci/protocol-smoke.sh`, `mapping-audit.sh`, `install-smoke.sh`, `build-release.sh`, `.github/workflows/ci.yml` | Azure Web PubSub JSON/Protobuf/MQTT slice covers exact endpoint/schema validation, four PubSub subprotocols, official proto3 wire vectors, binary Any/stream messages, token containment, reliable recovery/sequence/publisher state, MQTT 3.1.1/5.0 QoS/state/property flow, and atomic failure behavior; Protobuf implementation run [30784347113](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30784347113) passed. Vertex Live recovery adds exact function-call ID response dispatch, atomic handler bounds, private handle sanitization, acknowledged-message replay, GoAway/unexpected-close reconnect, integer preservation, and two fuzz targets; implementation run [30785793131](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30785793131) passed macOS, Ubuntu, ShellCheck, security, and release jobs. Google schema-driven gRPC adds exact descriptor-selected methods, strict ProtoJSON request conversion, finite four-shape streaming, recursive unknown-wire rejection including `Any`, official 64-bit-safe output mapping, atomic NDJSON, and two fuzz targets; implementation run [30787842631](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30787842631) passed. Baidu RTC AI Agent adds exact BCE v1 create/private-token WSS/stop, internal license containment, concurrent bounded duplex streaming, fail-closed atomic output, mutation-only policy, a live mutation entrypoint, and a plan fuzz target; initial lifecycle run [30790805986](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30790805986) passed. The codec follow-up corrects the control field to official `config.audiocodec`, covers `raw`, `raw16k`, PCMA, PCMU, G.722, and variable-length Opus packet vectors, and internally derives `ac`/`ptime`/`plen`; implementation run [30792487473](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30792487473) passed. The static command follow-up strictly parses every documented non-event-correlated client command, rejects credential material and unsafe media URLs, and preserves pre/post-audio order; implementation run [30793969272](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30793969272) passed. The stateful media follow-up adds approved-root `image_file`, exact provider-triggered 16 KiB/Base64 upload framing, one-request consumption, and non-interleaved writes; implementation run [30795300185](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30795300185) passed. The Function Call follow-up implements the current nested JSON format, exact provider `session_id` correlation, bounded credential-free result/post-function templates, and fail-closed unknown/duplicate/overflow handling; implementation run [30796139810](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30796139810) passed. The SignalR slice adds exact `signalr-ws` endpoint/hub validation, internal Entra `:generateToken`, official `0x1E`-framed JSON Hub Protocol handshake, read-only Subscribe with automatic void Completion replies, mutation-only Invoke with StreamItem/Completion correlation, atomic failure behavior, and a lookalike pre-credential protocol-smoke entry. The Chime messaging slice adds exact `chime-messaging-ws` endpoint/ARN/session validation, internal SigV4 `GetMessagingSessionEndpoint`, a `/connect` presign vector verified against the AWS SDK Go v2 presigner, strict event-type/message-type/payload sanitization, atomic failure behavior, and a lookalike pre-credential protocol-smoke entry. The Amazon Connect chat slice adds exact `connect-chat-ws` participant endpoint validation, internal SigV4 `StartChatContact` plus bearer-only `CreateParticipantConnection`, official `aws/subscribe`/`aws/chat` topic framing with subscribe-state enforcement, sanitized bounded event output, atomic failure behavior, WebSocket-URL host validation, and a lookalike pre-credential protocol-smoke entry. The Azure OpenAI Chat Completions streaming slice adds exact `openai-chat-stream` endpoint/path/api-version validation, internal `https://cognitiveservices.azure.com/.default` token containment, strict `text/event-stream` `chat.completion.chunk`/`[DONE]` parsing, bounded finite plans, atomic failure behavior, and a lookalike pre-credential protocol-smoke entry. The mapping audit adds `mapping-audit.sh`, which proves every one of the 72 implemented `auth_scheme` identifiers is defined as a Go constant, wired into the policy/adapter dispatch, covered by a hermetic test, exercised by the protocol-smoke scheme sweep, and documented in both `api-protocol-coverage.md` and this matrix, and verifies the six skill suites. The Azure OpenAI Responses streaming slice adds exact `openai-responses-stream` endpoint/path/api-version validation, internal `https://cognitiveservices.azure.com/.default` token containment, strict documented `ResponseStreamEvent` type/sequence/per-type validation with `response.completed`/`response.incomplete` termination and fail-closed `error`/`response.failed`, internally injected `stream:true`/`store:false`, bounded finite plans, atomic failure behavior, and a lookalike pre-credential protocol-smoke entry | Fresh local module verification, formatting, vet, race suite (cloud 80.6% / sdk 96% coverage), build, protocol smoke with the 72-scheme pre-credential sweep, scheme mapping audit, install smoke, shell syntax, and six-Skill validation passed. Remote macOS/Ubuntu verification, ShellCheck, security scans, govulncheck, Actionlint, and four-platform release builds passed on the pushed branch head [30868567201](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30868567201), alongside the per-slice runs [30784347113](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30784347113), [30785793131](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30785793131), [30787842631](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30787842631), [30790805986](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30790805986), [30792487473](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30792487473), [30793969272](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30793969272), [30795300185](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30795300185), and [30796139810](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30796139810) |
| Tencent Enterprise TCR resource protocol | Direct Docker/OCI Registry HTTP with internal TC3 `CreateInstanceToken(TokenType=temp)`, exact public/VPC/operator-pinned custom hosts, pre-credential path/method validation, temporary Basic containment, response-file support, and fail-closed redirects; Personal Edition is credential-bound | Hermetic signer/credential/path/upload/redirect tests, protocol smoke, Skill/docs mapping, and dedicated read-only ListTags live gate | Implemented; live pending |
| Baidu CCR Enterprise/Personal resource protocol | Direct Docker/OCI Registry HTTP with fixed internal BCE v1 user/one-hour credential exchange, exact public/VPC/operator-pinned custom or Personal host validation, same-origin challenge/scope binding, response-file support, and fail-closed redirects | Hermetic signer/credential/path/upload/challenge/redirect tests, protocol smoke, Skill/docs mapping, and dedicated read-only ListTags live gate | Implemented; live pending |
| AWS IoT Core MQTT resource protocol | Direct SigV4-presigned MQTT 3.1.1/5.0 WSS with read-only subscribe and mutation-only bidirectional client entrypoints; QoS 0/1 subscribe/unsubscribe/publish/PUBACK, clean/persistent session state, bounded offline queue drain, retained/Last Will, supported MQTT 5 application/session properties, broker capability validation, quota pacing, graceful error disconnect, atomic output, and no exposed IAM/presign material | Official SDK signature vector plus CONNECT/SUBSCRIBE/UNSUBSCRIBE/PUBLISH and version-aware CONNACK/SUBACK/UNSUBACK/PUBACK/DISCONNECT transport tests, lookalike pre-credential protocol smoke, Skill/docs mapping, fuzzing, and dedicated self-publish mutation gate | Implemented; live pending |
| Amazon IVS Chat messaging resource protocol | Direct WSS after an internal IAM-signed `CreateChatToken` HTTPS call; least-capability view/send/moderation tokens, exact current regional endpoints, bounded Message/Event collection, SendMessage pacing, sensitive-gated DeleteMessage/DisconnectUser, atomic output, and no exposed token or SigV4 material | Token-signing, capability, endpoint, action, frame, policy and atomic-failure tests; lookalike pre-credential protocol smoke; Skill/docs mapping; dedicated self-message mutation gate | Implemented; live pending |
| Amazon Lex V2 streaming conversation resource protocol | Direct HTTP/2 `StartConversation` after internal SigV4 `STREAMING-AWS4-HMAC-SHA256-EVENTS` stream signing; exact official `runtime-v2-lex` origin, bot/alias/locale/session URI, configuration-then-audio/DTMF/text event state machine, official 8 kHz lpcm audio and single-character DTMF bounds, bounded sanitized TextResponse/Transcript/Heartbeat/IntentResult/AudioResponse/PlaybackInterruption output, graceful EOF/timeout publication, and no exposed signature material | Event-signing, URI, state-machine, capability, audio/DTMF, sanitization, atomic-failure and timeout tests; lookalike pre-credential protocol smoke; Skill/docs mapping | Implemented; live pending |
| Amazon Chime SDK Messaging WebSocket resource protocol | Direct WSS after internal SigV4 `GetMessagingSessionEndpoint` and a SigV4-presigned `/connect` (`chime` service, bounded `X-Amz-Expires`, official AppInstanceUser ARN as `userArn`, unique `sessionId`, optional `prefetch-on=connect`, STS token only in the canonical query); exact official `data-messaging.chime.aws` host, read-only subscription, strict documented event-type/message-type/payload validation, decoded bounded `Payload` JSON, atomic sanitized NDJSON, and no exposed signed URL or STS material | Boundary, endpoint-request, SDK-reference signature vector, event sanitization, atomic-failure and endpoint-mismatch tests; lookalike pre-credential protocol smoke; Skill/docs mapping; dedicated read-only Subscribe live gate | Implemented; live pending |
| Amazon Connect chat participant WebSocket resource protocol | Direct WSS after internal SigV4 `StartChatContact` and a bearer-only `CreateParticipantConnection`; exact `participant.connect.<region>.amazonaws.com` host and `/participant/connect` path validation, official `aws/subscribe`/`aws/chat` topic framing with heartbeat/ping handling, mutation-only policy (starting a chat contact), bounded instance/contact-flow/display-name plan with optional initial message, up to 16 bounded attributes, and 1–16 bounded text/plain or text/markdown messages sent through the internal participant SendMessage REST path after subscription, sanitized bounded event output, atomic NDJSON, and no exposed participant token, connection token, or dial URL | Boundary, plan, transport, subscribe-state, event-sanitization, atomic-failure and WebSocket-URL tests; host fuzzing; lookalike pre-credential protocol smoke; Skill/docs mapping; dedicated mutation-only ObserveChat live gate | Implemented; live pending |
| Azure SignalR Service serverless WSS resource protocol | Direct WSS after internal Entra `:generateToken` (`api-version=2022-11-01`) and `https://signalr.azure.com/.default`; exact single-label `<resource>.service.signalr.net` host, swagger-validated hub names, official JSON Hub Protocol handshake/`0x1E` framing, read-only Subscribe with automatic void Completion replies, mutation-only Invoke with correlated StreamItem/Completion terminal state, bounded `minutes_to_expire`/message/timeout, atomic sanitized NDJSON, and no exposed Entra/client token | Boundary, token-minting, frame-parser, sanitization, correlation and atomic-failure tests; lookalike pre-credential protocol smoke; Skill/docs mapping; dedicated read-only Subscribe live gate | Implemented; live pending |

| Azure OpenAI Chat Completions streaming SSE resource protocol | Direct POST `text/event-stream` after an internal `https://cognitiveservices.azure.com/.default` Entra token; exact single-label `<resource>.openai.azure.com` host and official `/openai/deployments/<deployment>/chat/completions` path, date-structured `api_version`, internally injected `stream:true`, bounded model/messages/max_tokens/temperature plan, strict `chat.completion.chunk` shape and `[DONE]` termination, atomic sanitized NDJSON, and no exposed Entra token or api key | Boundary, plan, transport, chunk-shape, sentinel, atomic-failure and timeout tests; lookalike pre-credential protocol smoke; Skill/docs mapping; dedicated read-only streaming live gate | Implemented; live pending |
| Azure OpenAI Responses streaming SSE resource protocol | Direct POST `text/event-stream` after an internal `https://cognitiveservices.azure.com/.default` Entra token; exact single-label `<resource>.openai.azure.com` host and official `/openai/v1/responses` path, optional documented `api_version` (`v1`, `preview`, date-structured, or omitted for the v1 GA default), internally injected `stream:true` and `store:false`, bounded model/input/max_output_tokens/temperature plan, strict documented `ResponseStreamEvent` type/sequence/per-type validation with `response.completed`/`response.incomplete` termination and fail-closed `error`/`response.failed`, atomic sanitized NDJSON, and no exposed Entra token or api key | Boundary, plan, transport, event-envelope, terminal, atomic-failure and timeout tests; lookalike pre-credential protocol smoke; Skill/docs mapping; dedicated read-only streaming live gate | Implemented; live pending |
| Baidu IoT Core HTTP/MQTT resource protocols | Direct HTTP Publish and MQTT 3.1.1/5.0 over exact HTTPS/WSS with internal IAM application-permission HMAC, bounded wildcard/shared QoS 0/1/2 subscribe/unsubscribe/publish/Will plans, MQTT 5 properties, duplicate-safe bidirectional QoS 2, read/mutate separation, atomic output, and no exposed derived credential/token | Official signature and HTTP contract vectors, CONNECT/SUBSCRIBE/UNSUBSCRIBE/PUBLISH plus SUBACK/UNSUBACK/PUBACK/PUBREC/PUBREL/PUBCOMP transport tests, lookalike pre-credential protocol smoke, Skill/docs mapping, and dedicated MQTT read/HTTP mutation broker gates | Implemented; live pending |
| Explicit retired / device-plane / media-plane / client-line-protocol exclusions | AWS MSK Kafka and ElastiCache/MemoryDB RESP are custom client line protocols (IAM/SASL/mTLS or RESP AUTH), AWS MediaConnect inputs are the Zixi/SRT/RTP UDP media plane, Azure Event Hubs Kafka requires SASL/PLAIN `$ConnectionString` plus connection-string password or an in-client OAuth path, Azure IoT Hub and Alibaba IoT Platform and Tencent IoT Explorer device planes authenticate with device SAS/device-secret/X.509 identities rather than operator IAM, Azure Speech TTS WebSocket V2 raw WSS is withdrawn to Speech-SDK-only, and Google Cloud IoT Core is retired (2023-08-16); every entry is recorded with official-source evidence. AWS Kinesis Video Streams RTMP/HLS media ingest, Azure Media Services (retired 2024-06-30), the deprecated Azure OpenAI Assistants API (retired 2026-08-26), GCP Memorystore for Redis RESP and Managed Service for Apache Kafka, Alibaba ApsaraMQ for Kafka and ApsaraVideo Live ingest, Tencent CKafka, Cloud Streaming Services, and UserSig-bound IM, and Baidu Message Service for Kafka are likewise recorded with official-source evidence. Amazon MQ ActiveMQ/RabbitMQ broker wire protocols, Azure Cache for Redis RESP and Azure Relay SAS-token Hybrid Connections, Alibaba ApsaraMQ for MQTT device credentials, Tencent TDMQ for Apache Pulsar and TDMQ for RabbitMQ, and Baidu Message Service for RabbitMQ AMQP are likewise recorded as documented non-resource/credential-bound client line protocols. Database wire protocols (Azure Database for PostgreSQL/MySQL, Cloud SQL, ApsaraDB for Redis/Tair and ApsaraMQ for RabbitMQ, TencentDB for Redis, and Baidu SCS Redis) plus AWS MediaLive media inputs complete the documented client line protocol and media plane mappings. Chime SDK real-time WebRTC media, MediaPackage HLS/DASH/CMAF delivery, AppStream 2.0 client sessions, GCP Live Streaming API ingest/delivery, and Alibaba ApsaraDB RDS plus Baidu RDS database wire protocols complete the media-plane and client line protocol mappings. ACS real-time RTP/WebRTC media and IVS Stage media are recorded as media-plane mappings in `api-protocol-coverage.md` and the control planes remain addressable | Provider table and explicit-mapping section updated with official documentation citations and non-resource/credential-bound/unavailable reasons | Implemented as documented exclusions |
| Observable six-cloud acceptance | `TestLiveSixCloudReadOnly` selects providers and logs provider/outcome/bytes/request ID only; `scripts/ci/live-acceptance.sh` is the single executable entrypoint that requires an enabled `CLOUD_SKILLS_LIVE_*` gate (and `CLOUD_SKILLS_ALLOW_MUTATIONS=1` for mutation gates) and runs every `TestLive*`; dedicated gates cover AWS IoT Core MQTT mutation, Amazon IVS Chat, Amazon Chime SDK Messaging, Amazon Connect chat, Baidu CCR, Baidu IoT Core MQTT, Baidu RTC, Alibaba MQ, Alibaba Enterprise ACR, Tencent CLS, Tencent Enterprise TCR, Azure ACR, Azure SignalR, Azure OpenAI Chat Completions, Azure OpenAI Responses, private/public Amazon ECR, and Google Artifact Registry without printing response bodies or secrets | Requires operator-injected credentials and opt-in live flags; product gates additionally require their exact endpoint and existing resource identifiers. Per operator decision 2026-08-04, live acceptance is scoped to AWS and Tencent as representative cross-provider proofs: AWS `sts:GetCallerIdentity` (request `ed7f62aa-6aad-4e40-a711-95e9ab809cef`, 399 bytes, audit succeeded), AWS ECR Public `GetManifest` on `https://public.ecr.aws/v2/aws-containers/hello-app-runner/manifests/latest` (5832 bytes, audit succeeded), and Tencent `sts:GetCallerIdentity` (request `e9e17daa-f20b-42ea-8965-c39c8011b551`, 194 bytes, audit succeeded). Azure/GCP/Alibaba/Baidu remain hermetic-verified with live gates documented as pending operator credentials | **Accepted — AWS and Tencent live passed; remaining four providers accepted on hermetic evidence** |

Verified slices:

Alibaba Cloud ACR Enterprise Edition Docker/OCI Registry now uses direct HTTPS
through `auth_scheme=acr-registry`. The gateway resolves only the official
credentials-go RAM AKSK/STS/role chain, signs the fixed RPC V2
`GetAuthorizationToken` request internally, and keeps its temporary login and
the subsequent Registry Bearer token out of MCP, audit, and error output.
Hermetic coverage binds the Enterprise instance ID, region, exact public/VPC or
operator-pinned custom Registry endpoint, current `dockerauth`/`dockerauth-ee`
realm families, Zhangjiakou exception, service audience, repository path,
method, catalog/target/source scopes, and mutation gate. It covers Registry
version/catalog, manifest, blob, tags, referrers, chunked/monolithic upload and
cross-repository mount operations, plus bounded same-region OSS 307 blob
downloads with Authorization removed and atomic response-file publication.
Personal Edition remains an explicit credential-bound exclusion because its
official interface requires a fixed Registry password and does not support
`GetAuthorizationToken`. A dedicated ListTags live gate is implemented but
remains pending operator RAM credentials and an existing readable repository.
Implementation commit `04a069fdec5fae3a6491029a7dade6aa29605c53` passed
remote macOS, Ubuntu, ShellCheck, Actionlint, govulncheck and four-platform
release verification in
[CI run 30837268830](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30837268830).
Fresh local module verification, formatting, vet, race coverage (80.0% total,
80.1% cloud package), build, protocol/install smoke, shell syntax, all six
Skill validators, Actionlint, govulncheck, four-platform archives, and a
ten-second ACR invocation/challenge fuzz run (226,104 executions) also passed.

Google Artifact Registry and Artifact Registry-backed `gcr.io` Docker/OCI
repositories now use direct HTTPS through `auth_scheme=artifact-registry`.
The gateway resolves only the official ADC chain, sends its short-lived OAuth
token only as an internal Bearer header, and accepts exact regional or
multi-regional `docker.pkg.dev` plus the four current `gcr.io` compatibility
hosts. Hermetic coverage binds project/repository/image namespaces and OCI
version, manifest, blob, tags, referrers, mount and monolithic-upload methods,
including an internal exact-origin POST/PUT fallback whose Location capability
never leaves the server; internal token endpoints, unscoped catalog, lookalike hosts, redirects,
caller credentials and unsupported chunked PATCH uploads fail before ADC.
Atomic response-file, request-ID, token-containment, protocol-smoke, Skill
documentation and a dedicated live ListTags gate are included. Live acceptance
remains pending operator ADC and an existing readable repository.
Implementation commit `61af7ea2d5dfa0d8147db3ed7a53141af4acb15c`
passed remote macOS, Ubuntu, ShellCheck, Actionlint, govulncheck and
four-platform release verification in
[CI run 30834332364](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30834332364).
Fresh local module verification, formatting, vet, race coverage (80.2% total),
build, protocol/install smoke, shell syntax, all six Skill validators,
Actionlint, govulncheck, four-platform archives, and a ten-second Registry
invocation fuzz run (186,275 executions) also passed.

Amazon ECR private and public Docker/OCI Registry HTTP now uses
`auth_scheme=ecr` and only the AWS SDK credential chain. The gateway derives
the provider authorization token through an internal SigV4 JSON request,
keeps both the base64 token and decoded password out of MCP/audit/error output,
and sends the documented private Basic or public Bearer form. Hermetic tests
cover classic, FIPS, dual-stack, China and Public endpoints, strict
account/region/service/path/method binding, the Public tags-API exclusion,
bounded token failures, exact proxy selection, request IDs, mutation policy,
and private layer 307 downloads to the same-region Starport S3 bucket with
Authorization removed and atomic response-file publication. Dedicated private
and Public live gates are implemented but remain pending operator IAM
credentials and existing repositories; this slice is not yet live-proven.
Implementation commit `1ed3ab41f64960ca97dfce47774e460ecc7b92a0`
passed remote macOS, Ubuntu, ShellCheck, Actionlint, govulncheck and
four-platform release verification in
[CI run 30831950990](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30831950990).
Fresh local module verification, formatting, vet, race coverage (80.3% total),
build, protocol/install smoke, AWS Skill validation, Actionlint, govulncheck,
four-platform archives, and a ten-second ECR invocation fuzz run (189,200
executions) also passed.

Azure Container Registry now uses direct HTTPS with the official non-CLI
Entra-to-ACR OAuth2 exchange and path/method-matched access scopes. It covers
Docker/OCI catalog, manifest, blob, upload, tags, referrers and cross-repository
mount calls plus ACR repository/tag/manifest metadata, delete, soft-delete
catalog/list and manifest restore calls. Login, regional-login, Private Endpoint
DNS and provider-owned blob redirect rules fail closed; Entra, refresh, access
and signed redirect values remain internal. Implementation commit
`b15cee7508753eb3e322e6e64bc9a0e7ba15bf7c` and live-gate commit
`cae2116b6ff23ef33f772ebb82d6982bc5220b3c` passed remote macOS, Ubuntu,
ShellCheck, Actionlint, govulncheck and four-platform release verification in
[CI run 30827896455](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30827896455).
Fresh local module verification, formatting, vet, race coverage (80.3% total),
build, protocol/install smoke, shell syntax, all six Skill validators,
Actionlint, govulncheck, four-platform archives and a ten-second ACR validation
fuzz run (169,822 executions) also passed. Live provider acceptance remains
pending operator-injected Azure IAM credentials, registry endpoint and existing
pullable repository.

Firebase Realtime Database SSE supports bounded
read-only listeners through direct HTTPS and the official Google Auth ADC
chain. The gateway requests the documented `firebase.database` and
`userinfo.email` scopes, keeps the Bearer token in the internal Authorization
header, validates all current Firebase Database URL forms, and follows only
bounded official 307 redirects that preserve the exact path and query. It
strictly parses `put`, `patch`, and optional `keep-alive` events, rejects
`cancel`, `auth_revoked`, malformed, unknown, oversized, or credential-bearing
provider data, and atomically publishes mode-0600 NDJSON. Implementation commit
`71dbb5ee7127c02ab2d9c375a6829c8d74c3cb49` passed remote macOS,
Ubuntu, ShellCheck, Actionlint, govulncheck and four-platform release
verification in [CI run 30803592532](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30803592532).
Fresh local module verification, formatting, vet, race coverage (80.4% total),
build, protocol/install smoke, shell syntax, all six Skill validators,
Actionlint, govulncheck, four-platform archives and a ten-second Firebase SSE
event fuzz run (340,418 executions) also passed.

Azure Event Grid Namespace MQTT v5 now uses direct WSS with the official
`mqtt` subprotocol and non-CLI Entra identity. The gateway requests
`https://eventgrid.azure.net/.default`, places the JWT only in
`OAUTH2-JWT` CONNECT/AUTH packets, and exposes finite read-only subscriptions
or mutation-gated publish/stateful clients. It covers the broker's documented
QoS 0/1, persistent session and Will, PUBLISH user/request-response properties,
message expiry, retained messages, aliases, flow control, assigned client IDs,
wildcard/shared subscriptions, subscription identifiers, negative
acknowledgements, server disconnect, and atomic mode-0600 Base64 NDJSON. The
implementation commit `1bf80fdde5492baef9378a84f3dc97c25402d151` passed
remote macOS, Ubuntu, ShellCheck, Actionlint, govulncheck and four-platform
release verification in [CI run 30805779230](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30805779230).
Fresh local module verification, formatting, vet, race coverage (80.1% total,
80.2% cloud package), build, protocol/install smoke, all six Skill validators,
Actionlint, govulncheck, four-platform archives, and a ten-second MQTT
packet/property fuzz run (341,933 executions) also passed.

Azure Service Bus and Event Hubs now use AMQP 1.0 directly over the exact
public `$servicebus/websocket` WSS endpoint. The server obtains only the
documented Service Bus or Event Hubs Entra scope from the non-CLI operator
identity, while the official Azure Go SDK keeps SASL anonymous, CBS claims,
token refresh, AMQP sessions and links internal. Service Bus covers bounded
send, schedule/cancel, peek, receive/deferred receive with explicit in-call
settlement, dead-letter subqueues, session state, session-lock renewal and
near-expiry message-lock renewal without exporting lock capabilities. Event
Hubs covers hub/partition properties, finite per-partition receive from all
five official start-position forms, and bounded partition-ID/key sends.
Implementation commit `f14e130438a8571775964d9ab34742e814c43fc4` plus the
ShellCheck-safe smoke-vector follow-up `d32200acdc4d706f0adf3c4784e24bf1d322c0d3`
passed remote macOS, Ubuntu, ShellCheck, Actionlint, govulncheck and
four-platform release verification in [CI run 30809196252](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30809196252).
Fresh local module verification, formatting, vet, race coverage (80.1% total),
build, protocol/install smoke, shell syntax and ShellCheck, all six Skill
validators, Actionlint, govulncheck, four-platform archives, and a five-second
AMQP plan fuzz run (158,828 executions) also passed.

Azure messaging sovereign endpoint routing now accepts only the three active
official Service Bus namespace suffixes: public Azure
`servicebus.windows.net`, Azure US Government
`servicebus.usgovcloudapi.net`, and Azure operated by 21Vianet
`servicebus.chinacloudapi.cn`. Service Bus and Event Hubs preserve the exact
`$servicebus/websocket` WSS target through the Azure SDK. Lookalike hosts,
private-link aliases, custom domains, and the retired Microsoft Cloud Germany
suffix fail closed; credentials and CBS tokens remain internal. Implementation
commit `999761951b1c8d376033848ab230018ba5263838` passed remote macOS, Ubuntu,
ShellCheck, Actionlint, govulncheck and four-platform release verification in
[CI run 30810688615](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30810688615).
Fresh local module verification, formatting, vet, race coverage (80.1% total),
build, protocol/install smoke, shell syntax and ShellCheck, all six Skill
validators, Actionlint, govulncheck, four-platform archives, and a five-second
endpoint allowlist fuzz run (128,000 executions) also passed.

Azure OpenAI Realtime endpoint routing now accepts only the current official
single-label public-cloud `<resource>.openai.azure.com` host. Nested public
subdomains, guessed Azure Government/China suffixes, private-link/custom hosts,
and lookalikes fail before credential resolution. The official Realtime page
publishes only the public endpoint and public Global deployment regions; the
Azure Government model page states that its list includes all Azure OpenAI
models offered there and lists no Realtime model, while Microsoft publishes no
Azure China Foundry Realtime endpoint contract. This sovereign mapping is now
recorded as explicitly unavailable rather than pending guessed implementation.
Implementation commit `e45852af75d408a209d1e62e3da68946bc0ba22d` passed
remote macOS, Ubuntu, ShellCheck, Actionlint, govulncheck, and four-platform
release verification in [CI run 30811775660](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30811775660).
Fresh local module verification, formatting, vet, race coverage (80.1% total),
build, protocol/install smoke, shell syntax and ShellCheck, all six Skill
validators, Actionlint, govulncheck, four-platform archives, and a five-second
endpoint fuzz run (58,666 executions) also passed.

Azure Voice Live now has an explicit public-cloud endpoint contract. The
official Azure Speech sovereign-cloud tables mark Voice Live unsupported in
both Azure Government and Azure operated by 21Vianet, so guessed sovereign
hosts, nested/custom/lookalike hosts, and direct private-DNS-zone names fail
before token resolution. The same hardening rejects the reserved `privatelink`
zone label for Azure OpenAI Realtime. Normal public resource hostnames remain
compatible with Azure Private Link because VNet DNS resolves those unchanged
names to private endpoints. Implementation commit
`8865168f9feecb1921ac65ac76182563227b94ae` passed remote macOS, Ubuntu,
ShellCheck, Actionlint, govulncheck, and four-platform release verification in
[CI run 30812707583](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30812707583).
Fresh local module verification, formatting, vet, race coverage (80.1% total),
build, protocol/install smoke, shell syntax and ShellCheck, all six Skill
validators, Actionlint, govulncheck, four-platform archives, a five-second
Voice Live host fuzz run (130,789 executions), and a five-second Realtime host
fuzz run (149,503 executions) also passed.

Azure Web PubSub standard/reliable JSON/Protobuf and MQTT WSS clients now
enforce Microsoft's Private Endpoint and resource-name contracts. Both paths
accept only a 3-63 character, letter-first single-label
`<resource>.webpubsub.azure.com` host. Private Endpoint callers keep that
public resource URL and let VNet DNS resolve it privately; direct
`privatelink.webpubsub.azure.com`, nested private-DNS-zone names, numeric-first
resources, custom hosts, and lookalikes fail before Entra or generated client
token resolution. Implementation commit
`cac73504b5b2d9c3c2ab56543e48d2cb739f13e2` passed remote macOS, Ubuntu,
ShellCheck, Actionlint, govulncheck, and four-platform release verification in
[CI run 30813832471](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30813832471).
Fresh local module verification, formatting, vet, race coverage (80.1% total),
build, protocol/install smoke, shell syntax and ShellCheck, all six Skill
validators, Actionlint, govulncheck, four-platform archives, and a five-second
shared standard/MQTT endpoint fuzz run (170,545 executions) also passed.

Azure SignalR Service serverless JSON-hub WSS now uses `auth_scheme=signalr-ws`:
the gateway requests the documented `https://signalr.azure.com/.default`,
calls the fixed `POST /api/hubs/{hub}/:generateToken?api-version=2022-11-01`
with bounded `minutesToExpire` and optional `userId`, and dials the exact
public single-label `<resource>.service.signalr.net/client/?hub=<hub>` URL
with the client token only in the internal `access_token` query parameter.
The hub name follows the data-plane swagger contract (alphabetic start,
alphanumeric or underscore only); `privatelink` labels, nested subdomains,
numeric-first resources, lookalike hosts, sovereign guesses, caller headers,
and caller query parameters fail before token resolution. The in-band JSON Hub
Protocol handshake, `0x1E` record-separator framing, Ping/Close handling, and
Completion error rules follow the official Hub Protocol specification;
unsupported message types, invalid frames, error Completions, and missing
terminal Completions fail closed with atomic output. Read-only `Subscribe`
auto-replies void Completion frames for server-to-client Invocations;
mutation-only `Invoke` correlates StreamItem/Completion frames to unique
bounded invocation IDs. Hermetic boundary, token-minting, frame-parser,
sanitization, atomic-failure, and correlation tests plus the lookalike
pre-credential protocol smoke pass; fresh local formatting, vet, race coverage
(80.4% total, 80.5% cloud package), build, protocol smoke, and four-platform
build remain green. A dedicated live gate remains pending an operator identity
with the `SignalR REST API Owner` or `SignalR Service Owner` role, an existing
SignalR Service resource, and a hub.

Azure OpenAI Chat Completions streaming now uses `auth_scheme=openai-chat-stream`:
the gateway requests the documented `https://cognitiveservices.azure.com/.default`
scope through the non-CLI Azure Identity chain, posts to the exact public
single-label `https://<resource>.openai.azure.com/openai/deployments/<deployment>/chat/completions`
URL with a date-structured `api_version` and an internally injected
`"stream": true`, and requires the `text/event-stream` content type. The finite
body plan binds `model` to the URL deployment, 1–128 messages with
`system|user|assistant|developer` roles and bounded string/text-part content,
optional `max_tokens`/`temperature`, and `max_events`/`timeout_seconds`
bounds; caller query parameters, headers, api-key/stream fields, sovereign
hosts, nested subdomains, direct `privatelink` labels, and lookalikes fail
before token resolution. Every `chat.completion.chunk` is validated against
the documented shape (id/object/created/model/choices/delta/finish_reason),
unknown events or credential-bearing chunks fail closed, `data: [DONE]` or the
finite event/time bound terminates the read, and sanitized chunk NDJSON is
published atomically only after success. Hermetic boundary, plan, transport,
chunk-shape, sentinel, atomic-failure, and partial-timeout tests plus the
lookalike pre-credential protocol smoke pass. A dedicated read-only streaming
live gate remains pending an operator identity with the `Cognitive Services
OpenAI User` role, an existing deployment, and a current `api-version`.

Azure OpenAI Responses streaming now uses `auth_scheme=openai-responses-stream`
on the official GA `/openai/v1/responses` route: the gateway requests the
REST-reference `https://cognitiveservices.azure.com/.default` scope through
the non-CLI Azure Identity chain, posts to the exact public single-label
`https://<resource>.openai.azure.com/openai/v1/responses` URL with an optional
documented `api_version` (`v1`, `preview`, date-structured, or omitted for the
v1 GA default), injects `"stream": true` and `"store": false` internally so no
response state is retained, and requires the `text/event-stream` content type.
The finite body plan binds `model` to one bounded deployment name, `input` to a
bounded string or 1–64 `user|system|developer|assistant` message items with
string or `input_text` part content, optional `max_output_tokens`/`temperature`,
and `max_events`/`timeout_seconds` bounds; caller query parameters, headers,
api-key/stream/store fields, sovereign hosts, nested subdomains, direct
`privatelink` labels, and lookalikes fail before token resolution. Every event
is validated against the documented `ResponseStreamEvent` union (type
allowlist, required `sequence_number`, bounded delta/text, and terminal
`response.id`/`object`); `response.completed`/`response.incomplete` terminate
the read, `error`/`response.failed` fail closed, unknown or credential-bearing
events and missing terminals fail atomically, and sanitized event NDJSON is
published only after success. Hermetic boundary, plan, transport,
event-envelope, terminal, atomic-failure, and partial-timeout tests plus the
lookalike pre-credential protocol smoke pass. A dedicated read-only streaming
live gate remains pending an operator identity with the `Cognitive Services
OpenAI User` role, an existing deployment, and a current Responses route.

The remaining protocol-family audit is recorded as explicit mappings rather
than open placeholders: AWS MSK Kafka and ElastiCache/MemoryDB ValKey/Redis
RESP are custom client line protocols (IAM, SASL/SCRAM/mTLS, or RESP AUTH),
AWS Elemental MediaConnect flow inputs are the Zixi/SRT/RTP/RIST UDP media
plane, Azure Event Hubs Kafka requires SASL/PLAIN `$ConnectionString` plus the
connection string as password or in-client Entra OAuth, Azure IoT Hub and the
Alibaba IoT Platform and Tencent IoT Explorer device planes authenticate with
device SAS/device-secret/X.509 identities rather than operator IAM, Azure
Speech TTS WebSocket V2 raw WSS is withdrawn to Speech-SDK-only, and Google
Cloud IoT Core was retired on 2023-08-16 with no active Device Manager API. The follow-up audit adds AWS Kinesis Video Streams RTMP/HLS media ingest, Azure Media Services retirement (2024-06-30), the deprecated Azure OpenAI Assistants API (2026-08-26), GCP Memorystore for Redis RESP and Managed Service for Apache Kafka, Alibaba ApsaraMQ for Kafka and ApsaraVideo Live, Tencent CKafka, CSS, and UserSig-bound IM, and Baidu Message Service for Kafka client line protocols as explicit mappings with official-source evidence. Amazon MQ broker wire protocols, Azure Cache for Redis RESP and Azure Relay SAS-token tunnels, Alibaba ApsaraMQ for MQTT device credentials, Tencent TDMQ Pulsar/RabbitMQ, and Baidu Message Service for RabbitMQ AMQP complete the client line protocol mappings. Chime SDK real-time media, MediaPackage delivery, AppStream 2.0 sessions, GCP Live Streaming API, and Alibaba/Baidu RDS database wire protocols finish the remaining media-plane and client line protocol rows. The database wire protocol batch (Azure Database for PostgreSQL/MySQL, Cloud SQL, ApsaraDB for Redis/Tair, ApsaraMQ for RabbitMQ, TencentDB for Redis, Baidu SCS Redis) and AWS MediaLive media inputs close the remaining client-protocol/media rows. ACS calling RTP/WebRTC and IVS Stage media complete the media-plane rows, while Amazon Connect chat was upgraded from a documented exclusion to the implemented `connect-chat-ws` scheme.
Each entry names the official-source evidence and keeps the provider control
plane addressable through the existing generic adapters.

Google Cloud descriptor-driven ProtoJSON gRPC now gives every
server-streaming method an explicit finite response-message and total-time
bound. This closes the indefinite-wait gap for APIs such as Firestore `Listen`,
Cloud Logging `TailLogEntries`, Speech `StreamingRecognize`, and Pub/Sub
`StreamingPull` without adding service-specific transports: the supplied
official `FileDescriptorSet` proves the RPC shape. The exact Firestore and
Logging observation paths are read-only; lookalike `Listen` methods fail the
read classifier, and Pub/Sub `StreamingPull` remains mutation-gated because
its bidirectional requests can acknowledge messages or change ack deadlines.
Complete frames are atomically published up to the caller's bound; partial
frames and any already-known nonzero `grpc-status` fail closed. Implementation
commit `b1843abe89c9d7007c8c0bd8024b93f2e7a22697` passed remote macOS,
Ubuntu, ShellCheck, Actionlint, govulncheck, and four-platform release
verification in [CI run 30815515763](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30815515763).
Fresh local module verification, formatting, vet, race coverage (80.1% total,
80.2% cloud package), build, protocol/install smoke, shell syntax, all six
Skill validators, Actionlint, govulncheck, four-platform archives, and a
five-second stream-bound fuzz run (126,616 executions) also passed.

Alibaba Cloud ApsaraMQ for RocketMQ 4.x now has a guarded `auth_scheme=mq`
data-plane session over direct HTTPS. It implements the provider's distinct
`MQ` HMAC-SHA1, `x-mq-version=2015-06-06`, XML body-MD5, and AKSK/STS rules;
publishes normal, ordered, scheduled, or transactional messages; and performs
normal/order/transaction-half pull with in-call acknowledge, release, commit,
or rollback. Every pull is mutation-only. `ReceiptHandle` is parsed only inside
the invocation, output is written and bounded before settlement, and the
mode-0600 NDJSON target is atomically published only after all required
follow-up requests succeed. Caller headers, raw HTTP, body files, standalone
handle operations, and credential fields are rejected before credential
resolution. RocketMQ 5.x control-plane resources remain ACS3-addressable; the
public 5.x message plane is recorded as a credential-bound exclusion because
the provider requires instance ACL username/password rather than this Goal's
AKSK/IAM entrypoint. Implementation commit
`190cedeba29f9e074479503b58db5fb33e5f0623` passed remote macOS, Ubuntu,
ShellCheck, Actionlint, govulncheck, and four-platform release verification in
[CI run 30819146895](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30819146895).
The opt-in, default-release real data-plane acceptance gate was added in commit
`57179716d77766615003ad2fe1701a1c50b1bb8d` and passed the same remote gates in
[CI run 30819632334](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30819632334).
Fresh local module verification, formatting, vet, race coverage (80.2% total,
80.3% cloud package), build, protocol/install smoke, shell syntax, all six
Skill validators, Actionlint, govulncheck, four-platform archives, and a
ten-second receipt/property sanitizer fuzz run (181,068 executions) also
passed. An unsigned TLS transport probe against the documented `mqrest`
endpoint form completed certificate verification and received an AliyunMQS
protocol response over HTTPS; AKSK authorization remains pending the operator
live gate and is not inferred from that transport-only result.

Tencent Cloud CLS now has a distinct `auth_scheme=cls` direct-HTTPS path for
the still-published legacy data-plane contract. It implements the provider's
`q-sign-algorithm=sha1` canonical method/path/query/header signature and a
15-minute authorization window, while current resource management and new CLS
features remain routed through API 3.0 TC3. Only exact single-region public
`<region>.cls.tencentcs.com` and same-region internal
`<region>.cls.tencentyun.com` hosts are accepted. Nested/lookalike endpoints,
caller Authorization, q-sign credential parameters, and `X-Cls-Token` fail
before credential resolution; CAM temporary tokens are added and signed only
inside the adapter. The implementation includes a fixed signer vector, STS
header containment, exact endpoint/service tests, request-ID capture,
pre-credential rejection, protocol smoke coverage, and an endpoint fuzz target.
Implementation commit `053afda0e2b0013ee7bc66d27270dc3e422c768a`
passed remote macOS, Ubuntu, ShellCheck, Actionlint, govulncheck, and
four-platform release verification in
[CI run 30821604602](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30821604602).
The opt-in real q-sign `GetLogset` acceptance gate was added in commit
`f8bcbded42433dd08fca5f8f45e0bb0e12a5bd11` and passed the same remote gates
in [CI run 30822139007](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30822139007).
Fresh local module verification, formatting, vet, race coverage (80.3%), build,
protocol/install smoke, shell syntax, all six Skill validators, Actionlint,
govulncheck, four-platform archives, and a five-second endpoint fuzz run
(64,633 executions) also passed. Real CLS authorization remains pending the
operator live gate and is not inferred from hermetic signing tests.

Tencent Cloud TCR Enterprise Registry now has a distinct
`auth_scheme=tcr-registry` direct-HTTP path. It validates exact default public,
VPC, or operator-pinned custom endpoints plus Registry methods and repository
paths before resolving CAM credentials; internally sends only the fixed TC3
`CreateInstanceToken(TokenType=temp)` request; rejects long-term, expired, or
malformed results; and contains the temporary username/JWT inside Registry
Basic authorization. Version/catalog, manifests, blobs, tags, referrers,
monolithic and chunked uploads, cross-repository mounts, fail-closed redirects,
response-file handling, policy/schema exposure, Skills, official references,
and a sanitized read-only ListTags live gate are included. Personal Edition is
an explicit credential-bound mapping because its documented login requires a
separately configured Registry username/password rather than CAM AKSK/IAM.
Implementation commit `96de20136ef30d64b374b4df4a3edddfd58b98dc`
passed remote macOS, Ubuntu, ShellCheck, Actionlint, govulncheck, and
four-platform release verification in
[CI run 30839370163](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30839370163).
Fresh local module verification, formatting, vet, race coverage (80.1%), build,
protocol/install smoke, shell syntax, all six Skill validators, Actionlint,
govulncheck, four-platform archives, and a ten-second endpoint/path fuzz run
(32,980 executions) also passed. Real TCR authorization remains pending the
operator live gate and is not inferred from hermetic TC3/Registry tests.

Baidu CCR Enterprise and Personal Registry now have a distinct
`auth_scheme=ccr-registry` direct-HTTP path. The gateway validates exact
official public/VPC/Personal or operator-pinned custom endpoints and every
Registry path/method before resolving BCE credentials. Enterprise performs
only the fixed BCE v1 IAM-user lookup and one-hour instance-password request;
Personal performs only the fixed current-user lookup and one-hour token
request. Both then validate an exact same-origin `/service/token` challenge,
derive catalog/repository scopes, and keep temporary login, Basic and Bearer
material out of MCP, audit and error output. Version/catalog, manifests, blobs,
tags, referrers, monolithic/chunked uploads, cross-repository mounts,
response-file handling, redirect rejection, policy/schema exposure, Skills,
official references and a sanitized ListTags live gate are included. Real CCR
authorization remains pending the operator live gate and is not inferred from
hermetic BCE/Registry tests.
Implementation commit `b310648d69dcf7801ff49d88412e093411a6cc9e`
passed remote macOS, Ubuntu, ShellCheck, Actionlint, govulncheck and
four-platform release verification in
[CI run 30841509268](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30841509268).
Fresh local module verification, formatting, vet, race coverage (80.1%), build,
protocol/install smoke, shell syntax, all six Skill validators, Actionlint,
govulncheck, four-platform archives, and a ten-second CCR endpoint/path fuzz
run (134,477 executions) also passed.

Baidu IoT Core MQTT 3.1.1 WSS now has a distinct
`auth_scheme=iotcore-mqtt-ws` direct-protocol path. It validates the exact
single-instance `/mqtt` endpoint and credential-free bounded plan before
resolving BCE IAM AK/SK; matches the official application-permission HMAC
example vector; and implements CONNECT, QoS 0/1 SUBSCRIBE/PUBLISH/PUBACK,
provider-compliant batching for 100 subscriptions per connection, the 32 KiB
default and explicit 128 KiB approved-instance payload bounds, PING,
DISCONNECT, Will configuration, atomic subscription output, read/mutate
separation, schema/Skills discovery, and credential/error containment. The
documented credential has no STS session-token field, so session credentials
fail closed. Hermetic unit and MCP protocol-smoke tests pass locally. Real
broker acceptance remains pending `TestLiveBaiduIoTCoreMQTTReadOnly` with an
operator-bound IAM application and one message staged after subscription; no
live success is inferred from the official signature vector.
Implementation commit `ac60a54f41b25ba334c1ac67fdca5ea27ab7eb85`
passed remote macOS, Ubuntu, ShellCheck, Actionlint, govulncheck, and
four-platform release verification in
[CI run 30844840502](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30844840502).
Fresh local module verification, formatting, vet, race coverage (80.1%), build,
protocol/install smoke, shell syntax, all six Skill validators, Actionlint,
govulncheck, four-platform archives, and a ten-second endpoint/plan fuzz run
(68,953 executions) also passed.

Baidu IoT Core HTTP Publish now has a separate mutation-only
`auth_scheme=iotcore-http-pub` direct-HTTPS path. It validates the exact
single-instance `/pub` endpoint and credential-free topic/QoS/payload plan
before resolving IAM AK/SK; reuses the official application-permission
signature vector; performs fixed same-origin `/auth` and `/pub` requests; and
keeps the 60-second token plus derived username/password internal. The 32 KiB
default and explicit 128 KiB approved-instance payload bounds, topic rules,
50 QPS/IP pacing, strict success response, body-file policy, mutation gate,
protocol smoke, and credential/error containment are covered hermetically.
Real cloud acceptance remains pending the explicit
`TestLiveBaiduIoTCoreHTTPPubMutation` gate with an operator-bound IAM
application and disposable topic; no live success is inferred from hermetic
HTTP or signature tests.
Implementation commit `1a5a9dd6416c09278d2c0868d74d76601249c10d`
passed remote macOS, Ubuntu, ShellCheck, Actionlint, govulncheck, and
four-platform release verification in
[CI run 30846516291](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30846516291).
Fresh local module verification, formatting, vet, race coverage (80.1%), build,
protocol/install smoke, shell syntax, all six Skill validators, Actionlint,
govulncheck, four-platform archives, and a ten-second HTTP endpoint/plan fuzz
run (244,437 executions) also passed.

Baidu IoT Core MQTT now also implements the current official MQTT 5.0 and QoS
2 contract while retaining MQTT 3.1.1 compatibility. The guarded client covers
wildcard/shared QoS 0/1/2 subscriptions, mutation-gated batched unsubscribe and
UNSUBACK reason validation, bidirectional duplicate-safe
PUBREC/PUBREL/PUBCOMP, application and Will properties, message expiry, Will
delay, Receive Maximum, broker packet/QoS capabilities, and server DISCONNECT.
It uses a Baidu-specific bounded packet codec so the documented approved 128
KiB payload ceiling remains usable as payload rather than being confused with
an entire MQTT packet ceiling. The dedicated live gate now selects MQTT 5 and
requests QoS 2; real broker acceptance still requires an operator-bound IAM
application and one staged message. Implementation commit
`f80ba600d8ddacd981841ceb81b42fea32c47891` passed remote macOS, Ubuntu,
ShellCheck, Actionlint, govulncheck, and four-platform release verification in
[CI run 30849781456](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30849781456).
Fresh local module verification, formatting, vet, race coverage (80.1%), build,
protocol/install smoke, shell syntax, all six Skill validators, Actionlint,
govulncheck, four-platform archives, and a ten-second MQTT 5 packet/property
fuzz run (17,808 executions) also passed.

AWS IoT Core now has direct SigV4-presigned WSS clients for both MQTT 3.1.1
and MQTT 5.0. The existing read-only subscriber remains available, while the
new mutation-gated client adds bounded QoS 0/1 publish, subscribe and
unsubscribe, PUBACK handling, retained messages, Last Will, clean and
persistent sessions, offline-queue draining, and the broker-supported MQTT 5
application/session properties. It validates negotiated broker capabilities
and every version-specific reason code, applies documented publish and retained
message pacing, requires atomic mode-0600 output whenever messages may arrive,
and keeps IAM credentials and the SigV4 query signature internal. AWS IoT Core
does not support MQTT QoS 2, so QoS 2 and other broker-unsupported MQTT 5
features fail closed rather than being advertised. The dedicated live gate
performs a QoS 1 self-publish through a new MQTT 5 persistent session and then
expires that session cleanly; real broker acceptance remains pending an
operator IAM identity, data endpoint, disposable topic, and unique client ID.
Implementation commit `b462f43efd4ae922b9a9a13e1ef7c0c2b2d55d70`
passed remote macOS, Ubuntu, ShellCheck, Actionlint, govulncheck, and
four-platform release verification in
[CI run 30853251779](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30853251779).
Fresh local module verification, formatting, vet, race coverage (80.1% total
and cloud package), build, protocol/install smoke, shell syntax, all six Skill
validators, Actionlint, govulncheck, four-platform archives, and a ten-second
MQTT 5 packet/property fuzz run (88,706 executions) also passed.

Amazon IVS Chat messaging now uses `auth_scheme=ivs-chat-ws`: the server signs
the fixed HTTPS `CreateChatToken` call through the AWS SDK credential chain,
derives only view, send, or moderation capabilities from the selected MCP
operation, and passes the single-use token only as the internal WebSocket
subprotocol. Read-only `SubscribeChat`, mutation-gated `ClientChat` sends with
10 requests/second pacing, and sensitive-gated `ModerateChat` are separated by
policy; the seven current regional endpoints, room ARN, user/session bounds,
1 KiB attributes, 500-code-point messages, MESSAGE/EVENT/ERROR frames, and
atomic failure output are enforced without exposing the token, SigV4
authorization, or STS material. Hermetic token-signing, capability, endpoint,
action, frame, policy, pacing, dial, and atomic-failure tests plus the
lookalike pre-credential protocol smoke pass; fresh local formatting, vet,
race coverage (80.3% total, 80.3% cloud package), build, protocol smoke, and
four-platform build remain green. The dedicated self-message live gate is
implemented and pending an operator IAM identity, exact current regional
endpoint, and a disposable room ARN.

Amazon Lex V2 streaming conversations now use `auth_scheme=lex-v2-conversation`:
the server validates the exact official `runtime-v2-lex.<region>.amazonaws.com`
origin, bot/alias/locale/session URI identifiers, and a credential-free bounded
plan, then signs the `POST /bots/.../conversation` request and every
`STREAMING-AWS4-HMAC-SHA256-EVENTS` frame with the AWS SDK credential chain.
The first event is always the official `ConfigurationEvent` with a fixed
TEXT or `audio/lpcm` response content type, followed by optional bounded
`AudioInputEvent` chunks (official 8 kHz lpcm, up to 256 KiB), single-character
`DTMFInputEvent` frames (`A-D0-9#*`), and/or one to 64 `TextInputEvent` frames.
The response stream accepts the documented TEXT-mode events plus
`AudioResponseEvent` and `PlaybackInterruptionEvent` in AUDIO mode, rejects
unknown, credential-bearing, exception, and mode-inconsistent frames, and
atomically publishes sanitized NDJSON with graceful EOF and timeout handling.
Hermetic event-signing, URI/state-machine, audio/DTMF, sanitization,
atomic-failure, partial-stream, and timeout tests plus the lookalike
pre-credential protocol smoke pass. A dedicated live gate remains pending an
operator IAM identity allowed to call `StartConversation` on an existing bot,
alias, and session.

Amazon Chime SDK Messaging event streaming now uses `auth_scheme=chime-messaging-ws`:
the server signs the fixed `GET https://messaging-chime.<region>.amazonaws.com/endpoints/messaging-session`
request with the `chime` signing name through the AWS SDK credential chain,
rejects any returned endpoint other than the official `wss://data-messaging.chime.aws`
host, and then SigV4-presigns `GET /connect` with a bounded `X-Amz-Expires`,
the official AppInstanceUser ARN as `userArn`, a unique `sessionId`, an
optional `prefetch-on=connect`, and the STS token only inside the canonical
query. The signed URL, STS token, and SigV4 material never enter MCP output.
Read-only `SubscribeMessages` validates the exact official
`data-messaging.chime.aws/connect` URL, bounded ARN/session identifiers, and
every event against the documented `x-amz-chime-event-type` table and
`STANDARD|CONTROL|SYSTEM` message types, decodes bounded `Payload` JSON
strings, and atomically publishes sanitized NDJSON. The `/connect` signature is
verified against the AWS SDK Go v2 presigner on the same request vector.
Hermetic boundary, endpoint-request, signature, event-sanitization,
endpoint-mismatch, and atomic-failure tests plus the lookalike pre-credential
protocol smoke pass; fresh local formatting, vet, race coverage (80.4% total,
80.5% cloud package), build, protocol smoke, and four-platform build remain
green. A dedicated live gate remains pending an operator IAM identity with
`chime:GetMessagingSessionEndpoint` on `*` and `chime:Connect` on an existing
AppInstanceUser ARN.

Amazon Connect chat participant streaming now uses `auth_scheme=connect-chat-ws`:
the server signs the fixed `PUT https://connect.<region>.amazonaws.com/contact/chat`
request with the `connect` signing name through the AWS SDK credential chain,
calls `POST https://participant.connect.<region>.amazonaws.com/participant/connection`
with the returned participant token only in the internal `X-Amz-Bearer`
header, validates the returned `wss://participant.connect.<region>.amazonaws.com/participant/connect`
URL (scheme, host, path, and bounded credential query), and dials it directly.
It then publishes the official `{"topic":"aws/subscribe","content":{"topics":["aws/chat"]}}`
frame, waits for subscription success, ignores `aws/heartbeat`/`aws/ping`
frames, and validates every `aws/chat` content object against the documented
`Type`/`ParticipantRole` tables with bounded Id/AbsoluteTime/Content/ContentType
fields before atomic sanitized NDJSON publication. After subscription success the gateway also sends the caller's bounded
`messages` through `POST /participant/message` with the connection token only in
the internal `X-Amz-Bearer` header and a generated idempotent `ClientToken`,
counting them in `sent_messages` metadata while the echoed events flow through
the sanitized NDJSON stream. The participant token,
connection token, and dial URL never enter MCP output; a dedicated live gate
starts a new chat contact and remains pending an operator IAM identity with
`connect:StartChatContact` and `connectparticipant:CreateParticipantConnection`
permissions plus an existing chat-enabled instance and contact flow.

## Scheme identifier registry

The mapping audit proves every implemented `auth_scheme` is defined, wired,
tested, smoke-covered, and mapped here and in `api-protocol-coverage.md`:

- AWS: `sigv4`, `sigv4a`, `ecr`, `sigv4-ws`, `connect-health-ws`,
  `transcribe-ws`, `iot-mqtt-ws`, `kinesisvideo-signaling-ws`,
  `appsync-event-ws`, `appsync-graphql-ws`, `ivs-chat-ws`,
  `lex-v2-conversation`, `chime-messaging-ws`, `connect-chat-ws`
- Azure: `acr`, `realtime-ws`, `voice-live-ws`, `openai-chat-stream`,
  `openai-responses-stream`, `webpubsub-ws`, `signalr-ws`,
  `webpubsub-mqtt-ws`, `eventgrid-mqtt-ws`, `servicebus-amqp-ws`,
  `eventhubs-amqp-ws`
- Google Cloud: `artifact-registry`, `firebase-sse`, `grpc`, `vertex-live-ws`
- Alibaba Cloud: `acs3`, `rpc`, `roa`, `datahub`, `opensearch`, `odps`,
  `odps4`, `fc`, `fc3`, `fc-custom`, `oss`, `oss4`, `sls`, `sls4`, `mns`,
  `mq`, `acr-registry`, `ots`, `ots4`, `nls-rest`, `nls-ws`
- Tencent Cloud: `tc3`, `tc1`, `tc1-sha256`, `qcloud`, `qcloud-sha256`,
  `cos`, `cls`, `tcr-registry`, `asr-ws`, `virtual-number-ws`, `soe-ws`,
  `speech-translate-ws`, `voice-convert-ws`, `mps-ws`, `mps-tts-ws`,
  `tts-ws`, `tts-stream-ws`, `podcast-ws`
- Baidu AI Cloud: `ccr-registry`, `iotcore-http-pub`, `iotcore-mqtt-ws`,
  `rtc-aiagent-ws`

## Live acceptance command

Run from the repository root after credentials are injected into the test
process through official provider identity mechanisms:

```bash
CLOUD_SKILLS_LIVE_TEST=1 \
go test ./internal/mcp/cloud -run TestLiveSixCloudReadOnly -v
```

`CLOUD_SKILLS_LIVE_GCP_PROJECT` is required for the Google Cloud probe.
`CLOUD_SKILLS_LIVE_PROVIDERS` can select a comma-separated subset while
credentials are staged. A provider passes only when the API read succeeds and
the test observes a matching `outcome=succeeded` audit event. Before the call,
the test logs only adapter availability and the non-secret credential
source/status; `unverified` is expected for lazy official identity chains. The
live test never calls a mutate tool or prints response bodies.

For the guarded RocketMQ 4.x data-plane acceptance gate, stage one matching
message first. The default settlement is `release`, so the broker can redeliver
the staged message:

```bash
CLOUD_SKILLS_ALLOW_MUTATIONS=1 \
CLOUD_SKILLS_LIVE_ALIBABA_MQ=1 \
CLOUD_SKILLS_LIVE_ALIBABA_MQ_ENDPOINT=https://...mqrest... \
CLOUD_SKILLS_LIVE_ALIBABA_MQ_TOPIC=... \
CLOUD_SKILLS_LIVE_ALIBABA_MQ_CONSUMER=... \
CLOUD_SKILLS_LIVE_ALIBABA_MQ_INSTANCE=... \
go test ./internal/mcp/cloud -run TestLiveAlibabaMQMutation -v
```

`CLOUD_SKILLS_LIVE_ALIBABA_MQ_INSTANCE` is optional for endpoints that do not
require a namespace. Set `CLOUD_SKILLS_LIVE_ALIBABA_MQ_SETTLEMENT=acknowledge`
only when consuming the staged message permanently is intentional.

For Alibaba Cloud ACR Enterprise Registry HTTP, provide the exact public or
VPC Registry origin, instance ID, and an existing readable repository. The
test uses credentials-go RAM AKSK/STS, keeps both temporary login and Bearer
credentials internal, and performs `/tags/list` without printing its body:

```bash
CLOUD_SKILLS_LIVE_ALIBABA_ACR=1 \
CLOUD_SKILLS_LIVE_ALIBABA_ACR_ENDPOINT=https://demo-registry.cn-hangzhou.cr.aliyuncs.com \
CLOUD_SKILLS_LIVE_ALIBABA_ACR_INSTANCE_ID=cri-xxxxxxxx \
CLOUD_SKILLS_LIVE_ALIBABA_ACR_REPOSITORY=team/app \
go test ./internal/mcp/cloud -run TestLiveAlibabaACRReadOnly -v
```

For the distinct Tencent CLS q-sign path, provide an existing logset ID and its
exact regional endpoint:

```bash
CLOUD_SKILLS_LIVE_TENCENT_CLS=1 \
CLOUD_SKILLS_LIVE_TENCENT_CLS_ENDPOINT=https://ap-shanghai.cls.tencentcs.com \
CLOUD_SKILLS_LIVE_TENCENT_CLS_LOGSET_ID=... \
go test ./internal/mcp/cloud -run TestLiveTencentCLSReadOnly -v
```

This gate is read-only and records only sanitized outcome, byte count, and
provider request ID evidence.

For Tencent TCR Enterprise Registry HTTP, provide the exact public, VPC, or
operator-pinned custom Registry origin, instance ID, region, and an existing
readable namespaced repository. The test uses only CAM AKSK/STS, mints a
one-hour temporary Registry credential internally, and performs `/tags/list`
without printing its body:

```bash
CLOUD_SKILLS_LIVE_TENCENT_TCR=1 \
CLOUD_SKILLS_LIVE_TENCENT_TCR_ENDPOINT=https://demo-tcr.tencentcloudcr.com \
CLOUD_SKILLS_LIVE_TENCENT_TCR_INSTANCE_ID=tcr-xxxxxxxx \
CLOUD_SKILLS_LIVE_TENCENT_TCR_REGION=ap-guangzhou \
CLOUD_SKILLS_LIVE_TENCENT_TCR_REPOSITORY=team/app \
go test ./internal/mcp/cloud -run TestLiveTencentTCRReadOnly -v
```

This gate is read-only and records only sanitized outcome, byte count, and
provider request ID evidence. Personal Edition is not accepted because its
separate Registry password falls outside the AKSK/IAM-only Goal boundary.

For Baidu CCR Personal Registry HTTP, provide an existing readable namespaced
repository. The test uses only the BCE AKSK/IAM-STS chain, internally derives
the one-hour Personal login and Registry Bearer token, and performs
`/tags/list` without printing its body:

```bash
CLOUD_SKILLS_LIVE_BAIDU_CCR=1 \
CLOUD_SKILLS_LIVE_BAIDU_CCR_ENDPOINT=https://registry.baidubce.com \
CLOUD_SKILLS_LIVE_BAIDU_CCR_REPOSITORY=team/app \
go test ./internal/mcp/cloud -run TestLiveBaiduCCRReadOnly -v
```

For Enterprise CCR, set its exact public/VPC or operator-pinned custom origin
and add the non-secret resource identifiers required by the fixed temporary
credential APIs:

```bash
CLOUD_SKILLS_LIVE_BAIDU_CCR=1 \
CLOUD_SKILLS_LIVE_BAIDU_CCR_ENDPOINT=https://ccr-xxxxxxxx-pub.cnc.bj.baidubce.com \
CLOUD_SKILLS_LIVE_BAIDU_CCR_REPOSITORY=team/app \
CLOUD_SKILLS_LIVE_BAIDU_CCR_REGION=bj \
CLOUD_SKILLS_LIVE_BAIDU_CCR_INSTANCE_ID=ccr-xxxxxxxx \
CLOUD_SKILLS_LIVE_BAIDU_CCR_USER_ID=<iam-user-id> \
go test ./internal/mcp/cloud -run TestLiveBaiduCCRReadOnly -v
```

For Baidu IoT Core MQTT, bind the same long-lived BCE IAM AK/SK as an
application permission on the instance, clear BCE session-token variables,
and publish one matching message only after the subscriber starts:

```bash
CLOUD_SKILLS_LIVE_BAIDU_IOTCORE_MQTT=1 \
CLOUD_SKILLS_LIVE_BAIDU_IOTCORE_MQTT_ENDPOINT=wss://<iot-core-id>.iot.gz.baidubce.com/mqtt \
CLOUD_SKILLS_LIVE_BAIDU_IOTCORE_MQTT_TOPIC='sensors/test' \
CLOUD_SKILLS_LIVE_BAIDU_IOTCORE_MQTT_CLIENT_ID='cloud-skills-live-unique' \
go test ./internal/mcp/cloud -run TestLiveBaiduIoTCoreMQTTReadOnly -v
```

This gate is read-only, atomically records one received message, and checks a
sanitized succeeded audit event. It does not print the message or credentials.

For Baidu IoT Core HTTP Publish, bind the same long-lived BCE IAM AK/SK as an
application permission, clear BCE session-token variables, choose a disposable
topic, and explicitly approve the mutation:

```bash
CLOUD_SKILLS_LIVE_BAIDU_IOTCORE_HTTP_PUB=1 \
CLOUD_SKILLS_LIVE_BAIDU_IOTCORE_HTTP_PUB_ENDPOINT=https://<iot-core-id>.iot.gz.baidubce.com/pub \
CLOUD_SKILLS_LIVE_BAIDU_IOTCORE_HTTP_PUB_TOPIC='commands/test' \
CLOUD_SKILLS_LIVE_BAIDU_IOTCORE_HTTP_PUB_PAYLOAD_BASE64='Y2xvdWQtc2tpbGxzLWxpdmU=' \
go test ./internal/mcp/cloud -run TestLiveBaiduIoTCoreHTTPPubMutation -v
```

The test enables mutation approval only inside its runtime, validates the
documented `{"message":"ok"}` response and succeeded audit event, and does not
print payload or credentials.

For AWS IoT Core MQTT, bind an IAM identity authorized for the disposable test
topic, provide the account-specific data endpoint and region, and use a unique
client ID. The mutation gate creates a persistent MQTT 5 session, subscribes,
self-publishes one QoS 1 message, validates the received payload, and expires
the session without leaving a retained message:

```bash
CLOUD_SKILLS_LIVE_AWS_IOT_MQTT=1 \
CLOUD_SKILLS_LIVE_AWS_IOT_MQTT_ENDPOINT=wss://<account-endpoint>-ats.iot.<region>.amazonaws.com/mqtt \
CLOUD_SKILLS_LIVE_AWS_IOT_MQTT_REGION=<region> \
CLOUD_SKILLS_LIVE_AWS_IOT_MQTT_TOPIC='cloud-skills/live-test' \
CLOUD_SKILLS_LIVE_AWS_IOT_MQTT_CLIENT_ID='cloud-skills-live-unique' \
go test ./internal/mcp/cloud -run TestLiveAWSIoTMQTTMutation -v
```

The test enables mutation approval only inside its runtime and does not print
the payload, presigned URL, credentials, or session token.

For Amazon IVS Chat, bind an IAM identity allowed to call `CreateChatToken` on
a disposable room. The gate creates a one-minute least-capability chat token
internally, sends a unique non-secret message, receives its room echo, and
verifies that no token or AWS credential entered the atomic output:

```bash
CLOUD_SKILLS_ALLOW_MUTATIONS=1 \
CLOUD_SKILLS_LIVE_AWS_IVS_CHAT=1 \
CLOUD_SKILLS_LIVE_AWS_IVS_CHAT_ENDPOINT=wss://edge.ivschat.<region>.amazonaws.com \
CLOUD_SKILLS_LIVE_AWS_IVS_CHAT_REGION=<region> \
CLOUD_SKILLS_LIVE_AWS_IVS_CHAT_ROOM_ARN=arn:aws:ivschat:<region>:<account>:room/<room-id> \
CLOUD_SKILLS_LIVE_AWS_IVS_CHAT_USER_ID=cloud-skills-live \
go test ./internal/mcp/cloud -run TestLiveAWSIVSChatMutation -v
```

The gate does not print the generated content, token, signed request, or
credentials. It intentionally leaves one disposable chat message in the room.

For Amazon Chime SDK Messaging, bind an IAM identity with
`chime:GetMessagingSessionEndpoint` on `*` and `chime:Connect` on an existing
AppInstanceUser ARN. The gate signs `GetMessagingSessionEndpoint` and the
`/connect` WebSocket internally, observes up to one event or the bounded
timeout, and verifies that no signed URL, STS token, or credential entered the
atomic output:

```bash
CLOUD_SKILLS_LIVE_AWS_CHIME_MESSAGING=1 \
CLOUD_SKILLS_LIVE_AWS_CHIME_MESSAGING_REGION=us-east-1 \
CLOUD_SKILLS_LIVE_AWS_CHIME_MESSAGING_USER_ARN=arn:aws:chime:<region>:<account>:app-instance/<app-instance-id>/user/<user-id> \
go test ./internal/mcp/cloud -run TestLiveAWSChimeMessagingSubscribeReadOnly -v
```

The gate succeeds even when the AppInstanceUser receives no channel events
during the observation window; a failed endpoint request, signature, or
WebSocket handshake fails the gate.

For Amazon Connect chat, bind an IAM identity with `connect:StartChatContact`
and `connectparticipant:CreateParticipantConnection` on the instance and a
chat-enabled contact flow. The gate starts one new chat contact, subscribes to
`aws/chat`, observes up to four events or the bounded timeout, and verifies
that no participant token, connection token, dial URL, or credential entered
the atomic output:

```bash
CLOUD_SKILLS_ALLOW_MUTATIONS=1 \
CLOUD_SKILLS_LIVE_AWS_CONNECT_CHAT=1 \
CLOUD_SKILLS_LIVE_AWS_CONNECT_CHAT_REGION=us-east-1 \
CLOUD_SKILLS_LIVE_AWS_CONNECT_CHAT_INSTANCE_ID=<instance-uuid> \
CLOUD_SKILLS_LIVE_AWS_CONNECT_CHAT_CONTACT_FLOW_ID=<contact-flow-arn-or-uuid> \
go test ./internal/mcp/cloud -run TestLiveAWSConnectChatObserve -v
```

The gate succeeds even when no chat messages arrive during the observation
window; a failed signed contact creation, participant connection, or WebSocket
handshake fails the gate.

For Azure SignalR Service, bind a managed identity or service principal with
the `SignalR REST API Owner` or `SignalR Service Owner` role on the resource.
The gate calls the fixed `:generateToken` data-plane API internally, completes
the official JSON Hub Protocol handshake, and observes up to one server
Invocation before the bounded timeout without printing the Entra token, client
token, or dial URL:

```bash
CLOUD_SKILLS_LIVE_AZURE_SIGNALR=1 \
CLOUD_SKILLS_LIVE_AZURE_SIGNALR_ENDPOINT=wss://<resource>.service.signalr.net/client/?hub=<hub> \
go test ./internal/mcp/cloud -run TestLiveAzureSignalRSubscribeReadOnly -v
```

The gate succeeds even when the hub emits no messages during the observation
window; a failed Entra token request or handshake fails the gate.

For Azure OpenAI Chat Completions streaming, bind a managed identity or
service principal with the `Cognitive Services OpenAI User` role on the Azure
OpenAI resource. The gate injects `stream:true` internally, observes up to one
`chat.completion.chunk` or the bounded timeout, and verifies that no Entra
token or api key entered the atomic output:

```bash
CLOUD_SKILLS_LIVE_AZURE_OPENAI_STREAM=1 \
CLOUD_SKILLS_LIVE_AZURE_OPENAI_STREAM_ENDPOINT=https://<resource>.openai.azure.com/openai/deployments/<deployment>/chat/completions \
CLOUD_SKILLS_LIVE_AZURE_OPENAI_STREAM_API_VERSION=2024-06-01 \
go test ./internal/mcp/cloud -run TestLiveAzureOpenAIStreamRead -v
```

The gate succeeds even when the model returns no text before the event bound;
a failed Entra token request or stream validation fails the gate.

For Azure OpenAI Responses streaming, bind the same `Cognitive Services
OpenAI User` role on the Azure OpenAI resource. The gate injects
`stream:true` and `store:false` internally, observes up to one
`response.output_text.delta` or the bounded timeout, and verifies that no
Entra token or api key entered the atomic output:

```bash
CLOUD_SKILLS_LIVE_AZURE_OPENAI_RESPONSES=1 \
CLOUD_SKILLS_LIVE_AZURE_OPENAI_RESPONSES_ENDPOINT=https://<resource>.openai.azure.com/openai/v1/responses \
CLOUD_SKILLS_LIVE_AZURE_OPENAI_RESPONSES_MODEL=<deployment> \
CLOUD_SKILLS_LIVE_AZURE_OPENAI_RESPONSES_API_VERSION=v1 \
go test ./internal/mcp/cloud -run TestLiveAzureOpenAIResponsesRead -v
```

The gate succeeds even when the model returns no text before the event bound;
a failed Entra token request or stream validation fails the gate.

For private ECR Registry HTTP, provide the exact registry origin and an
existing pullable repository. The test resolves the region from the endpoint,
uses only the AWS SDK credential chain, derives GetAuthorizationToken
internally, and performs `/tags/list` without printing its body:

```bash
CLOUD_SKILLS_LIVE_AWS_ECR=1 \
CLOUD_SKILLS_LIVE_AWS_ECR_ENDPOINT=https://123456789012.dkr.ecr.us-west-2.amazonaws.com \
CLOUD_SKILLS_LIVE_AWS_ECR_REPOSITORY=team/app \
go test ./internal/mcp/cloud -run TestLiveAWSECRReadOnly -v
```

The ECR Public protocol has a separate live gate because it uses the
`ecr-public` service in `us-east-1`, a Bearer token, and does not support the
Registry tags API. Point `CLOUD_SKILLS_LIVE_AWS_ECR_PUBLIC_REPOSITORY` at an
existing `<registry-alias>/<repository>` whose `latest` manifest exists:

```bash
CLOUD_SKILLS_LIVE_AWS_ECR_PUBLIC=1 \
CLOUD_SKILLS_LIVE_AWS_ECR_PUBLIC_ENDPOINT=https://public.ecr.aws \
CLOUD_SKILLS_LIVE_AWS_ECR_PUBLIC_REPOSITORY='<registry-alias>/<repository>' \
go test ./internal/mcp/cloud -run TestLiveAWSECRPublicReadOnly -v
```

# Six-cloud full-resource MCP contract

Date: 2026-08-02
Status: active goal contract

## Completion definition

The product supports AWS, Microsoft Azure, Google Cloud, Alibaba Cloud,
Tencent Cloud and Baidu AI Cloud. "All resources" means every operation that
the provider exposes through its official OpenAPI or REST API
can be addressed through a provider gateway without adding a new Go tool for
each product or resource type.

Coverage is an invocation capability, not an entitlement claim. The caller's
IAM policy, enabled services, account state, region availability and provider
API support remain authoritative. Console-only workflows are outside the API
surface.

## Public MCP surface

The unified `cloud-skills-mcp` stdio server exposes:

- `cloud_provider_status`: report local HTTP adapter readiness, non-secret auth
  source type and a credential status. `available` is not proof of cloud
  authentication; `credential_status=unverified` means the official identity
  chain is resolved lazily by the first API call. Direct BCE signing can report
  local credential-material presence, but only a live read proves validity.
- `<provider>_api_discover`: read official HTTP API/authentication discovery metadata.
- `<provider>_api_read`: invoke an operation classified as read-only.
- `<provider>_api_mutate`: invoke any supported operation after mutation and
  human-approval gates.

Provider prefixes are `aws`, `azure`, `gcp`, `alicloud`, `tencent` and
`baiducloud`. The former TCCLI-backed Tencent compatibility server is not built,
installed, or included in release archives; Tencent now uses the same signed
HTTP gateway as the other providers.

Read tools reject operations that cannot be conservatively classified as
read-only. Such operations remain reachable through the mutation tool with
explicit approval; a caller cannot downgrade an operation by labeling it
"read".

## Universal provider adapters

| Provider | Universal access mechanism | Credential boundary |
|---|---|---|
| AWS | Direct HTTPS with AWS SigV4 and pure-Go SigV4a for multi-region endpoints, including bidirectional HTTP/2 EventStream, plus guarded raw-frame SigV4, Connect Health Medical Scribe, Transcribe, IoT MQTT, AppSync Events, and AppSync GraphQL subscription WSS | AWS SDK chain: IAM Identity Center, profile/role, web identity, instance role, or AK/SK/STS env |
| Azure | Direct HTTPS with Entra Bearer Token and validated audience plus Azure OpenAI Realtime WSS and Entra-backed Web PubSub standard/reliable JSON/Protobuf and MQTT 3.1.1/5.0 WSS | Non-CLI Azure Identity Environment, Workload Identity, or Managed Identity credentials |
| Google Cloud | Google Auth ADC authenticated REST, raw framed-protobuf or operator-approved `FileDescriptorSet`-driven ProtoJSON gRPC over HTTP/2, and bounded Vertex/Gemini Live WSS against validated `googleapis.com` endpoints | ADC, service account, workload identity federation, impersonation, or metadata identity |
| Alibaba Cloud | Direct ACS3 OpenAPI, legacy RPC/ROA V2, DataHub, OpenSearch V3, MaxCompute ODPS v2/v4 project/data/Tunnel, Function Compute classic FC/current Trigger ACS3/custom-domain POP, OSS v1/v4 Header, SLS v1/v4, MNS and OTS v2/v4 signed HTTPS plus guarded NLS recognition/synthesis HTTPS/WSS | Official credentials-go chain: RAM/OIDC/ECS role, STS, or AK/SK env; NLS token is derived and cached internally |
| Tencent Cloud | Direct API 3.0 TC3 and v1 HmacSHA1/HmacSHA256 HTTPS, still-active qcloud API 2017 query/form HTTPS, COS data-plane signed HTTPS, and internally connected realtime ASR/virtual-number detection/SOE evaluation/speech translation/voice conversion/MPS recognition/MPS TTS/standard realtime TTS/streaming-text TTS/large-model podcast signed WSS | SecretId/SecretKey or CAM/STS temporary credentials injected into the server environment; realtime ASR signs its documented temporary token, while WSS protocols without a Token field require the long-lived tuple |
| Baidu AI Cloud | BCE signed HTTPS against validated `baidubce.com` endpoints plus guarded RTC AI Agent BCE-create/private-token-WSS/stop | BCE AK/SK or IAM/STS temporary AK/SK/session token; RTC product license is server-only entitlement material |

The MCP server never returns, logs or writes credential material. Login,
credential creation, credential export and access-token printing are not cloud
resource operations and are not exposed through invoke tools. Credentials are
injected by the operator through the official provider chain.

The invocation boundary hard-rejects credential issuance and export families,
including STS role/session credentials, login or authorization tokens,
AccessKey/API-key creation, service-account private keys, Graph password/key
issuance, and provider `listKeys`/credential-export actions. Mutation and
sensitive-operation switches cannot override this prohibition. Ordinary IAM,
RAM and CAM role, policy, membership and authorization-resource operations
remain available through the guarded resource gateway.

## Request and output boundaries

- The unified server executes no cloud CLI or shell command; signing and token
  acquisition happen in-process before direct HTTPS requests.
- Google schema-driven gRPC loads a bounded binary `FileDescriptorSet` from an
  operator-approved root, derives request and response types only from the URL
  method descriptor, and never invokes `protoc`, `grpcurl`, or `gcloud` at
  runtime. Descriptor bytes remain local; response publication is atomic.
- Provider auth scheme, service/action names, API versions, HTTP methods, URLs,
  headers, query values, body size and response size are bounded.
- AWS SigV4a derives the documented ECDSA P-256 key from the same
  operator-owned AKSK/IAM chain and requires a validated `region_set`; it never
  exposes the derived private key and does not enable presigned URLs.
- AWS SigV4/SigV4a S3 streaming uploads accept only an approved request body and
  generate `aws-chunked` framing, content lengths, seed and chained chunk
  signatures in-process; callers cannot provide signing or transfer-length
  headers.
- SigV4a streaming uses the documented region-independent scope and fixed
  144-character padded DER-ECDSA chunk signatures so the wire length is known
  before header signing; the derived private key never leaves the process.
- AWS SigV4/SigV4a S3 signed-trailer uploads compute CRC32, CRC32C, CRC64NVME, SHA-1,
  or SHA-256 over the decoded payload while streaming, then generate the
  checksum and trailer signatures in-process. Callers select only the
  algorithm and cannot provide checksum values or trailer headers.
- Finite or bidirectional HTTP/2 AWS SigV4 EventStream requests accept only
  bounded, CRC-valid unsigned frames, then use the official SDK stream signer
  to create dated, chained signing envelopes and a terminal frame. Optional
  pacing occurs only between complete logical frames; concurrent responses are
  length/CRC validated and atomically published. Interactive WebSocket sessions
  remain a separate transport family.
- AWS AppSync GraphQL WSS accepts only a single finite IAM-authenticated
  subscription, separately signs the documented HTTPS `/graphql/connect` and
  `/graphql` requests, keeps dynamic subprotocol/start authorization internal,
  validates the realtime message lifecycle, and atomically publishes sanitized
  data payloads before sending `stop`.
- Generic AWS SigV4 WSS signs only exact official or operator-approved
  endpoints and allowed non-credential headers, keeps Authorization and STS
  tokens inside the Upgrade, accepts bounded JSON/text/Base64 client frames,
  and atomically records bounded text/binary responses. Because raw frames can
  have arbitrary effects, this scheme is mutation-only regardless of the
  caller's operation label.
- Connect Health Medical Scribe WSS accepts only the two official regional
  endpoints and six documented session parameters, internally creates the
  maximum-60-second SigV4 URL, chains signatures across configuration, raw
  audio and `END_OF_SESSION` EventStream frames, and atomically publishes only
  validated transcript events after a normal provider close. Starting a
  session is always mutation-only; callers remain responsible for recording
  consent, PHI handling and trained clinical review.
- Google Cloud Vertex/Gemini Live WSS accepts only the official global,
  regional, or `us|eu` multi-region aiplatform endpoint and the v1beta1
  `BidiGenerateContent` path. It obtains the cloud-platform OAuth token from
  ADC, keeps it inside the Upgrade, enforces setup/setupComplete ordering,
  bounds client/server messages and time, dispatches only pre-approved bounded
  tool-response templates with exact provider call IDs, and can recover from
  GoAway or transport loss by retaining the provider resumption handle only in
  memory and replaying only unacknowledged buffered messages. Caller-supplied
  handles are rejected, `newHandle` is stripped from output, and validated
  server JSON is published atomically. Paid stateful sessions are mutation-only.
- Azure Web PubSub JSON/Protobuf WSS accepts only a public-cloud resource endpoint and
  the exact hub path. It obtains an Entra token, calls the fixed five-minute
  Generate Client Token API internally, keeps both tokens out of MCP, requires
  the exact standard/reliable JSON or Protobuf subprotocol, bounds both
  directions, rejects failed protocol acknowledgements, and atomically
  publishes sanitized logical NDJSON. Protobuf mode uses binary frames and the
  official proto3 field schema for Any/binary data and streaming messages.
  Reliable mode keeps recovery state internal, uses
  exact uint64 sequence acknowledgements, suppresses duplicates, and resends
  pending publisher messages for at most the documented one-minute recovery
  window. Sessions are mutation-only.
- Azure Web PubSub MQTT has finite read-only `SubscribeMQTT` and mutation-only
  `ClientMQTT` entrypoints for MQTT 3.1.1 and 5.0. It accepts exact topics and
  alphanumeric client IDs, derives least-privilege join/send roles, mints a
  five-minute token with `clientType=MQTT`, and keeps it in the internal WSS
  Authorization header. The state machine covers publish/subscribe QoS 0/1/2,
  duplicate-safe inbound QoS 2, persistent sessions up to Azure's documented
  30-second recovery guarantee, Last Will, MQTT 5 message/session properties,
  subscription identifiers, broker flow-control limits, keepalive, server and
  client DISCONNECT, and atomic Base64 NDJSON. Azure-unsupported wildcard,
  retained, topic-alias and shared-subscription features are rejected;
  client-certificate and CONNECT username/password authentication are excluded
  by the operator-IAM-only credential boundary.
- Endpoint overrides from MCP input, authorization headers and credential
  management operations are rejected.
- Alibaba NLS accepts only official public `nls-gateway` WSS endpoints and a
  non-secret project AppKey. The server signs the fixed HTTPS RPC V2
  `CreateToken` request internally, validates and caches the temporary token,
  generates all task/message IDs, enforces start/stream/stop/completed order,
  and never returns or audits the token. Public `CreateToken` calls remain
  prohibited by the credential-issuance boundary.
- Direct REST adapters allow HTTPS only and provider-owned hostname suffixes.
  Redirects are disabled so credentials cannot cross host boundaries.
- Local file references are rejected unless their resolved path is below an
  operator-configured `CLOUD_SKILLS_ALLOWED_FILE_ROOTS` entry.
- REST data-plane uploads use `body_file`, never embed file content in MCP
  context, accept regular files only, and are capped at 64 MiB per request;
  provider multipart or resumable APIs cover larger objects.
- Successful response bodies can use `response_file` to stream into a new file
  below an approved root without entering MCP context. Existing targets are
  never overwritten; a mode-0600 same-directory temporary file is synced and
  atomically published only after the bounded download completes. The default
  1 GiB per-call bound is controlled by
  `CLOUD_SKILLS_MAX_RESPONSE_FILE_BYTES`; official `Range` APIs cover larger
  objects. Provider error bodies never create the target file.
- Azure uncommon data-plane endpoints can use a first-class, validated
  `audience` identifier. The value is never a credential: the adapter derives
  the `.default` scope internally.
- Azure Maps `atlas.microsoft.com` and geographic subdomains route to the
  documented Maps Entra resource. Azure Health Data Services FHIR endpoints
  use the service host audience by default, while DICOM endpoints route to the
  documented shared `dicom.healthcareapis.azure.com` resource. Similar-looking
  sibling domains are rejected.
- Baidu endpoint validation includes both the general `*.baidubce.com` service
  plane and the official BOS `*.bcebos.com` object-storage plane.
- Baidu calls use `bce-auth-v1` by default and can select guarded
  `auth_version=v2` with required service/region fields for APIs that mandate
  the region- and service-scoped v2 signature.
- Baidu RTC AI Agent uses `auth_scheme=rtc-aiagent-ws`: the MCP body is a
  credential-free bounded plan, the adapter signs fixed BCE v1 create and stop
  requests, keeps the returned instance token inside the exact WSS URL, and
  processes product license activation from server-only
  `BCE_RTC_LICENSE_KEY`. It supports the six documented WSS audio codecs,
  forces control-plane `config.audiocodec` to match internal WSS `ac`, and
  constructs Opus `ptime`/`plen` only from a validated bounded packet plan.
  The direct AK/SK query connection is forbidden. Every path after create
  attempts stop, and output is published only after a successful terminal
  event and stop.
- New official endpoint exceptions for any provider are operator policy,
  never model input. `CLOUD_SKILLS_<PROVIDER>_ALLOWED_ENDPOINT_HOSTS` accepts
  comma-separated exact DNS hostnames only; schemes, ports, paths, IPs, and
  wildcard/subdomain inheritance are not accepted.
- Responses are capped and credential-shaped fields are redacted before they
  enter MCP output. A `response_file` call returns only path/byte-count/content
  metadata through MCP.

## Approval, audit and retry

- Every mutation requires `CLOUD_SKILLS_ALLOW_MUTATIONS=1`, `force=true` and
  host-side explicit human approval for the concrete provider/action/target.
- Sensitive IAM, key, password, token and secret operations additionally
  require `CLOUD_SKILLS_ALLOW_SENSITIVE=1`.
- `force=true` is a technical acknowledgement, never proof of human approval.
- An optional JSONL audit sink records provider, service/action or method/URL,
  region/project/subscription, classification, outcome and provider request ID.
  It never records full bodies, responses, headers or credentials. Pre-call
  audit failure is fail-closed for mutations and sensitive calls.
- Only conservatively classified read calls may retry transient provider
  failures. Mutations are never automatically retried.

## Skills contract

Each provider directory contains a concise `SKILL.md` that:

1. routes user intent to status, discover, read or mutate MCP tools;
2. requires official API documentation lookup for the exact operation and
   parameters before invocation;
3. identifies the supported credential chains without asking the model to
   print or store secrets;
4. requires explicit approval before mutation or sensitive reads;
5. links to one-level provider references for invocation syntax, endpoints,
   auth and live acceptance.

The Skills must not enumerate a small resource subset as the product boundary.
Examples are navigation aids, not a support allowlist.

## Verification gates

- Contract tests cover all provider tools, schemas and annotations.
- Hermetic fake-adapter tests prove exact HTTP construction and signing vectors,
  credential redaction, endpoint and file-root rejection, mutation/sensitive
  gates, output caps, timeout handling and audit behavior.
- CI runs formatting, vet, race tests, coverage, ShellCheck, actionlint,
  vulnerability scanning, protocol smoke, installer smoke and cross-platform
  release builds.
- Live tests are opt-in, read-only by default and provider-selectable. The
  operator injects credentials and observes provider, audit outcome, response
  size and request ID without printing response bodies. Mutation live tests use
  dedicated disposable resources and separate explicit approval.

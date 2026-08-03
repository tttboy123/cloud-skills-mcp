# Six-cloud Goal completion matrix

Date: 2026-08-03
Overall status: **IN PROGRESS — protocol-family coverage and six-provider live gates pending**

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
| All documented resource APIs addressable without per-resource Go handlers | Generic transport coverage exists for AWS SigV4/SigV4a including finite/bidirectional HTTP/2 signed EventStream, finite mutation-only raw-frame SigV4 WSS, Connect Health Medical Scribe signed-EventStream WSS, standard/Medical/Call Analytics Transcribe WSS, finite IoT MQTT WSS, Kinesis Video WebRTC Signaling Master/Viewer WSS, finite AppSync Events IAM WSS, and finite legacy AppSync GraphQL IAM WSS subscriptions, Azure Entra REST plus OpenAI Realtime, Voice Live model/Agent WSS, Entra-backed Web PubSub standard/reliable JSON/Protobuf WSS and finite MQTT 3.1.1/5.0 clients, Event Grid Namespace Entra MQTT v5 WSS, and Service Bus/Event Hubs AMQP 1.0 over exact public, US Government, or China WSS with internal Entra/CBS, bounded broker operations and no exported message/session locks, GCP ADC REST plus both raw and operator-approved `FileDescriptorSet`-driven ProtoJSON gRPC over HTTP/2 (including Speech v2 bidirectional-streaming vectors) and bounded Vertex/Gemini Live ADC WSS with bounded tool-call dispatch and internal transparent resumption, Alibaba ACS3/RPC V2/ROA V2/DataHub/OpenSearch/MaxCompute ODPS V2/V4/Function Compute classic and current Trigger families/OSS V1/V4/SLS/MNS/RocketMQ 4.x MQ HTTP/OTS plus NLS REST short ASR/TTS and five NLS recognition/synthesis WSS namespaces with internal token and receipt-handle containment, Tencent TC3/API 3.0 v1/legacy qcloud API 2017/COS/CLS q-sign/realtime ASR, virtual-number detection, SOE evaluation, speech-translation, voice-conversion, standard realtime TTS, streaming-text TTS and large-model podcast WSS/MPS private-audio recognition WSS/MPS streaming TTS WSS, and Baidu BCE v1/v2 plus RTC AI Agent BCE-create/private-token-WSS/stop; remaining families and explicit retired/credential-bound mappings are enumerated in `api-protocol-coverage.md` | Official and SDK signature vectors plus transport, policy, and data-plane tests | **Partial — remaining product-specific protocols and live vectors remain** |
| Official AKSK/IAM identity entrypoints | AWS SDK chain, non-CLI Azure Identity, Google ADC, Alibaba credentials-go RAM/OIDC/ECS role, Tencent CAM temporary tuple, Baidu BCE AKSK/STS | Credential-chain and status tests; credentials absent from MCP schemas; status distinguishes adapter availability from unverified authentication | Implemented; live identity resolution pending |
| Credentials cannot be supplied, minted or exported through MCP | Credential headers/query parameters rejected; STS/token/key/password issuance and export families hard-rejected; output redaction remains defense in depth | `TestInvocationBoundaryRejectsCredentialExfiltrationAndUnboundedInput` and `TestInvocationBoundaryNeverIssuesOrExportsCloudCredentials` | Hermetic gate implemented |
| Large/binary API responses remain usable without entering model context | All six adapters support bounded `response_file` streaming to a new approved-root target; no overwrite, mode 0600, atomic publish, provider errors and over-limit transfers leave no target; official Range requests cover larger objects | `TestReadRESTResponseStreamsSuccessfulBodyToNewFile`, `TestReadRESTResponseFileNeverOverwritesOrLeavesPartialFiles`, policy/schema/protocol tests | Hermetic gate implemented; live object download pending |
| Read/write and sensitive-operation boundary | Conservative read classifier; `CLOUD_SKILLS_ALLOW_MUTATIONS=1` + `force=true`; separate sensitive gate; host approval remains mandatory | Server contract, mutation-gate and protocol-smoke tests | Hermetic gate implemented |
| Sanitized, fail-closed audit | Mode-0600 JSONL; no headers/body/response/query values; mutation pre-audit fail closed; request ID captured | Audit sink, URL sanitization, failure and live audit tests | Hermetic implemented; live audit pending |
| Skills service and official documentation mapping | Six direct-network `SKILL.md`, six `agents/openai.yaml`, provider `references/official-docs.md` files | Skill validator and live official-link review | Implemented |
| Protocol, installer, security and cross-platform build gates | `scripts/ci/protocol-smoke.sh`, `install-smoke.sh`, `build-release.sh`, `.github/workflows/ci.yml` | Azure Web PubSub JSON/Protobuf/MQTT slice covers exact endpoint/schema validation, four PubSub subprotocols, official proto3 wire vectors, binary Any/stream messages, token containment, reliable recovery/sequence/publisher state, MQTT 3.1.1/5.0 QoS/state/property flow, and atomic failure behavior; Protobuf implementation run [30784347113](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30784347113) passed. Vertex Live recovery adds exact function-call ID response dispatch, atomic handler bounds, private handle sanitization, acknowledged-message replay, GoAway/unexpected-close reconnect, integer preservation, and two fuzz targets; implementation run [30785793131](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30785793131) passed macOS, Ubuntu, ShellCheck, security, and release jobs. Google schema-driven gRPC adds exact descriptor-selected methods, strict ProtoJSON request conversion, finite four-shape streaming, recursive unknown-wire rejection including `Any`, official 64-bit-safe output mapping, atomic NDJSON, and two fuzz targets; implementation run [30787842631](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30787842631) passed. Baidu RTC AI Agent adds exact BCE v1 create/private-token WSS/stop, internal license containment, concurrent bounded duplex streaming, fail-closed atomic output, mutation-only policy, a live mutation entrypoint, and a plan fuzz target; initial lifecycle run [30790805986](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30790805986) passed. The codec follow-up corrects the control field to official `config.audiocodec`, covers `raw`, `raw16k`, PCMA, PCMU, G.722, and variable-length Opus packet vectors, and internally derives `ac`/`ptime`/`plen`; implementation run [30792487473](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30792487473) passed. The static command follow-up strictly parses every documented non-event-correlated client command, rejects credential material and unsafe media URLs, and preserves pre/post-audio order; implementation run [30793969272](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30793969272) passed. The stateful media follow-up adds approved-root `image_file`, exact provider-triggered 16 KiB/Base64 upload framing, one-request consumption, and non-interleaved writes; implementation run [30795300185](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30795300185) passed. The Function Call follow-up implements the current nested JSON format, exact provider `session_id` correlation, bounded credential-free result/post-function templates, and fail-closed unknown/duplicate/overflow handling; implementation run [30796139810](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30796139810) passed | Fresh local module verification, formatting, vet, race suite (80.4% total / 80.4% cloud-package coverage), build, protocol/install smoke, shell syntax, six-Skill validation, Actionlint, govulncheck, 10-second Baidu RTC fuzzing (223,961 executions), and four-platform release builds passed; remote macOS/Ubuntu verification, ShellCheck, security scans, and release archives also passed |
| Observable six-cloud acceptance | `TestLiveSixCloudReadOnly` selects providers and logs provider/outcome/bytes/request ID only; `TestLiveBaiduRTCAgentMutation` separately exercises the explicitly approved billed create/WSS/stop lifecycle without response bodies or secrets; `TestLiveAlibabaMQMutation` performs a default-release real HTTPS consume probe without exposing credentials or receipt handles; `TestLiveTencentCLSReadOnly` exercises the distinct CLS q-sign path without credential output; `TestLiveAzureACRReadOnly` exercises the complete Entra-to-ACR scoped-token chain against an existing repository | Requires operator-injected credentials and opt-in live flags; RTC additionally requires mutation approval, app/device/user values and server-only product license; RocketMQ additionally requires the exact endpoint, topic, consumer group, optional namespace, and one staged matching message; CLS requires an exact regional endpoint and existing logset ID; ACR requires an exact registry endpoint and existing repository with pull permission | **Pending** |

Verified slices:

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

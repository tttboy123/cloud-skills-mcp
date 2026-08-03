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
| All documented resource APIs addressable without per-resource Go handlers | Generic transport coverage exists for AWS SigV4/SigV4a including finite/bidirectional HTTP/2 signed EventStream, finite mutation-only raw-frame SigV4 WSS, Connect Health Medical Scribe signed-EventStream WSS, standard/Medical/Call Analytics Transcribe WSS, finite IoT MQTT WSS, Kinesis Video WebRTC Signaling Master/Viewer WSS, finite AppSync Events IAM WSS, and finite legacy AppSync GraphQL IAM WSS subscriptions, Azure Entra REST plus OpenAI Realtime, Voice Live model/Agent WSS, Entra-backed Web PubSub standard/reliable JSON/Protobuf WSS and finite MQTT 3.1.1/5.0 clients, Event Grid Namespace Entra MQTT v5 WSS, and Service Bus/Event Hubs AMQP 1.0 over exact public, US Government, or China WSS with internal Entra/CBS, bounded broker operations and no exported message/session locks, GCP ADC REST plus both raw and operator-approved `FileDescriptorSet`-driven ProtoJSON gRPC over HTTP/2 (including Speech v2 bidirectional-streaming vectors) and bounded Vertex/Gemini Live ADC WSS with bounded tool-call dispatch and internal transparent resumption, Alibaba ACS3/RPC V2/ROA V2/DataHub/OpenSearch/MaxCompute ODPS V2/V4/Function Compute classic and current Trigger families/OSS V1/V4/SLS/MNS/OTS plus NLS REST short ASR/TTS and five NLS recognition/synthesis WSS namespaces with internal token containment, Tencent TC3/API 3.0 v1/legacy qcloud API 2017/COS/realtime ASR, virtual-number detection, SOE evaluation, speech-translation, voice-conversion, standard realtime TTS, streaming-text TTS and large-model podcast WSS/MPS private-audio recognition WSS/MPS streaming TTS WSS, and Baidu BCE v1/v2 plus RTC AI Agent BCE-create/private-token-WSS/stop; remaining families and explicit retired/credential-bound mappings are enumerated in `api-protocol-coverage.md` | Official and SDK signature vectors plus transport, policy, and data-plane tests | **Partial — remaining product-specific protocols and live vectors remain** |
| Official AKSK/IAM identity entrypoints | AWS SDK chain, non-CLI Azure Identity, Google ADC, Alibaba credentials-go RAM/OIDC/ECS role, Tencent CAM temporary tuple, Baidu BCE AKSK/STS | Credential-chain and status tests; credentials absent from MCP schemas; status distinguishes adapter availability from unverified authentication | Implemented; live identity resolution pending |
| Credentials cannot be supplied, minted or exported through MCP | Credential headers/query parameters rejected; STS/token/key/password issuance and export families hard-rejected; output redaction remains defense in depth | `TestInvocationBoundaryRejectsCredentialExfiltrationAndUnboundedInput` and `TestInvocationBoundaryNeverIssuesOrExportsCloudCredentials` | Hermetic gate implemented |
| Large/binary API responses remain usable without entering model context | All six adapters support bounded `response_file` streaming to a new approved-root target; no overwrite, mode 0600, atomic publish, provider errors and over-limit transfers leave no target; official Range requests cover larger objects | `TestReadRESTResponseStreamsSuccessfulBodyToNewFile`, `TestReadRESTResponseFileNeverOverwritesOrLeavesPartialFiles`, policy/schema/protocol tests | Hermetic gate implemented; live object download pending |
| Read/write and sensitive-operation boundary | Conservative read classifier; `CLOUD_SKILLS_ALLOW_MUTATIONS=1` + `force=true`; separate sensitive gate; host approval remains mandatory | Server contract, mutation-gate and protocol-smoke tests | Hermetic gate implemented |
| Sanitized, fail-closed audit | Mode-0600 JSONL; no headers/body/response/query values; mutation pre-audit fail closed; request ID captured | Audit sink, URL sanitization, failure and live audit tests | Hermetic implemented; live audit pending |
| Skills service and official documentation mapping | Six direct-network `SKILL.md`, six `agents/openai.yaml`, provider `references/official-docs.md` files | Skill validator and live official-link review | Implemented |
| Protocol, installer, security and cross-platform build gates | `scripts/ci/protocol-smoke.sh`, `install-smoke.sh`, `build-release.sh`, `.github/workflows/ci.yml` | Azure Web PubSub JSON/Protobuf/MQTT slice covers exact endpoint/schema validation, four PubSub subprotocols, official proto3 wire vectors, binary Any/stream messages, token containment, reliable recovery/sequence/publisher state, MQTT 3.1.1/5.0 QoS/state/property flow, and atomic failure behavior; Protobuf implementation run [30784347113](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30784347113) passed. Vertex Live recovery adds exact function-call ID response dispatch, atomic handler bounds, private handle sanitization, acknowledged-message replay, GoAway/unexpected-close reconnect, integer preservation, and two fuzz targets; implementation run [30785793131](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30785793131) passed macOS, Ubuntu, ShellCheck, security, and release jobs. Google schema-driven gRPC adds exact descriptor-selected methods, strict ProtoJSON request conversion, finite four-shape streaming, recursive unknown-wire rejection including `Any`, official 64-bit-safe output mapping, atomic NDJSON, and two fuzz targets; implementation run [30787842631](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30787842631) passed. Baidu RTC AI Agent adds exact BCE v1 create/private-token WSS/stop, internal license containment, concurrent bounded duplex streaming, fail-closed atomic output, mutation-only policy, a live mutation entrypoint, and a plan fuzz target; initial lifecycle run [30790805986](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30790805986) passed. The codec follow-up corrects the control field to official `config.audiocodec`, covers `raw`, `raw16k`, PCMA, PCMU, G.722, and variable-length Opus packet vectors, and internally derives `ac`/`ptime`/`plen`; implementation run [30792487473](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30792487473) passed. The static command follow-up strictly parses every documented non-event-correlated client command, rejects credential material and unsafe media URLs, and preserves pre/post-audio order; implementation run [30793969272](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30793969272) passed. The stateful media follow-up adds approved-root `image_file`, exact provider-triggered 16 KiB/Base64 upload framing, one-request consumption, and non-interleaved writes; implementation run [30795300185](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30795300185) passed. The Function Call follow-up implements the current nested JSON format, exact provider `session_id` correlation, bounded credential-free result/post-function templates, and fail-closed unknown/duplicate/overflow handling; implementation run [30796139810](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30796139810) passed | Fresh local module verification, formatting, vet, race suite (80.4% total / 80.4% cloud-package coverage), build, protocol/install smoke, shell syntax, six-Skill validation, Actionlint, govulncheck, 10-second Baidu RTC fuzzing (223,961 executions), and four-platform release builds passed; remote macOS/Ubuntu verification, ShellCheck, security scans, and release archives also passed |
| Observable six-cloud acceptance | `TestLiveSixCloudReadOnly` selects providers and logs provider/outcome/bytes/request ID only; `TestLiveBaiduRTCAgentMutation` separately exercises the explicitly approved billed create/WSS/stop lifecycle without response bodies or secrets | Requires operator-injected credentials and opt-in live flags; RTC additionally requires mutation approval, app/device/user values and server-only product license | **Pending** |

Latest verified slice: Firebase Realtime Database SSE now supports bounded
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

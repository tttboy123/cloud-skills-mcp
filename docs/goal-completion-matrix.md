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
| All documented resource APIs addressable without per-resource Go handlers | Generic transport coverage exists for AWS SigV4/SigV4a including finite/bidirectional HTTP/2 signed EventStream, finite mutation-only raw-frame SigV4 WSS, Connect Health Medical Scribe signed-EventStream WSS, standard/Medical/Call Analytics Transcribe WSS, finite IoT MQTT WSS, Kinesis Video WebRTC Signaling Master/Viewer WSS, finite AppSync Events IAM WSS, and finite legacy AppSync GraphQL IAM WSS subscriptions, Azure Entra REST plus OpenAI Realtime, Voice Live model/Agent WSS, Entra-backed Web PubSub standard/reliable JSON/Protobuf WSS and finite MQTT 3.1.1/5.0 clients, plus Event Grid Namespace Entra MQTT v5 WSS, GCP ADC REST plus both raw and operator-approved `FileDescriptorSet`-driven ProtoJSON gRPC over HTTP/2 (including Speech v2 bidirectional-streaming vectors) and bounded Vertex/Gemini Live ADC WSS with bounded tool-call dispatch and internal transparent resumption, Alibaba ACS3/RPC V2/ROA V2/DataHub/OpenSearch/MaxCompute ODPS V2/V4/Function Compute classic and current Trigger families/OSS V1/V4/SLS/MNS/OTS plus NLS REST short ASR/TTS and five NLS recognition/synthesis WSS namespaces with internal token containment, Tencent TC3/API 3.0 v1/legacy qcloud API 2017/COS/realtime ASR, virtual-number detection, SOE evaluation, speech-translation, voice-conversion, standard realtime TTS, streaming-text TTS and large-model podcast WSS/MPS private-audio recognition WSS/MPS streaming TTS WSS, and Baidu BCE v1/v2 plus RTC AI Agent BCE-create/private-token-WSS/stop; remaining families and explicit retired/credential-bound mappings are enumerated in `api-protocol-coverage.md` | Official and SDK signature vectors plus transport, policy, and data-plane tests | **Partial — remaining product-specific protocols and live vectors remain** |
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

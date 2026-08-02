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
| All documented resource APIs addressable without per-resource Go handlers | Generic transport coverage exists for AWS SigV4/SigV4a, Azure Entra, GCP ADC, Alibaba ACS3/RPC V2/ROA V2/DataHub/OpenSearch/MaxCompute ODPS V2/V4/Function Compute classic and current Trigger families/OSS V1/V4/SLS/MNS/OTS, Tencent TC3/API 3.0 v1/legacy qcloud API 2017/COS/realtime ASR, speech-translation and voice-conversion WSS/MPS private-audio recognition WSS/MPS streaming TTS WSS, and Baidu BCE v1/v2; remaining families and explicit retired/credential-bound mappings are enumerated in `api-protocol-coverage.md` | Official and SDK signature vectors plus transport, policy, and data-plane tests | **Partial — remaining interactive/non-REST, product-specific protocol audit and live vectors remain** |
| Official AKSK/IAM identity entrypoints | AWS SDK chain, non-CLI Azure Identity, Google ADC, Alibaba credentials-go RAM/OIDC/ECS role, Tencent CAM temporary tuple, Baidu BCE AKSK/STS | Credential-chain and status tests; credentials absent from MCP schemas; status distinguishes adapter availability from unverified authentication | Implemented; live identity resolution pending |
| Credentials cannot be supplied, minted or exported through MCP | Credential headers/query parameters rejected; STS/token/key/password issuance and export families hard-rejected; output redaction remains defense in depth | `TestInvocationBoundaryRejectsCredentialExfiltrationAndUnboundedInput` and `TestInvocationBoundaryNeverIssuesOrExportsCloudCredentials` | Hermetic gate implemented |
| Large/binary API responses remain usable without entering model context | All six adapters support bounded `response_file` streaming to a new approved-root target; no overwrite, mode 0600, atomic publish, provider errors and over-limit transfers leave no target; official Range requests cover larger objects | `TestReadRESTResponseStreamsSuccessfulBodyToNewFile`, `TestReadRESTResponseFileNeverOverwritesOrLeavesPartialFiles`, policy/schema/protocol tests | Hermetic gate implemented; live object download pending |
| Read/write and sensitive-operation boundary | Conservative read classifier; `CLOUD_SKILLS_ALLOW_MUTATIONS=1` + `force=true`; separate sensitive gate; host approval remains mandatory | Server contract, mutation-gate and protocol-smoke tests | Hermetic gate implemented |
| Sanitized, fail-closed audit | Mode-0600 JSONL; no headers/body/response/query values; mutation pre-audit fail closed; request ID captured | Audit sink, URL sanitization, failure and live audit tests | Hermetic implemented; live audit pending |
| Skills service and official documentation mapping | Six direct-network `SKILL.md`, six `agents/openai.yaml`, provider `references/official-docs.md` files | Skill validator and live official-link review | Implemented |
| Protocol, installer, security and cross-platform build gates | `scripts/ci/protocol-smoke.sh`, `install-smoke.sh`, `build-release.sh`, `.github/workflows/ci.yml` | Current Tencent realtime voice-conversion WSS slice: local race coverage 81.2% overall and 81.3% in the core cloud package, vet, module verification, protocol/install smoke, six Skill validations, actionlint, vulnerability scan, shell syntax and four-target builds; the preceding realtime speech-translation WSS slice passed all remote macOS, Ubuntu, ShellCheck, security and release jobs in GitHub Actions run [30764552501](https://github.com/tttboy123/cloud-skills-mcp/actions/runs/30764552501) | Current local and preceding remote gates passed; current remote gate runs on push |
| Observable six-cloud acceptance | `TestLiveSixCloudReadOnly` selects providers and logs provider/outcome/bytes/request ID only | Requires operator-injected credentials and `CLOUD_SKILLS_LIVE_TEST=1` | **Pending** |

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

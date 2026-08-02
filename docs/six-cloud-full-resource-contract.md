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
| AWS | Direct HTTPS with AWS SigV4 and pure-Go SigV4a for multi-region endpoints | AWS SDK chain: IAM Identity Center, profile/role, web identity, instance role, or AK/SK/STS env |
| Azure | Direct HTTPS with Entra Bearer Token and validated audience | Non-CLI Azure Identity Environment, Workload Identity, or Managed Identity credentials |
| Google Cloud | Google Auth ADC authenticated HTTPS against validated `googleapis.com` endpoints | ADC, service account, workload identity federation, impersonation, or metadata identity |
| Alibaba Cloud | Direct ACS3 OpenAPI, legacy RPC/ROA V2, DataHub, OpenSearch V3, MaxCompute ODPS v2/v4 project/data/Tunnel, Function Compute classic FC/current Trigger ACS3/custom-domain POP, OSS v1/v4 Header, SLS v1/v4, MNS and OTS v2/v4 signed HTTPS | Official credentials-go chain: RAM/OIDC/ECS role, STS, or AK/SK env |
| Tencent Cloud | Direct API 3.0 TC3 and v1 HmacSHA1/HmacSHA256 HTTPS, still-active qcloud API 2017 query/form HTTPS, COS data-plane signed HTTPS, and internally connected realtime ASR/virtual-number detection/SOE evaluation/speech translation/voice conversion/MPS recognition/MPS TTS/standard realtime TTS/streaming-text TTS signed WSS | SecretId/SecretKey or CAM/STS temporary credentials injected into the server environment; realtime ASR signs its documented temporary token, while WSS protocols without a Token field require the long-lived tuple |
| Baidu AI Cloud | BCE signed HTTPS request against validated `baidubce.com` endpoints | BCE AK/SK or IAM/STS temporary AK/SK/session token |

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
- Finite AWS SigV4 HTTP EventStream requests accept only bounded, CRC-valid
  unsigned frames, then use the official SDK stream signer to create dated,
  chained signing envelopes and a terminal frame. Interactive WebSocket
  sessions remain a separate transport family.
- Endpoint overrides from MCP input, authorization headers and credential
  management operations are rejected.
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

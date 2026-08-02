# Six-cloud full-resource MCP contract

Date: 2026-08-02
Status: active goal contract

## Completion definition

The product supports AWS, Microsoft Azure, Google Cloud, Alibaba Cloud,
Tencent Cloud and Baidu AI Cloud. "All resources" means every operation that
the provider exposes through its official OpenAPI, REST API or universal CLI
can be addressed through a provider gateway without adding a new Go tool for
each product or resource type.

Coverage is an invocation capability, not an entitlement claim. The caller's
IAM policy, enabled services, account state, region availability and provider
API support remain authoritative. Console-only workflows are outside the API
surface.

## Public MCP surface

The unified `cloud-skills-mcp` stdio server exposes:

- `cloud_provider_status`: report local adapter/CLI readiness, non-secret auth
  source type and a credential status. `available` is not proof of cloud
  authentication; `credential_status=unverified` means the official identity
  chain is resolved lazily by the first API call. Direct BCE signing can report
  local credential-material presence, but only a live read proves validity.
- `<provider>_api_discover`: read official CLI help or API discovery metadata.
- `<provider>_api_read`: invoke an operation classified as read-only.
- `<provider>_api_mutate`: invoke any supported operation after mutation and
  human-approval gates.

Provider prefixes are `aws`, `azure`, `gcp`, `alicloud`, `tencent` and
`baiducloud`. Existing Tencent fine-grained tools remain backward compatible.

Read tools reject operations that cannot be conservatively classified as
read-only. Such operations remain reachable through the mutation tool with
explicit approval; a caller cannot downgrade an operation by labeling it
"read".

## Universal provider adapters

| Provider | Universal access mechanism | Credential boundary |
|---|---|---|
| AWS | AWS CLI service + operation, structured JSON input | Official AWS credential provider chain: IAM Identity Center, profile/role, web identity, instance role, or AK/SK/STS env |
| Azure | Official Azure Identity direct REST for known audiences, with `az rest` fallback for other validated Azure endpoints | DefaultAzureCredential, Azure CLI identity, managed identity, workload identity, or Service Principal |
| Google Cloud | Google Auth ADC authenticated REST against validated `googleapis.com` endpoints, with gcloud identity fallback | ADC, service account, workload identity federation, impersonation, or active gcloud identity |
| Alibaba Cloud | Alibaba Cloud CLI product + OpenAPI action / REST-style plugin operation | OAuth, RAM role, STS, OIDC, profile, or AK/SK env through the official CLI chain |
| Tencent Cloud | TCCLI product + API action with JSON input | CAM role/OIDC/profile or SecretId/SecretKey/STS env through TCCLI |
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

- Commands use fixed executables and argument arrays; no shell is involved.
- Provider service/action names, HTTP methods, URLs, headers, argument count,
  argument length, body size and response size are bounded.
- Endpoint overrides, proxy flags, TLS-disable flags, authorization headers and
  credential-management commands are rejected.
- Direct REST adapters allow HTTPS only and provider-owned hostname suffixes.
  Redirects are disabled so credentials cannot cross host boundaries.
- Local file references are rejected unless their resolved path is below an
  operator-configured `CLOUD_SKILLS_ALLOWED_FILE_ROOTS` entry.
- REST data-plane uploads use `body_file`, never embed file content in MCP
  context, accept regular files only, and are capped at 64 MiB per request;
  provider multipart or resumable APIs cover larger objects.
- Azure uncommon data-plane endpoints can use a first-class, validated
  `audience` identifier. The value is never a credential: the adapter derives
  the `.default` scope internally or passes it only to `az rest --resource`.
- Baidu endpoint validation includes both the general `*.baidubce.com` service
  plane and the official BOS `*.bcebos.com` object-storage plane.
- Baidu calls use `bce-auth-v1` by default and can select guarded
  `auth_version=v2` with required service/region fields for APIs that mandate
  the region- and service-scoped v2 signature.
- New official Azure, GCP, or Baidu endpoint exceptions are operator policy,
  never model input. `CLOUD_SKILLS_<PROVIDER>_ALLOWED_ENDPOINT_HOSTS` accepts
  comma-separated exact DNS hostnames only; schemes, ports, paths, IPs, and
  wildcard/subdomain inheritance are not accepted.
- CLI JSON request bodies use mode-0600 temporary files and are removed after
  the call.
- Responses are capped and credential-shaped fields are redacted before they
  enter MCP output.

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
- Hermetic fake-adapter tests prove exact executable/HTTP construction,
  credential redaction, endpoint and file-root rejection, mutation/sensitive
  gates, output caps, timeout handling and audit behavior.
- CI runs formatting, vet, race tests, coverage, ShellCheck, actionlint,
  vulnerability scanning, protocol smoke, installer smoke and cross-platform
  release builds.
- Live tests are opt-in, read-only by default and provider-selectable. The
  operator injects credentials and observes provider, audit outcome, response
  size and request ID without printing response bodies. Mutation live tests use
  dedicated disposable resources and separate explicit approval.

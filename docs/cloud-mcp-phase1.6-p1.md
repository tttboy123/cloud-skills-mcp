# Cloud MCP Phase 1.6 / P1 Contract

Date: 2026-08-02

## Product boundary

This repository is an MCP server plus installable Skills service. Phase 1.6
makes the Tencent implementation live-testable and release-ready. The first P1
slice expands the native MCP surface without claiming that every Bash fallback
is already an MCP tool.

The server remains local JSON-RPC over stdio. Remote HTTP transport,
multi-tenancy and a hosted control plane are not part of this slice.

## Stable tool surface

Existing tool names and successful raw Tencent JSON responses remain compatible.
New tools are additive.

| Tool | Tencent API / command | Mutation |
|---|---|---:|
| `tencent_cloud_cli_status` | `tccli --version` | no |
| `tencent_cvm_list_instances` | CVM `DescribeInstances` | no |
| `tencent_cvm_describe_instance` | CVM `DescribeInstances` | no |
| `tencent_cvm_start_instance` | CVM `StartInstances` | yes |
| `tencent_cvm_stop_instance` | CVM `StopInstances` | yes |
| `tencent_cvm_reboot_instance` | CVM `RebootInstances` | yes |
| `tencent_lighthouse_list_instances` | Lighthouse `DescribeInstances` | no |
| `tencent_lighthouse_describe_instance` | Lighthouse `DescribeInstances` | no |
| `tencent_lighthouse_start_instance` | Lighthouse `StartInstances` | yes |
| `tencent_lighthouse_stop_instance` | Lighthouse `StopInstances` | yes |
| `tencent_lighthouse_reboot_instance` | Lighthouse `RebootInstances` | yes |
| `tencent_cdb_list_instances` | CDB `DescribeDBInstances` | no |
| `tencent_cdb_describe_instance` | CDB `DescribeDBInstances` | no |
| `tencent_cloudbase_list_environments` | CloudBase `DescribeEnvs` | no |
| `tencent_cloudbase_describe_environment` | CloudBase `DescribeEnvs` | no |

All list tools accept `offset` and `limit`. CVM, Lighthouse and CloudBase use a
maximum limit of 100. CDB uses the documented maximum of 2000.

## Runtime policy

- Mutations still require both `CLOUD_SKILLS_ALLOW_MUTATIONS=1` and
  `force=true`; neither is proof of human approval.
- `CLOUD_SKILLS_ALLOWED_REGIONS` optionally contains a comma-separated exact
  allowlist. Calls outside it fail before credential loading.
- `CLOUD_SKILLS_ALLOWED_RESOURCES` optionally contains exact CVM, Lighthouse,
  CDB or CloudBase IDs. When set, an unfiltered list is rejected because it
  would disclose resources outside the allowlist.
- `CLOUD_SKILLS_AUDIT_LOG` optionally enables JSON Lines audit events. Mutations
  fail closed if their audit event cannot be persisted. Audit records never
  contain credentials or full Tencent response bodies.
- Read-only calls retry documented transient failures with bounded exponential
  backoff. Mutating calls are never automatically retried.

## Error contract

Tencent API errors are detected from the documented
`Response.Error.Code/Message` envelope even if the CLI process exits zero.
Errors returned to MCP clients preserve a redacted message and RequestId and add
machine-readable provider, service, action, code, retryable and attempts fields.
Successful responses remain the raw Tencent JSON document for compatibility.

## Official sources

- CVM `DescribeInstances`, `StartInstances`, `StopInstances`, `RebootInstances`:
  <https://cloud.tencent.com/document/product/213/15728>,
  <https://cloud.tencent.com/document/product/213/15735>,
  <https://cloud.tencent.com/document/api/213/15743>,
  <https://cloud.tencent.com/document/api/213/15742>
- Lighthouse instance APIs:
  <https://cloud.tencent.com/document/product/1207/47573>,
  <https://cloud.tencent.com/document/product/1207/47570/>,
  <https://cloud.tencent.com/document/product/1207/47569>,
  <https://cloud.tencent.com/document/api/1207/47572>
- CDB `DescribeDBInstances`:
  <https://cloud.tencent.com/document/product/236/15872>
- CloudBase `DescribeEnvs`:
  <https://cloud.tencent.com/document/api/876/34820>
- Tencent Cloud common errors and API throttling:
  <https://cloud.tencent.com/document/api/213/30435>,
  <https://cloud.tencent.com/document/product/1278/109059>
- TCCLI installation, version and JSON input:
  <https://cloud.tencent.com/document/product/440/34011>,
  <https://cloud.tencent.com/document/product/440/129328>

## COS deferral

COS uses its XML API and official COS SDK/COSCLI rather than the current TCCLI
adapter. Tencent also documents that List Buckets cannot be restricted to a
selected bucket for a sub-account. Native COS MCP tools therefore require a
separate adapter, temporary-credential model and explicit disclosure policy;
the Bash fallback is not promoted to MCP in this slice.

## Live acceptance gate

CI remains hermetic. Real read-only acceptance is opt-in with
`CLOUD_SKILLS_LIVE_TEST=1`. Real start/stop/reboot operations are never run by
default tests and require a dedicated test instance plus explicit operator
approval outside the test suite.

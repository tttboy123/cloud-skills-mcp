---
name: alicloud
description: Operate or inspect Alibaba Cloud resources through ACS3, RPC V2, OSS4, SLS, MNS, or OTS signed HTTPS in cloud-skills-mcp. Use for Alibaba Cloud, Aliyun, ECS, OSS, SLS, MNS, Tablestore, RDS, VPC, RAM, ACK, Function Compute, Model Studio, BaaS, or a documented Alibaba Cloud API.
---

# Alibaba Cloud

Use the unified MCP server for Alibaba Cloud HTTP APIs. The gateway signs general OpenAPI requests with ACS3-HMAC-SHA256, product APIs that still document legacy RPC V2 with query/form HMAC-SHA1, OSS data-plane requests with OSS4-HMAC-SHA256, Simple Log Service requests with SLS v1 or v4, Simple Message Queue requests with MNS HMAC-SHA1, and Tablestore protobuf requests with OTS v2 or v4. It never executes Alibaba Cloud CLI.

## Workflow

1. Call `cloud_provider_status` with `provider="alicloud"`; treat `available` as HTTP adapter readiness and `credential_status=unverified` as pending live authentication.
2. Use `alicloud_api_discover` for official OpenAPI/ACS3/RPC/OSS4/SLS/MNS/OTS references, then verify method, endpoint, action, version, query, headers, and body in the product API metadata.
3. Use `alicloud_api_read` only for actions classified as read-only (`Describe*`, `List*`, `Get*`, `Query*`, and similar).
4. For other actions, obtain explicit human approval for the account, region, resources, action, and effect; then use `alicloud_api_mutate(force=true)`.
5. Secret-resource operations require the separate sensitive gate. STS role/session credentials, login profiles, AccessKey creation, and other credential issuance/export actions are never exposed by the gateway.
6. Do not pass AccessKeys, security tokens, or authorization data as arguments or headers.

## MCP arguments

- `auth_scheme`: `acs3` (default) for current general OpenAPI; `rpc` only when the product metadata still requires legacy RPC V2 HMAC-SHA1; `oss4` for OSS; `sls` for SLS signature v1; `sls4` for SLS signature v4; `mns` for Simple Message Queue (formerly MNS); `ots` or `ots4` for Tablestore data management.
- `service`: product/signing code, such as `ecs`, `rds`, `vpc`, `ram`, or `oss`.
- `operation`: exact action name used by ACS3 headers and read/write classification.
- `api_version`: required for ACS3 and RPC, such as `2014-05-26`; optional for SLS/MNS/OTS, whose official defaults are `0.6.0`, `2015-06-06`, and `2015-12-31`.
- `region`: required by OSS4, SLS4, and OTS4 and recommended as operation context.
- `method` and `url`: exact official Alibaba Cloud HTTPS request.
- `parameters`: optional scalar query parameters; use `body` or `body_file` for request payloads.
- `response_file`: new approved-root file for OSS objects, exports, or other large responses. Use the documented `Range` header above the configured per-call limit; existing files are never overwritten.

Example read: `alicloud_api_read(auth_scheme="acs3", service="ecs", operation="DescribeInstances", api_version="2014-05-26", region="cn-hangzhou", method="POST", url="https://ecs.cn-hangzhou.aliyuncs.com/", parameters={"RegionId":"cn-hangzhou","PageSize":20})`.

Legacy RPC example: `alicloud_api_read(auth_scheme="rpc", service="baas", operation="DescribeFabricOrganization", api_version="2018-12-21", method="GET", url="https://baas.aliyuncs.com/", parameters={"Format":"JSON","OrganizationId":"..."})`. The adapter adds `Action`, `Version`, timestamp, nonce, AK ID, optional RAM `SecurityToken`, and signature; optional `Format` remains caller-selected because documented defaults differ by product. For operations whose metadata puts parameters in `formData`, pass an `application/x-www-form-urlencoded` body; those fields are included in the signature without being copied into the URL. RPC accepts only the documented root path and GET or POST, and repeated parameter names are rejected.

SLS example: `alicloud_api_read(auth_scheme="sls4", service="sls", operation="ListLogstores", region="cn-hangzhou", method="GET", url="https://<project>.cn-hangzhou.log.aliyuncs.com/logstores")`. For protobuf log ingestion, pass the encoded body through `body_file` and its documented non-auth headers such as `Content-Type` and `x-log-bodyrawsize`; signing headers are server-controlled.

MNS example: `alicloud_api_read(auth_scheme="mns", service="mns", operation="ListQueues", method="GET", url="https://<account-id>.mns.cn-hangzhou.aliyuncs.com/queues")`. MNS request bodies are XML; when a body is present and no content type is supplied, the adapter uses `application/xml`.

Tablestore data management is protobuf over direct HTTP, not ACS3. Encode the exact operation request with Alibaba's published `.proto` definitions, pass it through `body_file`, and call the operation path with POST, for example `alicloud_api_read(auth_scheme="ots4", service="ots", operation="ListTable", region="cn-hangzhou", method="POST", url="https://<instance>.cn-hangzhou.ots.aliyuncs.com/ListTable", body_file="<approved-root>/ListTableRequest.pb", response_file="<approved-root>/ListTableResponse.pb")`. The adapter derives the instance from the first official endpoint label and internally sets the body MD5, OTS headers, V4 derived key, AK ID, signature, and STS token. Decode the response with the matching official response message definition.

## Credentials

Use the official credentials-go chain: RAM/OIDC/ECS role, STS, or `ALIBABA_CLOUD_ACCESS_KEY_ID` / `ALIBABA_CLOUD_ACCESS_KEY_SECRET`. Temporary credentials also use `ALIBABA_CLOUD_SECURITY_TOKEN`. Never put credentials in MCP arguments.

Read [references/official-docs.md](references/official-docs.md) when profile type, parameter shape, or action name needs verification.

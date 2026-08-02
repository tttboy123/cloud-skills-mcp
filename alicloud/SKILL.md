---
name: alicloud
description: Operate or inspect Alibaba Cloud resources through ACS3, RPC/ROA V2, DataHub, OpenSearch, MaxCompute ODPS, Function Compute FC, OSS4, SLS, MNS, or OTS signed HTTPS in cloud-skills-mcp. Use for Alibaba Cloud, Aliyun, ECS, DataHub, OpenSearch, MaxCompute, Function Compute, OSS, SLS, MNS, Tablestore, RDS, VPC, RAM, ACK, Model Studio, BaaS, PDS, or a documented Alibaba Cloud API.
---

# Alibaba Cloud

Use the unified MCP server for Alibaba Cloud HTTP APIs. The gateway signs general OpenAPI with ACS3, legacy RPC/ROA V2, DataHub, OpenSearch, MaxCompute ODPS V2/V4, Function Compute FC, OSS, SLS, MNS, and Tablestore protocols. It never executes Alibaba Cloud CLI.

## Workflow

1. Call `cloud_provider_status` with `provider="alicloud"`; treat `available` as HTTP adapter readiness and `credential_status=unverified` as pending live authentication.
2. Use `alicloud_api_discover` for official OpenAPI and product-protocol references, then verify method, endpoint, action, version, query, headers, and body in the product API metadata.
3. Use `alicloud_api_read` only for actions classified as read-only (`Describe*`, `List*`, `Get*`, `Query*`, and similar).
4. For other actions, obtain explicit human approval for the account, region, resources, action, and effect; then use `alicloud_api_mutate(force=true)`.
5. Secret-resource operations require the separate sensitive gate. STS role/session credentials, login profiles, AccessKey creation, and other credential issuance/export actions are never exposed by the gateway.
6. Do not pass AccessKeys, security tokens, or authorization data as arguments or headers.

## MCP arguments

- `auth_scheme`: `acs3` (default) for current general OpenAPI; `rpc|roa` for legacy V2 APIs; `datahub` for DataHub; `opensearch` for OpenSearch V3 AccessKey APIs; `odps4` for current MaxCompute project/data/Tunnel APIs and `odps` for their legacy V2 signature; `fc` for classic Function Compute resources and authenticated HTTP Trigger paths; `oss4` for OSS; `sls|sls4` for SLS; `mns` for Simple Message Queue; `ots|ots4` for Tablestore.
- `service`: product/signing code, such as `ecs`, `rds`, `vpc`, `ram`, or `oss`.
- `operation`: exact action name used by ACS3 headers and read/write classification.
- `api_version`: required for ACS3, RPC, and ROA, such as `2014-05-26`; optional for SLS/MNS/OTS, whose official defaults are `0.6.0`, `2015-06-06`, and `2015-12-31`.
- `region`: required by ODPS4, OSS4, SLS4, and OTS4 and recommended as operation context.
- `method` and `url`: exact official Alibaba Cloud HTTPS request.
- `parameters`: optional scalar query parameters; use `body` or `body_file` for request payloads.
- `response_file`: new approved-root file for OSS objects, exports, or other large responses. Use the documented `Range` header above the configured per-call limit; existing files are never overwritten.

Example read: `alicloud_api_read(auth_scheme="acs3", service="ecs", operation="DescribeInstances", api_version="2014-05-26", region="cn-hangzhou", method="POST", url="https://ecs.cn-hangzhou.aliyuncs.com/", parameters={"RegionId":"cn-hangzhou","PageSize":20})`.

Legacy RPC example: `alicloud_api_read(auth_scheme="rpc", service="baas", operation="DescribeFabricOrganization", api_version="2018-12-21", method="GET", url="https://baas.aliyuncs.com/", parameters={"Format":"JSON","OrganizationId":"..."})`. The adapter adds `Action`, `Version`, timestamp, nonce, AK ID, optional RAM `SecurityToken`, and signature; optional `Format` remains caller-selected because documented defaults differ by product. For operations whose metadata puts parameters in `formData`, pass an `application/x-www-form-urlencoded` body; those fields are included in the signature without being copied into the URL. RPC accepts only the documented root path and GET or POST, and repeated parameter names are rejected.

Legacy ROA example: `alicloud_api_read(auth_scheme="roa", service="pds", operation="ListDrives", api_version="v2", method="POST", url="https://<domain-id>.api.aliyunpds.com/v2/drive/list", body={"limit":20})`. The adapter signs the exact path/query and body, derives `Content-MD5`, and internally adds Date, nonce, version, optional RAM `x-acs-security-token`, and `Authorization`. Do not provide those controlled headers. ROA supports the documented GET, POST, PUT, and DELETE methods.

DataHub example: `alicloud_api_read(auth_scheme="datahub", service="datahub", operation="ListProjects", method="GET", url="https://dh-cn-hangzhou.aliyuncs.com/projects")`. The adapter defaults `x-datahub-client-version` to `1.1` (override with `api_version`), signs the exact resource path/query, and internally adds Date, optional RAM `x-datahub-security-token`, and `DATAHUB` authorization. Project, topic, shard, connector, record, and subscription endpoints use the same scheme.

OpenSearch example: `alicloud_api_read(auth_scheme="opensearch", service="opensearch", operation="Search", method="GET", url="https://opensearch-cn-hangzhou.aliyuncs.com/v3/openapi/apps/<app>/search", parameters={"query":"config=start:0&&query=default:'term'"})`. The adapter RFC3986-canonicalizes non-empty search parameters, generates the required timestamp-plus-random nonce, and adds ISO-8601 Date, optional `X-Opensearch-Security-Token`, and `OPENSEARCH` authorization. Body requests receive the documented lowercase hexadecimal `Content-MD5`; non-GET request query parameters are not part of the OpenSearch push-resource signature.

MaxCompute example: `alicloud_api_read(auth_scheme="odps4", service="maxcompute", operation="ListProjects", region="cn-hangzhou", method="GET", url="https://service.cn-hangzhou.maxcompute.aliyun.com/api/projects")`. Use the exact public, VPC, or interconnected endpoint documented for the project region; Tunnel calls use the matching `dt.<region>.maxcompute.aliyun.com` endpoint. The adapter signs the canonical ODPS resource (the service endpoint's `/api` base is not part of that resource), derives the current V4 key from date/region/service, and internally adds Date, optional RAM `Authorization-Sts-Token`, and `ODPS` authorization. Use `auth_scheme="odps"` only for an official endpoint that still requires the legacy V2 HMAC-SHA1 signature. Do not auto-retry a mutation with the other scheme.

Function Compute example: `alicloud_api_read(auth_scheme="fc", service="fc", operation="ListServices", method="GET", url="https://<account-id>.cn-hangzhou.fc.aliyuncs.com/2016-08-15/services")`. The adapter derives a lowercase hexadecimal body MD5 when a body exists, adds RFC 1123 Date and optional RAM `X-Fc-Security-Token`, canonicalizes all caller-supplied non-credential `x-fc-*` operation headers, and generates `FC` HMAC-SHA256 Authorization. Common resource requests sign the decoded path and discard query from the signature; authenticated standard `/2016-08-15/proxy/...` HTTP Trigger calls sign the decoded path plus all decoded `key=value` pairs sorted on separate lines. Caller-supplied presign/credential query parameters remain forbidden.

SLS example: `alicloud_api_read(auth_scheme="sls4", service="sls", operation="ListLogstores", region="cn-hangzhou", method="GET", url="https://<project>.cn-hangzhou.log.aliyuncs.com/logstores")`. For protobuf log ingestion, pass the encoded body through `body_file` and its documented non-auth headers such as `Content-Type` and `x-log-bodyrawsize`; signing headers are server-controlled.

MNS example: `alicloud_api_read(auth_scheme="mns", service="mns", operation="ListQueues", method="GET", url="https://<account-id>.mns.cn-hangzhou.aliyuncs.com/queues")`. MNS request bodies are XML; when a body is present and no content type is supplied, the adapter uses `application/xml`.

Tablestore data management is protobuf over direct HTTP, not ACS3. Encode the exact operation request with Alibaba's published `.proto` definitions, pass it through `body_file`, and call the operation path with POST, for example `alicloud_api_read(auth_scheme="ots4", service="ots", operation="ListTable", region="cn-hangzhou", method="POST", url="https://<instance>.cn-hangzhou.ots.aliyuncs.com/ListTable", body_file="<approved-root>/ListTableRequest.pb", response_file="<approved-root>/ListTableResponse.pb")`. The adapter derives the instance from the first official endpoint label and internally sets the body MD5, OTS headers, V4 derived key, AK ID, signature, and STS token. Decode the response with the matching official response message definition.

## Credentials

Use the official credentials-go chain: RAM/OIDC/ECS role, STS, or `ALIBABA_CLOUD_ACCESS_KEY_ID` / `ALIBABA_CLOUD_ACCESS_KEY_SECRET`. Temporary credentials also use `ALIBABA_CLOUD_SECURITY_TOKEN`. Never put credentials in MCP arguments.

Read [references/official-docs.md](references/official-docs.md) when profile type, parameter shape, or action name needs verification.

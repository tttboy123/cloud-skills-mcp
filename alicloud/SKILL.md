---
name: alicloud
description: Operate or inspect any Alibaba Cloud resource through ACS3 or OSS4 signed HTTPS in cloud-skills-mcp. Use for Alibaba Cloud, Aliyun, ECS, OSS, RDS, VPC, RAM, ACK, Function Compute, Model Studio, or any documented Alibaba Cloud API.
---

# Alibaba Cloud

Use the unified MCP server for Alibaba Cloud HTTP APIs. The gateway signs general OpenAPI requests with ACS3-HMAC-SHA256 and OSS data-plane requests with OSS4-HMAC-SHA256. It never executes Alibaba Cloud CLI.

## Workflow

1. Call `cloud_provider_status` with `provider="alicloud"`; treat `available` as HTTP adapter readiness and `credential_status=unverified` as pending live authentication.
2. Use `alicloud_api_discover` for official OpenAPI/ACS3/OSS4 references, then verify method, endpoint, action, version, query, and body in the product API metadata.
3. Use `alicloud_api_read` only for actions classified as read-only (`Describe*`, `List*`, `Get*`, `Query*`, and similar).
4. For other actions, obtain explicit human approval for the account, region, resources, action, and effect; then use `alicloud_api_mutate(force=true)`.
5. Secret-resource operations require the separate sensitive gate. STS role/session credentials, login profiles, AccessKey creation, and other credential issuance/export actions are never exposed by the gateway.
6. Do not pass AccessKeys, security tokens, or authorization data as arguments or headers.

## MCP arguments

- `auth_scheme`: `acs3` (default) for general OpenAPI; `oss4` for OSS data plane.
- `service`: product/signing code, such as `ecs`, `rds`, `vpc`, `ram`, or `oss`.
- `operation`: exact action name used by ACS3 headers and read/write classification.
- `api_version`: required for ACS3, such as `2014-05-26`.
- `region`: required by OSS4 and recommended as operation context.
- `method` and `url`: exact official Alibaba Cloud HTTPS request.
- `parameters`: optional scalar query parameters; use `body` or `body_file` for request payloads.

Example read: `alicloud_api_read(auth_scheme="acs3", service="ecs", operation="DescribeInstances", api_version="2014-05-26", region="cn-hangzhou", method="POST", url="https://ecs.cn-hangzhou.aliyuncs.com/", parameters={"RegionId":"cn-hangzhou","PageSize":20})`.

## Credentials

Use the official credentials-go chain: RAM/OIDC/ECS role, STS, or `ALIBABA_CLOUD_ACCESS_KEY_ID` / `ALIBABA_CLOUD_ACCESS_KEY_SECRET`. Temporary credentials also use `ALIBABA_CLOUD_SECURITY_TOKEN`. Never put credentials in MCP arguments.

Read [references/official-docs.md](references/official-docs.md) when profile type, parameter shape, or action name needs verification.

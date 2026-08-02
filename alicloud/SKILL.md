---
name: alicloud
description: Operate or inspect any Alibaba Cloud resource through cloud-skills-mcp and Alibaba Cloud CLI. Use for Alibaba Cloud, Aliyun, ECS, OSS, RDS, VPC, RAM, ACK, Function Compute, Model Studio, or any documented Alibaba Cloud OpenAPI action.
---

# Alibaba Cloud

Use the unified MCP server for every operation exposed by Alibaba Cloud CLI/OpenAPI. The gateway fixes the executable to `aliyun`, validates product/action/flags, and delegates signing to the official credential chain.

## Workflow

1. Call `cloud_provider_status` with `provider="alicloud"`.
2. Use `alicloud_api_discover` with the product code and action when parameters are uncertain.
3. Use `alicloud_api_read` only for actions classified as read-only (`Describe*`, `List*`, `Get*`, `Query*`, and similar).
4. For other actions, obtain explicit human approval for the account, region, resources, action, and effect; then use `alicloud_api_mutate(force=true)`.
5. Secret, credential, token, password, and access-key actions require the separate sensitive gate.
6. Do not pass endpoints, proxies, TLS-disable flags, AccessKeys, security tokens, or authorization data as arguments.

## MCP arguments

- `service`: Alibaba Cloud product code, such as `ecs`, `rds`, `vpc`, or `ram`.
- `operation`: exact OpenAPI action, such as `DescribeInstances`.
- `region`: optional region.
- `parameters`: structured action parameters converted to deterministic `--Name value` arguments.
- `arguments`: optional safe Alibaba Cloud CLI flags.

Example read: `alicloud_api_read(service="ecs", operation="DescribeInstances", region="cn-hangzhou", parameters={"PageSize":20})`.

## Credentials

Use an Alibaba Cloud CLI profile, RAM role/STS configuration, or the current official `ALIBABA_CLOUD_ACCESS_KEY_ID` / `ALIBABA_CLOUD_ACCESS_KEY_SECRET` environment variables in the MCP server process. Temporary credentials also use `ALIBABA_CLOUD_SECURITY_TOKEN`. Never put credentials in MCP arguments.

Read [references/official-docs.md](references/official-docs.md) when profile type, parameter shape, or action name needs verification.

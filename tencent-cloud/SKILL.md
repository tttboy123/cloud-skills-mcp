---
name: tencent-cloud
description: Operate or inspect any Tencent Cloud resource through cloud-skills-mcp and TCCLI API 3.0. Use for Tencent Cloud, CVM, Lighthouse, COS, CDB, VPC, CAM, TKE, CloudBase, or any documented TCCLI product/action.
---

# Tencent Cloud

Use the unified MCP gateway for every TCCLI API 3.0 product and action. The existing fine-grained CVM/Lighthouse/CDB/CloudBase tools remain compatible, but the universal tools cover products without hand-written handlers.

## Workflow

1. Call `cloud_provider_status` with `provider="tencent"`.
2. Use `tencent_api_discover` with the product and action when parameters are uncertain.
3. Use `tencent_api_read` only for actions classified as read-only (`Describe*`, `List*`, `Get*`, `Query*`, and similar).
4. For other actions, obtain explicit human approval for the account, region, resources, action, and effect; then use `tencent_api_mutate(force=true)`.
5. Secret, credential, token, password, and access-key actions require the separate sensitive gate.
6. Do not pass endpoints, proxies, TLS-disable flags, SecretId, SecretKey, session tokens, or authorization data as arguments.

## MCP arguments

- `service`: TCCLI product code, such as `cvm`, `lighthouse`, `cos`, `cdb`, or `cam`.
- `operation`: exact API action, such as `DescribeInstances`.
- `region`: optional region.
- `parameters`: structured API input passed through a mode-0600 temporary `--cli-input-json` file.
- `arguments`: optional safe TCCLI flags.

Example read: `tencent_api_read(service="cvm", operation="DescribeInstances", region="ap-shanghai", parameters={"Limit":20})`.

## Credentials

Use a TCCLI profile, CAM role/temporary credentials, or `TENCENTCLOUD_SECRET_ID` / `TENCENTCLOUD_SECRET_KEY` in the MCP server environment. Never pass credentials in MCP inputs.

Read [references/official-docs.md](references/official-docs.md) for TCCLI product coverage, generic parameters, and credential configuration. Use the legacy scripts only when a workflow specifically requires their additional guardrails.

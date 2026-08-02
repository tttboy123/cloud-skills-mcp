---
name: tencent-cloud
description: Operate or inspect any Tencent Cloud resource through TC3 or COS signed HTTPS in cloud-skills-mcp. Use for Tencent Cloud, CVM, Lighthouse, COS, CDB, VPC, CAM, TKE, CloudBase, or any documented Tencent Cloud API.
---

# Tencent Cloud

Use the unified MCP gateway for API 3.0 product actions and COS REST operations. It calculates TC3-HMAC-SHA256 or COS signatures in-process and never executes TCCLI.

## Workflow

1. Call `cloud_provider_status` with `provider="tencent"`; treat `available` as HTTP adapter readiness and `credential_status=unverified` as pending live authentication.
2. Use `tencent_api_discover` for official API 3.0/TC3/COS references, then verify endpoint, version, action, region, and request body in the product API reference.
3. Use `tencent_api_read` only for actions classified as read-only (`Describe*`, `List*`, `Get*`, `Query*`, and similar).
4. For other actions, obtain explicit human approval for the account, region, resources, action, and effect; then use `tencent_api_mutate(force=true)`.
5. Secret-resource operations require the separate sensitive gate. STS role/session credentials, SecretId creation, login tokens, and other credential issuance/export actions are never exposed by the gateway.
6. Do not pass endpoints, proxies, TLS-disable flags, SecretId, SecretKey, session tokens, or authorization data as arguments.

## MCP arguments

- `auth_scheme`: `tc3` (default) for API 3.0; `cos` for COS REST data plane.
- `service`: TC3 signing/product code such as `cvm`, `lighthouse`, `cdb`, or `cam`; use `cos` for COS.
- `operation`: exact action, such as `DescribeInstances`, used by TC3 headers and read/write classification.
- `api_version`: required for TC3, such as `2017-03-12`.
- `region`: region context; TC3 sends it as `X-TC-Region` when present.
- `method` and `url`: exact official Tencent Cloud HTTPS request.
- `parameters`: optional scalar query parameters; API 3.0 normally uses a JSON `body`.
- `headers`, `body`, `body_file`: non-credential request data.
- `response_file`: new approved-root file for COS objects, exports, or other large responses. Use the documented single `Range` above the configured per-call limit; existing files are never overwritten.

Example read: `tencent_api_read(auth_scheme="tc3", service="cvm", operation="DescribeInstances", api_version="2017-03-12", region="ap-shanghai", method="POST", url="https://cvm.tencentcloudapi.com/", body={"Limit":20})`.

## Credentials

Use `TENCENTCLOUD_SECRET_ID` / `TENCENTCLOUD_SECRET_KEY` in the MCP server environment. Temporary CAM/STS credentials also use `TENCENTCLOUD_SESSION_TOKEN` or `TENCENTCLOUD_TOKEN`. Never pass credentials in MCP inputs.

Read [references/official-docs.md](references/official-docs.md) for API 3.0, TC3, COS signing, and CAM credentials.

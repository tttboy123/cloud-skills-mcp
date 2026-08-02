---
name: tencent-cloud
description: Operate or inspect any Tencent Cloud resource through TC3, API 3.0 v1, legacy qcloud API 2017, or COS signed HTTPS in cloud-skills-mcp. Use for Tencent Cloud, CVM, Lighthouse, COS, CDB, VPC, CAM, TKE, CloudBase, or any documented Tencent Cloud API.
---

# Tencent Cloud

Use the unified MCP gateway for API 3.0 product actions, still-active qcloud API 2017 actions, and COS REST operations. It calculates TC3-HMAC-SHA256, API 3.0 v1 HmacSHA1/HmacSHA256, legacy qcloud HmacSHA1/HmacSHA256, or COS signatures in-process and never executes TCCLI.

## Workflow

1. Call `cloud_provider_status` with `provider="tencent"`; treat `available` as HTTP adapter readiness and `credential_status=unverified` as pending live authentication.
2. Use `tencent_api_discover` for official API 3.0/TC3/qcloud/COS references, then verify endpoint, version, action, region, and request body in the product API reference.
3. Use `tencent_api_read` only for actions classified as read-only (`Describe*`, `List*`, `Get*`, `Query*`, and similar).
4. For other actions, obtain explicit human approval for the account, region, resources, action, and effect; then use `tencent_api_mutate(force=true)`.
5. Secret-resource operations require the separate sensitive gate. STS role/session credentials, SecretId creation, login tokens, and other credential issuance/export actions are never exposed by the gateway.
6. Do not pass endpoints, proxies, TLS-disable flags, SecretId, SecretKey, session tokens, or authorization data as arguments.

## MCP arguments

- `auth_scheme`: `tc3` (default and recommended) for API 3.0 JSON/multipart calls; `tc1|tc1-sha256` for the still-documented API 3.0 v1 GET/query or `application/x-www-form-urlencoded` protocol; `qcloud|qcloud-sha256` for still-running legacy product endpoints at `*.api.qcloud.com/v2/index.php`; `cos` for COS REST data plane.
- `service`: TC3 signing/product code such as `cvm`, `lighthouse`, `cdb`, or `cam`; use `cos` for COS.
- `operation`: exact action, such as `DescribeInstances`, used by TC3 headers and read/write classification.
- `api_version`: required for TC3, such as `2017-03-12`.
- `region`: region context; TC3 sends it as `X-TC-Region` when present.
- `method` and `url`: exact official Tencent Cloud HTTPS request.
- `parameters`: optional scalar query parameters; API 3.0 normally uses a JSON `body`.
- `headers`, `body`, `body_file`: non-credential request data.
- `response_file`: new approved-root file for COS objects, exports, or other large responses. Use the documented single `Range` above the configured per-call limit; existing files are never overwritten.

Example read: `tencent_api_read(auth_scheme="tc3", service="cvm", operation="DescribeInstances", api_version="2017-03-12", region="ap-shanghai", method="POST", url="https://cvm.tencentcloudapi.com/", body={"Limit":20})`.

V1 read: `tencent_api_read(auth_scheme="tc1", service="cvm", operation="DescribeInstances", api_version="2017-03-12", region="ap-shanghai", method="GET", url="https://cvm.tencentcloudapi.com/", parameters={"Limit":20})`. For a form POST, use `tc1` or `tc1-sha256`, set `Content-Type: application/x-www-form-urlencoded`, and pass the form string as `body`. The adapter signs raw decoded values in ASCII name order, RFC3986-encodes the transmitted values, and injects Action, Version, Timestamp, a cryptographically random positive Nonce, SecretId, optional CAM Token, SignatureMethod, and Signature. Those controlled parameters cannot be supplied by MCP callers.

Legacy Direct Connect read: `tencent_api_read(auth_scheme="qcloud", service="dc", operation="DescribeDirectConnects", region="ap-guangzhou", method="GET", url="https://dc.api.qcloud.com/v2/index.php", parameters={"offset":0,"limit":20})`. Use the exact legacy product endpoint documented for the action. The adapter accepts only a product subdomain ending in `.api.qcloud.com` and the fixed `/v2/index.php` path, signs that path, and injects Action, Timestamp, Nonce, SecretId, `SignatureMethod`, optional CAM Token, and Signature. Unlike API 3.0 v1, this family does not inject Version. Use `qcloud-sha256` when the product supports HmacSHA256; `qcloud` emits the documented HmacSHA1 method.

## Credentials

Use `TENCENTCLOUD_SECRET_ID` / `TENCENTCLOUD_SECRET_KEY` in the MCP server environment. Temporary CAM/STS credentials also use `TENCENTCLOUD_SESSION_TOKEN` or `TENCENTCLOUD_TOKEN`. Never pass credentials in MCP inputs.

Read [references/official-docs.md](references/official-docs.md) for API 3.0 v1/v3, legacy qcloud API 2017, COS signing, and CAM credentials.

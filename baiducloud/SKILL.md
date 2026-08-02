---
name: baidu-cloud
description: Operate or inspect any Baidu AI Cloud BCE resource through cloud-skills-mcp signed HTTPS. Use for Baidu Cloud, BCE, BCC, BOS, VPC, RDS, IAM, CDN, CCE, or documented baidubce.com and bcebos.com APIs.
---

# Baidu AI Cloud

Use the universal BCE REST gateway. It implements the official `bce-auth-v1` HMAC-SHA256 signing contract and restricts requests to validated `baidubce.com` service endpoints plus official BOS `bcebos.com` HTTPS endpoints.

## Workflow

1. Call `cloud_provider_status` with `provider="baiducloud"`.
2. Call `baiducloud_api_discover(service="bcc")` or another product code to obtain the official API-center and product-doc links, then verify the exact endpoint/path.
3. Use `baiducloud_api_read` for `GET`, `HEAD`, or `OPTIONS` only.
4. For `POST`, `PUT`, `PATCH`, or `DELETE`, obtain explicit human approval for the account, region, URL, method, body, and effect; then use `baiducloud_api_mutate(force=true)`.
5. Credential, token, password, and secret operations require the separate sensitive gate.
6. Never provide `Authorization` or `x-bce-security-token`; the adapter creates them internally.

## MCP arguments

- `method` and `url`: exact official BCE HTTPS API endpoint.
- `auth_version`: `v1` by default; use `v2` only when the product API requires it. V2 also requires the documented `service` and `region` values and signs `x-bce-date` internally.
- `headers`: non-credential product headers.
- `body`: JSON-compatible request body.
- `body_file`: binary/media request body under an operator-approved `CLOUD_SKILLS_ALLOWED_FILE_ROOTS` directory. Do not combine it with `body`; use BCE multipart APIs above 64 MiB.

Example read: `baiducloud_api_read(method="GET", url="https://bcc.bj.baidubce.com/v2/instance")`.

## Credentials

Set `BCE_ACCESS_KEY_ID` and `BCE_SECRET_ACCESS_KEY` only in the MCP server environment. For IAM/STS temporary credentials, also set `BCE_SESSION_TOKEN` (or `BCE_SECURITY_TOKEN`). Credentials are signed internally and never returned or audited.

Read [references/official-docs.md](references/official-docs.md) for v1/v2 signing, IAM/STS, BOS, and API-center references.

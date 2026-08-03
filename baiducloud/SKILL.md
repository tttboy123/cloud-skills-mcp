---
name: baidu-cloud
description: Operate or inspect any Baidu AI Cloud BCE resource through cloud-skills-mcp signed HTTPS and the guarded RTC AI Agent WebSocket lifecycle. Use for Baidu Cloud, BCE, BCC, BOS, VPC, RDS, IAM, CDN, CCE, RTC AI Agent, or documented baidubce.com and bcebos.com APIs.
---

# Baidu AI Cloud

Use the universal BCE REST gateway. It implements the official `bce-auth-v1` HMAC-SHA256 signing contract and restricts requests to validated `baidubce.com` service endpoints plus official BOS `bcebos.com` HTTPS endpoints. RTC AI Agent uses a separate guarded `rtc-aiagent-ws` plan: the server signs create/stop through BCE v1, keeps the returned instance token private, activates the operator-held product license internally, and publishes only sanitized bounded WSS events.

## Workflow

1. Call `cloud_provider_status` with `provider="baiducloud"`; `local-material-present` confirms only local AK/SK presence, while `missing-local-material` requires operator credential injection. Neither proves cloud authentication.
2. Call `baiducloud_api_discover(service="bcc")` or another product code to obtain the official API-center and product-doc links, then verify the exact endpoint/path.
3. Use `baiducloud_api_read` for `GET`, `HEAD`, or `OPTIONS` only.
4. For `POST`, `PUT`, `PATCH`, or `DELETE`, obtain explicit human approval for the account, region, URL, method, body, and effect; then use `baiducloud_api_mutate(force=true)`.
5. Secret-resource operations require the separate sensitive gate. STS session credentials, AccessKey creation, login tokens, and other credential issuance/export operations are never exposed by the gateway.
6. Never provide `Authorization` or `x-bce-security-token`; the adapter creates them internally.
7. Treat every RTC AI Agent session as a mutation because create starts a billed instance. Obtain explicit approval, use `baiducloud_api_mutate(force=true)`, and require the server operator to set `BCE_RTC_LICENSE_KEY`; never put AK/SK, the license, or an instance token in MCP arguments.

## MCP arguments

- `method` and `url`: exact official BCE HTTPS API endpoint.
- `auth_version`: `v1` by default; use `v2` only when the product API requires it. V2 also requires the documented `service` and `region` values and signs `x-bce-date` internally.
- `headers`: non-credential product headers.
- `body`: JSON-compatible request body.
- `body_file`: binary/media request body under an operator-approved `CLOUD_SKILLS_ALLOWED_FILE_ROOTS` directory. Generic BCE REST calls do not combine it with `body`; the guarded RTC protocol is the explicit exception because its credential-free body is the session/packet plan. Use BCE multipart APIs above 64 MiB.
- `response_file`: new approved-root file for BOS objects, exports, or other large responses. Use the documented `Range` header above the configured per-call limit; existing files are never overwritten.
- RTC AI Agent: set `auth_scheme="rtc-aiagent-ws"`, `service="rtc-aiagent"`, `operation="RealtimeInteraction"`, `api_version="1"`, method `GET`, and the exact credential-free `wss://rtc-aiotgw.exp.bcelive.com/v1/realtime` URL. Body contains `app_id`, optional credential-free `instance_type`/`config`, `audio_codec` (`raw`, `raw16k`, `pcma`, `pcmu`, `g722`, or `opus`; default `raw16k`), required `device_id`/`user_id`, `messages` before audio, optional `final_messages` after audio, `max_messages` (1–256), `timeout_seconds` (1–300), and `terminal_event` (`tts_end`, `answer`, or `message_limit`). The two command arrays accept at most 64 total official static client commands: break, text/direct TTS, auto-interrupt, device/GIS, remote-player, ASR-mode, system-prompt, variables, credential-free third-party data, scene role, enhanced query, MCP-tool change, direct music, and meeting-summary controls. They reject server-only/license/raw-image events, arbitrary prefixes, embedded credential fields/material, and Function Call results that are not correlated to a received provider event. For event-correlated vision, set `image_file` to one approved-root, non-empty file and optionally set body `image_mode="image_generate"`; the server waits for the exact provider `[E]:[UPLOAD_IMAGE]` event, uploads one official 16 KiB/Base64 chunk sequence atomically against concurrent audio writes, and fails if the request is missing, repeated, or never consumed. The server forces `config.audiocodec` and internal WSS `ac` to the same codec. A fixed-rate `body_file` uses 20–200 ms packets (20 ms default), with an exact codec-derived `stream_chunk_bytes`; Opus requires `opus_packet_time_ms` 20/40/60 and `opus_packet_lengths` that exactly partition the file, with optional `opus_packet_max_bytes`. Audio duration must be shorter than the session timeout. `response_file` is required.

Example read: `baiducloud_api_read(method="GET", url="https://bcc.bj.baidubce.com/v2/instance")`.

## Credentials

Set `BCE_ACCESS_KEY_ID` and `BCE_SECRET_ACCESS_KEY` only in the MCP server environment. For IAM/STS temporary credentials, also set `BCE_SESSION_TOKEN` (or `BCE_SECURITY_TOKEN`). Credentials are signed internally and never returned or audited. An entitled RTC AI Agent deployment additionally sets `BCE_RTC_LICENSE_KEY` in the server environment; it is product entitlement material, never an MCP credential input.

Read [references/official-docs.md](references/official-docs.md) for v1/v2 signing, IAM/STS, BOS, and API-center references.

For RTC, never use the official direct `ak`/`sk` query mode. The Skill always uses BCE v1 create → private instance-token WSS → signed stop. Only the server constructs `ac` and the Opus-only `ptime`/`plen` query values. License `MUST`/`ACTIVE`/`RES-PASS` events, the activation payload, the 24-hour instance token, and control-plane bodies never enter MCP output or audit; failure before stop prevents `response_file` publication.

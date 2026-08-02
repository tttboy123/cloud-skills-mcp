---
name: tencent-cloud
description: Operate or inspect any Tencent Cloud resource through TC3, API 3.0 v1, legacy qcloud API 2017, COS signed HTTPS, or internally connected ASR/MPS recognition and MPS TTS WSS in cloud-skills-mcp. Use for Tencent Cloud, CVM, Lighthouse, COS, CDB, VPC, CAM, TKE, CloudBase, ASR, MPS, TTS, or any documented Tencent Cloud API.
---

# Tencent Cloud

Use the unified MCP gateway for API 3.0 product actions, still-active qcloud API 2017 actions, COS REST operations, finite ASR/MPS recognition streams, and MPS streaming TTS. It calculates all signatures in-process, opens WSS connections internally, and never executes TCCLI or returns signed connection URLs.

## Workflow

1. Call `cloud_provider_status` with `provider="tencent"`; treat `available` as HTTP adapter readiness and `credential_status=unverified` as pending live authentication.
2. Use `tencent_api_discover` for official API 3.0/TC3/qcloud/COS/ASR/MPS recognition/TTS WSS references, then verify endpoint, version, action, region, parameters, and payload format in the product API reference.
3. Use `tencent_api_read` only for actions classified as read-only (`Describe*`, `List*`, `Get*`, `Query*`, and similar).
4. For other actions, obtain explicit human approval for the account, region, resources, action, and effect; then use `tencent_api_mutate(force=true)`.
5. Secret-resource operations require the separate sensitive gate. STS role/session credentials, SecretId creation, login tokens, and other credential issuance/export actions are never exposed by the gateway.
6. Do not pass endpoints, proxies, TLS-disable flags, SecretId, SecretKey, session tokens, or authorization data as arguments.

## MCP arguments

- `auth_scheme`: `tc3` (default and recommended) for API 3.0 JSON/multipart calls; `tc1|tc1-sha256` for the still-documented API 3.0 v1 GET/query or `application/x-www-form-urlencoded` protocol; `qcloud|qcloud-sha256` for still-running legacy product endpoints at `*.api.qcloud.com/v2/index.php`; `cos` for COS REST data plane; `asr-ws` for realtime ASR; `mps-ws` for MPS private-audio recognition/translation; `mps-tts-ws` for MPS streaming speech synthesis.
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

ASR WebSocket read: `tencent_api_read(auth_scheme="asr-ws", service="asr", operation="RecognizeStream", method="GET", url="wss://asr.cloud.tencent.com/asr/v2/<appid>", parameters={"engine_model_type":"16k_zh","voice_format":1}, body_file="<approved-root>/audio.pcm", response_file="<approved-root>/asr.ndjson")`. The server generates a new voice ID and bounded positive nonce, injects timestamp/expiry/SecretId/signature, performs the HTTP Upgrade internally, streams binary frames at the documented default 200ms cadence, sends the final `{"type":"end"}` text frame, and records text responses as bounded NDJSON. It never exposes the signed URL. PCM defaults to 6400-byte 16k or 3200-byte 8k frames; use `stream_chunk_bytes` for compressed or complete-m4a fragments and `stream_interval_ms` only when the media timing requires it.

MPS WebSocket read: `tencent_api_read(auth_scheme="mps-ws", service="mps", operation="RecognizeStream", method="GET", url="wss://mps.cloud.tencent.com/wss/v1/<appid>", parameters={"asrDst":"zh","fragmentNotify":0,"timeoutSec":10}, body_file="<approved-root>/audio.pcm", response_file="<approved-root>/mps.ndjson", stream_user_id="speaker-1", stream_format=1)`. Use `asrDst` for recognition only, or omit it and provide both `transSrc` and `transDst` for recognition plus translation. The adapter generates the required 10-digit nonce, applies the MPS-specific TC3 canonical `post` signature internally, wraps PCM in the documented network-byte-order binary frame, marks the final frame `IsEnd=1`, and finishes only on `ProcessEof`. `stream_format=1` is 16 kHz s16 mono and `2` is 8 kHz; the default 40ms frames are 1280 and 640 bytes respectively. `timeoutSec` is optional and retains the official 120-second default; finite-file callers should normally set a bounded 1–300 second value.

MPS TTS read: `tencent_api_read(auth_scheme="mps-tts-ws", service="mps", operation="SynthesizeSpeech", method="GET", url="wss://mps.cloud.tencent.com/tts/v1/<appid>", parameters={"voiceId":"<voice-id>","format":"mp3","sampleRate":22050,"language":"zh","timeoutSec":30}, body=["first text","second text"], response_file="<approved-root>/speech.mp3")`. The body is one non-empty string or 1–256 string segments; each segment is at most 5000 Unicode characters. The adapter signs the MPS TTS-specific TC3 canonical request including `sha256("")`, waits for negotiated format/sample rate, sends each text segment plus the final empty `Final=true` message, writes binary audio only to the approved new file, and atomically publishes it only after `ProcessEof Code=0`. The signed URL and text never appear in audit events.

## Credentials

Use `TENCENTCLOUD_SECRET_ID` / `TENCENTCLOUD_SECRET_KEY` in the MCP server environment. Temporary CAM/STS credentials also use `TENCENTCLOUD_SESSION_TOKEN` or `TENCENTCLOUD_TOKEN` for the HTTP schemes that document Token. The ASR and MPS WebSocket documents specify AppID plus SecretId/SecretKey but no temporary Token field, so `asr-ws`, `mps-ws`, and `mps-tts-ws` fail closed when a CAM session token is configured. Never pass credentials in MCP inputs.

Read [references/official-docs.md](references/official-docs.md) for API 3.0 v1/v3, legacy qcloud API 2017, COS signing, ASR/MPS WSS, and CAM credentials.

# Changelog

## 0.4.0 (2026-08-04)

- Six-cloud Skills + MCP server: unified stdio server over official HTTPS/WSS
  APIs for AWS, Azure, Google Cloud, Alibaba Cloud, Tencent Cloud, and Baidu
  AI Cloud; 72 `auth_scheme` identifiers implemented or mapped to documented
  exclusions.
- Added Azure OpenAI Chat Completions and Responses streaming SSE, AWS Connect
  chat participant WebSocket with internal SigV4/bearer credential minting and
  bounded SendMessage, plus the earlier WSS/MQTT/AMQP/EventStream families.
- Added `scripts/ci/mapping-audit.sh` (72 schemes + six skill suites + plugin
  bundle drift) and the 72-scheme pre-credential protocol-smoke sweep.
- Added `scripts/ci/live-acceptance.sh` as the single executable entrypoint for
  the opt-in `TestLive*` gates that require operator-injected credentials.
- Open-source Codex plugin packaging under `plugin/` with a marketplace
  manifest, verified end to end with `codex plugin add`.

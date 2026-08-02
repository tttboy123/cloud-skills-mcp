# Cloud MCP Phase 1.5 — Hardening Contract

> Historical phase contract. Current behavior is defined by
> [`six-cloud-full-resource-contract.md`](six-cloud-full-resource-contract.md).

Date: 2026-08-02

## Scope

Phase 1.5 hardens the existing Tencent Cloud CVM PoC. It does not claim that the
other five providers have MCP implementations.

## Trust boundaries

- MCP arguments and LLM output are untrusted input.
- The host application owns human approval and external-action policy.
- `force=true` is a request guard, not proof of approval.
- macOS Keychain, environment variables and vendor CLI config are credential
  sources; credentials must not enter MCP responses or command arguments.
- `tccli` is the only process allowed to cross the Tencent Cloud API boundary.

## Acceptance criteria

- The binary builds from `./cmd/tencent-cloud-mcp`.
- MCP initialization and `tools/list` work without credentials.
- Read-only tools are annotated `readOnlyHint=true` and
  `destructiveHint=false`.
- Mutating tools are disabled unless the operator starts the server with
  `CLOUD_SKILLS_ALLOW_MUTATIONS=1`; a permitted call still needs `force=true`.
- CVM instance ID, region and limit are validated before credential loading or
  process execution.
- JSON payload temporary files are mode 0600 and removed when the handler
  returns.
- Redaction removes named secret values while preserving RequestId and paths.
- Unit and in-process MCP tests do not require a real Keychain or cloud account.
- Automated Go statement coverage is at least 80 percent.
- Build and vulnerability checks use a Go toolchain fixed for GO-2026-5856
  (minimum 1.25.12; repository toolchain 1.26.5).
- Live credential tests are opt-in through `CLOUD_SKILLS_LIVE_TEST=1`.
- Installer smoke tests can run entirely under a temporary directory and do not
  mutate MCP client configuration.

## Explicit non-goals

- No push, release, registry publication or client config mutation.
- No AWS/Azure/GCP/AliCloud/Baidu MCP implementation.
- No claim of production readiness or multi-account authorization.
- No live start/stop operation during automated verification.

---
name: azure-cloud
description: Operate or inspect Azure resources through direct authenticated HTTPS or Azure OpenAI Realtime WSS in cloud-skills-mcp. Use for Azure, ARM, Entra ID, Microsoft Graph, subscriptions, resource groups, Azure OpenAI Realtime, or documented Azure APIs.
---

# Azure Cloud

Use the unified MCP server as a guarded Azure REST and Azure OpenAI Realtime WebSocket gateway. It obtains tokens only from non-CLI Azure Identity credentials: Service Principal environment, Workload Identity, or Managed Identity. There is no `az rest` or Azure CLI credential fallback.

## Workflow

1. Call `cloud_provider_status` with `provider="azure"`; treat `available` as adapter readiness and `credential_status=unverified` as pending live authentication.
2. Verify the resource-provider API version in the official reference. `azure_api_discover` explains the adapter but cannot choose an API version for you.
3. Use `azure_api_read` for REST `GET`, `HEAD`, or `OPTIONS`. Azure OpenAI `auth_scheme="realtime-ws"` uses an HTTP GET upgrade and the fixed read-only operations `RealtimeResponse`, `RealtimeTranscription`, or `RealtimeSession`.
4. For `POST`, `PUT`, `PATCH`, or `DELETE`, obtain explicit human approval for the tenant/subscription, target URL, method, body, and effect; then use `azure_api_mutate(force=true)`.
5. Secret-resource operations require the separate sensitive gate. Credential issuance/export endpoints such as Graph `addPassword` and resource `listKeys` are never exposed by the gateway.
6. Do not place bearer tokens, SAS signatures, client secrets, cookies, or API keys in URL/query/header arguments.

## MCP arguments

- `method` and `url`: exact documented Azure REST request. Hosts are restricted to official Azure/Microsoft domains.
- `subscription`: optional audit and routing context; include the subscription in the documented URL when the API requires it.
- `audience`: optional Microsoft Entra resource/application audience for an uncommon official data-plane endpoint. Supply the documented audience, not a token; the server derives the `.default` scope internally.
- `headers`: non-credential headers such as `If-Match`.
- `body`: JSON-compatible request body.
- `body_file`: binary/media request body under an operator-approved `CLOUD_SKILLS_ALLOWED_FILE_ROOTS` directory. Do not combine it with `body`; use provider multipart/chunk APIs above 64 MiB.
- `response_file`: new approved-root file for blob, export, backup, or other large responses. Use the official `Range`/`x-ms-range` header above the configured per-call limit; existing files are never overwritten.
- `auth_scheme="realtime-ws"`: direct Azure OpenAI Realtime WSS using an internally acquired Microsoft Entra token for `https://ai.azure.com/.default`. Use `service="openai"`, `method="GET"`, a GA `/openai/v1/realtime` URL with exactly `model=<deployment>` or `intent=transcription`, or a preview `/openai/realtime` URL with `api-version` and `deployment`. Never supply handshake headers, API keys, bearer tokens, or an `audience` override.
- For Realtime, `body` is one official client event or an array of events. For larger audio streams, use `body_file` as bounded NDJSON with one complete event per line, including official base64 `input_audio_buffer.append` events and commit events. `response_file` is required and receives validated server events as NDJSON only after all `response.create`/`response.done` and audio commit/transcription-completed pairs reach a successful terminal state. `stream_interval_ms` optionally paces outbound events.

Example read: `azure_api_read(method="GET", url="https://management.azure.com/subscriptions/<id>/resources?api-version=2021-04-01", subscription="<id>")`.

Example Realtime text response: `azure_api_read(auth_scheme="realtime-ws", service="openai", operation="RealtimeResponse", method="GET", url="wss://<resource>.openai.azure.com/openai/v1/realtime", parameters={"model":"<deployment>"}, body=[{"type":"conversation.item.create","item":{"type":"message","role":"user","content":[{"type":"input_text","text":"Please assist the user."}]}},{"type":"response.create"}], response_file="/approved/results/realtime.ndjson")`.

Common ARM, Graph, Storage, Key Vault, SQL, Service Bus, Monitor, App Configuration, Search, Databricks, Grafana, Web PubSub, SignalR, Digital Twins, Synapse, Log Analytics, ACR, Azure Maps, FHIR, and DICOM endpoints have built-in audience routing. Azure Maps callers must include the documented non-secret `x-ms-client-id` header. FHIR uses its service URL as the default audience; DICOM uses `https://dicom.healthcareapis.azure.com`. Use an explicit documented `audience` when a FHIR deployment overrides its default authentication audience.

Public Azure, Azure operated by 21Vianet, Azure US Government, and legacy Germany endpoint suffixes are validated. Configure the matching Azure authority in Azure Identity before calling a sovereign endpoint.

## Credentials

Authenticate the server process through managed identity, workload identity, or an Azure service principal (`AZURE_TENANT_ID`, `AZURE_CLIENT_ID`, `AZURE_CLIENT_SECRET`). Managed Identity is always the final non-CLI source in the lazy Azure Identity chain, so Azure-hosted workloads do not need a cloud CLI or an extra activation flag. Credentials stay in Azure Identity and are never MCP parameters.

Read [references/official-docs.md](references/official-docs.md) before selecting authentication or an API version.

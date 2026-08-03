---
name: azure-cloud
description: Operate or inspect Azure resources through direct authenticated HTTPS, Azure OpenAI Realtime WSS, or Entra-backed Azure Web PubSub JSON, Protobuf, and MQTT WSS in cloud-skills-mcp. Use for Azure, ARM, Entra ID, Microsoft Graph, subscriptions, resource groups, Azure OpenAI Realtime, Web PubSub, MQTT, or documented Azure APIs.
---

# Azure Cloud

Use the unified MCP server as a guarded Azure REST, Azure OpenAI Realtime, and Azure Web PubSub WebSocket gateway. It obtains tokens only from non-CLI Azure Identity credentials: Service Principal environment, Workload Identity, or Managed Identity. There is no `az rest` or Azure CLI credential fallback.

## Workflow

1. Call `cloud_provider_status` with `provider="azure"`; treat `available` as adapter readiness and `credential_status=unverified` as pending live authentication.
2. Verify the resource-provider API version in the official reference. `azure_api_discover` explains the adapter but cannot choose an API version for you.
3. Use `azure_api_read` for REST `GET`, `HEAD`, or `OPTIONS`. Azure OpenAI `auth_scheme="realtime-ws"` uses an HTTP GET upgrade and the fixed read-only operations `RealtimeResponse`, `RealtimeTranscription`, or `RealtimeSession`.
4. Azure Web PubSub `auth_scheme="webpubsub-ws"` always uses `azure_api_mutate(force=true)`. MQTT `auth_scheme="webpubsub-mqtt-ws"` uses `azure_api_read` only for `SubscribeMQTT`; use `azure_api_mutate(force=true)` for the publishing or stateful `ClientMQTT` operation. The server internally mints a five-minute client token through Entra.
5. For `POST`, `PUT`, `PATCH`, or `DELETE`, obtain explicit human approval for the tenant/subscription, target URL, method, body, and effect; then use `azure_api_mutate(force=true)`.
6. Secret-resource operations require the separate sensitive gate. Credential issuance/export endpoints such as Graph `addPassword` and resource `listKeys` are never exposed by the gateway.
7. Do not place bearer tokens, SAS signatures, client secrets, cookies, or API keys in URL/query/header arguments.

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
- `auth_scheme="webpubsub-ws"`: use service `webpubsub`, operation `ClientConnect`, method `GET`, and the exact public-cloud `wss://<resource>.webpubsub.azure.com/client/hubs/<hub>` URL. `body` contains optional `protocol="json|json-reliable|protobuf|protobuf-reliable"` (default `json`), bounded `user_id`, documented `roles` and initial `groups`, 1–256 official logical `messages`, `max_messages`, and `timeout_seconds` (1–300). Protobuf mode converts those JSON-shaped messages to/from official proto3 binary frames and supports join/leave, publish, event, ping, stream start/data/end, text/binary/Any payloads, and every documented downstream message. Use `{typeUrl,value}` with Base64 `value` for Protobuf Any. Reliable mode requires an `ackId` on join/leave/publish/event requests, acknowledges exact uint64 sequences, drops duplicates, retries recovery for at most one minute, and resends only unacknowledged publisher messages; ping and stream control use their dedicated acknowledgements. `response_file` is required. Caller headers, query/recovery parameters, access tokens, client-token/reconnection-token responses, custom endpoints, and body files are forbidden.
- `auth_scheme="webpubsub-mqtt-ws"`: use service `webpubsub`, method `GET`, and exact `wss://<resource>.webpubsub.azure.com/clients/mqtt/hubs/<hub>`. `protocol_version` is `4` (MQTT 3.1.1, default) or `5`; `client_id` is 1–128 ASCII alphanumeric bytes; topics are exact and bounded; `keep_alive_seconds`, `max_messages`, and `timeout_seconds` keep every session finite. `SubscribeMQTT` is read-only and requires 1–8 subscriptions at QoS 0/1/2, clean start, and no publish/Will/session persistence. `ClientMQTT` is mutation-only and accepts up to eight subscriptions plus eight initial Base64 publishes, QoS 0/1/2, optional Last Will, and `max_messages=0` for publish-only calls. MQTT 5 additionally accepts session expiry up to 30 seconds, subscription identifier, payload format/content type/message expiry, and Will delay. The gateway implements both QoS 2 directions exactly once, honors broker Receive Maximum/Maximum Packet Size/Maximum QoS/Server Keep Alive, derives exact join/send roles, and writes Base64 NDJSON atomically. Retained messages, wildcard/shared subscriptions, and topic alias are rejected because Azure does not support them. Username/password, caller tokens, and client certificates are excluded by the operator-IAM-only credential boundary.

Example read: `azure_api_read(method="GET", url="https://management.azure.com/subscriptions/<id>/resources?api-version=2021-04-01", subscription="<id>")`.

Example Realtime text response: `azure_api_read(auth_scheme="realtime-ws", service="openai", operation="RealtimeResponse", method="GET", url="wss://<resource>.openai.azure.com/openai/v1/realtime", parameters={"model":"<deployment>"}, body=[{"type":"conversation.item.create","item":{"type":"message","role":"user","content":[{"type":"input_text","text":"Please assist the user."}]}},{"type":"response.create"}], response_file="/approved/results/realtime.ndjson")`.

Example Web PubSub subscription: `azure_api_mutate(auth_scheme="webpubsub-ws", service="webpubsub", operation="ClientConnect", method="GET", url="wss://<resource>.webpubsub.azure.com/client/hubs/<hub>", body={"roles":["webpubsub.joinLeaveGroup.<group>"],"groups":["<group>"],"messages":[{"type":"joinGroup","group":"<group>","ackId":1}],"max_messages":32,"timeout_seconds":30}, response_file="/approved/results/webpubsub.ndjson", force=true)`. The gateway uses Entra to call the fixed Generate Client Token API, keeps both tokens internal, requires `json.webpubsub.azure.v1`, rejects failed acknowledgements, and atomically publishes only bounded protocol JSON.

For reliable delivery, add `protocol="json-reliable"` to that body. The gateway requires `json.reliable.webpubsub.azure.v1`, removes the reconnection token before output, automatically emits `sequenceAck`, suppresses redelivered sequence IDs, and performs bounded recovery without exposing its internal recovery URL.

For binary Protobuf delivery, use `protocol="protobuf"` or `protocol="protobuf-reliable"`. Example: `azure_api_mutate(auth_scheme="webpubsub-ws", service="webpubsub", operation="ClientConnect", method="GET", url="wss://<resource>.webpubsub.azure.com/client/hubs/<hub>", body={"protocol":"protobuf-reliable","roles":["webpubsub.sendToGroup.<group>"],"messages":[{"type":"sendToGroup","group":"<group>","ackId":1,"dataType":"protobuf","data":{"typeUrl":"type.googleapis.com/example.Message","value":"CAE="}}],"max_messages":32,"timeout_seconds":30}, response_file="/approved/results/webpubsub-protobuf.ndjson", force=true)`. The gateway requires the exact reliable Protobuf subprotocol, uses only binary WebSocket frames, and returns sanitized logical NDJSON instead of raw protobuf or recovery tokens.

Example MQTT 5 read: `azure_api_read(auth_scheme="webpubsub-mqtt-ws", service="webpubsub", operation="SubscribeMQTT", method="GET", url="wss://<resource>.webpubsub.azure.com/clients/mqtt/hubs/<hub>", body={"protocol_version":5,"client_id":"Observer123","subscriptions":[{"topic_filter":"room/in","qos":2}],"subscription_identifier":7,"keep_alive_seconds":30,"max_messages":32,"timeout_seconds":30}, response_file="/approved/results/mqtt.ndjson")`.

Example MQTT 5 publish: `azure_api_mutate(auth_scheme="webpubsub-mqtt-ws", service="webpubsub", operation="ClientMQTT", method="GET", url="wss://<resource>.webpubsub.azure.com/clients/mqtt/hubs/<hub>", body={"protocol_version":5,"client_id":"Publisher123","publishes":[{"topic":"room/out","qos":2,"payload_base64":"aGVsbG8=","payload_format":1,"content_type":"text/plain"}],"keep_alive_seconds":30,"max_messages":0,"timeout_seconds":30}, response_file="/approved/results/mqtt-publish.ndjson", force=true)`.

Common ARM, Graph, Storage, Key Vault, SQL, Service Bus, Monitor, App Configuration, Search, Databricks, Grafana, Web PubSub, SignalR, Digital Twins, Synapse, Log Analytics, ACR, Azure Maps, FHIR, and DICOM endpoints have built-in audience routing. Azure Maps callers must include the documented non-secret `x-ms-client-id` header. FHIR uses its service URL as the default audience; DICOM uses `https://dicom.healthcareapis.azure.com`. Use an explicit documented `audience` when a FHIR deployment overrides its default authentication audience.

Public Azure, Azure operated by 21Vianet, Azure US Government, and legacy Germany endpoint suffixes are validated. Configure the matching Azure authority in Azure Identity before calling a sovereign endpoint.

## Credentials

Authenticate the server process through managed identity, workload identity, or an Azure service principal (`AZURE_TENANT_ID`, `AZURE_CLIENT_ID`, `AZURE_CLIENT_SECRET`). Managed Identity is always the final non-CLI source in the lazy Azure Identity chain, so Azure-hosted workloads do not need a cloud CLI or an extra activation flag. Credentials stay in Azure Identity and are never MCP parameters.

Read [references/official-docs.md](references/official-docs.md) before selecting authentication or an API version.

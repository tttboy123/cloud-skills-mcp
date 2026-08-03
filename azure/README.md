# Azure Skill

本目录把 Azure/ARM/Microsoft Graph 资源意图路由到统一 `cloud-skills-mcp` server 的 `azure_api_discover`、`azure_api_read` 和 `azure_api_mutate`。底层只通过非 CLI 的 Azure Identity 凭证获取 Token 并直连 HTTPS/WSS；Azure OpenAI Realtime 使用 `auth_scheme=realtime-ws`，Azure Web PubSub 普通/可靠 JSON 或 Protobuf 客户端使用 `auth_scheme=webpubsub-ws`，其 MQTT 使用 `auth_scheme=webpubsub-mqtt-ws`，Event Grid Namespace MQTT v5 使用 `auth_scheme=eventgrid-mqtt-ws`。Web PubSub client/recovery token 与 Event Grid `OAUTH2-JWT` 都只在 server 内部使用。其他受控 Azure REST endpoint 可提供公开 `audience` 标识，凭证或 token 始终不是 MCP 参数。

凭证仅使用 managed/workload identity 或 Service Principal。具体规则见 [SKILL.md](SKILL.md)，官方来源见 [references/official-docs.md](references/official-docs.md)。

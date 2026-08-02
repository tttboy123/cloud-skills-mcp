# Azure Skill

本目录把 Azure/ARM/Microsoft Graph 资源意图路由到统一 `cloud-skills-mcp` server 的 `azure_api_discover`、`azure_api_read` 和 `azure_api_mutate`。底层只通过非 CLI 的 Azure Identity 凭证获取 Token 并直连 HTTPS；其他受控 Azure REST endpoint 可提供公开 `audience` 标识，凭证或 token 始终不是 MCP 参数。

凭证仅使用 managed/workload identity 或 Service Principal。具体规则见 [SKILL.md](SKILL.md)，官方来源见 [references/official-docs.md](references/official-docs.md)。

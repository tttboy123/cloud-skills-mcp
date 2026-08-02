# Azure Skill

本目录把 Azure/ARM/Microsoft Graph 资源意图路由到统一 `cloud-skills-mcp` server 的 `azure_api_discover`、`azure_api_read` 和 `azure_api_mutate`。底层优先通过官方 `DefaultAzureCredential` 直连已知 audience，其他受控 Azure REST endpoint 可提供公开 `audience` 标识并回退 `az rest --resource`；凭证或 token 始终不是 MCP 参数。

凭证仅使用 Azure CLI identity、managed/workload identity 或 Service Principal。具体规则见 [SKILL.md](SKILL.md)，官方来源见 [references/official-docs.md](references/official-docs.md)。

# Alibaba Cloud Skill

本目录把阿里云资源意图路由到统一 `cloud-skills-mcp` server 的 `alicloud_api_discover`、`alicloud_api_read` 和 `alicloud_api_mutate`。底层使用固定 Alibaba Cloud CLI product/OpenAPI action，覆盖 CLI 暴露的完整 API surface。

凭证仅使用官方 CLI profile、RAM role/STS 或 AKSK 环境链。具体规则见 [SKILL.md](SKILL.md)，官方来源见 [references/official-docs.md](references/official-docs.md)。

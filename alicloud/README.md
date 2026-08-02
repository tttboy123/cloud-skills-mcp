# Alibaba Cloud Skill

本目录把阿里云资源意图路由到统一 `cloud-skills-mcp` server 的 `alicloud_api_discover`、`alicloud_api_read` 和 `alicloud_api_mutate`。底层直接发送 ACS3 OpenAPI 或 OSS4 数据面 HTTPS 请求，不调用 Alibaba Cloud CLI。

凭证仅使用官方 credentials-go 的 RAM/OIDC/ECS role、STS 或 AKSK 环境链。具体规则见 [SKILL.md](SKILL.md)，官方来源见 [references/official-docs.md](references/official-docs.md)。

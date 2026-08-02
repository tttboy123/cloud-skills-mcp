# Alibaba Cloud Skill

本目录把阿里云资源意图路由到统一 `cloud-skills-mcp` server。底层直接发送 ACS3 OpenAPI、旧版 RPC/ROA V2、DataHub、OSS4、SLS v1/v4、MNS 或 OTS v2/v4 签名 HTTPS 请求，不调用 Alibaba Cloud CLI。

凭证仅使用官方 credentials-go 的 RAM/OIDC/ECS role、STS 或 AKSK 环境链。具体规则见 [SKILL.md](SKILL.md)，官方来源见 [references/official-docs.md](references/official-docs.md)。

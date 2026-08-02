# Baidu AI Cloud Skill

本目录把 BCE 资源意图路由到统一 `cloud-skills-mcp` server 的 `baiducloud_api_discover`、`baiducloud_api_read` 和 `baiducloud_api_mutate`。底层对官方 `baidubce.com` 与 BOS `bcebos.com` HTTPS API 实现 `bce-auth-v1`，并可按 API 文档选择 region/service scoped `bce-auth-v2`。

凭证仅使用 BCE AK/SK 或 IAM/STS temporary AK/SK/session token。具体规则见 [SKILL.md](SKILL.md)，官方来源见 [references/official-docs.md](references/official-docs.md)。

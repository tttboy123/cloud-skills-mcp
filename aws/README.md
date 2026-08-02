# AWS Skill

本目录把 AWS 资源意图路由到统一 `cloud-skills-mcp` server 的 `aws_api_discover`、`aws_api_read` 和 `aws_api_mutate`。底层使用固定的 AWS CLI service/operation，因此覆盖由 AWS CLI 暴露的完整 API surface，而不是固定资源清单。

凭证仅使用 AWS 官方 profile/SSO、IAM role、web identity 或 AKSK/STS 环境链。具体规则见 [SKILL.md](SKILL.md)，官方来源见 [references/official-docs.md](references/official-docs.md)。

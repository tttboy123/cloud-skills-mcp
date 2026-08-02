# Google Cloud Skill

本目录把 GCP 资源意图路由到统一 `cloud-skills-mcp` server 的 `gcp_api_discover`、`gcp_api_read` 和 `gcp_api_mutate`。底层通过官方 Google Auth ADC 链访问受限于 `googleapis.com` 的 authenticated REST API，并使用 public Discovery Service 做 API 发现。

凭证仅使用 ADC、workload identity、impersonation 或 authenticated gcloud identity。具体规则见 [SKILL.md](SKILL.md)，官方来源见 [references/official-docs.md](references/official-docs.md)。

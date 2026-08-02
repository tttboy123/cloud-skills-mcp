# AWS Skill

本目录把 AWS 资源意图路由到统一 `cloud-skills-mcp` server 的 `aws_api_discover`、`aws_api_read` 和 `aws_api_mutate`。底层对精确官方 HTTPS 请求执行 AWS SigV4/SigV4a 签名，也在进程内部完成 Amazon Transcribe 双层 EventStream 与 AWS IoT MQTT 的 SigV4 WSS 握手和有限协议会话，不调用 AWS CLI、不返回预签名 URL，覆盖边界是官方 HTTP/WSS 协议族而不是固定资源清单。

凭证仅使用 AWS 官方 profile/SSO、IAM role、web identity 或 AKSK/STS 环境链。具体规则见 [SKILL.md](SKILL.md)，官方来源见 [references/official-docs.md](references/official-docs.md)。

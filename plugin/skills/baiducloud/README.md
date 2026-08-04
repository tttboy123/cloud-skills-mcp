# Baidu AI Cloud Skill

本目录把 BCE 资源意图路由到统一 `cloud-skills-mcp` server 的 `baiducloud_api_discover`、`baiducloud_api_read` 和 `baiducloud_api_mutate`。底层对官方 `baidubce.com` 与 BOS `bcebos.com` HTTPS API 实现 `bce-auth-v1`，并可按 API 文档选择 region/service scoped `bce-auth-v2`；`iotcore-http-pub` 以内置 60 秒 token 完成 HTTPS 发布，`rtc-aiagent-ws` 则实现 BCE create、私有 instance-token WSS、内部 license 激活与 stop 的原子生命周期。

云身份仅使用 BCE AK/SK 或 IAM/STS temporary AK/SK/session token。RTC 产品 license 仅由 operator 通过 `BCE_RTC_LICENSE_KEY` 注入，不出现在 MCP 调用面。具体规则见 [SKILL.md](SKILL.md)，官方来源见 [references/official-docs.md](references/official-docs.md)。

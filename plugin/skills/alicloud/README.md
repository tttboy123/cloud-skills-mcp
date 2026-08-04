# Alibaba Cloud Skill

本目录把阿里云资源意图路由到统一 `cloud-skills-mcp` server。底层直接发送 ACS3、RPC/ROA V2、DataHub、OpenSearch V3、MaxCompute ODPS v2/v4、Function Compute、OSS v1/v4、SLS v1/v4、MNS、RocketMQ 4.x、ACR 企业版 Docker/OCI Registry、OTS v2/v4 签名 HTTPS 请求，也支持 Intelligent Speech Interaction 的受控 NLS HTTPS/WSS 识别与合成，不调用 Alibaba Cloud CLI、Docker 或 credential helper。

凭证仅使用官方 credentials-go 的 RAM/OIDC/ECS role、STS 或 AKSK 环境链。ACR 临时登录/Bearer 凭证和 NLS 临时 Token 都只在 server 内部派生，不通过 MCP 参数、响应或审计暴露；NLS AppKey 只是项目标识。具体规则见 [SKILL.md](SKILL.md)，官方来源见 [references/official-docs.md](references/official-docs.md)。

# Google Cloud Skill

本目录把 GCP 资源意图路由到统一 `cloud-skills-mcp` server 的 `gcp_api_discover`、`gcp_api_read` 和 `gcp_api_mutate`。底层通过官方 Google Auth ADC 链访问受限于 `googleapis.com` 的 authenticated REST API，也可用 `auth_scheme=grpc` 通过原生 HTTP/2 传输已经按官方 protobuf schema 编码并加上标准五字节 gRPC frame header 的有限请求流，或用 `auth_scheme=vertex-live-ws` 启动 setup-first、有限时长/消息数、原子 NDJSON 输出的 Vertex/Gemini Live 会话；REST API 使用 public Discovery Service 做发现，gRPC-only API 使用官方 RPC/protobuf reference。

凭证仅使用 ADC、workload identity、impersonation 或 metadata identity；运行时不调用 gcloud。具体规则见 [SKILL.md](SKILL.md)，官方来源见 [references/official-docs.md](references/official-docs.md)。

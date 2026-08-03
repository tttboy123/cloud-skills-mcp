# Google Cloud Skill

本目录把 GCP 资源意图路由到统一 `cloud-skills-mcp` server 的 `gcp_api_discover`、`gcp_api_read` 和 `gcp_api_mutate`。底层通过官方 Google Auth ADC 链访问受限于 `googleapis.com` 的 authenticated REST API；`auth_scheme=grpc` 既可通过原生 HTTP/2 传输已有的标准五字节 framed protobuf，也可用 `payload_mode=protobuf-json` 和 operator-approved `FileDescriptorSet` 在 server 内完成 ProtoJSON↔protobuf、有限流和原子 NDJSON 输出；`auth_scheme=vertex-live-ws` 支持 setup-first、有限时长/消息数、动态函数响应、内部 session resumption 和原子 NDJSON。REST API 使用 public Discovery Service 做发现，gRPC-only API 使用官方 RPC/protobuf reference。运行时不调用 gcloud、grpcurl、protoc 或其他 CLI。

凭证仅使用 ADC、workload identity、impersonation 或 metadata identity；运行时不调用 gcloud。具体规则见 [SKILL.md](SKILL.md)，官方来源见 [references/official-docs.md](references/official-docs.md)。

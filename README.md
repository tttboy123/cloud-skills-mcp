# cloud-skills-mcp

面向 AI Agent 的六云全资源 Skill + MCP 服务。统一 stdio MCP server 通过各云官方通用 API/CLI 入口访问 AWS、Azure、Google Cloud、Alibaba Cloud、Tencent Cloud 和 Baidu AI Cloud 的已公开资源 API；新增服务或资源不需要再增加一个 Go handler。

## 能力模型

服务暴露 19 个工具：

- `cloud_provider_status`
- 每个云一个 `<provider>_api_discover`
- 每个云一个 `<provider>_api_read`
- 每个云一个 `<provider>_api_mutate`

provider 前缀为 `aws`、`azure`、`gcp`、`alicloud`、`tencent`、`baiducloud`。

| 云 | 通用访问层 | 官方身份链 |
|---|---|---|
| AWS | AWS CLI 任意 service/operation；Cloud Control 可作为标准资源模型 | Profile/SSO、IAM Role、Web Identity、AKSK/STS |
| Azure | Azure Identity 直连 ARM/Graph/常见数据面，其他官方 endpoint 回退 `az rest` | `DefaultAzureCredential`、Managed Identity、Workload Identity、Service Principal、`az login` |
| Google Cloud | Google Auth ADC 直连 `googleapis.com` REST + Discovery Service，gcloud identity fallback | ADC、Workload Identity、Impersonation、gcloud identity |
| Alibaba Cloud | Alibaba Cloud CLI 任意 product/OpenAPI action | CLI profile、RAM role/STS、AKSK |
| Tencent Cloud | TCCLI 任意 API 3.0 product/action | TCCLI profile、CAM role/STS、SecretId/SecretKey |
| Baidu AI Cloud | `baidubce.com`/BOS `bcebos.com` signed HTTPS，支持 `bce-auth-v1` 与按 API 选择 v2 | BCE AK/SK、IAM/STS temporary AK/SK/session token |

原有 `tencent-cloud-mcp` 15 个细粒度工具继续保留，作为兼容入口。

## 安全边界

- 默认只读。read 工具只允许保守分类的 CLI 查询操作或 REST `GET`/`HEAD`/`OPTIONS`。
- 写操作必须同时满足宿主已取得人类批准、server 环境设置 `CLOUD_SKILLS_ALLOW_MUTATIONS=1`、单次调用包含 `force=true`。
- secret/password/token/credential/access-key 类操作还要求 `CLOUD_SKILLS_ALLOW_SENSITIVE=1`。
- CLI executable 固定且不经过 shell；service、operation、参数、文件引用和 endpoint 均先校验。
- REST 只允许官方 HTTPS 域名、443 端口且禁止 redirect；调用方不能提供 Authorization、API key、cookie、SAS/signed URL 或 session-token 参数。Azure 非常见数据面可提供公开的 Entra `audience` 标识，但不能提供 token。
- 新发布且尚未进入内置域名表的官方 REST endpoint，只能由 operator 在 server 启动环境通过 `CLOUD_SKILLS_AZURE_ALLOWED_ENDPOINT_HOSTS`、`CLOUD_SKILLS_GCP_ALLOWED_ENDPOINT_HOSTS` 或 `CLOUD_SKILLS_BAIDU_ALLOWED_ENDPOINT_HOSTS` 追加逗号分隔的精确 hostname；不接受 URL、端口、子域 wildcard 或 MCP 参数。
- REST 数据面上传可使用 `body_file`，Alibaba/AWS/Tencent CLI 可使用官方文件参数；本地文件只允许位于 `CLOUD_SKILLS_ALLOWED_FILE_ROOTS` 下，且会解析 symlink 后再判断。REST 单文件默认上限 64 MiB，更大对象使用厂商 multipart/chunk API。
- CLI 和 HTTP 响应均有大小上限；结果会做 credential-field redaction。
- `CLOUD_SKILLS_AUDIT_LOG` 写入 mode `0600` JSONL。审计不记录请求 body、headers、response 或 URL query；写操作的预执行审计失败时 fail closed。
- MCP server 不创建、不更新、不导出凭证。凭证只来自各云官方身份链或 operator 注入的 AKSK/IAM 环境。
- STS AssumeRole/session token、登录 token、AccessKey/API key 创建、Service Account key、Graph `addPassword`、云资源 `listKeys` 等凭证签发或导出操作会被硬拒绝，不能用 mutation/sensitive 开关绕过；普通 IAM/RAM/CAM 角色、策略和成员资源管理仍可调用。

`force=true` 和环境开关只是 server 技术门，不能替代 MCP 宿主的人类批准。

## 安装

要求 Go 1.25.12+。AWS、Alibaba Cloud、Tencent Cloud 需要对应官方 CLI。Azure 非 ARM/Graph/常见数据面操作可能回退 Azure CLI；Google Cloud CLI 仅作为没有 ADC 时的兼容身份 fallback。百度 BCE adapter 不依赖额外 CLI。

```bash
git clone https://github.com/tttboy123/cloud-skills-mcp.git
cd cloud-skills-mcp

./install.sh \
  --bin-dir "$HOME/.local/bin" \
  --skills-dir "$HOME/.codex/skills"
```

安装器会构建 `cloud-skills-mcp` 和兼容的 `tencent-cloud-mcp`，复制六份 Skill；不会修改 MCP 客户端配置、云凭证或云资源。已有 Skill 默认不覆盖，只有显式传 `--force` 才替换。

只构建仓库内二进制：

```bash
./build-mcp-servers.sh
```

## 注册统一 MCP server

Codex CLI：

```bash
codex mcp add cloud-skills -- "$HOME/.local/bin/cloud-skills-mcp"
```

兼容 stdio MCP 的其他宿主：

```json
{
  "mcpServers": {
    "cloud-skills": {
      "command": "/absolute/path/to/cloud-skills-mcp",
      "env": {}
    }
  }
}
```

把凭证/IAM 环境只注入 server 进程，不要放在 MCP tool arguments 或 prompt 中。

## 凭证入口

```text
AWS       AWS_PROFILE / AWS_ROLE_ARN + AWS_WEB_IDENTITY_TOKEN_FILE /
          AWS_ACCESS_KEY_ID + AWS_SECRET_ACCESS_KEY (+ AWS_SESSION_TOKEN)
Azure     DefaultAzureCredential / az login / managed identity /
          AZURE_TENANT_ID + AZURE_CLIENT_ID + AZURE_CLIENT_SECRET
GCP       ADC / GOOGLE_APPLICATION_CREDENTIALS / workload identity / gcloud auth fallback
Alibaba   aliyun profile / ALIBABA_CLOUD_ACCESS_KEY_ID + ALIBABA_CLOUD_ACCESS_KEY_SECRET
          (+ ALIBABA_CLOUD_SECURITY_TOKEN for STS)
Tencent   tccli profile / TENCENTCLOUD_SECRET_ID + TENCENTCLOUD_SECRET_KEY
          (+ TENCENTCLOUD_TOKEN for CAM/STS)
Baidu     BCE_ACCESS_KEY_ID + BCE_SECRET_ACCESS_KEY (+ BCE_SESSION_TOKEN)
```

优先使用最小权限 IAM/RAM/CAM role 或临时凭证；不要给日常只读 server 长期写权限。

## MCP 调用示例

AWS 查询：

```json
{"name":"aws_api_read","arguments":{"service":"ec2","operation":"describe-instances","region":"us-east-1","parameters":{"MaxResults":20}}}
```

Azure 查询：

```json
{"name":"azure_api_read","arguments":{"method":"GET","url":"https://management.azure.com/subscriptions/<id>/resources?api-version=2021-04-01","subscription":"<id>"}}
```

GCP 查询：

```json
{"name":"gcp_api_read","arguments":{"method":"GET","url":"https://compute.googleapis.com/compute/v1/projects/<project>/aggregated/instances","project":"<project>"}}
```

Alibaba/Tencent 使用 `service` + `operation` + `parameters`；Baidu 使用 `method` + 官方 `baidubce.com` 或 BOS `bcebos.com` URL。Azure/GCP/Baidu 的二进制或媒体 request body 使用受控 `body_file`，文件内容不会进入模型上下文。六份 Skill 中有完整路由规则和官方文档入口。

## 验证

无需凭证的 hermetic gate：

```bash
go test -race ./...
go vet ./...
go build -trimpath ./cmd/cloud-skills-mcp ./cmd/tencent-cloud-mcp
./scripts/ci/protocol-smoke.sh ./bin/cloud-skills-mcp
./scripts/ci/install-smoke.sh
```

真实云只读验收必须由 operator 显式打开，并只从进程环境读取凭证：

```bash
CLOUD_SKILLS_LIVE_TEST=1 go test ./internal/mcp/cloud -run TestLiveSixCloudReadOnly -v
```

GCP live 验收需要 `CLOUD_SKILLS_LIVE_GCP_PROJECT`。各云可用 `CLOUD_SKILLS_LIVE_PROVIDERS` 选择子集。live gate 永不调用 mutate 工具，也不打印响应正文；它逐云输出审计 outcome、响应字节数和可用的 provider RequestId。

## 目录

```text
cmd/cloud-skills-mcp/       # 六云统一 stdio server
cmd/tencent-cloud-mcp/     # Tencent 15 工具兼容入口
internal/mcp/cloud/        # 通用 contract、policy 与六云 adapters
internal/mcp/tencent/      # Tencent 细粒度兼容 server
aws/ azure/ google-cloud/ alicloud/ tencent-cloud/ baiducloud/
                            # 六份 Skills + 官方文档映射
docs/six-cloud-full-resource-contract.md
scripts/ci/                # 协议、安装与安全验证
```

## 官方文档

每份 Skill 的 `references/official-docs.md` 保存对应云厂商的认证、CLI/REST、资源发现和 API reference 链接。通用访问层的完成定义、安全合同与真实验收门见 [docs/six-cloud-full-resource-contract.md](docs/six-cloud-full-resource-contract.md)，逐项完成证据与仍未通过的 live gate 见 [docs/goal-completion-matrix.md](docs/goal-completion-matrix.md)。

## License

MIT

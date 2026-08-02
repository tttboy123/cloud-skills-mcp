# cloud-skills-mcp

面向 AI Agent 的六云全资源 Skill + MCP 服务。统一 stdio MCP server 仅通过各云官方 HTTPS API 访问 AWS、Azure、Google Cloud、Alibaba Cloud、Tencent Cloud 和 Baidu AI Cloud 的已公开资源 API；新增服务或资源不需要再增加一个 Go handler，也不会启动云厂商 CLI 子进程。

## 能力模型

服务暴露 19 个工具：

- `cloud_provider_status`
- 每个云一个 `<provider>_api_discover`
- 每个云一个 `<provider>_api_read`
- 每个云一个 `<provider>_api_mutate`

provider 前缀为 `aws`、`azure`、`gcp`、`alicloud`、`tencent`、`baiducloud`。

`cloud_provider_status.available` 表示 HTTP adapter 可用，不表示云端认证已经成功。`credential_status=unverified` 表示身份链会在首次 API 调用时延迟解析；缺少明确本地材料时可报告 `missing-local-material`。只有成功的只读 live API 调用才证明凭证和 IAM 权限可用。

| 云 | 通用访问层 | 官方身份链 |
|---|---|---|
| AWS | 任意官方 endpoint 的 SigV4 HTTPS；多区域 API 的纯 Go SigV4a | AWS SDK credential chain：IAM Role、Web Identity、profile/SSO、AKSK/STS |
| Azure | Azure Identity Bearer Token + ARM/Graph/数据面 HTTPS | 非 CLI 的 Service Principal、Workload Identity、Managed Identity |
| Google Cloud | Google Auth ADC + `googleapis.com` HTTPS / Discovery Service | ADC、Workload Identity、Service Account、Impersonation、Metadata Identity |
| Alibaba Cloud | ACS3；旧版 RPC/ROA V2；DataHub；OSS4；SLS v1/v4；MNS；OTS v2/v4 签名 HTTPS | 官方 credentials-go：AKSK/STS、RAM/OIDC、ECS RAM Role |
| Tencent Cloud | API 3.0 TC3 HTTPS；COS 数据面 COS signed HTTPS | SecretId/SecretKey 或 CAM/STS 临时三元组 |
| Baidu AI Cloud | `baidubce.com`/BOS `bcebos.com` signed HTTPS，支持 `bce-auth-v1` 与按 API 选择 v2 | BCE AK/SK、IAM/STS temporary AK/SK/session token |

## 安全边界

- 默认只读。Azure/GCP/Baidu read 工具只允许 `GET`/`HEAD`/`OPTIONS`；AWS/Alibaba/Tencent 的 POST 查询按官方 action 名称保守分类。
- 写操作必须同时满足宿主已取得人类批准、server 环境设置 `CLOUD_SKILLS_ALLOW_MUTATIONS=1`、单次调用包含 `force=true`。
- secret/password/token/credential/access-key 类操作还要求 `CLOUD_SKILLS_ALLOW_SENSITIVE=1`。
- 所有六云请求都由进程内 HTTP adapter 构造和签名；统一 Server 不执行 `aws`、`az`、`gcloud`、`aliyun` 或 `tccli`。
- REST 只允许官方 HTTPS 域名、443 端口且禁止 redirect；调用方不能提供 Authorization、API key、cookie、SAS/signed URL 或 session-token 参数。Azure 非常见数据面可提供公开的 Entra `audience` 标识，但不能提供 token。
- 新发布且尚未进入内置域名表的官方 endpoint，只能由 operator 通过 `CLOUD_SKILLS_<PROVIDER>_ALLOWED_ENDPOINT_HOSTS` 追加逗号分隔的精确 hostname；不接受 URL、端口、子域 wildcard 或 MCP 参数。
- 六云数据面上传均可使用 `body_file`；本地文件只允许位于 `CLOUD_SKILLS_ALLOWED_FILE_ROOTS` 下，且会解析 symlink 后再判断。单文件默认上限 64 MiB，更大对象使用厂商 multipart/chunk API。
- 六云成功响应均可使用 `response_file` 流式写入批准目录中的新文件，正文不会进入 MCP/模型上下文。目标文件绝不覆盖，临时文件以 mode `0600` 写完并同步后才原子发布；默认单次上限 1 GiB，可用 `CLOUD_SKILLS_MAX_RESPONSE_FILE_BYTES` 收紧或调大，更大对象使用厂商 `Range` API 分段下载。
- HTTP 响应有大小上限；结果会做 credential-field redaction。
- `CLOUD_SKILLS_AUDIT_LOG` 写入 mode `0600` JSONL。审计不记录请求 body、headers、response 或 URL query；写操作的预执行审计失败时 fail closed。
- MCP server 不创建、不更新、不导出凭证。凭证只来自各云官方身份链或 operator 注入的 AKSK/IAM 环境。
- STS AssumeRole/session token、登录 token、AccessKey/API key 创建、Service Account key、Graph `addPassword`、云资源 `listKeys` 等凭证签发或导出操作会被硬拒绝，不能用 mutation/sensitive 开关绕过；普通 IAM/RAM/CAM 角色、策略和成员资源管理仍可调用。

`force=true` 和环境开关只是 server 技术门，不能替代 MCP 宿主的人类批准。

## 安装

要求 Go 1.25.12+。统一 Server 不要求安装任何云厂商 CLI；身份由 server 环境、官方 SDK 凭证文件、Workload/Managed Identity 或实例 Metadata 提供。

```bash
git clone https://github.com/tttboy123/cloud-skills-mcp.git
cd cloud-skills-mcp

./install.sh \
  --bin-dir "$HOME/.local/bin" \
  --skills-dir "$HOME/.codex/skills"
```

安装器只构建 HTTP-only 的 `cloud-skills-mcp`，并复制六份 Skill；不会修改 MCP 客户端配置、云凭证或云资源。已有 Skill 默认不覆盖，只有显式传 `--force` 才替换。

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
Azure     managed identity / workload identity /
          AZURE_TENANT_ID + AZURE_CLIENT_ID + AZURE_CLIENT_SECRET
GCP       ADC / GOOGLE_APPLICATION_CREDENTIALS / workload identity / metadata identity
Alibaba   RAM/OIDC/ECS role / ALIBABA_CLOUD_ACCESS_KEY_ID + ALIBABA_CLOUD_ACCESS_KEY_SECRET
          (+ ALIBABA_CLOUD_SECURITY_TOKEN for STS)
Tencent   TENCENTCLOUD_SECRET_ID + TENCENTCLOUD_SECRET_KEY
          (+ TENCENTCLOUD_SESSION_TOKEN or TENCENTCLOUD_TOKEN for CAM/STS)
Baidu     BCE_ACCESS_KEY_ID + BCE_SECRET_ACCESS_KEY (+ BCE_SESSION_TOKEN)
```

优先使用最小权限 IAM/RAM/CAM role 或临时凭证；不要给日常只读 server 长期写权限。

## MCP 调用示例

AWS 查询：

```json
{"name":"aws_api_read","arguments":{"auth_scheme":"sigv4","service":"ec2","operation":"describe-instances","region":"us-east-1","method":"POST","url":"https://ec2.us-east-1.amazonaws.com/","headers":{"Content-Type":"application/x-www-form-urlencoded"},"body":"Action=DescribeInstances&Version=2016-11-15&MaxResults=20"}}
```

AWS S3 Multi-Region Access Point 使用 `auth_scheme=sigv4a` 和官方 region set；`region_set` 只定义签名可用区域，不是凭证：

```json
{"name":"aws_api_read","arguments":{"auth_scheme":"sigv4a","service":"s3","operation":"get-object","region_set":"us-east-1,us-west-*","method":"GET","url":"https://<alias>.accesspoint.s3-global.amazonaws.com/object","response_file":"/approved/downloads/object.bin"}}
```

S3 `PutObject`/`UploadPart` 的 SigV4 或 SigV4a 流式上传使用 `payload_mode=aws-chunked`。服务端固定使用官方建议的 64 KiB chunk，并生成对应的 HMAC 或定长 DER-ECDSA 链式签名；签名头不能由 MCP 调用方传入：

```json
{"name":"aws_api_mutate","arguments":{"auth_scheme":"sigv4","payload_mode":"aws-chunked","service":"s3","operation":"put-object","region":"us-east-1","method":"PUT","url":"https://<bucket>.s3.us-east-1.amazonaws.com/object","body_file":"/approved/uploads/object.bin","force":true}}
```

需要 S3 签名尾随校验和时使用 `payload_mode=aws-chunked-trailer`，并从 `crc32`、`crc32c`、`crc64nvme`、`sha1`、`sha256` 中选择 `checksum_algorithm`。校验和值由服务端在流式读取时计算，调用方不能提交或覆盖它；`crc64nvme` 是当前 S3 的默认完整性算法：

```json
{"name":"aws_api_mutate","arguments":{"auth_scheme":"sigv4","payload_mode":"aws-chunked-trailer","checksum_algorithm":"crc64nvme","service":"s3","operation":"put-object","region":"us-east-1","method":"PUT","url":"https://<bucket>.s3.us-east-1.amazonaws.com/object","body_file":"/approved/uploads/object.bin","force":true}}
```

多区域 S3 endpoint 使用相同两个 `payload_mode`，把 `auth_scheme` 改为 `sigv4a` 并提供官网定义的 `region_set`；流式请求的 seed、每个 chunk 与可选 trailer 都由同一个服务端派生密钥链签名。

需要 SigV4 EventStream 请求签名的有限 HTTP 流使用 `payload_mode=aws-eventstream`。`body_file` 是一个或多个连续的、CRC 有效但尚未添加签名外层的 Amazon EventStream 帧；服务端验证单帧边界后添加 `:date`、链式 `:chunk-signature` 和终止帧。交互式 WebSocket 会话不属于这个有限 HTTP 请求模式。

```json
{"name":"aws_api_mutate","arguments":{"auth_scheme":"sigv4","payload_mode":"aws-eventstream","service":"transcribestreaming","operation":"start-stream-transcription","region":"us-east-1","method":"POST","url":"https://transcribestreaming.us-east-1.amazonaws.com/stream-transcription","body_file":"/approved/streams/audio.events","response_file":"/approved/streams/transcript.events","force":true}}
```

Azure 查询：

```json
{"name":"azure_api_read","arguments":{"method":"GET","url":"https://management.azure.com/subscriptions/<id>/resources?api-version=2021-04-01","subscription":"<id>"}}
```

GCP 查询：

```json
{"name":"gcp_api_read","arguments":{"method":"GET","url":"https://compute.googleapis.com/compute/v1/projects/<project>/aggregated/instances","project":"<project>"}}
```

Alibaba ACS3 使用 `auth_scheme=acs3`；旧版 RPC/ROA V2 使用 `rpc|roa`；DataHub 使用 `datahub`；OSS 使用 `oss4`，SLS 使用 `sls|sls4`，MNS 使用 `mns`，Tablestore 使用 `ots|ots4`。Tencent API 3.0 使用 `tc3`，COS 使用 `cos`。Baidu 使用 `auth_version=v1|v2`。六云二进制或媒体 request body 都可使用受控 `body_file`；大响应使用 `response_file`，文件内容不会进入模型上下文。

Google Cloud Storage 分段下载示例（其他五云同样使用各自官方 GetObject/Get Blob URL 与 `Range` header）：

```json
{"name":"gcp_api_read","arguments":{"method":"GET","url":"https://storage.googleapis.com/storage/v1/b/<bucket>/o/<url-encoded-object>?alt=media","headers":{"Range":"bytes=0-104857599"},"response_file":"/approved/downloads/object.part-000"}}
```

## 验证

无需凭证的 hermetic gate：

```bash
go test -race ./...
go vet ./...
go build -trimpath ./cmd/cloud-skills-mcp
./scripts/ci/protocol-smoke.sh ./bin/cloud-skills-mcp
./scripts/ci/install-smoke.sh
```

真实云只读验收必须由 operator 显式打开，并只从进程环境读取凭证：

```bash
CLOUD_SKILLS_LIVE_TEST=1 go test ./internal/mcp/cloud -run TestLiveSixCloudReadOnly -v
```

GCP live 验收需要 `CLOUD_SKILLS_LIVE_GCP_PROJECT`。各云可用 `CLOUD_SKILLS_LIVE_PROVIDERS` 选择子集。live gate 永不调用 mutate 工具，也不打印响应正文；它逐云输出 adapter availability、credential source/status、审计 outcome、响应字节数和可用的 provider RequestId。

## 目录

```text
cmd/cloud-skills-mcp/       # 六云统一 stdio server
internal/mcp/cloud/        # 通用 contract、policy 与六云 adapters
aws/ azure/ google-cloud/ alicloud/ tencent-cloud/ baiducloud/
                            # 六份 Skills + 官方文档映射
docs/six-cloud-full-resource-contract.md
scripts/ci/                # 协议、安装与安全验证
```

## 官方文档

每份 Skill 的 `references/official-docs.md` 保存对应云厂商的认证、HTTP 签名、资源发现和 API reference 链接。通用访问层的完成定义、安全合同与真实验收门见 [docs/six-cloud-full-resource-contract.md](docs/six-cloud-full-resource-contract.md)，认证/传输族的真实覆盖与缺口见 [docs/api-protocol-coverage.md](docs/api-protocol-coverage.md)，逐项完成证据与仍未通过的 live gate 见 [docs/goal-completion-matrix.md](docs/goal-completion-matrix.md)。

## License

MIT

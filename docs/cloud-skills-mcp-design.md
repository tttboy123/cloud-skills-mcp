# 云厂商 MCP + Skills 双形态方案

> **Status**: DRAFT v1.0
> **Author**: Mavis (2026-08-01)
> **Workspace**: `/Users/lune/Documents/Codex/2026-06-18/hermes-openclaw/agent-platform/`

> **Current-state note (2026-08-02):** This is a historical design record.
> The active product boundary is defined by `README.md` and
> `docs/six-cloud-full-resource-contract.md`: the universal stdio server covers
> the six providers through 19 guarded direct-HTTPS gateway tools. The former
> CLI-backed Tencent compatibility server is no longer built, installed, or
> shipped. CLI/fallback designs, phased resource lists, and placeholders below
> are retained only as design history and do not describe the current runtime.

## 1. 目标

为 6 大云厂商 (Google Cloud / Azure / AWS / AliCloud / TencentCloud / BaiduCloud) 搭建**双形态管理接口**：
- **SKILL.md** = 流程化 SOP，触发式加载
- **MCP Server** = 原子 API 工具，stdin/stdout JSON-RPC 持续运行

两者**不是二选一，是分层**：SKILL.md 是门脸（用户自然语言触发），MCP server 是手脚（执行原子操作）。

## 2. 核心设计原则

| 原则 | 说明 |
|---|---|
| **双形态共存** | SKILL.md 入口 + MCP server 执行，缺一不可 |
| **凭证零暴露** | 全部走 macOS Keychain / 云 CLI 自身凭证机制 |
| **Loom 集成** | 跟 v2.0 spec §3.6 Cloud Provider Adapter 对齐 |
| **渐进披露** | SKILL.md < 5K tokens（触发后），MCP 工具列表按需加载 |
| **Fail Open** | MCP 不可用时降级到 SKILL.md 内的 bash 脚本（fallback） |
| **YAGNI** | 先 6 云 × 4 核心工具（24 工具）验证架构，再扩到 12 工具/云 |

## 3. 架构图

```
┌─────────────────────────────────────────────────────────────┐
│  User: "帮我重启那台 web-prod-01"                            │
└────────────┬────────────────────────────────────────────────┘
             │
             ▼
┌─────────────────────────────────────────────────────────────┐
│  Claude / Cursor / Codex / Loom daemon                       │
│  1. SKILL.md 触发 (加载 ~/.claude/skills/aws/SKILL.md)        │
│  2. 看到 "调 aws_ec2_* 工具"                                  │
│  3. 通过 mcp_servers.json 找到 aws-mcp                       │
└────────────┬────────────────────────────────────────────────┘
             │ JSON-RPC over stdio / SSE
             ▼
┌─────────────────────────────────────────────────────────────┐
│  aws-mcp-server (Go binary, ~/bin/aws-mcp)                   │
│  ┌─────────────────────────────────────────────────────┐   │
│  │ Tools:                                              │   │
│  │  - aws_ec2_list_instances                            │   │
│  │  - aws_ec2_describe_instance                         │   │
│  │  - aws_ec2_start_instance                            │   │
│  │  - aws_ec2_stop_instance                             │   │
│  │  - aws_s3_list_buckets                               │   │
│  │  - aws_s3_get_object                                 │   │
│  │  - aws_s3_put_object                                 │   │
│  │  - aws_rds_list_instances                            │   │
│  │  - aws_vpc_list_vpcs                                 │   │
│  │  - aws_secretsmanager_get_secret                     │   │
│  └─────────────────────────────────────────────────────┘   │
│  ↕ aws-sdk-go-v2 (官方 SDK)                                  │
└────────────┬────────────────────────────────────────────────┘
             │ HTTPS + SigV4
             ▼
┌─────────────────────────────────────────────────────────────┐
│  AWS API (ec2.us-east-1.amazonaws.com)                       │
└─────────────────────────────────────────────────────────────┘
```

## 4. 6 个云的服务映射

| 云 | CLI / SDK | 默认 region | 凭证 Keychain service | env var | 核心服务对应 |
|---|---|---|---|---|---|
| **AWS** | `aws` / aws-sdk-go-v2 | us-east-1 | `aws` | `AWS_ACCESS_KEY_ID` | EC2/S3/RDS/VPC/SecretsMgr/EKS/Lambda/CloudFront |
| **Azure** | `az` / azure-sdk-for-go | eastus | `azure` | `AZURE_SUBSCRIPTION_ID` | VM/Blob/SQL/VNet/KeyVault/AKS/Functions/CDN |
| **GCP** | `gcloud` / cloud.google.com/go | us-central1 | `gcp` | `GOOGLE_APPLICATION_CREDENTIALS` | GCE/GCS/CloudSQL/VPC/SecretMgr/GKE/CloudRun/CloudCDN |
| **Aliyun** | `aliyun` / aliyun-cli | cn-hangzhou | `alicloud` | `ALIBABA_CLOUD_ACCESS_KEY_ID` | ECS/OSS/RDS/VPC/KMS/SLB/FC/CDN |
| **Tencent** | `tccli` / tencentcloud-cli | ap-shanghai | `tencent-cloud` | `TENCENTCLOUD_SECRET_ID` | CVM/Lighthouse/CDB/COS/CKMS/SCF/CDN |
| **Baidu BCE** | `bcecmd` / bce-sdk-go | cn-bj | `baiducloud` | `BCE_ACCESS_KEY_ID` | BCC/BOS/RDS/VPC/KMS/BCC/CCC/CFC/CDN |

## 5. MCP server 工具清单（Phase 2 完整 12 工具 / 云）

| # | 工具命名 | 覆盖服务（每云换前缀） |
|---|---|---|
| 1 | `<prefix>_compute_list` | EC2/VM/GCE/ECS/CVM/BCC |
| 2 | `<prefix>_compute_describe` | 同上（按 ID 查详情）|
| 3 | `<prefix>_compute_start` | 同上 |
| 4 | `<prefix>_compute_stop` | 同上 |
| 5 | `<prefix>_storage_list_buckets` | S3/Blob/GCS/OSS/COS/BOS |
| 6 | `<prefix>_storage_get_object` | 同上（下载）|
| 7 | `<prefix>_storage_put_object` | 同上（上传）|
| 8 | `<prefix>_database_list` | RDS/SQL/CloudSQL/RDS/CDB/BCE-RDS |
| 9 | `<prefix>_network_list_vpcs` | VPC/VNet/VPC/VPC/VPC/VPC |
| 10 | `<prefix>_secrets_get` | SecretsMgr/KeyVault/SecretMgr/KMS/CKMS/KMS |
| 11 | `<prefix>_container_list` | EKS/AKS/GKE/Swarm/TKE/CCE |
| 12 | `<prefix>_cdn_list_distributions` | CloudFront/CDN/CloudCDN/CDN/CDN/CDN |

**Phase 1 起步只做前 4 个（compute_list/describe/start/stop）** 验证架构。

## 6. 凭证流

```go
// internal/mcp/sdk/creds.go (共享)
type Creds struct {
    AccessKeyID     string
    AccessKeySecret string
    SecurityToken   string  // STS (optional)
    Region          string
}

func LoadCreds(cloud string) (*Creds, error) {
    // 优先级 1: macOS Keychain
    if hasKeychain(cloud) {
        return loadFromKeychain(cloud)
    }
    // 优先级 2: 环境变量
    if hasEnvVars(cloud) {
        return loadFromEnv(cloud)
    }
    // 优先级 3: 各云自家 CLI 配置
    // AWS: ~/.aws/credentials
    // Azure: ~/.azure/
    // GCP: gcloud auth application-default login
    // Aliyun: ~/.aliyun/config.json
    // Tencent: ~/.tencentcloud/credentials
    // Baidu: ~/.bce/config
    return loadFromCloudCLI(cloud)
}
```

每个云独立 service 名：
- `aws`, `azure`, `gcp`, `alicloud`, `tencent-cloud`, `baiducloud`

## 7. 文件结构

```
~/.claude/skills/                          # SKILL.md 入口层
├── tencent-cloud/                         # 已有，升级指向 MCP
├── alicloud/                              # clone 184 sub-skill + 新 meta SKILL
│   ├── SKILL.md (meta 入口, 指向 MCP)
│   ├── scripts/                           # 旧 bash fallback
│   ├── references/
│   └── skills/                            # cinience 184 sub-skill
├── google-cloud/                          # clone 99 sub-skill + 新 meta SKILL
│   └── (同上)
├── aws/                                   # 新建
│   ├── SKILL.md
│   ├── scripts/
│   └── references/
├── azure/                                 # 新建
└── baiducloud/                            # 新建

~/code/loom/                               # MCP server 源码
├── internal/mcp/
│   ├── sdk/                               # 共享: creds + error + tools
│   │   ├── creds.go
│   │   ├── tools.go
│   │   ├── errors.go
│   │   └── apidoc.go
│   ├── tencent/main.go                    # 6 个 Go daemon
│   ├── alicloud/main.go
│   ├── google/main.go
│   ├── aws/main.go
│   ├── azure/main.go
│   └── baidu/main.go
├── go.mod
└── cmd/build-mcp-servers.sh               # 一键编译 6 个二进制

~/bin/                                     # 编译产物
├── tencent-cloud-mcp
├── alicloud-mcp
├── google-cloud-mcp
├── aws-mcp
├── azure-mcp
└── baiducloud-mcp

~/.claude/mcp_servers.json                 # 注册 6 个 server
```

## 8. 实施分阶段

### Phase 1 (30 分钟) — 架构 + PoC

**目标**：验证 SKILL.md + MCP server 双形态能跑通

**产出**：
- `internal/mcp/sdk/creds.go` (100 行，凭证抽象)
- `internal/mcp/sdk/tools.go` (50 行，工具注册)
- `internal/mcp/tencent/main.go` (300 行，2 工具：cvm_list + lighthouse_list)
- 编译 → `~/bin/tencent-cloud-mcp`
- 端到端测试：`echo '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tencent_cvm_list_instances","arguments":{}}}' | tencent-cloud-mcp`
- 更新 `~/.claude/skills/tencent-cloud/SKILL.md`，加 MCP 调用方式
- 写 `~/.claude/mcp_servers.json` 注册（stub 也行）

**VERDICT 验收**：
- `tencent-cloud-mcp --help` exit 0
- 上面 JSON-RPC 测试返回 InstanceSet
- `bash -n` 旧 bash 脚本仍然 OK
- 内存中找得到 `internal/mcp/sdk/creds.go`

### Phase 2 (1-2 小时) — 6 个 server 全实现

**目标**：6 云 × 4 核心工具 = 24 工具全跑通

**产出**：
- 6 个 Go daemon，每个 ~500-800 行
- 共享 internal/mcp/sdk
- 端到端测试 6 个云的 list 类工具（空账户也返回 TotalCount: 0）
- 24 个工具的 JSON schema 文件

**VERDICT 验收**：
- 6 个 daemon 都能 `--help` exit 0
- 每个 daemon 都能 list 返回结构化数据
- 6 个 SKILL.md 都更新指向 MCP

### Phase 3 (持续) — 自动化 + Loom 集成

**目标**：从 SDK 仓库自动同步新 API，跟 Loom v2.0 对接

**产出**：
- `cmd/build-mcp-servers.sh` 一键编译
- `internal/mcp/apidoc-sync` 工具（从 SDK 仓库拉新 API 定义）
- 跟 Loom v2.0 spec §3.6 Cloud Provider Adapter 集成
- 完整 12 工具/云 = 72 工具

## 9. API 文档读取策略

每个云按以下顺序抓 API：

| 云 | 官方 spec 来源 | 抓取方式 |
|---|---|---|
| AWS | github.com/aws/aws-sdk-go-v2/tree/main/models/apis | Go struct tag 反射 |
| Azure | github.com/Azure/azure-rest-api-specs/specification | OpenAPI/Swagger 解析 |
| GCP | github.com/googleapis/googleapis | protobuf 解析 |
| Aliyun | github.com/aliyun/aliyun-openapi-snapshot | OpenAPI 解析 |
| Tencent | github.com/tencentcloud/tencentcloud-cli (含 spec) | OpenAPI 解析 |
| Baidu BCE | github.com/baidubce/bce-sdk-go (含 OpenAPI) | Go struct tag 反射 |

**统一流程**：
1. 抓 SDK 仓库 → 提取 `models/apis/<service>/<version>/api.json`
2. 生成 MCP tool 定义（input/output JSON schema）
3. 工具名规范：`<cloud_short>_<service>_<action>`，例 `aws_ec2_list_instances`
4. 用官方 SDK 调 API，错误码映射到 MCP error code

## 10. 跟 Loom v2.0 spec 关系

v2.0 spec §3.6 Cloud Provider Adapter 草案：

```go
// internal/cloud/adapter.go (Loom v2.0 §3.6)
type CloudProvider interface {
    ListCompute(ctx context.Context) ([]Instance, error)
    StartCompute(ctx context.Context, id string) error
    StopCompute(ctx context.Context, id string) error
    ListStorage(ctx context.Context) ([]Bucket, error)
    GetSecret(ctx context.Context, name string) (string, error)
}

// 6 个实现
type AWSProvider struct { ... }      // 包装 aws-sdk-go-v2
type AzureProvider struct { ... }    // 包装 azure-sdk-for-go
type GCPProvider struct { ... }      // 包装 cloud.google.com/go
type AliyunProvider struct { ... }   // 包装 aliyun-cli
type TencentProvider struct { ... }  // 包装 tencentcloud-cli
type BaiduProvider struct { ... }    // 包装 bce-sdk-go
```

**MCP server 跟 CloudProvider 关系**：
- MCP server 是 CloudProvider 的**薄包装**
- CloudProvider 内部调 SDK，MCP server 内部调 CloudProvider
- 同一个 SDK 调用，可被两个接口（gRPC + MCP）使用

## 11. 风险与对策

| 风险 | 影响 | 对策 |
|---|---|---|
| **凭证泄露** | 严重 | 全 Keychain, 不进 env / git / 日志 |
| **tool schema 漂移** | 中 | 用 Go struct tag + 反射, 编译期类型检查 |
| **MCP client 不兼容** | 中 | 用 mcp-go 官方 SDK, 跟 Claude/Cursor/Codex 同步 |
| **API 配额超限** | 低 | 工具内自动 retry + rate limit, 跟 Loom Token 桶集成 |
| **沙箱逃逸** | 高 | 危险操作（destroy/terminate）必须 `--force` 二次确认 + dry-run 优先 |
| **多账号冲突** | 中 | Keychain service + account 区分, 工具接受 account 参数 |

## 12. 不做什么 (YAGNI)

- ❌ 不实现 Web UI（用 SKILL.md + MCP 就够）
- ❌ 不支持 Custom Cloud Provider plugin（先 6 云固定）
- ❌ 不实现跨云联邦查询（先单云内功能完整）
- ❌ 不做完整 Terraform 兼容（只读 + 写子集，先别管 import）
- ❌ 不做 SSO/OAuth（用现成 Keychain 模式）

## 13. 验收标准

**Phase 1 PoC 通过**：
1. `go build` exit 0, 产出 `tencent-cloud-mcp` 二进制
2. `~/bin/tencent-cloud-mcp` exit 0 with --help
3. JSON-RPC 调用返回 `cvm_list` 的真实数据
4. `~/.claude/skills/tencent-cloud/SKILL.md` 更新指向 MCP
5. `~/.claude/mcp_servers.json` 注册
6. 旧 bash 脚本仍然可用（fallback）
7. **deliverable.md 末尾有 `VERDICT: PASS`**

**Phase 2 完整通过**：
1. 6 个 daemon 都编译 + 启动 + JSON-RPC 调用 OK
2. 24 个工具的 schema 完整
3. 6 个 SKILL.md 都更新
4. `bash -n` 6 个云的 bash 脚本都过（fallback 仍可用）
5. **deliverable.md 末尾有 `VERDICT: PASS`**

## 14. 参考资料

- Anthropic Skills 协议：https://docs.claude.com/en/docs/agents-and-tools/agent-skills
- Anthropic MCP 协议：https://modelcontextprotocol.io/
- mcp-go SDK：https://github.com/mark3labs/mcp-go
- 6 大云官方 SDK 仓库（见 §9）
- Loom v2.0 spec：`.loom-drafts/v2.0-spec.md` §3.6

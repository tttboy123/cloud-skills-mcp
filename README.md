# cloud-skills-mcp

> **6 大云厂商的 SKILL.md + MCP Server 双形态管理接口**
> Google Cloud · Azure · AWS · Alibaba Cloud · Tencent Cloud · Baidu BCE

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Cloud Providers: 6](https://img.shields.io/badge/Cloud_Providers-6-blue)](#supported-clouds)
[![Format: SKILL.md + MCP](https://img.shields.io/badge/Format-SKILL.md_+_MCP-green)](#architecture)

## 解决什么问题

AI Agent (Claude / Cursor / Codex / 自家 daemon) 想"重启那台 web-prod-01"、"查 OSS 桶"、"备份数据库" — 需要一个**统一入口**调用 6 大云厂商的 API。

不同云厂商：
- CLI 都不一样 (`aws` / `az` / `gcloud` / `aliyun` / `tccli` / `bcecmd`)
- 鉴权都不一样 (AccessKey / OAuth / IAM / RAM / CAM / AK/SK)
- API endpoint 全独立 (不能混用)

**本仓库** = 标准化这两层：
- **SKILL.md** 入口（自然语言触发）
- **MCP Server** 工具（原子 API 暴露）

## 架构 (双形态)

```
┌────────────────────────────────────────────────────────────┐
│  User: "帮我重启那台 web-prod-01"                          │
└────────────┬───────────────────────────────────────────────┘
             │
             ▼
┌────────────────────────────────────────────────────────────┐
│  Claude / Cursor / Codex / Loom daemon                     │
│  1. SKILL.md 触发 (加载 ./tencent-cloud/SKILL.md)          │
│  2. 看到 "调 tencent_cvm_* 工具"                            │
│  3. 通过 mcp_servers.json 找 tencent-cloud-mcp             │
└────────────┬───────────────────────────────────────────────┘
             │ JSON-RPC over stdio / SSE
             ▼
┌────────────────────────────────────────────────────────────┐
│  tencent-cloud-mcp (Go binary)                              │
│  Tools: tencent_cvm_list / describe / start / stop         │
│  ↕ tccli / tencentcloud-sdk-go                             │
└────────────┬───────────────────────────────────────────────┘
             │ HTTPS + TC3-HMAC-SHA256
             ▼
┌────────────────────────────────────────────────────────────┐
│  Tencent Cloud API (cvm.tencentcloudapi.com)                │
└────────────────────────────────────────────────────────────┘
```

详细架构见 [`docs/cloud-skills-mcp-design.md`](docs/cloud-skills-mcp-design.md)

## Supported Clouds

| 云 | CLI | 默认 region | 凭证 Keychain service | env var | 状态 |
|---|---|---|---|---|---|
| **Tencent Cloud** | `tccli` | ap-shanghai | `tencent-cloud` | `TENCENTCLOUD_SECRET_ID` | ✅ Phase 1 |
| **Alibaba Cloud** | `aliyun` | cn-hangzhou | `alicloud` | `ALIBABACLOUD_ACCESS_KEY_ID` | ✅ Meta SKILL (184 sub-skill 来自 [cinience/alicloud-skills](https://github.com/cinience/alicloud-skills)) |
| **Google Cloud** | `gcloud` | us-central1 | `gcp` | `GOOGLE_APPLICATION_CREDENTIALS` | ✅ Meta SKILL (99 sub-skill 来自 [google/skills](https://github.com/google/skills)) |
| **AWS** | `aws` | us-east-1 | `aws` | `AWS_ACCESS_KEY_ID` | ⏳ Phase 2 |
| **Azure** | `az` | eastus | `azure` | `AZURE_SUBSCRIPTION_ID` | ⏳ Phase 2 |
| **Baidu BCE** | `bcecmd` | cn-bj | `baiducloud` | `BCE_ACCESS_KEY_ID` | ⏳ Phase 2 |

## 快速开始

### 安装

```bash
# 1. Clone 仓库
git clone https://github.com/tttboy123/cloud-skills-mcp.git
cd cloud-skills-mcp

# 2. 安装 6 个 SKILL.md 到 ~/.claude/skills/
./install.sh  # 待写 (Phase 1 完成后)

# 3. 编译 6 个 MCP server
go build -o ~/bin/ ./cmd/<cloud>-mcp/...
```

### 配置凭证

```bash
# Tencent Cloud
bash tencent-cloud/scripts/setup-keychain.sh
# 等价: security add-generic-password -s tencent-cloud -a tccli-secretid -w <AKID>

# AWS
bash aws/scripts/setup-keychain.sh  # 待写

# Azure
bash azure/scripts/setup-keychain.sh  # 待写

# GCP
bash google-cloud/scripts/setup-keychain.sh  # 待写

# Aliyun
bash alicloud/scripts/setup-keychain.sh  # 待写

# Baidu BCE
bash baiducloud/scripts/setup-keychain.sh  # 待写
```

### 注册 MCP server

写 `~/.claude/mcp_servers.json`:

```json
{
  "mcpServers": {
    "tencent-cloud": { "command": "/Users/lune/bin/tencent-cloud-mcp" },
    "aws": { "command": "/Users/lune/bin/aws-mcp" },
    "azure": { "command": "/Users/lune/bin/azure-mcp" },
    "google-cloud": { "command": "/Users/lune/bin/google-cloud-mcp" },
    "alicloud": { "command": "/Users/lune/bin/alicloud-mcp" },
    "baiducloud": { "command": "/Users/lune/bin/baiducloud-mcp" }
  }
}
```

### 测试

```bash
# 调 MCP server
echo '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' | ~/bin/tencent-cloud-mcp

# 调 bash fallback
bash tencent-cloud/scripts/cvm.sh list
```

## 文件结构

```
cloud-skills-mcp/
├── README.md                           # 本文件
├── LICENSE                             # MIT
├── .gitignore
├── docs/
│   ├── cloud-skills-mcp-design.md      # 完整架构设计
│   └── cloud-mcp-poc-deliverable.md    # Phase 1 PoC 验收报告
├── tencent-cloud/                      # 腾讯云
│   ├── SKILL.md                        # meta 入口
│   ├── scripts/                        # bash fallback
│   │   ├── _creds.sh
│   │   ├── setup-keychain.sh
│   │   ├── cvm.sh                      # 云服务器
│   │   ├── lighthouse.sh               # 轻量应用服务器
│   │   ├── cdb.sh                      # 云数据库
│   │   ├── cos.sh                      # 对象存储
│   │   └── cloudbase.sh                # Serverless
│   └── references/
├── alicloud/                           # 阿里云 (meta 入口)
│   ├── SKILL.md
│   └── README.md                       # 指向 cinience/alicloud-skills
├── google-cloud/                       # GCP (meta 入口)
│   ├── SKILL.md
│   └── README.md                       # 指向 google/skills
├── aws/                                # AWS (待写)
├── azure/                              # Azure (待写)
├── baiducloud/                         # Baidu BCE (待写)
├── cmd/                                # MCP server 编译入口
│   ├── tencent-cloud-mcp/
│   ├── alicloud-mcp/
│   ├── google-cloud-mcp/
│   ├── aws-mcp/
│   ├── azure-mcp/
│   └── baiducloud-mcp/
├── internal/                           # MCP server 共享代码
│   └── mcp/
│       ├── sdk/                        # 凭证 / 工具注册 / 错误处理
│       │   ├── creds.go
│       │   ├── tools.go
│       │   ├── errors.go
│       │   └── apidoc.go
│       ├── tencent/main.go
│       ├── alicloud/main.go
│       ├── google/main.go
│       ├── aws/main.go
│       ├── azure/main.go
│       └── baidu/main.go
├── go.mod
├── go.sum
└── build-mcp-servers.sh                # 一键编译 6 个 daemon
```

## 路线图

- [x] **Phase 1 (PoC)**: 1 个云 (Tencent) + 4 工具 + 共享 SDK
- [ ] **Phase 2**: 6 云 × 4 核心工具 = 24 工具
- [ ] **Phase 3**: 6 云 × 12 工具 = 72 工具 + Loom v2.0 集成
- [ ] **Phase 4**: 自动从云 SDK 仓库同步新 API

## 致谢

- **MCP 协议**: [modelcontextprotocol.io](https://modelcontextprotocol.io/)
- **Anthropic Skills 协议**: [anthropic/skills](https://github.com/anthropics/skills)
- **Google Agent Skills**: [google/skills](https://github.com/google/skills) (15.4K stars)
- **Alibaba Cloud Skills**: [cinience/alicloud-skills](https://github.com/cinience/alicloud-skills) (184 sub-skill)
- **mcp-go SDK**: [mark3labs/mcp-go](https://github.com/mark3labs/mcp-go)

## License

MIT © 2026 lune (tttboy123)

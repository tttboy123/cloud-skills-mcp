---
name: alicloud
description: 通过 aliyun CLI / Python SDK 管理阿里云 (Alibaba Cloud) 资源。当用户提到"阿里云/aliyun/ecs/oss/rds/vpc/查询/开机/部署/aliyun-cli" 时触发。本仓库是 meta 入口, 详细 sub-skill 在 cinience/alicloud-skills 仓库 (184 个细粒度 skill)。
allowed-tools: Bash
license: MIT
---

# Alibaba Cloud (阿里云) Manager — Meta 入口

本目录是 `cloud-skills-mcp` 仓库的 alicloud **meta 入口**。完整的 184 个细粒度 sub-skill 来自 [`cinience/alicloud-skills`](https://github.com/cinience/alicloud-skills) (Apache 2.0)。

## 快速使用

### 方式 1: 用本仓库自带的 meta SKILL.md (推荐新手)

```bash
# 1. 装 aliyun CLI (一次性)
brew install aliyun-cli  # 或 pip install aliyun-cli

# 2. 配 AccessKey
aliyun configure

# 3. 装本 meta skill 到 ~/.claude/skills/
cp -r ~/code/cloud-skills-mcp/alicloud ~/.claude/skills/

# 4. 调通 (Phase 2 后才有 MCP server, Phase 1 只能用 bash)
# 详见 https://github.com/cinience/alicloud-skills
```

### 方式 2: 直接用 cinience 完整 184 sub-skill (推荐老手)

```bash
# 一键装全部 184 个 sub-skill
npx skills add cinience/alicloud-skills --all -y --force
```

## 本目录范围

| 路径 | 内容 | 状态 |
|---|---|---|
| `SKILL.md` | 本 meta 入口 | ✅ 完整 |
| `scripts/` | 5-6 个核心产品 bash fallback (ecs/oss/rds/vpc/kms 等) | ⏳ Phase 2 |
| `references/` | aliyun CLI 速查 | ⏳ Phase 2 |
| MCP server | `alicloud-mcp` Go daemon | ⏳ Phase 2 |

## 184 个 Sub-Skill (来自 cinience)

分类速查：
- **compute/** — ECS / FC (函数计算) / SWAS
- **storage/** — OSS (对象存储)
- **database/** — RDS / AnalyticDB
- **network/** — VPC / ALB / CDN / DNS / ESA
- **security/** — KMS / Cloud Firewall / SAS / 身份 / 内容安全
- **ai/** (56 个) — Model Studio (Qwen / Wan / CosyVoice / DashVector / DashScope)
- **observability/** — SLS (日志) / PTS (压测)
- **platform/** — CLI / OpenAPI / DevOps / 文档评审
- **media/** — ICE / Live / MPS / VOD (智能媒体)
- **backup/** — BDRC / HBR (备份容灾)
- **solutions/** — 解决方案

## 凭证 (跟 tencent-cloud 同 Keychain 模式)

```bash
# macOS Keychain (推荐)
security add-generic-password -s alicloud -a accesskey-id -w <AK>
security add-generic-password -s alicloud -a accesskey-secret -w <SK>
security add-generic-password -s alicloud -a region -w cn-hangzhou

# 或环境变量 (临时)
export ALIBABACLOUD_ACCESS_KEY_ID=...
export ALIBABACLOUD_ACCESS_KEY_SECRET=...
export ALIBABACLOUD_REGION=cn-hangzhou
```

## 跟其他云对比

| 维度 | alicloud | tencent-cloud | google-cloud |
|---|---|---|---|
| CLI | `aliyun` | `tccli` | `gcloud` |
| 默认 region | cn-hangzhou | ap-shanghai | us-central1 |
| env var | `ALIBABACLOUD_*` | `TENCENTCLOUD_*` (带下划线) | `GOOGLE_APPLICATION_CREDENTIALS` |
| 签名 | Alibaba Cloud RPC v3 | TC3-HMAC-SHA256 | OAuth 2.0 |
| 社区 sub-skill 数 | **184** (cinience) | 0 (本仓库自建) | **99** (google/skills) |

## 致谢

- Sub-skill 来自 [`cinience/alicloud-skills`](https://github.com/cinience/alicloud-skills) (Apache 2.0) — 184 个细粒度 skill
- 跟 tencent-cloud / google-cloud / aws / azure / baiducloud 共享 SKILL.md + MCP 双形态架构

---
name: google-cloud
description: 通过 gcloud CLI / 官方 SDK 管理 Google Cloud Platform (GCP) 资源。当用户提到"GCP/google cloud/gce/gcs/cloudsql/gke/查询/开机/部署/gcloud" 时触发。本仓库是 meta 入口, 详细 sub-skill 在 google/skills 仓库 (99 个细粒度 skill, Apache 2.0, 15.4K stars)。
allowed-tools: Bash
license: MIT
---

# Google Cloud (GCP) Manager — Meta 入口

本目录是 `cloud-skills-mcp` 仓库的 google-cloud **meta 入口**。完整的 99 个细粒度 sub-skill 来自 [`google/skills`](https://github.com/google/skills) (Apache 2.0, 15.4K stars, 1.2K forks) — Google 官方。

## 快速使用

### 方式 1: 用本仓库自带的 meta SKILL.md (推荐新手)

```bash
# 1. 装 gcloud CLI
brew install --cask google-cloud-sdk  # macOS

# 2. 登录
gcloud auth login
gcloud auth application-default login  # 给 SDK 用

# 3. 装本 meta skill 到 ~/.claude/skills/
cp -r ~/code/cloud-skills-mcp/google-cloud ~/.claude/skills/

# 4. 调通 (Phase 2 后才有 MCP server)
```

### 方式 2: 用 google/skills 完整 99 sub-skill (推荐老手)

```bash
# 复制 google/skills 仓库到 skills 目录
git clone --depth 1 https://github.com/google/skills.git ~/.claude/skills/google-skills-raw
# 然后在 Claude Code 里用 /plugin marketplace add google/skills
```

## 本目录范围

| 路径 | 内容 | 状态 |
|---|---|---|
| `SKILL.md` | 本 meta 入口 | ✅ 完整 |
| `scripts/` | GCE / GCS / CloudSQL / VPC bash fallback | ⏳ Phase 2 |
| `references/` | gcloud CLI 速查 | ⏳ Phase 2 |
| MCP server | `google-cloud-mcp` Go daemon | ⏳ Phase 2 |

## 99 个 Sub-Skill (来自 google/skills)

**核心可用 (cloud 类)**:
- `gcloud` (14.5K SKILL.md) — gcloud CLI 通用, 涵盖所有 GCP 服务
- `gke-cluster-autoscaler` — GKE 节点自动扩缩
- `gke-observability` / `gke-upgrades` / `gke-inference` — GKE 运维
- `cloud-sql-basics` / `spanner-basics` / `bigquery-basics` / `bigtable-basics` — 数据库
- `cloud-run-basics` — Cloud Run (Serverless)
- `cloud-logging-*` / `cloud-monitoring-*` — 可观测性
- `firebase-basics` / `alloydb-basics` — 其他服务
- 详细列表见 https://github.com/google/skills/tree/main/skills/cloud

**ads 类** (Google Ads 营销 API, 12 个) 和 **analytics 类** (Google Analytics) 也可用。

## 凭证 (3 种方式)

```bash
# 方式 1: 用户 OAuth (推荐, 适合个人开发)
gcloud auth login
gcloud auth application-default login

# 方式 2: Service Account JSON (推荐, 适合 CI/CD)
gcloud iam service-accounts keys create key.json --iam-account=sa@project.iam.gserviceaccount.com
export GOOGLE_APPLICATION_CREDENTIALS=/path/to/key.json

# 方式 3: macOS Keychain (本仓库统一)
security add-generic-password -s gcp -a service-account-json -w "$(cat key.json)"
```

## 跟其他云对比

| 维度 | google-cloud | alicloud | tencent-cloud |
|---|---|---|---|
| CLI | `gcloud` | `aliyun` | `tccli` |
| 默认 region | us-central1 | cn-hangzhou | ap-shanghai |
| 鉴权 | OAuth 2.0 + Service Account | RAM + AccessKey | CAM + SecretId/SecretKey |
| 签名 | Bearer Token (OAuth) | Alibaba Cloud RPC v3 | TC3-HMAC-SHA256 |
| 社区 sub-skill 数 | **99** (google/skills, 官方) | 184 (cinience) | 0 (本仓库自建) |

## 致谢

- Sub-skill 来自 [`google/skills`](https://github.com/google/skills) (Apache 2.0) — Google 官方
- `gcloud` 通用 skill 14.5K, 是 GCP 操作的事实标准

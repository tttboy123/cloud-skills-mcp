# google-cloud — Google Cloud Platform (GCP) Manager

**本目录是 `cloud-skills-mcp` 仓库的 google-cloud meta 入口**。

完整的 99 个细粒度 sub-skill 来自:
- [`google/skills`](https://github.com/google/skills) (Apache 2.0, 15.4K stars, 1.2K forks) — Google 官方

## 安装

### 方式 1: 单独使用本 meta skill

```bash
cp -r ~/code/cloud-skills-mcp/google-cloud ~/.claude/skills/
```

### 方式 2: 用 google/skills 完整 99 sub-skill (推荐)

```bash
# 直接 clone
git clone --depth 1 https://github.com/google/skills.git ~/.claude/skills/google-skills-raw
```

或在 Claude Code 里:
```
/plugin marketplace add google/skills
```

## 凭证配置

```bash
# gcloud CLI
gcloud auth login
gcloud auth application-default login

# 或 Service Account
export GOOGLE_APPLICATION_CREDENTIALS=/path/to/key.json

# 或 macOS Keychain
security add-generic-password -s gcp -a service-account-json -w "$(cat key.json)"
```

## 状态

- ✅ Meta SKILL.md
- ⏳ 自建核心 bash 脚本 (Phase 2)
- ⏳ MCP server `google-cloud-mcp` (Phase 2)

## 致谢

- [`google/skills`](https://github.com/google/skills) (Apache 2.0) — 99 sub-skill

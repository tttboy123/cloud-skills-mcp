# alicloud — Alibaba Cloud Manager

**本目录是 `cloud-skills-mcp` 仓库的 alicloud meta 入口**。

完整的 184 个细粒度 sub-skill 来自:
- [`cinience/alicloud-skills`](https://github.com/cinience/alicloud-skills) (Apache 2.0) — ECS / OSS / RDS / VPC / KMS / FC / Model Studio ...

## 安装

### 方式 1: 单独使用本 meta skill

```bash
cp -r ~/code/cloud-skills-mcp/alicloud ~/.claude/skills/
```

### 方式 2: 一键装全部 184 sub-skill (推荐)

```bash
npx skills add cinience/alicloud-skills --all -y --force
```

## 凭证配置

```bash
# aliyun CLI
aliyun configure

# 或 macOS Keychain
security add-generic-password -s alicloud -a accesskey-id -w <AK>
security add-generic-password -s alicloud -a accesskey-secret -w <SK>
security add-generic-password -s alicloud -a region -w cn-hangzhou
```

## 状态

- ✅ Meta SKILL.md
- ⏳ 自建核心 bash 脚本 (Phase 2)
- ⏳ MCP server `alicloud-mcp` (Phase 2)

## 致谢

- [`cinience/alicloud-skills`](https://github.com/cinience/alicloud-skills) (Apache 2.0) — 184 sub-skill

# Agentic 快速使用指南

cloud-skills-mcp 以「六套 Skill + 19 个 MCP 工具」供 AI Agent（Codex、Claude
Code、Cursor 等）直接使用。本文给出每种场景的复制即用提示词、固定工作流和安全
规则，让 Agent 在 1 分钟内进入受控的只读/写门流程。

## 1. 一次性配置（用户侧）

```bash
git clone https://github.com/tttboy123/cloud-skills-mcp.git
cd cloud-skills-mcp
./install.sh --bin-dir "$HOME/.local/bin" --skills-dir "$HOME/.codex/skills"
codex mcp add cloud-skills -- "$HOME/.local/bin/cloud-skills-mcp"
```

或将六套 Skill 作为插件安装（可选）：

```bash
codex plugin marketplace add https://github.com/tttboy123/cloud-skills-mcp.git
codex plugin add cloud-skills-mcp@cloud-skills-mcp
```

凭证只注入 server 进程环境（见仓库 README `## 凭证入口`），绝不出现在提示词或
MCP 工具参数里。

## 2. 给 Agent 的通用启动提示

首次会话建议使用下面这条「探针」提示，Agent 会先确认连接、再进入对应 Skill：

```text
你是六云运维助手。先调用 cloud_provider_status 查看六个云提供商的 adapter 状态，
然后读取 <provider> 技能（SKILL.md）的 Workflow 和 MCP 参数说明，再按它执行我的请求。
全程遵守：只读优先、不碰凭证、写操作必须先问我并带 force=true、大响应写 response_file。
```

把 `<provider>` 换成 `aws`/`azure`/`gcp`/`alicloud`/`tencent`/`baiducloud`。

## 3. 场景化提示词

**只读巡检（推荐首个场景）**

```text
用 aws 技能只读巡检：cloud_provider_status 后，按 aws/SKILL.md 列出 us-east-1 的 EC2
实例（DescribeInstances），只调用 aws_api_read，不写任何资源。
```

**对象/文件读取**

```text
用 azure 技能读取 <blob-url> 的前 1 MiB 到 <approved-root>/download.bin，使用
response_file，不把内容打进对话。
```

**跨云对比**

```text
分别用 gcp 与 alicloud 技能只读列出我的项目/实例清单，对比可用区分布；不要调用 mutate。
```

**Registry 只读**

```text
用 tencent 技能（tcr-registry）只读列出 <instance> 的 tag；不要把临时凭证写进输出。
```

## 4. Agent 必须遵守的规则（写进 SKILL.md 和提示词）

1. **只读优先**：默认只用 `<provider>_api_read`；`GET/HEAD/OPTIONS` 与官方只读
   action 之外的操作一律视为写。
2. **写操作三重门**：宿主已批准 + server 环境 `CLOUD_SKILLS_ALLOW_MUTATIONS=1` +
   单次调用带 `force=true`。秘密类操作还需 `CLOUD_SKILLS_ALLOW_SENSITIVE=1`。
3. **凭证隔离**：不生成、不导出、不读取凭证；access key/token/password 出现在
   URL、query、header、body 或 audit 输出即拒绝。
4. **大响应走文件**：超过对话可接受量级用 `response_file`，正文不进模型上下文。
5. **失败即停**：出现认证失败、端点拒绝或异常输出时停止并报告，不重试暴力猜测。

## 5. Live 验收（需要真实凭证）

Agent 或用户注入凭证并启用 gate 后，统一入口：

```bash
CLOUD_SKILLS_LIVE_TEST=1 scripts/ci/live-acceptance.sh
```

每个 gate 的必填环境变量与命令见
[goal-completion-matrix.md](goal-completion-matrix.md) 的 live gate 段落。

## 6. 排障

- `cloud_provider_status` 显示 `credential_status=unverified`：正常，凭证在首次
  真实调用时才解析。
- 工具报「credential/敏感操作」：按规则 2/3 检查是否误触写门或凭证字段。
- 端点报错：确认 URL 是官方域名（`*.amazonaws.com`/`*.azure.com`/`googleapis.com`
  等），自定义域名需 `CLOUD_SKILLS_<PROVIDER>_ALLOWED_ENDPOINT_HOSTS` 显式放行。
- 需要审计：设置 `CLOUD_SKILLS_AUDIT_LOG=/path/audit.jsonl`，日志不含请求体/响应/
  query/凭证。

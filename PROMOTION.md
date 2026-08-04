# 宣传与推广方案（Promotion Kit）

目标：让「cloud-skills-mcp：六云全资源 Skill + MCP」被 1) 用过 MCP 的开发者、
2) 云平台工程师、3) 关注 AI Agent 自动化的人快速理解并试用。

## 一句话定位

CN：**一个 MCP Server + 六套 Skill，让 AI Agent 用官方身份链安全操作 AWS、Azure、
GCP、阿里云、腾讯云、百度智能云的全部公开资源 API，凭证永不进 MCP。**

EN：**One MCP server + six skills for AI agents to operate AWS, Azure, GCP,
Alibaba, Tencent, and Baidu Cloud through official HTTPS/WSS APIs — with
credentials that never enter the MCP surface.**

## 核心卖点（对外可验证）

1. **六云一站**：统一 stdio server 覆盖 6 家云，72 个 `auth_scheme` 全部实现或
   文档化排除，`mapping-audit.sh` 机器可验证。
2. **凭证隔离**：工具参数永不接受/导出凭证；走各家官方身份链（AKSK/IAM/ADC/
   Entra/BCE），密钥只存在 server 进程内存，审计脱敏。
3. **安全默认**：默认只读；写操作三开门（宿主批准 + `ALLOW_MUTATIONS` +
   `force=true`）；敏感操作再加一重；不执行任何云 CLI。
4. **Agent 友好**：六套 SKILL.md 自带 Workflow/参数说明/示例，Agent 一句提示即可
   进入受控流程；也可作为 Codex 插件一键安装。
5. **工程化交付**：72-scheme 协议冒烟、race+覆盖率门禁、远程 CI 全绿、四平台
   release 归档、live gate 以可执行脚本交付。

## 目标用户

- 用 Codex/Claude/Cursor 管理多云资源的开发者
- 云平台/DevOps 工程师，想要“Agent 可审计地操作云”
- MCP 生态贡献者，想对比通用 REST 网关 vs 每云专用工具

## 渠道与文案草稿

### GitHub（仓库首页）

已完成：徽章（CI/License/Go/Release）、`## 快速开始`（用户 3 步 + Agentic 一句话）、
`docs/agentic-quickstart.md`、`PROMOTION.md`。建议再补：5 分钟演示录屏（见下）、
README 顶部一张架构图（server ↔ skills ↔ 六云官方 API）。

### Show HN / Hacker News

标题：**Show HN: cloud-skills-mcp — one MCP server + skills for all six major clouds**

正文要点（150–300 词）：痛点（多云 Agent 自动化要么写一堆工具要么裸调 REST 暴露凭证）→
方案（统一 Go stdio server + 6 套 Skill，72 个 auth_scheme，官方身份链）→ 安全
（凭证零进入、只读默认、写三开门、无云 CLI）→ 工程证据（race/覆盖/mapping audit/
CI 全绿，live gate 脚本）→ 30 秒上手命令。附仓库链接。

### Reddit

- r/selfhosted：标题 *One MCP server to operate AWS/Azure/GCP/Alibaba/Tencent/Baidu
  with credentials that never enter the model*。
- r/ClaudeAI 或 r/CodexCLI：标题 *Six-cloud Skills + MCP server for agentic cloud ops*。
- r/devops：标题 *We made agentic cloud ops auditable: read-only by default, three
  gates for writes*。

### X / Twitter（帖子串，3–5 条）

```text
1/ 一个 MCP server + 六套 Skill，让 Agent 安全操作六大云：
   AWS · Azure · GCP · 阿里云 · 腾讯云 · 百度智能云
2/ 凭证永不进 MCP：只走官方身份链，密钥仅存 server 进程内存，审计脱敏。
3/ 默认只读；写操作 = 宿主批准 + ALLOW_MUTATIONS + force=true 三重门。
4/ 72 个 auth_scheme 全部实现或文档化排除，mapping-audit 机器可验证；
   race/覆盖/协议冒烟/远程 CI 全绿。
5/ 30 秒上手：git clone && ./install.sh && codex mcp add cloud-skills -- ...
   https://github.com/tttboy123/cloud-skills-mcp
```

### dev.to / Medium / 掘金 / 知乎

长文标题（CN）：《让 AI Agent 安全操作六大云：一个 MCP Server + 六套 Skill 的
工程化实践》/（EN）*Building an auditable six-cloud MCP server for AI agents*。
结构：痛点 → 设计（工具模型、凭证隔离、安全门）→ 协议族覆盖与审计 → 可验证交付
（测试/CI/live gate）→ 快速开始 → 路线图。

## 5 分钟演示脚本

1. `git clone && ./install.sh --bin-dir ... --skills-dir ... && codex mcp add cloud-skills`
2. 第一条提示：`调用 cloud_provider_status 查看六云 adapter 状态` → 展示
   `available=true / credential_status=unverified`
3. 注入一家凭证（例如 AWS env），提示 `用 aws 技能只读列出 EC2` → 展示一次真实只读调用
4. 展示写门：不带 `force=true` 的 mutate 被拒；带 `force=true` 且批准后执行
5. 展示 audit：`CLOUD_SKILLS_AUDIT_LOG` 的 JSONL 只含 provider/outcome/bytes/request_id
6. 收尾：`scripts/ci/mapping-audit.sh` 输出 72 schemes + 六套 Skill 全绿

录屏建议：终端 + Codex 对话双窗口，60–90 秒，配字幕；放 README 顶部与发布帖。

## Release 公告模板（v0.4.0）

```text
cloud-skills-mcp v0.4.0 发布

新增
- Azure OpenAI Chat Completions / Responses 流式 SSE
- AWS Connect chat participant WebSocket（内部 SigV4+bearer 换令牌，可选 SendMessage）
- 六云剩余协议族审计：Kafka/RESP/数据库 wire/媒体面/设备面/退役服务全部映射
- 72-scheme mapping-audit + 协议冒烟 sweep
- live-acceptance.sh 统一验收入口
- Codex 插件打包（plugin/ + marketplace 清单）

资源
- 四平台 release 归档与 checksums：<release-url>
- 文档：README 快速开始 / agentic-quickstart / goal-completion-matrix
```

## 宣传资产清单

- [x] README 徽章与快速开始
- [x] Agentic 快速使用文档
- [ ] 5 分钟演示录屏（建议放在 README 顶部）
- [ ] 架构图（server ↔ skills ↔ 六云官方 API）
- [ ] v0.4.0 GitHub Release（dist 归档已就绪，需授权发布）
- [ ] 合入默认分支 main 后再对外转发（当前工作在 six-cloud-full-resource）

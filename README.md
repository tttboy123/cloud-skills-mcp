# cloud-skills-mcp

> 面向 AI Agent 的云管理 Skill + MCP 双形态项目。

当前可运行范围是 **Tencent Cloud Phase 1.5**：一个 Go stdio MCP server，提供
CVM list / describe / start / stop 四个工具。Google Cloud 与 Alibaba Cloud 目录
目前是上游 Skill 的 meta 入口；AWS、Azure、Baidu BCE 仍在路线图中。

## 当前能力

| 云 | Skill | MCP server | 状态 |
|---|---:|---:|---|
| Tencent Cloud | ✅ | ✅ 4 个 CVM 工具 | Phase 1.5，本地试用 |
| Alibaba Cloud | ✅ meta | — | 上游 `cinience/alicloud-skills` 入口 |
| Google Cloud | ✅ meta | — | 上游 `google/skills` 入口 |
| AWS | — | — | 计划中 |
| Azure | — | — | 计划中 |
| Baidu BCE | — | — | 计划中 |

这里的“双形态”是分层关系：

- `SKILL.md` 描述触发条件、操作流程与安全规则。
- MCP server 暴露参数化的原子工具并负责调用云 CLI。

当前 MCP transport 只有 JSON-RPC over stdio。

## 安全模型

Tencent MCP 默认是 read-only 模式：

- `list` / `describe` 可调用，MCP annotation 正确标为 read-only。
- `start` / `stop` 默认拒绝执行。
- 写操作需要服务器进程显式设置 `CLOUD_SKILLS_ALLOW_MUTATIONS=1`，并且每次请求
  仍须包含 `force=true`。
- `force=true` 不是人类批准证明。Codex、Claude、Cursor 或 Loom 等宿主仍必须在
  调用写工具前取得用户明确批准。
- 实例 ID、region 和分页 limit 会在启动 `tccli` 前校验。
- 凭证错误会做字段级 redaction；RequestId 等排障标识会保留。

建议使用最小权限 CAM 子账号，并将只读 MCP 与获批维护窗口分开运行。

## 安装

要求：Go 1.25.12+、`tccli`，以及 macOS（如果使用 Keychain 凭证）。仓库的
`toolchain` 指令使用已修复 `GO-2026-5856` 的 Go 1.26.5。

```bash
git clone https://github.com/tttboy123/cloud-skills-mcp.git
cd cloud-skills-mcp

# 只安装已实现的 Tencent MCP binary；不改任何客户端配置
./install.sh --bin-dir "$HOME/.local/bin"

# 可选：同时安装当前三个 skill/meta-skill
./install.sh \
  --bin-dir "$HOME/.local/bin" \
  --skills-dir "$HOME/.codex/skills"
```

安装器不会修改 MCP 配置、云资源或凭证。已有 Skill 目录默认不会覆盖；只有显式
传入 `--force` 才会替换。

也可以只在仓库内构建：

```bash
./build-mcp-servers.sh
# 输出: ./bin/tencent-cloud-mcp
```

## 配置腾讯云凭证

```bash
bash tencent-cloud/scripts/setup-keychain.sh
```

设置脚本会先用只读 `DescribeInstances` 验证输入，验证失败不会修改 Keychain。
MCP 的凭证优先级为：

1. macOS Keychain：service=`tencent-cloud`
2. `TENCENTCLOUD_SECRET_ID` / `TENCENTCLOUD_SECRET_KEY` 环境变量
3. `~/.tencentcloud/credentials`

## 注册 MCP server

### Codex CLI / ECC

```bash
codex mcp add tencent-cloud -- "$HOME/.local/bin/tencent-cloud-mcp"
```

保持 `CLOUD_SKILLS_ALLOW_MUTATIONS` 未设置，即为默认只读模式。不要把长期写权限环境
变量写进共享基线；需要维护时应在受控会话中临时启用，并保留宿主审批。

### Claude Desktop / 兼容客户端

```json
{
  "mcpServers": {
    "tencent-cloud": {
      "command": "/absolute/path/to/tencent-cloud-mcp",
      "env": {}
    }
  }
}
```

## 验证

```bash
go test -race ./...
go vet ./...
go build -trimpath -o /tmp/tencent-cloud-mcp ./cmd/tencent-cloud-mcp
/tmp/tencent-cloud-mcp --help
```

工具发现不需要云凭证：

```bash
echo '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' \
  | "$HOME/.local/bin/tencent-cloud-mcp"
```

Live 云调用不属于默认测试。只有显式设置 `CLOUD_SKILLS_LIVE_TEST=1` 时，测试才会读取
真实 Keychain。

## 目录

```text
cloud-skills-mcp/
├── cmd/tencent-cloud-mcp/       # 可执行程序入口
├── internal/mcp/sdk/            # 凭证、错误、工具公共代码
├── internal/mcp/tencent/        # Tencent MCP server 与测试
├── tencent-cloud/               # Tencent Skill + bash fallback
├── alicloud/                    # Alibaba Cloud meta-skill
├── google-cloud/                # Google Cloud meta-skill
├── aws/ azure/ baiducloud/      # 路线图占位
├── install.sh                   # 安全、可选路径安装器
├── build-mcp-servers.sh         # 构建当前已实现 server
└── docs/                        # 设计与阶段交付记录
```

## 路线图

- [x] Phase 1：Tencent CVM MCP PoC
- [x] Phase 1.5：构建入口、惰性凭证、默认只读门、输入校验、hermetic tests、CI
- [ ] Phase 2：逐云设计原生认证模型，再扩展核心 read-only 工具
- [ ] Phase 3：经过独立安全审查后增加更多写操作与 Loom adapter

不会在 Phase 2 直接复制统一 `AccessKeyID/AccessKeySecret` 抽象：GCP ADC、Azure
tenant/subscription 等认证模型需要分别设计。

## 上游与许可证

- 本仓库：MIT
- [`mark3labs/mcp-go`](https://github.com/mark3labs/mcp-go)：MIT
- [`google/skills`](https://github.com/google/skills)：Apache-2.0
- [`cinience/alicloud-skills`](https://github.com/cinience/alicloud-skills)：MIT

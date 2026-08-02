---
name: tencent-cloud
description: 通过 tccli 管理腾讯云资源, 覆盖 CVM (云服务器) / CloudBase (Serverless) / CDB (MySQL 数据库) / COS (对象存储)。当用户提到"开机/关服/查服务器/查数据库/查桶/部署/腾讯云/cvm/cdb/cos/cloudbase" 时触发, 通过 macOS Keychain 读 tccli 凭证。
allowed-tools: Bash
license: MIT
---

# Tencent Cloud Manager

通过 tccli (腾讯云官方 CLI) 管理腾讯云资源的统一入口。所有凭证从 macOS Keychain 读取, 不进 shell 历史、不进 git。

## 快速开始

```bash
# 1. 首次使用: 把 SecretId/SecretKey 存到 Keychain
bash ~/.codex/skills/tencent-cloud/scripts/setup-keychain.sh

# 2. 列你的服务器 (先试 Lighthouse, 0 台再试 CVM)
bash ~/.codex/skills/tencent-cloud/scripts/lighthouse.sh list
bash ~/.codex/skills/tencent-cloud/scripts/cvm.sh list

# 3. 开机/关机 (CVM 和 Lighthouse 用各自的脚本)
# 必须先取得用户对具体 instance/action 的明确批准
bash ~/.codex/skills/tencent-cloud/scripts/lighthouse.sh start <instance-id>
bash ~/.codex/skills/tencent-cloud/scripts/lighthouse.sh stop <instance-id>
bash ~/.codex/skills/tencent-cloud/scripts/cvm.sh start <instance-id>
bash ~/.codex/skills/tencent-cloud/scripts/cvm.sh stop <instance-id>

# 4. 查数据库
bash ~/.codex/skills/tencent-cloud/scripts/cdb.sh list

# 5. 查对象存储桶
bash ~/.codex/skills/tencent-cloud/scripts/cos.sh list

# 6. CloudBase 环境
bash ~/.codex/skills/tencent-cloud/scripts/cloudbase.sh envs
```

## 覆盖范围 (5 大场景)

| 脚本 | 服务 | 能力 | endpoint / API 版本 |
|---|---|---|---|
| `scripts/cvm.sh` | CVM (云服务器) | list / start / stop / reboot / describe / create (危险) | cvm.tencentcloudapi.com / 2017-03-12 |
| **`scripts/lighthouse.sh`** | **Lighthouse (轻量应用服务器)** | **list / start / stop / reboot / describe / firewall / packages** | **lighthouse.tencentcloudapi.com / 2020-03-24** |
| `scripts/cloudbase.sh` | CloudBase (Serverless) | envs / functions / databases / storage | tcb.tencentcloudapi.com |
| `scripts/cdb.sh` | CDB (云数据库 MySQL) | list / describe / start / stop / restart | cdb.tencentcloudapi.com |
| `scripts/cos.sh` | COS (对象存储) | list / put / get / delete (危险) | cos.<region>.myqcloud.com |

### ⚠️ CVM vs Lighthouse 别搞混

腾讯云有 2 套完全独立的"云服务器" API:
- **CVM** (云服务器) = 标准 ECS, 包年包月/按量, 灵活配置, 走 `tccli cvm` + `cvm.tencentcloudapi.com`
- **Lighthouse** (轻量应用服务器) = 套餐制 VPS, 一价全包, 走 `tccli lighthouse` + `lighthouse.tencentcloudapi.com`

**怎么判断用哪个**:
- `cvm.sh list` 返回 0 台 → 没 CVM, 试 `lighthouse.sh list`
- 控制台左上角 logo 是 "云服务器" → CVM; 写 "轻量应用服务器" → Lighthouse
- 套餐 ID 是 `bundle_*` → Lighthouse; `instance.*` → CVM

详细 tccli 命令速查见 [references/tccli-cheatsheet.md](references/tccli-cheatsheet.md)

## 凭证流 (macOS Keychain)

**存**: `setup-keychain.sh` 把 SecretId/SecretKey 存到 Keychain:
- service: `tencent-cloud`
- account: `tccli-secretid` / `tccli-secretkey` / `tccli-region`
- 用 `security find-generic-password` 读

**读**: 所有脚本统一通过 `scripts/_creds.sh` 读, export 为:
- `TENCENTCLOUD_SECRET_ID` (带下划线, **tccli 真正读的变量名**)
- `TENCENTCLOUD_SECRET_KEY` (带下划线, **tccli 真正读的变量名**)
- 兼容老的无下划线 `TENCENTCLOUD_SECRETID` / `TENCENTCLOUD_SECRETKEY` (有的话自动转换 + 警告)

**优点**:
- 凭证不暴露在 `ps aux` / shell history / git
- 跟 Loom macOS Keychain 习惯一致
- 一台机器多账号: 复制到不同 account 即可

**⚠️ 重要: env var 必须带下划线** — tccli 内部读的是 `TENCENTCLOUD_SECRET_ID`,
不是 `TENCENTCLOUD_SECRETID`。变量名错了会报 `secretId is invalid`,
但实际 AKSK 是有效的 (tccli 拿到空字符串去签名, 服务端拒认)。

## 安全约束 (Hard Rules)

1. **不删资源**: `cvm.sh destroy` / `cos.sh delete-bucket` / `cdb.sh drop` 默认 **拒绝执行**, 必须手动加 `--force` 二次确认
2. **不批量**: stop/reboot 一次最多 5 个 instance, 防止误操作全关
3. **dry-run 优先**: 任何 create/destroy 命令先 `--dry-run` 打印 plan
4. **凭证不写日志**: tccli 输出有 secret 时, 自动 redact
5. **宿主批准是权威边界**: `force=true` 不是用户批准证明。任何 start/stop/reboot/reset/destroy/put/delete 前必须取得用户对具体资源和动作的明确批准
6. **MCP 默认只读**: 写工具还要求 server 环境中有 `CLOUD_SKILLS_ALLOW_MUTATIONS=1`; 不要长期写入共享 MCP 基线

## 常见任务 (Recipes)

### "我有哪些服务器?"
```bash
# 先试 Lighthouse (轻量), 0 台再试 CVM (标准 ECS)
bash scripts/lighthouse.sh list
# 等价: tccli lighthouse DescribeInstances --region ap-shanghai
bash scripts/cvm.sh list
# 等价: tccli cvm DescribeInstances --region ap-shanghai
```

### "帮我重启那台 web-prod-01"
```bash
# 1. 查 ID (用 lighthouse 还是 cvm 看 list 结果)
bash scripts/lighthouse.sh list --filter web-prod
# 2. 重启
bash scripts/lighthouse.sh reboot <instance-id>
# 或 CVM:
bash scripts/cvm.sh reboot <instance-id>
```

### "我的数据库现在啥状态"
```bash
bash scripts/cdb.sh list
# 看 InstanceState: 0=创建中 1=运行中 4=隔离中 5=已删除
```

### "把 /tmp/log.txt 上传到 COS"
```bash
bash scripts/cos.sh put my-bucket /tmp/log.txt logs/2026-08-01.txt
```

### "CloudBase 上有哪些云函数"
```bash
bash scripts/cloudbase.sh functions
```

## Loom 集成 (v2.0+)

这个 skill 是 v1 (立即可用) 形态。v2 计划:
- 包装成独立 `tencent-cloud-mcp` daemon (mcp-go + tccli), 跟 Loom daemon 解耦
- 走 Loom 3-Token 设计, 凭证进 CubeEgress 类保险库
- 加 `internal/mcp/tencent-cloud/main.go`

设计见 `.loom-drafts/tencent-cloud-mcp-design.md` (待写)

## 错误处理

| 错误 | 原因 | 解决 |
|---|---|---|
| `secretId is invalid` | **env var 名错了** (用了 `SECRETID` 不是 `SECRET_ID`)/ AKSK 真错 | 检查 `TENCENTCLOUD_SECRET_ID` (带下划线!) / 重跑 `setup-keychain.sh` |
| `AuthFailure.SignatureFailure` | AKSK 对应的 SecretKey 错 | 重跑 `setup-keychain.sh`, 重新粘 SecretKey |
| `AuthFailure.InvalidAuthorization` | 系统时间不对/时区错 | `sudo sntp -sS time.apple.com` |
| `TencentCloudSDKError: AuthFailure` | AKSK 失效/欠费冻结 | https://console.cloud.tencent.com/expense 查账户 |
| `ResourceNotFound.Instance` | 实例 ID 错或已销毁 | `cvm.sh list` 重新查 |
| `UnauthorizedOperation` | 子账号没权限 | 腾讯云 CAM 控制台加权限 |
| `RequestLimitExceeded` | API 调用频率超限 | 加 `--interval 1` 减慢 |

## MCP 调用方式 (推荐, Phase 1 PoC)

本 skill 同时支持 **bash 脚本 (fallback)** 和 **MCP server (推荐)** 两种调用方式。
两者读同一份 macOS Keychain 凭证。MCP 默认禁用写操作；只有宿主进程显式设置
`CLOUD_SKILLS_ALLOW_MUTATIONS=1` 且单次调用传入 `force=true` 才会执行 `start` / `stop`。
这两个技术门都不能替代用户批准。

### 工具清单 (4 个 CVM)

| MCP 工具 | 作用 | 是否需要 force |
|---|---|---|
| `tencent_cvm_list_instances` | 列 CVM 实例 | 否 |
| `tencent_cvm_describe_instance` | 按 ID 查 CVM 详情 | 否 |
| `tencent_cvm_start_instance` | 开机 CVM | **是** |
| `tencent_cvm_stop_instance` | 关机 CVM | **是** |

### 注册到 Codex

```bash
codex mcp add tencent-cloud -- "$HOME/.local/bin/tencent-cloud-mcp"
```

默认不要给 server 配 `CLOUD_SKILLS_ALLOW_MUTATIONS`，此时只有 list/describe 可用。

### 其他兼容客户端

```json
{
  "mcpServers": {
    "tencent-cloud": {
      "command": "/Users/lune/bin/tencent-cloud-mcp",
      "env": {}
    }
  }
}
```

> 其他 5 个云 (aws / azure / gcp / alicloud / baiducloud) 在 Phase 2 注册。

### stdio JSON-RPC 调用样例

```bash
# 列工具
echo '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | /Users/lune/bin/tencent-cloud-mcp

# 列出 ap-shanghai region 的 CVM
echo '{"jsonrpc":"2.0","id":2,"method":"tools/call",
       "params":{"name":"tencent_cvm_list_instances",
                 "arguments":{"region":"ap-shanghai","limit":10}}}' \
  | /Users/lune/bin/tencent-cloud-mcp

# 启动 CVM (server 还必须由 operator 启用 mutations，且宿主已取得用户批准)
echo '{"jsonrpc":"2.0","id":3,"method":"tools/call",
       "params":{"name":"tencent_cvm_start_instance",
                 "arguments":{"instance_id":"ins-abc123def","force":true}}}' \
  | /Users/lune/bin/tencent-cloud-mcp
```

### 何时用 bash vs MCP

| 场景 | 推荐 |
|---|---|
| 一次性手动查 (单条命令) | bash (`lighthouse.sh list`) |
| Claude/Cursor 集成, 多步自动编排 | **MCP** (工具更细粒度, 错误处理一致) |
| 需要 `--filter` / 模糊匹配 | bash (`lighthouse.sh list --filter web`) |
| 危险操作需要二次确认 | **MCP** (force 守卫) |

### 凭证读取顺序

- MCP server 跟 bash 共用 macOS Keychain (service=`tencent-cloud`)
- env var 路径也兼容 (`TENCENTCLOUD_SECRET_ID` 带下划线)
- MCP 错误信息自动 redact AKSK, 不会泄漏到 LLM context

MCP server 源码: `cmd/tencent-cloud-mcp/main.go` + `internal/mcp/tencent/server.go`

更多见仓库根目录 `README.md`。

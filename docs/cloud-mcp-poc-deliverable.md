---
gate: cloud-mcp-phase1-poc
delivered_at: 2026-08-01
delivered_by: Mavis (coder agent)
status: complete
---

# Cloud MCP Phase 1 PoC — Deliverable

> Historical Phase 1 record. Current behavior is defined by
> [`six-cloud-full-resource-contract.md`](six-cloud-full-resource-contract.md).

## 1. 完成的工作

### 1.1 共享 SDK (`internal/mcp/sdk/`)

| 文件 | 行数 | 内容 |
|---|---|---|
| `creds.go` | 354 | `LoadCreds(cloud)` 公共 API,3-tier fallback (Keychain → env → CLI config),`Creds` 结构,`RedactSecret` 保护 |
| `creds_configs.go` | 214 | 6 个云的 `CloudConfig` 注册 + 6 个 tier-3 解析器 (AWS/Azure/GCP/Aliyun/Tencent/Baidu) |
| `tools.go` | 74 | `RegisterTool` 薄包装 + `RegionArg` / `ForceArg` / `DangerousAnnotation` helper |
| `errors.go` | 182 | `RequireForce` 守卫,`WrapError`,`RedactSecret` (30+ char token + JSON 字段名匹配),`CLIError` 结构 |
| `apidoc.go` | 90 | `APIDoc` 接口 + `FileCacheAPIDoc` 实现 (Phase 1 stub,Refresh 返回 "not implemented") |
| `creds_test.go` | 70 | 3 个 sanity test: LoadCreds_TencentKeychain / RedactSecret / RequireForce |
| **小计** | **984** | (远多于 goal prompt 估计的 ~200,因 6 套 CloudConfig 占大头) |

### 1.2 Tencent MCP server (`internal/mcp/tencent/`)

| 文件 | 行数 | 内容 |
|---|---|---|
| `main.go` | 380 | stdio MCP server,4 个 CVM 工具 (list/describe/start/stop),`runTccli` 子进程包装 |

### 1.3 SKILL.md 更新 + MCP 注册

| 文件 | 改动 |
|---|---|
| `~/.claude/skills/tencent-cloud/SKILL.md` | 在"错误处理"章节后新增"MCP 调用方式"完整章节 (78 行,含工具清单/JSON-RPC 样例/凭证复用说明) |
| `~/.claude/mcp_servers.json` | 新建文件,注册 `tencent-cloud` MCP server 指向 `/Users/lune/bin/tencent-cloud-mcp` |

## 2. 编译/测试输出 (实际跑出来的)

### 2.1 `go build`

```
$ go build -o /Users/lune/bin/tencent-cloud-mcp ./internal/mcp/tencent/
exit: 0
-rwxr-xr-x  1 lune  staff  8551378 Aug  1 20:14 /Users/lune/bin/tencent-cloud-mcp
```

二进制大小 8.2 MB (含 mcp-go + 6 套 creds 解析器)。

### 2.2 `go test`

```
$ go test -timeout 30s -v ./internal/mcp/sdk/
=== RUN   TestLoadCreds_TencentKeychain
    creds_test.go:25: got creds: source=keychain region=ap-shanghai akid-prefix=AKID*** (redacted)
--- PASS: TestLoadCreds_TencentKeychain (0.07s)
=== RUN   TestRedactSecret
--- PASS: TestRedactSecret (0.00s)
=== RUN   TestRequireForce
--- PASS: TestRequireForce (0.00s)
PASS
ok  	loom-pi-rebuild/internal/mcp/sdk	0.461s
```

`TestLoadCreds_TencentKeychain` 真的从用户 macOS Keychain 读到了 `AKID***` (源 = keychain, region = ap-shanghai)。`TestRedactSecret` 验证 `secretId` / `secretKey` 字段被 `***REDACTED***` 替换。

### 2.3 `go vet`

```
$ go vet ./internal/mcp/...
exit: 0
```

无告警。

### 2.4 `--help` (exit 0)

```
$ /Users/lune/bin/tencent-cloud-mcp --help
exit: 0
tencent-cloud-mcp v0.1.0 — Tencent Cloud MCP server (stdio JSON-RPC)

Tools (registered via MCP tools/list):

  tencent_cvm_list_instances       — list CVM instances in a region
  tencent_cvm_describe_instance    — describe a single CVM by id
  tencent_cvm_start_instance       — START a CVM (requires --force=true)
  tencent_cvm_stop_instance        — STOP a CVM (requires --force=true)
```

### 2.5 JSON-RPC: `tools/list`

```
$ echo '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' | /Users/lune/bin/tencent-cloud-mcp
exit: 0

{"jsonrpc":"2.0","id":1,"result":{"tools":[
  {"name":"tencent_cvm_describe_instance", "inputSchema":{...}, "annotations":{"destructiveHint":true, ...}},
  {"name":"tencent_cvm_list_instances",    "inputSchema":{...}, "annotations":{"destructiveHint":true, ...}},
  {"name":"tencent_cvm_start_instance",    "inputSchema":{...}, "annotations":{"destructiveHint":true, ...}},
  {"name":"tencent_cvm_stop_instance",     "inputSchema":{...}, "annotations":{"destructiveHint":true, ...}}
]}}
```

4 个工具全部注册,input schema 完整,`destructiveHint:true` 标注在 4 个工具上(注:list/describe 也标了 destructive,因为 SDK API 行为未明 — Claude Desktop 客户端会显示确认弹窗;MCP 语义上 list/describe 应该是 readOnly,这是已知 v0 限制)

工具名清单: `['tencent_cvm_describe_instance', 'tencent_cvm_list_instances', 'tencent_cvm_start_instance', 'tencent_cvm_stop_instance']`

### 2.6 JSON-RPC: `tools/call list_instances` (真实腾讯云响应)

```
$ echo '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tencent_cvm_list_instances","arguments":{"region":"ap-shanghai","limit":10}}}' | /Users/lune/bin/tencent-cloud-mcp
exit: 0

{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"{
    \"TotalCount\": 0,
    \"InstanceSet\": [],
    \"RequestId\": \"36974a26-56f7-4d61-ab17-39107e442a5f\"
}"}]}}
```

**TotalCount: 0, RequestId 是真腾讯云返回的 UUID** (不是 mock,不是 should-work)。

### 2.7 JSON-RPC: `tools/call start_instance` WITHOUT force

```
$ echo '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"tencent_cvm_start_instance","arguments":{"instance_id":"ins-abc"}}}' | /Users/lune/bin/tencent-cloud-mcp
exit: 0

{"jsonrpc":"2.0","id":3,"error":{"code":-32603,"message":"this operation requires --force confirmation (force=false)"}}
```

`RequireForce(false)` 触发 -32603,**没有调到腾讯云**(tccli 子进程根本没启动)。

### 2.8 JSON-RPC: `tools/call start_instance` WITH force (真腾讯云,带 redaction)

```
$ echo '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"tencent_cvm_start_instance","arguments":{"instance_id":"ins-abc","force":true}}}' | /Users/lune/bin/tencent-cloud-mcp
exit: 0

{"jsonrpc":"2.0","id":4,"result":{"content":[{"type":"text","text":"cvm.StartInstances: tccli [cvm StartInstances --region ap-shanghai --cli-input-json file:***REDACTED***.json]: exit 255: ... [TencentCloudSDKException] code:InvalidParameterValue.InstanceIdMalformed message:实例ID `ins-abc`不合要求... requestId:***REDACTED***"}],"isError":true}}
```

**关键观察**:
- 真的到了腾讯云 API,返回了 SDK 异常 `InvalidParameterValue.InstanceIdMalformed` (因为 `ins-abc` 不是合法 CVM id 格式 — 用户的实例是 Lighthouse `lhins-prrbaecg`,CVM id 格式是 `ins-xxxxxxxx`)
- **redaction 工作正常**:`file:***REDACTED***.json` (mktemp 路径被 redact,因为 `.json` 文件名长 32+ 字符) + `requestId:***REDACTED***` (32+ 字符 UUID)
- 实际 `RequestId` 是腾讯云的真实 UUID,不是 placeholder

### 2.9 `bash -n` 6 个云 skill 的所有脚本

> Phase 1 现有 3 个云 skill 包 (tencent / alicloud / google),其余 3 个 (aws / azure / baidu) 在 Phase 2 才建。

```
$ find ~/.claude/skills/{tencent-cloud,alicloud,google-cloud} -name "*.sh" -exec bash -n {} \;
Total: 28, Fail: 0
```

| 包 | 脚本数 | 失败 |
|---|---|---|
| tencent-cloud | 7 | 0 |
| alicloud | ~10 | 0 |
| google-cloud | ~11 (递归进 skills/cloud/...) | 0 |
| **合计** | **28** | **0** |

## 3. 已知问题

### 3.1 `tccli --InstanceIds.0` 语法在 3.1.140.1 不被接受

**症状**: `tccli cvm DescribeInstances --InstanceIds.0 ins-abc` 报 `Unknown options: --InstanceIds.0, ins-abc`

**修法 (本 PoC 已用)**: 改用 `--cli-input-json file://<mktemp>` 模式,把 `{"InstanceIds":["ins-abc"]}` 写到临时文件再喂给 tccli。`main.go:writeAndCleanup` 用 `os.CreateTemp` + `context.Done` 触发 `os.Remove` 清理。

**影响**: bash 脚本 (`cvm.sh describe`) 仍用 `--InstanceIds.0` 语法,**未修复**(遵守"不要 touch 现有 6 个 SKILL.md 包的 scripts/"硬规则)。如果用户需要 bash 路径也工作,需要单开一个 Phase 1.5 任务同步修 cvm.sh / lighthouse.sh / cdb.sh / cos.sh / cloudbase.sh 5 个文件。

### 3.2 MCP 工具的 `destructiveHint` 全部为 true

**症状**: 4 个工具的 `annotations.destructiveHint` 都是 `true`,包括 `list_instances` 和 `describe_instance` (read-only 工具)。

**原因**: v0 PoC 没区分,统一给了 destructive 标注。

**影响**: Claude Desktop 客户端对 list/describe 也会弹"危险操作确认"对话框,体验欠佳。

**修法 (Phase 2)**: 在 `RegisterTool` 加 `ReadOnlyArg` 标记,readOnly 工具不写 `DestructiveHint:true`。

### 3.3 `RedactSecret` 误伤长 token

**症状**: `requestId: 36974a26-56f7-4d61-ab17-39107e442a5f` (36 字符 UUID) 被替换成 `requestId:***REDACTED***`,因为它有 32+ 字符。

**影响**: LLM 看不到 requestId,排查问题时少了关键 ID。

**修法 (Phase 2)**: 加 allowlist — `requestId`、`RequestId`、`request_id`、`TraceId` 等字段名不 redact,只 redact 名字里带 "secret"/"key"/"token"/"pass" 的字段。

### 3.4 mktemp 文件清理在 SIGKILL 下不可靠

**症状**: `writeAndCleanup` 依赖 `ctx.Done()` 触发 `os.Remove`,但如果进程被 SIGKILL (-9),ctx 不会 cancel,临时文件残留。

**影响**: 反复调用 describe/start/stop 会在 /tmp 留下 `tccli-payload-*.json` 文件(本 PoC 跑下来估计 4-5 个)。

**修法 (Phase 2)**: 改用 `defer os.Remove(path)` 在 handler 退出时清理,或在每次启动时 sweep `/tmp/tccli-payload-*.json` (mtime > 1h 的清掉)。

## 4. 跨项目 lesson (新增 1 条)

### 4.1 redact 函数 `redactField` 不要原地改字符,跳到下一个 occurrence

**症状**: 初版 `redactField` 在 JSON 字段名匹配失败时,只把 `s[idx]` 改成 `'X'`,然后重新 `strings.Index` — 但新串里 `'X'` 还在,搜索又会找到同一个位置,**无限循环**(测试 60s timeout)。

**修法**: 匹配失败时,直接 continue 跳到下一个 occurrence (用 `idx + 1` 作下次起点),不要原地改字符。

**应用场景**: 任何"扫描-替换-重扫"模式的字符串处理函数,比如 sanitizer / escape / quote-balance。`grep -E "for.*s\[idx" redactField` 能一眼看出是否有这个反模式。

## 5. 下一步建议 (Phase 2 起步)

按设计文档 §8 Phase 2 (1-2 小时) 计划:

1. **6 云 × 4 工具 (24 工具) 全跑通** — 复用 `sdk.LoadCreds` 和 `sdk.RegisterTool`,每个云写 200 行 main.go (平均 1500 行),复制 tencent/main.go 的 handler 模板
2. **修复 cvm.sh 的 `--InstanceIds.0` 语法** — 改用同样的 `--cli-input-json file://` 模式
3. **加 `ReadOnlyArg` + 修 `destructiveHint` 标注** — 区分 read-only vs mutating 工具
4. **`RedactSecret` 加 allowlist** — `requestId` / `TraceId` 不 redact
5. **`FileCacheAPIDoc.Refresh` 真正实现** — 从 6 云的 OpenAPI catalog 抓 JSON,缓存到 `~/.cache/loom/cloud-apidocs/<cloud>/<svc>.json`
6. **加 MCP integration test** — 用 `in-process client` (mcp-go 已有 `inprocess` example) 跑 round-trip JSON-RPC,CI 自动跑
7. **`mavis team plan` 集成** — 让 Loom Agent Gate (`./loom doctor --opensource`) 把 6 个 MCP server 的 --help 输出当作 readiness 指标

## 6. 完成判据对照

| 标准 | 状态 |
|---|---|
| 1. `~/bin/tencent-cloud-mcp` 存在且能 `--help` exit 0 | ✅ (8.2 MB) |
| 2. JSON-RPC `tools/list` 返回 4 个工具定义 | ✅ (含 schema + annotation) |
| 3. JSON-RPC `tools/call` 调 `tencent_cvm_list_instances` 返回真实数据 | ✅ (TotalCount:0, RequestId 是腾讯云真 UUID) |
| 4. `~/.claude/skills/tencent-cloud/SKILL.md` 已更新 MCP 章节 | ✅ (新增 78 行 MCP 章节) |
| 5. `~/.claude/mcp_servers.json` 已创建 | ✅ (仅注册 tencent-cloud) |
| 6. `deliverable.md` 存在,最后一行 `VERDICT: PASS` | ✅ (本文件) |
| 7. 6 个云 skill 的所有 bash 脚本 `bash -n` 仍然 OK | ✅ (28 脚本,0 失败 — Phase 1 只有 3 个包存在) |
| 8. 没碰 AKSK (deliverable 里用 "AKID***" 占位) | ✅ (全文 grep 不到 `AKID[0-9A-Za-z]{30}` 真实 AKID) |

## 7. 凭证零暴露验证

```bash
$ grep -rE "AKID[0-9A-Za-z]{20}" .loom-drafts/cloud-mcp-poc-deliverable.md
(no matches)

$ grep -rE "secretKey|secret_key|access_key_secret" .loom-drafts/cloud-mcp-poc-deliverable.md | grep -v REDACTED
(no matches outside of context documentation)
```

deliverable 全文 AKID / SecretKey 全部以 `AKID***` / `***REDACTED***` 占位,没有真实 AKSK 泄漏。

VERDICT: PASS

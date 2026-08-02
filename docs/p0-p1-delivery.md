# P0 / P1 交付状态

Date: 2026-08-02

## P0 可交付性

已完成：

- README、Tencent Skill 和 MCP `tools/list` 的产品边界对齐；当前原生 MCP
  只声明 Tencent CVM、Lighthouse、CDB、CloudBase 和 CLI 诊断。
- macOS / Linux CI 矩阵，包含 module verify、gofmt、vet、race tests、
  80% 覆盖率门、ShellCheck、govulncheck、协议 smoke 和安装器 smoke。
- darwin/linux 的 arm64/amd64 发行包生成与 SHA-256 checksums。
- hermetic 协议验收确认 15 个工具、mutation gate、force schema、
  分页限制和凭证加载前的输入拒绝。

需要所有者明确授权或真实账号后才能完成：

- 推送分支、开 PR、发 GitHub Release。
- 修改 GitHub repository description。建议文案：
  `AI Agent 云管理 MCP Server + Skills；当前原生支持腾讯云 CVM、Lighthouse、CDB 和 CloudBase`。
- 用最小权限 CAM 子账号执行 opt-in 只读 live gate。

## P1 运行时与服务能力

已完成：

- 新增 Lighthouse list/describe/start/stop/reboot、CDB list/describe、
  CloudBase list/describe 和 TCCLI 状态工具。
- 精确 region/resource allowlist；资源 allowlist 存在时禁止无过滤 list。
- mode `0600` JSONL mutation audit；执行前审计写入失败时 fail closed。
- 只读调用的有界指数退避；mutation 从不自动重试。
- Tencent `Response.Error` 结构化错误，保留 RequestId 并对凭证字段做
  redaction。

后续 P1 slice：

- COS 独立 adapter：使用官方 COS SDK/COSCLI，设计临时凭证、bucket 级
  disclosure policy、对象大小/路径限制与上传审计。
- 真实账号的只读验收和经人工批准的专用测试实例 mutation 验收；
  后者不纳入默认自动化。

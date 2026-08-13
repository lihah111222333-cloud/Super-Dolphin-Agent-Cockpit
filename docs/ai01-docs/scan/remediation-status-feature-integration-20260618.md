# feature-integration-20260618 安全扫描修复状态

日期：2026-06-18
工作区：`D:\project\Super-Dolphin-worktrees\feature-integration-20260618`
原始报告：`docs/ai01-docs/scan/deep-security-scan-feature-integration-20260618.md`

## 总览

| 等级 | 原数量 | 当前状态 |
| --- | ---: | --- |
| P0 | 0 | 未发现 P0 |
| P1 | 3 | 已修复 3 项 |
| P2 | 8 | 已修复 8 项 |

## 修复明细

| 编号 | 状态 | 修复摘要 |
| --- | --- | --- |
| P1-01 | 已修复 | `/wails/ws` 增加 Host/Origin loopback 校验，并通过同源 HttpOnly/SameSite cookie 绑定 WebSocket token。 |
| P1-02 | 已修复 | automation command template 在进入 `sh -c` 前拒绝 shell 注入元字符和换行/NUL。 |
| P1-03 | 已修复 | 项目级 stdio MCP 配置增加命令白名单，仅允许内置 postgres、sqlite/dbhub、playwright 及迁移兼容包。 |
| P2-01 | 已修复 | `skills/remote/read` 增加 HTTP egress policy，拒绝 loopback、内网、link-local 等地址，并停止回显错误响应体。 |
| P2-02 | 已修复 | HTTP MCP 配置和请求路径复用 egress/header policy，拒绝敏感 header，错误状态不回显响应体。 |
| P2-03 | 已修复 | DAG artifact import 在 `allowed_source_roots` 为空时 fail-fast，不再允许任意本地文件。 |
| P2-04 | 已修复 | datasource v1/v2 导入源文件必须位于当前 workspace 内。 |
| P2-05 | 已修复 | LSP 显式绝对 `work_dir` 必须落在可信 workspace roots 内。 |
| P2-06 | 已修复 | toolbridge loopback proxy 的生产 Handler 默认生成 bearer token，请求必须带 `Authorization: Bearer ...`。 |
| P2-07 | 已修复 | 平台 TCP RPC 在监听前强制校验 `RPCAddr` 只能是 `localhost`、`127.0.0.1` 或 `::1`；每条 TCP 连接必须先用 `GO_AGENT_CTL_SESSION_TOKEN` 完成 `ctl/register` 后才允许访问其它 RPC 方法。 |
| P2-08 | 已修复 | JS/TS LSP 自动 pnpm install 增加 `--ignore-scripts`，阻断 dependency lifecycle scripts。 |

## 关键实现点

- 新增 `internal/platform/httpegress`，统一约束 HTTP 出站 URL 和 header。
- 本地文件导入统一改为 workspace root 边界校验，缺少 allowlist 时 fail-fast。
- 本地 RPC/HTTP proxy 不再只依赖 loopback 端口随机性，增加请求来源或 token 约束。
- stdio MCP 不再接受仓库配置里的任意本地命令。

## 已验证命令

```powershell
go test ./internal/platform/rpc -run TestControlRPCConnectionAuthRequiresRegisterToken -count=1
go test ./internal/ui/wails ./internal/platform/rpc ./internal/module/mcp_server ./internal/platform/toolbridge ./internal/provider/unified ./internal/contract ./internal/module/skill ./internal/module/datasource ./internal/module/datasource_v2 ./cmd/mcp-orch/orchestration/nodeexec ./cmd/mcp-orch/store/sharedfile ./cmd/mcp-lsp/multilsp -count=1
go test ./cmd/mcp-lsp/tools -count=1
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./cmd/mcp-orch/orchestration/nodeexec ./cmd/mcp-orch/store/sharedfile ./cmd/mcp-lsp/multilsp ./internal/module/skill ./internal/module/datasource ./internal/module/datasource_v2 ./internal/module/mcp_server ./internal/platform/httpegress ./internal/ui/wails ./internal/platform/rpc ./internal/platform/toolbridge ./internal/provider/unified ./internal/contract ./internal/dto/provider -count=1
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./cmd/mcp-lsp/tools -count=1
```

说明：`cmd/mcp-lsp/tools` 全量测试已通过；此前缺失的 `docs/internal-notes/LSP系统提示词.md` 与英文版 LSP 提示词文档已恢复到正式路径。

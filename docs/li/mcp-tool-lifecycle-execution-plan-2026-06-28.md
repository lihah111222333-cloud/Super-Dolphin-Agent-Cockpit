# MCP Tool Lifecycle Execution Plan 2026-06-28

## 状态

已执行完成并合入当前 `main`。本文件原先记录的是 schema/store 第一切片
之后的 ADR 0003 backend/toolbridge gate；当前 `main` 已完成
owner API、discovery/backfill、toolbridge list filtering、direct-call denial、
compatibility tests 和最终验证。

本次闭合范围不包含 UI lifecycle 控件或展示。UI 如果后续需要做，应作为单独
产品交互任务处理，不能反向改写本文件的 backend/toolbridge 完成状态。

## 已完成范围

- Schema/store gate：新增产品 SQLite migration、root `sqlc.yaml` 输入、
  `sql/queries/mcp_tool_lifecycle.sql`、sqlc 生成物、
  `internal/contract/mcp_control.go` lifecycle DTO/接口，以及
  `internal/store/mcpserver` store wrapper 和测试。
- Owner API/backfill gate：`internal/module/mcp_server/lifecycle.go` 提供
  explicit upsert/get/list、discovery ensure 和 batch backfill；backfill 不覆盖
  已有 `suspended` 或 `removed` 行。
- Toolbridge list gate：`internal/platform/toolbridge/handler_host_tools.go`
  通过 lifecycle reader 过滤 managed peer MCP tools；reader 缺失、project root
  缺失、managed tool 缺行或未知状态均 fail-closed，不发布半可用工具面。
- Direct-call gate：`internal/platform/toolbridge/handler_peer_decode.go` 和
  `handler_peer_decode_helpers.go` 在 managed MCP tool 执行前读取 lifecycle
  状态，`suspended` 与 `removed` 会拒绝 Codex surface direct call。
- Compatibility gate：旧 MCP 配置/工具 wire 不暴露 lifecycle 字段；HTTP、stdio、
  Codex app、provider e2e 与 unified manifest 的兼容性测试已覆盖。
- Test helper gate：`internal/platform/toolbridge/module.go` 提供
  `NewHandlerForTestingWithLifecycle`，使 E2E 测试能够显式注入 lifecycle reader。

## 代码证据

- `internal/module/mcp_server/lifecycle.go`
- `internal/module/mcp_server/lifecycle_service_test.go`
- `internal/module/mcp_server/rpc_test.go`
- `internal/store/mcpserver/lifecycle_store.go`
- `internal/store/mcpserver/lifecycle_store_test.go`
- `internal/platform/toolbridge/handler_host_tools.go`
- `internal/platform/toolbridge/host_tools_lifecycle_test.go`
- `internal/platform/toolbridge/handler_peer_decode.go`
- `internal/platform/toolbridge/handler_peer_decode_helpers.go`
- `internal/platform/toolbridge/codex_surface_lifecycle_test.go`
- `internal/provider/e2e/lifecycle_wire_test.go`
- `internal/provider/codexapp/lifecycle_wire_test.go`
- `internal/provider/unified/manifest_test.go`
- `internal/contract/mcp_control_test.go`
- `internal/platform/toolbridge/http_mcp_client_test.go`
- `internal/platform/toolbridge/stdio_mcp_client_test.go`

## 验证记录

主分支合并后已通过：

- LSP diagnostics：覆盖本轮变更 Go 文件，无诊断。
- `./scripts/test_with_guard.sh ./internal/module/mcp_server ./internal/store/mcpserver ./internal/platform/toolbridge ./internal/provider/e2e -count=1`
- `./scripts/test_with_guard.sh ./internal/provider/unified ./internal/provider/codexapp -count=1`
- `make sqlc-verify`
- `git diff --check && git diff --cached --check`

## ADR Gates 当前状态

- Schema gate：完成。
- Backfill gate：完成。
- Compatibility gate：完成。
- Toolbridge gate：完成。
- Direct-call gate：完成。
- Verification gate：完成。
- UI gate：非本轮范围，当前没有实现 lifecycle 控件或展示。

---
description: RPC 返回 shape 对齐 + review/start 最小实现 + InvalidParams 自定义码
---

# P1-B: RPC Shape 收口（⚡并行，~2.5h）

## 基线修正（v1→v2）

| v1 子任务 | 实际状态 | v2 处理 |
|----------|---------|--------|
| B2 Approval Method Family | ✅ 已有 `CallbackMethod/SourceMethod` + 多 method 分发 | **删除** |
| B3 Transport 新建文件 | ✅ `pending_allocator`/`fallback` 已在 approval.go 接线 | **删除新建任务** |
| B1 返回 Shape | ⚠️ 部分过时：thread/start 已 snake_case，forceComplete 返回 `{ok,forceCompleted}` 非 nil | **修正后保留** |
| B3 requestId passthrough | ⚠️ eventsurface 已注入 requestId 到 payload | **缩减** |
| B4 review/start | ❌ 仍 ErrNotImplemented | **保留** |
| (新) F3 InvalidParams | 从 P1-F 并入 | **新增** |

## 任务范围

### B1（修正）: 返回 Shape 对齐（~1h）

| Handler | 当前实际返回 | V2 期望 | 修复 |
|---------|------------|---------|------|
| `agent.launch` | `nil` | `{agent_id,name,status}` | ✅ 需修 |
| `approval/respond` | `nil` | 结构化 ack | ✅ 需修 |
| `turn/forceComplete` | `{ok:true, forceCompleted:true}` | `{force_completed:true}` | ✅ JSON tag 修正 |
| `turnForceCompleteResult` tag | `forceCompleted` | `force_completed` | ✅ 需修 |
| ~~`thread/start` tag~~ | ~~已 snake_case~~ | — | ❌ 已修，跳过 |

**修改文件**: `orchestration/rpc.go`, `turn/rpc_helpers.go`, `turn/rpc_types.go`

### B4: review/start 最小可用实现（~0.5h）

**方案**: 复用现有 `withReadyTurnSession + PrepareTurn + StartTurn`，在 turn RPC 层新增兼容参数解析，返回 `{"reviewThreadId": threadID, "turn": {id, status, items}}`。

**修改文件**: `internal/module/turn/rpc.go`, `internal/module/turn/rpc_helpers.go`, `internal/module/turn/rpc_types.go`

### B5（从 F3 并入）: InvalidParams → 自定义码（~0.5h）

**问题**: `handler.go:64,77` 直接返回 `jrpc2.InvalidParams (-32602)`

**修复**: 新增 `CodeInvalidParams = -31007`，替换两处 `jrpc2.InvalidParams`

**修改文件**: `internal/platform/rpc/errors.go`, `internal/platform/rpc/handler.go`, `internal/platform/rpc/handler_test.go`（4 处断言 `jrpc2.InvalidParams` 需同步改为 `CodeInvalidParams`，否则测试必失）

### B6: requestId transport 层承载（~0.5h）

**现状**: payload 层已通过 eventsurface 注入 requestId。缺口是 transport 层没有独立的 correlation 字段。

**修复**: 在 `push.go` 的 `PushEvent` 接口中增加可选 `RequestID` 字段

**修改文件**: `internal/platform/rpc/push.go`

### 禁止触碰 ⚠️
- `cmd/mcp-orch/orchestration/service.go` / `turn_lifecycle.go` (P1-A)
- `internal/store/*` (P1-C)
- `internal/platform/bus/*` (P1-D)
- `internal/app/*` (P1-E)
- `internal/provider/*/driver.go` / `recovery.go` (P1-F)

## 死代码清理（必做）

- B1: `forceCompleted` tag 改为 `force_completed` 后，用 `lsp_grep` 全仓确认无消费方依赖旧 tag；`approval/respond` 旧 `nil` 返回相关断言同步清理
- B4: 删除 `review/start` 旧 `ErrNotImplemented` 返回行；旧/新 handler 注册不可并存
- B5: 用 `lsp_grep` 搜索 `jrpc2.InvalidParams`，确认全仓零残留；`handler_test.go` 中 4 处旧断言同步替换
- 通用: 删除替换后未使用的 import、辅助结构和旧返回分支

## 完成标准

- [x] RPC shape 已统一到新返回结构，旧 `nil` ack 和旧 `forceCompleted` tag 不再对外暴露
- [x] `review/start` 最小实现已接线，旧 `ErrNotImplemented` 路径和重复注册已清理
- [x] `lsp_grep` 搜索旧模式关键字，全仓零残留
- [x] `go vet ./...` 无 unused import 警告
- [x] 无空函数/空文件/空目录残留

## 验证命令

```bash
go build ./internal/... ./cmd/mcp-orch/...
go test ./internal/platform/rpc/... -v
go test ./internal/module/turn/... -v
go test ./internal/module/thread/... -v

# 死代码清理验证
go vet ./...
```

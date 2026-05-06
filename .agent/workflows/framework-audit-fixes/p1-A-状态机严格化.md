---
description: 清理 SM recovered trigger + 迁移 Turn Tracker 到 stateless
---

# P1-A: SM 清理 + Turn Tracker 迁移（⚡并行，~2h）

## 基线修正（v1→v2）

| v1 子任务 | 实际状态 | v2 处理 |
|----------|---------|--------|
| A1 prepareLaunchStateLocked | ✅ 已改为 `normalizeLaunchStateLocked`，走 `recover_requested` | **删除** |
| A3 awaiting_user_input | ✅ `user_input.go` 已实现，有测试覆盖 | **删除** |
| A2 forceIdleAfterCompletionError | ⚠️ 已改用 `recoverTurnCompletionStateLocked`，但仍发非声明 `turn_completion_recovered` | **保留，缩减** |
| A4 turn tracker | ❌ 仍用裸字符串管理 6 状态 | **保留，扩展** |

## 任务范围

### A2（缩减）: 清理 `turn_completion_recovered` 非声明 trigger（~0.5h）

**问题**: `turn_lifecycle.go` 的 `recoverTurnCompletionStateLocked` 仍通过 `publishStateChanged` 发出自定义 `turn_completion_recovered` trigger，不在状态机声明表中。

**修复方案**:
1. 将 `turn_completion_recovered` 替换为标准 `TriggerTurnAborted` 或 `TriggerTurnCompleted`
2. 在 `events.go` 中删除 `turn_completion_recovered` 相关常量

**修改文件**:
- `cmd/mcp-orch/orchestration/turn_lifecycle.go`
- `cmd/mcp-orch/orchestration/events.go`

### A4（扩展）: Turn Tracker 迁移到 stateless（~1h）

**问题**: `internal/module/turn/tracker.go` 用裸字符串管理 **8 个状态**（非文档 v1 所述的 6 个），14 个接收者方法，调用面分散在 `service.go`、`interrupt_service.go`、`thread_cleanup.go`、`interrupt_envelope.go`

**修复方案**:
1. 新建 `internal/module/turn/tracker_states.go` 定义 **8 状态**（preparing, running, force_completing, interrupting, interrupted, completed, failed, stalled）+ 完整转换矩阵
2. 用 `platform/statemachine` 工厂构建 turn SM
3. 重写 `tracker.go` 使用 SM fire
4. 同步修改 `service.go`/`interrupt_service.go`/`thread_cleanup.go`/`interrupt_envelope.go` 中的裸字符串状态检查

**注意**: 复杂度高于 v1 估计。tracker 有 14 个方法，`Update()` 接受任意字符串，3 处调用站传入不同状态值。建议执行前先用 `lsp_xref(references)` 列出所有 tracker 方法的调用点，建立完整转换矩阵。

**新建文件**: `internal/module/turn/tracker_states.go`
**修改文件**: `tracker.go`, `service.go`, `interrupt_service.go`, `thread_cleanup.go`, `interrupt_envelope.go`

> ⚠️ `service.go` 由 P1-A 独占。P1-E 的 SafeGo :230 等 A4 完成后再加。

### 禁止触碰 ⚠️
- `internal/module/thread/rpc*.go` (P1-B)
- `internal/store/*` (P1-C)
- `internal/platform/bus/*` (P1-D)
- `internal/app/*` (P1-E)
- `internal/provider/*/driver.go` (P1-F)

## 死代码清理（必做）

- A2: 用 `lsp_xref(references)` 确认 `turn_completion_recovered` 常量在替换后已零引用；若零引用则删除其定义
- A2: 清理只服务于旧 recovered trigger 的辅助函数、分支和发布路径，避免标准 trigger 落地后旧逻辑残留
- A4: 用 `lsp_grep` 全仓搜索 `"preparing"`、`"running"`、`"force_completing"`、`"interrupting"`、`"interrupted"`、`"completed"`、`"failed"`、`"stalled"`，确认业务路径全部改为状态常量引用
- A4: 删除被状态机取代的旧状态判断函数（如手写 `isTerminal`）和 `interrupt_envelope.go` 中旧字符串匹配逻辑
- 通用: 删除替换后未使用的旧变量、辅助函数和 import

## 完成标准

- [x] `turn_completion_recovered` 非声明 trigger 已移除，恢复流程只走标准 trigger
- [x] Turn Tracker 已迁移到状态机常量/转换矩阵，不再依赖裸字符串状态流转
- [x] `lsp_grep` 搜索旧模式关键字，全仓零残留
- [x] `go vet ./...` 无 unused import 警告
- [x] 无空函数/空文件/空目录残留

## 验证命令

```bash
go build ./cmd/mcp-orch/... ./internal/module/turn/...
go test ./cmd/mcp-orch/orchestration/... -v
go test ./internal/module/turn/... -v

# 死代码清理验证
go vet ./...
```

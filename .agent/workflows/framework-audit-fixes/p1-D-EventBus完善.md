---
description: 真实零发布事件处理 + LogSink lifecycle 修复
---

# P1-D: Event Bus 收尾（⚡并行，~1h）

## 基线修正（v1→v2）

| v1 假设 | 实际状态 | v2 处理 |
|---------|---------|--------|
| 9 个零发布事件 | **实际仅 2 个**: `TurnStalled`/`TurnResumed` | 大幅缩减 |
| UITokensUpdated 零发布 | ✅ provider translator 已发布，uistate 已消费 | 从删除列表移除 |
| UIThreadPatch 零发布 | ✅ uistate patch.go 已发布 | 从删除列表移除 |
| UIProjectionUpdated 零发布 | ✅ uistate SetPreference 已发布 | 从删除列表移除 |
| 4 个 task 事件零发布 | ⚠️ `unified/event_map.go` 仍注册了翻译器 | 删除需同步清理 event_map |

## 任务范围

### D1: 零发布事件处理（~0.5h）

**真实零发布（2 个，补发布链路）**:
| 事件 | 发布位置 |
|------|---------|
| `TurnStalled` | orchestration stall detector |
| `TurnResumed` | orchestration recover 后 |

**死定义（4 个 task 事件，删除）**:
| 事件 | 删除范围 |
|------|---------|
| `TaskDagCreated` | `dto/task/event.go` + `dto/shared/event.go` 常量 + `bus/sink.go` 订阅 + **`unified/event_map.go` 翻译器** |
| `TaskNodeStatusChanged` | 同上 |
| `TaskWakeupDispatched` | 同上 |
| `TaskWakeupCompleted` | 同上 |

**保留不动（5 个，已有发布者）**: `UITokensUpdated`, `UIThreadPatch`, `UIProjectionUpdated`, `UITimelineAppended`（检查 uistate）, `SkillsChanged`

**修改文件**: `internal/dto/shared/event.go`, `internal/dto/task/event.go`, `internal/platform/bus/sink.go`, `internal/provider/unified/event_map.go`

### D2: LogSink Lifecycle 修复（~0.5h）

**修复**: 将 `bind*` 从 `NewLogSink()` 构造函数移到新增 `Start()` 方法，在 `bus/module.go` OnStart 中调用。

**修改文件**: `internal/platform/bus/sink.go`, `internal/platform/bus/module.go`

### 禁止触碰 ⚠️
- `cmd/mcp-orch/orchestration/service.go` / `turn_lifecycle.go` (P1-A)
- `internal/module/*/rpc*.go` (P1-B)
- `internal/platform/rpc/handler.go` (P1-B 独占)
- `internal/platform/rpc/push.go` (P1-B)
- `internal/store/*` (P1-C)
- `internal/app/*` (P1-E)
- `internal/provider/*/driver.go` (P1-F)

## 死代码清理（必做）

- D1: 删除 4 个 task 事件后，用 `lsp_grep` 搜索 `TaskDagCreated`、`TaskNodeStatusChanged`、`TaskWakeupDispatched`、`TaskWakeupCompleted`，确认全仓零残留
- D1: 如果 `dto/task/event.go` 清空则删文件；如果 `bindTask` 只剩空壳则删函数；检查 `TaskEmitters` 是否仍有保留必要
- D2: `LogSink` 构造函数中不再残留订阅逻辑；迁移后删除无用的临时变量、闭包和重复绑定代码
- 通用: 删除替换后空分支、空类型和未使用 import

## 完成标准

- [x] 4 个死 task 事件及其翻译/订阅注册已删除，真实发布链路仅保留有效事件
- [x] `LogSink` 订阅逻辑已迁到显式 `Start()` 生命周期，不留旧构造期订阅残影
- [x] `TaskEmitters`、空文件和空函数已按实际引用结果完成清理
- [x] `lsp_grep` 搜索旧模式关键字，全仓零残留
- [x] `go vet ./...` 无 unused import 警告
- [x] 无空函数/空文件/空目录残留

## 验证命令

```bash
go build ./internal/platform/bus/... ./internal/dto/... ./internal/provider/unified/...
go test ./internal/platform/bus/... -v
go test ./internal/platform/eventsurface/... -v

# 死代码清理验证
go vet ./...
```

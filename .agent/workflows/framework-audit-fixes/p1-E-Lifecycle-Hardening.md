---
description: shutdown 顺序修正 + start timeout + signal 收敛 + goroutine panic policy
---

# P1-E: Lifecycle Hardening（⚡并行，~2.5h）

## 基线修正（v1→v2）

- `app.go` 已有 `fx.StopTimeout(ShutdownTimeout)`，但无 `fx.StartTimeout`
- fx@v1.24.0 默认 start/stop timeout 是 15s
- 双 signal 问题是 Fx `app.Done()` + RunGroup signal actor 并存，不是 `group.go` 内部
- `group.go` 自身无裸 `go func()`，goroutine 由 `oklog/run` 启动
- 生产代码至少 11 处裸 `go func()`（分布在 app/skill/turn/rpc/provider/wails）

## 任务范围

### E1: Start Timeout + Stop Timeout 显式化（~0.5h）

**修复**: 在 `app.go` 的 `fx.New()` 中增加 `fx.StartTimeout(platformconfig.StartupTimeout)`。在 `timeouts.go` 中新增 `StartupTimeout = 30 * time.Second`（不与 `ShutdownTimeout` 重名）。

**修改文件**: `internal/app/app.go`, `internal/platform/config/timeouts.go`

### E2: Shutdown 顺序验证（~0.25h）

**现状**: `modules.go` 已经是 `db.Module` 在前、`bus.Module` 在后，Fx LIFO stop 顺序已经满足 `... → bus → db`。provider `sessions.CloseAll` 在 OnStop 中会早于 db/bus 执行。

**任务**: 验证而非修改。在集成测试中确认 stop 顺序符合预期，如果发现偏差再调整。

**修改文件**: `internal/app/modules.go` (仅在需要时)

### E3: Signal 入口收敛（~0.5h）

**问题**: `runApp()` 等待 `app.Done()` + `BindRuntime` 的 `RunGroup(EnableSignals: true)` 两套 signal 并存。

**修复**: 在 Fx 托管 runtime 下不再给 RunGroup 开 signal actor（`EnableSignals: false`）。在 `runner.go` 中把 shutdown 请求做成 `sync.Once` idempotent。

**修改文件**: `internal/app/runner.go`, `internal/platform/runner/group.go`

### E4: Goroutine Panic Policy（~1h）

**修复**: 使用 P0 创建的 `SafeGo` helper，替换以下生产裸 `go func()` 启动点：

| 文件 | 行 | 说明 |
|------|-----|------|
| `internal/app/app.go` | :96 | 后台任务 |
| `internal/app/runner.go` | :37 | 主 goroutine |
| `internal/module/skill/events.go` | :46 | 事件处理 |
| `internal/module/turn/service.go` | :230 | turn 启动 |
| `internal/platform/rpc/server.go` | :120 | 连接处理 |
| `internal/provider/claudecli/session_events.go` | :14 | 事件循环 |
| `internal/provider/codexapp/recovery.go` | :105,:260 | 恢复重试 |
| `internal/provider/codexapp/session_approval.go` | :29 | 审批处理 |
| `internal/provider/unified/session.go` | :178 | 关闭超时 |
| `internal/ui/wails/runner.go` | :25 | Wails 启动 |
| ~~`internal/platform/hooks/dispatcher.go`~~ | ~~:204~~ | ~~已有自定义 recover+markDispatchWorkerPanicResult，不用 SafeGo~~ |

同时，`oklog/run` 的 execute func 需要外层 defer/recover 包装（在 `group.go` 的 `Add` wrapper 中处理）。

**注意**: P1-F 负责的 `codexapp/recovery.go` 和 `codexapp/driver.go` 中的吞错改动不在此任务范围。本任务只加 SafeGo 包装，不改错误处理逻辑。

**额外职责**: `runner.go:52` 的 `_ = p.Shutdowner.Shutdown()` 改为 `LogIgnoredError`（从 P1-F 接管）。

**修改文件**: `internal/platform/runner/group.go`, `internal/app/runner.go` + 上表中各文件

### 禁止触碰 ⚠️
- `cmd/mcp-orch/orchestration/*` (P1-A)
- `internal/module/*/rpc*.go` (P1-B)
- `internal/platform/rpc/handler.go` (P1-B 独占)
- `internal/store/*` (P1-C)
- `internal/platform/bus/*` (P1-D)
- `internal/provider/*/driver.go` 吞错改动 (P1-F，SafeGo 可以加)

## 死代码清理（必做）

- E3: 若 `EnableSignals` 全仓最终全部为 `false`，评估并删除 signal actor 实现代码；删除旧的重复 shutdown 路径，确保只保留幂等入口
- E4: 每处 SafeGo 替换都必须删除旧 `go func()` 整块，不能“加新不删旧”
- E4: 旧 goroutine 内的通用 `recover` 分支如已被 SafeGo 覆盖则删除；清理闭包中不再使用的局部变量和参数
- 通用: 删除替换后无用的 helper/import，避免 `runner`、`group`、`app` 中双路径并存

## 完成标准

- [x] Fx start/stop timeout 与 shutdown 行为已显式化或验证完成，不保留重复入口
- [x] signal 收敛后仅保留单一路径，旧重复 shutdown 和 signal actor 残留已清理
- [x] 生产裸 `go func()` 替换为 SafeGo 后，旧 goroutine 块和冗余 recover 已删除
- [x] `lsp_grep` 搜索旧模式关键字，全仓零残留
- [x] `go vet ./...` 无 unused import 警告
- [x] 无空函数/空文件/空目录残留

## 验证命令

```bash
go build ./internal/... ./cmd/...
go test ./internal/archtest/... -run 'TestCodeSizeGuard|TestSharedBudget'

# 死代码清理验证
go vet ./...
```

---
description: 高风险吞错点 LogIgnoredError 替换 + CapabilityError 统一映射
---

# P1-F: 错误处理收口（⚡并行，~2h）

## 基线修正（v1→v2）

| v1 子任务 | v2 处理 |
|----------|--------|
| F1 12 个高风险吞错 | 修正为 **10 个**（runner.go 交 P1-E，handler.go 交 P1-B） |
| F2 CapabilityError | 保留，P1-F 只加 `MapCapabilityError` 到 `errors_helper.go` |
| F3 InvalidParams | **并入 P1-B**（B5 子任务） |

## 协调规则

| 冲突文件 | 归属 |
|---------|------|
| `internal/platform/rpc/handler.go` | **P1-B 独占**（B5 InvalidParams） |
| `internal/app/runner.go:52` | **P1-E 独占**（E4 顺手接入 LogIgnoredError） |
| `internal/platform/rpc/errors_helper.go` | **P1-B 先合并** CodeInvalidParams，**P1-F 后加** MapCapabilityError |

## 任务范围

### F1: 高风险吞错点修复（~1.5h）

使用 P0 创建的 `LogIgnoredError(logger, msg, err)` 替换以下 10 个高风险点：

| # | 文件 | 行 | 当前 | 需要 logger? |
|---|------|-----|------|-------------|
| ~~1~~ | ~~`internal/provider/codexapp/recovery.go`~~ | ~~:105~~ | ~~划归 P1-E 独占（SafeGo+LogIgnored 统一处理）~~ | — |
| 2 | `internal/provider/codexapp/recovery.go` | :283 | `_ = s.attemptRecovery(...)` | ✅ |
| 3 | `internal/provider/codexapp/recovery.go` | :286 | `_ = s.attemptRecovery(...)` | ✅ |
| 4 | `internal/provider/codexapp/driver.go` | :93 | `_ = s.ForceStop()` | ✅ driver 有 logger |
| 5 | `internal/provider/codexapp/driver.go` | :108 | `_ = s.ForceStop()` | ✅ |
| 6 | `internal/provider/claudecli/driver.go` | :141 | `_ = s.stop(true)` | ✅ driver 有 logger |
| 7 | `internal/module/thread/lifecycle.go` | :316 | `_ = s.stopManagedAgent(...)` | ✅ service 有 logger |
| 8 | `internal/module/thread/stop.go` | :116 | `_ = s.turns.CleanupThread(...)` | ✅ |
| 9 | `internal/module/thread/command.go` | :402 | `_ = s.upsertThread(...)` | ✅ |
| 10 | `internal/ui/wails/module.go` | :131 | `_ = shutdowner.Shutdown()` | ⚠️ 需从闭包传入 |

**修改文件**: 上表 6 个文件

### F2: MapCapabilityError 统一映射（~0.5h）

在 `internal/platform/rpc/errors_helper.go` 中新增：

```go
// MapCapabilityError maps a CapabilityError to the standard RPC gate code.
func MapCapabilityError(err error) *jrpc2.Error {
    var capErr *provider.CapabilityError
    if errors.As(err, &capErr) {
        return jrpc2.Errorf(CodeCapabilityGate, "%s", capErr.Error())
    }
    return nil
}
```

**注意**: 实际在 thread/turn rpc handler 中集成此映射由 **P1-B 负责**（在 handler middleware 层统一拦截），P1-F 只提供映射函数。

**修改文件**: `internal/platform/rpc/errors_helper.go`

### 禁止触碰 ⚠️
- `cmd/mcp-orch/orchestration/*` (P1-A)
- `internal/module/*/rpc*.go` (P1-B)
- `internal/platform/rpc/handler.go` (P1-B 独占)
- `internal/platform/rpc/errors.go` (P1-B 独占)
- `internal/store/*` (P1-C)
- `internal/platform/bus/*` (P1-D)
- `internal/app/*` (P1-E 独占)
- `internal/platform/runner/*` (P1-E)

## 死代码清理（必做）

- F1: 旧 `_ =` 忽略错误行必须全部删除；相关 `//nolint:errcheck` 注释一并删除
- F1: 引入 `shared`、logger 等新 import 后，确认不违反 archtest，且不留下未使用导入
- F2: 用 `lsp_grep` 搜全仓现有 `CapabilityError` 手写处理，统一评估是否替换为 `MapCapabilityError`，至少不能新增第二套映射逻辑
- 通用: 删除被 `LogIgnoredError` 替代的旧辅助分支、局部变量和 import

## 完成标准

- [x] 高风险吞错点已改为显式记录错误，旧 `_ =` 和 `nolint:errcheck` 残留已清除
- [x] `MapCapabilityError` 已提供统一入口，不再继续扩散手写 capability 映射分支
- [x] `lsp_grep` 搜索旧模式关键字，全仓零残留
- [x] `go vet ./...` 无 unused import 警告
- [x] 无空函数/空文件/空目录残留

## 验证命令

```bash
go build ./internal/...
go vet ./internal/provider/... ./internal/module/thread/... ./internal/ui/wails/...
go test ./internal/provider/... -v
go test ./internal/module/thread/... -v

# 死代码清理验证
go vet ./...
```

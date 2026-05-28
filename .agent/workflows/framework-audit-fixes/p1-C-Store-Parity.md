---
description: 扩展现有 binding repo + AILog keyword 下推 + DBQuery READ ONLY 加固
---

# P1-C: Store 加固（⚡并行，~1h）

## 基线修正（v1→v2）

| v1 子任务 | 实际状态 | v2 处理 |
|----------|---------|--------|
| C1 新建 threadbinding repo | ✅ `binding` 包已有 `BindAgentThread/Unbind/List/GetThread/UpdateCwd`，sqlc `thread_binding.sql.go` 已存在 | **改为扩展现有 binding** |
| C2 AILog 从零建分类 | ✅ 已有 `Category/Method/URL/Status/Model` + `ListByCategory/CountByStatus/ListRecent` | **缩减为 keyword 下推** |
| C3 DBQuery 实现执行器 | ✅ 已有真实执行器，带 SELECT-only/黑名单/10s 超时/10000 行限制 | **缩减为 READ ONLY 加固** |

## 任务范围

### C1（修正）: 扩展 binding repo 补齐 V2 parity（~0.5h）

**现状**: `internal/store/binding/` 已覆盖多数 thread binding 操作。
**缺口**: 对比 V2 `AgentThreadBindingStore`，仍缺 `Rebind`（事务级）和 `ListProviderMap/ListCwdMap`。

**修复**: 在 `internal/store/binding/contract.go` 和 `store.go` 中补齐缺失方法，如需新 SQL 则加到 `sql/queries/`。

**修改文件**: `internal/store/binding/contract.go`, `internal/store/binding/store.go`, `sql/queries/thread_binding.sql` (如需)

### C2（缩减）: AILog keyword 下推到 store 层（~0.25h）

**现状**: 已有 `ListByCategory`，但 keyword 过滤仍在 dashboard 内存层。
**修复**: 在 `internal/store/ailog/contract.go` 的 `List`/`ListByCategory` 中增加 `keyword` 参数，sqlc query 加 `ILIKE` 过滤。

**修改文件**: `internal/store/ailog/contract.go`, `internal/store/ailog/store.go`

### C3（缩减）: DBQuery 事务级 READ ONLY（~0.25h）

**现状**: 已有执行器，安全限制完善。
**缺口**: 缺事务级 `SET TRANSACTION READ ONLY` defense-in-depth。
**修复**: 在 `executor.go` 的 `executeQuery` 方法（未导出，由 `store.go:Query` 调用）中包一层 `BEGIN; SET TRANSACTION READ ONLY; ... COMMIT`，或基于 `sqlc.WithTx` 在 tx 内执行。

**修改文件**: `internal/store/dbquery/executor.go`

### 禁止触碰 ⚠️
- `cmd/mcp-orch/orchestration/*` (P1-A)
- `internal/module/*/rpc*.go` (P1-B)
- `internal/platform/rpc/*` (P1-B 独占 handler.go/errors.go)
- `internal/platform/bus/*` (P1-D)
- `internal/app/*` (P1-E)
- `internal/provider/*` (P1-F)

## 死代码清理（必做）

- C1: 新增 repo 方法后，用 `lsp_grep` 全仓搜索 module 层是否仍有手写等价的直接 sqlc 调用散落；有则收口或删除重复调用
- C2: AILog keyword 下推后，检查 `dashboard/ai_logs.go` 中内存 keyword filter 是否已删除，避免 store/db 过滤与内存过滤并存
- C3: READ ONLY 加固后清理旧临时事务包装或重复防御分支，避免新旧执行路径并存
- 通用: 删除未使用的旧函数、变量和 import

## 完成标准

- [x] binding repo 缺口方法已补齐，module 层无散落的手写等价 binding/sqlc 调用
- [x] AILog keyword 过滤已下推到 store，dashboard 不再保留重复内存过滤
- [x] DBQuery 已具备事务级 READ ONLY 防护，未留下重复或失效的旧执行分支
- [x] `lsp_grep` 搜索旧模式关键字，全仓零残留
- [x] `go vet ./...` 无 unused import 警告
- [x] 无空函数/空文件/空目录残留

## 验证命令

```bash
go build ./internal/store/...
go test ./internal/store/... -v
go test ./internal/archtest/... -run 'TestSqlcBoundary|TestDependencyDirection'

# 死代码清理验证
go vet ./...
```

# P1c: CodexApp session runtime 收口

## 目标

把 `internal/provider/codexapp` 中 peer supervisor 之外的 session 级长跑 runtime 单独收口，避免 P1a 完成后误判为 codexapp 全部 runtime ownership 已达标。

## 覆盖问题

- `newSession()` 返回前启动 `startReadLoop()` / `startHealthLoop()`
- `startReadLoop()` 裸 `go s.runReadLoop(...)`
- `handleConnectionDead()` 使用 `SafeGo(context.Background(), ...)` fire-and-forget `attemptRecovery`
- recovery worker 没有 coalescing / owner / shutdown drain

## 目标架构

- session runtime 必须由显式 owner 持有，例如 `SessionRuntime` 或 provider-level session runner
- reader / health / recovery 都通过同一 session ctx、同一 shutdown gate、同一 drain 入口管理
- `Close()` / `ForceStop()` 必须先阻止新 recovery，再 cancel，再 join reader/health/recovery
- `connection.dead` 抖动必须 coalesce，不能无限派生 recovery worker

## TDD 与旧实现清理

- 先补失败测试：`newSession()` 不再隐式起飞，或起飞必须有显式 owner handle
- 先补失败测试：`Close/ForceStop` 后不再发布新的 `recovery.attempt`
- 先补失败测试：重复 `connection.dead` 不会派生多个并发 recovery worker
- 修复后删除旧 `SafeGo(context.Background(), ...)` recovery 路径
- 修复后删除裸 `go s.runReadLoop(...)` 旁路，或降为 owner 内部可 join 原语

## 验收标准

- session reader / health / recovery 都有明确 owner
- shutdown 时能 cancel + join/drain
- recovery worker 有 coalescing 与 stop gate
- `P1a` 只闭环 peer supervisor，`P1c` 闭环 session runtime，二者验收互不替代

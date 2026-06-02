# 第 52 轮审查结论

## 审查范围

- `cmd/mcp-orch/orchestration/node_executor_dispatch.go`（NodeLifecycleHooks、ProvideNodeLifecycleHooks、ProvideAutomationExecutor、loggingNodeLifecycleHook.Handle、executeNodeWithLifecycleHooks、invokeLifecycleHook、invokeTerminalFailureHooksForWakeup/TaskNode、invokeStateChangeHooksForTaskNode、invokeFailureHookForTaskNode、executorForNodeType、handleClaimedViaRouter、patchAgentExecModel、appendAgentValidationDiagnostic、rawJSONObject、nestedJSONObject）

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `node_executor_dispatch.go:79-119` invokeLifecycleHook | 协程延迟 | line 90 `hookCtx := context.Background()` 然后 line 94 加 1s timeout；但 line 108-118 只等 100ms（`lifecycleHookDispatchWait`）就放弃等待 | hook 在 100ms 内未完成时 Warn 但继续执行——hook 可能在后台跑 1s 后超时。如果 hook 有副作用（如 DB 写），主流程已继续但 hook 还在改状态 → 竞态 | 要么同步等待（阻塞直到 hook 完成或超时）；要么明确文档化「hook 是 fire-and-forget」 |
| `node_executor_dispatch.go:90-93` invokeLifecycleHook | 静默 fallback | `ctx != nil` 时用 `context.WithoutCancel(ctx)`；ctx nil 时用 Background | ctx nil 是 caller bug；WithoutCancel 让 hook 不响应上游 cancel（设计选择但有风险） | ctx nil 改 panic；WithoutCancel 加注释说明意图 |
| `node_executor_dispatch.go:121-137` invokeTerminalFailureHooksForWakeup | 静默 | `r == nil \|\| w == nil` 静默 return；dagKey/nodeKey 空静默 return；lookup 失败 Warn + return | terminal failure hook 不触发 → 通知/告警不发送 → 运维不知道节点终态失败 | lookup 失败应 return error 让 caller 决定 |
| `node_executor_dispatch.go:147-159, 161-173` invokeStateChange/FailureHookForTaskNode | 静默 | `target == nil` 静默 return；`exec == nil` 静默 return | executor 未注册时 lifecycle hook 静默不触发 | exec nil 时 Warn 日志 |
| `node_executor_dispatch.go:260-278` executorForNodeType | 静默 | `default: return nil` —— 未知 node_type 静默返 nil | 新增 node_type（如 "hybrid"）但未注册 executor 时静默跳过所有 lifecycle hook | 加 Warn 日志带 nodeType |
| `node_executor_dispatch.go:309-343` handleClaimedViaRouter | 弱契约 | line 336-342 `switch outcome.Status` 的 default 分支把 done/skipped/waiting_human/zero-value 都 markLaunched | zero-value Status（空字符串）被视为成功——如果 executor 忘记设 Status，wakeup 被标记完成但节点实际未执行 | 空 Status 应 fail-fast（返 error 或 markTransientRetry） |
| `node_executor_dispatch.go:310-316` handleClaimedViaRouter | 弱契约 | `routeRunID(w) <= 0` 时 markPermanentFail | run_id 缺失是数据 bug（enqueue 时应校验）；permanent fail 意味着永不重试 | 合理（数据 bug 不应重试），但应加 alert |
| `node_executor_dispatch.go:96-107` invokeLifecycleHook goroutine | 协程泄漏 | `runtimesafe.SafeGo` 启动 goroutine 跑 hook；主流程 100ms 后放弃等待 | 如果 hook handler 阻塞（如 DB 不可达），goroutine 在 1s timeout 后才退出。但 1s 内如果 handler 持有资源（如 DB 连接），连接池被占用 | 加 hook handler 的 DB 连接超时（应 < 1s） |

## 协程延迟定位方案

| 位置 | 延迟风险点 | 定位方案 |
| --- | --- | --- |
| `node_executor_dispatch.go:79-119` invokeLifecycleHook | 100ms dispatch wait + 1s execution timeout；hook 在后台异步跑 | 已有 Warn 日志（line 113-118）——**正面案例**。建议加 hook duration histogram |
| `node_executor_dispatch.go:309-343` handleClaimedViaRouter | RouteByWakeup 是同步调用——内部可能涉及 DB 查询 + executor.Execute | 加 per-wakeup duration 日志（从 claim 到 mark 的总耗时） |
| `node_executor_dispatch.go:66-77` executeNodeWithLifecycleHooks | before hook → Execute → after hook 串行 | Execute 是主要耗时点；before/after hook 各 100ms dispatch wait | 加 Execute duration 单独监控 |
| `node_executor_dispatch.go:96` runtimesafe.SafeGo | 每次 hook 调用都启动一个 goroutine | 高频节点执行时 goroutine 数量累积；加 concurrent hook goroutine 计数器 |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `node_executor_dispatch.go:87-88` | handler nil 静默 return |
| `node_executor_dispatch.go:90-93` | ctx nil fallback Background |
| `node_executor_dispatch.go:121-137` | r/w nil、dagKey/nodeKey 空、lookup 失败均静默 |
| `node_executor_dispatch.go:148-149, 162-163` | target nil 静默 return |
| `node_executor_dispatch.go:153-154, 167-168` | exec nil 静默 return |
| `node_executor_dispatch.go:260-278` executorForNodeType | 未知 nodeType 静默返 nil |
| `node_executor_dispatch.go:336-342` | 空 Status 被视为成功 |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `node_executor_dispatch.go:79-119` | hook 是「半同步」（等 100ms 后放弃）——既非同步也非异步 |
| `node_executor_dispatch.go:336-342` | default 分支把多种状态混为一谈 |
| `node_executor_dispatch.go:260-278` | executorForNodeType 只认 agent/automation |
| `node_executor_dispatch.go:20-22` | lifecycleHookDispatchWait=100ms / ExecutionTimeout=1s 硬编码 |
| `node_executor_dispatch.go:175-184` currentAgentModel | parse 失败静默返 "" |

## 修复优先级

### P0（必须本周修）
1. **`node_executor_dispatch.go:336-342` 空 Status 被视为成功**——如果 executor.Execute 返回 `NodeOutcome{Status: ""}` （忘记设 Status），handleClaimedViaRouter 走 default 分支 markLaunched → wakeup 标记完成但节点实际未执行。这是 DAG 调度正确性 bug。改为：空 Status 视为 framework error → markTransientRetry。
2. **`node_executor_dispatch.go:79-119` invokeLifecycleHook 半同步设计**——100ms 后放弃等待但 hook 仍在后台跑。如果 hook 有副作用（如写 DB 标记节点状态），主流程已继续执行下一步（如 markLaunched），hook 的 DB 写可能覆盖主流程的写 → 状态竞态。改为：有副作用的 hook 必须同步等待完成；无副作用的 hook 可以 fire-and-forget。

### P1（本月）
3. `node_executor_dispatch.go:260-278` executorForNodeType 未知 type 加 Warn
4. `node_executor_dispatch.go:121-137` lookup 失败改 return error
5. `node_executor_dispatch.go:90-93` ctx nil 改 panic
6. `node_executor_dispatch.go:20-22` timeout 改 cfg-driven

### P2（下个 sprint）
7. `node_executor_dispatch.go:96` hook goroutine 并发计数器
8. `node_executor_dispatch.go:175-184` currentAgentModel parse 失败加 Warn
9. `node_executor_dispatch.go:309-343` 加 per-wakeup duration 日志

## 边界条件

1. **`node_executor_dispatch.go:79-119` invokeLifecycleHook 的「半同步」设计是项目内独特模式**：100ms dispatch wait 是为了「大多数 hook 能在 100ms 内完成（如 logging hook），不阻塞主流程；慢 hook 异步跑不影响调度延迟」。这是合理的性能优化——但前提是 hook 无副作用。当前 `loggingNodeLifecycleHook`（line 51-63）确实无副作用（只打 Debug 日志）。但如果未来加有副作用的 hook（如写 metrics store），竞态风险出现。建议：hook 接口加 `SideEffectFree() bool` 方法，有副作用的 hook 同步等待。
2. **`node_executor_dispatch.go:309-343` handleClaimedViaRouter 的状态映射是 DAG 调度的核心决策点**：router 返回 outcome → 决定 wakeup 命运（markLaunched / markTransientRetry / markPermanentFail）。这个映射必须 exhaustive——任何未处理的 Status 都应 fail-safe。当前 default 分支把所有非 failed 状态都 markLaunched 是 fail-soft 选择。P0 因为空 Status 也走这个分支。
3. **`node_executor_dispatch.go:96-107` runtimesafe.SafeGo 的 panic recovery**：hook handler panic 被 runtimesafe.SafeGo 接住（与第40轮 safego.Go 同机制）。panic 后 `defer close(done)` 仍会执行（在 recover 之前 defer 已注册），所以主流程的 `<-done` 会正常返回。这是正确的——hook panic 不应杀死调度主循环。
4. **`node_executor_dispatch.go:186-231` patchAgentExecModel / appendAgentValidationDiagnostic 的 JSON 操作**：这两个函数修改 node.Config JSON（patch model / append diagnostic）。操作是 immutable（返回新 json.RawMessage），不修改原 Config。这是正确的——避免 shared state 污染。但 JSON marshal/unmarshal 在 hot path 上有性能成本。
5. **`node_executor_dispatch.go:302-343` handleClaimedViaRouter 的错误分类**：`errAgentReadyRunningWriteFailed` 走 `retryWakeup`（立即重试）；其他 framework error 走 `markTransientRetry`（延迟重试）。这是合理的错误分级——ready→running 写失败是瞬态竞态（另一个 dispatcher 先写了），立即重试大概率成功。
6. **`node_executor_dispatch.go:33-41` ProvideNodeLifecycleHooks 注册 4 个 hook point**：BeforeExecute / AfterExecute / OnStateChange / OnFailure 全部用同一个 loggingNodeLifecycleHook。这是 MVP 实现——未来可替换为 metrics hook、notification hook 等。当前 logging hook 只打 Debug 日志，无副作用，与「半同步」设计兼容。

---

**本轮总结**：发现 2 个 P0 问题：①handleClaimedViaRouter 空 Status 被视为成功让节点可能未执行就标记完成；②invokeLifecycleHook 半同步设计在有副作用 hook 场景下存在状态竞态。`invokeLifecycleHook` 的 100ms dispatch wait + Warn 日志是协程延迟可观测性的正面案例。`handleClaimedViaRouter` 的错误分级（immediate retry vs delayed retry vs permanent fail）是合理的调度设计。

**累计进度**：52 轮完成。cron `fd4b4728` 继续推进。

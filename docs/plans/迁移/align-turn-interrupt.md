# V2↔V3 1:1 对齐：`turn/interrupt` + `turn/forceComplete`

## 范围与源码

- V2 RPC 入口：
  - `go-agent-v2/internal/apiserver/methods_thread_turn.go:62-70`
  - `go-agent-v2/internal/apiserver/methods_turn.go:84-89`
  - `go-agent-v2/internal/apiserver/methods.go:168-181`
- V2 实际执行链：
  - `go-agent-v2/internal/apiserver/codexadapter/adapter.go:269-355`
  - `go-agent-v2/internal/apiserver/codexadapter/adapter_stall.go:31-43`
  - `go-agent-v2/pkg/agentsdk/service/interrupt/turn_interrupt_core.go:93-104,144-306`
  - `go-agent-v2/pkg/agentsdk/service/tracker/turn_tracker_lifecycle_core.go:60-107,320-369`
- V3 RPC 入口：
  - `internal/module/turn/rpc.go:60-75`
  - `internal/module/turn/rpc_types.go:22-29,71-73`
  - `internal/platform/rpc/handler.go:53-77,122-135`
- V3 实际执行链：
  - `internal/module/turn/service.go:117-159,173-220,285-309`
  - `internal/module/turn/tracker.go:83-166`
  - `internal/provider/codexapp/session.go:133-146,237-287`
  - `internal/provider/claudecli/session.go:208-230`
  - `internal/provider/claudecli/session_events.go:46-96`
  - `internal/provider/claudecli/event_map.go:55-84`

## 先校正一个口径

- 按当前仓库里的生产实现，V2 `turn/interrupt` 并不返回 `{"ok":true}`。
  - 真实生产返回来自 `interruptPayload(...)`，字段是 `confirmed`、`mode`、`interruptSent`、`stateBefore`、`stateAfter`，以及条件性追加的 `waitedMs`、`activeObserved`，见 `go-agent-v2/pkg/agentsdk/service/interrupt/turn_interrupt_core.go:128-142,212`。
  - `{"ok":true}` 只出现在部分测试替身里，例如 `go-agent-v2/internal/guards/rpc_golden_test.go:535-545`。
- 当前仓库里的 V2 `turn/forceComplete` 生产返回是 `{"confirmed":true,"forceCompleted":true}`，见 `go-agent-v2/pkg/agentsdk/service/interrupt/turn_interrupt_core.go:304`。

## `turn/interrupt`

| 对比项 | V2 | V3 | 结论 |
| --- | --- | --- | --- |
| 参数（`threadId`） | handler 直接解 `turnInterruptParams`，再走 `withRequiredThreadID("turn/interrupt", p.ThreadID, ...)`，缺失即报错。 | `rpc.ThreadHandler(...)` 先由 `ThreadScope()` 从 `threadId/threadID/thread_id` 提取 thread id 放进 context，再由 `SessionResolver.ResolveSession(ctx, threadID)` 解析 session。 | ✅ |
| provider 侧中断机制 | `Adapter.TurnInterrupt()` 最终走 `interruptsvc.TurnInterrupt(...)`，内部通过 `sendInterruptCommand()` 对进程发送 `"/interrupt"`。这是本地进程命令式中断。 | `turn.Service.InterruptTurn()` 只调用统一 contract `session.Interrupt(ctx, dto.InterruptRequest{ThreadID, Source})`。`codexapp` 实现会调用远端 RPC `turn/interrupt`；`claudecli` 实现则直接 `SIGINT` 进程并本地结束 active turn。 | ⚠️ |
| 状态机转换 | V2 先用 `markTrackedTurnInterruptRequested(threadID)` 打 `InterruptRequested` 标记；tracker 最终在 `CompleteTrackedTurnByIDCore()` 中把 `"completed"` 改写成 `"interrupted"`。另一路还会轮询 runtime/tracked turn 终态。 | V3 先 `tracker.MarkInterruptRequested(localID)`，显式把状态改成 `"interrupting"`；随后 `waitForTurnSettle()` 根据 handle/tracker 结果进入 `"interrupted"` / `"completed"` / `"failed"`。语义接近，但实现依赖 handle watcher，不是 V2 那套 runtime+active-turn tracker 组合。 | ⚠️ |
| 返回值 | 生产实现返回确认型 envelope，不是 `{"ok":true}`。 | handler 固定返回 `turnInterruptResult{OK:true}`；provider 侧返回体被 `session.Interrupt(...) error` contract 吃掉了，`codexapp` 甚至直接丢弃底层 RPC 返回值。 | ❌ |
| tracker 清理 | V2 真正 finalize 时走 `CompleteTrackedTurnByIDCore()`，会 `removeActiveTrackedTurn(...)`，把 active turn 直接从 tracker map 移除。 | V3 `tracker.Complete()` 只把状态改成 terminal，并不删除；删除要等后续 `tracker.Cleanup()` 的 TTL 清扫。 | ❌ |
| 事件发布 | V2 `turn/interrupt` 自身只在 `no active turn` 重入分支里直接 `notifyTurnCompleted(...)`；常规路径更多依赖 tracked turn 完成链路再发 `turn/completed`。 | V3 `turn` 模块本身不发布 `turn/completed`/`turn/interrupted` RPC 事件；`codexapp` 依赖 provider 通知 `turn/completed`/`turn/aborted`，`claudecli` 则自己 dispatch `turn:interrupted` raw event。事件源头与事件名都不再 1:1。 | ⚠️ |

**小结**：`turn/interrupt` 只有参数入口还算对齐；provider 中断方式、返回值、tracker 生命周期、事件源都已经不是 1:1。整体判定：`⚠️ 部分对齐`。

## `turn/forceComplete`

| 对比项 | V2 | V3 | 结论 |
| --- | --- | --- | --- |
| 参数（`threadId`） | handler 直接解 `threadIDParams`，再走 `withRequiredThreadID("turn/forceComplete", p.ThreadID, ...)`。 | handler 走 `rpc.ThreadHandler(...)`，仍然强制要求 `threadId`，再经 `SessionResolver` 拿 session。 | ✅ |
| provider 侧中断机制 | `Adapter.TurnForceComplete()` 只是调用 `interruptsvc.TurnForceComplete(...)`。内部是 best-effort `sendInterrupt(proc)`，本质仍是本地 `"/interrupt"`，而且没有独立 `forceComplete` provider contract，也不会把 `"force_complete"` 传给 provider。 | `turn.Service.ForceCompleteTurn()` 仍然没有独立 provider contract，但会调用 `session.Interrupt(ctx, dto.InterruptRequest{ThreadID, Source:"force_complete"})`。`codexapp` 仍走 `turn/interrupt` RPC，`claudecli` 仍走 `SIGINT`。V3 和 V2 不是同一套 force-complete 语义。 | ❌ |
| 状态机转换 | V2 `turnForceComplete()` 不打 `InterruptRequested`，而是立刻 `notifyTurnCompleted(threadID, "completed", "force_complete")`；tracker 会被直接 finalize 成 `"completed"`。 | V3 `ForceCompleteTurn()` 只发一次 `session.Interrupt(Source="force_complete")`，既不 `MarkInterruptRequested`，也不 `waitForTurnSettle()`。最终状态完全留给 handle/watcher/provider 决定。测试 `internal/module/turn/service_test.go:204-245` 明确说明这一点。 | ❌ |
| 返回值 | 生产实现返回 `{"confirmed":true,"forceCompleted":true}`。 | handler 直接 `return nil, svc.ForceCompleteTurn(...)`，成功时 JSON-RPC result 是 `null`。 | ❌ |
| tracker 清理 | V2 通过 `completeTrackedTurnByID(...)` 立即 finalize active tracked turn，并同步生成 completion payload。 | V3 `ForceCompleteTurn()` 本身完全不动 tracker；后续 watcher 最多把状态改成 terminal，但不会像 V2 一样立即从 active 集合里清掉。 | ❌ |
| 事件发布 | V2 直接同步发 `turn/completed`，reason=`force_complete`。从现有 side-effect trace 看，这条通知还会带出后续 UI 变更事件。 | V3 `turn` 模块没有对应直发事件。若底层 provider 自己发事件，`codexapp` 可能是 `turn/completed`/`turn/aborted`，`claudecli` 则更像 `turn:interrupted`；没有 V2 那种固定的同步 `turn/completed(force_complete)`。 | ❌ |

**小结**：`turn/forceComplete` 在 V3 里只是 `Interrupt(Source="force_complete")` 的薄包装，和 V2 当前实现不是 1:1。整体判定：`❌ 不对齐`。

## 总结

| 方法 | 总结 |
| --- | --- |
| `turn/interrupt` | `⚠️` 只有 `threadId` 入参与“会发一次中断请求”这层意图相近；返回值、provider 中断方式、tracker 清理、事件发布都不再 1:1。 |
| `turn/forceComplete` | `❌` V2 是“best-effort interrupt + 立即本地完成并发 `turn/completed`”，V3 是“给 generic interrupt 打一个 `force_complete` source，终态交给 provider/watcher”，语义已明显漂移。 |

## 迁移结论

- 若目标是 **V2↔V3 1:1 对齐**，`turn/interrupt` 需要至少补齐两点：
  - 返回值 contract 统一。
  - tracker 完成/清理与事件发布时机统一。
- `turn/forceComplete` 需要补独立语义，而不是继续复用 generic `Interrupt(Source="force_complete")`：
  - 独立返回值。
  - 独立 tracker finalize/cleanup。
  - 独立 completion event contract。

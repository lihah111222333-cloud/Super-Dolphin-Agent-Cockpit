# 第 50 轮审查结论

## 审查范围

- `internal/sidecar/orch/orchestration/hook_consumer.go`（hookConsumer、After、dispatchAfterTopic、handleSessionStartTopic、handleStateChangeTopic、handleTurnAfterTopic、handleTurnFailedTopic、handleTurnProgressTopic、handleProcessExitTopic、handleThreadStarted、handleStateChanged、shouldDeferIdleHook、ProvideHookAfterHandler、HookAfterHandlerParams）

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `hook_consumer.go:136-148` After | 静默 | `c == nil \|\| c.svc == nil` 时静默返 Approve（line 138-140） | hook consumer 未注入时所有 hook 被静默 approve——agent 生命周期事件全部丢失 | 改为返 error 或 Reject |
| `hook_consumer.go:141-143` After | 静默 | `decodeHookContextEnvelope` 失败时 `!ok` 静默返 Approve | hook payload 格式错误（如 JSON 损坏）被静默吞掉——事件丢失 | 至少 Warn 日志 + 返 error |
| `hook_consumer.go:150-165` dispatchAfterTopic | 静默 | 未知 topic 走 `default`（无 default case）—— 静默丢弃 | 新增 topic 但未更新 switch 时事件静默丢失 | 加 `default: c.logger.Warn("unknown topic", ...)` |
| `hook_consumer.go:167-201` handle*Topic | 静默 | `decodeHookEvent` 失败时 `!ok` 静默 return | 事件 JSON 解码失败（字段缺失/类型错）被静默吞掉 | decodeHookEvent 内部应已 Warn；但 caller 无法区分「解码失败」vs「kind 不匹配」 |
| `hook_consumer.go:203-222` handleThreadStarted | 弱契约 | `c.svc.withAgentLocked(ev.AgentID, ...)` 如果 agentID 不存在会怎样？ | 取决于 withAgentLocked 实现——如果 agent 不存在返 error，line 221 `logUnexpectedHookError` 只 Warn | 新 agent 的 session.start 可能在 runtime 注册前到达——需确认 withAgentLocked 的 not-found 行为 |
| `hook_consumer.go:233-269` handleStateChanged | 静默 | line 244-253 session fence 不匹配时 Warn + `return nil`（不报错） | stale event 被丢弃是正确的（fence 设计），但 Warn 日志可能在高频场景下爆量 | 加 rate limiter 或改 Debug |
| `hook_consumer.go:258-265` shouldDeferIdleHook | 静默 | provisioning/recovering 状态下 idle hook 被 defer（Info 日志 + return nil） | 如果 launch commit 永远不来（launch 卡住），idle hook 被永久丢弃 → agent 永远不进 idle | 加 timeout：defer 超 30s 后强制应用 idle |
| `hook_consumer.go:69-83` HookAfterHandlerParams | 弱契约 | 所有依赖都标记 `optional:"true"` | 任何依赖缺失都不报错——hookConsumer 可能在缺少关键依赖（如 DAGFallbackFlow）时运行 | 关键依赖（Service）不应 optional |
| `hook_consumer.go:128-132` opts loop | 静默 | nil opt 静默 continue | 与第30轮 middleware Chain nil-skip 同模式 | 至少 Debug 日志 |

## 协程延迟定位方案

| 位置 | 延迟风险点 | 定位方案 |
| --- | --- | --- |
| `hook_consumer.go:136-148` After | 每个 hook event 都走 After → decode → dispatch → handle* → withAgentLocked | 加 per-event duration 监控；P99 > 50ms 打 Warn |
| `hook_consumer.go:203-222` handleThreadStarted | withAgentLocked 持锁 + applyRuntimeReport + publishAgentRuntimeReported | 锁持有时间监控；publish 如果阻塞会传染 |
| `hook_consumer.go:233-269` handleStateChanged | withAgentLocked + 状态机转换 + 可能触发 DAG 回调 | 同上 |
| `hook_consumer.go:150-165` dispatchAfterTopic | switch 分发是 O(1)；但每个 handler 内部可能有 DB 调用 | 加 per-topic duration 直方图 |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `hook_consumer.go:138-140` | c/svc nil 静默 Approve |
| `hook_consumer.go:141-143` | decode 失败静默 Approve |
| `hook_consumer.go:150-165` | 未知 topic 静默丢弃（无 default case） |
| `hook_consumer.go:167-201` | decodeHookEvent 失败静默 return |
| `hook_consumer.go:244-253` | session fence 不匹配静默丢弃 |
| `hook_consumer.go:258-265` | idle hook 被 defer 无 timeout |
| `hook_consumer.go:128-132` | nil opt 静默 continue |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `hook_consumer.go:69-83` | 所有依赖 optional |
| `hook_consumer.go:136-148` After | 返回 (AfterDecision, error) 但 error 永远 nil |
| `hook_consumer.go:150-165` | 无 default case |
| `hook_consumer.go:203-222` | handleThreadStarted 依赖 withAgentLocked 的 not-found 行为 |
| `hook_consumer.go:258-265` | defer idle 无 timeout 保证 |

## 修复优先级

### P0（必须本周修）
1. **`hook_consumer.go:138-140` c/svc nil 静默 Approve**——hook consumer 是 agent 生命周期感知的唯一入口。nil 时静默 approve 意味着所有 hook 事件被丢弃但 agent 继续运行——编排器完全失去对 agent 状态的感知。改为返 error（让 hook 框架知道 consumer 不可用）。
2. **`hook_consumer.go:150-165` 未知 topic 无 default case**——新增 hook topic（如 `agent.turn.cancelled`）但未更新 switch 时事件静默丢失。这是 fail-fast 的基本要求：未知输入应报错而非静默。加 `default: c.logger.Warn(...)` 或 return error。
3. **`hook_consumer.go:258-265` shouldDeferIdleHook 无 timeout**——如果 launch commit 永远不来（launch 进程 hang / crash 但 exit event 也丢失），idle hook 被永久丢弃 → agent 永远停在 provisioning/recovering 状态。这是第47轮 exit_monitor P0（exit event 丢弃）的下游影响。加 timeout：defer 超 30s 后强制应用 idle。

### P1（本月）
4. `hook_consumer.go:141-143` decode 失败加 Warn + 返 error
5. `hook_consumer.go:69-83` Service 依赖改为非 optional
6. `hook_consumer.go:244-253` session fence Warn 加 rate limiter
7. `hook_consumer.go:136-148` After 加 per-event duration 监控

### P2（下个 sprint）
8. `hook_consumer.go:167-201` decodeHookEvent 失败区分「解码错」vs「kind 不匹配」
9. `hook_consumer.go:128-132` nil opt 加 Debug 日志
10. `hook_consumer.go:203-222` handleThreadStarted 文档化 withAgentLocked not-found 行为

## 边界条件

1. **`hook_consumer.go:244-253` session fence 是项目正面案例**：P22 P4 §121/§282 引入的 session-identity fence 防止 stale event 污染当前 session 状态。注释明确引用 plan 编号，Warn 日志带完整上下文（event_session_id / current_session_id）。这是「防止 race condition 导致状态错乱」的良好实践。
2. **`hook_consumer.go:226-231` shouldDeferIdleHook 的设计意图**：launch/recover 流程是 idle 状态的「single writer」——避免 hook 和 launch commit 同时写 idle 导致 double-fire。这是正确的并发设计。但缺少 timeout 让它变成「如果 writer 永远不来，状态永远不更新」的 liveness 问题。
3. **`hook_consumer.go:69-83` 全 optional 依赖的设计取舍**：fx 的 `optional:"true"` 让 hookConsumer 在任何 fx graph 中都能构造（即使缺少 DAG store、notify tap 等）。这是为了测试和 standalone 模式的灵活性。但生产环境中关键依赖缺失应 fail-fast。建议：Service 改为非 optional；其他依赖保持 optional 但在 After 入口检查关键依赖是否到位。
4. **`hook_consumer.go:136-148` After 的 error 返回值**：当前 error 永远 nil——所有错误都被内部消化（Warn 日志）。这意味着 hook 框架永远认为 consumer 成功。如果 hook 框架有 retry 机制（error 时重试），当前设计让 retry 永远不触发。建议：关键错误（decode 失败、svc nil）返 error 让框架重试或告警。
5. **`hook_consumer.go:150-165` dispatchAfterTopic 的 switch 无 default**：Go 的 switch 无 default 时未匹配的 case 静默跳过。这是 Go 语言特性，但在事件驱动系统中是危险的——新增 topic 时编译器不会报错（不像 enum exhaustive check）。建议加 linter 规则或 archtest 确保所有 `orchestrationHookTopics`（hook_subscription.go:13-20）都在 switch 中有对应 case。
6. **hookConsumer 整体是项目事件驱动架构的核心**：所有 agent 生命周期事件（start/state/turn/exit）都通过 After → dispatchAfterTopic → handle* 路径处理。这是 single-consumer 模式——所有事件串行处理。如果某个 handler 阻塞（如 DAG fallback 的 DB 调用），后续事件排队。建议加 event queue depth 监控 + 慢 handler 告警。

---

**本轮总结**：发现 3 个 P0 问题：①hook consumer nil 时静默 approve 让编排器失去 agent 感知；②未知 topic 无 default case 让新增事件静默丢失；③shouldDeferIdleHook 无 timeout 让 agent 可能永远停在 provisioning。session fence 是正面案例。hookConsumer 是项目事件驱动核心，single-consumer 模式需要 event queue depth 监控。

**累计进度**：50 轮完成（里程碑！）。cron `fd4b4728` 继续推进。

---

## 第50轮里程碑总结

**审查覆盖：**
- 已完成：第27-50轮，共24轮
- 覆盖文件：~80+ 个生产代码文件
- 累计 P0 问题：**53 个**

**P0 问题 Top 10 高危文件：**
1. `tool_edit_lock.go` — 3 个 P0（死锁 + 自死锁 + panic 不释放）
2. `hook_consumer.go` — 3 个 P0（nil approve + 无 default + 无 timeout）
3. `exit_monitor.go` — 2 个 P0（event 丢弃 + cmd.Wait 无超时）
4. `tx.go` — 2 个 P0（rollback 吞错 + ReadOnly 退化）
5. `store_lease.go` — 1 个 P0（renew 静默 lease-lost → 双 worker）
6. `sharedfile/store.go` — 2 个 P0（双写无事务 + delete 部分失败）
7. `dag_dispatch.go` — 1 个 P0（assign+enqueue 非原子）
8. `transport.go` — 1 个 P0（respondToServerRequest 无超时）
9. `config.go` — 2 个 P0（resolveProjectRoot 空 + env 静默 fallback）
10. `retry.go` — 2 个 P0（MaxAttempts=0 fn 不执行 + normalize 静默修正）

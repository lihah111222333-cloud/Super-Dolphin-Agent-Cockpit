# 第 53 轮审查结论

## 审查范围

- `internal/sidecar/orch/orchestration/dag_turn_completed_subscriber.go`（RegisterDAGTurnCompletedSubscriber、handleDAGTurnCompleted、advanceNodeForTurnCompleted、advanceNodeDoneForSuccess、advanceNodeDone、advanceNodeFailed、advanceNodeFailedWithReason、materializeSharedfileAfterClaim、claimNodeOutputMaterialization、stopSpawnedAgentForSubscriber）

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `dag_turn_completed_subscriber.go:83-121` handleDAGTurnCompleted | 静默 | 整个函数 void；lookup 失败 / deps 未 wired 只 Warn + metrics + return | turn 完成后 DAG 节点不推进 → 下游节点永远不被触发 → DAG stuck | 改为 error 返回 + 重试机制 |
| `dag_turn_completed_subscriber.go:89-93` | 静默 | `threadID == ""` 时 metrics + return | 空 threadID 是 hook payload 缺字段——caller bug 被静默吞掉 | 加 Warn 日志 |
| `dag_turn_completed_subscriber.go:110-113` | 弱契约 | N>1 nodes 同 threadID 时 Warn 但继续处理所有 | 多节点共享 threadID 是数据 bug（spawning_thread_id 应唯一），但被容忍 | 加 metrics + 考虑只处理第一个 |
| `dag_turn_completed_subscriber.go:227-254` advanceNodeFailedWithReason | 静默/bug | line 248-251 `ErrNoRows` 分支 Debug + return false；但 line 252 `logger.Warn` 在 switch 外——**所有 case 都会执行 line 252** | 这是一个 bug：ErrNoRows 分支（idempotent skip）也会打 Warn "fail node failed"。Warn 日志噪声 | 把 line 252 移到 `default:` 分支内 |
| `dag_turn_completed_subscriber.go:64-81` RegisterDAGTurnCompletedSubscriber | 协程延迟 | `bus.ResilientSubscribe` 注册同步 handler；每个 TurnCompleted event 同步调 handleDAGTurnCompleted | 如果 handleDAGTurnCompleted 慢（DB 查询 + sharedfile 写），event bus 被阻塞 | 改为异步 dispatch（goroutine pool）或 buffered channel |
| `dag_turn_completed_subscriber.go:120` stopSpawnedAgentForSubscriber | 静默 | turn 完成后停止 spawned agent；失败只 Warn | agent 不停止 → 资源泄漏（进程继续跑） | 加重试；或 exit_monitor 兜底 |
| `dag_turn_completed_subscriber.go:301-332` claimNodeOutputMaterialization | 弱契约 | `flow.(nodeOutputMaterializationClaimer)` 类型断言失败时 Warn + fail node | 类型断言失败意味着 FlowStore 实现不完整——这是配置 bug | 改为启动期校验（fx.Invoke 检查接口实现） |
| `dag_turn_completed_subscriber.go:256-295` materializeSharedfileAfterClaim | 复杂 | 多步骤：check exists → claim → write；任一步失败走 handleMaterializationFailure | 如果 claim 成功但 write 失败（line 290-293），claim 已消耗但 sharedfile 未写入 → 数据不一致 | claim + write 应在事务中；或 write 失败时 unclaim |
| `dag_turn_completed_subscriber.go:22` completeNodeResultCap | 弱契约 | 4KB 硬编码 cap | 大 agent 输出被截断但 caller 不知道 | 截断时加 Warn 日志 + 标记 truncated |

## 协程延迟定位方案

| 位置 | 延迟风险点 | 定位方案 |
| --- | --- | --- |
| `dag_turn_completed_subscriber.go:64-81` | 同步 handler 阻塞 event bus | 加 per-event duration 监控；> 500ms 打 Warn |
| `dag_turn_completed_subscriber.go:99-103` | LookupNodesBySpawningThread DB 查询 | 加 query duration |
| `dag_turn_completed_subscriber.go:182-201` advanceNodeDone | CompleteNodeAndScheduleDownstream 可能涉及多个下游节点 enqueue | 加 cascade count + duration |
| `dag_turn_completed_subscriber.go:256-295` materializeSharedfileAfterClaim | check exists + claim + write 三步串行 | 加分步 duration |
| `dag_turn_completed_subscriber.go:334-347` stopSpawnedAgentForSubscriber | StopSpawnedAgent 可能涉及进程 Kill + 等待退出 | 加 stop duration |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `dag_turn_completed_subscriber.go:89-93` | 空 threadID metrics + return |
| `dag_turn_completed_subscriber.go:94-98` | deps 未 wired Warn + return |
| `dag_turn_completed_subscriber.go:100-103` | lookup 失败 Warn + return |
| `dag_turn_completed_subscriber.go:120` | stop agent 失败 Warn |
| `dag_turn_completed_subscriber.go:198` | complete node 失败 Warn |
| `dag_turn_completed_subscriber.go:252` | fail node 失败 Warn（且 bug：ErrNoRows 也打） |
| `dag_turn_completed_subscriber.go:330` | claim 失败 Warn |
| `dag_turn_completed_subscriber.go:340-341` | deps 未 wired Debug + skip |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `dag_turn_completed_subscriber.go:83-121` | void 返回值 |
| `dag_turn_completed_subscriber.go:110-113` | N>1 nodes 容忍 |
| `dag_turn_completed_subscriber.go:256-295` | claim + write 非原子 |
| `dag_turn_completed_subscriber.go:301-332` | 类型断言决定功能可用性 |
| `dag_turn_completed_subscriber.go:22` | 4KB cap 硬编码 |
| `dag_turn_completed_subscriber.go:64-81` | 同步 handler 阻塞 bus |

## 修复优先级

### P0（必须本周修）
1. **`dag_turn_completed_subscriber.go:248-252` advanceNodeFailedWithReason 日志 bug**——ErrNoRows 分支（idempotent skip）也会执行 line 252 的 Warn "fail node failed"。这不是静默问题而是**代码 bug**：switch 的 `case errors.Is(...)` 分支没有 `return false`，fall-through 到 switch 外的 Warn。修复：在 line 250 后加 `return false`。
2. **`dag_turn_completed_subscriber.go:256-295` claim + write 非原子**——claim 成功（line 287）但 sharedfile write 失败（line 290-293）时，claim 已消耗但文件未写入。下次 turn 完成时 claim 被 fence 拒绝（已 claimed）→ 节点永远无法完成 sharedfile 写入 → DAG stuck。改为：write 失败时 unclaim（回滚 claim）。

### P1（本月）
3. `dag_turn_completed_subscriber.go:64-81` 同步 handler 改异步 dispatch
4. `dag_turn_completed_subscriber.go:83-121` 加 per-event duration 监控
5. `dag_turn_completed_subscriber.go:89-93` 空 threadID 加 Warn
6. `dag_turn_completed_subscriber.go:301-332` 类型断言改启动期校验
7. `dag_turn_completed_subscriber.go:120` stop agent 加重试

### P2（下个 sprint）
8. `dag_turn_completed_subscriber.go:22` completeNodeResultCap 截断加 Warn
9. `dag_turn_completed_subscriber.go:110-113` N>1 nodes 评估是否只处理第一个
10. `dag_turn_completed_subscriber.go:83-121` 改 error 返回 + 重试

## 边界条件

1. **`dag_turn_completed_subscriber.go:248-252` 是一个真实的代码 bug**：Go 的 switch 语句中，`case` 分支不会自动 fall-through（与 C 不同），但 switch 外的代码会在任何分支执行后继续执行。当前代码结构：
   ```go
   switch {
   case err == nil: ... return true
   case errors.Is(err, pgx.ErrNoRows): ... // 没有 return！
   }
   logger.Warn("fail node failed", ...) // 所有非 nil-err 分支都会执行到这里
   return false
   ```
   ErrNoRows 分支缺少 `return false`，导致 idempotent skip 也打 Warn。这是 P0 因为它产生大量虚假告警，掩盖真正的 fail 错误。

2. **`dag_turn_completed_subscriber.go:256-295` materializeSharedfileAfterClaim 的 claim-then-write 模式**：这是 optimistic locking 的变体——先 claim（标记「我在写」）再 write。如果 write 失败，claim 不回滚 → 节点被锁死。正确模式应该是：①claim → write → 成功；②claim → write 失败 → unclaim → 让下次 turn 重试。或者用 2PC（prepare → commit）。

3. **`dag_turn_completed_subscriber.go:64-81` 同步 handler 的性能影响**：`bus.ResilientSubscribe` 注册的 handler 在 event bus 的 dispatch goroutine 中同步执行。如果 handleDAGTurnCompleted 耗时 500ms（DB 查询 + sharedfile 写），event bus 在这 500ms 内无法 dispatch 其他 TurnCompleted 事件 → 所有 agent 的 turn 完成处理被串行化。建议改为 buffered channel + worker pool。

4. **`dag_turn_completed_subscriber.go:130-147` advanceNodeForTurnCompleted 的 success/failure 分流**：`ev.Success` 决定走 done 还是 failed 路径。这是合理的——但 `ev.Success == true && ev.Error != ""` 的情况（成功但有 warning）未被处理。当前走 done 路径忽略 Error 字段。建议加 Debug 日志记录 success-with-error 情况。

5. **`dag_turn_completed_subscriber.go:174-201` advanceNodeDone 的 fence 设计是正面案例**：`CompleteNodeAndScheduleDownstream` 返回 `pgx.ErrNoRows` 时视为 idempotent skip（节点已被其他 subscriber 完成）。这是 exactly-once 语义的正确实现——多个 subscriber 可能同时处理同一 turn 完成事件，fence 保证只有一个成功。

6. **`dag_turn_completed_subscriber.go:334-347` stopSpawnedAgentForSubscriber 的设计意图**：turn 完成后停止 spawned agent 是资源回收。但如果 stop 失败（进程 hang），agent 继续跑 → 资源泄漏。exit_monitor（第47轮）应该是最终兜底——但如果 exit_monitor 也有 P0 问题（event 丢弃），两层兜底都失效。建议加 periodic reaper 作为第三层防御。

---

**本轮总结**：发现 2 个 P0 问题：①advanceNodeFailedWithReason 缺少 `return false` 导致 ErrNoRows 也打 Warn（代码 bug）；②materializeSharedfileAfterClaim claim+write 非原子导致 write 失败时节点被锁死。`advanceNodeDone` 的 fence 设计是 exactly-once 正面案例。同步 handler 阻塞 event bus 是性能瓶颈需改异步。

**累计进度**：53 轮完成。cron `fd4b4728` 继续推进。

---

## 第53轮后累计状态

- 已完成：第27-53轮，共27轮
- 累计 P0 问题：**59 个**
- 覆盖深度：orchestration 核心路径已基本覆盖（hook_consumer → runtime → exit_monitor → dag_dispatch → node_router → node_executor_dispatch → dag_turn_completed_subscriber）
- 下一轮计划：`orchestration/factory.go`（service 构造）或 `orchestration/launcher.go`（agent 启动器）

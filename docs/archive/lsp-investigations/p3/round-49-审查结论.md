# 第 49 轮审查结论

## 审查范围

- `cmd/mcp-orch/orchestration/dag_dispatch.go`（DispatchNode、normalizeDispatchInputs、findDispatchTarget、ensureDispatchEligible、assignAndPersist、enqueueManualDispatchWakeup）
- `cmd/mcp-orch/orchestration/hook_consumer_dag_fallback.go`（runThreadStoppedDAGFallback、failThreadStoppedFallbackNode、invokeThreadStoppedFallbackLifecycleHook、failedNodeForLifecycle、isDAGFallbackFailEligibleStatus）

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `dag_dispatch.go:57-63` DispatchNode | 数据一致性 | 先 `assignAndPersist`（DB 写入 assigned_to）→ 后 `enqueueManualDispatchWakeup`（DB 写入 wakeup） | assign 成功 + enqueue 失败 → 节点已 assign 但无 wakeup → 节点永远不被 dispatch（stuck） | 包在事务中；或 enqueue 失败时回滚 assign |
| `hook_consumer_dag_fallback.go:13-40` runThreadStoppedDAGFallback | 静默 | 整个函数 void；lookup 失败只 Warn + metrics + return | thread 停止后 DAG 节点应被标记 failed；如果 lookup 失败，节点永远停在 running 状态 | 改为 `error` 返回让 caller 决定是否重试 |
| `hook_consumer_dag_fallback.go:15-17` | 静默 | `threadID == ""` 时静默 return | 空 threadID 是 caller bug（hook payload 缺字段），被静默吞掉 | 至少 Warn 日志 |
| `hook_consumer_dag_fallback.go:18-22` | 静默 | `lookup == nil \|\| flow == nil` 时静默 return | 依赖未注入是配置 bug，被静默吞掉 | 改 panic 或 Warn |
| `hook_consumer_dag_fallback.go:42-62` failThreadStoppedFallbackNode | 静默 | `FailNodeAndCancelDownstream` 失败只 Warn + metrics + return | 节点 fail 操作失败 → 节点仍在 running → 下游永远不被触发 | 加重试逻辑；或返 error 让 caller 决定 |
| `hook_consumer_dag_fallback.go:64-71` invokeTerminalFailureHooksForTaskNode | 静默 | `router != nil` 时调用；router nil 时静默跳过 | lifecycle hook 不触发 → 通知/告警不发送 → 运维不知道节点失败 | router nil 时 Warn |
| `hook_consumer_dag_fallback.go:73-91` failedNodeForLifecycle | 兜底 | result nil 时用 original；result.Node nil 时用 original；各字段空时 fallback original | 多层 fallback 让最终 node 状态不确定——可能混合 original 和 result 的字段 | 文档化 merge 语义；或改为 strict（result 必须完整） |
| `hook_consumer_dag_fallback.go:93-100` isDAGFallbackFailEligibleStatus | 弱契约 | 黑名单模式（列出不可 fail 的状态）；`default: return true` | 新增状态（如 "retrying"）默认可 fail——可能不是预期行为 | 改为白名单（列出可 fail 的状态）；unknown 状态返 false + Warn |
| `dag_dispatch.go:92-103` findDispatchTarget | 性能 | `ListRunNodes` 返回所有节点后线性扫描找 nodeKey | 大 DAG（100+ 节点）每次 dispatch 都全量 list | 改为 `GetRunNode(dagKey, nodeKey, runID)` 单点查询 |

## 协程延迟定位方案

| 位置 | 延迟风险点 | 定位方案 |
| --- | --- | --- |
| `dag_dispatch.go:40-73` DispatchNode | 同步路径：normalize → findTarget（DB list）→ ensureEligible → validateCWD → assign（DB write）→ enqueue（DB write） | 加分阶段 duration 日志；总 > 500ms 打 Warn |
| `dag_dispatch.go:92-103` findDispatchTarget | ListRunNodes 全量 list + 线性扫描 | 改单点查询；或加 limit + 索引 |
| `hook_consumer_dag_fallback.go:34-39` for loop | 多节点串行 fail；每个 fail 是 DB roundtrip | 加 per-node duration；总 > 1s 打 Warn |
| `hook_consumer_dag_fallback.go:47-52` FailNodeAndCancelDownstream | 可能涉及级联 cancel 多个下游节点 | 加 cascade count + duration 日志 |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `hook_consumer_dag_fallback.go:15-17` | 空 threadID 静默 return |
| `hook_consumer_dag_fallback.go:18-22` | lookup/flow nil 静默 return |
| `hook_consumer_dag_fallback.go:24-29` | lookup 失败 Warn + return（不重试） |
| `hook_consumer_dag_fallback.go:53-58` | fail 失败 Warn + return（不重试） |
| `hook_consumer_dag_fallback.go:65-71` | router nil 静默跳过 lifecycle hook |
| `hook_consumer_dag_fallback.go:93-100` | unknown 状态默认可 fail |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `dag_dispatch.go:57-63` | assign + enqueue 非原子 |
| `dag_dispatch.go:92-103` | findDispatchTarget 全量 list 后线性扫描 |
| `hook_consumer_dag_fallback.go:73-91` | failedNodeForLifecycle 多层 merge 语义 |
| `hook_consumer_dag_fallback.go:93-100` | 黑名单模式 vs 白名单 |
| `hook_consumer_dag_fallback.go:13-40` | void 返回值 |

## 修复优先级

### P0（必须本周修）
1. **`dag_dispatch.go:57-63` assign + enqueue 非原子**——assign 成功但 enqueue 失败时节点 stuck（已 assign 但无 wakeup 触发执行）。这是 DAG 调度正确性的根本问题。改为事务包装；或 enqueue 失败时回滚 assign（`UnassignNode`）。
2. **`hook_consumer_dag_fallback.go:93-100` isDAGFallbackFailEligibleStatus 黑名单模式**——新增状态默认可 fail 是危险的。如果未来加 "retrying" 或 "waiting_human" 状态，thread_stopped_fallback 会错误地把它们标记 failed。改为白名单（只列出 pending/ready/running/dispatching 等可 fail 状态）。

### P1（本月）
3. `hook_consumer_dag_fallback.go:13-40` 改为 error 返回
4. `hook_consumer_dag_fallback.go:15-22` 空 threadID / nil deps 加 Warn
5. `dag_dispatch.go:92-103` findDispatchTarget 改单点查询
6. `hook_consumer_dag_fallback.go:42-62` fail 失败加重试（至少 1 次）
7. `hook_consumer_dag_fallback.go:65-71` router nil 加 Warn

### P2（下个 sprint）
8. `hook_consumer_dag_fallback.go:73-91` failedNodeForLifecycle merge 语义文档化
9. `dag_dispatch.go:40-73` 加分阶段 duration 日志
10. `hook_consumer_dag_fallback.go:34-39` 多节点 fail 改并行（需评估 DB 压力）

## 边界条件

1. **`dag_dispatch.go` 整体是 fail-fast 正面案例**：`normalizeDispatchInputs` 校验所有必填字段（line 77-89）；`ensureDispatchEligible` 状态闸（line 106-113）；agent 节点 CWD 校验（line 52-56）。三层校验在 DB 写入前完成——这是「先校验后写入」的良好实践。唯一缺陷是 assign+enqueue 非原子（P0）。
2. **`dag_dispatch.go:52-56` agent 节点 CWD 校验是正面案例**：在 enqueue 前校验 `node.config.exec.cwd` 存在——避免 wakeup 被 dispatch 后 executor 因缺 CWD 失败。这是「fail-fast at enqueue time, not at execution time」的良好实践。
3. **`hook_consumer_dag_fallback.go` 的设计意图**：当 agent thread 意外停止（crash / kill）时，DAG 中由该 thread 执行的节点应被标记 failed + 下游 cancel。这是 DAG 容错的关键路径。但当前实现全部 void + Warn 日志——如果 fail 操作本身失败，节点永远 stuck。建议加 retry + 最终 alert。
4. **`hook_consumer_dag_fallback.go:93-100` 黑名单 vs 白名单的设计取舍**：黑名单（列出不可 fail 的终态）的优势是「新增中间态默认可 fail」——这在大多数情况下是安全的（中间态的节点确实应该被 fail）。但 "awaiting_verify" 这种「等待人工确认」状态也在黑名单中——说明作者已经意识到某些非终态也不应被 fallback fail。问题是：未来新增的「等待」类状态（如 "waiting_human_approval"）如果忘记加入黑名单，会被错误 fail。**白名单更安全**。
5. **`dag_dispatch.go:131-149` enqueueManualDispatchWakeup 的幂等性设计**：`IdempotencyKey` 由 `dagKey+nodeKey+runID+assignedTo` 组成——同一 assignee 多次 dispatch 被 ON CONFLICT 去重。换 assignee 得到新 row。这是良好的幂等性设计——防止重复 dispatch 创建多个 wakeup。
6. **`hook_consumer_dag_fallback.go:34-39` 多节点串行 fail 的性能影响**：一个 thread 可能 spawn 多个 DAG 节点。thread 停止时需要 fail 所有这些节点。当前串行处理——如果有 10 个节点，每个 fail 需要 50ms DB roundtrip，总计 500ms。在 hook consumer 同步路径上这会阻塞后续 hook 处理。建议改为并行（但需评估 DB 连接池压力）或异步队列。

---

**本轮总结**：发现 2 个 P0 问题：①DispatchNode assign+enqueue 非原子导致节点可能 stuck；②isDAGFallbackFailEligibleStatus 黑名单模式让新增状态默认可 fail 是危险的。`dag_dispatch.go` 的三层校验（normalize→eligible→CWD）是 fail-fast 正面案例。`enqueueManualDispatchWakeup` 的 IdempotencyKey 是良好的幂等性设计。hook_consumer_dag_fallback 整体 void + Warn 模式让 DAG 容错路径缺乏可靠性保证。

**累计进度**：49 轮完成。cron `fd4b4728` 继续推进。

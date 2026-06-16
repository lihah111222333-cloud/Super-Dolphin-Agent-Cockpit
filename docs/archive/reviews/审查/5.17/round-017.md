# Round 017 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 06:03:46 KST
- 结束：2026-05-17 06:06:30 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 mcp-orch DAG runtime 的状态量化、wakeup lease、node spawn 反查和下游调度，重点看 pending/ready/running/done、dispatching/sent、attempt_count、lease_expires_at 等状态是否会互相脱钩。

- `cmd/mcp-orch/store/taskdag/store.go`
- `cmd/mcp-orch/store/taskdag/store_lease.go`
- `cmd/mcp-orch/store/taskdag/store_wakeup.go`
- `cmd/mcp-orch/store/taskdag/store_complete_downstream.go`
- `cmd/mcp-orch/store/taskdag/store_node_spawn.go`
- `cmd/mcp-orch/sql/queries/task_dag_worker_lease.sql`
- `cmd/mcp-orch/sql/queries/task_dag_wakeup_dispatch.sql`
- `cmd/mcp-orch/sql/queries/task_dag_wakeup_query.sql`
- `cmd/mcp-orch/sql/queries/task_dag_node_runtime.sql`
- `cmd/mcp-orch/sql/queries/task_dag_node_spawning_thread.sql`
- `cmd/mcp-orch/sql/queries/task_dag_node_write.sql`
- `cmd/mcp-orch/orchestration/wakeup_dispatcher.go`
- `cmd/mcp-orch/orchestration/wakeup_reclaim.go`
- `cmd/mcp-orch/orchestration/node_executor_dispatch.go`
- `cmd/mcp-orch/orchestration/node_router.go`
- `cmd/mcp-orch/orchestration/dag_turn_completed_subscriber.go`
- `cmd/mcp-orch/orchestration/dag.go`
- 相关测试：`store_update_running_status_test.go`、`store_fencing_test.go`、`store_complete_downstream_test.go`、`store_node_spawn_test.go`、`dispatch_agent_running_test.go`

## Findings

1. **[critical] agent launch 成功后 ready→running 写失败被吞，wakeup 仍会标记 sent，导致 DAG 节点与真实子线程脱钩**
   - 证据：`dispatchAgent()` 在 child agent launch 成功后调用 `advanceAgentNodeToRunning()`，但该函数对通用 DB 错误只记录 warn/metric 并返回 false，不把错误传回 dispatcher（`cmd/mcp-orch/orchestration/node_router.go:372-440`）。测试明确锁定通用 DB 错误“不传播回 RouteByWakeup”（`cmd/mcp-orch/orchestration/dispatch_agent_running_test.go:102-139`）。dispatcher 对非 failed outcome 默认调用 `markLaunched()`，把 wakeup 标记为 `sent`（`cmd/mcp-orch/orchestration/node_executor_dispatch.go:302-341`、`cmd/mcp-orch/orchestration/wakeup_dispatcher.go:278-293`）。
   - 风险：真实 agent 已启动，但节点仍可能停在 `ready`，`active_wakeup_id` / `active_turn_id` 也未按 running 路径建立。后续 `TurnCompleted` 依赖 `spawning_thread_id` 反查节点；如果 running 写失败同时伴随 spawn 写回失败或状态不一致，DAG 可能永远不完成，而 wakeup 已 `sent` 不会被 reclaimer 回收。
   - 建议：ready→running 写失败应让 `RouteByWakeup` 返回 framework error，使 dispatcher 保持 wakeup 可 retry；或者引入“launched_but_state_write_failed”补偿队列，直到 node row 与 wakeup fence 都落盘后才 MarkSent。

2. **[major] `RecordNodeSpawn` 不限制节点状态，迟到的 spawn 写回可以覆盖终态节点的 `spawning_thread_id`**
   - 证据：`UpdateTaskDagNodeSpawningThread` 的注释明确 “WHERE 不限定 status”，SQL 仅按 `dag_key/node_key/run_id` 更新（`cmd/mcp-orch/sql/queries/task_dag_node_spawning_thread.sql:16-43`）。store 入口只校验 dag/node/thread/run 必填，不校验当前 status（`cmd/mcp-orch/store/taskdag/store_node_spawn.go:46-70`）。现有测试只覆盖输入必填、重试覆盖和事件环形截断，没有终态拒绝用例（`cmd/mcp-orch/store/taskdag/store_node_spawn_test.go:219-330`）。
   - 风险：如果某个旧 launch 在节点已经 done/failed/cancelled 后才完成并回写 thread id，它会把终态节点的反查指针改到新线程。之后新线程的 TurnCompleted 会被 subscriber 识别成这个已终态节点，产生脏数据告警、错误 stop，或让调试人员误判最终执行者。
   - 建议：`RecordNodeSpawn` 至少限制 status in (`ready`,`running`,`retrying`) 或要求 `active_wakeup_id`/wakeup fence 匹配；终态节点只允许追加审计事件，不允许覆盖当前 `spawning_thread_id`。

3. **[major] `task_dispatch_node` 的 assign 和 enqueue 非事务化，失败会留下“已指派但无 wakeup”的 ready 节点**
   - 证据：`DispatchNode()` 先 list 检查状态，再 `AssignNode()`，再 `EnqueueWakeup()`；代码注释明确“不在事务里跑 list + assign + enqueue”（`cmd/mcp-orch/orchestration/dag_dispatch.go:37-56`）。`AssignTaskDagNode` 会在 `pending/ready` 状态写 `assigned_to`（`cmd/mcp-orch/sql/queries/task_dag_node_write.sql:36-46`），`enqueueManualDispatchWakeup()` 失败时直接返回错误，不回滚前面的 assigned_to（`cmd/mcp-orch/orchestration/dag_dispatch.go:129-164`）。
   - 风险：手工接管未指派 ready 节点时，如果 enqueue 因 DB/唯一键/网络失败，节点看起来已经有 assignee，但没有 pending wakeup。后续自动调度不会再次为这个节点入队，运维界面也可能以为它已被派发。
   - 建议：把 assign + enqueue 放入同一 store 事务，或在 enqueue 失败时回滚 assigned_to / 标记 dispatch_failed；至少提供 repair job 扫描 ready+assigned_to 非空但无 pending/sent wakeup 的节点。

4. **[moderate] worker lease acquire 不校验空 owner/target，可能出现空 owner 抢占或续租**
   - 证据：`AcquireWorkerLease()` / `RenewWorkerLease()` 直接把 `TargetAgentID`、`OwnerID` 传给 SQL，没有 trim/required 校验（`cmd/mcp-orch/store/taskdag/store_lease.go:9-35`）。SQL 以 `target_agent_id` 为唯一冲突键，允许同 owner 续租或过期抢占（`cmd/mcp-orch/sql/queries/task_dag_worker_lease.sql:1-15`）。
   - 风险：上层传空 owner 或空 target 时，会写入难以归属的 lease。多个调用方如果都使用空 owner，会互相续租同一 target，破坏“一个 agent 一个明确 worker owner”的量化约束，后续 release 也可能误删。
   - 建议：store 层 trim 并强制 target/owner 非空；把空值测试加入 `store_lease` 覆盖，避免依赖调用方自律。

5. **[moderate] wakeup reclaim 只回收 dispatching，不处理 sent 但长期 unbound 的 wakeup**
   - 证据：`ReclaimStaleDispatchingTaskDagWakeups` 只更新 `status='dispatching' AND lease_expires_at < NOW()`（`cmd/mcp-orch/sql/queries/task_dag_wakeup_query.sql:1-4`）。`ListSentUnboundTaskDagWakeups` 能列出 `status='sent' AND bound_turn_id IS NULL`（`cmd/mcp-orch/sql/queries/task_dag_wakeup_query.sql:6-10`），但 reclaimer 主循环只调用 `ReclaimStaleDispatchingWakeups()`（`cmd/mcp-orch/orchestration/wakeup_reclaim.go:100-118`）。
   - 风险：dispatcher MarkSent 成功后，如果后续 turn 绑定失败、agent 没真正启动、或启动后没有回传 turn，wakeup 会永久停在 sent/unbound。attempt_count 不再增加，lease 也不再参与回收，DAG 节点可能停在 running/ready 无后续推进。
   - 建议：增加 sent-unbound 超时监控和重试/失败策略；对于 DAG wakeup，可结合 `active_wakeup_id`、`spawning_thread_id`、`last_event_at` 判断是否需要重新入队或标记节点失败。

## 误报与已覆盖项

- `ClaimDueWakeups` 使用 `FOR UPDATE SKIP LOCKED` 且 Mark/Retry/Fail 都带完整 claim fence，已有测试覆盖 stale claim 不能提交（`cmd/mcp-orch/sql/queries/task_dag_wakeup_dispatch.sql:6-40`、`cmd/mcp-orch/store/taskdag/store_fencing_test.go:18-130`）。
- downstream 自动调度的 idempotency key 已包含 run_id，避免多 run 下同 dag/node 去重互相污染（`cmd/mcp-orch/store/taskdag/store_complete_downstream.go:298-305`）。
- `UpdateRunningNodeStatus` 已允许 pending/ready -> running 且拒绝 running/done 重写，这是有测试保护的（`cmd/mcp-orch/sql/queries/task_dag_node_runtime.sql:28-36`、`cmd/mcp-orch/store/taskdag/store_update_running_status_test.go:11-90`）。

## 验证

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/store/taskdag ./cmd/mcp-orch/orchestration -count=1
```

结果：Go guard、`internal/archtest`、`cmd/mcp-orch/store/taskdag` 与 `cmd/mcp-orch/orchestration` 通过。

## 下一轮建议

- Round 018 审查 DAG retry/fail-fast/smart-retry/replan 路径，重点看 attempt_count、MaxAttempts、failure_class 和 cascade 的量化语义是否一致。

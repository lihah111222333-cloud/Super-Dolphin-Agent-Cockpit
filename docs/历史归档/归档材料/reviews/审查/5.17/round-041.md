# Round 041 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 07:45:29 KST
- 结束：2026-05-17 07:55:12 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 taskdag wakeup claim/retry/fail/lease SQL、dispatcher 对 fence 的使用、worker lease 与 reclaim 行为，重点看量化 DAG wakeup 是否会丢失、误耗重试次数或长期卡在 sent/dispatching。

- `internal/sidecar/orch/store/sqlc/task_dag_wakeup_dispatch.sql.go`
- `internal/sidecar/orch/store/sqlc/task_dag_wakeup_query.sql.go`
- `internal/sidecar/orch/store/sqlc/task_dag_worker_lease.sql.go`
- `internal/sidecar/orch/store/taskdag/store_wakeup.go`
- `internal/sidecar/orch/store/taskdag/store_lease.go`
- `internal/sidecar/orch/store/taskdag/factory.go`
- `internal/sidecar/orch/store/taskdag/store_fencing_test.go`
- `internal/sidecar/orch/store/taskdag/store_wakeup_test.go`
- `internal/sidecar/orch/orchestration/wakeup_dispatcher.go`
- `internal/sidecar/orch/orchestration/wakeup_dispatcher_shard17_batch_test.go`

## Findings

1. **[major] attempt_count 在 claim 阶段递增，dispatcher 崩溃或 Tick-only 路径会消耗重试次数**
   - 证据：`ClaimDueTaskDagWakeups` 在 `pending -> dispatching` 时直接 `attempt_count = attempt_count + 1`（`internal/sidecar/orch/store/sqlc/task_dag_wakeup_dispatch.sql.go:33-57`）。`Tick()` 明确只 claim 并打日志，不启动 launcher，等待 reclaim 回收（`internal/sidecar/orch/orchestration/wakeup_dispatcher.go:449-495`）。测试覆盖 reclaim 后再次 claim 时 attempt_count 变 2（`internal/sidecar/orch/store/taskdag/store_fencing_test.go:48-74`）。
   - 风险：量化调度器重启、launcher nil 或处理前崩溃会消耗尝试次数，最终触发 retry exhaustion，即使 agent 从未真正执行。
   - 建议：attempt_count 在真正 dispatch/route 开始后递增，或增加 `dispatch_started_at` 与 `claim_count` 分离字段。

2. **[major] sent 且未绑定 turn 的 wakeup 没有 SQL 超时回收，只能查询列表**
   - 证据：store 仅提供 `ListSentUnboundWakeups()` 查询 `status='sent' AND bound_turn_id IS NULL`（`internal/sidecar/orch/store/sqlc/task_dag_wakeup_query.sql.go:94-140`），回收 SQL 只处理 `status='dispatching' AND lease_expires_at < NOW()`（`internal/sidecar/orch/store/sqlc/task_dag_wakeup_query.sql.go:142-154`）。
   - 风险：量化 agent 启动成功后，如果 turn binding 事件丢失，wakeup 永久停在 sent-unbound；不会回到 pending，也不会 fail 节点。
   - 建议：增加 sent-unbound aging/reclaimer，按 `sent_at` 超时转 retry/failed，并写 DAG node reason。

3. **[major] MarkSent/Retry/Fail 都要求 lease 未过期；长时间启动后成功提交会 fence miss 且无补偿**
   - 证据：`markTaskDagWakeupSent`、`retryTaskDagWakeup`、`failTaskDagWakeup` 都带 `lease_expires_at = $X AND lease_expires_at >= NOW()`（`internal/sidecar/orch/store/sqlc/task_dag_wakeup_dispatch.sql.go:139-180`、`internal/sidecar/orch/store/sqlc/task_dag_wakeup_dispatch.sql.go:203-214`）。dispatcher mark sent 失败只 warn，不回滚已启动 agent（`internal/sidecar/orch/orchestration/wakeup_dispatcher.go:278-293`）。
   - 风险：量化 agent 启动耗时超过 30s 默认 lease 时，本地已 launch，但 wakeup 仍可能被 reclaim 成 pending 并再次调度，造成重复 worker。
   - 建议：处理过程中续租，或在 launcher/route 前后按节点维度加执行锁；mark sent fence miss 后应停止刚启动的 agent。

4. **[moderate] ClaimDueWakeups store 层不校验 Limit/ClaimedBy，直接传 SQL LIMIT 与 claimed_by**
   - 证据：`ClaimDueWakeupsInput` 允许空 `ClaimedBy`、任意 `Limit`（`internal/sidecar/orch/store/taskdag/contract.go:499-503`），store 只解析 lease interval 后传给 SQL（`internal/sidecar/orch/store/taskdag/store_wakeup.go:27-38`）。dispatcher 配置会填默认值（`internal/sidecar/orch/orchestration/wakeup_dispatcher.go:64-73`），但 store 作为公共端口没有同等防护。
   - 风险：测试/其他调用者传 `Limit<=0` 可能导致 claim 0 行或 SQL 行为依赖数据库；空 claimed_by 会削弱 fence 可观测性。
   - 建议：store 层对 `Limit<=0` 和空 `ClaimedBy` 返回 validation error，或统一填默认。

5. **[moderate] worker lease owner/target 不做 trim/非空校验，空 key 也能参与抢占**
   - 证据：`AcquireWorkerLease`、`RenewWorkerLease` 只解析 interval，直接把 `TargetAgentID/OwnerID` 传 SQL（`internal/sidecar/orch/store/taskdag/store_lease.go:9-35`）。SQL 以 `target_agent_id` 做唯一冲突键，允许同 owner 续约或过期抢占（`internal/sidecar/orch/store/sqlc/task_dag_worker_lease.sql.go:14-23`）。
   - 风险：量化 worker lease 如果调用方传空 target/owner，多个逻辑 worker 会竞争同一空 key，导致错误串行或错误续租。
   - 建议：store lease 入口强制 trim 后非空；指标区分 acquire rows=0 的竞争失败。

## 误报与已覆盖项

- dispatching lease 过期后有 reclaim，可以阻止永久卡在 dispatching（`internal/sidecar/orch/store/sqlc/task_dag_wakeup_query.sql.go:142-154`、`internal/sidecar/orch/store/taskdag/store_fencing_test.go:48-74`）。
- MarkSent/Retry/Fail 已使用完整 claim fence，防止过期 worker 写入新 owner 的 wakeup（`internal/sidecar/orch/store/taskdag/store_fencing_test.go:103-248`）。
- finalized run 的 pending wakeup 不会再被 claim，避免已结束 run 继续派发（`internal/sidecar/orch/store/taskdag/store_wakeup_test.go:26-46`）。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/sidecar/orch/store/taskdag ./internal/sidecar/orch/orchestration -count=1
```

结果：通过。

## 下一轮建议

- Round 042 审查 NodeExecutorRouter、agent/automation executor 的 RouteByWakeup 执行边界与结果分类。

# Round 031 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 07:04:13 KST
- 结束：2026-05-17 07:19:42 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 DAG wakeup dispatcher、lease、retry、reclaim 与 recovery 的状态闭环，重点看 dispatching/sent/bound 的边界、lease 过期后是否能恢复、成功派发后 turn 绑定是否可靠。

- `cmd/mcp-orch/orchestration/wakeup_dispatcher.go`
- `cmd/mcp-orch/orchestration/wakeup_reclaim.go`
- `cmd/mcp-orch/orchestration/wakeup_reclaim_test.go`
- `cmd/mcp-orch/orchestration/recover.go`
- `cmd/mcp-orch/orchestration/recover_test.go`
- `cmd/mcp-orch/store/taskdag/store_wakeup.go`
- `cmd/mcp-orch/store/taskdag/factory.go`
- `cmd/mcp-orch/sql/queries/task_dag_wakeup_dispatch.sql`
- `cmd/mcp-orch/sql/queries/task_dag_wakeup_query.sql`

## Findings

1. **[critical] `sent` 但未绑定 turn 的 wakeup 没有回收机制，会让 DAG 节点永久等待**
   - 证据：成功 launch 后 dispatcher 只调用 `MarkWakeupSent()` 把 wakeup 改成 `sent`（`cmd/mcp-orch/orchestration/wakeup_dispatcher.go:278-293`）；`BindTaskDagWakeupTurn` 只在 status=`sent` 且 `bound_turn_id IS NULL` 时绑定（`cmd/mcp-orch/sql/queries/task_dag_wakeup_dispatch.sql:42-45`）。reclaimer 只回收 `status='dispatching'` 且 lease 过期的行（`cmd/mcp-orch/sql/queries/task_dag_wakeup_query.sql:1-4`），不会处理 sent-unbound。recovery 只 replay `status='sent'` 且 `bound_turn_id` 等于 active turn 的 wakeup（`cmd/mcp-orch/orchestration/recover.go:171-183`）。
   - 风险：agent 已启动但 turn bind 事件丢失、进程崩溃或持久化失败时，wakeup 会停在 sent/unbound。DAG 节点可能已经被路由器置 running，但后续 turn.completed 无法关联 wakeup，量化任务卡在运行中且不会被 retry/reclaim。
   - 建议：增加 sent-unbound reclaimer，超过 TTL 后按 node 状态决定重试、失败或重新绑定；同时对 MarkSent 后未绑定建立指标和告警。

2. **[major] MarkSent/Retry/Fail 的 fence miss 只打日志，缺少自愈或显式失败路径**
   - 证据：`MarkTaskDagWakeupSent`、`RetryTaskDagWakeup`、`FailTaskDagWakeup` 都要求原 claim fence 和 `lease_expires_at >= NOW()`（`cmd/mcp-orch/sql/queries/task_dag_wakeup_dispatch.sql:32-67`）。`markLaunched()` 对 MarkSent 错误只 warn 后返回（`cmd/mcp-orch/orchestration/wakeup_dispatcher.go:278-288`）；`retryWakeup()` 在 rows=0 时直接走 hard-cap fallback（`cmd/mcp-orch/orchestration/wakeup_dispatcher.go:335-355`），无法区分过期 fence 与 attempt hard cap。
   - 风险：慢启动或 DB 延迟导致 lease 过期时，实际 agent 可能已经启动，但 wakeup 状态未能 sent；reclaimer 又会把 dispatching 还原 pending，下一轮可能重复启动同一节点。
   - 建议：把 fence miss 分类为 `expired_lease`、`stolen_claim`、`hard_cap`；MarkSent fence miss 应检查 node active_turn/active_wakeup 或 launcher 返回的 turn id 后再决定是否重试。

3. **[major] reclaimer 只重置 claim 信息，不记录 lease 过期原因或次数**
   - 证据：`ReclaimStaleDispatchingTaskDagWakeups` 只把 status 改回 pending 并清空 claimed/lease 字段（`cmd/mcp-orch/sql/queries/task_dag_wakeup_query.sql:1-4`），没有更新 `last_error`，也不增加独立的 reclaim/lease timeout 计数。测试只断言 rows 数和 loop 存活（`cmd/mcp-orch/orchestration/wakeup_reclaim_test.go:59-127`）。
   - 风险：同一个量化节点若因 provider 慢启动、命令卡阻塞或 DB 抖动反复过期，会无限回到 pending 并增加 claim attempt，但 dashboard 很难区分“正常 retry”与“lease timeout”。这会掩盖重复执行风险。
   - 建议：reclaim 时写 `last_error='lease expired'`、`last_reclaimed_at`、`reclaim_count`；达到阈值后转 failed 并触发 DAG node fail。

4. **[major] `lease_expires_at < NOW()` 与代码注释的 `<= NOW()` 不一致，边界时刻会多卡一个 reclaim 周期**
   - 证据：reclaimer 注释写“`lease_expires_at <= NOW()` 的 wakeup 还原 pending”（`cmd/mcp-orch/orchestration/wakeup_reclaim.go:18-21`），SQL 实际使用 `lease_expires_at < NOW()`（`cmd/mcp-orch/sql/queries/task_dag_wakeup_query.sql:1-4`）。
   - 风险：边界很小，但默认 reclaim 周期与 lease 都是 30s（`cmd/mcp-orch/orchestration/wakeup_reclaim.go:31-45`、`cmd/mcp-orch/orchestration/wakeup_dispatcher.go:28-39`），精确等于边界的 wakeup 会等到下一轮。高频量化任务会放大延迟抖动。
   - 建议：统一注释和 SQL，优先改为 `<= NOW()`；同时测试覆盖等于过期时间的回收。

5. **[moderate] `BindTaskDagWakeupTurn` 不校验 target agent 或 run/node，错误 turn 可能绑定到同一 wakeup**
   - 证据：SQL 只按 `id`、`status='sent'`、`sent_at IS NOT NULL`、`bound_turn_id IS NULL` 更新（`cmd/mcp-orch/sql/queries/task_dag_wakeup_dispatch.sql:42-45`）；store 层 `bindWakeupTurnTx()` 也只传 TurnID 和 ID（`cmd/mcp-orch/store/taskdag/factory.go:72-89`）。
   - 风险：如果 launch/bind 事件乱序或错误地携带 wakeup id，可能把其他 agent/节点的 turn id 绑定到该 wakeup。后续 recovery 和 turn.completed 会基于错误绑定继续推进节点状态。
   - 建议：bind 时带上 `target_agent_id`、`dag_key`、`node_key`、`run_id` 或 launch token；至少在 service 层读取 wakeup 后做一致性校验。

## 误报与已覆盖项

- `dispatching` lease 过期的基本回收存在，并且 runner 接入为独立 ticker，dispatcher 失败不会让 reclaim loop 退出（`cmd/mcp-orch/orchestration/wakeup_reclaim.go:74-117`）。
- 单条 wakeup 处理失败不会中断同 batch 其余项，`handleClaimed()` 的子步骤只记录日志并继续（`cmd/mcp-orch/orchestration/wakeup_dispatcher.go:237-255`）。
- recovery 会跳过已经被 reclaim 回 pending 的 wakeup，测试覆盖该语义（`cmd/mcp-orch/orchestration/recover_test.go:260-293`）。

## 验证

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration ./cmd/mcp-orch/store/taskdag -count=1
```

结果：通过。

## 下一轮建议

- Round 032 审查 smart retry 与 node config patch 的事务/并发语义，重点看 retry prompt 改写、config CAS、策略分类和失败后是否污染节点配置。

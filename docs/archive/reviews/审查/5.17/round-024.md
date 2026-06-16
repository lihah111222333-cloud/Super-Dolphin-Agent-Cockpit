# Round 024 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 06:36:04 KST
- 结束：2026-05-17 06:36:50 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 DAG events / spawn history / final output helper / dashboard 与 sharedfile 删除保护，重点看审计事件是否足以重建 agent spawn、retry、replan 和最终产物链路。

- `cmd/mcp-orch/store/taskdag/store_node_spawn.go`
- `cmd/mcp-orch/sql/queries/task_dag_node_spawning_thread.sql`
- `cmd/mcp-orch/sql/queries/task_dag_run.sql`
- `cmd/mcp-orch/store/taskdag/store_node_spawn_test.go`
- `cmd/mcp-orch/orchestration/nodeexec/executor_agent.go`
- `cmd/mcp-orch/orchestration/node_router.go`
- `internal/contract/orchestration.go`
- `internal/contract/dag_final_output_test.go`
- `internal/module/dashboard/ui_page.go`
- `internal/module/memory/ui_rpc.go`

## Findings

1. **[critical] `task_dag_runs.events` 只记录“重试覆盖 spawning_thread_id”，不是完整 DAG 审计事件流**
   - 证据：`RecordNodeSpawn()` 注释和实现只在旧 `spawning_thread_id` 非空且与新 thread 不同时 append `node_spawn`（`cmd/mcp-orch/store/taskdag/store_node_spawn.go:20-31`、`cmd/mcp-orch/store/taskdag/store_node_spawn.go:90-98`）。测试明确首次 spawn 不写 events（`cmd/mcp-orch/store/taskdag/store_node_spawn_test.go:32-63`）。状态变化、dispatch、turn bound、retry、replan planner 启动、finalize 等路径没有统一 append run events。
   - 风险：`events` 字段看起来像 Temporal-style replay / audit log，但实际上只是 spawn 重试历史的一小段。任何 UI/agent 如果尝试从 `task_get_run.run.events` 重建 DAG 执行轨迹，会漏掉首次启动、节点状态推进、失败原因、replan 决策和最终产物生成，形成错误审计结论。
   - 建议：重命名或文档明确 `events` 现阶段只承载 `node_spawn` retry history；若要做审计，新增 append-only run event 表，覆盖 node_status_changed、wakeup_sent、turn_bound、retry_scheduled、replan_launched、finalized、final_output_promoted 等事件。

2. **[major] 首次 spawn 不写 `node_spawn` 事件，run events 无法列出每个节点实际启动的第一个 child thread**
   - 证据：`recordNodeSpawnTx()` 在 `PreviousThreadID == ""` 时直接返回，不 append event（`cmd/mcp-orch/store/taskdag/store_node_spawn.go:90-96`）。测试 `TestRecordNodeSpawn_FirstSpawn_WritesFieldNoEvents` 锁定 run.events 为空（`cmd/mcp-orch/store/taskdag/store_node_spawn_test.go:32-63`）。
   - 风险：`task_get_run.nodes[].spawning_thread_id` 能看到每个节点当前最近一次 child thread，但无法从 events 看到最初 thread，也无法区分“首次执行成功但未重试”和“历史事件被截断”。如果后续 thread 被覆盖，首次 thread 只会作为第二次 spawn 的 `prev_thread_id` 出现；没有重试的节点则永远不在 events 中。
   - 建议：首次 spawn 也 append `node_spawn`，允许 `prev_thread_id` 为空；或者新增 `node_started` event，把当前 thread_id、wakeup_id、run_id、node_key 一起落盘。

3. **[major] run events 固定只保留最近 50 条，高频重试会丢失早期审计证据**
   - 证据：`AppendTaskDagRunEvent` SQL 在 append 后只保留最近 50 条事件（`cmd/mcp-orch/sql/queries/task_dag_run.sql:208-224`）。测试 `TestRecordNodeSpawn_EventsRingTrim_KeepsLastFifty` 明确 60 次 spawn 后只保留 50 条，最早保留的是 thread-10→thread-11（`cmd/mcp-orch/store/taskdag/store_node_spawn_test.go:256-300`）。
   - 风险：对于长 DAG、多节点、反复 replan/retry 的量化任务，事件链会被截断。由于没有 event_count_dropped、first_kept_seq 或 external archive，审计方无法知道丢了多少早期事件，也无法重建完整执行历史。
   - 建议：若继续环形截断，至少写 `events_dropped_count` / `first_event_seq`；更稳妥是把审计事件迁到独立表并按 run_id 分页读取，run row 只保留摘要。

4. **[major] spawn 事件 append 对“run 不存在/非 running”是软失败，会覆盖节点 thread 但不留下 run 级审计**
   - 证据：`appendNodeSpawnEvent()` 对 `AppendTaskDagRunEvent` 返回 `pgx.ErrNoRows` 直接 nil，不报错（`cmd/mcp-orch/store/taskdag/store_node_spawn.go:116-128`）。测试 `TestRecordNodeSpawn_RetryWithoutRunningRun_SoftMiss` 锁定覆盖成功、`AppendedEvent=false`、`RunKey=""`（`cmd/mcp-orch/store/taskdag/store_node_spawn_test.go:123-157`）。SQL 只更新 `status='running' AND id=$3` 的 run（`cmd/mcp-orch/sql/queries/task_dag_run.sql:214-230`）。
   - 风险：如果 run 已终态、run row 缺失、或状态异常，节点仍会更新 `spawning_thread_id`，但 run.events 没有记录这次覆盖。这样会出现 node 当前指向新 thread，而 run 审计链缺一段的状态，后续 UI 跳转和故障归因会不一致。
   - 建议：对 run_id 缺失/非 running 的 append miss 至少写 warning event 或返回可观测错误；如果确实要软失败，应在 node result/metadata 或 metrics 中记录 `spawn_event_append_missed`。

5. **[moderate] dashboard 与 sharedfile 删除保护只识别 file final_output，JSON/text 最终产物不会进入同一解释层**
   - 证据：dashboard 通过 `contract.FinalOutputFileFromRunMetadata()` 生成 `FinalOutputRef`，且固定 `Kind: "file"`（`internal/module/dashboard/ui_page.go:260-313`）。sharedfile 删除保护也只用同一个 helper 查 path（`internal/module/memory/ui_rpc.go:506-527`）。helper 测试明确 text output 返回 false（`internal/contract/dag_final_output_test.go:42-46`）。
   - 风险：file final output 能进入 dashboard 和删除保护，JSON/text final output 没有等价 UI 聚合；用户会以为没有最终产物。虽然删除保护只需要 file path，但 dashboard 的“最终产物”概念也被收窄成 file，报告类 DAG 的 text/json 结果不可见。
   - 建议：新增通用 final output union helper，dashboard 展示 file/text/json 三类；删除保护继续只筛 file path，但不要把 helper 命名成唯一 final output 解析入口。

## 误报与已覆盖项

- `RecordNodeSpawn` 已经是 run-scoped：SQL 要求 `run_id=$4 AND $4>0`，测试覆盖同一 DAG 两个 run 的 thread 和 event 不串线（`cmd/mcp-orch/sql/queries/task_dag_node_spawning_thread.sql:16-43`、`cmd/mcp-orch/store/taskdag/store_node_spawn_test.go:159-206`）。
- `AppendTaskDagRunEvent` 已修复 JSON object concat 被误当 append 的问题，使用 `jsonb_build_array($2::jsonb)` 强制数组 append（`cmd/mcp-orch/sql/queries/task_dag_run.sql:198-206`）。本轮不报告 object merge 覆盖。
- `FinalOutputFileFromRunMetadata` 兼容 legacy `sharedfile.path` envelope（`internal/contract/dag_final_output_test.go:31-41`）。

## 验证

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/store/taskdag ./cmd/mcp-orch/orchestration ./cmd/mcp-orch/orchestration/nodeexec ./internal/contract ./internal/module/dashboard ./internal/module/memory -count=1
```

结果：Go guard、`internal/archtest`、`cmd/mcp-orch/store/taskdag`、`cmd/mcp-orch/orchestration`、`cmd/mcp-orch/orchestration/nodeexec`、`internal/contract`、`internal/module/dashboard` 与 `internal/module/memory` 通过。

## 下一轮建议

- Round 025 审查 sharedfile final output 保护边界与 node outputs.to_sharedfile I/O 路径，重点看最终产物写入、引用保护、路径约束和删除/提升操作的一致性。

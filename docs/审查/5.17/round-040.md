# Round 040 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 07:36:05 KST
- 结束：2026-05-17 07:45:28 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 taskdag store 层 `CompleteNodeAndScheduleDownstream`、下游 ready promotion、wakeup 幂等、run finalize 与 run 隔离测试，重点看量化 DAG 多 run、下游唤醒 payload 与依赖解析的风险。

- `cmd/mcp-orch/store/taskdag/store_complete_downstream.go`
- `cmd/mcp-orch/store/taskdag/store_wakeup.go`
- `cmd/mcp-orch/store/taskdag/store_complete_downstream_test.go`
- `cmd/mcp-orch/store/taskdag/store_run_isolation_test.go`
- `cmd/mcp-orch/store/taskdag/store_finalize_run_test.go`
- `cmd/mcp-orch/store/sqlc/task_dag_wakeup_dispatch.sql.go`
- `cmd/mcp-orch/store/sqlc/task_dag_run.sql.go`
- `cmd/mcp-orch/store/sqlc/task_dag_node_write.sql.go`

## Findings

1. **[major] 下游 payload 的 upstream output 路径不含 run_id，多 run 并发时可能引用错输出**
   - 证据：`scheduleDownstreamWakeupsTx()` 构造 `DownstreamUpstreamRef.Path` 为 `dag/<dagKey>/<nodeKey>/output.json`，没有 run id（`cmd/mcp-orch/store/taskdag/store_complete_downstream.go:129-132`）。同文件其他逻辑已经把 downstream wakeup idempotency key 改为含 run_id（`cmd/mcp-orch/store/taskdag/store_complete_downstream.go:298-305`），run 隔离测试也只验证 wakeup run_id/key，不验证 payload path（`cmd/mcp-orch/store/taskdag/store_run_isolation_test.go:39-62`）。
   - 风险：同一 DAG 多个量化 run 并发时，下游节点拿到的 upstream output path 相同，可能读取另一个 run 的输出或覆盖共享文件。
   - 建议：`DownstreamUpstreamRef.Path` 纳入 run_id/run_key，例如 `dag/<dagKey>/run/<runID>/<nodeKey>/output.json`，并增加 payload 断言。

2. **[major] `depends_on` 解码失败被当作依赖不满足，CompleteNode 可成功但下游永久 pending**
   - 证据：`dependsSatisfiedForUpstream()` 对 `decodeDependsOn()` 错误直接返回 false，注释称上层会再 decode 并冒错，但实际 `scheduleDownstreamWakeupsTx()` 只在该函数返回 true 后才进入 promote/enqueue，没有第二次 decode（`cmd/mcp-orch/store/taskdag/store_complete_downstream.go:173-190`）。
   - 风险：量化 DAG 节点依赖字段损坏时，上游完成会正常提交事务并可能最终 finalize 卡住；错误不会暴露给调用方或指标，排查表现为下游长期 pending。
   - 建议：依赖 JSON 解码失败应使 `CompleteNodeAndScheduleDownstream` 返回错误，或至少写 run event/metric。

3. **[major] 预先存在同 idempotency_key 的 wakeup 会保留旧 payload，新完成的上游输出不会更新**
   - 证据：`EnqueueTaskDagWakeup` 使用 `ON CONFLICT (idempotency_key) DO NOTHING`（`cmd/mcp-orch/store/sqlc/task_dag_wakeup_dispatch.sql.go:107-111`）。测试明确要求冲突时原 `PromptPayload` 不被覆盖（`cmd/mcp-orch/store/taskdag/store_complete_downstream_test.go:133-180`）。
   - 风险：如果之前失败/重放遗留的 pending wakeup payload 指向旧 upstream outputs，新的量化结果不会修正 payload；下游启动时读取旧上下文。
   - 建议：冲突时校验 payload hash 一致；不一致应 fail/run-event，而不是静默跳过。

4. **[moderate] 无 assigned_to 的下游被 promote 到 ready 但不 enqueue，缺少自动补派路径**
   - 证据：F6.3 先把依赖满足的 pending 下游 promote 到 ready（`cmd/mcp-orch/store/taskdag/store_complete_downstream.go:140-160`），`assigned_to` 为空时 `tryEnqueueDownstream()` 直接返回 nil（`cmd/mcp-orch/store/taskdag/store_complete_downstream.go:200-220`）。测试锁定无 assigned_to 仍变 ready、但没有 wakeup（`cmd/mcp-orch/store/taskdag/store_complete_downstream_test.go:249-342`）。
   - 风险：量化 DAG 动态分配 agent 时，下游进入 ready 后没有 pending wakeup，后续补 assigned_to 若没有额外 dispatcher 扫描，节点可能停在 ready。
   - 建议：提供 ready-unassigned reclaimer/dispatcher，或在 assigned_to 被补齐时自动 enqueue。

5. **[moderate] run finalize 只在 final_node_key 成功时写 final_output，失败/取消 run 无结构化最终输出**
   - 证据：finalize SQL 仅当 `final_status='succeeded'` 且 `final_output` 非空时写 metadata（`cmd/mcp-orch/store/sqlc/task_dag_run.sql.go:300-317`）。测试覆盖成功 final node 文件输出（`cmd/mcp-orch/store/taskdag/store_finalize_run_test.go:163-240`）。
   - 风险：量化 run 失败时只有节点 result 里的 reason，没有 run-level final_output/error_summary；上层 dashboard 或审计报告无法直接显示失败根因。
   - 建议：失败/取消 finalize 时也写 `final_error` 或 `terminal_summary`，包含首个失败节点和 reason。

## 误报与已覆盖项

- `CompleteNodeAndScheduleDownstream`、`FailNodeAndCancelDownstream` 已经按 run_id 操作，不会跨 run 改节点状态；测试覆盖两个 run 的节点和 wakeup 隔离（`cmd/mcp-orch/store/taskdag/store_run_isolation_test.go:39-96`）。
- idempotency key 已包含 run_id，避免不同 run 的同名节点 wakeup 键冲突（`cmd/mcp-orch/store/taskdag/store_complete_downstream.go:298-305`）。
- pending 下游只有全部依赖为 done 才 promote，diamond/chain 场景已有测试覆盖（`cmd/mcp-orch/store/taskdag/store_complete_downstream_test.go:416-519`）。

## 验证

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/store/taskdag -count=1
```

结果：通过。

## 下一轮建议

- Round 041 审查 taskdag wakeup claim/retry/lease SQL 与 dispatcher 的状态转换边界。

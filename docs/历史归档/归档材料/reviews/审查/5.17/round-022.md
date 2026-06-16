# Round 022 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 06:31:33 KST
- 结束：2026-05-17 06:32:38 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 node router / dispatch prompt / dynamic `task_dag_apply_ops` 调用闭环，重点看 agent 是否能拿到正确的 `base_version`、`run_id/run_key`、运行节点状态和工具语义，从而做出可执行的量化修复决策。

- `internal/sidecar/orch/orchestration/node_router.go`
- `internal/sidecar/orch/orchestration/node_executor_dispatch.go`
- `internal/sidecar/orch/orchestration/dag_dispatch.go`
- `internal/sidecar/orch/orchestration/nodeexec/executor_agent.go`
- `internal/sidecar/orch/orchestration/nodeexec/inputs.go`
- `internal/sidecar/orch/tools/task_tools.go`
- `internal/sidecar/orch/orchestration/node_router_test.go`
- `internal/sidecar/orch/orchestration/node_router_shard18_test.go`
- `internal/sidecar/orch/orchestration/wakeup_dispatcher_shard17_smart_retry_test.go`

## Findings

1. **[critical] replan planner 被要求调用 `task_dag_apply_ops`，但 prompt 没给可用的 `base_version`，且读路径也不保证能取到 version**
   - 证据：`buildReplanPlannerPrompt()` 只写入 DAG key、node key、failure class 和 error，然后要求 planner “use task_dag_apply_ops with the current base_version”（`internal/sidecar/orch/orchestration/node_router.go:111-120`）。测试也只断言 prompt 包含 DAG key、node key 和 `task_dag_apply_ops`（`internal/sidecar/orch/orchestration/wakeup_dispatcher_shard17_smart_retry_test.go:107-109`）。工具 schema 把 `base_version` 标为必填（`internal/sidecar/orch/tools/task_tools.go:397-401`），但 Round 021 已确认常规 `task_get_dag` DTO 没有暴露 `version`，StartDAG snapshot 也固定为 0。
   - 风险：replan planner 在失败恢复路径上被引导使用一个必填但不可见的 OCC 参数。实际结果要么传 0 导致版本冲突，要么猜测/复用陈旧值，要么反复读取也得不到字段。replan 是失败恢复链路，若它系统性卡在 base_version 上，会把可修复的节点失败放大成 DAG 卡死或重试耗尽。
   - 建议：在 replan prompt 里直接注入当前 `dag.version`，并说明冲突后调用 `task_get_dag` 刷新；同时把 `version` 暴露到 `task_get_dag`。若 version 尚未可见，replan 策略不应自动选择 `task_dag_apply_ops`。

2. **[major] replan prompt 缺少 `run_id/run_key`，无法区分“修当前 run”还是“改未来模板”**
   - 证据：`buildReplanPlannerPrompt()` 没有写 `w.RunID` 或 run_key（`internal/sidecar/orch/orchestration/node_router.go:111-120`）。Wakeup 本身有 run_id，路由器对 DAG wakeup 也强制 `RunID > 0`（`internal/sidecar/orch/orchestration/node_router.go:284-299`），dispatcher 缺 run_id 会永久失败（`internal/sidecar/orch/orchestration/node_executor_dispatch.go:309-317`）。但 `task_dag_apply_ops` 输入只有 dag_key/base_version/ops，没有 run scope（`internal/sidecar/orch/tools/task_tools.go:397-401`）。
   - 风险：planner 想修“当前失败节点”时没有 run scope，也没有工具能表达 runtime patch；结合 Round 021 的证据，`add_node` 只会写模板。planner 可能以为自己把修复节点接进当前 run，实际只影响未来 run，当前失败节点仍会按 retry/fail 继续推进。
   - 建议：replan prompt 必须展示 run_id/run_key 和当前失败节点的 runtime 状态；如果当前工具只能改模板，prompt 应禁止声称恢复当前 run。真正的 current-run replan 需要新增 runtime-scoped apply/dispatch 接口。

3. **[major] 普通 agent 节点启动 prompt 只由 inputs prefix + `first_turn` 组成，没有自动注入 DAG/run/version 上下文**
   - 证据：`AgentExecutor.Execute()` 构造 `LaunchRequest` 后只把 `inputsPrefix` 和 `cfg.FirstTurn` 拼成 `req.Prompt`（`internal/sidecar/orch/orchestration/nodeexec/executor_agent.go:222-225`）。`composePrompt()` 只有 `[inputs.from_nodes]`、`[inputs.from_sharedfiles]` 和 `[first_turn]` 三类内容（`internal/sidecar/orch/orchestration/nodeexec/inputs.go:260-279`）。路由器构造的 `RunContext` 虽含 `DagKey/NodeKey/RunID`（`internal/sidecar/orch/orchestration/node_router.go:191-198`），但该上下文没有注入 prompt；测试也锁定无 inputs 时 prompt 与 `first_turn` 完全一致（`internal/sidecar/orch/orchestration/nodeexec/executor_agent_inputs_test.go:182-205`）。
   - 风险：任何需要 agent 自主调用 `task_get_run`、`task_dispatch_node`、`task_dag_apply_ops` 的节点，都必须依赖人工在 `first_turn` 里写对 dag_key/run_id/base_version。漏写时 agent 看不到自己处于哪个 run，也不知道应该操作模板还是 runtime 节点，容易把量化执行动作打到错误目标。
   - 建议：为 DAG agent prompt 增加不可省略的 `[dag_context]` 前缀，至少包含 `dag_key/node_key/run_id/run_key/dag_version_snapshot/current_template_version`。若担心打破兼容，可通过 config 开关或 agent_key capability 开启，但 replan/dispatcher 节点应默认开启。

4. **[major] `task_dispatch_node` 要求 runtime `run_id`，但 replan/agent prompt 没有稳定提供，容易出现 ready 未指派节点无法被 agent 推进**
   - 证据：`DispatchNode` 输入层要求 `run_id`，service 也在 `normalizeDispatchInputs()` 中拒绝 `run_id <= 0`（`internal/sidecar/orch/orchestration/dag_dispatch.go:87-103`），工具 schema 同样把 run_id 作为必填（`internal/sidecar/orch/tools/task_tools.go:409-414`）。但普通 agent prompt 不注入 run_id，replan prompt 也不注入 run_id。Round 019 已记录 ready+unassigned 节点会停在 ready 且没有 wakeup 的风险。
   - 风险：agent 发现某个节点 ready 但未指派时，缺少 run_id 会让 `task_dispatch_node` 直接失败；如果它用模板节点或猜测 run_id，又可能调度错误 run。ready 未指派节点因此更难被自动修复。
   - 建议：在所有 DAG 子 agent prompt 中注入 `run_id`；在 `task_get_dag`/`task_get_run` 文档里明确模板节点和 runtime 节点的区别；错误信息建议提示“先 task_get_run(run_key) 获取 run.id”。

5. **[moderate] 工具描述仍含 “Skeleton stage returns ErrLifecycleNotImplemented”，会误导 agent 对真实路径可用性判断**
   - 证据：`task_dag_apply_ops` 工具描述写 “Skeleton stage returns ErrLifecycleNotImplemented”（`internal/sidecar/orch/tools/task_tools.go:397-401`），`task_start_dag` 描述也保留类似骨架阶段说法（`internal/sidecar/orch/tools/task_tools.go:416-420`）。但当前 service 已有真实 `ApplyOps`、`StartDAG` 实现和测试。
   - 风险：agent 可能把可用工具当成不可用，或在失败时误判为骨架未实现而不是参数/version 问题。对于自动 replan，这会降低恢复成功率。
   - 建议：更新工具描述为当前真实语义，删除骨架阶段提示；把常见错误（version conflict、idempotency exhausted、runtime run_id required）写入描述或 handler 反馈。

## 误报与已覆盖项

- Node router 已经正确用 `ListRunNodes(dagKey, runID)` 路由 runtime node，不再用模板节点执行（`internal/sidecar/orch/orchestration/node_router.go:301-325`）。本轮不报告 router 执行面误读模板。
- Dispatcher 对 DAG wakeup 缺 `run_id` 已 fail-loud，不会继续走错误 runtime node（`internal/sidecar/orch/orchestration/node_executor_dispatch.go:309-317`）。本轮风险是 agent prompt 和 replan prompt 没把这个必填上下文传给需要调用工具的 agent。
- inputs.from_nodes/from_sharedfiles 注入已有测试覆盖，且 run-scoped prefetch 会过滤非 done 结果（`internal/sidecar/orch/orchestration/node_router.go:228-281`）。本轮不报告 inputs 数据源串 run。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/sidecar/orch/orchestration ./internal/sidecar/orch/orchestration/nodeexec ./internal/sidecar/orch/tools -count=1
```

结果：Go guard、`internal/archtest`、`internal/sidecar/orch/orchestration`、`internal/sidecar/orch/orchestration/nodeexec` 与 `internal/sidecar/orch/tools` 通过。

## 下一轮建议

- Round 023 审查 `task_get_dag` / `task_get_run` / `task_list_runs` 读模型和 DTO，重点看 UI/agent 可见状态是否足以支撑 `ApplyOps`、dispatch 和 replan 决策。

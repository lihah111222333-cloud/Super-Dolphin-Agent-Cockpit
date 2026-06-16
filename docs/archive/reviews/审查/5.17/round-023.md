# Round 023 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 06:33:54 KST
- 结束：2026-05-17 06:34:56 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 `task_get_dag` / `task_get_run` / `task_list_runs` 读模型和 DTO，重点看 UI/agent 可见状态是否足以支撑 `ApplyOps`、dispatch、replan 和 final output 判断。

- `internal/contract/orchestration.go`
- `cmd/mcp-orch/orchestration/dag.go`
- `cmd/mcp-orch/orchestration/dag_query.go`
- `cmd/mcp-orch/orchestration/dag_query_test.go`
- `cmd/mcp-orch/store/taskdag/store_run.go`
- `cmd/mcp-orch/store/taskdag/contract.go`
- `cmd/mcp-orch/sql/queries/task_dag_run.sql`
- `cmd/mcp-orch/tools/task_tools.go`

## Findings

1. **[critical] `task_get_dag` 的 DAG DTO 不含 `version`，调用方无法从权威读模型取得 `ApplyOps.base_version`**
   - 证据：`DAGSummary` 字段只有 id/key/title/description/status/created_by/metadata/time，没有 version（`internal/contract/orchestration.go:387-399`）。`loadDAGDetail()` 返回 `dagSummaryDTO(*dag)` 和模板节点（`cmd/mcp-orch/orchestration/dag.go:305-315`），`dagSummaryDTO()` 也没有 version 映射（`cmd/mcp-orch/orchestration/dag.go:391-405`）。同时 `ApplyOpsRequest` 要求 `BaseVersion`（`internal/contract/orchestration.go:281-285`），工具 schema 也将 base_version 设为必填（`cmd/mcp-orch/tools/task_tools.go:397-401`）。
   - 风险：读模型无法提供写模型必需的 OCC 版本。UI/agent 做模板编辑、replan 或动态补图时没有可靠方式拿 `base_version`，只能传 0、猜值或依赖隐藏知识，导致冲突重试不可用。
   - 建议：把 `task_dags.version` 加到 sqlc `GetTaskDag/ListTaskDags`、store `DAG`、contract `DAGSummary` 和 `task_get_dag` 响应；所有 `ApplyOps` 调用示例必须从 `task_get_dag.dag.version` 取值。

2. **[major] `task_get_run` 返回 `dag_version_snapshot`，但当前 StartDAG 写入恒为 0，读模型会传播错误审计值**
   - 证据：`Run` DTO 暴露 `DagVersionSnapshot`（`internal/contract/orchestration.go:319-334`），`dagRunDTO()` 原样映射该字段（`cmd/mcp-orch/orchestration/dag_query.go:144-160`），测试也断言该字段会被返回（`cmd/mcp-orch/orchestration/dag_query_test.go:109-120`）。但 Round 020 已确认 `dagVersionFor()` 当前固定返回 0（`cmd/mcp-orch/orchestration/scheduler.go:220-225`）。
   - 风险：UI/agent 会看到一个正式字段并把它当作版本快照，但新 run 实际都是 0。后续排障时“run 基于哪个 DAG 版本执行”的证据面会被污染，甚至与 `ApplyOps.NewVersion` 形成矛盾。
   - 建议：修复 StartDAG snapshot 前，在 UI 和文档中标记该字段不可依赖；修复后补端到端测试：ApplyOps bump 到 N 后 StartDAG，`task_get_run.run.dag_version_snapshot == N`。

3. **[major] `task_list_runs` 只返回 run header，不带节点进度/失败摘要，监控面无法直接区分卡住类型**
   - 证据：`ListRuns()` 仅调用 `runStore.ListRuns` 并返回 `[]Run`（`cmd/mcp-orch/orchestration/dag_query.go:104-122`）。SQL 只查 `task_dag_runs` 行，不聚合 runtime nodes（`cmd/mcp-orch/sql/queries/task_dag_run.sql:18-27`）。测试只覆盖 run slice、status filter 和 limit（`cmd/mcp-orch/orchestration/dag_query_test.go:341-490`）。
   - 风险：列表页或自动 watcher 只能看到 run.status=running，无法知道是 root 没 promote、ready 无 assignee、sent-unbound、running 无 turn 绑定、awaiting_verify 卡住，还是某个节点重试中。用户或 agent 必须逐个 `task_get_run` 才能定位，规模稍大时会隐藏量化执行风险。
   - 建议：给 `task_list_runs` 增加可选聚合字段，例如 `node_counts_by_status`、`blocked_reasons`、`last_node_event_at`、`failed_node_count`、`ready_unassigned_count`，或新增轻量 `task_list_run_summaries`。

4. **[major] `task_get_run` 返回 runtime nodes，但没有包含 DAG 模板当前 version，调用方无法判断 run 快照与当前模板是否漂移**
   - 证据：`GetRunResponse` 只有 `Run` 和 `Nodes`（`internal/contract/orchestration.go:300-309`），`getRunResponse()` 只读 run row 和 `ListRunNodes`（`cmd/mcp-orch/orchestration/dag.go:360-373`）。它不会同时读取当前 DAG template 或 version。
   - 风险：agent 拿到某个 run 后，不知道当前模板是否已经被 `ApplyOps` 改过。对于 replan/dispatch，应该基于 runtime nodes 操作当前 run，还是先刷新模板再改未来 run，读模型没有给出判断依据。
   - 建议：`task_get_run` 返回 `current_dag_version`、`run.dag_version_snapshot` 和 `template_drifted`；或者把 `task_get_run` 响应增加 `template` 摘要，明确当前模板和本 run snapshot 的关系。

5. **[moderate] final output helper 只抽取 file final output，JSON/text final output 没有等价 typed helper**
   - 证据：contract 层提供 `FinalOutputFileFromRunMetadata()`，只在 `final_output.kind` 为空或 `file` 时返回 path（`internal/contract/orchestration.go:336-380`）。Round 020 已确认 store 会写入 JSON/text/file 三类 final_output；但 contract helper 对 text/json 返回 false。
   - 风险：不同 UI/agent 如果复用 helper，只能可靠识别文件产物；JSON/text 成功产物会表现为“没有 final output”。这会误导自动汇报或下游消费，尤其是量化审查/报告类 DAG 经常直接输出 JSON/text。
   - 建议：提供统一 `ParseFinalOutputFromRunMetadata()` typed union，覆盖 file/json/text 和缺失原因；保留 file helper 作为兼容包装。

## 误报与已覆盖项

- `task_get_run` 已经按 run_id 返回 runtime nodes，而不是模板节点；`getRunResponse()` 要求 `RunNodeReadStore` 并调用 `ListRunNodes(dagKey, run.ID)`（`cmd/mcp-orch/orchestration/dag.go:360-373`），测试覆盖了 runtime node 返回（`cmd/mcp-orch/orchestration/dag_query_test.go:136-180`）。
- `task_list_runs` 有默认 limit=50 和上限 200，避免无界扫描（`cmd/mcp-orch/orchestration/dag_query.go:85-116`、`cmd/mcp-orch/orchestration/dag_query_test.go:411-490`）。本轮不报告列表无分页上限。
- `Run` DTO 对 `budget_limit` 指针做防御拷贝，测试已覆盖（`cmd/mcp-orch/orchestration/dag_query_test.go:122-134`）。

## 验证

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration ./cmd/mcp-orch/store/taskdag ./cmd/mcp-orch/tools -count=1
```

结果：Go guard、`internal/archtest`、`cmd/mcp-orch/orchestration`、`cmd/mcp-orch/store/taskdag` 与 `cmd/mcp-orch/tools` 通过。

## 下一轮建议

- Round 024 审查 DAG events / spawn history / final output helper / UI 解释层，重点看审计事件是否足以重建 agent spawn、retry、replan 和最终产物链路。

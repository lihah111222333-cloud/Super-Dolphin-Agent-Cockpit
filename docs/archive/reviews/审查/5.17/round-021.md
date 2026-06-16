# Round 021 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 06:27:52 KST
- 结束：2026-05-17 06:30:04 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 DAG `ApplyOps` / 运行中 DAG mutation / version OCC / add-update-remove 的事务边界，重点看模板编辑、运行实例、版本号和运行中新增节点是否形成一致的量化状态。

- `cmd/mcp-orch/orchestration/dag.go`
- `cmd/mcp-orch/orchestration/dag_query.go`
- `cmd/mcp-orch/store/taskdag/store_dag_ops.go`
- `cmd/mcp-orch/store/taskdag/store.go`
- `cmd/mcp-orch/store/taskdag/contract.go`
- `cmd/mcp-orch/sql/queries/task_dag_dag.sql`
- `cmd/mcp-orch/sql/queries/task_dag_node_read.sql`
- `cmd/mcp-orch/sql/queries/task_dag_node_write.sql`
- `cmd/mcp-orch/orchestration/dag_ops_running_invariants_test.go`
- `cmd/mcp-orch/orchestration/dag_ops_remove_node_test.go`

## Findings

1. **[critical] 运行中 `add_node` 被允许，但持久化只写模板行，当前 run 不会得到新增 runtime node**
   - 证据：`enforceRunningDAGInvariants()` 在 DAG running 或存在 active run 时，只拒绝 `update_dag/update_node/remove_node`，允许 `add_node`，且无依赖的 `add_node` 也被测试明确允许（`cmd/mcp-orch/orchestration/dag_query.go:358-427`、`cmd/mcp-orch/orchestration/dag_ops_running_invariants_test.go:136-155`）。但 `persistAddNodeSpecs()` 调用 `tx.UpsertNode()`（`cmd/mcp-orch/orchestration/dag_query.go:731-746`），底层 `UpsertTaskDagNode` 只插入 `run_id IS NULL` 的模板节点并在模板唯一约束上冲突更新（`cmd/mcp-orch/sql/queries/task_dag_node_write.sql:1-12`）。运行实例节点是在 StartDAG 时由模板复制到 `run_id=$2`（`cmd/mcp-orch/sql/queries/task_dag_run.sql:35-48`），本轮未发现 `ApplyOps` 在 active run 中插入 `run_id>0` runtime node 或 wakeup 的逻辑。
   - 风险：AI/用户以为对 running DAG 做 dynamic patch 后当前执行会继续长出新节点，但实际新增节点只进入未来 run 的模板。当前 run 不会调度该节点，也不会在 finalization 的节点集合里看到它。对量化引擎而言，这是“控制面显示成功、执行面无效”的高风险状态，特别是 smart retry/replan 依赖 `task_dag_apply_ops` 追加修复节点时。
   - 建议：二选一收敛语义：如果 running patch 要影响当前 run，则 `ApplyOps` 必须接收/解析 run scope，在 active run 下同时插入 runtime node、生成 wakeup 并更新 run 事件；如果只是改未来模板，则 running/active run 下应拒绝所有 `add_node`，错误信息明确“只影响下一次运行”。

2. **[major] 运行中 `add_node.depends_on` 的 done 判定使用模板节点状态，不是当前 run 的 runtime 状态**
   - 证据：`preflightOpsBatch()` 在事务内调用 `tx.ListNodes(ctx, dagKey)` 得到 `existing`（`cmd/mcp-orch/orchestration/dag_query.go:307-324`），`DAGOpsStore` 注释说明它复用 `DAGDetailStore` 的 `ListNodes`（`cmd/mcp-orch/store/taskdag/contract.go:93-119`）。SQL `ListTaskDagNodes` 明确只查 `run_id IS NULL` 的模板节点（`cmd/mcp-orch/sql/queries/task_dag_node_read.sql:1-6`）。随后 `enforceRunningAddNodeDeps()` 用 `doneNodeKeys(existing)` 要求依赖指向 `status == "done"` 的节点（`cmd/mcp-orch/orchestration/dag_query.go:412-437`）。但 F6.5 后 StartDAG 会把模板节点复制为 runtime node，真实执行状态在 `run_id>0` 行上推进。
   - 风险：模板节点通常是 `pending/ready`，不会随某个 run 完成变成 `done`；因此 active run 下依赖真实已完成节点的 add_node 可能被错误拒绝。反过来，如果模板状态因历史路径或人工修正变成 `done`，即使当前 run 的对应 runtime node 仍 pending/running，也会被错误允许。这会破坏动态补图的执行依赖判断。
   - 建议：运行中 patch 必须指定目标 run 或选择唯一 active run，并基于 `ListRunNodes(dagKey, runID)` 判断依赖状态；多 active run 时要拒绝或要求 caller 指明 run_key/run_id，不能用模板状态代表所有运行实例。

3. **[major] `ApplyOps` 的 OCC version 已接通 raw SQL，但 DAG DTO/StartDAG 仍不暴露 version，调用方很难拿到可靠 `base_version`**
   - 证据：`ApplyOps` 入口要求 `BaseVersion` 非负并在事务内用 `GetDAGVersionForUpdate` 比对（`cmd/mcp-orch/orchestration/dag.go:608-643`、`cmd/mcp-orch/orchestration/dag_query.go:297-306`），store 用 raw SQL 读取 `task_dags.version`（`cmd/mcp-orch/store/taskdag/store_dag_ops.go:23-47`）。但常规 `GetTaskDag` / `GetTaskDagForUpdate` SQL 返回列不包含 `version`（`cmd/mcp-orch/sql/queries/task_dag_dag.sql:24-32`），`fromDAG` 使用的 sqlc 模型也不含 version；Round 020 已确认 StartDAG snapshot 用的 `dagVersionFor()` 仍固定返回 0（`cmd/mcp-orch/orchestration/scheduler.go:220-225`）。
   - 风险：写路径要求 caller 提供真实 `base_version`，但读路径不给出同一个 version 字段，AI 设计师或 UI 很容易传 0/旧值并收到冲突，或者绕过冲突重试逻辑。这个断层会让动态 DAG 变更表现为偶发失败，而不是可恢复的 OCC 流程。
   - 建议：把 `version` 纳入 `task_dags` 的 sqlc query、store `DAG`、contract `DAG` 和 `task_get_dag` 响应；node router/prompt 也应给出当前 version，并说明冲突后重新拉取。

4. **[major] active run 保护只锁 DAG 行，不锁/冻结 active run 集合，StartDAG 与 ApplyOps 可在检查后交错**
   - 证据：`ApplyOps` 在事务内先 `GetDAGVersionForUpdate` 锁 `task_dags` 行，再 `CountRunningRunsByDagKey()`（`cmd/mcp-orch/orchestration/dag_query.go:297-318`）。StartDAG 也在事务内 `GetDAGForUpdate` 锁同一 DAG 行后创建 run 和复制节点（`cmd/mcp-orch/orchestration/scheduler.go:112-128`）。这能让两条写路径串行，但 `ApplyOps` 如果先拿锁并看到 activeRuns=0，就会继续修改模板、bump version；随后排队的 StartDAG 拿锁并复制修改后的模板。此行为对 draft 编辑合理，但对“active run 保护”来说，保护对象不是一个稳定的“无 run 即将启动”条件。
   - 风险：自动调度或外部触发与 UI/AI apply_ops 同时发生时，StartDAG 可能基于用户尚未预期投入运行的新模板启动。因为 `dag_version_snapshot` 又未接通，后续很难审计这个 run 是在 ApplyOps 前还是后启动的。
   - 建议：若调度器需要强一致模板发布语义，增加 DAG `status`/`publish_version` 或 edit lock，把 StartDAG 限制为只消费已发布版本；否则文档和 UI 必须明确 ApplyOps 提交后立即影响之后排队的所有 StartDAG。

5. **[moderate] `update_dag` 每个 patch 单独计算 `next_run_at`，同批多 patch 时使用同一旧 schedule 校验，顺序语义不明确**
   - 证据：`planDAGUpdates()` 对每个 `update_dag` op 调 `validateDAGPatch(patch, current)`，其中 `current` 是同一个 `GetDAGSchedule()` 结果（`cmd/mcp-orch/orchestration/dag_query.go:491-509`）。`persistDAGPatches()` 又按 patch 顺序逐条 `UpdateDAGPatch`（`cmd/mcp-orch/orchestration/dag_query.go:682-692`）。同批 duplicate `update_dag` 有测试拒绝（`cmd/mcp-orch/orchestration/dag_ops_update_dag_test.go:381-400`），因此当前不会出现多 patch，但这个约束由 `partitionOps` 侧的 duplicate 规则间接保护，并没有在 `planDAGUpdates` 自身表达。
   - 风险：后续如果放开多 `update_dag` 或新增 DAG patch op，旧 schedule 复用会让第二个 patch 的校验和 `next_run_at` 计算不基于第一个 patch 的结果，调度时间可能错。该风险当前被 duplicate 测试压住，属于演进风险。
   - 建议：保留“每批最多一个 update_dag”的显式注释和测试；若要支持多 patch，先合并为一个 final patch 再校验和计算 `next_run_at`。

## 误报与已覆盖项

- `ApplyOps` 的基本 OCC 不是空的：事务内 `GetDAGVersionForUpdate` + `BumpDAGVersion(expected)` 已接通，且 bump lost-lock 会翻译为 `ErrVersionConflict`（`cmd/mcp-orch/orchestration/dag_query.go:346-356`、`cmd/mcp-orch/store/taskdag/store_dag_ops.go:123-143`）。
- `remove_node` 已有状态 fence：service 规划时拒绝被依赖和 running DAG，SQL 删除也只允许模板节点 `status IN ('pending','ready')`（`cmd/mcp-orch/orchestration/dag_ops_remove_node_test.go:157-223`、`cmd/mcp-orch/sql/queries/task_dag_node_write.sql:29-34`）。本轮不报告 remove 无保护。
- 空 ops 短路会读取当前 version 并校验 base_version，不会推进 version，这是测试覆盖的设计行为（`cmd/mcp-orch/orchestration/dag.go:693-713`）。

## 验证

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration ./cmd/mcp-orch/store/taskdag ./cmd/mcp-orch/orchestration/nodeexec ./cmd/mcp-orch/tools -count=1
```

结果：Go guard、`internal/archtest`、`cmd/mcp-orch/orchestration`、`cmd/mcp-orch/store/taskdag`、`cmd/mcp-orch/orchestration/nodeexec` 与 `cmd/mcp-orch/tools` 通过。

## 下一轮建议

- Round 022 审查 node router / dispatch prompt / dynamic apply_ops 调用闭环，重点看 agent 是否能拿到正确 `base_version/run_id/run_key`，以及 prompt 是否误导它对 running DAG 做只改模板的 patch。

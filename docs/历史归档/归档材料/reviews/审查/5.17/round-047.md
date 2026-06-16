# Round 047 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 07:39:56 KST
- 结束：2026-05-17 07:48:31 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 DAG 模板节点写入、批量 upsert、运行时节点快照，以及 ApplyOps update_node 的整行写回语义，重点看量化自动化 DAG 在创建、变更、启动 run 时是否能保证模板与运行实例一致。

- `internal/sidecar/orch/orchestration/scheduler.go`
- `internal/sidecar/orch/orchestration/dag.go`
- `internal/sidecar/orch/orchestration/dag_query.go`
- `internal/sidecar/orch/store/taskdag/store_run.go`
- `internal/sidecar/orch/store/sqlc/task_dag_node_write_batch.go`
- `internal/sidecar/orch/sql/queries/task_dag_node_write.sql`
- `internal/sidecar/orch/sql/queries/task_dag_run.sql`

## Findings

1. **[critical] 批量 Upsert 的冲突目标与单条 Upsert 不一致**
   - 证据：单条 `UpsertTaskDagNode` 明确使用 `ON CONFLICT (dag_key, node_key) WHERE run_id IS NULL`，只匹配模板节点唯一约束（`internal/sidecar/orch/sql/queries/task_dag_node_write.sql:1-12`）。批量路径的手写 SQL 使用 `ON CONFLICT (dag_key, node_key) DO UPDATE`，没有 partial predicate（`internal/sidecar/orch/store/sqlc/task_dag_node_write_batch.go:35-55`）。但服务端创建 DAG 时优先走批量路径，并且注释声称两者“行为完全等价”（`internal/sidecar/orch/orchestration/dag.go:273-295`）。
   - 风险：生产 store 实现 batch 接口后，创建/更新量化 DAG 模板会走与测试 mock/fallback 不同的 SQL 语义。若数据库唯一索引是模板 partial unique，批量语句可能无法匹配约束并直接失败；若存在非 partial 约束，则可能与 runtime 节点唯一性隔离预期冲突。
   - 建议：把 batch SQL 改成与单条 SQL 完全相同的 `ON CONFLICT (dag_key, node_key) WHERE run_id IS NULL`，并补一个真实 store 层测试覆盖 batch 与 fallback 的等价性。

2. **[major] StartDAG 不校验克隆节点数和 root 提升数**
   - 证据：`StartDAG` 在事务中调用 `CloneNodesForRun()` 和 `PromoteRootNodesToReady()`，但丢弃两者返回的 rows affected（`internal/sidecar/orch/orchestration/scheduler.go:110-128`）。store 注释反而写着返回行数应被 service 层用于断言至少一个根节点被提升（`internal/sidecar/orch/store/taskdag/store_run.go:60-78`）。SQL 对空模板或无根模板只会返回 0 行，不会报错（`internal/sidecar/orch/sql/queries/task_dag_run.sql:35-61`）。
   - 风险：空 DAG、所有节点互相依赖的 DAG，或模板节点因上游写入问题未落库时，仍可能返回一个 `running` run。调度器后续没有 ready 节点可派发，量化任务会卡在 running 而不是启动阶段 fail-fast。
   - 建议：`CloneNodesForRun` 返回 0 时回滚并标识模板为空；`PromoteRootNodesToReady` 返回 0 时回滚并报告无根节点/依赖环。

3. **[major] run snapshot 只复制 node 字段，不冻结外部 command card 内容**
   - 证据：`CreateTaskDagRun` 注释说 `dag_version_snapshot` 用于保证 run 创建后 DAG 模板被改不影响本次 run（`internal/sidecar/orch/sql/queries/task_dag_run.sql:5-11`）。实际 `CloneTaskDagNodesForRun` 只复制 `command_ref` 与 `config` 等 node 字段（`internal/sidecar/orch/sql/queries/task_dag_run.sql:38-48`），没有复制 command card 模板、版本 id 或 hash。
   - 风险：运行实例看似有 DAG 版本快照，但 automation executor 后续按 `command_ref` 解析当前 command card；命令卡被修改后，同一个 run 的执行内容可能与启动时审核内容不同。
   - 建议：启动 run 时把 command card 的 version/hash/template 快照到 runtime node config 或 run metadata，并在 executor 中优先使用快照。

4. **[moderate] ApplyOps add_node 没有保存 command_ref/assigned_to**
   - 证据：批量创建 DAG 的 `dagNodeFromRequest()` 会保存 `AssignedTo` 和 `CommandRef`（`internal/sidecar/orch/orchestration/dag.go:317-327`）。但 ApplyOps 的 `persistAddNodeSpecs()` 从 `NodeSpec` 写新节点时只填 `DagKey/NodeKey/Title/NodeType/DependsOn/Config`，没有填 `AssignedTo` 或 `CommandRef`（`internal/sidecar/orch/orchestration/dag_query.go:731-745`）。
   - 风险：replan 或 agent 通过 ApplyOps 动态新增 automation 节点时，即使计划层表达了执行目标，也可能落库为空 assignee/command_ref，导致后续 dispatch/executor 只能从 config 中二次推断或直接失败。
   - 建议：对齐 CreateDAG 与 ApplyOps 的 node 写入模型；若 `NodeSpec` 不应携带这些字段，应在 schema/plan 层显式禁止并给出错误，而不是静默丢失。

5. **[moderate] update_node 仍采用整行 Upsert，未来新增字段容易被旧值或空值覆盖**
   - 证据：`persistOpsBatch()` 注释要求 update 路径必须把未 patch 字段从旧节点复制回去，否则整行 upsert 会覆盖（`internal/sidecar/orch/orchestration/dag_query.go:665-668`）。`mergeNodePatch()` 当前只处理 title、assigned_to、depends_on、config（`internal/sidecar/orch/orchestration/dag_query.go:797-814`）。
   - 风险：task_dag_nodes 后续新增执行相关字段时，如果只扩展写入 SQL 而忘记扩展 merge，ApplyOps update_node 会出现静默丢字段。对量化 DAG 来说，这类字段通常是执行约束、预算、审批或输入输出契约，丢失后风险难以从审计日志还原。
   - 建议：把 update_node 改成窄 SQL patch，或者在 `taskdag.Node` 新字段测试中强制覆盖 merge 保留语义。

## 误报与已覆盖项

- 本轮不重复 Round 020 已记录的“无根 DAG 可启动 running”结论；这里补充的是当前 `StartDAG` 丢弃 clone/promote 行数与 store 注释不一致的证据。
- `CloneTaskDagNodesForRun` 会复制当前模板节点的 `config` 和 `command_ref`，因此本轮没有报告“模板变更会直接改写已复制 runtime node 字段”；风险点是外部 command card 内容没有进入快照。
- ApplyOps update_node 会保留旧 `AssignedTo`，不会在普通 patch 中主动清空该字段（`internal/sidecar/orch/orchestration/dag_query.go:799-807`）。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/sidecar/orch/orchestration ./internal/sidecar/orch/store/taskdag -count=1
```

结果：待执行。

## 下一轮建议

- Round 048 审查 nodeexec plan/schema 对 automation 节点、command_ref、config、assigned_to 的校验边界，确认工具输入与落库模型是否同源。

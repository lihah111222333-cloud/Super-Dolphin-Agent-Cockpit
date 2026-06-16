# Round 049 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 07:48:44 KST
- 结束：2026-05-17 08:00:32 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 `NodeExecutorRouter` 的 `RunContext` 构造、`inputs.from_nodes` 预取、`inputs.from_sharedfiles` 读取、sharedfile reader/writer 适配器，以及 agent turn completed 的 sharedfile materialization。重点看多 run 量化 DAG 的输入读取是否隔离、可诊断、可审计。

- `internal/sidecar/orch/orchestration/node_router.go`
- `internal/sidecar/orch/orchestration/node_router_shard18_test.go`
- `internal/sidecar/orch/orchestration/sharedfile_adapter.go`
- `internal/sidecar/orch/orchestration/dag_turn_completed_subscriber.go`
- `internal/sidecar/orch/orchestration/nodeexec/inputs.go`
- `internal/sidecar/orch/orchestration/nodeexec/executor_automation.go`
- `internal/sidecar/orch/orchestration/nodeexec/types.go`
- `internal/sidecar/orch/sql/queries/task_dag_node_read.sql`
- `internal/sidecar/orch/store/sharedfile/contract.go`
- `internal/sidecar/orch/store/sharedfile/store.go`
- `internal/store/sharedfile/contract.go`
- `internal/store/sharedfile/store.go`

## Findings

1. **[major] `inputs.from_sharedfiles` 只有全局 path，没有 dag/run/node scope**
   - 证据：`RunContext.SharedFileReader` 的接口只有 `ReadSharedFile(ctx, path)`，没有 dag_key/run_id/node_key 参数（`internal/sidecar/orch/orchestration/nodeexec/types.go:133-149`）。router 只把 reader 原样注入 `RunContext`（`internal/sidecar/orch/orchestration/node_router.go:191-198`），agent 与 automation executor 都按配置里的 path 直接读取（`internal/sidecar/orch/orchestration/nodeexec/inputs.go:228-257`；`internal/sidecar/orch/orchestration/nodeexec/executor_automation.go:527-555`）。sharedfile store 本身也只以 `Path` 作为领域键（`internal/sidecar/orch/store/sharedfile/contract.go:18-35`）。
   - 风险：并发量化 run 若复用 `reports/out.log`、`plan.md` 等路径，下游节点可能读取另一个 run 或另一个 DAG 的共享文件。`from_nodes` 已通过 `ListTaskDagRunNodes(dag_key, run_id)` 做运行实例隔离（`internal/sidecar/orch/sql/queries/task_dag_node_read.sql:8-13`），但 sharedfile 输入没有同等级隔离。
   - 建议：把 DAG 输出路径标准化为包含 `dag_key/run_id/node_key` 的命名空间，或扩展 `SharedFileReader/Writer` 端口接受 `RunContext` 并在适配器层强制前缀。

2. **[major] `from_nodes` 上游未 done 会被诊断成 unknown node_key**
   - 证据：`prefetchPrevResults()` 在找到目标上游但 status 非 `done` 时选择跳过，不填 `PrevResults`（`internal/sidecar/orch/orchestration/node_router.go:263-281`）。随后 `loadFromNodes()` 对缺失 key 返回 `from_nodes references unknown node_key`（`internal/sidecar/orch/orchestration/nodeexec/inputs.go:187-221`）。测试也把非 done 上游描述为触发 unknown node_key validation（`internal/sidecar/orch/orchestration/node_router_shard18_test.go:159-190`）。
   - 风险：量化 DAG 出现调度竞态或依赖状态异常时，错误信息会把“节点存在但未完成”伪装成“引用了不存在节点”。这会误导自动修复 agent 去改 DAG 结构，而不是检查调度/状态推进。
   - 建议：`Prefetch` 返回结构化状态，区分 missing、not_done、empty_result；executor 错误摘要保留真实 node status。

3. **[major] sharedfile 已存在时 agent 输出 materialization 复用旧内容**
   - 证据：subscriber 在写 `outputs.to_sharedfile` 前先 `configuredSharedfileAlreadyExists()` 读取同 path（`internal/sidecar/orch/orchestration/dag_turn_completed_subscriber.go:300-324`）。如果 exists=true，只写 result 引用并保留旧内容，日志说明 “preserve existing content”（`internal/sidecar/orch/orchestration/dag_turn_completed_subscriber.go:316-323`）。
   - 风险：同一路径被上一次量化 run、其他 DAG 或人工工具写过时，新 run 会成功引用旧文件，造成下游基于陈旧结果继续执行。这个问题比普通 sharedfile 覆盖风险更隐蔽，因为节点状态可能是 done。
   - 建议：输出 materialization 默认应写入 run-scoped path；若 path 已存在，应按 lock_mode/CAS 明确失败或 append，而不是静默复用旧内容。

4. **[moderate] sharedfile writer 审计身份固定为 `node-router`**
   - 证据：适配器写入时固定 `UpdatedBy: sharedFileWriterUpdatedBy`，常量值为 `"node-router"`（`internal/sidecar/orch/orchestration/sharedfile_adapter.go:70-95`）。注释也说明生产 `RunContext` 暂未带节点级身份（`internal/sidecar/orch/orchestration/sharedfile_adapter.go:20-24`）。
   - 风险：量化 DAG 多节点/多 run 写同一 sharedfile 后，只能看到统一写入者，无法从 sharedfile 元数据反查具体 dag_key、run_id、node_key 或 command_ref。
   - 建议：把 `UpdatedBy` 扩展为 `dag:<dag_key>/run:<run_id>/node:<node_key>`，或在 sharedfile metadata 中增加结构化 provenance。

5. **[moderate] automation sharedfile 写入错误被归类为 validation**
   - 证据：automation executor 的 `writeAutomationSharedfile()` 对 `SharedFileWriter.WriteSharedFile` 返回的任何错误，都生成 `FailureClassValidation`（`internal/sidecar/orch/orchestration/nodeexec/executor_automation.go:561-581`）。而 sharedfile 适配器可能返回 DB、磁盘、路径校验等不同类型错误（`internal/sidecar/orch/orchestration/sharedfile_adapter.go:83-96`）。
   - 风险：磁盘满、DB 写失败、路径策略错误会统一表现为配置 validation。量化自动化任务的重试策略无法区分应重试的基础设施故障和应修配置的路径错误。
   - 建议：沿用输入读取的分类方式，把 writer 错误交给 `classifyAutomationError` 或暴露 typed error。

## 误报与已覆盖项

- `inputs.from_nodes` 已有同 run 隔离测试，`runB` 不会读到 `runA` 的上游结果（`internal/sidecar/orch/orchestration/node_router_shard18_test.go:106-157`）。
- router 现在要求 wakeup 必须带 `run_id`，不会回退读模板节点（`internal/sidecar/orch/orchestration/node_router.go:284-325`）。
- sharedfile reader 对 not found 做了三态区分，不会把缺文件当基础设施错误（`internal/sidecar/orch/orchestration/sharedfile_adapter.go:44-63`）。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/sidecar/orch/orchestration ./internal/sidecar/orch/orchestration/nodeexec ./internal/sidecar/orch/store/sharedfile ./internal/store/sharedfile -count=1
```

结果：通过。

## 下一轮建议

- Round 050 审查 retry strategy、on_failure 配置、attempt 计数和 failure class 分类在 agent/automation 节点间是否一致。

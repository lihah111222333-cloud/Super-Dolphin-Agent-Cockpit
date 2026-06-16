# Round 025 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 06:37:56 KST
- 结束：2026-05-17 06:40:02 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 sharedfile final output 保护与 `outputs.to_sharedfile` I/O 路径，重点看 agent/automation 两条输出物化路径、路径策略、写入身份、锁语义和最终产物引用保护的一致性。

- `cmd/mcp-orch/orchestration/sharedfile_adapter.go`
- `cmd/mcp-orch/orchestration/nodeexec/config.go`
- `cmd/mcp-orch/orchestration/nodeexec/executor_automation.go`
- `cmd/mcp-orch/orchestration/nodeexec/config_test.go`
- `cmd/mcp-orch/orchestration/nodeexec/executor_agent_outputs_test.go`
- `cmd/mcp-orch/orchestration/dag_turn_completed_subscriber.go`
- `cmd/mcp-orch/orchestration/dag_turn_completed_subscriber_shard16_test.go`
- `cmd/mcp-orch/sql/queries/task_dag_node_write.sql`
- `cmd/mcp-orch/store/taskdag/store_output_materialization_claim_test.go`
- `cmd/mcp-orch/store/sharedfile/store.go`
- `cmd/mcp-orch/store/sharedfile/contract.go`
- `internal/platform/sharedfilepath/policy.go`
- `internal/module/memory/ui_rpc.go`
- `internal/module/dashboard/ui_page.go`

## Findings

1. **[critical] `lock_mode` 只存在于配置结构，没有任何写入层执行独占或追加语义**
   - 证据：`SharedfileTarget` 注释声明 `LockMode: exclusive | append | shared`，且字段位于所有节点共享的 outputs 配置中（`cmd/mcp-orch/orchestration/nodeexec/config.go:41-57`）。但 automation 写入只取 `target.Path` 后直接 `WriteSharedFile()`，完全不读取 `target.LockMode`（`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:561-579`）；agent turn.completed 路径也只保留 path，`configuredSharedfilePath()` 只返回 `ToSharedfile.Path`（`cmd/mcp-orch/orchestration/dag_turn_completed_subscriber.go:520-529`、`cmd/mcp-orch/orchestration/dag_turn_completed_subscriber.go:632-637`）。底层 sharedfile store 的 `UpsertParams` 也只有 `Path/Content/UpdatedBy`，没有 lock mode、expected version 或 append API（`cmd/mcp-orch/store/sharedfile/contract.go:26-35`）。
   - 风险：DAG 配置里写 `lock_mode:"exclusive"` 或 `"append"` 会给调用方强一致错觉，但实际所有路径都是 last-write-wins upsert。多个 agent/automation 输出同一路径时，量化任务的最终报告、指标 CSV 或中间决策文件可能被后完成的节点覆盖；如果配置期望 append，则前序节点输出会静默丢失。
   - 建议：在 sharedfile store 增加 `CreateExclusive` / `Append` / CAS 版本参数，nodeexec 按 lock_mode 分派；在未实现前，解析阶段拒绝非空 lock_mode 或把字段标注为未实现，避免配置误导。

2. **[major] agent `outputs.to_sharedfile` 遇到已存在文件会直接复用旧内容，不校验是否属于同一 run 或同一输出**
   - 证据：`materializeSharedfileAfterClaim()` 先调用 `configuredSharedfileAlreadyExists()`；只要 reader 读到 path 存在，就编码 `{"sharedfile":{"path":...}}` 并 claim/complete，不再写入当前 `TurnCompleted.Result`（`cmd/mcp-orch/orchestration/dag_turn_completed_subscriber.go:311-324`）。测试也锁定 replay 时文件存在则不再写（`cmd/mcp-orch/orchestration/dag_turn_completed_subscriber_shard16_test.go:122-173`）。该判断没有比较 content hash、run_id、dag_key、node_key 或 updated_by。
   - 风险：如果两个 run 复用固定路径如 `reports/final.md`，第二个 run 的 agent 结果可能不会落盘，run metadata 却指向旧文件。对量化引擎而言，这会把昨日/旧参数回测结果当成当前 run 的 final output，风险高于普通覆盖，因为状态会显示 done 且无错误。
   - 建议：只在 `awaiting_verify` replay 且存在同一节点/同一 run 的物化标记时复用文件；否则 exclusive 模式应拒绝已存在，append 模式应追加，默认路径建议包含 `{dag_key}/{run_key}/{node_key}`。

3. **[major] sharedfile 写入审计只有固定 `updated_by=node-router`，无法追踪到 DAG、run、node 或 agent**
   - 证据：生产 adapter 固定使用 `sharedFileWriterUpdatedBy = "node-router"`（`cmd/mcp-orch/orchestration/sharedfile_adapter.go:70-95`）。`UpsertParams` 只提供 `UpdatedBy` 字段（`cmd/mcp-orch/store/sharedfile/contract.go:32-35`），store 直接写入 DB（`cmd/mcp-orch/store/sharedfile/store.go:31-44`），没有 metadata 保存 dag_key/node_key/run_id/thread_id。
   - 风险：sharedfile 是 final output 与跨节点输入的关键媒介。一旦文件被覆盖、复用或删除保护触发，审计只能看到“node-router”写过，无法区分是哪个 DAG run、哪个节点、哪个 child agent 或 automation command 写入。量化审查中这会阻断结果溯源和责任归因。
   - 建议：扩展 sharedfile metadata 或 `UpdatedBy` 结构化值，至少写入 `dag_key/run_id/node_key/thread_id`；dashboard 和删除保护展示这些来源，方便判断 stale final output。

4. **[major] automation 的 sharedfile 输出只写 `stdout`，但 node.result 默认又会保存完整命令结果，双路径语义不一致**
   - 证据：`writeAutomationSharedfile()` 注释明确只把 `result.Stdout` 写入 sharedfile（`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:558-560`）。随后 `shouldEmitNodeResult()` 在没有配置 `to_sharedfile` 时保存完整 `AutomationCommandResult`；配置 `to_sharedfile` 且未勾 `to_node_result` 时不保存 node.result（`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:157-172`、`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:211-224`）。
   - 风险：同一个 automation 节点在不同 outputs 配置下，最终可审计内容从完整 `{exit_code, stdout, stderr, command, args}` 变成只有 stdout。命令产生警告、参数、命令行或 stderr 诊断时，sharedfile final output 缺少执行上下文，后续审计无法解释结果来源。
   - 建议：为 `to_sharedfile` 增加 mode（`stdout` / `full_result` / `artifact`）并在默认模式写结构化完整结果；至少在 run/node metadata 保留 command、args、exit_code 和 stderr 摘要。

5. **[moderate] 后端 DAG 写入可以写 `handoff/tasks/` 系统保留路径，`outputs.to_sharedfile` 没有 agent 级保留路径约束**
   - 证据：路径策略把 `handoff/tasks/` 标为 agent 写入保留区，只由 `ValidateAgentWritePath()` 阻断（`internal/platform/sharedfilepath/policy.go:15-18`、`internal/platform/sharedfilepath/policy.go:77-89`）。但 sharedfile store 的 `Upsert()` 使用的是通用 `ValidateWritePath()`，允许 `handoff/` 前缀（`cmd/mcp-orch/store/sharedfile/store.go:31-35`、`internal/platform/sharedfilepath/policy.go:44-75`）。`outputs.to_sharedfile` 通过后端 adapter 走 store.Upsert，没有额外区分 agent/automation 调用身份（`cmd/mcp-orch/orchestration/sharedfile_adapter.go:83-95`）。
   - 风险：DAG 作者可以把 agent/automation 输出配置到 `handoff/tasks/...`，覆盖系统任务描述路径。虽然这是后端系统写入而非 MCP agent tool 写入，但从语义上仍是节点输出驱动，可能污染 agent 后续读取的 handoff 内容。
   - 建议：为 DAG node outputs 单独定义允许前缀，默认禁止 `handoff/tasks/` 和 `_internal/`，只允许 `dag/`、`reports/` 或 run-scoped 子目录；确需系统路径时使用显式 privileged flag。

## 误报与已覆盖项

- sharedfile 读写已经有基本路径安全：`ValidateWritePath()` 拒绝空路径、绝对路径和 traversal，并限制写前缀；`ValidateReadPath()` 也拒绝 traversal/absolute（`internal/platform/sharedfilepath/policy.go:61-98`）。本轮不报告路径逃逸。
- agent sharedfile 大输出不会直接塞入 `task_dag_nodes.result`：测试覆盖大 payload 写 sharedfile 后 node.result 只保存 path envelope（`cmd/mcp-orch/orchestration/dag_turn_completed_subscriber_shard16_test.go:40-84`）。
- stale duplicate turn.completed 在写文件前有 SQL claim fence：`ClaimTaskDagNodeOutputMaterialization` 只接受 `ready/running/awaiting_verify` 且限定 run_id（`cmd/mcp-orch/sql/queries/task_dag_node_write.sql:60-73`），测试覆盖 terminal/pending 拒绝（`cmd/mcp-orch/store/taskdag/store_output_materialization_claim_test.go:15-80`）。

## 验证

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration ./cmd/mcp-orch/orchestration/nodeexec ./cmd/mcp-orch/store/taskdag ./cmd/mcp-orch/store/sharedfile ./internal/platform/sharedfilepath ./internal/module/memory ./internal/module/dashboard -count=1
```

结果：Go guard、`internal/archtest`、`cmd/mcp-orch/orchestration`、`cmd/mcp-orch/orchestration/nodeexec`、`cmd/mcp-orch/store/taskdag`、`cmd/mcp-orch/store/sharedfile`、`internal/platform/sharedfilepath`、`internal/module/memory` 与 `internal/module/dashboard` 通过。

## 下一轮建议

- Round 026 审查 `inputs.from_sharedfiles` 与 `prev_results` 注入路径，重点看大文件输入、JSON/string 语义、输入预算、路径列表规模和缺失文件处理是否会污染节点执行。

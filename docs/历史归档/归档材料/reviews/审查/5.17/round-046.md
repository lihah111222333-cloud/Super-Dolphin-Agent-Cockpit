# Round 046 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 07:55:11 KST
- 结束：2026-05-17 08:02:55 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 command card 的 store、SQL、MCP tools、dashboard 只读 store，以及 automation executor 对 `command_ref` 的使用，重点看量化自动化命令是否可版本固定、可审计、可安全下线。

- `internal/sidecar/orch/store/commandcard/contract.go`
- `internal/sidecar/orch/store/commandcard/store.go`
- `internal/sidecar/orch/sql/queries/command_card.sql`
- `internal/sidecar/orch/tools/command_tools.go`
- `internal/sidecar/orch/tools/parity_v2_test.go`
- `internal/store/commandcard/store.go`
- `internal/store/commandcard/store_test.go`
- `internal/sidecar/orch/orchestration/nodeexec/executor_automation.go`
- `internal/sidecar/orch/orchestration/nodeexec/config.go`

## Findings

1. **[major] command_card_versions 表存在，但 Upsert/Delete 不自动归档当前版本**
   - 证据：store 暴露 `InsertVersion()` / `ListVersions()`（`internal/sidecar/orch/store/commandcard/contract.go:35-58`），SQL 也有 `InsertCommandCardVersion`（`internal/sidecar/orch/sql/queries/command_card.sql:10-21`）。但 `Upsert()` 只执行 `UpsertCommandCard()`（`internal/sidecar/orch/store/commandcard/store.go:27-44`），`Delete()` 只执行 `DeleteCommandCard()`（`internal/sidecar/orch/store/commandcard/store.go:58-60`），没有读取旧值并写 version。
   - 风险：高风险量化命令模板被修改或删除后，没有自动快照可追溯；历史 DAG run 只能知道 `command_ref`，难以复现当时执行的命令。
   - 建议：Upsert/Delete 在同一事务中把旧 command card 写入 version 表，记录操作者、source_updated_at 和变更原因。

2. **[major] automation DAG 只存 `command_ref`，运行时读取当前 command card，不固定版本**
   - 证据：`AutomationExecConfig` 只有 `CommandRef` 和 args，没有 version/id/hash（`internal/sidecar/orch/orchestration/nodeexec/config.go:91-99`）。executor 每次 `loadCommandCard()` 都按 trimmed command_ref 读取当前卡片（`internal/sidecar/orch/orchestration/nodeexec/executor_automation.go:261-272`）。
   - 风险：量化 DAG 定义审核通过后，command card 内容可被后续修改，下一次 run 执行不同命令；这绕过 DAG version snapshot 和审计预期。
   - 建议：DAG node config 保存 `command_card_version_id` 或 `command_template_hash`；启动 run 时把使用的 card 内容快照进 run/node。

3. **[major] command_card_runs 统计表被 List 查询使用，但 executor 没有写入运行记录**
   - 证据：`ListCommandCards` 聚合 `command_card_runs` 计算 `last_run_at/run_count`（`internal/sidecar/orch/sql/queries/command_card.sql:39-54`）。全文只找到该表被查询，没有 insert 运行记录；executor 只 `RunCommandCard()` 后 finalize outcome（`internal/sidecar/orch/orchestration/nodeexec/executor_automation.go:144-148`）。
   - 风险：dashboard 显示的使用次数和最近运行时间可能长期为 0/null，无法识别高频、高失败率或最近被调用的量化命令。
   - 建议：AutomationExecutor 或 router 在每次执行后写 command_card_runs，包含 dag_key、node_key、run_id、exit_code、duration、失败分类。

4. **[major] DeleteCommandCard 没有 DAG 引用保护**
   - 证据：`Delete()` 直接按 card_key 删除当前 command card（`internal/sidecar/orch/store/commandcard/store.go:58-60`；SQL 在 `internal/sidecar/orch/sql/queries/command_card.sql:6-8`）。automation node config 使用 `command_ref` 字符串引用卡片（`internal/sidecar/orch/orchestration/nodeexec/config.go:91-99`）。
   - 风险：正在使用或历史可重跑的量化 DAG 会在下一次执行时报 `command_get` not found；删除动作没有提示被哪些 DAG 节点引用。
   - 建议：删除前扫描 DAG template/run nodes 的 command_ref，或改为 soft-disable；允许强删时写 version tombstone 和影响范围。

5. **[moderate] command_list 暴露完整 command_template 给调用方**
   - 证据：`commandCardDTO` 包含 `CommandTemplate`（`internal/sidecar/orch/tools/command_tools.go:23-38`），`command_list` 映射也返回该字段（`internal/sidecar/orch/tools/command_tools.go:95-120`）。
   - 风险：任何能列 command card 的 agent 都能看到完整 shell 模板，包括内部路径、部署命令或敏感环境变量名称；列表接口比按需 get 更容易扩大泄露面。
   - 建议：list 只返回 key/title/risk/enabled/run stats；完整模板仅 `command_get` 且按权限返回。

6. **[moderate] dashboard 侧 commandcard store 只有 List 能力，无法展示版本/删除保护状态**
   - 证据：`internal/store/commandcard.NewStore()` 返回 `Reader`，querier 只含 `ListCommandCards()`（`internal/store/commandcard/store.go:12-27`）。测试只覆盖 list 参数映射和 row 映射（`internal/store/commandcard/store_test.go:24-133`）。
   - 风险：运营界面只能看到当前卡片和统计，不能看到高风险命令的历史版本、最近变更、引用 DAG 或是否安全删除。
   - 建议：dashboard 增加 version/ref summary 查询，风险字段与执行/删除保护联动。

## 误报与已覆盖项

- disabled command card 会在 executor 层 hard fail，不会继续执行（`internal/sidecar/orch/orchestration/nodeexec/executor_automation.go:268-271`）。
- command_get 对 not found 做了用户可读错误转换（`internal/sidecar/orch/tools/parity_v2_test.go:160-176`）。
- command_list 固定使用 `resourceListLimit=50`，不是用户可控无上限列表（`internal/sidecar/orch/tools/command_tools.go:13`、`internal/sidecar/orch/tools/command_tools.go:65-77`）。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/sidecar/orch/store/commandcard ./internal/sidecar/orch/tools ./internal/sidecar/orch/orchestration/nodeexec ./internal/store/commandcard -count=1
```

结果：通过。

## 下一轮建议

- Round 047 审查 DAG node/template 写入、command_ref 与 config 的同步、run snapshot 对自动化配置的拷贝语义。

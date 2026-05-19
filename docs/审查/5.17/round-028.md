# Round 028 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 06:46:23 KST
- 结束：2026-05-17 06:48:17 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 command card runs / dashboard 统计与执行审计闭环，重点看 `command_card_runs` 是否真实写入、`run_count/last_run_at` 是否可信、执行命令和输出是否能追溯。

- `migrations/0001_initial_schema.sql`
- `migrations/001_baseline.sql`
- `cmd/mcp-orch/sql/queries/command_card.sql`
- `cmd/mcp-orch/store/sqlc/command_card.sql.go`
- `cmd/mcp-orch/store/commandcard/store.go`
- `cmd/mcp-orch/store/commandcard/contract.go`
- `cmd/mcp-orch/orchestration/nodeexec/executor_automation.go`
- `internal/store/commandcard/store.go`
- `internal/store/commandcard/contract.go`
- `internal/store/commandcard/store_test.go`
- `internal/module/dashboard/ui_page.go`
- `internal/module/dashboard/rpc.go`
- `docs/doc/codemap/10-store.md`
- `docs/plans/迁移/audit-store-sqlc.md`

## Findings

1. **[critical] `command_card_runs` 表有完整执行历史 schema，但当前执行路径没有任何写入**
   - 证据：schema 定义了 `command_card_runs(card_key, requested_by, params, rendered_command, risk_level, status, requires_review, output, error, exit_code, executed_at...)`（`migrations/0001_initial_schema.sql:171-187`、`migrations/001_baseline.sql:88-96`）。但 `rg` 只找到 `ListCommandCards` 聚合读取该表，没有 insert/update query 或 store 方法；`AutomationExecutor` 执行命令后只返回 `AutomationCommandResult`，不写 run 表（`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:59-89`、`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:144-148`）。
   - 风险：command card 执行看起来有审计表，但实际 DAG automation run 不会留下 rendered command、params、stdout/stderr、exit code、执行时间或审批状态。量化任务的自动化步骤无法从 DB 复盘，事故后只能依赖节点 result 或临时日志。
   - 建议：新增 `InsertCommandCardRun/MarkExecuted/MarkFailed` store 方法，并在 `ShellCommandRunner` 或 `AutomationExecutor` 周围记录 before/after；至少把 DAG key/run id/node key 也写入 run metadata。

2. **[major] dashboard 的 command card `run_count/last_run_at` 来自永远不写的聚合，容易显示为 0 / nil**
   - 证据：`ListCommandCards` 通过 `command_card_runs` 子查询聚合 `MAX(created_at)` 与 `COUNT(*)`（`cmd/mcp-orch/sql/queries/command_card.sql:39-54`）。`cmd/mcp-orch/store/commandcard` 和 `internal/store/commandcard` 都只是映射 `LastRunAt/RunCount`（`cmd/mcp-orch/store/commandcard/store.go:107-123`、`internal/store/commandcard/store.go:41-57`）。dashboard commands 页面直接调用 reader list（`internal/module/dashboard/ui_page.go:154-201`、`internal/module/dashboard/rpc.go:182-188`）。
   - 风险：UI 会把命令卡展示为从未运行，实际 DAG 可能已经运行多次。运营者根据 run_count 判断活跃度、风险暴露或清理候选时会得出错误结论。
   - 建议：在执行路径写入 run 表后再展示统计；短期在 dashboard 上标注“run_count only tracks command_card_runs, DAG automation execution currently not recorded”，避免误导。

3. **[major] `command_card_runs.status/requires_review` 暗示审批流，但 DAG automation 完全绕过该状态机**
   - 证据：run 表默认 `status='pending_review'` 且 `requires_review=true`（`migrations/0001_initial_schema.sql:171-187`）。实际 `AutomationExecutor.loadCommandCard()` 只检查 `card.Enabled`，没有创建 pending_review run、等待批准或推进 executed/failed 状态（`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:261-272`）。
   - 风险：schema 让人以为高风险命令有 review gate，但 DAG 自动执行不会进入这个 gate。结合 Round 027 的 `risk_level` 未接入，审批与统计字段都变成装饰字段。
   - 建议：把 command card run 状态机接入 DAG dispatcher：高风险卡先创建 pending review，批准后才执行；低风险也至少创建 executed/failed 记录。

4. **[major] 执行结果只可能落到 DAG node.result 或 sharedfile，无法按 command card 聚合失败率和输出**
   - 证据：`AutomationCommandResult` 包含 `CardKey/ExitCode/Stdout/Stderr/Command/Args`（`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:46-53`），但只有 `finalizeAutomationOutcome()` 决定写 node.result 或 sharedfile（`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:151-172`）。command card store 没有 run 级 CRUD（`cmd/mcp-orch/store/commandcard/contract.go:35-42`）。
   - 风险：同一 command card 在多个 DAG、多个节点中的失败率、平均耗时、最近 stderr 无法聚合。量化审查很依赖“哪个自动化检查经常失败/输出异常”的横向指标，当前数据模型无法支撑。
   - 建议：run 表新增 dag/run/node 关联列或 JSON metadata；在 dashboard 提供 card-level failure rate、recent failures、last rendered command 和 last stderr 摘要。

5. **[moderate] 仓库已知迁移审计指出 command_card_runs 只有聚合读无 CRUD，但当前实现仍未补闭环**
   - 证据：`docs/plans/迁移/audit-store-sqlc.md` 明确记录 `command_card_runs` 只有聚合读、没有独立 CRUD；`docs/doc/codemap/10-store.md` 也描述它作为执行历史用于统计 `run_count/last_run_at`（`docs/plans/迁移/audit-store-sqlc.md:50-78`、`docs/doc/codemap/10-store.md:574-582`）。
   - 风险：这是历史已识别缺口，不是单一代码疏漏。后续如果只修 dashboard 展示而不接执行写入，审计闭环仍然缺失。
   - 建议：把该缺口升级为 ADR/任务，明确 owner：是 command_card 执行层负责，还是 DAG automation lifecycle 负责。

## 误报与已覆盖项

- command card list 的 SQL 聚合本身没有明显语法问题；如果 `command_card_runs` 有数据，`run_count/last_run_at` 能被映射到 DTO（`internal/store/commandcard/store_test.go:24-77`）。
- disabled command card 的阻断在 executor 已覆盖；本轮关注的是执行历史，不重复报告 enabled/risk_level 执行策略。
- `command_card_versions` 也有 store 方法，但这是版本归档，不是执行 run 历史；不能替代 `command_card_runs`。

## 验证

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/store/commandcard ./internal/store/commandcard ./internal/module/dashboard ./cmd/mcp-orch/orchestration/nodeexec -count=1
```

结果：Go guard、`internal/archtest`、`cmd/mcp-orch/store/commandcard`、`internal/store/commandcard`、`internal/module/dashboard` 与 `cmd/mcp-orch/orchestration/nodeexec` 通过。

## 下一轮建议

- Round 029 审查 DAG cron/scheduler 触发与 command/automation 组合风险，重点看自动运行、并发触发、last_run_at、失败恢复和风险策略是否一致。

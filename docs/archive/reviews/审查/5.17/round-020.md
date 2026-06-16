# Round 020 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 06:20:18 KST
- 结束：2026-05-17 06:26:22 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 DAG start / run idempotency / budget / final output / version snapshot 路径，重点看一次 run 启动时的量化状态是否能真实表达“基于哪个 DAG 版本执行、是否有预算约束、是否有可执行根节点、幂等 key 是否可观测可控”。

- `cmd/mcp-orch/orchestration/scheduler.go`
- `cmd/mcp-orch/sql/queries/task_dag_run.sql`
- `cmd/mcp-orch/store/taskdag/store_run.go`
- `cmd/mcp-orch/store/taskdag/contract.go`
- `cmd/mcp-orch/tools/task_tools.go`
- `internal/contract/orchestration.go`
- `cmd/mcp-orch/orchestration/dag_query.go`
- `cmd/mcp-orch/orchestration/dag_start_test.go`
- `cmd/mcp-orch/store/taskdag/store_finalize_run_test.go`

## Findings

1. **[critical] `dag_version_snapshot` 永远写 0，run 无法证明自己基于哪个 DAG 模板版本执行**
   - 证据：`StartDAG()` 先取 `dagVersion := dagVersionFor(dag)` 并写入 `CreateRunInput.DagVersionSnapshot`（`cmd/mcp-orch/orchestration/scheduler.go:69-77`），事务内锁定 DAG 后又再次用 `dagVersionFor(lockedDAG)` 覆盖（`cmd/mcp-orch/orchestration/scheduler.go:112-128`）。但 `dagVersionFor()` 当前无条件返回 0，并注释说明 version 尚未接通（`cmd/mcp-orch/orchestration/scheduler.go:220-225`）。SQL 注释却明确要求 `dag_version_snapshot` 来自 `task_dags.version` 当前值，用于保证 run 创建后模板修改不影响本次执行（`cmd/mcp-orch/sql/queries/task_dag_run.sql:5-11`）。外露响应也把 `Version` 定义为 run snapshot 的 `dag.version`（`internal/contract/orchestration.go:271-274`）。
   - 风险：量化执行记录无法回答“这次 run 使用的是哪个 DAG 版本”。ApplyOps 之后即便模板发生修改，历史 run 也只显示 version=0；审计、回放、对账和失败归因会把不同模板版本混在一起。更糟的是 `StartDAGResponse.Version` 对新 run 也会是 0，调用方可能误以为服务端没有发生版本变化。
   - 建议：让 store 层 `DAG` / sqlc row 暴露 `task_dags.version`，`GetDAGForUpdate` 后从锁定行写入 `DagVersionSnapshot`；补一条 StartDAG happy-path 测试断言新 run 的 Version 等于模板 version，而不是只断言幂等重放路径。

2. **[major] run 预算字段只读不写，`budget_limit` 不能通过启动入口配置，`budget_used` 没有计量闭环**
   - 证据：store 的 `CreateRunInput` 支持 `BudgetLimit`（`cmd/mcp-orch/store/taskdag/contract.go:662-670`），`CreateRun()` 会把它写入 `budget_limit`（`cmd/mcp-orch/store/taskdag/store_run.go:24-33`）。但 MCP `StartDAGInput` 只有 `dag_key/trigger_source/idempotency_key`（`cmd/mcp-orch/tools/task_tools.go:155-160`），contract `StartDAGRequest` 也只有同三项（`internal/contract/orchestration.go:265-269`），`StartDAG()` 构造 `CreateRunInput` 时没有填 `BudgetLimit`（`cmd/mcp-orch/orchestration/scheduler.go:72-77`）。全仓 `rg "budget_used|BudgetUsed|budget_limit|BudgetLimit|Update.*Budget|Increment.*Budget"` 只看到建表写入、读取和 DTO 映射，未发现增量记账或限额判断；DTO 会继续外露 `BudgetUsed/BudgetLimit`（`cmd/mcp-orch/orchestration/dag_query.go:144-157`、`internal/contract/orchestration.go:319-331`）。
   - 风险：调用方看到预算字段会以为 DAG run 有 token/cost/step 预算约束，但实际启动时无法设置 limit，执行过程中也没有 used 递增和超限中止。对于量化引擎，这会造成成本/容量风险被“字段存在”掩盖，监控面板显示 0 used 或 nil limit，却不是安全状态。
   - 建议：明确预算口径（token、turn、节点数或成本），把 `BudgetLimit` 加到 MCP/contract 启动入口；在 node completion 或 turn usage 回写时递增 `budget_used`，并在 dispatch 前执行超限 gate。若暂不支持预算，应移除或标注 experimental 字段，避免被运维当作控制面。

3. **[major] 根节点提升返回值被忽略，无根或空 DAG 会创建一个没有可执行起点的 running run**
   - 证据：SQL 注释写明 `PromoteRootNodesToReady` 返回受影响行数，service 层用于断言至少一个根节点被提升，否则视为 DAG 无可执行起点（`cmd/mcp-orch/sql/queries/task_dag_run.sql:50-54`）。store 注释也重复了这个契约（`cmd/mcp-orch/store/taskdag/store_run.go:60-62`）。但 `StartDAG()` 事务内只检查 error，丢弃 rows（`cmd/mcp-orch/orchestration/scheduler.go:122-128`）。测试 stub 记录 `promoteRows`，happy path 只断言调用次数，不断言 0 rows 会失败（`cmd/mcp-orch/orchestration/dag_start_test.go:187-211`、`cmd/mcp-orch/orchestration/dag_start_test.go:537-557`）。
   - 风险：DAG 模板没有 root、节点复制为 0 行、或所有节点依赖形成无入口状态时，系统仍会返回 StartDAG 成功并留下 `running` run。后续调度没有 ready root 可 claim；finalize SQL 在 `total=0` 或存在非终态时不会推进终态（`cmd/mcp-orch/sql/queries/task_dag_run.sql:90-108`），这个 run 会变成无进展的量化状态。
   - 建议：在事务内检查 `CloneNodesForRun` 和 `PromoteRootNodesToReady` 的 rows，0 root 应回滚并返回明确错误；补 `promoteRows=0` 的 StartDAG 测试，区分“合法空 DAG 不允许启动”和“根节点全部已被其他路径处理”的设计口径。

4. **[major] `idempotency_key` 直接拼进 `run_key`，缺少长度、字符集和分隔符约束**
   - 证据：MCP 层只 trim `IdempotencyKey`，没有长度或字符集校验（`cmd/mcp-orch/tools/task_tools.go:291-308`）。`generateRunKey()` 也只是 trim 后返回 `fmt.Sprintf("%s#run-%s", dagKey, idempotencyKey)`（`cmd/mcp-orch/orchestration/scheduler.go:211-218`）。测试锁定了同 key 必须原样进入 `dag-x#run-abc`（`cmd/mcp-orch/orchestration/dag_start_test.go:230-257`、`cmd/mcp-orch/orchestration/dag_start_test.go:575-582`）。
   - 风险：调用方可提交超长、包含换行/控制字符/路径样式/再次包含 `#run-` 的 idempotency key，导致 run_key 过长、日志和 UI 展示污染、人工排障时分隔符歧义，甚至触发 DB 字段长度或索引异常。幂等 key 是量化执行的去重轴，输入不可控会让去重和审计都变脆。
   - 建议：定义 idempotency key 白名单和最大长度，例如 `[A-Za-z0-9._:-]{1,128}`；或对原始 key 做 hash，run metadata 保留安全截断后的展示值。测试应覆盖非法字符、超长 key 和 trim 后空串。

5. **[moderate] final output 只在 run 成功聚合时写入 metadata，失败/缺 final 节点时没有显式“无最终产物”状态**
   - 证据：finalize SQL 只在 aggregate final status 为 `succeeded` 且 `task_dags.metadata.final_node_key` 能找到节点时，才把 final node result 提升到 `task_dag_runs.metadata.final_output`（`cmd/mcp-orch/sql/queries/task_dag_run.sql:80-84`、`cmd/mcp-orch/sql/queries/task_dag_run.sql:110-170`）。测试确认没有 `final_node_key` 或 final node 缺失时 metadata 保持原样（`cmd/mcp-orch/store/taskdag/store_finalize_run_test.go:351-395`），失败 run 即使 final node 已 done 也不写 final_output（`cmd/mcp-orch/store/taskdag/store_finalize_run_test.go:442-471`）。
   - 风险：消费方只能通过 metadata 里是否存在 `final_output` 推断最终产物，无法区分“还没完成”“成功但没有配置 final_node_key”“final_node_key 配错”“run 失败所以没有产物”。对于自动汇报或下游流水线，这会把配置错误和正常失败折叠成同一个空值。
   - 建议：在 run metadata 或独立字段里写入 `final_output_status`，例如 `missing_config/missing_node/not_applicable_failed/ready`；至少在 finalize 成功但缺 final node 时追加结构化 event，方便审计和 UI 展示。

## 误报与已覆盖项

- `StartDAG()` 已经把 `CreateRun + CloneNodesForRun + PromoteRootNodesToReady` 放进同一个 `WithRunTx`，promote 报错会回滚，测试覆盖了 promote error 传播（`cmd/mcp-orch/orchestration/dag_start_test.go:537-557`）。本轮风险不是“报错不回滚”，而是“0 rows 不算错误”。
- 幂等 replay 已有 GetRun fallback：running/succeeded 返回既有 run，failed/cancelled 返回 `ErrIdempotencyKeyExhausted`（`cmd/mcp-orch/orchestration/scheduler.go:193-209`），测试覆盖了这些状态（`cmd/mcp-orch/orchestration/dag_start_test.go:279-416`）。本轮不报告“幂等重试一定新建 run”。
- final output 对 JSON/text/sharedfile 三类结果已有正向测试（`cmd/mcp-orch/store/taskdag/store_finalize_run_test.go:247-349`）。本轮关注的是缺失和失败场景缺少可机器判读的原因状态。

## 验证

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/store/taskdag ./cmd/mcp-orch/orchestration ./cmd/mcp-orch/tools -count=1
```

结果：Go guard、`internal/archtest`、`cmd/mcp-orch/store/taskdag`、`cmd/mcp-orch/orchestration` 与 `cmd/mcp-orch/tools` 通过。

## 下一轮建议

- Round 021 审查 DAG apply_ops / 运行中 DAG mutation / version OCC / add-update-remove 的事务边界，重点看运行实例与模板编辑是否会交错，以及 version/baseline 是否能防止丢更新。

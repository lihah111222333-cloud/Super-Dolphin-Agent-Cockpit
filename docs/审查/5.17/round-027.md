# Round 027 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 06:43:26 KST
- 结束：2026-05-17 06:46:22 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 command card 渲染与 shell 执行路径，重点看模板注入、shell sandbox、命令卡版本快照、args schema/risk_level 是否参与执行，以及错误分类对调度重试的影响。

- `cmd/mcp-orch/orchestration/nodeexec/executor_automation.go`
- `cmd/mcp-orch/orchestration/nodeexec/executor_automation_test.go`
- `cmd/mcp-orch/orchestration/wakeup_dispatcher_shard17_smart_retry_test.go`
- `cmd/mcp-orch/store/commandcard/store.go`
- `cmd/mcp-orch/store/commandcard/contract.go`
- `cmd/mcp-orch/sql/queries/command_card.sql`
- `cmd/mcp-orch/store/sqlc/command_card.sql.go`
- `cmd/mcp-orch/tools/command_tools.go`
- `cmd/mcp-orch/fx.go`
- `docs/decisions/ADR-007-automation-kind-progressive.md`

## Findings

1. **[critical] command card 最终通过 `sh -c` 执行，模板参数没有 shell quoting / sandbox / cwd/env 限制**
   - 证据：`ShellCommandRunner.RunCommandCard()` 先渲染 `CommandTemplate`，再直接 `exec.CommandContext(ctx, "sh", "-c", command)`（`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:59-89`）。`renderCommandTemplate()` 使用 Go template 把 args 放进字符串，只做 JSON 解析和 missingkey 检查，没有提供 quote 函数或安全参数数组（`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:339-367`）。ADR-007 明确指出 `command_card + CommandTemplate` 现状已存在 shell sandbox 缺口，且 Q6 仍是 open question（`docs/decisions/ADR-007-automation-kind-progressive.md:20-26`、`docs/decisions/ADR-007-automation-kind-progressive.md:82-91`）。
   - 风险：只要命令卡模板把用户/DAG 参数拼入 shell，就可能出现 `; rm ...`、命令替换、环境变量展开、重定向等注入。量化任务常用路径、ticker、日期、报告文件名作为参数，任何未 quote 的值都可能变成 shell 片段；同时命令继承进程 cwd/env，缺少工作目录和环境隔离。
   - 建议：把 command card 执行模型改为 argv 数组而非 shell 字符串；短期提供强制 `shellquote` template func 并默认拒绝未 quote 参数；补 cwd/env allowlist、禁用高危命令或在沙箱进程中执行。

2. **[critical] automation 节点执行时只按 `command_ref` 拉当前 command card，没有绑定创建/启动时的版本快照**
   - 证据：`AutomationExecutor.loadCommandCard()` 每次执行只 `GetCommandCard(ctx, commandRef)`（`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:261-272`）。生产 getter 也是通过 `command_get` 工具读取当前 store 记录（`cmd/mcp-orch/fx.go:225-250`、`cmd/mcp-orch/tools/command_tools.go:79-93`）。store 虽然有 `InsertVersion/ListVersions`，但 executor 和 DAG node config 只保存 `exec.command_ref`，没有 version id/hash（`cmd/mcp-orch/store/commandcard/contract.go:35-58`、`cmd/mcp-orch/sql/queries/command_card.sql:10-21`）。
   - 风险：同一个 DAG run 在排队、重试或 replan 期间，如果 command card 被编辑，后续执行会跑新模板而不是 run 启动时的模板。审计时只能看到当前 command card 和 node config 的 ref，无法证明实际运行过哪一版命令；量化结果可能因为命令模板漂移而不可复现。
   - 建议：启动 DAG 或 dispatch automation 节点时 snapshot `command_card_version_id` / content hash 到 runtime node config 或 run event；执行时按 snapshot 版本读取，当前卡片被禁用/修改只影响新 run。

3. **[major] `args_schema` 只暴露给 UI/工具，不参与 automation 执行校验**
   - 证据：command card DTO/store 包含 `ArgsSchema`（`cmd/mcp-orch/tools/command_tools.go:23-38`、`cmd/mcp-orch/store/commandcard/contract.go:18-26`），但 `renderCommandTemplate()` 只把 `cfg.Exec.Args` 解成 `map[string]any` 并执行模板（`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:339-367`）。`AutomationCommandCard.ArgsSchema` 在 runner 路径中没有被读取，相关测试只覆盖缺 key template error，不覆盖 schema validation（`cmd/mcp-orch/orchestration/nodeexec/executor_automation_test.go:55-89`）。
   - 风险：UI 看到 schema 会以为参数已经按类型、必填、枚举校验，但执行端实际接受任意 JSON object。字段拼写错误会等到模板 missingkey 才失败；多余字段、错误类型、危险字符或越界数值不会被 schema 拦截。
   - 建议：在 `AutomationExecutor.Execute` 调用 runner 前使用 JSON Schema 校验 `runArgs`；schema 不存在时至少限制 args 必须是 object 并记录无 schema 风险。

4. **[major] `risk_level` 不参与调度或审批，只有 disabled 会阻断执行**
   - 证据：`AutomationCommandCard` 有 `RiskLevel` 字段（`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:36-43`），store/list 也返回 risk_level（`cmd/mcp-orch/sql/queries/command_card.sql:39-54`）。但 `loadCommandCard()` 只检查 `card.Enabled`，没有根据 `RiskLevel` 做人工确认、策略降级或工具限制（`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:261-272`）。
   - 风险：高风险命令卡和普通命令卡在 DAG 自动执行时完全等价。量化引擎如果使用会写文件、删除缓存、联网拉取、提交结果或修改数据库的命令卡，`risk_level` 无法防止无人值守执行高危操作。
   - 建议：把 `risk_level` 接入 DAG policy：high/critical 默认 `needs_human` 或 require approval；记录审批人和 run event；在 cron/auto run 中拒绝高风险命令。

5. **[major] 非零退出统一归类 hard，可能跳过 transient/retry 策略**
   - 证据：shell 命令非零退出被包装为 `CommandExitError`（`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:82-100`），`classifyAutomationError()` 对任何 `CommandExitError` 直接返回 `FailureClassHard`，不再检查 stderr/stdout 或 exit code（`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:290-313`）。测试也锁定 nonzero hard（`cmd/mcp-orch/orchestration/nodeexec/executor_automation_test.go:552-572`）。
   - 风险：curl/git/npm/回测脚本因为网络、锁等待、临时限流或外部服务 503 退出非零时，会被标成 hard permanent。dispatcher 对 unmapped hard failure 不走 retry，直接 fail-fast（`cmd/mcp-orch/orchestration/wakeup_dispatcher_shard17_smart_retry_test.go:207-238`），导致可恢复失败中断整条 DAG。
   - 建议：`CommandExitError` 携带 stderr/stdout/exit code 后再分类；允许 command card 配置 `retryable_exit_codes` 或 `failure_class_rules`，默认只把明确配置的 nonzero 视作 hard。

## 误报与已覆盖项

- `missingkey=error` 已避免模板缺字段静默渲染为空（`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:354-360`）。
- disabled command card 会被 hard failure 阻断，不会继续执行 runner（`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:261-272`）。
- unsupported automation kind 已 fail-loud，当前只允许 command_card（`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:226-247`、`cmd/mcp-orch/orchestration/nodeexec/executor_automation_test.go:91-114`）。

## 验证

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration ./cmd/mcp-orch/orchestration/nodeexec ./cmd/mcp-orch/store/commandcard ./cmd/mcp-orch/tools -count=1
```

结果：Go guard、`internal/archtest`、`cmd/mcp-orch/orchestration`、`cmd/mcp-orch/orchestration/nodeexec`、`cmd/mcp-orch/store/commandcard` 与 `cmd/mcp-orch/tools` 通过。

## 下一轮建议

- Round 028 审查 command card runs / dashboard 统计与执行审计闭环，重点看 run_count、last_run_at、命令执行结果是否真实记录和可追溯。

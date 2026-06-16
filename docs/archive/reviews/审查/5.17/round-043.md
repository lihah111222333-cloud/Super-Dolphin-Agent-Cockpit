# Round 043 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 07:31:41 KST
- 结束：2026-05-17 07:39:20 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮继续审查 `node_type=automation` 的 executor 层，重点看 command card 模板、参数、输出和失败分类是否足以支撑量化任务的可控自动执行。

- `cmd/mcp-orch/orchestration/nodeexec/executor_automation.go`
- `cmd/mcp-orch/orchestration/nodeexec/config.go`
- `cmd/mcp-orch/orchestration/nodeexec/executor_automation_test.go`

## Findings

1. **[critical] ShellCommandRunner 渲染模板后直接 `sh -c` 执行，args 值只参与模板替换，没有命令级参数隔离**
   - 证据：`RunCommandCard()` 调 `renderCommandTemplate()` 后执行 `exec.CommandContext(ctx, "sh", "-c", command)`（`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:59-88`）。模板执行使用 `text/template` 将 args 注入任意命令字符串（`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:339-366`）。测试 happy path 也是 shell 模板 `printf 'hello %s' '{{.name}}'`（`cmd/mcp-orch/orchestration/nodeexec/executor_automation_test.go:55-89`）。
   - 风险：量化 automation 一旦 command card 或 args 被污染，模板中拼出的 shell 会拥有当前进程权限；`risk_level`、审批和 sandbox 都不在 executor 层执行，容易形成命令注入或越权执行。
   - 建议：command card runner 改为 argv 数组/固定可执行文件白名单；确需 shell 时必须显式高风险审批，并记录渲染前后的参数审计。

2. **[major] command card 的 `args_schema` 字段未校验实际 args**
   - 证据：`AutomationCommandCard` 包含 `ArgsSchema` 字段（`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:36-43`），但 `Execute()` 只 load card、build args、RunCommandCard，没有 JSON Schema 校验分支（`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:119-148`）。全文搜索仅见字段定义和测试引用，未见 schema validator。
   - 风险：量化任务可传入缺字段、错类型或额外危险参数，模板仍会渲染执行；错误可能在 shell 层变成破坏性命令或不可解释失败。
   - 建议：在 runner 前按 `ArgsSchema` 做严格校验，默认拒绝额外字段；校验失败归 validation 且不执行命令。

3. **[major] command card `risk_level` 只被存储，不参与审批、限权或执行策略**
   - 证据：`AutomationCommandCard` 包含 `RiskLevel`（`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:36-43`），`loadCommandCard()` 只检查 `Enabled`（`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:261-272`），执行链路没有按 risk_level 分支。
   - 风险：高风险量化 automation 与低风险命令同样无人值守执行；风险标签只在 UI/配置层存在，不能阻止自动化下单、删除文件或长时任务。
   - 建议：executor 或 dispatcher 按 `risk_level` 强制审批、sandbox、超时和 allowed tools；高风险默认禁止 cron/DAG 无人值守。

4. **[major] `outputs.schema` 和 `lock_mode` 字段位已定义但 executor 不执行**
   - 证据：`OutputsConfig.Schema` 与 `SharedfileTarget.LockMode` 在 schema 中定义（`cmd/mcp-orch/orchestration/nodeexec/config.go:41-57`），但 `finalizeAutomationOutcome()` 只 marshal result、写 sharedfile、按 4KB cap 写 node result（`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:157-172`）；`writeAutomationSharedfile()` 只读取 `target.Path`，未读取 `LockMode`（`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:561-580`）。
   - 风险：声明了输出 JSON Schema 或独占/追加写策略的量化节点没有真实约束；多个节点可覆盖同一路径，错误格式也会被当成功结果传播。
   - 建议：落库前执行输出 schema 校验；SharedFileWriter 端口携带 lock mode，并在 store 层做 CAS/append/exclusive 语义。

5. **[moderate] sharedfile 输出只写 stdout，stderr/exit_code/command/args 被丢弃**
   - 证据：`writeAutomationSharedfile()` 注释明确只写 stdout，并调用 `WriteSharedFile(ctx, path, result.Stdout)`（`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:558-579`）。测试也只断言 content 等于 stdout（`cmd/mcp-orch/orchestration/nodeexec/executor_automation_test.go:374-402`）。
   - 风险：量化自动化将大输出导向 sharedfile 后，审计结果缺少实际命令、退出码和 stderr；排查失败或复现交易动作时上下文不足。
   - 建议：sharedfile 支持 `mode: stdout|full_result|artifact`，默认写完整结构化结果，stdout 作为显式精简模式。

6. **[moderate] sharedfile 写入失败被固定归为 validation，可能抑制可恢复重试**
   - 证据：`writeAutomationSharedfile()` 对 `WriteSharedFile` 任意错误都返回 `FailureClassValidation`（`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:575-579`）；测试把 `"disk full"` 也期望为 validation（`cmd/mcp-orch/orchestration/nodeexec/executor_automation_test.go:455-470`）。
   - 风险：磁盘满、DB 短暂失败、文件锁竞争等基础设施问题会被当配置错误，智能重试/infra 告警无法触发。
   - 建议：沿用 `classifyAutomationError()` 或引入 sharedfile 专用错误分类，IO/DB/timeout 归 transient/infrastructure。

## 误报与已覆盖项

- `missingkey=error` 能阻止模板引用缺失字段时继续执行（`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:354-360`）。
- command card disabled 会被拒绝执行（`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:268-271`）。
- node result 有 4KB cap，超限时提示改走 sharedfile（`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:175-208`）。

## 验证

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration/nodeexec -count=1
```

结果：通过。

## 下一轮建议

- Round 044 审查 sharedfile store、磁盘 source、路径策略和 DAG subscriber 的 materialization 写入顺序。

# orch MCP 调用认知修复验收

日期：2026-09-08。评审对象：基于 `07b5ee1414758b009807e37600bb61a98c7be412` 的当前工作区修复，未提交。

范围：核心 agent MCP 调用闭环及 DAG 接口抽查。分数不代表整个仓库、所有工具或全部 provider 的生产就绪状态。

后续按用户要求扩大端到端验证，新增真实中断、消息超时恢复和活动停止场景，发现并修复了原评审未覆盖的公开运行态与进程回收问题。最新消息交付增强后，56 个登记场景按修复后的分组复验全部通过，无 skip；真实交付由 3 项扩展至 12 项。原完整运行的 outbox 时序断言失败已修正，控制组整体复验及该用例 30 次重复均通过；最新证据和边界见[端到端验证报告](orch-mcp-e2e-validation.md)。下文 98 分是此前范围内的复评记录，不能外推为全仓或全部 provider 的评分。

## 独立评分

使用用户指定的 Astra、低思考子代理独立复评。发现的 P3 批量规范化身份重复问题经红态复现、修复和独立复验后关闭，95 分阶段之后补齐参数发现、结构化恢复和真实 Codex 验证，最终独立复评 **98/100**。

| 项目 | 得分 |
|---|---:|
| 命名与发现 | 15/15 |
| 参数与身份 | 19/20 |
| 动作、等待与完成 | 20/20 |
| 返回可预测性 | 15/15 |
| 失败与重试 | 19/20 |
| 常见闭环 | 10/10 |

保留两分：高级身份/环境参数仍有理解成本；普通消息未知提交结果缺少本次请求的精确原生回执或幂等确认，结构化恢复只允许只读对账，不能授权自动重发。没有通过调整权重或忽略这些限制提升分数。

## 98 分阶段新增证据

- tools/list 发布标准 schema 用途标题、发现链和四类示例；真实 binary 调用 list_models、prompt_list、prompt_get 验证文案中的依赖与返回字段。
- standalone 专属公开 schema 要求显式 model；调用时在基础 handler 前拒绝缺失、null、空白或非字符串 model，防止先创建 agent 状态才失败。peer schema 保持原有模式约束。
- 消息错误在 structuredContent.meta.submission_recovery 中提供阶段、提交状态、真实身份、已有游标及只读工具参数；content.text 从同一 typed query 生成完整查询。字段守卫与最终 MCP JSON 验证覆盖真实零游标、缺游标、整数精度及别名隔离。
- 新编译 binary 的 TestStandaloneBinaryCodexLifecycleThroughMCP 使用真实 gpt-6-astra 低思考、隔离的临时 provider home 执行成功，耗时 66.820 秒。测试验证 shell 随机 challenge、cwd、报告游标、重复启动、父任务 fork 和同一 thread ID 的 native resume。首次尝试因 CLI 符号链接被显式执行文件契约拒绝，解析为真实文件路径后通过；未放宽该契约。
- stop/recover 描述已更正：stopped 运行态与 archived 持久状态可以同时存在，支持原生恢复的 provider 可以取消归档并恢复原线程。

## 已关闭发现

- 直接消息公开声明“持久化并提交接收者 turn”，说明 intent、receipt、completion 的区别及同 key 重试边界。
- 等待新报告必须提供明确游标；批量要求目标完整覆盖并拒绝多余、重复规范化身份。终态缺报告返回错误，不合成成功报告。
- 启动条件规则进入公开 inputSchema，并在 Registry 业务副作用前执行；提供常用调用示例，普通 agent 调用以 agent_id 为主。
- 八个 agent 工具具有具体 outputSchema；动态反射守卫比对嵌套 DTO 字段、类型、必填性，真实 handler 结果经过 Registry 校验。
- 列表统一对象，先过滤计数再截页，只补全返回页报告。负 limit、未知状态、envelope:false 明确拒绝。
- 消息必须解析到真实线程身份。提交结果未知与提交后的报告等待失败采用不同错误码，恢复提示避免重复发送。
- 内置编排提示同步新游标用法；[调用及迁移指南](orch-mcp-agent-contract.md) 说明旧调用方式需要的调整。

## 测试证据

通过的相关验证：

- `go test ./cmd/mcp-orch/tools ./cmd/mcp-orch ./internal/mcpserver/common -count=1`。
- `go test ./internal/platform/shared/builtinprompts -run '^TestDefaultRegistryDocumentsFocusedContextGuidance$' -count=1`。
- `go test -tags=e2e ./cmd/mcp-orch -run '^(TestStandaloneAgentContract|TestMCPOrchServeBlackBoxSingleInstance|TestStandaloneBinaryDirectPeerOperatorBoundary)$' -count=1 -timeout=180s`。从当前源码编译新 binary，验证 MCP schema、拒绝路径、默认列表输出、会话/实例边界、operator 授权边界和退出。
- `./scripts/test_with_guard.sh --light-guard-only`；相关文件 gopls diagnostics 无报错。
- 定向宿主脚本的代码守卫、架构检查及消息阶段错误/游标测试通过。宿主结果明确属于本地证据，不是远程发布凭据。

真实 fail-first：移除 launch 的 agent_type schema 登记，测试报告 missing/stale 字段；移除 snapshot 的 provider_source 登记，测试报告 snapshot/list 字段缺口；移除等待游标验证，旧报告被读取且测试失败；输入重复目标 `["a"," a "]` 初始触发两次读取，修复后在读取前拒绝。全部临时突变已经恢复。

## 扩大回归的原有失败（已修复）

最初扩大回归时，下列三个失败已在 `/tmp/orch-baseline-HEAD.F00vC0` 的独立 HEAD 归档源码中逐一重现。随后用户授权修复，三项均已关闭：

| 测试 | 根因与修复 |
|---|---|
| standalone 的 TestConfigStrictContract | 旧测试误将全部 Provider 字段视为必填。按 Codex/Claude 分别验证 auth_mode、sandbox 条件，并动态检查全部字段的缺失、null、错误类型和可选策略登记；生产配置契约未修改。 |
| standalone/managedruntime 的 TestCandidateSupervisorRemainsUnwired | 合法的只读 receipt/verifier 包导入被误判为候选 Supervisor 接线。改为 AST 检测候选类型及构造器引用，包含别名与 dot 导入；候选 Supervisor 仍禁止进入生产。 |
| builtinprompts 的 TestMainGeneralZhLSPSectionsUseWithLSPContract | lsp_basics 41 个非空行超过 40 行上限。压缩 JSON 示例排版至 38 个非空行，内容、字段、两文件差量示例和原守卫均保留。 |

修复后 `go test ./cmd/mcp-orch/... ./internal/platform/shared/builtinprompts -count=1` 整体退出码 0：78 个有测试包通过，11 个包无测试文件。原始和突变红态证明字段登记与候选接线守卫仍有效；临时突变均已恢复。日志为 `/tmp/orch-baseline-fixes-full-regression.log`。结论覆盖上述完整命令范围，仍不外推为全仓测试通过或可发布。

## 审查覆盖边界

Applied：D01/D03/D19 仅覆盖 Registry 到公共 MCP 出口；D02/D10/D11 覆盖参数、线程身份和错误恢复；D05/D06/D15 覆盖核心 agent 交互语义；D08 覆盖内置编排提示；D12/D17 覆盖行为与字段守卫；D14 覆盖列表分页后补报告；D16/D18 覆盖工作区差异及共享契约。

N/A：D04 未修改 LSP 产品工具；D07 未修改数据库或持久化 schema；D09 未修改前端；D13 未执行安装或发布验证。Applied 不表示该维度全仓审计通过。

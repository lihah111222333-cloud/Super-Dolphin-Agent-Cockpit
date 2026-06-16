# Round 026 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 06:40:03 KST
- 结束：2026-05-17 06:43:25 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 `inputs.from_nodes` / `inputs.from_sharedfiles` 注入路径，重点看 agent prompt 拼接、automation `__inputs` 参数注入、router 预取、输入大小与字段位是否真正生效。

- `cmd/mcp-orch/orchestration/nodeexec/inputs.go`
- `cmd/mcp-orch/orchestration/nodeexec/executor_agent.go`
- `cmd/mcp-orch/orchestration/nodeexec/executor_automation.go`
- `cmd/mcp-orch/orchestration/nodeexec/types.go`
- `cmd/mcp-orch/orchestration/nodeexec/config.go`
- `cmd/mcp-orch/orchestration/nodeexec/executor_agent_inputs_test.go`
- `cmd/mcp-orch/orchestration/nodeexec/executor_automation_test.go`
- `cmd/mcp-orch/orchestration/node_router.go`
- `cmd/mcp-orch/orchestration/node_router_shard18_test.go`
- `docs/design/F1-lifecycle-audit-2026-05-12.md`
- `docs/decisions/ADR-018-agent-output-materialization.md`

## Findings

1. **[critical] `inputs.summarization.max_tokens` 只是字段位，agent 和 automation 输入路径都不执行裁剪或摘要**
   - 证据：`InputsConfig` 包含 `Summarization *SummarizationConfig`，注释称“输入摘要/裁剪策略字段位”（`cmd/mcp-orch/orchestration/nodeexec/config.go:24-39`）。但 agent 的 `collectInputSections()` 只读取 `FromNodes` 和 `FromSharedfiles`，随后把原始结果拼入 prompt（`cmd/mcp-orch/orchestration/nodeexec/inputs.go:154-179`、`cmd/mcp-orch/orchestration/nodeexec/inputs.go:199-257`）；automation 的 `buildInputsPayload()` 同样只注入完整 prev/sharedfile 内容（`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:479-496`）。`rg` 未发现任何 `Summarization` 执行逻辑，只有 round-trip 测试（`cmd/mcp-orch/orchestration/nodeexec/config_test.go:34-91`）。
   - 风险：DAG 配置里写 `summarization.max_tokens` 会给用户“输入已受预算约束”的错觉，但真实执行会把所有上游 result 和 sharedfile 原文注入。量化任务常有大表、日志和回测报告，可能直接撑爆 agent 上下文或 shell command args，导致调度失败、模型截断，或把关键风险信号挤出 prompt。
   - 建议：在 H7 未实现前解析阶段拒绝非空 summarization，或至少把字段暴露为 `unsupported` validation；实现时应在 sharedfile 和 prev result 两条路径统一执行 byte/token budget、摘要策略和超限诊断。

2. **[major] agent 输入用裸文本 section 拼接，没有内容边界转义，sharedfile/prev result 可伪造后续 `[first_turn]` 或输入段落**
   - 证据：`loadFromNodes()` 直接写 `## node:<key>` 后追加 raw `json.RawMessage`（`cmd/mcp-orch/orchestration/nodeexec/inputs.go:199-219`）；`loadFromSharedfiles()` 直接写 `## sharedfile:<path>` 后追加文件 content（`cmd/mcp-orch/orchestration/nodeexec/inputs.go:237-255`）；`composePrompt()` 再用 `[first_turn]` 分隔真实指令（`cmd/mcp-orch/orchestration/nodeexec/inputs.go:260-279`）。没有 fenced block、length prefix、JSON envelope 或 escaping。
   - 风险：上游节点或 sharedfile 内容如果包含 `[first_turn]`、`[inputs.from_nodes]`、Markdown 标题或“忽略后续指令”等文本，会与系统生成的 section header 混在同一 prompt 中。对量化审查 DAG，这使上游产物可污染下游 agent 的任务边界，造成 prompt injection 或审查范围漂移。
   - 建议：把每个输入包成结构化 JSON/YAML block，至少使用 fenced code block 并转义 fence；在 agent system prompt 中明确这些块是只读数据，不是指令；更稳妥是通过 provider 的 structured input item 或 tool result channel 传递。

3. **[major] `from_nodes` 依赖上游 `task_dag_nodes.result`，但上游 sharedfile-only 输出会注入 `(empty)` 而不是自动解引用 sharedfile**
   - 证据：router 预取只复制 done 节点的 `Result` 列；若 `Result` 长度为 0，就填 nil（`cmd/mcp-orch/orchestration/node_router.go:259-280`）。agent 输入装载遇到 nil result 会写 `(empty)` 并继续 launch（`cmd/mcp-orch/orchestration/nodeexec/inputs.go:211-219`），测试明确锁定“上游可能未配 outputs.to_node_result”时继续执行（`cmd/mcp-orch/orchestration/nodeexec/executor_agent_inputs_test.go:317-343`、`cmd/mcp-orch/orchestration/node_router_shard18_test.go:192-225`）。
   - 风险：如果上游节点为了避免 4KB 限制只写 sharedfile 且没有在 result 中保留 path envelope，下游 `inputs.from_nodes` 会看到 `(empty)`，但节点仍会正常执行。这比 fail-loud 更危险：量化流水线可能在缺少回测结果、风险清单或指标文件的情况下继续产出最终结论。
   - 建议：把 sharedfile-only 输出规范成 result path envelope；`from_nodes` 发现 empty result 时应区分“上游确实空输出”和“输出在 sharedfile”，必要时自动解引用或 validation fail。

4. **[major] automation 与 agent 的输入语义不一致：同一 `from_nodes` 在 automation 中变成 JSON 对象，在 agent 中变成原文 prompt**
   - 证据：automation 的 `collectPrevResults()` 会尝试 `json.Unmarshal`，成功后注入 `__inputs.from_nodes.<key>` 对象；解析失败才退回原始字符串（`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:499-524`）。agent 的 `loadFromNodes()` 不解析 JSON，直接把 raw bytes 写进 prompt（`cmd/mcp-orch/orchestration/nodeexec/inputs.go:199-219`）。测试分别锁定 automation 看到 object（`cmd/mcp-orch/orchestration/nodeexec/executor_automation_test.go:229-277`）和 agent prompt 包含原文 JSON（`cmd/mcp-orch/orchestration/nodeexec/executor_agent_inputs_test.go:37-77`）。
   - 风险：DAG 作者以为 `inputs` 是跨 node_type 统一抽象，但同一个上游 JSON 对 agent 是文本，对 automation 是结构化对象。量化任务中如果 command_template 和 agent prompt 复用同一节点定义，字段缺失、模板访问失败或语义误读都很难在配置层提前发现。
   - 建议：明确 inputs contract 分层：`structured` 与 `prompt_text` 两种模式；或者 agent 也注入带 schema 的 JSON envelope，automation 也可选择 raw text，避免隐式类型转换。

5. **[moderate] 空字符串路径和空 node_key 被静默跳过，配置错误可能退化成空输入而非失败**
   - 证据：`loadFromNodes()` 对 trim 后空 key 直接 `continue`（`cmd/mcp-orch/orchestration/nodeexec/inputs.go:201-205`）；`loadFromSharedfiles()` 对空 path 也 `continue`（`cmd/mcp-orch/orchestration/nodeexec/inputs.go:239-243`）；automation 同样跳过空 key/path（`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:503-507`、`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:537-541`）。如果数组只含空字符串，`inputsConfigured()` 仍为 true，但最终 sections/injected 为空，可能继续执行。
   - 风险：配置生成器输出 `[""]`、多余逗号迁移或 UI 表单空项时，节点不会 fail-loud；下游 agent/automation 可能在缺输入状态继续执行，结果质量不可控。
   - 建议：解析层规范化数组并拒绝空元素；如果兼容历史配置，至少在 node outcome/result 中写 warning，并在 dashboard 暴露 skipped input refs。

## 误报与已覆盖项

- `from_nodes` 已按 run 隔离：router 按当前 `runID` 列节点并只填目标 run 的结果，测试覆盖 run A/run B 不串线（`cmd/mcp-orch/orchestration/node_router_shard18_test.go:106-157`）。
- 未 done 的上游节点不会被注入：router 过滤非 done，nodeexec 随后 validation fail，测试覆盖 running 上游被拒绝（`cmd/mcp-orch/orchestration/node_router.go:263-280`、`cmd/mcp-orch/orchestration/node_router_shard18_test.go:159-190`）。
- sharedfile reader 未注入或文件不存在会 fail-loud，测试覆盖 nil reader 与 missing path（`cmd/mcp-orch/orchestration/nodeexec/executor_agent_inputs_test.go:244-315`、`cmd/mcp-orch/orchestration/nodeexec/executor_automation_test.go:329-372`）。

## 验证

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration ./cmd/mcp-orch/orchestration/nodeexec -count=1
```

结果：Go guard、`internal/archtest`、`cmd/mcp-orch/orchestration` 与 `cmd/mcp-orch/orchestration/nodeexec` 通过。

## 下一轮建议

- Round 027 审查 command card 渲染与 shell 执行路径，重点看模板注入、shell quote、工作目录、环境继承、timeout 与非零退出分类。

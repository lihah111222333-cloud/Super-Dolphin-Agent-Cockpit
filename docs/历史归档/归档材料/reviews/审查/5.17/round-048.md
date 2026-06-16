# Round 048 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 07:46:19 KST
- 结束：2026-05-17 07:56:08 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 `nodeexec` 的 DAG 节点 schema、ApplyOps plan、CreateDAG tool 输入映射，以及 node router 的执行入口，重点看量化自动化节点在进入调度前是否能被同源校验。

- `.agent/skills/安全工程师/SKILL.md`
- `.agent/skills/Agent工程学/SKILL.md`
- `internal/sidecar/orch/orchestration/nodeexec/ops.go`
- `internal/sidecar/orch/orchestration/nodeexec/plan.go`
- `internal/sidecar/orch/orchestration/nodeexec/config.go`
- `internal/sidecar/orch/orchestration/nodeexec/stubs.go`
- `internal/sidecar/orch/orchestration/nodeexec/config_test.go`
- `internal/sidecar/orch/orchestration/dag.go`
- `internal/sidecar/orch/orchestration/dag_query.go`
- `internal/sidecar/orch/orchestration/node_router.go`
- `internal/sidecar/orch/tools/task_tools.go`

## Findings

1. **[major] CreateDAG 工具丢弃 automation 所需 exec config，只保留旧 execution overrides**
   - 证据：`CreateDAGNodeInput` 暴露 `node_type`、`command_ref` 和 `execution`，但没有完整 `config` 字段（`internal/sidecar/orch/tools/task_tools.go:45-53`）。映射时 `createDAGNodesFromInput()` 只把 `node.Execution` 编成 `{"execution": ...}`，并把 `command_ref` 写入独立列（`internal/sidecar/orch/tools/task_tools.go:501-525`）。真正的 automation executor 要从 `node.config.exec.command_ref` 读取命令引用，缺失就 validation fail（`internal/sidecar/orch/orchestration/nodeexec/executor_automation.go:226-243`）。
   - 风险：通过 MCP `task_create_dag` 创建 `node_type=automation` 的量化节点时，即使调用方填了 `command_ref`，runtime node 的执行视图只包含 `Config`，不会把独立列 `CommandRef` 传给 executor（`internal/sidecar/orch/orchestration/node_router.go:145-152`）。结果是创建成功、启动成功、派发时才失败，风险从设计期延迟到运行期。
   - 建议：`task_create_dag` 对 automation 节点直接生成 `config.exec.command_ref`，或 node router 在构造 `nodeexec.Node` 前把兼容列 `command_ref` 合并进 config；同时给 CreateDAG tool 增加 automation 端到端测试。

2. **[major] ApplyOps add_node 只校验图结构，不校验 node_type/config 是否可执行**
   - 证据：`NodeSpec` 注释说 `Config` 由 `ParseNodeConfig` 解码（`internal/sidecar/orch/orchestration/nodeexec/ops.go:49-57`），但 `PlanAddNodes()` 只校验 `node_key`、重复、self-dependency 和 depends_on 引用（`internal/sidecar/orch/orchestration/nodeexec/plan.go:45-61`、`internal/sidecar/orch/orchestration/nodeexec/plan.go:76-115`）。`persistAddNodeSpecs()` 随后直接写库（`internal/sidecar/orch/orchestration/dag_query.go:731-745`）。
   - 风险：agent 可以把未知 `node_type`、automation 空 `command_ref`、hybrid 未实现配置写进 DAG，直到 wakeup 执行才被 router 或 executor 标失败。量化 DAG 的无效补丁会污染版本历史，并可能让 scheduled run 周期性失败。
   - 建议：plan 阶段调用 `ParseNodeConfig(spec.NodeType, spec.Config)`，并针对 automation/hybrid 增加必填字段与已实现能力校验。

3. **[major] node_type 空串会默认 agent，未知值到运行时才失败**
   - 证据：CreateDAG 工具 schema 把 `node_type` 标为可选字符串（`internal/sidecar/orch/tools/task_tools.go:450-464`），service 写入时只 trim，不设置枚举默认（`internal/sidecar/orch/orchestration/dag.go:317-327`）。router 的 `resolveNodeType()` 只把空串改为 `"agent"`，未知值原样返回（`internal/sidecar/orch/orchestration/node_router.go:341-348`），最后 `dispatchByNodeType()` 才将未知值转成 validation outcome（`internal/sidecar/orch/orchestration/node_router.go:355-369`）。
   - 风险：拼写错误如 `automatoin` 可以进入模板、被 snapshot 到 run，并在调度时失败；空 node_type 则会意外走 agent executor。量化自动化节点对执行通道很敏感，默认 agent 可能造成错误的交互式 agent 任务被启动。
   - 建议：CreateDAG/ApplyOps 入库前把 node_type 限定为 `agent|automation|hybrid`，并要求调用方显式指定 automation；兼容旧空值可在迁移层处理，而不是对新写入继续静默默认。

4. **[major] hybrid 节点对外可创建，但 router 与 stub 语义冲突**
   - 证据：`ParseNodeConfig` 接受 `hybrid`，测试也覆盖 hybrid dispatch config（`internal/sidecar/orch/orchestration/nodeexec/config.go:153-176`；`internal/sidecar/orch/orchestration/nodeexec/config_test.go:205-270`）。但 router 对 `node_type=hybrid` 直接返回 validation failure “not yet implemented”（`internal/sidecar/orch/orchestration/node_router.go:355-366`）。同包还存在 `HybridExecutor` stub，`Execute()` 返回 `NodeStatusDone`（`internal/sidecar/orch/orchestration/nodeexec/stubs.go:13-19`）。
   - 风险：调用方看到 schema/parse 支持 hybrid，可能把高风险量化操作设计成 hybrid verifier；实际 router 永远失败，stub 又在单测或未来接线时表现为直接 done，两个语义都不能提供真实验证。
   - 建议：在对外 schema/plan 阶段禁用 hybrid，直到真实 executor 落地；删除或改造 `HybridExecutor` stub，避免任何路径误接后直接成功。

5. **[moderate] Automation config 解析不使用 strict JSON，拼写错误会静默降级**
   - 证据：`ParseAutomationConfig()` 直接 `json.Unmarshal` 到 struct，没有 `DisallowUnknownFields`（`internal/sidecar/orch/orchestration/nodeexec/config.go:191-210`）。现有测试覆盖 round-trip、kind 默认和未知 kind，但没有覆盖未知字段拒绝（`internal/sidecar/orch/orchestration/nodeexec/config_test.go:105-178`）。
   - 风险：`command_ref` 拼错成 `commandRef`、`outputs.to_sharedfile` 拼错等配置会被静默丢弃，最终变成运行期 validation 或默认写 node.result。对自动量化命令来说，这会把“配置错误”伪装成“命令失败”或“输出路径缺失”。
   - 建议：对 node config 解析使用 strict decoder；需要兼容 legacy 字段时显式写 alias 迁移，而不是允许任意未知字段。

## 误报与已覆盖项

- update_node patch 顶层和 config 深层会拒绝 `status/node_key/node_type/agent_key` 等禁改字段，本轮不重复报告这类防护缺失（`internal/sidecar/orch/orchestration/nodeexec/ops.go:127-165`）。
- `PlanUpdateNodes` 会限制更新 pending/ready 节点，并校验 depends_on 引用与自依赖；本轮关注的是 add_node 和 CreateDAG 的可执行语义校验缺口（`internal/sidecar/orch/orchestration/nodeexec/plan.go:180-201`）。
- automation executor 运行时会检查 `command_ref` 必填，本轮风险是该错误没有在 DAG 写入阶段被拦截（`internal/sidecar/orch/orchestration/nodeexec/executor_automation.go:240-243`）。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/sidecar/orch/orchestration/nodeexec ./internal/sidecar/orch/orchestration ./internal/sidecar/orch/tools -count=1
```

结果：通过。

## 下一轮建议

- Round 049 审查 `node_router` 的 RunContext 构造、prev_results/sharedfile 预取和 runtime node scope，确认多 run 并发下输入读取是否隔离。

# DAG 计划索引

本目录只作为本次主线 DAG 改造相关文档的集中索引，不复制既有正文。

源文件仍以仓库原始 `docs/...` 路径为准。后续维护计划、ADR 或设计审计时，只改源文件；本索引只在文档增删或范围变化时更新。

## 主线计划

- [DAG 改造蓝图 v2](../../../plans/dag改造蓝图v2.md)：DAG 改造总蓝图、产品边界、final_output/sharedfile 定位。
- [DAG 改造实施计划](../../../plans/dag改造实施计划.md)：主线阶段状态、F/H 项进度、M3 dogfood、H14/H15 记录。
- [DAG agent 节点 lifecycle 闭环 C-A 实施计划](../../../plans/dag-lifecycle-c-a-implementation.md)：C-A lifecycle 闭环计划及 C1/C2/C3/A1/A2 落地记录。
- [DAG UI 决策台账](../../../plans/dag-ui-decision-ledger.md)：DAG UI 已锁边界、U1/U2 完成状态、后续 UI 拍板入口和推荐顺序。
- [DAG Console v1 实施计划](2026-05-23-dag-console-v1-narrow-plan.md)：U1 最小 UI/RPC 已落地并封口；后续只作为范围边界和验证清单。

## 契约与审计

- [ADR 0001: DAG v2 骨架阶段契约](../../../adr/0001-dag-v2-contracts.md)：DAG v2 骨架阶段固化契约。
- [F1.x agent 节点 lifecycle 设计审计](../../../design/F1-lifecycle-audit-2026-05-12.md)：C-A 路线的前置问题审计。

## DAG 相关 ADR

- [ADR-003: MCP 工具 input enum 校验](../../../decisions/ADR-003-mcp-input-enum-validation.md)
- [ADR-004: F6.4 dispatcher 对无 assignee 节点跳过自动 wakeup](../../../decisions/ADR-004-f6.4-dispatcher-no-assignee.md)
- [ADR-005: F4.5 与 F6.5 running 语义](../../../decisions/ADR-005-f4.5-f6.5-running-semantics.md)
- [ADR-006: outputs.to_node_result 4KB size cap](../../../decisions/ADR-006-to-node-result-size-cap.md)
- [ADR-007: automation.kind 多 kind 渐进开通策略](../../../decisions/ADR-007-automation-kind-progressive.md)
- [ADR-008: FailureClass 映射规则全集](../../../decisions/ADR-008-failureclass-mapping.md)
- [ADR-009: DAG node 与 child thread 双向追溯](../../../decisions/ADR-009-thread-dag-traceability.md)
- [ADR-010: DAG 上下文与 token budget 兜底阈值](../../../decisions/ADR-010-dag-context-token-budget.md)
- [ADR-011: HybridExecutor 拓扑边界与未来扩展](../../../decisions/ADR-011-hybrid-executor-topology.md)
- [ADR-012: taskdag 聚合 Store 接口预算锁死](../../../decisions/ADR-012-store-aggregate-frozen.md)
- [ADR-014: prompt_template-first 路线冻结](../../../decisions/ADR-014-prompt-template-first-automation-frozen.md)
- [ADR-015: provider TurnCompleted.Result 补完](../../../decisions/ADR-015-provider-turn-completed-result.md)
- [ADR-016: spawned agent 自动 stop](../../../decisions/ADR-016-spawned-agent-auto-stop.md)
- [ADR-017: DAG turn.completed subscriber + thread.stopped fallback](../../../decisions/ADR-017-dag-turn-completed-subscriber.md)
- [ADR-018: agent 节点真实输出物化](../../../decisions/ADR-018-agent-output-materialization.md)
- [ADR 总索引](../../../decisions/README.md)

## 不收录范围

- 源码、测试、migration、sqlc 生成文件不放进本目录；这些仍以实施计划中的落点表和原模块路径为准。
- `docs/plans/迁移/**` 的早期迁移材料默认不列入本索引，除非后续需要追溯历史设计。

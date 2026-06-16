# V3 架构决策记录（Architecture Decision Records）

> 更新日期：2026-05-23

本目录用于沉淀 V3 的正式架构决策记录（ADR），只记录已经裁决、需要长期引用的技术选择，不替代执行计划、review 记录或 session-summary。

> ⚠️ **双 ADR 体系说明**：项目另有 `docs/adr/0001-dag-v2-contracts.md` 单文件，用于 DAG v2 专项骨架阶段契约固化，**不混入本目录**。两者命名体系不同：`docs/adr/NNNN-*.md` vs `docs/decisions/ADR-NNN-*.md`。勿误将「ADR 0001」与「ADR-001」互指——前者是 DAG v2 骨架契约，后者是 runtime memory 检索。

## 当前 ADR 清单

### Accepted

- [ADR-001：V3 采用 runtime memory 检索（保留对 Claude 原生"模型主动检索"的架构性偏离）](./ADR-001-runtime-memory-retrieval.md)
- [ADR-002：V3 固化 dependency-aware cache 三分法（放弃 Claude name-only）](./ADR-002-dependency-aware-cache.md)
- [ADR-003：MCP 工具 input enum 校验落 handler 层（A+ 方案）](./ADR-003-mcp-input-enum-validation.md)
- [ADR-004：F6.4 dispatcher 对无 assignee 节点跳过自动 wakeup（方案 A）](./ADR-004-f6.4-dispatcher-no-assignee.md)
- [ADR-005：F4.5 与 F6.5 联动 —— 多 run 并发后 running 受限的判定主体](./ADR-005-f4.5-f6.5-running-semantics.md) — 2026-05-23 状态回写为 Accepted（F4.5 / F6.5 已落地，running edit guard 与 run_id 隔离并存）
- [ADR-006：outputs.to_node_result 超 size_cap 的处理策略（方案 A：4KB validation + 建议 to_sharedfile，2026-05-12 端口收敛 batch 升 Accepted）](./ADR-006-to-node-result-size-cap.md)
- [ADR-007：automation.kind 多 kind 渐进开通策略（方案 A：command_card → webhook → http → shell）](./ADR-007-automation-kind-progressive.md)
- [ADR-008：FailureClass 七类的映射规则全集](./ADR-008-failureclass-mapping.md) — 2026-05-12 升 Accepted（代码引用 ≥5 处 + 行为稳定数轮）
- [ADR-009：DAG node ↔ child thread 双向追溯（spawning_thread_id）](./ADR-009-thread-dag-traceability.md) — 2026-05-12 升 Accepted（F1.5 落地 + DTO 透出 + archtest 守护）
- [ADR-010：DAG 上下文与 token budget 兜底阈值](./ADR-010-dag-context-token-budget.md) — 2026-05-23 状态回写为 Accepted（M3 backend dogfood 已按硬阈值验收；H7/H8 实装仍按需）
- [ADR-011：HybridExecutor 拓扑边界与未来扩展](./ADR-011-hybrid-executor-topology.md) — 2026-05-12 v1 升 Accepted（F3.1 等同 AutomationWithVerifier 语义稳定）；v2 拓扑（F3.2/F3.3/F3.4）仍 Proposed
- [ADR-012：taskdag 聚合 Store 接口 7/7 嵌入端口预算锁死](./ADR-012-store-aggregate-frozen.md) — 2026-05-12 Accepted（禁止再向聚合 Store 加 embedded port，新端口走独立窄接口 + var _ 编译期断言）
- [ADR-014：prompt_template-first 路线 / automation kind 冻结在 command_card](./ADR-014-prompt-template-first-automation-frozen.md) — 2026-05-12 Accepted（prompt_template-first 主路线，command_card 冻结为辅路线）
- [ADR-015：codex/claude provider TurnCompleted.Result 补完（C1 + C2）](./ADR-015-provider-turn-completed-result.md) — 2026-05-12 v4.1 升 Accepted（`f923ebd7`；C1/C2 provider 层真实输出补完）
- [ADR-016：DAG agent 节点完成后 spawned agent 自动 stop（C3）](./ADR-016-spawned-agent-auto-stop.md) — 2026-05-12 v1.2 升 Accepted（`cddb3ea2`；stop helper + metric + e2e）
- [ADR-017：DAG turn.completed subscriber + thread.stopped fallback（A1）](./ADR-017-dag-turn-completed-subscriber.md) — 2026-05-12 v1.2 升 Accepted（`00864aa7`；subscriber lifecycle 闭环 + fallback）
- [ADR-018：agent 节点真实输出物化（A2）](./ADR-018-agent-output-materialization.md) — 2026-05-13 升 Accepted（`3e70e468` + review-fix `02009e22`；复用 `CompleteNodeAndScheduleDownstream`，sharedfile 路径新增 materialization claim fence）

### Proposed（DAG 改造修补单 §7 占位 + 返修轮新立，待拍板）

- [ADR-011 v2 部分（F3.2/F3.3/F3.4）](./ADR-011-hybrid-executor-topology.md#) — 占位，拍板后拆分为 ADR-011a/b/c

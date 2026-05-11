# V3 架构决策记录（Architecture Decision Records）

> 更新日期：2026-05-11

本目录用于沉淀 V3 的正式架构决策记录（ADR），只记录已经裁决、需要长期引用的技术选择，不替代执行计划、review 记录或 session-summary。

> ⚠️ **双 ADR 体系说明**：项目另有 `docs/adr/0001-dag-v2-contracts.md` 单文件，用于 DAG v2 专项骨架阶段契约固化，**不混入本目录**。两者命名体系不同：`docs/adr/NNNN-*.md` vs `docs/decisions/ADR-NNN-*.md`。勿误将「ADR 0001」与「ADR-001」互指——前者是 DAG v2 骨架契约，后者是 runtime memory 检索。

## 当前 ADR 清单

### Accepted

- [ADR-001：V3 采用 runtime memory 检索（保留对 Claude 原生"模型主动检索"的架构性偏离）](./ADR-001-runtime-memory-retrieval.md)
- [ADR-002：V3 固化 dependency-aware cache 三分法（放弃 Claude name-only）](./ADR-002-dependency-aware-cache.md)
- [ADR-003：MCP 工具 input enum 校验落 handler 层（A+ 方案）](./ADR-003-mcp-input-enum-validation.md)
- [ADR-004：F6.4 dispatcher 对无 assignee 节点跳过自动 wakeup（方案 A）](./ADR-004-f6.4-dispatcher-no-assignee.md)

### Proposed（DAG 改造修补单 §7 占位，待 F 阶段拍板）

- [ADR-005：F4.5 与 F6.5 联动 —— 多 run 并发后 running 受限的判定主体](./ADR-005-f4.5-f6.5-running-semantics.md)
- [ADR-006：outputs.to_node_result 超 size_cap 的处理策略](./ADR-006-to-node-result-size-cap.md)
- [ADR-007：automation.kind 多 kind 渐进开通策略](./ADR-007-automation-kind-progressive.md)
- [ADR-008：FailureClass 七类的映射规则全集](./ADR-008-failureclass-mapping.md)


# 架构决策记录

本目录保存架构决策及提案。每份 ADR 自己声明状态；只有状态为 Accepted 的决策才属于当前约束，Proposed 文档不能当作已生效事实。

| ADR | 状态 | 主题 |
| --- | --- | --- |
| [0001](0001-workflow-runtime-kernel.md) | Accepted | Workflow Runtime Kernel |
| [0002](0002-session-ports-and-prefix-stability.md) | Proposed | Session Ports And Prefix Stability |
| [0003](0003-mcp-tool-lifecycle-owner-storage.md) | Accepted | MCP Tool Lifecycle Owner And Storage |
| [Writer Preview Contract Spike](2026-06-30-writer-preview-contract-spike.md) | Accepted as ADR-only spike | 仅接受调研结论，生产 preview contract 延后 |

## 维护规则

- 新 ADR 必须写明状态、背景、决策、影响和回滚或替代条件。
- 状态变化时同步更新本索引。
- ADR 与源码或测试不一致时，先确认实现事实，再明确是修实现、修 ADR，还是新增替代 ADR。

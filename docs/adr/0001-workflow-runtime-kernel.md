# ADR 0001: Workflow Runtime Kernel

日期：2026-06-22

状态：Accepted

## 背景

Super-Dolphin 已有 DAG v2 和 `cmd/mcp-orch` 编排能力。后续工作流能力需要先冻结运行时术语和状态边界，避免模板、MCP tools、UI 和 runtime 各自解释状态。

本 ADR 只记录当前 Runtime Foundation 决策，不引入新的 backend。

## 决策

1. `cmd/mcp-orch` 是当前 Workflow Runtime Kernel owner。
2. DAG v2 是当前工作流定义、运行实例和节点快照模型。
3. `internal/module/workflowtemplate` 只负责模板目录、草稿渲染和预览输入，不承担 run/node 持久化、调度或状态迁移。
4. MCP tools 是 runtime adapter。工具负责 decode、输入校验和响应编码，核心运行状态仍以 `cmd/mcp-orch/orchestration` 与 `cmd/mcp-orch/store/taskdag` 为事实来源。
5. `waiting_for_assignee` 是派生展示状态，不写入 `task_dag_nodes.status`。来源只能是 `ready + assigned_to == ""` 或 `task_start_dag` 的 `execution_state`。
6. 持久 run status 对齐 DB CHECK：`running`、`succeeded`、`failed`、`cancelled`。
7. `hybrid`、`waiting_human`、`skipped` 和 `awaiting_verify` 属于保留或 legacy 能力。runtime 闭环前不得作为新的用户可创建可执行能力暴露。
8. Temporal 只保留为未来 backend 选项。当前阶段不引入 Temporal、外部队列或新的 workflow backend 抽象。

## 当前映射

| 领域名 | 当前代码/存储映射 | 说明 |
| --- | --- | --- |
| `WorkflowDefinition` | `task_dags` | 可运行 DAG 定义，不是模板草稿。 |
| `WorkflowVersion` | `task_dags.version` | run 启动时快照版本。 |
| `WorkflowRun` | `task_dag_runs` | 一次运行实例。 |
| `WorkflowNodeRun` | `task_dag_nodes.run_id != NULL` | 运行中的节点快照。 |
| `Wakeup` | `task_dag_wakeups` | 调度任务。 |
| `Lease` | wakeup claim 字段、worker lease store | 当前已有基础，fencing/TTL 语义后续继续收敛。 |
| `ActivityExecutor` | `nodeexec` router/executor | Agent 和 Automation 已有；Hybrid 未闭环。 |

## 后果

- 新文档、测试、schema 和 UI 文案必须区分持久状态与派生展示状态。
- `waiting_for_assignee` 只能作为展示或诊断输出出现，不能进入持久 node status 列表。
- 未实现的 Hybrid/HITL 能力应隐藏或 fail-fast，不能创建“看似可执行但 runtime 不闭环”的 DAG。
- 未来如引入 Temporal，需要新的 ADR 和迁移设计，不能在当前 Runtime Kernel 内静默替换。

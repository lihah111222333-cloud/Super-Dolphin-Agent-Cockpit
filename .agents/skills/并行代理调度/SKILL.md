---
name: 并行代理调度
description: 当用户以 并行代理调度 名称要求在 super-agent-v3 中并发审查、调查或修复多个独立任务时使用。
aliases: ["@并行代理调度", "@调度并行代理", "@parallel-agent-orchestration"]
---

# 并行代理调度

这是 `调度并行代理` 的同名兼容入口。核心要求：

- 每个独立任务一个子代理或任务节点。
- 平台原生并行代理可直接使用；不要因为没有 mcp-orch `task_*` 工具就阻断派发。
- 只有需要持久 DAG、重试、租约或结构化交接记录时，才可选使用 `task_create_dag`、`task_start_dag`、`task_dispatch_node`、`task_update_node`。
- 不适合派发子代理时，改为单代理只读分析或当前会话执行，并说明观测限制。

不要只用聊天摘要代替任务状态；使用 `update_plan`、子代理返回摘要，或在已选择 mcp-orch 时写回 DAG 状态。

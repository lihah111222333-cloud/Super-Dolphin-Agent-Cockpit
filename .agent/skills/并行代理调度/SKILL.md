---
name: 并行代理调度
description: 当用户以 并行代理调度 名称要求在 super-agent-v3 中并发审查、调查或修复多个独立任务时使用。
aliases: ["@并行代理调度", "@调度并行代理", "@parallel-agent-orchestration"]
---

# 并行代理调度

这是 `调度并行代理` 的同名兼容入口。核心要求：

- 先用 `task_create_dag` 建 DAG。
- 每个独立任务一个 node。
- 用 `task_start_dag` 启动 run；需要派发 ready node 时用 `task_dispatch_node`。
- 用 `task_update_node` 写入 `running` / `done` / `failed` / `blocked`。
- 如果当前 Codex 没有暴露 mcp-go-agent-orchestration 工具，先向用户说明限制，不要启动子代理；只能改为单代理只读分析或等待工具可用。

不要只用聊天摘要代替 DAG 状态。

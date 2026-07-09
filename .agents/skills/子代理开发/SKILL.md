---
name: 子代理开发
description: 当用户以 子代理开发 名称要求在 super-agent-v3 中用子代理执行实现计划、审查或修复任务时使用。
aliases: ["@子代理开发", "@子代理驱动开发", "@subagent-driven-development"]
---

# 子代理开发

这是 `子代理驱动开发` 的同名兼容入口。执行规则：

- 每个实现者、规格审查者、代码质量审查者都可以用平台原生子代理直接派发；不要把生命周期强制绑定到 mcp-orch。
- 只有任务需要持久 DAG、重试、租约或结构化交接记录时，才可选使用 `task_create_dag`、`task_start_dag`、`task_dispatch_node`、`task_update_node`。
- 先规格符合性审查，再代码质量审查。
- 审查发现问题必须回到实现 node 修复并复审。
- 没有 mcp-orch `task_*` 工具不是阻断条件；使用当前平台可用的子代理能力，或在不适合派发时改为当前会话审查并说明观测限制。

详细流程见 repo-local `子代理驱动开发` 技能。

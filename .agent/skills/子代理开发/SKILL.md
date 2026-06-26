---
name: 子代理开发
description: 当用户以 子代理开发 名称要求在 super-agent-v3 中用子代理执行实现计划、审查或修复任务时使用。
aliases: ["@子代理开发", "@子代理驱动开发", "@subagent-driven-development"]
---

# 子代理开发

这是 `子代理驱动开发` 的同名兼容入口。执行规则：

- 每个实现者、规格审查者、代码质量审查者都必须是 mcp-orch DAG node。
- 启动前 `task_create_dag`，执行 run 时 `task_start_dag`，需要指派 ready node 时 `task_dispatch_node`，状态变化时 `task_update_node`。
- 先规格符合性审查，再代码质量审查。
- 审查发现问题必须回到实现 node 修复并复审。
- 没有 mcp-go-agent-orchestration 工具时先说明限制，不要启动子代理。

详细流程见 repo-local `子代理驱动开发` 技能。

---
name: MCP服务器构建
description: 当用户以 MCP服务器构建 名称要求在 super-agent-v3 中新增、维护或审查 MCP sidecar/tool/server 时使用。
aliases: ["@MCP服务器构建", "@MCP协议"]
---

# MCP 服务器构建

这是 `MCP协议` 的同名兼容入口。本仓库不要套用通用 mark3labs 示例或旧项目 server 模板：

- sidecar：`cmd/mcp-orch`、`cmd/mcp-lsp`、`cmd/mcp-ida`。
- 协议层：`internal/mcpserver/common`。
- 当前工具执行主路径：stdio MCP。
- `legacy HTTP` 仅兼容旧调用方或 peer mode 包装。
- DAG 工具遵守 `task_create_dag`、`task_dag_apply_ops`、`task_update_node` 状态/版本约束。

验证按改动包运行 `./scripts/test_with_guard.sh <packages> -count=1`；SQL/store 变更追加 `make sqlc-verify`。

---
name: 架构设计
description: 当在 super-agent-v3 中评估或修改模块边界、依赖方向、fx 装配、MCP sidecar、provider/runtime、store 或 frontend 架构时使用。
aliases: ["@架构设计", "@architecture"]
---

# super-agent-v3 架构设计

## 当前结构

- `cmd/agent-terminal`：Wails desktop host + HTTP/RPC bridge。
- `cmd/mcp-orch`：orchestration、DAG、cron、toolbridge peer。
- `cmd/mcp-lsp`：generic multi-language LSP MCP peer。
- `internal/app`：应用装配。
- `internal/contract`：跨模块接口和 DTO。
- `internal/module`：thread、prompt、memory、cron、skill 等业务模块。
- `internal/platform`：db、rpc、config、runtime safety、toolbridge。
- `internal/provider`：Claude/Codex/provider adapter。
- `internal/store`：SQLite + sqlc persistence。
- `frontend-app`：当前 React/Vite UI。

## 设计规则

1. 不存在独立后端子模块，不要套用旧项目的数据库 ORM、业务领域或单体目录映射。
2. 依赖方向优先遵守 `docs/契约/modularity-convention.md`。
3. runtime 装配优先看 fx module 和现有 constructor pattern。
4. MCP 工具壳在 `cmd/mcp-*`，通用协议在 `internal/mcpserver/common`。
5. provider mirror 是生成物；skill canonical truth 在 `.agents/skills`，历史 `.agent/skills` 不作为入口。
6. 当前前端是 `frontend-app`；旧 Vue 前端已删除，不再作为编辑或验证目标。

## 验证

架构/guard 改动跑：

```bash
./scripts/test_with_guard.sh ./internal/archtest -count=1
make guard
```

跨模块 Go 改动按受影响包追加 `./scripts/test_with_guard.sh <packages> -count=1`。

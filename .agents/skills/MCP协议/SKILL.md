---
name: "MCP协议"
display_name: "MCP协议"
description: "仅当用户明确点名 `MCP协议` 技能时使用。"
disable_model_invocation: true
---

# MCP 协议与服务模式

## 仓库事实

super-agent-v3 的 MCP 实现以源码和契约为准：

- sidecar 入口：`cmd/mcp-orch`、`cmd/mcp-lsp`、`cmd/mcp-ida`。
- 通用协议层：`internal/mcpserver/common`，包括 stdio transport、JSON-RPC server、tool provider、legacy HTTP transport。
- 当前工具执行主路径是 **stdio MCP**；`legacy HTTP` 仅保留给旧调用方或 peer mode 包装，不应作为新功能默认路径。
- mcp-orch 负责编排、DAG、workspace、prompt、command card、shared file 等工具出口；DAG 工具必须遵守 `task_create_dag` / `task_dag_apply_ops` / `task_update_node` 的状态与版本约束。
- mcp-lsp 是 generic multi-language LSP peer，不要把它写成单语言服务。

## 何时使用

- 新增或修改 `cmd/mcp-*` 工具、schema、handler、manifest、bootstrap 或 transport。
- 排查 stdio MCP framing、stdout 污染、Content-Length、JSON-RPC 错误码、tool payload envelope。
- 修改 provider 侧 MCP server config、turn manifest、stdio command allowlist、HTTP/stdio 配置合并。
- 审查 mcp-orch DAG/wakeup/lease 工具是否正确写入状态。

## 快速参考

| 场景 | 正确入口 |
|---|---|
| 新增 mcp-orch 工具 | `cmd/mcp-orch/tools` + registry/provider 映射 + 同包测试 |
| 新增 mcp-lsp 工具 | `cmd/mcp-lsp/tools` + `cmd/mcp-lsp/tools.go` 注册 + LSP/handler 测试 |
| 协议层变更 | `internal/mcpserver/common`，同时验证 stdio 与 legacy HTTP 行为是否保持兼容 |
| provider MCP 配置 | `internal/provider/shared/config_helpers.go`、`internal/module/turn/*manifest*` |
| DAG 生命周期 | `cmd/mcp-orch/orchestration`、`cmd/mcp-orch/store/taskdag`、`cmd/mcp-orch/sql/queries/task_dag*` |

## 实现规则

1. stdout 属于 MCP stdio 帧；普通日志、panic、fmt 调试输出必须走 stderr 或日志文件。
2. 工具参数必须 schema-first、强校验、fail-fast；不要用 `map[string]any` 静默吞字段。
3. stdio 和 legacy HTTP 共享语义时，错误 envelope、payload 日志和 scope 不能分叉。
4. provider 配置只接受 `stdio` 或 `http` transport；未知 transport 必须报错。
5. 修改 DAG 工具时必须处理版本冲突、running/active run 下的节点结构变更限制，以及 done/failed 状态的下游影响。
6. server identity、transport、provider 和必需依赖为空时必须在构造或启动边界报错；不得用 `mcp-server`、`dev` 或空实现补成可运行状态，并补失败测试。

## 验证

- Go 文件变更后先跑单文件守卫：`./scripts/test_with_guard.sh <file.go>`。
- MCP sidecar/tool contract 改动至少跑受影响包：`./scripts/test_with_guard.sh ./cmd/mcp-orch ./internal/mcpserver/common -count=1`，按实际改动替换包列表。
- provider/turn manifest 改动跑对应 provider/module 包测试。
- 如果改了 SQL/store，追加 `make sqlc-verify`。

## 常见错误

| 错误 | 修正 |
|---|---|
| 把 HTTP transport 当新工具默认路径 | 新工具默认 stdio MCP；legacy HTTP 只保持兼容 |
| stdout 打印日志 | 改为 stderr/log，避免破坏 MCP 帧 |
| tool handler 接受任意 map 后默认空值 | 明确 schema 和字段校验，缺字段立即报错 |
| 手写“已排程”但只改 metadata | scheduled DAG 必须通过 `task_dag_apply_ops(update_dag)` 写 trigger/cron |
| 在 running DAG 上 add/update/remove node | 结构变更应 fail-fast；只允许受支持的 future metadata 更新 |

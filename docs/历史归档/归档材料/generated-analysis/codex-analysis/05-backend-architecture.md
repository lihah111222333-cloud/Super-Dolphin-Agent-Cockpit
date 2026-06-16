# 后端架构分析

## 1. 本阶段目标

分析 Go 后端入口、模块分层、RPC、provider、MCP、错误处理和业务逻辑分布。

## 2. 已读取文件

- `cmd/agent-terminal/main.go`
- `cmd/mcp-orch/main.go`
- `cmd/mcp-lsp/main.go`
- `internal/app/app.go`
- `internal/app/modules.go`
- `internal/platform/rpc/*.go`
- `internal/module/thread/*`
- `internal/module/turn/*`
- `internal/provider/unified/*`
- `internal/sidecar/orch/tools/*.go`
- `internal/sidecar/lsp/tools.go`

## 3. 关键发现

- `cmd/agent-terminal` 设置进程角色、加载 packaged runtime/video env，然后调用 `app.RunDesktop(frontendDistFS())`。
- `cmd/mcp-orch` 和 `cmd/mcp-lsp` 都设置 sidecar 角色并保护 MCP stdio：stdout 专用于协议，其它输出重定向 stderr。
- `internal/app/modules.go` 是后端总装配点，业务模块和基础设施通过 Fx 连接。
- RPC 采用 jrpc2，handler 链包含 thread scope、strict handler、capability gate、invalid params/capability error mapper。
- Thread/turn 是核心写链；provider unified 层管理 Claude/Codex session；mcp-orch 承载 agent/DAG/workspace/prompt/recall/shared file 工具。
- mcp-lsp 提供 file/inspect/xref/grep/structure/edit/completion 七类工具。

## 4. 证据说明

| 结论 | 文件 |
|---|---|
| 桌面入口 fail-fast 设置环境并运行 Wails desktop | `cmd/agent-terminal/main.go`、`internal/app/app.go` |
| mcp sidecar 保护 stdout，避免污染 JSON-RPC | `cmd/mcp-orch/main.go`、`cmd/mcp-lsp/main.go` |
| 后端根组装包含 config/db/bus/rpc/store/module/provider/toolbridge | `internal/app/modules.go` |
| RPC handler 支持 thread scope 和 capability gate | `internal/platform/rpc/handler.go` |
| mcp-orch 工具面包含 launch/list/send/stop、DAG、workspace、prompt、shared file | `internal/sidecar/orch/tools/registry.go` |
| mcp-lsp 工具面覆盖 file/inspect/xref/grep/structure/edit/completion | `internal/sidecar/lsp/tools.go` |

## 5. 风险与问题

- P1：`internal/app/modules.go` 聚合大量模块，新增依赖可能引入启动环或 optional 行为误判。
- P1：MCP 工具面具备写入/启动能力，handler 参数校验和 trusted scope 必须持续测试。
- P2：mcp-orch/mcp-lsp 的 sidecar 运行依赖环境变量和打包策略，部署文档需更明确。

## 6. 无法判断的信息

- 无法判断所有 RPC 方法的权限模型是否满足多用户场景；当前代码更像本地桌面信任边界。
- 无法判断 provider 外部错误率和重试策略的线上效果。

## 7. 下一阶段建议

进入数据模型与数据流分析，重点看 migrations、sqlc、store、DB auto-migrate 和运行态表。

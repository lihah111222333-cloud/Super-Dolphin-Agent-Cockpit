package mcpserver

import (
	"database/sql"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	"go.uber.org/fx"
)

// Module 把 MCP server 配置和 tool lifecycle 存储接入 Fx 图，供 mcp_server 模块按接口注入。
var Module = fx.Module("store.mcpserver",
	fx.Provide(newMCPServerConfigStore),
	fx.Provide(newMCPToolLifecycleStore),
)

// newMCPServerConfigStore 从数据库池创建 MCP server 配置存储，避免业务模块直接接触数据库类型。
func newMCPServerConfigStore(db *sql.DB) contract.MCPServerConfigStore {
	return NewMCPServerConfigStore(db)
}

// newMCPToolLifecycleStore 从 sqlc 查询器创建 tool lifecycle 存储，保持 owner 模块不直接依赖生成类型。
func newMCPToolLifecycleStore(q *sqlc.Queries) contract.MCPToolLifecycleStore {
	return NewMCPToolLifecycleStore(q)
}

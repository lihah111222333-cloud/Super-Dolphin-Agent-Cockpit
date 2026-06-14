package mcpserver

import (
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
)

// Module 把 MCP server 配置存储接入 Fx 图，供 mcp_server 模块按接口注入。
var Module = fx.Module("store.mcpserver",
	fx.Provide(newMCPServerConfigStore),
)

// newMCPServerConfigStore 从数据库池创建 MCP server 配置存储，避免业务模块直接接触数据库类型。
func newMCPServerConfigStore(pool *pgxpool.Pool) contract.MCPServerConfigStore {
	return NewMCPServerConfigStore(pool)
}

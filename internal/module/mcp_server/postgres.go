package mcpserver

import "context"

const (
	// DefaultPostgresServerName 是显式启动入口写入的本地 PostgreSQL MCP server 名称。
	DefaultPostgresServerName = "postgres"

	defaultPostgresDatabaseURL = "postgresql://super_dolphin@127.0.0.1:55433/super_dolphin?sslmode=disable"
)

// StartPostgresServerRequest 是默认 postgres MCP server 的显式启动请求。
type StartPostgresServerRequest struct{}

// StartPostgresServerResult 返回配置写入位置以及本次是否新增配置。
type StartPostgresServerResult struct {
	ConfigPath string       `json:"configPath"`
	ServerName string       `json:"serverName"`
	Added      bool         `json:"added"`
	Config     ServerConfig `json:"config"`
}

// StartPostgresServer 写入默认 postgres stdio MCP server 配置。
// 这个入口只在 RPC 被显式调用时生效；真正的 npx 进程由后续 provider 会话按配置拉起。
func (s *service) StartPostgresServer(ctx context.Context, _ StartPostgresServerRequest) (StartPostgresServerResult, error) {
	ctx = mcpServerContext(ctx)
	if err := ctx.Err(); err != nil {
		return StartPostgresServerResult{}, err
	}
	listed, err := s.ListServers(ctx)
	if err != nil {
		return StartPostgresServerResult{}, err
	}
	config := defaultPostgresServerConfig()
	if existing, ok := listed.MCPServers[DefaultPostgresServerName]; ok {
		return StartPostgresServerResult{
			ConfigPath: listed.ConfigPath,
			ServerName: DefaultPostgresServerName,
			Added:      false,
			Config:     existing,
		}, nil
	}
	added, err := s.AddServers(ctx, AddServersRequest{
		MCPServers: map[string]ServerConfig{
			DefaultPostgresServerName: config,
		},
	})
	if err != nil {
		return StartPostgresServerResult{}, err
	}
	return StartPostgresServerResult{
		ConfigPath: added.ConfigPath,
		ServerName: DefaultPostgresServerName,
		Added:      true,
		Config:     config,
	}, nil
}

func defaultPostgresServerConfig() ServerConfig {
	return ServerConfig{
		Transport: "stdio",
		Command:   "npx",
		Args: []string{
			"-y",
			"@modelcontextprotocol/server-postgres",
			defaultPostgresDatabaseURL,
		},
	}
}

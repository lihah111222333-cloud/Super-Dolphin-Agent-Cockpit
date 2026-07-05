package mcpserver

import (
	"context"
	"errors"

	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

type listServersPublicResult struct {
	ConfigPath string                           `json:"configPath"`
	MCPServers map[string]mcpServerPublicStatus `json:"mcpServers"`
}

type mcpServerPublicStatus struct {
	Enabled bool `json:"enabled"`
}

type startPostgresServerPublicResult struct {
	ConfigPath string `json:"configPath"`
	ServerName string `json:"serverName"`
	Added      bool   `json:"added"`
}

type startSQLiteServerPublicResult struct {
	ConfigPath string `json:"configPath"`
	ServerName string `json:"serverName"`
	Added      bool   `json:"added"`
	Enabled    bool   `json:"enabled"`
}

type startPlaywrightServerPublicResult struct {
	ConfigPath string `json:"configPath"`
	ServerName string `json:"serverName"`
	Added      bool   `json:"added"`
	Enabled    bool   `json:"enabled"`
}

func toListServersPublicResult(result ListServersResult) listServersPublicResult {
	servers := make(map[string]mcpServerPublicStatus, len(result.MCPServers))
	for name, config := range result.MCPServers {
		servers[name] = mcpServerPublicStatus{Enabled: mcpServerConfigEnabled(config)}
	}
	return listServersPublicResult{
		ConfigPath: result.ConfigPath,
		MCPServers: servers,
	}
}

func toStartPostgresServerPublicResult(result StartPostgresServerResult) startPostgresServerPublicResult {
	return startPostgresServerPublicResult{
		ConfigPath: result.ConfigPath,
		ServerName: result.ServerName,
		Added:      result.Added,
	}
}

func toStartSQLiteServerPublicResult(result StartSQLiteServerResult) startSQLiteServerPublicResult {
	return startSQLiteServerPublicResult{
		ConfigPath: result.ConfigPath,
		ServerName: result.ServerName,
		Added:      result.Added,
		Enabled:    result.Enabled,
	}
}

func toStartPlaywrightServerPublicResult(result StartPlaywrightServerResult) startPlaywrightServerPublicResult {
	return startPlaywrightServerPublicResult{
		ConfigPath: result.ConfigPath,
		ServerName: result.ServerName,
		Added:      result.Added,
		Enabled:    result.Enabled,
	}
}

// NewHandlers 注册 MCP server 管理相关的 RPC 处理器。
func NewHandlers(svc Service) platformrpc.HandlerMapResult {
	return platformrpc.HandlerMapResult{Handlers: handler.Map{
		"mcpServer/add":                  platformrpc.StrictHandler(addServersHandler(svc)),
		"mcpServer/list":                 platformrpc.StrictHandler(listServersHandler(svc)),
		"mcpServer/tools":                platformrpc.StrictHandler(listServerToolsHandler(svc)),
		"mcpServer/postgres/start":       platformrpc.StrictHandler(startPostgresServerHandler(svc)),
		"mcpServer/sqlite/start":         platformrpc.StrictHandler(startSQLiteServerHandler(svc)),
		"mcpServer/sqlite/stop":          platformrpc.StrictHandler(stopSQLiteServerHandler(svc)),
		"mcpServer/playwright/start":     platformrpc.StrictHandler(startPlaywrightServerHandler(svc)),
		"mcpServer/playwright/stop":      platformrpc.StrictHandler(stopPlaywrightServerHandler(svc)),
		"mcpServer/delete":               platformrpc.StrictHandler(deleteServerHandler(svc)),
		"mcpServer/toolLifecycle/set":    platformrpc.StrictHandler(setMCPToolLifecycleHandler(svc)),
		"mcpServer/toolLifecycle/list":   platformrpc.StrictHandler(listMCPToolLifecycleHandler(svc)),
		"mcpServer/toolLifecycle/export": platformrpc.StrictHandler(exportMCPToolLifecycleHandler(svc)),
	}}
}

func addServersHandler(svc Service) func(context.Context, AddServersRequest) (AddServersResult, error) {
	return func(ctx context.Context, req AddServersRequest) (AddServersResult, error) {
		if svc == nil {
			return AddServersResult{}, platformrpc.ErrInvalidState("mcp server service is not configured")
		}
		result, err := svc.AddServers(ctx, req)
		if err != nil {
			return AddServersResult{}, mcpServerRPCError(err)
		}
		return result, nil
	}
}

func listServersHandler(svc Service) func(context.Context, struct{}) (listServersPublicResult, error) {
	return func(ctx context.Context, _ struct{}) (listServersPublicResult, error) {
		if svc == nil {
			return listServersPublicResult{}, platformrpc.ErrInvalidState("mcp server service is not configured")
		}
		result, err := svc.ListServers(ctx)
		if err != nil {
			return listServersPublicResult{}, mcpServerRPCError(err)
		}
		return toListServersPublicResult(result), nil
	}
}

func listServerToolsHandler(svc Service) func(context.Context, ListServerToolsRequest) (ListServerToolsResult, error) {
	return func(ctx context.Context, req ListServerToolsRequest) (ListServerToolsResult, error) {
		if svc == nil {
			return ListServerToolsResult{}, platformrpc.ErrInvalidState("mcp server service is not configured")
		}
		result, err := svc.ListServerTools(ctx, req)
		if err != nil {
			return ListServerToolsResult{}, mcpServerRPCError(err)
		}
		return result, nil
	}
}

func startPostgresServerHandler(svc Service) func(context.Context, StartPostgresServerRequest) (startPostgresServerPublicResult, error) {
	return func(ctx context.Context, req StartPostgresServerRequest) (startPostgresServerPublicResult, error) {
		if svc == nil {
			return startPostgresServerPublicResult{}, platformrpc.ErrInvalidState("mcp server service is not configured")
		}
		result, err := svc.StartPostgresServer(ctx, req)
		if err != nil {
			return startPostgresServerPublicResult{}, mcpServerRPCError(err)
		}
		return toStartPostgresServerPublicResult(result), nil
	}
}

func startSQLiteServerHandler(svc Service) func(context.Context, StartSQLiteServerRequest) (startSQLiteServerPublicResult, error) {
	return func(ctx context.Context, req StartSQLiteServerRequest) (startSQLiteServerPublicResult, error) {
		if svc == nil {
			return startSQLiteServerPublicResult{}, platformrpc.ErrInvalidState("mcp server service is not configured")
		}
		result, err := svc.StartSQLiteServer(ctx, req)
		if err != nil {
			return startSQLiteServerPublicResult{}, mcpServerRPCError(err)
		}
		return toStartSQLiteServerPublicResult(result), nil
	}
}

func stopSQLiteServerHandler(svc Service) func(context.Context, StopSQLiteServerRequest) (StopSQLiteServerResult, error) {
	return func(ctx context.Context, req StopSQLiteServerRequest) (StopSQLiteServerResult, error) {
		if svc == nil {
			return StopSQLiteServerResult{}, platformrpc.ErrInvalidState("mcp server service is not configured")
		}
		result, err := svc.StopSQLiteServer(ctx, req)
		if err != nil {
			return StopSQLiteServerResult{}, mcpServerRPCError(err)
		}
		return result, nil
	}
}

func startPlaywrightServerHandler(svc Service) func(context.Context, StartPlaywrightServerRequest) (startPlaywrightServerPublicResult, error) {
	return func(ctx context.Context, req StartPlaywrightServerRequest) (startPlaywrightServerPublicResult, error) {
		if svc == nil {
			return startPlaywrightServerPublicResult{}, platformrpc.ErrInvalidState("mcp server service is not configured")
		}
		result, err := svc.StartPlaywrightServer(ctx, req)
		if err != nil {
			return startPlaywrightServerPublicResult{}, mcpServerRPCError(err)
		}
		return toStartPlaywrightServerPublicResult(result), nil
	}
}

func stopPlaywrightServerHandler(svc Service) func(context.Context, StopPlaywrightServerRequest) (StopPlaywrightServerResult, error) {
	return func(ctx context.Context, req StopPlaywrightServerRequest) (StopPlaywrightServerResult, error) {
		if svc == nil {
			return StopPlaywrightServerResult{}, platformrpc.ErrInvalidState("mcp server service is not configured")
		}
		result, err := svc.StopPlaywrightServer(ctx, req)
		if err != nil {
			return StopPlaywrightServerResult{}, mcpServerRPCError(err)
		}
		return result, nil
	}
}

func deleteServerHandler(svc Service) func(context.Context, DeleteServerRequest) (DeleteServerResult, error) {
	return func(ctx context.Context, req DeleteServerRequest) (DeleteServerResult, error) {
		if svc == nil {
			return DeleteServerResult{}, platformrpc.ErrInvalidState("mcp server service is not configured")
		}
		result, err := svc.DeleteServer(ctx, req)
		if err != nil {
			return DeleteServerResult{}, mcpServerRPCError(err)
		}
		return result, nil
	}
}

// setMCPToolLifecycleHandler 暴露单个 MCP tool lifecycle 的人工写入口。
// handler 只做 RPC 适配和错误映射，状态校验与持久化仍由 service owner 负责。
func setMCPToolLifecycleHandler(
	svc Service,
) func(context.Context, SetMCPToolLifecycleRequest) (contract.MCPToolLifecycleDecision, error) {
	return func(ctx context.Context, req SetMCPToolLifecycleRequest) (contract.MCPToolLifecycleDecision, error) {
		if svc == nil {
			return contract.MCPToolLifecycleDecision{}, platformrpc.ErrInvalidState("mcp server service is not configured")
		}
		result, err := svc.SetMCPToolLifecycle(ctx, req)
		if err != nil {
			return contract.MCPToolLifecycleDecision{}, mcpServerRPCError(err)
		}
		return result, nil
	}
}

// listMCPToolLifecycleHandler 暴露指定 server 的 MCP tool lifecycle 读入口。
// 调用方必须显式传 serverName，避免把空配置解释成默认放行。
func listMCPToolLifecycleHandler(
	svc Service,
) func(context.Context, ListMCPToolLifecycleRequest) ([]contract.MCPToolLifecycleDecision, error) {
	return func(ctx context.Context, req ListMCPToolLifecycleRequest) ([]contract.MCPToolLifecycleDecision, error) {
		if svc == nil {
			return nil, platformrpc.ErrInvalidState("mcp server service is not configured")
		}
		result, err := svc.ListMCPToolLifecycle(ctx, req)
		if err != nil {
			return nil, mcpServerRPCError(err)
		}
		return result, nil
	}
}

// exportMCPToolLifecycleHandler 暴露 workspace 级 lifecycle 导出入口。
// 它用于回滚或迁移前保留用户手动关闭、暂停和移除工具的决策。
func exportMCPToolLifecycleHandler(
	svc Service,
) func(context.Context, ExportMCPToolLifecycleRequest) ([]contract.MCPToolLifecycleDecision, error) {
	return func(ctx context.Context, req ExportMCPToolLifecycleRequest) ([]contract.MCPToolLifecycleDecision, error) {
		if svc == nil {
			return nil, platformrpc.ErrInvalidState("mcp server service is not configured")
		}
		result, err := svc.ExportMCPToolLifecycle(ctx, req)
		if err != nil {
			return nil, mcpServerRPCError(err)
		}
		return result, nil
	}
}

// mcpServerRPCError 将模块内部错误转换为对应的 RPC 错误类型，确保参数错误和远端状态错误分类清晰。
func mcpServerRPCError(err error) error {
	switch {
	case errors.Is(err, errMissingMCPServers),
		errors.Is(err, errMissingServerName),
		errors.Is(err, errDuplicateServerName),
		errors.Is(err, errMissingServerTransport),
		errors.Is(err, errUnsupportedTransport),
		errors.Is(err, errMissingServerURL),
		errors.Is(err, errInvalidServerURL),
		errors.Is(err, errMissingServerCommand),
		errors.Is(err, errUnsupportedStdioCommand),
		errors.Is(err, errMissingServerArg),
		errors.Is(err, errMissingServerEnvName),
		errors.Is(err, errMissingServerEnvValue),
		errors.Is(err, errMissingHeaderName),
		errors.Is(err, errMissingHeaderValue),
		errors.Is(err, errInvalidConfigDocument),
		errors.Is(err, errMissingToolName),
		errors.Is(err, errInvalidToolLifecycleState):
		return platformrpc.ErrInvalidParams(err.Error())
	case errors.Is(err, errMCPServerStoreNotConfigured):
		return platformrpc.ErrInvalidState(err.Error())
	case errors.Is(err, errMCPServerToolsRequestFailed),
		errors.Is(err, errInvalidToolsResponse),
		errors.Is(err, errPostgresInstallerMissing):
		return platformrpc.ErrInvalidState(err.Error())
	case errors.Is(err, errServerNotFound),
		errors.Is(err, errToolLifecycleNotFound):
		return platformrpc.ErrNotFound(err.Error())
	case errors.Is(err, errServerAlreadyExists):
		return platformrpc.ErrConflict(err.Error())
	default:
		return err
	}
}

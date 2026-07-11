package mcpserver

import (
	"context"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

const (
	// DefaultPlaywrightServerName 是按需开启的本地 Playwright MCP server 名称。
	DefaultPlaywrightServerName = "playwright"

	defaultPlaywrightPackage = "@playwright/mcp@latest"
)

// StartPlaywrightServerRequest 是默认 playwright MCP server 的显式启动 RPC 请求。
type StartPlaywrightServerRequest = contract.MCPPlaywrightServerStartRequest

// StartPlaywrightServerResult 返回 playwright 配置写入位置、本次是否新增以及最终 enabled 状态。
type StartPlaywrightServerResult = contract.MCPPlaywrightServerStartResult

// StopPlaywrightServerRequest 是默认 playwright MCP server 的显式关闭 RPC 请求。
type StopPlaywrightServerRequest = contract.MCPPlaywrightServerStopRequest

// StopPlaywrightServerResult 返回 playwright 配置路径和关闭后的 enabled 状态。
type StopPlaywrightServerResult = contract.MCPPlaywrightServerStopResult

// StartPlaywrightServer 写入或重新启用默认 playwright stdio MCP server 配置。
// 已存在同名配置时只切换 enabled，避免覆盖用户可能调整过的默认参数。
func (s *service) StartPlaywrightServer(ctx context.Context, _ StartPlaywrightServerRequest) (StartPlaywrightServerResult, error) {
	ctx = mcpServerContext(ctx)
	if err := ctx.Err(); err != nil {
		return StartPlaywrightServerResult{}, err
	}
	if s == nil {
		return StartPlaywrightServerResult{}, errMCPServerStoreNotConfigured
	}
	config := defaultPlaywrightServerConfig()
	listed, err := s.ListServers(ctx)
	if err != nil {
		return StartPlaywrightServerResult{}, err
	}
	if existing, ok := listed.MCPServers[DefaultPlaywrightServerName]; ok {
		if err := s.setDefaultPlaywrightServerEnabled(ctx, true); err != nil {
			return StartPlaywrightServerResult{}, err
		}
		existing.Enabled = boolPtr(true)
		return StartPlaywrightServerResult{
			ConfigPath: listed.ConfigPath,
			ServerName: DefaultPlaywrightServerName,
			Added:      false,
			Enabled:    true,
			Config:     existing,
		}, nil
	}
	added, err := s.AddServers(ctx, AddServersRequest{
		MCPServers: map[string]ServerConfig{
			DefaultPlaywrightServerName: config,
		},
	})
	if err != nil {
		return StartPlaywrightServerResult{}, err
	}
	return StartPlaywrightServerResult{
		ConfigPath: added.ConfigPath,
		ServerName: DefaultPlaywrightServerName,
		Added:      true,
		Enabled:    true,
		Config:     config,
	}, nil
}

// StopPlaywrightServer 关闭默认 playwright MCP server，但保留配置行供后续 start 复用。
func (s *service) StopPlaywrightServer(ctx context.Context, _ StopPlaywrightServerRequest) (StopPlaywrightServerResult, error) {
	ctx = mcpServerContext(ctx)
	if err := ctx.Err(); err != nil {
		return StopPlaywrightServerResult{}, err
	}
	if s == nil {
		return StopPlaywrightServerResult{}, errMCPServerStoreNotConfigured
	}
	if err := s.setDefaultPlaywrightServerEnabled(ctx, false); err != nil {
		return StopPlaywrightServerResult{}, err
	}
	workspaceRoot, _, err := s.resolveWorkspaceServers(ctx, "")
	if err != nil {
		return StopPlaywrightServerResult{}, err
	}
	return StopPlaywrightServerResult{
		ConfigPath: mcpServerConfigPath(workspaceRoot),
		ServerName: DefaultPlaywrightServerName,
		Enabled:    false,
	}, nil
}

func defaultPlaywrightServerConfig() ServerConfig {
	return ServerConfig{
		Transport: "stdio",
		Command:   "npx",
		Args:      []string{defaultPlaywrightPackage},
		Enabled:   boolPtr(true),
	}
}

// setDefaultPlaywrightServerEnabled 只切换默认 playwright server 的 enabled 状态。
// 如果配置行或 store 缺失会立即报错，避免 start/stop 看似成功但实际未落库。
func (s *service) setDefaultPlaywrightServerEnabled(ctx context.Context, enabled bool) error {
	workspaceRoot, servers, err := s.resolveWorkspaceServers(ctx, "")
	if err != nil {
		return err
	}
	if _, ok := servers[DefaultPlaywrightServerName]; !ok {
		return fmt.Errorf("%w: %s", errServerNotFound, DefaultPlaywrightServerName)
	}
	store, err := s.requireStore()
	if err != nil {
		return err
	}
	updated, err := store.SetServerEnabled(ctx, workspaceRoot, DefaultPlaywrightServerName, enabled)
	if err != nil {
		return err
	}
	if !updated {
		return fmt.Errorf("%w: %s", errServerNotFound, DefaultPlaywrightServerName)
	}
	return nil
}

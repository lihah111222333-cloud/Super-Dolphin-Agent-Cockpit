package mcpserver

import (
	"context"
	"errors"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type mcpServerConfigProvider struct {
	svc Service
}

// AsMCPServerConfigProvider 将 Service 适配为 provider 层可消费的 MCPServerConfigProvider。
// 适配器只暴露 enabled 配置，避免 provider 拉起已关闭的 server。
func AsMCPServerConfigProvider(svc Service) contract.MCPServerConfigProvider {
	return mcpServerConfigProvider{svc: svc}
}

// ListMCPServerConfigs 返回指定 cwd 下 provider 可启动的 MCP server 配置。
// service 缺失是装配错误，会直接返回 error，避免 provider 静默无 MCP 工具。
func (p mcpServerConfigProvider) ListMCPServerConfigs(
	ctx context.Context,
	cwd string,
) (map[string]contract.MCPServerConfig, error) {
	if p.svc == nil {
		return nil, errors.New("mcp server service is not configured")
	}
	result, err := p.svc.ListServersForCWD(ctx, cwd)
	if err != nil {
		return nil, err
	}
	return enabledMCPServersToContract(result.MCPServers), nil
}

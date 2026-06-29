package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

// BackfillMCPServerToolsRequest 是 discovery 路径回填某个 server 工具清单的输入。
type BackfillMCPServerToolsRequest struct {
	WorkspaceRoot string                                  `json:"workspaceRoot,omitempty"`
	ServerName    string                                  `json:"serverName"`
	ManifestName  string                                  `json:"manifestName,omitempty"`
	Tools         []contract.MCPToolLifecycleObservedTool `json:"tools"`
}

// SetMCPToolLifecycleRequest 是人工设置单个 MCP tool 状态的输入。
type SetMCPToolLifecycleRequest struct {
	WorkspaceRoot   string                         `json:"workspaceRoot,omitempty"`
	ServerName      string                         `json:"serverName"`
	ManifestName    string                         `json:"manifestName,omitempty"`
	ToolName        string                         `json:"toolName"`
	State           contract.MCPToolLifecycleState `json:"state"`
	Reason          string                         `json:"reason,omitempty"`
	ReplacementTool string                         `json:"replacementTool,omitempty"`
}

// ListMCPToolLifecycleRequest 指定要列出治理状态的 MCP server。
type ListMCPToolLifecycleRequest struct {
	WorkspaceRoot string `json:"workspaceRoot,omitempty"`
	ServerName    string `json:"serverName"`
}

// BackfillMCPServerTools 将 discovery 看到的工具写入生命周期表。
// 该方法只刷新可观测信息，不覆盖人工设置的 disabled/suspended/removed 状态。
func (s *service) BackfillMCPServerTools(
	ctx context.Context,
	req BackfillMCPServerToolsRequest,
) ([]contract.MCPToolLifecycleDecision, error) {
	ctx = mcpServerContext(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	workspaceRoot, serverName, err := s.normalizeLifecycleServerRequest(ctx, req.WorkspaceRoot, req.ServerName)
	if err != nil {
		return nil, err
	}
	store, err := s.requireStore()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().UnixMilli()
	out := make([]contract.MCPToolLifecycleDecision, 0, len(req.Tools))
	for _, tool := range req.Tools {
		toolName, err := normalizeMCPToolLifecycleToolName(tool.Name)
		if err != nil {
			return nil, err
		}
		manifestName, err := normalizeOptionalMCPToolLifecycleName(firstNonEmpty(tool.ManifestName, req.ManifestName))
		if err != nil {
			return nil, err
		}
		decision, err := store.BackfillToolLifecycle(ctx, contract.BackfillMCPToolLifecycleParams{
			WorkspaceRoot: workspaceRoot,
			ServerName:    serverName,
			ManifestName:  manifestName,
			ToolName:      toolName,
			NowMillis:     now,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, decision)
	}
	return out, nil
}

// SetMCPToolLifecycle 设置单个工具的人工状态，调用方必须显式给出目标状态。
func (s *service) SetMCPToolLifecycle(
	ctx context.Context,
	req SetMCPToolLifecycleRequest,
) (contract.MCPToolLifecycleDecision, error) {
	ctx = mcpServerContext(ctx)
	if err := ctx.Err(); err != nil {
		return contract.MCPToolLifecycleDecision{}, err
	}
	workspaceRoot, serverName, err := s.normalizeLifecycleServerRequest(ctx, req.WorkspaceRoot, req.ServerName)
	if err != nil {
		return contract.MCPToolLifecycleDecision{}, err
	}
	toolName, err := normalizeMCPToolLifecycleToolName(req.ToolName)
	if err != nil {
		return contract.MCPToolLifecycleDecision{}, err
	}
	state, err := normalizeMCPToolLifecycleState(req.State)
	if err != nil {
		return contract.MCPToolLifecycleDecision{}, err
	}
	manifestName, err := normalizeOptionalMCPToolLifecycleName(req.ManifestName)
	if err != nil {
		return contract.MCPToolLifecycleDecision{}, err
	}
	store, err := s.requireStore()
	if err != nil {
		return contract.MCPToolLifecycleDecision{}, err
	}
	decision, err := store.UpsertToolLifecycle(ctx, contract.StoreMCPToolLifecycleParams{
		WorkspaceRoot:   workspaceRoot,
		ServerName:      serverName,
		ManifestName:    manifestName,
		ToolName:        toolName,
		State:           state,
		Reason:          strings.TrimSpace(req.Reason),
		ReplacementTool: strings.TrimSpace(req.ReplacementTool),
		NowMillis:       time.Now().UTC().UnixMilli(),
	})
	if err != nil {
		return contract.MCPToolLifecycleDecision{}, err
	}
	return decisionWithDenyCode(decision), nil
}

// ListMCPToolLifecycle 列出指定 server 的工具治理状态。
func (s *service) ListMCPToolLifecycle(
	ctx context.Context,
	req ListMCPToolLifecycleRequest,
) ([]contract.MCPToolLifecycleDecision, error) {
	ctx = mcpServerContext(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	workspaceRoot, serverName, err := s.normalizeLifecycleServerRequest(ctx, req.WorkspaceRoot, req.ServerName)
	if err != nil {
		return nil, err
	}
	store, err := s.requireStore()
	if err != nil {
		return nil, err
	}
	decisions, err := store.ListToolLifecycle(ctx, workspaceRoot, serverName)
	if err != nil {
		return nil, err
	}
	for i := range decisions {
		decisions[i] = decisionWithDenyCode(decisions[i])
	}
	return decisions, nil
}

// ResolveMCPToolLifecycle 是调用前策略读入口；缺失 owner 记录时 fail closed。
func (s *service) ResolveMCPToolLifecycle(
	ctx context.Context,
	req contract.MCPToolLifecyclePolicyRequest,
) (contract.MCPToolLifecycleDecision, error) {
	ctx = mcpServerContext(ctx)
	if err := ctx.Err(); err != nil {
		return contract.MCPToolLifecycleDecision{}, err
	}
	workspaceRoot, serverName, servers, err := s.resolveLifecyclePolicyServer(ctx, req)
	if err != nil {
		return contract.MCPToolLifecycleDecision{}, err
	}
	toolName, err := normalizeMCPToolLifecycleToolName(firstNonEmpty(req.ToolName, req.CallName))
	if err != nil {
		return contract.MCPToolLifecycleDecision{}, err
	}
	config := servers[serverName]
	if config.Enabled != nil && !*config.Enabled {
		manifestName, err := normalizeOptionalMCPToolLifecycleName(req.ManifestName)
		if err != nil {
			return contract.MCPToolLifecycleDecision{}, err
		}
		return contract.MCPToolLifecycleDecision{
			WorkspaceRoot:  workspaceRoot,
			ServerName:     serverName,
			ManifestName:   manifestName,
			ToolName:       toolName,
			State:          contract.MCPToolLifecycleDisabled,
			ServerDisabled: true,
			DenyCode:       contract.MCPToolLifecycleDenyCodeServerDisabled,
		}, nil
	}
	store, err := s.requireStore()
	if err != nil {
		return contract.MCPToolLifecycleDecision{}, err
	}
	decision, err := store.GetToolLifecycle(ctx, workspaceRoot, serverName, toolName)
	if err != nil {
		if platformdb.IsNotFound(err) {
			return contract.MCPToolLifecycleDecision{}, fmt.Errorf("%w: %s/%s", errToolLifecycleNotFound, serverName, toolName)
		}
		return contract.MCPToolLifecycleDecision{}, err
	}
	return decisionWithDenyCode(decision), nil
}

// normalizeLifecycleServerRequest 解析 workspace 并确认 server 已配置。
func (s *service) normalizeLifecycleServerRequest(ctx context.Context, cwd string, serverName string) (string, string, error) {
	rawServerName := serverName
	serverName = strings.TrimSpace(serverName)
	if serverName == "" || serverName != rawServerName {
		return "", "", errMissingServerName
	}
	workspaceRoot, servers, err := s.resolveWorkspaceServers(ctx, cwd)
	if err != nil {
		return "", "", err
	}
	if _, ok := servers[serverName]; !ok {
		return "", "", fmt.Errorf("%w: %s", errServerNotFound, serverName)
	}
	return workspaceRoot, serverName, nil
}

// resolveLifecyclePolicyServer 解析策略请求中的 server，并返回同 workspace 下的配置快照。
func (s *service) resolveLifecyclePolicyServer(
	ctx context.Context,
	req contract.MCPToolLifecyclePolicyRequest,
) (string, string, map[string]ServerConfig, error) {
	rawServerName := req.ServerName
	serverName := strings.TrimSpace(req.ServerName)
	if serverName == "" || serverName != rawServerName {
		return "", "", nil, errMissingServerName
	}
	workspaceRoot, servers, err := s.resolveWorkspaceServers(ctx, req.WorkspaceRoot)
	if err != nil {
		return "", "", nil, err
	}
	if _, ok := servers[serverName]; !ok {
		return "", "", nil, fmt.Errorf("%w: %s", errServerNotFound, serverName)
	}
	return workspaceRoot, serverName, servers, nil
}

func normalizeMCPToolLifecycleToolName(raw string) (string, error) {
	toolName := strings.TrimSpace(raw)
	if toolName == "" || toolName != raw {
		return "", errMissingToolName
	}
	return toolName, nil
}

func normalizeOptionalMCPToolLifecycleName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name != raw {
		return "", errMissingToolName
	}
	return name, nil
}

func normalizeMCPToolLifecycleState(raw contract.MCPToolLifecycleState) (contract.MCPToolLifecycleState, error) {
	state := contract.MCPToolLifecycleState(strings.TrimSpace(string(raw)))
	switch state {
	case contract.MCPToolLifecycleEnabled,
		contract.MCPToolLifecycleDisabled,
		contract.MCPToolLifecycleSuspended,
		contract.MCPToolLifecycleRemoved:
		return state, nil
	default:
		return "", fmt.Errorf("%w: %s", errInvalidToolLifecycleState, raw)
	}
}

func decisionWithDenyCode(decision contract.MCPToolLifecycleDecision) contract.MCPToolLifecycleDecision {
	switch decision.State {
	case contract.MCPToolLifecycleDisabled:
		decision.DenyCode = contract.MCPToolLifecycleDenyCodeDisabled
	case contract.MCPToolLifecycleSuspended:
		decision.DenyCode = contract.MCPToolLifecycleDenyCodeSuspended
	case contract.MCPToolLifecycleRemoved:
		decision.DenyCode = contract.MCPToolLifecycleDenyCodeRemoved
	default:
		decision.DenyCode = ""
	}
	return decision
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

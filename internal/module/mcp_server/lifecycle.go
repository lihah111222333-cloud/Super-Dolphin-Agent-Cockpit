package mcpserver

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

// BackfillMCPToolLifecycleRequest 表示一次 discovery 结果回填。
// Tools 使用 MCP 协议原始 tool name，不能传入 toolbridge 派生别名。
type BackfillMCPToolLifecycleRequest struct {
	WorkspaceRoot string           `json:"workspaceRoot"`
	ServerName    string           `json:"serverName"`
	Tools         []mcpdto.MCPTool `json:"tools"`
	Reason        string           `json:"reason,omitempty"`
	UpdatedBy     string           `json:"updatedBy,omitempty"`
}

// BackfillMCPToolLifecycleResult 返回 discovery 回填后的状态行。
// Inserted 只统计本次新增的 active 行，已有 suspended/removed 会原样返回。
type BackfillMCPToolLifecycleResult struct {
	Records  []contract.MCPToolLifecycleRecord `json:"records"`
	Inserted int                               `json:"inserted"`
}

// UpsertMCPToolLifecycleState 写入一条显式 tool lifecycle 状态。
// owner 层先确认 server 属于该 workspace，避免 store 被跨 server 任意写入。
func (s *service) UpsertMCPToolLifecycleState(
	ctx context.Context,
	params contract.MCPToolLifecycleUpsertParams,
) (contract.MCPToolLifecycleRecord, error) {
	ctx = mcpServerContext(ctx)
	if err := ctx.Err(); err != nil {
		return contract.MCPToolLifecycleRecord{}, err
	}
	normalized, err := normalizeMCPToolLifecycleUpsertParams(params)
	if err != nil {
		return contract.MCPToolLifecycleRecord{}, err
	}
	workspaceRoot, err := s.requireLifecycleServer(ctx, normalized.Key.WorkspaceRoot, normalized.Key.ServerName)
	if err != nil {
		return contract.MCPToolLifecycleRecord{}, err
	}
	store, err := s.requireToolLifecycleStore()
	if err != nil {
		return contract.MCPToolLifecycleRecord{}, err
	}
	normalized.Key.WorkspaceRoot = workspaceRoot
	return store.UpsertMCPToolLifecycleState(ctx, normalized)
}

// EnsureDiscoveredMCPToolLifecycleState 为单个发现到的 tool 补齐 active 行。
// 已有记录必须保留，尤其不能把 suspended/removed 覆盖回 active。
func (s *service) EnsureDiscoveredMCPToolLifecycleState(
	ctx context.Context,
	params contract.MCPToolLifecycleDiscoveryParams,
) (contract.MCPToolLifecycleRecord, bool, error) {
	ctx = mcpServerContext(ctx)
	if err := ctx.Err(); err != nil {
		return contract.MCPToolLifecycleRecord{}, false, err
	}
	normalized, err := normalizeMCPToolLifecycleDiscoveryParams(params)
	if err != nil {
		return contract.MCPToolLifecycleRecord{}, false, err
	}
	workspaceRoot, err := s.requireLifecycleServer(ctx, normalized.Key.WorkspaceRoot, normalized.Key.ServerName)
	if err != nil {
		return contract.MCPToolLifecycleRecord{}, false, err
	}
	store, err := s.requireToolLifecycleStore()
	if err != nil {
		return contract.MCPToolLifecycleRecord{}, false, err
	}
	normalized.Key.WorkspaceRoot = workspaceRoot
	return store.EnsureDiscoveredMCPToolLifecycleState(ctx, normalized)
}

// GetMCPToolLifecycleState 读取指定 workspace/server/tool 的 lifecycle 行。
// 缺少 server 或缺少 lifecycle 行都会返回错误，不会把缺行解释为 active。
func (s *service) GetMCPToolLifecycleState(
	ctx context.Context,
	key contract.MCPToolLifecycleKey,
) (contract.MCPToolLifecycleRecord, error) {
	ctx = mcpServerContext(ctx)
	if err := ctx.Err(); err != nil {
		return contract.MCPToolLifecycleRecord{}, err
	}
	normalized, err := normalizeMCPToolLifecycleKey(key)
	if err != nil {
		return contract.MCPToolLifecycleRecord{}, err
	}
	workspaceRoot, err := s.requireLifecycleServer(ctx, normalized.WorkspaceRoot, normalized.ServerName)
	if err != nil {
		return contract.MCPToolLifecycleRecord{}, err
	}
	store, err := s.requireToolLifecycleStore()
	if err != nil {
		return contract.MCPToolLifecycleRecord{}, err
	}
	normalized.WorkspaceRoot = workspaceRoot
	return store.GetMCPToolLifecycleState(ctx, normalized)
}

// ListMCPToolLifecycleStates 按 workspace/server 列出 lifecycle 行。
// owner 层仍会确认 server 存在，避免读取一个没有配置归属的状态集合。
func (s *service) ListMCPToolLifecycleStates(
	ctx context.Context,
	params contract.MCPToolLifecycleListParams,
) ([]contract.MCPToolLifecycleRecord, error) {
	ctx = mcpServerContext(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	normalized, err := normalizeMCPToolLifecycleListParams(params)
	if err != nil {
		return nil, err
	}
	workspaceRoot, err := s.requireLifecycleServer(ctx, normalized.WorkspaceRoot, normalized.ServerName)
	if err != nil {
		return nil, err
	}
	store, err := s.requireToolLifecycleStore()
	if err != nil {
		return nil, err
	}
	normalized.WorkspaceRoot = workspaceRoot
	return store.ListMCPToolLifecycleStates(ctx, normalized)
}

// BackfillDiscoveredMCPToolLifecycleStates 批量回填 discovery 得到的工具。
// 该方法只补缺失 active 行；已有用户态或 tombstone 记录必须由 store 原样返回。
func (s *service) BackfillDiscoveredMCPToolLifecycleStates(
	ctx context.Context,
	req BackfillMCPToolLifecycleRequest,
) (BackfillMCPToolLifecycleResult, error) {
	ctx = mcpServerContext(ctx)
	if err := ctx.Err(); err != nil {
		return BackfillMCPToolLifecycleResult{}, err
	}
	normalized, err := normalizeBackfillMCPToolLifecycleRequest(req)
	if err != nil {
		return BackfillMCPToolLifecycleResult{}, err
	}
	workspaceRoot, err := s.requireLifecycleServer(ctx, normalized.WorkspaceRoot, normalized.ServerName)
	if err != nil {
		return BackfillMCPToolLifecycleResult{}, err
	}
	store, err := s.requireToolLifecycleStore()
	if err != nil {
		return BackfillMCPToolLifecycleResult{}, err
	}
	return backfillMCPToolLifecycleStates(
		ctx,
		store,
		workspaceRoot,
		normalized.ServerName,
		normalized.Tools,
		normalized.Reason,
		normalized.UpdatedBy,
	)
}

// backfillMCPToolLifecycleStates 把发现到的原始 tool name 写成缺失 active 行。
// 这里必须依赖 store 的 insert-if-absent 语义，不能覆盖用户暂停或删除的记录。
func backfillMCPToolLifecycleStates(
	ctx context.Context,
	store contract.MCPToolLifecycleStore,
	workspaceRoot string,
	serverName string,
	tools []mcpdto.MCPTool,
	reason string,
	updatedBy string,
) (BackfillMCPToolLifecycleResult, error) {
	if store == nil {
		return BackfillMCPToolLifecycleResult{}, errMCPToolLifecycleStoreMissing
	}
	normalizedTools, err := normalizeMCPToolLifecycleTools(tools)
	if err != nil {
		return BackfillMCPToolLifecycleResult{}, err
	}
	result := BackfillMCPToolLifecycleResult{
		Records: make([]contract.MCPToolLifecycleRecord, 0, len(normalizedTools)),
	}
	for _, tool := range normalizedTools {
		record, inserted, err := store.EnsureDiscoveredMCPToolLifecycleState(ctx, contract.MCPToolLifecycleDiscoveryParams{
			Key: contract.MCPToolLifecycleKey{
				WorkspaceRoot: workspaceRoot,
				ServerName:    serverName,
				ToolName:      tool.Name,
			},
			Reason:    strings.TrimSpace(reason),
			UpdatedBy: strings.TrimSpace(updatedBy),
		})
		if err != nil {
			return BackfillMCPToolLifecycleResult{}, err
		}
		if inserted {
			result.Inserted++
		}
		result.Records = append(result.Records, record)
	}
	return result, nil
}

func (s *service) requireLifecycleServer(ctx context.Context, workspaceRoot, serverName string) (string, error) {
	resolvedRoot, servers, err := s.resolveWorkspaceServers(ctx, workspaceRoot)
	if err != nil {
		return "", err
	}
	if _, ok := servers[serverName]; !ok {
		return "", fmt.Errorf("%w: %s", errServerNotFound, serverName)
	}
	return resolvedRoot, nil
}

func normalizeMCPToolLifecycleUpsertParams(
	params contract.MCPToolLifecycleUpsertParams,
) (contract.MCPToolLifecycleUpsertParams, error) {
	key, err := normalizeMCPToolLifecycleKey(params.Key)
	if err != nil {
		return contract.MCPToolLifecycleUpsertParams{}, err
	}
	if err := validateMCPToolLifecycleState(params.State); err != nil {
		return contract.MCPToolLifecycleUpsertParams{}, err
	}
	if err := validateMCPToolLifecycleSource(params.Source); err != nil {
		return contract.MCPToolLifecycleUpsertParams{}, err
	}
	params.Key = key
	params.Reason = strings.TrimSpace(params.Reason)
	params.UpdatedBy = strings.TrimSpace(params.UpdatedBy)
	return params, nil
}

func normalizeMCPToolLifecycleDiscoveryParams(
	params contract.MCPToolLifecycleDiscoveryParams,
) (contract.MCPToolLifecycleDiscoveryParams, error) {
	key, err := normalizeMCPToolLifecycleKey(params.Key)
	if err != nil {
		return contract.MCPToolLifecycleDiscoveryParams{}, err
	}
	params.Key = key
	params.Reason = strings.TrimSpace(params.Reason)
	params.UpdatedBy = strings.TrimSpace(params.UpdatedBy)
	return params, nil
}

func normalizeMCPToolLifecycleKey(
	key contract.MCPToolLifecycleKey,
) (contract.MCPToolLifecycleKey, error) {
	workspaceRoot, err := normalizeMCPToolLifecycleWorkspaceRoot(key.WorkspaceRoot)
	if err != nil {
		return contract.MCPToolLifecycleKey{}, err
	}
	serverName, err := normalizeMCPToolLifecycleName(key.ServerName, errMissingServerName)
	if err != nil {
		return contract.MCPToolLifecycleKey{}, err
	}
	toolName, err := normalizeMCPToolLifecycleName(key.ToolName, errMissingToolName)
	if err != nil {
		return contract.MCPToolLifecycleKey{}, err
	}
	return contract.MCPToolLifecycleKey{
		WorkspaceRoot: workspaceRoot,
		ServerName:    serverName,
		ToolName:      toolName,
	}, nil
}

func normalizeMCPToolLifecycleListParams(
	params contract.MCPToolLifecycleListParams,
) (contract.MCPToolLifecycleListParams, error) {
	workspaceRoot, err := normalizeMCPToolLifecycleWorkspaceRoot(params.WorkspaceRoot)
	if err != nil {
		return contract.MCPToolLifecycleListParams{}, err
	}
	serverName, err := normalizeMCPToolLifecycleName(params.ServerName, errMissingServerName)
	if err != nil {
		return contract.MCPToolLifecycleListParams{}, err
	}
	return contract.MCPToolLifecycleListParams{
		WorkspaceRoot: workspaceRoot,
		ServerName:    serverName,
	}, nil
}

func normalizeBackfillMCPToolLifecycleRequest(
	req BackfillMCPToolLifecycleRequest,
) (BackfillMCPToolLifecycleRequest, error) {
	workspaceRoot, err := normalizeMCPToolLifecycleWorkspaceRoot(req.WorkspaceRoot)
	if err != nil {
		return BackfillMCPToolLifecycleRequest{}, err
	}
	serverName, err := normalizeMCPToolLifecycleName(req.ServerName, errMissingServerName)
	if err != nil {
		return BackfillMCPToolLifecycleRequest{}, err
	}
	tools, err := normalizeMCPToolLifecycleTools(req.Tools)
	if err != nil {
		return BackfillMCPToolLifecycleRequest{}, err
	}
	return BackfillMCPToolLifecycleRequest{
		WorkspaceRoot: workspaceRoot,
		ServerName:    serverName,
		Tools:         tools,
		Reason:        strings.TrimSpace(req.Reason),
		UpdatedBy:     strings.TrimSpace(req.UpdatedBy),
	}, nil
}

func normalizeMCPToolLifecycleWorkspaceRoot(workspaceRoot string) (string, error) {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if workspaceRoot == "" {
		return "", errMissingWorkspaceRoot
	}
	if abs, err := filepath.Abs(workspaceRoot); err == nil {
		workspaceRoot = abs
	}
	return filepath.Clean(workspaceRoot), nil
}

func normalizeMCPToolLifecycleName(raw string, missingErr error) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" || name != raw {
		return "", missingErr
	}
	return name, nil
}

func normalizeMCPToolLifecycleTools(tools []mcpdto.MCPTool) ([]mcpdto.MCPTool, error) {
	normalized := make([]mcpdto.MCPTool, 0, len(tools))
	for _, tool := range tools {
		name, err := normalizeMCPToolLifecycleName(tool.Name, errMissingToolName)
		if err != nil {
			return nil, err
		}
		tool.Name = name
		normalized = append(normalized, tool)
	}
	return normalized, nil
}

func validateMCPToolLifecycleState(state contract.MCPToolLifecycleState) error {
	switch state {
	case contract.MCPToolLifecycleStateActive,
		contract.MCPToolLifecycleStateSuspended,
		contract.MCPToolLifecycleStateRemoved:
		return nil
	default:
		return fmt.Errorf("%w: %s", errInvalidMCPToolLifecycleState, state)
	}
}

func validateMCPToolLifecycleSource(source contract.MCPToolLifecycleSource) error {
	switch source {
	case contract.MCPToolLifecycleSourceDiscovery,
		contract.MCPToolLifecycleSourceUser,
		contract.MCPToolLifecycleSourceMigration,
		contract.MCPToolLifecycleSourceSystem:
		return nil
	default:
		return fmt.Errorf("%w: %s", errInvalidMCPToolLifecycleSource, source)
	}
}

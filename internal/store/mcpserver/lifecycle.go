package mcpserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

var (
	errMissingToolName           = errors.New("mcp_server: tool name is required")
	errInvalidLifecycleName      = errors.New("mcp_server: invalid tool lifecycle name")
	errInvalidLifecycleState     = errors.New("mcp_server: invalid tool lifecycle state")
	errInvalidLifecycleTimestamp = errors.New("mcp_server: invalid tool lifecycle timestamp")
)

type mcpToolLifecycleDBTX struct {
	platformdb.Queryable
}

// PrepareContext 明确拒绝 sqlc 的预编译路径。
// 当前生成代码只用 Query/Exec；若未来切到 Prepare，应先扩展底层 Queryable。
func (db mcpToolLifecycleDBTX) PrepareContext(context.Context, string) (*sql.Stmt, error) {
	return nil, errors.New("mcp_server: sqlc prepare is not supported by lifecycle query adapter")
}

// GetToolLifecycle 读取单个 MCP tool 的治理状态，未命中时返回 store not found 语义。
func (s *configStore) GetToolLifecycle(
	ctx context.Context,
	workspaceRoot string,
	serverName string,
	toolName string,
) (contract.MCPToolLifecycleDecision, error) {
	workspaceRoot, serverName, toolName, err := normalizeMCPToolLifecycleKey(workspaceRoot, serverName, toolName)
	if err != nil {
		return contract.MCPToolLifecycleDecision{}, err
	}
	q, err := s.lifecycleQueries()
	if err != nil {
		return contract.MCPToolLifecycleDecision{}, err
	}
	row, err := q.GetMCPToolLifecycle(ctx, sqlc.GetMCPToolLifecycleParams{
		WorkspaceRoot: workspaceRoot,
		ServerName:    serverName,
		ToolName:      toolName,
	})
	if err != nil {
		return contract.MCPToolLifecycleDecision{}, wrapMCPToolLifecycleStoreError(err, "get")
	}
	return decodeMCPToolLifecycle(row)
}

// ListToolLifecycle 按 server 列出已登记的 MCP tool 状态，返回值保持 tool_name 升序。
func (s *configStore) ListToolLifecycle(
	ctx context.Context,
	workspaceRoot string,
	serverName string,
) ([]contract.MCPToolLifecycleDecision, error) {
	workspaceRoot, serverName, err := normalizeMCPToolLifecycleServerKey(workspaceRoot, serverName)
	if err != nil {
		return nil, err
	}
	q, err := s.lifecycleQueries()
	if err != nil {
		return nil, err
	}
	rows, err := q.ListMCPToolLifecycle(ctx, sqlc.ListMCPToolLifecycleParams{
		WorkspaceRoot: workspaceRoot,
		ServerName:    serverName,
	})
	if err != nil {
		return nil, wrapMCPToolLifecycleStoreError(err, "list")
	}
	out := make([]contract.MCPToolLifecycleDecision, 0, len(rows))
	for _, row := range rows {
		decision, err := decodeMCPToolLifecycle(row)
		if err != nil {
			return nil, err
		}
		out = append(out, decision)
	}
	return out, nil
}

// ExportToolLifecycle 导出当前 workspace 的全部 MCP tool lifecycle 状态。
// 该只读入口用于回滚或降级前保留人工 disabled/suspended/removed 决策。
func (s *configStore) ExportToolLifecycle(
	ctx context.Context,
	workspaceRoot string,
) ([]contract.MCPToolLifecycleDecision, error) {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if workspaceRoot == "" {
		return nil, errMissingWorkspaceRoot
	}
	q, err := s.lifecycleQueries()
	if err != nil {
		return nil, err
	}
	rows, err := q.ExportMCPToolLifecycle(ctx, sqlc.ExportMCPToolLifecycleParams{WorkspaceRoot: workspaceRoot})
	if err != nil {
		return nil, wrapMCPToolLifecycleStoreError(err, "export")
	}
	out := make([]contract.MCPToolLifecycleDecision, 0, len(rows))
	for _, row := range rows {
		decision, err := decodeMCPToolLifecycle(row)
		if err != nil {
			return nil, err
		}
		out = append(out, decision)
	}
	return out, nil
}

// UpsertToolLifecycle 写入人工状态变更；状态、原因和替代工具以本次请求为准。
func (s *configStore) UpsertToolLifecycle(
	ctx context.Context,
	params contract.StoreMCPToolLifecycleParams,
) (contract.MCPToolLifecycleDecision, error) {
	normalized, err := normalizeStoreMCPToolLifecycleParams(params)
	if err != nil {
		return contract.MCPToolLifecycleDecision{}, err
	}
	q, err := s.lifecycleQueries()
	if err != nil {
		return contract.MCPToolLifecycleDecision{}, err
	}
	row, err := q.UpsertMCPToolLifecycle(ctx, sqlc.UpsertMCPToolLifecycleParams{
		WorkspaceRoot:   normalized.WorkspaceRoot,
		ServerName:      normalized.ServerName,
		ManifestName:    normalized.ManifestName,
		ToolName:        normalized.ToolName,
		State:           string(normalized.State),
		Reason:          normalized.Reason,
		ReplacementTool: normalized.ReplacementTool,
		Now:             normalized.NowMillis,
	})
	if err != nil {
		return contract.MCPToolLifecycleDecision{}, wrapMCPToolLifecycleStoreError(err, "upsert")
	}
	return decodeMCPToolLifecycle(row)
}

// BackfillToolLifecycle 记录 discovery 看到的工具，但保留已有人工状态和原因。
func (s *configStore) BackfillToolLifecycle(
	ctx context.Context,
	params contract.BackfillMCPToolLifecycleParams,
) (contract.MCPToolLifecycleDecision, error) {
	normalized, err := normalizeBackfillMCPToolLifecycleParams(params)
	if err != nil {
		return contract.MCPToolLifecycleDecision{}, err
	}
	q, err := s.lifecycleQueries()
	if err != nil {
		return contract.MCPToolLifecycleDecision{}, err
	}
	row, err := q.BackfillMCPToolLifecycle(ctx, sqlc.BackfillMCPToolLifecycleParams{
		WorkspaceRoot: normalized.WorkspaceRoot,
		ServerName:    normalized.ServerName,
		ManifestName:  normalized.ManifestName,
		ToolName:      normalized.ToolName,
		Now:           normalized.NowMillis,
	})
	if err != nil {
		return contract.MCPToolLifecycleDecision{}, wrapMCPToolLifecycleStoreError(err, "backfill")
	}
	return decodeMCPToolLifecycle(row)
}

// lifecycleQueries 只创建 sqlc 查询对象，不替 lifecycle 表做历史兼容建表。
func (s *configStore) lifecycleQueries() (*sqlc.Queries, error) {
	if s == nil || s.db == nil {
		return nil, errMCPServerStoreNotConfigured
	}
	return sqlc.New(mcpToolLifecycleDBTX{Queryable: s.db}), nil
}

// normalizeStoreMCPToolLifecycleParams 校验人工状态写入参数，避免 store 写入不可解释的状态行。
func normalizeStoreMCPToolLifecycleParams(
	params contract.StoreMCPToolLifecycleParams,
) (contract.StoreMCPToolLifecycleParams, error) {
	workspaceRoot, serverName, toolName, err := normalizeMCPToolLifecycleKey(params.WorkspaceRoot, params.ServerName, params.ToolName)
	if err != nil {
		return contract.StoreMCPToolLifecycleParams{}, err
	}
	state := contract.MCPToolLifecycleState(strings.TrimSpace(string(params.State)))
	if !isKnownMCPToolLifecycleState(state) {
		return contract.StoreMCPToolLifecycleParams{}, fmt.Errorf("%w: %s", errInvalidLifecycleState, params.State)
	}
	manifestName, err := normalizeOptionalMCPToolLifecycleName(params.ManifestName, "manifest")
	if err != nil {
		return contract.StoreMCPToolLifecycleParams{}, err
	}
	if params.NowMillis <= 0 {
		return contract.StoreMCPToolLifecycleParams{}, errInvalidLifecycleTimestamp
	}
	return contract.StoreMCPToolLifecycleParams{
		WorkspaceRoot:   workspaceRoot,
		ServerName:      serverName,
		ManifestName:    manifestName,
		ToolName:        toolName,
		State:           state,
		Reason:          strings.TrimSpace(params.Reason),
		ReplacementTool: strings.TrimSpace(params.ReplacementTool),
		NowMillis:       params.NowMillis,
	}, nil
}

// normalizeBackfillMCPToolLifecycleParams 校验 discovery 回填参数。
// 回填允许 manifest 为空，但 workspace、server 和 tool 必须完整。
func normalizeBackfillMCPToolLifecycleParams(
	params contract.BackfillMCPToolLifecycleParams,
) (contract.BackfillMCPToolLifecycleParams, error) {
	workspaceRoot, serverName, toolName, err := normalizeMCPToolLifecycleKey(params.WorkspaceRoot, params.ServerName, params.ToolName)
	if err != nil {
		return contract.BackfillMCPToolLifecycleParams{}, err
	}
	manifestName, err := normalizeOptionalMCPToolLifecycleName(params.ManifestName, "manifest")
	if err != nil {
		return contract.BackfillMCPToolLifecycleParams{}, err
	}
	if params.NowMillis <= 0 {
		return contract.BackfillMCPToolLifecycleParams{}, errInvalidLifecycleTimestamp
	}
	return contract.BackfillMCPToolLifecycleParams{
		WorkspaceRoot: workspaceRoot,
		ServerName:    serverName,
		ManifestName:  manifestName,
		ToolName:      toolName,
		NowMillis:     params.NowMillis,
	}, nil
}

func normalizeMCPToolLifecycleKey(workspaceRoot, serverName, toolName string) (string, string, string, error) {
	workspaceRoot, serverName, err := normalizeMCPToolLifecycleServerKey(workspaceRoot, serverName)
	if err != nil {
		return "", "", "", err
	}
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return "", "", "", errMissingToolName
	}
	return workspaceRoot, serverName, toolName, nil
}

func normalizeMCPToolLifecycleServerKey(workspaceRoot, serverName string) (string, string, error) {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if workspaceRoot == "" {
		return "", "", errMissingWorkspaceRoot
	}
	serverName = strings.TrimSpace(serverName)
	if serverName == "" {
		return "", "", errMissingServerName
	}
	return workspaceRoot, serverName, nil
}

func normalizeOptionalMCPToolLifecycleName(raw string, label string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed != raw {
		return "", fmt.Errorf("%w: %s", errInvalidLifecycleName, label)
	}
	return trimmed, nil
}

func decodeMCPToolLifecycle(row sqlc.McpToolLifecycle) (contract.MCPToolLifecycleDecision, error) {
	state := contract.MCPToolLifecycleState(row.State)
	if !isKnownMCPToolLifecycleState(state) {
		return contract.MCPToolLifecycleDecision{}, fmt.Errorf("%w: %s", errInvalidLifecycleState, row.State)
	}
	return contract.MCPToolLifecycleDecision{
		WorkspaceRoot:   row.WorkspaceRoot,
		ServerName:      row.ServerName,
		ManifestName:    row.ManifestName,
		ToolName:        row.ToolName,
		State:           state,
		Reason:          row.Reason,
		ReplacementTool: row.ReplacementTool,
		LastSeenAt:      row.LastSeenAt,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
	}, nil
}

func isKnownMCPToolLifecycleState(state contract.MCPToolLifecycleState) bool {
	switch state {
	case contract.MCPToolLifecycleEnabled,
		contract.MCPToolLifecycleDisabled,
		contract.MCPToolLifecycleSuspended,
		contract.MCPToolLifecycleRemoved:
		return true
	default:
		return false
	}
}

// wrapMCPToolLifecycleStoreError 统一包装 MCP tool lifecycle 表的数据库错误。
func wrapMCPToolLifecycleStoreError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "mcp_tool_lifecycle")
}

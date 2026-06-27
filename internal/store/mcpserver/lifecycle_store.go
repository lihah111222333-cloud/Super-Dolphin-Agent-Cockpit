package mcpserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

var (
	errMissingToolName            = errors.New("mcp_tool_lifecycle: tool name is required")
	errInvalidToolLifecycleState  = errors.New("mcp_tool_lifecycle: invalid state")
	errInvalidToolLifecycleSource = errors.New("mcp_tool_lifecycle: invalid source")
)

type lifecycleQuerier interface {
	UpsertMCPToolLifecycleState(
		ctx context.Context,
		arg sqlc.UpsertMCPToolLifecycleStateParams,
	) (sqlc.McpToolLifecycleState, error)
	InsertMCPToolLifecycleStateIfAbsent(
		ctx context.Context,
		arg sqlc.InsertMCPToolLifecycleStateIfAbsentParams,
	) (int64, error)
	GetMCPToolLifecycleState(
		ctx context.Context,
		arg sqlc.GetMCPToolLifecycleStateParams,
	) (sqlc.McpToolLifecycleState, error)
	ListMCPToolLifecycleStatesByServer(
		ctx context.Context,
		arg sqlc.ListMCPToolLifecycleStatesByServerParams,
	) ([]sqlc.McpToolLifecycleState, error)
}

type lifecycleStore struct {
	q lifecycleQuerier
}

// NewMCPToolLifecycleStore 创建 MCP tool lifecycle 的 sqlc 存储实现。
// 该 store 只维护状态表，不做 server/tool 是否存在的 owner 校验。
func NewMCPToolLifecycleStore(q *sqlc.Queries) contract.MCPToolLifecycleStore {
	if q == nil {
		return nil
	}
	return &lifecycleStore{q: q}
}

// UpsertMCPToolLifecycleState 写入一条显式 lifecycle 状态。
// 空 key、非法 state/source 在进入 sqlc 前失败，避免依赖数据库约束兜底。
func (s *lifecycleStore) UpsertMCPToolLifecycleState(
	ctx context.Context,
	params contract.MCPToolLifecycleUpsertParams,
) (contract.MCPToolLifecycleRecord, error) {
	if err := validateLifecycleStoreConfigured(s); err != nil {
		return contract.MCPToolLifecycleRecord{}, err
	}
	key, err := normalizeLifecycleKey(params.Key)
	if err != nil {
		return contract.MCPToolLifecycleRecord{}, wrapMCPToolLifecycleStoreError(err, "upsert")
	}
	if err := validateLifecycleState(params.State); err != nil {
		return contract.MCPToolLifecycleRecord{}, wrapMCPToolLifecycleStoreError(err, "upsert")
	}
	if err := validateLifecycleSource(params.Source); err != nil {
		return contract.MCPToolLifecycleRecord{}, wrapMCPToolLifecycleStoreError(err, "upsert")
	}
	row, err := s.q.UpsertMCPToolLifecycleState(ctx, sqlc.UpsertMCPToolLifecycleStateParams{
		WorkspaceRoot:  key.WorkspaceRoot,
		ServerName:     key.ServerName,
		ToolName:       key.ToolName,
		LifecycleState: string(params.State),
		Reason:         strings.TrimSpace(params.Reason),
		Source:         string(params.Source),
		UpdatedBy:      strings.TrimSpace(params.UpdatedBy),
	})
	if err != nil {
		return contract.MCPToolLifecycleRecord{}, wrapMCPToolLifecycleStoreError(err, "upsert")
	}
	return lifecycleRecordFromSQLC(row)
}

// EnsureDiscoveredMCPToolLifecycleState 为 discovery/backfill 补齐缺失的 active 行。
// 已有记录原样返回并标记 inserted=false，确保 suspended/removed 不会被发现流程覆盖。
func (s *lifecycleStore) EnsureDiscoveredMCPToolLifecycleState(
	ctx context.Context,
	params contract.MCPToolLifecycleDiscoveryParams,
) (contract.MCPToolLifecycleRecord, bool, error) {
	if err := validateLifecycleStoreConfigured(s); err != nil {
		return contract.MCPToolLifecycleRecord{}, false, err
	}
	key, err := normalizeLifecycleKey(params.Key)
	if err != nil {
		return contract.MCPToolLifecycleRecord{}, false, wrapMCPToolLifecycleStoreError(err, "ensure_discovered")
	}
	rows, err := s.q.InsertMCPToolLifecycleStateIfAbsent(ctx, sqlc.InsertMCPToolLifecycleStateIfAbsentParams{
		WorkspaceRoot:  key.WorkspaceRoot,
		ServerName:     key.ServerName,
		ToolName:       key.ToolName,
		LifecycleState: string(contract.MCPToolLifecycleStateActive),
		Reason:         strings.TrimSpace(params.Reason),
		Source:         string(contract.MCPToolLifecycleSourceDiscovery),
		UpdatedBy:      strings.TrimSpace(params.UpdatedBy),
	})
	if err != nil {
		return contract.MCPToolLifecycleRecord{}, false, wrapMCPToolLifecycleStoreError(err, "ensure_discovered")
	}
	record, err := s.GetMCPToolLifecycleState(ctx, key)
	if err != nil {
		return contract.MCPToolLifecycleRecord{}, false, wrapMCPToolLifecycleStoreError(err, "ensure_discovered.read")
	}
	return record, rows > 0, nil
}

// GetMCPToolLifecycleState 读取单个 tool lifecycle 状态。
// 缺行会以 not found 返回；读到未知状态或来源说明数据库被破坏，直接失败。
func (s *lifecycleStore) GetMCPToolLifecycleState(
	ctx context.Context,
	key contract.MCPToolLifecycleKey,
) (contract.MCPToolLifecycleRecord, error) {
	if err := validateLifecycleStoreConfigured(s); err != nil {
		return contract.MCPToolLifecycleRecord{}, err
	}
	normalized, err := normalizeLifecycleKey(key)
	if err != nil {
		return contract.MCPToolLifecycleRecord{}, wrapMCPToolLifecycleStoreError(err, "get")
	}
	row, err := s.q.GetMCPToolLifecycleState(ctx, sqlc.GetMCPToolLifecycleStateParams{
		WorkspaceRoot: normalized.WorkspaceRoot,
		ServerName:    normalized.ServerName,
		ToolName:      normalized.ToolName,
	})
	if err != nil {
		return contract.MCPToolLifecycleRecord{}, wrapMCPToolLifecycleStoreError(err, "get")
	}
	return lifecycleRecordFromSQLC(row)
}

// ListMCPToolLifecycleStates 按 workspace/server 列出 tool lifecycle 状态。
// 列表中任何未知状态都会让整个读取失败，避免后续过滤面拿到半可信数据。
func (s *lifecycleStore) ListMCPToolLifecycleStates(
	ctx context.Context,
	params contract.MCPToolLifecycleListParams,
) ([]contract.MCPToolLifecycleRecord, error) {
	if err := validateLifecycleStoreConfigured(s); err != nil {
		return nil, err
	}
	workspaceRoot, serverName, err := normalizeLifecycleListParams(params)
	if err != nil {
		return nil, wrapMCPToolLifecycleStoreError(err, "list")
	}
	rows, err := s.q.ListMCPToolLifecycleStatesByServer(ctx, sqlc.ListMCPToolLifecycleStatesByServerParams{
		WorkspaceRoot: workspaceRoot,
		ServerName:    serverName,
	})
	if err != nil {
		return nil, wrapMCPToolLifecycleStoreError(err, "list")
	}
	records := make([]contract.MCPToolLifecycleRecord, 0, len(rows))
	for _, row := range rows {
		record, err := lifecycleRecordFromSQLC(row)
		if err != nil {
			return nil, wrapMCPToolLifecycleStoreError(err, "list.map")
		}
		records = append(records, record)
	}
	return records, nil
}

func validateLifecycleStoreConfigured(s *lifecycleStore) error {
	if s == nil || s.q == nil {
		return wrapMCPToolLifecycleStoreError(errMCPServerStoreNotConfigured, "configured")
	}
	return nil
}

func normalizeLifecycleKey(key contract.MCPToolLifecycleKey) (contract.MCPToolLifecycleKey, error) {
	workspaceRoot := strings.TrimSpace(key.WorkspaceRoot)
	if workspaceRoot == "" {
		return contract.MCPToolLifecycleKey{}, errMissingWorkspaceRoot
	}
	serverName := strings.TrimSpace(key.ServerName)
	if serverName == "" {
		return contract.MCPToolLifecycleKey{}, errMissingServerName
	}
	toolName := strings.TrimSpace(key.ToolName)
	if toolName == "" {
		return contract.MCPToolLifecycleKey{}, errMissingToolName
	}
	return contract.MCPToolLifecycleKey{
		WorkspaceRoot: workspaceRoot,
		ServerName:    serverName,
		ToolName:      toolName,
	}, nil
}

func normalizeLifecycleListParams(params contract.MCPToolLifecycleListParams) (string, string, error) {
	workspaceRoot := strings.TrimSpace(params.WorkspaceRoot)
	if workspaceRoot == "" {
		return "", "", errMissingWorkspaceRoot
	}
	serverName := strings.TrimSpace(params.ServerName)
	if serverName == "" {
		return "", "", errMissingServerName
	}
	return workspaceRoot, serverName, nil
}

func validateLifecycleState(state contract.MCPToolLifecycleState) error {
	switch state {
	case contract.MCPToolLifecycleStateActive,
		contract.MCPToolLifecycleStateSuspended,
		contract.MCPToolLifecycleStateRemoved:
		return nil
	default:
		return fmt.Errorf("%w: %s", errInvalidToolLifecycleState, state)
	}
}

func validateLifecycleSource(source contract.MCPToolLifecycleSource) error {
	switch source {
	case contract.MCPToolLifecycleSourceDiscovery,
		contract.MCPToolLifecycleSourceUser,
		contract.MCPToolLifecycleSourceMigration,
		contract.MCPToolLifecycleSourceSystem:
		return nil
	default:
		return fmt.Errorf("%w: %s", errInvalidToolLifecycleSource, source)
	}
}

func lifecycleRecordFromSQLC(row sqlc.McpToolLifecycleState) (contract.MCPToolLifecycleRecord, error) {
	state := contract.MCPToolLifecycleState(row.LifecycleState)
	if err := validateLifecycleState(state); err != nil {
		return contract.MCPToolLifecycleRecord{}, err
	}
	source := contract.MCPToolLifecycleSource(row.Source)
	if err := validateLifecycleSource(source); err != nil {
		return contract.MCPToolLifecycleRecord{}, err
	}
	return contract.MCPToolLifecycleRecord{
		WorkspaceRoot: row.WorkspaceRoot,
		ServerName:    row.ServerName,
		ToolName:      row.ToolName,
		State:         state,
		Reason:        row.Reason,
		Source:        source,
		UpdatedBy:     row.UpdatedBy,
		CreatedAt:     unixMillisToTime(row.CreatedAt),
		UpdatedAt:     unixMillisToTime(row.UpdatedAt),
	}, nil
}

func unixMillisToTime(value int64) time.Time {
	return time.UnixMilli(value).UTC()
}

func wrapMCPToolLifecycleStoreError(err error, operation string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		err = platformdb.ErrNotFound
	}
	return platformdb.WrapStoreError(err, operation, "mcp_tool_lifecycle")
}

var _ contract.MCPToolLifecycleStore = (*lifecycleStore)(nil)

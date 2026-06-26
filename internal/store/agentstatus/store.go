package agentstatus

import (
	"context"
	"encoding/json"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

// querier 是 agentstatus store 依赖的 sqlc 查询子集，测试可用内存替身覆盖读写路径。
type querier interface {
	UpsertAgentStatus(ctx context.Context, arg sqlc.UpsertAgentStatusParams) (sqlc.AgentStatus, error)
	GetAgentStatus(ctx context.Context, arg sqlc.GetAgentStatusParams) (sqlc.AgentStatus, error)
	ListAgentStatuses(ctx context.Context, arg sqlc.ListAgentStatusesParams) ([]sqlc.AgentStatus, error)
}

// store 实现 agent 状态持久化，所有数据库错误统一带上 agent_status 实体名。
type store struct {
	q querier
}

// NewStore 使用生产 sqlc 查询对象创建 agentstatus Store。
func NewStore(q *sqlc.Queries) Store {
	return &store{q: q}
}

// newStoreForTest 使用窄 querier 构造测试 Store，避免测试依赖真实数据库池。
func newStoreForTest(q querier) Store { return &store{q: q} }

// Upsert 写入 agent 最新状态，并在落库前校验 OutputTail JSON。
func (s *store) Upsert(ctx context.Context, params UpsertParams) (*AgentStatus, error) {
	if err := platformdb.ValidateJSONRaw(params.OutputTail); err != nil {
		return nil, wrapAgentStatusError(err, "upsert")
	}
	now := platformdb.Millis(time.Now().UTC())
	row, err := s.q.UpsertAgentStatus(ctx, sqlc.UpsertAgentStatusParams{
		AgentID:     params.AgentID,
		AgentName:   params.AgentName,
		SessionID:   params.SessionID,
		Status:      params.Status,
		StagnantSec: int64(params.StagnantSec),
		Error:       params.Error,
		OutputTail:  params.OutputTail,
		Now:         now,
	})
	if err != nil {
		return nil, wrapAgentStatusError(err, "upsert")
	}
	result := mapAgentStatus(row)
	return &result, nil
}

// Get 按 agent ID 读取状态记录，底层 not found 由统一 store 错误包装保留。
func (s *store) Get(ctx context.Context, agentID string) (*AgentStatus, error) {
	row, err := s.q.GetAgentStatus(ctx, sqlc.GetAgentStatusParams{AgentID: agentID})
	if err != nil {
		return nil, wrapAgentStatusError(err, "get")
	}
	result := mapAgentStatus(row)
	return &result, nil
}

// List 按状态过滤 agent 状态列表，空 status 由 SQL 查询解释为不过滤。
func (s *store) List(ctx context.Context, status string) ([]AgentStatus, error) {
	rows, err := s.q.ListAgentStatuses(ctx, sqlc.ListAgentStatusesParams{StatusFilter: status})
	if err != nil {
		return nil, wrapAgentStatusError(err, "list")
	}
	result := make([]AgentStatus, len(rows))
	for i, row := range rows {
		result[i] = mapAgentStatus(row)
	}
	return result, nil
}

// mapAgentStatus 将 sqlc 行转换为 JSON wire DTO，并把毫秒时间戳转为 time.Time。
func mapAgentStatus(row sqlc.AgentStatus) AgentStatus {
	return AgentStatus{
		AgentID:     row.AgentID,
		AgentName:   row.AgentName,
		SessionID:   row.SessionID,
		Status:      row.Status,
		StagnantSec: int32(row.StagnantSec),
		Error:       row.Error,
		OutputTail:  json.RawMessage(row.OutputTail),
		CreatedAt:   platformdb.TimeFromMillis(row.CreatedAt),
		UpdatedAt:   platformdb.TimeFromMillis(row.UpdatedAt),
	}
}

// wrapAgentStatusError 统一包装 agent status store 错误，保留 operation 便于排查。
func wrapAgentStatusError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "agent_status")
}

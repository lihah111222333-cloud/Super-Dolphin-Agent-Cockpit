package agentstatus

import (
	"context"
	"encoding/json"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

// querier is the narrow subset of *sqlc.Queries this store calls.
// Splitting it out keeps unit tests free of a live pool.
type querier interface {
	UpsertAgentStatus(ctx context.Context, arg sqlc.UpsertAgentStatusParams) (sqlc.AgentStatus, error)
	GetAgentStatus(ctx context.Context, arg sqlc.GetAgentStatusParams) (sqlc.AgentStatus, error)
	ListAgentStatuses(ctx context.Context, arg sqlc.ListAgentStatusesParams) ([]sqlc.AgentStatus, error)
}

type store struct {
	q querier
}

func NewStore(q *sqlc.Queries) Store {
	return &store{q: q}
}

func newStoreForTest(q querier) Store { return &store{q: q} }

func (s *store) Upsert(ctx context.Context, params UpsertParams) (*AgentStatus, error) {
	row, err := s.q.UpsertAgentStatus(ctx, sqlc.UpsertAgentStatusParams{
		AgentID:     params.AgentID,
		AgentName:   params.AgentName,
		SessionID:   params.SessionID,
		Status:      params.Status,
		StagnantSec: int64(params.StagnantSec),
		Error:       params.Error,
		OutputTail:  params.OutputTail,
	})
	if err != nil {
		return nil, wrapAgentStatusError(err, "upsert")
	}
	result := mapAgentStatus(row)
	return &result, nil
}

func (s *store) Get(ctx context.Context, agentID string) (*AgentStatus, error) {
	row, err := s.q.GetAgentStatus(ctx, sqlc.GetAgentStatusParams{AgentID: agentID})
	if err != nil {
		return nil, wrapAgentStatusError(err, "get")
	}
	result := mapAgentStatus(row)
	return &result, nil
}

func (s *store) List(ctx context.Context, status string) ([]AgentStatus, error) {
	rows, err := s.q.ListAgentStatuses(ctx, sqlc.ListAgentStatusesParams{Column1: status})
	if err != nil {
		return nil, wrapAgentStatusError(err, "list")
	}
	result := make([]AgentStatus, len(rows))
	for i, row := range rows {
		result[i] = mapAgentStatus(row)
	}
	return result, nil
}

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

func wrapAgentStatusError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "agent_status")
}

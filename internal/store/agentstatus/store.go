package agentstatus

import (
	"context"
	"encoding/json"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type store struct {
	q *sqlc.Queries
}

func NewStore(q *sqlc.Queries) Store {
	return &store{q: q}
}

func (s *store) Upsert(ctx context.Context, params UpsertParams) (*AgentStatus, error) {
	row, err := s.q.UpsertAgentStatus(ctx, sqlc.UpsertAgentStatusParams{
		AgentID:     params.AgentID,
		AgentName:   params.AgentName,
		SessionID:   params.SessionID,
		Status:      params.Status,
		StagnantSec: params.StagnantSec,
		Error:       params.Error,
		Column7:     params.OutputTail,
	})
	if err != nil {
		return nil, wrapAgentStatusError(err, "upsert")
	}
	result := mapAgentStatus(row)
	return &result, nil
}

func (s *store) Get(ctx context.Context, agentID string) (*AgentStatus, error) {
	row, err := s.q.GetAgentStatus(ctx, agentID)
	if err != nil {
		return nil, wrapAgentStatusError(err, "get")
	}
	result := mapAgentStatus(row)
	return &result, nil
}

func (s *store) List(ctx context.Context, status string) ([]AgentStatus, error) {
	rows, err := s.q.ListAgentStatuses(ctx, status)
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
		StagnantSec: row.StagnantSec,
		Error:       row.Error,
		OutputTail:  json.RawMessage(row.OutputTail),
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}
}

func wrapAgentStatusError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "agent_status")
}

package binding

import (
	"context"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type store struct {
	q *sqlc.Queries
}

func NewStore(q *sqlc.Queries) Store {
	return &store{q: q}
}

func (s *store) GetByProviderThread(ctx context.Context, provider, providerThreadID string) (*Binding, error) {
	row, err := s.q.GetAgentProviderBindingByProviderThread(ctx, sqlc.GetAgentProviderBindingByProviderThreadParams{
		Provider:         provider,
		ProviderThreadID: providerThreadID,
	})
	if err != nil {
		return nil, wrapBindingError(err, "get_by_provider_thread")
	}
	result := mapBinding(row)
	return &result, nil
}

func (s *store) Upsert(ctx context.Context, params UpsertParams) error {
	err := s.q.UpsertAgentProviderBinding(ctx, sqlc.UpsertAgentProviderBindingParams{
		AgentID:          params.AgentID,
		Provider:         params.Provider,
		ProviderThreadID: params.ProviderThreadID,
		CodexThreadID:    params.CodexThreadID,
		RolloutPath:      params.RolloutPath,
		Cwd:              params.Cwd,
		CreatedAt:        params.CreatedAt,
		UpdatedAt:        params.UpdatedAt,
	})
	if err == nil {
		return nil
	}
	if !platformdb.IsUniqueViolation(err) {
		return wrapBindingError(err, "upsert")
	}
	existing, lookupErr := s.q.GetAgentProviderBindingByProviderThread(ctx, sqlc.GetAgentProviderBindingByProviderThreadParams{
		Provider:         params.Provider,
		ProviderThreadID: params.ProviderThreadID,
	})
	if lookupErr == nil && existing.AgentID == params.AgentID {
		return nil
	}
	if lookupErr != nil {
		return wrapBindingError(lookupErr, "get_by_provider_thread")
	}
	return wrapBindingError(err, "upsert")
}

func (s *store) DeleteByAgentID(ctx context.Context, agentID string) error {
	return wrapBindingError(s.q.DeleteAgentProviderBindingByAgentID(ctx, agentID), "delete_by_agent_id")
}

func (s *store) UpdateSessionUUID(ctx context.Context, params UpdateSessionUUIDParams) error {
	return wrapBindingError(s.q.UpdateAgentProviderBindingSessionUUID(ctx, sqlc.UpdateAgentProviderBindingSessionUUIDParams{
		SessionUUID: params.SessionUUID,
		UpdatedAt:   params.UpdatedAt,
		AgentID:     params.AgentID,
	}), "update_session_uuid")
}

func (s *store) SetArchived(ctx context.Context, params SetArchivedParams) error {
	return wrapBindingError(s.q.UpdateAgentProviderBindingArchived(ctx, sqlc.UpdateAgentProviderBindingArchivedParams{
		Archived:  params.Archived,
		UpdatedAt: params.UpdatedAt,
		AgentID:   params.AgentID,
	}), "set_archived")
}

func (s *store) GetByAgentID(ctx context.Context, agentID string) (*Binding, error) {
	row, err := s.q.GetAgentProviderBindingByAgentID(ctx, agentID)
	if err != nil {
		return nil, wrapBindingError(err, "get_by_agent_id")
	}
	result := mapBinding(row)
	return &result, nil
}

func mapBinding(row sqlc.AgentProviderBinding) Binding {
	return Binding{
		AgentID:          row.AgentID,
		Provider:         row.Provider,
		ProviderThreadID: row.ProviderThreadID,
		CodexThreadID:    row.CodexThreadID,
		RolloutPath:      row.RolloutPath,
		Cwd:              row.Cwd,
		Archived:         row.Archived,
		CreatedAt:        row.CreatedAt,
		UpdatedAt:        row.UpdatedAt,
		SessionUUID:      row.SessionUUID,
	}
}

func wrapBindingError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "binding")
}

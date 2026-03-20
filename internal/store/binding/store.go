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
		return nil, err
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
	if err == nil || !platformdb.IsUniqueViolation(err) {
		return err
	}
	existing, lookupErr := s.q.GetAgentProviderBindingByProviderThread(ctx, sqlc.GetAgentProviderBindingByProviderThreadParams{
		Provider:         params.Provider,
		ProviderThreadID: params.ProviderThreadID,
	})
	if lookupErr == nil && existing.AgentID == params.AgentID {
		return nil
	}
	return err
}

func (s *store) DeleteByAgentID(ctx context.Context, agentID string) error {
	return s.q.DeleteAgentProviderBindingByAgentID(ctx, agentID)
}

func (s *store) UpdateSessionUUID(ctx context.Context, params UpdateSessionUUIDParams) error {
	return s.q.UpdateAgentProviderBindingSessionUUID(ctx, sqlc.UpdateAgentProviderBindingSessionUUIDParams{
		SessionUUID: params.SessionUUID,
		UpdatedAt:   params.UpdatedAt,
		AgentID:     params.AgentID,
	})
}

func (s *store) SetArchived(ctx context.Context, params SetArchivedParams) error {
	return s.q.UpdateAgentProviderBindingArchived(ctx, sqlc.UpdateAgentProviderBindingArchivedParams{
		Archived:  params.Archived,
		UpdatedAt: params.UpdatedAt,
		AgentID:   params.AgentID,
	})
}

func (s *store) GetByAgentID(ctx context.Context, agentID string) (*Binding, error) {
	row, err := s.q.GetAgentProviderBindingByAgentID(ctx, agentID)
	if err != nil {
		return nil, err
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

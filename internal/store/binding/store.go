package binding

import (
	"context"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type querier interface {
	BindAgentThread(ctx context.Context, arg sqlc.BindAgentThreadParams) error
	DeleteAgentProviderBindingByAgentID(ctx context.Context, agentID string) error
	GetAgentProviderBindingByAgentID(ctx context.Context, agentID string) (sqlc.AgentProviderBinding, error)
	GetAgentProviderBindingByProviderThread(ctx context.Context, arg sqlc.GetAgentProviderBindingByProviderThreadParams) (sqlc.AgentProviderBinding, error)
	GetThreadByAgent(ctx context.Context, agentID string) (string, error)
	ListAgentThreadBindings(ctx context.Context) ([]sqlc.AgentProviderBinding, error)
	UnbindAgentThread(ctx context.Context, agentID string) error
	UpdateAgentCwd(ctx context.Context, arg sqlc.UpdateAgentCwdParams) error
	UpdateAgentProviderBindingArchived(ctx context.Context, arg sqlc.UpdateAgentProviderBindingArchivedParams) error
	UpdateAgentProviderBindingSessionUUID(ctx context.Context, arg sqlc.UpdateAgentProviderBindingSessionUUIDParams) error
	UpsertAgentProviderBinding(ctx context.Context, arg sqlc.UpsertAgentProviderBindingParams) error
}

type store struct {
	q querier
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
		SessionUUID:      params.SessionUUID,
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

func (s *store) BindAgentThread(ctx context.Context, params BindAgentThreadParams) error {
	now := time.Now().Unix()
	if params.CreatedAt == 0 {
		params.CreatedAt = now
	}
	if params.UpdatedAt == 0 {
		params.UpdatedAt = now
	}
	return wrapBindingError(s.q.BindAgentThread(ctx, sqlc.BindAgentThreadParams{
		AgentID:   params.AgentID,
		ThreadID:  params.ThreadID,
		Cwd:       params.Cwd,
		CreatedAt: params.CreatedAt,
		UpdatedAt: params.UpdatedAt,
	}), "bind_agent_thread")
}

func (s *store) UnbindAgentThread(ctx context.Context, agentID string) error {
	return wrapBindingError(s.q.UnbindAgentThread(ctx, agentID), "unbind_agent_thread")
}

func (s *store) ListAgentThreadBindings(ctx context.Context) ([]Binding, error) {
	rows, err := s.q.ListAgentThreadBindings(ctx)
	if err != nil {
		return nil, wrapBindingError(err, "list_agent_thread_bindings")
	}
	result := make([]Binding, len(rows))
	for i, row := range rows {
		result[i] = mapBinding(row)
	}
	return result, nil
}

func (s *store) GetThreadByAgent(ctx context.Context, agentID string) (string, error) {
	threadID, err := s.q.GetThreadByAgent(ctx, agentID)
	if err != nil {
		return "", wrapBindingError(err, "get_thread_by_agent")
	}
	return threadID, nil
}

func (s *store) UpdateAgentCwd(ctx context.Context, params UpdateAgentCwdParams) error {
	updatedAt := params.UpdatedAt
	if updatedAt == 0 {
		updatedAt = time.Now().Unix()
	}
	return wrapBindingError(s.q.UpdateAgentCwd(ctx, sqlc.UpdateAgentCwdParams{
		Cwd:       params.Cwd,
		UpdatedAt: updatedAt,
		AgentID:   params.AgentID,
	}), "update_agent_cwd")
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

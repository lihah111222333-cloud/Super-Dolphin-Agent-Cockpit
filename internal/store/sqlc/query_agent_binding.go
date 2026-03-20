package sqlc

import "context"

const (
	getAgentProviderBindingByProviderThreadSQL = `SELECT agent_id, provider, provider_thread_id, codex_thread_id, rollout_path, cwd, archived, created_at, updated_at, session_uuid FROM agent_provider_binding WHERE provider = $1 AND provider_thread_id = $2;`
	upsertAgentProviderBindingSQL              = `INSERT INTO agent_provider_binding ( agent_id, provider, provider_thread_id, codex_thread_id, rollout_path, cwd, archived, created_at, updated_at ) VALUES ($1, $2, $3, $4, $5, $6, false, $7, $8) ON CONFLICT (agent_id) DO UPDATE SET provider = EXCLUDED.provider, provider_thread_id = EXCLUDED.provider_thread_id, codex_thread_id = EXCLUDED.codex_thread_id, rollout_path = EXCLUDED.rollout_path, cwd = EXCLUDED.cwd, updated_at = EXCLUDED.updated_at;`
	deleteAgentProviderBindingByAgentIDSQL     = `DELETE FROM agent_provider_binding WHERE agent_id = $1;`
	updateAgentProviderBindingSessionUUIDSQL   = `UPDATE agent_provider_binding SET session_uuid = $1, updated_at = $2 WHERE agent_id = $3;`
	updateAgentProviderBindingArchivedSQL      = `UPDATE agent_provider_binding SET archived = $1, updated_at = $2 WHERE agent_id = $3;`
	getAgentProviderBindingByAgentIDSQL        = `SELECT agent_id, provider, provider_thread_id, codex_thread_id, rollout_path, cwd, archived, created_at, updated_at, session_uuid FROM agent_provider_binding WHERE agent_id = $1;`
)

func scanAgentProviderBinding(row rowScanner) (AgentProviderBinding, error) {
	var item AgentProviderBinding
	err := row.Scan(&item.AgentID, &item.Provider, &item.ProviderThreadID, &item.CodexThreadID, &item.RolloutPath, &item.Cwd, &item.Archived, &item.CreatedAt, &item.UpdatedAt, &item.SessionUUID)
	return item, err
}

func (q *Queries) GetAgentProviderBindingByProviderThread(ctx context.Context, arg GetAgentProviderBindingByProviderThreadParams) (AgentProviderBinding, error) {
	return queryOne(ctx, q, getAgentProviderBindingByProviderThreadSQL, scanAgentProviderBinding, arg.Provider, arg.ProviderThreadID)
}

func (q *Queries) UpsertAgentProviderBinding(ctx context.Context, arg UpsertAgentProviderBindingParams) error {
	return q.exec(ctx, upsertAgentProviderBindingSQL, arg.AgentID, arg.Provider, arg.ProviderThreadID, arg.CodexThreadID, arg.RolloutPath, arg.Cwd, arg.CreatedAt, arg.UpdatedAt)
}

func (q *Queries) DeleteAgentProviderBindingByAgentID(ctx context.Context, agentID string) error {
	return q.exec(ctx, deleteAgentProviderBindingByAgentIDSQL, agentID)
}

func (q *Queries) UpdateAgentProviderBindingSessionUUID(ctx context.Context, arg UpdateAgentProviderBindingSessionUUIDParams) error {
	return q.exec(ctx, updateAgentProviderBindingSessionUUIDSQL, arg.SessionUUID, arg.UpdatedAt, arg.AgentID)
}

func (q *Queries) UpdateAgentProviderBindingArchived(ctx context.Context, arg UpdateAgentProviderBindingArchivedParams) error {
	return q.exec(ctx, updateAgentProviderBindingArchivedSQL, arg.Archived, arg.UpdatedAt, arg.AgentID)
}

func (q *Queries) GetAgentProviderBindingByAgentID(ctx context.Context, agentID string) (AgentProviderBinding, error) {
	return queryOne(ctx, q, getAgentProviderBindingByAgentIDSQL, scanAgentProviderBinding, agentID)
}

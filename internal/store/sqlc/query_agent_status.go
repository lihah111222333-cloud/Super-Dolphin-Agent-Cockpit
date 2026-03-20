package sqlc

import "context"

const (
	upsertAgentStatusSQL = `INSERT INTO agent_status (agent_id, agent_name, session_id, status, stagnant_sec, error, output_tail, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, NOW(), NOW()) ON CONFLICT (agent_id) DO UPDATE SET agent_name = EXCLUDED.agent_name, session_id = EXCLUDED.session_id, status = EXCLUDED.status, stagnant_sec = EXCLUDED.stagnant_sec, error = EXCLUDED.error, output_tail = EXCLUDED.output_tail, updated_at = NOW() RETURNING agent_id, agent_name, session_id, status, stagnant_sec, error, output_tail, created_at, updated_at;`
	getAgentStatusSQL    = `SELECT agent_id, agent_name, session_id, status, stagnant_sec, error, output_tail, created_at, updated_at FROM agent_status WHERE agent_id = $1;`
	listAgentStatusesSQL = `SELECT agent_id, agent_name, session_id, status, stagnant_sec, error, output_tail, created_at, updated_at FROM agent_status WHERE ($1::text = '' OR status = $1) ORDER BY updated_at DESC LIMIT 500;`
)

func scanAgentStatus(row rowScanner) (AgentStatus, error) {
	var item AgentStatus
	err := row.Scan(&item.AgentID, &item.AgentName, &item.SessionID, &item.Status, &item.StagnantSec, &item.Error, &item.OutputTail, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func (q *Queries) UpsertAgentStatus(ctx context.Context, arg UpsertAgentStatusParams) (AgentStatus, error) {
	return queryOne(ctx, q, upsertAgentStatusSQL, scanAgentStatus, arg.AgentID, arg.AgentName, arg.SessionID, arg.Status, arg.StagnantSec, arg.Error, arg.OutputTail)
}

func (q *Queries) GetAgentStatus(ctx context.Context, agentID string) (AgentStatus, error) {
	return queryOne(ctx, q, getAgentStatusSQL, scanAgentStatus, agentID)
}

func (q *Queries) ListAgentStatuses(ctx context.Context, status string) ([]AgentStatus, error) {
	return queryMany(ctx, q, listAgentStatusesSQL, scanAgentStatus, status)
}

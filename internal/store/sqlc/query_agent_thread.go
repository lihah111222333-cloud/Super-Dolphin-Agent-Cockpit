package sqlc

import "context"

const (
	getAgentThreadByIDSQL          = `SELECT thread_id, prompt, model, cwd, status, port, pid, created_at, updated_at, finished_at, last_event_type, error_message, workspace_run_key, owner_thread_id, COALESCE((SELECT b.agent_id FROM agent_provider_binding b WHERE b.provider_thread_id = agent_threads.thread_id OR b.codex_thread_id = agent_threads.thread_id OR (agent_threads.owner_thread_id <> '' AND (b.provider_thread_id = agent_threads.owner_thread_id OR b.codex_thread_id = agent_threads.owner_thread_id)) ORDER BY b.updated_at DESC LIMIT 1), '') AS agent_id FROM agent_threads WHERE thread_id = $1 LIMIT 1;`
	getAgentThreadByPortSQL        = `SELECT thread_id, prompt, model, cwd, status, port, pid, created_at, updated_at, finished_at, last_event_type, error_message, workspace_run_key, owner_thread_id, COALESCE((SELECT b.agent_id FROM agent_provider_binding b WHERE b.provider_thread_id = agent_threads.thread_id OR b.codex_thread_id = agent_threads.thread_id OR (agent_threads.owner_thread_id <> '' AND (b.provider_thread_id = agent_threads.owner_thread_id OR b.codex_thread_id = agent_threads.owner_thread_id)) ORDER BY b.updated_at DESC LIMIT 1), '') AS agent_id FROM agent_threads WHERE port = $1 AND status = 'running' ORDER BY updated_at DESC LIMIT 1;`
	listAgentThreadsSQL            = `SELECT thread_id, prompt, model, cwd, status, port, pid, created_at, updated_at, finished_at, last_event_type, error_message, workspace_run_key, owner_thread_id, COALESCE((SELECT b.agent_id FROM agent_provider_binding b WHERE b.provider_thread_id = agent_threads.thread_id OR b.codex_thread_id = agent_threads.thread_id OR (agent_threads.owner_thread_id <> '' AND (b.provider_thread_id = agent_threads.owner_thread_id OR b.codex_thread_id = agent_threads.owner_thread_id)) ORDER BY b.updated_at DESC LIMIT 1), '') AS agent_id FROM agent_threads ORDER BY created_at DESC;`
	listRunningAgentsSQL           = `SELECT thread_id, port, pid, status FROM agent_threads WHERE status = 'running' ORDER BY created_at DESC;`
	listRunningAgentThreadsSQL     = `SELECT thread_id, prompt, model, cwd, status, port, pid, created_at, updated_at, finished_at, last_event_type, error_message, workspace_run_key, owner_thread_id, COALESCE((SELECT b.agent_id FROM agent_provider_binding b WHERE b.provider_thread_id = agent_threads.thread_id OR b.codex_thread_id = agent_threads.thread_id OR (agent_threads.owner_thread_id <> '' AND (b.provider_thread_id = agent_threads.owner_thread_id OR b.codex_thread_id = agent_threads.owner_thread_id)) ORDER BY b.updated_at DESC LIMIT 1), '') AS agent_id FROM agent_threads WHERE status = 'running' ORDER BY created_at ASC;`
	listRecoverableAgentThreadsSQL = `SELECT thread_id, prompt, model, cwd, status, port, pid, created_at, updated_at, finished_at, last_event_type, error_message, workspace_run_key, owner_thread_id, COALESCE((SELECT b.agent_id FROM agent_provider_binding b WHERE b.provider_thread_id = agent_threads.thread_id OR b.codex_thread_id = agent_threads.thread_id OR (agent_threads.owner_thread_id <> '' AND (b.provider_thread_id = agent_threads.owner_thread_id OR b.codex_thread_id = agent_threads.owner_thread_id)) ORDER BY b.updated_at DESC LIMIT 1), '') AS agent_id FROM agent_threads WHERE status = 'created' ORDER BY created_at ASC;`
	upsertAgentThreadSQL           = `INSERT INTO agent_threads (thread_id, prompt, model, cwd, status, port, pid, created_at, updated_at, owner_thread_id) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) ON CONFLICT (thread_id) DO UPDATE SET prompt = $2, model = $3, cwd = $4, status = $5, port = $6, pid = $7, updated_at = $9, owner_thread_id = $10;`
	updateAgentThreadStatusSQL     = `UPDATE agent_threads SET status = $2, updated_at = $3 WHERE thread_id = $1;`
	deleteAgentThreadByIDSQL       = `DELETE FROM agent_threads WHERE thread_id = $1;`
	resetRunningAgentThreadsSQL    = `UPDATE agent_threads SET status = 'created' WHERE status = 'running';`
	expireStaleAgentThreadsSQL     = `UPDATE agent_threads SET status = 'expired', updated_at = $1 WHERE status IN ('created', 'running') AND updated_at < $2;`
	agentThreadRunningExistsSQL    = `SELECT EXISTS( SELECT 1 FROM agent_threads WHERE thread_id = $1 AND status = 'running' );`
	listAgentThreadCwdsSQL         = `SELECT thread_id, cwd FROM agent_threads WHERE cwd <> '' ORDER BY created_at DESC;`
	listAgentThreadCwdsByPrefixSQL = `SELECT thread_id, cwd FROM agent_threads WHERE cwd <> '' AND cwd LIKE $1 || '%' ORDER BY created_at DESC;`
)

func scanAgentThread(row rowScanner) (AgentThread, error) {
	var item AgentThread
	err := row.Scan(&item.ThreadID, &item.Prompt, &item.Model, &item.Cwd, &item.Status, &item.Port, &item.PID, &item.CreatedAt, &item.UpdatedAt, &item.FinishedAt, &item.LastEventType, &item.ErrorMessage, &item.WorkspaceRunKey, &item.OwnerThreadID, &item.AgentID)
	return item, err
}

func scanListRunningAgentsRow(row rowScanner) (ListRunningAgentsRow, error) {
	var item ListRunningAgentsRow
	err := row.Scan(&item.ThreadID, &item.Port, &item.PID, &item.Status)
	return item, err
}

func scanAgentThreadCwdRow(row rowScanner) (AgentThreadCwdRow, error) {
	var item AgentThreadCwdRow
	err := row.Scan(&item.ThreadID, &item.Cwd)
	return item, err
}

func (q *Queries) GetAgentThreadByID(ctx context.Context, threadID string) (AgentThread, error) {
	return queryOne(ctx, q, getAgentThreadByIDSQL, scanAgentThread, threadID)
}

func (q *Queries) GetAgentThreadByPort(ctx context.Context, port int32) (AgentThread, error) {
	return queryOne(ctx, q, getAgentThreadByPortSQL, scanAgentThread, port)
}

func (q *Queries) ListAgentThreads(ctx context.Context) ([]AgentThread, error) {
	return queryMany(ctx, q, listAgentThreadsSQL, scanAgentThread)
}

func (q *Queries) ListRunningAgents(ctx context.Context) ([]ListRunningAgentsRow, error) {
	return queryMany(ctx, q, listRunningAgentsSQL, scanListRunningAgentsRow)
}

func (q *Queries) ListRunningAgentThreads(ctx context.Context) ([]AgentThread, error) {
	return queryMany(ctx, q, listRunningAgentThreadsSQL, scanAgentThread)
}

func (q *Queries) ListRecoverableAgentThreads(ctx context.Context) ([]AgentThread, error) {
	return queryMany(ctx, q, listRecoverableAgentThreadsSQL, scanAgentThread)
}

func (q *Queries) UpsertAgentThread(ctx context.Context, arg UpsertAgentThreadParams) error {
	return q.exec(ctx, upsertAgentThreadSQL, arg.ThreadID, arg.Prompt, arg.Model, arg.Cwd, arg.Status, arg.Port, arg.PID, arg.CreatedAt, arg.UpdatedAt, arg.OwnerThreadID)
}

func (q *Queries) UpdateAgentThreadStatus(ctx context.Context, arg UpdateAgentThreadStatusParams) error {
	return q.exec(ctx, updateAgentThreadStatusSQL, arg.ThreadID, arg.Status, arg.UpdatedAt)
}

func (q *Queries) DeleteAgentThreadByID(ctx context.Context, arg DeleteAgentThreadByIDParams) error {
	return q.exec(ctx, deleteAgentThreadByIDSQL, arg.ThreadID)
}

func (q *Queries) ResetRunningAgentThreads(ctx context.Context) error {
	return q.exec(ctx, resetRunningAgentThreadsSQL)
}

func (q *Queries) ExpireStaleAgentThreads(ctx context.Context, arg ExpireStaleAgentThreadsParams) (int64, error) {
	return q.execRows(ctx, expireStaleAgentThreadsSQL, arg.UpdatedAt, arg.Cutoff)
}

func (q *Queries) AgentThreadRunningExists(ctx context.Context, threadID string) (bool, error) {
	return queryOne(ctx, q, agentThreadRunningExistsSQL, scanValue[bool], threadID)
}

func (q *Queries) ListAgentThreadCwds(ctx context.Context) ([]AgentThreadCwdRow, error) {
	return queryMany(ctx, q, listAgentThreadCwdsSQL, scanAgentThreadCwdRow)
}

func (q *Queries) ListAgentThreadCwdsByPrefix(ctx context.Context, prefix string) ([]AgentThreadCwdRow, error) {
	return queryMany(ctx, q, listAgentThreadCwdsByPrefixSQL, scanAgentThreadCwdRow, prefix)
}

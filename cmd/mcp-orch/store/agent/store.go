// Package agent provides lightweight store implementations for
// orchestration.AgentThreadStore and orchestration.AgentBindingStore
// that query the shared PostgreSQL tables directly via pgxpool.Pool.
//
// This avoids importing internal/store/thread and internal/store/binding,
// which would violate mcp-service-convention.md S3.1.
package agent

import (
	"context"
	"fmt"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ──────────────────────────────────────────────────────────────────────
// AgentThreadStore
// ──────────────────────────────────────────────────────────────────────

type threadStore struct{ pool *pgxpool.Pool }

// NewThreadStore returns an orchestration.AgentThreadStore backed by pool.
// NewThreadStore 创建线程存储。
func NewThreadStore(pool *pgxpool.Pool) orchestration.AgentThreadStore {
	return &threadStore{pool: pool}
}

// listSQL is the same query as internal/store/sqlc ListAgentThreads but
// only selects the columns needed by orchestration.PersistedThread.
const listSQL = `
SELECT
    t.thread_id, t.name, t.prompt, t.cwd, t.status, t.port, t.pid,
    t.created_at, t.updated_at, t.pending_launch, t.parent_agent_id,
    COALESCE((
        SELECT b.agent_id
        FROM agent_provider_binding b
        WHERE b.provider_thread_id = t.thread_id
           OR b.codex_thread_id    = t.thread_id
        ORDER BY b.updated_at DESC
        LIMIT 1
    ), '') AS agent_id
FROM agent_threads t
ORDER BY t.created_at DESC
`

// ListAll 列出all。
func (s *threadStore) ListAll(ctx context.Context) ([]orchestration.PersistedThread, error) {
	rows, err := s.pool.Query(ctx, listSQL)
	if err != nil {
		return nil, wrapThread(err, "list_all")
	}
	defer rows.Close()
	var out []orchestration.PersistedThread
	for rows.Next() {
		var t orchestration.PersistedThread
		var agentID any
		if err := rows.Scan(
			&t.ThreadID, &t.Name, &t.Prompt, &t.Cwd, &t.Status,
			&t.Port, &t.PID, &t.CreatedAt, &t.UpdatedAt,
			&t.PendingLaunch, &t.ParentAgentID, &agentID,
		); err != nil {
			return nil, wrapThread(err, "list_all")
		}
		t.AgentID = stringFromAny(agentID)
		out = append(out, t)
	}
	return out, wrapThread(rows.Err(), "list_all")
}

const getByIDSQL = `
SELECT
    t.thread_id, t.name, t.prompt, t.cwd, t.status, t.port, t.pid,
    t.created_at, t.updated_at, t.pending_launch, t.parent_agent_id,
    COALESCE((
        SELECT b.agent_id
        FROM agent_provider_binding b
        WHERE b.provider_thread_id = t.thread_id
           OR b.codex_thread_id    = t.thread_id
        ORDER BY b.updated_at DESC
        LIMIT 1
    ), '') AS agent_id
FROM agent_threads t
WHERE t.thread_id = $1
LIMIT 1
`

// GetByThreadID 按线程ID读取编排。
func (s *threadStore) GetByThreadID(ctx context.Context, threadID string) (*orchestration.PersistedThread, error) {
	row := s.pool.QueryRow(ctx, getByIDSQL, threadID)
	var t orchestration.PersistedThread
	var agentID any
	err := row.Scan(
		&t.ThreadID, &t.Name, &t.Prompt, &t.Cwd, &t.Status,
		&t.Port, &t.PID, &t.CreatedAt, &t.UpdatedAt,
		&t.PendingLaunch, &t.ParentAgentID, &agentID,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, wrapThread(err, "get_by_thread_id")
		}
		return nil, wrapThread(err, "get_by_thread_id")
	}
	t.AgentID = stringFromAny(agentID)
	return &t, nil
}

const updateStatusSQL = `
UPDATE agent_threads
SET status = $2, updated_at = $3
WHERE thread_id = $1
`

// UpdateStatus 更新状态。
func (s *threadStore) UpdateStatus(ctx context.Context, params orchestration.PersistedThreadStatusUpdate) error {
	_, err := s.pool.Exec(ctx, updateStatusSQL, params.ThreadID, params.Status, params.UpdatedAt)
	return wrapThread(err, "update_status")
}

// ──────────────────────────────────────────────────────────────────────
// AgentBindingStore
// ──────────────────────────────────────────────────────────────────────

type bindingStore struct{ pool *pgxpool.Pool }

// NewBindingStore returns an orchestration.AgentBindingStore backed by pool.
// NewBindingStore 创建binding存储。
func NewBindingStore(pool *pgxpool.Pool) orchestration.AgentBindingStore {
	return &bindingStore{pool: pool}
}

const getBindingSQL = `
SELECT agent_id, provider, provider_thread_id, codex_thread_id, cwd,
       archived, created_at, updated_at
FROM agent_provider_binding
WHERE agent_id = $1
`

// GetByAgentID 按代理ID读取编排。
func (s *bindingStore) GetByAgentID(ctx context.Context, agentID string) (*orchestration.PersistedBinding, error) {
	row := s.pool.QueryRow(ctx, getBindingSQL, agentID)
	var b orchestration.PersistedBinding
	err := row.Scan(
		&b.AgentID, &b.Provider, &b.ProviderThreadID,
		&b.CodexThreadID, &b.Cwd, &b.Archived,
		&b.CreatedAt, &b.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, wrapBinding(err, "get_by_agent_id")
		}
		return nil, wrapBinding(err, "get_by_agent_id")
	}
	return &b, nil
}

const setArchivedSQL = `
UPDATE agent_provider_binding
SET archived = $1, updated_at = $2
WHERE agent_id = $3
`

// SetArchived 设置archived。
func (s *bindingStore) SetArchived(ctx context.Context, params orchestration.PersistedBindingArchiveUpdate) error {
	_, err := s.pool.Exec(ctx, setArchivedSQL, params.Archived, params.UpdatedAt, params.AgentID)
	return wrapBinding(err, "set_archived")
}

// ──────────────────────────────────────────────────────────────────────
// helpers
// ──────────────────────────────────────────────────────────────────────

func wrapThread(err error, op string) error  { return platformdb.WrapStoreError(err, op, "thread") }
func wrapBinding(err error, op string) error { return platformdb.WrapStoreError(err, op, "binding") }

func stringFromAny(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case *string:
		if typed == nil {
			return ""
		}
		return *typed
	default:
		return fmt.Sprint(typed)
	}
}

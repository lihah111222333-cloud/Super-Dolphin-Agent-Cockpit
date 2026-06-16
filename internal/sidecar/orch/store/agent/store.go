// Package agent provides lightweight store implementations for
// orchestration.AgentThreadStore and orchestration.AgentBindingStore
// that query the shared SQLite tables directly via database/sql.
//
// This avoids importing internal/store/thread and internal/store/binding,
// which would violate mcp-service-convention.md S3.1.
package agent

import (
	"context"
	"database/sql"
	"fmt"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/orchestration"
)

// ──────────────────────────────────────────────────────────────────────
// AgentThreadStore
// ──────────────────────────────────────────────────────────────────────

type threadStore struct{ db *sql.DB }

// NewThreadStore 创建基于 SQLite 连接的编排线程存储。
func NewThreadStore(db *sql.DB) orchestration.AgentThreadStore {
	return &threadStore{db: db}
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

// ListAll 返回所有已持久化线程及其最新 provider 绑定。
func (s *threadStore) ListAll(ctx context.Context) ([]orchestration.PersistedThread, error) {
	rows, err := s.db.QueryContext(ctx, listSQL)
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
WHERE t.thread_id = ?
LIMIT 1
`

// GetByThreadID 按线程 ID 读取持久化线程及其最新 provider 绑定。
func (s *threadStore) GetByThreadID(ctx context.Context, threadID string) (*orchestration.PersistedThread, error) {
	row := s.db.QueryRowContext(ctx, getByIDSQL, threadID)
	var t orchestration.PersistedThread
	var agentID any
	err := row.Scan(
		&t.ThreadID, &t.Name, &t.Prompt, &t.Cwd, &t.Status,
		&t.Port, &t.PID, &t.CreatedAt, &t.UpdatedAt,
		&t.PendingLaunch, &t.ParentAgentID, &agentID,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, wrapThread(err, "get_by_thread_id")
		}
		return nil, wrapThread(err, "get_by_thread_id")
	}
	t.AgentID = stringFromAny(agentID)
	return &t, nil
}

const updateStatusSQL = `
UPDATE agent_threads
SET status = ?, updated_at = ?
WHERE thread_id = ?
`

// UpdateStatus 更新持久化线程的运行状态和更新时间。
func (s *threadStore) UpdateStatus(ctx context.Context, params orchestration.PersistedThreadStatusUpdate) error {
	_, err := s.db.ExecContext(ctx, updateStatusSQL, params.Status, params.UpdatedAt, params.ThreadID)
	return wrapThread(err, "update_status")
}

// ──────────────────────────────────────────────────────────────────────
// AgentBindingStore
// ──────────────────────────────────────────────────────────────────────

type bindingStore struct{ db *sql.DB }

// NewBindingStore 创建基于 SQLite 连接的 provider 绑定存储。
func NewBindingStore(db *sql.DB) orchestration.AgentBindingStore {
	return &bindingStore{db: db}
}

const getBindingSQL = `
SELECT agent_id, provider, provider_thread_id, codex_thread_id, cwd,
       archived, created_at, updated_at
FROM agent_provider_binding
WHERE agent_id = ?
`

// GetByAgentID 按 agent ID 读取 provider 绑定。
func (s *bindingStore) GetByAgentID(ctx context.Context, agentID string) (*orchestration.PersistedBinding, error) {
	row := s.db.QueryRowContext(ctx, getBindingSQL, agentID)
	var b orchestration.PersistedBinding
	err := row.Scan(
		&b.AgentID, &b.Provider, &b.ProviderThreadID,
		&b.CodexThreadID, &b.Cwd, &b.Archived,
		&b.CreatedAt, &b.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, wrapBinding(err, "get_by_agent_id")
		}
		return nil, wrapBinding(err, "get_by_agent_id")
	}
	return &b, nil
}

const setArchivedSQL = `
UPDATE agent_provider_binding
SET archived = ?, updated_at = ?
WHERE agent_id = ?
`

// SetArchived 更新 provider 绑定的归档状态。
func (s *bindingStore) SetArchived(ctx context.Context, params orchestration.PersistedBindingArchiveUpdate) error {
	_, err := s.db.ExecContext(ctx, setArchivedSQL, params.Archived, params.UpdatedAt, params.AgentID)
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

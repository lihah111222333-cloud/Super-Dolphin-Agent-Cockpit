package agent

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone"
)

// SessionStore 复用当前 thread/binding 表，原子创建独立 CLI 身份，不拥有 runtime 状态机。
type SessionStore struct{ db *sql.DB }

// NewSessionStore 创建 sidecar 自有写端，复用本包既有 database/sql 存储边界。
func NewSessionStore(db *sql.DB) *SessionStore { return &SessionStore{db: db} }

// CreateSession 两行在同一事务提交；重复身份明确冲突，不覆盖其他任务。
func (s *SessionStore) CreateSession(ctx context.Context, record standalone.SessionRecord) error {
	if s.db == nil || record.AgentID == "" || record.ThreadID == "" || record.CreatedAt.IsZero() {
		return errors.New("CLI session store requires database and complete identity")
	}
	if !filepath.IsAbs(record.CWD) || (record.Provider != "codex" && record.Provider != "claude") {
		return errors.New("CLI session store requires absolute cwd and known provider")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := insertSessionRows(ctx, tx, record); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	return tx.Commit()
}

// insertSessionRows 使用明确列列表，不复制完整启动配置、prompt 或认证材料。
func insertSessionRows(ctx context.Context, tx *sql.Tx, r standalone.SessionRecord) error {
	now := r.CreatedAt.UnixMilli()
	_, err := tx.ExecContext(ctx, `INSERT INTO agent_threads
		(thread_id,name,model,cwd,status,parent_agent_id,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?)`, r.ThreadID, r.Name, r.Model, r.CWD, "idle", r.ParentID, now, now)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO agent_provider_binding
		(agent_id,provider,provider_thread_id,cwd,parent_agent_id,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?)`, r.AgentID, r.Provider, r.ThreadID, r.CWD, r.ParentID, now, now)
	return err
}

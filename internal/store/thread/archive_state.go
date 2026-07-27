package thread

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
)

const archiveStateWriteRetryAttempts = 3

type archiveStateStore struct {
	db      *sql.DB
	queries *sqlc.Queries
}

// SetArchiveState 在同一事务内更新 thread 状态和 binding 归档标记。
func (s *pagedStore) SetArchiveState(ctx context.Context, params ArchiveStateParams) error {
	if s == nil || s.archiveState == nil {
		return wrapThreadError(errors.New("thread archive state store requires explicit DB"), "set_archive_state")
	}
	return s.archiveState.SetArchiveState(ctx, params)
}

// SetArchiveState 校验原子写入目标，并以 IMMEDIATE 事务串行化并发归档切换。
func (s *archiveStateStore) SetArchiveState(ctx context.Context, params ArchiveStateParams) error {
	if s == nil || s.db == nil || s.queries == nil {
		return wrapThreadError(errors.New("thread archive state store requires DB and sqlc queries"), "set_archive_state")
	}
	params.ThreadID = strings.TrimSpace(params.ThreadID)
	params.AgentID = strings.TrimSpace(params.AgentID)
	if params.ThreadID == "" || params.AgentID == "" || params.UpdatedAt <= 0 {
		return wrapThreadError(errors.New("thread archive state requires thread_id, agent_id, and updated_at"), "set_archive_state")
	}
	err := platformdb.BoundedWriteRetry(ctx, archiveStateWriteRetryAttempts, func() error {
		return platformdb.WithImmediateTx(ctx, s.db, func(tx *sql.Tx) error {
			return setArchiveStateInTx(ctx, s.queries.WithTx(tx), params)
		})
	})
	return wrapThreadError(err, "set_archive_state")
}

// setArchiveStateInTx 在单一事务查询器上写入两表，并拒绝任一目标缺失。
func setArchiveStateInTx(ctx context.Context, q *sqlc.Queries, params ArchiveStateParams) error {
	status := "created"
	if params.Archived {
		status = "archived"
	}
	threadRows, err := q.UpdateAgentThreadStatus(ctx, sqlc.UpdateAgentThreadStatusParams{
		Status: status, UpdatedAt: params.UpdatedAt, ThreadID: params.ThreadID,
	})
	if err != nil {
		return err
	}
	if threadRows != 1 {
		return platformdb.ErrNotFound
	}
	bindingRows, err := q.UpdateAgentProviderBindingArchived(ctx, sqlc.UpdateAgentProviderBindingArchivedParams{
		Archived: boolToInt64(params.Archived), UpdatedAt: params.UpdatedAt, AgentID: params.AgentID,
	})
	if err != nil {
		return err
	}
	if bindingRows != 1 {
		return platformdb.ErrNotFound
	}
	return nil
}

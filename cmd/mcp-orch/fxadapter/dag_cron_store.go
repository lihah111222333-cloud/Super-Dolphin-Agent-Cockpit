package fxadapter

import (
	"context"
	"errors"
	"fmt"
	"time"

	orchcron "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/cron"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const advisoryUnlockTimeout = 5 * time.Second

type sqlDAGScheduleStore struct {
	q *sqlc.Queries
}

func NewSQLDAGScheduleStore(q *sqlc.Queries) (orchcron.DAGScheduleStore, error) {
	if q == nil {
		return nil, orchcron.ErrNilScheduleStore
	}
	return &sqlDAGScheduleStore{q: q}, nil
}

func (s *sqlDAGScheduleStore) DueDAGs(ctx context.Context, now time.Time) ([]orchcron.DueDAG, error) {
	rows, err := s.q.ListDueScheduledTaskDags(ctx, pgtype.Timestamptz{Time: now, Valid: true})
	if err != nil {
		return nil, err
	}
	dags := make([]orchcron.DueDAG, 0, len(rows))
	for _, row := range rows {
		if !row.NextRunAt.Valid {
			return nil, fmt.Errorf("scheduled dag %q has null next_run_at", row.DagKey)
		}
		dags = append(dags, orchcron.DueDAG{
			DagKey:   row.DagKey,
			CronExpr: row.CronExpr,
			DueAt:    row.NextRunAt.Time,
		})
	}
	return dags, nil
}

func (s *sqlDAGScheduleStore) UpdateNextRun(ctx context.Context, dagKey string, dueAt time.Time, nextRunAt time.Time) error {
	rows, err := s.q.UpdateTaskDagNextRun(ctx, sqlc.UpdateTaskDagNextRunParams{
		NextRunAt: pgtype.Timestamptz{Time: nextRunAt, Valid: true},
		DagKey:    dagKey,
		DueAt:     pgtype.Timestamptz{Time: dueAt, Valid: true},
	})
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("%w: dag_key=%s rows=%d", orchcron.ErrScheduleStateChanged, dagKey, rows)
	}
	return nil
}

type pgAdvisoryLocker struct {
	pool   *pgxpool.Pool
	lockID int64
}

func NewPGAdvisoryLocker(pool *pgxpool.Pool, lockID int64) (orchcron.AdvisoryLocker, error) {
	if pool == nil {
		return nil, orchcron.ErrNilLockPool
	}
	return &pgAdvisoryLocker{pool: pool, lockID: lockID}, nil
}

func (l *pgAdvisoryLocker) TryLock(ctx context.Context) (orchcron.AdvisoryLockHandle, bool, error) {
	conn, err := l.pool.Acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	lockConn := newPGXPoolAdvisoryLockConn(conn)
	acquired, err := lockConn.tryAdvisoryLock(ctx, l.lockID)
	if err != nil {
		lockConn.release()
		return nil, false, err
	}
	if !acquired {
		lockConn.release()
		return nil, false, nil
	}
	return &pgAdvisoryLockHandle{conn: lockConn, lockID: l.lockID}, true, nil
}

type pgAdvisoryLockHandle struct {
	conn   advisoryLockConn
	lockID int64
}

type advisoryLockConn interface {
	tryAdvisoryLock(ctx context.Context, lockID int64) (bool, error)
	unlockAdvisoryLock(ctx context.Context, lockID int64) (bool, error)
	release()
	hijackAndClose(ctx context.Context) error
}

type pgxpoolAdvisoryLockConn struct {
	conn *pgxpool.Conn
	q    *sqlc.Queries
}

func newPGXPoolAdvisoryLockConn(conn *pgxpool.Conn) pgxpoolAdvisoryLockConn {
	return pgxpoolAdvisoryLockConn{conn: conn, q: sqlc.New(conn)}
}

func (c pgxpoolAdvisoryLockConn) tryAdvisoryLock(ctx context.Context, lockID int64) (bool, error) {
	return c.q.TryTaskDagAdvisoryLock(ctx, lockID)
}

func (c pgxpoolAdvisoryLockConn) unlockAdvisoryLock(ctx context.Context, lockID int64) (bool, error) {
	return c.q.UnlockTaskDagAdvisoryLock(ctx, lockID)
}

func (c pgxpoolAdvisoryLockConn) release() {
	c.conn.Release()
}

func (c pgxpoolAdvisoryLockConn) hijackAndClose(ctx context.Context) error {
	raw := c.conn.Hijack()
	return raw.Close(ctx)
}

func (h *pgAdvisoryLockHandle) Unlock(ctx context.Context) error {
	release := true
	defer func() {
		if release {
			h.conn.release()
		}
	}()
	unlocked, err := h.conn.unlockAdvisoryLock(ctx, h.lockID)
	if err != nil {
		release = false
		closeCtx, cancel := platformconfig.WithTimeout(context.Background(), advisoryUnlockTimeout)
		defer cancel()
		_ = h.conn.hijackAndClose(closeCtx)
		return err
	}
	if !unlocked {
		return errors.New("cron: advisory lock was not held")
	}
	return nil
}

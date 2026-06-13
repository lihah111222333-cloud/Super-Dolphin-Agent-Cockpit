package fxadapter

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	orchcron "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/cron"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
)

const sqliteRuntimeLockLease = 2 * time.Minute

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
	nowMillis := now.UTC().UnixMilli()
	rows, err := s.q.ListDueScheduledTaskDags(ctx, sqlc.ListDueScheduledTaskDagsParams{NextRunAt: &nowMillis})
	if err != nil {
		return nil, err
	}
	dags := make([]orchcron.DueDAG, 0, len(rows))
	for _, row := range rows {
		if row.NextRunAt == nil {
			return nil, fmt.Errorf("scheduled dag %q has null next_run_at", row.DagKey)
		}
		dags = append(dags, orchcron.DueDAG{
			DagKey:   row.DagKey,
			CronExpr: row.CronExpr,
			DueAt:    sqlc.TimeValue(*row.NextRunAt),
		})
	}
	return dags, nil
}

type sqliteRuntimeLocker struct {
	db      *sql.DB
	lockKey string
	holder  string
	lease   time.Duration
}

func NewSQLiteRuntimeLocker(db *sql.DB, lockKey string) (orchcron.AdvisoryLocker, error) {
	if db == nil {
		return nil, orchcron.ErrNilLockPool
	}
	if lockKey == "" {
		return nil, errors.New("cron: empty runtime lock key")
	}
	return &sqliteRuntimeLocker{
		db:      db,
		lockKey: lockKey,
		holder:  runtimeLockHolder(),
		lease:   sqliteRuntimeLockLease,
	}, nil
}

func (l *sqliteRuntimeLocker) TryLock(ctx context.Context) (orchcron.AdvisoryLockHandle, bool, error) {
	nowMillis := time.Now().UTC().UnixMilli()
	leaseExpiresAt := time.Now().UTC().Add(l.lease).UnixMilli()
	result, err := l.db.ExecContext(ctx, acquireRuntimeLockSQL,
		l.lockKey,
		l.holder,
		leaseExpiresAt,
		nowMillis,
		nowMillis,
		l.holder,
	)
	if err != nil {
		return nil, false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return nil, false, err
	}
	if rows == 0 {
		return nil, false, nil
	}
	return sqliteRuntimeLockHandle{db: l.db, lockKey: l.lockKey, holder: l.holder}, true, nil
}

type sqliteRuntimeLockHandle struct {
	db      *sql.DB
	lockKey string
	holder  string
}

func (h sqliteRuntimeLockHandle) Unlock(ctx context.Context) error {
	result, err := h.db.ExecContext(ctx, releaseRuntimeLockSQL, h.lockKey, h.holder)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return errors.New("cron: runtime lock was not held")
	}
	return nil
}

func runtimeLockHolder() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown-host"
	}
	return fmt.Sprintf("%s:%d", host, os.Getpid())
}

const acquireRuntimeLockSQL = `
INSERT INTO runtime_locks (lock_key, holder, lease_expires_at, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(lock_key) DO UPDATE
SET holder = EXCLUDED.holder,
    lease_expires_at = EXCLUDED.lease_expires_at,
    updated_at = EXCLUDED.updated_at
WHERE runtime_locks.lease_expires_at < ?
   OR runtime_locks.holder = ?
`

const releaseRuntimeLockSQL = `
DELETE FROM runtime_locks
WHERE lock_key = ? AND holder = ?
`

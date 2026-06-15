package fxadapter

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	orchcron "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/cron"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlctx"
)

const sqliteRuntimeLockLease = 2 * time.Minute

var runtimeLockProcessStartNonce = strconv.FormatInt(time.Now().UTC().UnixNano(), 36)

type sqlDAGScheduleStore struct {
	q *sqlc.Queries
}

// NewSQLDAGScheduleStore 创建基于 SQLite 查询集的 DAG 计划存储适配器。
func NewSQLDAGScheduleStore(q *sqlc.Queries) (orchcron.DAGScheduleStore, error) {
	if q == nil {
		return nil, orchcron.ErrNilScheduleStore
	}
	return &sqlDAGScheduleStore{q: q}, nil
}

// DueDAGs 查询已到期的 DAG 计划并转换为编排层 DTO。
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

// NewSQLiteRuntimeLocker 创建基于 SQLite runtime_locks 表的运行时租约锁。
func NewSQLiteRuntimeLocker(db *sql.DB, lockKey string) (orchcron.RuntimeLocker, error) {
	if db == nil {
		return nil, orchcron.ErrNilLockPool
	}
	if lockKey == "" {
		return nil, errors.New("cron: empty runtime lock key")
	}
	holder, err := runtimeLockHolder()
	if err != nil {
		return nil, err
	}
	return &sqliteRuntimeLocker{
		db:      db,
		lockKey: lockKey,
		holder:  holder,
		lease:   sqliteRuntimeLockLease,
	}, nil
}

// TryLock 通过 SQLite 条件写入获取运行时租约锁。
func (l *sqliteRuntimeLocker) TryLock(ctx context.Context) (orchcron.RuntimeLockHandle, bool, error) {
	nowMillis := time.Now().UTC().UnixMilli()
	leaseExpiresAt := time.UnixMilli(nowMillis).UTC().Add(l.lease).UnixMilli()
	var rows int64
	err := sqlctx.WithWriteRetry(ctx, func() error {
		result, err := l.db.ExecContext(ctx, acquireRuntimeLockSQL,
			l.lockKey,
			l.holder,
			leaseExpiresAt,
			nowMillis,
			nowMillis,
		)
		if err != nil {
			return err
		}
		rows, err = result.RowsAffected()
		return err
	})
	if err != nil {
		return nil, false, err
	}
	if rows == 0 {
		return nil, false, nil
	}
	return sqliteRuntimeLockHandle{db: l.db, lockKey: l.lockKey, holder: l.holder, lease: l.lease}, true, nil
}

type sqliteRuntimeLockHandle struct {
	db      *sql.DB
	lockKey string
	holder  string
	lease   time.Duration
}

// Renew 续租当前 holder 持有的运行时锁。
func (h sqliteRuntimeLockHandle) Renew(ctx context.Context) error {
	nowMillis := time.Now().UTC().UnixMilli()
	leaseExpiresAt := time.UnixMilli(nowMillis).UTC().Add(h.lease).UnixMilli()
	var rows int64
	err := sqlctx.WithWriteRetry(ctx, func() error {
		result, err := h.db.ExecContext(ctx, renewRuntimeLockSQL, leaseExpiresAt, nowMillis, h.lockKey, h.holder)
		if err != nil {
			return err
		}
		rows, err = result.RowsAffected()
		return err
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return errors.New("cron: runtime lock was not held by holder")
	}
	return nil
}

// Unlock 释放当前 holder 持有的运行时锁。
func (h sqliteRuntimeLockHandle) Unlock(ctx context.Context) error {
	var rows int64
	err := sqlctx.WithWriteRetry(ctx, func() error {
		result, err := h.db.ExecContext(ctx, releaseRuntimeLockSQL, h.lockKey, h.holder)
		if err != nil {
			return err
		}
		rows, err = result.RowsAffected()
		return err
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return errors.New("cron: runtime lock was not held by holder")
	}
	return nil
}

// runtimeLockHolder 构造进程级唯一的运行时锁 holder 标识。
func runtimeLockHolder() (string, error) {
	host, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("cron: resolve runtime lock hostname: %w", err)
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return "", errors.New("cron: runtime lock hostname is empty")
	}
	return fmt.Sprintf("%s:%d:%s", host, os.Getpid(), runtimeLockProcessStartNonce), nil
}

const acquireRuntimeLockSQL = `
INSERT INTO runtime_locks (lock_key, holder, lease_expires_at, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(lock_key) DO UPDATE
SET holder = EXCLUDED.holder,
    lease_expires_at = EXCLUDED.lease_expires_at,
    updated_at = EXCLUDED.updated_at
WHERE runtime_locks.lease_expires_at < ?
`

const renewRuntimeLockSQL = `
UPDATE runtime_locks
SET lease_expires_at = ?,
    updated_at = ?
WHERE lock_key = ? AND holder = ?
`

const releaseRuntimeLockSQL = `
DELETE FROM runtime_locks
WHERE lock_key = ? AND holder = ?
`

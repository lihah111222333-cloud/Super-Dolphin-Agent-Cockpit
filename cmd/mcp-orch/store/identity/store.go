package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Store 是唯一身份 owner 的 SQL 访问层，不写入 legacy agent_* 投影表。
type Store struct {
	db      *sql.DB
	queries *storedb.Queries
}

// NewStore 创建身份 owner；缺数据库时立即失败，不提供隐式内存实现。
func NewStore(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("identity store database is required")
	}
	return &Store{db: db, queries: storedb.New(db)}, nil
}

func (s *Store) ready(ctx context.Context) error {
	if s == nil || s.db == nil || s.queries == nil {
		return errors.New("identity store dependencies are missing")
	}
	if ctx == nil {
		return errors.New("identity store context is required")
	}
	return ctx.Err()
}

func (s *Store) withWrite(ctx context.Context, fn func(*storedb.Queries) error) error {
	if err := s.ready(ctx); err != nil {
		return err
	}
	return sqlctx.WithImmediateTx(ctx, s.db, s.queries, func(q *storedb.Queries, _ storedb.DBTX) error {
		return fn(q)
	})
}

func classifyIdentityDBError(operation string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrIdentityNotFound
	}
	var sqliteErr *modernsqlite.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() {
		case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
			return errors.Join(ErrIdentityConflict, err)
		case sqlite3.SQLITE_CONSTRAINT_CHECK, sqlite3.SQLITE_CONSTRAINT_TRIGGER, sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY:
			return errors.Join(ErrIdentityFence, err)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func rowsAffectedOrFence(operation string, affected int64) error {
	if affected == 1 {
		return nil
	}
	return fmt.Errorf("%w: %s affected %d rows", ErrIdentityFence, operation, affected)
}

func reservationCommandID(value string) *string {
	if value == "" {
		return nil
	}
	copyValue := value
	return &copyValue
}

func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

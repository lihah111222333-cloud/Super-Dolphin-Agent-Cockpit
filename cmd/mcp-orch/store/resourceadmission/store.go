package resourceadmission

import (
	"context"
	"database/sql"
	"errors"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

// Clock 提供policy snapshot的权威创建时间，测试可显式注入固定UTC时间。
type Clock func() time.Time

// Store 独占RESOURCE effective-policy与reserved snapshot的SQLite事务边界。
type Store struct {
	db      *sql.DB
	queries *storedb.Queries
	now     Clock
}

// NewStore 创建RESOURCE持久化owner；数据库和可选时钟必须显式可用。
func NewStore(db *sql.DB, clocks ...Clock) (*Store, error) {
	if db == nil {
		return nil, errors.New("resource admission store requires database")
	}
	if len(clocks) > 1 {
		return nil, errors.New("resource admission store accepts at most one clock")
	}
	now := Clock(func() time.Time { return time.Now().UTC() })
	if len(clocks) == 1 {
		if clocks[0] == nil {
			return nil, errors.New("resource admission store clock cannot be nil")
		}
		now = clocks[0]
	}
	return &Store{db: db, queries: storedb.New(db), now: now}, nil
}

// ready 检查RESOURCE owner依赖、上下文和时钟是否可用于权威读写。
func (s *Store) ready(ctx context.Context) error {
	if s == nil || s.db == nil || s.queries == nil || s.now == nil {
		return ErrStoreUninitialized
	}
	if ctx == nil {
		return errors.Join(ErrStoreUninitialized, errors.New("resource admission context is nil"))
	}
	return ctx.Err()
}

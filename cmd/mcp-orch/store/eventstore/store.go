package eventstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Store 是 canonical event owner；不写 direct-peer、terminal 或 DAG 事件投影。
type Store struct {
	db           *sql.DB
	queries      *storedb.Queries
	verifier     FenceVerifier
	retentionPin RetentionPinVerifier
	notifier     EventNotifier
}

// NewStore 创建 event owner；必须显式提供只读 fence verifier，不提供默认放行实现。
func NewStore(db *sql.DB, verifier FenceVerifier) (*Store, error) {
	return newStore(db, verifier, nil, nil)
}

// NewStoreWithWatcher 构造未注册的本地候选；append 路径仅在事务成功提交后发出
// 提示唤醒，watcher 不作为持久化或 transport owner。
func NewStoreWithWatcher(db *sql.DB, verifier FenceVerifier, notifier EventNotifier) (*Store, error) {
	if eventNotifierIsNil(notifier) {
		return nil, errors.New("event store watcher notifier is required")
	}
	return newStore(db, verifier, notifier, nil)
}

// NewStoreWithRetentionPinVerifier 构造启用保留 pin 校验的本地候选 owner。
// 该构造不进入 Fx；调用方必须显式提供同库、事务绑定的 verifier。
func NewStoreWithRetentionPinVerifier(db *sql.DB, verifier FenceVerifier, retentionPin RetentionPinVerifier) (*Store, error) {
	if verifierIsNil(retentionPin) {
		return nil, errors.New("event store retention pin verifier is required")
	}
	return newStore(db, verifier, nil, retentionPin)
}

// NewStoreWithRetentionPin 是上一个候选构造的短名称别名，便于 owner-local 调用。
func NewStoreWithRetentionPin(db *sql.DB, verifier FenceVerifier, retentionPin RetentionPinVerifier) (*Store, error) {
	return NewStoreWithRetentionPinVerifier(db, verifier, retentionPin)
}

// NewStoreWithWatcherAndRetentionPin 构造同时启用提交后 watcher 与保留 pin 校验的候选 owner。
// 两个可选 seam 都只在本地显式接入，不改变默认 Fx 构造路径。
func NewStoreWithWatcherAndRetentionPin(db *sql.DB, verifier FenceVerifier, notifier EventNotifier, retentionPin RetentionPinVerifier) (*Store, error) {
	if eventNotifierIsNil(notifier) {
		return nil, errors.New("event store watcher notifier is required")
	}
	if verifierIsNil(retentionPin) {
		return nil, errors.New("event store retention pin verifier is required")
	}
	return newStore(db, verifier, notifier, retentionPin)
}

// newStore 校验并组装 EventStore 的显式依赖；不创建默认 verifier 或 pin。
func newStore(db *sql.DB, verifier FenceVerifier, notifier EventNotifier, retentionPin RetentionPinVerifier) (*Store, error) {
	if db == nil || verifierIsNil(verifier) {
		return nil, errors.New("event store database and fence verifier are required")
	}
	if notifier != nil && eventNotifierIsNil(notifier) {
		return nil, errors.New("event store watcher notifier is invalid")
	}
	if retentionPin != nil && verifierIsNil(retentionPin) {
		return nil, errors.New("event store retention pin verifier is invalid")
	}
	return &Store{db: db, queries: storedb.New(db), verifier: verifier, retentionPin: retentionPin, notifier: notifier}, nil
}

func verifierIsNil(verifier any) bool {
	if verifier == nil {
		return true
	}
	value := reflect.ValueOf(verifier)
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Func, reflect.Slice, reflect.Chan:
		return value.IsNil()
	default:
		return false
	}
}

// ready 在每条 owner 操作前检查数据库、验证器、候选 pin 和上下文。
func (s *Store) ready(ctx context.Context) error {
	if s == nil || s.db == nil || s.queries == nil || verifierIsNil(s.verifier) || (s.retentionPin != nil && verifierIsNil(s.retentionPin)) || (s.notifier != nil && eventNotifierIsNil(s.notifier)) {
		return errors.New("event store dependencies are missing")
	}
	if ctx == nil {
		return errors.New("event store context is required")
	}
	return ctx.Err()
}

func (s *Store) withWrite(ctx context.Context, fn func(*storedb.Queries) error) error {
	return s.withWriteTx(ctx, func(q *storedb.Queries, _ storedb.DBTX) error {
		return fn(q)
	})
}

// withWriteTx exposes the same IMMEDIATE transaction to narrowly scoped
// owner callbacks that must perform a transaction-bound identity/fence read.
// It is intentionally private so callers cannot turn the EventStore into a
// general-purpose cross-owner transaction coordinator.
func (s *Store) withWriteTx(ctx context.Context, fn func(*storedb.Queries, storedb.DBTX) error) error {
	if err := s.ready(ctx); err != nil {
		return err
	}
	if fn == nil {
		return errors.New("event store write callback is required")
	}
	return sqlctx.WithImmediateTx(ctx, s.db, s.queries, func(q *storedb.Queries, txdb storedb.DBTX) error {
		return fn(q, txdb)
	})
}

func classifyEventDBError(operation string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrEventNotFound
	}
	var sqliteErr *modernsqlite.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() {
		case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
			return errors.Join(ErrEventConflict, err)
		case sqlite3.SQLITE_CONSTRAINT_CHECK, sqlite3.SQLITE_CONSTRAINT_TRIGGER, sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY:
			return errors.Join(ErrEventFence, err)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func requireEventRows(operation string, affected int64) error {
	if affected == 1 {
		return nil
	}
	return fmt.Errorf("%w: %s affected %d rows", ErrEventFence, operation, affected)
}

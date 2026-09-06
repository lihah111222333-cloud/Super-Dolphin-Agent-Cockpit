package accesswait

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Clock 是授权 owner 的时间注入 seam。旧 Store 调用方未传 clock 时仍保留
// wall-clock 兼容行为；candidate expiry runner 必须显式注入外部 security-time
// owner 的已验证快照，不能依赖该兼容默认。
type Clock func() time.Time

// Store 独占 migration 130 的授权和 wait handle 持久化，不写 direct-peer 或正文表。
type Store struct {
	db         *sql.DB
	queries    *storedb.Queries
	identities IdentityReader
	events     EventReader
	eventsTx   transactionalEventReader
	watcher    EventWatcher
	now        Clock
}

var _ AccessWaitOwner = (*Store)(nil)

// NewStore 创建访问 owner；数据库、身份读者和 canonical 事件读者缺一不可。
func NewStore(db *sql.DB, identities IdentityReader, events EventReader, clocks ...Clock) (*Store, error) {
	return newStore(db, identities, events, nil, clocks...)
}

// NewStoreWithWatcher 构造带可选同进程 canonical EventStore 唤醒 seam 的 durable
// owner；watcher 只作提示，不持久化，也不经 public transport 暴露。
func NewStoreWithWatcher(db *sql.DB, identities IdentityReader, events EventReader, watcher EventWatcher, clocks ...Clock) (*Store, error) {
	if isNilInterface(watcher) {
		return nil, errors.New("access wait watcher cannot be nil")
	}
	return newStore(db, identities, events, watcher, clocks...)
}

func newStore(db *sql.DB, identities IdentityReader, events EventReader, watcher EventWatcher, clocks ...Clock) (*Store, error) {
	if err := validateStoreInputs(db, identities, events, watcher); err != nil {
		return nil, err
	}
	if err := requireTransactionalIdentity(identities); err != nil {
		return nil, err
	}
	eventsTx, err := requireTransactionalEvents(events)
	if err != nil {
		return nil, err
	}
	now, err := resolveClock(clocks)
	if err != nil {
		return nil, err
	}
	return &Store{db: db, queries: storedb.New(db), identities: identities, events: events, eventsTx: eventsTx, watcher: watcher, now: now}, nil
}

// validateStoreInputs 校验数据库、基础读者和可选 watcher，不作默认替换。
func validateStoreInputs(db *sql.DB, identities IdentityReader, events EventReader, watcher EventWatcher) error {
	if db == nil {
		return errors.New("access wait store requires database, identity reader, and event reader")
	}
	if isNilInterface(identities) || isNilInterface(events) {
		return errors.New("access wait store requires database, identity reader, and event reader")
	}
	if watcher != nil && isNilInterface(watcher) {
		return errors.New("access wait watcher is invalid")
	}
	return nil
}

// requireTransactionalIdentity 固定授权 owner 必须使用同一事务身份读者。
func requireTransactionalIdentity(identities IdentityReader) error {
	if _, ok := identities.(transactionalIdentityReader); !ok {
		return errors.New("access wait store requires transactional identity reader")
	}
	return nil
}

// requireTransactionalEvents 固定 wait owner 必须使用事务绑定的 canonical reader。
func requireTransactionalEvents(events EventReader) (transactionalEventReader, error) {
	eventsTx, ok := events.(transactionalEventReader)
	if !ok || isNilInterface(eventsTx) {
		return nil, errors.New("access wait store requires transaction-bound canonical event reader")
	}
	return eventsTx, nil
}

// resolveClock 保留安全时间 owner 语义，并拒绝多 clock 或 typed-nil clock。
func resolveClock(clocks []Clock) (Clock, error) {
	if len(clocks) > 1 {
		return nil, errors.New("access wait store accepts at most one clock")
	}
	if len(clocks) == 1 {
		if clocks[0] == nil {
			return nil, errors.New("access wait store clock cannot be nil")
		}
		return clocks[0], nil
	}
	return func() time.Time { return time.Now().UTC() }, nil
}

func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Func, reflect.Slice, reflect.Chan:
		return rv.IsNil()
	default:
		return false
	}
}

// ready 检查访问 owner 的数据库、依赖端口和上下文是否可用。
func (s *Store) ready(ctx context.Context) error {
	if err := validateReadyStore(s); err != nil {
		return err
	}
	if ctx == nil {
		return errors.New("access wait context is nil")
	}
	return ctx.Err()
}

// validateReadyStore 将每个 owner 依赖逐项 fail-fast，避免 typed-nil 穿透。
func validateReadyStore(s *Store) error {
	if s == nil {
		return errors.New("access wait store dependencies are missing")
	}
	if s.db == nil || s.queries == nil {
		return errors.New("access wait store dependencies are missing")
	}
	if isNilInterface(s.identities) || isNilInterface(s.events) || isNilInterface(s.eventsTx) {
		return errors.New("access wait store dependencies are missing")
	}
	if s.watcher != nil && isNilInterface(s.watcher) {
		return errors.New("access wait store dependencies are missing")
	}
	if s.now == nil {
		return errors.New("access wait store dependencies are missing")
	}
	return nil
}

func (s *Store) withWrite(ctx context.Context, fn func(*storedb.Queries) error) error {
	return s.withWriteTx(ctx, func(q *storedb.Queries, _ storedb.DBTX) error { return fn(q) })
}

// withWriteTx 开启 SQLite IMMEDIATE 事务并把同一连接暴露给需要原子重读的 owner。
func (s *Store) withWriteTx(ctx context.Context, fn func(*storedb.Queries, storedb.DBTX) error) error {
	if err := s.ready(ctx); err != nil {
		return err
	}
	return sqlctx.WithImmediateTx(ctx, s.db, s.queries, func(q *storedb.Queries, txdb storedb.DBTX) error {
		return fn(q, txdb)
	})
}

func classifyDBError(operation string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAccessNotFound
	}
	var sqliteErr *modernsqlite.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() {
		case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
			return errors.Join(ErrAccessConflict, err)
		case sqlite3.SQLITE_CONSTRAINT_CHECK, sqlite3.SQLITE_CONSTRAINT_TRIGGER, sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY:
			return errors.Join(ErrAccessDenied, err)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func requireAffected(kind error, operation string, affected int64) error {
	if affected == 1 {
		return nil
	}
	return fmt.Errorf("%w: %s affected %d rows", kind, operation, affected)
}

func utcText(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

// parseUTC 解析 Go 与 SQLite 允许的固定 UTC 时间格式，拒绝隐式时区转换。
func parseUTC(name, value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse %s: %w", name, err)
	}
	if parsed.Location() != time.UTC || !strings.HasSuffix(value, "Z") {
		return time.Time{}, fmt.Errorf("parse %s: non-canonical UTC timestamp", name)
	}
	canonical := parsed.UTC().Format(time.RFC3339Nano)
	millisecond := parsed.UTC().Format("2006-01-02T15:04:05.000Z")
	second := parsed.UTC().Format("2006-01-02T15:04:05Z")
	if value != canonical && value != millisecond && value != second {
		return time.Time{}, fmt.Errorf("parse %s: non-canonical UTC timestamp", name)
	}
	return parsed.UTC(), nil
}

func newOpaqueID(prefix string) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate %s: %w", prefix, err)
	}
	return prefix + ":" + hex.EncodeToString(raw[:]), nil
}

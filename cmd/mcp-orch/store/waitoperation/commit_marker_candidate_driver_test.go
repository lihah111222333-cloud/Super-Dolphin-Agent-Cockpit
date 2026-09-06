package waitoperation

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
	modernsqlite "modernc.org/sqlite"
)

var errCandidateCommitUnknown = errors.New("candidate driver committed then hid COMMIT result")

// commitUnknownDriver 在真实 SQLite 驱动完成 COMMIT 后注入一次明确的未知结果。
// 它只存在于候选测试文件，不改变生产 Fx、registry 或公共 wait 路径。
type commitUnknownDriver struct {
	base   driver.Driver
	armed  atomic.Bool
	failed atomic.Bool
}

func (d *commitUnknownDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.base.Open(name)
	if err != nil {
		return nil, err
	}
	return &commitUnknownConn{Conn: conn, owner: d}, nil
}

type commitUnknownConn struct {
	driver.Conn
	owner *commitUnknownDriver
}

func (c *commitUnknownConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	execer, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	result, err := execer.ExecContext(ctx, query, args)
	if err != nil || !isCommitStatement(query) {
		return result, err
	}
	if c.owner.armed.Load() && c.owner.failed.CompareAndSwap(false, true) {
		return result, errCandidateCommitUnknown
	}
	return result, nil
}

func (c *commitUnknownConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	queryer, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return queryer.QueryContext(ctx, query, args)
}

func (c *commitUnknownConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	beginner, ok := c.Conn.(driver.ConnBeginTx)
	if !ok {
		return nil, driver.ErrSkip
	}
	return beginner.BeginTx(ctx, opts)
}

func isCommitStatement(query string) bool {
	return strings.EqualFold(strings.TrimSpace(query), "COMMIT")
}

// TestCommitMarkerCandidateRealDriverUnknownAfterCommit 验证 SQL driver 在提交已生效
// 后返回未知错误时，第二个连接只查询原 operation/marker，显式 resolve 后只回放一次。
func TestCommitMarkerCandidateRealDriverUnknownAfterCommit(t *testing.T) {
	driverName := fmt.Sprintf("sqlite-commit-unknown-candidate-%d", time.Now().UnixNano())
	injected := &commitUnknownDriver{base: &modernsqlite.Driver{}}
	sql.Register(driverName, injected)
	fixture := newCommitUnknownFixture(t, driverName)
	clock := fixture.now
	store := mustRealDriverStore(t, fixture.db, clock)
	request := markerPlanRequest(fixture, "marker-real-driver")
	operation := mustPlanMarker(t, store, request)
	predecessor := operation.Targets[0]
	transition := reopenTransition(request, predecessor)
	descriptor := mustCandidateDescriptor(t, request, predecessor, transition)
	injected.armed.Store(true)
	committed := mustRealDriverAppend(t, store, transition, predecessor, injected)
	reopenedDB, reopened := reopenRealDriverStore(t, fixture.db, driverName, clock)
	assertRealDriverQueryAndFence(t, reopened, request, descriptor, committed)
	if _, err := reopened.ResolveCommitMarker(t.Context(), descriptor.MarkerKey()); err != nil {
		t.Fatal(err)
	}
	replayed := mustAppendMarker(t, reopened, transition)
	assertRealDriverReplay(t, reopenedDB, request, replayed, committed)
}

func mustRealDriverStore(t *testing.T, db *sql.DB, clock time.Time) *Store {
	t.Helper()
	markers, err := NewCommitMarkerStore(db, func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStoreWithCommitMarkers(db, func() time.Time { return clock }, markers)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func mustRealDriverAppend(t *testing.T, store *Store, transition TargetTransitionRequest, predecessor TargetState, injected *commitUnknownDriver) TargetState {
	t.Helper()
	committed, err := store.AppendTarget(t.Context(), transition)
	if !errors.Is(err, ErrCommitUnknown) || !errors.Is(err, ErrFenced) {
		t.Fatalf("real driver post-commit error = %#v, want commit-unknown fence", err)
	}
	if committed.Version != predecessor.Version+1 || !injected.failed.Load() {
		t.Fatalf("real driver append = %#v failed=%v, want committed successor and injected fault", committed, injected.failed.Load())
	}
	return committed
}

func reopenRealDriverStore(t *testing.T, db *sql.DB, driverName string, clock time.Time) (*sql.DB, *Store) {
	t.Helper()
	path := sqliteDatabasePath(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopenedDB, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopenedDB.Close() })
	if err := reopenedDB.PingContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	return reopenedDB, mustRealDriverStore(t, reopenedDB, clock)
}

func assertRealDriverQueryAndFence(t *testing.T, store *Store, request PlanRequest, descriptor CommitMarkerDescriptor, committed TargetState) {
	t.Helper()
	if !store.fenced.Load() {
		t.Fatal("cross-connection owner did not retain pending-marker fence")
	}
	queried, err := store.Get(t.Context(), OperationLookup{Key: request.OperationKey, Digest: request.OperationDigest})
	if err != nil || queried.Targets[0].Version != committed.Version {
		t.Fatalf("cross-connection exact query = %#v err=%v, want committed successor", queried, err)
	}
	marker, err := store.commitMarkers.load(t.Context(), descriptor.MarkerKey())
	if err != nil || marker.State != "pending" || marker.PostStateDigest != targetStateDigest(committed) {
		t.Fatalf("cross-connection marker query = %#v err=%v, want pending proven successor", marker, err)
	}
}

func assertRealDriverReplay(t *testing.T, db *sql.DB, request PlanRequest, replayed, committed TargetState) {
	t.Helper()
	if replayed.Version != committed.Version || replayed.RecordDigest != committed.RecordDigest {
		t.Fatalf("resolved replay = %#v, want original successor", replayed)
	}
	var journalCount int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM orchestration_wait_operation_target_journal WHERE operation_key = ?`, request.OperationKey).Scan(&journalCount); err != nil {
		t.Fatal(err)
	}
	if journalCount != 2 {
		t.Fatalf("cross-connection journal rows = %d, want callback not rerun", journalCount)
	}
}

func newCommitUnknownFixture(t *testing.T, driverName string) waitOperationFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wait-operation-driver.sqlite")
	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := sqliteruntime.RunEmbeddedMigrations(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}
	seedWaitOperationIdentity(t, db, "src", "res-src", "inc-src")
	seedWaitOperationIdentity(t, db, "dst", "res-dst", "inc-dst")
	digest := strings.Repeat("a", 64)
	return waitOperationFixture{db: db, now: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
		source: accesswait.FenceSnapshot{AgentID: "src", ReservationID: "res-src", ReservationGeneration: 1, IncarnationID: "inc-src", IncarnationGeneration: 1, BindingVersion: 1, BindingDigest: digest, SourceFenceRef: "source-fence"},
		target: accesswait.FenceSnapshot{AgentID: "dst", ReservationID: "res-dst", ReservationGeneration: 1, IncarnationID: "inc-dst", IncarnationGeneration: 1, BindingVersion: 1, BindingDigest: digest, SourceFenceRef: "target-fence"}}
}

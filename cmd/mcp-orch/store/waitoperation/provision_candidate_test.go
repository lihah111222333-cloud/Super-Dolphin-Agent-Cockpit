package waitoperation

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
)

type candidateEvents struct{}

func (candidateEvents) ListEvents(context.Context, string, string, eventstore.Lane, int64, int64) ([]eventstore.AgentEvent, error) {
	return nil, nil
}
func (candidateEvents) GetSourceState(context.Context, string, string, eventstore.Lane) (eventstore.SourceState, error) {
	return eventstore.SourceState{NextSequence: 1, Status: "active"}, nil
}
func (candidateEvents) ListEventsTx(context.Context, storedb.DBTX, string, string, eventstore.Lane, int64, int64) ([]eventstore.AgentEvent, error) {
	return nil, nil
}
func (candidateEvents) GetEventTx(context.Context, storedb.DBTX, string) (eventstore.AgentEvent, error) {
	return eventstore.AgentEvent{}, nil
}
func (candidateEvents) GetSourceStateTx(context.Context, storedb.DBTX, string, string, eventstore.Lane) (eventstore.SourceState, error) {
	return eventstore.SourceState{NextSequence: 1, Status: "active"}, nil
}

func TestProvisionTargetRequiresExplicitAccessOwner(t *testing.T) {
	fixture := newWaitOperationFixture(t)
	store, err := NewStore(fixture.db, func() time.Time { return fixture.now })
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.ProvisionTarget(t.Context(), ProvisionTargetRequest{OperationKey: "missing-owner", OperationDigest: strings.Repeat("a", 64), TargetKey: "target-1", ExpectedVersion: 1, ExpectedPhase: "planned", ExpectedRecordDigest: strings.Repeat("b", 64)})
	if !errors.Is(err, ErrAccessOwnerRequired) {
		t.Fatalf("ProvisionTarget() = %v, want ErrAccessOwnerRequired", err)
	}
}

func TestProvisionTargetPlannedCommitsGrantWaitAndJournal(t *testing.T) {
	fixture := newWaitOperationFixture(t)
	identities, err := identity.NewStore(fixture.db)
	if err != nil {
		t.Fatal(err)
	}
	access, err := accesswait.NewStore(fixture.db, identities, candidateEvents{}, func() time.Time { return fixture.now })
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStoreWithAccess(fixture.db, func() time.Time { return fixture.now }, access)
	if err != nil {
		t.Fatal(err)
	}
	request := PlanRequest{OperationKey: "provision-atomic", OperationDigest: strings.Repeat("d", 64), CallerPrincipalRef: "principal", ScopeRef: "scope", Source: fixture.source, DeadlineAt: fixture.now.Add(30 * time.Second), Targets: []TargetPlan{{Key: "target-1", Target: fixture.target, Selector: accesswait.EventSelector{Version: 1, SourceID: "task/src", SourceEpoch: "inc-src", Lane: eventstore.LaneControl, TargetAgentID: "dst", EventTypes: []string{"output"}}}}}
	planned, err := store.Plan(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.ProvisionTarget(t.Context(), ProvisionTargetRequest{OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, TargetKey: "target-1", ExpectedVersion: 1, ExpectedPhase: "planned", ExpectedRecordDigest: planned.Targets[0].RecordDigest})
	if err != nil {
		t.Fatalf("ProvisionTarget() error = %v", err)
	}
	assertProvisionedTarget(t, access, request, result)
}

// assertProvisionedTarget 校验授权、等待句柄及目标状态在同一事务中落盘。
func assertProvisionedTarget(t *testing.T, access *accesswait.Store, request PlanRequest, result TargetState) {
	t.Helper()
	if result.Phase != "waiting" || result.Version != 4 || result.ResolvedGrantID == nil || result.ResolvedWaitHandleID == nil {
		t.Fatalf("ProvisionTarget() = %#v, want waiting version 4 with grant and wait", result)
	}
	if _, err := access.GetGrant(t.Context(), *result.ResolvedGrantID); err != nil {
		t.Fatalf("GetGrant() error = %v", err)
	}
	if _, err := access.GetWait(t.Context(), accesswait.PollRequest{WaitHandleID: *result.ResolvedWaitHandleID, CallerPrincipalRef: request.CallerPrincipalRef, ScopeRef: request.ScopeRef, GrantRevision: result.GrantRevision}); err != nil {
		t.Fatalf("GetWait() error = %v", err)
	}
}

func TestProvisionTargetWaitFailureRollsBackGrantAndJournal(t *testing.T) {
	fixture := newWaitOperationFixture(t)
	identities, err := identity.NewStore(fixture.db)
	if err != nil {
		t.Fatal(err)
	}
	accessClock := fixture.now.Add(2 * time.Minute)
	access, err := accesswait.NewStore(fixture.db, identities, candidateEvents{}, func() time.Time { return accessClock })
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStoreWithAccess(fixture.db, func() time.Time { return fixture.now }, access)
	if err != nil {
		t.Fatal(err)
	}
	request := PlanRequest{OperationKey: "provision-rollback", OperationDigest: strings.Repeat("f", 64), CallerPrincipalRef: "principal", ScopeRef: "scope", Source: fixture.source, DeadlineAt: fixture.now.Add(30 * time.Second), Targets: []TargetPlan{{Key: "target-1", Target: fixture.target, Selector: accesswait.EventSelector{Version: 1, SourceID: "task/src", SourceEpoch: "inc-src", Lane: eventstore.LaneControl, TargetAgentID: "dst", EventTypes: []string{"output"}}}}}
	planned, err := store.Plan(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.ProvisionTarget(t.Context(), ProvisionTargetRequest{OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, TargetKey: "target-1", ExpectedVersion: 1, ExpectedPhase: "planned", ExpectedRecordDigest: planned.Targets[0].RecordDigest})
	if err == nil {
		t.Fatal("ProvisionTarget() unexpectedly succeeded with expired access-owner clock")
	}
	assertProvisionTargetRollback(t, fixture, store, request)
}

// assertProvisionTargetRollback 确认等待创建失败后授权、句柄和目标状态均回滚。
func assertProvisionTargetRollback(t *testing.T, fixture waitOperationFixture, store *Store, request PlanRequest) {
	t.Helper()
	var grants, waits int
	if err := fixture.db.QueryRow("SELECT COUNT(*) FROM orchestration_access_grants").Scan(&grants); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.QueryRow("SELECT COUNT(*) FROM orchestration_event_wait_handles").Scan(&waits); err != nil {
		t.Fatal(err)
	}
	if grants != 0 || waits != 0 {
		t.Fatalf("failed provisioning left grants=%d waits=%d", grants, waits)
	}
	current, err := store.Get(t.Context(), OperationLookup{Key: request.OperationKey, Digest: request.OperationDigest})
	if err != nil {
		t.Fatal(err)
	}
	if len(current.Targets) != 1 || current.Targets[0].Version != 1 || current.Targets[0].Phase != "planned" {
		t.Fatalf("failed provisioning changed target = %#v", current.Targets)
	}
}

func TestProvisionTargetRejectsStaleReplayWithUnknownPredecessor(t *testing.T) {
	fixture := newWaitOperationFixture(t)
	identities, err := identity.NewStore(fixture.db)
	if err != nil {
		t.Fatal(err)
	}
	access, err := accesswait.NewStore(fixture.db, identities, candidateEvents{}, func() time.Time { return fixture.now })
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStoreWithAccess(fixture.db, func() time.Time { return fixture.now }, access)
	if err != nil {
		t.Fatal(err)
	}
	request := PlanRequest{OperationKey: "provision-replay-guard", OperationDigest: strings.Repeat("e", 64), CallerPrincipalRef: "principal", ScopeRef: "scope", Source: fixture.source, DeadlineAt: fixture.now.Add(30 * time.Second), Targets: []TargetPlan{{Key: "target-1", Target: fixture.target, Selector: accesswait.EventSelector{Version: 1, SourceID: "task/src", SourceEpoch: "inc-src", Lane: eventstore.LaneControl, TargetAgentID: "dst", EventTypes: []string{"output"}}}}}
	planned, err := store.Plan(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ProvisionTarget(t.Context(), ProvisionTargetRequest{OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, TargetKey: "target-1", ExpectedVersion: 1, ExpectedPhase: "planned", ExpectedRecordDigest: planned.Targets[0].RecordDigest}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ProvisionTarget(t.Context(), ProvisionTargetRequest{OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, TargetKey: "target-1", ExpectedVersion: 1, ExpectedPhase: "planned", ExpectedRecordDigest: strings.Repeat("f", 64)}); !errors.Is(err, ErrTargetConflict) {
		t.Fatalf("stale replay = %v, want ErrTargetConflict", err)
	}
}

func TestProvisionTargetConcurrentReplayHasOneDurableWinner(t *testing.T) {
	fixture := newWaitOperationFixture(t)
	fixture.db.SetMaxOpenConns(16)
	identities, err := identity.NewStore(fixture.db)
	if err != nil {
		t.Fatal(err)
	}
	access, err := accesswait.NewStore(fixture.db, identities, candidateEvents{}, func() time.Time { return fixture.now })
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStoreWithAccess(fixture.db, func() time.Time { return fixture.now }, access)
	if err != nil {
		t.Fatal(err)
	}
	request := PlanRequest{OperationKey: "provision-race", OperationDigest: strings.Repeat("1", 64), CallerPrincipalRef: "principal", ScopeRef: "scope", Source: fixture.source, DeadlineAt: fixture.now.Add(30 * time.Second), Targets: []TargetPlan{{Key: "target-1", Target: fixture.target, Selector: accesswait.EventSelector{Version: 1, SourceID: "task/src", SourceEpoch: "inc-src", Lane: eventstore.LaneControl, TargetAgentID: "dst", EventTypes: []string{"output"}}}}}
	planned, err := store.Plan(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	candidate := ProvisionTargetRequest{OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, TargetKey: "target-1", ExpectedVersion: 1, ExpectedPhase: "planned", ExpectedRecordDigest: planned.Targets[0].RecordDigest}
	results := make([]TargetState, 16)
	errs := make([]error, 16)
	var wg sync.WaitGroup
	for index := range results {
		wg.Add(1)
		runtimesafe.SafeGo(t.Context(), nil, "waitoperation.test.concurrent-provision-target", func(context.Context) {
			defer wg.Done()
			results[index], errs[index] = store.ProvisionTarget(t.Context(), candidate)
		})
	}
	wg.Wait()
	assertConcurrentProvisionTargetResults(t, errs, results)
	assertConcurrentProvisionTargetRows(t, fixture)
}

// assertConcurrentProvisionTargetResults 校验并发调用均返回可复用的等待目标。
func assertConcurrentProvisionTargetResults(t *testing.T, errs []error, results []TargetState) {
	t.Helper()
	for index, err := range errs {
		if err != nil {
			t.Fatalf("concurrent ProvisionTarget[%d] error = %v", index, err)
		}
		if results[index].Phase != "waiting" || results[index].ResolvedGrantID == nil || results[index].ResolvedWaitHandleID == nil {
			t.Fatalf("concurrent result[%d] = %#v", index, results[index])
		}
	}
}

// assertConcurrentProvisionTargetRows 校验并发幂等调用仅写入一份授权和句柄。
func assertConcurrentProvisionTargetRows(t *testing.T, fixture waitOperationFixture) {
	t.Helper()
	var grants, waits int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM orchestration_access_grants`).Scan(&grants); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM orchestration_event_wait_handles`).Scan(&waits); err != nil {
		t.Fatal(err)
	}
	if grants != 1 || waits != 1 {
		t.Fatalf("durable rows grants=%d waits=%d, want one each", grants, waits)
	}
}

func TestProvisionTargetExpiredFreshDeniedAndExactReplayAllowed(t *testing.T) {
	fixture := newWaitOperationFixture(t)
	identities, err := identity.NewStore(fixture.db)
	if err != nil {
		t.Fatal(err)
	}
	clock := fixture.now
	access, err := accesswait.NewStore(fixture.db, identities, candidateEvents{}, func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStoreWithAccess(fixture.db, func() time.Time { return clock }, access)
	if err != nil {
		t.Fatal(err)
	}
	request := PlanRequest{OperationKey: "provision-expiry", OperationDigest: strings.Repeat("2", 64), CallerPrincipalRef: "principal", ScopeRef: "scope", Source: fixture.source, DeadlineAt: clock.Add(30 * time.Second), Targets: []TargetPlan{{Key: "target-1", Target: fixture.target, Selector: accesswait.EventSelector{Version: 1, SourceID: "task/src", SourceEpoch: "inc-src", Lane: eventstore.LaneControl, TargetAgentID: "dst", EventTypes: []string{"output"}}}}}
	planned, err := store.Plan(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	candidate := ProvisionTargetRequest{OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, TargetKey: "target-1", ExpectedVersion: 1, ExpectedPhase: "planned", ExpectedRecordDigest: planned.Targets[0].RecordDigest}
	clock = request.DeadlineAt.Add(time.Second)
	if _, err := store.ProvisionTarget(t.Context(), candidate); !errors.Is(err, ErrDeadlineExpired) {
		t.Fatalf("expired ProvisionTarget() = %v, want ErrDeadlineExpired", err)
	}
	clock = fixture.now
	result, err := store.ProvisionTarget(t.Context(), candidate)
	if err != nil || result.Phase != "waiting" {
		t.Fatalf("fresh ProvisionTarget() = %#v err=%v, want waiting", result, err)
	}
	clock = request.DeadlineAt.Add(time.Hour)
	replay, err := store.ProvisionTarget(t.Context(), candidate)
	if err != nil || replay.RecordDigest != result.RecordDigest {
		t.Fatalf("expired exact replay = %#v err=%v, want original", replay, err)
	}
}

func TestProvisionTargetReopensAndReplaysDurableResult(t *testing.T) {
	assertProvisionTargetReopen(t)
}

// assertProvisionTargetReopen 校验关闭并重启 SQLite 后可重放持久化等待结果。
func assertProvisionTargetReopen(t *testing.T) {
	t.Helper()
	fixture := newWaitOperationFixture(t)
	path := sqliteDatabasePath(t, fixture.db)
	identities, err := identity.NewStore(fixture.db)
	if err != nil {
		t.Fatal(err)
	}
	clock := fixture.now
	access, err := accesswait.NewStore(fixture.db, identities, candidateEvents{}, func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStoreWithAccess(fixture.db, func() time.Time { return clock }, access)
	if err != nil {
		t.Fatal(err)
	}
	request := PlanRequest{OperationKey: "provision-reopen", OperationDigest: strings.Repeat("3", 64), CallerPrincipalRef: "principal", ScopeRef: "scope", Source: fixture.source, DeadlineAt: clock.Add(30 * time.Second), Targets: []TargetPlan{{Key: "target-1", Target: fixture.target, Selector: accesswait.EventSelector{Version: 1, SourceID: "task/src", SourceEpoch: "inc-src", Lane: eventstore.LaneControl, TargetAgentID: "dst", EventTypes: []string{"output"}}}}}
	planned, err := store.Plan(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	candidate := ProvisionTargetRequest{OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, TargetKey: "target-1", ExpectedVersion: 1, ExpectedPhase: "planned", ExpectedRecordDigest: planned.Targets[0].RecordDigest}
	first, err := store.ProvisionTarget(t.Context(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Close(); err != nil {
		t.Fatal(err)
	}
	reopenedStore := reopenProvisionStore(t, path, clock)
	replay, err := reopenedStore.ProvisionTarget(t.Context(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	assertReopenedProvisionTarget(t, first, replay)
}

// assertReopenedProvisionTarget 校验重启后的幂等重放保持原始摘要与句柄。
func assertReopenedProvisionTarget(t *testing.T, first, replay TargetState) {
	t.Helper()
	if replay.RecordDigest != first.RecordDigest || replay.ResolvedGrantID == nil || replay.ResolvedWaitHandleID == nil {
		t.Fatalf("reopened replay = %#v, want exact durable waiting target", replay)
	}
}

// reopenProvisionStore 通过真实 SQLite 重启重新构造等待操作存储。
func reopenProvisionStore(t *testing.T, path string, clock time.Time) *Store {
	t.Helper()
	reopened, err := sqliteruntime.Open(context.Background(), sqliteruntime.OpenOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if err := sqliteruntime.RunEmbeddedMigrations(context.Background(), reopened); err != nil {
		t.Fatal(err)
	}
	reopenedIdentity, err := identity.NewStore(reopened)
	if err != nil {
		t.Fatal(err)
	}
	reopenedAccess, err := accesswait.NewStore(reopened, reopenedIdentity, candidateEvents{}, func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	reopenedStore, err := NewStoreWithAccess(reopened, func() time.Time { return clock }, reopenedAccess)
	if err != nil {
		t.Fatal(err)
	}
	return reopenedStore
}

func sqliteDatabasePath(t *testing.T, db *sql.DB) string {
	t.Helper()
	var sequence int
	var name, path string
	if err := db.QueryRow("PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	if path == "" {
		t.Fatal("SQLite database path is empty")
	}
	return path
}

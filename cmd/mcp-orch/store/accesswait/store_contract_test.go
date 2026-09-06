package accesswait

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/orchestrationcommand"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
)

// TestAccessWaitStoreContract is the aggregate contract for access grants and
// durable event waits.  Keep the matrix in one test so the guarded test
// runner can execute the complete owner contract as one unit.
func TestAccessWaitStoreContract(t *testing.T) {
	checks := []struct {
		name string
		run  func(*testing.T)
	}{
		{"field_guard", testAccessWaitFieldGuard},
		{"schema_replay_conflict", testAccessWaitSchemaReplayConflict},
		{"unauthorized_zero_side_effect", testAccessWaitUnauthorizedZeroSideEffect},
		{"fence_revoke_expiry", testAccessWaitFenceRevokeExpiry},
		{"wait_match_restart_cursor", testAccessWaitMatchRestartCursor},
		{"gap_quarantine", testAccessWaitGapQuarantine},
		{"cancel_no_directpeer", testAccessWaitCancelAndNoDirectPeerWrites},
		{"edge_omitted_id_replay", testAccessWaitOmittedIDReplay},
		{"edge_bounded_scan", testAccessWaitBoundedScanFindsLaterMatch},
		{"edge_selector_filter", testAccessWaitSelectorEventTypeFiltering},
		{"edge_past_deadline", testAccessWaitPastDeadlineTimesOut},
		{"edge_expire_grant_waits", testAccessWaitExpireGrantMarksWaitsExpired},
		{"edge_stale_target", testAccessWaitStaleHandleTargetDenied},
		{"edge_transactional_identity", testAccessWaitRejectsNonTransactionalIdentity},
		{"edge_unknown_source", testAccessWaitRejectsUnknownSource},
		{"edge_cursor_watermarks", testAccessWaitRejectsCursorWatermarkViolations},
		{"edge_cancel_race", testAccessWaitCancelRaceReturnsWinner},
		{"edge_poll_terminal_race", testAccessWaitPollRaceReplaysAnyTerminalWinner},
		{"edge_timestamp_precision", testAccessWaitTimestampPrecision},
		{"edge_owner_clock", testAccessWaitUsesOwnerClock},
		{"edge_selector_column_consistency", testAccessWaitSelectorColumnConsistency},
		{"edge_cross_source_selector", testAccessWaitRejectsCrossSourceSelector},
		{"target_source_read_cursor_zero_effect", testAccessWaitTargetSourceReadCursorZeroEffect},
		{"target_source_forged_source_target", testAccessWaitTargetSourceRejectsForgedSourceTarget},
		{"target_source_revoke_rollover", testAccessWaitTargetSourceRevokeAndRollover},
		{"same_source_compatibility", testAccessWaitSameSourceCompatibility},
	}
	for _, check := range checks {
		t.Run(check.name, check.run)
	}
}

func TestAccessWaitAuthorizeTxUsesCallerTransactionAndRejectsBareDB(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	grant := validGrantRequest("grant-authorize-tx", "grant/authorize-tx", strings.Repeat("a", 64))
	mustCreateGrant(t, fixture.store, grant)
	request := accessRequestFor(grant, "read")
	tx, err := fixture.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.AuthorizeTx(t.Context(), tx, request); err != nil {
		_ = tx.Rollback()
		t.Fatalf("AuthorizeTx() error = %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.AuthorizeTx(t.Context(), fixture.db, request); err == nil {
		t.Fatal("AuthorizeTx() unexpectedly accepted bare database")
	}
	var _ storedb.DBTX = fixture.db
}

func testAccessWaitSchemaReplayConflict(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	db, store := fixture.db, fixture.store
	for _, table := range []string{"orchestration_access_grants", "orchestration_event_wait_handles"} {
		var got string
		if err := db.QueryRowContext(t.Context(), "SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&got); err != nil {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
	grant := validGrantRequest("grant-replay", "grant/key", strings.Repeat("a", 64))
	first, err := store.CreateGrant(t.Context(), grant)
	if err != nil {
		t.Fatalf("CreateGrant(first): %v", err)
	}
	replay, err := store.CreateGrant(t.Context(), grant)
	if err != nil || !reflect.DeepEqual(first, replay) {
		t.Fatalf("CreateGrant(replay)=%#v err=%v, want original %#v", replay, err, first)
	}
	conflict := grant
	conflict.RequestDigest = strings.Repeat("9", 64)
	if _, err := store.CreateGrant(t.Context(), conflict); !errors.Is(err, ErrAccessConflict) {
		t.Fatalf("CreateGrant(conflict)=%v, want ErrAccessConflict", err)
	}
	var rows int
	if err := db.QueryRow("SELECT count(*) FROM orchestration_access_grants").Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("grant rows=%d err=%v, want one replay winner", rows, err)
	}
}

func testAccessWaitUnauthorizedZeroSideEffect(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	db, store := fixture.db, fixture.store
	grant := validGrantRequest("grant-unauthorized", "grant/unauth", strings.Repeat("b", 64))
	if _, err := store.CreateGrant(t.Context(), grant); err != nil {
		t.Fatalf("CreateGrant: %v", err)
	}
	var before int
	if err := db.QueryRow("SELECT count(*) FROM orchestration_access_grants").Scan(&before); err != nil {
		t.Fatal(err)
	}
	authRequest := accessRequestFor(grant, "read")
	authRequest.CallerPrincipalRef = "intruder"
	if _, err := store.Authorize(t.Context(), authRequest); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("Authorize(unauthorized)=%v, want ErrAccessDenied", err)
	}
	// Admission must deny before opening a write transaction. This fixture
	// intentionally performs no SQL write and asserts the zero-side-effect
	// invariant used by the owner implementation.
	var after int
	if err := db.QueryRow("SELECT count(*) FROM orchestration_access_grants").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("unauthorized admission changed grant rows: before=%d after=%d", before, after)
	}
}

func testAccessWaitFenceRevokeExpiry(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	grantStore := fixture.store
	grant := validGrantRequest("grant-fence", "grant/fence", strings.Repeat("c", 64))
	if _, err := grantStore.CreateGrant(t.Context(), grant); err != nil {
		t.Fatalf("CreateGrant: %v", err)
	}
	if _, err := grantStore.Authorize(t.Context(), accessRequestFor(grant, "read")); err != nil {
		t.Fatalf("Authorize(active): %v", err)
	}
	clockStore, err := NewStore(fixture.db, fixture.identity, fixture.events, func() time.Time { return grant.ExpiresAt.Add(time.Second) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := clockStore.Authorize(t.Context(), accessRequestFor(grant, "read")); !errors.Is(err, ErrAccessExpired) {
		t.Fatalf("Authorize(expired)=%v, want ErrAccessExpired", err)
	}
	revoked, err := grantStore.RevokeGrant(t.Context(), RevokeGrantRequest{GrantID: grant.GrantID, GrantRevision: 1, CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef, ReasonRef: "reason", RequestDigest: grant.RequestDigest, RevokedAt: grant.IssuedAt.Add(time.Minute)})
	if err != nil || revoked.Status != "revoked" {
		t.Fatalf("RevokeGrant=%#v err=%v, want revoked", revoked, err)
	}
	if _, err := grantStore.Authorize(t.Context(), accessRequestFor(grant, "read")); !errors.Is(err, ErrAccessRevoked) {
		t.Fatalf("Authorize(revoked)=%v, want ErrAccessRevoked", err)
	}
}

func testAccessWaitMatchRestartCursor(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	grantRequest := validGrantRequest("grant-wait", "grant/wait", strings.Repeat("7", 64))
	mustCreateGrant(t, fixture.store, grantRequest)
	selector := waitSelector()
	wait := waitRequest(grantRequest, "wait-1", selector, "wait", strings.Repeat("e", 64), strings.Repeat("f", 64))
	handle := mustCreateWait(t, fixture.store, wait)
	fixture.events.events = append(fixture.events.events, matchingWaitEvent(selector))
	result := mustPollWait(t, fixture.store, handle)
	if result.Status != "satisfied" || result.Event == nil || result.NextCursor != "task/source/inc-source/output/1" {
		t.Fatalf("PollWait(match)=%#v, want satisfied event/cursor", result)
	}
	// A newly constructed owner sees the same terminal handle and canonical
	// event; no in-memory waiter state is required for restart recovery.
	restarted := mustAccessWaitStore(t, fixture)
	recovered := mustPollWait(t, restarted, handle)
	if recovered.Status != "satisfied" || recovered.Event == nil {
		t.Fatalf("PollWait(restart)=%#v, want durable satisfied result", recovered)
	}
	wait.AfterSequence = 1
	if err := wait.Validate(); !errors.Is(err, ErrInvalidWait) {
		t.Fatalf("cursor without canonical after_cursor error=%v, want ErrInvalidWait", err)
	}
	wait.AfterCursor = "task/source/inc-source/output/1"
	if err := wait.Validate(); err != nil {
		t.Fatalf("restart wait with durable cursor: %v", err)
	}
}

func testAccessWaitGapQuarantine(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	grant := validGrantRequest("grant-gap", "grant/gap", strings.Repeat("a", 64))
	if _, err := fixture.store.CreateGrant(t.Context(), grant); err != nil {
		t.Fatalf("CreateGrant: %v", err)
	}
	wait := WaitRequest{WaitHandleID: "wait-gap", GrantID: grant.GrantID, GrantRevision: 1, CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef, Selector: EventSelector{Version: 1, SourceID: "task/source", SourceEpoch: "inc-source", Lane: eventstore.LaneControl, TargetAgentID: "target", EventTypes: []string{"output"}}, DeadlineAt: grant.IssuedAt.Add(30 * time.Second), IdempotencyNamespace: "wait-gap", IdempotencyKeyDigest: strings.Repeat("b", 64), RequestDigest: strings.Repeat("c", 64)}
	handle, err := fixture.store.CreateWait(t.Context(), wait)
	if err != nil {
		t.Fatalf("CreateWait gap: %v", err)
	}
	fixture.events.err = eventstore.ErrEventGap
	if _, err := fixture.store.PollWait(t.Context(), PollRequest{WaitHandleID: handle.WaitHandleID, CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef, GrantRevision: 1}); !errors.Is(err, ErrWaitGap) {
		t.Fatalf("PollWait(gap)=%v, want ErrWaitGap", err)
	}
	fixture.events.err = eventstore.ErrEventQuarantined
	if _, err := fixture.store.PollWait(t.Context(), PollRequest{WaitHandleID: handle.WaitHandleID, CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef, GrantRevision: 1}); !errors.Is(err, ErrWaitQuarantined) {
		t.Fatalf("PollWait(quarantine)=%v, want ErrWaitQuarantined", err)
	}
	for _, err := range []error{ErrWaitGap, ErrWaitQuarantined} {
		if !errors.Is(err, err) {
			t.Fatalf("wait error %v is not stable", err)
		}
	}
	selector := EventSelector{Version: 1, SourceID: "task/source", SourceEpoch: "inc-source", Lane: eventstore.LaneControl, TargetAgentID: "target", EventTypes: []string{"control"}}
	if err := selector.Validate(); err != nil {
		t.Fatalf("valid selector: %v", err)
	}
	selector.EventTypes = []string{"control", "control"}
	if err := selector.Validate(); !errors.Is(err, ErrInvalidWait) {
		t.Fatalf("duplicate selector event type error=%v, want ErrInvalidWait", err)
	}
}

func testAccessWaitCancelAndNoDirectPeerWrites(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	db, store := fixture.db, fixture.store
	grant := validGrantRequest("grant-cancel", "grant/cancel", strings.Repeat("d", 64))
	mustCreateGrant(t, store, grant)
	wait := mustCreateWait(t, store, waitRequest(grant, "wait-cancel", cancelSelector(), "wait-cancel", strings.Repeat("e", 64), strings.Repeat("f", 64)))
	cancelled := mustCancelWait(t, store, wait, grant, time.Second)
	if cancelled.Status != "cancelled" {
		t.Fatalf("CancelWait=%#v, want cancelled", cancelled)
	}
	replay := mustCancelWait(t, store, wait, grant, 2*time.Second)
	if replay.Status != "cancelled" {
		t.Fatalf("CancelWait(replay)=%#v, want terminal cancellation", replay)
	}
	var before int
	for _, table := range []string{"orchestration_direct_peer_messages", "orchestration_direct_peer_events", "orchestration_direct_peer_event_outbox"} {
		if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&before); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		var after int
		if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&after); err != nil {
			t.Fatalf("recount %s: %v", table, err)
		}
		if after != before {
			t.Fatalf("access/wait path changed direct-peer table %s", table)
		}
	}
	cancel := CancelRequest{WaitHandleID: "wait-1", CallerPrincipalRef: "principal", ScopeRef: "scope", GrantRevision: 1, ReasonRef: "cancelled", CancelledAt: time.Now().UTC()}
	if err := cancel.Validate(); err != nil {
		t.Fatalf("valid cancel request: %v", err)
	}
}

func mustCreateGrant(t *testing.T, store *Store, request AccessGrantRequest) AccessGrant {
	t.Helper()
	grant, err := store.CreateGrant(t.Context(), request)
	if err != nil {
		t.Fatalf("CreateGrant: %v", err)
	}
	return grant
}

func waitSelector() EventSelector {
	return EventSelector{Version: 1, SourceID: "task/source", SourceEpoch: "inc-source", Lane: eventstore.LaneOutput, TargetAgentID: "target", EventTypes: []string{"output"}}
}
func cancelSelector() EventSelector {
	return EventSelector{Version: 1, SourceID: "task/source", SourceEpoch: "inc-source", Lane: eventstore.LaneControl, TargetAgentID: "target", EventTypes: []string{"output"}}
}
func waitRequest(grant AccessGrantRequest, id string, selector EventSelector, namespace, key, digest string) WaitRequest {
	return WaitRequest{WaitHandleID: id, GrantID: grant.GrantID, GrantRevision: 1, CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef, Selector: selector, DeadlineAt: grant.IssuedAt.Add(30 * time.Second), IdempotencyNamespace: namespace, IdempotencyKeyDigest: key, RequestDigest: digest}
}
func mustCreateWait(t *testing.T, store *Store, request WaitRequest) WaitHandle {
	t.Helper()
	handle, err := store.CreateWait(t.Context(), request)
	if err != nil {
		t.Fatalf("CreateWait: %v", err)
	}
	return handle
}
func mustPollWait(t *testing.T, store *Store, handle WaitHandle) WaitResult {
	t.Helper()
	result, err := store.PollWait(t.Context(), PollRequest{WaitHandleID: handle.WaitHandleID, CallerPrincipalRef: handle.CallerPrincipalRef, ScopeRef: handle.ScopeRef, GrantRevision: handle.GrantRevision})
	if err != nil {
		t.Fatalf("PollWait: %v", err)
	}
	return result
}
func mustAccessWaitStore(t *testing.T, fixture accessWaitFixture) *Store {
	t.Helper()
	store, err := NewStore(fixture.db, fixture.identity, fixture.events)
	if err != nil {
		t.Fatal(err)
	}
	return store
}
func mustCancelWait(t *testing.T, store *Store, handle WaitHandle, grant AccessGrantRequest, offset time.Duration) WaitResult {
	t.Helper()
	result, err := store.CancelWait(t.Context(), CancelRequest{WaitHandleID: handle.WaitHandleID, CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef, GrantRevision: 1, ReasonRef: "cancelled", CancelledAt: grant.IssuedAt.Add(offset)})
	if err != nil {
		t.Fatalf("CancelWait: %v", err)
	}
	return result
}
func matchingWaitEvent(selector EventSelector) eventstore.AgentEvent {
	incarnation := "inc-target"
	cursor, _ := eventstore.CanonicalCursor(selector.SourceID, selector.SourceEpoch, selector.Lane, 1)
	return eventstore.AgentEvent{IngressFrame: eventstore.IngressFrame{SourceID: selector.SourceID, SourceEpoch: selector.SourceEpoch, Lane: selector.Lane, SourceSequence: 1, CanonicalCursor: cursor, EnvelopeHash: strings.Repeat("a", 64), Envelope: eventstore.EventEnvelope{EventID: "event-wait-1", EventType: "output", SchemaVersion: 1, RootAgentID: "source", SubjectAgentID: "target", ReservationID: "res-target", IncarnationID: &incarnation, ExternalEpoch: "epoch-target", IncarnationGeneration: 1, IdentityDigest: strings.Repeat("5", 64), BindingVersion: 1, BindingDigest: strings.Repeat("6", 64), SourceFenceRef: "source/target", Provenance: "fixture", TrustClass: "trusted", ContentRole: "none", ObservedAt: "2026-09-01T00:00:00Z", WriterEpoch: "writer"}}, Status: "materialized"}
}

func validGrantRequest(id, namespace, key string) AccessGrantRequest {
	now := time.Now().UTC().Truncate(time.Microsecond)
	fence := FenceSnapshot{AgentID: "source", ReservationID: "res-source", ReservationGeneration: 1, IncarnationID: "inc-source", IncarnationGeneration: 1, BindingVersion: 1, BindingDigest: strings.Repeat("6", 64), SourceFenceRef: "source/source"}
	target := fence
	target.AgentID, target.ReservationID, target.IncarnationID, target.SourceFenceRef = "target", "res-target", "inc-target", "source/target"
	return AccessGrantRequest{GrantID: id, CallerPrincipalRef: "principal", ScopeRef: "scope", Source: fence, Target: target, CanRead: true, IdempotencyNamespace: namespace, IdempotencyKeyDigest: key, RequestDigest: strings.Repeat("2", 64), IssuedAt: now, ExpiresAt: now.Add(time.Hour)}
}

func accessRequestFor(grant AccessGrantRequest, capability string) AccessRequest {
	now := time.Now().UTC()
	return AccessRequest{RequestID: "request-" + grant.GrantID, GrantID: grant.GrantID, CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef, Capability: capability, Source: grant.Source, Target: grant.Target, RequestDigest: strings.Repeat("8", 64), RequestedAt: now}
}

type accessWaitFixture struct {
	db       *sql.DB
	store    *Store
	identity *identity.Store
	events   *fixtureEvents
}

type fixtureEvents struct {
	events      []eventstore.AgentEvent
	err         error
	sourceState eventstore.SourceState
	listCalls   int
	txReadHook  func()
}

func (f *fixtureEvents) GetSourceState(_ context.Context, _, _ string, _ eventstore.Lane) (eventstore.SourceState, error) {
	if f.err != nil {
		return eventstore.SourceState{}, f.err
	}
	if f.sourceState.Status != "" {
		return f.sourceState, nil
	}
	return eventstore.SourceState{NextSequence: 1, Status: "active"}, nil
}

func (f *fixtureEvents) ListEvents(_ context.Context, source, epoch string, lane eventstore.Lane, after, limit int64) ([]eventstore.AgentEvent, error) {
	f.listCalls++
	if f.err != nil {
		return nil, f.err
	}
	result := make([]eventstore.AgentEvent, 0, len(f.events))
	for _, event := range f.events {
		if event.SourceID == source && event.SourceEpoch == epoch && event.Lane == lane && event.SourceSequence > after {
			result = append(result, event)
			if int64(len(result)) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (f *fixtureEvents) GetEventTx(ctx context.Context, dbtx storedb.DBTX, eventID string) (eventstore.AgentEvent, error) {
	if err := sqlctx.RequireTransactionDBTX(dbtx); err != nil {
		return eventstore.AgentEvent{}, err
	}
	for _, item := range f.events {
		if item.Envelope.EventID == eventID {
			return item, nil
		}
	}
	return eventstore.AgentEvent{}, eventstore.ErrEventNotFound
}

func (f *fixtureEvents) ListEventsTx(ctx context.Context, dbtx storedb.DBTX, source, epoch string, lane eventstore.Lane, after, limit int64) ([]eventstore.AgentEvent, error) {
	if err := sqlctx.RequireTransactionDBTX(dbtx); err != nil {
		return nil, err
	}
	items, err := f.ListEvents(ctx, source, epoch, lane, after, limit)
	if err == nil && f.txReadHook != nil {
		f.txReadHook()
	}
	return items, err
}

func (f *fixtureEvents) GetSourceStateTx(ctx context.Context, dbtx storedb.DBTX, source, epoch string, lane eventstore.Lane) (eventstore.SourceState, error) {
	if err := sqlctx.RequireTransactionDBTX(dbtx); err != nil {
		return eventstore.SourceState{}, err
	}
	return f.GetSourceState(ctx, source, epoch, lane)
}

func newAccessWaitFixture(t *testing.T) accessWaitFixture {
	t.Helper()
	db := openAccessWaitTestDB(t)
	ids, err := identity.NewStore(db)
	if err != nil {
		t.Fatalf("identity store: %v", err)
	}
	chains := make([]accessFenceFixture, 0, 2)
	for _, agentID := range []string{"source", "target"} {
		chains = append(chains, createAccessIdentityChain(t, db, ids, agentID))
	}
	events := &fixtureEvents{}
	store, err := NewStore(db, ids, events)
	if err != nil {
		t.Fatalf("access wait store: %v", err)
	}
	return accessWaitFixture{db: db, store: store, identity: ids, events: events}
}

type accessFenceFixture struct {
	reservation identity.Reservation
	incarnation identity.Incarnation
	binding     identity.CurrentBinding
}

func createAccessIdentityChain(t *testing.T, db *sql.DB, ids *identity.Store, agentID string) accessFenceFixture {
	t.Helper()
	commandID := "accesswait-command-" + agentID
	commands, err := orchestrationcommand.NewStore(db)
	if err != nil {
		t.Fatalf("command store: %v", err)
	}
	if _, err := commands.Reserve(t.Context(), orchestrationcommand.Intent{CommandID: commandID, CommandKind: "launch_agent", CallerPrincipalRef: "principal/" + agentID, ScopeRef: "scope/" + agentID, IdempotencyNamespace: "accesswait/command/" + agentID, IdempotencyKeyDigest: strings.Repeat("1", 64), RequestDigest: strings.Repeat("2", 64), DigestKeyID: "test-key", RequestContentRef: "ref/" + agentID, SourceFenceRef: "source/" + agentID, TargetFenceRef: "target/" + agentID}); err != nil {
		t.Fatalf("reserve command: %v", err)
	}
	reservation, err := ids.Reserve(t.Context(), identity.ReservationRequest{ReservationID: "res-" + agentID, CommandID: commandID, AgentID: agentID, IdentityKind: "native_subagent", SourceKind: "first_launch", CallerPrincipalRef: "principal/" + agentID, ScopeRef: "scope/" + agentID, ParentAgentID: "root", IdempotencyNamespace: "accesswait/reservation/" + agentID, IdempotencyKeyDigest: strings.Repeat("3", 64), RequestDigest: strings.Repeat("4", 64), SourceFenceRef: "source/" + agentID, TargetFenceRef: "target/" + agentID, ReservationGeneration: 1})
	if err != nil {
		t.Fatalf("reserve identity %s: %v", agentID, err)
	}
	incarnation, err := ids.CreateIncarnation(t.Context(), identity.IncarnationRequest{IncarnationID: "inc-" + agentID, ReservationID: reservation.ReservationID, AgentID: agentID, IdentityKind: reservation.IdentityKind, ExternalIdentityRef: "external-" + agentID, ExternalEpoch: "epoch-" + agentID, SourceFenceRef: reservation.SourceFenceRef, IdentityDigest: strings.Repeat("5", 64), IncarnationGeneration: 1})
	if err != nil {
		t.Fatalf("create incarnation %s: %v", agentID, err)
	}
	prep, err := ids.PrepareBinding(t.Context(), identity.BindingPreparationRequest{PreparationID: "accesswait-prep-" + agentID, ReservationID: reservation.ReservationID, IncarnationID: incarnation.IncarnationID, AgentID: agentID, IdentityKind: reservation.IdentityKind, PreparationGeneration: 1, ExpectedBindingVersion: 0, ExpectedIncarnationGeneration: 1, BindingDigest: strings.Repeat("6", 64), SourceFenceRef: reservation.SourceFenceRef})
	if err != nil {
		t.Fatalf("prepare binding %s: %v", agentID, err)
	}
	binding, err := ids.FinalizeBinding(t.Context(), identity.FinalizeBindingRequest{PreparationID: prep.PreparationID, AgentID: agentID, ExpectedPreparationGeneration: 1, ExpectedBindingVersion: 0, IncarnationID: incarnation.IncarnationID, ExternalIdentityRef: incarnation.ExternalIdentityRef, IdentityKind: reservation.IdentityKind, SourceFenceRef: reservation.SourceFenceRef, ActiveProcessReceiptRef: "receipt-" + agentID, BindingDigest: prep.BindingDigest})
	if err != nil {
		t.Fatalf("finalize binding %s: %v", agentID, err)
	}
	return accessFenceFixture{reservation: reservation, incarnation: incarnation, binding: binding}
}

func openAccessWaitTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sqliteruntime.Open(t.Context(), sqliteruntime.OpenOptions{Path: filepath.Join(t.TempDir(), "accesswait.sqlite")})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqliteruntime.RunEmbeddedMigrations(context.Background(), db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return db
}

func seedAccessWaitIdentity(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, suffix := range []string{"source", "target"} {
		if _, err := db.Exec(`INSERT INTO orchestration_identity_reservations (reservation_id,agent_id,identity_kind,source_kind,caller_principal_ref,scope_ref,parent_agent_id,idempotency_namespace,idempotency_key_digest,request_digest,source_fence_ref,target_fence_ref,expected_binding_version,reservation_generation,state,schema_version) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, "res-"+suffix, suffix, "a2a_remote_agent", "a2a_observation", "principal", "scope", "", "seed/"+suffix, strings.Repeat("a", 64), strings.Repeat("b", 64), "fence-"+suffix, "target-fence-"+suffix, 0, 1, "reserved", 1); err != nil {
			t.Fatalf("seed reservation %s: %v", suffix, err)
		}
		if _, err := db.Exec(`INSERT INTO orchestration_execution_incarnations (incarnation_id,reservation_id,agent_id,identity_kind,external_identity_ref,external_epoch,source_fence_ref,identity_digest,incarnation_generation,schema_version) VALUES (?,?,?,?,?,?,?,?,?,?)`, "inc-"+suffix, "res-"+suffix, suffix, "a2a_remote_agent", "external-"+suffix, "epoch-"+suffix, "fence-"+suffix, strings.Repeat("c", 64), 1, 1); err != nil {
			t.Fatalf("seed incarnation %s: %v", suffix, err)
		}
	}
}

func insertGrantRow(t *testing.T, db *sql.DB, g AccessGrantRequest) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO orchestration_access_grants (grant_id,caller_principal_ref,scope_ref,source_agent_id,target_agent_id,source_reservation_id,source_reservation_generation,source_incarnation_id,source_incarnation_generation,source_binding_version,source_binding_digest,source_fence_ref,target_reservation_id,target_reservation_generation,target_incarnation_id,target_incarnation_generation,target_binding_version,target_binding_digest,target_fence_ref,can_read,can_send,idempotency_namespace,idempotency_key_digest,request_digest,revision,status,issued_at,expires_at,schema_version) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, g.GrantID, g.CallerPrincipalRef, g.ScopeRef, g.Source.AgentID, g.Target.AgentID, g.Source.ReservationID, g.Source.ReservationGeneration, g.Source.IncarnationID, g.Source.IncarnationGeneration, g.Source.BindingVersion, g.Source.BindingDigest, g.Source.SourceFenceRef, g.Target.ReservationID, g.Target.ReservationGeneration, g.Target.IncarnationID, g.Target.IncarnationGeneration, g.Target.BindingVersion, g.Target.BindingDigest, g.Target.SourceFenceRef, g.CanRead, g.CanSend, g.IdempotencyNamespace, g.IdempotencyKeyDigest, g.RequestDigest, 1, "active", g.IssuedAt.Format(time.RFC3339Nano), g.ExpiresAt.Format(time.RFC3339Nano), 1)
	if err != nil {
		t.Fatalf("insert grant: %v", err)
	}
}

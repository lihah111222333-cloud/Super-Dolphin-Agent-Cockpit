package accesswait

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
)

// TestAccessWaitSQLiteCloseReopenTerminalReplay proves that a satisfied wait
// can be replayed from durable AccessWait and EventStore rows after restart.
func TestAccessWaitSQLiteCloseReopenTerminalReplay(t *testing.T) {
	fixture := newSQLiteRestartFixture(t)
	grant := sqliteRestartGrant(t, fixture, "terminal")
	selector := sqliteRestartTargetSelector(fixture.target)
	now := fixture.now
	frame := sqliteRestartEventFrame(t, fixture.target, "terminal", 1, "output")
	appendRestartEvent(t, fixture, frame, "canonical")
	handle := createRestartWait(t, fixture, grant, selector, now, "wait-reopen-terminal", "terminal", 0, "")
	assertSatisfiedWait(t, fixture.access, handle, "before close")

	fixture.closeDB(t)
	reopened := fixture.reopen(t)
	replayed := assertSatisfiedWait(t, reopened.access, handle, "after reopen")
	assertTerminalReplay(t, reopened, frame, replayed)
}

// TestAccessWaitSQLiteCloseReopenPendingCursorAndFloor proves that a pending
// wait keeps its cursor across restart and that EventStore's dedup floor does
// not reset when the database is reopened.
func TestAccessWaitSQLiteCloseReopenPendingCursorAndFloor(t *testing.T) {
	fixture := newSQLiteRestartFixture(t)
	grant := sqliteRestartGrant(t, fixture, "pending")
	selector := sqliteRestartTargetSelector(fixture.target)
	now := fixture.now
	firstFrame := sqliteRestartEventFrame(t, fixture.target, "pending", 1, "control")
	appendRestartEvent(t, fixture, firstFrame, "non-matching canonical")
	handle := createRestartWait(t, fixture, grant, selector, now, "wait-reopen-pending", "pending", 1, firstFrame.CanonicalCursor)
	assertPendingWait(t, fixture.access, handle, "before close")

	fixture.closeDB(t)
	reopened := fixture.reopen(t)
	recovered := assertPendingWait(t, reopened.access, handle, "after reopen")
	if recovered.Handle.AfterSequence != 1 || recovered.Handle.AfterCursor != firstFrame.CanonicalCursor {
		t.Fatalf("pending cursor after reopen = %#v, want sequence 1 cursor %q", recovered.Handle, firstFrame.CanonicalCursor)
	}

	secondFrame := sqliteRestartEventFrame(t, fixture.target, "pending", 2, "output")
	appendRestartEvent(t, reopened, secondFrame, "matching canonical")
	satisfied := assertSatisfiedWait(t, reopened.access, handle, "after matching append")
	if satisfied.NextCursor != secondFrame.CanonicalCursor {
		t.Fatalf("PollWait(after matching append) cursor = %q, want %q", satisfied.NextCursor, secondFrame.CanonicalCursor)
	}

	recordRestartTombstone(t, reopened.events, firstFrame)
	fixture.closeDB(t)
	reopened = fixture.reopen(t)
	assertRestartFloor(t, reopened.events, firstFrame)
}

func appendRestartEvent(t *testing.T, fixture *sqliteRestartFixture, frame eventstore.IngressFrame, label string) {
	t.Helper()
	appended, err := fixture.events.Append(t.Context(), frame)
	if err != nil || appended.Event == nil {
		t.Fatalf("Append(%s) = %#v err=%v", label, appended, err)
	}
	if label == "canonical" && appended.Receipt.Status != "materialized" {
		t.Fatalf("Append(%s) receipt = %#v, want materialized", label, appended.Receipt)
	}
}

func createRestartWait(t *testing.T, fixture *sqliteRestartFixture, grant AccessGrant, selector EventSelector, now time.Time, waitID, suffix string, afterSequence int64, afterCursor string) WaitHandle {
	t.Helper()
	handle, err := fixture.access.CreateWait(t.Context(), WaitRequest{
		WaitHandleID: waitID, GrantID: grant.GrantID, GrantRevision: grant.Revision,
		CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef, Selector: selector,
		AfterSequence: afterSequence, AfterCursor: afterCursor, DeadlineAt: now.Add(30 * time.Second),
		IdempotencyNamespace: "reopen/" + suffix, IdempotencyKeyDigest: sqliteRestartDigest(suffix + "-key"),
		RequestDigest: sqliteRestartDigest(suffix + "-request"),
	})
	if err != nil {
		t.Fatalf("CreateWait: %v", err)
	}
	return handle
}

func assertSatisfiedWait(t *testing.T, access *Store, handle WaitHandle, phase string) WaitResult {
	t.Helper()
	result, err := access.PollWait(t.Context(), sqliteRestartPoll(handle))
	if err != nil || result.Status != "satisfied" || result.Event == nil {
		t.Fatalf("PollWait(%s) = %#v err=%v, want satisfied event", phase, result, err)
	}
	return result
}

func assertPendingWait(t *testing.T, access *Store, handle WaitHandle, phase string) WaitResult {
	t.Helper()
	result, err := access.PollWait(t.Context(), sqliteRestartPoll(handle))
	if err != nil || result.Status != "pending" || result.Event != nil {
		t.Fatalf("PollWait(%s) = %#v err=%v, want pending", phase, result, err)
	}
	return result
}

func assertTerminalReplay(t *testing.T, fixture *sqliteRestartFixture, frame eventstore.IngressFrame, replayed WaitResult) {
	t.Helper()
	if replayed.Event.Envelope.EventID != frame.Envelope.EventID || replayed.NextCursor != frame.CanonicalCursor {
		t.Fatalf("replayed event = %#v, want event %q cursor %q", replayed.Event, frame.Envelope.EventID, frame.CanonicalCursor)
	}
	if replayed.Handle.Status != "satisfied" || replayed.Handle.MatchedEventID != frame.Envelope.EventID || replayed.Handle.MatchedCanonicalCursor != frame.CanonicalCursor {
		t.Fatalf("replayed handle = %#v, want durable terminal metadata", replayed.Handle)
	}
	assertRestartSourceState(t, fixture.events, frame)
}

func assertRestartSourceState(t *testing.T, events *eventstore.Store, frame eventstore.IngressFrame) {
	t.Helper()
	state, err := events.GetSourceState(t.Context(), frame.SourceID, frame.SourceEpoch, frame.Lane)
	if err != nil {
		t.Fatal(err)
	}
	if state.NextSequence != 2 || state.MaterializedSequence != 1 || state.DedupFloor != 0 || state.Status != "active" {
		t.Fatalf("source state after reopen = %#v, want next=2 materialized=1 floor=0 active", state)
	}
	if _, err := events.GetReceipt(t.Context(), frame.SourceID, frame.SourceEpoch, frame.Lane, frame.SourceSequence); err != nil {
		t.Fatalf("GetReceipt(after reopen): %v", err)
	}
}

func recordRestartTombstone(t *testing.T, events *eventstore.Store, frame eventstore.IngressFrame) {
	t.Helper()
	if _, err := events.RecordTombstone(t.Context(), eventstore.TombstoneRequest{
		SourceID: frame.SourceID, SourceEpoch: frame.SourceEpoch, Lane: frame.Lane,
		SourceSequence: 1, EnvelopeHash: frame.EnvelopeHash, EventID: frame.Envelope.EventID,
		CanonicalCursor: frame.CanonicalCursor, ExpectedFloor: 0, Status: "expired",
	}); err != nil {
		t.Fatalf("RecordTombstone(seq1): %v", err)
	}
}

func assertRestartFloor(t *testing.T, events *eventstore.Store, frame eventstore.IngressFrame) {
	t.Helper()
	state, err := events.GetSourceState(t.Context(), frame.SourceID, frame.SourceEpoch, frame.Lane)
	if err != nil {
		t.Fatal(err)
	}
	if state.NextSequence != 3 || state.MaterializedSequence != 2 || state.DedupFloor != 1 {
		t.Fatalf("source state after floor reopen = %#v, want next=3 materialized=2 floor=1", state)
	}
	if _, err := events.Append(t.Context(), frame); !errors.Is(err, eventstore.ErrEventReplayExpired) {
		t.Fatalf("Append(replay below durable floor) = %v, want ErrEventReplayExpired", err)
	}
}

type sqliteRestartFixture struct {
	db     *sql.DB
	path   string
	access *Store
	events *eventstore.Store
	source accessFenceFixture
	target accessFenceFixture
	now    time.Time
}

func newSQLiteRestartFixture(t *testing.T) *sqliteRestartFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "accesswait-reopen.sqlite")
	db := sqliteRestartOpen(t, path)
	ids, err := identity.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	source := createAccessIdentityChain(t, db, ids, "source")
	target := createAccessIdentityChain(t, db, ids, "target")
	verifier, err := eventstore.NewIdentityFenceVerifier(ids)
	if err != nil {
		t.Fatal(err)
	}
	events, err := eventstore.NewStore(db, verifier)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	access, err := NewStore(db, ids, events, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return &sqliteRestartFixture{db: db, path: path, access: access, events: events, source: source, target: target, now: now}
}

func (f *sqliteRestartFixture) closeDB(t *testing.T) {
	t.Helper()
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
}

func (f *sqliteRestartFixture) reopen(t *testing.T) *sqliteRestartFixture {
	t.Helper()
	db := sqliteRestartOpen(t, f.path)
	ids, err := identity.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := eventstore.NewIdentityFenceVerifier(ids)
	if err != nil {
		t.Fatal(err)
	}
	events, err := eventstore.NewStore(db, verifier)
	if err != nil {
		t.Fatal(err)
	}
	access, err := NewStore(db, ids, events, func() time.Time { return f.now })
	if err != nil {
		t.Fatal(err)
	}
	return &sqliteRestartFixture{db: db, path: f.path, access: access, events: events, source: f.source, target: f.target, now: f.now}
}

func sqliteRestartOpen(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sqliteruntime.Open(t.Context(), sqliteruntime.OpenOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := sqliteruntime.RunEmbeddedMigrations(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func sqliteRestartGrant(t *testing.T, fixture *sqliteRestartFixture, suffix string) AccessGrant {
	t.Helper()
	fence := func(item accessFenceFixture) FenceSnapshot {
		return FenceSnapshot{AgentID: item.binding.AgentID, ReservationID: item.binding.ReservationID, ReservationGeneration: item.binding.ReservationGeneration, IncarnationID: item.binding.IncarnationID, IncarnationGeneration: item.binding.IncarnationGeneration, BindingVersion: item.binding.BindingVersion, BindingDigest: item.binding.BindingDigest, SourceFenceRef: item.binding.SourceFenceRef}
	}
	request := AccessGrantRequest{
		GrantID: "grant-reopen-" + suffix, CallerPrincipalRef: "principal/reopen", ScopeRef: "scope/reopen",
		Source: fence(fixture.source), Target: fence(fixture.target), CanRead: true,
		IdempotencyNamespace: "reopen/grant/" + suffix, IdempotencyKeyDigest: sqliteRestartDigest("grant-key-" + suffix), RequestDigest: sqliteRestartDigest("grant-request-" + suffix),
		IssuedAt: fixture.now.Add(-time.Second), ExpiresAt: fixture.now.Add(time.Hour),
	}
	grant, err := fixture.access.CreateGrant(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

func sqliteRestartTargetSelector(target accessFenceFixture) EventSelector {
	return EventSelector{Version: 1, SourceID: "task/" + target.binding.AgentID, SourceEpoch: target.binding.IncarnationID, Lane: eventstore.LaneOutput, TargetAgentID: target.binding.AgentID, EventTypes: []string{"output"}}
}

func sqliteRestartPoll(handle WaitHandle) PollRequest {
	return PollRequest{WaitHandleID: handle.WaitHandleID, CallerPrincipalRef: handle.CallerPrincipalRef, ScopeRef: handle.ScopeRef, GrantRevision: handle.GrantRevision}
}

func sqliteRestartEventFrame(t *testing.T, target accessFenceFixture, suffix string, sequence int64, eventType string) eventstore.IngressFrame {
	t.Helper()
	incarnationID := target.incarnation.IncarnationID
	sourceID := "task/" + target.binding.AgentID
	sourceEpoch := target.binding.IncarnationID
	cursor, err := eventstore.CanonicalCursor(sourceID, sourceEpoch, eventstore.LaneOutput, sequence)
	if err != nil {
		t.Fatalf("CanonicalCursor: %v", err)
	}
	envelope := eventstore.EventEnvelope{
		EventID: fmt.Sprintf("event-reopen-%s-%s-%d", suffix, eventType, sequence), EventType: eventType, SchemaVersion: 1,
		RootAgentID: target.binding.AgentID, SubjectAgentID: target.binding.AgentID, ReservationID: target.binding.ReservationID,
		IncarnationID: &incarnationID, ExternalEpoch: target.incarnation.ExternalEpoch, IncarnationGeneration: target.incarnation.IncarnationGeneration,
		IdentityDigest: target.incarnation.IdentityDigest, BindingVersion: target.binding.BindingVersion, BindingDigest: target.binding.BindingDigest,
		SourceFenceRef: target.binding.SourceFenceRef, Provenance: "sqlite-reopen-test", TrustClass: "trusted", ContentRole: "event",
		ContentRef: "content/reopen/" + suffix, ContentSize: int64(len(suffix)), MediaType: "application/json", ObservedAt: "2026-09-01T00:00:00.000000000Z", WriterEpoch: "writer-reopen",
	}
	envelopeHash, err := eventstore.CanonicalEnvelopeHash(envelope)
	if err != nil {
		t.Fatalf("CanonicalEnvelopeHash: %v", err)
	}
	return eventstore.IngressFrame{SourceID: sourceID, SourceEpoch: sourceEpoch, Lane: eventstore.LaneOutput, SourceSequence: sequence, EnvelopeHash: envelopeHash, CanonicalCursor: cursor, Envelope: envelope}
}

func sqliteRestartDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

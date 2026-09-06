package accesswait

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

// testAccessWaitRejectsCrossSourceSelector verifies that direct accesswait
// callers cannot replace the unified_v2-owned source tuple, and that rejection
// happens before a durable wait row or event read is created.
func testAccessWaitRejectsCrossSourceSelector(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	grant := validGrantRequest("grant-source-binding", "grant/source-binding", strings.Repeat("0", 64))
	mustCreateGrant(t, fixture.store, grant)
	request := waitRequest(grant, "wait-source-binding", waitSelector(), "wait/source-binding", strings.Repeat("1", 64), strings.Repeat("2", 64))
	request.Selector.SourceID = "task/other"
	request.Selector.SourceEpoch = "inc-other"
	request.AfterSequence = 0
	request.AfterCursor = ""
	var before int
	if err := fixture.db.QueryRowContext(t.Context(), "SELECT count(*) FROM orchestration_event_wait_handles").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.CreateWait(t.Context(), request); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("CreateWait(cross-source)=%v, want ErrAccessDenied", err)
	}
	var after int
	if err := fixture.db.QueryRowContext(t.Context(), "SELECT count(*) FROM orchestration_event_wait_handles").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("cross-source CreateWait changed rows: before=%d after=%d", before, after)
	}

	// A valid current-task source remains admissible; this guards against
	// accidentally rejecting source epoch rollover/current-task semantics.
	request.Selector = waitSelector()
	handle := mustCreateWait(t, fixture.store, request)
	if handle.Selector.SourceID != "task/source" || handle.Selector.SourceEpoch != "inc-source" {
		t.Fatalf("CreateWait(current source) handle=%#v", handle)
	}

	// A selector tampering attempt must be denied before the poll path reaches
	// the canonical event reader. Durable SQL rows are immutable by trigger, so
	// exercise the owner authorization seam with a forged in-memory handle.
	forged := handle.Selector
	forged.SourceID = "task/other"
	forged.SourceEpoch = "inc-other"
	forgedHandle := handle
	forgedHandle.Selector = forged
	fixture.events.listCalls = 0
	if err := fixture.store.authorizeWait(t.Context(), PollRequest{WaitHandleID: handle.WaitHandleID, CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef, GrantRevision: handle.GrantRevision}, forgedHandle); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("authorizeWait(cross-source selector)=%v, want ErrAccessDenied", err)
	}
	if fixture.events.listCalls != 0 {
		t.Fatalf("cross-source PollWait reached event reader %d times", fixture.events.listCalls)
	}
}

func testAccessWaitOmittedIDReplay(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	grant := validGrantRequest("grant-omitted-id", "grant/omitted-id", strings.Repeat("1", 64))
	mustCreateGrant(t, fixture.store, grant)
	request := waitRequest(grant, "", waitSelector(), "wait/omitted-id", strings.Repeat("2", 64), strings.Repeat("3", 64))
	first := mustCreateWait(t, fixture.store, request)
	replay := mustCreateWait(t, fixture.store, request)
	if first.WaitHandleID == "" || replay.WaitHandleID != first.WaitHandleID {
		t.Fatalf("omitted wait handle replay ids: first=%q replay=%q", first.WaitHandleID, replay.WaitHandleID)
	}
	var rows int
	if err := fixture.db.QueryRowContext(t.Context(), "SELECT count(*) FROM orchestration_event_wait_handles").Scan(&rows); err != nil {
		t.Fatalf("count wait handles: %v", err)
	}
	if rows != 1 {
		t.Fatalf("wait handle rows=%d, want one idempotent row", rows)
	}
}

func testAccessWaitBoundedScanFindsLaterMatch(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	grant := validGrantRequest("grant-scan-pages", "grant/scan-pages", strings.Repeat("4", 64))
	mustCreateGrant(t, fixture.store, grant)
	selector := waitSelector()
	handle := mustCreateWait(t, fixture.store, waitRequest(grant, "wait-scan-pages", selector, "wait/scan-pages", strings.Repeat("5", 64), strings.Repeat("6", 64)))
	for sequence := int64(1); sequence <= 200; sequence++ {
		fixture.events.events = append(fixture.events.events, waitEventAt(selector, sequence, "noise", selector.TargetAgentID))
	}
	fixture.events.events = append(fixture.events.events, waitEventAt(selector, 201, "output", selector.TargetAgentID))
	result := mustPollWait(t, fixture.store, handle)
	if result.Status != "satisfied" || result.Event == nil || result.Event.SourceSequence != 201 {
		t.Fatalf("PollWait(after bounded scan)=%#v, want sequence 201 match", result)
	}
}

func testAccessWaitSelectorEventTypeFiltering(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	grant := validGrantRequest("grant-selector-filter", "grant/selector-filter", strings.Repeat("7", 64))
	mustCreateGrant(t, fixture.store, grant)
	selector := waitSelector()
	handle := mustCreateWait(t, fixture.store, waitRequest(grant, "wait-selector-filter", selector, "wait/selector-filter", strings.Repeat("8", 64), strings.Repeat("9", 64)))
	fixture.events.events = append(fixture.events.events,
		waitEventAt(selector, 1, "control", selector.TargetAgentID),
		waitEventAt(selector, 2, "output", selector.TargetAgentID),
	)
	result := mustPollWait(t, fixture.store, handle)
	if result.Status != "satisfied" || result.Event == nil || result.Event.Envelope.EventType != "output" || result.Event.SourceSequence != 2 {
		t.Fatalf("PollWait(selector filtering)=%#v, want output sequence 2", result)
	}
}

func testAccessWaitPastDeadlineTimesOut(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	grant := validGrantRequest("grant-past-deadline", "grant/past-deadline", strings.Repeat("a", 64))
	mustCreateGrant(t, fixture.store, grant)
	selector := waitSelector()
	request := waitRequest(grant, "wait-past-deadline", selector, "wait/past-deadline", strings.Repeat("b", 64), strings.Repeat("c", 64))
	request.DeadlineAt = grant.IssuedAt.Add(-time.Second)
	handle := mustCreateWait(t, fixture.store, request)
	fixture.events.events = append(fixture.events.events, matchingWaitEvent(selector))
	result := mustPollWait(t, fixture.store, handle)
	if result.Status != "timed_out" || result.Event != nil {
		t.Fatalf("PollWait(past deadline)=%#v, want timed_out without event", result)
	}
}

func testAccessWaitExpireGrantMarksWaitsExpired(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	grant := validGrantRequest("grant-expire-waits", "grant/expire-waits", strings.Repeat("d", 64))
	mustCreateGrant(t, fixture.store, grant)
	handle := mustCreateWait(t, fixture.store, waitRequest(grant, "wait-expire-waits", waitSelector(), "wait/expire-waits", strings.Repeat("e", 64), strings.Repeat("f", 64)))
	clocked, err := NewStore(fixture.db, fixture.identity, fixture.events, func() time.Time { return grant.ExpiresAt.Add(time.Second) })
	if err != nil {
		t.Fatalf("clocked store: %v", err)
	}
	expired, err := clocked.ExpireGrant(t.Context(), ExpireGrantRequest{GrantID: grant.GrantID, ExpectedRevision: 1, ExpiredAt: grant.ExpiresAt})
	if err != nil || expired.Status != "expired" {
		t.Fatalf("ExpireGrant=%#v err=%v, want expired grant", expired, err)
	}
	var status string
	var revision int64
	if err := fixture.db.QueryRowContext(t.Context(), "SELECT status, revision FROM orchestration_event_wait_handles WHERE wait_handle_id = ?", handle.WaitHandleID).Scan(&status, &revision); err != nil {
		t.Fatalf("read expired wait: %v", err)
	}
	if status != "expired" || revision != 2 {
		t.Fatalf("expired wait status=%q revision=%d, want expired revision 2", status, revision)
	}
}

func testAccessWaitStaleHandleTargetDenied(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	grant := validGrantRequest("grant-stale-target", "grant/stale-target", strings.Repeat("3", 64))
	mustCreateGrant(t, fixture.store, grant)
	handle := mustCreateWait(t, fixture.store, waitRequest(grant, "wait-stale-target", waitSelector(), "wait/stale-target", strings.Repeat("4", 64), strings.Repeat("5", 64)))
	handle.Target.BindingDigest = strings.Repeat("9", 64)
	err := fixture.store.authorizeWait(t.Context(), PollRequest{WaitHandleID: handle.WaitHandleID, CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef, GrantRevision: handle.GrantRevision}, handle)
	if !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("authorize stale handle target=%v, want ErrAccessDenied", err)
	}
}

// testAccessWaitRejectsNonTransactionalIdentity 保证 owner 不退回外层预检。
func testAccessWaitRejectsNonTransactionalIdentity(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	reader := nonTransactionalIdentityReader{inner: fixture.identity}
	if store, err := NewStore(fixture.db, reader, fixture.events); store != nil || err == nil {
		t.Fatalf("NewStore(non-transactional identity)=(%#v,%v), want fail-fast", store, err)
	}
}

// testAccessWaitRejectsUnknownSource 防止缺失 source 被静默当作 pending。
func testAccessWaitRejectsUnknownSource(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	grant := validGrantRequest("grant-unknown-source", "grant/unknown-source", strings.Repeat("6", 64))
	mustCreateGrant(t, fixture.store, grant)
	handle := mustCreateWait(t, fixture.store, waitRequest(grant, "wait-unknown-source", waitSelector(), "wait/unknown-source", strings.Repeat("7", 64), strings.Repeat("8", 64)))
	fixture.events.err = eventstore.ErrEventNotFound
	_, err := fixture.store.PollWait(t.Context(), PollRequest{WaitHandleID: handle.WaitHandleID, CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef, GrantRevision: 1})
	if !errors.Is(err, ErrWaitNotFound) {
		t.Fatalf("PollWait(unknown source)=%v, want ErrWaitNotFound", err)
	}
}

// testAccessWaitRejectsCursorWatermarkViolations 防止越洞和回放过期游标静默返回空页。
func testAccessWaitRejectsCursorWatermarkViolations(t *testing.T) {
	t.Run("cursor ahead", func(t *testing.T) {
		fixture := newAccessWaitFixture(t)
		grant := validGrantRequest("grant-cursor-ahead", "grant/cursor-ahead", strings.Repeat("9", 64))
		mustCreateGrant(t, fixture.store, grant)
		request := waitRequest(grant, "wait-cursor-ahead", waitSelector(), "wait/cursor-ahead", strings.Repeat("a", 64), strings.Repeat("b", 64))
		request.AfterSequence, request.AfterCursor = 1, "task/source/inc-source/output/1"
		fixture.events.sourceState = eventstore.SourceState{NextSequence: 2, MaterializedSequence: 1, DedupFloor: 0, Revision: 1, Status: "active"}
		handle := mustCreateWait(t, fixture.store, request)
		fixture.events.sourceState = eventstore.SourceState{NextSequence: 2, MaterializedSequence: 0, DedupFloor: 0, Revision: 1, Status: "active"}
		_, err := fixture.store.PollWait(t.Context(), PollRequest{WaitHandleID: handle.WaitHandleID, CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef, GrantRevision: 1})
		if !errors.Is(err, ErrWaitCursorAhead) {
			t.Fatalf("PollWait(cursor ahead)=%v, want ErrWaitCursorAhead", err)
		}
	})
	t.Run("replay floor", func(t *testing.T) {
		fixture := newAccessWaitFixture(t)
		grant := validGrantRequest("grant-replay-floor", "grant/replay-floor", strings.Repeat("c", 64))
		mustCreateGrant(t, fixture.store, grant)
		request := waitRequest(grant, "wait-replay-floor", waitSelector(), "wait/replay-floor", strings.Repeat("d", 64), strings.Repeat("e", 64))
		request.AfterSequence, request.AfterCursor = 1, "task/source/inc-source/output/1"
		fixture.events.sourceState = eventstore.SourceState{NextSequence: 2, MaterializedSequence: 1, DedupFloor: 0, Revision: 1, Status: "active"}
		handle := mustCreateWait(t, fixture.store, request)
		fixture.events.sourceState = eventstore.SourceState{NextSequence: 11, MaterializedSequence: 10, DedupFloor: 5, Revision: 1, Status: "active"}
		_, err := fixture.store.PollWait(t.Context(), PollRequest{WaitHandleID: handle.WaitHandleID, CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef, GrantRevision: 1})
		if !errors.Is(err, ErrWaitReplayExpired) {
			t.Fatalf("PollWait(replay floor)=%v, want ErrWaitReplayExpired", err)
		}
	})
}

// testAccessWaitCancelRaceReturnsWinner verifies terminal CAS replay after a concurrent expiry.
func testAccessWaitCancelRaceReturnsWinner(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	grant := validGrantRequest("grant-cancel-race", "grant/cancel-race", strings.Repeat("f", 64))
	mustCreateGrant(t, fixture.store, grant)
	handle := mustCreateWait(t, fixture.store, waitRequest(grant, "wait-cancel-race", cancelSelector(), "wait/cancel-race", strings.Repeat("1", 64), strings.Repeat("2", 64)))
	expiredStore, err := NewStore(fixture.db, fixture.identity, fixture.events, func() time.Time { return handle.DeadlineAt.Add(time.Second) })
	if err != nil {
		t.Fatal(err)
	}
	if result, err := expiredStore.PollWait(t.Context(), PollRequest{WaitHandleID: handle.WaitHandleID, CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef, GrantRevision: 1}); err != nil || result.Status != "timed_out" {
		t.Fatalf("expire winner=%#v err=%v", result, err)
	}
	result, err := fixture.store.cancelPendingWait(t.Context(), CancelRequest{WaitHandleID: handle.WaitHandleID, CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef, GrantRevision: 1, ReasonRef: "race", CancelledAt: time.Now().UTC()}, handle)
	if err != nil || result.Status != "timed_out" {
		t.Fatalf("cancel race result=%#v err=%v, want timed_out winner", result, err)
	}
}

// testAccessWaitPollRaceReplaysAnyTerminalWinner verifies that a poll losing
// its satisfied CAS replays whichever terminal transition won the race, not
// only a competing satisfied transition.
func testAccessWaitPollRaceReplaysAnyTerminalWinner(t *testing.T) {
	for _, winner := range []struct {
		name   string
		status string
		apply  func(*testing.T, *Store, WaitHandle, AccessGrantRequest)
	}{
		{
			name:   "cancelled",
			status: "cancelled",
			apply: func(t *testing.T, store *Store, handle WaitHandle, grant AccessGrantRequest) {
				t.Helper()
				if _, err := store.CancelWait(t.Context(), CancelRequest{
					WaitHandleID: handle.WaitHandleID, CallerPrincipalRef: grant.CallerPrincipalRef,
					ScopeRef: grant.ScopeRef, GrantRevision: 1,
					ReasonRef: "race", CancelledAt: time.Now().UTC(),
				}); err != nil {
					t.Fatalf("CancelWait: %v", err)
				}
			},
		},
		{
			name:   "timed_out",
			status: "timed_out",
			apply: func(t *testing.T, store *Store, handle WaitHandle, grant AccessGrantRequest) {
				t.Helper()
				clocked, err := NewStore(store.db, store.identities, store.events, func() time.Time {
					return handle.DeadlineAt.Add(time.Second)
				})
				if err != nil {
					t.Fatalf("clocked store: %v", err)
				}
				if _, err := clocked.PollWait(t.Context(), PollRequest{
					WaitHandleID: handle.WaitHandleID, CallerPrincipalRef: grant.CallerPrincipalRef,
					ScopeRef: grant.ScopeRef, GrantRevision: 1,
				}); err != nil {
					t.Fatalf("PollWait(timeout): %v", err)
				}
			},
		},
	} {
		t.Run(winner.name, func(t *testing.T) {
			fixture := newAccessWaitFixture(t)
			grant := validGrantRequest("grant-poll-race-"+winner.name, "grant/poll-race-"+winner.name, strings.Repeat("a", 64))
			mustCreateGrant(t, fixture.store, grant)
			handle := mustCreateWait(t, fixture.store, waitRequest(grant, "wait-poll-race-"+winner.name, cancelSelector(), "wait/poll-race-"+winner.name, strings.Repeat("b", 64), strings.Repeat("c", 64)))
			winner.apply(t, fixture.store, handle, grant)
			result, err := fixture.store.resolveSatisfiedRace(t.Context(), handle)
			if err != nil || result.Status != winner.status {
				t.Fatalf("resolveSatisfiedRace(%s)=%#v err=%v, want terminal winner", winner.status, result, err)
			}
		})
	}
}

// testAccessWaitTimestampPrecision 接受 Go 与 SQLite 毫秒两种固定 UTC 表示。
func testAccessWaitTimestampPrecision(t *testing.T) {
	want := time.Date(2026, time.September, 1, 1, 2, 3, 660000000, time.UTC)
	for _, encoded := range []string{"2026-09-01T01:02:03.66Z", "2026-09-01T01:02:03.660Z"} {
		got, err := parseUTC("fixture", encoded)
		if err != nil || !got.Equal(want) {
			t.Fatalf("parseUTC(%q)=(%v,%v), want %v", encoded, got, err, want)
		}
	}
	if got, err := parseUTC("fixture", "2026-09-01T01:02:03.6600Z"); err == nil || !got.IsZero() {
		t.Fatalf("parseUTC(non-canonical)=(%v,%v), want rejection", got, err)
	}
	if got, err := parseUTC("fixture", "2026-09-01T01:02:03.660+00:00"); err == nil || !got.IsZero() {
		t.Fatalf("parseUTC(offset)=(%v,%v), want rejection", got, err)
	}
}

// testAccessWaitUsesOwnerClock 保证 Wait 不混用真实 wall clock 与安全时间 owner。
func testAccessWaitUsesOwnerClock(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	grant := validGrantRequest("grant-owner-clock", "grant/owner-clock", strings.Repeat("3", 64))
	mustCreateGrant(t, fixture.store, grant)
	handle := mustCreateWait(t, fixture.store, waitRequest(grant, "wait-owner-clock", waitSelector(), "wait/owner-clock", strings.Repeat("4", 64), strings.Repeat("5", 64)))
	clocked, err := NewStore(fixture.db, fixture.identity, fixture.events, func() time.Time { return handle.DeadlineAt.Add(time.Second) })
	if err != nil {
		t.Fatal(err)
	}
	result, err := clocked.Wait(t.Context(), PollRequest{WaitHandleID: handle.WaitHandleID, CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef, GrantRevision: 1})
	if err != nil || result.Status != "timed_out" {
		t.Fatalf("Wait(owner clock)=%#v err=%v, want timed_out", result, err)
	}
}

// testAccessWaitSelectorColumnConsistency 防止篡改 selector JSON 绕过持久化列绑定。
func testAccessWaitSelectorColumnConsistency(t *testing.T) {
	row := storedb.OrchestrationEventWaitHandle{SelectorVersion: 1, SourceID: "source", SourceEpoch: "epoch", Lane: "output", TargetAgentID: "target"}
	selector := EventSelector{Version: 1, SourceID: "source", SourceEpoch: "epoch", Lane: eventstore.LaneOutput, TargetAgentID: "target"}
	if err := selectorColumnsMatch(row, selector); err != nil {
		t.Fatalf("selectorColumnsMatch(valid)=%v", err)
	}
	selector.SourceID = "tampered"
	if err := selectorColumnsMatch(row, selector); !errors.Is(err, ErrInvalidWait) {
		t.Fatalf("selectorColumnsMatch(tampered)=%v, want ErrInvalidWait", err)
	}
}

// testAccessWaitTargetSourceReadCursorZeroEffect proves that an explicit
// cross-task read uses the target source tuple, honors its canonical cursor,
// and does not create a second durable side effect during event reads.
func testAccessWaitTargetSourceReadCursorZeroEffect(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	grantRequest := validGrantRequest("grant-target-source", "grant/target-source", strings.Repeat("a", 64))
	grant := mustCreateGrant(t, fixture.store, grantRequest)
	selector := targetSourceSelector()
	assertTargetSelectorAdmitted(t, selector, grant)
	fixture.events.sourceState = eventstore.SourceState{
		SourceID: selector.SourceID, SourceEpoch: selector.SourceEpoch, Lane: selector.Lane,
		NextSequence: 3, MaterializedSequence: 2, Revision: 1, Status: "active",
	}
	fixture.events.events = []eventstore.AgentEvent{
		waitEventAt(selector, 1, "output", selector.TargetAgentID),
		waitEventAt(selector, 2, "output", selector.TargetAgentID),
	}
	handle := mustCreateWait(t, fixture.store, waitRequest(grantRequest, "wait-target-source", selector, "wait/target-source", strings.Repeat("b", 64), strings.Repeat("c", 64)))
	assertTargetWaitSatisfied(t, mustPollWait(t, fixture.store, handle))
	assertTargetCursorReadNoWaitMutation(t, fixture, grantRequest, selector)
}

func assertTargetSelectorAdmitted(t *testing.T, selector EventSelector, grant AccessGrant) {
	t.Helper()
	if !selectorSourceMatchesGrant(selector, grant) {
		t.Fatal("target selector was not admitted for a readable bound grant")
	}
}

func assertTargetWaitSatisfied(t *testing.T, result WaitResult) {
	t.Helper()
	if result.Status != "satisfied" || result.Event == nil || result.Event.SourceSequence != 1 || result.NextCursor != "task/target/inc-target/output/1" {
		t.Fatalf("PollWait(target source)=%#v, want target sequence 1", result)
	}
}

func assertTargetCursorReadNoWaitMutation(t *testing.T, fixture accessWaitFixture, grant AccessGrantRequest, selector EventSelector) {
	t.Helper()
	before := accessWaitHandleCount(t, fixture)
	items, err := fixture.store.ReadAuthorizedEvents(t.Context(), accessRequestFor(grant, "read"), selector, 1, 1)
	if err != nil {
		t.Fatalf("ReadAuthorizedEvents(target cursor): %v", err)
	}
	if !targetCursorItemsValid(items) {
		t.Fatalf("ReadAuthorizedEvents(target cursor)=%#v, want sequence 2", items)
	}
	if after := accessWaitHandleCount(t, fixture); after != before {
		t.Fatalf("target event read changed wait rows: before=%d after=%d", before, after)
	}
}

func targetCursorItemsValid(items []eventstore.AgentEvent) bool {
	return len(items) == 1 && items[0].SourceSequence == 2 && items[0].CanonicalCursor == "task/target/inc-target/output/2"
}

// testAccessWaitTargetSourceRejectsForgedSourceTarget checks selector and
// request fence forgery before the canonical event reader is touched.
func testAccessWaitTargetSourceRejectsForgedSourceTarget(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	grantRequest := validGrantRequest("grant-target-forged", "grant/target-forged", strings.Repeat("d", 64))
	grant := mustCreateGrant(t, fixture.store, grantRequest)
	selector := targetSourceSelector()
	request := accessRequestFor(grantRequest, "read")
	assertForgedTargetReadsDenied(t, fixture, request, selector)
	assertMalformedTargetSelectorDenied(t, selector, grant)
}

func assertForgedTargetReadsDenied(t *testing.T, fixture accessWaitFixture, request AccessRequest, selector EventSelector) {
	t.Helper()
	for _, test := range []struct {
		name   string
		mutate func(*AccessRequest, *EventSelector)
	}{
		{name: "source id", mutate: func(_ *AccessRequest, selector *EventSelector) { selector.SourceID = "task/other" }},
		{name: "source epoch", mutate: func(_ *AccessRequest, selector *EventSelector) { selector.SourceEpoch = "inc-other" }},
		{name: "target subject", mutate: func(_ *AccessRequest, selector *EventSelector) { selector.TargetAgentID = "other" }},
		{name: "target fence", mutate: func(request *AccessRequest, _ *EventSelector) { request.Target.BindingDigest = strings.Repeat("9", 64) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			forgedRequest, forgedSelector := request, selector
			test.mutate(&forgedRequest, &forgedSelector)
			fixture.events.listCalls = 0
			before := accessWaitHandleCount(t, fixture)
			if _, err := fixture.store.ReadAuthorizedEvents(t.Context(), forgedRequest, forgedSelector, 0, 1); !errors.Is(err, ErrAccessDenied) {
				t.Fatalf("ReadAuthorizedEvents(%s)=%v, want ErrAccessDenied", test.name, err)
			}
			if fixture.events.listCalls != 0 {
				t.Fatalf("forged %s reached event reader %d times", test.name, fixture.events.listCalls)
			}
			if after := accessWaitHandleCount(t, fixture); after != before {
				t.Fatalf("forged %s changed wait rows: before=%d after=%d", test.name, before, after)
			}
		})
	}
}

func assertMalformedTargetSelectorDenied(t *testing.T, selector EventSelector, grant AccessGrant) {
	t.Helper()
	malformed := grant
	malformed.Target.IncarnationID = ""
	if selectorSourceMatchesGrant(selector, malformed) {
		t.Fatal("selectorSourceMatchesGrant accepted an unbound target fence")
	}
	malformed = grant
	malformed.CanRead = false
	if selectorSourceMatchesGrant(selector, malformed) {
		t.Fatal("selectorSourceMatchesGrant accepted target source without read capability")
	}
}

// testAccessWaitTargetSourceRevokeAndRollover proves revocation and current
// target-fence rollover both deny before any target event is read.
func testAccessWaitTargetSourceRevokeAndRollover(t *testing.T) {
	assertTargetSourceRevocation(t)
	assertTargetSourceRollover(t)
	assertTargetSourcePostReadRollover(t)
}

func assertTargetSourceRevocation(t *testing.T) {
	t.Helper()
	fixture := newAccessWaitFixture(t)
	grantRequest := validGrantRequest("grant-target-revoke", "grant/target-revoke", strings.Repeat("e", 64))
	grant := mustCreateGrant(t, fixture.store, grantRequest)
	selector := targetSourceSelector()
	handle := mustCreateWait(t, fixture.store, waitRequest(grantRequest, "wait-target-revoke", selector, "wait/target-revoke", strings.Repeat("f", 64), strings.Repeat("0", 64)))
	if _, err := fixture.store.RevokeGrant(t.Context(), RevokeGrantRequest{
		GrantID: grant.GrantID, GrantRevision: grant.Revision, CallerPrincipalRef: grant.CallerPrincipalRef,
		ScopeRef: grant.ScopeRef, ReasonRef: "test-revoke", RequestDigest: grant.RequestDigest, RevokedAt: grant.IssuedAt.Add(time.Minute),
	}); err != nil {
		t.Fatalf("RevokeGrant(target source): %v", err)
	}
	fixture.events.listCalls = 0
	if _, err := fixture.store.PollWait(t.Context(), PollRequest{WaitHandleID: handle.WaitHandleID, CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef, GrantRevision: grant.Revision}); !errors.Is(err, ErrAccessDenied) && !errors.Is(err, ErrAccessRevoked) {
		t.Fatalf("PollWait(revoked target source)=%v, want access denial", err)
	}
	if fixture.events.listCalls != 0 {
		t.Fatalf("revoked target source reached event reader %d times", fixture.events.listCalls)
	}
}

func assertTargetSourceRollover(t *testing.T) {
	t.Helper()
	fixture := newAccessWaitFixture(t)
	grantRequest := validGrantRequest("grant-target-rollover", "grant/target-rollover", strings.Repeat("2", 64))
	grant := mustCreateGrant(t, fixture.store, grantRequest)
	selector := targetSourceSelector()
	handle := mustCreateWait(t, fixture.store, waitRequest(grantRequest, "wait-target-rollover", selector, "wait/target-rollover", strings.Repeat("3", 64), strings.Repeat("4", 64)))
	current, err := fixture.identity.GetCurrent(t.Context(), "target")
	if err != nil {
		t.Fatalf("read target binding: %v", err)
	}
	current.BindingVersion++
	current.BindingDigest = strings.Repeat("9", 64)
	rotating := &rotatingIdentityReader{inner: fixture.identity, targetOverride: current}
	rolledStore, err := NewStore(fixture.db, rotating, fixture.events)
	if err != nil {
		t.Fatalf("new rollover store: %v", err)
	}
	fixture.events.listCalls = 0
	if _, err := rolledStore.PollWait(t.Context(), PollRequest{WaitHandleID: handle.WaitHandleID, CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef, GrantRevision: grant.Revision}); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("PollWait(target rollover)=%v, want ErrAccessDenied", err)
	}
	if fixture.events.listCalls != 0 {
		t.Fatalf("target rollover reached event reader %d times", fixture.events.listCalls)
	}
}

func assertTargetSourcePostReadRollover(t *testing.T) {
	t.Helper()
	fixture := newAccessWaitFixture(t)
	grantRequest := validGrantRequest("grant-target-post-read", "grant/target-post-read", strings.Repeat("8", 64))
	grant := mustCreateGrant(t, fixture.store, grantRequest)
	selector := targetSourceSelector()
	assertTargetSelectorAdmitted(t, selector, grant)
	fixture.events.sourceState = eventstore.SourceState{SourceID: selector.SourceID, SourceEpoch: selector.SourceEpoch, Lane: selector.Lane, NextSequence: 2, MaterializedSequence: 1, Revision: 1, Status: "active"}
	fixture.events.events = []eventstore.AgentEvent{waitEventAt(selector, 1, "output", selector.TargetAgentID)}
	current, err := fixture.identity.GetCurrent(t.Context(), "target")
	if err != nil {
		t.Fatalf("read target binding for post-read rollover: %v", err)
	}
	current.BindingVersion++
	current.BindingDigest = strings.Repeat("7", 64)
	identities := &rolloverAfterReadIdentityReader{inner: fixture.identity, targetOverride: current}
	fixture.events.txReadHook = func() { identities.afterRead = true }
	rolledStore, err := NewStore(fixture.db, identities, fixture.events)
	if err != nil {
		t.Fatalf("new post-read rollover store: %v", err)
	}
	before := accessWaitHandleCount(t, fixture)
	items, err := rolledStore.ReadAuthorizedEvents(t.Context(), accessRequestFor(grantRequest, "read"), selector, 0, 1)
	if !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("ReadAuthorizedEvents(post-read rollover)=%v, want ErrAccessDenied", err)
	}
	if items != nil {
		t.Fatalf("ReadAuthorizedEvents(post-read rollover)=%#v, want no events", items)
	}
	if fixture.events.listCalls != 1 {
		t.Fatalf("post-read rollover event reader calls=%d, want one pre-authorized read", fixture.events.listCalls)
	}
	if after := accessWaitHandleCount(t, fixture); after != before {
		t.Fatalf("post-read rollover changed wait rows: before=%d after=%d", before, after)
	}
}

// testAccessWaitSameSourceCompatibility keeps the pre-existing caller-source
// selector shape valid while cross-task target selectors use the additive path.
func testAccessWaitSameSourceCompatibility(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	grantRequest := validGrantRequest("grant-same-source", "grant/same-source", strings.Repeat("5", 64))
	grant := mustCreateGrant(t, fixture.store, grantRequest)
	selector := waitSelector()
	if !selectorSourceMatchesGrant(selector, grant) {
		t.Fatal("same-source selector no longer matches grant source")
	}
	if _, err := fixture.store.CreateWait(t.Context(), waitRequest(grantRequest, "wait-same-source", selector, "wait/same-source", strings.Repeat("6", 64), strings.Repeat("7", 64))); err != nil {
		t.Fatalf("CreateWait(same-source): %v", err)
	}
}

func targetSourceSelector() EventSelector {
	return EventSelector{Version: 1, SourceID: "task/target", SourceEpoch: "inc-target", Lane: eventstore.LaneOutput, TargetAgentID: "target", EventTypes: []string{"output"}}
}

func accessWaitHandleCount(t *testing.T, fixture accessWaitFixture) int {
	t.Helper()
	var count int
	if err := fixture.db.QueryRowContext(t.Context(), "SELECT count(*) FROM orchestration_event_wait_handles").Scan(&count); err != nil {
		t.Fatalf("count wait handles: %v", err)
	}
	return count
}

type rotatingIdentityReader struct {
	inner          *identity.Store
	targetOverride identity.CurrentBinding
}

func (r *rotatingIdentityReader) GetCurrent(ctx context.Context, agentID string) (identity.CurrentBinding, error) {
	if agentID == r.targetOverride.AgentID {
		return r.targetOverride, nil
	}
	return r.inner.GetCurrent(ctx, agentID)
}

func (r *rotatingIdentityReader) GetCurrentWithDBTX(ctx context.Context, dbtx storedb.DBTX, agentID string) (identity.CurrentBinding, error) {
	if agentID == r.targetOverride.AgentID {
		return r.targetOverride, nil
	}
	return r.inner.GetCurrentWithDBTX(ctx, dbtx, agentID)
}

type rolloverAfterReadIdentityReader struct {
	inner          *identity.Store
	targetOverride identity.CurrentBinding
	targetReads    int
	afterRead      bool
}

func (r *rolloverAfterReadIdentityReader) GetCurrent(ctx context.Context, agentID string) (identity.CurrentBinding, error) {
	if agentID == r.targetOverride.AgentID {
		r.targetReads++
		if r.targetReads >= 2 {
			return r.targetOverride, nil
		}
	}
	return r.inner.GetCurrent(ctx, agentID)
}

func (r *rolloverAfterReadIdentityReader) GetCurrentWithDBTX(ctx context.Context, dbtx storedb.DBTX, agentID string) (identity.CurrentBinding, error) {
	if agentID == r.targetOverride.AgentID && r.afterRead {
		return r.targetOverride, nil
	}
	return r.inner.GetCurrentWithDBTX(ctx, dbtx, agentID)
}

type nonTransactionalIdentityReader struct{ inner *identity.Store }

func (r nonTransactionalIdentityReader) GetCurrent(ctx context.Context, agentID string) (identity.CurrentBinding, error) {
	return r.inner.GetCurrent(ctx, agentID)
}

func waitEventAt(selector EventSelector, sequence int64, eventType, subject string) eventstore.AgentEvent {
	event := matchingWaitEvent(selector)
	event.SourceSequence = sequence
	event.CanonicalCursor, _ = eventstore.CanonicalCursor(selector.SourceID, selector.SourceEpoch, selector.Lane, sequence)
	event.Envelope.EventID = fmt.Sprintf("event-wait-%d", sequence)
	event.Envelope.EventType = eventType
	event.Envelope.SubjectAgentID = subject
	return event
}

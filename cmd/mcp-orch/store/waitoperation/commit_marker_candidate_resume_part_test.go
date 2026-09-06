package waitoperation

import (
	"database/sql"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
)

func TestCommitMarkerResumeAndReplayAvoidsWaitRecreation(t *testing.T) {
	store, request, operation := statePlanWithProvisioned(t, 1, 0)
	planned := operation.Targets[0]
	grant := mustResumeGrant(t, store, operation, planned)
	grantPending := mustGrantPending(t, store, request, planned)
	waitPending := mustWaitPending(t, store, request, planned, grant, grantPending)
	wait := mustResumeWait(t, store, operation, planned, grant)
	waitUnknown := mustWaitUnknown(t, store, request, planned, grant, waitPending)
	clock := operation.IssuedAt.Add(5 * time.Second)
	marked := mustMarkedAccessStore(t, store.db, store.access, clock)
	resumeRequest := resumeRequestFor(waitUnknown)
	resumed := mustResumeMarker(t, marked, resumeRequest)
	assertResumedMarker(t, resumed, wait)
	replay := mustResumeMarker(t, marked, resumeRequest)
	assertResumeReplay(t, resumed, replay)
	assertSingleWait(t, store.db, planned)
}

func mustResumeGrant(t *testing.T, store *Store, operation Operation, target TargetState) accesswait.AccessGrant {
	t.Helper()
	grant, err := store.access.CreateGrant(t.Context(), resumeGrantRequest(operation, target))
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

func mustGrantPending(t *testing.T, store *Store, request PlanRequest, planned TargetState) TargetState {
	t.Helper()
	result, err := store.AppendTarget(t.Context(), TargetTransitionRequest{OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, TargetKey: planned.Key, ExpectedVersion: planned.Version, ExpectedPhase: planned.Phase, ExpectedRecordDigest: planned.RecordDigest, GrantStatus: "planned", WaitStatus: "absent", GrantCleanupState: "none", WaitCleanupState: "none", NextPhase: "grant_pending"})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func mustWaitPending(t *testing.T, store *Store, request PlanRequest, planned TargetState, grant accesswait.AccessGrant, grantPending TargetState) TargetState {
	t.Helper()
	grantID := grant.GrantID
	result, err := store.AppendTarget(t.Context(), TargetTransitionRequest{OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, TargetKey: planned.Key, ExpectedVersion: grantPending.Version, ExpectedPhase: grantPending.Phase, ExpectedRecordDigest: grantPending.RecordDigest, ResolvedGrantID: &grantID, GrantRevision: grant.Revision, GrantStatus: grant.Status, WaitStatus: "planned", GrantCleanupState: "none", WaitCleanupState: "none", NextPhase: "wait_pending"})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func mustResumeWait(t *testing.T, store *Store, operation Operation, planned TargetState, grant accesswait.AccessGrant) accesswait.WaitHandle {
	t.Helper()
	wait, err := store.access.CreateWait(t.Context(), accesswait.WaitRequest{GrantID: grant.GrantID, GrantRevision: grant.Revision, CallerPrincipalRef: operation.CallerPrincipalRef, ScopeRef: operation.ScopeRef, Selector: planned.Selector, AfterSequence: planned.AfterSequence, AfterCursor: planned.AfterCursor, DeadlineAt: operation.DeadlineAt, IdempotencyNamespace: planned.WaitNamespace, IdempotencyKeyDigest: planned.WaitKeyDigest, RequestDigest: planned.WaitRequestDigest})
	if err != nil {
		t.Fatal(err)
	}
	return wait
}

func mustWaitUnknown(t *testing.T, store *Store, request PlanRequest, planned TargetState, grant accesswait.AccessGrant, waitPending TargetState) TargetState {
	t.Helper()
	grantID := grant.GrantID
	result, err := store.AppendTarget(t.Context(), TargetTransitionRequest{OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, TargetKey: planned.Key, ExpectedVersion: waitPending.Version, ExpectedPhase: waitPending.Phase, ExpectedRecordDigest: waitPending.RecordDigest, ResolvedGrantID: &grantID, GrantRevision: grant.Revision, GrantStatus: grant.Status, WaitStatus: "unknown", GrantCleanupState: "none", WaitCleanupState: "none", NextPhase: "outcome_unknown", UncertainPhase: "wait_create"})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func mustResumeMarker(t *testing.T, store *Store, request ResumeTargetRequest) TargetState {
	t.Helper()
	result, err := store.ResumeTarget(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertResumedMarker(t *testing.T, target TargetState, wait accesswait.WaitHandle) {
	t.Helper()
	if target.Phase != "waiting" || target.ResolvedWaitHandleID == nil || *target.ResolvedWaitHandleID != wait.WaitHandleID {
		t.Fatalf("marked ResumeTarget() = %#v, want existing wait handle", target)
	}
}

func assertResumeReplay(t *testing.T, first, replay TargetState) {
	t.Helper()
	if replay.Version != first.Version || replay.RecordDigest != first.RecordDigest {
		t.Fatalf("marked ResumeTarget replay = %#v, want original target", replay)
	}
}

func assertSingleWait(t *testing.T, db *sql.DB, target TargetState) {
	t.Helper()
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM orchestration_event_wait_handles WHERE idempotency_namespace = ? AND idempotency_key_digest = ?`, target.WaitNamespace, target.WaitKeyDigest).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("marked resume wait count = %d, want one", count)
	}
}

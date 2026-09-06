package waitoperation

import (
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
)

func resumeRequestFor(target TargetState) ResumeTargetRequest {
	return ResumeTargetRequest{
		OperationKey:           target.OperationKey,
		OperationDigest:        target.OperationDigest,
		TargetKey:              target.Key,
		ExpectedVersion:        target.Version,
		ExpectedPhase:          target.Phase,
		ExpectedUncertainPhase: target.UncertainPhase,
		ExpectedRecordDigest:   target.RecordDigest,
	}
}

func resumeGrantRequest(operation Operation, target TargetState) accesswait.AccessGrantRequest {
	return accesswait.AccessGrantRequest{
		GrantID:              target.OriginalGrantID,
		CallerPrincipalRef:   operation.CallerPrincipalRef,
		ScopeRef:             operation.ScopeRef,
		Source:               operation.Source,
		Target:               target.Target,
		CanRead:              true,
		IdempotencyNamespace: target.GrantNamespace,
		IdempotencyKeyDigest: target.GrantKeyDigest,
		RequestDigest:        target.GrantRequestDigest,
		IssuedAt:             operation.IssuedAt,
		ExpiresAt:            operation.DeadlineAt,
	}
}

func TestResumeTargetReadsGrantAndLeavesWaitCreationExplicit(t *testing.T) {
	store, request, operation := statePlanWithProvisioned(t, 1, 0)
	planned := operation.Targets[0]
	unknown, err := store.AppendTarget(t.Context(), TargetTransitionRequest{
		OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, TargetKey: planned.Key,
		ExpectedVersion: planned.Version, ExpectedPhase: planned.Phase, ExpectedRecordDigest: planned.RecordDigest,
		GrantStatus: "unknown", WaitStatus: "absent", GrantCleanupState: "none", WaitCleanupState: "none",
		NextPhase: "outcome_unknown", UncertainPhase: "grant_create",
	})
	if err != nil {
		t.Fatalf("unknown grant transition = %#v err=%v", unknown, err)
	}
	if _, err := store.access.CreateGrant(t.Context(), resumeGrantRequest(operation, planned)); err != nil {
		t.Fatalf("durable grant setup = %v", err)
	}
	resumed, err := store.ResumeTarget(t.Context(), resumeRequestFor(unknown))
	if err != nil {
		t.Fatalf("ResumeTarget grant_create = %#v err=%v", resumed, err)
	}
	assertGrantResumeWithoutWait(t, store, resumed, planned)
}

func assertGrantResumeWithoutWait(t *testing.T, store *Store, resumed, planned TargetState) {
	t.Helper()
	if resumed.Phase != "wait_pending" || resumed.GrantStatus != "active" || resumed.ResolvedGrantID == nil || resumed.WaitStatus != "planned" || resumed.ResolvedWaitHandleID != nil {
		t.Fatalf("resumed grant_create = %#v", resumed)
	}
	var waitCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM orchestration_event_wait_handles WHERE idempotency_namespace = ? AND idempotency_key_digest = ?`, planned.WaitNamespace, planned.WaitKeyDigest).Scan(&waitCount); err != nil {
		t.Fatal(err)
	}
	if waitCount != 0 {
		t.Fatalf("resume grant_create created %d wait rows", waitCount)
	}
}

func TestResumeTargetReadsExistingWaitExactlyOnce(t *testing.T) {
	store, _, planned, unknown, wait := prepareExistingWaitResume(t)
	resumed, err := store.ResumeTarget(t.Context(), resumeRequestFor(unknown))
	if err != nil {
		t.Fatalf("ResumeTarget wait_create = %#v err=%v", resumed, err)
	}
	assertWaitResumeResult(t, store, resumed, unknown, planned, wait)
	replay, err := store.ResumeTarget(t.Context(), resumeRequestFor(unknown))
	assertWaitResumeReplay(t, replay, err, resumed)
	planOnly, err := NewStore(store.db, store.clock)
	if err != nil {
		t.Fatal(err)
	}
	planReplay, err := planOnly.ResumeTarget(t.Context(), resumeRequestFor(unknown))
	if err != nil || planReplay.RecordDigest != resumed.RecordDigest {
		t.Fatalf("plan-only resume replay = %#v err=%v", planReplay, err)
	}
}

func assertWaitResumeResult(t *testing.T, store *Store, resumed, unknown, planned TargetState, wait accesswait.WaitHandle) {
	t.Helper()
	if resumed.Phase != "waiting" || resumed.WaitStatus != "pending" || resumed.ResolvedWaitHandleID == nil || *resumed.ResolvedWaitHandleID != wait.WaitHandleID {
		t.Fatalf("resumed wait_create = %#v", resumed)
	}
	var waitCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM orchestration_event_wait_handles WHERE idempotency_namespace = ? AND idempotency_key_digest = ?`, planned.WaitNamespace, planned.WaitKeyDigest).Scan(&waitCount); err != nil {
		t.Fatal(err)
	}
	if waitCount != 1 {
		t.Fatalf("resume wait_create wait rows = %d, want one", waitCount)
	}
}

func assertWaitResumeReplay(t *testing.T, replay TargetState, err error, resumed TargetState) {
	t.Helper()
	if err != nil || replay.RecordDigest != resumed.RecordDigest || replay.Version != resumed.Version {
		t.Fatalf("resume replay = %#v err=%v", replay, err)
	}
}

func prepareExistingWaitResume(t *testing.T) (*Store, PlanRequest, TargetState, TargetState, accesswait.WaitHandle) {
	t.Helper()
	store, request, operation := statePlanWithProvisioned(t, 1, 0)
	planned := operation.Targets[0]
	grant, err := store.access.CreateGrant(t.Context(), resumeGrantRequest(operation, planned))
	if err != nil {
		t.Fatalf("durable grant setup = %v", err)
	}
	grantID := grant.GrantID
	grantPending, err := store.AppendTarget(t.Context(), TargetTransitionRequest{OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, TargetKey: planned.Key, ExpectedVersion: planned.Version, ExpectedPhase: planned.Phase, ExpectedRecordDigest: planned.RecordDigest, GrantStatus: "planned", WaitStatus: "absent", GrantCleanupState: "none", WaitCleanupState: "none", NextPhase: "grant_pending"})
	if err != nil {
		t.Fatal(err)
	}
	waitPending, err := store.AppendTarget(t.Context(), TargetTransitionRequest{OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, TargetKey: planned.Key, ExpectedVersion: grantPending.Version, ExpectedPhase: grantPending.Phase, ExpectedRecordDigest: grantPending.RecordDigest, ResolvedGrantID: &grantID, GrantRevision: grant.Revision, GrantStatus: grant.Status, WaitStatus: "planned", GrantCleanupState: "none", WaitCleanupState: "none", NextPhase: "wait_pending"})
	if err != nil {
		t.Fatal(err)
	}
	wait, err := store.access.CreateWait(t.Context(), accesswait.WaitRequest{GrantID: grant.GrantID, GrantRevision: grant.Revision, CallerPrincipalRef: operation.CallerPrincipalRef, ScopeRef: operation.ScopeRef, Selector: planned.Selector, AfterSequence: planned.AfterSequence, AfterCursor: planned.AfterCursor, DeadlineAt: operation.DeadlineAt, IdempotencyNamespace: planned.WaitNamespace, IdempotencyKeyDigest: planned.WaitKeyDigest, RequestDigest: planned.WaitRequestDigest})
	if err != nil {
		t.Fatalf("durable wait setup = %v", err)
	}
	unknown, err := store.AppendTarget(t.Context(), TargetTransitionRequest{OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, TargetKey: planned.Key, ExpectedVersion: waitPending.Version, ExpectedPhase: waitPending.Phase, ExpectedRecordDigest: waitPending.RecordDigest, ResolvedGrantID: &grantID, GrantRevision: grant.Revision, GrantStatus: "active", WaitStatus: "unknown", GrantCleanupState: "none", WaitCleanupState: "none", NextPhase: "outcome_unknown", UncertainPhase: "wait_create"})
	if err != nil {
		t.Fatalf("unknown wait transition = %#v err=%v", unknown, err)
	}
	return store, request, planned, unknown, wait
}

func TestResumeTargetTransitionUnknownReadsBothResources(t *testing.T) {
	store, request, operation := statePlan(t, 1)
	waiting := operation.Targets[0]
	erasing := TargetTransitionRequest{
		OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, TargetKey: waiting.Key,
		ExpectedVersion: waiting.Version, ExpectedPhase: waiting.Phase, ExpectedRecordDigest: waiting.RecordDigest,
		ResolvedGrantID: waiting.ResolvedGrantID, GrantRevision: waiting.GrantRevision, GrantStatus: "active",
		WaitRevision: 0, WaitStatus: "planned", GrantCleanupState: "none", WaitCleanupState: "none",
		NextPhase: "outcome_unknown", UncertainPhase: "target_transition",
	}
	if _, err := store.AppendTarget(t.Context(), erasing); err == nil {
		t.Fatal("target-transition unknown dropped an existing wait handle")
	}
	unknown, err := store.AppendTarget(t.Context(), TargetTransitionRequest{
		OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, TargetKey: waiting.Key,
		ExpectedVersion: waiting.Version, ExpectedPhase: waiting.Phase, ExpectedRecordDigest: waiting.RecordDigest,
		ResolvedGrantID: waiting.ResolvedGrantID, GrantRevision: waiting.GrantRevision, GrantStatus: "active",
		ResolvedWaitHandleID: waiting.ResolvedWaitHandleID, WaitRevision: waiting.WaitRevision, WaitStatus: "pending",
		GrantCleanupState: "none", WaitCleanupState: "none", NextPhase: "outcome_unknown", UncertainPhase: "target_transition",
	})
	if err != nil {
		t.Fatalf("unknown target transition = %#v err=%v", unknown, err)
	}
	resumed, err := store.ResumeTarget(t.Context(), resumeRequestFor(unknown))
	if err != nil || resumed.Phase != "waiting" || resumed.WaitStatus != "pending" || resumed.ResolvedWaitHandleID == nil {
		t.Fatalf("resumed target transition = %#v err=%v", resumed, err)
	}
}

func TestResumeTargetMissingGrantStaysUnknown(t *testing.T) {
	store, request, operation := statePlanWithProvisioned(t, 1, 0)
	planned := operation.Targets[0]
	unknown, err := store.AppendTarget(t.Context(), TargetTransitionRequest{
		OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, TargetKey: planned.Key,
		ExpectedVersion: planned.Version, ExpectedPhase: planned.Phase, ExpectedRecordDigest: planned.RecordDigest,
		GrantStatus: "unknown", WaitStatus: "absent", GrantCleanupState: "none", WaitCleanupState: "none",
		NextPhase: "outcome_unknown", UncertainPhase: "grant_create",
	})
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := store.ResumeTarget(t.Context(), resumeRequestFor(unknown))
	if !errors.Is(err, ErrResumeUnknown) {
		t.Fatalf("missing grant resume = %#v err=%v, want ErrResumeUnknown", resumed, err)
	}
	current, err := store.Get(t.Context(), OperationLookup{Key: request.OperationKey, Digest: request.OperationDigest})
	if err != nil || current.Targets[0].Version != unknown.Version || current.Targets[0].Phase != "outcome_unknown" {
		t.Fatalf("missing grant changed target = %#v err=%v", current, err)
	}
}

func TestResumeTargetRejectsCleanupUnknownAndMalformedIdentity(t *testing.T) {
	store, request, operation := statePlan(t, 1)
	bad := ResumeTargetRequest{OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, TargetKey: operation.Targets[0].Key, ExpectedVersion: 1, ExpectedPhase: "outcome_unknown", ExpectedUncertainPhase: "cleanup", ExpectedRecordDigest: strings.Repeat("a", 64)}
	if _, err := store.ResumeTarget(t.Context(), bad); !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("cleanup unknown resume = %v, want ErrInvalidOperation", err)
	}
	bad.OperationKey = ""
	if _, err := store.ResumeTarget(t.Context(), bad); !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("malformed resume identity = %v, want ErrInvalidOperation", err)
	}
}

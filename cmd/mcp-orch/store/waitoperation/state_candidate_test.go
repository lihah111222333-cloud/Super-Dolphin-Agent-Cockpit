package waitoperation

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
)

func statePlan(t *testing.T, count int) (*Store, PlanRequest, Operation) {
	return statePlanWithProvisioned(t, count, count)
}

func statePlanWithProvisioned(t *testing.T, count, provisioned int) (*Store, PlanRequest, Operation) {
	t.Helper()
	if provisioned < 0 || provisioned > count {
		t.Fatalf("invalid provisioned count %d/%d", provisioned, count)
	}
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
	request := PlanRequest{OperationKey: "state-" + strings.Repeat("x", count), OperationDigest: strings.Repeat("a", 64), CallerPrincipalRef: "principal", ScopeRef: "scope", Source: fixture.source, DeadlineAt: fixture.now.Add(30 * time.Second)}
	for index := 0; index < count; index++ {
		target := TargetPlan{Key: "target-" + string(rune('a'+index)), Target: fixture.target, Selector: testSelector()}
		request.Targets = append(request.Targets, target)
	}
	operation, err := store.Plan(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < provisioned; index++ {
		operation.Targets[index], err = store.ProvisionTarget(t.Context(), ProvisionTargetRequest{OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, TargetKey: operation.Targets[index].Key, ExpectedVersion: 1, ExpectedPhase: "planned", ExpectedRecordDigest: operation.Targets[index].RecordDigest})
		if err != nil {
			t.Fatal(err)
		}
	}
	return store, request, operation
}

func testSelector() accesswait.EventSelector {
	return accesswait.EventSelector{Version: 1, SourceID: "task/src", SourceEpoch: "inc-src", Lane: eventstore.LaneControl, TargetAgentID: "dst", EventTypes: []string{"output"}}
}

func makeTargetPhase(t *testing.T, store *Store, operation Operation, request PlanRequest, target TargetState, phase string) TargetState {
	t.Helper()
	grantID, waitID := cloneString(target.ResolvedGrantID), cloneString(target.ResolvedWaitHandleID)
	waitStatus, grantCleanup, waitCleanup := target.WaitStatus, target.GrantCleanupState, target.WaitCleanupState
	waitRevision := target.WaitRevision
	uncertainPhase := ""
	if phase == "cancelled" && target.WaitStatus == "pending" {
		if target.ResolvedWaitHandleID == nil {
			t.Fatal("pending target has no durable wait handle")
		}
		cancelled, err := store.access.CancelWait(t.Context(), accesswait.CancelRequest{
			WaitHandleID:       *target.ResolvedWaitHandleID,
			CallerPrincipalRef: operation.CallerPrincipalRef,
			ScopeRef:           operation.ScopeRef,
			GrantRevision:      target.GrantRevision,
			ReasonRef:          "state-test-cancel",
			CancelledAt:        store.clock().UTC(),
		})
		if err != nil {
			t.Fatal(err)
		}
		waitStatus = "cancelled"
		waitRevision = cancelled.Handle.Revision
	}
	if phase == "satisfied" {
		waitStatus = "satisfied"
	}
	if phase == "cleanup_pending" {
		grantCleanup, waitCleanup = "pending", "pending"
		uncertainPhase = "cleanup"
	}
	if phase == "cleanup_cleared" {
		grantCleanup, waitCleanup = "cleared", "cleared"
		uncertainPhase = "cleanup"
	}
	result, err := store.AppendTarget(t.Context(), TargetTransitionRequest{OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, TargetKey: target.Key, ExpectedVersion: target.Version, ExpectedPhase: target.Phase, ExpectedRecordDigest: target.RecordDigest, ResolvedGrantID: grantID, GrantRevision: target.GrantRevision, GrantStatus: target.GrantStatus, ResolvedWaitHandleID: waitID, WaitRevision: waitRevision, WaitStatus: waitStatus, GrantCleanupState: grantCleanup, WaitCleanupState: waitCleanup, NextPhase: phase, UncertainPhase: uncertainPhase})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func stateRequest(operation Operation, next, uncertain, digest, reason string) OperationStateRequest {
	return OperationStateRequest{OperationKey: operation.OperationKey, OperationDigest: operation.OperationDigest, ExpectedRevision: operation.Revision, ExpectedStatus: operation.Status, ExpectedUncertainPhase: operation.UncertainPhase, ExpectedCleanupRequestDigest: operation.CleanupRequestDigest, ExpectedCleanupReasonRef: operation.CleanupReasonRef, ExpectedResolvedAt: formatOptionalTime(operation.ResolvedAt), NextStatus: next, NextUncertainPhase: uncertain, NextCleanupRequestDigest: digest, NextCleanupReasonRef: reason}
}

func formatOptionalTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return formatUTC(*value)
}

func TestAdvanceStateReadyRequiresAllLatestTargetHeads(t *testing.T) {
	store, request, operation := statePlanWithProvisioned(t, 2, 1)
	_, err := store.AdvanceState(t.Context(), stateRequest(operation, "ready", "", "", ""))
	if !errors.Is(err, ErrOperationStateConflict) {
		t.Fatalf("AdvanceState incomplete = %v, want ErrOperationStateConflict", err)
	}
	current, err := store.Get(t.Context(), OperationLookup{Key: request.OperationKey, Digest: request.OperationDigest})
	if err != nil || current.Revision != operation.Revision || current.Status != operation.Status {
		t.Fatalf("incomplete ready changed operation=%#v err=%v", current, err)
	}
}

func TestAdvanceStateReadyAndStaleReplayConflicts(t *testing.T) {
	store, _, operation := statePlan(t, 1)
	advance := stateRequest(operation, "ready", "", "", "")
	wrongPredecessor := advance
	wrongPredecessor.ExpectedStatus = "partial"
	if _, err := store.AdvanceState(t.Context(), wrongPredecessor); !errors.Is(err, ErrOperationStateConflict) {
		t.Fatalf("wrong predecessor state = %v, want ErrOperationStateConflict", err)
	}
	ready, err := store.AdvanceState(t.Context(), advance)
	if err != nil || ready.Status != "ready" || ready.Revision != 2 {
		t.Fatalf("ready = %#v err=%v", ready, err)
	}
	replay, err := store.AdvanceState(t.Context(), advance)
	if !errors.Is(err, ErrOperationStateConflict) {
		t.Fatalf("stale ready replay = %#v err=%v, want ErrOperationStateConflict", replay, err)
	}
}

func TestAdvanceStateCleanupGateAndClearedPreservesMetadata(t *testing.T) {
	store, request, operation := statePlan(t, 1)
	terminal := makeTargetPhase(t, store, operation, request, operation.Targets[0], "cancelled")
	reason := "cleanup-required"
	digest, err := CanonicalCleanupRequestDigest(request.OperationKey, request.OperationDigest, reason)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := store.AdvanceState(t.Context(), stateRequest(operation, "cleanup_pending", "cleanup", digest, reason))
	if err != nil || pending.Status != "cleanup_pending" {
		t.Fatalf("cleanup_pending = %#v err=%v", pending, err)
	}
	clearedTarget, err := store.CleanupTarget(t.Context(), cleanupRequestFor(pending, terminal, reason))
	if err != nil {
		t.Fatalf("target cleanup = %#v err=%v", clearedTarget, err)
	}
	if clearedTarget.Phase != "cleanup_cleared" {
		t.Fatalf("target cleanup phase = %#v", clearedTarget)
	}
	cleared, err := store.AdvanceState(t.Context(), stateRequest(pending, "cleanup_cleared", "cleanup", digest, reason))
	if err != nil || cleared.Status != "cleanup_cleared" || cleared.CleanupRequestDigest != digest || cleared.ResolvedAt == nil {
		t.Fatalf("cleanup_cleared = %#v err=%v", cleared, err)
	}
}

func TestAdvanceStateUnknownRecoveryAndCleanupNoReopen(t *testing.T) {
	store, request, operation := statePlan(t, 1)
	recovered, err := store.AdvanceState(t.Context(), stateRequest(operation, "outcome_unknown", "operation_create", "", ""))
	if err != nil {
		t.Fatalf("unknown = %#v err=%v", recovered, err)
	}
	provisioned, err := store.AdvanceState(t.Context(), stateRequest(recovered, "provisioning", "", "", ""))
	if err != nil || provisioned.Status != "provisioning" {
		t.Fatalf("non-cleanup recovery = %#v err=%v", provisioned, err)
	}
	terminal := makeTargetPhase(t, store, provisioned, request, provisioned.Targets[0], "cancelled")
	_ = terminal
	reason := "cleanup-unknown"
	digest, _ := CanonicalCleanupRequestDigest(request.OperationKey, request.OperationDigest, reason)
	unknownCleanup, err := store.AdvanceState(t.Context(), stateRequest(provisioned, "outcome_unknown", "cleanup", digest, reason))
	if err != nil {
		t.Fatalf("cleanup unknown = %#v err=%v", unknownCleanup, err)
	}
	if _, err := store.AdvanceState(t.Context(), stateRequest(unknownCleanup, "provisioning", "", "", "")); err == nil {
		t.Fatal("cleanup outcome_unknown unexpectedly reopened provisioning")
	}
}

func TestAdvanceStateConcurrentCASAndReopen(t *testing.T) {
	store, request, operation := statePlan(t, 1)
	advance := stateRequest(operation, "ready", "", "", "")
	results := make([]Operation, 16)
	errs := make([]error, 16)
	var wg sync.WaitGroup
	for index := range results {
		wg.Add(1)
		runtimesafe.SafeGo(t.Context(), nil, "waitoperation.test.concurrent-advance-state", func(context.Context) {
			defer wg.Done()
			results[index], errs[index] = store.AdvanceState(t.Context(), advance)
		})
	}
	wg.Wait()
	winners := 0
	for index, err := range errs {
		if err == nil {
			winners++
			if results[index].Revision != 2 {
				t.Fatalf("winner[%d] = %#v", index, results[index])
			}
		} else if !errors.Is(err, ErrOperationStateConflict) {
			t.Fatalf("loser[%d] = %v", index, err)
		}
	}
	if winners != 1 {
		t.Fatalf("winners=%d, want one", winners)
	}
	got, err := store.Get(t.Context(), OperationLookup{Key: request.OperationKey, Digest: request.OperationDigest})
	if err != nil || got.Status != "ready" || got.Revision != 2 {
		t.Fatalf("reopen query = %#v err=%v", got, err)
	}
}

func TestAdvanceStateMalformedTimestampAndDigestZeroWrite(t *testing.T) {
	store, request, operation := statePlan(t, 1)
	bad := stateRequest(operation, "ready", "", "", "")
	bad.ExpectedResolvedAt = "2020-01-01T00:00:00+09:00"
	if _, err := store.AdvanceState(t.Context(), bad); !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("malformed expected timestamp = %v", err)
	}
	bad = stateRequest(operation, "cleanup_pending", "cleanup", strings.Repeat("f", 64), "reason")
	if _, err := store.AdvanceState(t.Context(), bad); !errors.Is(err, ErrReplayConflict) {
		t.Fatalf("malformed cleanup digest = %v", err)
	}
	current, err := store.Get(t.Context(), OperationLookup{Key: request.OperationKey, Digest: request.OperationDigest})
	if err != nil || current.Revision != operation.Revision {
		t.Fatalf("malformed request wrote operation=%#v err=%v", current, err)
	}
}

func TestAdvanceStateRejectsExpiredAggregateWrite(t *testing.T) {
	store, request, operation := statePlan(t, 1)
	store.clock = func() time.Time { return operation.DeadlineAt }
	if _, err := store.AdvanceState(t.Context(), stateRequest(operation, "partial", "", "", "")); !errors.Is(err, ErrDeadlineExpired) {
		t.Fatalf("expired aggregate state write = %v, want ErrDeadlineExpired", err)
	}
	current, err := store.Get(t.Context(), OperationLookup{Key: request.OperationKey, Digest: request.OperationDigest})
	if err != nil || current.Revision != operation.Revision || current.Status != operation.Status {
		t.Fatalf("expired aggregate state write changed operation=%#v err=%v", current, err)
	}
}

func TestAdvanceStateUnknownRequiresUncertainPhase(t *testing.T) {
	store, request, operation := statePlan(t, 1)
	bad := stateRequest(operation, "outcome_unknown", "", "", "")
	if _, err := store.AdvanceState(t.Context(), bad); !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("unknown without uncertain phase = %v, want ErrInvalidOperation", err)
	}
	current, err := store.Get(t.Context(), OperationLookup{Key: request.OperationKey, Digest: request.OperationDigest})
	if err != nil || current.Revision != operation.Revision {
		t.Fatalf("unknown malformed request changed operation=%#v err=%v", current, err)
	}
}

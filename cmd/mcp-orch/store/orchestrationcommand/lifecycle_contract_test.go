package orchestrationcommand

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

// TestCommandLifecycleContract 聚合命令生命周期的状态、证据、并发与恢复契约。
func TestCommandLifecycleContract(t *testing.T) {
	checks := []struct {
		name string
		run  func(*testing.T)
	}{
		{"constructor_requires_verifier", testLifecycleConstructorRequiresVerifier},
		{"prepare_is_explicit_and_idempotent", testLifecyclePrepareIsExplicitAndIdempotent},
		{"apply_idempotency_and_version_fence", testLifecycleApplyIdempotencyAndVersionFence},
		{"verifier_error_is_fail_closed", testLifecycleVerifierErrorIsFailClosed},
		{"verifier_envelope_fields", testLifecycleVerifierEnvelopeFields},
		{"persisted_read_integrity", TestCommandLifecycleReadIntegrity},
		{"regular_chain", testLifecycleRegularChain},
		{"terminal_states_do_not_revive", testLifecycleTerminalStatesDoNotRevive},
		{"attempt_fence", testLifecycleAttemptFence},
		{"unknown_recovery", testLifecycleUnknownRecovery},
		{"abort_paths_require_cleanup", testLifecycleAbortPathsRequireCleanup},
		{"concurrent_different_operations", testLifecycleConcurrentDifferentOperations},
		{"concurrent_same_operation", testLifecycleConcurrentSameOperation},
		{"close_reopen", testLifecycleCloseReopen},
		{"history_paging_and_limits", testLifecycleHistoryPagingAndLimits},
		{"state_matrix", testLifecycleStateMatrix},
		{"attempt_index_guard", testLifecycleAttemptIndexMatchesStateMachine},
		{"wire_field_guard", testLifecycleWireFieldGuard},
		{"mapper_omission_red_green", TestCommandLifecycleMapperOmissionFailsGuard},
		{"intent_regression", TestCommandIntentStoreContract},
	}
	for _, check := range checks {
		t.Run(check.name, check.run)
	}
}

func testLifecycleConstructorRequiresVerifier(t *testing.T) {
	db := openCommandIntentTestDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if got, err := NewCoordinator(store, nil); err == nil || got != nil {
		t.Fatalf("NewCoordinator(nil verifier) = %#v, %v; want nil coordinator and error", got, err)
	}
	var typedNil *lifecycleVerifier
	if got, err := NewCoordinator(store, typedNil); err == nil || got != nil {
		t.Fatalf("NewCoordinator(typed nil verifier) = %#v, %v; want nil coordinator and error", got, err)
	}
	if got, err := NewCoordinator(nil, &lifecycleVerifier{}); err == nil || got != nil {
		t.Fatalf("NewCoordinator(nil store) = %#v, %v; want nil coordinator and error", got, err)
	}
}

func testLifecyclePrepareIsExplicitAndIdempotent(t *testing.T) {
	fixture := newLifecycleFixture(t, "lifecycle-prepare")
	commandID := fixture.intent.Intent.CommandID
	assertLifecycleStateMissingBeforePrepare(t, fixture, commandID)
	first := prepareLifecycle(t, fixture, "prepare-operation")
	assertLifecyclePreparedGenesis(t, fixture, commandID, first)
	assertLifecyclePrepareReplay(t, fixture, commandID, first)
}

func assertLifecycleStateMissingBeforePrepare(t *testing.T, fixture lifecycleFixture, commandID string) {
	t.Helper()
	if _, err := fixture.store.GetState(t.Context(), commandID); !errors.Is(err, ErrLifecycleNotFound) {
		t.Fatalf("GetState before Prepare error = %v, want ErrLifecycleNotFound", err)
	}
}

func assertLifecyclePreparedGenesis(t *testing.T, fixture lifecycleFixture, commandID string, snapshot Snapshot) {
	t.Helper()
	assertLifecyclePhase(t, snapshot, PhaseCustodyPending, 1, "")
	if snapshot.AcceptResolution != "pending" || snapshot.Trigger != TriggerPrepare || snapshot.EvidenceRef != "" || snapshot.UncertainPhase != "" {
		t.Fatalf("prepare snapshot = %#v, want pending genesis", snapshot)
	}
	wantDigest, err := transitionDigest(TransitionRequest{CommandID: commandID, OperationID: "prepare-operation", Trigger: TriggerPrepare})
	if err != nil {
		t.Fatalf("transitionDigest(prepare) error = %v", err)
	}
	if snapshot.OperationDigest != wantDigest {
		t.Fatalf("prepare operation digest = %q, want computed digest %q", snapshot.OperationDigest, wantDigest)
	}
	if got := fixture.verifier.callCount(); got != 0 {
		t.Fatalf("Prepare verifier calls = %d, want 0", got)
	}
}

func assertLifecyclePrepareReplay(t *testing.T, fixture lifecycleFixture, commandID string, first Snapshot) {
	t.Helper()
	replay := prepareLifecycle(t, fixture, "prepare-operation")
	if !reflect.DeepEqual(replay, first) {
		t.Fatalf("same Prepare replay = %#v, want %#v", replay, first)
	}
	if _, err := fixture.coordinator.Prepare(t.Context(), commandID, "another-prepare-operation"); err == nil {
		t.Fatal("different Prepare operation unexpectedly created a second genesis")
	}
	state, err := fixture.store.GetState(t.Context(), commandID)
	if err != nil {
		t.Fatalf("GetState after Prepare conflict error = %v", err)
	}
	if !reflect.DeepEqual(state, first) {
		t.Fatalf("state after second Prepare = %#v, want %#v", state, first)
	}
}

func testLifecycleApplyIdempotencyAndVersionFence(t *testing.T) {
	fixture := newLifecycleFixture(t, "lifecycle-idempotency")
	prepared := prepareLifecycle(t, fixture, "prepare-operation")
	fixture.verifier.expectEvidence(TriggerAccept, "fixture:accept")
	accept := lifecycleRequest(prepared, "accept-operation", TriggerAccept, "", "fixture:accept")
	accepted := applyLifecycle(t, fixture, accept)
	assertLifecyclePhase(t, accepted, PhaseAccepted, 2, "")
	fixture.verifier.expectEvidence(TriggerBeginDispatch, "fixture:dispatch")
	dispatch := lifecycleRequest(accepted, "dispatch-operation", TriggerBeginDispatch, "attempt-a1", "fixture:dispatch")
	latest := applyLifecycle(t, fixture, dispatch)
	assertLifecyclePhase(t, latest, PhaseDispatchPending, 3, "attempt-a1")
	callsBeforeReplay := fixture.verifier.callCount()
	replay, err := fixture.coordinator.Apply(t.Context(), accept)
	if err != nil {
		t.Fatalf("same Apply replay error = %v", err)
	}
	if !reflect.DeepEqual(replay, accepted) || replay.Version == latest.Version {
		t.Fatalf("same Apply replay = %#v, want original version %#v", replay, accepted)
	}
	if got := fixture.verifier.callCount(); got != callsBeforeReplay {
		t.Fatalf("same Apply replay verifier calls = %d, want %d", got, callsBeforeReplay)
	}
	changed := accept
	changed.EvidenceRef = "fixture:changed"
	if _, err := fixture.coordinator.Apply(t.Context(), changed); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("same operation with changed request error = %v, want ErrOperationConflict", err)
	}
	stale := lifecycleRequest(latest, "stale-operation", TriggerConfirmDispatch, "attempt-a1", "fixture:stale")
	stale.ExpectedVersion = accepted.Version
	expectLifecycleError(t, fixture, stale, ErrTransitionConflict)
	state, err := fixture.store.GetState(t.Context(), fixture.intent.Intent.CommandID)
	if err != nil {
		t.Fatalf("GetState after idempotency checks error = %v", err)
	}
	if !reflect.DeepEqual(state, latest) {
		t.Fatalf("state after idempotency checks = %#v, want %#v", state, latest)
	}
}

func testLifecycleVerifierErrorIsFailClosed(t *testing.T) {
	fixture := newLifecycleFixture(t, "lifecycle-verifier-error")
	prepared := prepareLifecycle(t, fixture, "prepare-operation")
	before := prepared
	rejection := errors.New("fixture receipt is not authoritative")
	fixture.verifier.setError(rejection)
	request := lifecycleRequest(prepared, "accept-operation", TriggerAccept, "", "fixture:untrusted")
	fixture.verifier.expectEvidence(request.Trigger, request.EvidenceRef)
	if _, err := fixture.coordinator.Apply(t.Context(), request); !errors.Is(err, ErrEvidenceRejected) || !errors.Is(err, rejection) {
		t.Fatalf("verifier error = %v, want ErrEvidenceRejected and fixture error", err)
	}
	after, err := fixture.store.GetState(t.Context(), fixture.intent.Intent.CommandID)
	if err != nil {
		t.Fatalf("GetState after verifier rejection error = %v", err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("state after verifier rejection = %#v, want %#v", after, before)
	}
	fixture.verifier.setError(nil)
	accepted := applyLifecycle(t, fixture, request)
	assertLifecyclePhase(t, accepted, PhaseAccepted, 2, "")
}

func testLifecycleRegularChain(t *testing.T) {
	fixture := newLifecycleFixture(t, "lifecycle-regular-chain")
	current := prepareLifecycle(t, fixture, "prepare-operation")
	steps := []struct {
		operation string
		trigger   Trigger
		attempt   string
		evidence  string
		phase     Phase
	}{
		{"accept-operation", TriggerAccept, "", "fixture:accept", PhaseAccepted},
		{"dispatch-operation", TriggerBeginDispatch, "attempt-a1", "fixture:dispatch", PhaseDispatchPending},
		{"confirm-operation", TriggerConfirmDispatch, "attempt-a1", "fixture:confirm", PhaseDispatched},
		{"redeem-operation", TriggerRedeemEffect, "attempt-a1", "fixture:redeem", PhaseEffectPending},
		{"issue-operation", TriggerIssueToken, "attempt-a1", "fixture:issue", PhaseTokenIssued},
		{"start-operation", TriggerStartEffect, "attempt-a1", "fixture:start", PhaseEffectStarted},
		{"ack-operation", TriggerProviderAck, "attempt-a1", "fixture:ack", PhaseProviderAcknowledged},
		{"complete-operation", TriggerComplete, "attempt-a1", "fixture:complete", PhaseCompleted},
	}
	for index, step := range steps {
		fixture.verifier.expectEvidence(step.trigger, step.evidence)
		current = applyLifecycle(t, fixture, lifecycleRequest(current, step.operation, step.trigger, step.attempt, step.evidence))
		assertLifecyclePhase(t, current, step.phase, int64(index+2), step.attempt)
	}
	if got := fixture.verifier.callCount(); got != len(steps) {
		t.Fatalf("regular chain verifier calls = %d, want %d", got, len(steps))
	}
}

func testLifecycleTerminalStatesDoNotRevive(t *testing.T) {
	t.Run("completed", func(t *testing.T) {
		fixture := newLifecycleFixture(t, "lifecycle-terminal-completed")
		current := lifecycleToCompleted(t, fixture)
		assertLifecycleTerminalNoRevival(t, fixture, current)
	})
	t.Run("failed", func(t *testing.T) {
		fixture := newLifecycleFixture(t, "lifecycle-terminal-failed")
		current := lifecycleToEffectStarted(t, fixture)
		current = applyLifecycle(t, fixture, lifecycleRequest(current, "fail-operation", TriggerFailEffect, "attempt-a1", "fixture:fail"))
		assertLifecyclePhase(t, current, PhaseFailed, 8, "attempt-a1")
		assertLifecycleTerminalNoRevival(t, fixture, current)
	})
	t.Run("rejected", func(t *testing.T) {
		fixture := newLifecycleFixture(t, "lifecycle-terminal-rejected")
		current := prepareLifecycle(t, fixture, "prepare-operation")
		current = applyLifecycle(t, fixture, lifecycleRequest(current, "abort-accept-operation", TriggerAbortAccept, "", "fixture:abort-accept"))
		if current.AcceptResolution != "aborted" {
			t.Fatalf("abort accept resolution = %q, want aborted", current.AcceptResolution)
		}
		assertLifecyclePhase(t, current, PhaseRejected, 2, "")
		assertLifecycleTerminalNoRevival(t, fixture, current)
	})
}

func lifecycleToEffectStarted(t *testing.T, fixture lifecycleFixture) Snapshot {
	t.Helper()
	current := prepareLifecycle(t, fixture, "prepare-operation")
	steps := []struct {
		operation string
		trigger   Trigger
		attempt   string
	}{
		{"accept-operation", TriggerAccept, ""},
		{"dispatch-operation", TriggerBeginDispatch, "attempt-a1"},
		{"confirm-operation", TriggerConfirmDispatch, "attempt-a1"},
		{"redeem-operation", TriggerRedeemEffect, "attempt-a1"},
		{"issue-operation", TriggerIssueToken, "attempt-a1"},
		{"start-operation", TriggerStartEffect, "attempt-a1"},
	}
	for _, step := range steps {
		current = applyLifecycle(t, fixture, lifecycleRequest(current, step.operation, step.trigger, step.attempt, "fixture:"+string(step.trigger)))
	}
	return current
}

func lifecycleToCompleted(t *testing.T, fixture lifecycleFixture) Snapshot {
	t.Helper()
	current := lifecycleToEffectStarted(t, fixture)
	current = applyLifecycle(t, fixture, lifecycleRequest(current, "ack-operation", TriggerProviderAck, "attempt-a1", "fixture:ack"))
	return applyLifecycle(t, fixture, lifecycleRequest(current, "complete-operation", TriggerComplete, "attempt-a1", "fixture:complete"))
}

func assertLifecycleTerminalNoRevival(t *testing.T, fixture lifecycleFixture, current Snapshot) {
	t.Helper()
	for index, trigger := range lifecycleTriggerUniverse() {
		attempt := current.AttemptID
		if startsAttempt(trigger) {
			attempt = "revival-attempt"
		}
		request := lifecycleRequest(current, fmt.Sprintf("revival-operation-%d", index), trigger, attempt, "fixture:revival")
		fixture.verifier.expectEvidence(request.Trigger, request.EvidenceRef)
		if _, err := fixture.coordinator.Apply(t.Context(), request); err == nil {
			t.Fatalf("terminal phase %s accepted trigger %s", current.Phase, trigger)
		}
	}
	got, err := fixture.store.GetState(t.Context(), current.CommandID)
	if err != nil {
		t.Fatalf("GetState after terminal revival attempts error = %v", err)
	}
	if !reflect.DeepEqual(got, current) {
		t.Fatalf("terminal state after revival attempts = %#v, want %#v", got, current)
	}
}

func testLifecycleAttemptFence(t *testing.T) {
	fixture := newLifecycleFixture(t, "lifecycle-attempt-fence")
	current := prepareLifecycle(t, fixture, "prepare-operation")
	current = applyLifecycle(t, fixture, lifecycleRequest(current, "accept-operation", TriggerAccept, "", "fixture:accept"))
	current = applyLifecycle(t, fixture, lifecycleRequest(current, "dispatch-operation", TriggerBeginDispatch, "attempt-a1", "fixture:dispatch"))
	wrongEvidence := lifecycleRequest(current, "wrong-reclaim-operation", TriggerReclaimUnsent, "attempt-a2", "not-authoritative")
	fixture.verifier.expectEvidence(TriggerReclaimUnsent, "fixture:reclaim-authority")
	if _, err := fixture.coordinator.Apply(t.Context(), wrongEvidence); !errors.Is(err, ErrEvidenceRejected) {
		t.Fatalf("wrong reclaim evidence error = %v, want ErrEvidenceRejected", err)
	}
	unchanged, err := fixture.store.GetState(t.Context(), current.CommandID)
	if err != nil {
		t.Fatalf("GetState after wrong reclaim evidence error = %v", err)
	}
	if !reflect.DeepEqual(unchanged, current) {
		t.Fatalf("state after wrong reclaim evidence = %#v, want %#v", unchanged, current)
	}
	current = applyLifecycle(t, fixture, lifecycleRequest(current, "reclaim-operation", TriggerReclaimUnsent, "attempt-a2", "fixture:reclaim-authority"))
	assertLifecyclePhase(t, current, PhaseDispatchPending, 4, "attempt-a2")
	fixture.verifier.expectEvidence(TriggerReclaimUnsent, "fixture:old-attempt-reclaim")
	oldReclaim := lifecycleRequest(current, "old-attempt-reclaim", TriggerReclaimUnsent, "attempt-a1", "fixture:old-attempt-reclaim")
	expectLifecycleError(t, fixture, oldReclaim, ErrTransitionConflict)
	if _, err := fixture.store.GetOperation(t.Context(), current.CommandID, oldReclaim.OperationID); !errors.Is(err, ErrLifecycleNotFound) {
		t.Fatalf("old attempt reclaim operation lookup error = %v, want ErrLifecycleNotFound", err)
	}
	oldAttempt := lifecycleRequest(current, "old-attempt-confirm", TriggerConfirmDispatch, "attempt-a1", "fixture:old-attempt")
	expectLifecycleError(t, fixture, oldAttempt, ErrTransitionConflict)
	current = applyLifecycle(t, fixture, lifecycleRequest(current, "confirm-operation", TriggerConfirmDispatch, "attempt-a2", "fixture:confirm"))
	assertLifecyclePhase(t, current, PhaseDispatched, 5, "attempt-a2")
}

func testLifecycleUnknownRecovery(t *testing.T) {
	t.Run("token_issued_can_reconcile_unstarted_only_with_cleanup", testUnknownTokenIssuedCleanup)
	t.Run("started_unknown_cannot_reconcile_unstarted", testStartedUnknownRejectsUnstarted)
	t.Run("provider_ack_unknown_cannot_reconcile_unstarted", testProviderAckUnknownRejectsUnstarted)
	t.Run("unknown_allowed_reconciliation", testUnknownAllowedReconciliation)
}

func testUnknownTokenIssuedCleanup(t *testing.T) {
	fixture := newLifecycleFixture(t, "lifecycle-unknown-unstarted")
	current := lifecycleToTokenIssued(t, fixture)
	current = applyLifecycle(t, fixture, lifecycleRequest(current, "unknown-operation", TriggerMarkUnknown, "attempt-a1", "fixture:unknown"))
	if current.UncertainPhase != PhaseTokenIssued {
		t.Fatalf("unknown uncertain phase = %s, want %s", current.UncertainPhase, PhaseTokenIssued)
	}
	current = applyLifecycle(t, fixture, lifecycleRequest(current, "unstarted-operation", TriggerReconcileUnstarted, "attempt-a1", "fixture:unstarted"))
	if current.Phase != PhaseAbortedBeforeStart || current.UncertainPhase != "" {
		t.Fatalf("reconcile unstarted snapshot = %#v, want aborted-before-start with cleared uncertainty", current)
	}
	premature := lifecycleRequest(current, "premature-finish", TriggerFinishCleanup, "attempt-a1", "fixture:finish")
	fixture.verifier.expectEvidence(premature.Trigger, premature.EvidenceRef)
	if _, err := fixture.coordinator.Apply(t.Context(), premature); err == nil {
		t.Fatal("unknown unstarted path finished cleanup without begin_cleanup")
	}
	current = applyLifecycle(t, fixture, lifecycleRequest(current, "begin-cleanup", TriggerBeginCleanup, "attempt-a1", "fixture:begin-cleanup"))
	current = applyLifecycle(t, fixture, lifecycleRequest(current, "finish-cleanup", TriggerFinishCleanup, "attempt-a1", "fixture:finish-cleanup"))
	if current.Phase != PhaseRejected || current.UncertainPhase != "" || current.AcceptResolution != "accepted" {
		t.Fatalf("unknown unstarted cleanup result = %#v, want accepted operation rejected after cleanup", current)
	}
}

func lifecycleToTokenIssued(t *testing.T, fixture lifecycleFixture) Snapshot {
	t.Helper()
	current := prepareLifecycle(t, fixture, "prepare-operation")
	current = applyLifecycle(t, fixture, lifecycleRequest(current, "accept-operation", TriggerAccept, "", "fixture:accept"))
	current = applyLifecycle(t, fixture, lifecycleRequest(current, "dispatch-operation", TriggerBeginDispatch, "attempt-a1", "fixture:dispatch"))
	current = applyLifecycle(t, fixture, lifecycleRequest(current, "confirm-operation", TriggerConfirmDispatch, "attempt-a1", "fixture:confirm"))
	current = applyLifecycle(t, fixture, lifecycleRequest(current, "redeem-operation", TriggerRedeemEffect, "attempt-a1", "fixture:redeem"))
	return applyLifecycle(t, fixture, lifecycleRequest(current, "issue-operation", TriggerIssueToken, "attempt-a1", "fixture:issue"))
}

func testStartedUnknownRejectsUnstarted(t *testing.T) {
	fixture := newLifecycleFixture(t, "lifecycle-unknown-started")
	current := lifecycleToEffectStarted(t, fixture)
	current = applyLifecycle(t, fixture, lifecycleRequest(current, "unknown-operation", TriggerMarkUnknown, "attempt-a1", "fixture:unknown"))
	before := current
	request := lifecycleRequest(current, "unstarted-operation", TriggerReconcileUnstarted, "attempt-a1", "fixture:unstarted")
	fixture.verifier.expectEvidence(request.Trigger, request.EvidenceRef)
	if _, err := fixture.coordinator.Apply(t.Context(), request); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("started unknown reconcile_unstarted error = %v, want ErrInvalidTransition", err)
	}
	assertLifecycleStateUnchanged(t, fixture, before)
}

func testProviderAckUnknownRejectsUnstarted(t *testing.T) {
	fixture := newLifecycleFixture(t, "lifecycle-unknown-ack")
	current := lifecycleToEffectStarted(t, fixture)
	current = applyLifecycle(t, fixture, lifecycleRequest(current, "ack-operation", TriggerProviderAck, "attempt-a1", "fixture:ack"))
	current = applyLifecycle(t, fixture, lifecycleRequest(current, "unknown-operation", TriggerMarkUnknown, "attempt-a1", "fixture:unknown"))
	before := current
	request := lifecycleRequest(current, "unstarted-operation", TriggerReconcileUnstarted, "attempt-a1", "fixture:unstarted")
	fixture.verifier.expectEvidence(request.Trigger, request.EvidenceRef)
	if _, err := fixture.coordinator.Apply(t.Context(), request); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("provider ack unknown reconcile_unstarted error = %v, want ErrInvalidTransition", err)
	}
	assertLifecycleStateUnchanged(t, fixture, before)
}

func testUnknownAllowedReconciliation(t *testing.T) {
	t.Run("reconcile_ack_then_complete", func(t *testing.T) {
		fixture := newLifecycleFixture(t, "lifecycle-unknown-ack-reconcile")
		current := lifecycleToEffectStarted(t, fixture)
		current = applyLifecycle(t, fixture, lifecycleRequest(current, "unknown-operation", TriggerMarkUnknown, "attempt-a1", "fixture:unknown"))
		before := current
		for index, trigger := range []Trigger{
			TriggerBeginDispatch, TriggerReclaimUnsent, TriggerIssueToken,
			TriggerStartEffect, TriggerReconcileAck, TriggerReconcileComplete,
			TriggerReconcileFailed, TriggerReconcileUnstarted,
		} {
			request := lifecycleRequest(current, fmt.Sprintf("unknown-disallowed-%d", index), trigger, "attempt-a2", "fixture:disallowed")
			fixture.verifier.expectEvidence(request.Trigger, request.EvidenceRef)
			if _, err := fixture.coordinator.Apply(t.Context(), request); err == nil {
				t.Fatalf("unknown phase accepted disallowed trigger %s", trigger)
			}
		}
		assertLifecycleStateUnchanged(t, fixture, before)
		current = applyLifecycle(t, fixture, lifecycleRequest(current, "reconcile-ack-operation", TriggerReconcileAck, "attempt-a1", "fixture:reconcile-ack"))
		if current.Phase != PhaseProviderAcknowledged || current.UncertainPhase != "" {
			t.Fatalf("reconcile ack result = %#v, want acknowledged and certain", current)
		}
		current = applyLifecycle(t, fixture, lifecycleRequest(current, "complete-operation", TriggerComplete, "attempt-a1", "fixture:complete"))
		assertLifecyclePhase(t, current, PhaseCompleted, 10, "attempt-a1")
	})
	t.Run("reconcile_complete", func(t *testing.T) {
		fixture := newLifecycleFixture(t, "lifecycle-unknown-complete-reconcile")
		current := lifecycleToTokenIssued(t, fixture)
		current = applyLifecycle(t, fixture, lifecycleRequest(current, "unknown-operation", TriggerMarkUnknown, "attempt-a1", "fixture:unknown"))
		current = applyLifecycle(t, fixture, lifecycleRequest(current, "reconcile-complete-operation", TriggerReconcileComplete, "attempt-a1", "fixture:reconcile-complete"))
		assertLifecyclePhase(t, current, PhaseCompleted, 8, "attempt-a1")
		if current.UncertainPhase != "" {
			t.Fatalf("reconcile complete uncertain phase = %s, want empty", current.UncertainPhase)
		}
	})
	t.Run("reconcile_failed", func(t *testing.T) {
		fixture := newLifecycleFixture(t, "lifecycle-unknown-failed-reconcile")
		current := lifecycleToEffectStarted(t, fixture)
		current = applyLifecycle(t, fixture, lifecycleRequest(current, "unknown-operation", TriggerMarkUnknown, "attempt-a1", "fixture:unknown"))
		current = applyLifecycle(t, fixture, lifecycleRequest(current, "reconcile-failed-operation", TriggerReconcileFailed, "attempt-a1", "fixture:reconcile-failed"))
		assertLifecyclePhase(t, current, PhaseFailed, 9, "attempt-a1")
		if current.UncertainPhase != "" {
			t.Fatalf("reconcile failed uncertain phase = %s, want empty", current.UncertainPhase)
		}
	})
}

func assertLifecycleStateUnchanged(t *testing.T, fixture lifecycleFixture, want Snapshot) {
	t.Helper()
	got, err := fixture.store.GetState(t.Context(), want.CommandID)
	if err != nil {
		t.Fatalf("GetState unchanged check error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("state changed unexpectedly: got %#v want %#v", got, want)
	}
}

func testLifecycleAbortPathsRequireCleanup(t *testing.T) {
	t.Run("abort_accept_is_distinct", func(t *testing.T) {
		fixture := newLifecycleFixture(t, "lifecycle-abort-accept")
		current := prepareLifecycle(t, fixture, "prepare-operation")
		current = applyLifecycle(t, fixture, lifecycleRequest(current, "abort-accept-operation", TriggerAbortAccept, "", "fixture:abort-accept"))
		if current.Phase != PhaseRejected || current.AcceptResolution != "aborted" || current.AttemptID != "" {
			t.Fatalf("abort accept result = %#v, want aborted acceptance rejected without attempt", current)
		}
	})
	t.Run("abort_dispatch_retains_attempt", func(t *testing.T) {
		fixture := newLifecycleFixture(t, "lifecycle-abort-dispatch")
		current := prepareLifecycle(t, fixture, "prepare-operation")
		current = applyLifecycle(t, fixture, lifecycleRequest(current, "accept-operation", TriggerAccept, "", "fixture:accept"))
		current = applyLifecycle(t, fixture, lifecycleRequest(current, "dispatch-operation", TriggerBeginDispatch, "attempt-a1", "fixture:dispatch"))
		current = applyLifecycle(t, fixture, lifecycleRequest(current, "abort-dispatch-operation", TriggerAbortDispatch, "attempt-a1", "fixture:abort-dispatch"))
		if current.Phase != PhaseDispatchAborted || current.AttemptID != "attempt-a1" || current.AcceptResolution != "accepted" {
			t.Fatalf("abort dispatch result = %#v, want attempt retained", current)
		}
		current = applyLifecycle(t, fixture, lifecycleRequest(current, "reject-dispatch-operation", TriggerRejectBeforeDispatch, "attempt-a1", "fixture:reject-dispatch"))
		if current.Phase != PhaseRejected || current.AttemptID != "attempt-a1" || current.AcceptResolution != "accepted" {
			t.Fatalf("reject after abort dispatch result = %#v, want retained attempt", current)
		}
	})
	t.Run("abort_before_issue", func(t *testing.T) {
		fixture := newLifecycleFixture(t, "lifecycle-abort-before-issue")
		assertLifecycleAbortCleanupPath(t, fixture, lifecycleToEffectPending(t, fixture), TriggerAbortBeforeIssue)
	})
	t.Run("abort_before_start", func(t *testing.T) {
		fixture := newLifecycleFixture(t, "lifecycle-abort-before-start")
		assertLifecycleAbortCleanupPath(t, fixture, lifecycleToTokenIssued(t, fixture), TriggerAbortBeforeStart)
	})
}

func lifecycleToEffectPending(t *testing.T, fixture lifecycleFixture) Snapshot {
	t.Helper()
	current := prepareLifecycle(t, fixture, "prepare-operation")
	current = applyLifecycle(t, fixture, lifecycleRequest(current, "accept-operation", TriggerAccept, "", "fixture:accept"))
	current = applyLifecycle(t, fixture, lifecycleRequest(current, "dispatch-operation", TriggerBeginDispatch, "attempt-a1", "fixture:dispatch"))
	current = applyLifecycle(t, fixture, lifecycleRequest(current, "confirm-operation", TriggerConfirmDispatch, "attempt-a1", "fixture:confirm"))
	return applyLifecycle(t, fixture, lifecycleRequest(current, "redeem-operation", TriggerRedeemEffect, "attempt-a1", "fixture:redeem"))
}

func assertLifecycleAbortCleanupPath(t *testing.T, fixture lifecycleFixture, current Snapshot, abort Trigger) {
	t.Helper()
	current = applyLifecycle(t, fixture, lifecycleRequest(current, "abort-operation", abort, "attempt-a1", "fixture:"+string(abort)))
	if current.Phase != PhaseAbortedBeforeIssue && current.Phase != PhaseAbortedBeforeStart {
		t.Fatalf("abort %s phase = %s", abort, current.Phase)
	}
	for _, trigger := range []Trigger{TriggerRejectBeforeDispatch, TriggerFinishCleanup} {
		request := lifecycleRequest(current, "premature-"+string(trigger), trigger, "attempt-a1", "fixture:premature")
		fixture.verifier.expectEvidence(request.Trigger, request.EvidenceRef)
		if _, err := fixture.coordinator.Apply(t.Context(), request); err == nil {
			t.Fatalf("abort phase %s accepted premature %s", current.Phase, trigger)
		}
	}
	current = applyLifecycle(t, fixture, lifecycleRequest(current, "begin-cleanup", TriggerBeginCleanup, "attempt-a1", "fixture:begin-cleanup"))
	if current.Phase != PhaseCleanupPending {
		t.Fatalf("begin cleanup phase = %s, want %s", current.Phase, PhaseCleanupPending)
	}
	current = applyLifecycle(t, fixture, lifecycleRequest(current, "finish-cleanup", TriggerFinishCleanup, "attempt-a1", "fixture:finish-cleanup"))
	if current.Phase != PhaseRejected || current.AcceptResolution != "accepted" || current.UncertainPhase != "" {
		t.Fatalf("finished cleanup = %#v, want accepted operation rejected after cleanup", current)
	}
}

func testLifecycleConcurrentDifferentOperations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lifecycle-concurrent-different.sqlite")
	dbOne := openCommandIntentTestDBAtPath(t, path)
	dbTwo := openCommandIntentTestDBAtPath(t, path)
	fixtureOne := newLifecycleFixtureWithDB(t, dbOne, "lifecycle-concurrent-different", true)
	fixtureTwo := newLifecycleFixtureWithDB(t, dbTwo, "lifecycle-concurrent-different", false)
	prepareLifecycle(t, fixtureOne, "prepare-operation")
	current, err := fixtureOne.store.GetState(t.Context(), fixtureOne.intent.Intent.CommandID)
	if err != nil {
		t.Fatalf("GetState before concurrent apply error = %v", err)
	}
	fixtureOne.verifier.expectEvidence(TriggerAccept, "fixture:accept-one")
	fixtureTwo.verifier.expectEvidence(TriggerAccept, "fixture:accept-two")
	start := make(chan struct{})
	results := make(chan lifecycleApplyResult, 2)
	var workers sync.WaitGroup
	workers.Go(func() {
		<-start
		snapshot, applyErr := fixtureOne.coordinator.Apply(t.Context(), lifecycleRequest(current, "concurrent-operation-one", TriggerAccept, "", "fixture:accept-one"))
		results <- lifecycleApplyResult{snapshot: snapshot, err: applyErr}
	})
	workers.Go(func() {
		<-start
		snapshot, applyErr := fixtureTwo.coordinator.Apply(t.Context(), lifecycleRequest(current, "concurrent-operation-two", TriggerAccept, "", "fixture:accept-two"))
		results <- lifecycleApplyResult{snapshot: snapshot, err: applyErr}
	})
	close(start)
	workers.Wait()
	close(results)
	var winner Snapshot
	var failures []error
	for result := range results {
		if result.err == nil {
			winner = result.snapshot
			continue
		}
		failures = append(failures, result.err)
	}
	if winner.Version != 2 || winner.Phase != PhaseAccepted || len(failures) != 1 || !errors.Is(failures[0], ErrTransitionConflict) {
		t.Fatalf("different-operation concurrent results winner=%#v failures=%v", winner, failures)
	}
	assertLifecycleStateUnchanged(t, fixtureOne, winner)
}

func testLifecycleConcurrentSameOperation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lifecycle-concurrent-same.sqlite")
	dbOne := openCommandIntentTestDBAtPath(t, path)
	dbTwo := openCommandIntentTestDBAtPath(t, path)
	fixtureOne := newLifecycleFixtureWithDB(t, dbOne, "lifecycle-concurrent-same", true)
	fixtureTwo := newLifecycleFixtureWithDB(t, dbTwo, "lifecycle-concurrent-same", false)
	prepareLifecycle(t, fixtureOne, "prepare-operation")
	current, err := fixtureOne.store.GetState(t.Context(), fixtureOne.intent.Intent.CommandID)
	if err != nil {
		t.Fatalf("GetState before same-operation concurrent apply error = %v", err)
	}
	request := lifecycleRequest(current, "concurrent-same-operation", TriggerAccept, "", "fixture:accept")
	fixtureOne.verifier.expectEvidence(request.Trigger, request.EvidenceRef)
	fixtureTwo.verifier.expectEvidence(request.Trigger, request.EvidenceRef)
	start := make(chan struct{})
	results := make(chan lifecycleApplyResult, 2)
	var workers sync.WaitGroup
	workers.Go(func() {
		<-start
		snapshot, applyErr := fixtureOne.coordinator.Apply(t.Context(), request)
		results <- lifecycleApplyResult{snapshot: snapshot, err: applyErr}
	})
	workers.Go(func() {
		<-start
		snapshot, applyErr := fixtureTwo.coordinator.Apply(t.Context(), request)
		results <- lifecycleApplyResult{snapshot: snapshot, err: applyErr}
	})
	close(start)
	workers.Wait()
	close(results)
	var snapshots []Snapshot
	for result := range results {
		if result.err != nil {
			t.Fatalf("same-operation concurrent Apply error = %v", result.err)
		}
		snapshots = append(snapshots, result.snapshot)
	}
	if len(snapshots) != 2 || !reflect.DeepEqual(snapshots[0], snapshots[1]) {
		t.Fatalf("same-operation concurrent snapshots = %#v, want one historical result", snapshots)
	}
	assertLifecyclePhase(t, snapshots[0], PhaseAccepted, 2, "")
	latest := applyLifecycle(t, fixtureOne, lifecycleRequest(snapshots[0], "advance-operation", TriggerBeginDispatch, "attempt-a1", "fixture:dispatch"))
	startReplay := make(chan struct{})
	replays := make(chan lifecycleApplyResult, 2)
	var replayWorkers sync.WaitGroup
	replayWorkers.Go(func() {
		<-startReplay
		replay, replayErr := fixtureOne.coordinator.Apply(t.Context(), request)
		replays <- lifecycleApplyResult{snapshot: replay, err: replayErr}
	})
	replayWorkers.Go(func() {
		<-startReplay
		replay, replayErr := fixtureTwo.coordinator.Apply(t.Context(), request)
		replays <- lifecycleApplyResult{snapshot: replay, err: replayErr}
	})
	close(startReplay)
	replayWorkers.Wait()
	close(replays)
	for replay := range replays {
		if replay.err != nil || !reflect.DeepEqual(replay.snapshot, snapshots[0]) {
			t.Fatalf("concurrent historical replay = %#v/%v, want original %#v", replay.snapshot, replay.err, snapshots[0])
		}
	}
	assertLifecycleStateUnchanged(t, fixtureOne, latest)
}

func testLifecycleCloseReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lifecycle-reopen.sqlite")
	db := openCommandIntentTestDBAtPath(t, path)
	fixture := newLifecycleFixtureWithDB(t, db, "lifecycle-reopen", true)
	prepared := prepareLifecycle(t, fixture, "prepare-operation")
	accepted := applyLifecycle(t, fixture, lifecycleRequest(prepared, "accept-operation", TriggerAccept, "", "fixture:accept"))
	if err := db.Close(); err != nil {
		t.Fatalf("close lifecycle database: %v", err)
	}
	reopenedDB := openCommandIntentTestDBAtPath(t, path)
	reopened := newLifecycleFixtureWithDB(t, reopenedDB, fixture.intent.Intent.CommandID, false)
	state, err := reopened.store.GetState(t.Context(), fixture.intent.Intent.CommandID)
	if err != nil {
		t.Fatalf("GetState after reopen error = %v", err)
	}
	if !reflect.DeepEqual(state, accepted) {
		t.Fatalf("reopened state = %#v, want %#v", state, accepted)
	}
	operation, err := reopened.store.GetOperation(t.Context(), fixture.intent.Intent.CommandID, "accept-operation")
	if err != nil {
		t.Fatalf("GetOperation after reopen error = %v", err)
	}
	if !reflect.DeepEqual(operation, accepted) {
		t.Fatalf("reopened operation = %#v, want %#v", operation, accepted)
	}
	history, err := reopened.store.History(t.Context(), fixture.intent.Intent.CommandID, 0, 200)
	if err != nil {
		t.Fatalf("History after reopen error = %v", err)
	}
	if len(history) != 2 || !reflect.DeepEqual(history[0], prepared) || !reflect.DeepEqual(history[1], accepted) {
		t.Fatalf("reopened history = %#v, want prepare and accept", history)
	}
}

func testLifecycleHistoryPagingAndLimits(t *testing.T) {
	fixture := newLifecycleFixture(t, "lifecycle-history")
	prepared := prepareLifecycle(t, fixture, "prepare-operation")
	accepted := applyLifecycle(t, fixture, lifecycleRequest(prepared, "accept-operation", TriggerAccept, "", "fixture:accept"))
	dispatched := applyLifecycle(t, fixture, lifecycleRequest(accepted, "dispatch-operation", TriggerBeginDispatch, "attempt-a1", "fixture:dispatch"))
	assertLifecycleHistoryRejectsInvalidRanges(t, fixture, dispatched)
	assertLifecycleHistoryPages(t, fixture, prepared, accepted, dispatched)
}

func assertLifecycleHistoryRejectsInvalidRanges(t *testing.T, fixture lifecycleFixture, current Snapshot) {
	t.Helper()
	checks := []struct {
		after int64
		limit int
	}{
		{-1, 1},
		{0, 0},
		{0, 201},
		{current.Version + 1, 1},
	}
	for _, check := range checks {
		if _, err := fixture.store.History(t.Context(), fixture.intent.Intent.CommandID, check.after, check.limit); err == nil {
			t.Fatalf("History(after=%d, limit=%d) unexpectedly succeeded", check.after, check.limit)
		}
	}
}

func assertLifecycleHistoryPages(t *testing.T, fixture lifecycleFixture, prepared, accepted, dispatched Snapshot) {
	t.Helper()
	first, err := fixture.store.History(t.Context(), fixture.intent.Intent.CommandID, 0, 1)
	if err != nil {
		t.Fatalf("History first page error = %v", err)
	}
	if len(first) != 1 || !reflect.DeepEqual(first[0], prepared) {
		t.Fatalf("History first page = %#v, want prepare", first)
	}
	second, err := fixture.store.History(t.Context(), fixture.intent.Intent.CommandID, 1, 1)
	if err != nil {
		t.Fatalf("History second page error = %v", err)
	}
	if len(second) != 1 || !reflect.DeepEqual(second[0], accepted) {
		t.Fatalf("History second page = %#v, want accept", second)
	}
	all, err := fixture.store.History(t.Context(), fixture.intent.Intent.CommandID, 0, 200)
	if err != nil {
		t.Fatalf("History full page error = %v", err)
	}
	if len(all) != 3 || !reflect.DeepEqual(all[2], dispatched) {
		t.Fatalf("History full page = %#v, want three ordered snapshots", all)
	}
}

func lifecycleTriggerUniverse() []Trigger {
	triggers := make([]Trigger, 0)
	seen := make(map[Trigger]bool)
	for _, rule := range phaseTransitions() {
		if !seen[rule.Trigger] {
			seen[rule.Trigger] = true
			triggers = append(triggers, rule.Trigger)
		}
	}
	triggers = append(triggers, Trigger("unknown-trigger"))
	return triggers
}

package orchestrationcommand

import (
	"errors"
	"strings"
	"testing"
)

// TestCommandLifecycleReadIntegrity 拒绝 DDL 形状合法但摘要或直接前驱不一致的真实数据库记录。
func TestCommandLifecycleReadIntegrity(t *testing.T) {
	t.Run("impossible_predecessor", func(t *testing.T) {
		fixture := newLifecycleFixture(t, "corrupt-predecessor")
		current := prepareLifecycle(t, fixture, "prepare")
		current = applyLifecycle(t, fixture, lifecycleRequest(current, "accept", TriggerAccept, "", "fixture:accept"))
		request := lifecycleRequest(current, "forged-completion", TriggerReconcileComplete, "forged-attempt", "fixture:forged")
		insertLifecycleReadCorruption(t, fixture, request, PhaseCompleted, "accepted", "", "")
		assertLifecycleReadsRejectCorruption(t, fixture, request.OperationID)
	})
	t.Run("wrong_digest", func(t *testing.T) {
		fixture := newLifecycleFixture(t, "corrupt-digest")
		current := prepareLifecycle(t, fixture, "prepare")
		request := lifecycleRequest(current, "forged-accept", TriggerAccept, "", "fixture:forged")
		insertLifecycleReadCorruption(t, fixture, request, PhaseAccepted, "accepted", "", strings.Repeat("a", 64))
		assertLifecycleReadsRejectCorruption(t, fixture, request.OperationID)
	})
	t.Run("wrong_resolution", func(t *testing.T) {
		fixture := newLifecycleFixture(t, "corrupt-resolution")
		current := prepareLifecycle(t, fixture, "prepare")
		request := lifecycleRequest(current, "forged-abort", TriggerAbortAccept, "", "fixture:forged")
		insertLifecycleReadCorruption(t, fixture, request, PhaseRejected, "accepted", "", "")
		assertLifecycleReadsRejectCorruption(t, fixture, request.OperationID)
	})
	t.Run("wrong_uncertain_origin", func(t *testing.T) {
		fixture := newLifecycleFixture(t, "corrupt-uncertainty")
		current := lifecycleToTokenIssued(t, fixture)
		request := lifecycleRequest(current, "forged-unknown", TriggerMarkUnknown, current.AttemptID, "fixture:forged")
		insertLifecycleReadCorruption(t, fixture, request, PhaseUnknown, "accepted", PhaseEffectStarted, "")
		assertLifecycleReadsRejectCorruption(t, fixture, request.OperationID)
	})
}

func insertLifecycleReadCorruption(t *testing.T, fixture lifecycleFixture, request TransitionRequest, phase Phase, resolution string, uncertain Phase, digestOverride string) {
	t.Helper()
	digest, err := transitionDigest(request)
	if err != nil {
		t.Fatal(err)
	}
	if digestOverride != "" {
		if digestOverride == digest {
			t.Fatal("digest corruption fixture must differ from the correct digest")
		}
		digest = digestOverride
	}
	_, err = fixture.db.ExecContext(t.Context(), `INSERT INTO orchestration_command_journal (
		command_id, command_version, version, operation_id, operation_digest, phase,
		accept_resolution, attempt_id, trigger, evidence_ref, uncertain_phase, schema_version
	) VALUES (?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`, request.CommandID, request.ExpectedVersion+1,
		request.OperationID, digest, string(phase), resolution, request.AttemptID, string(request.Trigger), request.EvidenceRef, string(uncertain))
	if err != nil {
		t.Fatalf("corruption fixture must pass DDL before exercising read guard: %v", err)
	}
}

func assertLifecycleReadsRejectCorruption(t *testing.T, fixture lifecycleFixture, operationID string) {
	t.Helper()
	commandID := fixture.intent.Intent.CommandID
	_, stateErr := fixture.store.GetState(t.Context(), commandID)
	_, operationErr := fixture.store.GetOperation(t.Context(), commandID, operationID)
	_, historyErr := fixture.store.History(t.Context(), commandID, 0, 200)
	for name, err := range map[string]error{"state": stateErr, "operation": operationErr, "history": historyErr} {
		if !errors.Is(err, ErrInvalidTransition) && !errors.Is(err, ErrTransitionConflict) {
			t.Fatalf("%s corruption read error = %v, want explicit transition rejection", name, err)
		}
	}
}

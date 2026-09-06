package orchestrationcommand

import (
	"context"
	"errors"
	"testing"
)

func TestUnavailableReceiptVerifierFailsClosed(t *testing.T) {
	verifier := NewUnavailableReceiptVerifier()
	if verifier == nil {
		t.Fatal("NewUnavailableReceiptVerifier() returned nil")
	}
	if err := verifier.VerifyCommandTransition(nil, Verification{}); !errors.Is(err, ErrContextRequired) {
		t.Fatalf("nil context error = %v, want ErrContextRequired", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := verifier.VerifyCommandTransition(canceled, Verification{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context error = %v, want context.Canceled", err)
	}
	if err := verifier.VerifyCommandTransition(context.Background(), Verification{}); !errors.Is(err, ErrReceiptVerifierUnavailable) {
		t.Fatalf("available context error = %v, want ErrReceiptVerifierUnavailable", err)
	}
}

func TestUnavailableReceiptVerifierLeavesLifecycleUnchanged(t *testing.T) {
	db := openCommandIntentTestDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	intent, err := store.Reserve(t.Context(), commandIntentFixture("unavailable-verifier"))
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	coordinator, err := NewCoordinator(store, NewUnavailableReceiptVerifier())
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	prepared, err := coordinator.Prepare(t.Context(), intent.Intent.CommandID, "prepare-unavailable")
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	request := TransitionRequest{
		CommandID: intent.Intent.CommandID, OperationID: "accept-unavailable", ExpectedVersion: prepared.Version,
		Trigger: TriggerAccept, EvidenceRef: "receipt://unavailable",
	}
	if _, err := coordinator.Apply(t.Context(), request); !errors.Is(err, ErrEvidenceRejected) || !errors.Is(err, ErrReceiptVerifierUnavailable) {
		t.Fatalf("Apply() error = %v, want ErrEvidenceRejected and ErrReceiptVerifierUnavailable", err)
	}
	assertLifecycleUnchangedAfterUnavailable(t, store, intent.Intent.CommandID, prepared)
}

func assertLifecycleUnchangedAfterUnavailable(t *testing.T, store *Store, commandID string, prepared Snapshot) {
	t.Helper()
	after, err := store.GetState(t.Context(), commandID)
	if err != nil {
		t.Fatalf("GetState() error = %v", err)
	}
	if after.Version != prepared.Version || after.Phase != PhaseCustodyPending {
		t.Fatalf("state after rejected Apply = %#v, want unchanged custody-pending version %d", after, prepared.Version)
	}
	history, err := store.History(t.Context(), commandID, 0, 10)
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(history) != 1 || history[0] != prepared {
		t.Fatalf("history after rejected Apply = %#v, want only prepare snapshot %#v", history, prepared)
	}
}

package orchestrationcommand

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

type lifecycleFixture struct {
	db          *sql.DB
	store       *Store
	coordinator *Coordinator
	verifier    *lifecycleVerifier
	intent      Record
}

type lifecycleApplyResult struct {
	snapshot Snapshot
	err      error
}

// lifecycleVerifier 是测试注入的显式回执端口，不把非空 evidence_ref 当成真实证明。
type lifecycleVerifier struct {
	mu               sync.Mutex
	intent           Record
	err              error
	calls            []Verification
	expectedEvidence map[Trigger]string
}

func (v *lifecycleVerifier) VerifyCommandTransition(ctx context.Context, verification Verification) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.calls = append(v.calls, verification)
	if !reflect.DeepEqual(verification.Intent, v.intent) {
		return errors.New("fixture verifier received an unrelated intent")
	}
	request := verification.Request
	if request.CommandID != v.intent.Intent.CommandID || verification.Current.CommandID != request.CommandID {
		return errors.New("fixture verifier command binding mismatch")
	}
	if verification.Current.Version != request.ExpectedVersion {
		return errors.New("fixture verifier version binding mismatch")
	}
	wantNext, err := nextCommandPhase(ctx, verification.Current.Phase, request.Trigger)
	if err != nil {
		return fmt.Errorf("fixture verifier transition is invalid: %w", err)
	}
	if verification.NextPhase != wantNext {
		return errors.New("fixture verifier next phase mismatch")
	}
	wantEvidence, ok := v.expectedEvidence[request.Trigger]
	if !ok || request.EvidenceRef != wantEvidence {
		return errors.New("fixture verifier rejected unregistered evidence reference")
	}
	return v.err
}

func (v *lifecycleVerifier) callCount() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.calls)
}

func (v *lifecycleVerifier) setError(err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.err = err
}

func (v *lifecycleVerifier) expectEvidence(trigger Trigger, evidence string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.expectedEvidence == nil {
		v.expectedEvidence = make(map[Trigger]string)
	}
	v.expectedEvidence[trigger] = evidence
}

func newLifecycleFixture(t *testing.T, commandID string) lifecycleFixture {
	t.Helper()
	db := openCommandIntentTestDB(t)
	return newLifecycleFixtureWithDB(t, db, commandID, true)
}

func newLifecycleFixtureWithDB(t *testing.T, db *sql.DB, commandID string, reserve bool) lifecycleFixture {
	t.Helper()
	store, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	var intent Record
	if reserve {
		intent, err = store.Reserve(t.Context(), commandIntentFixture(commandID))
	} else {
		intent, err = store.Get(t.Context(), commandID)
	}
	if err != nil {
		t.Fatalf("load lifecycle intent %q error = %v", commandID, err)
	}
	verifier := &lifecycleVerifier{intent: intent, expectedEvidence: make(map[Trigger]string)}
	coordinator, err := NewCoordinator(store, verifier)
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	return lifecycleFixture{db: db, store: store, coordinator: coordinator, verifier: verifier, intent: intent}
}

func lifecycleRequest(current Snapshot, operationID string, trigger Trigger, attemptID, evidence string) TransitionRequest {
	return TransitionRequest{
		CommandID: current.CommandID, OperationID: operationID, ExpectedVersion: current.Version,
		Trigger: trigger, AttemptID: attemptID, EvidenceRef: evidence,
	}
}

func prepareLifecycle(t *testing.T, fixture lifecycleFixture, operationID string) Snapshot {
	t.Helper()
	snapshot, err := fixture.coordinator.Prepare(t.Context(), fixture.intent.Intent.CommandID, operationID)
	if err != nil {
		t.Fatalf("Prepare(%q) error = %v", operationID, err)
	}
	return snapshot
}

func applyLifecycle(t *testing.T, fixture lifecycleFixture, request TransitionRequest) Snapshot {
	t.Helper()
	fixture.verifier.expectEvidence(request.Trigger, request.EvidenceRef)
	snapshot, err := fixture.coordinator.Apply(t.Context(), request)
	if err != nil {
		t.Fatalf("Apply(%s/%s) error = %v", request.OperationID, request.Trigger, err)
	}
	return snapshot
}

func expectLifecycleError(t *testing.T, fixture lifecycleFixture, request TransitionRequest, sentinel error) {
	t.Helper()
	if _, err := fixture.coordinator.Apply(t.Context(), request); !errors.Is(err, sentinel) {
		t.Fatalf("Apply(%s/%s) error = %v, want %v", request.OperationID, request.Trigger, err, sentinel)
	}
}

func assertLifecyclePhase(t *testing.T, snapshot Snapshot, phase Phase, version int64, attempt string) {
	t.Helper()
	if snapshot.Phase != phase || snapshot.Version != version || snapshot.AttemptID != attempt {
		t.Fatalf("snapshot phase/version/attempt = %s/%d/%q, want %s/%d/%q", snapshot.Phase, snapshot.Version, snapshot.AttemptID, phase, version, attempt)
	}
	if snapshot.CommandVersion != 1 || snapshot.SchemaVersion != 1 || snapshot.RecordedAt.IsZero() {
		t.Fatalf("snapshot versions/time = %d/%d/%s, want 1/1/non-zero", snapshot.CommandVersion, snapshot.SchemaVersion, snapshot.RecordedAt)
	}
}

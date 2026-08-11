package remoteci

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

type cleanupProofObservation struct {
	absent bool
	err    error
}

type cleanupProofState struct {
	observations []cleanupProofObservation
	sticky       *cleanupProofObservation
}

type cleanupProofRuntime struct {
	*coordinatorRuntime
	mu             sync.Mutex
	deleteCalls    []string
	confirmCalls   []string
	deleteErrors   map[string]error
	confirmByGroup map[string]cleanupProofState
}

func (runtime *cleanupProofRuntime) DeleteContainerGroup(ctx context.Context, groupID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	runtime.mu.Lock()
	runtime.deleteCalls = append(runtime.deleteCalls, groupID)
	err := runtime.deleteErrors[groupID]
	runtime.mu.Unlock()
	return err
}

func (runtime *cleanupProofRuntime) ConfirmContainerGroupAbsent(ctx context.Context, groupID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.confirmCalls = append(runtime.confirmCalls, groupID)
	state := runtime.confirmByGroup[groupID]
	if state.sticky != nil {
		return state.sticky.absent, state.sticky.err
	}
	if len(state.observations) == 0 {
		return true, nil
	}
	observation := state.observations[0]
	state.observations = state.observations[1:]
	runtime.confirmByGroup[groupID] = state
	return observation.absent, observation.err
}

type cleanupProofStore struct {
	*coordinatorStore
	mu              sync.Mutex
	deleteCalls     []string
	confirmCalls    []string
	deleteErrors    map[string]error
	confirmByPrefix map[string]cleanupProofState
}

func (store *cleanupProofStore) DeletePrefix(ctx context.Context, prefix string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store.mu.Lock()
	store.deleteCalls = append(store.deleteCalls, prefix)
	err := store.deleteErrors[prefix]
	store.mu.Unlock()
	if err != nil {
		return err
	}
	return store.coordinatorStore.DeletePrefix(ctx, prefix)
}

func (store *cleanupProofStore) ConfirmPrefixEmpty(ctx context.Context, prefix string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.confirmCalls = append(store.confirmCalls, prefix)
	state := store.confirmByPrefix[prefix]
	if state.sticky != nil {
		return state.sticky.absent, state.sticky.err
	}
	if len(state.observations) == 0 {
		return true, nil
	}
	observation := state.observations[0]
	state.observations = state.observations[1:]
	store.confirmByPrefix[prefix] = state
	return observation.absent, observation.err
}

func newCleanupProofCoordinator(t *testing.T, store ObjectStore, runtime Runtime, timeout time.Duration) *Coordinator {
	t.Helper()
	coordinator := newTestCoordinator(t, store, runtime)
	coordinator.config.PollInterval = time.Millisecond
	coordinator.config.CleanupTimeout = timeout
	return coordinator
}

func runCleanupProof(t *testing.T, coordinator *Coordinator, groupIDs, objectKeys []string) (bool, error) {
	t.Helper()
	return finalizePreparedCleanup(t.TempDir(), func() error {
		return coordinator.cleanup("job-0123456789abcdef01234567", groupIDs, objectKeys)
	})
}

func TestCoordinatorCleanupRequiresECIAndOSSAbsenceProof(t *testing.T) {
	jobID := "job-0123456789abcdef01234567"
	prefix := "baseline-artifacts/source-bundles/" + jobID + "/"
	objectKeys := []string{prefix + "request.json"}
	tests := []struct {
		name    string
		store   *cleanupProofStore
		runtime *cleanupProofRuntime
	}{
		{
			name: "eci group remains after delete ack",
			store: &cleanupProofStore{
				coordinatorStore: &coordinatorStore{},
				confirmByPrefix:  map[string]cleanupProofState{},
			},
			runtime: &cleanupProofRuntime{
				coordinatorRuntime: &coordinatorRuntime{},
				confirmByGroup: map[string]cleanupProofState{
					"eci-1": {sticky: &cleanupProofObservation{absent: false}},
				},
			},
		},
		{
			name: "oss object remains after delete ack",
			store: &cleanupProofStore{
				coordinatorStore: &coordinatorStore{},
				confirmByPrefix: map[string]cleanupProofState{
					prefix: {sticky: &cleanupProofObservation{absent: false}},
				},
			},
			runtime: &cleanupProofRuntime{
				coordinatorRuntime: &coordinatorRuntime{},
				confirmByGroup:     map[string]cleanupProofState{},
			},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			coordinator := newCleanupProofCoordinator(t, testCase.store, testCase.runtime, 8*time.Millisecond)
			complete, err := runCleanupProof(t, coordinator, []string{"eci-1"}, objectKeys)
			if err == nil || complete {
				t.Fatalf("cleanup complete=%t err=%v, want false/non-nil", complete, err)
			}
			if !slices.Contains(testCase.runtime.deleteCalls, "eci-1") {
				t.Fatalf("ECI delete calls=%v, want eci-1", testCase.runtime.deleteCalls)
			}
			if !slices.Contains(testCase.store.deleteCalls, prefix) {
				t.Fatalf("OSS delete calls=%v, want %q", testCase.store.deleteCalls, prefix)
			}
		})
	}
}

func TestCoordinatorCleanupBecomesCompleteOnlyAfterBothProofsDisappear(t *testing.T) {
	jobID := "job-0123456789abcdef01234567"
	prefix := "baseline-artifacts/source-bundles/" + jobID + "/"
	store := &cleanupProofStore{
		coordinatorStore: &coordinatorStore{},
		confirmByPrefix: map[string]cleanupProofState{
			prefix: {observations: []cleanupProofObservation{{absent: false}, {absent: true}}},
		},
	}
	runtime := &cleanupProofRuntime{
		coordinatorRuntime: &coordinatorRuntime{},
		confirmByGroup: map[string]cleanupProofState{
			"eci-1": {observations: []cleanupProofObservation{{absent: false}, {absent: true}}},
		},
	}
	coordinator := newCleanupProofCoordinator(t, store, runtime, time.Second)
	complete, err := runCleanupProof(t, coordinator, []string{"eci-1"}, []string{prefix + "request.json"})
	if err != nil || !complete {
		t.Fatalf("cleanup complete=%t err=%v, want true/nil", complete, err)
	}
	if len(runtime.confirmCalls) < 2 || len(store.confirmCalls) < 2 {
		t.Fatalf("proof calls ECI=%v OSS=%v, want retries before disappearance", runtime.confirmCalls, store.confirmCalls)
	}
}

func TestCoordinatorCleanupPreservesErrorsAndAttemptsEveryTarget(t *testing.T) {
	jobID := "job-0123456789abcdef01234567"
	prefix := "baseline-artifacts/source-bundles/" + jobID + "/"
	storeErr := errors.New("OSS delete failed")
	runtimeErr := errors.New("ECI delete failed")
	store := &cleanupProofStore{
		coordinatorStore: &coordinatorStore{},
		deleteErrors:     map[string]error{prefix: storeErr},
		confirmByPrefix: map[string]cleanupProofState{
			prefix: {sticky: &cleanupProofObservation{absent: false}},
		},
	}
	runtime := &cleanupProofRuntime{
		coordinatorRuntime: &coordinatorRuntime{},
		deleteErrors:       map[string]error{"eci-1": runtimeErr},
		confirmByGroup: map[string]cleanupProofState{
			"eci-1": {sticky: &cleanupProofObservation{absent: false}},
			"eci-2": {observations: []cleanupProofObservation{{absent: true}}},
		},
	}
	coordinator := newCleanupProofCoordinator(t, store, runtime, 8*time.Millisecond)
	complete, err := runCleanupProof(t, coordinator, []string{"eci-1", "eci-2"}, []string{prefix + "request.json"})
	if err == nil || complete {
		t.Fatalf("cleanup complete=%t err=%v, want false/non-nil", complete, err)
	}
	if !slices.Contains(runtime.deleteCalls, "eci-1") || !slices.Contains(runtime.deleteCalls, "eci-2") {
		t.Fatalf("ECI delete calls=%v, want both targets", runtime.deleteCalls)
	}
	if !slices.Contains(store.deleteCalls, prefix) {
		t.Fatalf("OSS delete calls=%v, want %q", store.deleteCalls, prefix)
	}
	if !errors.Is(err, runtimeErr) || !errors.Is(err, storeErr) {
		t.Fatalf("cleanup error=%v, want both provider errors preserved", err)
	}
}

func TestCleanupProofRuntimeSatisfiesRuntime(t *testing.T) {
	var _ Runtime = (*cleanupProofRuntime)(nil)
	var _ ObjectStore = (*cleanupProofStore)(nil)
}

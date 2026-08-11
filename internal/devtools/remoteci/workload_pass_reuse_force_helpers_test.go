package remoteci

import (
	"context"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

type forceExecutionCase struct {
	input       RunInput
	identities  []gate.WorkloadPassIdentity
	store       *coordinatorStore
	runtime     *coordinatorRuntime
	coordinator *Coordinator
}

func TestCoordinatorRunForceExecutesAllWorkloadsWithoutDeletingPassEvidence(t *testing.T) {
	testCase := prepareForceExecutionCase(t)
	result := runForceExecutionCase(t, testCase)
	assertForceExecutionCase(t, testCase, result)
}

func prepareForceExecutionCase(t *testing.T) forceExecutionCase {
	t.Helper()
	_, input := coordinatorReuseFixture(t)
	seed := runCoordinatorFreshWorkloads(t, input)
	workerDigest, err := ResolveWorkerExecutionDigest(context.Background(), input.RepositoryRoot, input.Tree)
	if err != nil {
		t.Fatal(err)
	}
	input.WorkerExecutionSemanticDigest = workerDigest
	seedCoordinatorWorkloadPassEvidence(t, input, seed, nil)
	identities, err := remoteWorkloadPassIdentities(
		context.Background(), input, mustCoordinatorCatalog(t, input), 10*time.Minute, testRemoteResourcePolicy(),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertSeededPassEvidenceMatchesIdentities(t, input.LedgerStore, identities)
	input.Force = true
	store := &coordinatorStore{}
	runtime := &coordinatorRuntime{}
	coordinator := newTestCoordinator(t, store, runtime)
	coordinator.newID = func() (string, error) { return "job-0123456789abcdef01234572", nil }
	return forceExecutionCase{input: input, identities: identities, store: store, runtime: runtime, coordinator: coordinator}
}

func runForceExecutionCase(t *testing.T, testCase forceExecutionCase) RunResult {
	t.Helper()
	result, err := runCoordinatorTest(t, testCase.coordinator, context.Background(), testCase.input)
	if err != nil {
		t.Fatalf("force Run() error = %v", err)
	}
	return result
}

func assertForceExecutionCase(t *testing.T, testCase forceExecutionCase, result RunResult) {
	t.Helper()
	assertForceExecutionResult(t, result)
	assertForceExecutionSideEffects(t, testCase)
	assertForceExecutionPassEvidence(t, testCase.input, testCase.identities)
}

func assertForceExecutionResult(t *testing.T, result RunResult) {
	t.Helper()
	if !result.Force || len(result.ReusedWorkloads) != 0 ||
		len(result.CacheMissWorkloads) != len(result.WorkloadPassIdentities) ||
		len(result.FreshWorkloadExecutions) != len(result.WorkloadPassIdentities) {
		t.Fatalf("force Run() did not bypass every PASS: %#v", result)
	}
}

func assertForceExecutionSideEffects(t *testing.T, testCase forceExecutionCase) {
	t.Helper()
	if len(testCase.runtime.creates) == 0 || len(testCase.store.uploads) == 0 {
		t.Fatalf("force Run() did not execute remote misses: runtime=%#v store=%#v", testCase.runtime.creates, testCase.store.uploads)
	}
}

func assertForceExecutionPassEvidence(t *testing.T, input RunInput, identities []gate.WorkloadPassIdentity) {
	t.Helper()
	for _, identity := range identities {
		evidence, lookupErr := input.LedgerStore.LookupWorkloadPassEvidence([]gate.WorkloadPassIdentity{identity})
		if lookupErr != nil || len(evidence) != 1 {
			t.Fatalf("force Run() deleted prior PASS evidence for %q: evidence=%#v error=%v", identity.WorkloadID, evidence, lookupErr)
		}
	}
}

func assertSeededPassEvidenceMatchesIdentities(t *testing.T, store *gate.DurationLedgerStore, identities []gate.WorkloadPassIdentity) {
	t.Helper()
	for _, identity := range identities {
		evidence, err := store.LookupWorkloadPassEvidence([]gate.WorkloadPassIdentity{identity})
		if err != nil || len(evidence) != 1 {
			t.Fatalf("seeded PASS evidence does not match request identity %q: evidence=%#v error=%v", identity.WorkloadID, evidence, err)
		}
	}
}

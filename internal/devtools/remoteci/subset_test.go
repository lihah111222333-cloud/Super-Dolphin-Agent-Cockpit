package remoteci

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestPrepareSubsetAllHitReusesOnlySelectedWorkloads(t *testing.T) {
	_, input := coordinatorReuseFixture(t)
	seed := runCoordinatorFreshWorkloads(t, input)
	seedCoordinatorWorkloadPassEvidence(t, input, seed, func(index int) bool { return index == 0 })
	clearCoordinatorAllHitExecutionIdentity(&input)

	catalog := mustCoordinatorCatalog(t, input)
	selected, excluded := subsetFixtureIDs(t, catalog)
	store := &coordinatorStore{}
	runtime := &coordinatorRuntime{}
	coordinator := newTestCoordinator(t, store, runtime)
	coordinator.newID = func() (string, error) { return "job-0123456789abcdef01234568", nil }

	prepared := mustPrepareSubset(t, coordinator, input, RemoteSubsetRequest{Selected: selected, Excluded: excluded})
	if !prepared.AllReused() {
		t.Fatal("PrepareSubset() allReused = false, want selected PASS hit")
	}
	assertPreparedSubsetScope(t, prepared, selected, excluded)

	result := mustRunPrepared(t, coordinator, prepared)
	assertSubsetAllHitResult(t, result)
	assertCoordinatorNoRemoteSideEffects(t, store, runtime)
}

func TestPrepareSubsetPlansOnlyScopedMisses(t *testing.T) {
	_, input := coordinatorReuseFixture(t)
	catalog := mustCoordinatorCatalog(t, input)
	selected, excluded := subsetFixtureIDs(t, catalog)
	selected = append(selected, excluded[0])
	excluded = nil
	coordinator := newTestCoordinator(t, &coordinatorStore{}, &coordinatorRuntime{})

	prepared := mustPrepareSubset(t, coordinator, input, RemoteSubsetRequest{Selected: selected})
	_, misses := prepared.WorkloadReuseDecision()
	if len(misses) != len(selected) || strings.Join(gateIDsToStrings(misses), ",") != strings.Join(gateIDsToStrings(selected), ",") {
		t.Fatalf("PrepareSubset() misses = %v, want scope-only %v", misses, selected)
	}
	if len(prepared.executionCatalog.Workloads) != len(selected) {
		t.Fatalf("PrepareSubset() execution catalog = %#v, want only selected workloads", prepared.executionCatalog)
	}
	for _, workload := range prepared.executionCatalog.Workloads {
		if workload.ID != string(selected[0]) && workload.ID != string(selected[1]) {
			t.Fatalf("PrepareSubset() included unselected workload %q", workload.ID)
		}
	}
}

func TestPrepareSubsetMergesSelectedReuseAndFreshMissWithoutOwner(t *testing.T) {
	_, input := coordinatorReuseFixture(t)
	seed := runCoordinatorFreshWorkloads(t, input)
	seedCoordinatorWorkloadPassEvidence(t, input, seed, func(index int) bool { return index == 0 })
	catalog := mustCoordinatorCatalog(t, input)
	selected, _ := subsetFixtureIDs(t, catalog)
	selected = append(selected, gate.GateID(remoteShardableWorkloads(catalog)[1].ID))
	store := &coordinatorStore{}
	runtime := &coordinatorRuntime{}
	coordinator := newTestCoordinator(t, store, runtime)
	coordinator.newID = func() (string, error) { return "job-0123456789abcdef01234569", nil }

	prepared := mustPrepareSubset(t, coordinator, input, RemoteSubsetRequest{Selected: selected})
	if prepared.AllReused() {
		t.Fatal("PrepareSubset() allReused = true, want one selected MISS")
	}
	if err := bindPreparedMissExecutionForTest(context.Background(), coordinator, prepared, input); err != nil {
		t.Fatalf("BindPreparedMissExecution() error = %v", err)
	}
	result := mustRunPrepared(t, coordinator, prepared)
	if len(result.ReusedWorkloads) != 1 || len(result.FreshWorkloadExecutions) != 1 || len(result.WorkloadExecutions) != 2 {
		t.Fatalf("subset partial result has reused=%d fresh=%d workloads=%d, want 1/1/2", len(result.ReusedWorkloads), len(result.FreshWorkloadExecutions), len(result.WorkloadExecutions))
	}
	assertNoSubsetReleaseOwner(t, result)
	if len(runtime.creates) == 0 {
		t.Fatal("subset partial run did not create a shard for its selected MISS")
	}
}

func TestRemoteSubsetScopeRejectsOverlapAndUnknownExcluded(t *testing.T) {
	_, input := coordinatorReuseFixture(t)
	catalog := mustCoordinatorCatalog(t, input)
	selected, excluded := subsetFixtureIDs(t, catalog)
	if _, _, _, err := newRemoteSubsetScope(catalog, RemoteSubsetRequest{Selected: selected, Excluded: selected}); err == nil || !strings.Contains(err.Error(), "both selected and excluded") {
		t.Fatalf("newRemoteSubsetScope(overlap) error = %v, want overlap rejection", err)
	}
	if _, _, _, err := newRemoteSubsetScope(catalog, RemoteSubsetRequest{Selected: selected, Excluded: []gate.GateID{"unknown"}}); err == nil || !strings.Contains(err.Error(), "outside catalog") {
		t.Fatalf("newRemoteSubsetScope(unknown excluded) error = %v, want catalog rejection", err)
	}
	if _, _, _, err := newRemoteSubsetScope(catalog, RemoteSubsetRequest{Selected: []gate.GateID{"unknown"}}); err == nil || !strings.Contains(err.Error(), "outside catalog") {
		t.Fatalf("newRemoteSubsetScope(unknown selected) error = %v, want catalog rejection", err)
	}
	if len(excluded) != 1 {
		t.Fatalf("subset fixture excluded = %v, want one workload", excluded)
	}
}

// TestRemoteSubsetScopeCanonicalizesSchedulerAndExplicitSelections proves the
// common remote boundary accepts arbitrary scheduler/CLI order, then freezes
// both selected and excluded IDs in the full catalog's canonical order.
func TestRemoteSubsetScopeCanonicalizesSchedulerAndExplicitSelections(t *testing.T) {
	_, input := coordinatorReuseFixture(t)
	catalog := mustCoordinatorCatalog(t, input)
	workloads := remoteShardableWorkloads(catalog)
	if len(workloads) < 3 {
		t.Fatalf("catalog shardable workloads = %d, want at least three", len(workloads))
	}

	testCases := []struct {
		name     string
		selected []gate.GateID
		excluded []gate.GateID
	}{
		{
			name:     "auto overflow remote subset",
			selected: []gate.GateID{gate.GateID(workloads[2].ID), gate.GateID(workloads[0].ID)},
			excluded: []gate.GateID{gate.GateID(workloads[1].ID)},
		},
		{
			name:     "hybrid remote subset",
			selected: []gate.GateID{gate.GateID(workloads[1].ID), gate.GateID(workloads[0].ID)},
			excluded: []gate.GateID{gate.GateID(workloads[2].ID)},
		},
		{
			name:     "explicit gate workload subset",
			selected: []gate.GateID{gate.GateID(workloads[2].ID), gate.GateID(workloads[0].ID)},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			scope, executionCatalog, excluded, err := newRemoteSubsetScope(catalog, RemoteSubsetRequest{Selected: testCase.selected, Excluded: testCase.excluded})
			if err != nil {
				t.Fatalf("newRemoteSubsetScope() error = %v", err)
			}
			wantSelected, wantExcluded := catalogOrderedSubsetIDs(catalog, testCase.selected, testCase.excluded)
			if got := scope.SelectedGateIDs(); !slices.Equal(got, wantSelected) {
				t.Fatalf("scope selected IDs = %v, want canonical catalog order %v", got, wantSelected)
			}
			if !slices.Equal(excluded, wantExcluded) {
				t.Fatalf("excluded IDs = %v, want canonical catalog order %v", excluded, wantExcluded)
			}
			if got := catalogGateIDs(executionCatalog); !slices.Equal(got, wantSelected) {
				t.Fatalf("execution catalog IDs = %v, want exactly selected canonical subset %v", got, wantSelected)
			}
		})
	}
}

func catalogOrderedSubsetIDs(catalog gate.WorkloadCatalog, selected, excluded []gate.GateID) ([]gate.GateID, []gate.GateID) {
	selectedSet := make(map[gate.GateID]struct{}, len(selected))
	for _, gateID := range selected {
		selectedSet[gateID] = struct{}{}
	}
	excludedSet := make(map[gate.GateID]struct{}, len(excluded))
	for _, gateID := range excluded {
		excludedSet[gateID] = struct{}{}
	}
	orderedSelected := make([]gate.GateID, 0, len(selected))
	orderedExcluded := make([]gate.GateID, 0, len(excluded))
	for _, workload := range catalog.Workloads {
		gateID := gate.GateID(workload.ID)
		if _, ok := selectedSet[gateID]; ok {
			orderedSelected = append(orderedSelected, gateID)
		}
		if _, ok := excludedSet[gateID]; ok {
			orderedExcluded = append(orderedExcluded, gateID)
		}
	}
	return orderedSelected, orderedExcluded
}

func catalogGateIDs(catalog gate.WorkloadCatalog) []gate.GateID {
	ids := make([]gate.GateID, 0, len(catalog.Workloads))
	for _, workload := range catalog.Workloads {
		ids = append(ids, gate.GateID(workload.ID))
	}
	return ids
}

func mustPrepareSubset(
	t *testing.T,
	coordinator *Coordinator,
	input RunInput,
	request RemoteSubsetRequest,
) *PreparedRun {
	t.Helper()

	prepared, err := coordinator.PrepareSubset(context.Background(), input, request)
	if err != nil {
		t.Fatalf("PrepareSubset() error = %v", err)
	}
	return prepared
}

func mustRunPrepared(t *testing.T, coordinator *Coordinator, prepared *PreparedRun) RunResult {
	t.Helper()

	result, err := coordinator.RunPrepared(context.Background(), prepared)
	if err != nil {
		t.Fatalf("RunPrepared() error = %v", err)
	}
	return result
}

func assertPreparedSubsetScope(
	t *testing.T,
	prepared *PreparedRun,
	selected []gate.GateID,
	excluded []gate.GateID,
) {
	t.Helper()

	scope, frozenExcluded, err := prepared.RemoteExecutionScope()
	if err != nil {
		t.Fatalf("RemoteExecutionScope() error = %v", err)
	}
	if !scope.IsSubset() || len(scope.SelectedGateIDs()) != 1 || scope.SelectedGateIDs()[0] != selected[0] {
		t.Fatalf("RemoteExecutionScope() scope = %#v, want selected subset %v", scope, selected)
	}
	assertFrozenExcludedSubsetScope(t, prepared, selected, excluded, frozenExcluded)
}

func assertFrozenExcludedSubsetScope(
	t *testing.T,
	prepared *PreparedRun,
	selected []gate.GateID,
	excluded []gate.GateID,
	frozenExcluded []gate.GateID,
) {
	t.Helper()

	if len(frozenExcluded) != 1 || frozenExcluded[0] != excluded[0] {
		t.Fatalf("RemoteExecutionScope() excluded = %v, want %v", frozenExcluded, excluded)
	}
	frozenExcluded[0] = selected[0]
	_, frozenExcluded, err := prepared.RemoteExecutionScope()
	if err != nil || frozenExcluded[0] != excluded[0] {
		t.Fatalf("RemoteExecutionScope() did not defensively preserve excluded IDs: excluded=%v error=%v", frozenExcluded, err)
	}
}

func assertSubsetAllHitResult(t *testing.T, result RunResult) {
	t.Helper()

	if result.Scope == nil || !result.Scope.IsSubset() || len(result.WorkloadExecutions) != 1 || len(result.ReusedWorkloads) != 1 || len(result.Shards) != 0 {
		t.Fatalf("subset all-hit result = %#v, want exactly one reused workload and no remote shards", result)
	}
	assertNoSubsetReleaseOwner(t, result)
}

func assertNoSubsetReleaseOwner(t *testing.T, result RunResult) {
	t.Helper()

	for _, execution := range result.GateExecutions {
		if execution.GateID == gate.GateIDReleaseLayeredCheck {
			t.Fatal("subset run appended release owner attestation")
		}
	}
}

func subsetFixtureIDs(t *testing.T, catalog gate.WorkloadCatalog) ([]gate.GateID, []gate.GateID) {
	t.Helper()
	workloads := remoteShardableWorkloads(catalog)
	if len(workloads) < 2 {
		t.Fatalf("catalog shardable workloads = %d, want at least two", len(workloads))
	}
	return []gate.GateID{gate.GateID(workloads[0].ID)}, []gate.GateID{gate.GateID(workloads[1].ID)}
}

func gateIDsToStrings(ids []gate.GateID) []string {
	values := make([]string, len(ids))
	for index, id := range ids {
		values[index] = string(id)
	}
	return values
}

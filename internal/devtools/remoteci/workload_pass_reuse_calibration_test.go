package remoteci

import (
	"context"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestCoordinatorRunReusesCalibrationWorkloadPassesWithoutRemoteSideEffects(t *testing.T) {
	_, input := coordinatorReuseFixture(t)
	input.Calibration = true
	input.CalibrationResource = testRemoteResourcePolicy().CalibrationResource
	reloadRemotePlanningSnapshot(t, &input)
	seed := runCoordinatorFreshWorkloads(t, input)
	seedCoordinatorWorkloadPassEvidence(t, input, seed, nil)

	store := &coordinatorStore{}
	runtime := &coordinatorRuntime{}
	coordinator := newTestCoordinator(t, store, runtime)
	coordinator.newID = func() (string, error) { return "job-0123456789abcdef0123456c", nil }
	result, err := coordinator.Run(context.Background(), input)
	assertCoordinatorFullReuse(t, result, err, store, runtime)
}

// TestCoordinatorRunReusesReleaseCalibrationPassesAllHitWithOwner 验证 release calibration all-hit 保留 owner-only 证明。
func TestCoordinatorRunReusesReleaseCalibrationPassesAllHitWithOwner(t *testing.T) {
	input, catalog := releaseCalibrationInput(t)
	seedReleaseCalibrationPasses(t, input)
	result, store, runtime := runReleaseCalibrationAllHit(t, input)
	assertReleaseCalibrationAllHit(t, result, catalog, store, runtime)
	assertCoordinatorOwnerProjection(t, result, catalog)
}

func releaseCalibrationInput(t *testing.T) (RunInput, gate.WorkloadCatalog) {
	t.Helper()
	_, input := coordinatorReuseFixture(t)
	input.Calibration = true
	input.CalibrationResource = testRemoteResourcePolicy().CalibrationResource
	reloadRemotePlanningSnapshot(t, &input)
	input.Profile = gate.ProfileRelease
	input.Entrypoint = gate.CIEntrypointRelease
	input.Source = gate.SourceSpec{
		Kind: gate.SourceKindCommit, ObjectFormat: gate.GitObjectFormatSHA1,
		Commit: &gate.CommitSource{SHA: input.Commit}, SourceTreeSHA: input.Tree,
	}
	return input, mustCoordinatorCatalog(t, input)
}

func seedReleaseCalibrationPasses(t *testing.T, input RunInput) {
	t.Helper()
	seedCoordinator := newTestCoordinator(t, &coordinatorStore{}, &coordinatorRuntime{})
	seed, err := seedCoordinator.Run(context.Background(), input)
	if err != nil || seed.Status != gate.ResultStatusPassed || len(seed.FreshWorkloadExecutions) == 0 {
		t.Fatalf("fresh release calibration result=%#v error=%v", seed, err)
	}
	seedCoordinatorWorkloadPassEvidence(t, input, seed, nil)
}

func runReleaseCalibrationAllHit(t *testing.T, input RunInput) (RunResult, *coordinatorStore, *coordinatorRuntime) {
	t.Helper()
	store := &coordinatorStore{}
	runtime := &coordinatorRuntime{}
	coordinator := newTestCoordinator(t, store, runtime)
	coordinator.newID = func() (string, error) { return "job-0123456789abcdef01234571", nil }
	result, err := coordinator.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("release calibration all-hit Run() error = %v", err)
	}
	return result, store, runtime
}

func assertReleaseCalibrationAllHit(t *testing.T, result RunResult, catalog gate.WorkloadCatalog, store *coordinatorStore, runtime *coordinatorRuntime) {
	t.Helper()
	shardableCount := len(remoteShardableWorkloads(catalog))
	if result.Status != gate.ResultStatusPassed || len(result.ReusedWorkloads) != shardableCount || len(result.CacheMissWorkloads) != 0 || len(result.FreshWorkloadExecutions) != 0 {
		t.Fatalf("release calibration all-hit reuse result = %#v", result)
	}
	if len(result.WorkloadExecutions) != shardableCount || len(runtime.creates) != 0 || len(store.uploads) != 0 {
		t.Fatalf("release calibration all-hit executed shardable work: %#v", result)
	}
}

func TestCoordinatorRunReusesCalibrationPassesAcrossProfilesWithCanonicalTimeout(t *testing.T) {
	_, input := coordinatorReuseFixture(t)
	input.Calibration = true
	input.CalibrationResource = testRemoteResourcePolicy().CalibrationResource
	reloadRemotePlanningSnapshot(t, &input)
	seedCatalog := mustCoordinatorCatalog(t, input)
	seed := runCoordinatorFreshWorkloads(t, input)
	seedCoordinatorWorkloadPassEvidence(t, input, seed, nil)

	releaseInput := input
	releaseInput.Profile = gate.ProfileRelease
	releaseInput.Entrypoint = gate.CIEntrypointRelease
	releaseInput.Source = gate.SourceSpec{
		Kind: gate.SourceKindCommit, ObjectFormat: gate.GitObjectFormatSHA1,
		Commit: &gate.CommitSource{SHA: releaseInput.Commit}, SourceTreeSHA: releaseInput.Tree,
	}
	releaseCatalog := mustCoordinatorCatalog(t, releaseInput)
	seedIDs := make(map[string]struct{})
	for _, workload := range remoteShardableWorkloads(seedCatalog) {
		seedIDs[workload.ID] = struct{}{}
	}
	wantReused := 0
	for _, workload := range remoteShardableWorkloads(releaseCatalog) {
		if _, ok := seedIDs[workload.ID]; ok {
			wantReused++
		}
	}
	if wantReused == 0 {
		t.Fatal("release calibration catalog has no local-fast workload intersection")
	}

	store := &coordinatorStore{}
	runtime := &coordinatorRuntime{}
	coordinator := newTestCoordinator(t, store, runtime)
	coordinator.newID = func() (string, error) { return "job-0123456789abcdef01234570", nil }
	result, err := coordinator.Run(context.Background(), releaseInput)
	if err != nil {
		t.Fatalf("release calibration Run() error = %v", err)
	}
	if len(result.ReusedWorkloads) != wantReused {
		t.Fatalf("release calibration reused %d workloads, want %d: %#v", len(result.ReusedWorkloads), wantReused, result)
	}
	if len(result.FreshWorkloadExecutions) != len(result.CacheMissWorkloads) || len(runtime.creates) == 0 {
		t.Fatalf("release calibration did not execute only catalog misses: %#v", result)
	}
	assertCoordinatorOwnerProjection(t, result, releaseCatalog)
}

func TestCoordinatorRunReusesCalibrationPassesForNormal(t *testing.T) {
	_, input := coordinatorReuseFixture(t)
	input.Calibration = true
	input.CalibrationResource = testRemoteResourcePolicy().CalibrationResource
	reloadRemotePlanningSnapshot(t, &input)
	seed := runCoordinatorFreshWorkloads(t, input)
	seedCoordinatorWorkloadPassEvidence(t, input, seed, nil)

	input.Calibration = false
	reloadRemotePlanningSnapshot(t, &input)
	store := &coordinatorStore{}
	runtime := &coordinatorRuntime{}
	coordinator := newTestCoordinator(t, store, runtime)
	coordinator.newID = func() (string, error) { return "job-0123456789abcdef0123456e", nil }
	result, err := coordinator.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("normal Run() after calibration PASS error = %v", err)
	}
	if len(result.ReusedWorkloads) != len(result.WorkloadPassIdentities) || len(result.FreshWorkloadExecutions) != 0 {
		t.Fatalf("normal Run() did not reuse calibration PASS evidence: %#v", result)
	}
	if len(result.CacheMissWorkloads) != 0 || len(runtime.creates) != 0 {
		t.Fatalf("normal Run() executed calibration-to-normal PASS misses: %#v", result)
	}
}

func TestCoordinatorRunExecutesOnlyCalibrationWorkloadPassMisses(t *testing.T) {
	_, input := coordinatorReuseFixture(t)
	input.Calibration = true
	input.CalibrationResource = testRemoteResourcePolicy().CalibrationResource
	reloadRemotePlanningSnapshot(t, &input)
	seed := runCoordinatorFreshWorkloads(t, input)
	seedCoordinatorWorkloadPassEvidence(t, input, seed, func(index int) bool { return index%2 == 0 })

	store := &coordinatorStore{}
	runtime := &coordinatorRuntime{}
	coordinator := newTestCoordinator(t, store, runtime)
	coordinator.newID = func() (string, error) { return "job-0123456789abcdef0123456d", nil }
	result, err := coordinator.Run(context.Background(), input)
	assertCoordinatorPartialReuse(t, result, err, runtime)
}

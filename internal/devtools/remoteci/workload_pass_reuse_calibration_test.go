package remoteci

import (
	"context"
	"strings"
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
	seedCalibrationDurationIndex(t, &input, mustCoordinatorCatalog(t, input))
	clearCoordinatorAllHitExecutionIdentity(&input)

	store := &coordinatorStore{}
	runtime := &coordinatorRuntime{}
	coordinator := newTestCoordinator(t, store, runtime)
	coordinator.newID = func() (string, error) { return "job-0123456789abcdef0123456c", nil }
	result, err := runCoordinatorTest(t, coordinator, context.Background(), input)
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
	seed, err := runCoordinatorTest(t, seedCoordinator, context.Background(), input)
	if err != nil || seed.Status != gate.ResultStatusPassed || len(seed.FreshWorkloadExecutions) == 0 {
		t.Fatalf("fresh release calibration result=%#v error=%v", seed, err)
	}
	seedCoordinatorWorkloadPassEvidence(t, input, seed, nil)
}

func runReleaseCalibrationAllHit(t *testing.T, input RunInput) (RunResult, *coordinatorStore, *coordinatorRuntime) {
	t.Helper()
	seedCalibrationDurationIndex(t, &input, mustCoordinatorCatalog(t, input))
	clearCoordinatorAllHitExecutionIdentity(&input)
	store := &coordinatorStore{}
	runtime := &coordinatorRuntime{}
	coordinator := newTestCoordinator(t, store, runtime)
	coordinator.newID = func() (string, error) { return "job-0123456789abcdef01234571", nil }
	result, err := runCoordinatorTest(t, coordinator, context.Background(), input)
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
	seedCalibrationDurationIndex(t, &releaseInput, releaseCatalog)
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
	result, err := runCoordinatorTest(t, coordinator, context.Background(), releaseInput)
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
	clearCoordinatorAllHitExecutionIdentity(&input)
	reloadRemotePlanningSnapshot(t, &input)
	store := &coordinatorStore{}
	runtime := &coordinatorRuntime{}
	coordinator := newTestCoordinator(t, store, runtime)
	coordinator.newID = func() (string, error) { return "job-0123456789abcdef0123456e", nil }
	result, err := runCoordinatorTest(t, coordinator, context.Background(), input)
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
	seedCalibrationDurationIndex(t, &input, mustCoordinatorCatalog(t, input))

	store := &coordinatorStore{}
	runtime := &coordinatorRuntime{}
	coordinator := newTestCoordinator(t, store, runtime)
	coordinator.newID = func() (string, error) { return "job-0123456789abcdef0123456d", nil }
	result, err := runCoordinatorTest(t, coordinator, context.Background(), input)
	assertCoordinatorPartialReuse(t, result, err, runtime)
}

// TestCoordinatorCalibrationDemotesOnlyPassesWithoutDurationSamples 锁定校准
// 复用还需独立 duration 证据，缺一项时只能重跑该项而不能扩大为整批 MISS。
func TestCoordinatorCalibrationDemotesOnlyPassesWithoutDurationSamples(t *testing.T) {
	_, input := coordinatorReuseFixture(t)
	input.Calibration = true
	input.CalibrationResource = testRemoteResourcePolicy().CalibrationResource
	catalog := mustCoordinatorCatalog(t, input)
	workloads := remoteShardableWorkloads(catalog)
	if len(workloads) < 2 {
		t.Fatal("calibration fixture requires two shardable workloads")
	}
	input.LedgerSnapshot.Ledger.Samples = []gate.DurationSample{calibrationDurationSample(input, workloads[0])}
	index, err := gate.BuildDurationSampleIndex(input.LedgerSnapshot.Ledger, remotePlanningContext(input))
	if err != nil {
		t.Fatal(err)
	}
	input.LedgerSnapshot.SampleIndex = &index
	reused := map[string]gate.WorkloadPassEvidence{workloads[0].ID: {}, workloads[1].ID: {}}
	demoted, err := demoteCalibrationReuseWithoutDuration(input, catalog, reused, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if demoted != 1 || len(reused) != 1 {
		t.Fatalf("calibration duration demotion = %d reused=%d, want 1/1", demoted, len(reused))
	}
	if _, kept := reused[workloads[0].ID]; !kept {
		t.Fatalf("calibration demoted workload with comparable sample %q", workloads[0].ID)
	}
}

// TestCoordinatorCalibrationDemotesPassWithOnlyDifferentInputDuration 验证不同
// input 的历史上界只能参与规划，不能替代当前 input 的可比较校准样本。
func TestCoordinatorCalibrationDemotesPassWithOnlyDifferentInputDuration(t *testing.T) {
	_, input := coordinatorReuseFixture(t)
	input.Calibration = true
	input.CalibrationResource = testRemoteResourcePolicy().CalibrationResource
	catalog := mustCoordinatorCatalog(t, input)
	workloads := remoteShardableWorkloads(catalog)
	if len(workloads) == 0 {
		t.Fatal("calibration fixture requires a shardable workload")
	}
	current := workloads[0]
	historical := current
	historical.InputDigest = "sha256:" + strings.Repeat("f", 64)
	input.LedgerSnapshot.Ledger.Samples = []gate.DurationSample{calibrationDurationSample(input, historical)}
	index, err := gate.BuildDurationSampleIndex(input.LedgerSnapshot.Ledger, remotePlanningContext(input))
	if err != nil {
		t.Fatal(err)
	}
	if !index.HasSuccessfulCalibrationDurationEvidence(current) || index.HasComparableSuccessfulDurationSample(current) {
		t.Fatal("fixture must contain only a different-input calibration upper bound")
	}
	input.LedgerSnapshot.SampleIndex = &index
	reused := map[string]gate.WorkloadPassEvidence{current.ID: {}}
	demoted, err := demoteCalibrationReuseWithoutDuration(input, catalog, reused, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if demoted != 1 || len(reused) != 0 {
		t.Fatalf("different-input calibration demotion = %d reused=%d, want 1/0", demoted, len(reused))
	}
}

func calibrationDurationSample(input RunInput, workload gate.Workload) gate.DurationSample {
	resource := input.CalibrationResource
	return gate.DurationSample{Bucket: gate.DurationBucket{
		WorkloadID: workload.ID, CommandDigest: workload.CommandDigest, InputDigest: workload.InputDigest,
		Platform: input.Platform, Runner: input.RunnerIdentityDigest, Toolchain: input.ToolchainDigest,
		ExecutionMode: gate.DurationExecutionModeCalibration, ResourceClassID: resource.ID,
		ResourceCPU: float64(resource.VCPU), ResourceMemoryGiB: float64(resource.MemoryGiB),
	}, Succeeded: true, DurationMS: 1_000}
}

func seedCalibrationDurationIndex(t *testing.T, input *RunInput, catalog gate.WorkloadCatalog) {
	t.Helper()
	samples := make([]gate.DurationSample, 0)
	for _, workload := range remoteShardableWorkloads(catalog) {
		samples = append(samples, calibrationDurationSample(*input, workload))
	}
	input.LedgerSnapshot.Ledger.Samples = samples
	index, err := gate.BuildDurationSampleIndex(input.LedgerSnapshot.Ledger, remotePlanningContext(*input))
	if err != nil {
		t.Fatal(err)
	}
	input.LedgerSnapshot.SampleIndex = &index
}

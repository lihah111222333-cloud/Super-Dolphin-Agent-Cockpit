package remoteci

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"golang.org/x/sync/errgroup"
)

type deferredCredentialCommandRunner struct {
	calls int
}

func (runner *deferredCredentialCommandRunner) Run(context.Context, string, ...string) ([]byte, error) {
	runner.calls++
	return []byte(`{}`), nil
}

func newDeferredCredentialECIClient(t *testing.T, runner eci.CommandRunner) *eci.Client {
	t.Helper()
	client, err := eci.NewWithRunner(eci.Config{
		Binary: "aliyun", RegionID: "cn-shenzhen",
		VSwitches: []cicontract.ECIVSwitch{
			{ID: "vsw-zone-a", ZoneID: "cn-test-a"},
			{ID: "vsw-zone-b", ZoneID: "cn-test-b"},
		},
		SecurityGroupID: "sg-remote-ci", WorkerRoleName: "remote-ci-worker", Profile: "ci-profile",
		Deadline: 10 * time.Minute, SpotStrategy: eci.SpotStrategyAsPriceGo, SpotDurationHours: 1,
		RegistryCredentialLoader: func() (eci.RegistryCredential, error) {
			username, usernamePresent := os.LookupEnv("SUPER_DOLPHIN_CI_GHCR_USERNAME")
			token, tokenPresent := os.LookupEnv("SUPER_DOLPHIN_CI_GHCR_TOKEN")
			if !usernamePresent || !tokenPresent || strings.TrimSpace(username) == "" || strings.TrimSpace(token) == "" {
				return eci.RegistryCredential{}, errors.New("remote CI GHCR credential is required")
			}
			return eci.RegistryCredential{Server: "ghcr.io", UserName: username, Password: token}, nil
		},
	}, runner)
	if err != nil {
		t.Fatalf("NewWithRunner() error = %v", err)
	}
	return client
}

func TestCoordinatorRunReusesAcceptedWorkloadPassesWithoutRemoteSideEffects(t *testing.T) {
	_, input := coordinatorReuseFixture(t)
	seed := runCoordinatorFreshWorkloads(t, input)
	seedCoordinatorWorkloadPassEvidence(t, input, seed, nil)

	store := &coordinatorStore{}
	runtime := &coordinatorRuntime{}
	coordinator := newTestCoordinator(t, store, runtime)
	coordinator.newID = func() (string, error) { return "job-0123456789abcdef0123456a", nil }
	result, err := runCoordinatorTest(t, coordinator, context.Background(), input)
	assertCoordinatorFullReuse(t, result, err, store, runtime)
}

// TestCoordinatorAllHitWithMissingRegistryCredentialReusesWithoutECICreate 验证全命中不解析 GHCR 凭据且不创建 ECI。
func TestCoordinatorAllHitWithMissingRegistryCredentialReusesWithoutECICreate(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_CI_GHCR_USERNAME", "")
	t.Setenv("SUPER_DOLPHIN_CI_GHCR_TOKEN", "")
	_, input := coordinatorReuseFixture(t)
	seed := runCoordinatorFreshWorkloads(t, input)
	seedCoordinatorWorkloadPassEvidence(t, input, seed, nil)
	// 全命中运行不得进入 planner。先写入 PASS 证据，再清空规划快照；若误入
	// planner 会立即因快照无效失败，而纯复用路径仍应自洽完成。
	input.LedgerSnapshot = gate.DurationLedgerSnapshot{}

	store := &coordinatorStore{}
	runner := &deferredCredentialCommandRunner{}
	runtime := newDeferredCredentialECIClient(t, runner)
	coordinator := newTestCoordinator(t, store, runtime)
	coordinator.newID = func() (string, error) { return "job-0123456789abcdef0123456c", nil }
	result, err := runCoordinatorTest(t, coordinator, context.Background(), input)
	if err != nil {
		t.Fatalf("all-hit Run() error = %v", err)
	}
	assertCoordinatorReuseResult(t, result)
	if len(store.uploads) != 0 || len(store.deletePrefixes) != 0 || runner.calls != 0 {
		t.Fatalf("all-hit performed remote work: uploads=%v deletes=%v eci_cli_calls=%d", store.uploads, store.deletePrefixes, runner.calls)
	}
}

// TestCoordinatorMissWithMissingRegistryCredentialFailsBeforeECICreate 验证 miss 在 ECI CLI 创建前因缺少凭据失败。
func TestCoordinatorMissWithMissingRegistryCredentialFailsBeforeECICreate(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_CI_GHCR_USERNAME", "")
	t.Setenv("SUPER_DOLPHIN_CI_GHCR_TOKEN", "")
	_, input := coordinatorReuseFixture(t)
	store := &coordinatorStore{}
	runner := &deferredCredentialCommandRunner{}
	runtime := newDeferredCredentialECIClient(t, runner)
	coordinator := newTestCoordinator(t, store, runtime)
	coordinator.newID = func() (string, error) { return "job-0123456789abcdef0123456d", nil }
	_, err := runCoordinatorTest(t, coordinator, context.Background(), input)
	if err == nil || !strings.Contains(err.Error(), "GHCR credential") {
		t.Fatalf("miss Run() error = %v, want missing GHCR credential", err)
	}
	if runner.calls != 0 {
		t.Fatalf("miss ECI CLI create calls = %d, want 0", runner.calls)
	}
}

func TestCoordinatorRunExecutesOnlyWorkloadPassMissesAndMergesResults(t *testing.T) {
	_, input := coordinatorReuseFixture(t)
	seed := runCoordinatorFreshWorkloads(t, input)
	seedCoordinatorWorkloadPassEvidence(t, input, seed, func(index int) bool { return index%2 == 0 })

	store := &coordinatorStore{}
	runtime := &coordinatorRuntime{}
	coordinator := newTestCoordinator(t, store, runtime)
	coordinator.newID = func() (string, error) { return "job-0123456789abcdef0123456b", nil }
	result, err := runCoordinatorTest(t, coordinator, context.Background(), input)
	assertCoordinatorPartialReuse(t, result, err, runtime)
}

func TestCoordinatorPrepareFreezesAllReuseWithoutRemoteSideEffects(t *testing.T) {
	_, input := coordinatorReuseFixture(t)
	seed := runCoordinatorFreshWorkloads(t, input)
	seedCoordinatorWorkloadPassEvidence(t, input, seed, nil)

	store := &coordinatorStore{}
	runtime := &coordinatorRuntime{}
	coordinator := newTestCoordinator(t, store, runtime)
	coordinator.newID = func() (string, error) { return "job-0123456789abcdef0123456e", nil }
	prepared, err := coordinator.Prepare(context.Background(), input)
	if err != nil || !prepared.AllReused() {
		t.Fatalf("Prepare() prepared=%#v error=%v", prepared, err)
	}
	assertCoordinatorNoRemoteSideEffects(t, store, runtime)

	// RunPrepared 消费 Prepare 冻结的复用决策；同一权威证据必须持续到最终化。
	result, err := coordinator.RunPrepared(context.Background(), prepared)
	assertCoordinatorFullReuse(t, result, err, store, runtime)
}

func TestCoordinatorRunPreparedRefreshesOnlyPlanningSnapshotForMisses(t *testing.T) {
	_, input := coordinatorReuseFixture(t)
	seed := runCoordinatorFreshWorkloads(t, input)
	seedCoordinatorWorkloadPassEvidence(t, input, seed, func(index int) bool { return index%2 == 0 })

	store := &coordinatorStore{}
	runtime := &coordinatorRuntime{}
	coordinator := newTestCoordinator(t, store, runtime)
	coordinator.newID = func() (string, error) { return "job-0123456789abcdef0123456f", nil }
	prepared, err := coordinator.Prepare(context.Background(), input)
	if err != nil || prepared.AllReused() || len(prepared.reuse.cacheMisses) == 0 {
		t.Fatalf("Prepare() prepared=%#v error=%v", prepared, err)
	}
	if err := prepared.RefreshPlanningSnapshot(input.LedgerStore); err != nil {
		t.Fatalf("RefreshPlanningSnapshot() error = %v", err)
	}
	result, err := coordinator.RunPrepared(context.Background(), prepared)
	assertCoordinatorPartialReuse(t, result, err, runtime)
	assertCoordinatorShardsContainOnlyMisses(t, result)
}

// TestCoordinatorRunPreparedRejectsPlanningOverheadIdentityDrift 验证最终 SQLite 规划重载在副作用前关闭刷新到执行的 TOCTOU 窗口。
func TestCoordinatorRunPreparedRejectsPlanningOverheadIdentityDrift(t *testing.T) {
	_, input := coordinatorReuseFixture(t)
	seed := runCoordinatorFreshWorkloads(t, input)
	seedCoordinatorWorkloadPassEvidence(t, input, seed, func(index int) bool { return index%2 == 0 })

	store := &coordinatorStore{}
	runtime := &coordinatorRuntime{}
	coordinator := newTestCoordinator(t, store, runtime)
	coordinator.newID = func() (string, error) { return "job-0123456789abcdef01234570", nil }
	prepared, err := coordinator.Prepare(context.Background(), input)
	if err != nil || prepared.AllReused() {
		t.Fatalf("Prepare() prepared=%#v error=%v, want normal miss", prepared, err)
	}
	if err := prepared.RefreshPlanningSnapshot(input.LedgerStore); err != nil {
		t.Fatalf("RefreshPlanningSnapshot() error = %v", err)
	}

	replaceCoordinatorPlanningOverhead(t, input)

	if _, err := coordinator.RunPrepared(context.Background(), prepared); err == nil || !strings.Contains(err.Error(), "planning shard overhead identity drifted") {
		t.Fatalf("RunPrepared() error = %v, want planning overhead identity drift", err)
	}
	if len(store.uploads) != 0 || len(store.deletePrefixes) != 0 || len(runtime.creates) != 0 {
		t.Fatalf("planning drift caused remote side effects: uploads=%v deletes=%v creates=%v", store.uploads, store.deletePrefixes, runtime.creates)
	}
}

func replaceCoordinatorPlanningOverhead(t *testing.T, input RunInput) {
	t.Helper()
	currentOverhead := input.LedgerSnapshot.Ledger.ShardOverhead
	if currentOverhead == nil {
		t.Fatal("prepared planning snapshot has no accepted overhead")
	}
	replacement := *currentOverhead
	replacement.CalibrationResourceClassID = "calibration-drift"
	replacement.ProvenanceDigest = "sha256:" + strings.Repeat("e", 64)
	sample := gate.ShardOrchestrationOverheadSample{
		AcceptedGeneration:     replacement.AcceptedGeneration,
		ProvenanceDigest:       replacement.ProvenanceDigest,
		JobID:                  "fixture-shard-overhead",
		ShardIdentity:          "fixture-shard",
		TotalStartedAt:         time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC),
		TotalCompletedAt:       time.Date(2026, 7, 27, 0, 0, 2, 0, time.UTC),
		WorkloadEnvelopeStart:  time.Date(2026, 7, 27, 0, 0, 0, 500000000, time.UTC),
		WorkloadEnvelopeEnd:    time.Date(2026, 7, 27, 0, 0, 1, 500000000, time.UTC),
		AccountedDurationMS:    1000,
		AccountedIntervalCount: 1,
		OverheadMS:             1000,
	}
	if _, err := input.LedgerStore.CompareAndSwapShardOverhead(input.LedgerSnapshot.Generation, replacement, []gate.ShardOrchestrationOverheadSample{sample}); err != nil {
		t.Fatalf("replace planning overhead identity: %v", err)
	}
}

func TestPreparedRunRejectsInvalidPlanningSnapshotWithoutSideEffects(t *testing.T) {
	_, input := coordinatorReuseFixture(t)
	store := &coordinatorStore{}
	runtime := &coordinatorRuntime{}
	coordinator := newTestCoordinator(t, store, runtime)
	prepared, err := coordinator.Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if err := prepared.RefreshPlanningSnapshot(nil); err == nil {
		t.Fatal("RefreshPlanningSnapshot() accepted a nil authority")
	}
	assertCoordinatorNoRemoteSideEffects(t, store, runtime)
}

func TestCoordinatorRunPreparedConsumesOnePreparedRunExactlyOnce(t *testing.T) {
	_, input := coordinatorReuseFixture(t)
	seed := runCoordinatorFreshWorkloads(t, input)
	seedCoordinatorWorkloadPassEvidence(t, input, seed, nil)

	coordinator := newTestCoordinator(t, &coordinatorStore{}, &coordinatorRuntime{})
	coordinator.newID = func() (string, error) { return "job-0123456789abcdef0123456c", nil }
	prepared, err := coordinator.Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	results := make(chan error, 2)
	var runs errgroup.Group
	for range 2 {
		runs.Go(func() error {
			_, runErr := coordinator.RunPrepared(context.Background(), prepared)
			results <- runErr
			return nil
		})
	}
	if err := runs.Wait(); err != nil {
		t.Fatalf("RunPrepared() coordination error = %v", err)
	}
	close(results)
	successes, consumed := 0, 0
	for runErr := range results {
		if runErr == nil {
			successes++
			continue
		}
		if strings.Contains(runErr.Error(), "already consumed") {
			consumed++
			continue
		}
		t.Fatalf("RunPrepared() error = %v", runErr)
	}
	if successes != 1 || consumed != 1 {
		t.Fatalf("RunPrepared() successes=%d consumed=%d", successes, consumed)
	}
}

func TestCoordinatorRunPreparedRejectsAliasedInputMutation(t *testing.T) {
	_, input := coordinatorReuseFixture(t)
	store := &coordinatorStore{}
	runtime := &coordinatorRuntime{}
	coordinator := newTestCoordinator(t, store, runtime)
	prepared, err := coordinator.Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	input.OCIProjectCache.Image = "registry.example/mutated@sha256:" + strings.Repeat("f", 64)
	if _, err := coordinator.RunPrepared(context.Background(), prepared); err == nil || !strings.Contains(err.Error(), "identity drifted") {
		t.Fatalf("RunPrepared() error = %v, want frozen identity rejection", err)
	}
	assertCoordinatorNoRemoteSideEffects(t, store, runtime)
}

func TestCoordinatorRunPreparedRejectsDifferentCoordinator(t *testing.T) {
	_, input := coordinatorReuseFixture(t)
	preparedOwner := newTestCoordinator(t, &coordinatorStore{}, &coordinatorRuntime{})
	prepared, err := preparedOwner.Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	store := &coordinatorStore{}
	runtime := &coordinatorRuntime{}
	other := newTestCoordinator(t, store, runtime)
	if _, err := other.RunPrepared(context.Background(), prepared); err == nil || !strings.Contains(err.Error(), "different coordinator") {
		t.Fatalf("RunPrepared() error = %v, want coordinator rejection", err)
	}
	assertCoordinatorNoRemoteSideEffects(t, store, runtime)
}

func TestRemoteWorkloadPassIdentityBindsOnlyExecutionSemantics(t *testing.T) {
	repository, input := remoteRunFixture(t)
	input.RepositoryRoot = repository
	_, catalog, _, err := buildRemotePlan(input)
	assertCoordinatorCatalog(t, catalog, err)
	baseline, err := remoteWorkloadPassIdentities(context.Background(), input, catalog, 10*time.Minute, testRemoteResourcePolicy())
	if err != nil {
		t.Fatalf("remoteWorkloadPassIdentities() error = %v", err)
	}
	assertCoordinatorBaselineRefreshIdentity(t, input, catalog, baseline)
	assertCoordinatorSemanticIdentityMisses(t, input, catalog, baseline)
	assertCoordinatorWorkerTimeoutIdentityUnchanged(t, input, catalog, baseline)
	assertCoordinatorIgnoresCoordinatorSourceIdentity(t, input, catalog, baseline)
	assertCoordinatorForceIdentityUnchanged(t, input, catalog, baseline)
	assertCoordinatorCommandAndInputMiss(t, repository, input, catalog, baseline)
}

func TestRemoteWorkloadPassIdentitySharesAcrossCalibrationModeAndResource(t *testing.T) {
	repository, input := remoteRunFixture(t)
	input.RepositoryRoot = repository
	_, catalog, _, err := buildRemotePlan(input)
	assertCoordinatorCatalog(t, catalog, err)
	policy := testRemoteResourcePolicy()
	normal, err := remoteWorkloadPassIdentities(context.Background(), input, catalog, 10*time.Minute, policy)
	if err != nil {
		t.Fatalf("normal remoteWorkloadPassIdentities() error = %v", err)
	}
	calibrationInput := input
	calibrationInput.Calibration = true
	calibrationInput.CalibrationResource = policy.CalibrationResource
	reloadRemotePlanningSnapshot(t, &calibrationInput)
	calibration, err := remoteWorkloadPassIdentities(context.Background(), calibrationInput, catalog, 10*time.Minute, policy)
	if err != nil {
		t.Fatalf("calibration remoteWorkloadPassIdentities() error = %v", err)
	}
	if len(normal) == 0 || len(calibration) == 0 || normal[0].EnvironmentDigest != calibration[0].EnvironmentDigest {
		t.Fatalf("calibration mode changed PASS environment identity: normal=%#v calibration=%#v", normal, calibration)
	}
	changedResource := calibrationInput
	changedResource.CalibrationResource.ID = calibrationInput.CalibrationResource.ID + "-alternate"
	changedDigest, err := remoteWorkloadEnvironmentDigest(changedResource, 10*time.Minute, policy)
	if err != nil {
		t.Fatalf("changed calibration resource digest error = %v", err)
	}
	if changedDigest != calibration[0].EnvironmentDigest {
		t.Fatalf("calibration resource changed PASS environment identity: got=%q want=%q", changedDigest, calibration[0].EnvironmentDigest)
	}
}

func TestCoordinatorRunReusesPassesWhenWorkerTimeoutChanges(t *testing.T) {
	_, input := coordinatorReuseFixture(t)
	seed := runCoordinatorFreshWorkloads(t, input)
	seedCoordinatorWorkloadPassEvidence(t, input, seed, nil)

	coordinator := newTestCoordinator(t, &coordinatorStore{}, &coordinatorRuntime{})
	coordinator.config.WorkerTimeout = 30 * time.Minute
	coordinator.newID = func() (string, error) { return "job-0123456789abcdef0123456d", nil }
	result, err := runCoordinatorTest(t, coordinator, context.Background(), input)
	if err != nil {
		t.Fatalf("Run() with changed worker timeout error = %v", err)
	}
	if len(result.ReusedWorkloads) != len(result.WorkloadPassIdentities) || len(result.FreshWorkloadExecutions) != 0 {
		t.Fatalf("worker timeout change did not reuse prior PASS evidence: %#v", result)
	}
}

func TestCoordinatorRunReusesPassesWhenResourcePolicyChanges(t *testing.T) {
	_, input := coordinatorReuseFixture(t)
	seed := runCoordinatorFreshWorkloads(t, input)
	seedCoordinatorWorkloadPassEvidence(t, input, seed, nil)

	coordinator := newTestCoordinator(t, &coordinatorStore{}, &coordinatorRuntime{})
	coordinator.config.ResourcePolicy.HeadroomPercent++
	coordinator.newID = func() (string, error) { return "job-0123456789abcdef0123456e", nil }
	result, err := runCoordinatorTest(t, coordinator, context.Background(), input)
	if err != nil {
		t.Fatalf("Run() with changed resource policy error = %v", err)
	}
	if len(result.ReusedWorkloads) != len(result.WorkloadPassIdentities) || len(result.FreshWorkloadExecutions) != 0 {
		t.Fatalf("resource policy change did not reuse prior PASS evidence: %#v", result)
	}
}

func assertCoordinatorFullReuse(t *testing.T, result RunResult, err error, store *coordinatorStore, runtime *coordinatorRuntime) {
	t.Helper()
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	assertCoordinatorReuseResult(t, result)
	assertCoordinatorNoRemoteSideEffects(t, store, runtime)
}

func assertCoordinatorReuseResult(t *testing.T, result RunResult) {
	t.Helper()
	if result.Status != gate.ResultStatusPassed || !result.CleanupComplete {
		t.Fatalf("reused Run() result = %#v", result)
	}
	if len(result.Shards) != 0 || len(result.FreshWorkloadExecutions) != 0 {
		t.Fatalf("reused Run() executed fresh shards: %#v", result)
	}
	if len(result.ReusedWorkloads) != len(result.WorkloadPassIdentities) || len(result.WorkloadExecutions) != len(result.WorkloadPassIdentities) {
		t.Fatalf("reused Run() did not preserve complete workload result: %#v", result)
	}
}

func assertCoordinatorNoRemoteSideEffects(t *testing.T, store *coordinatorStore, runtime *coordinatorRuntime) {
	t.Helper()
	if len(store.uploads) != 0 || len(store.deletePrefixes) != 0 {
		t.Fatalf("reused Run() performed OSS work: uploads=%v deletes=%v", store.uploads, store.deletePrefixes)
	}
	if len(runtime.creates) != 0 || len(runtime.deletes) != 0 {
		t.Fatalf("reused Run() performed ECI work: creates=%v deletes=%v", runtime.creates, runtime.deletes)
	}
}

func assertCoordinatorPartialReuse(t *testing.T, result RunResult, err error, runtime *coordinatorRuntime) {
	t.Helper()
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.ReusedWorkloads) == 0 || len(result.CacheMissWorkloads) == 0 {
		t.Fatalf("partial reuse result = %#v", result)
	}
	if len(result.FreshWorkloadExecutions) != len(result.CacheMissWorkloads) || len(runtime.creates) == 0 {
		t.Fatalf("partial reuse fresh execution = %#v", result)
	}
	if len(result.WorkloadExecutions) != len(result.WorkloadPassIdentities) || len(result.GateExecutions) == 0 {
		t.Fatalf("partial reuse did not merge complete catalog result: %#v", result)
	}
	assertCoordinatorShardsContainOnlyMisses(t, result)
	assertCoordinatorPartialReuseTiming(t, result)
}

func assertCoordinatorOwnerProjection(t *testing.T, result RunResult, catalog gate.WorkloadCatalog) {
	t.Helper()
	assertCoordinatorReleaseOwnerCatalog(t, catalog)
	assertCoordinatorReleaseOwnerGateExecution(t, result)
	assertCoordinatorReleaseOwnerExcludedFromWorkloadExecutions(t, result)
}

func assertCoordinatorReleaseOwnerCatalog(t *testing.T, catalog gate.WorkloadCatalog) {
	t.Helper()
	for _, workload := range catalog.Workloads {
		if workload.ID == string(gate.GateIDReleaseLayeredCheck) && !workload.Shardable {
			return
		}
	}
	t.Fatal("release catalog omitted owner-only workload")
}

func assertCoordinatorReleaseOwnerGateExecution(t *testing.T, result RunResult) {
	t.Helper()
	ownerExecutions := 0
	for _, execution := range result.GateExecutions {
		if execution.GateID == gate.GateIDReleaseLayeredCheck {
			ownerExecutions++
		}
	}
	if ownerExecutions != 1 {
		t.Fatalf("release owner GateExecutions = %d, want exactly one: %#v", ownerExecutions, result.GateExecutions)
	}
	last := result.GateExecutions[len(result.GateExecutions)-1]
	if last.GateID != gate.GateIDReleaseLayeredCheck || last.Status != gate.ResultStatusPassed {
		t.Fatalf("release owner attestation = %#v", last)
	}
}

func assertCoordinatorReleaseOwnerExcludedFromWorkloadExecutions(t *testing.T, result RunResult) {
	t.Helper()
	for _, execution := range result.WorkloadExecutions {
		if execution.GateID == gate.GateIDReleaseLayeredCheck {
			t.Fatal("owner-only release attestation leaked into shardable workload executions")
		}
	}
}

func assertCoordinatorPartialReuseTiming(t *testing.T, result RunResult) {
	t.Helper()
	observations, err := remoteTimingObservations(result)
	if err != nil {
		t.Fatalf("partial reuse timing observations: %v", err)
	}
	for _, observation := range observations {
		if observation.Scope == cicontract.TimingScopeWorkload && !containsCoordinatorGateID(result.CacheMissWorkloads, observation.WorkloadID) {
			t.Fatalf("reused workload %q received current-run timing %#v", observation.WorkloadID, observation)
		}
	}
	corrupted := result
	corrupted.FreshWorkloadExecutions = append(append([]gate.PlanGateExecution(nil), result.FreshWorkloadExecutions...), result.ReusedWorkloads[0].OriginExecution)
	if _, err := remoteTimingObservations(corrupted); err == nil {
		t.Fatal("timing accepted reused workload as a current shard execution")
	}
}

func assertCoordinatorShardsContainOnlyMisses(t *testing.T, result RunResult) {
	t.Helper()
	for _, shard := range result.Shards {
		for _, workloadID := range shard.ExecutedWorkloads {
			if !containsCoordinatorGateID(result.CacheMissWorkloads, workloadID) {
				t.Fatalf("reused workload %q entered shard %#v", workloadID, shard)
			}
		}
	}
}

func assertCoordinatorCatalog(t *testing.T, catalog gate.WorkloadCatalog, err error) {
	t.Helper()
	if err != nil || len(catalog.Workloads) == 0 {
		t.Fatalf("buildRemotePlan() catalog=%#v error=%v", catalog, err)
	}
}

func assertCoordinatorBaselineRefreshIdentity(t *testing.T, input RunInput, catalog gate.WorkloadCatalog, baseline []gate.WorkloadPassIdentity) {
	t.Helper()
	for name, mutate := range map[string]func(*RunInput){
		"accepted generation": func(in *RunInput) { in.AcceptedGeneration++ },
		"accepted snapshot":   func(in *RunInput) { in.ImageCacheSnapshotID = "snap-refreshed" },
		"agent token":         func(in *RunInput) { in.AgentTokenDigest = "sha256:" + strings.Repeat("a", 64) },
		"baseline manifest":   func(in *RunInput) { in.BaselineManifestDigest = "sha256:" + strings.Repeat("b", 64) },
		"runner cache seed":   func(in *RunInput) { in.OCIProjectCache.ContentManifestSHA256 = "sha256:" + strings.Repeat("c", 64) },
	} {
		t.Run(name, func(t *testing.T) { assertCoordinatorIdentityUnchanged(t, input, catalog, baseline, mutate) })
	}
}

func assertCoordinatorIdentityUnchanged(t *testing.T, input RunInput, catalog gate.WorkloadCatalog, baseline []gate.WorkloadPassIdentity, mutate func(*RunInput)) {
	t.Helper()
	changed := input
	changed.OCIProjectCache = cloneBaselineOCIProjectCache(input.OCIProjectCache)
	mutate(&changed)
	identities, err := remoteWorkloadPassIdentities(context.Background(), changed, catalog, 10*time.Minute, testRemoteResourcePolicy())
	if err != nil || !reflect.DeepEqual(identities, baseline) {
		t.Fatalf("cache/source-only refresh changed reuse identity: got=%#v err=%v want=%#v", identities, err, baseline)
	}
}

func assertCoordinatorSemanticIdentityMisses(t *testing.T, input RunInput, catalog gate.WorkloadCatalog, baseline []gate.WorkloadPassIdentity) {
	t.Helper()
	for name, mutate := range map[string]func(*RunInput){
		"platform": func(in *RunInput) { in.Platform = "linux/arm64" }, "policy": func(in *RunInput) { in.PolicyDigest = "sha256:" + strings.Repeat("1", 64) },
		"toolchain": func(in *RunInput) { in.ToolchainDigest = "sha256:" + strings.Repeat("2", 64) }, "runtime seed": func(in *RunInput) { in.RuntimeSeedSHA256 = "sha256:" + strings.Repeat("3", 64) },
	} {
		t.Run(name, func(t *testing.T) { assertCoordinatorIdentityChanged(t, input, catalog, baseline, mutate) })
	}
}

func assertCoordinatorIgnoresCoordinatorSourceIdentity(t *testing.T, input RunInput, catalog gate.WorkloadCatalog, baseline []gate.WorkloadPassIdentity) {
	t.Helper()
	for name, mutate := range map[string]func(*RunInput){
		"candidate Gate source":    func(in *RunInput) { in.CandidateGateSourceSHA256 = "sha256:" + strings.Repeat("4", 64) },
		"candidate Gate toolchain": func(in *RunInput) { in.CandidateGateToolchainSHA256 = "sha256:" + strings.Repeat("5", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			changed := input
			mutate(&changed)
			identities, err := remoteWorkloadPassIdentities(context.Background(), changed, catalog, 10*time.Minute, testRemoteResourcePolicy())
			if err != nil || !reflect.DeepEqual(identities, baseline) {
				t.Fatalf("coordinator source-only change altered PASS identity: got=%#v err=%v want=%#v", identities, err, baseline)
			}
		})
	}
}

func assertCoordinatorWorkerTimeoutIdentityUnchanged(t *testing.T, input RunInput, catalog gate.WorkloadCatalog, baseline []gate.WorkloadPassIdentity) {
	t.Helper()
	identities, err := remoteWorkloadPassIdentities(context.Background(), input, catalog, 30*time.Minute, testRemoteResourcePolicy())
	if err != nil || !reflect.DeepEqual(identities, baseline) {
		t.Fatalf("worker timeout change altered PASS identity: got=%#v err=%v want=%#v", identities, err, baseline)
	}
}

func assertCoordinatorForceIdentityUnchanged(t *testing.T, input RunInput, catalog gate.WorkloadCatalog, baseline []gate.WorkloadPassIdentity) {
	t.Helper()
	changed := input
	changed.Force = true
	identities, err := remoteWorkloadPassIdentities(context.Background(), changed, catalog, 10*time.Minute, testRemoteResourcePolicy())
	if err != nil || !reflect.DeepEqual(identities, baseline) {
		t.Fatalf("force mode altered PASS identity: got=%#v err=%v want=%#v", identities, err, baseline)
	}
}

func assertCoordinatorIdentityChanged(t *testing.T, input RunInput, catalog gate.WorkloadCatalog, baseline []gate.WorkloadPassIdentity, mutate func(*RunInput)) {
	t.Helper()
	changed := input
	mutate(&changed)
	identities, err := remoteWorkloadPassIdentities(context.Background(), changed, catalog, 10*time.Minute, testRemoteResourcePolicy())
	if err != nil || reflect.DeepEqual(identities, baseline) {
		t.Fatalf("semantic change retained reuse identity: got=%#v err=%v", identities, err)
	}
}

func assertCoordinatorCommandAndInputMiss(t *testing.T, repository string, input RunInput, catalog gate.WorkloadCatalog, baseline []gate.WorkloadPassIdentity) {
	t.Helper()
	changedCatalog := catalog
	changedCatalog.Workloads = append([]gate.Workload(nil), catalog.Workloads...)
	shardableIndex := slices.IndexFunc(changedCatalog.Workloads, func(workload gate.Workload) bool { return workload.Shardable })
	if shardableIndex < 0 {
		t.Fatal("remote workload catalog has no shardable workload")
	}
	changedCatalog.Workloads[shardableIndex].CommandDigest = "sha256:" + strings.Repeat("6", 64)
	assertCoordinatorIdentityChanged(t, input, changedCatalog, baseline, func(*RunInput) {})
	writeCoordinatorFixture(t, repository, "frontend-app/package.json", "{\"observable\":true}\n")
	runCoordinatorGit(t, repository, "add", "frontend-app/package.json")
	runCoordinatorGit(t, repository, "commit", "--quiet", "-m", "observable input")
	changedInput := input
	changedInput.Commit = coordinatorGitOutput(t, repository, "rev-parse", "HEAD")
	changedInput.Tree = coordinatorGitOutput(t, repository, "rev-parse", "HEAD^{tree}")
	changedInput.Source.Commit.SHA = changedInput.Commit
	changedInput.Source.SourceTreeSHA = changedInput.Tree
	changedInput.WorkloadInputDigests = nil
	assertCoordinatorIdentityChanged(t, changedInput, catalog, baseline, func(*RunInput) {})
}

func TestCoordinatorRunSharesAcceptedPassesAcrossAgentsWithIndependentJobs(t *testing.T) {
	_, input := coordinatorReuseFixture(t)
	seed := runCoordinatorFreshWorkloads(t, input)
	seedCoordinatorWorkloadPassEvidence(t, input, seed, nil)
	firstInput, secondInput := input, input
	firstInput.AgentTokenDigest = "sha256:" + strings.Repeat("1", 64)
	secondInput.AgentTokenDigest = "sha256:" + strings.Repeat("2", 64)
	firstStore, secondStore := &coordinatorStore{}, &coordinatorStore{}
	firstRuntime, secondRuntime := &coordinatorRuntime{}, &coordinatorRuntime{}
	first, second := newTestCoordinator(t, firstStore, firstRuntime), newTestCoordinator(t, secondStore, secondRuntime)
	first.newID = func() (string, error) { return "job-0123456789abcdef0123456a", nil }
	second.newID = func() (string, error) { return "job-0123456789abcdef0123456b", nil }
	var results [2]RunResult
	var runs errgroup.Group
	runs.Go(func() error {
		var runErr error
		results[0], runErr = runCoordinatorTest(t, first, context.Background(), firstInput)
		return runErr
	})
	runs.Go(func() error {
		var runErr error
		results[1], runErr = runCoordinatorTest(t, second, context.Background(), secondInput)
		return runErr
	})
	if err := runs.Wait(); err != nil {
		t.Fatalf("reused concurrent Run() error = %v", err)
	}
	for index, result := range results {
		if len(result.FreshWorkloadExecutions) != 0 || len(result.ReusedWorkloads) != len(result.WorkloadPassIdentities) {
			t.Fatalf("agent %d reuse result = %#v", index, result)
		}
	}
	if len(firstRuntime.creates)+len(secondRuntime.creates) != 0 || len(firstStore.uploads)+len(secondStore.uploads) != 0 {
		t.Fatal("cross-agent reuse created ECI or OSS work")
	}
	assertCoordinatorAgentIdentityRows(t, input.LedgerStore.AuthorityPath(), map[string]string{
		results[0].JobID: firstInput.AgentTokenDigest, results[1].JobID: secondInput.AgentTokenDigest,
	})
}

func TestCoordinatorRunConcurrentSameCandidateMissesDoNotSerialize(t *testing.T) {
	repository, input := remoteRunFixture(t)
	input.RepositoryRoot = repository
	planned := mustBuildAllMissRemoteExecutionShardSet(t, input)
	store := &coordinatorStore{uploadBarrier: newCoordinatorOverlapBarrier(len(planned.Shards)*2, 2)}
	runtime := &coordinatorRuntime{createBarrier: newCoordinatorOverlapBarrier(len(planned.Shards)*2, 2)}
	defer store.uploadBarrier.unblock()
	defer runtime.createBarrier.unblock()
	first, second := newTestCoordinator(t, store, runtime), newTestCoordinator(t, store, runtime)
	first.newID = func() (string, error) { return "job-0123456789abcdef0123456a", nil }
	second.newID = func() (string, error) { return "job-0123456789abcdef0123456b", nil }
	// 准备阶段无副作用，但包含源码指纹和账本工作，在 -race 下可能超过短屏障的等待时间。
	// 先冻结两份计划，再启动有副作用的执行，使屏障只观测本测试关注的并发阶段。
	firstPrepared, err := first.Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("first Prepare() error = %v", err)
	}
	secondPrepared, err := second.Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("second Prepare() error = %v", err)
	}
	var runs errgroup.Group
	runs.Go(func() error { _, err := first.RunPrepared(context.Background(), firstPrepared); return err })
	runs.Go(func() error { _, err := second.RunPrepared(context.Background(), secondPrepared); return err })
	assertCoordinatorBarrierReached(t, store.uploadBarrier, "same-candidate miss uploads")
	store.uploadBarrier.unblock()
	assertCoordinatorBarrierReached(t, runtime.createBarrier, "same-candidate miss ECI creates")
	runtime.createBarrier.unblock()
	if err := runs.Wait(); err != nil {
		t.Fatalf("concurrent miss Run() error = %v", err)
	}
}

func runCoordinatorFreshWorkloads(t *testing.T, input RunInput) RunResult {
	t.Helper()
	authorityInput := input
	authorityInput.Entrypoint = gate.CIEntrypointGitPreCommit
	result, err := runCoordinatorTest(t, newTestCoordinator(t, &coordinatorStore{}, &coordinatorRuntime{}), context.Background(), authorityInput)
	if err != nil || result.Status != gate.ResultStatusPassed || len(result.FreshWorkloadExecutions) == 0 {
		t.Fatalf("fresh Run() result=%#v error=%v", result, err)
	}
	return result
}

func coordinatorReuseFixture(t *testing.T) (string, RunInput) {
	t.Helper()
	repository, input := remoteRunFixture(t)
	input.RepositoryRoot = repository
	input.Source = gate.SourceSpec{
		Kind: gate.SourceKindTree, ObjectFormat: gate.GitObjectFormatSHA1,
		Tree: &gate.TreeSource{SHA: input.Tree, ParentCommitSHA: input.Commit}, SourceTreeSHA: input.Tree,
	}
	return repository, input
}

func seedCoordinatorWorkloadPassEvidence(t *testing.T, input RunInput, result RunResult, keep func(int) bool) {
	t.Helper()
	identities, err := remoteWorkloadPassIdentities(context.Background(), input, mustCoordinatorCatalog(t, input), 10*time.Minute, testRemoteResourcePolicy())
	if err != nil {
		t.Fatal(err)
	}
	promoteCoordinatorFreshWorkloads(t, input, result)
	removeCoordinatorPassEvidence(t, input.LedgerStore, identities, keep)
}

func promoteCoordinatorFreshWorkloads(t *testing.T, input RunInput, result RunResult) {
	t.Helper()
	if err := recordRemoteCIRun(input.LedgerStore, result, nil); err != nil {
		t.Fatalf("record fresh provisional PASS run: %v", err)
	}
	identity := gate.RemoteCIRunAuthorityIdentity{
		JobID: result.JobID, AgentTokenDigest: result.AgentTokenDigest, Force: result.Force, Entrypoint: result.Entrypoint,
		Profile: result.Profile, PlanDigest: result.PlanDigest, CatalogDigest: result.CatalogDigest,
		AcceptedGeneration: result.AcceptedGeneration, ImageCacheSnapshotID: result.ImageCacheSnapshotID,
		SourceTreeSHA: result.SourceTreeSHA, CandidateGateSourceSHA256: result.CandidateGateSourceSHA256,
		CandidateGateToolchainSHA256: result.CandidateGateToolchainSHA256, RunnerImage: result.RunnerImage,
		StartedAt: result.StartedAt, WorkloadPassIdentities: append([]gate.WorkloadPassIdentity(nil), result.WorkloadPassIdentities...),
	}
	if err := input.LedgerStore.FinalizeRemoteCIRunAuthorityWithSamples(
		identity,
		coordinatorFreshCheckReceipts(t, input, result),
		nil,
		len(result.FreshWorkloadExecutions) != 0,
	); err != nil {
		t.Fatalf("finalize fresh workload PASS authority: %v", err)
	}
}

func coordinatorFreshCheckReceipts(t *testing.T, input RunInput, result RunResult) []gate.CheckReceiptRecord {
	t.Helper()
	required, err := gate.RequiredChecksForWorkloadCatalog(mustCoordinatorCatalog(t, input))
	if err != nil {
		t.Fatal(err)
	}
	receipts := make([]gate.CheckReceiptRecord, 0, len(required))
	for index, check := range required {
		startedAt := result.StartedAt.Add(time.Duration(index) * time.Millisecond)
		receipt := gate.CheckReceiptRecord{RunID: result.JobID, JobID: result.JobID, CandidateTreeSHA: input.Tree, AgentTokenDigest: input.AgentTokenDigest, Force: result.Force, AcceptedGeneration: input.AcceptedGeneration, AcceptedSnapshotID: input.ImageCacheSnapshotID, RequiredCheck: check, Executed: true, Passed: true, StartedAt: startedAt, CompletedAt: startedAt.Add(time.Millisecond), Duration: time.Millisecond}
		digest, err := gate.CheckReceiptSHA256(receipt)
		if err != nil {
			t.Fatalf("digest required check %q receipt: %v", check, err)
		}
		receipt.ReceiptSHA256 = digest
		receipts = append(receipts, receipt)
	}
	return receipts
}

func removeCoordinatorPassEvidence(t *testing.T, store *gate.DurationLedgerStore, identities []gate.WorkloadPassIdentity, keep func(int) bool) {
	t.Helper()
	if keep == nil {
		return
	}
	database, err := sql.Open("sqlite", store.AuthorityPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	for index, identity := range identities {
		if !keep(index) {
			if _, err := database.Exec(`DELETE FROM ci_workload_pass_evidence WHERE identity_digest = ?`, identity.IdentityDigest); err != nil {
				t.Fatalf("remove evidence %q to model cache miss: %v", identity.WorkloadID, err)
			}
			continue
		}
	}
}

func mustCoordinatorCatalog(t *testing.T, input RunInput) gate.WorkloadCatalog {
	t.Helper()
	_, catalog, _, err := buildRemotePlan(input)
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func containsCoordinatorGateID(ids []gate.GateID, wanted gate.GateID) bool {
	return slices.Contains(ids, wanted)
}

func assertCoordinatorAgentIdentityRows(t *testing.T, authorityPath string, want map[string]string) {
	t.Helper()
	database, err := sql.Open("sqlite", authorityPath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for jobID, tokenDigest := range want {
		var got string
		if err := database.QueryRow(`SELECT agent_token_digest FROM ci_run_agent_identities WHERE job_id = ?`, jobID).Scan(&got); err != nil || got != tokenDigest {
			t.Fatalf("job %q agent identity = %q, %v; want %q", jobID, got, err, tokenDigest)
		}
	}
}

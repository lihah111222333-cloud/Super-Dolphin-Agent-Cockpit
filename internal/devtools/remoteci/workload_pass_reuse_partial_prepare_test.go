package remoteci

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const partialPrepareE2EWorkloadCount = 68
const partialPrepareE2EMissIndex = 10

type partialPrepareE2EFixture struct {
	input      RunInput
	plan       gate.GatePlan
	catalog    gate.WorkloadCatalog
	identities []gate.WorkloadPassIdentity
	missID     gate.GateID
}

// runPreparePartialFailedRunProjectsOnlyStrictMiss 锁定失败 run 的逐 workload
// PASS evidence 进入 Prepare/classify/compile/planner 后只为严格 MISS 保留闭环。
func runPreparePartialFailedRunProjectsOnlyStrictMiss(t *testing.T) {
	fixture := newPartialPrepareE2EFixture(t)
	recordPartialPrepareFailedRun(t, fixture)
	secondInput, secondCatalog, second := partialPrepareE2ESecond(t, fixture)
	assertPartialPrepareCompileAndPlan(t, fixture, secondInput, secondCatalog, second)
	assertPartialPrepareFollowupReuse(t, fixture, secondInput, secondCatalog, second)
}

func recordPartialPrepareFailedRun(t *testing.T, fixture partialPrepareE2EFixture) {
	t.Helper()
	failed := partialPrepareE2EFailedRunAt(t, fixture, partialPrepareE2EMissIndex)
	if err := recordRemoteCIRun(fixture.input.LedgerStore, failed, fmt.Errorf("workload %q failed", fixture.missID)); err != nil {
		t.Fatalf("record failed partial run: %v", err)
	}
}

func partialPrepareE2ESecond(t *testing.T, fixture partialPrepareE2EFixture) (RunInput, gate.WorkloadCatalog, remoteWorkloadReusePreparation) {
	t.Helper()
	secondInput := partialPrepareE2EInputForTree(fixture.input, "9")
	secondCatalog := partialPrepareE2ECatalog(fixture.catalog)
	secondCatalog.Workloads[partialPrepareE2EMissIndex].InputDigest = partialPrepareE2EDigest(100)
	secondInput.WorkloadInputDigests[string(fixture.missID)] = partialPrepareE2EDigest(100)
	second := partialPrepareE2EReuse(t, secondInput, secondCatalog)
	if len(second.reusedWorkloads) != partialPrepareE2EWorkloadCount-1 || len(second.cacheMisses) != 1 || second.cacheMisses[0] != fixture.missID {
		t.Fatalf("changed-tree partial reuse = reused=%d misses=%v, want 67/%q", len(second.reusedWorkloads), second.cacheMisses, fixture.missID)
	}
	return secondInput, secondCatalog, second
}

func assertPartialPrepareCompileAndPlan(t *testing.T, fixture partialPrepareE2EFixture, input RunInput, catalog gate.WorkloadCatalog, preparation remoteWorkloadReusePreparation) {
	t.Helper()
	input = assertPartialPrepareCompileInputs(t, fixture, input, catalog, preparation.cacheMisses)
	assertPartialPreparePlan(t, fixture, input, catalog, preparation.cacheMisses)
}

func assertPartialPrepareCompileInputs(t *testing.T, fixture partialPrepareE2EFixture, input RunInput, catalog gate.WorkloadCatalog, cacheMisses []gate.GateID) RunInput {
	t.Helper()
	snapshot, err := loadRemoteGitTreeSnapshot(context.Background(), fixture.input.RepositoryRoot, fixture.input.Tree)
	if err != nil {
		t.Fatalf("load compile fingerprint snapshot: %v", err)
	}
	compileInputs, err := remoteCompileGroupInputsForMisses(context.Background(), snapshot, catalog, cacheMisses)
	if err != nil {
		t.Fatalf("remoteCompileGroupInputsForMisses: %v", err)
	}
	if len(compileInputs) != 1 {
		t.Fatalf("compile-group inputs for partial MISS = %d, want 1", len(compileInputs))
	}
	if _, ok := compileInputs[string(fixture.missID)]; !ok {
		t.Fatalf("compile-group inputs = %v, want only %q", compileInputs, fixture.missID)
	}
	input.WorkloadCompileGroupInputs = make(map[string]gate.CompileGroupInput, len(compileInputs))
	maps.Copy(input.WorkloadCompileGroupInputs, compileInputs)
	return input
}

func assertPartialPreparePlan(t *testing.T, fixture partialPrepareE2EFixture, input RunInput, catalog gate.WorkloadCatalog, cacheMisses []gate.GateID) {
	t.Helper()
	shardSet, err := buildRemoteExecutionShardSetForWorkloads(fixture.plan, catalog, cacheMisses, input)
	if err != nil {
		t.Fatalf("buildRemoteExecutionShardSetForWorkloads: %v", err)
	}
	if len(shardSet.WorkloadPlan.ExecutionWorkloadIDs) != 1 || shardSet.WorkloadPlan.ExecutionWorkloadIDs[0] != fixture.missID {
		t.Fatalf("planned execution IDs = %v, want only %q", shardSet.WorkloadPlan.ExecutionWorkloadIDs, fixture.missID)
	}
	if len(shardSet.WorkloadPlan.Shards) != 1 || len(shardSet.Shards) != 1 || !slices.Equal(shardSet.Shards[0].GateIDs, []gate.GateID{fixture.missID}) {
		t.Fatalf("partial MISS shards = plan=%v containers=%v, want one shard for %q", shardSet.WorkloadPlan.Shards, shardSet.Shards, fixture.missID)
	}
}

func assertPartialPrepareFollowupReuse(t *testing.T, fixture partialPrepareE2EFixture, secondInput RunInput, secondCatalog gate.WorkloadCatalog, second remoteWorkloadReusePreparation) {
	t.Helper()
	fixedFixture := fixture
	fixedFixture.input, fixedFixture.catalog, fixedFixture.identities = secondInput, secondCatalog, second.identities
	fixed := partialPrepareE2EOnePassRun(t, fixedFixture, partialPrepareE2EMissIndex)
	if err := recordRemoteCIRun(secondInput.LedgerStore, fixed, errors.New("coordinator terminal failure after all workload PASS")); err != nil {
		t.Fatalf("record all-PASS non-authoritative run: %v", err)
	}
	treeOnly := partialPrepareE2EReuse(t, partialPrepareE2EInputForTree(secondInput, "a"), secondCatalog)
	if len(treeOnly.reusedWorkloads) != partialPrepareE2EWorkloadCount || len(treeOnly.cacheMisses) != 0 {
		t.Fatalf("tree-only identity reuse = reused=%d misses=%v, want 68/0", len(treeOnly.reusedWorkloads), treeOnly.cacheMisses)
	}
	changedSuccessCatalog := partialPrepareE2ECatalog(secondCatalog)
	changedSuccessInput := partialPrepareE2EInputForTree(secondInput, "b")
	changedSuccessCatalog.Workloads[0].InputDigest = partialPrepareE2EDigest(101)
	changedSuccessCatalog.Workloads[partialPrepareE2EMissIndex].InputDigest = partialPrepareE2EDigest(102)
	changedSuccessInput.WorkloadInputDigests[changedSuccessCatalog.Workloads[0].ID] = partialPrepareE2EDigest(101)
	changedSuccessInput.WorkloadInputDigests[string(fixture.missID)] = partialPrepareE2EDigest(102)
	changedSuccess := partialPrepareE2EReuse(t, changedSuccessInput, changedSuccessCatalog)
	if len(changedSuccess.reusedWorkloads) != partialPrepareE2EWorkloadCount-2 || len(changedSuccess.cacheMisses) != 2 || !slices.Equal(changedSuccess.cacheMisses, []gate.GateID{gate.GateID(changedSuccessCatalog.Workloads[0].ID), fixture.missID}) {
		t.Fatalf("one-success plus failed identity changes = reused=%d misses=%v, want 66/%q,%q", len(changedSuccess.reusedWorkloads), changedSuccess.cacheMisses, changedSuccessCatalog.Workloads[0].ID, fixture.missID)
	}
}

func newPartialPrepareE2EFixture(t *testing.T) partialPrepareE2EFixture {
	t.Helper()
	_, input := coordinatorReuseFixture(t)
	plan, err := gate.BuildGatePlan(input.Profile, input.Source)
	if err != nil {
		t.Fatalf("BuildGatePlan: %v", err)
	}
	catalog, inputDigests := partialPrepareE2ECatalogAndDigests(t)
	input.WorkloadInputDigests = inputDigests
	input.WorkerExecutionSemanticDigest = "sha256:" + strings.Repeat("a", 64)
	ledger := gate.DurationLedger{Version: 1, ShardOverhead: &gate.ShardOrchestrationOverhead{
		SchemaVersion: gate.ShardOrchestrationOverheadSchemaVersion, PolicyVersion: gate.ShardOverheadPolicyVersion,
		Platform: input.Platform, Runner: input.RunnerIdentityDigest, Toolchain: input.ToolchainDigest,
		CalibrationResourceClassID: "calibration", CalibrationResourceCPU: 4, CalibrationResourceMemoryGiB: 8,
		P95MS: 1_000, SampleCount: 1, ProvenanceDigest: "sha256:" + strings.Repeat("d", 64),
		AcceptedGeneration: 1, AcceptedSnapshotID: input.ImageCacheSnapshotID,
	}}
	for _, workload := range catalog.Workloads {
		ledger.Samples = append(ledger.Samples, gate.DurationSample{Bucket: gate.DurationBucket{
			WorkloadID: workload.ID, CommandDigest: workload.CommandDigest, InputDigest: workload.InputDigest,
			Platform: input.Platform, Runner: input.RunnerIdentityDigest, Toolchain: input.ToolchainDigest,
			ExecutionMode: gate.DurationExecutionModeNormal, ResourceClassID: "small", ResourceCPU: 2, ResourceMemoryGiB: 4,
		}, Succeeded: true, DurationMS: 1_000})
	}
	input.LedgerStore, input.LedgerSnapshot = newRemoteRunLedgerAuthority(t, ledger)
	identities, err := remoteWorkloadPassIdentities(context.Background(), input, catalog, 10*time.Minute, testRemoteResourcePolicy())
	if err != nil {
		t.Fatalf("remoteWorkloadPassIdentities: %v", err)
	}
	return partialPrepareE2EFixture{input: input, plan: plan, catalog: catalog, identities: identities, missID: gate.GateID(catalog.Workloads[partialPrepareE2EMissIndex].ID)}
}

func partialPrepareE2ECatalogAndDigests(t *testing.T) (gate.WorkloadCatalog, map[string]string) {
	t.Helper()
	workloads := make([]gate.Workload, 0, partialPrepareE2EWorkloadCount)
	inputDigests := make(map[string]string, partialPrepareE2EWorkloadCount)
	for index := range partialPrepareE2EWorkloadCount {
		packageTarget := fmt.Sprintf("./fixture/bulk%02d", index)
		if index == partialPrepareE2EMissIndex {
			packageTarget = "./internal/fixture"
		}
		var workload gate.Workload
		var err error
		if index == partialPrepareE2EMissIndex {
			workload, err = gate.NewGoTestWorkload(gate.GateIDBackendTestWithGuard, packageTarget, "TestBulkMiss", 1_000)
		} else {
			workload, err = gate.NewGoPackageWorkload(gate.GateIDBackendTestWithGuard, packageTarget, 1_000)
		}
		if err != nil {
			t.Fatalf("NewGoPackageWorkload(%q): %v", packageTarget, err)
		}
		workload.InputDigest = partialPrepareE2EDigest(index)
		workloads = append(workloads, workload)
		inputDigests[workload.ID] = workload.InputDigest
	}
	return gate.WorkloadCatalog{Version: 1, Authoritative: false, Workloads: workloads}, inputDigests
}

func partialPrepareE2ECatalog(catalog gate.WorkloadCatalog) gate.WorkloadCatalog {
	catalog.Workloads = append([]gate.Workload(nil), catalog.Workloads...)
	return catalog
}

func partialPrepareE2EDigest(index int) string {
	return fmt.Sprintf("sha256:%064x", index+1)
}

func partialPrepareE2EInputForTree(input RunInput, marker string) RunInput {
	changed := input
	changed.Tree = strings.Repeat(marker, 40)
	changed.Source = gate.SourceSpec{Kind: gate.SourceKindTree, ObjectFormat: gate.GitObjectFormatSHA1,
		Tree: &gate.TreeSource{SHA: changed.Tree, ParentCommitSHA: input.Commit}, SourceTreeSHA: changed.Tree}
	changed.WorkloadInputDigests = mapsClonePartialPrepare(input.WorkloadInputDigests)
	return changed
}

func mapsClonePartialPrepare(input map[string]string) map[string]string {
	cloned := make(map[string]string, len(input))
	maps.Copy(cloned, input)
	return cloned
}

func partialPrepareE2EReuse(t *testing.T, input RunInput, catalog gate.WorkloadCatalog) remoteWorkloadReusePreparation {
	t.Helper()
	preparation, err := prepareRemoteWorkloadReuse(context.Background(), input, catalog, 10*time.Minute, testRemoteResourcePolicy(), nil, nil)
	if err != nil {
		t.Fatalf("prepareRemoteWorkloadReuse: %v", err)
	}
	return preparation
}

func partialPrepareE2EFailedRunAt(t *testing.T, fixture partialPrepareE2EFixture, failureIndex int) RunResult {
	t.Helper()
	started := time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC)
	shardIdentity := "sha256:" + strings.Repeat("e", 64)
	executions := make([]gate.PlanGateExecution, 0, len(fixture.catalog.Workloads))
	for index, workload := range fixture.catalog.Workloads {
		flags, err := gate.WorkloadExecutionGoFlags(workload.ID)
		if err != nil {
			t.Fatalf("WorkloadExecutionGoFlags(%q): %v", workload.ID, err)
		}
		status, exitCode := gate.ResultStatusPassed, 0
		if index == failureIndex {
			status, exitCode = gate.ResultStatusFailed, 1
		}
		begin := started.Add(time.Duration(index) * time.Millisecond)
		executions = append(executions, gate.PlanGateExecution{GateID: gate.GateID(workload.ID), ShardIdentity: shardIdentity,
			Status: status, ExitCode: exitCode, StartedAt: begin, CompletedAt: begin.Add(3 * time.Millisecond),
			ExecutionProfile: gate.ExecutionProfile{GoFlags: flags, CacheSource: "none", CacheStatus: gate.CacheObservationNotApplicable, CacheMeasurement: "measured", StartupMS: 1, TestBodyMS: 2, TotalMS: 3}})
	}
	shard := completeFailedShardFixture(started, shardIdentity, executions...)
	shard.ContainerGroup = "eci-created"
	result := RunResult{AcceptedGeneration: fixture.input.AcceptedGeneration, JobID: fmt.Sprintf("job-partial-prepare-%d", failureIndex), AgentTokenDigest: fixture.input.AgentTokenDigest,
		Entrypoint: gate.CIEntrypointGitPreCommit, Profile: fixture.input.Profile, PlanDigest: fixture.plan.PlanDigest, SourceTreeSHA: fixture.input.Tree,
		CandidateGateSourceSHA256: fixture.input.CandidateGateSourceSHA256, CandidateGateToolchainSHA256: fixture.input.CandidateGateToolchainSHA256,
		ImageCacheSnapshotID: fixture.input.ImageCacheSnapshotID, RunnerImage: fixture.input.RunnerImage, RunnerIdentityDigest: fixture.input.RunnerIdentityDigest,
		ToolchainDigest: fixture.input.ToolchainDigest, Status: gate.ResultStatusFailed, StartedAt: started, CompletedAt: started.Add(2 * time.Second),
		CleanupComplete: true, Shards: []ShardResult{shard}, WorkloadExecutions: executions,
		FreshWorkloadExecutions: executions, WorkloadPassIdentities: fixture.identities}
	recordPartialResultsCatalog(t, fixture.input.LedgerStore, &result, fixture.catalog, started)
	return result
}

func partialPrepareE2EOnePassRun(t *testing.T, fixture partialPrepareE2EFixture, workloadIndex int) RunResult {
	t.Helper()
	workload := fixture.catalog.Workloads[workloadIndex]
	flags, err := gate.WorkloadExecutionGoFlags(workload.ID)
	if err != nil {
		t.Fatalf("WorkloadExecutionGoFlags(%q): %v", workload.ID, err)
	}
	started := time.Date(2026, 8, 10, 4, 0, 0, 0, time.UTC)
	shardIdentity := "sha256:" + strings.Repeat("f", 64)
	execution := gate.PlanGateExecution{GateID: gate.GateID(workload.ID), ShardIdentity: shardIdentity,
		Status: gate.ResultStatusPassed, ExitCode: 0, StartedAt: started, CompletedAt: started.Add(3 * time.Millisecond),
		ExecutionProfile: gate.ExecutionProfile{GoFlags: flags, CacheSource: "none", CacheStatus: gate.CacheObservationNotApplicable, CacheMeasurement: "measured", StartupMS: 1, TestBodyMS: 2, TotalMS: 3}}
	shard := completeFailedShardFixture(started, shardIdentity, execution)
	shard.ContainerGroup = "eci-created"
	result := RunResult{AcceptedGeneration: fixture.input.AcceptedGeneration, JobID: "job-partial-prepare-miss-pass", AgentTokenDigest: fixture.input.AgentTokenDigest,
		Entrypoint: gate.CIEntrypointGitPreCommit, Profile: fixture.input.Profile, PlanDigest: fixture.plan.PlanDigest, SourceTreeSHA: fixture.input.Tree,
		CandidateGateSourceSHA256: fixture.input.CandidateGateSourceSHA256, CandidateGateToolchainSHA256: fixture.input.CandidateGateToolchainSHA256,
		ImageCacheSnapshotID: fixture.input.ImageCacheSnapshotID, RunnerImage: fixture.input.RunnerImage, RunnerIdentityDigest: fixture.input.RunnerIdentityDigest,
		ToolchainDigest: fixture.input.ToolchainDigest, Status: gate.ResultStatusFailed, StartedAt: started, CompletedAt: started.Add(time.Second),
		CleanupComplete: true, Shards: []ShardResult{shard}, WorkloadExecutions: []gate.PlanGateExecution{execution},
		FreshWorkloadExecutions: []gate.PlanGateExecution{execution}, WorkloadPassIdentities: []gate.WorkloadPassIdentity{fixture.identities[workloadIndex]}}
	recordPartialResultsCatalog(t, fixture.input.LedgerStore, &result, fixture.catalog, started)
	return result
}

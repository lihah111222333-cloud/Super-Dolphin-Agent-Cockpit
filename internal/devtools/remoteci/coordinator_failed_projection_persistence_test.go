package remoteci

import (
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// TestRecordRemoteCIRunPersistsCompleteFailedProjectionAndPartialEvidence 锁定失败 run 的
// SQLite 投影与逐 workload PASS：可测的 passed 独立复用，failed/cancelled 只留下 MISS。
func TestRecordRemoteCIRunPersistsCompleteFailedProjectionAndPartialEvidence(t *testing.T) {
	testCase := newCompleteFailedProjectionCase(t)
	stored := loadCompleteFailedProjection(t, testCase)
	assertCompleteFailedProjectionStored(t, testCase, stored)
	reused := lookupCompleteFailedProjectionEvidence(t, testCase)
	assertCompleteFailedProjectionEvidence(t, testCase, reused)
}

type completeFailedProjectionCase struct {
	store      *gate.DurationLedgerStore
	result     RunResult
	ids        []gate.GateID
	identities []gate.WorkloadPassIdentity
}

func newCompleteFailedProjectionCase(t *testing.T) completeFailedProjectionCase {
	t.Helper()
	store := newPartialResultsLedgerStore(t)
	started := time.Date(2026, 8, 7, 2, 0, 0, 0, time.UTC)
	ids := []gate.GateID{"guard:passed", "guard:failed", "guard:cancelled"}
	catalog := completeFailedCatalogFixture(ids...)
	result := completeFailedProjectionResult(t, store, started, ids, catalog)
	identities := completeFailedProjectionIdentities(t, catalog)
	result.WorkloadPassIdentities = identities
	return completeFailedProjectionCase{store: store, result: mustRecordCompleteFailedProjection(t, store, result), ids: ids, identities: identities}
}

func completeFailedProjectionResult(t *testing.T, store *gate.DurationLedgerStore, started time.Time, ids []gate.GateID, catalog gate.WorkloadCatalog) RunResult {
	t.Helper()
	result := RunResult{
		AcceptedGeneration: 1, JobID: "job-complete-failed-projection", AgentTokenDigest: testRemoteAgentTokenDigest,
		Entrypoint: gate.CIEntrypointGitPreCommit, Profile: gate.ProfileLocalFast, PlanDigest: "sha256:" + strings.Repeat("b", 64),
		SourceTreeSHA: strings.Repeat("f", 40), CandidateGateSourceSHA256: "sha256:" + strings.Repeat("c", 64),
		CandidateGateToolchainSHA256: "sha256:" + strings.Repeat("d", 64), ImageCacheSnapshotID: "snapshot-1", RunnerImage: "ubuntu:22.04",
		Status: gate.ResultStatusFailed, StartedAt: started, CompletedAt: started.Add(2 * time.Second), CleanupComplete: true,
	}
	recordPartialResultsCatalog(t, store, &result, catalog, started)
	shardIdentity := "sha256:" + strings.Repeat("a", 64)
	passed := completeFailedProjectionWorkloadExecution(ids[0], shardIdentity, gate.ResultStatusPassed, 0, started)
	failed := completeFailedProjectionWorkloadExecution(ids[1], shardIdentity, gate.ResultStatusFailed, 1, started.Add(4*time.Millisecond))
	cancelled := completeFailedProjectionWorkloadExecution(ids[2], shardIdentity, gate.ResultStatusCancelled, -1, failed.CompletedAt)
	result.FreshWorkloadExecutions = []gate.PlanGateExecution{passed, failed, cancelled}
	result.WorkloadExecutions = append([]gate.PlanGateExecution(nil), result.FreshWorkloadExecutions...)
	result.GateExecutions = []gate.PlanGateExecution{
		completeFailedProjectionAggregateExecution(ids[0], gate.ResultStatusPassed, 0, started),
		completeFailedProjectionAggregateExecution(ids[1], gate.ResultStatusFailed, 1, started.Add(4*time.Millisecond)),
		completeFailedProjectionAggregateExecution(ids[2], gate.ResultStatusCancelled, -1, failed.CompletedAt),
	}
	result.Shards = []ShardResult{completeFailedShardFixture(started, shardIdentity, passed, failed, cancelled)}
	result.Shards[0].ContainerGroup = "eci-created"
	if err := result.Shards[0].MaterializationTiming.Validate(); err != nil {
		t.Fatalf("fixture materialization timing invalid: %v (%#v)", err, result.Shards[0].MaterializationTiming)
	}
	return result
}

func completeFailedProjectionIdentities(t *testing.T, catalog gate.WorkloadCatalog) []gate.WorkloadPassIdentity {
	t.Helper()
	identities := make([]gate.WorkloadPassIdentity, 0, len(catalog.Workloads))
	inputDigests := make(map[string]string, len(catalog.Workloads))
	for _, workload := range catalog.Workloads {
		inputDigests[workload.ID] = workload.InputDigest
		identity, err := remoteWorkloadPassIdentity(workload, inputDigests, "sha256:"+strings.Repeat("e", 64))
		if err != nil {
			t.Fatalf("remoteWorkloadPassIdentity(%q): %v", workload.ID, err)
		}
		identities = append(identities, identity)
	}
	return identities
}

func mustRecordCompleteFailedProjection(t *testing.T, store *gate.DurationLedgerStore, result RunResult) RunResult {
	t.Helper()
	if err := recordRemoteCIRun(store, result, nil); err != nil {
		t.Fatalf("recordRemoteCIRun() = %v", err)
	}
	return result
}

func loadCompleteFailedProjection(t *testing.T, testCase completeFailedProjectionCase) gate.RemoteCIRunRecord {
	t.Helper()
	stored, err := testCase.store.LoadRemoteCIRun(testCase.result.JobID)
	if err != nil {
		t.Fatalf("LoadRemoteCIRun() = %v", err)
	}
	return stored
}

func assertCompleteFailedProjectionStored(t *testing.T, testCase completeFailedProjectionCase, stored gate.RemoteCIRunRecord) {
	t.Helper()
	if stored.Authoritative || stored.Status != gate.ResultStatusFailed || !stored.CleanupComplete {
		t.Fatalf("stored failed run authority/status/cleanup = auth=%t status=%s cleanup=%t", stored.Authoritative, stored.Status, stored.CleanupComplete)
	}
	if len(stored.Shards) != 1 || len(stored.WorkloadExecutions) != len(testCase.ids) || len(stored.WorkloadResults) != len(testCase.ids) || len(stored.TimingObservations) == 0 {
		t.Fatalf("stored failed projection counts = shards=%d workload_executions=%d results=%d timing=%d", len(stored.Shards), len(stored.WorkloadExecutions), len(stored.WorkloadResults), len(stored.TimingObservations))
	}
}

func lookupCompleteFailedProjectionEvidence(t *testing.T, testCase completeFailedProjectionCase) map[string]gate.WorkloadPassEvidence {
	t.Helper()
	reused, err := lookupRemoteWorkloadPasses(testCase.store, testCase.identities)
	if err != nil {
		t.Fatalf("lookupRemoteWorkloadPasses() = %v", err)
	}
	return reused
}

func assertCompleteFailedProjectionEvidence(t *testing.T, testCase completeFailedProjectionCase, reused map[string]gate.WorkloadPassEvidence) {
	t.Helper()
	if len(reused) != 1 || reused[string(testCase.ids[0])].OriginJobID != testCase.result.JobID {
		t.Fatalf("reused failed-run PASS evidence = %#v, want only %q", reused, testCase.ids[0])
	}
	_, misses := classifyRemoteWorkloadPasses(testCase.identities, reused)
	if len(misses) != 2 || !containsCoordinatorGateID(misses, testCase.ids[1]) || !containsCoordinatorGateID(misses, testCase.ids[2]) {
		t.Fatalf("same-tree failed-run misses = %v, want %v", misses, testCase.ids[1:])
	}
}

func completeFailedProjectionWorkloadExecution(workloadID gate.GateID, shardIdentity string, status gate.ResultStatus, exitCode int, started time.Time) gate.PlanGateExecution {
	completed := started
	profile := gate.ExecutionProfile{CacheSource: "none", CacheStatus: gate.CacheObservationNotApplicable, CacheMeasurement: "measured"}
	if status == gate.ResultStatusPassed {
		completed = started.Add(3 * time.Millisecond)
		profile.StartupMS, profile.TestBodyMS, profile.TotalMS = 1, 2, 3
	}
	return gate.PlanGateExecution{GateID: workloadID, ShardIdentity: shardIdentity, Status: status, ExitCode: exitCode, StartedAt: started, CompletedAt: completed, ExecutionProfile: profile}
}

func completeFailedProjectionAggregateExecution(workloadID gate.GateID, status gate.ResultStatus, exitCode int, started time.Time) gate.PlanGateExecution {
	return gate.PlanGateExecution{GateID: workloadID, Status: status, ExitCode: exitCode, StartedAt: started, CompletedAt: started.Add(3 * time.Millisecond), ExecutionProfile: gate.ExecutionProfile{CacheSource: "none", CacheStatus: gate.CacheObservationNotApplicable, CacheMeasurement: "measured", StartupMS: 1, TestBodyMS: 2, TotalMS: 3}}
}

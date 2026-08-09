package remoteci

import (
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// TestRemoteFailedExactGoProjectionPersistsWithoutPassEvidence 锁定 failure-path：
// strict projection 必须接受带 canonical GoFlags 的失败/取消 exact workload，
// 但 worker failure 只能形成 provisional 诊断，不能提升 PASS evidence。
func TestRemoteFailedExactGoProjectionPersistsWithoutPassEvidence(t *testing.T) {
	for _, test := range []struct {
		name   string
		parent gate.GateID
	}{
		{name: "normal", parent: gate.GateIDBackendTestWithGuard},
		{name: "race", parent: gate.GateIDBackendTestGuardWithRace},
	} {
		t.Run(test.name, func(t *testing.T) {
			runRemoteFailedExactGoProjectionCase(t, test.parent)
		})
	}
}

func runRemoteFailedExactGoProjectionCase(t *testing.T, parent gate.GateID) {
	t.Helper()
	workload := mustRemoteFailedExactGoWorkload(t, parent)
	started := time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC)
	shardID := "sha256:" + strings.Repeat("a", 64)
	profile := mustRemoteFailedExactGoProfile(t, gate.GateID(workload.ID))
	execution := gate.PlanGateExecution{
		GateID: gate.GateID(workload.ID), ShardIdentity: shardID, Status: gate.ResultStatusCancelled, ExitCode: -1,
		StartedAt: started, CompletedAt: started, ExecutionProfile: profile,
	}
	report := gate.PlanExecutionReport{
		SchemaVersion: gate.ExecutorPlanReportSchemaVersion,
		ExecutionOutcome: gate.WorkerExecutionOutcome{
			Status: gate.WorkerExecutionStatusFailed, ExitCode: 17,
			ReasonCode: gate.WorkerExecutionReasonExecutionError,
		},
		Gates: []gate.PlanGateExecution{execution},
	}
	shard := partialRemoteShardResult(shardID, "eci-failed-exact-go", "Failed", gate.GateID(workload.ID), started, report)
	fresh, workerErr := remoteFreshWorkloadExecutions([]gate.Workload{workload}, []ShardResult{shard})
	projected := requireRemoteFailedExactGoProjection(t, fresh, workload, profile, workerErr)
	assertRemoteFailedExactGoPersistence(t, workload, shard, projected, profile, started, workerErr)
}

func requireRemoteFailedExactGoProjection(
	t *testing.T,
	fresh map[string]gate.PlanGateExecution,
	workload gate.Workload,
	profile gate.ExecutionProfile,
	workerErr error,
) gate.PlanGateExecution {
	t.Helper()
	if workerErr == nil || !strings.Contains(workerErr.Error(), "remote worker execution failed") {
		t.Fatalf("remoteFreshWorkloadExecutions() error = %v, want worker failure", workerErr)
	}
	projected, ok := fresh[string(workload.ID)]
	if !ok {
		t.Fatalf("remoteFreshWorkloadExecutions() omitted failed exact workload: %#v", fresh)
	}
	if projected.ExecutionProfile.GoFlags != profile.GoFlags {
		t.Fatalf("projected exact GoFlags = %q, want %q", projected.ExecutionProfile.GoFlags, profile.GoFlags)
	}
	return projected
}

func assertRemoteFailedExactGoPersistence(
	t *testing.T,
	workload gate.Workload,
	shard ShardResult,
	projected gate.PlanGateExecution,
	profile gate.ExecutionProfile,
	started time.Time,
	workerErr error,
) {
	t.Helper()
	store := newPartialResultsLedgerStore(t)
	catalog := gate.WorkloadCatalog{Version: 1, Authoritative: true, Workloads: []gate.Workload{workload}}
	result := remoteFailedExactGoRunResult(shard, projected, started)
	recordPartialResultsCatalog(t, store, &result, catalog, started)
	identity, err := remoteWorkloadPassIdentity(workload, map[string]string{workload.ID: workload.InputDigest}, testDurationDigest("e"))
	if err != nil {
		t.Fatalf("remoteWorkloadPassIdentity() = %v", err)
	}
	result.WorkloadPassIdentities = []gate.WorkloadPassIdentity{identity}
	if err := recordRemoteCIRun(store, result, workerErr); err != nil {
		t.Fatalf("recordRemoteCIRun() = %v", err)
	}
	stored, err := store.LoadRemoteCIRun(result.JobID)
	if err != nil {
		t.Fatalf("LoadRemoteCIRun() = %v", err)
	}
	assertStoredRemoteFailedExactGoProjection(t, stored, profile)
	reused, err := lookupRemoteWorkloadPasses(store, []gate.WorkloadPassIdentity{identity})
	if err != nil {
		t.Fatalf("lookupRemoteWorkloadPasses() = %v", err)
	}
	if len(reused) != 0 {
		t.Fatalf("failed exact projection unexpectedly upgraded PASS evidence: %#v", reused)
	}
}

func assertStoredRemoteFailedExactGoProjection(t *testing.T, stored gate.RemoteCIRunRecord, profile gate.ExecutionProfile) {
	t.Helper()
	if stored.Authoritative || stored.Status != gate.ResultStatusFailed || !strings.Contains(stored.ErrorText, "remote worker execution failed") {
		t.Fatalf("stored failed exact projection = auth=%t status=%s error=%q", stored.Authoritative, stored.Status, stored.ErrorText)
	}
	if len(stored.WorkloadExecutions) != 1 || stored.WorkloadExecutions[0].ExecutionProfile.GoFlags != profile.GoFlags {
		t.Fatalf("stored failed exact workload projection = %#v", stored.WorkloadExecutions)
	}
}

func mustRemoteFailedExactGoWorkload(t *testing.T, parent gate.GateID) gate.Workload {
	t.Helper()
	workload, err := gate.NewGoTestWorkload(parent, "./internal/archtest", "TestPendingRemoteFailure", 1)
	if err != nil {
		t.Fatalf("NewGoTestWorkload(%q) = %v", parent, err)
	}
	workload.InputDigest = testDurationDigest("f")
	return workload
}

func mustRemoteFailedExactGoProfile(t *testing.T, workloadID gate.GateID) gate.ExecutionProfile {
	t.Helper()
	flags, err := gate.WorkloadExecutionGoFlags(string(workloadID))
	if err != nil {
		t.Fatalf("WorkloadExecutionGoFlags(%q) = %v", workloadID, err)
	}
	profile := gate.ExecutionProfile{GoFlags: flags, CacheSource: "none", CacheStatus: gate.CacheObservationNotApplicable, CacheMeasurement: "measured"}
	if err := profile.Validate(); err != nil {
		t.Fatalf("failed exact Go profile validation = %v", err)
	}
	return profile
}

func remoteFailedExactGoRunResult(shard ShardResult, execution gate.PlanGateExecution, started time.Time) RunResult {
	return RunResult{
		AcceptedGeneration: 1, JobID: "job-failed-exact-go-projection", AgentTokenDigest: testRemoteAgentTokenDigest,
		Entrypoint: gate.CIEntrypointGitPreCommit, Profile: gate.ProfileLocalFast, PlanDigest: testDurationDigest("b"),
		SourceTreeSHA: strings.Repeat("f", 40), CandidateGateSourceSHA256: testDurationDigest("c"), CandidateGateToolchainSHA256: testDurationDigest("d"),
		ImageCacheSnapshotID: "snapshot-1", RunnerImage: "ubuntu:22.04", Status: gate.ResultStatusFailed,
		StartedAt: started, CompletedAt: started.Add(2 * time.Second), CleanupComplete: true,
		Shards: []ShardResult{shard}, FreshWorkloadExecutions: []gate.PlanGateExecution{execution},
		WorkloadExecutions: []gate.PlanGateExecution{execution},
		GateExecutions:     nil,
	}
}

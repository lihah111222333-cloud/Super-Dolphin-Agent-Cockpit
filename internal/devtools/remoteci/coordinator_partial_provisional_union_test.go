package remoteci

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// TestRecordRemoteCIRunPersistsCleanedFailedRunWithTimingGaps 验证失败且清理完成时，
// 合法 fresh 执行仍能保留，计时缺口只进入诊断。
func TestRecordRemoteCIRunPersistsCleanedFailedRunWithTimingGaps(t *testing.T) {
	started := time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC)
	ids := []gate.GateID{"guard:passed-gap", "guard:failed-gap", "guard:missing-gap"}
	catalog := completeFailedCatalogFixture(ids...)
	store := newPartialResultsLedgerStore(t)
	result := RunResult{
		AcceptedGeneration: 1, ImageCacheSnapshotID: "snapshot-1", JobID: "job-partial-provisional-union",
		AgentTokenDigest: testRemoteAgentTokenDigest, Entrypoint: gate.CIEntrypointGitPreCommit,
		Profile: gate.ProfileLocalFast, PlanDigest: "sha256:" + strings.Repeat("e", 64),
		CatalogDigest: "sha256:" + strings.Repeat("f", 64), SourceTreeSHA: strings.Repeat("a", 40),
		CandidateGateSourceSHA256: "sha256:" + strings.Repeat("b", 64), CandidateGateToolchainSHA256: "sha256:" + strings.Repeat("c", 64),
		RunnerImage: "ubuntu:22.04", Status: gate.ResultStatusFailed, StartedAt: started,
		CompletedAt: started.Add(time.Minute), CleanupComplete: true,
	}
	recordPartialResultsCatalog(t, store, &result, catalog, started)
	shardIdentity := "sha256:" + strings.Repeat("d", 64)
	passed := completeFailedProjectionWorkloadExecution(ids[0], shardIdentity, gate.ResultStatusPassed, 0, started)
	failed := completeFailedProjectionWorkloadExecution(ids[1], shardIdentity, gate.ResultStatusFailed, 1, started.Add(4*time.Millisecond))
	failed.CompletedAt = failed.StartedAt.Add(20 * time.Millisecond)
	failed.ExecutionProfile.StartupMS, failed.ExecutionProfile.TestBodyMS, failed.ExecutionProfile.TotalMS = 5, 0, 20
	result.FreshWorkloadExecutions = []gate.PlanGateExecution{passed, failed}
	result.WorkloadExecutions = append([]gate.PlanGateExecution(nil), result.FreshWorkloadExecutions...)
	result.WorkloadPassIdentities = completeFailedProjectionIdentities(t, catalog)[:2]
	result.Shards = []ShardResult{completeFailedShardFixture(started, shardIdentity, passed, failed)}
	result.Shards[0].ExecutedWorkloads = append(result.Shards[0].ExecutedWorkloads, ids[2])
	result.Shards[0].ContainerGroup = "eci-created"
	assertPartialShardMaterializationTiming(t, result.Shards[0])

	timingErr := errors.New("remote workload guard:missing-gap has no matching result")
	if err := recordRemoteCIRun(store, result, timingErr); err != nil {
		t.Fatalf("recordRemoteCIRun() = %v, want provisional persistence", err)
	}
	stored, err := store.LoadRemoteCIRun(result.JobID)
	if err != nil {
		t.Fatalf("LoadRemoteCIRun() = %v", err)
	}
	if len(stored.WorkloadExecutions) != 2 || len(stored.WorkloadResults) != 2 {
		t.Fatalf("stored partial projection = executions=%d results=%d, want fresh=2 results=2", len(stored.WorkloadExecutions), len(stored.WorkloadResults))
	}
	if !strings.Contains(stored.ErrorText, "has no matching result") && !strings.Contains(stored.ErrorText, "coverage") {
		t.Fatalf("stored error_text = %q, want timing/coverage diagnostic", stored.ErrorText)
	}
	evidence, err := lookupRemoteWorkloadPasses(store, result.WorkloadPassIdentities)
	if err != nil {
		t.Fatalf("lookupRemoteWorkloadPasses() = %v", err)
	}
	if len(evidence) != 1 || evidence[string(ids[0])].OriginJobID != result.JobID {
		t.Fatalf("partial provisional evidence = %#v, want only %q", evidence, ids[0])
	}
}

func assertPartialShardMaterializationTiming(t *testing.T, shard ShardResult) {
	t.Helper()
	if err := shard.MaterializationTiming.Validate(); err != nil {
		t.Fatalf("fixture materialization timing invalid: %v", err)
	}
}

// TestRecordRemoteCIRunKeepsFreshExecutionsSeparateFromReusedResults 验证 partial run 的 fresh execution 与 reused proof 不混写。
func TestRecordRemoteCIRunKeepsFreshExecutionsSeparateFromReusedResults(t *testing.T) {
	origin := newCompleteFailedProjectionCase(t)
	originEvidence, err := lookupRemoteWorkloadPasses(origin.store, origin.identities)
	if err != nil {
		t.Fatalf("lookup origin evidence = %v", err)
	}
	reused := originEvidence[string(origin.ids[0])]
	started := time.Date(2026, 8, 10, 2, 0, 0, 0, time.UTC)
	consumer := origin.result
	consumer.JobID = "job-partial-fresh-reused"
	consumer.StartedAt, consumer.CompletedAt = started, started.Add(time.Minute)
	consumer.Status, consumer.CleanupComplete = gate.ResultStatusFailed, true
	fresh := completeFailedProjectionWorkloadExecution(origin.ids[1], origin.result.Shards[0].ShardIdentity, gate.ResultStatusPassed, 0, started)
	consumer.FreshWorkloadExecutions = []gate.PlanGateExecution{fresh}
	consumer.WorkloadExecutions = []gate.PlanGateExecution{fresh, reused.OriginExecution}
	consumer.ReusedWorkloads = []gate.WorkloadPassEvidence{reused}
	consumer.WorkloadPassIdentities = origin.identities
	consumer.GateExecutions = []gate.PlanGateExecution{completeFailedProjectionAggregateExecution(origin.ids[1], gate.ResultStatusPassed, 0, started)}
	consumer.Shards = []ShardResult{completeFailedShardFixture(started, origin.result.Shards[0].ShardIdentity, fresh)}
	consumer.Shards[0].ContainerGroup = "eci-created"
	mustRecordRemotePartialReuse(t, origin.store, consumer)
	stored, err := origin.store.LoadRemoteCIRun(consumer.JobID)
	if err != nil {
		t.Fatalf("LoadRemoteCIRun() = %v", err)
	}
	if len(stored.WorkloadExecutions) != 1 || stored.WorkloadExecutions[0].GateID != origin.ids[1] {
		t.Fatalf("stored current executions = %#v, want only fresh %q", stored.WorkloadExecutions, origin.ids[1])
	}
	if len(stored.WorkloadResults) != 2 {
		t.Fatalf("stored workload results = %d, want fresh+reused", len(stored.WorkloadResults))
	}
	var reusedResult gate.RemoteCIWorkloadResult
	for _, result := range stored.WorkloadResults {
		if result.Disposition == gate.WorkloadDispositionReused {
			reusedResult = result
		}
	}
	if reusedResult.OriginJobID != origin.result.JobID || reusedResult.EvidenceSHA256 != reused.EvidenceSHA256 {
		t.Fatalf("stored reused proof = %#v, want origin %q", reusedResult, origin.result.JobID)
	}
}

func mustRecordRemotePartialReuse(t *testing.T, store *gate.DurationLedgerStore, result RunResult) {
	t.Helper()
	if err := recordRemoteCIRun(store, result, errors.New("remote worker failed after partial completion")); err != nil {
		t.Fatalf("recordRemoteCIRun() = %v", err)
	}
}

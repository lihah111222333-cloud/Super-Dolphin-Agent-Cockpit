package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func TestRemoteRunCheckObservations(t *testing.T) {
	plan, catalog := remoteRunReceiptTestPlanAndCatalog(t)
	complete := remoteRunReceiptTestResult(t, plan, catalog)
	store := remoteRunReceiptTestAuthority(t, complete)

	t.Run("complete authoritative release mints six actual receipts", func(t *testing.T) {
		input := remoteci.RunInput{AcceptedGeneration: 7, Profile: plan.Profile, Source: plan.Source, ImageCacheSnapshotID: "snapshot-7", LedgerStore: store}
		observations, receipts, err := validateRemoteRunContract(input, 7, complete)
		if err != nil {
			t.Fatal(err)
		}
		if err := cicontract.ValidateRequiredChecksObservedPass(observations); err != nil {
			t.Fatalf("ValidateRequiredChecksObservedPass() error = %v", err)
		}
		if len(observations) != len(cicontract.RequiredChecks()) {
			t.Fatalf("observation count = %d, want %d", len(observations), len(cicontract.RequiredChecks()))
		}
		for _, observation := range observations {
			if !observation.Executed || !observation.Passed || observation.SourceTree != complete.SourceTreeSHA || observation.AcceptedSnapshotID != "snapshot-7" || observation.PlanDigest != plan.PlanDigest || observation.DurationMS <= 0 || observation.ReceiptSHA256 == "" {
				t.Fatalf("observation is incomplete: %#v", observation)
			}
		}
		if len(receipts) != len(cicontract.RequiredChecks()) {
			t.Fatalf("receipt count = %d, want %d", len(receipts), len(cicontract.RequiredChecks()))
		}
		for _, receipt := range receipts {
			if receipt.RunID != complete.JobID || receipt.JobID != complete.JobID || receipt.CandidateTreeSHA != complete.SourceTreeSHA || receipt.AcceptedGeneration != 7 || receipt.AcceptedSnapshotID != "snapshot-7" || !receipt.Executed || !receipt.Passed || receipt.Duration <= 0 {
				t.Fatalf("receipt has invalid binding: %#v", receipt)
			}
			digest, err := gatecontract.CheckReceiptSHA256(receipt)
			if err != nil || digest != receipt.ReceiptSHA256 {
				t.Fatalf("receipt SHA-256 = %q, %v; want %q", digest, err, receipt.ReceiptSHA256)
			}
		}
		if err := appendRemoteCheckReceipts(store, receipts); err != nil {
			t.Fatalf("appendRemoteCheckReceipts() error = %v", err)
		}
		if err := validateRemoteRunStoredCheckReceipts(store, complete.JobID, receipts); err != nil {
			t.Fatalf("validateRemoteRunStoredCheckReceipts() error = %v", err)
		}
		recorded, err := store.LoadRemoteCIRun(complete.JobID)
		if err != nil {
			t.Fatalf("LoadRemoteCIRun() error = %v", err)
		}
		if recorded.AcceptedGeneration != input.AcceptedGeneration || len(recorded.TimingObservations) == 0 {
			t.Fatalf("recorded authority = %#v", recorded)
		}
	})

	t.Run("missing workload is rejected", func(t *testing.T) {
		missing := complete
		missing.WorkloadExecutions = missing.WorkloadExecutions[1:]
		if _, err := remoteRunCheckObservations(plan, catalog, "snapshot-7", missing); err == nil {
			t.Fatal("remoteRunCheckObservations() error = nil")
		}
	})

	t.Run("failed workload is rejected", func(t *testing.T) {
		failed := complete
		failed.WorkloadExecutions = append([]gatecontract.PlanGateExecution(nil), complete.WorkloadExecutions...)
		failed.WorkloadExecutions[0].Status = gatecontract.ResultStatusFailed
		if _, err := remoteRunCheckObservations(plan, catalog, "snapshot-7", failed); err == nil {
			t.Fatal("remoteRunCheckObservations() error = nil")
		}
	})

	t.Run("duplicate workload receipt is rejected", func(t *testing.T) {
		duplicate := complete
		duplicate.WorkloadExecutions = append(append([]gatecontract.PlanGateExecution(nil), complete.WorkloadExecutions...), complete.WorkloadExecutions[0])
		if _, err := remoteRunCheckObservations(plan, catalog, "snapshot-7", duplicate); err == nil {
			t.Fatal("remoteRunCheckObservations() error = nil")
		}
	})

	t.Run("missing actual timing is rejected", func(t *testing.T) {
		missingTiming := complete
		missingTiming.WorkloadExecutions = append([]gatecontract.PlanGateExecution(nil), complete.WorkloadExecutions...)
		missingTiming.WorkloadExecutions[0].CompletedAt = time.Time{}
		if _, err := remoteRunCheckObservations(plan, catalog, "snapshot-7", missingTiming); err == nil {
			t.Fatal("remoteRunCheckObservations() error = nil")
		}
	})
}

func remoteRunReceiptTestAuthority(t *testing.T, complete remoteci.RunResult) *gatecontract.DurationLedgerStore {
	t.Helper()
	store, err := gatecontract.NewDurationLedgerStore(filepath.Join(t.TempDir(), "remote-ci-authority.sqlite"))
	if err != nil {
		t.Fatalf("NewDurationLedgerStore() error = %v", err)
	}
	for generation := range uint64(7) {
		if _, err := store.CompareAndSwap(generation, gatecontract.NewDurationLedger()); err != nil {
			t.Fatalf("initialize authority generation %d: %v", generation+1, err)
		}
	}
	seedRemoteRunTestAcceptedGeneration(t, store, 7)
	startedAt := time.Date(2026, time.August, 3, 10, 0, 0, 0, time.UTC)
	workloadID := gatecontract.GateID("guard:authority")
	shardID := "sha256:" + strings.Repeat("c", 64)
	catalog := gatecontract.WorkloadCatalog{Version: 1, Authoritative: true, Workloads: []gatecontract.Workload{{
		ID: string(workloadID), Kind: gatecontract.WorkloadKindGuard, CommandDigest: strings.Repeat("d", 64), BootstrapEstimateMS: 1_000, Shardable: true,
	}}}
	catalogDigest, err := gatecontract.WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatalf("WorkloadCatalogDigest() error = %v", err)
	}
	if err := store.RecordWorkloadCatalog(catalog, gatecontract.WorkloadCatalogObservation{
		SourceTreeSHA: complete.SourceTreeSHA, Entrypoint: gatecontract.CIEntrypointRelease,
		Profile: gatecontract.ProfileRelease, AcceptedGeneration: 7, ObservedAt: startedAt,
	}); err != nil {
		t.Fatalf("RecordWorkloadCatalog() error = %v", err)
	}
	profile := gatecontract.ExecutionProfile{CacheSource: "go_build_cache", CacheStatus: "miss", CacheMeasurement: "measured", StartupMS: 1, TestBodyMS: 10, TotalMS: 11}
	execution := gatecontract.PlanGateExecution{
		GateID: workloadID, ShardIdentity: shardID, Status: gatecontract.ResultStatusPassed, ExitCode: 0,
		StartedAt: startedAt.Add(3 * time.Millisecond), CompletedAt: startedAt.Add(14 * time.Millisecond), ExecutionProfile: profile,
	}
	shard := gatecontract.RemoteCIShardRecord{
		ShardIdentity: shardID, ContainerGroup: "eci-authority", ContainerStatus: "Succeeded", Workloads: []gatecontract.GateID{workloadID},
		MaterializationTiming: gatecontract.ShardMaterializationTiming{Measurement: gatecontract.MaterializationMeasurementMeasured, ShardIdentity: shardID,
			Source:           gatecontract.MaterializationPhaseTiming{StartedAtUnixMS: startedAt.Add(time.Millisecond).UnixMilli(), CompletedAtUnixMS: startedAt.Add(2 * time.Millisecond).UnixMilli(), MaterializeMS: 1},
			CandidateCompile: gatecontract.MaterializationPhaseTiming{StartedAtUnixMS: startedAt.Add(2 * time.Millisecond).UnixMilli(), CompletedAtUnixMS: startedAt.Add(3 * time.Millisecond).UnixMilli(), MaterializeMS: 1},
		},
	}
	record := gatecontract.RemoteCIRunRecord{
		JobID: complete.JobID, Entrypoint: gatecontract.CIEntrypointRelease, Profile: gatecontract.ProfileRelease, AcceptedGeneration: 7,
		PlanDigest: "sha256:authority", CatalogDigest: catalogDigest, SourceTreeSHA: complete.SourceTreeSHA,
		CandidateGateSourceSHA256: "sha256:" + strings.Repeat("e", 64), CandidateGateToolchainSHA256: "sha256:" + strings.Repeat("f", 64),
		RunnerImage: "ubuntu:22.04", Status: gatecontract.ResultStatusFailed, Authoritative: true,
		StartedAt: startedAt, CompletedAt: startedAt.Add(20 * time.Millisecond), Shards: []gatecontract.RemoteCIShardRecord{shard}, WorkloadExecutions: []gatecontract.PlanGateExecution{execution},
	}
	record.TimingObservations = remoteRunReceiptTestTimingObservations(record.JobID, shard, execution, startedAt)
	if err := store.RecordRemoteCIRun(record); err != nil {
		t.Fatalf("RecordRemoteCIRun() error = %v", err)
	}
	return store
}

func remoteRunReceiptTestTimingObservations(jobID string, shard gatecontract.RemoteCIShardRecord, execution gatecontract.PlanGateExecution, startedAt time.Time) []gatecontract.TimingObservation {
	measured := func(scope cicontract.TimingScope, shardID string, workloadID gatecontract.GateID, phase cicontract.TimingPhase, start, end time.Time, aggregation cicontract.TimingAggregation, evidence gatecontract.CacheEvidence) gatecontract.TimingObservation {
		return gatecontract.TimingObservation{JobID: jobID, Scope: scope, ShardIdentity: shardID, WorkloadID: workloadID, Phase: phase, StartedAt: start, CompletedAt: end, DurationMS: end.Sub(start).Milliseconds(), Measurement: cicontract.ObservationMeasured, Aggregation: aggregation, CacheEvidence: evidence}
	}
	observations := []gatecontract.TimingObservation{measured(cicontract.TimingScopeRun, "", "", cicontract.TimingTotal, startedAt, startedAt.Add(20*time.Millisecond), cicontract.TimingAggregationCriticalPath, gatecontract.NewNotApplicableCacheEvidence("run_has_no_workload_cache"))}
	intervals := map[cicontract.TimingPhase][2]time.Time{
		cicontract.TimingECIWait: {startedAt, startedAt.Add(time.Millisecond)}, cicontract.TimingSourceMaterialize: {startedAt.Add(time.Millisecond), startedAt.Add(2 * time.Millisecond)}, cicontract.TimingCandidateCompile: {startedAt.Add(2 * time.Millisecond), startedAt.Add(3 * time.Millisecond)}, cicontract.TimingStartup: {startedAt.Add(3 * time.Millisecond), startedAt.Add(4 * time.Millisecond)}, cicontract.TimingTestBody: {startedAt.Add(4 * time.Millisecond), startedAt.Add(14 * time.Millisecond)}, cicontract.TimingTotal: {startedAt, startedAt.Add(20 * time.Millisecond)},
	}
	for _, phase := range cicontract.TimingPhases() {
		aggregation := cicontract.TimingAggregationRaw
		switch phase {
		case cicontract.TimingStartup, cicontract.TimingTestBody:
			aggregation = cicontract.TimingAggregationIntervalUnion
		case cicontract.TimingTotal:
			aggregation = cicontract.TimingAggregationCriticalPath
		}
		interval := intervals[phase]
		observations = append(observations, measured(cicontract.TimingScopeShard, shard.ShardIdentity, "", phase, interval[0], interval[1], aggregation, gatecontract.NewNotApplicableCacheEvidence("shard_has_no_workload_cache")))
		if phase == cicontract.TimingECIWait || phase == cicontract.TimingSourceMaterialize || phase == cicontract.TimingCandidateCompile {
			observations = append(observations, gatecontract.TimingObservation{JobID: jobID, Scope: cicontract.TimingScopeWorkload, ShardIdentity: shard.ShardIdentity, WorkloadID: execution.GateID, Phase: phase, Measurement: cicontract.ObservationNotApplicable, Reason: "shard_scoped:" + shard.ShardIdentity, Aggregation: cicontract.TimingAggregationRaw, CacheEvidence: gatecontract.NewTimingCacheEvidenceFromProfile(execution.ExecutionProfile)})
		}
	}
	for _, phase := range []cicontract.TimingPhase{cicontract.TimingStartup, cicontract.TimingTestBody, cicontract.TimingTotal} {
		interval := intervals[phase]
		if phase == cicontract.TimingTotal {
			interval = [2]time.Time{execution.StartedAt, execution.CompletedAt}
		}
		observations = append(observations, measured(cicontract.TimingScopeWorkload, shard.ShardIdentity, execution.GateID, phase, interval[0], interval[1], cicontract.TimingAggregationRaw, gatecontract.NewTimingCacheEvidenceFromProfile(execution.ExecutionProfile)))
	}
	return observations
}

func TestAppendRemoteCheckReceiptsRejectsStoreFailure(t *testing.T) {
	if err := appendRemoteCheckReceipts(nil, nil); err == nil {
		t.Fatal("appendRemoteCheckReceipts() error = nil")
	}
}

func remoteRunReceiptTestPlanAndCatalog(t *testing.T) (gatecontract.GatePlan, gatecontract.WorkloadCatalog) {
	t.Helper()
	commit := strings.Repeat("a", 40)
	tree := strings.Repeat("b", 40)
	plan, err := gatecontract.BuildGatePlan(gatecontract.ProfileRelease, gatecontract.SourceSpec{
		Kind: gatecontract.SourceKindCommit, ObjectFormat: gatecontract.GitObjectFormatSHA1,
		Commit: &gatecontract.CommitSource{SHA: commit}, SourceTreeSHA: tree,
	})
	if err != nil {
		t.Fatalf("BuildGatePlan() error = %v", err)
	}
	catalog, err := gatecontract.BuildExpandedWorkloadCatalog(plan, gatecontract.DefaultWorkloadBootstrapPolicy(), gatecontract.WorkloadInventory{})
	if err != nil {
		t.Fatalf("BuildExpandedWorkloadCatalog() error = %v", err)
	}
	return plan, catalog
}

func remoteRunReceiptTestResult(t *testing.T, plan gatecontract.GatePlan, catalog gatecontract.WorkloadCatalog) remoteci.RunResult {
	t.Helper()
	catalogDigest, err := gatecontract.WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatalf("WorkloadCatalogDigest() error = %v", err)
	}
	executions := make([]gatecontract.PlanGateExecution, 0, len(catalog.Workloads))
	startedAt := time.Date(2026, time.August, 3, 10, 0, 0, 0, time.UTC)
	for _, workload := range catalog.Workloads {
		executions = append(executions, gatecontract.PlanGateExecution{
			GateID: gatecontract.GateID(workload.ID), Status: gatecontract.ResultStatusPassed,
			StartedAt: startedAt, CompletedAt: startedAt.Add(time.Second),
		})
		startedAt = startedAt.Add(time.Second)
	}
	return remoteci.RunResult{
		AcceptedGeneration: 7,
		JobID:              "remote-job",
		Profile:            plan.Profile, PlanDigest: plan.PlanDigest, CatalogDigest: catalogDigest,
		SourceTreeSHA: plan.Source.SourceTreeSHA, Status: gatecontract.ResultStatusPassed,
		Authoritative: true, WorkloadExecutions: executions,
	}
}

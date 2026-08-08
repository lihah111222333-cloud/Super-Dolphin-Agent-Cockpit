package main

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

const remoteRunReceiptTestAgentTokenDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func TestRemoteRunStoredFreshExecutionMatchesPersistedProjection(t *testing.T) {
	t.Parallel()

	expected := gatecontract.PlanGateExecution{
		ShardIdentity: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		GateID:        "guard:code-size",
		Status:        gatecontract.ResultStatusPassed,
		ExitCode:      0,
		StartedAt:     time.UnixMilli(1_000).UTC(),
		CompletedAt:   time.UnixMilli(1_100).UTC(),
		ArgvDigest:    "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		Log:           gatecontract.PlainTextLog("guard passed\n"),
		LogDigest:     fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("guard passed\n"))),
		TestTimings:   nil,
		ExecutionProfile: gatecontract.ExecutionProfile{
			TotalMS: 100,
		},
	}
	recorded := expected
	recorded.Log = nil
	recorded.TestTimings = []gatecontract.GoTestTiming{}
	if !remoteRunStoredFreshExecutionMatches(recorded, expected) {
		t.Fatal("persisted execution projection does not match its log-digest-bound result")
	}

	recorded.Log = gatecontract.PlainTextLog("unexpected persisted log")
	if remoteRunStoredFreshExecutionMatches(recorded, expected) {
		t.Fatal("persisted execution projection accepted a log body outside the SQLite contract")
	}
	recorded.Log = nil
	recorded.LogDigest = strings.Repeat("0", len(expected.LogDigest))
	if remoteRunStoredFreshExecutionMatches(recorded, expected) {
		t.Fatal("persisted execution projection accepted a different log digest")
	}

	recorded = expected
	recorded.Log = nil
	recorded.ExecutionProfile.TotalMS++
	if remoteRunStoredFreshExecutionMatches(recorded, expected) {
		t.Fatal("persisted execution projection accepted a different execution profile")
	}
}

func TestRemoteRunCheckObservations(t *testing.T) {
	plan, catalog := remoteRunReceiptTestPlanAndCatalog(t)
	complete := remoteRunReceiptTestResult(t, plan, catalog)
	store := remoteRunReceiptTestAuthority(t, catalog, complete)

	t.Run("complete provisional release finalizes six actual receipts", func(t *testing.T) { remoteRunReceiptTestComplete(t, plan, catalog, store, complete) })

	t.Run("complete provisional release reaches the sole finalizer", func(t *testing.T) {
		if !remoteRunIsFullAuthoritativeAcceptance(catalog, complete) {
			t.Fatal("complete provisional release result is not eligible for finalization")
		}
	})

	t.Run("already authoritative result is rejected before finalization", func(t *testing.T) {
		alreadyFinal := complete
		alreadyFinal.Authoritative = true
		if remoteRunIsFullAuthoritativeAcceptance(catalog, alreadyFinal) {
			t.Fatal("already authoritative result bypasses the sole finalizer")
		}
	})

	t.Run("different agent token digest is rejected", func(t *testing.T) {
		remoteRunReceiptTestRejectsDifferentAgentToken(t, remoteRunReceiptTestInput(plan, store), complete)
	})

	t.Run("fresh receipt failures are rejected", func(t *testing.T) { remoteRunReceiptTestFreshFailures(t, plan, catalog, complete) })

	t.Run("all reused workloads mint current receipt intervals and proofs", func(t *testing.T) { remoteRunReceiptTestAllReuse(t, plan, catalog, complete) })

	t.Run("mixed fresh and reused workloads preserve both receipt flags", func(t *testing.T) { remoteRunReceiptTestMixed(t, plan, catalog, complete) })
}

// TestRemoteRunCheckObservationsFollowProfileCatalog 验证快速 profile 的权威回执不伪造
// release-only e2e/race/dependency 检查。
func TestRemoteRunCheckObservationsFollowProfileCatalog(t *testing.T) {
	commit := strings.Repeat("c", 40)
	tree := strings.Repeat("d", 40)
	plan, err := gatecontract.BuildGatePlan(gatecontract.ProfileLocalFast, gatecontract.SourceSpec{
		Kind: gatecontract.SourceKindCommit, ObjectFormat: gatecontract.GitObjectFormatSHA1,
		Commit: &gatecontract.CommitSource{SHA: commit}, SourceTreeSHA: tree,
	})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := gatecontract.BuildExpandedWorkloadCatalog(plan, gatecontract.DefaultWorkloadBootstrapPolicy(), gatecontract.WorkloadInventory{})
	if err != nil {
		t.Fatal(err)
	}
	result := remoteRunReceiptTestResult(t, plan, catalog)
	observations, err := remoteRunCheckObservations(plan, catalog, result.ImageCacheSnapshotID, result)
	if err != nil {
		t.Fatal(err)
	}
	required, err := gatecontract.RequiredChecksForWorkloadCatalog(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := cicontract.ValidateRequiredChecksObservedPassFor(required, observations); err != nil {
		t.Fatal(err)
	}
	if len(required) >= len(cicontract.RequiredChecks()) {
		t.Fatalf("local-fast checks = %v, want strict release subset", required)
	}
	if !remoteRunIsFullAuthoritativeAcceptance(catalog, result) {
		t.Fatal("complete provisional local-fast result is not eligible for profile-scoped finalization")
	}
}

// TestRemoteRunRecordedIdentityMatchesAllBindings 锁定持久回执的运行与候选身份边界。
func TestRemoteRunRecordedIdentityMatchesAllBindings(t *testing.T) {
	input := remoteci.RunInput{
		AcceptedGeneration:   7,
		AgentTokenDigest:     remoteRunReceiptTestAgentTokenDigest,
		Force:                true,
		ImageCacheSnapshotID: "snapshot-7",
	}
	result := remoteci.RunResult{
		AcceptedGeneration:   7,
		AgentTokenDigest:     remoteRunReceiptTestAgentTokenDigest,
		Force:                true,
		ImageCacheSnapshotID: "snapshot-7",
		SourceTreeSHA:        "tree-sha",
		PlanDigest:           "plan-digest",
		CatalogDigest:        "catalog-digest",
		Profile:              gatecontract.ProfileRelease,
		Status:               gatecontract.ResultStatusPassed,
	}
	recorded := gatecontract.RemoteCIRunRecord{
		AcceptedGeneration:   7,
		AgentTokenDigest:     remoteRunReceiptTestAgentTokenDigest,
		Force:                true,
		ImageCacheSnapshotID: "snapshot-7",
		SourceTreeSHA:        "tree-sha",
		PlanDigest:           "plan-digest",
		CatalogDigest:        "catalog-digest",
		Profile:              gatecontract.ProfileRelease,
		Status:               gatecontract.ResultStatusPassed,
	}
	if !remoteRunRecordedIdentityMatches(recorded, input, 7, result) {
		t.Fatal("matching recorded identity was rejected")
	}
	for _, testCase := range []struct {
		name   string
		mutate func(*gatecontract.RemoteCIRunRecord)
	}{
		{name: "force", mutate: func(record *gatecontract.RemoteCIRunRecord) { record.Force = false }},
		{name: "snapshot", mutate: func(record *gatecontract.RemoteCIRunRecord) { record.ImageCacheSnapshotID = "other-snapshot" }},
		{name: "source tree", mutate: func(record *gatecontract.RemoteCIRunRecord) { record.SourceTreeSHA = "other-tree" }},
		{name: "plan", mutate: func(record *gatecontract.RemoteCIRunRecord) { record.PlanDigest = "other-plan" }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			mismatched := recorded
			testCase.mutate(&mismatched)
			if remoteRunRecordedIdentityMatches(mismatched, input, 7, result) {
				t.Fatalf("mismatched %s identity was accepted", testCase.name)
			}
		})
	}
}

func TestRemoteRunContractUsesPersistedInputBoundCatalog(t *testing.T) {
	plan, catalog := remoteRunReceiptTestPlanAndCatalog(t)
	for index := range catalog.Workloads {
		catalog.Workloads[index].InputDigest = "sha256:" + strings.Repeat("9", 64)
	}
	catalogDigest, err := gatecontract.WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	store, err := gatecontract.NewDurationLedgerStore(filepath.Join(t.TempDir(), "remote-ci-catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	for generation := range uint64(7) {
		if _, err := store.CompareAndSwap(generation, gatecontract.NewDurationLedger()); err != nil {
			t.Fatal(err)
		}
	}
	seedRemoteRunTestAcceptedGeneration(t, store, 7)
	if err := store.RecordWorkloadCatalog(catalog, gatecontract.WorkloadCatalogObservation{
		SourceTreeSHA: plan.Source.SourceTreeSHA, Entrypoint: gatecontract.CIEntrypointRelease,
		Profile: plan.Profile, AcceptedGeneration: 7, ObservedAt: time.Date(2026, time.August, 5, 10, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	result := remoteci.RunResult{AcceptedGeneration: 7, CatalogDigest: catalogDigest, SourceTreeSHA: plan.Source.SourceTreeSHA, Entrypoint: gatecontract.CIEntrypointRelease, Profile: plan.Profile}
	input := remoteci.RunInput{Profile: plan.Profile, Source: plan.Source, LedgerStore: store}
	gotPlan, gotCatalog, err := remoteRunContractPlanAndCatalog(input, result)
	if err != nil {
		t.Fatal(err)
	}
	if gotPlan.PlanDigest != plan.PlanDigest {
		t.Fatalf("loaded plan digest = %q, want %q", gotPlan.PlanDigest, plan.PlanDigest)
	}
	if gotCatalog.Workloads[0].InputDigest != catalog.Workloads[0].InputDigest {
		t.Fatalf("loaded catalog lost persisted input digest: got %q want %q", gotCatalog.Workloads[0].InputDigest, catalog.Workloads[0].InputDigest)
	}

	drifted := result
	drifted.SourceTreeSHA = strings.Repeat("e", 40)
	if _, _, err := remoteRunContractPlanAndCatalog(input, drifted); err == nil || !strings.Contains(err.Error(), "no matching observation") {
		t.Fatalf("catalog observation drift error = %v, want strict rejection", err)
	}
}

// 以下辅助函数仅拆分断言，保持回执边界测试的原有语义。
func remoteRunReceiptTestComplete(t *testing.T, plan gatecontract.GatePlan, catalog gatecontract.WorkloadCatalog, store *gatecontract.DurationLedgerStore, complete remoteci.RunResult) {
	t.Helper()
	input := remoteRunReceiptTestInput(plan, store)
	observations := remoteRunReceiptTestObservations(t, plan, catalog, input, complete)
	receipts := remoteRunReceiptTestReceipts(t, complete, observations)
	if err := finalizeRemoteRunReceiptAuthority(input, complete, receipts, nil, nil); err != nil {
		t.Fatal(err)
	}
	assertRemoteRunStoredCheckReceipts(t, store, complete.JobID, receipts)
	recorded, err := store.LoadRemoteCIRun(complete.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if !recorded.Authoritative {
		t.Fatal("finalizer did not promote the provisional remote run")
	}
}

// assertRemoteRunStoredCheckReceipts 保留回执持久化的精确回归断言，并直接读取正式 SQLite 边界。
func assertRemoteRunStoredCheckReceipts(t *testing.T, store *gatecontract.DurationLedgerStore, jobID string, want []gatecontract.CheckReceiptRecord) {
	t.Helper()
	if store == nil {
		t.Fatal("remote CI duration ledger store is required")
	}
	got, err := store.LoadCheckReceipts(jobID)
	if err != nil {
		t.Fatalf("reload remote CI check receipts: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("reloaded remote CI check receipt count = %d, want %d", len(got), len(want))
	}
	wantByCheck := make(map[cicontract.RequiredCheck]gatecontract.CheckReceiptRecord, len(want))
	for _, receipt := range want {
		if _, duplicate := wantByCheck[receipt.RequiredCheck]; duplicate {
			t.Fatalf("expected remote CI check receipt %q is duplicated", receipt.RequiredCheck)
		}
		wantByCheck[receipt.RequiredCheck] = receipt
	}
	for _, receipt := range got {
		expected, found := wantByCheck[receipt.RequiredCheck]
		if !found || receipt != expected {
			t.Fatalf("reloaded remote CI check receipt %q does not exactly match this invocation", receipt.RequiredCheck)
		}
		delete(wantByCheck, receipt.RequiredCheck)
	}
	if len(wantByCheck) != 0 {
		t.Fatalf("reloaded remote CI check receipt collection is incomplete")
	}
}

func TestRemoteRunFinalizerRejectsTamperedAggregateExecution(t *testing.T) {
	plan, catalog := remoteRunReceiptTestPlanAndCatalog(t)
	complete := remoteRunReceiptTestResult(t, plan, catalog)
	store := remoteRunReceiptTestAuthority(t, catalog, complete)
	recorded, err := store.LoadRemoteCIRun(complete.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(recorded.Executions) == 0 {
		t.Fatal("authority fixture has no aggregate execution")
	}
	recorded.Executions[0].LogDigest = strings.Repeat("0", len(recorded.Executions[0].LogDigest))
	if err := store.RecordProvisionalRemoteCIRun(recorded); err != nil {
		t.Fatalf("RecordProvisionalRemoteCIRun() tampered aggregate: %v", err)
	}
	input := remoteRunReceiptTestInput(plan, store)
	observations := remoteRunReceiptTestObservations(t, plan, catalog, input, complete)
	receipts := remoteRunReceiptTestReceipts(t, complete, observations)
	if err := finalizeRemoteRunReceiptAuthority(input, complete, receipts, nil, nil); err == nil {
		t.Fatal("finalizer accepted tampered aggregate execution log digest")
	}
}

// remoteRunReceiptTestObservations 生成并验证完整的当前候选检查观测。
func remoteRunReceiptTestObservations(t *testing.T, plan gatecontract.GatePlan, catalog gatecontract.WorkloadCatalog, input remoteci.RunInput, complete remoteci.RunResult) []cicontract.CheckObservation {
	t.Helper()
	observations, err := remoteRunCheckObservations(plan, catalog, input.ImageCacheSnapshotID, complete)
	if err != nil {
		t.Fatal(err)
	}
	if err := cicontract.ValidateRequiredChecksObservedPass(observations); err != nil {
		t.Fatal(err)
	}
	for _, observation := range observations {
		if !observation.Executed || !observation.Passed || observation.ReceiptSHA256 == "" {
			t.Fatalf("observation is incomplete: %#v", observation)
		}
	}
	return observations
}

// remoteRunReceiptTestReceipts 生成并验证每条检查回执的不可变摘要。
func remoteRunReceiptTestReceipts(t *testing.T, complete remoteci.RunResult, observations []cicontract.CheckObservation) []gatecontract.CheckReceiptRecord {
	t.Helper()
	receipts, err := remoteRunCheckReceipts(complete, 7, observations)
	if err != nil {
		t.Fatal(err)
	}
	for _, receipt := range receipts {
		digest, err := gatecontract.CheckReceiptSHA256(receipt)
		if err != nil || digest != receipt.ReceiptSHA256 {
			t.Fatalf("receipt digest: %q, %v", digest, err)
		}
	}
	return receipts
}

func remoteRunReceiptTestFreshFailures(t *testing.T, plan gatecontract.GatePlan, catalog gatecontract.WorkloadCatalog, complete remoteci.RunResult) {
	t.Helper()
	cases := []func(remoteci.RunResult) remoteci.RunResult{
		func(r remoteci.RunResult) remoteci.RunResult {
			r.FreshWorkloadExecutions = r.FreshWorkloadExecutions[1:]
			return r
		},
		func(r remoteci.RunResult) remoteci.RunResult {
			r.FreshWorkloadExecutions[0].Status = gatecontract.ResultStatusFailed
			return r
		},
		func(r remoteci.RunResult) remoteci.RunResult {
			r.FreshWorkloadExecutions = append(r.FreshWorkloadExecutions, r.FreshWorkloadExecutions[0])
			return r
		},
		func(r remoteci.RunResult) remoteci.RunResult {
			r.FreshWorkloadExecutions[0].CompletedAt = time.Time{}
			return r
		},
	}
	for _, mutate := range cases {
		r := complete
		r.FreshWorkloadExecutions = append([]gatecontract.PlanGateExecution(nil), complete.FreshWorkloadExecutions...)
		remoteRunReceiptTestObservationRejected(t, plan, catalog, mutate(r))
	}
}

func remoteRunReceiptTestAllReuse(t *testing.T, plan gatecontract.GatePlan, catalog gatecontract.WorkloadCatalog, complete remoteci.RunResult) {
	t.Helper()
	reused := remoteRunReceiptTestReuseResult(t, complete, catalog, 0)
	observations, err := remoteRunCheckObservations(plan, catalog, "snapshot-7", reused)
	if err != nil {
		t.Fatal(err)
	}
	for _, observation := range observations {
		if observation.Check == "gate" {
			if !observation.Executed || !observation.Reused || observation.ReuseProofSHA256 == "" || observation.CompletedAtUnixMS <= observation.StartedAtUnixMS {
				t.Fatalf("all-reuse owner observation: %#v", observation)
			}
			continue
		}
		if observation.Executed || !observation.Reused || observation.ReuseProofSHA256 == "" || observation.StartedAtUnixMS != reused.StartedAt.UnixMilli() {
			t.Fatalf("all-reuse observation: %#v", observation)
		}
	}
	for _, invalid := range []remoteci.RunResult{remoteRunReceiptTestTamperedReuse(reused), remoteRunReceiptTestDuplicateReuse(reused), remoteRunReceiptTestMissingReuse(reused)} {
		remoteRunReceiptTestObservationRejected(t, plan, catalog, invalid)
	}
}

func remoteRunReceiptTestTamperedReuse(r remoteci.RunResult) remoteci.RunResult {
	r.ReusedWorkloads = append([]gatecontract.WorkloadPassEvidence(nil), r.ReusedWorkloads...)
	r.ReusedWorkloads[0].EvidenceSHA256 = "sha256:" + strings.Repeat("f", 64)
	return r
}
func remoteRunReceiptTestDuplicateReuse(r remoteci.RunResult) remoteci.RunResult {
	r.ReusedWorkloads = append(r.ReusedWorkloads, r.ReusedWorkloads[0])
	return r
}
func remoteRunReceiptTestMissingReuse(r remoteci.RunResult) remoteci.RunResult {
	r.ReusedWorkloads = r.ReusedWorkloads[1:]
	return r
}

func remoteRunReceiptTestMixed(t *testing.T, plan gatecontract.GatePlan, catalog gatecontract.WorkloadCatalog, complete remoteci.RunResult) {
	t.Helper()
	mixed := remoteRunReceiptTestReuseResult(t, complete, catalog, 1)
	observations, err := remoteRunCheckObservations(plan, catalog, "snapshot-7", mixed)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, observation := range observations {
		found = found || observation.Executed && observation.Reused
	}
	if !found {
		t.Fatal("mixed result lacks combined check")
	}
	mixed.ReusedWorkloads = append(mixed.ReusedWorkloads, remoteRunReceiptTestEvidence(t, catalog.Workloads[0], mixed.StartedAt))
	remoteRunReceiptTestObservationRejected(t, plan, catalog, mixed)
}

func remoteRunReceiptTestReuseResult(t *testing.T, base remoteci.RunResult, catalog gatecontract.WorkloadCatalog, freshCount int) remoteci.RunResult {
	t.Helper()
	result := base
	result.StartedAt = time.Date(2026, time.August, 3, 11, 0, 0, 0, time.UTC)
	result.CompletedAt = result.StartedAt.Add(time.Second)
	result.FreshWorkloadExecutions = append([]gatecontract.PlanGateExecution(nil), base.FreshWorkloadExecutions[:freshCount]...)
	result.ReusedWorkloads = nil
	for index, workload := range catalog.Workloads[freshCount:] {
		if !workload.Shardable {
			continue
		}
		result.ReusedWorkloads = append(result.ReusedWorkloads, remoteRunReceiptTestEvidence(t, workload, result.StartedAt.Add(-time.Duration(index+2)*time.Second)))
	}
	return result
}

func remoteRunReceiptTestEvidence(t *testing.T, workload gatecontract.Workload, start time.Time) gatecontract.WorkloadPassEvidence {
	t.Helper()
	identity := gatecontract.WorkloadPassIdentity{WorkloadID: gatecontract.GateID(workload.ID), ExecutionDigest: "sha256:" + strings.Repeat("1", 64), InputDigest: "sha256:" + strings.Repeat("2", 64), EnvironmentDigest: "sha256:" + strings.Repeat("3", 64)}
	var err error
	identity.IdentityDigest, err = gatecontract.WorkloadPassIdentitySHA256(identity)
	if err != nil {
		t.Fatal(err)
	}
	execution := gatecontract.PlanGateExecution{GateID: identity.WorkloadID, ShardIdentity: "sha256:" + strings.Repeat("4", 64), Status: gatecontract.ResultStatusPassed, ExitCode: 0, StartedAt: start, CompletedAt: start.Add(time.Millisecond), ExecutionProfile: gatecontract.ExecutionProfile{CacheSource: "go_build_cache", CacheStatus: "miss", CacheMeasurement: "measured", StartupMS: 1, TestBodyMS: 1, TotalMS: 2}}
	evidence := gatecontract.WorkloadPassEvidence{Identity: identity, OriginJobID: "origin-job", OriginAcceptedGeneration: 6, OriginSourceTreeSHA: strings.Repeat("c", 40), OriginReceiptSetSHA256: "sha256:" + strings.Repeat("5", 64), OriginExecution: execution}
	evidence.EvidenceSHA256, err = gatecontract.WorkloadPassEvidenceSHA256(evidence)
	if err != nil {
		t.Fatal(err)
	}
	return evidence
}

func remoteRunReceiptTestInput(plan gatecontract.GatePlan, store *gatecontract.DurationLedgerStore) remoteci.RunInput {
	return remoteci.RunInput{AgentTokenDigest: remoteRunReceiptTestAgentTokenDigest, AcceptedGeneration: 7, Profile: plan.Profile, Source: plan.Source, ImageCacheSnapshotID: "snapshot-7", LedgerStore: store}
}

func remoteRunReceiptTestRejectsDifferentAgentToken(t *testing.T, input remoteci.RunInput, complete remoteci.RunResult) {
	t.Helper()
	otherAgent := complete
	otherAgent.AgentTokenDigest = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	if _, _, err := validateRemoteRunContract(input, 7, otherAgent); err == nil {
		t.Fatal("validateRemoteRunContract() accepted a result from a different agent")
	}
}

func remoteRunReceiptTestObservationRejected(t *testing.T, plan gatecontract.GatePlan, catalog gatecontract.WorkloadCatalog, result remoteci.RunResult) {
	t.Helper()
	if _, err := remoteRunCheckObservations(plan, catalog, "snapshot-7", result); err == nil {
		t.Fatal("remoteRunCheckObservations() error = nil")
	}
}

func remoteRunReceiptTestAuthority(t *testing.T, catalog gatecontract.WorkloadCatalog, complete remoteci.RunResult) *gatecontract.DurationLedgerStore {
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
	catalogDigest, err := gatecontract.WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatalf("WorkloadCatalogDigest() error = %v", err)
	}
	if catalogDigest != complete.CatalogDigest {
		t.Fatalf("complete catalog digest = %q, want %q", complete.CatalogDigest, catalogDigest)
	}
	if err := store.RecordWorkloadCatalog(catalog, gatecontract.WorkloadCatalogObservation{
		SourceTreeSHA: complete.SourceTreeSHA, Entrypoint: gatecontract.CIEntrypointRelease,
		Profile: gatecontract.ProfileRelease, AcceptedGeneration: 7, ObservedAt: startedAt,
	}); err != nil {
		t.Fatalf("RecordWorkloadCatalog() error = %v", err)
	}
	workloadByID := make(map[gatecontract.GateID]gatecontract.Workload, len(catalog.Workloads))
	for _, workload := range catalog.Workloads {
		workloadByID[gatecontract.GateID(workload.ID)] = workload
	}
	shardID := "sha256:" + strings.Repeat("c", 64)
	workloadExecutions := make([]gatecontract.PlanGateExecution, 0, len(complete.FreshWorkloadExecutions))
	workloadResults := make([]gatecontract.RemoteCIWorkloadResult, 0, len(complete.FreshWorkloadExecutions))
	workloads := make([]gatecontract.GateID, 0, len(complete.FreshWorkloadExecutions))
	for _, execution := range complete.FreshWorkloadExecutions {
		workload, found := workloadByID[execution.GateID]
		if !found || !workload.Shardable {
			continue
		}
		execution.ShardIdentity = shardID
		execution.ExitCode = 0
		execution.StartedAt = complete.FreshWorkloadExecutions[0].StartedAt
		execution.CompletedAt = execution.StartedAt.Add(11 * time.Millisecond)
		execution.ExecutionProfile = gatecontract.ExecutionProfile{CacheSource: "go_build_cache", CacheStatus: "miss", CacheMeasurement: "measured", StartupMS: 1, TestBodyMS: 10, TotalMS: 11}
		workloadExecutions = append(workloadExecutions, execution)
		workloads = append(workloads, execution.GateID)
		identity := gatecontract.WorkloadPassIdentity{WorkloadID: execution.GateID, ExecutionDigest: gatecontract.WorkloadPassExecutionDigest(workload), InputDigest: workload.InputDigest, EnvironmentDigest: "sha256:" + strings.Repeat("3", 64)}
		identity.IdentityDigest, err = gatecontract.WorkloadPassIdentitySHA256(identity)
		if err != nil {
			t.Fatalf("WorkloadPassIdentitySHA256() error = %v", err)
		}
		workloadResults = append(workloadResults, gatecontract.RemoteCIWorkloadResult{Identity: identity, Disposition: gatecontract.WorkloadDispositionExecuted, OriginJobID: complete.JobID, OriginAcceptedGeneration: complete.AcceptedGeneration})
	}
	if len(workloadExecutions) == 0 {
		t.Fatal("complete result has no shardable workload execution")
	}
	shard := gatecontract.RemoteCIShardRecord{
		ShardIdentity: shardID, ContainerGroup: "eci-authority", ContainerStatus: "Succeeded", Workloads: workloads, Resources: gatecontract.RemoteCIShardResources{ClassID: "medium", CPU: 4, MemoryGiB: 8},
		MaterializationTiming: gatecontract.ShardMaterializationTiming{Measurement: gatecontract.MaterializationMeasurementMeasured, ShardIdentity: shardID,
			Source:           gatecontract.MaterializationPhaseTiming{StartedAtUnixMS: startedAt.Add(time.Millisecond).UnixMilli(), CompletedAtUnixMS: startedAt.Add(2 * time.Millisecond).UnixMilli(), MaterializeMS: 1},
			CandidateCompile: gatecontract.MaterializationPhaseTiming{StartedAtUnixMS: startedAt.Add(2 * time.Millisecond).UnixMilli(), CompletedAtUnixMS: startedAt.Add(3 * time.Millisecond).UnixMilli(), MaterializeMS: 1},
		},
	}
	record := gatecontract.RemoteCIRunRecord{
		JobID: complete.JobID, AgentTokenDigest: complete.AgentTokenDigest, Entrypoint: complete.Entrypoint, Profile: gatecontract.ProfileRelease, AcceptedGeneration: 7, ImageCacheSnapshotID: complete.ImageCacheSnapshotID,
		PlanDigest: complete.PlanDigest, CatalogDigest: catalogDigest, SourceTreeSHA: complete.SourceTreeSHA,
		CandidateGateSourceSHA256: "sha256:" + strings.Repeat("e", 64), CandidateGateToolchainSHA256: "sha256:" + strings.Repeat("f", 64),
		RunnerImage: "ubuntu:22.04", Status: gatecontract.ResultStatusPassed, Authoritative: false, CleanupComplete: true,
		StartedAt: complete.StartedAt, CompletedAt: complete.CompletedAt, Shards: []gatecontract.RemoteCIShardRecord{shard}, Executions: append([]gatecontract.PlanGateExecution(nil), complete.GateExecutions...), WorkloadExecutions: workloadExecutions, WorkloadResults: workloadResults,
	}
	record.TimingObservations = remoteRunReceiptTestTimingObservations(record.JobID, shard, workloadExecutions[0], workloadExecutions[0].StartedAt.Add(-3*time.Millisecond))
	for _, execution := range workloadExecutions[1:] {
		for _, observation := range remoteRunReceiptTestTimingObservations(record.JobID, shard, execution, execution.StartedAt.Add(-3*time.Millisecond)) {
			if observation.Scope == cicontract.TimingScopeWorkload {
				record.TimingObservations = append(record.TimingObservations, observation)
			}
		}
	}
	if err := store.RecordProvisionalRemoteCIRun(record); err != nil {
		t.Fatalf("RecordProvisionalRemoteCIRun() error = %v", err)
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
	catalog, err := gatecontract.BuildExpandedWorkloadCatalog(plan, gatecontract.DefaultWorkloadBootstrapPolicy(), gatecontract.WorkloadInventory{
		GoPackages: []string{"./internal/devtools/gate"},
	})
	if err != nil {
		t.Fatalf("BuildExpandedWorkloadCatalog() error = %v", err)
	}
	for index := range catalog.Workloads {
		if !catalog.Workloads[index].Shardable {
			continue
		}
		digest := sha256.Sum256([]byte("remote-run-receipt-test-input:" + catalog.Workloads[index].ID))
		catalog.Workloads[index].InputDigest = fmt.Sprintf("sha256:%x", digest)
	}
	if err := gatecontract.ValidateWorkloadCatalog(catalog); err != nil {
		t.Fatalf("ValidateWorkloadCatalog() error = %v", err)
	}
	return plan, catalog
}

func remoteRunReceiptTestResult(t *testing.T, plan gatecontract.GatePlan, catalog gatecontract.WorkloadCatalog) remoteci.RunResult {
	t.Helper()
	catalogDigest, err := gatecontract.WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatalf("WorkloadCatalogDigest() error = %v", err)
	}
	workloadExecutions := make([]gatecontract.PlanGateExecution, 0, len(catalog.Workloads))
	identities := make([]gatecontract.WorkloadPassIdentity, 0, len(catalog.Workloads))
	startedAt := time.Date(2026, time.August, 3, 10, 0, 0, 0, time.UTC)
	for _, workload := range catalog.Workloads {
		if !workload.Shardable {
			continue
		}
		workloadExecutions = append(workloadExecutions, gatecontract.PlanGateExecution{
			GateID: gatecontract.GateID(workload.ID), Status: gatecontract.ResultStatusPassed,
			StartedAt: startedAt, CompletedAt: startedAt.Add(time.Second),
		})
		identity := gatecontract.WorkloadPassIdentity{WorkloadID: gatecontract.GateID(workload.ID), ExecutionDigest: gatecontract.WorkloadPassExecutionDigest(workload), InputDigest: workload.InputDigest, EnvironmentDigest: "sha256:" + strings.Repeat("3", 64)}
		identity.IdentityDigest, err = gatecontract.WorkloadPassIdentitySHA256(identity)
		if err != nil {
			t.Fatalf("WorkloadPassIdentitySHA256() error = %v", err)
		}
		identities = append(identities, identity)
		startedAt = startedAt.Add(time.Second)
	}
	ownerExecutions := remoteRunReceiptTestOwnerExecutions(t, plan)
	return remoteci.RunResult{
		AgentTokenDigest: remoteRunReceiptTestAgentTokenDigest, AcceptedGeneration: 7,
		JobID: "remote-job", Entrypoint: gatecontract.CIEntrypointRelease,
		Profile: plan.Profile, PlanDigest: plan.PlanDigest, CatalogDigest: catalogDigest,
		SourceTreeSHA: plan.Source.SourceTreeSHA, Status: gatecontract.ResultStatusPassed,
		ImageCacheSnapshotID: "snapshot-7", CandidateGateSourceSHA256: "sha256:" + strings.Repeat("e", 64),
		CandidateGateToolchainSHA256: "sha256:" + strings.Repeat("f", 64), RunnerImage: "ubuntu:22.04",
		Authoritative: false, CleanupComplete: true, StartedAt: time.Date(2026, time.August, 3, 10, 0, 0, 0, time.UTC), CompletedAt: time.Date(2026, time.August, 3, 10, 0, 0, int(20*time.Millisecond), time.UTC), GateExecutions: ownerExecutions, WorkloadExecutions: workloadExecutions, FreshWorkloadExecutions: workloadExecutions, WorkloadPassIdentities: identities,
	}
}

func remoteRunReceiptTestOwnerExecutions(t *testing.T, plan gatecontract.GatePlan) []gatecontract.PlanGateExecution {
	t.Helper()
	if plan.Profile != gatecontract.ProfileRelease {
		return nil
	}
	log := []byte("release prerequisite\n")
	logDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(log))
	startedAt := time.Date(2026, time.August, 3, 10, 0, 0, 0, time.UTC)
	observed := make(map[gatecontract.GateID]gatecontract.PlanGateExecution)
	for index, spec := range plan.Gates {
		if spec.ID == gatecontract.GateIDReleaseLayeredCheck {
			continue
		}
		started := startedAt.Add(time.Duration(index) * time.Second)
		observed[spec.ID] = gatecontract.PlanGateExecution{GateID: spec.ID, Status: gatecontract.ResultStatusPassed, ExitCode: 0, StartedAt: started, CompletedAt: started.Add(time.Second), Log: log, LogDigest: logDigest, ExecutionProfile: gatecontract.ExecutionProfile{CacheSource: "none", CacheStatus: gatecontract.CacheObservationNotApplicable, CacheMeasurement: "measured", StartupMS: 1, TestBodyMS: 1, TotalMS: 1000}}
	}
	attestation, err := gatecontract.ExecuteReleaseLayerAttestation(gatecontract.ProfileRelease, plan.PlanDigest, observed, func() time.Time {
		return startedAt.Add(30 * time.Second)
	})
	if err != nil {
		t.Fatalf("ExecuteReleaseLayerAttestation() error = %v", err)
	}
	executions := make([]gatecontract.PlanGateExecution, 0, len(observed)+1)
	for _, execution := range observed {
		executions = append(executions, execution)
	}
	return append(executions, attestation)
}

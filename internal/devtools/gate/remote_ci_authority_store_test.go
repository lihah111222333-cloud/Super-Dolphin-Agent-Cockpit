package gate

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"golang.org/x/sync/errgroup"
)

func TestLoadCheckReceiptsUsesPersistedExecutionScope(t *testing.T) {
	t.Run("authoritative subset reloads only selected checks", testLoadCheckReceiptsReloadsSubset)
	t.Run("full reload remains full", testLoadCheckReceiptsReloadsFull)
	t.Run("legacy reload remains full", testLoadCheckReceiptsReloadsLegacy)
	t.Run("missing scope fails fast", testLoadCheckReceiptsRejectsMissingScope)
	t.Run("missing scope with same check class fails fast", testLoadCheckReceiptsRejectsMissingScopeWithSameCheckClass)
	t.Run("canonical same-check subset replacement fails fast", testLoadCheckReceiptsRejectsCanonicalSameCheckScopeReplacement)
	t.Run("tampered scope fails fast", testLoadCheckReceiptsRejectsTamperedScope)
	t.Run("scope generation mismatch fails fast", testLoadCheckReceiptsRejectsScopeGenerationMismatch)
}

func testLoadCheckReceiptsReloadsSubset(t *testing.T) {
	t.Helper()
	assertScopedCheckReceiptReload(t, "subset-reload", RemoteCIExecutionScopeSubset)
}

func testLoadCheckReceiptsReloadsFull(t *testing.T) {
	t.Helper()
	assertScopedCheckReceiptReload(t, "full-reload", RemoteCIExecutionScopeFull)
}

func testLoadCheckReceiptsReloadsLegacy(t *testing.T) {
	t.Helper()
	assertScopedCheckReceiptReload(t, "legacy-reload", "")
}

func assertScopedCheckReceiptReload(t *testing.T, jobID string, scopeKind RemoteCIExecutionScopeKind) {
	t.Helper()
	store := newWorkloadPassEvidenceStore(t, 6)
	record, receipts := recordScopedCheckReceiptAuthority(t, store, jobID, scopeKind)
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, false); err != nil {
		t.Fatalf("finalize authority: %v", err)
	}
	loaded, err := store.LoadCheckReceipts(record.JobID)
	if err != nil {
		t.Fatalf("reload receipts: %v", err)
	}
	if !reflect.DeepEqual(loaded, receipts) {
		t.Fatalf("receipts = %#v, want %#v", loaded, receipts)
	}
}

func testLoadCheckReceiptsRejectsMissingScope(t *testing.T) {
	t.Helper()
	assertLoadCheckReceiptsRejectsScopeMutation(t, "missing-scope", `DELETE FROM ci_remote_run_execution_scopes WHERE job_id = ?`)
}

func testLoadCheckReceiptsRejectsMissingScopeWithSameCheckClass(t *testing.T) {
	t.Helper()
	store := newWorkloadPassEvidenceStore(t, 6)
	catalog := WorkloadCatalog{Version: durationLedgerVersion, Authoritative: true, Workloads: []Workload{
		{ID: string(GateIDWhitespaceCheck), Kind: WorkloadKindGuard, CommandDigest: strings.Repeat("a", 64), InputDigest: digestForWorkloadPass("same-check-class-whitespace"), BootstrapEstimateMS: 1, Shardable: true},
		{ID: string(GateIDAIMaintenanceSelfTest), Kind: WorkloadKindGuard, CommandDigest: strings.Repeat("b", 64), InputDigest: digestForWorkloadPass("same-check-class-maintenance"), BootstrapEstimateMS: 1, Shardable: true},
	}}
	scope, err := NewRemoteCISubsetExecutionScope(catalog, []GateID{GateIDWhitespaceCheck})
	if err != nil {
		t.Fatal(err)
	}
	record, receipts := recordScopedCheckReceiptAuthorityForCatalog(t, store, "missing-scope-same-check-class", catalog, &scope, catalog.Workloads[:1])
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, false); err != nil {
		t.Fatalf("finalize subset authority: %v", err)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`DELETE FROM ci_remote_run_execution_scopes WHERE job_id = ?`, record.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadCheckReceipts(record.JobID); err == nil {
		t.Fatal("LoadCheckReceipts accepted missing subset scope with unchanged check classes")
	}
}

func testLoadCheckReceiptsRejectsCanonicalSameCheckScopeReplacement(t *testing.T) {
	t.Helper()
	store := newWorkloadPassEvidenceStore(t, 6)
	catalog := WorkloadCatalog{Version: durationLedgerVersion, Authoritative: true, Workloads: []Workload{
		{ID: string(GateIDWhitespaceCheck), Kind: WorkloadKindGuard, CommandDigest: strings.Repeat("a", 64), InputDigest: digestForWorkloadPass("same-check-scope-whitespace"), BootstrapEstimateMS: 1, Shardable: true},
		{ID: string(GateIDAIMaintenanceSelfTest), Kind: WorkloadKindGuard, CommandDigest: strings.Repeat("b", 64), InputDigest: digestForWorkloadPass("same-check-scope-maintenance"), BootstrapEstimateMS: 1, Shardable: true},
	}}
	original, err := NewRemoteCISubsetExecutionScope(catalog, []GateID{GateIDWhitespaceCheck})
	if err != nil {
		t.Fatal(err)
	}
	record, receipts := recordScopedCheckReceiptAuthorityForCatalog(t, store, "canonical-same-check-scope", catalog, &original, catalog.Workloads[:1])
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, false); err != nil {
		t.Fatalf("finalize subset authority: %v", err)
	}
	replacement, err := NewRemoteCISubsetExecutionScope(catalog, []GateID{GateIDAIMaintenanceSelfTest})
	if err != nil {
		t.Fatal(err)
	}
	scopeJSON, err := replacement.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	scopeDigest, err := replacement.Digest()
	if err != nil {
		t.Fatal(err)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`UPDATE ci_remote_run_execution_scopes SET scope_json = ?, scope_digest = ?, scope_count = ?, accepted_generation = ? WHERE job_id = ?`, scopeJSON, scopeDigest, len(replacement.selectedGateIDs), "6", record.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadCheckReceipts(record.JobID); err == nil {
		t.Fatal("LoadCheckReceipts accepted canonical same-check subset scope replacement")
	}
}

func testLoadCheckReceiptsRejectsTamperedScope(t *testing.T) {
	t.Helper()
	assertLoadCheckReceiptsRejectsScopeMutation(t, "tampered-scope", `UPDATE ci_remote_run_execution_scopes SET scope_digest = 'sha256:tampered' WHERE job_id = ?`)
}

func testLoadCheckReceiptsRejectsScopeGenerationMismatch(t *testing.T) {
	t.Helper()
	assertLoadCheckReceiptsRejectsScopeMutation(t, "scope-generation-mismatch", `UPDATE ci_remote_run_execution_scopes SET accepted_generation = '7' WHERE job_id = ?`)
}

func assertLoadCheckReceiptsRejectsScopeMutation(t *testing.T, jobID, mutation string) {
	t.Helper()
	store := newWorkloadPassEvidenceStore(t, 6)
	record, receipts := recordScopedCheckReceiptAuthority(t, store, jobID, RemoteCIExecutionScopeSubset)
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, false); err != nil {
		t.Fatalf("finalize subset authority: %v", err)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(mutation, record.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadCheckReceipts(record.JobID); err == nil {
		t.Fatal("LoadCheckReceipts accepted corrupted persisted execution scope")
	}
}

func recordScopedCheckReceiptAuthority(t *testing.T, store *DurationLedgerStore, jobID string, scopeKind RemoteCIExecutionScopeKind) (RemoteCIRunRecord, []CheckReceiptRecord) {
	t.Helper()
	catalog, scope, selected := scopedCheckReceiptCatalog(t, jobID, scopeKind)
	return recordScopedCheckReceiptAuthorityForCatalog(t, store, jobID, catalog, scope, selected)
}

func recordScopedCheckReceiptAuthorityForCatalog(t *testing.T, store *DurationLedgerStore, jobID string, catalog WorkloadCatalog, scope *RemoteCIExecutionScope, selected []Workload) (RemoteCIRunRecord, []CheckReceiptRecord) {
	t.Helper()
	now := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	catalogDigest, err := WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	treeSHA := strings.Repeat("6", 40)
	if err := store.RecordWorkloadCatalog(catalog, WorkloadCatalogObservation{SourceTreeSHA: treeSHA, Entrypoint: CIEntrypointGitPreCommit, Profile: ProfileLocalFast, AcceptedGeneration: 6, ObservedAt: now}); err != nil {
		t.Fatalf("record scoped catalog: %v", err)
	}
	record := newScopedCheckReceiptRecord(jobID, now, treeSHA, catalogDigest, scope)
	appendScopedCheckReceiptExecutions(t, &record, selected)
	normalizeScopeReloadShardTiming(&record)
	receipts := scopedCheckReceiptRecords(t, record, catalog, scope)
	if err := store.RecordProvisionalRemoteCIRun(record); err != nil {
		t.Fatalf("record scoped provisional run: %v", err)
	}
	return record, receipts
}

func scopedCheckReceiptCatalog(t *testing.T, jobID string, scopeKind RemoteCIExecutionScopeKind) (WorkloadCatalog, *RemoteCIExecutionScope, []Workload) {
	t.Helper()
	catalog := WorkloadCatalog{Version: durationLedgerVersion, Authoritative: true, Workloads: []Workload{
		{ID: string(GateIDWhitespaceCheck), Kind: WorkloadKindGuard, CommandDigest: strings.Repeat("a", 64), InputDigest: digestForWorkloadPass(jobID + "-gate"), BootstrapEstimateMS: 1, Shardable: true},
		{ID: string(GateIDBackendTestWithGuard), Kind: WorkloadKindGuard, CommandDigest: strings.Repeat("b", 64), InputDigest: digestForWorkloadPass(jobID + "-normal"), BootstrapEstimateMS: 1, Shardable: true},
		{ID: string(GateIDBackendTestGuardWithRace), Kind: WorkloadKindGuard, CommandDigest: strings.Repeat("c", 64), InputDigest: digestForWorkloadPass(jobID + "-race"), BootstrapEstimateMS: 1, Shardable: true},
	}}
	if scopeKind != RemoteCIExecutionScopeSubset {
		catalog.Workloads = catalog.Workloads[:1]
	}
	var scope *RemoteCIExecutionScope
	selected := catalog.Workloads
	switch scopeKind {
	case RemoteCIExecutionScopeSubset:
		value, err := NewRemoteCISubsetExecutionScope(catalog, []GateID{GateIDWhitespaceCheck, GateIDBackendTestWithGuard})
		if err != nil {
			t.Fatal(err)
		}
		scope, selected = &value, catalog.Workloads[:2]
	case RemoteCIExecutionScopeFull:
		value, err := NewRemoteCIFullExecutionScope(catalog)
		if err != nil {
			t.Fatal(err)
		}
		scope = &value
	}
	return catalog, scope, selected
}

func newScopedCheckReceiptRecord(jobID string, now time.Time, treeSHA, catalogDigest string, scope *RemoteCIExecutionScope) RemoteCIRunRecord {
	shardIdentity := digestForWorkloadPass(jobID + "-shard")
	shard := RemoteCIShardRecord{ShardIdentity: shardIdentity, ContainerGroup: "eci-" + jobID, ContainerStatus: "Succeeded", MaterializationTiming: measuredShardMaterializationTiming(shardIdentity), Resources: RemoteCIShardResources{ClassID: "fixed", CPU: 4, MemoryGiB: 8}}
	return RemoteCIRunRecord{JobID: jobID, AgentTokenDigest: digestForWorkloadPass(jobID + "-agent"), Entrypoint: CIEntrypointGitPreCommit, Profile: ProfileLocalFast, AcceptedGeneration: 6, Scope: scope, ImageCacheSnapshotID: "snapshot-6", PlanDigest: "sha256:plan", CatalogDigest: catalogDigest, SourceTreeSHA: treeSHA, CandidateGateSourceSHA256: digestForWorkloadPass(jobID + "-gate-source"), CandidateGateToolchainSHA256: digestForWorkloadPass(jobID + "-toolchain"), RunnerImage: "ubuntu:22.04", Status: ResultStatusPassed, StartedAt: now, CompletedAt: now.Add(time.Second), CleanupComplete: true, Shards: []RemoteCIShardRecord{shard}}
}

func appendScopedCheckReceiptExecutions(t *testing.T, record *RemoteCIRunRecord, selected []Workload) {
	t.Helper()
	shard := record.Shards[0]
	for index, workload := range selected {
		workloadID := GateID(workload.ID)
		goFlags, err := WorkloadExecutionGoFlags(workload.ID)
		if err != nil {
			t.Fatal(err)
		}
		startedAt := record.StartedAt.Add(time.Duration(index+3) * time.Millisecond)
		execution := PlanGateExecution{ShardIdentity: shard.ShardIdentity, GateID: workloadID, Status: ResultStatusPassed, StartedAt: startedAt, CompletedAt: startedAt.Add(7 * time.Millisecond), ExecutionProfile: ExecutionProfile{GoFlags: goFlags, CacheSource: "go_build_cache", CacheStatus: CacheObservationMiss, CacheMeasurement: "measured", StartupMS: 1, TestBodyMS: 6, TotalMS: 7}}
		identity := WorkloadPassIdentity{WorkloadID: workloadID, ExecutionDigest: WorkloadPassExecutionDigest(workload), InputDigest: workload.InputDigest, EnvironmentDigest: digestForWorkloadPass(record.JobID + "-environment-" + workload.ID)}
		identity.IdentityDigest = workloadPassIdentityDigest(t, identity)
		shard.Workloads = append(shard.Workloads, workloadID)
		record.WorkloadExecutions = append(record.WorkloadExecutions, execution)
		record.WorkloadResults = append(record.WorkloadResults, RemoteCIWorkloadResult{Identity: identity, Disposition: WorkloadDispositionExecuted, OriginJobID: record.JobID, OriginAcceptedGeneration: 6})
		observations := authoritativeTimingObservationsForTest(record.JobID, execution)
		if index != 0 {
			observations = scopeReloadWorkloadTimingObservations(observations)
		}
		record.TimingObservations = append(record.TimingObservations, observations...)
	}
	record.Shards[0] = shard
}

func scopeReloadWorkloadTimingObservations(observations []TimingObservation) []TimingObservation {
	workloadOnly := make([]TimingObservation, 0, len(observations))
	for _, observation := range observations {
		if observation.Scope == cicontract.TimingScopeWorkload {
			workloadOnly = append(workloadOnly, observation)
		}
	}
	return workloadOnly
}

func normalizeScopeReloadShardTiming(record *RemoteCIRunRecord) {
	if len(record.WorkloadExecutions) < 2 {
		return
	}
	first, last := record.WorkloadExecutions[0], record.WorkloadExecutions[len(record.WorkloadExecutions)-1]
	for index := range record.TimingObservations {
		observation := &record.TimingObservations[index]
		switch observation.Scope {
		case cicontract.TimingScopeRun:
			setScopeReloadTimingInterval(observation, first.StartedAt.Add(-3*time.Millisecond), last.CompletedAt.Add(2*time.Millisecond))
		case cicontract.TimingScopeShard:
			normalizeScopeReloadShardPhase(observation, first, last)
		}
	}
}

func normalizeScopeReloadShardPhase(observation *TimingObservation, first, last PlanGateExecution) {
	switch observation.Phase {
	case cicontract.TimingStartup:
		setScopeReloadTimingInterval(observation, first.StartedAt, last.StartedAt.Add(time.Duration(last.ExecutionProfile.StartupMS)*time.Millisecond))
	case cicontract.TimingTestBody:
		setScopeReloadTimingInterval(observation, first.CompletedAt.Add(-time.Duration(first.ExecutionProfile.TestBodyMS)*time.Millisecond), last.CompletedAt)
	case cicontract.TimingTotal:
		setScopeReloadTimingInterval(observation, first.StartedAt.Add(-3*time.Millisecond), last.CompletedAt.Add(time.Millisecond))
	}
}

func setScopeReloadTimingInterval(observation *TimingObservation, startedAt, completedAt time.Time) {
	observation.StartedAt, observation.CompletedAt = startedAt, completedAt
	observation.DurationMS = completedAt.Sub(startedAt).Milliseconds()
}

func scopedCheckReceiptRecords(t *testing.T, record RemoteCIRunRecord, catalog WorkloadCatalog, scope *RemoteCIExecutionScope) []CheckReceiptRecord {
	t.Helper()
	executionCatalog, err := ProjectRemoteCIExecutionCatalog(catalog, scope)
	if err != nil {
		t.Fatal(err)
	}
	checks, err := RequiredChecksForWorkloadCatalog(executionCatalog)
	if err != nil {
		t.Fatal(err)
	}
	receipts := make([]CheckReceiptRecord, 0, len(checks))
	for index, check := range checks {
		startedAt := record.StartedAt.Add(time.Duration(index) * time.Minute)
		receipt := CheckReceiptRecord{RunID: record.JobID, JobID: record.JobID, CandidateTreeSHA: record.SourceTreeSHA, AgentTokenDigest: record.AgentTokenDigest, AcceptedGeneration: 6, AcceptedSnapshotID: record.ImageCacheSnapshotID, RequiredCheck: check, Executed: true, Passed: true, StartedAt: startedAt, CompletedAt: startedAt.Add(time.Second), Duration: time.Second}
		receipt.ReceiptSHA256, err = CheckReceiptSHA256(receipt)
		if err != nil {
			t.Fatal(err)
		}
		receipts = append(receipts, receipt)
	}
	return receipts
}

func TestRemoteCIAuthorityRecordsRoundTripAndFailFast(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 6)
	const (
		jobID      = "refresh-job"
		generation = "7"
		treeSHA    = "6666666666666666666666666666666666666666"
	)
	record := prepareRemoteCIAuthorityRecordTest(t, store, jobID, generation, treeSHA)
	receipts := completeWorkloadPassReceipts(t, record)
	testCheckReceiptRoundTripAndFailFast(t, store, record, receipts)
	testCheckReceiptValidation(t, receipts)
}

// TestSharedSQLiteConcurrentJobReceiptsKeepAgentTokensIsolated 验证普通 CI 回执写入共享
// WAL 权威而无进程级准入锁；刷新单例刻意不参与该路径。
func TestSharedSQLiteConcurrentJobReceiptsKeepAgentTokensIsolated(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 6)
	now := time.Date(2026, time.August, 3, 11, 0, 0, 0, time.UTC)
	jobs := prepareConcurrentReceiptJobs(t, store)
	if err := appendConcurrentJobReceipts(store, jobs, now); err != nil {
		t.Fatalf("concurrent receipt write: %v", err)
	}
	assertConcurrentJobReceiptIsolation(t, store, jobs)
}

type concurrentReceiptJob struct {
	jobID, treeSHA, tokenDigest string
	record                      RemoteCIRunRecord
}

func prepareConcurrentReceiptJobs(t *testing.T, store *DurationLedgerStore) []concurrentReceiptJob {
	t.Helper()
	jobs := make([]concurrentReceiptJob, 4)
	for index := range jobs {
		jobID := fmt.Sprintf("concurrent-job-%d", index)
		record, _, _ := recordWorkloadPassRun(t, store, jobID, 6, "concurrent-authority-"+jobID)
		jobs[index] = concurrentReceiptJob{jobID: jobID, treeSHA: record.SourceTreeSHA, tokenDigest: record.AgentTokenDigest, record: record}
	}
	return jobs
}

func appendConcurrentJobReceipts(store *DurationLedgerStore, jobs []concurrentReceiptJob, now time.Time) error {
	var group errgroup.Group
	for _, job := range jobs {
		group.Go(func() error {
			receipts, err := concurrentJobReceipts(job, now)
			if err != nil {
				return err
			}
			return store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(job.record), receipts, nil, false)
		})
	}
	return group.Wait()
}

func concurrentJobReceipts(job concurrentReceiptJob, now time.Time) ([]CheckReceiptRecord, error) {
	receipts := completeWorkloadPassReceiptsForTime(job.record, now)
	for index := range receipts {
		receipts[index].AgentTokenDigest = job.tokenDigest
		digest, err := CheckReceiptSHA256(receipts[index])
		if err != nil {
			return nil, err
		}
		receipts[index].ReceiptSHA256 = digest
	}
	return receipts, nil
}

func assertConcurrentJobReceiptIsolation(t *testing.T, store *DurationLedgerStore, jobs []concurrentReceiptJob) {
	t.Helper()
	for _, job := range jobs {
		receipts, err := store.LoadCheckReceipts(job.jobID)
		if err != nil {
			t.Fatalf("load %s receipts: %v", job.jobID, err)
		}
		for _, receipt := range receipts {
			if receipt.JobID != job.jobID || receipt.AgentTokenDigest != job.tokenDigest {
				t.Fatalf("receipt cross-talk: %#v", receipt)
			}
		}
	}
}

func testCheckReceiptRoundTripAndFailFast(t *testing.T, store *DurationLedgerStore, record RemoteCIRunRecord, receipts []CheckReceiptRecord) {
	t.Helper()
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, false); err != nil {
		t.Fatalf("FinalizeRemoteCIRunAuthorityWithSamples() error = %v", err)
	}
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, false); err == nil {
		t.Fatal("duplicate remote CI authority finalization was accepted")
	}
	loaded, err := store.LoadCheckReceipts(record.JobID)
	if err != nil {
		t.Fatalf("LoadCheckReceipts() error = %v", err)
	}
	if !reflect.DeepEqual(loaded, receipts) {
		t.Fatalf("check receipts = %#v, want %#v", loaded, receipts)
	}
}

func testCheckReceiptValidation(t *testing.T, receipts []CheckReceiptRecord) {
	t.Helper()
	testCheckReceiptIdentityValidation(t, receipts)
	testCheckReceiptReuseValidation(t, receipts)
}

func testCheckReceiptIdentityValidation(t *testing.T, receipts []CheckReceiptRecord) {
	t.Helper()
	failed := append([]CheckReceiptRecord(nil), receipts...)
	failed[0].Passed = false
	if err := validatePassingCheckReceiptsFor([]cicontract.RequiredCheck{cicontract.RequiredCheckNormal}, failed); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("failed check validation error = %v", err)
	}
	zeroGeneration := receipts[0]
	zeroGeneration.AcceptedGeneration = 0
	if err := validateCheckReceiptRecord(zeroGeneration); err == nil || !strings.Contains(err.Error(), "generation") {
		t.Fatalf("zero check receipt generation error = %v", err)
	}
	maxGenerationReceipt := receipts[0]
	maxGenerationReceipt.AcceptedGeneration = ^uint64(0)
	receiptSHA256, err := CheckReceiptSHA256(maxGenerationReceipt)
	if err != nil {
		t.Fatal(err)
	}
	maxGenerationReceipt.ReceiptSHA256 = receiptSHA256
	if err := validateCheckReceiptRecord(maxGenerationReceipt); err != nil {
		t.Fatalf("maximum uint64 check receipt generation error = %v", err)
	}
	tampered := receipts[0]
	tampered.CompletedAt = tampered.CompletedAt.Add(time.Second)
	tampered.Duration = tampered.CompletedAt.Sub(tampered.StartedAt)
	if err := validateCheckReceiptRecord(tampered); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("tampered check receipt error = %v", err)
	}
}

func testCheckReceiptReuseValidation(t *testing.T, receipts []CheckReceiptRecord) {
	t.Helper()
	var err error
	reused := receipts[0]
	reused.Executed, reused.Reused = false, true
	reused.ReuseProofSHA256 = "sha256:" + strings.Repeat("b", 64)
	reused.ReceiptSHA256, err = CheckReceiptSHA256(reused)
	if err != nil || validateCheckReceiptRecord(reused) != nil {
		t.Fatalf("reused receipt validation = hash %v validate %v", err, validateCheckReceiptRecord(reused))
	}
	missingProof := reused
	missingProof.ReuseProofSHA256 = ""
	missingProof.ReceiptSHA256, err = CheckReceiptSHA256(missingProof)
	if err != nil || validateCheckReceiptRecord(missingProof) == nil {
		t.Fatal("reused receipt without proof was accepted")
	}
	mixed := reused
	mixed.Executed = true
	mixed.ReceiptSHA256, err = CheckReceiptSHA256(mixed)
	if err != nil || validateCheckReceiptRecord(mixed) != nil {
		t.Fatalf("mixed receipt validation = hash %v validate %v", err, validateCheckReceiptRecord(mixed))
	}
	nonReuseProof := receipts[0]
	nonReuseProof.ReuseProofSHA256 = reused.ReuseProofSHA256
	nonReuseProof.ReceiptSHA256, err = CheckReceiptSHA256(nonReuseProof)
	if err != nil || validateCheckReceiptRecord(nonReuseProof) == nil {
		t.Fatal("non-reused receipt carrying proof was accepted")
	}
}

func TestLoadCheckReceiptsRejectsStoredFailureAndSchemaRejectsMissingTables(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 6)
	const jobID = "receipt-job"
	treeSHA := strings.Repeat("6", 40)
	record := prepareRemoteCIAuthorityRecordTest(t, store, jobID, "9", treeSHA)
	testCheckReceiptRoundTripAndFailFast(t, store, record, completeWorkloadPassReceipts(t, record))
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	testLoadCheckReceiptsRejectsStoredFailure(t, store, database, jobID)
	testRemoteCIAuthoritySchemaRejectsMissingTables(t, database)
}

func testLoadCheckReceiptsRejectsStoredFailure(t *testing.T, store *DurationLedgerStore, database *sql.DB, jobID string) {
	t.Helper()
	if _, err := database.Exec(fmt.Sprintf("UPDATE %s SET passed = 0 WHERE job_id = ? AND required_check = ?", cicontract.CheckReceiptsTable), jobID, cicontract.RequiredCheckNormal); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadCheckReceipts(jobID); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("LoadCheckReceipts() failure error = %v", err)
	}
}

func testRemoteCIAuthoritySchemaRejectsMissingTables(t *testing.T, database *sql.DB) {
	t.Helper()
	if _, err := database.Exec(fmt.Sprintf("DROP TABLE %s", cicontract.CheckReceiptsTable)); err != nil {
		t.Fatal(err)
	}
	if err := ensureDurationLedgerSQLiteSchemaWithValidator(database, time.Now, newDurationLedgerSQLiteSchemaValidator()); err == nil {
		t.Fatal("partial authority schema was accepted by the sole canonical schema writer")
	}
}

func TestDurationLedgerSQLiteRejectsEmptyLegacyWorkloadReuseTables(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TABLE ci_run_workloads (job_id TEXT NOT NULL, workload_id TEXT NOT NULL, disposition TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if err := ensureDurationLedgerSQLiteSchemaWithValidator(database, time.Now, newDurationLedgerSQLiteSchemaValidator()); err == nil {
		t.Fatalf("empty legacy workload reuse schema error = %v, want fail-fast refusal", err)
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='ci_run_workloads'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("rejected legacy reuse table count = %d, want 1", count)
	}
}

func TestDurationLedgerSQLiteRefusesNonEmptyLegacyWorkloadReuseTables(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TABLE ci_workload_pass_proofs (identity_digest TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO ci_workload_pass_proofs(identity_digest) VALUES ('historical-pass')`); err != nil {
		t.Fatal(err)
	}
	if err := ensureDurationLedgerSQLiteSchemaWithValidator(database, time.Now, newDurationLedgerSQLiteSchemaValidator()); err == nil {
		t.Fatalf("non-empty legacy workload reuse schema error = %v", err)
	}
}

func prepareRemoteCIAuthorityRecordTest(t *testing.T, store *DurationLedgerStore, jobID, _ string, treeSHA string) RemoteCIRunRecord {
	t.Helper()
	record, _, _ := recordWorkloadPassRun(t, store, jobID, 6, "authority-"+jobID)
	if record.SourceTreeSHA != treeSHA {
		t.Fatalf("provisional remote CI run tree = %q, want %q", record.SourceTreeSHA, treeSHA)
	}
	return record
}

func testCompleteCheckReceipts(jobID, treeSHA string, startedAt time.Time) []CheckReceiptRecord {
	checks := cicontract.RequiredChecks()
	receipts := make([]CheckReceiptRecord, 0, len(checks))
	for index, check := range checks {
		start := startedAt.Add(time.Duration(index) * time.Minute)
		receipt := CheckReceiptRecord{
			RunID: jobID, JobID: jobID, CandidateTreeSHA: treeSHA, AgentTokenDigest: "sha256:" + strings.Repeat("a", 64), AcceptedGeneration: 6, AcceptedSnapshotID: "snapshot-6",
			RequiredCheck: check, Executed: true, Passed: true, StartedAt: start, CompletedAt: start.Add(time.Second), Duration: time.Second,
		}
		digest, err := CheckReceiptSHA256(receipt)
		if err != nil {
			panic(err)
		}
		receipt.ReceiptSHA256 = digest
		receipts = append(receipts, receipt)
	}
	return receipts
}

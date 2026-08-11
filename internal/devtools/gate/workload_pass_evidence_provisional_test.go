package gate

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestWorkloadPassEvidencePromotesPassingWorkloadFromFailedRun verifies that a
// cleaned failed run remains non-authoritative while its complete passing
// workload execution is independently reusable.
func TestWorkloadPassEvidencePromotesPassingWorkloadFromFailedRun(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, identity, _ := recordWorkloadPassRun(t, store, "failed-partial-pass", 1, "failed-partial-pass-workload")
	record.Status = ResultStatusFailed
	record.Authoritative = false
	if err := store.RecordProvisionalRemoteCIRun(record); err != nil {
		t.Fatalf("record cleaned failed run: %v", err)
	}
	loaded, err := store.LoadRemoteCIRun(record.JobID)
	if err != nil {
		t.Fatalf("load failed run: %v", err)
	}
	if loaded.Authoritative || loaded.Status != ResultStatusFailed || !loaded.CleanupComplete {
		t.Fatalf("failed run authority projection = %#v", loaded)
	}
	evidence, err := store.LookupWorkloadPassEvidence([]WorkloadPassIdentity{identity})
	if err != nil {
		t.Fatalf("lookup failed-run workload evidence: %v", err)
	}
	if len(evidence) != 1 || evidence[0].OriginJobID != record.JobID || evidence[0].OriginExecution.Status != ResultStatusPassed {
		t.Fatalf("failed-run workload evidence = %#v", evidence)
	}
}

// TestCleanedFailureOriginContextReusesLoadedProjection 锁定 environment replay
// 在同一只读事务中只完整解码一次 cleaned-failure origin，再直接摘要该 projection。
func TestCleanedFailureOriginContextReusesLoadedProjection(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, identity, _ := recordWorkloadPassRun(t, store, "failed-origin-projection-reuse", 1, "failed-origin-projection-reuse-workload")
	record.Status = ResultStatusFailed
	record.Authoritative = false
	if err := store.RecordProvisionalRemoteCIRun(record); err != nil {
		t.Fatalf("record cleaned failed run: %v", err)
	}
	evidence := lookupSingleWorkloadPassEvidence(t, store, identity)
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	stats := workloadPassEvidenceLookupStats{}
	if _, err := loadWorkloadPassEvidenceBaseOriginContext(transaction, evidence, 1, &stats); err != nil {
		t.Fatalf("load cleaned failure origin context: %v", err)
	}
	if stats.originRunLoads != 1 || stats.originReceiptSetValidations != 1 {
		t.Fatalf("origin validation counts = run:%d receipt:%d, want 1/1", stats.originRunLoads, stats.originReceiptSetValidations)
	}
	if stats.provisionalProjectionReloads != 0 || stats.loadedProjectionDigests != 1 {
		t.Fatalf("provisional projection counts = reload:%d loaded-digest:%d, want 0/1", stats.provisionalProjectionReloads, stats.loadedProjectionDigests)
	}
}

// TestCleanedFailureLoadedProjectionRejectsSQLiteDrift 验证复用已加载 record
// 仍会对当前只读快照的完整 projection 摘要；任一 SQLite 字段漂移都会 fail-fast。
func TestCleanedFailureLoadedProjectionRejectsSQLiteDrift(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, identity, _ := recordWorkloadPassRun(t, store, "failed-origin-projection-drift", 1, "failed-origin-projection-drift-workload")
	record.Status = ResultStatusFailed
	record.Authoritative = false
	if err := store.RecordProvisionalRemoteCIRun(record); err != nil {
		t.Fatalf("record cleaned failed run: %v", err)
	}
	if evidence, err := store.LookupWorkloadPassEvidence([]WorkloadPassIdentity{identity}); err != nil || len(evidence) != 1 {
		t.Fatalf("initial cleaned failure evidence = %#v, err=%v", evidence, err)
	}
	database := openWorkloadPassDatabase(t, store)
	if _, err := database.Exec(`UPDATE ci_runs SET error_text = error_text || '; drift' WHERE job_id = ?`, record.JobID); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LookupWorkloadPassEvidence([]WorkloadPassIdentity{identity}); err == nil || !strings.Contains(err.Error(), "receipt set is missing or tampered") {
		t.Fatalf("cleaned failure projection drift error = %v, want receipt tamper rejection", err)
	}
}

// TestWorkloadPassEvidencePromotesCleanFailureFromPreviouslyAcceptedGeneration
// 验证旧 accepted generation 的失败收尾仍会提升完整 executed PASS；窗口 compactor
// 负责 current/current-2 retention，不能在写入时因非当前代静默丢失证据。
func TestWorkloadPassEvidencePromotesCleanFailureFromPreviouslyAcceptedGeneration(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, identity, _ := recordWorkloadPassRun(t, store, "failed-old-generation", 1, "failed-old-generation-workload")
	record.Status = ResultStatusFailed
	record.Authoritative = false
	seedAcceptedGenerationForTest(t, store, 2)
	if err := store.RecordProvisionalRemoteCIRun(record); err != nil {
		t.Fatalf("record old-generation cleaned failed run: %v", err)
	}
	evidence, err := store.LookupWorkloadPassEvidence([]WorkloadPassIdentity{identity})
	if err != nil {
		t.Fatalf("lookup old-generation workload evidence: %v", err)
	}
	if len(evidence) != 1 || evidence[0].OriginJobID != record.JobID || evidence[0].OriginAcceptedGeneration != 1 {
		t.Fatalf("old-generation workload evidence = %#v", evidence)
	}
}

// TestWorkloadPassEvidenceReusesPartialFailedRunAcrossTreeChanges exercises the SQLite
// provisional path with 68 workloads and keeps SourceTreeSHA outside the lookup identity.
func TestWorkloadPassEvidenceReusesPartialFailedRunAcrossTreeChanges(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	first := recordBulkFailedRun(t, store, "bulk-run-1", strings.Repeat("1", 40), 10)
	requireBulkCleanFailedRun(t, store, first)
	identities := bulkRunIdentities(first)
	firstEvidence := lookupBulkEvidence(t, store, identities)
	assertBulkEvidence(t, firstEvidence, 67)
	changedFailed := mutateBulkIdentity(t, identities[10], "changed-failed-run-2")
	second := bulkRetryRun(t, first, firstEvidence, "bulk-run-2", strings.Repeat("2", 40), bulkRetryMiss{10, changedFailed, ResultStatusFailed})
	recordBulkRetryOrFatal(t, store, second, "second cleaned failed run")
	requireBulkLookupCount(t, store, bulkReplaceIdentity(identities, 10, changedFailed), 67, "run2 workload lookup")
	changedSecondFailure := mutateBulkIdentity(t, identities[11], "changed-failed-run-3")
	third := bulkRetryRun(t, first, firstEvidence, "bulk-run-3", strings.Repeat("3", 40), bulkRetryMiss{10, identities[10], ResultStatusPassed}, bulkRetryMiss{11, changedSecondFailure, ResultStatusFailed})
	recordBulkRetryOrFatal(t, store, third, "third cleaned failed run")
	allEvidence := lookupBulkEvidence(t, store, identities)
	assertBulkEvidence(t, allEvidence, 68)
	fourth := bulkAllReusedRun(t, first, allEvidence, "bulk-run-4", strings.Repeat("4", 40))
	recordBulkRetryOrFatal(t, store, fourth, "all-reused tree-changed run")
	requireBulkLookupCount(t, store, identities, 68, "all-observable-identities lookup")
	changedSuccess := mutateBulkIdentity(t, identities[0], "changed-success-run-5")
	changedFailure := mutateBulkIdentity(t, identities[10], "changed-failed-run-5")
	requireBulkLookupCount(t, store, bulkReplaceIdentities(identities, map[int]WorkloadPassIdentity{0: changedSuccess, 10: changedFailure}), 66, "one successful and one failed workload changed lookup")
}

func requireBulkCleanFailedRun(t *testing.T, store *DurationLedgerStore, record RemoteCIRunRecord) {
	t.Helper()
	loaded, err := store.LoadRemoteCIRun(record.JobID)
	if err != nil {
		t.Fatalf("load first cleaned failed run: %v", err)
	}
	if loaded.Authoritative || loaded.Status != ResultStatusFailed || !loaded.CleanupComplete {
		t.Fatalf("first run authority projection = %#v", loaded)
	}
}

func recordBulkRetryOrFatal(t *testing.T, store *DurationLedgerStore, record RemoteCIRunRecord, label string) {
	t.Helper()
	if err := store.RecordProvisionalRemoteCIRun(record); err != nil {
		t.Fatalf("record %s: %v", label, err)
	}
}

func requireBulkLookupCount(t *testing.T, store *DurationLedgerStore, identities []WorkloadPassIdentity, want int, label string) {
	t.Helper()
	if got := bulkLookupCount(t, store, identities); got != want {
		t.Fatalf("%s count = %d, want %d", label, got, want)
	}
}

func bulkRunIdentities(record RemoteCIRunRecord) []WorkloadPassIdentity {
	identities := make([]WorkloadPassIdentity, len(record.WorkloadResults))
	for index, result := range record.WorkloadResults {
		identities[index] = result.Identity
	}
	return identities
}

func bulkWorkloadCatalog(t *testing.T) (WorkloadCatalog, []WorkloadPassIdentity) {
	t.Helper()
	workloads := make([]Workload, 68)
	identities := make([]WorkloadPassIdentity, 68)
	for index := range workloads {
		workloadID, err := targetWorkloadID(GateIDBackendTestWithGuard, workloadTargetGoPackage, fmt.Sprintf("./internal/devtools/gate/bulk%02d", index))
		if err != nil {
			t.Fatalf("target workload ID %d: %v", index, err)
		}
		workload := Workload{ID: workloadID, Kind: WorkloadKindGoTest, CommandDigest: fmt.Sprintf("%064x", index+1), InputDigest: bulkDigest(uint64(100 + index)), BootstrapEstimateMS: 1, Shardable: true}
		workloads[index] = workload
		identity := WorkloadPassIdentity{WorkloadID: GateID(workload.ID), ExecutionDigest: WorkloadPassExecutionDigest(workload), InputDigest: workload.InputDigest, EnvironmentDigest: bulkDigest(uint64(500 + index))}
		identity.IdentityDigest = workloadPassIdentityDigest(t, identity)
		identities[index] = identity
	}
	return WorkloadCatalog{Version: durationLedgerVersion, Authoritative: true, Workloads: workloads}, identities
}

func recordBulkFailedRun(t *testing.T, store *DurationLedgerStore, jobID, tree string, failedIndex int) RemoteCIRunRecord {
	t.Helper()
	catalog, identities := bulkWorkloadCatalog(t)
	catalogDigest, err := WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatalf("bulk catalog digest: %v", err)
	}
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	if err := store.RecordWorkloadCatalog(catalog, WorkloadCatalogObservation{SourceTreeSHA: tree, Entrypoint: CIEntrypointGitPreCommit, Profile: ProfileLocalFast, AcceptedGeneration: 1, ObservedAt: now}); err != nil {
		t.Fatalf("record bulk catalog: %v", err)
	}
	record := RemoteCIRunRecord{JobID: jobID, AgentTokenDigest: bulkDigest(1), Entrypoint: CIEntrypointGitPreCommit, Profile: ProfileLocalFast, PlanDigest: bulkDigest(2), CatalogDigest: catalogDigest, AcceptedGeneration: 1, ImageCacheSnapshotID: "snapshot-" + jobID, SourceTreeSHA: tree, CandidateGateSourceSHA256: bulkDigest(3), CandidateGateToolchainSHA256: bulkDigest(4), RunnerImage: "ubuntu:22.04", Status: ResultStatusFailed, StartedAt: now, CompletedAt: now.Add(2 * time.Minute), CleanupComplete: true, ErrorText: "bulk workload failure"}
	for index, workload := range catalog.Workloads {
		shard, execution := bulkExecutionParts(t, workload, record.StartedAt, jobID, index == failedIndex, index)
		record.Shards = append(record.Shards, shard)
		record.WorkloadExecutions = append(record.WorkloadExecutions, execution)
		record.WorkloadResults = append(record.WorkloadResults, RemoteCIWorkloadResult{Identity: identities[index], Disposition: WorkloadDispositionExecuted, OriginJobID: jobID, OriginAcceptedGeneration: 1})
	}
	if err := store.RecordProvisionalRemoteCIRun(record); err != nil {
		t.Fatalf("record bulk failed run: %v", err)
	}
	return record
}

func bulkExecutionParts(t *testing.T, workload Workload, started time.Time, jobID string, failed bool, index int) (RemoteCIShardRecord, PlanGateExecution) {
	shardIdentity := bulkDigest(uint64(10000 + index))
	goFlags, err := WorkloadExecutionGoFlags(workload.ID)
	if err != nil {
		t.Fatalf("bulk workload GoFlags %q: %v", workload.ID, err)
	}
	status, exitCode, containerStatus := ResultStatusPassed, 0, "Succeeded"
	if failed {
		status, exitCode, containerStatus = ResultStatusFailed, 17, "Failed"
	}
	executionStarted := started.Add(time.Duration(index+1) * time.Millisecond)
	execution := PlanGateExecution{ShardIdentity: shardIdentity, GateID: GateID(workload.ID), Status: status, ExitCode: exitCode, StartedAt: executionStarted, CompletedAt: executionStarted.Add(7 * time.Millisecond), ExecutionProfile: ExecutionProfile{GoFlags: goFlags, CacheSource: "go_build_cache", CacheStatus: CacheObservationMiss, CacheMeasurement: "measured", StartupMS: 1, TestBodyMS: 6, TotalMS: 7}}
	shard := RemoteCIShardRecord{ShardIdentity: shardIdentity, ContainerGroup: fmt.Sprintf("eci-%s-%02d", jobID, index), ContainerStatus: containerStatus, Workloads: []GateID{GateID(workload.ID)}, MaterializationTiming: measuredShardMaterializationTiming(shardIdentity), Resources: RemoteCIShardResources{ClassID: "fixed", CPU: 4, MemoryGiB: 8}}
	return shard, execution
}

func bulkRetryExecutionParts(previous PlanGateExecution, started time.Time, jobID string, failed bool, index int) (RemoteCIShardRecord, PlanGateExecution) {
	shardIdentity := bulkDigest(uint64(20000 + index))
	status, exitCode, containerStatus := ResultStatusPassed, 0, "Succeeded"
	if failed {
		status, exitCode, containerStatus = ResultStatusFailed, 17, "Failed"
	}
	execution := previous
	execution.ShardIdentity, execution.Status, execution.ExitCode = shardIdentity, status, exitCode
	execution.StartedAt, execution.CompletedAt = started.Add(time.Duration(index+1)*time.Millisecond), started.Add(time.Duration(index+8)*time.Millisecond)
	shard := RemoteCIShardRecord{ShardIdentity: shardIdentity, ContainerGroup: fmt.Sprintf("eci-%s-%02d", jobID, index), ContainerStatus: containerStatus, Workloads: []GateID{execution.GateID}, MaterializationTiming: measuredShardMaterializationTiming(shardIdentity), Resources: RemoteCIShardResources{ClassID: "fixed", CPU: 4, MemoryGiB: 8}}
	return shard, execution
}

type bulkRetryMiss struct {
	index    int
	identity WorkloadPassIdentity
	status   ResultStatus
}

func bulkRetryRun(t *testing.T, first RemoteCIRunRecord, evidence []WorkloadPassEvidence, jobID, tree string, misses ...bulkRetryMiss) RemoteCIRunRecord {
	t.Helper()
	missByIndex := make(map[int]bulkRetryMiss, len(misses))
	for _, miss := range misses {
		missByIndex[miss.index] = miss
	}
	evidenceByWorkload := make(map[GateID]WorkloadPassEvidence, len(evidence))
	for _, item := range evidence {
		evidenceByWorkload[item.Identity.WorkloadID] = item
	}
	record := first
	record.JobID, record.SourceTreeSHA, record.AgentTokenDigest = jobID, tree, bulkDigest(uint64(len(jobID)+200))
	record.ImageCacheSnapshotID, record.StartedAt = "snapshot-"+jobID, first.StartedAt.Add(time.Hour)
	record.CompletedAt, record.ErrorText = record.StartedAt.Add(2*time.Minute), "bulk retry failure"
	record.WorkloadResults, record.WorkloadExecutions, record.Shards = nil, nil, nil
	for index, original := range first.WorkloadResults {
		miss, fresh := missByIndex[index]
		if !fresh {
			item, ok := evidenceByWorkload[original.Identity.WorkloadID]
			if !ok {
				t.Fatalf("missing evidence for reused workload %q", original.Identity.WorkloadID)
			}
			original.Disposition, original.OriginJobID, original.OriginAcceptedGeneration, original.EvidenceSHA256 = WorkloadDispositionReused, item.OriginJobID, item.OriginAcceptedGeneration, item.EvidenceSHA256
			record.WorkloadResults = append(record.WorkloadResults, original)
			continue
		}
		shard, execution := bulkRetryExecutionParts(first.WorkloadExecutions[index], record.StartedAt, jobID, miss.status != ResultStatusPassed, index+100)
		execution.GateID, execution.Status = miss.identity.WorkloadID, miss.status
		record.Shards = append(record.Shards, shard)
		record.WorkloadExecutions = append(record.WorkloadExecutions, execution)
		record.WorkloadResults = append(record.WorkloadResults, RemoteCIWorkloadResult{Identity: miss.identity, Disposition: WorkloadDispositionExecuted, OriginJobID: jobID, OriginAcceptedGeneration: 1})
	}
	return record
}

func bulkAllReusedRun(t *testing.T, first RemoteCIRunRecord, evidence []WorkloadPassEvidence, jobID, tree string) RemoteCIRunRecord {
	record := bulkRetryRun(t, first, evidence, jobID, tree)
	record.Status, record.CandidateGateSourceSHA256, record.CandidateGateToolchainSHA256 = ResultStatusPassed, "", ""
	record.Shards, record.WorkloadExecutions = nil, nil
	return record
}

func lookupBulkEvidence(t *testing.T, store *DurationLedgerStore, identities []WorkloadPassIdentity) []WorkloadPassEvidence {
	t.Helper()
	evidence, err := store.LookupWorkloadPassEvidence(identities)
	if err != nil {
		t.Fatalf("lookup bulk workload evidence: %v", err)
	}
	return evidence
}

func assertBulkEvidence(t *testing.T, evidence []WorkloadPassEvidence, want int) {
	t.Helper()
	if len(evidence) != want {
		t.Fatalf("bulk workload evidence count = %d, want %d", len(evidence), want)
	}
	for _, item := range evidence {
		if item.OriginExecution.Status != ResultStatusPassed || item.OriginExecution.ExitCode != 0 || item.OriginExecution.CompletedAt.IsZero() {
			t.Fatalf("bulk evidence has non-passing/incomplete execution: %#v", item)
		}
	}
}

func bulkLookupCount(t *testing.T, store *DurationLedgerStore, identities []WorkloadPassIdentity) int {
	return len(lookupBulkEvidence(t, store, identities))
}

func bulkReplaceIdentity(identities []WorkloadPassIdentity, index int, replacement WorkloadPassIdentity) []WorkloadPassIdentity {
	return bulkReplaceIdentities(identities, map[int]WorkloadPassIdentity{index: replacement})
}

func bulkReplaceIdentities(identities []WorkloadPassIdentity, replacements map[int]WorkloadPassIdentity) []WorkloadPassIdentity {
	result := append([]WorkloadPassIdentity(nil), identities...)
	for index, replacement := range replacements {
		result[index] = replacement
	}
	return result
}

func mutateBulkIdentity(t *testing.T, identity WorkloadPassIdentity, label string) WorkloadPassIdentity {
	t.Helper()
	sum := sha256.Sum256([]byte(label))
	identity.InputDigest = fmt.Sprintf("sha256:%x", sum)
	identity.IdentityDigest = workloadPassIdentityDigest(t, identity)
	return identity
}

func bulkDigest(value uint64) string {
	return fmt.Sprintf("sha256:%064x", value)
}

func TestWorkloadPassEvidenceDoesNotPromoteWhenCleanupIsIncomplete(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, identity, _ := recordWorkloadPassRun(t, store, "failed-dirty", 1, "failed-dirty-workload")
	record.Status = ResultStatusFailed
	record.Authoritative = false
	record.CleanupComplete = false
	if err := store.RecordProvisionalRemoteCIRun(record); err != nil {
		t.Fatalf("record dirty failed run: %v", err)
	}
	evidence, err := store.LookupWorkloadPassEvidence([]WorkloadPassIdentity{identity})
	if err != nil {
		t.Fatalf("lookup dirty failed run: %v", err)
	}
	if len(evidence) != 0 {
		t.Fatalf("dirty failed-run evidence = %#v, want miss", evidence)
	}
}

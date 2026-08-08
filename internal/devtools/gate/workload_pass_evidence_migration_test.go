package gate

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"
)

// TestDurationLedgerSQLiteRepairsMissingPassReuseIndexes 验证旧 current schema
// 缺复用索引时只执行原子 CREATE INDEX，保留既有数据与版本。
func TestDurationLedgerSQLiteRepairsMissingPassReuseIndexes(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	database := openWorkloadPassDatabase(t, store)
	prepareMissingPassReuseIndexes(t, database)
	reopened, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatalf("repair missing reusable PASS index on reopen: %v", err)
	}
	database = reopened
	defer database.Close()
	assertPassReuseIndexesRepaired(t, database)
}

// prepareMissingPassReuseIndexes 将两个可迁移索引置为缺失，保留 marker 与其余 schema。
func prepareMissingPassReuseIndexes(t *testing.T, database *sql.DB) {
	t.Helper()
	statements := []string{
		`INSERT INTO ci_schema_migrations(name, applied_at_unix_ms) VALUES ('before-reusable-pass-index', 1)`,
		`DROP INDEX idx_ci_runs_reusable_pass`,
		`DROP INDEX idx_ci_workload_pass_evidence_migration`,
	}
	for _, statement := range statements {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
}

// assertPassReuseIndexesRepaired 检查索引、版本和既有 migration marker 均保留。
func assertPassReuseIndexesRepaired(t *testing.T, database *sql.DB) {
	t.Helper()
	if _, ok := sqliteIndexListForTest(t, database, "ci_runs")["idx_ci_runs_reusable_pass"]; !ok {
		t.Fatal("repaired schema omitted reusable PASS index")
	}
	if _, ok := sqliteIndexListForTest(t, database, "ci_workload_pass_evidence")["idx_ci_workload_pass_evidence_migration"]; !ok {
		t.Fatal("repaired schema omitted migration workload tuple index")
	}
	var rows int
	if err := database.QueryRow(`SELECT COUNT(*) FROM ci_schema_migrations WHERE name = 'before-reusable-pass-index'`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("migration marker rows = %d err=%v, want 1", rows, err)
	}
	if got := durationLedgerSQLiteUserVersionForTest(t, database); got != durationLedgerSQLiteSchemaVersion {
		t.Fatalf("repaired user_version = %d, want %d", got, durationLedgerSQLiteSchemaVersion)
	}
}

// TestWorkloadPassEvidenceMigrationQueryPlansUseBoundedIndexes 锁定 requested tuple
// migration 查询使用 workload-leading 复合索引，且不退化为 evidence 全表扫。
func TestWorkloadPassEvidenceMigrationQueryPlansUseBoundedIndexes(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 12)
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	identity := WorkloadPassIdentity{WorkloadID: GateIDBackendTestWithGuard, ExecutionDigest: digestForWorkloadPass("migration-plan-execution"), EnvironmentDigest: digestForWorkloadPass("migration-plan-environment"), InputDigest: digestForWorkloadPass("migration-plan-input")}
	identity.IdentityDigest = workloadPassIdentityDigest(t, identity)
	query, args := workloadPassEvidenceMigrationLookupQuery(map[string]WorkloadPassIdentity{workloadPassEvidenceMigrationIdentityKey(identity): identity}, retainedWorkloadPassGenerations(12))
	details := sqliteQueryPlanDetails(t, database, query, args...)
	assertSQLiteQueryPlanAccess(t, details, []string{"USING INDEX idx_ci_workload_pass_evidence_migration"})
	assertSQLiteQueryPlanNoFullTableScan(t, details, []string{"ci_workload_pass_evidence"})
}

// TestWorkloadPassEvidenceMigrationLookupQueryPlanUsesRequestedTupleIndex
// 锁定 migration 的 workload/execution/environment + retained generation 查询
// 走 workload-leading 复合索引，不退化为 evidence 全表扫描。
func TestWorkloadPassEvidenceMigrationLookupQueryPlanUsesRequestedTupleIndex(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 12)
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	identity := WorkloadPassIdentity{
		WorkloadID:        GateIDBackendTestWithGuard,
		ExecutionDigest:   digestForWorkloadPass("migration-query-execution"),
		InputDigest:       digestForWorkloadPass("migration-query-input"),
		EnvironmentDigest: digestForWorkloadPass("migration-query-environment"),
	}
	identity.IdentityDigest = workloadPassIdentityDigest(t, identity)
	query, args := workloadPassEvidenceMigrationLookupQuery(
		map[string]WorkloadPassIdentity{workloadPassEvidenceMigrationIdentityKey(identity): identity},
		retainedWorkloadPassGenerations(12),
	)
	details := sqliteQueryPlanDetails(t, database, query, args...)
	assertSQLiteQueryPlanAccess(t, details, []string{"USING INDEX idx_ci_workload_pass_evidence_migration"})
	assertSQLiteQueryPlanNoFullTableScan(t, details, []string{"ci_workload_pass_evidence"})
}

// TestWorkloadPassEvidenceAliasLookupQueryPlanUsesAliasKey 锁定后续 lookup
// 通过 alias 主键查找 source，不退化为关系表全表扫描。
func TestWorkloadPassEvidenceAliasLookupQueryPlanUsesAliasKey(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	details := sqliteQueryPlanDetails(t, database, `SELECT source_identity_digest, source_accepted_generation FROM ci_workload_pass_evidence_aliases WHERE alias_identity_digest = ? AND alias_accepted_generation = ?`, "sha256:"+fmt.Sprintf("%064d", 1), "1")
	assertSQLiteQueryPlanAccess(t, details, []string{"USING INDEX sqlite_autoindex_ci_workload_pass_evidence_aliases_1"})
	assertSQLiteQueryPlanNoFullTableScan(t, details, []string{"ci_workload_pass_evidence_aliases"})
}

// TestLookupWorkloadPassEvidenceMigrationCandidatesIgnoresNewInputDigest 验证
// migration API 只按 workload/execution/environment 找历史候选，输入摘要由
// remoteci 在 exact-tree 上重新计算，gate 不会把旧 identity 当作新 identity。
func TestLookupWorkloadPassEvidenceMigrationCandidatesIgnoresNewInputDigest(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 3)
	record, historical, receipts := recordWorkloadPassRun(t, store, "migration-input-drift", 3, "migration-input-drift-workload")
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, true); err != nil {
		t.Fatal(err)
	}

	requested := historical
	requested.InputDigest = digestForWorkloadPass("migration-new-input")
	requested.IdentityDigest = workloadPassIdentityDigest(t, requested)
	candidates, err := store.LookupWorkloadPassEvidenceMigrationCandidates([]WorkloadPassIdentity{requested})
	if err != nil {
		t.Fatalf("lookup migration candidates: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("migration candidate count = %d, want 1", len(candidates))
	}
	if candidates[0].Identity != historical {
		t.Fatalf("migration candidate identity = %#v, want historical %#v", candidates[0].Identity, historical)
	}
	if candidates[0].OriginJobID != record.JobID {
		t.Fatalf("migration candidate origin job = %q, want %q", candidates[0].OriginJobID, record.JobID)
	}
}

// TestLookupWorkloadPassEvidenceMigrationCandidatesAllowsPassedProjectionInFailedRun
// 验证整体 failed 但 cleanup 完成的 run 仍可返回其独立 passed workload 投影。
func TestLookupWorkloadPassEvidenceMigrationCandidatesAllowsPassedProjectionInFailedRun(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, historical, _ := recordWorkloadPassRun(t, store, "migration-failed-run", 1, "migration-failed-run-workload")
	record.Status = ResultStatusFailed
	record.Authoritative = false
	if err := store.RecordProvisionalRemoteCIRun(record); err != nil {
		t.Fatalf("record failed projection: %v", err)
	}
	requested := historical
	requested.InputDigest = digestForWorkloadPass("migration-failed-run-new-input")
	requested.IdentityDigest = workloadPassIdentityDigest(t, requested)
	candidates, err := store.LookupWorkloadPassEvidenceMigrationCandidates([]WorkloadPassIdentity{requested})
	if err != nil {
		t.Fatalf("lookup failed-run migration candidate: %v", err)
	}
	if len(candidates) != 1 || candidates[0].OriginExecution.Status != ResultStatusPassed {
		t.Fatalf("failed-run migration candidates = %#v, want one passed workload projection", candidates)
	}
}

// TestLookupWorkloadPassEvidenceMigrationCandidatesKeepsAuthoritativeRunBesideFailedRun
// 验证同代较新的局部失败投影不会遮蔽较早完整权威 run 的 PASS 证据。
func TestLookupWorkloadPassEvidenceMigrationCandidatesKeepsAuthoritativeRunBesideFailedRun(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	authoritative, historical, receipts := recordWorkloadPassRun(t, store, "migration-authoritative-run", 1, "migration-authoritative-workload")
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(authoritative), receipts, nil, true); err != nil {
		t.Fatal(err)
	}
	failed, _, _ := recordWorkloadPassRun(t, store, "migration-newer-failed-run", 1, "migration-failed-workload")
	failed.Status = ResultStatusFailed
	failed.Authoritative = false
	failed.CompletedAt = authoritative.CompletedAt.Add(time.Second)
	if err := store.RecordProvisionalRemoteCIRun(failed); err != nil {
		t.Fatalf("record newer failed projection: %v", err)
	}
	requested := historical
	requested.InputDigest = digestForWorkloadPass("migration-authoritative-new-input")
	requested.IdentityDigest = workloadPassIdentityDigest(t, requested)
	candidates, err := store.LookupWorkloadPassEvidenceMigrationCandidates([]WorkloadPassIdentity{requested})
	if err != nil {
		t.Fatalf("lookup authoritative migration candidate: %v", err)
	}
	if len(candidates) != 1 || candidates[0].OriginJobID != authoritative.JobID {
		t.Fatalf("migration candidates = %#v, want authoritative job %q", candidates, authoritative.JobID)
	}
}

// TestLookupWorkloadPassEvidenceMigrationCandidatesReadsDispersedAuthoritativeOrigins
// 验证同一代较新的无关权威 run 不会遮蔽较早 origin 中的 requested PASS。
func TestLookupWorkloadPassEvidenceMigrationCandidatesReadsDispersedAuthoritativeOrigins(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	origin, historical, receipts := recordWorkloadPassRunAt(t, store, "migration-dispersed-origin", 1, "migration-dispersed-target", time.Date(2026, 8, 8, 1, 0, 0, 0, time.UTC))
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(origin), receipts, nil, true); err != nil {
		t.Fatalf("finalize dispersed origin: %v", err)
	}
	newer, _, newerReceipts := recordWorkloadPassRunAt(t, store, "migration-dispersed-newer-unrelated", 1, "migration-dispersed-unrelated", time.Date(2026, 8, 8, 2, 0, 0, 0, time.UTC))
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(newer), newerReceipts, nil, true); err != nil {
		t.Fatalf("finalize newer unrelated origin: %v", err)
	}
	requested := historical
	requested.InputDigest = digestForWorkloadPass("migration-dispersed-current-input")
	requested.IdentityDigest = workloadPassIdentityDigest(t, requested)
	candidates, err := store.LookupWorkloadPassEvidenceMigrationCandidates([]WorkloadPassIdentity{requested})
	if err != nil {
		t.Fatalf("lookup dispersed migration candidates: %v", err)
	}
	if len(candidates) != 1 || candidates[0].OriginJobID != origin.JobID {
		t.Fatalf("dispersed migration candidates = %#v, want origin %q", candidates, origin.JobID)
	}
}

// TestLookupWorkloadPassEvidenceMigrationCandidatesReturnsRetainedCandidates
// 锁定 current+前两代的有界多候选行为；调用方发现同 workload 的多个历史
// candidate 后必须自行判定歧义，gate 不会修改旧行或写 alias。
func TestLookupWorkloadPassEvidenceMigrationCandidatesReturnsRetainedCandidates(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 3)
	var historical WorkloadPassIdentity
	for _, generation := range []uint64{3, 2, 1} {
		record, identity, receipts := recordWorkloadPassRun(t, store, "migration-retained-"+string(rune('0'+generation)), generation, "migration-retained-workload")
		if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, true); err != nil {
			t.Fatal(err)
		}
		historical = identity
	}
	requested := historical
	requested.InputDigest = digestForWorkloadPass("migration-retained-new-input")
	requested.IdentityDigest = workloadPassIdentityDigest(t, requested)
	candidates, err := store.LookupWorkloadPassEvidenceMigrationCandidates([]WorkloadPassIdentity{requested})
	if err != nil {
		t.Fatalf("lookup retained migration candidates: %v", err)
	}
	if len(candidates) != 3 {
		t.Fatalf("retained migration candidate count = %d, want 3", len(candidates))
	}
	for index, wantGeneration := range []uint64{3, 2, 1} {
		if candidates[index].OriginAcceptedGeneration != wantGeneration {
			t.Fatalf("candidate[%d] generation = %d, want %d", index, candidates[index].OriginAcceptedGeneration, wantGeneration)
		}
	}
}

// TestLookupWorkloadPassEvidenceMigrationCandidatesBatchesLargeRequest 验证超过单批
// 1024 个 workload 仍按有界批次完成，只返回真实可验证候选而不扫描整表。
func TestLookupWorkloadPassEvidenceMigrationCandidatesBatchesLargeRequest(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	identities := make([]WorkloadPassIdentity, workloadPassEvidenceMigrationBatchSize+1)
	for index := range identities {
		identity := WorkloadPassIdentity{
			WorkloadID:        GateID(fmt.Sprintf("migration-batch-%04d", index)),
			ExecutionDigest:   digestForWorkloadPass(fmt.Sprintf("batch-execution-%04d", index)),
			InputDigest:       digestForWorkloadPass(fmt.Sprintf("batch-input-%04d", index)),
			EnvironmentDigest: digestForWorkloadPass("batch-environment"),
		}
		identity.IdentityDigest = workloadPassIdentityDigest(t, identity)
		identities[index] = identity
	}
	candidates, err := store.LookupWorkloadPassEvidenceMigrationCandidates(identities)
	if err != nil {
		t.Fatalf("lookup large migration request: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("large migration request candidates = %d, want empty fixture result", len(candidates))
	}
}

// TestLookupWorkloadPassEvidenceMigrationCandidatesReturnsAllRowsAcrossBatches
// 锁定大于 1024 的请求不会按每批或每代行数截断真实历史候选。
func TestLookupWorkloadPassEvidenceMigrationCandidatesReturnsAllRowsAcrossBatches(t *testing.T) {
	const evidenceCount = workloadPassEvidenceMigrationBatchSize + 1
	store := newWorkloadPassEvidenceStore(t, 1)
	origin, historical, receipts := recordWorkloadPassRun(t, store, "migration-over-1024-origin", 1, "migration-over-1024")
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(origin), receipts, nil, true); err != nil {
		t.Fatalf("finalize over-1024 origin: %v", err)
	}
	originEvidence := lookupSingleWorkloadPassEvidence(t, store, historical)
	database := openWorkloadPassDatabase(t, store)
	insertBatchScaleEvidence(t, database, originEvidence, evidenceCount)
	database.Close()

	identities := batchScaleIdentities(t, originEvidence, evidenceCount)
	for index := range identities {
		identities[index].InputDigest = digestForWorkloadPass(fmt.Sprintf("migration-current-input-%04d", index))
		identities[index].IdentityDigest = workloadPassIdentityDigest(t, identities[index])
	}
	candidates, err := store.LookupWorkloadPassEvidenceMigrationCandidates(identities)
	if err != nil {
		t.Fatalf("lookup over-1024 migration candidates: %v", err)
	}
	if len(candidates) != evidenceCount {
		t.Fatalf("over-1024 migration candidates = %d, want %d", len(candidates), evidenceCount)
	}
}

// TestRecordMigratedWorkloadPassEvidencePersistsProvisionalAlias verifies that
// a passing workload from a cleaned provisional failure can be rehashed into a
// durable alias, while the source row remains authoritative for later lookup.
func TestRecordMigratedWorkloadPassEvidencePersistsProvisionalAlias(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, historical, _ := recordWorkloadPassRun(t, store, "migration-alias-provisional", 1, "migration-alias-provisional-workload")
	record.Status = ResultStatusFailed
	record.Authoritative = false
	if err := store.RecordProvisionalRemoteCIRun(record); err != nil {
		t.Fatalf("record cleaned provisional failure: %v", err)
	}
	source := lookupSingleWorkloadPassEvidence(t, store, historical)
	projected := source
	projected.Identity.InputDigest = digestForWorkloadPass("migration-alias-projected-input")
	projected.Identity.IdentityDigest = workloadPassIdentityDigest(t, projected.Identity)
	projected.EvidenceSHA256 = evidenceDigestForTest(t, projected)
	if err := store.RecordMigratedWorkloadPassEvidence([]WorkloadPassEvidenceMigration{{Source: source, Projected: projected}}); err != nil {
		t.Fatalf("persist provisional-origin alias: %v", err)
	}
	if err := store.RecordMigratedWorkloadPassEvidence([]WorkloadPassEvidenceMigration{{Source: source, Projected: projected}}); err != nil {
		t.Fatalf("persist idempotent provisional-origin alias: %v", err)
	}
	database := openWorkloadPassDatabase(t, store)
	var sourceRows, aliasRows int
	if err := database.QueryRow(`SELECT COUNT(*) FROM ci_workload_pass_evidence WHERE identity_digest = ?`, source.Identity.IdentityDigest).Scan(&sourceRows); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM ci_workload_pass_evidence_aliases WHERE alias_identity_digest = ?`, projected.Identity.IdentityDigest).Scan(&aliasRows); err != nil {
		t.Fatal(err)
	}
	if sourceRows != 1 || aliasRows != 1 {
		t.Fatalf("persisted source/alias rows = %d/%d, want 1/1", sourceRows, aliasRows)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	got := lookupSingleWorkloadPassEvidence(t, store, projected.Identity)
	if !reflect.DeepEqual(got, projected) {
		t.Fatalf("reopened alias lookup = %#v, want %#v", got, projected)
	}
}

// TestLookupWorkloadPassEvidenceSeparatesAliasesBySourceIdentity 验证同一来源 run
// 中多个不同 source identity 的 alias 批量查询不会复用错误的 source 上下文。
func TestLookupWorkloadPassEvidenceSeparatesAliasesBySourceIdentity(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, historical, receipts := recordWorkloadPassRun(t, store, "migration-alias-cache-key", 1, "migration-alias-cache-first")
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, true); err != nil {
		t.Fatalf("finalize alias cache-key origin: %v", err)
	}
	firstSource := lookupSingleWorkloadPassEvidence(t, store, historical)
	secondSource := firstSource
	secondSource.Identity.WorkloadID = GateID("migration-alias-cache-second")
	secondSource.Identity.ExecutionDigest = digestForWorkloadPass("migration-alias-cache-second-execution")
	secondSource.Identity.InputDigest = digestForWorkloadPass("migration-alias-cache-second-input")
	secondSource.Identity.EnvironmentDigest = digestForWorkloadPass("migration-alias-cache-second-environment")
	secondSource.Identity.IdentityDigest = workloadPassIdentityDigest(t, secondSource.Identity)
	secondSource.OriginExecution.GateID = secondSource.Identity.WorkloadID
	secondSource.EvidenceSHA256 = evidenceDigestForTest(t, secondSource)
	insertWorkloadPassEvidenceRowForTest(t, store, secondSource)

	firstProjected := firstSource
	firstProjected.Identity.InputDigest = digestForWorkloadPass("migration-alias-cache-first-projected-input")
	firstProjected.Identity.IdentityDigest = workloadPassIdentityDigest(t, firstProjected.Identity)
	firstProjected.EvidenceSHA256 = evidenceDigestForTest(t, firstProjected)
	secondProjected := secondSource
	secondProjected.Identity.InputDigest = digestForWorkloadPass("migration-alias-cache-second-projected-input")
	secondProjected.Identity.IdentityDigest = workloadPassIdentityDigest(t, secondProjected.Identity)
	secondProjected.EvidenceSHA256 = evidenceDigestForTest(t, secondProjected)
	if err := store.RecordMigratedWorkloadPassEvidence([]WorkloadPassEvidenceMigration{
		{Source: firstSource, Projected: firstProjected},
		{Source: secondSource, Projected: secondProjected},
	}); err != nil {
		t.Fatalf("persist aliases with shared origin job: %v", err)
	}
	requested := []WorkloadPassIdentity{firstProjected.Identity, secondProjected.Identity}
	got, err := store.LookupWorkloadPassEvidence(requested)
	if err != nil {
		t.Fatalf("lookup aliases after store reopen: %v", err)
	}
	if len(got) != len(requested) {
		t.Fatalf("reopened alias lookup count = %d, want %d", len(got), len(requested))
	}
	for index, want := range []WorkloadPassEvidence{firstProjected, secondProjected} {
		if !reflect.DeepEqual(got[index], want) {
			t.Fatalf("reopened alias lookup[%d] = %#v, want %#v", index, got[index], want)
		}
	}
}

// insertWorkloadPassEvidenceRowForTest 写入第二条同源 run 的合法 source 行，专门构造
// alias origin cache key 回归，不绕过 RecordMigratedWorkloadPassEvidence 的 source 验证。
func insertWorkloadPassEvidenceRowForTest(t *testing.T, store *DurationLedgerStore, evidence WorkloadPassEvidence) {
	t.Helper()
	encoded, err := json.Marshal(evidence.OriginExecution)
	if err != nil {
		t.Fatal(err)
	}
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	_, err = database.Exec(`INSERT INTO ci_workload_pass_evidence (identity_digest, accepted_generation, workload_id, execution_digest, input_digest, environment_digest, origin_job_id, origin_source_tree_sha, origin_receipt_set_sha256, origin_execution_json, evidence_sha256) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, evidence.Identity.IdentityDigest, fmt.Sprintf("%d", evidence.OriginAcceptedGeneration), string(evidence.Identity.WorkloadID), evidence.Identity.ExecutionDigest, evidence.Identity.InputDigest, evidence.Identity.EnvironmentDigest, evidence.OriginJobID, evidence.OriginSourceTreeSHA, evidence.OriginReceiptSetSHA256, string(encoded), evidence.EvidenceSHA256)
	if err != nil {
		t.Fatalf("insert second source evidence: %v", err)
	}
}

// TestRecordMigratedWorkloadPassEvidenceRejectsTamperedPair verifies that a
// source/projection origin mismatch rolls back before writing either row.
func TestRecordMigratedWorkloadPassEvidenceRejectsTamperedPair(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, historical, _ := recordWorkloadPassRun(t, store, "migration-alias-tampered", 1, "migration-alias-tampered-workload")
	record.Status = ResultStatusFailed
	record.Authoritative = false
	if err := store.RecordProvisionalRemoteCIRun(record); err != nil {
		t.Fatalf("record cleaned provisional failure: %v", err)
	}
	source := lookupSingleWorkloadPassEvidence(t, store, historical)
	projected := source
	projected.Identity.InputDigest = digestForWorkloadPass("migration-alias-tampered-input")
	projected.Identity.IdentityDigest = workloadPassIdentityDigest(t, projected.Identity)
	projected.OriginJobID = "tampered-origin-job"
	projected.EvidenceSHA256 = evidenceDigestForTest(t, projected)
	if err := store.RecordMigratedWorkloadPassEvidence([]WorkloadPassEvidenceMigration{{Source: source, Projected: projected}}); err == nil {
		t.Fatal("tampered source/projection pair was accepted")
	}
	database := openWorkloadPassDatabase(t, store)
	var projectedRows, aliasRows int
	if err := database.QueryRow(`SELECT COUNT(*) FROM ci_workload_pass_evidence WHERE identity_digest = ?`, projected.Identity.IdentityDigest).Scan(&projectedRows); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM ci_workload_pass_evidence_aliases WHERE alias_identity_digest = ?`, projected.Identity.IdentityDigest).Scan(&aliasRows); err != nil {
		t.Fatal(err)
	}
	if projectedRows != 0 || aliasRows != 0 {
		t.Fatalf("tampered pair persisted rows = %d/%d, want 0/0", projectedRows, aliasRows)
	}
	database.Close()
}

// TestDurationLedgerSQLiteV12AliasMigrationRetainsEvidence verifies the
// explicit v12-to-v13 relation migration leaves every historical evidence row.
func TestDurationLedgerSQLiteV12AliasMigrationRetainsEvidence(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	historical, source := prepareDurationLedgerSQLiteV12AliasFixture(t, store)
	assertDurationLedgerSQLiteV12AliasMigration(t, store, historical, source)
}

// prepareDurationLedgerSQLiteV12AliasFixture 写入 source evidence 后降级物理版本，模拟真实 v12 authority。
func prepareDurationLedgerSQLiteV12AliasFixture(t *testing.T, store *DurationLedgerStore) (WorkloadPassIdentity, WorkloadPassEvidence) {
	record, historical, _ := recordWorkloadPassRun(t, store, "migration-v12-alias", 1, "migration-v12-alias-workload")
	record.Status = ResultStatusFailed
	record.Authoritative = false
	if err := store.RecordProvisionalRemoteCIRun(record); err != nil {
		t.Fatalf("record v12 source run: %v", err)
	}
	source := lookupSingleWorkloadPassEvidence(t, store, historical)
	database := openWorkloadPassDatabase(t, store)
	if _, err := database.Exec(`DROP TABLE ci_workload_pass_evidence_aliases`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`PRAGMA user_version = 12`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return historical, source
}

// assertDurationLedgerSQLiteV12AliasMigration 检查升级后的版本、关系表和历史 source 行均保留。
func assertDurationLedgerSQLiteV12AliasMigration(t *testing.T, store *DurationLedgerStore, historical WorkloadPassIdentity, source WorkloadPassEvidence) {
	reopened, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatalf("upgrade v12 authority: %v", err)
	}
	defer reopened.Close()
	var sourceRows int
	if err := reopened.QueryRow(`SELECT COUNT(*) FROM ci_workload_pass_evidence WHERE identity_digest = ?`, source.Identity.IdentityDigest).Scan(&sourceRows); err != nil {
		t.Fatal(err)
	}
	if sourceRows != 1 {
		t.Fatalf("v12 migration source rows = %d, want 1", sourceRows)
	}
	if got := durationLedgerSQLiteUserVersionForTest(t, reopened); got != durationLedgerSQLiteSchemaVersion {
		t.Fatalf("v12 migration user_version = %d, want %d", got, durationLedgerSQLiteSchemaVersion)
	}
	if _, err := reopened.Exec(`SELECT source_identity_digest FROM ci_workload_pass_evidence_aliases WHERE 1 = 0`); err != nil {
		t.Fatalf("v12 migration omitted alias relation: %v", err)
	}
	if got := lookupSingleWorkloadPassEvidence(t, store, historical); !reflect.DeepEqual(got, source) {
		t.Fatalf("v12 migration lookup = %#v, want source %#v", got, source)
	}
}

func evidenceDigestForTest(t *testing.T, evidence WorkloadPassEvidence) string {
	t.Helper()
	digest, err := WorkloadPassEvidenceSHA256(evidence)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

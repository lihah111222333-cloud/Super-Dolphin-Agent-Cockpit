package gate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWorkloadPassEvidenceLookupQueryPlanUsesIdentityIndex 锁定 identity-first 候选查询；
// origin run 留给后续 strict validator 单独按 job_id 索引加载，禁止 JOIN 提前隐藏漂移 proof。
func TestWorkloadPassEvidenceLookupQueryPlanUsesIdentityIndex(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 12)
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	identity := WorkloadPassIdentity{
		WorkloadID:        GateIDBackendTestWithGuard,
		ExecutionDigest:   digestForWorkloadPass("query-plan-execution"),
		InputDigest:       digestForWorkloadPass("query-plan-input"),
		EnvironmentDigest: digestForWorkloadPass("query-plan-environment"),
	}
	identity.IdentityDigest = workloadPassIdentityDigest(t, identity)
	retained := retainedWorkloadPassGenerations(12)
	query, args := workloadPassEvidenceBatchQuery([]WorkloadPassIdentity{identity}, retained)
	details := sqliteQueryPlanDetails(t, database, query, args...)
	assertSQLiteQueryPlanAccess(t, details, []string{
		"SEARCH evidence USING INDEX sqlite_autoindex_ci_workload_pass_evidence_1 (identity_digest=? AND accepted_generation=?)",
		"SEARCH proof USING INDEX idx_ci_retained_workload_pass_proofs_lookup (identity_digest=?)",
	})
	assertSQLiteQueryPlanNoFullTableScan(t, details, []string{"evidence", "proof"})
}

// TestWorkloadPassSourceReplayQueryPlanUsesPartitionIndexes 锁定 direct proof 按
// workload/execution/environment 分区、retained proof 按 workload 分区，禁止重复扫整代。
func TestWorkloadPassSourceReplayQueryPlanUsesPartitionIndexes(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 12)
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	identity := WorkloadPassIdentity{
		WorkloadID:        GateIDBackendTestWithGuard,
		ExecutionDigest:   digestForWorkloadPass("source-replay-query-execution"),
		InputDigest:       digestForWorkloadPass("source-replay-query-input"),
		EnvironmentDigest: digestForWorkloadPass("source-replay-query-environment"),
	}
	identity.IdentityDigest = workloadPassIdentityDigest(t, identity)
	query, args := workloadPassSourceReplayQuery([]WorkloadPassIdentity{identity}, retainedWorkloadPassGenerations(12))
	details := sqliteQueryPlanDetails(t, database, query, args...)
	assertSQLiteQueryPlanAccess(t, details, []string{
		"SEARCH evidence USING INDEX idx_ci_workload_pass_evidence_source_replay (workload_id=? AND execution_digest=? AND environment_digest=? AND accepted_generation=?)",
		"SEARCH proof USING INDEX idx_ci_retained_workload_pass_proofs_source_replay (workload_id=?)",
	})
	assertSQLiteQueryPlanNoFullTableScan(t, details, []string{"ci_workload_pass_evidence", "ci_retained_workload_pass_proofs"})
}

// TestWorkloadPassEnvironmentReplayQueryPlanUsesPartitionIndexes 锁定批量请求先按
// workload/execution 分区索引收窄；不得退回 generation 全分区扫描。
func TestWorkloadPassEnvironmentReplayQueryPlanUsesPartitionIndexes(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 12)
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	identities := make([]WorkloadPassIdentity, workloadPassEnvironmentReplayBatchSize)
	for index := range identities {
		identity := WorkloadPassIdentity{
			WorkloadID:        GateID(fmt.Sprintf("environment-replay-%03d", index)),
			ExecutionDigest:   digestForWorkloadPass(fmt.Sprintf("environment-replay-execution-%03d", index)),
			InputDigest:       digestForWorkloadPass(fmt.Sprintf("environment-replay-input-%03d", index)),
			EnvironmentDigest: digestForWorkloadPass(fmt.Sprintf("environment-replay-environment-%03d", index)),
		}
		identity.IdentityDigest = workloadPassIdentityDigest(t, identity)
		identities[index] = identity
	}
	query, args := workloadPassEnvironmentReplayQuery(identities, 12)
	if !strings.Contains(query, "WHERE evidence.accepted_generation = ?") || strings.Contains(query, "accepted_generation IN") {
		t.Fatalf("environment replay query is not current-generation-only: %s", query)
	}
	details := sqliteQueryPlanDetails(t, database, query, args...)
	assertSQLiteQueryPlanAccess(t, details, []string{
		"SEARCH evidence USING INDEX idx_ci_workload_pass_evidence_source_replay (workload_id=? AND execution_digest=?)",
		"SEARCH proof USING INDEX idx_ci_retained_workload_pass_proofs_source_replay (workload_id=?)",
	})
	assertSQLiteQueryPlanNoFullTableScan(t, details, []string{"ci_workload_pass_evidence", "ci_retained_workload_pass_proofs"})
}

func TestWorkloadInputReplayCacheQueryPlanUsesPrimaryPartition(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 12)
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	query := `SELECT workload_id, input_digest, cache_sha256 FROM ci_workload_input_replay_cache WHERE accepted_generation = ? AND source_tree_sha = ? AND input_algorithm_digest = ? AND workload_id IN (?, ?) ORDER BY workload_id`
	details := sqliteQueryPlanDetails(t, database, query, "12", strings.Repeat("a", 40), digestForWorkloadPass("query-plan-input-algorithm"), GateIDWhitespaceCheck, GateIDProjectMapCheck)
	assertSQLiteQueryPlanAccess(t, details, []string{"SEARCH ci_workload_input_replay_cache USING PRIMARY KEY (accepted_generation=? AND source_tree_sha=? AND input_algorithm_digest=? AND workload_id=?)"})
	assertSQLiteQueryPlanNoFullTableScan(t, details, []string{"ci_workload_input_replay_cache"})
}

// TestLookupWorkloadPassEvidenceInitializesMissingAuthorityButRejectsMissingBaseline
// 验证直接 PASS lookup 在缺失路径上原子初始化 schema/index，但仍因 accepted
// baseline 缺失而 fail-fast，不生成默认 generation 或 PASS。
func TestLookupWorkloadPassEvidenceInitializesMissingAuthorityButRejectsMissingBaseline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-duration-ledger.sqlite")
	store, err := NewDurationLedgerStore(path)
	if err != nil {
		t.Fatal(err)
	}
	identity := WorkloadPassIdentity{
		WorkloadID:        GateIDBackendTestWithGuard,
		ExecutionDigest:   digestForWorkloadPass("missing-authority-execution"),
		InputDigest:       digestForWorkloadPass("missing-authority-input"),
		EnvironmentDigest: digestForWorkloadPass("missing-authority-environment"),
	}
	identity.IdentityDigest = workloadPassIdentityDigest(t, identity)
	_, err = store.LookupWorkloadPassEvidence([]WorkloadPassIdentity{identity})
	assertMissingWorkloadPassBaselineError(t, err)
	if info, err := os.Stat(path); err != nil {
		t.Fatalf("missing authority path was not initialized: %v", err)
	} else if info.IsDir() {
		t.Fatalf("missing authority path initialized as directory: %s", path)
	}
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	if err := verifyDurationLedgerSQLAuthorityBindings(database); err != nil {
		t.Fatalf("initialized authority bindings: %v", err)
	}
	indexes := sqliteIndexListForTest(t, database, "ci_workload_pass_evidence")
	for _, name := range []string{"idx_ci_workload_pass_evidence_origin_job", "idx_ci_workload_pass_evidence_retention", "idx_ci_workload_pass_evidence_source_replay"} {
		if _, ok := indexes[name]; !ok {
			t.Fatalf("initialized PASS evidence schema missing index %q", name)
		}
	}
	var baselines int
	if err := database.QueryRow(`SELECT COUNT(*) FROM ci_remote_baseline_state`).Scan(&baselines); err != nil {
		t.Fatal(err)
	}
	if baselines != 0 {
		t.Fatalf("missing-baseline lookup created %d accepted baseline rows", baselines)
	}
}

func assertMissingWorkloadPassBaselineError(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrRemoteBaselineStateNotFound) {
		t.Fatalf("LookupWorkloadPassEvidence() error = %v, want ErrRemoteBaselineStateNotFound", err)
	}
	if !strings.Contains(err.Error(), "load workload evidence accepted baseline generation") {
		t.Fatalf("LookupWorkloadPassEvidence() error = %v, want workload evidence context", err)
	}
}

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
		"SEARCH evidence USING INDEX",
		"(identity_digest=? AND accepted_generation=?)",
	})
	assertSQLiteQueryPlanNoFullTableScan(t, details, []string{"evidence"})
}

// TestWorkloadPassSourceReplayQueryPlanUsesRetainedGenerationIndex 锁定来源候选先由既有保留代索引限界；
// workload、执行和环境条件再在该代际窗口内过滤，避免为只读升级修改现有 SQLite authority shape。
func TestWorkloadPassSourceReplayQueryPlanUsesRetainedGenerationIndex(t *testing.T) {
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
		"USING INDEX idx_ci_workload_pass_evidence_retention",
		"(accepted_generation=?)",
	})
	assertSQLiteQueryPlanNoFullTableScan(t, details, []string{"ci_workload_pass_evidence"})
}

// TestWorkloadPassEnvironmentReplayQueryPlanUsesRetentionIndex 锁定 200-term
// environment replay 批量查询按既有 accepted-generation 入口限界；v15 不得
// 悄然写入新的 remote PASS 物理对象。
func TestWorkloadPassEnvironmentReplayQueryPlanUsesRetentionIndex(t *testing.T) {
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
	if !strings.Contains(query, "WHERE accepted_generation = ?") || strings.Contains(query, "accepted_generation IN") {
		t.Fatalf("environment replay query is not current-generation-only: %s", query)
	}
	details := sqliteQueryPlanDetails(t, database, query, args...)
	assertSQLiteQueryPlanAccess(t, details, []string{"USING INDEX idx_ci_workload_pass_evidence_retention"})
	assertSQLiteQueryPlanNoFullTableScan(t, details, []string{"ci_workload_pass_evidence"})
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
	for _, name := range []string{"idx_ci_workload_pass_evidence_origin_job", "idx_ci_workload_pass_evidence_retention"} {
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

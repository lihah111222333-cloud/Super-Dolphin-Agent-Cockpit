package gate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestWorkloadPassEvidenceLookupQueryPlanUsesIdentityAndRunIndexes 锁定生产 strict
// PASS JOIN 的真实查询计划：identity_digest+accepted_generation 和 ci_runs.job_id
// 必须走索引，不能退化为任一热表全表扫描。
func TestWorkloadPassEvidenceLookupQueryPlanUsesIdentityAndRunIndexes(t *testing.T) {
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
		"SEARCH runs USING INDEX",
		"(job_id=?)",
	})
	assertSQLiteQueryPlanNoFullTableScan(t, details, []string{"evidence", "runs"})
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
	if _, err := store.LookupWorkloadPassEvidence([]WorkloadPassIdentity{identity}); !errors.Is(err, ErrRemoteBaselineStateNotFound) {
		t.Fatalf("LookupWorkloadPassEvidence() error = %v, want ErrRemoteBaselineStateNotFound", err)
	}
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

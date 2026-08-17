package gate

import (
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func TestRemoteCIPlanningReceiptStoreA2CanonicalPlaceholderCAS(t *testing.T) {
	var store *DurationLedgerStore
	if _, err := store.InitializeWorkloadPlanningReceipt("job-a2", "sha256:"+strings.Repeat("1", 64), "sha256:"+strings.Repeat("2", 64), 1); err == nil {
		t.Fatal("nil store unexpectedly initialized planning receipt")
	}
	if _, err := store.TransitionWorkloadPlanningReceipt("job-a2", WorkloadPlanningReceipt{}); err == nil {
		t.Fatal("nil store unexpectedly transitioned planning receipt")
	}
	if _, err := store.LoadWorkloadPlanningReceipt("job-a2"); err == nil {
		t.Fatal("nil store unexpectedly loaded planning receipt")
	}
}

func TestRemoteCIPlanningReceiptStoreA2SQLiteCASAndCascade(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	seedPlanningReceiptRunA2(t, store, "job-a2")
	gateDigest := "sha256:" + strings.Repeat("1", 64)
	catalogDigest := "sha256:" + strings.Repeat("2", 64)
	initializePlanningReceiptA2(t, store, gateDigest, catalogDigest)
	transitionPlanningReceiptA2(t, store, gateDigest, catalogDigest)
	deletePlanningReceiptRunA2(t, store, "job-a2")
	assertPlanningReceiptCascadeA2(t, store)
}

func initializePlanningReceiptA2(t *testing.T, store *DurationLedgerStore, gateDigest, catalogDigest string) {
	t.Helper()
	placeholder, err := store.InitializeWorkloadPlanningReceipt("job-a2", gateDigest, catalogDigest, 1)
	if err != nil || placeholder.Eligibility != WorkloadPlanningReceiptEligibilityPlaceholder {
		t.Fatalf("initialize placeholder = %+v, err=%v", placeholder, err)
	}
	if _, err := store.InitializeWorkloadPlanningReceipt("job-a2", gateDigest, catalogDigest, 1); err == nil {
		t.Fatal("duplicate placeholder insert unexpectedly succeeded")
	}
}

func transitionPlanningReceiptA2(t *testing.T, store *DurationLedgerStore, gateDigest, catalogDigest string) {
	t.Helper()
	receipt := WorkloadPlanningReceipt{Status: cicontract.WorkloadPlanningReceiptStatusNotApplicable, NotApplicable: &WorkloadPlanningReceiptNotApplicable{Reason: cicontract.WorkloadPlanningReceiptReasonAllHit, GatePlanDigest: gateDigest, CatalogDigest: catalogDigest, LedgerGeneration: 1}}
	present, err := store.TransitionWorkloadPlanningReceipt("job-a2", receipt)
	if err != nil || present.Eligibility != WorkloadPlanningReceiptEligibilityPresent || present.ReceiptDigest == "" {
		t.Fatalf("transition = %+v, err=%v", present, err)
	}
	loaded, err := store.LoadWorkloadPlanningReceipt("job-a2")
	if err != nil || loaded.Receipt == nil || string(loaded.ReceiptBytes) == "" {
		t.Fatalf("load = %+v, err=%v", loaded, err)
	}
	if _, err := store.TransitionWorkloadPlanningReceipt("job-a2", receipt); err == nil {
		t.Fatal("terminal receipt was rewritten")
	}
}

func assertPlanningReceiptCascadeA2(t *testing.T, store *DurationLedgerStore) {
	t.Helper()
	missing, err := store.LoadWorkloadPlanningReceipt("job-a2")
	if err != nil || missing.Eligibility != WorkloadPlanningReceiptEligibilityMissing || missing.Receipt != nil {
		t.Fatalf("cascade load = %+v, err=%v", missing, err)
	}
}

func seedPlanningReceiptRunA2(t *testing.T, store *DurationLedgerStore, jobID string) {
	t.Helper()
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`INSERT INTO ci_runs (job_id, entrypoint, profile, plan_digest, catalog_digest, accepted_generation, image_cache_snapshot_id, source_tree_sha, runner_image, status, authoritative, started_at_unix_ms, completed_at_unix_ms, cleanup_complete) VALUES (?, 'commit', 'light', 'plan', 'catalog', '1', 'snapshot', 'tree', 'runner', 'PASS', 0, 1, 1, 1)`, jobID); err != nil {
		t.Fatal(err)
	}
}

func deletePlanningReceiptRunA2(t *testing.T, store *DurationLedgerStore, jobID string) {
	t.Helper()
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`DELETE FROM ci_runs WHERE job_id = ?`, jobID); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteCIPlanningReceiptStoreA2Schema19To21(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`DROP INDEX idx_ci_remote_run_planning_receipts_status; DROP TABLE ci_remote_run_planning_receipts; ALTER TABLE ci_workload_input_replay_cache DROP COLUMN runtime_fallback; ALTER TABLE ci_workload_input_replay_cache DROP COLUMN runtime_digest; ALTER TABLE ci_workload_input_replay_cache DROP COLUMN declaration_digest; ALTER TABLE ci_workload_input_replay_cache DROP COLUMN compile_digest; PRAGMA user_version = 19`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	database.Close()
	database, err = store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var version, tableCount, indexCount int
	if err := database.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, "ci_remote_run_planning_receipts").Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, "idx_ci_remote_run_planning_receipts_status").Scan(&indexCount); err != nil {
		t.Fatal(err)
	}
	if version != durationLedgerSQLiteSchemaVersion || tableCount != 1 || indexCount != 1 {
		t.Fatalf("schema 19->21 result version=%d table=%d index=%d", version, tableCount, indexCount)
	}
}

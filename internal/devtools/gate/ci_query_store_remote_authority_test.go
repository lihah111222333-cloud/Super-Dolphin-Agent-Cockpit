package gate

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRemoteCIRunWorkloadExecutionGoFlagsBoundAtWriteAndRead(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, _, _ := recordWorkloadPassRun(t, store, "workload-go-flags-boundary", 1, "go-flags-boundary")
	forged := record
	forged.WorkloadExecutions = append([]PlanGateExecution(nil), record.WorkloadExecutions...)
	forged.WorkloadExecutions[0].ExecutionProfile.GoFlags = CanonicalGoFlags(true)
	if err := store.RecordProvisionalRemoteCIRun(forged); err == nil || !strings.Contains(err.Error(), "GoFlags") {
		t.Fatalf("forged race workload profile write error = %v, want GoFlags mismatch", err)
	}

	profile, err := json.Marshal(forged.WorkloadExecutions[0].ExecutionProfile)
	if err != nil {
		t.Fatal(err)
	}
	database := openWorkloadPassDatabase(t, store)
	if _, err := database.Exec(`UPDATE ci_workload_executions SET execution_profile_json = ? WHERE job_id = ?`, string(profile), record.JobID); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadRemoteCIRun(record.JobID); err == nil || !strings.Contains(err.Error(), "GoFlags") {
		t.Fatalf("forged race workload profile readback error = %v, want GoFlags mismatch", err)
	}
}

// TestRecordProvisionalRemoteCIRunRejectsAuthoritativeRewriteWithoutReplacingChildren
// 验证已权威 run 不能通过 provisional 重写路径删除并重建 child projection。
func TestRecordProvisionalRemoteCIRunRejectsAuthoritativeRewriteWithoutReplacingChildren(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, _, receipts := recordWorkloadPassRun(t, store, "authoritative-rewrite", 1, "workload-authoritative-rewrite")
	record.Warnings = []string{"initial-warning"}
	if err := store.RecordProvisionalRemoteCIRun(record); err != nil {
		t.Fatalf("record provisional run with child warning: %v", err)
	}
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, true); err != nil {
		t.Fatalf("finalize authoritative run: %v", err)
	}

	rewrite := record
	rewrite.Warnings = []string{"rewritten-warning"}
	if err := store.RecordProvisionalRemoteCIRun(rewrite); err == nil || !strings.Contains(err.Error(), "already authoritative") {
		t.Fatalf("provisional authoritative rewrite error = %v", err)
	}

	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	var authoritative int
	if err := database.QueryRow(`SELECT authoritative FROM ci_runs WHERE job_id = ?`, record.JobID).Scan(&authoritative); err != nil {
		t.Fatal(err)
	}
	if authoritative != 1 {
		t.Fatalf("authoritative flag = %d, want 1", authoritative)
	}
	var warningCount int
	var warning string
	if err := database.QueryRow(`SELECT COUNT(*), COALESCE(MAX(warning_text), '') FROM ci_run_warnings WHERE job_id = ?`, record.JobID).Scan(&warningCount, &warning); err != nil {
		t.Fatal(err)
	}
	if warningCount != 1 || warning != "initial-warning" {
		t.Fatalf("child warning projection = (%d, %q), want (1, %q)", warningCount, warning, "initial-warning")
	}
}

package gate

import (
	"strings"
	"testing"
)

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

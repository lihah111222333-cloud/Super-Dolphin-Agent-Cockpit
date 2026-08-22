package archtest

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestHeterociPostgreSQLReceiptEventAuthorityGuard(t *testing.T) {
	contents, joined := loadPostgreSQLReceiptGuardContents(t)
	assertPostgreSQLReceiptGuardMarkers(t, joined)
	assertPostgreSQLReceiptGuardForbidden(t, contents)
	assertPostgreSQLReceiptGuardStages(t, joined)
}

func loadPostgreSQLReceiptGuardContents(t *testing.T) (map[string]string, string) {
	t.Helper()
	root := findRepoRoot(t)
	files := []string{"internal/devtools/heterociexec/postgresql_terminal_receipt.go", "internal/devtools/heterociexec/postgresql_terminal_receipt_schema.go", "internal/devtools/heterociexec/postgresql_terminal_receipt_store.go", "internal/devtools/heterociexec/postgresql_terminal_receipt_rows.go", "internal/devtools/heterociexec/receipt_common.go", "internal/devtools/heterociexec/postgresql_receipt_schema.go"}
	contents := make(map[string]string, len(files))
	for _, relative := range files {
		contents[relative] = readRemoteCIContractGuardFile(t, filepath.Join(root, relative))
	}
	return contents, strings.Join(filesContent(contents), "\n")
}

func assertPostgreSQLReceiptGuardMarkers(t *testing.T, joined string) {
	t.Helper()
	for _, marker := range []string{"operation_id", "manifest_digest", "plan_digest", "node_id", "executor_kind", "authority_generation", "started_at", "finished_at", "exit_code", "cleanup_state", "error_code", "capacity_code", "event_seq", "event_digest", "receipt_events", "terminal_receipts", "remote-ci-result/v1", "remote-ci-event/v1", "cleanup_state = 'verified'", "PostgreSQLReceiptLifecycleStages", "ensurePostgreSQLReceiptOperationOpen", "skipped", "sql.LevelSerializable", "pg_advisory_xact_lock", "synchronous_commit", "remote_apply", "ON CONFLICT"} {
		if !strings.Contains(joined, marker) {
			t.Errorf("PostgreSQL receipt/event implementation is missing marker %q", marker)
		}
	}
}

func assertPostgreSQLReceiptGuardForbidden(t *testing.T, contents map[string]string) {
	t.Helper()
	for relative, content := range contents {
		for _, forbidden := range []string{"sqliteReceipt", "SQLiteReceipt", "heteroci_owner", "password", "credential", "raw_command", "GRANT SELECT, INSERT, UPDATE ON TABLE heteroci_receipt.terminal_receipts", "GRANT SELECT, INSERT, UPDATE ON TABLE heteroci_receipt.receipt_events"} {
			if strings.Contains(content, forbidden) {
				t.Errorf("%s contains forbidden PostgreSQL receipt marker %q", relative, forbidden)
			}
		}
	}
}

func assertPostgreSQLReceiptGuardStages(t *testing.T, joined string) {
	t.Helper()
	for _, stage := range []string{"request_validation", "manifest_binding", "capacity_snapshot", "reservation", "materialize", "execute", "cleanup", "terminal"} {
		if !strings.Contains(joined, stage) {
			t.Errorf("PostgreSQL receipt/event stage %q is not frozen", stage)
		}
	}
}

func filesContent(contents map[string]string) []string {
	values := make([]string, 0, len(contents))
	for _, content := range contents {
		values = append(values, content)
	}
	sort.Strings(values)
	return values
}

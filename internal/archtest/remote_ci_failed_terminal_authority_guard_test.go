package archtest

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestRemoteCIFailedTerminalAuthorityGuard freezes failed execution as a
// PostgreSQL-backed terminal lifecycle rather than a local CLI-only error.
func TestRemoteCIFailedTerminalAuthorityGuard(t *testing.T) {
	root := findRepoRoot(t)
	authority := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/remotecicli/postgresql_authority.go"))
	events := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/remotecicli/validation_event.go"))
	workflow := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/heterociworkflow/run.go"))
	binding := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/heterociworkflow/executor_binding.go"))
	for _, marker := range []string{
		"finishFailed", "PostgreSQLReceiptStatusFailed", "persistTerminalProjection", "publishExecuteTerminal", "classifyExecuteFailure",
		"executeFailureCodeTimeout", "executeFailureCodeReceipt", "failedExecuteTerminalShards", "ErrCleanupIncomplete",
	} {
		if !strings.Contains(authority+events, marker) {
			t.Errorf("failed terminal authority path is missing marker %q", marker)
		}
	}
	if observe, bind := strings.Index(binding, "workflow.observePreparedRun"), strings.Index(binding, "workflow.bindExecutors"); observe < 0 || bind < 0 || observe > bind {
		t.Fatalf("prepared lifecycle observation must precede runtime executor binding: observe=%d bind=%d", observe, bind)
	}
	for _, marker := range []string{"ErrBatchReceiptRequired", "cleanupStarted", "LeaseStateFailed"} {
		if !strings.Contains(workflow, marker) {
			t.Errorf("workflow failure cleanup is missing marker %q", marker)
		}
	}
	for _, forbidden := range []string{"sqlite", "SQLite", "retry executor", "fallback executor"} {
		if strings.Contains(authority, forbidden) {
			t.Errorf("PostgreSQL failure authority contains forbidden fallback marker %q", forbidden)
		}
	}
}

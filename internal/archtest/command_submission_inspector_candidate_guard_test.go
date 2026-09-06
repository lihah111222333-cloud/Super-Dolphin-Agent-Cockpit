package archtest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandSubmissionInspectorCandidateStaysReadOnlyAndUnregistered(t *testing.T) {
	path := filepath.Join(repoRoot(t), "cmd", "mcp-orch", "tools", "unifiedv2", "command_submission_inspector_candidate.go")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	for _, forbidden := range []string{"SubmissionProvider", "Provider", "NewRegistry", "Register", "fx.Provide", "Room", "os/exec", "net/http"} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("inspector candidate contains forbidden production seam %q", forbidden)
		}
	}
	for _, required := range []string{"GetTx", "GetIntentTx", "RequireTransactionDBTX", "ExecutionTerminal", "accept_resolution_pending", "accept_resolution_unknown"} {
		if !strings.Contains(source, required) {
			t.Fatalf("inspector candidate is missing required guard %q", required)
		}
	}
}

package archtest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandSubmissionWaitCandidateDocumentsProjectionGap(t *testing.T) {
	root := repoRoot(t)
	candidatePath := filepath.Join(root, "cmd", "mcp-orch", "tools", "unifiedv2", "command_submission_wait_candidate.go")
	source, err := os.ReadFile(candidatePath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	assertCandidateSurface(t, text)
	assertDurableFiles(t, root)
	t.Log("candidate wait remains disabled; durable submission projection is complete but unregistered")
}

func assertDurableFiles(t *testing.T, root string) {
	t.Helper()
	durablePath := filepath.Join(root, "cmd", "mcp-orch", "store", "orchestrationcommand", "durable_submission.go")
	durable, err := os.ReadFile(durablePath)
	if err != nil {
		t.Fatal(err)
	}
	durableText := string(durable)
	assertDurableProjection(t, durableText)
	intentPath := filepath.Join(root, "cmd", "mcp-orch", "store", "orchestrationcommand", "store.go")
	intent, err := os.ReadFile(intentPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(intent), "GetTx(") {
		t.Fatal("command intent owner must expose transaction-bound reads")
	}
}

func assertCandidateSurface(t *testing.T, text string) {
	t.Helper()
	for _, forbidden := range []string{"NewRegistry", "Register", "SubmissionProvider", "Submit(context.Context", "fx.Provide"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("command_submission wait candidate contains forbidden production/provider seam %q", forbidden)
		}
	}
	if !strings.Contains(text, "ErrCommandSubmissionWaitDisabled") || !strings.Contains(text, "return nil, ErrCommandSubmissionWaitDisabled") {
		t.Fatal("candidate must remain explicitly disabled until the projection contract is complete")
	}
	if !strings.Contains(text, "read-only transaction projection") || !strings.Contains(text, "GetSubmissionTx") || !strings.Contains(text, "GetIntentTx") {
		t.Fatal("candidate must document the missing read-only transaction projection")
	}
}

func assertDurableProjection(t *testing.T, text string) {
	t.Helper()
	for _, required := range []string{"CallerPrincipalRef", "ScopeRef", "SourceFenceRef", "TargetFenceRef", "CommandFence", "ResolutionKind"} {
		if !strings.Contains(text, required) {
			t.Fatalf("durable submission projection is missing field %q", required)
		}
	}
	if !strings.Contains(text, "GetTx(") || !strings.Contains(text, "GetIntentTx(") {
		t.Fatal("durable submission projection must expose transaction-bound reads")
	}
}

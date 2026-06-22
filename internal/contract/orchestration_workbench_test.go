package contract

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRunWorkbenchSummaryJSONUsesToolFieldNames(t *testing.T) {
	raw, err := json.Marshal(Run{
		RunKey:        "run-1",
		Status:        "running",
		DerivedState:  "waiting_for_assignee",
		BlockedReason: "root node missing assigned_to",
		NextAction:    "dispatch_node",
		ArtifactCount: 2,
		RecoveryActions: []WorkflowRecoveryAction{{
			Action:  "cancel_with_cleanup",
			Label:   "取消并清理",
			Enabled: true,
			Policy:  "allowlist",
		}},
	})
	if err != nil {
		t.Fatalf("Marshal(Run) error = %v", err)
	}
	text := string(raw)
	for _, want := range []string{
		`"derived_state":"waiting_for_assignee"`,
		`"blocked_reason":"root node missing assigned_to"`,
		`"next_action":"dispatch_node"`,
		`"artifact_count":2`,
		`"recovery_actions":[`,
		`"action":"cancel_with_cleanup"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("Run JSON = %s, want substring %s", text, want)
		}
	}
}

func TestDAGNodeWorkbenchDiagnosticsJSONUsesToolFieldNames(t *testing.T) {
	raw, err := json.Marshal(DAGNode{
		NodeKey:      "draft",
		Status:       "failed",
		Executor:     "codex-runner",
		FailureClass: "transient",
		NextAction:   "retry_failed_node",
		ArtifactLinks: []WorkflowArtifactLink{{
			Kind:    "sharedfile",
			Label:   "草稿",
			Path:    "reports/draft.md",
			NodeKey: "draft",
		}},
	})
	if err != nil {
		t.Fatalf("Marshal(DAGNode) error = %v", err)
	}
	text := string(raw)
	for _, want := range []string{
		`"executor":"codex-runner"`,
		`"failure_class":"transient"`,
		`"next_action":"retry_failed_node"`,
		`"artifact_links":[`,
		`"path":"reports/draft.md"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("DAGNode JSON = %s, want substring %s", text, want)
		}
	}
}

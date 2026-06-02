package toolfilter

import (
	"slices"
	"testing"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

func assertAllow(t *testing.T, got mcp.BeforeDecision) {
	t.Helper()
	if got.Decision != mcp.HookDecisionAllow {
		t.Fatalf("decision = %q, want %q", got.Decision, mcp.HookDecisionAllow)
	}
}

func TestReviewerPreset_AllowsReadOnlyTools(t *testing.T) {
	got := ReviewerDecision()
	assertAllow(t, got)
	want := []string{"file", "grep", "inspect", "xref", "structure", "completion", "lsp_file", "lsp_grep", "lsp_inspect", "lsp_xref", "lsp_structure", "lsp_completion", "shared_file_read"}
	if !slices.Equal(got.AllowedTools, want) {
		t.Fatalf("allowed = %#v, want %#v", got.AllowedTools, want)
	}
}

func TestReviewerPreset_DeniesWriteTools(t *testing.T) {
	got := ReviewerDecision()
	assertAllow(t, got)
	want := []string{"edit", "lsp_edit", "orchestration_launch_agent", "orchestration_stop_agent"}
	if !slices.Equal(got.DeniedTools, want) {
		t.Fatalf("denied = %#v, want %#v", got.DeniedTools, want)
	}
}

func TestReviewerPreset_ExcludesSharedFileWrite(t *testing.T) {
	got := ReviewerDecision()
	assertAllow(t, got)
	if slices.Contains(got.AllowedTools, "shared_file_write") {
		t.Fatalf("allowed unexpectedly contains shared_file_write: %#v", got.AllowedTools)
	}
}

func TestWorkerPreset_DeniesOrchestration(t *testing.T) {
	got := WorkerDecision()
	assertAllow(t, got)
	want := []string{"orchestration_launch_agent", "orchestration_send_message", "orchestration_stop_agent", "orchestration_list_agents", "orchestration_get_agent_report"}
	if !slices.Equal(got.DeniedTools, want) {
		t.Fatalf("denied = %#v, want %#v", got.DeniedTools, want)
	}
}

func TestWorkerPreset_NoAllowListRestriction(t *testing.T) {
	got := WorkerDecision()
	assertAllow(t, got)
	if got.AllowedTools != nil {
		t.Fatalf("allowed = %#v, want nil", got.AllowedTools)
	}
}

func TestFullAccessPreset_NoRestrictions(t *testing.T) {
	got := FullAccessDecision()
	assertAllow(t, got)
	if got.AllowedTools != nil || got.DeniedTools != nil {
		t.Fatalf("preset = %#v, want no restrictions", got)
	}
}

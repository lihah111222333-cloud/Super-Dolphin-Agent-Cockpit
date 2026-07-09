package toolfilter

import (
	"slices"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/toolpolicy"
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
	want := contract.ReadOnlyAgentDeniedTools()
	if !slices.Equal(got.DeniedTools, want) {
		t.Fatalf("denied = %#v, want %#v", got.DeniedTools, want)
	}
}

func TestReviewerPreset_DeniesExactUnsafeDelegationSurfaces(t *testing.T) {
	got := ReviewerDecision()
	assertAllow(t, got)

	deniedNames := append([]string{
		"shared_file_write",
		"lsp_edit",
		"memory_write",
		"task_create_dag",
		"task_dag_apply_ops",
		"task_update_node",
		"task_dispatch_node",
		"task_start_dag",
		"task_terminate_dag",
		"task_delete_dag",
		"task_workflow_recovery_action",
		"workspace_create_run",
		"workspace_merge_run",
		"workspace_abort_run",
		"workflow_template_save",
		"workflow_template_rollback",
		"update_plan",
		"wait",
		"bash_output",
		"BashOutput",
		"todo_write",
		"TodoWrite",
		"complete_step",
		"multi_agent",
		"multi_tool_use.parallel",
		"spawn_agent",
		"send_input",
		"resume_agent",
		"wait_agent",
		"close_agent",
		"connect_tool_source",
	}, contract.OrchestrationToolDenylist()...)
	for _, name := range deniedNames {
		if slices.Contains(got.AllowedTools, name) {
			t.Fatalf("allowed unexpectedly contains %s: %#v", name, got.AllowedTools)
		}
		if !toolDenied(got, name) {
			t.Fatalf("denied missing %s: %#v", name, got.DeniedTools)
		}
	}
}

func TestReviewerPreset_DeniesOrchestrationTools(t *testing.T) {
	got := ReviewerDecision()
	assertAllow(t, got)

	for _, name := range orchestrationDenylistForTest() {
		if !toolDenied(got, name) {
			t.Fatalf("reviewer denied missing orchestration tool %q: %#v", name, got.DeniedTools)
		}
	}
}

func TestReviewerPreset_DoesNotUseDeniedToolPrefixes(t *testing.T) {
	got := ReviewerDecision()
	assertAllow(t, got)

	for _, denied := range got.DeniedTools {
		if slices.Contains([]string{"task_", "workspace_", "workflow_template_"}, denied) {
			t.Fatalf("denied uses prefix sentinel %q; DeniedTools are exact names: %#v", denied, got.DeniedTools)
		}
	}
}

func TestReviewerPreset_AllowsOnlyInternallyTrustedReadOnlyTools(t *testing.T) {
	got := ReviewerDecision()
	assertAllow(t, got)

	readOnlyDecision := reviewerToolPolicyDecision(trustedReadOnlyTool("file"))
	if !readOnlyDecision.Allow {
		t.Fatalf("trusted read-only decision = %#v, want allow", readOnlyDecision)
	}

	externalSourceDecision := reviewerToolPolicyDecision(reviewerToolCandidate{
		name:              "external_claimed_read",
		trust:             toolpolicy.TrustExternal,
		capabilities:      toolpolicy.CapabilityReadOnly,
		readOnlyHint:      true,
		readOnlyHintTrust: toolpolicy.TrustExternal,
	})
	if externalSourceDecision.Allow || externalSourceDecision.Code != toolpolicy.CodeUntrustedSource {
		t.Fatalf("external source decision = %#v, want untrusted-source deny", externalSourceDecision)
	}

	externalHintDecision := reviewerToolPolicyDecision(reviewerToolCandidate{
		name:              "provider_claimed_external_hint",
		trust:             toolpolicy.TrustProvider,
		capabilities:      toolpolicy.CapabilityReadOnly,
		readOnlyHint:      true,
		readOnlyHintTrust: toolpolicy.TrustExternal,
	})
	if externalHintDecision.Allow || externalHintDecision.Code != toolpolicy.CodeExternalHint {
		t.Fatalf("external read-only hint decision = %#v, want external-hint deny", externalHintDecision)
	}

	for _, name := range got.AllowedTools {
		if !reviewerToolPolicyDecision(trustedReadOnlyTool(name)).Allow {
			t.Fatalf("allowed tool %s is not backed by trusted read-only policy", name)
		}
	}
}

func toolDenied(decision mcp.BeforeDecision, name string) bool {
	return slices.Contains(decision.DeniedTools, name)
}

func TestWorkerPreset_DeniesOrchestration(t *testing.T) {
	got := WorkerDecision()
	assertAllow(t, got)
	want := orchestrationDenylistForTest()
	if !slices.Equal(got.DeniedTools, want) {
		t.Fatalf("denied = %#v, want %#v", got.DeniedTools, want)
	}
}

func TestWorkerPreset_DeniesOrchestrationTools(t *testing.T) {
	got := WorkerDecision()
	assertAllow(t, got)

	for _, name := range orchestrationDenylistForTest() {
		if !toolDenied(got, name) {
			t.Fatalf("worker denied missing orchestration tool %q: %#v", name, got.DeniedTools)
		}
	}
}

func TestWorkerPreset_OrchestrationDenylistMatchesReviewer(t *testing.T) {
	worker := WorkerDecision()
	reviewer := ReviewerDecision()
	assertAllow(t, worker)
	assertAllow(t, reviewer)

	for _, name := range orchestrationDenylistForTest() {
		if toolDenied(reviewer, name) != toolDenied(worker, name) {
			t.Fatalf("orchestration tool %q reviewer denied=%v worker denied=%v", name, toolDenied(reviewer, name), toolDenied(worker, name))
		}
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

func orchestrationDenylistForTest() []string {
	return contract.OrchestrationToolDenylist()
}

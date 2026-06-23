package toolbridge

import "testing"

func TestInjectManagedLaunchArgsAddsCodexModelProvider(t *testing.T) {
	args := map[string]any{}

	changed := injectManagedLaunchArgs(args, toolCallBinding{
		AgentID:            "agent-parent",
		CodexModelProvider: " openai ",
	}, "provider-thread-parent", "codex", "gpt-5.5", "xhigh")

	if !changed {
		t.Fatal("injectManagedLaunchArgs changed = false, want true")
	}
	if got := mapString(args, "codex_model_provider"); got != "openai" {
		t.Fatalf("codex_model_provider = %q, want openai; args=%#v", got, args)
	}
	if got := mapString(args, "parent_thread_id"); got != "provider-thread-parent" {
		t.Fatalf("parent_thread_id = %q, want provider-thread-parent; args=%#v", got, args)
	}
}

func TestInjectManagedLaunchArgsDoesNotOverwriteCodexModelProvider(t *testing.T) {
	args := map[string]any{"codex_model_provider": "custom-provider"}

	injectManagedLaunchArgs(args, toolCallBinding{
		AgentID:            "agent-parent",
		CodexModelProvider: "openai",
	}, "provider-thread-parent", "codex", "gpt-5.5", "xhigh")

	if got := mapString(args, "codex_model_provider"); got != "custom-provider" {
		t.Fatalf("codex_model_provider = %q, want custom-provider", got)
	}
}

package toolbridge

import "testing"

func TestInjectManagedLaunchArgsAddsCodexIdentity(t *testing.T) {
	args := map[string]any{}

	changed := injectManagedLaunchArgs(args, toolCallBinding{
		AgentID:            "agent-parent",
		CodexHome:          " /Users/test/.codex ",
		CodexInstanceKey:   " default ",
		CodexModelProvider: " openai ",
	}, "provider-thread-parent", "codex", "gpt-5.5", "xhigh")

	if !changed {
		t.Fatal("injectManagedLaunchArgs changed = false, want true")
	}
	for key, want := range map[string]string{
		"codex_home":           "/Users/test/.codex",
		"codex_instance_key":   "default",
		"codex_model_provider": "openai",
	} {
		if got := mapString(args, key); got != want {
			t.Fatalf("%s = %q, want %q; args=%#v", key, got, want, args)
		}
	}
	if got := mapString(args, "parent_thread_id"); got != "provider-thread-parent" {
		t.Fatalf("parent_thread_id = %q, want provider-thread-parent; args=%#v", got, args)
	}
}

func TestInjectManagedLaunchArgsDoesNotOverwriteCodexIdentity(t *testing.T) {
	args := map[string]any{
		"codex_home":           "/custom/.codex",
		"codex_instance_key":   "custom-key",
		"codex_model_provider": "custom-provider",
	}

	injectManagedLaunchArgs(args, toolCallBinding{
		AgentID:            "agent-parent",
		CodexHome:          "/Users/test/.codex",
		CodexInstanceKey:   "default",
		CodexModelProvider: "openai",
	}, "provider-thread-parent", "codex", "gpt-5.5", "xhigh")

	for key, want := range map[string]string{
		"codex_home":           "/custom/.codex",
		"codex_instance_key":   "custom-key",
		"codex_model_provider": "custom-provider",
	} {
		if got := mapString(args, key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

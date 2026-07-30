package toolbridge

import (
	"context"
	"strings"
	"testing"
)

func TestInjectManagedLaunchArgsAddsCodexIdentity(t *testing.T) {
	args := map[string]any{}

	changed := injectManagedLaunchArgs(args, toolCallBinding{
		AgentID:            "agent-parent",
		CodexHome:          " /Users/test/.codex ",
		CodexInstanceKey:   " default ",
		CodexModelProvider: " openai ",
	}, "codex", "gpt-5.5", "xhigh")

	if !changed {
		t.Fatal("injectManagedLaunchArgs changed = false, want true")
	}
	for key, want := range map[string]string{
		"parent_id":            "agent-parent",
		"codex_home":           "/Users/test/.codex",
		"codex_instance_key":   "default",
		"codex_model_provider": "openai",
	} {
		if got := mapString(args, key); got != want {
			t.Fatalf("%s = %q, want %q; args=%#v", key, got, want, args)
		}
	}
	if _, ok := args["parent_thread_id"]; ok {
		t.Fatalf("parent_thread_id present in launch args: %#v", args)
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
	}, "codex", "gpt-5.5", "xhigh")

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

func TestManagedLaunchRequiresRegisteredParentProviderThread(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		binding toolCallBinding
		want    string
	}{
		{name: "parent missing", want: "parent agent binding is required"},
		{name: "provider thread missing", binding: toolCallBinding{AgentID: "agent-parent", Provider: "codex"}, want: "provider_thread_id is required"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bindings := map[string]toolCallBinding{}
			if tc.binding.AgentID != "" {
				bindings[tc.binding.AgentID] = tc.binding
			}
			h := &Handler{bindingStore: &toolCallBindingStoreStub{bindingsByAgent: bindings}}
			_, err := h.injectManagedLaunchContext(context.Background(), ToolCallRequest{Name: "launch_agent", AgentID: "agent-parent"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("injectManagedLaunchContext() error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestManagedLaunchInjectsParentAfterProviderThreadIsBound(t *testing.T) {
	t.Parallel()

	h := &Handler{bindingStore: &toolCallBindingStoreStub{bindingsByAgent: map[string]toolCallBinding{
		"agent-parent": {AgentID: "agent-parent", Provider: "codex", ProviderThreadID: "019fa80e-3ddc-7d51-87a2-90a76e2f5c74"},
	}}}
	req, err := h.injectManagedLaunchContext(context.Background(), ToolCallRequest{Name: "launch_agent", AgentID: "agent-parent", Arguments: []byte(`{}`)})
	if err != nil {
		t.Fatalf("injectManagedLaunchContext() error = %v", err)
	}
	if got := mapString(decodeToolArguments(req.Arguments), "parent_id"); got != "agent-parent" {
		t.Fatalf("parent_id = %q, want agent-parent; args=%s", got, req.Arguments)
	}
}

func TestManagedLaunchInjectsSameRegisteredParentForThreeChildren(t *testing.T) {
	t.Parallel()

	h := &Handler{bindingStore: &toolCallBindingStoreStub{bindingsByAgent: map[string]toolCallBinding{
		"agent-parent": {AgentID: "agent-parent", Provider: "codex", ProviderThreadID: "019fa80e-3ddc-7d51-87a2-90a76e2f5c74"},
	}}}
	for _, childName := range []string{"Hubble", "Lagrange", "Nietzsche"} {
		req, err := h.injectManagedLaunchContext(context.Background(), ToolCallRequest{
			Name:      "launch_agent",
			AgentID:   "agent-parent",
			Arguments: []byte(`{"name":"` + childName + `"}`),
		})
		if err != nil {
			t.Fatalf("injectManagedLaunchContext(%s) error = %v", childName, err)
		}
		args := decodeToolArguments(req.Arguments)
		if got := mapString(args, "parent_id"); got != "agent-parent" {
			t.Fatalf("%s parent_id = %q, want agent-parent; args=%s", childName, got, req.Arguments)
		}
		if got := mapString(args, "name"); got != childName {
			t.Fatalf("child name = %q, want %q; args=%s", got, childName, req.Arguments)
		}
	}
}

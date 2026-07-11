package toolbridge

import (
	"context"
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
)

func TestToolBridgeTaskCreateDAGInjectsCodexIdentityIntoAgentNodes(t *testing.T) {
	args := taskCreateDAGArgs(t, false)
	wantArgs := taskCreateDAGArgs(t, true)
	h, registry := newHandlerForTest(newToolCallPeer(t, "task_create_dag", wantArgs, "created", nil))
	h.bindingStore = taskCreateDAGCodexBinding()

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "task_create_dag",
		Arguments: args,
		ThreadID:  "provider-thread-parent",
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	assertSingleTextItem(t, got, "created", true)
	if len(registry.gotKinds) != 1 || registry.gotKinds[0] != dto.ClientKindOrch {
		t.Fatalf("FindActiveByKind() kinds = %#v, want [%q]", registry.gotKinds, dto.ClientKindOrch)
	}
}

func taskCreateDAGCodexBinding() *toolCallBindingStoreStub {
	return &toolCallBindingStoreStub{bindingsByProvider: map[string]toolCallBinding{
		"codex:provider-thread-parent": {
			AgentID:            "agent-parent",
			Provider:           "codex",
			ProviderThreadID:   "provider-thread-parent",
			CWD:                "/repo/project",
			CodexHome:          "/Users/test/.codex",
			CodexInstanceKey:   "default",
			CodexModelProvider: "openai",
		},
	}}
}

func taskCreateDAGArgs(t *testing.T, injected bool) []byte {
	t.Helper()
	return mustRawJSON(t, map[string]any{
		"agent_id": "agent-parent",
		"dag_key":  "dag-essay",
		"title":    "Essay DAG",
		"nodes": []any{
			taskCreateDAGAgentNode(injected),
			taskCreateDAGAutomationNode(),
		},
	})
}

func taskCreateDAGAgentNode(injected bool) map[string]any {
	exec := map[string]any{
		"prompt_key": "main/code-task",
		"cwd":        "/repo/project",
	}
	if injected {
		exec["provider"] = "codex"
		exec["codex_home"] = "/Users/test/.codex"
		exec["codex_instance_key"] = "default"
		exec["codex_model_provider"] = "openai"
	}
	return map[string]any{
		"node_key":    "essay_01",
		"title":       "Essay 01",
		"node_type":   "agent",
		"assigned_to": "agent-parent",
		"config":      map[string]any{"exec": exec},
	}
}

func taskCreateDAGAutomationNode() map[string]any {
	return map[string]any{
		"node_key":  "notify",
		"title":     "Notify",
		"node_type": "automation",
		"config": map[string]any{
			"exec": map[string]any{
				"kind":        "command_card",
				"command_ref": "notify",
			},
		},
	}
}

package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/testutil/golden"
)

func TestHandleCreateDAGRejectsAgentNodeMissingProvider(t *testing.T) {
	err := handleCreateDAGRejects(t, `{
		"agent_id":"designer-1",
		"dag_key":"dag-missing-provider",
		"title":"Missing provider",
		"nodes":[{
			"node_key":"writer",
			"title":"Writer",
			"node_type":"agent",
			"assigned_to":"agent-parent",
			"config":{"exec":{"prompt_key":"main/code-task","cwd":"/repo/project"}}
		}]
	}`)
	for _, want := range []string{"nodes[0].config.exec.provider", "writer", "claude", "codex"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("HandleCreateDAG() error = %q, want substring %q", err.Error(), want)
		}
	}
}

func TestHandleCreateDAGRejectsCodexAgentNodeMissingIdentity(t *testing.T) {
	err := handleCreateDAGRejects(t, `{
		"agent_id":"designer-1",
		"dag_key":"dag-missing-codex-identity",
		"title":"Missing codex identity",
		"nodes":[{
			"node_key":"writer",
			"title":"Writer",
			"node_type":"agent",
			"assigned_to":"agent-parent",
			"config":{"exec":{"provider":"codex","prompt_key":"main/code-task","cwd":"/repo/project"}}
		}]
	}`)
	for _, want := range []string{"writer", "codex_home", "codex_instance_key", "codex_model_provider"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("HandleCreateDAG() error = %q, want substring %q", err.Error(), want)
		}
	}
}

func handleCreateDAGRejects(t *testing.T, raw string) error {
	t.Helper()
	handler := HandleCreateDAG(&golden.OrchestrationStub{
		CreateDAGFunc: func(context.Context, contract.CreateDAGRequest) (contract.DAGDetail, error) {
			t.Fatal("CreateDAG should not be called for invalid agent launch config")
			return contract.DAGDetail{}, nil
		},
	})
	_, err := handler(context.Background(), json.RawMessage(raw))
	if err == nil {
		t.Fatal("HandleCreateDAG() error = nil, want validation failure")
	}
	return err
}

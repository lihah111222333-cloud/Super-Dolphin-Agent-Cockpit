package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpcommon "github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"github.com/anthropic-ai/super-agent-v3/internal/testutil/golden"
)

func TestCreateDAGNodesFromInputRejectsBlankRequiredFields(t *testing.T) {
	tests := []struct {
		name  string
		nodes []CreateDAGNodeInput
		want  string
	}{
		{
			name:  "blank node key",
			nodes: []CreateDAGNodeInput{{NodeKey: " ", Title: "Build"}},
			want:  "nodes[0].node_key is required",
		},
		{
			name: "blank title",
			nodes: []CreateDAGNodeInput{
				{NodeKey: "build", Title: "ok"},
				{NodeKey: "test", Title: " "},
			},
			want: "nodes[1].title is required",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := createDAGNodesFromInput(tc.nodes)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("createDAGNodesFromInput() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestCreateDAGRequestFromInputRequiresAgentID(t *testing.T) {
	_, err := createDAGRequestFromInput(CreateDAGInput{
		DagKey:   "dag-1",
		Title:    "Build",
		Schedule: DAGScheduleInput{Trigger: "manual"},
	}, "")
	if err == nil || err.Error() != "agent_id is required" {
		t.Fatalf("createDAGRequestFromInput() error = %v", err)
	}
}

func TestCreateDAGRequestFromInputMapsCreatedBy(t *testing.T) {
	req, err := createDAGRequestFromInput(CreateDAGInput{
		AgentID:  " agent-42 ",
		DagKey:   "dag-1",
		Title:    "Build",
		Schedule: DAGScheduleInput{Trigger: "manual"},
	}, "")
	if err != nil {
		t.Fatalf("createDAGRequestFromInput() error = %v", err)
	}
	if req.CreatedBy != "agent-42" {
		t.Fatalf("CreatedBy = %q, want agent-42", req.CreatedBy)
	}
}

func TestHandleCreateDAGUsesTrustedScopeAgentID(t *testing.T) {
	var got contract.CreateDAGRequest
	handler := HandleCreateDAG(&golden.OrchestrationStub{
		CreateDAGFunc: func(_ context.Context, req contract.CreateDAGRequest) (contract.DAGDetail, error) {
			got = req
			return contract.DAGDetail{}, nil
		},
	})
	ctx := mcpcommon.WithToolScope(context.Background(), mcpcommon.ToolScope{AgentID: " trusted-agent "})

	_, err := handler(ctx, json.RawMessage(`{
		"dag_key":"dag-scope",
		"title":"Scoped DAG",
		"schedule":{"trigger":"manual"}
	}`))
	if err != nil {
		t.Fatalf("HandleCreateDAG() error = %v", err)
	}
	if got.CreatedBy != "trusted-agent" {
		t.Fatalf("CreatedBy = %q, want trusted-agent", got.CreatedBy)
	}
}

func TestHandleCreateDAGRejectsAgentIDScopeMismatch(t *testing.T) {
	handler := HandleCreateDAG(&golden.OrchestrationStub{
		CreateDAGFunc: func(context.Context, contract.CreateDAGRequest) (contract.DAGDetail, error) {
			t.Fatal("CreateDAG should not be called when public agent_id conflicts with trusted scope")
			return contract.DAGDetail{}, nil
		},
	})
	ctx := mcpcommon.WithToolScope(context.Background(), mcpcommon.ToolScope{AgentID: "trusted-agent"})

	_, err := handler(ctx, json.RawMessage(`{
		"agent_id":"model-guessed-agent",
		"dag_key":"dag-scope",
		"title":"Scoped DAG",
		"schedule":{"trigger":"manual"}
	}`))
	if err == nil {
		t.Fatal("HandleCreateDAG() error = nil, want scope mismatch error")
	}
	env := mcpcommon.NewToolErrorEnvelope("task_create_dag", err)
	if env.Code != "invalid_input" {
		t.Fatalf("tool error code = %q, want invalid_input", env.Code)
	}
}

func TestHandleCreateDAGRejectsScheduledTriggerAtCreate(t *testing.T) {
	err := handleCreateDAGRejects(t, `{
		"agent_id":"designer-1",
		"dag_key":"dag-scheduled",
		"title":"Scheduled DAG",
		"schedule":{"trigger":"scheduled"}
	}`)
	env := mcpcommon.NewToolErrorEnvelope("task_create_dag", err)
	if env.Code != "invalid_input" {
		t.Fatalf("tool error code = %q, want invalid_input", env.Code)
	}
}

func TestHandleCreateDAGRejectsRunnableRootAgentWithoutAssignee(t *testing.T) {
	err := handleCreateDAGRejects(t, `{
		"agent_id":"designer-1",
		"dag_key":"dag-unassigned-root",
		"title":"Unassigned root",
		"schedule":{"trigger":"manual"},
		"nodes":[{
			"node_key":"writer",
			"title":"Writer",
			"node_type":"agent",
			"config":{"exec":{"provider":"codex","prompt_key":"main/code-task"}}
		}]
	}`)
	for _, want := range []string{"nodes[0].assigned_to", "writer", "root agent node", "task_start_dag"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("HandleCreateDAG() error = %q, want substring %q", err.Error(), want)
		}
	}
}

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

func TestHandleCreateDAGRejectsAgentNodeMissingLaunchIdentity(t *testing.T) {
	tests := []struct {
		name     string
		nodeType string
	}{
		{name: "implicit agent node", nodeType: ""},
		{name: "explicit agent node", nodeType: `"agent"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			nodeTypeLine := ""
			if tc.nodeType != "" {
				nodeTypeLine = `"node_type":` + tc.nodeType + `,`
			}
			err := handleCreateDAGRejects(t, `{
				"agent_id":"designer-1",
				"dag_key":"dag-missing-launch-identity",
				"title":"Missing launch identity",
				"nodes":[{
					"node_key":"writer",
					"title":"Writer",
					`+nodeTypeLine+`
					"assigned_to":"agent-parent",
					"config":{"exec":{
						"provider":"codex",
						"cwd":"/repo/project",
						"codex_home":"/tmp/codex-home",
						"codex_instance_key":"default",
						"codex_model_provider":"openai"
					}}
				}]
			}`)
			assertErrorContains(t, err, "nodes[0].config.exec.prompt_key", "nodes[0].config.exec.agent_key", "writer")
		})
	}
}

func TestHandleCreateDAGAcceptsAgentNodeWithPromptKey(t *testing.T) {
	handleCreateDAGAccepts(t, `{
		"agent_id":"designer-1",
		"dag_key":"dag-agent-prompt-key",
		"title":"Agent prompt key",
		"nodes":[{
			"node_key":"writer",
			"title":"Writer",
			"node_type":"agent",
			"assigned_to":"agent-parent",
			"config":{"exec":{
				"provider":"codex",
				"prompt_key":"main/code-task",
				"cwd":"/repo/project",
				"codex_home":"/tmp/codex-home",
				"codex_instance_key":"default",
				"codex_model_provider":"openai"
			}}
		}]
	}`)
}

func TestHandleCreateDAGAcceptsAgentNodeWithAgentKey(t *testing.T) {
	handleCreateDAGAccepts(t, `{
		"agent_id":"designer-1",
		"dag_key":"dag-agent-agent-key",
		"title":"Agent key",
		"nodes":[{
			"node_key":"writer",
			"title":"Writer",
			"assigned_to":"agent-parent",
			"config":{"exec":{
				"provider":"claude",
				"agent_key":"daily_brief_agent",
				"cwd":"/repo/project"
			}}
		}]
	}`)
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

func TestHandleCreateDAGRejectsHybridVerifierMissingLaunchIdentity(t *testing.T) {
	err := handleCreateDAGRejects(t, `{
		"agent_id":"designer-1",
		"dag_key":"dag-hybrid-missing-launch-identity",
		"title":"Hybrid missing launch identity",
		"nodes":[{
			"node_key":"review",
			"title":"Review",
			"node_type":"hybrid",
			"assigned_to":"agent-parent",
			"config":{"exec":{
				"automation":{"kind":"command_card","command_ref":"run_tests"},
				"verifier":{
					"provider":"codex",
					"cwd":"/repo/project",
					"codex_home":"/tmp/codex-home",
					"codex_instance_key":"default",
					"codex_model_provider":"openai"
				}
			}}
		}]
	}`)
	assertErrorContains(t, err, "nodes[0].config.exec.verifier.prompt_key", "nodes[0].config.exec.verifier.agent_key", "review")
}

func TestHandleCreateDAGRejectsHybridVerifierMissingProvider(t *testing.T) {
	err := handleCreateDAGRejects(t, `{
		"agent_id":"designer-1",
		"dag_key":"dag-hybrid-missing-provider",
		"title":"Hybrid missing provider",
		"nodes":[{
			"node_key":"review",
			"title":"Review",
			"node_type":"hybrid",
			"assigned_to":"agent-parent",
			"config":{"exec":{
				"automation":{"kind":"command_card","command_ref":"run_tests"},
				"verifier":{
					"prompt_key":"main/review-task",
					"cwd":"/repo/project"
				}
			}}
		}]
	}`)
	assertErrorContains(t, err, "nodes[0].config.exec.verifier.provider", "review", "claude", "codex")
}

func TestHandleCreateDAGRejectsCodexHybridVerifierMissingIdentity(t *testing.T) {
	err := handleCreateDAGRejects(t, `{
		"agent_id":"designer-1",
		"dag_key":"dag-hybrid-missing-codex-identity",
		"title":"Hybrid missing codex identity",
		"nodes":[{
			"node_key":"review",
			"title":"Review",
			"node_type":"hybrid",
			"assigned_to":"agent-parent",
			"config":{"exec":{
				"automation":{"kind":"command_card","command_ref":"run_tests"},
				"verifier":{
					"provider":"codex",
					"prompt_key":"main/review-task",
					"cwd":"/repo/project"
				}
			}}
		}]
	}`)
	assertErrorContains(t, err, "review", "codex_home", "codex_instance_key", "codex_model_provider")
}

func handleCreateDAGRejects(t *testing.T, raw string) error {
	t.Helper()
	handler := HandleCreateDAG(&golden.OrchestrationStub{
		CreateDAGFunc: func(context.Context, contract.CreateDAGRequest) (contract.DAGDetail, error) {
			t.Fatal("CreateDAG should not be called for invalid task_create_dag input")
			return contract.DAGDetail{}, nil
		},
	})
	_, err := handler(context.Background(), json.RawMessage(raw))
	if err == nil {
		t.Fatal("HandleCreateDAG() error = nil, want validation failure")
	}
	return err
}

func handleCreateDAGAccepts(t *testing.T, raw string) {
	t.Helper()
	called := false
	handler := HandleCreateDAG(&golden.OrchestrationStub{
		CreateDAGFunc: func(context.Context, contract.CreateDAGRequest) (contract.DAGDetail, error) {
			called = true
			return contract.DAGDetail{}, nil
		},
	})
	if _, err := handler(context.Background(), json.RawMessage(raw)); err != nil {
		t.Fatalf("HandleCreateDAG() error = %v", err)
	}
	if !called {
		t.Fatal("CreateDAG was not called for valid task_create_dag input")
	}
}

func assertErrorContains(t *testing.T, err error, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("HandleCreateDAG() error = %q, want substring %q", err.Error(), want)
		}
	}
}

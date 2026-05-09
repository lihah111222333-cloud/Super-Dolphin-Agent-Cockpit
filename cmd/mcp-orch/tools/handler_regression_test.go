package tools

import (
	"context"
	"encoding/json"
	"testing"
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
	})
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
	})
	if err != nil {
		t.Fatalf("createDAGRequestFromInput() error = %v", err)
	}
	if req.CreatedBy != "agent-42" {
		t.Fatalf("CreatedBy = %q, want agent-42", req.CreatedBy)
	}
}

func TestOrchestrationNilGuardsUseConsistentMessage(t *testing.T) {
	handlers := []struct {
		name    string
		handler ToolHandler
		input   string
	}{
		{name: "launch", handler: HandleLaunchAgent(nil), input: `{}`},
		{name: "send", handler: HandleSendMessage(nil), input: `{}`},
		{name: "stop", handler: HandleStopAgent(nil), input: `{}`},
		{name: "list", handler: HandleListAgents(nil), input: `{}`},
		{name: "report", handler: HandleGetAgentReport(nil), input: `{}`},
		{name: "create_dag", handler: HandleCreateDAG(nil), input: `{}`},
		{name: "get_dag", handler: HandleGetDAG(nil), input: `{}`},
		{name: "update_node", handler: HandleUpdateNode(nil), input: `{}`},
		{name: "start_dag", handler: HandleStartDAG(nil), input: `{}`},
	}
	for _, tc := range handlers {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.handler(context.Background(), json.RawMessage(tc.input))
			if err == nil || err.Error() != "orchestration service is not configured" {
				t.Fatalf("handler error = %v", err)
			}
		})
	}
}

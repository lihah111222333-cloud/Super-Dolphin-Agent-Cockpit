package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
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

func TestHandleListDAGsReturnsWrappedDAGs(t *testing.T) {
	var gotFilter contract.ListDAGsFilter
	handler := HandleListDAGs(&golden.OrchestrationStub{
		ListDAGsFunc: func(_ context.Context, filter contract.ListDAGsFilter) ([]contract.DAGSummary, error) {
			gotFilter = filter
			return []contract.DAGSummary{{DagKey: "dag-1", Title: "Daily", Status: "running"}}, nil
		},
	})

	resp, err := handler(context.Background(), json.RawMessage(`{"status":" running ","keyword":" daily ","limit":7}`))
	if err != nil {
		t.Fatalf("HandleListDAGs() error = %v", err)
	}
	out, ok := resp.(ListDAGsOutput)
	if !ok {
		t.Fatalf("HandleListDAGs() response = %T, want ListDAGsOutput", resp)
	}
	if len(out.DAGs) != 1 || out.DAGs[0].DagKey != "dag-1" {
		t.Fatalf("HandleListDAGs() = %#v", out)
	}
	if gotFilter != (contract.ListDAGsFilter{Status: "running", Keyword: "daily", Limit: 7}) {
		t.Fatalf("ListDAGs filter = %#v", gotFilter)
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

func TestHandleCreateDAGPreservesFinalNodeKey(t *testing.T) {
	var got contract.CreateDAGRequest
	handler := HandleCreateDAG(&golden.OrchestrationStub{
		CreateDAGFunc: func(_ context.Context, req contract.CreateDAGRequest) (contract.DAGDetail, error) {
			got = req
			return contract.DAGDetail{}, nil
		},
	})

	_, err := handler(context.Background(), json.RawMessage(`{
		"agent_id":"designer-1",
		"dag_key":"dag-final",
		"title":"Daily final output",
		"schedule":{"trigger":"manual"},
		"final_node_key":" delivery_writer ",
		"nodes":[
			{"node_key":"source_monitor","title":"Source monitor"},
			{"node_key":"delivery_writer","title":"Delivery writer","depends_on":["source_monitor"]}
		]
	}`))
	if err != nil {
		t.Fatalf("HandleCreateDAG() error = %v", err)
	}
	var metadata map[string]any
	if err := json.Unmarshal(got.Metadata, &metadata); err != nil {
		t.Fatalf("unmarshal metadata: %v raw=%s", err, string(got.Metadata))
	}
	if metadata["final_node_key"] != "delivery_writer" {
		t.Fatalf("metadata.final_node_key = %v, want delivery_writer (metadata=%v)", metadata["final_node_key"], metadata)
	}
}

func TestHandleCreateDAGRejectsUnknownFinalNodeKey(t *testing.T) {
	handler := HandleCreateDAG(&golden.OrchestrationStub{
		CreateDAGFunc: func(context.Context, contract.CreateDAGRequest) (contract.DAGDetail, error) {
			t.Fatal("CreateDAG should not be called for unknown final_node_key")
			return contract.DAGDetail{}, nil
		},
	})

	_, err := handler(context.Background(), json.RawMessage(`{
		"agent_id":"designer-1",
		"dag_key":"dag-final",
		"title":"Daily final output",
		"schedule":{"trigger":"manual"},
		"final_node_key":"missing_final",
		"nodes":[{"node_key":"source_monitor","title":"Source monitor"}]
	}`))
	if err == nil || err.Error() != "final_node_key missing_final does not match any node_key" {
		t.Fatalf("HandleCreateDAG() error = %v, want final_node_key validation", err)
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
		{name: "list_dags", handler: HandleListDAGs(nil), input: `{}`},
		{name: "get_dag", handler: HandleGetDAG(nil), input: `{}`},
		{name: "update_node", handler: HandleUpdateNode(nil), input: `{}`},
		{name: "start_dag", handler: HandleStartDAG(nil), input: `{}`},
		{name: "get_run", handler: HandleGetRun(nil), input: `{}`},
		{name: "apply_ops", handler: HandleApplyOps(nil), input: `{}`},
		{name: "list_runs", handler: HandleListRuns(nil), input: `{}`},
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

package dashboard

import (
	"encoding/json"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
)

func assertDashboardDAGCreateAndStart(t *testing.T, server *platformrpc.Server, orchestration *stubDashboardOrchestration) {
	t.Helper()

	var startResp struct {
		DAGKey         string `json:"dagKey"`
		RunID          int64  `json:"runId"`
		RunKey         string `json:"runKey"`
		Version        int64  `json:"version"`
		ExecutionState string `json:"executionState"`
	}
	if err := dispatchDashboardInto(server, "dashboard/dagCreateAndStart", `{"dagKey":"created-dag","title":"Created DAG","description":"from template","finalNodeKey":"draft","metadata":{"source":"ui"},"idempotencyKey":"ui-create-123","nodes":[{"nodeKey":"draft","title":"Draft","nodeType":"agent","assignedTo":"codex-runner","dependsOn":["intake"],"commandRef":"prompt_list","config":{"prompt":"draft"}}]}`, &startResp); err != nil {
		t.Fatalf("dispatch dag create and start error = %v", err)
	}
	assertDashboardDAGCreateAndStartResponse(t, startResp)
	assertDashboardCreateDAGRequest(t, orchestration.createDAGRequest)
	assertDashboardCreateAndStartRequest(t, orchestration.startDAGRequest)
}

func assertDashboardDAGCreateAndStartResponse(t *testing.T, resp struct {
	DAGKey         string `json:"dagKey"`
	RunID          int64  `json:"runId"`
	RunKey         string `json:"runKey"`
	Version        int64  `json:"version"`
	ExecutionState string `json:"executionState"`
}) {
	t.Helper()
	if resp.DAGKey != "created-dag" || resp.RunKey != "dag-1#run-ui" || resp.Version != 9 {
		t.Fatalf("dag create and start response = %#v", resp)
	}
	if resp.RunID != 88 || resp.ExecutionState != "waiting_for_assignee" {
		t.Fatalf("dag create and start response = %#v", resp)
	}
}

func assertDashboardCreateDAGRequest(t *testing.T, req contract.CreateDAGRequest) {
	t.Helper()
	if req.DagKey != "created-dag" || req.Title != "Created DAG" || req.Description != "from template" {
		t.Fatalf("CreateDAG() request = %#v", req)
	}
	if req.CreatedBy != dashboardUICreatedBy {
		t.Fatalf("CreateDAG() createdBy = %q, want %q", req.CreatedBy, dashboardUICreatedBy)
	}
	assertDashboardCreateDAGNode(t, req.Nodes)
	assertDashboardCreateDAGMetadata(t, req.Metadata)
}

func assertDashboardCreateDAGNode(t *testing.T, nodes []contract.CreateDAGNodeRequest) {
	t.Helper()
	if len(nodes) != 1 {
		t.Fatalf("CreateDAG() nodes = %#v", nodes)
	}
	node := nodes[0]
	if node.NodeKey != "draft" || node.NodeType != "agent" || node.AssignedTo != "codex-runner" || node.CommandRef != "prompt_list" {
		t.Fatalf("CreateDAG() node = %#v", node)
	}
	if len(node.DependsOn) != 1 || node.DependsOn[0] != "intake" {
		t.Fatalf("CreateDAG() node dependencies = %#v", node.DependsOn)
	}
	if string(node.Config) != `{"prompt":"draft"}` {
		t.Fatalf("CreateDAG() node config = %s", node.Config)
	}
}

func assertDashboardCreateDAGMetadata(t *testing.T, raw json.RawMessage) {
	t.Helper()
	var metadata map[string]any
	if err := json.Unmarshal(raw, &metadata); err != nil {
		t.Fatalf("CreateDAG() metadata decode error = %v", err)
	}
	if metadata["source"] != "ui" || metadata["final_node_key"] != "draft" {
		t.Fatalf("CreateDAG() metadata = %#v", metadata)
	}
	schedule, ok := metadata["schedule"].(map[string]any)
	if !ok || schedule["trigger"] != "manual" {
		t.Fatalf("CreateDAG() schedule = %#v", metadata["schedule"])
	}
}

func assertDashboardCreateAndStartRequest(t *testing.T, req contract.StartDAGRequest) {
	t.Helper()
	want := contract.StartDAGRequest{DagKey: "created-dag", TriggerSource: "manual", IdempotencyKey: "ui-create-123"}
	if req != want {
		t.Fatalf("StartDAG() request after create = %#v", req)
	}
}

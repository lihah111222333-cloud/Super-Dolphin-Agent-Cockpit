package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/testutil/golden"
)

func TestWorkflowDiagnosticsReturnsDerivedRunAndMatchingThreadNode(t *testing.T) {
	threadID := "thread-child"
	handler := HandleWorkflowDiagnostics(&golden.OrchestrationStub{
		GetRunFunc: func(_ context.Context, req contract.GetRunRequest) (contract.GetRunResponse, error) {
			if req.RunKey != "run-1" {
				t.Fatalf("GetRun() req = %#v", req)
			}
			return contract.GetRunResponse{
				Run: contract.Run{ID: 88, RunKey: "run-1", DagKey: "dag-1", Status: "failed"},
				Nodes: []contract.DAGNode{{
					DagKey:           "dag-1",
					NodeKey:          "draft",
					Status:           "failed",
					AssignedTo:       "codex-runner",
					SpawningThreadID: &threadID,
					Result:           json.RawMessage(`{"failure_class":"transient","sharedfile":{"path":"reports/draft.md"}}`),
				}},
			}, nil
		},
	})

	result, err := handler(context.Background(), json.RawMessage(`{"run_key":"run-1","child_thread_id":"thread-child"}`))
	if err != nil {
		t.Fatalf("HandleWorkflowDiagnostics() error = %v", err)
	}
	out := result.(WorkflowDiagnosticsOutput)
	if len(out.Runs) != 1 || out.Runs[0].DerivedState != "recoverable_failed" || out.Runs[0].ArtifactCount != 1 {
		t.Fatalf("diagnostic runs = %#v", out.Runs)
	}
	if len(out.Nodes) != 1 || out.Nodes[0].FailureClass != "transient" || len(out.Nodes[0].ArtifactLinks) != 1 {
		t.Fatalf("diagnostic nodes = %#v", out.Nodes)
	}
}

func TestWorkflowDiagnosticsRejectsMissingIdentifier(t *testing.T) {
	handler := HandleWorkflowDiagnostics(&golden.OrchestrationStub{})
	_, err := handler(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "diagnostics require") {
		t.Fatalf("HandleWorkflowDiagnostics() error = %v, want missing identifier", err)
	}
}

func TestListRunsAddsDerivedSummary(t *testing.T) {
	handler := HandleListRuns(&golden.OrchestrationStub{
		ListRunsFunc: func(_ context.Context, req contract.ListRunsRequest) (contract.ListRunsResponse, error) {
			if req.DagKey != "dag-1" {
				t.Fatalf("ListRuns() req = %#v", req)
			}
			return contract.ListRunsResponse{Runs: []contract.Run{{
				RunKey: "run-1",
				DagKey: "dag-1",
				Status: "running",
			}}}, nil
		},
	})

	result, err := handler(context.Background(), json.RawMessage(`{"dag_key":"dag-1"}`))
	if err != nil {
		t.Fatalf("HandleListRuns() error = %v", err)
	}
	out := result.(ListRunsOutput)
	if len(out.Runs) != 1 || out.Runs[0].DerivedState != "active" || out.Runs[0].NextAction != "monitor" {
		t.Fatalf("ListRunsOutput.Runs = %#v", out.Runs)
	}
}

func TestWorkflowRecoveryCancelWithCleanupUsesTerminateDAG(t *testing.T) {
	var got contract.TerminateDAGRequest
	handler := HandleWorkflowRecoveryAction(&golden.OrchestrationStub{
		TerminateDAGFunc: func(_ context.Context, req contract.TerminateDAGRequest) error {
			got = req
			return nil
		},
	})

	result, err := handler(context.Background(), json.RawMessage(`{"action":"cancel_with_cleanup","pos":"dag:dag-1/run:run-1","reason":"operator_cancelled"}`))
	if err != nil {
		t.Fatalf("HandleWorkflowRecoveryAction() error = %v", err)
	}
	if got != (contract.TerminateDAGRequest{DagKey: "dag-1", RunKey: "run-1", Reason: "operator_cancelled"}) {
		t.Fatalf("TerminateDAG() req = %#v", got)
	}
	out := result.(WorkflowRecoveryActionOutput)
	if out.Action != "cancel_with_cleanup" || out.Status != "accepted" || out.RunKey != "run-1" {
		t.Fatalf("recovery output = %#v", out)
	}
}

func TestWorkflowRecoveryRetryFailedNodeFailsFastWithoutRuntimeContract(t *testing.T) {
	handler := HandleWorkflowRecoveryAction(&golden.OrchestrationStub{})
	_, err := handler(context.Background(), json.RawMessage(`{"action":"retry_failed_node","run_id":88,"node_key":"draft"}`))
	if err == nil || !strings.Contains(err.Error(), "runtime reset/retry contract") {
		t.Fatalf("HandleWorkflowRecoveryAction() error = %v, want runtime contract message", err)
	}
}

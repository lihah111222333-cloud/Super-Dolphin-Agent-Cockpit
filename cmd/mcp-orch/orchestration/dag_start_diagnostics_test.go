package orchestration

import (
	"context"
	"strings"
	"testing"

	taskdag "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestStartDAGResponseIncludesDispatchDiagnostics(t *testing.T) {
	dagStore := &stubStartDAGStore{dag: &taskdag.DAG{DagKey: "dag-1"}}
	runStore := &stubRunStore{promoteRows: 1, scheduleRootWakeupsRows: 1}
	svc := makeStartDAGService(dagStore, runStore)

	resp, err := svc.StartDAG(context.Background(), StartDAGRequest{DagKey: "dag-1", TriggerSource: "manual"})
	if err != nil {
		t.Fatalf("StartDAG() error = %v, want nil", err)
	}

	if resp.RunID != 99 || resp.ReadyRootNodes != 1 || resp.ScheduledWakeups != 1 {
		t.Fatalf("StartDAG() response = %#v, want run_id=99 ready_roots=1 scheduled_wakeups=1", resp)
	}
	if resp.ExecutionState != contract.StartDAGExecutionQueued || resp.Warning != "" {
		t.Fatalf("StartDAG() response = %#v, want queued with no warning", resp)
	}
}

func TestStartDAG_ReturnsWaitingForAssigneeWhenNoRootWakeupsScheduled(t *testing.T) {
	dagStore := &stubStartDAGStore{dag: &taskdag.DAG{DagKey: "dag-1"}}
	runStore := &stubRunStore{promoteRows: 1, scheduleRootWakeupsRows: 0}
	svc := makeStartDAGService(dagStore, runStore)

	resp, err := svc.StartDAG(context.Background(), StartDAGRequest{DagKey: "dag-1", TriggerSource: "manual"})
	if err != nil {
		t.Fatalf("StartDAG() error = %v, want nil", err)
	}

	if resp.ExecutionState != contract.StartDAGExecutionWaitingForAssignee || resp.ScheduledWakeups != 0 {
		t.Fatalf("StartDAG() response = %#v, want waiting_for_assignee with no scheduled wakeups", resp)
	}
	if !strings.Contains(resp.Warning, "task_dispatch_node") {
		t.Fatalf("resp.Warning = %q, want task_dispatch_node guidance", resp.Warning)
	}
}

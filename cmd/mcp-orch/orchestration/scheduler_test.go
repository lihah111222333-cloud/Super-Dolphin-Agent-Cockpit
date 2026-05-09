package orchestration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
)

func TestNoopScheduler_TickReturnsNotImplemented(t *testing.T) {
	s := NewNoopScheduler()
	n, err := s.Tick(context.Background(), time.Now())
	if !errors.Is(err, ErrSchedulerNotImplemented) {
		t.Fatalf("Tick err = %v, want ErrSchedulerNotImplemented", err)
	}
	if n != 0 {
		t.Fatalf("Tick count = %d, want 0", n)
	}
}

func TestNoopScheduler_ScheduleReturnsNotImplemented(t *testing.T) {
	s := NewNoopScheduler()
	if err := s.Schedule(context.Background(), "dag-x"); !errors.Is(err, ErrSchedulerNotImplemented) {
		t.Fatalf("Schedule err = %v, want ErrSchedulerNotImplemented", err)
	}
}

func TestNoopScheduler_ImplementsScheduler(t *testing.T) {
	// 编译期检查
	var _ Scheduler = noopScheduler{}
	var _ Scheduler = NewNoopScheduler()
}

// === DAG lifecycle stub (S2.1) ===

func TestService_StartDAG_NotImplemented(t *testing.T) {
	s := &service{}
	resp, err := s.StartDAG(context.Background(), StartDAGRequest{DagKey: "dag-x", TriggerSource: "manual"})
	if !errors.Is(err, ErrLifecycleNotImplemented) {
		t.Fatalf("StartDAG err = %v, want ErrLifecycleNotImplemented", err)
	}
	if resp.RunKey != "" || resp.Version != 0 {
		t.Fatalf("StartDAG resp should be zero value, got %+v", resp)
	}
}

func TestService_TerminateDAG_NotImplemented(t *testing.T) {
	s := &service{}
	err := s.TerminateDAG(context.Background(), TerminateDAGRequest{DagKey: "dag-x", RunKey: "run-y"})
	if !errors.Is(err, ErrLifecycleNotImplemented) {
		t.Fatalf("TerminateDAG err = %v, want ErrLifecycleNotImplemented", err)
	}
}

func TestService_ApplyOps_NotImplemented(t *testing.T) {
	s := &service{}
	req := nodeexec.OpsRequest{
		DagKey:      "dag-x",
		BaseVersion: 1,
		Ops:         nodeexec.Ops{nodeexec.OpRemoveNode{NodeKey: "n1"}},
	}
	resp, err := s.ApplyOps(context.Background(), req)
	if !errors.Is(err, ErrLifecycleNotImplemented) {
		t.Fatalf("ApplyOps err = %v, want ErrLifecycleNotImplemented", err)
	}
	if resp.NewVersion != 0 {
		t.Fatalf("ApplyOps resp should be zero value, got %+v", resp)
	}
}

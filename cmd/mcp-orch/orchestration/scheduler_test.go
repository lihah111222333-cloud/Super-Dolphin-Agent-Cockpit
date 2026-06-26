package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
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

// DAG 生命周期空实现测试，锁定未接入 run store 时的错误边界。

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

func TestService_TerminateDAG_RunStoreUnset(t *testing.T) {
	s := &service{}
	err := s.TerminateDAG(context.Background(), TerminateDAGRequest{DagKey: "dag-x", RunKey: "run-y"})
	if !errors.Is(err, ErrRunStoreUnset) {
		t.Fatalf("TerminateDAG err = %v, want ErrRunStoreUnset", err)
	}
}

// TestService_ApplyOps_UpdateDAGImplemented 验证 update_dag 操作已进入业务层。
// 该用例锁定 ApplyOps 对 DAG 更新操作的调度路径，避免工具层只返回空响应。
func TestService_ApplyOps_UpdateDAGImplemented(t *testing.T) {
	stub := &stubDAGOpsStore{currentVersion: 1}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-x",
		BaseVersion: 1,
		Ops:         json.RawMessage(`[{"op":"update_dag","patch":{"title":"x"}}]`),
	}
	resp, err := s.ApplyOps(context.Background(), req)
	if err != nil {
		t.Fatalf("ApplyOps err = %v, want nil", err)
	}
	if resp.NewVersion != 2 {
		t.Fatalf("NewVersion = %d, want 2", resp.NewVersion)
	}
}

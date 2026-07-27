package taskupdatelease

import (
	"context"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
	mcpcommon "github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

type leaseStoreStub struct {
	acquireCalls []taskdag.AcquireWorkerLeaseInput
	renewCalls   []taskdag.RenewWorkerLeaseInput
	acquireRows  int64
	acquireErr   error
	renewRows    int64
	renewErr     error
}

func (s *leaseStoreStub) AcquireWorkerLease(_ context.Context, input taskdag.AcquireWorkerLeaseInput) (int64, error) {
	s.acquireCalls = append(s.acquireCalls, input)
	return s.acquireRows, s.acquireErr
}

func (s *leaseStoreStub) RenewWorkerLease(_ context.Context, input taskdag.RenewWorkerLeaseInput) (int64, error) {
	s.renewCalls = append(s.renewCalls, input)
	return s.renewRows, s.renewErr
}

func (s *leaseStoreStub) ReleaseWorkerLease(context.Context, taskdag.ReleaseWorkerLeaseInput) error {
	return nil
}

func leaseTestContext() context.Context {
	return mcpcommon.WithToolScope(context.Background(), mcpcommon.ToolScope{AgentID: "owner-agent"})
}

func leaseTestNode(status string) taskdag.Node {
	return taskdag.Node{
		DagKey:     "dag-1",
		NodeKey:    "node-1",
		Status:     status,
		AssignedTo: "target-agent",
	}
}

// TestValidateRejectsAcquireZeroRows 验证正式领取失败时必须阻断状态写入。
func TestValidateRejectsAcquireZeroRows(t *testing.T) {
	store := &leaseStoreStub{}

	err := Validate(leaseTestContext(), store, leaseTestNode("ready"), "running", "30s")
	if err == nil || !strings.Contains(err.Error(), "worker lease rejected") {
		t.Fatalf("Validate err = %v, want worker lease rejection", err)
	}
	if len(store.acquireCalls) != 1 {
		t.Fatalf("AcquireWorkerLease calls = %d, want 1", len(store.acquireCalls))
	}
	if len(store.renewCalls) != 0 {
		t.Fatalf("RenewWorkerLease calls = %d, want 0", len(store.renewCalls))
	}
}

// TestValidateReadyToRunningAcquiresWithoutRenew 验证 ready→running 只建立租约，不先续约。
func TestValidateReadyToRunningAcquiresWithoutRenew(t *testing.T) {
	store := &leaseStoreStub{acquireRows: 1}

	err := Validate(leaseTestContext(), store, leaseTestNode("ready"), "running", "30s")
	if err != nil {
		t.Fatalf("Validate err = %v", err)
	}
	if len(store.acquireCalls) != 1 {
		t.Fatalf("AcquireWorkerLease calls = %d, want 1", len(store.acquireCalls))
	}
	if len(store.renewCalls) != 0 {
		t.Fatalf("RenewWorkerLease calls = %d, want 0", len(store.renewCalls))
	}
	got := store.acquireCalls[0]
	if got.TargetAgentID != "target-agent" || got.OwnerID != "owner-agent" || got.LeaseInterval != "30s" {
		t.Fatalf("AcquireWorkerLease input = %+v, want target-agent/owner-agent/30s", got)
	}
}

// TestValidateRunningTerminalRenewsWithoutAcquire 验证运行中节点写终态仍只做 fencing 续约。
func TestValidateRunningTerminalRenewsWithoutAcquire(t *testing.T) {
	store := &leaseStoreStub{renewRows: 1}

	err := Validate(leaseTestContext(), store, leaseTestNode("running"), "done", "30s")
	if err != nil {
		t.Fatalf("Validate err = %v", err)
	}
	if len(store.renewCalls) != 1 {
		t.Fatalf("RenewWorkerLease calls = %d, want 1", len(store.renewCalls))
	}
	if len(store.acquireCalls) != 0 {
		t.Fatalf("AcquireWorkerLease calls = %d, want 0", len(store.acquireCalls))
	}
	got := store.renewCalls[0]
	if got.TargetAgentID != "target-agent" || got.OwnerID != "owner-agent" || got.LeaseInterval != "30s" {
		t.Fatalf("RenewWorkerLease input = %+v, want target-agent/owner-agent/30s", got)
	}
}

// TestValidateRenewZeroRowsRejectsWithoutAcquire 验证续约 fencing 失败后不得回退为重新领取。
func TestValidateRenewZeroRowsRejectsWithoutAcquire(t *testing.T) {
	store := &leaseStoreStub{}

	err := Validate(leaseTestContext(), store, leaseTestNode("running"), "done", "30s")
	if err == nil || !strings.Contains(err.Error(), "worker lease rejected") {
		t.Fatalf("Validate err = %v, want worker lease rejection", err)
	}
	if len(store.renewCalls) != 1 {
		t.Fatalf("RenewWorkerLease calls = %d, want 1", len(store.renewCalls))
	}
	if len(store.acquireCalls) != 0 {
		t.Fatalf("AcquireWorkerLease calls = %d, want 0 after renew fencing rejection", len(store.acquireCalls))
	}
}

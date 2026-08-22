package heterociworkflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociowner"
)

// TestWorkflowIntegrityRejectsMismatchedReceiptIdentity 验证 receipt 不能跨 batch、run 或 executor lane 复用。
func TestWorkflowIntegrityRejectsMismatchedReceiptIdentity(t *testing.T) {
	tests := []struct {
		name string
		mode integrityReceiptMode
	}{
		{name: "batch id", mode: integrityReceiptWrongBatchID},
		{name: "run id", mode: integrityReceiptWrongRunID},
		{name: "executor key", mode: integrityReceiptWrongExecutor},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor := &integrityReceiptExecutor{mode: test.mode}
			fixture := newWorkflowFixture(executor)
			executor.events = &fixture.events

			_, err := Execute(t.Context(), fixture.deps, fixture.request)
			if !errors.Is(err, ErrBatchResult) || !errors.Is(err, ErrBatchReceiptRequired) || !errors.Is(err, ErrCleanupIncomplete) {
				t.Fatalf("Execute error = %v, want receipt identity and cleanup blockers", err)
			}
			if executor.executeCount != 1 || executor.receiptCount != 1 {
				t.Fatalf("execute/receipt count = %d/%d", executor.executeCount, executor.receiptCount)
			}
			if fixture.owner.finishCount != 0 || fixture.owner.cancelCount != 0 {
				t.Fatalf("mismatched receipt mutated durable state: finish/cancel = %d/%d", fixture.owner.finishCount, fixture.owner.cancelCount)
			}
		})
	}
}

// TestWorkflowIntegrityRejectsNonDerivedPlanOperation 验证计划 operation identity 必须由 run identity 派生。
func TestWorkflowIntegrityRejectsNonDerivedPlanOperation(t *testing.T) {
	fixture := newWorkflowFixture(&fakeExecutor{})
	fixture.deps.Owner = &integrityPlanOwner{fakeOwner: fixture.owner}

	_, err := Execute(t.Context(), fixture.deps, fixture.request)
	if !errors.Is(err, ErrExecutionState) {
		t.Fatalf("Execute error = %v, want ErrExecutionState", err)
	}
	if fixture.owner.startCount != 0 || fixture.owner.cancelCount != len(fixture.units) {
		t.Fatalf("start/cancel count = %d/%d", fixture.owner.startCount, fixture.owner.cancelCount)
	}
}

// TestWorkflowIntegrityRejectsMalformedFinishCleanup 验证 cleanup Finish 返回值不能推进错误 fence、unit、状态或 lease identity。
func TestWorkflowIntegrityRejectsMalformedFinishCleanup(t *testing.T) {
	tests := []struct {
		name string
		mode integrityFinishMode
	}{
		{name: "wrong fence", mode: integrityFinishWrongFence},
		{name: "wrong unit", mode: integrityFinishWrongUnit},
		{name: "non failed", mode: integrityFinishNonFailed},
		{name: "identity drift", mode: integrityFinishIdentityDrift},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor := &integrityOutcomeExecutor{state: heteroci.LeaseStateSucceeded}
			fixture := newWorkflowFixture(executor)
			executor.events = &fixture.events
			owner := &integrityFinishOwner{fakeOwner: fixture.owner, mode: test.mode}
			fixture.deps.Owner = owner

			_, err := Execute(t.Context(), fixture.deps, fixture.request)
			if !errors.Is(err, ErrExecutionState) || !errors.Is(err, ErrBatchReceiptRequired) || !errors.Is(err, ErrCleanupIncomplete) {
				t.Fatalf("Execute error = %v, want malformed Finish uncertainty and cleanup blockers", err)
			}
			if owner.calls != 1 {
				t.Fatalf("Finish calls = %d, want one primary call without opposite-state cleanup retry", owner.calls)
			}
			if fixture.owner.startCount != 1 || fixture.owner.cancelCount != 0 || executor.executeCount != 1 {
				t.Fatalf("start/execute/cancel count = %d/%d/%d", fixture.owner.startCount, executor.executeCount, fixture.owner.cancelCount)
			}
			if fixture.owner.run.Leases[0].State != heteroci.LeaseStateStarted {
				t.Fatalf("malformed Finish advanced durable lease to %q", fixture.owner.run.Leases[0].State)
			}
		})
	}
}

// TestWorkflowIntegrityRejectsMalformedCancelCleanup 验证 cleanup CancelPlanned 返回值不能推进错误 fence、unit、状态或 lease identity。
func TestWorkflowIntegrityRejectsMalformedCancelCleanup(t *testing.T) {
	tests := []struct {
		name string
		mode integrityCancelMode
	}{
		{name: "wrong fence", mode: integrityCancelWrongFence},
		{name: "wrong unit", mode: integrityCancelWrongUnit},
		{name: "wrong state", mode: integrityCancelWrongState},
		{name: "identity drift", mode: integrityCancelIdentityDrift},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWorkflowFixture(&fakeExecutor{})
			fixture.binder.err = errors.New("binder unavailable")
			owner := &integrityCancelOwner{fakeOwner: fixture.owner, mode: test.mode}
			fixture.deps.Owner = owner

			_, err := Execute(t.Context(), fixture.deps, fixture.request)
			if !errors.Is(err, ErrExecutorBinding) || !errors.Is(err, ErrExecutionState) || !errors.Is(err, ErrCleanupIncomplete) {
				t.Fatalf("Execute error = %v, want binding and cleanup blockers", err)
			}
			if owner.calls != 1 || fixture.owner.startCount != 0 {
				t.Fatalf("Cancel/start calls = %d/%d", owner.calls, fixture.owner.startCount)
			}
			if fixture.owner.run.Leases[0].State != heteroci.LeaseStatePlanned {
				t.Fatalf("malformed CancelPlanned advanced durable lease to %q", fixture.owner.run.Leases[0].State)
			}
		})
	}
}

// TestWorkflowIntegrityFailedOutcomeReturnsTypedFailure 验证 failed outcome 只产生 typed failure，绝不重执行或 fallback。
func TestWorkflowIntegrityFailedOutcomeReturnsTypedFailure(t *testing.T) {
	executor := &integrityOutcomeExecutor{state: heteroci.LeaseStateFailed}
	fixture := newWorkflowFixture(executor)
	executor.events = &fixture.events

	result, err := Execute(t.Context(), fixture.deps, fixture.request)
	if err == nil || !errors.Is(err, ErrBatchFailed) {
		t.Fatalf("Execute error = %v, want ErrBatchFailed", err)
	}
	if _, ok := errors.AsType[*BatchOutcomeError](err); !ok {
		t.Fatalf("Execute error = %v, want BatchOutcomeError", err)
	}
	if executor.executeCount != 1 || fixture.owner.finishCount != 1 || fixture.owner.cancelCount != 0 {
		t.Fatalf("execute/finish/cancel count = %d/%d/%d", executor.executeCount, fixture.owner.finishCount, fixture.owner.cancelCount)
	}
	if len(result.Units) != 0 || strings.Contains(err.Error(), fixture.manifest.Nodes[0].Endpoint) {
		t.Fatalf("failed outcome leaked success projection or endpoint: result=%+v error=%v", result, err)
	}
}

type integrityReceiptMode uint8

const (
	integrityReceiptWrongBatchID integrityReceiptMode = iota + 1
	integrityReceiptWrongRunID
	integrityReceiptWrongExecutor
)

type integrityReceiptExecutor struct {
	events       *[]string
	mode         integrityReceiptMode
	batch        ExecutionBatch
	executeCount int
	receiptCount int
}

// ExecuteBatch 故意只返回 transport error，让 workflow 读取 durable receipt。
func (executor *integrityReceiptExecutor) ExecuteBatch(_ context.Context, batch ExecutionBatch) (BatchResult, error) {
	*executor.events = append(*executor.events, "execute")
	executor.executeCount++
	executor.batch = batch
	return BatchResult{}, errors.New("transport acknowledgement lost")
}

// ReadBatchReceipt 返回指定 identity 被篡改的 canonical-shaped receipt。
func (executor *integrityReceiptExecutor) ReadBatchReceipt(_ context.Context, batch ExecutionBatch) (BatchResult, bool, error) {
	*executor.events = append(*executor.events, "receipt")
	executor.receiptCount++
	executor.batch = batch
	result := integrityBatchResult(executor.batch, heteroci.LeaseStateSucceeded)
	switch executor.mode {
	case integrityReceiptWrongBatchID:
		result.BatchID = BatchID("wrong-batch")
	case integrityReceiptWrongRunID:
		result.RunID = "wrong-run"
	case integrityReceiptWrongExecutor:
		result.Executor = ExecutorKey{Kind: heteroci.ExecutorKindLocalNode, NodeID: "wrong-node"}
	}
	return result, true, nil
}

type integrityOutcomeExecutor struct {
	events       *[]string
	state        heteroci.RoutingLeaseState
	executeCount int
}

// ExecuteBatch 返回带完整 identity 的结果，供成功和 failed outcome 测试复用。
func (executor *integrityOutcomeExecutor) ExecuteBatch(_ context.Context, batch ExecutionBatch) (BatchResult, error) {
	*executor.events = append(*executor.events, "execute")
	executor.executeCount++
	return integrityBatchResult(batch, executor.state), nil
}

// integrityBatchResult 生成当前 batch 的完整本地结果头和逐 unit outcome。
func integrityBatchResult(batch ExecutionBatch, state heteroci.RoutingLeaseState) BatchResult {
	outcomes := make([]BatchOutcome, 0, len(batch.Units))
	for _, unit := range batch.Units {
		outcomes = append(outcomes, BatchOutcome{UnitID: unit.UnitID, State: state})
	}
	result := BatchResult{BatchID: batch.BatchID, RunID: batch.RunID, Executor: batch.Key, Authority: heteroci.AuthorityLocalNonAuthoritative, Status: ResultStatusNotVerified, Outcomes: outcomes}
	fillFakeBatchResultIdentity(&result, batch)
	return result
}

// integrityPlanOwner 只篡改返回投影，不篡改 fake owner 已落盘的合法计划。
type integrityPlanOwner struct {
	*fakeOwner
}

func (owner *integrityPlanOwner) PlanAndReserve(ctx context.Context, fence heteroci.OwnerFence, request heteroci.TopologyPlanRequest) (heterociowner.DurableReservedRoutingPlan, error) {
	planned, err := owner.fakeOwner.PlanAndReserve(ctx, fence, request)
	if err != nil {
		return heterociowner.DurableReservedRoutingPlan{}, err
	}
	wrong, err := DeriveOperationID(request.RunIdentity.DurableRunID, OperationPhaseStart, request.Units[0].UnitID)
	if err != nil {
		return heterociowner.DurableReservedRoutingPlan{}, err
	}
	planned.OperationID = wrong
	return planned, nil
}

type integrityFinishMode uint8

const (
	integrityFinishWrongFence integrityFinishMode = iota + 1
	integrityFinishWrongUnit
	integrityFinishNonFailed
	integrityFinishIdentityDrift
)

type integrityFinishOwner struct {
	*fakeOwner
	mode  integrityFinishMode
	calls int
}

// Finish 返回不可接受的 transition，且不改变 fake owner 的 durable projection。
func (owner *integrityFinishOwner) Finish(_ context.Context, fence heteroci.OwnerFence, _ heteroci.OperationID, _ heteroci.TopologyRunID, unitID string, _ heteroci.RoutingLeaseState) (heterociowner.DurableLeaseTransition, error) {
	owner.fakeOwner.record("finish")
	owner.calls++
	lease := integrityLease(owner.fakeOwner, unitID)
	lease.State = heteroci.LeaseStateFailed
	lease.FinishedAtUnixMilli = owner.now.UnixMilli()
	transition := heterociowner.DurableLeaseTransition{Fence: fence, Lease: lease}
	switch owner.mode {
	case integrityFinishWrongFence:
		transition.Fence = fence
	case integrityFinishWrongUnit:
		transition.Lease.UnitID = "wrong-unit"
	case integrityFinishNonFailed:
		transition.Lease.State = heteroci.LeaseStateCancelled
		transition.Lease.StartedAtUnixMilli = 0
	case integrityFinishIdentityDrift:
		transition.Lease.Endpoint = "https://drift.invalid/runner"
	}
	return transition, nil
}

type integrityCancelMode uint8

const (
	integrityCancelWrongFence integrityCancelMode = iota + 1
	integrityCancelWrongUnit
	integrityCancelWrongState
	integrityCancelIdentityDrift
)

type integrityCancelOwner struct {
	*fakeOwner
	mode  integrityCancelMode
	calls int
}

// CancelPlanned 返回不可接受的 transition，且不改变 fake owner 的 durable projection。
func (owner *integrityCancelOwner) CancelPlanned(_ context.Context, fence heteroci.OwnerFence, _ heteroci.OperationID, _ heteroci.TopologyRunID, unitID string) (heterociowner.DurableLeaseTransition, error) {
	owner.fakeOwner.record("cancel")
	owner.calls++
	lease := integrityLease(owner.fakeOwner, unitID)
	lease.State = heteroci.LeaseStateCancelled
	lease.FinishedAtUnixMilli = owner.now.UnixMilli()
	lease.StartedAtUnixMilli = 0
	transition := heterociowner.DurableLeaseTransition{Fence: fence, Lease: lease}
	switch owner.mode {
	case integrityCancelWrongFence:
		transition.Fence = fence
	case integrityCancelWrongUnit:
		transition.Lease.UnitID = "wrong-unit"
	case integrityCancelWrongState:
		transition.Lease.State = heteroci.LeaseStatePlanned
		transition.Lease.FinishedAtUnixMilli = 0
	case integrityCancelIdentityDrift:
		transition.Lease.Endpoint = "https://drift.invalid/runner"
	}
	return transition, nil
}

// integrityLease 读取 fake owner 当前 unit lease，供 malformed transition 构造使用。
func integrityLease(owner *fakeOwner, unitID string) heteroci.RoutingLease {
	for _, lease := range owner.run.Leases {
		if lease.UnitID == unitID {
			return lease
		}
	}
	return heteroci.RoutingLease{}
}

var (
	_ DurableOwner       = (*integrityPlanOwner)(nil)
	_ DurableOwner       = (*integrityFinishOwner)(nil)
	_ DurableOwner       = (*integrityCancelOwner)(nil)
	_ BatchExecutor      = (*integrityReceiptExecutor)(nil)
	_ BatchExecutor      = (*integrityOutcomeExecutor)(nil)
	_ BatchReceiptReader = (*integrityReceiptExecutor)(nil)
)

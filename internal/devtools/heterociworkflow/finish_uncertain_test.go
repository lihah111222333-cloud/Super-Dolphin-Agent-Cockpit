package heterociworkflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociowner"
)

// TestWorkflowFinishCommitThenErrorReplaysExactOperationWithoutRetry 验证 Finish
// 已经提交但响应丢失时，workflow 读取 exact operation 并禁止以 failed 反向重试。
func TestWorkflowFinishCommitThenErrorReplaysExactOperationWithoutRetry(t *testing.T) {
	executor := &fakeExecutor{result: BatchResult{
		Authority: heteroci.AuthorityLocalNonAuthoritative,
		Status:    ResultStatusNotVerified,
		Outcomes:  []BatchOutcome{{UnitID: "unit-1", State: heteroci.LeaseStateSucceeded}},
	}}
	fixture := newWorkflowFixture(executor)
	executor.events = &fixture.events
	owner := &finishCommitThenErrorOwner{fakeOwner: fixture.owner, readFound: true}
	fixture.deps.Owner = owner

	result, err := Execute(context.Background(), fixture.deps, fixture.request)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Units[0].State != heteroci.LeaseStateSucceeded {
		t.Fatalf("terminal state = %s, want succeeded", result.Units[0].State)
	}
	if owner.finishCalls != 1 || owner.readOperationCalls != 1 {
		t.Fatalf("finish/read operation calls = %d/%d, want 1/1", owner.finishCalls, owner.readOperationCalls)
	}
	if fixture.owner.finishCount != 1 || fixture.owner.cancelCount != 0 {
		t.Fatalf("underlying finish/cancel calls = %d/%d, want 1/0", fixture.owner.finishCount, fixture.owner.cancelCount)
	}
}

// TestWorkflowFinishErrorWithoutExactOperationStaysUncertain 验证无法证明 Finish
// 终态时只保留 uncertain，cleanup 不得以 failed 反向重试同一 operation。
func TestWorkflowFinishErrorWithoutExactOperationStaysUncertain(t *testing.T) {
	executor := &fakeExecutor{result: BatchResult{
		Authority: heteroci.AuthorityLocalNonAuthoritative,
		Status:    ResultStatusNotVerified,
		Outcomes:  []BatchOutcome{{UnitID: "unit-1", State: heteroci.LeaseStateSucceeded}},
	}}
	fixture := newWorkflowFixture(executor)
	executor.events = &fixture.events
	owner := &finishCommitThenErrorOwner{fakeOwner: fixture.owner, readFound: false}
	fixture.deps.Owner = owner

	_, err := Execute(context.Background(), fixture.deps, fixture.request)
	if !errors.Is(err, ErrBatchReceiptRequired) || !errors.Is(err, ErrCleanupIncomplete) {
		t.Fatalf("Execute error = %v, want receipt and cleanup blockers", err)
	}
	if owner.finishCalls != 1 || owner.readOperationCalls != 1 || fixture.owner.finishCount != 1 || fixture.owner.cancelCount != 0 {
		t.Fatalf("finish/read/underlying finish/cancel = %d/%d/%d/%d, want 1/1/1/0", owner.finishCalls, owner.readOperationCalls, fixture.owner.finishCount, fixture.owner.cancelCount)
	}
}

// TestWorkflowMalformedFinishReplaysExactOperation 验证 malformed Finish 返回也必须
// 先标记 uncertain，再以同 operation 的 canonical replay 恢复，不能猜测终态。
func TestWorkflowMalformedFinishReplaysExactOperation(t *testing.T) {
	executor := &fakeExecutor{result: BatchResult{
		Authority: heteroci.AuthorityLocalNonAuthoritative,
		Status:    ResultStatusNotVerified,
		Outcomes:  []BatchOutcome{{UnitID: "unit-1", State: heteroci.LeaseStateSucceeded}},
	}}
	fixture := newWorkflowFixture(executor)
	executor.events = &fixture.events
	owner := &finishCommitThenErrorOwner{fakeOwner: fixture.owner, readFound: true, malformed: true}
	fixture.deps.Owner = owner

	result, err := Execute(context.Background(), fixture.deps, fixture.request)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Units[0].State != heteroci.LeaseStateSucceeded || owner.finishCalls != 1 || owner.readOperationCalls != 1 {
		t.Fatalf("result/calls = %+v/%d/%d", result.Units, owner.finishCalls, owner.readOperationCalls)
	}
}

type finishCommitThenErrorOwner struct {
	*fakeOwner
	finishCalls        int
	readOperationCalls int
	lastFence          heteroci.OwnerFence
	lastOperationID    heteroci.OperationID
	replay             heterociowner.DurableOperationState
	readFound          bool
	malformed          bool
}

func (owner *finishCommitThenErrorOwner) Finish(ctx context.Context, fence heteroci.OwnerFence, operationID heteroci.OperationID, runID, unitID string, terminal heteroci.RoutingLeaseState) (heterociowner.DurableLeaseTransition, error) {
	owner.finishCalls++
	transition, err := owner.fakeOwner.Finish(ctx, fence, operationID, runID, unitID, terminal)
	if owner.finishCalls != 1 {
		return transition, err
	}
	owner.lastFence = fence
	owner.lastOperationID = operationID
	resultBytes, marshalErr := json.Marshal(transition)
	if marshalErr != nil {
		return heterociowner.DurableLeaseTransition{}, marshalErr
	}
	owner.replay = heterociowner.DurableOperationState{
		Fence:         transition.Fence,
		OperationID:   operationID,
		Kind:          "finish",
		RequestDigest: finishRequestDigest(fence, runID, unitID, terminal),
		ResultDigest:  sha256Digest(resultBytes),
		ResultBytes:   resultBytes,
		Revision:      transition.Fence.Revision,
	}
	if owner.malformed {
		return heterociowner.DurableLeaseTransition{}, nil
	}
	return transition, errors.New("finish acknowledgement lost after commit")
}

func (owner *finishCommitThenErrorOwner) ReadOperation(_ context.Context, fence heteroci.OwnerFence, operationID heteroci.OperationID) (heterociowner.DurableOperationState, bool, error) {
	owner.readOperationCalls++
	if fence != owner.lastFence || operationID != owner.lastOperationID {
		return heterociowner.DurableOperationState{}, false, errors.New("operation lookup identity mismatch")
	}
	return owner.replay, owner.readFound, nil
}

func finishRequestDigest(fence heteroci.OwnerFence, runID, unitID string, state heteroci.RoutingLeaseState) string {
	request, err := json.Marshal(struct {
		Fence  heteroci.OwnerFence        `json:"fence"`
		RunID  string                     `json:"run_id"`
		UnitID string                     `json:"unit_id"`
		State  heteroci.RoutingLeaseState `json:"state"`
	}{Fence: fence, RunID: runID, UnitID: unitID, State: state})
	if err != nil {
		// archguard:ignore panic_count -- 固定 finish 请求必须可 JSON 编码，失败表示测试夹具编程错误。
		panic(err)
	}
	return sha256Digest(request)
}

func sha256Digest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

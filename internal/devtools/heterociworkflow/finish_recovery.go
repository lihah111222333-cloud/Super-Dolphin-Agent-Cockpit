package heterociworkflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociowner"
)

const durableFinishOperationKind = "finish"

// durableOperationReader 是可选的 journal 读取端口，用于确认提交后响应丢失的 Finish。
type durableOperationReader interface {
	ReadOperation(ctx context.Context, fence heteroci.OwnerFence, operationID heteroci.OperationID) (heterociowner.DurableOperationState, bool, error)
}

type durableFinishOperationRequest struct {
	Fence  heteroci.OwnerFence        `json:"fence"`
	RunID  heteroci.TopologyRunID     `json:"run_id"`
	UnitID string                     `json:"unit_id"`
	State  heteroci.RoutingLeaseState `json:"state"`
}

type uncertainFinishError struct {
	cause error
}

// Error 返回不包含 executor、端点或请求载荷的稳定 Finish 不确定错误。
func (err uncertainFinishError) Error() string {
	return "heteroci workflow finish outcome is uncertain"
}

// Unwrap 保留 Finish 原始错误供 errors.Is 判断，同时隔离其错误文本。
func (err uncertainFinishError) Unwrap() error {
	return err.cause
}

// finishUnitWithRecovery 校验 Finish 返回并在异常时读取 exact journal operation。
func (workflow *Workflow) finishUnitWithRecovery(ctx context.Context, previousFence heteroci.OwnerFence, runID heteroci.TopologyRunID, unitID string, operationID heteroci.OperationID, terminal heteroci.RoutingLeaseState, started heteroci.RoutingLease, decision heteroci.RoutingDecision, transition heterociowner.DurableLeaseTransition, finishErr error, state *executionState) (heterociowner.DurableLeaseTransition, error) {
	now, nowErr := workflow.now()
	if finishErr == nil && nowErr == nil {
		if err := validateFinishedLease(previousFence, string(runID), unitID, started, decision, transition, now); err == nil {
			delete(state.uncertain, unitID)
			return transition, nil
		} else {
			finishErr = err
		}
	}
	state.uncertain[unitID] = struct{}{}
	if nowErr != nil && finishErr == nil {
		finishErr = nowErr
	}
	replayed, replayErr := workflow.replayFinishedOperation(ctx, previousFence, runID, unitID, operationID, terminal, started, decision, now, nowErr)
	if replayErr == nil {
		delete(state.uncertain, unitID)
		return replayed, nil
	}
	uncertain := uncertainFinishError{cause: finishErr}
	return heterociowner.DurableLeaseTransition{}, errors.Join(ErrExecutionState, ErrBatchReceiptRequired, uncertain, replayErr)
}

// replayFinishedOperation 通过 operation digest、kind、revision 和 lease projection 确认终态意图。
func (workflow *Workflow) replayFinishedOperation(ctx context.Context, previousFence heteroci.OwnerFence, runID heteroci.TopologyRunID, unitID string, operationID heteroci.OperationID, terminal heteroci.RoutingLeaseState, started heteroci.RoutingLease, decision heteroci.RoutingDecision, now time.Time, nowErr error) (heterociowner.DurableLeaseTransition, error) {
	if nowErr != nil {
		return heterociowner.DurableLeaseTransition{}, fmt.Errorf("%w: finish replay clock is unavailable", ErrBatchReceiptRequired)
	}
	operation, err := workflow.readFinishOperation(ctx, previousFence, operationID)
	if err != nil {
		return heterociowner.DurableLeaseTransition{}, err
	}
	requestBytes, err := json.Marshal(durableFinishOperationRequest{Fence: previousFence, RunID: runID, UnitID: unitID, State: terminal})
	if err != nil {
		return heterociowner.DurableLeaseTransition{}, fmt.Errorf("%w: finish request encoding failed", ErrBatchReceiptRequired)
	}
	if err := validateFinishOperationDigests(operation, requestBytes); err != nil {
		return heterociowner.DurableLeaseTransition{}, err
	}
	replay, err := decodeFinishOperation(operation)
	if err != nil {
		return heterociowner.DurableLeaseTransition{}, err
	}
	if err := validateFinishReplay(previousFence, string(runID), unitID, terminal, started, decision, operation, replay, now); err != nil {
		return heterociowner.DurableLeaseTransition{}, err
	}
	return replay, nil
}

// readFinishOperation 读取可选 durable journal，并将读取失败归类为 receipt 缺失。
func (workflow *Workflow) readFinishOperation(ctx context.Context, fence heteroci.OwnerFence, operationID heteroci.OperationID) (heterociowner.DurableOperationState, error) {
	reader, ok := workflow.deps.Owner.(durableOperationReader)
	if !ok {
		return heterociowner.DurableOperationState{}, fmt.Errorf("%w: finish replay reader is unavailable", ErrBatchReceiptRequired)
	}
	operation, found, err := reader.ReadOperation(ctx, fence, operationID)
	if err != nil {
		return heterociowner.DurableOperationState{}, fmt.Errorf("%w: finish replay read failed", ErrBatchReceiptRequired)
	}
	if !found {
		return heterociowner.DurableOperationState{}, fmt.Errorf("%w: finish operation is missing", ErrBatchReceiptRequired)
	}
	if err := validateFinishOperationEnvelope(fence, operationID, operation); err != nil {
		return heterociowner.DurableOperationState{}, err
	}
	return operation, nil
}

// validateFinishOperationEnvelope 校验 operation 的 kind、identity、revision 和 fence。
func validateFinishOperationEnvelope(fence heteroci.OwnerFence, operationID heteroci.OperationID, operation heterociowner.DurableOperationState) error {
	if operation.OperationID != operationID || operation.Kind != durableFinishOperationKind || operation.Revision != operation.Fence.Revision {
		return fmt.Errorf("%w: finish operation identity drifted", ErrBatchReceiptRequired)
	}
	if err := operation.Fence.Validate(); err != nil || operation.Fence.Scope != fence.Scope || operation.Fence.OwnerID != fence.OwnerID || operation.Fence.OwnerEpoch != fence.OwnerEpoch || operation.Fence.Revision != fence.Revision+1 {
		return fmt.Errorf("%w: finish operation fence drifted", ErrBatchReceiptRequired)
	}
	return nil
}

// validateFinishOperationDigests 校验 request/result payload 的 immutable digest。
func validateFinishOperationDigests(operation heterociowner.DurableOperationState, requestBytes []byte) error {
	if operation.RequestDigest != localDigestBytes(requestBytes) || operation.ResultDigest != localDigestBytes(operation.ResultBytes) {
		return fmt.Errorf("%w: finish operation digest drifted", ErrBatchReceiptRequired)
	}
	return nil
}

// decodeFinishOperation 解码 journal 中的 canonical terminal transition。
func decodeFinishOperation(operation heterociowner.DurableOperationState) (heterociowner.DurableLeaseTransition, error) {
	var replay heterociowner.DurableLeaseTransition
	if len(operation.ResultBytes) == 0 || json.Unmarshal(operation.ResultBytes, &replay) != nil {
		return heterociowner.DurableLeaseTransition{}, fmt.Errorf("%w: finish replay payload is invalid", ErrBatchReceiptRequired)
	}
	return replay, nil
}

// validateFinishReplay 校验 replay transition 与请求 terminal 及 started lease 完全一致。
func validateFinishReplay(previousFence heteroci.OwnerFence, runID, unitID string, terminal heteroci.RoutingLeaseState, started heteroci.RoutingLease, decision heteroci.RoutingDecision, operation heterociowner.DurableOperationState, replay heterociowner.DurableLeaseTransition, now time.Time) error {
	if replay.Fence != operation.Fence || replay.Lease.State != terminal {
		return fmt.Errorf("%w: finish replay projection drifted", ErrBatchReceiptRequired)
	}
	if err := validateFinishedLease(previousFence, runID, unitID, started, decision, replay, now); err != nil {
		return fmt.Errorf("%w: finish replay lease is invalid", ErrBatchReceiptRequired)
	}
	return nil
}

func localDigestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

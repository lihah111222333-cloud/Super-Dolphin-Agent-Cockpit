package remoteci

import (
	"context"
	"errors"
)

// EvidenceFinalizer 是唯一 remote run evidence/SQLite authority 最终化端口。
type EvidenceFinalizer func(RunInput, *RunResult, error) error

type preparedRunExecutor interface {
	RunPrepared(context.Context, *PreparedRun) (RunResult, error)
}

// ExecutionOwner 把唯一 Coordinator 与唯一 authority finalizer 绑定为不可拆执行 owner。
type ExecutionOwner struct {
	executor  preparedRunExecutor
	finalizer EvidenceFinalizer
}

// NewExecutionOwner 构造共享 remote execution owner；调用方不得分别调用执行与最终化。
func NewExecutionOwner(coordinator *Coordinator, finalizer EvidenceFinalizer) (*ExecutionOwner, error) {
	if coordinator == nil {
		return nil, errors.New("remote execution owner coordinator is required")
	}
	return newExecutionOwner(coordinator, finalizer)
}

func newExecutionOwner(executor preparedRunExecutor, finalizer EvidenceFinalizer) (*ExecutionOwner, error) {
	if interfaceValueIsNil(executor) || finalizer == nil {
		return nil, errors.New("remote execution owner dependencies are required")
	}
	return &ExecutionOwner{executor: executor, finalizer: finalizer}, nil
}

// ExecutePrepared 恰好执行一次冻结计划，并把原始执行错误交给同一 finalizer 裁决。
func (owner *ExecutionOwner) ExecutePrepared(ctx context.Context, input RunInput, prepared *PreparedRun) (RunResult, error) {
	if owner == nil || interfaceValueIsNil(owner.executor) || owner.finalizer == nil {
		return RunResult{}, errors.New("remote execution owner is invalid")
	}
	result, runErr := owner.executor.RunPrepared(ctx, prepared)
	if err := owner.finalizer(input, &result, runErr); err != nil {
		return result, err
	}
	return result, runErr
}

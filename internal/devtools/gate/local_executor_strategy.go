package gate

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// validateLocalExecutorProgramSupport 是唯一的本地执行器能力 owner。
//
// 内建策略在 canonical owner 能够通过严格本地 sandbox 前保持 ineligible，避免复制命令语义。
// Commands 程序继续复用现有 resolved-step runner。
func validateLocalExecutorProgramSupport(program ExecutorProgram) error {
	if err := validateExecutorProgram(program); err != nil {
		return fmt.Errorf("local executor program is invalid: %w", err)
	}
	if program.Strategy != ExecutorStrategyCommands {
		return fmt.Errorf("local executor strategy %q is ineligible: strict sandbox/exact-tree owner is unavailable", program.Strategy)
	}
	if program.NeedsFrontendSeed {
		return errors.New("local frontend workload is explicitly ineligible: a batch-private offline npm install receipt is required")
	}
	return nil
}

// localExecutorZeroStepError 将零步骤 workload 转换为明确的 fail-fast 错误。
func localExecutorZeroStepError(id GateID) error {
	_, program, err := executorProgramForWorkload(id)
	if err != nil {
		return err
	}
	if err := validateLocalExecutorProgramSupport(program); err != nil {
		return err
	}
	return errors.New("local executor resolved zero command steps")
}

// runLocalExecutorStepSet 在 strict sandbox 中运行已解析步骤，并拒绝零步骤假绿。
func runLocalExecutorStepSet(ctx context.Context, id GateID, steps []resolvedStep, environment []string, stdout, stderr io.Writer, sandboxPath, sandboxProfile string) error {
	if len(steps) == 0 {
		return localExecutorZeroStepError(id)
	}
	for _, step := range steps {
		if err := runSandboxedResolvedStep(ctx, step, environment, stdout, stderr, sandboxPath, sandboxProfile); err != nil {
			return err
		}
	}
	return nil
}

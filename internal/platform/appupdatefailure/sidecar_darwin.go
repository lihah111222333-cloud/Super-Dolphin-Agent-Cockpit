//go:build darwin

package appupdatefailure

import "github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"

// Begin 发布新安装代际的 pending 状态，并使任何旧代际失效。
func Begin(stageDir string, generation string) error {
	if err := validateGeneration(generation); err != nil {
		return err
	}
	return withLockedStageDir(stageDir, func(dir *lockedStageDir) error {
		return dir.writeRecord(pendingRecord(generation))
	})
}

// Fail 仅把 matching generation 的现存状态推进为 failure。
func Fail(stageDir string, expectedGeneration string, failure contract.RecoveryFailure) error {
	if err := validateGeneration(expectedGeneration); err != nil {
		return err
	}
	if err := validateFailure(failure); err != nil {
		return err
	}
	return withLockedStageDir(stageDir, func(dir *lockedStageDir) error {
		current, exists, err := dir.readRecord()
		if err != nil {
			return err
		}
		if !exists || current.Generation != expectedGeneration {
			return errGenerationMissing
		}
		return dir.writeRecord(failureRecord(expectedGeneration, failure))
	})
}

// ReadFailure 只暴露 failure 状态；pending 对用户不可见。
func ReadFailure(stageDir string) (failure contract.RecoveryFailure, exists bool, err error) {
	err = withLockedStageDir(stageDir, func(dir *lockedStageDir) error {
		current, present, readErr := dir.readRecord()
		if readErr != nil || !present {
			return readErr
		}
		if current.State == statePending {
			return nil
		}
		failure = contract.RecoveryFailure{Code: current.Code, Retryable: current.Retryable, Action: contract.RecoveryAction(current.Action), TransactionID: current.TransactionID}
		exists = true
		return nil
	})
	if err != nil {
		return contract.RecoveryFailure{}, false, err
	}
	return failure, exists, nil
}

// Clear 仅删除 matching generation，阻止旧 helper 清理新安装尝试。
func Clear(stageDir string, expectedGeneration string) error {
	if err := validateGeneration(expectedGeneration); err != nil {
		return err
	}
	return withLockedStageDir(stageDir, func(dir *lockedStageDir) error {
		current, exists, err := dir.readRecord()
		if err != nil {
			return err
		}
		if !exists || current.Generation != expectedGeneration {
			return errGenerationMissing
		}
		return dir.removeRecord()
	})
}

// InvalidateAll 在显式 retry 边界使任意旧代际失效。
func InvalidateAll(stageDir string) error {
	return withLockedStageDir(stageDir, func(dir *lockedStageDir) error {
		return dir.removeRecord()
	})
}

package gate

import (
	"errors"
	"fmt"
	"time"
)

// requireCompiledSelectorBatchResult 要求精确 Go test selector 只能消费其批处理结果。
// 缺失或不一致时直接失败，禁止回退到共享 binary 的单 selector 执行。
func requireCompiledSelectorBatchResult(id GateID, batchedResults map[GateID]PlanGateExecution, executions []CompileGroupExecution) (PlanGateExecution, error) {
	result, ok := batchedResults[id]
	if !ok {
		err := fmt.Errorf("compile-group selector %q has no batched result", id)
		result, profileErr := failedCompileGroupSelectorWithDiagnostic(id, executions, err, nil)
		return result, errors.Join(err, profileErr)
	}
	if err := validateCompiledSelectorBatchResult(id, result); err != nil {
		failed, profileErr := failedCompileGroupSelectorWithDiagnostic(id, executions, err, result.Log)
		return failed, errors.Join(err, profileErr)
	}
	return result, nil
}

func validateCompiledSelectorBatchResult(id GateID, result PlanGateExecution) error {
	if err := validateCompiledSelectorBatchIdentityAndInterval(id, result); err != nil {
		return err
	}
	if err := result.ExecutionProfile.Validate(); err != nil {
		return fmt.Errorf("compile-group selector %q batched profile: %w", id, err)
	}
	if err := validateCompiledSelectorTiming(id, result.TestTimings); err != nil {
		return fmt.Errorf("compile-group selector %q batched terminal timing: %w", id, err)
	}
	if err := validateCompiledSelectorBatchProfile(id, result); err != nil {
		return err
	}
	return nil
}

func validateCompiledSelectorBatchIdentityAndInterval(id GateID, result PlanGateExecution) error {
	if result.GateID != id {
		return fmt.Errorf("compile-group selector %q batched result identity is %q", id, result.GateID)
	}
	if result.Status == ResultStatusCancelled || result.StartedAt.IsZero() || result.CompletedAt.IsZero() || !result.CompletedAt.After(result.StartedAt) {
		return fmt.Errorf("compile-group selector %q batched result interval is invalid", id)
	}
	return nil
}

func validateCompiledSelectorBatchProfile(id GateID, result PlanGateExecution) error {
	matched := exactGoTestTimings(result.TestTimings, mustGoTestName(id))
	if len(matched) != 1 || result.ExecutionProfile.TestBodyMS != matched[0].DurationMS {
		return fmt.Errorf("compile-group selector %q batched profile body does not match terminal timing", id)
	}
	if result.ExecutionProfile.TotalMS != result.ExecutionProfile.StartupMS+result.ExecutionProfile.TestBodyMS {
		return fmt.Errorf("compile-group selector %q batched profile total is not startup plus body", id)
	}
	_, _, totalMS, err := CanonicalExecutionInterval(result.StartedAt, result.CompletedAt)
	if err != nil || totalMS != result.ExecutionProfile.TotalMS {
		return fmt.Errorf("compile-group selector %q batched profile interval does not match timestamps", id)
	}
	return nil
}

func mustGoTestName(id GateID) string {
	_, _, target, targeted, err := parseTargetWorkloadID(string(id))
	if err != nil || !targeted {
		return ""
	}
	targetSpec, err := ParseGoTestTarget(target)
	if err != nil {
		return ""
	}
	return targetSpec.Name
}

func failedCompileGroupSelectorWithDiagnostic(id GateID, executions []CompileGroupExecution, err error, existingLog []byte) (PlanGateExecution, error) {
	result, profileErr := failedCompileGroupSelector(id, executions, time.Now)
	if profileErr != nil {
		return PlanGateExecution{}, profileErr
	}
	if len(existingLog) != 0 {
		result.Log = append(append([]byte(nil), existingLog...), '\n')
	}
	result.Log = fmt.Appendf(result.Log, "[gate-executor] selector=%s status=failed reason=%s\n", id, compactCompileGroupError(err))
	result.LogDigest = digestPlanLog(result.Log)
	return result, nil
}

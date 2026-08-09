package gate

import (
	"errors"
	"fmt"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func failedCompiledSelectorBatchResults(group CompileGroup, argv []string, observation *compiledSelectorBatchObservation, err error, nowFunc func() time.Time) (map[GateID]PlanGateExecution, error) {
	results := make(map[GateID]PlanGateExecution, len(group.WorkloadIDs))
	var resultErr error
	for _, id := range group.WorkloadIDs {
		if observation == nil {
			if nowFunc == nil {
				return results, errors.New("compiled selector batch failure clock is required")
			}
			now := nowFunc().UTC()
			observation = &compiledSelectorBatchObservation{started: now, completed: now.Add(cicontract.TimingResolution), log: newBoundedPlanLog(executorPlanMaxLogBytes)}
		}
		selectorName := ""
		if parsed, parseErr := selectorSpecForWorkload(id, group.PackageTarget); parseErr == nil {
			selectorName = parsed.testName
		}
		result, profileErr := failedCompiledSelectorBatchResult(id, argv, *observation, selectorName, err, len(results) == 0)
		if profileErr != nil {
			resultErr = errors.Join(resultErr, profileErr)
			continue
		}
		results[id] = result
	}
	return results, resultErr
}

// failedCompiledSelectorBatchResult 保留 test2json 已观测的 selector 终态，
// 并把没有终态事件的 companion 标记为 cancelled。进程区间只证明 batch，
// 不能证明每个 companion 都运行了整个进程生命周期。
func failedCompiledSelectorBatchResult(id GateID, argv []string, observation compiledSelectorBatchObservation, selectorName string, batchErr error, fullDiagnostic bool) (PlanGateExecution, error) {
	timings := observation.selectorTimings[selectorName]
	matched := exactGoTestTimings(timings, selectorName)
	interval := observation.selectorIntervals[selectorName]
	if len(matched) == 1 && !interval.runAt.IsZero() && !interval.completedAt.IsZero() {
		// 已观测到 PASS 只能证明 selector 的测试终态；batch 进程、解析、
		// 上下文或清理错误仍使该 selector 的执行证据不可接受，不能把
		// batchErr 丢在 `_ error` 中后继续投影为 PASS。
		selectorErr := batchErr
		if matched[0].Status == GoTestStatusFail {
			selectorErr = errors.Join(selectorErr, fmt.Errorf("compiled selector %q reported fail", selectorName))
		}
		result, profileErr := compiledSelectorResultWithLog(id, argv, observation, selectorName, timings, interval, selectorErr, fullDiagnostic)
		if profileErr != nil {
			return PlanGateExecution{}, profileErr
		}
		if batchErr != nil {
			result.Log = appendCompiledSelectorBatchError(result.Log, compiledSelectorBatchErrorSummary(observation, batchErr))
			result.LogDigest = digestPlanLog(result.Log)
		}
		return result, nil
	}
	return cancelledCompiledSelectorResult(id, argv, observation, selectorName, fullDiagnostic)
}

// cancelledCompiledSelectorResult 记录没有终态事件的 selector；它在进程
// 完成时建立零长度投影，不复制进程耗时，也不虚构测试正文耗时。
func cancelledCompiledSelectorResult(id GateID, argv []string, observation compiledSelectorBatchObservation, selectorName string, fullDiagnostic bool) (PlanGateExecution, error) {
	completed := observation.completed.UTC().Truncate(cicontract.TimingResolution)
	if completed.IsZero() {
		return PlanGateExecution{}, errors.New("compiled selector cancellation completion time is required")
	}
	log := newBoundedPlanLog(executorPlanMaxLogBytes)
	data := observation.selectorLogs[selectorName]
	if len(data) == 0 && fullDiagnostic && observation.log != nil {
		data = observation.log.Bytes()
	}
	if len(data) != 0 {
		_, _ = log.Write(compiledSelectorDiagnosticLog(data, errors.New("selector not executed"), fullDiagnostic))
	}
	_, _ = fmt.Fprintf(log, "[gate-executor] selector=%s status=cancelled reason=no-terminal-test2json-result\n", selectorName)
	profile, err := measuredExecutionProfileForWorkload(id)
	if err != nil {
		return PlanGateExecution{}, err
	}
	result := PlanGateExecution{
		GateID: id, Status: ResultStatusCancelled, ExitCode: -1,
		StartedAt: completed, CompletedAt: completed, ArgvDigest: digestCommandArgv(argv),
		Log: log.Bytes(), LogDigest: digestPlanLog(log.Bytes()), TestTimings: nil,
		ExecutionProfile: profile,
	}
	return result, nil
}

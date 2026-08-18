package gate

import (
	"errors"
	"fmt"
)

// compiledSelectorBatchResults 将每个顶层 selector 生成独立的 canonical 计划结果。
func compiledSelectorBatchResults(group CompileGroup, argv []string, specs map[GateID]compiledSelectorBatchSpec, observation compiledSelectorBatchObservation) (map[GateID]PlanGateExecution, error) {
	results := make(map[GateID]PlanGateExecution, len(specs))
	var resultErr error
	fullFailureLogAvailable := true
	for _, id := range group.WorkloadIDs {
		spec := specs[id]
		timings := observation.selectorTimings[spec.name]
		matched := exactGoTestTimings(timings, spec.name)
		if len(matched) != 1 {
			selectorErr := fmt.Errorf("compiled selector batch test %q has no unique terminal result", spec.name)
			cancelled, profileErr := cancelledCompiledSelectorResult(id, argv, observation, spec.name, fullFailureLogAvailable)
			if profileErr != nil {
				return results, errors.Join(resultErr, profileErr)
			}
			results[id] = cancelled
			fullFailureLogAvailable = false
			resultErr = errors.Join(resultErr, selectorErr)
			continue
		}
		interval := observation.selectorIntervals[spec.name]
		selectorErr := error(nil)
		if matched[0].Status == GoTestStatusFail {
			selectorErr = fmt.Errorf("compiled selector %q reported fail", spec.name)
		}
		fullFailureLog := selectorErr != nil && fullFailureLogAvailable
		result, profileErr := compiledSelectorResultWithLog(id, argv, observation, spec.name, timings, interval, selectorErr, fullFailureLog)
		if profileErr != nil {
			return results, errors.Join(resultErr, profileErr)
		}
		if fullFailureLog {
			fullFailureLogAvailable = false
		}
		if timingErr := validateCompiledSelectorTiming(id, timings); timingErr != nil {
			result.Status, result.ExitCode = ResultStatusFailed, ExecutorExitCode(timingErr)
			selectorErr = errors.Join(selectorErr, timingErr)
		}
		if result.Status == ResultStatusFailed {
			resultErr = errors.Join(resultErr, selectorErr, fmt.Errorf("compiled selector %q failed", id))
		}
		results[id] = result
	}
	return results, resultErr
}

// compiledSelectorResultWithLog 绑定 selector 专属日志并还原其独立启动、正文和总区间。
func compiledSelectorResultWithLog(id GateID, argv []string, observation compiledSelectorBatchObservation, selectorName string, timings []GoTestTiming, interval compiledSelectorBatchInterval, selectorErr error, fullDiagnostic bool) (PlanGateExecution, error) {
	log := newBoundedPlanLog(executorPlanMaxLogBytes)
	data := observation.selectorLogs[selectorName]
	if len(data) == 0 && observation.log != nil {
		data = observation.log.Bytes()
	}
	if len(data) != 0 {
		_, _ = log.Write(compiledSelectorDiagnosticLog(data, selectorErr, fullDiagnostic))
	}
	compiledObservation := compiledSelectorObservation{
		started: observation.started, bodyStarted: observation.bodyStarted, completed: observation.completed,
		log: log, timings: timings, argv: argv, runErr: selectorErr, contextErr: observation.contextErr,
	}
	if interval.runAt.IsZero() || interval.completedAt.IsZero() {
		return compiledSelectorResult(id, argv, compiledObservation)
	}
	started, bodyStarted, completed, intervalErr := canonicalCompiledSelectorBatchInterval(observation.started, interval)
	if intervalErr != nil {
		compiledObservation.runErr = errors.Join(compiledObservation.runErr, intervalErr)
	} else {
		completed = normalizeCompiledSelectorBatchCompletion(bodyStarted, completed, timings, selectorName)
		compiledObservation.started, compiledObservation.bodyStarted, compiledObservation.completed = started, bodyStarted, completed
	}
	result, profileErr := compiledSelectorResult(id, argv, compiledObservation)
	if profileErr != nil {
		return PlanGateExecution{}, profileErr
	}
	if intervalErr == nil {
		canonical, err := CanonicalizePlanGateExecutionTiming(result)
		if err != nil {
			return result, err
		}
		result = canonical
	}
	attachBatchResourceObservation(&result, observation.resourceUsage)
	return result, nil
}

func attachBatchResourceObservation(result *PlanGateExecution, observation *BatchResourceObservation) {
	if observation == nil {
		return
	}
	usage := *observation
	result.ExecutionProfile.BatchResource = &usage
}

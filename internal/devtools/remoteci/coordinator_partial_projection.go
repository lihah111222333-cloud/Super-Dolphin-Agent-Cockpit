package remoteci

import (
	"errors"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// canonicalPartialFreshAndReusedWorkloadExecutions 持久化失败边界上已严格校验的
// fresh 与 PASS origin 执行证据。缺失的 fresh workload 不阻断其它 workload 的
// 投影；未知 reused identity 仍 fail-fast，避免 SQLite 丢失可复用证据或写入越界条目。
func canonicalPartialFreshAndReusedWorkloadExecutions(
	workloads []gate.Workload,
	partialFresh []gate.PlanGateExecution,
	reused map[string]gate.WorkloadPassEvidence,
) ([]gate.PlanGateExecution, error) {
	expected := partialWorkloadIDSet(workloads)
	freshByID, err := indexPartialFreshWorkloadExecutions(expected, partialFresh)
	if err != nil {
		return nil, err
	}
	projected := make([]gate.PlanGateExecution, 0, len(partialFresh)+len(reused))
	var projectionErr error
	for _, workload := range workloads {
		workloadID := workload.ID
		expected[workloadID] = struct{}{}
		if execution, ok := freshByID[workloadID]; ok {
			projected = append(projected, execution)
			continue
		}
		evidence, ok := reused[workloadID]
		if !ok {
			continue
		}
		canonical, err := gate.CanonicalizePlanGateExecutionTiming(evidence.OriginExecution)
		if err != nil {
			projectionErr = errors.Join(projectionErr, fmt.Errorf("remote reused workload %q timing: %w", workloadID, err))
			continue
		}
		if canonical.GateID != gate.GateID(workloadID) {
			projectionErr = errors.Join(projectionErr, fmt.Errorf("remote reused workload %q identity does not match origin execution %q", workloadID, canonical.GateID))
			continue
		}
		projected = append(projected, canonical)
	}
	for workloadID := range reused {
		if _, ok := expected[workloadID]; !ok {
			projectionErr = errors.Join(projectionErr, fmt.Errorf("remote reused workload %q is outside the current catalog", workloadID))
		}
	}
	return projected, projectionErr
}

// partialWorkloadIDSet 建立当前执行 catalog 的严格 workload identity 集合。
func partialWorkloadIDSet(workloads []gate.Workload) map[string]struct{} {
	expected := make(map[string]struct{}, len(workloads))
	for _, workload := range workloads {
		expected[workload.ID] = struct{}{}
	}
	return expected
}

// indexPartialFreshWorkloadExecutions 校验 fresh projection 的重复与越界 identity。
func indexPartialFreshWorkloadExecutions(
	expected map[string]struct{},
	partialFresh []gate.PlanGateExecution,
) (map[string]gate.PlanGateExecution, error) {
	freshByID := make(map[string]gate.PlanGateExecution, len(partialFresh))
	for _, execution := range partialFresh {
		workloadID := string(execution.GateID)
		if _, duplicate := freshByID[workloadID]; duplicate {
			return nil, fmt.Errorf("remote workload %q is duplicated in partial fresh projection", execution.GateID)
		}
		if _, ok := expected[workloadID]; !ok {
			return nil, fmt.Errorf("remote fresh workload %q is outside the current catalog", execution.GateID)
		}
		freshByID[workloadID] = execution
	}
	return freshByID, nil
}

// canonicalPartialRemoteWorkloadExecutions 保留已严格校验的 workload，并把缺失或失真的条目标记为错误。
func canonicalPartialRemoteWorkloadExecutions(
	workloads []gate.Workload,
	fresh map[string]gate.PlanGateExecution,
) ([]gate.PlanGateExecution, error) {
	if len(fresh) == 0 {
		return nil, nil
	}
	expected := make(map[string]struct{}, len(workloads))
	partial := make([]gate.PlanGateExecution, 0, len(fresh))
	var partialErr error
	for _, workload := range workloads {
		expected[workload.ID] = struct{}{}
		execution, ok := fresh[workload.ID]
		if !ok {
			continue
		}
		canonical, err := gate.CanonicalizePlanGateExecutionTiming(execution)
		if err != nil {
			partialErr = errors.Join(partialErr, fmt.Errorf("remote workload %q timing: %w", workload.ID, err))
			continue
		}
		partial = append(partial, canonical)
	}
	for workloadID := range fresh {
		if _, ok := expected[workloadID]; !ok {
			partialErr = errors.Join(partialErr, fmt.Errorf("remote workload %q is outside the current catalog", workloadID))
		}
	}
	return partial, partialErr
}

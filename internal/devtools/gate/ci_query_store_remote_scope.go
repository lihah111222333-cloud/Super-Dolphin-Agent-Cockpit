package gate

import (
	"errors"
	"fmt"
)

// resolveRemoteCIRunExecutionScope 将 legacy 空 scope 规范化为完整目录 scope，其他 scope 必须绑定当前 catalog。
func resolveRemoteCIRunExecutionScope(scope *RemoteCIExecutionScope, catalog WorkloadCatalog) (RemoteCIExecutionScope, error) {
	if scope == nil {
		return NewRemoteCIFullExecutionScope(catalog)
	}
	if err := scope.ValidateAgainstCatalog(catalog); err != nil {
		return RemoteCIExecutionScope{}, fmt.Errorf("validate remote CI execution scope: %w", err)
	}
	return *scope, nil
}

// validateRemoteCIRunScopeRecords 拒绝 subset 外的 workload 或 aggregate gate 记录。
func validateRemoteCIRunScopeRecords(record RemoteCIRunRecord, _ WorkloadCatalog, scope RemoteCIExecutionScope) error {
	if scope.IsFull() {
		return nil
	}
	selected := remoteCIRunScopeSelectedWorkloads(scope)
	if err := validateRemoteCIRunScopeShardRecords(record.Shards, selected); err != nil {
		return err
	}
	if err := validateRemoteCIRunScopeWorkloadResults(record.WorkloadResults, selected); err != nil {
		return err
	}
	if err := validateRemoteCIRunScopeWorkloadExecutions(record.WorkloadExecutions, selected); err != nil {
		return err
	}
	return validateRemoteCIRunScopeAggregateExecutions(record.Executions, selected)
}

// validateRemoteCIRunScopeRecordCoverage binds every loaded run projection to
// its resolved full, subset, or legacy execution scope.
func validateRemoteCIRunScopeRecordCoverage(record RemoteCIRunRecord, catalog WorkloadCatalog, index remoteCIRunCatalogIndex) error {
	scope, err := resolveRemoteCIRunExecutionScope(record.Scope, catalog)
	if err != nil {
		return err
	}
	if err := validateRemoteCIRunScopeRecords(record, catalog, scope); err != nil {
		return err
	}
	if record.Status != ResultStatusPassed {
		return nil
	}
	return index.validatePassed(record, scope)
}

func remoteCIRunScopeSelectedWorkloads(scope RemoteCIExecutionScope) map[GateID]struct{} {
	selected := make(map[GateID]struct{}, len(scope.selectedGateIDs))
	for _, workloadID := range scope.selectedGateIDs {
		selected[workloadID] = struct{}{}
	}
	return selected
}

// validateRemoteCIRunScopeShardRecords 确认 subset scope 未投影范围外的 shard workload。
func validateRemoteCIRunScopeShardRecords(shards []RemoteCIShardRecord, selected map[GateID]struct{}) error {
	for _, shard := range shards {
		for _, workloadID := range shard.Workloads {
			if _, ok := selected[workloadID]; !ok {
				return fmt.Errorf("remote CI subset scope contains extra shard workload %q", workloadID)
			}
		}
	}
	return nil
}

// validateRemoteCIRunScopeWorkloadResults 确认 subset scope 未投影范围外的结果。
func validateRemoteCIRunScopeWorkloadResults(results []RemoteCIWorkloadResult, selected map[GateID]struct{}) error {
	for _, result := range results {
		if _, ok := selected[result.Identity.WorkloadID]; !ok {
			return fmt.Errorf("remote CI subset scope contains extra workload result %q", result.Identity.WorkloadID)
		}
	}
	return nil
}

// validateRemoteCIRunScopeWorkloadExecutions 确认 subset scope 未投影范围外的 workload execution。
func validateRemoteCIRunScopeWorkloadExecutions(executions []PlanGateExecution, selected map[GateID]struct{}) error {
	for _, execution := range executions {
		if _, ok := selected[execution.GateID]; !ok {
			return fmt.Errorf("remote CI subset scope contains extra workload execution %q", execution.GateID)
		}
	}
	return nil
}

// validateRemoteCIRunScopeAggregateExecutions 确认 aggregate gate 有对应的 subset workload。
func validateRemoteCIRunScopeAggregateExecutions(executions []PlanGateExecution, selected map[GateID]struct{}) error {
	for _, execution := range executions {
		if err := validateRemoteCIRunScopeAggregateGate(execution.GateID, selected); err != nil {
			return err
		}
	}
	return nil
}

// validateRemoteCIRunScopeAggregateGate 拒绝 release owner 或无对应 workload 的 aggregate gate。
func validateRemoteCIRunScopeAggregateGate(gateID GateID, selected map[GateID]struct{}) error {
	if gateID == GateIDReleaseLayeredCheck {
		return errors.New("remote CI subset scope must not contain release owner attestation")
	}
	parent, err := WorkloadParentGateID(string(gateID))
	if err != nil {
		return fmt.Errorf("resolve remote CI subset aggregate gate %q: %w", gateID, err)
	}
	found, err := remoteCIRunScopeContainsParentWorkload(selected, parent)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("remote CI subset scope contains extra aggregate gate %q", gateID)
	}
	return nil
}

// remoteCIRunScopeContainsParentWorkload 查找指定 aggregate gate 的任一 selected workload。
func remoteCIRunScopeContainsParentWorkload(selected map[GateID]struct{}, parent GateID) (bool, error) {
	for workloadID := range selected {
		selectedParent, err := WorkloadParentGateID(string(workloadID))
		if err != nil {
			return false, err
		}
		if selectedParent == parent {
			return true, nil
		}
	}
	return false, nil
}

// expectedRemoteCIShardableWorkloads 生成本次 full 或 subset scope 的精确结果覆盖集合。
func expectedRemoteCIShardableWorkloads(shardable map[GateID]struct{}, scope RemoteCIExecutionScope) (map[GateID]struct{}, error) {
	expected := make(map[GateID]struct{}, len(shardable))
	if scope.IsFull() {
		for workloadID := range shardable {
			expected[workloadID] = struct{}{}
		}
		return expected, nil
	}
	for _, workloadID := range scope.selectedGateIDs {
		if _, ok := shardable[workloadID]; !ok {
			return nil, fmt.Errorf("remote CI subset scope workload %q is not shardable", workloadID)
		}
		expected[workloadID] = struct{}{}
	}
	return expected, nil
}

// passedRemoteCIWorkloadResults 验证结果精确覆盖可分片目录，并提取本次必须 fresh 执行的 workload。
func passedRemoteCIWorkloadResults(workloadResults []RemoteCIWorkloadResult, expected map[GateID]struct{}) (map[GateID]string, map[GateID]struct{}, error) {
	results := make(map[GateID]string, len(workloadResults))
	executed := make(map[GateID]struct{})
	for _, result := range workloadResults {
		workloadID := result.Identity.WorkloadID
		if _, exists := expected[workloadID]; !exists {
			return nil, nil, fmt.Errorf("passed remote CI workload result %q is absent from its shardable catalog", workloadID)
		}
		if _, duplicate := results[workloadID]; duplicate {
			return nil, nil, fmt.Errorf("passed remote CI workload result %q is duplicated", workloadID)
		}
		results[workloadID] = result.Disposition
		if result.Disposition == WorkloadDispositionExecuted {
			executed[workloadID] = struct{}{}
		}
	}
	for workloadID := range expected {
		if _, exists := results[workloadID]; !exists {
			return nil, nil, fmt.Errorf("passed remote CI run does not cover shardable workload result %q", workloadID)
		}
	}
	return results, executed, nil
}

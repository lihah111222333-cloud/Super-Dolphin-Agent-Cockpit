package main

import (
	"fmt"
	"slices"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// remoteRunContractExecutionCatalog 投影回执必须覆盖的目录；nil scope 保留
// legacy/full SQLite 形态，显式 subset 必须相对完整持久化目录有效。
func remoteRunContractExecutionCatalog(catalog gatecontract.WorkloadCatalog, scope *gatecontract.RemoteCIExecutionScope) (gatecontract.WorkloadCatalog, error) {
	return gatecontract.ProjectRemoteCIExecutionCatalog(catalog, scope)
}

// validateRemoteRunRecordedExecutionScope 把持久化 subset proof 绑定到结果；
// full scope 不得合成 side-table 行。
func validateRemoteRunRecordedExecutionScope(catalog gatecontract.WorkloadCatalog, recorded, result *gatecontract.RemoteCIExecutionScope) error {
	if result == nil {
		if recorded != nil {
			return fmt.Errorf("remote CI full receipt has an unexpected persisted execution scope")
		}
		return nil
	}
	if err := result.ValidateAgainstCatalog(catalog); err != nil {
		return fmt.Errorf("validate remote CI result execution scope: %w", err)
	}
	if result.IsFull() {
		if recorded != nil {
			return fmt.Errorf("remote CI full receipt has a persisted execution scope")
		}
		return nil
	}
	if recorded == nil {
		return fmt.Errorf("remote CI subset receipt execution scope is missing")
	}
	if err := recorded.ValidateAgainstCatalog(catalog); err != nil {
		return fmt.Errorf("validate recorded remote CI execution scope: %w", err)
	}
	if !recorded.IsSubset() || !slices.Equal(recorded.SelectedGateIDs(), result.SelectedGateIDs()) {
		return fmt.Errorf("recorded remote CI execution scope does not match result")
	}
	return nil
}

// validateRemoteRunOwnerExecutionSet 为 nil/full scope 保留 legacy release-owner
// 形态；subset 仅按完整目录父级顺序携带所选 shardable workload 的规范聚合。
func validateRemoteRunOwnerExecutionSet(
	catalog gatecontract.WorkloadCatalog,
	scope *gatecontract.RemoteCIExecutionScope,
	executions []gatecontract.PlanGateExecution,
) error {
	if scope == nil || scope.IsFull() {
		return nil
	}
	if !scope.IsSubset() {
		return fmt.Errorf("remote CI execution scope kind is unsupported")
	}
	if err := scope.ValidateAgainstCatalog(catalog); err != nil {
		return fmt.Errorf("validate remote CI subset execution scope: %w", err)
	}
	expected, err := remoteRunSubsetParentGateIDs(catalog, scope)
	if err != nil {
		return err
	}
	if len(executions) != len(expected) {
		return fmt.Errorf("remote CI subset parent aggregate count = %d, want %d", len(executions), len(expected))
	}
	for index, execution := range executions {
		if execution.GateID != expected[index] {
			return fmt.Errorf("remote CI subset parent aggregate %d = %q, want %q", index, execution.GateID, expected[index])
		}
	}
	return nil
}

// remoteRunSubsetParentGateIDs 按完整目录规范顺序派生唯一父 gate，拒绝使用请求或结果顺序。
func remoteRunSubsetParentGateIDs(
	catalog gatecontract.WorkloadCatalog,
	scope *gatecontract.RemoteCIExecutionScope,
) ([]gatecontract.GateID, error) {
	selected := make(map[gatecontract.GateID]struct{}, len(scope.SelectedGateIDs()))
	for _, workloadID := range scope.SelectedGateIDs() {
		selected[workloadID] = struct{}{}
	}
	parents := make([]gatecontract.GateID, 0, len(selected))
	seen := make(map[gatecontract.GateID]struct{}, len(selected))
	for _, workload := range catalog.Workloads {
		if !workload.Shardable {
			continue
		}
		if _, selected := selected[gatecontract.GateID(workload.ID)]; !selected {
			continue
		}
		parent, err := gatecontract.WorkloadParentGateID(workload.ID)
		if err != nil {
			return nil, fmt.Errorf("parse selected remote CI workload %q parent: %w", workload.ID, err)
		}
		if _, duplicate := seen[parent]; duplicate {
			continue
		}
		seen[parent] = struct{}{}
		parents = append(parents, parent)
	}
	if len(parents) != len(seen) {
		return nil, fmt.Errorf("remote CI subset parent aggregate projection is inconsistent")
	}
	return parents, nil
}

// remoteRunExpectedWorkloadResults 生成本次 fresh/reused 组合应持久化的完整工作负载结果。
func remoteRunExpectedWorkloadResults(result remoteci.RunResult) (map[gatecontract.GateID]gatecontract.RemoteCIWorkloadResult, error) {
	canonical, err := remoteci.CanonicalRemoteCIWorkloadResults(result)
	if err != nil {
		return nil, err
	}
	want := make(map[gatecontract.GateID]gatecontract.RemoteCIWorkloadResult, len(canonical))
	for _, workloadResult := range canonical {
		if _, duplicate := want[workloadResult.Identity.WorkloadID]; duplicate {
			return nil, fmt.Errorf("canonical remote CI workload result %q is duplicated", workloadResult.Identity.WorkloadID)
		}
		want[workloadResult.Identity.WorkloadID] = workloadResult
	}
	return want, nil
}

// remoteRunReexecutedResultMap 校验 package 原子重跑的旧 proof 投影无重复。
func remoteRunReexecutedResultMap(results []gatecontract.RemoteCIWorkloadResult) (map[gatecontract.GateID]gatecontract.RemoteCIWorkloadResult, error) {
	indexed := make(map[gatecontract.GateID]gatecontract.RemoteCIWorkloadResult, len(results))
	for _, result := range results {
		if err := result.Validate(); err != nil {
			return nil, fmt.Errorf("validate package-atomic remote CI workload %q proof result: %w", result.Identity.WorkloadID, err)
		}
		if result.Disposition != gatecontract.WorkloadDispositionReused {
			return nil, fmt.Errorf("package-atomic remote CI workload %q proof disposition is invalid", result.Identity.WorkloadID)
		}
		if _, duplicate := indexed[result.Identity.WorkloadID]; duplicate {
			return nil, fmt.Errorf("package-atomic remote CI workload %q proof is duplicated", result.Identity.WorkloadID)
		}
		indexed[result.Identity.WorkloadID] = result
	}
	return indexed, nil
}

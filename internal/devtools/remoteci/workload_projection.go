package remoteci

import (
	"errors"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func remoteShardableWorkloads(catalog gate.WorkloadCatalog) []gate.Workload {
	workloads := make([]gate.Workload, 0, len(catalog.Workloads))
	for _, workload := range catalog.Workloads {
		if workload.Shardable {
			workloads = append(workloads, workload)
		}
	}
	return workloads
}

// remoteExecutionCatalog 从已验证的完整 catalog 计划派生 worker 输入，而不创建新的权威目录。
func remoteExecutionCatalog(plan gate.WorkloadExecutionPlan) gate.WorkloadCatalog {
	selected := make(map[gate.GateID]struct{}, len(plan.ExecutionWorkloadIDs))
	for _, id := range plan.ExecutionWorkloadIDs {
		selected[id] = struct{}{}
	}
	catalog := gate.WorkloadCatalog{Version: plan.Catalog.Version, Authoritative: false}
	for _, workload := range plan.Catalog.Workloads {
		if _, ok := selected[gate.GateID(workload.ID)]; ok {
			catalog.Workloads = append(catalog.Workloads, workload)
		}
	}
	return catalog
}

func remoteExecutionWorkloads(plan gate.WorkloadExecutionPlan) []gate.Workload {
	return remoteExecutionCatalog(plan).Workloads
}

// remoteFreshWorkloadExecutions 汇总新分片结果并拒绝重复执行同一 workload。
func remoteFreshWorkloadExecutions(workloads []gate.Workload, results []ShardResult) (map[string]gate.PlanGateExecution, error) {
	catalog := make(map[string]gate.Workload, len(workloads))
	for _, workload := range workloads {
		catalog[workload.ID] = workload
	}
	executions := make(map[string]gate.PlanGateExecution)
	for _, result := range results {
		if result.ShardIdentity == "" {
			return nil, errors.New("remote workload result shard identity is required")
		}
		if result.Report.SchemaVersion != gate.ExecutorPlanReportSchemaVersion {
			return nil, errors.New("remote workload result report schema is unsupported")
		}
		for _, execution := range result.Report.Gates {
			workloadID, execution, err := projectRemoteFreshWorkloadExecution(catalog, result.ShardIdentity, execution)
			if err != nil {
				return nil, err
			}
			if _, duplicate := executions[workloadID]; duplicate {
				return nil, fmt.Errorf("remote workload %q was executed more than once", workloadID)
			}
			executions[workloadID] = execution
		}
	}
	return executions, nil
}

// projectRemoteFreshWorkloadExecution 绑定分片身份并校验目录成员及当前结构化 profile。
func projectRemoteFreshWorkloadExecution(
	catalog map[string]gate.Workload,
	shardIdentity string,
	execution gate.PlanGateExecution,
) (string, gate.PlanGateExecution, error) {
	if execution.ShardIdentity != "" && execution.ShardIdentity != shardIdentity {
		return "", gate.PlanGateExecution{}, fmt.Errorf("remote workload %q reported shard identity %q, want %q", execution.GateID, execution.ShardIdentity, shardIdentity)
	}
	execution.ShardIdentity = shardIdentity
	workloadID := string(execution.GateID)
	_, ok := catalog[workloadID]
	if !ok {
		return "", gate.PlanGateExecution{}, fmt.Errorf("remote workload result %q is outside the current catalog", workloadID)
	}
	if err := execution.ExecutionProfile.Validate(); err != nil {
		return "", gate.PlanGateExecution{}, fmt.Errorf("remote workload %q execution profile: %w", workloadID, err)
	}
	return workloadID, execution, nil
}

// collectFreshRemoteWorkloadExecutions 验证当前运行的全部 workload 均有新执行结果。
func collectFreshRemoteWorkloadExecutions(
	workloads []gate.Workload,
	fresh map[string]gate.PlanGateExecution,
) (map[string]gate.PlanGateExecution, error) {
	observed := make(map[string]gate.PlanGateExecution, len(workloads))
	for _, workload := range workloads {
		execution, ok := fresh[workload.ID]
		if !ok || execution.GateID != gate.GateID(workload.ID) {
			return nil, fmt.Errorf("remote workload %q has no matching result", workload.ID)
		}
		if err := execution.ExecutionProfile.Validate(); err != nil {
			return nil, fmt.Errorf("remote workload %q execution profile: %w", workload.ID, err)
		}
		observed[workload.ID] = execution
	}
	if len(observed) != len(fresh) {
		return nil, fmt.Errorf("remote workload results contain entries outside the current catalog")
	}
	return observed, nil
}

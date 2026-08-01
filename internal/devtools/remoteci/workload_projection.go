package remoteci

import (
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

// remoteCachedWorkloadIDs 按目录顺序校验并返回可复用 workload 标识。
func remoteCachedWorkloadIDs(
	workloads []gate.Workload,
	cached map[string]gate.PlanGateExecution,
) ([]gate.GateID, error) {
	ids := make([]gate.GateID, 0, len(cached))
	observed := make(map[string]struct{}, len(cached))
	for _, workload := range workloads {
		execution, ok := cached[workload.ID]
		if !ok {
			continue
		}
		if execution.GateID != gate.GateID(workload.ID) ||
			execution.Status != gate.ResultStatusPassed ||
			execution.ExitCode != 0 {
			return nil, fmt.Errorf("cached workload %q is not a passing observation", workload.ID)
		}
		ids = append(ids, execution.GateID)
		observed[workload.ID] = struct{}{}
	}
	if len(observed) != len(cached) {
		return nil, fmt.Errorf("passed workload cache contains an entry outside the current catalog")
	}
	return ids, nil
}

func remoteShardWorkloadIDs(shards []gate.ContainerShard) []gate.GateID {
	var ids []gate.GateID
	for _, shard := range shards {
		ids = append(ids, shard.GateIDs...)
	}
	return ids
}

// remoteFreshWorkloadExecutions 汇总新分片结果并拒绝重复执行同一 workload。
func remoteFreshWorkloadExecutions(results []ShardResult) (map[string]gate.PlanGateExecution, error) {
	executions := make(map[string]gate.PlanGateExecution)
	for _, result := range results {
		for _, execution := range result.Report.Gates {
			workloadID := string(execution.GateID)
			if _, duplicate := executions[workloadID]; duplicate {
				return nil, fmt.Errorf("remote workload %q was executed more than once", workloadID)
			}
			executions[workloadID] = execution
		}
	}
	return executions, nil
}

// mergeRemoteWorkloadExecutions 合并复用与新执行结果，并验证目录覆盖完整且互斥。
func mergeRemoteWorkloadExecutions(
	workloads []gate.Workload,
	cached map[string]gate.PlanGateExecution,
	fresh map[string]gate.PlanGateExecution,
) (map[string]gate.PlanGateExecution, error) {
	observed := make(map[string]gate.PlanGateExecution, len(workloads))
	for _, workload := range workloads {
		cachedExecution, cacheHit := cached[workload.ID]
		freshExecution, executed := fresh[workload.ID]
		if cacheHit && executed {
			return nil, fmt.Errorf("remote workload %q was both reused and executed", workload.ID)
		}
		execution, ok := cachedExecution, cacheHit
		if executed {
			execution, ok = freshExecution, true
		}
		if !ok || execution.GateID != gate.GateID(workload.ID) {
			return nil, fmt.Errorf("remote workload %q has no matching result", workload.ID)
		}
		observed[workload.ID] = execution
	}
	if len(observed) != len(cached)+len(fresh) {
		return nil, fmt.Errorf("remote workload results contain entries outside the current catalog")
	}
	return observed, nil
}

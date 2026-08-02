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
func remoteFreshWorkloadExecutions(workloads []gate.Workload, results []ShardResult) (map[string]gate.PlanGateExecution, error) {
	catalog := make(map[string]gate.Workload, len(workloads))
	for _, workload := range workloads {
		catalog[workload.ID] = workload
	}
	executions := make(map[string]gate.PlanGateExecution)
	for _, result := range results {
		for _, execution := range result.Report.Gates {
			workloadID := string(execution.GateID)
			if _, duplicate := executions[workloadID]; duplicate {
				return nil, fmt.Errorf("remote workload %q was executed more than once", workloadID)
			}
			workload, ok := catalog[workloadID]
			if !ok {
				return nil, fmt.Errorf("remote workload result %q is outside the current catalog", workloadID)
			}
			var err error
			execution, err = normalizeRemoteWorkloadExecutionProfile(workload, result.Report.SchemaVersion, execution)
			if err != nil {
				return nil, err
			}
			if err := execution.ExecutionProfile.Validate(); err != nil {
				return nil, fmt.Errorf("remote workload %q execution profile: %w", workloadID, err)
			}
			executions[workloadID] = execution
		}
	}
	return executions, nil
}

// normalizeRemoteWorkloadExecutionProfile makes an omitted profile explicit only where
// legacy reports or non-exact workloads could not have produced cache measurements.
func normalizeRemoteWorkloadExecutionProfile(
	workload gate.Workload,
	reportSchemaVersion uint32,
	execution gate.PlanGateExecution,
) (gate.PlanGateExecution, error) {
	if !zeroExecutionProfile(execution.ExecutionProfile) {
		return execution, nil
	}
	exact, err := remoteExactGoTestWorkload(workload)
	if err != nil {
		return gate.PlanGateExecution{}, err
	}
	if reportSchemaVersion >= 4 && exact {
		return execution, nil
	}
	execution.ExecutionProfile = gate.ExecutionProfile{
		CacheSource: "none", CacheStatus: "not_applicable", CacheMeasurement: "not_measured",
	}
	return execution, nil
}

func remoteExactGoTestWorkload(workload gate.Workload) (bool, error) {
	_, kind, _, targeted, err := gate.ParseWorkloadID(workload.ID)
	if err != nil {
		return false, err
	}
	return targeted && kind == gate.WorkloadTargetGoTest, nil
}

func zeroExecutionProfile(profile gate.ExecutionProfile) bool {
	return profile.CacheSource == "" &&
		profile.CacheStatus == "" &&
		profile.CacheMeasurement == "" &&
		profile.PrivateHitCount == 0 &&
		profile.BaselineHitCount == 0 &&
		len(profile.BaselineHitByGeneration) == 0 &&
		profile.CacheMissCount == 0 &&
		profile.CachePutCount == 0 &&
		profile.MaterializeMS == 0 &&
		profile.DownloadMS == 0 &&
		profile.VerifyMS == 0 &&
		profile.StartupMS == 0 &&
		profile.TestBodyMS == 0 &&
		profile.TotalMS == 0
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
		var err error
		execution, err = normalizeRemoteWorkloadExecutionProfile(workload, 1, execution)
		if err != nil {
			return nil, err
		}
		observed[workload.ID] = execution
	}
	if len(observed) != len(cached)+len(fresh) {
		return nil, fmt.Errorf("remote workload results contain entries outside the current catalog")
	}
	return observed, nil
}

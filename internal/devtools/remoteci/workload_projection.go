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
		if result.ShardIdentity == "" {
			return nil, errors.New("remote workload result shard identity is required")
		}
		for _, execution := range result.Report.Gates {
			if execution.ShardIdentity != "" && execution.ShardIdentity != result.ShardIdentity {
				return nil, fmt.Errorf("remote workload %q reported shard identity %q, want %q", execution.GateID, execution.ShardIdentity, result.ShardIdentity)
			}
			execution.ShardIdentity = result.ShardIdentity
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
		var err error
		execution, err = normalizeRemoteWorkloadExecutionProfile(workload, 1, execution)
		if err != nil {
			return nil, err
		}
		observed[workload.ID] = execution
	}
	if len(observed) != len(fresh) {
		return nil, fmt.Errorf("remote workload results contain entries outside the current catalog")
	}
	return observed, nil
}

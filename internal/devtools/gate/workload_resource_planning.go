package gate

import (
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// distributeTieredLPTWithinTarget 先隔离 2C、4C、8C workload，再在各档内部按 100 秒目标做 LPT。
func distributeTieredLPTWithinTarget(planned []PlannedWorkload, context PlanningContext) ([]ShardPlan, error) {
	tiers := make([][]PlannedWorkload, int(cicontract.WorkloadResourceTierSlow))
	for _, workload := range planned {
		tier, err := plannedWorkloadResourceTier(workload)
		if err != nil {
			return nil, fmt.Errorf("classify workload %q resource tier: %w", workload.Workload.ID, err)
		}
		tiers[int(tier)-1] = append(tiers[int(tier)-1], workload)
	}
	shards := make([]ShardPlan, 0, len(planned))
	for _, tierWorkloads := range tiers {
		if len(tierWorkloads) == 0 {
			continue
		}
		for _, shard := range distributeLPTWithinTarget(tierWorkloads, context) {
			shard.Index = len(shards)
			shards = append(shards, shard)
		}
	}
	return shards, nil
}

// plannedWorkloadResourceTier 只接受计划内固化的 normal 资源身份。
func plannedWorkloadResourceTier(workload PlannedWorkload) (cicontract.WorkloadResourceTier, error) {
	switch {
	case workload.ResourceCPU == 2 && workload.ResourceMemoryGiB == 4:
		return cicontract.WorkloadResourceTierFast, nil
	case workload.ResourceCPU == 4 && workload.ResourceMemoryGiB == 8:
		return cicontract.WorkloadResourceTierMedium, nil
	case workload.ResourceCPU == 8 && workload.ResourceMemoryGiB == 16:
		return cicontract.WorkloadResourceTierSlow, nil
	default:
		return 0, fmt.Errorf("workload %q has unsupported resource %.gC/%.gGiB", workload.Workload.ID, workload.ResourceCPU, workload.ResourceMemoryGiB)
	}
}

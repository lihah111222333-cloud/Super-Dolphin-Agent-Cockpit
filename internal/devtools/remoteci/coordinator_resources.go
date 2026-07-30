package remoteci

import (
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/shardresource"
)

// remoteExecutionShardResources 为每个缓存未命中分片选择一个精确 ECI 资源档位。
func remoteExecutionShardResources(
	policy shardresource.Policy,
	observations []shardresource.Observation,
	catalog gate.WorkloadCatalog,
	shards []gate.ContainerShard,
	input RunInput,
) ([]eci.Resources, error) {
	workloads := make(map[string]gate.Workload, len(catalog.Workloads))
	for _, workload := range catalog.Workloads {
		if _, duplicate := workloads[workload.ID]; duplicate {
			return nil, fmt.Errorf("remote CI resource catalog repeats workload %q", workload.ID)
		}
		workloads[workload.ID] = workload
	}
	context := shardresource.Context{
		Runner:    input.RunnerIdentityDigest,
		Toolchain: input.ToolchainDigest,
	}
	resources := make([]eci.Resources, len(shards))
	for index, shard := range shards {
		selection := shardresource.Shard{
			Identity:  shard.IdentityDigest,
			Workloads: make([]shardresource.Workload, 0, len(shard.GateIDs)),
		}
		for _, gateID := range shard.GateIDs {
			workload, ok := workloads[string(gateID)]
			if !ok {
				return nil, fmt.Errorf("remote CI resource workload %q is absent from catalog", gateID)
			}
			selection.Workloads = append(selection.Workloads, shardresource.Workload{
				ID:   workload.ID,
				Kind: string(workload.Kind),
			})
		}
		class, err := shardresource.Select(policy, selection, context, observations)
		if err != nil {
			return nil, fmt.Errorf("select remote CI resources for shard %d: %w", index, err)
		}
		resources[index] = eci.Resources{CPU: class.VCPU, MemoryGiB: class.MemoryGiB}
	}
	return resources, nil
}

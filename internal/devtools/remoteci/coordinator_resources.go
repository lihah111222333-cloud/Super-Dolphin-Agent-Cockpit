package remoteci

import (
	"errors"
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
	if input.Calibration {
		class, err := policy.ResolveCalibrationClass()
		if err != nil {
			return nil, fmt.Errorf("resolve remote CI calibration resources: %w", err)
		}
		if input.CalibrationResource != class {
			return nil, errors.New("remote CI calibration resource identity drifted")
		}
		resources := make([]eci.Resources, len(shards))
		for index := range resources {
			resources[index] = eci.Resources{CPU: class.VCPU, MemoryGiB: class.MemoryGiB}
		}
		return resources, nil
	}
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

func bindRemoteShardResources(results []ShardResult, resources []eci.Resources, requests []ShardRequest) error {
	if len(results) != len(resources) || len(results) != len(requests) {
		return errors.New("remote CI shard resource receipts are incomplete")
	}
	for index := range results {
		if err := validateShardResourceBinding(resources[index], requests[index]); err != nil {
			return fmt.Errorf("remote CI shard %d resource receipt: %w", index, err)
		}
		results[index].Resources = resources[index]
		if requests[index].CalibrationResource != nil {
			results[index].ResourceClass = requests[index].CalibrationResource.ID
		}
	}
	return nil
}

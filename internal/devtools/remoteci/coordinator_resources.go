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
	plan gate.WorkloadExecutionPlan,
	shards []gate.ContainerShard,
	input RunInput,
) ([]shardresource.Class, error) {
	if input.Calibration {
		return remoteCalibrationShardResources(policy, shards, input.CalibrationResource)
	}
	workloads, err := remotePlannedResourceWorkloads(plan)
	if err != nil {
		return nil, err
	}
	context := shardresource.Context{Runner: input.RunnerIdentityDigest, Toolchain: input.ToolchainDigest}
	resources := make([]shardresource.Class, len(shards))
	for index, shard := range shards {
		resources[index], err = selectRemoteShardResources(policy, observations, workloads, shard, context)
		if err != nil {
			return nil, fmt.Errorf("select remote CI resources for shard %d: %w", index, err)
		}
	}
	return resources, nil
}

// remoteCalibrationShardResources 将每个校准分片锁定到独立的 4C/8GiB 规格。
func remoteCalibrationShardResources(policy shardresource.Policy, shards []gate.ContainerShard, requested shardresource.Class) ([]shardresource.Class, error) {
	class, err := policy.ResolveCalibrationClass()
	if err != nil {
		return nil, fmt.Errorf("resolve remote CI calibration resources: %w", err)
	}
	if requested != class {
		return nil, errors.New("remote CI calibration resource identity drifted")
	}
	resources := make([]shardresource.Class, len(shards))
	for index := range resources {
		resources[index] = class
	}
	return resources, nil
}

// remotePlannedResourceWorkloads 按 ID 索引冻结计划中的单 workload 历史估时。
func remotePlannedResourceWorkloads(plan gate.WorkloadExecutionPlan) (map[string]gate.PlannedWorkload, error) {
	workloads := make(map[string]gate.PlannedWorkload, len(plan.ExecutionWorkloadIDs))
	for _, plannedShard := range plan.Shards {
		for _, workload := range plannedShard.Workloads {
			if _, duplicate := workloads[workload.Workload.ID]; duplicate {
				return nil, fmt.Errorf("remote CI resource plan repeats workload %q", workload.Workload.ID)
			}
			workloads[workload.Workload.ID] = workload
		}
	}
	return workloads, nil
}

// selectRemoteShardResources 严格投影计划中已固化的资源身份，禁止按耗时二次分类。
func selectRemoteShardResources(
	policy shardresource.Policy,
	observations []shardresource.Observation,
	workloads map[string]gate.PlannedWorkload,
	shard gate.ContainerShard,
	context shardresource.Context,
) (shardresource.Class, error) {
	selection := shardresource.Shard{
		Identity:  shard.IdentityDigest,
		Workloads: make([]shardresource.Workload, 0, len(shard.GateIDs)),
	}
	for _, gateID := range shard.GateIDs {
		planned, ok := workloads[string(gateID)]
		if !ok {
			return shardresource.Class{}, fmt.Errorf("remote CI resource workload %q is absent from plan", gateID)
		}
		selection.Workloads = append(selection.Workloads, shardresource.Workload{
			ID: planned.Workload.ID, Kind: string(planned.Workload.Kind),
			EstimatedDurationMS: planned.EstimatedDurationMS,
			ResourceCPU: planned.ResourceCPU, ResourceMemoryGiB: planned.ResourceMemoryGiB,
		})
	}
	return shardresource.Select(policy, selection, context, observations)
}

// bindRemoteShardResources 将请求资源与 ECI 回执绑定，并拒绝校准规格缺失或漂移。
func bindRemoteShardResources(results []ShardResult, resources []shardresource.Class, requests []ShardRequest) error {
	if len(results) != len(resources) || len(results) != len(requests) {
		return errors.New("remote CI shard resource receipts are incomplete")
	}
	var bindingErrors []error
	for index := range results {
		if err := validateShardResourceBinding(resources[index], requests[index]); err != nil {
			bindingErrors = append(bindingErrors, fmt.Errorf("remote CI shard %d resource receipt: %w", index, err))
			continue
		}
		observed, err := bindRemoteShardObservedResources(index, results[index].Resources, resources[index], requests[index].Calibration)
		if err != nil {
			bindingErrors = append(bindingErrors, err)
			continue
		}
		results[index].Resources = observed
		results[index].ResourceClass = resources[index].ID
	}
	// 先固定所有 ECI 容器回执，再校验 worker 的 compile-group manifest。
	// 单个 manifest 漂移不得阻止后续分片资源进入失败账本。
	for index := range results {
		if err := bindRemoteShardCompileGroups(index, results[index], requests[index]); err != nil {
			bindingErrors = append(bindingErrors, err)
		}
	}
	return errors.Join(bindingErrors...)
}

func bindRemoteShardCompileGroups(index int, result ShardResult, request ShardRequest) error {
	if request.ShardIdentity == "" || result.ShardIdentity == "" || result.ShardIdentity != request.ShardIdentity {
		return fmt.Errorf("remote CI shard %d identity does not match request", index)
	}
	digest, err := request.ComputeShardExecutionManifestDigest()
	if err != nil {
		return fmt.Errorf("remote CI shard %d execution manifest: %w", index, err)
	}
	if digest != request.ShardExecutionManifestDigest {
		return fmt.Errorf("remote CI shard %d execution manifest digest drifted", index)
	}
	if err := request.validateCompileGroupResourceBinding(); err != nil {
		return fmt.Errorf("remote CI shard %d compile group resource: %w", index, err)
	}
	if err := gate.ValidateCompileGroupExecutions(request.CompileGroups, result.Report.CompileGroupExecutions); err != nil {
		return fmt.Errorf("remote CI shard %d compile group receipt: %w", index, err)
	}
	return nil
}

// bindRemoteShardObservedResources 校验 ECI 实时资源回执，禁止用请求规格伪造观测。
func bindRemoteShardObservedResources(index int, observed eci.Resources, requested shardresource.Class, calibration bool) (eci.Resources, error) {
	if observed.CPU <= 0 || observed.MemoryGiB <= 0 {
		return observed, fmt.Errorf("remote CI shard %d %s provider resource observation is incomplete: %.gC/%.gGiB; requested %.gC/%.gGiB", index, remoteResourceBindingMode(calibration), observed.CPU, observed.MemoryGiB, requested.VCPU, requested.MemoryGiB)
	}
	if !remoteResourcesMatch(observed, requested) {
		return observed, fmt.Errorf("remote CI shard %d %s provider resource %.gC/%.gGiB does not match requested %.gC/%.gGiB", index, remoteResourceBindingMode(calibration), observed.CPU, observed.MemoryGiB, requested.VCPU, requested.MemoryGiB)
	}
	return observed, nil
}

func remoteResourceBindingMode(calibration bool) string {
	if calibration {
		return "calibration"
	}
	return "normal"
}

func remoteResourcesMatch(observed eci.Resources, requested shardresource.Class) bool {
	return observed.CPU == requested.VCPU && observed.MemoryGiB == requested.MemoryGiB
}

package cicontract

// PlannerSSOTConsumerBinding 是 planner 资源身份 canonical owner 的生产消费登记。
// 架构守卫从 production AST 独立发现 consumer，再与该 registry 双向核对。
type PlannerSSOTConsumerBinding struct {
	SourceFile        string
	FunctionName      string
	CanonicalCall     string
	ExpectedArguments []string
	FixtureSignature  string
	DirectReturn      bool
}

// PlannerSSOTConsumerBindings 返回 planner 资源 SSOT 的完整生产消费登记副本。
func PlannerSSOTConsumerBindings() []PlannerSSOTConsumerBinding {
	bindings := []PlannerSSOTConsumerBinding{
		{"internal/devtools/gate/workload_duration_index.go", "normalResourceForTier", "NormalResourceTupleForTier", []string{"tier"}, "func normalResourceForTier(tier int) (float64, float64, error)", true},
		{"internal/devtools/gate/workload_compile_selector_planning.go", "normalCompileResourceClass", "NormalResourceClassForTier", []string{"tier"}, "func normalCompileResourceClass(tier int) (string, error)", true},
		{"internal/devtools/gate/local_workload_planning_projection.go", "localProjectionNormalClass", "NormalResourceClassProjection", []string{"resource.classID", "resource.cpu", "resource.memoryGiB"}, "func localProjectionNormalClass(resource durationSampleResource) (string, error)", true},
		{"internal/devtools/gate/workload_resource_planning.go", "plannedWorkloadResourceTier", "NormalResourceTierForResources", []string{"workload.ResourceCPU", "workload.ResourceMemoryGiB"}, "func plannedWorkloadResourceTier(workload PlannedWorkload) (cicontract.WorkloadResourceTier, error)", true},
		{"internal/devtools/gate/compile_group.go", "validateCompileGroupResourceClass", "ValidateResourceClassID", []string{"value"}, "func validateCompileGroupResourceClass(value string) error", true},
		{"internal/devtools/gate/workload_duration_index.go", "normalResourceTierForTuple", "NormalResourceTierForResources", []string{"cpu", "memoryGiB"}, "func normalResourceTierForTuple(cpu, memoryGiB float64) (cicontract.WorkloadResourceTier, error)", true},
		{"internal/devtools/gate/workload_resource_reserve.go", "resourceTierForReserve", "NormalResourceTierForIdentity", []string{"resources.ClassID", "resources.CPU", "resources.MemoryGiB"}, "func resourceTierForReserve(resources RemoteCIShardResources) (cicontract.WorkloadResourceTier, error)", true},
	}
	for index := range bindings {
		bindings[index].ExpectedArguments = append([]string(nil), bindings[index].ExpectedArguments...)
	}
	return bindings
}

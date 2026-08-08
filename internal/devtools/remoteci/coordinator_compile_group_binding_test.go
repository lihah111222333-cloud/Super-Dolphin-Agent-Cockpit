package remoteci

import (
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/shardresource"
)

func TestBindRemoteShardCompileGroupsRequiresExactRequestLedger(t *testing.T) {
	request, executions := compileGroupBindingFixture(t)
	valid := ShardResult{ShardIdentity: request.ShardIdentity, Report: gate.PlanExecutionReport{CompileGroupExecutions: executions}}
	if err := bindRemoteShardCompileGroups(0, valid, request); err != nil {
		t.Fatalf("valid compile group report rejected: %v", err)
	}
	tests := map[string]func([]gate.CompileGroupExecution) []gate.CompileGroupExecution{
		"missing": func(_ []gate.CompileGroupExecution) []gate.CompileGroupExecution { return nil },
		"extra": func(value []gate.CompileGroupExecution) []gate.CompileGroupExecution {
			return append(value, value[0])
		},
		"reordered workloads": func(value []gate.CompileGroupExecution) []gate.CompileGroupExecution {
			value[0].WorkloadIDs[0], value[0].WorkloadIDs[1] = value[0].WorkloadIDs[1], value[0].WorkloadIDs[0]
			return value
		},
		"forged": func(value []gate.CompileGroupExecution) []gate.CompileGroupExecution {
			value[0].ArtifactKey = "sha256:" + strings.Repeat("f", 64)
			return value
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			report := valid
			report.Report.CompileGroupExecutions = mutate(append([]gate.CompileGroupExecution(nil), executions...))
			if err := bindRemoteShardCompileGroups(0, report, request); err == nil {
				t.Fatalf("%s compile group report was accepted", name)
			}
		})
	}
	ordinary := request
	ordinary.CompileGroups = nil
	ordinary.ShardExecutionManifestDigest, _ = ordinary.ComputeShardExecutionManifestDigest()
	ordinaryResult := ShardResult{ShardIdentity: ordinary.ShardIdentity, Report: gate.PlanExecutionReport{CompileGroupExecutions: executions}}
	if err := bindRemoteShardCompileGroups(0, ordinaryResult, ordinary); err == nil {
		t.Fatal("ordinary shard accepted compile executions without manifest groups")
	}
	driftedResource := request
	driftedResource.ResourceClass.ID = "maximum"
	if err := bindRemoteShardCompileGroups(0, valid, driftedResource); err == nil {
		t.Fatal("compile group accepted a resource class that drifted from its ECI request")
	}
}

func TestBindRemoteShardResourcesBindsContainerBeforeCompileGroupReceipt(t *testing.T) {
	request, executions := compileGroupBindingFixture(t)
	calibration := shardresource.Class{ID: "calibration", VCPU: 4, MemoryGiB: 8}
	request.Calibration = true
	request.CalibrationResource = &calibration
	request.ResourceClass = calibration

	// 模拟仍将编译组标为 normal medium 的陈旧 worker manifest；容器本身
	// 返回固定校准规格，该回执必须保留给耗时诊断。
	group := request.CompileGroups[0]
	group.ResourceClassID = "medium"
	var err error
	group.BatchPlanDigest, err = gate.CompileGroupBatchPlanDigest(group)
	if err != nil {
		t.Fatalf("CompileGroupBatchPlanDigest() error = %v", err)
	}
	group.GroupID, err = gate.CompileGroupID(group)
	if err != nil {
		t.Fatalf("CompileGroupID() error = %v", err)
	}
	request.CompileGroups[0] = group
	request.ShardExecutionManifestDigest, err = request.ComputeShardExecutionManifestDigest()
	if err != nil {
		t.Fatalf("ComputeShardExecutionManifestDigest() error = %v", err)
	}
	executions[0].GroupID = group.GroupID
	executions[0].ResourceClassID = group.ResourceClassID

	results := []ShardResult{{
		ShardIdentity: request.ShardIdentity,
		Resources:     eci.Resources{CPU: calibration.VCPU, MemoryGiB: calibration.MemoryGiB},
		Report:        gate.PlanExecutionReport{CompileGroupExecutions: executions},
	}}
	err = bindRemoteShardResources(results, []shardresource.Class{calibration}, []ShardRequest{request})
	if err == nil || !strings.Contains(err.Error(), "compile group resource") {
		t.Fatalf("bindRemoteShardResources() error = %v, want compile group resource rejection", err)
	}
	if got, want := results[0].ResourceClass, calibration.ID; got != want {
		t.Fatalf("ResourceClass = %q, want bound container class %q after manifest rejection", got, want)
	}
	if got, want := results[0].Resources, (eci.Resources{CPU: 4, MemoryGiB: 8}); got != want {
		t.Fatalf("Resources = %#v, want bound calibration receipt %#v after manifest rejection", got, want)
	}
}

func TestBindRemoteShardResourcesRetainsLaterContainerReceiptsAfterCompileGroupDrift(t *testing.T) {
	firstRequest, firstExecutions := compileGroupBindingFixture(t)
	class := shardresource.Class{ID: "medium", VCPU: 4, MemoryGiB: 8}
	firstRequest.ResourceClass = class
	firstExecutions[0].ResourceClassID = "stale-medium"

	secondRequest, secondExecutions := compileGroupBindingFixture(t)
	secondRequest.ShardIdentity = strings.Repeat("b", 64)
	secondRequest.ResourceClass = class
	results := []ShardResult{
		{
			ShardIdentity: firstRequest.ShardIdentity,
			Resources:     eci.Resources{CPU: 4, MemoryGiB: 8},
			Report:        gate.PlanExecutionReport{CompileGroupExecutions: firstExecutions},
		},
		{
			ShardIdentity: secondRequest.ShardIdentity,
			Resources:     eci.Resources{CPU: 4, MemoryGiB: 8},
			Report:        gate.PlanExecutionReport{CompileGroupExecutions: secondExecutions},
		},
	}

	err := bindRemoteShardResources(results, []shardresource.Class{class, class}, []ShardRequest{firstRequest, secondRequest})
	if err == nil || !strings.Contains(err.Error(), "compile group resource") {
		t.Fatalf("bindRemoteShardResources() error = %v, want compile group resource rejection", err)
	}
	for index, result := range results {
		if got := result.ResourceClass; got != class.ID {
			t.Fatalf("results[%d].ResourceClass = %q, want %q", index, got, class.ID)
		}
		if got := result.Resources; got != (eci.Resources{CPU: 4, MemoryGiB: 8}) {
			t.Fatalf("results[%d].Resources = %#v, want bound provider receipt", index, got)
		}
	}
}

func compileGroupBindingFixture(t *testing.T) (ShardRequest, []gate.CompileGroupExecution) {
	t.Helper()
	first, err := gate.NewGoTestWorkload(gate.GateIDBackendTestWithGuard, "./internal/archtest", "TestBoundary", 10)
	if err != nil {
		t.Fatal(err)
	}
	second, err := gate.NewGoTestWorkload(gate.GateIDBackendTestWithGuard, "./internal/archtest", "TestAnotherBoundary", 10)
	if err != nil {
		t.Fatal(err)
	}
	groups := []gate.CompileGroup{compileBindingGroup(t, gate.GateID(first.ID), gate.GateID(second.ID))}
	request := ShardRequest{
		Profile: gate.ProfileLocalFast, PlanDigest: "sha256:" + strings.Repeat("a", 64),
		ShardIdentity: "sha256:" + strings.Repeat("b", 64), SourceTreeSHA: strings.Repeat("c", 40),
		GateIDs: []gate.GateID{gate.GateID(first.ID), gate.GateID(second.ID)}, CompileGroups: groups,
		ResourceClass: shardresource.Class{ID: "small", VCPU: 2, MemoryGiB: 4},
	}
	request.ShardExecutionManifestDigest, err = request.ComputeShardExecutionManifestDigest()
	if err != nil {
		t.Fatal(err)
	}
	executions := make([]gate.CompileGroupExecution, len(groups))
	for index, group := range groups {
		artifact, err := gate.CompileArtifactKey(group)
		if err != nil {
			t.Fatal(err)
		}
		digest := "sha256:" + strings.Repeat(string(rune('d'+index)), 64)
		executions[index] = gate.CompileGroupExecution{
			Scope: cicontract.TimingScopeCompileGroup, Phase: cicontract.TimingTestBinaryCompile,
			GroupID: group.GroupID, ArtifactKey: artifact, PackageTarget: group.PackageTarget,
			WorkloadIDs: append([]gate.GateID(nil), group.WorkloadIDs...), StartedAtUnixMS: 1_000,
			CompletedAtUnixMS: 1_025, DurationMS: 25, ArtifactSHA256: digest, ArtifactSize: 1,
			Status: gate.ResultStatusPassed, ExitCode: 0, CompileCommandDigest: digest,
			ProfileDigest: group.ProfileDigest, ResourceClassID: group.ResourceClassID,
		}
	}
	return request, executions
}

func compileBindingGroup(t *testing.T, workloadIDs ...gate.GateID) gate.CompileGroup {
	t.Helper()
	group := gate.CompileGroup{
		PackageTarget: "./internal/archtest", SemanticKey: gate.CompileGroupSemanticGoTestNormal,
		SharedInputDigest: "sha256:" + strings.Repeat("e", 64), ProfileDigest: "sha256:" + strings.Repeat("f", 64),
		ResourceClassID: "small", WorkloadIDs: append([]gate.GateID(nil), workloadIDs...), CompileEstimateMS: 10,
		BodyEstimateMS: 20, EstimatedDurationMS: 30,
	}
	finalizeTestCompileGroup(t, &group)
	return group
}

func finalizeTestCompileGroup(t *testing.T, group *gate.CompileGroup) {
	t.Helper()
	if group == nil || len(group.WorkloadIDs) == 0 || group.BodyEstimateMS < int64(len(group.WorkloadIDs)) {
		t.Fatal("compile group fixture requires positive selector body estimates")
	}
	selectorIDs := append([]gate.GateID(nil), group.WorkloadIDs...)
	slices.Sort(selectorIDs)
	bodyBySelector := make(map[gate.GateID]int64, len(selectorIDs))
	base, remainder := group.BodyEstimateMS/int64(len(selectorIDs)), group.BodyEstimateMS%int64(len(selectorIDs))
	for index, id := range selectorIDs {
		body := base
		if int64(index) < remainder {
			body++
		}
		bodyBySelector[id] = body
	}
	group.SelectorEstimates = make([]gate.CompileSelectorEstimate, len(selectorIDs))
	for index, id := range selectorIDs {
		group.SelectorEstimates[index] = gate.CompileSelectorEstimate{SelectorID: id, BodyEstimateMS: bodyBySelector[id]}
	}
	group.BatchPlan = []gate.CompileGroupBatch{{BatchID: "batch-000", Wave: 0, SelectorIDs: selectorIDs, EstimatedBodyMS: group.BodyEstimateMS}}
	var err error
	group.BatchPlanDigest, err = gate.CompileGroupBatchPlanDigest(*group)
	if err != nil {
		t.Fatal(err)
	}
	group.GroupID, err = gate.CompileGroupID(*group)
	if err != nil {
		t.Fatal(err)
	}
	if err := group.Validate(); err != nil {
		t.Fatal(err)
	}
}

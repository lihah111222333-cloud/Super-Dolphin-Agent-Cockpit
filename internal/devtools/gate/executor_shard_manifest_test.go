package gate

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func TestShardExecutionManifestRejectsCompileGroupSemanticDrift(t *testing.T) {
	first := mustManifestTestWorkload(t, GateIDBackendTestWithGuard, "./internal/archtest", "TestBoundary")
	benchmark := mustManifestTestBenchmark(t, GateIDBackendTestWithGuard, "./internal/archtest", "BenchmarkBoundary")
	otherPackage := mustManifestTestWorkload(t, GateIDBackendTestWithGuard, "./internal/module/turn", "TestRedact")
	raced := mustManifestTestWorkload(t, GateIDBackendTestGuardWithRace, "./internal/archtest", "TestBoundary")
	tests := []struct {
		name string
		ids  []GateID
	}{
		{name: "mixed-kind", ids: []GateID{GateID(first.ID), GateID(benchmark.ID)}},
		{name: "mixed-package", ids: []GateID{GateID(first.ID), GateID(otherPackage.ID)}},
		{name: "mixed-parent", ids: []GateID{GateID(first.ID), GateID(raced.ID)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			group := manifestTestCompileGroup(t, test.ids)
			manifest := manifestTestInput(test.ids, group)
			if err := manifest.Validate(); err == nil {
				t.Fatal("semantic drift was accepted")
			}
		})
	}
}

func TestShardExecutionManifestAcceptsAtomicArchtestSingleBatch(t *testing.T) {
	first := mustManifestTestWorkload(t, GateIDBackendTestWithGuard, "./internal/archtest", "TestAtomicFirst")
	second := mustManifestTestWorkload(t, GateIDBackendTestWithGuard, "./internal/archtest", "TestAtomicSecond")
	ids := []GateID{GateID(first.ID), GateID(second.ID)}
	group := CompileGroup{
		PackageTarget:     AtomicArchtestPackageTarget,
		SemanticKey:       CompileGroupSemanticGoTestNormal,
		SharedInputDigest: "sha256:" + strings.Repeat("c", 64),
		ProfileDigest:     "sha256:" + strings.Repeat("d", 64),
		ResourceClassID:   "medium",
		WorkloadIDs:       ids,
		SelectorEstimates: []CompileSelectorEstimate{{SelectorID: ids[0], BodyEstimateMS: 10}, {SelectorID: ids[1], BodyEstimateMS: 10}},
		BatchPlan:         []CompileGroupBatch{{BatchID: "batch-000", Wave: 0, SelectorIDs: ids, EstimatedBodyMS: 20}},
		CompileEstimateMS: 10, BodyEstimateMS: 20, EstimatedDurationMS: 30,
	}
	var err error
	group.BatchPlanDigest, err = CompileGroupBatchPlanDigest(group)
	if err != nil {
		t.Fatal(err)
	}
	group.GroupID, err = CompileGroupID(group)
	if err != nil {
		t.Fatal(err)
	}
	if err := group.Validate(); err != nil {
		t.Fatalf("compile group rejected shared-binary archtest plan: %v", err)
	}
	manifest := manifestTestInput(ids, group)
	if err := manifest.Validate(); err != nil {
		t.Fatalf("manifest rejected shared-binary archtest plan: %v", err)
	}
}

func TestShardExecutionManifestRejectsAtomicAgentTerminalMultipleBatches(t *testing.T) {
	first := mustManifestTestWorkload(t, GateIDBackendTestWithGuard, AtomicAgentTerminalPackageTarget, "TestAgentTerminalMain")
	second := mustManifestTestWorkload(t, GateIDBackendTestWithGuard, AtomicAgentTerminalPackageTarget, "TestAgentTerminalRecovery")
	ids := []GateID{GateID(first.ID), GateID(second.ID)}
	group := CompileGroup{
		PackageTarget:     AtomicAgentTerminalPackageTarget,
		SemanticKey:       CompileGroupSemanticGoTestNormal,
		SharedInputDigest: "sha256:" + strings.Repeat("c", 64),
		ProfileDigest:     "sha256:" + strings.Repeat("d", 64),
		ResourceClassID:   "medium",
		WorkloadIDs:       ids,
		SelectorEstimates: []CompileSelectorEstimate{{SelectorID: ids[0], BodyEstimateMS: 10}, {SelectorID: ids[1], BodyEstimateMS: 10}},
		BatchPlan: []CompileGroupBatch{
			{BatchID: "batch-000", Wave: 0, SelectorIDs: []GateID{ids[0]}, EstimatedBodyMS: 10},
			{BatchID: "batch-001", Wave: 0, SelectorIDs: []GateID{ids[1]}, EstimatedBodyMS: 10},
		},
		CompileEstimateMS: 10, BodyEstimateMS: 20, EstimatedDurationMS: 30,
	}
	var err error
	group.BatchPlanDigest, err = CompileGroupBatchPlanDigest(group)
	if err != nil {
		t.Fatal(err)
	}
	group.GroupID, err = CompileGroupID(group)
	if err != nil {
		t.Fatal(err)
	}
	if err := group.Validate(); err == nil || !strings.Contains(err.Error(), "exactly one batch") {
		t.Fatalf("compile group accepted forged two-batch agent-terminal plan: %v", err)
	}
	manifest := manifestTestInput(ids, group)
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "exactly one batch") {
		t.Fatalf("manifest accepted forged two-batch agent-terminal plan: %v", err)
	}
}

func TestShardExecutionManifestAcceptsUpdaterAndTaskDAGMultipleBatches(t *testing.T) {
	for _, test := range []struct {
		name          string
		packageTarget string
		names         []string
	}{
		{
			name:          "updater",
			packageTarget: AtomicUpdaterPackageTarget,
			names:         []string{"TestUpdaterCandidateCleanup", "TestUpdaterRollbackEntries"},
		},
		{
			name:          "taskdag",
			packageTarget: AtomicTaskDAGPackageTarget,
			names:         []string{"TestTaskDAGStore", "TestTaskDAGWakeup"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			workloads := make([]Workload, len(test.names))
			ids := make([]GateID, len(test.names))
			for index, name := range test.names {
				workloads[index] = mustManifestTestWorkload(t, GateIDBackendTestWithGuard, test.packageTarget, name)
				ids[index] = GateID(workloads[index].ID)
			}
			group := manifestTestSharedBatchCompileGroup(t, test.packageTarget, ids)
			if err := group.Validate(); err != nil {
				t.Fatalf("%s compile group rejected: %v", test.packageTarget, err)
			}
			if err := manifestTestInput(ids, group).Validate(); err != nil {
				t.Fatalf("%s manifest rejected: %v", test.packageTarget, err)
			}
		})
	}
}

func TestShardExecutionManifestAcceptsAtomicSQLiteMultipleBatches(t *testing.T) {
	first := mustManifestTestWorkload(t, GateIDBackendTestWithGuard, AtomicSQLitePackageTarget, "TestSQLiteFirst")
	second := mustManifestTestWorkload(t, GateIDBackendTestWithGuard, AtomicSQLitePackageTarget, "TestSQLiteSecond")
	ids := []GateID{GateID(first.ID), GateID(second.ID)}
	group := CompileGroup{
		PackageTarget:     AtomicSQLitePackageTarget,
		SemanticKey:       CompileGroupSemanticGoTestNormal,
		SharedInputDigest: "sha256:" + strings.Repeat("c", 64),
		ProfileDigest:     "sha256:" + strings.Repeat("d", 64),
		ResourceClassID:   "medium",
		WorkloadIDs:       ids,
		SelectorEstimates: []CompileSelectorEstimate{{SelectorID: ids[0], BodyEstimateMS: 10}, {SelectorID: ids[1], BodyEstimateMS: 10}},
		BatchPlan: []CompileGroupBatch{
			{BatchID: "batch-000", Wave: 0, SelectorIDs: []GateID{ids[0]}, EstimatedBodyMS: 10},
			{BatchID: "batch-001", Wave: 0, SelectorIDs: []GateID{ids[1]}, EstimatedBodyMS: 10},
		},
		CompileEstimateMS: 10, BodyEstimateMS: 20, EstimatedDurationMS: 30,
	}
	var err error
	group.BatchPlanDigest, err = CompileGroupBatchPlanDigest(group)
	if err != nil {
		t.Fatal(err)
	}
	group.GroupID, err = CompileGroupID(group)
	if err != nil {
		t.Fatal(err)
	}
	if err := group.Validate(); err != nil {
		t.Fatalf("compile group rejected valid sqlite batches: %v", err)
	}
	manifest := manifestTestInput(ids, group)
	if err := manifest.Validate(); err != nil {
		t.Fatalf("manifest rejected valid sqlite batches: %v", err)
	}
}

func TestShardExecutionManifestRejectsGroupOutsideShardOrDuplicate(t *testing.T) {
	first := mustManifestTestWorkload(t, GateIDBackendTestWithGuard, "./internal/archtest", "TestBoundary")
	second := mustManifestTestWorkload(t, GateIDBackendTestWithGuard, "./internal/archtest", "TestAnotherBoundary")
	group := manifestTestCompileGroup(t, []GateID{GateID(first.ID)})
	outside := manifestTestCompileGroup(t, []GateID{GateID(second.ID)})
	if err := manifestTestInput([]GateID{GateID(first.ID)}, outside).Validate(); err == nil {
		t.Fatal("group workload outside GateIDs was accepted")
	}
	if err := manifestTestInput([]GateID{GateID(first.ID)}, group, group).Validate(); err == nil {
		t.Fatal("duplicate group workload was accepted")
	}
}

func TestShardExecutionManifestRejectsOrdinarySelectorBesideSpecialGroup(t *testing.T) {
	special := mustManifestTestWorkload(t, GateIDBackendTestWithGuard, AtomicArchtestPackageTarget, "TestSpecialIsolation")
	ordinary := mustManifestTestWorkload(t, GateIDBackendTestWithGuard, AtomicGatePackageTarget, "TestOrdinaryIsolation")
	group := manifestTestCompileGroup(t, []GateID{GateID(special.ID)})
	manifest := manifestTestInput([]GateID{GateID(special.ID), GateID(ordinary.ID)}, group)
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "mixes grouped and ordinary") {
		t.Fatalf("special group plus ordinary selector error = %v, want isolation failure", err)
	}
}

func TestShardExecutionManifestRejectsDuplicateArtifactResourceBinding(t *testing.T) {
	first := mustManifestTestWorkload(t, GateIDBackendTestWithGuard, "./internal/archtest", "TestBoundary")
	second := mustManifestTestWorkload(t, GateIDBackendTestWithGuard, "./internal/archtest", "TestAnotherBoundary")
	firstGroup := manifestTestCompileGroup(t, []GateID{GateID(first.ID)})
	secondGroup := manifestTestCompileGroup(t, []GateID{GateID(second.ID)})
	if firstGroup.GroupID == secondGroup.GroupID {
		t.Fatal("fixture compile groups unexpectedly share group identity")
	}
	groups := []CompileGroup{firstGroup, secondGroup}
	slices.SortFunc(groups, func(left, right CompileGroup) int { return strings.Compare(left.GroupID, right.GroupID) })
	manifest := manifestTestInput([]GateID{GateID(first.ID), GateID(second.ID)}, groups...)
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "compile artifact") {
		t.Fatalf("duplicate artifact/resource binding error = %v", err)
	}
}

func TestShardExecutionManifestRejectsDifferentArtifactsInOneShard(t *testing.T) {
	first := mustManifestTestWorkload(t, GateIDBackendTestWithGuard, "./internal/archtest", "TestArtifactFirst")
	second := mustManifestTestWorkload(t, GateIDBackendTestWithGuard, "./internal/devtools/gate", "TestArtifactSecond")
	firstGroup := manifestTestCompileGroup(t, []GateID{GateID(first.ID)})
	secondID := GateID(second.ID)
	secondGroup := CompileGroup{
		PackageTarget: "./internal/devtools/gate", SemanticKey: CompileGroupSemanticGoTestNormal,
		SharedInputDigest: "sha256:" + strings.Repeat("e", 64), ProfileDigest: "sha256:" + strings.Repeat("f", 64),
		ResourceClassID: "medium", WorkloadIDs: []GateID{secondID},
		SelectorEstimates: []CompileSelectorEstimate{{SelectorID: secondID, BodyEstimateMS: 20}},
		BatchPlan:         []CompileGroupBatch{{BatchID: "batch-000", Wave: 0, SelectorIDs: []GateID{secondID}, EstimatedBodyMS: 20}},
		CompileEstimateMS: 10, BodyEstimateMS: 20, EstimatedDurationMS: 30,
	}
	var err error
	secondGroup.BatchPlanDigest, err = CompileGroupBatchPlanDigest(secondGroup)
	if err != nil {
		t.Fatal(err)
	}
	secondGroup.GroupID, err = CompileGroupID(secondGroup)
	if err != nil {
		t.Fatal(err)
	}
	if err := secondGroup.Validate(); err != nil {
		t.Fatal(err)
	}
	groups := []CompileGroup{firstGroup, secondGroup}
	slices.SortFunc(groups, func(left, right CompileGroup) int { return strings.Compare(left.GroupID, right.GroupID) })
	manifest := manifestTestInput([]GateID{GateID(first.ID), secondID}, groups...)
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "serial packing") {
		t.Fatalf("manifest accepted different artifacts in one shard: %v", err)
	}
}

func TestShardExecutionManifestAcceptsSameResourceOrdinaryGroups(t *testing.T) {
	first := mustManifestTestWorkload(t, GateIDBackendTestWithGuard, AtomicGatePackageTarget, "TestSerialPackingFirst")
	second := mustManifestTestWorkload(t, GateIDBackendTestWithGuard, AtomicGatePackageTarget, "TestSerialPackingSecond")
	firstID, secondID := GateID(first.ID), GateID(second.ID)
	firstGroup := manifestTestSharedBatchCompileGroup(t, AtomicGatePackageTarget, []GateID{firstID})
	secondGroup := manifestTestSharedBatchCompileGroup(t, AtomicGatePackageTarget, []GateID{secondID})
	secondGroup.SharedInputDigest = "sha256:" + strings.Repeat("e", 64)
	var err error
	secondGroup.BatchPlanDigest, err = CompileGroupBatchPlanDigest(secondGroup)
	if err != nil {
		t.Fatal(err)
	}
	secondGroup.GroupID, err = CompileGroupID(secondGroup)
	if err != nil {
		t.Fatal(err)
	}
	if err := secondGroup.Validate(); err != nil {
		t.Fatal(err)
	}
	groups := []CompileGroup{firstGroup, secondGroup}
	slices.SortFunc(groups, func(left, right CompileGroup) int { return strings.Compare(left.GroupID, right.GroupID) })
	if err := manifestTestInput([]GateID{firstID, secondID}, groups...).Validate(); err != nil {
		t.Fatalf("same-resource ordinary groups rejected: %v", err)
	}
}

func TestShardExecutionManifestRejectsLegacyAndUnknownSchemaVersions(t *testing.T) {
	workload := mustManifestTestWorkload(t, GateIDBackendTestWithGuard, AtomicGatePackageTarget, "TestManifestSchemaVersion")
	group := manifestTestSharedBatchCompileGroup(t, AtomicGatePackageTarget, []GateID{GateID(workload.ID)})
	for _, version := range []uint32{1, ShardExecutionManifestSchemaVersion + 1} {
		manifest := manifestTestInput([]GateID{GateID(workload.ID)}, group)
		manifest.SchemaVersion = version
		if err := manifest.Validate(); err == nil {
			t.Fatalf("schema version %d unexpectedly accepted", version)
		}
	}
}

func TestShardExecutionManifestRejectsMixedResourceSpecialAndReorderedGroups(t *testing.T) {
	t.Run("mixed resource", func(t *testing.T) {
		ids, groups := manifestMixedResourceGroups(t)
		assertManifestRejected(t, ids, groups, "resource class")
	})
	t.Run("reordered", func(t *testing.T) {
		ids, groups := manifestMixedResourceGroups(t)
		groups[1] = manifestGroupWithResource(t, groups[1], groups[0].ResourceClassID)
		slices.SortFunc(groups, func(left, right CompileGroup) int { return strings.Compare(left.GroupID, right.GroupID) })
		assertManifestRejected(t, ids, []CompileGroup{groups[1], groups[0]}, "canonical GroupID order")
	})
	t.Run("special", func(t *testing.T) {
		first := mustManifestTestWorkload(t, GateIDBackendTestWithGuard, AtomicArchtestPackageTarget, "TestSpecialFirst")
		second := mustManifestTestWorkload(t, GateIDBackendTestWithGuard, AtomicArchtestPackageTarget, "TestSpecialSecond")
		groups := []CompileGroup{
			manifestTestSharedBatchCompileGroup(t, AtomicArchtestPackageTarget, []GateID{GateID(first.ID)}),
			manifestTestSharedBatchCompileGroup(t, AtomicArchtestPackageTarget, []GateID{GateID(second.ID)}),
		}
		groups[1].SharedInputDigest = "sha256:" + strings.Repeat("a", 64)
		groups[1] = manifestGroupWithResource(t, groups[1], groups[1].ResourceClassID)
		slices.SortFunc(groups, func(left, right CompileGroup) int { return strings.Compare(left.GroupID, right.GroupID) })
		assertManifestRejected(t, []GateID{GateID(first.ID), GateID(second.ID)}, groups, "serial packing")
	})
}

// manifestMixedResourceGroups 构造同资源普通组并将第二组改为另一资源档。
func manifestMixedResourceGroups(t *testing.T) ([]GateID, []CompileGroup) {
	ordinaryFirst := mustManifestTestWorkload(t, GateIDBackendTestWithGuard, AtomicGatePackageTarget, "TestMixedFirst")
	ordinarySecond := mustManifestTestWorkload(t, GateIDBackendTestWithGuard, AtomicGatePackageTarget, "TestMixedSecond")
	firstID, secondID := GateID(ordinaryFirst.ID), GateID(ordinarySecond.ID)
	firstGroup := manifestTestSharedBatchCompileGroup(t, AtomicGatePackageTarget, []GateID{firstID})
	secondGroup := manifestTestSharedBatchCompileGroup(t, AtomicGatePackageTarget, []GateID{secondID})
	secondGroup.SharedInputDigest = "sha256:" + strings.Repeat("f", 64)
	secondGroup = manifestGroupWithResource(t, secondGroup, "small")
	groups := []CompileGroup{firstGroup, secondGroup}
	slices.SortFunc(groups, func(left, right CompileGroup) int { return strings.Compare(left.GroupID, right.GroupID) })
	return []GateID{firstID, secondID}, groups
}

// manifestGroupWithResource 重算资源变更后的 batch/group 身份。
func manifestGroupWithResource(t *testing.T, group CompileGroup, resourceClassID string) CompileGroup {
	t.Helper()
	group.ResourceClassID = resourceClassID
	var err error
	group.BatchPlanDigest, err = CompileGroupBatchPlanDigest(group)
	if err != nil {
		t.Fatal(err)
	}
	group.GroupID, err = CompileGroupID(group)
	if err != nil {
		t.Fatal(err)
	}
	if err := group.Validate(); err != nil {
		t.Fatal(err)
	}
	return group
}

// assertManifestRejected 断言 manifest 验证失败且包含预期边界原因。
func assertManifestRejected(t *testing.T, ids []GateID, groups []CompileGroup, reason string) {
	t.Helper()
	err := manifestTestInput(ids, groups...).Validate()
	if err == nil || !strings.Contains(err.Error(), reason) {
		t.Fatalf("manifest rejection error = %v, want %q", err, reason)
	}
}

func TestWorkerCompileGroupReportRejectsMissingOrReorderedExecutions(t *testing.T) {
	first := mustManifestTestWorkload(t, GateIDBackendTestWithGuard, "./internal/archtest", "TestBoundary")
	second := mustManifestTestWorkload(t, GateIDBackendTestWithGuard, "./internal/archtest", "TestAnotherBoundary")
	groups := []CompileGroup{
		manifestTestCompileGroup(t, []GateID{GateID(first.ID)}),
		manifestTestCompileGroup(t, []GateID{GateID(second.ID)}),
	}
	executions := []CompileGroupExecution{
		manifestTestCompileExecution(t, groups[0], 0),
		manifestTestCompileExecution(t, groups[1], 1),
	}
	if err := ValidateCompileGroupExecutions(groups, executions[:1]); err == nil {
		t.Fatal("worker report accepted a missing compile-group execution")
	}
	if err := ValidateCompileGroupExecutions(groups, []CompileGroupExecution{executions[1], executions[0]}); err == nil {
		t.Fatal("worker report accepted reordered compile-group executions")
	}
	var stdout bytes.Buffer
	err := writeExecutorPlanReportWithCompileGroups(
		executorPlanRequest{compileGroups: groups},
		PlanExecutionReport{CompileGroupExecutions: []CompileGroupExecution{executions[1], executions[0]}},
		nil,
		&stdout,
	)
	if err == nil {
		t.Fatal("worker report boundary accepted reordered compile-group executions")
	}
	if stdout.Len() != 0 {
		t.Fatalf("worker report boundary emitted %d bytes for invalid correlation", stdout.Len())
	}
}

func TestShardExecutionManifestBindingUsesIndependentPlanIdentityAndDigestCoversShardIdentity(t *testing.T) {
	workload := mustManifestTestWorkload(t, GateIDBackendTestWithGuard, "./internal/archtest", "TestBoundary")
	ids := []GateID{GateID(workload.ID)}
	group := manifestTestCompileGroup(t, ids)
	manifest := manifestTestInput(ids, group)
	encoded, digest, err := EncodeShardExecutionManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) == 0 || digest == "" {
		t.Fatal("manifest encoding did not produce canonical bytes and digest")
	}
	manifest.ManifestDigest = digest
	if err := manifest.ValidateBinding(ProfileLocalFast, manifest.PlanDigest); err != nil {
		t.Fatalf("valid manifest binding rejected: %v", err)
	}
	if err := manifest.ValidateBinding(ProfileRelease, manifest.PlanDigest); err == nil {
		t.Fatal("profile drift was accepted")
	}
	if err := manifest.ValidateBinding(ProfileLocalFast, "sha256:"+strings.Repeat("e", 64)); err == nil {
		t.Fatal("plan digest drift was accepted")
	}
	manifest.ShardIdentity = digestPlanLog([]byte("different-shard"))
	if err := manifest.ValidateBinding(ProfileLocalFast, manifest.PlanDigest); err == nil {
		t.Fatal("shard identity drift with the original digest was accepted")
	}
}

func manifestTestInput(ids []GateID, groups ...CompileGroup) ShardExecutionManifest {
	return ShardExecutionManifest{
		SchemaVersion: ShardExecutionManifestSchemaVersion, Profile: ProfileLocalFast,
		PlanDigest: "sha256:" + strings.Repeat("a", 64), ShardIdentity: digestPlanLog([]byte("manifest-test")),
		SourceTreeSHA: strings.Repeat("b", 40), GateIDs: ids, CompileGroups: groups,
	}
}

func manifestTestCompileGroup(t *testing.T, ids []GateID) CompileGroup {
	t.Helper()
	group := CompileGroup{
		PackageTarget: "./internal/archtest", SemanticKey: CompileGroupSemanticGoTestNormal,
		SharedInputDigest: "sha256:" + strings.Repeat("c", 64), ProfileDigest: "sha256:" + strings.Repeat("d", 64),
		ResourceClassID: "medium", WorkloadIDs: ids, CompileEstimateMS: 10, BodyEstimateMS: 20, EstimatedDurationMS: 30,
	}
	if len(ids) == 1 {
		group.SelectorEstimates = []CompileSelectorEstimate{{SelectorID: ids[0], BodyEstimateMS: 20}}
		group.BatchPlan = []CompileGroupBatch{{BatchID: "batch-000", Wave: 0, SelectorIDs: append([]GateID(nil), ids...), EstimatedBodyMS: 20}}
		var err error
		group.BatchPlanDigest, err = CompileGroupBatchPlanDigest(group)
		if err != nil {
			t.Fatal(err)
		}
	}
	var err error
	group.GroupID, err = CompileGroupID(group)
	if err != nil {
		t.Fatal(err)
	}
	return group
}

func manifestTestSharedBatchCompileGroup(t *testing.T, packageTarget string, ids []GateID) CompileGroup {
	t.Helper()
	selectorEstimates := make([]CompileSelectorEstimate, len(ids))
	batches := make([]CompileGroupBatch, len(ids))
	for index, id := range ids {
		selectorEstimates[index] = CompileSelectorEstimate{SelectorID: id, BodyEstimateMS: 10}
		batches[index] = CompileGroupBatch{
			BatchID:         "batch-" + fmt.Sprintf("%03d", index),
			Wave:            0,
			SelectorIDs:     []GateID{id},
			EstimatedBodyMS: 10,
		}
	}
	group := CompileGroup{
		PackageTarget:       packageTarget,
		SemanticKey:         CompileGroupSemanticGoTestNormal,
		SharedInputDigest:   "sha256:" + strings.Repeat("c", 64),
		ProfileDigest:       "sha256:" + strings.Repeat("d", 64),
		ResourceClassID:     "medium",
		WorkloadIDs:         append([]GateID(nil), ids...),
		SelectorEstimates:   selectorEstimates,
		BatchPlan:           batches,
		CompileEstimateMS:   10,
		BodyEstimateMS:      int64(len(ids)) * 10,
		EstimatedDurationMS: 10 + int64(len(ids))*10,
	}
	var err error
	group.BatchPlanDigest, err = CompileGroupBatchPlanDigest(group)
	if err != nil {
		t.Fatal(err)
	}
	group.GroupID, err = CompileGroupID(group)
	if err != nil {
		t.Fatal(err)
	}
	return group
}

func manifestTestCompileExecution(t *testing.T, group CompileGroup, index int) CompileGroupExecution {
	t.Helper()
	artifactKey, err := CompileArtifactKey(group)
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("e", 64)
	started := int64(1_000 + index*10)
	return CompileGroupExecution{
		Scope: cicontract.TimingScopeCompileGroup, Phase: cicontract.TimingTestBinaryCompile,
		GroupID: group.GroupID, ArtifactKey: artifactKey, PackageTarget: group.PackageTarget,
		WorkloadIDs: append([]GateID(nil), group.WorkloadIDs...), StartedAtUnixMS: started,
		CompletedAtUnixMS: started + 5, DurationMS: 5, ArtifactSHA256: digest, ArtifactSize: 1,
		CacheMisses: 1, Status: ResultStatusPassed, ExitCode: 0, CompileCommandDigest: digest,
		ProfileDigest: group.ProfileDigest, ResourceClassID: group.ResourceClassID,
	}
}

func mustManifestTestWorkload(t *testing.T, parent GateID, packageTarget, name string) Workload {
	t.Helper()
	workload, err := NewGoTestWorkload(parent, packageTarget, name, 10)
	if err != nil {
		t.Fatal(err)
	}
	return workload
}

func mustManifestTestBenchmark(t *testing.T, parent GateID, packageTarget, name string) Workload {
	t.Helper()
	workload, err := NewGoBenchmarkWorkload(parent, packageTarget, name, 10)
	if err != nil {
		t.Fatal(err)
	}
	return workload
}

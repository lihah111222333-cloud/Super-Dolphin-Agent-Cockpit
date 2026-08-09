package gate

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func TestCompileAwarePlannerSharesOnePackageCompile(t *testing.T) {
	context := testPlanningContext()
	input := compileTestInput("./internal/archtest", "sha256:"+strings.Repeat("a", 64))
	workloads, inputs := compileTestWorkloads(t, "./internal/archtest", []string{"TestAlpha", "TestBravo", "TestCharlie"}, 60_000, input)
	parent := compileParentWorkload(t, "./internal/archtest", 60_000)
	samples := compileParentAndSelectorSamples(t, parent, workloads, input.SharedInputDigest, 60_000, 10_000)
	index, err := BuildDurationSampleIndex(testPlanningLedger(context, samples), context)
	if err != nil {
		t.Fatal(err)
	}
	index.CompileTimingIndex = testCompileTimingIndex(t, input, context, "small", 2, 4, 50_000)
	shards, groups, err := planLPTWithCompileInputs(testWorkloadCatalog(workloads...), index, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || len(groups[0].WorkloadIDs) != 3 {
		t.Fatalf("compile groups = %#v, want one group covering three selectors", groups)
	}
	if len(shards) != 1 || len(shards[0].CompileGroupIDs) != 1 || shards[0].EstimatedDurationMS != 80_000 {
		t.Fatalf("shards = %#v, want one 80s compile-aware shard", shards)
	}
	if groups[0].CompileEstimateMS != 50_000 || groups[0].BodyEstimateMS != 30_000 {
		t.Fatalf("group costs = %#v, want compile=50s body=30s", groups[0])
	}
}

func TestCompileAwarePlannerExactSelectorBodyUsesSelectorInputWithoutParentAggregate(t *testing.T) {
	context := testPlanningContext()
	input := compileTestInput("./internal/archtest", "sha256:"+strings.Repeat("a", 64))
	workloads, inputs := compileTestWorkloads(t, input.PackageTarget, []string{"TestDirectBody"}, 1_000, input)
	parent := compileParentWorkload(t, input.PackageTarget, 1_000)
	selector := workloads[0]
	target := compileSelectorTarget(t, selector)
	sample := compileDurationSample(
		GoTestDurationWorkloadID(parent.ID, target.Name),
		GoTestDurationCommandDigest(parent.CommandDigest, target.Name),
		input.SharedInputDigest, 321, "small", 2, 4,
	)
	sample.TargetKind = WorkloadKindGoTest
	sample.ParentWorkloadID = parent.ID
	sample.ParentCommandDigest = parent.CommandDigest
	sample.TargetName = target.Name
	sample.TargetStatus = GoTestStatusPass
	index, err := BuildDurationSampleIndex(testPlanningLedger(context, []DurationSample{sample}), context)
	if err != nil {
		t.Fatal(err)
	}
	_, groups, err := planLPTWithCompileInputs(testWorkloadCatalog(selector), index, inputs)
	if err != nil {
		t.Fatalf("planLPTWithCompileInputs() error = %v", err)
	}
	if len(groups) != 1 || groups[0].BodyEstimateMS != 321 {
		t.Fatalf("compile group = %#v, want exact selector body 321ms without parent aggregate", groups)
	}
}

func TestCompileSelectorBodyRejectsMissingExactInputDigest(t *testing.T) {
	workload, err := NewGoTestWorkload(GateIDBackendTestWithGuard, "./internal/archtest", "TestMissingExactInput", 1_000)
	if err != nil {
		t.Fatal(err)
	}
	parent, kind, payload, targeted, err := ParseWorkloadID(workload.ID)
	if err != nil || !targeted {
		t.Fatalf("ParseWorkloadID(%q) = parent=%q kind=%q targeted=%t err=%v", workload.ID, parent, kind, targeted, err)
	}
	target, err := ParseGoTestTarget(payload)
	if err != nil {
		t.Fatal(err)
	}
	index, err := BuildDurationSampleIndex(testPlanningLedger(testPlanningContext(), nil), testPlanningContext())
	if err != nil {
		t.Fatal(err)
	}
	_, err = selectorBodyEstimate(PlannedWorkload{Workload: workload, ResourceCPU: 2, ResourceMemoryGiB: 4}, parent, kind, target, index)
	if err == nil || !strings.Contains(err.Error(), "exact production input digest") {
		t.Fatalf("selectorBodyEstimate() error = %v, want missing exact input digest guard", err)
	}
}

func TestCompileAwarePlannerKeepsHigherBodyTierWhenOwnerIsSmall(t *testing.T) {
	context := testPlanningContext()
	input := compileTestInput("./internal/archtest", "sha256:"+strings.Repeat("b", 64))
	workloads, inputs := compileTestWorkloads(t, input.PackageTarget, []string{"TestBodySlow"}, 1_000, input)
	parent := compileParentWorkload(t, input.PackageTarget, 1_000)
	selector := workloads[0]
	target := compileSelectorTarget(t, selector)
	workloadSample := compileDurationSample(selector.ID, selector.CommandDigest, input.SharedInputDigest, 80_000, "small", 2, 4)
	bodySample := compileDurationSample(
		GoTestDurationWorkloadID(parent.ID, target.Name),
		GoTestDurationCommandDigest(parent.CommandDigest, target.Name),
		input.SharedInputDigest, 321, "maximum", 8, 16,
	)
	bodySample.TargetKind = WorkloadKindGoTest
	bodySample.ParentWorkloadID = parent.ID
	bodySample.ParentCommandDigest = parent.CommandDigest
	bodySample.TargetName = target.Name
	bodySample.TargetStatus = GoTestStatusPass
	index, err := BuildDurationSampleIndex(testPlanningLedger(context, []DurationSample{workloadSample, bodySample}), context)
	if err != nil {
		t.Fatal(err)
	}
	index.CompileTimingIndex = testCompileTimingIndex(t, input, context, "small", 2, 4, 1_000)
	_, groups, err := planLPTWithCompileInputs(testWorkloadCatalog(selector), index, inputs)
	if err != nil {
		t.Fatalf("planLPTWithCompileInputs() error = %v", err)
	}
	if len(groups) != 1 || groups[0].ResourceClassID != "maximum" || groups[0].BodyEstimateMS != 321 {
		t.Fatalf("compile group = %#v, want maximum body tier and 321ms body estimate", groups)
	}
}

func TestCompileAwarePlannerDoesNotDuplicateArtifactToMeetTarget(t *testing.T) {
	context := testPlanningContext()
	input := compileTestInput("./internal/archtest", "sha256:"+strings.Repeat("b", 64))
	workloads, inputs := compileTestWorkloads(t, "./internal/archtest", []string{"TestAlpha", "TestBravo", "TestCharlie"}, 90_000, input)
	parent := compileParentWorkload(t, "./internal/archtest", 90_000)
	samples := compileParentAndSelectorSamples(t, parent, workloads, input.SharedInputDigest, 60_000, 20_000)
	index, err := BuildDurationSampleIndex(testPlanningLedger(context, samples), context)
	if err != nil {
		t.Fatal(err)
	}
	shards, groups, err := planLPTWithCompileInputs(testWorkloadCatalog(workloads...), index, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || len(shards) != 1 {
		t.Fatalf("groups=%d shards=%d, want one artifact-bound group", len(groups), len(shards))
	}
	if len(groups[0].WorkloadIDs) != len(workloads) || len(shards[0].CompileGroupIDs) != 1 {
		t.Fatalf("groups=%#v shards=%#v, want all selectors on one compiled artifact", groups, shards)
	}
}

func TestCompileAwarePlannerKeepsOneGroupWhenSharedCompileExceedsTarget(t *testing.T) {
	context := testPlanningContext()
	input := compileTestInput("./internal/archtest", "sha256:"+strings.Repeat("8", 64))
	workloads, inputs := compileTestWorkloads(t, "./internal/archtest", []string{"TestAlpha", "TestBravo", "TestCharlie"}, 130_000, input)
	parent := compileParentWorkload(t, "./internal/archtest", 130_000)
	samples := compileOverTargetSamples(t, parent, workloads, input.SharedInputDigest)
	index, err := BuildDurationSampleIndex(testPlanningLedger(context, samples), context)
	if err != nil {
		t.Fatal(err)
	}
	index.CompileTimingIndex = testCompileTimingIndex(t, input, context, "small", 2, 4, 120_000)
	shards, groups, err := planLPTWithCompileInputs(testWorkloadCatalog(workloads...), index, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || len(groups[0].WorkloadIDs) != len(workloads) {
		t.Fatalf("compile groups = %#v, want one group covering all selectors", groups)
	}
	if groups[0].CompileEstimateMS != 120_000 || groups[0].BodyEstimateMS != 30_000 {
		t.Fatalf("group costs = %#v, want compile=120s body=30s", groups[0])
	}
	if len(shards) != 1 || len(shards[0].CompileGroupIDs) != 1 || shards[0].EstimatedDurationMS != 150_000 {
		t.Fatalf("shards = %#v, want one 150s compile-aware shard", shards)
	}
}

func TestCompileAwarePlannerEmptyLedgerSplitsArchtestIntoBoundedCompileGroups(t *testing.T) {
	input := compileTestInput("./internal/archtest", "sha256:"+strings.Repeat("7", 64))
	names := archtestPlanningSelectorNames(423)
	workloads, inputs := compileTestWorkloads(t, input.PackageTarget, names, 1_000, input)
	context := testCalibrationPlanningContext()
	durationIndex, err := BuildDurationSampleIndex(testPlanningLedger(context, nil), context)
	if err != nil {
		t.Fatal(err)
	}
	catalog := testWorkloadCatalog(workloads...)
	shards, groups, err := planLPTWithCompileInputs(catalog, durationIndex, inputs)
	if err != nil {
		t.Fatal(err)
	}
	groupsByID := assertBoundedArchtestGroups(t, groups, len(names), len(workloads))
	assertBoundedArchtestShards(t, shards, groupsByID)
	assertStoredBoundedArchtestPlan(t, catalog, workloads, groups, shards)
}

func assertBoundedArchtestGroups(t *testing.T, groups []CompileGroup, selectorCount, workloadCount int) map[string]CompileGroup {
	t.Helper()
	wantGroups := (selectorCount + cicontract.CompileGroupMaxSelectors - 1) / cicontract.CompileGroupMaxSelectors
	if len(groups) != wantGroups {
		t.Fatalf("groups=%d workloads=%d, want %d bounded archtest groups", len(groups), workloadCount, wantGroups)
	}
	seen := make(map[GateID]struct{}, workloadCount)
	groupsByID := make(map[string]CompileGroup, len(groups))
	for _, group := range groups {
		assertBoundedArchtestGroup(t, group, seen)
		groupsByID[group.GroupID] = group
	}
	if len(seen) != workloadCount {
		t.Fatalf("archtest selector coverage = %d, want %d", len(seen), workloadCount)
	}
	return groupsByID
}

func assertBoundedArchtestGroup(t *testing.T, group CompileGroup, seen map[GateID]struct{}) {
	t.Helper()
	if len(group.WorkloadIDs) == 0 || len(group.WorkloadIDs) > cicontract.CompileGroupMaxSelectors {
		t.Fatalf("archtest group %q contains %d selectors, want <=%d", group.GroupID, len(group.WorkloadIDs), cicontract.CompileGroupMaxSelectors)
	}
	if len(group.BatchPlan) != 1 || len(group.BatchPlan[0].SelectorIDs) != len(group.WorkloadIDs) {
		t.Fatalf("archtest group %q batch plan = %#v, want one batch covering the group", group.GroupID, group.BatchPlan)
	}
	if group.BatchPlan[0].Wave != 0 || group.BatchPlan[0].Exclusive || group.BatchPlanWarning != "" {
		t.Fatalf("archtest group %q batch = %#v warning=%q, want non-exclusive wave 0 without warning", group.GroupID, group.BatchPlan[0], group.BatchPlanWarning)
	}
	for _, selectorID := range group.WorkloadIDs {
		if _, duplicate := seen[selectorID]; duplicate {
			t.Fatalf("selector %q appears in multiple archtest groups", selectorID)
		}
		seen[selectorID] = struct{}{}
	}
}

func assertBoundedArchtestShards(t *testing.T, shards []ShardPlan, groups map[string]CompileGroup) {
	t.Helper()
	if len(shards) != len(groups) {
		t.Fatalf("shards=%d groups=%d, want one shard per bounded group", len(shards), len(groups))
	}
	for _, shard := range shards {
		if len(shard.CompileGroupIDs) != 1 {
			t.Fatalf("shard %d compile groups = %v, want one bounded group per ECI shard", shard.Index, shard.CompileGroupIDs)
		}
		if _, ok := groups[shard.CompileGroupIDs[0]]; !ok {
			t.Fatalf("shard %d references unknown group %q", shard.Index, shard.CompileGroupIDs[0])
		}
	}
}

func assertStoredBoundedArchtestPlan(t *testing.T, catalog WorkloadCatalog, workloads []Workload, groups []CompileGroup, shards []ShardPlan) {
	t.Helper()
	executionIDs := make([]GateID, len(workloads))
	for index, workload := range workloads {
		executionIDs[index] = GateID(workload.ID)
	}
	stored := WorkloadExecutionPlan{ExecutionWorkloadIDs: executionIDs, CompileGroups: groups, Shards: shards}
	if _, _, err := validateStoredCompileGroups(stored, catalog); err != nil {
		t.Fatalf("bounded archtest groups should validate across independent shards: %v", err)
	}
}

func TestCompileGroupRejectsArchtestSelectorGroupOverBound(t *testing.T) {
	input := compileTestInput(AtomicArchtestPackageTarget, "sha256:"+strings.Repeat("9", 64))
	names := archtestPlanningSelectorNames(cicontract.CompileGroupMaxSelectors + 1)
	workloads, inputs := compileTestWorkloads(t, input.PackageTarget, names, 1_000, input)
	context := testCalibrationPlanningContext()
	index, err := BuildDurationSampleIndex(testPlanningLedger(context, nil), context)
	if err != nil {
		t.Fatal(err)
	}
	_, groups, err := planLPTWithCompileInputs(testWorkloadCatalog(workloads...), index, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || len(groups[0].WorkloadIDs)+len(groups[1].WorkloadIDs) != len(names) {
		t.Fatalf("planner groups = %#v, want bounded 64+1 selector coverage", groups)
	}
	for _, estimate := range groups[1].SelectorEstimates {
		groups[0].WorkloadIDs = append(groups[0].WorkloadIDs, estimate.SelectorID)
		groups[0].SelectorEstimates = append(groups[0].SelectorEstimates, estimate)
		groups[0].BatchPlan[0].SelectorIDs = append(groups[0].BatchPlan[0].SelectorIDs, estimate.SelectorID)
		groups[0].BodyEstimateMS += estimate.BodyEstimateMS
		groups[0].BatchPlan[0].EstimatedBodyMS += estimate.BodyEstimateMS
		groups[0].EstimatedDurationMS += estimate.BodyEstimateMS
	}
	slices.Sort(groups[0].BatchPlan[0].SelectorIDs)
	slices.SortFunc(groups[0].SelectorEstimates, func(left, right CompileSelectorEstimate) int {
		return strings.Compare(string(left.SelectorID), string(right.SelectorID))
	})
	groups[0].BatchPlanDigest, err = CompileGroupBatchPlanDigest(groups[0])
	if err != nil {
		t.Fatal(err)
	}
	groups[0].GroupID, err = CompileGroupID(groups[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := groups[0].Validate(); err == nil || !strings.Contains(err.Error(), "exceeds selector bound") {
		t.Fatalf("over-bound archtest group validation error = %v", err)
	}
}

func TestStoredCompilePlanRejectsArchtestArtifactDuplicateWithinShard(t *testing.T) {
	input := compileTestInput(AtomicArchtestPackageTarget, "sha256:"+strings.Repeat("a", 64))
	workloads, inputs := compileTestWorkloads(t, input.PackageTarget, []string{"TestArchA", "TestArchB"}, 1_000, input)
	catalog := testWorkloadCatalog(workloads...)
	context := testCalibrationPlanningContext()
	index, err := BuildDurationSampleIndex(testPlanningLedger(context, nil), context)
	if err != nil {
		t.Fatal(err)
	}
	_, groups, err := planLPTWithCompileInputs(catalog, index, inputs)
	if err != nil {
		t.Fatal(err)
	}
	first := splitCompileGroupFixture(t, groups[0], groups[0].WorkloadIDs[:1], 1_000)
	second := splitCompileGroupFixture(t, groups[0], groups[0].WorkloadIDs[1:], 1_000)
	executionIDs := []GateID{GateID(workloads[0].ID), GateID(workloads[1].ID)}
	plan := WorkloadExecutionPlan{
		ExecutionWorkloadIDs: executionIDs,
		CompileGroups:        []CompileGroup{first, second},
		Shards:               []ShardPlan{{Index: 0, CompileGroupIDs: []string{first.GroupID, second.GroupID}}},
	}
	if _, _, err := validateStoredCompileGroups(plan, catalog); err == nil || !strings.Contains(err.Error(), "contains multiple groups for compile artifact") {
		t.Fatalf("same-shard archtest artifact duplicate validation error = %v", err)
	}
}

// archtestPlanningSelectorNames 生成确定性的空账本 archtest selector 集合。
func archtestPlanningSelectorNames(count int) []string {
	names := make([]string, count)
	for index := range names {
		names[index] = fmt.Sprintf("TestArch%03d", index)
	}
	return names
}

func TestStoredCompilePlanRejectsDuplicateArtifactResourceBinding(t *testing.T) {
	input := compileTestInput("./internal/example", "sha256:"+strings.Repeat("6", 64))
	workloads, inputs := compileTestWorkloads(t, input.PackageTarget, []string{"TestAlpha", "TestBravo", "TestCharlie"}, 1_000, input)
	catalog := testWorkloadCatalog(workloads...)
	context := testCalibrationPlanningContext()
	durationIndex, err := BuildDurationSampleIndex(testPlanningLedger(context, nil), context)
	if err != nil {
		t.Fatal(err)
	}
	_, groups, err := planLPTWithCompileInputs(catalog, durationIndex, inputs)
	if err != nil {
		t.Fatal(err)
	}
	first := splitCompileGroupFixture(t, groups[0], groups[0].WorkloadIDs[:1], 1_000)
	second := splitCompileGroupFixture(t, groups[0], groups[0].WorkloadIDs[1:], 2_000)
	executionIDs := make([]GateID, len(workloads))
	for index, workload := range workloads {
		executionIDs[index] = GateID(workload.ID)
	}
	plan := WorkloadExecutionPlan{
		ExecutionWorkloadIDs: executionIDs,
		CompileGroups:        []CompileGroup{first, second},
		Shards: []ShardPlan{
			{Index: 0, CompileGroupIDs: []string{first.GroupID}},
			{Index: 1, CompileGroupIDs: []string{second.GroupID}},
		},
	}
	if _, _, err := validateStoredCompileGroups(plan, catalog); err == nil || !strings.Contains(err.Error(), "is duplicated by groups") {
		t.Fatalf("duplicate artifact validation error = %v", err)
	}
}

func TestStoredCompilePlanRejectsDifferentArtifactsInOneShard(t *testing.T) {
	firstInput := compileTestInput("./internal/archtest", "sha256:"+strings.Repeat("2", 64))
	secondInput := compileTestInput("./internal/devtools/gate", "sha256:"+strings.Repeat("3", 64))
	first, firstInputs := compileTestWorkloads(t, firstInput.PackageTarget, []string{"TestStoredArtifactFirst"}, 1_000, firstInput)
	second, secondInputs := compileTestWorkloads(t, secondInput.PackageTarget, []string{"TestStoredArtifactSecond"}, 1_000, secondInput)
	workloads := append(first, second...)
	inputs := mergeCompileInputs(firstInputs, secondInputs)
	context := testPlanningContext()
	index, err := BuildDurationSampleIndex(testPlanningLedger(context, nil), context)
	if err != nil {
		t.Fatal(err)
	}
	_, groups, err := planLPTWithCompileInputs(testWorkloadCatalog(workloads...), index, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 {
		t.Fatalf("groups=%#v, want two artifact groups", groups)
	}
	firstArtifact, err := CompileArtifactKey(groups[0])
	if err != nil {
		t.Fatal(err)
	}
	secondArtifact, err := CompileArtifactKey(groups[1])
	if err != nil {
		t.Fatal(err)
	}
	if firstArtifact == secondArtifact {
		t.Fatal("fixture groups unexpectedly share an artifact")
	}
	executionIDs := []GateID{GateID(workloads[0].ID), GateID(workloads[1].ID)}
	plan := WorkloadExecutionPlan{
		ExecutionWorkloadIDs: executionIDs,
		CompileGroups:        groups,
		Shards:               []ShardPlan{{Index: 0, Workloads: []PlannedWorkload{{Workload: workloads[0], EstimatedDurationMS: 1_000, ResourceCPU: 2, ResourceMemoryGiB: 4}, {Workload: workloads[1], EstimatedDurationMS: 1_000, ResourceCPU: 2, ResourceMemoryGiB: 4}}, CompileGroupIDs: []string{groups[0].GroupID, groups[1].GroupID}, EstimatedDurationMS: groups[0].EstimatedDurationMS + groups[1].EstimatedDurationMS}},
	}
	if _, _, err := validateStoredCompileGroups(plan, testWorkloadCatalog(workloads...)); err == nil || !strings.Contains(err.Error(), "exactly one compile group") {
		t.Fatalf("different-artifact shard validation error = %v", err)
	}
}

func splitCompileGroupFixture(t *testing.T, source CompileGroup, workloadIDs []GateID, bodyEstimate int64) CompileGroup {
	t.Helper()
	group := source
	group.WorkloadIDs = append([]GateID(nil), workloadIDs...)
	group.BodyEstimateMS = bodyEstimate
	group.EstimatedDurationMS = group.CompileEstimateMS + bodyEstimate
	selectorBodies := make(map[GateID]int64, len(source.SelectorEstimates))
	for _, estimate := range source.SelectorEstimates {
		selectorBodies[estimate.SelectorID] = estimate.BodyEstimateMS
	}
	group.SelectorEstimates = make([]CompileSelectorEstimate, len(workloadIDs))
	for index, id := range workloadIDs {
		group.SelectorEstimates[index] = CompileSelectorEstimate{SelectorID: id, BodyEstimateMS: selectorBodies[id]}
	}
	slices.SortFunc(group.SelectorEstimates, func(left, right CompileSelectorEstimate) int {
		return strings.Compare(string(left.SelectorID), string(right.SelectorID))
	})
	group.BatchPlan = []CompileGroupBatch{{BatchID: "batch-000", SelectorIDs: append([]GateID(nil), workloadIDs...), EstimatedBodyMS: bodyEstimate}}
	slices.Sort(group.BatchPlan[0].SelectorIDs)
	group.BatchPlanDigest = ""
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

func TestCompileAwarePlannerSeparatesPackageAffinity(t *testing.T) {
	context := testPlanningContext()
	firstInput := compileTestInput("./internal/archtest", "sha256:"+strings.Repeat("c", 64))
	secondInput := compileTestInput("./internal/gate", "sha256:"+strings.Repeat("d", 64))
	first, firstInputs := compileTestWorkloads(t, firstInput.PackageTarget, []string{"TestAlpha"}, 60_000, firstInput)
	second, secondInputs := compileTestWorkloads(t, secondInput.PackageTarget, []string{"TestAlpha"}, 60_000, secondInput)
	all := append(first, second...)
	inputs := mergeCompileInputs(firstInputs, secondInputs)
	samples := append(compileParentAndSelectorSamples(t, compileParentWorkload(t, firstInput.PackageTarget, 60_000), first, firstInput.SharedInputDigest, 60_000, 10_000), compileParentAndSelectorSamples(t, compileParentWorkload(t, secondInput.PackageTarget, 60_000), second, secondInput.SharedInputDigest, 60_000, 10_000)...)
	index, err := BuildDurationSampleIndex(testPlanningLedger(context, samples), context)
	if err != nil {
		t.Fatal(err)
	}
	shards, groups, err := planLPTWithCompileInputs(testWorkloadCatalog(all...), index, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || len(shards) != 2 || len(shards[0].CompileGroupIDs) != 1 || len(shards[1].CompileGroupIDs) != 1 {
		t.Fatalf("groups=%d shards=%#v, want two package groups in separate target-bound shards", len(groups), shards)
	}
	firstKey, err := CompileArtifactKey(groups[0])
	if err != nil {
		t.Fatal(err)
	}
	secondKey, err := CompileArtifactKey(groups[1])
	if err != nil {
		t.Fatal(err)
	}
	if firstKey == secondKey {
		t.Fatal("different packages shared one compile artifact key")
	}
}

func TestCompileAwarePlannerKeepsDifferentArtifactsInSeparateShards(t *testing.T) {
	firstInput := compileTestInput("./internal/archtest", "sha256:"+strings.Repeat("e", 64))
	secondInput := compileTestInput("./internal/devtools/gate", "sha256:"+strings.Repeat("f", 64))
	first, firstInputs := compileTestWorkloads(t, firstInput.PackageTarget, []string{"TestArtifactFirst"}, 1_000, firstInput)
	second, secondInputs := compileTestWorkloads(t, secondInput.PackageTarget, []string{"TestArtifactSecond"}, 1_000, secondInput)
	inputs := mergeCompileInputs(firstInputs, secondInputs)
	index, err := BuildDurationSampleIndex(testPlanningLedger(testPlanningContext(), nil), testPlanningContext())
	if err != nil {
		t.Fatal(err)
	}
	shards, groups, err := planLPTWithCompileInputs(testWorkloadCatalog(append(first, second...)...), index, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || len(shards) != 2 {
		t.Fatalf("groups=%d shards=%d, want each artifact in its own shard", len(groups), len(shards))
	}
	for _, shard := range shards {
		if len(shard.CompileGroupIDs) != 1 {
			t.Fatalf("shard %d compile groups=%v, want exactly one compile group", shard.Index, shard.CompileGroupIDs)
		}
	}
}

func TestCompileAwarePlannerRejectsMissingSelectorInput(t *testing.T) {
	workload, err := NewGoTestWorkload(GateIDBackendTestWithGuard, "./internal/archtest", "TestMissingInput", 1_000)
	if err != nil {
		t.Fatal(err)
	}
	context := testPlanningContext()
	index, err := BuildDurationSampleIndex(testPlanningLedger(context, nil), context)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = planLPTWithCompileInputs(testWorkloadCatalog(workload), index, nil)
	if err == nil || !strings.Contains(err.Error(), "compile input is missing") {
		t.Fatalf("missing compile input error = %v", err)
	}
}

func TestCompileAwarePlannerGroupsBenchmarksSeparately(t *testing.T) {
	workload, err := NewGoBenchmarkWorkload(GateIDBackendTestWithGuard, "./internal/archtest", "BenchmarkAlpha", 20_000)
	if err != nil {
		t.Fatal(err)
	}
	input := CompileGroupInput{PackageTarget: "./internal/archtest", SemanticKey: CompileGroupSemanticGoBenchmark, SharedInputDigest: "sha256:" + strings.Repeat("f", 64), ProfileDigest: "sha256:" + strings.Repeat("e", 64)}
	context := testPlanningContext()
	index, err := BuildDurationSampleIndex(testPlanningLedger(context, nil), context)
	if err != nil {
		t.Fatal(err)
	}
	shards, groups, err := planLPTWithCompileInputs(testWorkloadCatalog(workload), index, map[GateID]CompileGroupInput{GateID(workload.ID): input})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || len(shards) != 1 || groups[0].WorkloadIDs[0] != GateID(workload.ID) {
		t.Fatalf("benchmark groups=%#v shards=%#v, want one benchmark compile group", groups, shards)
	}
}

func TestCompileAwarePlannerOnlyPlansExecutionMisses(t *testing.T) {
	input := compileTestInput("./internal/archtest", "sha256:"+strings.Repeat("1", 64))
	workloads, inputs := compileTestWorkloads(t, "./internal/archtest", []string{"TestMiss", "TestPass"}, 20_000, input)
	projected, err := workloadExecutionCatalog(testWorkloadCatalog(workloads...), []GateID{GateID(workloads[0].ID)})
	if err != nil {
		t.Fatal(err)
	}
	context := testPlanningContext()
	index, err := BuildDurationSampleIndex(testPlanningLedger(context, nil), context)
	if err != nil {
		t.Fatal(err)
	}
	missID := GateID(workloads[0].ID)
	missInputs := map[GateID]CompileGroupInput{missID: inputs[missID]}
	_, groups, err := planLPTWithCompileInputs(projected, index, missInputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || len(groups[0].WorkloadIDs) != 1 || groups[0].WorkloadIDs[0] != missID {
		t.Fatalf("miss-only groups=%#v, matched PASS selector leaked", groups)
	}
}

func TestCompileAwarePlannerRejectsInputsOutsideExecutionCatalog(t *testing.T) {
	input := compileTestInput("./internal/archtest", "sha256:"+strings.Repeat("7", 64))
	workloads, inputs := compileTestWorkloads(t, input.PackageTarget, []string{"TestMiss", "TestPass"}, 20_000, input)
	projected, err := workloadExecutionCatalog(testWorkloadCatalog(workloads...), []GateID{GateID(workloads[0].ID)})
	if err != nil {
		t.Fatal(err)
	}
	context := testPlanningContext()
	index, err := BuildDurationSampleIndex(testPlanningLedger(context, nil), context)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = planLPTWithCompileInputs(projected, index, inputs)
	if err == nil || !strings.Contains(err.Error(), "outside the execution catalog") {
		t.Fatalf("extra PASS compile input error = %v, want strict execution projection rejection", err)
	}
}

func TestCompileAwarePlannerSeparatesPackageAffinityAcrossResourceTiers(t *testing.T) {
	firstInput := compileTestInput("./internal/archtest", "sha256:"+strings.Repeat("2", 64))
	secondInput := compileTestInput("./internal/gate", "sha256:"+strings.Repeat("9", 64))
	firstWorkloads, firstInputs := compileTestWorkloads(t, firstInput.PackageTarget, []string{"TestFast"}, 4_000, firstInput)
	secondWorkloads, secondInputs := compileTestWorkloads(t, secondInput.PackageTarget, []string{"TestSlow"}, 80_000, secondInput)
	workloads := append(firstWorkloads, secondWorkloads...)
	inputs := mergeCompileInputs(firstInputs, secondInputs)
	context := testPlanningContext()
	samples := append(
		compileParentAndSelectorSamples(t, compileParentWorkload(t, firstInput.PackageTarget, 4_000), firstWorkloads, firstInput.SharedInputDigest, 4_000, 4_000),
		compileParentAndSelectorSamples(t, compileParentWorkload(t, secondInput.PackageTarget, 80_000), secondWorkloads, secondInput.SharedInputDigest, 80_000, 80_000)...,
	)
	index, err := BuildDurationSampleIndex(testPlanningLedger(context, samples), context)
	if err != nil {
		t.Fatal(err)
	}
	firstCompileHistory := testCompileTimingIndex(t, firstInput, context, "small", 2, 4, 4_000)
	secondCompileHistory := testCompileTimingIndex(t, secondInput, context, "small", 2, 4, 80_000)
	index.CompileTimingIndex, err = BuildCompileTimingIndex(append(firstCompileHistory.Samples, secondCompileHistory.Samples...))
	if err != nil {
		t.Fatal(err)
	}
	shards, groups, err := planLPTWithCompileInputs(testWorkloadCatalog(workloads...), index, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || len(shards) != 2 {
		t.Fatalf("groups=%#v shards=%#v, want one package group per persisted resource tier", groups, shards)
	}
	wantClasses := map[GateID]string{
		GateID(workloads[0].ID): "small",
		GateID(workloads[1].ID): "maximum",
	}
	for _, group := range groups {
		if len(group.WorkloadIDs) != 1 {
			t.Fatalf("group=%#v, want exactly one tier-homogeneous selector", group)
		}
		wantClass, ok := wantClasses[group.WorkloadIDs[0]]
		if !ok {
			t.Fatalf("group=%#v contains an unexpected workload", group)
		}
		if group.ResourceClassID != wantClass {
			t.Fatalf("group=%#v, want resource class %q from its workload estimate", group, wantClass)
		}
	}
}

func TestCompileAwarePlannerResourceIdentityStable(t *testing.T) {
	input := compileTestInput("./internal/archtest", "sha256:"+strings.Repeat("3", 64))
	workloads, inputs := compileTestWorkloads(t, input.PackageTarget, []string{"TestFastA", "TestFastB"}, 1_000, input)
	context := testPlanningContext()
	index, err := BuildDurationSampleIndex(testPlanningLedger(context, nil), context)
	if err != nil {
		t.Fatal(err)
	}
	catalog := testWorkloadCatalog(workloads...)
	firstShards, firstGroups, err := planLPTWithCompileInputs(catalog, index, inputs)
	if err != nil {
		t.Fatal(err)
	}
	secondShards, secondGroups, err := planLPTWithCompileInputs(catalog, index, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if firstGroups[0].ResourceClassID != "small" {
		t.Fatalf("resource class = %q, want canonical fast-tier small despite aggregate duration", firstGroups[0].ResourceClassID)
	}
	if firstGroups[0].GroupID != secondGroups[0].GroupID {
		t.Fatalf("planner identity drifted across repeated planning: first=%#v/%#v second=%#v/%#v", firstGroups, firstShards, secondGroups, secondShards)
	}
}

func TestCompileAwarePlannerCalibrationUsesFixedResourceClass(t *testing.T) {
	input := compileTestInput("./internal/archtest", "sha256:"+strings.Repeat("4", 64))
	workloads, inputs := compileTestWorkloads(t, input.PackageTarget, []string{"TestFast", "TestSlow"}, 4_000, input)
	workloads[1].BootstrapEstimateMS = 80_000
	context := testCalibrationPlanningContext()
	index, err := BuildDurationSampleIndex(testPlanningLedger(context, nil), context)
	if err != nil {
		t.Fatal(err)
	}
	shards, groups, err := planLPTWithCompileInputs(testWorkloadCatalog(workloads...), index, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || len(shards) != 1 {
		t.Fatalf("groups=%#v shards=%#v, want one fixed-resource calibration group", groups, shards)
	}
	if groups[0].ResourceClassID != context.CalibrationResourceClassID {
		t.Fatalf("calibration resource class = %q, want fixed %q", groups[0].ResourceClassID, context.CalibrationResourceClassID)
	}
}

func compileTestWorkloads(t *testing.T, packageTarget string, names []string, estimate int64, input CompileGroupInput) ([]Workload, map[GateID]CompileGroupInput) {
	t.Helper()
	workloads := make([]Workload, 0, len(names))
	inputs := make(map[GateID]CompileGroupInput, len(names))
	for _, name := range names {
		workload, err := NewGoTestWorkload(GateIDBackendTestWithGuard, packageTarget, name, estimate)
		if err != nil {
			t.Fatal(err)
		}
		// Compile samples are keyed by the frozen package input.  Keep the
		// catalog projection on that same identity so planner observations are
		// comparable instead of silently falling back to bootstrap estimates.
		workload.InputDigest = input.SharedInputDigest
		workloads = append(workloads, workload)
		inputs[GateID(workload.ID)] = input
	}
	return workloads, inputs
}

func compileParentWorkload(t *testing.T, packageTarget string, estimate int64) Workload {
	t.Helper()
	parent, err := NewGoPackageWorkload(GateIDBackendTestWithGuard, packageTarget, estimate)
	if err != nil {
		t.Fatal(err)
	}
	return parent
}

func compileParentAndSelectorSamples(t *testing.T, parent Workload, workloads []Workload, inputDigest string, parentDuration, bodyDuration int64) []DurationSample {
	t.Helper()
	resourceClass, resourceCPU, resourceMemory := compileTestNormalResourceForDuration(parentDuration)
	samples := []DurationSample{
		compileDurationSample(parent.ID, parent.CommandDigest, inputDigest, parentDuration, "small", 2, 4),
	}
	if resourceClass != "small" {
		samples = append(samples, compileDurationSample(parent.ID, parent.CommandDigest, inputDigest, parentDuration, resourceClass, resourceCPU, resourceMemory))
	}
	for _, workload := range workloads {
		samples = append(samples, compileDurationSample(workload.ID, workload.CommandDigest, inputDigest, parentDuration, "small", 2, 4))
		if resourceClass != "small" {
			samples = append(samples, compileDurationSample(workload.ID, workload.CommandDigest, inputDigest, parentDuration, resourceClass, resourceCPU, resourceMemory))
		}
		_, _, payload, targeted, err := ParseWorkloadID(workload.ID)
		if err != nil {
			t.Fatalf("parse selector workload %q: %v", workload.ID, err)
		}
		if !targeted {
			t.Fatalf("selector workload %q is not targeted", workload.ID)
		}
		target, err := ParseGoTestTarget(payload)
		if err != nil {
			t.Fatalf("parse selector target %q: %v", payload, err)
		}
		targetSample := compileDurationSample(GoTestDurationWorkloadID(parent.ID, target.Name), GoTestDurationCommandDigest(parent.CommandDigest, target.Name), inputDigest, bodyDuration, resourceClass, resourceCPU, resourceMemory)
		targetSample.TargetKind = WorkloadKindGoTest
		targetSample.ParentWorkloadID = parent.ID
		targetSample.ParentCommandDigest = parent.CommandDigest
		targetSample.TargetName = target.Name
		targetSample.TargetStatus = GoTestStatusPass
		samples = append(samples, targetSample)
	}
	return samples
}

func compileOverTargetSamples(t *testing.T, parent Workload, workloads []Workload, inputDigest string) []DurationSample {
	t.Helper()
	samples := []DurationSample{
		compileDurationSample(parent.ID, parent.CommandDigest, inputDigest, 130_000, "small", 2, 4),
		compileDurationSample(parent.ID, parent.CommandDigest, inputDigest, 130_000, "large", 8, 16),
	}
	for _, workload := range workloads {
		samples = append(samples, compileDurationSample(workload.ID, workload.CommandDigest, inputDigest, 130_000, "small", 2, 4))
		samples = append(samples, compileDurationSample(workload.ID, workload.CommandDigest, inputDigest, 130_000, "large", 8, 16))
		target := compileSelectorTarget(t, workload)
		targetSample := compileDurationSample(GoTestDurationWorkloadID(parent.ID, target.Name), GoTestDurationCommandDigest(parent.CommandDigest, target.Name), inputDigest, 10_000, "large", 8, 16)
		targetSample.TargetKind = WorkloadKindGoTest
		targetSample.ParentWorkloadID = parent.ID
		targetSample.ParentCommandDigest = parent.CommandDigest
		targetSample.TargetName = target.Name
		targetSample.TargetStatus = GoTestStatusPass
		samples = append(samples, targetSample)
	}
	return samples
}

func compileTestNormalResourceForDuration(durationMS int64) (string, float64, float64) {
	switch {
	case durationMS <= 5_000:
		return "small", 2, 4
	case durationMS <= 70_000:
		return "medium", 4, 8
	default:
		return "large", 8, 16
	}
}

func compileSelectorTarget(t *testing.T, workload Workload) GoTestTarget {
	t.Helper()
	_, _, payload, targeted, err := ParseWorkloadID(workload.ID)
	if err != nil {
		t.Fatalf("parse selector workload %q: %v", workload.ID, err)
		return GoTestTarget{}
	}
	if !targeted {
		t.Fatalf("selector workload %q is not targeted", workload.ID)
		return GoTestTarget{}
	}
	target, err := ParseGoTestTarget(payload)
	if err != nil {
		t.Fatalf("parse selector target %q: %v", payload, err)
		return GoTestTarget{}
	}
	return target
}

func compileDurationSample(workloadID, commandDigest, inputDigest string, duration int64, class string, cpu, memory float64) DurationSample {
	sample := testDurationSample(workloadID, commandDigest, true, duration)
	sample.Bucket.InputDigest = inputDigest
	sample.Bucket.ResourceClassID = class
	sample.Bucket.ResourceCPU = cpu
	sample.Bucket.ResourceMemoryGiB = memory
	return sample
}

func mergeCompileInputs(left, right map[GateID]CompileGroupInput) map[GateID]CompileGroupInput {
	merged := make(map[GateID]CompileGroupInput, len(left)+len(right))
	maps.Copy(merged, left)
	maps.Copy(merged, right)
	return merged
}

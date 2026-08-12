package gate

import (
	"strings"
	"testing"
)

func TestDurationSampleIndexSeparatesCalibrationFromNormalResourceSamples(t *testing.T) {
	workload := Workload{
		ID: "guard:duration-isolation", Kind: WorkloadKindGuard,
		CommandDigest: strings.Repeat("1", 64), InputDigest: "sha256:" + strings.Repeat("a", 64),
		BootstrapEstimateMS: 1_000, Shardable: true,
	}
	ledger := DurationLedger{
		Version:       durationLedgerVersion,
		ShardOverhead: testDurationIndexOverhead(),
		Samples: []DurationSample{
			testDurationIndexSample(workload, DurationExecutionModeNormal, "small", 2, 4, 2_000),
			testDurationIndexSample(workload, DurationExecutionModeCalibration, "calibration", 4, 8, 9_000),
		},
	}
	context := PlanningContext{
		Platform: "linux/amd64", Runner: "runner-v1", Toolchain: "go1.26",
		TargetDurationMS: FullCITargetDurationMS, AcceptedSnapshotID: "snapshot-v1",
	}
	normalIndex, err := BuildDurationSampleIndex(ledger, context)
	if err != nil {
		t.Fatalf("BuildDurationSampleIndex(normal) error = %v", err)
	}
	normalEstimate, err := normalIndex.EstimateWorkloadDurationMS(workload)
	if err != nil {
		t.Fatalf("EstimateWorkloadDurationMS(normal) error = %v", err)
	}
	if normalEstimate != 2_000 {
		t.Fatalf("normal estimate = %d, want normal 2C/4GiB sample 2000", normalEstimate)
	}

	calibrationContext := context
	calibrationContext.Calibration = true
	calibrationContext.CalibrationResourceClassID = "calibration"
	calibrationContext.CalibrationResourceCPU = 4
	calibrationContext.CalibrationResourceMemoryGiB = 8
	calibrationIndex, err := BuildDurationSampleIndex(ledger, calibrationContext)
	if err != nil {
		t.Fatalf("BuildDurationSampleIndex(calibration) error = %v", err)
	}
	calibrationEstimate, err := calibrationIndex.EstimateWorkloadDurationMS(workload)
	if err != nil {
		t.Fatalf("EstimateWorkloadDurationMS(calibration) error = %v", err)
	}
	if calibrationEstimate != 9_000 {
		t.Fatalf("calibration estimate = %d, want calibration 4C/8GiB sample 9000", calibrationEstimate)
	}
}

// TestDurationSampleIndexCalibrationUsesCrossInputUpperBound 验证校准规划不会在源码输入变化后退回偏小 bootstrap，
// 而是同时参考同 workload/命令的历史上界，避免把大量不确定 workload 塞进一个分片。
func TestDurationSampleIndexCalibrationUsesCrossInputUpperBound(t *testing.T) {
	current := calibrationDurationWorkload("guard:calibration-cross-input", "b", 1_000)
	historical := current
	historical.InputDigest = "sha256:" + strings.Repeat("a", 64)
	index := mustBuildCalibrationDurationIndex(t, []DurationSample{
		testDurationIndexSample(current, DurationExecutionModeCalibration, "calibration", 4, 8, 9_000),
		testDurationIndexSample(historical, DurationExecutionModeCalibration, "calibration", 4, 8, 40_000),
	})
	estimate, err := index.EstimateWorkloadDurationMS(current)
	if err != nil {
		t.Fatal(err)
	}
	if estimate != 40_000 {
		t.Fatalf("calibration estimate = %d, want cross-input historical upper bound 40000", estimate)
	}
	currentOnlyHistory := mustBuildCalibrationDurationIndex(t, []DurationSample{
		testDurationIndexSample(historical, DurationExecutionModeCalibration, "calibration", 4, 8, 40_000),
	})
	if !currentOnlyHistory.HasSuccessfulCalibrationDurationEvidence(current) || currentOnlyHistory.HasComparableSuccessfulDurationSample(current) {
		t.Fatal("cross-input history was not distinguished from an exact comparable sample")
	}
}

// TestDurationSampleIndexCalibrationFloorsUnknownNilnessPackage 验证全新 nilness 包没有历史时仍保留单次 analyzer 冷启动预算。
func TestDurationSampleIndexCalibrationFloorsUnknownNilnessPackage(t *testing.T) {
	workload, err := NewGoPackageWorkload(GateIDBackendNilness, "./internal/newpkg", 625)
	if err != nil {
		t.Fatal(err)
	}
	workload.InputDigest = "sha256:" + strings.Repeat("c", 64)
	estimate, err := mustBuildCalibrationDurationIndex(t, nil).EstimateWorkloadDurationMS(workload)
	if err != nil {
		t.Fatal(err)
	}
	if estimate != 10_000 {
		t.Fatalf("unknown nilness calibration estimate = %d, want 10000", estimate)
	}
}

func calibrationDurationWorkload(id, inputSeed string, bootstrapMS int64) Workload {
	return Workload{ID: id, Kind: WorkloadKindGuard, CommandDigest: strings.Repeat("1", 64), InputDigest: "sha256:" + strings.Repeat(inputSeed, 64), BootstrapEstimateMS: bootstrapMS, Shardable: true}
}

func mustBuildCalibrationDurationIndex(t *testing.T, samples []DurationSample) DurationSampleIndex {
	t.Helper()
	context := PlanningContext{Platform: "linux/amd64", Runner: "runner-v1", Toolchain: "go1.26", Calibration: true, CalibrationResourceClassID: "calibration", CalibrationResourceCPU: 4, CalibrationResourceMemoryGiB: 8, TargetDurationMS: FullCITargetDurationMS, AcceptedSnapshotID: "snapshot-v1"}
	index, err := BuildDurationSampleIndex(DurationLedger{Version: durationLedgerVersion, ShardOverhead: testDurationIndexOverhead(), Samples: samples}, context)
	if err != nil {
		t.Fatal(err)
	}
	return index
}

func TestDurationSampleIndexNormalEstimateReclassifiesToExactResourceTier(t *testing.T) {
	workload := Workload{
		ID: "guard:duration-fixed-point", Kind: WorkloadKindGuard,
		CommandDigest: strings.Repeat("2", 64), InputDigest: "sha256:" + strings.Repeat("b", 64),
		BootstrapEstimateMS: 1_000, Shardable: true,
	}
	ledger := DurationLedger{
		Version:       durationLedgerVersion,
		ShardOverhead: testDurationIndexOverhead(),
		Samples: []DurationSample{
			testDurationIndexSample(workload, DurationExecutionModeNormal, "small", 2, 4, 6_000),
			testDurationIndexSample(workload, DurationExecutionModeNormal, "medium", 4, 8, 7_000),
		},
	}
	context := PlanningContext{
		Platform: "linux/amd64", Runner: "runner-v1", Toolchain: "go1.26",
		TargetDurationMS: FullCITargetDurationMS, AcceptedSnapshotID: "snapshot-v1",
	}
	index, err := BuildDurationSampleIndex(ledger, context)
	if err != nil {
		t.Fatalf("BuildDurationSampleIndex() error = %v", err)
	}
	estimate, err := index.EstimateWorkloadDurationMS(workload)
	if err != nil {
		t.Fatalf("EstimateWorkloadDurationMS() error = %v", err)
	}
	if estimate != 7_000 {
		t.Fatalf("fixed-point estimate = %d, want medium-tier sample 7000", estimate)
	}
}

func TestDurationSampleIndexNormalEstimateUpdaterFastToMediumDoesNotDowngrade(t *testing.T) {
	workload := Workload{
		ID: "go-package:./cmd/super-dolphin-updater", Kind: WorkloadKindGoTest,
		CommandDigest: strings.Repeat("5", 64), InputDigest: "sha256:" + strings.Repeat("f", 64),
		BootstrapEstimateMS: 1_000, Shardable: true,
	}
	ledger := DurationLedger{
		Version:       durationLedgerVersion,
		ShardOverhead: testDurationIndexOverhead(),
		Samples: []DurationSample{
			testDurationIndexSample(workload, DurationExecutionModeNormal, "small", 2, 4, 7_139),
			testDurationIndexSample(workload, DurationExecutionModeNormal, "medium", 4, 8, 4_733),
		},
	}
	context := PlanningContext{
		Platform: "linux/amd64", Runner: "runner-v1", Toolchain: "go1.26",
		TargetDurationMS: FullCITargetDurationMS, AcceptedSnapshotID: "snapshot-v1",
	}
	index, err := BuildDurationSampleIndex(ledger, context)
	if err != nil {
		t.Fatalf("BuildDurationSampleIndex() error = %v", err)
	}
	estimate, resource, err := index.estimateNormalWorkload(workload, context.TargetDurationMS)
	if err != nil {
		t.Fatalf("estimateNormalWorkload() error = %v", err)
	}
	if estimate != 4_733 || resource.cpu != 4 || resource.memoryGiB != 8 {
		t.Fatalf("estimateNormalWorkload() = %dms %.0fC/%.0fGiB, want medium-tier 4733ms at 4C/8GiB", estimate, resource.cpu, resource.memoryGiB)
	}
}

func TestDurationSampleIndexNormalExactGoTestKeepsAuthoritativeOverTargetEstimate(t *testing.T) {
	workload, err := NewGoTestWorkload(
		GateIDBackendTestWithGuard,
		"./internal/archtest",
		"TestAuthoritativeOverTarget",
		10_000,
	)
	if err != nil {
		t.Fatal(err)
	}
	measured := testDurationSample(workload.ID, workload.CommandDigest, true, FullCITargetDurationMS+1)
	index, err := BuildDurationSampleIndex(testPlanningLedger(testPlanningContext(), []DurationSample{measured}), testPlanningContext())
	if err != nil {
		t.Fatalf("BuildDurationSampleIndex() error = %v", err)
	}
	estimate, resource, err := index.estimateNormalWorkload(workload, FullCITargetDurationMS)
	if err != nil {
		t.Fatalf("estimateNormalWorkload() error = %v", err)
	}
	if estimate != FullCITargetDurationMS+1 || resource.cpu != 8 || resource.memoryGiB != 16 {
		t.Fatalf("exact Go test estimate = %dms at %.0fC/%.0fGiB, want authoritative %dms at 8C/16GiB", estimate, resource.cpu, resource.memoryGiB, FullCITargetDurationMS+1)
	}
}

func TestGoTestDurationMSAtResourceQueriesTargetWithoutParentAggregate(t *testing.T) {
	parent, err := NewGoPackageWorkload(GateIDBackendTestWithGuard, "./internal/archtest", 1_000)
	if err != nil {
		t.Fatal(err)
	}
	const testName = "TestDirectSelectorBody"
	inputDigest := "sha256:" + strings.Repeat("9", 64)
	otherInputDigest := "sha256:" + strings.Repeat("8", 64)
	parent.InputDigest = inputDigest
	target := DurationSample{
		Bucket: DurationBucket{
			WorkloadID:    GoTestDurationWorkloadID(parent.ID, testName),
			CommandDigest: GoTestDurationCommandDigest(parent.CommandDigest, testName),
			InputDigest:   inputDigest,
			Platform:      "darwin", Runner: "local", Toolchain: "go1.25",
			ExecutionMode: DurationExecutionModeNormal, ResourceClassID: "small", ResourceCPU: 2, ResourceMemoryGiB: 4,
		},
		Succeeded: true, DurationMS: 125,
		TargetKind: WorkloadKindGoTest, ParentWorkloadID: parent.ID,
		ParentCommandDigest: parent.CommandDigest, TargetName: testName, TargetStatus: GoTestStatusPass,
	}
	otherTarget := target
	otherTarget.Bucket.InputDigest = otherInputDigest
	otherTarget.DurationMS = 999
	calibrationParent := testCalibrationDurationSample(parent.ID, parent.CommandDigest, true, 9_000)
	index, err := BuildDurationSampleIndex(testPlanningLedger(testPlanningContext(), []DurationSample{target, otherTarget, calibrationParent}), testPlanningContext())
	if err != nil {
		t.Fatalf("BuildDurationSampleIndex() error = %v", err)
	}
	if got, ok := index.GoTestDurationMSAtResource(parent, testName, 2, 4); !ok || got != 125 {
		t.Fatalf("GoTestDurationMSAtResource() = %d, %t; want direct target body 125ms without normal parent aggregate", got, ok)
	}
}

// TestDurationSampleIndexNormalEstimateCarriesMeasuredDurationIntoNewTier 锁定首次升档保留权威实测估值的固定点语义。
func TestDurationSampleIndexNormalEstimateCarriesMeasuredDurationIntoNewTier(t *testing.T) {
	workload := Workload{
		ID: "guard:duration-missing-tier", Kind: WorkloadKindGuard,
		CommandDigest: strings.Repeat("3", 64), InputDigest: "sha256:" + strings.Repeat("c", 64),
		BootstrapEstimateMS: 1_000, Shardable: true,
	}
	ledger := DurationLedger{
		Version:       durationLedgerVersion,
		ShardOverhead: testDurationIndexOverhead(),
		Samples: []DurationSample{
			testDurationIndexSample(workload, DurationExecutionModeNormal, "small", 2, 4, 6_000),
		},
	}
	context := PlanningContext{
		Platform: "linux/amd64", Runner: "runner-v1", Toolchain: "go1.26",
		TargetDurationMS: FullCITargetDurationMS, AcceptedSnapshotID: "snapshot-v1",
	}
	index, err := BuildDurationSampleIndex(ledger, context)
	if err != nil {
		t.Fatalf("BuildDurationSampleIndex() error = %v", err)
	}
	estimate, resource, err := index.estimateNormalWorkload(workload, context.TargetDurationMS)
	if err != nil {
		t.Fatalf("estimateNormalWorkload() error = %v", err)
	}
	if estimate != 6_000 || resource.cpu != 4 || resource.memoryGiB != 8 {
		t.Fatalf("estimateNormalWorkload() = %dms %.0fC/%.0fGiB, want carried 6000ms at 4C/8GiB", estimate, resource.cpu, resource.memoryGiB)
	}
}

// TestDurationSampleIndexNormalBootstrapAlwaysUsesFastTier 锁定无权威样本时三类 workload 都从 2C/4GiB 启动。
func TestDurationSampleIndexNormalBootstrapAlwaysUsesFastTier(t *testing.T) {
	workload := Workload{
		ID: "guard:duration-bootstrap-fast-tier", Kind: WorkloadKindGuard,
		CommandDigest: strings.Repeat("4", 64), InputDigest: "sha256:" + strings.Repeat("e", 64),
		BootstrapEstimateMS: 90_000, Shardable: true,
	}
	context := PlanningContext{
		Platform: "linux/amd64", Runner: "runner-v1", Toolchain: "go1.26",
		TargetDurationMS: FullCITargetDurationMS, AcceptedSnapshotID: "snapshot-v1",
	}
	index, err := BuildDurationSampleIndex(DurationLedger{
		Version: durationLedgerVersion, ShardOverhead: testDurationIndexOverhead(),
	}, context)
	if err != nil {
		t.Fatalf("BuildDurationSampleIndex() error = %v", err)
	}
	estimate, resource, err := index.estimateNormalWorkload(workload, context.TargetDurationMS)
	if err != nil {
		t.Fatalf("estimateNormalWorkload() error = %v", err)
	}
	if estimate != workload.BootstrapEstimateMS || resource.cpu != 2 || resource.memoryGiB != 4 {
		t.Fatalf("estimateNormalWorkload() = %dms %.0fC/%.0fGiB, want bootstrap 90000ms at 2C/4GiB", estimate, resource.cpu, resource.memoryGiB)
	}
}

func testDurationIndexOverhead() *ShardOrchestrationOverhead {
	return &ShardOrchestrationOverhead{
		SchemaVersion: ShardOrchestrationOverheadSchemaVersion, PolicyVersion: ShardOverheadPolicyVersion,
		Platform: "linux/amd64", Runner: "runner-v1", Toolchain: "go1.26",
		CalibrationResourceClassID: "calibration", CalibrationResourceCPU: 4, CalibrationResourceMemoryGiB: 8,
		P95MS: 0, SampleCount: 1, ProvenanceDigest: "sha256:" + strings.Repeat("d", 64),
		AcceptedGeneration: 1, AcceptedSnapshotID: "snapshot-v1",
	}
}

func testDurationIndexSample(workload Workload, mode, classID string, cpu, memoryGiB float64, durationMS int64) DurationSample {
	return DurationSample{
		Bucket: DurationBucket{
			WorkloadID: workload.ID, CommandDigest: workload.CommandDigest, InputDigest: workload.InputDigest,
			Platform: "linux/amd64", Runner: "runner-v1", Toolchain: "go1.26",
			ExecutionMode: mode, ResourceClassID: classID, ResourceCPU: cpu, ResourceMemoryGiB: memoryGiB,
		},
		Succeeded: true, DurationMS: durationMS,
	}
}

func TestDurationEstimatorV2OutlierAndPermutationDeterministic(t *testing.T) {
	context := PlanningContext{Platform: "linux/amd64", Runner: "runner-v1", Toolchain: "go1.26", TargetDurationMS: FullCITargetDurationMS, AcceptedSnapshotID: "snapshot-v1"}
	workload := Workload{ID: "guard:robust-outlier", Kind: WorkloadKindGuard, CommandDigest: strings.Repeat("a", 64), InputDigest: "sha256:" + strings.Repeat("b", 64), BootstrapEstimateMS: 100, Shardable: true}
	makeSample := func(duration int64) DurationSample {
		sample := testDurationIndexSample(workload, DurationExecutionModeNormal, "small", 2, 4, duration)
		sample.AcceptedGeneration = 3
		return sample
	}
	ledger := testPlanningLedger(context, []DurationSample{makeSample(1), makeSample(1), makeSample(1_000)})
	index, err := BuildDurationSampleIndex(ledger, context)
	if err != nil {
		t.Fatal(err)
	}
	got, err := index.EstimateWorkloadDurationMS(workload)
	if err != nil {
		t.Fatal(err)
	}
	// Generation median is 1ms; n=3 shrinks toward the 100ms workload prior: (1*3+100*2)/5.
	if got != 40 {
		t.Fatalf("robust outlier estimate = %d, want 40", got)
	}
	permuted := testPlanningLedger(context, []DurationSample{makeSample(1_000), makeSample(1), makeSample(1)})
	permutedIndex, err := BuildDurationSampleIndex(permuted, context)
	if err != nil {
		t.Fatal(err)
	}
	permutedGot, err := permutedIndex.EstimateWorkloadDurationMS(workload)
	if err != nil {
		t.Fatal(err)
	}
	if permutedGot != got {
		t.Fatalf("permutation changed estimate: got %d want %d", permutedGot, got)
	}
}

func TestDurationEstimatorV2WeightsLatestGenerationAndRetainsFailureDiagnostics(t *testing.T) {
	context := PlanningContext{Platform: "linux/amd64", Runner: "runner-v1", Toolchain: "go1.26", TargetDurationMS: FullCITargetDurationMS, AcceptedSnapshotID: "snapshot-v1"}
	workload := Workload{ID: "guard:generation-weight", Kind: WorkloadKindGuard, CommandDigest: strings.Repeat("c", 64), InputDigest: "sha256:" + strings.Repeat("d", 64), BootstrapEstimateMS: 100, Shardable: true}
	sample := func(generation uint64, duration int64, succeeded bool) DurationSample {
		result := testDurationIndexSample(workload, DurationExecutionModeNormal, "small", 2, 4, duration)
		result.AcceptedGeneration, result.Succeeded = generation, succeeded
		return result
	}
	index, err := BuildDurationSampleIndex(testPlanningLedger(context, []DurationSample{
		sample(1, 1, true), sample(2, 10, true), sample(3, 100, true), sample(3, 200, false),
	}), context)
	if err != nil {
		t.Fatal(err)
	}
	got, err := index.EstimateWorkloadDurationMS(workload)
	if err != nil {
		t.Fatal(err)
	}
	if got != 100 {
		t.Fatalf("weighted latest-generation estimate = %d, want 100", got)
	}
	if gotFailures := index.FailureDiagnostics(workload); len(gotFailures) != 1 || gotFailures[0].DurationMS != 200 {
		t.Fatalf("failure diagnostics = %#v, want one 200ms failure", gotFailures)
	}
	if len(index.AcceptedGenerations) != 3 || index.AcceptedGenerations[0] != 3 {
		t.Fatalf("accepted generations = %v, want [3 2 1]", index.AcceptedGenerations)
	}
}

func TestDurationEstimatorV2SparseNewestGenerationShrinksSinglePoint(t *testing.T) {
	context := PlanningContext{Platform: "linux/amd64", Runner: "runner-v1", Toolchain: "go1.26", TargetDurationMS: FullCITargetDurationMS, AcceptedSnapshotID: "snapshot-v1"}
	workload := Workload{ID: "guard:sparse-newest", Kind: WorkloadKindGuard, CommandDigest: strings.Repeat("e", 64), InputDigest: "sha256:" + strings.Repeat("f", 64), BootstrapEstimateMS: 100, Shardable: true}
	sample := func(generation uint64, durationMS int64) DurationSample {
		result := testDurationIndexSample(workload, DurationExecutionModeNormal, "small", 2, 4, durationMS)
		result.AcceptedGeneration = generation
		return result
	}
	samples := []DurationSample{sample(3, 1_000)}
	for range 5 {
		samples = append(samples, sample(2, 100))
	}
	index, err := BuildDurationSampleIndex(testPlanningLedger(context, samples), context)
	if err != nil {
		t.Fatal(err)
	}
	got, err := index.EstimateWorkloadDurationMS(workload)
	if err != nil {
		t.Fatal(err)
	}
	if got != 280 {
		t.Fatalf("sparse newest estimate = %d, want 280 after confidence shrink", got)
	}
}

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

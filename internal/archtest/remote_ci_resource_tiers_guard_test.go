package archtest

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestRemoteCIResourceTiersBindPerWorkloadDuration 将 normal 2C/4C/8C 选择锁定到
// 冻结的 per-workload 估时，并保持独立的 4C/8GiB calibration class 分离。
func TestRemoteCIResourceTiersBindPerWorkloadDuration(t *testing.T) {
	root := findRepoRoot(t)
	workloadPlanner := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/workload_resource_planning.go"))
	assertRemoteCIResourceTierPlannerMarkers(t, workloadPlanner)
	if strings.Contains(workloadPlanner, "ClassifyWorkloadResourceDuration") {
		t.Error("resource selector must consume persisted plan identity, not reclassify duration")
	}
}

// assertRemoteCIResourceTierPlannerMarkers 检查持久化资源身份和 tier partitioning。
func assertRemoteCIResourceTierPlannerMarkers(t *testing.T, planner string) {
	t.Helper()
	for _, required := range []string{"distributeTieredLPTWithinTarget", "plannedWorkloadResourceTier", "ResourceCPU", "ResourceMemoryGiB"} {
		if !strings.Contains(planner, required) {
			t.Errorf("workload planner must partition resource tiers before LPT; missing %q", required)
		}
	}
}

// assertRemoteCIResourcePlannerContract 检查 normal 与 calibration resource marker。
func assertRemoteCIResourcePlannerContract(t *testing.T, root string) {
	t.Helper()
	planner := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/workload_resource_planning.go"))
	for _, required := range []string{
		`json:"normal_classes"`, `json:"calibration_resource"`,
		`json:"fast_workload_max_duration_ms"`, `json:"medium_workload_max_duration_ms"`,
		`func validateCalibrationResource(class Class) error`,
		`return cicontract.ValidateCalibrationResources(class.ID, class.VCPU, class.MemoryGiB)`,
		`len(classes) != 3`, `NormalResourceIdentityForTier`, `ValidateNormalResourceIdentity`,
		`FastNormalResourceIdentity`, `bootstrapIdentity.CPU`, `bootstrapIdentity.MemoryGiB`,
		`bootstrap.ID != fastIdentity.ClassID`, `bootstrap.VCPU != fastIdentity.CPU`,
	} {
		if !strings.Contains(planner, required) {
			t.Errorf("resource planner is missing contract marker %q", required)
		}
	}
	if marker := remoteCIResourcePlannerDuplicateCalibrationTuple(planner); marker != "" {
		t.Errorf("resource planner must delegate calibration validation to cicontract owner; duplicate tuple marker %q", marker)
	}
	if strings.Contains(planner, `json:"calibration_class"`) {
		t.Error("resource planner reintroduced calibration as a normal class ID")
	}
	if strings.Contains(planner, "ClassifyWorkloadResourceDuration") {
		t.Error("resource selector must consume persisted plan identity, not reclassify duration")
	}
}

// remoteCIResourcePlannerDuplicateCalibrationTuple identifies retired tuple checks
// that would create a second calibration-resource owner in the planner.
func remoteCIResourcePlannerDuplicateCalibrationTuple(planner string) string {
	for _, marker := range []string{
		`class.VCPU != cicontract.CalibrationResourceCPU`,
		`class.MemoryGiB != cicontract.CalibrationResourceMemoryGiB`,
	} {
		if strings.Contains(planner, marker) {
			return marker
		}
	}
	return ""
}

// TestRemoteCIResourcePlannerGuardRejectsDuplicateCalibrationTuple keeps the
// negative guard fixture independent from the live planner source text.
func TestRemoteCIResourcePlannerGuardRejectsDuplicateCalibrationTuple(t *testing.T) {
	fixture := `func validateCalibrationResource(class Class) error {
	if class.VCPU != cicontract.CalibrationResourceCPU || class.MemoryGiB != cicontract.CalibrationResourceMemoryGiB {
		return nil
	}
	return cicontract.ValidateCalibrationResources(class.ID, class.VCPU, class.MemoryGiB)
}`
	if got := remoteCIResourcePlannerDuplicateCalibrationTuple(fixture); got == "" {
		t.Fatal("duplicate calibration tuple fixture was not rejected")
	}
}

// assertRemoteCIResourceProjectionContract 检查选择使用 per-workload estimate。
func assertRemoteCIResourceProjectionContract(t *testing.T, root string) {
	t.Helper()
	projection := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/workload_resource_planning.go"))
	projection = strings.Join(strings.Fields(projection), " ")
	if !strings.Contains(projection, "EstimatedDurationMS: planned.EstimatedDurationMS") ||
		!strings.Contains(projection, "ResourceCPU: planned.ResourceCPU") ||
		!strings.Contains(projection, "ResourceMemoryGiB: planned.ResourceMemoryGiB") ||
		strings.Contains(projection, "EstimatedDurationMS: shard.EstimatedDurationMS") {
		t.Error("remote resource selection must project persisted per-workload resources, never shard aggregate duration")
	}
}

// assertRemoteCIResourcePassIdentityContract 检查 resource-independent PASS identity marker。
func assertRemoteCIResourcePassIdentityContract(t *testing.T, root string) {
	t.Helper()
	reuse := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/workload_pass_evidence.go"))
	if strings.Contains(reuse, `ResourcePolicyDigest`) || strings.Contains(reuse, `remote-workload-pass-environment/v4`) ||
		strings.Contains(reuse, `WorkerTimeout                string`) || strings.Contains(reuse, `CandidateGateSourceSHA256`) ||
		strings.Contains(reuse, `remoteWorkloadWorkerExecutionDigest`) || strings.Contains(reuse, `WorkerExecutionContractSHA256`) ||
		strings.Contains(reuse, `RunnerIdentityDigest`) {
		t.Error("workload PASS identity must exclude resource, timeout, candidate Gate source, and tree-derived worker fallback identities")
	}
	for _, required := range []string{`cicontract.WorkloadPassEnvironmentSchemaVersion`, `ToolchainDigest`, `RuntimeSeedSHA256`, `PolicyDigest`, `WorkerExecutionProvenance`, `SemanticEnvironment`, `GoFlags`, `go_flags`} {
		if !strings.Contains(reuse, required) {
			t.Errorf("workload PASS identity is missing correctness marker %q", required)
		}
	}
}

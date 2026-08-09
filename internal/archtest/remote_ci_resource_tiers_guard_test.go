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
	assertRemoteCIResourcePlannerContract(t, root)
	assertRemoteCIResourceProjectionContract(t, root)
	assertRemoteCIResourcePassIdentityContract(t, root)
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
	planner := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/shardresource/planner.go"))
	for _, required := range []string{
		`json:"normal_classes"`, `json:"calibration_resource"`,
		`json:"fast_workload_max_duration_ms"`, `json:"medium_workload_max_duration_ms"`,
		`class.VCPU != cicontract.CalibrationResourceCPU || class.MemoryGiB != cicontract.CalibrationResourceMemoryGiB`, `class.VCPU != 2`,
		`len(classes) != 3`, `VCPU: 2, MemoryGiB: 4`, `VCPU: 4, MemoryGiB: 8`, `VCPU: 8, MemoryGiB: 16`,
		`class.MemoryGiB != 4`,
	} {
		if !strings.Contains(planner, required) {
			t.Errorf("resource planner is missing contract marker %q", required)
		}
	}
	if strings.Contains(planner, `json:"calibration_class"`) {
		t.Error("resource planner reintroduced calibration as a normal class ID")
	}
	if strings.Contains(planner, "ClassifyWorkloadResourceDuration") {
		t.Error("resource selector must consume persisted plan identity, not reclassify duration")
	}
}

// assertRemoteCIResourceProjectionContract 检查选择使用 per-workload estimate。
func assertRemoteCIResourceProjectionContract(t *testing.T, root string) {
	t.Helper()
	projection := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/remoteci/coordinator_resources.go"))
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
	reuse := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/remoteci/workload_pass_reuse.go"))
	if strings.Contains(reuse, `ResourcePolicyDigest`) || strings.Contains(reuse, `remote-workload-pass-environment/v4`) ||
		strings.Contains(reuse, `WorkerTimeout                string`) || strings.Contains(reuse, `CandidateGateSourceSHA256`) ||
		strings.Contains(reuse, `remoteWorkloadWorkerExecutionDigest`) || strings.Contains(reuse, `WorkerExecutionContractSHA256`) ||
		strings.Contains(reuse, `RunnerIdentityDigest`) {
		t.Error("workload PASS identity must exclude resource, timeout, candidate Gate source, and tree-derived worker fallback identities")
	}
	for _, required := range []string{`remote-workload-pass-environment/v9`, `ToolchainDigest`, `RuntimeSeedSHA256`, `PolicyDigest`, `WorkerExecutionProvenance`, `SemanticEnvironment`, `GoFlags`, `go_flags`} {
		if !strings.Contains(reuse, required) {
			t.Errorf("workload PASS identity is missing correctness marker %q", required)
		}
	}
}

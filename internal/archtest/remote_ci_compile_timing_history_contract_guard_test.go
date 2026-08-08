package archtest

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestRemoteCICompileTimingHistoryContract 锁定 compile history 的完整身份、规划前 fixed-point 和共享成本边界。
func TestRemoteCICompileTimingHistoryContract(t *testing.T) {
	root := findRepoRoot(t)
	owner := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/workload_compile_owner_planning.go"))
	planner := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/workload_compile_planning.go"))
	index := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/workload_duration_index.go"))
	history := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/compile_timing_history.go"))
	contract := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/cicontract/contract.go"))
	document := readRemoteCIContractGuardFile(t, filepath.Join(root, "docs/契约/remote-ci-eci-imagecache-contract.md"))

	assertCompileTimingIdentityMarkers(t, history)
	assertCompileOwnerPlanningMarkers(t, owner, planner, index)
	assertCompileTimingContractMarkers(t, contract, document)
}

func assertCompileTimingIdentityMarkers(t *testing.T, source string) {
	t.Helper()
	for _, marker := range []string{
		"type CompileTimingIdentity struct",
		"PackageTarget",
		"SemanticKey",
		"Platform",
		"RunnerIdentityDigest",
		"ToolchainDigest",
		"ExecutionMode",
		"ResourceClassID",
		"ResourceCPU",
		"ResourceMemoryGiB",
		"BuildCompileTimingIndex",
		"func (index CompileTimingIndex) EstimateMS",
		"accepted generation",
		"CompileTimingObservation",
	} {
		if !strings.Contains(source, marker) {
			t.Errorf("compile timing history is missing marker %q", marker)
		}
	}
}

func assertCompileOwnerPlanningMarkers(t *testing.T, owner, planner, index string) {
	t.Helper()
	for _, marker := range []string{
		"type CompileOwnerHint struct",
		"BuildCompileOwnerHints",
		"resolveCompileOwnerHint",
		"compileParentBootstrapEstimateMS",
		"WorkloadResourceTierFast",
		"GoTestDurationMSAtResource",
		"ResourceCPU",
		"ResourceMemoryGiB",
		"plannedWorkloadsFromEstimates",
	} {
		if !strings.Contains(owner+planner+index, marker) {
			t.Errorf("compile owner planner is missing marker %q", marker)
		}
	}
	if strings.Contains(owner+planner, "240_053") || strings.Contains(owner+planner, "240053") {
		t.Fatal("compile owner planner hardcodes a measured duration sample")
	}
	assertCompilePlannerOrder(t, planner)
}

func assertCompilePlannerOrder(t *testing.T, planner string) {
	t.Helper()
	ordered := []string{
		"base, err := estimateShardableWorkloads(catalog, index)",
		"ownerHints, err := BuildCompileOwnerHints(base, compileInputs, index.CompileTimingIndex, index.context)",
		"planned, err := plannedWorkloadsFromEstimates(base, ownerHints)",
		"units, groups, err := buildCompileUnits(planned, index, compileInputs, order, ownerHints)",
	}
	previous := -1
	for _, marker := range ordered {
		position := strings.Index(planner, marker)
		if position < 0 {
			t.Fatalf("compile planner is missing fixed-point ordering marker %q", marker)
		}
		if position <= previous {
			t.Fatalf("compile planner creates PlannedWorkload or compile units before fixed-point marker %q", marker)
		}
		previous = position
	}
}

func assertCompileTimingContractMarkers(t *testing.T, contract, document string) {
	t.Helper()
	combined := contract + "\n" + document
	for _, marker := range []string{
		"PackageTarget、SemanticKey、Platform、RunnerIdentityDigest、ToolchainDigest、ExecutionMode",
		"ResourceClassID/CPU/Memory",
		"最近三个 accepted generation",
		"authoritative、passed、cleanup-complete、measured/raw",
		"source tree、shared input 与 artifact digest",
		"PlannedWorkload",
		"shared compile cost 每组只计一次",
		"normal 无历史固定 2C/4GiB",
		"calibration 固定 4C/8GiB",
	} {
		if !strings.Contains(combined, marker) {
			t.Errorf("compile timing contract is missing marker %q", marker)
		}
	}
}

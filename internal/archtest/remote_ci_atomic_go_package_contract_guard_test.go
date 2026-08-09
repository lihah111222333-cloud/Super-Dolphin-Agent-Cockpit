package archtest

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestRemoteCIAtomicGoPackageContract 锁定 gate/remoteci 原子清单、统一 inventory 来源与 manifest 批次闭包。
func TestRemoteCIAtomicGoPackageContract(t *testing.T) {
	root := findRepoRoot(t)
	catalog := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/workload_catalog.go"))
	inventory := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/remoteci/inventory.go"))
	planner := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/workload_compile_planning.go"))
	manifest := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/compile_group.go"))

	for _, marker := range []string{
		"AtomicGatePackageTarget",
		"AtomicRemoteCIPackageTarget",
		"AtomicMcpLSPPackageTarget",
		"AtomicSuperDolphinGatePackageTarget",
		`"./internal/devtools/gate"`,
		`"./internal/devtools/remoteci"`,
		"func AtomicGoPackageTargets() []string",
		"return slices.Clone(atomicGoPackageTargets)",
	} {
		if !strings.Contains(catalog, marker) {
			t.Errorf("atomic Go catalog is missing marker %q", marker)
		}
	}
	for _, marker := range []string{
		"for _, packageTarget := range gate.AtomicGoPackageTargets()",
		"inventoryAtomicGoTests",
	} {
		if !strings.Contains(inventory, marker) {
			t.Errorf("remote Go inventory is missing marker %q", marker)
		}
	}
	for _, marker := range []string{
		"compileGroupBatchCapacity",
		"compileGroupBatchPlan",
		"resourceTier",
	} {
		if !strings.Contains(planner, marker) {
			t.Errorf("compile planner is missing marker %q", marker)
		}
	}
	for _, marker := range []string{
		"CompileGroupBatchPlanDigest",
		"validateCompileGroupBatchCoverage",
		"BatchPlanDigest",
		"WorkloadIDs",
	} {
		if !strings.Contains(manifest, marker) {
			t.Errorf("compile-group manifest guard is missing marker %q", marker)
		}
	}
}

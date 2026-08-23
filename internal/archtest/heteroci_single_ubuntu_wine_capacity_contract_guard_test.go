package archtest

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHeterociSingleUbuntuWineCapacityContract freezes one physical Ubuntu
// host with ubuntu_container and ubuntu_wine_container as logical capabilities.
func TestHeterociSingleUbuntuWineCapacityContract(t *testing.T) {
	root := findRepoRoot(t)
	manifestPath := filepath.Join(root, "internal", "devtools", "heteroci", "manifest.go")
	manifest := readRemoteCIContractGuardFile(t, manifestPath)
	for _, marker := range []string{
		"NodeConfig", "NodeID", "PlatformLinuxAMD64", "Capabilities",
		"RuntimeUbuntuContainer", "RuntimeUbuntuWineContainer", "ValidateSingleUbuntuWineExecutionPool",
	} {
		if !strings.Contains(manifest, marker) {
			t.Errorf("manifest is missing single Ubuntu/Wine marker %q", marker)
		}
	}
	for _, retired := range []string{
		"ValidateFourNodeExecutionPool", "expectedFourNodePlatform", "RuntimeWindowsNative",
		"RuntimeDarlingCompatRuntime", "RuntimeDarwinNative", "RuntimeDockerOSXExperimental",
		"Ubuntu-01", "Ubuntu-02", "Windows-01", "Windows-02",
	} {
		if strings.Contains(manifest, retired) {
			t.Errorf("manifest retains retired topology/runtime marker %q", retired)
		}
	}
}

// TestHeterociSingleUbuntuWineSharedCapacityBoundary requires one physical
// snapshot/reservation budget while carrying the logical runtime identity.
func TestHeterociSingleUbuntuWineSharedCapacityBoundary(t *testing.T) {
	root := findRepoRoot(t)
	paths := map[string][]string{
		"internal/devtools/heteroci/execution_unit.go":                         {"TargetPlatform", "RequiredRuntime", "executionUnitRuntimeSupportsPlatform"},
		"internal/devtools/heterocinode/batch.go":                              {"TargetPlatform", "RuntimeCapability", "CommandDigest", "ExecutionDigest", "validateBatchTargetRuntime"},
		"internal/devtools/heterociworkflow/types.go":                          {"MaterialDigest", "CommandDigest", "UnitSetDigest"},
		"internal/devtools/heterociworkflow/executor_binding.go":               {"NodeIDDigest", "ShardDigest", "BatchSetDigest", "UnitSetDigest", "NodeFactsDigest"},
		"internal/devtools/heterociexec/postgresql_receipt_schema.go":          {"material_digest", "command_digest"},
		"internal/devtools/heterociexec/postgresql_terminal_receipt_schema.go": {"node_id", "node_id_digest", "shard_digest", "batch_set_digest", "unit_set_digest"},
		"internal/devtools/heterociexec/postgresql_terminal_receipt_store.go":  {"INSERT", "SELECT", "batch_set_digest", "unit_set_digest"},
	}
	for relative, markers := range paths {
		source := readRemoteCIContractGuardFile(t, filepath.Join(root, filepath.FromSlash(relative)))
		for _, marker := range markers {
			if !strings.Contains(source, marker) {
				t.Errorf("%s is missing shared-capacity marker %q", relative, marker)
			}
		}
	}
}

// TestHeterociSingleUbuntuWineRejectsPhysicalWindowsAndForeignRuntimes keeps
// the old physical Windows/Darwin/ECI paths out of the remote node surface.
func TestHeterociSingleUbuntuWineRejectsPhysicalWindowsAndForeignRuntimes(t *testing.T) {
	root := findRepoRoot(t)
	for _, relative := range []string{
		"internal/devtools/heterocinode/windows_facts.go", "internal/devtools/heterocinode/windows_facts_test.go",
		"scripts/deploy/windows", "scripts/deploy/mac", "Dockerfile.ci-runner-Darwin-compat", "scripts/darwin_qualification",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative))); err == nil {
			t.Errorf("physical/foreign runtime artifact is still present: %s", relative)
		}
	}
	for _, relative := range []string{"internal/devtools/heterocinode", "cmd/super-dolphin-heteroci-node", "internal/devtools/remoteruntime"} {
		assertRemoteCIProductionMarkersAbsent(t, filepath.Join(root, filepath.FromSlash(relative)), []string{
			"WindowsFactsProvider", "PowerShell", "powershell", "RuntimeWindowsNative", "WindowsPlatform", "PlatformDarwin",
			"darling_compat_runtime", "docker_osx_experimental", "aliyun-eci", "ImageCache",
		})
	}
}

// TestHeterociSingleUbuntuWinePostgreSQLFieldChain requires the target/runtime
// digest chain from ExecutionUnit through BatchWorkload/material/unit/batch to
// the PostgreSQL receipt, while NodeID remains the only physical-host key.
func TestHeterociSingleUbuntuWinePostgreSQLFieldChain(t *testing.T) {
	root := findRepoRoot(t)
	paths := map[string][]string{
		"internal/devtools/heteroci/execution_unit.go":                         {"TargetPlatform", "RequiredRuntime", "executionUnitRuntimeSupportsPlatform"},
		"internal/devtools/heterocinode/batch.go":                              {"TargetPlatform", "RuntimeCapability", "CommandDigest", "ExecutionDigest", "validateBatchTargetRuntime"},
		"internal/devtools/heterociworkflow/types.go":                          {"MaterialDigest", "CommandDigest", "UnitSetDigest"},
		"internal/devtools/heterociworkflow/executor_binding.go":               {"NodeIDDigest", "ShardDigest", "BatchSetDigest", "UnitSetDigest", "NodeFactsDigest"},
		"internal/devtools/heterociexec/postgresql_receipt_schema.go":          {"material_digest", "command_digest"},
		"internal/devtools/heterociexec/postgresql_terminal_receipt_schema.go": {"node_id", "node_id_digest", "shard_digest", "batch_set_digest", "unit_set_digest"},
		"internal/devtools/heterociexec/postgresql_terminal_receipt_store.go":  {"INSERT", "SELECT", "batch_set_digest", "unit_set_digest"},
	}
	for relative, markers := range paths {
		source := readRemoteCIContractGuardFile(t, filepath.Join(root, filepath.FromSlash(relative)))
		for _, marker := range markers {
			if !strings.Contains(source, marker) {
				t.Errorf("%s is missing runtime/target digest marker %q", relative, marker)
			}
		}
	}
}

func assertRemoteCIProductionMarkersAbsent(t *testing.T, root string, markers []string) {
	t.Helper()
	info, err := os.Stat(root)
	if err != nil {
		t.Fatalf("stat remote CI source root %s: %v", root, err)
	}
	if !info.IsDir() {
		t.Fatalf("remote CI source root is not a directory: %s", root)
	}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		contents := readRemoteCIContractGuardFile(t, path)
		for _, marker := range markers {
			if strings.Contains(contents, marker) {
				t.Errorf("%s retains forbidden remote CI marker %q", filepath.ToSlash(path), marker)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan remote CI source root %s: %v", root, err)
	}
}

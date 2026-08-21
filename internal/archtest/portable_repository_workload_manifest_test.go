package archtest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// TestPortableRepositoryWorkloadManifest 绑定 V3 仓库自有的 exact Go test manifest。
func TestPortableRepositoryWorkloadManifest(t *testing.T) {
	root := findRepoRoot(t)
	path := filepath.Join(root, ".super-dolphin", "remote-ci-workloads.json")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("portable workload manifest is not a regular file: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := remoteci.DecodeRepositoryWorkloadManifest(raw)
	if err != nil {
		t.Fatalf("decode portable workload manifest: %v", err)
	}
	assertPortableRepositoryManifest(t, manifest)
}

// assertPortableRepositoryManifest 校验 V3 仓库自有 manifest 的稳定 workload 绑定。
func assertPortableRepositoryManifest(t *testing.T, manifest remoteci.RepositoryWorkloadManifest) {
	t.Helper()
	expected := map[string]struct {
		packagePath string
		name        string
		platform    remoteci.RepositoryWorkloadPlatform
		runtime     remoteci.RepositoryWorkloadRuntime
	}{
		"go-test/internal-devtools-gate/TestPlanGateFailureSummaryIsBoundedAndDoesNotEchoRawError": {
			packagePath: "./internal/devtools/gate",
			name:        "TestPlanGateFailureSummaryIsBoundedAndDoesNotEchoRawError",
			platform:    remoteci.RepositoryWorkloadPlatformLinuxAMD64,
			runtime:     remoteci.RepositoryWorkloadRuntimeUbuntuContainer,
		},
		"go-test/internal-devtools-heteroci/TestProbeSchemaVersionsAreFrozen": {
			packagePath: "./internal/devtools/heteroci",
			name:        "TestProbeSchemaVersionsAreFrozen",
			platform:    remoteci.RepositoryWorkloadPlatformWindowsAMD64,
			runtime:     remoteci.RepositoryWorkloadRuntimeWindowsNative,
		},
		"go-test/windows/internal-devtools-heterocinode/TestWindowsFactsProviderCollectsCIMCapacity": {
			packagePath: "./internal/devtools/heterocinode",
			name:        "TestWindowsFactsProviderCollectsCIMCapacity",
			platform:    remoteci.RepositoryWorkloadPlatformWindowsAMD64,
			runtime:     remoteci.RepositoryWorkloadRuntimeWindowsNative,
		},
	}
	if manifest.RepositoryID != "super-agent-v3" || len(manifest.Workloads) != len(expected) {
		t.Fatalf("portable workload manifest identity = %#v", manifest)
	}
	for _, workload := range manifest.Workloads {
		want, ok := expected[workload.ID]
		if !ok || workload.ModuleRoot != "." || workload.Package != want.packagePath || workload.Name != want.name ||
			workload.Platform != want.platform || workload.Runtime != want.runtime {
			t.Fatalf("portable workload binding = %#v", workload)
		}
	}
}

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
	if manifest.RepositoryID != "super-agent-v3" || len(manifest.Workloads) != 1 {
		t.Fatalf("portable workload manifest identity = %#v", manifest)
	}
	workload := manifest.Workloads[0]
	if workload.ModuleRoot != "." || workload.Package != "./internal/devtools/gate" ||
		workload.Name != "TestPlanGateFailureSummaryIsBoundedAndDoesNotEchoRawError" {
		t.Fatalf("portable workload binding = %#v", workload)
	}
}

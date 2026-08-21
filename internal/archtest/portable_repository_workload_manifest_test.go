package archtest

import (
	"os"
	"path/filepath"
	"slices"
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

type portableWorkloadExpectation struct {
	target   string
	platform remoteci.RepositoryWorkloadPlatform
	runtime  remoteci.RepositoryWorkloadRuntime
}

// assertPortableRepositoryManifest 校验 V3 仓库自有 manifest 的稳定 workload 绑定。
func assertPortableRepositoryManifest(t *testing.T, manifest remoteci.RepositoryWorkloadManifest) {
	t.Helper()
	if manifest.RepositoryID != "super-agent-v3" || len(manifest.Workloads) != 3 || len(manifest.Profiles) != 1 {
		t.Fatalf("portable workload manifest identity = %#v", manifest)
	}
	workloadIDs := make([]string, 0, len(manifest.Workloads))
	for _, workload := range manifest.Workloads {
		workloadIDs = append(workloadIDs, workload.ID)
	}
	profile := manifest.Profiles[0]
	if profile.Profile != "local-fast" || !slices.Equal(profile.RequiredWorkloadIDs, workloadIDs) {
		t.Fatalf("portable workload profile = %#v, workload IDs = %#v", profile, workloadIDs)
	}
	expected := map[string]portableWorkloadExpectation{
		"go-test/internal-devtools-gate/TestPlanGateFailureSummaryIsBoundedAndDoesNotEchoRawError": {
			target:   "./internal/devtools/gate#TestPlanGateFailureSummaryIsBoundedAndDoesNotEchoRawError",
			platform: remoteci.RepositoryWorkloadPlatformLinuxAMD64,
			runtime:  remoteci.RepositoryWorkloadRuntimeUbuntuContainer,
		},
		"go-test/internal-devtools-heteroci/TestProbeSchemaVersionsAreFrozen": {
			target:   "./internal/devtools/heteroci#TestProbeSchemaVersionsAreFrozen",
			platform: remoteci.RepositoryWorkloadPlatformWindowsAMD64,
			runtime:  remoteci.RepositoryWorkloadRuntimeWindowsNative,
		},
		"go-test/windows/internal-devtools-heterocinode/TestWindowsFactsProviderCollectsCIMCapacity": {
			target:   "./internal/devtools/heterocinode#TestWindowsFactsProviderCollectsCIMCapacity",
			platform: remoteci.RepositoryWorkloadPlatformWindowsAMD64,
			runtime:  remoteci.RepositoryWorkloadRuntimeWindowsNative,
		},
	}
	for _, workload := range manifest.Workloads {
		assertPortableWorkloadBinding(t, workload, expected)
	}
}

func assertPortableWorkloadBinding(t *testing.T, workload remoteci.RepositoryWorkload, expected map[string]portableWorkloadExpectation) {
	t.Helper()
	want, ok := expected[workload.ID]
	if !ok {
		t.Fatalf("portable workload ID is not registered: %#v", workload)
	}
	if want.target != workload.Package+"#"+workload.Name {
		t.Fatalf("portable workload target drifted: %#v", workload)
	}
	if want.platform != workload.Platform || want.runtime != workload.Runtime {
		t.Fatalf("portable workload platform/runtime drifted: %#v", workload)
	}
	if workload.CommandDigest == "" {
		t.Fatalf("portable workload command digest is empty: %#v", workload)
	}
}

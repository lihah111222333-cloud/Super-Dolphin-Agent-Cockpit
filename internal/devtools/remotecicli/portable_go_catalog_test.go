package remotecicli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func TestExpandDetectedRepositoryGoCatalogExternalRepository(t *testing.T) {
	root := os.Getenv("REMOTE_CI_TEST_REPOSITORY")
	if root == "" {
		t.Skip("REMOTE_CI_TEST_REPOSITORY is not set")
	}
	detected, err := DetectRepository(context.Background(), root)
	if err != nil {
		t.Fatalf("DetectRepository() error = %v", err)
	}
	expanded, err := expandDetectedRepositoryGoCatalog(context.Background(), detected)
	if err != nil {
		t.Fatalf("expandDetectedRepositoryGoCatalog() error = %v", err)
	}
	encoded, err := expanded.Manifest.CanonicalEncode()
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) >= remoteci.RepositoryWorkloadManifestMaxBytes {
		t.Fatalf("expanded manifest size = %d", len(encoded))
	}
	if expectedRoot := os.Getenv("REMOTE_CI_EXPECT_MODULE_ROOT_PACKAGE"); expectedRoot != "" && !slices.ContainsFunc(expanded.Manifest.Workloads, func(workload remoteci.RepositoryWorkload) bool {
		return workload.ModuleRoot == expectedRoot && workload.Package == "."
	}) {
		t.Fatalf("module root package %q was not projected", expectedRoot)
	}
	t.Logf("repository=%s tree=%s workloads=%d manifest_bytes=%d headroom=%d digest=%s", expanded.RepositoryID, expanded.TreeSHA, len(expanded.Manifest.Workloads), len(encoded), remoteci.RepositoryWorkloadManifestMaxBytes-len(encoded), expanded.ManifestSHA256)
}

func TestExpandDetectedRepositoryGoCatalogDerivesBothPlatformsFromExactTree(t *testing.T) {
	root := newDetectedRepositoryFixture(t, "portable-catalog")
	detected, err := DetectRepository(context.Background(), root)
	if err != nil {
		t.Fatalf("DetectRepository() error = %v", err)
	}
	originalDigest := detected.ManifestSHA256
	expanded, err := expandDetectedRepositoryGoCatalog(context.Background(), detected)
	if err != nil {
		t.Fatalf("expandDetectedRepositoryGoCatalog() error = %v", err)
	}
	if expanded.ManifestSHA256 == originalDigest || len(expanded.Manifest.Workloads) != 3 {
		t.Fatalf("expanded catalog identity/workloads = %q/%#v", expanded.ManifestSHA256, expanded.Manifest.Workloads)
	}
	if !slices.IsSortedFunc(expanded.Manifest.Workloads, func(left, right remoteci.RepositoryWorkload) int { return strings.Compare(left.ID, right.ID) }) {
		t.Fatal("expanded workloads are not canonically sorted")
	}
	assertPortablePackageWorkload(t, expanded.Manifest.Workloads[0], remoteci.RepositoryWorkloadPlatformLinuxAMD64)
	assertPortablePackageWorkload(t, expanded.Manifest.Workloads[1], remoteci.RepositoryWorkloadPlatformWindowsAMD64)
	if expanded.Manifest.Workloads[2].Kind != remoteci.RepositoryWorkloadKindGoTest || expanded.Manifest.Workloads[2].ID != "go-test/example/TestFixture" {
		t.Fatalf("explicit workload was not preserved: %#v", expanded.Manifest.Workloads[2])
	}
}

func TestExpandDetectedRepositoryGoCatalogSupportsRootlessDeclaredModules(t *testing.T) {
	root := newPortableMultiModuleFixture(t)
	detected, err := DetectRepository(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	expanded, err := expandDetectedRepositoryGoCatalog(context.Background(), detected)
	if err != nil {
		t.Fatal(err)
	}
	assertPortablePackageCount(t, expanded.Manifest.Workloads, ".", "./example", 2)
	assertPortablePackageCount(t, expanded.Manifest.Workloads, "nested", ".", 2)
	assertPortablePackageCount(t, expanded.Manifest.Workloads, "nested", "./sub", 1)
	assertPortablePackageCount(t, expanded.Manifest.Workloads, "hidden", ".", 0)
}

func newPortableMultiModuleFixture(t *testing.T) string {
	t.Helper()
	root := newDetectedRepositoryFixture(t, "portable-multi-module")
	manifestPath := filepath.Join(root, filepath.FromSlash(portableWorkloadManifestRelativePath))
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := remoteci.DecodeRepositoryWorkloadManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Modules = append(manifest.Modules, remoteci.RepositoryWorkloadModule{Root: "nested", ModulePath: "example.invalid/portable-multi-module/nested"})
	encoded, err := manifest.CanonicalEncode()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	writePortableFixtureFile(t, root, "nested/go.mod", "module example.invalid/portable-multi-module/nested\n\ngo 1.24.0\n")
	writePortableFixtureFile(t, root, "nested/root.go", "package nested\n")
	writePortableFixtureFile(t, root, "nested/root_windows_test.go", "package nested\n")
	writePortableFixtureFile(t, root, "nested/sub/sub.go", "package sub\n")
	writePortableFixtureFile(t, root, "hidden/go.mod", "module example.invalid/hidden\n\ngo 1.24.0\n")
	writePortableFixtureFile(t, root, "hidden/root.go", "package hidden\n")
	commitDetectedFixture(t, root, "add declared and hidden modules")
	return root
}

func writePortableFixtureFile(t *testing.T, root, relative, contents string) {
	t.Helper()
	filePath := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filePath, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertPortablePackageCount(t *testing.T, workloads []remoteci.RepositoryWorkload, moduleRoot, packageTarget string, want int) {
	t.Helper()
	got := 0
	for _, workload := range workloads {
		if workload.Kind == remoteci.RepositoryWorkloadKindGoPackage && workload.ModuleRoot == moduleRoot && workload.Package == packageTarget {
			got++
		}
	}
	if got != want {
		t.Fatalf("package %s:%s count = %d, want %d", moduleRoot, packageTarget, got, want)
	}
}

// assertPortablePackageWorkload 校验动态 package workload 的平台与 normal 策略。
func assertPortablePackageWorkload(t *testing.T, workload remoteci.RepositoryWorkload, platform remoteci.RepositoryWorkloadPlatform) {
	t.Helper()
	if workload.Kind != remoteci.RepositoryWorkloadKindGoPackage || workload.Name != "" || workload.Package != "./example" || workload.ModuleRoot != "." || workload.Platform != platform || workload.ExecutionPolicy != remoteci.RepositoryWorkloadExecutionPolicyNormal {
		t.Fatalf("expanded workload = %#v", workload)
	}
}

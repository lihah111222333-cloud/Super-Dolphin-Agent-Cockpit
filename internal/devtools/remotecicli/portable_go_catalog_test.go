package remotecicli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteportable"
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
	encoded, err := remoteportable.PortableManifestCanonicalEncode(expanded.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) >= 1<<20 {
		t.Fatalf("expanded manifest size = %d", len(encoded))
	}
	if expectedRoot := os.Getenv("REMOTE_CI_EXPECT_MODULE_ROOT_PACKAGE"); expectedRoot != "" && !slices.ContainsFunc(expanded.Workloads, func(workload remoteportable.PortableManifestWorkload) bool {
		return workload.ModuleRoot == expectedRoot && workload.Package == "."
	}) {
		t.Fatalf("module root package %q was not projected", expectedRoot)
	}
	t.Logf("repository=%s tree=%s workloads=%d manifest_bytes=%d headroom=%d blob_digest=%s", expanded.RepositoryID, expanded.TreeSHA, len(expanded.Workloads), len(encoded), (1<<20)-len(encoded), expanded.ManifestBlobSHA256)
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
	assertPortablePackageCount(t, expanded.Workloads, ".", "./example", 1)
	assertPortablePackageCount(t, expanded.Workloads, "nested", ".", 1)
	assertPortablePackageCount(t, expanded.Workloads, "nested", "./sub", 1)
	assertPortablePackageCount(t, expanded.Workloads, "hidden", ".", 0)
}

func newPortableMultiModuleFixture(t *testing.T) string {
	t.Helper()
	root := newDetectedRepositoryFixture(t, "portable-multi-module")
	manifestPath := filepath.Join(root, filepath.FromSlash(portableWorkloadManifestRelativePath))
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := remoteportable.DecodePortableManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Modules = append(manifest.Modules, remoteportable.RepositoryWorkloadModule{Root: "nested", ModulePath: "example.invalid/portable-multi-module/nested"})
	encoded, err := remoteportable.PortableManifestCanonicalEncode(manifest)
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

func assertPortablePackageCount(t *testing.T, workloads []remoteportable.PortableManifestWorkload, moduleRoot, packageTarget string, want int) {
	t.Helper()
	got := 0
	for _, workload := range workloads {
		if workload.Kind == remoteportable.RepositoryWorkloadKindGoPackage && workload.ModuleRoot == moduleRoot && workload.Package == packageTarget {
			got++
		}
	}
	if got != want {
		t.Fatalf("package %s:%s count = %d, want %d", moduleRoot, packageTarget, got, want)
	}
}

package remotecicli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	assertPortablePackageCount(t, expanded.Workloads, ".", "./example", 2)
	assertPortablePackageCount(t, expanded.Workloads, "nested", ".", 2)
	assertPortablePackageCount(t, expanded.Workloads, "nested", "./sub", 2)
	assertPortablePackageCount(t, expanded.Workloads, "hidden", ".", 0)
}

func TestExpandDetectedRepositoryGoCatalogBindsDualPlatformPoliciesAndStableDigests(t *testing.T) {
	root := newDetectedRepositoryFixture(t, "portable-dual-platform")
	detected, err := DetectRepository(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	expanded, err := expandDetectedRepositoryGoCatalog(context.Background(), detected)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.IsSortedFunc(expanded.Workloads, func(left, right remoteportable.PortableManifestWorkload) int {
		return strings.Compare(left.UnitID, right.UnitID)
	}) {
		t.Fatal("expanded workloads are not sorted by UnitID")
	}
	platforms := map[string]struct{}{}
	for _, workload := range expanded.Workloads {
		if workload.Kind != remoteportable.RepositoryWorkloadKindGoPackage {
			continue
		}
		platforms[workload.TargetPlatform] = struct{}{}
		assertGeneratedPortableGoPackage(t, workload)
	}
	if len(platforms) != 2 {
		t.Fatalf("generated platform set = %#v, want Linux and Windows", platforms)
	}
}

func TestExpandDetectedRepositoryGoCatalogSupportsExplicitWorkloadOnlyManifest(t *testing.T) {
	root := newDetectedRepositoryFixture(t, "portable-explicit-only")
	detected, err := DetectRepository(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	detected.Manifest.CatalogPolicies = nil
	detected.Manifest.CapacityPolicyDigest, err = remoteportable.DerivePortableCapacityPolicyDigest(detected.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]remoteportable.PortableManifestWorkload(nil), detected.Workloads...)
	expanded, err := expandDetectedRepositoryGoCatalog(context.Background(), detected)
	if err != nil {
		t.Fatalf("expand explicit-only manifest: %v", err)
	}
	if !slices.Equal(expanded.Workloads, want) {
		t.Fatalf("explicit-only workloads changed: got=%#v want=%#v", expanded.Workloads, want)
	}
}

func assertGeneratedPortableGoPackage(t *testing.T, workload remoteportable.PortableManifestWorkload) {
	t.Helper()
	switch workload.TargetPlatform {
	case string(remoteportable.RepositoryWorkloadPlatformLinuxAMD64):
		assertGeneratedPortableGoPolicy(t, workload, string(remoteportable.RepositoryWorkloadRuntimeUbuntuContainer), 1_000, 1<<30, 1_000)
	case string(remoteportable.RepositoryWorkloadPlatformWindowsAMD64):
		assertGeneratedPortableGoPolicy(t, workload, string(remoteportable.RepositoryWorkloadRuntimeWindowsNative), 2_000, 2<<30, 2_000)
	default:
		t.Fatalf("unexpected generated platform: %#v", workload)
	}
	if !strings.Contains(workload.UnitID, strings.TrimSuffix(workload.TargetPlatform, "/amd64")) || workload.CommandDigest == workload.ExecutionDigest {
		t.Fatalf("generated workload identity/digests are not platform-bound: %#v", workload)
	}
	wantExecutionDigest, err := remoteportable.DerivePortableWorkloadExecutionDigest(workload)
	if err != nil || wantExecutionDigest != workload.ExecutionDigest {
		t.Fatalf("generated execution digest = %q, want %q, err = %v", workload.ExecutionDigest, wantExecutionDigest, err)
	}
}

func assertGeneratedPortableGoPolicy(t *testing.T, workload remoteportable.PortableManifestWorkload, runtime string, cpu, memory, estimate int64) {
	t.Helper()
	if workload.RuntimeCapability != runtime || workload.CPUMilli != cpu || workload.MemoryBytes != memory || workload.HistoricalEstimateMS != estimate {
		t.Fatalf("catalog policy was not bound: %#v", workload)
	}
}

func TestExpandDetectedRepositoryGoCatalogRejectsMissingPolicyAndDuplicateManualUnit(t *testing.T) {
	root := newDetectedRepositoryFixture(t, "portable-policy-fail-fast")
	detected, err := DetectRepository(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	detected.Manifest.CatalogPolicies = detected.Manifest.CatalogPolicies[:1]
	if _, err := expandDetectedRepositoryGoCatalog(context.Background(), detected); err == nil || !strings.Contains(err.Error(), "catalog_policies") {
		t.Fatalf("missing catalog policy error = %v", err)
	}
	detected, err = DetectRepository(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	detected.Workloads = append(detected.Workloads, detected.Workloads[0])
	if _, err := expandDetectedRepositoryGoCatalog(context.Background(), detected); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("duplicate manual unit error = %v", err)
	}
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
	manifest.ExcludedNestedModules = []string{"hidden"}
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

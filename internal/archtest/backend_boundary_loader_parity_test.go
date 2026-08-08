package archtest_test

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest/ssaload"
	"golang.org/x/tools/go/packages"
)

const archtestImportPath = "github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest"

func stablePathDigest(paths []string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(paths, "\n"))))
}

// TestBackendBoundaryLoaderSelectsUniqueExternalTestVariant 固定真实 external-test variant 选择。
func TestBackendBoundaryLoaderSelectsUniqueExternalTestVariant(t *testing.T) {
	root := repoRoot(t)
	pkgs, err := ssaload.Load(ssaload.Options{
		RepoRoot: root,
		Patterns: []string{"./internal/archtest"},
		Tests:    true,
		// 该 parity 只消费包名、ForTest、文件名与语法树；避免为唯一 variant 选择加载 imports/types。
		LoadMode: packages.LoadFiles | packages.NeedSyntax,
		Include:  includeBackendBoundaryArchtestVariant,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("external variants=%d, want 1", len(pkgs))
	}
	pkg := pkgs[0]
	if strings.HasSuffix(pkg.ID, ".test") {
		t.Fatalf("synthetic main selected: %s", pkg.ID)
	}
	found := false
	for _, file := range pkg.Syntax {
		if filepath.Base(pkg.Fset.Position(file.Pos()).Filename) == "backend_boundary_single_source_test.go" {
			found = true
		}
	}
	if !found {
		t.Fatal("consumer _test.go absent from Syntax")
	}
	assertBackendBoundaryExternalVariantSyntaxDigest(t, root, pkg)
}

// TestBackendBoundaryLoaderFileQueryPreservesCandidates 固定 SSA 使用的 file query 与目录候选集相同。
func TestBackendBoundaryLoaderFileQueryPreservesCandidates(t *testing.T) {
	root := repoRoot(t)
	pkgs, err := ssaload.Load(ssaload.Options{
		RepoRoot: root,
		Patterns: []string{"file=" + filepath.Join(root, filepath.FromSlash(backendBoundarySSAEntryFile))},
		Tests:    true,
		LoadMode: packages.LoadFiles | packages.NeedSyntax,
		Include:  includeBackendBoundaryArchtestVariant,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("file-query external variants=%d, want 1", len(pkgs))
	}
	assertBackendBoundaryExternalVariantSyntaxDigest(t, root, pkgs[0])
}

func assertBackendBoundaryExternalVariantSyntaxDigest(t *testing.T, root string, pkg *packages.Package) {
	t.Helper()
	paths := make([]string, 0, len(pkg.Syntax))
	for _, file := range pkg.Syntax {
		rel, err := filepath.Rel(root, pkg.Fset.Position(file.Pos()).Filename)
		if err != nil || strings.HasPrefix(rel, "..") {
			t.Fatalf("external variant syntax path escaped repository root: %q", pkg.Fset.Position(file.Pos()).Filename)
		}
		paths = append(paths, filepath.ToSlash(rel))
	}
	sort.Strings(paths)
	const wantSyntaxCount = 64
	const wantSyntaxDigest = "d00a869b869748f366559806929fcbae9b5e5ef5a922dc72fea7369a3f8bb411"
	if len(paths) != wantSyntaxCount || stablePathDigest(paths) != wantSyntaxDigest {
		t.Fatalf("external variant syntax candidate set changed: count=%d digest=%s", len(paths), stablePathDigest(paths))
	}
}

// TestWideOrchestrationLoaderExtractionPreservesCandidates 固定 seam 的候选集，并避免在 parity 检查中重复全仓加载。
func TestWideOrchestrationLoaderExtractionPreservesCandidates(t *testing.T) {
	root := repoRoot(t)
	loads := 0
	assertWideOrchestrationLoaderCandidates(t, root, func(t *testing.T, path string) []string {
		loads++
		return loadWideOrchestrationTypeGuardPackagePaths(t, path)
	})
	if loads != 1 {
		t.Fatalf("wide orchestration loader calls=%d, want 1", loads)
	}
}

// loadWideOrchestrationTypeGuardPackagePaths 只检查候选 seam，不加载 parity 测试不消费的语法和类型信息。
// 生产类型守卫和 SSA 守卫继续使用原有的 typed loader。
func loadWideOrchestrationTypeGuardPackagePaths(t *testing.T, root string) []string {
	t.Helper()
	loaded, err := ssaload.Load(ssaload.Options{
		RepoRoot: root,
		Patterns: []string{"./cmd/...", "./internal/..."},
		Overlay:  wideOrchestrationGuardOverlay(root),
		LoadMode: packages.LoadFiles,
		Include: func(pkg *packages.Package) bool {
			return pkg != nil && isOrchestrationServiceTypeGuardProductionPackagePath(pkg.PkgPath) && len(pkg.GoFiles) > 0
		},
	})
	if err != nil {
		t.Fatalf("load production package paths: %v", err)
	}
	paths := make([]string, 0, len(loaded))
	for _, pkg := range loaded {
		paths = append(paths, pkg.PkgPath)
	}
	sort.Strings(paths)
	return paths
}

func assertWideOrchestrationLoaderCandidates(
	t *testing.T,
	root string,
	load func(*testing.T, string) []string,
) {
	t.Helper()
	pkgs := load(t, root)
	paths := make([]string, len(pkgs))
	copy(paths, pkgs)
	sort.Strings(paths)
	const wantCount = 260
	const wantDigest = "8693ad04b40932ded14d3a27c9e6a5d6501a00a42ec1844eab84704082ed7f8a"
	if len(paths) != wantCount || stablePathDigest(paths) != wantDigest {
		t.Fatalf("seam cd81d4c9a wide candidates count=%d digest=%s", len(paths), stablePathDigest(paths))
	}
	for _, required := range []string{
		"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/processobserve",
		"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/processprobe",
	} {
		if !containsWidePath(paths, required) {
			t.Fatalf("wide candidate set is missing %s", required)
		}
	}
}

func containsWidePath(paths []string, want string) bool {
	return slices.Contains(paths, want)
}

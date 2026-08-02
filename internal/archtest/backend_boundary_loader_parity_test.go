package archtest_test

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest/ssaload"
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
}

// TestWideOrchestrationLoaderExtractionPreservesCandidates 固定 seam 的候选集，并避免在 parity 检查中重复全仓加载。
func TestWideOrchestrationLoaderExtractionPreservesCandidates(t *testing.T) {
	root := repoRoot(t)
	loads := 0
	assertWideOrchestrationLoaderCandidates(t, root, func(t *testing.T, path string) []*orchestrationServiceCheckedPackage {
		loads++
		return loadWideOrchestrationTypeGuardPackages(t, path)
	})
	if loads != 1 {
		t.Fatalf("wide orchestration loader calls=%d, want 1", loads)
	}
}

func assertWideOrchestrationLoaderCandidates(
	t *testing.T,
	root string,
	load func(*testing.T, string) []*orchestrationServiceCheckedPackage,
) {
	t.Helper()
	pkgs := load(t, root)
	paths := make([]string, len(pkgs))
	for i, pkg := range pkgs {
		paths[i] = pkg.pkgPath
	}
	sort.Strings(paths)
	const wantCount = 253
	const wantDigest = "d555b259698bf6d81b9202b4d7bdd01e21d231ff451eb296b135acb54135d300"
	if len(paths) != wantCount || stablePathDigest(paths) != wantDigest {
		t.Fatalf("seam cd81d4c9a wide candidates count=%d digest=%s", len(paths), stablePathDigest(paths))
	}
}

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

// TestWideOrchestrationLoaderExtractionPreservesCandidates 固定 seam 的候选集与空违规基线。
func TestWideOrchestrationLoaderExtractionPreservesCandidates(t *testing.T) {
	root := repoRoot(t)
	pkgs := loadWideOrchestrationTypeGuardPackages(t, root)
	paths := make([]string, len(pkgs))
	for i, pkg := range pkgs {
		paths[i] = pkg.pkgPath
	}
	sort.Strings(paths)
	const wantCount = 239
	const wantDigest = "b70308d9b612b7319ea732d71c01d0363938d67fd889d9bfc240fe5367e15645"
	if len(paths) != wantCount || stablePathDigest(paths) != wantDigest {
		t.Fatalf("seam cd81d4c9a wide candidates count=%d digest=%s", len(paths), stablePathDigest(paths))
	}
	if violations := collectWideOrchestrationProductionViolationMessages(t, root); len(violations) != 0 {
		t.Fatalf("wide violations=%v", violations)
	}
}

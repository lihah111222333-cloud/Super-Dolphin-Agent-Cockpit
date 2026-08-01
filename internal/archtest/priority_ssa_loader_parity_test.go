package archtest

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestPrioritySSALoaderExtractionPreservesCandidates 固定 seam 的 priority 候选集，并避免在 parity 检查中重复全仓加载。
func TestPrioritySSALoaderExtractionPreservesCandidates(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	loads := 0
	assertPrioritySSALoaderCandidates(t, root, func(path string) ([]*prioritySSAPackage, error) {
		loads++
		return loadPrioritySSAPackages(path)
	})
	if loads != 1 {
		t.Fatalf("priority loader calls=%d, want 1", loads)
	}
}

func assertPrioritySSALoaderCandidates(
	t *testing.T,
	root string,
	load func(string) ([]*prioritySSAPackage, error),
) {
	t.Helper()
	pkgs, err := load(root)
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, len(pkgs))
	for i, pkg := range pkgs {
		paths[i] = pkg.pkgPath
	}
	sort.Strings(paths)
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(paths, "\n"))))
	const wantCount = 255
	const wantDigest = "d16682eb40c864a3f90e2f154bffc768a75c6e3dbf5fbefa6861066b57e57afc"
	if len(paths) != wantCount || digest != wantDigest {
		t.Fatalf("seam cd81d4c9a priority candidates count=%d digest=%s", len(paths), digest)
	}
}

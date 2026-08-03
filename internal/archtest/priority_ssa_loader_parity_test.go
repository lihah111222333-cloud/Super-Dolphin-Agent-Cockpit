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
	const wantCount = 254
	const wantDigest = "c304acad566794d4a5c0ead6e8b21ada9c2f49ac17b00a4c11a560257bc044e9"
	if len(paths) != wantCount || digest != wantDigest {
		t.Fatalf("seam cd81d4c9a priority candidates count=%d digest=%s", len(paths), digest)
	}
}

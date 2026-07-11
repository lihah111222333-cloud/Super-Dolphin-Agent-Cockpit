package archtest

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestPrioritySSALoaderExtractionPreservesCandidates 固定 seam 的 priority 候选集与违规基线。
func TestPrioritySSALoaderExtractionPreservesCandidates(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	pkgs, err := loadPrioritySSAPackages(root)
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, len(pkgs))
	for i, pkg := range pkgs {
		paths[i] = pkg.pkgPath
	}
	sort.Strings(paths)
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(paths, "\n"))))
	const wantCount = 215
	const wantDigest = "ddf59472af245994381c9a35aece78799664fe7aa145a6bc73ecf7f1d21a54d5"
	if len(paths) != wantCount || digest != wantDigest {
		t.Fatalf("seam cd81d4c9a priority candidates count=%d digest=%s", len(paths), digest)
	}
	violations, err := CollectPrioritySSAViolations(CheckOptions{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("priority violations=%v", violations)
	}
}

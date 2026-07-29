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
	const wantCount = 242
	const wantDigest = "a071419ca6eac00878de207eb4e79317bf37a24144bfda9bd8bce7beea01fa2a"
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

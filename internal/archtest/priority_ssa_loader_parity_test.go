package archtest

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"slices"
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
	const wantCount = 258
	const wantDigest = "7f93776c61a8d0dfcd3aa11118c03c1e3c64c885b31acd42478eee4cbbc84eea"
	if len(paths) != wantCount || digest != wantDigest {
		t.Fatalf("seam cd81d4c9a priority candidates count=%d digest=%s", len(paths), digest)
	}
	for _, required := range []string{
		"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/processobserve",
		"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/processprobe",
	} {
		if !containsPriorityPath(paths, required) {
			t.Fatalf("priority candidate set is missing %s", required)
		}
	}
}

func containsPriorityPath(paths []string, want string) bool {
	return slices.Contains(paths, want)
}

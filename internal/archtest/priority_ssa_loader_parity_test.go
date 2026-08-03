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
	const wantCount = 256
	const wantDigest = "532c2cb4cd83ea0906e6e841a63d8a6a8296850737b3d75697e61f0a007ef03e"
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
	for _, path := range paths {
		if path == want {
			return true
		}
	}
	return false
}

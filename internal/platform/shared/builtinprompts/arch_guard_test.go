package builtinprompts

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest"
)

func TestBuiltinPromptsTargetArchGuards(t *testing.T) {
	t.Parallel()

	violations := archtest.CheckAll(archtest.CheckOptions{
		RepoRoot: builtinPromptsRepoRoot(t),
		ScanRoots: []string{
			"internal/platform/shared/builtinprompts/load.go",
			"internal/platform/shared/builtinprompts/validate.go",
		},
		SkipDirs: archtest.DefaultSkipDirs(),
	})
	if len(violations) == 0 {
		return
	}

	lines := make([]string, 0, len(violations))
	for _, violation := range violations {
		lines = append(lines, violation.String())
	}
	t.Fatalf("builtinprompts target arch guard violations (%d):\n%s", len(lines), strings.Join(lines, "\n"))
}

func builtinPromptsRepoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("go.mod not found from %s: %v", root, err)
	}
	return root
}

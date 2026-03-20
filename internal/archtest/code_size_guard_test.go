package archtest_test

import (
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/archtest"
)

func TestCodeSizeGuard(t *testing.T) {
	violations := archtest.CheckAll(archtest.CheckOptions{
		RepoRoot:  repoRoot(t),
		ScanRoots: []string{"internal", "cmd", "scripts"},
		SkipDirs:  archtest.DefaultSkipDirs(),
	})
	if len(violations) == 0 {
		return
	}
	lines := make([]string, 0, len(violations))
	for _, violation := range violations {
		lines = append(lines, violation.String())
	}
	t.Fatalf("code size guard violations (%d):\n%s", len(violations), strings.Join(lines, "\n"))
}

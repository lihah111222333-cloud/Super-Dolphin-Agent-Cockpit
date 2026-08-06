package codemapindex

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// TestModuleReadPromptDirectProductionFileCountMatchesNarrativeAndMarker 锁定 prompt 直属生产 Go 文件数与正文、marker 一致。
func TestModuleReadPromptDirectProductionFileCountMatchesNarrativeAndMarker(t *testing.T) {
	root := codemapGeneratorRepoRoot(t)
	actual, err := countDirectGoFiles(filepath.Join(root, "internal", "module", "prompt"), false)
	if err != nil {
		t.Fatalf("count prompt direct production Go files: %v", err)
	}

	content := readCodemapSemanticFixture(t, root, "docs/doc/codemap/07-module-read.md")
	assertSingleCodemapStatement(t, content, fmt.Sprintf(
		"`internal/module/prompt/` 的生产文件数由下面的机器计数声明直接锁定为 %d。", actual,
	))
	assertSingleCodemapStatement(t, content, fmt.Sprintf(
		"相邻 `prompt` 真值仍以 §1.1 的 `%d` 为准。", actual,
	))
	assertSingleCodemapStatement(t, content, fmt.Sprintf(
		"<!-- codemap-count path=\"internal/module/prompt\" kind=\"go-files\" expected=\"%d\" -->", actual,
	))
}

func assertSingleCodemapStatement(t *testing.T, content, want string) {
	t.Helper()
	if got := strings.Count(content, want); got != 1 {
		t.Fatalf("codemap statement %q count = %d, want 1", want, got)
	}
}

package codemapindex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestModuleReadPromptDirectProductionFileCountUsesGeneratedMetric 锁定 prompt 计数只来自生成索引。
func TestModuleReadPromptDirectProductionFileCountUsesGeneratedMetric(t *testing.T) {
	root := codemapGeneratorRepoRoot(t)
	actual, err := countDirectGoFiles(filepath.Join(root, "internal", "module", "prompt"), false)
	if err != nil {
		t.Fatalf("count prompt direct production Go files: %v", err)
	}

	content := readCodemapSemanticFixture(t, root, "docs/doc/codemap/07-module-read.md")
	assertSingleCodemapStatement(t, content, "<!-- codemap-count path=\"internal/module/prompt\" kind=\"go-files\" -->")
	if strings.Contains(content, "codemap-count path=\"internal/module/prompt\" kind=\"go-files\" expected=") {
		t.Fatal("prompt codemap count must not contain a hand-maintained expected value")
	}

	data, err := os.ReadFile(filepath.Join(root, "docs", "doc", "codemap", "ai-index.json"))
	if err != nil {
		t.Fatalf("read generated ai-index: %v", err)
	}
	var index Index
	if err := json.Unmarshal(data, &index); err != nil {
		t.Fatalf("decode generated ai-index: %v", err)
	}
	for _, count := range index.Counts {
		if count.Path == "internal/module/prompt" && count.Kind == "go-files" {
			if count.Value != actual {
				t.Fatalf("generated prompt count = %d, want %d", count.Value, actual)
			}
			return
		}
	}
	t.Fatal("generated ai-index is missing internal/module/prompt go-files count")
}

func assertSingleCodemapStatement(t *testing.T, content, want string) {
	t.Helper()
	if got := strings.Count(content, want); got != 1 {
		t.Fatalf("codemap statement %q count = %d, want 1", want, got)
	}
}

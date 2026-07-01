package archtest

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestLSPToolPositionConversionsUseSharedHelper 防止位置型 LSP 工具重新直接拼 protocol.Position。
// 用户输入列号是 1-based rune column；发给 LSP 前必须经 ResolveLSPPosition/linePositionMapping 转 UTF-16。
func TestLSPToolPositionConversionsUseSharedHelper(t *testing.T) {
	const dir = "../../cmd/mcp-lsp/tools"
	forbidden := []struct {
		name    string
		pattern *regexp.Regexp
	}{
		{
			name:    "direct column minus one conversion",
			pattern: regexp.MustCompile(`Character:\s*(column|col)\s*-\s*1`),
		},
		{
			name:    "completion retry direct character position",
			pattern: regexp.MustCompile(`protocol\.Position\s*\{[^}]*Character:\s*character`),
		},
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(raw)
		for _, rule := range forbidden {
			if rule.pattern.MatchString(text) {
				t.Errorf("%s: forbidden %s; route LSP positions through ResolveLSPPosition or linePositionMapping", path, rule.name)
			}
		}
	}
	assertLSPToolPositionHelperPresent(t, filepath.Join(dir, "factory.go"), "func ResolveLSPPosition(")
	assertLSPToolPositionHelperPresent(t, filepath.Join(dir, "factory.go"), "func utf16OffsetsForRunes(")
	assertLSPToolPositionHelperPresent(t, filepath.Join(dir, "tool_completion.go"), "positionFromRuneIndex")
}

func assertLSPToolPositionHelperPresent(t *testing.T, path string, want string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(raw), want) {
		t.Fatalf("%s missing %q", path, want)
	}
}

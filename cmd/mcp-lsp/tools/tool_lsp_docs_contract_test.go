package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLSPUsageDocsUseReadFilePosContract(t *testing.T) {
	for _, rel := range []string{
		"docs/internal-notes/LSP系统提示词.md",
		"docs/internal-notes/lsp提示词英文版.md",
	} {
		body, err := os.ReadFile(filepath.Join("..", "..", "..", rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		text := string(body)
		if strings.Contains(text, "offset=") || strings.Contains(text, "read_file, offset") || strings.Contains(text, "read_file(offset") {
			t.Fatalf("%s still documents removed read_file offset contract", rel)
		}
		if !strings.Contains(text, "pos=<file>:<line>") {
			t.Fatalf("%s does not document read_file pos=<file>:<line> contract", rel)
		}
	}
}

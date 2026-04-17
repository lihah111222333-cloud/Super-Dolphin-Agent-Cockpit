package tools

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
)

func TestReadFileWithModeNormalizesCRLF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.txt")
	raw := "first\r\nsecond\r\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	file, err := readFileWithMode(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if file.content != "first\nsecond\n" {
		t.Fatalf("content mismatch: %q", file.content)
	}
	if file.raw != raw {
		t.Fatalf("raw mismatch: %q", file.raw)
	}
	if file.lineEnding != lineEndingCRLF {
		t.Fatalf("line ending mismatch: %q", file.lineEnding)
	}
	if restored := file.diskContent(file.content); restored != raw {
		t.Fatalf("restored mismatch: %q", restored)
	}
}

func TestBuildRangeReplacePlanRestoresCRLF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.txt")
	raw := "a\r\nb\r\nc\r\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	file, err := readFileWithMode(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	plan, err := buildRangeReplacePlan(file.content, EditRequest{
		Line:      2,
		Column:    1,
		EndLine:   2,
		EndColumn: 2,
		NewText:   "x\r\ny",
	})
	if err != nil {
		t.Fatalf("build range plan: %v", err)
	}
	if plan.updatedContent != "a\nx\ny\nc\n" {
		t.Fatalf("updated content mismatch: %q", plan.updatedContent)
	}
	if restored := file.diskContent(plan.updatedContent); restored != "a\r\nx\r\ny\r\nc\r\n" {
		t.Fatalf("restored mismatch: %q", restored)
	}
}

func TestApplyTextEditsNormalizesInsertedCRLF(t *testing.T) {
	content := "a\nb\n"
	updated, err := applyTextEdits(content, []protocol.TextEdit{{
		Range: protocol.Range{
			Start: protocol.Position{Line: 0, Character: 0},
			End:   protocol.Position{Line: 0, Character: 1},
		},
		NewText: "x\r\ny",
	}})
	if err != nil {
		t.Fatalf("apply text edits: %v", err)
	}
	if updated != "x\ny\nb\n" {
		t.Fatalf("updated mismatch: %q", updated)
	}
}

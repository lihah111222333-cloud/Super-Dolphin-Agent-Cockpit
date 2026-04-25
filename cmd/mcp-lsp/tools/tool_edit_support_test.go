package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
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

func TestReplaceRangeAppliesUnsupportedTextFilesWithoutLSPManager(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		content string
		old     string
		new     string
		want    string
	}{
		{
			name:    "markdown",
			file:    "plan.md",
			content: "# Title\n\nold markdown line\n",
			old:     "old markdown line",
			new:     "new markdown line",
			want:    "# Title\n\nnew markdown line\n",
		},
		{
			name:    "plaintext",
			file:    "notes.txt",
			content: "first\nold text\nlast\n",
			old:     "old text",
			new:     "new text",
			want:    "first\nnew text\nlast\n",
		},
		{
			name:    "json",
			file:    "config.json",
			content: "{\n  \"mode\": \"old\"\n}\n",
			old:     "  \"mode\": \"old\"",
			new:     "  \"mode\": \"new\"",
			want:    "{\n  \"mode\": \"new\"\n}\n",
		},
		{
			name:    "yaml",
			file:    "config.yaml",
			content: "server:\n  port: 8080\nfeatures:\n  dag: false\n",
			old:     "  dag: false",
			new:     "  dag: true",
			want:    "server:\n  port: 8080\nfeatures:\n  dag: true\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tt.file)
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			handler := NewEditHandler(&structureTestRegistry{fileErr: lspmanager.ErrUnsupportedLanguage})
			input, err := json.Marshal(EditRequest{
				Action:   "replace_range",
				FilePath: path,
				Edits: []ReplaceEdit{{
					OldString: tt.old,
					NewString: tt.new,
				}},
			})
			if err != nil {
				t.Fatalf("marshal input: %v", err)
			}

			got, err := handler(context.Background(), input)
			if err != nil {
				t.Fatalf("replace_range returned error: %v", err)
			}
			result, ok := got.(replaceRangeResult)
			if !ok {
				t.Fatalf("result type = %T, want replaceRangeResult", got)
			}
			if !result.Success || !result.Applied || result.Status != "applied" {
				t.Fatalf("unexpected result: %#v", result)
			}
			if result.LSPSync {
				t.Fatalf("LSPSync = true, want false without LSP manager")
			}
			if !strings.Contains(result.Warning, "LSP sync skipped") {
				t.Fatalf("warning = %q, want LSP sync skipped", result.Warning)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read updated file: %v", err)
			}
			if string(raw) != tt.want {
				t.Fatalf("updated content mismatch:\nwant %q\ngot  %q", tt.want, string(raw))
			}
		})
	}
}

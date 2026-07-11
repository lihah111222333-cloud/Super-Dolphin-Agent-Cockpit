package tools

import (
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/format"
)

func TestFormatToPlainTextUsesCompletionTitleForCompactCompletionList(t *testing.T) {
	list := format.NewCompactList([]format.CompactCompletionItem{
		{Label: "Println", Kind: 3, Detail: "func"},
	}, 2)

	text, ok := FormatToPlainText(list)
	if !ok {
		t.Fatalf("FormatToPlainText() handled = false")
	}
	if !strings.Contains(text, "Code Completions: showing 1 of 2 total") {
		t.Fatalf("text = %q, want completion title", text)
	}
	if strings.Contains(text, "Workspace Search Matches") {
		t.Fatalf("text = %q, contains workspace search title for completion list", text)
	}
}

func TestFormatToPlainTextUsesWorkspaceSymbolTitleForCompactWorkspaceList(t *testing.T) {
	list := format.NewCompactList([]format.CompactWorkspaceSymbol{
		{Name: "Target", Kind: 12, File: "target.go", Line: 7, Col: 3},
	}, 1)

	text, ok := FormatToPlainText(list)
	if !ok {
		t.Fatalf("FormatToPlainText() handled = false")
	}
	if !strings.Contains(text, "Workspace Symbol Matches: showing 1 of 1 total") {
		t.Fatalf("text = %q, want workspace symbol title", text)
	}
}

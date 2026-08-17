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
	if !strings.HasPrefix(text, "OK total=2 showing=1 truncated=1 unit=completion\n") {
		t.Fatalf("text = %q, want completion line-protocol header", text)
	}
	if !strings.Contains(text, "ROW\tlabel=Println\tkind=3\tdetail=func") {
		t.Fatalf("text = %q, want retained completion row", text)
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
	if !strings.HasPrefix(text, "OK total=1 showing=1 truncated=0 unit=symbol\n") {
		t.Fatalf("text = %q, want workspace-symbol line-protocol header", text)
	}
	if !strings.Contains(text, "ROW\tname=Target\tkind=12\tfile=target.go\tline=7\tcol=3") {
		t.Fatalf("text = %q, want retained workspace-symbol row", text)
	}
}

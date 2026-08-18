package tools

import (
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/format"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
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

func TestFormatLocations1BasedCoordinates(t *testing.T) {
	locations := []protocol.LocationResult{
		{
			Location: &protocol.Location{
				URI: "file:///workspace/main.go",
				Range: protocol.Range{
					Start: protocol.Position{Line: 0, Character: 0},
					End:   protocol.Position{Line: 0, Character: 5},
				},
			},
		},
	}
	text := formatLocations(locations)
	if !strings.Contains(text, "/workspace/main.go:1:1") {
		t.Fatalf("formatLocations text = %q, want 1-based coordinates :1:1", text)
	}
}

func TestFormatCallHierarchy1BasedCoordinates(t *testing.T) {
	items := []protocol.CallHierarchyResult{
		{
			Item: protocol.CallHierarchyItem{
				Name: "Caller",
				Kind: int(protocol.SymbolKindFunction),
				URI:  "file:///workspace/call.go",
				Range: protocol.Range{
					Start: protocol.Position{Line: 9, Character: 4},
				},
			},
			Incoming: []protocol.CallHierarchyIncomingCall{
				{
					From: protocol.CallHierarchyItem{
						Name: "CallerFrom",
						Kind: int(protocol.SymbolKindFunction),
						URI:  "file:///workspace/from.go",
						Range: protocol.Range{
							Start: protocol.Position{Line: 19, Character: 8},
						},
					},
					FromRanges: []protocol.Range{
						{
							Start: protocol.Position{Line: 29, Character: 12},
						},
					},
				},
			},
			Outgoing: []protocol.CallHierarchyOutgoingCall{
				{
					To: protocol.CallHierarchyItem{
						Name: "CalleeTo",
						Kind: int(protocol.SymbolKindFunction),
						URI:  "file:///workspace/to.go",
						Range: protocol.Range{
							Start: protocol.Position{Line: 39, Character: 2},
						},
					},
					FromRanges: []protocol.Range{
						{
							Start: protocol.Position{Line: 49, Character: 6},
						},
					},
				},
			},
		},
	}
	text := formatCallHierarchy(items)
	if !strings.Contains(text, "/workspace/call.go:10:5") {
		t.Fatalf("formatCallHierarchy root text = %q, want 10:5", text)
	}
	if !strings.Contains(text, "/workspace/from.go:20:9") {
		t.Fatalf("formatCallHierarchy incoming text = %q, want 20:9", text)
	}
	if !strings.Contains(text, "/workspace/from.go:30:13") {
		t.Fatalf("formatCallHierarchy incoming call site text = %q, want 30:13", text)
	}
	if !strings.Contains(text, "/workspace/to.go:40:3") {
		t.Fatalf("formatCallHierarchy outgoing text = %q, want 40:3", text)
	}
	if !strings.Contains(text, "/workspace/to.go:50:7") {
		t.Fatalf("formatCallHierarchy outgoing call site text = %q, want 50:7", text)
	}
}

func TestFormatTypeHierarchy1BasedCoordinates(t *testing.T) {
	items := []protocol.TypeHierarchyResult{
		{
			Item: protocol.TypeHierarchyItem{
				Name: "MyClass",
				Kind: int(protocol.SymbolKindClass),
				URI:  "file:///workspace/class.go",
				Range: protocol.Range{
					Start: protocol.Position{Line: 4, Character: 2},
				},
			},
			Supertypes: []protocol.TypeHierarchyItem{
				{
					Name: "SuperClass",
					Kind: int(protocol.SymbolKindClass),
					URI:  "file:///workspace/super.go",
					Range: protocol.Range{
						Start: protocol.Position{Line: 14, Character: 6},
					},
				},
			},
			Subtypes: []protocol.TypeHierarchyItem{
				{
					Name: "SubClass",
					Kind: int(protocol.SymbolKindClass),
					URI:  "file:///workspace/sub.go",
					Range: protocol.Range{
						Start: protocol.Position{Line: 24, Character: 8},
					},
				},
			},
		},
	}
	text := formatTypeHierarchy(items)
	if !strings.Contains(text, "/workspace/class.go:5:3") {
		t.Fatalf("formatTypeHierarchy item text = %q, want 5:3", text)
	}
	if !strings.Contains(text, "/workspace/super.go:15:7") {
		t.Fatalf("formatTypeHierarchy supertype text = %q, want 15:7", text)
	}
	if !strings.Contains(text, "/workspace/sub.go:25:9") {
		t.Fatalf("formatTypeHierarchy subtype text = %q, want 25:9", text)
	}
}

func TestFormatDocumentOutline1BasedCoordinates(t *testing.T) {
	symbols := []protocol.DocumentSymbol{
		{
			Name: "RootFunction",
			Kind: protocol.SymbolKindFunction,
			Range: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 0},
				End:   protocol.Position{Line: 9, Character: 0},
			},
		},
	}
	text := formatDocumentOutline(symbols)
	if !strings.Contains(text, "(L1-L10)") {
		t.Fatalf("formatDocumentOutline text = %q, want (L1-L10)", text)
	}
}

func TestFormatWorkspaceSymbols1BasedCoordinates(t *testing.T) {
	symbols := []protocol.WorkspaceSymbolResult{
		{
			SymbolInformation: &protocol.SymbolInformation{
				Name: "GlobalVar",
				Kind: protocol.SymbolKindVariable,
				Location: protocol.Location{
					URI: "file:///workspace/vars.go",
					Range: protocol.Range{
						Start: protocol.Position{Line: 2, Character: 4},
					},
				},
			},
		},
	}
	text := formatWorkspaceSymbols(symbols)
	if !strings.Contains(text, "/workspace/vars.go:3:5") {
		t.Fatalf("formatWorkspaceSymbols text = %q, want 3:5", text)
	}
}

func TestFormatFoldingRanges1BasedCoordinates(t *testing.T) {
	ranges := []protocol.FoldingRange{
		{
			StartLine: 0,
			EndLine:   10,
			Kind:      "imports",
		},
	}
	text := formatFoldingRanges(ranges)
	if !strings.Contains(text, "Lines L1 - L11") {
		t.Fatalf("formatFoldingRanges text = %q, want Lines L1 - L11", text)
	}
}


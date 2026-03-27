package format

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/protocol"
)

func FromLSP(v int) int {
	if v < 0 {
		return v
	}
	return v + 1
}

func FromLSPPtr(v *int) *int {
	if v == nil {
		return nil
	}
	value := FromLSP(*v)
	return &value
}

func Position(pos protocol.Position) protocol.Position {
	pos.Line = FromLSP(pos.Line)
	pos.Character = FromLSP(pos.Character)
	return pos
}

func Range(r protocol.Range) protocol.Range {
	r.Start = Position(r.Start)
	r.End = Position(r.End)
	return r
}

func Ranges(items []protocol.Range) []protocol.Range {
	if len(items) == 0 {
		return items
	}
	out := make([]protocol.Range, len(items))
	for i := range items {
		out[i] = Range(items[i])
	}
	return out
}

func Location(loc protocol.Location) protocol.Location {
	loc.URI = URIToPath(loc.URI)
	loc.Range = Range(loc.Range)
	return loc
}

func LocationPtr(loc *protocol.Location) *protocol.Location {
	if loc == nil {
		return nil
	}
	converted := Location(*loc)
	return &converted
}

func LocationLinkPtr(link *protocol.LocationLink) *protocol.LocationLink {
	if link == nil {
		return nil
	}
	converted := *link
	converted.TargetURI = URIToPath(link.TargetURI)
	converted.TargetRange = Range(link.TargetRange)
	converted.TargetSelectionRange = Range(link.TargetSelectionRange)
	if link.OriginSelectionRange != nil {
		origin := Range(*link.OriginSelectionRange)
		converted.OriginSelectionRange = &origin
	}
	return &converted
}

func LocationResults(items []protocol.LocationResult) []protocol.LocationResult {
	if len(items) == 0 {
		return items
	}
	out := make([]protocol.LocationResult, len(items))
	for i := range items {
		item := items[i]
		item.Location = LocationPtr(item.Location)
		item.Canonical = LocationPtr(item.Canonical)
		item.LocationLink = LocationLinkPtr(item.LocationLink)
		out[i] = item
	}
	return out
}

func DocumentSymbol(symbol protocol.DocumentSymbol) protocol.DocumentSymbol {
	symbol.Range = Range(symbol.Range)
	symbol.SelectionRange = Range(symbol.SelectionRange)
	if len(symbol.Children) == 0 {
		return symbol
	}
	children := make([]protocol.DocumentSymbol, len(symbol.Children))
	for i := range symbol.Children {
		children[i] = DocumentSymbol(symbol.Children[i])
	}
	symbol.Children = children
	return symbol
}

func DocumentSymbols(items []protocol.DocumentSymbol) []protocol.DocumentSymbol {
	if len(items) == 0 {
		return items
	}
	out := make([]protocol.DocumentSymbol, len(items))
	for i := range items {
		out[i] = DocumentSymbol(items[i])
	}
	return out
}

func TextEdit(edit protocol.TextEdit) protocol.TextEdit {
	edit.Range = Range(edit.Range)
	return edit
}

func TextEdits(items []protocol.TextEdit) []protocol.TextEdit {
	if len(items) == 0 {
		return items
	}
	out := make([]protocol.TextEdit, len(items))
	for i := range items {
		out[i] = TextEdit(items[i])
	}
	return out
}

func WorkspaceEdit(edit *protocol.WorkspaceEdit) *protocol.WorkspaceEdit {
	if edit == nil {
		return nil
	}
	out := &protocol.WorkspaceEdit{}
	if len(edit.Changes) > 0 {
		out.Changes = make(map[string][]protocol.TextEdit, len(edit.Changes))
		for uri, edits := range edit.Changes {
			out.Changes[URIToPath(uri)] = TextEdits(edits)
		}
	}
	if len(edit.DocumentChanges) > 0 {
		out.DocumentChanges = make([]protocol.TextDocumentEdit, len(edit.DocumentChanges))
		for i := range edit.DocumentChanges {
			item := edit.DocumentChanges[i]
			item.TextDocument.URI = URIToPath(item.TextDocument.URI)
			item.Edits = TextEdits(item.Edits)
			out.DocumentChanges[i] = item
		}
	}
	return out
}

func Diagnostic(diag protocol.Diagnostic) protocol.Diagnostic {
	diag.Range = Range(diag.Range)
	for i := range diag.RelatedInformation {
		diag.RelatedInformation[i].Location = Location(diag.RelatedInformation[i].Location)
	}
	return diag
}

func Diagnostics(items []protocol.Diagnostic) []protocol.Diagnostic {
	if len(items) == 0 {
		return items
	}
	out := make([]protocol.Diagnostic, len(items))
	for i := range items {
		out[i] = Diagnostic(items[i])
	}
	return out
}

func HoverResult(result protocol.HoverResult) protocol.HoverResult {
	if result.Range == nil {
		return result
	}
	converted := Range(*result.Range)
	result.Range = &converted
	return result
}

func CodeActionResults(items []protocol.CodeActionResult) []protocol.CodeActionResult {
	if len(items) == 0 {
		return items
	}
	out := make([]protocol.CodeActionResult, len(items))
	for i := range items {
		item := items[i]
		if item.CodeAction != nil {
			action := *item.CodeAction
			action.Diagnostics = Diagnostics(action.Diagnostics)
			action.Edit = WorkspaceEdit(action.Edit)
			item.CodeAction = &action
		}
		out[i] = item
	}
	return out
}

func WorkspaceSymbolResults(items []protocol.WorkspaceSymbolResult) []protocol.WorkspaceSymbolResult {
	if len(items) == 0 {
		return items
	}
	out := make([]protocol.WorkspaceSymbolResult, len(items))
	for i := range items {
		item := items[i]
		if item.SymbolInformation != nil {
			si := *item.SymbolInformation
			si.Location = Location(si.Location)
			item.SymbolInformation = &si
		}
		if item.WorkspaceSymbol != nil {
			ws := *item.WorkspaceSymbol
			ws.Location = workspaceSymbolLocationAny(ws.Location)
			item.WorkspaceSymbol = &ws
		}
		out[i] = item
	}
	return out
}

func CallHierarchyResults(items []protocol.CallHierarchyResult) []protocol.CallHierarchyResult {
	if len(items) == 0 {
		return items
	}
	out := make([]protocol.CallHierarchyResult, len(items))
	for i := range items {
		item := items[i]
		item.Item = hierarchyItem(item.Item)
		for j := range item.Incoming {
			item.Incoming[j].From = hierarchyItem(item.Incoming[j].From)
			item.Incoming[j].FromRanges = Ranges(item.Incoming[j].FromRanges)
		}
		for j := range item.Outgoing {
			item.Outgoing[j].To = hierarchyItem(item.Outgoing[j].To)
			item.Outgoing[j].FromRanges = Ranges(item.Outgoing[j].FromRanges)
		}
		out[i] = item
	}
	return out
}

func TypeHierarchyResults(items []protocol.TypeHierarchyResult) []protocol.TypeHierarchyResult {
	if len(items) == 0 {
		return items
	}
	out := make([]protocol.TypeHierarchyResult, len(items))
	for i := range items {
		item := items[i]
		item.Item = typeHierarchyItem(item.Item)
		for j := range item.Supertypes {
			item.Supertypes[j] = typeHierarchyItem(item.Supertypes[j])
		}
		for j := range item.Subtypes {
			item.Subtypes[j] = typeHierarchyItem(item.Subtypes[j])
		}
		out[i] = item
	}
	return out
}

func SemanticTokensResult(result *protocol.SemanticTokensResult) *protocol.SemanticTokensResult {
	if result == nil {
		return nil
	}
	out := *result
	if len(result.Decoded) > 0 {
		out.Decoded = make([]protocol.DecodedSemanticToken, len(result.Decoded))
		for i := range result.Decoded {
			item := result.Decoded[i]
			item.Line = FromLSP(item.Line)
			item.StartCharacter = FromLSP(item.StartCharacter)
			out.Decoded[i] = item
		}
	}
	return &out
}

func FoldingRange(item protocol.FoldingRange) protocol.FoldingRange {
	item.StartLine = FromLSP(item.StartLine)
	item.StartCharacter = FromLSPPtr(item.StartCharacter)
	item.EndLine = FromLSP(item.EndLine)
	item.EndCharacter = FromLSPPtr(item.EndCharacter)
	return item
}

func FoldingRanges(items []protocol.FoldingRange) []protocol.FoldingRange {
	if len(items) == 0 {
		return items
	}
	out := make([]protocol.FoldingRange, len(items))
	for i := range items {
		out[i] = FoldingRange(items[i])
	}
	return out
}

func URIToPath(uri string) string {
	trimmed := strings.TrimSpace(uri)
	if trimmed == "" {
		return ""
	}
	path := parseFileURI(trimmed)
	path = filepath.Clean(path)
	path = tryMakeRelative(path)
	return filepath.ToSlash(path)
}

func parseFileURI(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(parsed.Scheme, "file") {
		return raw
	}
	path := parsed.Path
	if parsed.Host != "" {
		path = "//" + parsed.Host + path
	}
	if unescaped, err := url.PathUnescape(path); err == nil && unescaped != "" {
		path = unescaped
	}
	return path
}

func tryMakeRelative(path string) string {
	cwd, err := os.Getwd()
	if err != nil || !filepath.IsAbs(path) {
		return path
	}
	if rel, err := filepath.Rel(cwd, path); err == nil && withinWorkspace(rel) {
		return rel
	}
	return path
}

func hierarchyItem(item protocol.CallHierarchyItem) protocol.CallHierarchyItem {
	item.URI = URIToPath(item.URI)
	item.Range = Range(item.Range)
	item.SelectionRange = Range(item.SelectionRange)
	return item
}

func typeHierarchyItem(item protocol.TypeHierarchyItem) protocol.TypeHierarchyItem {
	item.URI = URIToPath(item.URI)
	item.Range = Range(item.Range)
	item.SelectionRange = Range(item.SelectionRange)
	return item
}

func withinWorkspace(rel string) bool {
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

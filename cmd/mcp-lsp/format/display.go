package format

import (
	"net/url"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
)

// FromLSP 将 LSP 0-based 坐标转换为用户可读的 1-based 坐标。
// 负数保留原值，避免把缺省或哨兵值误展示成有效位置。
func FromLSP(v int) int {
	if v < 0 {
		return v
	}
	return v + 1
}

// FromLSPPtr 转换可选 LSP 坐标指针。
// nil 表示该坐标未提供，必须原样保留给调用方区分。
func FromLSPPtr(v *int) *int {
	if v == nil {
		return nil
	}
	value := FromLSP(*v)
	return &value
}

// Position 将 LSP position 转为展示坐标。
func Position(pos protocol.Position) protocol.Position {
	pos.Line = FromLSP(pos.Line)
	pos.Character = FromLSP(pos.Character)
	return pos
}

// Range 将 LSP range 的起止位置都转为展示坐标。
func Range(r protocol.Range) protocol.Range {
	r.Start = Position(r.Start)
	r.End = Position(r.End)
	return r
}

// Ranges 批量转换 range，空切片保持原样避免无意义分配。
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

// Location 将 LSP location 转成可读路径和 1-based range。
func Location(loc protocol.Location) protocol.Location {
	loc.URI = URIToPath(loc.URI)
	loc.Range = Range(loc.Range)
	return loc
}

// LocationPtr 转换可选 location，nil 表示 LSP 没有返回位置。
func LocationPtr(loc *protocol.Location) *protocol.Location {
	if loc == nil {
		return nil
	}
	converted := Location(*loc)
	return &converted
}

// LocationLinkPtr 转换 definition/implementation 可能返回的 location link。
// target 与 origin range 都要保持同一套 1-based 展示坐标。
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

// LocationResults 规整 references/definition 等工具的 union 位置结果。
// 兼容 Location、Canonical 和 LocationLink 三种返回形态。
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

// DocumentSymbol 递归转换 document symbol 的范围坐标。
// children 会复制到新切片，避免修改调用方持有的原始 LSP 响应。
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

// DocumentSymbols 批量转换文档符号树。
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

// TextEdit 转换文本编辑范围，便于 code_action/edit 结果直接展示。
func TextEdit(edit protocol.TextEdit) protocol.TextEdit {
	edit.Range = Range(edit.Range)
	return edit
}

// TextEdits 批量转换文本编辑范围。
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

// WorkspaceEdit 转换工作区编辑中的 URI 和 range。
// Changes 与 DocumentChanges 两种 LSP 形态都会被规整到可读路径。
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

// Diagnostic 转换诊断范围和相关信息位置。
func Diagnostic(diag protocol.Diagnostic) protocol.Diagnostic {
	diag.Range = Range(diag.Range)
	for i := range diag.RelatedInformation {
		diag.RelatedInformation[i].Location = Location(diag.RelatedInformation[i].Location)
	}
	return diag
}

// Diagnostics 批量转换诊断结果。
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

// HoverResult 转换 hover 结果中的可选范围。
func HoverResult(result protocol.HoverResult) protocol.HoverResult {
	if result.Range == nil {
		return result
	}
	converted := Range(*result.Range)
	result.Range = &converted
	return result
}

// CodeActionResults 转换 code action 结果中的诊断和 workspace edit。
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

// WorkspaceSymbolResults 规整 workspace symbol 的两种协议返回形态。
// SymbolInformation 和 WorkspaceSymbol 都会转成可读路径。
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

// CallHierarchyResults 转换调用层级结果的 item URI 和 from/to ranges。
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

// TypeHierarchyResults 转换类型层级中的 item URI 和 range。
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

// SemanticTokensResult 转换已解码语义 token 的展示坐标。
// 原始 Data 不在这里重写，只处理工具层额外解码出的 Decoded 列表。
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

// FoldingRange 转换折叠范围行列坐标。
func FoldingRange(item protocol.FoldingRange) protocol.FoldingRange {
	item.StartLine = FromLSP(item.StartLine)
	item.StartCharacter = FromLSPPtr(item.StartCharacter)
	item.EndLine = FromLSP(item.EndLine)
	item.EndCharacter = FromLSPPtr(item.EndCharacter)
	return item
}

// FoldingRanges 批量转换折叠范围。
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

// URIToPath 将 file URI 或路径转成展示用绝对路径。
// 这里始终返回斜杠规范化后的绝对路径，避免不同绑定 cwd 下出现误导性的相对路径。
func URIToPath(uri string) string {
	trimmed := strings.TrimSpace(uri)
	if trimmed == "" {
		return ""
	}
	if path, err := AbsolutePathFromURI(trimmed); err == nil {
		return filepath.ToSlash(filepath.Clean(path))
	}
	path := parseFileURI(trimmed)
	path = filepath.Clean(path)
	return filepath.ToSlash(path)
}

// parseFileURI 解析 file URI，保留非 file 输入原文。
// Windows/UNC host 会拼回路径前缀，确保后续 filepath.Clean 能按路径处理。
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

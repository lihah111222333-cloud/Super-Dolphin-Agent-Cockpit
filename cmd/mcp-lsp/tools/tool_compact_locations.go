package tools

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/format"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	common "github.com/anthropic-ai/super-agent-v3/internal/mcpserver/runtime"
)

type compactHierarchyResult struct {
	Item     compactHierarchyItem           `json:"item"`
	Incoming []compactHierarchyIncomingCall `json:"incoming,omitempty"`
	Outgoing []compactHierarchyOutgoingCall `json:"outgoing,omitempty"`
}

type compactHierarchyItem struct {
	Name   string `json:"name"`
	Kind   int    `json:"kind,omitempty"`
	File   string `json:"file,omitempty"`
	Line   int    `json:"line"`
	Col    int    `json:"col"`
	Detail string `json:"detail,omitempty"`
}

type compactHierarchyIncomingCall struct {
	From       compactHierarchyItem       `json:"from"`
	FromRanges []compactHierarchyLocation `json:"fromRanges,omitempty"`
}

type compactHierarchyOutgoingCall struct {
	To         compactHierarchyItem       `json:"to"`
	FromRanges []compactHierarchyLocation `json:"fromRanges,omitempty"`
}

type compactHierarchyLocation struct {
	File string `json:"file,omitempty"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
}

func groupLocationsByFile(ctx context.Context, items []protocol.LocationResult, total int) protocol.GroupedLocationResult {
	grouped := format.GroupLocationsByFile(items, total)
	if len(grouped.Data) == 0 {
		return grouped
	}
	relative := make(map[string][]protocol.CompactLocation, len(grouped.Data))
	for file, locations := range grouped.Data {
		relative[compactToolFilePath(ctx, file)] = locations
	}
	grouped.Data = relative
	return grouped
}

func compactCallHierarchyResults(ctx context.Context, items []protocol.CallHierarchyResult) []compactHierarchyResult {
	out := make([]compactHierarchyResult, 0, len(items))
	for i := range items {
		row := compactHierarchyResult{Item: compactCallHierarchyItem(ctx, items[i].Item)}
		row.Incoming = compactIncomingCalls(ctx, items[i].Incoming)
		row.Outgoing = compactOutgoingCalls(ctx, items[i].Item, items[i].Outgoing)
		out = append(out, row)
	}
	return out
}

func compactIncomingCalls(ctx context.Context, calls []protocol.CallHierarchyIncomingCall) []compactHierarchyIncomingCall {
	out := make([]compactHierarchyIncomingCall, 0, len(calls))
	for i := range calls {
		from := compactCallHierarchyItem(ctx, calls[i].From)
		out = append(out, compactHierarchyIncomingCall{
			From:       from,
			FromRanges: compactHierarchyRanges(ctx, calls[i].From.URI, calls[i].FromRanges),
		})
	}
	return out
}

func compactOutgoingCalls(
	ctx context.Context,
	source protocol.CallHierarchyItem,
	calls []protocol.CallHierarchyOutgoingCall,
) []compactHierarchyOutgoingCall {
	out := make([]compactHierarchyOutgoingCall, 0, len(calls))
	for i := range calls {
		out = append(out, compactHierarchyOutgoingCall{
			To:         compactCallHierarchyItem(ctx, calls[i].To),
			FromRanges: compactHierarchyRanges(ctx, source.URI, calls[i].FromRanges),
		})
	}
	return out
}

func compactCallHierarchyItem(ctx context.Context, item protocol.CallHierarchyItem) compactHierarchyItem {
	return compactHierarchyItem{
		Name:   item.Name,
		Kind:   item.Kind,
		File:   compactToolFilePath(ctx, item.URI),
		Line:   format.FromLSP(item.SelectionRange.Start.Line),
		Col:    format.FromLSP(item.SelectionRange.Start.Character),
		Detail: item.Detail,
	}
}

func compactHierarchyRanges(ctx context.Context, uri string, ranges []protocol.Range) []compactHierarchyLocation {
	out := make([]compactHierarchyLocation, 0, len(ranges))
	for i := range ranges {
		out = append(out, compactHierarchyLocation{
			File: compactToolFilePath(ctx, uri),
			Line: format.FromLSP(ranges[i].Start.Line),
			Col:  format.FromLSP(ranges[i].Start.Character),
		})
	}
	return out
}

func compactToolFilePath(ctx context.Context, raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	file := format.URIToPath(raw)
	if strings.TrimSpace(file) == "" {
		return ""
	}
	scope, ok := common.ToolScopeFromContext(ctx)
	if !ok || strings.TrimSpace(scope.CWD) == "" {
		return filepath.ToSlash(filepath.Clean(file))
	}
	return relativeToScope(scope.CWD, file)
}

// relativeToScope 把相对处理为作用域。
func relativeToScope(root string, file string) string {
	cleanFile := canonicalClean(file)
	if !filepath.IsAbs(cleanFile) {
		return filepath.ToSlash(filepath.Clean(cleanFile))
	}
	rel, err := filepath.Rel(canonicalClean(root), cleanFile)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(cleanFile)
	}
	return filepath.ToSlash(rel)
}

func canonicalClean(path string) string {
	cleaned := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		return resolved
	}
	return cleaned
}

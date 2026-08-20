package tools

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/format"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/lspplatform"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
)

const hierarchyContentMaxBytes = 4 * 1024

type compactHierarchyItem struct {
	Name   string `json:"name"`
	Kind   int    `json:"kind,omitempty"`
	File   string `json:"file,omitempty"`
	Line   int    `json:"line"`
	Col    int    `json:"col"`
	Detail string `json:"detail,omitempty"`
}

type compactHierarchyLocation struct {
	File string `json:"file,omitempty"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
}

type hierarchyEdgeRow struct {
	Direction    string
	Item         compactHierarchyItem
	Sites        []compactHierarchyLocation
	SitesTotal   int
	HasCallSites bool
}

type hierarchyEdgeListResponse struct {
	Rows  []hierarchyEdgeRow
	Total int
	Unit  string
	Hint  string
}

// ToPlainText 渲染扁平 hierarchy edge，并按最终文本硬预算同步裁剪 showing。
func (response hierarchyEdgeListResponse) ToPlainText() string {
	for {
		text := response.renderPlainText()
		if len([]byte(text)) <= hierarchyContentMaxBytes || len(response.Rows) == 0 {
			return text
		}
		response.Rows = response.Rows[:len(response.Rows)-1]
	}
}

func (response hierarchyEdgeListResponse) renderPlainText() string {
	lines := []string{lineprotocol.HeaderLine(response.Total, len(response.Rows), len(response.Rows) < response.Total, response.Unit)}
	for _, row := range response.Rows {
		lines = append(lines, hierarchyEdgeRecord(row))
	}
	if hint := strings.TrimSpace(response.Hint); hint != "" {
		lines = append(lines, lineprotocol.TextRecord("HINT", hint))
	}
	return strings.Join(lines, "\n")
}

func hierarchyEdgeRecord(row hierarchyEdgeRow) string {
	fields := []lineprotocol.Field{
		{Key: "direction", Value: row.Direction},
		{Key: "name", Value: row.Item.Name},
		{Key: "kind", Value: strconv.Itoa(row.Item.Kind)},
		{Key: "file", Value: row.Item.File},
		{Key: "line", Value: strconv.Itoa(row.Item.Line)},
		{Key: "col", Value: strconv.Itoa(row.Item.Col)},
	}
	if row.HasCallSites {
		fields = append(fields,
			lineprotocol.Field{Key: "sites_total", Value: strconv.Itoa(row.SitesTotal)},
			lineprotocol.Field{Key: "sites_showing", Value: strconv.Itoa(len(row.Sites))},
			lineprotocol.Field{Key: "sites_truncated", Value: strconv.Itoa(boolToInt(len(row.Sites) < row.SitesTotal))},
		)
		if len(row.Sites) > 0 {
			fields = append(fields,
				lineprotocol.Field{Key: "site_file", Value: row.Sites[0].File},
				lineprotocol.Field{Key: "site_line", Value: strconv.Itoa(row.Sites[0].Line)},
				lineprotocol.Field{Key: "site_col", Value: strconv.Itoa(row.Sites[0].Col)},
			)
		}
	}
	return lineprotocol.FieldsRecord("ROW", fields...)
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func compactCallHierarchyEdges(ctx context.Context, items []protocol.CallHierarchyResult) ([]hierarchyEdgeRow, []hierarchyEdgeRow) {
	var incoming, outgoing []hierarchyEdgeRow
	for _, result := range items {
		for _, edge := range result.Incoming {
			incoming = append(incoming, compactCallEdge(ctx, "incoming", edge.From, edge.From.URI, edge.FromRanges))
		}
		for _, edge := range result.Outgoing {
			outgoing = append(outgoing, compactCallEdge(ctx, "outgoing", edge.To, result.Item.URI, edge.FromRanges))
		}
	}
	return incoming, outgoing
}

func compactCallEdge(ctx context.Context, direction string, item protocol.CallHierarchyItem, siteURI string, ranges []protocol.Range) hierarchyEdgeRow {
	shown := ranges
	if len(shown) > 1 {
		shown = shown[:1]
	}
	return hierarchyEdgeRow{
		Direction: direction, Item: compactCallHierarchyItem(ctx, item),
		Sites: compactHierarchyRanges(ctx, siteURI, shown), SitesTotal: len(ranges), HasCallSites: true,
	}
}

func compactTypeHierarchyEdges(ctx context.Context, items []protocol.TypeHierarchyResult) ([]hierarchyEdgeRow, []hierarchyEdgeRow) {
	var supertypes, subtypes []hierarchyEdgeRow
	for _, result := range items {
		for _, item := range result.Supertypes {
			supertypes = append(supertypes, hierarchyEdgeRow{Direction: "supertype", Item: compactTypeHierarchyItem(ctx, item)})
		}
		for _, item := range result.Subtypes {
			subtypes = append(subtypes, hierarchyEdgeRow{Direction: "subtype", Item: compactTypeHierarchyItem(ctx, item)})
		}
	}
	return supertypes, subtypes
}

func compactTypeHierarchyItem(ctx context.Context, item protocol.TypeHierarchyItem) compactHierarchyItem {
	return compactHierarchyItem{
		Name: item.Name, Kind: item.Kind, File: compactToolFilePath(ctx, item.URI),
		Line: format.FromLSP(item.SelectionRange.Start.Line), Col: format.FromLSP(item.SelectionRange.Start.Character), Detail: item.Detail,
	}
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
	if resolved, err := lspplatform.CanonicalExistingPath(cleaned); err == nil {
		return resolved
	}
	return cleaned
}

package format

import (
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/lsp/protocol"
)

const (
	VerbosityCompact = "compact"
	VerbosityFull    = "full"

	lspReferencesCompactLimit      = 30
	lspCompletionCompactLimit      = 20
	lspWorkspaceSymbolCompactLimit = 20
)

// CompactList describes a format API type.
type CompactList[T any] struct {
	Data      []T    `json:"data"`
	Total     int    `json:"total"`
	Showing   int    `json:"showing"`
	Truncated bool   `json:"truncated,omitempty"`
	Hint      string `json:"hint,omitempty"`
}

// CompactCompletionItem describes a format API type.
type CompactCompletionItem struct {
	Label  string `json:"label"`
	Kind   int    `json:"kind,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// CompactWorkspaceSymbol describes a format API type.
type CompactWorkspaceSymbol struct {
	Name      string `json:"name"`
	Kind      int    `json:"kind,omitempty"`
	File      string `json:"file,omitempty"`
	Line      int    `json:"line"`
	Col       int    `json:"col"`
	Container string `json:"container,omitempty"`
}

// NormalizeVerbosity 规范化详细度。
func NormalizeVerbosity(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case VerbosityFull:
		return VerbosityFull
	default:
		return VerbosityCompact
	}
}

// ResolveResultLimit 解析结果limit。
func ResolveResultLimit(requested int, verbosity string, compactDefault int) int {
	if requested > protocol.XRefResultLimit {
		requested = protocol.XRefResultLimit
	}
	if requested > 0 {
		return requested
	}
	if NormalizeVerbosity(verbosity) == VerbosityFull {
		return protocol.XRefResultLimit
	}
	return compactDefault
}

// ReferencesLimit 返回引用limit。
func ReferencesLimit(requested int, verbosity string) int {
	return ResolveResultLimit(requested, verbosity, lspReferencesCompactLimit)
}

// CompletionLimit 返回补全limit。
func CompletionLimit(requested int, verbosity string) int {
	return ResolveResultLimit(requested, verbosity, lspCompletionCompactLimit)
}

// WorkspaceSymbolLimit 返回工作区符号limit。
func WorkspaceSymbolLimit(requested int, verbosity string) int {
	return ResolveResultLimit(requested, verbosity, lspWorkspaceSymbolCompactLimit)
}

// NewCompactList 创建紧凑列表list。
func NewCompactList[T any](items []T, total int, hints ...string) CompactList[T] {
	if total < len(items) {
		total = len(items)
	}
	truncated := total > len(items)
	hint := ""
	if truncated {
		hint = "next: increase max_results or narrow the request"
		for _, candidate := range hints {
			if value := strings.TrimSpace(candidate); value != "" {
				hint = value
				break
			}
		}
	}
	return CompactList[T]{
		Data:      items,
		Total:     total,
		Showing:   len(items),
		Truncated: truncated,
		Hint:      hint,
	}
}

// CompactCompletionItems 转换紧凑列表补全items用于展示。
func CompactCompletionItems(items []protocol.CompletionItem) []CompactCompletionItem {
	out := make([]CompactCompletionItem, 0, len(items))
	for i := range items {
		out = append(out, CompactCompletionItem{
			Label:  items[i].Label,
			Kind:   items[i].Kind,
			Detail: items[i].Detail,
		})
	}
	return out
}

// CompactWorkspaceSymbols 转换紧凑列表工作区符号用于展示。
func CompactWorkspaceSymbols(items []protocol.WorkspaceSymbolResult) []CompactWorkspaceSymbol {
	out := make([]CompactWorkspaceSymbol, 0, len(items))
	for i := range items {
		if si := items[i].SymbolInformation; si != nil {
			out = append(out, CompactWorkspaceSymbol{
				Name:      si.Name,
				Kind:      int(si.Kind),
				File:      URIToPath(si.Location.URI),
				Line:      FromLSP(si.Location.Range.Start.Line),
				Col:       FromLSP(si.Location.Range.Start.Character),
				Container: si.ContainerName,
			})
			continue
		}
		if ws := items[i].WorkspaceSymbol; ws != nil {
			row := CompactWorkspaceSymbol{
				Name:      ws.Name,
				Kind:      ws.Kind,
				Container: ws.ContainerName,
			}
			if file, line, column, ok := LocationFromAny(ws.Location); ok {
				row.File = file
				row.Line = line
				row.Col = column
			}
			out = append(out, row)
		}
	}
	return out
}

// LocationFromAny 从任意值处理位置。
func LocationFromAny(location any) (file string, line int, column int, ok bool) {
	switch value := location.(type) {
	case nil:
		return "", 0, 0, false
	case protocol.Location:
		return locationFromProtocol(value)
	case *protocol.Location:
		return locationFromProtocolPtr(value)
	case protocol.WorkspaceSymbolLocation:
		return locationFromWorkspaceSymbol(value)
	case *protocol.WorkspaceSymbolLocation:
		return locationFromWorkspaceSymbolPtr(value)
	case map[string]any:
		return locationFromMap(value)
	default:
		return "", 0, 0, false
	}
}

func locationFromProtocol(value protocol.Location) (string, int, int, bool) {
	return URIToPath(value.URI), FromLSP(value.Range.Start.Line), FromLSP(value.Range.Start.Character), true
}

func locationFromProtocolPtr(value *protocol.Location) (string, int, int, bool) {
	if value == nil {
		return "", 0, 0, false
	}
	return locationFromProtocol(*value)
}

func locationFromWorkspaceSymbol(value protocol.WorkspaceSymbolLocation) (string, int, int, bool) {
	return URIToPath(value.URI), 0, 0, strings.TrimSpace(value.URI) != ""
}

func locationFromWorkspaceSymbolPtr(value *protocol.WorkspaceSymbolLocation) (string, int, int, bool) {
	if value == nil {
		return "", 0, 0, false
	}
	return locationFromWorkspaceSymbol(*value)
}

func locationFromMap(value map[string]any) (string, int, int, bool) {
	uri, _ := value["uri"].(string)
	if strings.TrimSpace(uri) == "" {
		return "", 0, 0, false
	}
	line, column := mapStartPosition(value)
	return URIToPath(uri), FromLSP(line), FromLSP(column), true
}

func mapStartPosition(value map[string]any) (int, int) {
	rangeMap, ok := value["range"].(map[string]any)
	if !ok {
		return 0, 0
	}
	startMap, ok := rangeMap["start"].(map[string]any)
	if !ok {
		return 0, 0
	}
	return intFromAny(startMap["line"]), intFromAny(startMap["character"])
}

// GroupLocationsByFile 按文件处理group位置。
func GroupLocationsByFile(items []protocol.LocationResult, total int) protocol.GroupedLocationResult {
	if total < len(items) {
		total = len(items)
	}
	grouped := protocol.GroupedLocationResult{
		Data:      make(map[string][]protocol.CompactLocation),
		Total:     total,
		Showing:   len(items),
		Truncated: total > len(items),
	}
	lastRange := make(map[string][2]int)
	for i := range items {
		loc := items[i].PrimaryLocation()
		if loc == nil {
			continue
		}
		file := URIToPath(loc.URI)
		row := protocol.CompactLocation{
			Line: FromLSP(loc.Range.Start.Line),
			Col:  FromLSP(loc.Range.Start.Character),
		}
		if items[i].HasFuncRange() {
			cur := [2]int{items[i].FuncStart, items[i].FuncEnd}
			if prev, seen := lastRange[file]; !seen || prev != cur {
				row.FuncStart = items[i].FuncStart
				row.FuncEnd = items[i].FuncEnd
				lastRange[file] = cur
				grouped.Hint = "next: file action=read_file pos=<file>:<func_start> limit=<func_end-func_start+1>"
			}
		}
		grouped.Data[file] = append(grouped.Data[file], row)
	}
	if grouped.Truncated && grouped.Hint == "" {
		grouped.Hint = "next: increase max_results or narrow the target position"
	}
	return grouped
}

// intFromAny 从任意值处理int。
func intFromAny(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int8:
		return int(v)
	case int16:
		return int(v)
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float32:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

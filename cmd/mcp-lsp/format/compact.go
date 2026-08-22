package format

import (
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

const (
	lspReferencesDefaultLimit      = 30
	lspCompletionDefaultLimit      = 20
	lspWorkspaceSymbolDefaultLimit = 20
	lspDocumentSymbolDefaultLimit  = 20
)

// CompactList 是紧凑输出的通用 wire 结构。
// Total/Showing/Truncated 告诉调用方是否需要收窄查询或提升 max_results。
type CompactList[T any] struct {
	Data      []T    `json:"data"`
	Total     int    `json:"total"`
	Showing   int    `json:"showing"`
	Truncated bool   `json:"truncated,omitempty"`
	Hint      string `json:"hint,omitempty"`
}

// CompactCompletionItem 是 completion 工具紧凑模式的单项结果。
type CompactCompletionItem struct {
	Label  string `json:"label"`
	Kind   int    `json:"kind,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// CompactWorkspaceSymbol 是 workspace_symbol 紧凑模式的跨 LSP server 统一行格式。
type CompactWorkspaceSymbol struct {
	Name      string `json:"name"`
	Kind      int    `json:"kind,omitempty"`
	File      string `json:"file,omitempty"`
	Line      int    `json:"line"`
	Col       int    `json:"col"`
	Container string `json:"container,omitempty"`
}

// ResolveResultLimit 计算工具结果上限。
// 显式请求受协议硬上限约束，未请求时使用对应工具的默认值。
func ResolveResultLimit(requested int, defaultLimit int) int {
	if requested > protocol.XRefResultLimit {
		requested = protocol.XRefResultLimit
	}
	if requested > 0 {
		return requested
	}
	return defaultLimit
}

// ReferencesLimit 返回 references 工具的结果上限。
func ReferencesLimit(requested int) int {
	return ResolveResultLimit(requested, lspReferencesDefaultLimit)
}

// CompletionLimit 返回 completion 工具的结果上限。
func CompletionLimit(requested int) int {
	return ResolveResultLimit(requested, lspCompletionDefaultLimit)
}

// WorkspaceSymbolLimit 返回 workspace_symbol 工具的结果上限。
func WorkspaceSymbolLimit(requested int) int {
	return ResolveResultLimit(requested, lspWorkspaceSymbolDefaultLimit)
}

// DocumentSymbolLimit 返回 document_symbol 工具的结果上限。
func DocumentSymbolLimit(requested int) int {
	return ResolveResultLimit(requested, lspDocumentSymbolDefaultLimit)
}

// NewCompactList 构造带截断提示的紧凑列表。
// total 小于当前 items 时会被修正，避免上游统计缺失造成展示不一致。
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

// CompactCompletionItems 提取补全结果中适合紧凑展示的字段。
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

// CompactWorkspaceSymbols 将两种 LSP workspace symbol 返回形态压成统一行。
// 位置字段会被转成可读路径和 1-based 坐标。
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

// LocationFromAny 从 LSP union/map 形态中提取文件和起始坐标。
// 不识别或缺少 URI 时返回 ok=false，让调用方跳过不完整位置。
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

// GroupLocationsByFile 将位置结果按文件分组并压缩重复函数范围提示。
// 它保留 Total/Truncated，方便调用方知道是否需要收窄查询。
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
				grouped.Hint = "next: inspect the reported function range with native cat/head tools"
			}
		}
		grouped.Data[file] = append(grouped.Data[file], row)
	}
	if grouped.Truncated && grouped.Hint == "" {
		grouped.Hint = "next: increase max_results or narrow the target position"
	}
	return grouped
}

// intFromAny 从 JSON 解码后的数值类型中读取 int。
// 不支持的类型返回 0，调用方只把它用于可选展示坐标。
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

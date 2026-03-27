package format

import (
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/protocol"
)

const (
	VerbosityCompact = "compact"
	VerbosityFull    = "full"

	lspReferencesCompactLimit      = 30
	lspCompletionCompactLimit      = 20
	lspWorkspaceSymbolCompactLimit = 20
)

type CompactList[T any] struct {
	Data    []T `json:"data"`
	Total   int `json:"total"`
	Showing int `json:"showing"`
}

type CompactCompletionItem struct {
	Label  string `json:"label"`
	Kind   int    `json:"kind,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type CompactWorkspaceSymbol struct {
	Name      string `json:"name"`
	Kind      int    `json:"kind,omitempty"`
	File      string `json:"file,omitempty"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	Container string `json:"container,omitempty"`
}

func NormalizeVerbosity(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case VerbosityFull:
		return VerbosityFull
	default:
		return VerbosityCompact
	}
}

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

func ReferencesLimit(requested int, verbosity string) int {
	return ResolveResultLimit(requested, verbosity, lspReferencesCompactLimit)
}

func CompletionLimit(requested int, verbosity string) int {
	return ResolveResultLimit(requested, verbosity, lspCompletionCompactLimit)
}

func WorkspaceSymbolLimit(requested int, verbosity string) int {
	return ResolveResultLimit(requested, verbosity, lspWorkspaceSymbolCompactLimit)
}

func NewCompactList[T any](items []T, total int) CompactList[T] {
	if total < len(items) {
		total = len(items)
	}
	return CompactList[T]{
		Data:    items,
		Total:   total,
		Showing: len(items),
	}
}

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

func CompactWorkspaceSymbols(items []protocol.WorkspaceSymbolResult) []CompactWorkspaceSymbol {
	out := make([]CompactWorkspaceSymbol, 0, len(items))
	for i := range items {
		if si := items[i].SymbolInformation; si != nil {
			out = append(out, CompactWorkspaceSymbol{
				Name:      si.Name,
				Kind:      int(si.Kind),
				File:      URIToPath(si.Location.URI),
				Line:      FromLSP(si.Location.Range.Start.Line),
				Column:    FromLSP(si.Location.Range.Start.Character),
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
				row.Column = column
			}
			out = append(out, row)
		}
	}
	return out
}

func LocationFromAny(location any) (file string, line int, column int, ok bool) {
	switch value := location.(type) {
	case nil:
		return "", 0, 0, false
	case protocol.Location:
		return URIToPath(value.URI), FromLSP(value.Range.Start.Line), FromLSP(value.Range.Start.Character), true
	case *protocol.Location:
		if value == nil {
			return "", 0, 0, false
		}
		return URIToPath(value.URI), FromLSP(value.Range.Start.Line), FromLSP(value.Range.Start.Character), true
	case protocol.WorkspaceSymbolLocation:
		return URIToPath(value.URI), 0, 0, strings.TrimSpace(value.URI) != ""
	case *protocol.WorkspaceSymbolLocation:
		if value == nil {
			return "", 0, 0, false
		}
		return URIToPath(value.URI), 0, 0, strings.TrimSpace(value.URI) != ""
	case map[string]any:
		uri, _ := value["uri"].(string)
		if strings.TrimSpace(uri) == "" {
			return "", 0, 0, false
		}
		line, column = 0, 0
		if rangeMap, ok := value["range"].(map[string]any); ok {
			if startMap, ok := rangeMap["start"].(map[string]any); ok {
				line = intFromAny(startMap["line"])
				column = intFromAny(startMap["character"])
			}
		}
		return URIToPath(uri), FromLSP(line), FromLSP(column), true
	default:
		return "", 0, 0, false
	}
}

func GroupLocationsByFile(items []protocol.LocationResult, total int) protocol.GroupedLocationResult {
	if total < len(items) {
		total = len(items)
	}
	grouped := protocol.GroupedLocationResult{
		Files:   make(map[string][]protocol.CompactLocation),
		Total:   total,
		Showing: len(items),
	}
	lastRange := make(map[string][2]int)
	for i := range items {
		loc := items[i].PrimaryLocation()
		if loc == nil {
			continue
		}
		file := URIToPath(loc.URI)
		row := protocol.CompactLocation{
			Line:   FromLSP(loc.Range.Start.Line),
			Column: FromLSP(loc.Range.Start.Character),
		}
		if items[i].HasFuncRange() {
			cur := [2]int{items[i].FuncStart, items[i].FuncEnd}
			if prev, seen := lastRange[file]; !seen || prev != cur {
				row.FuncStart = items[i].FuncStart
				row.FuncEnd = items[i].FuncEnd
				lastRange[file] = cur
				grouped.Hint = "step 2: use the returned func_start/func_end to read that function range, e.g. read_file(offset=func_start, limit=func_end-func_start+1)"
			}
		}
		grouped.Files[file] = append(grouped.Files[file], row)
	}
	return grouped
}

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

package format

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
)

// RenderJSON 渲染JSON。
func RenderJSON(value any) (string, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return "", fmt.Errorf("render json: %w", err)
	}
	return strings.TrimRight(buffer.String(), "\n"), nil
}

// RenderLineNumberedText 渲染行带行号文本。
func RenderLineNumberedText(content string, startLine int) string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return ""
	}
	if startLine <= 0 {
		startLine = 1
	}
	width := len(strconv.Itoa(startLine + len(lines) - 1))
	var builder strings.Builder
	for i := range lines {
		fmt.Fprintf(&builder, "%*d: %s", width, startLine+i, lines[i])
		if i+1 < len(lines) {
			builder.WriteByte('\n')
		}
	}
	return builder.String()
}

// RenderGroupedLocations 渲染分组位置。
func RenderGroupedLocations(result protocol.GroupedLocationResult) string {
	if len(result.Data) == 0 {
		return ""
	}
	files := make([]string, 0, len(result.Data))
	for file := range result.Data {
		files = append(files, file)
	}
	sort.Strings(files)
	var builder strings.Builder
	for i := range files {
		file := files[i]
		for _, row := range result.Data[file] {
			builder.WriteString(file)
			builder.WriteByte(':')
			builder.WriteString(strconv.Itoa(row.Line))
			builder.WriteByte(':')
			builder.WriteString(strconv.Itoa(row.Col))
			if row.FuncStart > 0 && row.FuncEnd >= row.FuncStart {
				builder.WriteString(" [func L")
				builder.WriteString(strconv.Itoa(row.FuncStart))
				builder.WriteString("-L")
				builder.WriteString(strconv.Itoa(row.FuncEnd))
				builder.WriteByte(']')
			}
			builder.WriteByte('\n')
		}
		if i+1 < len(files) {
			builder.WriteByte('\n')
		}
	}
	return strings.TrimRight(builder.String(), "\n")
}

// RenderCompactList 渲染紧凑列表list。
func RenderCompactList[T any](list CompactList[T]) (string, error) {
	return RenderJSON(list)
}

// NormalizeForDisplay 为显示规范化LSP。
func NormalizeForDisplay[T any](value T) T {
	if normalizer, ok := displayNormalizers[reflect.TypeOf(value)]; ok {
		return normalizer(value).(T)
	}
	return value
}

// workspaceSymbolLocationAny 转换工作区符号位置任意值用于展示。
func workspaceSymbolLocationAny(location any) any {
	switch value := location.(type) {
	case nil:
		return nil
	case protocol.Location:
		return Location(value)
	case *protocol.Location:
		return LocationPtr(value)
	case protocol.WorkspaceSymbolLocation:
		value.URI = URIToPath(value.URI)
		return value
	case *protocol.WorkspaceSymbolLocation:
		if value == nil {
			return nil
		}
		converted := *value
		converted.URI = URIToPath(converted.URI)
		return &converted
	case map[string]any:
		out := cloneAnyMap(value)
		if uri, ok := value["uri"].(string); ok {
			out["uri"] = URIToPath(uri)
		}
		if rangeMap, ok := value["range"].(map[string]any); ok {
			out["range"] = rangeMapForDisplay(rangeMap)
		}
		return out
	default:
		return location
	}
}

func cloneAnyMap(value map[string]any) map[string]any {
	out := make(map[string]any, len(value))
	maps.Copy(out, value)
	return out
}

func rangeMapForDisplay(value map[string]any) map[string]any {
	out := cloneAnyMap(value)
	if start, ok := value["start"].(map[string]any); ok {
		out["start"] = positionMapForDisplay(start)
	}
	if end, ok := value["end"].(map[string]any); ok {
		out["end"] = positionMapForDisplay(end)
	}
	return out
}

// positionMapForDisplay 为显示处理位置map。
func positionMapForDisplay(value map[string]any) map[string]any {
	out := cloneAnyMap(value)
	for _, key := range []string{"line", "character", "column"} {
		if raw, ok := value[key]; ok {
			if converted, ok := anyCoordinate(raw); ok {
				out[key] = converted
			}
		}
	}
	return out
}

func anyCoordinate(value any) (any, bool) {
	if result, ok := convertIntegerCoordinate(value); ok {
		return result, true
	}
	return convertFloatCoordinate(value)
}

func convertIntegerCoordinate(value any) (any, bool) {
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		converted := FromLSP(int(rv.Int()))
		return reflect.ValueOf(converted).Convert(rv.Type()).Interface(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		converted := FromLSP(int(rv.Uint()))
		return reflect.ValueOf(converted).Convert(rv.Type()).Interface(), true
	default:
		return nil, false
	}
}

func convertFloatCoordinate(value any) (any, bool) {
	switch v := value.(type) {
	case float32:
		if v < 0 {
			return v, true
		}
		return v + 1, true
	case float64:
		if v < 0 {
			return v, true
		}
		return v + 1, true
	default:
		return nil, false
	}
}

func displayTypeOf[T any]() reflect.Type {
	var zero T
	return reflect.TypeOf(zero)
}

var displayNormalizers = map[reflect.Type]func(any) any{
	displayTypeOf[protocol.Location]():                func(value any) any { return Location(value.(protocol.Location)) },
	displayTypeOf[*protocol.Location]():               func(value any) any { return LocationPtr(value.(*protocol.Location)) },
	displayTypeOf[protocol.HoverResult]():             func(value any) any { return HoverResult(value.(protocol.HoverResult)) },
	displayTypeOf[*protocol.HoverResult]():            func(value any) any { result := HoverResult(*value.(*protocol.HoverResult)); return &result },
	displayTypeOf[[]protocol.LocationResult]():        func(value any) any { return LocationResults(value.([]protocol.LocationResult)) },
	displayTypeOf[[]protocol.DocumentSymbol]():        func(value any) any { return DocumentSymbols(value.([]protocol.DocumentSymbol)) },
	displayTypeOf[*protocol.WorkspaceEdit]():          func(value any) any { return WorkspaceEdit(value.(*protocol.WorkspaceEdit)) },
	displayTypeOf[[]protocol.TextEdit]():              func(value any) any { return TextEdits(value.([]protocol.TextEdit)) },
	displayTypeOf[[]protocol.Diagnostic]():            func(value any) any { return Diagnostics(value.([]protocol.Diagnostic)) },
	displayTypeOf[[]protocol.CodeActionResult]():      func(value any) any { return CodeActionResults(value.([]protocol.CodeActionResult)) },
	displayTypeOf[[]protocol.WorkspaceSymbolResult](): func(value any) any { return WorkspaceSymbolResults(value.([]protocol.WorkspaceSymbolResult)) },
	displayTypeOf[[]protocol.CallHierarchyResult]():   func(value any) any { return CallHierarchyResults(value.([]protocol.CallHierarchyResult)) },
	displayTypeOf[[]protocol.TypeHierarchyResult]():   func(value any) any { return TypeHierarchyResults(value.([]protocol.TypeHierarchyResult)) },
	displayTypeOf[*protocol.SemanticTokensResult]():   func(value any) any { return SemanticTokensResult(value.(*protocol.SemanticTokensResult)) },
	displayTypeOf[protocol.FoldingRange]():            func(value any) any { return FoldingRange(value.(protocol.FoldingRange)) },
	displayTypeOf[[]protocol.FoldingRange]():          func(value any) any { return FoldingRanges(value.([]protocol.FoldingRange)) },
}

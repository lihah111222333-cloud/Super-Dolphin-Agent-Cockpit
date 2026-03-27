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

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/protocol"
)

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

func RenderTable(headers []string, rows [][]string) string {
	widths := columnWidths(headers, rows)
	var builder strings.Builder
	writeTableRow(&builder, headers, widths)
	writeTableSeparator(&builder, widths)
	for i := range rows {
		writeTableRow(&builder, rows[i], widths)
	}
	return builder.String()
}

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

func RenderGroupedLocations(result protocol.GroupedLocationResult) string {
	if len(result.Files) == 0 {
		return ""
	}
	files := make([]string, 0, len(result.Files))
	for file := range result.Files {
		files = append(files, file)
	}
	sort.Strings(files)
	var builder strings.Builder
	for i := range files {
		file := files[i]
		builder.WriteString(file)
		builder.WriteByte('\n')
		for _, row := range result.Files[file] {
			builder.WriteString("  ")
			builder.WriteString(strconv.Itoa(row.Line))
			builder.WriteByte(':')
			builder.WriteString(strconv.Itoa(row.Column))
			if row.FuncStart > 0 && row.FuncEnd >= row.FuncStart {
				builder.WriteString("  func ")
				builder.WriteString(strconv.Itoa(row.FuncStart))
				builder.WriteByte('-')
				builder.WriteString(strconv.Itoa(row.FuncEnd))
			}
			builder.WriteByte('\n')
		}
		if i+1 < len(files) {
			builder.WriteByte('\n')
		}
	}
	return strings.TrimRight(builder.String(), "\n")
}

func RenderCompactList[T any](list CompactList[T]) (string, error) {
	return RenderJSON(list)
}

func NormalizeForDisplay[T any](value T) T {
	if normalizer, ok := displayNormalizers[reflect.TypeOf(value)]; ok {
		return normalizer(value).(T)
	}
	return value
}

func columnWidths(headers []string, rows [][]string) []int {
	widths := make([]int, len(headers))
	for i := range headers {
		widths[i] = len(headers[i])
	}
	for _, row := range rows {
		for i := range row {
			if i < len(widths) && len(row[i]) > widths[i] {
				widths[i] = len(row[i])
			}
		}
	}
	return widths
}

func writeTableRow(builder *strings.Builder, row []string, widths []int) {
	for i := range widths {
		if i > 0 {
			builder.WriteString(" | ")
		}
		cell := ""
		if i < len(row) {
			cell = row[i]
		}
		builder.WriteString(cell)
		if pad := widths[i] - len(cell); pad > 0 {
			builder.WriteString(strings.Repeat(" ", pad))
		}
	}
	builder.WriteByte('\n')
}

func writeTableSeparator(builder *strings.Builder, widths []int) {
	for i := range widths {
		if i > 0 {
			builder.WriteString("-+-")
		}
		builder.WriteString(strings.Repeat("-", widths[i]))
	}
	builder.WriteByte('\n')
}

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
	switch v := value.(type) {
	case int:
		return FromLSP(v), true
	case int8:
		return int8(FromLSP(int(v))), true
	case int16:
		return int16(FromLSP(int(v))), true
	case int32:
		return int32(FromLSP(int(v))), true
	case int64:
		return int64(FromLSP(int(v))), true
	case uint:
		return uint(FromLSP(int(v))), true
	case uint8:
		return uint8(FromLSP(int(v))), true
	case uint16:
		return uint16(FromLSP(int(v))), true
	case uint32:
		return uint32(FromLSP(int(v))), true
	case uint64:
		return uint64(FromLSP(int(v))), true
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

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

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

// RenderJSON 将工具结构化结果渲染成稳定缩进 JSON。
// 关闭 HTML escape，避免路径、代码片段和泛型符号在展示层被额外转义。
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

// RenderLineNumberedText 为文本窗口补 1-based 行号。
// startLine 非法时从 1 开始，保证 read_file 回显始终可定位。
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

// RenderGroupedLocations 将按文件分组的位置结果渲染成纯文本。
// 函数范围只在结果携带时展示，用于提示下一步精读范围。
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

// RenderCompactList 渲染紧凑列表 wire 结构。
func RenderCompactList[T any](list CompactList[T]) (string, error) {
	return RenderJSON(list)
}

// NormalizeForDisplay 根据结果类型套用展示层 normalizer。
// 未登记类型原样返回，避免工具结果被意外改写。
func NormalizeForDisplay[T any](value T) T {
	dispatch := newDisplayNormalizerDispatch()
	if normalized, ok := dispatch.normalize(value); ok {
		return normalized.(T)
	}
	return value
}

// workspaceSymbolLocationAny 规整 workspace symbol 的 location union。
// 它兼容结构体、指针和 map 形态，统一把 URI 和坐标转成展示格式。
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

// positionMapForDisplay 转换 map 形态 position 的 line/character/column 字段。
// 非数值字段会被保留，避免破坏未知 LSP 扩展字段。
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

// displayNormalizerDispatch 是展示归一化的 owner-local、不可变分派器。
// 每次构造都得到独立的注册表，不暴露可变注册表，也不依赖包级 singleton。
type displayNormalizerDispatch struct {
	normalizers map[reflect.Type]func(any) any
}

// newDisplayNormalizerDispatch 构造展示归一化分派器。
func newDisplayNormalizerDispatch() displayNormalizerDispatch {
	return displayNormalizerDispatch{normalizers: map[reflect.Type]func(any) any{
		reflect.TypeOf(protocol.Location{}):                   func(value any) any { return Location(value.(protocol.Location)) },
		reflect.TypeOf((*protocol.Location)(nil)):             func(value any) any { return LocationPtr(value.(*protocol.Location)) },
		reflect.TypeOf(protocol.HoverResult{}):                func(value any) any { return HoverResult(value.(protocol.HoverResult)) },
		reflect.TypeOf((*protocol.HoverResult)(nil)):          normalizeHoverResultPtr,
		reflect.TypeOf([]protocol.LocationResult{}):           func(value any) any { return LocationResults(value.([]protocol.LocationResult)) },
		reflect.TypeOf([]protocol.DocumentSymbol{}):           func(value any) any { return DocumentSymbols(value.([]protocol.DocumentSymbol)) },
		reflect.TypeOf((*protocol.WorkspaceEdit)(nil)):        func(value any) any { return WorkspaceEdit(value.(*protocol.WorkspaceEdit)) },
		reflect.TypeOf([]protocol.TextEdit{}):                 func(value any) any { return TextEdits(value.([]protocol.TextEdit)) },
		reflect.TypeOf([]protocol.Diagnostic{}):               func(value any) any { return Diagnostics(value.([]protocol.Diagnostic)) },
		reflect.TypeOf([]protocol.CodeActionResult{}):         func(value any) any { return CodeActionResults(value.([]protocol.CodeActionResult)) },
		reflect.TypeOf([]protocol.WorkspaceSymbolResult{}):    func(value any) any { return WorkspaceSymbolResults(value.([]protocol.WorkspaceSymbolResult)) },
		reflect.TypeOf([]protocol.CallHierarchyResult{}):      func(value any) any { return CallHierarchyResults(value.([]protocol.CallHierarchyResult)) },
		reflect.TypeOf([]protocol.TypeHierarchyResult{}):      func(value any) any { return TypeHierarchyResults(value.([]protocol.TypeHierarchyResult)) },
		reflect.TypeOf((*protocol.SemanticTokensResult)(nil)): func(value any) any { return SemanticTokensResult(value.(*protocol.SemanticTokensResult)) },
		reflect.TypeOf(protocol.FoldingRange{}):               func(value any) any { return FoldingRange(value.(protocol.FoldingRange)) },
		reflect.TypeOf([]protocol.FoldingRange{}):             func(value any) any { return FoldingRanges(value.([]protocol.FoldingRange)) },
	}}
}

func normalizeHoverResultPtr(value any) any {
	result := value.(*protocol.HoverResult)
	if result == nil {
		return result
	}
	converted := HoverResult(*result)
	return &converted
}

// normalize 根据精确的协议类型选择展示归一化器；未知类型原样返回。
func (dispatch displayNormalizerDispatch) normalize(value any) (any, bool) {
	normalizer, ok := dispatch.normalizers[reflect.TypeOf(value)]
	if !ok {
		return value, false
	}
	return normalizer(value), true
}

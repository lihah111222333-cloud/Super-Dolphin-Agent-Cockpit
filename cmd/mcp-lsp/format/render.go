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
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
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
// startLine 非法时从 1 开始，保证文本回显始终可定位。
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

// RenderGroupedLocations 将按文件分组的位置结果渲染成稳定行协议。
// total 保留限制前计数，showing 由实际输出的 ROW 数决定。
func RenderGroupedLocations(result protocol.GroupedLocationResult) string {
	files := make([]string, 0, len(result.Data))
	showing := 0
	for file := range result.Data {
		files = append(files, file)
		showing += len(result.Data[file])
	}
	sort.Strings(files)
	total := max(result.Total, showing)
	lines := make([]string, 0, showing+2)
	lines = append(lines, lineprotocol.HeaderLine(total, showing, showing < total, "location"))
	for _, file := range files {
		for _, row := range result.Data[file] {
			fields := []lineprotocol.Field{
				{Key: "file", Value: file},
				{Key: "line", Value: strconv.Itoa(row.Line)},
				{Key: "col", Value: strconv.Itoa(row.Col)},
			}
			if row.FuncStart > 0 && row.FuncEnd >= row.FuncStart {
				fields = append(fields,
					lineprotocol.Field{Key: "func_start", Value: strconv.Itoa(row.FuncStart)},
					lineprotocol.Field{Key: "func_end", Value: strconv.Itoa(row.FuncEnd)},
				)
			}
			lines = append(lines, lineprotocol.FieldsRecord("ROW", fields...))
		}
	}
	if hint := strings.TrimSpace(result.Hint); hint != "" {
		lines = append(lines, lineprotocol.TextRecord("HINT", hint))
	}
	return strings.Join(lines, "\n")
}

// RenderCompactList 渲染已按上限保留的全部列表行。
func RenderCompactList[T any](list CompactList[T]) (string, error) {
	return list.ToPlainText(), nil
}

// ToPlainText 将已保留列表完整渲染为稳定行协议。
func (list CompactList[T]) ToPlainText() string {
	total := max(list.Total, len(list.Data))
	showing := len(list.Data)
	lines := []string{lineprotocol.HeaderLine(total, showing, showing < total, compactListUnit[T]())}
	for _, item := range list.Data {
		lines = append(lines, compactListRecord(item))
	}
	if hint := strings.TrimSpace(list.Hint); hint != "" {
		lines = append(lines, lineprotocol.TextRecord("HINT", hint))
	}
	return strings.Join(lines, "\n")
}

func compactListUnit[T any]() string {
	elemType := reflect.TypeFor[T]()
	for elemType.Kind() == reflect.Pointer {
		elemType = elemType.Elem()
	}
	switch elemType.Name() {
	case "CompactCompletionItem":
		return "completion"
	case "CompactWorkspaceSymbol":
		return "symbol"
	default:
		return "row"
	}
}

func compactListRecord(item any) string {
	value := reflect.ValueOf(item)
	for value.IsValid() && value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return lineprotocol.FieldsRecord("ROW", lineprotocol.Field{Key: "value", Value: "<nil>"})
		}
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return lineprotocol.FieldsRecord("ROW", lineprotocol.Field{Key: "value", Value: fmt.Sprint(item)})
	}
	fields := make([]lineprotocol.Field, 0, value.NumField())
	valueType := value.Type()
	for index := range value.NumField() {
		field := compactListField(strings.ToLower(valueType.Field(index).Name), value.Field(index))
		if field.Key != "" {
			fields = append(fields, field)
		}
	}
	if len(fields) == 0 {
		fields = append(fields, lineprotocol.Field{Key: "value", Value: fmt.Sprint(item)})
	}
	return lineprotocol.FieldsRecord("ROW", fields...)
}

func compactListField(name string, value reflect.Value) lineprotocol.Field {
	switch value.Kind() {
	case reflect.String:
		if value.String() != "" {
			return lineprotocol.Field{Key: name, Value: value.String()}
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if value.Int() != 0 {
			return lineprotocol.Field{Key: name, Value: strconv.FormatInt(value.Int(), 10)}
		}
	}
	return lineprotocol.Field{}
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
		reflect.TypeFor[protocol.Location]():                func(value any) any { return Location(value.(protocol.Location)) },
		reflect.TypeFor[*protocol.Location]():               func(value any) any { return LocationPtr(value.(*protocol.Location)) },
		reflect.TypeFor[protocol.HoverResult]():             func(value any) any { return HoverResult(value.(protocol.HoverResult)) },
		reflect.TypeFor[*protocol.HoverResult]():            normalizeHoverResultPtr,
		reflect.TypeFor[[]protocol.LocationResult]():        func(value any) any { return LocationResults(value.([]protocol.LocationResult)) },
		reflect.TypeFor[[]protocol.DocumentSymbol]():        func(value any) any { return DocumentSymbols(value.([]protocol.DocumentSymbol)) },
		reflect.TypeFor[*protocol.WorkspaceEdit]():          func(value any) any { return WorkspaceEdit(value.(*protocol.WorkspaceEdit)) },
		reflect.TypeFor[[]protocol.TextEdit]():              func(value any) any { return TextEdits(value.([]protocol.TextEdit)) },
		reflect.TypeFor[[]protocol.Diagnostic]():            func(value any) any { return Diagnostics(value.([]protocol.Diagnostic)) },
		reflect.TypeFor[[]protocol.CodeActionResult]():      func(value any) any { return CodeActionResults(value.([]protocol.CodeActionResult)) },
		reflect.TypeFor[[]protocol.WorkspaceSymbolResult](): func(value any) any { return WorkspaceSymbolResults(value.([]protocol.WorkspaceSymbolResult)) },
		reflect.TypeFor[[]protocol.CallHierarchyResult]():   func(value any) any { return CallHierarchyResults(value.([]protocol.CallHierarchyResult)) },
		reflect.TypeFor[[]protocol.TypeHierarchyResult]():   func(value any) any { return TypeHierarchyResults(value.([]protocol.TypeHierarchyResult)) },
		reflect.TypeFor[*protocol.SemanticTokensResult]():   func(value any) any { return SemanticTokensResult(value.(*protocol.SemanticTokensResult)) },
		reflect.TypeFor[protocol.FoldingRange]():            func(value any) any { return FoldingRange(value.(protocol.FoldingRange)) },
		reflect.TypeFor[[]protocol.FoldingRange]():          func(value any) any { return FoldingRanges(value.([]protocol.FoldingRange)) },
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

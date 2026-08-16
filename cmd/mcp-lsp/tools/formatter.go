package tools

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/format"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
)

// symbolKindName 返回 SymbolKind 的可读名称，未知值返回 SymbolKind(n) 形式。
func symbolKindName(kind protocol.SymbolKind) string {
	names := map[protocol.SymbolKind]string{
		1:  "File",
		2:  "Module",
		3:  "Namespace",
		4:  "Package",
		5:  "Class",
		6:  "Method",
		7:  "Property",
		8:  "Field",
		9:  "Constructor",
		10: "Enum",
		11: "Interface",
		12: "Function",
		13: "Variable",
		14: "Constant",
		15: "String",
		16: "Number",
		17: "Boolean",
		18: "Array",
		19: "Object",
		20: "Key",
		21: "Null",
		22: "EnumMember",
		23: "Struct",
		24: "Event",
		25: "Operator",
		26: "TypeParameter",
	}
	if name, ok := names[kind]; ok {
		return name
	}
	return fmt.Sprintf("SymbolKind(%d)", kind)
}

// FormatToPlainText 将复杂工具结果渲染为面向模型阅读的纯文本。
// 只处理已知结构化响应；未知类型返回 ok=false 交给默认 JSON 渲染。
func FormatToPlainText(result any) (string, bool) {
	if result == nil {
		return "", false
	}
	if text, ok := formatToolErrorEnvelope(result); ok {
		return text, true
	}
	if text, ok := formatBudgetOverflow(result); ok {
		return text, true
	}
	if text, ok := formatXrefAndOutline(result); ok {
		return text, true
	}
	if text, ok := formatOtherStructures(result); ok {
		return text, true
	}
	return formatCompactList(result)
}

// formatToolErrorEnvelope 只在 mcp-lsp 边界把类型化错误渲染为稳定错误行协议。
func formatToolErrorEnvelope(result any) (string, bool) {
	var envelope common.ToolErrorEnvelope
	switch value := result.(type) {
	case common.ToolErrorEnvelope:
		envelope = value
	case *common.ToolErrorEnvelope:
		if value == nil {
			return "", false
		}
		envelope = *value
	default:
		return "", false
	}
	code := strings.TrimSpace(envelope.Code)
	if code == "" {
		code = "tool_error"
	}
	retryable := 0
	if envelope.Retryable {
		retryable = 1
	}
	lines := []string{fmt.Sprintf("ERROR code=%s retryable=%d", code, retryable)}
	if message := strings.TrimSpace(envelope.Error); message != "" {
		lines = append(lines, lineprotocol.TextRecord("MESSAGE", message))
	}
	if hint := strings.TrimSpace(envelope.Hint); hint != "" {
		lines = append(lines, lineprotocol.TextRecord("HINT", hint))
	}
	return strings.Join(lines, "\n"), true
}

// formatBudgetOverflow 将 result_too_large 信封转成人类可读提示。
// 输出包含实际字节数、预算和 next_action，避免模型只看到原始 JSON 后继续扩大结果。
func formatBudgetOverflow(result any) (string, bool) {
	payload, ok := result.(map[string]any)
	if !ok {
		return "", false
	}
	if code, _ := payload["error_code"].(string); code != "result_too_large" {
		return "", false
	}
	tool := stringPayloadValue(payload, "tool", "tool")
	hint, _ := payload["hint"].(string)
	actual := numericPayloadValue(payload, "actual_bytes")
	budget := numericPayloadValue(payload, "budget_bytes")
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s result truncated: exceeded output budget (%.0f / %.0f bytes).", tool, actual, budget)
	if hint != "" {
		fmt.Fprintf(&sb, "\nHint: %s", hint)
	}
	appendBudgetNextAction(&sb, payload["next_action"])
	return sb.String(), true
}

// stringPayloadValue 从 payload map 中取字符串值，缺失时返回 fallback。
func stringPayloadValue(payload map[string]any, key, fallback string) string {
	value, _ := payload[key].(string)
	if value == "" {
		return fallback
	}
	return value
}

// numericPayloadValue 从 payload map 中取数值，兼容 float64 和 int 类型。
func numericPayloadValue(payload map[string]any, key string) float64 {
	switch value := payload[key].(type) {
	case float64:
		return value
	case int:
		return float64(value)
	default:
		return 0
	}
}

// appendBudgetNextAction 把 next_action 的 tip 和 suggest_args 追加到输出。
func appendBudgetNextAction(sb *strings.Builder, value any) {
	next, ok := value.(map[string]any)
	if !ok {
		return
	}
	if tip, _ := next["tip"].(string); tip != "" {
		fmt.Fprintf(sb, "\nTip: %s", tip)
	}
	args, ok := next["suggest_args"].(map[string]any)
	if ok && len(args) > 0 {
		fmt.Fprintf(sb, "\nSuggested args: %v", args)
	}
}

// formatXrefAndOutline 渲染跳转、调用层级和文档大纲类结果。
// 这些结果保留 file:line:col 形态，便于后续直接传给 read_file 或 inspect。
func formatXrefAndOutline(result any) (string, bool) {
	switch val := result.(type) {
	case protocol.GroupedLocationResult:
		return format.RenderGroupedLocations(val), true
	case []protocol.LocationResult:
		return formatLocations(val), true
	case []protocol.CallHierarchyResult:
		return formatCallHierarchy(val), true
	case format.CompactList[compactHierarchyResult]:
		return formatCompactCallHierarchy(val), true
	case []protocol.TypeHierarchyResult:
		return formatTypeHierarchy(val), true
	case []protocol.DocumentSymbol:
		return formatDocumentOutline(val), true
	case documentSymbolListResponse:
		return val.ToPlainText(), true
	}
	return "", false
}

// formatOtherStructures 渲染补全、折叠、签名和 workspace symbol 等非位置类 LSP 结果。
func formatOtherStructures(result any) (string, bool) {
	switch val := result.(type) {
	case []protocol.WorkspaceSymbolResult:
		return formatWorkspaceSymbols(val), true
	case []protocol.FoldingRange:
		return formatFoldingRanges(val), true
	case *protocol.SemanticTokensResult:
		return formatSemanticTokens(val), true
	case *protocol.SignatureHelpResult:
		return formatSignatureHelp(val), true
	case []protocol.CompletionItem:
		return formatCompletionItems(val), true
	}
	return "", false
}

// formatLocations 把位置列表渲染为 file:line:col 格式的纯文本。
func formatLocations(val []protocol.LocationResult) string {
	if len(val) == 0 {
		return "No locations found."
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Locations Found: %d total\n", len(val))
	for i, item := range val {
		loc := item.PrimaryLocation()
		if loc == nil {
			continue
		}
		path := format.URIToPath(loc.URI)
		funcInfo := ""
		if item.HasFuncRange() {
			funcInfo = fmt.Sprintf(" [func L%d-L%d]", item.FuncStart, item.FuncEnd)
		}
		fmt.Fprintf(&sb, "  [%d] %s:%d:%d%s\n", i+1, path, loc.Range.Start.Line, loc.Range.Start.Character, funcInfo)
	}
	return strings.TrimSpace(sb.String())
}

// formatCallHierarchy 把 call hierarchy 结果渲染为带缩进的纯文本。
func formatCallHierarchy(val []protocol.CallHierarchyResult) string {
	if len(val) == 0 {
		return "No call hierarchy items found."
	}
	var sb strings.Builder
	sb.WriteString("Call Hierarchy:\n")
	for _, item := range val {
		fmt.Fprintf(&sb, "- %s `%s` at %s:%d:%d\n",
			symbolKindName(protocol.SymbolKind(item.Item.Kind)), item.Item.Name,
			format.URIToPath(item.Item.URI),
			item.Item.Range.Start.Line, item.Item.Range.Start.Character)
		formatIncomingCalls(&sb, item.Incoming)
		formatOutgoingCalls(&sb, item.Outgoing)
	}
	return strings.TrimSpace(sb.String())
}

// formatCompactCallHierarchy 将 compact hierarchy envelope 渲染为包含真实入边和出边的文本。
func formatCompactCallHierarchy(val format.CompactList[compactHierarchyResult]) string {
	if len(val.Data) == 0 {
		return "No call hierarchy items found."
	}
	var sb strings.Builder
	sb.WriteString("Call Hierarchy:\n")
	for _, row := range val.Data {
		formatCompactHierarchyItem(&sb, "-", row.Item)
		if len(row.Incoming) > 0 {
			sb.WriteString("  Incoming Calls:\n")
			for i, call := range row.Incoming {
				formatCompactHierarchyItem(&sb, fmt.Sprintf("    [%d]", i+1), call.From)
				formatCompactHierarchyRanges(&sb, call.FromRanges)
			}
		}
		if len(row.Outgoing) > 0 {
			sb.WriteString("  Outgoing Calls:\n")
			for i, call := range row.Outgoing {
				formatCompactHierarchyItem(&sb, fmt.Sprintf("    [%d]", i+1), call.To)
				formatCompactHierarchyRanges(&sb, call.FromRanges)
			}
		}
	}
	return strings.TrimSpace(sb.String())
}

func formatCompactHierarchyItem(sb *strings.Builder, prefix string, item compactHierarchyItem) {
	fmt.Fprintf(sb, "%s %s `%s` at %s:%d:%d\n",
		prefix, symbolKindName(protocol.SymbolKind(item.Kind)), item.Name, item.File, item.Line, item.Col)
}

func formatCompactHierarchyRanges(sb *strings.Builder, ranges []compactHierarchyLocation) {
	if len(ranges) == 0 {
		return
	}
	parts := make([]string, 0, len(ranges))
	for _, location := range ranges {
		parts = append(parts, fmt.Sprintf("%s:%d:%d", location.File, location.Line, location.Col))
	}
	fmt.Fprintf(sb, "      call sites: %s\n", strings.Join(parts, ", "))
}

// formatIncomingCalls 把入向调用列表渲染为带缩进的纯文本。
func formatIncomingCalls(sb *strings.Builder, incoming []protocol.CallHierarchyIncomingCall) {
	if len(incoming) == 0 {
		return
	}
	sb.WriteString("  Incoming Calls:\n")
	for i, call := range incoming {
		path := format.URIToPath(call.From.URI)
		ranges := callSiteRanges(path, call.FromRanges)
		fmt.Fprintf(sb, "    [%d] %s `%s` at %s:%d:%d (call sites: %s)\n",
			i+1, symbolKindName(protocol.SymbolKind(call.From.Kind)), call.From.Name,
			path, call.From.Range.Start.Line, call.From.Range.Start.Character,
			ranges)
	}
}

// formatOutgoingCalls 把出向调用列表渲染为带缩进的纯文本。
func formatOutgoingCalls(sb *strings.Builder, outgoing []protocol.CallHierarchyOutgoingCall) {
	if len(outgoing) == 0 {
		return
	}
	sb.WriteString("  Outgoing Calls:\n")
	for i, call := range outgoing {
		path := format.URIToPath(call.To.URI)
		ranges := callSiteRanges(path, call.FromRanges)
		fmt.Fprintf(sb, "    [%d] %s `%s` at %s:%d:%d (call sites: %s)\n",
			i+1, symbolKindName(protocol.SymbolKind(call.To.Kind)), call.To.Name,
			path, call.To.Range.Start.Line, call.To.Range.Start.Character,
			ranges)
	}
}

// callSiteRanges 将调用点范围渲染成 path:line:col 列表。
// 输出保持工具可复制格式，方便后续精确读取调用位置。
func callSiteRanges(path string, ranges []protocol.Range) string {
	if len(ranges) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(ranges))
	for _, r := range ranges {
		parts = append(parts, fmt.Sprintf("%s:%d:%d", path, r.Start.Line, r.Start.Character))
	}
	return strings.Join(parts, ", ")
}

// formatTypeHierarchy 把类型层次结果渲染为带缩进的纯文本。
func formatTypeHierarchy(val []protocol.TypeHierarchyResult) string {
	if len(val) == 0 {
		return "No type hierarchy items found."
	}
	var sb strings.Builder
	sb.WriteString("Type Hierarchy:\n")
	for _, item := range val {
		fmt.Fprintf(&sb, "- %s `%s` at %s:%d:%d\n",
			symbolKindName(protocol.SymbolKind(item.Item.Kind)), item.Item.Name,
			format.URIToPath(item.Item.URI),
			item.Item.Range.Start.Line, item.Item.Range.Start.Character)
		formatSupertypes(&sb, item.Supertypes)
		formatSubtypes(&sb, item.Subtypes)
	}
	return strings.TrimSpace(sb.String())
}

// formatSupertypes 把父类型列表渲染为带缩进的纯文本。
func formatSupertypes(sb *strings.Builder, supertypes []protocol.TypeHierarchyItem) {
	if len(supertypes) == 0 {
		return
	}
	sb.WriteString("  Supertypes:\n")
	for i, super := range supertypes {
		fmt.Fprintf(sb, "    [%d] %s `%s` at %s:%d:%d\n",
			i+1, symbolKindName(protocol.SymbolKind(super.Kind)), super.Name,
			format.URIToPath(super.URI),
			super.Range.Start.Line, super.Range.Start.Character)
	}
}

// formatSubtypes 把子类型列表渲染为带缩进的纯文本。
func formatSubtypes(sb *strings.Builder, subtypes []protocol.TypeHierarchyItem) {
	if len(subtypes) == 0 {
		return
	}
	sb.WriteString("  Subtypes:\n")
	for i, sub := range subtypes {
		fmt.Fprintf(sb, "    [%d] %s `%s` at %s:%d:%d\n",
			i+1, symbolKindName(protocol.SymbolKind(sub.Kind)), sub.Name,
			format.URIToPath(sub.URI),
			sub.Range.Start.Line, sub.Range.Start.Character)
	}
}

// formatDocumentOutline 把文档符号树渲染为缩进大纲纯文本。
func formatDocumentOutline(val []protocol.DocumentSymbol) string {
	if len(val) == 0 {
		return "No document outline symbols found."
	}
	var sb strings.Builder
	sb.WriteString("Document Symbol Outline:\n")
	var formatSymbol func(protocol.DocumentSymbol, int)
	formatSymbol = func(s protocol.DocumentSymbol, depth int) {
		indent := strings.Repeat("  ", depth)
		fmt.Fprintf(&sb, "%s- %s `%s` (L%d-L%d)\n", indent, symbolKindName(s.Kind), s.Name, s.Range.Start.Line, s.Range.End.Line)
		for _, child := range s.Children {
			formatSymbol(child, depth+1)
		}
	}
	for _, s := range val {
		formatSymbol(s, 0)
	}
	return strings.TrimSpace(sb.String())
}

// formatWorkspaceSymbols 格式化工作区符号。
func formatWorkspaceSymbols(val []protocol.WorkspaceSymbolResult) string {
	if len(val) == 0 {
		return "No workspace symbol search results found."
	}
	var sb strings.Builder
	sb.WriteString("Workspace Symbol Search Results:\n")
	for i, item := range val {
		if si := item.SymbolInformation; si != nil {
			fmt.Fprintf(&sb, "  [%d] %s `%s` at %s:%d:%d\n",
				i+1, symbolKindName(si.Kind), si.Name,
				format.URIToPath(si.Location.URI),
				si.Location.Range.Start.Line, si.Location.Range.Start.Character)
		} else if ws := item.WorkspaceSymbol; ws != nil {
			file, line, col, ok := format.LocationFromAny(ws.Location)
			locStr := ""
			if ok {
				locStr = fmt.Sprintf(" at %s:%d:%d", file, line, col)
			}
			containerStr := ""
			if ws.ContainerName != "" {
				containerStr = fmt.Sprintf(" (container: %s)", ws.ContainerName)
			}
			fmt.Fprintf(&sb, "  [%d] %s `%s`%s%s\n", i+1, symbolKindName(protocol.SymbolKind(ws.Kind)), ws.Name, containerStr, locStr)
		}
	}
	return strings.TrimSpace(sb.String())
}

// formatFoldingRanges 把折叠范围列表渲染为纯文本。
func formatFoldingRanges(val []protocol.FoldingRange) string {
	if len(val) == 0 {
		return "No folding ranges found."
	}
	var sb strings.Builder
	sb.WriteString("Folding Ranges:\n")
	for i, fr := range val {
		kindStr := ""
		if fr.Kind != "" {
			kindStr = fmt.Sprintf(" [Kind: %s]", fr.Kind)
		}
		fmt.Fprintf(&sb, "  [%d] Lines L%d - L%d%s\n", i+1, fr.StartLine, fr.EndLine, kindStr)
	}
	return strings.TrimSpace(sb.String())
}

// formatSemanticTokens 把语义 token 列表渲染为纯文本（最多显示 100 条）。
func formatSemanticTokens(val *protocol.SemanticTokensResult) string {
	if val == nil || len(val.Decoded) == 0 {
		return "No semantic tokens decoded."
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Semantic Tokens: %d decoded\n", len(val.Decoded))
	for i, tok := range val.Decoded {
		if i >= 100 {
			sb.WriteString("  ...[truncated]\n")
			break
		}
		fmt.Fprintf(&sb, "  L%d:C%d len=%d type=%s mod=%v\n", tok.Line, tok.StartCharacter, tok.Length, tok.TokenType, tok.TokenModifiers)
	}
	return strings.TrimSpace(sb.String())
}

// formatSignatureHelp 格式化签名帮助。
func formatSignatureHelp(val *protocol.SignatureHelpResult) string {
	if val == nil || len(val.Signatures) == 0 {
		return "No signature help information."
	}
	var sb strings.Builder
	sb.WriteString("Signature Help:\n")
	for i, sig := range val.Signatures {
		active := ""
		if val.ActiveSignature != nil && i == *val.ActiveSignature {
			active = " (active)"
		}
		fmt.Fprintf(&sb, "- Signature%s: `%s`\n", active, sig.Label)
		if docStr, ok := sig.Documentation.(string); ok && docStr != "" {
			fmt.Fprintf(&sb, "  Docs: %s\n", docStr)
		}
		formatParams(&sb, sig.Parameters, val.ActiveSignature != nil && i == *val.ActiveSignature, val.ActiveParameter)
	}
	return strings.TrimSpace(sb.String())
}

// formatParams 格式化params。
func formatParams(sb *strings.Builder, params []protocol.ParameterInformationResult, isActiveSig bool, activeParam *int) {
	if len(params) == 0 {
		return
	}
	sb.WriteString("  Parameters:\n")
	for j, param := range params {
		paramActive := ""
		if isActiveSig && activeParam != nil && j == *activeParam {
			paramActive = " (active)"
		}
		fmt.Fprintf(sb, "    [%d] `%s`%s\n", j+1, param.Label, paramActive)
	}
}

// formatCompletionItems 把补全项列表渲染为纯文本。
func formatCompletionItems(val []protocol.CompletionItem) string {
	if len(val) == 0 {
		return "No completions found."
	}
	var sb strings.Builder
	sb.WriteString("Code Completions:\n")
	for _, item := range val {
		fmt.Fprintf(&sb, "- `%s` [Kind %d]: %s\n", item.Label, item.Kind, item.Detail)
	}
	return strings.TrimSpace(sb.String())
}

// formatCompactList 格式化紧凑列表list。
func formatCompactList(result any) (string, bool) {
	rv := reflect.ValueOf(result)
	if rv.Kind() != reflect.Struct {
		return "", false
	}
	typeName := rv.Type().Name()
	if !strings.HasPrefix(typeName, "CompactList") {
		return "", false
	}
	dataField := rv.FieldByName("Data")
	totalField := rv.FieldByName("Total")
	showingField := rv.FieldByName("Showing")
	hintField := rv.FieldByName("Hint")
	if !dataField.IsValid() || dataField.Kind() != reflect.Slice {
		return "", false
	}
	total := 0
	if totalField.IsValid() {
		total = int(totalField.Int())
	}
	showing := 0
	if showingField.IsValid() {
		showing = int(showingField.Int())
	}
	hint := ""
	if hintField.IsValid() && hintField.Kind() == reflect.String {
		hint = hintField.String()
	}
	return formatCompactListSlice(dataField, total, showing, hint), true
}

// formatCompactListSlice 把反射切片渲染为带序号和总量的纯文本列表。
func formatCompactListSlice(dataField reflect.Value, total, showing int, hint string) string {
	var sb strings.Builder
	length := dataField.Len()
	if length == 0 {
		sb.WriteString("No matches found.")
		appendCompactListHint(&sb, hint)
		return sb.String()
	}
	fmt.Fprintf(&sb, "%s: showing %d of %d total\n\n", compactListTitle(dataField), showing, total)
	preview := min(length, 3)
	for i := range preview {
		elem := dataField.Index(i).Interface()
		fmt.Fprintf(&sb, "  [%d] ", i+1)
		formatCompactListElem(&sb, elem)
		sb.WriteString("\n")
	}
	if preview < length {
		fmt.Fprintf(&sb, "\nText preview: %d of %d rows; full rows are in structuredContent.", preview, length)
	}
	appendCompactListHint(&sb, hint)
	return strings.TrimSpace(sb.String())
}

// appendCompactListHint 把可执行后续动作追加到紧凑文本通道。
func appendCompactListHint(sb *strings.Builder, hint string) {
	if hint = strings.TrimSpace(hint); hint != "" {
		fmt.Fprintf(sb, "\n\nHint: %s", hint)
	}
}

// compactListTitle 根据元素类型名返回紧凑列表的标题。
func compactListTitle(dataField reflect.Value) string {
	elemType := dataField.Type().Elem()
	for elemType.Kind() == reflect.Pointer {
		elemType = elemType.Elem()
	}
	switch elemType.Name() {
	case "CompactCompletionItem":
		return "Code Completions"
	case "CompactWorkspaceSymbol":
		return "Workspace Symbol Matches"
	default:
		return "Compact Results"
	}
}

// formatCompactListElem 格式化紧凑列表listelem。
func formatCompactListElem(sb *strings.Builder, elem any) {
	elemVal := reflect.ValueOf(elem)
	if elemVal.Kind() != reflect.Struct {
		fmt.Fprintf(sb, "%+v", elem)
		return
	}
	elemType := elemVal.Type()
	var fields []string
	for f := 0; f < elemVal.NumField(); f++ {
		fieldName := elemType.Field(f).Name
		fieldVal := elemVal.Field(f).Interface()
		if str, ok := fieldVal.(string); ok && str != "" {
			fields = append(fields, fmt.Sprintf("%s: %q", strings.ToLower(fieldName), str))
		} else if num, ok := fieldVal.(int); ok && num > 0 {
			fields = append(fields, fmt.Sprintf("%s: %d", strings.ToLower(fieldName), num))
		}
	}
	sb.WriteString(strings.Join(fields, ", "))
}

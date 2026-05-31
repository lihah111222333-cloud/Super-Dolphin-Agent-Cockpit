package tools

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/format"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
)

var symbolKindNames = map[protocol.SymbolKind]string{
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

func symbolKindName(kind protocol.SymbolKind) string {
	if name, ok := symbolKindNames[kind]; ok {
		return name
	}
	return fmt.Sprintf("SymbolKind(%d)", kind)
}

// FormatToPlainText checks if the result is a complex structured type
// that requires specialized plain-text / Markdown formatting for the LLM.
func FormatToPlainText(result any) (string, bool) {
	if result == nil {
		return "", false
	}
	if text, ok := formatXrefAndOutline(result); ok {
		return text, true
	}
	if text, ok := formatOtherStructures(result); ok {
		return text, true
	}
	return formatCompactList(result)
}

func formatXrefAndOutline(result any) (string, bool) {
	switch val := result.(type) {
	case protocol.GroupedLocationResult:
		return format.RenderGroupedLocations(val), true
	case []protocol.LocationResult:
		return formatLocations(val), true
	case []protocol.CallHierarchyResult:
		return formatCallHierarchy(val), true
	case []protocol.TypeHierarchyResult:
		return formatTypeHierarchy(val), true
	case []protocol.DocumentSymbol:
		return formatDocumentOutline(val), true
	}
	return "", false
}

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

func formatLocations(val []protocol.LocationResult) string {
	if len(val) == 0 {
		return "No locations found."
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Locations Found: %d total\n", len(val)))
	for i, item := range val {
		loc := item.PrimaryLocation()
		if loc == nil {
			continue
		}
		funcInfo := ""
		if item.HasFuncRange() {
			funcInfo = fmt.Sprintf(" [enclosing function: L%d-L%d]", item.FuncStart, item.FuncEnd)
		}
		fmt.Fprintf(&sb, "  [%d] %s:L%d:C%d%s\n", i+1, loc.URI, loc.Range.Start.Line, loc.Range.Start.Character, funcInfo)
	}
	return strings.TrimSpace(sb.String())
}

func formatCallHierarchy(val []protocol.CallHierarchyResult) string {
	if len(val) == 0 {
		return "No call hierarchy items found."
	}
	var sb strings.Builder
	sb.WriteString("Call Hierarchy:\n")
	for _, item := range val {
		sb.WriteString(fmt.Sprintf("- Function: `%s` [Kind %d] at %s:L%d\n", item.Item.Name, item.Item.Kind, item.Item.URI, item.Item.Range.Start.Line))
		formatIncomingCalls(&sb, item.Incoming)
		formatOutgoingCalls(&sb, item.Outgoing)
	}
	return strings.TrimSpace(sb.String())
}

func formatIncomingCalls(sb *strings.Builder, incoming []protocol.CallHierarchyIncomingCall) {
	if len(incoming) == 0 {
		return
	}
	sb.WriteString("  Incoming Calls:\n")
	for i, call := range incoming {
		var ranges []string
		for _, r := range call.FromRanges {
			ranges = append(ranges, fmt.Sprintf("L%d", r.Start.Line))
		}
		sb.WriteString(fmt.Sprintf("    [%d] `%s` in %s:L%d (called from %s)\n", i+1, call.From.Name, call.From.URI, call.From.Range.Start.Line, strings.Join(ranges, ", ")))
	}
}

func formatOutgoingCalls(sb *strings.Builder, outgoing []protocol.CallHierarchyOutgoingCall) {
	if len(outgoing) == 0 {
		return
	}
	sb.WriteString("  Outgoing Calls:\n")
	for i, call := range outgoing {
		var ranges []string
		for _, r := range call.FromRanges {
			ranges = append(ranges, fmt.Sprintf("L%d", r.Start.Line))
		}
		sb.WriteString(fmt.Sprintf("    [%d] `%s` in %s:L%d (call site: %s)\n", i+1, call.To.Name, call.To.URI, call.To.Range.Start.Line, strings.Join(ranges, ", ")))
	}
}

func formatTypeHierarchy(val []protocol.TypeHierarchyResult) string {
	if len(val) == 0 {
		return "No type hierarchy items found."
	}
	var sb strings.Builder
	sb.WriteString("Type Hierarchy:\n")
	for _, item := range val {
		sb.WriteString(fmt.Sprintf("- Type: `%s` [Kind %d] at %s:L%d\n", item.Item.Name, item.Item.Kind, item.Item.URI, item.Item.Range.Start.Line))
		formatSupertypes(&sb, item.Supertypes)
		formatSubtypes(&sb, item.Subtypes)
	}
	return strings.TrimSpace(sb.String())
}

func formatSupertypes(sb *strings.Builder, supertypes []protocol.TypeHierarchyItem) {
	if len(supertypes) == 0 {
		return
	}
	sb.WriteString("  Supertypes:\n")
	for i, super := range supertypes {
		sb.WriteString(fmt.Sprintf("    [%d] `%s` in %s:L%d\n", i+1, super.Name, super.URI, super.Range.Start.Line))
	}
}

func formatSubtypes(sb *strings.Builder, subtypes []protocol.TypeHierarchyItem) {
	if len(subtypes) == 0 {
		return
	}
	sb.WriteString("  Subtypes:\n")
	for i, sub := range subtypes {
		sb.WriteString(fmt.Sprintf("    [%d] `%s` in %s:L%d\n", i+1, sub.Name, sub.URI, sub.Range.Start.Line))
	}
}

func formatDocumentOutline(val []protocol.DocumentSymbol) string {
	if len(val) == 0 {
		return "No document outline symbols found."
	}
	var sb strings.Builder
	sb.WriteString("Document Symbol Outline:\n")
	var formatSymbol func(protocol.DocumentSymbol, int)
	formatSymbol = func(s protocol.DocumentSymbol, depth int) {
		indent := strings.Repeat("  ", depth)
		sb.WriteString(fmt.Sprintf("%s- %s: `%s` (Range L%d-L%d)\n", indent, symbolKindName(s.Kind), s.Name, s.Range.Start.Line, s.Range.End.Line))
		for _, child := range s.Children {
			formatSymbol(child, depth+1)
		}
	}
	for _, s := range val {
		formatSymbol(s, 0)
	}
	return strings.TrimSpace(sb.String())
}

func formatWorkspaceSymbols(val []protocol.WorkspaceSymbolResult) string {
	if len(val) == 0 {
		return "No workspace symbol search results found."
	}
	var sb strings.Builder
	sb.WriteString("Workspace Symbol Search Results:\n")
	for i, item := range val {
		if si := item.SymbolInformation; si != nil {
			sb.WriteString(fmt.Sprintf("  [%d] %s: `%s` in %s:L%d\n", i+1, symbolKindName(si.Kind), si.Name, format.URIToPath(si.Location.URI), si.Location.Range.Start.Line))
		} else if ws := item.WorkspaceSymbol; ws != nil {
			file, line, _, ok := format.LocationFromAny(ws.Location)
			locStr := ""
			if ok {
				locStr = fmt.Sprintf(" in %s:L%d", file, line)
			}
			sb.WriteString(fmt.Sprintf("  [%d] Kind %d: `%s` (container: %s)%s\n", i+1, ws.Kind, ws.Name, ws.ContainerName, locStr))
		}
	}
	return strings.TrimSpace(sb.String())
}

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
		sb.WriteString(fmt.Sprintf("  [%d] Lines L%d - L%d%s\n", i+1, fr.StartLine, fr.EndLine, kindStr))
	}
	return strings.TrimSpace(sb.String())
}

func formatSemanticTokens(val *protocol.SemanticTokensResult) string {
	if val == nil || len(val.Decoded) == 0 {
		return "No semantic tokens decoded."
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Semantic Tokens: %d decoded\n", len(val.Decoded)))
	for i, tok := range val.Decoded {
		if i >= 100 {
			sb.WriteString("  ...[truncated]\n")
			break
		}
		sb.WriteString(fmt.Sprintf("  L%d:C%d len=%d type=%s mod=%v\n", tok.Line, tok.StartCharacter, tok.Length, tok.TokenType, tok.TokenModifiers))
	}
	return strings.TrimSpace(sb.String())
}

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
		sb.WriteString(fmt.Sprintf("- Signature%s: `%s`\n", active, sig.Label))
		if docStr, ok := sig.Documentation.(string); ok && docStr != "" {
			sb.WriteString(fmt.Sprintf("  Docs: %s\n", docStr))
		}
		formatParams(&sb, sig.Parameters, val.ActiveSignature != nil && i == *val.ActiveSignature, val.ActiveParameter)
	}
	return strings.TrimSpace(sb.String())
}

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
		sb.WriteString(fmt.Sprintf("    [%d] `%s`%s\n", j+1, param.Label, paramActive))
	}
}

func formatCompletionItems(val []protocol.CompletionItem) string {
	if len(val) == 0 {
		return "No completions found."
	}
	var sb strings.Builder
	sb.WriteString("Code Completions:\n")
	for _, item := range val {
		sb.WriteString(fmt.Sprintf("- `%s` [Kind %d]: %s\n", item.Label, item.Kind, item.Detail))
	}
	return strings.TrimSpace(sb.String())
}

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
	return formatCompactListSlice(dataField, total, showing), true
}

func formatCompactListSlice(dataField reflect.Value, total, showing int) string {
	var sb strings.Builder
	length := dataField.Len()
	if length == 0 {
		return "No matches found."
	}
	sb.WriteString(fmt.Sprintf("%s: showing %d of %d total\n\n", compactListTitle(dataField), showing, total))
	for i := 0; i < length; i++ {
		elem := dataField.Index(i).Interface()
		sb.WriteString(fmt.Sprintf("  [%d] ", i+1))
		formatCompactListElem(&sb, elem)
		sb.WriteString("\n")
	}
	return strings.TrimSpace(sb.String())
}

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

func formatCompactListElem(sb *strings.Builder, elem any) {
	elemVal := reflect.ValueOf(elem)
	if elemVal.Kind() != reflect.Struct {
		sb.WriteString(fmt.Sprintf("%+v", elem))
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

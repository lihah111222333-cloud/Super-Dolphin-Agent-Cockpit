package tools

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/format"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
)

// documentSymbolListResponse 是 document_symbol 的分页响应。
type documentSymbolListResponse struct {
	Data           []protocol.DocumentSymbol  `json:"data"`
	Total          int                        `json:"total"`
	Showing        int                        `json:"showing"`
	Truncated      bool                       `json:"truncated,omitempty"`
	FailureReason  string                     `json:"failure_reason,omitempty"`
	EffectiveLimit int                        `json:"effective_limit,omitempty"`
	NextStep       string                     `json:"next_step,omitempty"`
	Hint           string                     `json:"hint,omitempty"`
	Continuation   *protocol.ContinuationInfo `json:"continuation,omitempty"`
}

type compactDocumentSymbol struct {
	Name      string              `json:"name"`
	Kind      protocol.SymbolKind `json:"kind"`
	Depth     int                 `json:"depth"`
	StartLine int                 `json:"start_line"`
	StartCol  int                 `json:"start_col"`
	EndLine   int                 `json:"end_line"`
	EndCol    int                 `json:"end_col"`
}

// MarshalJSON 输出扁平符号骨架，避免结构化响应重复携带深层 children/detail 元数据。
// ToPlainText 仍从原始树渲染，因此模型可见行协议和结构化摘要保持同一 total/showing。
func (response documentSymbolListResponse) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Data           []compactDocumentSymbol    `json:"data"`
		Total          int                        `json:"total"`
		Showing        int                        `json:"showing"`
		Truncated      bool                       `json:"truncated,omitempty"`
		FailureReason  string                     `json:"failure_reason,omitempty"`
		EffectiveLimit int                        `json:"effective_limit,omitempty"`
		NextStep       string                     `json:"next_step,omitempty"`
		Hint           string                     `json:"hint,omitempty"`
		Continuation   *protocol.ContinuationInfo `json:"continuation,omitempty"`
	}{
		Data: compactDocumentSymbolRows(response.Data), Total: response.Total, Showing: response.Showing,
		Truncated: response.Truncated, FailureReason: response.FailureReason, EffectiveLimit: response.EffectiveLimit,
		NextStep: response.NextStep, Hint: response.Hint, Continuation: response.Continuation,
	})
}

func compactDocumentSymbolRows(symbols []protocol.DocumentSymbol) []compactDocumentSymbol {
	return appendCompactDocumentSymbolRows(nil, symbols, 0)
}

func appendCompactDocumentSymbolRows(rows []compactDocumentSymbol, symbols []protocol.DocumentSymbol, depth int) []compactDocumentSymbol {
	for _, symbol := range symbols {
		rows = append(rows, compactDocumentSymbol{
			Name: symbol.Name, Kind: symbol.Kind, Depth: depth,
			StartLine: symbol.Range.Start.Line, StartCol: symbol.Range.Start.Character,
			EndLine: symbol.Range.End.Line, EndCol: symbol.Range.End.Character,
		})
		rows = appendCompactDocumentSymbolRows(rows, symbol.Children, depth+1)
	}
	return rows
}

type workspaceSymbolListResponse struct {
	Data         []format.CompactWorkspaceSymbol `json:"data"`
	Total        int                             `json:"total"`
	Showing      int                             `json:"showing"`
	Truncated    bool                            `json:"truncated,omitempty"`
	Hint         string                          `json:"hint,omitempty"`
	Continuation *protocol.ContinuationInfo      `json:"continuation,omitempty"`
}

type foldingRangeListResponse struct {
	Data         []protocol.FoldingRange    `json:"data"`
	Total        int                        `json:"total"`
	Showing      int                        `json:"showing"`
	Truncated    bool                       `json:"truncated,omitempty"`
	Hint         string                     `json:"hint,omitempty"`
	Continuation *protocol.ContinuationInfo `json:"continuation,omitempty"`
}

type semanticTokenListResponse struct {
	Legend       protocol.SemanticTokensLegend   `json:"legend"`
	Data         []protocol.DecodedSemanticToken `json:"data"`
	Total        int                             `json:"total"`
	Truncated    bool                            `json:"truncated,omitempty"`
	Hint         string                          `json:"hint,omitempty"`
	Continuation *protocol.ContinuationInfo      `json:"continuation,omitempty"`
}

// ToPlainText 把递归符号树扁平为稳定的前序 ROW。
func (response documentSymbolListResponse) ToPlainText() string {
	lines := []string{lineprotocol.HeaderLine(response.Total, response.Showing, response.Truncated, "symbol")}
	if response.FailureReason != "" {
		lines = append(lines, lineprotocol.FieldsRecord("ATTR",
			lineprotocol.Field{Key: "failure_reason", Value: response.FailureReason},
			lineprotocol.Field{Key: "effective_limit", Value: strconv.Itoa(response.EffectiveLimit)},
			lineprotocol.Field{Key: "next_step", Value: response.NextStep},
		))
	}
	appendDocumentSymbolRows(&lines, response.Data, 0)
	lines = appendContinuationRecord(lines, response.Continuation)
	return appendStructureHint(lines, response.Hint)
}

// isCoreStructureSymbol 判断符号是否属于可展示的文件结构骨架。
func isCoreStructureSymbol(kind protocol.SymbolKind, inFunction bool) bool {
	switch kind {
	case protocol.SymbolKindClass, protocol.SymbolKindStruct, protocol.SymbolKindInterface,
		protocol.SymbolKindFunction, protocol.SymbolKindMethod, protocol.SymbolKindEnum,
		protocol.SymbolKindTypeParameter, protocol.SymbolKindFile, protocol.SymbolKindModule,
		protocol.SymbolKindNamespace, protocol.SymbolKindPackage, protocol.SymbolKindConstructor,
		protocol.SymbolKindEnumMember, protocol.SymbolKindProperty, protocol.SymbolKindField,
		protocol.SymbolKindConstant, protocol.SymbolKindEvent:
		return true
	case protocol.SymbolKindVariable:
		return !inFunction
	default:
		return false
	}
}

// isFunctionKind 判断符号是否代表函数体作用域。
func isFunctionKind(kind protocol.SymbolKind) bool {
	switch kind {
	case protocol.SymbolKindFunction, protocol.SymbolKindMethod, protocol.SymbolKindConstructor:
		return true
	default:
		return false
	}
}

func appendDocumentSymbolRows(lines *[]string, symbols []protocol.DocumentSymbol, depth int) {
	for _, symbol := range symbols {
		*lines = append(*lines, lineprotocol.FieldsRecord("ROW",
			lineprotocol.Field{Key: "name", Value: symbol.Name},
			lineprotocol.Field{Key: "kind", Value: strconv.Itoa(int(symbol.Kind))},
			lineprotocol.Field{Key: "depth", Value: strconv.Itoa(depth)},
			lineprotocol.Field{Key: "start_line", Value: strconv.Itoa(format.FromLSP(symbol.Range.Start.Line))},
			lineprotocol.Field{Key: "start_col", Value: strconv.Itoa(format.FromLSP(symbol.Range.Start.Character))},
			lineprotocol.Field{Key: "end_line", Value: strconv.Itoa(format.FromLSP(symbol.Range.End.Line))},
			lineprotocol.Field{Key: "end_col", Value: strconv.Itoa(format.FromLSP(symbol.Range.End.Character))},
		))
		appendDocumentSymbolRows(lines, symbol.Children, depth+1)
	}
}

// ToPlainText 把 workspace symbol 列表渲染为紧凑 ROW。
func (response workspaceSymbolListResponse) ToPlainText() string {
	lines := []string{lineprotocol.HeaderLine(response.Total, response.Showing, response.Truncated, "symbol")}
	for _, symbol := range response.Data {
		lines = append(lines, lineprotocol.FieldsRecord("ROW",
			lineprotocol.Field{Key: "name", Value: symbol.Name},
			lineprotocol.Field{Key: "kind", Value: strconv.Itoa(symbol.Kind)},
			lineprotocol.Field{Key: "file", Value: symbol.File},
			lineprotocol.Field{Key: "line", Value: strconv.Itoa(symbol.Line)},
			lineprotocol.Field{Key: "col", Value: strconv.Itoa(symbol.Col)},
			lineprotocol.Field{Key: "container", Value: symbol.Container},
		))
	}
	lines = appendContinuationRecord(lines, response.Continuation)
	return appendStructureHint(lines, response.Hint)
}

// ToPlainText 把 folding ranges 渲染为紧凑 ROW。
func (response foldingRangeListResponse) ToPlainText() string {
	lines := []string{lineprotocol.HeaderLine(response.Total, response.Showing, response.Truncated, "range")}
	for _, item := range response.Data {
		lines = append(lines, lineprotocol.FieldsRecord("ROW",
			lineprotocol.Field{Key: "start_line", Value: strconv.Itoa(format.FromLSP(item.StartLine))},
			lineprotocol.Field{Key: "end_line", Value: strconv.Itoa(format.FromLSP(item.EndLine))},
			lineprotocol.Field{Key: "kind", Value: item.Kind},
		))
	}
	lines = appendContinuationRecord(lines, response.Continuation)
	return appendStructureHint(lines, response.Hint)
}

// ToPlainText 输出 initialize legend 和已解码 token，不重复原始整数数组。
func (response semanticTokenListResponse) ToPlainText() string {
	lines := []string{lineprotocol.HeaderLine(response.Total, len(response.Data), response.Truncated, "token")}
	lines = append(lines, lineprotocol.FieldsRecord("LEGEND",
		lineprotocol.Field{Key: "token_types", Value: strings.Join(response.Legend.TokenTypes, ",")},
		lineprotocol.Field{Key: "token_modifiers", Value: strings.Join(response.Legend.TokenModifiers, ",")},
	))
	for _, token := range response.Data {
		lines = append(lines, lineprotocol.FieldsRecord("ROW",
			lineprotocol.Field{Key: "line", Value: strconv.Itoa(token.Line)},
			lineprotocol.Field{Key: "col", Value: strconv.Itoa(token.StartCharacter)},
			lineprotocol.Field{Key: "length", Value: strconv.Itoa(token.Length)},
			lineprotocol.Field{Key: "type", Value: token.TokenType},
			lineprotocol.Field{Key: "modifiers", Value: strings.Join(token.TokenModifiers, ",")},
		))
	}
	lines = appendContinuationRecord(lines, response.Continuation)
	return appendStructureHint(lines, response.Hint)
}

func appendStructureHint(lines []string, hint string) string {
	if hint = strings.TrimSpace(hint); hint != "" {
		lines = append(lines, lineprotocol.TextRecord("HINT", hint))
	}
	return strings.Join(lines, "\n")
}

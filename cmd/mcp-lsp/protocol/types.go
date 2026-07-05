package protocol

// Position 表示 LSP 文档内的零基行列位置。
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range 表示 LSP 文档内的半开文本范围。
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location 表示某个 URI 上的具体文本范围。
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// LocationLink 表示定义/实现等跳转返回的链接形态。
type LocationLink struct {
	OriginSelectionRange *Range `json:"originSelectionRange,omitempty"`
	TargetURI            string `json:"targetUri"`
	TargetRange          Range  `json:"targetRange"`
	TargetSelectionRange Range  `json:"targetSelectionRange"`
}

// TextDocumentIdentifier 标识一个已知文档 URI。
type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

// TextDocumentItem 是 didOpen 时发送的完整文档内容。
type TextDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

// VersionedTextDocumentIdentifier 标识带版本号的文档。
type VersionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
}

// OptionalVersionedTextDocumentIdentifier 标识可省略版本号的文档编辑目标。
type OptionalVersionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version *int   `json:"version,omitempty"`
}

// TextDocumentPositionParams 是按文档位置查询的通用参数。
type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// DidOpenTextDocumentParams 是 textDocument/didOpen 的参数。
type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

// DidCloseTextDocumentParams 是 textDocument/didClose 的参数。
type DidCloseTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// DidChangeTextDocumentParams 是 textDocument/didChange 的参数。
type DidChangeTextDocumentParams struct {
	TextDocument   VersionedTextDocumentIdentifier  `json:"textDocument"`
	ContentChanges []TextDocumentContentChangeEvent `json:"contentChanges"`
}

// TextDocumentContentChangeEvent 表示一次全文或增量文本变更。
type TextDocumentContentChangeEvent struct {
	Range       *Range `json:"range,omitempty"`
	RangeLength *int   `json:"rangeLength,omitempty"`
	Text        string `json:"text"`
}

// DiagnosticSeverity 是 LSP 诊断严重程度枚举。
type DiagnosticSeverity int

// 诊断严重程度常量与 LSP 规范的数字值保持一致。
const (
	SeverityError DiagnosticSeverity = iota + 1
	SeverityWarning
	SeverityInformation
	SeverityHint
)

var diagnosticSeverityNames = [...]string{"", "error", "warning", "info", "hint"}

// String 返回诊断严重程度的稳定英文名称。
func (s DiagnosticSeverity) String() string {
	i := int(s)
	if i > 0 && i < len(diagnosticSeverityNames) {
		return diagnosticSeverityNames[i]
	}
	return "unknown"
}

// DiagnosticRelatedInformation 表示诊断关联的补充位置和消息。
type DiagnosticRelatedInformation struct {
	Location Location `json:"location"`
	Message  string   `json:"message"`
}

// Diagnostic 表示语言服务器发布的一条诊断。
type Diagnostic struct {
	Range              Range                          `json:"range"`
	Severity           DiagnosticSeverity             `json:"severity,omitempty"`
	Source             string                         `json:"source,omitempty"`
	Message            string                         `json:"message"`
	Code               any                            `json:"code,omitempty"`
	RelatedInformation []DiagnosticRelatedInformation `json:"relatedInformation,omitempty"`
}

// PublishDiagnosticsParams 是 textDocument/publishDiagnostics 通知参数。
type PublishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Version     *int         `json:"version,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// DocumentDiagnosticParams 是 textDocument/diagnostic 的请求参数。
type DocumentDiagnosticParams struct {
	TextDocument     TextDocumentIdentifier `json:"textDocument"`
	Identifier       string                 `json:"identifier,omitempty"`
	PreviousResultID string                 `json:"previousResultId,omitempty"`
}

// DocumentDiagnosticReport 是 pull diagnostics 返回的完整或未变化结果。
type DocumentDiagnosticReport struct {
	Kind  string       `json:"kind"`
	Items []Diagnostic `json:"items,omitempty"`
}

// MarkupContent 表示 markdown/plaintext 等格式化文本。
type MarkupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// HoverResult 是 hover 请求返回的内容和可选范围。
type HoverResult struct {
	Contents any    `json:"contents"`
	Range    *Range `json:"range,omitempty"`
}

// ReferenceContext 描述 references 查询是否包含声明本身。
type ReferenceContext struct {
	IncludeDeclaration bool `json:"includeDeclaration"`
}

// ReferenceParams 是 textDocument/references 的参数。
type ReferenceParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	Context      ReferenceContext       `json:"context"`
}

// SymbolKind 是 LSP 符号类型枚举。
type SymbolKind int

// SymbolKind 常量与 LSP 规范中的符号类型编号保持一致。
const (
	SymbolKindFile          SymbolKind = 1
	SymbolKindModule        SymbolKind = 2
	SymbolKindNamespace     SymbolKind = 3
	SymbolKindPackage       SymbolKind = 4
	SymbolKindClass         SymbolKind = 5
	SymbolKindMethod        SymbolKind = 6
	SymbolKindProperty      SymbolKind = 7
	SymbolKindField         SymbolKind = 8
	SymbolKindConstructor   SymbolKind = 9
	SymbolKindEnum          SymbolKind = 10
	SymbolKindInterface     SymbolKind = 11
	SymbolKindFunction      SymbolKind = 12
	SymbolKindVariable      SymbolKind = 13
	SymbolKindConstant      SymbolKind = 14
	SymbolKindString        SymbolKind = 15
	SymbolKindNumber        SymbolKind = 16
	SymbolKindBoolean       SymbolKind = 17
	SymbolKindArray         SymbolKind = 18
	SymbolKindObject        SymbolKind = 19
	SymbolKindKey           SymbolKind = 20
	SymbolKindNull          SymbolKind = 21
	SymbolKindEnumMember    SymbolKind = 22
	SymbolKindStruct        SymbolKind = 23
	SymbolKindEvent         SymbolKind = 24
	SymbolKindOperator      SymbolKind = 25
	SymbolKindTypeParameter SymbolKind = 26
)

// DocumentSymbol 表示 documentSymbol 返回的层级符号。
type DocumentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           SymbolKind       `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}

// SymbolInformation 表示旧版扁平 workspace/document symbol 条目。
type SymbolInformation struct {
	Name          string     `json:"name"`
	Kind          SymbolKind `json:"kind"`
	Location      Location   `json:"location"`
	ContainerName string     `json:"containerName,omitempty"`
}

// WorkspaceSymbolLocation 是 workspaceSymbol 可简化为 URI 的位置形态。
type WorkspaceSymbolLocation struct {
	URI string `json:"uri"`
}

// WorkspaceSymbol 表示 workspace/symbol 返回的新版符号条目。
type WorkspaceSymbol struct {
	Name          string `json:"name"`
	Kind          int    `json:"kind"`
	Location      any    `json:"location,omitempty"`
	ContainerName string `json:"containerName,omitempty"`
	Data          any    `json:"data,omitempty"`
}

// WorkspaceSymbolParams 是 workspace/symbol 查询参数。
type WorkspaceSymbolParams struct {
	Query string `json:"query"`
}

// CompletionItem 表示补全列表中的单个候选项。
type CompletionItem struct {
	Label         string `json:"label"`
	Kind          int    `json:"kind,omitempty"`
	Detail        string `json:"detail,omitempty"`
	Documentation any    `json:"documentation,omitempty"`
	InsertText    string `json:"insertText,omitempty"`
	SortText      string `json:"sortText,omitempty"`
	FilterText    string `json:"filterText,omitempty"`
}

// CompletionList 表示 completion 返回的候选集合。
type CompletionList struct {
	IsIncomplete bool             `json:"isIncomplete"`
	Items        []CompletionItem `json:"items"`
}

// FormattingOptions 表示文档格式化所需的缩进选项。
type FormattingOptions struct {
	TabSize      int  `json:"tabSize"`
	InsertSpaces bool `json:"insertSpaces"`
}

// TextEdit 表示对单个文档范围的文本替换。
type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

// TextDocumentEdit 表示带版本目标的文档编辑列表。
type TextDocumentEdit struct {
	TextDocument OptionalVersionedTextDocumentIdentifier `json:"textDocument"`
	Edits        []TextEdit                              `json:"edits"`
}

// WorkspaceEdit 表示跨文档编辑集合。
type WorkspaceEdit struct {
	Changes         map[string][]TextEdit `json:"changes,omitempty"`
	DocumentChanges []TextDocumentEdit    `json:"documentChanges,omitempty"`
}

// Command 表示 LSP 命令调用描述。
type Command struct {
	Title     string `json:"title"`
	Command   string `json:"command"`
	Arguments []any  `json:"arguments,omitempty"`
}

// CodeActionContext 表示 codeAction 查询时的诊断和触发上下文。
type CodeActionContext struct {
	Diagnostics []Diagnostic `json:"diagnostics"`
	Only        []string     `json:"only,omitempty"`
	TriggerKind int          `json:"triggerKind,omitempty"`
}

// CodeAction 表示可直接应用或执行命令的代码动作。
type CodeAction struct {
	Title       string         `json:"title"`
	Kind        string         `json:"kind,omitempty"`
	Diagnostics []Diagnostic   `json:"diagnostics,omitempty"`
	Edit        *WorkspaceEdit `json:"edit,omitempty"`
	Command     *Command       `json:"command,omitempty"`
	Data        any            `json:"data,omitempty"`
}

// CodeActionParams 是 textDocument/codeAction 的参数。
type CodeActionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Range        Range                  `json:"range"`
	Context      CodeActionContext      `json:"context"`
}

// RenameParams 是 textDocument/rename 的参数。
type RenameParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	NewName      string                 `json:"newName"`
}

// CallHierarchyItem 表示 call hierarchy 中的一个函数或方法节点。
type CallHierarchyItem struct {
	Name           string `json:"name"`
	Kind           int    `json:"kind"`
	URI            string `json:"uri"`
	Range          Range  `json:"range"`
	SelectionRange Range  `json:"selectionRange"`
	Detail         string `json:"detail,omitempty"`
	Data           any    `json:"data,omitempty"`
}

// TypeHierarchyItem 表示 type hierarchy 中的一个类型节点。
type TypeHierarchyItem struct {
	Name           string `json:"name"`
	Kind           int    `json:"kind"`
	URI            string `json:"uri"`
	Range          Range  `json:"range"`
	SelectionRange Range  `json:"selectionRange"`
	Detail         string `json:"detail,omitempty"`
	Data           any    `json:"data,omitempty"`
}

// CallHierarchyIncomingCall 表示调用当前节点的上游调用。
type CallHierarchyIncomingCall struct {
	From       CallHierarchyItem `json:"from"`
	FromRanges []Range           `json:"fromRanges"`
}

// CallHierarchyOutgoingCall 表示当前节点发出的下游调用。
type CallHierarchyOutgoingCall struct {
	To         CallHierarchyItem `json:"to"`
	FromRanges []Range           `json:"fromRanges"`
}

// SemanticTokensLegend 描述 semantic tokens 数据中数字索引对应的 token 名称。
type SemanticTokensLegend struct {
	TokenTypes     []string `json:"tokenTypes"`
	TokenModifiers []string `json:"tokenModifiers"`
}

// SemanticTokensOptions 表示 semantic tokens provider 暴露的 legend。
type SemanticTokensOptions struct {
	Legend SemanticTokensLegend `json:"legend"`
}

// SemanticTokens 表示 LSP 原始 semantic tokens 响应。
type SemanticTokens struct {
	ResultID string `json:"resultId,omitempty"`
	Data     []int  `json:"data"`
}

// FoldingRange 表示一个可折叠代码区间。
type FoldingRange struct {
	StartLine      int    `json:"startLine"`
	StartCharacter *int   `json:"startCharacter,omitempty"`
	EndLine        int    `json:"endLine"`
	EndCharacter   *int   `json:"endCharacter,omitempty"`
	Kind           string `json:"kind,omitempty"`
	CollapsedText  string `json:"collapsedText,omitempty"`
}

// SignatureInformationResult 表示签名帮助中的一个函数签名。
type SignatureInformationResult struct {
	Label         string                       `json:"label"`
	Documentation any                          `json:"documentation,omitempty"`
	Parameters    []ParameterInformationResult `json:"parameters,omitempty"`
}

// ParameterInformationResult 表示签名帮助中的单个参数说明。
type ParameterInformationResult struct {
	Label         string `json:"label,omitempty"`
	LabelOffsets  []int  `json:"labelOffsets,omitempty"`
	Documentation any    `json:"documentation,omitempty"`
}

// SignatureHelpResult 表示 signatureHelp 返回的签名集合和当前选中项。
type SignatureHelpResult struct {
	Signatures      []SignatureInformationResult `json:"signatures,omitempty"`
	ActiveSignature *int                         `json:"activeSignature,omitempty"`
	ActiveParameter *int                         `json:"activeParameter,omitempty"`
}

// itemRequest 包装 hierarchy 后续请求中的 item 字段。
type itemRequest[T any] struct {
	Item T `json:"item"`
}

// 常用请求参数别名把同形状 LSP 请求复用到具体方法名。
type (
	HoverParams                = TextDocumentPositionParams
	DefinitionParams           = TextDocumentPositionParams
	ImplementationParams       = TextDocumentPositionParams
	TypeDefinitionParams       = TextDocumentPositionParams
	CompletionParams           = TextDocumentPositionParams
	SignatureHelpParams        = TextDocumentPositionParams
	PrepareCallHierarchyParams = TextDocumentPositionParams
	PrepareTypeHierarchyParams = TextDocumentPositionParams
	DocumentSymbolParams       = DidCloseTextDocumentParams
	DocumentFormattingParams   struct {
		TextDocument TextDocumentIdentifier `json:"textDocument"`
		Options      FormattingOptions      `json:"options"`
	}
	FoldingRangeParams               = DidCloseTextDocumentParams
	SemanticTokensParams             = DidCloseTextDocumentParams
	CallHierarchyIncomingCallsParams = itemRequest[CallHierarchyItem]
	CallHierarchyOutgoingCallsParams = itemRequest[CallHierarchyItem]
	TypeHierarchySupertypesParams    = itemRequest[TypeHierarchyItem]
	TypeHierarchySubtypesParams      = itemRequest[TypeHierarchyItem]
)

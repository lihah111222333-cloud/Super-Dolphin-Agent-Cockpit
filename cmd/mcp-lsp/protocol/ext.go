package protocol

import "encoding/json"

// LSP 工具响应限制用于保护 xref 与 semantic token 输出体积。
const (
	XRefResultLimit          = 50
	SemanticTokenResultLimit = 200
)

// WorkspaceFolder 对应 LSP initialize 中的 workspace folder 条目。
type WorkspaceFolder struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
}

// InitializeParams 是启动语言服务器时发送的初始化参数。
type InitializeParams struct {
	ProcessID             int                `json:"processId"`
	RootURI               string             `json:"rootUri,omitempty"`
	Capabilities          ClientCapabilities `json:"capabilities"`
	WorkspaceFolders      []WorkspaceFolder  `json:"workspaceFolders,omitempty"`
	InitializationOptions any                `json:"initializationOptions,omitempty"`
}

// ClientCapabilities 汇总客户端在 workspace 和 textDocument 维度声明的能力。
type ClientCapabilities struct {
	TextDocument *TextDocumentClientCapabilities `json:"textDocument,omitempty"`
	Workspace    *WorkspaceClientCapability      `json:"workspace,omitempty"`
}

// WorkspaceClientCapability 描述客户端 workspace 级特性。
type WorkspaceClientCapability struct {
	WorkspaceFolders bool `json:"workspaceFolders,omitempty"`
}

// DynamicRegistrationCapability 表示某类 LSP 能力是否支持动态注册。
type DynamicRegistrationCapability struct {
	DynamicRegistration bool `json:"dynamicRegistration,omitempty"`
}

// TextDocumentClientCapabilities 汇总本 sidecar 使用到的 textDocument 能力声明。
type TextDocumentClientCapabilities struct {
	PublishDiagnostics *PublishDiagnosticsCapability  `json:"publishDiagnostics,omitempty"`
	Hover              *HoverCapability               `json:"hover,omitempty"`
	Completion         *CompletionClientCapability    `json:"completion,omitempty"`
	Rename             *RenameClientCapability        `json:"rename,omitempty"`
	DocumentSymbol     *DocumentSymbolCapability      `json:"documentSymbol,omitempty"`
	Definition         *DynamicRegistrationCapability `json:"definition,omitempty"`
	Implementation     *DynamicRegistrationCapability `json:"implementation,omitempty"`
	TypeDefinition     *DynamicRegistrationCapability `json:"typeDefinition,omitempty"`
	References         *DynamicRegistrationCapability `json:"references,omitempty"`
	CallHierarchy      *CallHierarchyCapability       `json:"callHierarchy,omitempty"`
	TypeHierarchy      *TypeHierarchyCapability       `json:"typeHierarchy,omitempty"`
	CodeAction         *CodeActionCapability          `json:"codeAction,omitempty"`
	SignatureHelp      *SignatureHelpCapability       `json:"signatureHelp,omitempty"`
	Formatting         *FormattingCapability          `json:"formatting,omitempty"`
	FoldingRange       *FoldingRangeCapability        `json:"foldingRange,omitempty"`
	SemanticTokens     *SemanticTokensCapability      `json:"semanticTokens,omitempty"`
}

// DocumentSymbolCapability 描述 documentSymbol 请求的层级符号能力。
type DocumentSymbolCapability struct {
	DynamicRegistration               bool `json:"dynamicRegistration,omitempty"`
	HierarchicalDocumentSymbolSupport bool `json:"hierarchicalDocumentSymbolSupport,omitempty"`
}

// PublishDiagnosticsCapability 描述诊断推送是否可携带 relatedInformation。
type PublishDiagnosticsCapability struct {
	RelatedInformation bool `json:"relatedInformation,omitempty"`
}

// HoverCapability 描述 hover 返回内容可接受的 markup 格式。
type HoverCapability struct {
	ContentFormat []string `json:"contentFormat,omitempty"`
}

// RenameClientCapability 描述 rename 是否支持 prepare 阶段。
type RenameClientCapability struct {
	PrepareSupport bool `json:"prepareSupport,omitempty"`
}

// SemanticTokensCapability 描述 semantic tokens 请求能力和 token legend。
type SemanticTokensCapability struct {
	DynamicRegistration bool                              `json:"dynamicRegistration,omitempty"`
	Requests            *SemanticTokensRequestsCapability `json:"requests,omitempty"`
	TokenTypes          []string                          `json:"tokenTypes,omitempty"`
	TokenModifiers      []string                          `json:"tokenModifiers,omitempty"`
	Formats             []string                          `json:"formats,omitempty"`
}

// SemanticTokensRequestsCapability 描述 semantic tokens 支持 range/full 哪些请求形态。
type SemanticTokensRequestsCapability struct {
	Range any `json:"range,omitempty"`
	Full  any `json:"full,omitempty"`
}

// SemanticTokensFullRequestsCapability 描述 full semantic tokens 是否支持 delta。
type SemanticTokensFullRequestsCapability struct {
	Delta bool `json:"delta,omitempty"`
}

// InitializeResult 是语言服务器初始化响应中的能力集合。
type InitializeResult struct {
	Capabilities ServerCapabilities `json:"capabilities"`
}

// ServerCapabilities 保存 sidecar 关心的语言服务器能力开关。
type ServerCapabilities struct {
	TextDocumentSync           any `json:"textDocumentSync,omitempty"`
	HoverProvider              any `json:"hoverProvider,omitempty"`
	DefinitionProvider         any `json:"definitionProvider,omitempty"`
	ReferencesProvider         any `json:"referencesProvider,omitempty"`
	DocumentSymbolProvider     any `json:"documentSymbolProvider,omitempty"`
	RenameProvider             any `json:"renameProvider,omitempty"`
	DiagnosticProvider         any `json:"diagnosticProvider,omitempty"`
	CompletionProvider         any `json:"completionProvider,omitempty"`
	WorkspaceSymbolProvider    any `json:"workspaceSymbolProvider,omitempty"`
	ImplementationProvider     any `json:"implementationProvider,omitempty"`
	TypeDefinitionProvider     any `json:"typeDefinitionProvider,omitempty"`
	CallHierarchyProvider      any `json:"callHierarchyProvider,omitempty"`
	TypeHierarchyProvider      any `json:"typeHierarchyProvider,omitempty"`
	CodeActionProvider         any `json:"codeActionProvider,omitempty"`
	SignatureHelpProvider      any `json:"signatureHelpProvider,omitempty"`
	DocumentFormattingProvider any `json:"documentFormattingProvider,omitempty"`
	FoldingRangeProvider       any `json:"foldingRangeProvider,omitempty"`
	SemanticTokensProvider     any `json:"semanticTokensProvider,omitempty"`
}

// 常见能力别名复用动态注册结构，保持 initialize JSON 与 LSP 规范字段形状一致。
type (
	CompletionClientCapability = DynamicRegistrationCapability
	CallHierarchyCapability    = DynamicRegistrationCapability
	TypeHierarchyCapability    = DynamicRegistrationCapability
	CodeActionCapability       = DynamicRegistrationCapability
	SignatureHelpCapability    = DynamicRegistrationCapability
	FormattingCapability       = DynamicRegistrationCapability
	FoldingRangeCapability     = DynamicRegistrationCapability
)

// LocationResult 统一承接 Location、LocationLink 和附加函数范围信息。
type LocationResult struct {
	Location     *Location     `json:"location,omitempty"`
	LocationLink *LocationLink `json:"locationLink,omitempty"`
	Canonical    *Location     `json:"canonical,omitempty"`
	FuncStart    int           `json:"func_start,omitempty"`
	FuncEnd      int           `json:"func_end,omitempty"`
}

// CompactLocation 是对外输出的轻量位置，避免暴露完整 LSP Range。
type CompactLocation struct {
	Line      int `json:"line"`
	Col       int `json:"col"`
	FuncStart int `json:"func_start,omitempty"`
	FuncEnd   int `json:"func_end,omitempty"`
}

// GroupedLocationResult 按文件分组 compact locations，并携带截断提示。
type GroupedLocationResult struct {
	Data      map[string][]CompactLocation `json:"data"`
	Total     int                          `json:"total"`
	Showing   int                          `json:"showing"`
	Truncated bool                         `json:"truncated,omitempty"`
	Hint      string                       `json:"hint,omitempty"`
}

// WorkspaceSymbolResult 兼容 workspace/symbol 返回的 SymbolInformation 或 WorkspaceSymbol 两种形态。
type WorkspaceSymbolResult struct {
	SymbolInformation *SymbolInformation `json:"symbolInformation,omitempty"`
	WorkspaceSymbol   *WorkspaceSymbol   `json:"workspaceSymbol,omitempty"`
}

// CodeActionResult 兼容 codeAction 返回的 CodeAction 或 Command 两种形态。
type CodeActionResult struct {
	CodeAction *CodeAction `json:"codeAction,omitempty"`
	Command    *Command    `json:"command,omitempty"`
}

// CallHierarchyResult 聚合一个 call hierarchy item 的 incoming/outgoing 结果。
type CallHierarchyResult struct {
	Item     CallHierarchyItem           `json:"item"`
	Incoming []CallHierarchyIncomingCall `json:"incoming,omitempty"`
	Outgoing []CallHierarchyOutgoingCall `json:"outgoing,omitempty"`
}

// TypeHierarchyResult 聚合一个 type hierarchy item 的父类型和子类型结果。
type TypeHierarchyResult struct {
	Item       TypeHierarchyItem   `json:"item"`
	Supertypes []TypeHierarchyItem `json:"supertypes,omitempty"`
	Subtypes   []TypeHierarchyItem `json:"subtypes,omitempty"`
}

// DecodedSemanticToken 是把 LSP 相对编码解开后的单个 token。
type DecodedSemanticToken struct {
	Line           int      `json:"line"`
	StartCharacter int      `json:"startCharacter"`
	Length         int      `json:"length"`
	TokenType      string   `json:"tokenType"`
	TokenModifiers []string `json:"tokenModifiers,omitempty"`
}

// SemanticTokensResult 同时保留原始 semantic tokens 和便于阅读的解码结果。
type SemanticTokensResult struct {
	ResultID string                 `json:"resultId,omitempty"`
	Data     []int                  `json:"data,omitempty"`
	Decoded  []DecodedSemanticToken `json:"decoded,omitempty"`
}

// PrimaryLocation 返回 LocationResult 中最适合对外展示的位置。
func (r LocationResult) PrimaryLocation() *Location {
	if r.Location != nil {
		return r.Location
	}
	if r.Canonical != nil {
		return r.Canonical
	}
	if r.LocationLink == nil {
		return nil
	}
	location := Location{
		URI:   r.LocationLink.TargetURI,
		Range: r.LocationLink.TargetSelectionRange,
	}
	if location.Range == (Range{}) {
		location.Range = r.LocationLink.TargetRange
	}
	return &location
}

// MarshalJSON 输出扁平位置结构，保持工具响应紧凑。
func (r LocationResult) MarshalJSON() ([]byte, error) {
	loc := r.PrimaryLocation()
	flat := map[string]any{}
	if loc != nil {
		flat["file"] = loc.URI
		flat["line"] = loc.Range.Start.Line
		flat["col"] = loc.Range.Start.Character
		if loc.Range.End != (Position{}) {
			flat["end_line"] = loc.Range.End.Line
			flat["end_col"] = loc.Range.End.Character
		}
	}
	if r.FuncStart > 0 {
		flat["func_start"] = r.FuncStart
	}
	if r.FuncEnd > 0 {
		flat["func_end"] = r.FuncEnd
	}
	return json.Marshal(flat)
}

// HasFuncRange 判断结果是否携带可用于后续 read_file 的函数范围。
func (r LocationResult) HasFuncRange() bool {
	return r.FuncStart > 0 && r.FuncEnd >= r.FuncStart
}

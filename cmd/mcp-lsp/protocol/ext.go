package protocol

import "encoding/json"

const (
	XRefResultLimit          = 50
	SemanticTokenResultLimit = 200
)

type WorkspaceFolder struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
}

type InitializeParams struct {
	ProcessID             int                `json:"processId"`
	RootURI               string             `json:"rootUri,omitempty"`
	Capabilities          ClientCapabilities `json:"capabilities"`
	WorkspaceFolders      []WorkspaceFolder  `json:"workspaceFolders,omitempty"`
	InitializationOptions any                `json:"initializationOptions,omitempty"`
}

type ClientCapabilities struct {
	TextDocument *TextDocumentClientCapabilities `json:"textDocument,omitempty"`
	Workspace    *WorkspaceClientCapability      `json:"workspace,omitempty"`
}

type WorkspaceClientCapability struct {
	WorkspaceFolders bool `json:"workspaceFolders,omitempty"`
}

type DynamicRegistrationCapability struct {
	DynamicRegistration bool `json:"dynamicRegistration,omitempty"`
}

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

type DocumentSymbolCapability struct {
	DynamicRegistration               bool `json:"dynamicRegistration,omitempty"`
	HierarchicalDocumentSymbolSupport bool `json:"hierarchicalDocumentSymbolSupport,omitempty"`
}

type PublishDiagnosticsCapability struct {
	RelatedInformation bool `json:"relatedInformation,omitempty"`
}

type HoverCapability struct {
	ContentFormat []string `json:"contentFormat,omitempty"`
}

type RenameClientCapability struct {
	PrepareSupport bool `json:"prepareSupport,omitempty"`
}

type SemanticTokensCapability struct {
	DynamicRegistration bool                              `json:"dynamicRegistration,omitempty"`
	Requests            *SemanticTokensRequestsCapability `json:"requests,omitempty"`
	TokenTypes          []string                          `json:"tokenTypes,omitempty"`
	TokenModifiers      []string                          `json:"tokenModifiers,omitempty"`
	Formats             []string                          `json:"formats,omitempty"`
}

type SemanticTokensRequestsCapability struct {
	Range any `json:"range,omitempty"`
	Full  any `json:"full,omitempty"`
}

type SemanticTokensFullRequestsCapability struct {
	Delta bool `json:"delta,omitempty"`
}

type InitializeResult struct {
	Capabilities ServerCapabilities `json:"capabilities"`
}

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

type (
	CompletionClientCapability = DynamicRegistrationCapability
	CallHierarchyCapability    = DynamicRegistrationCapability
	TypeHierarchyCapability    = DynamicRegistrationCapability
	CodeActionCapability       = DynamicRegistrationCapability
	SignatureHelpCapability    = DynamicRegistrationCapability
	FormattingCapability       = DynamicRegistrationCapability
	FoldingRangeCapability     = DynamicRegistrationCapability
)

type LocationResult struct {
	Location     *Location     `json:"location,omitempty"`
	LocationLink *LocationLink `json:"locationLink,omitempty"`
	Canonical    *Location     `json:"canonical,omitempty"`
	FuncStart    int           `json:"func_start,omitempty"`
	FuncEnd      int           `json:"func_end,omitempty"`
}

type CompactLocation struct {
	Line      int `json:"line"`
	Col       int `json:"col"`
	FuncStart int `json:"func_start,omitempty"`
	FuncEnd   int `json:"func_end,omitempty"`
}

type GroupedLocationResult struct {
	Data      map[string][]CompactLocation `json:"data"`
	Total     int                          `json:"total"`
	Showing   int                          `json:"showing"`
	Truncated bool                         `json:"truncated,omitempty"`
	Hint      string                       `json:"hint,omitempty"`
}

type WorkspaceSymbolResult struct {
	SymbolInformation *SymbolInformation `json:"symbolInformation,omitempty"`
	WorkspaceSymbol   *WorkspaceSymbol   `json:"workspaceSymbol,omitempty"`
}

type CodeActionResult struct {
	CodeAction *CodeAction `json:"codeAction,omitempty"`
	Command    *Command    `json:"command,omitempty"`
}

type CallHierarchyResult struct {
	Item     CallHierarchyItem           `json:"item"`
	Incoming []CallHierarchyIncomingCall `json:"incoming,omitempty"`
	Outgoing []CallHierarchyOutgoingCall `json:"outgoing,omitempty"`
}

type TypeHierarchyResult struct {
	Item       TypeHierarchyItem   `json:"item"`
	Supertypes []TypeHierarchyItem `json:"supertypes,omitempty"`
	Subtypes   []TypeHierarchyItem `json:"subtypes,omitempty"`
}

type DecodedSemanticToken struct {
	Line           int      `json:"line"`
	StartCharacter int      `json:"startCharacter"`
	Length         int      `json:"length"`
	TokenType      string   `json:"tokenType"`
	TokenModifiers []string `json:"tokenModifiers,omitempty"`
}

type SemanticTokensResult struct {
	ResultID string                 `json:"resultId,omitempty"`
	Data     []int                  `json:"data,omitempty"`
	Decoded  []DecodedSemanticToken `json:"decoded,omitempty"`
}

// PrimaryLocation 处理primary位置。
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

// MarshalJSON 编码JSON。
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

// HasFuncRange 判断func范围是否可用。
func (r LocationResult) HasFuncRange() bool {
	return r.FuncStart > 0 && r.FuncEnd >= r.FuncStart
}

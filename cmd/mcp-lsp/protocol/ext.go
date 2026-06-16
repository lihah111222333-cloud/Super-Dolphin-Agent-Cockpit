package protocol

import "encoding/json"

const (
	XRefResultLimit          = 50
	SemanticTokenResultLimit = 200
)

// WorkspaceFolder describes one workspace root announced during LSP
// initialization.
type WorkspaceFolder struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
}

// InitializeParams is the client-to-server initialize request payload.
type InitializeParams struct {
	ProcessID             int                `json:"processId"`
	RootURI               string             `json:"rootUri,omitempty"`
	Capabilities          ClientCapabilities `json:"capabilities"`
	WorkspaceFolders      []WorkspaceFolder  `json:"workspaceFolders,omitempty"`
	InitializationOptions any                `json:"initializationOptions,omitempty"`
}

// ClientCapabilities groups client features advertised to an LSP server.
type ClientCapabilities struct {
	TextDocument *TextDocumentClientCapabilities `json:"textDocument,omitempty"`
	Workspace    *WorkspaceClientCapability      `json:"workspace,omitempty"`
}

// WorkspaceClientCapability records workspace-level LSP client support.
type WorkspaceClientCapability struct {
	WorkspaceFolders bool `json:"workspaceFolders,omitempty"`
}

// DynamicRegistrationCapability records whether a capability supports dynamic
// registration.
type DynamicRegistrationCapability struct {
	DynamicRegistration bool `json:"dynamicRegistration,omitempty"`
}

// TextDocumentClientCapabilities groups text-document LSP capabilities this
// sidecar advertises.
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

// DocumentSymbolCapability records document-symbol feature support.
type DocumentSymbolCapability struct {
	DynamicRegistration               bool `json:"dynamicRegistration,omitempty"`
	HierarchicalDocumentSymbolSupport bool `json:"hierarchicalDocumentSymbolSupport,omitempty"`
}

// PublishDiagnosticsCapability records diagnostics notification support.
type PublishDiagnosticsCapability struct {
	RelatedInformation bool `json:"relatedInformation,omitempty"`
}

// HoverCapability records hover result formats the client can consume.
type HoverCapability struct {
	ContentFormat []string `json:"contentFormat,omitempty"`
}

// RenameClientCapability records rename preparation support.
type RenameClientCapability struct {
	PrepareSupport bool `json:"prepareSupport,omitempty"`
}

// SemanticTokensCapability records semantic-token formats and legends accepted
// by the client.
type SemanticTokensCapability struct {
	DynamicRegistration bool                              `json:"dynamicRegistration,omitempty"`
	Requests            *SemanticTokensRequestsCapability `json:"requests,omitempty"`
	TokenTypes          []string                          `json:"tokenTypes,omitempty"`
	TokenModifiers      []string                          `json:"tokenModifiers,omitempty"`
	Formats             []string                          `json:"formats,omitempty"`
}

// SemanticTokensRequestsCapability records range/full semantic-token request
// support.
type SemanticTokensRequestsCapability struct {
	Range any `json:"range,omitempty"`
	Full  any `json:"full,omitempty"`
}

// SemanticTokensFullRequestsCapability records full semantic-token delta
// support.
type SemanticTokensFullRequestsCapability struct {
	Delta bool `json:"delta,omitempty"`
}

// InitializeResult is the server-to-client initialize response payload.
type InitializeResult struct {
	Capabilities ServerCapabilities `json:"capabilities"`
}

// ServerCapabilities groups LSP server features detected during initialize.
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

// LocationResult normalizes location and location-link responses into a single
// result shape for tools.
type LocationResult struct {
	Location     *Location     `json:"location,omitempty"`
	LocationLink *LocationLink `json:"locationLink,omitempty"`
	Canonical    *Location     `json:"canonical,omitempty"`
	FuncStart    int           `json:"func_start,omitempty"`
	FuncEnd      int           `json:"func_end,omitempty"`
}

// CompactLocation is the compact model-facing location projection.
type CompactLocation struct {
	Line      int `json:"line"`
	Col       int `json:"col"`
	FuncStart int `json:"func_start,omitempty"`
	FuncEnd   int `json:"func_end,omitempty"`
}

// GroupedLocationResult groups compact locations by file and reports cap
// metadata.
type GroupedLocationResult struct {
	Data      map[string][]CompactLocation `json:"data"`
	Total     int                          `json:"total"`
	Showing   int                          `json:"showing"`
	Truncated bool                         `json:"truncated,omitempty"`
	Hint      string                       `json:"hint,omitempty"`
}

// WorkspaceSymbolResult represents either LSP workspace-symbol response shape.
type WorkspaceSymbolResult struct {
	SymbolInformation *SymbolInformation `json:"symbolInformation,omitempty"`
	WorkspaceSymbol   *WorkspaceSymbol   `json:"workspaceSymbol,omitempty"`
}

// CodeActionResult represents either a code action or command response item.
type CodeActionResult struct {
	CodeAction *CodeAction `json:"codeAction,omitempty"`
	Command    *Command    `json:"command,omitempty"`
}

// CallHierarchyResult groups prepared hierarchy item with incoming/outgoing
// calls.
type CallHierarchyResult struct {
	Item     CallHierarchyItem           `json:"item"`
	Incoming []CallHierarchyIncomingCall `json:"incoming,omitempty"`
	Outgoing []CallHierarchyOutgoingCall `json:"outgoing,omitempty"`
}

// TypeHierarchyResult groups prepared hierarchy item with related type items.
type TypeHierarchyResult struct {
	Item       TypeHierarchyItem   `json:"item"`
	Supertypes []TypeHierarchyItem `json:"supertypes,omitempty"`
	Subtypes   []TypeHierarchyItem `json:"subtypes,omitempty"`
}

// DecodedSemanticToken is a human-readable semantic-token entry decoded from
// LSP delta encoding.
type DecodedSemanticToken struct {
	Line           int      `json:"line"`
	StartCharacter int      `json:"startCharacter"`
	Length         int      `json:"length"`
	TokenType      string   `json:"tokenType"`
	TokenModifiers []string `json:"tokenModifiers,omitempty"`
}

// SemanticTokensResult carries raw and decoded semantic-token response data.
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

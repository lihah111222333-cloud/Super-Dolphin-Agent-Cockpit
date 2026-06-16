package protocol

// Position is a zero-based LSP document position.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range is an LSP half-open text range.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location identifies a document range by URI.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// LocationLink identifies a target range plus the origin selection that
// produced it.
type LocationLink struct {
	OriginSelectionRange *Range `json:"originSelectionRange,omitempty"`
	TargetURI            string `json:"targetUri"`
	TargetRange          Range  `json:"targetRange"`
	TargetSelectionRange Range  `json:"targetSelectionRange"`
}

// TextDocumentIdentifier identifies an open or addressable text document.
type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

// TextDocumentItem is the full text payload sent on didOpen.
type TextDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

// VersionedTextDocumentIdentifier identifies a document at a concrete version.
type VersionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
}

// OptionalVersionedTextDocumentIdentifier identifies a document when the server
// may omit a version.
type OptionalVersionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version *int   `json:"version,omitempty"`
}

// TextDocumentPositionParams is the common LSP payload for cursor-position
// requests.
type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// DidOpenTextDocumentParams is the payload for textDocument/didOpen.
type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

// DidCloseTextDocumentParams is the payload for textDocument/didClose.
type DidCloseTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// DidChangeTextDocumentParams is the payload for textDocument/didChange.
type DidChangeTextDocumentParams struct {
	TextDocument   VersionedTextDocumentIdentifier  `json:"textDocument"`
	ContentChanges []TextDocumentContentChangeEvent `json:"contentChanges"`
}

// TextDocumentContentChangeEvent represents one text replacement from an LSP
// change notification.
type TextDocumentContentChangeEvent struct {
	Range       *Range `json:"range,omitempty"`
	RangeLength *int   `json:"rangeLength,omitempty"`
	Text        string `json:"text"`
}

// DiagnosticSeverity is the numeric severity enum used by LSP diagnostics.
type DiagnosticSeverity int

const (
	SeverityError DiagnosticSeverity = iota + 1
	SeverityWarning
	SeverityInformation
	SeverityHint
)

var diagnosticSeverityNames = [...]string{"", "error", "warning", "info", "hint"}

// String 返回字符串表示。
func (s DiagnosticSeverity) String() string {
	i := int(s)
	if i > 0 && i < len(diagnosticSeverityNames) {
		return diagnosticSeverityNames[i]
	}
	return "unknown"
}

// DiagnosticRelatedInformation links a diagnostic to related source context.
type DiagnosticRelatedInformation struct {
	Location Location `json:"location"`
	Message  string   `json:"message"`
}

// Diagnostic is one LSP diagnostic item.
type Diagnostic struct {
	Range              Range                          `json:"range"`
	Severity           DiagnosticSeverity             `json:"severity,omitempty"`
	Source             string                         `json:"source,omitempty"`
	Message            string                         `json:"message"`
	Code               any                            `json:"code,omitempty"`
	RelatedInformation []DiagnosticRelatedInformation `json:"relatedInformation,omitempty"`
}

// PublishDiagnosticsParams is the payload for textDocument/publishDiagnostics.
type PublishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Version     *int         `json:"version,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// MarkupContent is an LSP markdown/plaintext content container.
type MarkupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// HoverResult is the response payload for textDocument/hover.
type HoverResult struct {
	Contents any    `json:"contents"`
	Range    *Range `json:"range,omitempty"`
}

// ReferenceContext controls whether declaration locations are included.
type ReferenceContext struct {
	IncludeDeclaration bool `json:"includeDeclaration"`
}

// ReferenceParams is the payload for textDocument/references.
type ReferenceParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	Context      ReferenceContext       `json:"context"`
}

// SymbolKind is the LSP symbol kind enum.
type SymbolKind int

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

// DocumentSymbol is a hierarchical symbol returned by documentSymbol.
type DocumentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           SymbolKind       `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}

// SymbolInformation is the flat symbol shape used by older LSP symbol
// responses.
type SymbolInformation struct {
	Name          string     `json:"name"`
	Kind          SymbolKind `json:"kind"`
	Location      Location   `json:"location"`
	ContainerName string     `json:"containerName,omitempty"`
}

// WorkspaceSymbolLocation is the lightweight workspace-symbol location shape.
type WorkspaceSymbolLocation struct {
	URI string `json:"uri"`
}

// WorkspaceSymbol is a workspace symbol response item.
type WorkspaceSymbol struct {
	Name          string `json:"name"`
	Kind          int    `json:"kind"`
	Location      any    `json:"location,omitempty"`
	ContainerName string `json:"containerName,omitempty"`
	Data          any    `json:"data,omitempty"`
}

// WorkspaceSymbolParams is the payload for workspace/symbol.
type WorkspaceSymbolParams struct {
	Query string `json:"query"`
}

// CompletionItem is one completion candidate.
type CompletionItem struct {
	Label         string `json:"label"`
	Kind          int    `json:"kind,omitempty"`
	Detail        string `json:"detail,omitempty"`
	Documentation any    `json:"documentation,omitempty"`
	InsertText    string `json:"insertText,omitempty"`
	SortText      string `json:"sortText,omitempty"`
	FilterText    string `json:"filterText,omitempty"`
}

// CompletionList is the response payload for textDocument/completion.
type CompletionList struct {
	IsIncomplete bool             `json:"isIncomplete"`
	Items        []CompletionItem `json:"items"`
}

// FormattingOptions describes indentation settings for formatting requests.
type FormattingOptions struct {
	TabSize      int  `json:"tabSize"`
	InsertSpaces bool `json:"insertSpaces"`
}

// TextEdit is one replacement edit in LSP coordinates.
type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

// TextDocumentEdit groups edits for one optional-version document.
type TextDocumentEdit struct {
	TextDocument OptionalVersionedTextDocumentIdentifier `json:"textDocument"`
	Edits        []TextEdit                              `json:"edits"`
}

// WorkspaceEdit is an edit set across one or more documents.
type WorkspaceEdit struct {
	Changes         map[string][]TextEdit `json:"changes,omitempty"`
	DocumentChanges []TextDocumentEdit    `json:"documentChanges,omitempty"`
}

// Command describes an executable LSP command.
type Command struct {
	Title     string `json:"title"`
	Command   string `json:"command"`
	Arguments []any  `json:"arguments,omitempty"`
}

// CodeActionContext carries diagnostics and filters for code-action requests.
type CodeActionContext struct {
	Diagnostics []Diagnostic `json:"diagnostics"`
	Only        []string     `json:"only,omitempty"`
	TriggerKind int          `json:"triggerKind,omitempty"`
}

// CodeAction is one code action returned by an LSP server.
type CodeAction struct {
	Title       string         `json:"title"`
	Kind        string         `json:"kind,omitempty"`
	Diagnostics []Diagnostic   `json:"diagnostics,omitempty"`
	Edit        *WorkspaceEdit `json:"edit,omitempty"`
	Command     *Command       `json:"command,omitempty"`
	Data        any            `json:"data,omitempty"`
}

// CodeActionParams is the payload for textDocument/codeAction.
type CodeActionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Range        Range                  `json:"range"`
	Context      CodeActionContext      `json:"context"`
}

// RenameParams is the payload for textDocument/rename.
type RenameParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	NewName      string                 `json:"newName"`
}

// CallHierarchyItem identifies one item in LSP call hierarchy.
type CallHierarchyItem struct {
	Name           string `json:"name"`
	Kind           int    `json:"kind"`
	URI            string `json:"uri"`
	Range          Range  `json:"range"`
	SelectionRange Range  `json:"selectionRange"`
	Detail         string `json:"detail,omitempty"`
	Data           any    `json:"data,omitempty"`
}

// TypeHierarchyItem identifies one item in LSP type hierarchy.
type TypeHierarchyItem struct {
	Name           string `json:"name"`
	Kind           int    `json:"kind"`
	URI            string `json:"uri"`
	Range          Range  `json:"range"`
	SelectionRange Range  `json:"selectionRange"`
	Detail         string `json:"detail,omitempty"`
	Data           any    `json:"data,omitempty"`
}

// CallHierarchyIncomingCall records one incoming call edge.
type CallHierarchyIncomingCall struct {
	From       CallHierarchyItem `json:"from"`
	FromRanges []Range           `json:"fromRanges"`
}

// CallHierarchyOutgoingCall records one outgoing call edge.
type CallHierarchyOutgoingCall struct {
	To         CallHierarchyItem `json:"to"`
	FromRanges []Range           `json:"fromRanges"`
}

// SemanticTokensLegend maps semantic-token integer IDs to names.
type SemanticTokensLegend struct {
	TokenTypes     []string `json:"tokenTypes"`
	TokenModifiers []string `json:"tokenModifiers"`
}

// SemanticTokensOptions describes server semantic-token support.
type SemanticTokensOptions struct {
	Legend SemanticTokensLegend `json:"legend"`
}

// SemanticTokens is the raw semantic-token response payload.
type SemanticTokens struct {
	ResultID string `json:"resultId,omitempty"`
	Data     []int  `json:"data"`
}

// FoldingRange is one foldable document range.
type FoldingRange struct {
	StartLine      int    `json:"startLine"`
	StartCharacter *int   `json:"startCharacter,omitempty"`
	EndLine        int    `json:"endLine"`
	EndCharacter   *int   `json:"endCharacter,omitempty"`
	Kind           string `json:"kind,omitempty"`
	CollapsedText  string `json:"collapsedText,omitempty"`
}

// SignatureInformationResult describes one callable signature.
type SignatureInformationResult struct {
	Label         string                       `json:"label"`
	Documentation any                          `json:"documentation,omitempty"`
	Parameters    []ParameterInformationResult `json:"parameters,omitempty"`
}

// ParameterInformationResult describes one callable parameter.
type ParameterInformationResult struct {
	Label         string `json:"label,omitempty"`
	LabelOffsets  []int  `json:"labelOffsets,omitempty"`
	Documentation any    `json:"documentation,omitempty"`
}

// SignatureHelpResult is the response payload for textDocument/signatureHelp.
type SignatureHelpResult struct {
	Signatures      []SignatureInformationResult `json:"signatures,omitempty"`
	ActiveSignature *int                         `json:"activeSignature,omitempty"`
	ActiveParameter *int                         `json:"activeParameter,omitempty"`
}

type itemRequest[T any] struct {
	Item T `json:"item"`
}

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

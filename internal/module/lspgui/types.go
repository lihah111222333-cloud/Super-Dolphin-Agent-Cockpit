package lspgui

type fileParams struct {
	Action   string `json:"action"`
	FilePath string `json:"file_path,omitempty"`
	Offset   int    `json:"offset,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

type grepParams struct {
	Action        string `json:"action"`
	Query         string `json:"query,omitempty"`
	Symbol        string `json:"symbol,omitempty"`
	Path          string `json:"path,omitempty"`
	Glob          string `json:"glob,omitempty"`
	Regex         bool   `json:"regex,omitempty"`
	CaseSensitive bool   `json:"case_sensitive,omitempty"`
	MaxResults    int    `json:"max_results,omitempty"`
	Language      string `json:"language,omitempty"`
}

type structureParams struct {
	Action     string `json:"action"`
	FilePath   string `json:"file_path,omitempty"`
	Query      string `json:"query,omitempty"`
	Language   string `json:"language,omitempty"`
	MaxResults int    `json:"max_results,omitempty"`
	Verbosity  string `json:"verbosity,omitempty"`
}

type inspectParams struct {
	Action   string `json:"action"`
	FilePath string `json:"file_path,omitempty"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
}

type xrefParams struct {
	Action             string `json:"action"`
	FilePath           string `json:"file_path,omitempty"`
	Line               int    `json:"line,omitempty"`
	Column             int    `json:"column,omitempty"`
	Direction          string `json:"direction,omitempty"`
	IncludeDeclaration bool   `json:"include_declaration,omitempty"`
	MaxResults         int    `json:"max_results,omitempty"`
	Verbosity          string `json:"verbosity,omitempty"`
}

type fileReadResult struct {
	Content    string `json:"content"`
	FilePath   string `json:"file_path,omitempty"`
	Offset     int    `json:"offset,omitempty"`
	Limit      int    `json:"limit,omitempty"`
	TotalLines int    `json:"total_lines,omitempty"`
}

type fileStatusResult struct {
	FilePath string `json:"file_path,omitempty"`
	Opened   bool   `json:"opened,omitempty"`
}

type diagnosticsResult struct {
	Diagnostics []any `json:"diagnostics"`
}

type searchResult struct {
	Results []searchMatch `json:"results"`
	Status  string        `json:"status,omitempty"`
	Stub    bool          `json:"stub,omitempty"`
}

type searchMatch struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
	Text string `json:"text"`
}

type symbolsResult struct {
	Symbols []any  `json:"symbols"`
	Status  string `json:"status,omitempty"`
	Stub    bool   `json:"stub,omitempty"`
}

type referencesResult struct {
	References []searchMatch `json:"references"`
	Status     string        `json:"status,omitempty"`
	Stub       bool          `json:"stub,omitempty"`
}

type hoverResult struct {
	Contents string `json:"contents"`
	Status   string `json:"status,omitempty"`
	Stub     bool   `json:"stub,omitempty"`
}

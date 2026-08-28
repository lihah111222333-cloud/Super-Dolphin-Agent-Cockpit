package tools

// diagnosticsInput 是一级 diagnostics 工具的唯一外部入参。
// 文件读取和 action 分发已经从 MCP-LSP 工具面删除，内部诊断流程只接收目标路径。
type diagnosticsInput struct {
	FilePath     string                      `json:"file_path,omitempty"`
	FilePaths    []string                    `json:"file_paths,omitempty"`
	LanguageID   string                      `json:"language_id,omitempty"`
	ChangedLines []int                       `json:"changed_lines,omitempty"`
	DiffFilter   *diagnosticsDiffFilterInput `json:"diff_filter,omitempty"`
}

// diagnosticsDiffFilterInput 是显式差量过滤模式的外部 schema。
// changed_lines 使用目标路径到一基行号列表的映射，避免批量请求把一组行号错误应用到所有文件。
type diagnosticsDiffFilterInput struct {
	Mode         string           `json:"mode"`
	ChangedLines map[string][]int `json:"changed_lines"`
}

// diagnosticsFilterRequest 是验证后的差量过滤请求；路径在目标收集完成后再解析为 URI。
type diagnosticsFilterRequest struct {
	mode               string
	legacyChangedLines []int
	changedLinesByPath map[string][]int
}

// diagnosticsTable 是按文件分组的诊断响应表。
type diagnosticsTable struct {
	File string   `json:"file"`
	Cols []string `json:"cols"`
	Rows [][]any  `json:"rows"`
}

// diagnosticsFilteredEntry 记录被显式差量过滤的单条诊断，确保过滤不会伪装成 clean。
type diagnosticsFilteredEntry struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Severity string `json:"severity"`
	Source   string `json:"source,omitempty"`
	Code     string `json:"code,omitempty"`
	Message  string `json:"message,omitempty"`
}

// diagnosticsFilterMeta 是差量过滤的可审计摘要，FilteredDiagnostics 保留每条被省略诊断的 code/severity。
type diagnosticsFilterMeta struct {
	Mode                string                     `json:"mode"`
	Filtered            int                        `json:"filtered"`
	FilteredDiagnostics []diagnosticsFilteredEntry `json:"filtered_diagnostics"`
}

// diagnosticsMeta 是 diagnostics 响应的审计元信息。
type diagnosticsMeta struct {
	Message    string                 `json:"message,omitempty"`
	DiffFilter *diagnosticsFilterMeta `json:"diff_filter,omitempty"`
}

// diagnosticsResponse 是 diagnostics producer 的结构化响应。
type diagnosticsResponse struct {
	Success   bool               `json:"success"`
	Data      []diagnosticsTable `json:"data"`
	Total     int                `json:"total"`
	Showing   int                `json:"showing"`
	Truncated bool               `json:"truncated,omitempty"`
	Hint      string             `json:"hint,omitempty"`
	Meta      diagnosticsMeta    `json:"meta"`
}

// diagnosticsWaitResult 描述诊断启动等待是否经历恢复或部分恢复。
type diagnosticsWaitResult struct {
	recovered bool
	partial   bool
	message   string
}

// diagnosticTarget 是经过 workspace containment 校验的诊断文件目标。
type diagnosticTarget struct {
	URI         string
	AbsPath     string
	DisplayPath string
}

// diagnosticsFilterSpec 是将显式差量行号绑定到目标 URI 后的内部过滤规格。
type diagnosticsFilterSpec struct {
	Mode              string
	ChangedLinesByURI map[string]map[int]struct{}
}

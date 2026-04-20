package skill

type pathParams struct {
	Path string `json:"path"`
}

type contentParams struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type listSkillFilesParams struct {
	Dir  string `json:"dir"`
	Path string `json:"path,omitempty"`
}

type importSkillDirParams struct {
	Path  string   `json:"path"`
	Paths []string `json:"paths,omitempty"`
	Name  string   `json:"name,omitempty"`
	CWD   string   `json:"cwd,omitempty"`
}

type deleteLocalSkillParams struct {
	Name string `json:"name"`
}

type skillConfigReadParams struct {
	AgentID string `json:"agent_id"`
}

type skillNamedContentParams struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

type skillSummaryWriteParams struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
}

type skillRemoteReadParams struct {
	URL string `json:"url"`
}

type UserInput struct {
	Type    string `json:"type"`
	Text    string `json:"text,omitempty"`
	URL     string `json:"url,omitempty"`
	Path    string `json:"path,omitempty"`
	Name    string `json:"name,omitempty"`
	Content string `json:"content,omitempty"`
}

type skillMatchPreviewParams struct {
	ThreadID string      `json:"threadId,omitempty"`
	AgentID  string      `json:"agent_id,omitempty"` // Falls back when threadId is empty.
	Text     string      `json:"text"`
	Input    []UserInput `json:"input,omitempty"`
}

type skillListParams struct{}

type skillListItem struct {
	Name                   string     `json:"name"`
	Summary                string     `json:"summary"`
	Description            string     `json:"description"`
	Trust                  TrustScope `json:"trust"`
	ContentHash            string     `json:"content_hash"`
	DisableModelInvocation bool       `json:"disable_model_invocation"`
}

type skillListResult struct {
	Skills []skillListItem `json:"skills"`
}

type skillExpandParams struct {
	Name     string `json:"name"`
	Section  string `json:"section,omitempty"`
	MaxBytes int64  `json:"max_bytes,omitempty"`
}

type skillExpandResult struct {
	Name        string     `json:"name"`
	Section     string     `json:"section"`
	Path        string     `json:"path"`
	Summary     string     `json:"summary"`
	Content     string     `json:"content"`
	Truncated   bool       `json:"truncated"`
	TotalBytes  int64      `json:"total_bytes"`
	ContentHash string     `json:"content_hash"`
	Trust       TrustScope `json:"trust"`
}

// ============================================================================
// P20.1 Phase 6 工具拆分：skill_expand_body / skill_read_resource
// ============================================================================

// ExpandBodyParams 是 skill_expand_body 工具的入参。
//
// P20.1 §3.1 语义拆分：该工具只负责读 SKILL.md body（可选按 Markdown H2/H3
// 锚点切片），不负责 resource 资源读取。
type ExpandBodyParams struct {
	Name     string `json:"name"`
	Anchor   string `json:"anchor,omitempty"`
	MaxBytes int64  `json:"max_bytes,omitempty"`
}

// ExpandBodyResult 是 skill_expand_body 的返回结构。
//
// 字段语义：
//   - Name：skill 名（规范化后）
//   - Path：SKILL.md 绝对路径，供诊断 / 日志
//   - Version：SKILL.md 内容 hash 的前 12 位（与 SkillRef.Version 语义对齐）
//   - Anchor：实际命中的锚点名；空 = 返回全文
//   - Summary：SKILL.md frontmatter 或 body 摘要
//   - Content：切片后 / 截断后的正文
//   - Truncated：是否因 MaxBytes 截断
//   - TotalBytes：原始切片的字节数（未截断时）
type ExpandBodyResult struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Version    string `json:"version,omitempty"`
	Anchor     string `json:"anchor,omitempty"`
	Summary    string `json:"summary,omitempty"`
	Content    string `json:"content"`
	Truncated  bool   `json:"truncated,omitempty"`
	TotalBytes int64  `json:"total_bytes"`
}

// ReadResourceParams 是 skill_read_resource 工具的入参。
//
// P20.1 §3.1 拆分后的专用入口：仅读取 skill 目录内的资源文件（references/、
// scripts/、assets/ 等相对路径）。路径经 NormalizeArtifactLocator 规范化，
// 不允许 `../` 逃逸。
type ReadResourceParams struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	MaxBytes int64  `json:"max_bytes,omitempty"`
}

// ReadResourceResult 是 skill_read_resource 的返回结构。
type ReadResourceResult struct {
	Name       string `json:"name"`
	SkillDir   string `json:"skill_dir"`
	Path       string `json:"path"`
	Version    string `json:"version,omitempty"`
	Content    string `json:"content"`
	Truncated  bool   `json:"truncated,omitempty"`
	TotalBytes int64  `json:"total_bytes"`
}

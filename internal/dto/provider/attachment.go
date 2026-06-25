package provider

// AttachmentKind* 是 AttachmentEnvelope.Kind 的合法枚举值。
const (
	AttachmentKindRelevantMemory = "relevant_memory" // 相关记忆片段。
	AttachmentKindNestedMemory   = "nested_memory"   // 嵌套记忆片段。
)

// AttachmentEnvelope 是随 turn 携带的附件信封，包含内容正文和元数据。
type AttachmentEnvelope struct {
	Kind      string `json:"kind,omitempty"`      // 附件类型，见 AttachmentKind* 常量。
	Path      string `json:"path"`                // 附件文件路径或标识符。
	Header    string `json:"header"`              // 附件在 prompt 中的展示标题。
	Content   string `json:"content"`             // 附件正文内容。
	MtimeMs   int64  `json:"mtimeMs,omitempty"`   // 文件最后修改时间（Unix 毫秒）。
	UpdatedAt string `json:"updatedAt,omitempty"` // 可读更新时间字符串。
	Limit     int    `json:"limit,omitempty"`     // 内容截断前的原始长度限制。
	Truncated bool   `json:"truncated,omitempty"` // 内容是否已被截断。
}

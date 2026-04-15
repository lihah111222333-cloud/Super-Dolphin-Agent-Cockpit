package provider

const (
	AttachmentKindRelevantMemory = "relevant_memory"
	AttachmentKindNestedMemory   = "nested_memory"
)

type AttachmentEnvelope struct {
	Kind      string `json:"kind,omitempty"`
	Path      string `json:"path"`
	Header    string `json:"header"`
	Content   string `json:"content"`
	MtimeMs   int64  `json:"mtimeMs,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

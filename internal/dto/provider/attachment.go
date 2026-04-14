package provider

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

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

type RelevantMemoryAttachment struct {
	AttachmentEnvelope
}

func NewRelevantMemoryAttachment(
	path, header, content string,
	updatedAt time.Time,
	limit int,
	truncated bool,
) RelevantMemoryAttachment {
	envelope := AttachmentEnvelope{
		Kind:      AttachmentKindRelevantMemory,
		Path:      normalizeAttachmentPath(path),
		Header:    strings.TrimSpace(header),
		Content:   strings.TrimSpace(content),
		Limit:     limit,
		Truncated: truncated,
	}
	if envelope.Limit < 0 {
		envelope.Limit = 0
	}
	if !updatedAt.IsZero() {
		envelope.MtimeMs = updatedAt.UnixMilli()
		envelope.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	}
	return RelevantMemoryAttachment{AttachmentEnvelope: envelope}
}

func (a RelevantMemoryAttachment) Envelope() AttachmentEnvelope {
	return a.AttachmentEnvelope.Normalize()
}

func (a AttachmentEnvelope) Normalize() AttachmentEnvelope {
	a.Kind = strings.TrimSpace(a.Kind)
	a.Path = normalizeAttachmentPath(a.Path)
	a.Header = strings.TrimSpace(a.Header)
	a.Content = strings.TrimSpace(a.Content)
	a.UpdatedAt = strings.TrimSpace(a.UpdatedAt)
	if a.Limit < 0 {
		a.Limit = 0
	}
	if a.MtimeMs < 0 {
		a.MtimeMs = 0
	}
	return a
}

func (a AttachmentEnvelope) IsValid() bool {
	a = a.Normalize()
	if a.Path == "" || a.Header == "" || a.Content == "" {
		return false
	}
	return a.MtimeMs > 0 || a.UpdatedAt != ""
}

func (a AttachmentEnvelope) DisplayName() string {
	a = a.Normalize()
	if a.Path == "" {
		return "attachment"
	}
	if base := strings.TrimSpace(filepath.Base(a.Path)); base != "" && base != "." {
		return base
	}
	return a.Path
}

func (a AttachmentEnvelope) RenderText() string {
	a = a.Normalize()
	if !a.IsValid() {
		return ""
	}
	lines := []string{"<attachment>"}
	if a.Kind != "" {
		lines = append(lines, "kind: "+a.Kind)
	}
	lines = append(lines, "path: "+a.Path)
	if a.MtimeMs > 0 {
		lines = append(lines, fmt.Sprintf("mtimeMs: %d", a.MtimeMs))
	}
	if a.UpdatedAt != "" {
		lines = append(lines, "updatedAt: "+a.UpdatedAt)
	}
	if a.Limit > 0 {
		lines = append(lines, fmt.Sprintf("limit: %d", a.Limit))
	}
	if a.Truncated {
		lines = append(lines, "truncated: true")
	}
	lines = append(lines, "", a.Header, a.Content, "</attachment>")
	return strings.Join(lines, "\n")
}

func normalizeAttachmentPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.ToSlash(path)
}

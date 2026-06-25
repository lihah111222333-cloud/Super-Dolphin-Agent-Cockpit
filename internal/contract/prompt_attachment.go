package contract

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

// NewRelevantMemoryAttachment 创建relevant记忆attachment。
func NewRelevantMemoryAttachment(
	path, header, content string,
	updatedAt time.Time,
	limit int,
	truncated bool,
) dto.AttachmentEnvelope {
	envelope := dto.AttachmentEnvelope{
		Kind:      dto.AttachmentKindRelevantMemory,
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
	return NormalizeAttachmentEnvelope(envelope)
}

// NormalizeAttachmentEnvelope 规范化attachment包装。
func NormalizeAttachmentEnvelope(attachment dto.AttachmentEnvelope) dto.AttachmentEnvelope {
	attachment.Kind = strings.TrimSpace(attachment.Kind)
	attachment.Path = normalizeAttachmentPath(attachment.Path)
	attachment.Header = strings.TrimSpace(attachment.Header)
	attachment.Content = strings.TrimSpace(attachment.Content)
	attachment.UpdatedAt = strings.TrimSpace(attachment.UpdatedAt)
	if attachment.Limit < 0 {
		attachment.Limit = 0
	}
	if attachment.MtimeMs < 0 {
		attachment.MtimeMs = 0
	}
	return attachment
}

// IsValidAttachmentEnvelope 判断validattachment包装是否可用。
func IsValidAttachmentEnvelope(attachment dto.AttachmentEnvelope) bool {
	attachment = NormalizeAttachmentEnvelope(attachment)
	if attachment.Path == "" || attachment.Header == "" || attachment.Content == "" {
		return false
	}
	return attachment.MtimeMs > 0 || attachment.UpdatedAt != ""
}

// AttachmentDisplayName 处理attachment显示名称。
func AttachmentDisplayName(attachment dto.AttachmentEnvelope) string {
	attachment = NormalizeAttachmentEnvelope(attachment)
	if attachment.Path == "" {
		return "attachment"
	}
	if base := strings.TrimSpace(filepath.Base(attachment.Path)); base != "" && base != "." {
		return base
	}
	return attachment.Path
}

// RenderAttachmentText 渲染attachment文本。
func RenderAttachmentText(attachment dto.AttachmentEnvelope) string {
	attachment = NormalizeAttachmentEnvelope(attachment)
	if !IsValidAttachmentEnvelope(attachment) {
		return ""
	}
	lines := []string{"<attachment>"}
	if attachment.Kind != "" {
		lines = append(lines, "kind: "+attachment.Kind)
	}
	lines = append(lines, "path: "+attachment.Path)
	if attachment.MtimeMs > 0 {
		lines = append(lines, fmt.Sprintf("mtimeMs: %d", attachment.MtimeMs))
	}
	if attachment.UpdatedAt != "" {
		lines = append(lines, "updatedAt: "+attachment.UpdatedAt)
	}
	if attachment.Limit > 0 {
		lines = append(lines, fmt.Sprintf("limit: %d", attachment.Limit))
	}
	if attachment.Truncated {
		lines = append(lines, "truncated: true")
	}
	lines = append(lines, "", attachment.Header, attachment.Content, "</attachment>")
	return strings.Join(lines, "\n")
}

// normalizeAttachmentPath 规范化附件路径：trim 后转换为 slash 风格。
func normalizeAttachmentPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.ToSlash(path)
}

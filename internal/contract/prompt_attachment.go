package contract

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

// NewRelevantMemoryAttachment 构造 relevant memory 的 provider attachment。
// limit 负数会收敛为 0，updatedAt 非零时同时写入毫秒和 RFC3339 字符串，保持 provider wire 兼容。
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

// NormalizeAttachmentEnvelope 清理 attachment wire 字段。
// 路径转为 slash 风格，文本字段 trim，负数 limit/mtime 归零，避免下游 prompt 渲染收到非法值。
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

// IsValidAttachmentEnvelope 校验 attachment 是否足够安全地进入 prompt。
// path、header、content 必须非空，且至少有一种时间字段用于解释来源新鲜度。
func IsValidAttachmentEnvelope(attachment dto.AttachmentEnvelope) bool {
	attachment = NormalizeAttachmentEnvelope(attachment)
	if attachment.Path == "" || attachment.Header == "" || attachment.Content == "" {
		return false
	}
	return attachment.MtimeMs > 0 || attachment.UpdatedAt != ""
}

// AttachmentDisplayName 生成 attachment 的 UI 展示名。
// 优先使用路径 basename；空路径或无有效 basename 时返回稳定占位名。
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

// RenderAttachmentText 将 attachment 渲染为 provider prompt 文本块。
// 无效 attachment 返回空串，调用方可直接跳过而不生成半截标签。
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

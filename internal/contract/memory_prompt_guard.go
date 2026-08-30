package contract

import (
	"errors"
	"fmt"
	"strings"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

// ErrLegacyMemoryPromptInjection 表示旧 V3 memory prompt 路径已被 provider-native owner 接管。
// 调用方必须过滤或阻断该载荷，不能把它静默当作普通 prompt 内容继续下发。
var ErrLegacyMemoryPromptInjection = errors.New("legacy memory prompt injection is disabled")

// IsLegacyMemoryPromptSection 判断 section 名称是否属于已退役的 V3 memory 注入槽位。
// 比较按大小写不敏感处理，避免旧客户端通过大小写变体绕过边界。
func IsLegacyMemoryPromptSection(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case DynamicSectionMemory,
		DynamicSectionMemoryEntrypoint,
		DynamicSectionMemoryContext,
		dto.AttachmentKindRelevantMemory,
		dto.AttachmentKindNestedMemory:
		return true
	default:
		return false
	}
}

// FilterLegacyMemoryPromptSections 移除旧 memory section，保留普通 prompt section 的顺序和内容。
// 返回副本，调用方可以安全修改结果而不影响输入切片。
func FilterLegacyMemoryPromptSections(sections []ResolvedPromptSection) []ResolvedPromptSection {
	if len(sections) == 0 {
		return nil
	}
	out := make([]ResolvedPromptSection, 0, len(sections))
	for _, section := range sections {
		if IsLegacyMemoryPromptSection(section.Name) {
			continue
		}
		out = append(out, section)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ValidateLegacyMemoryPromptSections 拒绝仍携带旧 memory section 的 prompt assembly。
// 该校验用于不能安全重建正文的生命周期边界（start/resume/fork/restart）。
func ValidateLegacyMemoryPromptSections(sections []ResolvedPromptSection) error {
	for _, section := range sections {
		if IsLegacyMemoryPromptSection(section.Name) {
			return fmt.Errorf("%w: section %q", ErrLegacyMemoryPromptInjection, strings.TrimSpace(section.Name))
		}
	}
	return nil
}

// ValidateLegacyMemoryPromptSnapshot 拒绝快照中仍持久化的旧 memory section 名称。
// 仅检查结构化 section 名称；正文已被压入 base/boundary 时无法可靠反向解析，调用方应
// 在生成快照前先过滤 ResolvedSections，并在生命周期边界拒绝带有这些名称的旧快照。
func ValidateLegacyMemoryPromptSnapshot(snapshot PromptAssemblySnapshot) error {
	for name := range snapshot.SectionSnapshot {
		if IsLegacyMemoryPromptSection(name) {
			return fmt.Errorf("%w: snapshot section %q", ErrLegacyMemoryPromptInjection, strings.TrimSpace(name))
		}
	}
	return nil
}

// IsLegacyMemoryAttachmentKind 判断附件是否由旧 V3 memory runtime 生成。
func IsLegacyMemoryAttachmentKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case DynamicSectionMemory,
		DynamicSectionMemoryEntrypoint,
		DynamicSectionMemoryContext,
		dto.AttachmentKindRelevantMemory,
		dto.AttachmentKindNestedMemory:
		return true
	default:
		return false
	}
}

// FilterLegacyMemoryAttachments 移除旧 memory 附件并保留普通附件。
// 返回副本，避免 provider 侧在过滤时修改调用方持有的底层数组。
func FilterLegacyMemoryAttachments(attachments []dto.AttachmentEnvelope) []dto.AttachmentEnvelope {
	if len(attachments) == 0 {
		return nil
	}
	out := make([]dto.AttachmentEnvelope, 0, len(attachments))
	for _, attachment := range attachments {
		if IsLegacyMemoryAttachmentKind(attachment.Kind) {
			continue
		}
		out = append(out, attachment)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

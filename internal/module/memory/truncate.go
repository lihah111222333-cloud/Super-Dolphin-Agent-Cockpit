package memory

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	parse "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/memory/parse"
)

const (
	entrypointMaxLines     = 200
	entrypointMaxCodeUnits = 25_000
)

// EntrypointTruncation 描述 MEMORY.md 入口文件被加载和截断后的结果。
// LineCount/CodeUnitCount 保留原始规模，Warning 写回 prompt 以提示用户拆分过大的入口索引。
type EntrypointTruncation struct {
	Content          string
	LineCount        int
	CodeUnitCount    int
	WasLineTruncated bool
	WasByteTruncated bool
	Warning          string
}

// TruncateEntrypointContent 按行数和 JS code unit 双上限裁剪入口记忆。
// 超限时在返回内容末尾追加警告，避免模型误以为完整 MEMORY.md 已注入。
func TruncateEntrypointContent(raw string) EntrypointTruncation {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return EntrypointTruncation{}
	}

	contentLines := strings.Split(trimmed, "\n")
	result := EntrypointTruncation{
		Content:       trimmed,
		LineCount:     len(contentLines),
		CodeUnitCount: parse.JSStringLength(trimmed),
	}
	result.WasLineTruncated = result.LineCount > entrypointMaxLines
	result.WasByteTruncated = result.CodeUnitCount > entrypointMaxCodeUnits
	if !result.WasLineTruncated && !result.WasByteTruncated {
		return result
	}

	truncated := trimmed
	if result.WasLineTruncated {
		truncated = strings.Join(contentLines[:entrypointMaxLines], "\n")
	}
	if parse.JSStringLength(truncated) > entrypointMaxCodeUnits {
		truncated = parse.TruncateAtCodeUnitLimit(truncated, entrypointMaxCodeUnits)
	}
	result.Warning = "MEMORY.md is " + truncateEntrypointReason(result) + ". Only part of it was loaded. Keep index entries to one line under ~200 chars; move detail into topic files."
	result.Content = truncated + "\n\n> WARNING: " + result.Warning
	return result
}

// truncateEntrypointReason 生成人类可读的截断原因，区分行数过多、单行过长和双重超限。
func truncateEntrypointReason(result EntrypointTruncation) string {
	switch {
	case result.WasByteTruncated && !result.WasLineTruncated:
		return fmt.Sprintf("%s (limit: %s) — index entries are too long", formatEntrypointSize(result.CodeUnitCount), formatEntrypointSize(entrypointMaxCodeUnits))
	case result.WasLineTruncated && !result.WasByteTruncated:
		return fmt.Sprintf("%d lines (limit: %d)", result.LineCount, entrypointMaxLines)
	default:
		return fmt.Sprintf("%d lines and %s", result.LineCount, formatEntrypointSize(result.CodeUnitCount))
	}
}

// formatEntrypointSize 将 code unit 数格式化为近似字节大小，只用于用户可见的超限提示。
func formatEntrypointSize(size int) string {
	kb := float64(size) / 1024
	if kb < 1 {
		return fmt.Sprintf("%d bytes", size)
	}
	if kb < 1024 {
		return strings.TrimSuffix(fmt.Sprintf("%.1f", kb), ".0") + "KB"
	}
	mb := kb / 1024
	if mb < 1024 {
		return strings.TrimSuffix(fmt.Sprintf("%.1f", mb), ".0") + "MB"
	}
	gb := mb / 1024
	return strings.TrimSuffix(fmt.Sprintf("%.1f", gb), ".0") + "GB"
}

var ErrForbiddenMemoryContent = errors.New("forbidden memory content")

// MemoryContentValidationError 记录持久记忆内容命中的规则，Error 文案只暴露规则原因而不带原文片段。
type MemoryContentValidationError struct {
	RuleID string
	Reason string
}

// MemoryContentValidator 校验 durable memory 是否包含不应长期保存的敏感或可派生内容。
type MemoryContentValidator struct{}

// memoryContentPattern 是持久记忆禁写规则，RuleID 用于测试和 UI 分类，Regex 只匹配归一化后的文本。
type memoryContentPattern struct {
	RuleID string
	Reason string
	Regex  *regexp.Regexp
}

var forbiddenMemoryContentPatterns = []memoryContentPattern{
	{
		RuleID: "derivable_codebase",
		Reason: "code patterns, architecture, file paths, and project structure should be derived from the current repository state, not durable memory",
		Regex:  regexp.MustCompile(`(?:^|\n)\s*(?:code patterns?|code structure|coding conventions?|naming conventions?|project structure|repository structure|repository layout|repo structure|repo layout|folder structure|architecture|file paths?)\s*:`),
	},
	{
		RuleID: "derivable_git",
		Reason: "git history, recent changes, PR lists, and activity summaries should come from current git state, not durable memory",
		Regex:  regexp.MustCompile(`(?:^|\n)\s*(?:git history|recent changes|who[- ]changed[- ]what|git blame|git log|pr list|activity summary|activity log)\s*:`),
	},
	{
		RuleID: "temporary_tasks",
		Reason: "temporary task details, task lists, and in-progress state belong in plans or tasks, not durable memory",
		Regex:  regexp.MustCompile(`(?:^|\n)\s*(?:in[- ]progress|current conversation(?: task list)?|temporary state|next steps?|progress tracker|task list|current task|working on right now|currently implementing|todo)\s*:`),
	},
	{
		RuleID: "debug_recipe",
		Reason: "debugging solutions and fix recipes should live in code, commits, or runbooks, not durable memory",
		Regex:  regexp.MustCompile(`(?:^|\n)\s*(?:debug(?:ging)? (?:recipes?|solutions?|steps?)|fix recipe|reproduction steps?|repro steps?|workaround|set a breakpoint|stack trace)\s*:`),
	},
}

var defaultMemoryContentValidator = NewMemoryContentValidator()

// NewMemoryContentValidator 创建无状态内容校验器，便于测试替换默认规则调用入口。
func NewMemoryContentValidator() *MemoryContentValidator {
	return &MemoryContentValidator{}
}

// Error 返回 UI/RPC 可展示的禁写原因；nil 或空原因统一退回哨兵错误文本。
func (e *MemoryContentValidationError) Error() string {
	if e == nil || strings.TrimSpace(e.Reason) == "" {
		return ErrForbiddenMemoryContent.Error()
	}
	return fmt.Sprintf("%s: %s", ErrForbiddenMemoryContent, strings.TrimSpace(e.Reason))
}

// Unwrap 暴露 ErrForbiddenMemoryContent，供调用方用 errors.Is 识别禁写类别。
func (e *MemoryContentValidationError) Unwrap() error {
	return ErrForbiddenMemoryContent
}

// ValidateMemoryEntryContent 使用默认校验器检查单条记忆，写入路径必须在落盘前调用。
func ValidateMemoryEntryContent(entry MemoryEntry) error {
	return defaultMemoryContentValidator.Validate(entry)
}

// Validate 同时执行密钥扫描和 durable-memory 禁写规则，命中任一规则都会 fail-fast 阻断保存。
func (v *MemoryContentValidator) Validate(entry MemoryEntry) error {
	if err := validateMemorySecrets(entry); err != nil {
		return err
	}
	corpus := memoryValidationCorpus(entry)
	for _, pattern := range forbiddenMemoryContentPatterns {
		if pattern.Regex.MatchString(corpus) {
			return &MemoryContentValidationError{RuleID: pattern.RuleID, Reason: pattern.Reason}
		}
	}
	return nil
}

// validateMemorySecrets 复用团队记忆密钥扫描规则检查 durable memory，命中时只返回规则 ID 不回显秘密。
func validateMemorySecrets(entry MemoryEntry) error {
	findings := ScanTeamMemContent(memoryValidationRawText(entry))
	if len(findings) == 0 {
		return nil
	}
	reason := "secrets, credentials, or tokens do not belong in durable memory"
	if first := strings.TrimSpace(findings[0].RuleID); first != "" {
		reason += " (" + first + ")"
	}
	return &MemoryContentValidationError{RuleID: "secrets", Reason: reason}
}

// memoryValidationRawText 合并名称、描述和正文作为扫描输入，避免秘密藏在 frontmatter 字段中。
func memoryValidationRawText(entry MemoryEntry) string {
	parts := []string{
		strings.TrimSpace(entry.Frontmatter.Name),
		strings.TrimSpace(entry.Frontmatter.Description),
		strings.TrimSpace(entry.Content),
	}
	return strings.TrimSpace(strings.Join(nonEmpty(parts), "\n"))
}

// memoryValidationCorpus 将扫描输入转为小写、压缩空白的逐行语料，稳定匹配可派生内容规则。
func memoryValidationCorpus(entry MemoryEntry) string {
	lines := make([]string, 0, 4)
	for line := range strings.SplitSeq(memoryValidationRawText(entry), "\n") {
		line = strings.ToLower(strings.Join(strings.Fields(line), " "))
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

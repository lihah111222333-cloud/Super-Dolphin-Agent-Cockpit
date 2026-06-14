package memory

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	parse "github.com/anthropic-ai/super-agent-v3/internal/module/memory/parse"
)

const (
	entrypointMaxLines     = 200
	entrypointMaxCodeUnits = 25_000
)

type EntrypointTruncation struct {
	Content          string
	LineCount        int
	CodeUnitCount    int
	WasLineTruncated bool
	WasByteTruncated bool
	Warning          string
}

// TruncateEntrypointContent 截断entrypoint内容。
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

type MemoryContentValidationError struct {
	RuleID string
	Reason string
}

type MemoryContentValidator struct{}

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

// NewMemoryContentValidator 创建记忆内容校验器。
func NewMemoryContentValidator() *MemoryContentValidator {
	return &MemoryContentValidator{}
}

// Error 返回错误文本。
func (e *MemoryContentValidationError) Error() string {
	if e == nil || strings.TrimSpace(e.Reason) == "" {
		return ErrForbiddenMemoryContent.Error()
	}
	return fmt.Sprintf("%s: %s", ErrForbiddenMemoryContent, strings.TrimSpace(e.Reason))
}

// Unwrap 返回底层错误。
func (e *MemoryContentValidationError) Unwrap() error {
	return ErrForbiddenMemoryContent
}

// ValidateMemoryEntryContent 校验记忆条目内容。
func ValidateMemoryEntryContent(entry MemoryEntry) error {
	return defaultMemoryContentValidator.Validate(entry)
}

// Validate 校验记忆。
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

func memoryValidationRawText(entry MemoryEntry) string {
	parts := []string{
		strings.TrimSpace(entry.Frontmatter.Name),
		strings.TrimSpace(entry.Frontmatter.Description),
		strings.TrimSpace(entry.Content),
	}
	return strings.TrimSpace(strings.Join(nonEmpty(parts), "\n"))
}

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

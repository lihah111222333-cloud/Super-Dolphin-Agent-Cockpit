package memory

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

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

func NewMemoryContentValidator() *MemoryContentValidator {
	return &MemoryContentValidator{}
}

func (e *MemoryContentValidationError) Error() string {
	if e == nil || strings.TrimSpace(e.Reason) == "" {
		return ErrForbiddenMemoryContent.Error()
	}
	return fmt.Sprintf("%s: %s", ErrForbiddenMemoryContent, strings.TrimSpace(e.Reason))
}

func (e *MemoryContentValidationError) Unwrap() error {
	return ErrForbiddenMemoryContent
}

func ValidateMemoryEntryContent(entry MemoryEntry) error {
	return defaultMemoryContentValidator.Validate(entry)
}

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
	for _, line := range strings.Split(memoryValidationRawText(entry), "\n") {
		line = strings.ToLower(strings.Join(strings.Fields(line), " "))
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

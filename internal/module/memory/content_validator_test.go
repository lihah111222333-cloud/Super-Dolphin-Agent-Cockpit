package memory

import (
	"errors"
	"testing"
)

func TestMemoryContentValidatorRejectsForbiddenContent(t *testing.T) {
	validator := NewMemoryContentValidator()
	tests := []struct {
		name    string
		ruleID  string
		content string
	}{
		{
			name:    "derivable codebase",
			ruleID:  "derivable_codebase",
			content: "Project structure: internal/module/memory handles durable memory and cmd/ exposes tools.",
		},
		{
			name:    "derivable git history",
			ruleID:  "derivable_git",
			content: "Recent changes: Alice merged PR #42 and Bob updated the parser yesterday.",
		},
		{
			name:    "temporary tasks",
			ruleID:  "temporary_tasks",
			content: "Current conversation task list: update the parser, run go test, and report status.",
		},
		{
			name:    "debug recipe",
			ruleID:  "debug_recipe",
			content: "Debugging steps: reproduce with --debug and set a breakpoint in service.go.",
		},
		{
			name:    "secrets",
			ruleID:  "secrets",
			content: "token = \"ghp_abcdefghijklmnopqrstuvwxyz1234567890AB\"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.Validate(testMemoryEntry("Forbidden", "forbidden", MemoryTypeProject, tt.content))
			if !errors.Is(err, ErrForbiddenMemoryContent) {
				t.Fatalf("Validate() error = %v, want %v", err, ErrForbiddenMemoryContent)
			}
			var validationErr *MemoryContentValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("Validate() error = %T, want *MemoryContentValidationError", err)
			}
			if validationErr.RuleID != tt.ruleID {
				t.Fatalf("Validate() RuleID = %q, want %q", validationErr.RuleID, tt.ruleID)
			}
		})
	}
}

func TestMemoryContentValidatorAllowsDurableProjectContext(t *testing.T) {
	entry := testMemoryEntry(
		"Partner Demo Date",
		"Keep the demo date stable",
		MemoryTypeProject,
		"Partner demo is fixed on 2026-05-01. Why: customer travel is already booked.",
	)
	if err := ValidateMemoryEntryContent(entry); err != nil {
		t.Fatalf("ValidateMemoryEntryContent() error = %v", err)
	}
}

func TestDiskStoreCreateRejectsForbiddenMemoryContent(t *testing.T) {
	store := newTestDiskStore(t, newTestMemoryRoot(t))
	_, err := store.Create(testMemoryEntry(
		"Repo Structure",
		"Do not save this",
		MemoryTypeProject,
		"Project structure: internal/module contains runtime logic and cmd/ contains entrypoints.\nWhy: this is the current layout snapshot.\nHow to apply: consult this note before navigating the repo.",
	))
	if !errors.Is(err, ErrForbiddenMemoryContent) {
		t.Fatalf("Create() error = %v, want %v", err, ErrForbiddenMemoryContent)
	}
}

func TestWriteMemoryFileRejectsForbiddenMemoryContent(t *testing.T) {
	_, err := WriteMemoryFile(newTestMemoryRoot(t), testMemoryEntry(
		"Secret Note",
		"This should be rejected",
		MemoryTypeUser,
		"api_key = \"sk-proj-abcdefghijklmnopqrstuvwxyz1234567890\"",
	))
	if !errors.Is(err, ErrForbiddenMemoryContent) {
		t.Fatalf("WriteMemoryFile() error = %v, want %v", err, ErrForbiddenMemoryContent)
	}
}

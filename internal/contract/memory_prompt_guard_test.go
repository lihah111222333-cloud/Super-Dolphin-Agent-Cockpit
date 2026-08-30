package contract

import (
	"errors"
	"strings"
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

func TestLegacyMemoryPromptSectionGuard(t *testing.T) {
	t.Parallel()
	sections := []ResolvedPromptSection{
		{Name: DynamicSectionMemory, Content: "old memory"},
		{Name: "ordinary", Content: "keep"},
		{Name: " MEMORY_CONTEXT ", Content: "old context"},
		{Name: "relevant_memory", Content: "old attachment-shaped section"},
		{Name: "nested_memory", Content: "old nested attachment-shaped section"},
	}
	filtered := FilterLegacyMemoryPromptSections(sections)
	if len(filtered) != 1 || filtered[0].Name != "ordinary" {
		t.Fatalf("FilterLegacyMemoryPromptSections() = %#v, want ordinary section only", filtered)
	}
	if err := ValidateLegacyMemoryPromptSections(sections); !errors.Is(err, ErrLegacyMemoryPromptInjection) {
		t.Fatalf("ValidateLegacyMemoryPromptSections() error = %v, want legacy-memory sentinel", err)
	}
	if err := ValidateLegacyMemoryPromptSnapshot(PromptAssemblySnapshot{
		SectionSnapshot: map[string]string{"memory_entrypoint": "old memory"},
	}); !errors.Is(err, ErrLegacyMemoryPromptInjection) {
		t.Fatalf("ValidateLegacyMemoryPromptSnapshot() error = %v, want legacy-memory sentinel", err)
	}
	if err := ValidateLegacyMemoryPromptSnapshot(PromptAssemblySnapshot{
		SectionSnapshot: map[string]string{"nested_memory": "old memory"},
	}); !errors.Is(err, ErrLegacyMemoryPromptInjection) {
		t.Fatalf("ValidateLegacyMemoryPromptSnapshot(nested_memory) error = %v, want legacy-memory sentinel", err)
	}
	if err := ValidateResumePromptSnapshot(PromptAssemblySnapshot{
		Version:          0,
		Hash:             "stale-hash",
		BaseInstructions: "old base",
		SectionSnapshot:  map[string]string{DynamicSectionMemory: "old memory"},
	}); !errors.Is(err, ErrLegacyMemoryPromptInjection) {
		t.Fatalf("ValidateResumePromptSnapshot(stale legacy) error = %v, want legacy-memory sentinel", err)
	}
}

func TestLegacyMemoryAttachmentGuard(t *testing.T) {
	t.Parallel()
	ordinary := dto.AttachmentEnvelope{Kind: "file", Path: "README.md", Header: "README", Content: "background", MtimeMs: 1}
	legacyKinds := []string{
		DynamicSectionMemory,
		DynamicSectionMemoryEntrypoint,
		DynamicSectionMemoryContext,
		dto.AttachmentKindRelevantMemory,
		dto.AttachmentKindNestedMemory,
	}
	attachments := make([]dto.AttachmentEnvelope, 0, len(legacyKinds)+1)
	for _, kind := range legacyKinds {
		attachments = append(attachments, dto.AttachmentEnvelope{Kind: " " + strings.ToUpper(kind) + " ", Path: "memory.md"})
	}
	attachments = append(attachments, ordinary)
	filtered := FilterLegacyMemoryAttachments(attachments)
	if len(filtered) != 1 || filtered[0].Kind != ordinary.Kind {
		t.Fatalf("FilterLegacyMemoryAttachments() = %#v, want ordinary attachment only", filtered)
	}
	for _, kind := range legacyKinds {
		legacy := dto.AttachmentEnvelope{Kind: kind, Path: "memory.md"}
		if got := RenderAttachmentText(legacy); got != "" {
			t.Fatalf("RenderAttachmentText(%q) = %q, want empty", kind, got)
		}
	}
	if got := RenderAttachmentText(ordinary); got == "" {
		t.Fatal("RenderAttachmentText(ordinary) = empty, want rendered attachment")
	}
}

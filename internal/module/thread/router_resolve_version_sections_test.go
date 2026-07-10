package thread

import (
	"context"
	"errors"
	"testing"
)

type sectionSnapshotPromptStore struct {
	*fakePromptCatalog
	sections   map[int64][]PromptTemplateSection
	sectionErr error
}

func (s *sectionSnapshotPromptStore) ListSectionsByTemplateID(_ context.Context, templateID int64) ([]PromptTemplateSection, error) {
	if s.sectionErr != nil {
		return nil, s.sectionErr
	}
	return append([]PromptTemplateSection(nil), s.sections[templateID]...), nil
}

func TestResolveRoutedPrompt_InsertVersionUsesInjectableSections(t *testing.T) {
	t.Parallel()

	tpl := sqlTemplate("main/sectioned", "main", "legacy monolith", nil)
	tpl.ID = 42
	store := &sectionSnapshotPromptStore{
		fakePromptCatalog: &fakePromptCatalog{
			templates: []PromptTemplate{tpl},
		},
		sections: map[int64][]PromptTemplateSection{
			42: {
				{SectionKey: "workflow", Region: "dynamic", Ordinal: 1, Body: "  Workflow body  ", Enabled: true, EnableWhen: []byte(`{"language":"zh"}`)},
				{SectionKey: "identity", Region: "static", Ordinal: 2, Body: "Identity body", Enabled: true},
				{SectionKey: "disabled", Region: "dynamic", Ordinal: 2, Body: "Disabled body", Enabled: false},
				{SectionKey: "blank", Region: "dynamic", Ordinal: 3, Body: "   ", Enabled: true},
				{SectionKey: "recall_sqlc", Region: "dynamic", Ordinal: 4, Body: "Recall body", Enabled: true, TriggerType: "recall"},
				{SectionKey: "wrong_language", Region: "dynamic", Ordinal: 5, Body: "Wrong language body", Enabled: true, EnableWhen: []byte(`{"language":"en"}`)},
			},
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{CWD: "/repo/a", PromptKey: "main/sectioned", Prompt: "hello", Language: "zh"}
	s.resolveRoutedPrompt(context.Background(), req)

	if req.BaseInstructions != "" {
		t.Fatalf("BaseInstructions = %q, want structured sections", req.BaseInstructions)
	}
	if got, want := len(req.BaseInstructionBlocks), 2; got != want {
		t.Fatalf("BaseInstructionBlocks len = %d, want %d: %#v", got, want, req.BaseInstructionBlocks)
	}
	if got, want := req.BaseInstructionBlocks[0].Key, "identity"; got != want {
		t.Fatalf("first block key = %q, want %q", got, want)
	}
	if got, want := req.BaseInstructionBlocks[1].Key, "workflow"; got != want {
		t.Fatalf("second block key = %q, want %q", got, want)
	}
	if got, want := store.lastInsertVersion.PromptText, "Identity body\n\nWorkflow body"; got != want {
		t.Fatalf("version prompt_text = %q, want %q", got, want)
	}
}

func TestResolveRoutedPrompt_AllInjectableSectionsGatedOutDoesNotFallBackToPromptText(t *testing.T) {
	t.Parallel()

	tpl := sqlTemplate("main/sectioned", "main", "legacy monolith", nil)
	tpl.ID = 42
	store := &sectionSnapshotPromptStore{
		fakePromptCatalog: &fakePromptCatalog{
			templates: []PromptTemplate{tpl},
		},
		sections: map[int64][]PromptTemplateSection{
			42: {
				{SectionKey: "workflow", Region: "dynamic", Ordinal: 1, Body: "Workflow body", Enabled: true, EnableWhen: []byte(`{"language":"en"}`)},
			},
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{CWD: "/repo/a", PromptKey: "main/sectioned", Prompt: "hello", Language: "zh"}
	s.resolveRoutedPrompt(context.Background(), req)

	if req.BaseInstructions != "" {
		t.Fatalf("BaseInstructions = %q, want no legacy fallback", req.BaseInstructions)
	}
	if len(req.BaseInstructionBlocks) != 0 {
		t.Fatalf("BaseInstructionBlocks = %#v, want none", req.BaseInstructionBlocks)
	}
	if got := store.lastInsertVersion.PromptText; got != "" {
		t.Fatalf("version prompt_text = %q, want no legacy fallback", got)
	}
}

func TestResolveRoutedPrompt_SectionStoreErrorFailsFast(t *testing.T) {
	t.Parallel()

	tpl := sqlTemplate("main/sectioned", "main", "legacy monolith", nil)
	tpl.ID = 42
	sectionErr := errors.New("section store down")
	store := &sectionSnapshotPromptStore{
		fakePromptCatalog: &fakePromptCatalog{
			templates: []PromptTemplate{tpl},
		},
		sectionErr: sectionErr,
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{CWD: "/repo/a", PromptKey: "main/sectioned", Prompt: "hello", Language: "zh"}
	err := s.resolveRoutedPrompt(context.Background(), req)
	if err == nil {
		t.Fatal("resolveRoutedPrompt() error = nil, want section store error")
	}
	if !errors.Is(err, sectionErr) {
		t.Fatalf("resolveRoutedPrompt() error = %v, want wrapped section store error", err)
	}
	if req.BaseInstructions != "" {
		t.Fatalf("BaseInstructions = %q, want no prompt_text fallback", req.BaseInstructions)
	}
	if len(req.BaseInstructionBlocks) != 0 {
		t.Fatalf("BaseInstructionBlocks = %#v, want none", req.BaseInstructionBlocks)
	}
	if got := store.lastInsertVersion.PromptText; got != "" {
		t.Fatalf("version prompt_text = %q, want no materialized fallback", got)
	}
}

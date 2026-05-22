package thread

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
)

type recallOnlySectionStore struct{}

func (recallOnlySectionStore) List(context.Context, promptstore.ListFilter) ([]promptstore.PromptTemplate, error) {
	tags, _ := json.Marshal([]string{"scope.cwd:/repo/a"})
	return []promptstore.PromptTemplate{{
		ID:         91,
		PromptKey:  defaultPromptKey,
		AgentKey:   "main",
		PromptText: "default body",
		Tags:       tags,
		Enabled:    true,
		UpdatedAt:  time.Now(),
	}}, nil
}
func (recallOnlySectionStore) InsertVersion(context.Context, promptstore.PromptTemplateVersion) (int64, error) {
	return 1, nil
}
func (recallOnlySectionStore) CreatePromptTemplate(context.Context, promptstore.PromptTemplate) (*promptstore.PromptTemplate, error) {
	panic("unused")
}
func (recallOnlySectionStore) ListSectionsByTemplateID(context.Context, int64) ([]promptstore.PromptTemplateSection, error) {
	return []promptstore.PromptTemplateSection{
		{TemplateID: 91, SectionKey: "recall_sqlc", Region: "dynamic", Body: "recall body", TriggerType: "recall", Enabled: true},
	}, nil
}
func (recallOnlySectionStore) ListSectionsByTemplateIDs(context.Context, []int64) ([]promptstore.PromptTemplateSection, error) {
	return []promptstore.PromptTemplateSection{
		{TemplateID: 91, SectionKey: "recall_sqlc", Region: "dynamic", Body: "recall body", TriggerType: "recall", Enabled: true},
	}, nil
}
func (recallOnlySectionStore) ListRecallSections(context.Context, string) ([]promptstore.PromptTemplateSection, error) {
	panic("unused")
}
func (recallOnlySectionStore) ListDefaultRuleSections(context.Context, string) ([]promptstore.PromptTemplateSection, error) {
	panic("unused")
}
func (recallOnlySectionStore) WithTx(context.Context, func(promptstore.Store) error) error {
	panic("unused")
}
func (recallOnlySectionStore) Get(context.Context, string) (*promptstore.PromptTemplate, error) {
	panic("unused")
}
func (recallOnlySectionStore) Delete(context.Context, string) error {
	panic("unused")
}
func (recallOnlySectionStore) Upsert(context.Context, promptstore.PromptTemplate) (*promptstore.PromptTemplate, error) {
	panic("unused")
}
func (recallOnlySectionStore) UpsertSection(context.Context, promptstore.PromptTemplateSection) (*promptstore.PromptTemplateSection, error) {
	panic("unused")
}
func (recallOnlySectionStore) DeleteSection(context.Context, int64, string) error {
	panic("unused")
}
func (recallOnlySectionStore) UpsertIntentDraft(context.Context, promptstore.PromptIntentDraft) (*promptstore.PromptIntentDraft, error) {
	panic("unused")
}
func (recallOnlySectionStore) GetIntentDraft(context.Context, string, string) (*promptstore.PromptIntentDraft, error) {
	panic("unused")
}
func (recallOnlySectionStore) ListIntentDrafts(context.Context, promptstore.PromptIntentDraftListFilter) ([]promptstore.PromptIntentDraft, error) {
	panic("unused")
}
func (recallOnlySectionStore) UpdateIntentDraftStatus(context.Context, string, string, string) (*promptstore.PromptIntentDraft, error) {
	panic("unused")
}
func (recallOnlySectionStore) LockRecallTopicInCWD(context.Context, string, string) error {
	panic("unused")
}

func TestResolveRoutedPrompt_RecallOnlySectionsDoNotLaunchAsDefaultPrompt(t *testing.T) {
	t.Parallel()

	s := newServiceWithRouter(recallOnlySectionStore{})
	req := &StartRequest{CWD: "/repo/a", Prompt: "hello"}
	if err := s.resolveRoutedPrompt(context.Background(), req); err != nil {
		t.Fatalf("resolveRoutedPrompt() error = %v", err)
	}

	if req.BaseInstructions != "" {
		t.Fatalf("BaseInstructions = %q, want recall-only runtime asset skipped", req.BaseInstructions)
	}
	if len(req.BaseInstructionBlocks) != 0 {
		t.Fatalf("BaseInstructionBlocks = %#v, want none", req.BaseInstructionBlocks)
	}
	if req.PromptKey != "" || req.AgentKey != "" {
		t.Fatalf("runtime asset default must not stamp launch identity: %+v", req)
	}
}

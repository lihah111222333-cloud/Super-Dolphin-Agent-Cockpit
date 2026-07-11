package prompt

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	"github.com/stretchr/testify/require"
)

func TestNewServiceRegistersBuiltInDynamicProviders(t *testing.T) {
	t.Setenv("SHELL", "/bin/zsh")
	svc := NewService(&Config{}, nil)

	assembly, err := svc.AssembleTurn(context.Background(), TurnInput{
		CWD:      "/repo",
		GitRoot:  "/repo",
		Language: "Chinese",
		EnabledTools: []string{
			"file",
			"request_user_input",
			"spawn_agent",
		},
		MCPSnapshot: MCPSnapshot{
			Servers:      []string{"lsp"},
			Instructions: map[string]string{"lsp": "Use the LSP MCP first."},
		},
		SessionFlags: map[string]bool{"verification_required": true},
	})
	if err != nil {
		t.Fatalf("AssembleTurn() error = %v", err)
	}

	checks := []string{
		"# Session-specific guidance",
		"request_user_input",
		"spawn_agent",
		"# Environment",
		"# Language",
		"# MCP Server Instructions",
	}
	for _, check := range checks {
		if !resolvedSectionsContain(assembly.ResolvedSections, check) {
			t.Fatalf("ResolvedSections = %#v, want substring %q", assembly.ResolvedSections, check)
		}
	}
}

func resolvedSectionsContain(sections []ResolvedPromptSection, want string) bool {
	for _, section := range sections {
		if strings.Contains(section.Content, want) {
			return true
		}
	}
	return false
}

type inMemoryPromptStore struct {
	promptTemplateMemoryStore
	promptSectionMemoryStore
	promptDraftMemoryStore
	promptRecallMemoryStore

	txCalls int
}

type promptTemplateMemoryStore struct {
	templates   map[string]Template
	versions    []TemplateVersion
	listFilters []ListFilter
	getCalls    int
	upsertCalls int
	deleteCalls int
}

type promptSectionMemoryStore struct {
	sections                          map[int64]map[string]TemplateSection
	listSectionsByTemplateIDsCalls    int
	listSectionsByTemplateIDsCaptured []int64
}

type promptDraftMemoryStore struct {
	drafts map[string]IntentDraft
}

type promptRecallMemoryStore struct {
	lockRecallCalls []struct {
		cwd   string
		topic string
	}
	upsertRecallTargetCalls []struct {
		cwd        string
		topic      string
		templateID int64
		sectionKey string
	}
}

func newInMemoryPromptStore() *inMemoryPromptStore {
	return &inMemoryPromptStore{
		promptTemplateMemoryStore: promptTemplateMemoryStore{templates: map[string]Template{}},
		promptSectionMemoryStore:  promptSectionMemoryStore{sections: map[int64]map[string]TemplateSection{}},
		promptDraftMemoryStore:    promptDraftMemoryStore{drafts: map[string]IntentDraft{}},
	}
}

func promptIntentStoreForTest(store Store) promptIntentStoreAdapter {
	return promptIntentStoreAdapter{store: store}
}

func (s *promptTemplateMemoryStore) List(_ context.Context, filter ListFilter) ([]Template, error) {
	s.listFilters = append(s.listFilters, filter)
	items := make([]Template, 0, len(s.templates))
	for _, template := range s.templates {
		items = append(items, template)
	}
	return items, nil
}

func (s *inMemoryPromptStore) WithTx(_ context.Context, fn func(Store) error) error {
	s.txCalls++
	return fn(s)
}

func (s *promptTemplateMemoryStore) Get(_ context.Context, promptKey string) (*Template, error) {
	s.getCalls++
	template, ok := s.templates[promptKey]
	if !ok {
		return nil, platformdb.ErrNotFound
	}
	copy := template
	return &copy, nil
}

func (s *promptTemplateMemoryStore) Delete(_ context.Context, promptKey string) error {
	if _, ok := s.templates[promptKey]; !ok {
		return platformdb.ErrNotFound
	}
	s.deleteCalls++
	delete(s.templates, promptKey)
	return nil
}

func (s *promptTemplateMemoryStore) InsertVersion(_ context.Context, version TemplateVersion) (int64, error) {
	s.versions = append(s.versions, version)
	return int64(len(s.versions)), nil
}

func (s *promptTemplateMemoryStore) CreatePromptTemplate(_ context.Context, template Template) (*Template, error) {
	if _, ok := s.templates[template.PromptKey]; ok {
		return nil, platformdb.ErrConflict
	}
	if template.ID == 0 {
		template.ID = int64(len(s.templates) + 1)
	}
	now := time.Unix(1_700_000_000, int64(len(s.templates)+1)).UTC()
	if template.CreatedAt.IsZero() {
		template.CreatedAt = now
	}
	template.UpdatedAt = now
	s.templates[template.PromptKey] = template
	copy := template
	return &copy, nil
}

func (s *promptTemplateMemoryStore) Upsert(_ context.Context, template Template) (*Template, error) {
	s.upsertCalls++
	now := time.Unix(1_700_000_000, int64(s.upsertCalls)).UTC()
	if current, ok := s.templates[template.PromptKey]; ok {
		if template.ID == 0 {
			template.ID = current.ID
		}
		if template.CreatedAt.IsZero() {
			template.CreatedAt = current.CreatedAt
		}
		if strings.TrimSpace(template.CreatedBy) == "" {
			template.CreatedBy = current.CreatedBy
		}
	}
	if template.CreatedAt.IsZero() {
		template.CreatedAt = now
	}
	template.UpdatedAt = now
	s.templates[template.PromptKey] = template
	copy := template
	return &copy, nil
}

func (s *promptSectionMemoryStore) ListSectionsByTemplateID(_ context.Context, templateID int64) ([]TemplateSection, error) {
	byKey := s.sections[templateID]
	sections := make([]TemplateSection, 0, len(byKey))
	for _, section := range byKey {
		sections = append(sections, section)
	}
	sort.Slice(sections, func(i, j int) bool {
		if sections[i].Region != sections[j].Region {
			return sections[i].Region < sections[j].Region
		}
		if sections[i].Ordinal != sections[j].Ordinal {
			return sections[i].Ordinal < sections[j].Ordinal
		}
		return sections[i].SectionKey < sections[j].SectionKey
	})
	return sections, nil
}

func (s *promptSectionMemoryStore) ListSectionsByTemplateIDs(_ context.Context, templateIDs []int64) ([]TemplateSection, error) {
	s.listSectionsByTemplateIDsCalls++
	s.listSectionsByTemplateIDsCaptured = append([]int64(nil), templateIDs...)
	sections := make([]TemplateSection, 0)
	for _, templateID := range templateIDs {
		templateSections, err := s.ListSectionsByTemplateID(context.Background(), templateID)
		if err != nil {
			return nil, err
		}
		sections = append(sections, templateSections...)
	}
	return sections, nil
}

func (s *inMemoryPromptStore) ListRecallSections(_ context.Context, cwd string) ([]TemplateSection, error) {
	var sections []TemplateSection
	for _, template := range s.templates {
		if !templateVisibleForCWD(template, cwd) {
			continue
		}
		byKey := s.sections[template.ID]
		for _, section := range byKey {
			if section.TriggerType == "recall" && section.Enabled {
				section.TemplateID = template.ID
				section.TemplatePromptKey = template.PromptKey
				section.TemplateTitle = template.Title
				section.TemplateDescription = template.Description
				section.TemplateWhenToUse = template.WhenToUse
				section.TemplateTags = append(json.RawMessage(nil), template.Tags...)
				sections = append(sections, section)
			}
		}
	}
	return sections, nil
}

func (s *inMemoryPromptStore) ListDefaultRuleSections(_ context.Context, cwd string) ([]TemplateSection, error) {
	var sections []TemplateSection
	for _, template := range s.templates {
		if template.AgentKey != "default_rule" || !templateVisibleForCWD(template, cwd) {
			continue
		}
		for _, section := range s.sections[template.ID] {
			if section.TriggerType == "always" && section.Enabled {
				section.TemplateID = template.ID
				section.TemplatePromptKey = template.PromptKey
				section.TemplateTitle = template.Title
				section.TemplateTags = append(json.RawMessage(nil), template.Tags...)
				sections = append(sections, section)
			}
		}
	}
	return sections, nil
}

func templateVisibleForCWD(template Template, cwd string) bool {
	cwd = strings.TrimSpace(cwd)
	for _, tag := range promptstore.TemplateTags(template.Tags) {
		tag = strings.TrimSpace(tag)
		if tag == "scope.global" || (cwd != "" && tag == "scope.cwd:"+cwd) {
			return true
		}
	}
	return false
}

func (s *promptSectionMemoryStore) UpsertSection(_ context.Context, section TemplateSection) (*TemplateSection, error) {
	copy := section
	if copy.ID == 0 {
		copy.ID = int64(len(s.sections[section.TemplateID]) + 1)
	}
	if s.sections[section.TemplateID] == nil {
		s.sections[section.TemplateID] = map[string]TemplateSection{}
	}
	s.sections[section.TemplateID][section.SectionKey] = copy
	return &copy, nil
}

func (s *promptSectionMemoryStore) DeleteSection(context.Context, int64, string) error {
	return nil
}

func (s *promptDraftMemoryStore) UpsertIntentDraft(_ context.Context, draft IntentDraft) (*IntentDraft, error) {
	s.drafts[draft.DraftKey] = draft
	copy := draft
	return &copy, nil
}

func (s *promptDraftMemoryStore) GetIntentDraft(_ context.Context, cwd, draftKey string) (*IntentDraft, error) {
	draft, ok := s.drafts[draftKey]
	if !ok || strings.TrimSpace(draft.CWD) != strings.TrimSpace(cwd) {
		return nil, platformdb.ErrNotFound
	}
	copy := draft
	return &copy, nil
}

func (s *promptDraftMemoryStore) ListIntentDrafts(_ context.Context, filter IntentDraftListFilter) ([]IntentDraft, error) {
	var drafts []IntentDraft
	for _, draft := range s.drafts {
		if strings.TrimSpace(draft.CWD) != strings.TrimSpace(filter.CWD) {
			continue
		}
		if filter.Status != "" && draft.Status != filter.Status {
			continue
		}
		drafts = append(drafts, draft)
	}
	return drafts, nil
}

func (s *promptDraftMemoryStore) UpdateIntentDraftStatus(_ context.Context, cwd, draftKey, status string) (*IntentDraft, error) {
	draft, ok := s.drafts[draftKey]
	if !ok || strings.TrimSpace(draft.CWD) != strings.TrimSpace(cwd) {
		return nil, platformdb.ErrNotFound
	}
	draft.Status = strings.TrimSpace(status)
	s.drafts[draftKey] = draft
	copy := draft
	return &copy, nil
}

func (s *promptRecallMemoryStore) LockRecallTopicInCWD(_ context.Context, cwd, topic string) error {
	s.lockRecallCalls = append(s.lockRecallCalls, struct {
		cwd   string
		topic string
	}{cwd: strings.TrimSpace(cwd), topic: strings.TrimSpace(topic)})
	return nil
}

func (s *promptRecallMemoryStore) UpsertRecallTopicTargetInCWD(_ context.Context, cwd, topic string, templateID int64, sectionKey string) error {
	s.upsertRecallTargetCalls = append(s.upsertRecallTargetCalls, struct {
		cwd        string
		topic      string
		templateID int64
		sectionKey string
	}{
		cwd:        strings.TrimSpace(cwd),
		topic:      strings.TrimSpace(topic),
		templateID: templateID,
		sectionKey: strings.TrimSpace(sectionKey),
	})
	return nil
}

func scopedPromptTemplate(promptKey, cwd string) Template {
	now := time.Unix(1_700_000_000, 0).UTC()
	return Template{
		PromptKey:   promptKey,
		Title:       "Scoped Prompt",
		AgentKey:    "main",
		PromptText:  "original",
		Variables:   json.RawMessage("{}"),
		Tags:        withPromptScopeTag(json.RawMessage("[]"), cwd),
		Enabled:     true,
		CreatedBy:   "tester",
		UpdatedBy:   "tester",
		CreatedAt:   now,
		UpdatedAt:   now,
		Description: "scoped",
	}
}

func TestPromptMutationsRespectCwdScope(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const promptKey = "main/scoped"

	t.Run("write rejects empty cwd", func(t *testing.T) {
		store := newInMemoryPromptStore()
		svc := newPromptService(store)

		_, err := svc.WritePrompt(ctx, "   ", PromptWriteRequest{Name: "Scoped Prompt", Content: "updated"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "cwd is required")
		require.Zero(t, store.txCalls)
	})

	t.Run("delete rejects empty cwd", func(t *testing.T) {
		store := newInMemoryPromptStore()
		store.templates[promptKey] = scopedPromptTemplate(promptKey, "/repo/a")
		svc := newPromptService(store)

		err := svc.DeletePrompt(ctx, "", promptKey)
		require.Error(t, err)
		require.Contains(t, err.Error(), "cwd is required")
		require.Zero(t, store.txCalls)
		require.Zero(t, store.deleteCalls)
		require.Empty(t, store.versions)
	})

	t.Run("write rejects cross cwd", func(t *testing.T) {
		store := newInMemoryPromptStore()
		store.templates[promptKey] = scopedPromptTemplate(promptKey, "/repo/a")
		svc := newPromptService(store)

		_, err := svc.WritePrompt(ctx, "/repo/b", PromptWriteRequest{ID: promptKey, Name: "Scoped Prompt", Content: "updated"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "outside cwd scope")
		require.Zero(t, store.upsertCalls)
		require.Empty(t, store.versions)
		require.Equal(t, "original", store.templates[promptKey].PromptText)
	})

	t.Run("delete rejects cross cwd", func(t *testing.T) {
		store := newInMemoryPromptStore()
		store.templates[promptKey] = scopedPromptTemplate(promptKey, "/repo/a")
		svc := newPromptService(store)

		err := svc.DeletePrompt(ctx, "/repo/b", promptKey)
		require.Error(t, err)
		require.Contains(t, err.Error(), "outside cwd scope")
		require.Zero(t, store.deleteCalls)
		require.Empty(t, store.versions)
		require.Contains(t, store.templates, promptKey)
	})

	t.Run("write existing prompt marks manually edited", func(t *testing.T) {
		store := newInMemoryPromptStore()
		store.templates[promptKey] = scopedPromptTemplate(promptKey, "/repo/a")
		svc := newPromptService(store)

		got, err := svc.WritePrompt(ctx, "/repo/a", PromptWriteRequest{
			ID:      promptKey,
			Name:    "Scoped Prompt",
			Content: "updated by user",
		})
		if err != nil {
			t.Fatalf("WritePrompt() unexpected error: %v", err)
		}
		require.NotNil(t, got)
		require.Truef(t, got.ManuallyEdited, "WritePrompt() manually_edited = false, want true: %+v", got)
		if saved := store.templates[promptKey]; !saved.ManuallyEdited || saved.PromptText != "updated by user" {
			t.Fatalf("stored prompt = %+v, want manually edited updated prompt", saved)
		}
	})

}

func TestPromptWritePreservesWhenToUseWhenOmitted(t *testing.T) {
	t.Parallel()

	const promptKey = "main/scoped"
	store := newInMemoryPromptStore()
	template := scopedPromptTemplate(promptKey, "/repo/a")
	template.WhenToUse = "Use when modifying scoped prompt behavior."
	store.templates[promptKey] = template
	svc := newPromptService(store)

	got, err := svc.WritePrompt(context.Background(), "/repo/a", PromptWriteRequest{
		ID:      promptKey,
		Name:    "Scoped Prompt",
		Content: "updated by user",
	})
	require.NoError(t, err)
	require.Equal(t, "Use when modifying scoped prompt behavior.", got.WhenToUse)
	require.Equal(t, "Use when modifying scoped prompt behavior.", store.templates[promptKey].WhenToUse)
}

func TestPromptWritePreservesEnabledWhenOmitted(t *testing.T) {
	t.Parallel()

	const promptKey = "main/scoped"
	store := newInMemoryPromptStore()
	template := scopedPromptTemplate(promptKey, "/repo/a")
	template.Enabled = false
	store.templates[promptKey] = template
	svc := newPromptService(store)

	got, err := svc.WritePrompt(context.Background(), "/repo/a", PromptWriteRequest{
		ID:      promptKey,
		Name:    "Scoped Prompt",
		Content: "updated by user",
	})
	require.NoError(t, err)
	require.False(t, got.Enabled)
	require.False(t, store.templates[promptKey].Enabled)
}

func TestPromptWriteAppliesExplicitEnabled(t *testing.T) {
	t.Parallel()

	enabled := false
	store := newInMemoryPromptStore()
	svc := newPromptService(store)

	got, err := svc.WritePrompt(context.Background(), "/repo/a", PromptWriteRequest{
		Name:         "Scoped Prompt",
		Content:      "project local prompt",
		WhenToUse:    "Use for scoped prompt tests.",
		WhenToUseSet: true,
		Enabled:      &enabled,
	})
	require.NoError(t, err)
	require.False(t, got.Enabled)
	require.False(t, store.templates[got.PromptKey].Enabled)
}

func TestPromptWritePreservesPromptTextWhenContentOmitted(t *testing.T) {
	t.Parallel()

	const promptKey = "main/scoped"
	store := newInMemoryPromptStore()
	template := scopedPromptTemplate(promptKey, "/repo/a")
	template.PromptText = "original body"
	template.WhenToUse = "old guidance"
	store.templates[promptKey] = template
	svc := newPromptService(store)

	got, err := svc.WritePrompt(context.Background(), "/repo/a", PromptWriteRequest{
		ID:           promptKey,
		Name:         "Scoped Prompt",
		WhenToUse:    "Use after metadata edits.",
		WhenToUseSet: true,
	})
	require.NoError(t, err)
	require.Equal(t, "original body", got.PromptText)
	require.Equal(t, "original body", store.templates[promptKey].PromptText)
	require.Equal(t, "Use after metadata edits.", got.WhenToUse)
}

func TestPromptWriteRejectsMissingWhenToUse(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	svc := newPromptService(store)

	_, err := svc.WritePrompt(context.Background(), "/repo/a", PromptWriteRequest{
		Name:    "Scoped Prompt",
		Content: "new body",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "when_to_use is required")
	require.Zero(t, store.upsertCalls)
}

func TestPromptWriteUpdatesExplicitWhenToUse(t *testing.T) {
	t.Parallel()

	const promptKey = "main/scoped"
	store := newInMemoryPromptStore()
	template := scopedPromptTemplate(promptKey, "/repo/a")
	template.WhenToUse = "old guidance"
	store.templates[promptKey] = template
	svc := newPromptService(store)

	got, err := svc.WritePrompt(context.Background(), "/repo/a", PromptWriteRequest{
		ID:           promptKey,
		Name:         "Scoped Prompt",
		Content:      "updated by user",
		WhenToUse:    "Use after UI metadata edits.",
		WhenToUseSet: true,
	})
	require.NoError(t, err)
	require.Equal(t, "Use after UI metadata edits.", got.WhenToUse)
	require.Equal(t, "Use after UI metadata edits.", store.templates[promptKey].WhenToUse)
}

func TestPromptWriteInvalidatesPromptDynamicCatalogs(t *testing.T) {
	t.Parallel()

	const promptKey = "main/scoped"
	store := newInMemoryPromptStore()
	store.templates[promptKey] = scopedPromptTemplate(promptKey, "/repo/a")
	rec := &recordingSectionInvalidator{}
	svc := newPromptService(store, rec)

	_, err := svc.WritePrompt(context.Background(), "/repo/a", PromptWriteRequest{
		ID:           promptKey,
		Name:         "Scoped Prompt",
		Content:      "updated by user",
		WhenToUse:    "Use after UI metadata edits.",
		WhenToUseSet: true,
	})
	require.NoError(t, err)
	require.Equal(t, contract.InvalidateClear, rec.reason)
	require.Equal(t, []string{contract.DynamicSectionAvailableExperts, contract.DynamicSectionRecallCatalog, contract.DynamicSectionProjectDefaultRules}, rec.names)
}

func TestPromptDeleteInvalidatesPromptDynamicCatalogs(t *testing.T) {
	t.Parallel()

	const promptKey = "main/scoped"
	store := newInMemoryPromptStore()
	store.templates[promptKey] = scopedPromptTemplate(promptKey, "/repo/a")
	rec := &recordingSectionInvalidator{}
	svc := newPromptService(store, rec)

	err := svc.DeletePrompt(context.Background(), "/repo/a", promptKey)
	require.NoError(t, err)
	require.Equal(t, contract.InvalidateClear, rec.reason)
	require.Equal(t, []string{contract.DynamicSectionAvailableExperts, contract.DynamicSectionRecallCatalog, contract.DynamicSectionProjectDefaultRules}, rec.names)
}

func TestPromptSectionWriteInvalidatesRecallCatalog(t *testing.T) {
	t.Parallel()

	const promptKey = "main/scoped"
	store := newInMemoryPromptStore()
	template := scopedPromptTemplate(promptKey, "/repo/a")
	template.ID = 7
	store.templates[promptKey] = template
	rec := &recordingSectionInvalidator{}
	svc := newPromptService(store, rec)

	_, err := svc.WriteSection(context.Background(), "/repo/a", PromptSectionWriteRequest{
		PromptKey:   promptKey,
		SectionKey:  "recall_sqlc",
		Region:      "dynamic",
		Body:        "SQLC workflow body",
		Enabled:     true,
		TriggerType: "recall",
		RecallTopic: "sqlc-workflow",
	})
	require.NoError(t, err)
	require.Equal(t, contract.InvalidateClear, rec.reason)
	require.Equal(t, []string{contract.DynamicSectionRecallCatalog, contract.DynamicSectionProjectDefaultRules}, rec.names)
}

func TestPromptSectionDeleteInvalidatesRecallCatalog(t *testing.T) {
	t.Parallel()

	const promptKey = "main/scoped"
	store := newInMemoryPromptStore()
	template := scopedPromptTemplate(promptKey, "/repo/a")
	template.ID = 7
	store.templates[promptKey] = template
	store.sections[7] = map[string]TemplateSection{
		"recall_sqlc": {TemplateID: 7, SectionKey: "recall_sqlc", TriggerType: "recall", RecallTopic: "sqlc-workflow", Enabled: true},
	}
	rec := &recordingSectionInvalidator{}
	svc := newPromptService(store, rec)

	err := svc.DeleteSection(context.Background(), "/repo/a", promptKey, "recall_sqlc")
	require.NoError(t, err)
	require.Equal(t, contract.InvalidateClear, rec.reason)
	require.Equal(t, []string{contract.DynamicSectionRecallCatalog, contract.DynamicSectionProjectDefaultRules}, rec.names)
}

func TestPromptWriteRejectsExplicitEmptyWhenToUse(t *testing.T) {
	t.Parallel()

	const promptKey = "main/scoped"
	store := newInMemoryPromptStore()
	template := scopedPromptTemplate(promptKey, "/repo/a")
	template.WhenToUse = "old guidance"
	store.templates[promptKey] = template
	svc := newPromptService(store)

	_, err := svc.WritePrompt(context.Background(), "/repo/a", PromptWriteRequest{
		ID:           promptKey,
		Name:         "Scoped Prompt",
		Content:      "updated by user",
		WhenToUse:    "   ",
		WhenToUseSet: true,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "when_to_use is required")
	require.Equal(t, "old guidance", store.templates[promptKey].WhenToUse)
}

type recordingSectionInvalidator struct {
	reason contract.InvalidateReason
	names  []string
}

func (r *recordingSectionInvalidator) InvalidateSections(reason contract.InvalidateReason, names ...string) uint64 {
	r.reason = reason
	r.names = append([]string(nil), names...)
	return 1
}

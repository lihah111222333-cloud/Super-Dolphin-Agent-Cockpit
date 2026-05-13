package prompt

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
)

func TestNewServiceRegistersBuiltInDynamicProviders(t *testing.T) {
	t.Setenv("SHELL", "/bin/zsh")
	svc := NewService(&Config{}, nil)

	assembly, err := svc.AssembleTurn(context.Background(), TurnInput{
		CWD:      "/repo",
		GitRoot:  "/repo",
		Language: "Chinese",
		EnabledTools: []string{
			"lsp_file",
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
	templates   map[string]promptstore.PromptTemplate
	versions    []promptstore.PromptTemplateVersion
	txCalls     int
	upsertCalls int
	deleteCalls int
}

func newInMemoryPromptStore() *inMemoryPromptStore {
	return &inMemoryPromptStore{templates: map[string]promptstore.PromptTemplate{}}
}

func (s *inMemoryPromptStore) List(context.Context, promptstore.ListFilter) ([]promptstore.PromptTemplate, error) {
	items := make([]promptstore.PromptTemplate, 0, len(s.templates))
	for _, template := range s.templates {
		items = append(items, template)
	}
	return items, nil
}

func (s *inMemoryPromptStore) WithTx(_ context.Context, fn func(promptstore.Store) error) error {
	s.txCalls++
	return fn(s)
}

func (s *inMemoryPromptStore) Get(_ context.Context, promptKey string) (*promptstore.PromptTemplate, error) {
	template, ok := s.templates[promptKey]
	if !ok {
		return nil, platformdb.ErrNotFound
	}
	copy := template
	return &copy, nil
}

func (s *inMemoryPromptStore) Delete(_ context.Context, promptKey string) error {
	if _, ok := s.templates[promptKey]; !ok {
		return platformdb.ErrNotFound
	}
	s.deleteCalls++
	delete(s.templates, promptKey)
	return nil
}

func (s *inMemoryPromptStore) InsertVersion(_ context.Context, version promptstore.PromptTemplateVersion) (int64, error) {
	s.versions = append(s.versions, version)
	return int64(len(s.versions)), nil
}

func (s *inMemoryPromptStore) Upsert(_ context.Context, template promptstore.PromptTemplate) (*promptstore.PromptTemplate, error) {
	s.upsertCalls++
	now := time.Unix(1_700_000_000, int64(s.upsertCalls)).UTC()
	if current, ok := s.templates[template.PromptKey]; ok {
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

func (s *inMemoryPromptStore) ListSectionsByTemplateID(context.Context, int64) ([]promptstore.PromptTemplateSection, error) {
	return nil, nil
}

func (s *inMemoryPromptStore) UpsertSection(_ context.Context, section promptstore.PromptTemplateSection) (*promptstore.PromptTemplateSection, error) {
	copy := section
	return &copy, nil
}

func (s *inMemoryPromptStore) DeleteSection(context.Context, int64, string) error {
	return nil
}

func scopedPromptTemplate(promptKey, cwd string) promptstore.PromptTemplate {
	now := time.Unix(1_700_000_000, 0).UTC()
	return promptstore.PromptTemplate{
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
		if err == nil || !strings.Contains(err.Error(), "cwd is required") {
			t.Fatalf("WritePrompt() error = %v, want cwd required", err)
		}
		if store.txCalls != 0 {
			t.Fatalf("WritePrompt() txCalls = %d, want 0", store.txCalls)
		}
	})

	t.Run("delete rejects empty cwd", func(t *testing.T) {
		store := newInMemoryPromptStore()
		store.templates[promptKey] = scopedPromptTemplate(promptKey, "/repo/a")
		svc := newPromptService(store)

		err := svc.DeletePrompt(ctx, "", promptKey)
		if err == nil || !strings.Contains(err.Error(), "cwd is required") {
			t.Fatalf("DeletePrompt() error = %v, want cwd required", err)
		}
		if store.txCalls != 0 || store.deleteCalls != 0 || len(store.versions) != 0 {
			t.Fatalf("DeletePrompt() mutated store unexpectedly: tx=%d delete=%d versions=%d", store.txCalls, store.deleteCalls, len(store.versions))
		}
	})

	t.Run("write rejects cross cwd", func(t *testing.T) {
		store := newInMemoryPromptStore()
		store.templates[promptKey] = scopedPromptTemplate(promptKey, "/repo/a")
		svc := newPromptService(store)

		_, err := svc.WritePrompt(ctx, "/repo/b", PromptWriteRequest{ID: promptKey, Name: "Scoped Prompt", Content: "updated"})
		if err == nil || !strings.Contains(err.Error(), "outside cwd scope") {
			t.Fatalf("WritePrompt() error = %v, want outside cwd scope", err)
		}
		if store.upsertCalls != 0 || len(store.versions) != 0 {
			t.Fatalf("WritePrompt() mutated store unexpectedly: upsert=%d versions=%d", store.upsertCalls, len(store.versions))
		}
		if got := store.templates[promptKey].PromptText; got != "original" {
			t.Fatalf("WritePrompt() prompt text = %q, want original", got)
		}
	})

	t.Run("delete rejects cross cwd", func(t *testing.T) {
		store := newInMemoryPromptStore()
		store.templates[promptKey] = scopedPromptTemplate(promptKey, "/repo/a")
		svc := newPromptService(store)

		err := svc.DeletePrompt(ctx, "/repo/b", promptKey)
		if err == nil || !strings.Contains(err.Error(), "outside cwd scope") {
			t.Fatalf("DeletePrompt() error = %v, want outside cwd scope", err)
		}
		if store.deleteCalls != 0 || len(store.versions) != 0 {
			t.Fatalf("DeletePrompt() mutated store unexpectedly: delete=%d versions=%d", store.deleteCalls, len(store.versions))
		}
		if _, ok := store.templates[promptKey]; !ok {
			t.Fatal("DeletePrompt() removed prompt unexpectedly")
		}
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
		if got == nil || !got.ManuallyEdited {
			t.Fatalf("WritePrompt() manually_edited = false, want true: %+v", got)
		}
		if saved := store.templates[promptKey]; !saved.ManuallyEdited || saved.PromptText != "updated by user" {
			t.Fatalf("stored prompt = %+v, want manually edited updated prompt", saved)
		}
	})
}

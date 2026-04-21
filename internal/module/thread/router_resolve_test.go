package thread

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/router"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
)

// fakePromptStore is the minimum surface of promptstore.Store that
// resolveRoutedPrompt exercises. Other methods panic on use so an incorrect
// code path fails loudly.
type fakePromptStore struct {
	templates         []promptstore.PromptTemplate
	nextVersionID     int64
	insertErr         error
	lastInsertVersion promptstore.PromptTemplateVersion
}

func (f *fakePromptStore) List(_ context.Context, _ promptstore.ListFilter) ([]promptstore.PromptTemplate, error) {
	return append([]promptstore.PromptTemplate(nil), f.templates...), nil
}

func (f *fakePromptStore) InsertVersion(_ context.Context, v promptstore.PromptTemplateVersion) (int64, error) {
	f.lastInsertVersion = v
	if f.insertErr != nil {
		return 0, f.insertErr
	}
	f.nextVersionID++
	return f.nextVersionID, nil
}

func (f *fakePromptStore) Get(context.Context, string) (*promptstore.PromptTemplate, error) {
	panic("unused")
}
func (f *fakePromptStore) Delete(context.Context, string) error { panic("unused") }
func (f *fakePromptStore) Upsert(context.Context, promptstore.PromptTemplate) (*promptstore.PromptTemplate, error) {
	panic("unused")
}
func (f *fakePromptStore) WithTx(context.Context, func(promptstore.Store) error) error {
	panic("unused")
}

func newServiceWithRouter(store *fakePromptStore) *service {
	s := &service{
		promptStore:   store,
		routerBackend: router.NewRuleRouter(),
	}
	return s
}

func sqlTemplate(promptKey, agentKey, text string, tags []string) promptstore.PromptTemplate {
	b, _ := json.Marshal(tags)
	return promptstore.PromptTemplate{
		PromptKey:  promptKey,
		AgentKey:   agentKey,
		PromptText: text,
		Tags:       b,
		Enabled:    true,
		UpdatedAt:  time.Now(),
	}
}

func TestResolveRoutedPrompt_RouterMatchFillsAllFields(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplate("main/sql", "sql_expert", "You are a SQL expert.", []string{"SQL", "database"}),
			sqlTemplate("main/ui", "ui_expert", "You are a UI expert.", []string{"React", "CSS"}),
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{Prompt: "please help me write a SQL query"}
	s.resolveRoutedPrompt(context.Background(), req)

	if req.AgentKey != "sql_expert" {
		t.Fatalf("agent_key: want sql_expert, got %q", req.AgentKey)
	}
	if req.PromptVersionID == nil || *req.PromptVersionID != 1 {
		t.Fatalf("prompt_version_id: want ptr to 1, got %v", req.PromptVersionID)
	}
	if !strings.Contains(req.BaseInstructions, "SQL expert") {
		t.Fatalf("BaseInstructions not injected: %q", req.BaseInstructions)
	}
	if store.lastInsertVersion.PromptKey != "main/sql" {
		t.Fatalf("materialized wrong prompt_key: %q", store.lastInsertVersion.PromptKey)
	}
}

func TestResolveRoutedPrompt_ExplicitBaseInstructionsShortCircuits(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplate("main/sql", "sql_expert", "from db", []string{"sql"}),
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{BaseInstructions: "caller-provided", Prompt: "SQL please"}
	s.resolveRoutedPrompt(context.Background(), req)

	if req.BaseInstructions != "caller-provided" {
		t.Fatalf("BaseInstructions overwritten: %q", req.BaseInstructions)
	}
	if req.AgentKey != "" || req.PromptVersionID != nil {
		t.Fatalf("should not touch agent_key or version_id: %+v", req)
	}
	if store.lastInsertVersion.PromptKey != "" {
		t.Fatalf("should not have materialized a version: %+v", store.lastInsertVersion)
	}
}

func TestResolveRoutedPrompt_ExplicitAgentKeyBypassesRouter(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplate("main/sql", "sql_expert", "db body", []string{"sql"}),
			sqlTemplate("main/ui", "ui_expert", "ui body", []string{"react"}),
		},
	}
	s := newServiceWithRouter(store)

	// User input says "react" but AgentKey overrides \u2014 we should get ui_expert
	// *not* by tag match, but by explicit pin.
	req := &StartRequest{AgentKey: "ui_expert", Prompt: "write some react code"}
	s.resolveRoutedPrompt(context.Background(), req)

	if req.AgentKey != "ui_expert" {
		t.Fatalf("want ui_expert, got %q", req.AgentKey)
	}
	if req.BaseInstructions != "ui body" {
		t.Fatalf("want 'ui body', got %q", req.BaseInstructions)
	}
	if req.PromptVersionID == nil {
		t.Fatalf("prompt_version_id should be set")
	}
}

func TestResolveRoutedPrompt_NoMatchLeavesRequestUntouched(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplate("main/sql", "sql_expert", "db body", []string{"database"}),
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{Prompt: "hello world, no relevant tags"}
	s.resolveRoutedPrompt(context.Background(), req)

	if req.AgentKey != "" || req.PromptVersionID != nil || req.BaseInstructions != "" {
		t.Fatalf("expected untouched req, got %+v", req)
	}
}

func TestResolveRoutedPrompt_InsertVersionFailStillRecordsAgentKey(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplate("main/sql", "sql_expert", "db body", []string{"sql"}),
		},
		insertErr: errors.New("simulated"),
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{Prompt: "need SQL help"}
	s.resolveRoutedPrompt(context.Background(), req)

	if req.AgentKey != "sql_expert" {
		t.Fatalf("agent_key should still be recorded on degrade: got %q", req.AgentKey)
	}
	if req.PromptVersionID != nil {
		t.Fatalf("prompt_version_id should be nil on insert failure: %v", req.PromptVersionID)
	}
	if req.BaseInstructions != "db body" {
		t.Fatalf("BaseInstructions should still be injected on insert failure: %q", req.BaseInstructions)
	}
}

func TestResolveRoutedPrompt_NoStoreIsNoop(t *testing.T) {
	t.Parallel()
	s := &service{routerBackend: router.NewRuleRouter()} // no promptStore
	req := &StartRequest{Prompt: "SQL"}
	s.resolveRoutedPrompt(context.Background(), req)
	if req.AgentKey != "" || req.PromptVersionID != nil {
		t.Fatalf("no-store path must be noop: %+v", req)
	}
}

func TestResolveRoutedPrompt_DisabledTemplateSkipped(t *testing.T) {
	t.Parallel()
	tpl := sqlTemplate("main/sql", "sql_expert", "db body", []string{"sql"})
	tpl.Enabled = false
	store := &fakePromptStore{templates: []promptstore.PromptTemplate{tpl}}
	s := newServiceWithRouter(store)

	req := &StartRequest{Prompt: "need SQL"}
	s.resolveRoutedPrompt(context.Background(), req)
	if req.AgentKey != "" || req.BaseInstructions != "" {
		t.Fatalf("disabled template must be ignored: %+v", req)
	}
}

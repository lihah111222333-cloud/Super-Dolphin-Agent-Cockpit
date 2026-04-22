package thread

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

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
	return &service{promptStore: store}
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

// TestResolveRoutedPrompt_EmptyAgentKeyFallsBackToDefaultPromptKey asserts
// that when the caller does not pin an agent_key (the typical UI "open a
// fresh thread" path), pickRoutedTemplate lands on the hardcoded
// `main/default` persona and stamps req.AgentKey with that row's agent_key.
func TestResolveRoutedPrompt_EmptyAgentKeyFallsBackToDefaultPromptKey(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplate("main/sql", "sql_expert", "sql body", nil),
			sqlTemplate(defaultPromptKey, "main", "default body", nil),
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{Prompt: "any user input at all"}
	s.resolveRoutedPrompt(context.Background(), req)

	if req.AgentKey != "main" {
		t.Fatalf("want agent_key=main (from default prompt row), got %q", req.AgentKey)
	}
	if req.PromptKey != defaultPromptKey {
		t.Fatalf("want prompt_key=%q, got %q", defaultPromptKey, req.PromptKey)
	}
	if req.BaseInstructions != "default body" {
		t.Fatalf("want default body injected, got %q", req.BaseInstructions)
	}
	if req.PromptVersionID == nil {
		t.Fatalf("prompt_version_id should be set on successful inject")
	}
}

// TestResolveRoutedPrompt_EmptyAgentKeyAndNoDefaultLeavesRequestUntouched
// asserts the degenerate case where the defaultPromptKey row does not exist
// in the store: we do not invent a fallback, we simply leave req alone and
// let the provider CLI use its bundled system prompt.
func TestResolveRoutedPrompt_EmptyAgentKeyAndNoDefaultLeavesRequestUntouched(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplate("main/sql", "sql_expert", "sql body", nil),
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{Prompt: "anything"}
	s.resolveRoutedPrompt(context.Background(), req)

	if req.AgentKey != "" || req.PromptVersionID != nil || req.BaseInstructions != "" {
		t.Fatalf("expected untouched req when no default row exists, got %+v", req)
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

// TestResolveRoutedPrompt_UnknownExplicitAgentKeyLeavesRequestUntouched:
// caller pinned an agent_key that has no matching (or only disabled) rows.
// We must not silently fall back to the default — the caller asked for a
// specific identity; returning the wrong one would be worse than returning
// none. Upstream CLI then uses its bundled prompt.
func TestResolveRoutedPrompt_UnknownExplicitAgentKeyLeavesRequestUntouched(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplate("main/sql", "sql_expert", "sql body", nil),
			sqlTemplate(defaultPromptKey, "main", "default body", nil),
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{AgentKey: "does-not-exist", Prompt: "whatever"}
	s.resolveRoutedPrompt(context.Background(), req)

	if req.PromptVersionID != nil || req.BaseInstructions != "" || req.PromptKey != "" {
		t.Fatalf("unknown agent_key must not fall through to default: %+v", req)
	}
	if req.AgentKey != "does-not-exist" {
		t.Fatalf("caller-pinned agent_key should be preserved: %q", req.AgentKey)
	}
}

func TestResolveRoutedPrompt_InsertVersionFailStillRecordsAgentKey(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplate("main/sql", "sql_expert", "db body", nil),
		},
		insertErr: errors.New("simulated"),
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{AgentKey: "sql_expert"}
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
	s := &service{} // no promptStore wired
	req := &StartRequest{AgentKey: "sql_expert", Prompt: "SQL"}
	s.resolveRoutedPrompt(context.Background(), req)
	if req.PromptVersionID != nil || req.BaseInstructions != "" || req.PromptKey != "" {
		t.Fatalf("no-store path must be noop: %+v", req)
	}
}

func TestResolveRoutedPrompt_AgentKeyMatchIsCaseAndWhitespaceInsensitive(t *testing.T) {
	t.Parallel()
	tpl := sqlTemplate("main/orchestrator", "  Orchestrator ", "you coordinate", nil)
	store := &fakePromptStore{templates: []promptstore.PromptTemplate{tpl}}
	s := newServiceWithRouter(store)

	req := &StartRequest{AgentKey: "orchestrator"}
	s.resolveRoutedPrompt(context.Background(), req)

	if req.BaseInstructions != "you coordinate" {
		t.Fatalf("case-insensitive agent_key match failed, BaseInstructions=%q", req.BaseInstructions)
	}
	if req.PromptKey != "main/orchestrator" {
		t.Fatalf("prompt_key not recorded: %q", req.PromptKey)
	}
}

// TestResolveRoutedPrompt_ExplicitPromptKeyWinsOverAgentKey: SystemPromptPage's
// "set as launch prompt" preference flows through as req.PromptKey. It must
// resolve the exact row regardless of agent_key, and stamp picked.AgentKey so
// the UI / observability sees a concrete identity.
func TestResolveRoutedPrompt_ExplicitPromptKeyWinsOverAgentKey(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplate("main/sql", "sql_expert", "sql body", nil),
			sqlTemplate("main/launch-fav", "main", "fav launch body", nil),
			sqlTemplate(defaultPromptKey, "main", "default body", nil),
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{PromptKey: "main/launch-fav", AgentKey: "sql_expert"}
	s.resolveRoutedPrompt(context.Background(), req)

	if req.PromptKey != "main/launch-fav" {
		t.Fatalf("want prompt_key=main/launch-fav, got %q", req.PromptKey)
	}
	if req.AgentKey != "main" {
		t.Fatalf("want agent_key stamped from picked template (main), got %q", req.AgentKey)
	}
	if req.BaseInstructions != "fav launch body" {
		t.Fatalf("want fav body, got %q", req.BaseInstructions)
	}
	if req.PromptVersionID == nil {
		t.Fatalf("prompt_version_id should be set on successful inject")
	}
}

// TestResolveRoutedPrompt_UnknownPromptKeyDoesNotFallback: caller pinned an
// exact prompt_key that does not exist. Mirrors the unknown-agent-key
// semantics — refuse to silently substitute the default persona.
func TestResolveRoutedPrompt_UnknownPromptKeyDoesNotFallback(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplate(defaultPromptKey, "main", "default body", nil),
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{PromptKey: "main/missing"}
	s.resolveRoutedPrompt(context.Background(), req)
	if req.BaseInstructions != "" || req.AgentKey != "" {
		t.Fatalf("unknown prompt_key must not fall through to default: %+v", req)
	}
	if req.PromptKey != "main/missing" {
		t.Fatalf("caller-pinned prompt_key should be preserved: %q", req.PromptKey)
	}
	if req.PromptVersionID != nil {
		t.Fatalf("prompt_version_id must remain nil: %v", req.PromptVersionID)
	}
}

// TestResolveRoutedPrompt_DisabledPromptKeyDoesNotFallback: an explicit pin to
// a disabled row must not silently degrade to the default — the operator
// disabling a prompt would expect threads pinned to it to stop using it.
func TestResolveRoutedPrompt_DisabledPromptKeyDoesNotFallback(t *testing.T) {
	t.Parallel()
	tpl := sqlTemplate("main/launch-fav", "main", "fav body", nil)
	tpl.Enabled = false
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			tpl,
			sqlTemplate(defaultPromptKey, "main", "default body", nil),
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{PromptKey: "main/launch-fav"}
	s.resolveRoutedPrompt(context.Background(), req)
	if req.BaseInstructions != "" || req.AgentKey != "" {
		t.Fatalf("disabled pinned prompt must not silently fall back: %+v", req)
	}
}

func TestResolveRoutedPrompt_DisabledTemplateSkipped(t *testing.T) {
	t.Parallel()
	tpl := sqlTemplate("main/sql", "sql_expert", "db body", nil)
	tpl.Enabled = false
	store := &fakePromptStore{templates: []promptstore.PromptTemplate{tpl}}
	s := newServiceWithRouter(store)

	req := &StartRequest{AgentKey: "sql_expert"}
	s.resolveRoutedPrompt(context.Background(), req)
	if req.BaseInstructions != "" || req.PromptKey != "" {
		t.Fatalf("disabled template must be ignored: %+v", req)
	}
}

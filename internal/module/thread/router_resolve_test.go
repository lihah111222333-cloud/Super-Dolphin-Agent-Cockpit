package thread

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	promptpkg "github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
	"github.com/anthropic-ai/super-agent-v3/internal/module/threadprompt"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
)

// fakePromptStore is the minimum surface of promptstore.Store that
// resolveRoutedPrompt exercises. Other methods panic on use so an incorrect
// code path fails loudly.
type fakePromptStore struct {
	promptstore.Store
	templates            []promptstore.PromptTemplate
	listErr              error
	listFilters          []promptstore.ListFilter
	sectionsByTemplateID map[int64][]promptstore.PromptTemplateSection
	recallSections       []promptstore.PromptTemplateSection
	defaultRuleSections  []promptstore.PromptTemplateSection
	recallErr            error
	nextVersionID        int64
	insertErr            error
	lastInsertVersion    promptstore.PromptTemplateVersion
}

func (f *fakePromptStore) List(_ context.Context, filter promptstore.ListFilter) ([]promptstore.PromptTemplate, error) {
	f.listFilters = append(f.listFilters, filter)
	if f.listErr != nil {
		return nil, f.listErr
	}
	return filterFakePromptTemplatesByCWD(f.templates, filter.CWD), nil
}

func (f *fakePromptStore) InsertVersion(_ context.Context, v promptstore.PromptTemplateVersion) (int64, error) {
	f.lastInsertVersion = v
	if f.insertErr != nil {
		return 0, f.insertErr
	}
	f.nextVersionID++
	return f.nextVersionID, nil
}

func (f *fakePromptStore) ListSectionsByTemplateID(_ context.Context, templateID int64) ([]promptstore.PromptTemplateSection, error) {
	return append([]promptstore.PromptTemplateSection(nil), f.sectionsByTemplateID[templateID]...), nil
}
func (f *fakePromptStore) ListRecallSections(_ context.Context, cwd string) ([]promptstore.PromptTemplateSection, error) {
	if f.recallErr != nil {
		return nil, f.recallErr
	}
	return filterFakePromptSectionsByCWD(f.recallSections, cwd), nil
}
func (f *fakePromptStore) ListDefaultRuleSections(_ context.Context, cwd string) ([]promptstore.PromptTemplateSection, error) {
	return filterFakePromptSectionsByCWD(f.defaultRuleSections, cwd), nil
}
func filterFakePromptSectionsByCWD(sections []promptstore.PromptTemplateSection, cwd string) []promptstore.PromptTemplateSection {
	if len(sections) == 0 {
		return nil
	}
	out := make([]promptstore.PromptTemplateSection, 0, len(sections))
	for _, section := range sections {
		if !section.Enabled {
			continue
		}
		if promptTemplateVisibleInCWD(promptstore.TemplateTags(section.TemplateTags), cwd) {
			out = append(out, section)
		}
	}
	return out
}

func newServiceWithRouter(store promptstore.Store) *service {
	return &service{
		promptCatalog:  threadprompt.NewRuntimeCatalog(store, nil),
		matchWhenEval:  promptpkg.EvaluateMatchWhen,
		enableWhenEval: promptpkg.EvaluateEnableWhen,
	}
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

func runtimePromptCatalogAdapterFixture() (*fakePromptStore, runtimePromptCatalog) {
	tpl := sqlTemplate("main/local", "main", "body", []string{"scope.global"})
	tpl.ID = 7
	tpl.Title = "Main"
	tpl.ToolName = "codex"
	tpl.WhenToUse = "when needed"
	tpl.Variables = json.RawMessage(`{"name":"value"}`)
	tpl.MatchWhen = json.RawMessage(`{"language":"go"}`)
	tpl.Priority = 12
	tpl.Description = "desc"
	tpl.CreatedBy = "seed"
	tpl.UpdatedBy = "operator"
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{tpl},
		sectionsByTemplateID: map[int64][]promptstore.PromptTemplateSection{
			7: {
				{TemplateID: 7, SectionKey: "identity", Body: "identity", EnableWhen: json.RawMessage(`{"provider":"codex"}`), Enabled: true},
			},
		},
	}
	catalog := (&service{promptCatalog: threadprompt.NewRuntimeCatalog(store, nil)}).runtimePromptCatalog()
	return store, catalog
}

func TestRuntimePromptCatalogAdapterCopiesTemplateDTO(t *testing.T) {
	t.Parallel()

	store, catalog := runtimePromptCatalogAdapterFixture()
	rows, err := catalog.ListTemplates(context.Background(), runtimePromptListFilter{CWD: "/repo/a", Limit: 200})
	if err != nil {
		t.Fatalf("ListTemplates() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListTemplates() len = %d, want 1", len(rows))
	}
	got := rows[0]
	if got.ID != 7 || got.PromptKey != "main/local" || got.AgentKey != "main" || got.Priority != 12 {
		t.Fatalf("runtime template DTO = %+v, want copied local template", got)
	}
	got.Tags[0] = 'X'
	if string(store.templates[0].Tags) == string(got.Tags) {
		t.Fatal("runtime template tags share backing bytes with store DTO")
	}
}

func TestRuntimePromptCatalogAdapterCopiesSectionDTO(t *testing.T) {
	t.Parallel()

	store, catalog := runtimePromptCatalogAdapterFixture()
	sections, err := catalog.ListSectionsByTemplateID(context.Background(), 7)
	if err != nil {
		t.Fatalf("ListSectionsByTemplateID() error = %v", err)
	}
	if len(sections) != 1 || sections[0].SectionKey != "identity" || string(sections[0].EnableWhen) != `{"provider":"codex"}` {
		t.Fatalf("runtime sections = %#v, want copied section DTO", sections)
	}
	sections[0].EnableWhen[0] = 'X'
	if string(store.sectionsByTemplateID[7][0].EnableWhen) == string(sections[0].EnableWhen) {
		t.Fatal("runtime section enable_when shares backing bytes with store DTO")
	}
}

func TestRuntimePromptCatalogAdapterCopiesVersionDTO(t *testing.T) {
	t.Parallel()

	store, catalog := runtimePromptCatalogAdapterFixture()
	versionID, err := catalog.InsertVersion(context.Background(), runtimePromptTemplateVersion{
		PromptKey:  "main/local",
		Title:      "Main",
		AgentKey:   "main",
		ToolName:   "codex",
		PromptText: "snapshot body",
		Variables:  json.RawMessage(`{"v":1}`),
		Tags:       json.RawMessage(`["scope.global"]`),
		Enabled:    true,
		CreatedBy:  "seed",
		UpdatedBy:  "operator",
	})
	if err != nil {
		t.Fatalf("InsertVersion() error = %v", err)
	}
	if versionID == 0 {
		t.Fatal("InsertVersion() id = 0, want generated id")
	}
	if store.lastInsertVersion.PromptKey != "main/local" || store.lastInsertVersion.PromptText != "snapshot body" {
		t.Fatalf("InsertVersion() stored version = %+v, want copied local DTO", store.lastInsertVersion)
	}
}

func TestConvertStoreSectionsToBlocksSkipsRecallSections(t *testing.T) {
	t.Parallel()

	blocks := convertRuntimeSectionsToBlocks([]runtimePromptTemplateSection{
		{SectionKey: "identity", Region: "static", Body: "base identity", TriggerType: "always", Enabled: true},
		{SectionKey: "recall_sqlc", Region: "dynamic", Body: "recall body", TriggerType: " recall ", Enabled: true},
		{SectionKey: "workflow", Region: "dynamic", Body: "workflow body", Enabled: true},
	})

	if len(blocks) != 2 {
		t.Fatalf("blocks len = %d, want 2: %#v", len(blocks), blocks)
	}
	if blocks[0].Key != "identity" || blocks[1].Key != "workflow" {
		t.Fatalf("block keys = %#v, want identity/workflow", []string{blocks[0].Key, blocks[1].Key})
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

	req := &StartRequest{CWD: "/repo/a", Prompt: "any user input at all"}
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

	req := &StartRequest{CWD: "/repo/a", Prompt: "anything"}
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
	req := &StartRequest{CWD: "/repo/a", AgentKey: "ui_expert", Prompt: "write some react code"}
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

	req := &StartRequest{CWD: "/repo/a", AgentKey: "does-not-exist", Prompt: "whatever"}
	s.resolveRoutedPrompt(context.Background(), req)

	if req.PromptVersionID != nil || req.BaseInstructions != "" || req.PromptKey != "" {
		t.Fatalf("unknown agent_key must not fall through to default: %+v", req)
	}
	if req.AgentKey != "does-not-exist" {
		t.Fatalf("caller-pinned agent_key should be preserved: %q", req.AgentKey)
	}
}

func TestResolveRoutedPrompt_InsertVersionFailFailsFastAfterRecordingAgentKey(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplate("main/sql", "sql_expert", "db body", nil),
		},
		insertErr: errors.New("simulated"),
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{CWD: "/repo/a", AgentKey: "sql_expert"}
	err := s.resolveRoutedPrompt(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "materialize prompt_versions") {
		t.Fatalf("resolveRoutedPrompt() error = %v, want prompt_versions materialization failure", err)
	}

	if req.AgentKey != "sql_expert" {
		t.Fatalf("agent_key should still be recorded before failure: got %q", req.AgentKey)
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

	req := &StartRequest{CWD: "/repo/a", AgentKey: "orchestrator"}
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

	req := &StartRequest{CWD: "/repo/a", PromptKey: "main/launch-fav", AgentKey: "sql_expert"}
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

	req := &StartRequest{CWD: "/repo/a", PromptKey: "main/missing"}
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

	req := &StartRequest{CWD: "/repo/a", PromptKey: "main/launch-fav"}
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

	req := &StartRequest{CWD: "/repo/a", AgentKey: "sql_expert"}
	s.resolveRoutedPrompt(context.Background(), req)
	if req.BaseInstructions != "" || req.PromptKey != "" {
		t.Fatalf("disabled template must be ignored: %+v", req)
	}
}

// TestResolveRoutedPrompt_UnknownPromptKeyMarksStale: when the caller pinned a
// prompt_key that does not exist, the router must surface a stale signal so
// the UI can self-clean its activePromptKey preference. Without this the user
// keeps seeing the "已强制使用" badge for a prompt that no longer affects the
// next thread launch.
func TestResolveRoutedPrompt_UnknownPromptKeyMarksStale(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplate(defaultPromptKey, "main", "default body", nil),
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{CWD: "/repo/a", PromptKey: "main/missing"}
	s.resolveRoutedPrompt(context.Background(), req)

	if !req.PromptKeyStale {
		t.Fatalf("want PromptKeyStale=true when pinned prompt_key is unknown, got %+v", req)
	}
	if req.PromptKey != "main/missing" {
		t.Fatalf("pinned prompt_key must be preserved alongside stale flag: %q", req.PromptKey)
	}
	if req.BaseInstructions != "" || req.AgentKey != "" {
		t.Fatalf("stale path must not inject a fallback persona: %+v", req)
	}
}

// TestResolveRoutedPrompt_DisabledPromptKeyMarksStale: an enabled-false row
// resolved by the pinned prompt_key behaves the same as a deleted row from the
// UI's perspective — both must trip the stale flag.
func TestResolveRoutedPrompt_DisabledPromptKeyMarksStale(t *testing.T) {
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

	req := &StartRequest{CWD: "/repo/a", PromptKey: "main/launch-fav"}
	s.resolveRoutedPrompt(context.Background(), req)

	if !req.PromptKeyStale {
		t.Fatalf("want PromptKeyStale=true when pinned prompt_key is disabled, got %+v", req)
	}
}

// TestResolveRoutedPrompt_KnownPromptKeyKeepsStaleFalse: when the explicit pin
// resolves to an enabled row, the stale flag must remain false. Otherwise the
// UI would clear pref on every successful launch.
func TestResolveRoutedPrompt_KnownPromptKeyKeepsStaleFalse(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplate("main/launch-fav", "main", "fav body", nil),
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{CWD: "/repo/a", PromptKey: "main/launch-fav"}
	s.resolveRoutedPrompt(context.Background(), req)

	if req.PromptKeyStale {
		t.Fatalf("PromptKeyStale must remain false on successful pin: %+v", req)
	}
}

// TestResolveRoutedPrompt_EmptyPromptKeyKeepsStaleFalse: caller did not pin
// any prompt_key — the router takes the default-fallback / auto-route path,
// which must not flip the stale flag (there is nothing to invalidate).
func TestResolveRoutedPrompt_EmptyPromptKeyKeepsStaleFalse(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplate(defaultPromptKey, "main", "default body", nil),
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{CWD: "/repo/a", Prompt: "hello"}
	s.resolveRoutedPrompt(context.Background(), req)

	if req.PromptKeyStale {
		t.Fatalf("PromptKeyStale must remain false when caller did not pin: %+v", req)
	}
}

// TestResolveRoutedPrompt_NoStoreKeepsStaleFalse: store-not-wired degrade path
// is not a stale signal — req.PromptKey is preserved untouched and the UI
// should NOT clear the pref. Otherwise a transient backend wiring issue would
// silently wipe the user's active prompt selection.
func TestResolveRoutedPrompt_NoStoreKeepsStaleFalse(t *testing.T) {
	t.Parallel()
	s := &service{} // no promptStore wired
	req := &StartRequest{PromptKey: "main/launch-fav"}
	s.resolveRoutedPrompt(context.Background(), req)

	if req.PromptKeyStale {
		t.Fatalf("degrade path (no store) must not flip PromptKeyStale: %+v", req)
	}
	if req.PromptKey != "main/launch-fav" {
		t.Fatalf("caller-pinned prompt_key should be preserved on degrade: %q", req.PromptKey)
	}
}

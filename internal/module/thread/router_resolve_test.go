package thread

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	promptpkg "github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
	"github.com/anthropic-ai/super-agent-v3/internal/module/prompt/classifier"
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
func (f *fakePromptStore) ListSectionsByTemplateID(context.Context, int64) ([]promptstore.PromptTemplateSection, error) {
	return nil, nil
}
func (f *fakePromptStore) UpsertSection(context.Context, promptstore.PromptTemplateSection) (*promptstore.PromptTemplateSection, error) {
	panic("unused")
}
func (f *fakePromptStore) DeleteSection(context.Context, int64, string) error {
	panic("unused")
}

func newServiceWithRouter(store *fakePromptStore) *service {
	return &service{
		promptStore:             store,
		classifierFastPath:      classifier.FastPath,
		classifierPrune:         classifier.PruneCandidates,
		classifierMaxCandidates: classifier.MaxCandidatesFromEnv,
		matchWhenEval:           promptpkg.EvaluateMatchWhen,
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

// sqlTemplateWithMatchWhen is a convenience builder for the match_when
// auto-route tests: it accepts a raw JSON expression (use "{}" for opt-in
// always-match) and a priority integer. Pass nil raw to leave match_when
// unset (= opt-out of auto-routing).
func sqlTemplateWithMatchWhen(promptKey, agentKey, text string, matchWhen []byte, priority int) promptstore.PromptTemplate {
	tpl := sqlTemplate(promptKey, agentKey, text, nil)
	tpl.MatchWhen = append(json.RawMessage(nil), matchWhen...)
	tpl.Priority = priority
	return tpl
}

// TestResolveRoutedPrompt_MatchWhenAutoRoutePicksHighestPriority: no caller
// pin, classifier off, two auto-route candidates with match_when={} — the
// higher-priority row wins and its body is injected.
func TestResolveRoutedPrompt_MatchWhenAutoRoutePicksHighestPriority(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplateWithMatchWhen("main/low", "low", "low body", []byte(`{}`), 1),
			sqlTemplateWithMatchWhen("main/hi", "hi", "hi body", []byte(`{}`), 10),
			sqlTemplate(defaultPromptKey, "main", "default body", nil),
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{Prompt: "anything"}
	s.resolveRoutedPrompt(context.Background(), req)

	if req.PromptKey != "main/hi" {
		t.Fatalf("want prompt_key=main/hi (priority=10), got %q", req.PromptKey)
	}
	if req.BaseInstructions != "hi body" {
		t.Fatalf("want hi body injected, got %q", req.BaseInstructions)
	}
}

// TestResolveRoutedPrompt_MatchWhenCWDPrefixMatches: auto-route fires only
// when the CWD prefix rule matches the request's CWD.
func TestResolveRoutedPrompt_MatchWhenCWDPrefixMatches(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "work")
	matchWhen, err := json.Marshal(map[string]string{"cwd_prefix": filepath.ToSlash(root)})
	if err != nil {
		t.Fatalf("marshal match_when: %v", err)
	}
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplateWithMatchWhen("main/work",
				"work", "work body",
				matchWhen, 5),
			sqlTemplate(defaultPromptKey, "main", "default body", nil),
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{CWD: filepath.ToSlash(filepath.Join(root, "project-x")), Prompt: "hey"}
	s.resolveRoutedPrompt(context.Background(), req)
	if req.PromptKey != "main/work" {
		t.Fatalf("want prompt_key=main/work (cwd matched), got %q", req.PromptKey)
	}
}

// TestResolveRoutedPrompt_MatchWhenCWDPrefixMissFallsBackToDefault: when no
// auto-route rule matches, the default persona still wins.
func TestResolveRoutedPrompt_MatchWhenCWDPrefixMissFallsBackToDefault(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplateWithMatchWhen("main/work",
				"work", "work body",
				[]byte(`{"cwd_prefix":"/Users/mac/work"}`), 5),
			sqlTemplate(defaultPromptKey, "main", "default body", nil),
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{CWD: "/tmp/elsewhere", Prompt: "hey"}
	s.resolveRoutedPrompt(context.Background(), req)
	if req.PromptKey != defaultPromptKey {
		t.Fatalf("want fallback to %q, got %q", defaultPromptKey, req.PromptKey)
	}
}

// TestResolveRoutedPrompt_MatchWhenSkippedWhenPromptKeyPinned: caller's
// explicit PromptKey pin takes precedence over any match_when row.
func TestResolveRoutedPrompt_MatchWhenSkippedWhenPromptKeyPinned(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplateWithMatchWhen("main/auto", "auto", "auto body", []byte(`{}`), 99),
			sqlTemplate("main/pinned", "pinned", "pinned body", nil),
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{PromptKey: "main/pinned", Prompt: "whatever"}
	s.resolveRoutedPrompt(context.Background(), req)
	if req.PromptKey != "main/pinned" {
		t.Fatalf("want pinned prompt_key preserved, got %q", req.PromptKey)
	}
	if req.BaseInstructions != "pinned body" {
		t.Fatalf("want pinned body injected, got %q", req.BaseInstructions)
	}
}

// TestResolveRoutedPrompt_MatchWhenSkippedWhenAgentKeyPinned: explicit
// AgentKey also blocks auto-routing — the caller expressed an identity
// preference and we honor it without overriding.
func TestResolveRoutedPrompt_MatchWhenSkippedWhenAgentKeyPinned(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplateWithMatchWhen("main/auto", "auto", "auto body", []byte(`{}`), 99),
			sqlTemplate("main/sql", "sql_expert", "sql body", nil),
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{AgentKey: "sql_expert", Prompt: "whatever"}
	s.resolveRoutedPrompt(context.Background(), req)
	if req.PromptKey != "main/sql" {
		t.Fatalf("want main/sql (agent-key pinned), got %q", req.PromptKey)
	}
}

// TestResolveRoutedPrompt_MatchWhenDoesNotTrustDotCWD: "." is not a trusted
// workspace root, so cwd_prefix / cwd_glob rules must not match process cwd.
func TestResolveRoutedPrompt_MatchWhenDoesNotTrustDotCWD(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplateWithMatchWhen("main/by-wd", "by-wd", "by-wd body", []byte(`{"cwd_prefix":"/"}`), 5),
			sqlTemplate(defaultPromptKey, "main", "default body", nil),
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{CWD: ".", Prompt: "hey"}
	s.resolveRoutedPrompt(context.Background(), req)
	if req.PromptKey != defaultPromptKey {
		t.Fatalf("want default prompt for untrusted dot CWD, got %q", req.PromptKey)
	}
}

// TestResolveRoutedPrompt_MatchWhenDisabledRowIgnored: disabled rows are
// filtered out of the auto-route candidate list even if their match_when
// would otherwise pass.
func TestResolveRoutedPrompt_MatchWhenDisabledRowIgnored(t *testing.T) {
	t.Parallel()
	tpl := sqlTemplateWithMatchWhen("main/auto", "auto", "auto body", []byte(`{}`), 99)
	tpl.Enabled = false
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			tpl,
			sqlTemplate(defaultPromptKey, "main", "default body", nil),
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{Prompt: "whatever"}
	s.resolveRoutedPrompt(context.Background(), req)
	if req.PromptKey != defaultPromptKey {
		t.Fatalf("disabled auto-route row must be ignored, got %q", req.PromptKey)
	}
}

// TestResolveRoutedPrompt_MatchWhenSpecificBeatsFallback: when a specific
// (non-empty match_when) row matches, it must win even though a higher-
// priority fallback (match_when={}) row exists. Reproduces the production
// bug where main/claude-style (priority=150, match_when={}) shadowed every
// user-created tag-based prompt. Uses tags_has string form because the
// template-level match_when DSL only supports string today; array form is
// the section-level dialect and would test something different.
func TestResolveRoutedPrompt_MatchWhenSpecificBeatsFallback(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplateWithMatchWhen("main/claude-style", "main",
				"fallback body", []byte(`{}`), 150),
			sqlTemplateWithMatchWhen("user/sql", "sql_expert",
				"specific body", []byte(`{"tags_has":"sql"}`), 0),
			sqlTemplate(defaultPromptKey, "main", "default body", nil),
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{Prompt: "please write me some sql"}
	s.resolveRoutedPrompt(context.Background(), req)

	if req.PromptKey != "user/sql" {
		t.Fatalf("want user/sql (specific beats fallback), got %q", req.PromptKey)
	}
	if req.BaseInstructions != "specific body" {
		t.Fatalf("want specific body injected, got %q", req.BaseInstructions)
	}
}

// TestResolveRoutedPrompt_MatchWhenFallbackKicksInWhenNoSpecificMatches: with
// no specific match (tags_has miss), the {} fallback row should pick up the
// auto-route slot before main/default ultimate fallback.
func TestResolveRoutedPrompt_MatchWhenFallbackKicksInWhenNoSpecificMatches(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplateWithMatchWhen("main/claude-style", "claude_main",
				"fallback body", []byte(`{}`), 150),
			sqlTemplateWithMatchWhen("user/sql", "sql_expert",
				"specific body", []byte(`{"tags_has":"sql"}`), 0),
			sqlTemplate(defaultPromptKey, "main", "default body", nil),
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{Prompt: "hello world, nothing to do with the tag keyword"}
	s.resolveRoutedPrompt(context.Background(), req)

	if req.PromptKey != "main/claude-style" {
		t.Fatalf("want main/claude-style (fallback after specific miss), got %q", req.PromptKey)
	}
	if req.BaseInstructions != "fallback body" {
		t.Fatalf("want fallback body injected, got %q", req.BaseInstructions)
	}
}

// TestResolveRoutedPrompt_MatchWhenSpecificPoolPriorityOrder: within the
// specific pool, the higher-priority row must win when both match. Guards
// the per-pool DESC ordering after the two-stage split.
func TestResolveRoutedPrompt_MatchWhenSpecificPoolPriorityOrder(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplateWithMatchWhen("user/sql-low", "sql_low",
				"low body", []byte(`{"tags_has":"sql"}`), 1),
			sqlTemplateWithMatchWhen("user/sql-hi", "sql_hi",
				"hi body", []byte(`{"tags_has":"sql"}`), 10),
			sqlTemplateWithMatchWhen("main/claude-style", "main",
				"fallback body", []byte(`{}`), 150),
			sqlTemplate(defaultPromptKey, "main", "default body", nil),
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{Prompt: "sql please"}
	s.resolveRoutedPrompt(context.Background(), req)

	if req.PromptKey != "user/sql-hi" {
		t.Fatalf("want user/sql-hi (higher priority specific), got %q", req.PromptKey)
	}
	if req.BaseInstructions != "hi body" {
		t.Fatalf("want hi body injected, got %q", req.BaseInstructions)
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

	req := &StartRequest{PromptKey: "main/missing"}
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

	req := &StartRequest{PromptKey: "main/launch-fav"}
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

	req := &StartRequest{PromptKey: "main/launch-fav"}
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

	req := &StartRequest{Prompt: "hello"}
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

// TestResolveRoutedPrompt_MatchWhenSpecificBeatsFallback_ArrayForm: same shape
// as MatchWhenSpecificBeatsFallback, but uses tags_has=["sql","db"] array form,
// which is what SystemPromptPage UI auto-generates from chip tags and what real
// DB rows store. Hot fix 3f8c27ff lets matchWhenKeyMatches accept []any via
// matchSectionTagsHas; this test guards that the array DSL stays wired into
// the two-stage auto-route path. Reverting hot fix turns matchWhenStringValue
// on a []any into "", matchTagsHas("","...") returns false, specific pool
// becomes empty, fallback main/claude-style ({}) wins — test goes red.
func TestResolveRoutedPrompt_MatchWhenSpecificBeatsFallback_ArrayForm(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplateWithMatchWhen("main/claude-style", "main",
				"fallback body", []byte(`{}`), 150),
			sqlTemplateWithMatchWhen("user/sql", "sql_expert",
				"specific body", []byte(`{"tags_has":["sql","db"]}`), 0),
			sqlTemplate(defaultPromptKey, "main", "default body", nil),
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{Prompt: "please write me some sql"}
	s.resolveRoutedPrompt(context.Background(), req)

	if req.PromptKey != "user/sql" {
		t.Fatalf("want user/sql (array tags_has hit), got %q", req.PromptKey)
	}
	if req.BaseInstructions != "specific body" {
		t.Fatalf("want specific body injected, got %q", req.BaseInstructions)
	}
}

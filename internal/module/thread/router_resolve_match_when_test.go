package thread

import (
	"context"
	"encoding/json"
	"testing"

	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
)

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

func matchWhenCWDPrefix(cwd string) []byte {
	raw, err := json.Marshal(map[string]string{"cwd_prefix": resolvePromptCWD(cwd)})
	if err != nil {
		// archguard:ignore panic_count -- static map[string]string test fixture must always marshal.
		panic(err)
	}
	return raw
}

// TestResolveRoutedPrompt_MatchWhenAutoRoutePicksHighestPriority: no caller
// pin, two auto-route candidates with match_when={} — the
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

	req := &StartRequest{CWD: "/repo/a", Prompt: "anything"}
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
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplateWithMatchWhen("main/work",
				"work", "work body",
				matchWhenCWDPrefix("/Users/mac/work"), 5),
			sqlTemplate(defaultPromptKey, "main", "default body", nil),
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{CWD: "/Users/mac/work/project-x", Prompt: "hey"}
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
				matchWhenCWDPrefix("/Users/mac/work"), 5),
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

	req := &StartRequest{CWD: "/repo/a", PromptKey: "main/pinned", Prompt: "whatever"}
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

	req := &StartRequest{CWD: "/repo/a", AgentKey: "sql_expert", Prompt: "whatever"}
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

	req := &StartRequest{CWD: "/repo/a", Prompt: "whatever"}
	s.resolveRoutedPrompt(context.Background(), req)
	if req.PromptKey != defaultPromptKey {
		t.Fatalf("disabled auto-route row must be ignored, got %q", req.PromptKey)
	}
}

// TestResolveRoutedPrompt_MatchWhenSpecificBeatsFallback: when a specific
// (non-empty match_when) row matches, it must win even though a higher-priority
// fallback (match_when={}) row exists. Uses cwd_prefix because template-level
// tags_has keyword routing is retired.
func TestResolveRoutedPrompt_MatchWhenSpecificBeatsFallback(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplateWithMatchWhen("main/general-zh", "main",
				"fallback body", []byte(`{}`), 150),
			sqlTemplateWithMatchWhen("user/sql", "sql_expert",
				"specific body", matchWhenCWDPrefix("/repo/sql"), 0),
			sqlTemplate(defaultPromptKey, "main", "default body", nil),
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{CWD: "/repo/sql/project", Prompt: "please write me some sql"}
	s.resolveRoutedPrompt(context.Background(), req)

	if req.PromptKey != "user/sql" {
		t.Fatalf("want user/sql (specific beats fallback), got %q", req.PromptKey)
	}
	if req.BaseInstructions != "specific body" {
		t.Fatalf("want specific body injected, got %q", req.BaseInstructions)
	}
}

// TestResolveRoutedPrompt_MatchWhenFallbackKicksInWhenNoSpecificMatches: with
// no specific match, the {} fallback row should pick up the auto-route slot
// before main/default ultimate fallback. tags_has rows are no longer specific
// matches because template-level keyword routing is retired.
func TestResolveRoutedPrompt_MatchWhenFallbackKicksInWhenNoSpecificMatches(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplateWithMatchWhen("main/general-zh", "general_main",
				"fallback body", []byte(`{}`), 150),
			sqlTemplateWithMatchWhen("user/sql", "sql_expert",
				"specific body", []byte(`{"tags_has":"sql"}`), 0),
			sqlTemplate(defaultPromptKey, "main", "default body", nil),
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{CWD: "/repo/a", Prompt: "hello world, nothing to do with the tag keyword"}
	s.resolveRoutedPrompt(context.Background(), req)

	if req.PromptKey != "main/general-zh" {
		t.Fatalf("want main/general-zh (fallback after specific miss), got %q", req.PromptKey)
	}
	if req.BaseInstructions != "fallback body" {
		t.Fatalf("want fallback body injected, got %q", req.BaseInstructions)
	}
}

// TestResolveRoutedPrompt_MatchWhenSpecificPoolPriorityOrder: within the
// specific pool, the higher-priority row must win when both match. Guards the
// per-pool DESC ordering after the two-stage split.
func TestResolveRoutedPrompt_MatchWhenSpecificPoolPriorityOrder(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplateWithMatchWhen("user/sql-low", "sql_low",
				"low body", matchWhenCWDPrefix("/repo/sql"), 1),
			sqlTemplateWithMatchWhen("user/sql-hi", "sql_hi",
				"hi body", matchWhenCWDPrefix("/repo/sql"), 10),
			sqlTemplateWithMatchWhen("main/general-zh", "main",
				"fallback body", []byte(`{}`), 150),
			sqlTemplate(defaultPromptKey, "main", "default body", nil),
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{CWD: "/repo/sql/project", Prompt: "sql please"}
	s.resolveRoutedPrompt(context.Background(), req)

	if req.PromptKey != "user/sql-hi" {
		t.Fatalf("want user/sql-hi (higher priority specific), got %q", req.PromptKey)
	}
	if req.BaseInstructions != "hi body" {
		t.Fatalf("want hi body injected, got %q", req.BaseInstructions)
	}
}

package prompt

import (
	"context"
	"strings"
	"testing"
)

// TestAssembleStart_CachedPrefixBytewiseStable verifies the Phase 3 invariant:
// given the same BuildCtx inputs and a frozen PROMPT_START_CURRENT_DATE, two
// successive AssembleStart calls produce a bytewise-identical
// Boundary.CachedPrefix AND BaseInstructions. The CachedPrefix is what gets
// routed to the --system-prompt flag on Claude CLI in Phase 3, and
// Anthropic's org-level ephemeral prompt cache (5-minute TTL) only hits when
// this value does not change across turns.
func TestAssembleStart_CachedPrefixBytewiseStable(t *testing.T) {
	t.Setenv(envClaudeSimple, "")
	t.Setenv(envPromptStartCurrentDate, "2026-04-22")
	cwd := t.TempDir()

	newAssembly := func() StartAssembly {
		svc := NewService(&Config{}, nil)
		got, err := svc.AssembleStart(context.Background(), StartInput{
			Provider: "claudecli",
			CWD:      cwd,
			Language: "English",
			Model:    "claude-sonnet-4",
		})
		if err != nil {
			t.Fatalf("AssembleStart() error = %v", err)
		}
		return got
	}

	first := newAssembly()
	second := newAssembly()

	if first.Boundary == nil || second.Boundary == nil {
		t.Fatalf("Boundary missing: first=%#v second=%#v", first.Boundary, second.Boundary)
	}
	if first.Boundary.CachedPrefix != second.Boundary.CachedPrefix {
		t.Fatalf("CachedPrefix diverged:\nfirst  = %q\nsecond = %q",
			first.Boundary.CachedPrefix, second.Boundary.CachedPrefix)
	}
	if strings.TrimSpace(first.Boundary.CachedPrefix) == "" {
		t.Fatal("CachedPrefix is empty; static sections should always produce content")
	}
	// Phase 3 promise: BaseInstructions is also bytewise stable. Previously
	// the per-start user meta (currentDate, runtimeExtras) and System Context
	// block (gitStatus) were embedded into BaseInstructions, breaking
	// stability. After Phase 3 these flow through the structured UserContext
	// / SystemContext fields instead.
	if first.BaseInstructions != second.BaseInstructions {
		t.Fatalf("BaseInstructions diverged across calls with same BuildCtx:\nfirst  = %q\nsecond = %q",
			first.BaseInstructions, second.BaseInstructions)
	}
	// Guard against the previous leak shapes. Note: the literal token
	// "<system-reminder>" appears as a benign mention in the
	// system_constraints section body; the real leak is the wrapper block
	// containing userMeta ("Today's date is ..."). Check the distinctive
	// currentDate phrase instead of the bare tag.
	if strings.Contains(first.BaseInstructions, "Today's date is") {
		t.Fatalf("BaseInstructions leaked currentDate payload; userMeta must live in StartAssembly.UserContext only:\n%s",
			first.BaseInstructions)
	}
	if strings.Contains(first.BaseInstructions, "# System Context") {
		t.Fatalf("BaseInstructions leaked System Context block; gitStatus must live in StartAssembly.SystemContext only:\n%s",
			first.BaseInstructions)
	}
	// Positive assertion: the same data must be available via the structured
	// fields so provider bridges can route it into the synthetic user meta.
	if _, ok := first.UserContext["currentDate"]; !ok {
		t.Fatalf("UserContext missing currentDate after Phase 3 split: %#v", first.UserContext)
	}
}

// TestAssembleStart_PopulatesUserMetaFields verifies Phase 0 contract extension:
// StartAssembly exposes UserContext / UserContextText / SystemContext fields in
// parallel with the legacy BaseInstructions embedding, so Phase 3 provider
// bridges can consume them without pulling data out of the BaseInstructions
// string.
func TestAssembleStart_PopulatesUserMetaFields(t *testing.T) {
	t.Setenv(envClaudeSimple, "")
	t.Setenv(envPromptStartCurrentDate, "2026-04-22")

	svc := NewService(&Config{}, nil)
	assembly, err := svc.AssembleStart(context.Background(), StartInput{
		Provider: "claudecli",
		CWD:      t.TempDir(),
	})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}

	date, ok := assembly.UserContext["currentDate"]
	if !ok {
		t.Fatalf("UserContext missing currentDate: %#v", assembly.UserContext)
	}
	if !strings.Contains(date, "2026-04-22") {
		t.Fatalf("UserContext[currentDate] = %q, want the frozen date", date)
	}
	if !strings.Contains(assembly.UserContextText, "currentDate") {
		t.Fatalf("UserContextText missing currentDate heading: %q", assembly.UserContextText)
	}
	// SystemContext may be nil when the cwd is not a git repo and no cache
	// breaker is configured; if present, each entry must be non-empty.
	for key, value := range assembly.SystemContext {
		if strings.TrimSpace(value) == "" {
			t.Fatalf("SystemContext[%q] populated but empty", key)
		}
	}
}

// TestSimpleStartAssembly_ThreeLineForm asserts the CLAUDE_CODE_SIMPLE fast
// path emits exactly the three-line form matching Claude Code's ultraSimple
// behavior (prompts.ts L444-454). Phase 1 tightened this path so that
// UserContext/UserContextText/SystemContext are intentionally left empty —
// the fast path is meant to be load-bearing minimum, not a richer variant.
func TestSimpleStartAssembly_ThreeLineForm(t *testing.T) {
	t.Setenv(envClaudeSimple, "1")
	t.Setenv(envPromptStartCurrentDate, "2026-04-22")

	svc := NewService(&Config{}, nil)
	cwd := t.TempDir()
	assembly, err := svc.AssembleStart(context.Background(), StartInput{
		Provider: "claudecli",
		CWD:      cwd,
	})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	want := simpleStartIdentityLine + "\nCWD: " + cwd + "\nDate: 2026-04-22"
	if assembly.BaseInstructions != want {
		t.Fatalf("BaseInstructions = %q, want strict three-line form %q", assembly.BaseInstructions, want)
	}
	if assembly.UserContext != nil || assembly.UserContextText != "" || assembly.SystemContext != nil {
		t.Fatalf("ultraSimple must leave UserContext/SystemContext empty: %#v / %q / %#v", assembly.UserContext, assembly.UserContextText, assembly.SystemContext)
	}
}

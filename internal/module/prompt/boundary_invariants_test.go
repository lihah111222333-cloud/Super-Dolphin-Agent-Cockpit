package prompt

import (
	"context"
	"strings"
	"testing"
)

// TestBoundary_DynamicLivesInUncachedTail_MCPInstructions asserts the
// invariant Phase 3 will lean on: DANGEROUS-class dynamic sections (here
// mcp_instructions) must render into Boundary.UncachedTail, never into
// Boundary.CachedPrefix. The CachedPrefix is what Phase 3 routes through
// --system-prompt and is required to stay bytewise stable for Anthropic
// org ephemeral cache (5-min TTL) to hit; MCP server connect/disconnect is
// expected to invalidate the tail, not the prefix.
func TestBoundary_DynamicLivesInUncachedTail_MCPInstructions(t *testing.T) {
	t.Setenv(envClaudeSimple, "")
	t.Setenv(envPromptStartCurrentDate, "2026-04-22")

	svc := NewService(&Config{}, nil)
	cwd := t.TempDir()
	assembly, err := svc.AssembleStart(context.Background(), StartInput{
		Provider: "claudecli",
		CWD:      cwd,
		Language: "English",
		Model:    "claude-sonnet-4",
		MCPSnapshot: MCPSnapshot{
			Servers: []string{"alpha"},
			Instructions: map[string]string{
				"alpha": "MCP-ALPHA-BOUNDARY-MARKER",
			},
		},
	})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	if assembly.Boundary == nil {
		t.Fatal("Boundary missing; expected static/dynamic split metadata")
	}
	if strings.Contains(assembly.Boundary.CachedPrefix, "MCP-ALPHA-BOUNDARY-MARKER") {
		t.Fatalf("CachedPrefix leaked DANGEROUS MCP content; cache would miss on every MCP change:\n%s",
			assembly.Boundary.CachedPrefix)
	}
	if !strings.Contains(assembly.Boundary.UncachedTail, "MCP-ALPHA-BOUNDARY-MARKER") {
		t.Fatalf("UncachedTail missing MCP content; expected it to land in tail:\n%s",
			assembly.Boundary.UncachedTail)
	}
	if !strings.Contains(assembly.Boundary.CachedPrefix, "You are Claude Code") {
		t.Fatalf("CachedPrefix missing stable identity header:\n%s",
			assembly.Boundary.CachedPrefix)
	}
}

// TestBoundary_MemoryIsStartOnly asserts that the memory behavior-rules
// section is start-only: AssembleTurn must not emit it again. This matches
// Claude Code's memory semantics (rules injected once at thread start, never
// re-emitted per turn) and keeps per-turn dynamic content bounded.
func TestBoundary_MemoryIsStartOnly(t *testing.T) {
	spec, ok := dynamicSectionSpecForName(DynamicSectionMemory)
	if !ok {
		t.Fatal("memory slot missing from dynamic matrix")
	}
	if !spec.startOnly {
		t.Fatalf("memory slot must be startOnly=true; got spec=%#v", spec)
	}
}

// TestBoundary_RuntimeExtrasExcludesStaticSections guards against a subtle
// duplication regression: earlier drafts of includeRuntimeExtraSection only
// filtered known dynamic slots, so every static section (identity,
// system_constraints, ...) was mirrored into UserContext.runtimeExtras.
// That would ship the full static body twice per turn (once in the cacheable
// system prompt prefix, once in the synthetic user meta) and poison the
// prompt cache. The filter must drop PromptRegionStatic sections outright.
func TestBoundary_RuntimeExtrasExcludesStaticSections(t *testing.T) {
	t.Setenv(envClaudeSimple, "")
	t.Setenv(envPromptStartCurrentDate, "2026-04-22")

	svc := NewService(&Config{}, nil)
	assembly, err := svc.AssembleStart(context.Background(), StartInput{
		Provider: "claudecli",
		CWD:      t.TempDir(),
		Language: "English",
		Model:    "claude-sonnet-4",
	})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	extras := assembly.UserContext["runtimeExtras"]
	if extras == "" {
		return
	}
	// A leak would carry verbatim phrases from the static section bodies.
	leakMarkers := []string{
		"You are Claude Code, Anthropic's official CLI", // identity
		"System constraints:",
		"Engineering principles:",
		"Executing actions with care:",
		"Tool preferences:",
		"Tone and style:",
		"Output efficiency:",
	}
	for _, marker := range leakMarkers {
		if strings.Contains(extras, marker) {
			t.Fatalf("runtimeExtras leaks static section %q; static content must stay in cacheable prefix only:\nextras=%s", marker, extras)
		}
	}
}

// TestBoundary_MemorySectionRelativeOrder pins the order in which the four
// memory-related dynamic slots are scheduled. Rules (`memory`) must come
// before the rendered MEMORY.md (`memory_entrypoint`) so the model sees how
// to use memory before seeing the raw index. AgentMemory and MemoryContext
// follow. A regression that swaps these would silently confuse the model
// ("index without rules") and is otherwise unguarded — spec order is the
// only source of truth, see Phase 1 plan section 13.
func TestBoundary_MemorySectionRelativeOrder(t *testing.T) {
	wantOrder := []string{
		DynamicSectionMemory,
		DynamicSectionMemoryEntrypoint,
		DynamicSectionAgentMemory,
		DynamicSectionMemoryContext,
	}
	prev := -1
	for _, name := range wantOrder {
		spec, ok := dynamicSectionSpecForName(name)
		if !ok {
			t.Fatalf("section %q missing from dynamic spec list", name)
		}
		if spec.order <= prev {
			t.Fatalf("section %q order=%d not strictly greater than previous (=%d); want order memory < memory_entrypoint < agent_memory < memory_context",
				name, spec.order, prev)
		}
		prev = spec.order
	}
}

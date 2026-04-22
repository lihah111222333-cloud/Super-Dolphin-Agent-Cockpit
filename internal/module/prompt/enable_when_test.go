package prompt

import (
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestEvaluateEnableWhen_EmptyAndInvalidFailOpen(t *testing.T) {
	t.Parallel()
	ctx := contract.BuildCtx{Language: "zh"}
	cases := map[string][]byte{
		"nil":          nil,
		"empty":        []byte(""),
		"whitespace":   []byte("   "),
		"null":         []byte("null"),
		"empty_object": []byte("{}"),
		"malformed":    []byte("{not json"),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if !EvaluateEnableWhen(raw, ctx) {
				t.Fatalf("%s: expected fail-open (true), got false", name)
			}
		})
	}
}

func TestEvaluateEnableWhen_StringFieldEquality(t *testing.T) {
	t.Parallel()
	ctx := contract.BuildCtx{Language: "zh", Provider: "claude-cli", Model: "sonnet-4"}
	if !EvaluateEnableWhen([]byte(`{"language":"zh"}`), ctx) {
		t.Fatalf("expected match on language=zh")
	}
	if EvaluateEnableWhen([]byte(`{"language":"en"}`), ctx) {
		t.Fatalf("expected mismatch on language=en")
	}
	// Multi-key AND: all must match.
	if !EvaluateEnableWhen([]byte(`{"language":"zh","provider":"claude-cli"}`), ctx) {
		t.Fatalf("expected AND match")
	}
	if EvaluateEnableWhen([]byte(`{"language":"zh","provider":"codex"}`), ctx) {
		t.Fatalf("AND must drop when any key mismatches")
	}
}

func TestEvaluateEnableWhen_BoolField(t *testing.T) {
	t.Parallel()
	worktreeCtx := contract.BuildCtx{IsWorktree: true}
	plainCtx := contract.BuildCtx{IsWorktree: false}
	if !EvaluateEnableWhen([]byte(`{"isWorktree":true}`), worktreeCtx) {
		t.Fatalf("expected match when IsWorktree=true")
	}
	if EvaluateEnableWhen([]byte(`{"isWorktree":true}`), plainCtx) {
		t.Fatalf("expected mismatch when IsWorktree=false")
	}
	if !EvaluateEnableWhen([]byte(`{"isWorktree":false}`), plainCtx) {
		t.Fatalf("expected match when IsWorktree=false")
	}
}

func TestEvaluateEnableWhen_SessionFlagsNested(t *testing.T) {
	t.Parallel()
	ctx := contract.BuildCtx{SessionFlags: map[string]bool{"debug": true, "verbose": false}}
	if !EvaluateEnableWhen([]byte(`{"sessionFlags.debug":true}`), ctx) {
		t.Fatalf("expected match on sessionFlags.debug=true")
	}
	if EvaluateEnableWhen([]byte(`{"sessionFlags.debug":false}`), ctx) {
		t.Fatalf("expected mismatch when asking for debug=false but it's true")
	}
	if !EvaluateEnableWhen([]byte(`{"sessionFlags.verbose":false}`), ctx) {
		t.Fatalf("expected match on sessionFlags.verbose=false")
	}
	// Absent flag resolves to false (map zero value).
	if !EvaluateEnableWhen([]byte(`{"sessionFlags.missing":false}`), ctx) {
		t.Fatalf("absent flag should match sessionFlags.missing=false")
	}
	if EvaluateEnableWhen([]byte(`{"sessionFlags.missing":true}`), ctx) {
		t.Fatalf("absent flag must not match sessionFlags.missing=true")
	}
	// Empty suffix must fail-closed (unknown key).
	if EvaluateEnableWhen([]byte(`{"sessionFlags.":true}`), ctx) {
		t.Fatalf("empty sessionFlags suffix must be rejected")
	}
}

func TestEvaluateEnableWhen_UnknownKeyFailsClosed(t *testing.T) {
	t.Parallel()
	ctx := contract.BuildCtx{Language: "zh"}
	if EvaluateEnableWhen([]byte(`{"doesNotExist":"zh"}`), ctx) {
		t.Fatalf("unknown keys must fail-closed to avoid silent bypass")
	}
}

func TestEvaluateEnableWhen_TypeMismatch(t *testing.T) {
	t.Parallel()
	ctx := contract.BuildCtx{Language: "zh", IsWorktree: true}
	// JSON number vs bool/string: the evaluator only accepts bool/string,
	// everything else is a mismatch.
	if EvaluateEnableWhen([]byte(`{"language":42}`), ctx) {
		t.Fatalf("numeric want against string field must mismatch")
	}
	if EvaluateEnableWhen([]byte(`{"isWorktree":"true"}`), ctx) {
		t.Fatalf("string want against bool field must mismatch")
	}
}

func TestMergeTemplateSections_FiltersByEnableWhen(t *testing.T) {
	t.Parallel()
	ctx := contract.BuildCtx{Language: "zh", IsWorktree: true}
	blocks := []contract.BaseInstructionBlock{
		{Key: "identity", Region: contract.PromptRegionStatic, Ordinal: 0, Body: "stay-static"},
		{Key: "zh_only", Region: contract.PromptRegionDynamic, Ordinal: 0, Body: "zh-only",
			EnableWhen: []byte(`{"language":"zh"}`)},
		{Key: "en_only", Region: contract.PromptRegionDynamic, Ordinal: 1, Body: "en-only",
			EnableWhen: []byte(`{"language":"en"}`)},
		{Key: "worktree", Region: contract.PromptRegionDynamic, Ordinal: 2, Body: "in-worktree",
			EnableWhen: []byte(`{"isWorktree":true}`)},
		{Key: "always", Region: contract.PromptRegionDynamic, Ordinal: 3, Body: "always-on",
			EnableWhen: []byte(`{}`)},
	}
	got := mergeTemplateSections(nil, blocks, ctx)
	wantNames := []string{"tpl:identity", "tpl:zh_only", "tpl:worktree", "tpl:always"}
	if len(got) != len(wantNames) {
		t.Fatalf("resolved len=%d want=%d (%+v)", len(got), len(wantNames), got)
	}
	for i, want := range wantNames {
		if got[i].Name != want {
			t.Fatalf("resolved[%d].Name=%q want=%q", i, got[i].Name, want)
		}
	}
	// Legacy callers pass nil blocks → unchanged.
	if out := mergeTemplateSections(nil, nil, ctx); len(out) != 0 {
		t.Fatalf("nil blocks should pass through as empty; got %d", len(out))
	}
}

// TestMergeAndBoundary_StaticToCachedDynamicToUncached is the E2E contract
// test for Step 1/2/3b: given a realistic mix of BaseInstructionBlock
// (static + dynamic, some gated, some unconditional), the rendered
// PromptAssemblyBoundary must place static blocks into CachedPrefix (so they
// end up in --system-prompt / cacheable) and dynamic blocks into
// UncachedTail (so they end up in --append-system-prompt / volatile).
// EnableWhen filtering must run before region partitioning so gated-out
// blocks show up in neither segment.
func TestMergeAndBoundary_StaticToCachedDynamicToUncached(t *testing.T) {
	t.Parallel()

	buildCtx := contract.BuildCtx{
		Language:   "zh",
		IsWorktree: true,
		Provider:   "claude-cli",
	}
	blocks := []contract.BaseInstructionBlock{
		// Two static blocks — always injected, both should appear in
		// CachedPrefix in ordinal order.
		{Key: "identity", Region: contract.PromptRegionStatic, Ordinal: 0, Body: "STATIC-IDENTITY"},
		{Key: "tool_prefs", Region: contract.PromptRegionStatic, Ordinal: 10, Body: "STATIC-TOOL-PREFS"},
		// Dynamic block gated on isWorktree=true → should pass (ctx matches).
		{Key: "worktree", Region: contract.PromptRegionDynamic, Ordinal: 0, Body: "DYN-WORKTREE",
			EnableWhen: []byte(`{"isWorktree":true}`)},
		// Dynamic block gated on language=en → should be filtered out (ctx=zh).
		{Key: "en_only", Region: contract.PromptRegionDynamic, Ordinal: 10, Body: "DYN-EN-ONLY",
			EnableWhen: []byte(`{"language":"en"}`)},
		// Unconditional dynamic block → should pass.
		{Key: "always", Region: contract.PromptRegionDynamic, Ordinal: 20, Body: "DYN-ALWAYS"},
	}
	resolved := mergeTemplateSections(nil, blocks, buildCtx)
	boundary := startAssemblyBoundary(resolved, "" /* no legacy tail */)
	if boundary == nil {
		t.Fatalf("expected non-nil boundary")
	}

	// Static contract: both static blocks land in CachedPrefix.
	for _, want := range []string{"STATIC-IDENTITY", "STATIC-TOOL-PREFS"} {
		if !strings.Contains(boundary.CachedPrefix, want) {
			t.Fatalf("CachedPrefix missing %q:\n%s", want, boundary.CachedPrefix)
		}
	}
	// Static blocks MUST NOT leak into UncachedTail (region partitioning).
	for _, leak := range []string{"STATIC-IDENTITY", "STATIC-TOOL-PREFS"} {
		if strings.Contains(boundary.UncachedTail, leak) {
			t.Fatalf("static block %q leaked into UncachedTail:\n%s", leak, boundary.UncachedTail)
		}
	}

	// Dynamic contract: worktree + always land in UncachedTail; en_only dropped.
	for _, want := range []string{"DYN-WORKTREE", "DYN-ALWAYS"} {
		if !strings.Contains(boundary.UncachedTail, want) {
			t.Fatalf("UncachedTail missing %q:\n%s", want, boundary.UncachedTail)
		}
	}
	if strings.Contains(boundary.UncachedTail, "DYN-EN-ONLY") {
		t.Fatalf("EnableWhen-filtered block leaked into UncachedTail:\n%s", boundary.UncachedTail)
	}
	if strings.Contains(boundary.CachedPrefix, "DYN-") {
		t.Fatalf("dynamic block leaked into CachedPrefix:\n%s", boundary.CachedPrefix)
	}

	// Ordinal order within CachedPrefix: identity (0) must precede tool_prefs (10).
	identityIdx := strings.Index(boundary.CachedPrefix, "STATIC-IDENTITY")
	toolsIdx := strings.Index(boundary.CachedPrefix, "STATIC-TOOL-PREFS")
	if identityIdx < 0 || toolsIdx < 0 || identityIdx > toolsIdx {
		t.Fatalf("CachedPrefix ordinal order broken: identity@%d tools@%d\n%s",
			identityIdx, toolsIdx, boundary.CachedPrefix)
	}
}

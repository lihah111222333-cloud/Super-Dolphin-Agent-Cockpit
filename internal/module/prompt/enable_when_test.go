package prompt

import (
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestEnableWhen_EmptyAndInvalidFailOpen(t *testing.T) {
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
			if !EvaluateEnableWhen(raw, ctx, "") {
				t.Fatalf("%s: expected fail-open (true), got false", name)
			}
		})
	}
}

func TestEnableWhen_StringFieldEquality(t *testing.T) {
	t.Parallel()
	ctx := contract.BuildCtx{Language: "zh", Provider: "claude-cli", Model: "sonnet-4"}
	if !EvaluateEnableWhen([]byte(`{"language":"zh"}`), ctx, "") {
		t.Fatalf("expected match on language=zh")
	}
	if EvaluateEnableWhen([]byte(`{"language":"en"}`), ctx, "") {
		t.Fatalf("expected mismatch on language=en")
	}
	// Multi-key AND: all must match.
	if !EvaluateEnableWhen([]byte(`{"language":"zh","provider":"claude-cli"}`), ctx, "") {
		t.Fatalf("expected AND match")
	}
	if EvaluateEnableWhen([]byte(`{"language":"zh","provider":"codex"}`), ctx, "") {
		t.Fatalf("AND must drop when any key mismatches")
	}
}

func TestEnableWhen_BoolField(t *testing.T) {
	t.Parallel()
	worktreeCtx := contract.BuildCtx{IsWorktree: true}
	plainCtx := contract.BuildCtx{IsWorktree: false}
	if !EvaluateEnableWhen([]byte(`{"isWorktree":true}`), worktreeCtx, "") {
		t.Fatalf("expected match when IsWorktree=true")
	}
	if EvaluateEnableWhen([]byte(`{"isWorktree":true}`), plainCtx, "") {
		t.Fatalf("expected mismatch when IsWorktree=false")
	}
	if !EvaluateEnableWhen([]byte(`{"isWorktree":false}`), plainCtx, "") {
		t.Fatalf("expected match when IsWorktree=false")
	}
}

func TestEnableWhen_SessionFlagsNested(t *testing.T) {
	t.Parallel()
	ctx := contract.BuildCtx{SessionFlags: map[string]bool{"debug": true, "verbose": false}}
	if !EvaluateEnableWhen([]byte(`{"sessionFlags.debug":true}`), ctx, "") {
		t.Fatalf("expected match on sessionFlags.debug=true")
	}
	if EvaluateEnableWhen([]byte(`{"sessionFlags.debug":false}`), ctx, "") {
		t.Fatalf("expected mismatch when asking for debug=false but it's true")
	}
	if !EvaluateEnableWhen([]byte(`{"sessionFlags.verbose":false}`), ctx, "") {
		t.Fatalf("expected match on sessionFlags.verbose=false")
	}
	// Absent flag resolves to false (map zero value).
	if !EvaluateEnableWhen([]byte(`{"sessionFlags.missing":false}`), ctx, "") {
		t.Fatalf("absent flag should match sessionFlags.missing=false")
	}
	if EvaluateEnableWhen([]byte(`{"sessionFlags.missing":true}`), ctx, "") {
		t.Fatalf("absent flag must not match sessionFlags.missing=true")
	}
	// Empty suffix must fail-closed (unknown key).
	if EvaluateEnableWhen([]byte(`{"sessionFlags.":true}`), ctx, "") {
		t.Fatalf("empty sessionFlags suffix must be rejected")
	}
}

func TestEnableWhen_UnknownKeyFailsClosed(t *testing.T) {
	t.Parallel()
	ctx := contract.BuildCtx{Language: "zh"}
	if EvaluateEnableWhen([]byte(`{"doesNotExist":"zh"}`), ctx, "") {
		t.Fatalf("unknown keys must fail-closed to avoid silent bypass")
	}
}

func TestEnableWhen_TypeMismatch(t *testing.T) {
	t.Parallel()
	ctx := contract.BuildCtx{Language: "zh", IsWorktree: true}
	// JSON number vs bool/string: the evaluator only accepts bool/string,
	// everything else is a mismatch.
	if EvaluateEnableWhen([]byte(`{"language":42}`), ctx, "") {
		t.Fatalf("numeric want against string field must mismatch")
	}
	if EvaluateEnableWhen([]byte(`{"isWorktree":"true"}`), ctx, "") {
		t.Fatalf("string want against bool field must mismatch")
	}
}

func TestEnableWhen_TagsHas(t *testing.T) {
	t.Parallel()
	ctx := contract.BuildCtx{}

	// Single string value: case-insensitive substring match against userPrompt.
	if !EvaluateEnableWhen([]byte(`{"tags_has":"refactor"}`), ctx, "please refactor the Foo interface") {
		t.Fatalf("tags_has string must hit when userPrompt contains the keyword")
	}
	if !EvaluateEnableWhen([]byte(`{"tags_has":"REFACTOR"}`), ctx, "please refactor the Foo interface") {
		t.Fatalf("tags_has must be case-insensitive")
	}
	if EvaluateEnableWhen([]byte(`{"tags_has":"refactor"}`), ctx, "add a new feature") {
		t.Fatalf("tags_has string must miss when keyword absent")
	}
	if EvaluateEnableWhen([]byte(`{"tags_has":"refactor"}`), ctx, "") {
		t.Fatalf("tags_has must fail-closed on empty userPrompt")
	}

	// Array value: OR across entries.
	arr := []byte(`{"tags_has":["rename","trace","impact"]}`)
	if !EvaluateEnableWhen(arr, ctx, "analyze the impact of changing Bar") {
		t.Fatalf("tags_has array must hit when any entry matches")
	}
	if !EvaluateEnableWhen(arr, ctx, "rename the symbol") {
		t.Fatalf("tags_has array must hit on any element (case-insensitive)")
	}
	if EvaluateEnableWhen(arr, ctx, "say hello") {
		t.Fatalf("tags_has array must miss when no element matches")
	}

	// Combined with other keys: AND semantics.
	bothKeys := []byte(`{"language":"zh","tags_has":"refactor"}`)
	zh := contract.BuildCtx{Language: "zh"}
	if !EvaluateEnableWhen(bothKeys, zh, "帮我 refactor 这段逻辑") {
		t.Fatalf("AND with language should pass when both match")
	}
	if EvaluateEnableWhen(bothKeys, zh, "just a greeting") {
		t.Fatalf("AND with language should fail when tags_has misses")
	}
	en := contract.BuildCtx{Language: "en"}
	if EvaluateEnableWhen(bothKeys, en, "please refactor this") {
		t.Fatalf("AND with language should fail when language mismatches")
	}

	// Invalid value types: non-string / non-array → mismatch.
	if EvaluateEnableWhen([]byte(`{"tags_has":42}`), ctx, "anything") {
		t.Fatalf("tags_has with non-string non-array value must fail-closed")
	}
}

func TestEnableWhen_EnabledToolsHas(t *testing.T) {
	t.Parallel()
	withLsp := contract.BuildCtx{EnabledTools: []string{"code_run", "grep", "file"}}
	withLegacyLsp := contract.BuildCtx{EnabledTools: []string{"code_run", "lsp_grep", "lsp_file"}}
	noLsp := contract.BuildCtx{EnabledTools: []string{"code_run"}}
	noTools := contract.BuildCtx{}

	// Single string hit / miss.
	if !EvaluateEnableWhen([]byte(`{"enabled_tools_has":"grep"}`), withLsp, "") {
		t.Fatalf("must hit when EnabledTools contains the tool")
	}
	if !EvaluateEnableWhen([]byte(`{"enabled_tools_has":"lsp_grep"}`), withLsp, "") {
		t.Fatalf("legacy wanted tool name must match canonical EnabledTools")
	}
	if !EvaluateEnableWhen([]byte(`{"enabled_tools_has":"grep"}`), withLegacyLsp, "") {
		t.Fatalf("canonical wanted tool name must match legacy EnabledTools")
	}
	if EvaluateEnableWhen([]byte(`{"enabled_tools_has":"grep"}`), noLsp, "") {
		t.Fatalf("must miss when EnabledTools lacks the tool")
	}
	if EvaluateEnableWhen([]byte(`{"enabled_tools_has":"grep"}`), noTools, "") {
		t.Fatalf("must miss when EnabledTools is empty")
	}

	// Exact match, not substring.
	if EvaluateEnableWhen([]byte(`{"enabled_tools_has":"lsp"}`), withLsp, "") {
		t.Fatalf("must require exact tool name, not substring")
	}

	// Array value: OR across entries.
	arr := []byte(`{"enabled_tools_has":["xref","grep","inspect"]}`)
	if !EvaluateEnableWhen(arr, withLsp, "") {
		t.Fatalf("array must hit when any element matches")
	}
	if EvaluateEnableWhen(arr, noLsp, "") {
		t.Fatalf("array must miss when no element matches")
	}

	// AND with tags_has: both must satisfy.
	bothKeys := []byte(`{"enabled_tools_has":"grep","tags_has":"refactor"}`)
	if !EvaluateEnableWhen(bothKeys, withLsp, "please refactor") {
		t.Fatalf("AND should pass when both conditions match")
	}
	if EvaluateEnableWhen(bothKeys, withLsp, "hello") {
		t.Fatalf("AND should fail when tags_has misses")
	}
	if EvaluateEnableWhen(bothKeys, noLsp, "please refactor") {
		t.Fatalf("AND should fail when tool missing")
	}

	// Invalid value type → mismatch.
	if EvaluateEnableWhen([]byte(`{"enabled_tools_has":42}`), withLsp, "") {
		t.Fatalf("non-string non-array value must fail-closed")
	}

	// Empty string in array is ignored, not treated as match-all.
	if EvaluateEnableWhen([]byte(`{"enabled_tools_has":""}`), withLsp, "") {
		t.Fatalf("empty string want must miss (never match)")
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
	got := mergeTemplateSections(nil, blocks, ctx, "")
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
	if out := mergeTemplateSections(nil, nil, ctx, ""); len(out) != 0 {
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
	resolved := mergeTemplateSections(nil, blocks, buildCtx, "")
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

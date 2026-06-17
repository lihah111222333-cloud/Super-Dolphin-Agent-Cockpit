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
	assertEnableWhen(t, "tags_has string hit", []byte(`{"tags_has":"refactor"}`), ctx, "please refactor the Foo interface", true)
	assertEnableWhen(t, "tags_has case-insensitive", []byte(`{"tags_has":"REFACTOR"}`), ctx, "please refactor the Foo interface", true)
	assertEnableWhen(t, "tags_has string miss", []byte(`{"tags_has":"refactor"}`), ctx, "add a new feature", false)
	assertEnableWhen(t, "tags_has empty prompt", []byte(`{"tags_has":"refactor"}`), ctx, "", false)

	// Array value: OR across entries.
	arr := []byte(`{"tags_has":["rename","trace","impact"]}`)
	assertEnableWhen(t, "tags_has array impact", arr, ctx, "analyze the impact of changing Bar", true)
	assertEnableWhen(t, "tags_has array rename", arr, ctx, "rename the symbol", true)
	assertEnableWhen(t, "tags_has array miss", arr, ctx, "say hello", false)

	// Combined with other keys: AND semantics.
	bothKeys := []byte(`{"language":"zh","tags_has":"refactor"}`)
	zh := contract.BuildCtx{Language: "zh"}
	assertEnableWhen(t, "tags_has AND pass", bothKeys, zh, "帮我 refactor 这段逻辑", true)
	assertEnableWhen(t, "tags_has AND tag miss", bothKeys, zh, "just a greeting", false)
	en := contract.BuildCtx{Language: "en"}
	assertEnableWhen(t, "tags_has AND language miss", bothKeys, en, "please refactor this", false)

	// Invalid value types: non-string / non-array → mismatch.
	assertEnableWhen(t, "tags_has invalid type", []byte(`{"tags_has":42}`), ctx, "anything", false)
}

func TestEnableWhen_EnabledToolsHas(t *testing.T) {
	t.Parallel()
	withLsp := contract.BuildCtx{EnabledTools: []string{"exec_command", "grep", "file"}}
	withLegacyLsp := contract.BuildCtx{EnabledTools: []string{"exec_command", "lsp_grep", "lsp_file"}}
	noLsp := contract.BuildCtx{EnabledTools: []string{"exec_command"}}
	noTools := contract.BuildCtx{}

	// Single string hit / miss.
	assertEnableWhen(t, "enabled tool direct hit", []byte(`{"enabled_tools_has":"grep"}`), withLsp, "", true)
	assertEnableWhen(t, "enabled tool legacy want", []byte(`{"enabled_tools_has":"lsp_grep"}`), withLsp, "", true)
	assertEnableWhen(t, "enabled tool canonical want", []byte(`{"enabled_tools_has":"grep"}`), withLegacyLsp, "", true)
	assertEnableWhen(t, "enabled orchestration launch legacy runtime name",
		[]byte(`{"enabled_tools_has":"launch_agent"}`),
		contract.BuildCtx{EnabledTools: []string{"orchestration_launch_agent"}}, "", true)
	assertEnableWhen(t, "enabled orchestration report legacy runtime name",
		[]byte(`{"enabled_tools_has":"get_agent_report"}`),
		contract.BuildCtx{EnabledTools: []string{"orchestration_get_agent_report"}}, "", true)
	assertEnableWhen(t, "enabled tool miss", []byte(`{"enabled_tools_has":"grep"}`), noLsp, "", false)
	assertEnableWhen(t, "enabled tool empty", []byte(`{"enabled_tools_has":"grep"}`), noTools, "", false)

	// Exact match, not substring.
	assertEnableWhen(t, "enabled tool exact only", []byte(`{"enabled_tools_has":"lsp"}`), withLsp, "", false)

	// Array value: OR across entries.
	arr := []byte(`{"enabled_tools_has":["xref","grep","inspect"]}`)
	assertEnableWhen(t, "enabled tool array hit", arr, withLsp, "", true)
	assertEnableWhen(t, "enabled tool array miss", arr, noLsp, "", false)

	// AND with tags_has: both must satisfy.
	bothKeys := []byte(`{"enabled_tools_has":"grep","tags_has":"refactor"}`)
	assertEnableWhen(t, "enabled tool AND pass", bothKeys, withLsp, "please refactor", true)
	assertEnableWhen(t, "enabled tool AND tags miss", bothKeys, withLsp, "hello", false)
	assertEnableWhen(t, "enabled tool AND tool miss", bothKeys, noLsp, "please refactor", false)

	// Invalid value type → mismatch.
	assertEnableWhen(t, "enabled tool invalid type", []byte(`{"enabled_tools_has":42}`), withLsp, "", false)

	// Empty string in array is ignored, not treated as match-all.
	assertEnableWhen(t, "enabled tool empty string", []byte(`{"enabled_tools_has":""}`), withLsp, "", false)
}

func TestEnableWhen_EnabledToolsAll(t *testing.T) {
	t.Parallel()
	withDAGTools := contract.BuildCtx{EnabledTools: []string{
		"list_models",
		"prompt_list",
		"command_list",
		"shared_file_list",
		"task_create_dag",
		"task_get_dag",
		"task_dag_apply_ops",
		"task_start_dag",
	}}
	missingApplyOps := contract.BuildCtx{EnabledTools: []string{
		"list_models",
		"prompt_list",
		"command_list",
		"shared_file_list",
		"task_create_dag",
		"task_get_dag",
		"task_start_dag",
	}}

	allTools := []byte(`{"enabled_tools_all":["list_models","prompt_list","command_list","shared_file_list","task_create_dag","task_get_dag","task_dag_apply_ops","task_start_dag"]}`)
	assertEnableWhen(t, "enabled tools all pass", allTools, withDAGTools, "", true)
	assertEnableWhen(t, "enabled tools all missing one", allTools, missingApplyOps, "", false)
	assertEnableWhen(t, "enabled tools all invalid type", []byte(`{"enabled_tools_all":"grep"}`), withDAGTools, "", false)
}

func assertEnableWhen(t *testing.T, name string, raw []byte, ctx contract.BuildCtx, prompt string, want bool) {
	t.Helper()
	if got := EvaluateEnableWhen(raw, ctx, prompt); got != want {
		t.Fatalf("%s: EvaluateEnableWhen() = %v, want %v", name, got, want)
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
	assertContainsAll(t, "CachedPrefix", boundary.CachedPrefix, []string{"STATIC-IDENTITY", "STATIC-TOOL-PREFS"})
	// Static blocks MUST NOT leak into UncachedTail (region partitioning).
	assertContainsNone(t, "UncachedTail static leak", boundary.UncachedTail, []string{"STATIC-IDENTITY", "STATIC-TOOL-PREFS"})

	// Dynamic contract: worktree + always land in UncachedTail; en_only dropped.
	assertContainsAll(t, "UncachedTail", boundary.UncachedTail, []string{"DYN-WORKTREE", "DYN-ALWAYS"})
	assertContainsNone(t, "UncachedTail gated leak", boundary.UncachedTail, []string{"DYN-EN-ONLY"})
	assertContainsNone(t, "CachedPrefix dynamic leak", boundary.CachedPrefix, []string{"DYN-"})

	// Ordinal order within CachedPrefix: identity (0) must precede tool_prefs (10).
	assertSubstringOrder(t, "CachedPrefix", boundary.CachedPrefix, "STATIC-IDENTITY", "STATIC-TOOL-PREFS")
}

func assertContainsAll(t *testing.T, label, text string, values []string) {
	t.Helper()
	for _, want := range values {
		if !strings.Contains(text, want) {
			t.Fatalf("%s missing %q:\n%s", label, want, text)
		}
	}
}

func assertContainsNone(t *testing.T, label, text string, values []string) {
	t.Helper()
	for _, leak := range values {
		if strings.Contains(text, leak) {
			t.Fatalf("%s contains %q:\n%s", label, leak, text)
		}
	}
}

func assertSubstringOrder(t *testing.T, label, text, before, after string) {
	t.Helper()
	beforeIdx := strings.Index(text, before)
	afterIdx := strings.Index(text, after)
	if beforeIdx < 0 || afterIdx < 0 || beforeIdx > afterIdx {
		t.Fatalf("%s order broken: %s@%d %s@%d\n%s", label, before, beforeIdx, after, afterIdx, text)
	}
}

// Package prompt source note: this file preserves the original enable_when.go
// logic and the former match_when.go logic after merging them to keep the
// prompt package within the production-file budget while retaining both source
// comment blocks (§10.31).
package prompt

import (
	"encoding/json"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// EvaluateEnableWhen decides whether a prompt_template_section should be
// injected for the given BuildCtx and (Start-time) user prompt.
//
// Expression shape (JSONB kv match, AND semantics across all keys):
//
//	null / empty / invalid JSON → always inject (no gating)
//	{}                          → always inject
//	{"language":"zh"}           → inject only when buildCtx.Language == "zh"
//	{"isWorktree":true}         → inject only when buildCtx.IsWorktree is true
//	{"provider":"claude-cli",
//	 "model":"sonnet-4"}        → both must match (AND)
//	{"sessionFlags.debug":true} → buildCtx.SessionFlags["debug"] == true
//	{"tags_has":"refactor"}     → case-insensitive substring in userPrompt
//	{"tags_has":["rename","trace","impact"]}
//	                            → OR across the array; any substring hit passes
//	{"enabled_tools_has":"grep"}
//	                            → BuildCtx.EnabledTools contains this short tool name
//	{"enabled_tools_has":["grep","xref"]}
//	                            → OR across the array; any match passes
//	{"enabled_tools_all":["task_create_dag","task_start_dag"]}
//	                            → AND across the array; every listed tool must be present
//
// Step 3b kept the DSL deliberately tiny; tags_has and enabled_tools_* are the
// intentional extensions (still no $not / $in / regex) added to enable
// userPrompt-driven and tool-availability section gating without growing the
// schema. All other mismatches or lookup misses still drop the section
// (fail-closed). Unknown keys (not listed above and not under sessionFlags.)
// are treated as a mismatch.
// EvaluateEnableWhen 处理evaluateenablewhen。
func EvaluateEnableWhen(raw []byte, buildCtx contract.BuildCtx, userPrompt string) bool {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return true
	}
	var expr map[string]any
	if err := json.Unmarshal([]byte(trimmed), &expr); err != nil {
		// Malformed expression: fail-open to preserve backwards-compatibility
		// with any rows someone wrote by hand. Operators get a clearer signal
		// via logs than by having their sections silently vanish.
		return true
	}
	if len(expr) == 0 {
		return true
	}
	for key, want := range expr {
		if !sectionEnableKeyMatches(key, want, buildCtx, userPrompt) {
			return false
		}
	}
	return true
}

// sectionEnableKeyMatches dispatches a single enable_when key. tags_has is
// the userPrompt-aware extension; everything else falls through to the shared
// BuildCtx equality table used by both enable_when and match_when.
func sectionEnableKeyMatches(key string, want any, buildCtx contract.BuildCtx, userPrompt string) bool {
	switch key {
	case "tags_has":
		return matchSectionTagsHas(want, userPrompt)
	case "enabled_tools_has":
		return matchEnabledToolsHas(want, buildCtx.EnabledTools)
	case "enabled_tools_all":
		return matchEnabledToolsAll(want, buildCtx.EnabledTools)
	}
	got, ok := resolveEnableWhenField(key, buildCtx)
	if !ok {
		return false
	}
	return enableWhenValueEquals(got, want)
}

// matchEnabledToolsHas implements enabled_tools_has for section-level
// enable_when: string value matches one tool; array value is OR across each
// string element. Comparison is exact (case-sensitive) against canonical short
// tool names in BuildCtx.EnabledTools (e.g. "grep", "exec_command"). Legacy
// "lsp_*" names are accepted as aliases during the tool rename migration.
// matchEnabledToolsHas 判断enabled工具has是否匹配。
func matchEnabledToolsHas(want any, enabled []string) bool {
	if len(enabled) == 0 {
		return false
	}
	switch v := want.(type) {
	case string:
		return containsExact(enabled, v)
	case []any:
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				continue
			}
			if containsExact(enabled, s) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// matchEnabledToolsAll 判断enabled工具all是否匹配。
func matchEnabledToolsAll(want any, enabled []string) bool {
	if len(enabled) == 0 {
		return false
	}
	items, ok := want.([]any)
	if !ok || len(items) == 0 {
		return false
	}
	for _, item := range items {
		s, ok := item.(string)
		if !ok || !containsExact(enabled, s) {
			return false
		}
	}
	return true
}

func containsExact(values []string, want string) bool {
	if want == "" {
		return false
	}
	want = canonicalPromptToolName(want)
	for _, v := range values {
		if canonicalPromptToolName(v) == want {
			return true
		}
	}
	return false
}

// canonicalPromptToolName 处理canonicalprompt工具名称。
func canonicalPromptToolName(name string) string {
	switch strings.TrimSpace(name) {
	case "lsp_file":
		return "file"
	case "lsp_grep":
		return "grep"
	case "lsp_inspect":
		return "inspect"
	case "lsp_xref":
		return "xref"
	case "lsp_structure":
		return "structure"
	case "lsp_edit":
		return "edit"
	case "lsp_completion":
		return "completion"
	case "orchestration_launch_agent":
		return "launch_agent"
	case "orchestration_get_agent_report":
		return "get_agent_report"
	default:
		return strings.TrimSpace(name)
	}
}

func canonicalPromptLSPTools(values []string) []string {
	tools := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range sortedPromptValues(values) {
		tool := canonicalPromptToolName(value)
		if !isPromptLSPToolName(tool) {
			continue
		}
		if _, ok := seen[tool]; ok {
			continue
		}
		seen[tool] = struct{}{}
		tools = append(tools, tool)
	}
	return tools
}

func isPromptLSPToolName(name string) bool {
	switch strings.TrimSpace(name) {
	case "file", "grep", "inspect", "xref", "structure", "edit", "completion":
		return true
	default:
		return false
	}
}

// matchSectionTagsHas implements tags_has for section-level enable_when:
// string value is a single case-insensitive substring probe; array value is
// OR across each string element.
// matchSectionTagsHas 判断sectiontagshas是否匹配。
func matchSectionTagsHas(want any, userPrompt string) bool {
	if strings.TrimSpace(userPrompt) == "" {
		return false
	}
	switch v := want.(type) {
	case string:
		return matchTagsHas(v, userPrompt)
	case []any:
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				continue
			}
			if matchTagsHas(s, userPrompt) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// resolveEnableWhenField returns the runtime value of the requested BuildCtx
// field. The second return is false when the key is unrecognized; callers
// treat that as a mismatch (fail-closed for unknown gates).
// resolveEnableWhenField 解析enablewhen字段。
func resolveEnableWhenField(key string, c contract.BuildCtx) (any, bool) {
	if strings.HasPrefix(key, "sessionFlags.") {
		name := strings.TrimPrefix(key, "sessionFlags.")
		if name == "" {
			return nil, false
		}
		return c.SessionFlags[name], true
	}
	switch key {
	case "cwd":
		return c.CWD, true
	case "gitRoot":
		return c.GitRoot, true
	case "isWorktree":
		return c.IsWorktree, true
	case "language":
		return c.Language, true
	case "provider":
		return c.Provider, true
	case "model":
		return c.Model, true
	default:
		return nil, false
	}
}

// enableWhenValueEquals compares a BuildCtx-derived value with the JSON-decoded
// expected value. JSON decoding yields bool / string / float64; we normalize
// string-string and bool-bool and coerce map-missing SessionFlags (nil) to a
// zero-value bool so {"sessionFlags.debug":false} can match when the flag is
// absent.
// enableWhenValueEquals 处理enablewhen值equals。
func enableWhenValueEquals(got, want any) bool {
	if got == nil {
		// Absent SessionFlags entry resolves to the zero value of its type;
		// for bool that's false. Only treat it as a match when the caller
		// actually asked for false.
		if w, ok := want.(bool); ok {
			return !w
		}
		return false
	}
	switch g := got.(type) {
	case bool:
		w, ok := want.(bool)
		return ok && g == w
	case string:
		w, ok := want.(string)
		return ok && g == w
	default:
		return false
	}
}

// MatchWhen source note: the functions below were merged from the former
// match_when.go so the prompt package can stay within the package file budget
// without changing behavior.

// EvaluateMatchWhen decides whether a prompt_template should be picked by
// the "auto-route" rung of the router (between explicit pins and main/default
// fallback). Semantics are deliberately different from EvaluateEnableWhen:
//
//   - nil / empty        → NOT a match (template opted out of auto-routing)
//   - malformed JSON     → NOT a match (fail-closed — we'd rather skip than
//     accidentally route to a template with broken rules)
//   - "{}"                → always match (opt-in with no filter; relies on priority)
//   - JSON kv AND match  → all keys must satisfy the current BuildCtx
//
// Supported keys (a + b + d per 决策 1):
//
//	cwd_glob        filepath.Match glob against buildCtx.CWD
//	                  e.g. "*/projects/data-*"
//	cwd_prefix      strings.HasPrefix against buildCtx.CWD
//	                  e.g. "/Users/mac/work"
//	language        buildCtx.Language == value
//	provider        buildCtx.Provider == value
//	model           buildCtx.Model == value
//	isWorktree      buildCtx.IsWorktree == value
//	sessionFlags.X  buildCtx.SessionFlags[X] == value
//	tags_has        retired for template match_when; always fails closed
//
// The two layers (template match_when + section enable_when) never share data
// at call time; both read the same BuildCtx but are evaluated in different
// phases (routing vs assembling).
func EvaluateMatchWhen(raw []byte, buildCtx contract.BuildCtx, userPrompt string) bool {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return false
	}
	var expr map[string]any
	if err := json.Unmarshal([]byte(trimmed), &expr); err != nil {
		return false
	}
	if len(expr) == 0 {
		return true
	}
	for key, want := range expr {
		if !matchWhenKeyMatches(key, want, buildCtx, userPrompt) {
			return false
		}
	}
	return true
}

// matchWhenKeyMatches handles one key from a match_when expression. It
// dispatches between the custom glob/prefix keys and the shared
// BuildCtx-field equality table reused from EvaluateEnableWhen.
func matchWhenKeyMatches(key string, want any, buildCtx contract.BuildCtx, userPrompt string) bool {
	switch key {
	case "cwd_glob":
		return matchCWDGlob(matchWhenStringValue(want), buildCtx.CWD)
	case "cwd_prefix":
		return matchCWDPrefix(matchWhenStringValue(want), buildCtx.CWD)
	case "tags_has":
		// Template-level keyword routing is retired. Keep section-level
		// enable_when.tags_has in sectionEnableKeyMatches above.
		return false
	default:
		// Fall through to the shared BuildCtx equality table (language /
		// provider / model / cwd / gitRoot / isWorktree / sessionFlags.*).
		got, ok := resolveEnableWhenField(key, buildCtx)
		if !ok {
			return false
		}
		return enableWhenValueEquals(got, want)
	}
}

package prompt

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// EvaluateMatchWhen decides whether a prompt_template should be picked by
// the "auto-route" rung of the router (between classifier and main/default
// fallback). Semantics are deliberately different from EvaluateEnableWhen:
//
//   - nil / empty        → NOT a match (template opted out of auto-routing)
//   - malformed JSON     → NOT a match (fail-closed — we'd rather skip than
//                          accidentally route to a template with broken rules)
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
//	tags_has        case-insensitive substring match of value in userPrompt
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
// dispatches between the custom glob/prefix/tags keys and the shared
// BuildCtx-field equality table reused from EvaluateEnableWhen.
func matchWhenKeyMatches(key string, want any, buildCtx contract.BuildCtx, userPrompt string) bool {
	switch key {
	case "cwd_glob":
		pattern, ok := want.(string)
		if !ok || pattern == "" {
			return false
		}
		matched, err := filepath.Match(pattern, buildCtx.CWD)
		return err == nil && matched
	case "cwd_prefix":
		prefix, ok := want.(string)
		if !ok || prefix == "" {
			return false
		}
		return strings.HasPrefix(buildCtx.CWD, prefix)
	case "tags_has":
		keyword, ok := want.(string)
		if !ok || keyword == "" {
			return false
		}
		return strings.Contains(
			strings.ToLower(userPrompt),
			strings.ToLower(keyword),
		)
	}
	// Fall through to the shared BuildCtx equality table (language / provider /
	// model / cwd / gitRoot / isWorktree / sessionFlags.*).
	got, ok := resolveEnableWhenField(key, buildCtx)
	if !ok {
		return false
	}
	return enableWhenValueEquals(got, want)
}

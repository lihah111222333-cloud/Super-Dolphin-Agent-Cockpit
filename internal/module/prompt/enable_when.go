package prompt

import (
	"encoding/json"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// EvaluateEnableWhen decides whether a prompt_template_section should be
// injected for the given BuildCtx.
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
//
// Step 3b keeps the DSL deliberately tiny (equality-only, no $not / $in / regex);
// any mismatch or lookup miss drops the section. The schema column stays JSONB
// so a future evaluator can grow without migrating data.
//
// Unknown keys (not in fieldExtractors and not under sessionFlags.) are treated
// as a mismatch — fail-closed is the safer default for a feature gate.
func EvaluateEnableWhen(raw []byte, buildCtx contract.BuildCtx) bool {
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
		got, ok := resolveEnableWhenField(key, buildCtx)
		if !ok {
			return false
		}
		if !enableWhenValueEquals(got, want) {
			return false
		}
	}
	return true
}

// resolveEnableWhenField returns the runtime value of the requested BuildCtx
// field. The second return is false when the key is unrecognized; callers
// treat that as a mismatch (fail-closed for unknown gates).
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

package prompt

import (
	"path/filepath"
	"strings"
)

// MatchWhen helper source note: these helpers support the merged
// EvaluateMatchWhen/matchWhenKeyMatches evaluator after the former match_when.go
// contents were folded into enable_when.go.
func matchWhenStringValue(want any) string {
	value, _ := want.(string)
	return value
}

func matchCWDGlob(pattern string, cwd string) bool {
	if pattern == "" || cwd == "" {
		return false
	}
	matched, err := filepath.Match(pattern, cwd)
	return err == nil && matched
}

func matchCWDPrefix(prefix string, cwd string) bool {
	if prefix == "" || cwd == "" {
		return false
	}
	return strings.HasPrefix(cwd, prefix)
}

func matchTagsHas(keyword string, userPrompt string) bool {
	if keyword == "" || userPrompt == "" {
		return false
	}
	return strings.Contains(
		strings.ToLower(userPrompt),
		strings.ToLower(keyword),
	)
}

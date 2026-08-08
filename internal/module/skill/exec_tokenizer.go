package skill

import (
	"path/filepath"
	"strings"
)

type shellToken struct {
	text         string
	commandStart bool
}

func normalizeExecToken(value string) string {
	name := strings.TrimSpace(value)
	if name == "" {
		return ""
	}
	return strings.ToLower(filepath.Base(name))
}

func literalExecTokens(base string, args []string) []shellToken {
	tokens := make([]shellToken, 0, len(args)+1)
	if strings.TrimSpace(base) != "" {
		tokens = append(tokens, shellToken{text: base, commandStart: true})
	}
	for _, arg := range args {
		if strings.TrimSpace(arg) != "" {
			tokens = append(tokens, shellToken{text: arg})
		}
	}
	return tokens
}

func detectDangerousTokens(tokens []shellToken) string {
	for i, token := range tokens {
		if !token.commandStart {
			continue
		}
		if blocked := dangerousCommandAt(tokens, i, 0); blocked != "" {
			return blocked
		}
	}
	return ""
}

func dangerousCommandAt(tokens []shellToken, idx, depth int) string {
	if idx < 0 || idx >= len(tokens) || depth > 8 {
		return ""
	}
	name := normalizeExecToken(tokens[idx].text)
	if blocked := isDangerousBasename(name); blocked != "" {
		return blocked
	}
	return isDangerousWrapper(tokens, idx, depth, name)
}

func isEnvAssignmentToken(value string) bool {
	key, _, ok := strings.Cut(value, "=")
	return ok && key != "" && isEnvAssignmentKey(key)
}

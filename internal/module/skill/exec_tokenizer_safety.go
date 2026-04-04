package skill

import "strings"

func isDangerousBasename(name string) string {
	switch {
	case isBlockedCommand(name):
		return name
	case isShellInterpreter(name):
		return name
	default:
		return ""
	}
}

func isDangerousWrapper(tokens []shellToken, idx, depth int, name string) string {
	return isDangerousChain(tokens, wrappedCommandIndex(tokens, idx, name), depth)
}

func wrappedCommandIndex(tokens []shellToken, idx int, name string) int {
	rule, ok := wrapperSkipRules[name]
	if !ok {
		return -1
	}
	return skipOptionsAndFindCommand(tokens, idx+1, rule)
}

func isDangerousChain(tokens []shellToken, idx, depth int) string {
	if idx < 0 {
		return ""
	}
	return dangerousCommandAt(tokens, idx, depth+1)
}

func nextCommandIndex(tokens []shellToken, start int) int {
	for i := start; i < len(tokens); i++ {
		if strings.TrimSpace(tokens[i].text) != "" {
			return i
		}
	}
	return -1
}

func isEnvAssignmentKey(key string) bool {
	for i := 0; i < len(key); i++ {
		if !isEnvAssignmentChar(key[i]) {
			return false
		}
	}
	return true
}

func isEnvAssignmentChar(ch byte) bool {
	return ch == '_' || isASCIIAlpha(ch) || isASCIIDigit(ch)
}

func isASCIIAlpha(ch byte) bool {
	return ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z'
}

func isASCIIDigit(ch byte) bool {
	return ch >= '0' && ch <= '9'
}

func looksLikeSignedInteger(value string) bool {
	if value == "" {
		return false
	}
	if value[0] == '+' || value[0] == '-' {
		value = value[1:]
	}
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

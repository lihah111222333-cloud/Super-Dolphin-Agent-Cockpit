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

// wrappedCommandIndex 处理wrapped命令索引。
func wrappedCommandIndex(tokens []shellToken, idx int, name string) int {
	switch name {
	case "env":
		return nextEnvCommandIndex(tokens, idx+1)
	case "command", "time":
		return nextOptionCommandIndex(tokens, idx+1)
	case "nohup":
		return nextCommandIndex(tokens, idx+1)
	case "nice":
		return nextNiceCommandIndex(tokens, idx+1)
	case "timeout":
		return nextTimeoutCommandIndex(tokens, idx+1)
	case "find":
		return nextFindExecCommandIndex(tokens, idx+1)
	case "xargs":
		return nextXargsCommandIndex(tokens, idx+1)
	default:
		return -1
	}
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

// nextEnvCommandIndex 处理nextenv命令索引。
func nextEnvCommandIndex(tokens []shellToken, start int) int {
	for i := start; i < len(tokens); i++ {
		text := strings.TrimSpace(tokens[i].text)
		switch {
		case text == "":
			continue
		case text == "-u" || text == "--unset" || text == "-S":
			i++
		case strings.HasPrefix(text, "--unset="), text == "-i", text == "--ignore-environment", isEnvAssignmentToken(text):
			continue
		case strings.HasPrefix(text, "-"):
			continue
		default:
			return i
		}
	}
	return -1
}

func nextOptionCommandIndex(tokens []shellToken, start int) int {
	for i := start; i < len(tokens); i++ {
		text := strings.TrimSpace(tokens[i].text)
		switch {
		case text == "":
			continue
		case strings.HasPrefix(text, "-"):
			continue
		default:
			return i
		}
	}
	return -1
}

// nextNiceCommandIndex 处理nextnice命令索引。
func nextNiceCommandIndex(tokens []shellToken, start int) int {
	for i := start; i < len(tokens); i++ {
		text := strings.TrimSpace(tokens[i].text)
		switch {
		case text == "":
			continue
		case text == "-n" || text == "--adjustment":
			i++
		case strings.HasPrefix(text, "--adjustment="):
			continue
		case looksLikeSignedInteger(text):
			continue
		case strings.HasPrefix(text, "-"):
			continue
		default:
			return i
		}
	}
	return -1
}

// nextTimeoutCommandIndex 处理next超时命令索引。
func nextTimeoutCommandIndex(tokens []shellToken, start int) int {
	for i := start; i < len(tokens); i++ {
		text := strings.TrimSpace(tokens[i].text)
		switch {
		case text == "":
			continue
		case text == "-k" || text == "--kill-after" || text == "-s" || text == "--signal":
			i++
		case strings.HasPrefix(text, "--kill-after="), strings.HasPrefix(text, "--signal="):
			continue
		case strings.HasPrefix(text, "-"):
			continue
		default:
			return nextCommandIndex(tokens, i+1)
		}
	}
	return -1
}

func nextFindExecCommandIndex(tokens []shellToken, start int) int {
	for i := start; i < len(tokens); i++ {
		text := strings.ToLower(strings.TrimSpace(tokens[i].text))
		if text == "-exec" || text == "-execdir" {
			return nextCommandIndex(tokens, i+1)
		}
	}
	return -1
}

func nextXargsCommandIndex(tokens []shellToken, start int) int {
	for i := start; i < len(tokens); i++ {
		text := strings.TrimSpace(tokens[i].text)
		switch {
		case text == "":
			continue
		case xargsOptionNeedsValue(text):
			i++
		case xargsInlineValueOption(text), strings.HasPrefix(text, "-"):
			continue
		default:
			return i
		}
	}
	return -1
}

func xargsOptionNeedsValue(text string) bool {
	switch text {
	case "-n", "-L", "-P", "-I", "-d", "--max-args", "--max-lines", "--max-procs", "--replace", "--delimiter":
		return true
	default:
		return false
	}
}

func xargsInlineValueOption(text string) bool {
	return strings.HasPrefix(text, "--max-args=") ||
		strings.HasPrefix(text, "--max-lines=") ||
		strings.HasPrefix(text, "--max-procs=") ||
		strings.HasPrefix(text, "--replace=") ||
		strings.HasPrefix(text, "--delimiter=")
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

// looksLikeSignedInteger 处理lookslikesignedinteger。
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

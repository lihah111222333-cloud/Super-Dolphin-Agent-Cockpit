package skill

import (
	"path/filepath"
	"strings"
)

type shellToken struct {
	text         string
	commandStart bool
}

type shellTokenizer struct {
	tokens            []shellToken
	current           strings.Builder
	tokenCommandStart bool
	nextCommandStart  bool
	singleQuoted      bool
	doubleQuoted      bool
	escaped           bool
}

type shellScanState struct {
	singleQuoted bool
	doubleQuoted bool
	escaped      bool
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

// shellCommandArg 处理shell命令arg。
func shellCommandArg(base string, args []string) (string, bool) {
	if !isShellInterpreter(normalizeExecToken(base)) {
		if len(args) == 0 {
			return "", false
		}
		return strings.Join(args, " "), true
	}
	for i, arg := range args {
		switch strings.ToLower(strings.TrimSpace(arg)) {
		case "-c", "-lc", "/c", "-command", "-encodedcommand":
			if i+1 >= len(args) {
				return "", false
			}
			return args[i+1], true
		}
	}
	if len(args) == 0 {
		return "", false
	}
	return strings.Join(args, " "), true
}

func tokenizeShellCommand(input string) []shellToken {
	tokenizer := newShellTokenizer()
	for i := 0; i < len(input); i++ {
		switch {
		case tokenizer.escaped:
			tokenizer.handleEscapeChar(input[i])
		case tokenizer.singleQuoted || tokenizer.doubleQuoted:
			i = tokenizer.handleQuotedToken(input, i)
		default:
			i = tokenizer.handleUnquotedToken(input, i)
		}
	}
	tokenizer.flush()
	return tokenizer.tokens
}

func newShellTokenizer() *shellTokenizer {
	return &shellTokenizer{
		tokens:            make([]shellToken, 0, 8),
		tokenCommandStart: true,
		nextCommandStart:  true,
	}
}

func (t *shellTokenizer) handleEscapeChar(ch byte) {
	t.writeByte(ch)
	t.escaped = false
}

func (t *shellTokenizer) handleQuotedToken(input string, idx int) int {
	if t.singleQuoted {
		if input[idx] == '\'' {
			t.singleQuoted = false
			return idx
		}
		t.writeByte(input[idx])
		return idx
	}
	return t.handleDoubleQuotedToken(input, idx)
}

// handleDoubleQuotedToken 处理doublequoted令牌。
func (t *shellTokenizer) handleDoubleQuotedToken(input string, idx int) int {
	switch input[idx] {
	case '"':
		t.doubleQuoted = false
	case '\\':
		return t.handleQuotedEscape(input, idx)
	case '$':
		if next, ok := t.handleCommandSubstitution(input, idx); ok {
			return next
		}
		t.writeByte(input[idx])
	case '`':
		if next, ok := t.handleBacktickCommand(input, idx); ok {
			return next
		}
		t.markCommandBoundary()
	default:
		t.writeByte(input[idx])
	}
	return idx
}

func (t *shellTokenizer) handleQuotedEscape(input string, idx int) int {
	if idx+1 >= len(input) {
		return idx
	}
	t.writeByte(input[idx+1])
	return idx + 1
}

// handleUnquotedToken 处理unquoted令牌。
func (t *shellTokenizer) handleUnquotedToken(input string, idx int) int {
	switch input[idx] {
	case '\\':
		t.escaped = true
	case '\'':
		t.singleQuoted = true
	case '"':
		t.doubleQuoted = true
	case '$':
		if next, ok := t.handleCommandSubstitution(input, idx); ok {
			return next
		}
		t.writeByte(input[idx])
	case '`':
		if next, ok := t.handleBacktickCommand(input, idx); ok {
			return next
		}
		t.markCommandBoundary()
	default:
		if !t.handleDelimiter(input[idx]) {
			t.writeByte(input[idx])
		}
	}
	return idx
}

func (t *shellTokenizer) handleDelimiter(ch byte) bool {
	switch ch {
	case ' ', '\t', '\r':
		t.flush()
		return true
	case '\n', ';', '|', '&', '(', ')':
		t.markCommandBoundary()
		return true
	default:
		return false
	}
}

func (t *shellTokenizer) handleCommandSubstitution(input string, idx int) (int, bool) {
	if idx+1 >= len(input) || input[idx+1] != '(' {
		return idx, false
	}
	end := findCommandSubstitutionEnd(input, idx+2)
	if end < 0 {
		t.markCommandBoundary()
		return idx + 1, true
	}
	t.appendCommandSubstitution(input[idx+2 : end])
	return end, true
}

func (t *shellTokenizer) handleBacktickCommand(input string, idx int) (int, bool) {
	end := findBacktickEnd(input, idx+1)
	if end < 0 {
		return idx, false
	}
	t.appendCommandSubstitution(input[idx+1 : end])
	return end, true
}

func (t *shellTokenizer) appendCommandSubstitution(command string) {
	t.markCommandBoundary()
	t.tokens = append(t.tokens, tokenizeShellCommand(command)...)
	t.nextCommandStart = false
}

// findCommandSubstitutionEnd 查找命令substitutionend。
func findCommandSubstitutionEnd(input string, start int) int {
	depth := 1
	state := shellScanState{}
	for i := start; i < len(input); i++ {
		if state.consumeEscaped() {
			continue
		}
		if state.consumeQuoted(input[i]) {
			continue
		}
		switch {
		case input[i] == '\\':
			state.escaped = true
		case input[i] == '\'':
			state.singleQuoted = true
		case input[i] == '"':
			state.doubleQuoted = true
		case isNestedCommandSubstitution(input, i):
			depth++
			i++
		case input[i] == ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func (s *shellScanState) consumeEscaped() bool {
	if !s.escaped {
		return false
	}
	s.escaped = false
	return true
}

func (s *shellScanState) consumeQuoted(ch byte) bool {
	if s.singleQuoted {
		s.singleQuoted = ch != '\''
		return true
	}
	if !s.doubleQuoted {
		return false
	}
	switch ch {
	case '\\':
		s.escaped = true
	case '"':
		s.doubleQuoted = false
	}
	return true
}

func isNestedCommandSubstitution(input string, idx int) bool {
	return input[idx] == '$' && idx+1 < len(input) && input[idx+1] == '('
}

func findBacktickEnd(input string, start int) int {
	escaped := false
	for i := start; i < len(input); i++ {
		switch {
		case escaped:
			escaped = false
		case input[i] == '\\':
			escaped = true
		case input[i] == '`':
			return i
		}
	}
	return -1
}

func (t *shellTokenizer) writeByte(ch byte) {
	t.startToken()
	t.current.WriteByte(ch)
}

func (t *shellTokenizer) flush() {
	if t.current.Len() == 0 {
		return
	}
	text := strings.TrimSpace(t.current.String())
	if text != "" {
		t.tokens = append(t.tokens, shellToken{text: text, commandStart: t.tokenCommandStart})
	}
	t.current.Reset()
}

func (t *shellTokenizer) startToken() {
	if t.current.Len() != 0 {
		return
	}
	t.tokenCommandStart = t.nextCommandStart
	t.nextCommandStart = false
}

func (t *shellTokenizer) markCommandBoundary() {
	t.flush()
	t.nextCommandStart = true
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

package turn

import (
	"encoding/json"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	toolResultPersistThresholdChars = 50_000
	toolResultAggregateBudgetChars  = 200_000

	// Budget unit is Unicode code points (runes), not bytes.
	toolResultRelevantMemoryHeadroomChars = 60_000
	toolResultNestedMemoryHeadroomChars   = 20_000
	toolResultPreviewBudgetChars          = toolResultAggregateBudgetChars - toolResultRelevantMemoryHeadroomChars - toolResultNestedMemoryHeadroomChars
)

type toolResultBudget struct {
	remaining int
}

type toolResultBudgetRegistry struct {
	mu      sync.Mutex
	budgets map[string]*toolResultBudget
}

var defaultToolResultBudgetRegistry = &toolResultBudgetRegistry{
	budgets: map[string]*toolResultBudget{},
}

// ResetToolResultScope 重置工具结果作用域。
func ResetToolResultScope(threadID, turnID string) {
	defaultToolResultBudgetRegistry.Reset(toolResultScope(threadID, turnID))
}

// Reset 重置turn。
func (r *toolResultBudgetRegistry) Reset(scope string) {
	scope = strings.TrimSpace(scope)
	if r == nil || scope == "" {
		return
	}
	r.mu.Lock()
	delete(r.budgets, scope)
	r.mu.Unlock()
}

func takeToolResultPreview(threadID, turnID, raw string) (string, bool) {
	return defaultToolResultBudgetRegistry.Take(toolResultScope(threadID, turnID), raw)
}

// Take 处理take。
func (r *toolResultBudgetRegistry) Take(scope, raw string) (string, bool) {
	chars := toolResultCharCount(raw)
	if chars == 0 {
		return "", false
	}
	allowed := toolResultPreviewBudgetChars
	scope = strings.TrimSpace(scope)
	if r != nil && scope != "" {
		r.mu.Lock()
		budget, ok := r.budgets[scope]
		if !ok {
			budget = &toolResultBudget{remaining: toolResultPreviewBudgetChars}
			r.budgets[scope] = budget
		}
		allowed = budget.remaining
		if allowed > chars {
			allowed = chars
		}
		budget.remaining -= allowed
		r.mu.Unlock()
	} else if allowed > chars {
		allowed = chars
	}
	preview := truncateToolResultChars(raw, allowed)
	return preview, toolResultCharCount(preview) < chars
}

func toolResultScope(threadID, turnID string) string {
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if threadID == "" || turnID == "" {
		return ""
	}
	return threadID + ":" + turnID
}

func toolResultCharCount(raw string) int {
	if strings.TrimSpace(raw) == "" {
		return 0
	}
	return utf8.RuneCountInString(raw)
}

func truncateToolResultChars(raw string, limit int) string {
	if limit <= 0 || raw == "" {
		return ""
	}
	runes := []rune(raw)
	if len(runes) <= limit {
		return raw
	}
	return string(runes[:limit])
}

func repairTruncatedJSON(original, truncated string) string {
	if !isRepairableJSON(original) {
		return truncated
	}
	runes := []rune(truncated)
	if len(runes) == 0 {
		return truncated
	}
	pos, stack, ok := scanCleanPosition(runes)
	if !ok {
		return truncated
	}
	repaired := closeJSONBrackets(runes[:pos], stack)
	if !json.Valid([]byte(repaired)) {
		return truncated
	}
	return repaired
}

func isRepairableJSON(s string) bool {
	if len(s) == 0 {
		return false
	}
	return (s[0] == '{' || s[0] == '[') && json.Valid([]byte(s))
}

type jsonRepairScanner struct {
	inString  bool
	awaitKey  bool
	stack     []rune
	bestPos   int
	bestStack []rune
}

func scanCleanPosition(runes []rune) (int, []rune, bool) {
	s := &jsonRepairScanner{bestPos: -1}
	for i := 0; i < len(runes); i++ {
		if s.inString {
			i = s.advanceString(runes, i)
			continue
		}
		s.processToken(runes[i], i)
	}
	return s.bestPos, s.bestStack, s.bestPos > 0
}

func (s *jsonRepairScanner) record(pos int) {
	s.bestPos = pos
	s.bestStack = append([]rune(nil), s.stack...)
}

// advanceString 处理advancestring。
func (s *jsonRepairScanner) advanceString(runes []rune, i int) int {
	for i < len(runes) {
		ch := runes[i]
		if ch == '\\' {
			i++
		} else if ch == '"' {
			s.inString = false
			if !s.awaitKey {
				s.record(i + 1)
			}
			return i
		}
		i++
	}
	return i
}

func (s *jsonRepairScanner) popMatchingBracket(opener rune) {
	if len(s.stack) > 0 && s.stack[len(s.stack)-1] == opener {
		s.stack = s.stack[:len(s.stack)-1]
	}
}

func (s *jsonRepairScanner) handleCloseBracket(ch rune, i int) {
	if ch == '}' {
		s.popMatchingBracket('{')
	} else {
		s.popMatchingBracket('[')
	}
	s.awaitKey = false
	s.record(i + 1)
}

// processToken 处理进程令牌。
func (s *jsonRepairScanner) processToken(ch rune, i int) {
	switch ch {
	case '"':
		s.inString = true
	case '{', '[':
		s.stack = append(s.stack, ch)
		s.awaitKey = ch == '{'
	case '}', ']':
		s.handleCloseBracket(ch, i)
	case ':':
		s.awaitKey = false
	case ',':
		s.record(i)
		if len(s.stack) > 0 && s.stack[len(s.stack)-1] == '{' {
			s.awaitKey = true
		}
	case ' ', '\t', '\n', '\r':
	default:
		if !s.awaitKey {
			s.record(i + 1)
		}
	}
}

// closeJSONBrackets 关闭JSONbrackets。
func closeJSONBrackets(prefix []rune, stack []rune) string {
	result := append([]rune(nil), prefix...)
	for len(result) > 0 {
		last := result[len(result)-1]
		if last == ',' || last == ' ' || last == '\t' || last == '\n' || last == '\r' {
			result = result[:len(result)-1]
		} else {
			break
		}
	}
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == '{' {
			result = append(result, '}')
		} else if stack[i] == '[' {
			result = append(result, ']')
		}
	}
	return string(result)
}

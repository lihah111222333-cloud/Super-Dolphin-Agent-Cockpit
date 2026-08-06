package turn

import (
	"encoding/json"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	// 工具结果预算按字符数控制，避免单个 turn 的大结果挤占上下文。
	toolResultPersistThresholdChars = 50_000
	toolResultAggregateBudgetChars  = 200_000

	// 预算单位是 Unicode 码点（rune），不能按字节截断。
	toolResultRelevantMemoryHeadroomChars = 60_000
	toolResultNestedMemoryHeadroomChars   = 20_000
	toolResultPreviewBudgetChars          = toolResultAggregateBudgetChars - toolResultRelevantMemoryHeadroomChars - toolResultNestedMemoryHeadroomChars
)

// toolResultBudget 保存单个 thread/turn 作用域剩余的工具结果预览额度。
type toolResultBudget struct {
	remaining int
}

// toolResultBudgetRegistry 按作用域串行扣减预览额度，防止并发工具结果重复占用预算。
type toolResultBudgetRegistry struct {
	mu      sync.Mutex
	budgets map[string]*toolResultBudget
}

// Reset 删除指定预算作用域；空 scope 表示调用方没有足够信息，直接跳过。
func (r *toolResultBudgetRegistry) Reset(scope string) {
	scope = strings.TrimSpace(scope)
	if r == nil || scope == "" {
		return
	}
	r.mu.Lock()
	delete(r.budgets, scope)
	r.mu.Unlock()
}

// Take 按 scope 递减预览预算；没有 scope 时仍使用单条结果上限，避免无限扩张。
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

// toolResultScope 生成预算注册表的稳定 key，缺少线程或 turn 时不建立共享预算。
func toolResultScope(threadID, turnID string) string {
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if threadID == "" || turnID == "" {
		return ""
	}
	return threadID + ":" + turnID
}

// toolResultCharCount 使用 rune 计数，和截断逻辑保持一致，避免多字节字符被切坏。
func toolResultCharCount(raw string) int {
	if strings.TrimSpace(raw) == "" {
		return 0
	}
	return utf8.RuneCountInString(raw)
}

// truncateToolResultChars 按 rune 上限截取工具结果预览。
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

// repairTruncatedJSON 尝试把被截断的完整 JSON 修成仍可解析的前缀，失败时保留原预览。
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

// isRepairableJSON 只允许对原始完整 JSON 做修复，避免把普通文本误补成 JSON。
func isRepairableJSON(s string) bool {
	if len(s) == 0 {
		return false
	}
	return (s[0] == '{' || s[0] == '[') && json.Valid([]byte(s))
}

// jsonRepairScanner 记录扫描 JSON 前缀时最后一个安全截断点和未闭合括号栈。
type jsonRepairScanner struct {
	inString  bool
	awaitKey  bool
	stack     []rune
	bestPos   int
	bestStack []rune
}

// scanCleanPosition 找到截断后仍能安全闭合 JSON 的最后位置。
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

// record 保存当前可回退的位置和括号栈快照。
func (s *jsonRepairScanner) record(pos int) {
	s.bestPos = pos
	s.bestStack = append([]rune(nil), s.stack...)
}

// advanceString 跳过 JSON 字符串内容，并在 value 字符串结束时记录安全点。
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

// popMatchingBracket 在闭合括号匹配时弹出栈顶，容忍不匹配前缀由最终校验兜住。
func (s *jsonRepairScanner) popMatchingBracket(opener rune) {
	if len(s.stack) > 0 && s.stack[len(s.stack)-1] == opener {
		s.stack = s.stack[:len(s.stack)-1]
	}
}

// handleCloseBracket 处理对象或数组闭合，并把闭合点标记为安全截断点。
func (s *jsonRepairScanner) handleCloseBracket(ch rune, i int) {
	if ch == '}' {
		s.popMatchingBracket('{')
	} else {
		s.popMatchingBracket('[')
	}
	s.awaitKey = false
	s.record(i + 1)
}

// processToken 更新 JSON 前缀扫描状态，只在 value 边界记录可修复位置。
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

// closeJSONBrackets 删除前缀尾部无效分隔符后，按扫描栈补齐剩余闭合括号。
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

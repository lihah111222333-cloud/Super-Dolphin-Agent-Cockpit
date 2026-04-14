package turn

import (
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

func ResetToolResultScope(threadID, turnID string) {
	defaultToolResultBudgetRegistry.Reset(toolResultScope(threadID, turnID))
}

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

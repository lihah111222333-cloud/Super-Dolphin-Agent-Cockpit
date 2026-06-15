package observation

import (
	"sync"
	"time"
)

// Memory is an in-memory Contract implementation suitable for tests and for
// the default production wiring. It is safe for concurrent use.
type Memory struct {
	mu          sync.RWMutex
	localToProv map[string]string
	provToLocal map[string]string
	callToTurn  map[string]string
	tokens      map[string]TokenSnapshot
	terminals   map[string]Terminal
	skills      map[string][]string
	seenDedupe  map[DedupeKey]struct{}
	counts      map[string]Counts
	timestamps  map[string]Timestamps
}

// NewMemory returns an empty Memory contract.
// NewMemory 创建记忆。
func NewMemory() *Memory {
	return &Memory{
		localToProv: map[string]string{},
		provToLocal: map[string]string{},
		callToTurn:  map[string]string{},
		tokens:      map[string]TokenSnapshot{},
		terminals:   map[string]Terminal{},
		skills:      map[string][]string{},
		seenDedupe:  map[DedupeKey]struct{}{},
		counts:      map[string]Counts{},
		timestamps:  map[string]Timestamps{},
	}
}

// MapTurn 映射turn。
func (m *Memory) MapTurn(local, provider string) bool {
	if local == "" || provider == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.localToProv[local]; ok && existing != provider {
		return false
	}
	if existing, ok := m.provToLocal[provider]; ok && existing != local {
		return false
	}
	m.localToProv[local] = provider
	m.provToLocal[provider] = local
	return true
}

// ResolveLocalTurn 解析localturn。
func (m *Memory) ResolveLocalTurn(provider string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.provToLocal[provider]
	return id, ok
}

// ResolveProviderTurn 解析providerturn。
func (m *Memory) ResolveProviderTurn(local string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.localToProv[local]
	return id, ok
}

// AttributeCall 处理attributecall。
func (m *Memory) AttributeCall(callID, localTurnID string) bool {
	if callID == "" || localTurnID == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callToTurn[callID] = localTurnID
	return true
}

// LookupCall 处理lookupcall。
func (m *Memory) LookupCall(callID string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.callToTurn[callID]
	return id, ok
}

// RecordTokens 记录令牌。
func (m *Memory) RecordTokens(turnID string, snap TokenSnapshot) TokenSnapshot {
	if turnID == "" {
		return snap
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	merged := mergeTokens(m.tokens[turnID], snap)
	m.tokens[turnID] = merged
	return merged
}

// Tokens 处理令牌。
func (m *Memory) Tokens(turnID string) (TokenSnapshot, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.tokens[turnID]
	return t, ok
}

// mergeTokens 合并令牌。
func mergeTokens(prev, next TokenSnapshot) TokenSnapshot {
	out := prev
	if next.Input != 0 {
		out.Input = next.Input
	}
	if next.Output != 0 {
		out.Output = next.Output
	}
	if next.Total != 0 {
		out.Total = next.Total
	}
	if next.ContextWindowTokens != 0 {
		out.ContextWindowTokens = next.ContextWindowTokens
	}
	if next.Projection != "" {
		out.Projection = next.Projection
	}
	if next.Observed {
		out.Observed = true
	}
	return out
}

// RecordTerminal 记录terminal。
func (m *Memory) RecordTerminal(turnID string, t Terminal) Terminal {
	if turnID == "" {
		return t
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	prev, ok := m.terminals[turnID]
	if !ok {
		m.terminals[turnID] = t
		return t
	}
	// Locked kinds stay forever.
	if prev.Kind == TerminalInterrupted || prev.Kind == TerminalAborted {
		return prev
	}
	if terminalPrecedence(t.Kind) >= terminalPrecedence(prev.Kind) {
		merged := t
		if merged.Reason == "" {
			merged.Reason = prev.Reason
		}
		if merged.Success == nil {
			merged.Success = prev.Success
		}
		m.terminals[turnID] = merged
		return merged
	}
	return prev
}

// Terminal 处理terminal。
func (m *Memory) Terminal(turnID string) (Terminal, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.terminals[turnID]
	return t, ok
}

// SetSkillsSelected 设置skillsselected。
func (m *Memory) SetSkillsSelected(turnID string, slugs []string) {
	if turnID == "" {
		return
	}
	cp := append([]string(nil), slugs...)
	m.mu.Lock()
	m.skills[turnID] = cp
	m.mu.Unlock()
}

// SkillsSelected 处理skillsselected。
func (m *Memory) SkillsSelected(turnID string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]string(nil), m.skills[turnID]...)
}

// Dedupe 去重turn。
func (m *Memory) Dedupe(key DedupeKey) bool {
	if key == (DedupeKey{}) {
		// No identifier — treat as unique. Observation must not silently
		// collapse events that arrive without any dedupe key.
		return true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, seen := m.seenDedupe[key]; seen {
		return false
	}
	m.seenDedupe[key] = struct{}{}
	return true
}

// IncrementToolCalls 累加工具calls。
func (m *Memory) IncrementToolCalls(turnID string) int32 {
	return m.bumpCounter(turnID, func(c *Counts) {
		c.ToolCalls++
		c.ToolCallsObserved = true
	}).ToolCalls
}

// IncrementToolFailures 累加工具failures。
func (m *Memory) IncrementToolFailures(turnID string) int32 {
	return m.bumpCounter(turnID, func(c *Counts) {
		c.ToolFailures++
		c.ToolFailuresObserved = true
	}).ToolFailures
}

// IncrementApprovalRequests 累加审批请求。
func (m *Memory) IncrementApprovalRequests(turnID string) int32 {
	return m.bumpCounter(turnID, func(c *Counts) {
		c.ApprovalRequests++
		c.ApprovalRequestsObserved = true
	}).ApprovalRequests
}

func (m *Memory) bumpCounter(turnID string, apply func(*Counts)) Counts {
	if turnID == "" {
		var zero Counts
		apply(&zero)
		return zero
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.counts[turnID]
	apply(&c)
	m.counts[turnID] = c
	return c
}

// Counts 处理counts。
func (m *Memory) Counts(turnID string) (Counts, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.counts[turnID]
	return c, ok
}

// RecordStartedAt 记录startedat。
func (m *Memory) RecordStartedAt(turnID string, at time.Time) {
	if turnID == "" || at.IsZero() {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ts := m.timestamps[turnID]
	if ts.StartedAt.IsZero() {
		ts.StartedAt = at
		m.timestamps[turnID] = ts
	}
}

// RecordCompletedAt 记录completedat。
func (m *Memory) RecordCompletedAt(turnID string, at time.Time) {
	if turnID == "" || at.IsZero() {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ts := m.timestamps[turnID]
	if at.After(ts.CompletedAt) {
		ts.CompletedAt = at
		m.timestamps[turnID] = ts
	}
}

// Timestamps 处理timestamps。
func (m *Memory) Timestamps(turnID string) (Timestamps, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ts, ok := m.timestamps[turnID]
	return ts, ok
}

var _ Contract = (*Memory)(nil)

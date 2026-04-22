package observation

import "sync"

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
}

// NewMemory returns an empty Memory contract.
func NewMemory() *Memory {
	return &Memory{
		localToProv: map[string]string{},
		provToLocal: map[string]string{},
		callToTurn:  map[string]string{},
		tokens:      map[string]TokenSnapshot{},
		terminals:   map[string]Terminal{},
		skills:      map[string][]string{},
		seenDedupe:  map[DedupeKey]struct{}{},
	}
}

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

func (m *Memory) ResolveLocalTurn(provider string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.provToLocal[provider]
	return id, ok
}

func (m *Memory) ResolveProviderTurn(local string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.localToProv[local]
	return id, ok
}

func (m *Memory) AttributeCall(callID, localTurnID string) bool {
	if callID == "" || localTurnID == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callToTurn[callID] = localTurnID
	return true
}

func (m *Memory) LookupCall(callID string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.callToTurn[callID]
	return id, ok
}

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

func (m *Memory) Tokens(turnID string) (TokenSnapshot, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.tokens[turnID]
	return t, ok
}

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
	if t.Kind.precedence() >= prev.Kind.precedence() {
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

func (m *Memory) Terminal(turnID string) (Terminal, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.terminals[turnID]
	return t, ok
}

func (m *Memory) SetSkillsSelected(turnID string, slugs []string) {
	if turnID == "" {
		return
	}
	cp := append([]string(nil), slugs...)
	m.mu.Lock()
	m.skills[turnID] = cp
	m.mu.Unlock()
}

func (m *Memory) SkillsSelected(turnID string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]string(nil), m.skills[turnID]...)
}

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

var _ Contract = (*Memory)(nil)

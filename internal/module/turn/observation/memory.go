package observation

import (
	"sync"
	"time"
)

// Memory 是并发安全的内存 observation 实现，供默认 wiring 和测试替身使用。
// 所有对外读取都会返回值或副本，避免调用方改写内部状态。
type Memory struct {
	mu          sync.RWMutex
	localToProv map[string]string        // 本地 turnID → provider turnID 映射
	provToLocal map[string]string        // provider turnID → 本地 turnID 反向映射
	callToTurn  map[string]string        // callID → 本地 turnID 归因表
	tokens      map[string]TokenSnapshot // 按 turnID 存储的 token 快照
	terminals   map[string]Terminal      // 按 turnID 存储的终止状态，粘性写入
	skills      map[string][]string      // 按 turnID 存储已选 skill slug 列表
	seenDedupe  map[DedupeKey]struct{}   // 已处理的去重键集合，防止事件重复计数
	counts      map[string]Counts        // 按 turnID 存储的工具调用/失败/审批计数
	timestamps  map[string]Timestamps    // 按 turnID 存储的开始/完成时间戳
}

// NewMemory 创建空的并发安全观察存储，用于默认 wiring 和测试替身。
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

// MapTurn 登记本地 turnID 与 provider turnID 的双向映射，冲突时返回 false 拒绝覆盖。
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

// ResolveLocalTurn 通过 provider turnID 反查本地 turnID。
func (m *Memory) ResolveLocalTurn(provider string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.provToLocal[provider]
	return id, ok
}

// ResolveProviderTurn 通过本地 turnID 查询 provider turnID。
func (m *Memory) ResolveProviderTurn(local string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.localToProv[local]
	return id, ok
}

// AttributeCall 把 provider callID 归因到本地 turn，后续工具结束事件可借此补 turnID。
func (m *Memory) AttributeCall(callID, localTurnID string) bool {
	if callID == "" || localTurnID == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callToTurn[callID] = localTurnID
	return true
}

// LookupCall 根据 provider callID 查询本地 turnID。
func (m *Memory) LookupCall(callID string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.callToTurn[callID]
	return id, ok
}

// RecordTokens 合并指定 turn 的 token 快照，只用非零字段覆盖已有值。
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

// Tokens 返回指定 turn 的最新 token 快照。
func (m *Memory) Tokens(turnID string) (TokenSnapshot, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.tokens[turnID]
	return t, ok
}

// mergeTokens 按增量快照语义合并 token 字段；未观测字段保留旧值。
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

// RecordTerminal 写入 turn 的粘性终态；Interrupted/Aborted 一旦出现就不再被覆盖。
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
	// 中断和放弃是粘性终态，不能被迟到的 completed/failed 覆盖。
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

// Terminal 返回指定 turn 的粘性终止状态。
func (m *Memory) Terminal(turnID string) (Terminal, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.terminals[turnID]
	return t, ok
}

// SetSkillsSelected 保存 turn 已选 skill slug，并复制切片避免调用方后续修改影响内存状态。
func (m *Memory) SetSkillsSelected(turnID string, slugs []string) {
	if turnID == "" {
		return
	}
	cp := append([]string(nil), slugs...)
	m.mu.Lock()
	m.skills[turnID] = cp
	m.mu.Unlock()
}

// SkillsSelected 返回 turn 已选 skill slug 的副本，避免调用方修改内部切片。
func (m *Memory) SkillsSelected(turnID string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]string(nil), m.skills[turnID]...)
}

// Dedupe 记录事件去重键并返回是否首次出现；空键按唯一事件处理，不折叠未知来源。
func (m *Memory) Dedupe(key DedupeKey) bool {
	if key == (DedupeKey{}) {
		// 缺少去重标识时按唯一事件处理，避免把无 key 事件静默折叠到一起。
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

// IncrementToolCalls 递增工具调用计数并标记该计数已被观测。
func (m *Memory) IncrementToolCalls(turnID string) int32 {
	return m.bumpCounter(turnID, func(c *Counts) {
		c.ToolCalls++
		c.ToolCallsObserved = true
	}).ToolCalls
}

// IncrementToolFailures 递增工具失败计数并标记该计数已被观测。
func (m *Memory) IncrementToolFailures(turnID string) int32 {
	return m.bumpCounter(turnID, func(c *Counts) {
		c.ToolFailures++
		c.ToolFailuresObserved = true
	}).ToolFailures
}

// IncrementApprovalRequests 递增审批请求计数并标记该计数已被观测。
func (m *Memory) IncrementApprovalRequests(turnID string) int32 {
	return m.bumpCounter(turnID, func(c *Counts) {
		c.ApprovalRequests++
		c.ApprovalRequestsObserved = true
	}).ApprovalRequests
}

// bumpCounter 在锁内更新指定 turn 的计数快照；空 turnID 只返回计算后的零值副本。
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

// Counts 返回工具调用、失败和审批请求的聚合计数。
func (m *Memory) Counts(turnID string) (Counts, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.counts[turnID]
	return c, ok
}

// RecordStartedAt 只记录第一次开始时间，避免迟到或重复事件改写原始启动点。
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

// RecordCompletedAt 保存最新完成时间，允许迟到事件把终止时间推进但不回退。
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

// Timestamps 返回 turn 的开始和完成时间戳快照。
func (m *Memory) Timestamps(turnID string) (Timestamps, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ts, ok := m.timestamps[turnID]
	return ts, ok
}

var _ Contract = (*Memory)(nil)

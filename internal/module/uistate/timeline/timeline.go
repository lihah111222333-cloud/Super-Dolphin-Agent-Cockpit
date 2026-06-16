package timeline

import (
	"strings"
	"sync"

	shared "github.com/anthropic-ai/super-agent-v3/internal/dto/eventcore"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
	pkglogger "github.com/anthropic-ai/super-agent-v3/internal/platform/logging"
)

// Item represents a single renderable entry in the thread timeline.
type Item struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Status      string `json:"status"`
	CallID      string `json:"callId,omitempty"`
	RequestID   int64  `json:"requestId,omitempty"`
	Command     string `json:"command,omitempty"`
	File        string `json:"file,omitempty"`
	Tool        string `json:"tool,omitempty"`
	Preview     string `json:"preview,omitempty"`
	ElapsedMS   *int   `json:"elapsedMs,omitempty"`
	Output      string `json:"output,omitempty"`
	ExitCode    *int   `json:"exitCode,omitempty"`
	Done        bool   `json:"done,omitempty"`
	Text        string `json:"text,omitempty"`
	Internal    bool   `json:"internal,omitempty"`
	Attachments []any  `json:"attachments,omitempty"`
	Error       string `json:"error,omitempty"`
	Success     *bool  `json:"success,omitempty"`
	AgentID     string `json:"agentId,omitempty"`
	TurnID      string `json:"turnId,omitempty"`
	ToolName    string `json:"toolName,omitempty"`
	ItemType    string `json:"itemType,omitempty"`
	Ts          string `json:"ts,omitempty"`
	lookupKey   string
}

// AppendedEmitter publishes newly appended timeline items to downstream consumers.
type AppendedEmitter func(uidto.UITimelineAppended)

func itemLookupKey(item Item) string {
	if key := strings.TrimSpace(item.lookupKey); key != "" {
		return key
	}
	if key := toolCallLookupKey(item); key != "" {
		return key
	}
	return strings.TrimSpace(item.CallID)
}

func toolCallLookupKey(item Item) string {
	if strings.TrimSpace(item.Kind) != "tool" {
		return ""
	}
	tool := kernel.FirstNonEmpty(strings.TrimSpace(item.Tool), strings.TrimSpace(item.ToolName))
	callID := strings.TrimSpace(item.CallID)
	if tool == "" || callID == "" {
		return ""
	}
	return timelineID("tool", tool, callID)
}

// Service manages per-thread UI timeline state in memory.
type Service interface {
	Append(threadID, agentID string, item Item)
	UpdateByCallID(threadID, agentID, callID string, fn func(*Item)) bool
	GetByThread(threadID string) []Item
	Snapshot() map[string][]Item
	SetEmitter(AppendedEmitter)
}

const defaultCapacity = 200

// New 创建uistate。
func New(logger *pkglogger.Logger, emitter AppendedEmitter, capacity int) Service {
	if capacity <= 0 {
		capacity = defaultCapacity
	}
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &service{
		timelines: make(map[string]*threadTimeline),
		logger:    logger,
		emitter:   emitter,
		capacity:  capacity,
	}
}

type service struct {
	mu        sync.RWMutex
	timelines map[string]*threadTimeline
	logger    *pkglogger.Logger
	emitter   AppendedEmitter
	capacity  int
}

// Append 追加uistate。
func (s *service) Append(threadID, agentID string, item Item) {
	s.mu.Lock()
	tl := s.timelineLocked(threadID)
	if idx, ok := tl.findDuplicate(item); ok {
		mergeItem(&tl.items[idx], item)
		if lookupKey := itemLookupKey(tl.items[idx]); lookupKey != "" {
			tl.index[lookupKey] = idx
		}
		if key := turnKindKey(tl.items[idx]); key != "" {
			tl.turnKind[key] = key
		}
		s.mu.Unlock()
		return
	}
	if item.Kind == "plan" {
		tl.insertPlan(item)
	} else {
		tl.append(item)
	}
	emitter := s.emitter
	s.mu.Unlock()

	s.emitAppended(emitter, threadID, item)
}

// UpdateByCallID 按callID更新uistate。
func (s *service) UpdateByCallID(threadID, agentID, callID string, fn func(*Item)) bool {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return false
	}

	s.mu.Lock()
	tl := s.timelineLocked(threadID)
	updated := tl.updateByCallID(callID, fn)
	s.mu.Unlock()
	return updated
}

// GetByThread 按线程读取uistate。
func (s *service) GetByThread(threadID string) []Item {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tl, ok := s.timelines[threadID]
	if !ok {
		return nil
	}
	return tl.snapshot()
}

// Snapshot 处理快照。
func (s *service) Snapshot() map[string][]Item {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.timelines) == 0 {
		return nil
	}

	out := make(map[string][]Item, len(s.timelines))
	for threadID, tl := range s.timelines {
		if tl.len() == 0 {
			continue
		}
		out[threadID] = tl.snapshot()
	}
	return out
}

// SetEmitter 设置emitter。
func (s *service) SetEmitter(emitter AppendedEmitter) {
	s.mu.Lock()
	s.emitter = emitter
	s.mu.Unlock()
}

func (s *service) timelineLocked(threadID string) *threadTimeline {
	tl, ok := s.timelines[threadID]
	if ok {
		return tl
	}

	tl = newThreadTimeline(s.capacity)
	s.timelines[threadID] = tl
	return tl
}

func (s *service) emitAppended(emitter AppendedEmitter, threadID string, item Item) {
	if emitter == nil {
		return
	}

	emitter(uidto.UITimelineAppended{
		UITurnHeader: shared.UITurnHeader{
			UIProjectionHeader: shared.UIProjectionHeader{
				ThreadHeader: shared.ThreadHeader{ThreadID: threadID},
				Projection:   "timeline",
			},
			TurnIDHeader: shared.TurnIDHeader{TurnID: item.TurnID},
		},
		ItemID:    item.ID,
		ItemKind:  item.Kind,
		RequestID: item.RequestID,
		CallID:    item.CallID,
		ToolName:  strings.TrimSpace(kernel.FirstNonEmpty(item.ToolName, item.Tool)),
	})
}

type threadTimeline struct {
	items    []Item
	cap      int
	index    map[string]int
	turnKind map[string]string
}

func newThreadTimeline(capacity int) *threadTimeline {
	return &threadTimeline{
		items:    make([]Item, 0, capacity),
		cap:      capacity,
		index:    make(map[string]int),
		turnKind: make(map[string]string),
	}
}

func (tl *threadTimeline) append(item Item) {
	if len(tl.items) >= tl.cap {
		tl.evictOldest()
	}

	tl.items = append(tl.items, item)
	idx := len(tl.items) - 1
	if lookupKey := itemLookupKey(item); lookupKey != "" {
		tl.index[lookupKey] = idx
	}
	if key := turnKindKey(item); key != "" {
		tl.turnKind[key] = key
	}
}

// insertPlan 插入plan。
func (tl *threadTimeline) insertPlan(item Item) {
	if len(tl.items) >= tl.cap {
		tl.evictOldest()
	}

	insertIdx := len(tl.items)
	for i := len(tl.items) - 1; i >= 0; i-- {
		if tl.items[i].TurnID == item.TurnID && tl.items[i].Kind == "turn_start" {
			insertIdx = i + 1
			break
		}
	}

	if insertIdx == len(tl.items) {
		tl.items = append(tl.items, item)
	} else {
		tl.items = append(tl.items, Item{})
		copy(tl.items[insertIdx+1:], tl.items[insertIdx:])
		tl.items[insertIdx] = item
		tl.rebuildIndex()
	}

	if lookupKey := itemLookupKey(item); lookupKey != "" {
		tl.index[lookupKey] = insertIdx
	}
	if key := turnKindKey(item); key != "" {
		tl.turnKind[key] = key
	}
}

func (tl *threadTimeline) updateByCallID(callID string, fn func(*Item)) bool {
	idx, ok := tl.findByCallID(callID)
	if !ok {
		return false
	}
	if fn == nil {
		return true
	}

	fn(&tl.items[idx])
	return true
}

func (tl *threadTimeline) findByCallID(callID string) (int, bool) {
	idx, ok := tl.index[callID]
	if !ok || idx < 0 || idx >= len(tl.items) {
		return 0, false
	}
	return idx, true
}

// findDuplicate 查找duplicate。
func (tl *threadTimeline) findDuplicate(item Item) (int, bool) {
	if lookupKey := itemLookupKey(item); lookupKey != "" {
		if idx, exists := tl.index[lookupKey]; exists && idx >= 0 && idx < len(tl.items) {
			return idx, true
		}
	}

	if key := turnKindKey(item); key != "" {
		for i := range tl.items {
			if turnKindKey(tl.items[i]) == key {
				return i, true
			}
		}
	}
	return 0, false
}

func (tl *threadTimeline) evictOldest() {
	if len(tl.items) == 0 {
		return
	}

	evicted := tl.items[0]
	tl.items = tl.items[1:]
	if lookupKey := itemLookupKey(evicted); lookupKey != "" {
		delete(tl.index, lookupKey)
	}
	if key := turnKindKey(evicted); key != "" {
		delete(tl.turnKind, key)
	}
	tl.rebuildIndex()
}

func (tl *threadTimeline) snapshot() []Item {
	out := make([]Item, len(tl.items))
	copy(out, tl.items)
	return out
}

func (tl *threadTimeline) rebuildIndex() {
	for key := range tl.index {
		delete(tl.index, key)
	}
	for i, item := range tl.items {
		if lookupKey := itemLookupKey(item); lookupKey != "" {
			tl.index[lookupKey] = i
		}
	}
}

func (tl *threadTimeline) len() int {
	return len(tl.items)
}

func turnKindKey(item Item) string {
	if !isTurnBoundaryKind(item.Kind) {
		return ""
	}
	turnID := strings.TrimSpace(item.TurnID)
	if turnID == "" {
		return ""
	}
	return turnID + ":" + item.Kind
}

func isTurnBoundaryKind(kind string) bool {
	return kind == "turn_start" || kind == "turn_end" || kind == "turn_interrupted"
}

package timeline

import (
	"log/slog"
	"strings"
	"sync"

	shared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	uidto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/ui"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// Item 是线程 timeline 下发给前端的单条可渲染记录。
// 字段同时兼容 tool、plan、turn 和错误行，私有 lookupKey 只服务后端去重索引。
type Item struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Status       string `json:"status"`
	SessionScope string `json:"sessionScope,omitempty"`
	CallID       string `json:"callId,omitempty"`
	RequestID    int64  `json:"requestId,omitempty"`
	Command      string `json:"command,omitempty"`
	File         string `json:"file,omitempty"`
	Tool         string `json:"tool,omitempty"`
	Preview      string `json:"preview,omitempty"`
	ElapsedMS    *int   `json:"elapsedMs,omitempty"`
	Output       string `json:"output,omitempty"`
	ExitCode     *int   `json:"exitCode,omitempty"`
	Done         bool   `json:"done,omitempty"`
	Text         string `json:"text,omitempty"`
	Internal     bool   `json:"internal,omitempty"`
	Attachments  []any  `json:"attachments,omitempty"`
	Error        string `json:"error,omitempty"`
	Success      *bool  `json:"success,omitempty"`
	AgentID      string `json:"agentId,omitempty"`
	TurnID       string `json:"turnId,omitempty"`
	ToolName     string `json:"toolName,omitempty"`
	ItemType     string `json:"itemType,omitempty"`
	Ts           string `json:"ts,omitempty"`
	lookupKey    string
}

// AppendedEmitter 是 timeline 追加事件发送到 UI patch 总线的回调。
type AppendedEmitter func(uidto.UITimelineAppended)

func itemLookupKey(item Item) string {
	if key := strings.TrimSpace(item.lookupKey); key != "" {
		return key
	}
	if strings.TrimSpace(item.Kind) == "approval" {
		return approvalUpdateKey(item.SessionScope, item.CallID, item.RequestID)
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
	tool := util.FirstNonEmpty(strings.TrimSpace(item.Tool), strings.TrimSpace(item.ToolName))
	callID := strings.TrimSpace(item.CallID)
	if tool == "" || callID == "" {
		return ""
	}
	return timelineID("tool", tool, callID)
}

// Service 管理 thread timeline 的追加、更新和快照读取。
type Service interface {
	Append(threadID, agentID string, item Item)
	UpdateByCallID(threadID, agentID, callID string, fn func(*Item)) bool
	GetByThread(threadID string) []Item
	Snapshot() map[string][]Item
	SetEmitter(AppendedEmitter)
}

const defaultCapacity = 200

// New 创建线程 timeline 服务，并设置单线程 item 容量上限。
func New(logger *slog.Logger, emitter AppendedEmitter, capacity int) Service {
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
	logger    *slog.Logger
	emitter   AppendedEmitter
	capacity  int
}

// Append 向线程 timeline 追加 item；重复 item 会合并而不是新增。
// emitter 在释放锁后调用，避免订阅者回调反向进入 timeline 时造成死锁。
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

// UpdateByCallID 用 callID 定位已有 item 并在锁内执行更新函数。
// callID 为空时直接返回 false，避免把无键事件误更新到其他行。
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

// GetByThread 返回指定线程 timeline 的快照副本。
func (s *service) GetByThread(threadID string) []Item {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tl, ok := s.timelines[threadID]
	if !ok {
		return nil
	}
	return tl.snapshot()
}

// Snapshot 返回全部非空线程 timeline 的快照副本。
// 返回 map 和切片均可由调用方修改，不会污染内部 timeline 状态。
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

// SetEmitter 更新追加事件 emitter，主要用于 fx 装配后补齐事件总线。
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
		ToolName:  strings.TrimSpace(util.FirstNonEmpty(item.ToolName, item.Tool)),
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

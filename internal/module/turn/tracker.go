package turn

import (
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/statemachine"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/qmuntal/stateless"
)

const trackerTTL = 30 * time.Minute

// ---------------------------------------------------------------------------
// turnTrackerStore — injectable storage interface
// ---------------------------------------------------------------------------

// turnTrackerStore abstracts mutable turn-entry storage behind an
// injectable interface so turnTracker itself holds zero local state.
//
// The default in-process implementation (inMemoryTurnTrackerStore)
// wraps sync.RWMutex + map.  A future SQL/Redis backend can persist
// turn metadata for cross-process crash recovery and horizontal
// scaling without changing the tracker's business logic.
type turnTrackerStore interface {
	Put(localID string, turn *trackedTurn)
	Mutate(localID string, fn func(*trackedTurn)) bool
	RangeMut(fn func(localID string, turn *trackedTurn))
	DeleteMatching(fn func(localID string, turn *trackedTurn) bool)
	View(localID string, fn func(*trackedTurn)) bool
	RangeView(fn func(localID string, turn *trackedTurn) bool)
	Tick() time.Time
}

// ---------------------------------------------------------------------------
// inMemoryTurnTrackerStore — default in-process implementation
// ---------------------------------------------------------------------------

type inMemoryTurnTrackerStore struct {
	mu     sync.RWMutex
	turns  map[string]*trackedTurn
	tickMu sync.Mutex
	last   time.Time
}

func newInMemoryTurnTrackerStore() *inMemoryTurnTrackerStore {
	return &inMemoryTurnTrackerStore{turns: make(map[string]*trackedTurn)}
}

// Put 写入turn。
func (s *inMemoryTurnTrackerStore) Put(localID string, turn *trackedTurn) {
	s.mu.Lock()
	s.turns[localID] = turn
	s.mu.Unlock()
}

// Mutate 处理mutate。
func (s *inMemoryTurnTrackerStore) Mutate(localID string, fn func(*trackedTurn)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	turn, ok := s.turns[localID]
	if !ok {
		return false
	}
	fn(turn)
	return true
}

// RangeMut 处理范围mut。
func (s *inMemoryTurnTrackerStore) RangeMut(fn func(localID string, turn *trackedTurn)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, turn := range s.turns {
		fn(id, turn)
	}
}

// DeleteMatching 删除matching。
func (s *inMemoryTurnTrackerStore) DeleteMatching(fn func(localID string, turn *trackedTurn) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, turn := range s.turns {
		if fn(id, turn) {
			delete(s.turns, id)
		}
	}
}

// View 处理view。
func (s *inMemoryTurnTrackerStore) View(localID string, fn func(*trackedTurn)) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	turn, ok := s.turns[localID]
	if !ok {
		return false
	}
	fn(turn)
	return true
}

// RangeView 处理范围view。
func (s *inMemoryTurnTrackerStore) RangeView(fn func(localID string, turn *trackedTurn) bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for id, turn := range s.turns {
		if !fn(id, turn) {
			return
		}
	}
}

// Tick 处理tick。
func (s *inMemoryTurnTrackerStore) Tick() time.Time {
	s.tickMu.Lock()
	defer s.tickMu.Unlock()
	now := time.Now()
	if !now.After(s.last) {
		now = s.last.Add(time.Nanosecond)
	}
	s.last = now
	return now
}

// ---------------------------------------------------------------------------
// turnTracker — stateless business-logic layer
// ---------------------------------------------------------------------------

type turnTracker struct {
	store  turnTrackerStore
	logger *pkglogger.Logger
}

type trackedTurn struct {
	localID, providerID, threadID string
	dedupeKey                     string
	state                         TurnState
	startedAt, updatedAt          time.Time
	lastError                     string
	interruptRequested            bool
	handle                        contract.TurnHandle
	sm                            *stateless.StateMachine
}

type activeTurn struct {
	localID    string
	providerID string
	handle     contract.TurnHandle
}

func newTurnTracker() *turnTracker {
	return &turnTracker{store: newInMemoryTurnTrackerStore(), logger: pkglogger.Get()}
}

// Start 启动turn流程。
func (t *turnTracker) Start(localID, providerID, threadID string) {
	localID = strings.TrimSpace(localID)
	if localID == "" {
		return
	}
	now := t.store.Tick()

	turn := &trackedTurn{
		localID:    localID,
		providerID: strings.TrimSpace(providerID),
		threadID:   strings.TrimSpace(threadID),
		state:      StatePreparing,
		startedAt:  now,
		updatedAt:  now,
	}

	turn.sm = statemachine.New(
		newTurnStateMachineConfig(),
		func() string { return string(turn.state) },
		func(next string) { turn.state = TurnState(next) },
	)

	t.store.Put(localID, turn)
}

// AttachHandle 处理attachhandle。
func (t *turnTracker) AttachHandle(localID string, handle contract.TurnHandle) {
	localID = strings.TrimSpace(localID)
	if localID == "" || handle == nil {
		return
	}
	t.store.Mutate(localID, func(turn *trackedTurn) {
		turn.handle = handle
		if turn.providerID == "" {
			turn.providerID = strings.TrimSpace(handle.ProviderID())
		}
		turn.updatedAt = t.store.Tick()
	})
}

// BindProviderID 绑定providerID。
func (t *turnTracker) BindProviderID(localID, providerID string) {
	localID = strings.TrimSpace(localID)
	if localID == "" || strings.TrimSpace(providerID) == "" {
		return
	}
	t.store.Mutate(localID, func(turn *trackedTurn) {
		turn.providerID = strings.TrimSpace(providerID)
		turn.updatedAt = t.store.Tick()
	})
}

var stateToTrigger = map[TurnState]TurnTrigger{
	StateRunning:         TriggerRun,
	StateForceCompleting: TriggerForce,
	StateInterrupting:    TriggerInterrupt,
	StateInterrupted:     TriggerAbort,
	StateCompleted:       TriggerComplete,
	StateFailed:          TriggerFail,
	StateStalled:         TriggerStall,
}

// Update 更新turn。
func (t *turnTracker) Update(localID string, state TurnState) {
	localID = strings.TrimSpace(localID)
	trigger := stateToTrigger[state]
	if localID == "" || trigger == "" {
		return
	}
	t.store.Mutate(localID, func(turn *trackedTurn) {
		if err := turn.sm.Fire(string(trigger)); err != nil {
			t.logger.Warn("turn: state machine fire failed", "trigger", trigger, "localID", localID, "error", err)
		}
		turn.updatedAt = t.store.Tick()
	})
}

// Complete 完成turn。
func (t *turnTracker) Complete(localID string, success bool, errMsg string) {
	localID = strings.TrimSpace(localID)
	if localID == "" {
		return
	}
	t.store.Mutate(localID, func(turn *trackedTurn) {
		turn.handle = nil
		turn.lastError = strings.TrimSpace(errMsg)

		// Already in a terminal state (e.g. interrupted): just update
		// metadata without attempting a state machine transition. This
		// handles the race where Codex sends turn/completed after the
		// local tracker already moved to "interrupted".
		if turn.isTerminal() {
			turn.updatedAt = t.store.Tick()
			return
		}

		trigger := TriggerFail
		if success {
			trigger = TriggerComplete
		} else if turn.interruptRequested || turn.state == StateInterrupted {
			trigger = TriggerFail
		}

		if err := turn.sm.Fire(string(trigger)); err != nil {
			t.logger.Warn("turn: state machine fire failed", "trigger", trigger, "localID", localID, "error", err)
		}
		turn.updatedAt = t.store.Tick()
	})
}

// MarkInterruptRequested 标记interruptrequested。
func (t *turnTracker) MarkInterruptRequested(localID string) bool {
	localID = strings.TrimSpace(localID)
	if localID == "" {
		return false
	}
	var interrupted bool
	if !t.store.Mutate(localID, func(turn *trackedTurn) {
		turn.interruptRequested = true
		if err := turn.sm.Fire(string(TriggerInterrupt)); err == nil {
			turn.updatedAt = t.store.Tick()
			interrupted = true
		}
	}) {
		return false
	}
	return interrupted
}

// Stall 处理stall。
func (t *turnTracker) Stall(localID string, errMsg string) {
	localID = strings.TrimSpace(localID)
	if localID == "" {
		return
	}
	t.store.Mutate(localID, func(turn *trackedTurn) {
		turn.handle = nil
		turn.lastError = strings.TrimSpace(errMsg)
		if err := turn.sm.Fire(string(TriggerStall)); err != nil {
			t.logger.Warn("turn: state machine fire failed", "trigger", TriggerStall, "localID", localID, "error", err)
		}
		turn.updatedAt = t.store.Tick()
	})
}

// Cleanup 处理cleanup。
func (t *turnTracker) Cleanup() {
	cutoff := time.Now().Add(-trackerTTL)
	t.store.DeleteMatching(func(_ string, turn *trackedTurn) bool {
		return turn.isTerminal() && turn.updatedAt.Before(cutoff)
	})
}

// ActiveByThread 按线程处理active。
func (t *turnTracker) ActiveByThread(threadID string) (activeTurn, bool) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return activeTurn{}, false
	}
	var result activeTurn
	var found bool
	var latestUpdate time.Time
	t.store.RangeView(func(_ string, turn *trackedTurn) bool {
		if turn.threadID != threadID || turn.isTerminal() {
			return true
		}
		if !found || turn.updatedAt.After(latestUpdate) {
			result = activeTurn{
				localID:    turn.localID,
				providerID: turn.providerID,
				handle:     turn.handle,
			}
			latestUpdate = turn.updatedAt
			found = true
		}
		return true
	})
	return result, found
}

// AbortThread 处理abort线程。
func (t *turnTracker) AbortThread(threadID, errMsg string) bool {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false
	}
	errMsg = strings.TrimSpace(errMsg)
	var updated bool
	t.store.RangeMut(func(_ string, turn *trackedTurn) {
		if turn.threadID != threadID || turn.isTerminal() {
			return
		}
		turn.handle = nil
		turn.interruptRequested = true
		turn.lastError = errMsg
		if err := turn.sm.Fire(string(TriggerAbort)); err == nil {
			turn.updatedAt = t.store.Tick()
			updated = true
		}
	})
	return updated
}

// Get 读取turn。
func (t *turnTracker) Get(localID string) (TurnStatus, bool) {
	localID = strings.TrimSpace(localID)
	if localID == "" {
		return TurnStatus{}, false
	}
	var status TurnStatus
	found := t.store.View(localID, func(turn *trackedTurn) {
		status = turn.status()
	})
	return status, found
}

// GetByProviderID 按providerID读取turn。
func (t *turnTracker) GetByProviderID(providerID string) (TurnStatus, bool) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return TurnStatus{}, false
	}
	var status TurnStatus
	var found bool
	t.store.RangeView(func(_ string, turn *trackedTurn) bool {
		if turn.providerID == providerID {
			status = turn.status()
			found = true
			return false
		}
		return true
	})
	return status, found
}

// RegisterDedupeKey 注册去重键。
func (t *turnTracker) RegisterDedupeKey(localID, dedupeKey string) {
	localID = strings.TrimSpace(localID)
	dedupeKey = strings.TrimSpace(dedupeKey)
	if localID == "" || dedupeKey == "" {
		return
	}
	t.store.Mutate(localID, func(turn *trackedTurn) {
		turn.dedupeKey = dedupeKey
		turn.updatedAt = t.store.Tick()
	})
}

// DedupeKeyOf 去重键。
func (t *turnTracker) DedupeKeyOf(localID string) string {
	localID = strings.TrimSpace(localID)
	if localID == "" {
		return ""
	}
	var key string
	t.store.View(localID, func(turn *trackedTurn) {
		key = turn.dedupeKey
	})
	return key
}

// GetByDedupeKey 按去重键读取turn。
func (t *turnTracker) GetByDedupeKey(dedupeKey string) (TurnStatus, bool) {
	dedupeKey = strings.TrimSpace(dedupeKey)
	if dedupeKey == "" {
		return TurnStatus{}, false
	}
	var result TurnStatus
	var found bool
	var latestUpdate time.Time
	t.store.RangeView(func(_ string, turn *trackedTurn) bool {
		if turn.dedupeKey != dedupeKey || turn.isTerminal() {
			return true
		}
		if !found || turn.updatedAt.After(latestUpdate) {
			result = turn.status()
			latestUpdate = turn.updatedAt
			found = true
		}
		return true
	})
	return result, found
}

func (t *trackedTurn) status() TurnStatus {
	return TurnStatus{
		LocalID:    t.localID,
		ProviderID: t.providerID,
		State:      string(t.state),
		Error:      t.lastError,
	}
}

func (t *trackedTurn) isTerminal() bool {
	switch t.state {
	case StateCompleted, StateInterrupted, StateFailed, StateStalled:
		return true
	}
	return false
}

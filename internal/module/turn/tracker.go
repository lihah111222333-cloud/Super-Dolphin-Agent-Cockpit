package turn

import (
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/statemachine"
	"github.com/qmuntal/stateless"
)

const trackerTTL = 30 * time.Minute

type turnTracker struct {
	mu       sync.RWMutex
	turns    map[string]*trackedTurn
	lastTick time.Time
}

func (t *turnTracker) tick() time.Time {
	now := time.Now()
	if !now.After(t.lastTick) {
		now = t.lastTick.Add(time.Nanosecond)
	}
	t.lastTick = now
	return now
}

type trackedTurn struct {
	localID, providerID, threadID string
	dedupeKey                     string
	state                         string
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

func newTurnTracker() *turnTracker { return &turnTracker{turns: make(map[string]*trackedTurn)} }

func (t *turnTracker) Start(localID, providerID, threadID string) {
	localID = strings.TrimSpace(localID)
	if localID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.tick()

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
		func() string { return turn.state },
		func(next string) { turn.state = next },
	)

	t.turns[localID] = turn
}

func (t *turnTracker) AttachHandle(localID string, handle contract.TurnHandle) {
	localID = strings.TrimSpace(localID)
	if localID == "" || handle == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	turn, ok := t.turns[localID]
	if !ok {
		return
	}
	turn.handle = handle
	if turn.providerID == "" {
		turn.providerID = strings.TrimSpace(handle.ProviderID())
	}
	turn.updatedAt = t.tick()
}

func (t *turnTracker) BindProviderID(localID, providerID string) {
	localID = strings.TrimSpace(localID)
	if localID == "" || strings.TrimSpace(providerID) == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if turn, ok := t.turns[localID]; ok {
		turn.providerID = strings.TrimSpace(providerID)
		turn.updatedAt = t.tick()
	}
}

func (t *turnTracker) Update(localID string, state string) {
	localID = strings.TrimSpace(localID)
	state = strings.TrimSpace(state)
	if localID == "" || state == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if turn, ok := t.turns[localID]; ok {
		// Map the raw state string to a trigger if possible.
		// Since callers pass the *dest state* as a string, we map it to triggers here.
		trigger := ""
		switch state {
		case StateRunning:
			trigger = TriggerRun
		case StateForceCompleting:
			trigger = TriggerForce
		case StateInterrupting:
			trigger = TriggerInterrupt
		case StateInterrupted:
			trigger = TriggerAbort
		case StateCompleted:
			trigger = TriggerComplete
		case StateFailed:
			trigger = TriggerFail
		case StateStalled:
			trigger = TriggerStall
		default:
			return
		}
		
		if trigger != "" {
			_ = turn.sm.Fire(trigger)
			turn.updatedAt = t.tick()
		}
	}
}

func (t *turnTracker) Complete(localID string, success bool, errMsg string) {
	localID = strings.TrimSpace(localID)
	if localID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	turn, ok := t.turns[localID]
	if !ok {
		return
	}
	turn.handle = nil
	turn.lastError = strings.TrimSpace(errMsg)
	
	trigger := TriggerFail
	if success {
		trigger = TriggerComplete
	} else if turn.interruptRequested || turn.state == StateInterrupted {
		trigger = TriggerFail // But StateInterrupting -> Fail is mapped to Interrupted!
	}
	
	_ = turn.sm.Fire(trigger)
	turn.updatedAt = t.tick()
}

func (t *turnTracker) MarkInterruptRequested(localID string) bool {
	localID = strings.TrimSpace(localID)
	if localID == "" {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	turn, ok := t.turns[localID]
	if !ok {
		return false
	}
	turn.interruptRequested = true
	if err := turn.sm.Fire(TriggerInterrupt); err == nil {
		turn.updatedAt = t.tick()
		return true
	}
	return false
}

func (t *turnTracker) Stall(localID string, errMsg string) {
	localID = strings.TrimSpace(localID)
	if localID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	turn, ok := t.turns[localID]
	if !ok {
		return
	}
	turn.handle = nil
	turn.lastError = strings.TrimSpace(errMsg)
	_ = turn.sm.Fire(TriggerStall)
	turn.updatedAt = t.tick()
}

func (t *turnTracker) Cleanup() {
	t.mu.Lock()
	defer t.mu.Unlock()

	cutoff := time.Now().Add(-trackerTTL)
	for id, turn := range t.turns {
		if turn.isTerminal() && turn.updatedAt.Before(cutoff) {
			delete(t.turns, id)
		}
	}
}

func (t *turnTracker) ActiveByThread(threadID string) (activeTurn, bool) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return activeTurn{}, false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	var current *trackedTurn
	for _, turn := range t.turns {
		if turn.threadID != threadID || turn.isTerminal() {
			continue
		}
		if current == nil || turn.updatedAt.After(current.updatedAt) {
			current = turn
		}
	}
	if current == nil {
		return activeTurn{}, false
	}
	return activeTurn{localID: current.localID, providerID: current.providerID, handle: current.handle}, true
}

func (t *turnTracker) AbortThread(threadID, errMsg string) bool {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return abortTrackedTurns(t.turns, threadID, errMsg, t.tick())
}

func (t *turnTracker) Get(localID string) (TurnStatus, bool) {
	localID = strings.TrimSpace(localID)
	if localID == "" {
		return TurnStatus{}, false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	turn, ok := t.turns[localID]
	if !ok {
		return TurnStatus{}, false
	}
	return turn.status(), true
}

func (t *turnTracker) GetByProviderID(providerID string) (TurnStatus, bool) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return TurnStatus{}, false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, turn := range t.turns {
		if turn.providerID == providerID {
			return turn.status(), true
		}
	}
	return TurnStatus{}, false
}

func (t *turnTracker) RegisterDedupeKey(localID, dedupeKey string) {
	localID = strings.TrimSpace(localID)
	dedupeKey = strings.TrimSpace(dedupeKey)
	if localID == "" || dedupeKey == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if turn, ok := t.turns[localID]; ok {
		turn.dedupeKey = dedupeKey
		turn.updatedAt = t.tick()
	}
}

func (t *turnTracker) DedupeKeyOf(localID string) string {
	localID = strings.TrimSpace(localID)
	if localID == "" {
		return ""
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	turn, ok := t.turns[localID]
	if !ok {
		return ""
	}
	return turn.dedupeKey
}

func (t *turnTracker) GetByDedupeKey(dedupeKey string) (TurnStatus, bool) {
	dedupeKey = strings.TrimSpace(dedupeKey)
	if dedupeKey == "" {
		return TurnStatus{}, false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	var current *trackedTurn
	for _, turn := range t.turns {
		if turn.dedupeKey != dedupeKey || turn.isTerminal() {
			continue
		}
		if current == nil || turn.updatedAt.After(current.updatedAt) {
			current = turn
		}
	}
	if current == nil {
		return TurnStatus{}, false
	}
	return current.status(), true
}

func (t *trackedTurn) status() TurnStatus {
	return TurnStatus{
		LocalID:    t.localID,
		ProviderID: t.providerID,
		State:      t.state,
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

func abortTrackedTurns(turns map[string]*trackedTurn, threadID, errMsg string, now time.Time) bool {
	updated := false
	for _, turn := range turns {
		if turn.threadID != threadID || turn.isTerminal() {
			continue
		}
		turn.handle = nil
		turn.interruptRequested = true
		turn.lastError = strings.TrimSpace(errMsg)
		if err := turn.sm.Fire(TriggerAbort); err == nil {
			turn.updatedAt = now
			updated = true
		}
	}
	return updated
}

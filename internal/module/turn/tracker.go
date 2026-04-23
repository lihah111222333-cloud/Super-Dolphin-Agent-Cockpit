package turn

import (
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

const trackerTTL = 30 * time.Minute

type turnTracker struct {
	mu    sync.RWMutex
	turns map[string]*trackedTurn
}

type trackedTurn struct {
	localID, providerID, threadID string
	dedupeKey                     string
	state                         string
	startedAt, updatedAt          time.Time
	lastError                     string
	interruptRequested            bool
	handle                        contract.TurnHandle
}

type activeTurn struct {
	localID string
	handle  contract.TurnHandle
}

func newTurnTracker() *turnTracker { return &turnTracker{turns: make(map[string]*trackedTurn)} }

func (t *turnTracker) Start(localID, providerID, threadID string) {
	localID = strings.TrimSpace(localID)
	if localID == "" {
		return
	}
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.turns[localID] = &trackedTurn{
		localID:    localID,
		providerID: strings.TrimSpace(providerID),
		threadID:   strings.TrimSpace(threadID),
		state:      "preparing",
		startedAt:  now,
		updatedAt:  now,
	}
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
	turn.updatedAt = time.Now()
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
		turn.updatedAt = time.Now()
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
		turn.state = state
		turn.updatedAt = time.Now()
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
	switch {
	case success:
		turn.state = "completed"
	case turn.interruptRequested || turn.state == "interrupted":
		turn.state = "interrupted"
	default:
		turn.state = "failed"
	}
	turn.updatedAt = time.Now()
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
	turn.state = "interrupting"
	turn.updatedAt = time.Now()
	return true
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
	turn.state = "stalled"
	turn.lastError = strings.TrimSpace(errMsg)
	turn.updatedAt = time.Now()
}

// Cleanup removes terminal turns that have aged past the tracker TTL.
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
	return activeTurn{localID: current.localID, handle: current.handle}, true
}

func (t *turnTracker) AbortThread(threadID, errMsg string) bool {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return abortTrackedTurns(t.turns, threadID, errMsg, time.Now())
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

// RegisterDedupeKey stamps a dedupeKey onto an already-tracked turn.
// Empty localID or dedupeKey is silently ignored so callers can always
// invoke this without guarding on "did cron set a key this time". The
// tracker does not enforce uniqueness here — the authoritative dedupe
// check happens in GetByDedupeKey; a duplicate registration simply
// overwrites the previous key (the last Start wins, which matches the
// provider-layer idempotency contract).
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
		turn.updatedAt = time.Now()
	}
}

// DedupeKeyOf returns the dedupeKey previously registered for a
// tracked localID, or "" when the turn never had one bound (or is
// already evicted). Used by the service to resolve the key at
// terminal-state callsites that only know the localID.
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

// GetByDedupeKey returns the most recently updated non-terminal turn
// that matches dedupeKey. A terminal turn (completed / failed / ...)
// is deliberately skipped so a fresh StartTurn after a prior
// terminal isn't mistaken for a still-in-flight submission — cron
// crash recovery only cares about "is there a live turn with this
// key right now".
//
// When nothing matches the caller gets ok=false and an empty status.
// Callers MUST treat ok=false as "never submitted" per the P1b plan.
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
	case "completed", "interrupted", "failed", "stalled":
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
		turn.state = "interrupted"
		turn.lastError = strings.TrimSpace(errMsg)
		turn.updatedAt = now
		updated = true
	}
	return updated
}

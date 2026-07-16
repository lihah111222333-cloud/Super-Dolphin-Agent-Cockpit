package turn

import (
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/statemachine"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
	"github.com/qmuntal/stateless"
)

// trackerTTL 同时控制 watcher 等待 provider 完成的最长时间，以及终态 turn 在内存中的保留窗口。
// 超过该窗口的终态记录会被 Cleanup 删除，非终态记录不会因为 TTL 被清掉。
const trackerTTL = 30 * time.Minute

// ----- tracker 存储接口 -----

// turnTrackerStore 隔离可变 turn 存储，让 turnTracker 只负责状态机推进和收敛规则。
// 默认实现使用进程内 RWMutex+map；未来替换为持久化存储时，必须保持同样的锁内快照和
// Tick 单调更新时间边界，避免并发查询看到乱序状态。
type turnTrackerStore interface {
	Put(localID string, turn *trackedTurn)
	Mutate(localID string, fn func(*trackedTurn)) bool
	MutateLatest(match func(*trackedTurn) bool, mutate func(*trackedTurn)) bool
	RangeMut(fn func(localID string, turn *trackedTurn))
	DeleteMatching(fn func(localID string, turn *trackedTurn) bool)
	View(localID string, fn func(*trackedTurn)) bool
	RangeView(fn func(localID string, turn *trackedTurn) bool)
	Tick() time.Time
}

// ----- 进程内 tracker 存储实现 -----

// inMemoryTurnTrackerStore 是默认进程内 store。
// turns 由 RWMutex 保护，last 由独立锁生成单调更新时间，避免同纳秒状态排序抖动。
type inMemoryTurnTrackerStore struct {
	mu     sync.RWMutex
	turns  map[string]*trackedTurn
	tickMu sync.Mutex
	last   time.Time
}

// newInMemoryTurnTrackerStore 创建空的进程内 tracker 存储；调用方仍需通过 turnTracker 封装访问。
func newInMemoryTurnTrackerStore() *inMemoryTurnTrackerStore {
	return &inMemoryTurnTrackerStore{turns: make(map[string]*trackedTurn)}
}

// Put 在写锁下保存新的 trackedTurn，调用方负责保证 localID 已校验。
func (s *inMemoryTurnTrackerStore) Put(localID string, turn *trackedTurn) {
	s.mu.Lock()
	s.turns[localID] = turn
	s.mu.Unlock()
}

// Mutate 在写锁下读取并修改指定 turn，未找到时返回 false。
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

// MutateLatest 在同一写锁内选择最新匹配 turn 并执行一次修改。
func (s *inMemoryTurnTrackerStore) MutateLatest(match func(*trackedTurn) bool, mutate func(*trackedTurn)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	var latest *trackedTurn
	for _, turn := range s.turns {
		if match(turn) && (latest == nil || turn.updatedAt.After(latest.updatedAt)) {
			latest = turn
		}
	}
	if latest == nil {
		return false
	}
	mutate(latest)
	return true
}

// RangeMut 在写锁下遍历所有 turn，适合需要批量终止或清理的操作。
func (s *inMemoryTurnTrackerStore) RangeMut(fn func(localID string, turn *trackedTurn)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, turn := range s.turns {
		fn(id, turn)
	}
}

// DeleteMatching 在写锁下删除满足条件的 turn，主要用于 TTL 清理。
func (s *inMemoryTurnTrackerStore) DeleteMatching(fn func(localID string, turn *trackedTurn) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, turn := range s.turns {
		if fn(id, turn) {
			delete(s.turns, id)
		}
	}
}

// View 在读锁下查看单个 turn，不允许回调修改内部状态。
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

// RangeView 在读锁下遍历 turn，回调返回 false 时提前停止。
func (s *inMemoryTurnTrackerStore) RangeView(fn func(localID string, turn *trackedTurn) bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for id, turn := range s.turns {
		if !fn(id, turn) {
			return
		}
	}
}

// Tick 返回单调递增的更新时间，避免同纳秒内多次状态变化排序不稳定。
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

// ----- tracker 状态机层 -----

// turnTracker 封装 turn 状态机业务逻辑，所有可变状态经 store 读写。
// watcher、interrupt 和 force-complete 可能并发推进同一 turn，非法转换只告警不回退。
type turnTracker struct {
	turnTrackerRoles

	store  turnTrackerStore
	logger *slog.Logger
}

type turnTrackerRoles struct {
	*turnTrackerQueryOps
	*turnTrackerDedupeOps
}

type turnTrackerQueryOps struct{ tracker *turnTracker }
type turnTrackerDedupeOps struct{ tracker *turnTracker }

// trackedTurn 是单个本地 turn 的可变状态，所有字段必须通过 store 锁访问。
type trackedTurn struct {
	localID, providerID, threadID string
	dedupeKey                     string
	state                         TurnState
	startedAt, updatedAt          time.Time
	lastError                     string
	interruptRequested            bool
	interruptClaimed              bool
	handle                        contract.TurnHandle
	sm                            *stateless.StateMachine
}

// activeTurn 是对外暴露的活跃 turn 最小信息，避免直接泄露 trackedTurn 指针。
type activeTurn struct {
	localID    string
	providerID string
	handle     contract.TurnHandle
}

type interruptClaim struct {
	target  activeTurn
	before  TurnStatus
	found   bool
	claimed bool
}

// newTurnTracker 创建默认进程内 tracker，终态记录按 trackerTTL 延迟清理。
func newTurnTracker() *turnTracker {
	tracker := &turnTracker{store: newInMemoryTurnTrackerStore(), logger: pkglogger.Get()}
	tracker.turnTrackerRoles = turnTrackerRoles{
		turnTrackerQueryOps:  &turnTrackerQueryOps{tracker: tracker},
		turnTrackerDedupeOps: &turnTrackerDedupeOps{tracker: tracker},
	}
	return tracker
}

// Start 注册新的本地 turn，并初始化状态机为 preparing。
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

// AttachHandle 把 provider handle 挂到本地 turn 上，并补齐 providerID。
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

// BindProviderID 在 provider 返回真实 turn ID 后更新 tracker 映射信息。
// 该方法只补充映射和更新时间，不推进生命周期状态。
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

// stateToTrigger 把目标状态映射为状态机 trigger，非法目标会被 Update 忽略。
var stateToTrigger = map[TurnState]TurnTrigger{
	StateRunning:         TriggerRun,
	StateForceCompleting: TriggerForce,
	StateInterrupting:    TriggerInterrupt,
	StateInterrupted:     TriggerAbort,
	StateCompleted:       TriggerComplete,
	StateFailed:          TriggerFail,
	StateStalled:         TriggerStall,
}

// Update 通过状态机把 turn 推进到目标状态，非法转换只记录告警。
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

// Complete 清除 handle 并把 turn 标记为成功或失败终态；已有终态只刷新元数据。
func (t *turnTracker) Complete(localID string, success bool, errMsg string) {
	localID = strings.TrimSpace(localID)
	if localID == "" {
		return
	}
	t.store.Mutate(localID, func(turn *trackedTurn) {
		turn.handle = nil
		turn.lastError = strings.TrimSpace(errMsg)

		// turn 已进入终态时只刷新错误和更新时间，不再触发状态机转换。
		// 这覆盖 provider 迟到完成事件与本地 interrupt 收敛之间的竞态，避免终态被回滚。
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

// MarkInterruptRequested 记录中断请求并尝试进入 interrupting 状态，返回是否转换成功。
func (t *turnTracker) MarkInterruptRequested(localID string) bool {
	localID = strings.TrimSpace(localID)
	if localID == "" {
		return false
	}
	var interrupted bool
	if !t.store.Mutate(localID, func(turn *trackedTurn) {
		if err := turn.sm.Fire(string(TriggerInterrupt)); err == nil {
			turn.interruptRequested = true
			turn.updatedAt = t.store.Tick()
			interrupted = true
		}
	}) {
		return false
	}
	return interrupted
}

// ClaimInterruptTarget 在 tracker 写锁内完成 active 选择、expectedTurnID 比较和 handle 捕获。
func (t *turnTracker) ClaimInterruptTarget(threadID, expectedTurnID string) interruptClaim {
	threadID = strings.TrimSpace(threadID)
	expectedTurnID = strings.TrimSpace(expectedTurnID)
	claim := interruptClaim{}
	if threadID == "" {
		return claim
	}
	claim.found = t.store.MutateLatest(func(turn *trackedTurn) bool {
		return turn.threadID == threadID && !turn.isTerminal()
	}, func(turn *trackedTurn) {
		claim.target = activeTurnFromTracked(turn)
		claim.before = turn.status()
		if expectedTurnID != "" && turn.localID != expectedTurnID {
			return
		}
		if turn.interruptClaimed {
			return
		}
		turn.interruptClaimed = true
		claim.claimed = true
	})
	return claim
}

// releaseInterruptClaim 释放尚未被 provider 接受的中断 claim。
func releaseInterruptClaim(t *turnTracker, localID string) {
	t.store.Mutate(strings.TrimSpace(localID), func(turn *trackedTurn) {
		turn.interruptClaimed = false
	})
}

// confirmInterruptClaim 在 provider 接受后把 claim 推进为 interrupting 状态。
func confirmInterruptClaim(t *turnTracker, localID string) bool {
	confirmed := false
	t.store.Mutate(strings.TrimSpace(localID), func(turn *trackedTurn) {
		if !turn.interruptClaimed {
			return
		}
		turn.interruptClaimed = false
		if turn.isTerminal() {
			return
		}
		if err := turn.sm.Fire(string(TriggerInterrupt)); err != nil {
			return
		}
		turn.interruptRequested = true
		turn.updatedAt = t.store.Tick()
		confirmed = true
	})
	return confirmed
}

// Stall 将 turn 标为 stalled 并保存错误原因，用于 watcher 超时和服务关闭路径。
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

// Cleanup 删除超过 trackerTTL 的终态 turn，只清理已收敛状态，避免活跃 turn 被 TTL 误删。
func (t *turnTracker) Cleanup() {
	cutoff := time.Now().Add(-trackerTTL)
	t.store.DeleteMatching(func(_ string, turn *trackedTurn) bool {
		return turn.isTerminal() && turn.updatedAt.Before(cutoff)
	})
}

// ActiveByThread 返回指定线程最近更新的非终态 turn。
func (r *turnTrackerQueryOps) ActiveByThread(threadID string) (activeTurn, bool) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return activeTurn{}, false
	}
	t := r.tracker
	var result activeTurn
	var found bool
	var latestUpdate time.Time
	t.store.RangeView(func(_ string, turn *trackedTurn) bool {
		if turn.threadID != threadID || turn.isTerminal() {
			return true
		}
		if !found || turn.updatedAt.After(latestUpdate) {
			result = activeTurnFromTracked(turn)
			latestUpdate = turn.updatedAt
			found = true
		}
		return true
	})
	return result, found
}

func activeTurnFromTracked(turn *trackedTurn) activeTurn {
	return activeTurn{localID: turn.localID, providerID: turn.providerID, handle: turn.handle}
}

// AbortThread 将线程下所有非终态 turn 标记为 interrupted/aborted 路径。
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

// Get 按本地 turnID 返回 tracker 状态快照；读取在 store 读锁内完成，返回值不暴露内部指针。
func (r *turnTrackerQueryOps) Get(localID string) (TurnStatus, bool) {
	localID = strings.TrimSpace(localID)
	if localID == "" {
		return TurnStatus{}, false
	}
	t := r.tracker
	var status TurnStatus
	found := t.store.View(localID, func(turn *trackedTurn) {
		status = turn.status()
	})
	return status, found
}

// GetByProviderID 按 provider turnID 查找本地状态快照。
func (r *turnTrackerQueryOps) GetByProviderID(providerID string) (TurnStatus, bool) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return TurnStatus{}, false
	}
	t := r.tracker
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

// RegisterDedupeKey 把调度去重键绑定到本地 turn，供并发查询和 watcher 终态回写使用。
func (r *turnTrackerDedupeOps) RegisterDedupeKey(localID, dedupeKey string) {
	localID = strings.TrimSpace(localID)
	dedupeKey = strings.TrimSpace(dedupeKey)
	if localID == "" || dedupeKey == "" {
		return
	}
	t := r.tracker
	t.store.Mutate(localID, func(turn *trackedTurn) {
		turn.dedupeKey = dedupeKey
		turn.updatedAt = t.store.Tick()
	})
}

// DedupeKeyOf 返回本地 turn 绑定的去重键，缺失时返回空字符串。
func (r *turnTrackerDedupeOps) DedupeKeyOf(localID string) string {
	localID = strings.TrimSpace(localID)
	if localID == "" {
		return ""
	}
	t := r.tracker
	var key string
	t.store.View(localID, func(turn *trackedTurn) {
		key = turn.dedupeKey
	})
	return key
}

// GetByDedupeKey 返回指定去重键下最近更新的非终态 turn。
func (r *turnTrackerDedupeOps) GetByDedupeKey(dedupeKey string) (TurnStatus, bool) {
	dedupeKey = strings.TrimSpace(dedupeKey)
	if dedupeKey == "" {
		return TurnStatus{}, false
	}
	t := r.tracker
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

// status 把内部 trackedTurn 转为对外只读状态快照。
func (t *trackedTurn) status() TurnStatus {
	return TurnStatus{
		LocalID:    t.localID,
		ProviderID: t.providerID,
		State:      string(t.state),
		Error:      t.lastError,
	}
}

// isTerminal 判断 tracker 状态是否已经不能再接受常规状态推进。
// 终态只允许刷新元数据，不允许被迟到 provider 事件回滚到 running/completed 之外的状态。
func (t *trackedTurn) isTerminal() bool {
	switch t.state {
	case StateCompleted, StateInterrupted, StateFailed, StateStalled:
		return true
	}
	return false
}

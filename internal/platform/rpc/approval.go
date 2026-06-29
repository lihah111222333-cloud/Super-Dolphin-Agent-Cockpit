package rpc

import (
	"context"
	"errors"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/kelindar/event"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// ApprovalManager 管理工具调用审批、用户输入请求以及客户端回调结果。
// pending 与 pendingByRequestID 必须在同一把锁下维护，避免 callID/requestID 查询视图分裂。
type ApprovalManager struct {
	mu                   sync.Mutex
	lifecycleMu          sync.Mutex
	pending              map[string]*pendingApproval
	pendingByRequestID   map[int64]map[string]*pendingApproval
	completed            map[string]completedApproval
	completedByRequestID map[int64]map[string]completedApproval
	nextRequestID        atomic.Int64
	logger               *pkglogger.Logger
	dispatcher           *event.Dispatcher
}

var _ contract.ApprovalResponder = (*ApprovalManager)(nil)

// pendingApproval 是单个审批请求的内存状态。
// done 和 once 保证等待方、恢复流程和回调 goroutine 只会共同完成一次。
type pendingApproval struct {
	key       string
	callID    string
	requestID *int64
	toolName  string
	result    chan contract.ApprovalDecision
	createdAt time.Time

	done        chan struct{}
	cancel      context.CancelFunc
	dispatcher  *event.Dispatcher
	dispatching bool
	request     ApprovalRequest
	decision    contract.ApprovalDecision
	err         error
	once        sync.Once
}

type completedApproval struct {
	callID    string
	requestID *int64
	decision  contract.ApprovalDecision
	err       error
}

// ApprovalRequest 是 RPC 层内部使用的审批请求 DTO。
// CallbackMethod 与 ApprovalPolicy 不透出 JSON，只用于本进程内的回调路由和策略判断。
type ApprovalRequest struct {
	CallID         string         `json:"callId,omitempty"`     // provider 侧工具调用 ID
	ApprovalID     string         `json:"approvalId,omitempty"` // 前端兼容的审批 ID
	ToolName       string         `json:"toolName,omitempty"`   // 触发审批的工具名
	AgentID        string         `json:"agentId,omitempty"`    // 发起审批的 agent
	ThreadID       string         `json:"threadId,omitempty"`   // 所属 thread
	TurnID         string         `json:"turnId,omitempty"`     // 所属 turn
	Reason         string         `json:"reason,omitempty"`     // 展示给用户的审批原因
	Kind           string         `json:"kind,omitempty"`       // tool 或 request_user_input
	State          string         `json:"state,omitempty"`      // provider 当前等待状态
	SourceMethod   string         `json:"sourceMethod,omitempty"`
	CallbackMethod string         `json:"-"`
	RequestID      *int64         `json:"requestId,omitempty"` // Codex 风格请求 ID
	ApprovalPolicy string         `json:"-"`
	Payload        map[string]any `json:"payload,omitempty"` // 原始 provider payload 的安全副本
}

// NewApprovalManager 创建审批管理器，并初始化 callID 与 requestID 双索引。
func NewApprovalManager(logger *pkglogger.Logger, dispatcher *event.Dispatcher) *ApprovalManager {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &ApprovalManager{
		pending:              make(map[string]*pendingApproval),
		pendingByRequestID:   make(map[int64]map[string]*pendingApproval),
		completed:            make(map[string]completedApproval),
		completedByRequestID: make(map[int64]map[string]completedApproval),
		logger:               logger,
		dispatcher:           dispatcher,
	}
}

// bridgeDispatcher 返回 push bridge 绑定的事件分发器，bridge 缺失时由 manager 默认值接管。
func bridgeDispatcher(bridge *PushBridge) *event.Dispatcher {
	if bridge == nil {
		return nil
	}
	return bridge.dispatcher
}

// RequestApproval 注册审批请求、派发给客户端并等待用户决策。
// 同一个 callID/requestID 的重复请求会复用 pending 状态，只有 owner 负责回调派发和超时失败。
func (m *ApprovalManager) RequestApproval(ctx context.Context, bridge *PushBridge, server *jrpc2.Server, req ApprovalRequest) (contract.ApprovalDecision, error) {
	ctx, cancel := WithApprovalDeadline(ctx)
	defer cancel()
	req, err := normalizeApprovalRequest(req)
	if err != nil {
		return contract.ApprovalDecision{}, err
	}
	pending, owner := m.registerPending(req, bridgeDispatcher(bridge))
	if owner {
		m.publishRequested(pending)
	}
	if owner {
		if _, err := m.ensureDispatch(bridge, server, pending); err != nil {
			m.failPending(pending, err)
			return contract.ApprovalDecision{}, err
		}
	}
	decision, err := waitForApproval(ctx, pending)
	if err == nil {
		return decision, nil
	}
	if owner {
		if decision, ok := canceledApprovalDecision(ctx, err); ok {
			m.finishPending(pending, decision, nil)
			return waitForApproval(context.Background(), pending)
		}
	}
	if !owner {
		return decision, err
	}
	err = mapApprovalWaitErr(err, pending.callID)
	m.failPending(pending, err)
	return contract.ApprovalDecision{}, err
}

// RequestUserInput 把用户输入请求映射到审批等待流程，并补齐默认 kind。
func (m *ApprovalManager) RequestUserInput(ctx context.Context, bridge *PushBridge, server *jrpc2.Server, req ApprovalRequest) (contract.ApprovalDecision, error) {
	if strings.TrimSpace(req.Kind) == "" {
		req.Kind = "request_user_input"
	}
	return m.RequestApproval(ctx, bridge, server, req)
}

// Respond 根据 callID/requestID 找到 pending 请求并写入用户决策。
func (m *ApprovalManager) Respond(callID string, requestID *int64, decision contract.ApprovalDecision) error {
	pending := m.lookupPending(callID, requestID)
	if pending == nil {
		if approvalCallID(callID, requestID) == "" {
			return ErrInvalidState("approval call id is required")
		}
		if completed, ok := m.lookupCompleted(callID, requestID); ok {
			if !sameApprovalDecision(completed.decision, decision) {
				return ErrInvalidState("approval already resolved with a different decision")
			}
			return completed.err
		}
		return ErrNotFound("approval is not pending")
	}
	m.finishPending(pending, decision, nil)
	return nil
}

// AutoApprove 对旧式只按 callID 定位的请求写入批准决策。
func (m *ApprovalManager) AutoApprove(callID string) error {
	return m.Respond(callID, nil, approvedDecision())
}

// registerPending 创建或复用 pending 请求，并返回当前调用方是否为 owner。
// requestID 缺失时生成本地递增 ID，保证同 callID 的并发请求仍可区分。
func (m *ApprovalManager) registerPending(req ApprovalRequest, dispatcher *event.Dispatcher) (*pendingApproval, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if dispatcher == nil {
		dispatcher = m.dispatcher
	}
	requestID := cloneInt64Ptr(req.RequestID)
	if requestID == nil || *requestID <= 0 {
		next := m.nextRequestID.Add(1)
		requestID = &next
	}
	key := pendingStorageKey(req.CallID, requestID)
	if pending := m.pending[key]; pending != nil {
		return pending, false
	}
	pending := &pendingApproval{
		key:        key,
		callID:     req.CallID,
		requestID:  requestID,
		toolName:   req.ToolName,
		result:     make(chan contract.ApprovalDecision, 1),
		createdAt:  time.Now(),
		done:       make(chan struct{}),
		dispatcher: dispatcher,
		request:    cloneApprovalRequest(req, requestID),
	}
	m.pending[key] = pending
	m.indexPendingLocked(pending)
	return pending, true
}

// ensureDispatch 确保审批回调已启动，或在不需要回调时直接完成请求。
// 回调 goroutine 的 panic 会被恢复为审批失败，避免挂住等待方。
func (m *ApprovalManager) ensureDispatch(bridge *PushBridge, server *jrpc2.Server, pending *pendingApproval) (bool, error) {
	if pending == nil {
		return false, ErrInvalidState("approval pending state is nil")
	}
	if decision, warnMsg, ok := dispatchApprovalDecision(pending.request, bridge, server); ok {
		if warnMsg != "" {
			m.logger.Warn(warnMsg,
				"call_id", pending.callID,
				"request_id", int64Value(pending.requestID),
				"kind", pending.request.Kind)
		}
		m.finishPending(pending, decision, nil)
		return false, nil
	}
	ctx, method, params, err := m.beginDispatch(bridge, server, pending)
	if err != nil {
		return false, err
	}
	if ctx == nil {
		return false, nil
	}
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				m.logger.Error("rpc: recovered approval dispatch panic",
					"call_id", pending.callID, "panic", rec)
				m.failPending(pending, errors.New("approval dispatch panicked"))
			}
		}()
		m.dispatchApproval(ctx, bridge, server, pending, method, params)
	}()
	return true, nil
}

// beginDispatch 在锁内检查 pending 状态并标记 dispatching。
// 返回 nil ctx 表示请求已完成或已有派发进行中，调用方不应重复启动 goroutine。
func (m *ApprovalManager) beginDispatch(bridge *PushBridge, server *jrpc2.Server, pending *pendingApproval) (context.Context, string, map[string]any, error) {
	if pending == nil {
		return nil, "", nil, ErrInvalidState("approval pending state is nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.pending[pending.key]
	if current != pending || pending.dispatching || isPendingDone(pending) {
		return nil, "", nil, nil
	}
	if bridge == nil {
		return nil, "", nil, ErrInvalidState("approval push bridge is nil")
	}
	if server == nil {
		return nil, "", nil, ErrInvalidState("approval rpc server is nil")
	}
	ctx, cancel := context.WithCancel(context.Background())
	pending.cancel = cancel
	pending.dispatching = true
	return ctx, callbackMethod(pending.request), callbackParams(pending), nil
}

// dispatchApproval 调用客户端回调并把返回 payload 解码成审批决策。
func (m *ApprovalManager) dispatchApproval(ctx context.Context, bridge *PushBridge, server *jrpc2.Server, pending *pendingApproval, paramsMethod string, params map[string]any) {
	raw, err := bridge.CallbackClient(ctx, server, paramsMethod, params)
	if err != nil {
		m.handleDispatchErr(pending, err)
		return
	}
	decision, err := decodeApprovalDecision(raw)
	if err != nil {
		m.failPending(pending, err)
		return
	}
	m.finishPending(pending, decision, nil)
}

// handleDispatchErr 区分可恢复连接中断和真实审批失败。
// 可恢复错误会保留 pending，等待后续 UI 连接或启动恢复流程重新派发。
func (m *ApprovalManager) handleDispatchErr(pending *pendingApproval, err error) {
	if pending == nil || errors.Is(err, context.Canceled) {
		m.resetDispatch(pending)
		return
	}
	if isRecoverableDispatchErr(err) {
		m.logger.Warn("approval callback interrupted; pending request kept for restore", "call_id", pending.callID, "error", err)
		m.resetDispatch(pending)
		return
	}
	m.failPending(pending, err)
}

// resetDispatch 撤销当前派发状态，让 pending 可被后续恢复流程重新派发。
func (m *ApprovalManager) resetDispatch(pending *pendingApproval) {
	if pending == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.pending[pending.key]
	if current != pending {
		return
	}
	if pending.cancel != nil {
		pending.cancel()
		pending.cancel = nil
	}
	pending.dispatching = false
}

// finishPending 原子完成审批请求，清理索引、取消派发 context 并通知等待方。
func (m *ApprovalManager) finishPending(pending *pendingApproval, decision contract.ApprovalDecision, err error) {
	if pending == nil {
		return
	}
	pending.once.Do(func() {
		m.mu.Lock()
		if current := m.pending[pending.key]; current == pending {
			delete(m.pending, pending.key)
			m.removePendingLocked(pending)
		}
		cancel := pending.cancel
		pending.cancel = nil
		pending.dispatching = false
		pending.decision = decision
		pending.err = err
		m.indexCompletedLocked(completedApproval{
			callID:    pending.callID,
			requestID: cloneInt64Ptr(pending.requestID),
			decision:  decision,
			err:       err,
		})
		m.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if err == nil {
			select {
			case pending.result <- decision:
			default:
			}
		}
		close(pending.done)
		m.publishResolved(pending, decision, err)
	})
}

// failPending 用错误决策完成 pending 请求。
func (m *ApprovalManager) failPending(pending *pendingApproval, err error) {
	m.finishPending(pending, errorDecision(err), err)
}

// lookupPending 按 callID/requestID 查找 pending 请求。
// requestID 优先；仅当同 requestID 唯一时允许缺少 callID 的兼容查询。
func (m *ApprovalManager) lookupPending(callID string, requestID *int64) *pendingApproval {
	callID = strings.TrimSpace(callID)
	if callID == "" && int64Value(requestID) <= 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if reqID := int64Value(requestID); reqID > 0 {
		if callID != "" {
			if pending := m.pending[pendingStorageKey(callID, requestID)]; pending != nil {
				return pending
			}
		}
		if pending := m.lookupPendingByRequestIDLocked(reqID, callID); pending != nil {
			return pending
		}
	}
	if callID == "" {
		return nil
	}
	return m.pending[pendingStorageKey(callID, nil)]
}

// lookupPendingByRequestIDLocked 在持锁状态下按 requestID 查找 pending 请求。
// requestID 对应多个 callID 时必须提供 callID，避免把决策写入错误请求。
func (m *ApprovalManager) lookupPendingByRequestIDLocked(requestID int64, callID string) *pendingApproval {
	entries := m.pendingByRequestID[requestID]
	if len(entries) == 0 {
		return nil
	}
	for _, pending := range entries {
		if callID != "" && pending.callID == callID {
			return pending
		}
	}
	if len(entries) != 1 {
		return nil
	}
	for _, pending := range entries {
		return pending
	}
	return nil
}

// lookupCompleted 返回已完成审批的决策，用于处理前端超时后的同一请求重试。
func (m *ApprovalManager) lookupCompleted(callID string, requestID *int64) (completedApproval, bool) {
	callID = strings.TrimSpace(callID)
	if callID == "" && int64Value(requestID) <= 0 {
		return completedApproval{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if reqID := int64Value(requestID); reqID > 0 {
		if callID != "" {
			if completed, ok := m.completed[pendingStorageKey(callID, requestID)]; ok {
				return completed, true
			}
		}
		if completed, ok := m.lookupCompletedByRequestIDLocked(reqID, callID); ok {
			return completed, true
		}
	}
	if callID == "" {
		return completedApproval{}, false
	}
	completed, ok := m.completed[pendingStorageKey(callID, nil)]
	return completed, ok
}

func (m *ApprovalManager) lookupCompletedByRequestIDLocked(requestID int64, callID string) (completedApproval, bool) {
	entries := m.completedByRequestID[requestID]
	if len(entries) == 0 {
		return completedApproval{}, false
	}
	for _, completed := range entries {
		if callID != "" && completed.callID == callID {
			return completed, true
		}
	}
	if len(entries) != 1 {
		return completedApproval{}, false
	}
	for _, completed := range entries {
		return completed, true
	}
	return completedApproval{}, false
}

// indexPendingLocked 在持锁状态下维护 requestID 到 pending 的反向索引。
func (m *ApprovalManager) indexPendingLocked(pending *pendingApproval) {
	requestID := int64Value(pending.requestID)
	if requestID <= 0 {
		return
	}
	entries := m.pendingByRequestID[requestID]
	if entries == nil {
		entries = make(map[string]*pendingApproval)
		m.pendingByRequestID[requestID] = entries
	}
	entries[pending.key] = pending
}

func (m *ApprovalManager) indexCompletedLocked(completed completedApproval) {
	key := pendingStorageKey(completed.callID, completed.requestID)
	if key == "" {
		return
	}
	m.completed[key] = completed
	requestID := int64Value(completed.requestID)
	if requestID <= 0 {
		return
	}
	entries := m.completedByRequestID[requestID]
	if entries == nil {
		entries = make(map[string]completedApproval)
		m.completedByRequestID[requestID] = entries
	}
	entries[key] = completed
}

// removePendingLocked 在持锁状态下从 requestID 反向索引移除 pending。
func (m *ApprovalManager) removePendingLocked(pending *pendingApproval) {
	requestID := int64Value(pending.requestID)
	if requestID <= 0 {
		return
	}
	entries := m.pendingByRequestID[requestID]
	if len(entries) == 0 {
		return
	}
	delete(entries, pending.key)
	if len(entries) == 0 {
		delete(m.pendingByRequestID, requestID)
	}
}

// snapshotPending 返回当前 pending 切片快照，避免调用方持锁执行慢操作。
func (m *ApprovalManager) snapshotPending() []*pendingApproval {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*pendingApproval, 0, len(m.pending))
	for _, pending := range m.pending {
		out = append(out, pending)
	}
	return out
}

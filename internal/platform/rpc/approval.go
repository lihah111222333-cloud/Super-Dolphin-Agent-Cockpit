package rpc

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/google/uuid"
	"github.com/kelindar/event"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// ApprovalManager 管理工具调用审批、用户输入请求以及客户端回调结果。
// pending/completed 只接受完整审批身份生成的精确键，不保留 requestID-only 反向索引。
type ApprovalManager struct {
	mu                   sync.Mutex
	lifecycleMu          sync.Mutex
	pending              map[string]*pendingApproval
	completed            map[string]completedApproval
	internalSessionScope string
	nextRequestID        atomic.Int64
	logger               *pkglogger.Logger
	dispatcher           *event.Dispatcher
}

var _ contract.ApprovalResponder = (*ApprovalManager)(nil)

// pendingApproval 是单个审批请求的内存状态。
// done 和 once 保证等待方、恢复流程和回调 goroutine 只会共同完成一次。
type pendingApproval struct {
	key       string
	identity  contract.ApprovalIdentity
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
	identity    contract.ApprovalIdentity
	decision    contract.ApprovalDecision
	err         error
	completedAt time.Time
}

// ApprovalRequest 是 RPC 层内部使用的审批请求 DTO。
// CallbackMethod 与 ApprovalPolicy 不透出 JSON，只用于本进程内的回调路由和策略判断。
type ApprovalRequest struct {
	SessionScope   string         `json:"sessionScope,omitempty"` // 后端签发的 provider session scope
	CallID         string         `json:"callId,omitempty"`       // provider 侧工具调用 ID
	ApprovalID     string         `json:"approvalId,omitempty"`   // 前端兼容的审批 ID
	ToolName       string         `json:"toolName,omitempty"`     // 触发审批的工具名
	AgentID        string         `json:"agentId,omitempty"`      // 发起审批的 agent
	ThreadID       string         `json:"threadId,omitempty"`     // 所属 thread
	TurnID         string         `json:"turnId,omitempty"`       // 所属 turn
	Reason         string         `json:"reason,omitempty"`       // 展示给用户的审批原因
	Kind           string         `json:"kind,omitempty"`         // tool 或 request_user_input
	State          string         `json:"state,omitempty"`        // provider 当前等待状态
	SourceMethod   string         `json:"sourceMethod,omitempty"`
	CallbackMethod string         `json:"-"`
	RequestID      *int64         `json:"requestId,omitempty"` // Codex 风格请求 ID
	ApprovalPolicy string         `json:"-"`
	Payload        map[string]any `json:"payload,omitempty"` // 原始 provider payload 的安全副本
}

// NewApprovalManager 创建审批管理器，并为内部 host-tool 审批签发独立后端 scope。
func NewApprovalManager(logger *pkglogger.Logger, dispatcher *event.Dispatcher) *ApprovalManager {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &ApprovalManager{
		pending:              make(map[string]*pendingApproval),
		completed:            make(map[string]completedApproval),
		internalSessionScope: uuid.NewString(),
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

// RequestApproval 注册具有完整后端身份的审批请求、派发给客户端并等待用户决策。
// 同一个 sessionScope/callID/requestID 的重复请求会复用 pending 状态，只有 owner 负责派发和超时失败。
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
	return m.waitForPendingApproval(ctx, pending, owner)
}

// RequestEventApproval 注册并发布事件驱动审批，然后等待 approval/respond 按完整身份回写。
// 该入口不启动 RPC callback，也不因 callback bridge/server 为空而自动拒绝。
func (m *ApprovalManager) RequestEventApproval(ctx context.Context, req ApprovalRequest) (contract.ApprovalDecision, error) {
	ctx, cancel := WithApprovalDeadline(ctx)
	defer cancel()
	req, err := normalizeApprovalRequest(req)
	if err != nil {
		return contract.ApprovalDecision{}, err
	}
	pending, owner := m.registerPending(req, nil)
	if owner {
		m.publishRequested(pending)
	}
	return m.waitForPendingApproval(ctx, pending, owner)
}

// waitForPendingApproval 等待已登记审批完成，并统一处理取消、超时和 pending 清理。
func (m *ApprovalManager) waitForPendingApproval(
	ctx context.Context,
	pending *pendingApproval,
	owner bool,
) (contract.ApprovalDecision, error) {
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

// RequestInternalApproval 为可信的进程内 host-tool 请求签发身份后进入统一审批流程。
// 该入口不会读取调用方提供的 scope/requestID，避免把 provider 身份补全变成隐式兼容。
func (m *ApprovalManager) RequestInternalApproval(ctx context.Context, bridge *PushBridge, server *jrpc2.Server, req ApprovalRequest) (contract.ApprovalDecision, error) {
	if m == nil {
		return contract.ApprovalDecision{}, ErrInvalidState("approval manager is nil")
	}
	callID := strings.TrimSpace(req.CallID)
	if callID == "" {
		return contract.ApprovalDecision{}, ErrInvalidState("approval call id is required")
	}
	requestID := m.nextRequestID.Add(1)
	req.SessionScope = m.internalSessionScope
	req.CallID = callID
	req.RequestID = &requestID
	return m.RequestApproval(ctx, bridge, server, req)
}

// RequestUserInput 把用户输入请求映射到审批等待流程，并补齐默认 kind。
func (m *ApprovalManager) RequestUserInput(ctx context.Context, bridge *PushBridge, server *jrpc2.Server, req ApprovalRequest) (contract.ApprovalDecision, error) {
	if strings.TrimSpace(req.Kind) == "" {
		req.Kind = "request_user_input"
	}
	return m.RequestApproval(ctx, bridge, server, req)
}

// Respond 根据完整审批身份找到 pending 请求并写入用户决策。
func (m *ApprovalManager) Respond(identity contract.ApprovalIdentity, decision contract.ApprovalDecision) error {
	identity, err := normalizeApprovalIdentity(identity)
	if err != nil {
		return err
	}
	pending := m.lookupPending(identity)
	if pending == nil {
		if completed, ok := m.lookupCompleted(identity); ok {
			return approvalCompletionResult(completed, decision)
		}
		return ErrNotFound("approval is not pending")
	}
	return m.respondPending(pending, decision)
}

// respondPending 完成调用方已精确定位的 pending，并按实际赢家返回幂等、冲突或失败结果。
func (m *ApprovalManager) respondPending(pending *pendingApproval, decision contract.ApprovalDecision) error {
	completed := m.finishPending(pending, decision, nil)
	return approvalCompletionResult(completed, decision)
}

func approvalCompletionResult(completed completedApproval, decision contract.ApprovalDecision) error {
	if completed.err != nil {
		return completed.err
	}
	if !sameApprovalDecision(completed.decision, decision) {
		return ErrInvalidState("approval already resolved with a different decision")
	}
	return nil
}

// AutoApprove 对完整身份定位的请求写入批准决策。
func (m *ApprovalManager) AutoApprove(identity contract.ApprovalIdentity) error {
	return m.Respond(identity, approvedDecision())
}

// registerPending 创建或复用 pending 请求，并返回当前调用方是否为 owner。
// 调用方必须先完成身份标准化；缺失任一身份分量时拒绝注册。
func (m *ApprovalManager) registerPending(req ApprovalRequest, dispatcher *event.Dispatcher) (*pendingApproval, bool) {
	identity, err := approvalIdentityFromRequest(req)
	if err != nil {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if dispatcher == nil {
		dispatcher = m.dispatcher
	}
	requestID := identity.RequestID
	requestIDRef := &requestID
	key := pendingStorageKey(identity)
	if pending := m.pending[key]; pending != nil {
		return pending, false
	}
	pending := &pendingApproval{
		key:        key,
		identity:   identity,
		callID:     identity.CallID,
		requestID:  requestIDRef,
		toolName:   req.ToolName,
		result:     make(chan contract.ApprovalDecision, 1),
		createdAt:  time.Now(),
		done:       make(chan struct{}),
		dispatcher: dispatcher,
		request:    cloneApprovalRequest(req, requestIDRef),
	}
	m.pending[key] = pending
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
	var dispatchWG sync.WaitGroup
	dispatchWG.Go(func() {
		defer func() {
			if rec := recover(); rec != nil {
				m.logger.Error("rpc: recovered approval dispatch panic",
					"call_id", pending.callID, "panic", rec)
				m.failPending(pending, errors.New("approval dispatch panicked"))
			}
		}()
		m.dispatchApproval(ctx, bridge, server, pending, method, params)
	})
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
func (m *ApprovalManager) finishPending(pending *pendingApproval, decision contract.ApprovalDecision, err error) completedApproval {
	if pending == nil {
		return completedApproval{decision: decision, err: err}
	}
	pending.once.Do(func() {
		m.mu.Lock()
		if current := m.pending[pending.key]; current == pending {
			delete(m.pending, pending.key)
		}
		cancel := pending.cancel
		pending.cancel = nil
		pending.dispatching = false
		pending.decision = decision
		pending.err = err
		m.indexCompletedLocked(completedApproval{
			identity:    pending.identity,
			decision:    decision,
			err:         err,
			completedAt: time.Now(),
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
	return completedApproval{
		identity: pending.identity,
		decision: pending.decision,
		err:      pending.err,
	}
}

// failPending 用错误决策完成 pending 请求。
func (m *ApprovalManager) failPending(pending *pendingApproval, err error) {
	m.finishPending(pending, errorDecision(err), err)
}

// lookupPending 仅按完整身份精确查找 pending 请求。
func (m *ApprovalManager) lookupPending(identity contract.ApprovalIdentity) *pendingApproval {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pending[pendingStorageKey(identity)]
}

// lookupCompleted 返回已完成审批的决策，用于处理前端超时后的同一请求重试。
func (m *ApprovalManager) lookupCompleted(identity contract.ApprovalIdentity) (completedApproval, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	completed, ok := m.completed[pendingStorageKey(identity)]
	return completed, ok
}

func (m *ApprovalManager) indexCompletedLocked(completed completedApproval) {
	key := pendingStorageKey(completed.identity)
	if key == "" {
		return
	}
	m.completed[key] = completed
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

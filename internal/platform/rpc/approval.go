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

// ApprovalManager manages tool-call approvals and their client callbacks.
type ApprovalManager struct {
	mu                 sync.Mutex
	lifecycleMu        sync.Mutex
	pending            map[string]*pendingApproval
	pendingByRequestID map[int64]map[string]*pendingApproval
	nextRequestID      atomic.Int64
	logger             *pkglogger.Logger
	dispatcher         *event.Dispatcher
}

var _ contract.ApprovalResponder = (*ApprovalManager)(nil)

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

type ApprovalRequest struct {
	CallID         string         `json:"callId,omitempty"`
	ApprovalID     string         `json:"approvalId,omitempty"`
	ToolName       string         `json:"toolName,omitempty"`
	AgentID        string         `json:"agentId,omitempty"`
	ThreadID       string         `json:"threadId,omitempty"`
	TurnID         string         `json:"turnId,omitempty"`
	Reason         string         `json:"reason,omitempty"`
	Kind           string         `json:"kind,omitempty"`
	State          string         `json:"state,omitempty"`
	SourceMethod   string         `json:"sourceMethod,omitempty"`
	CallbackMethod string         `json:"-"`
	RequestID      *int64         `json:"requestId,omitempty"`
	ApprovalPolicy string         `json:"-"`
	Payload        map[string]any `json:"payload,omitempty"`
}

// NewApprovalManager 创建审批manager。
func NewApprovalManager(logger *pkglogger.Logger, dispatcher *event.Dispatcher) *ApprovalManager {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &ApprovalManager{
		pending:            make(map[string]*pendingApproval),
		pendingByRequestID: make(map[int64]map[string]*pendingApproval),
		logger:             logger,
		dispatcher:         dispatcher,
	}
}

func bridgeDispatcher(bridge *PushBridge) *event.Dispatcher {
	if bridge == nil {
		return nil
	}
	return bridge.dispatcher
}

// RequestApproval 处理请求审批。
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

// RequestUserInput 处理请求userinput。
func (m *ApprovalManager) RequestUserInput(ctx context.Context, bridge *PushBridge, server *jrpc2.Server, req ApprovalRequest) (contract.ApprovalDecision, error) {
	if strings.TrimSpace(req.Kind) == "" {
		req.Kind = "request_user_input"
	}
	return m.RequestApproval(ctx, bridge, server, req)
}

// Respond 写入审批响应。
func (m *ApprovalManager) Respond(callID string, requestID *int64, decision contract.ApprovalDecision) error {
	pending := m.lookupPending(callID, requestID)
	if pending == nil {
		if approvalCallID(callID, requestID) == "" {
			return ErrInvalidState("approval call id is required")
		}
		return ErrNotFound("approval is not pending")
	}
	m.finishPending(pending, decision, nil)
	return nil
}

// AutoApprove 按规则尝试自动批准请求。
func (m *ApprovalManager) AutoApprove(callID string) error {
	return m.Respond(callID, nil, approvedDecision())
}

// registerPending 注册待处理。
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

// ensureDispatch 确保dispatch。
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

// beginDispatch 处理begindispatch。
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

// finishPending 处理finish待处理。
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

func (m *ApprovalManager) failPending(pending *pendingApproval, err error) {
	m.finishPending(pending, errorDecision(err), err)
}

// lookupPending 处理lookup待处理。
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

// lookupPendingByRequestIDLocked 按请求IDlocked处理lookup待处理。
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

func (m *ApprovalManager) snapshotPending() []*pendingApproval {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*pendingApproval, 0, len(m.pending))
	for _, pending := range m.pending {
		out = append(out, pending)
	}
	return out
}

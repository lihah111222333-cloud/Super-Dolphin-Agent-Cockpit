package rpc

import (
	"context"
	"errors"
	"log/slog"
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
	mu            sync.Mutex
	pending       map[string]*pendingApproval
	nextRequestID atomic.Int64
	logger        *slog.Logger
}

var _ contract.ApprovalResponder = (*ApprovalManager)(nil)

type pendingApproval struct {
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
	Payload        map[string]any `json:"payload,omitempty"`
}

func NewApprovalManager(logger *slog.Logger) *ApprovalManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &ApprovalManager{
		pending: make(map[string]*pendingApproval),
		logger:  logger,
	}
}

func (m *ApprovalManager) RequestApproval(ctx context.Context, bridge *PushBridge, server *jrpc2.Server, req ApprovalRequest) (contract.ApprovalDecision, error) {
	req, err := normalizeApprovalRequest(req)
	if err != nil {
		return contract.ApprovalDecision{}, err
	}
	pending, owner := m.registerPending(req)
	if owner && bridge != nil {
		pending.dispatcher = bridge.dispatcher
	}
	if owner {
		m.publishRequested(bridge, pending)
	}
	if err := m.ensureDispatch(bridge, server, pending); err != nil {
		if owner {
			m.failPending(pending.callID, pending, err)
		}
		return contract.ApprovalDecision{}, err
	}
	decision, err := waitForApproval(ctx, pending)
	if err == nil || !owner {
		return decision, err
	}
	err = mapApprovalWaitErr(err, pending.callID)
	m.failPending(pending.callID, pending, err)
	return contract.ApprovalDecision{}, err
}

func (m *ApprovalManager) RequestUserInput(ctx context.Context, bridge *PushBridge, server *jrpc2.Server, req ApprovalRequest) (contract.ApprovalDecision, error) {
	if strings.TrimSpace(req.Kind) == "" {
		req.Kind = "request_user_input"
	}
	return m.RequestApproval(ctx, bridge, server, req)
}

func (m *ApprovalManager) Respond(callID string, requestID *int64, decision contract.ApprovalDecision) error {
	callID = approvalCallID(callID, requestID)
	if callID == "" {
		return ErrInvalidState("approval call id is required")
	}
	pending := m.lookupPending(callID)
	if pending == nil {
		return ErrNotFound("approval is not pending")
	}
	m.finishPending(callID, pending, decision, nil)
	return nil
}

func (m *ApprovalManager) AutoApprove(callID string) error {
	return m.Respond(callID, nil, contract.ApprovalDecision{
		Approved: true,
		Reason:   "auto_approved",
	})
}

func (m *ApprovalManager) registerPending(req ApprovalRequest) (*pendingApproval, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if pending := m.pending[req.CallID]; pending != nil {
		return pending, false
	}
	requestID := cloneInt64Ptr(req.RequestID)
	if requestID == nil || *requestID <= 0 {
		next := m.nextRequestID.Add(1)
		requestID = &next
	}
	pending := &pendingApproval{
		callID:    req.CallID,
		requestID: requestID,
		toolName:  req.ToolName,
		result:    make(chan contract.ApprovalDecision, 1),
		createdAt: time.Now(),
		done:      make(chan struct{}),
		request:   cloneApprovalRequest(req, requestID),
	}
	m.pending[req.CallID] = pending
	return pending, true
}

func (m *ApprovalManager) ensureDispatch(bridge *PushBridge, server *jrpc2.Server, pending *pendingApproval) error {
	ctx, method, params, err := m.beginDispatch(bridge, server, pending)
	if err != nil {
		return err
	}
	if ctx == nil {
		return nil
	}
	go m.dispatchApproval(ctx, bridge, server, pending.callID, method, params)
	return nil
}

func (m *ApprovalManager) beginDispatch(bridge *PushBridge, server *jrpc2.Server, pending *pendingApproval) (context.Context, string, map[string]any, error) {
	if pending == nil {
		return nil, "", nil, ErrInvalidState("approval pending state is nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.pending[pending.callID]
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

func (m *ApprovalManager) dispatchApproval(ctx context.Context, bridge *PushBridge, server *jrpc2.Server, callID string, paramsMethod string, params map[string]any) {
	raw, err := bridge.CallbackClient(ctx, server, paramsMethod, params)
	if err != nil {
		m.handleDispatchErr(callID, err)
		return
	}
	decision, err := decodeApprovalDecision(raw)
	if err != nil {
		m.failPending(callID, m.lookupPending(callID), err)
		return
	}
	if err := m.Respond(callID, m.lookupRequestID(callID), decision); err != nil && !isApprovalNotFound(err) {
		m.failPending(callID, m.lookupPending(callID), err)
	}
}

func (m *ApprovalManager) handleDispatchErr(callID string, err error) {
	pending := m.lookupPending(callID)
	if pending == nil || errors.Is(err, context.Canceled) {
		m.resetDispatch(callID, pending)
		return
	}
	if isRecoverableDispatchErr(err) {
		m.logger.Warn("approval callback interrupted; pending request kept for restore", "call_id", callID, "error", err)
		m.resetDispatch(callID, pending)
		return
	}
	m.failPending(callID, pending, err)
}

func (m *ApprovalManager) resetDispatch(callID string, pending *pendingApproval) {
	if pending == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.pending[callID]
	if current != pending {
		return
	}
	if pending.cancel != nil {
		pending.cancel()
		pending.cancel = nil
	}
	pending.dispatching = false
}

func (m *ApprovalManager) finishPending(callID string, pending *pendingApproval, decision contract.ApprovalDecision, err error) {
	if pending == nil {
		return
	}
	pending.once.Do(func() {
		m.mu.Lock()
		if current := m.pending[callID]; current == pending {
			delete(m.pending, callID)
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

func (m *ApprovalManager) failPending(callID string, pending *pendingApproval, err error) {
	decision := contract.ApprovalDecision{Reason: decisionReason(contract.ApprovalDecision{}, err)}
	m.finishPending(callID, pending, decision, err)
}

func (m *ApprovalManager) lookupPending(callID string) *pendingApproval {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pending[callID]
}

func (m *ApprovalManager) lookupRequestID(callID string) *int64 {
	pending := m.lookupPending(callID)
	if pending == nil {
		return nil
	}
	return cloneInt64Ptr(pending.requestID)
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

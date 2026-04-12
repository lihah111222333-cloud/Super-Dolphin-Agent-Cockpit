package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type processedApprovalEntry struct {
	decision contract.ApprovalDecision
	err      error
	ready    chan struct{}
	done     bool
}

const processedApprovalLimit = 1000

func (s *session) handleApprovalRequest(method string, params json.RawMessage) {
	if s.approvals == nil {
		return
	}
	payload := append(json.RawMessage(nil), params...)
	shared.SafeGo(s.logger, func() {
		if err := s.requestToolApproval(strings.TrimSpace(method), payload); err != nil && s.logger != nil {
			s.logger.Warn("codexapp: approval request failed", "method", method, "error", err)
		}
	})
}

func (s *session) requestToolApproval(method string, params json.RawMessage) error {
	req, requestID, ok := s.buildApprovalRequest(method, decodeEventPayload(params))
	if !ok {
		return nil
	}
	key := processedApprovalKey(req.CallID, requestID)
	entry, owner := s.beginProcessedApproval(key)
	if !owner {
		decision, err := waitProcessedApproval(entry)
		if err != nil {
			return err
		}
		return s.sendApprovalDecision(requestID, decision)
	}
	decision, err := s.requestApprovalDecision(req)
	s.finishProcessedApproval(key, entry, decision, err)
	if err != nil {
		return err
	}
	return s.sendApprovalDecision(requestID, decision)
}

func (s *session) requestApprovalDecision(req rpc.ApprovalRequest) (contract.ApprovalDecision, error) {
	ctx, cancel := approvalDecisionContext(s.ctx)
	defer cancel()
	if isRequestUserInputMethod(req.SourceMethod) {
		req.Kind = "request_user_input"
		return s.approvals.RequestUserInput(ctx, nil, nil, req)
	}
	return s.approvals.RequestApproval(ctx, nil, nil, req)
}

func approvalDecisionContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return rpc.WithApprovalDeadline(rpc.WithApprovalAutoDeclineOnCancel(ctx))
}

func (s *session) setApprovalPolicy(policy string) {
	if s == nil {
		return
	}
	s.approvalPolicy.Store(strings.TrimSpace(policy))
}

func (s *session) approvalPolicyValue() string {
	if s == nil {
		return ""
	}
	value, _ := s.approvalPolicy.Load().(string)
	return strings.TrimSpace(value)
}

func (s *session) beginProcessedApproval(key string) (*processedApprovalEntry, bool) {
	if s == nil || key == "" {
		return nil, true
	}
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	if s.processedApprovals == nil {
		s.processedApprovals = map[string]*processedApprovalEntry{}
	}
	if entry := s.processedApprovals[key]; entry != nil {
		return entry, false
	}
	if len(s.processedApprovals) >= processedApprovalLimit {
		s.purgeCompletedProcessedApprovalsLocked()
	}
	entry := &processedApprovalEntry{ready: make(chan struct{})}
	s.processedApprovals[key] = entry
	return entry, true
}

func (s *session) purgeCompletedProcessedApprovalsLocked() {
	for key, entry := range s.processedApprovals {
		if entry != nil && entry.done {
			delete(s.processedApprovals, key)
		}
	}
}

func (s *session) finishProcessedApproval(key string, entry *processedApprovalEntry, decision contract.ApprovalDecision, err error) {
	if s == nil || entry == nil {
		return
	}
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	if entry.done {
		return
	}
	entry.decision = cloneApprovalDecision(decision)
	entry.err = err
	entry.done = true
	if err != nil {
		if current := s.processedApprovals[key]; current == entry {
			delete(s.processedApprovals, key)
		}
	}
	close(entry.ready)
}

func (s *session) clearProcessedApprovals() {
	if s == nil {
		return
	}
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	clear(s.processedApprovals)
}

func (s *session) buildApprovalRequest(method string, payload map[string]any) (rpc.ApprovalRequest, int64, bool) {
	if len(payload) == 0 {
		return rpc.ApprovalRequest{}, 0, false
	}
	requestID := int64Value(payload, "requestId", "request_id")
	if requestID <= 0 {
		return rpc.ApprovalRequest{}, 0, false
	}
	callID := payloadCallID(payload, stringValue(payload, "approvalId", "approval_id"), strconv.FormatInt(requestID, 10))
	requestRef := requestID
	return rpc.ApprovalRequest{
		CallID:         callID,
		ApprovalID:     stringValue(payload, "approvalId", "approval_id"),
		ToolName:       payloadToolName(payload),
		AgentID:        s.agentID,
		ThreadID:       payloadThreadID(payload, s.ThreadID()),
		TurnID:         payloadTurnID(payload),
		Reason:         stringValue(payload, "reason", "message"),
		SourceMethod:   method,
		RequestID:      &requestRef,
		ApprovalPolicy: s.approvalPolicyValue(),
		Payload:        payload,
	}, requestID, callID != ""
}

func (s *session) sendApprovalDecision(requestID int64, decision contract.ApprovalDecision) error {
	if requestID <= 0 {
		return errors.New("codexapp: approval request id is required")
	}
	if s == nil || s.transport == nil {
		return errors.New("codexapp: approval transport is not initialized")
	}
	params := map[string]any{"requestId": requestID}
	if decision.Approved != nil {
		params["approved"] = *decision.Approved
	}
	if len(decision.Detail) > 0 {
		params["decision"] = append(json.RawMessage(nil), decision.Detail...)
	} else if reason := strings.TrimSpace(decision.Reason); reason != "" {
		params["decision"] = reason
	}
	_, err := callWithTimeout(s.ctx, callTargetFunc(s.callTransport), 10*time.Second, "approval/respond", params)
	return err
}

func cloneApprovalDecision(decision contract.ApprovalDecision) contract.ApprovalDecision {
	decision.Detail = append(json.RawMessage(nil), decision.Detail...)
	return decision
}

func processedApprovalKey(callID string, requestID int64) string {
	if requestID <= 0 {
		return ""
	}
	id := strconv.FormatInt(requestID, 10)
	return shared.FirstNonEmpty(callID, id) + ":" + id
}

func waitProcessedApproval(entry *processedApprovalEntry) (contract.ApprovalDecision, error) {
	if entry == nil {
		return contract.ApprovalDecision{}, nil
	}
	<-entry.ready
	return cloneApprovalDecision(entry.decision), entry.err
}

func isApprovalBridgeMethod(method string) bool {
	return hasMethod(method, approvalBridgeMethods)
}

func isRequestUserInputMethod(method string) bool {
	return hasMethod(method, requestUserInputMethods)
}

func (s *session) onNotification(method string, params json.RawMessage) {
	s.noteReadActivity()
	if s.isAlienThreadEvent(params) {
		pkglogger.Warn("codexapp: dropped alien thread event",
			"agent_id", s.agentID, "method", method, "own_thread", s.ThreadID())
		return
	}
	if s.shouldSuppressTurnEvent(method, params) {
		pkglogger.Warn("codexapp: suppressed duplicate turn terminal event",
			"agent_id", s.agentID, "method", method)
		return
	}
	method = strings.TrimSpace(method)
	raw := dto.RawProviderEvent{EventType: method, Data: params}
	if !isApprovalBridgeMethod(method) || s.approvals == nil {
		s.dispatch(raw)
	}
	s.handleNotificationAction(method, params)
}

func (s *session) handleNotificationAction(method string, params json.RawMessage) {
	switch {
	case isApprovalBridgeMethod(method):
		s.handleApprovalRequest(method, params)
	case isTurnTerminalEvent(method):
		s.finishTurn(params, turnTerminalSuccess(method, decodeEventPayload(params)))
	case method == "connection.dead":
		s.handleConnectionDead(params)
	}
}

// isAlienThreadEvent returns true when the event payload carries a threadId
// that does not match this session's thread. Events without a threadId are
// never considered alien.
func (s *session) isAlienThreadEvent(params json.RawMessage) bool {
	own := s.ThreadID()
	if own == "" {
		return false
	}
	var envelope struct {
		ThreadID string `json:"threadId"`
	}
	if err := json.Unmarshal(params, &envelope); err != nil {
		return false
	}
	eventThread := strings.TrimSpace(envelope.ThreadID)
	if eventThread == "" {
		return false
	}
	return eventThread != own
}

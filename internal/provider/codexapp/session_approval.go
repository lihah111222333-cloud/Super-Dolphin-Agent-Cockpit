package codexapp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimesafe"
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
	runtimesafe.SafeGo(s.ctx, s.logger, "codexapp.session.toolApprovalRequest", func(ctx context.Context) {
		if err := s.requestToolApprovalWithContext(ctx, strings.TrimSpace(method), payload); err != nil && s.logger != nil {
			s.logger.Warn("codexapp: approval request failed", "method", method, "error", err)
		}
	})
}

func (s *session) requestToolApprovalWithContext(ctx context.Context, method string, params json.RawMessage) error {
	req, requestID, ok := s.buildApprovalRequest(method, decodeEventPayload(params))
	if !ok {
		return nil
	}
	key := processedApprovalRequestKey(req, requestID)
	entry, owner := s.beginProcessedApproval(key)
	if !owner {
		decision, err := s.waitProcessedApproval(ctx, entry)
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
	if s == nil {
		return contract.ApprovalDecision{}, errors.New("session is nil")
	}
	ctx, cancel := approvalDecisionContext(s.ctx)
	defer cancel()
	if s.approvalDecisionHook != nil {
		return s.approvalDecisionHook(ctx, req)
	}
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

// beginProcessedApproval 处理beginprocessed审批。
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

// finishProcessedApproval 处理finishprocessed审批。
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

// sendApprovalDecision 处理send审批decision。
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
	if callID = strings.TrimSpace(callID); callID != "" {
		return callID + ":" + id
	}
	return id
}

func processedApprovalRequestKey(req rpc.ApprovalRequest, requestID int64) string {
	if requestID <= 0 {
		return ""
	}
	fingerprint := approvalRequestFingerprint(req, requestID)
	if fingerprint == "" {
		return processedApprovalKey(req.CallID, requestID)
	}
	return strconv.FormatInt(requestID, 10) + ":" + fingerprint
}

func approvalRequestFingerprint(req rpc.ApprovalRequest, requestID int64) string {
	payload := normalizeApprovalFingerprintPayload(req.Payload)
	envelope := map[string]any{
		"requestId": requestID,
		"method":    strings.TrimSpace(req.SourceMethod),
		"payload":   payload,
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func normalizeApprovalFingerprintPayload(payload map[string]any) any {
	out := make(map[string]any, len(payload))
	for key, child := range payload {
		switch strings.TrimSpace(key) {
		case "callId", "call_id", "approvalId", "approval_id":
			continue
		default:
			out[key] = normalizeApprovalFingerprintValue(child)
		}
	}
	return out
}

func normalizeApprovalFingerprintValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			out[key] = normalizeApprovalFingerprintValue(child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = normalizeApprovalFingerprintValue(child)
		}
		return out
	default:
		return value
	}
}

// waitProcessedApproval 等待审批请求处理完成。
func (s *session) waitProcessedApproval(ctx context.Context, entry *processedApprovalEntry) (contract.ApprovalDecision, error) {
	if entry == nil {
		return contract.ApprovalDecision{}, nil
	}
	ctx = shared.NonNilContext(ctx)
	sessionCtx := context.Background()
	if s != nil {
		sessionCtx = shared.NonNilContext(s.ctx)
	}
	select {
	case <-entry.ready:
		return cloneApprovalDecision(entry.decision), entry.err
	case <-ctx.Done():
		return contract.ApprovalDecision{}, ctx.Err()
	case <-sessionCtx.Done():
		return contract.ApprovalDecision{}, sessionCtx.Err()
	}
}

func isApprovalBridgeMethod(method string) bool {
	return hasMethod(method, approvalBridgeMethods)
}

func isRequestUserInputMethod(method string) bool {
	return hasMethod(method, requestUserInputMethods)
}

// onNotification 处理onnotification。
func (s *session) onNotification(method string, params json.RawMessage) {
	s.noteReadActivity()
	if eventThread, ok := s.alienThreadEventThread(params); ok {
		pkglogger.Warn("codexapp: dropped alien thread event",
			"agent_id", s.agentID,
			"method", method,
			"own_thread", s.ThreadID(),
			"event_thread", eventThread,
		)
		return
	}
	// ADR-015 v4.1 §2.1: sniff TurnOutputDelta / TurnCompleted BEFORE
	// shouldSuppressTurnEvent so suppressed-completed paths (forceCompleteTurn)
	// still flush the accumulated buffer.
	params = s.sniffTurnOutput(method, params)
	if s.shouldSuppressTurnEvent(method, params) {
		pkglogger.Warn("codexapp: suppressed duplicate turn terminal event",
			"agent_id", s.agentID, "method", method)
		return
	}
	if s.shouldSuppressToolEndEvent(method, params) {
		pkglogger.Warn("codexapp: suppressed duplicate tool terminal event",
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

// sniffTurnOutput inspects the incoming notification and:
//   - accumulates stream="message" TurnOutputDelta payloads into the
//     per-turn buffer (ADR-015 v4.1 §2.1)
//   - on TurnCompleted-class events, merges the buffer into the payload
//     under key "result" (and sets "truncated"=true when the buffer hit the
//     1 MiB hard cap), returning a re-encoded params for downstream dispatch
//
// For unrelated methods, sniffTurnOutput returns the original params
// unchanged so the normal dispatch path is preserved bit-for-bit.
func (s *session) sniffTurnOutput(method string, params json.RawMessage) json.RawMessage {
	switch {
	case isMessageStreamDeltaEvent(method):
		s.absorbMessageDelta(params)
		return params
	case isTurnTerminalEvent(method):
		return s.injectAccumulatedResult(params)
	default:
		return params
	}
}

// absorbMessageDelta routes a stream="message" TurnOutputDelta payload into
// the per-turn accumulator. No-op for malformed / empty payloads.
func (s *session) absorbMessageDelta(params json.RawMessage) {
	payload := decodeEventPayload(params)
	turnID := payloadTurnID(payload)
	delta := stringValue(payload, "delta", "content")
	if turnID == "" || delta == "" {
		return
	}
	s.appendTurnOutputDelta(turnID, delta)
}

// injectAccumulatedResult merges the accumulated message-stream content into
// the TurnCompleted payload under "result" (and "truncated" when the 1 MiB
// cap latched). Returns the rewritten params, falling back to the original
// when no buffer or marshal fails.
// injectAccumulatedResult 生成injectaccumulated结果。
func (s *session) injectAccumulatedResult(params json.RawMessage) json.RawMessage {
	payload := decodeEventPayload(params)
	turnID := payloadTurnID(payload)
	if turnID == "" {
		return params
	}
	merged, truncated := s.consumeTurnOutputAccumulator(turnID)
	if merged == "" && !truncated {
		return params
	}
	if payload == nil {
		payload = map[string]any{}
	}
	// Do not clobber an existing payload["result"] the provider may already
	// supply (defensive forward compatibility).
	if _, ok := payload["result"]; !ok && merged != "" {
		payload["result"] = merged
	}
	if truncated {
		payload["truncated"] = true
	}
	return encodeEventPayload(payload, params)
}

func (s *session) handleNotificationAction(method string, params json.RawMessage) {
	switch {
	case isApprovalBridgeMethod(method):
		s.handleApprovalRequest(method, params)
	case isTurnTerminalEvent(method):
		s.finishTurn(params, turnTerminalSuccess(method, decodeEventPayload(params)))
	case s.completeRolloutAssistantMessage(method, params):
	case method == "connection.dead":
		s.handleConnectionDead(params)
	}
}

func (s *session) completeRolloutAssistantMessage(method string, params json.RawMessage) bool {
	if strings.TrimSpace(method) != "response_item" {
		return false
	}
	msg, ok := parseRolloutLine(mustJSON(map[string]any{"type": "response_item", "payload": params}))
	if !ok || msg.Role != "assistant" {
		return false
	}
	payload := decodeEventPayload(params)
	turnID := shared.FirstNonEmpty(payloadTurnID(payload), payloadTurnID(codexToolItemPayload(payload)))
	s.mu.Lock()
	if turnID == "" {
		turnID = s.activeTurnID
	}
	ok = turnID != "" && turnID == s.activeTurnID && s.turns[turnID] != nil
	s.mu.Unlock()
	if !ok {
		return false
	}
	s.completeSyntheticTurn(turnID, "rollout_assistant_message", msg.Content)
	return true
}

func (s *session) alienThreadEventThread(params json.RawMessage) (string, bool) {
	own := s.ThreadID()
	if own == "" {
		return "", false
	}
	var envelope struct {
		ThreadID string `json:"threadId"`
	}
	if err := json.Unmarshal(params, &envelope); err != nil {
		return "", false
	}
	eventThread := strings.TrimSpace(envelope.ThreadID)
	if eventThread == "" {
		return "", false
	}
	if eventThread == own {
		return "", false
	}
	return eventThread, true
}

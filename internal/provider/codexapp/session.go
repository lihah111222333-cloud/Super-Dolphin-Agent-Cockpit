package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
)

type session struct {
	agentID            string
	threadID           atomic.Value
	approvalPolicy     atomic.Value
	transport          *transport
	caps               dto.CapabilitySet
	recovery           *recoveryManager
	history            *rolloutReader
	logger             *slog.Logger
	dispatcher         *unified.EventDispatcher
	approvals          *rpc.ApprovalManager
	ctx                context.Context
	cancel             context.CancelFunc
	mu                 sync.Mutex
	approvalMu         sync.Mutex
	recoveryMu         sync.Mutex
	readLoopMu         sync.Mutex
	readLoopDone       chan struct{}
	lastReadAt         atomic.Int64
	turns              map[string]*turnHandle
	activeTurnID       string
	pendingTurn        *turnReplayState
	suppressed         map[string]struct{}
	processedApprovals map[string]*processedApprovalEntry
}

var _ contract.Session = (*session)(nil)

type turnHandle struct {
	localID    string
	providerID string
	done       chan struct{}
	mu         sync.RWMutex
	err        error
	once       sync.Once
}

func newSession(
	logger *slog.Logger,
	serverURL string,
	agentID string,
	dispatcher *unified.EventDispatcher,
	approvals *rpc.ApprovalManager,
) (*session, error) {
	transport, err := newTransport(serverURL)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &session{
		agentID:            strings.TrimSpace(agentID),
		transport:          transport,
		caps:               cloneCaps(codexCapabilities),
		recovery:           &recoveryManager{transport: transport, logger: logger, maxRetry: 3},
		history:            &rolloutReader{logger: logger, transport: transport},
		logger:             logger,
		dispatcher:         dispatcher,
		approvals:          approvals,
		ctx:                ctx,
		cancel:             cancel,
		turns:              map[string]*turnHandle{},
		suppressed:         map[string]struct{}{},
		processedApprovals: map[string]*processedApprovalEntry{},
	}
	s.noteReadActivity()
	s.startReadLoop()
	s.startHealthLoop()
	return s, nil
}
func (s *session) Capabilities() dto.CapabilitySet { return cloneCaps(s.caps) }

func (s *session) StartTurn(ctx context.Context, req dto.TurnRequest) (contract.TurnHandle, error) {
	threadID := s.resolveThreadID(req.ThreadID)
	if threadID == "" {
		return nil, errors.New("codexapp: thread id is required")
	}
	params := buildTurnStartParams(threadID, req)
	callCtx, cancel := withTimeout(ctx, 30*time.Second)
	defer cancel()
	raw, err := s.callTransport(callCtx, "turn/start", params)
	if err != nil {
		return nil, err
	}
	var resp turnRPCResult
	if err := json.Unmarshal(raw, &resp); err != nil || strings.TrimSpace(resp.Turn.ID) == "" {
		return nil, errors.New("codexapp: invalid turn/start response")
	}
	providerID := strings.TrimSpace(resp.Turn.ID)
	h := newTurnHandle(resolveLocalTurnID(req.LocalID, providerID), providerID)
	s.mu.Lock()
	s.turns[providerID] = h
	s.activeTurnID = providerID
	s.mu.Unlock()
	s.rememberPendingTurn(h, params)
	return h, nil
}

func (s *session) Steer(ctx context.Context, req dto.SteerRequest) error {
	threadID := s.resolveThreadID(req.ThreadID)
	if threadID == "" {
		return errors.New("codexapp: thread id is required")
	}
	expectedTurnID := strings.TrimSpace(req.ExpectedTurnID)
	if expectedTurnID == "" {
		return errors.New("codexapp: expected turn id is required")
	}
	callCtx, cancel := withTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err := s.callTransport(callCtx, "turn/steer", buildTurnSteerParams(threadID, req))
	return err
}

func (s *session) Interrupt(ctx context.Context, req dto.InterruptRequest) error {
	threadID := s.resolveThreadID(req.ThreadID)
	if threadID == "" {
		return errors.New("codexapp: thread id is required")
	}
	params := map[string]any{"threadId": threadID}
	if source := strings.TrimSpace(req.Source); source != "" {
		params["source"] = source
	}
	callCtx, cancel := withTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := s.callTransport(callCtx, "turn/interrupt", params)
	return err
}

func (s *session) ForceComplete(ctx context.Context, req dto.ForceCompleteRequest) error {
	threadID := s.resolveThreadID(req.ThreadID)
	if threadID == "" {
		return errors.New("codexapp: thread id is required")
	}
	callCtx, cancel := withTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := s.callTransport(callCtx, "turn/forceComplete", map[string]any{"threadId": threadID}); err != nil {
		return err
	}
	s.forceCompleteTurn(strings.TrimSpace(req.ProviderID))
	return nil
}

func (s *session) ListThreads(ctx context.Context) ([]dto.ThreadRef, error) {
	callCtx, cancel := withTimeout(ctx, 10*time.Second)
	defer cancel()
	raw, err := s.callTransport(callCtx, "thread/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var listed struct {
		Data []dto.ThreadRef `json:"data"`
	}
	if json.Unmarshal(raw, &listed) == nil && len(listed.Data) > 0 {
		return listed.Data, nil
	}
	var loaded struct {
		Data []string `json:"data"`
	}
	if err := json.Unmarshal(raw, &loaded); err != nil {
		return nil, err
	}
	threads := make([]dto.ThreadRef, 0, len(loaded.Data))
	for _, id := range loaded.Data {
		threads = append(threads, dto.ThreadRef{ID: strings.TrimSpace(id)})
	}
	return threads, nil
}

func (s *session) ForkThread(ctx context.Context, req dto.ForkRequest) (dto.ForkResult, error) {
	threadID := s.resolveThreadID(req.ThreadID)
	if threadID == "" {
		return dto.ForkResult{}, errors.New("codexapp: thread id is required")
	}
	callCtx, cancel := withTimeout(ctx, 10*time.Second)
	defer cancel()
	raw, err := s.callTransport(callCtx, "thread/fork", map[string]any{"threadId": threadID})
	if err != nil {
		return dto.ForkResult{}, err
	}
	id, err := decodeThreadID(raw, "")
	if err != nil {
		return dto.ForkResult{}, err
	}
	return dto.ForkResult{NewThreadID: id}, nil
}

func (s *session) Configure(ctx context.Context, patch dto.ThreadConfigPatch) error {
	threadID := s.ThreadID()
	if threadID == "" {
		return errors.New("codexapp: thread id is required")
	}
	if patch.Model != nil || patch.Effort != nil {
		params := map[string]any{"threadId": threadID}
		if patch.Model != nil {
			params["model"] = strings.TrimSpace(*patch.Model)
		}
		if patch.Effort != nil {
			params["effort"] = strings.TrimSpace(*patch.Effort)
		}
		callCtx, cancel := withTimeout(ctx, 10*time.Second)
		defer cancel()
		_, err := s.callTransport(callCtx, "thread/config/set", params)
		if err != nil {
			return err
		}
	}
	if err := s.applySlashConfig(ctx, threadID, "thread/personality/set", "personality", patch.Personality); err != nil {
		return err
	}
	if err := s.applySlashConfig(ctx, threadID, "thread/approvals/set", "policy", patch.Approvals); err != nil {
		return err
	}
	if patch.Approvals != nil {
		s.setApprovalPolicy(*patch.Approvals)
	}
	return nil
}

func (s *session) Close(context.Context) error {
	s.failTurns(errors.New("codexapp: session closed"))
	s.clearProcessedApprovals()
	s.cancel()
	return s.transport.Close()
}

func (s *session) ForceStop() error {
	s.failTurns(errors.New("codexapp: session stopped"))
	s.clearProcessedApprovals()
	s.cancel()
	return s.transport.Kill()
}

func (s *session) applySlashConfig(ctx context.Context, threadID, method, key string, value *string) error {
	if value == nil {
		return nil
	}
	arg := strings.TrimSpace(*value)
	if arg == "" {
		return nil
	}
	callCtx, cancel := withTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := s.callTransport(callCtx, method, map[string]any{"threadId": threadID, key: arg, "args": arg})
	return err
}

func (s *session) onNotification(method string, params json.RawMessage) {
	s.noteReadActivity()
	if s.shouldSuppressTurnEvent(method, params) {
		return
	}
	raw := dto.RawProviderEvent{EventType: method, Data: params}
	method = strings.TrimSpace(method)
	if !isApprovalBridgeMethod(method) || s.approvals == nil {
		s.dispatch(raw)
	}
	switch {
	case isApprovalBridgeMethod(method):
		s.handleApprovalRequest(method, params)
	case method == "turn/completed" || method == "turn/aborted":
		s.finishTurn(params, method == "turn/completed")
	case method == "connection.dead":
		s.handleConnectionDead(params)
	}
}

func (s *session) dispatch(raw dto.RawProviderEvent) {
	if s.dispatcher == nil {
		return
	}
	payload := decodeAnyPayload(raw.Data)
	if len(payload) > 0 && stringValue(payload, "agentId", "agent_id") == "" {
		if agentID := strings.TrimSpace(s.agentID); agentID != "" {
			payload["agentId"] = agentID
			raw.Data = payload
		}
	}
	s.dispatcher.Dispatch(raw)
}

func (s *session) finishTurn(params json.RawMessage, optimistic bool) {
	payload := decodeEventPayload(params)
	turnID := strings.TrimSpace(stringValue(payload, "turnId", "turn_id"))
	if turnID == "" {
		return
	}
	h := s.takeTurn(turnID)
	if h == nil {
		return
	}
	errText := strings.TrimSpace(firstNonEmpty(
		stringValue(payload, "error", "message", "reason"),
		stringValue(nestedValue(payload, "error"), "message"),
	))
	if errText == "" && optimistic {
		h.complete(nil)
		return
	}
	if errText == "" {
		errText = "turn failed"
	}
	h.complete(errors.New(errText))
}

func (s *session) takeTurn(turnID string) *turnHandle {
	s.mu.Lock()
	defer s.mu.Unlock()
	if turnID == "" {
		turnID = s.activeTurnID
	}
	h := s.turns[turnID]
	delete(s.turns, turnID)
	if turnID == s.activeTurnID {
		s.activeTurnID = ""
	}
	if s.pendingTurn != nil && s.pendingTurn.handle == h {
		s.pendingTurn = nil
	}
	return h
}

func (s *session) forceCompleteTurn(turnID string) {
	if turnID == "" {
		turnID = strings.TrimSpace(s.activeTurnID)
	}
	if turnID == "" {
		return
	}
	s.suppressTurn(turnID)
	s.dispatch(dto.RawProviderEvent{EventType: "turn/completed", Data: map[string]any{
		"turnId":  turnID,
		"success": true,
		"status":  "completed",
		"reason":  "force_complete",
	}})
	if h := s.takeTurn(turnID); h != nil {
		h.complete(nil)
	}
}

func (s *session) shouldSuppressTurnEvent(method string, params json.RawMessage) bool {
	method = strings.TrimSpace(method)
	if method != "turn/completed" && method != "turn/aborted" {
		return false
	}
	turnID := strings.TrimSpace(stringValue(decodeEventPayload(params), "turnId", "turn_id"))
	return s.consumeSuppressedTurn(turnID)
}

func (s *session) suppressTurn(turnID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.suppressed[turnID] = struct{}{}
}

func (s *session) consumeSuppressedTurn(turnID string) bool {
	if turnID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.suppressed[turnID]; !ok {
		return false
	}
	delete(s.suppressed, turnID)
	return true
}

func (s *session) failTurns(err error) {
	s.mu.Lock()
	turns := s.turns
	s.turns = map[string]*turnHandle{}
	s.activeTurnID = ""
	s.pendingTurn = nil
	s.mu.Unlock()
	for _, h := range turns {
		h.complete(err)
	}
}

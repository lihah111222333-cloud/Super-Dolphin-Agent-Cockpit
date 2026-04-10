package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type session struct {
	agentID            string
	threadID           atomic.Value
	approvalPolicy     atomic.Value
	transport          *transport
	manager            *ServerManager
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
	recoveryCount      atomic.Int32
	turns              map[string]*turnHandle
	activeTurnID       string
	pendingTurn        *turnReplayState
	suppressed         map[string]struct{}
	processedApprovals map[string]*processedApprovalEntry
	runtimeConfig      map[string]any
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
	transportCtx context.Context,
	logger *slog.Logger,
	serverURL string,
	agentID string,
	dispatcher *unified.EventDispatcher,
	approvals *rpc.ApprovalManager,
	manager *ServerManager,
) (*session, error) {
	// Each session owns its own WS connection. When a ServerManager is
	// running, connect to the shared app-server process (no local spawn);
	// otherwise create a standalone transport with its own process.
	url := serverURL
	if manager != nil && manager.Running() {
		url = manager.ServerURL()
	}
	t, err := newTransport(transportCtx, url)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &session{
		agentID:            strings.TrimSpace(agentID),
		transport:          t,
		manager:            manager,
		caps:               cloneCaps(codexCapabilities),
		recovery:           &recoveryManager{transport: t, logger: logger, maxRetry: 3},
		history:            &rolloutReader{logger: logger, transport: t},
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

func (s *session) onInboundMessage(ctx context.Context, resp Responder, msg RawMessage) {
	s.noteReadActivity()
	if toolHandler := s.manager.getToolHandler(); len(msg.ID) != 0 && toolHandler != nil && isToolCallMethod(msg.Method) {
		go func() { result, err := toolHandler(ctx, msg); _ = resp.RespondWithID(msg.ID, result, err) }()
		return
	}
	if isKnownRequestMethod(msg.Method) { s.onNotification(msg.Method, msg.Params); return }
	if len(msg.ID) != 0 { _ = resp.RespondWithID(msg.ID, nil, fmt.Errorf("method not supported: %s", msg.Method)); return }
	s.onNotification(msg.Method, msg.Params)
}

func isKnownRequestMethod(method string) bool { return hasMethod(method, approvalBridgeMethods) || hasMethod(method, requestUserInputMethods) }
func isToolCallMethod(method string) bool {
	switch strings.TrimSpace(method) { case "item/tool/call", "dynamic_tool_call", "tool.call.begin": return true }
	return false
}
func (s *session) Capabilities() dto.CapabilitySet { return cloneCaps(s.caps) }

func (s *session) setRuntimeConfig(cfg map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtimeConfig = shared.CloneRuntimeConfigMap(cfg)
}

func (s *session) RuntimeConfigSnapshot() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := shared.CloneRuntimeConfigMap(s.runtimeConfig)
	if len(out) == 0 {
		out = map[string]any{}
	}
	if value := s.approvalPolicyValue(); value != "" {
		out["approvalPolicy"] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *session) StartTurn(ctx context.Context, req dto.TurnRequest) (contract.TurnHandle, error) {
	threadID, err := requireThreadID(s, req.ThreadID)
	if err != nil {
		return nil, err
	}
	params := buildTurnStartParams(threadID, req)
	raw, err := callWithTimeout(ctx, callTargetFunc(s.callTransport), 30*time.Second, "turn/start", params)
	if err != nil {
		return nil, err
	}
	resp, err := decodeTurnStartResult(raw)
	if err != nil {
		return nil, err
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
	threadID, err := requireThreadID(s, req.ThreadID)
	if err != nil {
		return err
	}
	expectedTurnID := strings.TrimSpace(req.ExpectedTurnID)
	if expectedTurnID == "" {
		return errors.New("codexapp: expected turn id is required")
	}
	_, err = callWithTimeout(ctx, callTargetFunc(s.callTransport), 30*time.Second, "turn/steer", buildTurnSteerParams(threadID, req))
	return err
}

func (s *session) Interrupt(ctx context.Context, req dto.InterruptRequest) error {
	threadID, err := requireThreadID(s, req.ThreadID)
	if err != nil {
		return err
	}
	params := map[string]any{"threadId": threadID}
	if source := strings.TrimSpace(req.Source); source != "" {
		params["source"] = source
	}
	_, err = callWithTimeout(ctx, callTargetFunc(s.callTransport), 10*time.Second, "turn/interrupt", params)
	return err
}

func (s *session) ForceComplete(ctx context.Context, req dto.ForceCompleteRequest) error {
	threadID, err := requireThreadID(s, req.ThreadID)
	if err != nil {
		return err
	}
	if _, err := callWithTimeout(ctx, callTargetFunc(s.callTransport), 10*time.Second, "turn/forceComplete", map[string]any{"threadId": threadID}); err != nil {
		return err
	}
	s.forceCompleteTurn(strings.TrimSpace(req.ProviderID))
	return nil
}

func (s *session) ListThreads(ctx context.Context) ([]dto.ThreadRef, error) {
	raw, err := callWithTimeout(ctx, callTargetFunc(s.callTransport), 10*time.Second, "thread/list", map[string]any{})
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
	threadID, err := requireThreadID(s, req.ThreadID)
	if err != nil {
		return dto.ForkResult{}, err
	}
	raw, err := callWithTimeout(ctx, callTargetFunc(s.callTransport), 10*time.Second, "thread/fork", map[string]any{"threadId": threadID})
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
	return s.configureThread(ctx, patch)
}

func (s *session) Close(context.Context) error {
	return s.shutdownSession(true)
}

func (s *session) ForceStop() error {
	return s.shutdownSession(false)
}

// shutdownSessionCleanup handles cleanup when a session shuts down.
func (s *session) shutdownSessionCleanup() {
	// placeholder for future idle tracking cleanup
}

func (s *session) dispatch(raw dto.RawProviderEvent) {
	if s.dispatcher == nil {
		pkglogger.Warn("codexapp: dispatch skipped: no dispatcher",
			"agent_id", s.agentID, "event_type", raw.EventType)
		return
	}
	payload := decodeAnyPayload(raw.Data)
	if len(payload) > 0 {
		if agentID := strings.TrimSpace(s.agentID); agentID != "" {
			if payloadAgentID(payload) == "" {
				payload["agentId"] = agentID
			}
			// Always map the codex-internal threadId to our public agentId so
			// downstream bus events use the correct thread identity. Without
			// this, the UI sees the raw providerThreadId and creates a
			// duplicate agent entry.
			if tid, _ := payload["threadId"].(string); tid != "" && tid != agentID {
				payload["threadId"] = agentID
			}
			raw.Data = payload
		}
	}
	s.dispatcher.Dispatch(raw)
}

func (s *session) finishTurn(params json.RawMessage, optimistic bool) {
	payload := decodeEventPayload(params)
	turnID := payloadTurnID(payload)
	if turnID == "" {
		return
	}
	h := s.takeTurn(turnID)
	if h == nil {
		return
	}
	errText := strings.TrimSpace(shared.FirstNonEmpty(
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
	if !isTurnTerminalEvent(method) {
		return false
	}
	turnID := payloadTurnID(decodeEventPayload(params))
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

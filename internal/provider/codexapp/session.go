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
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimesafe"
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
	lastReadAt         atomic.Int64
	recoveryCount      atomic.Int32
	turns              map[string]*turnHandle
	activeTurnID       string
	pendingTurn        *turnReplayState
	suppressed         map[string]struct{}
	processedApprovals map[string]*processedApprovalEntry
	runtimeConfig      map[string]any
	// poolRelease is set when the session was acquired from the P21
	// ServerPool (multi-provider Codex path). It decrements the entry's
	// refCount and closes the app-server process group when this was the last
	// session; nil for ServerManager-backed sessions.
	poolRelease     func()
	poolReleaseOnce sync.Once
	// runtime is the session-private RunnerModule owner introduced by P22
	// P1c. It replaces the implicit reader / health / recovery goroutines
	// newSession() used to start. driver.StartSession / ResumeSession call
	// runtime.Start() explicitly; Close / ForceStop drain via runtime.Stop().
	runtime *SessionRuntime
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
	return newSessionWithOptions(
		transportCtx,
		logger,
		serverURL,
		agentID,
		dispatcher,
		approvals,
		manager,
	)
}

func newSessionWithOptions(
	transportCtx context.Context,
	logger *slog.Logger,
	serverURL string,
	agentID string,
	dispatcher *unified.EventDispatcher,
	approvals *rpc.ApprovalManager,
	manager *ServerManager,
	opts ...sessionOption,
) (*session, error) {
	if logger == nil {
		logger = pkglogger.Get()
	}
	cfg := sessionOptions{}
	for _, opt := range opts {
		opt(&cfg)
	}
	url, err := resolveSessionTransportURL(transportCtx, serverURL, manager, cfg)
	if err != nil {
		return nil, err
	}
	t, err := newTransport(transportCtx, url)
	if err != nil {
		// On spawn failure we must still release the pool slot we
		// reserved so the next Acquire sees accurate refCount.
		releaseSessionPoolSlot(cfg)
		return nil, err
	}
	// P22 P1c: session ctx derives from transportCtx so shutdown of the
	// transport (or the caller's scope) cascades into session.Close path.
	ctx, cancel := context.WithCancel(transportCtx)
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
	s.poolRelease = cfg.poolRelease
	s.noteReadActivity()
	// P1c: newSession only builds the runtime handle. Start() is an explicit
	// production call site inside driver.StartSession / driver.ResumeSession.
	s.runtime = newSessionRuntime(s, logger)
	return s, nil
}

// Each session owns its own WS connection. Precedence:
//   - explicit poolURL (P21 multi-provider Codex, per-codexHome pool)
//   - ServerManager shared URL (single-instance Codex)
//   - raw serverURL arg (remote debug override)
func resolveSessionTransportURL(
	ctx context.Context,
	serverURL string,
	manager *ServerManager,
	cfg sessionOptions,
) (string, error) {
	if cfg.poolURL != "" {
		return cfg.poolURL, nil
	}
	if manager != nil && manager.Running() {
		return manager.ServerURL(), nil
	}
	if manager != nil && strings.TrimSpace(serverURL) == "" {
		if err := manager.EnsureRunning(ctx); err != nil {
			return "", err
		}
		return manager.ServerURL(), nil
	}
	return serverURL, nil
}

func releaseSessionPoolSlot(cfg sessionOptions) {
	if cfg.poolRelease != nil {
		cfg.poolRelease()
	}
}

// sessionOption is a functional option for newSession used to pass
// pool-specific context without exploding the positional signature
// every existing non-pool caller relies on.
type sessionOption func(*sessionOptions)

type sessionOptions struct {
	poolURL     string
	poolRelease func()
}

// withPoolServer wires a ServerPool-backed SpawnedServer into the
// session. The release function is invoked exactly once during
// shutdownSessionCleanup (guarded by poolReleaseOnce).
func withPoolServer(url string, release func()) sessionOption {
	return func(o *sessionOptions) {
		o.poolURL = url
		o.poolRelease = release
	}
}

func (s *session) onInboundMessage(ctx context.Context, resp Responder, msg RawMessage) {
	s.noteReadActivity()
	if toolHandler := s.manager.getToolHandler(); len(msg.ID) != 0 && toolHandler != nil && isToolCallMethod(msg.Method) {
		// P20.18 Phase 1：Codex 发的 item/tool/call params 只含 name + arguments，不包含
		// agentId（agent 是宿主概念 codex 不知道）。旧的 peer-routed 工具不需 agentId，但
		// host-direct 分支（skill_expand_body / skill_read_resource）需要从 agentId 解析 cwd，
		// 不 enrich 会 100% 失败。这里把 session 持有的 agentID 覆盖写入 msg.Params 后再转发。
		enriched := enrichToolCallParams(msg, s.agentID)
		runtimesafe.SafeGo(s.ctx, s.logger, "codexapp.session.toolCall", func(_ context.Context) {
			result, err := toolHandler(ctx, enriched)
			_ = resp.RespondWithID(msg.ID, result, err)
		})
		return
	}
	if isKnownRequestMethod(msg.Method) {
		s.onNotification(msg.Method, msg.Params)
		return
	}
	if len(msg.ID) != 0 {
		_ = resp.RespondWithID(msg.ID, nil, fmt.Errorf("method not supported: %s", msg.Method))
		return
	}
	s.onNotification(msg.Method, msg.Params)
}

func isKnownRequestMethod(method string) bool {
	return hasMethod(method, approvalBridgeMethods) || hasMethod(method, requestUserInputMethods)
}
func isToolCallMethod(method string) bool {
	switch strings.TrimSpace(method) {
	case "item/tool/call", "dynamic_tool_call", "tool.call.begin":
		return true
	}
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
	if value := strings.TrimSpace(shared.FirstNonEmpty(
		configString(out, "baseInstructions"),
		configString(out, "instructions"),
	)); value != "" {
		out["baseInstructions"] = value
	}
	if value := strings.TrimSpace(shared.FirstNonEmpty(
		configString(out, "developerInstructions"),
		configString(out, "developer_instructions"),
	)); value != "" {
		out["developerInstructions"] = value
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
	// Fill model/effort from session runtimeConfig if not set by turn request.
	// thread/config/set stores these in runtimeConfig; they take effect on the next turn.
	if params.Model == "" {
		params.Model = s.runtimeConfigString("model")
	}
	if params.Effort == "" {
		params.Effort = s.runtimeConfigString("effort")
	}
	pkglogger.Debug("codexapp: turn/start params",
		"agent_id", s.agentID,
		"model", params.Model,
		"effort", params.Effort,
	)
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
	turnID := strings.TrimSpace(req.TurnID)
	if turnID == "" {
		s.mu.Lock()
		turnID = strings.TrimSpace(s.activeTurnID)
		s.mu.Unlock()
	}
	params := buildTurnInterruptParams(threadID, turnID, req.Source)
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

// SessionRuntime returns the session-private runtime handle. Exposed for
// explicit Start() call sites (driver.StartSession / ResumeSession) and
// tests; no production code outside this package should reach into it.
func (s *session) SessionRuntime() *SessionRuntime { return s.runtime }

// shutdownSessionCleanup handles cleanup when a session shuts down.
// Currently its single job is to return a pool-backed session to the
// ServerPool so the app-server can be reclaimed when this was the last
// session. Idempotent so concurrent Close + ForceStop can't double-release.
func (s *session) shutdownSessionCleanup() {
	if s == nil {
		return
	}
	s.poolReleaseOnce.Do(func() {
		if s.poolRelease != nil {
			s.poolRelease()
		}
	})
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
		// Override contextWindowTokens when the Codex CLI uses fallback
		// metadata (it reports a wrong value for models it doesn't know).
		if cw := contextWindowForModel(s.runtimeConfigString("model")); cw > 0 {
			s.patchContextWindowInPayload(payload, cw)
			raw.Data = payload
		}
	}
	s.dispatcher.Dispatch(raw)
}

// patchContextWindowInPayload ensures all nested locations where the Codex CLI
// might report a context window carry the authoritative value.
func (s *session) patchContextWindowInPayload(payload map[string]any, cw int) {
	for _, key := range []string{"contextWindowTokens", "contextWindow", "modelContextWindow", "context_window"} {
		if _, ok := payload[key]; ok {
			payload[key] = cw
		}
	}
	if usage, ok := payload["usage"].(map[string]any); ok {
		for _, key := range []string{"contextWindowTokens", "contextWindow", "context_window"} {
			if _, ok := usage[key]; ok {
				usage[key] = cw
			}
		}
	}
	if tu, ok := payload["tokenUsage"].(map[string]any); ok {
		for _, key := range []string{"contextWindowTokens", "contextWindow", "modelContextWindow"} {
			if _, ok := tu[key]; ok {
				tu[key] = cw
			}
		}
	}
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

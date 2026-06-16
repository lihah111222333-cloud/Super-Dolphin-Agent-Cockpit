package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimesafe"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	codexprotocol "github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp/supportutil"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type session struct {
	agentID              string
	threadID             atomic.Value
	approvalPolicy       atomic.Value
	transport            *transport
	manager              *ServerManager
	caps                 dto.CapabilitySet
	recovery             *recoveryManager
	history              *rolloutReader
	logger               *slog.Logger
	dispatcher           *unified.EventDispatcher
	approvals            *rpc.ApprovalManager
	approvalDecisionHook func(context.Context, rpc.ApprovalRequest) (contract.ApprovalDecision, error)
	ctx                  context.Context
	cancel               context.CancelFunc
	mu                   sync.Mutex
	approvalMu           sync.Mutex
	recoveryMu           sync.Mutex
	lastReadAt           atomic.Int64
	recoveryCount        atomic.Int32
	turns                map[string]*turnHandle
	activeTurnID         string
	pendingTurn          *turnReplayState
	suppressed           map[string]struct{}
	suppressedToolEnds   map[string]struct{}
	suppressedToolOrder  []string
	rolloutToolNames     map[string]string
	rolloutToolOrder     []string
	processedApprovals   map[string]*processedApprovalEntry
	runtimeConfig        map[string]any
	// turnOutputAccumulator buffers per-turn output; key = provider turn UUID.
	turnOutputAccumulator map[string]*turnOutputBuffer
	accumulatorMu         sync.Mutex
	// poolRelease releases a P21 multi-provider Codex pool slot; nil outside pool mode.
	poolRelease            func()
	poolReleaseOnce        sync.Once
	prepareTools           func(context.Context, contract.CodexToolSurfaceScope) ([]codexprotocol.DynamicToolSchema, error)
	listTools              func(context.Context) ([]codexprotocol.DynamicToolSchema, error)
	releaseTools           func(contract.CodexToolSurfaceScope) error
	dynamicToolsEnabled    bool
	toolSurfaceReleaseOnce sync.Once
	toolSurfaceID          atomic.Value
	// runtime owns the session-private reader / health / recovery goroutines.
	runtime *SessionRuntime
}

var _ contract.Session = (*session)(nil)

var codexToolSurfaceSeq atomic.Uint64

type turnHandle struct {
	localID    string
	providerID string
	trace      observability.TraceContext
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
	agentLog := pkglogger.NewAgentLogger(agentID)
	s := &session{
		agentID:               strings.TrimSpace(agentID),
		transport:             t,
		manager:               manager,
		caps:                  cloneCaps(codexCapabilities),
		recovery:              &recoveryManager{transport: t, logger: agentLog, maxRetry: maxRecoveryAttempts},
		history:               &rolloutReader{logger: agentLog, transport: t},
		logger:                agentLog,
		dispatcher:            dispatcher,
		approvals:             approvals,
		ctx:                   ctx,
		cancel:                cancel,
		turns:                 map[string]*turnHandle{},
		suppressed:            map[string]struct{}{},
		suppressedToolEnds:    map[string]struct{}{},
		processedApprovals:    map[string]*processedApprovalEntry{},
		turnOutputAccumulator: map[string]*turnOutputBuffer{},
	}
	s.poolRelease = cfg.poolRelease
	s.noteReadActivity()
	// P1c: newSession only builds the runtime handle. Start() is an explicit
	// production call site inside driver.StartSession / driver.ResumeSession.
	s.runtime = newSessionRuntime(s, agentLog)
	return s, nil
}

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

type sessionOption func(*sessionOptions)

type sessionOptions struct {
	poolURL     string
	poolRelease func()
}

func withPoolServer(url string, release func()) sessionOption {
	return func(o *sessionOptions) {
		o.poolURL = url
		o.poolRelease = release
	}
}

func (s *session) onInboundMessage(ctx context.Context, resp Responder, msg RawMessage) {
	s.noteReadActivity()
	if toolHandler := s.manager.getToolHandler(); len(msg.ID) != 0 && toolHandler != nil && isToolCallMethod(msg.Method) {
		s.handleInboundToolCall(ctx, resp, msg, toolHandler)
		return
	}
	if isKnownRequestMethod(msg.Method) {
		s.onNotification(msg.Method, msg.Params)
		return
	}
	if len(msg.ID) != 0 {
		if respErr := resp.RespondWithID(msg.ID, nil, fmt.Errorf("method not supported: %s", msg.Method)); respErr != nil {
			s.logger.Warn("codexapp: unsupported method respond failed",
				"agent_id", s.agentID, "method", msg.Method, "error", respErr)
		}
		return
	}
	s.onNotification(msg.Method, msg.Params)
}

func (s *session) handleInboundToolCall(ctx context.Context, resp Responder, msg RawMessage, toolHandler ToolHandler) {
	toolName := toolCallParamString(msg.Params, "name")
	sessionCWD := s.runtimeConfigString("cwd")
	if shouldWarnToolCWDTrace(toolName) {
		s.logger.Warn("codexapp: tool call cwd trace",
			"agent_id", s.agentID,
			"thread_id", s.ThreadID(),
			"method", msg.Method,
			"tool", toolName,
			"session_cwd", sessionCWD,
		)
	}
	prepared, err := s.prepareToolCall(msg)
	if err != nil {
		s.respondInvalidToolCall(resp, msg, err)
		return
	}
	s.publishToolCallBegin(prepared)
	s.suppressToolEnd(prepared.header.TurnID, prepared.header.CallID, prepared.header.ToolName)
	toolCtx := s.contextWithTurnTrace(ctx, prepared.header.TurnID)
	runtimesafe.SafeGo(s.ctx, s.logger, "codexapp.session.toolCall", func(_ context.Context) {
		result, callErr := toolHandler(contract.WithToolLifecycleAlreadyPublished(toolCtx), prepared.params)
		s.publishToolCallEnd(prepared, result, callErr)
		if respErr := resp.RespondWithID(msg.ID, result, callErr); respErr != nil {
			s.logger.Warn("codexapp: tool call respond failed",
				"agent_id", s.agentID, "method", msg.Method, "error", respErr)
		}
	})
}

func (s *session) respondInvalidToolCall(resp Responder, msg RawMessage, err error) {
	if respErr := resp.RespondWithID(msg.ID, nil, err); respErr != nil {
		s.logger.Warn("codexapp: invalid tool call respond failed",
			"agent_id", s.agentID, "method", msg.Method, "error", respErr)
	}
}

func isKnownRequestMethod(method string) bool {
	return hasMethod(method, approvalBridgeMethods) || hasMethod(method, requestUserInputMethods)
}
func isToolCallMethod(method string) bool {
	switch strings.TrimSpace(method) {
	case "item/tool/call", "dynamic_tool_call", "tool.call.begin", "tools/call":
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
		supportutil.ConfigString(out, "baseInstructions"),
		supportutil.ConfigString(out, "instructions"),
	)); value != "" {
		out["baseInstructions"] = value
	}
	if value := strings.TrimSpace(shared.FirstNonEmpty(
		supportutil.ConfigString(out, "developerInstructions"),
		supportutil.ConfigString(out, "developer_instructions"),
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
	if err := s.applyTurnToolScopeRuntimeConfig(req); err != nil {
		return nil, err
	}
	params := buildTurnStartParams(threadID, req)
	if params.DynamicTools, err = s.prepareTurnDynamicTools(ctx, req); err != nil {
		return nil, err
	}
	// Fill model/effort from session runtimeConfig if not set by turn request.
	// thread/config/set stores these in runtimeConfig; they take effect on the next turn.
	if params.Model == "" {
		params.Model = s.runtimeConfigString("model")
	}
	if params.Effort == "" {
		params.Effort = normalizeCodexAppEffort(s.runtimeConfigString("effort"))
	}
	if supportutil.CodexModelNeedsListResolution(params.Model) {
		params.Model = s.resolveTurnStartModel(ctx, params.Model)
	}
	pkglogger.Debug("codexapp: turn/start params",
		"agent_id", s.agentID,
		"model", params.Model,
		"effort", params.Effort,
	)
	raw, err := callWithTimeout(ctx, callTargetFunc(s.callTransport), 30*time.Second, "turn/start", params)
	if err != nil {
		return nil, supportutil.WrapCodexModelUnsupportedError(err, params.Model)
	}
	resp, err := decodeTurnStartResult(raw)
	if err != nil {
		return nil, err
	}
	providerID := strings.TrimSpace(resp.Turn.ID)
	h := newTurnHandle(resolveLocalTurnID(req.LocalID, providerID), providerID)
	h.trace, _ = observability.TraceFromContext(ctx)
	s.mu.Lock()
	s.turns[providerID] = h
	s.activeTurnID = providerID
	s.mu.Unlock()
	s.rememberPendingTurn(h, params)
	return h, nil
}

func (s *session) contextWithTurnTrace(ctx context.Context, providerTurnID string) context.Context {
	providerTurnID = strings.TrimSpace(providerTurnID)
	if s == nil || providerTurnID == "" {
		return ctx
	}
	s.mu.Lock()
	h := s.turns[providerTurnID]
	s.mu.Unlock()
	if h == nil || h.trace.TraceID == "" {
		return ctx
	}
	return observability.ContextWithTrace(ctx, h.trace)
}

func (s *session) resolveTurnStartModel(ctx context.Context, requested string) string {
	requested = strings.TrimSpace(requested)
	if s == nil || s.transport == nil {
		return requested
	}
	model, replaced, err := resolveSupportedCodexModel(ctx, s.transport, requested)
	if err != nil {
		pkglogger.Warn("codexapp: turn/start model/list selection failed",
			"agent_id", s.agentID,
			"thread_id", s.ThreadID(),
			"requested_model", requested,
			"error", err,
		)
		return requested
	}
	if model == "" || !replaced {
		return requested
	}
	s.setRuntimeConfigValue("model", model)
	pkglogger.Info("codexapp: turn/start selected supported model from model/list",
		"agent_id", s.agentID,
		"thread_id", s.ThreadID(),
		"requested_model", requested,
		"model", model,
	)
	return model
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
	s.mu.Lock()
	turnID := shared.FirstNonEmpty(req.TurnID, s.activeTurnID)
	s.mu.Unlock()
	if turnID == "" {
		return errors.New("codexapp: active turn id is required for interrupt")
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
	turnID, ok := s.forceCompleteTargetTurnID(req.ProviderID)
	if !ok {
		return nil
	}
	if err := s.callForceComplete(ctx, threadID, turnID); err != nil {
		return err
	}
	s.forceCompleteTurn(turnID)
	return nil
}

func (s *session) callForceComplete(ctx context.Context, threadID, turnID string) error {
	_, err := callWithTimeout(ctx, callTargetFunc(s.callTransport), 10*time.Second, "turn/forceComplete", forceCompleteParams(threadID, turnID, true))
	if err == nil {
		return nil
	}
	if !forceCompleteTurnIDFallbackEligible(err) {
		return err
	}
	if s != nil && s.logger != nil {
		s.logger.Warn("codexapp: turn/forceComplete turnId rejected; retrying legacy payload", "error", err)
	}
	_, fallbackErr := callWithTimeout(ctx, callTargetFunc(s.callTransport), 10*time.Second, "turn/forceComplete", forceCompleteParams(threadID, "", false))
	if fallbackErr != nil {
		return fmt.Errorf("codexapp: legacy turn/forceComplete fallback failed: %w (original turnId error: %v)", fallbackErr, err)
	}
	return nil
}

func forceCompleteParams(threadID, turnID string, includeTurnID bool) map[string]any {
	params := map[string]any{"threadId": threadID}
	if includeTurnID {
		params["turnId"] = turnID
	}
	return params
}

func forceCompleteTurnIDFallbackEligible(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if msg == "" || (!strings.Contains(msg, "turnid") && !strings.Contains(msg, "turn_id") && !strings.Contains(msg, "turn id")) {
		return false
	}
	for _, marker := range []string{
		"extra", "forbid", "unknown", "unexpected", "unrecognized", "unsupported",
		"validation", "invalid", "not permitted", "no such field", "pydantic", "-32602",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
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

// ReadConfig 读取 Codex 会话当前线程配置。
func (s *session) ReadConfig(ctx context.Context, _ string) (dto.ThreadConfig, error) {
	if err := shared.CheckCtx(ctx); err != nil {
		return dto.ThreadConfig{}, err
	}
	threadID := s.ThreadID()
	if threadID == "" {
		return dto.ThreadConfig{}, errors.New("codexapp: thread id is required")
	}
	runtimeConfig := s.RuntimeConfigSnapshot()
	values := dto.ThreadConfigValues{
		Model:     supportutil.ConfigString(runtimeConfig, "model"),
		Effort:    supportutil.ConfigString(runtimeConfig, "effort"),
		Approvals: supportutil.ConfigString(runtimeConfig, "approvals", "approvalPolicy", "approval_policy"),
	}
	if values.Approvals == "" {
		values.Approvals = supportutil.SanitizeConfigStringArtifact(s.approvalPolicyValue())
	}
	return dto.ThreadConfig{
		ThreadID:               threadID,
		Provider:               "codex",
		SupportsThreadOverride: true,
		Override:               values,
		Effective:              values,
	}, nil
}

// Close 关闭 Codex app 会话并执行优雅清理。
func (s *session) Close(context.Context) error { return s.shutdownSession(true) }

// ForceStop 强制停止 Codex app 会话。
func (s *session) ForceStop() error { return s.shutdownSession(false) }

// SessionRuntime 返回会话运行时状态。
func (s *session) SessionRuntime() *SessionRuntime { return s.runtime }

func (s *session) shutdownSessionCleanup() error {
	if s == nil {
		return nil
	}
	var err error
	s.toolSurfaceReleaseOnce.Do(func() {
		if s.releaseTools != nil {
			err = s.releaseTools(s.codexToolSurfaceReleaseScope())
		}
	})
	s.poolReleaseOnce.Do(func() {
		if s.poolRelease != nil {
			s.poolRelease()
		}
	})
	return err
}

func (s *session) codexToolSurfaceReleaseScope() contract.CodexToolSurfaceScope {
	if s == nil {
		return contract.CodexToolSurfaceScope{}
	}
	if surfaceID := s.currentToolSurfaceID(); surfaceID != "" {
		return contract.CodexToolSurfaceScope{SurfaceID: surfaceID}
	}
	if providerThreadID := strings.TrimSpace(s.ThreadID()); providerThreadID != "" {
		return contract.CodexToolSurfaceScope{ProviderThreadID: providerThreadID}
	}
	return contract.CodexToolSurfaceScope{
		AgentID: strings.TrimSpace(s.agentID),
	}
}

func (s *session) ensureToolSurfaceID() string {
	if s == nil {
		return ""
	}
	if existing := s.currentToolSurfaceID(); existing != "" {
		return existing
	}
	id := "codex-surface:" + strings.TrimSpace(s.agentID) + ":" + strconv.FormatUint(codexToolSurfaceSeq.Add(1), 10)
	s.toolSurfaceID.Store(id)
	return id
}

func (s *session) currentToolSurfaceID() string {
	if s == nil {
		return ""
	}
	value, _ := s.toolSurfaceID.Load().(string)
	return strings.TrimSpace(value)
}

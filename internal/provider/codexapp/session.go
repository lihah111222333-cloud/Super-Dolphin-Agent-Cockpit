package codexapp

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	contract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	codexprotocol "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp/supportutil"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/unified"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

type session struct {
	agentID                string
	threadID               atomic.Value
	approvalPolicy         atomic.Value
	approvalPolicyVerified atomic.Bool
	approvalSessionScope   string
	transport              *transport
	manager                *ServerManager
	caps                   dto.CapabilitySet
	recovery               *recoveryManager
	history                *rolloutReader
	logger                 *slog.Logger
	dispatcher             *unified.EventDispatcher
	approvals              *rpc.ApprovalManager
	approvalDecisionHook   func(context.Context, rpc.ApprovalRequest) (contract.ApprovalDecision, error)
	ctx                    context.Context
	cancel                 context.CancelFunc
	mu                     sync.Mutex
	approvalMu             sync.Mutex
	recoveryMu             sync.Mutex
	lastReadAt             atomic.Int64
	recoveryCount          atomic.Int32
	turns                  map[string]*turnHandle
	activeTurnID           string
	activeTurnGeneration   uint64
	interruptRequests      map[string]string
	pendingTurn            *turnReplayState
	suppressed             map[string]struct{}
	suppressedToolEnds     map[string]struct{}
	suppressedToolOrder    []string
	rolloutToolNames       map[string]string
	rolloutToolOrder       []string
	processedApprovals     map[string]*processedApprovalEntry
	runtimeConfig          map[string]any
	// turnOutputAccumulator 按 provider turn UUID 暂存流式输出，供完成事件合并后再分发。
	turnOutputAccumulator map[string]*turnOutputBuffer
	accumulatorMu         sync.Mutex
	// poolRelease 释放当前 session 持有的池槽位；非池模式为空，Close 路径必须只调用一次。
	poolRelease            func()
	poolReleaseOnce        sync.Once
	prepareTools           func(context.Context, contract.CodexToolSurfaceScope) ([]codexprotocol.DynamicToolSchema, error)
	listTools              func(context.Context) ([]codexprotocol.DynamicToolSchema, error)
	releaseTools           func(contract.CodexToolSurfaceScope) error
	dynamicToolsEnabled    bool
	toolSurfaceReleaseOnce sync.Once
	toolSurfaceID          atomic.Value
	// runtime 持有当前会话私有的读取、健康检查和恢复 goroutine。
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

// ErrForceCompleteTargetNotFound marks a force-complete request with no active provider turn target.
var ErrForceCompleteTargetNotFound = forceCompleteTargetNotFoundError{}

// forceCompleteTargetNotFoundError is the typed no-target marker shared across package boundaries by behavior.
type forceCompleteTargetNotFoundError struct{}

// Error 返回 force-complete 无目标失败的稳定诊断文本，供日志和 RPC envelope 复用。
func (forceCompleteTargetNotFoundError) Error() string {
	return "force complete target not found"
}

// ForceCompleteTargetNotFound 暴露跨包识别标记，避免 turn 模块反向导入 provider 包。
func (forceCompleteTargetNotFoundError) ForceCompleteTargetNotFound() bool {
	return true
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

// newSessionWithOptions 创建 Codex app 会话并完成 transport、历史读取器和运行时句柄组装。
// 如果 transport 启动失败，会立即释放已占用的池槽位，避免 ServerPool 的引用计数滞留。
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
	cfg := sessionOptions{approvalScopeReader: rand.Reader}
	for _, opt := range opts {
		opt(&cfg)
	}
	if err := requireApprovalManager(approvals); err != nil {
		releaseSessionPoolSlot(cfg)
		return nil, err
	}
	approvalSessionScope, err := generateApprovalSessionScope(cfg.approvalScopeReader)
	if err != nil {
		releaseSessionPoolSlot(cfg)
		return nil, fmt.Errorf("codexapp: generate approval session scope: %w", err)
	}
	url, err := resolveSessionTransportURL(transportCtx, serverURL, manager, cfg)
	if err != nil {
		return nil, err
	}
	t, err := newTransport(transportCtx, url)
	if err != nil {
		// transport 启动失败时也要释放预占的池槽位，确保下一次 Acquire 看到准确引用计数。
		releaseSessionPoolSlot(cfg)
		return nil, err
	}
	// session ctx 从 transportCtx 派生，调用方取消 transport 上下文时会级联触发会话关闭。
	ctx, cancel := context.WithCancel(transportCtx)
	agentLog, err := pkglogger.NewAgentLogger(agentID)
	if err != nil {
		cancel()
		closeErr := t.Close()
		releaseSessionPoolSlot(cfg)
		return nil, errors.Join(fmt.Errorf("codexapp: create agent logger: %w", err), closeErr)
	}
	s := &session{
		agentID:               strings.TrimSpace(agentID),
		approvalSessionScope:  approvalSessionScope,
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
	// newSession 只组装运行时句柄；真正启动由 driver.StartSession/ResumeSession 显式触发。
	s.runtime = newSessionRuntime(s, agentLog)
	return s, nil
}

// resolveSessionTransportURL 选择会话要连接的 Codex app-server 地址。
// 池模式优先使用已分配 URL；非池模式在缺少显式地址时要求 ServerManager 启动本地服务。
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
	poolURL             string
	poolRelease         func()
	approvalScopeReader io.Reader
}

func withPoolServer(url string, release func()) sessionOption {
	return func(o *sessionOptions) {
		o.poolURL = url
		o.poolRelease = release
	}
}

// withApprovalScopeReader 注入审批会话 scope 的随机源，供构造失败路径做确定性验证。
func withApprovalScopeReader(reader io.Reader) sessionOption {
	return func(o *sessionOptions) {
		o.approvalScopeReader = reader
	}
}

// generateApprovalSessionScope 从不可预测随机源生成 RFC 4122 version 4 会话 scope。
func generateApprovalSessionScope(reader io.Reader) (string, error) {
	if reader == nil {
		return "", errors.New("approval session scope entropy reader is nil")
	}
	var value [16]byte
	if _, err := io.ReadFull(reader, value[:]); err != nil {
		return "", fmt.Errorf("read approval session scope entropy: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}

// onInboundMessage 分发 Codex app 主动发来的请求或通知。
// 带 ID 的工具调用会进入异步工具桥，未知请求必须明确返回错误，避免 provider 侧等待悬空。
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

// handleInboundToolCall 解析远端工具调用并通过本地 tool bridge 返回响应。
func (s *session) handleInboundToolCall(ctx context.Context, resp Responder, msg RawMessage, toolHandler ToolHandler) {
	toolName := toolCallParamString(msg.Params, "name")
	sessionCWD := s.runtimeConfigString("cwd")
	if shouldWarnToolCWDTrace(toolName) {
		fields := []any{
			"agent_id", s.agentID,
			"thread_id", s.ThreadID(),
			"method", msg.Method,
			"tool", toolName,
		}
		fields = append(fields, shared.SafePathLogFields("session_cwd", sessionCWD)...)
		s.logger.Warn("codexapp: tool call cwd trace", fields...)
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

// Capabilities 返回会话能力集副本，避免调用方修改共享模板。
func (s *session) Capabilities() dto.CapabilitySet { return cloneCaps(s.caps) }

func (s *session) setRuntimeConfig(cfg map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtimeConfig = shared.CloneRuntimeConfigMap(cfg)
}

// RuntimeConfigSnapshot 返回线程配置快照并统一历史字段别名。
// 返回值始终是克隆后的 map，调用方可以安全补字段而不会改写会话内状态。
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

// StartTurn 启动 provider turn，并记录本地 turn handle、trace 和可重放状态。
// 动态工具和模型解析必须在远端调用前完成，失败时不会登记 active turn。
func (s *session) StartTurn(ctx context.Context, req dto.TurnRequest) (contract.TurnHandle, error) {
	threadID, err := requireThreadID(s, req.ThreadID)
	if err != nil {
		return nil, err
	}
	params, err := buildTurnStartParams(threadID, req)
	if err != nil {
		return nil, err
	}
	if err := s.applyTurnToolScopeRuntimeConfig(req); err != nil {
		return nil, err
	}
	if params.DynamicTools, err = s.prepareTurnDynamicTools(ctx, req); err != nil {
		return nil, err
	}
	if err := s.applyRuntimeTurnStartOverrides(ctx, &params); err != nil {
		return nil, err
	}
	pkglogger.Debug("codexapp: turn/start params",
		"agent_id", s.agentID,
		"model", params.Model,
		"effort", params.Effort,
		"sandbox_policy_shape", sandboxPolicyLogShape(params.SandboxPolicy),
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
	s.setActiveTurnLocked(providerID)
	s.mu.Unlock()
	s.rememberPendingTurn(h, params)
	return h, nil
}

// applyRuntimeTurnStartOverrides 从会话 runtimeConfig 补齐 turn/start 支持的运行时覆盖。
// thread/config/set 或启动配置写入的值会在下一轮 turn/start 生效，sandboxPolicy 编码失败时直接阻断。
func (s *session) applyRuntimeTurnStartOverrides(ctx context.Context, params *turnStartParams) error {
	if params == nil {
		return nil
	}
	modelSource := supportutil.CodexModelResolutionSourceExplicit
	if params.Model == "" {
		modelSource = supportutil.CodexModelResolutionSourceDefault
		params.Model = s.runtimeConfigString("model")
	}
	if params.Effort == "" {
		params.Effort = normalizeCodexAppEffort(s.runtimeConfigString("effort"))
	}
	if len(params.SandboxPolicy) == 0 {
		sandboxPolicy, err := s.runtimeConfigJSON("sandboxPolicy")
		if err != nil {
			return err
		}
		params.SandboxPolicy = sandboxPolicy
	}
	if supportutil.CodexModelNeedsListResolutionForSource(params.Model, modelSource) {
		model, err := s.resolveTurnStartModel(ctx, params.Model)
		if err != nil {
			return err
		}
		params.Model = model
	}
	return nil
}

func sandboxPolicyLogShape(raw json.RawMessage) string {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return "empty"
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err == nil {
		if policyType, _ := obj["type"].(string); strings.TrimSpace(policyType) != "" {
			return "object:" + strings.TrimSpace(policyType)
		}
		return "object"
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return "string"
	}
	return "invalid"
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

// resolveTurnStartModel 在 model/list 可用时把默认模型解析成 provider 当前支持的具体模型。
// 默认模型必须解析成功；model/list 失败或为空会直接阻断 turn/start。
func (s *session) resolveTurnStartModel(ctx context.Context, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if s == nil || s.transport == nil {
		return "", supportutil.NewModelResolutionRequiredError(requested, errors.New("model/list transport is not configured"))
	}
	resolution, err := resolveSupportedCodexModel(ctx, s.transport, requested)
	if err != nil {
		return "", err
	}
	if resolution.model == "" {
		return "", supportutil.NewModelResolutionRequiredError(requested, nil)
	}
	if resolution.replaced {
		s.setRuntimeConfigValue("model", resolution.model)
		pkglogger.Info("codexapp: turn/start selected supported model from model/list",
			"agent_id", s.agentID,
			"thread_id", s.ThreadID(),
			"requested_model", requested,
			"model", resolution.model,
		)
	}
	return resolution.model, nil
}

// Steer 向当前线程的指定 turn 发送 steering 输入。
// ExpectedTurnID 是调用方的并发保护，缺失时立即失败，避免把指令投递到未知 turn。
func (s *session) Steer(ctx context.Context, req dto.SteerRequest) error {
	threadID, err := requireThreadID(s, req.ThreadID)
	if err != nil {
		return err
	}
	expectedTurnID := strings.TrimSpace(req.ExpectedTurnID)
	if expectedTurnID == "" {
		return errors.New("codexapp: expected turn id is required")
	}
	params, err := buildTurnSteerParams(threadID, req)
	if err != nil {
		return err
	}
	_, err = callWithTimeout(ctx, callTargetFunc(s.callTransport), 30*time.Second, "turn/steer", params)
	return err
}

// Interrupt 中断指定 turn；请求未带 turnID 时使用会话记录的 active turn。
// 如果两者都为空会立即报错，防止向 provider 发送无目标的 interrupt。
func (s *session) Interrupt(ctx context.Context, req dto.InterruptRequest) error {
	threadID, err := requireThreadID(s, req.ThreadID)
	if err != nil {
		return err
	}
	requestedTurnID := strings.TrimSpace(req.TurnID)
	claim, err := s.claimInterruptTarget(requestedTurnID)
	if err != nil {
		return err
	}
	turnID := claim.turnID
	if turnID == "" {
		return errors.New("codexapp: active turn id is required for interrupt")
	}
	params := buildTurnInterruptParams(threadID, turnID, req.Source)
	target := callTargetFunc(func(callCtx context.Context, method string, callParams any) (json.RawMessage, error) {
		return s.callTransportWithGuard(callCtx, method, callParams, func() error {
			return s.validateInterruptClaim(claim)
		})
	})
	_, err = callWithTimeout(ctx, target, 10*time.Second, "turn/interrupt", params)
	if err == nil {
		s.recordAcceptedInterruptRequest(claim, req.RequestID)
	}
	return err
}

type interruptTargetClaim struct {
	turnID     string
	generation uint64
}

func (s *session) claimInterruptTarget(requestedTurnID string) (interruptTargetClaim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	activeTurnID := strings.TrimSpace(s.activeTurnID)
	if requestedTurnID != "" && requestedTurnID != activeTurnID {
		return interruptTargetClaim{}, contract.ErrInterruptTargetChanged
	}
	turnID := shared.FirstNonEmpty(requestedTurnID, activeTurnID)
	return interruptTargetClaim{turnID: turnID, generation: s.activeTurnGeneration}, nil
}

func (s *session) validateInterruptClaim(claim interruptTargetClaim) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if claim.turnID == "" || claim.turnID != strings.TrimSpace(s.activeTurnID) || claim.generation != s.activeTurnGeneration {
		return contract.ErrInterruptTargetChanged
	}
	return nil
}

func (s *session) recordAcceptedInterruptRequest(claim interruptTargetClaim, requestID string) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if claim.turnID != strings.TrimSpace(s.activeTurnID) || claim.generation != s.activeTurnGeneration {
		return
	}
	if s.interruptRequests == nil {
		s.interruptRequests = map[string]string{}
	}
	s.interruptRequests[claim.turnID] = requestID

}

func (s *session) setActiveTurnLocked(turnID string) {
	turnID = strings.TrimSpace(turnID)
	if s.activeTurnID == turnID {
		return
	}
	s.activeTurnID = turnID
	s.activeTurnGeneration++
}

// ForceComplete 强制完成当前或指定 provider turn，并在远端确认后关闭本地 turn handle。
// 找不到目标 turn 时返回 typed 错误，避免 UI 把未执行的强制完成显示为成功。
func (s *session) ForceComplete(ctx context.Context, req dto.ForceCompleteRequest) error {
	threadID, err := requireThreadID(s, req.ThreadID)
	if err != nil {
		return err
	}
	turnID, ok := s.forceCompleteTargetTurnID(req.ProviderID)
	if !ok {
		return ErrForceCompleteTargetNotFound
	}
	if err := s.callForceComplete(ctx, threadID, turnID); err != nil {
		return err
	}
	s.forceCompleteTurn(turnID)
	return nil
}

// callForceComplete 调用 provider 的 turn/forceComplete，并兼容旧版不接受 turnId 的 payload。
// 只有明确属于 turnId 字段校验失败时才回退，其他远端错误必须原样返回。
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

// forceCompleteTurnIDFallbackEligible 判断错误是否适合走旧版 forceComplete payload。
// 只匹配远端 schema/字段拒绝类错误，避免隐藏真实的执行失败或网络异常。
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

// ListThreads 从 provider 读取线程列表，并兼容新旧两种返回形态。
// 结构化 ThreadRef 优先；旧版只返回字符串 ID 时会转换为最小 ThreadRef。
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

// ForkThread 请求 provider 复制当前线程，并只把新线程 ID 暴露给上层契约。
// requireThreadID 在远端调用前阻断空线程，避免创建来源不明的 fork。
func (s *session) ForkThread(ctx context.Context, req dto.ForkRequest) (dto.ForkResult, error) {
	threadID, err := requireThreadID(s, req.ThreadID)
	if err != nil {
		return dto.ForkResult{}, err
	}
	raw, err := callWithTimeout(ctx, callTargetFunc(s.callTransport), 10*time.Second, "thread/fork", map[string]any{"threadId": threadID})
	if err != nil {
		return dto.ForkResult{}, err
	}
	id, err := decodeThreadID(raw)
	if err != nil {
		return dto.ForkResult{}, err
	}
	return dto.ForkResult{NewThreadID: id}, nil
}

// Configure 将线程配置 patch 委托给 Codex 专用配置流程。
// 该入口保留 contract.Session 形状，实际校验和持久化边界由 configureThread 负责。
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

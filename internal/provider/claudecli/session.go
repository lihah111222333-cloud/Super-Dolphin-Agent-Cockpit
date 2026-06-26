package claudecli

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/pidregistry"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
)

type session struct {
	agentID              string
	threadID             string
	publicThreadID       string
	sessionID            string
	threadReady          chan struct{}
	transport            *transport
	caps                 dto.CapabilitySet
	history              *historyBackend
	logger               *slog.Logger
	eventDispatcher      *unified.EventDispatcher
	binaryPath           string
	cwd                  string
	launchCLI            func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error)
	model                string
	transportModel       string
	transportConfig      cliLaunchConfig
	transportManifest    dto.MCPManifest
	overrideModel        string
	overrideEffort       string
	overrideModelSet     bool
	overrideEffortSet    bool
	pendingModel         *string
	pendingEffort        *string
	configDirty          bool
	instructions         string
	config               cliLaunchConfig
	rawConfig            map[string]any
	manifest             dto.MCPManifest
	cleanup              func()
	pidRegistry          *pidregistry.Registry
	restartCancel        context.CancelFunc
	restartGeneration    uint64
	logWatcher           *sessionLogWatcher
	logWatcherGen        uint64
	sessionContextWindow int
	recovery             contract.SessionRecoveryReporter
	tracer               *observability.Service
	mu                   sync.Mutex

	activeTurn      *turnHandle
	pendingRetry    *turnRetryState
	activeToolCalls map[string]string
	suppressedTurns map[string]struct{}
	imageTracker    *imageHashTracker
	settleTransport func(*transport) error
}

type turnHandle struct {
	localID    string
	providerID string
	done       chan struct{}
	once       sync.Once
	mu         sync.Mutex
	err        error
}

func newTurnHandle(localID, providerID string) *turnHandle {
	return &turnHandle{
		localID:    strings.TrimSpace(localID),
		providerID: strings.TrimSpace(providerID),
		done:       make(chan struct{}),
	}
}

// LocalID 返回宿主侧创建 turn 时使用的本地 ID。
func (h *turnHandle) LocalID() string { return h.localID }

// ProviderID 返回 Claude CLI 回报或本地回填的 provider turn ID。
func (h *turnHandle) ProviderID() string { return h.providerID }

// Done 返回 turn 完成信号，调用方只读不能关闭。
func (h *turnHandle) Done() <-chan struct{} { return h.done }

// Err 返回 turn 完成时记录的错误。
func (h *turnHandle) Err() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.err
}

func (h *turnHandle) finish(err error) {
	h.once.Do(func() {
		h.mu.Lock()
		h.err = err
		h.mu.Unlock()
		close(h.done)
	})
}

// ThreadID 返回当前已解析的 Claude thread ID。
// 读取时持锁，避免 system:init 更新线程身份时出现半状态。
func (s *session) ThreadID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.threadID
}

// RolloutPath 返回当前 Claude JSONL 历史文件路径。
// 会话尚未解析 thread 或历史文件未落盘时返回空字符串，供 UI 隐藏 rollout 入口。
func (s *session) RolloutPath() string {
	if s == nil || s.history == nil {
		return ""
	}
	threadID := s.ThreadID()
	if threadID == "" {
		return ""
	}
	path, err := s.history.sessionPath(threadID)
	if err != nil {
		return ""
	}
	return path
}

// EventThreadID 返回事件总线使用的线程 ID。
// 在 provider UUID 出现前可能回退到 agentID，以保持前端会话卡片稳定。
func (s *session) EventThreadID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.eventThreadIDLocked()
}

// Capabilities 返回 Claude provider 能力声明的副本。
// 调用方拿到的是拷贝，不能反向修改 session 内部能力表。
func (s *session) Capabilities() dto.CapabilitySet {
	return copyCapabilities(s.caps)
}

// RuntimeConfigSnapshot 汇总 Claude 会话当前可展示的运行时配置。
// 它合并启动配置、transport 生效配置和 prompt snapshot，空快照返回 nil。
func (s *session) RuntimeConfigSnapshot() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := runtimeConfigMap(s.rawConfig)
	s.applyRuntimeConfigSnapshotLocked(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

func runtimeConfigMap(raw map[string]any) map[string]any {
	out := cloneConfigMap(raw)
	if len(out) != 0 {
		return out
	}
	return map[string]any{}
}

func (s *session) applyRuntimeConfigSnapshotLocked(out map[string]any) {
	snapshot := s.runtimePromptSnapshotLocked()
	putRuntimeConfigString(out, "model", s.currentTransportModelLocked())
	putRuntimeConfigString(out, "baseInstructions", promptSnapshotBaseInstructions(snapshot, s.instructions))
	putRuntimeConfigString(out, "approvalPolicy", s.config.ApprovalPolicy)
	putRuntimeConfigString(out, "developerInstructions", promptDeveloperInstructions(cliLaunchConfig{
		DeveloperInstructions: s.config.DeveloperInstructions,
		PromptSnapshot:        snapshot,
	}))
	putRuntimeConfigString(out, "personality", s.config.Personality)
	putRuntimeConfigStringIfMissing(out, "sandbox", s.config.Sandbox)
	putRuntimeConfigString(out, "claudeHome", s.config.ClaudeHome)
	putRuntimeConfigString(out, "claude_home", s.config.ClaudeHome)
	putRuntimeConfigString(out, "history_dir", s.config.ClaudeHome)
}

func (s *session) runtimePromptSnapshotLocked() contract.PromptAssemblySnapshot {
	snapshot := s.transportConfig.PromptSnapshot
	if promptSnapshotBlank(snapshot) {
		return s.config.PromptSnapshot
	}
	return snapshot
}

func putRuntimeConfigString(out map[string]any, key, value string) {
	if value = strings.TrimSpace(value); value != "" {
		out[key] = value
	}
}

func putRuntimeConfigStringIfMissing(out map[string]any, key, value string) {
	if _, ok := out[key]; ok {
		return
	}
	putRuntimeConfigString(out, key, value)
}

// StartTurn 构建 payload、绑定 active turn 并写入 Claude CLI stdin。
// 发送失败会立即回滚 active turn 并完成 handle，避免调用方永久等待。
func (s *session) StartTurn(ctx context.Context, req dto.TurnRequest) (out contract.TurnHandle, err error) {
	traceStarted := time.Now()
	var providerID string
	defer func() {
		s.recordProviderTrace(ctx, claudeTurnRunEvent(req, providerID, time.Since(traceStarted), err))
	}()
	if err := shared.CheckCtx(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	payload, turnID, handle, err := s.prepareTurnLocked(ctx, req)
	providerID = turnID
	out = handle
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if err := s.transport.Send(payload); err != nil {
		s.takeActiveTurnLocked()
		s.mu.Unlock()
		s.finishTurnWithError(handle, err)
		return nil, err
	}
	s.pendingRetry = &turnRetryState{payload: payload}
	started := s.turnRawEventLocked("turn:started", turnID, nil)

	var textBuf strings.Builder
	for _, in := range req.Inputs {
		if in.Content != "" {
			textBuf.WriteString(in.Content)
			textBuf.WriteString("\n")
		}
	}
	userText := strings.TrimSpace(textBuf.String())

	inputReceived := s.turnRawEventLocked("turn:input_received", turnID, map[string]any{
		"input_type": "message",
		"source":     "user",
		"text":       userText,
	})
	s.mu.Unlock()
	s.dispatch(started)
	s.dispatch(inputReceived)
	return handle, nil
}

// Steer 向当前打开的 Claude turn 追加 steer 输入。
// expectedTurnID 不匹配会阻断写入，避免 tool yield 或用户输入落到错误 turn。
func (s *session) Steer(ctx context.Context, req dto.SteerRequest) error {
	if err := shared.CheckCtx(ctx); err != nil {
		return err
	}
	payload, err := buildSteerPayload(req, s.imageTracker)
	if err != nil {
		return err
	}
	turnID, err := s.sendSteer(payload, req.ExpectedTurnID)
	if err != nil {
		return err
	}
	s.dispatch(s.turnRawEvent("turn:input_received", turnID, map[string]any{
		"input_type": "message",
		"source":     "tool_yield",
	}))
	return nil
}

// Interrupt 中断当前 Claude turn 或正在进行的 transport restart。
// 它会先摘除 active 状态和 log watcher，再清理旧 transport，防止旧事件继续进入 UI。
func (s *session) Interrupt(ctx context.Context, req dto.InterruptRequest) error {
	if err := shared.CheckCtx(ctx); err != nil {
		return err
	}
	reason := strings.TrimSpace(req.Source)
	s.mu.Lock()
	handle := s.takeActiveTurnLocked()
	restartCancel := s.restartCancel
	if handle == nil && restartCancel == nil {
		s.mu.Unlock()
		return nil
	}
	turnID := currentTurnID(handle)
	if turnID != "" {
		if s.suppressedTurns == nil {
			s.suppressedTurns = map[string]struct{}{}
		}
		s.suppressedTurns[turnID] = struct{}{}
	}
	tr := s.transport
	cleanup := s.cleanup
	reg := s.pidRegistry
	watcher := s.detachLogWatcherLocked()
	toolEvents := s.takeActiveToolInterruptEventsLocked(turnID, reason)
	s.restartCancel = nil
	s.transport = nil
	s.transportConfig = cliLaunchConfig{}
	s.transportManifest = dto.MCPManifest{}
	s.cleanup = nil
	s.sessionContextWindow = 0
	s.activeToolCalls = nil
	s.mu.Unlock()
	if restartCancel != nil {
		restartCancel()
	}
	if watcher != nil {
		watcher.stopAndWait()
	}
	cleanupInterruptedTransport(s.logger, reg, tr, cleanup, s.resolveSettleTransport())
	if handle == nil {
		return nil
	}
	for _, event := range toolEvents {
		s.dispatch(event)
	}
	handle.finish(context.Canceled)
	s.dispatch(s.turnRawEvent("turn:interrupted", turnID, map[string]any{
		"reason": reason,
	}))
	return nil
}

func (s *session) resolveSettleTransport() func(*transport) error {
	if s.settleTransport != nil {
		return s.settleTransport
	}
	return defaultSettleInterruptedTransport
}

// ListThreads 返回 Claude provider 不支持线程列表能力的明确错误。
func (s *session) ListThreads(context.Context) ([]dto.ThreadRef, error) {
	return nil, contract.NewCapabilityError(dto.CapThreadList, "claude")
}

// ForkThread 返回 Claude provider 不支持 fork 能力的明确错误。
func (s *session) ForkThread(context.Context, dto.ForkRequest) (dto.ForkResult, error) {
	return dto.ForkResult{}, contract.NewCapabilityError(dto.CapThreadFork, "claude")
}

// Close 按优雅路径关闭 Claude session。
func (s *session) Close(context.Context) error {
	return s.stop(false)
}

// ForceStop 按强制路径停止 Claude session。
func (s *session) ForceStop() error {
	return s.stop(true)
}

// stop 停止 Claude session 并释放 transport、watcher 和 PID 注册。
// force=true 时使用强杀路径；无论是否有 transport，都要向事件总线发布停止状态。
func (s *session) stop(force bool) error {
	s.mu.Lock()
	tr := s.transport
	cleanup := s.cleanup
	handle := s.takeActiveTurnLocked()
	reg := s.pidRegistry
	watcher := s.detachLogWatcherLocked()
	s.transport = nil
	s.transportConfig = cliLaunchConfig{}
	s.transportManifest = dto.MCPManifest{}
	s.cleanup = nil
	s.sessionContextWindow = 0
	s.activeToolCalls = nil
	s.mu.Unlock()

	if watcher != nil {
		watcher.stopAndWait()
	}
	unregisterTransportPID(reg, tr)
	if handle != nil {
		handle.finish(errors.New("claudecli: session stopped"))
	}
	var err error
	if tr != nil {
		err = stopTransport(tr, force)
	}
	if cleanup != nil {
		cleanup()
	}
	s.dispatch(s.buildStopEvent(tr, force))
	return err
}

// buildStopEvent 构造 session 停止或失败事件。
// 强制停止时附带 stderr 尾部，便于 UI 展示 provider 退出原因。
func (s *session) buildStopEvent(tr *transport, force bool) dto.RawProviderEvent {
	eventType := "agent:stopped"
	data := map[string]any{
		"agent_id":   s.agentID,
		"thread_id":  s.EventThreadID(),
		"session_id": s.sessionID,
		"timestamp":  time.Now().Format(time.RFC3339Nano),
	}
	if force {
		eventType = "agent:failed"
		data["error"] = "session stopped"
		if tr != nil {
			if stderr := tr.stderr.String(); stderr != "" {
				data["stderr"] = stderr
			}
		}
	}
	return dto.RawProviderEvent{EventType: eventType, Data: data}
}

func canonicalizeClaudeLaunchConfig(model string, cfg cliLaunchConfig) cliLaunchConfig {
	cfg.Effort = normalizeEffort(model, cfg.Effort)
	return cfg
}

func readyChannelClosed(ch chan struct{}) bool {
	if ch == nil {
		return false
	}
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func manifestChanged(next, current dto.MCPManifest) bool {
	if reflect.DeepEqual(next, dto.MCPManifest{}) {
		return false
	}
	if reflect.DeepEqual(next, current) {
		return false
	}
	// 当前 session 若由 orchestrator proxy 管理，proxy 会跨 turn 动态映射工具。
	// 新请求携带的 manifest 没有 proxy 语义，不能因此把会话降级成静态命令并重启 CLI。
	return !isProxyManifest(current)
}

// isProxyManifest 判断 MCP manifest 是否来自 orchestrator proxy。
// 只有带 agent 维度的 /mcp/... URL 才算 proxy manifest，普通 peer /mcp 仍按静态配置处理。
func isProxyManifest(m dto.MCPManifest) bool {
	if len(m.Binaries) == 0 {
		return false
	}
	for _, bin := range m.Binaries {
		if bin.Type == "http" && strings.Contains(bin.URL, "/mcp/") {
			// proxy URL 会追加 agent ID；普通 peer URL 停在 /mcp，不能混淆。
			parts := strings.Split(strings.TrimRight(bin.URL, "/"), "/")
			if len(parts) >= 6 {
				return true
			}
		}
	}
	return false
}

var _ contract.Session = (*session)(nil)

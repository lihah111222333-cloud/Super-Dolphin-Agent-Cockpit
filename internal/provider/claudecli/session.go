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

func (h *turnHandle) LocalID() string { return h.localID }

func (h *turnHandle) ProviderID() string { return h.providerID }

func (h *turnHandle) Done() <-chan struct{} { return h.done }

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

func (s *session) ThreadID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.threadID
}

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

func (s *session) EventThreadID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.eventThreadIDLocked()
}

func (s *session) Capabilities() dto.CapabilitySet {
	return copyCapabilities(s.caps)
}

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
func (s *session) ListThreads(context.Context) ([]dto.ThreadRef, error) {
	return nil, contract.NewCapabilityError(dto.CapThreadList, "claude")
}

func (s *session) ForkThread(context.Context, dto.ForkRequest) (dto.ForkResult, error) {
	return dto.ForkResult{}, contract.NewCapabilityError(dto.CapThreadFork, "claude")
}

func (s *session) Close(context.Context) error {
	return s.stop(false)
}

func (s *session) ForceStop() error {
	return s.stop(true)
}

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
	// If the current session is managed by the orchestrator proxy, the proxy dynamically
	// maps tools across turns. The incoming turn-request manifest (which lacks proxy awareness)
	// would downgrade us to static commands or peer http ports, needlessly restarting the CLI.
	return !isProxyManifest(current)
}

func isProxyManifest(m dto.MCPManifest) bool {
	if len(m.Binaries) == 0 {
		return false
	}
	for _, bin := range m.Binaries {
		if bin.Type == "http" && strings.Contains(bin.URL, "/mcp/") {
			// Proxy URLs append the agent ID, e.g. http://host:port/mcp/family/agent-123 (len >= 6).
			// Peer URLs end at /mcp: http://host:port/mcp (len = 4)
			parts := strings.Split(strings.TrimRight(bin.URL, "/"), "/")
			if len(parts) >= 6 {
				return true
			}
		}
	}
	return false
}

var _ contract.Session = (*session)(nil)

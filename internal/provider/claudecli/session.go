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
	"github.com/anthropic-ai/super-agent-v3/internal/platform/pidregistry"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
)

type session struct {
	agentID         string
	threadID        string
	publicThreadID  string
	sessionID       string
	threadReady     chan struct{}
	threadReadyOnce sync.Once
	transport       *transport
	caps            dto.CapabilitySet
	history         *historyBackend
	logger          *slog.Logger
	eventDispatcher *unified.EventDispatcher
	binaryPath      string
	cwd             string
	model           string
	instructions    string
	config          cliLaunchConfig
	rawConfig       map[string]any
	manifest        dto.MCPManifest
	cleanup         func()
	pidRegistry     *pidregistry.Registry
	mu              sync.Mutex
	activeTurn      *turnHandle
	suppressedTurns map[string]struct{}
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

func (s *session) RolloutPath() string { return "" }

// pid returns the PID of the claude CLI process, or 0 if unavailable.
func (s *session) pid() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.transport == nil || s.transport.cmd == nil || s.transport.cmd.Process == nil {
		return 0
	}
	return s.transport.cmd.Process.Pid
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
	out := cloneConfigMap(s.rawConfig)
	if len(out) == 0 {
		out = map[string]any{}
	}
	if value := strings.TrimSpace(s.model); value != "" {
		out["model"] = value
	}
	if value := strings.TrimSpace(s.instructions); value != "" {
		out["baseInstructions"] = value
	}
	if value := strings.TrimSpace(s.config.ApprovalPolicy); value != "" {
		out["approvalPolicy"] = value
	}
	if value := strings.TrimSpace(s.config.DeveloperInstructions); value != "" {
		out["developerInstructions"] = value
	}
	if value := strings.TrimSpace(s.config.Personality); value != "" {
		out["personality"] = value
	}
	if _, ok := out["sandbox"]; !ok {
		if value := strings.TrimSpace(s.config.Sandbox); value != "" {
			out["sandbox"] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *session) StartTurn(ctx context.Context, req dto.TurnRequest) (contract.TurnHandle, error) {
	if err := shared.CheckCtx(ctx); err != nil {
		return nil, err
	}
	payload, turnID, handle, err := s.prepareTurn(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := s.transport.Send(payload); err != nil {
		s.clearActiveTurn(handle)
		s.finishTurnWithError(handle, err)
		return nil, err
	}
	s.dispatch(s.turnRawEvent("turn:started", turnID, nil))
	s.dispatch(s.turnRawEvent("turn:input_received", turnID, map[string]any{
		"input_type": "message",
		"source":     "user",
	}))
	return handle, nil
}

func (s *session) Steer(ctx context.Context, req dto.SteerRequest) error {
	if err := shared.CheckCtx(ctx); err != nil {
		return err
	}
	payload, err := buildSteerPayload(req)
	if err != nil {
		return err
	}
	turnID, err := s.sendSteer(payload, req.ExpectedTurnID)
	if err != nil {
		return err
	}
	s.dispatch(s.turnRawEvent("turn:input_received", turnID, map[string]any{
		"input_type": "message",
		"source":     "user",
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
	if handle == nil {
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
	s.transport = nil
	s.cleanup = nil
	s.mu.Unlock()
	cleanupInterruptedTransport(s.logger, reg, tr, cleanup)
	handle.finish(context.Canceled)
	s.dispatch(s.turnRawEvent("turn:interrupted", turnID, map[string]any{
		"reason": reason,
	}))
	return nil
}

func (s *session) ListThreads(context.Context) ([]dto.ThreadRef, error) {
	return nil, dto.NewCapabilityError(dto.CapThreadList, "claude")
}

func (s *session) ForkThread(context.Context, dto.ForkRequest) (dto.ForkResult, error) {
	return dto.ForkResult{}, dto.NewCapabilityError(dto.CapThreadFork, "claude")
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
	s.transport = nil
	s.cleanup = nil
	s.mu.Unlock()

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

func unregisterTransportPID(reg *pidregistry.Registry, tr *transport) {
	if reg == nil || tr == nil || tr.cmd == nil || tr.cmd.Process == nil {
		return
	}
	reg.Unregister(tr.cmd.Process.Pid)
}

func stopTransport(tr *transport, force bool) error {
	if force {
		return tr.Kill()
	}
	return tr.Close()
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

func (s *session) restartIfNeededLocked(ctx context.Context, req dto.TurnRequest) error {
	needsRestart := !s.transport.readyForSend()
	prevModel := s.model
	prevConfig := s.config
	prevManifest := s.manifest
	settingsChanged := s.applyTurnSettingsLocked(req)
	resumeID := s.restartResumeIDLocked()
	if !settingsChanged && !needsRestart {
		return s.awaitThreadReadyLocked(ctx)
	}
	restartReason := "settings_changed"
	if needsRestart {
		restartReason = "transport_unavailable"
	}
	if s.logger != nil {
		s.logger.Warn("claudecli: session restart triggered",
			"agent_id", s.agentID,
			"thread_id", s.threadID,
			"session_id", s.sessionID,
			"old_model", prevModel,
			"new_model", s.model,
			"resume_id", resumeID,
			"reason", restartReason,
		)
	}
	oldTransport := s.transport
	oldCleanup := s.cleanup
	tr, cleanup, err := launchCLI(
		s.binaryPath,
		s.cwd,
		s.model,
		s.instructions,
		s.config,
		s.manifest,
		resumeID,
	)
	if err != nil {
		s.model = prevModel
		s.config = prevConfig
		s.manifest = prevManifest
		return err
	}
	s.resetThreadReadyLocked()
	if shouldMarkThreadReady(resumeID, s.publicThreadID) {
		s.markThreadReadyLocked()
	}
	s.activeTurn = nil
	s.suppressedTurns = map[string]struct{}{}
	s.transport = tr
	s.cleanup = cleanup
	s.startReadLoop(tr)
	if oldTransport != nil || oldCleanup != nil {
		go releaseTransport(oldTransport, oldCleanup)
	}
	return s.awaitThreadReadyLocked(ctx)
}

func (s *session) restartResumeIDLocked() string {
	// Keep the last resolved thread/session identity across restarts until the
	// new transport confirms the resumed session with a fresh system:init.
	resumeID := strings.TrimSpace(shared.FirstNonEmpty(s.sessionID, s.threadID))
	if requiresResolvedThreadID(resumeID) {
		return ""
	}
	return resumeID
}

func releaseTransport(tr *transport, cleanup func()) {
	if tr != nil {
		_ = tr.Close()
	}
	if cleanup != nil {
		cleanup()
	}
}

func (s *session) applyTurnSettingsLocked(req dto.TurnRequest) bool {
	changed := updateString(&s.model, req.Overrides.Model)
	changed = updateString(&s.config.Effort, req.Overrides.Effort) || changed
	if !manifestChanged(req.MCP, s.manifest) {
		return changed
	}
	s.manifest = req.MCP
	return true
}

func updateString(dst *string, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || value == *dst {
		return false
	}
	*dst = value
	return true
}

func manifestChanged(next, current dto.MCPManifest) bool {
	return !reflect.DeepEqual(next, dto.MCPManifest{}) && !reflect.DeepEqual(next, current)
}

func (s *session) clearActiveTurn(handle *turnHandle) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeTurn == handle {
		s.activeTurn = nil
	}
}

func (s *session) dispatch(raw dto.RawProviderEvent) {
	if s.eventDispatcher != nil {
		s.eventDispatcher.Dispatch(raw)
	}
}

func (s *session) turnRawEvent(eventType, turnID string, extras map[string]any) dto.RawProviderEvent {
	base := s.rawBase()
	data := buildEventData(base, base.SessionID, time.Now().Format(time.RFC3339Nano), extras)
	if turnID = strings.TrimSpace(turnID); turnID != "" {
		data["turn_id"] = turnID
	}
	return dto.RawProviderEvent{EventType: eventType, Data: data}
}

func currentTurnID(handle *turnHandle) string {
	if handle == nil {
		return ""
	}
	if providerID := handle.ProviderID(); providerID != "" {
		return providerID
	}
	return handle.LocalID()
}

var _ contract.Session = (*session)(nil)

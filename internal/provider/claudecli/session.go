package claudecli

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
)

type session struct {
	agentID         string
	threadID        string
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
	manifest        dto.MCPManifest
	cleanup         func()
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

func (s *session) Capabilities() dto.CapabilitySet {
	return copyCapabilities(s.caps)
}

func (s *session) StartTurn(ctx context.Context, req dto.TurnRequest) (contract.TurnHandle, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	payload, turnID, handle, err := s.prepareTurn(req)
	if err != nil {
		return nil, err
	}
	if err := s.transport.Send(payload); err != nil {
		s.clearActiveTurn(handle)
		handle.finish(err)
		s.dispatch(s.turnRawEvent("turn:complete", turnID, map[string]any{
			"success": false,
			"error":   err.Error(),
		}))
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
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
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
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.transport.signalProcess(syscall.SIGINT); err != nil {
		return err
	}
	s.mu.Lock()
	handle := s.activeTurn
	s.activeTurn = nil
	s.mu.Unlock()
	if handle != nil {
		turnID := currentTurnID(handle)
		handle.finish(context.Canceled)
		s.dispatch(s.turnRawEvent("turn:interrupted", turnID, map[string]any{
			"reason": strings.TrimSpace(req.Source),
		}))
	}
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
	handle := s.activeTurn
	s.transport = nil
	s.cleanup = nil
	s.activeTurn = nil
	s.mu.Unlock()
	if handle != nil {
		handle.finish(errors.New("claudecli: session stopped"))
	}
	var err error
	if tr != nil {
		if force {
			err = tr.Kill()
		} else {
			err = tr.Close()
		}
	}
	if cleanup != nil {
		cleanup()
	}
	eventType := "agent:stopped"
	data := map[string]any{
		"agent_id":   s.agentID,
		"thread_id":  s.ThreadID(),
		"session_id": s.sessionID,
		"timestamp":  time.Now().Format(time.RFC3339Nano),
	}
	if force {
		eventType = "agent:failed"
		data["error"] = "session stopped"
	}
	s.dispatch(dto.RawProviderEvent{EventType: eventType, Data: data})
	return err
}

func (s *session) restartIfNeededLocked(req dto.TurnRequest) error {
	if !s.applyTurnSettingsLocked(req) || s.transport == nil {
		return nil
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
		s.threadID,
	)
	if err != nil {
		return err
	}
	s.transport = tr
	s.cleanup = cleanup
	s.startReadLoop(tr)
	if oldTransport != nil {
		_ = oldTransport.Close()
	}
	if oldCleanup != nil {
		oldCleanup()
	}
	return nil
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
	data := map[string]any{
		"agent_id":   s.agentID,
		"thread_id":  s.ThreadID(),
		"session_id": s.sessionID,
		"turn_id":    strings.TrimSpace(turnID),
		"timestamp":  time.Now().Format(time.RFC3339Nano),
	}
	for key, value := range extras {
		data[key] = value
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

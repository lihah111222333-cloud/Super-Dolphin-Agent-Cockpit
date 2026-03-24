package claudecli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"syscall"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

const capThreadConfigure = "thread_configure"

func (s *session) Configure(ctx context.Context, patch dto.ThreadConfigPatch) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if configurePatchEmpty(patch) {
		return nil
	}
	return fmt.Errorf(
		"claudecli: runtime Configure is not supported for active sessions: %w",
		dto.NewCapabilityError(configureCapability(patch), "claude"),
	)
}

func configurePatchEmpty(patch dto.ThreadConfigPatch) bool {
	return strings.TrimSpace(configureValue(patch.Model)) == "" &&
		strings.TrimSpace(configureValue(patch.Effort)) == "" &&
		strings.TrimSpace(configureValue(patch.Personality)) == "" &&
		strings.TrimSpace(configureValue(patch.Approvals)) == ""
}

func configureCapability(patch dto.ThreadConfigPatch) string {
	if strings.TrimSpace(configureValue(patch.Model)) != "" {
		return dto.CapModelSwitch
	}
	return capThreadConfigure
}

func configureValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

var claudeAllowedModels = []string{
	"sonnet",
	"haiku",
	"opus",
	"opus[1m]",
}

func (s *session) AllowedModels(context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	models := append([]string(nil), claudeAllowedModels...)
	current := strings.TrimSpace(s.model)
	if current == "" || modelAllowed(current, models) {
		return models, nil
	}
	return append(models, current), nil
}

func (s *session) ReadConfig(context.Context, string) (dto.ThreadConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	threadID := strings.TrimSpace(s.threadID)
	if threadID == "" {
		return dto.ThreadConfig{}, errors.New("claudecli: thread id is required")
	}
	return dto.ThreadConfig{
		ThreadID: threadID,
		Provider: "claude",
		Effective: dto.ThreadConfigValues{
			Model:     strings.TrimSpace(s.model),
			Effort:    strings.TrimSpace(s.config.Effort),
			Approvals: strings.TrimSpace(s.config.ApprovalPolicy),
		},
	}, nil
}

func modelAllowed(model string, allowed []string) bool {
	for _, candidate := range allowed {
		if strings.EqualFold(strings.TrimSpace(candidate), model) {
			return true
		}
	}
	return false
}

func (s *session) ForceComplete(ctx context.Context, req dto.ForceCompleteRequest) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.transport.signalProcess(syscall.SIGINT); err != nil {
		return err
	}
	s.forceCompleteTurn(strings.TrimSpace(req.ProviderID))
	return nil
}

func (s *session) forceCompleteTurn(turnID string) {
	if turnID == "" {
		turnID = currentTurnID(s.activeTurnHandle())
	}
	if turnID == "" {
		return
	}
	s.suppressTurn(turnID)
	s.dispatch(s.turnRawEvent("turn:complete", turnID, map[string]any{
		"success": true,
		"status":  "completed",
		"reason":  "force_complete",
	}))
	if handle := s.takeActiveTurn(turnID); handle != nil {
		handle.finish(nil)
	}
}

func (s *session) activeTurnHandle() *turnHandle {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeTurn
}

func (s *session) takeActiveTurn(turnID string) *turnHandle {
	s.mu.Lock()
	defer s.mu.Unlock()
	if turnID != "" && currentTurnID(s.activeTurn) != turnID {
		return nil
	}
	handle := s.activeTurn
	s.activeTurn = nil
	return handle
}

func (s *session) suppressTurn(turnID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.suppressedTurns[turnID] = struct{}{}
}

func (s *session) consumeSuppressedTurn(turnID string) bool {
	if turnID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.suppressedTurns[turnID]; !ok {
		return false
	}
	delete(s.suppressedTurns, turnID)
	return true
}

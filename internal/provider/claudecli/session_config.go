package claudecli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const capThreadConfigure = "thread_configure"

func (s *session) Configure(ctx context.Context, patch dto.ThreadConfigPatch) error {
	if err := shared.CheckCtx(ctx); err != nil {
		return err
	}
	if threadConfigPatchNoop(patch) {
		return nil
	}
	if threadConfigPatchHasUnsupportedFields(patch) {
		return fmt.Errorf(
			"claudecli: runtime Configure only supports model/effort overrides: %w",
			contract.NewCapabilityError(capThreadConfigure, "claude"),
		)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applyConfiguredOverridesLocked(patch, true)
	return nil
}

func threadConfigPatchNoop(patch dto.ThreadConfigPatch) bool {
	return patch.Model == nil && patch.Effort == nil && patch.Personality == nil && patch.Approvals == nil
}

func threadConfigPatchHasUnsupportedFields(patch dto.ThreadConfigPatch) bool {
	return patch.Personality != nil || patch.Approvals != nil
}

func stringPtr(value string) *string {
	return &value
}

func (s *session) applyConfiguredOverridesLocked(patch dto.ThreadConfigPatch, stagePending bool) {
	if patch.Model != nil {
		value := strings.TrimSpace(*patch.Model)
		s.overrideModel = value
		s.overrideModelSet = true
		if stagePending {
			s.pendingModel = stringPtr(value)
		}
	}
	if patch.Effort != nil {
		value := strings.TrimSpace(*patch.Effort)
		s.overrideEffort = value
		s.overrideEffortSet = true
		if stagePending {
			s.pendingEffort = stringPtr(value)
		}
	}
	if stagePending {
		s.configDirty = s.pendingModel != nil || s.pendingEffort != nil
	}
}

func (s *session) effectiveModelLocked() string {
	if value := strings.TrimSpace(s.model); value != "" {
		return value
	}
	return readClaudeSettingsModel(s.history)
}

func (s *session) currentTransportModel() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentTransportModelLocked()
}

func (s *session) currentTransportModelLocked() string {
	if value := strings.TrimSpace(s.transportModel); value != "" {
		return value
	}
	return s.effectiveModelLocked()
}

func (s *session) effectiveEffortLocked() string {
	effort := s.config.Effort
	if s.transport != nil && strings.TrimSpace(s.transportConfig.Effort) != "" {
		effort = s.transportConfig.Effort
	}
	return normalizeEffort(s.currentTransportModelLocked(), effort)
}

func (s *session) configuredOverrideModelLocked() string {
	if s.pendingModel != nil {
		return strings.TrimSpace(*s.pendingModel)
	}
	if s.overrideModelSet {
		return strings.TrimSpace(s.overrideModel)
	}
	return ""
}

func (s *session) configuredOverrideEffortLocked() string {
	if s.pendingEffort != nil {
		return strings.TrimSpace(*s.pendingEffort)
	}
	if s.overrideEffortSet {
		return strings.TrimSpace(s.overrideEffort)
	}
	return ""
}

var claudeAllowedModels = []string{
	"best",
	// Short aliases resolved by Claude CLI to the current latest version.
	"sonnet",
	"sonnet[1m]",
	"haiku",
	"opus",
	"opus[1m]",
	// Explicit version slugs — let users pin a specific version.
	"claude-opus-4-7",
	"claude-opus-4-7[1m]",
	"claude-opus-4-6",
	"claude-opus-4-6[1m]",
	"claude-sonnet-4-7",
	"claude-sonnet-4-7[1m]",
	"claude-sonnet-4-6",
	"claude-sonnet-4-6[1m]",
}

func (s *session) AllowedModels(context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	models := append([]string(nil), claudeAllowedModels...)
	current := s.effectiveModelLocked()
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
		ThreadID:               threadID,
		Provider:               "claude",
		SupportsThreadOverride: true,
		Override: dto.ThreadConfigValues{
			Model:  s.configuredOverrideModelLocked(),
			Effort: s.configuredOverrideEffortLocked(),
		},
		Effective: dto.ThreadConfigValues{
			Model:     s.currentTransportModelLocked(),
			Effort:    s.effectiveEffortLocked(),
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
	if err := shared.CheckCtx(ctx); err != nil {
		return err
	}
	if err := s.transport.signalProcess(sigInterrupt); err != nil {
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
	return s.takeActiveTurnLocked()
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

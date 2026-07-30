package claudecli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

const capThreadConfigure = "thread_configure"

// Configure 更新 Claude 会话允许运行时覆盖的配置。
// 当前只支持 model/effort；其他字段直接返回能力错误，避免静默接受但不生效。
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

// applyConfiguredOverridesLocked 在持锁状态下记录 model/effort 覆盖。
// stagePending=true 表示新值要等下一次 transport restart 成功后再变成实际运行态。
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
	// Explicit version slugs let users pin older versions; latest families use aliases.
	"claude-opus-4-6",
	"claude-opus-4-6[1m]",
	"claude-sonnet-4-6",
	"claude-sonnet-4-6[1m]",
}

// AllowedModels 返回 UI 可选模型，并保留当前实际模型。
// 当前模型可能来自 Claude settings 或旧配置，即使不在静态列表中也要追加，避免前端显示丢失。
func (s *session) AllowedModels(context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	models := append([]string(nil), claudeAllowedModels...)
	current := claudeLaunchDisplayModel(s.effectiveModelLocked(), s.history)
	if current == "" || modelAllowed(current, models) {
		return models, nil
	}
	return append(models, current), nil
}

// ReadConfig 汇总线程配置的覆盖值与当前 transport 生效值。
// threadID 缺失代表会话尚未绑定到可配置线程，调用方需要得到明确错误。
func (s *session) ReadConfig(context.Context, string) (dto.ThreadConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	threadID := strings.TrimSpace(s.threadID)
	if threadID == "" {
		threadID = strings.TrimSpace(s.publicThreadID)
	}
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

// ForceComplete 将活跃 Claude turn 标记为完成并尝试中断底层进程。
// providerID 不匹配时不做任何事，防止旧 UI 请求误关当前 turn。
func (s *session) ForceComplete(ctx context.Context, req dto.ForceCompleteRequest) error {
	if err := shared.CheckCtx(ctx); err != nil {
		return err
	}
	providerID := strings.TrimSpace(req.ProviderID)
	tr, handle, turnID := s.forceCompleteTarget(providerID)
	if handle == nil || turnID == "" {
		return nil
	}
	if tr != nil {
		if err := normalizeSignalError(tr.signalProcess(sigInterrupt)); err != nil {
			return err
		}
	}
	s.forceCompleteTurn(tr, handle, turnID)
	return nil
}

func (s *session) forceCompleteTarget(providerID string) (*transport, *turnHandle, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	handle := s.activeTurn
	turnID := currentTurnID(handle)
	if handle == nil || turnID == "" {
		return s.transport, nil, ""
	}
	if providerID != "" && turnID != providerID {
		return s.transport, nil, ""
	}
	return s.transport, handle, turnID
}

// forceCompleteTurn 在确认 active turn 未漂移后发布系统中断事件。
// 已收口 turn 的 transport 不能再承载新 turn，否则迟到 result 会被错误归属给新 handle。
func (s *session) forceCompleteTurn(tr *transport, target *turnHandle, turnID string) {
	if target == nil || turnID == "" {
		return
	}
	var interrupted dto.RawProviderEvent
	s.mu.Lock()
	if s.activeTurn != target || currentTurnID(s.activeTurn) != turnID {
		s.mu.Unlock()
		return
	}
	if s.suppressedTurns == nil {
		s.suppressedTurns = map[string]struct{}{}
	}
	s.suppressedTurns[turnID] = struct{}{}
	if tr != nil && s.transport == tr {
		s.fencedTransport = tr
	}
	interrupted = s.turnRawEventLocked("turn:interrupted", turnID, map[string]any{
		"termination_cause": "system",
	})
	handle := s.takeActiveTurnLocked()
	s.mu.Unlock()
	s.dispatch(interrupted)
	handle.finish(nil)
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

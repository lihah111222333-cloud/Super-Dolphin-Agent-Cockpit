package thread

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	"github.com/anthropic-ai/super-agent-v3/internal/util/clone"
)

// NOTE(P8): 这里仍处在过渡态；provider-neutral Session 只暴露 Configure /
// Interrupt 等少量 typed surface，所以 SendCommand 目前只对高频命令做结构化
// 处理，其余命令保留低频兼容壳。`thread/skills/list` 返回线程绑定的 active
// skills，不同于扫描全量本地目录的 `skills/list`。
func (s *service) SendCommand(ctx context.Context, threadID, command, args string) (any, error) {
	cmd := normalizeCommand(command)
	if cmd == "/clear" {
		return s.sendClearCommand(ctx, threadID)
	}
	session, binding, err := s.resolveSession(ctx, threadID)
	if err != nil {
		return nil, err
	}
	switch cmd {
	case "/config/get":
		return s.GetConfig(ctx, threadID)
	case "/model":
		return s.SetModel(ctx, threadID, args)
	case "/personality", "/approvals":
		return sendConfigPatchCommand(ctx, session, cmd, threadID, args)
	case "/interrupt":
		return sendInterruptCommand(ctx, session, binding, threadID, args)
	case "/compact":
		return s.Compact(ctx, threadID, args)
	case "/config/set", "/rollback", "/undo", "/clean", "/mcp", "/skills":
		return nil, lowFrequencyCommandError(cmd)
	case "/realtime/start", "/realtime/appendaudio", "/realtime/appendtext", "/realtime/stop":
		return nil, lowFrequencyCommandError(cmd)
	default:
		return nil, fmt.Errorf("unsupported command: %s", cmd)
	}
}

func normalizeCommand(command string) string {
	cmd := strings.TrimSpace(strings.ToLower(command))
	if cmd == "" {
		return ""
	}
	if strings.HasPrefix(cmd, "/") {
		return cmd
	}
	return "/" + cmd
}

func commandPatch(command, args string) (dto.ThreadConfigPatch, error) {
	value := strings.TrimSpace(args)
	if value == "" {
		return dto.ThreadConfigPatch{}, errors.New("command args are required")
	}
	switch command {
	case "/personality":
		return dto.ThreadConfigPatch{Personality: &value}, nil
	case "/approvals":
		return dto.ThreadConfigPatch{Approvals: &value}, nil
	default:
		return dto.ThreadConfigPatch{}, fmt.Errorf("unsupported command: %s", command)
	}
}

type (
	threadCommandResult struct {
		Command  string `json:"command"`
		ThreadID string `json:"threadId"`
	}

	configReaderSession interface {
		ReadConfig(ctx context.Context, threadID string) (dto.ThreadConfig, error)
	}
	modelCatalogSession interface {
		AllowedModels(ctx context.Context) ([]string, error)
	}
	compactSession interface {
		CompactThread(ctx context.Context, threadID, args string) error
	}

	friendlyCapabilityError struct {
		message string
		cause   error
	}
)

const (
	errRuntimeModelSwitchUnsupported = "当前 provider 不支持运行时 model 切换"
	errContextCompactUnsupported     = "当前 provider 不支持上下文压缩（context_compact）"
)

// Error 返回错误文本。
func (e *friendlyCapabilityError) Error() string { return e.message }

// Unwrap 返回底层错误。
func (e *friendlyCapabilityError) Unwrap() error { return e.cause }

func newThreadCommandResult(command, threadID string) threadCommandResult {
	return threadCommandResult{Command: command, ThreadID: strings.TrimSpace(threadID)}
}

func (s *service) sendClearCommand(ctx context.Context, threadID string) (threadCommandResult, error) {
	if err := s.invalidatePromptAssembly(ctx, contract.InvalidateClear); err != nil {
		return threadCommandResult{}, err
	}
	return newThreadCommandResult("/clear", threadID), nil
}

func sendConfigPatchCommand(
	ctx context.Context,
	session contract.Session,
	command string,
	threadID string,
	args string,
) (threadCommandResult, error) {
	patch, err := commandPatch(command, args)
	if err != nil {
		return threadCommandResult{}, err
	}
	if err := session.Configure(ctx, patch); err != nil {
		return threadCommandResult{}, err
	}
	return newThreadCommandResult(command, threadID), nil
}

func sendInterruptCommand(
	ctx context.Context,
	session contract.Session,
	binding *bindingstore.Binding,
	threadID string,
	args string,
) (threadCommandResult, error) {
	req := dto.InterruptRequest{ThreadID: historyTargetID(binding, threadID), Source: strings.TrimSpace(args)}
	if err := session.Interrupt(ctx, req); err != nil {
		return threadCommandResult{}, err
	}
	return newThreadCommandResult("/interrupt", threadID), nil
}

func lowFrequencyCommandError(command string) error {
	return fmt.Errorf("command %s is not yet supported in the current session", command)
}

func newFriendlyCapabilityError(capability, provider, message string) error {
	return &friendlyCapabilityError{
		message: message,
		cause:   contract.NewCapabilityError(capability, providerLabel(provider)),
	}
}

func wrapFriendlyCapabilityError(err error, capability, provider, message string) error {
	var capErr *contract.CapabilityError
	if !errors.As(err, &capErr) || capErr.Capability != capability {
		return err
	}
	return newFriendlyCapabilityError(capability, provider, message)
}

func providerLabel(provider string) string {
	if provider = strings.TrimSpace(provider); provider != "" {
		return provider
	}
	return "active"
}

func bindingProvider(binding *bindingstore.Binding) string {
	if binding == nil {
		return providerLabel("")
	}
	return providerLabel(binding.Provider)
}

// GetConfig 读取配置。
func (s *service) GetConfig(ctx context.Context, threadID string) (dto.ThreadConfig, error) {
	session, binding, err := s.resolveSession(ctx, threadID)
	if err != nil {
		cfg, handled, offlineErr := s.pendingLaunchOfflineConfig(ctx, threadID, err)
		if offlineErr != nil {
			return dto.ThreadConfig{}, offlineErr
		}
		if handled {
			return cfg, nil
		}
		cfg, handled, offlineErr = s.offlineConfigForMissingSession(ctx, threadID, binding, err)
		if offlineErr != nil {
			return dto.ThreadConfig{}, offlineErr
		}
		if handled {
			return cfg, nil
		}
		return dto.ThreadConfig{}, err
	}
	reader, ok := session.(configReaderSession)
	if !ok {
		return dto.ThreadConfig{}, errors.New("thread config reader is not available")
	}
	cfg, err := reader.ReadConfig(ctx, threadID)
	if err != nil {
		return dto.ThreadConfig{}, err
	}
	return s.normalizeThreadConfig(ctx, threadID, binding, cfg), nil
}

func (s *service) offlineConfigForMissingSession(
	ctx context.Context,
	threadID string,
	binding *bindingstore.Binding,
	resolveErr error,
) (dto.ThreadConfig, bool, error) {
	if !errors.Is(resolveErr, contract.ErrSessionNotFound) {
		return dto.ThreadConfig{}, false, nil
	}
	if binding == nil {
		resolvedBinding, err := s.resolveBinding(ctx, threadID)
		if err != nil {
			return dto.ThreadConfig{}, false, err
		}
		binding = resolvedBinding
	}
	offline, err := s.buildOfflineConfig(ctx, threadID, binding)
	if err != nil {
		return dto.ThreadConfig{}, false, err
	}
	return offline.Config, true, nil
}

func (s *service) offlineRuntimeConfigForMissingSession(
	ctx context.Context,
	threadID string,
	binding *bindingstore.Binding,
	resolveErr error,
) (map[string]any, bool, error) {
	if !errors.Is(resolveErr, contract.ErrSessionNotFound) {
		return nil, false, nil
	}
	if binding == nil {
		resolvedBinding, err := s.resolveBinding(ctx, threadID)
		if err != nil {
			return nil, false, err
		}
		binding = resolvedBinding
	}
	offline, err := s.buildOfflineConfig(ctx, threadID, binding)
	if err != nil {
		return nil, false, err
	}
	return clone.RuntimeConfigMap(offline.Runtime), true, nil
}

func (s *service) pendingLaunchOfflineConfig(
	ctx context.Context,
	threadID string,
	resolveErr error,
) (dto.ThreadConfig, bool, error) {
	if !contract.IsNotFound(resolveErr) {
		return dto.ThreadConfig{}, false, nil
	}
	pendingLaunch, err := s.isThreadPendingLaunch(ctx, threadID)
	if err != nil {
		return dto.ThreadConfig{}, false, err
	}
	if !pendingLaunch {
		return dto.ThreadConfig{}, false, nil
	}
	offline, err := s.buildOfflineConfig(ctx, threadID, nil)
	if err != nil {
		return dto.ThreadConfig{}, false, err
	}
	return offline.Config, true, nil
}

// SetConfig 设置配置。
func (s *service) SetConfig(ctx context.Context, threadID string, patch dto.ThreadConfigPatch) (dto.ThreadConfig, error) {
	session, binding, err := s.resolveSession(ctx, threadID)
	if err != nil {
		return dto.ThreadConfig{}, err
	}
	provider := bindingProvider(binding)
	patch, err = normalizeThreadConfigPatch(ctx, session, provider, patch)
	if err != nil {
		return dto.ThreadConfig{}, err
	}
	if threadConfigPatchNoop(patch) {
		return s.GetConfig(ctx, threadID)
	}
	if err := session.Configure(ctx, patch); err != nil {
		return dto.ThreadConfig{}, wrapThreadConfigPatchError(err, provider, patch)
	}
	if err := s.persistThreadConfig(ctx, threadID, patch, dto.ThreadConfig{}); err != nil {
		return dto.ThreadConfig{}, err
	}
	cfg, err := s.GetConfig(ctx, threadID)
	if err != nil {
		return dto.ThreadConfig{}, err
	}
	s.logConfigPatchApplied("thread/config/set", threadID, provider, patch)
	s.emitThreadModelUpdated(threadID, patch.Model)
	if err := s.invalidatePromptAssembly(ctx, contract.InvalidateProviderSwitch); err != nil {
		return dto.ThreadConfig{}, err
	}
	return applyThreadConfigReturnPatch(cfg, patch), nil
}

// SetModel 设置模型。
func (s *service) SetModel(ctx context.Context, threadID, rawModel string) (dto.ThreadConfig, error) {
	session, binding, err := s.resolveSession(ctx, threadID)
	if err != nil {
		return dto.ThreadConfig{}, err
	}
	provider := bindingProvider(binding)
	model, err := validateModelName(rawModel)
	if err != nil {
		return dto.ThreadConfig{}, err
	}
	if err := ensureAllowedModel(ctx, session, model, provider); err != nil {
		return dto.ThreadConfig{}, err
	}
	if err := session.Configure(ctx, dto.ThreadConfigPatch{Model: &model}); err != nil {
		return dto.ThreadConfig{}, wrapFriendlyCapabilityError(
			err,
			dto.CapModelSwitch,
			provider,
			errRuntimeModelSwitchUnsupported,
		)
	}
	cfg, err := s.GetConfig(ctx, threadID)
	if err != nil {
		return dto.ThreadConfig{}, err
	}
	if err := s.persistThreadConfig(
		ctx,
		threadID,
		dto.ThreadConfigPatch{Model: &model},
		cfg,
	); err != nil {
		return dto.ThreadConfig{}, err
	}
	if err := s.invalidatePromptAssembly(ctx, contract.InvalidateProviderSwitch); err != nil {
		return dto.ThreadConfig{}, err
	}
	return cfg, nil
}

func normalizeThreadConfigPatch(
	ctx context.Context,
	session contract.Session,
	provider string,
	patch dto.ThreadConfigPatch,
) (dto.ThreadConfigPatch, error) {
	return normalizeThreadConfigPatchBase(provider, patch, func(model string) error {
		return ensureAllowedModel(ctx, session, model, provider)
	})
}

// normalizeThreadConfigPatchOffline validates a thread config patch without an active session.
// Model name format and effort range are checked, but session-scoped model whitelist validation is skipped.
// normalizeThreadConfigPatchBase 规范化线程配置补丁base。
func normalizeThreadConfigPatchBase(
	provider string,
	patch dto.ThreadConfigPatch,
	validateModel func(string) error,
) (dto.ThreadConfigPatch, error) {
	patch.Model, patch.Effort = trimThreadConfigPatchValue(patch.Model), trimThreadConfigPatchValue(patch.Effort)
	patch.Personality, patch.Approvals = trimThreadConfigPatchValue(patch.Personality), trimThreadConfigPatchValue(patch.Approvals)
	if model := threadConfigPatchValue(patch.Model); model != "" {
		validated, err := validateModelName(model)
		if err != nil {
			return dto.ThreadConfigPatch{}, err
		}
		patch.Model = &validated
		if validateModel != nil {
			if err := validateModel(validated); err != nil {
				return dto.ThreadConfigPatch{}, err
			}
		}
	}
	if err := validateThreadConfigEffort(provider, threadConfigPatchValue(patch.Effort)); err != nil {
		return dto.ThreadConfigPatch{}, err
	}
	return patch, nil
}

// ensureAllowedModel 确保allowed模型。
func ensureAllowedModel(
	ctx context.Context,
	session contract.Session,
	model string,
	provider string,
) error {
	catalog, ok := session.(modelCatalogSession)
	if !ok {
		return newFriendlyCapabilityError(dto.CapModelSwitch, provider, errRuntimeModelSwitchUnsupported)
	}
	allowed, err := catalog.AllowedModels(ctx)
	if err != nil {
		return wrapFriendlyCapabilityError(err, dto.CapModelSwitch, provider, errRuntimeModelSwitchUnsupported)
	}
	if modelAllowed(model, allowed) || providerAllowsRelaxedThreadConfig(provider) {
		return nil
	}
	if len(allowed) == 0 {
		return errors.New("provider model catalog is empty")
	}
	return fmt.Errorf("model %q is not supported by active provider", model)
}

func modelAllowed(model string, allowed []string) bool {
	for _, candidate := range allowed {
		if strings.EqualFold(strings.TrimSpace(candidate), model) {
			return true
		}
	}
	return false
}

func providerAllowsRelaxedThreadConfig(provider string) bool {
	return strings.EqualFold(strings.TrimSpace(provider), "claude")
}

func (s *service) storedThreadModel(ctx context.Context, threadID string) string {
	thread, err := s.getThread(ctx, threadID)
	if err != nil || thread == nil {
		return ""
	}
	return strings.TrimSpace(thread.Model)
}

func validateModelName(value string) (string, error) {
	model := strings.TrimSpace(value)
	if model == "" {
		return "", errors.New("model is required")
	}
	if strings.IndexFunc(model, func(r rune) bool { return !isModelRuneAllowed(r) }) >= 0 {
		return "", fmt.Errorf("invalid model name %q", model)
	}
	return model, nil
}

func validateThreadConfigEffort(provider, value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "none", "minimal", "low", "medium", "high", "xhigh":
		return nil
	case "max":
		if providerAllowsRelaxedThreadConfig(provider) {
			return nil
		}
	}
	return fmt.Errorf("invalid effort %q", value)
}

// isModelRuneAllowed 判断模型runeallowed是否可用。
func isModelRuneAllowed(r rune) bool {
	return r >= 'a' && r <= 'z' ||
		r >= 'A' && r <= 'Z' ||
		r >= '0' && r <= '9' ||
		strings.ContainsRune("._:/@+-[]", r)
}

func (s *service) logConfigPatchApplied(op, threadID, provider string, patch dto.ThreadConfigPatch) {
	if s.logger == nil {
		return
	}
	attrs := []any{"thread_id", threadID, "provider", provider}
	for _, field := range []struct {
		key   string
		value *string
	}{{"model", patch.Model}, {"effort", patch.Effort}, {"personality", patch.Personality}, {"approvals", patch.Approvals}} {
		if field.value != nil {
			attrs = append(attrs, field.key, *field.value)
		}
	}
	s.logger.Warn(op+": config patch applied", attrs...)
}

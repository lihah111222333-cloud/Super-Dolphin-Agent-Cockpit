package thread

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
)

// NOTE(P8): 这里仍处在过渡态。
// thread RPC 已经暴露了不少结构化方法，但 provider-neutral Session 目前只提供
// Configure / Interrupt 等少数 typed surface，没有统一的 slash-command / session-control 契约。
// 因此 SendCommand 现在只能对高频命令补参数校验与结构化结果；其余命令先保留壳层并显式标 TODO。
//
// thread/skills/list 通过这里的 thread 命令通道下沉，语义上返回 thread 绑定的 active skills。
// 与 skills/list 不同：后者扫描本地 skill 目录，返回所有已安装的 skill 元信息。
func (s *service) SendCommand(ctx context.Context, threadID, command, args string) (any, error) {
	session, binding, err := s.resolveSession(ctx, threadID)
	if err != nil {
		return nil, err
	}
	cmd := normalizeCommand(command)
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

type threadCommandResult struct {
	Command  string `json:"command"`
	ThreadID string `json:"threadId"`
}

type configReaderSession interface {
	ReadConfig(ctx context.Context, threadID string) (dto.ThreadConfig, error)
}

type modelCatalogSession interface {
	AllowedModels(ctx context.Context) ([]string, error)
}

type compactSession interface {
	CompactThread(ctx context.Context, threadID, args string) error
}

const (
	errRuntimeModelSwitchUnsupported = "当前 provider 不支持运行时 model 切换"
	errContextCompactUnsupported     = "当前 provider 不支持上下文压缩（context_compact）"
)

type friendlyCapabilityError struct {
	message string
	cause   error
}

func (e *friendlyCapabilityError) Error() string { return e.message }

func (e *friendlyCapabilityError) Unwrap() error { return e.cause }

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
	return threadCommandResult{
		Command:  command,
		ThreadID: strings.TrimSpace(threadID),
	}, nil
}

func sendInterruptCommand(
	ctx context.Context,
	session contract.Session,
	binding *bindingstore.Binding,
	threadID string,
	args string,
) (threadCommandResult, error) {
	req := dto.InterruptRequest{
		ThreadID: historyTargetID(binding, threadID),
		Source:   strings.TrimSpace(args),
	}
	if err := session.Interrupt(ctx, req); err != nil {
		return threadCommandResult{}, err
	}
	return threadCommandResult{
		Command:  "/interrupt",
		ThreadID: strings.TrimSpace(threadID),
	}, nil
}

func lowFrequencyCommandError(command string) error {
	return fmt.Errorf("TODO(P9): implement typed thread handler for %s", command)
}

func newFriendlyCapabilityError(capability, provider, message string) error {
	return &friendlyCapabilityError{
		message: message,
		cause:   dto.NewCapabilityError(capability, providerLabel(provider)),
	}
}

func wrapFriendlyCapabilityError(err error, capability, provider, message string) error {
	var capErr *dto.CapabilityError
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

func (s *service) GetConfig(ctx context.Context, threadID string) (dto.ThreadConfig, error) {
	session, binding, err := s.resolveSession(ctx, threadID)
	if err != nil {
		offline, offlineErr := s.buildOfflineConfig(ctx, threadID, binding)
		if offlineErr != nil {
			return dto.ThreadConfig{}, offlineErr
		}
		return offline.Config, nil
	}
	reader, ok := session.(configReaderSession)
	if !ok {
		offline, offlineErr := s.buildOfflineConfig(ctx, threadID, binding)
		if offlineErr != nil {
			return dto.ThreadConfig{}, errors.New("thread config reader is not available")
		}
		return offline.Config, nil
	}
	cfg, err := reader.ReadConfig(ctx, threadID)
	if err != nil {
		return dto.ThreadConfig{}, err
	}
	return s.normalizeThreadConfig(ctx, threadID, binding, cfg), nil
}

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
	cfg, err := s.GetConfig(ctx, threadID)
	if err != nil {
		return dto.ThreadConfig{}, err
	}
	if err := s.persistThreadConfig(ctx, threadID, patch, cfg); err != nil {
		return dto.ThreadConfig{}, err
	}
	return cfg, nil
}

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
	return cfg, nil
}

func normalizeThreadConfigPatch(
	ctx context.Context,
	session contract.Session,
	provider string,
	patch dto.ThreadConfigPatch,
) (dto.ThreadConfigPatch, error) {
	patch.Model = trimThreadConfigPatchValue(patch.Model)
	patch.Effort = trimThreadConfigPatchValue(patch.Effort)
	patch.Personality = trimThreadConfigPatchValue(patch.Personality)
	patch.Approvals = trimThreadConfigPatchValue(patch.Approvals)
	if model := threadConfigPatchValue(patch.Model); model != "" {
		validated, err := validateModelName(model)
		if err != nil {
			return dto.ThreadConfigPatch{}, err
		}
		patch.Model = &validated
		if err := ensureAllowedModel(ctx, session, validated, provider); err != nil {
			return dto.ThreadConfigPatch{}, err
		}
	}
	if err := validateThreadConfigEffort(threadConfigPatchValue(patch.Effort)); err != nil {
		return dto.ThreadConfigPatch{}, err
	}
	return patch, nil
}

func threadConfigPatchNoop(patch dto.ThreadConfigPatch) bool {
	return patch.Model == nil &&
		patch.Effort == nil &&
		patch.Personality == nil &&
		patch.Approvals == nil
}

func wrapThreadConfigPatchError(err error, provider string, patch dto.ThreadConfigPatch) error {
	if patch.Model != nil {
		return wrapFriendlyCapabilityError(
			err,
			dto.CapModelSwitch,
			provider,
			errRuntimeModelSwitchUnsupported,
		)
	}
	return err
}

func (s *service) normalizeThreadConfig(
	ctx context.Context,
	threadID string,
	binding *bindingstore.Binding,
	cfg dto.ThreadConfig,
) dto.ThreadConfig {
	cfg.ThreadID = firstNonEmpty(strings.TrimSpace(cfg.ThreadID), strings.TrimSpace(threadID))
	if binding != nil && strings.TrimSpace(cfg.Provider) == "" {
		cfg.Provider = strings.TrimSpace(binding.Provider)
	}
	if !cfg.SupportsThreadOverride && supportsThreadOverride(cfg.Provider) {
		cfg.SupportsThreadOverride = true
	}
	if cfg.Effective.Model == "" {
		cfg.Effective.Model = s.storedThreadModel(ctx, threadID)
	}
	return cfg
}

func ensureAllowedModel(
	ctx context.Context,
	session contract.Session,
	model string,
	provider string,
) error {
	catalog, ok := session.(modelCatalogSession)
	if !ok {
		return newFriendlyCapabilityError(
			dto.CapModelSwitch,
			provider,
			errRuntimeModelSwitchUnsupported,
		)
	}
	allowed, err := catalog.AllowedModels(ctx)
	if err != nil {
		return wrapFriendlyCapabilityError(
			err,
			dto.CapModelSwitch,
			provider,
			errRuntimeModelSwitchUnsupported,
		)
	}
	if len(allowed) == 0 {
		return errors.New("provider model catalog is empty")
	}
	if modelAllowed(model, allowed) {
		return nil
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

func trimThreadConfigPatchValue(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	return &trimmed
}

func threadConfigPatchValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func (s *service) storedThreadModel(ctx context.Context, threadID string) string {
	thread, err := s.getThread(ctx, threadID)
	if err != nil || thread == nil {
		return ""
	}
	return strings.TrimSpace(thread.Model)
}

func (s *service) recordThreadModel(ctx context.Context, threadID, model string) {
	thread, err := s.getThread(ctx, threadID)
	if err != nil || thread == nil {
		return
	}
	if strings.TrimSpace(thread.Model) == model {
		return
	}
	thread.Model = model
	thread.UpdatedAt = time.Now().Unix()
	shared.LogIgnoredError(s.logger, "upsert thread failed", s.upsertThread(ctx, *thread))
}

func validateModelName(value string) (string, error) {
	model := strings.TrimSpace(value)
	if model == "" {
		return "", errors.New("model is required")
	}
	for _, r := range model {
		if isModelRuneAllowed(r) {
			continue
		}
		return "", fmt.Errorf("invalid model name %q", model)
	}
	return model, nil
}

func validateThreadConfigEffort(value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "none", "minimal", "low", "medium", "high", "xhigh":
		return nil
	default:
		return fmt.Errorf("invalid effort %q", value)
	}
}

func isModelRuneAllowed(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z':
		return true
	case r >= 'A' && r <= 'Z':
		return true
	case r >= '0' && r <= '9':
		return true
	default:
		return strings.ContainsRune("._:/@+-", r)
	}
}

package thread

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/clone"
)

// SendCommand 处理仍未拆成独立 typed handler 的线程命令。
// 当前 provider-neutral Session 只暴露 Configure、Interrupt 等少量能力；低频命令会明确返回 unsupported，
// `thread/skills/list` 语义上读取线程绑定的 active skills，不扫描本地全量 skill 目录。
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
		return sendInterruptCommand(ctx, session, threadBindingRecordFromStore(binding), threadID, args)
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

// Error 返回可直接展示给用户的能力错误文案。
func (e *friendlyCapabilityError) Error() string { return e.message }

// Unwrap 暴露底层 CapabilityError，便于调用方用 errors.As 判断 provider 能力缺失。
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
	binding *threadBindingRecord,
	threadID string,
	args string,
) (threadCommandResult, error) {
	targetID, err := historyTargetIDRecord(binding, threadID)
	if err != nil {
		return threadCommandResult{}, err
	}
	req := dto.InterruptRequest{ThreadID: targetID, Source: strings.TrimSpace(args)}
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

func bindingRecordProvider(binding *threadBindingRecord) string {
	if binding == nil {
		return providerLabel("")
	}
	return providerLabel(binding.Provider)
}

// GetConfig 读取线程当前配置。
// 活跃 session 优先；pending_launch 或 session 缺失时会按持久化配置合成离线快照。
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
		cfg, handled, offlineErr = s.offlineConfigForMissingSession(ctx, threadID, threadBindingRecordFromStore(binding), err)
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
	models, err := providerModelCatalog(ctx, session)
	if err != nil {
		return dto.ThreadConfig{}, err
	}
	cfg = s.normalizeThreadConfig(ctx, threadID, threadBindingRecordFromStore(binding), cfg)
	// Agent runtime id 只用于读取当前 Provider 的实时配置与模型目录；
	// 这类会话仍沿用 provider 全局偏好，不能被误判为支持 thread override。
	if strings.TrimSpace(threadID) == strings.TrimSpace(binding.AgentID) {
		cfg.SupportsThreadOverride = false
	}
	cfg.AvailableModels = models
	return cfg, nil
}

func (s *service) offlineConfigForMissingSession(
	ctx context.Context,
	threadID string,
	binding *threadBindingRecord,
	resolveErr error,
) (dto.ThreadConfig, bool, error) {
	if !errors.Is(resolveErr, contract.ErrSessionNotFound) {
		return dto.ThreadConfig{}, false, nil
	}
	if binding == nil {
		resolvedBinding, err := s.resolveThreadBindingRecord(ctx, threadID)
		if err != nil {
			return dto.ThreadConfig{}, false, err
		}
		binding = resolvedBinding
	}
	offline, err := s.buildOfflineConfigRecord(ctx, threadID, binding)
	if err != nil {
		return dto.ThreadConfig{}, false, err
	}
	return offline.Config, true, nil
}

func (s *service) offlineRuntimeConfigForMissingSessionRecord(
	ctx context.Context,
	threadID string,
	binding *threadBindingRecord,
	resolveErr error,
) (map[string]any, bool, error) {
	if !errors.Is(resolveErr, contract.ErrSessionNotFound) {
		return nil, false, nil
	}
	if binding == nil {
		resolvedBinding, err := s.resolveThreadBindingRecord(ctx, threadID)
		if err != nil {
			return nil, false, err
		}
		binding = resolvedBinding
	}
	offline, err := s.buildOfflineConfigRecord(ctx, threadID, binding)
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

// SetConfig 更新线程配置并同步 provider session。
// patch 会先校验，再写入 session 与 thread store；任何一步失败都会返回错误，避免内存态和持久化状态分叉。
func (s *service) SetConfig(ctx context.Context, threadID string, patch dto.ThreadConfigPatch) (dto.ThreadConfig, error) {
	session, binding, err := s.resolveSession(ctx, threadID)
	if err != nil {
		return dto.ThreadConfig{}, err
	}
	provider := bindingRecordProvider(threadBindingRecordFromStore(binding))
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

// SetModel 切换线程模型。
// 模型名先做格式和 provider allow-list 校验，通过后再写 session 和持久化配置。
func (s *service) SetModel(ctx context.Context, threadID, rawModel string) (dto.ThreadConfig, error) {
	session, binding, err := s.resolveSession(ctx, threadID)
	if err != nil {
		return dto.ThreadConfig{}, err
	}
	provider := bindingRecordProvider(threadBindingRecordFromStore(binding))
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

// normalizeThreadConfigPatchOffline 在没有活跃 session 时校验配置补丁。
// 离线路径只能检查模型名格式和 effort 范围，不能访问 provider 的实时模型 allow-list。
func normalizeThreadConfigPatchOffline(provider string, patch dto.ThreadConfigPatch) (dto.ThreadConfigPatch, error) {
	return normalizeThreadConfigPatchBase(provider, patch, nil)
}

// normalizeThreadConfigPatchBase 清理并校验线程配置补丁。
// validateModel 非 nil 时执行 provider 侧模型校验；离线路径传 nil，只保留本地可验证规则。
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

// ensureAllowedModel 校验模型是否在当前 provider session 的 allow-list 中。
// provider 不支持模型目录时返回友好能力错误；目录为空则 fail-fast，避免接受不可确认的模型。
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

// providerModelCatalog 读取并规范化当前 Provider 的模型目录。
// 目录缺失或为空时直接报错，避免 UI 回退到另一份本地硬编码列表。
func providerModelCatalog(ctx context.Context, session contract.Session) ([]string, error) {
	catalog, ok := session.(modelCatalogSession)
	if !ok {
		return nil, errors.New("provider model catalog is not available")
	}
	allowed, err := catalog.AllowedModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("list provider models: %w", err)
	}
	models := make([]string, 0, len(allowed))
	seen := make(map[string]struct{}, len(allowed))
	for _, candidate := range allowed {
		model := strings.TrimSpace(candidate)
		key := strings.ToLower(model)
		if model == "" {
			return nil, errors.New("provider model catalog contains an empty model")
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		models = append(models, model)
	}
	if len(models) == 0 {
		return nil, errors.New("provider model catalog is empty")
	}
	return models, nil
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

// isModelRuneAllowed 限制模型名可用字符。
// 只允许 provider 常见模型标识符需要的字母、数字和少量分隔符，防止控制字符进入配置。
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

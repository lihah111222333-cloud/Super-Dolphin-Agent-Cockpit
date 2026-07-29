package unified

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/clone"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/providerrecovery"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// resumeSessionIdentityResolver 是支持在恢复前补全 session 身份的 provider 扩展接口。
type resumeSessionIdentityResolver interface {
	ResolveResumeSessionIdentity(ctx context.Context, req dto.ResumeSessionRequest) (dto.ResumeSessionRequest, error)
}

// autoResumePlan 保存 auto-resume 所需的 driver 与请求，确保校验完成后再触发 provider 调用。
type autoResumePlan struct {
	driver contract.Driver
	req    dto.ResumeSessionRequest
}

// buildAutoResumePlan 从持久化 binding 构造恢复计划，任何关键字段缺失都会阻断恢复。
func (r *sessionResolver) buildAutoResumePlan(binding *contract.SessionBinding, runtimeConfig map[string]any, promptSnapshot contract.PromptAssemblySnapshot, publicThreadID ...string) (autoResumePlan, error) {
	provider, err := autoResumeBindingProvider(binding)
	if err != nil {
		return autoResumePlan{}, err
	}
	providerThreadID, err := recoverableAutoResumeProviderThreadID(binding)
	if err != nil {
		logUnrecoverableAutoResumeBinding(binding, provider, err)
		return autoResumePlan{}, autoResumeRecoveryError(err)
	}
	driver, err := r.autoResumeDriver(provider)
	if err != nil {
		return autoResumePlan{}, err
	}
	cwd, err := autoResumeBindingCWD(binding)
	if err != nil {
		return autoResumePlan{}, err
	}
	req := buildAutoResumeRequest(binding, runtimeConfig, promptSnapshot, provider, providerThreadID, cwd, publicThreadID)
	if err := contract.ValidateResumePromptSnapshot(req.PromptSnapshot); err != nil {
		return autoResumePlan{}, fmt.Errorf("resolve session: auto-resume prompt snapshot: %w", err)
	}
	return autoResumePlan{driver: driver, req: req}, nil
}

// autoResumeRecoveryError 只把明确 artifact missing 映射为 session not found。
func autoResumeRecoveryError(err error) error {
	if errors.Is(err, providerrecovery.ErrNotFound) {
		return fmt.Errorf("resolve session: provider recovery missing: %w", contract.ErrSessionNotFound)
	}
	return fmt.Errorf("resolve session: provider recovery failed: %w", err)
}

// autoResumeBindingProvider 读取 binding 中的 provider 名称，缺失时返回可见错误。
func autoResumeBindingProvider(binding *contract.SessionBinding) (string, error) {
	if binding == nil {
		return "", contract.ErrSessionNotFound
	}
	provider := strings.TrimSpace(binding.Provider)
	if provider == "" {
		return "", fmt.Errorf("resolve session: binding for %q has no provider", binding.AgentID)
	}
	return provider, nil
}

// autoResumeDriver 从 registry 解析恢复用 driver，registry 未接入时直接 fail-fast。
func (r *sessionResolver) autoResumeDriver(provider string) (contract.Driver, error) {
	if r.registry == nil {
		return nil, fmt.Errorf("resolve session: no driver registry available")
	}
	driver, err := r.registry.Resolve(provider)
	if err != nil {
		return nil, fmt.Errorf("resolve session: %w", err)
	}
	return driver, nil
}

// buildAutoResumeRequest 组装 provider 恢复请求，并复制 runtimeConfig 防止后续修改污染输入。
func buildAutoResumeRequest(binding *contract.SessionBinding, runtimeConfig map[string]any, promptSnapshot contract.PromptAssemblySnapshot, provider, providerThreadID, cwd string, publicThreadID []string) dto.ResumeSessionRequest {
	codexHome, codexInstanceKey, codexModelProvider := autoResumeCodexIdentityFields(binding, runtimeConfig)
	return dto.ResumeSessionRequest{
		Provider:           provider,
		AgentID:            binding.AgentID,
		ThreadID:           autoResumePublicThreadID(publicThreadID),
		ProviderThreadID:   providerThreadID,
		CWD:                cwd,
		Config:             clone.RuntimeConfigMap(runtimeConfig),
		PromptSnapshot:     cloneAutoResumePromptSnapshot(promptSnapshot),
		CodexHome:          codexHome,
		CodexInstanceKey:   codexInstanceKey,
		CodexModelProvider: codexModelProvider,
	}
}

func cloneAutoResumePromptSnapshot(snapshot contract.PromptAssemblySnapshot) dto.PromptAssemblySnapshot {
	return dto.PromptAssemblySnapshot{
		DisplayName:           strings.TrimSpace(snapshot.DisplayName),
		BaseInstructions:      strings.TrimSpace(snapshot.BaseInstructions),
		Boundary:              cloneAutoResumePromptBoundary(snapshot.Boundary),
		DeveloperInstructions: strings.TrimSpace(snapshot.DeveloperInstructions),
		Provider:              strings.TrimSpace(snapshot.Provider),
		Version:               snapshot.Version,
		Hash:                  strings.TrimSpace(snapshot.Hash),
		SectionSnapshot:       clone.StringMap(snapshot.SectionSnapshot),
		Generation:            snapshot.Generation,
	}
}

func cloneAutoResumePromptBoundary(boundary *dto.PromptAssemblyBoundary) *dto.PromptAssemblyBoundary {
	if boundary == nil {
		return nil
	}
	return &dto.PromptAssemblyBoundary{
		CachedPrefix: strings.TrimSpace(boundary.CachedPrefix),
		UncachedTail: strings.TrimSpace(boundary.UncachedTail),
	}
}

// autoResumePublicThreadID 选择对外可见 thread ID，避免把内部路由键误传给 provider。
func autoResumePublicThreadID(candidates []string) string {
	// 这里不能回退到 CodexThreadID；它是路由键，可能只是 agent 占位 ID。
	// 为空时交给 driver 派生临时 thread ID，避免恢复到错误线程。
	for _, candidate := range candidates {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// logUnrecoverableAutoResumeBinding 记录无法恢复的 binding 快照，方便排查历史文件或线程 ID 损坏。
func logUnrecoverableAutoResumeBinding(binding *contract.SessionBinding, provider string, err error) {
	pkglogger.Warn("resolve session: provider thread is not recoverable",
		"agent_id", binding.AgentID,
		"provider", provider,
		"provider_thread_id", binding.ProviderThreadID,
		"session_uuid", binding.SessionUUID,
		"rollout_path", binding.RolloutPath,
		"error", err)
}

// logAutoResumeStart 在真正调用 provider 前记录恢复输入，便于重启恢复问题定位。
func logAutoResumeStart(binding *contract.SessionBinding, req dto.ResumeSessionRequest) {
	pkglogger.Warn("resolve session: auto-resume binding snapshot from DB",
		"agent_id", binding.AgentID,
		"provider", req.Provider,
		"provider_thread_id", req.ProviderThreadID,
		"codex_thread_id", binding.CodexThreadID,
		"rollout_path", binding.RolloutPath,
		"session_uuid", binding.SessionUUID,
		"cwd", binding.Cwd,
		"created_at", binding.CreatedAt,
	)
	pkglogger.Info("resolve session: auto-resuming after restart",
		"agent_id", binding.AgentID,
		"provider", req.Provider,
		"provider_thread_id", req.ProviderThreadID,
	)
}

// resolveAutoResumeSessionIdentity 让支持扩展接口的 driver 在恢复前补全 provider 私有身份。
func resolveAutoResumeSessionIdentity(ctx context.Context, driver contract.Driver, req dto.ResumeSessionRequest) (dto.ResumeSessionRequest, error) {
	resolver, ok := driver.(resumeSessionIdentityResolver)
	if !ok {
		return req, nil
	}
	return resolver.ResolveResumeSessionIdentity(ctx, req)
}

// backfillAutoResumeCodexIdentity 在 Codex 恢复成功后补写缺失身份，失败时由调用方停止新 session。
func (r *sessionResolver) backfillAutoResumeCodexIdentity(ctx context.Context, binding *contract.SessionBinding, req dto.ResumeSessionRequest, session contract.Session) error {
	if !r.canBackfillAutoResumeCodexIdentity(binding) {
		return nil
	}
	home, instanceKey, modelProvider := completeAutoResumeCodexIdentity(req, session)
	if !hasCompleteCodexIdentity(home, instanceKey, modelProvider) {
		return nil
	}
	repaired := bindingWithCodexIdentity(binding, home, instanceKey, modelProvider)
	return r.bindingWriter.UpsertSessionBinding(ctx, repaired)
}

// canBackfillAutoResumeCodexIdentity 判断当前 binding 是否允许补写 Codex 身份字段。
func (r *sessionResolver) canBackfillAutoResumeCodexIdentity(binding *contract.SessionBinding) bool {
	if r == nil || r.bindingWriter == nil || binding == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(binding.Provider), "codex")
}

// completeAutoResumeCodexIdentity 优先使用请求身份，缺失时再从恢复后的 session 快照读取。
func completeAutoResumeCodexIdentity(req dto.ResumeSessionRequest, session contract.Session) (string, string, string) {
	home, instanceKey, modelProvider := resumeRequestCodexIdentity(req)
	if hasCompleteCodexIdentity(home, instanceKey, modelProvider) {
		return home, instanceKey, modelProvider
	}
	return sessionCodexIdentity(session)
}

// bindingWithCodexIdentity 复制 binding 后补齐 Codex 身份，避免直接改动调用方持有的结构体。
func bindingWithCodexIdentity(binding *contract.SessionBinding, home, instanceKey, modelProvider string) contract.SessionBinding {
	repaired := *binding
	repaired.CodexHome = home
	repaired.CodexInstanceKey = instanceKey
	repaired.CodexModelProvider = modelProvider
	if repaired.CreatedAt == 0 {
		repaired.CreatedAt = time.Now().Unix()
	}
	return repaired
}

// resumeRequestCodexIdentity 从恢复请求中读取已知 Codex 身份字段。
func resumeRequestCodexIdentity(req dto.ResumeSessionRequest) (string, string, string) {
	return strings.TrimSpace(req.CodexHome),
		strings.TrimSpace(req.CodexInstanceKey),
		strings.TrimSpace(req.CodexModelProvider)
}

// sessionCodexIdentity 从 session 的 runtime config 快照中读取 Codex 身份字段。
func sessionCodexIdentity(session contract.Session) (string, string, string) {
	reader, ok := session.(interface{ RuntimeConfigSnapshot() map[string]any })
	if !ok {
		return "", "", ""
	}
	cfg := reader.RuntimeConfigSnapshot()
	if len(cfg) == 0 {
		return "", "", ""
	}
	return configString(cfg, contract.CodexHomeKey),
		configString(cfg, contract.CodexInstanceKeyKey),
		configString(cfg, contract.CodexModelProviderKey)
}

// hasCompleteCodexIdentity 判断 Codex 恢复所需身份字段是否全部存在。
func hasCompleteCodexIdentity(home, instanceKey, modelProvider string) bool {
	return strings.TrimSpace(home) != "" &&
		strings.TrimSpace(instanceKey) != "" &&
		strings.TrimSpace(modelProvider) != ""
}

// configString 从 runtime config 中读取字符串字段，非字符串值按缺失处理。
func configString(config map[string]any, key string) string {
	value, _ := config[strings.TrimSpace(key)].(string)
	return strings.TrimSpace(value)
}

// autoResumeCodexIdentityFields 按 binding 优先、runtimeConfig 兜底的顺序读取 Codex 身份。
func autoResumeCodexIdentityFields(binding *contract.SessionBinding, runtimeConfig map[string]any) (string, string, string) {
	if binding == nil {
		return "", "", ""
	}
	return firstAutoResumeString(binding.CodexHome, runtimeConfig, contract.CodexHomeKey, "codex_home"),
		firstAutoResumeString(binding.CodexInstanceKey, runtimeConfig, contract.CodexInstanceKeyKey, "codex_instance_key"),
		firstAutoResumeString(binding.CodexModelProvider, runtimeConfig, contract.CodexModelProviderKey, "codex_model_provider")
}

// firstAutoResumeString 从 binding 值和 runtimeConfig 候选键中选择第一个非空字符串。
func firstAutoResumeString(bindingValue string, runtimeConfig map[string]any, runtimeKeys ...string) string {
	if text := strings.TrimSpace(bindingValue); text != "" {
		return text
	}
	for _, key := range runtimeKeys {
		raw, ok := runtimeConfig[strings.TrimSpace(key)]
		if !ok {
			continue
		}
		text, ok := raw.(string)
		if !ok {
			continue
		}
		if text = strings.TrimSpace(text); text != "" {
			return text
		}
	}
	return ""
}

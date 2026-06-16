package unified

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type resumeSessionIdentityResolver interface {
	ResolveResumeSessionIdentity(ctx context.Context, req dto.ResumeSessionRequest) (dto.ResumeSessionRequest, error)
}

type autoResumePlan struct {
	driver contract.Driver
	req    dto.ResumeSessionRequest
}

func (r *sessionResolver) buildAutoResumePlan(binding *contract.SessionBinding, runtimeConfig map[string]any, publicThreadID ...string) (autoResumePlan, error) {
	provider, err := autoResumeBindingProvider(binding)
	if err != nil {
		return autoResumePlan{}, err
	}
	providerThreadID, err := recoverableAutoResumeProviderThreadID(binding)
	if err != nil {
		logUnrecoverableAutoResumeBinding(binding, provider, err)
		return autoResumePlan{}, contract.ErrSessionNotFound
	}
	driver, err := r.autoResumeDriver(provider)
	if err != nil {
		return autoResumePlan{}, err
	}
	cwd, err := autoResumeBindingCWD(binding)
	if err != nil {
		return autoResumePlan{}, err
	}
	req := buildAutoResumeRequest(binding, runtimeConfig, provider, providerThreadID, cwd, publicThreadID)
	return autoResumePlan{driver: driver, req: req}, nil
}

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

func buildAutoResumeRequest(binding *contract.SessionBinding, runtimeConfig map[string]any, provider, providerThreadID, cwd string, publicThreadID []string) dto.ResumeSessionRequest {
	codexHome, codexInstanceKey, codexModelProvider := autoResumeCodexIdentityFields(binding, runtimeConfig)
	return dto.ResumeSessionRequest{
		Provider:           provider,
		AgentID:            binding.AgentID,
		ThreadID:           autoResumePublicThreadID(publicThreadID),
		ProviderThreadID:   providerThreadID,
		CWD:                cwd,
		Config:             kernel.CloneRuntimeConfigMap(runtimeConfig),
		CodexHome:          codexHome,
		CodexInstanceKey:   codexInstanceKey,
		CodexModelProvider: codexModelProvider,
	}
}

func autoResumePublicThreadID(candidates []string) string {
	// Do not fall back to CodexThreadID here; it is a routing key and may be an
	// agent placeholder ID. Drivers can derive a synthetic thread ID if empty.
	for _, candidate := range candidates {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func logUnrecoverableAutoResumeBinding(binding *contract.SessionBinding, provider string, err error) {
	pkglogger.Warn("resolve session: provider thread is not recoverable",
		"agent_id", binding.AgentID,
		"provider", provider,
		"provider_thread_id", binding.ProviderThreadID,
		"session_uuid", binding.SessionUUID,
		"rollout_path", binding.RolloutPath,
		"error", err)
}

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

func resolveAutoResumeSessionIdentity(ctx context.Context, driver contract.Driver, req dto.ResumeSessionRequest) (dto.ResumeSessionRequest, error) {
	resolver, ok := driver.(resumeSessionIdentityResolver)
	if !ok {
		return req, nil
	}
	return resolver.ResolveResumeSessionIdentity(ctx, req)
}

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

func (r *sessionResolver) canBackfillAutoResumeCodexIdentity(binding *contract.SessionBinding) bool {
	if r == nil || r.bindingWriter == nil || binding == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(binding.Provider), "codex")
}

func completeAutoResumeCodexIdentity(req dto.ResumeSessionRequest, session contract.Session) (string, string, string) {
	home, instanceKey, modelProvider := resumeRequestCodexIdentity(req)
	if hasCompleteCodexIdentity(home, instanceKey, modelProvider) {
		return home, instanceKey, modelProvider
	}
	return sessionCodexIdentity(session)
}

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

func resumeRequestCodexIdentity(req dto.ResumeSessionRequest) (string, string, string) {
	return strings.TrimSpace(req.CodexHome),
		strings.TrimSpace(req.CodexInstanceKey),
		strings.TrimSpace(req.CodexModelProvider)
}

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

func hasCompleteCodexIdentity(home, instanceKey, modelProvider string) bool {
	return strings.TrimSpace(home) != "" &&
		strings.TrimSpace(instanceKey) != "" &&
		strings.TrimSpace(modelProvider) != ""
}

func configString(config map[string]any, key string) string {
	value, _ := config[strings.TrimSpace(key)].(string)
	return strings.TrimSpace(value)
}

func autoResumeCodexIdentityFields(binding *contract.SessionBinding, runtimeConfig map[string]any) (string, string, string) {
	if binding == nil {
		return "", "", ""
	}
	return firstAutoResumeString(binding.CodexHome, runtimeConfig, contract.CodexHomeKey, "codex_home"),
		firstAutoResumeString(binding.CodexInstanceKey, runtimeConfig, contract.CodexInstanceKeyKey, "codex_instance_key"),
		firstAutoResumeString(binding.CodexModelProvider, runtimeConfig, contract.CodexModelProviderKey, "codex_model_provider")
}

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

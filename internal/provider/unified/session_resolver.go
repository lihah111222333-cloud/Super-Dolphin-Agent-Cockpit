package unified

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/util/clone"
	"github.com/anthropic-ai/super-agent-v3/internal/util/historyjsonl"
	"github.com/anthropic-ai/super-agent-v3/internal/util/identifier"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type driverRegistry interface {
	Resolve(provider string) (contract.Driver, error)
	Names() []string
}

type sessionResolver struct {
	threadStore  contract.SessionThreadLookup
	bindingStore contract.SessionBindingLookup
	registry     driverRegistry
	sessions     *SessionManager
}

var _ contract.SessionResolver = (*sessionResolver)(nil)

// NewSessionResolver 创建会话解析器。
func NewSessionResolver(
	threadStore contract.SessionThreadLookup,
	bindingStore contract.SessionBindingLookup,
	registry *Registry,
	sessions *SessionManager,
) contract.SessionResolver {
	return &sessionResolver{
		threadStore:  threadStore,
		bindingStore: bindingStore,
		registry:     registry,
		sessions:     sessions,
	}
}

// ResolveSession 解析会话。
func (r *sessionResolver) ResolveSession(ctx context.Context, threadID string) (contract.Session, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, fmt.Errorf("resolve session: thread id is required")
	}
	if r.sessions == nil {
		return nil, fmt.Errorf("resolve session: session manager is not configured")
	}
	if session, ok := r.tryExistingSession(threadID); ok {
		return session, nil
	}
	session, errs := r.tryCreateSession(ctx, threadID)
	if session != nil {
		return session, nil
	}
	return nil, r.resolveLookupError(threadID, errs)
}

func (r *sessionResolver) tryExistingSession(threadID string) (contract.Session, bool) {
	// Preserve V2's cheapest reuse path when the caller already passes agent_id.
	session, err := r.sessions.Get(threadID)
	return session, err == nil
}

// "Create" here means recovering the active session through durable thread bindings
// after the direct agent-ID lookup misses; it does not construct a new runtime session.
// tryCreateSession 处理trycreate会话。
func (r *sessionResolver) tryCreateSession(ctx context.Context, threadID string) (contract.Session, []error) {
	errs := make([]error, 0, 2)
	if session, err := r.resolveThreadSession(ctx, threadID); err == nil {
		return session, nil
	} else if !platformdb.IsNotFound(err) {
		errs = append(errs, err)
	}
	if session, err := r.resolveProviderThreadSession(ctx, threadID); err == nil {
		return session, nil
	} else if !platformdb.IsNotFound(err) && !errors.Is(err, contract.ErrSessionNotFound) {
		errs = append(errs, err)
	}
	return nil, errs
}

func (r *sessionResolver) resolveLookupError(threadID string, errs []error) error {
	if r.threadStore == nil && r.bindingStore == nil {
		return fmt.Errorf("resolve session: no thread lookup backend is configured")
	}
	if len(errs) > 0 {
		return fmt.Errorf("resolve session: thread %q: %w", threadID, errors.Join(errs...))
	}
	return fmt.Errorf("resolve session: thread %q not found", threadID)
}

// resolveThreadSession 解析线程会话。
func (r *sessionResolver) resolveThreadSession(ctx context.Context, threadID string) (contract.Session, error) {
	if r.threadStore == nil {
		return nil, platformdb.ErrNotFound
	}
	ref, err := r.threadStore.GetByThreadID(ctx, threadID)
	if err != nil {
		return nil, err
	}
	if ref == nil {
		return nil, platformdb.ErrNotFound
	}
	if err := rejectAutoResumeLifecycle(ref); err != nil {
		return nil, err
	}
	agentID := strings.TrimSpace(ref.AgentID)
	if agentID == "" {
		return nil, fmt.Errorf("resolve session: thread %q has no agent id", threadID)
	}
	if session, err := r.sessions.Get(agentID); err == nil {
		return session, nil
	}
	// Session not in memory (e.g. after restart). Look up the binding to
	// get the provider thread UUID and auto-resume.
	if r.bindingStore == nil {
		return nil, contract.ErrSessionNotFound
	}
	binding, err := r.bindingStore.GetByAgentID(ctx, agentID)
	if err != nil {
		if !platformdb.IsNotFound(err) {
			return nil, err
		}
		return nil, contract.ErrSessionNotFound
	}
	return r.autoResumeSession(ctx, binding, ref.RuntimeConfig, ref.ThreadID, threadID)
}

// resolveProviderThreadSession 解析provider线程会话。
func (r *sessionResolver) resolveProviderThreadSession(ctx context.Context, threadID string) (contract.Session, error) {
	if r.bindingStore == nil {
		return nil, platformdb.ErrNotFound
	}
	var errs []error
	for _, provider := range r.providerNames() {
		binding, err := r.bindingStore.GetByProviderThread(ctx, provider, threadID)
		if err != nil {
			if !platformdb.IsNotFound(err) {
				errs = append(errs, fmt.Errorf("provider %q: %w", provider, err))
			}
			continue
		}
		if err := r.rejectBindingAutoResumeLifecycle(ctx, binding); err != nil {
			return nil, err
		}
		agentID := strings.TrimSpace(binding.AgentID)
		if agentID == "" {
			errs = append(errs, fmt.Errorf("resolve session: provider %q thread %q has no agent id", provider, threadID))
			continue
		}
		if session, err := r.sessions.Get(agentID); err == nil {
			return session, nil
		}
		// Memory miss — auto-resume from binding.
		return r.autoResumeSession(ctx, binding, r.lookupAutoResumeRuntimeConfig(ctx, binding))
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return nil, platformdb.ErrNotFound
}

// autoResumeSession rebuilds a runtime session from a persisted binding.
// This is the key recovery path after application restart: the DB has the
// thread UUID but the in-memory SessionManager is empty.
// autoResumeSession 处理auto恢复会话。
func (r *sessionResolver) autoResumeSession(ctx context.Context, binding *contract.SessionBinding, runtimeConfig map[string]any, publicThreadID ...string) (contract.Session, error) {
	if binding == nil {
		return nil, contract.ErrSessionNotFound
	}
	provider := strings.TrimSpace(binding.Provider)
	if provider == "" {
		return nil, fmt.Errorf("resolve session: binding for %q has no provider", binding.AgentID)
	}
	// Reject bindings whose UUID is not backed by a provider CLI history file.
	// A DB-only UUID can be an unmaterialized placeholder; sending it into the
	// driver used to end in resume failure/ForceStop and surface as WS 1006.
	// Returning ErrSessionNotFound lets the caller take the fresh-start path.
	providerThreadID, err := recoverableAutoResumeProviderThreadID(binding)
	if err != nil {
		pkglogger.Warn("resolve session: provider thread is not recoverable",
			"agent_id", binding.AgentID,
			"provider", provider,
			"provider_thread_id", binding.ProviderThreadID,
			"session_uuid", binding.SessionUUID,
			"rollout_path", binding.RolloutPath,
			"error", err)
		return nil, contract.ErrSessionNotFound
	}
	if r.registry == nil {
		return nil, fmt.Errorf("resolve session: no driver registry available")
	}
	driver, err := r.registry.Resolve(provider)
	if err != nil {
		return nil, fmt.Errorf("resolve session: %w", err)
	}
	cwd, err := autoResumeBindingCWD(binding)
	if err != nil {
		return nil, err
	}

	threadID := ""
	for _, candidate := range publicThreadID {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			threadID = trimmed
			break
		}
	}
	// Note: deliberately do NOT fall back to binding.CodexThreadID here.
	// CodexThreadID is a routing key (often an agent placeholder ID) and
	// passing it as req.ThreadID let placeholders cross provider boundaries
	// into claude driver, where it caused the 5s system:init deadlock.
	// req.ThreadID may be empty; the driver derives a synthetic ID itself.

	req := dto.ResumeSessionRequest{
		Provider:           provider,
		AgentID:            binding.AgentID,
		ThreadID:           threadID,
		ProviderThreadID:   providerThreadID,
		CWD:                cwd,
		Config:             clone.RuntimeConfigMap(runtimeConfig),
		CodexHome:          binding.CodexHome,
		CodexInstanceKey:   binding.CodexInstanceKey,
		CodexModelProvider: binding.CodexModelProvider,
	}
	pkglogger.Warn("resolve session: auto-resume binding snapshot from DB",
		"agent_id", binding.AgentID,
		"provider", provider,
		"provider_thread_id", providerThreadID,
		"codex_thread_id", binding.CodexThreadID,
		"rollout_path", binding.RolloutPath,
		"session_uuid", binding.SessionUUID,
		"cwd", binding.Cwd,
		"created_at", binding.CreatedAt,
	)
	pkglogger.Info("resolve session: auto-resuming after restart",
		"agent_id", binding.AgentID,
		"provider", provider,
		"provider_thread_id", providerThreadID,
	)
	session, err := driver.ResumeSession(ctx, req)
	if err != nil {
		pkglogger.Warn("resolve session: auto-resume failed",
			"agent_id", binding.AgentID, "error", err)
		return nil, fmt.Errorf("resolve session: auto-resume failed: %w", err)
	}
	r.sessions.Register(binding.AgentID, session)
	pkglogger.Info("resolve session: auto-resume succeeded",
		"agent_id", binding.AgentID,
		"thread_id", session.ThreadID(),
	)
	return session, nil
}

// lookupAutoResumeRuntimeConfig 处理lookupauto恢复运行时配置。
func (r *sessionResolver) lookupAutoResumeRuntimeConfig(ctx context.Context, binding *contract.SessionBinding) map[string]any {
	if r == nil || r.threadStore == nil || binding == nil {
		return nil
	}
	for _, candidate := range []string{binding.CodexThreadID, binding.AgentID} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		ref, err := r.threadStore.GetByThreadID(ctx, candidate)
		if err != nil || ref == nil {
			continue
		}
		if len(ref.RuntimeConfig) > 0 {
			return clone.RuntimeConfigMap(ref.RuntimeConfig)
		}
	}
	return nil
}

// rejectBindingAutoResumeLifecycle 处理rejectbindingauto恢复生命周期。
func (r *sessionResolver) rejectBindingAutoResumeLifecycle(ctx context.Context, binding *contract.SessionBinding) error {
	if r == nil || r.threadStore == nil || binding == nil {
		return nil
	}
	for _, candidate := range []string{binding.CodexThreadID, binding.AgentID} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		ref, err := r.threadStore.GetByThreadID(ctx, candidate)
		if err != nil {
			if platformdb.IsNotFound(err) {
				continue
			}
			return err
		}
		if err := rejectAutoResumeLifecycle(ref); err != nil {
			return err
		}
	}
	return nil
}

func rejectAutoResumeLifecycle(ref *contract.SessionThreadRef) error {
	if ref == nil {
		return nil
	}
	switch status := strings.TrimSpace(ref.Status); status {
	case "stopped", "archived":
		return fmt.Errorf("resolve session: thread %q is %s", strings.TrimSpace(ref.ThreadID), status)
	default:
		return nil
	}
}

func autoResumeBindingCWD(binding *contract.SessionBinding) (string, error) {
	if binding == nil {
		return "", contract.ErrSessionNotFound
	}
	cwd := strings.TrimSpace(binding.Cwd)
	if cwd == "" || cwd == "." {
		return "", fmt.Errorf("resolve session: auto-resume cwd is required for agent %q", strings.TrimSpace(binding.AgentID))
	}
	return cwd, nil
}

// recoverableAutoResumeProviderThreadID 处理recoverableauto恢复provider线程ID。
func recoverableAutoResumeProviderThreadID(binding *contract.SessionBinding) (string, error) {
	if binding == nil {
		return "", contract.ErrSessionNotFound
	}
	var lastErr error
	for _, candidate := range []string{binding.ProviderThreadID, binding.SessionUUID} {
		providerThreadID := strings.TrimSpace(candidate)
		if !identifier.LooksLikeUUID(providerThreadID) {
			continue
		}
		if _, err := historyjsonl.ExistingProviderPath(historyjsonl.ReadRequest{
			Provider:         binding.Provider,
			RolloutPath:      binding.RolloutPath,
			ThreadID:         binding.CodexThreadID,
			ProviderThreadID: providerThreadID,
			SessionUUID:      providerThreadID,
			CodexHome:        binding.CodexHome,
		}); err != nil {
			lastErr = err
			continue
		}
		return providerThreadID, nil
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", contract.ErrSessionNotFound
}

// providerNames 处理provider名称。
func (r *sessionResolver) providerNames() []string {
	names := []string(nil)
	if r.registry != nil {
		names = append(names, r.registry.Names()...)
	}
	if len(names) == 0 {
		names = []string{"codex", "claude"}
	}
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, raw := range names {
		name := normalizeProviderName(raw)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

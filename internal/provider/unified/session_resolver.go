package unified

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
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
		return nil, contract.ErrSessionNotFound
	}
	return r.autoResumeSession(ctx, binding, ref.ThreadID, threadID)
}

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
		agentID := strings.TrimSpace(binding.AgentID)
		if agentID == "" {
			errs = append(errs, fmt.Errorf("resolve session: provider %q thread %q has no agent id", provider, threadID))
			continue
		}
		if session, err := r.sessions.Get(agentID); err == nil {
			return session, nil
		}
		// Memory miss — auto-resume from binding.
		return r.autoResumeSession(ctx, binding)
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return nil, platformdb.ErrNotFound
}

// autoResumeSession rebuilds a runtime session from a persisted binding.
// This is the key recovery path after application restart: the DB has the
// thread UUID but the in-memory SessionManager is empty.
func (r *sessionResolver) autoResumeSession(ctx context.Context, binding *contract.SessionBinding, publicThreadID ...string) (contract.Session, error) {
	if binding == nil {
		return nil, contract.ErrSessionNotFound
	}
	provider := strings.TrimSpace(binding.Provider)
	if provider == "" {
		return nil, fmt.Errorf("resolve session: binding for %q has no provider", binding.AgentID)
	}
	if r.registry == nil {
		return nil, fmt.Errorf("resolve session: no driver registry available")
	}
	driver, err := r.registry.Resolve(provider)
	if err != nil {
		return nil, fmt.Errorf("resolve session: %w", err)
	}

	threadID := ""
	for _, candidate := range publicThreadID {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			threadID = trimmed
			break
		}
	}
	if threadID == "" {
		threadID = strings.TrimSpace(binding.CodexThreadID)
	}

	req := dto.ResumeSessionRequest{
		Provider:           provider,
		AgentID:            binding.AgentID,
		ThreadID:           threadID,
		ProviderThreadID:   binding.ProviderThreadID,
		CWD:                binding.Cwd,
		CodexHome:          binding.CodexHome,
		CodexInstanceKey:   binding.CodexInstanceKey,
		CodexModelProvider: binding.CodexModelProvider,
	}
	pkglogger.Warn("resolve session: auto-resume binding snapshot from DB",
		"agent_id", binding.AgentID,
		"provider", provider,
		"provider_thread_id", binding.ProviderThreadID,
		"codex_thread_id", binding.CodexThreadID,
		"rollout_path", binding.RolloutPath,
		"session_uuid", binding.SessionUUID,
		"cwd", binding.Cwd,
		"created_at", binding.CreatedAt,
	)
	pkglogger.Info("resolve session: auto-resuming after restart",
		"agent_id", binding.AgentID,
		"provider", provider,
		"provider_thread_id", binding.ProviderThreadID,
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

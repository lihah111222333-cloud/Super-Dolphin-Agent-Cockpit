package unified

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

type threadLookup interface {
	GetByThreadID(ctx context.Context, threadID string) (*threadstore.Thread, error)
}

type providerThreadLookup interface {
	GetByProviderThread(ctx context.Context, provider, providerThreadID string) (*bindingstore.Binding, error)
}

type providerNameSource interface {
	Names() []string
}

type sessionResolver struct {
	threadStore  threadLookup
	bindingStore providerThreadLookup
	registry     providerNameSource
	sessions     *SessionManager
}

var _ contract.SessionResolver = (*sessionResolver)(nil)

func NewSessionResolver(
	threadStore threadstore.Store,
	bindingStore bindingstore.Store,
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
	return r.sessions.Get(agentID)
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
		return r.sessions.Get(agentID)
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return nil, platformdb.ErrNotFound
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

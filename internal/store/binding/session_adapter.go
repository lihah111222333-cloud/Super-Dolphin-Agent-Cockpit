package binding

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

var _ contract.SessionBindingLookup = (*sessionBindingAdapter)(nil)

type sessionBindingAdapter struct {
	store Store
}

// NewSessionBindingLookup adapts the concrete binding Store to the narrow
// contract.SessionBindingLookup interface consumed by the session resolver.
// NewSessionBindingLookup 创建会话bindinglookup。
func NewSessionBindingLookup(store Store) contract.SessionBindingLookup {
	if store == nil {
		return nil
	}
	return &sessionBindingAdapter{store: store}
}

// GetByProviderThread 按provider线程读取binding存储。
func (a *sessionBindingAdapter) GetByProviderThread(ctx context.Context, provider, providerThreadID string) (*contract.SessionBinding, error) {
	binding, err := a.store.GetByProviderThread(ctx, provider, providerThreadID)
	if err != nil || binding == nil {
		return nil, err
	}
	return mapSessionBinding(binding), nil
}

// GetByAgentID 按代理ID读取binding存储。
func (a *sessionBindingAdapter) GetByAgentID(ctx context.Context, agentID string) (*contract.SessionBinding, error) {
	binding, err := a.store.GetByAgentID(ctx, agentID)
	if err != nil || binding == nil {
		return nil, err
	}
	return mapSessionBinding(binding), nil
}

func mapSessionBinding(b *Binding) *contract.SessionBinding {
	if b == nil {
		return nil
	}
	return &contract.SessionBinding{
		AgentID:            b.AgentID,
		Provider:           b.Provider,
		ProviderThreadID:   b.ProviderThreadID,
		CodexThreadID:      b.CodexThreadID,
		RolloutPath:        b.RolloutPath,
		SessionUUID:        b.SessionUUID,
		Cwd:                b.Cwd,
		CreatedAt:          b.CreatedAt,
		CodexHome:          b.CodexHome,
		CodexInstanceKey:   b.CodexInstanceKey,
		CodexModelProvider: b.CodexModelProvider,
	}
}

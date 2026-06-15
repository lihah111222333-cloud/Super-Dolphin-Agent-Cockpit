package binding

import (
	"context"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

var _ contract.SessionBindingLookup = (*sessionBindingAdapter)(nil)
var _ contract.SessionBindingUpserter = (*sessionBindingAdapter)(nil)

type sessionBindingAdapter struct {
	store Store
}

// NewSessionBindingLookup adapts the concrete binding Store to the narrow
// contract.SessionBindingLookup interface consumed by the session resolver.
func NewSessionBindingLookup(store Store) contract.SessionBindingLookup {
	if store == nil {
		return nil
	}
	return &sessionBindingAdapter{store: store}
}

func NewSessionBindingUpserter(store Store) contract.SessionBindingUpserter {
	if store == nil {
		return nil
	}
	return &sessionBindingAdapter{store: store}
}

func (a *sessionBindingAdapter) GetByProviderThread(ctx context.Context, provider, providerThreadID string) (*contract.SessionBinding, error) {
	binding, err := a.store.GetByProviderThread(ctx, provider, providerThreadID)
	if err != nil || binding == nil {
		return nil, err
	}
	return mapSessionBinding(binding), nil
}

func (a *sessionBindingAdapter) GetByAgentID(ctx context.Context, agentID string) (*contract.SessionBinding, error) {
	binding, err := a.store.GetByAgentID(ctx, agentID)
	if err != nil || binding == nil {
		return nil, err
	}
	return mapSessionBinding(binding), nil
}

func (a *sessionBindingAdapter) UpsertSessionBinding(ctx context.Context, binding contract.SessionBinding) error {
	createdAt := binding.CreatedAt
	if createdAt == 0 {
		createdAt = time.Now().Unix()
	}
	return a.store.Upsert(ctx, UpsertParams{
		AgentID:            binding.AgentID,
		Provider:           binding.Provider,
		ProviderThreadID:   binding.ProviderThreadID,
		CodexThreadID:      binding.CodexThreadID,
		RolloutPath:        binding.RolloutPath,
		SessionUUID:        binding.SessionUUID,
		Cwd:                binding.Cwd,
		ParentAgentID:      binding.ParentAgentID,
		AgentType:          binding.AgentType,
		AgentMemoryScope:   binding.AgentMemoryScope,
		CreatedAt:          createdAt,
		UpdatedAt:          time.Now().Unix(),
		CodexHome:          binding.CodexHome,
		CodexInstanceKey:   binding.CodexInstanceKey,
		CodexModelProvider: binding.CodexModelProvider,
	})
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
		ParentAgentID:      b.ParentAgentID,
		AgentType:          b.AgentType,
		AgentMemoryScope:   b.AgentMemoryScope,
		CreatedAt:          b.CreatedAt,
		CodexHome:          b.CodexHome,
		CodexInstanceKey:   b.CodexInstanceKey,
		CodexModelProvider: b.CodexModelProvider,
	}
}

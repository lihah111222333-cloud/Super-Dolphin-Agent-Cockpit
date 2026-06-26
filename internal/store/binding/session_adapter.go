package binding

import (
	"context"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

var _ contract.SessionBindingLookup = (*sessionBindingAdapter)(nil)
var _ contract.SessionBindingUpserter = (*sessionBindingAdapter)(nil)

// sessionBindingAdapter 把 binding store 适配为 provider 会话恢复所需的窄接口。
type sessionBindingAdapter struct {
	store Store
}

// NewSessionBindingLookup 把具体 Store 暴露为 session resolver 只读 binding 查询接口。
func NewSessionBindingLookup(store Store) contract.SessionBindingLookup {
	if store == nil {
		return nil
	}
	return &sessionBindingAdapter{store: store}
}

// NewSessionBindingUpserter 把具体 Store 暴露为 auto-resume 身份回填所需的写入接口。
func NewSessionBindingUpserter(store Store) contract.SessionBindingUpserter {
	if store == nil {
		return nil
	}
	return &sessionBindingAdapter{store: store}
}

// GetByProviderThread 按 provider 线程 ID 读取绑定，并转换为跨模块 contract DTO。
func (a *sessionBindingAdapter) GetByProviderThread(ctx context.Context, provider, providerThreadID string) (*contract.SessionBinding, error) {
	binding, err := a.store.GetByProviderThread(ctx, provider, providerThreadID)
	if err != nil || binding == nil {
		return nil, err
	}
	return mapSessionBinding(binding), nil
}

// GetByAgentID 按 agent ID 读取绑定，供公共 thread 路径恢复 provider session。
func (a *sessionBindingAdapter) GetByAgentID(ctx context.Context, agentID string) (*contract.SessionBinding, error) {
	binding, err := a.store.GetByAgentID(ctx, agentID)
	if err != nil || binding == nil {
		return nil, err
	}
	return mapSessionBinding(binding), nil
}

// UpsertSessionBinding 写入恢复后的 session binding，保留创建时间并刷新更新时间。
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

// mapSessionBinding 将 store 层 Binding 转成 provider/session contract 使用的 DTO。
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

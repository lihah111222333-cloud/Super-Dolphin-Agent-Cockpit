package thread

import (
	"context"
	"encoding/json"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

var _ contract.SessionThreadLookup = (*sessionThreadAdapter)(nil)

type sessionThreadAdapter struct {
	store Store
}

// NewSessionThreadLookup adapts the concrete thread Store to the narrow
// contract.SessionThreadLookup interface consumed by the session resolver.
// NewSessionThreadLookup 创建会话线程lookup。
func NewSessionThreadLookup(store Store) contract.SessionThreadLookup {
	if store == nil {
		return nil
	}
	return &sessionThreadAdapter{store: store}
}

// GetByThreadID 按线程ID读取线程存储。
func (a *sessionThreadAdapter) GetByThreadID(ctx context.Context, threadID string) (*contract.SessionThreadRef, error) {
	thread, err := a.store.GetByThreadID(ctx, threadID)
	if err != nil || thread == nil {
		return nil, err
	}
	return &contract.SessionThreadRef{
		ThreadID:      thread.ThreadID,
		AgentID:       thread.AgentID,
		Status:        thread.Status,
		RuntimeConfig: decodeSessionThreadRuntimeConfig(thread.ConfigOverride),
	}, nil
}

func decodeSessionThreadRuntimeConfig(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var stored struct {
		Runtime map[string]any `json:"runtime,omitempty"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil
	}
	if len(stored.Runtime) == 0 {
		return nil
	}
	out := make(map[string]any, len(stored.Runtime))
	for key, value := range stored.Runtime {
		out[key] = value
	}
	return out
}

package thread

import (
	"context"
	"encoding/json"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

var _ contract.SessionThreadLookup = (*sessionThreadAdapter)(nil)

// sessionThreadAdapter 将 thread.Store 裁剪成 session 模块只需要的线程查询口。
// RuntimeConfig 只从 config_override.runtime 提取，解析失败时保持 nil 以避免污染 session 恢复。
type sessionThreadAdapter struct {
	store Store
}

// NewSessionThreadLookup 创建会话线程 lookup 适配器，store 为空时不注册实现。
func NewSessionThreadLookup(store Store) contract.SessionThreadLookup {
	if store == nil {
		return nil
	}
	return &sessionThreadAdapter{store: store}
}

// GetByThreadID 读取 session 恢复所需的线程引用字段。
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

// decodeSessionThreadRuntimeConfig 从线程配置覆盖里提取 runtime 段。
// 非法 JSON 视为没有 runtime 配置，避免 session 查询路径因为历史脏数据崩溃。
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

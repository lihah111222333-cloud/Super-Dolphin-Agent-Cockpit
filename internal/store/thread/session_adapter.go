package thread

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

var _ contract.SessionThreadLookup = (*sessionThreadAdapter)(nil)

// sessionThreadAdapter 将 thread.Store 裁剪成 session 模块只需要的线程查询口。
// RuntimeConfig 只从 config_override.runtime 提取；解析失败必须阻断 session 恢复，避免读错运行时身份。
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
	runtimeConfig, err := decodeSessionThreadRuntimeConfig(thread.ConfigOverride)
	if err != nil {
		return nil, fmt.Errorf("thread session lookup: config_override.runtime for %q: %w", threadID, err)
	}
	promptSnapshot, err := a.store.LoadPromptSnapshot(ctx, thread.ThreadID)
	if err != nil {
		return nil, fmt.Errorf("thread session lookup: load prompt snapshot for %q: %w", thread.ThreadID, err)
	}
	return &contract.SessionThreadRef{
		ThreadID:       thread.ThreadID,
		AgentID:        thread.AgentID,
		Status:         thread.Status,
		RuntimeConfig:  runtimeConfig,
		PromptSnapshot: sessionPromptSnapshotFromStore(promptSnapshot),
	}, nil
}

// decodeSessionThreadRuntimeConfig 从线程配置覆盖里提取 runtime 段。
// 非法 JSON 直接返回错误，避免 auto-resume 在身份配置损坏时继续恢复。
func decodeSessionThreadRuntimeConfig(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var stored struct {
		Runtime map[string]any `json:"runtime,omitempty"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, err
	}
	if len(stored.Runtime) == 0 {
		return nil, nil
	}
	out := make(map[string]any, len(stored.Runtime))
	for key, value := range stored.Runtime {
		out[key] = value
	}
	return out, nil
}

func sessionPromptSnapshotFromStore(snapshot *PromptSnapshot) contract.PromptAssemblySnapshot {
	if snapshot == nil {
		return contract.PromptAssemblySnapshot{}
	}
	return contract.PromptAssemblySnapshot{
		DisplayName:           snapshot.DisplayName,
		BaseInstructions:      snapshot.BaseInstructions,
		Boundary:              sessionPromptBoundaryFromStore(snapshot.Boundary),
		DeveloperInstructions: snapshot.DeveloperInstructions,
		Provider:              snapshot.Provider,
		Version:               snapshot.Version,
		Hash:                  snapshot.Hash,
		SectionSnapshot:       cloneSessionPromptSectionSnapshot(snapshot.SectionSnapshot),
		Generation:            snapshot.Generation,
	}
}

func sessionPromptBoundaryFromStore(boundary *PromptBoundary) *contract.PromptAssemblyBoundary {
	if boundary == nil {
		return nil
	}
	return &contract.PromptAssemblyBoundary{
		CachedPrefix: boundary.CachedPrefix,
		UncachedTail: boundary.UncachedTail,
	}
}

func cloneSessionPromptSectionSnapshot(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

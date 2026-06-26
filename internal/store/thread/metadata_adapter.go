package thread

import (
	"context"
	"encoding/json"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

var _ contract.ThreadMetadataStore = (*metadataStoreAdapter)(nil)

// metadataStoreAdapter 将 thread.Store 裁剪成 contract.ThreadMetadataStore。
// 这里只暴露元数据字段，并在跨模块返回前复制 JSON，避免调用方改写 store 内部切片。
type metadataStoreAdapter struct {
	store Store
}

// NewMetadataStore 创建面向 contract 层的线程元数据存储适配器。
func NewMetadataStore(store Store) contract.ThreadMetadataStore {
	if store == nil {
		return nil
	}
	return &metadataStoreAdapter{store: store}
}

// GetByThreadID 按线程ID读取线程元数据；底层未找到或查询失败时直接透传 store 错误。
func (a *metadataStoreAdapter) GetByThreadID(ctx context.Context, threadID string) (*contract.ThreadMetadata, error) {
	thread, err := a.store.GetByThreadID(ctx, threadID)
	if err != nil || thread == nil {
		return nil, err
	}
	return mapThreadMetadata(thread), nil
}

// ListAll 列出所有线程元数据，供跨模块只读索引使用。
func (a *metadataStoreAdapter) ListAll(ctx context.Context) ([]contract.ThreadMetadata, error) {
	threads, err := a.store.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	return mapThreadMetadataList(threads), nil
}

// mapThreadMetadata 把完整线程记录裁剪成跨模块元数据 DTO。
func mapThreadMetadata(thread *Thread) *contract.ThreadMetadata {
	if thread == nil {
		return nil
	}
	return &contract.ThreadMetadata{
		ThreadID:         thread.ThreadID,
		ParentAgentID:    thread.ParentAgentID,
		AgentMemoryScope: thread.AgentMemoryScope,
		Cwd:              thread.Cwd,
		CreatedAt:        thread.CreatedAt,
		UpdatedAt:        thread.UpdatedAt,
		FinishedAt:       thread.FinishedAt,
		OwnerThreadID:    thread.OwnerThreadID,
		ConfigOverride:   cloneRawMessage(thread.ConfigOverride),
	}
}

// mapThreadMetadataList 批量转换线程元数据，并跳过空转换结果。
func mapThreadMetadataList(threads []Thread) []contract.ThreadMetadata {
	if len(threads) == 0 {
		return nil
	}
	out := make([]contract.ThreadMetadata, 0, len(threads))
	for idx := range threads {
		meta := mapThreadMetadata(&threads[idx])
		if meta != nil {
			out = append(out, *meta)
		}
	}
	return out
}

// cloneRawMessage 复制 JSON 原始字节，避免适配器调用方持有可变共享切片。
func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

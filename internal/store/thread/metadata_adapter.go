package thread

import (
	"context"
	"encoding/json"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

var _ contract.ThreadMetadataStore = (*metadataStoreAdapter)(nil)

type metadataStoreAdapter struct {
	store Store
}

// NewMetadataStore 创建元数据存储。
func NewMetadataStore(store Store) contract.ThreadMetadataStore {
	if store == nil {
		return nil
	}
	return &metadataStoreAdapter{store: store}
}

// GetByThreadID 按线程ID读取线程存储。
func (a *metadataStoreAdapter) GetByThreadID(ctx context.Context, threadID string) (*contract.ThreadMetadata, error) {
	thread, err := a.store.GetByThreadID(ctx, threadID)
	if err != nil || thread == nil {
		return nil, err
	}
	return mapThreadMetadata(thread), nil
}

// ListAll 列出all。
func (a *metadataStoreAdapter) ListAll(ctx context.Context) ([]contract.ThreadMetadata, error) {
	threads, err := a.store.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	return mapThreadMetadataList(threads), nil
}

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

func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

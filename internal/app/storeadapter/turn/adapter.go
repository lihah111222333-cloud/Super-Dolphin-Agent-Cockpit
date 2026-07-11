package turnadapter

import (
	"context"
	"errors"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/app/internal/storeguard"
	"github.com/anthropic-ai/super-agent-v3/internal/module/turn"
	turndedupe "github.com/anthropic-ai/super-agent-v3/internal/store/turndedupe"
)

type turnDedupeStoreAdapter struct {
	store turndedupe.Store
}

var _ turn.DedupeStore = (*turnDedupeStoreAdapter)(nil)

// provideTurnDedupeStore 保持 optional 语义，底层 Store 缺失或 typed nil 时返回 nil 领域端口。
func provideTurnDedupeStore(store turndedupe.Store) turn.DedupeStore {
	if storeguard.IsNil(store) {
		return nil
	}
	return &turnDedupeStoreAdapter{store: store}
}

// Upsert 将 turn 领域参数逐字段转换后写入 Store。
func (a *turnDedupeStoreAdapter) Upsert(ctx context.Context, params turn.DedupeUpsertParams) error {
	return a.store.Upsert(ctx, turndedupe.UpsertParams{
		DedupeKey:   params.DedupeKey,
		LocalTurnID: params.LocalTurnID,
		ThreadID:    params.ThreadID,
		Now:         params.Now,
	})
}

// BindProviderTurnID 将 provider turn ID 回写参数逐字段转换后写入 Store。
func (a *turnDedupeStoreAdapter) BindProviderTurnID(ctx context.Context, params turn.DedupeBindProviderTurnIDParams) error {
	return a.store.BindProviderTurnID(ctx, turndedupe.BindProviderTurnIDParams{
		DedupeKey:      params.DedupeKey,
		ProviderTurnID: params.ProviderTurnID,
		Now:            params.Now,
	})
}

// MarkTerminal 标记 dedupe key 已进入终态。
func (a *turnDedupeStoreAdapter) MarkTerminal(ctx context.Context, dedupeKey string, now time.Time) error {
	return a.store.MarkTerminal(ctx, dedupeKey, now)
}

// GetLive 读取 live registry 行，并只把 Store not-found 映射为 turn 领域 sentinel。
func (a *turnDedupeStoreAdapter) GetLive(ctx context.Context, dedupeKey string) (turn.DedupeEntry, error) {
	entry, err := a.store.GetLive(ctx, dedupeKey)
	if err != nil {
		if errors.Is(err, turndedupe.ErrNotFound) {
			return turn.DedupeEntry{}, turn.ErrDedupeNotFound
		}
		return turn.DedupeEntry{}, err
	}
	return turnDedupeEntryFromStore(entry), nil
}

// turnDedupeEntryFromStore 将 Store 行逐字段投影为 turn 领域 DTO。
func turnDedupeEntryFromStore(entry turndedupe.Entry) turn.DedupeEntry {
	return turn.DedupeEntry{
		DedupeKey:      entry.DedupeKey,
		LocalTurnID:    entry.LocalTurnID,
		ProviderTurnID: entry.ProviderTurnID,
		ThreadID:       entry.ThreadID,
		CreatedAt:      entry.CreatedAt,
		UpdatedAt:      entry.UpdatedAt,
		TerminalAt:     entry.TerminalAt,
	}
}

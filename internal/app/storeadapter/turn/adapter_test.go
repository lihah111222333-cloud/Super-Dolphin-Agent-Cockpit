package turnadapter

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/module/turn"
	turndedupe "github.com/anthropic-ai/super-agent-v3/internal/store/turndedupe"
	storeadaptertest "github.com/anthropic-ai/super-agent-v3/internal/testutil/storeadapter"
)

var _ turn.DedupeStore = (*turnDedupeStoreAdapter)(nil)

type turnDedupeStoreStub struct {
	upsert       func(context.Context, turndedupe.UpsertParams) error
	bindProvider func(context.Context, turndedupe.BindProviderTurnIDParams) error
	markTerminal func(context.Context, string, time.Time) error
	getLive      func(context.Context, string) (turndedupe.Entry, error)
}

func (s *turnDedupeStoreStub) Upsert(ctx context.Context, params turndedupe.UpsertParams) error {
	if s.upsert != nil {
		return s.upsert(ctx, params)
	}
	return nil
}

func (s *turnDedupeStoreStub) BindProviderTurnID(ctx context.Context, params turndedupe.BindProviderTurnIDParams) error {
	if s.bindProvider != nil {
		return s.bindProvider(ctx, params)
	}
	return nil
}

func (s *turnDedupeStoreStub) MarkTerminal(ctx context.Context, dedupeKey string, now time.Time) error {
	if s.markTerminal != nil {
		return s.markTerminal(ctx, dedupeKey, now)
	}
	return nil
}

func (s *turnDedupeStoreStub) GetLive(ctx context.Context, dedupeKey string) (turndedupe.Entry, error) {
	if s.getLive != nil {
		return s.getLive(ctx, dedupeKey)
	}
	return turndedupe.Entry{}, nil
}

func (*turnDedupeStoreStub) Sweep(context.Context, time.Time) error { return nil }

// TestTurnDedupeStoreProviderNilSemantics 固定 optional Store 缺失时 provider 返回 nil 领域端口。
func TestTurnDedupeStoreProviderNilSemantics(t *testing.T) {
	if store := provideTurnDedupeStore(nil); store != nil {
		t.Fatalf("expected nil turn dedupe port, got %T", store)
	}
	var typedNil *turnDedupeStoreStub
	if store := provideTurnDedupeStore(typedNil); store != nil {
		t.Fatalf("expected typed nil Store to produce nil turn dedupe port, got %T", store)
	}
}

// TestTurnDedupeStoreAdapterFieldCoverage 自动覆盖写入、绑定与读取 DTO 的全部导出字段。
func TestTurnDedupeStoreAdapterFieldCoverage(t *testing.T) {
	t.Run("upsert_to_store", testTurnDedupeUpsertFieldCoverage)
	t.Run("bind_to_store", testTurnDedupeBindFieldCoverage)
	t.Run("entry_from_store", testTurnDedupeEntryFieldCoverage)
}

func testTurnDedupeUpsertFieldCoverage(t *testing.T) {
	storeadaptertest.AssertFieldsMapE(t, func(params turn.DedupeUpsertParams) (turndedupe.UpsertParams, error) {
		var captured turndedupe.UpsertParams
		store := provideTurnDedupeStore(&turnDedupeStoreStub{upsert: func(_ context.Context, stored turndedupe.UpsertParams) error {
			captured = stored
			return nil
		}})
		err := store.Upsert(context.Background(), params)
		return captured, err
	})
}

func testTurnDedupeBindFieldCoverage(t *testing.T) {
	storeadaptertest.AssertFieldsMapE(t, func(params turn.DedupeBindProviderTurnIDParams) (turndedupe.BindProviderTurnIDParams, error) {
		var captured turndedupe.BindProviderTurnIDParams
		store := provideTurnDedupeStore(&turnDedupeStoreStub{bindProvider: func(_ context.Context, stored turndedupe.BindProviderTurnIDParams) error {
			captured = stored
			return nil
		}})
		err := store.BindProviderTurnID(context.Background(), params)
		return captured, err
	})
}

func testTurnDedupeEntryFieldCoverage(t *testing.T) {
	storeadaptertest.AssertFieldsMapE(t, func(entry turndedupe.Entry) (turn.DedupeEntry, error) {
		store := provideTurnDedupeStore(&turnDedupeStoreStub{getLive: func(context.Context, string) (turndedupe.Entry, error) {
			return entry, nil
		}})
		return store.GetLive(context.Background(), "dedupe-key")
	})
}

// TestTurnDedupeStoreAdapterMapsOnlyNotFound 固定 Store not-found 是唯一会被转换的错误。
func TestTurnDedupeStoreAdapterMapsOnlyNotFound(t *testing.T) {
	wrapped := fmt.Errorf("lookup registry: %w", turndedupe.ErrNotFound)
	store := provideTurnDedupeStore(&turnDedupeStoreStub{getLive: func(context.Context, string) (turndedupe.Entry, error) {
		return turndedupe.Entry{}, wrapped
	}})
	_, err := store.GetLive(context.Background(), "missing")
	if err != turn.ErrDedupeNotFound || !errors.Is(err, turn.ErrDedupeNotFound) {
		t.Fatalf("expected domain not-found sentinel, got %v", err)
	}
	if errors.Is(err, turndedupe.ErrNotFound) {
		t.Fatalf("Store sentinel leaked through domain boundary: %v", err)
	}
}

// TestTurnDedupeStoreAdapterPreservesOtherErrors 固定四个方法对普通 Store 错误保持对象身份和 errors.Is 链。
func TestTurnDedupeStoreAdapterPreservesOtherErrors(t *testing.T) {
	sentinel := errors.New("turn dedupe store sentinel")
	storeErr := fmt.Errorf("turn dedupe operation: %w", sentinel)
	store := newFailingTurnDedupePort(storeErr)
	tests := map[string]func() error{
		"upsert": func() error { return store.Upsert(context.Background(), turn.DedupeUpsertParams{}) },
		"bind": func() error {
			return store.BindProviderTurnID(context.Background(), turn.DedupeBindProviderTurnIDParams{})
		},
		"mark_terminal": func() error { return store.MarkTerminal(context.Background(), "key", time.Time{}) },
		"get_live":      func() error { _, err := store.GetLive(context.Background(), "key"); return err },
	}
	for name, call := range tests {
		t.Run(name, func(t *testing.T) {
			assertTurnDedupeStoreErrorPreserved(t, call(), storeErr, sentinel)
		})
	}
}

func newFailingTurnDedupePort(storeErr error) turn.DedupeStore {
	return provideTurnDedupeStore(&turnDedupeStoreStub{
		upsert:       func(context.Context, turndedupe.UpsertParams) error { return storeErr },
		bindProvider: func(context.Context, turndedupe.BindProviderTurnIDParams) error { return storeErr },
		markTerminal: func(context.Context, string, time.Time) error { return storeErr },
		getLive:      func(context.Context, string) (turndedupe.Entry, error) { return turndedupe.Entry{}, storeErr },
	})
}

func assertTurnDedupeStoreErrorPreserved(t *testing.T, got, storeErr, sentinel error) {
	t.Helper()
	if got != storeErr {
		t.Fatalf("expected original Store error, got %v", got)
	}
	if !errors.Is(got, sentinel) {
		t.Fatalf("expected errors.Is to preserve sentinel, got %v", got)
	}
}

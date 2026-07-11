package turndedupe

import (
	"context"
	"errors"
	"strings"
	"time"

	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
)

// querier 描述 turn 去重 store 依赖的 sqlc 查询集合。
// 拆出窄接口后，单元测试可用 fake querier 避开真实连接池。
type querier interface {
	UpsertTurnDedupeRegistry(ctx context.Context, arg sqlc.UpsertTurnDedupeRegistryParams) error
	BindTurnDedupeProviderID(ctx context.Context, arg sqlc.BindTurnDedupeProviderIDParams) error
	MarkTurnDedupeTerminal(ctx context.Context, arg sqlc.MarkTurnDedupeTerminalParams) error
	GetLiveTurnDedupe(ctx context.Context, arg sqlc.GetLiveTurnDedupeParams) (sqlc.TurnDedupeRegistry, error)
	SweepTurnDedupeRegistry(ctx context.Context, arg sqlc.SweepTurnDedupeRegistryParams) error
}

// store 实现 turn 去重注册表的 SQLite 持久化边界。
type store struct {
	q querier
}

// NewStore 创建 sqlc 支撑的 turn 去重 store。
// 调用方必须传入已初始化的查询器，这里不做 nil 兜底，避免恢复去重路径静默失效。
func NewStore(q *sqlc.Queries) Store { return &store{q: q} }

// newStoreForTest 让测试注入 fake querier，而不暴露内部 store 类型。
func newStoreForTest(q querier) Store { return &store{q: q} }

// Upsert 写入或刷新 dedupe key，空 key 或 local turn ID 会立即报错。
func (s *store) Upsert(ctx context.Context, p UpsertParams) error {
	key := strings.TrimSpace(p.DedupeKey)
	if key == "" {
		return errors.New("turndedupe: dedupe key required for upsert")
	}
	if strings.TrimSpace(p.LocalTurnID) == "" {
		return errors.New("turndedupe: local turn id required for upsert")
	}
	return s.q.UpsertTurnDedupeRegistry(ctx, sqlc.UpsertTurnDedupeRegistryParams{
		DedupeKey:   key,
		LocalTurnID: strings.TrimSpace(p.LocalTurnID),
		ThreadID:    strings.TrimSpace(p.ThreadID),
		Now:         tsMS(nonZero(p.Now)),
	})
}

// BindProviderTurnID 给已登记的 dedupe key 绑定 provider turn ID。
func (s *store) BindProviderTurnID(ctx context.Context, p BindProviderTurnIDParams) error {
	key := strings.TrimSpace(p.DedupeKey)
	if key == "" {
		return errors.New("turndedupe: dedupe key required for bind")
	}
	return s.q.BindTurnDedupeProviderID(ctx, sqlc.BindTurnDedupeProviderIDParams{
		ProviderTurnID: strings.TrimSpace(p.ProviderTurnID),
		Now:            tsMS(nonZero(p.Now)),
		DedupeKey:      key,
	})
}

// MarkTerminal 将 dedupe key 标记为终态，阻止后续 live 查询复用。
func (s *store) MarkTerminal(ctx context.Context, dedupeKey string, now time.Time) error {
	key := strings.TrimSpace(dedupeKey)
	if key == "" {
		return errors.New("turndedupe: dedupe key required for mark terminal")
	}
	v := tsMS(nonZero(now))
	return s.q.MarkTurnDedupeTerminal(ctx, sqlc.MarkTurnDedupeTerminalParams{
		Now:       &v,
		DedupeKey: key,
	})
}

// GetLive 读取仍未终态的去重记录，未命中统一返回 ErrNotFound。
func (s *store) GetLive(ctx context.Context, dedupeKey string) (Entry, error) {
	key := strings.TrimSpace(dedupeKey)
	if key == "" {
		return Entry{}, ErrNotFound
	}
	row, err := s.q.GetLiveTurnDedupe(ctx, sqlc.GetLiveTurnDedupeParams{DedupeKey: key})
	if err != nil {
		if platformdb.IsNotFound(err) {
			return Entry{}, ErrNotFound
		}
		return Entry{}, platformdb.WrapStoreError(err, "get_live", "turn_dedupe_registry")
	}
	return Entry{
		DedupeKey:      row.DedupeKey,
		LocalTurnID:    row.LocalTurnID,
		ProviderTurnID: row.ProviderTurnID,
		ThreadID:       row.ThreadID,
		CreatedAt:      fromMS(row.CreatedAt),
		UpdatedAt:      fromMS(row.UpdatedAt),
		TerminalAt:     fromMSPtr(row.TerminalAt),
	}, nil
}

// Sweep 清理早于 cutoff 的旧去重记录，cutoff 零值会 fail-fast。
func (s *store) Sweep(ctx context.Context, cutoff time.Time) error {
	if cutoff.IsZero() {
		return errors.New("turndedupe: sweep cutoff must be non-zero")
	}
	return platformdb.WrapStoreError(
		s.q.SweepTurnDedupeRegistry(ctx, sqlc.SweepTurnDedupeRegistryParams{Cutoff: tsMS(cutoff)}),
		"sweep",
		"turn_dedupe_registry",
	)
}

// nonZero 在调用方未传时间时使用当前时间。
func nonZero(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now()
	}
	return t
}

// tsMS 将时间转成数据库存储使用的毫秒时间戳。
func tsMS(t time.Time) int64 {
	return platformdb.Millis(t)
}

// fromMS 将毫秒时间戳转成 time.Time，零值表示没有时间。
func fromMS(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return platformdb.TimeFromMillis(ms)
}

// fromMSPtr 将可空毫秒时间戳转成 time.Time，nil 表示没有时间。
func fromMSPtr(ms *int64) time.Time {
	if ms == nil {
		return time.Time{}
	}
	return fromMS(*ms)
}

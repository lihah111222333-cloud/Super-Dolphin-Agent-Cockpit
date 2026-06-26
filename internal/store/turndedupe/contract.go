// Package turndedupe 持久化 dedupe_key 到 local_turn_id 的映射。
// 它只补足 mcp-orch 重启后的查漏路径，正常热路径仍由 internal/module/turn 的内存去重状态处理。
package turndedupe

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound 表示没有匹配的 live 去重记录，调用方应按“尚未提交”处理。
var ErrNotFound = errors.New("turndedupe: no live registry row")

// Entry 表示 turn_dedupe_registry 的跨进程恢复 DTO。
// TerminalAt 为零值时说明记录仍可用于 live 去重判断，非零时只能作为历史清理对象。
type Entry struct {
	DedupeKey      string
	LocalTurnID    string
	ProviderTurnID string
	ThreadID       string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	TerminalAt     time.Time
}

// Store 定义 turn 去重注册表的持久化接口。
// 它用于跨进程恢复后的幂等判断，默认生产实现由 sqlc store 提供，测试可用 NewNoop 避开数据库。
type Store interface {
	// Upsert 写入或刷新 dedupeKey 对应行，冲突时会清空 terminal_at 以恢复 live 状态。
	Upsert(ctx context.Context, params UpsertParams) error
	// BindProviderTurnID 只绑定 provider_turn_id，不改 local_turn_id 和 terminal_at。
	BindProviderTurnID(ctx context.Context, params BindProviderTurnIDParams) error
	// MarkTerminal 标记记录已终态，后续 GetLive 会跳过它。
	MarkTerminal(ctx context.Context, dedupeKey string, now time.Time) error
	// GetLive 返回 dedupeKey 的 live 记录，未命中或仅命中终态记录时返回 ErrNotFound。
	GetLive(ctx context.Context, dedupeKey string) (Entry, error)
	// Sweep 删除 updated_at 早于 cutoff 的旧记录。
	Sweep(ctx context.Context, cutoff time.Time) error
}

// UpsertParams 承载 Upsert 入参。
// 空 ThreadID 在 SQL 层表示保留旧值，允许先注册再补线程信息的流程重复调用。
type UpsertParams struct {
	DedupeKey   string
	LocalTurnID string
	ThreadID    string
	Now         time.Time
}

// BindProviderTurnIDParams 承载 provider turn ID 绑定入参。
type BindProviderTurnIDParams struct {
	DedupeKey      string
	ProviderTurnID string
	Now            time.Time
}

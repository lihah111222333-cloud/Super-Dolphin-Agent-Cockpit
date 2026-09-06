// Package securitytime 提供与传输、数据库和 provider 无关的安全时间 owner 候选。
//
// 该包只定义状态机和持久化端口；它不会打开数据库、联系 RecoveryCustody
// 或为任何编排操作提供默认时间。生产接线必须由外部 security-time owner
// 实现 StateStore，并在不可用时保持 fail-closed。
package securitytime

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var (
	ErrUnavailable      = errors.New("security time is unavailable")
	ErrStaleWriter      = errors.New("security time writer is stale")
	ErrInvalidSample    = errors.New("security time sample is invalid")
	ErrSnapshotExpired  = errors.New("security time snapshot is expired")
	ErrSnapshotMismatch = errors.New("security time snapshot does not match current owner")
	ErrStateNotFound    = errors.New("security time state was not found")
	ErrInvalidContext   = errors.New("security time context is nil")
)

// Sample 是一次由受信时钟源提供的 wall/monotonic 联合观测。
// MonoNS 必须来自同一 boot 的单调时钟；WallMS 是 UTC Unix 毫秒。
type Sample struct {
	SourceID  string
	BootEpoch int64
	WallMS    int64
	MonoNS    int64
	Fresh     bool
}

// Snapshot 是可跨层携带的安全时间快照。MonoDeadlineNS 仅在同一 boot 内
// 有效；跨进程或重启必须由 BootEpoch/ClockEpoch 和持久 high-water 重新验证。
type Snapshot struct {
	SecurityDomainID string
	LineageDigest    string
	SourceID         string
	BootEpoch        int64
	ClockEpoch       int64
	Generation       int64
	Revision         int64
	WallHighWaterMS  int64
	IssuedAtMS       int64
	LeaseExpiresAtMS int64
	MonoIssuedNS     int64
	MonoDeadlineNS   int64
	MaxSkewMS        int64
}

// Validate 检查快照是否具备可用于安全期限判断的完整字段。
func (s Snapshot) Validate() error { return validateSnapshot(s) }

// State 是 StateStore 持久化的可重放 owner 状态。StickyUnavailable 只能由
// 外部恢复/重新认证流程清除，普通 Observe/Allocate 不得自行清除。
type State struct {
	Version           int64
	SecurityDomainID  string
	LineageDigest     string
	SourceID          string
	BootEpoch         int64
	ClockEpoch        int64
	Generation        int64
	Revision          int64
	WallHighWaterMS   int64
	LastWallMS        int64
	LastMonoNS        int64
	MaxSkewMS         int64
	StickyUnavailable bool
}

// StateStore 是持久 owner 的最小 CAS 端口。实现方负责把 CompareAndSwap
// 映射到自己的单一持久 writer；本包不假设 SQLite、PostgreSQL 或文件实现。
type StateStore interface {
	Load(context.Context, string) (State, error)
	CompareAndSwap(context.Context, string, int64, State) error
}

// Clock 是可注入的时钟源。生产实现必须同时提供 wall 与 monotonic 观测，
// 测试实现可确定性地注入回拨、前跳和 sleep/resume。
type Clock interface {
	Sample(context.Context) (Sample, error)
}

// Config 固定 security domain、lineage、启动 epoch 和最大容忍偏差。
type Config struct {
	SecurityDomainID string
	LineageDigest    string
	BootEpoch        int64
	MaxSkewMS        int64
}

// ClockOwner 是安全时间 owner 的并发安全操作面。
type ClockOwner struct {
	mu    sync.Mutex
	cfg   Config
	store StateStore
	clock Clock
}

// NewClockOwner 创建未接线的安全时间 owner。缺少任一持久端口、时钟源或
// 配置会直接失败，不会落到 time.Now 或内存默认状态。
func NewClockOwner(cfg Config, store StateStore, clock Clock) (*ClockOwner, error) {
	if store == nil || clock == nil {
		return nil, fmt.Errorf("security time owner: state store and clock required")
	}
	if !validText(cfg.SecurityDomainID, 256) || !validText(cfg.LineageDigest, 512) {
		return nil, fmt.Errorf("security time owner: security domain and lineage required")
	}
	if cfg.BootEpoch <= 0 || cfg.MaxSkewMS <= 0 {
		return nil, fmt.Errorf("security time owner: positive boot epoch and max skew required")
	}
	return &ClockOwner{cfg: cfg, store: store, clock: clock}, nil
}

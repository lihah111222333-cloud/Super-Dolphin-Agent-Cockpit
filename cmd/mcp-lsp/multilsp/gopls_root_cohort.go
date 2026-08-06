package multilsp

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
)

var (
	// ErrGoplsRootCohortConfigConflict 表示同一仓库实例试图接入第二份不可变配置。
	ErrGoplsRootCohortConfigConflict = errors.New("gopls root cohort immutable config conflict")
	// ErrGoplsRootCohortFenceStale 表示租约 fence 已经失效，不能再提交副作用。
	ErrGoplsRootCohortFenceStale = errors.New("gopls root cohort fence is stale")
	// ErrGoplsRootCohortClosed 表示 controller 已封闭 admission。
	ErrGoplsRootCohortClosed = errors.New("gopls root cohort controller is closed")
	// ErrGoplsRootCohortDurabilityUnsupported 表示跨 sidecar durable authority 尚未接入。
	ErrGoplsRootCohortDurabilityUnsupported = errors.New("gopls root cohort durable admission is unsupported")
	// ErrGoplsRootCohortDrainOwnerUnavailable 表示 root idle drain 没有可执行 owner。
	ErrGoplsRootCohortDrainOwnerUnavailable = errors.New("gopls root cohort drain owner is unavailable")
	// ErrGoplsRootCohortDrainCleanupPending 表示 shutdown 失败，owner evidence 必须保留并重试。
	ErrGoplsRootCohortDrainCleanupPending = errors.New("gopls root cohort drain cleanup is pending")
)

// GoplsRepositoryInstanceProof 是仓库实例身份的不可变证明。
// 各字段由入口层从 canonical Git common-dir、文件系统身份和 marker 读取结果生成；
// controller 只比较 typed proof，不自行猜测路径或回退到当前工作目录。
type GoplsRepositoryInstanceProof struct {
	CanonicalRootDigest string
	FilesystemIdentity  string
	GitMarkerDigest     string
	InstanceNonce       string
}

// GoplsRootCohortConfig 描述一个 gopls root cohort 的 immutable admission 配置。
// CohortID 和 EffectiveConfigDigest 必须由入口层根据同一份 canonical root proof 计算。
type GoplsRootCohortConfig struct {
	CohortID                string
	RepositoryInstanceProof GoplsRepositoryInstanceProof
	EffectiveConfigDigest   string
}

// Validate 检查 root cohort 配置是否携带完整的不可变证明。
func (c GoplsRootCohortConfig) Validate() error {
	if strings.TrimSpace(c.CohortID) == "" {
		return errors.New("gopls root cohort cohort ID is required")
	}
	if err := c.RepositoryInstanceProof.validate(); err != nil {
		return err
	}
	if strings.TrimSpace(c.EffectiveConfigDigest) == "" {
		return errors.New("gopls root cohort effective config digest is required")
	}
	return nil
}

func (p GoplsRepositoryInstanceProof) validate() error {
	fields := []struct {
		name  string
		value string
	}{
		{"canonical root digest", p.CanonicalRootDigest},
		{"filesystem identity", p.FilesystemIdentity},
		{"Git marker digest", p.GitMarkerDigest},
		{"instance nonce", p.InstanceNonce},
	}
	for _, field := range fields {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("gopls root cohort %s is required", field.name)
		}
	}
	return nil
}

func (c GoplsRootCohortConfig) rootKey() string {
	// canonical root digest 是 admission map 的唯一 key；其余 proof 字段变化必须
	// 进入同一个 entry 并触发 conflict，绝不能因为 proof 不同而创建第二 cohort。
	return c.RepositoryInstanceProof.CanonicalRootDigest
}

func (c GoplsRootCohortConfig) equalImmutable(other GoplsRootCohortConfig) bool {
	return c.CohortID == other.CohortID &&
		c.EffectiveConfigDigest == other.EffectiveConfigDigest &&
		c.RepositoryInstanceProof == other.RepositoryInstanceProof
}

// GoplsRootCohortState 描述 root cohort controller 的最小生命周期。
type GoplsRootCohortState string

const (
	GoplsRootCohortStateAdmitted       GoplsRootCohortState = "admitted"
	GoplsRootCohortStateIdle           GoplsRootCohortState = "idle"
	GoplsRootCohortStateClosed         GoplsRootCohortState = "closed"
	GoplsRootCohortStateCleanupPending GoplsRootCohortState = "cleanup_pending"
)

// GoplsRootCohortFence 是 AcquireLease 返回的成员级提交 fence。
// JournalRevision 不等同于进程时间；它只在 controller 所有的 admission/release 事件上递增。
type GoplsRootCohortFence struct {
	Epoch            uint64
	JournalRevision  uint64
	MemberID         string
	MemberGeneration uint64
	LeaseID          string
}

// GoplsRootCohortLease 持有一个 root cohort member 的 fence。
type GoplsRootCohortLease struct {
	controller       *goplsRootCohortController
	releaseFn        func() error
	releaseWithOwner func(func() error) error
	rootKey          string
	config           GoplsRootCohortConfig
	fence            GoplsRootCohortFence
	releaseState     *goplsRootCohortReleaseState
}

type goplsRootCohortReleaseState struct {
	once sync.Once
	err  error
}

// Fence 返回租约的不可变 fence 快照。
func (l *GoplsRootCohortLease) Fence() GoplsRootCohortFence {
	if l == nil {
		return GoplsRootCohortFence{}
	}
	return l.fence
}

// Config 返回租约绑定的 immutable root cohort 配置。
func (l *GoplsRootCohortLease) Config() GoplsRootCohortConfig {
	if l == nil {
		return GoplsRootCohortConfig{}
	}
	return l.config
}

// Release 释放 member lease；重复调用是幂等的，并不会制造新的 cohort。
func (l *GoplsRootCohortLease) Release() error {
	return l.ReleaseWithOwner(nil)
}

// ReleaseWithOwner 释放 member lease，并在该 member 成为最后一个成员时把
// forwarder shutdown 交给 root controller 的唯一 idle-drain owner。owner
// callback 不得绕过 controller 直接删除 durable evidence。
func (l *GoplsRootCohortLease) ReleaseWithOwner(owner func() error) error {
	if l == nil || (l.controller == nil && l.releaseFn == nil && l.releaseWithOwner == nil) {
		return nil
	}
	if l.releaseState == nil {
		l.releaseState = &goplsRootCohortReleaseState{}
	}
	l.releaseState.once.Do(func() {
		if l.releaseWithOwner != nil {
			l.releaseState.err = l.releaseWithOwner(owner)
			return
		}
		if l.releaseFn != nil {
			l.releaseState.err = l.releaseFn()
			return
		}
		l.releaseState.err = l.controller.release(l.rootKey, l.fence)
	})
	return l.releaseState.err
}

// NewGoplsRootCohortLeaseFromAuthority binds a durable authority's release callback
// to the same typed lease used by the in-process controller. The callback must be
// idempotent; Release itself additionally guarantees at-most-once invocation per lease.
func NewGoplsRootCohortLeaseFromAuthority(
	config GoplsRootCohortConfig,
	fence GoplsRootCohortFence,
	release func() error,
) (GoplsRootCohortLease, error) {
	if err := config.Validate(); err != nil {
		return GoplsRootCohortLease{}, err
	}
	if release == nil {
		return GoplsRootCohortLease{}, errors.New("gopls root cohort authority release callback is required")
	}
	if strings.TrimSpace(fence.MemberID) == "" || strings.TrimSpace(fence.LeaseID) == "" {
		return GoplsRootCohortLease{}, errors.New("gopls root cohort authority fence identity is required")
	}
	return GoplsRootCohortLease{
		releaseFn:    release,
		rootKey:      config.rootKey(),
		config:       config,
		fence:        fence,
		releaseState: &goplsRootCohortReleaseState{},
	}, nil
}

// NewGoplsRootCohortLeaseFromAuthorityWithOwner 构造带 idle-drain owner 的 durable lease。
func NewGoplsRootCohortLeaseFromAuthorityWithOwner(
	config GoplsRootCohortConfig,
	fence GoplsRootCohortFence,
	release func() error,
	releaseWithOwner func(func() error) error,
) (GoplsRootCohortLease, error) {
	if err := config.Validate(); err != nil {
		return GoplsRootCohortLease{}, err
	}
	if release == nil || releaseWithOwner == nil {
		return GoplsRootCohortLease{}, errors.New("gopls root cohort authority release callbacks are required")
	}
	if fence.Epoch == 0 || fence.JournalRevision == 0 || fence.MemberGeneration == 0 ||
		strings.TrimSpace(fence.MemberID) == "" || strings.TrimSpace(fence.LeaseID) == "" {
		return GoplsRootCohortLease{}, errors.New("gopls root cohort authority fence is incomplete")
	}
	return GoplsRootCohortLease{
		releaseFn:        release,
		releaseWithOwner: releaseWithOwner,
		rootKey:          config.rootKey(),
		config:           config,
		fence:            fence,
		releaseState:     &goplsRootCohortReleaseState{},
	}, nil
}

// GoplsRootCohortSnapshot 是 controller 的只读状态快照。
type GoplsRootCohortSnapshot struct {
	Config          GoplsRootCohortConfig
	State           GoplsRootCohortState
	Epoch           uint64
	JournalRevision uint64
	ActiveMembers   int
}

// GoplsRootCohortController 暴露 root-level immutable admission 与 fence 检查。
// 当前实现是进程内 controller；它不伪称跨进程 durable authority。
type GoplsRootCohortController interface {
	AcquireLease(config GoplsRootCohortConfig) (GoplsRootCohortLease, error)
	ValidateFence(config GoplsRootCohortConfig, fence GoplsRootCohortFence) error
	Snapshot(config GoplsRootCohortConfig) (GoplsRootCohortSnapshot, bool)
	Close() error
}

type goplsRootCohortController struct {
	mu     sync.Mutex
	closed bool
	seq    uint64
	roots  map[string]*goplsRootCohortEntry
}

type goplsRootCohortEntry struct {
	config               GoplsRootCohortConfig
	state                GoplsRootCohortState
	epoch                uint64
	journalRevision      uint64
	nextMemberGeneration uint64
	activeMembers        map[string]GoplsRootCohortFence
}

// NewGoplsRootCohortController 创建进程内 root cohort admission controller。
// 跨 sidecar/进程的 durable journal 尚未接入时，调用方必须把该边界视为 process-local。
func NewGoplsRootCohortController() GoplsRootCohortController {
	return &goplsRootCohortController{roots: make(map[string]*goplsRootCohortEntry)}
}

// AcquireLease 原子准入同一 root 的 immutable config，并复用既有 cohort。
func (c *goplsRootCohortController) AcquireLease(config GoplsRootCohortConfig) (GoplsRootCohortLease, error) {
	if c == nil {
		return GoplsRootCohortLease{}, errors.New("gopls root cohort controller is nil")
	}
	if err := config.Validate(); err != nil {
		return GoplsRootCohortLease{}, err
	}
	rootKey := config.rootKey()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return GoplsRootCohortLease{}, ErrGoplsRootCohortClosed
	}
	entry := c.roots[rootKey]
	if entry == nil {
		entry = &goplsRootCohortEntry{
			config:        config,
			state:         GoplsRootCohortStateAdmitted,
			epoch:         1,
			activeMembers: make(map[string]GoplsRootCohortFence),
		}
		c.roots[rootKey] = entry
	} else if !entry.config.equalImmutable(config) {
		return GoplsRootCohortLease{}, fmt.Errorf("%w for canonical root proof %s", ErrGoplsRootCohortConfigConflict, config.RepositoryInstanceProof.CanonicalRootDigest)
	}
	entry.state = GoplsRootCohortStateAdmitted
	entry.nextMemberGeneration++
	entry.journalRevision++
	c.seq++
	fence := GoplsRootCohortFence{
		Epoch:            entry.epoch,
		JournalRevision:  entry.journalRevision,
		MemberID:         fmt.Sprintf("member-%d", c.seq),
		MemberGeneration: entry.nextMemberGeneration,
		LeaseID:          fmt.Sprintf("lease-%d", c.seq),
	}
	entry.activeMembers[fence.LeaseID] = fence
	return GoplsRootCohortLease{
		controller: c,
		releaseWithOwner: func(owner func() error) error {
			if err := c.release(rootKey, fence); err != nil {
				return err
			}
			if owner != nil {
				return owner()
			}
			return nil
		},
		rootKey:      rootKey,
		config:       entry.config,
		fence:        fence,
		releaseState: &goplsRootCohortReleaseState{},
	}, nil
}

// ValidateFence 检查 member lease 是否仍属于同一份 immutable config。
func (c *goplsRootCohortController) ValidateFence(config GoplsRootCohortConfig, fence GoplsRootCohortFence) error {
	if c == nil {
		return ErrGoplsRootCohortFenceStale
	}
	if err := config.Validate(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.roots[config.rootKey()]
	if c.closed || entry == nil || !entry.config.equalImmutable(config) {
		return ErrGoplsRootCohortFenceStale
	}
	active, ok := entry.activeMembers[fence.LeaseID]
	if !ok || active != fence {
		return ErrGoplsRootCohortFenceStale
	}
	return nil
}

// Snapshot 返回 root cohort 的只读状态；未知 root 不会隐式创建条目。
func (c *goplsRootCohortController) Snapshot(config GoplsRootCohortConfig) (GoplsRootCohortSnapshot, bool) {
	if c == nil {
		return GoplsRootCohortSnapshot{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.roots[config.rootKey()]
	if entry == nil || !entry.config.equalImmutable(config) {
		return GoplsRootCohortSnapshot{}, false
	}
	return GoplsRootCohortSnapshot{
		Config:          entry.config,
		State:           entry.state,
		Epoch:           entry.epoch,
		JournalRevision: entry.journalRevision,
		ActiveMembers:   len(entry.activeMembers),
	}, true
}

func (c *goplsRootCohortController) release(rootKey string, fence GoplsRootCohortFence) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.roots[rootKey]
	if entry == nil {
		return ErrGoplsRootCohortFenceStale
	}
	active, ok := entry.activeMembers[fence.LeaseID]
	if !ok || active != fence {
		return ErrGoplsRootCohortFenceStale
	}
	delete(entry.activeMembers, fence.LeaseID)
	entry.journalRevision++
	if len(entry.activeMembers) == 0 {
		entry.state = GoplsRootCohortStateIdle
	}
	return nil
}

// Close 封闭后续 admission，并保留快照状态供诊断；不会伪造跨进程 journal receipt。
func (c *goplsRootCohortController) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	for _, entry := range c.roots {
		entry.state = GoplsRootCohortStateClosed
	}
	return nil
}

// DigestGoplsRootCohortConfig 为入口层提供稳定的有效配置摘要。
func DigestGoplsRootCohortConfig(config GoplsRootCohortConfig) string {
	p := config.RepositoryInstanceProof
	value := strings.Join([]string{
		config.CohortID,
		p.CanonicalRootDigest,
		p.FilesystemIdentity,
		p.GitMarkerDigest,
		p.InstanceNonce,
		config.EffectiveConfigDigest,
	}, "\x00")
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

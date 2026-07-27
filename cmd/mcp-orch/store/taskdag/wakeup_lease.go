package taskdag

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// ClaimedWakeupLease 保存同一 dispatch claim 的最新续租行，供执行和最终提交共享 fence。
type ClaimedWakeupLease struct {
	mu     sync.RWMutex
	wakeup Wakeup
}

// NewClaimedWakeupLease 从已领取 wakeup 创建并发安全的 lease 快照。
func NewClaimedWakeupLease(wakeup *Wakeup) (*ClaimedWakeupLease, error) {
	if wakeup == nil {
		return nil, fmt.Errorf("claimed wakeup lease: wakeup required")
	}
	return &ClaimedWakeupLease{wakeup: cloneWakeupLeaseRow(*wakeup)}, nil
}

// Context 把动态 lease 放入执行上下文；WakeupFenceFromContext 会读取最新续租值。
func (l *ClaimedWakeupLease) Context(ctx context.Context) (context.Context, error) {
	if l == nil {
		return nil, fmt.Errorf("claimed wakeup lease: nil receiver")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, wakeupLeaseContextKey{}, l), nil
}

// CurrentWakeup 返回当前 lease 行的独立快照。
func (l *ClaimedWakeupLease) CurrentWakeup() *Wakeup {
	if l == nil {
		return nil
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	copy := cloneWakeupLeaseRow(l.wakeup)
	return &copy
}

// Update 接受同一 claim 的续租结果，并原子替换当前 wakeup/fence。
func (l *ClaimedWakeupLease) Update(renewed *Wakeup) error {
	if l == nil || renewed == nil {
		return fmt.Errorf("claimed wakeup lease: current and renewed wakeup required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := validateRenewedWakeupIdentity(l.wakeup, *renewed); err != nil {
		return err
	}
	l.wakeup = cloneWakeupLeaseRow(*renewed)
	return nil
}

// CurrentFence 返回当前续租行对应的节点副作用 fence。
func (l *ClaimedWakeupLease) CurrentFence() WakeupFence {
	current := l.CurrentWakeup()
	if current == nil {
		return WakeupFence{}
	}
	return wakeupFenceFromWakeup(current)
}

type wakeupLeaseContextKey struct{}

// validateRenewedWakeupIdentity 防止续租响应切换 wakeup、attempt、owner 或 claim 代次。
func validateRenewedWakeupIdentity(current, renewed Wakeup) error {
	switch {
	case renewed.ID != current.ID:
		return fmt.Errorf("claimed wakeup lease: renewed id=%d, want %d", renewed.ID, current.ID)
	case renewed.AttemptCount != current.AttemptCount:
		return fmt.Errorf("claimed wakeup lease: renewed attempt=%d, want %d", renewed.AttemptCount, current.AttemptCount)
	case strings.TrimSpace(renewed.ClaimedBy) != strings.TrimSpace(current.ClaimedBy):
		return fmt.Errorf("claimed wakeup lease: renewed owner=%q, want %q", renewed.ClaimedBy, current.ClaimedBy)
	case current.ClaimedAt != nil && (renewed.ClaimedAt == nil || !renewed.ClaimedAt.Equal(*current.ClaimedAt)):
		return fmt.Errorf("claimed wakeup lease: renewed claimed_at changed")
	case renewed.LeaseExpiresAt == nil:
		return fmt.Errorf("claimed wakeup lease: renewed lease_expires_at required")
	default:
		return nil
	}
}

// wakeupFenceFromWakeup 把持久化 wakeup 行转换为节点副作用使用的完整 fence。
func wakeupFenceFromWakeup(wakeup *Wakeup) WakeupFence {
	if wakeup == nil {
		return WakeupFence{}
	}
	fence := WakeupFence{
		WakeupID:      wakeup.ID,
		WakeupAttempt: wakeup.AttemptCount,
		ClaimedBy:     strings.TrimSpace(wakeup.ClaimedBy),
	}
	if wakeup.ClaimedAt != nil {
		fence.ClaimedAt = *wakeup.ClaimedAt
	}
	if wakeup.LeaseExpiresAt != nil {
		fence.LeaseExpiresAt = *wakeup.LeaseExpiresAt
	}
	return fence
}

// cloneWakeupLeaseRow 复制会随续租变化的时间指针，避免快照之间共享可变地址。
func cloneWakeupLeaseRow(wakeup Wakeup) Wakeup {
	copy := wakeup
	if wakeup.ClaimedAt != nil {
		claimedAt := *wakeup.ClaimedAt
		copy.ClaimedAt = &claimedAt
	}
	if wakeup.LeaseExpiresAt != nil {
		leaseExpiresAt := *wakeup.LeaseExpiresAt
		copy.LeaseExpiresAt = &leaseExpiresAt
	}
	return copy
}

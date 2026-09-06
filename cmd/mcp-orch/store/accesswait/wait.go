package accesswait

import (
	"context"
	"errors"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
)

const (
	maxWaitPollEvents = int64(200)
	maxWaitDuration   = 60 * time.Second
	waitPollInterval  = 25 * time.Millisecond
)

// Wait 在有界轮询窗口内等待首个匹配事件；重启后可用同一 PollRequest 继续。
func (s *Store) Wait(ctx context.Context, request PollRequest) (WaitResult, error) {
	if err := s.ready(ctx); err != nil {
		return WaitResult{}, err
	}
	if err := request.Validate(); err != nil {
		return WaitResult{}, err
	}
	filter, err := s.waitFilter(ctx, request)
	if err != nil {
		return WaitResult{}, err
	}
	for {
		result, done, err := s.waitIteration(ctx, request, filter)
		if err != nil || done {
			return result, err
		}
	}
}

// waitIteration 完成一次“注册（可选）→ durable poll → bounded sleep”循环。
func (s *Store) waitIteration(ctx context.Context, request PollRequest, filter eventstore.WatchFilter) (WaitResult, bool, error) {
	subscription, err := s.registerWaitWatcher(ctx, filter)
	if err != nil {
		return WaitResult{}, false, err
	}
	result, err := s.PollWait(ctx, request)
	if err != nil {
		closeWatchSubscription(subscription)
		return WaitResult{}, false, err
	}
	if result.Status != "pending" {
		closeWatchSubscription(subscription)
		return result, true, nil
	}
	if subscription == nil {
		return WaitResult{}, false, s.waitForPollInterval(ctx, result.Handle.DeadlineAt)
	}
	if err := s.waitForWatcher(ctx, subscription, result.Handle.DeadlineAt); err != nil {
		subscription.Close()
		return WaitResult{}, false, err
	}
	subscription.Close()
	return WaitResult{}, false, nil
}

// waitFilter 从 durable handle 提取固定 source/lane；无 watcher 时返回空值
// 以保留原有 PollWait 轮询兼容路径。
func (s *Store) waitFilter(ctx context.Context, request PollRequest) (eventstore.WatchFilter, error) {
	if s.watcher == nil {
		return eventstore.WatchFilter{}, nil
	}
	handle, err := s.GetWait(ctx, request)
	if err != nil {
		return eventstore.WatchFilter{}, err
	}
	filter := eventstore.WatchFilter{SourceID: handle.Selector.SourceID, SourceEpoch: handle.Selector.SourceEpoch, Lane: handle.Selector.Lane}
	if err := filter.Validate(); err != nil {
		return eventstore.WatchFilter{}, err
	}
	return filter, nil
}

// registerWaitWatcher 建立一次 bounded 进程内订阅；未配置 seam 时返回 nil。
func (s *Store) registerWaitWatcher(ctx context.Context, filter eventstore.WatchFilter) (*eventstore.WatchSubscription, error) {
	if s.watcher == nil {
		return nil, nil
	}
	return s.watcher.Register(ctx, filter)
}

// closeWatchSubscription 在轮询/上下文/终态路径统一回收临时订阅。
func closeWatchSubscription(subscription *eventstore.WatchSubscription) {
	if subscription != nil {
		subscription.Close()
	}
}

// waitForWatcher 等待一次提示唤醒，再由下一次 PollWait 重读 durable 状态。短计时器
// 为丢失通知保留有界 polling fallback，避免把本地 channel 当成权威来源。
func (s *Store) waitForWatcher(ctx context.Context, subscription *eventstore.WatchSubscription, deadline time.Time) error {
	if subscription == nil {
		return errors.New("access wait watcher subscription is required")
	}
	remaining := deadline.Sub(s.now().UTC())
	if remaining <= 0 {
		return nil
	}
	timer := time.NewTimer(minDuration(waitPollInterval, remaining))
	defer stopTimer(timer)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-subscription.Events():
		return nil
	case <-timer.C:
		return nil
	}
}

// waitForPollInterval 使用同一 owner clock 计算下一次轮询，避免安全时间与 wall clock 分裂。
func (s *Store) waitForPollInterval(ctx context.Context, deadline time.Time) error {
	remaining := deadline.Sub(s.now().UTC())
	if remaining <= 0 {
		return nil
	}
	timer := time.NewTimer(minDuration(waitPollInterval, remaining))
	defer stopTimer(timer)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func stopTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func minDuration(left, right time.Duration) time.Duration {
	if left < right {
		return left
	}
	return right
}

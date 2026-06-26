package shared

import (
	"context"
	"math/rand"
	"time"
)

// Policy 描述 RetryWithPolicy 的重试次数、退避和回调策略。
type Policy struct {
	MaxAttempts int                                               // 最大尝试次数，0 表示不执行。
	BaseDelay   time.Duration                                     // 初始退避间隔。
	MaxDelay    time.Duration                                     // 单次退避上限，0 表示不限制。
	Jitter      float64                                           // 抖动比例，规范化到 0..1。
	OnRetry     func(attempt int, err error, delay time.Duration) // 每次等待前的观测回调。
}

// Retry 使用最大次数和基础退避执行 fn，是 RetryWithPolicy 的简化入口。
func Retry(ctx context.Context, maxAttempts int, base time.Duration, fn func() error) error {
	return RetryWithPolicy(ctx, Policy{
		MaxAttempts: maxAttempts,
		BaseDelay:   base,
	}, fn)
}

// RetryWithPolicy 按策略重试 fn，context 取消时立即返回取消错误。
func RetryWithPolicy(ctx context.Context, policy Policy, fn func() error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	policy = normalizeRetryPolicy(policy)

	var lastErr error
	for attempt := 0; attempt < policy.MaxAttempts; attempt++ {
		if err := fn(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if attempt == policy.MaxAttempts-1 {
			break
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		delay := retryDelay(policy, attempt, rand.Float64())
		if policy.OnRetry != nil {
			policy.OnRetry(attempt+1, lastErr, delay)
		}
		if err := waitRetry(ctx, delay); err != nil {
			return err
		}
	}
	return lastErr
}

// normalizeRetryPolicy 清理负数和越界 jitter，避免退避计算产生无效值。
func normalizeRetryPolicy(policy Policy) Policy {
	if policy.MaxAttempts < 0 {
		policy.MaxAttempts = 0
	}
	if policy.BaseDelay < 0 {
		policy.BaseDelay = 0
	}
	if policy.MaxDelay < 0 {
		policy.MaxDelay = 0
	}
	if policy.Jitter < 0 {
		policy.Jitter = 0
	}
	if policy.Jitter > 1 {
		policy.Jitter = 1
	}
	return policy
}

// retryDelay 计算指定尝试轮次的退避时间，并应用上限和 jitter。
func retryDelay(policy Policy, attempt int, rnd float64) time.Duration {
	delay := exponentialDelay(policy.BaseDelay, attempt)
	if policy.MaxDelay > 0 && delay > policy.MaxDelay {
		delay = policy.MaxDelay
	}
	delay = applyJitter(delay, policy.Jitter, rnd)
	if policy.MaxDelay > 0 && delay > policy.MaxDelay {
		delay = policy.MaxDelay
	}
	return delay
}

// exponentialDelay 按 2 倍指数退避计算延迟，并在溢出前截断到最大 Duration。
func exponentialDelay(base time.Duration, attempt int) time.Duration {
	if base <= 0 || attempt <= 0 {
		return base
	}
	delay := base
	const maxDuration = time.Duration(1<<63 - 1)
	for i := 0; i < attempt; i++ {
		if delay > maxDuration/2 {
			return maxDuration
		}
		delay *= 2
	}
	return delay
}

// applyJitter 按随机值在退避时间两侧应用 jitter。
func applyJitter(delay time.Duration, jitter, rnd float64) time.Duration {
	if delay <= 0 || jitter <= 0 {
		return delay
	}
	if rnd < 0 {
		rnd = 0
	}
	if rnd > 1 {
		rnd = 1
	}
	factor := 1 + ((rnd*2)-1)*jitter
	if factor < 0 {
		factor = 0
	}
	return time.Duration(float64(delay) * factor)
}

// waitRetry 等待下一次重试；即使 delay 为 0 也会先检查 context 取消。
func waitRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

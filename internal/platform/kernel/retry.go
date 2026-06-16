package kernel

import (
	"context"
	"math/rand"
	"time"
)

// Policy configures retry attempts, delay bounds, jitter, and callbacks.
type Policy struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
	Jitter      float64
	OnRetry     func(attempt int, err error, delay time.Duration)
}

// Retry 重试平台shared。
func Retry(ctx context.Context, maxAttempts int, base time.Duration, fn func() error) error {
	return RetryWithPolicy(ctx, Policy{
		MaxAttempts: maxAttempts,
		BaseDelay:   base,
	}, fn)
}

// RetryWithPolicy 重试带策略的平台shared。
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

// normalizeRetryPolicy 规范化重试策略。
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

// applyJitter 应用jitter。
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

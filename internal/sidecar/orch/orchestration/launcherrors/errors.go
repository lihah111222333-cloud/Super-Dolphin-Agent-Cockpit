package launcherrors

import (
	"context"
	"errors"
	"strings"
	"time"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const (
	MaxRetries       = 3
	launchRetryBase  = 2 * time.Second
	RateLimitBackoff = 60 * time.Second
)

// ComputeRetryBackoff 计算重试backoff。
func ComputeRetryBackoff(attempt int, prevErr error) time.Duration {
	if IsRateLimited(prevErr) {
		return RateLimitBackoff
	}
	return time.Duration(attempt) * launchRetryBase
}

// WaitRetryBackoff 等待重试backoff。
func WaitRetryBackoff(ctx context.Context, attempt int, agentID string, prevErr error) error {
	delay := ComputeRetryBackoff(attempt, prevErr)
	startedAt := time.Now()
	pkglogger.Info("orchestration: retrying launch",
		pkglogger.String(pkglogger.FieldAgentID, agentID),
		pkglogger.Int("attempt", attempt+1),
		pkglogger.Any("prev_error", prevErr),
		pkglogger.Int64("backoff_ms", delay.Milliseconds()),
		pkglogger.Any("rate_limited", IsRateLimited(prevErr)))
	select {
	case <-time.After(delay):
		elapsed := time.Since(startedAt)
		attrs := []any{
			pkglogger.String(pkglogger.FieldAgentID, agentID),
			pkglogger.Int("attempt", attempt+1),
			pkglogger.Int64(pkglogger.FieldDurationMS, elapsed.Milliseconds()),
		}
		pkglogger.Info("orchestration: launch retry backoff completed", attrs...)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Class describes a launcherrors API type.
type Class string

const (
	ClassTransient Class = "transient"
	ClassPermanent Class = "permanent"
	ClassUnknown   Class = "unknown"
)

var permanentLaunchPatterns = []string{"401", "unauthoriz", "authentication failed", "authentication required", "not authenticated", "not logged in", "login required", "login-required", "login_required", "please log in", "please run /login", "sign in", "auth expired", "auth token expired", "session expired", "unable to connect to api (connectionrefused)", "selected model", "may not exist or you may not have access", "not have access to it", "pick a different model", "model unavailable", "model_not_found", "model not found", "invalid api key", "invalid_api_key", "403", "forbidden", "permission denied", "quota_exhausted", "insufficient_quota", "usage limit", "out of credits", "402", "payment_required", "subscription expired", "context_length_exceeded", "context length exceeded", "maximum context", "prompt is too long", "launch cwd is required", "launch cwd is invalid"}

var transientLaunchPatterns = []string{"deadline exceeded", "connection refused", "transport unavailable", "empty thread id", "timed out", "i/o timeout"}

// Classify 分类编排。
func Classify(err error) Class {
	if err == nil {
		return ClassTransient
	}
	msg := strings.ToLower(err.Error())
	for _, pattern := range permanentLaunchPatterns {
		if strings.Contains(msg, pattern) {
			return ClassPermanent
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || IsRateLimited(err) {
		return ClassTransient
	}
	for _, pattern := range transientLaunchPatterns {
		if strings.Contains(msg, pattern) {
			return ClassTransient
		}
	}
	return ClassUnknown
}

// IsRateLimited 判断ratelimited是否可用。
func IsRateLimited(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, p := range []string{"rate limit", "rate-limit", "rate_limit", "too many requests", "http 429", " 429 ", "status 429", "status: 429"} {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}

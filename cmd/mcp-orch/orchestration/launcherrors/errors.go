package launcherrors

import (
	"context"
	"errors"
	"strings"
	"time"

	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

const (
	MaxRetries       = 3
	launchRetryBase  = 2 * time.Second
	RateLimitBackoff = 60 * time.Second
)

// ComputeRetryBackoff 根据尝试次数和上次错误计算启动重试退避。
// rate limit 错误使用固定长退避，其他错误按 attempt 线性放大。
func ComputeRetryBackoff(attempt int, prevErr error) time.Duration {
	if IsRateLimited(prevErr) {
		return RateLimitBackoff
	}
	return time.Duration(attempt) * launchRetryBase
}

// WaitRetryBackoff 等待启动重试退避并记录等待耗时。
// ctx 取消时立即返回，调用方不应继续本轮启动。
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

// Class 描述启动错误的重试分类。
// 调度层据此区分可重试临时错误和应立即停止的配置/认证错误。
type Class string

const (
	ClassTransient Class = "transient"
	ClassPermanent Class = "permanent"
	ClassUnknown   Class = "unknown"
)

// permanentLaunchPatterns 返回供本次分类独占使用的不可共享模式集合。
func permanentLaunchPatterns() []string {
	return []string{"401", "unauthoriz", "authentication failed", "authentication required", "not authenticated", "not logged in", "login required", "login-required", "login_required", "please log in", "please run /login", "sign in", "auth expired", "auth token expired", "session expired", "unable to connect to api (connectionrefused)", "selected model", "may not exist or you may not have access", "not have access to it", "pick a different model", "model unavailable", "model_not_found", "model not found", "invalid api key", "invalid_api_key", "403", "forbidden", "permission denied", "quota_exhausted", "insufficient_quota", "usage limit", "out of credits", "402", "payment_required", "subscription expired", "context_length_exceeded", "context length exceeded", "maximum context", "prompt is too long", "launch cwd is required", "launch cwd is invalid"}
}

// transientLaunchPatterns 返回供本次分类独占使用的不可共享模式集合。
func transientLaunchPatterns() []string {
	return []string{"deadline exceeded", "connection refused", "transport unavailable", "empty thread id", "timed out", "i/o timeout"}
}

// Classify 把启动错误归类为 permanent/transient/unknown。
// permanent 优先匹配认证、配额、模型和 cwd 配置问题，避免对不可恢复错误反复重试。
func Classify(err error) Class {
	if err == nil {
		return ClassTransient
	}
	msg := strings.ToLower(err.Error())
	for _, pattern := range permanentLaunchPatterns() {
		if strings.Contains(msg, pattern) {
			return ClassPermanent
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || IsRateLimited(err) {
		return ClassTransient
	}
	for _, pattern := range transientLaunchPatterns() {
		if strings.Contains(msg, pattern) {
			return ClassTransient
		}
	}
	return ClassUnknown
}

// IsRateLimited 识别常见 rate limit 错误文本。
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

package orchestration

import (
	"context"
	"errors"
	"strings"
	"time"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const (
	maxLaunchRetries = 3
	launchRetryBase  = 2 * time.Second
	// rateLimitBackoff is the wait used when the previous launch error looks
	// like an HTTP 429 / rate limit. The launcher uses jrpc2 over a line
	// protocol so we cannot read Retry-After; this fixed default still beats
	// the linear path by ~30x and is consistent with provider docs (60s).
	rateLimitBackoff = 60 * time.Second
)

func computeRetryBackoff(attempt int, prevErr error) time.Duration {
	if isRateLimited(prevErr) {
		return rateLimitBackoff
	}
	return time.Duration(attempt) * launchRetryBase
}

func waitRetryBackoff(ctx context.Context, attempt int, agentID string, prevErr error) error {
	delay := computeRetryBackoff(attempt, prevErr)
	pkglogger.Info("orchestration: retrying launch",
		"agent_id", agentID, "attempt", attempt+1,
		"prev_error", prevErr, "backoff", delay,
		"rate_limited", isRateLimited(prevErr))
	select {
	case <-time.After(delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// launchErrorClass 把 launch error 分成三类，让 launchWithRetry 决定是
// 继续重试 (transient/unknown) 还是直接跳出 (permanent)。值类型为 typed
// string 让 switch 编译期检查并方便日志/前端 reason 字段对齐。
type launchErrorClass string

const (
	launchClassTransient launchErrorClass = "transient"
	launchClassPermanent launchErrorClass = "permanent"
	launchClassUnknown   launchErrorClass = "unknown"
)

// permanentLaunchPatterns 与前端 useAutoContinue.PERMANENT_ERROR_PATTERNS
// 同源（5 类永久错误：401/403/quota/payment/context_length）。命中任一关键字
// 即视为 permanent，launchRetry 直接跳出循环不重试。两层各持一份是因为
// jrpc2 line protocol 让后端不能拿到 HTTP envelope；上游错误在哪一层先撞到
// 就在哪一层先识别。
var permanentLaunchPatterns = []string{
	// permanent_unauthenticated
	"401", "unauthoriz", "invalid api key", "invalid_api_key",
	// permanent_forbidden
	"403", "forbidden", "permission denied",
	// permanent_quota_exhausted
	"quota_exhausted", "insufficient_quota", "usage limit", "out of credits",
	// permanent_payment_required
	"402", "payment_required", "subscription expired",
	// permanent_context_length_exceeded
	"context_length_exceeded", "context length exceeded", "maximum context", "prompt is too long",
}

// transientLaunchPatterns 是已知的瞬态故障关键字（连接级 / 超时 / 启动竞态）。
// 命中则归 transient 让 launchRetry 退避后重试。和 isRateLimited（429）一起
// 构成 transient 集合。
var transientLaunchPatterns = []string{
	"deadline exceeded",
	"connection refused",
	"transport unavailable",
	"empty thread id",
	"timed out",
	"i/o timeout",
}

// classifyLaunchError 把 launch error 分类。permanent 优先级最高（即使消息里
// 同时含 transient 关键字，permanent 仍胜出——例如 "401 timeout"）。
func classifyLaunchError(err error) launchErrorClass {
	if err == nil {
		return launchClassTransient
	}
	msg := strings.ToLower(err.Error())
	for _, pattern := range permanentLaunchPatterns {
		if strings.Contains(msg, pattern) {
			return launchClassPermanent
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return launchClassTransient
	}
	if isRateLimited(err) {
		return launchClassTransient
	}
	for _, pattern := range transientLaunchPatterns {
		if strings.Contains(msg, pattern) {
			return launchClassTransient
		}
	}
	return launchClassUnknown
}

// isRateLimited reports whether err looks like an HTTP 429 / rate limit
// response. We cannot read Retry-After (jrpc2 line protocol strips it), so we
// match common substrings the upstream surfaces in its error message.
func isRateLimited(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, pattern := range []string{
		"rate limit",
		"rate-limit",
		"rate_limit",
		"too many requests",
		"http 429",
		" 429 ",
		"status 429",
		"status: 429",
	} {
		if strings.Contains(msg, pattern) {
			return true
		}
	}
	return false
}

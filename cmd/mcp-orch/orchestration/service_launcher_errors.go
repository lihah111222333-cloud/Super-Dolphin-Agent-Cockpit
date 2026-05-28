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

type launchErrorClass string

const (
	launchClassTransient launchErrorClass = "transient"
	launchClassPermanent launchErrorClass = "permanent"
	launchClassUnknown   launchErrorClass = "unknown"
)

var permanentLaunchPatterns = []string{"401", "unauthoriz", "authentication failed", "authentication required", "not authenticated", "not logged in", "login required", "login-required", "login_required", "please log in", "please run /login", "sign in", "auth expired", "auth token expired", "session expired", "unable to connect to api (connectionrefused)", "selected model", "may not exist or you may not have access", "not have access to it", "pick a different model", "model unavailable", "model_not_found", "model not found", "invalid api key", "invalid_api_key", "403", "forbidden", "permission denied", "quota_exhausted", "insufficient_quota", "usage limit", "out of credits", "402", "payment_required", "subscription expired", "context_length_exceeded", "context length exceeded", "maximum context", "prompt is too long", "launch cwd is required", "launch cwd is invalid"}

var transientLaunchPatterns = []string{"deadline exceeded", "connection refused", "transport unavailable", "empty thread id", "timed out", "i/o timeout"}

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

package orchestration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestIsRateLimited(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"empty", errors.New(""), false},
		{"rate limit phrase", errors.New("rate limit exceeded"), true},
		{"rate-limit hyphen", errors.New("rate-limit hit, retry later"), true},
		{"rate_limit underscore", errors.New("provider returned rate_limit_error"), true},
		{"too many requests", errors.New("Too Many Requests"), true},
		{"http 429 phrase", errors.New("upstream returned http 429"), true},
		{"status 429", errors.New("status 429: throttled"), true},
		{"status colon 429", errors.New("status: 429"), true},
		{"bare 429 with spaces", errors.New("got 429 from upstream"), true},
		{"transient timeout (not rate limited)", errors.New("i/o timeout"), false},
		{"connection refused (not rate limited)", errors.New("connection refused"), false},
		{"random 4290 (not 429)", errors.New("error 4290 something"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRateLimited(tc.err); got != tc.want {
				t.Fatalf("isRateLimited(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestComputeRetryBackoff(t *testing.T) {
	cases := []struct {
		name    string
		attempt int
		err     error
		want    time.Duration
	}{
		{"linear attempt 1 transient", 1, errors.New("i/o timeout"), 2 * time.Second},
		{"linear attempt 2 transient", 2, errors.New("connection refused"), 4 * time.Second},
		{"rate limit attempt 1 -> 60s", 1, errors.New("rate limit hit"), rateLimitBackoff},
		{"rate limit attempt 2 -> still 60s", 2, errors.New("HTTP 429"), rateLimitBackoff},
		{"too many requests -> 60s", 1, errors.New("Too Many Requests"), rateLimitBackoff},
		{"nil err falls through to linear", 1, nil, 2 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := computeRetryBackoff(tc.attempt, tc.err); got != tc.want {
				t.Fatalf("computeRetryBackoff(%d, %v) = %v, want %v", tc.attempt, tc.err, got, tc.want)
			}
		})
	}
}

func TestClassifyLaunchError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want launchErrorClass
	}{
		// nil 当 transient（保守 default：让重试逻辑遇到 nil 不主动跳出）
		{"nil -> transient", nil, launchClassTransient},
		// transient · context cancellation / timeout
		{"context deadline -> transient", context.DeadlineExceeded, launchClassTransient},
		{"context canceled -> transient", context.Canceled, launchClassTransient},
		{"deadline exceeded msg -> transient", errors.New("deadline exceeded"), launchClassTransient},
		// transient · 连接级 / 启动竞态
		{"connection refused -> transient", errors.New("connection refused"), launchClassTransient},
		{"transport unavailable -> transient", errors.New("transport unavailable"), launchClassTransient},
		{"empty thread id -> transient", errors.New("empty thread id"), launchClassTransient},
		{"i/o timeout -> transient", errors.New("i/o timeout"), launchClassTransient},
		{"timed out -> transient", errors.New("timed out"), launchClassTransient},
		// transient · rate limit (1.8c)
		{"rate limit -> transient", errors.New("rate limit exceeded"), launchClassTransient},
		{"http 429 -> transient", errors.New("upstream http 429"), launchClassTransient},
		{"too many requests -> transient", errors.New("Too Many Requests"), launchClassTransient},
		// permanent · 401
		{"401 unauthorized -> permanent", errors.New("401 unauthorized"), launchClassPermanent},
		{"invalid api key -> permanent", errors.New("invalid api key"), launchClassPermanent},
		{"invalid_api_key -> permanent", errors.New("invalid_api_key"), launchClassPermanent},
		// permanent · 403
		{"403 forbidden -> permanent", errors.New("403 forbidden"), launchClassPermanent},
		{"permission denied -> permanent", errors.New("permission denied"), launchClassPermanent},
		// permanent · quota
		{"quota_exhausted -> permanent", errors.New("quota_exhausted"), launchClassPermanent},
		{"insufficient_quota -> permanent", errors.New("insufficient_quota"), launchClassPermanent},
		{"usage limit -> permanent", errors.New("daily usage limit reached"), launchClassPermanent},
		{"out of credits -> permanent", errors.New("out of credits"), launchClassPermanent},
		// permanent · payment
		{"402 payment required -> permanent", errors.New("402 payment_required"), launchClassPermanent},
		{"subscription expired -> permanent", errors.New("subscription expired"), launchClassPermanent},
		// permanent · context_length
		{"context_length_exceeded -> permanent", errors.New("context_length_exceeded"), launchClassPermanent},
		{"context length exceeded msg -> permanent", errors.New("context length exceeded"), launchClassPermanent},
		{"maximum context -> permanent", errors.New("maximum context tokens"), launchClassPermanent},
		{"prompt is too long -> permanent", errors.New("prompt is too long"), launchClassPermanent},
		// permanent · launch contract
		{"missing launch cwd -> permanent", contract.ErrLaunchCWDRequired, launchClassPermanent},
		{"invalid launch cwd -> permanent", contract.ErrLaunchCWDInvalid, launchClassPermanent},
		{"root task missing -> permanent", errors.New(`root task id missing on thread "agent-parent"`), launchClassPermanent},
		{"task handoff title -> permanent", errors.New(`task handoff title is required for task "task-demo"`), launchClassPermanent},
		{"task handoff file -> permanent", errors.New(`task handoff file is required for task "task-demo"`), launchClassPermanent},
		{"task handoff config -> permanent", errors.New(`task handoff config "taskId" must be a string`), launchClassPermanent},
		// permanent 优先级高于 transient（同时含两类关键字时归 permanent）
		{"401 + timeout -> permanent", errors.New("401 unauthorized after i/o timeout"), launchClassPermanent},
		// unknown · 不在任何已知关键字列表里
		{"random unknown -> unknown", errors.New("some_unrecognized_failure"), launchClassUnknown},
		{"empty msg -> unknown", errors.New(""), launchClassUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyLaunchError(tc.err); got != tc.want {
				t.Fatalf("classifyLaunchError(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

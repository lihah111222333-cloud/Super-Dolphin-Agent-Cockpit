package orchestration

import (
	"context"
	"errors"
	"testing"
	"time"
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

func TestIsRetryableLaunchErrorIncludesRateLimit(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil -> false", nil, false},
		{"context deadline -> true", context.DeadlineExceeded, true},
		{"context canceled -> true", context.Canceled, true},
		{"rate limit -> true", errors.New("rate limit exceeded"), true},
		{"http 429 -> true", errors.New("upstream http 429"), true},
		{"too many requests -> true", errors.New("Too Many Requests"), true},
		{"connection refused -> true", errors.New("connection refused"), true},
		{"empty thread id -> true", errors.New("empty thread id"), true},
		{"random permanent -> false", errors.New("invalid_api_key"), false},
		{"401 unauthorized -> false (1.8b territory)", errors.New("401 unauthorized"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRetryableLaunchError(tc.err); got != tc.want {
				t.Fatalf("isRetryableLaunchError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

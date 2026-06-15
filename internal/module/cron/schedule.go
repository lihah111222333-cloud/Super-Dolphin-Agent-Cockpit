package cron

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	robfigcron "github.com/robfig/cron/v3"
)

// cronParser is the shared cron expression parser. We use the
// "standard" 5-field spec (minute, hour, dom, month, dow) plus the
// usual @yearly / @monthly / @weekly / @daily / @hourly descriptors,
// and @every <duration>. This matches the P1b plan's schedule_expr
// column and the subset cron callers typically write.
var cronParser = robfigcron.NewParser(
	robfigcron.Minute | robfigcron.Hour | robfigcron.Dom |
		robfigcron.Month | robfigcron.Dow |
		robfigcron.Descriptor,
)

// ErrInvalidScheduleExpr wraps the underlying parser error for callers
// that prefer sentinel-based checks. The detailed message still reaches
// the logs through fmt.Errorf %w.
var ErrInvalidScheduleExpr = errors.New("cron: invalid schedule_expr")

// ParseSchedule returns a NextRunAt computer for the given schedule_expr
// and timezone. Timezone defaults to UTC when empty or invalid. The
// returned func is pure and safe for concurrent use.
//
// 空或非法 timezone 目前按 UTC 处理，这是历史兼容行为。
// 若要改成失败，Create/Update 校验也要一起改。
func ParseSchedule(scheduleExpr, timezone string) (func(after time.Time) time.Time, error) {
	expr := strings.TrimSpace(scheduleExpr)
	if expr == "" {
		return nil, fmt.Errorf("%w: schedule_expr is empty", ErrInvalidScheduleExpr)
	}
	sched, err := cronParser.Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidScheduleExpr, err)
	}
	loc := time.UTC
	if tz := strings.TrimSpace(timezone); tz != "" {
		if parsed, err := time.LoadLocation(tz); err == nil {
			loc = parsed
		}
	}
	return func(after time.Time) time.Time {
		if after.IsZero() {
			after = time.Now()
		}
		return sched.Next(after.In(loc))
	}, nil
}

// ComputeNextRunAt is the one-shot version of ParseSchedule — parses the
// expression, computes next_run_at, and returns both for convenience.
// ComputeNextRunAt 根据 cron 配置计算下一次运行时间。
func ComputeNextRunAt(scheduleExpr, timezone string, after time.Time) (time.Time, error) {
	next, err := ParseSchedule(scheduleExpr, timezone)
	if err != nil {
		return time.Time{}, err
	}
	return next(after), nil
}

// BackoffConfig sets the retry backoff curve. Defaults match the P1b
// plan: base=30s, cap=15m, full jitter.
type BackoffConfig struct {
	Base   time.Duration
	Cap    time.Duration
	Jitter *rand.Rand // nil defaults to crypto-free default source
}

// DefaultBackoff matches the P1b plan.
var DefaultBackoff = BackoffConfig{
	Base: 30 * time.Second,
	Cap:  15 * time.Minute,
}

// NextRetryAt returns the next retry timestamp given the number of
// failures so far (1-based: failureCount=1 means "this is the first
// retry"). Uses exponential backoff capped at cfg.Cap, then applies
// full jitter (0..delay uniformly).
//
// Important corner case from the P1b plan: if the naive next retry
// crosses into or past the next scheduled fire (nextRunAt), the retry
// is skipped by returning time.Time{}; the scheduler then marks the
// current run failed and waits for the schedule's own tick so we don't
// spin retries past the daily cron window.
//
// 返回零值表示本轮不再重试，等下一次正常 schedule。不要把它当成计算失败。
func NextRetryAt(cfg BackoffConfig, now, nextRunAt time.Time, failureCount int32) time.Time {
	if failureCount <= 0 {
		return time.Time{}
	}
	base := cfg.Base
	if base <= 0 {
		base = DefaultBackoff.Base
	}
	cap := cfg.Cap
	if cap <= 0 {
		cap = DefaultBackoff.Cap
	}
	delay := base
	for i := int32(1); i < failureCount && delay < cap; i++ {
		delay *= 2
	}
	if delay > cap {
		delay = cap
	}
	// full jitter: [0, delay]
	var jittered time.Duration
	if cfg.Jitter != nil {
		jittered = time.Duration(cfg.Jitter.Int63n(int64(delay) + 1))
	} else {
		jittered = time.Duration(rand.Int63n(int64(delay) + 1))
	}
	next := now.Add(jittered)
	if !nextRunAt.IsZero() && !next.Before(nextRunAt) {
		// retry would cross the next scheduled tick; skip.
		return time.Time{}
	}
	return next
}

// DedupeKey computes sha256(job_id || scheduled_at || idempotency_key)
// and returns the hex digest. The value is used as the stable identifier
// that protects against provider-side duplicate StartTurn submissions
// across crash/restart windows. scheduled_at is serialized as RFC3339
// UTC so two schedulers running on clocks with different zones produce
// the same key for the same logical trigger.
//
// idempotencyKey 要来自 run 本身；只用 jobID+scheduledAt 会让 retry 和 RunOnce
// 互相去重，造成漏跑。
func DedupeKey(jobID string, scheduledAt time.Time, idempotencyKey string) string {
	h := sha256.New()
	h.Write([]byte(jobID))
	h.Write([]byte{0})
	h.Write([]byte(scheduledAt.UTC().Format(time.RFC3339Nano)))
	h.Write([]byte{0})
	h.Write([]byte(idempotencyKey))
	return hex.EncodeToString(h.Sum(nil))
}

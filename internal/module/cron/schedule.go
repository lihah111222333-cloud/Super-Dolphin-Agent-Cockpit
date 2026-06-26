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

// cronParser 是共享的 cron 表达式解析器，支持标准 5 字段格式和常见描述符。
var cronParser = robfigcron.NewParser(
	robfigcron.Minute | robfigcron.Hour | robfigcron.Dom |
		robfigcron.Month | robfigcron.Dow |
		robfigcron.Descriptor,
)

// ErrInvalidScheduleExpr 标记 schedule_expr 解析失败。
// 具体 parser 错误通过 fmt.Errorf 的 wrapping 继续传到调用方和日志。
var ErrInvalidScheduleExpr = errors.New("cron: invalid schedule_expr")

// ParseSchedule 解析 cron 表达式并返回可复用的下一次运行时间计算函数。
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

// ComputeNextRunAt 是 ParseSchedule 的一次性入口。
// 调用方需要同时处理表达式解析错误和零值 next_run_at。
func ComputeNextRunAt(scheduleExpr, timezone string, after time.Time) (time.Time, error) {
	next, err := ParseSchedule(scheduleExpr, timezone)
	if err != nil {
		return time.Time{}, err
	}
	return next(after), nil
}

// BackoffConfig 定义 cron run 失败后的重试退避曲线。
// Base/Cap 为零时使用 DefaultBackoff；Jitter 可由测试注入以获得确定性。
type BackoffConfig struct {
	Base   time.Duration
	Cap    time.Duration
	Jitter *rand.Rand // nil 时使用包级随机源
}

// DefaultBackoff 是生产默认重试退避，使用 30s 起步、15m 封顶。
var DefaultBackoff = BackoffConfig{
	Base: 30 * time.Second,
	Cap:  15 * time.Minute,
}

// NextRetryAt 根据失败次数计算下一次重试时间。
// 它使用指数退避和 full jitter；若重试会越过下一次正常 schedule，则返回零值让 scheduler 等正常 tick。
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
	// full jitter 使用 [0, delay]，避免多 job 同时重试。
	var jittered time.Duration
	if cfg.Jitter != nil {
		jittered = time.Duration(cfg.Jitter.Int63n(int64(delay) + 1))
	} else {
		jittered = time.Duration(rand.Int63n(int64(delay) + 1))
	}
	next := now.Add(jittered)
	if !nextRunAt.IsZero() && !next.Before(nextRunAt) {
		// 重试会跨过下一次正常触发时跳过，避免在日程窗口外继续自旋。
		return time.Time{}
	}
	return next
}

// DedupeKey 计算 provider 提交去重键。
// scheduled_at 固定用 UTC RFC3339Nano 序列化；idempotencyKey 必须来自 run，保证 retry/RunOnce 不互相吞掉。
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

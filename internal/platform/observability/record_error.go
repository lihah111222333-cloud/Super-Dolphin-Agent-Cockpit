package observability

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// traceRecordErrorWarnInterval 限制同一 scope 的 trace 写入失败告警频率。
const traceRecordErrorWarnInterval = time.Minute

// recordErrorWarningLimiter 保存一个 Service 实例的 per-scope 告警时间表。
type recordErrorWarningLimiter struct {
	warnings sync.Map
}

// newRecordErrorWarningLimiter 创建 Service 专属的 trace 写入失败告警限频器。
func newRecordErrorWarningLimiter() *recordErrorWarningLimiter {
	return &recordErrorWarningLimiter{}
}

// WarnRecordError 记录 trace 写入失败，但不影响原业务调用链。
// 同一 scope 在固定窗口内只告警一次，避免落盘故障刷屏。
func (s *Service) WarnRecordError(logger *slog.Logger, scope string, event TraceEvent, err error) {
	if s == nil || s.recordErrorWarnings == nil {
		panic("observability: record error warning limiter is required")
	}
	if err == nil {
		return
	}
	if logger == nil {
		logger = pkglogger.Get()
	}
	if !s.recordErrorWarnings.allow(scope, time.Now()) {
		return
	}
	logger.Warn("observability: trace record failed",
		"scope", scope,
		"method", event.Method,
		"trace_id", event.TraceID,
		"span_id", event.SpanID,
		"status", event.Status,
		"error", err,
	)
}

// allow 用原子时间戳实现 owner-local 的按 scope 限频。
func (l *recordErrorWarningLimiter) allow(scope string, now time.Time) bool {
	if l == nil {
		panic("observability: record error warning limiter is required")
	}
	if scope == "" {
		scope = "unknown"
	}
	value, _ := l.warnings.LoadOrStore(scope, &atomic.Int64{})
	last := value.(*atomic.Int64)
	nowNanos := now.UnixNano()
	for {
		previous := last.Load()
		if previous != 0 && nowNanos-previous < int64(traceRecordErrorWarnInterval) {
			return false
		}
		if last.CompareAndSwap(previous, nowNanos) {
			return true
		}
	}
}

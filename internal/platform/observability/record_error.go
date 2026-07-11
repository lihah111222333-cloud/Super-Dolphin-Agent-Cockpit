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

// traceRecordErrorWarnings 延迟创建各 scope 最近一次告警时间表。
var traceRecordErrorWarnings = sync.OnceValue(func() *sync.Map { return &sync.Map{} })

// WarnRecordError 记录 trace 写入失败，但不影响原业务调用链。
// 同一 scope 在固定窗口内只告警一次，避免落盘故障刷屏。
func WarnRecordError(logger *slog.Logger, scope string, event TraceEvent, err error) {
	if err == nil {
		return
	}
	if logger == nil {
		logger = pkglogger.Get()
	}
	if !allowTraceRecordErrorWarning(scope, time.Now()) {
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

// allowTraceRecordErrorWarning 用原子时间戳实现按 scope 限频。
func allowTraceRecordErrorWarning(scope string, now time.Time) bool {
	if scope == "" {
		scope = "unknown"
	}
	value, _ := traceRecordErrorWarnings().LoadOrStore(scope, &atomic.Int64{})
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

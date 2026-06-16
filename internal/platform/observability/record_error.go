package observability

import (
	"sync"
	"sync/atomic"
	"time"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const traceRecordErrorWarnInterval = time.Minute

var traceRecordErrorWarnings sync.Map

// WarnRecordError reports tracing write failures without failing the caller path.
// WarnRecordError 处理warn记录错误。
func WarnRecordError(logger *pkglogger.Logger, scope string, event TraceEvent, err error) {
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

// allowTraceRecordErrorWarning 判断trace记录错误warning是否可用。
func allowTraceRecordErrorWarning(scope string, now time.Time) bool {
	if scope == "" {
		scope = "unknown"
	}
	value, _ := traceRecordErrorWarnings.LoadOrStore(scope, &atomic.Int64{})
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

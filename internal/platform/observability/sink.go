package observability

import "context"

// Sink 持久化已脱敏的 trace event，并暴露关闭和统计能力。
type Sink interface {
	Append(context.Context, TraceEvent) error
	Close() error
	Stats() SinkStats
}

// SinkStats 记录 sink 写入成功与失败的累计计数。
type SinkStats struct {
	EventsWritten int64
	WriteErrors   int64
}

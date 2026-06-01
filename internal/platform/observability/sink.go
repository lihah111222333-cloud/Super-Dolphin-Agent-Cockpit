package observability

import "context"

// Sink durably records sanitized trace events.
type Sink interface {
	Append(context.Context, TraceEvent) error
	Close() error
	Stats() SinkStats
}

type SinkStats struct {
	EventsWritten int64
	WriteErrors   int64
}

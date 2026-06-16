package insight

import (
	"context"
	"log/slog"
	"reflect"
	"strings"
	"sync/atomic"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"

	"github.com/kelindar/event"

	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// defaultQueueCapacity is the bounded queue size between the subscriber
// callbacks and the flusher. A full queue causes new terminal signals to
// be dropped with a metric — preferable to blocking the bus publisher.
const defaultQueueCapacity = 512

// collector is the bus-facing half of the insight module. It listens for
// terminal turn events and enqueues lightweight signals for the flusher.
// It never reads observation.Contract itself; that lives in the flusher.
type collector struct {
	logger  *slog.Logger
	queue   chan flushSignal
	dropped atomic.Int64
}

// newCollector wires the bounded queue and logger. Capacity 0 falls back
// to defaultQueueCapacity.
func newCollector(logger *slog.Logger, capacity int) *collector {
	if logger == nil {
		logger = pkglogger.Get()
	}
	if capacity <= 0 {
		capacity = defaultQueueCapacity
	}
	return &collector{
		logger: logger,
		queue:  make(chan flushSignal, capacity),
	}
}

// subscribe registers bus subscribers that enqueue flush signals. Returns
// a cancel that tears every subscription down. Call order: the caller owns
// the cancel and must invoke it before closing the flusher's queue (or
// the subscriber could race with a closed channel).
func (c *collector) subscribe(dispatcher *event.Dispatcher, logger *pkglogger.Logger) context.CancelFunc {
	if dispatcher == nil {
		return func() {}
	}
	cancels := []context.CancelFunc{
		contract.ResilientSubscribe(dispatcher, func(ev turndto.TurnCompleted) {
			c.enqueueTerminal(ev.TurnID, ev.ThreadID, ev.AgentID, eventProvider(ev), ev.Timestamp)
		}, logger),
		contract.ResilientSubscribe(dispatcher, func(ev turndto.TurnInterrupted) {
			c.enqueueTerminal(ev.TurnID, ev.ThreadID, ev.AgentID, eventProvider(ev), ev.Timestamp)
		}, logger),
		contract.ResilientSubscribe(dispatcher, func(ev turndto.TurnStalled) {
			c.enqueueTerminal(ev.TurnID, ev.ThreadID, ev.AgentID, eventProvider(ev), ev.Timestamp)
		}, logger),
	}
	return func() {
		for _, cancel := range cancels {
			cancel()
		}
	}
}

// enqueueTerminal is the inner non-blocking enqueue. If the queue is full
// the signal is dropped and a metric (Dropped()) counts the loss — the
// plan requires bounded intake so a stuck flusher never backpressures
// bus publication.
func (c *collector) enqueueTerminal(turnID, threadID, agentID, provider string, timestamp time.Time) {
	localTurnID := strings.TrimSpace(turnID)
	if localTurnID == "" {
		// Without a turn id the flusher cannot find observation facts;
		// silently drop rather than pollute the queue.
		return
	}
	sig := flushSignal{
		LocalTurnID: localTurnID,
		ThreadID:    strings.TrimSpace(threadID),
		AgentID:     strings.TrimSpace(agentID),
		Provider:    strings.TrimSpace(provider),
		Timestamp:   timestamp,
	}
	select {
	case c.queue <- sig:
	default:
		n := c.dropped.Add(1)
		if c.logger != nil {
			c.logger.Warn("insight: flush queue full, dropping terminal signal",
				slog.String("local_turn_id", localTurnID),
				slog.Int64("dropped_total", n),
			)
		}
	}
}

// Dropped returns the total number of signals that were dropped because
// the queue was full. Useful for dashboards and tests.
// Dropped 处理dropped。
func (c *collector) Dropped() int64 { return c.dropped.Load() }

// eventProvider reads an optional Provider field from turn DTOs. Current
// turn DTOs may not carry provider yet; keeping this reflective adapter
// lets the collector preserve the field as soon as the wire shape adds it
// without changing the subscriber contract again.
// eventProvider 处理事件provider。
func eventProvider(ev any) string {
	v := reflect.ValueOf(ev)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return ""
	}
	field := v.FieldByName("Provider")
	if !field.IsValid() || field.Kind() != reflect.String {
		return ""
	}
	return field.String()
}

package insight

import (
	"context"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/kelindar/event"

	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
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
		platformbus.ResilientSubscribe(dispatcher, func(ev turndto.TurnCompleted) {
			c.enqueueTerminal(ev.TurnID, ev.ThreadID, ev.AgentID)
		}, logger),
		platformbus.ResilientSubscribe(dispatcher, func(ev turndto.TurnInterrupted) {
			c.enqueueTerminal(ev.TurnID, ev.ThreadID, ev.AgentID)
		}, logger),
		platformbus.ResilientSubscribe(dispatcher, func(ev turndto.TurnStalled) {
			c.enqueueTerminal(ev.TurnID, ev.ThreadID, ev.AgentID)
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
func (c *collector) enqueueTerminal(turnID, threadID, agentID string) {
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
func (c *collector) Dropped() int64 { return c.dropped.Load() }

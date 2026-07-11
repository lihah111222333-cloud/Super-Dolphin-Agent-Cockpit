package insight

import (
	"context"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"log/slog"
	"reflect"
	"strings"
	"sync/atomic"
	"time"

	"github.com/kelindar/event"

	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// defaultQueueCapacity 是 subscriber 到 flusher 之间的有界队列容量。
// 队列满时新到的 terminal 信号被丢弃并计入指标，而非阻塞总线发布方。
const defaultQueueCapacity = 512

// collector 是 insight 模块中面向总线的半部，监听 terminal turn 事件并将轻量信号入队。
// 它本身不读取 observation.Contract，读取工作由 flusher 负责。
type collector struct {
	logger  *slog.Logger
	queue   chan flushSignal
	dropped atomic.Int64
}

// newCollector 创建带有界队列和 logger 的 collector。capacity<=0 时使用 defaultQueueCapacity。
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

// subscribe 注册总线订阅，将 terminal turn 事件转换为 flush 信号入队。
// 返回的 cancel 会注销所有订阅；调用方在关闭 flusher 队列之前必须先调用 cancel，
// 避免向已关闭的 channel 写入。
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

// enqueueTerminal 将 terminal 信号非阻塞地写入队列。
// 队列已满时丢弃信号并累加 dropped 计数，保证总线发布方不受背压阻塞。
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

// Dropped 返回因队列已满而丢弃的信号总数，可用于监控看板和测试断言。
func (c *collector) Dropped() int64 { return c.dropped.Load() }

// eventProvider 从 turn DTO 中反射读取可选的 Provider 字段。
// 当前 turn DTO 可能还没有 provider 字段；使用反射适配器，一旦 wire 层加上该字段即可无缝传递，
// 无需再改 subscriber 契约。
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

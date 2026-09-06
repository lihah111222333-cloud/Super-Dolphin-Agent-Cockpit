package eventstore

import (
	"context"
	"errors"
	"reflect"
	"sync"
)

// EventNotifier 是 Store 使用的提交后窄通知端口；实现不得成为第二个持久化 owner。
type EventNotifier interface {
	Notify(AgentEvent) error
}

// EventWatcher 是同进程 waiter 消费的窄订阅端口，不暴露 transport、持久化或重放语义。
type EventWatcher interface {
	Register(context.Context, WatchFilter) (*WatchSubscription, error)
}

var (
	ErrInvalidWatch      = errors.New("event watcher input is invalid")
	ErrWatcherClosed     = errors.New("event watcher is closed")
	ErrWatcherCapacity   = errors.New("event watcher registration capacity exceeded")
	ErrWatcherOverflow   = errors.New("event watcher notification buffer overflow")
	ErrInvalidWatchEvent = errors.New("event watcher event is not materialized")
)

const (
	maxWatcherBuffer            = 1024
	maxWatcherRegistrations     = 4096
	defaultWatcherBuffer        = 16
	defaultWatcherRegistrations = 128
)

// WatchFilter 固定一个 canonical source/lane；watcher 不会隐式扩大 tuple，epoch
// rollover 必须重新注册。
type WatchFilter struct {
	SourceID    string
	SourceEpoch string
	Lane        Lane
}

// Validate 在分配订阅和 channel 前拒绝 malformed 或不完整的 source selector。
func (f WatchFilter) Validate() error {
	if err := validateSourceKey(f.SourceID, f.SourceEpoch, f.Lane); err != nil {
		return errors.Join(ErrInvalidWatch, err)
	}
	return nil
}

// WatcherConfig 在构造时固定 channel 与注册上限。NewWatcher 拒绝零值；不想选择
// 数值时可使用 DefaultWatcherConfig 取得有界本地候选配置。
type WatcherConfig struct {
	BufferSize       int
	MaxRegistrations int
}

// DefaultWatcherConfig 返回本地候选使用的有限配置。
func DefaultWatcherConfig() WatcherConfig {
	return WatcherConfig{BufferSize: defaultWatcherBuffer, MaxRegistrations: defaultWatcherRegistrations}
}

func (c WatcherConfig) validate() error {
	if c.BufferSize < 1 || c.BufferSize > maxWatcherBuffer {
		return errors.Join(ErrInvalidWatch, errors.New("watcher buffer size is out of bounds"))
	}
	if c.MaxRegistrations < 1 || c.MaxRegistrations > maxWatcherRegistrations {
		return errors.Join(ErrInvalidWatch, errors.New("watcher registration limit is out of bounds"))
	}
	return nil
}

// Watcher 是进程内有界唤醒 fanout，仅提供提示：通知溢出或关闭不改变 canonical
// event 或 wait 状态，durable AccessWait poll 路径仍是权威来源。
type Watcher struct {
	mu     sync.Mutex
	config WatcherConfig
	closed bool
	nextID uint64
	subs   map[uint64]*WatchSubscription
}

var _ EventWatcher = (*Watcher)(nil)

// NewWatcher 构造有界 watcher，不启动 goroutine。
func NewWatcher(config WatcherConfig) (*Watcher, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	return &Watcher{config: config, subs: make(map[uint64]*WatchSubscription)}, nil
}

// Register 分配一个有界订阅并安排 context 清理。使用 context.AfterFunc 而非无主
// goroutine；取消与注册/通知竞争时 Close 仍保持幂等。
func (w *Watcher) Register(ctx context.Context, filter WatchFilter) (*WatchSubscription, error) {
	if w == nil {
		return nil, ErrWatcherClosed
	}
	if ctx == nil {
		return nil, errors.Join(ErrInvalidWatch, errors.New("watcher context is required"))
	}
	if err := filter.Validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil, ErrWatcherClosed
	}
	if len(w.subs) >= w.config.MaxRegistrations {
		w.mu.Unlock()
		return nil, ErrWatcherCapacity
	}
	w.nextID++
	sub := &WatchSubscription{owner: w, id: w.nextID, filter: filter, ch: make(chan AgentEvent, w.config.BufferSize)}
	w.subs[sub.id] = sub
	w.mu.Unlock()

	// Install cancellation after insertion. If cancellation wins the race,
	// Close removes the just-created subscription and closes its channel.
	sub.setStop(context.AfterFunc(ctx, sub.Close))
	if err := ctx.Err(); err != nil {
		sub.Close()
		return nil, err
	}
	if sub.isClosed() {
		return nil, ErrWatcherClosed
	}
	return sub, nil
}

// Watch 是 Register 的语义别名，便于 wait 代码按“建立 watcher”描述调用。
func (w *Watcher) Watch(ctx context.Context, filter WatchFilter) (*WatchSubscription, error) {
	return w.Register(ctx, filter)
}

// Subscribe 是 Register 的语义别名；两者共享同一注册上限与清理规则。
func (w *Watcher) Subscribe(ctx context.Context, filter WatchFilter) (*WatchSubscription, error) {
	return w.Register(ctx, filter)
}

// Notify 向匹配订阅执行非阻塞尽力发送。满 channel 会 fail-closed：溢出订阅
// 被关闭并返回 ErrWatcherOverflow，调用方必须回到 durable cursor 轮询恢复。
func (w *Watcher) Notify(event AgentEvent) error {
	if w == nil {
		return ErrWatcherClosed
	}
	if event.Status != "materialized" {
		return ErrInvalidWatchEvent
	}
	if err := validateWatchEvent(event); err != nil {
		return errors.Join(ErrInvalidWatchEvent, err)
	}

	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return ErrWatcherClosed
	}
	var overflowed []*WatchSubscription
	for _, sub := range w.subs {
		if !sub.filter.matches(event) {
			continue
		}
		select {
		case sub.ch <- event:
		default:
			// Never block the canonical append caller or leave a stale subscription
			// looking healthy after a notification was lost.
			overflowed = append(overflowed, sub)
		}
	}
	w.mu.Unlock()
	for _, sub := range overflowed {
		sub.Close()
	}
	if len(overflowed) > 0 {
		return ErrWatcherOverflow
	}
	return nil
}

func validateWatchEvent(event AgentEvent) error {
	if err := event.IngressFrame.Validate(); err != nil {
		return err
	}
	return validateCanonicalEnvelopeHash(event.IngressFrame)
}

// Publish 是面向“发布”语义调用方的显式别名，保留相同非阻塞行为。
func (w *Watcher) Publish(event AgentEvent) error { return w.Notify(event) }

// Close 删除全部注册并关闭 channel；不持久化 cursor、不取消 durable wait，也不改变
// canonical event 状态。
func (w *Watcher) Close() {
	if w == nil {
		return
	}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.closed = true
	subs := make([]*WatchSubscription, 0, len(w.subs))
	for _, sub := range w.subs {
		subs = append(subs, sub)
	}
	w.mu.Unlock()
	for _, sub := range subs {
		sub.Close()
	}
}

// RegistrationCount 返回当前活跃的进程内订阅数。
func (w *Watcher) RegistrationCount() int {
	if w == nil {
		return 0
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.subs)
}

// WatchSubscription 是一个有界 source/lane channel；context 取消、显式 Close 或
// owner Close 时 channel 会关闭。
type WatchSubscription struct {
	owner  *Watcher
	id     uint64
	filter WatchFilter
	ch     chan AgentEvent
	once   sync.Once
	mu     sync.Mutex
	closed bool
	stop   func() bool
}

// Events 暴露只读唤醒 channel。
func (s *WatchSubscription) Events() <-chan AgentEvent {
	if s == nil {
		return nil
	}
	return s.ch
}

// C 是面向频繁 select 调用方的只读 channel 短别名。
func (s *WatchSubscription) C() <-chan AgentEvent { return s.Events() }

// Close 精确执行一次取消注册并关闭订阅。
func (s *WatchSubscription) Close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		stop := s.stop
		s.mu.Unlock()
		if s.owner != nil {
			s.owner.remove(s)
		}
		close(s.ch)
		if stop != nil {
			stop()
		}
	})
}

func (s *WatchSubscription) setStop(stop func() bool) {
	if stop == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		stop()
		return
	}
	s.stop = stop
	s.mu.Unlock()
}

func (s *WatchSubscription) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (w *Watcher) remove(sub *WatchSubscription) {
	w.mu.Lock()
	if current, ok := w.subs[sub.id]; ok && current == sub {
		delete(w.subs, sub.id)
	}
	w.mu.Unlock()
}

func (f WatchFilter) matches(event AgentEvent) bool {
	return f.SourceID == event.SourceID && f.SourceEpoch == event.SourceEpoch && f.Lane == event.Lane
}

// eventNotifierIsNil 在 owner 边界拒绝 typed-nil 实现。
func eventNotifierIsNil(notifier EventNotifier) bool {
	if notifier == nil {
		return true
	}
	value := reflect.ValueOf(notifier)
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Func, reflect.Slice, reflect.Chan:
		return value.IsNil()
	default:
		return false
	}
}

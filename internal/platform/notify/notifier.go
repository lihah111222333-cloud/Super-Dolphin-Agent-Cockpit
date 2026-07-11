package notify

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// DefaultQueueCapacity 是通知入队 channel 的默认容量，可由 NotifyConfig.QueueCapacity 覆盖。
const DefaultQueueCapacity = 512

// Notifier 是 core 侧 MessageNotifier 实现，用有界 channel 隔离调用方和后台 flusher。
// TryEnqueue 保持非阻塞，避免 webhook 发送卡住后反压总线回调。
type Notifier struct {
	logger   *slog.Logger
	queue    chan contract.NotifyRequest
	resolver Resolver
	dropped  atomic.Int64
}

var _ contract.MessageNotifier = (*Notifier)(nil)

// NewNotifier 创建非阻塞通知队列，nil resolver 会让入队 fail-fast 而不是静默丢弃未知 alias。
func NewNotifier(logger *slog.Logger, resolver Resolver, capacity int) *Notifier {
	if logger == nil {
		logger = pkglogger.Get()
	}
	if capacity <= 0 {
		capacity = DefaultQueueCapacity
	}
	return &Notifier{
		logger:   logger,
		queue:    make(chan contract.NotifyRequest, capacity),
		resolver: resolver,
	}
}

// TryEnqueue 先解析 alias 再非阻塞入队。
// 未配置 alias 返回 ErrNotifyAliasNotFound；队列已满返回 ErrNotifyQueueFull 并增加 dropped 指标。
func (n *Notifier) TryEnqueue(ctx context.Context, req contract.NotifyRequest) error {
	if n == nil {
		return contract.ErrNotifyAliasNotFound
	}
	alias := strings.TrimSpace(req.ChannelAlias)
	if alias == "" {
		return contract.ErrNotifyAliasNotFound
	}
	// 入队前解析 alias，避免未知 channel 占用队列并让 worker 重复处理同一配置错误。
	if n.resolver == nil {
		return contract.ErrNotifyAliasNotFound
	}
	if _, err := n.resolver.Resolve(alias); err != nil {
		if errors.Is(err, ErrAliasNotFound) {
			return contract.ErrNotifyAliasNotFound
		}
		return err
	}
	req.ChannelAlias = alias
	select {
	case n.queue <- req:
		return nil
	default:
		n.dropped.Add(1)
		return contract.ErrNotifyQueueFull
	}
}

// Dropped 返回因队列满导致的入队拒绝累计数，供 metrics 或诊断读取。
func (n *Notifier) Dropped() int64 { return n.dropped.Load() }

// queueForFlusher 只在包内暴露队列给 flusher，外部调用方不能直接消费通知。
func (n *Notifier) queueForFlusher() chan contract.NotifyRequest { return n.queue }

// resolverForFlusher 让 flusher 使用同一份不可变 resolver。
func (n *Notifier) resolverForFlusher() Resolver { return n.resolver }

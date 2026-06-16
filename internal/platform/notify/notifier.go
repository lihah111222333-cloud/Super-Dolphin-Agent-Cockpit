package notify

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// DefaultQueueCapacity backs the bounded intake channel. Overridable
// via config.NotifyConfig.QueueCapacity.
const DefaultQueueCapacity = 512

// Notifier is the core-side MessageNotifier implementation. It wraps a
// bounded channel; TryEnqueue is non-blocking so a stuck flusher cannot
// backpressure the bus callback that called us.
type Notifier struct {
	logger   *pkglogger.Logger
	queue    chan contract.NotifyRequest
	resolver Resolver
	dropped  atomic.Int64
}

var _ contract.MessageNotifier = (*Notifier)(nil)

// NewNotifier constructs a Notifier. A nil logger falls back to the
// package default; a nil resolver is treated as "no channels" — every
// TryEnqueue for an alias then fails with ErrNotifyAliasNotFound, which
// is preferable to a silent drop.
// NewNotifier 创建notifier。
func NewNotifier(logger *pkglogger.Logger, resolver Resolver, capacity int) *Notifier {
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

// TryEnqueue validates the request and non-blocking enqueues it.
// Returns:
//   - contract.ErrNotifyAliasNotFound when the alias is empty or not
//     configured — callers must treat this as a misconfiguration, not
//     a transient failure.
//   - contract.ErrNotifyQueueFull when the bounded channel is full —
//     the flusher is behind or stuck; caller drops the signal.
//
// TryEnqueue 处理tryenqueue。
func (n *Notifier) TryEnqueue(ctx context.Context, req contract.NotifyRequest) error {
	if n == nil {
		return contract.ErrNotifyAliasNotFound
	}
	alias := strings.TrimSpace(req.ChannelAlias)
	if alias == "" {
		return contract.ErrNotifyAliasNotFound
	}
	// Resolve up front so an unknown alias does not pollute the queue
	// and waste a worker tick parsing the same unknown alias later.
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

// Dropped exposes the total number of TryEnqueue rejections because
// the queue was full. Useful for metrics scraping.
// Dropped 处理dropped。
func (n *Notifier) Dropped() int64 { return n.dropped.Load() }

// queueForFlusher exposes the channel to the flusher package-private
// so external callers cannot read from it.
func (n *Notifier) queueForFlusher() chan contract.NotifyRequest { return n.queue }

// resolverForFlusher mirrors queueForFlusher.
func (n *Notifier) resolverForFlusher() Resolver { return n.resolver }

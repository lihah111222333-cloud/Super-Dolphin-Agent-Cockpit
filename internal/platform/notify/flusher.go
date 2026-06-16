package notify

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/util/ctxutil"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// DefaultDrainTimeout bounds the shutdown drain. P21 P2 plan mandates
// 5 seconds; overridable via config.NotifyConfig.DrainSeconds.
const DefaultDrainTimeout = 5 * time.Second

// Flusher is the platformrunner.Runner that drains the notifier queue,
// renders each request via the platform-specific helper, and POSTs with
// the SSRF-guarded webhook client. Every error is logged + counted;
// none abort the loop because failed deliveries should not tear down
// the Runner and block future notifications.
type Flusher struct {
	logger       *pkglogger.Logger
	queue        chan contract.NotifyRequest
	resolver     Resolver
	client       *WebhookClient
	drainTimeout time.Duration
	now          func() time.Time

	sent        atomic.Int64
	delivered   atomic.Int64
	sendErrors  atomic.Int64
	resolveErrs atomic.Int64
	renderErrs  atomic.Int64
	drainDrops  atomic.Int64
}

var _ contract.Runner = (*Flusher)(nil)

// NewFlusher wires a Flusher with the notifier's queue + resolver and a
// pre-built webhook client. drainTimeout <= 0 falls back to the plan
// default.
// NewFlusher 创建flusher。
func NewFlusher(logger *pkglogger.Logger, notifier *Notifier, client *WebhookClient, drainTimeout time.Duration) *Flusher {
	if logger == nil {
		logger = pkglogger.Get()
	}
	if drainTimeout <= 0 {
		drainTimeout = DefaultDrainTimeout
	}
	var queue chan contract.NotifyRequest
	var resolver Resolver
	if notifier != nil {
		queue = notifier.queueForFlusher()
		resolver = notifier.resolverForFlusher()
	}
	return &Flusher{
		logger:       logger,
		queue:        queue,
		resolver:     resolver,
		client:       client,
		drainTimeout: drainTimeout,
		now:          time.Now,
	}
}

// Run loops until ctx cancels. Each queued request is resolved via
// resolver, rendered per platform, and delivered via client.Post. On
// cancel it runs a bounded drain (5s by default) using a background
// context so in-flight POSTs finish cleanly rather than being aborted
// by the same signal that triggered shutdown.
//
// Cancel-race note: select picks cases randomly when several are
// ready, so after ctx is cancelled and the queue still holds items
// the select may pull a queued request with the already-cancelled
// ctx. We detect that inside the queue branch, requeue the request
// (best-effort, since the queue is bounded), and fall through to
// drain — which runs the POST with a fresh background context so
// the in-flight work isn't aborted by the same signal that triggered
// shutdown.
// Run 启动平台notify后台流程。
func (f *Flusher) Run(ctx context.Context) error {
	if f == nil || f.queue == nil {
		// Nothing to drain; sit on ctx so the run.Group keeps the
		// shutdown discipline.
		<-ctx.Done()
		return ctx.Err()
	}
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case req, ok := <-f.queue:
			if !ok {
				return nil
			}
			if ctx.Err() != nil {
				// Race: select picked queue while ctx was cancelling.
				// Push the request back so drain() picks it up under
				// the bounded background ctx. Best-effort requeue;
				// a full queue means one request is dropped, which
				// the dropped metric already counts.
				select {
				case f.queue <- req:
				default:
					f.drainDrops.Add(1)
				}
				break loop
			}
			f.handle(ctx, req)
		}
	}
	f.drain()
	return ctx.Err()
}

// drain pulls everything currently in the queue and flushes it under
// a bounded background context so in-flight POSTs complete cleanly
// after ctx cancel. Exits as soon as the queue is empty OR the
// drainTimeout fires; does not wait for new enqueues because bus
// subscribers should already be shutting down alongside Run.
// drain 处理drain。
func (f *Flusher) drain() {
	if f.drainTimeout <= 0 {
		return
	}
	drainCtx, cancel := ctxutil.WithTimeout(context.Background(), f.drainTimeout)
	defer cancel()
	for {
		select {
		case req, ok := <-f.queue:
			if !ok {
				return
			}
			f.handle(drainCtx, req)
		case <-drainCtx.Done():
			remaining := len(f.queue)
			if remaining > 0 {
				f.drainDrops.Add(int64(remaining))
				f.logger.Warn("notify: drain timeout, dropped",
					pkglogger.Int("remaining", remaining),
				)
			}
			return
		default:
			// Queue empty and timeout not yet fired — we're done.
			return
		}
	}
}

// handle is the single-request path. All failures end here — we log,
// bump a counter, and move on.
// handle 处理平台notify。
func (f *Flusher) handle(ctx context.Context, req contract.NotifyRequest) {
	f.sent.Add(1)
	if f.resolver == nil {
		f.resolveErrs.Add(1)
		f.logger.Warn("notify: no resolver configured")
		return
	}
	cfg, err := f.resolver.Resolve(req.ChannelAlias)
	if err != nil {
		f.resolveErrs.Add(1)
		f.logger.Warn("notify: resolve alias failed",
			pkglogger.String("alias", req.ChannelAlias),
			pkglogger.String("error", RedactError(err)),
		)
		return
	}
	postURL, body, contentType, err := f.render(cfg, req.Message)
	if err != nil {
		f.renderErrs.Add(1)
		f.logger.Warn("notify: render failed",
			pkglogger.String("platform", string(cfg.Platform)),
			pkglogger.String("url", RedactURL(cfg.URL)),
			pkglogger.String("error", RedactError(err)),
		)
		return
	}
	if f.client == nil {
		f.sendErrors.Add(1)
		f.logger.Warn("notify: webhook client not configured")
		return
	}
	if err := f.client.Post(ctx, postURL, contentType, body); err != nil {
		f.sendErrors.Add(1)
		f.logger.Warn("notify: post failed",
			pkglogger.String("platform", string(cfg.Platform)),
			pkglogger.String("url", RedactURL(cfg.URL)),
			pkglogger.String("error", RedactError(err)),
		)
		return
	}
	f.delivered.Add(1)
}

// render dispatches to the per-platform renderer. We wrap errors with a
// platform hint so a regression in one renderer is easy to spot.
func (f *Flusher) render(cfg ChannelConfig, msg contract.NotifyMessage) (string, []byte, string, error) {
	switch cfg.Platform {
	case PlatformDingtalk:
		return RenderDingtalk(cfg, msg, f.now().UnixMilli())
	case PlatformFeishu:
		return RenderFeishu(cfg, msg, f.now().Unix())
	case PlatformSlack:
		return RenderSlack(cfg, msg)
	default:
		return "", nil, "", fmt.Errorf("notify: unsupported platform %q", cfg.Platform)
	}
}

// Metrics returns a snapshot of the flusher's counters. Read-only;
// meant for dashboards / /metrics endpoints.
type Metrics struct {
	Sent        int64
	Delivered   int64
	SendErrors  int64
	ResolveErrs int64
	RenderErrs  int64
	DrainDrops  int64
}

// Metrics returns the current counter values.
// Metrics 处理指标。
func (f *Flusher) Metrics() Metrics {
	if f == nil {
		return Metrics{}
	}
	return Metrics{
		Sent:        f.sent.Load(),
		Delivered:   f.delivered.Load(),
		SendErrors:  f.sendErrors.Load(),
		ResolveErrs: f.resolveErrs.Load(),
		RenderErrs:  f.renderErrs.Load(),
		DrainDrops:  f.drainDrops.Load(),
	}
}

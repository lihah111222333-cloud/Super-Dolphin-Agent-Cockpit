package notify

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/ctxutil"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// DefaultDrainTimeout 限制 shutdown drain 时长，避免退出时被外部 webhook 卡住。
const DefaultDrainTimeout = 5 * time.Second

// Flusher 是通知队列的后台 Runner，负责解析 alias、渲染平台消息并用 SSRF 防护客户端发送。
// 单条发送失败只计数和记录日志，不终止循环，避免一个坏 webhook 阻塞后续通知。
type Flusher struct {
	logger       *slog.Logger
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

// NewFlusher 绑定 notifier 的队列和 resolver；drainTimeout 非正数时使用默认退出排空时间。
func NewFlusher(logger *slog.Logger, notifier *Notifier, client *WebhookClient, drainTimeout time.Duration) *Flusher {
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

// Run 持续消费队列直到 ctx 取消；取消后用独立 background ctx 做有界 drain。
// 如果 select 在取消竞争中先取到队列项，会尽力放回队列，避免用已取消 ctx 中断待发送通知。
func (f *Flusher) Run(ctx context.Context) error {
	if f == nil || f.queue == nil {
		// 无队列时仍阻塞到 ctx 取消，保持 run.Group 的统一停机顺序。
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
				// 取消竞争中已取出的请求尽力放回队列，交给有界 drain ctx 发送。
				// 队列已满说明该请求只能丢弃，drainDrops 会记录这次退出期丢失。
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

// drain 在有界后台 ctx 下排空当前队列；队列为空或超时即退出，不等待新的入队。
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
					slog.Int("remaining", remaining),
				)
			}
			return
		default:
			// 队列已空且未触发超时，本轮退出期排空完成。
			return
		}
	}
}

// handle 处理单条通知请求，所有解析/渲染/发送失败都会计数并继续下一条。
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
			slog.String("alias", req.ChannelAlias),
			slog.String("error", RedactError(err)),
		)
		return
	}
	postURL, body, contentType, err := f.render(cfg, req.Message)
	if err != nil {
		f.renderErrs.Add(1)
		f.logger.Warn("notify: render failed",
			slog.String("platform", string(cfg.Platform)),
			slog.String("url", RedactURL(cfg.URL)),
			slog.String("error", RedactError(err)),
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
			slog.String("platform", string(cfg.Platform)),
			slog.String("url", RedactURL(cfg.URL)),
			slog.String("error", RedactError(err)),
		)
		return
	}
	f.delivered.Add(1)
}

// render 按平台分发到具体 renderer，错误会携带平台信息便于定位单端回归。
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

// Metrics 是 flusher 指标快照，字段只读，用于 dashboard 或 /metrics 暴露。
type Metrics struct {
	Sent        int64
	Delivered   int64
	SendErrors  int64
	ResolveErrs int64
	RenderErrs  int64
	DrainDrops  int64
}

// Metrics 返回当前计数器快照，nil flusher 返回零值。
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

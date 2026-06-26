// Package notify 把平台层的通知能力（webhook 分发、队列、flusher）装配到 Fx 依赖树。
package notify

import (
	"log/slog"
	"time"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platform "github.com/anthropic-ai/super-agent-v3/internal/platform/notify"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// Module 将通知 resolver、webhook client、队列 notifier 和关机 flusher 装配进 Fx。
// ChannelsJSON 在启动期解析，配置错误会阻断应用启动；空配置只生成无操作 resolver，不触网。
// Notifier 同时以具体类型和 contract.MessageNotifier 暴露，flusher 进入 runner group 参与统一关机。
var Module = fx.Module("notify",
	fx.Provide(
		provideResolver,
		provideWebhookClient,
		provideNotifier,
		provideMessageNotifierContract,
		provideFlusher,
	),
	fx.Provide(fx.Annotate(flusherAsRunner, fx.ResultTags(`group:"runners"`))),
)

// provideResolver 从配置解析通知频道，空 JSON 返回无操作 resolver。
func provideResolver(cfg *contract.Config) (platform.Resolver, error) {
	if cfg == nil {
		return platform.ParseChannelsJSON("")
	}
	return platform.ParseChannelsJSON(cfg.Notify.ChannelsJSON)
}

// provideWebhookClient 创建 webhook 客户端，应用配置中的超时和私有 CIDR 许可。
func provideWebhookClient(cfg *contract.Config) *platform.WebhookClient {
	wcfg := platform.WebhookClientConfig{}
	if cfg != nil {
		wcfg.AllowPrivateCIDR = cfg.Notify.AllowPrivateCIDR
		if cfg.Notify.TimeoutSeconds > 0 {
			wcfg.Timeout = time.Duration(cfg.Notify.TimeoutSeconds) * time.Second
		}
	}
	return platform.NewWebhookClient(wcfg)
}

// provideNotifier 创建带队列容量的通知器实例。
func provideNotifier(logger *slog.Logger, cfg *contract.Config, resolver platform.Resolver) *platform.Notifier {
	if logger == nil {
		logger = pkglogger.Get()
	}
	capacity := platform.DefaultQueueCapacity
	if cfg != nil && cfg.Notify.QueueCapacity > 0 {
		capacity = cfg.Notify.QueueCapacity
	}
	return platform.NewNotifier(logger, resolver, capacity)
}

// provideMessageNotifierContract 将 notifier 收窄为公开接口，避免下游绑定平台层具体实现。
func provideMessageNotifierContract(n *platform.Notifier) contract.MessageNotifier { return n }

// provideFlusherParams 是创建通知 flusher 所需的 Fx 依赖集合。
type provideFlusherParams struct {
	fx.In

	Logger   *slog.Logger
	Cfg      *contract.Config
	Notifier *platform.Notifier
	Client   *platform.WebhookClient
}

// provideFlusher 创建 flusher，负责在关机时排空通知队列，超时由配置决定。
func provideFlusher(p provideFlusherParams) *platform.Flusher {
	logger := p.Logger
	if logger == nil {
		logger = pkglogger.Get()
	}
	drain := platform.DefaultDrainTimeout
	if p.Cfg != nil && p.Cfg.Notify.DrainSeconds > 0 {
		drain = time.Duration(p.Cfg.Notify.DrainSeconds) * time.Second
	}
	return platform.NewFlusher(logger, p.Notifier, p.Client, drain)
}

// flusherAsRunner 把 flusher 收窄为 contract.Runner 接口，供 runner group 调度。
func flusherAsRunner(f *platform.Flusher) contract.Runner { return f }

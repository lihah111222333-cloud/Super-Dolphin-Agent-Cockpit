package notify

import (
	"log/slog"
	"time"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platform "github.com/anthropic-ai/super-agent-v3/internal/module/notify/platform"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// Module wires the core-side notify stack into the Fx tree.
//
// Provides:
//   - platform.Resolver parsed from NotifyConfig.ChannelsJSON (empty
//     JSON is a no-op resolver; every TryEnqueue returns
//     ErrNotifyAliasNotFound without trying to reach any network).
//   - *platform.WebhookClient configured with AllowPrivateCIDR /
//     Timeout honoring NotifyConfig.
//   - *Notifier, exposed both as *Notifier (for metrics) and as the
//     contract.MessageNotifier interface (for downstream consumers).
//   - *Flusher, published into the shared group:"runners" slice so
//     platformrunner.RunGroup drives it with the rest of the core
//     Runners.
//
// Startup-time ChannelsJSON parse errors bubble out of fx.New so a
// typo or duplicate alias trips app boot rather than leaking into the
// first TryEnqueue.
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

func provideResolver(cfg *contract.Config) (platform.Resolver, error) {
	if cfg == nil {
		return platform.ParseChannelsJSON("")
	}
	return platform.ParseChannelsJSON(cfg.Notify.ChannelsJSON)
}

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

func provideNotifier(logger *slog.Logger, cfg *contract.Config, resolver platform.Resolver) *Notifier {
	if logger == nil {
		logger = pkglogger.Get()
	}
	capacity := DefaultQueueCapacity
	if cfg != nil && cfg.Notify.QueueCapacity > 0 {
		capacity = cfg.Notify.QueueCapacity
	}
	return NewNotifier(logger, resolver, capacity)
}

// provideMessageNotifierContract narrows *Notifier to the public
// contract.MessageNotifier interface so downstream consumers (cron,
// agent failure handlers, ...) don't bind to the concrete type.
func provideMessageNotifierContract(n *Notifier) contract.MessageNotifier { return n }

func provideFlusher(logger *slog.Logger, cfg *contract.Config, notifier *Notifier, client *platform.WebhookClient) *Flusher {
	if logger == nil {
		logger = pkglogger.Get()
	}
	drain := DefaultDrainTimeout
	if cfg != nil && cfg.Notify.DrainSeconds > 0 {
		drain = time.Duration(cfg.Notify.DrainSeconds) * time.Second
	}
	return NewFlusher(logger, notifier, client, drain)
}

func flusherAsRunner(f *Flusher) contract.Runner { return f }

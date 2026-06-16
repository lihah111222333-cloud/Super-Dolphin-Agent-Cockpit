package notify

import (
	"time"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	pkglogger "github.com/anthropic-ai/super-agent-v3/internal/platform/logging"
	platform "github.com/anthropic-ai/super-agent-v3/internal/platform/notify"
)

// Module wires the core-side notify stack into the Fx tree.
//
// Provides:
//   - platform.Resolver parsed from NotifyConfig.ChannelsJSON (empty
//     JSON is a no-op resolver; every TryEnqueue returns
//     ErrNotifyAliasNotFound without trying to reach any network).
//   - *platform.WebhookClient configured with AllowPrivateCIDR /
//     Timeout honoring NotifyConfig.
//   - *platform.Notifier, exposed both as *platform.Notifier (for metrics)
//     and as the contract.MessageNotifier interface (for downstream
//     consumers).
//   - *platform.Flusher, published into the shared group:"runners" slice so
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

func provideNotifier(logger *pkglogger.Logger, cfg *contract.Config, resolver platform.Resolver) *platform.Notifier {
	if logger == nil {
		logger = pkglogger.Get()
	}
	capacity := platform.DefaultQueueCapacity
	if cfg != nil && cfg.Notify.QueueCapacity > 0 {
		capacity = cfg.Notify.QueueCapacity
	}
	return platform.NewNotifier(logger, resolver, capacity)
}

// provideMessageNotifierContract narrows *platform.Notifier to the public
// contract.MessageNotifier interface so downstream consumers (cron,
// agent failure handlers, ...) don't bind to the concrete type.
func provideMessageNotifierContract(n *platform.Notifier) contract.MessageNotifier { return n }

type provideFlusherParams struct {
	fx.In

	Logger   *pkglogger.Logger
	Cfg      *contract.Config
	Notifier *platform.Notifier
	Client   *platform.WebhookClient
}

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

func flusherAsRunner(f *platform.Flusher) contract.Runner { return f }

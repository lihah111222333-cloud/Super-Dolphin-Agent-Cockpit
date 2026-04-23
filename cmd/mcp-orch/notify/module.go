package notify

import (
	"context"
	"log/slog"
	"time"

	"github.com/kelindar/event"
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	corenotify "github.com/anthropic-ai/super-agent-v3/internal/module/notify"
	notifyplatform "github.com/anthropic-ai/super-agent-v3/internal/module/notify/platform"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// Module wires the orch-side notifier stack. It intentionally does NOT
// import internal/module/notify.Module because that registers a single
// core-side *Notifier into fx — orch needs a separate instance bound
// to its own dispatcher and runners group. We reuse core's notifier /
// flusher constructors directly so the transport / SSRF logic stays in
// one place.
//
// Provides:
//   - notifyplatform.Resolver (parsed from the same NotifyConfig the
//     core module uses; startup-time JSON errors fail fast).
//   - *notifyplatform.WebhookClient configured with AllowPrivateCIDR /
//     Timeout honouring the config.
//   - *corenotify.Notifier exposed as contract.MessageNotifier so the
//     DAG subscriber can inject it.
//   - *corenotify.Flusher published into the shared group:"runners"
//     slice so platformrunner.RunGroup drives the drain lifecycle
//     alongside every other orch Runner.
//   - *DAGNotifier + its Subscribe lifecycle hook.
var Module = fx.Module("orch-notify",
	fx.Provide(
		provideOrchResolver,
		provideOrchWebhookClient,
		provideOrchNotifier,
		provideMessageNotifier,
		provideOrchFlusher,
		NewDAGNotifier,
	),
	fx.Provide(fx.Annotate(flusherAsRunner, fx.ResultTags(`group:"runners"`))),
	fx.Invoke(registerDAGSubscriberLifecycle),
)

func provideOrchResolver(cfg *platformconfig.Config) (notifyplatform.Resolver, error) {
	if cfg == nil {
		return notifyplatform.ParseChannelsJSON("")
	}
	return notifyplatform.ParseChannelsJSON(cfg.Notify.ChannelsJSON)
}

func provideOrchWebhookClient(cfg *platformconfig.Config) *notifyplatform.WebhookClient {
	wcfg := notifyplatform.WebhookClientConfig{}
	if cfg != nil {
		wcfg.AllowPrivateCIDR = cfg.Notify.AllowPrivateCIDR
		if cfg.Notify.TimeoutSeconds > 0 {
			wcfg.Timeout = time.Duration(cfg.Notify.TimeoutSeconds) * time.Second
		}
	}
	return notifyplatform.NewWebhookClient(wcfg)
}

func provideOrchNotifier(logger *slog.Logger, cfg *platformconfig.Config, resolver notifyplatform.Resolver) *corenotify.Notifier {
	if logger == nil {
		logger = pkglogger.Get()
	}
	capacity := corenotify.DefaultQueueCapacity
	if cfg != nil && cfg.Notify.QueueCapacity > 0 {
		capacity = cfg.Notify.QueueCapacity
	}
	return corenotify.NewNotifier(logger, resolver, capacity)
}

func provideMessageNotifier(n *corenotify.Notifier) contract.MessageNotifier { return n }

func provideOrchFlusher(logger *slog.Logger, cfg *platformconfig.Config, notifier *corenotify.Notifier, client *notifyplatform.WebhookClient) *corenotify.Flusher {
	if logger == nil {
		logger = pkglogger.Get()
	}
	drain := corenotify.DefaultDrainTimeout
	if cfg != nil && cfg.Notify.DrainSeconds > 0 {
		drain = time.Duration(cfg.Notify.DrainSeconds) * time.Second
	}
	return corenotify.NewFlusher(logger, notifier, client, drain)
}

func flusherAsRunner(f *corenotify.Flusher) platformrunner.Runner { return f }

type dagSubscribeParams struct {
	fx.In

	Lifecycle  fx.Lifecycle
	Dispatcher *event.Dispatcher
	Notifier   *DAGNotifier
	Logger     *pkglogger.Logger `optional:"true"`
}

// registerDAGSubscriberLifecycle attaches the orch DAG bus subscriber
// at OnStart and cancels it at OnStop. Running the cancel before the
// flusher shuts down would be ideal; platformrunner.RunGroup does so
// naturally because the flusher's drain path sees ctx cancel from the
// same root context as this OnStop hook.
func registerDAGSubscriberLifecycle(p dagSubscribeParams) {
	cancel := func() {}
	p.Lifecycle.Append(fx.Hook{
		OnStart: func(_ context.Context) error {
			cancel = p.Notifier.Subscribe(p.Dispatcher, p.Logger)
			return nil
		},
		OnStop: func(_ context.Context) error {
			cancel()
			cancel = func() {}
			return nil
		},
	})
}

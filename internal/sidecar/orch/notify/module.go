package notify

import (
	"context"
	"log/slog"
	"time"

	"github.com/kelindar/event"
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	notifyplatform "github.com/anthropic-ai/super-agent-v3/internal/platform/notify"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/orchestration"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// Module wires the orch-side notifier stack. It uses the shared
// internal/platform/notify library (Notifier, Flusher, Resolver,
// WebhookClient) so the transport / SSRF logic stays in one place
// without importing internal/module/notify (mcp-service-convention
// S3.1). Orch needs a separate instance bound to its own dispatcher
// and runners group.
//
// Provides:
//   - notifyplatform.Resolver (parsed from the same NotifyConfig the
//     core module uses; startup-time JSON errors fail fast).
//   - *notifyplatform.WebhookClient configured with AllowPrivateCIDR /
//     Timeout honouring the config.
//   - *notifyplatform.Notifier exposed as contract.MessageNotifier so
//     the DAG subscriber can inject it.
//   - *notifyplatform.Flusher published into the shared group:"runners"
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
		provideAgentAliasResolver,
		provideTurnNotifier,
		provideNotifyTap,
		provideDispatchRetryAlertSink,
	),
	fx.Provide(fx.Annotate(flusherAsRunner, fx.ResultTags(`group:"runners"`))),
	fx.Provide(fx.Annotate(dagNotifierAsRunner, fx.ResultTags(`group:"runners"`))),
	fx.Invoke(registerDAGSubscriberLifecycle),
)

// provideAgentAliasResolver hands back the default drop-all resolver.
// Deployments that want per-agent routing replace this via fx.Decorate
// with a real lookup (for example a store-backed one).
func provideAgentAliasResolver() AgentAliasResolver { return dropAllAliasResolver }

func provideTurnNotifier(logger *slog.Logger, notifier contract.MessageNotifier, resolver AgentAliasResolver) *TurnNotifier {
	return NewTurnNotifier(logger, notifier, resolver)
}

// provideNotifyTap narrows *TurnNotifier to the orchestration.NotifyTap
// interface so orchestration.ProvideHookAfterHandler (via the optional
// NotifyTap field on HookAfterHandlerParams) picks it up without binding
// orchestration to the concrete implementation.
func provideNotifyTap(t *TurnNotifier) orchestration.NotifyTap { return t }

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

func provideOrchNotifier(logger *slog.Logger, cfg *platformconfig.Config, resolver notifyplatform.Resolver) *notifyplatform.Notifier {
	if logger == nil {
		logger = pkglogger.Get()
	}
	capacity := notifyplatform.DefaultQueueCapacity
	if cfg != nil && cfg.Notify.QueueCapacity > 0 {
		capacity = cfg.Notify.QueueCapacity
	}
	return notifyplatform.NewNotifier(logger, resolver, capacity)
}

func provideMessageNotifier(n *notifyplatform.Notifier) contract.MessageNotifier { return n }

type provideOrchFlusherParams struct {
	fx.In

	Logger   *slog.Logger
	Cfg      *platformconfig.Config
	Notifier *notifyplatform.Notifier
	Client   *notifyplatform.WebhookClient
}

func provideOrchFlusher(p provideOrchFlusherParams) *notifyplatform.Flusher {
	logger := p.Logger
	if logger == nil {
		logger = pkglogger.Get()
	}
	drain := notifyplatform.DefaultDrainTimeout
	if p.Cfg != nil && p.Cfg.Notify.DrainSeconds > 0 {
		drain = time.Duration(p.Cfg.Notify.DrainSeconds) * time.Second
	}
	return notifyplatform.NewFlusher(logger, p.Notifier, p.Client, drain)
}

func flusherAsRunner(f *notifyplatform.Flusher) platformrunner.Runner { return f }

func dagNotifierAsRunner(n *DAGNotifier) platformrunner.Runner { return n }

type dagSubscribeParams struct {
	fx.In

	Lifecycle  fx.Lifecycle
	Dispatcher *event.Dispatcher
	Notifier   *DAGNotifier
	Logger     *pkglogger.Logger `optional:"true"`
}

// registerDAGSubscriberLifecycle attaches the orch DAG bus subscriber
// at OnStart and cancels it at OnStop. The worker goroutine is now
// managed by run.Group via DAGNotifier.Run (group:"runners"); this
// hook only wires the event subscriptions.
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
